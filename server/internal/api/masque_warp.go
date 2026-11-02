package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yzfn/proxypilot/server/internal/warp"
)

const (
	masqueWarpDefaultSNI = "zt-masque.cloudflareclient.com"
	masqueWarpMaxIPs     = 32
	masqueWarpMaxPorts   = 32
)

var (
	masqueWarpClient = warp.New()
	masqueWarpMu     sync.Mutex
	masqueWarpRateMu sync.Mutex
	masqueWarpTimes  []time.Time
)

type masqueWarpSettings struct {
	Enabled   bool     `json:"enabled"`
	SNI       string   `json:"sni"`
	ServerIPs []string `json:"server_ips"`
	Ports     []int    `json:"ports"`
	MTU       int      `json:"mtu"`
	DNS       []string `json:"dns"`
	UpdatedAt int64    `json:"updated_at"`
}

type masqueWarpProfile struct {
	ID            int64
	UUID          string
	DeviceID      string
	TokenEnc      string
	PrivateKeyEnc string
	PublicKey     string
	PeerPublicKey string
	IPv4          string
	IPv6          string
	Status        string
	LastError     string
	AuthorizedBy  string
	CreatedAt     int64
	LastRefreshAt int64
	RevokedAt     int64
}

func defaultMasqueWarpSettings() masqueWarpSettings {
	return masqueWarpSettings{
		Enabled: false,
		SNI:     masqueWarpDefaultSNI,
		ServerIPs: []string{
			"162.159.198.1", "162.159.198.2",
			"162.159.199.1", "162.159.199.2",
			"2606:4700:103::1", "2606:4700:103::2",
			"2606:4700:104::1", "2606:4700:104::2",
		},
		Ports: []int{443, 500, 1701, 4500, 4443, 8443, 8095},
		MTU:   1280,
		DNS:   []string{"1.1.1.1", "2606:4700:4700::1111"},
	}
}

func (h *handlers) loadMasqueWarpSettings(r *http.Request) (masqueWarpSettings, error) {
	s := defaultMasqueWarpSettings()
	var ips, ports, dns string
	var enabled int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT enabled, sni, server_ips, ports, mtu, dns, updated_at FROM masque_warp_settings WHERE id = 1`).
		Scan(&enabled, &s.SNI, &ips, &ports, &s.MTU, &dns, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		if _, err := h.db.ExecContext(r.Context(),
			`INSERT INTO masque_warp_settings (id, enabled, sni, server_ips, ports, mtu, dns, updated_at)
			 VALUES (1, 0, ?, ?, ?, ?, ?, ?)`,
			s.SNI, masqueJSON(s.ServerIPs), masqueJSON(s.Ports), s.MTU, masqueJSON(s.DNS), nowMillis()); err != nil {
			return s, err
		}
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.Enabled = enabled == 1
	if s.SNI == "" {
		s.SNI = masqueWarpDefaultSNI
	}
	_ = json.Unmarshal([]byte(ips), &s.ServerIPs)
	_ = json.Unmarshal([]byte(ports), &s.Ports)
	_ = json.Unmarshal([]byte(dns), &s.DNS)
	if len(s.ServerIPs) == 0 {
		s.ServerIPs = defaultMasqueWarpSettings().ServerIPs
	}
	if len(s.Ports) == 0 {
		s.Ports = defaultMasqueWarpSettings().Ports
	}
	if len(s.DNS) == 0 {
		s.DNS = defaultMasqueWarpSettings().DNS
	}
	if s.MTU <= 0 {
		s.MTU = 1280
	}
	return s, nil
}

func masqueJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (h *handlers) masqueWarpSettingsGet(w http.ResponseWriter, r *http.Request) {
	s, err := h.loadMasqueWarpSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 WARP 设置失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": s})
}

func (h *handlers) masqueWarpSettingsSave(w http.ResponseWriter, r *http.Request) {
	var in masqueWarpSettings
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	in.SNI = strings.TrimSpace(in.SNI)
	if in.SNI == "" {
		in.SNI = masqueWarpDefaultSNI
	}
	if len(in.ServerIPs) == 0 || len(in.ServerIPs) > masqueWarpMaxIPs {
		writeError(w, http.StatusBadRequest, "至少配置 1 个、最多 32 个 WARP 接入地址")
		return
	}
	for i, s := range in.ServerIPs {
		ip := net.ParseIP(strings.TrimSpace(s))
		if ip == nil {
			writeError(w, http.StatusBadRequest, "非法 IP："+s)
			return
		}
		in.ServerIPs[i] = ip.String()
	}
	if len(in.Ports) == 0 || len(in.Ports) > masqueWarpMaxPorts {
		writeError(w, http.StatusBadRequest, "至少配置 1 个、最多 32 个端口")
		return
	}
	for _, p := range in.Ports {
		if p < 1 || p > 65535 {
			writeError(w, http.StatusBadRequest, "端口必须在 1-65535")
			return
		}
	}
	if in.MTU < 1000 || in.MTU > 1500 {
		writeError(w, http.StatusBadRequest, "MTU 必须在 1000-1500")
		return
	}
	if len(in.DNS) == 0 {
		in.DNS = defaultMasqueWarpSettings().DNS
	}
	for _, d := range in.DNS {
		if net.ParseIP(strings.TrimSpace(d)) == nil {
			writeError(w, http.StatusBadRequest, "非法 DNS："+d)
			return
		}
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO masque_warp_settings (id, enabled, sni, server_ips, ports, mtu, dns, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE enabled=VALUES(enabled), sni=VALUES(sni), server_ips=VALUES(server_ips),
		   ports=VALUES(ports), mtu=VALUES(mtu), dns=VALUES(dns), updated_at=VALUES(updated_at)`,
		enabled, in.SNI, masqueJSON(in.ServerIPs), masqueJSON(in.Ports), in.MTU, masqueJSON(in.DNS), nowMillis()); err != nil {
		writeError(w, http.StatusInternalServerError, "保存 WARP 设置失败")
		return
	}
	in.UpdatedAt = nowMillis()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": in})
}

