# ProxyPilot 1.6.19 发布说明（CSP 内联脚本报错修复、nginx 缓冲优化）

日期：2026-11-24
版本：面板 V1.6.19 / Agent 1.4.2 / VPN 客户端 1.4.1

## 修复

### 1. CSP inline script 报错

根因：

- CSP `script-src` 同时包含 `'unsafe-inline'` 和三个 `sha256-...` hash；
- 按 CSP 规范，存在 hash 时 `'unsafe-inline'` 会被浏览器忽略；
- Cloudflare 挑战平台会注入带随机 ray 的内联脚本，无法用固定 hash 匹配，因此持续产生：
  - `Executing inline script violates ... script-src ...`
- 应用自身 HTML 已无可执行内联脚本，可执行脚本均为同源外部文件。

修复：

- 从 `script-src` 移除三个固定 sha256 hash；
- 保留 `'unsafe-inline'`，与 Cloudflare 挑战页共存；
- 保留 `'self'` 与 Cloudflare / GTM 白名单；
- 仍然禁止 `unsafe-eval`，不使用 nonce。

验证：

- CSP 响应头已不再包含 `sha256-...`；
- 内部 `/admin` 返回 200，CSP 头正常；
- 应用脚本仍全部为同源外部文件（`boot.js`/`admin-app.js`/chunks）。

## 优化

### 2. nginx 请求体缓冲

- 为 nginx server 增加：
  - `client_body_buffer_size 1m;`
- 解决 `/api/agent/poll` 等请求频繁写入临时文件：
  - `a client request body is buffered to a temporary file`
- `nginx -t` 通过并已 reload；
- nginx 保持 active。

## 未处理项说明

### 3. 部署 1 秒 upstream connection refused

- 当前部署脚本为 stop → install → start，重启窗口约 1 秒；
- nginx 会短暂返回 upstream connection refused；
- Agent 会自动重试，业务最终恢复；
- 进一步零停机需要蓝绿端口切换或 nginx upstream 动态切换，属于部署架构改造，本次未实施。

### 4. Vendor chunk 拆分

评估结论见：

- `myworkspace/reports/vendor-chunk-eval-report.md`

当前 esbuild 无法 manualChunks，多入口/external 方案会增加体积或破坏 tree-shaking；暂不实施，后续可评估 Vite/Rollup 迁移。

## 验证

- server 测试全绿；
- 面板 healthz V1.6.19，15/15 节点在线；
- CSP 头已移除 sha256 hash，保留 unsafe-inline；
- nginx 配置测试通过、已 reload；
- 全量发布 dist-release 更新 V1.6.19。

## 安全说明

- `'unsafe-inline'` 会放宽内联脚本限制，但这是与 Cloudflare 挑战页共存的必要配置；
- 应用自身仍不包含可执行内联脚本，XSS 防护继续依赖同源外部脚本、输出转义与服务端 CSP；
- 若未来不再使用 Cloudflare 挑战页，可再评估收紧 CSP。