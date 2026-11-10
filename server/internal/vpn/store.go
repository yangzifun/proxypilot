package vpn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("vpn: 记录不存在")

// Store 封装 VPN 相关的 MySQL 读写。
type Store struct {
	db *sql.DB
}

// NewStore 创建 Store。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromMS(v int64) time.Time {
	if v <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}

// ---------------------------------------------------------------------------
// 设置
// ---------------------------------------------------------------------------

// LoadSettings 读取全局设置；不存在时返回默认值。
func (s *Store) LoadSettings(ctx context.Context) (Settings, error) {
	def := DefaultSettings()
	var enabled, fallback, datagram int
	var transport, serverMesh, serverDomain, meshCIDR, clientCIDR, fecMode string
	var mtu, ttl, fecGroup int
	var updated int64
	err := s.db.QueryRowContext(ctx,
		`SELECT enabled, management_transport, masque_fallback, server_mesh_ip4,
		        server_mesh_domain, mesh_cidr4, client_cidr4, mtu, token_ttl_seconds,
		        datagram_enabled, fec_mode, fec_group, updated_at
		   FROM vpn_settings WHERE id = 1`).
		Scan(&enabled, &transport, &fallback, &serverMesh, &serverDomain, &meshCIDR, &clientCIDR,
			&mtu, &ttl, &datagram, &fecMode, &fecGroup, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return def, fmt.Errorf("读取 vpn_settings: %w", err)
	}
	out := Settings{
		Enabled:             enabled == 1,
		ManagementTransport: Transport(transport),
		MasqueFallback:      fallback == 1,
		ServerMeshIPv4:      serverMesh,
		ServerMeshDomain:    serverDomain,
		MeshCIDR4:           meshCIDR,
		ClientCIDR4:         clientCIDR,
		MTU:                 mtu,
		TokenTTLSeconds:     ttl,
		TokenTTL:            time.Duration(ttl) * time.Second,
		DatagramEnabled:     datagram == 1,
		FECMode:             fecMode,
		FECGroup:            fecGroup,
	}
	if err := out.Normalize(); err != nil {
		return def, err
	}
	return out, nil
}

