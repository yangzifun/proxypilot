package api

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
// task-12 自证测试：7 中危 + 7 低危
// =====================================================================

// ---------- 中-01 VPN domain 注入 ----------

func TestNormalizeNodeDomainRejectsShellInjection(t *testing.T) {
	bad := []string{
		"$(id).a.b", "`id`.a.b", "a;id.b", "a|id.b", "a&id.b", "a$(id)b.com",
		"a b.com", "a\tb.com", "a\"b.com", "a'b.com", "a\\b.com", "a/b.com",
		"-a.com", "a-.com", "a..com", "x", "",
	}
	for _, in := range bad {
		if got, err := normalizeNodeDomain(in); err == nil {
			t.Fatalf("应拒绝 %q，实际得到 %q", in, got)
		}
	}
	good := map[string]string{
		"  VPN01.Example.COM ":          "vpn01.example.com",
		"https://vpn.example.com/path":  "vpn.example.com",
		"vpn.example.com":               "vpn.example.com",
		"vpn.example.com.":              "vpn.example.com",
		"1.2.3.4":                       "1.2.3.4",
		"2001:db8::1":                   "2001:db8::1",
	}
	for in, want := range good {
		got, err := normalizeNodeDomain(in)
		if err != nil || got != want {
			t.Fatalf("normalizeNodeDomain(%q) = %q,%v，期望 %q", in, got, err, want)
		}
	}
}

func TestShellSafeVPNHost(t *testing.T) {
	ok := []string{"vpn.example.com", "1.2.3.4", "2001:db8::1", "[2001:db8::1]", "a-b_c.example.com"}
	for _, s := range ok {
		if !shellSafeVPNHost(s) {
			t.Fatalf("%q 应通过 shell 安全检查", s)
		}
	}
	bad := []string{"", "$(id).a.b", "`id`.a.b", "a;id", "a|id", "a&id", "a b", "a\"b", "a'b", "a>b", "a\nb"}
	for _, s := range bad {
		if shellSafeVPNHost(s) {
			t.Fatalf("%q 不应通过 shell 安全检查", s)
		}
	}
}

func TestBuildVPNDeployScriptNeverEmbedsShellMetachars(t *testing.T) {
	script := buildVPNDeployScript("$(id).a.b", "", `{"a":1}`, 443, "", "")
	if strings.Contains(script, "$(id)") || strings.Contains(script, "id).a.b") {
		t.Fatalf("部署脚本仍包含注入 payload：%s", script)
	}
	if !strings.Contains(script, "invalid.invalid") {
		t.Fatalf("不安全 host 应替换为占位域名（不执行注入内容）")
	}
}

// ---------- 低-02 盲 SSRF ----------

func TestCDNTargetAllowedRejectsInternalTargets(t *testing.T) {
	ctx := t.Context()
	for _, bad := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "169.254.1.1", "::1", ""} {
		if cdnTargetAllowed(ctx, bad) {
			t.Fatalf("CDN 检测不应允许内网/保留地址：%q", bad)
		}
	}
	for _, ok := range []string{"1.1.1.1", "8.8.8.8"} {
		if !cdnTargetAllowed(ctx, ok) {
			t.Fatalf("CDN 检测应允许公网地址：%q", ok)
		}
	}
}

func TestCDNPreferredCreateRejectsPrivateAddress(t *testing.T) {
	_, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPost, "/api/cdn-preferred",
		strings.NewReader(`{"address":"127.0.0.1","port":3306}`))
	rec := httptest.NewRecorder()
	h.cdnPreferredCreate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("私网地址应 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestValidateDistributionURL(t *testing.T) {
	bad := []string{
		"http://s3.example.com/agent", "https://user:pass@s3.example.com/agent",
		"https://127.0.0.1/agent", "https://10.1.2.3/agent", "https://localhost/agent",
		"ftp://s3.example.com/agent", "not-a-url",
	}
	for _, in := range bad {
		if err := validateDistributionURL(in); err == nil {
			t.Fatalf("分发基址应拒绝 %q", in)
		}
	}
	if err := validateDistributionURL("https://s3.example.com/agent-dist"); err != nil {
		t.Fatalf("合法分发基址被拒：%v", err)
	}
	if err := validateDistributionURL(""); err != nil {
		t.Fatalf("空值应允许（表示不配置）：%v", err)
	}
}

