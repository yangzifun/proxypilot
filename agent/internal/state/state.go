// Package state 维护 Agent 本地状态文件（与 V8.1 shell agent 同名同目录）：
//
//	<StateDir>/next_poll  服务端下发的轮询间隔（秒）
//	<StateDir>/last_poll  上次 poll 的 unix 秒（用于 cron/timer 模式下的节流）
//	<StateDir>/applied   每行「config_id version」，上报后由服务端做版本差异派发
package state

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"proxypilot-agent/internal/client"
)

const (
	nextPollFile = "next_poll"
	lastPollFile = "last_poll"
	appliedFile  = "applied"
)

// Store 状态目录。
type Store struct {
	Dir string
}

// Ensure 创建状态目录（0755）。
func (s Store) Ensure() error {
	if s.Dir == "" {
		return nil
	}
	return os.MkdirAll(s.Dir, 0o755)
}

// NextPoll 读取服务端下发的轮询间隔（秒）；不存在或不合法返回 0。
func (s Store) NextPoll() int {
	n, err := strconv.Atoi(strings.TrimSpace(s.read(nextPollFile)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// SetNextPoll 写入轮询间隔。
func (s Store) SetNextPoll(sec int) error {
	if sec <= 0 {
		return nil
	}
	return s.write(nextPollFile, strconv.Itoa(sec))
}

// LastPoll 读取上次 poll 时间（unix 秒）。
func (s Store) LastPoll() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s.read(lastPollFile)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// SetLastPoll 写入上次 poll 时间。
func (s Store) SetLastPoll(ts int64) error {
	return s.write(lastPollFile, strconv.FormatInt(ts, 10))
}

// Applied 读取已应用版本清单（容错：非法行忽略）。
func (s Store) Applied() []client.Applied {
	out := []client.Applied{}
	seen := map[int]bool{}
	for _, line := range strings.Split(s.read(appliedFile), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		cid, err1 := strconv.Atoi(fields[0])
		ver, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil || cid <= 0 || seen[cid] {
			continue
		}
		seen[cid] = true
		out = append(out, client.Applied{ConfigID: cid, Version: ver})
	}
	return out
}

// SetApplied 记录某配置已应用到指定版本（同 config_id 只保留最新一行）。
func (s Store) SetApplied(configID, version int) error {
	if configID <= 0 {
		return nil
	}
	kept := []string{}
	for _, line := range strings.Split(s.read(appliedFile), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if cid, err := strconv.Atoi(fields[0]); err == nil && cid == configID {
			continue
		}
		kept = append(kept, strings.Join(fields, " "))
	}
	kept = append(kept, strconv.Itoa(configID)+" "+strconv.Itoa(version))
	return s.write(appliedFile, strings.Join(kept, "\n")+"\n")
}

func (s Store) path(name string) string {
	if s.Dir == "" {
		return ""
	}
	return filepath.Join(s.Dir, name)
}

func (s Store) read(name string) string {
	p := s.path(name)
	if p == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s Store) write(name, text string) error {
	p := s.path(name)
	if p == "" {
		return nil
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(text), 0o644)
}
