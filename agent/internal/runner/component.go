package runner

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"proxypilot-agent/internal/metrics"
)

// 组件下载地址（测试可覆盖）
var (
	singboxReleaseBase = "https://github.com/SagerNet/sing-box/releases/download"
	singboxVersion     = "1.14.4"
	xrayReleaseBase    = "https://github.com/XTLS/Xray-core/releases/download"
	xrayVersion        = "v25.9.11"
	// V13.3：MTProto（Telegram）代理；mtg-multi 是 mtg 的多 secret 分支
	mtgReleaseBase  = "https://github.com/dolonet/mtg-multi/releases/download"
	mtgVersion      = "1.13.0"
	componentBinDir = "/usr/local/bin"
	componentHTTP   = &http.Client{Timeout: 300 * time.Second}
)

// firstCommandWord 取 check_cmd 的第一个词（如 "sing-box check -c ..." → "sing-box"）。
func firstCommandWord(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	if len(fields) == 0 {
		return ""
	}
	w := fields[0]
	if strings.ContainsAny(w, "/\\") {
		return filepath.Base(w)
	}
	// 跳过 shell 前缀
	if w == "sh" || w == "bash" || w == "env" {
		return ""
	}
	return w
}

// ensureComponent 在目标机器上安装缺失的组件：sing-box / xray / mtg-multi。
// 只处理白名单里的二进制，避免任意命令执行。
func (r *Runner) ensureComponent(ctx context.Context, bin string, configPath string) (int, string) {
	switch bin {
	case "sing-box":
		if code, out := r.installSingbox(ctx); code != 0 {
			return code, out
		}
		r.ensureUnit("sing-box", "/usr/local/bin/sing-box run -c /etc/sing-box/sb.json")
		return 0, "已自动安装 sing-box"
	case "xray":
		if code, out := r.installXray(ctx); code != 0 {
			return code, out
		}
		cfg := configPath
		if cfg == "" {
			cfg = "/etc/sing-box/xray.json"
		}
		r.ensureUnit("xray", "/usr/local/bin/xray run -config "+cfg)
		return 0, "已自动安装 xray"
	case "mtg-multi":
		if code, out := r.installMtg(ctx); code != 0 {
			return code, out
		}
		// MTProto 的配置在 /etc/mtg/config.toml；reload = SIGHUP，只增删改 secret、不断已有会话
		r.ensureUnit("mtg", "/usr/local/bin/mtg-multi run /etc/mtg/config.toml", "/bin/kill -HUP $MAINPID")
		return 0, "已自动安装 mtg-multi"
	}
	return 1, "缺少组件 " + bin + "，且不在自动安装白名单内（仅支持 sing-box / xray / mtg-multi）"
}

// ensureUnit 如果 systemd 单元不存在就写一个最小可用的（root 运行）。
// execReload 非空时写入 ExecReload；已存在的单元若缺这一段，用 drop-in 补上，
// 避免覆盖用户自己改过的单元文件。
func (r *Runner) ensureUnit(name, execStart string, execReload ...string) {
	unitPath := "/etc/systemd/system/" + name + ".service"
	reload := ""
	if len(execReload) > 0 {
		reload = strings.TrimSpace(execReload[0])
	}
	if cur, err := os.ReadFile(unitPath); err == nil {
		if reload == "" || strings.Contains(string(cur), "ExecReload=") {
			return
		}
		dir := unitPath + ".d"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			r.log("写 systemd drop-in 目录失败：%v", err)
			return
		}
		dropIn := "[Service]\nExecReload=" + reload + "\n"
		if err := os.WriteFile(filepath.Join(dir, "99-proxypilot-reload.conf"), []byte(dropIn), 0o644); err != nil {
			r.log("写 systemd drop-in 失败：%v", err)
			return
		}
		_, _ = exec.Command("systemctl", "daemon-reload").CombinedOutput()
		r.log("已为 %s 补上 ExecReload", name)
		return
	}
	content := fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target
Wants=network-online.target

