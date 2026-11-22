# ProxyPilot 1.5.33 发布说明（管理员用户名修改，含默认超管）

日期：2026-11-22
版本：面板 V1.5.33 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

1. 管理员账号支持修改登录用户名：
   - 非 root 管理员、额外超级管理员均可修改；
   - 默认超管/根超管（PP_SUPER_ADMIN，当前 admin）也可修改用户名；
   - 用户名校验 1–64 位 `[A-Za-z0-9._@-]`，不能与现有账号重复；
   - 改名事务同步 `admin_users`、`admin_oidc_identities`、`admin_email_codes`、`mfa_backup_codes`；
   - root 改名后保持 `is_root=1 / is_super=1`；
   - 修改当前登录账号或 root 用户名后返回 `relogin_required:true`，前端提示重新登录并清除 token。
2. 根超管身份落库：
   - 迁移 `0062_admin_root`：`admin_users.is_root`；
   - `isRootAdmin` 优先 DB `is_root=1`，无 root 行时才回退 `PP_SUPER_ADMIN`；
   - 启动时无 root 行会自动把 PP_SUPER_ADMIN 对应账号补为 root；
   - root 仍不可降级/删除，但可以改名。
3. 前端管理账号操作列新增「修改用户名」：
   - 普通账号改名后刷新列表；
   - 当前账号/root 改名提示重新登录；
   - root 角色 Tag 保持「超管（根）」。

## 验证

- 迁移 0062 已应用，admin_users 含 `is_super/is_root`；
- `/api/auth/me` 对 admin 返回 `is_root=true/is_super=true`；
- 普通账号 shao 改名 `shao-tmp` 后成功，再改回 `shao` 成功；
- root 改名单元测试通过并保持 root 身份；
- 面板 healthz V1.5.33，15/15 节点在线；
- DoH 外部请求仍返回 200 application/dns-message；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.33。

## 注意

- 根超管改名后，旧 `PP_SUPER_ADMIN` 用户名不再代表 root；请使用新用户名登录；
- 旧 JWT 在用户名修改后失效，需要重新登录；
- root 改名后仍需保留新账号，不能删除或降级；
- 改名不会改变密码、MFA、邮箱与角色。