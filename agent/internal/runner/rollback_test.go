package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/state"
)

// allowTempDeploy 把 deploy 的路径白名单临时指向 dir。
//
// 生产代码里 deploy 调的是 safeConfigPath（只允许 /etc/... 前缀），
// Windows 上不存在这些目录，测试改为注入一个等价的「只允许 dir 内」校验。
func allowTempDeploy(t *testing.T, dir string) {
	t.Helper()
	prev := safeConfigPathFn
	root := strings.TrimRight(dir, string(os.PathSeparator)) + string(os.PathSeparator)
	safeConfigPathFn = func(p string) (string, error) {
		abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(p)))
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(abs, root) {
			return "", errors.New("路径不在允许目录内")
		}
		return abs, nil
	}
	t.Cleanup(func() { safeConfigPathFn = prev })
}

// deployCase 一条「check/restart 失败后是否回滚」的用例。
type deployCase struct {
	name       string
	hadOld     bool
	oldContent string
	checkCmd   string
	restartCmd string
	// checkCode/restartFail：校验退出码、前 N 次 restart 调用返回失败
	checkCode   int
	restartFail int
	// wantExists=false 表示回滚后目标文件应不存在
	wantExists bool
	wantFile   string
	wantOut    []string
	wantNoOut  string
	wantOK     bool
}

// P1-2 回归：check_cmd / restart_cmd 失败必须把旧配置还原，
// 否则节点上是「未验证通过的新配置 + 旧进程」，重启/宕机后服务起不来。
func TestDeployRollsBackOnCheckOrRestartFailure(t *testing.T) {
	const (
		oldConf = `{"marker":"old-valid"}`
		newConf = `{"marker":"new-broken"}`
	)
	cases := []deployCase{
		{
			name: "校验失败回到旧配置并按旧配置重启", hadOld: true, oldContent: oldConf,
			checkCmd: "sing-box check -c sb.json", restartCmd: "systemctl restart sing-box",
			checkCode:  1,
			wantExists: true, wantFile: oldConf,
			wantOut: []string{"校验命令失败", "已回滚", "已按旧配置重启"},
		},
		{
			name: "重启失败回到旧配置", hadOld: true, oldContent: oldConf,
			checkCmd: "sing-box check -c sb.json", restartCmd: "systemctl restart sing-box",
			restartFail: 1,
			wantExists:  true, wantFile: oldConf,
			wantOut: []string{"重启命令失败", "已回滚", "已按旧配置重启"},
		},
		{
			name: "下发前无文件则删除新配置", hadOld: false,
			checkCmd: "sing-box check -c sb.json", restartCmd: "systemctl restart sing-box",
			checkCode:  1,
			wantExists: false,
			wantOut:    []string{"已回滚", "下发前目标文件不存在"},
		},
		{
			name: "回滚后重启仍失败要写明", hadOld: true, oldContent: oldConf,
			checkCmd: "sing-box check -c sb.json", restartCmd: "systemctl restart sing-box",
			checkCode: 1, restartFail: 2, // 首次重启 + 回滚重启都失败
			wantExists: true, wantFile: oldConf,
			wantOut: []string{"已回滚", "按旧配置重启失败"},
		},
		{
			name: "成功时正常替换不回滚", hadOld: true, oldContent: oldConf,
			checkCmd: "sing-box check -c sb.json", restartCmd: "systemctl restart sing-box",
			wantExists: true, wantFile: newConf,
			wantNoOut: "已回滚", wantOK: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			allowTempDeploy(t, dir)
			path := filepath.Join(dir, "sb.json")
			if c.hadOld {
				if err := os.WriteFile(path, []byte(c.oldContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			calls := &[]shellCall{}
			restartCalls := 0
			r := &Runner{
				Shell: func(ctx context.Context, script string, timeoutSec int) (string, int) {
					*calls = append(*calls, shellCall{Script: script, Timeout: timeoutSec})
					switch {
					case c.checkCmd != "" && script == c.checkCmd:
						return "check output", c.checkCode
					case c.restartCmd != "" && script == c.restartCmd:
						restartCalls++
						if restartCalls <= c.restartFail {
							return "restart failed", 1
						}
						return "restarted", 0
					}
					return "", 0
				},
				Store: state.Store{Dir: filepath.Join(t.TempDir(), "state")},
				Log:   func(string, ...any) {},
				Now:   func() time.Time { return time.Unix(1700000000, 0) },
			}
			res := r.Run(context.Background(), client.Action{
				ID: 1, Type: "deploy", ConfigID: 7, Path: path, Content: newConf, Version: 3,
				CheckCmd: c.checkCmd, RestartCmd: c.restartCmd,
			})
			out := decodeOutput(t, res.OutputB64)
			if c.wantOK {
				if res.ExitCode != 0 {
					t.Fatalf("应成功：%+v（%s）", res, out)
				}
			} else if res.ExitCode == 0 {
				t.Fatalf("校验/重启失败必须返回失败：%+v（%s）", res, out)
			}
			for _, want := range c.wantOut {
				if !strings.Contains(out, want) {
					t.Fatalf("回执缺少 %q：\n%s", want, out)
				}
			}
			if c.wantNoOut != "" && strings.Contains(out, c.wantNoOut) {
				t.Fatalf("回执不该出现 %q：\n%s", c.wantNoOut, out)
			}
			got, err := os.ReadFile(path)
			if !c.wantExists {
				if err == nil {
					t.Fatalf("回滚后目标文件应不存在，实际内容 %q", got)
				}
			} else if err != nil || string(got) != c.wantFile {
				t.Fatalf("目标文件内容错：%q err=%v（期望 %q）", got, err, c.wantFile)
			}
			if c.wantOK {
				applied := r.Store.Applied()
				if len(applied) != 1 || applied[0].ConfigID != 7 || applied[0].Version != 3 {
					t.Fatalf("成功时应记录 applied：%+v", applied)
				}
			} else if len(r.Store.Applied()) != 0 {
				t.Fatalf("失败并回滚后不应记录 applied：%+v", r.Store.Applied())
			}
			leftover := path + ".pp.rollback." + strconv.Itoa(os.Getpid())
			if _, err := os.Stat(leftover); err == nil {
				t.Fatalf("回滚临时文件应被清理：%s", leftover)
			}
		})
	}
}
