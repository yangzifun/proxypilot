package api

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

const dohTestUUID = "11111111-1111-4111-8111-111111111111"

func newDoHTestHandler(t *testing.T, settings map[string]string) (*handlers, *fakeDB) {
	t.Helper()
	fdb, db := newFakeDB(t)
	h := &handlers{
		db:  db,
		log: testLogger(),
		cfg: config.Config{JWTSecret: "doh-test-secret", PortalTokenTTL: time.Hour},
	}
	h.settings = settings
	h.settingsAt = time.Now()
	return h, fdb
}

// startDoHUpstream 启动一个 TLS mock 上游，并让 dohForward 使用它的客户端。
func startDoHUpstream(t *testing.T, handler http.HandlerFunc) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	old := dohUpstreamClient
	dohUpstreamClient = srv.Client()
	t.Cleanup(func() {
		dohUpstreamClient = old
		srv.Close()
	})
	return srv, srv.URL + "/dns-query"
}

func dohActiveQueries(fdb *fakeDB) {
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info WHERE uuid") {
			return fakeRowsSet([]string{"status"}, []driver.Value{"active"}), nil
		}
		return &fakeRows{}, nil
	}
}

func dohRequest(method, uuid, query string) *http.Request {
	target := "/dns-query/" + uuid
	var body *strings.Reader
	contentType := ""
	if method == http.MethodGet {
		target += "?dns=" + base64.RawURLEncoding.EncodeToString([]byte(query))
	} else {
		body = strings.NewReader(query)
		contentType = "application/dns-message"
	}
	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, body)
		req.Header.Set("Content-Type", contentType)
	}
	req.SetPathValue("uuid", uuid)
	return req
}

func TestDoHGETAndPOSTSuccess(t *testing.T) {
	wireResp := []byte{0x12, 0x34, 0x01, 0x00, 0xaa, 0xbb}
	var gotMethod, gotCT string
	_, upstream := startDoHUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotCT = r.Method, r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(wireResp)
	})
	dohRate = newDoHRateLimiter()
	h, fdb := newDoHTestHandler(t, map[string]string{
		"doh_enabled": "1", "doh_upstreams": upstream, "doh_max_qps": "20",
	})
	dohActiveQueries(fdb)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		h.dohQuery(rec, dohRequest(method, dohTestUUID, "\x00\x01\x02\x03\x04\x05"))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", method, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "application/dns-message" {
			t.Fatalf("%s content-type = %q", method, got)
		}
		if string(rec.Body.Bytes()) != string(wireResp) {
			t.Fatalf("%s body mismatch", method)
		}
		if gotMethod != http.MethodPost || gotCT != "application/dns-message" {
			t.Fatalf("上游应收到 POST + dns-message，实际 %s/%q", gotMethod, gotCT)
		}
	}
	// 两次成功均写统计。
	if n := fdb.countExec("doh_usage_hourly"); n != 2 {
		t.Fatalf("统计 upsert 次数 = %d，期望 2", n)
	}
	call, _ := fdb.lastExec("doh_usage_hourly")
	if len(call.Args) < 4 || call.Args[0] != dohTestUUID || call.Args[2].(int64) == 0 || call.Args[3].(int64) == 0 {
		t.Fatalf("统计参数异常：%#v", call.Args)
	}
}

func TestDoHValidationAndDisabled(t *testing.T) {
	upstreamHit := 0
	_, upstream := startDoHUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHit++
		_, _ = w.Write([]byte{1, 2, 3})
	})
	dohRate = newDoHRateLimiter()
	h, fdb := newDoHTestHandler(t, map[string]string{"doh_enabled": "1", "doh_upstreams": upstream})
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info WHERE uuid") {
			return fakeRowsSet([]string{"status"}, []driver.Value{"inactive"}), nil
		}
		return &fakeRows{}, nil
	}
	rec := httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodGet, dohTestUUID, "abc"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("inactive UUID 应 403，实际 %d", rec.Code)
	}

	// suspended UUID 同样拒绝。
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info WHERE uuid") {
			return fakeRowsSet([]string{"status"}, []driver.Value{"suspended"}), nil
		}
		return &fakeRows{}, nil
	}
	rec = httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodGet, dohTestUUID, "abc"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("suspended UUID 应 403，实际 %d", rec.Code)
	}

	// 非法 UUID
	rec = httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodGet, "not-a-uuid", "abc"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 UUID 应 400，实际 %d", rec.Code)
	}

	// DoH 关闭
	disabled, _ := newDoHTestHandler(t, map[string]string{"doh_enabled": "0"})
	rec = httptest.NewRecorder()
	disabled.dohQuery(rec, dohRequest(http.MethodGet, dohTestUUID, "abc"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DoH 关闭应 403，实际 %d", rec.Code)
	}
	if upstreamHit != 0 {
		t.Fatalf("校验失败不应访问上游，实际 %d 次", upstreamHit)
	}
}

