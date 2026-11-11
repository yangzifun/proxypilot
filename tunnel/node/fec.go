package node

import (
	"encoding/binary"
	"fmt"
	"sync"
)

// DATAGRAM 通道的 XOR 分组前向纠错（36.0）。
//
// 为什么只给 DATAGRAM 做 FEC：
//
//	可靠流由 QUIC 负责重传，应用层 FEC 救不了拥塞窗口，只是浪费带宽；
//	DATAGRAM 根本不重传，丢了就没了，应用层 FEC 是唯一的补救手段。
//
// 分组：每 K 个数据包额外发 1 个校验包，组内丢任意 1 个即可无损还原，
// 代价是 1/K 的带宽开销（K=8 时 12.5%）。
//
// 帧格式（DATAGRAM 载荷）：
//
//	数据帧：[type=0][group 2B][index 1B][原始 IP 报文...]
//	校验帧：[type=1][group 2B][n 1B][len 2B × n][零填充后 XOR 结果...]
//
// 校验帧带上组内每个包的原始长度，还原时据此截断（否则补齐的零字节
// 会让 IP 头里的 total length 与实际长度不一致，对端会判坏包）。

const (
	fecTypeData   = 0
	fecTypeParity = 1

	// fecHeaderLen 是数据帧的头长度。
	fecHeaderLen = 4
	// fecMaxGroup 是组大小的上限，防止配置错得离谱。
	fecMaxGroup = 32
	// fecMaxPending 是解码器同时跟踪的组数上限。
	fecMaxPending = 64
)

// ---------------------------------------------------------------------------
// 编码
// ---------------------------------------------------------------------------

// fecEncoder 按组收集数据包，第 K 个之后产出校验包。
type fecEncoder struct {
	k int

	group  uint16
	cur    *fecGroup
	closed bool
}

type fecGroup struct {
	id     uint16
	expect int // 组内应有多少数据包（K）
	count  int // 已收到的数据包数
	lens   []int
	xor    []byte // 零填充后 XOR
}

func newFECEncoder(k int) *fecEncoder {
	if k <= 1 || k > fecMaxGroup {
		k = 8
	}
	return &fecEncoder{k: k}
}

// Encode 返回该包对应的帧；凑满一组时额外返回一个校验帧。
func (e *fecEncoder) Encode(pkt []byte) (data []byte, parity []byte) {
	if e.closed || e.k <= 1 {
		return pkt, nil
	}
	if e.cur == nil {
		e.cur = &fecGroup{id: e.group, expect: e.k, lens: make([]int, 0, e.k)}
		e.group++
	}
	g := e.cur

	frame := make([]byte, fecHeaderLen+len(pkt))
	frame[0] = fecTypeData
	binary.BigEndian.PutUint16(frame[1:3], g.id)
	frame[3] = byte(g.count)
	copy(frame[fecHeaderLen:], pkt)

	g.lens = append(g.lens, len(pkt))
	g.count++
	if len(g.xor) < len(pkt) {
		g.xor = append(g.xor, make([]byte, len(pkt)-len(g.xor))...)
	}
	for i := range pkt {
		g.xor[i] ^= pkt[i]
	}

	if g.count >= g.expect {
		parity = e.buildParity(g)
		e.cur = nil
	}
	return frame, parity
}

// Flush 把未凑满的组补成校验帧（调用方在空闲时使用）。
func (e *fecEncoder) Flush() []byte {
	if e.cur == nil || e.cur.count == 0 {
		return nil
	}
	g := e.cur
	e.cur = nil
	return e.buildParity(g)
}

func (e *fecEncoder) buildParity(g *fecGroup) []byte {
	// [type][group 2B][n 1B][len 2B × n][xor...]
	n := g.count
	size := fecHeaderLen + n*2 + len(g.xor)
	out := make([]byte, size)
	out[0] = fecTypeParity
	binary.BigEndian.PutUint16(out[1:3], g.id)
	out[3] = byte(n)
	off := fecHeaderLen
	for i := 0; i < n; i++ {
		binary.BigEndian.PutUint16(out[off:off+2], uint16(g.lens[i]))
		off += 2
	}
	copy(out[off:], g.xor)
	return out
}

// ---------------------------------------------------------------------------
// 解码
// ---------------------------------------------------------------------------

