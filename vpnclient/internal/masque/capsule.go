// Package masque 实现 VPN 客户端侧的标准 MASQUE CONNECT-IP 传输（RFC 9484 +
// RFC 9297 HTTP Datagram），用于与 tunnel/node 的 MASQUE 服务端对接。
//
// 说明：本包只实现客户端需要的子集，不引入额外依赖，也不 fork quic-go：
//   - 原始 QUIC 连接由调用方准备（证书策略、TLS 配置由主程序决定）；
//   - 内部实现最小 HTTP/3 客户端（控制流 SETTINGS + CONNECT 请求流 + QPACK 静态表）；
//   - CONNECT-IP 的 capsules（ADDRESS_ASSIGN / ADDRESS_REQUEST / ROUTE_ADVERTISEMENT）
//     按 RFC 9484 编解码；
//   - IP 包通过 HTTP Datagram（Context ID=0）承载，帧格式按 RFC 9297/9484。
package masque

import (
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/quic-go/quic-go/quicvarint"
)

// Capsule 类型（RFC 9484 §4.7 / IANA）。
const (
	CapsuleDatagram           = 0x00
	CapsuleAddressAssign      = 0x01
	CapsuleAddressRequest     = 0x02
	CapsuleRouteAdvertisement = 0x03
)

// 地址请求/分配相关错误。
var (
	ErrShortCapsule = errors.New("masque: capsule 数据不完整")
	ErrBadAddress   = errors.New("masque: 非法 IP 地址")
)

// Capsule 是一条 HTTP Capsule（type + payload）。
type Capsule struct {
	Type  uint64
	Value []byte
}

// ReadCapsule 从流中读取一条 capsule（type/length 均为 QUIC varint）。
func ReadCapsule(r io.Reader) (Capsule, error) {
	typ, err := readVarint(r)
	if err != nil {
		return Capsule{}, fmt.Errorf("masque: 读取 capsule 类型失败: %w", err)
	}
	length, err := readVarint(r)
	if err != nil {
		return Capsule{}, fmt.Errorf("masque: 读取 capsule 长度失败: %w", err)
	}
	if length > maxCapsuleSize {
		return Capsule{}, fmt.Errorf("masque: capsule 过大（%d 字节）", length)
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(r, value); err != nil {
		return Capsule{}, fmt.Errorf("masque: 读取 capsule 内容失败: %w", err)
	}
	return Capsule{Type: typ, Value: value}, nil
}

// AppendCapsule 把 type/length/value 追加到 dst，便于整体写出（可合并到 DATA 帧）。
func AppendCapsule(dst []byte, typ uint64, value []byte) []byte {
	dst = quicvarint.Append(dst, typ)
	dst = quicvarint.Append(dst, uint64(len(value)))
	return append(dst, value...)
}

const maxCapsuleSize = 1 << 20

// AssignedAddress 是 ADDRESS_ASSIGN 中的一项（RFC 9484 §4.7.1）。
type AssignedAddress struct {
	RequestID uint64
	IP        net.IP
	PrefixLen uint8
}

// RequestedAddress 是 ADDRESS_REQUEST 中的一项（RFC 9484 §4.7.2）。
type RequestedAddress struct {
	RequestID uint64
	IP        net.IP
	PrefixLen uint8
}

// Route 是 ROUTE_ADVERTISEMENT 中的一条路由（RFC 9484 §4.7.3）。
type Route struct {
	IP        net.IP // 起始地址
	EndIP     net.IP // 结束地址
	PrefixLen uint8  // 由 start/end 计算，便于日志/安装路由
	Protocol  uint8  // IP 协议号，0 表示全部协议
}

// EncodeAddressAssign 编码 ADDRESS_ASSIGN capsule 的 payload。
func EncodeAddressAssign(addrs []AssignedAddress) ([]byte, error) {
	var out []byte
	for _, a := range addrs {
		ip := a.IP.To4()
		if ip == nil {
			ip = a.IP.To16()
		}
		if ip == nil {
			return nil, ErrBadAddress
		}
		out = quicvarint.Append(out, a.RequestID)
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, 4)
			out = append(out, ip4...)
		} else {
			out = append(out, 6)
			out = append(out, ip.To16()...)
		}
		out = append(out, a.PrefixLen)
	}
	return out, nil
}

