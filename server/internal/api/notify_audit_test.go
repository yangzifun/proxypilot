package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNotifyStatusConcurrentOnlyOneSend(t *testing.T) {
	notifyStatusLastReserved = 0
	t.Cleanup(func() { notifyStatusLastReserved = 0 })
	settings := map[string]string{
		"monitor_notify_enabled":      "1",
		"monitor_notify_emails":       "ops@example.com,ops@example.com",
		"monitor_notify_cooldown_min": "10",
		"monitor_notify_last_at":      "0",
		"public_base_url":             "https://panel.example.com",
	}
	var mu sync.Mutex
	var notifications []string
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM app_settings"):
			rows := make([][]driver.Value, 0, len(settings))
			mu.Lock()
			for k, v := range settings {
				rows = append(rows, []driver.Value{k, v})
			}
			mu.Unlock()
			return fakeRowsSet([]string{"key", "value"}, rows...), nil
		case strings.Contains(q, "FROM servers WHERE id"):
			return fakeRowsSet([]string{"name", "host", "ip", "ip4", "ip6", "last_seen", "version", "err"},
				[]driver.Value{"hk-01", "1.1.1.1", "1.1.1.1", "", "", int64(1), "1.2", ""}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO app_settings") && len(args) >= 2 {
			mu.Lock()
			settings[args[0].(string)] = args[1].(string)
			mu.Unlock()
		}
		if strings.Contains(q, "INSERT INTO notification_logs") && len(args) > 2 {
			mu.Lock()
			notifications = append(notifications, args[4].(string))
			mu.Unlock()
		}
		return fakeResult(1, 0), nil
	}
	oldSend := notifyStatusSendMail
	var sends int32
	notifyStatusSendMail = func(h *handlers, to, subject, body string) error {
		atomic.AddInt32(&sends, 1)
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	t.Cleanup(func() { notifyStatusSendMail = oldSend })

	h := &handlers{db: db, log: testLogger(), settings: settings, settingsAt: time.Now()}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.notifyStatus(context.Background(), "服务器恢复", []serverStatusNotice{{ID: 1, Detail: "恢复"}})
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&sends); got != 1 {
		t.Fatalf("并发 notifyStatus 只应发一封，实际 %d", got)
	}
	mu.Lock()
	defer mu.Unlock()
	sent := 0
	for _, s := range notifications {
		if s == "sent" {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("notification_logs sent 应 1 条，实际 %d %v", sent, notifications)
	}
}

func TestNotifyStatusFailureRecordedAndCooled(t *testing.T) {
	notifyStatusLastReserved = 0
	t.Cleanup(func() { notifyStatusLastReserved = 0 })
	settings := map[string]string{
		"monitor_notify_enabled":      "1",
		"monitor_notify_emails":       "ops@example.com",
		"monitor_notify_cooldown_min": "10",
		"monitor_notify_last_at":      "0",
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM app_settings") {
			rows := make([][]driver.Value, 0, len(settings))
			for k, v := range settings {
				rows = append(rows, []driver.Value{k, v})
			}
			return fakeRowsSet([]string{"key", "value"}, rows...), nil
		}
		if strings.Contains(q, "FROM servers WHERE id") {
			return fakeRowsSet([]string{"name", "host", "ip", "ip4", "ip6", "last_seen", "version", "err"},
				[]driver.Value{"hk-01", "1.1.1.1", "1.1.1.1", "", "", int64(1), "1.2", ""}), nil
		}
		return &fakeRows{}, nil
	}
	var logArgs []any
	var sends int32
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO notification_logs") {
			logArgs = fakeArgsAny(args)
		}
		return fakeResult(1, 0), nil
	}
	oldSend := notifyStatusSendMail
	notifyStatusSendMail = func(h *handlers, to, subject, body string) error {
		atomic.AddInt32(&sends, 1)
		return errors.New("smtp down")
	}
	t.Cleanup(func() { notifyStatusSendMail = oldSend })
	h := &handlers{db: db, log: testLogger(), settings: settings, settingsAt: time.Now()}
	h.notifyStatus(context.Background(), "服务器离线", []serverStatusNotice{{ID: 1, Detail: "超时"}})
	if len(logArgs) < 6 || logArgs[4] != "failed" || !strings.Contains(logArgs[5].(string), "smtp down") {
		t.Fatalf("失败通知日志异常：%#v", logArgs)
	}
	// 失败已预占冷却，第二次不应再发信。
	h.notifyStatus(context.Background(), "服务器离线", []serverStatusNotice{{ID: 1, Detail: "超时"}})
	if got := atomic.LoadInt32(&sends); got != 1 {
		t.Fatalf("失败后不应重复发送，实际 %d", got)
	}
}

