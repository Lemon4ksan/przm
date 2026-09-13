// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// TestSymplectic_Theorem30_InvolutionAxiom verifies the core mathematical identity:
// T_S^2 == I over Sp(4, F_{2^8}). Applying the shear transvection twice must be
// strictly the identity transformation for ANY symmetric S and ANY 4-phase vector.
func TestSymplectic_Theorem30_InvolutionAxiom(t *testing.T) {
	testShears := []SymplecticShear{
		{S00: 0, S01: 0, S11: 0},          // Identity
		{S00: 1, S01: 0, S11: 1},          // Unit diagonal
		{S00: 0, S01: 1, S11: 0},          // Cross-shear
		{S00: 0x57, S01: 0xC3, S11: 0x8A}, // Arbitrary Galois field elements
		{S00: 0xFF, S01: 0x01, S11: 0xFE},
	}

	for _, s := range testShears {
		// Test representative vector states across the entire 4D phase space
		for q0 := 0; q0 < 256; q0 += 51 {
			for q1 := 0; q1 < 256; q1 += 53 {
				for p0 := 0; p0 < 256; p0 += 37 {
					for p1 := 0; p1 < 256; p1 += 41 {
						q0b, q1b, p0b, p1b := uint8(q0), uint8(q1), uint8(p0), uint8(p1)

						// First application T_S
						tq0, tq1, tp0, tp1 := s.Apply4(q0b, q1b, p0b, p1b)

						// Momentum must be invariant
						if tp0 != p0b || tp1 != p1b {
							t.Fatalf("Momentum non-invariance: (%d, %d) -> (%d, %d)", p0b, p1b, tp0, tp1)
						}

						// Second application T_S
						rq0, rq1, rp0, rp1 := s.Apply4(tq0, tq1, tp0, tp1)

						// Must identically recover original coordinates
						if rq0 != q0b || rq1 != q1b || rp0 != p0b || rp1 != p1b {
							t.Fatalf("Involution failed for S=%+v: got (%d,%d,%d,%d), expected (%d,%d,%d,%d)",
								s, rq0, rq1, rp0, rp1, q0b, q1b, p0b, p1b)
						}
					}
				}
			}
		}
	}
}

// TestSymplectic_InplaceInvolution tests pipelined SIMD/loop-unrolled block transformation.
func TestSymplectic_InplaceInvolution(t *testing.T) {
	sizes := []int{16, 64, 256, 1024, 65536}
	shear := SymplecticShear{S00: 0x3F, S01: 0x91, S11: 0x5A}

	for _, sz := range sizes {
		orig := make([]byte, sz)
		rand.Read(orig)

		buf := make([]byte, sz)
		copy(buf, orig)

		// Transform forward
		shear.TransformBlockInplace(buf)

		// buf should be transformed (not equal unless unlucky)
		if bytes.Equal(buf, orig) {
			t.Fatalf("Transform had no effect on block size %d", sz)
		}

		// Transform backward (involution: identical call!)
		shear.TransformBlockInplace(buf)

		if !bytes.Equal(buf, orig) {
			t.Fatalf("Inplace involution failed for size %d", sz)
		}
	}
}

// TestSymplectic_Roundtrip verifies CompressSymplectic -> DecompressSymplectic roundtrip.
func TestSymplectic_Roundtrip(t *testing.T) {
	t.Run("ShortBuffers", func(t *testing.T) {
		for sz := 0; sz < 16; sz++ {
			data := make([]byte, sz)
			for i := range data {
				data[i] = byte(i + 1)
			}
			compressed := CompressSymplectic(data)
			decompressed, err := DecompressSymplectic(compressed, nil)
			if err != nil {
				t.Fatalf("Decompress failed for size %d: %v", sz, err)
			}
			if !bytes.Equal(decompressed, data) {
				t.Fatalf("Roundtrip mismatch for short buffer size %d", sz)
			}
		}
	})

	t.Run("CorrelatedPhaseData", func(t *testing.T) {
		// Construct synthetic 4D phase space data where q is strongly correlated with p:
		// q0 = p0 * 1, q1 = p1 * 1
		n := 8192
		data := make([]byte, n)
		for i := 0; i < n; i += 4 {
			p0 := uint8(i & 0xFF)
			p1 := uint8((i * 3) & 0xFF)
			q0 := p0 // Correlation
			q1 := p1 // Correlation
			data[i] = q0
			data[i+1] = q1
			data[i+2] = p0
			data[i+3] = p1
		}

		compressed := CompressSymplectic(data)
		decompressed, err := DecompressSymplectic(compressed, nil)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Fatalf("Roundtrip mismatch on correlated phase data")
		}
	})

	t.Run("RandomUniformNoise", func(t *testing.T) {
		n := 16384
		data := make([]byte, n)
		rand.Read(data)

		compressed := CompressSymplectic(data)
		decompressed, err := DecompressSymplectic(compressed, nil)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(decompressed, data) {
			t.Fatalf("Roundtrip mismatch on random uniform noise")
		}
	})

	t.Run("ErrorCases", func(t *testing.T) {
		// Header too short
		if _, err := DecompressSymplectic([]byte{1, 2, 3}, nil); err != ErrSymplecticCorrupt {
			t.Fatalf("Expected ErrSymplecticCorrupt for short header, got %v", err)
		}

		// Bad magic
		badMagic := make([]byte, 16)
		if _, err := DecompressSymplectic(badMagic, nil); err != ErrSymplecticCorrupt {
			t.Fatalf("Expected ErrSymplecticCorrupt for bad magic, got %v", err)
		}
	})
}

