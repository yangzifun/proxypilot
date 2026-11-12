package api

import (
	"archive/zip"
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
// 安全修复自证测试（对应 fix-security-report.md 的 3 项）
//   1. 严重：MFA 绕过（mfa_token / email_login_token 不能当管理端 Bearer）
//   2. 严重：超管边界（备份导出/导入 superOnly + 导出剔除密钥 + 导入禁止覆盖安全表）
//   3. 高：反射 XSS（/client-auth?code= 转义）+ CSP 强制（nonce）
// =====================================================================

// ---------- 1. 严重：令牌用途隔离 ----------

// mfa_token / email_login_token 签名有效、Sub=admin，但用途不是 access，
// 必须被 authed（包括 superOnly 接口）拒绝。
func TestAuthedRejectsNonAccessTokens(t *testing.T) {
	const secret = "test-secret-token-type"
	h := &handlers{cfg: config.Config{JWTSecret: secret, SuperAdmin: "admin"}}

	exp := time.Now().Add(10 * time.Minute).UnixMilli()
	mfaToken, err := auth.IssueMFAToken("admin", secret)
	if err != nil {
		t.Fatalf("签发 mfa_token 失败：%v", err)
	}
	emailTicket, err := auth.Sign(auth.Claims{
		Sub: "admin", Exp: exp, Purpose: emailLoginTicketPurpose, Typ: auth.TokenTypeEmailLogin,
	}, secret)
	if err != nil {
		t.Fatalf("签发 email_login_token 失败：%v", err)
	}
	// 旧版本签发的令牌没有 typ 字段，也必须拒绝（默认拒绝）。
	legacy, err := auth.Sign(auth.Claims{Sub: "admin", Role: "admin", Exp: exp}, secret)
	if err != nil {
		t.Fatalf("签发旧版令牌失败：%v", err)
	}
	access, err := auth.IssueAdminTokenTTL("admin", secret, true, time.Hour)
	if err != nil {
		t.Fatalf("签发 access 令牌失败：%v", err)
	}

	call := func(opts authOpts, token string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		h.authed(opts, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})(rec, req)
		return rec.Code
	}

	bad := map[string]string{
		"mfa_token":       mfaToken,
		"email_login_tok": emailTicket,
		"legacy_no_typ":   legacy,
	}
	for name, tok := range bad {
		if got := call(authOpts{}, tok); got != http.StatusUnauthorized {
			t.Fatalf("%s 访问受保护接口 = %d，期望 401", name, got)
		}
		// 事务令牌的 Sub 即使等于超管，也不能通过 superOnly 接口。
		if got := call(authOpts{superOnly: true}, tok); got != http.StatusUnauthorized {
			t.Fatalf("%s 访问 superOnly 接口 = %d，期望 401", name, got)
		}
	}
	if got := call(authOpts{superOnly: true}, access); got != http.StatusOK {
		t.Fatalf("access 令牌访问 superOnly 接口 = %d，期望 200", got)
	}
}

// 不在 authed 后面的自有鉴权入口同样要拒绝事务令牌，避免「MFA 绕过」从
// /api/shell/local（本机 root 终端）或 SSE / 门户管理员判定这些旁路穿透。
func TestNonAccessTokenCannotUseSideChannelAuth(t *testing.T) {
	const secret = "test-secret-sidechannel"
	h := &handlers{cfg: config.Config{JWTSecret: secret, SuperAdmin: "admin"}}
	mfaToken, err := auth.IssueMFAToken("admin", secret)
	if err != nil {
		t.Fatalf("签发 mfa_token 失败：%v", err)
	}

	// 本机 root 终端
	req := httptest.NewRequest(http.MethodGet, "/api/shell/local?token="+url.QueryEscape(mfaToken), nil)
	rec := httptest.NewRecorder()
	h.shellLocal(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mfa_token 打开本机终端 = %d，期望 401", rec.Code)
	}

	// 管理端 SSE
	req = httptest.NewRequest(http.MethodGet, "/api/servers/stream?token="+url.QueryEscape(mfaToken), nil)
	rec = httptest.NewRecorder()
	h.sseServersStream(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mfa_token 订阅管理端 SSE = %d，期望 401", rec.Code)
	}

	// adminOrPortal：事务令牌既不是管理员也不是门户用户
	req = httptest.NewRequest(http.MethodGet, "/manage/configs", nil)
	req.Header.Set("Authorization", "Bearer "+mfaToken)
	if _, admin, ok := h.adminOrPortal(req); ok || admin {
		t.Fatalf("mfa_token 在 adminOrPortal 中被接受（ok=%v admin=%v）", ok, admin)
	}
}

