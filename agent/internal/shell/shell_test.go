package shell

import (
	"bufio"
	"net"
	"testing"
	"time"
)

func TestURL(t *testing.T) {
	cases := []struct{ origin, want string }{
		{"https://pp.example.com", "wss://pp.example.com/api/shell/agent/7?session=abc"},
		{"https://pp.example.com/", "wss://pp.example.com/api/shell/agent/7?session=abc"},
		{"http://127.0.0.1:8787", "ws://127.0.0.1:8787/api/shell/agent/7?session=abc"},
	}
	for _, c := range cases {
		if got := URL(c.origin, 7, "abc"); got != c.want {
			t.Errorf("URL(%q) = %q, want %q", c.origin, got, c.want)
		}
	}
}

func TestWriteReadMaskedFrame(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	client := &Conn{c: c1, br: bufio.NewReader(c1)}
	server := &Conn{c: c2, br: bufio.NewReader(c2)}

	payload := []byte("root@hk-01:~# ls -la\n")
	go func() {
		_ = client.WriteMessage(opBinary, payload)
	}()

	_ = c2.SetReadDeadline(time.Now().Add(2 * time.Second))
	op, data, err := server.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != opBinary {
		t.Fatalf("opcode = %d, want %d", op, opBinary)
	}
	if string(data) != string(payload) {
		t.Fatalf("payload = %q, want %q", string(data), string(payload))
	}
}
