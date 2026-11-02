package api

import (
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestParseAgentVersion(t *testing.T) {
	cases := map[string][]int{
		"1.6.0":    {1, 6, 0},
		"v1.5.4":   {1, 5, 4},
		"1.5.5-rc": {1, 5, 5},
		"2":        {2},
		"":         nil,
		"v":        nil,
		"abc":      nil,
	}
	for in, want := range cases {
		got := parseAgentVersion(in)
		if len(got) != len(want) {
			t.Fatalf("parse(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("parse(%q) = %v, want %v", in, got, want)
			}
		}
	}
}

func TestAgentVersionCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.6.0", "1.5.5", true},
		{"1.5.5", "1.5.5", true},
		{"1.5.4", "1.5.5", false},
		{"1.5", "1.5.0", true},
		{"2.0.0", "10.0.0", false},
		{"", "1.0.0", false},
	}
	for _, c := range cases {
		if got := agentVersionAtLeast(c.a, c.b); got != c.want {
			t.Errorf("atLeast(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}

	if !agentVersionNewer("1.6.0", "1.5.5") {
		t.Fatal("1.6.0 应比 1.5.5 新")
	}
	if agentVersionNewer("1.5.5", "1.5.5") {
		t.Fatal("同版本不应判为更新")
	}
	if !agentVersionNewer("1.6.0", "") {
		t.Fatal("空版本应判为可更新")
	}
	if agentVersionNewer("", "1.0.0") {
		t.Fatal("未知最新版本不应判为可更新")
	}
}

func TestAgentBinaryBaseURL(t *testing.T) {
	h := &handlers{}
	if got := h.agentBinaryBaseURL(); got != "" {
		t.Fatalf("未配置时基址应为空（走请求 origin），得到 %q", got)
	}
	h2 := &handlers{cfg: config.Config{PublicBaseURL: "https://pp.example.com"}}
	if got := h2.agentBinaryBaseURL(); got != "https://pp.example.com/agent-dist" {
		t.Fatalf("PublicBaseURL 基址 = %q", got)
	}
	h3 := &handlers{cfg: config.Config{AgentBinaryBaseURL: "https://cdn.example.com/agent/"}}
	if got := h3.agentBinaryBaseURL(); got != "https://cdn.example.com/agent" {
		t.Fatalf("外部基址 = %q", got)
	}
}