func (h *handlers) masqueWarpProfilesList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT p.id, p.uuid, p.device_id, p.public_key, p.peer_public_key, p.client_ipv4, p.client_ipv6,
		        p.status, p.last_error, p.authorized_by, p.created_at, p.last_refresh_at,
		        COALESCE(u.email,''), COALESCE(u.owner,'')
		   FROM masque_warp_profiles p
		   LEFT JOIN uuid_info u ON u.uuid = p.uuid
		  ORDER BY p.id DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 WARP 授权失败")
		return
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var (
			p            masqueWarpProfile
			email, owner string
		)
		if err := rows.Scan(&p.ID, &p.UUID, &p.DeviceID, &p.PublicKey, &p.PeerPublicKey,
			&p.IPv4, &p.IPv6, &p.Status, &p.LastError, &p.AuthorizedBy,
			&p.CreatedAt, &p.LastRefreshAt, &email, &owner); err != nil {
			continue
		}
		out = append(out, masqueWarpProfilePublic(p, email, owner))
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out})
}

func masqueWarpProfilePublic(p masqueWarpProfile, email, owner string) map[string]any {
	return map[string]any{
		"id": p.ID, "uuid": p.UUID, "email": email, "owner": owner,
		"warp_ipv4": p.IPv4, "warp_ipv6": p.IPv6,
		"status": p.Status, "last_error": p.LastError,
		"authorized_by": p.AuthorizedBy,
		"created_at":    p.CreatedAt, "last_refresh_at": p.LastRefreshAt,
	}
}

