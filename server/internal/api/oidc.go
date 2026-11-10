package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

type oidcDiscovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserinfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSUri               string `json:"jwks_uri"`
	Issuer                string `json:"issuer"`
}

type oidcCacheEntry struct {
	At   time.Time
	Data oidcDiscovery
}

type oidcTx struct {
	State    string `json:"state"`
	Verifier string `json:"verifier"`
	Nonce    string `json:"nonce"`
	// Mode 区分管理员登录与用户中心登录（共用同一个回调地址）
	Mode string `json:"mode,omitempty"`
}

var (
	oidcMu    sync.Mutex
	oidcCache = map[string]oidcCacheEntry{}
	jwksMu    sync.Mutex
	jwksCache = map[string]struct {
		At   time.Time
		Data jwksDoc
	}{}
)

func (h *handlers) oidcEnabled() bool {
	return h.effectiveOIDC(context.Background()).Enabled
}

func (h *handlers) oidcDiscover(r *http.Request) (oidcDiscovery, error) {
	issuer := strings.TrimRight(h.effectiveOIDC(r.Context()).Issuer, "/")
	oidcMu.Lock()
	if e, ok := oidcCache[issuer]; ok && time.Since(e.At) < time.Hour {
		oidcMu.Unlock()
		return e.Data, nil
	}
	oidcMu.Unlock()

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return oidcDiscovery{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return oidcDiscovery{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var d oidcDiscovery
	if err := json.Unmarshal(body, &d); err != nil {
		return oidcDiscovery{}, err
	}
	oidcMu.Lock()
	oidcCache[issuer] = oidcCacheEntry{At: time.Now(), Data: d}
	oidcMu.Unlock()
	return d, nil
}

func (h *handlers) oidcJWKS(r *http.Request, uri string) (jwksDoc, error) {
	if uri == "" {
		return jwksDoc{}, nil
	}
	jwksMu.Lock()
	if e, ok := jwksCache[uri]; ok && time.Since(e.At) < time.Hour {
		jwksMu.Unlock()
		return e.Data, nil
	}
	jwksMu.Unlock()
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, uri, nil)
	if err != nil {
		return jwksDoc{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return jwksDoc{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var j jwksDoc
	if err := json.Unmarshal(body, &j); err != nil {
		return jwksDoc{}, err
	}
	jwksMu.Lock()
	jwksCache[uri] = struct {
		At   time.Time
		Data jwksDoc
	}{At: time.Now(), Data: j}
	jwksMu.Unlock()
	return j, nil
}

func randomState() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomVerifier() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (h *handlers) oidcRedirectURI(r *http.Request) string {
	if v := strings.TrimSpace(h.effectiveOIDC(r.Context()).RedirectURI); v != "" {
		return v
	}
	if base := strings.TrimRight(strings.TrimSpace(h.cfg.PublicBaseURL), "/"); base != "" {
		return base + "/api/oidc/callback"
	}
	scheme := "https"
	if r.TLS == nil && strings.HasPrefix(r.Host, "localhost") {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
	}
	return scheme + "://" + host + "/api/oidc/callback"
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// OIDC 事务 cookie。
//
// 两个坑都踩过，务必保持现状：
//
//  1. Path 必须是 /api，不能是 /api/oidc。
//     用户中心的发起端点是 /api/portal/oidc/login，Path=/api/oidc 不是它的前缀，
//     浏览器会直接丢掉这个 Set-Cookie，回调时 state 对不上（或退化成管理员流程）。
//  2. 管理端与用户中心用不同 cookie 名。
//     同一个浏览器可能刚试过管理端 SSO 又来试用户中心，
//     同名 cookie 互相覆盖会让 mode 串掉。
const (
	oidcTxCookieAdmin  = "pp_oidc_tx"
	oidcTxCookiePortal = "pp_oidc_tx_portal"
	oidcTxCookiePath   = "/api"
)

func oidcTxCookieNamed(r *http.Request, name string, tx oidcTx, maxAge int) *http.Cookie {
	raw, _ := json.Marshal(tx)
	return &http.Cookie{
		Name: name, Value: base64.RawURLEncoding.EncodeToString(raw), Path: oidcTxCookiePath,
		HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	}
}

// oidcTxCookie 管理端使用。
func oidcTxCookie(r *http.Request, tx oidcTx, maxAge int) *http.Cookie {
	return oidcTxCookieNamed(r, oidcTxCookieAdmin, tx, maxAge)
}

// portalOidcTxCookie 用户中心使用。
func portalOidcTxCookie(r *http.Request, tx oidcTx, maxAge int) *http.Cookie {
	return oidcTxCookieNamed(r, oidcTxCookiePortal, tx, maxAge)
}

func readOidcTxNamed(r *http.Request, name string) (oidcTx, bool) {
	c, err := r.Cookie(name)
	if err != nil || c.Value == "" {
		return oidcTx{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return oidcTx{}, false
	}
	var tx oidcTx
	if json.Unmarshal(raw, &tx) != nil {
		return oidcTx{}, false
	}
	return tx, true
}

func readOidcTx(r *http.Request) (oidcTx, bool) {
	return readOidcTxNamed(r, oidcTxCookieAdmin)
}

// clearOidcTx 清掉两个事务 cookie（成功或失败后都要清）。
func clearOidcTx(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, oidcTxCookieNamed(r, oidcTxCookieAdmin, oidcTx{}, -1))
	http.SetCookie(w, oidcTxCookieNamed(r, oidcTxCookiePortal, oidcTx{}, -1))
}

// pickOidcTx 按 state 从两个 cookie 里挑出匹配的那个事务，
// 并返回事务自带的渠道（cookie 里显式记的 mode 优先）。
//
// 用户中心优先：同一浏览器先后试过两端时，portal 的 state 只可能来自 portal 流程。
func pickOidcTx(r *http.Request, state string) (oidcTx, string, bool) {
	if strings.TrimSpace(state) == "" {
		return oidcTx{}, "", false
	}
	for _, name := range []string{oidcTxCookiePortal, oidcTxCookieAdmin} {
		cand, found := readOidcTxNamed(r, name)
		if !found || cand.State == "" || cand.State != state {
			continue
		}
		mode := cand.Mode
		if mode == "" {
			mode = oidcStateMode(state)
		}
		return cand, mode, true
	}
	return oidcTx{}, "", false
}

// oidcState 把渠道编码进 state：回调即使拿不到 cookie，也能知道该回哪个登录页。
func oidcState(mode string) string {
	if mode == "" {
		mode = oidcModeAdmin
	}
	return randomState() + "." + mode
}

// oidcStateMode 从 state 里取渠道；取不到按管理端处理（兼容老链接）。
func oidcStateMode(state string) string {
	if i := strings.LastIndex(state, "."); i >= 0 {
		switch state[i+1:] {
		case oidcModePortal:
			return oidcModePortal
		}
	}
	return oidcModeAdmin
}

// oidcFail 按渠道把失败跳回对应登录页，并清掉事务 cookie。
func oidcFail(w http.ResponseWriter, r *http.Request, mode, code string) {
	clearOidcTx(w, r)
	target := "/login?oidc_error="
	if mode == oidcModePortal {
		target = "/?oidc_error="
	}
	http.Redirect(w, r, target+url.QueryEscape(code), http.StatusFound)
}

// GET /api/oidc/status
func (h *handlers) oidcStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "enabled": h.oidcEnabled(),
		"provider_name":    firstNonEmpty(h.effectiveOIDC(r.Context()).ProviderName, "OIDC 登录"),
		"button_text":      firstNonEmpty(h.effectiveOIDC(r.Context()).ButtonText, "使用 OIDC 登录"),
		"disable_password": !h.passwordLoginEnabled(r.Context()),
	})
}

// GET /api/oidc/login
func (h *handlers) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if !h.oidcEnabled() {
		writeError(w, http.StatusNotFound, "OIDC 未启用")
		return
	}
	if err := h.verifyTurnstile(r, r.URL.Query().Get("cf_turnstile_response")); err != nil {
		oidcFail(w, r, oidcModeAdmin, "turnstile_failed")
		return
	}
	d, err := h.oidcDiscover(r)
	if err != nil || d.AuthorizationEndpoint == "" {
		writeError(w, http.StatusBadGateway, "OIDC 发现文档读取失败")
		return
	}
	verifier := randomVerifier()
	tx := oidcTx{State: oidcState(oidcModeAdmin), Verifier: verifier, Nonce: randomState(), Mode: oidcModeAdmin}
	http.SetCookie(w, oidcTxCookie(r, tx, 600))
	oc := h.effectiveOIDC(r.Context())
	scopes := firstNonEmpty(oc.Scopes, "openid profile email")
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", oc.ClientID)
	q.Set("redirect_uri", h.oidcRedirectURI(r))
	q.Set("scope", scopes)
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

// GET /api/oidc/callback
func (h *handlers) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if !h.oidcEnabled() {
		writeError(w, http.StatusNotFound, "OIDC 未启用")
		return
	}
	q := r.URL.Query()
	// 渠道以 state 后缀为准（cookie 丢了也能把错误送回正确的登录页）
	mode := oidcStateMode(q.Get("state"))
	if errMsg := q.Get("error"); errMsg != "" {
		oidcFail(w, r, mode, errMsg)
		return
	}

	state := q.Get("state")
	tx, pickedMode, ok := pickOidcTx(r, state)
	if ok && pickedMode != "" {
		mode = pickedMode
	}
	if !ok {
		oidcFail(w, r, mode, "state_mismatch")
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	if code == "" {
		oidcFail(w, r, mode, "missing_code")
		return
	}
	d, err := h.oidcDiscover(r)
	if err != nil || d.TokenEndpoint == "" || d.UserinfoEndpoint == "" {
		oidcFail(w, r, mode, "discovery_failed")
		return
	}

	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", h.oidcRedirectURI(r))
	oc := h.effectiveOIDC(r.Context())
	form.Set("client_id", oc.ClientID)
	form.Set("code_verifier", tx.Verifier)
	if oc.ClientSecret != "" {
		form.Set("client_secret", oc.ClientSecret)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	tokenReq, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenReq.Header.Set("Accept", "application/json")
	tokenResp, err := client.Do(tokenReq)
	if err != nil {
		oidcFail(w, r, mode, "token_failed")
		return
	}
	defer tokenResp.Body.Close()
	tokenBody, _ := io.ReadAll(io.LimitReader(tokenResp.Body, 1<<20))
	var tokenData struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	_ = json.Unmarshal(tokenBody, &tokenData)
	if tokenResp.StatusCode >= 400 || tokenData.AccessToken == "" {
		oidcFail(w, r, mode, "token_rejected")
		return
	}
	if tokenData.IDToken == "" {
		oidcFail(w, r, mode, "id_token_missing")
		return
	}
	jwks, err := h.oidcJWKS(r, d.JWKSUri)
	if err != nil {
		oidcFail(w, r, mode, "jwks_failed")
		return
	}
	claims, err := verifyRS256JWT(tokenData.IDToken, jwks, d.Issuer, oc.ClientID, tx.Nonce)
	if err != nil {
		oidcFail(w, r, mode, "id_token_invalid")
		return
	}

	uiReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, d.UserinfoEndpoint, nil)
	uiReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	uiReq.Header.Set("Accept", "application/json")
	uiResp, err := client.Do(uiReq)
	if err != nil {
		oidcFail(w, r, mode, "userinfo_failed")
		return
	}
	defer uiResp.Body.Close()
	uiBody, _ := io.ReadAll(io.LimitReader(uiResp.Body, 1<<20))
	var info struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
	}
	_ = json.Unmarshal(uiBody, &info)
	email := normalizeEmail(firstNonEmpty(info.Email, jsonStr(claims["email"])))
	if email == "" {
		oidcFail(w, r, mode, "email_missing")
		return
	}
	if v, ok := info.EmailVerified.(bool); ok && !v {
		oidcFail(w, r, mode, "email_unverified")
		return
	}
	if v, ok := claims["email_verified"].(bool); ok && !v {
		oidcFail(w, r, mode, "email_unverified")
		return
	}
	// 用户中心登录：换成 portal 令牌后跳回首页
	if mode == oidcModePortal || tx.Mode == oidcModePortal {
		ptoken, uuidVal, reason := h.finishPortalOIDC(r, email)
		if ptoken == "" {
			oidcFail(w, r, oidcModePortal, reason)
			return
		}
		clearOidcTx(w, r)
		h.log.Info("用户中心 SSO 登录成功", "email", emailNormForLog(email), "uuid", uuidVal)
		http.Redirect(w, r, "/?token="+url.QueryEscape(ptoken), http.StatusFound)
		return
	}

	issuer := strings.TrimRight(firstNonEmpty(d.Issuer, oc.Issuer), "/")
	username, reason := h.resolveOIDCAdmin(r.Context(), issuer, jsonStr(claims["sub"]), email)
	if username == "" {
		oidcFail(w, r, mode, reason)
		return
	}
	token, err := auth.IssueAdminTokenTTL(username, h.cfg.JWTSecret, true, h.cfg.AdminTokenTTL)
	if err != nil {
		oidcFail(w, r, mode, "token_sign_failed")
		return
	}
	clearOidcTx(w, r)
	http.Redirect(w, r, "/admin#token="+url.QueryEscape(token), http.StatusFound)
}

// resolveOIDCAdmin 复刻 Worker 的管理员 OIDC 授权模型：
//  1. (issuer, sub) 已绑定 → 直接登录；
//  2. id_token/userinfo 邮箱命中「已验证邮箱」的管理员 → 自动绑定并登录；
//  3. 其他拒绝。
func (h *handlers) resolveOIDCAdmin(ctx context.Context, issuer, sub, email string) (string, string) {
	sub = strings.TrimSpace(sub)
	if sub == "" {
		return "", "no_subject"
	}
	var username string
	err := h.db.QueryRowContext(ctx,
		`SELECT username FROM admin_oidc_identities WHERE issuer = ? AND sub = ? LIMIT 1`,
		issuer, sub).Scan(&username)
	if err == nil && username != "" {
		_, _ = h.db.ExecContext(ctx,
			`UPDATE admin_oidc_identities SET last_login_at = ? WHERE issuer = ? AND sub = ?`,
			nowMillis(), issuer, sub)
		return username, ""
	}
	email = normalizeEmail(email)
	if email == "" {
		return "", "email_missing"
	}
	err = h.db.QueryRowContext(ctx,
		`SELECT username FROM admin_users WHERE LOWER(email) = ? AND email_verified = 1 LIMIT 1`, email).Scan(&username)
	if err != nil || username == "" {
		return "", "no_admin_binding"
	}
	now := nowMillis()
	_, _ = h.db.ExecContext(ctx, `INSERT INTO admin_oidc_identities
	  (username, issuer, sub, email, created_at, last_login_at) VALUES (?, ?, ?, ?, ?, ?)
	  ON DUPLICATE KEY UPDATE username = VALUES(username), email = VALUES(email), last_login_at = VALUES(last_login_at)`,
		username, issuer, sub, email, now, now)
	return username, ""
}
