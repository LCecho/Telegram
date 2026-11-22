package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func mustRand(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return b
}

func TestIGERoundTrip(t *testing.T) {
	for _, size := range []int{16, 32, 48, 256, 1024} {
		key := mustRand(t, 32)
		iv := mustRand(t, 32)
		plain := mustRand(t, size)

		enc, err := IGEEncrypt(key, iv, plain)
		if err != nil {
			t.Fatalf("size=%d encrypt: %v", size, err)
		}
		if bytes.Equal(enc, plain) {
			t.Fatalf("size=%d 密文与明文相同", size)
		}

		dec, err := IGEDecrypt(key, iv, enc)
		if err != nil {
			t.Fatalf("size=%d decrypt: %v", size, err)
		}
		if !bytes.Equal(dec, plain) {
			t.Fatalf("size=%d round-trip 不一致", size)
		}
	}
}

// IGE 的错误传播是"向前无界"：改动第 1 块会让其后所有块的密文变化；
// 而改动最后一块不会影响前面任何块。
func TestIGEBlockChaining(t *testing.T) {
	key := make([]byte, 32)
	iv := make([]byte, 32)
	plain := make([]byte, 64)
	for i := range plain {
		plain[i] = byte(i)
	}
	base, err := IGEEncrypt(key, iv, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// 改动第 1 块：后续所有块都必须变化。
	modified := append([]byte(nil), plain...)
	modified[0] ^= 0xFF
	got, err := IGEEncrypt(key, iv, modified)
	if err != nil {
		t.Fatalf("encrypt modified: %v", err)
	}
	if bytes.Equal(base[0:16], got[0:16]) {
		t.Fatal("第 1 块密文未变化")
	}
	for off := 16; off < 64; off += 16 {
		if bytes.Equal(base[off:off+16], got[off:off+16]) {
			t.Fatalf("第 %d 块未受影响（IGE 前向传播失效）", off/16+1)
		}
	}

	// 改动最后一块：前面三块必须保持不变。
	last := append([]byte(nil), plain...)
	last[63] ^= 0xFF
	gotLast, err := IGEEncrypt(key, iv, last)
	if err != nil {
		t.Fatalf("encrypt last: %v", err)
	}
	if bytes.Equal(base[48:64], gotLast[48:64]) {
		t.Fatal("最后一块密文未变化")
	}
	for off := 0; off < 48; off += 16 {
		if !bytes.Equal(base[off:off+16], gotLast[off:off+16]) {
			t.Fatalf("第 %d 块不应变化", off/16+1)
		}
	}
}

func TestIGEInvalidInput(t *testing.T) {
	key := make([]byte, 32)
	iv := make([]byte, 32)
	plain := make([]byte, 16)

	if _, err := IGEEncrypt(key[:16], iv, plain); err != ErrKeySize {
		t.Fatalf("短密钥应返回 ErrKeySize，实际 %v", err)
	}
	if _, err := IGEEncrypt(key, iv[:16], plain); err != ErrIVSize {
		t.Fatalf("短 IV 应返回 ErrIVSize，实际 %v", err)
	}
	if _, err := IGEEncrypt(key, iv, plain[:15]); err != ErrNotBlockAligned {
		t.Fatalf("非整块长度应返回 ErrNotBlockAligned，实际 %v", err)
	}
}

// 空输入应返回空切片而不是错误（MTProto 合法场景：无 payload 的容器）。
func TestIGEEmpty(t *testing.T) {
	out, err := IGEEncrypt(make([]byte, 32), make([]byte, 32), nil)
	if err != nil {
		t.Fatalf("空输入报错: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %d", len(out))
	}
}
