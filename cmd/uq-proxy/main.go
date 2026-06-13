// Command uq-proxy is the ProxmoxUserQuota transparent proxy (phases P1–P4).
//
// It sits between users and pveproxy:8006 and forwards everything verbatim,
// including websocket consoles (noVNC/xterm.js/SPICE) and ISO uploads. P2 adds
// audit mode (every quota-relevant write is attributed and logged). P3 adds
// accounting: a read-only service-account client computes live per-user usage
// from pool configs, and a hot-reloaded quota store (quotas.yaml) holds the
// limits. P4 enforces: with -enforce, over-quota create/config/resize are
// rejected (and pool-membership edits denied) under a per-user lock.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/admission"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/proxy"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/pve"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/quota"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/usage"
)

const version = "0.4.0-p4"

func main() {
	var (
		listenAddr  = flag.String("listen", ":8006", "TLS listen address for user traffic")
		upstream    = flag.String("upstream", "", "upstream pveproxy base URL, e.g. https://10.0.0.11:8006 (required)")
		tlsCert     = flag.String("tls-cert", "", "PEM certificate served to clients (required)")
		tlsKey      = flag.String("tls-key", "", "PEM private key (required)")
		upstreamCA  = flag.String("upstream-ca", "", "PEM file with CA(s) used to verify the upstream; empty = system roots")
		insecureUp  = flag.Bool("upstream-insecure", false, "skip upstream TLS verification (test clusters only)")
		adminListen = flag.String("admin-listen", "127.0.0.1:9090", "plain-HTTP admin/health listener; empty to disable")
		quotasPath  = flag.String("quotas", "", "path to quotas.yaml; enables the accounting endpoints")
		pveTokenF   = flag.String("pve-token-file", "", "file with the service-account API token 'uq-proxy@pve!id=secret' for accounting reads")
		enforce     = flag.Bool("enforce", false, "P4: reject over-quota create/config/resize (requires -quotas and -pve-token-file)")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if *upstream == "" || *tlsCert == "" || *tlsKey == "" {
		fmt.Fprintln(os.Stderr, "missing required flags: -upstream, -tls-cert, -tls-key")
		flag.Usage()
		os.Exit(2)
	}
	target, err := url.Parse(*upstream)
	if err != nil || target.Scheme == "" || target.Host == "" {
		logger.Error("invalid -upstream URL", "value", *upstream, "err", err)
		os.Exit(2)
	}

	var caPEM []byte
	if *upstreamCA != "" {
		caPEM, err = os.ReadFile(*upstreamCA)
		if err != nil {
			logger.Error("reading -upstream-ca", "err", err)
			os.Exit(2)
		}
	}

	// P3 accounting (optional): quota store + read-only service-account client.
	var (
		quotaStore *quota.Store
		engine     *usage.Engine
	)
	if *quotasPath != "" {
		quotaStore, err = quota.Open(*quotasPath, logger)
		if err != nil {
			logger.Error("loading quotas", "path", *quotasPath, "err", err)
			os.Exit(2)
		}
		logger.Info("quota store loaded", "path", *quotasPath, "users", len(quotaStore.Users()))
	}
	if *pveTokenF != "" {
		tok, rerr := os.ReadFile(*pveTokenF)
		if rerr != nil {
			logger.Error("reading -pve-token-file", "err", rerr)
			os.Exit(2)
		}
		pveTLS := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: *insecureUp}
		if len(caPEM) > 0 {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caPEM) {
				logger.Error("upstream CA: no certificates parsed")
				os.Exit(2)
			}
			pveTLS.RootCAs = pool
		}
		engine = usage.NewEngine(pve.New(target, strings.TrimSpace(string(tok)), pveTLS))
		logger.Info("accounting client ready", "upstream", target.String())
	}

	if *enforce && (quotaStore == nil || engine == nil) {
		fmt.Fprintln(os.Stderr, "-enforce requires both -quotas and -pve-token-file")
		os.Exit(2)
	}
	var admins []string
	if quotaStore != nil {
		admins = quotaStore.Admins()
	}
	enforcer := admission.New(quotaStore, engine, admins, *enforce, logger)
	logger.Info("admission middleware", "enforce", *enforce, "admins", len(admins))

	handler, err := proxy.New(proxy.Options{
		Upstream:         target,
		UpstreamCAPEM:    caPEM,
		InsecureUpstream: *insecureUp,
		Logger:           logger,
	})
	if err != nil {
		logger.Error("building proxy", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		Addr: *listenAddr,
		// Order: access log (outermost) -> admission (audits every quota write,
		// and when -enforce is set rejects over-quota core writes) -> proxy.
		Handler: proxy.WithAccessLog(enforcer.Middleware(handler), logger),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// Force HTTP/1.1 towards clients: pveproxy is HTTP/1.1-only anyway,
			// and hijack-based websocket pass-through (noVNC/xterm.js) is only
			// defined for HTTP/1.1 connections.
			NextProtos: []string{"http/1.1"},
		},
		// Deliberately no Read/Write timeouts: consoles, task-log streams and
		// ISO uploads are long-lived. Header reads are bounded; idle
		// keep-alive connections are reaped.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	if *adminListen != "" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, "ok uq-proxy %s\n", version)
			})
			mux.HandleFunc("/usage", usageHandler(quotaStore, engine))
			logger.Info("admin listener", "addr", *adminListen)
			if err := http.ListenAndServe(*adminListen, mux); err != nil {
				logger.Error("admin listener failed", "err", err)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if quotaStore != nil {
		go quotaStore.Watch(ctx, 5*time.Second)
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("uq-proxy listening", "version", version, "listen", *listenAddr, "upstream", target.String())
	if err := srv.ListenAndServeTLS(*tlsCert, *tlsKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
	logger.Info("shut down cleanly")
}

// usageHandler reports computed usage vs configured quota per user (optionally
// filtered by ?user=). P3 is accounting-only: this local admin endpoint is how
// the exit gate is verified against a manual inventory. P4 reuses the engine
// and store for admission decisions.
func usageHandler(store *quota.Store, engine *usage.Engine) http.HandlerFunc {
	type entry struct {
		User    string           `json:"user"`
		Pool    string           `json:"pool,omitempty"`
		Usage   usage.Usage      `json:"usage"`
		DiskGiB map[string]int64 `json:"disk_gib,omitempty"`
		Quota   *quota.UserQuota `json:"quota,omitempty"`
		Error   string           `json:"error,omitempty"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil || engine == nil {
			http.Error(w, "accounting not configured (need -quotas and -pve-token-file)",
				http.StatusServiceUnavailable)
			return
		}
		users := store.Users()
		if u := r.URL.Query().Get("user"); u != "" {
			users = []string{u}
		}
		out := make([]entry, 0, len(users))
		for _, user := range users {
			e := entry{User: user}
			q, ok := store.Get(user)
			if !ok {
				e.Error = "no quota record (default deny)"
				out = append(out, e)
				continue
			}
			e.Pool = q.Pool
			e.Quota = q
			if u, err := engine.UserUsage(q.Pool); err != nil {
				e.Error = err.Error()
			} else {
				e.Usage = u
				e.DiskGiB = u.DiskGiB()
			}
			out = append(out, e)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}
