// Copyright (c) 2026 PRZM Swarm Team Silicon-GFNI. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package silicon_gfni

import (
	"errors"
	"math/bits"
)

// Irreducible polynomial for AES/PRZM Galois Field GF(2^8):
// p(x) = x^8 + x^4 + x^3 + x + 1 (0x11B = 283).
const GF256Polynomial = 0x11B

// Precomputed GF(2^8) multiplication and inverse tables.
var (
	gf256MulTable [256][256]uint8
	gf256InvTable [256]uint8
)

func init() {
	// Initialize GF(2^8) multiplication table modulo 0x11B
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			gf256MulTable[a][b] = gf256MulSlow(uint8(a), uint8(b))
		}
	}

	// Initialize multiplicative inverse table in GF(2^8) with 0^(-1) = 0
	gf256InvTable[0] = 0
	for a := 1; a < 256; a++ {
		for b := 1; b < 256; b++ {
			if gf256MulTable[a][b] == 1 {
				gf256InvTable[a] = uint8(b)
				break
			}
		}
	}
}

// gf256MulSlow computes GF(2^8) multiplication bit-by-bit modulo 0x11B.
func gf256MulSlow(a, b uint8) uint8 {
	var res uint8
	currA := a
	currB := b
	for i := 0; i < 8; i++ {
		if (currB & 1) != 0 {
			res ^= currA
		}
		hi := currA & 0x80
		currA <<= 1
		if hi != 0 {
			currA ^= 0x1B // 0x11B & 0xFF
		}
		currB >>= 1
	}
	return res
}

// GF256Mul returns a * b in GF(2^8) modulo p(x) = 0x11B.
func GF256Mul(a, b uint8) uint8 {
	return gf256MulTable[a][b]
}

// GF256Inv returns the multiplicative inverse a^(-1) in GF(2^8) with 0^(-1) = 0.
func GF256Inv(a uint8) uint8 {
	return gf256InvTable[a]
}

// GF2P8AffineQB emulates the Intel GFNI instruction VGF2P8AFFINEQB for a single byte.
// Given input byte x, 64-bit matrix A (8 rows of 8 bits each), and 8-bit affine offset b:
// Output bit i = parity(x & row_i) ^ b_i.
// In Intel representation, row i of matrix A is byte i of the 64-bit integer matrix:
// row_i = uint8((matrix >> (8 * i)) & 0xFF).
func GF2P8AffineQB(x uint8, matrix uint64, imm8 uint8) uint8 {
	var y uint8
	for i := 0; i < 8; i++ {
		rowI := uint8((matrix >> (8 * i)) & 0xFF)
		parity := uint8(bits.OnesCount8(x&rowI) & 1)
		bBit := (imm8 >> i) & 1
		y |= (parity ^ bBit) << i
	}
	return y
}

// GF2P8AffineInvQB emulates the Intel GFNI instruction VGF2P8AFFINEINVQB for a single byte.
// It computes the GF(2^8) inverse of x (with 0^(-1) = 0), followed by the affine transformation:
// y = A * (x^(-1)) ^ b.
func GF2P8AffineInvQB(x uint8, matrix uint64, imm8 uint8) uint8 {
	invX := GF256Inv(x)
	return GF2P8AffineQB(invX, matrix, imm8)
}

// InvertGF2Matrix8x8 computes the inverse of an 8x8 matrix over GF(2) using Gauss-Jordan elimination.
// Returns (inverse, true) if invertible, or (0, false) if singular.
func InvertGF2Matrix8x8(matrix uint64) (uint64, bool) {
	var a [8]uint8
	var inv [8]uint8

	for i := 0; i < 8; i++ {
		a[i] = uint8((matrix >> (8 * i)) & 0xFF)
		inv[i] = 1 << i // Augmented with 8x8 identity matrix
	}

	for col := 0; col < 8; col++ {
		// Find pivot in current column
		pivotRow := -1
		for row := col; row < 8; row++ {
			if (a[row] & (1 << col)) != 0 {
				pivotRow = row
				break
			}
		}
		if pivotRow == -1 {
			return 0, false // Matrix is singular
		}

		// Swap rows if necessary
		if pivotRow != col {
			a[col], a[pivotRow] = a[pivotRow], a[col]
			inv[col], inv[pivotRow] = inv[pivotRow], inv[col]
		}

		// Eliminate other rows
		for row := 0; row < 8; row++ {
			if row != col && ((a[row] & (1 << col)) != 0) {
				a[row] ^= a[col]
				inv[row] ^= inv[col]
			}
		}
	}

	var res uint64
	for i := 0; i < 8; i++ {
		res |= uint64(inv[i]) << (8 * i)
	}
	return res, true
}

