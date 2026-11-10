package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 用面板已有的 Cloudflare API Token 把发信所需的 DNS 记录一键写进去。
//
// 需要 Token 具备 Zone → DNS → Edit 权限；没有权限时给出明确提示，
// 退回「手动复制记录值」的流程。

const cfAPIBase = "https://api.cloudflare.com/client/v4"

type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfAPIError    `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfAPIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e cfEnvelope) errText() string {
	if len(e.Errors) == 0 {
		return "未知错误"
	}
	parts := make([]string, 0, len(e.Errors))
	for _, it := range e.Errors {
		parts = append(parts, fmt.Sprintf("%d %s", it.Code, it.Message))
	}
	return strings.Join(parts, "; ")
}

func cfRequest(ctx context.Context, token, method, path string, body any) (cfEnvelope, error) {
	var env cfEnvelope
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return env, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, cfAPIBase+path, rdr)
	if err != nil {
		return env, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return env, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err := json.Unmarshal(raw, &env); err != nil {
		return env, fmt.Errorf("Cloudflare 返回无法解析（HTTP %d）", resp.StatusCode)
	}
	if !env.Success {
		return env, fmt.Errorf("%s", env.errText())
	}
	return env, nil
}

// cfFindZoneID 从完整域名往上逐级找 zone（proxypilot.a.com → a.com → com）。
func cfFindZoneID(ctx context.Context, token, domain string) (zoneID, zoneName string, err error) {
	parts := strings.Split(domain, ".")
	for i := 0; i < len(parts)-1; i++ {
		candidate := strings.Join(parts[i:], ".")
		env, cerr := cfRequest(ctx, token, http.MethodGet, "/zones?name="+candidate, nil)
		if cerr != nil {
			return "", "", cerr
		}
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if json.Unmarshal(env.Result, &zones) == nil && len(zones) > 0 {
			return zones[0].ID, zones[0].Name, nil
		}
	}
	return "", "", fmt.Errorf("Token 下找不到 %s 所属的 Cloudflare Zone（或缺少 Zone:Read 权限）", domain)
}

type cfDNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment,omitempty"`
}

// cfUpsertTXT 新建或更新一条 TXT 记录（同 name 有旧值就覆盖）。
func cfUpsertTXT(ctx context.Context, token, zoneID string, rec cfDNSRecord) (string, error) {
	// 先找同名同类型记录
	env, err := cfRequest(ctx, token, http.MethodGet,
		"/zones/"+zoneID+"/dns_records?type=TXT&name="+rec.Name, nil)
	if err != nil {
		return "", err
	}
	var existing []cfDNSRecord
	_ = json.Unmarshal(env.Result, &existing)

	if len(existing) > 0 {
		target := existing[0]
		// 已有内容一致就不用动
		if strings.TrimSpace(target.Content) == strings.TrimSpace(rec.Content) {
			return "unchanged", nil
		}
		_, uerr := cfRequest(ctx, token, http.MethodPut,
			"/zones/"+zoneID+"/dns_records/"+target.ID, rec)
		if uerr != nil {
			return "", uerr
		}
		return "updated", nil
	}

	if _, cerr := cfRequest(ctx, token, http.MethodPost, "/zones/"+zoneID+"/dns_records", rec); cerr != nil {
		return "", cerr
	}
	return "created", nil
}

// POST /api/admin/mail/dns/apply —— 把当前指引里的记录一键写到 Cloudflare
func (h *handlers) mailDNSApply(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mc := h.effectiveMail(ctx)

	domain := strings.TrimSpace(mc.Domain)
	if domain == "" && strings.TrimSpace(mc.From) != "" {
		if _, d, err := splitMailAddress(mc.From); err == nil {
			domain = d
		}
	}
	if domain == "" {
		writeError(w, http.StatusBadRequest, "请先填写发信域名")
		return
	}
	if strings.TrimSpace(mc.DKIMPrivate) == "" {
		writeError(w, http.StatusBadRequest, "请先生成 DKIM 密钥")
		return
	}
	// 优先用专用的 DNS Token；没有就复用统一 Cloudflare Token
	dnsToken := strings.TrimSpace(mc.DNSToken)
	if dnsToken == "" {
		dnsToken = strings.TrimSpace(mc.CFToken)
	}
	if dnsToken == "" {
		writeError(w, http.StatusBadRequest,
			"没有可用的 Cloudflare Token：请在下面填「DNS 专用 Token」，或手动添加下列记录")
		return
	}

	publicB64, err := dkimPublicFromPrivate(mc.DKIMPrivate)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 DKIM 公钥失败："+err.Error())
		return
	}

	zoneID, zoneName, err := cfFindZoneID(ctx, dnsToken, domain)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			err.Error()+"。需要的权限：Zone → Zone → Read，Zone → DNS → Edit")
		return
	}

	records := h.mailDNSGuide(domain, mc.dkimSelector(), publicB64, h.mailEgressIP())

	type result struct {
		Key    string `json:"key"`
		Host   string `json:"host"`
		Action string `json:"action"`
		Error  string `json:"error,omitempty"`
	}
	var out []result
	failed := 0

	for _, rec := range records {
		if rec.Type != "TXT" {
			continue // PTR 只能在服务商控制台改
		}
		action, aerr := cfUpsertTXT(ctx, dnsToken, zoneID, cfDNSRecord{
			Type: "TXT", Name: rec.Host, Content: rec.Value, TTL: 300,
			Comment: "ProxyPilot 自建发信",
		})
		item := result{Key: rec.Key, Host: rec.Host, Action: action}
		if aerr != nil {
			item.Error = aerr.Error()
			failed++
		}
		out = append(out, item)
	}

	h.log.Info("已写入发信 DNS 记录", "zone", zoneName, "domain", domain, "failed", failed)

	msg := fmt.Sprintf("已写入 %d 条记录到 %s", len(out)-failed, zoneName)
	if failed > 0 {
		msg = fmt.Sprintf("部分失败：成功 %d 条，失败 %d 条（通常是没有 Zone:DNS:Edit 权限）", len(out)-failed, failed)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": failed == 0,
		"zone":    zoneName,
		"results": out,
		"message": msg,
	})
}