// ---------- 2. 严重：超管边界 ----------

func TestBackupRoutesRequireSuperAdmin(t *testing.T) {
	const secret = "test-secret-backup-gate"
	const superAdmin = "admin"
	srv := New(config.Config{JWTSecret: secret, SuperAdmin: superAdmin}, nil, testLogger())

	token := func(sub string) string {
		tok, err := auth.Sign(auth.Claims{
			Sub: sub, Role: "admin", Typ: auth.TokenTypeAccess,
			Exp: time.Now().Add(time.Hour).UnixMilli(),
		}, secret)
		if err != nil {
			t.Fatalf("签发令牌失败：%v", err)
		}
		return tok
	}
	ordinary, super := token("shao"), token(superAdmin)

	call := func(method, path, tok string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec.Code
	}

	// 普通管理员：原先可导出整库（含 jwt_secret）→ 现在必须 403
	if got := call(http.MethodGet, "/api/backup/export", ordinary); got != http.StatusForbidden {
		t.Fatalf("普通管理员导出备份 = %d，期望 403", got)
	}
	if got := call(http.MethodPost, "/api/backup/import", ordinary); got != http.StatusForbidden {
		t.Fatalf("普通管理员导入备份 = %d，期望 403", got)
	}
	// 超管：通过鉴权后进入 handler；本测试没有 DB，应返回 503 而不是 401/403
	if got := call(http.MethodGet, "/api/backup/export", super); got != http.StatusServiceUnavailable {
		t.Fatalf("超管导出备份（无 DB）= %d，期望 503（说明已通过鉴权）", got)
	}
	if got := call(http.MethodPost, "/api/backup/import", super); got != http.StatusServiceUnavailable {
		t.Fatalf("超管导入备份（无 DB）= %d，期望 503（说明已通过鉴权）", got)
	}
}

// 导出时必须剔除 jwt_secret / agent_key_secret / 第三方凭证与 enc:v1 密文。
func TestBackupExportRedactsSecretFields(t *testing.T) {
	cases := []struct {
		name  string
		table string
		row   map[string]any
	}{
		{"jwt_secret", "app_settings", map[string]any{"key": "jwt_secret", "value": "plain-secret-value"}},
		{"agent_key_secret", "app_settings", map[string]any{"key": "agent_key_secret", "value": "plain-secret-value"}},
		{"cloudflare", "app_settings", map[string]any{"key": settingCFToken, "value": "enc:v1:ciphertext"}},
		{"smtp_pass", "app_settings", map[string]any{"key": settingSMTPPass, "value": "enc:v1:ciphertext"}},
		{"agent_token_enc", "servers", map[string]any{"id": int64(7), "agent_token_enc": "enc:v1:ciphertext"}},
	}
	for _, tc := range cases {
		n := backupRedactRow(tc.table, tc.row)
		if n == 0 {
			t.Fatalf("%s：没有剔除密钥字段，row=%#v", tc.name, tc.row)
		}
		for _, v := range tc.row {
			if s, ok := v.(string); ok && s != "" && (strings.HasPrefix(s, agentKeyVersion) || s == "plain-secret-value") {
				t.Fatalf("%s：仍残留敏感值 %q", tc.name, s)
			}
		}
	}

	// 普通设置与非密文列必须保留，否则恢复后配置全丢。
	row := map[string]any{"key": settingAgentBinaryBase, "value": "https://cdn.example.com/agent-dist"}
	if n := backupRedactRow("app_settings", row); n != 0 || row["value"] != "https://cdn.example.com/agent-dist" {
		t.Fatalf("普通设置被误删：n=%d row=%#v", n, row)
	}
	row = map[string]any{"id": int64(7), "name": "s1"}
	if n := backupRedactRow("servers", row); n != 0 || row["name"] != "s1" {
		t.Fatalf("非密文列被误删：n=%d row=%#v", n, row)
	}
}