// ---------- 中-02 分发完整性 ----------

func TestComponentBinaryURLRequiresSHA(t *testing.T) {
	if got := componentBinaryURL("https://s3.example.com/singbox", "", "amd64"); got != "" {
		t.Fatalf("缺 sha256 时不应下发下载地址，got %q", got)
	}
	if got := componentBinaryURL("https://s3.example.com/singbox/", "abc", "amd64"); got != "https://s3.example.com/singbox/sing-box-linux-amd64" {
		t.Fatalf("组件地址 = %q", got)
	}
}

func TestInstallScriptHardening(t *testing.T) {
	body := installScriptBody
	// 低-07：--base 不再被写死地址覆盖
	if !strings.Contains(body, `[ -n "$BINARY_BASE" ] || BINARY_BASE=`) {
		t.Fatal("install.sh 仍会无条件覆盖 --base")
	}
	// 中-02：不再回退到无 sha256 校验的 GitHub 直连下载
	if strings.Contains(body, "github.com/SagerNet") {
		t.Fatal("install.sh 仍包含无校验的 GitHub 下载回退")
	}
	if !strings.Contains(body, "缺少 sha256") && !strings.Contains(body, "未配置 component_sha") {
		t.Fatal("install.sh 缺少组件 sha256 强制校验提示")
	}
	if !strings.Contains(body, "/dev/tty") {
		t.Fatal("install.sh 缺少交互式令牌输入（/dev/tty）")
	}
	// 低-07：生成的安装命令不再包含令牌
	cmd := buildInstallCommand("https://s3.example.com/agent", "https://pp.example.com", "ag_secret")
	if strings.Contains(cmd, "ag_secret") || strings.Contains(cmd, "--key") {
		t.Fatalf("安装命令仍包含令牌/--key：%s", cmd)
	}
}

// ---------- 中-04 Agent 令牌只认请求头 ----------

func TestAgentTokenFromRequestHeaderOnly(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/agent/poll?token=ag_query", strings.NewReader(`{"token":"ag_body"}`))
	if got := agentTokenFromRequest(r); got != "" {
		t.Fatalf("query/body 中的令牌不应被接受，got %q", got)
	}
	r.Header.Set("X-Agent-Token", "ag_header")
	if got := agentTokenFromRequest(r); got != "ag_header" {
		t.Fatalf("X-Agent-Token 未被识别，got %q", got)
	}
	r2 := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	r2.Header.Set("Authorization", "Bearer ag_bearer")
	if got := agentTokenFromRequest(r2); got != "ag_bearer" {
		t.Fatalf("Bearer 未被识别，got %q", got)
	}
}

// ---------- 低-05 组网令牌命名空间 ----------

func TestMeshHeartbeatTokenAllowed(t *testing.T) {
	for _, ok := range []string{"mesh", "MESH", " mesh "} {
		if !meshHeartbeatTokenAllowed(ok) {
			t.Fatalf("%q 应允许用于组网心跳", ok)
		}
	}
	for _, bad := range []string{"", "default", "vpn"} {
		if meshHeartbeatTokenAllowed(bad) {
			t.Fatalf("%q 不应允许用于组网心跳", bad)
		}
	}
}

// ---------- 中-05 组网 TLS ----------

func TestMeshTLSArg(t *testing.T) {
	if got := meshTLSArg(true, "-----BEGIN CERTIFICATE-----"); got != "-ca /etc/proxypilot/mesh/ca.crt" {
		t.Fatalf("有 CA 时应校验证书，got %q", got)
	}
	if got := meshTLSArg(false, "CA"); got != "-insecure" {
		t.Fatalf("显式关闭校验时应使用 -insecure，got %q", got)
	}
	if got := meshTLSArg(true, ""); got != "-insecure" {
		t.Fatalf("没有 CA 时只能回退 -insecure，got %q", got)
	}
}

// ---------- 低-03 配置文件权限 ----------

func TestLocalConfigFileModeIs0600(t *testing.T) {
	if localConfigFileMode() != os.FileMode(0o600) {
		t.Fatalf("本机下发配置权限 = %o，期望 0600", localConfigFileMode())
	}
}

