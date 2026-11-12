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
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var backupTableRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// backupImportForbiddenTables 导入时禁止整表替换的安全关键表：
//   - admin_users / admin_oidc_identities / mfa_backup_codes：整表替换可植入攻击者账号、
//     覆盖超管口令哈希或 MFA 状态，等于直接夺取唯一超管边界；
//   - app_settings：内含 jwt_secret / agent_key_secret，覆盖后可签发任意令牌、
//     解密其它密文（导出侧已剔除密钥，恢复时必须沿用本机密钥，不能从备份改写）。
//
// schema_migrations 另由版本管理排除，不参与导入。
var backupImportForbiddenTables = map[string]bool{
	"admin_users":           true,
	"admin_oidc_identities": true,
	"mfa_backup_codes":      true,
	"app_settings":          true,
}

// backupImportTableAllowed 判断某张表是否允许由备份导入覆盖。
func backupImportTableAllowed(table string) bool {
	if table == "schema_migrations" {
		return false
	}
	return !backupImportForbiddenTables[table]
}

// POST /api/backup/import —— 导入 backupExport 生成的 ZIP。
// 数据库先在一个事务里整表替换，桶文件在事务提交后覆盖写入。
func (h *handlers) backupImport(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusServiceUnavailable, "数据库未就绪")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<30)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少备份文件")
		return
	}
	defer file.Close()
	tmp, err := os.CreateTemp("", "proxypilot-import-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建临时文件失败")
		return
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()
	if _, err := io.Copy(tmp, file); err != nil {
		writeError(w, http.StatusBadRequest, "读取备份文件失败")
		return
	}
	zipName := tmpName
	if head := make([]byte, len(backupMagic)); func() bool {
		f, ferr := os.Open(tmpName)
		if ferr != nil {
			return false
		}
		defer f.Close()
		if _, rerr := io.ReadFull(f, head); rerr != nil {
			return false
		}
		return string(head) == backupMagic
	}() {
		pw := strings.TrimSpace(r.FormValue("password"))
		if pw == "" {
			writeError(w, http.StatusBadRequest, "备份已加密，请输入备份口令")
			return
		}
		dec, derr := os.CreateTemp("", "proxypilot-import-dec-*.zip")
		if derr != nil {
			writeError(w, http.StatusInternalServerError, "创建临时文件失败")
			return
		}
		decName := dec.Name()
		defer func() { dec.Close(); os.Remove(decName) }()
		srcF, oerr := os.Open(tmpName)
		if oerr != nil {
			writeError(w, http.StatusInternalServerError, "读取备份失败")
			return
		}
		derr = backupDecryptStream(srcF, dec, pw)
		srcF.Close()
		dec.Close()
		if derr != nil {
			writeError(w, http.StatusBadRequest, derr.Error())
			return
		}
		zipName = decName
	}
	zr, err := zip.OpenReader(zipName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "不是有效的 ZIP 备份")
		return
	}
	defer zr.Close()

	manifest, err := readBackupManifest(zr)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 校验所有路径，拒绝 zip slip，并限制总量/条目数/压缩比。
	if len(zr.File) > 3000 {
		writeError(w, http.StatusBadRequest, "备份条目过多")
		return
	}
	var totalUnzip uint64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			writeError(w, http.StatusBadRequest, "备份包含非法路径："+f.Name)
			return
		}
		if f.UncompressedSize64 > 1<<30 {
			writeError(w, http.StatusBadRequest, "备份内文件过大："+f.Name)
			return
		}
		totalUnzip += f.UncompressedSize64
		if totalUnzip > 2<<30 {
			writeError(w, http.StatusBadRequest, "备份解压总量超限")
			return
		}
		if f.CompressedSize64 > 0 && f.UncompressedSize64/f.CompressedSize64 > 200 {
			writeError(w, http.StatusBadRequest, "备份压缩比异常："+f.Name)
			return
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "开启事务失败")
		return
	}
	defer tx.Rollback()

	existing, err := backupExistingTables(r.Context(), tx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取数据库表失败")
		return
	}
	tables, rows := 0, int64(0)
	skipped := []string{}
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if !strings.HasPrefix(name, "db/") || !strings.HasSuffix(name, ".json") {
			continue
		}
		table := strings.TrimSuffix(strings.TrimPrefix(name, "db/"), ".json")
		if !backupTableRe.MatchString(table) {
			continue
		}
		if !existing[table] {
			continue
		}
		if !backupImportTableAllowed(table) {
			// 安全关键表一律跳过：备份不能改写账号 / 密钥 / 迁移版本。
			skipped = append(skipped, table)
			continue
		}
		n, err := importBackupTable(r.Context(), tx, table, f)
		if err != nil {
			h.log.Error("备份导入：表失败", "table", table, "err", err)
			writeError(w, http.StatusBadRequest, "导入表 "+table+" 失败："+err.Error())
			return
		}
		tables++
		rows += n
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "提交数据库事务失败")
		return
	}
	h.invalidateSettings()

	// 桶文件在数据库提交后写入；失败时数据库已恢复，可重试导入。
	files := 0
	if err := extractBackupDir(zr, "bucket/", h.bucketDir()); err != nil {
		writeError(w, http.StatusInternalServerError, "数据库已导入，但桶文件恢复失败："+err.Error())
		return
	} else {
		files += countBackupDir(zr, "bucket/")
	}
	if err := extractBackupDir(zr, "agent-dist/", h.agentDistDir()); err != nil {
		writeError(w, http.StatusInternalServerError, "数据库已导入，但 Agent 分发文件恢复失败："+err.Error())
		return
	} else {
		files += countBackupDir(zr, "agent-dist/")
	}

	h.recordNodeLog(r.Context(), 0, 0, "backup-import", "success", 0,
		fmt.Sprintf("导入备份 %s（表 %d 个，行 %d，文件 %d，跳过安全关键表 %d 个）", manifest, tables, rows, files, len(skipped)), "", "admin")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "version": manifest, "tables": tables, "rows": rows, "files": files,
		"skipped_tables": skipped,
	})
}

