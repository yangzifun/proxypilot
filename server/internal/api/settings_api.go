package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func (h *handlers) settingsSnapshot(r *http.Request) map[string]any {
	ctx := r.Context()
	values := map[string]any{}
	has := map[string]bool{}
	for _, d := range settingDefs {
		v := h.settingValue(ctx, d.Key)
		if d.Secret {
			has[d.Key] = v != "" || h.settingDefault(d.Key) != ""
			values[d.Key] = ""
			continue
		}
		if v == "" && !d.Secret {
			v = h.settingDefault(d.Key)
		}
		values[d.Key] = v
	}
	defs := make([]map[string]any, 0, len(settingDefs))
	for _, d := range settingDefs {
		defs = append(defs, map[string]any{"key": d.Key, "secret": d.Secret, "bool": d.Bool})
	}
	oc := h.effectiveOIDC(ctx)
	mail := h.effectiveMail(ctx)
	return map[string]any{
		"success": true, "values": values, "has": has, "defs": defs,
		"effective": map[string]any{
			"password_enabled":   h.passwordLoginEnabled(ctx),
			"email_code_enabled": h.emailCodeLoginEnabled(ctx),
			"oidc_enabled":       oc.Enabled,
			"oidc_provider":      oc.ProviderName,
			"mail_configured":    mail.Ready(),
			"mail_provider":      mail.ResolvedProvider(),
			"turnstile_required": h.turnstileSecret(ctx) != "",
			"public_base_url":    h.publicBaseURL(ctx),
			"agent_dist_base":    h.agentBinaryBaseURL(),
			"component_base":     h.componentBaseURL(),
		},
	}
}

func boolString(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "on", "yes":
		return "1", true
	case "0", "false", "off", "no", "":
		return "0", true
	}
	return "", false
}

