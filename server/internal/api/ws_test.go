package api

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

func TestWSAcceptKey(t *testing.T) {
	// RFC 6455 示例
	got := wsAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	if got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept = %q", got)
	}
}

func TestWSFrameRoundTrip(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	c := &wsConn{conn: server, br: bufio.NewReader(server)}
	_ = server.SetDeadline(time.Now().Add(3 * time.Second))
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	go func() {
		// 客户端帧必须掩码：text "hi"
		payload := []byte("hi")
		mask := [4]byte{1, 2, 3, 4}
		frame := []byte{0x81, 0x80 | byte(len(payload))}
		frame = append(frame, mask[:]...)
		for i, b := range payload {
			frame = append(frame, b^mask[i%4])
		}
		_, _ = client.Write(frame)
	}()

	op, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != 0x1 || string(data) != "hi" {
		t.Fatalf("op=%d data=%q", op, data)
	}

	go func() {
		_ = c.WriteText("ok")
	}()
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(client, hdr); err != nil {
		t.Fatalf("读响应头: %v", err)
	}
	if hdr[0] != 0x81 || hdr[1]&0x80 != 0 {
		t.Fatalf("响应头异常: %v", hdr)
	}
	n := int(hdr[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		_, _ = io.ReadFull(client, b[:])
		n = int(binary.BigEndian.Uint16(b[:]))
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(client, body); err != nil {
		t.Fatalf("读响应体: %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
}
