package api

import (
	"database/sql"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// Version 由 main 通过 ldflags 注入。
var Version = "V15.6"

// New 构造 HTTP 处理器。已迁移的路由走真实实现，其余转发给旧 Worker 或返回 501。
func New(cfg config.Config, pool *sql.DB, log *slog.Logger) http.Handler {
	h := &handlers{
		cfg:    cfg,
		db:     pool,
		log:    log,
		legacy: &http.Client{Timeout: 60 * time.Second},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("POST /api/login", h.login)

	// 账号
	mux.HandleFunc("POST /api/change-password", h.authed(authOpts{}, h.changePassword))
	mux.HandleFunc("GET /api/mfa/status", h.authed(authOpts{}, h.mfaStatus))
	mux.HandleFunc("POST /api/mfa/init", h.authed(authOpts{}, h.mfaInit))
	mux.HandleFunc("POST /api/mfa/verify-first", h.authed(authOpts{}, h.mfaVerifyFirst))
	mux.HandleFunc("POST /api/mfa/disable", h.authed(authOpts{}, h.mfaDisable))
	mux.HandleFunc("POST /api/mfa/backup-codes/regenerate", h.authed(authOpts{}, h.mfaRegenerateBackupCodes))
	mux.HandleFunc("POST /api/mfa/login-with-backup", h.mfaLoginWithBackup)

	// 系统管理（仅超管）
	mux.HandleFunc("GET /api/admin/users", h.authed(authOpts{superOnly: true}, h.adminUsersDispatch))
	mux.HandleFunc("POST /api/admin/users", h.authed(authOpts{superOnly: true}, h.adminUsersDispatch))
	mux.HandleFunc("PUT /api/admin/users", h.authed(authOpts{superOnly: true}, h.adminUsersDispatch))
	mux.HandleFunc("DELETE /api/admin/users", h.authed(authOpts{superOnly: true}, h.adminUsersDispatch))
	mux.HandleFunc("GET /api/apikeys", h.authed(authOpts{superOnly: true}, h.apiKeysDispatch))
	mux.HandleFunc("POST /api/apikeys", h.authed(authOpts{superOnly: true}, h.apiKeysDispatch))
	mux.HandleFunc("PUT /api/apikeys/{id}", h.authed(authOpts{superOnly: true}, h.apiKeyItemDispatch))
	mux.HandleFunc("DELETE /api/apikeys/{id}", h.authed(authOpts{superOnly: true}, h.apiKeyItemDispatch))
	mux.HandleFunc("POST /api/apikeys/{id}/rotate", h.authed(authOpts{superOnly: true}, h.apiKeyRotate))

	// 用户中心（邮箱验证码登录）
	mux.HandleFunc("POST /api/admin/users/email/code", h.authed(authOpts{superOnly: true}, h.adminEmailCodeSend))
	mux.HandleFunc("POST /api/admin/users/email/verify", h.authed(authOpts{superOnly: true}, h.adminEmailVerify))
	mux.HandleFunc("POST /api/admin/users/email/unbind", h.authed(authOpts{superOnly: true}, h.adminEmailUnbind))

	mux.HandleFunc("POST /api/login/email-code", h.loginEmailCodeSend)
	mux.HandleFunc("POST /api/login/email-code/verify", h.loginEmailCodeVerify)
	mux.HandleFunc("POST /api/login/email-code/mfa", h.loginEmailCodeMfa)

	mux.HandleFunc("POST /api/portal/send-code", h.portalSendCode)
	mux.HandleFunc("POST /api/portal/login", h.portalLogin)
	mux.HandleFunc("GET /api/portal/uuids", h.portalUuids)
	mux.HandleFunc("GET /api/portal/traffic", h.portalTraffic)
	mux.HandleFunc("GET /api/portal/masque", h.masqueWarpPortal)
	mux.HandleFunc("GET /api/portal/masque/config", h.masqueWarpPortalConfig)
	mux.HandleFunc("GET /api/portal/mtproto", h.portalMtproto)
	mux.HandleFunc("POST /api/portal/mtproto/{id}/deliver", h.portalMtprotoDeliver)
	mux.HandleFunc("POST /api/portal/mtproto/{id}/send-mail", h.portalMtprotoSendMail)
	mux.HandleFunc("POST /api/portal/email/send-code", h.portalEmailSendCode)
	mux.HandleFunc("POST /api/portal/email/verify", h.portalEmailVerify)

	// 订阅输出
	mux.HandleFunc("GET /sub/{uuid}", h.rawSubscription)
	mux.HandleFunc("GET /batch-configs/{uuid}", h.batchSubscription)
	mux.HandleFunc("GET /clash-config", h.clashConfig)

	// 订阅分组（UUID 元信息）
	mux.HandleFunc("GET /api/uuids", h.authed(authOpts{}, h.uuidsList))
	mux.HandleFunc("POST /api/uuids", h.authed(authOpts{}, h.uuidsSave))
	mux.HandleFunc("PUT /api/uuids", h.authed(authOpts{}, h.uuidsSave))
	mux.HandleFunc("DELETE /api/uuids", h.authed(authOpts{}, h.uuidsDelete))
	mux.HandleFunc("GET /api/uuids/{uuid}", h.authed(authOpts{}, h.uuidDetail))
	mux.HandleFunc("PUT /api/uuids/{uuid}", h.authed(authOpts{}, h.uuidDetail))
	mux.HandleFunc("DELETE /api/uuids/{uuid}", h.authed(authOpts{}, h.uuidDetail))
	mux.HandleFunc("GET /api/uuids/{uuid}/nodes", h.authed(authOpts{}, h.uuidNodes))
	mux.HandleFunc("GET /api/uuids/{uuid}/services", h.authed(authOpts{}, h.uuidServices))
	mux.HandleFunc("PUT /api/uuids/{uuid}/services", h.authed(authOpts{}, h.uuidServices))
	mux.HandleFunc("PUT /api/uuids/{uuid}/nodes", h.authed(authOpts{}, h.uuidNodes))
	mux.HandleFunc("POST /api/uuids/{uuid}/nodes", h.authed(authOpts{}, h.uuidNodes))
	mux.HandleFunc("GET /api/uuids/{uuid}/configs", h.authed(authOpts{}, h.uuidConfigsRest))
	mux.HandleFunc("POST /api/uuids/{uuid}/configs", h.authed(authOpts{}, h.uuidConfigsRest))
	mux.HandleFunc("GET /api/uuids/{uuid}/configs/{id}", h.authed(authOpts{}, h.uuidConfigItem))
	mux.HandleFunc("PUT /api/uuids/{uuid}/configs/{id}", h.authed(authOpts{}, h.uuidConfigItem))
	mux.HandleFunc("DELETE /api/uuids/{uuid}/configs/{id}", h.authed(authOpts{}, h.uuidConfigItem))

	// 订阅模板（GET 公开，与 Worker 一致）
	mux.HandleFunc("GET /api/templates", h.templatesList)
	mux.HandleFunc("GET /api/templates/manage", h.authed(authOpts{}, h.templatesList))
	mux.HandleFunc("POST /api/templates/manage", h.authed(authOpts{}, h.templateCreate))
	mux.HandleFunc("PUT /api/templates/manage", h.authed(authOpts{}, h.templateUpdate))
	mux.HandleFunc("DELETE /api/templates/manage", h.authed(authOpts{}, h.templateDelete))
	mux.HandleFunc("POST /api/templates/manage/set-default", h.authed(authOpts{}, h.templateSetDefault))
	mux.HandleFunc("GET /api/templates/manage/{id}", h.authed(authOpts{}, h.templateItemGet))
	mux.HandleFunc("PUT /api/templates/manage/{id}", h.authed(authOpts{}, h.templateItemPut))
	mux.HandleFunc("DELETE /api/templates/manage/{id}", h.authed(authOpts{}, h.templateDelete))

	// 统计与日志
	mux.HandleFunc("GET /api/stats", h.authed(authOpts{}, h.statsOverview))
	mux.HandleFunc("GET /api/logs/summary", h.authed(authOpts{superOnly: true}, h.logsSummary))
	mux.HandleFunc("POST /api/logs/cleanup", h.authed(authOpts{superOnly: true}, h.logsCleanup))

	// 配置管理（后台 + 用户中心）
	mux.HandleFunc("/api/uuid-configs", h.authed(authOpts{}, h.uuidConfigsDispatch))
	mux.HandleFunc("GET /manage/configs/{uuid}", h.manageConfigsList)
	mux.HandleFunc("POST /manage/configs", h.manageConfigsAdd)
	mux.HandleFunc("PUT /manage/configs", h.manageConfigsUpdate)
	mux.HandleFunc("DELETE /manage/configs/batch", h.manageConfigsBatchDelete)
	mux.HandleFunc("DELETE /manage/configs/id/{id}", h.manageConfigDeleteByID)
	mux.HandleFunc("DELETE /manage/configs/{uuid}", h.manageConfigsClear)
	mux.HandleFunc("GET /manage/uuid-sub-sources", h.manageSubSourcesList)
	mux.HandleFunc("POST /manage/uuid-sub-sources", h.manageSubSourcesAdd)
	mux.HandleFunc("PUT /manage/uuid-sub-sources", h.manageSubSourcesUpdate)
	mux.HandleFunc("DELETE /manage/uuid-sub-sources", h.manageSubSourcesDelete)

	// sing-box（只读：模板/节点/可用 UUID）
	mux.HandleFunc("GET /api/singbox/templates", h.authed(authOpts{}, h.singboxTemplatesList))
	mux.HandleFunc("GET /api/singbox/templates/{id}", h.authed(authOpts{}, h.singboxTemplateGet))
	mux.HandleFunc("POST /api/singbox/templates", h.authed(authOpts{}, h.singboxTemplateCreate))
	mux.HandleFunc("POST /api/singbox/templates/from-config", h.authed(authOpts{}, h.singboxTemplateFromConfig))
	mux.HandleFunc("POST /api/singbox/templates/preview", h.authed(authOpts{}, h.singboxTemplatePreview))
	mux.HandleFunc("PUT /api/singbox/templates/{id}", h.authed(authOpts{}, h.singboxTemplateUpdate))
	mux.HandleFunc("DELETE /api/singbox/templates/{id}", h.authed(authOpts{}, h.singboxTemplateDelete))
	mux.HandleFunc("GET /api/singbox/nodes", h.authed(authOpts{}, h.singboxNodesList))
	mux.HandleFunc("POST /api/singbox/nodes", h.authed(authOpts{}, h.singboxNodeCreate))
	mux.HandleFunc("PUT /api/singbox/nodes/{id}", h.authed(authOpts{}, h.singboxNodeUpdate))
	mux.HandleFunc("DELETE /api/singbox/nodes/{id}", h.authed(authOpts{}, h.singboxNodeDelete))
	mux.HandleFunc("GET /api/singbox/nodes/{id}", h.authed(authOpts{}, h.singboxNodeGet))
	mux.HandleFunc("GET /api/singbox/nodes/{id}/preview", h.authed(authOpts{}, h.singboxNodePreview))
	mux.HandleFunc("GET /api/singbox/nodes/{id}/links", h.authed(authOpts{}, h.singboxNodeLinks))
	mux.HandleFunc("POST /api/singbox/nodes/{id}/apply", h.authed(authOpts{}, h.singboxNodeApply))
	mux.HandleFunc("GET /api/singbox/uuids", h.authed(authOpts{}, h.singboxUUIDs))
	mux.HandleFunc("POST /api/singbox/preview", h.authed(authOpts{}, h.singboxPreview))
	mux.HandleFunc("POST /api/singbox/apply-all", h.authed(authOpts{}, h.singboxApplyAll))
	mux.HandleFunc("POST /api/singbox/deploy-all", h.authed(authOpts{}, h.singboxDeployAll))
	mux.HandleFunc("POST /api/singbox/push", h.authed(authOpts{}, h.singboxPush))
	mux.HandleFunc("POST /api/singbox/reconcile", h.authed(authOpts{}, h.singboxReconcile))
	mux.HandleFunc("POST /api/singbox/nodes/{id}/deploy", h.authed(authOpts{}, h.singboxNodeDeploy))
	mux.HandleFunc("POST /api/singbox/nodes/{id}/warp", h.authed(authOpts{}, h.singboxNodeWarp))
	mux.HandleFunc("GET /api/singbox/releases", h.authed(authOpts{}, h.singboxReleases))
	// ---- mihomo 模板管理 / 节点绑定 ----
	mux.HandleFunc("GET /api/mihomo/templates", h.authed(authOpts{}, h.mihomoTemplatesList))
	mux.HandleFunc("GET /api/mihomo/templates/{id}", h.authed(authOpts{}, h.mihomoTemplateGet))
	mux.HandleFunc("POST /api/mihomo/templates", h.authed(authOpts{}, h.mihomoTemplateCreate))
	mux.HandleFunc("PUT /api/mihomo/templates/{id}", h.authed(authOpts{}, h.mihomoTemplateUpdate))
	mux.HandleFunc("DELETE /api/mihomo/templates/{id}", h.authed(authOpts{}, h.mihomoTemplateDelete))
	mux.HandleFunc("GET /api/mihomo/nodes", h.authed(authOpts{}, h.mihomoNodesList))
	mux.HandleFunc("POST /api/mihomo/nodes", h.authed(authOpts{}, h.mihomoNodeCreate))
	mux.HandleFunc("PUT /api/mihomo/nodes/{id}", h.authed(authOpts{}, h.mihomoNodeUpdate))
	mux.HandleFunc("DELETE /api/mihomo/nodes/{id}", h.authed(authOpts{}, h.mihomoNodeDelete))
	mux.HandleFunc("GET /api/mihomo/nodes/{id}/preview", h.authed(authOpts{}, h.mihomoNodePreview))

	// Agent 接入（keycheck 用接入令牌鉴权；latest 走后台 JWT）
	mux.HandleFunc("POST /api/agent/poll", h.agentPoll)
	mux.HandleFunc("POST /api/agent/ack", h.agentAck)
	mux.HandleFunc("GET /api/agent/install.sh", h.agentInstallScript)
	mux.HandleFunc("GET /api/agent/bootstrap", h.agentBootstrap)
	mux.HandleFunc("POST /api/agent/bootstrap", h.agentBootstrap)
	mux.HandleFunc("GET /install.sh", h.agentInstallScript)
	mux.HandleFunc("GET /agent/install.sh", h.agentInstallScript)
	mux.HandleFunc("GET /api/agent/keycheck", h.agentKeycheck)
	mux.HandleFunc("POST /api/agent/keycheck", h.agentKeycheck)
	mux.HandleFunc("GET /api/agent/latest", h.authed(authOpts{}, h.agentLatest))
	mux.HandleFunc("GET /api/agent/tasks", h.authed(authOpts{}, h.agentTasksList))
	mux.HandleFunc("POST /api/agent/tasks", h.authed(authOpts{}, h.agentTaskCreate))
	mux.HandleFunc("POST /api/agent/push-update", h.authed(authOpts{}, h.agentPushUpdate))
	mux.HandleFunc("POST /api/agent/tasks/{id}/cancel", h.authed(authOpts{}, h.agentTaskCancel))
	mux.HandleFunc("DELETE /api/agent/tasks/{id}", h.authed(authOpts{}, h.agentTaskDelete))

	// 服务器管理
	mux.HandleFunc("GET /api/servers", h.authed(authOpts{}, h.serversList))
	mux.HandleFunc("GET /api/servers/status", h.authed(authOpts{}, h.serversStatus))
	mux.HandleFunc("POST /api/servers", h.authed(authOpts{}, h.serverCreate))
	mux.HandleFunc("GET /api/servers/{id}", h.authed(authOpts{}, h.serverGet))
	mux.HandleFunc("PUT /api/servers/{id}", h.authed(authOpts{}, h.serverUpdate))
	mux.HandleFunc("DELETE /api/servers/{id}", h.authed(authOpts{}, h.serverDelete))
	mux.HandleFunc("POST /api/servers/{id}/agent-token", h.authed(authOpts{}, h.serverRotateToken))
	mux.HandleFunc("GET /api/servers/{id}/unlock", h.authed(authOpts{}, h.serverUnlock))
	mux.HandleFunc("POST /api/servers/{id}/unlock", h.authed(authOpts{}, h.serverUnlock))
	mux.HandleFunc("GET /api/servers/{id}/{action}", h.authed(authOpts{}, h.serverAction))
	mux.HandleFunc("POST /api/servers/{id}/{action}", h.authed(authOpts{}, h.serverAction))

	// MTProto
	mux.HandleFunc("GET /api/mtproto/templates", h.authed(authOpts{}, h.mtprotoTemplatesList))
	mux.HandleFunc("POST /api/mtproto/templates", h.authed(authOpts{}, h.mtprotoTemplateCreate))
	mux.HandleFunc("GET /api/mtproto/templates/{id}", h.authed(authOpts{}, h.mtprotoTemplateGet))
	mux.HandleFunc("PUT /api/mtproto/templates/{id}", h.authed(authOpts{}, h.mtprotoTemplateUpdate))
	mux.HandleFunc("POST /api/mtproto/templates/{id}/apply", h.authed(authOpts{}, h.mtprotoTemplateApply))
	mux.HandleFunc("POST /api/mtproto/templates/{id}/preview", h.authed(authOpts{}, h.mtprotoTemplatePreview))
	mux.HandleFunc("DELETE /api/mtproto/templates/{id}", h.authed(authOpts{}, h.mtprotoTemplateDelete))
	mux.HandleFunc("GET /api/mtproto/nodes", h.authed(authOpts{}, h.mtprotoNodesList))
	mux.HandleFunc("POST /api/mtproto/nodes", h.authed(authOpts{}, h.mtprotoNodeCreate))
	mux.HandleFunc("GET /api/mtproto/nodes/{id}", h.authed(authOpts{}, h.mtprotoNodeGet))
	mux.HandleFunc("PUT /api/mtproto/nodes/{id}", h.authed(authOpts{}, h.mtprotoNodeUpdate))
	mux.HandleFunc("DELETE /api/mtproto/nodes/{id}", h.authed(authOpts{}, h.mtprotoNodeDelete))
	mux.HandleFunc("POST /api/mtproto/nodes/{id}/apply", h.authed(authOpts{}, h.mtprotoNodeApply))
	mux.HandleFunc("GET /api/mtproto/nodes/{id}/clients", h.authed(authOpts{}, h.mtprotoClientsList))
	mux.HandleFunc("POST /api/mtproto/nodes/{id}/clients", h.authed(authOpts{}, h.mtprotoClientCreate))
	mux.HandleFunc("DELETE /api/mtproto/clients/{id}", h.authed(authOpts{}, h.mtprotoClientDelete))

	// MASQUE over Cloudflare WARP（服务端注册、按 UUID 授权）
	mux.HandleFunc("GET /api/masque/warp/settings", h.authed(authOpts{}, h.masqueWarpSettingsGet))
	mux.HandleFunc("PUT /api/masque/warp/settings", h.authed(authOpts{}, h.masqueWarpSettingsSave))
	mux.HandleFunc("GET /api/masque/warp/profiles", h.authed(authOpts{}, h.masqueWarpProfilesList))
	mux.HandleFunc("POST /api/masque/warp/profiles", h.authed(authOpts{}, h.masqueWarpProfileCreate))
	mux.HandleFunc("POST /api/masque/warp/profiles/{uuid}/refresh", h.authed(authOpts{}, h.masqueWarpProfileRefresh))
	mux.HandleFunc("DELETE /api/masque/warp/profiles/{uuid}", h.authed(authOpts{}, h.masqueWarpProfileRevoke))
	mux.HandleFunc("GET /api/masque/warp/profiles/{uuid}/config", h.authed(authOpts{}, h.masqueWarpProfileConfig))
	mux.HandleFunc("GET /api/mtproto/uuids", h.authed(authOpts{}, h.mtprotoUUIDs))
	mux.HandleFunc("POST /api/mtproto/nodes/{id}/preview", h.authed(authOpts{}, h.mtprotoNodePreview))
	mux.HandleFunc("GET /api/mtproto/clients/{id}/link", h.authed(authOpts{}, h.mtprotoClientLinkHandler))
	mux.HandleFunc("POST /api/mtproto/clients/{id}/rotate", h.authed(authOpts{}, h.mtprotoClientRotate))
	mux.HandleFunc("POST /api/mtproto/clients/{id}/revoke", h.authed(authOpts{}, h.mtprotoClientRevoke))
	mux.HandleFunc("POST /api/mtproto/clients/{id}/restore", h.authed(authOpts{}, h.mtprotoClientRestore))
	mux.HandleFunc("POST /api/mtproto/clients/{id}/send-mail", h.authed(authOpts{}, h.mtprotoClientSendMail))
	mux.HandleFunc("POST /api/mtproto/apply-all", h.authed(authOpts{}, h.mtprotoApplyAll))
	mux.HandleFunc("POST /api/mtproto/push", h.authed(authOpts{}, h.mtprotoPush))
	mux.HandleFunc("POST /api/mtproto/reconcile", h.authed(authOpts{}, h.mtprotoReconcile))

	// 节点配置模板 / 配置
	mux.HandleFunc("GET /api/node-templates", h.authed(authOpts{}, h.nodeTemplatesList))
	mux.HandleFunc("POST /api/node-templates", h.authed(authOpts{}, h.nodeTemplateCreate))
	mux.HandleFunc("GET /api/node-templates/{id}", h.authed(authOpts{}, h.nodeTemplateGet))
	mux.HandleFunc("PUT /api/node-templates/{id}", h.authed(authOpts{}, h.nodeTemplateUpdate))
	mux.HandleFunc("DELETE /api/node-templates/{id}", h.authed(authOpts{}, h.nodeTemplateDelete))
	mux.HandleFunc("GET /api/node-configs", h.authed(authOpts{}, h.nodeConfigsList))
	mux.HandleFunc("POST /api/node-configs", h.authed(authOpts{}, h.nodeConfigCreate))
	mux.HandleFunc("GET /api/node-configs/{id}", h.authed(authOpts{}, h.nodeConfigItem))
	mux.HandleFunc("PUT /api/node-configs/{id}", h.authed(authOpts{}, h.nodeConfigItem))
	mux.HandleFunc("DELETE /api/node-configs/{id}", h.authed(authOpts{}, h.nodeConfigItem))
	mux.HandleFunc("POST /api/node-configs/{id}/push", h.authed(authOpts{}, h.nodeConfigPushOne))
	mux.HandleFunc("GET /api/node-configs/logs", h.authed(authOpts{}, h.nodeConfigLogs))
	mux.HandleFunc("POST /api/node-configs/preview", h.authed(authOpts{}, h.nodeConfigPreview))
	mux.HandleFunc("POST /api/node-configs/push", h.authed(authOpts{}, h.nodeConfigPush))

	// OIDC
	mux.HandleFunc("GET /api/oidc/status", h.oidcStatus)
	mux.HandleFunc("GET /api/oidc/login", h.oidcLogin)
	mux.HandleFunc("GET /api/oidc/callback", h.oidcCallback)

	// 访问统计
	mux.HandleFunc("GET /api/stats/uuid-details", h.authed(authOpts{}, h.statsUUIDDetails))
	mux.HandleFunc("GET /api/uuid-configs/stats", h.authed(authOpts{}, h.uuidConfigsStats))

	// 流量 / 连接统计（V15.6）
	mux.HandleFunc("GET /api/stats/traffic/users", h.authed(authOpts{}, h.statsTrafficUsers))
	mux.HandleFunc("GET /api/stats/traffic/users/{uuid}", h.authed(authOpts{}, h.statsTrafficUserDetail))
	mux.HandleFunc("GET /api/stats/traffic/nodes", h.authed(authOpts{}, h.statsTrafficNodes))
	mux.HandleFunc("POST /api/stats/traffic/clear", h.authed(authOpts{superOnly: true}, h.statsTrafficClear))
	mux.HandleFunc("GET /api/nodes/{id}/connections", h.authed(authOpts{}, h.nodeConnections))

	// 批量任务
	mux.HandleFunc("GET /api/jobs", h.authed(authOpts{}, h.jobsList))
	mux.HandleFunc("POST /api/jobs", h.authed(authOpts{}, h.jobsCreate))
	mux.HandleFunc("GET /api/jobs/{id}", h.authed(authOpts{}, h.jobsGet))
	mux.HandleFunc("POST /api/jobs/{id}/cancel", h.authed(authOpts{}, h.jobsCancel))
	mux.HandleFunc("POST /api/jobs/{id}/retry", h.authed(authOpts{}, h.jobsRetry))
	mux.HandleFunc("POST /api/jobs/clear", h.authed(authOpts{}, h.jobsClear))

	mux.HandleFunc("GET /api/backup/export", h.authed(authOpts{superOnly: true}, h.backupExport))
	mux.HandleFunc("POST /api/backup/import", h.authed(authOpts{superOnly: true}, h.backupImport))

	mux.HandleFunc("GET /api/attachments/status", h.authed(authOpts{superOnly: true}, h.attachmentsStatus))
	mux.HandleFunc("GET /api/attachments", h.authed(authOpts{superOnly: true}, h.attachmentsList))
	mux.HandleFunc("POST /api/attachments/upload", h.authed(authOpts{superOnly: true}, h.attachmentsUpload))
	mux.HandleFunc("POST /api/attachments/folder", h.authed(authOpts{superOnly: true}, h.attachmentsMkdir))
	mux.HandleFunc("POST /api/attachments/move", h.authed(authOpts{superOnly: true}, h.attachmentsMove))
	mux.HandleFunc("POST /api/attachments/copy", h.authed(authOpts{superOnly: true}, h.attachmentsCopyOp))
	mux.HandleFunc("GET /api/attachments/download", h.authed(authOpts{superOnly: true}, h.attachmentsDownload))
	mux.HandleFunc("DELETE /api/attachments", h.authed(authOpts{superOnly: true}, h.attachmentsDelete))
	mux.HandleFunc("GET /api/attachments/content", h.authed(authOpts{superOnly: true}, h.attachmentsContent))
	mux.HandleFunc("PUT /api/attachments/content", h.authed(authOpts{superOnly: true}, h.attachmentsContentSave))
	mux.HandleFunc("POST /api/cloudflare/check", h.authed(authOpts{superOnly: true}, h.cloudflareCheck))

	mux.HandleFunc("GET /api/settings", h.authed(authOpts{superOnly: true}, h.settingsAPI))
	mux.HandleFunc("PUT /api/settings", h.authed(authOpts{superOnly: true}, h.settingsAPI))
	mux.HandleFunc("POST /api/settings", h.authed(authOpts{superOnly: true}, h.settingsAPI))

	mux.HandleFunc("GET /api/monitor/notify", h.authed(authOpts{}, h.monitorNotify))
	mux.HandleFunc("POST /api/monitor/notify", h.authed(authOpts{}, h.monitorNotify))
	mux.HandleFunc("POST /api/monitor/notify/test", h.authed(authOpts{}, h.monitorNotifyTest))
	mux.HandleFunc("GET /api/monitor/uuid-check", h.authed(authOpts{}, h.monitorUUIDCheck))
	mux.HandleFunc("POST /api/monitor/uuid-check", h.authed(authOpts{}, h.monitorUUIDCheck))
	mux.HandleFunc("GET /api/agent/ws", h.agentWS)

	mux.HandleFunc("GET /api/bucket/files", h.authed(authOpts{}, h.bucketFiles))
	mux.HandleFunc("GET /api/bucket/file", h.authed(authOpts{}, h.bucketFile))
	mux.HandleFunc("POST /api/bucket/file", h.authed(authOpts{}, h.bucketFile))
	mux.HandleFunc("PUT /api/bucket/file", h.authed(authOpts{}, h.bucketFile))

	mux.HandleFunc("GET /api/servers/services", h.authed(authOpts{}, h.serversServices))
	mux.HandleFunc("GET /api/servers/monitor", h.authed(authOpts{}, h.serversMonitor))
	mux.HandleFunc("GET /api/shell/status", h.authed(authOpts{}, h.shellStatus))
	mux.HandleFunc("GET /api/shell/browser/{id}", h.shellBrowser)
	mux.HandleFunc("GET /api/shell/agent/{id}", h.shellAgent)
	mux.HandleFunc("GET /api/agent/wait", h.agentWait)
	mux.HandleFunc("POST /api/agent/wait", h.agentWait)
	mux.HandleFunc("GET /api/singbox", h.authed(authOpts{}, h.singboxRoot))
	mux.HandleFunc("GET /api/mtproto", h.authed(authOpts{}, h.mtprotoRoot))

	// 前端页面（SPA；构建产物缺失时返回 503 说明）
	mux.HandleFunc("GET /{$}", h.pageIndex)
	mux.HandleFunc("GET /admin", h.pageIndex)
	mux.HandleFunc("GET /admin/", h.pageIndex)
	mux.HandleFunc("GET /login", h.pageIndex)
	mux.HandleFunc("GET /login/", h.pageIndex)
	mux.HandleFunc("GET /me", h.pageIndex)
	mux.HandleFunc("GET /me/", h.pageIndex)
	mux.HandleFunc("GET /ui", h.pageIndex)
	mux.HandleFunc("GET /ui/", h.pageIndex)
	mux.HandleFunc("GET /admin/settings", h.settingsPage)
	mux.HandleFunc("GET /manage", h.pageIndex)
	mux.HandleFunc("GET /manage/all-uuid-info", h.pageIndex)
	mux.HandleFunc("GET /manage/entry", h.pageIndex)
	mux.HandleFunc("GET /manage/generate-uuid", h.pageIndex)
	mux.HandleFunc("GET /manage/stats/{uuid}", h.manageStats)
	mux.HandleFunc("GET /manage/uuid-check/{uuid}", h.pageIndex)
	mux.HandleFunc("GET /manage/uuid-info", h.manageUUIDInfoGet)
	mux.HandleFunc("GET /manage/uuid-info/{uuid}", h.manageUUIDInfoGet)
	mux.HandleFunc("POST /manage/uuid-info", h.manageUUIDInfoSave)
	mux.HandleFunc("GET /manage/", h.pageIndex)
	mux.HandleFunc("GET /agent-dist/", h.agentDistFile)
	mux.HandleFunc("GET /admin-app.js", h.adminAppJS)
	mux.HandleFunc("GET /xterm-app.js", h.xtermAppJS)
	mux.HandleFunc("GET /admin-app.css", h.adminAppCSS)
	mux.HandleFunc("GET /assets/world.svg", h.worldSVG)
	mux.HandleFunc("GET /api/flag/{cc}", h.flagImage)
	mux.HandleFunc("GET /favicon.svg", h.favicon)
	mux.HandleFunc("GET /favicon.ico", h.favicon)
	mux.HandleFunc("GET /assets/", h.pageAssets)

	// 兜底：转发旧 Worker（若配置了 PP_LEGACY_BASE），否则 501 / 404
	mux.HandleFunc("/", h.notMigrated)

	return h.recoverer(h.cors(h.secureHeaders(h.logRequests(mux))))
}

type handlers struct {
	cfg    config.Config
	db     *sql.DB
	log    *slog.Logger
	legacy *http.Client

	settingsMu sync.RWMutex
	settings   map[string]string
	settingsAt time.Time
}

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()

	status, code := "up", http.StatusOK
	if err := h.db.PingContext(ctx); err != nil {
		status, code = "down", http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{
		"ok":      code == http.StatusOK,
		"service": "proxypilot-api",
		"version": Version,
		"db":      status,
	})
}