func readBackupManifest(zr *zip.ReadCloser) (string, error) {
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("读取 manifest 失败")
		}
		defer rc.Close()
		raw, err := io.ReadAll(io.LimitReader(rc, 1<<20))
		if err != nil {
			return "", fmt.Errorf("读取 manifest 失败")
		}
		var m struct {
			App     string `json:"app"`
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &m) != nil || m.App != "proxypilot" {
			return "", fmt.Errorf("不是 ProxyPilot 备份")
		}
		return firstNonEmpty(m.Version, "unknown"), nil
	}
	return "", fmt.Errorf("备份缺少 manifest.json")
}

func backupExistingTables(ctx context.Context, tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT TABLE_NAME FROM information_schema.tables
		  WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var t string
		if rows.Scan(&t) == nil {
			out[t] = true
		}
	}
	return out, rows.Err()
}

func importBackupTable(ctx context.Context, tx *sql.Tx, table string, f *zip.File) (int64, error) {
	// 防御性二次校验：即使调用方漏判，也绝不整表替换安全关键表。
	if !backupImportTableAllowed(table) {
		return 0, fmt.Errorf("表 %s 不允许从备份导入", table)
	}
	rc, err := f.Open()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	if _, err := tx.ExecContext(ctx, "DELETE FROM `"+table+"`"); err != nil {
		return 0, err
	}
	cols, err := tableColumns(ctx, tx, table)
	if err != nil {
		return 0, err
	}
	if len(cols) == 0 {
		return 0, nil
	}
	quoted := make([]string, len(cols))
	marks := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = "`" + c + "`"
		marks[i] = "?"
	}
	query := "INSERT INTO `" + table + "` (" + strings.Join(quoted, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	dec := json.NewDecoder(rc)
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('[') {
		return 0, fmt.Errorf("JSON 不是数组")
	}
	var n int64
	for dec.More() {
		var row map[string]any
		if err := dec.Decode(&row); err != nil {
			return n, err
		}
		args := make([]any, len(cols))
		for i, c := range cols {
			args[i] = backupImportValue(row[c])
		}
		if _, err := stmt.ExecContext(ctx, args...); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func tableColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT COLUMN_NAME FROM information_schema.columns
		  WHERE table_schema = DATABASE() AND table_name = ? ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if rows.Scan(&c) == nil {
			out = append(out, c)
		}
	}
	return out, rows.Err()
}

func backupImportValue(v any) any {
	switch x := v.(type) {
	case json.Number:
		return string(x)
	case []any:
		b, _ := json.Marshal(x)
		return string(b)
	case map[string]any:
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return v
	}
}

func extractBackupDir(zr *zip.ReadCloser, prefix, dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	base := filepath.Clean(dir)
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if !strings.HasPrefix(name, prefix) || strings.HasSuffix(name, "/") {
			continue
		}
		rel := strings.TrimPrefix(name, prefix)
		if rel == "" || path.IsAbs(rel) || strings.Contains(rel, "..") {
			continue
		}
		target := filepath.Join(base, filepath.FromSlash(rel))
		if !strings.HasPrefix(filepath.Clean(target), base+string(os.PathSeparator)) {
			return fmt.Errorf("非法路径 %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, io.LimitReader(rc, 2<<30))
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func countBackupDir(zr *zip.ReadCloser, prefix string) int {
	n := 0
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(name, prefix) && !strings.HasSuffix(name, "/") {
			n++
		}
	}
	return n
}
