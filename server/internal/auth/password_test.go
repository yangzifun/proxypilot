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
	// Worker 时代 admin/password 的哈希
	const legacy = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"
	ok, rehash := VerifyPassword(legacy, "password")
	if !ok || !rehash {
		t.Fatalf("ok=%v rehash=%v，期望 true/true（需升级为 PBKDF2）", ok, rehash)
	}
	if ok, _ := VerifyPassword(legacy, "Password"); ok {
		t.Fatal("遗留哈希大小写敏感口令不应通过")
	}
}

func TestPasswordUnknownFormat(t *testing.T) {
	if ok, _ := VerifyPassword("not-a-hash", "x"); ok {
		t.Fatal("未知格式不应通过")
	}
}
