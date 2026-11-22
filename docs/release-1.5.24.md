# ProxyPilot 1.5.24 发布说明（管理后台登录取消人机验证）

日期：2026-11-22
版本：面板 V1.5.24 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

- 管理后台登录不再要求 Cloudflare Turnstile 人机验证：
  - 后台密码登录直接校验账号密码，未携带 Turnstile token 也可登录；
  - 后台邮箱验证码登录发码不再要求 Turnstile；
  - 后台登录页不再加载/弹出 Turnstile 组件，也移除“服务端开启人机验证但前端没有站点密钥”的提示。
- 保留登录失败限速、账号密码校验、TOTP 双因素、OIDC 等安全机制。
- 用户中心（portal）的人机验证逻辑不受影响；Turnstile 配置仍保留给用户中心使用。

## 验证

- `POST /api/login` 未携带 Turnstile：返回 401「用户名或密码错误」，不再返回 403「请完成人机验证」；
- `POST /api/login/email-code` 未携带 Turnstile：正常受理（200，防枚举统一文案）；
- 面板 healthz V1.5.24，15/15 节点在线；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.24。

## 注意

- 这是按用户要求降低后台登录门槛；建议保持强密码、TOTP 和登录限速；
- 如果前面还有 Cloudflare Access / 反向代理的人机验证，需要在那层单独关闭；
- 用户中心仍可能启用 Turnstile，与后台登录相互独立。