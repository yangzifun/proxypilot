# 老版管理后台 · 能力清单（自动从 worker.js 抽取）

> 区间：worker.js 第 6942 – 12233 行（adminHtml 模板）
> 生成：audit-parity.mjs

## 分区（左侧菜单 9 项）

| key | 名称 | 接口数 | 动作数 | 表单控件 |
|---|---|---|---|---|
| `dash` | 系统概览 | 0 | 13 | 2 |
| `uuids` | 订阅分组 | 0 | 15 | 7 |
| `tpls` | 订阅模板 | 0 | 8 | 2 |
| `servers` | 服务器管理 | 98 | 77 | 81 |
| `singbox` | 节点管理 | 0 | 5 | 0 |
| `status` | 服务器监控 | 0 | 1 | 1 |
| `logs` | 日志管理 | 0 | 0 | 0 |
| `sys` | 系统管理 | 0 | 4 | 2 |
| `sec` | 安全中心 | 0 | 3 | 0 |

### 系统概览（dash）

**接口**

- （无）

**交互动作**（onclick）

- `switchCard('dash')`
- `switchCard('uuids')`
- `switchCard('tpls')`
- `switchCard('servers')`
- `switchCard('singbox')`
- `switchCard('status')`
- `switchCard('logs')`
- `switchCard('sys')`
- `switchCard('sec')`
- `toggleSideNav()`
- `refreshCurrentCard()`
- `openPwdModal()`
- `logout()`

**表单控件**（2）

`statsDays`、`chartView`

### 订阅分组（uuids）

**接口**

- （无）

**交互动作**（onclick）

- `sortUuids('uuid')`
- `sortUuids('owner')`
- `sortUuids('email')`
- `sortUuids('count')`
- `sortUuids('status')`
- `changeUuidPage(-1)`
- `changeUuidPage(1)`
- `configState.page=1;loadConfigs();`
- `clearAllConfigs()`
- `delSelectedConfigs()`
- `openConfigModal(null)`
- `changeConfigPage(-1)`
- `changeConfigPage(1)`
- `addSubSourceAdmin()`
- `closeConfigPanel()`

**表单控件**（7）

`uuidSize`、`configUuid`、`configProtocol`、`configCheckAll`、`configSize`、`subSrcUrl`、`subSrcRemark`

### 订阅模板（tpls）

**接口**

- （无）

**交互动作**（onclick）

- `openTplModal(null)`
- `sortTpls('name')`
- `sortTpls('url')`
- `sortTpls('is_default')`
- `changeTplPage(-1)`
- `changeTplPage(1)`
- `loadBucketFiles(true)`
- `createBucketFile()`

**表单控件**（2）

`tplSize`、`bucketPrefix`

### 服务器管理（servers）

**接口**

- `stats`
- `uuids`
- `uuid-configs`
- `templates/manage`
- `bucket/files`
- `bucket/file`
- `templates/manage/set-default`
- `change-password`
- `mfa/status`
- `mfa/init`
- `mfa/disable`
- `mfa/backup-codes/regenerate`
- `mfa/verify-first`
- `admin/users`
- `admin/users/email/code`
- `admin/users/email/verify`
- `admin/users/email/unbind`
- `logs/summary`
- `logs/cleanup`
- `apikeys`
- `servers`
- `agent/push-update`
- `agent/tasks`
- `singbox/uuids`
- `singbox/nodes`
- `singbox/push`
- `node-configs/logs`
- `monitor/notify`
- `monitor/notify/test`
- `servers/monitor`
- `node-templates`
- `singbox/templates`
- `singbox/templates/preview`
- `singbox/templates/from-config`
- `monitor/uuid-check`
- `singbox/apply-all`
- `singbox/deploy-all`

**交互动作**（onclick）

