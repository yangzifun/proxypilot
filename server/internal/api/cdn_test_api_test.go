package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// =====================================================================
//  CDN 优选单地址测试接口单测（网络三阶段全部注入，不联网）
// =====================================================================

type cdnFakeAddr struct{}

func (cdnFakeAddr) Network() string { return "tcp" }
func (cdnFakeAddr) String() string  { return "fake" }

type cdnFakeConn struct{}

func (cdnFakeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (cdnFakeConn) Write(b []byte) (int, error)      { return len(b), nil }
func (cdnFakeConn) Close() error                     { return nil }
func (cdnFakeConn) LocalAddr() net.Addr              { return cdnFakeAddr{} }
func (cdnFakeConn) RemoteAddr() net.Addr             { return cdnFakeAddr{} }
func (cdnFakeConn) SetDeadline(time.Time) error      { return nil }
func (cdnFakeConn) SetReadDeadline(time.Time) error  { return nil }
func (cdnFakeConn) SetWriteDeadline(time.Time) error { return nil }

// cdnNetStub 注入解析与三阶段网络，记录调用参数。
type cdnNetStub struct {
	resolved    []string
	resolveErr  error
	dialErr     error
	tlsErr      error
	httpStatus  int
	httpErr     error
	dialTargets []string
	tlsSNIs     []string
	httpPaths   []string
}

func (s *cdnNetStub) install(t *testing.T) *cdnNetStub {
	t.Helper()
	origResolve, origNet := cdnTestResolve, cdnTestNet
	cdnTestResolve = func(ctx context.Context, addr string) ([]string, error) {
		if s.resolveErr != nil {
			return nil, s.resolveErr
		}
		if s.resolved != nil {
			return s.resolved, nil
		}
		return resolveCDNAddress(ctx, addr)
	}
	cdnTestNet = cdnProbeTransport{
		Dial: func(ctx context.Context, target string) (net.Conn, error) {
			s.dialTargets = append(s.dialTargets, target)
			if s.dialErr != nil {
				return nil, s.dialErr
			}
			return cdnFakeConn{}, nil
		},
		TLS: func(conn net.Conn, sni string) (net.Conn, error) {
			s.tlsSNIs = append(s.tlsSNIs, sni)
			if s.tlsErr != nil {
				return nil, s.tlsErr
			}
			return conn, nil
		},
		HTTP: func(conn net.Conn, sni, path string) (int, error) {
			s.httpPaths = append(s.httpPaths, sni+"|"+path)
			if s.httpErr != nil {
				return 0, s.httpErr
			}
			return s.httpStatus, nil
		},
	}
	t.Cleanup(func() {
		cdnTestResolve = origResolve
		cdnTestNet = origNet
	})
	return s
}

func callCDNPreferredTest(t *testing.T, h *handlers, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/cdn-preferred/test", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.cdnPreferredTest(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败（code=%d）：%s", rec.Code, rec.Body.String())
	}
	return rec.Code, out
}

func cdnTestData(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	data, ok := out["data"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 data：%v", out)
	}
	return data
}

func newCDNTestHandlers(t *testing.T, onQuery func(query string, args []driver.Value) (driver.Rows, error)) *handlers {
	t.Helper()
	fdb, db := newFakeDB(t)
	if onQuery != nil {
		fdb.onQuery = onQuery
	}
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) {
		return fakeResult(1, 0), nil
	}
	return &handlers{db: db, log: testLogger()}
}

func TestCDNPreferredTestValidation(t *testing.T) {
	h := newCDNTestHandlers(t, func(query string, args []driver.Value) (driver.Rows, error) {
		return &fakeRows{}, nil // id 查询无记录
	})
	cases := []struct {
		name string
		body string
		want int
	}{
		{"空 body", `{}`, http.StatusBadRequest},
		{"地址为空白", `{"address":"   "}`, http.StatusBadRequest},
		{"非法 JSON", `{bad`, http.StatusBadRequest},
		{"port 越界", `{"address":"162.159.198.1","port":70000}`, http.StatusBadRequest},
		{"port 为 0", `{"address":"162.159.198.1","port":0}`, http.StatusBadRequest},
		{"id 不存在", `{"id":999}`, http.StatusNotFound},
	}
	for _, tc := range cases {
		code, _ := callCDNPreferredTest(t, h, tc.body)
		if code != tc.want {
			t.Fatalf("%s：code = %d，期望 %d", tc.name, code, tc.want)
		}
	}
}

