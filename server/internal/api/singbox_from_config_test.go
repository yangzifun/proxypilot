package api

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseSbConfig(t *testing.T) {
	raw := `{
	  "inbounds": [
	    {"type":"shadowsocks","listen_port":13702,"method":"2022-blake3-aes-256-gcm","password":"KEY",
	     "users":[{"name":"u1","password":"PW1"}]},
	    {"type":"vless","listen_port":13705,"users":[{"name":"u1","uuid":"uuid-1"}],
	     "tls":{"server_name":"www.example.com","reality":{"private_key":"pk","short_id":["sid"],
	       "handshake":{"server":"www.microsoft.com","server_port":443}}}}
	  ],
	  "endpoints": [
	    {"type":"masque-server","listen_port":13707,"path":"/mq","version":[1,3]}
	  ]
	}`
	p, msg := parseSbConfig(raw)
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if len(p.Users) != 1 {
		t.Fatalf("users = %#v", p.Users)
	}
	u := p.Users[0]
	if u.UUID != "uuid-1" || u.SSPassword != "PW1" {
		t.Fatalf("user = %#v", u)
	}
	if p.Enable["ss"] != 1 || p.Enable["reality"] != 1 || p.Enable["masque"] != 1 {
		t.Fatalf("enable = %#v", p.Enable)
	}
	if p.Ports["ss"] != 13702 || p.Ports["reality"] != 13705 || p.MasquePort != 13707 {
		t.Fatalf("ports = %#v masque=%d", p.Ports, p.MasquePort)
	}
	if p.Reality["dest"] != "www.microsoft.com:443" || p.Reality["short_id"] != "sid" {
		t.Fatalf("reality = %#v", p.Reality)
	}
	if p.SSMethod != "2022-blake3-aes-256-gcm" || p.SSKey != "KEY" {
		t.Fatalf("ss = %q/%q", p.SSMethod, p.SSKey)
	}
	if len(p.MasqueVersions) != 2 || p.MasqueVersions[1] != 3 {
		t.Fatalf("masque versions = %#v", p.MasqueVersions)
	}

	if _, msg := parseSbConfig("not-json"); msg == "" {
		t.Fatal("非法 JSON 应报错")
	}
	if _, msg := parseSbConfig(`{"inbounds":[]}`); msg == "" {
		t.Fatal("空 inbounds 应报错")
	}
}

func TestParseSbConfigCDNListen(t *testing.T) {
	raw := `{"inbounds":[{"type":"vless","tag":"vless-ws-cdn","listen":"0.0.0.0","listen_port":13707,
	  "transport":{"type":"ws","path":"/vlws"},"users":[{"name":"u1","uuid":"uuid-1"}]}]}`
	p, msg := parseSbConfig(raw)
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if p.CDNPort != 13707 || p.CDNListen != "0.0.0.0" {
		t.Fatalf("CDN port/listen = %d/%q", p.CDNPort, p.CDNListen)
	}
	if p.Enable["vlws"] != 1 {
		t.Fatalf("vlws enable = %#v", p.Enable)
	}

	// 旧模板常见形态：tag 无 -cdn、listen=127.0.0.1（当前识别条件）
	legacy := `{"inbounds":[{"type":"vless","tag":"vless-ws","listen":"127.0.0.1","listen_port":13707,
	  "transport":{"type":"ws","path":"/vlws"},"users":[{"name":"u1","uuid":"uuid-1"}]}]}`
	p2, msg := parseSbConfig(legacy)
	if msg != "" || p2.CDNListen != "127.0.0.1" {
		t.Fatalf("legacy CDN listen = %q msg=%s", p2.CDNListen, msg)
	}

	// 没有 listen 字段：CDNListen 为空，render 保持默认 127.0.0.1
	noListen := `{"inbounds":[{"type":"vless","tag":"vless-ws-cdn","listen_port":13707,
	  "transport":{"type":"ws","path":"/vlws"},"users":[{"name":"u1","uuid":"uuid-1"}]}]}`
	p3, msg := parseSbConfig(noListen)
	if msg != "" || p3.CDNListen != "" {
		t.Fatalf("缺省 listen = %q msg=%s", p3.CDNListen, msg)
	}
}

func TestFromConfigSavesSingboxVersion(t *testing.T) {
	fdb, db := newFakeDB(t)
	var insertArgs []driver.Value
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "COUNT(*) FROM singbox_templates") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		}
		if strings.Contains(q, "SELECT * FROM singbox_templates") {
			return fakeRowsSet([]string{"id", "name", "singbox_version"}, []driver.Value{
				int64(1), "t", "1.15.0-alpha.13",
			}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		insertArgs = args
		return fakeResult(1, 1), nil
	}
	h := &handlers{db: db, log: testLogger()}
	body := `{"name":"t","singbox_version":"1.15.0-alpha.13","sb_json":"{\"inbounds\":[{\"type\":\"vless\",\"tag\":\"vless-ws\",\"listen_port\":13706,\"transport\":{\"type\":\"ws\",\"path\":\"/vlws\"},\"users\":[{\"name\":\"u1\",\"uuid\":\"11111111-1111-4111-8111-111111111111\"}]}]}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/singbox/templates/from-config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.singboxTemplateFromConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	found := false
	for _, a := range insertArgs {
		if s, ok := a.(string); ok && s == "1.15.0-alpha.13" {
			found = true
		}
	}
	if !found {
		t.Fatalf("singbox_version 未保存：%#v", insertArgs)
	}
}

func TestFromConfigSavesCDNListen(t *testing.T) {
	fdb, db := newFakeDB(t)
	var insertQuery string
	var insertArgs []driver.Value
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "COUNT(*) FROM singbox_templates") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		}
		if strings.Contains(q, "SELECT * FROM singbox_templates") {
			return fakeRowsSet([]string{"id", "name", "protocol_options"}, []driver.Value{
				int64(1), "t", `{"vlws":{"cdn_port":13707,"cdn_listen":"0.0.0.0"}}`,
			}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		insertQuery, insertArgs = q, args
		return fakeResult(1, 1), nil
	}
	h := &handlers{db: db, log: testLogger()}
	body := `{"name":"t","sb_json":"{\"inbounds\":[{\"type\":\"vless\",\"tag\":\"vless-ws-cdn\",\"listen\":\"0.0.0.0\",\"listen_port\":13707,\"transport\":{\"type\":\"ws\",\"path\":\"/vlws\"},\"users\":[{\"name\":\"u1\",\"uuid\":\"11111111-1111-4111-8111-111111111111\"}]}]}"}`
	req := httptest.NewRequest(http.MethodPost, "/api/singbox/templates/from-config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.singboxTemplateFromConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(insertQuery, "INSERT INTO `singbox_templates`") {
		t.Fatalf("未写模板：%s", insertQuery)
	}
	found := false
	for _, a := range insertArgs {
		if s, ok := a.(string); ok && strings.Contains(s, `"cdn_listen":"0.0.0.0"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("protocol_options 未保存 cdn_listen：%#v", insertArgs)
	}
}
