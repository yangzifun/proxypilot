# 老后台 UI 能力（按卡片抽取）

## dash

**标题**：系统概览 ｜ 热门订阅（Top 10） ｜ 最新订阅（最近 10 条访问）

**按钮**（0）：—

**onclick 处理**（0）：—

## status

**标题**：服务器监控

**按钮**（1）：`通知设置`

**onclick 处理**（1）：`openMonitorNotify`

## uuids

**标题**：订阅分组 ｜ 配置管理（当前 UUID： - ） ｜ 外部订阅源（合并到该 UUID 的订阅）

**按钮**（8）：`上一页`、`下一页`、`加载`、`一键清理全部`、`删除选中`、`添加节点配置`、`添加`、`关闭配置管理`

**onclick 处理**（8）：`sortUuids`、`changeUuidPage`、`clearAllConfigs`、`delSelectedConfigs`、`openConfigModal`、`changeConfigPage`、`addSubSourceAdmin`、`closeConfigPanel`

## tpls

**标题**：订阅模板

**按钮**（5）：`添加模板`、`上一页`、`下一页`、`刷新`、`新建文件`

**onclick 处理**（5）：`openTplModal`、`sortTpls`、`changeTplPage`、`loadBucketFiles`、`createBucketFile`

## sys

**标题**：系统管理 ｜ 用户管理 ｜ 访问日志维护 ｜ API 密钥

**按钮**（4）：`新增用户`、`预览待删除数量`、`清理日志`、`新增密钥`

**onclick 处理**（4）：`openUserModal`、`previewLogCleanup`、`openLogCleanupModal`、`openApiKeyModal`

## sec

**标题**：安全中心 ｜ 我的邮箱（OIDC 单点登录） ｜ 双重验证 (MFA) ｜ 修改密码

**按钮**（3）：`绑定 / 验证邮箱`、`解绑邮箱`、`修改密码`

**onclick 处理**（3）：`openEmailModal`、`unbindEmail`、`openPwdModal`

## singbox

**标题**：协议配置 ｜ 节点配置

**按钮**（5）：`新增协议模板`、`协议引导`、`UUID 一致性巡检`、`一键下发模板`、`绑定节点`

**onclick 处理**（5）：`openSingboxTemplateModal`、`openSingboxGuide`、`runUuidReconcile`、`applyAllSingboxNodes`、`openSingboxNodeModal`

## logs

**标题**：日志管理

**按钮**（0）：—

**onclick 处理**（0）：—

## servers

**标题**：服务器管理 ｜ 服务器列表 ｜ 编辑 UUID 信息 ｜ 授权节点 ｜ 服务器上下线通知 ｜ 修改密码 ｜ 开启双重验证 ｜ 请妥善保管备份码 ｜ 绑定 / 验证邮箱 ｜ 清理访问日志 ｜ 新增 API 密钥 ｜ 渲染预览（尚未下发） ｜ 协议引导

**按钮**（28）：`一键更新 agent`、`新增服务器`、`检测全部`、`取消`、`保存`、`保存并下发`、`发送测试邮件`、`确认修改`、`确认`、`复制密钥`、`验证并启用`、`我已保存`、`发送验证码`、`验证并绑定`、`确认清理`、`创建`、`轮换令牌`、`查看安装命令`、`生成当前配置`、`填入示例`、`关闭`、`复制`、`仅保存`、`推送默认配置`、`保存并一键下发`、`执行`、`复制命令`、`轮换并复制命令`

**onclick 处理**（37）：`pushAgentUpdateAll`、`openServerModal`、`checkAllServers`、`closeModal`、`saveUuidInfo`、`saveUuidNodes`、`testMonitorNotify`、`saveMonitorNotify`、`saveConfig`、`savePassword`、`saveTpl`、`saveBucketFile`、`submitMfaPassword`、`copyMfaSecret`、`verifyMfaSetup`、`saveUser`、`sendEmailCode`、`verifyEmailCode`、`runLogCleanup`、`createApiKey`、`copyApiKeyOnce`、`rotateAgentToken`、`showAgentInstall`、`saveServer`、`saveNodeTemplate`、`fillSingboxTemplateCurrent`、`fillSingboxTemplateSample`、`fillSingboxXrayCurrent`、`fillSingboxXraySample`、`fillSingboxMasqueSample`、`saveSingboxTemplate`、`saveSingboxNode`、`singboxCopy`、`saveNodeConfig`、`runSshExec`、`copyAgentCommand`、`rotateAndCopyAgentCommand`


## 全部 JS 函数（265 个）