// GET/PUT /api/settings
func (h *handlers) settingsAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.settingsSnapshot(r))
	case http.MethodPut, http.MethodPost:
		var body map[string]any
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
		allowed := map[string]settingDef{}
		for _, d := range settingDefs {
			allowed[d.Key] = d
		}
		changes := map[string]string{}
		for key, raw := range body {
			def, ok := allowed[key]
			if !ok {
				continue
			}
			v := strings.TrimSpace(jsonStr(raw))
			if def.Secret {
				if v == "" {
					continue // 空=保持原值
				}
				if v == "__clear__" {
					v = ""
				}
			}
			if def.Bool {
				b, ok := boolString(v)
				if !ok {
					writeError(w, http.StatusBadRequest, key+" 只能是 0/1")
					return
				}
				v = b
			}
			switch key {
			case settingPublicBaseURL, settingOIDCIssuer, settingComponentBase, settingAgentBinaryBase:
				if v != "" {
					u, err := url.Parse(v)
					if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
						writeError(w, http.StatusBadRequest, key+" 必须是 http(s) URL")
						return
					}
				}
			case settingSMTPAddr:
				if v != "" && !strings.Contains(v, ":") {
					writeError(w, http.StatusBadRequest, "smtp_addr 需要 host:port")
					return
				}
			case settingMailFrom:
				if v != "" && normalizeEmail(v) == "" {
					writeError(w, http.StatusBadRequest, "mail_from 不是合法邮箱")
					return
				}
			case settingMailDomain:
				v = strings.ToLower(strings.TrimSpace(v))
				v = strings.TrimPrefix(v, "@")
				if v != "" && (!strings.Contains(v, ".") || strings.ContainsAny(v, " /\\")) {
					writeError(w, http.StatusBadRequest, "mail_domain 需要是合法域名，例如 proxypilot.yangzifun.org")
					return
				}
			case settingMailDKIMSelect:
				v = strings.ToLower(strings.TrimSpace(v))
				if v != "" && strings.ContainsAny(v, " .@/\\") {
					writeError(w, http.StatusBadRequest, "mail_dkim_selector 只能包含字母数字与短横线")
					return
				}
			case settingMailEgressIP:
				v = strings.TrimSpace(v)
				if v != "" && net.ParseIP(v) == nil {
					writeError(w, http.StatusBadRequest, "mail_egress_ip 不是合法 IP")
					return
				}
			case settingMailProvider:
				v = strings.ToLower(v)
				if v != "" && v != "auto" && v != "smtp" && v != "builtin" {
					writeError(w, http.StatusBadRequest, "mail_provider 只能是 auto / builtin / smtp")
					return
				}
			case "monitor_notify_emails":
				v = normalizeEmailList(v)
			case "monitor_notify_cooldown_min", "uuid_reconcile_interval_min",
				"batch_retention_days", "agent_task_retention_days", "metrics_retention_days":
				if v != "" {
					n := intOrJS(v, -1, -1, 1<<30)
					if n < 0 {
						writeError(w, http.StatusBadRequest, key+" 必须是整数")
						return
					}
					lo, hi := int64(1), int64(365)
					switch key {
					case "monitor_notify_cooldown_min":
						lo, hi = 1, 1440
					case "uuid_reconcile_interval_min":
						lo, hi = 5, 1440
					}
					if n < lo || n > hi {
						writeError(w, http.StatusBadRequest, key+" 超出范围（"+itoa64(lo)+"-"+itoa64(hi)+"）")
						return
					}
				}
			}
			changes[key] = v
		}

		// 先在“拟保存后的状态”上做锁定校验，避免把三种登录方式全部关掉。
		// 注意：未在 app_settings 中显式配置时，要使用环境变量/默认值，而不是当成关闭。
		ctx := r.Context()
		// 安全敏感设置只允许超级管理员修改（审计 P2-14 提权链）。
		// 只拦「真的改了」的键：设置页会整表单回传当前值，未修改不应报错。
		claims, hasClaims := claimsFrom(r)
		isSuper := hasClaims && h.cfg.SuperAdmin != "" && claims.Sub == h.cfg.SuperAdmin
		if !isSuper {
			for key, v := range changes {
				if !isSuperOnlySetting(key) {
					continue
				}
				if !h.sensitiveSettingChanged(ctx, key, v) {
					continue
				}
				writeError(w, http.StatusForbidden, "仅超级管理员可修改安全敏感设置（"+key+"）")
				return
			}
		}
		pick := func(key string) string {
			if v, ok := changes[key]; ok {
				return v
			}
			return h.settingValue(ctx, key)
		}
		pwOn := h.passwordLoginEnabled(ctx)
		if v, ok := changes[settingPasswordEnabled]; ok {
			pwOn = v == "1"
		}
		emailOn := h.emailCodeLoginEnabled(ctx)
		if v, ok := changes[settingEmailCode]; ok {
			emailOn = v == "1"
		}
		mail := h.effectiveMail(ctx)
		if v, ok := changes[settingMailProvider]; ok {
			mail.Provider = v
		}
		if v, ok := changes[settingSMTPAddr]; ok {
			mail.Addr = v
		}
		if v, ok := changes[settingMailFrom]; ok {
			mail.From = v
		}
		if v, ok := changes[settingSMTPUser]; ok {
			mail.User = v
		}
		if v, ok := changes[settingSMTPPass]; ok {
			mail.Pass = v
		}
		if v, ok := changes[settingCFToken]; ok {
			mail.CFToken = v
		}
		if v, ok := changes[settingCFAccount]; ok {
			mail.CFAccountID = v
		}
		mailConfigured := mail.Ready()
		oidcOn := h.settingBool(ctx, settingOIDCEnabled, true)
		if v, ok := changes[settingOIDCEnabled]; ok {
			oidcOn = v == "1"
		}
		oc := h.effectiveOIDC(ctx)
		issuer, clientID := oc.Issuer, oc.ClientID
		if v, ok := changes[settingOIDCIssuer]; ok {
			issuer = v
		}
		if v, ok := changes[settingOIDCClientID]; ok {
			clientID = v
		}
		oidcUsable := oidcOn && issuer != "" && clientID != ""
		if !pwOn && !(emailOn && mailConfigured) && !oidcUsable {
			writeError(w, http.StatusBadRequest, "密码、邮箱验证码（需配置 SMTP）、OIDC 至少保留一种可用登录方式，否则将无法登录")
			return
		}
		_ = pick

		for key, v := range changes {
			if err := h.saveSetting(ctx, key, v); err != nil {
				writeError(w, http.StatusInternalServerError, "保存 "+key+" 失败")
				return
			}
		}
		h.invalidateSettings()
		writeJSON(w, http.StatusOK, h.settingsSnapshot(r))
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}
