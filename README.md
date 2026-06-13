# ProxmoxUserQuota-Proxy

**中文** | [English](README_en.md)

Proxmox VE 的透明配额代理（Go）。位于用户与 `pveproxy:8006` 之间：除「资源变更」写请求外，一切逐字转发（含 noVNC/SPICE websocket 与 ISO 上传）；对约 15 个写端点做按用户配额审批，fail-closed。

**状态：P1 透明直通已在测试集群验证通过** —— 在 PVE 9.2.3 上实测：直连/代理逐字一致（`/api2/json` + `/api2/extjs`）、Cookie 登录与鉴权回环、控制台 websocket 升级（`101`）、ISO 上传流式直通（字节一致）。剩余 P1 退出门槛为整日真实使用与旁路封锁（见 [Docs / phases.md](https://github.com/WilliamLi0623/ProxmoxUserQuota-Docs/blob/main/phases.md)）。

## 构建与运行

    go build -o uq-proxy ./cmd/uq-proxy
    ./uq-proxy -upstream https://<入口节点>:8006 \
      -tls-cert tls.crt -tls-key tls.key \
      -upstream-ca pve-ca.pem    # 测试集群也可改用 -upstream-insecure

| 参数 | 说明 |
|---|---|
| `-listen` | 面向用户的 TLS 监听地址（默认 `:8006`） |
| `-upstream` | 上游 pveproxy 地址（必填） |
| `-tls-cert` / `-tls-key` | 对用户出示的证书与私钥（必填） |
| `-upstream-ca` | 校验上游证书的 CA（PEM）；留空用系统信任库 |
| `-upstream-insecure` | 跳过上游证书校验（仅测试集群） |
| `-admin-listen` | 管理/健康监听，`/healthz`（默认 `127.0.0.1:9090`，置空禁用） |

要求 Go ≥ 1.22，仅标准库。透明性的关键实现：`FlushInterval=-1`（控制台帧、任务日志、上传进度即时转发）、对用户强制 HTTP/1.1（保证 websocket 劫持直通）、访问日志包装器保持可劫持（`Unwrap`/`Hijack`）。systemd 单元见 [`deploy/uq-proxy.service`](deploy/uq-proxy.service)。

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
