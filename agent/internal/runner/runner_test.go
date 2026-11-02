package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/state"
)

type shellCall struct {
	Script  string
	Timeout int
}

func fakeShell(calls *[]shellCall, out string, code int) ShellRunner {
	return func(ctx context.Context, script string, timeoutSec int) (string, int) {
		*calls = append(*calls, shellCall{Script: script, Timeout: timeoutSec})
		return out, code
	}
}

func newRunner(t *testing.T, out string, code int) (*Runner, *[]shellCall, string) {
	t.Helper()
	dir := t.TempDir()
	calls := &[]shellCall{}
	r := &Runner{
		Shell: fakeShell(calls, out, code),
		Store: state.Store{Dir: filepath.Join(dir, "state")},
		Log:   func(string, ...any) {},
		Now:   func() time.Time { return time.Unix(1700000000, 0) },
	}
	return r, calls, dir
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func decodeOutput(t *testing.T, b64 string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("输出 base64 解码失败：%v", err)
	}
	return string(raw)
}

func TestDeployWritesFileAndRecordsApplied(t *testing.T) {
	r, calls, dir := newRunner(t, "restarted\n", 0)
	path := filepath.Join(dir, "etc", "xray", "config.json")
	a := client.Action{
		ID: 11, Type: "deploy", ConfigID: 3, Path: path, Content: `{"ok":true}`,
		Sha256: sha256Of(`{"ok":true}`), Version: 5, Backup: true,
		RestartCmd: "systemctl restart xray", CheckCmd: "xray run -test",
	}
	res := r.Run(context.Background(), a)
	if res.ExitCode != 0 || res.Status != "ok" {
		t.Fatalf("应成功：%+v（%s）", res, decodeOutput(t, res.OutputB64))
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != `{"ok":true}` {
		t.Fatalf("文件内容错：%q err=%v", got, err)
	}
	if len(*calls) != 2 || (*calls)[0].Script != "xray run -test" || (*calls)[1].Script != "systemctl restart xray" {
		t.Fatalf("命令顺序应为 check → restart：%+v", *calls)
	}
	applied := r.Store.Applied()
	if len(applied) != 1 || applied[0].ConfigID != 3 || applied[0].Version != 5 {
		t.Fatalf("applied 未记录：%+v", applied)
	}
	if !strings.Contains(decodeOutput(t, res.OutputB64), "已下发") {
		t.Fatalf("输出应包含下发摘要：%s", decodeOutput(t, res.OutputB64))
	}
}

func TestDeployShaMismatchKeepsOldFile(t *testing.T) {
	r, _, dir := newRunner(t, "", 0)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old-content"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := client.Action{ID: 12, Type: "deploy", ConfigID: 1, Path: path, Content: "new-content", Sha256: sha256Of("other-content"), Version: 2}
	res := r.Run(context.Background(), a)
	if res.ExitCode != 1 || res.Status != "failed" {
		t.Fatalf("sha 不一致必须失败：%+v", res)
	}
	if out := decodeOutput(t, res.OutputB64); !strings.Contains(out, "sha256 不一致") {
		t.Fatalf("失败原因不可读：%s", out)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "old-content" {
		t.Fatalf("目标文件被破坏：%q", got)
	}
	if len(r.Store.Applied()) != 0 {
		t.Fatal("失败不应记录 applied")
	}
	if _, err := os.Stat(path + ".pp." + itoa(os.Getpid())); err == nil {
		t.Fatal("临时文件应被清理")
	}
}

func TestDeployRejectsEmptyPathAndBadDecode(t *testing.T) {
	r, _, _ := newRunner(t, "", 0)
	res := r.Run(context.Background(), client.Action{ID: 1, Type: "deploy"})
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "目标路径为空") {
		t.Fatalf("空路径应失败：%+v", res)
	}
	res = r.Run(context.Background(), client.Action{ID: 2, Type: "deploy", Path: "/tmp/x", DecodeErr: true})
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "解码失败") {
		t.Fatalf("解码失败应中止：%+v", res)
	}
}

func TestDeployCheckCmdFailureFails(t *testing.T) {
	r, _, dir := newRunner(t, "bad config", 1)
	path := filepath.Join(dir, "config.json")
	a := client.Action{ID: 3, Type: "deploy", Path: path, Content: "x", CheckCmd: "xray run -test"}
	res := r.Run(context.Background(), a)
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "校验命令失败") {
		t.Fatalf("校验失败应返回 rc=1：%+v", res)
	}
}

func TestDeployRestartFailureFails(t *testing.T) {
	r, _, dir := newRunner(t, "restart failed", 1)
	a := client.Action{ID: 4, Type: "deploy", Path: filepath.Join(dir, "c"), Content: "x", ServiceName: "xray"}
	res := r.Run(context.Background(), a)
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "重启命令失败") {
		t.Fatalf("重启失败应返回 rc=1：%+v", res)
	}
}

