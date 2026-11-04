package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// cspReport 接收 CSP violation 报告（Report-Only 阶段使用）。
// 仅记录日志，始终返回 204，避免报告端重试风暴。
func (h *handlers) cspReport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err == nil && len(body) > 0 {
		var report struct {
			Body struct {
				DocumentURI        string `json:"document-uri"`
				ViolatedDirective  string `json:"violated-directive"`
				EffectiveDirective string `json:"effective-directive"`
				BlockedURI         string `json:"blocked-uri"`
				SourceFile         string `json:"source-file"`
				LineNumber         int    `json:"line-number"`
			} `json:"csp-report"`
		}
		if json.Unmarshal(body, &report) == nil && report.Body.DocumentURI != "" {
			h.log.Warn("CSP 违规",
				"document", truncateRunes(report.Body.DocumentURI, 200),
				"directive", firstNonEmpty(report.Body.EffectiveDirective, report.Body.ViolatedDirective),
				"blocked", truncateRunes(report.Body.BlockedURI, 200),
				"source", truncateRunes(report.Body.SourceFile, 200),
				"line", report.Body.LineNumber)
		} else if len(body) > 0 {
			h.log.Debug("CSP 报告无法解析", "body", truncateRunes(strings.TrimSpace(string(body)), 300))
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