// DecodeAddressAssign 解析 ADDRESS_ASSIGN capsule 的 payload。
func DecodeAddressAssign(payload []byte) ([]AssignedAddress, error) {
	out := make([]AssignedAddress, 0, 1)
	for len(payload) > 0 {
		id, n, err := quicvarint.Parse(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: request id", ErrShortCapsule)
		}
		payload = payload[n:]
		if len(payload) < 1 {
			return nil, ErrShortCapsule
		}
		ver := payload[0]
		payload = payload[1:]
		var ipLen int
		switch ver {
		case 4:
			ipLen = 4
		case 6:
			ipLen = 16
		default:
			return nil, fmt.Errorf("masque: ADDRESS_ASSIGN 非法 IP 版本 %d", ver)
		}
		if len(payload) < ipLen+1 {
			return nil, ErrShortCapsule
		}
		ip := make(net.IP, ipLen)
		copy(ip, payload[:ipLen])
		payload = payload[ipLen:]
		prefix := payload[0]
		payload = payload[1:]
		if int(prefix) > ipLen*8 {
			return nil, fmt.Errorf("masque: ADDRESS_ASSIGN 前缀长度非法 %d", prefix)
		}
		out = append(out, AssignedAddress{RequestID: id, IP: ip, PrefixLen: prefix})
	}
	return out, nil
}

// EncodeAddressRequest 编码 ADDRESS_REQUEST capsule 的 payload。
func EncodeAddressRequest(reqs []RequestedAddress) ([]byte, error) {
	var out []byte
	for _, a := range reqs {
		ip := a.IP.To4()
		if ip == nil {
			ip = a.IP.To16()
		}
		if ip == nil {
			return nil, ErrBadAddress
		}
		out = quicvarint.Append(out, a.RequestID)
		if ip4 := ip.To4(); ip4 != nil {
			out = append(out, 4)
			out = append(out, ip4...)
		} else {
			out = append(out, 6)
			out = append(out, ip.To16()...)
		}
		out = append(out, a.PrefixLen)
	}
	return out, nil
}

// DecodeAddressRequest 解析 ADDRESS_REQUEST capsule 的 payload。
func DecodeAddressRequest(payload []byte) ([]RequestedAddress, error) {
	var out []RequestedAddress
	for len(payload) > 0 {
		id, n, err := quicvarint.Parse(payload)
		if err != nil {
			return nil, ErrShortCapsule
		}
		payload = payload[n:]
		if len(payload) < 1 {
			return nil, ErrShortCapsule
		}
		ver := payload[0]
		payload = payload[1:]
		var ipLen int
		switch ver {
		case 4:
			ipLen = 4
		case 6:
			ipLen = 16
		default:
			return nil, fmt.Errorf("masque: ADDRESS_REQUEST 非法 IP 版本 %d", ver)
		}
		if len(payload) < ipLen+1 {
			return nil, ErrShortCapsule
		}
		ip := make(net.IP, ipLen)
		copy(ip, payload[:ipLen])
		payload = payload[ipLen:]
		prefix := payload[0]
		payload = payload[1:]
		out = append(out, RequestedAddress{RequestID: id, IP: ip, PrefixLen: prefix})
	}
	return out, nil
}

