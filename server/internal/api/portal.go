package api

import (
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

const portalLoginPurpose = "portal_login"
const portalEmailPurpose = "portal_email_change"

// portalSession 从 Bearer token 解析用户端会话（role=user）。
func (h *handlers) portalSession(r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(raw, "Bearer ") {
		return "", false
	}
	claims, err := auth.Parse(strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")), h.cfg.JWTSecret)
	if err != nil || claims.Role != "user" || claims.Sub == "" {
		return "", false
	}
	return normalizeEmail(claims.Sub), true
}

// POST /api/portal/send-code
func (h *handlers) portalSendCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email     string `json:"email"`
		Turnstile string `json:"cf_turnstile_response"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if err := h.verifyTurnstile(r, req.Turnstile); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	email := normalizeEmail(req.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	var bound int
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM uuid_info WHERE LOWER(email) = ?`, email).Scan(&bound); err != nil {
		bound = 0
	}
	if bound == 0 {
		writeError(w, http.StatusNotFound, "该邮箱还没有绑定任何 UUID，请联系管理员")
		return
	}
	if code, msg := h.checkEmailCodeRate(r.Context(), email, portalLoginPurpose); code != 0 {
		writeError(w, code, msg)
		return
	}
	code, err := h.issueEmailCode(r.Context(), email, email, portalLoginPurpose)
	if err != nil {
		h.log.Error("写入验证码失败", "err", err)
		writeError(w, http.StatusInternalServerError, "验证码写入失败")
		return
	}
	ttl := int(emailCodeTTL.Minutes())
	if err := h.sendMail(email, "【ProxyPilot】用户中心登录验证码",
		emailHTML("用户中心登录验证码", "您正在登录 ProxyPilot 用户中心，请在页面中输入下面的 6 位验证码：", code, ttl)); err != nil {
		if errors.Is(err, errMailNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "验证码邮件发送失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"email":      email,
		"expires_in": int(emailCodeTTL.Seconds()),
	})
}

// POST /api/portal/login
func (h *handlers) portalLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	email := normalizeEmail(req.Email)
	code := strings.TrimSpace(req.Code)
	if email == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	if len(code) != 6 || !isDigits(code) {
		writeError(w, http.StatusBadRequest, "请输入 6 位数字验证码")
		return
	}
	ok, status, msg := h.consumeEmailCode(r.Context(), email, code, portalLoginPurpose)
	if !ok {
		writeError(w, status, msg)
		return
	}
	token, err := auth.IssuePortalToken(email, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"email":      email,
		"expires_in": 7 * 24 * 3600,
	})
}

