package installer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"proxypilot-agent/internal/config"
)

func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.Origin = "https://pp.example.com"
	cfg.Key = "ag_" + strings.Repeat("a", 64)
	cfg.Interval = 60
	cfg.Services = []string{"xray", "nginx"}
	return cfg
}

func srcBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "proxypilot-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# fake binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRenderUnit(t *testing.T) {
	unit := RenderUnit(BinPath)
	for _, want := range []string{"[Unit]", "[Service]", "[Install]", "Restart=always", "ExecStart=" + BinPath + " run", "WantedBy=multi-user.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("单元缺少 %q：\n%s", want, unit)
		}
	}
}

func TestInstallWritesFilesAndSkipsSystemctlInTestRoot(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig()
	var calls []string
	runner := func(name string, args ...string) (string, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return "", nil
	}
	if err := Install(root, cfg, srcBinary(t), runner, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, BinPath)
	conf := filepath.Join(root, ConfPath)
	unit := filepath.Join(root, UnitPath)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("二进制未安装：%v", err)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(bin); err == nil && fi.Mode().Perm() != 0o700 {
			t.Fatalf("二进制权限应为 0700：%v", fi.Mode().Perm())
		}
		if fi, err := os.Stat(conf); err == nil && fi.Mode().Perm() != 0o600 {
			t.Fatalf("配置权限应为 0600：%v", fi.Mode().Perm())
		}
	}
	confText, err := os.ReadFile(conf)
	if err != nil {
		t.Fatal(err)
	}
	back := config.Parse(string(confText))
	if back.Origin != cfg.Origin || back.Key != cfg.Key || back.Interval != cfg.Interval {
		t.Fatalf("配置内容错：%+v", back)
	}
	if len(calls) != 0 {
		t.Fatalf("测试 root 不应调用 systemctl：%v", calls)
	}
	if _, err := os.Stat(unit); err != nil {
		t.Fatalf("单元未写入：%v", err)
	}
}

func TestInstallThenUninstallRemovesFiles(t *testing.T) {
	root := t.TempDir()
	runner := func(string, ...string) (string, error) { return "", nil }
	if err := Install(root, testConfig(), srcBinary(t), runner, nil); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root, runner, nil); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{BinPath, ConfPath, UnitPath} {
		if _, err := os.Stat(filepath.Join(root, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s 未删除：%v", rel, err)
		}
	}
}

func TestInstallRejectsMissingSource(t *testing.T) {
	if err := Install(t.TempDir(), testConfig(), "", DefaultRunner, nil); err == nil {
		t.Fatal("缺少源二进制应报错")
	}
	if err := Install(t.TempDir(), testConfig(), filepath.Join(t.TempDir(), "nope"), DefaultRunner, nil); err == nil {
		t.Fatal("源二进制不存在应报错")
	}
}
