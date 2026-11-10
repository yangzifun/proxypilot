package auth

import "testing"

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("s3cret-p@ss")
	if err != nil {
		t.Fatal(err)
	}
	ok, rehash := VerifyPassword(hash, "s3cret-p@ss")
	if !ok || rehash {
		t.Fatalf("ok=%v rehash=%v，期望 true/false", ok, rehash)
	}
	if ok, _ := VerifyPassword(hash, "wrong"); ok {
		t.Fatal("错误口令不应通过")
	}
	if h2, _ := HashPassword("s3cret-p@ss"); h2 == hash {
		t.Fatal("两次哈希不应相同（salt 未生效）")
	}
}

func TestPasswordLegacySHA256(t *testing.T) {
	// Worker 时代 admin/password 的哈希（hex(sha256("password")))
	const legacy = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"

	// 默认策略：遗留哈希一律拒绝，强制走改密流程。
	// （V17.29 起由 PP_ALLOW_LEGACY_SHA256 开关控制，默认关闭）
	t.Setenv("PP_ALLOW_LEGACY_SHA256", "")
	if ok, rehash := VerifyPassword(legacy, "password"); ok || rehash {
		t.Fatalf("默认应拒绝遗留哈希：ok=%v rehash=%v", ok, rehash)
	}

	// 打开兼容开关后：接受，并要求 rehash 升级到 PBKDF2
	t.Setenv("PP_ALLOW_LEGACY_SHA256", "1")
	ok, rehash := VerifyPassword(legacy, "password")
	if !ok || !rehash {
		t.Fatalf("开关打开后应通过并要求 rehash：ok=%v rehash=%v", ok, rehash)
	}
	if ok, _ := VerifyPassword(legacy, "Password"); ok {
		t.Fatal("遗留哈希大小写敏感口令不应通过")
	}

	// 升级路径：rehash 后应变成 PBKDF2 格式，且不再需要 rehash
	h, err := HashPassword("password")
	if err != nil {
		t.Fatal(err)
	}
	if ok, rehash := VerifyPassword(h, "password"); !ok || rehash {
		t.Fatalf("PBKDF2 哈希应通过且不需要 rehash：ok=%v rehash=%v", ok, rehash)
	}
}
