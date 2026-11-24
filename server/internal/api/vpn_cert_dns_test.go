package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateCFDNSName(t *testing.T) {
	cases := []struct {
		name string
		zone string
		want string
		ok   bool
	}{
		{"lax2-node.yzfn.top", "yzfn.top", "lax2-node.yzfn.top", true},
		{"LAX2-Node.yzfn.top.", "yzfn.top", "lax2-node.yzfn.top", true},
		{"hk1-node.yzfn.top", "yzfn.top", "hk1-node.yzfn.top", true},
		{"yzfn.top", "yzfn.top", "", false},
		{"a.b.yzfn.top", "yzfn.top", "", false},
		{"lax2-node.example.com", "yzfn.top", "", false},
		{"*.yzfn.top", "yzfn.top", "", false},
		{"-bad.yzfn.top", "yzfn.top", "", false},
		{"bad-.yzfn.top", "yzfn.top", "", false},
		{"", "yzfn.top", "", false},
	}
	for _, tc := range cases {
		got, err := validateCFDNSName(tc.name, tc.zone)
		if tc.ok && (err != nil || got != tc.want) {
			t.Fatalf("%q => %q,%v, want %q", tc.name, got, err, tc.want)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q 应被拒绝", tc.name)
		}
	}
}

func TestValidateCFDNSIPv4(t *testing.T) {
	for _, ip := range []string{"104.223.73.43", "23.141.204.54", "203.0.113.10"} {
		if got, err := validateCFDNSIPv4(ip); err != nil || got != ip {
			t.Fatalf("%s => %q,%v", ip, got, err)
		}
	}
	for _, ip := range []string{"", "not-ip", "127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.1", "0.0.0.0", "2606:4700::1"} {
		if _, err := validateCFDNSIPv4(ip); err == nil {
			t.Fatalf("%q 应被拒绝", ip)
		}
	}
}