[Service]
User=root
Group=root
ExecStart=%s
%sRestart=always
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
`, name, execStart, reloadLine(reload))
	if err := os.WriteFile(unitPath, []byte(content), 0o644); err != nil {
		r.log("写 systemd 单元失败：%v", err)
		return
	}
	_, _ = exec.Command("systemctl", "daemon-reload").CombinedOutput()
	r.log("已创建 systemd 单元 %s", unitPath)
}

func reloadLine(reload string) string {
	if strings.TrimSpace(reload) == "" {
		return ""
	}
	return "ExecReload=" + reload + "\n"
}

func componentArch() (linuxArch, xrayArch string) {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64", "arm64-v8a"
	case "386":
		return "386", "32"
	default:
		return "amd64", "64"
	}
}

// installSingbox 安装内置默认版本。
func (r *Runner) installSingbox(ctx context.Context) (int, string) {
	return r.installSingboxVersion(ctx, singboxVersion)
}

// installSingboxVersion 从 GitHub release 下载指定版本的 tar.gz 并安装到 /usr/local/bin/sing-box。
func (r *Runner) installSingboxVersion(ctx context.Context, version string) (int, string) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if version == "" {
		version = singboxVersion
	}
	arch, _ := componentArch()
	url := fmt.Sprintf("%s/v%s/sing-box-%s-linux-%s.tar.gz", singboxReleaseBase, version, version, arch)
	r.log("自动安装 sing-box：%s", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1, "构造 sing-box 下载请求失败：" + err.Error()
	}
	resp, err := componentHTTP.Do(req)
	if err != nil {
		return 1, "下载 sing-box 失败：" + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1, fmt.Sprintf("下载 sing-box 失败：HTTP %d", resp.StatusCode)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return 1, "解压 sing-box 失败：" + err.Error()
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 1, "读取 sing-box 压缩包失败：" + err.Error()
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "sing-box" {
			buf, err := io.ReadAll(io.LimitReader(tr, 200<<20))
			if err != nil {
				return 1, "读取 sing-box 二进制失败：" + err.Error()
			}
			if err := os.WriteFile(filepath.Join(componentBinDir, "sing-box"), buf, 0o755); err != nil {
				return 1, "写入 sing-box 失败：" + err.Error()
			}
			r.log("sing-box 已安装到 %s", filepath.Join(componentBinDir, "sing-box"))
			return 0, "已安装 sing-box"
		}
	}
	return 1, "sing-box 压缩包里没有找到二进制"
}

// installXray 安装内置默认版本。
func (r *Runner) installXray(ctx context.Context) (int, string) {
	return r.installXrayVersion(ctx, xrayVersion)
}

// installXrayVersion 从 GitHub release 下载指定 tag 的 zip 并安装到 /usr/local/bin/xray。
func (r *Runner) installXrayVersion(ctx context.Context, tag string) (int, string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		tag = xrayVersion
	}
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	_, arch := componentArch()
	url := fmt.Sprintf("%s/%s/Xray-linux-%s.zip", xrayReleaseBase, tag, arch)
	r.log("自动安装 xray：%s", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1, "构造 xray 下载请求失败：" + err.Error()
	}
	resp, err := componentHTTP.Do(req)
	if err != nil {
		return 1, "下载 xray 失败：" + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1, fmt.Sprintf("下载 xray 失败：HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return 1, "读取 xray 安装包失败：" + err.Error()
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return 1, "解压 xray 失败：" + err.Error()
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != "xray" || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return 1, "打开 xray 二进制失败：" + err.Error()
		}
		buf, err := io.ReadAll(io.LimitReader(rc, 200<<20))
		rc.Close()
		if err != nil {
			return 1, "读取 xray 二进制失败：" + err.Error()
		}
		if err := os.WriteFile(filepath.Join(componentBinDir, "xray"), buf, 0o755); err != nil {
			return 1, "写入 xray 失败：" + err.Error()
		}
		r.log("xray 已安装到 %s", filepath.Join(componentBinDir, "xray"))
		return 0, "已安装 xray"
	}
	return 1, "xray 压缩包里没有找到二进制"
}

// installMtg 从 GitHub release 下载 tar.gz 并安装到 /usr/local/bin/mtg-multi。
// 资产命名：mtg-multi-<ver>-linux-<arch>.tar.gz（tag 带 v，文件名不带）。
func (r *Runner) installMtg(ctx context.Context) (int, string) {
	arch, _ := componentArch()
	url := fmt.Sprintf("%s/v%s/mtg-multi-%s-linux-%s.tar.gz", mtgReleaseBase, mtgVersion, mtgVersion, arch)
	r.log("自动安装 mtg-multi：%s", url)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 1, "构造 mtg-multi 下载请求失败：" + err.Error()
	}
	resp, err := componentHTTP.Do(req)
	if err != nil {
		return 1, "下载 mtg-multi 失败：" + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1, fmt.Sprintf("下载 mtg-multi 失败：HTTP %d", resp.StatusCode)
	}
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return 1, "解压 mtg-multi 失败：" + err.Error()
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 1, "读取 mtg-multi 压缩包失败：" + err.Error()
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "mtg-multi" {
			buf, err := io.ReadAll(io.LimitReader(tr, 200<<20))
			if err != nil {
				return 1, "读取 mtg-multi 二进制失败：" + err.Error()
			}
			if err := os.WriteFile(filepath.Join(componentBinDir, "mtg-multi"), buf, 0o755); err != nil {
				return 1, "写入 mtg-multi 失败：" + err.Error()
			}
			r.log("mtg-multi 已安装到 %s", filepath.Join(componentBinDir, "mtg-multi"))
			return 0, "已安装 mtg-multi"
		}
	}
	return 1, "mtg-multi 压缩包里没有找到二进制"
}

// installedVersion 读已安装组件的版本号（解析不到或没装返回空串）。
func installedVersion(bin string) string {
	if _, err := exec.LookPath(bin); err != nil {
		return ""
	}
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	return metrics.ParseVersionOutput(string(out))
}

func sameVersion(a, b string) bool {
	norm := func(v string) string { return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v") }
	return norm(a) != "" && norm(a) == norm(b)
}

// ensureVersion 把组件切到模板指定的版本：已一致则跳过，否则下载安装（含 systemd 单元兜底）。
func (r *Runner) ensureVersion(ctx context.Context, bin, want string) (int, string) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	if want == "" {
		return 0, ""
	}
	cur := installedVersion(bin)
	if sameVersion(cur, want) {
		return 0, bin + " 已是 " + cur
	}
	code := 0
	switch bin {
	case "sing-box":
		code, _ = r.installSingboxVersion(ctx, want)
		if code == 0 {
			r.ensureUnit("sing-box", "/usr/local/bin/sing-box run -c /etc/sing-box/sb.json")
		}
	case "xray":
		code, _ = r.installXrayVersion(ctx, want)
		if code == 0 {
			r.ensureUnit("xray", "/usr/local/bin/xray run -config /etc/sing-box/xray.json")
		}
	default:
		return 1, "不支持的组件版本切换：" + bin
	}
	if code != 0 {
		return code, "安装 " + bin + " " + want + " 失败（原版本 " + cur + "）"
	}
	r.log("组件 %s 已切换：%s -> %s", bin, cur, want)
	return 0, bin + " " + cur + " -> " + want
}