func (h *handlers) masqueWarpProfileLoad(r *http.Request, uuid string) (masqueWarpProfile, error) {
	var p masqueWarpProfile
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, uuid, device_id, token_enc, private_key_enc, public_key, peer_public_key,
		        client_ipv4, client_ipv6, status, last_error, authorized_by, created_at, last_refresh_at, revoked_at
		   FROM masque_warp_profiles WHERE uuid = ? LIMIT 1`, uuid).
		Scan(&p.ID, &p.UUID, &p.DeviceID, &p.TokenEnc, &p.PrivateKeyEnc, &p.PublicKey, &p.PeerPublicKey,
			&p.IPv4, &p.IPv6, &p.Status, &p.LastError, &p.AuthorizedBy, &p.CreatedAt, &p.LastRefreshAt, &p.RevokedAt)
	return p, err
}

func masqueWarpAllowRegister() bool {
	now := time.Now()
	masqueWarpRateMu.Lock()
	defer masqueWarpRateMu.Unlock()
	keep := masqueWarpTimes[:0]
	for _, t := range masqueWarpTimes {
		if now.Sub(t) < time.Minute {
			keep = append(keep, t)
		}
	}
	masqueWarpTimes = keep
	if len(masqueWarpTimes) >= 10 {
		return false
	}
	masqueWarpTimes = append(masqueWarpTimes, now)
	return true
}

func (h *handlers) masqueWarpProfileCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID string `json:"uuid"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式不正确")
		return
	}
	settings, err := h.loadMasqueWarpSettings(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 WARP 设置失败")
		return
	}
	if !settings.Enabled {
		writeError(w, http.StatusBadRequest, "请先在 WARP 设置里启用")
		return
	}
	var exists int
	if err := h.db.QueryRowContext(r.Context(), `SELECT 1 FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).Scan(&exists); err != nil {
		writeError(w, http.StatusNotFound, "UUID 不存在")
		return
	}

	masqueWarpMu.Lock()
	defer masqueWarpMu.Unlock()

	if p, err := h.masqueWarpProfileLoad(r, uuid); err == nil {
		if p.Status == "active" {
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "reused": true, "data": masqueWarpProfilePublic(p, "", "")})
			return
		}
	} else if err != sql.ErrNoRows {
		writeError(w, http.StatusInternalServerError, "读取授权失败")
		return
	}
	status, err := h.masqueWarpRegisterAndStore(r, uuid)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	p, err := h.masqueWarpProfileLoad(r, uuid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取授权失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": masqueWarpProfilePublic(p, "", "")})
}

func (h *handlers) masqueWarpRegisterAndStore(r *http.Request, uuid string) (int, error) {
	if !masqueWarpAllowRegister() {
		return http.StatusTooManyRequests, fmt.Errorf("注册过于频繁，请稍后再试")
	}
	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	p, err := masqueWarpClient.Register(ctx)
	if err != nil {
		if strings.Contains(err.Error(), warp.ErrBlocked.Error()) {
			return http.StatusTooManyRequests, fmt.Errorf("Cloudflare 拒绝注册（频控或风控），请稍后再试")
		}
		return http.StatusBadGateway, fmt.Errorf("注册 WARP 失败：%v", err)
	}
	secret := h.agentKeySecret()
	tokenEnc, err := encryptAgentToken(secret, p.Token)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("加密 WARP 凭据失败")
	}
	privEnc, err := encryptAgentToken(secret, p.PrivateKey)
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("加密 WARP 私钥失败")
	}
	by := ""
	if claims, ok := claimsFrom(r); ok {
		by = claims.Sub
	}
	now := nowMillis()
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO masque_warp_profiles
		   (uuid, device_id, token_enc, private_key_enc, public_key, peer_public_key,
		    client_ipv4, client_ipv6, status, last_error, authorized_by, created_at, last_refresh_at, revoked_at)
		 VALUES (?,?,?,?,?,?,?,?,'active','',?,?,?,0)
		 ON DUPLICATE KEY UPDATE device_id=VALUES(device_id), token_enc=VALUES(token_enc),
		   private_key_enc=VALUES(private_key_enc), public_key=VALUES(public_key),
		   peer_public_key=VALUES(peer_public_key), client_ipv4=VALUES(client_ipv4),
		   client_ipv6=VALUES(client_ipv6), status='active', last_error='',
		   authorized_by=VALUES(authorized_by), last_refresh_at=VALUES(last_refresh_at), revoked_at=0`,
		uuid, p.DeviceID, tokenEnc, privEnc, p.PublicKey, p.PeerPublicKey,
		p.IPv4, p.IPv6, by, now, now); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("保存 WARP 授权失败")
	}
	return http.StatusOK, nil
}

func (h *handlers) masqueWarpProfileRefresh(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式不正确")
		return
	}
	masqueWarpMu.Lock()
	defer masqueWarpMu.Unlock()
	old, err := h.masqueWarpProfileLoad(r, uuid)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "尚未授权")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取授权失败")
		return
	}
	if old.Status == "active" {
		if token := decryptAgentToken(h.agentKeySecret(), old.TokenEnc); token != "" {
			ctx, cancel := contextWithTimeout(r, 20*time.Second)
			_ = masqueWarpClient.Delete(ctx, old.DeviceID, token)
			cancel()
		}
	}
	status, err := h.masqueWarpRegisterAndStore(r, uuid)
	if err != nil {
		// 保留旧配置可用性；只记录错误
		_, _ = h.db.ExecContext(r.Context(),
			`UPDATE masque_warp_profiles SET last_error = ? WHERE uuid = ?`, err.Error(), uuid)
		writeError(w, status, err.Error())
		return
	}
	p, _ := h.masqueWarpProfileLoad(r, uuid)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": masqueWarpProfilePublic(p, "", "")})
}

