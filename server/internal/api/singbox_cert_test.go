package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql/driver"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testCertPair(t *testing.T, cn string) (string, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     []string{cn, "*." + cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM)
}

func installTestCertDirs(t *testing.T) {
	t.Helper()
	oldMesh, oldVPN, oldSing, oldWait := certLocalMeshDir, certLocalVPNDir, certLocalSingboxDir, certLocalRestartWait
	certLocalMeshDir = t.TempDir()
	certLocalVPNDir = t.TempDir()
	certLocalSingboxDir = t.TempDir()
	certLocalRestartWait = 0
	t.Cleanup(func() {
		certLocalMeshDir, certLocalVPNDir, certLocalSingboxDir, certLocalRestartWait = oldMesh, oldVPN, oldSing, oldWait
	})
}

func withCertSystemctl(t *testing.T, state string, calls *[]string) {
	t.Helper()
	old := certSystemctl
	certSystemctl = func(args ...string) ([]byte, error) {
		*calls = append(*calls, strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "is-active" {
			if args[1] == "sing-box" {
				return []byte(state), nil
			}
			return []byte("active"), nil
		}
		return []byte("ok"), nil
	}
	t.Cleanup(func() { certSystemctl = old })
}

func TestCertInstallScriptHasSingbox(t *testing.T) {
	cert := &storedCert{
		CertPEM:     "CERT",
		ChainPEM:    "CHAIN",
		KeyPEM:      "KEY",
		Fingerprint: "AA:BB",
	}
	script := certInstallScript(cert, "节点 VPN")
	for _, want := range []string{
		"/etc/proxypilot/tunnel/tls/cert.pem",
		"/etc/proxypilot/tunnel/tls/key.pem",
		"/etc/sing-box/cert.crt",
		"/etc/sing-box/private.key",
		"write_atomic", "chmod \"$mode\"",
		"chmod \"$mode\" \"$tmp\"",
		"singbox_sha256=", "cert/key mismatch", "sing-box", "proxypilot-agent",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("脚本缺少 %q", want)
		}
	}
	if strings.Count(script, "/etc/sing-box/cert.crt") < 3 {
		t.Fatalf("脚本未回读 sing-box 证书：%s", script)
	}
	// base64 内容不应包含可截断脚本的单引号（base64 字母表本身安全，且外层用单引号）。
	if strings.Contains(script, "\nCERT\n") || strings.Contains(script, "\nKEY\n") {
		t.Fatal("脚本疑似写入原始 PEM")
	}
}

func TestInstallLocalCertWritesTunnelAndSingbox(t *testing.T) {
	installTestCertDirs(t)
	var calls []string
	withCertSystemctl(t, "active", &calls)
	certPEM, keyPEM := testCertPair(t, "yzfn.top")
	cert := &storedCert{CertPEM: certPEM, KeyPEM: keyPEM, ChainPEM: ""}
	h := &handlers{log: testLogger()}
	ok, err := h.installLocalCert(cert, "vpn")
	if err != nil || !ok {
		t.Fatalf("installLocalCert vpn 失败：ok=%v err=%v", ok, err)
	}
	for _, f := range []string{
		filepath.Join(certLocalVPNDir, "cert.pem"),
		filepath.Join(certLocalVPNDir, "key.pem"),
		filepath.Join(certLocalSingboxDir, "cert.crt"),
		filepath.Join(certLocalSingboxDir, "private.key"),
	} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("缺少文件 %s：%v", f, err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(certLocalSingboxDir, "cert.crt"))
	if string(got) != certPEM {
		t.Fatal("sing-box cert 内容不一致")
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(filepath.Join(certLocalSingboxDir, "cert.crt")); info.Mode().Perm() != 0o644 {
			t.Fatalf("cert 权限=%o，期望 644", info.Mode().Perm())
		}
		if info, _ := os.Stat(filepath.Join(certLocalSingboxDir, "private.key")); info.Mode().Perm() != 0o600 {
			t.Fatalf("key 权限=%o，期望 600", info.Mode().Perm())
		}
	}
	if !certCallsContain(calls, "restart sing-box") || !certCallsContain(calls, "restart proxypilot-agent") {
		t.Fatalf("未重启 sing-box/agent：%v", calls)
	}
}

