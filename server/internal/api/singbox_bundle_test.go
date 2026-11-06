package api

import "testing"

func TestSingboxRenderBundleValidation(t *testing.T) {
	base := &singboxTemplate{
		Name: "t", PortBase: 13700, WarpBase: 13800,
		UsersJSON: `[{"name":"u1","uuid":"11111111-2222-3333-4444-555555555555","ss_password":"p1"}]`,
	}
	node := singboxRenderNode{Name: "n1", Domain: "example.com"}

	// vless-ws 缺域名
	tpl := *base
	tpl.EnableVLWS = 1
	if _, _, _, msg := singboxRenderBundle(&tpl, singboxRenderNode{Name: "n1"}, serverRow{}, nil); msg == "" {
		t.Fatal("启用 vlws 但无域名应报错")
	}

	// 占位 UUID
	demo := *base
	demo.UsersJSON = `[{"name":"u","uuid":"00000000-0000-4000-8000-000000000000","ss_password":"p"}]`
	if _, _, _, msg := singboxRenderBundle(&demo, node, serverRow{}, nil); msg == "" {
		t.Fatal("占位 UUID 应报错")
	}

	// 正常：仅 sing-box → 1 份 sb.json
	files, ports, users, msg := singboxRenderBundle(&tpl, node, serverRow{}, nil)
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if len(files) != 1 || files[0]["key"] != "sb" {
		t.Fatalf("files = %#v", files)
	}
	if ports["ss"] != 13702 {
		t.Fatalf("ports = %#v", ports)
	}
	if len(users) != 1 || users[0]["uuid"] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("users = %#v", users)
	}
}
