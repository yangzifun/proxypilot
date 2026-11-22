# ProxyPilot 1.5.27 发布说明（模板地址改为存储桶文件、编辑直存）

日期：2026-11-22
版本：面板 V1.5.27 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

1. 模板地址不再需要填写 CDN URL
   - 新建/编辑模板改为从附件管理选择存储桶文件；
   - API 接受 `bucket_key`（如 `rule/configs/global_config.yaml`），后端自动推导受管存储桶 URL；
   - 仅允许 `rule/configs/*.yaml|yml`，外部 URL 和任意 CDN 地址拒绝；
   - 列表/详情/内容接口返回 `bucket_key`，前端显示「存储桶文件」。
2. 编辑内容直接写存储桶
   - `PUT /api/templates/manage/{id}/content` 校验后直接写入模板对应 bucket 文件；
   - 同步更新 DB 草稿/已发布内容、版本历史，返回 `source:"bucket"`、`sha256`、`url`、`bucket_key`、`version`；
   - 保存即生效，不再必须额外点「发布」；发布接口保留为版本快照兼容。
3. 用户页面订阅继续直接读取存储桶
   - `/clash-config?uuid=...&template=...` 使用模板 bucket URL 内容，`X-Template-Source: url`；
   - 附件管理编辑/上传/删除后清空模板缓存，下一次订阅立即读到新内容。
4. DoH 服务保持可用（V1.5.25 已上线）。

## 验证

- 模板列表返回 `bucket_key`：
  - `rule/configs/global_full_config.yaml` 等；
- 新建临时模板仅提交 `bucket_key` 成功：
  - 自动得到 `https://s3.proxypilot.yangzifun.org/rule/configs/lite_config.yaml`；
- 内容保存返回 `source:"bucket"`，写入 bucket 并同步版本；
- 模板内容读取 `source:"url"`，指向 bucket 文件；
- 用户 `/clash-config` 返回 `X-Template-Source: url`，约 89 KB YAML；
- 临时模板已删除；
- 面板 healthz V1.5.27，15/15 节点在线；
- server 测试全绿，web-antd 构建成功；
- 全量发布 dist-release 更新 V1.5.27。

## 注意

- 模板必须绑定受管存储桶文件 `rule/configs/*.yaml|yml`；
- 编辑内容直接写 bucket，需 R2 已配置且有写权限；
- 旧外部 URL 模板无法直写 bucket，需迁移到受管存储桶；
- 发布按钮保留用于版本快照，不再影响即时生效。