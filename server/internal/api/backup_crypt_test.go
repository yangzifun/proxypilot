package api

import (
	"bytes"
	"testing"
)

func TestBackupCryptRoundTrip(t *testing.T) {
	plain := bytes.Repeat([]byte("proxypilot-backup-chunk-"), 200000) // ~5MB，跨多块
	var buf bytes.Buffer
	enc, err := newBackupEncryptWriter(&buf, "s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte(backupMagic)) {
		t.Fatal("缺少魔数")
	}
	var out bytes.Buffer
	if err := backupDecryptStream(bytes.NewReader(buf.Bytes()), &out, "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), plain) {
		t.Fatal("解密内容不一致")
	}
}

func TestBackupCryptWrongPassword(t *testing.T) {
	var buf bytes.Buffer
	enc, err := newBackupEncryptWriter(&buf, "right")
	if err != nil {
		t.Fatal(err)
	}
	enc.Write([]byte("hello"))
	enc.Close()
	var out bytes.Buffer
	if err := backupDecryptStream(bytes.NewReader(buf.Bytes()), &out, "wrong"); err == nil {
		t.Fatal("错误口令不应解密成功")
	}
}
