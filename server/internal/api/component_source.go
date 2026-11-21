package api

import (
	"fmt"
	"net/url"
	"strings"
)

// singboxComponentBase 是唯一允许的 sing-box 组件分发基址（S3 自建构建）。
// 禁止从 GitHub / 第三方 / 任意后台 URL 安装 sing-box，避免把未经本平台构建的
// 二进制下发到全部节点；component_base_url 只能指向该地址。
const singboxComponentBase = "https://s3.proxypilot.yangzifun.org/singbox"

// normalizeSingboxComponentBase 校验并归一化 component_base_url：
// 仅允许 https://s3.proxypilot.yangzifun.org/singbox（允许尾斜杠），
// 空值表示使用默认 S3 基址；其他来源（GitHub、http、内网、带 userinfo/query 等）一律拒绝。
func normalizeSingboxComponentBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return singboxComponentBase, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("组件基址不是合法 URL")
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("组件基址只允许 https")
	}
	if u.User != nil {
		return "", fmt.Errorf("组件基址不允许携带用户名/密码")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("组件基址不允许 query/fragment")
	}
	if !strings.EqualFold(u.Host, "s3.proxypilot.yangzifun.org") {
		return "", fmt.Errorf("组件基址只允许 s3.proxypilot.yangzifun.org")
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if path != "/singbox" {
		return "", fmt.Errorf("组件基址路径只允许 /singbox")
	}
	return singboxComponentBase, nil
}

// validateSingboxComponentBase 是设置保存/启动时的校验入口；空值合法（使用默认 S3）。
func validateSingboxComponentBase(raw string) error {
	_, err := normalizeSingboxComponentBase(raw)
	return err
}

// isAllowedSingboxComponentURL 校验 bootstrap/Agent 实际下载 URL 是否仍落在 S3 组件目录内。
func isAllowedSingboxComponentURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if !strings.EqualFold(u.Host, "s3.proxypilot.yangzifun.org") {
		return false
	}
	path := u.Path
	return strings.HasPrefix(path, "/singbox/") && !strings.Contains(path, "..")
}
