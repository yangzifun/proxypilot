package stats

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/net/http2"
)

func TestReadExperimental(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sb.json")
	body := `{"experimental":{"clash_api":{"external_controller":"127.0.0.1:13790","secret":"s3"},"v2ray_api":{"listen":"127.0.0.1:13791"}}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	exp, err := ReadExperimental(path)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if exp.ClashAPI.ExternalController != "127.0.0.1:13790" || exp.ClashAPI.Secret != "s3" {
		t.Fatalf("clash = %+v", exp.ClashAPI)
	}
	if exp.V2RayAPI.Listen != "127.0.0.1:13791" {
		t.Fatalf("v2ray = %+v", exp.V2RayAPI)
	}
}

func TestReadExperimentalMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sb.json")
	if err := os.WriteFile(path, []byte(`{"inbounds":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadExperimental(path); err == nil {
		t.Fatal("缺少 experimental 应报错")
	}
	if _, err := ReadExperimental(filepath.Join(dir, "nope.json")); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

// startFakeStatsServer 起一个最小 h2c gRPC 服务，返回 QueryStats 计数。
func startFakeStatsServer(t *testing.T, stats []string) (addr string, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http2.Server{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.ServeConn(conn, &http2.ServeConnOpts{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != queryStatsPath {
						http.NotFound(w, r)
						return
					}
					_, _ = io.Copy(io.Discard, r.Body)
					w.Header().Set("Content-Type", "application/grpc")
					w.Header().Set("Trailer", "Grpc-Status")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write(grpcFrame(stats))
					w.Header().Set("Grpc-Status", "0")
				}),
			})
		}
	}()
	return ln.Addr().String(), func() { _ = ln.Close() }
}

// grpcFrame 把若干 `name>>>...>>>uplink` 语句编成 QueryStatsResponse 帧。
func grpcFrame(names []string) []byte {
	var msg []byte
	for i, name := range names {
		stat := encStat(name, int64(1000*(i+1)))
		msg = append(msg, 0x0a)
		msg = appendUvarint(msg, uint64(len(stat)))
		msg = append(msg, stat...)
	}
	frame := make([]byte, 5+len(msg))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(msg)))
	copy(frame[5:], msg)
	return frame
}

func encStat(name string, value int64) []byte {
	var b []byte
	b = append(b, 0x0a)
	b = appendUvarint(b, uint64(len(name)))
	b = append(b, name...)
	b = append(b, 0x10)
	b = appendUvarint(b, uint64(value))
	return b
}

func TestV2RayUserStatsOverH2C(t *testing.T) {
	addr, stop := startFakeStatsServer(t, []string{
		"user>>>11111111-2222-3333-4444-555555555555>>>traffic>>>uplink",
		"user>>>11111111-2222-3333-4444-555555555555>>>traffic>>>downlink",
		"user>>>other>>>traffic>>>uplink",
	})
	defer stop()

	users, err := v2rayUserStats(context.Background(), addr)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("users = %+v", users)
	}
	var found bool
	for _, u := range users {
		if u.Name == "11111111-2222-3333-4444-555555555555" {
			found = true
			if u.Up == 0 || u.Down == 0 {
				t.Fatalf("上下行未聚合：%+v", u)
			}
		}
	}
	if !found {
		t.Fatalf("缺少目标用户：%+v", users)
	}
}

func TestDecodeStatsResponseRejectsGarbage(t *testing.T) {
	if _, err := decodeStatsResponse([]byte{0xff}); err == nil {
		t.Fatal("非法 protobuf 应报错")
	}
	if _, err := decodeStatsResponse([]byte{0x0a, 0x05, 0x01}); err == nil {
		t.Fatal("长度越界应报错")
	}
}
