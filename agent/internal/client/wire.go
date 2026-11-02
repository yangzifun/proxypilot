package client

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

// wireFieldCount 一条 wire 记录的字段数（与 Worker 的 agentWireLine 一致）。
const wireFieldCount = 14

// V13.7：第 15 个字段（下标 14）是 install_versions（base64 JSON）；老 Agent 会忽略它，保持兼容。
const wireFieldInstallVersions = 14

// ParseWire 解析响应里的 wire 字段：记录用 ~ 分隔。
func ParseWire(s string) []Action {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	lines := strings.Split(s, "~")
	out := make([]Action, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if a, ok := ParseActionLine(line); ok {
			out = append(out, a)
		}
	}
	return out
}

// ParseActionLine 解析单条 wire 记录（便于单测）。
// 字段不足 / id 非法 → ok=false（丢弃，避免执行半截任务）。
func ParseActionLine(line string) (Action, bool) {
	var a Action
	fields := strings.Split(line, "|")
	if len(fields) < wireFieldCount {
		return a, false
	}
	id, err := strconv.Atoi(strings.TrimSpace(fields[0]))
	if err != nil || id <= 0 {
		return a, false
	}
	a.ID = id
	a.Type = strings.TrimSpace(fields[1])
	a.ConfigID = atoi(fields[2])
	a.Name, a.DecodeErr = b64Text(fields[3], a.DecodeErr)
	a.Path, a.DecodeErr = b64Text(fields[4], a.DecodeErr)
	a.Content, a.DecodeErr = b64Text(fields[5], a.DecodeErr)
	a.Sha256 = strings.ToLower(strings.TrimSpace(fields[6]))
	a.Backup = strings.TrimSpace(fields[7]) == "1"
	a.RestartCmd, a.DecodeErr = b64Text(fields[8], a.DecodeErr)
	a.CheckCmd, a.DecodeErr = b64Text(fields[9], a.DecodeErr)
	a.ServiceName, a.DecodeErr = b64Text(fields[10], a.DecodeErr)
	a.Version = atoi(fields[11])
	a.Command, a.DecodeErr = b64Text(fields[12], a.DecodeErr)
	a.Timeout = atoi(fields[13])
	if len(fields) > wireFieldInstallVersions {
		if text, err := b64Text(fields[wireFieldInstallVersions], false); !err && strings.TrimSpace(text) != "" {
			var vers map[string]string
			if json.Unmarshal([]byte(text), &vers) == nil {
				a.InstallVersions = vers
			}
		}
	}
	return a, true
}

func atoi(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0
	}
	return n
}

// b64Text 解码 base64 文本字段（Worker 侧用标准 base64；失败即置位 DecodeErr）。
func b64Text(v string, alreadyFailed bool) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", alreadyFailed
	}
	if raw, err := base64.StdEncoding.DecodeString(v); err == nil {
		return string(raw), alreadyFailed
	}
	if raw, err := base64.RawStdEncoding.DecodeString(v); err == nil {
		return string(raw), alreadyFailed
	}
	return "", true
}
