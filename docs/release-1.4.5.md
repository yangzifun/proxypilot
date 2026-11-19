# ProxyPilot 1.4.5 发布说明（登录页 CSP/人机验证）

日期：2026-11-19
版本：面板 V1.4.5 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

1. 管理后台登录页改为点击后再弹窗人机验证（与用户中心一致）：
   - 页面不再常驻 Cloudflare Turnstile 控件；
   - 点击登录/发送验证码/重新发送时才弹「人机验证」Modal，通过后自动继续；
   - 密码登录保留 MFA/TOTP。
2. SSO 登录取消人机验证：
   - 管理端/用户端 OIDC 入口不再强制 Turnstile；
   - 两个登录页点击 SSO 直接跳转 IdP，不弹验证框；
   - OIDC state/nonce/PKCE/回调校验保持不变。
3. CSP 兼容 Cloudflare 挑战平台/GTM：
   - 放行 googletagmanager/google-analytics 与 Cloudflare 挑战平台注入脚本；
   - 保留 `unsafe-inline`（Cloudflare 挑战脚本带随机 ray，无法用 hash/nonce 固定），消除控制台 CSP 报错；
   - 这是与 Cloudflare 挑战页共存的必要放宽，XSS 防护由 React 转义、外部脚本白名单与其它安全头共同承担。

## 验证

- 面板 healthz V1.4.5，15/15 节点在线；
- 管理端登录页无内联 Turnstile；点击登录弹「人机验证」；SSO 按钮直接跳转 IdP；
- server `go build/vet/test` 全绿；web-antd 构建成功。

## 注意

- `unsafe-inline` 会削弱 CSP 对 HTML 注入的防护；如后续不再使用 Cloudflare 挑战平台/GTM，可移除。
- 密码/邮箱验证码仍要求 Turnstile；SSO 不再要求。