package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// 证书链探测：只做 QUIC/TLS 握手，不发鉴权请求，因此不需要有效令牌。
//
//	PP_PROBE_API=https://proxypilot.yangzifun.org \
//	PP_PROBE_NODES=taipei-node.yzfn.top:443,hk2-node.yzfn.top:443,lax1-node.yzfn.top:443 \
//	  go test ./vpnclient -run TestProbeNodeTLS -v
//
// 用来确认「ACME 节点 + internal_ca 节点」在同一客户端里都能验过证书。
func TestProbeNodeTLS(t *testing.T) {
	api := strings.TrimSpace(os.Getenv("PP_PROBE_API"))
	list := strings.TrimSpace(os.Getenv("PP_PROBE_NODES"))
	if api == "" || list == "" {
		t.Skip("未设置 PP_PROBE_API / PP_PROBE_NODES")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pemBytes, err := fetchPlatformCA(ctx, api)
	if err != nil {
		t.Fatalf("拉取平台 CA 失败：%v", err)
	}
	caPEM = pemBytes
	t.Logf("已加载平台 CA（%d 字节）", len(pemBytes))
	defer func() { caPEM = nil }()

	pool, err := rootPoolWithPlatformCA()
	if err != nil {
		t.Fatalf("构造信任池失败：%v", err)
	}

	// PP_PROBE_OLD=1 复现修复前的行为：RootCAs 只放平台 CA（丢掉系统根），
	// 用来证明「连过 internal_ca 节点后再连 ACME 节点会报 unknown authority」。
	if os.Getenv("PP_PROBE_OLD") == "1" {
		old := x509.NewCertPool()
		if !old.AppendCertsFromPEM(caPEM) {
			t.Fatal("CA 无效")
		}
		pool = old
		t.Log("使用修复前的信任池（只含平台 CA）")
	}

	for _, ep := range strings.Split(list, ",") {
		ep = strings.TrimSpace(ep)
		if ep == "" {
			continue
		}
		host := strings.Split(ep, ":")[0]
		dctx, dcancel := context.WithTimeout(ctx, 12*time.Second)
		_, derr := quic.DialAddr(dctx, ep, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS13,
			NextProtos: []string{"h3"},
			RootCAs:    pool,
		}, &quic.Config{HandshakeIdleTimeout: 10 * time.Second})
		dcancel()
		if derr != nil {
			t.Errorf("%-32s 握手失败：%v", host, derr)
			continue
		}
		t.Logf("%-32s 证书校验通过", host)
	}
	fmt.Println("done")
}