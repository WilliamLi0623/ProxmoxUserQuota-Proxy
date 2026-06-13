# ProxmoxUserQuota-Proxy

[中文](README.md) | **English**

The transparent quota-enforcing reverse proxy for Proxmox VE (Go). Sits between users and `pveproxy:8006`: forwards everything verbatim (incl. noVNC/SPICE websockets and ISO uploads) except the ~15 resource-mutating write endpoints, which undergo per-user quota admission. Fail-closed.

**Status: P4 core-write quota admission validated on a test cluster** (P1–P3 all validated on PVE 9.2.3). With `-enforce`, over-quota create/config/resize are rejected per the user's quota (PVE-compatible error envelope, readable in the GUI) and pool-membership edits are denied for users; a per-user serialization lock is held until the change is observable in live accounting (a create until its VMID joins the pool, a config/resize until the guest's config changes), defeating PVE's async-task propagation lag so even concurrent floods never overshoot. Without `-enforce` it stays in audit mode (accounting + logging only). See [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md).

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
| `-enforce` | P4: reject over-quota create/config/resize (requires both `-quotas` and `-pve-token-file`); audit-only when unset |

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
