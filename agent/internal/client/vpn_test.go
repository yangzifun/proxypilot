package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVPNRegister(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/vpn/register" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-Agent-Token") != "ag_test" {
			t.Errorf("token header missing")
		}
		var in VPNRegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		if !in.Warp || in.Endpoint != "hk1.example.com:443" {
			t.Errorf("payload = %+v", in)
		}
		_ = json.NewEncoder(w).Encode(VPNRegisterResponse{
			NodeID: 7, State: "pending", EffectiveTransport: "warp",
			MeshIPv4: "10.99.0.11", MTU: 1280, TokenTTLSeconds: 600,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "ag_test", "test", false)
	resp, err := c.VPNRegister(context.Background(), VPNRegisterRequest{
		Name: "HK-01", Endpoint: "hk1.example.com:443", Pubkey: "pk", Warp: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.NodeID != 7 || resp.EffectiveTransport != "warp" || resp.MeshIPv4 != "10.99.0.11" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestVPNHeartbeat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/vpn/heartbeat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var in VPNHeartbeatRequest
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Transport != "masque" || in.RTTms != 12 {
			t.Errorf("payload = %+v", in)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "ag_test", "test", false)
	if err := c.VPNHeartbeat(context.Background(), VPNHeartbeatRequest{
		Version: "1.0.0", Transport: "masque", State: "up", RTTms: 12,
	}); err != nil {
		t.Fatal(err)
	}
}