func TestBackupImportSkipsSecurityCriticalTables(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "information_schema.tables"):
			return fakeRowsSet([]string{"TABLE_NAME"},
				[]driver.Value{"admin_users"},
				[]driver.Value{"app_settings"},
				[]driver.Value{"servers"}), nil
		case strings.Contains(q, "information_schema.columns"):
			table := ""
			if len(args) > 0 {
				table, _ = args[0].(string)
			}
			if table == "servers" {
				return fakeRowsSet([]string{"COLUMN_NAME"},
					[]driver.Value{"id"}, []driver.Value{"name"}), nil
			}
			return &fakeRows{}, nil
		}
		return &fakeRows{}, nil
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"manifest.json":        `{"app":"proxypilot","version":"test"}`,
		"db/admin_users.json":  `[{"id":1,"username":"admin","password_hash":"attacker-chosen"}]`,
		"db/app_settings.json": `[{"key":"jwt_secret","value":"attacker-secret"}]`,
		"db/servers.json":      `[{"id":42,"name":"s1"}]`,
	}
	for name, content := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("创建 zip 条目失败：%v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("写入 zip 条目失败：%v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 zip 失败：%v", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "backup.zip")
	if err != nil {
		t.Fatalf("创建 multipart 失败：%v", err)
	}
	if _, err := fw.Write(buf.Bytes()); err != nil {
		t.Fatalf("写入 multipart 失败：%v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("关闭 multipart 失败：%v", err)
	}

	h := &handlers{
		cfg: config.Config{BucketDir: t.TempDir(), AgentDistDir: t.TempDir()},
		db:  db, log: testLogger(),
	}
	req := httptest.NewRequest(http.MethodPost, "/api/backup/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.backupImport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("导入返回 %d：%s", rec.Code, rec.Body.String())
	}

	// 安全关键表绝不能整表替换（DELETE）或写入
	for _, table := range []string{"admin_users", "app_settings"} {
		if n := fdb.countExec("DELETE FROM `" + table + "`"); n != 0 {
			t.Fatalf("导入覆盖了安全关键表 %s（DELETE 执行 %d 次）", table, n)
		}
		if n := fdb.countExec("INSERT INTO `" + table + "`"); n != 0 {
			t.Fatalf("导入写入了安全关键表 %s（INSERT 执行 %d 次）", table, n)
		}
	}
	// 普通业务表仍应正常恢复
	if n := fdb.countExec("DELETE FROM `servers`"); n != 1 {
		t.Fatalf("普通表 servers 未被恢复（DELETE 执行 %d 次）", n)
	}

	var out struct {
		Success       bool     `json:"success"`
		SkippedTables []string `json:"skipped_tables"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if !out.Success {
		t.Fatal("导入未成功")
	}
	joined := strings.Join(out.SkippedTables, ",")
	if !strings.Contains(joined, "admin_users") || !strings.Contains(joined, "app_settings") {
		t.Fatalf("skipped_tables 未报告安全关键表：%v", out.SkippedTables)
	}
}

// 端到端：导出的备份 ZIP 里不能出现 jwt_secret 的明文。
func TestBackupExportZipRedactsSecrets(t *testing.T) {
	fdb, db := newFakeDB(t)
	const jwtSecret = "SUPER-SECRET-JWT-VALUE"
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "information_schema.tables"):
			return fakeRowsSet([]string{"TABLE_NAME"}, []driver.Value{"app_settings"}), nil
		case strings.Contains(q, "SHOW CREATE TABLE"):
			return fakeRowsSet([]string{"Table", "Create Table"},
				[]driver.Value{"app_settings", "CREATE TABLE `app_settings` (`key` varchar(64), `value` text)"}), nil
		case strings.Contains(q, "SELECT * FROM `app_settings`"):
			return fakeRowsSet([]string{"key", "value"},
				[]driver.Value{"jwt_secret", jwtSecret},
				[]driver.Value{settingAgentBinaryBase, "https://cdn.example.com/agent-dist"}), nil
		}
		return &fakeRows{}, nil
	}

	h := &handlers{cfg: config.Config{}, db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodGet, "/api/backup/export?bucket=0&agent=0&logs=0", nil)
	rec := httptest.NewRecorder()
	h.backupExport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("导出返回 %d：%s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("导出结果不是有效 ZIP：%v", err)
	}
	var appSettings string
	for _, f := range zr.File {
		if f.Name != "db/app_settings.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("打开备份条目失败：%v", err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("读取备份条目失败：%v", err)
		}
		appSettings = string(b)
	}
	if appSettings == "" {
		t.Fatal("备份里没有 db/app_settings.json")
	}
	if strings.Contains(appSettings, jwtSecret) {
		t.Fatalf("备份仍包含 jwt_secret 明文：%s", appSettings)
	}
	if !strings.Contains(appSettings, "agent_binary_base_url") {
		t.Fatalf("普通设置不应被整行删除：%s", appSettings)
	}
}

// ---------- 3. 高：反射 XSS + 强制 CSP（无 nonce、无可执行内联脚本）----------

// assertStrictCSPHeader 校验强制 CSP：有头、无 Report-Only、script-src 不含
// unsafe-inline，也不含非确定性 nonce（2026-11-12 白屏事故后禁止 reintroduce）。
func assertStrictCSPHeader(t *testing.T, hdr http.Header) string {
	t.Helper()
	csp := hdr.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("缺少强制 CSP 响应头")
	}
	if hdr.Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatal("不应再出现 Content-Security-Policy-Report-Only")
	}
	scriptSrc := ""
	if i := strings.Index(csp, "script-src"); i >= 0 {
		scriptSrc = csp[i:]
		if j := strings.Index(scriptSrc, ";"); j >= 0 {
			scriptSrc = scriptSrc[:j]
		}
	}
	if !strings.Contains(scriptSrc, "'self'") {
		t.Fatalf("script-src 未包含 'self'：%s", scriptSrc)
	}
	if strings.Contains(scriptSrc, "'unsafe-inline'") || strings.Contains(scriptSrc, "'unsafe-eval'") {
		t.Fatalf("script-src 不应包含 unsafe-inline/unsafe-eval：%s", scriptSrc)
	}
	if strings.Contains(csp, "nonce-") {
		t.Fatalf("CSP 不应再使用每请求 nonce（缓存/代理下会与 HTML 不一致）：%s", csp)
	}
	return csp
}

// assertNoExecutableInlineScripts 校验 HTML 里每个 <script> 要么是外部脚本（同源或
// CSP 白名单站点），要么是非执行数据块（如 application/json）；同时不允许内联事件处理器。
func assertNoExecutableInlineScripts(t *testing.T, page string) []htmlScript {
	t.Helper()
	scripts := parseHTMLScripts(page)
	if len(scripts) == 0 {
		t.Fatalf("页面没有解析到任何 <script>：%s", page[:min(300, len(page))])
	}
	for _, s := range scripts {
		if strings.Contains(strings.ToLower(s.attrs), "nonce=") {
			t.Fatalf("页面不应再依赖 nonce 属性：%q", s.attrs)
		}
		src := strings.TrimSpace(scriptAttrValue(s.attrs, "src"))
		if src == "" {
			if scriptTypeIsExecutable(s.attrs) {
				body := s.body
				if len(body) > 120 {
					body = body[:120]
				}
				t.Fatalf("存在可执行内联脚本（强制 CSP 下会被拦截）：type=%q body=%q", scriptAttrValue(s.attrs, "type"), body)
			}
			continue
		}
		if !(strings.HasPrefix(src, "/") ||
			strings.HasPrefix(src, "https://challenges.cloudflare.com/") ||
			strings.HasPrefix(src, "https://static.cloudflareinsights.com/")) {
			t.Fatalf("外部脚本地址不在 CSP 白名单内：%s", src)
		}
	}
	if inlineHandler.MatchString(page) {
		t.Fatal("页面存在内联事件处理器（on*=），强制 CSP 下会被拦截")
	}
	return scripts
}

func scriptSrcOf(t *testing.T, scripts []htmlScript, want string) bool {
	t.Helper()
	for _, s := range scripts {
		if strings.Contains(scriptAttrValue(s.attrs, "src"), want) {
			return true
		}
	}
	return false
}

// /client-auth?code= 必须转义回显；页面不得有可执行内联脚本；CSP 必须强制。
func TestClientAuthPageEscapesCodeAndUsesStrictCSP(t *testing.T) {
	h := &handlers{cfg: config.Config{}, log: testLogger()}
	hnd := h.secureHeaders(http.HandlerFunc(h.clientAuthPage))

	payload := `</span><script>alert(1)</script>`
	req := httptest.NewRequest(http.MethodGet, "/client-auth?code="+url.QueryEscape(payload), nil)
	req.Host = "pp.example.com"
	rec := httptest.NewRecorder()
	hnd.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/client-auth = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, payload) {
		t.Fatal("code 未转义，页面存在反射 XSS")
	}
	if !strings.Contains(body, "&lt;/span&gt;") {
		t.Fatalf("未看到转义后的 code：%s", body[:min(300, len(body))])
	}
	assertStrictCSPHeader(t, rec.Header())
	scripts := assertNoExecutableInlineScripts(t, body)
	if !scriptSrcOf(t, scripts, pageScriptClientAuth) {
		t.Fatalf("页面应引用外部脚本 %s", pageScriptClientAuth)
	}
}

// 管理端外壳：动态数据走 #pp-boot JSON 数据块，可执行脚本全部外部化，无 nonce。
func TestAdminShellStrictCSPNoInlineScripts(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, log: testLogger()}
	hnd := h.secureHeaders(http.HandlerFunc(h.pageIndex))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Host = "pp.example.com"
	rec := httptest.NewRecorder()
	hnd.ServeHTTP(rec, req)
	body := rec.Body.String()

	csp := assertStrictCSPHeader(t, rec.Header())
	if !strings.Contains(csp, "ws:") || !strings.Contains(csp, "wss:") {
		t.Fatalf("connect-src 未放行 WebSocket（终端会不可用）：%s", csp)
	}
	scripts := assertNoExecutableInlineScripts(t, body)
	if !scriptSrcOf(t, scripts, pageScriptBoot) {
		t.Fatalf("外壳应引用外部引导脚本 %s", pageScriptBoot)
	}
	if !scriptSrcOf(t, scripts, "/admin-app.js") {
		t.Fatal("外壳应引用 /admin-app.js")
	}

	// boot 数据块必须是 application/json（数据块不执行、不受 script-src 约束）
	const bootMarker = `<script type="application/json" id="pp-boot">`
	start := strings.Index(body, bootMarker)
	if start < 0 {
		t.Fatal("缺少 #pp-boot JSON 数据块")
	}
	rest := body[start+len(bootMarker):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatal("#pp-boot 数据块未闭合")
	}
	var boot map[string]any
	if err := json.Unmarshal([]byte(rest[:end]), &boot); err != nil {
		t.Fatalf("#pp-boot 不是合法 JSON：%v", err)
	}
	if boot["mode"] != "admin" {
		t.Fatalf("boot.mode = %v，期望 admin", boot["mode"])
	}
	// 数据块必须在引导脚本之前，引导脚本在 app bundle 之前执行
	iData := strings.Index(body, bootMarker)
	iBoot := strings.Index(body, pageScriptBoot)
	iApp := strings.Index(body, `src="/admin-app.js`)
	if !(iData >= 0 && iBoot >= 0 && iApp >= 0 && iData < iBoot && iBoot < iApp) {
		t.Fatalf("脚本顺序错误：data=%d boot=%d app=%d", iData, iBoot, iApp)
	}
	if !strings.Contains(body, `id="pp-reload-link"`) {
		t.Fatal("加载失败提示里的刷新链接缺少 id（事件委托无法绑定）")
	}
}

// CSP 必须是常量：不同 Host/不同请求拿到的 CSP 完全相同，缓存/代理拆分响应也不会
// 造成 body 与头不一致（V1.2 白屏事故的根因是每请求 nonce）。
func TestCSPHeaderIsConstantAcrossRequests(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, log: testLogger()}
	hnd := h.secureHeaders(http.HandlerFunc(h.pageIndex))
	get := func(host string) string {
		req := httptest.NewRequest(http.MethodGet, "/admin", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		hnd.ServeHTTP(rec, req)
		return rec.Header().Get("Content-Security-Policy")
	}
	a, b := get("pp.example.com"), get("1.2.3.4:8443")
	if a == "" || a != b {
		t.Fatalf("CSP 必须对所有请求完全一致（缓存安全），got %q vs %q", a, b)
	}
	if a != cspPolicy {
		t.Fatalf("CSP 头与常量策略不一致：%q", a)
	}
}

// 设置页 / 证书页 / 客户端授权页同样不能有可执行内联脚本。
func TestServerRenderedPagesNoInlineScripts(t *testing.T) {
	h := &handlers{cfg: config.Config{}, log: testLogger()}
	pages := map[string]struct {
		path string
		fn   http.HandlerFunc
	}{
		"settings":    {"/admin/settings", h.settingsPage},
		"certs":       {"/admin/certs", h.vpnCertPage},
		"client-auth": {"/client-auth", h.clientAuthPage},
	}
	for name, p := range pages {
		req := httptest.NewRequest(http.MethodGet, p.path, nil)
		req.Host = "pp.example.com"
		rec := httptest.NewRecorder()
		h.secureHeaders(p.fn).ServeHTTP(rec, req)
		assertStrictCSPHeader(t, rec.Header())
		assertNoExecutableInlineScripts(t, rec.Body.String())
		_ = name
	}
}

// 每个页面脚本都有独立的同源外部文件（强制 CSP 靠 'self' 放行）。
func TestPageScriptsServed(t *testing.T) {
	h := &handlers{log: testLogger()}
	routes := map[string]http.HandlerFunc{
		pageScriptBoot:          h.bootJS,
		pageScriptClientAuth:    h.clientAuthJS,
		pageScriptAdminSettings: h.adminSettingsJS,
		pageScriptAdminCerts:    h.adminCertsJS,
	}
	for path, fn := range routes {
		rec := httptest.NewRecorder()
		fn(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Fatalf("%s Content-Type = %q", path, ct)
		}
		if rec.Body.Len() < 100 {
			t.Fatalf("%s 体积异常：%d", path, rec.Body.Len())
		}
		if !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("%s 应禁用长缓存，避免 HTML 与脚本版本错配：%q", path, rec.Header().Get("Cache-Control"))
		}
	}
	// 引导脚本必须能解析 #pp-boot 数据块（前端入口依赖）
	if !strings.Contains(string(embeddedBootJS), "pp-boot") || !strings.Contains(string(embeddedBootJS), "__PP_BOOT__") {
		t.Fatal("/boot.js 必须解析 #pp-boot 并写入 window.__PP_BOOT__")
	}
}

// 全局兜底：所有服务端渲染页面都不应再出现 nonce=（nonce 方案已废弃）。
func TestNoNonceAnywhere(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, log: testLogger()}
	type page struct {
		path string
		fn   http.HandlerFunc
	}
	pages := []page{
		{"/admin", h.pageIndex},
		{"/admin/settings", h.settingsPage},
		{"/admin/certs", h.vpnCertPage},
		{"/client-auth", h.clientAuthPage},
	}
	for _, p := range pages {
		req := httptest.NewRequest(http.MethodGet, p.path, nil)
		req.Host = "pp.example.com"
		rec := httptest.NewRecorder()
		h.secureHeaders(p.fn).ServeHTTP(rec, req)
		body := strings.ToLower(rec.Body.String())
		if strings.Contains(body, "nonce=") {
			t.Fatalf("%s 的 HTML 仍含 nonce 属性", p.path)
		}
		if strings.Contains(strings.ToLower(rec.Header().Get("Content-Security-Policy")), "nonce-") {
			t.Fatalf("%s 的 CSP 仍含 nonce", p.path)
		}
	}
}

