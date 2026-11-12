package api

import (
	"context"
	"net"
	"testing"
	"time"
)

// 邮件出站必须钉死 IPv4：IPv4 回环能连，IPv6 回环必须连不上。
//
// 背景：本机有公网 IPv6，但发信域只有 IPv4 的 A、SPF 只有 ip4、反解也只有
// IPv4；如果 SMTP 走 IPv6，收件方会判「无 PTR + SPF softfail」。
func TestMailEgressForcesIPv4(t *testing.T) {
	if mailEgressNetwork != "tcp4" {
		t.Fatalf("mailEgressNetwork = %q，期望 tcp4", mailEgressNetwork)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ln4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听 IPv4 回环失败：%v", err)
	}
	defer ln4.Close()
	conn, err := dialMailSMTP(ctx, ln4.Addr().String())
	if err != nil {
		t.Fatalf("IPv4 拨号失败：%v", err)
	}
	_ = conn.Close()

	ln6, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skip("本机没有 IPv6 回环，跳过 IPv6 断言")
	}
	defer ln6.Close()
	if conn, err := dialMailSMTP(ctx, ln6.Addr().String()); err == nil {
		_ = conn.Close()
		t.Fatalf("dialMailSMTP 连上了 IPv6（%s）：邮件出站没有钉死 tcp4", ln6.Addr())
	}
}