// cfDNSTestServer 模拟 Cloudflare zone/list/create/update/delete 接口。
func cfDNSTestServer(t *testing.T, records []cfDNSARecord, onMutation func(method, path string, body map[string]any)) *httptest.Server {
	t.Helper()
	zoneID := "zone-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{map[string]any{"id": zoneID, "name": "yzfn.top"}}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": records})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/dns_records"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if onMutation != nil {
				onMutation(r.Method, r.URL.Path, body)
			}
			rec := cfDNSARecord{ID: "new-1", Type: "A", Name: mapStr(body, "name", ""), Content: mapStr(body, "content", ""), Proxied: false, TTL: 120}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rec})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/dns_records/"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if onMutation != nil {
				onMutation(r.Method, r.URL.Path, body)
			}
			rec := cfDNSARecord{ID: strings.TrimPrefix(r.URL.Path, "/zones/"+zoneID+"/dns_records/"), Type: "A", Name: mapStr(body, "name", ""), Content: mapStr(body, "content", ""), Proxied: false, TTL: 120}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rec})
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/dns_records/"):
			if onMutation != nil {
				onMutation(r.Method, r.URL.Path, nil)
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": "removed"}})
		default:
			t.Fatalf("unexpected Cloudflare request %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestCFDNS(baseURL string) *cfDNS {
	return &cfDNS{token: "test-token", zone: "yzfn.top", baseURL: baseURL}
}

func TestCFDNSUpsertCreatesDNSOnlyA(t *testing.T) {
	var mutations []string
	srv := cfDNSTestServer(t, nil, func(method, path string, body map[string]any) {
		mutations = append(mutations, method+" "+path)
		if mapStr(body, "type", "") != "A" || mapStr(body, "name", "") != "lax2-node.yzfn.top" || mapStr(body, "content", "") != "104.223.73.43" || body["proxied"] != false || body["ttl"] != float64(120) {
			t.Fatalf("POST body=%#v", body)
		}
	})
	action, rec, removed, err := newTestCFDNS(srv.URL).upsertARecord(context.Background(), "lax2-node.yzfn.top", "104.223.73.43")
	if err != nil {
		t.Fatal(err)
	}
	if action != "created" || rec.ID == "" || rec.Content != "104.223.73.43" || len(removed) != 0 {
		t.Fatalf("action=%s rec=%+v removed=%v", action, rec, removed)
	}
	if len(mutations) != 1 || mutations[0] != "POST /zones/zone-1/dns_records" {
		t.Fatalf("mutations=%v", mutations)
	}
}

func TestCFDNSUpsertUnchanged(t *testing.T) {
	srv := cfDNSTestServer(t, []cfDNSARecord{{ID: "a1", Type: "A", Name: "lax2-node.yzfn.top", Content: "104.223.73.43", Proxied: false, TTL: 120}}, func(method, path string, body map[string]any) {
		t.Fatalf("unchanged 不应写 Cloudflare：%s %s", method, path)
	})
	action, rec, _, err := newTestCFDNS(srv.URL).upsertARecord(context.Background(), "lax2-node.yzfn.top", "104.223.73.43")
	if err != nil || action != "unchanged" || rec.ID != "a1" {
		t.Fatalf("action=%s rec=%+v err=%v", action, rec, err)
	}
}

func TestCFDNSUpsertUpdatesAndDedupes(t *testing.T) {
	records := []cfDNSARecord{
		{ID: "a-old", Type: "A", Name: "lax2-node.yzfn.top", Content: "198.51.100.1", Proxied: true},
		{ID: "a-dup", Type: "A", Name: "lax2-node.yzfn.top", Content: "198.51.100.2", Proxied: false},
	}
	var methods []string
	srv := cfDNSTestServer(t, records, func(method, path string, body map[string]any) {
		methods = append(methods, method+" "+path)
		if method == http.MethodPut {
			if body["proxied"] != false || mapStr(body, "content", "") != "104.223.73.43" {
				t.Fatalf("PUT body=%#v", body)
			}
		}
	})
	action, rec, removed, err := newTestCFDNS(srv.URL).upsertARecord(context.Background(), "lax2-node.yzfn.top", "104.223.73.43")
	if err != nil {
		t.Fatal(err)
	}
	if action != "updated" || rec.Content != "104.223.73.43" || len(removed) != 1 || removed[0] != "a-dup" {
		t.Fatalf("action=%s rec=%+v removed=%v methods=%v", action, rec, removed, methods)
	}
	if len(methods) != 2 || methods[0] != "PUT /zones/zone-1/dns_records/a-old" || methods[1] != "DELETE /zones/zone-1/dns_records/a-dup" {
		t.Fatalf("methods=%v", methods)
	}
}

func TestCFDNSUpsertDedupesSameTarget(t *testing.T) {
	records := []cfDNSARecord{
		{ID: "a1", Type: "A", Name: "lax2-node.yzfn.top", Content: "104.223.73.43", Proxied: false},
		{ID: "a2", Type: "A", Name: "lax2-node.yzfn.top", Content: "104.223.73.43", Proxied: false},
	}
	srv := cfDNSTestServer(t, records, func(method, path string, body map[string]any) {
		if method != http.MethodDelete || !strings.HasSuffix(path, "/a2") {
			t.Fatalf("unexpected mutation %s %s", method, path)
		}
	})
	action, rec, removed, err := newTestCFDNS(srv.URL).upsertARecord(context.Background(), "lax2-node.yzfn.top", "104.223.73.43")
	if err != nil || action != "deduped" || rec.ID != "a1" || len(removed) != 1 || removed[0] != "a2" {
		t.Fatalf("action=%s rec=%+v removed=%v err=%v", action, rec, removed, err)
	}
}

func TestCFDNSUpsertCNAMEConflict(t *testing.T) {
	records := []cfDNSARecord{{ID: "cname-1", Type: "CNAME", Name: "lax2-node.yzfn.top", Content: "target.example.com"}}
	srv := cfDNSTestServer(t, records, func(method, path string, body map[string]any) {
		t.Fatalf("CNAME 冲突不应写 Cloudflare：%s %s", method, path)
	})
	_, _, _, err := newTestCFDNS(srv.URL).upsertARecord(context.Background(), "lax2-node.yzfn.top", "104.223.73.43")
	var conflict *cfDNSAConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}
