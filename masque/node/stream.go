package node

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// 可靠流隧道（POST /tunnel）。
//
// 客户端在一条 HTTP/3 双向流上按「2 字节长度 + IP 报文」发送，服务端同样格式回写。
// 相比 QUIC DATAGRAM（不重传），在跨境丢包链路上更可靠。

const maxStreamFrame = 4096

// serveStream 处理 POST /tunnel：鉴权后把该流作为隧道数据面。
func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw := r.Header.Get("Authorization")
	if len(raw) < 8 || raw[:7] != "Bearer " {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims, err := s.verifier.Verify(raw[7:])
	if err != nil {
		s.log.Warn("流隧道令牌校验失败", "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.cfg.NodeID > 0 && claims.NodeID != s.cfg.NodeID {
		http.Error(w, "wrong node", http.StatusForbidden)
		return
	}
	ip := net.ParseIP(claims.IPv4).To4()
	if ip == nil || !inCIDR(ip, s.cfg.ClientCIDR) {
		http.Error(w, "ip not in client cidr", http.StatusForbidden)
		return
	}

	flusher, _ := w.(http.Flusher)
	var wmu sync.Mutex
	closed := false

	sess := &session{
		jti: claims.JTI, ip: ip, device: claims.Subject, grantID: claims.GrantID,
		started: time.Now(),
	}
	sess.send = func(pkt []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		if closed {
			return io.ErrClosedPipe
		}
		if len(pkt) == 0 || len(pkt) > maxStreamFrame {
			return fmt.Errorf("报文长度非法：%d", len(pkt))
		}
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], uint16(len(pkt)))
		if _, err := w.Write(hdr[:]); err != nil {
			return err
		}
		if _, err := w.Write(pkt); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	}
	sess.closeFn = func() {
		wmu.Lock()
		closed = true
		wmu.Unlock()
	}

	if !s.register(sess) {
		http.Error(w, "ip already in use", http.StatusConflict)
		return
	}

	h := w.Header()
	h.Set("X-VPN-Auth", "Success")
	h.Set("X-VPN-Assigned-IPv4", ip.String())
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	s.log.Info("流隧道会话建立", "ip", ip.String(), "device", claims.Subject,
		"node", claims.NodeID, "remote", r.RemoteAddr)

	s.readStream(r.Body, sess)
}

// readStream 从请求体读报文写入 TUN，直到流结束。
func (s *Server) readStream(body io.Reader, sess *session) {
	defer func() {
		s.unregister(sess)
		if sess.closeFn != nil {
			sess.closeFn()
		}
		s.log.Info("流隧道会话结束", "ip", sess.ip.String(), "rx", sess.rx, "tx", sess.tx,
			"duration", time.Since(sess.started).Round(time.Second))
	}()
	var badPkt, badSrc int
	lastStats := time.Now()
	var hdr [2]byte
	for {
		if _, err := io.ReadFull(body, hdr[:]); err != nil {
			if err != io.EOF {
				s.log.Info("流隧道读取结束", "ip", sess.ip.String(), "err", err.Error())
			}
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if n == 0 || n > maxStreamFrame {
			s.log.Warn("流隧道帧长度非法", "ip", sess.ip.String(), "len", n)
			return
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(body, buf); err != nil {
			return
		}
		pkt, src, _, perr := StripPadding(buf)
		if perr != nil {
			badPkt++
			continue
		}
		if !src.Equal(sess.ip) {
			badSrc++
			if badSrc == 1 {
				s.log.Warn("流隧道丢弃源地址不符报文", "sess_ip", sess.ip.String(), "src", src.String())
			}
			continue
		}
		if _, werr := s.tun.Write(pkt); werr != nil {
			s.log.Warn("流隧道写入 TUN 失败", "err", werr, "len", len(pkt))
			continue
		}
		sess.tx++
		if time.Since(lastStats) > 30*time.Second {
			s.log.Info("流隧道流量", "ip", sess.ip.String(), "rx", sess.rx, "tx", sess.tx,
				"bad_pkt", badPkt, "bad_src", badSrc)
			lastStats = time.Now()
		}
	}
}

var _ = quic.ApplicationErrorCode(0)