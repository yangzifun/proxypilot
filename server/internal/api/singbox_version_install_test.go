package api

import "testing"

func TestSingboxInstallVersionAlpha(t *testing.T) {
	tpl := &singboxTemplate{SingboxVersion: "1.15.0-alpha.13"}
	if got := singboxInstallVersions(tpl, "1.14.4"); got != `{"sing-box":"1.15.0-alpha.13"}` {
		t.Fatalf("模板指定版本时应优先使用桶内版本，got %s", got)
	}
	tpl = &singboxTemplate{}
	if got := singboxInstallVersions(tpl, "1.14.4"); got != `{"sing-box":"1.14.4"}` {
		t.Fatalf("模板留空时应使用默认版本，got %s", got)
	}
}