func (h *handlers) masqueWarpProfileRevoke(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式不正确")
		return
	}
	p, err := h.masqueWarpProfileLoad(r, uuid)
	if err == sql.ErrNoRows {
		writeError(w, http.StatusNotFound, "尚未授权")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取授权失败")
		return
	}
	warning := ""
	if token := decryptAgentToken(h.agentKeySecret(), p.TokenEnc); token != "" {
		ctx, cancel := contextWithTimeout(r, 20*time.Second)
		if err := masqueWarpClient.Delete(ctx, p.DeviceID, token); err != nil {
			warning = "远端设备删除失败，已在本地撤销：" + err.Error()
		}
		cancel()
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE masque_warp_profiles SET status='revoked', revoked_at=?, last_error=? WHERE uuid=?`,
		nowMillis(), warning, uuid)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "warning": warning})
}

func (h *handlers) masqueWarpProfileConfig(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式不正确")
		return
	}
	h.masqueWarpWriteConfig(w, r, uuid)
}

func (h *handlers) masqueWarpWriteConfig(w http.ResponseWriter, r *http.Request, uuid string) {
	settings, err := h.loadMasqueWarpSettings(r)
	if err != nil || !settings.Enabled {
		writeError(w, http.StatusBadRequest, "WARP 未启用")
		return
	}
	p, err := h.masqueWarpProfileLoad(r, uuid)
	if err == sql.ErrNoRows || (err == nil && p.Status != "active") {
		writeError(w, http.StatusNotFound, "尚未授权或已撤销")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取授权失败")
		return
	}
	priv := decryptAgentToken(h.agentKeySecret(), p.PrivateKeyEnc)
	if priv == "" {
		writeError(w, http.StatusInternalServerError, "WARP 私钥不可用，请刷新授权")
		return
	}
	yaml := renderMasqueWarpYAML(settings, p, priv)
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"masque-%s.yaml\"", uuid))
	_, _ = w.Write([]byte(yaml))
}

func renderMasqueWarpYAML(s masqueWarpSettings, p masqueWarpProfile, privateKey string) string {
	var b strings.Builder
	b.WriteString("# Cloudflare WARP over MASQUE - mihomo Alpha\n")
	b.WriteString("# 需要 mihomo Alpha / ClashMi，稳定版不支持 masque\n\n")
	b.WriteString("mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nipv6: true\n\nproxies:\n")
	names := make([]string, 0)
	for _, ip := range s.ServerIPs {
		for _, port := range s.Ports {
			name := fmt.Sprintf("WARP-%s-%d", strings.NewReplacer(".", "-", ":", "-").Replace(ip), port)
			names = append(names, name)
			fmt.Fprintf(&b, "  - name: %q\n    type: masque\n    server: %q\n    port: %d\n    sni: %q\n",
				name, ip, port, s.SNI)
			fmt.Fprintf(&b, "    private-key: %q\n    public-key: %q\n", privateKey, p.PeerPublicKey)
			if p.IPv4 != "" {
				fmt.Fprintf(&b, "    ip: %s\n", p.IPv4)
			}
			if p.IPv6 != "" {
				fmt.Fprintf(&b, "    ipv6: %q\n", p.IPv6)
			}
			fmt.Fprintf(&b, "    mtu: %d\n    udp: true\n    remote-dns-resolve: true\n    dns: [", s.MTU)
			for i, d := range s.DNS {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%q", d)
			}
			b.WriteString("]\n")
		}
	}
	b.WriteString("\nproxy-groups:\n  - name: MASQUE\n    type: url-test\n    url: https://www.gstatic.com/generate_204\n    interval: 300\n    proxies:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "      - %q\n", n)
	}
	b.WriteString("\nrules:\n  - MATCH,MASQUE\n")
	return b.String()
}

func (h *handlers) masqueWarpPortal(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok || email == "" {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var uuid string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(uuid,'') FROM uuid_info WHERE LOWER(email) = ? LIMIT 1`, email).Scan(&uuid)
	settings, _ := h.loadMasqueWarpSettings(r)
	data := map[string]any{"enabled": settings.Enabled, "granted": false}
	if uuid != "" {
		if p, err := h.masqueWarpProfileLoad(r, uuid); err == nil && p.Status == "active" {
			data["granted"] = true
			data["uuid"] = uuid
			data["warp_ipv4"] = p.IPv4
			data["warp_ipv6"] = p.IPv6
			data["status"] = p.Status
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "email": email, "data": data})
}

func (h *handlers) masqueWarpPortalConfig(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok || email == "" {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	var uuid string
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(uuid,'') FROM uuid_info WHERE LOWER(email) = ? LIMIT 1`, email).Scan(&uuid)
	if uuid == "" {
		writeError(w, http.StatusNotFound, "该邮箱还没有绑定 UUID")
		return
	}
	h.masqueWarpWriteConfig(w, r, uuid)
}
