package node

// MASQUE CONNECT-IP 服务端（RFC 9484 / RFC 9297）。
//
// 与私有隧道（GET / + POST /tunnel）并存：
//   - 私有协议：自研 2 字节长度帧 + QUIC DATAGRAM 双通道；
//   - MASQUE：标准 HTTP Extended CONNECT（:protocol=connect-ip），
//     控制面用 capsule（ADDRESS_ASSIGN/ADDRESS_REQUEST/ROUTE_ADVERTISEMENT），
//     数据面用 HTTP Datagram（Context ID 0 + 原始 IP 报文）。
//
// 两者共用同一套 TUN / NAT / DNS relay，并注册到同一个 sessionSet：
// tunLoop 会按目标 IP 找到会话，下行包通过该会话的 send 发回客户端。
//
// 本文件只做协议适配，不改变私有协议的选路与行为。

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/quic-go/quic-go/quicvarint"
)

const (
	// MASQUE 路径模板（RFC 9484 §3 默认模板）。
	masquePathPrefix = "/.well-known/masque/ip/"

	// CONNECT-IP capsule 类型（RFC 9484 §12.4）。
	capsuleAddressAssign      uint64 = 0x01
	capsuleAddressRequest     uint64 = 0x02
	capsuleRouteAdvertisement uint64 = 0x03

	// Context ID 0 表示 IP 载荷（RFC 9484 §5/§6）。
	masqueContextIDIP uint64 = 0

	// masqueDefaultMTU：RFC 9484 §7.2 要求 IPv6 链路 MTU 至少 1280。
	// 实际值取节点配置的 MTU（私有隧道同值），并通过 X-VPN-MTU 告知客户端。
	masqueDefaultMTU = 1280

	// masqueMaxCapsuleLen 防御异常 capsule 长度（正常地址/路由 capsule 远小于此）。
	masqueMaxCapsuleLen = 64 * 1024
)

var (
	errNotMasquePath    = errors.New("不是 MASQUE CONNECT-IP 路径")
	errMasqueNoDatagram = errors.New("对端未协商 HTTP Datagram")
	errMasqueNoStreamer = errors.New("HTTP 流不支持接管")
)

// masqueAddress 是 RFC 9484 Address/Requested Address 的公共表示。
type masqueAddress struct {
	RequestID uint64
	IP        net.IP
	PrefixLen uint8
}

// masqueRoute 是 RFC 9484 IP Address Range 的表示。
type masqueRoute struct {
	Start    net.IP
	End      net.IP
	Protocol uint8
}

// ---- capsule 编解码（单测覆盖） ----

// marshalAddressList 序列化 Assigned Address / Requested Address 列表。
// 线格式：Request ID(varint) | IP Version(8) | IP Address(32/128) | IP Prefix Length(8)。
func marshalAddressList(addrs []masqueAddress) ([]byte, error) {
	var b []byte
	for _, a := range addrs {
		ip4 := a.IP.To4()
		bits := 128
		var raw []byte
		version := byte(6)
		if ip4 != nil {
			version = 4
			bits = 32
			raw = ip4
		} else {
			raw = a.IP.To16()
		}
		if raw == nil {
			return nil, fmt.Errorf("无效 IP 地址：%v", a.IP)
		}
		prefix := int(a.PrefixLen)
		if prefix < 0 || prefix > bits {
			return nil, fmt.Errorf("无效前缀长度 %d（IPv%d 上限 %d）", prefix, version, bits)
		}
		b = quicvarint.Append(b, a.RequestID)
		b = append(b, version)
		b = append(b, raw...)
		b = append(b, byte(prefix))
	}
	return b, nil
}

