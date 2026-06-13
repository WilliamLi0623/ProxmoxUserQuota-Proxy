// Command uq-proxy is the ProxmoxUserQuota transparent proxy (phases P1–P2).
//
// It sits between users and pveproxy:8006 and forwards everything verbatim,
// including websocket consoles (noVNC/xterm.js/SPICE) and ISO uploads. In P2
// it additionally runs in audit mode: every quota-relevant write is attributed
// to a user and its resource parameters are logged, without ever blocking.
// Quota enforcement arrives in later phases (P4..P6).
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/audit"
	"github.com/WilliamLi0623/ProxmoxUserQuota-Proxy/internal/proxy"
)

const version = "0.2.0-p2"

func main() {
	var (
		listenAddr  = flag.String("listen", ":8006", "TLS listen address for user traffic")
		upstream    = flag.String("upstream", "", "upstream pveproxy base URL, e.g. https://10.0.0.11:8006 (required)")
		tlsCert     = flag.String("tls-cert", "", "PEM certificate served to clients (required)")
		tlsKey      = flag.String("tls-key", "", "PEM private key (required)")
		upstreamCA  = flag.String("upstream-ca", "", "PEM file with CA(s) used to verify the upstream; empty = system roots")
		insecureUp  = flag.Bool("upstream-insecure", false, "skip upstream TLS verification (test clusters only)")
		adminListen = flag.String("admin-listen", "127.0.0.1:9090", "plain-HTTP admin/health listener; empty to disable")
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
		// Order: access log (outermost, times everything) -> audit (attributes
		// quota-relevant writes) -> reverse proxy. Audit is observe-only.
		Handler: proxy.WithAccessLog(audit.Middleware(handler, logger), logger),
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
			logger.Info("admin listener", "addr", *adminListen)
			if err := http.ListenAndServe(*adminListen, mux); err != nil {
				logger.Error("admin listener failed", "err", err)
			}
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
