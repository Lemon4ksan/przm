// Copyright (c) 2026 PRZM Swarm Team Silicon-GFNI. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package silicon_gfni

import (
	"bytes"
	"math/rand"
	"testing"
)

// Identity 8x8 matrix over GF(2) in 64-bit integer representation:
// Row i has bit i set => byte i is (1 << i).
const GF2IdentityMatrix uint64 = 0x8040201008040201

// Standard AES affine transformation matrix over GF(2):
// Row i: [1, 0, 0, 0, 1, 1, 1, 1] rotated by i positions.
const (
	AESAffineMatrix uint64 = 0xF87C3E1F8FC7E3F1
	AESAffineImm8   uint8  = 0x63
)

func TestGF256FieldAxioms(t *testing.T) {
	// Test multiplicative identity
	for a := range 256 {
		ua := uint8(a)
		if mul := GF256Mul(ua, 1); mul != ua {
			t.Fatalf("Multiplicative identity failed: %d * 1 = %d", ua, mul)
		}
		if mul := GF256Mul(1, ua); mul != ua {
			t.Fatalf("Multiplicative identity failed: 1 * %d = %d", ua, mul)
		}
		if mul := GF256Mul(ua, 0); mul != 0 {
			t.Fatalf("Zero multiplication failed: %d * 0 = %d", ua, mul)
		}
	}

	// Test multiplicative inverse for all non-zero elements
	for a := 1; a < 256; a++ {
		ua := uint8(a)
		inv := GF256Inv(ua)
		if inv == 0 {
			t.Fatalf("Non-zero element %d has zero inverse", ua)
		}
		prod := GF256Mul(ua, inv)
		if prod != 1 {
			t.Fatalf("Inverse failed: %d * %d = %d (expected 1)", ua, inv, prod)
		}
	}

	// Test that multiplication by any non-zero element is a bijection (permutation preserving)
	for a := 1; a < 256; a++ {
		ua := uint8(a)
		var seen [256]bool
		for x := range 256 {
			y := GF256Mul(ua, uint8(x))
			if seen[y] {
				t.Fatalf("Multiplication by %d is not injective: collision on %d", ua, y)
			}
			seen[y] = true
		}
		for i := range 256 {
			if !seen[i] {
				t.Fatalf("Multiplication by %d is not surjective: missed %d", ua, i)
			}
		}
	}
}

func TestGFNIIdentity(t *testing.T) {
	// Affine transform with identity matrix and imm8 = 0 must be identity
	for x := range 256 {
		ux := uint8(x)
		res := GF2P8AffineQB(ux, GF2IdentityMatrix, 0)
		if res != ux {
			t.Fatalf("GF2P8AffineQB identity failed for %02x: got %02x", ux, res)
		}
	}
}

func TestGFNIInvertMatrix(t *testing.T) {
	inv, ok := InvertGF2Matrix8x8(GF2IdentityMatrix)
	if !ok || inv != GF2IdentityMatrix {
		t.Fatalf("Inverse of identity must be identity, got ok=%v inv=%016x", ok, inv)
	}

	// Invert AES affine matrix
	invAES, ok := InvertGF2Matrix8x8(AESAffineMatrix)
	if !ok {
		t.Fatalf("AES affine matrix must be invertible")
	}

	// Verify A * A_inv = I
	for x := range 256 {
		ux := uint8(x)
		y := GF2P8AffineQB(ux, AESAffineMatrix, 0)
		z := GF2P8AffineQB(y, invAES, 0)
		if z != ux {
			t.Fatalf("Matrix roundtrip failed for %02x: got %02x", ux, z)
		}
	}
}

func TestGFNIAffineRoundtrip(t *testing.T) {
	// Test full affine roundtrip: x -> A*x ^ b -> A_inv*(y) ^ b_inv = x
	invMat, invImm, ok := InvertAffineTransform(AESAffineMatrix, AESAffineImm8)
	if !ok {
		t.Fatalf("InvertAffineTransform failed on AES parameters")
	}

	for x := range 256 {
		ux := uint8(x)
		forward := GF2P8AffineQB(ux, AESAffineMatrix, AESAffineImm8)
		reverse := GF2P8AffineQB(forward, invMat, invImm)
		if reverse != ux {
			t.Fatalf("Affine roundtrip failed for %02x: got %02x (forward=%02x)", ux, reverse, forward)
		}
	}
}