// SaveSettings 写入全局设置。
func (s *Store) SaveSettings(ctx context.Context, in Settings) error {
	if err := in.Normalize(); err != nil {
		return err
	}
	fallback := 0
	if in.MasqueFallback {
		fallback = 1
	}
	enabled := 0
	if in.Enabled {
		enabled = 1
	}
	datagram := 0
	if in.DatagramEnabled {
		datagram = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_settings
		   (id, enabled, management_transport, masque_fallback, server_mesh_ip4,
		    server_mesh_domain, mesh_cidr4, client_cidr4, mtu, token_ttl_seconds,
		    datagram_enabled, fec_mode, fec_group, updated_at)
		 VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   enabled = VALUES(enabled),
		   management_transport = VALUES(management_transport),
		   masque_fallback = VALUES(masque_fallback),
		   server_mesh_ip4 = VALUES(server_mesh_ip4),
		   server_mesh_domain = VALUES(server_mesh_domain),
		   mesh_cidr4 = VALUES(mesh_cidr4),
		   client_cidr4 = VALUES(client_cidr4),
		   mtu = VALUES(mtu),
		   token_ttl_seconds = VALUES(token_ttl_seconds),
		   datagram_enabled = VALUES(datagram_enabled),
		   fec_mode = VALUES(fec_mode),
		   fec_group = VALUES(fec_group),
		   updated_at = VALUES(updated_at)`,
		enabled, string(in.ManagementTransport), fallback, in.ServerMeshIPv4,
		in.ServerMeshDomain, in.MeshCIDR4, in.ClientCIDR4, in.MTU, in.TokenTTLSeconds,
		datagram, in.FECMode, in.FECGroup, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("写入 vpn_settings: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// 节点
// ---------------------------------------------------------------------------

// NodeRecord 是 vpn_nodes 的一行。
type NodeRecord struct {
	ID         int64     `json:"id"`
	ServerID   int64     `json:"server_id"`
	Name       string    `json:"name"`
	Domain     string    `json:"domain"`
	Region     string    `json:"region"`
	Endpoint   string    `json:"endpoint"`
	MeshIPv4   string    `json:"mesh_ip4"`
	ClientCIDR string    `json:"client_cidr4"`
	NodePubKey string    `json:"node_pubkey"`
	State      string    `json:"state"`
	ListenPort int       `json:"listen_port"`
	Capacity   int       `json:"capacity"`
	CertMode   string    `json:"cert_mode"`
	Version    string    `json:"version"`
	LastSeen   time.Time `json:"last_seen"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const nodeColumns = `id, server_id, name, domain, region, endpoint, mesh_ip4, client_cidr4,
	node_pubkey, state, listen_port, capacity, cert_mode, version, last_seen, created_at, updated_at`

func scanNode(sc interface {
	Scan(dest ...any) error
}) (*NodeRecord, error) {
	var n NodeRecord
	var lastSeen, created, updated int64
	err := sc.Scan(&n.ID, &n.ServerID, &n.Name, &n.Domain, &n.Region, &n.Endpoint, &n.MeshIPv4,
		&n.ClientCIDR, &n.NodePubKey, &n.State, &n.ListenPort, &n.Capacity,
		&n.CertMode, &n.Version, &lastSeen, &created, &updated)
	if err != nil {
		return nil, err
	}
	n.LastSeen, n.CreatedAt, n.UpdatedAt = fromMS(lastSeen), fromMS(created), fromMS(updated)
	return &n, nil
}

// ListNodes 返回全部节点。
func (s *Store) ListNodes(ctx context.Context) ([]NodeRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM vpn_nodes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询 vpn_nodes: %w", err)
	}
	defer rows.Close()
	out := make([]NodeRecord, 0)
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
}

// GetNode 按 ID 查询节点。
func (s *Store) GetNode(ctx context.Context, id int64) (*NodeRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM vpn_nodes WHERE id = ?`, id)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询节点: %w", err)
	}
	return n, nil
}

// GetNodeByServer 按 server_id 查询节点（Agent 注册用）。
func (s *Store) GetNodeByServer(ctx context.Context, serverID int64) (*NodeRecord, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM vpn_nodes WHERE server_id = ?`, serverID)
	n, err := scanNode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询节点: %w", err)
	}
	return n, nil
}

// CreateNode 新增节点，返回 ID。
func (s *Store) CreateNode(ctx context.Context, n NodeRecord) (int64, error) {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_nodes
		   (server_id, name, domain, region, endpoint, mesh_ip4, client_cidr4, node_pubkey,
		    state, listen_port, capacity, cert_mode, version, last_seen, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ServerID, n.Name, n.Domain, n.Region, n.Endpoint, n.MeshIPv4, n.ClientCIDR, n.NodePubKey,
		n.State, n.ListenPort, n.Capacity, n.CertMode, n.Version, 0, now, now)
	if err != nil {
		return 0, fmt.Errorf("创建节点: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

// UpdateNode 更新节点可编辑字段。
func (s *Store) UpdateNode(ctx context.Context, n NodeRecord) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_nodes SET name = ?, domain = ?, region = ?, endpoint = ?, mesh_ip4 = ?,
		   client_cidr4 = ?, node_pubkey = ?, state = ?, listen_port = ?, capacity = ?,
		   cert_mode = ?, updated_at = ?
		 WHERE id = ?`,
		n.Name, n.Domain, n.Region, n.Endpoint, n.MeshIPv4, n.ClientCIDR, n.NodePubKey, n.State,
		n.ListenPort, n.Capacity, n.CertMode, time.Now().UnixMilli(), n.ID)
	if err != nil {
		return fmt.Errorf("更新节点: %w", err)
	}
	return nil
}

// DeleteNode 删除节点。
func (s *Store) DeleteNode(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM vpn_nodes WHERE id = ?`, id); err != nil {
		return fmt.Errorf("删除节点: %w", err)
	}
	return nil
}

// SetNodeState 修改节点状态。
func (s *Store) SetNodeState(ctx context.Context, id int64, state string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_nodes SET state = ?, updated_at = ? WHERE id = ?`,
		state, time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("更新节点状态: %w", err)
	}
	return nil
}

// TouchNode 更新心跳时间与版本。
func (s *Store) TouchNode(ctx context.Context, id int64, version string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_nodes SET last_seen = ?, version = ? WHERE id = ?`,
		time.Now().UnixMilli(), version, id)
	if err != nil {
		return fmt.Errorf("更新节点心跳: %w", err)
	}
	return nil
}

