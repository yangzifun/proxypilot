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
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	zw := zip.NewWriter(w)
	defer zw.Close()

	tables, err := h.backupTableNames(r)
	if err != nil {
		h.log.Error("备份：读取表清单失败", "err", err)
		return
	}

	manifest := map[string]any{
		"app": "proxypilot", "version": Version, "exported_at": now.Format(time.RFC3339),
		"tables": map[string]int64{}, "files": 0, "bucket": includeBucket, "agent_dist": includeAgent,
		"note": "数据库为 schema.sql + 每表 JSON；本地桶按相对路径打包；Secret 为加密存储值，恢复需相同 PP_JWT_SECRET / PP_AGENT_KEY_SECRET",
	}
	counts := map[string]int64{}

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
		n, err := h.backupTableJSON(r, zw, table)
		if err != nil {
			h.log.Warn("备份：导出表失败", "table", table, "err", err)
			continue
		}
		counts[table] = n
	}
	manifest["tables"] = counts

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

func (h *handlers) backupTableJSON(r *http.Request, zw *zip.Writer, table string) (int64, error) {
	rows, err := h.db.QueryContext(r.Context(), "SELECT * FROM `"+strings.ReplaceAll(table, "`", "``")+"`")
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	fw, err := zw.Create("db/" + table + ".json")
	if err != nil {
		return 0, err
	}
	if _, err := io.WriteString(fw, "[\n"); err != nil {
		return 0, err
	}
	var n int64
	first := true
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return n, err
		}
		obj := make(map[string]any, len(cols))
		for i, c := range cols {
			obj[c] = backupValue(vals[i])
		}
		b, err := json.Marshal(obj)
		if err != nil {
			continue
		}
		if !first {
			if _, err := io.WriteString(fw, ",\n"); err != nil {
				return n, err
			}
		}
		first = false
		if _, err := fw.Write(b); err != nil {
			return n, err
		}
		n++
	}
	_, err = io.WriteString(fw, "\n]\n")
	return n, err
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
