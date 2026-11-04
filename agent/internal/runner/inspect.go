package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// inspectConfig 只读检查配置文件：返回文件 sha256、大小与去重排序后的用户名（UUID）。
// 不修改任何东西，供服务端「UUID 一致性巡检」核对节点机器上的真实配置。
func (r *Runner) inspectConfig(path string) string {
	res := map[string]any{"path": path}
	fail := func(msg string) string {
		res["error"] = msg
		b, _ := json.Marshal(res)
		return string(b)
	}
	if strings.TrimSpace(path) == "" {
		return fail("路径为空")
	}
	data, err := safeReadConfigFile(path, 8<<20)
	if err != nil {
		return fail("配置路径不允许或读取失败")
	}
	sum := sha256.Sum256(data)
	res["sha256"] = hex.EncodeToString(sum[:])
	res["size"] = len(data)
	var cfg struct {
		Inbounds []struct {
			Users []struct {
				Name     string `json:"name"`
				UUID     string `json:"uuid"`
				Password string `json:"password"`
			} `json:"users"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		res["inbounds"] = 0
		return fail("不是合法 JSON：" + err.Error())
	}
	seen := map[string]bool{}
	users := make([]string, 0, 16)
	for _, ib := range cfg.Inbounds {
		for _, u := range ib.Users {
			n := strings.TrimSpace(u.Name)
			if n == "" {
				n = strings.TrimSpace(u.UUID)
			}
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			users = append(users, n)
		}
	}
	sort.Strings(users)
	res["users"] = users
	res["inbounds"] = len(cfg.Inbounds)
	b, _ := json.Marshal(res)
	return string(b)
}
