# ProxyPilot 1.5.36 发布说明（系统设置增加存储桶设置）

日期：2026-11-22
版本：面板 V1.5.36 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

- 系统设置「Cloudflare（统一密钥）」Tab 增加「存储桶设置」区域：
  - Bucket 名称（必填）；
  - Endpoint（留空按 Account ID 自动拼接）；
  - CDN 公共地址；
  - Account ID（可选覆盖）；
  - Access Key ID（可选）。
- 字段随「保存设置」一起提交到 `PUT /api/settings`；
- 保存后立即生效；
- 原「存储桶设置已移到订阅模板」提示改为本页说明，同时保留订阅模板页的「对象存储设置」快捷入口；
- 敏感字段（Cloudflare Token 等）处理逻辑不变。

## 验证

- web-antd 构建成功；
- jsdom：
  - Cloudflare Tab 显示存储桶设置 5 个字段；
  - 回填现有 Bucket/CDN 配置；
  - 保存时请求 body 包含全部存储桶字段；
  - 旧「附件管理」提示不存在，无 JS 错误；
- server 测试全绿；
- 面板 healthz V1.5.36，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.36。

## 注意

- Bucket 名称设为必填；从未配置过存储桶的环境，需先填写 Bucket 才能保存系统设置；
- Cloudflare Token 仍需在 Cloudflare Tab 上方配置，并具备 R2 Object Read & Write 权限；
- 订阅模板页的快捷设置与系统设置读写同一组配置。