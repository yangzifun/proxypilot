package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMailProviderResolve(t *testing.T) {
	auto := mailConfig{Addr: "smtp:25", From: "a@b.c"}
	if auto.ResolvedProvider() != "smtp" {
		t.Fatalf("auto/smtp = %q", auto.ResolvedProvider())
	}
	cf := mailConfig{CFToken: "t", CFAccountID: "a", CFFrom: "a@b.c", Addr: "smtp:25", From: "a@b.c"}
	if cf.ResolvedProvider() != "cloudflare" {
		t.Fatalf("auto/cf = %q", cf.ResolvedProvider())
	}
	forced := mailConfig{Provider: "smtp", CFToken: "t", CFAccountID: "a", CFFrom: "a@b.c"}
	if forced.ResolvedProvider() != "smtp" {
		t.Fatalf("forced smtp = %q", forced.ResolvedProvider())
	}
	if !forced.Ready() || (mailConfig{}).Ready() {
		t.Fatal("Ready 判定错误")
	}
}

func TestHTMLToText(t *testing.T) {
	got := htmlToText("<h1>Title</h1><p>Hello&nbsp;World</p><br/>line2")
	if !strings.Contains(got, "Title") || !strings.Contains(got, "Hello") || strings.Contains(got, "<") {
		t.Fatalf("htmlToText = %q", got)
	}
}

func TestSendMailCloudflare(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"delivered":["to@example.com"]}}`))
	}))
	defer srv.Close()

	old := cloudflareEmailEndpoint
	cloudflareEmailEndpoint = srv.URL + "/accounts/%s/email/sending/send"
	defer func() { cloudflareEmailEndpoint = old }()

	h := &handlers{}
	mc := mailConfig{CFToken: "token-123", CFAccountID: "acc-1", CFFrom: "from@example.com"}
	if err := h.sendMailCloudflare(mc, "to@example.com", "Hi", "<b>Hello</b>"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotAuth != "Bearer token-123" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotPath != "/accounts/acc-1/email/sending/send" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody["to"] != "to@example.com" || gotBody["from"] != "from@example.com" || gotBody["subject"] != "Hi" {
		t.Fatalf("body = %#v", gotBody)
	}
	if !strings.Contains(gotBody["text"].(string), "Hello") {
		t.Fatalf("text = %#v", gotBody["text"])
	}
}

func TestSendMailCloudflareError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10102,"message":"authentication.forbidden"}]}`))
	}))
	defer srv.Close()
	old := cloudflareEmailEndpoint
	cloudflareEmailEndpoint = srv.URL + "/accounts/%s/email/sending/send"
	defer func() { cloudflareEmailEndpoint = old }()

	h := &handlers{}
	err := h.sendMailCloudflare(mailConfig{CFToken: "t", CFAccountID: "a", CFFrom: "f@e.com"}, "t@e.com", "s", "<p>x</p>")
	if err == nil || !strings.Contains(err.Error(), "10102") {
		t.Fatalf("err = %v", err)
	}
}
