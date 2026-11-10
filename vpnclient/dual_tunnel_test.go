package main

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeStream 是 packetConn 的测试替身。
type fakeStream struct {
	mu      sync.Mutex
	sent    [][]byte
	recv    chan []byte
	closed  bool
	recvErr error
}

func newFakeStream() *fakeStream {
	return &fakeStream{recv: make(chan []byte, 16)}
}

func (f *fakeStream) SendPacket(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(p))
	copy(cp, p)
	f.sent = append(f.sent, cp)
	return nil
}

func (f *fakeStream) RecvPacket() ([]byte, error) {
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	p, ok := <-f.recv
	if !ok {
		return nil, errors.New("stream closed")
	}
	return p, nil
}

func (f *fakeStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.recv)
	}
	return nil
}

func (f *fakeStream) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeStream) lastSent() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		return nil
	}
	return f.sent[len(f.sent)-1]
}

// fakeDatagram 是 datagramPeer 的测试替身。
type fakeDatagram struct {
	mu     sync.Mutex
	sent   [][]byte
	recv   chan []byte
	failue bool
}

func newFakeDatagram() *fakeDatagram {
	return &fakeDatagram{recv: make(chan []byte, 16)}
}

func (f *fakeDatagram) SendDatagram(b []byte) error {
	if f.failue {
		return errors.New("datagram 不支持")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	f.sent = append(f.sent, cp)
	return nil
}

func (f *fakeDatagram) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	select {
	case p := <-f.recv:
		return p, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeDatagram) CloseWithError(uint64, string) error { return nil }

func (f *fakeDatagram) Done() <-chan struct{} { return make(chan struct{}) }

func (f *fakeDatagram) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// 造一个内层 IPv4 包，指定协议号。
func makeIPPacket(proto byte, size int) []byte {
	if size < 20 {
		size = 20
	}
	p := make([]byte, size)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(size))
	p[9] = proto
	return p
}

// UDP 走 DATAGRAM，TCP/ICMP 走可靠流。
func TestDualSendRouting(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	c := newDualPacketConn(st, dg, 1400, 0)

	if err := c.SendPacket(makeIPPacket(udpProto, 60)); err != nil {
		t.Fatalf("UDP 发送失败：%v", err)
	}
	if dg.sentCount() != 1 || st.sentCount() != 0 {
		t.Fatalf("UDP 应走 DATAGRAM：dg=%d stream=%d", dg.sentCount(), st.sentCount())
	}

	if err := c.SendPacket(makeIPPacket(tcpProto, 60)); err != nil {
		t.Fatalf("TCP 发送失败：%v", err)
	}
	if st.sentCount() != 1 {
		t.Fatalf("TCP 应走可靠流：stream=%d", st.sentCount())
	}

	if err := c.SendPacket(makeIPPacket(icmpProto, 60)); err != nil {
		t.Fatalf("ICMP 发送失败：%v", err)
	}
	if st.sentCount() != 2 {
		t.Fatalf("ICMP 应走可靠流：stream=%d", st.sentCount())
	}
}

// DATAGRAM 发送失败时，UDP 必须回退到可靠流，不能丢。
func TestDualSendFallbackOnDatagramError(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	dg.failue = true
	c := newDualPacketConn(st, dg, 1400, 0)

	if err := c.SendPacket(makeIPPacket(udpProto, 60)); err != nil {
		t.Fatalf("回退后不应报错：%v", err)
	}
	if st.sentCount() != 1 {
		t.Fatalf("DATAGRAM 失败后应回退到可靠流：stream=%d", st.sentCount())
	}
}

// 超过 DATAGRAM 上限的 UDP 包必须走可靠流。
func TestDualSendOversizeToStream(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	c := newDualPacketConn(st, dg, 200, 0)

	if err := c.SendPacket(makeIPPacket(udpProto, 800)); err != nil {
		t.Fatalf("超大包发送失败：%v", err)
	}
	if dg.sentCount() != 0 {
		t.Fatal("超大包不应走 DATAGRAM")
	}
	if st.sentCount() != 1 {
		t.Fatal("超大包应走可靠流")
	}
}

// 下行两条通道的包都要能从 RecvPacket 取到。
func TestDualRecvMergesBothPaths(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	c := newDualPacketConn(st, dg, 1400, 0)

	fromStream := makeIPPacket(tcpProto, 40)
	fromDatagram := makeIPPacket(udpProto, 50)
	st.recv <- fromStream
	dg.recv <- fromDatagram

	got := map[byte]bool{}
	for i := 0; i < 2; i++ {
		select {
		case pkt := <-c.q:
			got[pkt[9]] = true
		case <-time.After(2 * time.Second):
			t.Fatal("下行包未合并进队列")
		}
	}
	if !got[tcpProto] {
		t.Fatal("可靠流的下行包没进队列")
	}
	if !got[udpProto] {
		t.Fatal("DATAGRAM 的下行包没进队列")
	}
}

// 可靠流断开时，RecvPacket 应返回错误而不是永久阻塞。
func TestDualRecvFailsWhenStreamDies(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	c := newDualPacketConn(st, dg, 1400, 0)

	st.mu.Lock()
	st.recvErr = errors.New("流已断")
	st.mu.Unlock()
	// 触发一次接收，让 streamLoop 感知错误
	_ = st.Close()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-c.done:
			return // 正确：done 已关闭
		case <-deadline:
			t.Fatal("可靠流断开后 done 未关闭")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
}