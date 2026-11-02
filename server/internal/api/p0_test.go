package api

import "testing"

func TestNormalizeEmailList(t *testing.T) {
	got := normalizeEmailList("A@Example.com, b@example.com; a@example.com  bad")
	if got != "a@example.com,b@example.com" {
		t.Fatalf("got %q", got)
	}
	if normalizeEmailList("") != "" {
		t.Fatal("空输入应为空")
	}
}

func TestIsSecretSetting(t *testing.T) {
	for _, k := range []string{settingSMTPPass, settingOIDCSecret, settingTurnstileSecret} {
		if !isSecretSetting(k) {
			t.Fatalf("%s 应为敏感项", k)
		}
	}
	for _, k := range []string{settingSMTPAddr, settingOIDCIssuer, settingPublicBaseURL} {
		if isSecretSetting(k) {
			t.Fatalf("%s 不应为敏感项", k)
		}
	}
}

func TestSingboxInstallVersions(t *testing.T) {
	tpl := &singboxTemplate{SingboxVersion: "v1.15.0"}
	got := singboxInstallVersions(tpl)
	want := `{"sing-box":"v1.15.0"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	tpl2 := &singboxTemplate{}
	if got := singboxInstallVersions(tpl2); got != "{}" {
		t.Fatalf("空版本应为 {}，得到 %s", got)
	}
}
