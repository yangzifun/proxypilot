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

func oidcTxCookie(r *http.Request, tx oidcTx, maxAge int) *http.Cookie {
	raw, _ := json.Marshal(tx)
	return &http.Cookie{
		Name: "pp_oidc_tx", Value: base64.RawURLEncoding.EncodeToString(raw), Path: "/api/oidc",
		HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	}
}

func readOidcTx(r *http.Request) (oidcTx, bool) {
	c, err := r.Cookie("pp_oidc_tx")
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
		http.Redirect(w, r, "/login?oidc_error="+url.QueryEscape("turnstile_failed"), http.StatusFound)
		return
	}
	d, err := h.oidcDiscover(r)
	if err != nil || d.AuthorizationEndpoint == "" {
		writeError(w, http.StatusBadGateway, "OIDC 发现文档读取失败")
		return
	}
	verifier := randomVerifier()
	tx := oidcTx{State: randomState(), Verifier: verifier, Nonce: randomState(), Mode: oidcModeAdmin}
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
	if errMsg := q.Get("error"); errMsg != "" {
		http.Redirect(w, r, "/login?oidc_error="+url.QueryEscape(errMsg), http.StatusFound)
		return
	}
	tx, ok := readOidcTx(r)
	if !ok || tx.State == "" || tx.State != q.Get("state") {
		http.Redirect(w, r, "/login?oidc_error=state_mismatch", http.StatusFound)
		return
	}
	code := strings.TrimSpace(q.Get("code"))
	if code == "" {
		http.Redirect(w, r, "/login?oidc_error=missing_code", http.StatusFound)
		return
	}
	d, err := h.oidcDiscover(r)
	if err != nil || d.TokenEndpoint == "" || d.UserinfoEndpoint == "" {
		http.Redirect(w, r, "/login?oidc_error=discovery_failed", http.StatusFound)
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
		http.Redirect(w, r, "/login?oidc_error=token_failed", http.StatusFound)
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
		http.Redirect(w, r, "/login?oidc_error=token_rejected", http.StatusFound)
		return
	}
	if tokenData.IDToken == "" {
		http.Redirect(w, r, "/login?oidc_error=id_token_missing", http.StatusFound)
		return
	}
	jwks, err := h.oidcJWKS(r, d.JWKSUri)
	if err != nil {
		http.Redirect(w, r, "/login?oidc_error=jwks_failed", http.StatusFound)
		return
	}
	claims, err := verifyRS256JWT(tokenData.IDToken, jwks, d.Issuer, oc.ClientID, tx.Nonce)
	if err != nil {
		http.Redirect(w, r, "/login?oidc_error=id_token_invalid", http.StatusFound)
		return
	}

	uiReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, d.UserinfoEndpoint, nil)
	uiReq.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	uiReq.Header.Set("Accept", "application/json")
	uiResp, err := client.Do(uiReq)
	if err != nil {
		http.Redirect(w, r, "/login?oidc_error=userinfo_failed", http.StatusFound)
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
		http.Redirect(w, r, "/login?oidc_error=email_missing", http.StatusFound)
		return
	}
	if v, ok := info.EmailVerified.(bool); ok && !v {
		http.Redirect(w, r, "/login?oidc_error=email_unverified", http.StatusFound)
		return
	}
	if v, ok := claims["email_verified"].(bool); ok && !v {
		http.Redirect(w, r, "/login?oidc_error=email_unverified", http.StatusFound)
		return
	}
	// 用户中心登录：换成 portal 令牌后跳回首页
	if tx.Mode == oidcModePortal {
		ptoken, uuidVal, reason := h.finishPortalOIDC(r, email)
		if ptoken == "" {
			http.SetCookie(w, oidcTxCookie(r, oidcTx{}, -1))
			http.Redirect(w, r, "/?oidc_error="+url.QueryEscape(reason), http.StatusFound)
			return
		}
		http.SetCookie(w, oidcTxCookie(r, oidcTx{}, -1))
		h.log.Info("用户中心 SSO 登录成功", "email", emailNormForLog(email), "uuid", uuidVal)
		http.Redirect(w, r, "/?token="+url.QueryEscape(ptoken), http.StatusFound)
		return
	}

	issuer := strings.TrimRight(firstNonEmpty(d.Issuer, oc.Issuer), "/")
	username, reason := h.resolveOIDCAdmin(r.Context(), issuer, jsonStr(claims["sub"]), email)
	if username == "" {
		http.Redirect(w, r, "/login?oidc_error="+url.QueryEscape(reason), http.StatusFound)
		return
	}
	token, err := auth.IssueAdminTokenTTL(username, h.cfg.JWTSecret, true, h.cfg.AdminTokenTTL)
	if err != nil {
		http.Redirect(w, r, "/login?oidc_error=token_sign_failed", http.StatusFound)
		return
	}
	http.SetCookie(w, oidcTxCookie(r, oidcTx{}, -1))
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
