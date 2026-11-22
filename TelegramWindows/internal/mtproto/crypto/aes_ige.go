// Package crypto 提供 MTProto 所需的密码学原语。
//
// 只使用标准库，禁止 cgo：AES-IGE 不在标准库中，故在此独立实现。
// 规范：https://core.telegram.org/mtproto/description#defining-aes-key-and-initialization-vector
package crypto

import (
	"crypto/aes"
	"errors"
)

// IGE 的块大小固定为 AES 块大小。
const BlockSize = aes.BlockSize

var (
	// ErrKeySize 表示 AES 密钥长度不是 32 字节（MTProto 固定 AES-256）。
	ErrKeySize = errors.New("crypto: AES-IGE 密钥必须是 32 字节")
	// ErrIVSize 表示 IGE 的 IV 不是 32 字节（两个 16 字节向量）。
	ErrIVSize = errors.New("crypto: AES-IGE 的 IV 必须是 32 字节")
	// ErrNotBlockAligned 表示明文/密文长度不是块大小的整数倍。
	ErrNotBlockAligned = errors.New("crypto: 数据长度必须是 16 字节的整数倍")
)

// IGEEncrypt 使用 AES-256-IGE 加密 src，返回新的密文切片。
//
// IGE 的块间耦合：
//
//	c_i = AES(p_i xor c_{i-1}) xor p_{i-1}
//
// 其中 c_{-1} = iv[0:16]、p_{-1} = iv[16:32]。
func IGEEncrypt(key, iv, src []byte) ([]byte, error) {
	return igeCrypt(key, iv, src, false)
}

// IGEDecrypt 使用 AES-256-IGE 解密 src，返回新的明文切片。
func IGEDecrypt(key, iv, src []byte) ([]byte, error) {
	return igeCrypt(key, iv, src, true)
}

func igeCrypt(key, iv, src []byte, decrypt bool) ([]byte, error) {
	if len(key) != 32 {
		return nil, ErrKeySize
	}
	if len(iv) != 32 {
		return nil, ErrIVSize
	}
	if len(src)%BlockSize != 0 {
		return nil, ErrNotBlockAligned
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	dst := make([]byte, len(src))
	prevC := iv[0:BlockSize]
	prevP := iv[BlockSize:32]
	var x [BlockSize]byte

	for off := 0; off < len(src); off += BlockSize {
		cur := src[off : off+BlockSize]
		out := dst[off : off+BlockSize]

		if decrypt {
			// x = D(c xor p_prev); p = x xor c_prev
			xorInto(x[:], cur, prevP)
			block.Decrypt(x[:], x[:])
			xorInto(out, x[:], prevC)
			prevP = out
			prevC = cur
			continue
		}

		// x = E(p xor c_prev); c = x xor p_prev
		xorInto(x[:], cur, prevC)
		block.Encrypt(x[:], x[:])
		xorInto(out, x[:], prevP)
		prevP = cur
		prevC = out
	}
	return dst, nil
}

func xorInto(dst, a, b []byte) {
	for i := 0; i < BlockSize; i++ {
		dst[i] = a[i] ^ b[i]
	}
}