func TestMarkServerOfflineOnlyOnTransition(t *testing.T) {
	fdb, db := newFakeDB(t)
	var updates int
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE servers SET last_status = 'offline'") {
			updates++
			if updates == 1 {
				return fakeResult(1, 0), nil
			}
			return fakeResult(0, 0), nil
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	if !h.markServerOffline(context.Background(), 1, 1000, "超时") {
		t.Fatal("首次 offline→offline 切换应成功")
	}
	if h.markServerOffline(context.Background(), 1, 2000, "超时") {
		t.Fatal("已是 offline 不应重复写事件")
	}
	if n := fdb.countExec("INSERT INTO server_status_events"); n != 1 {
		t.Fatalf("offline 事件应只写一次，实际 %d", n)
	}
}

func TestNotifyAuditAPIs(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "COUNT(*) FROM notification_logs"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
		case strings.Contains(q, "FROM notification_logs"):
			return fakeRowsSet(
				[]string{"id", "kind", "title", "server_ids", "recipients", "status", "error", "created_at"},
				[]driver.Value{int64(1), "offline", "服务器离线", "[1]", "a@example.com", "sent", "", int64(2000)},
			), nil
		case strings.Contains(q, "COUNT(*) FROM server_status_events"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
		case strings.Contains(q, "FROM server_status_events"):
			return fakeRowsSet(
				[]string{"id", "server_id", "event", "reason", "occurred_at", "duration_sec", "created_at"},
				[]driver.Value{int64(1), int64(1), "online", "恢复", int64(2000), int64(120), int64(2000)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}

	rec := httptest.NewRecorder()
	h.notificationLogsList(rec, httptest.NewRequest(http.MethodGet, "/api/notification-logs?page=1&size=20&kind=offline&status=sent", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("notification logs status=%d body=%s", rec.Code, rec.Body.String())
	}
	var logs map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &logs)
	if logs["total"] != float64(1) || len(logs["data"].([]any)) != 1 {
		t.Fatalf("notification logs 响应异常：%s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.serverStatusEventsList(rec, httptest.NewRequest(http.MethodGet,
		"/api/server-status-events?page=1&size=20&server_id=1&event=online&from=1000&to=3000", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status events status=%d body=%s", rec.Code, rec.Body.String())
	}
	var events map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &events)
	if events["total"] != float64(1) || len(events["data"].([]any)) != 1 {
		t.Fatalf("status events 响应异常：%s", rec.Body.String())
	}
}

func TestMarkServerOnlineOnlyOnce(t *testing.T) {
	fdb, db := newFakeDB(t)
	var updates int
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE servers SET last_status = 'online'") {
			updates++
			if updates == 1 {
				return fakeResult(1, 0), nil
			}
			return fakeResult(0, 0), nil
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	if !h.markServerOnlineIfOffline(context.Background(), 3) {
		t.Fatal("首次 offline→online 应成功")
	}
	if h.markServerOnlineIfOffline(context.Background(), 3) {
		t.Fatal("并发/重复恢复不应再次成功")
	}
}
