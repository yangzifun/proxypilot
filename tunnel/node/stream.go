package node

import (
	"bufio"
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

// 下行发送批量聚合参数。
//
// 原来每个报文要写两次（长度头 + 载荷）并显式 Flush 一次，
// 小包场景下 QUIC 帧数量与唤醒次数是有效载荷的数倍，pps 上不去；
// 写阻塞时还会反压 tunLoop，让所有会话一起卡住。
// 改成「每会话有界队列 + 聚合写」，tunLoop 只做入队。
const (
	streamQueueDepth = 2048
	// 下行同样从 64KB 降到 8KB：突发是双向都会被路径丢弃的，
	// 下载慢同样要吃这个亏。详见 vpnclient/stream_tunnel.go 的说明。
	streamBatchMax   = 8 * 1024
	streamBatchDelay = 2 * time.Millisecond
)

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
	q := make(chan []byte, streamQueueDepth)
	done := make(chan struct{})
	var closeOnce sync.Once
	var dropped uint64

	// 下行写协程：聚合多帧后一次 Write，显著减少 QUIC 帧数量。
	go func() {
		buf := make([]byte, 0, streamBatchMax)
		appendFrame := func(pkt []byte) {
			var hdr [2]byte
			binary.BigEndian.PutUint16(hdr[:], uint16(len(pkt)))
			buf = append(buf, hdr[:]...)
			buf = append(buf, pkt...)
		}
		flush := func() bool {
			if len(buf) == 0 {
				return true
			}
			wmu.Lock()
			ok := !closed
			if ok {
				if _, err := w.Write(buf); err != nil {
					s.log.Warn("流隧道写回失败", "err", err)
					ok = false
				} else if flusher != nil {
					flusher.Flush()
				}
			}
			wmu.Unlock()
			buf = buf[:0]
			return ok
		}
		for {
			select {
			case <-done:
				flush()
				return
			case pkt := <-q:
				appendFrame(pkt)
				for len(buf) < streamBatchMax {
					select {
					case p2 := <-q:
						appendFrame(p2)
						continue
					default:
					}
					break
				}
				if len(buf) < streamBatchMax/4 {
					timer := time.NewTimer(streamBatchDelay)
				drain:
					for len(buf) < streamBatchMax {
						select {
						case p2 := <-q:
							appendFrame(p2)
						case <-timer.C:
							break drain
						case <-done:
							timer.Stop()
							flush()
							return
						}
					}
					timer.Stop()
				}
				if !flush() {
					return
				}
			}
		}
	}()

	sess := &session{
		kind: kindStream,
		jti: claims.JTI, ip: ip, device: claims.Subject, grantID: claims.GrantID,
		started: time.Now(),
	}
	// send 只做入队，绝不阻塞 tunLoop（否则所有会话一起反压）。
	sess.send = func(pkt []byte) error {
		if len(pkt) == 0 || len(pkt) > maxStreamFrame {
			return fmt.Errorf("报文长度非法：%d", len(pkt))
		}
		if closed {
			return io.ErrClosedPipe
		}
		cp := make([]byte, len(pkt))
		copy(cp, pkt)
		select {
		case q <- cp:
			return nil
		default:
			dropped++
			if dropped%1000 == 1 {
				s.log.Warn("下行发送队列已满，丢弃报文", "ip", ip.String(), "dropped", dropped)
			}
			return nil
		}
	}
	sess.closeFn = func() {
		closeOnce.Do(func() {
			wmu.Lock()
			closed = true
			wmu.Unlock()
			close(done)
		})
	}

	// 双通道（36.3 起）：DATAGRAM 与隧道流**共用这条连接**。
	//
	// 之前 DATAGRAM 走的是 GET / 那条鉴权连接，它在鉴权返回后就没有流量了，
	// cwnd 回落到初始值、连接也可能已被回收，而 UDP 没有 FIN，
	// SendDatagram 在本地排队即成功 —— 实测「客户端发出 343 个、节点只收到
	// 129 个，双方都没有错误计数」。绑到隧道流这条连接后，DATAGRAM 与流
	// 共享同一个 cwnd 与存活状态。
	//
	// 注意：这条会话与流共享连接，绝不能在自己的清理里关闭连接，
	// 否则会把隧道流一起弄断（closeFn 置为空实现）。
	if s.cfg.DatagramEnabled {
		if conn, _ := r.Context().Value(connKey{}).(*quic.Conn); conn != nil {
			dsess := &session{
				jti: claims.JTI, kind: kindDatagram, ip: ip, device: claims.Subject,
				grantID: claims.GrantID, conn: conn, started: time.Now(),
			}
			dsess.send = func(p []byte) error { return dsess.conn.SendDatagram(p) }
			dsess.closeFn = func() {} // 共享连接，不在这里关闭
			if s.register(dsess) {
				s.log.Info("单连接双通道", "ip", ip.String(), "remote", r.RemoteAddr)
				go s.readSession(dsess)
			}
		}
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
	var badPkt, badSrc, frames int
	defer func() {
		s.unregister(sess)
		if sess.closeFn != nil {
			sess.closeFn()
		}
		s.log.Info("流隧道会话结束", "ip", sess.ip.String(), "frames", frames, "rx", sess.rx,
			"tx", sess.tx, "bad_pkt", badPkt, "bad_src", badSrc,
			"duration", time.Since(sess.started).Round(time.Second))
	}()
	lastStats := time.Now()
	var hdr [2]byte

	// 上行读取优化：
	//   1) 用 bufio 包一层，避免每个报文 2 次 syscall
	//   2) 复用载荷缓冲，避免每包一次 make（高 pps 下 GC 压力明显）
	br := bufio.NewReaderSize(body, 64*1024)
	var pktBuf []byte

	for {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
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
		if cap(pktBuf) < n {
			pktBuf = make([]byte, n)
		}
		buf := pktBuf[:n]
		if _, err := io.ReadFull(br, buf); err != nil {
			return
		}
		frames++
		pkt, src, _, perr := StripPadding(buf)
		if perr != nil {
			badPkt++
			// 前 8 个坏包打印前 32 字节，用来判断到底哪里不像 IP 报文
			if badPkt <= 8 {
				n := len(buf)
				if n > 32 {
					n = 32
				}
				s.log.Warn("流隧道坏包", "ip", sess.ip.String(), "frame_len", len(buf),
					"hex", fmt.Sprintf("% x", buf[:n]),
					"ver", buf[0]>>4, "ihl", int(buf[0]&0x0f)*4,
					"total", int(binary.BigEndian.Uint16(buf[2:4])))
			}
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
			s.log.Info("流隧道流量", "ip", sess.ip.String(), "frames", frames, "rx", sess.rx, "tx", sess.tx,
				"bad_pkt", badPkt, "bad_src", badSrc)
			lastStats = time.Now()
		}
	}
}

var _ = quic.ApplicationErrorCode(0)