// parseAddressList 解析 Assigned Address / Requested Address 列表。
func parseAddressList(b []byte) ([]masqueAddress, error) {
	var out []masqueAddress
	for len(b) > 0 {
		id, n, err := quicvarint.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("解析 Request ID: %w", err)
		}
		b = b[n:]
		if len(b) < 1 {
			return nil, errors.New("缺少 IP Version")
		}
		version := b[0]
		b = b[1:]
		var ipLen, bits int
		switch version {
		case 4:
			ipLen, bits = 4, 32
		case 6:
			ipLen, bits = 16, 128
		default:
			return nil, fmt.Errorf("不支持的 IP Version：%d", version)
		}
		if len(b) < ipLen+1 {
			return nil, fmt.Errorf("IP 地址/前缀长度不完整（version=%d）", version)
		}
		ip := net.IP(append([]byte(nil), b[:ipLen]...))
		if version == 4 {
			ip = ip.To4()
		}
		b = b[ipLen:]
		prefix := int(b[0])
		b = b[1:]
		if prefix > bits {
			return nil, fmt.Errorf("前缀长度 %d 超过 IPv%d 上限 %d", prefix, version, bits)
		}
		// RFC 9484：前缀未覆盖的低位必须为 0。
		if mask := net.CIDRMask(prefix, bits); mask != nil {
			if masked := ip.Mask(mask); masked != nil && !masked.Equal(ip) {
				return nil, fmt.Errorf("IP 地址 %s 在 /%d 前缀下有非零低位", ip, prefix)
			}
		}
		out = append(out, masqueAddress{RequestID: id, IP: ip, PrefixLen: uint8(prefix)})
	}
	return out, nil
}

// marshalRouteAdvertisement 序列化 ROUTE_ADVERTISEMENT。
// 线格式：IP Version(8) | Start IP | End IP | IP Protocol(8)。
func marshalRouteAdvertisement(routes []masqueRoute) ([]byte, error) {
	var b []byte
	for i, rt := range routes {
		start4, end4 := rt.Start.To4(), rt.End.To4()
		version := byte(6)
		var startRaw, endRaw []byte
		if start4 != nil && end4 != nil {
			version = 4
			startRaw, endRaw = start4, end4
		} else {
			startRaw, endRaw = rt.Start.To16(), rt.End.To16()
		}
		if startRaw == nil || endRaw == nil {
			return nil, fmt.Errorf("路由 %d 的 IP 地址无效", i)
		}
		// Start <= End（同字节序比较即可）。
		if bytesCompare(startRaw, endRaw) > 0 {
			return nil, fmt.Errorf("路由 %d 的 Start 大于 End", i)
		}
		b = append(b, version)
		b = append(b, startRaw...)
		b = append(b, endRaw...)
		b = append(b, rt.Protocol)
	}
	return b, nil
}

// parseRouteAdvertisement 解析并校验 ROUTE_ADVERTISEMENT（排序/不重叠按 RFC 校验）。
func parseRouteAdvertisement(b []byte) ([]masqueRoute, error) {
	var out []masqueRoute
	for len(b) > 0 {
		if len(b) < 1 {
			return nil, errors.New("缺少 IP Version")
		}
		version := b[0]
		b = b[1:]
		var ipLen int
		switch version {
		case 4:
			ipLen = 4
		case 6:
			ipLen = 16
		default:
			return nil, fmt.Errorf("路由含不支持的 IP Version：%d", version)
		}
		if len(b) < ipLen*2+1 {
			return nil, fmt.Errorf("路由地址/协议长度不完整（version=%d）", version)
		}
		start := net.IP(append([]byte(nil), b[:ipLen]...))
		b = b[ipLen:]
		end := net.IP(append([]byte(nil), b[:ipLen]...))
		b = b[ipLen:]
		protocol := b[0]
		b = b[1:]
		if version == 4 {
			start, end = start.To4(), end.To4()
		}
		if bytesCompare(start, end) > 0 {
			return nil, errors.New("路由 Start 大于 End")
		}
		out = append(out, masqueRoute{Start: start, End: end, Protocol: protocol})
	}
	// 同一 capsule 内按 (version, protocol, start) 升序且不重叠（RFC 9484 §4.7.3）。
	for i := 1; i < len(out); i++ {
		a, c := out[i-1], out[i]
		a4, c4 := a.Start.To4(), c.Start.To4()
		if a4 == nil && c4 != nil {
			return nil, errors.New("路由未按 IP Version 升序排列")
		}
		if a4 != nil && c4 == nil {
			continue
		}
		if a.Protocol > c.Protocol {
			return nil, errors.New("路由未按 IP Protocol 升序排列")
		}
		if a.Protocol == c.Protocol && bytesCompare(a.End, c.Start) >= 0 {
			return nil, errors.New("同协议的路由范围重叠")
		}
	}
	return out, nil
}

