# ProxyPilot 1.5.26 发布说明（订阅模板直接驱动存储桶）

日期：2026-11-22
版本：面板 V1.5.26 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

1. 订阅模板内容以存储桶文件为唯一来源：
   - 模板编辑器内容读取优先直接拉取模板 URL 指向的 S3/R2 文件；
   - 附件管理中对 `rule/configs/*.yaml` 的修改会直接反映到模板编辑器；
   - DB 草稿/已发布副本仅在 URL 暂时不可用时作为回退，保留版本历史能力。
2. 用户页面订阅直接驱动存储桶：
   - `/clash-config?uuid=...&template=...` 不再优先使用 DB `published_content`；
   - 始终拉取模板 URL 指向的存储桶文件后再注入用户节点；
   - 响应头 `X-Template-Source: url`，`X-Template-Used` 为对应存储桶地址。
3. 附件编辑/上传/删除后立即清空模板 URL 缓存：
   - 附件管理修改模板文件后，下一次模板编辑/用户订阅即可读取新内容；
   - 不再等待 5 分钟缓存过期。
4. DoH 服务保持正常（面板 V1.5.25 已上线）。

## 验证

- 模板编辑器内容接口 `source=url`，URL 指向 `s3.proxypilot.yangzifun.org/rule/configs/...`；
- `/clash-config?uuid=<uuid>&template=global_config.yaml`：
  - HTTP 200；
  - `X-Template-Source: url`；
  - `X-Template-Used` 为存储桶地址；
  - 返回 YAML 约 89 KB；
- 模板内容与附件管理中的存储桶文件一致；
- 面板 healthz V1.5.26，15/15 节点在线；
- server 测试全绿；
- 全量发布 dist-release 更新 V1.5.26。

## 注意

- 模板 URL 必须是受管存储桶地址（`s3.proxypilot.yangzifun.org/rule/configs/*.yaml|yml`）才能发布/编辑；
- 附件管理修改后无需重新发布模板，用户订阅直接读取新文件；
- 若存储桶暂时不可用，编辑器回退 DB 草稿/已发布副本，但用户订阅仍以 URL 为准；
- 建议保留模板版本历史，避免误改附件无法回滚。