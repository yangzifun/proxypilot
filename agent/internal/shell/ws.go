// Package shell 提供 Agent 侧的实时终端：出站 WebSocket + PTY。
//
// 协议：
//   - 浏览器输入 → DO → Agent：二进制帧（原样写入 PTY）
//   - Agent 输出 → DO → 浏览器：二进制帧
//   - 控制消息（JSON 文本帧）：{"type":"resize","cols":120,"rows":32} / {"type":"exit"}
//
// 与 Worker 的 /api/shell/agent/:id 握手，头部带 X-Agent-Token。
package shell

import (
	"bufio"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Conn 是最小可用的 WebSocket 客户端（只管收发帧，够终端用）。
type Conn struct {
	c      net.Conn
	br     *bufio.Reader
	mu     sync.Mutex
	closed bool
}

// Dial 建立 WebSocket 连接（ws:// 或 wss://）。
func Dial(rawURL string, headers map[string]string, insecure bool, userAgent string) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("URL 无效：%w", err)
	}
	if u.Scheme != "ws" && u.Scheme != "wss" {
		return nil, fmt.Errorf("仅支持 ws/wss：%s", u.Scheme)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "wss" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var c net.Conn
	if u.Scheme == "wss" {
		c, err = tls.DialWithDialer(dialer, "tcp", host, &tls.Config{
			ServerName:         u.Hostname(),
			InsecureSkipVerify: insecure,
			MinVersion:         tls.VersionTLS12,
		})
	} else {
		c, err = dialer.Dial("tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败：%w", host, err)
	}

	keyBytes := make([]byte, 16)
	if _, err = rand.Read(keyBytes); err != nil {
		_ = c.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := u.RequestURI()
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&b, "Host: %s\r\n", u.Host)
	b.WriteString("Upgrade: websocket\r\n")
	b.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&b, "Sec-WebSocket-Key: %s\r\n", key)
	b.WriteString("Sec-WebSocket-Version: 13\r\n")
	if userAgent != "" {
		fmt.Fprintf(&b, "User-Agent: %s\r\n", userAgent)
	}
	for k, v := range headers {
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	b.WriteString("\r\n")
	if _, err = c.Write([]byte(b.String())); err != nil {
		_ = c.Close()
		return nil, err
	}

	br := bufio.NewReaderSize(c, 16*1024)
	status, err := br.ReadString('\n')
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	if !strings.Contains(status, " 101 ") {
		code := strings.TrimSpace(status)
		_ = c.Close()
		return nil, fmt.Errorf("握手失败：%s", code)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			_ = c.Close()
			return nil, err
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	return &Conn{c: c, br: br}, nil
}

// ReadMessage 读取一条完整消息（处理分片、ping/pong、close）。
func (w *Conn) ReadMessage() (byte, []byte, error) {
	var payload []byte
	var opcode byte
	for {
		b0, err := w.br.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		b1, err := w.br.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		fin := b0&0x80 != 0
		op := b0 & 0x0f
		masked := b1&0x80 != 0
		length := int64(b1 & 0x7f)
		switch length {
		case 126:
			var ext [2]byte
			if _, err = io.ReadFull(w.br, ext[:]); err != nil {
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err = io.ReadFull(w.br, ext[:]); err != nil {
				return 0, nil, err
			}
			length = int64(binary.BigEndian.Uint64(ext[:]))
		}
		if length < 0 || length > 8*1024*1024 {
			return 0, nil, errors.New("帧过大")
		}
		var mask [4]byte
		if masked {
			if _, err = io.ReadFull(w.br, mask[:]); err != nil {
				return 0, nil, err
			}
		}
		data := make([]byte, length)
		if _, err = io.ReadFull(w.br, data); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range data {
				data[i] ^= mask[i%4]
			}
		}
		switch op {
		case opClose:
			return opClose, nil, io.EOF
		case opPing:
			_ = w.WriteMessage(opPong, data)
			continue
		case opPong:
			continue
		case opContinuation:
			if len(payload)+len(data) > 8<<20 {
				return 0, nil, errors.New("消息过大")
			}
			payload = append(payload, data...)
		default:
			opcode = op
			payload = data
		}
		if fin {
			return opcode, payload, nil
		}
	}
}

// WriteMessage 发送一帧（客户端帧必须 mask）。
func (w *Conn) WriteMessage(opcode byte, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("连接已关闭")
	}
	header := make([]byte, 0, 14)
	header = append(header, 0x80|opcode)
	n := len(data)
	switch {
	case n < 126:
		header = append(header, byte(n)|0x80)
	case n <= 65535:
		header = append(header, 126|0x80, byte(n>>8), byte(n))
	default:
		header = append(header, 127|0x80, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)
	payload := make([]byte, n)
	for i := 0; i < n; i++ {
		payload[i] = data[i] ^ mask[i%4]
	}
	_, err := w.c.Write(append(header, payload...))
	return err
}

// Close 关闭底层连接。
func (w *Conn) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	w.mu.Unlock()
	return w.c.Close()
}