// ---------- 中-07 令牌生命周期 ----------

func TestClientAuthPollExpiresApprovedToken(t *testing.T) {
	fdb, db := newFakeDB(t)
	expires := time.Now().Add(5 * time.Minute).UnixMilli()
	approvedAt := time.Now().Add(-2 * time.Minute).UnixMilli() // 超过 60s 领取窗口
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM client_auth_codes") {
			return fakeRowsSet([]string{"status", "token", "email", "expires_at", "approved_at"},
				[]driver.Value{"approved", "PORTAL-TOKEN", "u@example.com", expires, approvedAt}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.clientAuthPoll(rec, httptest.NewRequest(http.MethodGet, "/api/client-auth/poll?code=abc", nil))
	body := rec.Body.String()
	if strings.Contains(body, "PORTAL-TOKEN") || !strings.Contains(body, "expired") {
		t.Fatalf("超过领取窗口不应再返回令牌：%s", body)
	}
	if n := fdb.countExec("UPDATE client_auth_codes"); n == 0 {
		t.Fatal("过期后应把授权码置为 expired 并清空令牌")
	}
}

func TestClientAuthPollReturnsTokenWithinWindow(t *testing.T) {
	fdb, db := newFakeDB(t)
	expires := time.Now().Add(5 * time.Minute).UnixMilli()
	approvedAt := time.Now().Add(-5 * time.Second).UnixMilli()
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM client_auth_codes") {
			return fakeRowsSet([]string{"status", "token", "email", "expires_at", "approved_at"},
				[]driver.Value{"approved", "PORTAL-TOKEN", "u@example.com", expires, approvedAt}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.clientAuthPoll(rec, httptest.NewRequest(http.MethodGet, "/api/client-auth/poll?code=abc", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "PORTAL-TOKEN") || !strings.Contains(body, `"approved"`) {
		t.Fatalf("领取窗口内应返回令牌：%s", body)
	}
	if n := fdb.countExec("UPDATE client_auth_codes"); n != 0 {
		t.Fatal("窗口内不应提前作废授权码")
	}
}

// ---------- 低-04 枚举 ----------

func TestLoginEmailCodeSendDoesNotEnumerate(t *testing.T) {
	_, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPost, "/api/login/email-code",
		strings.NewReader(`{"email":"nobody@example.com"}`))
	rec := httptest.NewRecorder()
	h.loginEmailCodeSend(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("未绑定邮箱不应暴露 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "未绑定") {
		t.Fatalf("响应不应区分邮箱是否绑定：%s", rec.Body.String())
	}
}

func TestPortalSendCodeDoesNotEnumerate(t *testing.T) {
	_, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPost, "/api/portal/send-code",
		strings.NewReader(`{"email":"nobody@example.com"}`))
	rec := httptest.NewRecorder()
	h.portalSendCode(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("未绑定邮箱不应暴露 404，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "success") {
		t.Fatalf("应返回统一成功响应：%s", rec.Body.String())
	}
}

// ---------- 中-03 / 中-06 静态加密 ----------

func newTestSecretBox(t *testing.T) *secretBox {
	t.Helper()
	t.Setenv("PP_SECRET_KEY", "")
	box := LoadSecretBox(filepath.Join(t.TempDir(), "secret.key"), testLogger())
	if !box.Enabled() {
		t.Fatal("测试 KEK 未启用")
	}
	return box
}

func TestSecretBoxRoundTripAndWrongKEK(t *testing.T) {
	box := newTestSecretBox(t)
	sealed, err := box.Seal("hello-secret")
	if err != nil {
		t.Fatalf("Seal 失败：%v", err)
	}
	if !strings.HasPrefix(sealed, secretAtRestVersion) {
		t.Fatalf("密文格式 = %q", sealed)
	}
	if got := box.Open(sealed); got != "hello-secret" {
		t.Fatalf("Open = %q", got)
	}
	if got := (&secretBox{key: make([]byte, 32)}).Open(sealed); got != "" {
		t.Fatal("错误 KEK 不应能解密")
	}
}

func TestSealOpenAgentToken(t *testing.T) {
	box := newTestSecretBox(t)
	h := &handlers{box: box, cfg: config.Config{AgentKeySecret: "legacy-agent-key-32-bytes-long!!"}}
	sealed, err := h.sealAgentToken("ag_plain")
	if err != nil || !strings.HasPrefix(sealed, secretAtRestVersion) {
		t.Fatalf("sealAgentToken = %q, %v", sealed, err)
	}
	if got := h.openAgentToken(sealed); got != "ag_plain" {
		t.Fatalf("openAgentToken = %q", got)
	}
	// 旧 enc:v1: 兼容
	legacy, _ := encryptAgentToken(h.agentKeySecret(), "ag_legacy")
	if got := h.openAgentToken(legacy); got != "ag_legacy" {
		t.Fatalf("旧版 enc:v1 解密失败：%q", got)
	}
}

func TestMigrateSecretsAtRestEncrypts(t *testing.T) {
	box := newTestSecretBox(t)
	const legacyKEK = "legacy-agent-key-32-bytes-long!!"
	legacyEnc, _ := encryptAgentToken(legacyKEK, "smtp-password")
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT `key`, value FROM app_settings"):
			return fakeRowsSet([]string{"key", "value"},
				[]driver.Value{"jwt_secret", "plain-jwt"},
				[]driver.Value{"agent_key_secret", "plain-agent"},
				[]driver.Value{settingSMTPPass, legacyEnc},
				[]driver.Value{settingAgentBinaryBase, "https://cdn.example.com/agent"},
			), nil
		case strings.Contains(q, "FROM servers"):
			return fakeRowsSet([]string{"id", "agent_token_enc"},
				[]driver.Value{int64(7), legacyEnc}), nil
		case strings.Contains(q, "FROM vpn_certificates"):
			return fakeRowsSet([]string{"id", "key_pem", "key_pem_enc"},
				[]driver.Value{int64(1), "-----BEGIN PRIVATE KEY-----\nKEY\n", ""}), nil
		}
		return &fakeRows{}, nil
	}
	MigrateSecretsAtRest(t.Context(), db, box, legacyKEK, "auto", testLogger())

	var jwtUpdated, smtpUpdated, serverUpdated, certUpdated bool
	for _, call := range fdb.execCalls() {
		switch {
		case strings.Contains(call.Query, "UPDATE app_settings"):
			key, _ := call.Args[1].(string)
			val, _ := call.Args[0].(string)
			if val == "" || !strings.HasPrefix(val, secretAtRestVersion) {
				continue
			}
			if key == "jwt_secret" {
				jwtUpdated = true
				if box.Open(val) != "plain-jwt" {
					t.Fatalf("jwt_secret 重加密后无法解密")
				}
			}
			if key == settingSMTPPass {
				smtpUpdated = true
				if box.Open(val) != "smtp-password" {
					t.Fatalf("smtp_pass 从 enc:v1 迁移后无法解密")
				}
			}
		case strings.Contains(call.Query, "UPDATE servers SET agent_token_enc"):
			serverUpdated = true
			if val, _ := call.Args[0].(string); box.Open(val) != "smtp-password" {
				t.Fatalf("servers.agent_token_enc 迁移失败")
			}
		case strings.Contains(call.Query, "UPDATE vpn_certificates"):
			certUpdated = true
			if !strings.Contains(call.Query, "key_pem = ''") {
				t.Fatalf("迁移后应清空明文 key_pem：%s", call.Query)
			}
			if enc, _ := call.Args[0].(string); box.Open(enc) == "" {
				t.Fatalf("vpn 私钥密文无法解密")
			}
		}
	}
	if !jwtUpdated || !smtpUpdated || !serverUpdated || !certUpdated {
		t.Fatalf("迁移不完整：jwt=%v smtp=%v server=%v cert=%v", jwtUpdated, smtpUpdated, serverUpdated, certUpdated)
	}
	// 普通设置不能被加密（它本来就不是密钥）
	for _, call := range fdb.execCalls() {
		if strings.Contains(call.Query, "UPDATE app_settings") {
			if key, _ := call.Args[1].(string); key == settingAgentBinaryBase {
				t.Fatal("普通设置不应被静态加密")
			}
		}
	}
}

func TestMigrateSecretsAtRestRollback(t *testing.T) {
	box := newTestSecretBox(t)
	const legacyKEK = "legacy-agent-key-32-bytes-long!!"
	jwtSealed, _ := box.Seal("plain-jwt")
	smtpSealed, _ := box.Seal("smtp-password")
	certSealed, _ := box.Seal("PRIVATE-KEY")
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT `key`, value FROM app_settings"):
			return fakeRowsSet([]string{"key", "value"},
				[]driver.Value{"jwt_secret", jwtSealed},
				[]driver.Value{settingSMTPPass, smtpSealed},
			), nil
		case strings.Contains(q, "FROM servers"):
			return &fakeRows{}, nil
		case strings.Contains(q, "FROM vpn_certificates"):
			return fakeRowsSet([]string{"id", "key_pem", "key_pem_enc"},
				[]driver.Value{int64(1), "", certSealed}), nil
		}
		return &fakeRows{}, nil
	}
	MigrateSecretsAtRest(t.Context(), db, box, legacyKEK, "decrypt", testLogger())

	var jwtPlain, smtpLegacy, certPlain bool
	for _, call := range fdb.execCalls() {
		switch {
		case strings.Contains(call.Query, "UPDATE app_settings"):
			key, _ := call.Args[1].(string)
			val, _ := call.Args[0].(string)
			if key == "jwt_secret" && val == "plain-jwt" {
				jwtPlain = true
			}
			if key == settingSMTPPass && strings.HasPrefix(val, agentKeyVersion) {
				if decryptAgentToken(legacyKEK, val) != "smtp-password" {
					t.Fatalf("回滚后的 enc:v1 无法用旧 KEK 解密")
				}
				smtpLegacy = true
			}
		case strings.Contains(call.Query, "UPDATE vpn_certificates"):
			if val, _ := call.Args[0].(string); val == "PRIVATE-KEY" {
				certPlain = true
			}
		}
	}
	if !jwtPlain || !smtpLegacy || !certPlain {
		t.Fatalf("回滚不完整：jwt=%v smtp=%v cert=%v", jwtPlain, smtpLegacy, certPlain)
	}
}

