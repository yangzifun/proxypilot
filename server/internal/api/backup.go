package api

import (
	"archive/zip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GET /api/backup/export —— 一键导出：schema.sql + 全表 JSON + 本地桶（可选 Agent 二进制）
// 仅超管；流式 ZIP，不落临时文件。
func (h *handlers) backupExport(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "数据库未就绪")
		return
	}
	includeBucket := r.URL.Query().Get("bucket") != "0"
	includeAgent := r.URL.Query().Get("agent") == "1"
	includeLogs := r.URL.Query().Get("logs") != "0"

	now := time.Now().UTC()
	filename := "proxypilot-backup-" + strings.TrimPrefix(Version, "V") + "-" + now.Format("20060102-150405") + ".zip"
	password := strings.TrimSpace(r.URL.Query().Get("password"))
	encrypted := password != ""
	contentType := "application/zip"
	if encrypted {
		contentType = "application/octet-stream"
		filename = strings.TrimSuffix(filename, ".zip") + ".ppbak"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	var sink io.Writer = w
	var enc *backupEncryptWriter
	if encrypted {
		var eerr error
		enc, eerr = newBackupEncryptWriter(w, password)
		if eerr != nil {
			writeError(w, http.StatusInternalServerError, "初始化加密失败")
			return
		}
		sink = enc
	}
	zw := zip.NewWriter(sink)

	tables, err := h.backupTableNames(r)
	if err != nil {
		h.log.Error("备份：读取表清单失败", "err", err)
		return
	}

	manifest := map[string]any{
		"app": "proxypilot", "version": Version, "exported_at": now.Format(time.RFC3339),
		"tables": map[string]int64{}, "files": 0, "bucket": includeBucket, "agent_dist": includeAgent,
		"note": "数据库为 schema.sql + 每表 JSON；本地桶按相对路径打包；" +
			"jwt_secret / agent_key_secret 等密钥与 enc:v1 密文已剔除，恢复后需重新填写密钥类设置",
	}
	counts := map[string]int64{}
	redactedFields := int64(0)

	// 1) schema.sql
	sw, err := zw.Create("schema.sql")
	if err == nil {
		fmt.Fprintf(sw, "-- ProxyPilot %s backup %s\nSET NAMES utf8mb4;\n\n", Version, now.Format(time.RFC3339))
		for _, table := range tables {
			var name, ddl string
			if err := h.db.QueryRowContext(r.Context(), "SHOW CREATE TABLE `"+strings.ReplaceAll(table, "`", "``")+"`").Scan(&name, &ddl); err != nil {
				h.log.Warn("备份：读取表结构失败", "table", table, "err", err)
				continue
			}
			fmt.Fprintf(sw, "-- %s\n%s;\n\n", table, ddl)
		}
	}

	// 2) 每表 JSON
	for _, table := range tables {
		if !includeLogs && (table == "config_access_logs" || table == "node_push_logs") {
			continue
		}
		n, rf, err := h.backupTableJSON(r, zw, table)
		if err != nil {
			h.log.Warn("备份：导出表失败", "table", table, "err", err)
			continue
		}
		counts[table] = n
		redactedFields += rf
	}
	manifest["tables"] = counts
	manifest["redacted_fields"] = redactedFields

	// 3) 本地桶
	files := 0
	if includeBucket {
		files += h.backupDir(r.Context(), zw, h.cfg.BucketDir, "bucket")
	}
	if includeAgent {
		files += h.backupDir(r.Context(), zw, h.agentDistDir(), "agent-dist")
	}
	manifest["files"] = files

	// 4) manifest 最后写入（含统计）
	if mw, err := zw.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate}); err == nil {
		enc := json.NewEncoder(mw)
		enc.SetIndent("", "  ")
		_ = enc.Encode(manifest)
	}
	if err := zw.Close(); err != nil {
		h.log.Warn("备份：关闭 zip 失败", "err", err)
		return
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			h.log.Warn("备份：加密收尾失败", "err", err)
			return
		}
	}
	h.recordNodeLog(r.Context(), 0, 0, "backup-export", "success", 0,
		"导出备份 "+filename+"（表 "+itoa64(int64(len(counts)))+" 个，文件 "+itoa64(int64(files))+" 个）", "", "admin")
}