func TestVBMI_VPERMB(t *testing.T) {
	var src [64]byte
	var indices [64]byte
	var dst [64]byte

	for i := range 64 {
		src[i] = byte(i * 3)
		indices[i] = byte(63 - i) // Reverse permutation
	}

	VPERMB(dst[:], indices[:], src[:])

	for i := range 64 {
		expected := src[63-i]
		if dst[i] != expected {
			t.Fatalf("VPERMB mismatch at %d: got %d, expected %d", i, dst[i], expected)
		}
	}

	// Invert reverse permutation
	invPerm, err := InvertPermutation64(indices)
	if err != nil {
		t.Fatalf("InvertPermutation64 failed: %v", err)
	}

	var restored [64]byte
	VPERMB(restored[:], invPerm[:], dst[:])

	if !bytes.Equal(restored[:], src[:]) {
		t.Fatalf("VBMI permutation annihilation roundtrip failed")
	}
}

func TestVBMI_VPERMI2B(t *testing.T) {
	var src0 [64]byte
	var src1 [64]byte
	var indices [64]byte
	var dst [64]byte

	for i := range 64 {
		src0[i] = byte(i)
		src1[i] = byte(100 + i)
		if i%2 == 0 {
			indices[i] = byte(i) // Take from src0
		} else {
			indices[i] = byte(64 + i) // Take from src1
		}
	}

	VPERMI2B(dst[:], indices[:], src0[:], src1[:])

	for i := range 64 {
		var expected byte
		if i%2 == 0 {
			expected = src0[i]
		} else {
			expected = src1[i]
		}
		if dst[i] != expected {
			t.Fatalf("VPERMI2B mismatch at %d: got %d, expected %d", i, dst[i], expected)
		}
	}
}

func TestMicroDecompressorBlock64(t *testing.T) {
	// Create pseudo-random 64-byte permutation
	r := rand.New(rand.NewSource(42))
	permSlice := r.Perm(64)
	var perm [64]uint8
	for i := range 64 {
		perm[i] = uint8(permSlice[i])
	}

	md, err := NewMicroDecompressor(AESAffineMatrix, AESAffineImm8, perm)
	if err != nil {
		t.Fatalf("NewMicroDecompressor failed: %v", err)
	}

	var original [64]byte
	for i := range 64 {
		original[i] = byte(i*17 + 5)
	}

	var compressed [64]byte
	var decompressed [64]byte

	md.CompressBlock64(compressed[:], original[:])
	md.DecompressBlock64(decompressed[:], compressed[:])

	if !bytes.Equal(decompressed[:], original[:]) {
		t.Fatalf(
			"Permutation annihilation failed: decompressed does not match original\nGot:  %x\nWant: %x",
			decompressed,
			original,
		)
	}
}

func TestMicroDecompressorStreamFuzzing(t *testing.T) {
	r := rand.New(rand.NewSource(1337))

	for iter := range 10 {
		// Generate random invertible 8x8 GF(2) matrix
		var mat uint64
		for {
			mat = r.Uint64()
			if _, ok := InvertGF2Matrix8x8(mat); ok {
				break
			}
		}
		imm8 := uint8(r.Intn(256))

		permSlice := r.Perm(64)
		var perm [64]uint8
		for i := range 64 {
			perm[i] = uint8(permSlice[i])
		}

		md, err := NewMicroDecompressor(mat, imm8, perm)
		if err != nil {
			t.Fatalf("Failed to create micro-decompressor: %v", err)
		}

		// Test stream lengths of 64, 256, 1024, 65536 bytes
		for _, size := range []int{64, 256, 1024, 65536} {
			orig := make([]byte, size)
			r.Read(orig)

			comp := make([]byte, size)
			decomp := make([]byte, size)

			md.CompressStream(comp, orig)
			md.DecompressStream(decomp, comp)

			if !bytes.Equal(decomp, orig) {
				t.Fatalf("Fuzz iteration %d, size %d failed bit-exact match", iter, size)
			}
		}
	}
}

func TestNibbleCodec64(t *testing.T) {
	var original [64]byte
	for i := range 64 {
		original[i] = byte(i & 0x0F) // valid 4-bit nibbles
	}

	var packed [32]byte
	var unpacked [64]byte

	NibbleEncode64(packed[:], original[:])
	NibbleDecode64(unpacked[:], packed[:])

	if !bytes.Equal(unpacked[:], original[:]) {
		t.Fatalf("NibbleCodec64 roundtrip mismatch\nGot:  %x\nWant: %x", unpacked, original)
	}
}