// fecDecoder 收集组内数据包，收到校验包时尝试还原 1 个丢失包。
type fecDecoder struct {
	mu      sync.Mutex
	pending map[uint16]*fecRecvGroup
	order   []uint16
	// recovered / dropped 用于诊断
	recovered int
	dropped   int
}

type fecRecvGroup struct {
	payloads [][]byte
	lens     []int
	got      []bool
	expect   int
}

func newFECDecoder() *fecDecoder {
	return &fecDecoder{pending: make(map[uint16]*fecRecvGroup)}
}

// Decode 处理一帧，返回可直接写入 TUN 的完整 IP 报文列表。
func (d *fecDecoder) Decode(frame []byte) ([][]byte, error) {
	if len(frame) < fecHeaderLen {
		return nil, fmt.Errorf("FEC 帧太短：%d", len(frame))
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	switch frame[0] {
	case fecTypeData:
		id := binary.BigEndian.Uint16(frame[1:3])
		idx := int(frame[3])
		payload := frame[fecHeaderLen:]
		if idx < 0 || idx >= fecMaxGroup {
			return nil, fmt.Errorf("FEC 序号非法：%d", idx)
		}
		g := d.groupLocked(id)
		if g.expect <= 0 {
			g.expect = fecMaxGroup // 未知时放宽，靠校验帧纠正
		}
		for len(g.payloads) <= idx {
			g.payloads = append(g.payloads, nil)
			g.lens = append(g.lens, 0)
			g.got = append(g.got, false)
		}
		if g.got[idx] {
			return nil, nil // 重复包，丢弃
		}
		cp := make([]byte, len(payload))
		copy(cp, payload)
		g.payloads[idx] = cp
		g.lens[idx] = len(payload)
		g.got[idx] = true
		return [][]byte{cp}, nil

	case fecTypeParity:
		id := binary.BigEndian.Uint16(frame[1:3])
		n := int(frame[3])
		if n <= 0 || n > fecMaxGroup {
			return nil, fmt.Errorf("FEC 组大小非法：%d", n)
		}
		need := fecHeaderLen + n*2
		if len(frame) < need {
			return nil, fmt.Errorf("FEC 校验帧太短")
		}
		xor := frame[need:]
		g := d.groupLocked(id)

		// 补齐槽位
		for len(g.got) < n {
			g.payloads = append(g.payloads, nil)
			g.lens = append(g.lens, 0)
			g.got = append(g.got, false)
		}
		// 记录原始长度（校验帧是权威来源）
		for i := 0; i < n; i++ {
			if !g.got[i] {
				g.lens[i] = int(binary.BigEndian.Uint16(frame[fecHeaderLen+i*2 : fecHeaderLen+i*2+2]))
			}
		}

		missing := -1
		missingCount := 0
		for i := 0; i < n; i++ {
			if !g.got[i] {
				missing = i
				missingCount++
			}
		}
		delete(d.pending, id)
		d.removeOrder(id)

		if missingCount == 0 {
			return nil, nil // 组已完整
		}
		if missingCount > 1 {
			d.dropped++
			return nil, nil // 丢太多，无法还原
		}
		// XOR 掉其余包，剩下的就是丢失的那个
		recovered := make([]byte, len(xor))
		copy(recovered, xor)
		for i := 0; i < n; i++ {
			if i == missing {
				continue
			}
			p := g.payloads[i]
			for j := range p {
				if j < len(recovered) {
					recovered[j] ^= p[j]
				}
			}
		}
		l := g.lens[missing]
		if l <= 0 || l > len(recovered) {
			d.dropped++
			return nil, nil
		}
		d.recovered++
		return [][]byte{recovered[:l]}, nil

	default:
		return nil, fmt.Errorf("未知 FEC 帧类型：%d", frame[0])
	}
}

func (d *fecDecoder) groupLocked(id uint16) *fecRecvGroup {
	if g, ok := d.pending[id]; ok {
		return g
	}
	// 上限保护：超了就丢最老的组
	for len(d.order) >= fecMaxPending {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.pending, oldest)
	}
	g := &fecRecvGroup{}
	d.pending[id] = g
	d.order = append(d.order, id)
	return g
}

func (d *fecDecoder) removeOrder(id uint16) {
	for i, v := range d.order {
		if v == id {
			d.order = append(d.order[:i], d.order[i+1:]...)
			return
		}
	}
}

// FECStats 返回还原/放弃计数。
func (d *fecDecoder) FECStats() (recovered, dropped int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.recovered, d.dropped
}