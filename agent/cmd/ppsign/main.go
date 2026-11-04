// ppsign：Agent 分发包签名工具（Ed25519）。
//
// 用法：
//
//	ppsign gen <keyfile>            生成私钥（base64 seed）并输出公钥（base64）
//	ppsign pub <keyfile>            输出公钥
//	ppsign sign <keyfile> <message> 输出签名（base64）
//	ppsign verify <pub> <message> <sig> 校验
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

func die(msg string) {
	fmt.Fprintln(os.Stderr, "ppsign:", msg)
	os.Exit(1)
}

func readSeed(path string) ed25519.PrivateKey {
	raw, err := os.ReadFile(path)
	if err != nil {
		die("读取私钥失败: " + err.Error())
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		die("私钥格式无效（需要 base64 编码的 32 字节 seed）")
	}
	return ed25519.NewKeyFromSeed(seed)
}

func main() {
	if len(os.Args) < 3 {
		die("用法: ppsign gen|pub|sign|verify ...")
	}
	switch os.Args[1] {
	case "gen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			die(err.Error())
		}
		seed := priv.Seed()
		if err := os.WriteFile(os.Args[2], []byte(base64.StdEncoding.EncodeToString(seed)+"\n"), 0o600); err != nil {
			die("写入私钥失败: " + err.Error())
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
	case "pub":
		priv := readSeed(os.Args[2])
		fmt.Println(base64.StdEncoding.EncodeToString(priv.Public().(ed25519.PublicKey)))
	case "sign":
		if len(os.Args) < 4 {
			die("缺少待签名内容")
		}
		priv := readSeed(os.Args[2])
		sig := ed25519.Sign(priv, []byte(os.Args[3]))
		fmt.Println(base64.StdEncoding.EncodeToString(sig))
	case "verify":
		if len(os.Args) < 5 {
			die("缺少参数")
		}
		pub, err := base64.StdEncoding.DecodeString(os.Args[2])
		if err != nil || len(pub) != ed25519.PublicKeySize {
			die("公钥格式无效")
		}
		sig, err := base64.StdEncoding.DecodeString(os.Args[4])
		if err != nil {
			die("签名格式无效")
		}
		if !ed25519.Verify(ed25519.PublicKey(pub), []byte(os.Args[3]), sig) {
			die("签名校验失败")
		}
		fmt.Println("ok")
	case "msghash":
		// 便于核对：输出参数内容的 sha256
		if len(os.Args) < 3 {
			die("缺少内容")
		}
		sum := sha256.Sum256([]byte(os.Args[2]))
		fmt.Println(hex.EncodeToString(sum[:]))
	default:
		die("未知子命令: " + os.Args[1])
	}
}
