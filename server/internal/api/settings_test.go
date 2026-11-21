package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestSettingsSnapshotDefaults(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin", OIDCDisablePassword: false}}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	snap := h.settingsSnapshot(req)
	_ = rr
	eff, _ := snap["effective"].(map[string]any)
	if eff["password_enabled"] != true || eff["email_code_enabled"] != true {
		t.Fatalf("eff = %#v", eff)
	}
	if eff["oidc_enabled"] != false {
		t.Fatalf("OIDC 未配置时应为 false: %#v", eff)
	}
	if eff["mail_configured"] != false {
		t.Fatalf("未配置 SMTP 时 mail_configured 应为 false")
	}
}

func TestBoolString(t *testing.T) {
	cases := map[string]string{"1": "1", "true": "1", "on": "1", "0": "0", "false": "0", "": "0"}
	for in, want := range cases {
		got, ok := boolString(in)
		if !ok || got != want {
			t.Fatalf("boolString(%q) = %q,%v", in, got, ok)
		}
	}
	if _, ok := boolString("maybe"); ok {
		t.Fatal("非法布尔值应失败")
	}
}
