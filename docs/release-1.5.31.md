# ProxyPilot 1.5.31 发布说明（模板编辑页 List 编辑）

日期：2026-11-22
版本：面板 V1.5.31 / Agent 1.4.2 / VPN 客户端 1.4.1

## 变更

- 模板编辑页顶部操作行新增「List 编辑」入口；
- 打开独立 Drawer，不占用模板编辑区域；
- 可直接编辑 `rule/configs/` 上一级目录 `rule/` 下的 `.list` 文件：
  - `direct.list`；
  - `proxy.list`；
  - `googleplay.list`；
  - `korea.list`；
  - `se_asia.list`；
  - 以及后续新增的 `.list` 文件。
- List 编辑能力：
  - 文件下拉选择/搜索；
  - 显示文件 key、大小、CDN URL、可编辑状态；
  - 带行号等宽文本编辑器；
  - 保留注释、空行和顺序；
  - 简单非法行提示（不阻止保存）；
  - 保存直接写回存储桶并提示成功；
  - 支持重新加载/关闭；
  - R2 未配置/读取失败有提示。
- 与模板编辑、存储桶直存、DoH 等功能互不影响。

## 验证

- web-antd 构建成功；
- jsdom：
  - List 编辑按钮存在并能打开独立 Drawer；
  - 下拉加载 `rule/*.list`；
  - 选择后显示内容、key、大小、CDN URL；
  - 非法行提示；
  - 保存 PUT 提交 key/content/content_type；
  - 无 R2/读取失败提示；
- server 测试全绿；
- 面板 healthz V1.5.31，15/15 节点在线；
- 全量发布 dist-release 更新 V1.5.31。

## 注意

- 当前 List 编辑只处理 `rule/` 目录下的 `.list` 文件，不处理 `rule/configs/*.yaml` 模板；
- 保存需要 R2 配置和写权限；
- 列表文件修改后立即生效，模板无需重新保存；
- 非法行仅为提示，避免误伤特殊规则；保存前建议检查提示。