- `pushAgentUpdateAll()`
- `openServerModal(null)`
- `checkAllServers()`
- `closeModal('uuidInfoOverlay')`
- `saveUuidInfo()`
- `closeModal('uuidNodesOverlay')`
- `saveUuidNodes()`
- `closeModal('monitorNotifyOverlay')`
- `testMonitorNotify()`
- `saveMonitorNotify()`
- `closeModal('configOverlay')`
- `saveConfig()`
- `closeModal('pwdOverlay')`
- `savePassword()`
- `closeModal('tplOverlay')`
- `saveTpl()`
- `closeModal('bucketEditorOverlay')`
- `saveBucketFile()`
- `closeModal('mfaPwdOverlay')`
- `submitMfaPassword()`
- `copyMfaSecret()`
- `closeModal('mfaSetupOverlay')`
- `verifyMfaSetup()`
- `closeModal('backupOverlay')`
- `closeModal('userOverlay')`
- `saveUser()`
- `sendEmailCode()`
- `closeModal('emailOverlay')`
- `verifyEmailCode()`
- `closeModal('logCleanupOverlay')`
- `runLogCleanup()`
- `closeModal('apiKeyOverlay')`
- `createApiKey()`
- `copyApiKeyOnce()`
- `closeModal('apiKeyOnceOverlay')`
- `rotateAgentToken()`
- `showAgentInstall()`
- `closeModal('serverOverlay')`
- `saveServer()`
- `closeModal('nodeTemplateOverlay')`
- `saveNodeTemplate()`
- `fillSingboxTemplateCurrent()`
- `fillSingboxTemplateSample()`
- `fillSingboxXrayCurrent()`
- `fillSingboxXraySample()`
- `fillSingboxMasqueSample()`
- `closeModal('singboxTemplateOverlay')`
- `saveSingboxTemplate()`
- `closeModal('singboxNodeOverlay')`
- `saveSingboxNode()`
- `closeModal('singboxPreviewOverlay')`
- `closeModal('singboxGuideOverlay')`
- `singboxCopy(document.getElementById('singboxLinksSub').value, '订阅串已复制')`
- `closeModal('singboxLinksOverlay')`
- `closeModal('nodeConfigOverlay')`
- `saveNodeConfig(false, false)`
- `saveNodeConfig(true, true)`
- `saveNodeConfig(true, false)`
- `closeModal('sshExecOverlay')`
- `runSshExec()`
- `closeModal('serverDetailOverlay')`
- `copyAgentCommand()`
- `rotateAndCopyAgentCommand()`
- `closeModal('agentTokenOverlay')`
- `toggleSubSourceAdmin(' + s.id + ', ' + (enabled ? 0 : 1) + ')`
- `delSubSourceAdmin(' + s.id + ')`
- `openConfigModal(' + row.id + ')`
- `delConfig(' + row.id + ')`
- `nodeConfigSelect(' + n.id + ')`
- `nodeConfigPushDefault(' + n.id + ')`
- `nodeConfigPush(' + n.id + ')`
- `singboxCopyFrom(this)`
- `openSingboxTemplateModal(' + t.id + ')`
- `singboxTemplateDelete(' + t.id + ')`
- `applySingboxNode(' + n.id + ', true)`
- `openSingboxNodeModal(' + n.id + ')`
- `singboxNodeDelete(' + n.id + ')`

**表单控件**（81）

`uuidInfoUuid`、`uuidInfoOwner`、`uuidInfoEmail`、`uuidInfoStatus`、`uuidInfoRemark`、`uuidNodesUuid`、`monitorNotifyEnabled`、`monitorNotifyEmails`、`monitorNotifyCooldown`、`configId`、`configModalUuid`、`configData`、`pwdOld`、`pwdNew`、`pwdConfirm`、`tplId`、`tplName`、`tplUrl`、`tplDesc`、`tplDefault`、`bucketEditorText`、`mfaPwdInput`、`mfaCode`、`userNameInput`、`userPasswordInput`、`userMfaReset`、`emailUserName`、`emailInput`、`emailCodeInput`、`logCleanupCount`、`logCleanupCutoff`、`logCleanupPwd`、`apiKeyName`、`apiKeyDays`、`serverId`、`serverName`、`serverPollSec`、`serverMetricsEvery`、`serverAllowExec`、`serverTags`、`serverRemark`、`serverEnabled`、`nodeTemplateId`、`nodeTemplateName`、`nodeTemplateEngine`、`nodeTemplatePath`、`nodeTemplateService`、`nodeTemplateCheck`、`nodeTemplateRestart`、`nodeTemplateContent`、`nodeTemplateRemark`、`nodeTemplateBackup`、`nodeTemplateEnabled`、`singboxTemplateId`、`singboxTemplateName`、`singboxTemplateRemark`、`singboxEngineSb`、`singboxEngineXray`、`singboxEngineMasque`、`singboxTemplateSbText`、`singboxTemplateXrayText`、`singboxTemplateMasqueText`、`singboxNodeId`、`singboxNodeName`、`singboxNodeDomain`、`singboxNodeServer`、`singboxNodeTemplate`、`singboxNodeSbPath`、`singboxNodeSvcSb`、`singboxNodeXrayPath`、`singboxNodeSvcXray`、`singboxNodeEnabled`、`singboxNodeRemark`、`singboxPreviewSelect`、`singboxPreviewContent`、`singboxLinksUser`、`singboxLinksSub`、`nodeConfigId`、`nodeConfigUuidSearch`、`sshExecCommand`、`sshExecTimeout`

### 节点管理（singbox）

**接口**

- （无）

**交互动作**（onclick）

- `openSingboxTemplateModal(null)`
- `openSingboxGuide()`
- `runUuidReconcile()`
- `applyAllSingboxNodes()`
- `openSingboxNodeModal(null)`

**表单控件**（0）

（无）

### 服务器监控（status）

**接口**

- （无）

**交互动作**（onclick）

- `openMonitorNotify()`

**表单控件**（1）

`monitorRefresh`

### 日志管理（logs）

**接口**

- （无）

**交互动作**（onclick）

- （无）

**表单控件**（0）

（无）

### 系统管理（sys）

**接口**

- （无）

**交互动作**（onclick）

- `openUserModal(null)`
- `previewLogCleanup()`
- `openLogCleanupModal()`
- `openApiKeyModal()`

**表单控件**（2）

`logRetention`、`logBeforeDate`

### 安全中心（sec）

**接口**

- （无）

**交互动作**（onclick）

- `openEmailModal(currentUsername())`
- `unbindEmail(currentUsername())`
- `openPwdModal()`

**表单控件**（0）

（无）
