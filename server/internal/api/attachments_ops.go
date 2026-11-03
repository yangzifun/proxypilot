package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// cleanFolderPath 规范化目录前缀：去掉前导 /，拒绝 .. 与控制字符，补齐结尾 /。
func cleanFolderPath(p string) (string, bool) {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimLeft(p, "/")
	if p == "" || strings.Contains(p, "..") || strings.ContainsAny(p, "\r\n\x00") {
		return "", false
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p, true
}

// POST /api/attachments/folder  {prefix, name}
func (h *handlers) attachmentsMkdir(w http.ResponseWriter, r *http.Request) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置")
		return
	}
	var body struct {
		Prefix string `json:"prefix"`
		Name   string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	name := strings.Trim(strings.TrimSpace(body.Name), "/")
	if name == "" {
		writeError(w, http.StatusBadRequest, "目录名不能为空")
		return
	}
	key, ok := cleanFolderPath(normalizePrefix(body.Prefix) + name)
	if !ok {
		writeError(w, http.StatusBadRequest, "目录名无效")
		return
	}
	if err := newR2Client(cfg).put(r.Context(), key, strings.NewReader(""), 0, sha256Hex(""), "application/x-directory"); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "key": key})
}

// POST /api/attachments/move  {from, to, folder?}
func (h *handlers) attachmentsMove(w http.ResponseWriter, r *http.Request) {
	h.attachmentsRelocate(w, r, true)
}

// POST /api/attachments/copy  {from, to, folder?}
func (h *handlers) attachmentsCopyOp(w http.ResponseWriter, r *http.Request) {
	h.attachmentsRelocate(w, r, false)
}

func (h *handlers) attachmentsRelocate(w http.ResponseWriter, r *http.Request, move bool) {
	cfg := h.effectiveR2(r.Context())
	if !cfg.Ready() {
		writeError(w, http.StatusServiceUnavailable, "R2 未配置")
		return
	}
	var body struct {
		From   string `json:"from"`
		To     string `json:"to"`
		Folder bool   `json:"folder"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	client := newR2Client(cfg)
	isFolder := body.Folder || strings.HasSuffix(strings.TrimSpace(body.From), "/")
	if !isFolder {
		from, ok1 := safeObjectKey(body.From)
		to, ok2 := safeObjectKey(body.To)
		if !ok1 || !ok2 {
			writeError(w, http.StatusBadRequest, "key 无效")
			return
		}
		if from == to {
			writeError(w, http.StatusBadRequest, "源和目标相同")
			return
		}
		if err := client.copy(r.Context(), from, to); err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if move {
			if err := client.delete(r.Context(), from); err != nil {
				writeError(w, http.StatusBadGateway, "已复制但删除源失败："+err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "moved": move, "objects": 1, "to": to})
		return
	}
	from, ok1 := cleanFolderPath(body.From)
	to, ok2 := cleanFolderPath(body.To)
	if !ok1 || !ok2 {
		writeError(w, http.StatusBadRequest, "目录无效")
		return
	}
	if from == to {
		writeError(w, http.StatusBadRequest, "源和目标相同")
		return
	}
	n, msg := relocateFolder(r.Context(), client, from, to, move)
	if msg != "" {
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "moved": move, "objects": n, "to": to})
}

func relocateFolder(ctx context.Context, client *r2Client, from, to string, move bool) (int, string) {
	objects, err := client.listAll(ctx, from)
	if err != nil {
		return 0, err.Error()
	}
	if len(objects) == 0 {
		return 0, "目录为空"
	}
	n := 0
	for _, o := range objects {
		target := to + strings.TrimPrefix(o.Key, from)
		if err := client.copy(ctx, o.Key, target); err != nil {
			return n, "复制 " + o.Key + " 失败：" + err.Error()
		}
		if move {
			if err := client.delete(ctx, o.Key); err != nil {
				return n, "删除 " + o.Key + " 失败：" + err.Error()
			}
		}
		n++
	}
	return n, ""
}

// deletePrefixObjects 递归删除一个目录前缀下的全部对象。
func deletePrefixObjects(ctx context.Context, client *r2Client, prefix string) (int, string) {
	objects, err := client.listAll(ctx, prefix)
	if err != nil {
		return 0, err.Error()
	}
	n := 0
	for _, o := range objects {
		if err := client.delete(ctx, o.Key); err != nil {
			return n, "删除 " + o.Key + " 失败：" + err.Error()
		}
		n++
	}
	// 目录占位对象（prefix 自身）也删掉
	_ = client.delete(ctx, prefix)
	return n, ""
}

// sha256HexBytes 计算字节切片摘要。
func sha256HexBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