`accessBox`、`addSubSourceAdmin`、`agentDetailBlock`、`agentSupportsMonitor`、`api`、`apiKeyExampleText`、`apiKeyPermPayload`、`applyAllSingboxNodes`、`applyPageMeta`、`applySingboxNode`、`applySysNavVisibility`、`barClass`、`batchPushUuids`、`bucketPrefixChange`、`buildChartData`、`buildReserveAlerts`、`cancelAgentTask`、`changeConfigPage`、`changeConfigSize`、`changeTplPage`、`changeTplSize`、`changeUuidPage`、`changeUuidSize`、`checkAllServers`、`clearAllConfigs`、`closeConfigPanel`、`closeModal`、`configSelIds`、`confirmUuidPick`、`connChart`、`copyAgentCommand`、`copyAgentToken`、`copyApiKeyOnce`、`copyMfaSecret`、`copyTextToClipboard`、`createApiKey`、`createBucketFile`、`currentCard`、`currentNodeConfig`、`currentServer`、`currentSubSourceUuid`、`currentUsername`、`delApiKey`、`delConfig`、`delSelectedConfigs`、`delSubSourceAdmin`、`delTpl`、`delUser`、`delUuid`、`detailBox`、`editApiKeyPerms`、`editServiceWatch`、`ensureChartJs`、`escapeHtml`、`fillServerSelect`、`fillSingboxMasqueSample`、`fillSingboxNodeSelects`、`fillSingboxTemplateCurrent`、`fillSingboxTemplateSample`、`fillSingboxXrayCurrent`、`fillSingboxXraySample`、`findApiKey`、`fmtAgentKeyTime`、`fmtApiKeyExpiry`、`fmtApiKeyTime`、`fmtBytes`、`fmtDateTime`、`fmtLogTime`、`fmtRate`、`fmtUptime`、`geoFlag`、`geoFlagCss`、`geoPlaceText`、`geoText`、`isSuperAdminUser`、`isValidSubSourceUrlClient`、`loadApiKeys`、`loadBucketFiles`、`loadChart`、`loadConfigs`、`loadLogSummary`、`loadMfaStatus`、`loadNodeConfigs`、`loadNodeTemplates`、`loadPushLogs`、`loadPushPickers`、`loadServerTasks`、`loadServers`、`loadServicePanel`、`loadSingbox`、`loadStats`、`loadSubSourcesAdmin`、`loadTemplates`、`loadUsers`、`loadUuids`、`logRetentionPayload`、`logout`、`mapPointOf`、`mapXY`、`metricRow`、`nodeConfigPush`、`nodeConfigPushDefault`、`nodeConfigSelect`、`nodeTemplateAction`、`nodeTemplatePayload`、`onLogRetentionChange`、`openAgentKeyModal`、`openApiKeyModal`、`openBucketEditor`、`openConfigModal`、`openConfigPanel`、`openEmailModal`、`openLogCleanupModal`、`openMfaPwdModal`、`openModal`、`openMonitorNotify`、`openNodeConfigModal`、`openNodeTemplateModal`、`openPwdModal`、`openServerDetail`、`openServerExec`、`openServerInstall`、`openServerModal`、`openSingboxGuide`、`openSingboxLinks`、`openSingboxNodeModal`、`openSingboxTemplateModal`、`openTplModal`、`openUserModal`、`openUuidInfoModal`、`openUuidNodesModal`、`osIcon`、`p`、`pctText`、`previewLogCleanup`、`previewSingboxNode`、`previewSingboxPick`、`pushAgentUpdate`、`pushAgentUpdateAll`、`pushAllNodeConfigs`、`pushStatusText`、`refreshCurrentCard`、`renameApiKey`、`renderAccessChart`、`renderApiKeys`、`renderBucketFiles`、`renderConfigs`、`renderLatestLogs`、`renderMfaSection`、`renderMonitorGrid`、`renderMonitorMap`、`renderNodeConfigUuidList`、`renderNodeConfigs`、`renderNodeTemplates`、`renderPopularUuids`、`renderPushLogs`、`renderSelfEmail`、`renderServerDetailChart`、`renderServerPicker`、`renderServerRuntime`、`renderServers`、`renderServicePanel`、`renderServiceStates`、`renderSingboxLinks`、`renderSingboxNodes`、`renderSingboxParsed`、`renderSingboxTemplates`、`renderStatsSummary`、`renderStatusReserve`、`renderSubSourcesAdmin`、`renderTemplates`、`renderUsers`、`renderUuidConfirmed`、`renderUuidNodesList`、`renderUuidPicker`、`renderUuids`、`reserveDonut`、`resetUuidPick`、`restartServiceFromPanel`、`rotateAgentToken`、`rotateAgentTokenFromModal`、`rotateAndCopyAgentCommand`、`rotateApiKey`、`runLogCleanup`、`runSshExec`、`runUuidReconcile`、`saveBucketFile`、`saveConfig`、`saveMonitorNotify`、`saveNodeConfig`、`saveNodeTemplate`、`savePassword`、`saveServer`、`saveSingboxNode`、`saveSingboxTemplate`、`saveTpl`、`saveUser`、`saveUuidInfo`、`saveUuidNodes`、`scheduleMonitorRefresh`、`sendEmailCode`、`serverAction`、`serviceWatchKey`、`setDefaultTpl`、`setLoading`、`setMonitorRefresh`、`showAgentInstall`、`showAgentTokenModal`、`showApiKeyOnce`、`showBackupCodes`、`showMfaQr`、`showPushStep2`、`singboxCopy`、`singboxCopyFallback`、`singboxCopyFrom`、`singboxGuideSelect`、`singboxNodeAction`、`singboxNodeDelete`、`singboxNodePayload`、`singboxParseSbText`、`singboxPortsText`、`singboxProtocolEntries`、`singboxProtocolPorts`、`singboxProtocolText`、`singboxRandomB64`、`singboxRandomHex`、`singboxRandomRealityKey`、`singboxRandomUuid`、`singboxTemplateAction`、`singboxTemplateDelete`、`singboxTemplateSampleConfig`、`singboxTemplateSamplePayload`、`singboxTemplateSampleUsers`、`sortTpls`、`sortUuids`、`statBox`、`statusBadge`、`storedMonitorRefresh`、`storedServiceWatch`、`subSourceApi`、`submitMfaPassword`、`svcBadge`、`svgDonut`、`switchCard`、`testMonitorNotify`、`toast`、`toggleApiKey`、`toggleConfigSelect`、`toggleConfigSelectAll`、`toggleNodeConfigUuid`、`togglePickServer`、`togglePickUuid`、`toggleSideNav`、`toggleSingboxEngine`、`toggleSubSourceAdmin`、`tunnelBadge`、`unbindEmail`、`updateConfigPager`、`updateConfigSelBar`、`updateNodeConfigCount`、`updatePushSummary`、`updateTplPager`、`updateUuidPager`、`verifyEmailCode`、`verifyMfaSetup`
