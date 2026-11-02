package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

const maxAdminUsers = 50

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

type adminUserRow struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	Email           any    `json:"email"`
	EmailVerified   int64  `json:"email_verified"`
	EmailVerifiedAt int64  `json:"email_verified_at"`
	MFAEnabled      int64  `json:"mfa_enabled"`
	LastMFALogin    int64  `json:"last_mfa_login"`
	CreatedAt       int64  `json:"created_at"`
}

// GET/POST/PUT/DELETE /api/admin/users（超管）
func (h *handlers) adminUsersDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.adminUsersList(w, r)
	case http.MethodPost:
		h.adminUsersCreate(w, r)
	case http.MethodPut:
		h.adminUsersUpdate(w, r)
	case http.MethodDelete:
		h.adminUsersDelete(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (h *handlers) adminUsersList(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsFrom(r)
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, username, email, COALESCE(email_verified,0), COALESCE(email_verified_at,0),
		        COALESCE(mfa_enabled,0), COALESCE(last_mfa_login,0), COALESCE(created_at,0)
		   FROM admin_users ORDER BY id ASC`)
	if err != nil {
		h.log.Error("查询管理员失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()
	users := []adminUserRow{}
	for rows.Next() {
		var (
			u     adminUserRow
			email sql.NullString
		)
		if rows.Scan(&u.ID, &u.Username, &email, &u.EmailVerified, &u.EmailVerifiedAt,
			&u.MFAEnabled, &u.LastMFALogin, &u.CreatedAt) == nil {
			if email.Valid {
				u.Email = email.String
			}
			users = append(users, u)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "self": claims.Sub})
}

type adminUserRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Email       string `json:"email"`
	NewPassword string `json:"newPassword"`
	MFAReset    bool   `json:"mfaReset"`
}

func (h *handlers) adminUsersCreate(w http.ResponseWriter, r *http.Request) {
	var req adminUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	if !usernameRe.MatchString(username) {
		writeError(w, http.StatusBadRequest, "用户名仅支持字母、数字与 . _ @ -（最长 64 位）")
		return
	}
	if len(req.Password) < 6 {
		writeError(w, http.StatusBadRequest, "密码至少 6 位")
		return
	}
	rawEmail := strings.TrimSpace(req.Email)
	email := ""
	if rawEmail != "" {
		email = normalizeEmail(rawEmail)
		if email == "" {
			writeError(w, http.StatusBadRequest, "邮箱格式无效")
			return
		}
	}
	var n int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM admin_users WHERE username = ?`, username).Scan(&n); err == nil && n > 0 {
		writeError(w, http.StatusConflict, "用户名已存在")
		return
	}
	var count int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM admin_users`).Scan(&count)
	if count >= maxAdminUsers {
		writeError(w, http.StatusBadRequest, "管理员账号数已达上限（50）")
		return
	}
	if email != "" {
		var dupUser string
		err := h.db.QueryRowContext(r.Context(),
			`SELECT username FROM admin_users WHERE LOWER(email) = ? LIMIT 1`, email).Scan(&dupUser)
		if err == nil {
			writeError(w, http.StatusConflict, "该邮箱已绑定给账号 "+dupUser)
			return
		}
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	var emailArg any
	if email != "" {
		emailArg = email
	}
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO admin_users (username, password_hash, mfa_enabled, email, email_verified, email_verified_at, created_at)
		 VALUES (?, ?, 0, ?, 0, 0, ?)`, username, hash, emailArg, nowMillis()); err != nil {
		h.log.Error("创建管理员失败", "err", err)
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "username": username})
}

func (h *handlers) adminUsersUpdate(w http.ResponseWriter, r *http.Request) {
	rawBody, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var req adminUserRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	var rawMap map[string]json.RawMessage
	_ = json.Unmarshal(rawBody, &rawMap)
	_, emailProvided := rawMap["email"]
	username := strings.TrimSpace(req.Username)
	if !usernameRe.MatchString(username) {
		writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	var (
		curEmail         sql.NullString
		curEmailVerified int64
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT email, COALESCE(email_verified,0) FROM admin_users WHERE username = ? LIMIT 1`, username).
		Scan(&curEmail, &curEmailVerified)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}

	// 只有请求显式带了 email 字段才处理（空字符串表示解绑）
	if emailProvided {
		rawEmail := strings.TrimSpace(req.Email)
		email := ""
		if rawEmail != "" {
			email = normalizeEmail(rawEmail)
			if email == "" {
				writeError(w, http.StatusBadRequest, "邮箱格式无效")
				return
			}
		}
		unchanged := email != "" && strings.EqualFold(curEmail.String, email) && curEmailVerified == 1
		if !unchanged {
			if email != "" {
				var dupUser string
				if err := h.db.QueryRowContext(r.Context(),
					`SELECT username FROM admin_users WHERE LOWER(email) = ? AND username <> ? LIMIT 1`,
					email, username).Scan(&dupUser); err == nil {
					writeError(w, http.StatusConflict, "该邮箱已绑定给账号 "+dupUser)
					return
				}
			}
			var emailArg any
			if email != "" {
				emailArg = email
			}
			tx, err := h.db.BeginTx(r.Context(), nil)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "更新失败")
				return
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE admin_users SET email = ?, email_verified = 0, email_verified_at = 0 WHERE username = ?`,
				emailArg, username); err != nil {
				writeError(w, http.StatusInternalServerError, "更新失败")
				return
			}
			if _, err := tx.ExecContext(r.Context(),
				`UPDATE admin_email_codes SET used = 1 WHERE username = ? AND used = 0`, username); err != nil {
				writeError(w, http.StatusInternalServerError, "更新失败")
				return
			}
			if err := tx.Commit(); err != nil {
				writeError(w, http.StatusInternalServerError, "更新失败")
				return
			}
		}
	}

	if req.NewPassword != "" {
		if len(req.NewPassword) < 6 {
			writeError(w, http.StatusBadRequest, "新密码至少 6 位")
			return
		}
		hash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE admin_users SET password_hash = ? WHERE username = ?`, hash, username); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
	}

	if req.MFAReset {
		tx, err := h.db.BeginTx(r.Context(), nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE admin_users SET mfa_enabled = 0, mfa_secret = NULL WHERE username = ?`, username); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
		if _, err := tx.ExecContext(r.Context(),
			`DELETE FROM mfa_backup_codes WHERE username = ?`, username); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (h *handlers) adminUsersDelete(w http.ResponseWriter, r *http.Request) {
	var req adminUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	username := strings.TrimSpace(req.Username)
	claims, _ := claimsFrom(r)
	if username == "" {
		writeError(w, http.StatusBadRequest, "用户名不能为空")
		return
	}
	if username == claims.Sub {
		writeError(w, http.StatusBadRequest, "不能删除当前登录账号")
		return
	}
	var count int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM admin_users`).Scan(&count)
	if count <= 1 {
		writeError(w, http.StatusBadRequest, "至少保留一个管理员账号")
		return
	}
	var exists int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM admin_users WHERE username = ?`, username).Scan(&exists)
	if exists == 0 {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM mfa_backup_codes WHERE username = ?`,
		`DELETE FROM admin_oidc_identities WHERE username = ?`,
		`DELETE FROM admin_email_codes WHERE username = ?`,
		`DELETE FROM admin_users WHERE username = ?`,
	} {
		if _, err := tx.ExecContext(r.Context(), q, username); err != nil {
			writeError(w, http.StatusInternalServerError, "删除失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
