package api

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseCertTaskOutput(t *testing.T) {
	out := parseCertTaskOutput("singbox_service=active\nsingbox_sha256=abc123\n34:48:6A:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66\nrestart scheduled\n")
	if out.Service != "active" || out.SHA256 != "abc123" || out.Fingerprint != "34:48:6A:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66" {
		t.Fatalf("parse = %+v", out)
	}
	if got := normalizeCertFingerprint(out.Fingerprint); got != "34486AAABBCCDDEEFF00112233445566778899AABBCCDDEEFF00112233445566" {
		t.Fatalf("normalize = %q", got)
	}
	if parseCertTaskOutput("foo\nrestart scheduled").Fingerprint != "" {
		t.Fatal("非指纹行不应被识别")
	}
	if !looksLikeCertFingerprint("34486AABBCCDDEEFF34486AABBCCDDEEFF") || looksLikeCertFingerprint("not-a-fingerprint") {
		t.Fatal("指纹识别错误")
	}
}

func TestRecordCertTaskResult(t *testing.T) {
	fdb, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	h.recordCertTaskResult(context.Background(), 7, true,
		"singbox_service=active\nsingbox_sha256=abc\n34:48:6A:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66\n")
	call, ok := fdb.lastExec("UPDATE servers SET cert_status")
	if !ok || len(call.Args) < 5 {
		t.Fatalf("未写证书状态：%#v", call)
	}
	if call.Args[0] != "synced" || call.Args[1] != "34486AAABBCCDDEEFF00112233445566778899AABBCCDDEEFF00112233445566" || call.Args[2].(int64) == 0 || call.Args[3] != "" || call.Args[4] != int64(7) {
		t.Fatalf("成功状态参数异常：%#v", call.Args)
	}

	h.recordCertTaskResult(context.Background(), 8, false, "cert/key mismatch")
	call, _ = fdb.lastExec("UPDATE servers SET cert_status")
	if call.Args[0] != "failed" || !strings.Contains(call.Args[3].(string), "cert/key mismatch") {
		t.Fatalf("失败状态参数异常：%#v", call.Args)
	}

	h.recordCertTaskResult(context.Background(), 9, true, "no fingerprint here")
	call, _ = fdb.lastExec("UPDATE servers SET cert_status")
	if call.Args[0] != "failed" || !strings.Contains(call.Args[3].(string), "缺少指纹") {
		t.Fatalf("缺少指纹应 failed：%#v", call.Args)
	}
}

func TestCertRetryEligibleAndRenewWindow(t *testing.T) {
	now := int64(1800000000000)
	if !certRetryEligible(true, "", 0, now) || !certRetryEligible(true, "failed", 0, now) {
		t.Fatal("空/失败状态应可重试")
	}
	if certRetryEligible(true, "synced", 0, now) {
		t.Fatal("成功状态不应重试")
	}
	if certRetryEligible(false, "failed", 0, now) {
		t.Fatal("离线节点不应重试")
	}
	if !certRetryEligibleForCert(true, "synced", 0, now, now-1) {
		t.Fatal("证书已过期的 synced 节点应重试")
	}
	if certRetryEligibleForCert(true, "synced", 0, now, now+86400000) {
		t.Fatal("证书未过期的 synced 节点不应重试")
	}
	if certRetryEligible(true, "pending", now-10*60*1000, now) {
		t.Fatal("30 分钟冷却内不应重试")
	}
	if !certRetryEligible(true, "pending", now-30*60*1000, now) {
		t.Fatal("超过 30 分钟应可重试")
	}

	if !certNeedsRenew(now+29*24*3600*1000, now) {
		t.Fatal("剩余<30天应续期")
	}
	if certNeedsRenew(now+31*24*3600*1000, now) {
		t.Fatal("剩余>30天不应续期")
	}
	if certNeedsRenew(0, now) {
		t.Fatal("无到期时间不应续期")
	}
}

