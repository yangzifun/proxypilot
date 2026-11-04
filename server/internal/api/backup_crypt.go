package api

// 备份加密：AES-256-GCM + PBKDF2-SHA256 派生的分块流式格式。
//
// 文件格式：
//
//	"PPBK1" | salt(16) | 若干块
//	每块：uint32 长度（大端） | GCM 密文（含 tag）
//	每块 nonce = 4 字节 0 | uint64 块序号（大端）
//
// 分块设计使导出/导入都能流式处理，不需要把整包读入内存。

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

const (
	backupMagic     = "PPBK1"
	backupSaltSize  = 16
	backupChunkSize = 1 << 20 // 1MiB
	backupKDFIter   = 200000
)

func backupNonce(counter uint64) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[4:], counter)
	return n
}

func newBackupAEAD(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, backupKDFIter, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type backupEncryptWriter struct {
	w       io.Writer
	aead    cipher.AEAD
	counter uint64
	buf     []byte
	started bool
}

func newBackupEncryptWriter(w io.Writer, password string) (*backupEncryptWriter, error) {
	salt := make([]byte, backupSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	aead, err := newBackupAEAD(password, salt)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(backupMagic)); err != nil {
		return nil, err
	}
	if _, err := w.Write(salt); err != nil {
		return nil, err
	}
	return &backupEncryptWriter{w: w, aead: aead, buf: make([]byte, 0, backupChunkSize)}, nil
}

func (e *backupEncryptWriter) flush() error {
	if len(e.buf) == 0 {
		return nil
	}
	ct := e.aead.Seal(nil, backupNonce(e.counter), e.buf, nil)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(ct)))
	if _, err := e.w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := e.w.Write(ct); err != nil {
		return err
	}
	e.counter++
	e.buf = e.buf[:0]
	return nil
}

func (e *backupEncryptWriter) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		room := backupChunkSize - len(e.buf)
		if room == 0 {
			if err := e.flush(); err != nil {
				return total, err
			}
			continue
		}
		n := len(p)
		if n > room {
			n = room
		}
		e.buf = append(e.buf, p[:n]...)
		p = p[n:]
		total += n
	}
	return total, nil
}

func (e *backupEncryptWriter) Close() error {
	return e.flush()
}

// backupDecryptStream 把加密数据流解密写入 dst。
func backupDecryptStream(src io.Reader, dst io.Writer, password string) error {
	magic := make([]byte, len(backupMagic))
	if _, err := io.ReadFull(src, magic); err != nil || string(magic) != backupMagic {
		return errors.New("不是加密备份文件")
	}
	salt := make([]byte, backupSaltSize)
	if _, err := io.ReadFull(src, salt); err != nil {
		return errors.New("加密备份已损坏")
	}
	aead, err := newBackupAEAD(password, salt)
	if err != nil {
		return err
	}
	var counter uint64
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(src, hdr[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(hdr[:])
		if size == 0 || int(size) > backupChunkSize+aead.Overhead() {
			return errors.New("加密备份分块非法")
		}
		ct := make([]byte, size)
		if _, err := io.ReadFull(src, ct); err != nil {
			return errors.New("加密备份已截断")
		}
		pt, err := aead.Open(nil, backupNonce(counter), ct, nil)
		if err != nil {
			return errors.New("口令错误或备份已被篡改")
		}
		if _, err := dst.Write(pt); err != nil {
			return err
		}
		counter++
	}
}
