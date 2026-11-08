package vpn

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

// DeviceRecord 是 vpn_devices 的一行。
type DeviceRecord struct {
	ID        int64     `json:"id"`
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	Platform  string    `json:"platform"`
	PubKey    string    `json:"device_pubkey"`
	IPv4      string    `json:"ip4"`
	State     string    `json:"state"`
	LastSeen  time.Time `json:"last_seen"`
	CreatedAt time.Time `json:"created_at"`
}

const deviceColumns = `id, uuid, name, platform, device_pubkey, ip4, state,
	last_seen, created_at, revoked_at`

func scanDevice(sc interface{ Scan(dest ...any) error }) (*DeviceRecord, error) {
	var d DeviceRecord
	var lastSeen, created, revoked int64
	if err := sc.Scan(&d.ID, &d.UUID, &d.Name, &d.Platform, &d.PubKey, &d.IPv4,
		&d.State, &lastSeen, &created, &revoked); err != nil {
		return nil, err
	}
	d.LastSeen, d.CreatedAt = fromMS(lastSeen), fromMS(created)
	return &d, nil
}

// ListDevices 返回用户设备。
func (s *Store) ListDevices(ctx context.Context, uuid string) ([]DeviceRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deviceColumns+` FROM vpn_devices WHERE uuid = ? ORDER BY id DESC`, uuid)
	if err != nil {
		return nil, fmt.Errorf("查询设备: %w", err)
	}
	defer rows.Close()
	var out []DeviceRecord
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// GetDevice 按 ID 查询设备。
func (s *Store) GetDevice(ctx context.Context, id int64) (*DeviceRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM vpn_devices WHERE id = ?`, id)
	d, err := scanDevice(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询设备: %w", err)
	}
	return d, nil
}

// CreateDevice 新增设备。
func (s *Store) CreateDevice(ctx context.Context, uuid, name, platform, pubKey string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_devices (uuid, name, platform, device_pubkey, state, last_seen, created_at, revoked_at)
		 VALUES (?, ?, ?, ?, 'active', 0, ?, 0)`,
		uuid, name, platform, pubKey, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("创建设备: %w", err)
	}
	return res.LastInsertId()
}

// RevokeDevice 吊销设备。
func (s *Store) RevokeDevice(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_devices SET state = 'revoked', revoked_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("吊销设备: %w", err)
	}
	return nil
}

// TouchDevice 更新设备最后活跃时间与 IP。
func (s *Store) TouchDevice(ctx context.Context, id int64, ip4 string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_devices SET last_seen = ?, ip4 = ? WHERE id = ?`,
		time.Now().UnixMilli(), ip4, id)
	if err != nil {
		return fmt.Errorf("更新设备: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 设备码
// ---------------------------------------------------------------------------

// CreateDeviceCode 写入设备码。
func (s *Store) CreateDeviceCode(ctx context.Context, deviceCode, userCode string, expires time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_device_codes (device_code, user_code, uuid, approved, expires_at, created_at)
		 VALUES (?, ?, '', 0, ?, ?)`,
		deviceCode, userCode, ms(expires), time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("写设备码: %w", err)
	}
	return nil
}

// ApproveDeviceCode 由用户在门户确认设备码。
func (s *Store) ApproveDeviceCode(ctx context.Context, userCode, uuid string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE vpn_device_codes SET approved = 1, uuid = ? WHERE user_code = ? AND expires_at > ?`,
		uuid, userCode, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("确认设备码: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetDeviceCode 查询设备码状态。
func (s *Store) GetDeviceCode(ctx context.Context, deviceCode string) (uuid string, approved bool, expires time.Time, err error) {
	var exp int64
	var ok int
	err = s.db.QueryRowContext(ctx,
		`SELECT uuid, approved, expires_at FROM vpn_device_codes WHERE device_code = ?`, deviceCode).
		Scan(&uuid, &ok, &exp)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, time.Time{}, ErrNotFound
	}
	if err != nil {
		return "", false, time.Time{}, fmt.Errorf("查询设备码: %w", err)
	}
	return uuid, ok == 1, fromMS(exp), nil
}

// DeleteDeviceCode 删除设备码（换取令牌后）。
func (s *Store) DeleteDeviceCode(ctx context.Context, deviceCode string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM vpn_device_codes WHERE device_code = ?`, deviceCode)
	return err
}