// InvertAffineTransform computes the inverse affine transformation (A_inv, b_inv).
// For forward mapping y = A*x ^ b, the inverse is x = A^(-1)*y ^ A^(-1)*b.
// Thus A_inv = A^(-1) and b_inv = A^(-1)*b.
func InvertAffineTransform(matrix uint64, imm8 uint8) (invMatrix uint64, invImm8 uint8, ok bool) {
	invMatrix, ok = InvertGF2Matrix8x8(matrix)
	if !ok {
		return 0, 0, false
	}
	// b_inv = A_inv * b ^ 0
	invImm8 = GF2P8AffineQB(imm8, invMatrix, 0)
	return invMatrix, invImm8, true
}

// VPERMB emulates the AVX-512 VBMI VPERMB instruction across a 64-byte block.
// Each output byte is gathered from src using the lower 6 bits of indices:
// dst[i] = src[indices[i] & 63].
func VPERMB(dst, indices, src []byte) {
	_ = dst[63]
	_ = indices[63]
	_ = src[63]

	for i := 0; i < 64; i++ {
		dst[i] = src[indices[i]&63]
	}
}

// VPERMI2B emulates the AVX-512 VBMI VPERMI2B instruction (128-byte lookup across two 64-byte registers).
// Bit 6 of indices[i] selects table 0 (src0) or table 1 (src1), and bits 0..5 select the byte within that table.
func VPERMI2B(dst, indices, src0, src1 []byte) {
	_ = dst[63]
	_ = indices[63]
	_ = src0[63]
	_ = src1[63]

	for i := 0; i < 64; i++ {
		idx := indices[i]
		if (idx & 64) == 0 {
			dst[i] = src0[idx&63]
		} else {
			dst[i] = src1[idx&63]
		}
	}
}

// InvertPermutation64 computes the inverse permutation of a 64-byte array.
// Returns (inv, nil) if perm is a valid bijection of 0..63, error otherwise.
func InvertPermutation64(perm [64]uint8) ([64]uint8, error) {
	var inv [64]uint8
	var seen uint64

	for i := 0; i < 64; i++ {
		v := perm[i]
		if v >= 64 {
			return inv, errors.New("permutation index out of range [0, 63]")
		}
		mask := uint64(1) << v
		if (seen & mask) != 0 {
			return inv, errors.New("duplicate permutation index")
		}
		seen |= mask
		// In gather notation dst[i] = src[perm[i]].
		// To invert, if dst has permuted data, src[k] = dst[inv[k]].
		// When dst[i] = src[perm[i]], let k = perm[i] => src[k] = dst[i].
		// Therefore inv[perm[i]] = i.
		inv[v] = uint8(i)
	}

	return inv, nil
}

// MicroDecompressor holds the in-register state of a silicon-level micro-decompressor pipeline.
// In hardware, all tables and matrices are resident in vector registers (ZMM0-ZMM31).
type MicroDecompressor struct {
	// Forward and inverse 64-byte byte-permutation tables (AVX-512 VBMI)
	Permutation    [64]uint8
	InvPermutation [64]uint8

	// Forward and inverse affine transformation parameters (Intel GFNI)
	Matrix    uint64
	Imm8      uint8
	InvMatrix uint64
	InvImm8   uint8

	// In-register precomputed symbol mapping tables (emulating 1-cycle affine execution)
	ForwardTable [256]uint8
	InverseTable [256]uint8
}

// NewMicroDecompressor constructs a new decompressor engine given an affine transformation
// and a 64-byte permutation. Returns error if matrix is singular or permutation invalid.
func NewMicroDecompressor(matrix uint64, imm8 uint8, perm [64]uint8) (*MicroDecompressor, error) {
	invMatrix, invImm8, ok := InvertAffineTransform(matrix, imm8)
	if !ok {
		return nil, errors.New("affine matrix is singular over GF(2)")
	}

	invPerm, err := InvertPermutation64(perm)
	if err != nil {
		return nil, err
	}

	md := &MicroDecompressor{
		Permutation:    perm,
		InvPermutation: invPerm,
		Matrix:         matrix,
		Imm8:           imm8,
		InvMatrix:      invMatrix,
		InvImm8:        invImm8,
	}

	// Precompute 256-entry in-register affine tables
	for x := 0; x < 256; x++ {
		md.ForwardTable[x] = GF2P8AffineQB(uint8(x), matrix, imm8)
		md.InverseTable[x] = GF2P8AffineQB(uint8(x), invMatrix, invImm8)
	}

	return md, nil
}

