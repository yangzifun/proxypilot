package api

import (
	"database/sql/driver"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHiddifyCompatLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{
			in:   "anytls://uuid@example.com:443?sni=a&insecure=1&alpn=h2#frag",
			want: "anytls://uuid@example.com:443?sni=a&alpn=h2&insecure=0#frag",
		},
		{
			in:   "hysteria2://uuid@example.com:443?insecure=1&sni=a",
			want: "hysteria2://uuid@example.com:443?sni=a&insecure=0",
		},
		{
			in:   "hy2://uuid@example.com:443#frag",
			want: "hy2://uuid@example.com:443?insecure=0#frag",
		},
		{
			in:   "tuic://uuid@example.com:443?allow_insecure=1&sni=a&path=/x#frag",
			want: "tuic://uuid@example.com:443?sni=a&path=/x&allow_insecure=0#frag",
		},
		{
			in:   "anytls://uuid@example.com:443?insecure=1&insecure=0&sni=a",
			want: "anytls://uuid@example.com:443?sni=a&insecure=0",
		},
		{
			in:   "hy2://uuid@example.com:443?insecure=0",
			want: "hy2://uuid@example.com:443?insecure=0",
		},
		{
			in:   "vless://uuid@example.com:443?security=tls#x",
			want: "vless://uuid@example.com:443?security=tls#x",
		},
		{
			in:   "trojan://uuid@example.com:443?insecure=1",
			want: "trojan://uuid@example.com:443?insecure=1",
		},
	}
	for _, tc := range cases {
		if got := hiddifyCompatLine(tc.in); got != tc.want {
			t.Fatalf("hiddifyCompatLine(%q)\n got %q\nwant %q", tc.in, got, tc.want)
		}
	}
}

func TestHiddifyUserAgentAndApply(t *testing.T) {
	if !isHiddifyUserAgent("Hiddify/2.0 (iPhone)") || isHiddifyUserAgent("clash-verge/1.0") {
		t.Fatal("UA 判断错误")
	}
	lines := []string{
		"anytls://a@b:443?insecure=1&sni=x#n",
		"vless://a@b:443?security=tls#n",
	}
	out := applyHiddifyCompat(lines)
	if out[0] != "anytls://a@b:443?sni=x&insecure=0#n" || out[1] != lines[1] {
		t.Fatalf("apply = %#v", out)
	}
}

func TestHiddifySubscriptionHandlers(t *testing.T) {
	// 配置里的链接故意包含旧 insecure=1/allow_insecure=1、其它参数和 fragment。
	configLines := []string{
		"anytls://11111111-1111-4111-8111-111111111111@example.com:443?sni=a.example.com&insecure=1&alpn=h2#AnyTLS",
		"hysteria2://11111111-1111-4111-8111-111111111111@example.com:443?insecure=1&sni=b.example.com#HY2",
		"hy2://11111111-1111-4111-8111-111111111111@example.com:443#HY2-2",
		"tuic://11111111-1111-4111-8111-111111111111@example.com:443?allow_insecure=1&sni=c.example.com#TUIC",
		"vless://11111111-1111-4111-8111-111111111111@example.com:443?security=tls#VLESS",
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM uuid_info WHERE uuid"):
			return fakeRowsSet([]string{"status"}, []driver.Value{"active"}), nil
		case strings.Contains(q, "FROM configs WHERE uuid"):
			rows := make([][]driver.Value, 0, len(configLines))
			for _, line := range configLines {
				rows = append(rows, []driver.Value{line})
			}
			return fakeRowsSet([]string{"config_data"}, rows...), nil
		case strings.Contains(q, "FROM uuid_sub_sources"):
			return &fakeRows{}, nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}
	call := func(path, ua string) []string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetPathValue("uuid", "11111111-1111-4111-8111-111111111111")
		req.Header.Set("User-Agent", ua)
		rec := httptest.NewRecorder()
		if strings.HasPrefix(path, "/sub/") {
			h.rawSubscription(rec, req)
		} else {
			h.batchSubscription(rec, req)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rec.Body.String()))
		if err != nil {
			t.Fatalf("base64 解析失败：%v", err)
		}
		return strings.Split(string(decoded), "\n")
	}

	for _, path := range []string{"/sub/u", "/batch-configs/u"} {
		hiddify := strings.Join(call(path, "Hiddify/2.0"), "\n")
		if !strings.Contains(hiddify, "anytls://") || strings.Contains(hiddify, "insecure=1") ||
			strings.Contains(hiddify, "allow_insecure=1") {
			t.Fatalf("%s Hiddify 输出未清理不安全参数：%s", path, hiddify)
		}
		if strings.Count(hiddify, "insecure=0") != 4 {
			t.Fatalf("%s 应输出 4 个显式 insecure=0：%s", path, hiddify)
		}
		if !strings.Contains(hiddify, "sni=a.example.com") || !strings.Contains(hiddify, "#AnyTLS") ||
			!strings.Contains(hiddify, "alpn=h2") || !strings.Contains(hiddify, "allow_insecure=0") {
			t.Fatalf("%s 未保留其它参数/fragment：%s", path, hiddify)
		}

		normal := strings.Join(call(path, "clash-verge/1.0"), "\n")
		for _, line := range configLines {
			if !strings.Contains(normal, line) {
				t.Fatalf("%s 普通 UA 应原样：缺少 %s\n%s", path, line, normal)
			}
		}
		if strings.Contains(normal, "insecure=0") || strings.Contains(normal, "allow_insecure=0") {
			t.Fatalf("%s 普通 UA 不应追加安全参数：%s", path, normal)
		}
	}
}
