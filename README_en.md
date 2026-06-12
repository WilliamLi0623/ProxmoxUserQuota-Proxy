# ProxmoxUserQuota-Proxy

[中文](README.md) | **English**

The transparent quota-enforcing reverse proxy for Proxmox VE (Go). Sits between users and `pveproxy:8006`: forwards everything verbatim (incl. noVNC/SPICE websockets and ISO uploads) except the ~15 resource-mutating write endpoints, which undergo per-user quota admission. Fail-closed.

**Status: Not Started — P1 Begins Here** (see [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)).

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