func TestCDNPreferredTestTargetBlocked(t *testing.T) {
	stub := (&cdnNetStub{}).install(t)
	h := newCDNTestHandlers(t, nil)
	code, out := callCDNPreferredTest(t, h, `{"address":"127.0.0.1"}`)
	if code != http.StatusOK {
		t.Fatalf("目标不允许应仍返回 200，得到 %d", code)
	}
	data := cdnTestData(t, out)
	if data["ok"] != false || data["tcp_ok"] != false {
		t.Fatalf("内网地址不应通过：%v", data)
	}
	if !strings.Contains(data["error"].(string), "目标地址不允许") {
		t.Fatalf("error = %v", data["error"])
	}
	if len(stub.dialTargets) != 0 {
		t.Fatalf("目标不允许时不应发起连接：%v", stub.dialTargets)
	}
	resolved, _ := data["resolved"].([]any)
	if len(resolved) != 1 || resolved[0] != "127.0.0.1" {
		t.Fatalf("resolved = %v", data["resolved"])
	}
}

func TestCDNPreferredTestResolveFailure(t *testing.T) {
	(&cdnNetStub{resolveErr: errors.New("no such host")}).install(t)
	h := newCDNTestHandlers(t, nil)
	code, out := callCDNPreferredTest(t, h, `{"address":"nonexistent.invalid"}`)
	if code != http.StatusOK {
		t.Fatalf("解析失败应 200，得到 %d", code)
	}
	data := cdnTestData(t, out)
	if data["ok"] != false || !strings.Contains(data["error"].(string), "目标地址不允许") {
		t.Fatalf("解析失败结果 = %v", data)
	}
}

