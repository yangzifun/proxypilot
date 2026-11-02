package stats

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"golang.org/x/net/http2"

	"proxypilot-agent/internal/client"
)

// queryStatsPath v2ray stats 服务的 gRPC 方法（与 sing-box 的 v2ray_api 兼容）。
const queryStatsPath = "/v2ray.core.app.stats.command.StatsService/QueryStats"

// v2rayUserStats 读 user>>> 计数器，聚合成「每用户累计上下行」。
func v2rayUserStats(ctx context.Context, addr string) ([]client.UserTraffic, error) {
	stats, err := grpcQueryStats(ctx, addr, "user>>>")
	if err != nil {
		return nil, err
	}
	users := map[string]*client.UserTraffic{}
	order := make([]string, 0, len(stats))
	for name, value := range stats {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		u := users[parts[1]]
		if u == nil {
			u = &client.UserTraffic{Name: parts[1]}
			users[parts[1]] = u
			order = append(order, parts[1])
		}
		switch parts[3] {
		case "uplink":
			u.Up = value
		case "downlink":
			u.Down = value
		}
	}
	out := make([]client.UserTraffic, 0, len(order))
	for _, n := range order {
		out = append(out, *users[n])
	}
	return out, nil
}

// grpcQueryStats 走 h2c 发一个 unary gRPC 请求（不引入 grpc-go，Agent 保持轻量）。
func grpcQueryStats(ctx context.Context, addr, pattern string) (map[string]int64, error) {
	msg := encodeQueryStats(pattern)
	frame := make([]byte, 5+len(msg))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(msg)))
	copy(frame[5:], msg)

	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+queryStatsPath, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("User-Agent", "proxypilot-agent")
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if len(raw) < 5 {
		return nil, fmt.Errorf("gRPC 响应为空")
	}
	size := int(binary.BigEndian.Uint32(raw[1:5]))
	if len(raw) < 5+size {
		return nil, fmt.Errorf("gRPC 响应被截断")
	}
	return decodeStatsResponse(raw[5 : 5+size])
}

// ---------------- protobuf 手写编解码（报文很小，不值得引入 protobuf 运行时） ----------------

// encodeQueryStats 编码 QueryStatsRequest{pattern, reset=false}。
func encodeQueryStats(pattern string) []byte {
	b := make([]byte, 0, len(pattern)+8)
	b = append(b, 0x0a) // field 1, wire type 2（string）
	b = appendUvarint(b, uint64(len(pattern)))
	b = append(b, pattern...)
	return b
}

// decodeStatsResponse 解码 QueryStatsResponse{repeated Stat stat = 1}。
func decodeStatsResponse(msg []byte) (map[string]int64, error) {
	out := map[string]int64{}
	i := 0
	for i < len(msg) {
		field, wire, next, ok := readKey(msg, i)
		if !ok {
			return nil, fmt.Errorf("protobuf 解析失败")
		}
		i = next
		if field == 1 && wire == 2 {
			sub, after, ok := readBytes(msg, i)
			if !ok {
				return nil, fmt.Errorf("protobuf 字段截断")
			}
			i = after
			name, value, err := decodeStat(sub)
			if err != nil {
				return nil, err
			}
			if name != "" {
				out[name] = value
			}
			continue
		}
		skipped, ok := skipField(msg, i, wire)
		if !ok {
			return nil, fmt.Errorf("protobuf 字段类型不支持（wire=%d）", wire)
		}
		i = skipped
	}
	return out, nil
}

// decodeStat 解码 Stat{name=1, value=2}。
func decodeStat(b []byte) (string, int64, error) {
	var (
		name  string
		value int64
	)
	i := 0
	for i < len(b) {
		field, wire, next, ok := readKey(b, i)
		if !ok {
			return "", 0, fmt.Errorf("protobuf Stat 解析失败")
		}
		i = next
		switch {
		case field == 1 && wire == 2:
			raw, after, ok := readBytes(b, i)
			if !ok {
				return "", 0, fmt.Errorf("protobuf Stat 字段截断")
			}
			name = string(raw)
			i = after
		case field == 2 && wire == 0:
			v, after, ok := readUvarint(b, i)
			if !ok {
				return "", 0, fmt.Errorf("protobuf Stat 数值截断")
			}
			value = int64(v)
			i = after
		default:
			skipped, ok := skipField(b, i, wire)
			if !ok {
				return "", 0, fmt.Errorf("protobuf Stat 字段类型不支持（wire=%d）", wire)
			}
			i = skipped
		}
	}
	return name, value, nil
}

func appendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readUvarint(b []byte, i int) (uint64, int, bool) {
	var (
		v     uint64
		shift uint
	)
	for {
		if i >= len(b) || shift > 63 {
			return 0, i, false
		}
		c := b[i]
		i++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, i, true
		}
		shift += 7
	}
}

// readKey 读 tag，返回 field / wire type / 下一个下标。
func readKey(b []byte, i int) (int, int, int, bool) {
	v, next, ok := readUvarint(b, i)
	if !ok {
		return 0, 0, i, false
	}
	return int(v >> 3), int(v & 0x7), next, true
}

// readBytes 读 length-delimited 字段。
func readBytes(b []byte, i int) ([]byte, int, bool) {
	n, next, ok := readUvarint(b, i)
	if !ok || n > uint64(len(b)-next) {
		return nil, i, false
	}
	end := next + int(n)
	return b[next:end], end, true
}

// skipField 跳过不关心的字段（兼容 0/1/2/5 四种 wire type）。
func skipField(b []byte, i, wire int) (int, bool) {
	switch wire {
	case 0:
		_, next, ok := readUvarint(b, i)
		return next, ok
	case 1:
		if i+8 > len(b) {
			return i, false
		}
		return i + 8, true
	case 2:
		_, next, ok := readBytes(b, i)
		return next, ok
	case 5:
		if i+4 > len(b) {
			return i, false
		}
		return i + 4, true
	default:
		return i, false
	}
}
