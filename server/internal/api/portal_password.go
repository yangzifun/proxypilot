package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// 用户中心的密码登录与改密。
//
// 用户实体是 uuid_info（一个邮箱绑定一个 UUID）。管理员新建用户时随机生成初始密码
// 并一次性展示，用户登录后可在用户中心自行修改。

// pwdAlphabet 去掉了 0/O/1/l/I 等易混淆字符，方便手抄。
const pwdAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789"

const (
	portalPasswordMinLen = 8
	portalPasswordMaxLen = 72 // pbkdf2 本身不限，这里防超长输入
)

// randomPassword 生成 n 位随机密码（crypto/rand）。
func randomPassword(n int) string {
	if n <= 0 {
		n = 12
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// 极端情况下退化为固定长度占位，不让调用方拿到空密码
		return strings.Repeat("ProxyPilot", 2)[:n]
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = pwdAlphabet[int(b)%len(pwdAlphabet)]
	}
	return string(out)
}

// validatePortalPassword 检查口令强度（只做长度与基本可用性）。
func validatePortalPassword(pw string) error {
	if len(pw) < portalPasswordMinLen {
		return errors.New("密码至少 8 位")
	}
	if len(pw) > portalPasswordMaxLen {
		return errors.New("密码过长")
	}
	if strings.TrimSpace(pw) != pw {
		return errors.New("密码首尾不能有空格")
	}
	return nil
}

// setPortalPassword 写入口令哈希；mustChange=1 表示首登需改。
func (h *handlers) setPortalPassword(ctx context.Context, uuid, plain string, mustChange bool) error {
	hash, err := auth.HashPassword(plain)
	if err != nil {
		return err
	}
	flag := 0
	if mustChange {
		flag = 1
	}
	_, err = h.db.ExecContext(ctx,
		`UPDATE uuid_info SET password_hash = ?, password_updated_at = ?, password_must_change = ?
		 WHERE uuid = ?`,
		hash, nowMillis(), flag, uuid)
	return err
}

// issueInitialPassword 给新建的用户生成初始密码并落库。
// 返回明文（只在此刻返回一次），调用方负责展示给管理员。
func (h *handlers) issueInitialPassword(ctx context.Context, uuid string) (string, error) {
	pw := randomPassword(12)
	if err := h.setPortalPassword(ctx, uuid, pw, true); err != nil {
		return "", err
	}
	return pw, nil
}

// portalPasswordRow 读取某邮箱对应的口令状态。
type portalPasswordRow struct {
	UUID       string
	Email      string
	Hash       string
	MustChange bool
}

func (h *handlers) findPortalPasswordByEmail(ctx context.Context, emailNorm string) (portalPasswordRow, error) {
	var row portalPasswordRow
	var mustChange int64
	err := h.db.QueryRowContext(ctx,
		`SELECT uuid, COALESCE(email,''), COALESCE(password_hash,''), COALESCE(password_must_change,0)
		   FROM uuid_info WHERE email_norm = ? LIMIT 1`, emailNorm).
		Scan(&row.UUID, &row.Email, &row.Hash, &mustChange)
	row.MustChange = mustChange == 1
	return row, err
}

