package api

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 轻量 WebSocket 服务端实现（RFC 6455 子集），避免为 WebSSH/Agent 唤醒引入第三方依赖。
// 支持：握手、文本/二进制帧、分片、ping/pong、close；不支持扩展协商。

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
}

func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

// upgradeWS 完成握手并接管连接；失败时已写 HTTP 错误。
func upgradeWS(w http.ResponseWriter, r *http.Request) (*wsConn, bool) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		writeError(w, http.StatusUpgradeRequired, "需要 WebSocket 连接")
		return nil, false
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "缺少 Sec-WebSocket-Key")
		return nil, false
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前连接不支持 WebSocket")
		return nil, false
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, false
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + wsAcceptKey(key) + "\r\n\r\n"
	if _, err := rw.WriteString(resp); err != nil {
		conn.Close()
		return nil, false
	}
	if err := rw.Flush(); err != nil {
		conn.Close()
		return nil, false
	}
	return &wsConn{conn: conn, br: rw.Reader}, true
}

func (c *wsConn) Close() error { return c.conn.Close() }

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	header := make([]byte, 0, 10)
	header = append(header, 0x80|opcode)
	n := len(payload)
	switch {
	case n <= 125:
		header = append(header, byte(n))
	case n <= 65535:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		header = append(header, 127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		header = append(header, b[:]...)
	}
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}

func (c *wsConn) WriteText(s string) error { return c.writeFrame(0x1, []byte(s)) }
func (c *wsConn) WriteBinary(b []byte) error {
	return c.writeFrame(0x2, b)
}

func (c *wsConn) writeClose(code uint16, reason string) {
	buf := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(buf, code)
	copy(buf[2:], reason)
	_ = c.writeFrame(0x8, buf)
	_ = c.conn.Close()
}

// ReadMessage 读取完整消息（自动合并分片、应答 ping）。
func (c *wsConn) ReadMessage() (byte, []byte, error) {
	var (
		msgOp  byte
		msgBuf []byte
	)
	for {
		var hdr [2]byte
		if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
			return 0, nil, err
		}
		fin := hdr[0]&0x80 != 0
		op := hdr[0] & 0x0f
		masked := hdr[1]&0x80 != 0
		length := int64(hdr[1] & 0x7f)
		switch length {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.br, b[:]); err != nil {
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint64(b[:]))
		}
		if length < 0 || length > 32<<20 {
			return 0, nil, errors.New("帧过大")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.br, mask[:]); err != nil {
				return 0, nil, err
			}
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}

		switch op {
		case 0x8: // close
			_ = c.writeFrame(0x8, payload)
			return 0, nil, io.EOF
		case 0x9: // ping
			_ = c.writeFrame(0xA, payload)
			continue
		case 0xA: // pong
			continue
		case 0x0: // continuation
			if msgOp == 0 {
				return 0, nil, errors.New("意外的续帧")
			}
			msgBuf = append(msgBuf, payload...)
		case 0x1, 0x2:
			msgOp = op
			msgBuf = payload
		default:
			continue
		}
		if fin {
			return msgOp, msgBuf, nil
		}
	}
}

// ping 定时器：保持中间设备不超时（读循环里靠 pong 继续）。
func (c *wsConn) keepAlive(stop <-chan struct{}) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := c.writeFrame(0x9, nil); err != nil {
				return
			}
		}
	}
}