func TestCDNPreferredTestTCPFailure(t *testing.T) {
	stub := (&cdnNetStub{resolved: []string{"104.16.1.1"}, dialErr: errors.New("connection refused")}).install(t)
	h := newCDNTestHandlers(t, nil)
	code, out := callCDNPreferredTest(t, h, `{"address":"cdn.example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("TCP 失败应 200，得到 %d", code)
	}
	data := cdnTestData(t, out)
	if data["ok"] != false || data["tcp_ok"] != false || data["tls_ok"] != false {
		t.Fatalf("TCP 失败结果 = %v", data)
	}
	if !strings.HasPrefix(data["error"].(string), "TCP:") {
		t.Fatalf("error 应以 TCP: 开头：%v", data["error"])
	}
	if len(stub.dialTargets) != 1 || stub.dialTargets[0] != "cdn.example.com:443" {
		t.Fatalf("dial target = %v", stub.dialTargets)
	}
	resolved, _ := data["resolved"].([]any)
	if len(resolved) != 1 || resolved[0] != "104.16.1.1" {
		t.Fatalf("resolved = %v", data["resolved"])
	}
}

func TestCDNPreferredTestTLSFailure(t *testing.T) {
	(&cdnNetStub{resolved: []string{"104.16.1.1"}, tlsErr: errors.New("bad certificate")}).install(t)
	h := newCDNTestHandlers(t, nil)
	_, out := callCDNPreferredTest(t, h, `{"address":"cdn.example.com","sni":"cdn.example.com"}`)
	data := cdnTestData(t, out)
	if data["tcp_ok"] != true || data["tls_ok"] != false || data["ok"] != false {
		t.Fatalf("TLS 失败结果 = %v", data)
	}
	if !strings.HasPrefix(data["error"].(string), "TLS:") {
		t.Fatalf("error 应以 TLS: 开头：%v", data["error"])
	}
}

func TestCDNPreferredTestHTTPSuccess(t *testing.T) {
	stub := (&cdnNetStub{resolved: []string{"104.16.1.1"}, httpStatus: http.StatusSwitchingProtocols}).install(t)
	h := newCDNTestHandlers(t, nil)
	_, out := callCDNPreferredTest(t, h, `{"address":"cdn.example.com","sni":"cdn.example.com","path":"/vlws"}`)
	data := cdnTestData(t, out)
	if data["ok"] != true || data["tcp_ok"] != true || data["tls_ok"] != true {
		t.Fatalf("HTTP 101 结果 = %v", data)
	}
	if data["http_status"] != float64(http.StatusSwitchingProtocols) {
		t.Fatalf("http_status = %v", data["http_status"])
	}
	if data["error"] != "" {
		t.Fatalf("error 应为空：%v", data["error"])
	}
	if len(stub.httpPaths) != 1 || stub.httpPaths[0] != "cdn.example.com|/vlws" {
		t.Fatalf("http 参数 = %v", stub.httpPaths)
	}
	if data["latency_ms"].(float64) < 0 {
		t.Fatalf("latency_ms 异常：%v", data["latency_ms"])
	}
}

func TestCDNPreferredTestHTTPStatusError(t *testing.T) {
	(&cdnNetStub{resolved: []string{"104.16.1.1"}, httpStatus: http.StatusForbidden}).install(t)
	h := newCDNTestHandlers(t, nil)
	_, out := callCDNPreferredTest(t, h, `{"address":"cdn.example.com","sni":"cdn.example.com","path":"/vlws"}`)
	data := cdnTestData(t, out)
	if data["ok"] != false {
		t.Fatalf("403 应判失败：%v", data)
	}
	if data["http_status"] != float64(http.StatusForbidden) || data["error"] != "HTTP 403" {
		t.Fatalf("403 结果 = %v", data)
	}
}

func TestCDNPreferredTestHTTPReadError(t *testing.T) {
	(&cdnNetStub{resolved: []string{"104.16.1.1"}, httpErr: errors.New("unexpected EOF")}).install(t)
	h := newCDNTestHandlers(t, nil)
	_, out := callCDNPreferredTest(t, h, `{"address":"cdn.example.com","sni":"cdn.example.com","path":"/vlws"}`)
	data := cdnTestData(t, out)
	if data["ok"] != false || !strings.HasPrefix(data["error"].(string), "HTTP:") {
		t.Fatalf("HTTP 读失败结果 = %v", data)
	}
}

// sni/path 不传 → 取全局检测设置；显式空串 → 跳过该阶段。
func TestCDNPreferredTestDefaultsFromGlobalSettings(t *testing.T) {
	stub := (&cdnNetStub{resolved: []string{"104.16.1.1"}, httpStatus: http.StatusSwitchingProtocols}).install(t)
	h := newCDNTestHandlers(t, func(query string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "cdn_check_sni"):
			return fakeRowsSet([]string{"value"}, []driver.Value{"cdn.example.com"}), nil
		case strings.Contains(query, "cdn_check_path"):
			return fakeRowsSet([]string{"value"}, []driver.Value{"/vlws"}), nil
		}
		return &fakeRows{}, nil
	})
	_, out := callCDNPreferredTest(t, h, `{"address":"162.159.198.1"}`)
	data := cdnTestData(t, out)
	if data["ok"] != true {
		t.Fatalf("默认设置下应成功：%v", data)
	}
	if len(stub.tlsSNIs) != 1 || stub.tlsSNIs[0] != "cdn.example.com" {
		t.Fatalf("未使用全局 SNI：%v", stub.tlsSNIs)
	}
	if len(stub.httpPaths) != 1 || stub.httpPaths[0] != "cdn.example.com|/vlws" {
		t.Fatalf("未使用全局 path：%v", stub.httpPaths)
	}

	// 显式空串：跳过 TLS/HTTP，只做 TCP 检查
	stub2 := (&cdnNetStub{resolved: []string{"104.16.1.1"}}).install(t)
	_, out = callCDNPreferredTest(t, h, `{"address":"162.159.198.1","sni":"","path":""}`)
	data = cdnTestData(t, out)
	if data["ok"] != true || data["tcp_ok"] != true || data["tls_ok"] != false || data["http_status"] != float64(0) {
		t.Fatalf("显式空串应只做 TCP：%v", data)
	}
	if len(stub2.tlsSNIs) != 0 || len(stub2.httpPaths) != 0 {
		t.Fatalf("显式空串不应发起 TLS/HTTP：%v/%v", stub2.tlsSNIs, stub2.httpPaths)
	}
}

// id 模式：从库里取 address/port（记录存在）。
func TestCDNPreferredTestByIDUsesDB(t *testing.T) {
	stub := (&cdnNetStub{}).install(t)
	h := newCDNTestHandlers(t, func(query string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "FROM cdn_preferred_addresses WHERE id"):
			return fakeRowsSet([]string{"address", "port"},
				[]driver.Value{"162.159.198.1", int64(8443)}), nil
		case strings.Contains(query, "cdn_check_sni"), strings.Contains(query, "cdn_check_path"):
			return &fakeRows{}, nil // 全局未配置 → 只做 TCP
		}
		return &fakeRows{}, nil
	})
	_, out := callCDNPreferredTest(t, h, `{"id":5}`)
	data := cdnTestData(t, out)
	if data["address"] != "162.159.198.1" || data["port"] != float64(8443) {
		t.Fatalf("id 模式未使用库中地址：%v", data)
	}
	if len(stub.dialTargets) != 1 || stub.dialTargets[0] != "162.159.198.1:8443" {
		t.Fatalf("dial target = %v", stub.dialTargets)
	}
	if data["ok"] != true || data["tcp_ok"] != true {
		t.Fatalf("id 模式结果 = %v", data)
	}
}

// 域名解析结果要完整返回 resolved 列表。
func TestCDNPreferredTestResolvedDomainList(t *testing.T) {
	(&cdnNetStub{resolved: []string{"104.16.1.1", "104.16.2.2"}}).install(t)
	h := newCDNTestHandlers(t, nil)
	_, out := callCDNPreferredTest(t, h, `{"address":"cdn.example.com"}`)
	data := cdnTestData(t, out)
	resolved, _ := data["resolved"].([]any)
	if len(resolved) != 2 || resolved[0] != "104.16.1.1" || resolved[1] != "104.16.2.2" {
		t.Fatalf("resolved = %v", data["resolved"])
	}
}
