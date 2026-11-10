//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows 下用 DPAPI（CryptProtectData）加密本地保存的门户令牌。
//
// 为什么需要：配置文件里明文 JWT 意味着任何以当前用户身份运行的程序
// 都能读走令牌（换 VPN 票、访问用户中心）。DPAPI 用当前用户的登录凭据
// 派生密钥，换用户或换机器都无法解密。
//
// 作用域：当前用户（默认）。不传 CRYPTPROTECT_LOCAL_MACHINE，
// 避免同机其它用户也能解密。

var (
	crypt32             = windows.NewLazySystemDLL("crypt32.dll")
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procCryptProtect    = crypt32.NewProc("CryptProtectData")
	procCryptUnprotect  = crypt32.NewProc("CryptUnprotectData")
	procLocalFreeWin    = kernel32.NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newBlob(d []byte) dataBlob {
	if len(d) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(d)), pbData: &d[0]}
}

func (b dataBlob) bytes() []byte {
	if b.pbData == nil || b.cbData == 0 {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, b.cbData))
	return out
}

// protectSecret 用当前用户凭据加密。
func protectSecret(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, nil
	}
	in := newBlob(plain)
	var out dataBlob
	// CRYPTPROTECT_UI_FORBIDDEN = 0x1
	r, _, err := procCryptProtect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		uintptr(0x1),
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData 失败：%v", err)
	}
	defer procLocalFreeWin.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

// unprotectSecret 解密（必须是同一 Windows 用户）。
func unprotectSecret(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, nil
	}
	in := newBlob(cipher)
	var out dataBlob
	r, _, err := procCryptUnprotect.Call(
		uintptr(unsafe.Pointer(&in)),
		0, 0, 0, 0,
		uintptr(0x1),
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData 失败：%v", err)
	}
	defer procLocalFreeWin.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

// secretStorageKind 用于日志/界面展示。
const secretStorageKind = "dpapi"

// secureFileAcl 把配置文件 ACL 收紧到当前用户（Windows 用 icacls）。
func secureFileAcl(path string) {
	user := windows.GetCurrentProcessToken()
	_ = user
	// 交给 icacls 处理，失败不影响主流程
	runWinSilent("icacls", path, "/inheritance:r",
		"/grant:r", `"%USERNAME%":F`)
}