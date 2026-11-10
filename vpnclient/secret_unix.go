//go:build !windows

package main

// 非 Windows：没有 DPAPI 等价物，暂不加密（文件权限 0600 由调用方保证）。
// 后续可接 macOS Keychain / Linux Secret Service。

func protectSecret(plain []byte) ([]byte, error) { return plain, nil }

func unprotectSecret(cipher []byte) ([]byte, error) { return cipher, nil }

const secretStorageKind = "plain"

func secureFileAcl(path string) {}