func TestDeployBackupIssuesCopyPreservingAttributes(t *testing.T) {
	r, calls, dir := newRunner(t, "", 0)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := r.Run(context.Background(), client.Action{ID: 5, Type: "deploy", Path: path, Content: "new", Backup: true})
	if res.ExitCode != 0 {
		t.Fatalf("应成功：%+v", res)
	}
	if len(*calls) != 1 || !strings.HasPrefix((*calls)[0].Script, "cp -a '") || !strings.Contains((*calls)[0].Script, ".bak.") {
		t.Fatalf("备份命令错：%+v", *calls)
	}
}

func TestDeploySkipsWhenContentIdentical(t *testing.T) {
	r, calls, dir := newRunner(t, "", 0)
	path := filepath.Join(dir, "same.json")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := r.Run(context.Background(), client.Action{ID: 7, Type: "deploy", Path: path, Content: "same", Backup: true})
	if res.ExitCode != 0 {
		t.Fatalf("内容一致应视为成功：%+v", res)
	}
	if len(*calls) != 0 {
		t.Fatalf("内容一致不应执行备份/重启命令：%+v", *calls)
	}
	if res.OutputB64 == "" {
		t.Fatalf("应有输出说明跳过：%+v", res)
	}
}

func TestDeployRestartsInactiveServiceWhenContentIdentical(t *testing.T) {
	r, _, dir := newRunner(t, "", 0)
	calls := &[]shellCall{}
	r.Shell = func(ctx context.Context, script string, timeoutSec int) (string, int) {
		*calls = append(*calls, shellCall{Script: script, Timeout: timeoutSec})
		if strings.Contains(script, "is-active") {
			return "", 3
		}
		return "started", 0
	}
	path := filepath.Join(dir, "same.json")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := client.Action{ID: 8, Type: "deploy", Path: path, Content: "same", ServiceName: "mtg", RestartCmd: "systemctl restart mtg"}
	res := r.Run(context.Background(), a)
	if res.ExitCode != 0 {
		t.Fatalf("服务拉起后应成功：%+v（%s）", res, decodeOutput(t, res.OutputB64))
	}
	if len(*calls) != 2 || !strings.Contains((*calls)[0].Script, "is-active") || (*calls)[1].Script != "systemctl restart mtg" {
		t.Fatalf("应先探测再重启：%+v", *calls)
	}
	if out := decodeOutput(t, res.OutputB64); !strings.Contains(out, "已重启") {
		t.Fatalf("输出应说明已重启：%s", out)
	}
}

func TestDeploySkipsRestartWhenServiceActive(t *testing.T) {
	r, calls, dir := newRunner(t, "", 0)
	path := filepath.Join(dir, "same.json")
	if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := client.Action{ID: 9, Type: "deploy", Path: path, Content: "same", ServiceName: "mtg", RestartCmd: "systemctl restart mtg"}
	res := r.Run(context.Background(), a)
	if res.ExitCode != 0 {
		t.Fatalf("应成功：%+v", res)
	}
	if len(*calls) != 1 || !strings.Contains((*calls)[0].Script, "is-active") {
		t.Fatalf("服务已运行不应重启：%+v", *calls)
	}
}

func TestPruneBackupsKeepsNewestFive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sb.json")
	for _, ts := range []string{"20260101000001", "20260101000002", "20260101000003", "20260101000004", "20260101000005", "20260101000006", "20260101000007"} {
		if err := os.WriteFile(path+".bak."+ts, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(path, 5)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("应只保留 5 个备份，实际 %d", len(entries))
	}
	if _, err := os.Stat(path + ".bak.20260101000001"); !os.IsNotExist(err) {
		t.Fatalf("最旧的备份应被删除")
	}
	if _, err := os.Stat(path + ".bak.20260101000007"); err != nil {
		t.Fatalf("最新的备份应保留：%v", err)
	}
}

func TestUpdateAgentDownloadsVerifiesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "proxypilot-agent")
	if err := os.WriteFile(self, []byte("old-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("N"), 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	oldSelf, oldRestart, oldDelay := selfBinaryPath, restartAgentService, selfUpdateRestartDelay
	selfBinaryPath = func() (string, error) { return self, nil }
	restarted := make(chan struct{}, 1)
	restartAgentService = func() { restarted <- struct{}{} }
	selfUpdateRestartDelay = 0
	defer func() { selfBinaryPath, restartAgentService, selfUpdateRestartDelay = oldSelf, oldRestart, oldDelay }()

	r, _, _ := newRunner(t, "", 0)
	sum := sha256Hex(payload)
	res := r.Run(context.Background(), client.Action{
		ID: 9, Type: "update_agent", Name: "v9.9.9", Path: srv.URL + "/bin", Sha256: sum,
	})
	if res.ExitCode != 0 {
		t.Fatalf("自更新应成功：%+v", res)
	}
	got, err := os.ReadFile(self)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("二进制未替换：err=%v len=%d", err, len(got))
	}
	if _, err := os.Stat(self + ".prev"); err != nil {
		t.Fatalf("应保留上一版：%v", err)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("应触发重启")
	}
}

