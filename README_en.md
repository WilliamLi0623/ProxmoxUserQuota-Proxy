# ProxmoxUserQuota-Proxy

[中文](README.md) | **English**

The transparent quota-enforcing reverse proxy for Proxmox VE (Go). Sits between users and `pveproxy:8006`: forwards everything verbatim (incl. noVNC/SPICE websockets and ISO uploads) except the ~15 resource-mutating write endpoints, which undergo per-user quota admission. Fail-closed.

**Status: P6 hardening (proxy side) validated on a test cluster** (P1–P5 all validated on PVE 9.2.3). `-enforce` rejects over-quota create/config/resize and the side doors (clone/restore/move-disk/rollback); P6 adds `-fail-closed` (deny quota writes when the accounting backend is unreachable, reads keep flowing), `-default-deny` (reject write endpoints absent from the known intercept/pass-through tables), `/metrics` (admission counters), and an `over_quota` flag on `/usage` for a reconciliation cron. Still operational: storage-layer hard quotas (ZFS/Ceph), HA active/passive, and the PVE-upgrade API re-diff runbook. Without `-enforce` it stays in audit mode. See [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md).

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
| `-admin-listen` | admin/health listener, `/healthz` + `/usage` + `/metrics` (default `127.0.0.1:9090`; empty disables) |
| `-quotas` | path to `quotas.yaml`; enables the accounting endpoints (hot-reloaded) |
| `-pve-token-file` | file holding the service-account API token (`uq-proxy@pve!id=secret`), used only for read-only accounting queries |
| `-enforce` | P4: reject over-quota create/config/resize (requires both `-quotas` and `-pve-token-file`); audit-only when unset |
| `-fail-closed` | P6: deny quota-relevant writes when accounting reads fail (requires `-enforce`) |
| `-default-deny` | P6: deny write endpoints not in the known intercept/pass-through tables (requires `-enforce`) |

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
