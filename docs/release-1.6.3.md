# ProxyPilot 1.6.3 发布说明（功能优化批次）

日期：2026-11-24
版本：面板 V1.6.3 / Agent 1.4.2 / VPN 客户端 1.4.1

## 安全与稳定性

1. DoH 拒绝未注册 UUID
   - 现在仅 `uuid_info.status=active` 的 UUID 可使用 DoH；
   - 无记录 UUID 返回 403「UUID 不存在或未授权」；
   - 不访问上游、不写入成功统计；
   - inactive / suspended / pending / 空状态统一拒绝；
   - 实测：有效 UUID 200，随机 UUID 403。

2. 前端全局 ErrorBoundary
   - 路由内容外层新增 PageErrorBoundary；
   - 页面组件运行时异常时显示错误页，不再整站白屏；
   - 提供重试、重新加载、返回系统概览；
   - 路由切换自动重置错误状态。

## 功能补全

3. DoH 后台统计
   - 系统设置「DoH 服务」Tab 增加统计区：
     - 近 30 天查询量；
     - 近 24 小时查询量；
     - 活跃 UUID 数；
     - 近 30 天字节数；
     - Top 10 UUID 查询表；
   - 支持刷新、加载态、错误重试、空态。

4. 新建模板 YAML 文件
   - 订阅模板页新增「新建模板文件」；
   - 自动补 `.yaml`，校验文件名与重名；
   - 直接写入 `rule/configs/<name>.yaml`；
   - 创建后自动打开「新建模板」表单并预填名称/存储桶文件。

5. 域名列表增强
   - 新增「下载」：下载 `.list` 文件内容；
   - 新增「复制」：生成 `<原名>-copy.list` 唯一副本并打开编辑器；
   - 仍不提供删除，避免误删。

6. 分页体验
   - GroupDetail 受控分页接入 `info.recommendPage`；
   - 与订阅用户、订阅模板保持一致。

## 运维优化

- `/root/deploy-panel.sh` 部署抽查接口从已失效的 `/api/mail/settings` 改为 `/api/settings`，避免部署日志误报 404。

## 验证

- `node scripts/build.mjs` 成功；
- server 构建、测试全绿；
- DoH 有效 UUID 200，未注册 UUID 403；
- `/api/doh/stats` 返回真实 UUID 聚合数据；
- 临时模板 YAML 创建/读取/删除成功；
- 面板 healthz V1.6.3，15/15 节点在线；
- 全量发布 dist-release 更新 V1.6.3。

## 后续可继续优化

- 前端路由级代码拆分（当前 app.js 约 2.3 MB）；
- render-check 8 项陈旧断言修复；
- 侧边栏主题变量化（当前 5 套主题侧边栏保持深色）；
- 模板编辑器/大页面按需加载。