func TestInstallLocalCertInvalidPair(t *testing.T) {
	installTestCertDirs(t)
	var calls []string
	withCertSystemctl(t, "active", &calls)
	certPEM, _ := testCertPair(t, "a.example.com")
	_, otherKeyPEM := testCertPair(t, "b.example.com")
	cert := &storedCert{CertPEM: certPEM, KeyPEM: otherKeyPEM}
	h := &handlers{log: testLogger()}
	if _, err := h.installLocalCert(cert, "vpn"); err == nil {
		t.Fatal("证书/私钥不匹配应报错")
	}
}

func TestVPNCertNodes(t *testing.T) {
	certPEM, keyPEM := testCertPair(t, "yzfn.top")
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM vpn_certificates WHERE id = 1"):
			return fakeRowsSet(
				[]string{"domains", "source", "cert_pem", "key_pem", "chain_pem", "fingerprint", "not_before", "not_after", "updated_at", "key_pem_enc"},
				[]driver.Value{"yzfn.top,*.yzfn.top", "acme", certPEM, keyPEM, "", "AA:BB", int64(1), int64(2), int64(3), ""},
			), nil
		case strings.Contains(q, "FROM servers s"):
			return fakeRowsSet([]string{"id", "name", "domain", "services", "cert_fingerprint", "cert_status", "cert_synced_at", "cert_error"},
				[]driver.Value{int64(1), "hk-01", "hk.yzfn.top", `{"sing-box":"active"}`, "AA:BB", "synced", int64(3000), ""},
				[]driver.Value{int64(2), "jp-02", "jp.yzfn.top", `{"sing-box":"inactive"}`, "", "failed", int64(2000), "boom"},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.vpnCertNodes(rec, httptest.NewRequest(http.MethodGet, "/api/vpn/cert/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nodes status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if len(out.Data) != 2 {
		t.Fatalf("nodes data=%#v", out.Data)
	}
	if out.Data[0]["name"] != "hk-01" || out.Data[0]["domain"] != "hk.yzfn.top" ||
		out.Data[0]["singbox_service"] != "active" || out.Data[0]["singbox_installed"] != true ||
		out.Data[0]["fingerprint"] != "AA:BB" || out.Data[0]["certificate"] != "synced" ||
		out.Data[0]["last_synced_at"] != float64(3000) {
		t.Fatalf("节点字段异常：%#v", out.Data[0])
	}
	if out.Data[1]["singbox_service"] != "inactive" {
		t.Fatalf("第二个节点字段异常：%#v", out.Data[1])
	}
}

func TestVPNCertDistributeResult(t *testing.T) {
	installTestCertDirs(t)
	var calls []string
	withCertSystemctl(t, "active", &calls)
	certPEM, keyPEM := testCertPair(t, "yzfn.top")
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM vpn_certificates WHERE id = 1"):
			return fakeRowsSet(
				[]string{"domains", "source", "cert_pem", "key_pem", "chain_pem", "fingerprint", "not_before", "not_after", "updated_at", "key_pem_enc"},
				[]driver.Value{"yzfn.top,*.yzfn.top", "acme", certPEM, keyPEM, "", "AA:BB", int64(1), int64(2), int64(3), ""},
			), nil
		case strings.Contains(q, "FROM servers s"):
			return fakeRowsSet([]string{"id", "name", "domain", "services", "cert_fingerprint", "cert_status", "cert_synced_at", "cert_error"},
				[]driver.Value{int64(1), "hk-01", "hk.yzfn.top", `{"sing-box":"active"}`, "", "", int64(0), ""},
			), nil
		case strings.Contains(q, "SELECT COUNT(*) FROM agent_tasks"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		case strings.Contains(q, "FROM agent_tasks"):
			return &fakeRows{}, nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO agent_tasks") {
			return fakeResult(1, 123), nil
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.vpnCertDistribute(rec, httptest.NewRequest(http.MethodPost, "/api/vpn/cert/distribute", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("distribute status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Fingerprint string           `json:"fingerprint"`
		Results     []map[string]any `json:"results"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Fingerprint != "AA:BB" || len(out.Results) != 3 {
		t.Fatalf("distribute 响应异常：%#v", out)
	}
	if out.Results[2]["singbox_service"] != "active" || out.Results[2]["singbox_installed"] != true {
		t.Fatalf("远端节点 singbox 字段异常：%#v", out.Results[2])
	}
}

func certCallsContain(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
