# ProxyPilot 1.4.3 发布说明（登录页人机验证交互）

日期：2026-11-19
版本：面板 V1.4.3 / Agent 1.4.1 / VPN 客户端 1.4.1

## 变更

1. **管理后台登录页改为点击后弹窗验证**
   - 移除页面常驻 Turnstile 控件；
   - 点击“登录 / 发送登录验证码 / 重新发送”时，若未完成验证则弹出「人机验证」Modal（与用户中心一致）；
   - 验证通过后自动继续密码登录或发送验证码；密码登录保留 MFA/TOTP 流程；
   - 关闭弹窗清空 pending 并重置 Turnstile token。

2. **SSO 登录不做人机验证**
   - 管理端 `/api/oidc/login` 与用户端 `/api/portal/oidc/login` 不再强制 Turnstile；
   - 两个登录页点击 SSO 直接跳转 IdP，不弹验证框、不带 cf_turnstile_response；
   - OIDC state/nonce/PKCE/回调校验保持不变；
   - 密码登录、邮箱验证码发送仍保留 Turnstile 校验。

## 验证

- 管理端登录页无内联 Turnstile；点击登录弹出「人机验证」Modal，验证后自动登录；
- SSO 按钮直接跳转 `sso.yangzihome.space`，无弹窗、无前端 Turnstile 请求；
- server `go build/vet/test` 全绿；web-antd `node scripts/build.mjs` 成功；
- 面板 healthz V1.4.3，15/15 节点在线。

## 注意

- SSO 取消 Turnstile 后，OIDC 入口的被刷风险由 IdP 侧与现有登录限制承担；如后续需要可只在异常频率下加限制。
- 管理端/用户端密码与邮箱验证码仍必须通过 Turnstile。