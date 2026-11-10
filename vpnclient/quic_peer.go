package main

import (
	"context"

	"github.com/quic-go/quic-go"
)

// quicDatagramPeer 把 *quic.Conn 适配成 datagramPeer。
//
// 单独抽一层是为了让 dualPacketConn 可测试（测试里用替身），
// 同时把 quic-go 的错误码类型挡在接口外面。
type quicDatagramPeer struct {
	conn *quic.Conn
}

func (p quicDatagramPeer) SendDatagram(b []byte) error { return p.conn.SendDatagram(b) }

func (p quicDatagramPeer) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	return p.conn.ReceiveDatagram(ctx)
}

func (p quicDatagramPeer) CloseWithError(code uint64, text string) error {
	return p.conn.CloseWithError(quic.ApplicationErrorCode(code), text)
}

func (p quicDatagramPeer) Done() <-chan struct{} { return p.conn.Context().Done() }

// supportsDatagrams 判断对端是否协商了 QUIC DATAGRAM。
func supportsDatagrams(c *quic.Conn) bool {
	if c == nil {
		return false
	}
	return c.ConnectionState().SupportsDatagrams
}
