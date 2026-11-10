package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// 自建直投的管理接口：DNS 指引、发送测试、轮换 DKIM。

// GET /api/admin/mail/guide —— 发信配置现状 + 需要的 DNS 记录（带实时校验）
func (h *handlers) mailGuideGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mc := h.effectiveMail(ctx)

	domain := strings.TrimSpace(mc.Domain)
	if domain == "" && strings.TrimSpace(mc.From) != "" {
		if _, d, err := splitMailAddress(mc.From); err == nil {
			domain = d
		}
	}

	publicB64 := ""
	if strings.TrimSpace(mc.DKIMPrivate) != "" {
		if v, err := dkimPublicFromPrivate(mc.DKIMPrivate); err == nil {
			publicB64 = v
		} else {
			h.log.Warn("读取 DKIM 私钥失败", "err", err)
		}
	}

	var records []dnsRecord
	if domain != "" {
		records = h.mailDNSGuide(domain, mc.dkimSelector(), publicB64, h.mailEgressIP())
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"provider":        mc.ResolvedProvider(),
		"provider_raw":    mc.Provider,
		"domain":          domain,
		"from":            mc.From,
		"selector":        mc.dkimSelector(),
		"egress_ip":       h.mailEgressIP(),
		"has_dkim":        publicB64 != "",
		"ready":           mc.BuiltinReady() && publicB64 != "",
		"records":         records,
		"public_key_hint": shortText(publicB64, 40),
	})
}

// POST /api/admin/mail/dkim —— 生成 / 轮换 DKIM 密钥
func (h *handlers) mailDKIMRotate(w http.ResponseWriter, r *http.Request) {
	kp, err := generateDKIMKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := h.saveSetting(r.Context(), settingMailDKIMKey, kp.PrivatePEM); err != nil {
		writeError(w, http.StatusInternalServerError, "保存私钥失败："+err.Error())
		return
	}
	h.invalidateSettings()
	h.log.Info("已重新生成 DKIM 密钥")
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"public_b64": kp.PublicB64,
		"message":    "已生成新的 DKIM 密钥，请把新的 TXT 记录更新到 DNS",
	})
}

// POST /api/admin/mail/test —— 发送一封测试邮件
func (h *handlers) mailTestSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "参数错误")
		return
	}
	to := normalizeEmail(body.To)
	if to == "" {
		writeError(w, http.StatusBadRequest, "请输入合法的收件邮箱")
		return
	}

	mc := h.effectiveMail(r.Context())
	provider := mc.ResolvedProvider()
	if provider == "" {
		writeError(w, http.StatusBadRequest, "发信通道未配置完整（自建直投需要发信域名 + 发件人）")
		return
	}

	html := emailHTML("邮件通道测试",
		"这封邮件由 ProxyPilot 面板发出，用于验证发信配置是否可用。<br>"+
			"发信通道：<b>"+provider+"</b>；发件人：<b>"+mc.From+"</b>。",
		"", 0)

	// 直投是长连接，异步发送避免前端等待超时
	go func() {
		if err := h.sendMail(to, "ProxyPilot 邮件通道测试", html); err != nil {
			h.log.Warn("测试邮件发送失败", "to", maskEmailForLog(to), "err", err)
			return
		}
		h.log.Info("测试邮件已发送", "to", maskEmailForLog(to), "provider", provider)
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"provider": provider,
		"message":  "已提交发送，请检查收件箱（也在垃圾箱看一下）",
	})
}