func bytesCompare(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] == b[i] {
			continue
		}
		if a[i] < b[i] {
			return -1
		}
		return 1
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// readCapsule 从流上读取一个 RFC 9297 capsule（type varint + length varint + value）。
func readCapsule(r *bufio.Reader) (uint64, []byte, error) {
	ct, err := quicvarint.Read(r)
	if err != nil {
		return 0, nil, err
	}
	l, err := quicvarint.Read(r)
	if err != nil {
		return 0, nil, err
	}
	if l > masqueMaxCapsuleLen {
		return 0, nil, fmt.Errorf("capsule 长度 %d 超限", l)
	}
	value := make([]byte, l)
	if _, err := io.ReadFull(r, value); err != nil {
		return 0, nil, err
	}
	return ct, value, nil
}

// writeCapsule 写一个 RFC 9297 capsule。type/length 用 QUIC varint。
func writeCapsule(w io.Writer, ct uint64, value []byte) error {
	var hdr []byte
	hdr = quicvarint.Append(hdr, ct)
	hdr = quicvarint.Append(hdr, uint64(len(value)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(value)
	return err
}

// ---- 路径与请求校验（单测覆盖） ----

// parseMasquePath 解析 /.well-known/masque/ip/{target}/{ipproto}/。
//
// target 校验从宽（客户端 PoC 用 "*" 或 "0.0.0.0%2F0"）；ipproto 必须是
// "*" 或 0..255 的十进制数。
func parseMasquePath(escapedPath string) (target, ipproto string, err error) {
	if !strings.HasPrefix(escapedPath, masquePathPrefix) {
		return "", "", errNotMasquePath
	}
	rest := strings.TrimPrefix(escapedPath, masquePathPrefix)
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" {
		return "", "", errNotMasquePath
	}
	parts := strings.Split(rest, "/")
	target = parts[0]
	if target == "" {
		return "", "", errNotMasquePath
	}
	ipproto = "*"
	if len(parts) >= 2 && parts[1] != "" {
		ipproto = parts[1]
	}
	if ipproto == "*" {
		return target, ipproto, nil
	}
	n, perr := strconv.Atoi(ipproto)
	if perr != nil || n < 0 || n > 255 {
		return "", "", fmt.Errorf("ipproto 非法：%q", ipproto)
	}
	return target, ipproto, nil
}

// masqueAuthorize 校验 CONNECT-IP 请求的 JWT/节点/CIDR。
// 返回 (claims, ip, 0, "") 表示通过；否则 status/msg 为要返回给客户端的错误。
func (s *Server) masqueAuthorize(r *http.Request) (*Claims, net.IP, int, string) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(raw, "Bearer ") {
		return nil, nil, http.StatusUnauthorized, "missing bearer token"
	}
	if s.verifier == nil {
		return nil, nil, http.StatusInternalServerError, "verifier unavailable"
	}
	claims, err := s.verifier.Verify(strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")))
	if err != nil {
		return nil, nil, http.StatusUnauthorized, "invalid token"
	}
	if s.cfg.NodeID > 0 && claims.NodeID != s.cfg.NodeID {
		return nil, nil, http.StatusForbidden, "wrong node"
	}
	ip := net.ParseIP(claims.IPv4).To4()
	if ip == nil || !inCIDR(ip, s.cfg.ClientCIDR) {
		return nil, nil, http.StatusForbidden, "ip not in client cidr"
	}
	return claims, ip, 0, ""
}

// masqueMTU 返回对客户端宣告的隧道 MTU（取节点 MTU，最低 IPv6 要求的 1280）。
func (s *Server) masqueMTU() int {
	mtu := s.cfg.MTU
	if mtu <= 0 {
		return masqueDefaultMTU
	}
	if mtu < masqueDefaultMTU {
		return masqueDefaultMTU
	}
	return mtu
}

// masqueDatagramPayload 把 IP 报文封装成 HTTP Datagram 载荷（Context ID 0 + 报文）。
func masqueDatagramPayload(pkt []byte) []byte {
	out := make([]byte, 0, len(pkt)+4)
	out = quicvarint.Append(out, masqueContextIDIP)
	return append(out, pkt...)
}

// parseMasqueDatagram 拆分 HTTP Datagram 载荷，返回 (contextID, 剩余载荷)。
func parseMasqueDatagram(payload []byte) (uint64, []byte, error) {
	id, n, err := quicvarint.Parse(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("解析 Context ID: %w", err)
	}
	return id, payload[n:], nil
}

// masqueIPSource 校验内层报文并返回源地址（仅 IPv4；IPv6 预留）。
func masqueIPSource(pkt []byte) (net.IP, error) {
	if len(pkt) < 20 {
		return nil, ErrBadPacket
	}
	switch pkt[0] >> 4 {
	case 4:
		ihl := int(pkt[0]&0x0f) * 4
		if ihl < 20 || ihl > len(pkt) {
			return nil, ErrBadPacket
		}
		total := int(binary.BigEndian.Uint16(pkt[2:4]))
		if total < ihl || total > len(pkt) {
			return nil, ErrBadPacket
		}
		return net.IP(pkt[12:16]).To4(), nil
	case 6:
		// IPv6 预留：面板当前只分配 IPv4，MH 客户端可后续扩展。
		return nil, errors.New("IPv6 暂未支持")
	default:
		return nil, ErrBadPacket
	}
}

// ---- 会话数据面 ----

// masqueSendAssignment 下发地址与默认路由（RFC 9484 §4.7.1/§4.7.3）。
// reqID 用于回应 ADDRESS_REQUEST；主动下发时为 0。
func (s *Server) masqueSendAssignment(str *http3.Stream, ip net.IP, reqID uint64) error {
	assignPayload, err := marshalAddressList([]masqueAddress{{RequestID: reqID, IP: ip, PrefixLen: 32}})
	if err != nil {
		return err
	}
	if err := writeCapsule(str, capsuleAddressAssign, assignPayload); err != nil {
		return err
	}
	routePayload, err := marshalRouteAdvertisement([]masqueRoute{{
		Start: net.IPv4zero.To4(), End: net.IPv4bcast.To4(), Protocol: 0,
	}})
	if err != nil {
		return err
	}
	return writeCapsule(str, capsuleRouteAdvertisement, routePayload)
}

// serveMASQUE 处理 CONNECT-IP 请求：鉴权 → 200 + Capsule-Protocol → 接管流。
func (s *Server) serveMASQUE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect || (r.Proto != "connect-ip" && r.Header.Get("Connect-Protocol") != "connect-ip") {
		http.Error(w, "expected CONNECT connect-ip", http.StatusBadRequest)
		return
	}
	target, ipproto, perr := parseMasquePath(r.URL.EscapedPath())
	if perr != nil {
		http.Error(w, "invalid masque path", http.StatusNotFound)
		return
	}
	claims, ip, status, msg := s.masqueAuthorize(r)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	conn, _ := r.Context().Value(connKey{}).(*quic.Conn)
	if conn == nil || !conn.ConnectionState().SupportsDatagrams {
		http.Error(w, "HTTP Datagram not negotiated", http.StatusBadRequest)
		return
	}
	streamer, ok := w.(http3.HTTPStreamer)
	if !ok {
		http.Error(w, "stream takeover unsupported", http.StatusInternalServerError)
		return
	}
	if s.tun == nil {
		http.Error(w, "tun unavailable", http.StatusServiceUnavailable)
		return
	}
	mtu := s.masqueMTU()

	w.Header().Set("Capsule-Protocol", "?1")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-VPN-Assigned-IPv4", ip.String())
	w.Header().Set("X-VPN-MTU", strconv.Itoa(mtu))
	w.Header().Set("X-VPN-Masque-Target", target)
	w.Header().Set("X-VPN-Masque-IPProto", ipproto)
	w.WriteHeader(http.StatusOK)
	str := streamer.HTTPStream()

	sess := &session{
		kind: kindMasque, jti: claims.JTI, ip: ip, device: claims.Subject,
		grantID: claims.GrantID, started: time.Now(),
	}
	ctx, cancel := context.WithCancel(r.Context())
	var closeOnce sync.Once
	closeSession := func() {
		closeOnce.Do(func() {
			cancel()
			str.CancelRead(quic.StreamErrorCode(0))
			_ = str.Close()
		})
	}
	sess.closeFn = closeSession
	sess.send = func(pkt []byte) error {
		if len(pkt) == 0 {
			return errors.New("空报文")
		}
		if len(pkt) > mtu {
			sess.sendFail.Add(1)
			return fmt.Errorf("报文 %d 超过 MASQUE MTU %d", len(pkt), mtu)
		}
		payload := masqueDatagramPayload(pkt)
		if s.cfg.MaxDatagram > 0 && len(payload) > s.cfg.MaxDatagram {
			sess.sendFail.Add(1)
			return fmt.Errorf("报文 %d 超过 MaxDatagram %d", len(payload), s.cfg.MaxDatagram)
		}
		if err := str.SendDatagram(payload); err != nil {
			sess.sendFail.Add(1)
			return err
		}
		return nil
	}

	if !s.register(sess) {
		closeSession()
		s.log.Warn("MASQUE 会话注册冲突", "ip", ip.String())
		return
	}
	s.log.Info("MASQUE 会话建立", "ip", ip.String(), "device", claims.Subject,
		"node", claims.NodeID, "remote", r.RemoteAddr, "target", target,
		"ipproto", ipproto, "mtu", mtu)

	// 主动下发地址与默认路由；客户端也可再发 ADDRESS_REQUEST 询问。
	if err := s.masqueSendAssignment(str, ip, 0); err != nil {
		s.log.Warn("MASQUE 下发地址失败", "ip", ip.String(), "err", err)
		s.unregister(sess)
		closeSession()
		return
	}

	var (
		firstErr error
		mu       sync.Mutex
		wg       sync.WaitGroup
	)
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := s.masqueCapsuleLoop(ctx, str, sess, ip)
		mu.Lock()
		if firstErr == nil && err != nil {
			firstErr = err
		}
		mu.Unlock()
		errCh <- err
	}()
	go func() {
		defer wg.Done()
		err := s.masqueDatagramLoop(ctx, str, sess)
		mu.Lock()
		if firstErr == nil && err != nil {
			firstErr = err
		}
		mu.Unlock()
		errCh <- err
	}()

	select {
	case <-ctx.Done():
		firstErr = ctx.Err()
	case <-errCh:
	}

	// 先取消/关流，确保两个循环都能退出，再注销会话。
	closeSession()
	wg.Wait()
	s.unregister(sess)
	mu.Lock()
	err := firstErr
	mu.Unlock()
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		s.log.Info("MASQUE 会话结束", "ip", ip.String(), "device", claims.Subject,
			"duration", time.Since(sess.started).Round(time.Second),
			"rx", sess.rx, "tx", sess.tx, "send_fail", sess.sendFail.Load(),
			"err", err.Error())
	} else {
		s.log.Info("MASQUE 会话结束", "ip", ip.String(), "device", claims.Subject,
			"duration", time.Since(sess.started).Round(time.Second),
			"rx", sess.rx, "tx", sess.tx, "send_fail", sess.sendFail.Load())
	}
}

