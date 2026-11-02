package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const nodeGroupMaxNodes = 500

var placeholderRe = regexp.MustCompile(`\{\{\s*([A-Z0-9_]+)\s*\}\}`)

// renderNodeContent 替换 {{PLACEHOLDER}}；未定义的占位符原样保留。
func renderNodeContent(content string, vars map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(content, func(all string) string {
		m := placeholderRe.FindStringSubmatch(all)
		if len(m) != 2 {
			return all
		}
		if v, ok := vars[m[1]]; ok {
			return v
		}
		return all
	})
}

type nodeDeployConfig struct {
	ID              int64
	Name            string
	ServerID        int64
	Engine          string
	TargetPath      string
	ServiceName     string
	RestartCmd      string
	CheckCmd        string
	Content         string
	Backup          int64
	Enabled         int64
	Version         int64
	Applied         int64
	TemplateID      int64
	UUID            string
	ContentHash     string
	InstallVersions string
}

type nodeTemplateRow struct {
	ID          int64
	Name        string
	Engine      string
	TargetPath  string
	ServiceName string
	RestartCmd  string
	CheckCmd    string
	Content     string
	Backup      int64
}

func (h *handlers) loadNodeConfig(ctx context.Context, id int64) (*nodeDeployConfig, error) {
	var c nodeDeployConfig
	err := h.db.QueryRowContext(ctx, `SELECT id, COALESCE(name,''), COALESCE(server_id,0), COALESCE(engine,'other'),
	      COALESCE(target_path,''), COALESCE(service_name,''), COALESCE(restart_cmd,''), COALESCE(check_cmd,''),
	      COALESCE(content,''), COALESCE(backup,1), COALESCE(enabled,1), COALESCE(version,0),
	      COALESCE(applied_version,0), COALESCE(template_id,0), COALESCE(uuid,''), COALESCE(content_hash,''), COALESCE(install_versions,'')
	    FROM node_configs WHERE id = ? LIMIT 1`, id).
		Scan(&c.ID, &c.Name, &c.ServerID, &c.Engine, &c.TargetPath, &c.ServiceName, &c.RestartCmd,
			&c.CheckCmd, &c.Content, &c.Backup, &c.Enabled, &c.Version, &c.Applied,
			&c.TemplateID, &c.UUID, &c.ContentHash, &c.InstallVersions)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (h *handlers) listNodeConfigs(ctx context.Context, serverID int64) ([]nodeDeployConfig, error) {
	rows, err := h.db.QueryContext(ctx, `SELECT id, COALESCE(name,''), COALESCE(server_id,0), COALESCE(engine,'other'),
	      COALESCE(target_path,''), COALESCE(service_name,''), COALESCE(restart_cmd,''), COALESCE(check_cmd,''),
	      COALESCE(content,''), COALESCE(backup,1), COALESCE(enabled,1), COALESCE(version,0),
	      COALESCE(applied_version,0), COALESCE(template_id,0), COALESCE(uuid,''), COALESCE(content_hash,''), COALESCE(install_versions,'')
	    FROM node_configs WHERE server_id = ? AND enabled = 1 ORDER BY id ASC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []nodeDeployConfig
	for rows.Next() {
		var c nodeDeployConfig
		if err := rows.Scan(&c.ID, &c.Name, &c.ServerID, &c.Engine, &c.TargetPath, &c.ServiceName,
			&c.RestartCmd, &c.CheckCmd, &c.Content, &c.Backup, &c.Enabled, &c.Version,
			&c.Applied, &c.TemplateID, &c.UUID, &c.ContentHash, &c.InstallVersions); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (h *handlers) loadNodeTemplate(ctx context.Context, id int64) *nodeTemplateRow {
	if id <= 0 {
		return nil
	}
	var t nodeTemplateRow
	err := h.db.QueryRowContext(ctx, `SELECT id, COALESCE(name,''), COALESCE(engine,'other'),
	      COALESCE(target_path,''), COALESCE(service_name,''), COALESCE(restart_cmd,''),
	      COALESCE(check_cmd,''), COALESCE(content,''), COALESCE(backup,1)
	    FROM node_templates WHERE id = ? LIMIT 1`, id).
		Scan(&t.ID, &t.Name, &t.Engine, &t.TargetPath, &t.ServiceName, &t.RestartCmd,
			&t.CheckCmd, &t.Content, &t.Backup)
	if err != nil {
		return nil
	}
	return &t
}

func serverHostOf(s *agentServer) string {
	if strings.TrimSpace(s.Host) != "" {
		return strings.TrimSpace(s.Host)
	}
	if s.LastIP != "" && s.LastIP != "unknown" {
		return s.LastIP
	}
	if s.LastMetrics != "" {
		var m map[string]any
		if json.Unmarshal([]byte(s.LastMetrics), &m) == nil {
			if h, ok := m["hostname"].(string); ok && h != "" {
				return h
			}
		}
	}
	return ""
}

func (h *handlers) nodeConfigVars(cfg *nodeDeployConfig, srv *agentServer) map[string]string {
	now := time.Now().UTC()
	return map[string]string{
		"SERVER_ID":    fmt.Sprint(srv.ID),
		"SERVER_NAME":  srv.Name,
		"SERVER_HOST":  serverHostOf(srv),
		"SERVER_PORT":  fmt.Sprint(clampInt(srv.Port, 1, 65535)),
		"SERVER_USER":  srv.Username,
		"SERVER_TAGS":  srv.Tags,
		"SERVER_OS":    srv.OSInfo,
		"CONFIG_ID":    fmt.Sprint(cfg.ID),
		"CONFIG_NAME":  cfg.Name,
		"TARGET_PATH":  cfg.TargetPath,
		"SERVICE_NAME": cfg.ServiceName,
		"ENGINE":       firstNonEmpty(cfg.Engine, "other"),
		"NOW":          now.Format("2006-01-02T15:04:05.000Z"),
		"DATE":         now.Format("2006-01-02"),
		"TIMESTAMP":    fmt.Sprint(now.Unix()),
	}
}

// nodeGroupVars 构建配置分组注入变量（NODES / PROXIES_JSON / PROXIES_YAML 等）。
func (h *handlers) nodeGroupVars(ctx context.Context, uuid string) map[string]string {
	out := map[string]string{
		"UUID": uuid, "GROUP_NAME": "", "NODE_COUNT": "0", "NODE_COUNT_RAW": "0",
		"NODES": "", "NODES_B64": "", "PROXIES_JSON": "[]", "PROXIES_YAML": "", "SKIPPED_COUNT": "0",
	}
	if strings.TrimSpace(uuid) == "" {
		return out
	}
	var owner, remark sql.NullString
	if err := h.db.QueryRowContext(ctx,
		`SELECT owner, remark FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).Scan(&owner, &remark); err == nil {
		out["GROUP_NAME"] = firstNonEmpty(remark.String, owner.String)
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT config_data FROM configs WHERE uuid = ? ORDER BY id ASC`, uuid)
	if err != nil {
		return out
	}
	defer rows.Close()
	var items []string
	seen := map[string]bool{}
	for rows.Next() {
		var raw sql.NullString
		if rows.Scan(&raw) != nil {
			continue
		}
		s := strings.TrimSpace(raw.String)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		items = append(items, s)
		if len(items) >= nodeGroupMaxNodes {
			break
		}
	}

	out["NODE_COUNT_RAW"] = fmt.Sprint(len(items))
	out["NODE_COUNT"] = fmt.Sprint(len(items))
	nodes := strings.Join(items, "\n")
	out["NODES"] = nodes
	out["NODES_B64"] = base64.StdEncoding.EncodeToString([]byte(nodes))

	proxies := make([]map[string]any, 0, len(items))
	skipped := 0
	for _, raw := range items {
		protocol := getProtocol(raw)
		if clashUnsupportedReason(raw, protocol) != "" {
			skipped++
			continue
		}
		p := convertToClashFormat(raw)
		if p == nil {
			skipped++
			continue
		}
		proxies = append(proxies, p)
	}
	out["SKIPPED_COUNT"] = fmt.Sprint(skipped)
	if jsonBytes, err := json.MarshalIndent(proxies, "", "  "); err == nil {
		out["PROXIES_JSON"] = string(jsonBytes)
	}
	if len(proxies) > 0 {
		lines := make([]string, 0, len(proxies))
		for _, p := range proxies {
			lines = append(lines, "  - "+canonicalJSON(p))
		}
		out["PROXIES_YAML"] = strings.Join(lines, "\n")
	}
	return out
}

type nodeDeploy struct {
	Content     string
	Fingerprint string
	Engine      string
	TargetPath  string
	ServiceName string
	RestartCmd  string
	CheckCmd    string
	Backup      int64
	GroupVars   map[string]string
	TemplateID  int64
}

func (h *handlers) buildNodeDeploy(ctx context.Context, cfg *nodeDeployConfig, srv *agentServer, tpl *nodeTemplateRow) nodeDeploy {
	engine := cfg.Engine
	if engine == "" || engine == "other" {
		engine = "other"
		if tpl != nil && tpl.Engine != "" {
			engine = tpl.Engine
		}
	}
	merged := &nodeDeployConfig{
		ID: cfg.ID, Name: cfg.Name, Engine: engine,
		TargetPath:  firstNonEmpty(strings.TrimSpace(cfg.TargetPath), templateField(tpl, func(t *nodeTemplateRow) string { return t.TargetPath })),
		ServiceName: firstNonEmpty(strings.TrimSpace(cfg.ServiceName), templateField(tpl, func(t *nodeTemplateRow) string { return t.ServiceName })),
		RestartCmd:  firstNonEmpty(strings.TrimSpace(cfg.RestartCmd), templateField(tpl, func(t *nodeTemplateRow) string { return t.RestartCmd })),
		CheckCmd:    firstNonEmpty(strings.TrimSpace(cfg.CheckCmd), templateField(tpl, func(t *nodeTemplateRow) string { return t.CheckCmd })),
	}
	groupVars := h.nodeGroupVars(ctx, strings.TrimSpace(cfg.UUID))
	vars := h.nodeConfigVars(merged, srv)
	for k, v := range groupVars {
		vars[k] = v
	}

	body := cfg.Content
	if strings.TrimSpace(body) == "" && tpl != nil {
		body = tpl.Content
	}
	content := renderNodeContent(body, vars)
	checkCmd := renderNodeContent(merged.CheckCmd, vars)

	sum := sha256.Sum256([]byte(strings.Join([]string{
		content, merged.TargetPath, merged.ServiceName, merged.RestartCmd, checkCmd,
		merged.Engine, strings.TrimSpace(cfg.UUID), fmt.Sprint(cfg.TemplateID),
	}, "\x00")))
	return nodeDeploy{
		Content: content, Fingerprint: hex.EncodeToString(sum[:]),
		Engine: merged.Engine, TargetPath: merged.TargetPath, ServiceName: merged.ServiceName,
		RestartCmd: merged.RestartCmd, CheckCmd: checkCmd,
		Backup: cfg.Backup, GroupVars: groupVars, TemplateID: cfg.TemplateID,
	}
}

func templateField(tpl *nodeTemplateRow, pick func(*nodeTemplateRow) string) string {
	if tpl == nil {
		return ""
	}
	return strings.TrimSpace(pick(tpl))
}

func (h *handlers) agentBuildDeployPayload(ctx context.Context, cfg *nodeDeployConfig, srv *agentServer) map[string]any {
	tpl := h.loadNodeTemplate(ctx, cfg.TemplateID)
	merged := h.buildNodeDeploy(ctx, cfg, srv, tpl)
	sum := sha256.Sum256([]byte(merged.Content))
	return map[string]any{
		"name": cfg.Name, "path": merged.TargetPath, "content": merged.Content,
		"sha256": hex.EncodeToString(sum[:]), "version": cfg.Version,
		"backup": merged.Backup != 0, "service_name": merged.ServiceName,
		"restart_cmd": merged.RestartCmd, "check_cmd": merged.CheckCmd,
		"engine": merged.Engine, "template_id": merged.TemplateID,
		"uuid": cfg.UUID, "node_count": merged.GroupVars["NODE_COUNT_RAW"],
		"install_versions": cfg.InstallVersions,
	}
}
