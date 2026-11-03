package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const attachmentEditMax = 2 << 20 // 2 MiB

func attachmentContentType(key, fallback string) string {
	ct := strings.TrimSpace(strings.Split(fallback, ";")[0])
	if ct == "" || ct == "application/octet-stream" || ct == "binary/octet-stream" {
		if byExt := mime.TypeByExtension(filepath.Ext(key)); byExt != "" {
			return strings.Split(byExt, ";")[0]
		}
	}
	if ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func attachmentEditable(ct, key string, data []byte) bool {
	if len(data) > attachmentEditMax || !utf8.Valid(data) {
		return false
	}
	// 含 NUL 的一律按二进制处理
	if bytes.IndexByte(data, 0) >= 0 {
		return false
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	for _, bad := range []string{"image/", "audio/", "video/", "font/", "application/pdf",
		"application/zip", "application/gzip", "application/x-tar", "application/x-7z-compressed"} {
		if strings.HasPrefix(ct, bad) {
			return false
		}
	}
	// 其余只要是合法 UTF-8 文本（含未知扩展名）都允许在线编辑
	return true
}

func (h *handlers) effectiveCDNBase(ctx context.Context) string {
	return strings.TrimRight(h.setting(ctx, settingCDNBaseURL, h.cfg.CDNBaseURL), "/")
}

func objectCDNURL(base, key string) string {
	if base == "" || key == "" {
		return ""
	}
	parts := strings.Split(strings.TrimLeft(key, "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.TrimRight(base, "/") + "/" + strings.Join(parts, "/")
}

// GET /api/attachments/content?key=
func (h *handlers) attachmentsContent(w http.ResponseWriter, r *http.Request) {
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
		writeError(w, http.StatusBadGateway, "R2 读取失败："+strings.TrimSpace(string(raw)))
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, attachmentEditMax+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "读取对象失败")
		return
	}
	ct := attachmentContentType(key, resp.Header.Get("Content-Type"))
	editable := attachmentEditable(ct, key, data)
	out := map[string]any{
		"success": true, "key": key, "size": len(data), "content_type": ct,
		"editable": editable, "cdn_url": objectCDNURL(h.effectiveCDNBase(r.Context()), key),
	}
	if len(data) > attachmentEditMax {
		out["editable"] = false
		out["message"] = "文件超过 2 MiB，仅支持下载"
	} else if editable {
		out["content"] = string(data)
	} else {
		out["message"] = "二进制或非文本文件，仅支持下载"
	}
	writeJSON(w, http.StatusOK, out)
}

// PUT /api/attachments/content
func (h *handlers) attachmentsContentSave(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置")
		return
	}
	var body struct {
		Key         string `json:"key"`
		Content     string `json:"content"`
		ContentType string `json:"content_type"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	key, ok := safeObjectKey(body.Key)
	if !ok {
		writeError(w, http.StatusBadRequest, "key 无效")
		return
	}
	if len(body.Content) > attachmentEditMax {
		writeError(w, http.StatusRequestEntityTooLarge, "内容超过 2 MiB")
		return
	}
	ct := attachmentContentType(key, body.ContentType)
	if !attachmentEditable(ct, key, []byte(body.Content)) {
		writeError(w, http.StatusBadRequest, "该文件不是可编辑文本")
		return
	}
	sum := sha256.Sum256([]byte(body.Content))
	if err := newR2Client(cfg).put(r.Context(), key, strings.NewReader(body.Content),
		int64(len(body.Content)), hex.EncodeToString(sum[:]), ct); err != nil {
		if strings.Contains(err.Error(), "AccessDenied") || strings.Contains(err.Error(), "HTTP 403") {
			writeError(w, http.StatusForbidden, "R2 凭证无写入权限：请使用 Object Read & Write 的 Cloudflare Token")
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	h.recordNodeLog(r.Context(), 0, 0, "attachment-edit", "success", 0, "编辑附件 "+key, "", "admin")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "key": key, "size": len(body.Content),
		"cdn_url": objectCDNURL(h.effectiveCDNBase(r.Context()), key),
	})
}
