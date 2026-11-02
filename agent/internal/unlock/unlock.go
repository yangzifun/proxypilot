package unlock

import (
	"os"
	"strings"
)

const defaultToolBase = "https://s3.proxypilot.yangzifun.org/tools"

// Command 在目标服务器上执行 AI / 影视解锁检测。
// Agent 内部任务不需要 allow_exec，避免普通服务器因关闭远程命令而无法检测。
func Command() string {
	toolBase := strings.TrimRight(strings.TrimSpace(os.Getenv("PP_UNLOCK_TOOL_BASE")), "/")
	if toolBase == "" {
		toolBase = defaultToolBase
	}
	var b strings.Builder
	b.WriteString("command -v curl >/dev/null 2>&1 || { echo 'curl_missing=1'; exit 0; }\n")
	b.WriteString("geo=$(curl -L -sS --max-time 5 https://www.cloudflare.com/cdn-cgi/trace 2>/dev/null | sed -n 's/^loc=//p' | head -1)\n")
	b.WriteString("check() { key=\"$1\"; url=\"$2\"; out=$(curl -L -sS --retry 1 --retry-delay 1 -o /dev/null -w '%{http_code}|%{url_effective}' --max-time 8 -A 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36' \"$url\" 2>/dev/null || true); printf '%s=%s|%s\\n' \"$key\" \"${out:-000|error}\" \"$geo\"; }\n")
	for _, s := range []struct{ key, url string }{
		{"chatgpt", "https://chatgpt.com/cdn-cgi/trace"},
		{"claude", "https://claude.ai/"},
		{"gemini", "https://gemini.google.com/app"},
		{"copilot", "https://copilot.microsoft.com/"},
		{"netflix", "https://www.netflix.com/title/81215567"},
		{"youtube", "https://www.youtube.com/premium"},
		{"disney", "https://www.disneyplus.com/"},
		{"primevideo", "https://www.primevideo.com/"},
		{"spotify", "https://open.spotify.com/"},
		{"tiktok", "https://www.tiktok.com/"},
	} {
		b.WriteString("check '" + s.key + "' '" + s.url + "'\n")
	}
	b.WriteString("echo '##CMT##'\n")
	b.WriteString("arch=$(uname -m); case \"$arch\" in x86_64|amd64) arch=amd64;; aarch64|arm64) arch=arm64;; *) arch='' ;; esac\n")
	b.WriteString("if [ -n \"$arch\" ]; then bin=\"/tmp/.pp-cmt-$arch\"; if [ ! -x \"$bin\" ]; then curl -L -sS --max-time 60 -o \"$bin\" \"" + toolBase + "/CommonMediaTests-linux-$arch\" 2>/dev/null && chmod +x \"$bin\"; fi; if [ -x \"$bin\" ]; then \"$bin\" -l zh 2>/dev/null | grep -E 'Netflix|DisneyPlus|完整解锁|仅自制剧|地域信息|已开通|即将开通|未开通|不支持|无法正常使用|疑似代理|不提供服务' | head -16; fi; fi\n")
	b.WriteString("echo '##RRC##'\n")
	b.WriteString("rrc=/tmp/.pp-rrc.sh; funcs=/tmp/.pp-rrc-func.sh; if [ ! -s \"$rrc\" ]; then curl -L -sS --max-time 60 -o \"$rrc\" \"" + toolBase + "/rrc.sh\" 2>/dev/null; fi; if [ -s \"$rrc\" ]; then sed '/^color_print$/,$d' \"$rrc\" > \"$funcs\"; cat >> \"$funcs\" <<'PPRRC'\ncolor_print\ncheck_os_type >/dev/null 2>&1\ncheck_dependencies >/dev/null 2>&1\nprocess >/dev/null 2>&1 || true\nMediaUnlockTest_YouTube_Premium\nMediaUnlockTest_PrimeVideo\nMediaUnlockTest_Spotify\nWebTest_OpenAI\nWebTest_Gemini\nWebTest_Claude\nPPRRC\ntimeout 120 bash \"$funcs\" 2>/dev/null | grep -E 'YouTube Premium|Amazon Prime Video|Spotify Registration|ChatGPT|Google Gemini|Claude' | head -12; fi\n")
	return b.String()
}
