# ProxmoxUserQuota-Proxy

[中文](README.md) | **English**

The transparent quota-enforcing reverse proxy for Proxmox VE (Go). Sits between users and `pveproxy:8006`: forwards everything verbatim (incl. noVNC/SPICE websockets and ISO uploads) except the ~15 resource-mutating write endpoints, which undergo per-user quota admission. Fail-closed.

**Status: P1 transparent pass-through validated on a test cluster** — confirmed live on PVE 9.2.3: verbatim direct-vs-proxy parity (`/api2/json` + `/api2/extjs`), cookie login + auth round-trip, console websocket upgrade (`101`), and byte-exact streaming ISO uploads. Remaining P1 exit-gate items are a full day of real usage and bypass lockdown (see [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)).

## Build & Run

    go build -o uq-proxy ./cmd/uq-proxy
    ./uq-proxy -upstream https://<entry-node>:8006 \
      -tls-cert tls.crt -tls-key tls.key \
      -upstream-ca pve-ca.pem    # or -upstream-insecure on test clusters

| Flag | Meaning |
|---|---|
| `-listen` | user-facing TLS listen address (default `:8006`) |
| `-upstream` | upstream pveproxy base URL (required) |
| `-tls-cert` / `-tls-key` | certificate/key served to users (required) |
| `-upstream-ca` | CA bundle (PEM) to verify the upstream; empty = system roots |
| `-upstream-insecure` | skip upstream TLS verification (test clusters only) |
| `-admin-listen` | admin/health listener, `/healthz` (default `127.0.0.1:9090`; empty disables) |

Requires Go ≥ 1.22; standard library only. Transparency-critical details: `FlushInterval=-1` (console frames, task-log tails and upload progress are forwarded immediately), HTTP/1.1 forced towards clients (keeps websocket hijacking on the well-tested path), and a hijack-preserving access-log wrapper (`Unwrap`/`Hijack`). A systemd unit ships in [`deploy/uq-proxy.service`](deploy/uq-proxy.service).

## Planned Shape (P1+)

- Single static Go binary; declarative quota config (schema draft: [Docs / quota-model.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/quota-model.md))
- `httputil.ReverseProxy`-based pass-through: websocket upgrade support, streaming bodies (no buffering of uploads), `/api2/extjs` + `/api2/json` envelope awareness
- Audit mode first (P2): parse identity (`PVEAuthCookie` / `Authorization`) and classify requests without blocking
- Per-user serialization for quota admission (P4); default-deny whitelist for write endpoints (P6)

## Sibling Repositories

- [ProxmoxUserQuota-Docs](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs) — design, decisions, phases
- [ProxmoxUserQuota-Cluster](https://github.com/WilliamLi0623/ProxmoxUserQuota-Cluster) — PVE cluster-side provisioning & verification

## License

[MIT](LICENSE)
