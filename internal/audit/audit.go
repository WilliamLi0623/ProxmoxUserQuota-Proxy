// Package audit implements P2 audit mode: attribute every quota-relevant
// write to a user, parse the resource-bearing parameters, and emit a
// structured log record. It never blocks and never mutates the forwarded
// request beyond restoring a body it had to read for parsing.
package audit

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/classify"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/identity"
)

// maxBody caps how much of a request body we buffer for parsing. Config
// payloads are tiny; uploads are never parsed (see shouldParseBody).
const maxBody = 256 << 10 // 256 KiB

// Middleware returns a handler that audits quota-relevant writes, then calls
// next. Audit mode is observe-only: reads, pass-through writes and parse
// failures are all forwarded unchanged.
func Middleware(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if res := classify.Classify(r.Method, r.URL.Path); res.QuotaRelevant {
			record(r, res, logger)
		}
		next.ServeHTTP(w, r)
	})
}

func record(r *http.Request, res classify.Result, logger *slog.Logger) {
	id := identity.FromRequest(r)

	var params map[string]string
	if shouldParseBody(r, res) {
		if buf, ok := readAndRestore(r); ok {
			params = parseParams(r.Header.Get("Content-Type"), buf)
		}
	}

	restore := res.Action == classify.ActionGuestCreate && isRestore(params)
	resources := resourceParams(params)

	attrs := []any{
		"event", "quota-write",
		"user", id.User,
		"auth", string(id.Kind),
		"action", string(res.Action),
		"method", r.Method,
		"path", r.URL.Path,
		"envelope", res.Envelope,
	}
	if res.Phase != "" {
		attrs = append(attrs, "phase", res.Phase)
	}
	if id.Token != "" {
		attrs = append(attrs, "token", id.Token)
	}
	if res.GuestKind != "" {
		attrs = append(attrs, "guest", res.GuestKind)
	}
	if res.Node != "" {
		attrs = append(attrs, "node", res.Node)
	}
	if res.VMID != "" {
		attrs = append(attrs, "vmid", res.VMID)
	}
	if res.Storage != "" {
		attrs = append(attrs, "storage", res.Storage)
	}
	if restore {
		attrs = append(attrs, "restore", true)
	}
	if len(resources) > 0 {
		attrs = append(attrs, "resources", resources)
	}
	logger.Info("audit", attrs...)
}

// shouldParseBody is true only for small, config-style bodies. Multipart and
// upload bodies are never buffered so streaming stays intact.
func shouldParseBody(r *http.Request, res classify.Result) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return false
	}
	if res.Action == classify.ActionStorageUpload {
		return false
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		return false
	}
	return true
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (rc readCloser) Close() error { return rc.c.Close() }

// readAndRestore reads up to maxBody bytes, restores r.Body so the upstream
// still receives the full payload, and returns the buffered bytes. If the
// body exceeds maxBody it is restored but not parsed.
func readAndRestore(r *http.Request) ([]byte, bool) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		// Best effort: hand back whatever we read chained to the remainder.
		r.Body = readCloser{io.MultiReader(bytes.NewReader(buf), r.Body), r.Body}
		return nil, false
	}
	if len(buf) > maxBody {
		r.Body = readCloser{io.MultiReader(bytes.NewReader(buf), r.Body), r.Body}
		return nil, false
	}
	r.Body = io.NopCloser(bytes.NewReader(buf))
	r.ContentLength = int64(len(buf))
	r.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(buf)), nil
	}
	return buf, true
}

func parseParams(contentType string, body []byte) map[string]string {
	out := map[string]string{}
	ct := contentType
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))

	if ct == "application/json" || strings.HasSuffix(ct, "+json") {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			for k, v := range m {
				out[k] = scalar(v)
			}
		}
		return out
	}

	// PVE GUI and pvesh post application/x-www-form-urlencoded.
	if vals, err := url.ParseQuery(string(body)); err == nil {
		for k := range vals {
			out[k] = vals.Get(k)
		}
	}
	return out
}

func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "1"
		}
		return "0"
	case nil:
		return ""
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func isRestore(params map[string]string) bool {
	if _, ok := params["archive"]; ok {
		return true
	}
	switch strings.ToLower(params["restore"]) {
	case "1", "true":
		return true
	}
	return false
}

// resourceParams keeps only quota-relevant keys, dropping descriptions,
// passwords, SSH keys and other noise so the audit log stays focused.
func resourceParams(params map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range params {
		if isResourceKey(k) {
			out[k] = v
		}
	}
	return out
}

var resourceKeys = map[string]bool{
	"cores": true, "sockets": true, "vcpus": true, "cpulimit": true,
	"cpuunits": true, "memory": true, "balloon": true, "shares": true,
	"swap": true, "rootfs": true, "size": true, "disk": true,
	"newid": true, "vmid": true, "full": true, "storage": true,
	"target": true, "target-vmid": true, "targetstorage": true,
	"ostemplate": true, "archive": true, "restore": true,
	"content": true, "filename": true, "pool": true, "poolid": true,
	"vms": true, "delete": true, "efidisk0": true, "tpmstate0": true,
}

var indexedPrefixes = []string{"scsi", "virtio", "sata", "ide", "mp", "net", "unused"}

func isResourceKey(k string) bool {
	lk := strings.ToLower(k)
	if resourceKeys[lk] {
		return true
	}
	for _, p := range indexedPrefixes {
		if strings.HasPrefix(lk, p) && allDigits(lk[len(p):]) {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