// UpsertNodeRegistration 供 Agent 注册：按 server_id 创建或更新节点。
func (s *Store) UpsertNodeRegistration(ctx context.Context, serverID int64, name, endpoint, pubKey, version string) (*NodeRecord, error) {
	n, err := s.GetNodeByServer(ctx, serverID)
	if errors.Is(err, ErrNotFound) {
		id, cerr := s.CreateNode(ctx, NodeRecord{
			ServerID:   serverID,
			Name:       name,
			Endpoint:   endpoint,
			NodePubKey: pubKey,
			State:      "pending",
			ListenPort: 443,
			Capacity:   500,
			CertMode:   "internal_ca",
			Version:    version,
		})
		if cerr != nil {
			return nil, cerr
		}
		return s.GetNode(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	n.Name = name
	n.Endpoint = endpoint
	n.NodePubKey = pubKey
	n.Version = version
	if err := s.UpdateNode(ctx, *n); err != nil {
		return nil, err
	}
	return s.GetNode(ctx, n.ID)
}

// ---------------------------------------------------------------------------
// 授权绑定
// ---------------------------------------------------------------------------

// GrantRecord 是 vpn_node_grants 的一行。
type GrantRecord struct {
	ID         int64     `json:"id"`
	NodeID     int64     `json:"node_id"`
	SubjectID  string    `json:"subject_id"`
	Enabled    bool      `json:"enabled"`
	ExpiresAt  time.Time `json:"expires_at"`
	MaxDevices int       `json:"max_devices"`
	QuotaBytes int64     `json:"quota_bytes"`
	UsedBytes  int64     `json:"used_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

// ListGrants 返回某节点的绑定列表。
func (s *Store) ListGrants(ctx context.Context, nodeID int64) ([]GrantRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, node_id, subject_id, enabled, expires_at, max_devices,
		        quota_bytes, used_bytes, created_at
		   FROM vpn_node_grants WHERE node_id = ? ORDER BY id`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("查询节点绑定: %w", err)
	}
	defer rows.Close()
	var out []GrantRecord
	for rows.Next() {
		var g GrantRecord
		var enabled int
		var exp, created int64
		if err := rows.Scan(&g.ID, &g.NodeID, &g.SubjectID, &enabled, &exp,
			&g.MaxDevices, &g.QuotaBytes, &g.UsedBytes, &created); err != nil {
			return nil, err
		}
		g.Enabled = enabled == 1
		g.ExpiresAt, g.CreatedAt = fromMS(exp), fromMS(created)
		out = append(out, g)
	}
	return out, rows.Err()
}

// CreateGrant 新增绑定。
func (s *Store) CreateGrant(ctx context.Context, nodeID int64, subjectID string, expiresAt time.Time, maxDevices int, quotaBytes int64, actor string) (int64, error) {
	if maxDevices <= 0 {
		maxDevices = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_node_grants
		   (node_id, subject_type, subject_id, enabled, expires_at, max_devices,
		    quota_bytes, used_bytes, created_by, created_at, revoked_at)
		 VALUES (?, 'user', ?, 1, ?, ?, ?, 0, ?, ?, 0)
		 ON DUPLICATE KEY UPDATE
		   enabled = 1, expires_at = VALUES(expires_at), max_devices = VALUES(max_devices),
		   quota_bytes = VALUES(quota_bytes), revoked_at = 0, created_by = VALUES(created_by)`,
		nodeID, subjectID, ms(expiresAt), maxDevices, quotaBytes, actor, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("创建节点绑定: %w", err)
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		// 唯一键命中，走 UPDATE：取回已有记录 ID。
		_ = s.db.QueryRowContext(ctx,
			`SELECT id FROM vpn_node_grants WHERE node_id = ? AND subject_type = 'user' AND subject_id = ? LIMIT 1`,
			nodeID, subjectID).Scan(&id)
	}
	return id, nil
}

// RevokeGrant 解绑（停用并记录时间）。
func (s *Store) RevokeGrant(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_node_grants SET enabled = 0, revoked_at = ? WHERE id = ?`,
		time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("解绑: %w", err)
	}
	return nil
}

// GetGrant 查询用户在某节点的有效绑定。
func (s *Store) GetGrant(ctx context.Context, nodeID int64, userUUID string) (*GrantRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, node_id, subject_id, enabled, expires_at, max_devices,
		        quota_bytes, used_bytes, created_at
		   FROM vpn_node_grants
		  WHERE node_id = ? AND subject_id = ? AND subject_type = 'user' AND enabled = 1
		  ORDER BY id DESC LIMIT 1`, nodeID, userUUID)
	var g GrantRecord
	var enabled int
	var exp, created int64
	err := row.Scan(&g.ID, &g.NodeID, &g.SubjectID, &enabled, &exp,
		&g.MaxDevices, &g.QuotaBytes, &g.UsedBytes, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询绑定: %w", err)
	}
	g.Enabled = enabled == 1
	g.ExpiresAt, g.CreatedAt = fromMS(exp), fromMS(created)
	return &g, nil
}

// ---------------------------------------------------------------------------
// 用户资格
// ---------------------------------------------------------------------------

// AccessRecord 是 vpn_user_access 的一行。
type AccessRecord struct {
	UUID       string
	Enabled    bool
	ExpiresAt  time.Time
	MaxDevices int
	QuotaBytes int64
	UsedBytes  int64
	Note       string
}

// GetAccess 查询用户 VPN 资格；不存在返回 ErrNotFound。
func (s *Store) GetAccess(ctx context.Context, uuid string) (*AccessRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT uuid, enabled, expires_at, max_devices, quota_bytes, used_bytes, note
		   FROM vpn_user_access WHERE uuid = ?`, uuid)
	var a AccessRecord
	var enabled int
	var exp int64
	err := row.Scan(&a.UUID, &enabled, &exp, &a.MaxDevices, &a.QuotaBytes, &a.UsedBytes, &a.Note)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户资格: %w", err)
	}
	a.Enabled = enabled == 1
	a.ExpiresAt = fromMS(exp)
	return &a, nil
}