// masqueCapsuleLoop 读取控制流上的 capsule，响应 ADDRESS_REQUEST。
func (s *Server) masqueCapsuleLoop(ctx context.Context, str *http3.Stream, sess *session, assigned net.IP) error {
	br := bufio.NewReaderSize(str, 4096)
	var badPkt int
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ct, value, err := readCapsule(br)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return err
			}
			return err
		}
		switch ct {
		case capsuleAddressRequest:
			reqs, derr := parseAddressList(value)
			if derr != nil {
				badPkt++
				if badPkt == 1 {
					s.log.Warn("MASQUE ADDRESS_REQUEST 非法", "ip", sess.ip.String(), "err", derr)
				}
				continue
			}
			// 空请求（长度 0）：按"请分配"处理，返回当前地址。
			if len(reqs) == 0 {
				if err := s.masqueSendAssignment(str, assigned, 0); err != nil {
					return err
				}
				continue
			}
			addrs := make([]masqueAddress, 0, len(reqs))
			for _, req := range reqs {
				if req.IP.To4() != nil {
					addrs = append(addrs, masqueAddress{RequestID: req.RequestID, IP: assigned, PrefixLen: 32})
				} else {
					// IPv6 预留：明确拒绝（::/128 表示未分配）。
					addrs = append(addrs, masqueAddress{RequestID: req.RequestID, IP: net.IPv6zero, PrefixLen: 128})
				}
			}
			payload, merr := marshalAddressList(addrs)
			if merr != nil {
				return merr
			}
			if err := writeCapsule(str, capsuleAddressAssign, payload); err != nil {
				return err
			}
		case capsuleAddressAssign:
			// 网络到网络场景下对端可能给我们分配地址；解析校验后忽略（本部署不使用）。
			if _, derr := parseAddressList(value); derr != nil {
				badPkt++
				if badPkt == 1 {
					s.log.Warn("MASQUE 对端 ADDRESS_ASSIGN 非法", "ip", sess.ip.String(), "err", derr)
				}
			}
		case capsuleRouteAdvertisement:
			if _, derr := parseRouteAdvertisement(value); derr != nil {
				badPkt++
				if badPkt == 1 {
					s.log.Warn("MASQUE 对端 ROUTE_ADVERTISEMENT 非法", "ip", sess.ip.String(), "err", derr)
				}
			}
		default:
			// 未知 capsule 按 RFC 9297 忽略。
		}
	}
}

