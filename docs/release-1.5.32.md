# ProxyPilot 1.5.32 发布说明（多超级管理员 + 模板页域名列表管理）

日期：2026-11-22
版本：面板 V1.5.32 / Agent 1.4.2 / VPN 客户端 1.4.1

## 一、管理账号支持多个超级管理员

- 新增迁移 `0061_admin_super`：`admin_users.is_super`；
- 超级管理员判定：`PP_SUPER_ADMIN` 根账号 或 DB `is_super=1`；
- 管理账号页面：
  - 列表显示「超管（根）/ 超管 / 管理员」角色标签；
  - 当前超管可对非 root 账号设置/取消超级管理员；
  - root 账号不可降级、不可删除；
  - 当前登录账号不可取消自己的超管；
  - 新建账号时可勾选「设为超级管理员」；
- 新增 `GET /api/auth/me` 返回当前账号 `is_super/is_root`；
- superOnly 接口（管理账号、敏感设置、备份、证书、模板发布/直写等）对 DB 超管同样放行；
- 已验证：admin root 超管、shao 可临时设为超管并恢复普通。

## 二、模板列表页下方新增域名列表管理

- 在「Clash / Mihomo 订阅模板」表格下方新增「域名列表（rule/*.list）」卡片；
- 自动加载并过滤 `rule/` 下 `.list` 文件：
  - direct.list、proxy.list、googleplay.list、korea.list、se_asia.list 等；
- 显示文件 key、大小、CDN URL、编辑操作；
- 点击编辑打开 List 编辑 Drawer，并自动选中对应文件；
- 保存直接写存储桶，保存后自动刷新文件列表；
- 支持刷新；无 R2/读取失败有提示；不提供误删操作。

## 验证

- 面板 healthz V1.5.32，15/15 节点在线；
- 迁移 0061 已应用；
- `/api/auth/me` 返回 root 超管状态；
- 管理账号列表返回 `is_super/is_root`；shao 设置超管成功、恢复普通成功；
- 模板页 API 返回 6 个模板及 5 个 `.list` 文件；
- DoH 外部请求仍返回 200 application/dns-message；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.32；
- 提交以本次发布为准。

## 注意

- 迁移 0061 需在部署时执行；AutoMigrate 开启会自动执行；
- root 账号由 `PP_SUPER_ADMIN` 决定，不可在界面降级/删除；
- 授予超管后该账号可修改敏感设置、管理账号、备份/证书等，请谨慎操作；
- 域名列表编辑需要 R2 配置和写权限。