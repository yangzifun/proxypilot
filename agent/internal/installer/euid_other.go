//go:build !unix

package installer

// IsRoot 非 Unix 平台恒为 false（Agent 的生产目标平台是 Linux，此处仅为让本机编译/单测通过）。
func IsRoot() bool { return false }
