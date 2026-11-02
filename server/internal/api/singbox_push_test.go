package api

import (
	"encoding/base64"
	"testing"
)

func TestSingboxUsersFromUUIDs(t *testing.T) {
	tpl := &singboxTemplate{SSServerKey: "SERVERKEY"}
	uuids := []string{"11111111-2222-3333-4444-555555555555", " ", "66666666-7777-8888-9999-000000000000"}
	users := singboxUsersFromUUIDs(tpl, uuids)
	if len(users) != 2 {
		t.Fatalf("users = %d，期望 2", len(users))
	}
	if users[0].Name != "user01" || users[1].Name != "user02" {
		t.Fatalf("names = %q/%q", users[0].Name, users[1].Name)
	}
	raw, err := base64.StdEncoding.DecodeString(users[0].SSPassword)
	if err != nil || len(raw) != 32 {
		t.Fatalf("ss_password 不是 32 字节 base64: %q", users[0].SSPassword)
	}
	// 同一 UUID 在不同节点必须得到相同密码
	again := singboxUsersFromUUIDs(&singboxTemplate{SSServerKey: "SERVERKEY"}, uuids[:1])
	if len(again) != 1 || again[0].SSPassword != users[0].SSPassword {
		t.Fatal("SS 密码派生不稳定")
	}
}