func TestDoHUpstreamFailureAndFallback(t *testing.T) {
	wireResp := []byte{9, 8, 7, 6}
	_, base := startDoHUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/fail") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(wireResp)
	})
	dohRate = newDoHRateLimiter()
	h, fdb := newDoHTestHandler(t, map[string]string{
		"doh_enabled": "1", "doh_max_qps": "20",
		"doh_upstreams": `["` + strings.TrimSuffix(base, "/dns-query") + `/dns-query/fail","` + base + `"]`,
	})
	dohActiveQueries(fdb)
	rec := httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodPost, dohTestUUID, "\x01\x02\x03"))
	if rec.Code != http.StatusOK || string(rec.Body.Bytes()) != string(wireResp) {
		t.Fatalf("404 上游应 fallback 成功：%d %s", rec.Code, rec.Body.String())
	}

	// 全部上游失败 → 502，仍记录 bytes_out=0 的失败统计。
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info WHERE uuid") {
			return fakeRowsSet([]string{"status"}, []driver.Value{"active"}), nil
		}
		return &fakeRows{}, nil
	}
	h, fdb = newDoHTestHandler(t, map[string]string{
		"doh_enabled": "1", "doh_max_qps": "20",
		"doh_upstreams": strings.TrimSuffix(base, "/dns-query") + "/dns-query/fail",
	})
	dohRate = newDoHRateLimiter()
	dohActiveQueries(fdb)
	rec = httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodPost, dohTestUUID, "\x01\x02\x03"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("全部上游失败应 502，实际 %d", rec.Code)
	}
	call, ok := fdb.lastExec("doh_usage_hourly")
	if !ok || len(call.Args) < 4 || call.Args[3].(int64) != 0 {
		t.Fatalf("失败请求应记录 bytes_out=0：%#v", call.Args)
	}
}

func TestDoHRateLimit(t *testing.T) {
	_, upstream := startDoHUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte{1, 2, 3})
	})
	dohRate = newDoHRateLimiter()
	h, fdb := newDoHTestHandler(t, map[string]string{
		"doh_enabled": "1", "doh_upstreams": upstream, "doh_max_qps": "1",
	})
	dohActiveQueries(fdb)
	var last *httptest.ResponseRecorder
	for i := 0; i < 2; i++ {
		last = httptest.NewRecorder()
		h.dohQuery(last, dohRequest(http.MethodGet, dohTestUUID, "abc"))
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("第 2 次应 429，实际 %d", last.Code)
	}
	if got := last.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q", got)
	}
}

func TestDoHMissingUUIDRejectedAndBodyLimit(t *testing.T) {
	wireResp := []byte{7, 7, 7}
	upstreamHit := 0
	_, upstream := startDoHUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamHit++
		_, _ = w.Write(wireResp)
	})
	dohRate = newDoHRateLimiter()
	h, fdb := newDoHTestHandler(t, map[string]string{
		"doh_enabled": "1", "doh_upstreams": upstream, "doh_max_qps": "20", "doh_max_body_bytes": "512",
	})
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		return &fakeRows{}, nil // no uuid_info row -> reject
	}

	// 无记录 UUID → 403，不访问上游、不记成功统计。
	rec := httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodGet, dohTestUUID, "abc"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "UUID 不存在或未授权") {
		t.Fatalf("无记录 UUID 应 403：%d %s", rec.Code, rec.Body.String())
	}
	if upstreamHit != 0 {
		t.Fatalf("无记录 UUID 不应访问上游，实际 %d 次", upstreamHit)
	}
	if n := fdb.countExec("doh_usage_hourly"); n != 0 {
		t.Fatalf("无记录 UUID 不应记录 DoH 统计，实际 %d 次", n)
	}

	// 校验 body 限制/Content-Type 需要 active UUID。
	dohActiveQueries(fdb)
	big := strings.Repeat("x", 600)
	rec = httptest.NewRecorder()
	h.dohQuery(rec, dohRequest(http.MethodPost, dohTestUUID, big))
	if rec.Code != http.StatusBadRequest || upstreamHit != 0 {
		t.Fatalf("超限 body 应 400 且不打上游：%d hit=%d", rec.Code, upstreamHit)
	}

	req := httptest.NewRequest(http.MethodPost, "/dns-query/"+dohTestUUID, strings.NewReader("abc"))
	req.SetPathValue("uuid", dohTestUUID)
	req.Header.Set("Content-Type", "application/octet-stream")
	rec = httptest.NewRecorder()
	h.dohQuery(rec, req)
	if rec.Code != http.StatusBadRequest || upstreamHit != 0 {
		t.Fatalf("错误 Content-Type 应 400：%d hit=%d", rec.Code, upstreamHit)
	}
}

