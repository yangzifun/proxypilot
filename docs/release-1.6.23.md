# ProxyPilot 1.6.23 发布说明（证书生命周期、域名拆分、批量状态）

日期：2026-11-24
版本：面板 V1.6.23 / Agent 1.4.2 / VPN 客户端 1.4.1

## 本轮完成

### 1. 节点域名去重与 DNS

- server_id=20（洛杉矶2）的 sing-box 域名由 `lax1-node.yzfn.top` 改为 `lax2-node.yzfn.top`；
- DNS A 记录已存在且为 DNS only：
  - `lax2-node.yzfn.top → 104.223.73.43`
- 新增 superOnly DNS 管理接口：
  - `POST /api/vpn/cert/dns`
  - `GET /api/vpn/cert/dns?name=`
- 链路验证：
  - `lax1-node.yzfn.top` TLS 握手，CN=yzfn.top，Verification OK；
  - `lax2-node.yzfn.top` TLS 握手，CN=yzfn.top，Verification OK；
  - 节点链接 SNI 已切换为 `lax2-node.yzfn.top`，sing-box 配置已 apply。

### 2. 证书状态回采

- 迁移 0064：
  - `servers.cert_status`
  - `cert_fingerprint`
  - `cert_synced_at`
  - `cert_error`
- Agent 证书任务回执解析 `singbox_sha256`/指纹并写库；
- `/api/vpn/cert/nodes` 返回真实状态，不再 unknown；
- 当前 9 台已同步，6 台 pending（Agent 重启/离线，cron 会重试）。

### 3. 离线自动重试

- cron 每 10 分钟检查：
  - 节点在线；
  - 证书状态空/failed/pending；
  - 超过 30 分钟冷却；
- 自动重新排队证书任务；
- 不会重复刷已成功且证书未过期的节点。

### 4. 证书自动续期

- cron 每日检查 `not_after`；
- 剩余 < 30 天自动续期；
- 续期成功自动全量下发；
- 续期失败不覆盖旧证书，只告警；
- 使用 TryLock + last_renew_at 防重复。

### 5. 批量下发逐节点状态

- apply-all 批任务不再提前标记 success；
- 任务项先写 `queued`，由 Agent 回执回填：
  - `success`
  - `failed`
  - `timeout`
- 全部回执后聚合 job：
  - success / partial / failed；
- 离线/超时不算成功。

## 关键修复

- 发现 cron 进程的 handlers 未加载 KEK（`box`），导致自动重试读取证书私钥为空，部分节点 private.key 被写成 0 字节；
- 修复：`StartCronFor` 加载 `LoadSecretBox`；
- 取消旧的异常证书任务，重新下发有效证书；
- 已恢复节点证书：
  - cert 4800 字节；
  - key 227 字节；
  - 指纹 `34486A18...`；
  - TLS 握手 Verification OK。

## 验证

- server 构建、测试全绿；
- web-antd 构建成功，render-check 38/0；
- 面板 healthz V1.6.23；
- 证书状态 API 已返回真实 synced/pending；
- 域名与 DNS 变更已执行；
- 全量发布 dist-release 更新 V1.6.23；
- 提交：见 git log。

## 剩余

- 6 台 pending 节点等待 Agent 重启/离线恢复，cron 将自动重试；
- 本机无 `proxypilot-agent`，本机 VPN 下发项会显示失败，不影响远端节点；
- 自动续期尚未有临期证书可实测，仅单元测试覆盖；
- 如需继续，可在节点恢复后核对全部 15 台 synced。