func TestCronCertRetryQueuesOnlineTarget(t *testing.T) {
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
				[]driver.Value{"yzfn.top,*.yzfn.top", "acme", certPEM, keyPEM, "", "AA:BB", int64(1), int64(9999999999999), int64(3), ""},
			), nil
		case strings.Contains(q, "FROM servers"):
			return fakeRowsSet([]string{"id", "name", "cert_status", "cert_synced_at"},
				[]driver.Value{int64(7), "hk-01", "failed", int64(0)},
			), nil
		case strings.Contains(q, "FROM agent_tasks"):
			if strings.Contains(q, "SELECT COUNT(*)") {
				return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
			}
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
	h.settings = map[string]string{"vpn_cert_retry_at": "0"}
	h.settingsAt = time.Now()
	h.cronCertRetry(context.Background(), 1800000000000)

	if fdb.countExec("INSERT INTO agent_tasks") != 1 {
		t.Fatalf("应重新排队证书任务，exec=%d", fdb.countExec("INSERT INTO agent_tasks"))
	}
	var pending bool
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "UPDATE servers SET cert_status") && len(c.Args) > 0 && c.Args[0] == "pending" {
			pending = true
		}
	}
	if !pending {
		t.Fatal("入队后应标记 cert_status=pending")
	}
}

func TestCronCertRenewWindowAndConcurrency(t *testing.T) {
	installTestCertDirs(t)
	var calls []string
	withCertSystemctl(t, "active", &calls)
	oldPEM, keyPEM := testCertPair(t, "yzfn.top")
	newPEM, newKeyPEM := testCertPair(t, "yzfn.top")
	now := int64(1800000000000)
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM vpn_certificates WHERE id = 1") {
			return fakeRowsSet(
				[]string{"domains", "source", "cert_pem", "key_pem", "chain_pem", "fingerprint", "not_before", "not_after", "updated_at", "key_pem_enc"},
				[]driver.Value{"yzfn.top,*.yzfn.top", "acme", oldPEM, keyPEM, "", "OLD", int64(1), now + 10*24*3600*1000, now, ""},
			), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}
	h.settings = map[string]string{"vpn_cert_renew_check_at": "0"}
	h.settingsAt = time.Now()

	oldIssue := certRenewIssue
	issueCount := 0
	certRenewIssue = func(h *handlers, ctx context.Context, domains []string) (*storedCert, error) {
		issueCount++
		return &storedCert{
			Domains: "yzfn.top,*.yzfn.top", Source: "acme", CertPEM: newPEM, KeyPEM: newKeyPEM,
			Fingerprint: "NEW", NotBefore: now, NotAfter: now + 90*24*3600*1000, UpdatedAt: now,
		}, nil
	}
	t.Cleanup(func() { certRenewIssue = oldIssue })

	h.cronCertRenew(context.Background(), now)
	if issueCount != 1 {
		t.Fatalf("应触发一次续期，实际 %d", issueCount)
	}
	var renewed bool
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "INSERT INTO app_settings") && len(c.Args) >= 1 && c.Args[0] == "vpn_cert_last_renew_at" {
			renewed = true
		}
	}
	if !renewed {
		t.Fatal("续期成功应记录 last_renew_at")
	}

	// 并发：锁被占用时 TryLock 直接跳过，不再发起签发。
	if err := func() error { return nil }(); err != nil {
		t.Fatal(err)
	}
	certRenewMu.Lock()
	h.settings = map[string]string{"vpn_cert_renew_check_at": "0"}
	h.settingsAt = time.Now()
	h.cronCertRenew(context.Background(), now+24*3600*1000)
	// 上面调用在加锁后应立即返回；若触发了 issue 则测试失败。
	if issueCount != 1 {
		t.Fatalf("并发锁失效，issueCount=%d", issueCount)
	}
	certRenewMu.Unlock()

	// 续期失败不更新时间，也不覆盖旧证书。
	issueCount = 0
	h.settings = map[string]string{"vpn_cert_renew_check_at": "0"}
	h.settingsAt = time.Now()
	certRenewIssue = func(h *handlers, ctx context.Context, domains []string) (*storedCert, error) {
		issueCount++
		return nil, errors.New("acme down")
	}
	h.cronCertRenew(context.Background(), now+48*3600*1000)
	if issueCount != 1 {
		t.Fatalf("失败也应尝试一次，issueCount=%d", issueCount)
	}
}
