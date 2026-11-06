package api

import (
	"crypto/ecdh"
	"encoding/base64"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
)

type singboxNode struct {
	Name      string `json:"name"`
	Domain    string `json:"domain"`
	CDNDomain string `json:"cdn_domain"`
}

func (t *singboxTemplate) engineOn(key string) bool {
	switch key {
	case "masque":
		return t.EnableMasque == 1
	}
	if t.EnableSingbox != nil {
		return *t.EnableSingbox == 1
	}
	return true
}

// x25519PublicKeyFromPrivate 由 REALITY 私钥推导公钥（base64url，无填充），与 Worker 一致。
func x25519PublicKeyFromPrivate(privateB64 string) string {
	raw, ok := decodeAnyBase64(privateB64)
	if !ok || len(raw) != 32 {
		return ""
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
}

func (t *singboxTemplate) realityPublicKey() string {
	if explicit := strings.TrimSpace(t.RealityPublicKey); explicit != "" {
		return explicit
	}
	priv := strings.TrimSpace(t.RealityPrivateKey)
	if priv == "" {
		return ""
	}
	return x25519PublicKeyFromPrivate(priv)
}

// singboxClientLinks 生成客户端订阅链接（WARP 已从模板移除，这里不生成 warp 变体）。
func singboxClientLinks(tpl *singboxTemplate, node singboxNode, users []sbUser) []map[string]any {
	ports := singboxPorts(tpl)
	domain := strings.TrimSpace(node.Domain)
	cdnDomain := strings.TrimSpace(node.CDNDomain)
	enc := url.QueryEscape
	out := []map[string]any{}
	multi := len(users) > 1

	engineSb := tpl.engineOn("singbox")
	opts := tpl.protoOptions()
	realityPub := tpl.realityPublicKey()
	wsPath := firstNonEmpty(strings.TrimSpace(jsonStr(optMap(opts, "vlws")["path"])), "/vlws")
	realitySni := firstNonEmpty(strings.TrimSpace(jsonStr(optMap(opts, "reality")["server_name"])), strings.TrimSpace(tpl.RealitySNI), "www.microsoft.com")
	wsSni := firstNonEmpty(strings.TrimSpace(tpl.WSSNI), "www.bing.com")

	add := func(protocol string, port int, user, link, note string) {
		out = append(out, map[string]any{
			"id": len(out) + 1, "protocol": protocol, "port": port, "user": user, "link": link, "note": note,
		})
	}

	for _, u := range users {
		suffix := ""
		if multi {
			suffix = "-" + u.Name
		}
		tag := func(proto string) string { return node.Name + suffix + "-" + proto }

		if engineSb && tpl.EnableSS != 0 && u.SSPassword != "" {
			auth := base64.RawURLEncoding.EncodeToString([]byte(
				firstNonEmpty(strings.TrimSpace(tpl.SSMethod), singboxDefaultSSMethod) + ":" +
					strings.TrimSpace(tpl.SSServerKey) + ":" + u.SSPassword))
			add("shadowsocks", ports["ss"], u.Name,
				"ss://"+auth+"@"+domain+":"+strconv.Itoa(ports["ss"])+"#"+enc(tag("ss")),
				"SS2022 多用户：密码 = 服务器密钥:用户密钥")
		}
		if engineSb && tpl.EnableAnyTLS != 0 {
			link := "anytls://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["anytls"]) +
				"/?security=tls&sni=" + enc(wsSni) + "&insecure=1#" + enc(tag("anytls"))
			add("anytls", ports["anytls"], u.Name, link, "自签证书：客户端需允许不安全")
		}
		if engineSb && tpl.EnableHY2 != 0 {
			link := "hysteria2://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["hy2"]) +
				"/?sni=" + enc(wsSni) + "&insecure=1&alpn=h3#" + enc(tag("hy2"))
			add("hysteria2", ports["hy2"], u.Name, link, "UDP 端口：注意放行与 NAT 映射")
		}
		if engineSb && tpl.EnableReality != 0 && realityPub != "" {
			link := "vless://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["reality"]) +
				"/?security=reality&encryption=none&pbk=" + enc(realityPub) +
				"&fp=chrome&sni=" + enc(realitySni) + "&sid=" + enc(tpl.RealityShortID) +
				"&spx=%2F&flow=xtls-rprx-vision&type=tcp#" + enc(tag("reality"))
			add("vless-reality", ports["reality"], u.Name, link, "pbk / sid 必须与服务端一致")
		}
		if engineSb && tpl.EnableVLWS != 0 && domain != "" {
			link := "vless://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["vlws"]) +
				"/?security=tls&encryption=none&sni=" + enc(domain) + "&fp=chrome&type=ws&path=" +
				enc(wsPath) + "&host=" + enc(domain) + "#" + enc(tag("vlws"))
			add("vless-ws", ports["vlws"], u.Name, link, "直连源站 "+strconv.Itoa(ports["vlws"])+"；依赖 80/443 签证书")
			if cdnDomain != "" {
				// CDN 优选：按模板配置展开为多个地址；未配置时回退 cdn_domain
				vlwsOpt := optMap(tpl.protoOptions(), "vlws")
				prefer := optMap(vlwsOpt, "cdn_prefer")
				preferOn := cdnPreferEnabled() || jsonInt(prefer["enabled"]) != 0
				preferGroup := jsonStr(prefer["group"])
				preferMode := jsonStr(prefer["mode"])
				preferLimit := int(jsonInt(prefer["limit"]))
				addrs := []cdnPrefer{}
				if preferOn {
					list := cdnPreferredForGroup(preferGroup)
					if preferMode == "random" {
						rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
						n := preferLimit
						if n <= 0 {
							n = 1
						}
						if n > len(list) {
							n = len(list)
						}
						addrs = list[:n]
					} else {
						if preferLimit > 0 && len(list) > preferLimit {
							list = list[:preferLimit]
						}
						addrs = list
					}
				}
				buildCdnLink := func(addr string, suffix string) map[string]any {
					host := net.JoinHostPort(addr, "443")
					link := "vless://" + u.UUID + "@" + host + "/?security=tls&encryption=none&sni=" +
						enc(cdnDomain) + "&fp=chrome&type=ws&path=" + enc(wsPath) + "&host=" + enc(cdnDomain) +
						"#" + enc(tag("vlws")+"-cdn"+suffix)
					return map[string]any{
						"id": 0, "protocol": "vless-ws", "port": 443, "user": u.Name, "link": link, "cdn": true,
						"note": "CDN 优选：" + host + ":443（SNI/Host 保持 " + cdnDomain + "）",
					}
				}
				if len(addrs) == 0 {
					item := buildCdnLink(cdnDomain, "")
					item["id"] = len(out) + 1
					out = append(out, item)
				} else {
					for i, a := range addrs {
						item := buildCdnLink(a.Address, "-"+strconv.Itoa(i+1))
						item["id"] = len(out) + 1
						item["cdn_preferred"] = true
						item["remark"] = a.Remark
						out = append(out, item)
					}
				}
			}
		}
		if engineSb && tpl.EnableVMess != 0 && domain != "" {
			link := "tuic://" + enc(u.UUID) + ":" + enc(u.UUID) + "@" + domain + ":" + strconv.Itoa(ports["vmess"]) +
				"/?congestion_control=bbr&alpn=h3&sni=" + enc(domain) + "&allow_insecure=1#" + enc(tag("tuic"))
			add("tuic", ports["vmess"], u.Name, link, "TUIC v5（QUIC，UDP 13704）")
		}
	}
	return out
}