func TestDoHUUIDStatusOnlyActive(t *testing.T) {
	cases := []struct {
		name    string
		rows    []driver.Value
		status  string
		allowed bool
	}{
		{name: "missing", rows: nil, status: "missing", allowed: false},
		{name: "active", rows: []driver.Value{"active"}, status: "active", allowed: true},
		{name: "inactive", rows: []driver.Value{"inactive"}, status: "inactive", allowed: false},
		{name: "suspended", rows: []driver.Value{"suspended"}, status: "suspended", allowed: false},
		{name: "pending", rows: []driver.Value{"pending"}, status: "pending", allowed: false},
		{name: "empty", rows: []driver.Value{""}, status: "", allowed: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fdb, db := newFakeDB(t)
			fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
				if strings.Contains(q, "FROM uuid_info WHERE uuid") {
					if tc.rows == nil {
						return &fakeRows{}, nil
					}
					return fakeRowsSet([]string{"status"}, tc.rows), nil
				}
				return &fakeRows{}, nil
			}
			h := &handlers{db: db, log: testLogger()}
			status, ok, err := h.dohUUIDStatus(context.Background(), dohTestUUID)
			if err != nil || status != tc.status || ok != tc.allowed {
				t.Fatalf("status=%q ok=%v err=%v，期望 %q/%v", status, ok, err, tc.status, tc.allowed)
			}
		})
	}
}
func TestDoHConfigDefaultsAndValidation(t *testing.T) {
	h, _ := newDoHTestHandler(t, nil)
	rec := httptest.NewRecorder()
	h.dohConfigGet(rec, httptest.NewRequest(http.MethodGet, "/api/doh/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("config status = %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	data, _ := body["data"].(map[string]any)
	if data["domain"] != defaultDoHDomain || data["enabled"] != true || data["max_qps"] != float64(20) {
		t.Fatalf("默认配置异常：%#v", data)
	}

	if _, err := parseDoHUpstreams("http://insecure.example/dns-query"); err == nil {
		t.Fatal("HTTP 上游应拒绝")
	}
	if _, err := normalizeDoHDomain("https://bad.example/path"); err == nil {
		t.Fatal("带路径的 DoH 域名应拒绝")
	}
}

func TestDoHConfigPutPersists(t *testing.T) {
	h, fdb := newDoHTestHandler(t, map[string]string{})
	body := `{"doh_enabled":false,"doh_domain":"dns.example.org","doh_max_qps":30,"doh_log_queries":true}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/doh/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.dohConfigPut(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	found := false
	for _, call := range fdb.execCalls() {
		if strings.Contains(call.Query, "app_settings") && len(call.Args) >= 2 &&
			call.Args[0] == settingDoHDomain && call.Args[1] == "dns.example.org" {
			found = true
		}
	}
	if !found {
		t.Fatal("未保存 doh_domain")
	}
}

func TestDoHStatsAndPortal(t *testing.T) {
	h, fdb := newDoHTestHandler(t, map[string]string{"doh_domain": "dns.example.org"})
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM doh_usage_hourly h"):
			return fakeRowsSet(
				[]string{"uuid", "owner", "email", "queries", "bytes_in", "bytes_out", "last_used"},
				[]driver.Value{dohTestUUID, "u1", "u1@example.com", int64(12), int64(100), int64(200), int64(123456)},
			), nil
		case strings.Contains(q, "FROM uuid_info WHERE LOWER(email)"):
			return fakeRowsSet([]string{"uuid", "status"}, []driver.Value{dohTestUUID, "active"}), nil
		case strings.Contains(q, "FROM doh_usage_hourly WHERE uuid IN"):
			return fakeRowsSet([]string{"q", "last"}, []driver.Value{int64(7), int64(999)}), nil
		}
		return &fakeRows{}, nil
	}
	rec := httptest.NewRecorder()
	h.dohStats(rec, httptest.NewRequest(http.MethodGet, "/api/doh/stats?range=30d", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queries":12`) || !strings.Contains(rec.Body.String(), `"owner":"u1"`) {
		t.Fatalf("stats 响应异常：%d %s", rec.Code, rec.Body.String())
	}

	// portal 未登录 → 401
	rec = httptest.NewRecorder()
	h.portalDoH(rec, httptest.NewRequest(http.MethodGet, "/api/portal/doh", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("portal 未登录应 401，实际 %d", rec.Code)
	}
	token, err := auth.IssuePortalTokenTTL("u1@example.com", h.cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/portal/doh", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.portalDoH(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("portal status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != true || body["doh_url"] != "https://dns.example.org/dns-query/"+dohTestUUID ||
		body["queries_30d"] != float64(7) || body["last_used"] != float64(999) {
		t.Fatalf("portal 响应异常：%#v", body)
	}
}

func TestDoHRateLimiterWindow(t *testing.T) {
	l := newDoHRateLimiter()
	now := time.Unix(100, 0)
	if !l.allow("k", 1, now) {
		t.Fatal("第一次应放行")
	}
	if l.allow("k", 1, now) {
		t.Fatal("同一秒第二次应限速")
	}
	if !l.allow("k", 1, now.Add(time.Second)) {
		t.Fatal("下一窗口应放行")
	}
}
func TestDoHQuestionNameParsing(t *testing.T) {
	payload := make([]byte, 12)
	payload = append(payload, 3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1)
	if got := dohQuestionName(payload); got != "www.example.com" {
		t.Fatalf("qname = %q", got)
	}
	if got := dohQuestionName([]byte("bad")); got != "" {
		t.Fatalf("非法 wire 应返回空，got %q", got)
	}
}
