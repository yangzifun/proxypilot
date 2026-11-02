package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// Options 一次终端会话的参数。
type Options struct {
	Origin   string // Worker 地址（http(s)://...）
	Key      string // 接入密钥 ag_xxx
	ServerID int
	Session  string
	Insecure bool // 跳过 TLS 校验（仅排查）
	Log      func(format string, args ...any)
	Version  string
}

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// URL 把站点地址拼成 Agent 侧的 WebSocket 地址。
func URL(origin string, serverID int, session string) string {
	base := strings.TrimRight(strings.TrimSpace(origin), "/")
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	return fmt.Sprintf("%s/api/shell/agent/%d?session=%s", base, serverID, session)
}

// Serve 连上 Worker 并跑一个带 PTY 的交互式 shell；阻塞到会话结束。
func Serve(ctx context.Context, o Options) error {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ua := "ProxyPilot-Agent/" + strings.TrimPrefix(o.Version, "v")
	conn, err := Dial(URL(o.Origin, o.ServerID, o.Session), map[string]string{"X-Agent-Token": o.Key}, o.Insecure, ua)
	if err != nil {
		return err
	}
	defer conn.Close()

	shellPath := "/bin/bash"
	args := []string{"-il"}
	if _, statErr := os.Stat(shellPath); statErr != nil {
		shellPath = "/bin/sh"
		args = []string{"-i"}
	}
	cmd := exec.Command(shellPath, args...)
	cmd.Env = append(os.Environ(),
		"TERM=xterm-256color",
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
		"PROXYPILOT_SHELL=1",
	)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		return fmt.Errorf("启动 %s 失败：%w", shellPath, err)
	}
	logf("实时终端已建立：pid=%d shell=%s session=%s", cmd.Process.Pid, shellPath, o.Session)

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			_ = f.Close()
			_ = conn.Close()
		})
	}
	defer stop()

	// PTY 输出 → WebSocket
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 8192)
		for {
			n, rerr := f.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(opBinary, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				return
			}
		}
	}()

	// 退出信号 → 关连接
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		case <-time.After(12 * time.Hour):
			stop()
		}
	}()

	for {
		op, data, rerr := conn.ReadMessage()
		if rerr != nil {
			break
		}
		switch op {
		case opText:
			var m resizeMsg
			if json.Unmarshal(data, &m) == nil {
				if m.Type == "resize" && m.Cols > 0 && m.Rows > 0 {
					_ = pty.Setsize(f, &pty.Winsize{Cols: m.Cols, Rows: m.Rows})
				}
				if m.Type == "exit" {
					stop()
					return nil
				}
			}
		case opBinary:
			if _, werr := f.Write(data); werr != nil {
				stop()
				return nil
			}
		}
		select {
		case <-done:
			return nil
		default:
		}
	}
	stop()
	return nil
}
