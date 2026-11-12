package api

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

// validateDistributionURL 校验「组件/Agent 分发基址」。
//
// 审计中-02/低-02：这个设置决定全部节点从哪里下载二进制，属于面板信任根之一。
// 之前只校验 http(s)，普通管理员可把它指向内网服务（盲 SSRF/内网投递）或 http
// 明文地址。现在要求：
//   - 必须是 https；
//   - 不允许 URL 内嵌账号密码（userinfo）；
//   - 主机不能是 localhost / 私网 / 回环 / 链路本地 / CGNAT 字面量。
//
// 说明：DNS 解析层面的私网判定无法在保存时完全防住（DNS rebinding），分发实际
// 下载发生在节点侧；这里拦的是「管理员直接把面板/节点指向内网」的常见路径。
func validateDistributionURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("必须是合法的 https URL")
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return errors.New("必须是 https URL（禁止明文分发）")
	}
	if u.User != nil {
		return errors.New("URL 不能包含账号密码")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return errors.New("URL 缺少主机名")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errors.New("不能指向 localhost")
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
		return errors.New("不能指向内网/保留地址")
	}
	return nil
}