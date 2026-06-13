# ProxmoxUserQuota-Proxy

[中文](README.md) | **English**

The transparent quota-enforcing reverse proxy for Proxmox VE (Go). Sits between users and `pveproxy:8006`: forwards everything verbatim (incl. noVNC/SPICE websockets and ISO uploads) except the ~15 resource-mutating write endpoints, which undergo per-user quota admission. Fail-closed.

**Status: P3 accounting in progress** (P1 transparent pass-through and P2 audit mode are both validated on a PVE 9.2.3 test cluster). P3 adds a read-only service-account client that computes live per-user usage from pool configs (`cores` / `memory` / `disk.<storage>` incl. `unused[n]` / `instances`), a `quotas.yaml` store (default-deny, validated, hot-reloaded), exposed via the `/usage` admin endpoint. It does **not** block yet — enforcement is P4 (see [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)).

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
| `-admin-listen` | admin/health listener, `/healthz` + `/usage` (default `127.0.0.1:9090`; empty disables) |
| `-quotas` | path to `quotas.yaml`; enables the accounting endpoints (hot-reloaded) |
| `-pve-token-file` | file holding the service-account API token (`uq-proxy@pve!id=secret`), used only for read-only accounting queries |

Requires Go ≥ 1.22; standard library only, except `gopkg.in/yaml.v3` for parsing the quota config. Transparency-critical details: `FlushInterval=-1` (console frames, task-log tails and upload progress are forwarded immediately), HTTP/1.1 forced towards clients (keeps websocket hijacking on the well-tested path), and a hijack-preserving access-log wrapper (`Unwrap`/`Hijack`). A systemd unit ships in [`deploy/uq-proxy.service`](deploy/uq-proxy.service).

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