// hasExecutableInlineScript 的判定：数据块不算可执行内联脚本，classic/module 脚本算。
func TestHasExecutableInlineScript(t *testing.T) {
	cases := []struct {
		name string
		page string
		want bool
	}{
		{"数据块", `<script type="application/json" id="pp-boot">{"a":1}</script>`, false},
		{"外部脚本", `<script src="/app.js"></script>`, false},
		{"classic 内联", `<script>alert(1)</script>`, true},
		{"缺省 type 内联", `<script type="">x()</script>`, true},
		{"module 内联", `<script type="module">x()</script>`, true},
		{"importmap", `<script type="importmap">{"imports":{}}</script>`, false},
		{"大小写/属性", `<SCRIPT TYPE="text/javascript">x()</SCRIPT>`, true},
		{"含 src 空值", `<script src="">x()</script>`, true},
	}
	for _, tc := range cases {
		if got := hasExecutableInlineScript(tc.page); got != tc.want {
			t.Fatalf("%s：hasExecutableInlineScript=%v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 自定义 WebDist 外壳若含可执行内联脚本，强制 CSP 会拦掉 → 回退内置外壳，避免白屏。
func TestCustomIndexWithInlineScriptFallsBack(t *testing.T) {
	dir := t.TempDir()
	index := `<html><head><script>alert(1)</script></head><body><div id="root"></div></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatalf("写入 index.html 失败：%v", err)
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin", WebDist: dir}, log: testLogger()}
	rec := httptest.NewRecorder()
	h.pageIndex(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `id="pp-boot"`) || !strings.Contains(body, pageScriptBoot) {
		t.Fatal("含内联脚本的自定义外壳应回退到内置外壳")
	}
	if strings.Contains(body, "alert(1)") {
		t.Fatal("不应把含可执行内联脚本的自定义 HTML 原样输出")
	}
}

// 自定义 WebDist 外壳只有外部脚本时保留自定义内容，并注入 #pp-boot 数据块 + 外部引导脚本。
func TestCustomIndexWithoutInlineScriptInjectsBootBlock(t *testing.T) {
	dir := t.TempDir()
	index := `<html><head><script src="/assets/app.js"></script></head><body><div id="root"></div></body></html>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0o644); err != nil {
		t.Fatalf("写入 index.html 失败：%v", err)
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin", WebDist: dir}, log: testLogger()}
	rec := httptest.NewRecorder()
	h.pageIndex(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `id="pp-boot"`) || !strings.Contains(body, pageScriptBoot) {
		t.Fatal("自定义外壳应注入 #pp-boot 数据块与外部引导脚本")
	}
	if !strings.Contains(body, `<script src="/assets/app.js"></script>`) {
		t.Fatal("自定义外壳自身的外部脚本应保留")
	}
	if hasExecutableInlineScript(body) {
		t.Fatal("注入后的自定义外壳不应含可执行内联脚本")
	}
}