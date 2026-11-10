package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

type cloudflareCheckItem struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// POST /api/cloudflare/check —— 检查统一 Cloudflare Token 与 R2 读写权限
func (h *handlers) cloudflareCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := strings.TrimSpace(h.setting(ctx, settingCFToken, h.cfg.CloudflareAPIToken))
	cfg := h.effectiveR2(ctx)
	items := []cloudflareCheckItem{}

	add := func(key, name string, ok bool, detail string) {
		items = append(items, cloudflareCheckItem{Key: key, Name: name, OK: ok, Detail: detail})
	}

	if token == "" {
		add("token", "Cloudflare API Token", false, "未配置 Token")
	} else {
		// 强制重新校验，避免命中 30 分钟缓存后看不到刚换的 Token。
		cloudflareTokenIDs.Delete(sha256Hex(token))
		id, err := cloudflareTokenID(ctx, token)
		if err != nil {
			add("token", "Cloudflare API Token", false, err.Error())
		} else {
			add("token", "Cloudflare API Token", true, "有效（Token ID "+id+"）")
		}
	}

	if cfg.AccountID == "" {
		add("r2", "R2 账号 / Bucket", false, "缺少 Account ID")
	} else if cfg.Bucket == "" {
		add("r2", "R2 账号 / Bucket", false, "缺少 Bucket")
	} else if !cfg.Ready() {
		add("r2", "R2 账号 / Bucket", false, "R2 凭证未就绪（Token 无效或未配置）")
	} else {
		client := newR2Client(cfg)
		res, err := client.list(ctx, "", "", 1)
		if err != nil {
			add("r2_read", "R2 读取", false, err.Error())
		} else {
			add("r2_read", "R2 读取", true, fmt.Sprintf("Bucket %s 可读取（根目录 %d 项）", cfg.Bucket, len(res.Contents)+len(res.Folders)))
			// 写权限用临时对象验证，成功后立即删除。
			key := fmt.Sprintf(".proxypilot-check/%d.txt", time.Now().UnixNano())
			body := "proxypilot-cloudflare-check"
			putErr := client.put(ctx, key, strings.NewReader(body), int64(len(body)), sha256Hex(body), "text/plain")
			if putErr != nil {
				add("r2_write", "R2 写入", false, putErr.Error())
			} else {
				delErr := client.delete(ctx, key)
				if delErr != nil {
					add("r2_write", "R2 写入", true, "写入成功，但清理临时对象失败："+delErr.Error())
				} else {
					add("r2_write", "R2 写入", true, "写入 / 删除临时对象成功")
				}
			}
		}
	}

	overall := len(items) > 0
	for _, it := range items {
		if !it.OK {
			overall = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"overall": overall,
		"checks":  items,
		"account": cfg.AccountID,
		"bucket":  cfg.Bucket,
	})
}

