package api

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/config"
)

type clientErrorAlertMail struct {
	To      string
	Subject string
	Body    string
}

func clientErrorAlertTestHandler(t *testing.T, count int64) (*handlers, *fakeDB) {
	t.Helper()
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT COUNT(*) FROM client_errors"):
			return fakeRowsSet([]string{"n"}, []driver.Value{count}), nil
		case strings.Contains(q, "GROUP BY mode"):
			return fakeRowsSet([]string{"mode", "cnt"},
				[]driver.Value{"admin", int64(3)},
				[]driver.Value{"portal", int64(2)},
			), nil
		case strings.Contains(q, "GROUP BY message"):
			return fakeRowsSet([]string{"message", "cnt"},
				[]driver.Value{strings.Repeat("m", 300), int64(5)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{
		db:  db,
		log: testLogger(),
		cfg: config.Config{PublicBaseURL: "https://panel.example.com"},
		settings: map[string]string{
			"client_error_alert_enabled":      "1",
			"client_error_alert_threshold":    "5",
			"client_error_alert_cooldown_min": "30",
			"client_error_alert_emails":       "",
			"monitor_notify_emails":           "fallback@example.com",
		},
		settingsAt: time.Now(),
	}
	return h, fdb
}

func withClientErrorAlertMail(t *testing.T, fn func(h *handlers, to, subject, body string) error) *[]clientErrorAlertMail {
	t.Helper()
	orig := clientErrorAlertSendMail
	var sent []clientErrorAlertMail
	clientErrorAlertSendMail = func(h *handlers, to, subject, body string) error {
		sent = append(sent, clientErrorAlertMail{To: to, Subject: subject, Body: body})
		if fn != nil {
			return fn(h, to, subject, body)
		}
		return nil
	}
	t.Cleanup(func() { clientErrorAlertSendMail = orig })
	return &sent
}

func TestClientErrorAlertThresholdAndFallbackRecipients(t *testing.T) {
	sent := withClientErrorAlertMail(t, nil)
	h, fdb := clientErrorAlertTestHandler(t, 5)
	now := int64(1800000000000)
	if err := h.maybeAlertClientErrors(context.Background(), now); err != nil {
		t.Fatalf("告警失败：%v", err)
	}
	if len(*sent) != 1 || (*sent)[0].To != "fallback@example.com" {
		t.Fatalf("收件人回退/发送次数异常：%#v", *sent)
	}
	if !strings.Contains((*sent)[0].Body, "最近 5 分钟") || !strings.Contains((*sent)[0].Body, "admin") ||
		!strings.Contains((*sent)[0].Body, "panel.example.com") {
		t.Fatalf("告警邮件内容异常：%s", (*sent)[0].Body)
	}
	if len([]rune((*sent)[0].Body)) > 50000 {
		t.Fatal("告警邮件异常过大")
	}
	var alertAt bool
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "INSERT INTO app_settings") && len(c.Args) >= 1 && c.Args[0] == "client_error_alert_at" {
			alertAt = true
		}
	}
	if !alertAt {
		t.Fatal("发送成功应更新 client_error_alert_at")
	}
}

func TestClientErrorAlertBelowThresholdAndCooldown(t *testing.T) {
	sent := withClientErrorAlertMail(t, nil)
	h, _ := clientErrorAlertTestHandler(t, 4)
	if err := h.maybeAlertClientErrors(context.Background(), 1800000000000); err != nil {
		t.Fatalf("低于阈值不应报错：%v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("低于阈值不应发送：%#v", *sent)
	}

	h2, _ := clientErrorAlertTestHandler(t, 10)
	h2.settings["client_error_alert_emails"] = "ops@example.com"
	h2.settings["client_error_alert_at"] = itoa64(1800000000000 - 10*60_000)
	if err := h2.maybeAlertClientErrors(context.Background(), 1800000000000); err != nil {
		t.Fatalf("冷却中不应报错：%v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("冷却中不应发送：%#v", *sent)
	}
}

func TestClientErrorAlertNoRecipientsAndMailNotConfigured(t *testing.T) {
	sent := withClientErrorAlertMail(t, nil)
	h, _ := clientErrorAlertTestHandler(t, 10)
	h.settings["monitor_notify_emails"] = ""
	if err := h.maybeAlertClientErrors(context.Background(), 1800000000000); err != nil {
		t.Fatalf("无收件人应静默：%v", err)
	}
	if len(*sent) != 0 {
		t.Fatalf("无收件人不应发送：%#v", *sent)
	}

	h2, _ := clientErrorAlertTestHandler(t, 10)
	h2.settings["client_error_alert_emails"] = "ops@example.com"
	withClientErrorAlertMail(t, func(h *handlers, to, subject, body string) error {
		return errMailNotConfigured
	})
	if err := h2.maybeAlertClientErrors(context.Background(), 1800000000000); err != nil {
		t.Fatalf("邮件未配置应静默跳过：%v", err)
	}
}

func TestClientErrorAlertSendFailureNoTimestamp(t *testing.T) {
	withClientErrorAlertMail(t, func(h *handlers, to, subject, body string) error {
		return errors.New("smtp down")
	})
	h, fdb := clientErrorAlertTestHandler(t, 10)
	h.settings["client_error_alert_emails"] = "ops@example.com"
	if err := h.maybeAlertClientErrors(context.Background(), 1800000000000); err == nil {
		t.Fatal("发送失败应返回错误供异步日志记录")
	}
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "INSERT INTO app_settings") && len(c.Args) >= 1 && c.Args[0] == "client_error_alert_at" {
			t.Fatal("发送失败不应更新 alert_at")
		}
	}
}

func TestClientErrorAlertAsyncNoPanic(t *testing.T) {
	done := make(chan struct{}, 1)
	withClientErrorAlertMail(t, func(h *handlers, to, subject, body string) error {
		done <- struct{}{}
		return nil
	})
	h, _ := clientErrorAlertTestHandler(t, 10)
	h.settings["client_error_alert_emails"] = "ops@example.com"
	h.triggerClientErrorAlertAsync()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("异步告警未执行")
	}
	// 再次触发不应 panic（TryLock 下可能直接跳过）。
	h.triggerClientErrorAlertAsync()
	time.Sleep(20 * time.Millisecond)
}
