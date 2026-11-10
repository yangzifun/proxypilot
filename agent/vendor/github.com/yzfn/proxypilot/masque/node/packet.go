package node

import (
	"encoding/binary"
	"errors"
	"net"
)

// ErrBadPacket 表示无法解析的内层报文。
var ErrBadPacket = errors.New("节点：无法解析的内层 IP 报文")

// StripPadding 去掉客户端 DATAGRAM 末尾的随机 Padding。
//
// 客户端发送的是 [IP 报文][随机 Padding]；IP 头里的长度字段决定真实长度。
func StripPadding(b []byte) (pkt []byte, src, dst net.IP, err error) {
	if len(b) < 20 {
		return nil, nil, nil, ErrBadPacket
	}
	switch b[0] >> 4 {
	case 4:
		ihl := int(b[0]&0x0f) * 4
		if ihl < 20 || ihl > len(b) {
			return nil, nil, nil, ErrBadPacket
		}
		total := int(binary.BigEndian.Uint16(b[2:4]))
		if total < ihl || total > len(b) {
			return nil, nil, nil, ErrBadPacket
		}
		src = net.IP(append([]byte(nil), b[12:16]...)).To4()
		dst = net.IP(append([]byte(nil), b[16:20]...)).To4()
		return b[:total], src, dst, nil
	case 6:
		if len(b) < 40 {
			return nil, nil, nil, ErrBadPacket
		}
		total := 40 + int(binary.BigEndian.Uint16(b[4:6]))
		if total > len(b) {
			return nil, nil, nil, ErrBadPacket
		}
		src = net.IP(append([]byte(nil), b[8:24]...))
		dst = net.IP(append([]byte(nil), b[24:40]...))
		return b[:total], src, dst, nil
	default:
		return nil, nil, nil, ErrBadPacket
	}
}