// CompressBlock64 processes a 64-byte chunk:
// Fused VBMI byte-permutation and GFNI affine symbol scrambling:
// dst[i] = ForwardTable[src[Permutation[i]]]
// Guarantees strictly zero heap allocations and zero intermediate buffers.
func (md *MicroDecompressor) CompressBlock64(dst, src []byte) {
	_ = dst[63]
	_ = src[63]

	for i := 0; i < 64; i += 8 {
		dst[i+0] = md.ForwardTable[src[md.Permutation[i+0]]]
		dst[i+1] = md.ForwardTable[src[md.Permutation[i+1]]]
		dst[i+2] = md.ForwardTable[src[md.Permutation[i+2]]]
		dst[i+3] = md.ForwardTable[src[md.Permutation[i+3]]]
		dst[i+4] = md.ForwardTable[src[md.Permutation[i+4]]]
		dst[i+5] = md.ForwardTable[src[md.Permutation[i+5]]]
		dst[i+6] = md.ForwardTable[src[md.Permutation[i+6]]]
		dst[i+7] = md.ForwardTable[src[md.Permutation[i+7]]]
	}
}

// DecompressBlock64 performs exact permutation annihilation and symbol restoration on 64 bytes:
// Fused inverse affine symbol restoration and VBMI permutation annihilation:
// dst[i] = InverseTable[src[InvPermutation[i]]]
// Guarantees strictly zero heap allocations and zero intermediate buffers.
func (md *MicroDecompressor) DecompressBlock64(dst, src []byte) {
	_ = dst[63]
	_ = src[63]

	for i := 0; i < 64; i += 8 {
		dst[i+0] = md.InverseTable[src[md.InvPermutation[i+0]]]
		dst[i+1] = md.InverseTable[src[md.InvPermutation[i+1]]]
		dst[i+2] = md.InverseTable[src[md.InvPermutation[i+2]]]
		dst[i+3] = md.InverseTable[src[md.InvPermutation[i+3]]]
		dst[i+4] = md.InverseTable[src[md.InvPermutation[i+4]]]
		dst[i+5] = md.InverseTable[src[md.InvPermutation[i+5]]]
		dst[i+6] = md.InverseTable[src[md.InvPermutation[i+6]]]
		dst[i+7] = md.InverseTable[src[md.InvPermutation[i+7]]]
	}
}

// DecompressStream decompresses an arbitrary stream of length multiple of 64 bytes.
// Strictly zero heap allocations.
func (md *MicroDecompressor) DecompressStream(dst, src []byte) {
	n := len(src)
	blocks := n / 64
	for b := 0; b < blocks; b++ {
		offset := b * 64
		md.DecompressBlock64(dst[offset:offset+64], src[offset:offset+64])
	}
}

// CompressStream compresses an arbitrary stream of length multiple of 64 bytes.
// Strictly zero heap allocations.
func (md *MicroDecompressor) CompressStream(dst, src []byte) {
	n := len(src)
	blocks := n / 64
	for b := 0; b < blocks; b++ {
		offset := b * 64
		md.CompressBlock64(dst[offset:offset+64], src[offset:offset+64])
	}
}

// NibbleDecode64 unpacks 32 bytes of packed 4-bit nibbles into 64 unpacked bytes.
// Input src: 32 bytes [ (n0 | n1<<4), (n2 | n3<<4), ... ]
// Output dst: 64 bytes [ n0, n1, n2, n3, ... ]
// In hardware, this is accomplished via VPERMB index shuffle and bitwise AND with 0x0F.
// Strictly zero heap allocations.
func NibbleDecode64(dst, src []byte) {
	_ = dst[63]
	_ = src[31]

	for i := 0; i < 32; i++ {
		b := src[i]
		dst[2*i] = b & 0x0F
		dst[2*i+1] = (b >> 4) & 0x0F
	}
}

// NibbleEncode64 packs 64 unpacked nibble bytes into 32 packed bytes.
// Strictly zero heap allocations.
func NibbleEncode64(dst, src []byte) {
	_ = dst[31]
	_ = src[63]

	for i := 0; i < 32; i++ {
		dst[i] = (src[2*i] & 0x0F) | ((src[2*i+1] & 0x0F) << 4)
	}
}
