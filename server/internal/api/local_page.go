package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 本机（控制服务器）管理页所需的只读 + 上传接口。
// 只允许操作本机记录（link_mode='local'），不涉及其他节点。

const localUploadDir = "/opt/proxypilot/uploads"

// localServerID 返回本机服务器的 ID（不存在则 0）。
func (h *handlers) localServerID(r *http.Request) int64 {
	var id int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT id FROM servers WHERE link_mode='local' ORDER BY id LIMIT 1`).Scan(&id)
	return id
}

// GET /api/local/overview
func (h *handlers) localOverview(w http.ResponseWriter, r *http.Request) {
	id := h.localServerID(r)
	if id <= 0 {
		writeError(w, http.StatusNotFound, "本机服务器记录不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, COALESCE(name,'') AS name, COALESCE(host,'') AS host, COALESCE(last_status,'') AS last_status,
		        COALESCE(last_metrics,'') AS last_metrics, COALESCE(ssh_guard,0) AS ssh_guard,
		        COALESCE(last_ip,'') AS last_ip, COALESCE(updated_at,0) AS updated_at
		   FROM servers WHERE id = ?`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取本机信息失败")
		return
	}
	row := rows[0]
	metrics := map[string]any{}
	if raw := mapStr(row, "last_metrics", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &metrics)
	}
	delete(row, "last_metrics")
	row["metrics"] = metrics
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": row})
}

// GET /api/local/processes：按 CPU 排序的前 20 个进程
func (h *handlers) localProcesses(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("/bin/sh", "-c",
		`ps -eo pid,comm,pcpu,pmem,rss --sort=-pcpu 2>/dev/null | head -21`)
	out, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取进程列表失败")
		return
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	list := make([]map[string]any, 0, len(lines))
	for i, line := range lines {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		pid, _ := strconv.ParseInt(f[0], 10, 64)
		cpu, _ := strconv.ParseFloat(f[2], 64)
		mem, _ := strconv.ParseFloat(f[3], 64)
		rss, _ := strconv.ParseInt(f[4], 10, 64)
		name := strings.Join(f[1:len(f)-3], " ")
		list = append(list, map[string]any{
			"pid": pid, "name": name,
			"cpu": cpu, "mem": mem, "rss_mb": float64(rss) / 1024.0,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": list})
}

// GET /api/local/ports：监听中的 TCP 端口
func (h *handlers) localPorts(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("/bin/sh", "-c",
		`ss -tlnp 2>/dev/null | tail -n +2 | awk '{print $4" "$6}'`)
	out, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取端口失败")
		return
	}
	seen := map[string]bool{}
	list := []map[string]any{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		addr := strings.TrimSpace(parts[0])
		proc := ""
		if len(parts) > 1 {
			proc = strings.TrimSpace(parts[1])
		}
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		port := addr[idx+1:]
		bind := strings.Trim(addr[:idx], "[]")
		key := bind + ":" + port
		if seen[key] {
			continue
		}
		seen[key] = true
		p, _ := strconv.ParseInt(port, 10, 64)
		list = append(list, map[string]any{
			"port": p, "bind": bind, "process": strings.Trim(proc, "\""),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return mapInt(mapOf(list[i]), "port", 0) < mapInt(mapOf(list[j]), "port", 0)
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": list})
}

func mapOf(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// GET /api/local/files：已上传文件列表
func (h *handlers) localFiles(w http.ResponseWriter, r *http.Request) {
	list := []map[string]any{}
	entries, err := os.ReadDir(localUploadDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			list = append(list, map[string]any{
				"name": e.Name(), "size": info.Size(), "mod": info.ModTime().UnixMilli(),
			})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		return mapInt(mapOf(list[i]), "mod", 0) > mapInt(mapOf(list[j]), "mod", 0)
	})
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": list, "dir": localUploadDir})
}

// POST /api/local/upload：上传文件到本机
func (h *handlers) localUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 256<<20)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "上传解析失败："+err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "缺少文件字段 file")
		return
	}
	defer file.Close()

	name := filepath.Base(strings.TrimSpace(header.Filename))
	name = strings.ReplaceAll(name, "..", "_")
	if name == "" || name == "." || name == "/" {
		writeError(w, http.StatusBadRequest, "文件名无效")
		return
	}
	if err := os.MkdirAll(localUploadDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "创建目录失败："+err.Error())
		return
	}
	dst := filepath.Join(localUploadDir, name)
	out, err := os.Create(dst)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "写入失败："+err.Error())
		return
	}
	defer out.Close()
	n, err := io.Copy(out, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "写入失败："+err.Error())
		return
	}
	_ = os.Chmod(dst, 0o644)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "name": name, "size": n, "path": dst,
		"message": fmt.Sprintf("已上传到 %s（%.1f KB）", dst, float64(n)/1024),
	})
}

// DELETE /api/local/files/{name}
func (h *handlers) localFileDelete(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimSpace(pathParam(r, "name")))
	if name == "" || name == "." {
		writeError(w, http.StatusBadRequest, "文件名无效")
		return
	}
	if err := os.Remove(filepath.Join(localUploadDir, name)); err != nil {
		writeError(w, http.StatusNotFound, "删除失败："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "已删除 " + name})
}

func nowMsLocal() int64 { return time.Now().UnixMilli() }
