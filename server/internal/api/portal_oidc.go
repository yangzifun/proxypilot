package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// 用户中心 SSO（与管理员共用同一套 OIDC 配置，同一个回调地址）。
//
// 为什么共用回调：IdP 里只需要登记一个 redirect_uri，运维不必配两遍。
// 区分靠 tx.Mode（存在 pp_oidc_tx cookie 里），回调时决定签发管理员令牌还是用户令牌。

const oidcModePortal = "portal"
const oidcModeAdmin = "admin"

// GET /api/portal/oidc/status —— 用户中心登录页用它决定是否显示 SSO 按钮
func (h *handlers) portalOIDCStatus(w http.ResponseWriter, r *http.Request) {
	oc := h.effectiveOIDC(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"success":       true,
		"enabled":       oc.Enabled,
		"provider_name": firstNonEmpty(oc.ProviderName, "OIDC 登录"),
		"button_text":   firstNonEmpty(oc.ButtonText, "使用 SSO 登录"),
	})
}

// GET /api/portal/oidc/login —— 发起用户端 SSO
func (h *handlers) portalOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !h.oidcEnabled() {
		writeError(w, http.StatusNotFound, "OIDC 未启用")
		return
	}
	// 人机验证可选：配置了 Turnstile 就必须带
	if err := h.verifyTurnstile(r, r.URL.Query().Get("cf_turnstile_response")); err != nil {
		oidcFail(w, r, oidcModePortal, "turnstile_failed")
		return
	}
	d, err := h.oidcDiscover(r)
	if err != nil || d.AuthorizationEndpoint == "" {
		writeError(w, http.StatusBadGateway, "OIDC 发现文档读取失败")
		return
	}
	verifier := randomVerifier()
	tx := oidcTx{State: oidcState(oidcModePortal), Verifier: verifier, Nonce: randomState(), Mode: oidcModePortal}
	// 用用户中心专用 cookie 名，避免和管理端的事务互相覆盖
	http.SetCookie(w, portalOidcTxCookie(r, tx, 600))

	oc := h.effectiveOIDC(r.Context())
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", oc.ClientID)
	q.Set("redirect_uri", h.oidcRedirectURI(r))
	q.Set("scope", firstNonEmpty(oc.Scopes, "openid profile email"))
	q.Set("state", tx.State)
	q.Set("nonce", tx.Nonce)
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(d.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	http.Redirect(w, r, d.AuthorizationEndpoint+sep+q.Encode(), http.StatusFound)
}

// finishPortalOIDC 把 OIDC 身份换成用户中心令牌。
// 规则：邮箱必须已绑定到某个 uuid_info（即管理员已建好用户）。
func (h *handlers) finishPortalOIDC(r *http.Request, email string) (token, uuid, reason string) {
	emailNorm := normalizeEmail(email)
	if emailNorm == "" {
		return "", "", "email_missing"
	}

	var uuidVal, status string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT uuid, COALESCE(status,'') FROM uuid_info WHERE email_norm = ? LIMIT 1`,
		emailNorm).Scan(&uuidVal, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "no_uuid_binding"
	}
	if err != nil {
		h.log.Error("SSO 查询 UUID 失败", "err", err)
		return "", "", "lookup_failed"
	}
	if st := strings.ToLower(strings.TrimSpace(status)); st == "inactive" || st == "suspended" {
		return "", "", "account_disabled"
	}

	tk, terr := auth.IssuePortalTokenTTL(emailNorm, h.cfg.JWTSecret, h.cfg.PortalTokenTTL)
	if terr != nil {
		return "", "", "token_sign_failed"
	}
	return tk, uuidVal, ""
}

// portalOIDCErrorText 把错误码翻成中文，直接显示在登录页。
func portalOIDCErrorText(code string) string {
	switch code {
	case "turnstile_failed":
		return "人机验证未通过"
	case "state_mismatch":
		return "登录状态已失效，请重试"
	case "missing_code", "token_failed", "token_rejected":
		return "身份提供方返回异常，请重试"
	case "id_token_missing", "id_token_invalid", "jwks_failed":
		return "身份令牌校验失败"
	case "userinfo_failed":
		return "读取用户信息失败"
	case "email_missing":
		return "身份提供方未返回邮箱，无法匹配订阅"
	case "email_unverified":
		return "该邮箱在身份提供方处未验证"
	case "no_uuid_binding":
		return "这个邮箱还没有绑定订阅，请联系管理员"
	case "account_disabled":
		return "该账号已被停用"
	default:
		return "SSO 登录失败：" + code
	}
}

// keep imports used
var _ = json.Marshal
var _ = io.ReadAll