// UpsertAccess 写入用户 VPN 资格。
func (s *Store) UpsertAccess(ctx context.Context, a AccessRecord, actor string) error {
	enabled := 0
	if a.Enabled {
		enabled = 1
	}
	if a.MaxDevices <= 0 {
		a.MaxDevices = 3
	}
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vpn_user_access
		   (uuid, enabled, expires_at, max_devices, quota_bytes, used_bytes, note,
		    created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   enabled = VALUES(enabled), expires_at = VALUES(expires_at),
		   max_devices = VALUES(max_devices), quota_bytes = VALUES(quota_bytes),
		   note = VALUES(note), updated_at = VALUES(updated_at)`,
		a.UUID, enabled, ms(a.ExpiresAt), a.MaxDevices, a.QuotaBytes, a.Note, actor, now, now)
	if err != nil {
		return fmt.Errorf("写入用户资格: %w", err)
	}
	return nil
}

// CountUserDevices 统计用户设备数（未吊销）。
func (s *Store) CountUserDevices(ctx context.Context, uuid string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM vpn_devices WHERE uuid = ? AND state = 'active'`, uuid).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("统计用户设备: %w", err)
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// 可见节点（用户端）
// ---------------------------------------------------------------------------

// VisibleNode 是用户端节点列表的一行（含绑定信息）。
type VisibleNode struct {
	Node       NodeRecord
	GrantID    int64
	ExpiresAt  time.Time
	MaxDevices int
	QuotaBytes int64
	UsedBytes  int64
}

// ListVisibleNodes 返回该用户可见的节点：资格有效 + 绑定有效 + 节点 active。
func (s *Store) ListVisibleNodes(ctx context.Context, userUUID string, now time.Time) ([]VisibleNode, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+`n.id, n.server_id, n.name, n.region, n.endpoint, n.mesh_ip4,
		        n.client_cidr4, n.node_pubkey, n.state, n.listen_port, n.capacity,
		        n.cert_mode, n.version, n.last_seen, n.created_at, n.updated_at,
		        g.id, g.expires_at, g.max_devices, g.quota_bytes, g.used_bytes`+`
		   FROM vpn_nodes n
		   JOIN vpn_user_access a ON a.uuid = ?
		   JOIN vpn_node_grants g ON g.node_id = n.id AND g.subject_id = ?
		        AND g.subject_type = 'user' AND g.enabled = 1
		  WHERE n.state = 'active'
		    AND a.enabled = 1
		    AND (a.expires_at = 0 OR a.expires_at > ?)
		    AND (a.quota_bytes = 0 OR a.used_bytes < a.quota_bytes)
		    AND (g.expires_at = 0 OR g.expires_at > ?)
		    AND (g.quota_bytes = 0 OR g.used_bytes < g.quota_bytes)
		  ORDER BY n.id`, userUUID, userUUID, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("查询可见节点: %w", err)
	}
	defer rows.Close()

	out := make([]VisibleNode, 0)
	for rows.Next() {
		var v VisibleNode
		var lastSeen, created, updated, exp int64
		var enabled int
		if err := rows.Scan(
			&v.Node.ID, &v.Node.ServerID, &v.Node.Name, &v.Node.Region, &v.Node.Endpoint,
			&v.Node.MeshIPv4, &v.Node.ClientCIDR, &v.Node.NodePubKey, &v.Node.State,
			&v.Node.ListenPort, &v.Node.Capacity, &v.Node.CertMode, &v.Node.Version,
			&lastSeen, &created, &updated,
			&v.GrantID, &exp, &v.MaxDevices, &v.QuotaBytes, &v.UsedBytes,
		); err != nil {
			return nil, err
		}
		_ = enabled
		v.Node.LastSeen = fromMS(lastSeen)
		v.Node.CreatedAt = fromMS(created)
		v.Node.UpdatedAt = fromMS(updated)
		v.ExpiresAt = fromMS(exp)
		out = append(out, v)
	}
	return out, rows.Err()
}





// CloseDeviceSessions 关闭该设备在指定节点上仍然「未结束」的会话。
// 客户端异常退出（进程被杀、断网）时不会调用 disconnect，旧会话会一直占着
// 并发名额，导致同一设备再也连不上——重连前先把它收掉。
func (s *Store) CloseDeviceSessions(ctx context.Context, deviceID, nodeID int64, reason string) error {
	now := time.Now().UnixMilli()
	_, err := s.db.ExecContext(ctx,
		`UPDATE vpn_sessions SET ended_at = ?, close_reason = ?
		  WHERE device_id = ? AND node_id = ? AND ended_at = 0`,
		now, reason, deviceID, nodeID)
	return err
}

// ReapStaleSessions 把超过 ttl 仍未结束的会话标记为超时结束，避免并发名额被永久占用。
func (s *Store) ReapStaleSessions(ctx context.Context, ttl time.Duration) (int64, error) {
	cutoff := time.Now().Add(-ttl).UnixMilli()
	res, err := s.db.ExecContext(ctx,
		`UPDATE vpn_sessions SET ended_at = ?, close_reason = 'stale'
		  WHERE ended_at = 0 AND started_at < ?`, time.Now().UnixMilli(), cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
