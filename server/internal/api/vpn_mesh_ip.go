package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
)

// mountMeshIP 返回该服务器固定的组网 IP：已登记则原样复用（下发后不再变化），
// 未登记则从 10.99.0.0/16 中分配一个未被占用的地址并写入成员表。
func (h *handlers) mountMeshIP(ctx context.Context, serverID int64) (string, error) {
	var ip string
	err := h.db.QueryRowContext(ctx,
		`SELECT mesh_ip4 FROM vpn_mesh_nodes WHERE server_id = ?`, serverID).Scan(&ip)
	if err == nil && net.ParseIP(ip) != nil {
		return ip, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	used := map[string]struct{}{"10.99.0.1": {}} // 中心网关保留
	rows, err := h.db.QueryContext(ctx, `SELECT mesh_ip4 FROM vpn_mesh_nodes WHERE mesh_ip4 <> ''`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err == nil {
			used[v] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	for _, block := range []string{"10.99.0.%d", "10.99.1.%d"} {
		for host := 20; host <= 250; host++ {
			cand := fmt.Sprintf(block, host)
			if _, ok := used[cand]; ok {
				continue
			}
			if _, err := h.db.ExecContext(ctx,
				`INSERT INTO vpn_mesh_nodes (server_id, mesh_ip4, domain, state, transport, rtt_ms, last_seen, updated_at)
				 VALUES (?, ?, '', 'down', 'masque', 0, 0, 0)
				 ON DUPLICATE KEY UPDATE mesh_ip4 = VALUES(mesh_ip4)`, serverID, cand); err != nil {
				return "", err
			}
			return cand, nil
		}
	}
	return "", errors.New("组网地址池已耗尽")
}

// meshCenterFallbackIP 组网中心公网地址：域名被 CDN 代理时，部分节点会解析到 CDN 地址，
// QUIC 握手会超时，因此单元里始终带一个直连 IP 作为回退候选。
const meshCenterFallbackIP = "23.254.172.198"

// meshEndpoints 组装组网中心候选地址列表（域名优先，IP 兜底）。
func meshEndpoints(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		domain = "vpncenter.yzfn.top"
	}
	eps := []string{domain + ":443"}
	if meshCenterFallbackIP != "" {
		eps = append(eps, meshCenterFallbackIP+":443")
	}
	return strings.Join(eps, ",")
}

