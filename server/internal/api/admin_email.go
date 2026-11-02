package api

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

const bindEmailPurpose = "bind_email"

func adminTargetUsername(body map[string]any) string {
	if u := strings.TrimSpace(mapStr(body, "username", "")); u != "" {
		return u
	}
	return strings.TrimSpace(mapStr(body, "user", ""))
}

// POST /api/admin/users/email/code —— 发送绑定邮箱验证码
func (h *handlers) adminEmailCodeSend(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	username := adminTargetUsername(body)
	email := normalizeEmail(mapStr(body, "email", ""))
	if username == "" || email == "" {
		writeError(w, http.StatusBadRequest, "用户名与邮箱不能为空")
		return
	}
	var exists int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM admin_users WHERE username = ?`, username).Scan(&exists)
	if exists == 0 {
		writeError(w, http.StatusNotFound, "管理员不存在")
		return
	}
	var taken int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM admin_users WHERE LOWER(email) = ? AND username <> ?`, email, username).Scan(&taken)
	if taken > 0 {
		writeError(w, http.StatusConflict, "该邮箱已被其他管理员绑定")
		return
	}
	if code, msg := h.checkEmailCodeRate(r.Context(), email, bindEmailPurpose); code != 0 {
		writeError(w, code, msg)
		return
	}
	code, err := h.issueEmailCode(r.Context(), email, username, bindEmailPurpose)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成验证码失败")
		return
	}
	intro := fmt.Sprintf(`管理员账号：<b>%s</b><br>时间：%s<br>操作 IP：%s`,
		html.EscapeString(username), time.Now().Format("2006-01-02 15:04:05 MST"), html.EscapeString(clientIP(r)))
	if base := h.publicBaseURL(r.Context()); base != "" {
		intro += fmt.Sprintf(`<br>后台地址：<a href="%s">%s</a>`, html.EscapeString(base), html.EscapeString(base))
	}
	intro += `<br><br>请输入下面的 6 位验证码完成邮箱绑定。如非本人操作，请忽略本邮件。`
	mailBody := emailHTML("绑定管理员邮箱", intro, code, 10)
	if err := h.sendMail(email, "【代理配置管理】绑定邮箱验证码", mailBody); err != nil {
		writeError(w, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "ttl_minutes": 10})
}

// POST /api/admin/users/email/verify —— 校验并完成绑定
func (h *handlers) adminEmailVerify(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	username := adminTargetUsername(body)
	email := normalizeEmail(mapStr(body, "email", ""))
	code := strings.TrimSpace(mapStr(body, "code", ""))
	if username == "" || email == "" || len(code) != 6 {
		writeError(w, http.StatusBadRequest, "参数不完整")
		return
	}
	ok, status, msg := h.consumeEmailCode(r.Context(), email, code, bindEmailPurpose)
	if !ok {
		writeError(w, status, msg)
		return
	}
	now := nowMillis()
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE admin_users SET email = ?, email_verified = 1, email_verified_at = ? WHERE username = ?`,
		email, now, username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "绑定失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "管理员不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "email": email, "email_verified": true})
}

// POST /api/admin/users/email/unbind
func (h *handlers) adminEmailUnbind(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	username := adminTargetUsername(body)
	if username == "" {
		writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE admin_users SET email = NULL, email_verified = 0, email_verified_at = 0 WHERE username = ?`, username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "解绑失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "管理员不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
