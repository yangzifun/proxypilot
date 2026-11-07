package metrics

import (
	"strings"
	"sync"
	"time"
)

// 服务状态采集优化：
// 旧实现每 2 秒对每个服务执行 2 次 systemctl（LoadState + is-active），
// 1 核小机上 14+ 次 fork/DBus 会把 systemd 的 CPU 顶满，进而 CPU% 长期 100%。
// 改为「一次 systemctl show 批量查询 + 15 秒缓存」。

type svcCacheEntry struct {
	at    time.Time
	state map[string]string
}

var (
	svcCacheMu sync.Mutex
	svcCache   = map[string]svcCacheEntry{}
)

const svcCacheTTL = 15 * time.Second

// ServiceStates 批量查询服务状态，带 15 秒缓存。
func ServiceStates(r Runner, names []string) map[string]string {
	key := strings.Join(names, ",")
	svcCacheMu.Lock()
	if e, ok := svcCache[key]; ok && time.Since(e.at) < svcCacheTTL {
		out := make(map[string]string, len(e.state))
		for k, v := range e.state {
			out[k] = v
		}
		svcCacheMu.Unlock()
		return out
	}
	svcCacheMu.Unlock()

	out := queryServiceStates(r, names)

	svcCacheMu.Lock()
	svcCache[key] = svcCacheEntry{at: time.Now(), state: out}
	svcCacheMu.Unlock()

	cp := make(map[string]string, len(out))
	for k, v := range out {
		cp[k] = v
	}
	return cp
}

// queryServiceStates 用一次 systemctl show 取回所有服务的 LoadState/ActiveState。
func queryServiceStates(r Runner, names []string) map[string]string {
	out := map[string]string{}
	valid := make([]string, 0, len(names))
	for _, n := range names {
		if validServiceName(n) {
			valid = append(valid, n)
		}
	}
	if len(valid) > 0 {
		args := append([]string{"show", "-p", "Id", "-p", "LoadState", "-p", "ActiveState", "--value"}, valid...)
		if text, err := r("systemctl", args...); err == nil {
			// --value 输出按属性顺序逐行返回，用「空行」分块不可靠；
			// 这里用 `systemctl show` 不带 --value 的稳定格式更安全。
			_ = text
		}
		// 稳定格式：`systemctl show -p Id -p LoadState -p ActiveState a b c`
		args2 := append([]string{"show", "-p", "Id", "-p", "LoadState", "-p", "ActiveState"}, valid...)
		if text, err := r("systemctl", args2...); err == nil {
			out = parseSystemctlShow(text, valid)
			if len(out) > 0 {
				return fillSSHStates(r, out)
			}
		}
	}
	// 兜底：逐个 is-active（老路径）
	for _, name := range valid {
		text, err := r("systemctl", "is-active", name)
		st := strings.TrimSpace(text)
		if st == "" {
			st = "unknown"
		}
		_ = err
		out[name] = truncateState(st)
	}
	return fillSSHStates(r, out)
}

func truncateState(s string) string {
	if len(s) > 32 {
		return s[:32]
	}
	return s
}

// parseSystemctlShow 解析形如：
//
//	Id=sing-box.service
//	LoadState=loaded
//	ActiveState=active
//	Id=mihomo.service
//	...
func parseSystemctlShow(text string, names []string) map[string]string {
	out := map[string]string{}
	var curID, load, active string
	flush := func() {
		if curID == "" {
			return
		}
		unit := strings.TrimSuffix(curID, ".service")
		if load == "not-found" {
			out[unit] = "not-installed"
		} else if active != "" {
			out[unit] = truncateState(active)
		}
		curID, load, active = "", "", ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "Id":
			flush()
			curID = v
		case "LoadState":
			load = v
		case "ActiveState":
			active = v
		}
	}
	flush()
	// 只保留请求过的服务
	res := map[string]string{}
	for _, n := range names {
		if v, ok := out[n]; ok {
			res[n] = v
		}
	}
	return res
}

// fillSSHStates 保证 sshd 键存在（Debian/Ubuntu 上单元名常为 ssh）。
func fillSSHStates(r Runner, out map[string]string) map[string]string {
	if _, ok := out["sshd"]; !ok {
		if v, ok2 := out["ssh"]; ok2 {
			out["sshd"] = v
		} else if text, err := r("systemctl", "is-active", "sshd"); err == nil && strings.TrimSpace(text) != "" {
			out["sshd"] = truncateState(strings.TrimSpace(text))
		} else {
			out["sshd"] = "unknown"
		}
	}
	return out
}

// resetSvcCache 供测试清空缓存。
func resetSvcCache() {
	svcCacheMu.Lock()
	svcCache = map[string]svcCacheEntry{}
	svcCacheMu.Unlock()
}
