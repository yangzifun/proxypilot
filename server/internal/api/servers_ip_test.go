package api

import "testing"

func TestPublicDisplayIPv4(t *testing.T) {
	cases := []struct{ v4, conn, want string }{
		{"192.168.0.137", "114.42.197.179", "114.42.197.179"},
		{"10.0.0.2", "2406:da10::1", ""},
		{"23.141.204.54", "2406:da10::1", "23.141.204.54"},
		{"", "114.42.197.179", ""},
	}
	for _, c := range cases {
		if got := publicDisplayIPv4(c.v4, c.conn); got != c.want {
			t.Fatalf("publicDisplayIPv4(%q,%q)=%q，期望 %q", c.v4, c.conn, got, c.want)
		}
	}
}
