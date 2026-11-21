# ProxyPilot 1.5.19 发布说明（模板版本保存后回显）

日期：2026-11-21
版本：面板 V1.5.19 / Agent 1.4.2 / VPN 客户端 1.4.1

## 问题

在 sing-box 模板编辑器选择存储桶版本后保存，再次打开模板版本为空：

- 前端保存走 `/api/singbox/templates/from-config`；
- 该接口构造模板字段时漏掉了 `singbox_version`；
- `normalizeSingboxTemplateInput` 收到空版本，数据库写空；
- 重新打开时读取到空值，显示未选择。

## 修复

- `singboxTemplateFromConfig` 的模板输入增加：
  `singbox_version: strings.TrimSpace(body["singbox_version"])`
- 新增单测 `TestFromConfigSavesSingboxVersion`，校验 from-config 写入版本。
- 版本选择仍按桶版本索引（1.14.4 / 1.15.0-alpha.13），保存后下发时按 `install_versions` 安装对应 S3 版本。

## 验证

- 新建临时模板 `t59-version-check`，保存 `singbox_version=1.15.0-alpha.13`；
- 重新 GET 模板：`singbox_version=1.15.0-alpha.13`，回显正确；
- 临时模板已删除；
- 面板 healthz V1.5.19，15/15 节点在线；
- server 测试全绿；
- 全量发布 dist-release 更新 V1.5.19。

## 注意

- 本次只修版本字段保存/回显；选择版本后的实际安装仍依赖下发，已由 1.5.18 的 install_versions 修复保证；
- 模板版本留空时使用桶默认 1.14.4。