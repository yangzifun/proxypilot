package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (h *handlers) bucketDir() string {
	dir := strings.TrimSpace(h.cfg.BucketDir)
	if dir == "" {
		dir = "server/data/bucket"
	}
	return dir
}

func safeBucketKey(key string) (string, bool) {
	k := strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	k = strings.TrimPrefix(k, "/")
	if k == "" || strings.Contains(k, "..") || strings.HasPrefix(k, "/") {
		return "", false
	}
	return k, true
}

// GET /api/bucket/files —— 列出本地桶文件
func (h *handlers) bucketFiles(w http.ResponseWriter, r *http.Request) {
	dir := h.bucketDir()
	entries := []map[string]any{}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		entries = append(entries, map[string]any{
			"key": rel, "size": info.Size(),
			"updated_at": info.ModTime().UnixMilli(),
			"url":        "/api/bucket/file?key=" + rel,
		})
		return nil
	})
	sort.Slice(entries, func(i, j int) bool {
		return mapStr(entries[i], "key", "") < mapStr(entries[j], "key", "")
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": entries, "total": len(entries)})
}

// GET /api/bucket/file?key=... / POST /api/bucket/file?key=...
func (h *handlers) bucketFile(w http.ResponseWriter, r *http.Request) {
	key, ok := safeBucketKey(r.URL.Query().Get("key"))
	if !ok {
		writeError(w, http.StatusBadRequest, "key 无效")
		return
	}
	full := filepath.Join(h.bucketDir(), filepath.FromSlash(key))
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		info, err := os.Stat(full)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, full)
	case http.MethodPost, http.MethodPut:
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, "创建目录失败")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
		if r.ContentLength > 64<<20 {
			writeError(w, http.StatusRequestEntityTooLarge, "文件过大")
			return
		}
		var src io.Reader = r.Body
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				writeError(w, http.StatusBadRequest, "上传解析失败")
				return
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				writeError(w, http.StatusBadRequest, "缺少 file 字段")
				return
			}
			defer f.Close()
			src = f
		}
		f, err := os.Create(full)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "写入失败")
			return
		}
		defer f.Close()
		n, err := io.Copy(f, io.LimitReader(src, 256<<20))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "写入失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "key": key, "size": n,
			"url": "/api/bucket/file?key=" + key, "updated_at": time.Now().UnixMilli(),
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}
