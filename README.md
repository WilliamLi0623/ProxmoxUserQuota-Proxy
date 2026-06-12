# ProxmoxUserQuota-Proxy

**中文** | [English](README_en.md)

Proxmox VE 的透明配额代理（Go）。位于用户与 `pveproxy:8006` 之间：除「资源变更」写请求外，一切逐字转发（含 noVNC/SPICE websocket 与 ISO 上传）；对约 15 个写端点做按用户配额审批，fail-closed。

**状态：尚未开工 —— P1 从这里开始**（见 [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)）。

## 规划形态（P1+）

- 单个静态 Go 二进制；声明式配额配置（schema 草案见 [Docs / quota-model.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/quota-model.md)）
- 基于 `httputil.ReverseProxy` 的直通：支持 websocket 升级、流式请求体（上传不缓冲），同时适配 `/api2/extjs` 与 `/api2/json` 两种响应信封
- 先做审计模式（P2）：解析身份（`PVEAuthCookie` / `Authorization`）并分类请求，只记录不拦截
- 配额审批按用户串行化（P4）；写端点白名单、未知端点默认拒绝（P6）

## 兄弟仓库

- [ProxmoxUserQuota-Docs](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs) —— 设计、决策、阶段路线
- [ProxmoxUserQuota-Cluster](https://github.com/WilliamLi0623/ProxmoxUserQuota-Cluster) —— PVE 集群侧供给与验证

## 许可证

[MIT](LICENSE)
