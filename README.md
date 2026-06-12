# ProxmoxUserQuota-Proxy

**[中文]** Proxmox VE 的透明配额代理（Go）。位于用户与 `pveproxy:8006` 之间：除「资源变更」写请求外，一切逐字转发（含 noVNC/SPICE websocket 与 ISO 上传）；对约 15 个写端点做按用户配额审批，fail-closed。

**状态：尚未开工 —— P1 从这里开始**（见 [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)）。

**[English]** The transparent quota-enforcing reverse proxy for Proxmox VE (Go). Sits between users and `pveproxy:8006`: forwards everything verbatim (incl. noVNC/SPICE websockets and ISO uploads) except the ~15 resource-mutating write endpoints, which undergo per-user quota admission. Fail-closed.

**Status: not started — P1 begins here** (see [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)).

## Planned shape (P1+)

- Single static Go binary; declarative quota config (schema draft: [Docs / quota-model.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/quota-model.md))
- `httputil.ReverseProxy`-based pass-through: websocket upgrade support, streaming bodies (no buffering of uploads), `/api2/extjs` + `/api2/json` envelope awareness
- Audit mode first (P2): parse identity (`PVEAuthCookie` / `Authorization`) and classify requests without blocking
- Per-user serialization for quota admission (P4); default-deny whitelist for write endpoints (P6)

## Sibling repositories

- [ProxmoxUserQuota-Docs](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs) — design, decisions, phases
- [ProxmoxUserQuota-Cluster](https://github.com/WilliamLi0623/ProxmoxUserQuota-Cluster) — PVE cluster-side provisioning & verification
