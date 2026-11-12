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
	"regexp"
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

// ---------- 3. 高：反射 XSS + CSP ----------

var cspNonceRe = regexp.MustCompile(`'nonce-([^']+)'`)

// /client-auth?code= 必须转义回显；页面内联脚本必须带 nonce；CSP 必须是强制模式。
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

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("缺少强制 CSP 响应头")
	}
	if rec.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatal("仍在使用 Report-Only（只报告不拦截）")
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-") {
		t.Fatalf("script-src 未启用 nonce：%s", csp)
	}
	nonce := extractCSPNonce(t, csp)
	if got, want := strings.Count(body, "<script"), strings.Count(body, `nonce="`+nonce+`"`); got != want {
		t.Fatalf("页面有 %d 个 <script>，但只有 %d 个带 nonce", got, want)
	}
	if strings.Contains(body, "onclick=") {
		t.Fatal("页面仍含内联事件处理器（强制 CSP 下会被拦截）")
	}
}

// 管理端外壳（引导脚本 + 加载脚本）同样要带 nonce，且不能有内联 onclick。
func TestAdminShellUsesStrictCSPNonce(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, log: testLogger()}
	hnd := h.secureHeaders(http.HandlerFunc(h.pageIndex))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.Host = "pp.example.com"
	rec := httptest.NewRecorder()
	hnd.ServeHTTP(rec, req)
	body := rec.Body.String()

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" || rec.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatalf("CSP 不是强制模式：%q", csp)
	}
	if !strings.Contains(csp, "ws://pp.example.com") || !strings.Contains(csp, "wss://pp.example.com") {
		t.Fatalf("connect-src 未放行同主机 WebSocket（终端会不可用）：%s", csp)
	}
	nonce := extractCSPNonce(t, csp)
	if got, want := strings.Count(body, "<script"), strings.Count(body, `nonce="`+nonce+`"`); got != want {
		t.Fatalf("外壳有 %d 个 <script>，但只有 %d 个带 nonce", got, want)
	}
	if strings.Contains(body, "onclick=") {
		t.Fatal("外壳仍含 onclick 内联事件处理器")
	}
	if !strings.Contains(body, `id="pp-reload-link"`) {
		t.Fatal("加载失败提示里的刷新链接缺少 id（事件委托无法绑定）")
	}
}

// 设置页 / 证书页也是服务端拼出的 HTML，内联脚本必须带同一个 nonce。
func TestServerRenderedPagesUseStrictCSPNonce(t *testing.T) {
	h := &handlers{cfg: config.Config{}, log: testLogger()}
	pages := map[string]http.HandlerFunc{
		"/admin/settings": h.settingsPage,
		"/admin/certs":    h.vpnCertPage,
	}
	for path, fn := range pages {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "pp.example.com"
		rec := httptest.NewRecorder()
		h.secureHeaders(fn).ServeHTTP(rec, req)
		body := rec.Body.String()
		csp := rec.Header().Get("Content-Security-Policy")
		nonce := extractCSPNonce(t, csp)
		if got, want := strings.Count(body, "<script"), strings.Count(body, `nonce="`+nonce+`"`); got != want {
			t.Fatalf("%s：有 %d 个 <script>，但只有 %d 个带 nonce", path, got, want)
		}
	}
}

func extractCSPNonce(t *testing.T, csp string) string {
	t.Helper()
	m := cspNonceRe.FindStringSubmatch(csp)
	if len(m) != 2 || m[1] == "" {
		t.Fatalf("无法从 CSP 中解析 nonce：%s", csp)
	}
	return m[1]
}

// nonce 注入只处理标签起始处、大小写不敏感、已带 nonce 的不重复添加，
// 且不能因为页面含中文/大写标签而错位。
func TestInjectCSPNonce(t *testing.T) {
	page := `<p>中文页面</p><script>a()</script><SCRIPT src="x"></SCRIPT>` +
		`<script nonce="keep">b()</script><p>结尾中文</p>`
	out := injectCSPNonce(page, "abc123")
	if n := strings.Count(out, `nonce="abc123"`); n != 2 {
		t.Fatalf("应为 2 个标签补 nonce，实际 %d：%s", n, out)
	}
	if !strings.Contains(out, `<script nonce="keep">b()</script>`) {
		t.Fatalf("已有 nonce 的标签不应被改动：%s", out)
	}
	if !strings.Contains(out, `<SCRIPT src="x" nonce="abc123">`) {
		t.Fatalf("大写标签未补 nonce：%s", out)
	}
	if !strings.HasSuffix(out, `<p>结尾中文</p>`) {
		t.Fatalf("页面尾部内容被破坏：%s", out)
	}
	// nonce 为空时（单测直接调用页面 handler）保持原样，避免产生非法属性。
	if got := injectCSPNonce(page, ""); got != page {
		t.Fatalf("nonce 为空时不应改动页面")
	}
}