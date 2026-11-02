//go:build unix

package installer

import "os"

// IsRoot 判断当前是否 root（安装/卸载需要）。非 Unix 平台见 euid_other.go。
func IsRoot() bool { return os.Geteuid() == 0 }
