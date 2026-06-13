// Package proxy implements the transparent pass-through core (phase P1).
package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Options configures the pass-through proxy.
type Options struct {
	Upstream         *url.URL
	UpstreamCAPEM    []byte // optional PEM bundle for upstream verification
	InsecureUpstream bool   // test clusters only
	Logger           *slog.Logger
}

// New returns the verbatim reverse-proxy handler. Invariant 2 of the design:
// everything is forwarded as-is — no rewriting beyond the standard
// X-Forwarded-* headers, no request or response buffering.
func New(opts Options) (http.Handler, error) {
	tlsCfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: opts.InsecureUpstream,
	}
	if len(opts.UpstreamCAPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(opts.UpstreamCAPEM) {
			return nil, fmt.Errorf("upstream CA: no certificates parsed")
		}
		tlsCfg.RootCAs = pool
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
		// pveproxy speaks HTTP/1.1; plain HTTP/1.1 keeps Upgrade (websocket)
		// handling on the well-tested path.
		ForceAttemptHTTP2:     false,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
		// No ResponseHeaderTimeout: task logs and consoles are long-lived.
	}

	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(opts.Upstream) // also rewrites the outbound Host header
			pr.SetXForwarded()       // X-Forwarded-For/Host/Proto (OIDC redirects)
		},
		Transport: transport,
		// Flush response bytes immediately: noVNC frames, task-log tails and
		// upload progress must never sit in a buffer.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			opts.Logger.Error("upstream error", "method", r.Method, "path", r.URL.Path, "err", err)
			http.Error(w, "uq-proxy: upstream unavailable", http.StatusBadGateway)
		},
	}
	return rp, nil
}
