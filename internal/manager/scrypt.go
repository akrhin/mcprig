package manager

// scrypt по RFC 7914 на стандартной библиотеке: golang.org/x/crypto не входит
// в зависимости модуля, а тянуть его ради одной функции незачем. Проверяется
// векторами RFC 7914 в scrypt_test.go.

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
)

func scryptKey(password, salt []byte, n, r, p, keyLen int) ([]byte, error) {
	if n <= 1 || n&(n-1) != 0 {
		return nil, errors.New("scrypt: N должно быть степенью двойки больше 1")
	}
	if r <= 0 || p <= 0 || keyLen <= 0 {
		return nil, errors.New("scrypt: r, p и длина ключа должны быть положительными")
	}
	b, err := pbkdf2.Key(sha256.New, string(password), salt, 1, p*128*r)
	if err != nil {
		return nil, err
	}
	words := 32 * r
	for i := 0; i < p; i++ {
		block := make([]uint32, words)
		for j := range block {
			block[j] = binary.LittleEndian.Uint32(b[(i*32*r+j)*4:])
		}
		smix(block, r, n)
		for j, w := range block {
			binary.LittleEndian.PutUint32(b[(i*32*r+j)*4:], w)
		}
	}
	return pbkdf2.Key(sha256.New, string(password), b, 1, keyLen)
}

// smix — ROMix: заполнить V, потом N раз XOR с псевдослучайным блоком.
func smix(b []uint32, r, n int) {
	words := 32 * r
	v := make([]uint32, words*n)
	x := make([]uint32, words)
	y := make([]uint32, words)
	copy(x, b)
	for i := 0; i < n; i++ {
		copy(v[i*words:], x)
		blockMix(x, y, r)
		x, y = y, x
	}
	for i := 0; i < n; i++ {
		j := int(integerify(x, r) & uint64(n-1))
		for k := 0; k < words; k++ {
			x[k] ^= v[j*words+k]
		}
		blockMix(x, y, r)
		x, y = y, x
	}
	copy(b, x)
}

func integerify(x []uint32, r int) uint64 {
	i := (2*r - 1) * 16
	return uint64(x[i]) | uint64(x[i+1])<<32
}

// blockMix: Salsa20/8 над XOR очередного блока, чётные блоки в первую половину.
func blockMix(b, y []uint32, r int) {
	var x [16]uint32
	copy(x[:], b[(2*r-1)*16:])
	for i := 0; i < 2*r; i++ {
		for j := 0; j < 16; j++ {
			x[j] ^= b[i*16+j]
		}
		salsa8(&x)
		off := (i / 2) * 16
		if i%2 == 1 {
			off = (r + i/2) * 16
		}
		copy(y[off:], x[:])
	}
}

func salsa8(b *[16]uint32) {
	x := *b
	for i := 0; i < 4; i++ {
		x[4] ^= bits.RotateLeft32(x[0]+x[12], 7)
		x[8] ^= bits.RotateLeft32(x[4]+x[0], 9)
		x[12] ^= bits.RotateLeft32(x[8]+x[4], 13)
		x[0] ^= bits.RotateLeft32(x[12]+x[8], 18)
		x[9] ^= bits.RotateLeft32(x[5]+x[1], 7)
		x[13] ^= bits.RotateLeft32(x[9]+x[5], 9)
		x[1] ^= bits.RotateLeft32(x[13]+x[9], 13)
		x[5] ^= bits.RotateLeft32(x[1]+x[13], 18)
		x[14] ^= bits.RotateLeft32(x[10]+x[6], 7)
		x[2] ^= bits.RotateLeft32(x[14]+x[10], 9)
		x[6] ^= bits.RotateLeft32(x[2]+x[14], 13)
		x[10] ^= bits.RotateLeft32(x[6]+x[2], 18)
		x[3] ^= bits.RotateLeft32(x[15]+x[11], 7)
		x[7] ^= bits.RotateLeft32(x[3]+x[15], 9)
		x[11] ^= bits.RotateLeft32(x[7]+x[3], 13)
		x[15] ^= bits.RotateLeft32(x[11]+x[7], 18)

		x[1] ^= bits.RotateLeft32(x[0]+x[3], 7)
		x[2] ^= bits.RotateLeft32(x[1]+x[0], 9)
		x[3] ^= bits.RotateLeft32(x[2]+x[1], 13)
		x[0] ^= bits.RotateLeft32(x[3]+x[2], 18)
		x[6] ^= bits.RotateLeft32(x[5]+x[4], 7)
		x[7] ^= bits.RotateLeft32(x[6]+x[5], 9)
		x[4] ^= bits.RotateLeft32(x[7]+x[6], 13)
		x[5] ^= bits.RotateLeft32(x[4]+x[7], 18)
		x[11] ^= bits.RotateLeft32(x[10]+x[9], 7)
		x[8] ^= bits.RotateLeft32(x[11]+x[10], 9)
		x[9] ^= bits.RotateLeft32(x[8]+x[11], 13)
		x[10] ^= bits.RotateLeft32(x[9]+x[8], 18)
		x[12] ^= bits.RotateLeft32(x[15]+x[14], 7)
		x[13] ^= bits.RotateLeft32(x[12]+x[15], 9)
		x[14] ^= bits.RotateLeft32(x[13]+x[12], 13)
		x[15] ^= bits.RotateLeft32(x[14]+x[13], 18)
	}
	for i := range b {
		b[i] += x[i]
	}
}