func TestUpdateAgentRejectsBadSha(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "agent")
	_ = os.WriteFile(self, []byte("old"), 0o700)
	payload := bytes.Repeat([]byte("X"), 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer srv.Close()

	oldSelf := selfBinaryPath
	selfBinaryPath = func() (string, error) { return self, nil }
	defer func() { selfBinaryPath = oldSelf }()

	r, _, _ := newRunner(t, "", 0)
	res := r.Run(context.Background(), client.Action{
		ID: 10, Type: "update_agent", Name: "v1", Path: srv.URL, Sha256: strings.Repeat("0", 64),
	})
	if res.ExitCode == 0 {
		t.Fatalf("sha 不匹配必须失败：%+v", res)
	}
	got, _ := os.ReadFile(self)
	if string(got) != "old" {
		t.Fatalf("校验失败不应替换二进制：%q", string(got))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestRestartUsesServiceNameWhenNoCommand(t *testing.T) {
	r, calls, _ := newRunner(t, "", 0)
	res := r.Run(context.Background(), client.Action{ID: 6, Type: "restart", ServiceName: "xray,sing-box"})
	if res.ExitCode != 0 {
		t.Fatalf("应成功：%+v", res)
	}
	if len(*calls) != 1 || (*calls)[0].Script != "systemctl restart xray" {
		t.Fatalf("应从 service_name 推导（取第一个）：%+v", *calls)
	}
	res = r.Run(context.Background(), client.Action{ID: 7, Type: "restart"})
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "缺少重启命令") {
		t.Fatalf("缺命令应失败：%+v", res)
	}
}

func TestExecEmptyFailsAndOutputTrimmed(t *testing.T) {
	r, calls, _ := newRunner(t, "  hello\n", 0)
	res := r.Run(context.Background(), client.Action{ID: 8, Type: "exec"})
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "命令为空") {
		t.Fatalf("空命令应失败：%+v", res)
	}
	res = r.Run(context.Background(), client.Action{ID: 9, Type: "exec", Command: "echo hello", Timeout: 15})
	if res.ExitCode != 0 || decodeOutput(t, res.OutputB64) != "hello" {
		t.Fatalf("输出应去除首尾空白：%q", decodeOutput(t, res.OutputB64))
	}
	if len(*calls) != 1 || (*calls)[0].Timeout != 15 {
		t.Fatalf("超时应透传给 shell：%+v", *calls)
	}
}

func TestExecFailureIncludesExitCode(t *testing.T) {
	r, _, _ := newRunner(t, "boom", 2)
	res := r.Run(context.Background(), client.Action{ID: 10, Type: "exec", Command: "false"})
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "命令执行失败（rc=2）") {
		t.Fatalf("%+v => %s", res, decodeOutput(t, res.OutputB64))
	}
}

func TestProbeUsesInjectedOutput(t *testing.T) {
	r, _, _ := newRunner(t, "", 0)
	r.Probe = func() string { return "hostname=hk-01\nos=Ubuntu" }
	res := r.Run(context.Background(), client.Action{ID: 11, Type: "probe"})
	if res.ExitCode != 0 || !strings.Contains(decodeOutput(t, res.OutputB64), "hostname=hk-01") {
		t.Fatalf("%+v", res)
	}
}

func TestUnknownTypeFails(t *testing.T) {
	r, _, _ := newRunner(t, "", 0)
	res := r.Run(context.Background(), client.Action{ID: 12, Type: "frobnicate"})
	if res.ExitCode != 1 || !strings.Contains(decodeOutput(t, res.OutputB64), "未知任务类型") {
		t.Fatalf("%+v", res)
	}
}

func TestResultFieldsMatchWorkerExpectations(t *testing.T) {
	r, _, _ := newRunner(t, "done", 0)
	res := r.Run(context.Background(), client.Action{ID: 21, Type: "exec", Command: "true"})
	if res.ID != 21 || res.Status != "ok" || res.ExitCode != 0 || res.OutputB64 == "" {
		t.Fatalf("回执字段错：%+v", res)
	}
	if _, err := base64.StdEncoding.DecodeString(res.OutputB64); err != nil {
		t.Fatalf("输出必须是 base64：%v", err)
	}
}