// masqueDatagramLoop 接收 HTTP Datagram（Context ID 0 = IP 报文）并写入 TUN。
func (s *Server) masqueDatagramLoop(ctx context.Context, str *http3.Stream, sess *session) error {
	var badPkt, badSrc, writeFail int
	for {
		payload, err := str.ReceiveDatagram(ctx)
		if err != nil {
			return err
		}
		ctxID, data, perr := parseMasqueDatagram(payload)
		if perr != nil {
			badPkt++
			continue
		}
		if ctxID != masqueContextIDIP {
			// 未知 Context ID：按 RFC 9484 §5 静默丢弃。
			continue
		}
		src, ierr := masqueIPSource(data)
		if ierr != nil {
			badPkt++
			if badPkt == 1 {
				s.log.Warn("MASQUE 丢弃无法解析的 IP 报文", "ip", sess.ip.String(), "len", len(data), "err", ierr)
			}
			continue
		}
		if !src.Equal(sess.ip) {
			badSrc++
			if badSrc == 1 {
				s.log.Warn("MASQUE 丢弃源地址不符报文", "sess_ip", sess.ip.String(), "src", src.String())
			}
			continue
		}
		if _, werr := s.tun.Write(data); werr != nil {
			writeFail++
			if writeFail == 1 {
				s.log.Warn("MASQUE 写入 TUN 失败", "err", werr, "len", len(data), "ip", sess.ip.String())
			}
			continue
		}
		sess.tx++
	}
}
