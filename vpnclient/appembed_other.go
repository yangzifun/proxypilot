//go:build !(windows && desktop && installer)

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// 非安装程序构建：没有内置主程序，安装时复制自身（CLI/脚本安装场景）。
func installAppBinary(dir string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, embeddedAppName)
	if src, _ := filepath.Abs(exe); src == dst {
		return dst, nil
	}
	if err := copyFileTo(exe, dst); err != nil {
		return "", fmt.Errorf("复制程序失败：%w", err)
	}
	return dst, nil
}

// copyFileTo 通用文件复制（跨平台）。
func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	buf := make([]byte, 1<<20)
	for {
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				out.Close()
				os.Remove(tmp)
				return werr
			}
		}
		if rerr != nil {
			break
		}
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	_ = os.Remove(dst)
	return os.Rename(tmp, dst)
}