// EnsurePersistentSecret 必须能读取 enc:v2: 并返回明文，且写入时加密。
func TestEnsurePersistentSecretHandlesV2(t *testing.T) {
	box := newTestSecretBox(t)
	sealed, _ := box.Seal("stored-jwt-secret-32-bytes-long!!")
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT value FROM app_settings") {
			return fakeRowsSet([]string{"value"}, []driver.Value{sealed}), nil
		}
		return &fakeRows{}, nil
	}
	if got := EnsurePersistentSecret(t.Context(), db, "jwt_secret", "env-value", 32, box, testLogger()); got != "stored-jwt-secret-32-bytes-long!!" {
		t.Fatalf("应解密库内密钥，got %q", got)
	}

	// 空库 + 环境密钥：写入库的必须是密文
	fdb2, db2 := newFakeDB(t)
	const envSecret = "env-jwt-secret-32-bytes-long!!!!"
	if got := EnsurePersistentSecret(t.Context(), db2, "jwt_secret", envSecret, 32, box, testLogger()); got != envSecret {
		t.Fatalf("返回值 = %q", got)
	}
	call, ok := fdb2.lastExec("INSERT INTO app_settings")
	if !ok {
		t.Fatal("未写入 app_settings")
	}
	stored, _ := call.Args[1].(string)
	if !strings.HasPrefix(stored, secretAtRestVersion) || box.Open(stored) != envSecret {
		t.Fatalf("库内密钥未加密：%q", stored)
	}

	// KEK 不匹配且未提供环境密钥：必须拒绝启动态（返回空），且不能覆盖原密文。
	wrongBox := &secretBox{key: make([]byte, 32)}
	fdb3, db3 := newFakeDB(t)
	fdb3.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT value FROM app_settings") {
			return fakeRowsSet([]string{"value"}, []driver.Value{sealed}), nil
		}
		return &fakeRows{}, nil
	}
	if got := EnsurePersistentSecret(t.Context(), db3, "jwt_secret", "", 32, wrongBox, testLogger()); got != "" {
		t.Fatalf("KEK 不匹配时应返回空（触发启动失败保护），got %q", got)
	}
	if n := fdb3.countExec("INSERT INTO app_settings"); n != 0 {
		t.Fatal("KEK 不匹配时绝不能覆盖库内密文")
	}
}