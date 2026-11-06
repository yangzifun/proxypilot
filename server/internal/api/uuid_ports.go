package api

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// perUserInboundsEnabled 读取开关（默认关闭）。
func (h *handlers) perUserInboundsEnabled(ctx context.Context) bool {
	var v string
	err := h.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE `+"`key`"+` = 'singbox_per_user_enabled'`).Scan(&v)
	if err != nil {
		if err == sql.ErrNoRows {
			return false
		}
		return false
	}
	return strings.TrimSpace(v) == "1"
}

// portBase 读取端口段起点（默认 20000）。
func (h *handlers) portBase(ctx context.Context) int {
	var v string
	if err := h.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE `+"`key`"+` = 'singbox_port_base'`).Scan(&v); err != nil {
		return 20000
	}
	base := 0
	for _, ch := range strings.TrimSpace(v) {
		if ch < '0' || ch > '9' {
			return 20000
		}
		base = base*10 + int(ch-'0')
	}
	if base < 1024 || base > 65000 {
		return 20000
	}
	return base
}

// ensureUUIDPorts 为一个节点的用户分配稳定端口。
// 已存在映射的用户复用旧端口；新用户从 base 起找空闲端口。
func (h *handlers) ensureUUIDPorts(ctx context.Context, serverID int64, uuids []string) (map[string]int, error) {
	out := map[string]int{}
	if len(uuids) == 0 {
		return out, nil
	}
	base := h.portBase(ctx)
	rows, err := h.db.QueryContext(ctx,
		`SELECT uuid, port FROM uuid_port_map WHERE server_id = ?`, serverID)
	if err != nil {
		return out, err
	}
	used := map[int]bool{}
	for rows.Next() {
		var u string
		var p int
		if rows.Scan(&u, &p) == nil {
			out[u] = p
			used[p] = true
		}
	}
	rows.Close()

	uniq := map[string]bool{}
	list := make([]string, 0, len(uuids))
	for _, u := range uuids {
		u = strings.TrimSpace(u)
		if u == "" || uniq[u] {
			continue
		}
		uniq[u] = true
		list = append(list, u)
	}
	sort.Strings(list)

	next := base
	for _, u := range list {
		if _, ok := out[u]; ok {
			continue
		}
		for used[next] {
			next++
		}
		port := next
		used[port] = true
		if _, err := h.db.ExecContext(ctx,
			`INSERT INTO uuid_port_map (server_id, uuid, port, protocol) VALUES (?, ?, ?, '')
			 ON DUPLICATE KEY UPDATE port = VALUES(port)`, serverID, u, port); err != nil {
			return out, err
		}
		out[u] = port
	}
	return out, nil
}