// TestTwistor_Theorem31_DyadFactorization verifies twistor dyad factorization
// on null-cone Minkowski 4-vectors.
func TestTwistor_Theorem31_DyadFactorization(t *testing.T) {
	// 1. First test gfSqrt correctness for all 256 field elements
	for x := 0; x < 256; x++ {
		xb := uint8(x)
		root := gfSqrt(xb)
		sq := GF256Mul(root, root)
		if sq != xb {
			t.Fatalf("gfSqrt failure: sqrt(%d) = %d, root^2 = %d", xb, root, sq)
		}
	}

	// 2. Test TwistorDyad forward expand and factorization
	for pi0 := 0; pi0 < 256; pi0 += 17 {
		for pi1 := 0; pi1 < 256; pi1 += 19 {
			dyad := TwistorDyad{Pi0: uint8(pi0), Pi1: uint8(pi1)}
			x0, x1, x2, x3 := dyad.Expand()

			// Check Minkowski null cone: x0^2 ^ x1^2 ^ x2^2 ^ x3^2 == 0?
			mink := GF256Mul(x0, x0) ^ GF256Mul(x1, x1) ^ GF256Mul(x2, x2) ^ GF256Mul(x3, x3)
			if mink == 0 {
				factored, ok := FactorizeTwistor(x0, x1, x2, x3)
				if !ok {
					t.Fatalf("FactorizeTwistor returned ok=false for null-cone vector (%d,%d,%d,%d)", x0, x1, x2, x3)
				}
				// Verify reconstructed coordinates match
				rx0, rx1, rx2, rx3 := factored.Expand()
				if rx0 != x0 || rx1 != x1 || rx2 != x2 || rx3 != x3 {
					t.Fatalf("Dyad expansion mismatch: (%d,%d,%d,%d) vs (%d,%d,%d,%d)", rx0, rx1, rx2, rx3, x0, x1, x2, x3)
				}
			}
		}
	}

	// 3. Test non-null vector returns ok = false
	// Vector (1, 0, 0, 0) has det P = 1 != 0
	_, ok := FactorizeTwistor(1, 0, 0, 0)
	if ok {
		t.Fatalf("Expected non-null vector (1,0,0,0) to fail twistor factorization")
	}
}

// TestSymplectic_EndToEndPipeline verifies full integration into przm.Compress and przm.Decompress.
func TestSymplectic_EndToEndPipeline(t *testing.T) {
	n := 4096
	data := make([]byte, n)
	for i := 0; i < n; i += 4 {
		p0 := uint8(i & 0xFF)
		p1 := uint8((i * 7) & 0xFF)
		q0 := p0 ^ 0x33
		q1 := p1 ^ 0x55
		data[i] = q0
		data[i+1] = q1
		data[i+2] = p0
		data[i+3] = p1
	}

	compressed, err := Compress(data, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(decompressed, data) {
		t.Fatalf("End-to-end Compress/Decompress mismatch for Phase 12 data: got len %d, want %d", len(decompressed), len(data))
	}
}

// BenchmarkDecompressSymplectic_Throughput measures the decompression speed of the
// symplectic involutive transvection. By Theorem 30, it requires 0 heap allocations
// and achieves extreme throughput.
func BenchmarkDecompressSymplectic_Throughput(b *testing.B) {
	n := 64 * 1024
	orig := make([]byte, n)
	rand.Read(orig)

	compressed := CompressSymplectic(orig)
	dst := make([]byte, n)

	b.SetBytes(int64(n))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := DecompressSymplectic(compressed, dst); err != nil {
			b.Fatalf("Decompress failed: %v", err)
		}
	}
}
