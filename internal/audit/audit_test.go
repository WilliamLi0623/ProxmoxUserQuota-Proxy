package audit

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestParseParamsForm(t *testing.T) {
	body := "cores=4&memory=8192&name=test&scsi0=local-lvm%3A32&net0=virtio%2Crate%3D10"
	p := parseParams("application/x-www-form-urlencoded", []byte(body))
	if p["cores"] != "4" || p["memory"] != "8192" {
		t.Fatalf("cpu/mem wrong: %+v", p)
	}
	if p["scsi0"] != "local-lvm:32" {
		t.Errorf("scsi0=%q want local-lvm:32", p["scsi0"])
	}
	if p["net0"] != "virtio,rate=10" {
		t.Errorf("net0=%q want virtio,rate=10", p["net0"])
	}
}

func TestParseParamsJSON(t *testing.T) {
	body := `{"cores":4,"memory":8192,"full":true,"name":"x"}`
	p := parseParams("application/json", []byte(body))
	if p["cores"] != "4" || p["memory"] != "8192" || p["full"] != "1" {
		t.Fatalf("json parse wrong: %+v", p)
	}
}

func TestResourceParamsFilters(t *testing.T) {
	in := map[string]string{
		"cores": "4", "memory": "8192", "scsi0": "local:32",
		"net0": "virtio,rate=10", "description": "secret notes",
		"password": "hunter2", "name": "vm",
	}
	out := resourceParams(in)
	for _, k := range []string{"cores", "memory", "scsi0", "net0"} {
		if _, ok := out[k]; !ok {
			t.Errorf("missing resource key %s", k)
		}
	}
	for _, k := range []string{"description", "password", "name"} {
		if _, ok := out[k]; ok {
			t.Errorf("leaked non-resource key %s", k)
		}
	}
}

func TestIsResourceKey(t *testing.T) {
	yes := []string{"cores", "memory", "scsi0", "virtio15", "mp3", "net0",
		"unused0", "efidisk0", "tpmstate0", "size", "newid", "target-vmid"}
	no := []string{"description", "password", "name", "net", "scsi", "tags", "sshkeys"}
	for _, k := range yes {
		if !isResourceKey(k) {
			t.Errorf("expected %s to be a resource key", k)
		}
	}
	for _, k := range no {
		if isResourceKey(k) {
			t.Errorf("did not expect %s to be a resource key", k)
		}
	}
}

func TestIsRestore(t *testing.T) {
	if !IsRestore(map[string]string{"archive": "dump/x.vma.zst"}) {
		t.Error("archive should mark restore")
	}
	if !IsRestore(map[string]string{"restore": "1"}) {
		t.Error("restore=1 should mark restore")
	}
	if IsRestore(map[string]string{"cores": "4"}) {
		t.Error("plain create must not be restore")
	}
}

func TestReadAndRestore(t *testing.T) {
	body := "cores=4&memory=8192"
	r, _ := http.NewRequest("PUT", "/", io.NopCloser(strings.NewReader(body)))
	buf, ok := readAndRestore(r)
	if !ok || string(buf) != body {
		t.Fatalf("read buf=%q ok=%v", buf, ok)
	}
	got, _ := io.ReadAll(r.Body)
	if string(got) != body {
		t.Errorf("restored body=%q want %q", got, body)
	}
	if r.ContentLength != int64(len(body)) {
		t.Errorf("content-length=%d want %d", r.ContentLength, len(body))
	}
}

// The audit middleware must forward the request unchanged: the upstream must
// still receive the full body even though audit read it for parsing.
func TestMiddlewareRestoresBody(t *testing.T) {
	called := false
	var gotBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	})
	h := Middleware(next, discard())

	body := "cores=8&memory=16384"
	r, _ := http.NewRequest("PUT", "/api2/json/nodes/n1/qemu/100/config",
		io.NopCloser(strings.NewReader(body)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "PVEAuthCookie=PVE:root@pam:6A::sig")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if !called {
		t.Fatal("next handler was not called")
	}
	if gotBody != body {
		t.Errorf("upstream body=%q want %q (audit must restore the body)", gotBody, body)
	}
}

// Upload bodies must never be buffered, so the body is left untouched for
// streaming.
func TestMiddlewareSkipsUploadBody(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := Middleware(next, discard())
	r, _ := http.NewRequest("POST", "/api2/json/nodes/n1/storage/local/upload",
		io.NopCloser(strings.NewReader("BIGDATA")))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")
	before := r.Body
	h.ServeHTTP(httptest.NewRecorder(), r)
	if r.Body != before {
		t.Error("upload body was replaced; streaming would break")
	}
}