// GET /api/portal/uuids
func (h *handlers) portalUuids(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT uuid, owner, remark, status, COALESCE(created_at,0), COALESCE(updated_at,0)
		   FROM uuid_info WHERE LOWER(email) = ? ORDER BY created_at DESC`, email)
	if err != nil {
		h.log.Error("查询用户 UUID 失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()

	type item struct {
		UUID      string            `json:"uuid"`
		Owner     string            `json:"owner"`
		Remark    string            `json:"remark"`
		Status    string            `json:"status"`
		Configs   int64             `json:"configs"`
		CreatedAt int64             `json:"created_at"`
		UpdatedAt int64             `json:"updated_at"`
		Sub       map[string]string `json:"sub"`
	}
	var list []item
	var uuids []string
	for rows.Next() {
		var it item
		var status sql.NullString
		if err := rows.Scan(&it.UUID, &it.Owner, &it.Remark, &status, &it.CreatedAt, &it.UpdatedAt); err != nil {
			h.log.Error("扫描用户 UUID 失败", "err", err)
			writeError(w, http.StatusInternalServerError, "解析失败")
			return
		}
		if status.Valid && status.String != "" {
			it.Status = status.String
		} else {
			it.Status = "active"
		}
		list = append(list, it)
		uuids = append(uuids, it.UUID)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "读取失败")
		return
	}

	counts := h.configCounts(r, uuids)
	origin := requestOrigin(r)
	for i := range list {
		u := list[i].UUID
		list[i].Configs = counts[u]
		list[i].Sub = map[string]string{
			"raw":   origin + "/sub/" + url.PathEscape(u),
			"batch": origin + "/batch-configs/" + url.PathEscape(u),
			"clash": origin + "/clash-config?uuid=" + url.QueryEscape(u),
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"email":   email,
		"data":    list,
		"total":   len(list),
	})
}

func (h *handlers) configCounts(r *http.Request, uuids []string) map[string]int64 {
	counts := map[string]int64{}
	for start := 0; start < len(uuids); start += 40 {
		end := start + 40
		if end > len(uuids) {
			end = len(uuids)
		}
		part := uuids[start:end]
		ph := strings.TrimSuffix(strings.Repeat("?,", len(part)), ",")
		args := make([]any, len(part))
		for i, u := range part {
			args[i] = u
		}
		rows, err := h.db.QueryContext(r.Context(),
			`SELECT uuid, COUNT(*) FROM configs WHERE uuid IN (`+ph+`) GROUP BY uuid`, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var u string
			var c int64
			if rows.Scan(&u, &c) == nil {
				counts[u] = c
			}
		}
		rows.Close()
	}
	return counts
}

// POST /api/portal/email/send-code
func (h *handlers) portalEmailSendCode(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	target := normalizeEmail(req.Email)
	if target == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	if target == email {
		writeError(w, http.StatusBadRequest, "新邮箱与当前邮箱相同")
		return
	}
	binding, found := h.bindingByEmail(r.Context(), email)
	if !found {
		writeError(w, http.StatusNotFound, "当前账号还没有绑定 UUID，请联系管理员")
		return
	}
	if taken, exists := h.emailTakenBy(r.Context(), target, binding.UUID); exists {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "该邮箱已绑定其它 UUID，一个邮箱只能绑定一个 UUID",
			"code":  "email_taken", "existing_uuid": taken,
		})
		return
	}
	if code, msg := h.checkEmailCodeRate(r.Context(), target, portalEmailPurpose); code != 0 {
		writeError(w, code, msg)
		return
	}
	code, err := h.issueEmailCode(r.Context(), email, target, portalEmailPurpose)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "验证码写入失败")
		return
	}
	ttl := int(emailCodeTTL.Minutes())
	if err := h.sendMail(target, "【ProxyPilot】修改绑定邮箱验证码",
		emailHTML("修改绑定邮箱", "您正在把 ProxyPilot 账号邮箱修改为当前地址，请输入下面的验证码完成验证：", code, ttl)); err != nil {
		if errors.Is(err, errMailNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, "验证码邮件发送失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"email":      target,
		"expires_in": int(emailCodeTTL.Seconds()),
	})
}

// POST /api/portal/email/verify
func (h *handlers) portalEmailVerify(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	target := normalizeEmail(req.Email)
	code := strings.TrimSpace(req.Code)
	if target == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	if len(code) != 6 || !isDigits(code) {
		writeError(w, http.StatusBadRequest, "请输入 6 位数字验证码")
		return
	}
	binding, found := h.bindingByEmail(r.Context(), email)
	if !found {
		writeError(w, http.StatusNotFound, "当前账号还没有绑定 UUID")
		return
	}
	if ok, status, msg := h.consumeEmailCode(r.Context(), target, code, portalEmailPurpose); !ok {
		writeError(w, status, msg)
		return
	}
	if taken, exists := h.emailTakenBy(r.Context(), target, binding.UUID); exists {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "该邮箱已绑定其它 UUID，一个邮箱只能绑定一个 UUID",
			"code":  "email_taken", "existing_uuid": taken,
		})
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE uuid_info SET email = ?, updated_at = ? WHERE uuid = ?`,
		target, nowMillis(), binding.UUID); err != nil {
		writeError(w, http.StatusInternalServerError, "更新邮箱失败")
		return
	}
	token, err := auth.IssuePortalToken(target, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"email":      target,
		"token":      token,
		"expires_in": 7 * 24 * 3600,
	})
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return scheme + "://" + host
}