// DecodeRouteAdvertisement 解析 ROUTE_ADVERTISEMENT payload：
// 每条路由 = IP Version(1) + Start IP(4/16) + End IP(4/16) + IP Protocol(1)。
func DecodeRouteAdvertisement(payload []byte) ([]Route, error) {
	var out []Route
	for len(payload) > 0 {
		ver := payload[0]
		payload = payload[1:]
		var ipLen int
		switch ver {
		case 4:
			ipLen = 4
		case 6:
			ipLen = 16
		default:
			return nil, fmt.Errorf("masque: ROUTE_ADVERTISEMENT 非法 IP 版本 %d", ver)
		}
		if len(payload) < ipLen*2+1 {
			return nil, ErrShortCapsule
		}
		start := make(net.IP, ipLen)
		copy(start, payload[:ipLen])
		payload = payload[ipLen:]
		end := make(net.IP, ipLen)
		copy(end, payload[:ipLen])
		payload = payload[ipLen:]
		proto := payload[0]
		payload = payload[1:]
		prefix := commonPrefixLen(start, end)
		out = append(out, Route{IP: start, EndIP: end, PrefixLen: prefix, Protocol: proto})
	}
	return out, nil
}

// commonPrefixLen 计算 start/end 的公共前缀长度（用于日志/展示）。
func commonPrefixLen(a, b net.IP) uint8 {
	a4, b4 := a.To4(), b.To4()
	if a4 != nil && b4 != nil {
		n := 0
		for i := 0; i < 4; i++ {
			if a4[i] != b4[i] {
				break
			}
			n += 8
		}
		return uint8(n)
	}
	a16, b16 := a.To16(), b.To16()
	if a16 == nil || b16 == nil {
		return 0
	}
	n := 0
	for i := 0; i < 16; i++ {
		if a16[i] != b16[i] {
			break
		}
		n += 8
	}
	return uint8(n)
}

// ParseAddressAssign 从一次收到的 capsule 中解析地址（不存在时返回 nil）。
func ParseAddressAssign(capsule Capsule) ([]AssignedAddress, error) {
	if capsule.Type != CapsuleAddressAssign {
		return nil, nil
	}
	return DecodeAddressAssign(capsule.Value)
}

// ParseRouteAdvertisement 从一次收到的 capsule 中解析路由（不存在时返回 nil）。
func ParseRouteAdvertisement(capsule Capsule) ([]Route, error) {
	if capsule.Type != CapsuleRouteAdvertisement {
		return nil, nil
	}
	return DecodeRouteAdvertisement(capsule.Value)
}

// ParseDatagramPayload 解析 HTTP Datagram payload（Context ID + 数据）：
// 返回 context ID 与数据；context ID 为 0 表示 IP 包。
func ParseDatagramPayload(payload []byte) (uint64, []byte, error) {
	if len(payload) == 0 {
		return 0, nil, ErrShortCapsule
	}
	ctx, n, err := quicvarint.Parse(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("masque: datagram context id 非法: %w", err)
	}
	if n > len(payload) {
		return 0, nil, ErrShortCapsule
	}
	return ctx, payload[n:], nil
}

// AppendDatagramPayload 编码 HTTP Datagram payload（Context ID=0 + IP 包）。
func AppendDatagramPayload(dst, ipPacket []byte) []byte {
	dst = quicvarint.Append(dst, 0) // Context ID = 0
	return append(dst, ipPacket...)
}

// EncodeCapsule 编码一条完整 capsule（便于写测试/构造请求）。
func EncodeCapsule(typ uint64, value []byte) []byte {
	return AppendCapsule(nil, typ, value)
}

// IPProto 取内层 IP 包协议号（用于日志；非 IPv4 返回 0）。
func IPProto(pkt []byte) uint8 {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return 0
	}
	return pkt[9]
}

// readVarint 从 io.Reader 读取一个 QUIC varint（不依赖 io.ByteReader，
// 便于直接作用于 quic.Stream / bytes.Reader）。
func readVarint(r io.Reader) (uint64, error) {
	var first [1]byte
	if _, err := io.ReadFull(r, first[:]); err != nil {
		return 0, err
	}
	b := first[0]
	length := 1 << (b >> 6)
	if length == 1 {
		return uint64(b & 0x3f), nil
	}
	buf := make([]byte, length-1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, err
	}
	v := uint64(b & 0x3f)
	for _, c := range buf {
		v = v<<8 | uint64(c)
	}
	return v, nil
}