// POST /api/portal/login/password —— 邮箱 + 密码登录
func (h *handlers) portalLoginPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	emailNorm := normalizeEmail(req.Email)
	if emailNorm == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "请输入密码")
		return
	}

	// 与管理员登录共用限速：按 IP + 邮箱 维度
	key := loginRateKey(r, "portal:"+emailNorm)
	if wait, blocked := loginLimiter.Blocked(key); blocked {
		w.Header().Set("Retry-After", retryAfterSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "尝试过于频繁，请 "+retryAfterSeconds(wait)+" 秒后再试")
		return
	}

	row, err := h.findPortalPasswordByEmail(r.Context(), emailNorm)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}
	if err != nil {
		h.log.Error("查询用户口令失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if strings.TrimSpace(row.Hash) == "" {
		writeError(w, http.StatusBadRequest, "该账号还没设置密码，请用邮箱验证码登录")
		return
	}

	ok, needsRehash := auth.VerifyPassword(row.Hash, req.Password)
	if !ok {
		writeError(w, http.StatusUnauthorized, "邮箱或密码错误")
		return
	}
	loginLimiter.Reset(key)

	// 老格式哈希在登录成功后顺手升级
	if needsRehash {
		if hash, herr := auth.HashPassword(req.Password); herr == nil {
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE uuid_info SET password_hash = ? WHERE uuid = ?`, hash, row.UUID)
		}
	}

	token, err := auth.IssuePortalTokenTTL(emailNorm, h.cfg.JWTSecret, h.cfg.PortalTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":                token,
		"token_ttl_ms":         h.cfg.PortalTokenTTL.Milliseconds(),
		"email":                emailNorm,
		"uuid":                 row.UUID,
		"must_change_password": row.MustChange,
	})
}

// POST /api/portal/password —— 用户自行修改密码
func (h *handlers) portalPasswordChange(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if err := validatePortalPassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	row, err := h.findPortalPasswordByEmail(r.Context(), normalizeEmail(email))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}

	// 已设置过密码就必须验证旧密码；首次设置（哈希为空）允许直接设置
	if strings.TrimSpace(row.Hash) != "" {
		if ok, _ := auth.VerifyPassword(row.Hash, req.OldPassword); !ok {
			writeError(w, http.StatusBadRequest, "原密码不正确")
			return
		}
	}
	if err := h.setPortalPassword(r.Context(), row.UUID, req.NewPassword, false); err != nil {
		h.log.Error("修改密码失败", "err", err)
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	h.log.Info("用户已修改密码", "email", emailNormForLog(email), "uuid", row.UUID)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "密码已更新"})
}

// GET /api/portal/password —— 当前账号的密码状态（是否已设置 / 是否需改密）
func (h *handlers) portalPasswordStatus(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	row, err := h.findPortalPasswordByEmail(r.Context(), normalizeEmail(email))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":              true,
		"email":                email,
		"uuid":                 row.UUID,
		"has_password":         strings.TrimSpace(row.Hash) != "",
		"must_change_password": row.MustChange,
	})
}

// POST /api/uuids/{uuid}/password —— 管理员重置用户密码，返回明文一次
func (h *handlers) uuidResetPassword(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("uuid"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "缺少 uuid")
		return
	}
	var req struct {
		// 可指定明文；留空则随机生成
		Password string `json:"password"`
		// MustChange 默认 true，避免管理员设置的密码被长期共用
		MustChange *bool `json:"must_change"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req)

	var email string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(email,'') FROM uuid_info WHERE uuid = ?`, id).Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "用户不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if strings.TrimSpace(email) == "" {
		writeError(w, http.StatusBadRequest, "该用户还没绑定邮箱，无法使用密码登录")
		return
	}

	mustChange := true
	if req.MustChange != nil {
		mustChange = *req.MustChange
	}
	plain := strings.TrimSpace(req.Password)
	if plain == "" {
		plain = randomPassword(12)
	} else if err := validatePortalPassword(plain); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.setPortalPassword(r.Context(), id, plain, mustChange); err != nil {
		h.log.Error("重置用户密码失败", "err", err)
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	h.log.Info("管理员重置了用户密码", "uuid", id, "email", emailNormForLog(email))
	writeJSON(w, http.StatusOK, map[string]any{
		"success":              true,
		"uuid":                 id,
		"email":                email,
		"password":             plain, // 只在这里返回一次
		"must_change_password": mustChange,
	})
}

// emailNormForLog 日志里只保留域名，避免泄露完整地址。
func emailNormForLog(email string) string {
	email = strings.TrimSpace(email)
	if at := strings.LastIndex(email, "@"); at > 0 {
		return "***@" + email[at+1:]
	}
	return "***"
}