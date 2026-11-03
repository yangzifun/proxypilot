package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
)

func safeObjectKey(key string) (string, bool) {
	k := strings.TrimSpace(strings.ReplaceAll(key, "\\", "/"))
	k = strings.TrimLeft(k, "/")
	if k == "" || len(k) > 1024 || strings.Contains(k, "..") || strings.ContainsAny(k, "\r\n\x00") {
		return "", false
	}
	return k, true
}

func normalizePrefix(prefix string) string {
	p := strings.TrimSpace(strings.ReplaceAll(prefix, "\\", "/"))
	p = strings.TrimLeft(p, "/")
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// GET /api/attachments/status
func (h *handlers) attachmentsStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "configured": cfg.Ready(), "bucket": cfg.Bucket,
		"endpoint": cfg.Endpoint, "account_id": cfg.AccountID,
		"cdn_base_url": h.effectiveCDNBase(r.Context()),
	})
}

// GET /api/attachments?prefix=&token=
func (h *handlers) attachmentsList(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置：请设置 r2_bucket / r2_access_key_id / r2_secret_access_key")
		return
	}
	prefix := normalizePrefix(r.URL.Query().Get("prefix"))
	res, err := newR2Client(cfg).list(r.Context(), prefix, r.URL.Query().Get("token"), 200)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	folders := make([]map[string]any, 0, len(res.Folders))
	for _, f := range res.Folders {
		name := strings.TrimPrefix(f.Prefix, prefix)
		name = strings.TrimSuffix(name, "/")
		folders = append(folders, map[string]any{"prefix": f.Prefix, "name": name})
	}
	files := make([]map[string]any, 0, len(res.Contents))
	for _, o := range res.Contents {
		// 目录占位对象（key 以 / 结尾）不计入文件列表
		if strings.HasSuffix(o.Key, "/") {
			continue
		}
		files = append(files, map[string]any{
			"key": o.Key, "name": path.Base(o.Key), "size": o.Size,
			"updated_at": o.LastModified, "etag": strings.Trim(o.ETag, `"`),
			"url":     "/api/attachments/download?key=" + url.QueryEscape(o.Key),
			"cdn_url": objectCDNURL(h.effectiveCDNBase(r.Context()), o.Key),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "prefix": prefix, "folders": folders, "files": files,
		"next_token": res.NextContinuationToken, "storage": "r2", "bucket": cfg.Bucket,
	})
}

// POST /api/attachments/upload?prefix=... (multipart: file, 可选 key)
func (h *handlers) attachmentsUpload(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "上传解析失败")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少 file 字段")
		return
	}
	defer file.Close()
	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		key = normalizePrefix(r.FormValue("prefix")) + header.Filename
	}
	key, ok := safeObjectKey(key)
	if !ok {
		writeError(w, http.StatusBadRequest, "key 无效")
		return
	}
	tmp, err := os.CreateTemp("", "pp-r2-upload-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建临时文件失败")
		return
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(file, 256<<20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取上传内容失败")
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		writeError(w, http.StatusInternalServerError, "读取临时文件失败")
		return
	}
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(key))
	}
	if err := newR2Client(cfg).put(r.Context(), key, tmp, size, hex.EncodeToString(hash.Sum(nil)), ct); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.recordNodeLog(r.Context(), 0, 0, "attachment-upload", "success", 0, "上传附件 "+key, "", "admin")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "key": key, "size": size,
		"url": "/api/attachments/download?key=" + url.QueryEscape(key),
	})
}

// GET /api/attachments/download?key=&inline=1
func (h *handlers) attachmentsDownload(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置")
		return
	}
	key, ok := safeObjectKey(r.URL.Query().Get("key"))
	if !ok {
		writeError(w, http.StatusBadRequest, "key 无效")
		return
	}
	resp, err := newR2Client(cfg).get(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		http.NotFound(w, r)
		return
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		writeError(w, http.StatusBadGateway, fmt.Sprintf("R2 下载失败（HTTP %d）：%s", resp.StatusCode, strings.TrimSpace(string(raw))))
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(path.Ext(key))
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", fmt.Sprint(resp.ContentLength))
	}
	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", disposition+`; filename*=UTF-8''`+url.PathEscape(path.Base(key)))
	w.Header().Set("Cache-Control", "private, max-age=0")
	_, _ = io.Copy(w, resp.Body)
}

// DELETE /api/attachments?key= 或 ?prefix=（递归删除目录）
func (h *handlers) attachmentsDelete(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置")
		return
	}
	client := newR2Client(cfg)
	if raw := strings.TrimSpace(r.URL.Query().Get("prefix")); raw != "" {
		prefix, ok := cleanFolderPath(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "prefix 无效")
			return
		}
		n, msg := deletePrefixObjects(r.Context(), client, prefix)
		if msg != "" {
			writeError(w, http.StatusBadGateway, msg)
			return
		}
		h.recordNodeLog(r.Context(), 0, 0, "attachment-delete", "success", 0,
			"删除目录 "+prefix+"（"+itoa64(int64(n))+" 个对象）", "", "admin")
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "prefix": prefix, "deleted": n})
		return
	}
	key, ok := safeObjectKey(r.URL.Query().Get("key"))
	if !ok {
		writeError(w, http.StatusBadRequest, "key 无效")
		return
	}
	if err := client.delete(r.Context(), key); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.recordNodeLog(r.Context(), 0, 0, "attachment-delete", "success", 0, "删除附件 "+key, "", "admin")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "key": key})
}
