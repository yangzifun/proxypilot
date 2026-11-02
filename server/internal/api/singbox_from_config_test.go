package api

import "testing"

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
