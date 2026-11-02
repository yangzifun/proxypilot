package warp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterAndDelete(t *testing.T) {
	var deleted string
	mux := http.NewServeMux()
	mux.HandleFunc("/v0a2158/reg", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("期望 POST，实际 %s", r.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "dev-1",
			"token": "tok-1",
			"config": map[string]any{
				"peers": []any{map[string]any{
					"public_key": "peer-pub",
					"endpoint":   map[string]any{"host": "engage.example:2408"},
				}},
				"interface": map[string]any{"addresses": map[string]any{"v4": "172.16.0.2", "v6": "2606:4700::1"}},
			},
		})
	})
	mux.HandleFunc("/v0a2158/reg/dev-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("期望 DELETE，实际 %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			t.Errorf("Authorization 头不正确: %q", r.Header.Get("Authorization"))
		}
		deleted = "dev-1"
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Version: "v0a2158", CFClient: "test", UserAgent: "test"}
	p, err := c.Register(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.DeviceID != "dev-1" || p.Token != "tok-1" || p.PeerPublicKey != "peer-pub" || p.IPv4 != "172.16.0.2" {
		t.Fatalf("注册结果不正确: %+v", p)
	}
	if p.PrivateKey == "" || p.PublicKey == "" {
		t.Fatal("缺少密钥")
	}
	if err := c.Delete(context.Background(), p.DeviceID, p.Token); err != nil {
		t.Fatal(err)
	}
	if deleted != "dev-1" {
		t.Fatal("Delete 未请求设备端点")
	}
}

func TestRegisterBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL, Version: "v0a2158"}
	if _, err := c.Register(context.Background()); err == nil {
		t.Fatal("429 应返回错误")
	}
}
