package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"proxypilot-agent/internal/client"
)

func TestWSOnceSignalsWake(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		_ = c.Write(r.Context(), websocket.MessageText, []byte("wake"))
		time.Sleep(30 * time.Millisecond)
	}))
	defer srv.Close()

	a := &Agent{
		opts: Options{
			Client: client.New(srv.URL, "ag_test", Version, false),
			Log:    t.Logf,
		},
		wake: make(chan struct{}, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- a.wsOnce(ctx) }()

	select {
	case <-a.wake:
		// ok：握手成功并收到 wake
	case <-ctx.Done():
		t.Fatal("等待 wake 超时")
	}
	<-done
	if !a.wsConnected.Load() {
		t.Fatal("wsConnected 应为 true（连接成功后置位）")
	}
}