func TestZeroAllocations(t *testing.T) {
	var perm [64]uint8
	for i := range 64 {
		perm[i] = uint8(63 - i)
	}

	md, err := NewMicroDecompressor(AESAffineMatrix, AESAffineImm8, perm)
	if err != nil {
		t.Fatalf("NewMicroDecompressor failed: %v", err)
	}

	var src [64]byte
	var dst [64]byte

	// 1. DecompressBlock64 zero allocations
	allocsBlock := testing.AllocsPerRun(100, func() {
		md.DecompressBlock64(dst[:], src[:])
	})
	if allocsBlock != 0 {
		t.Fatalf("DecompressBlock64 allocated memory: %v allocs/run (must be 0)", allocsBlock)
	}

	// 2. CompressBlock64 zero allocations
	allocsComp := testing.AllocsPerRun(100, func() {
		md.CompressBlock64(dst[:], src[:])
	})
	if allocsComp != 0 {
		t.Fatalf("CompressBlock64 allocated memory: %v allocs/run (must be 0)", allocsComp)
	}

	// 3. DecompressStream zero allocations
	streamSrc := make([]byte, 4096)
	streamDst := make([]byte, 4096)
	allocsStream := testing.AllocsPerRun(100, func() {
		md.DecompressStream(streamDst, streamSrc)
	})
	if allocsStream != 0 {
		t.Fatalf("DecompressStream allocated memory: %v allocs/run (must be 0)", allocsStream)
	}

	// 4. NibbleDecode64 zero allocations
	var packed [32]byte
	var unpacked [64]byte
	allocsNibble := testing.AllocsPerRun(100, func() {
		NibbleDecode64(unpacked[:], packed[:])
	})
	if allocsNibble != 0 {
		t.Fatalf("NibbleDecode64 allocated memory: %v allocs/run (must be 0)", allocsNibble)
	}
}

// ----------------------------------------------------------------------------
// Benchmarks
// ----------------------------------------------------------------------------

func BenchmarkDecompressBlock64(b *testing.B) {
	var perm [64]uint8
	for i := range 64 {
		perm[i] = uint8(63 - i)
	}
	md, _ := NewMicroDecompressor(AESAffineMatrix, AESAffineImm8, perm)

	var src [64]byte
	var dst [64]byte
	for i := range 64 {
		src[i] = byte(i)
	}

	b.SetBytes(64)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		md.DecompressBlock64(dst[:], src[:])
	}
}

func BenchmarkDecompressStream64KB(b *testing.B) {
	var perm [64]uint8
	for i := range 64 {
		perm[i] = uint8(63 - i)
	}
	md, _ := NewMicroDecompressor(AESAffineMatrix, AESAffineImm8, perm)

	size := 65536
	src := make([]byte, size)
	dst := make([]byte, size)
	for i := range size {
		src[i] = byte(i)
	}

	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		md.DecompressStream(dst, src)
	}
}

func BenchmarkDecompressStream1MB(b *testing.B) {
	var perm [64]uint8
	for i := range 64 {
		perm[i] = uint8(63 - i)
	}
	md, _ := NewMicroDecompressor(AESAffineMatrix, AESAffineImm8, perm)

	size := 1024 * 1024
	src := make([]byte, size)
	dst := make([]byte, size)
	for i := range size {
		src[i] = byte(i)
	}

	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		md.DecompressStream(dst, src)
	}
}

func BenchmarkVPERMB_64B(b *testing.B) {
	var src [64]byte
	var idx [64]byte
	var dst [64]byte
	for i := range 64 {
		src[i] = byte(i)
		idx[i] = byte(63 - i)
	}

	b.SetBytes(64)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		VPERMB(dst[:], idx[:], src[:])
	}
}

func BenchmarkGF2P8AffineQB_64B(b *testing.B) {
	var src [64]byte
	var dst [64]byte
	for i := range 64 {
		src[i] = byte(i)
	}

	b.SetBytes(64)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j := range 64 {
			dst[j] = GF2P8AffineQB(src[j], AESAffineMatrix, AESAffineImm8)
		}
	}
}

func BenchmarkNibbleDecode64(b *testing.B) {
	var src [32]byte
	var dst [64]byte
	for i := range 32 {
		src[i] = byte(i)
	}

	b.SetBytes(64)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		NibbleDecode64(dst[:], src[:])
	}
}

func BenchmarkGF256Mul_64B(b *testing.B) {
	var srcA [64]byte
	var srcB [64]byte
	var dst [64]byte
	for i := range 64 {
		srcA[i] = byte(i*7 + 1)
		srcB[i] = byte(i*13 + 3)
	}

	b.SetBytes(64)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j := range 64 {
			dst[j] = GF256Mul(srcA[j], srcB[j])
		}
	}
}