func (h *handlers) backupTableNames(r *http.Request) ([]string, error) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT TABLE_NAME FROM information_schema.tables
		  WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY TABLE_NAME`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && name != "" {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

// backupSecretSettingKeys 备份导出时必须剔除明文的 app_settings 键：
// jwt_secret / agent_key_secret 是令牌签名与密文加密的根（拿到即可伪造超管令牌、
// 解密其它密文）；其余为第三方凭证（SMTP/OIDC/Turnstile/Cloudflare/R2/DKIM）。
var backupSecretSettingKeys = map[string]bool{
	"jwt_secret":           true,
	"agent_key_secret":     true,
	"agent_token_enc":      true,
	settingSMTPPass:        true,
	settingOIDCSecret:      true,
	settingTurnstileSecret: true,
	settingCFToken:         true,
	settingR2SecretKey:     true,
	settingMailDKIMKey:     true,
}

// backupRedactRow 在导出时剔除密钥类字段，返回发生剔除的字段数：
//   - app_settings 中明文保存的签名密钥 / 加密根 / 第三方凭证；
//   - 任意表中形如 enc:v1:... 的密文（如 servers.agent_token_enc）。
//
// 说明：servers.agent_token_hash 是单向哈希、不可直接使用，保留它可让恢复后的节点
// 继续鉴权；vpn_certificates.key_pem 属于证书功能数据，不在本次「备份泄露即超管失陷」
// 的利用链上，仍由 superOnly + 文件权限保护。
func backupRedactRow(table string, obj map[string]any) int {
	n := 0
	if table == "app_settings" {
		if key, _ := obj["key"].(string); backupSecretSettingKeys[key] {
			if v, ok := obj["value"]; !ok || v != "" {
				obj["value"] = ""
				n++
			}
		}
	}
	for col, raw := range obj {
		s, ok := raw.(string)
		if !ok || s == "" {
			continue
		}
		if strings.HasPrefix(s, "enc:v") {
			obj[col] = ""
			n++
		}
	}
	return n
}

func (h *handlers) backupTableJSON(r *http.Request, zw *zip.Writer, table string) (int64, int64, error) {
	rows, err := h.db.QueryContext(r.Context(), "SELECT * FROM `"+strings.ReplaceAll(table, "`", "``")+"`")
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, 0, err
	}
	fw, err := zw.Create("db/" + table + ".json")
	if err != nil {
		return 0, 0, err
	}
	if _, err := io.WriteString(fw, "[\n"); err != nil {
		return 0, 0, err
	}
	var n, redacted int64
	first := true
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, redacted, err
		}
		obj := make(map[string]any, len(cols))
		for i, c := range cols {
			obj[c] = backupValue(vals[i])
		}
		redacted += int64(backupRedactRow(table, obj))
		b, err := json.Marshal(obj)
		if err != nil {
			continue
		}
		if !first {
			if _, err := io.WriteString(fw, ",\n"); err != nil {
				return n, redacted, err
			}
		}
		first = false
		if _, err := fw.Write(b); err != nil {
			return n, redacted, err
		}
		n++
	}
	_, err = io.WriteString(fw, "\n]\n")
	return n, redacted, err
}

func backupValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case time.Time:
		return x.Format(time.RFC3339Nano)
	case sql.RawBytes:
		return string(x)
	default:
		return v
	}
}

// backupDir 把目录内文件按相对路径写入 ZIP，返回文件数。
func (h *handlers) backupDir(ctx context.Context, zw *zip.Writer, dir, prefix string) int {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return 0
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0
	}
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		fw, err := zw.Create(prefix + "/" + filepath.ToSlash(rel))
		if err != nil {
			return nil
		}
		if _, err := io.Copy(fw, f); err == nil {
			count++
		}
		return nil
	})
	return count
}