// ---------------------------------------------------------------------------
// 会话与 IP
// ---------------------------------------------------------------------------

// CreateSession 写入一条会话，返回 id。
func (s *Store) CreateSession(ctx context.Context, deviceID, nodeID, grantID int64, ip4, clientIP string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_sessions (device_id, node_id, grant_id, ip4, ip6, client_ip,
		   started_at, ended_at, bytes_tx, bytes_rx, packets, close_reason)
		 VALUES (?, ?, ?, ?, '', ?, ?, 0, 0, 0, 0, '')`,
		deviceID, nodeID, grantID, ip4, clientIP, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("创建会话: %w", err)
	}
	return res.LastInsertId()
}

// CloseSession 结束会话。
func (s *Store) CloseSession(ctx context.Context, id int64, reason string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_sessions SET ended_at = ?, close_reason = ? WHERE id = ? AND ended_at = 0`,
		time.Now().UnixMilli(), reason, id)
	if err != nil {
		return fmt.Errorf("结束会话: %w", err)
	}
	return nil
}

// LastSessionIP 返回设备在某节点最近一次分配的 IP。
func (s *Store) LastSessionIP(ctx context.Context, deviceID, nodeID int64) (string, error) {
	var ip string
	err := s.db.QueryRowContext(ctx,
		`SELECT ip4 FROM vpn_sessions WHERE device_id = ? AND node_id = ? AND ip4 <> ''
		 ORDER BY id DESC LIMIT 1`, deviceID, nodeID).Scan(&ip)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return ip, nil
}

// ActiveSessionCount 统计用户在某节点上的活跃会话数。
func (s *Store) ActiveSessionCount(ctx context.Context, nodeID int64, uuid string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vpn_sessions s
		   JOIN vpn_devices d ON d.id = s.device_id
		  WHERE s.node_id = ? AND s.ended_at = 0 AND d.uuid = ?`, nodeID, uuid).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计并发会话: %w", err)
	}
	return n, nil
}

// AllocateIP 从节点客户端子网中分配一个该设备可用地址：
// 优先复用该设备在此节点的历史 IP，否则从 .10 起找空闲地址。
func (s *Store) AllocateIP(ctx context.Context, nodeID, deviceID int64, cidr string) (string, error) {
	if last, err := s.LastSessionIP(ctx, deviceID, nodeID); err == nil && last != "" {
		var used int
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM vpn_sessions WHERE node_id = ? AND ip4 = ? AND ended_at = 0`,
			nodeID, last).Scan(&used)
		if used == 0 {
			return last, nil
		}
	}

	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("客户端子网 %q 非法: %w", cidr, err)
	}
	base := ipnet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("仅支持 IPv4 客户端子网，得到 %q", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	if bits != 32 {
		return "", fmt.Errorf("子网掩码非法: %q", cidr)
	}
	size := 1 << (bits - ones)
	start := 10
	if size <= start+2 {
		start = 1
	}
	baseNum := binary.BigEndian.Uint32(base)
	for i := start; i < size-1; i++ {
		ip := make(net.IP, 4)
		binary.BigEndian.PutUint32(ip, baseNum+uint32(i))
		candidate := ip.String()
		var used int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM vpn_sessions WHERE node_id = ? AND ip4 = ? AND ended_at = 0`,
			nodeID, candidate).Scan(&used); err != nil {
			return "", err
		}
		if used == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("节点 %d 的客户端地址池已耗尽", nodeID)
}


