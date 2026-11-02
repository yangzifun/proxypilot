package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// deployLine 构造一条 deploy 的 wire 记录（字段顺序与 Worker 的 agentWireLine 一致）。
func deployLine(id, configID int, name, path, content, sha, restart, check, service string, version int, backup bool) string {
	backupFlag := "0"
	if backup {
		backupFlag = "1"
	}
	return strings.Join([]string{
		strconv.Itoa(id), "deploy", strconv.Itoa(configID), b64(name), b64(path), b64(content), sha, backupFlag,
		b64(restart), b64(check), b64(service), strconv.Itoa(version), "", "0",
	}, "|")
}

func TestParseActionLineDeploy(t *testing.T) {
	line := deployLine(7, 3, "xray-hk", "/etc/xray/config.json", `{"a":1}`, strings.Repeat("ab", 32), "systemctl restart xray", "cat /etc/xray/config.json", "xray", 5, true)
	a, ok := ParseActionLine(line)
	if !ok {
		t.Fatal("应解析成功")
	}
	if a.ID != 7 || a.Type != "deploy" || a.ConfigID != 3 || a.Name != "xray-hk" || a.Path != "/etc/xray/config.json" {
		t.Fatalf("基础字段错：%+v", a)
	}
	if a.Content != `{"a":1}` || a.Sha256 != strings.Repeat("ab", 32) || !a.Backup {
		t.Fatalf("内容/校验字段错：%+v", a)
	}
	if a.RestartCmd != "systemctl restart xray" || a.CheckCmd != "cat /etc/xray/config.json" || a.ServiceName != "xray" || a.Version != 5 {
		t.Fatalf("动作字段错：%+v", a)
	}
	if a.DecodeErr {
		t.Fatalf("不应有解码错误：%+v", a)
	}
}

func TestParseActionLineMalformed(t *testing.T) {
	if _, ok := ParseActionLine("7|deploy|3"); ok {
		t.Fatal("字段不足应被丢弃")
	}
	if _, ok := ParseActionLine(deployLine(0, 0, "", "", "", "", "", "", "", 0, false)); ok {
		t.Fatal("id=0 应被丢弃")
	}
	fields := strings.Split(deployLine(7, 3, "", "", "", "", "", "", "", 0, false), "|")
	fields[0] = "abc"
	if _, ok := ParseActionLine(strings.Join(fields, "|")); ok {
		t.Fatal("id 非法应被丢弃")
	}
}

func TestParseActionLineBadBase64MarksDecodeErr(t *testing.T) {
	fields := strings.Split(deployLine(7, 3, "n", "/tmp/x", "content", "", "", "", "", 0, false), "|")
	fields[5] = "@@@not-base64@@@" // content 字段
	a, ok := ParseActionLine(strings.Join(fields, "|"))
	if !ok || !a.DecodeErr {
		t.Fatalf("内容解码失败必须置位 DecodeErr：%+v ok=%v", a, ok)
	}
	if a.Content != "" {
		t.Fatalf("解码失败时内容应为空：%q", a.Content)
	}
}

func TestParseWireMultiRecords(t *testing.T) {
	execLine := strings.Join([]string{"2", "exec", "0", "", "", "", "", "0", "", "", "", "0", b64("uname -a"), "30"}, "|")
	got := ParseWire(deployLine(1, 0, "a", "/tmp/a", "x", "", "", "", "", 0, false) + "~" + execLine + "~")
	if len(got) != 2 {
		t.Fatalf("应解析 2 条（尾部空记录忽略）：%d", len(got))
	}
	if got[1].Type != "exec" || got[1].Command != "uname -a" || got[1].Timeout != 30 {
		t.Fatalf("exec 记录解析错：%+v", got[1])
	}
	if len(ParseWire("")) != 0 {
		t.Fatal("空 wire 应返回空切片")
	}
}

func TestEncodeOutputTruncatesToMaxBytes(t *testing.T) {
	enc := EncodeOutput(strings.Repeat("x", MaxOutputBytes+100))
	decoded, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != MaxOutputBytes {
		t.Fatalf("截断后长度=%d，期望 %d", len(decoded), MaxOutputBytes)
	}
	if strings.TrimSpace(EncodeOutput("ok")) != b64("ok") {
		t.Fatalf("短输出不应被改动：%s", EncodeOutput("ok"))
	}
}

func TestPollSuccessSendsHeadersAndBody(t *testing.T) {
	var gotToken, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Agent-Token")
		gotUA = r.Header.Get("User-Agent")
		if r.URL.Path != "/api/agent/poll" || r.Method != http.MethodPost {
			t.Errorf("路径/方法错：%s %s", r.Method, r.URL.Path)
		}
		var body PollRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("请求体解析失败：%v", err)
		}
		// V11.0：连接 / 进程 / 网络 / 磁盘 IO / 温度必须真的随 poll 上报（否则后台监控恒为 0）
		// 用原始 JSON 再解一遍，确认字段名与 Worker 读取的键一致
		rawBody, _ := json.Marshal(body)
		var rawMap map[string]interface{}
		_ = json.Unmarshal(rawBody, &rawMap)
		for _, k := range []string{"conn", "proc", "net", "disk_io"} {
			if _, ok := rawMap[k]; !ok {
				t.Errorf("poll 载荷缺少字段 %q：%s", k, string(rawBody))
			}
		}
		if body.AgentVersion != "1.0.0" || body.Load != "0.1,0.2,0.3" || len(body.Applied) != 1 || body.Services["xray"] != "active" {
			t.Errorf("请求体字段错：%+v", body)
		}
		_ = json.NewEncoder(w).Encode(PollResponse{OK: true, ServerID: 4, ServerName: "hk-01", NextPollSec: 60, Wire: "x"})
	}))
	defer srv.Close()

	c := New(srv.URL+"/", "ag_test", "1.0.0", false)
	resp, err := c.Poll(context.Background(), PollRequest{
		AgentVersion: "1.0.0", Load: "0.1,0.2,0.3",
		Services: map[string]string{"xray": "active"},
		Applied:  []Applied{{ConfigID: 1, Version: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ServerID != 4 || resp.ServerName != "hk-01" || resp.NextPollSec != 60 {
		t.Fatalf("响应解析错：%+v", resp)
	}
	if gotToken != "ag_test" || gotUA != "ProxyPilot-Agent/1.0.0" {
		t.Fatalf("请求头错：token=%q ua=%q", gotToken, gotUA)
	}
}

func TestPollErrorsCarryStatusAndRetryAfter(t *testing.T) {
	cases := []struct {
		status int
		retry  string
		want   string
	}{
		{401, "", "HTTP 401"},
		{403, "", "HTTP 403"},
		{429, "7", "轮询过于频繁"},
	}
	for _, tc := range cases {
		status, retry, want := tc.status, tc.retry, tc.want
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"x"}`))
		}))
		c := New(srv.URL, "ag_test", "1.0.0", false)
		_, err := c.Poll(context.Background(), PollRequest{})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("status=%d：期望包含 %q，实际 %v", status, want, err)
		}
		if status == 429 {
			se, ok := err.(*StatusError)
			if !ok || se.RetryAfter != 7 || se.Status != 429 {
				t.Fatalf("应按 Retry-After 解析：%#v", err)
			}
		}
		srv.Close()
	}
}

func TestAckSendsResults(t *testing.T) {
	var got AckRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/ack" {
			t.Errorf("路径错：%s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("解析失败：%v", err)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "ag_test", "1.0.0", false)
	err := c.Ack(context.Background(), AckRequest{AgentVersion: "1.0.0", Results: []Result{{ID: 9, Status: "ok", OutputB64: b64("done")}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 1 || got.Results[0].ID != 9 || got.Results[0].Status != "ok" || got.Results[0].OutputB64 != b64("done") {
		t.Fatalf("ack 体错：%+v", got)
	}
}

func TestWaitStatusCodes(t *testing.T) {
	cases := []struct {
		code     int
		wantWoke bool
		wantErr  bool
	}{
		{http.StatusOK, true, false},
		{http.StatusNoContent, false, false},
		{http.StatusNotFound, false, true},
		{http.StatusInternalServerError, false, true},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/agent/wait" || r.Header.Get("X-Agent-Token") != "ag_test" {
				t.Errorf("请求不对：%s token=%s", r.URL.Path, r.Header.Get("X-Agent-Token"))
			}
			w.WriteHeader(c.code)
		}))
		cl := New(srv.URL, "ag_test", "1.5.4", false)
		woke, err := cl.Wait(context.Background(), 5)
		srv.Close()
		if woke != c.wantWoke || (err != nil) != c.wantErr {
			t.Fatalf("code=%d woke=%v err=%v", c.code, woke, err)
		}
		if c.code == http.StatusNotFound && !errors.Is(err, ErrWaitUnsupported) {
			t.Fatalf("404 应返回 ErrWaitUnsupported：%v", err)
		}
	}
}
