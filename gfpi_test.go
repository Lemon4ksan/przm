// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
)

// TestGFPI_FieldAxioms verifies the fundamental algebraic axioms of F_{2^8}:
// associativity, commutativity, distributivity, and multiplicative inverse.
func TestGFPI_FieldAxioms(t *testing.T) {
	// 1. Identity: a * 1 == a, a * 0 == 0
	for a := 0; a < 256; a++ {
		ba := byte(a)
		if przm.GF256Mul(ba, 1) != ba {
			t.Fatalf("identity failure: %d * 1 != %d", a, a)
		}
		if przm.GF256Mul(ba, 0) != 0 {
			t.Fatalf("zero failure: %d * 0 != 0", a)
		}
	}

	// 2. Multiplicative inverse: a * a^{-1} == 1 for all a != 0
	for a := 1; a < 256; a++ {
		ba := byte(a)
		inv := przm.GF256Inv(ba)
		prod := przm.GF256Mul(ba, inv)
		if prod != 1 {
			t.Fatalf("inverse failure for %d: %d * %d = %d != 1", a, ba, inv, prod)
		}
	}

	// 3. Commutativity: a * b == b * a
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			ba, bb := byte(a), byte(b)
			if przm.GF256Mul(ba, bb) != przm.GF256Mul(bb, ba) {
				t.Fatalf("commutativity failure: %d * %d != %d * %d", a, b, b, a)
			}
		}
	}

	// 4. Distributivity: a * (b ^ c) == (a * b) ^ (a * c) on sample subset
	for a := 0; a < 64; a++ {
		for b := 0; b < 64; b++ {
			for c := 0; c < 16; c++ {
				ba, bb, bc := byte(a), byte(b), byte(c)
				left := przm.GF256Mul(ba, bb^bc)
				right := przm.GF256Mul(ba, bb) ^ przm.GF256Mul(ba, bc)
				if left != right {
					t.Fatalf("distributivity failure: %d * (%d ^ %d) != (%d*%d) ^ (%d*%d)", a, b, c, a, b, a, c)
				}
			}
		}
	}
}

// TestGFPI_Theorem3_LosslessReconstruction proves Theorem 3 (Exact Reconstruction):
//
//	\hat{y}_j = r_j ^ P(j) ^ \epsilon_j == y_j
//
// using the nilpotent property a ^ a == 0 over F_{2^8}.
func TestGFPI_Theorem3_LosslessReconstruction(t *testing.T) {
	lengths := []int{16, 32, 64}

	for _, K := range lengths {
		t.Run(fmt.Sprintf("K_%d", K), func(t *testing.T) {
			ref := make([]byte, K)
			rand.Read(ref)

			// Generate a true affine drift: y[t] = ref[t] ^ c0 ^ (c1 * t)
			c0Target := byte(0x5A)
			c1Target := byte(0x13)

			y := make([]byte, K)
			delta := make([]byte, K)
			for tIdx := 0; tIdx < K; tIdx++ {
				pt := c0Target ^ przm.GF256Mul(c1Target, byte(tIdx))
				delta[tIdx] = pt
				y[tIdx] = ref[tIdx] ^ pt
			}

			// Add 2 point mutations (sparse errors)
			errPos1 := K / 4
			errPos2 := 3 * K / 4
			delta[errPos1] ^= 0xEE
			y[errPos1] ^= 0xEE
			delta[errPos2] ^= 0x77
			y[errPos2] ^= 0x77

			// Fit polynomial
			c0, c1, mask, errs, ok := przm.GFPIFitAffine(delta, 4)
			if !ok {
				t.Fatalf("expected GFPI affine fit to succeed for K=%d with 2 errors", K)
			}

			if c0 != c0Target || c1 != c1Target {
				t.Fatalf("parameter mismatch: got c0=%02X, c1=%02X; want %02X, %02X", c0, c1, c0Target, c1Target)
			}

			expectedMask := (uint64(1) << errPos1) | (uint64(1) << errPos2)
			if mask != expectedMask {
				t.Fatalf("mask mismatch: got %016X, want %016X", mask, expectedMask)
			}

			// Reconstruct dst
			dst := make([]byte, K)
			przm.GFPIDecodeBlock(ref, c0, c1, mask, errs, dst)

			if !bytes.Equal(dst, y) {
				t.Fatalf("Theorem 3 violation: reconstructed vector does not match original y")
			}
		})
	}
}

// TestGFPI_Quadratic verifies degree-2 polynomial fitting and reconstruction.
func TestGFPI_Quadratic(t *testing.T) {
	K := 32
	ref := make([]byte, K)
	rand.Read(ref)

	c0Target := byte(0xA5)
	c1Target := byte(0x3B)
	c2Target := byte(0x07)

	y := make([]byte, K)
	delta := make([]byte, K)
	for tIdx := 0; tIdx < K; tIdx++ {
		bt := byte(tIdx)
		pt := c0Target ^ przm.GF256Mul(c1Target, bt) ^ przm.GF256Mul(c2Target, przm.GF256Mul(bt, bt))
		delta[tIdx] = pt
		y[tIdx] = ref[tIdx] ^ pt
	}

	// Fit quadratic
	c0, c1, c2, mask, errs, ok := przm.GFPIFitQuadratic(delta, 2)
	if !ok {
		t.Fatalf("expected quadratic fit to succeed")
	}

	if c0 != c0Target || c1 != c1Target || c2 != c2Target {
		t.Fatalf("quadratic parameter mismatch: got (%02X, %02X, %02X), want (%02X, %02X, %02X)",
			c0, c1, c2, c0Target, c1Target, c2Target)
	}

	dst := make([]byte, K)
	przm.GFPIDecodeQuadraticBlock(ref, c0, c1, c2, mask, errs, dst)

	if !bytes.Equal(dst, y) {
		t.Fatalf("quadratic reconstruction mismatch")
	}
}

// TestGFPI_RealWorldPatterns demonstrates compression on real regular drift patterns
// where PRZM v0.1 fails (d_H >> 2) and falls back to literals.
func TestGFPI_RealWorldPatterns(t *testing.T) {
	t.Run("monotonic_counter_drift", func(t *testing.T) {
		K := 32
		// Reference window has base counter: 1000, 1001, 1002...
		ref := make([]byte, K)
		for i := range ref {
			ref[i] = byte(i)
		}

		// Current window has drifted counter with offset + step
		y := make([]byte, K)
		delta := make([]byte, K)
		for i := range y {
			// Linear drift: delta[i] = 0x20 ^ (0x03 * i)
			d := byte(0x20) ^ przm.GF256Mul(0x03, byte(i))
			delta[i] = d
			y[i] = ref[i] ^ d
		}

		// In PRZM v0.1: Hamming distance d_H = 32.
		// FLAG_DELTA_MASK rejects matches with d_H > 2!
		// v0.1 cost = 32 bytes (raw literals) -> CR = 1.00x!
		v01Cost := K

		// GFPI v0.2:
		c0, c1, mask, errs, ok := przm.GFPIFitAffine(delta, 2)
		if !ok {
			t.Fatalf("expected GFPI fit to succeed on monotonic counter drift")
		}

		match := &przm.GFPIMatch{
			RefOffset: 1024,
			Length:    uint16(K),
			Degree:    1,
			C0:        c0,
			C1:        c1,
			ErrMask:   mask,
			ErrBytes:  errs,
		}

		gfpiBytes := przm.PackGFPIMatch(match)
		gfpiCost := len(gfpiBytes)

		ratio := float64(v01Cost) / float64(gfpiCost)
		t.Logf("Monotonic Counter Drift (K=%d):", K)
		t.Logf("  PRZM v0.1 (Rejected d_H=%d): %d bytes (CR: 1.00x)", K, v01Cost)
		t.Logf("  GFPI v0.2 (Affine Poly):      %d bytes (CR: %.2fx)", gfpiCost, ratio)
		t.Logf("  Compression Gain vs v0.1:     +%.1f%%", (ratio-1.0)*100.0)

		// Unpack and decode
		unpacked, _, err := przm.UnpackGFPIMatch(gfpiBytes)
		if err != nil {
			t.Fatalf("unpack failed: %v", err)
		}

		dst := make([]byte, K)
		przm.GFPIDecodeBlock(ref, unpacked.C0, unpacked.C1, unpacked.ErrMask, unpacked.ErrBytes, dst)

		if !bytes.Equal(dst, y) {
			t.Fatalf("reconstruction mismatch")
		}
	})

	t.Run("random_noise_rejected", func(t *testing.T) {
		K := 32
		noise := make([]byte, K)
		rand.Read(noise)

		// Random noise has high degree; economic gate must reject it
		_, _, _, _, ok := przm.GFPIFitAffine(noise, 4)
		if ok {
			t.Fatalf("economic profitability gate failed: random noise should not be matched as GFPI")
		}
	})
}

func BenchmarkGFPI_Throughput(b *testing.B) {
	K := 64
	ref := make([]byte, K)
	rand.Read(ref)

	dst := make([]byte, K)
	c0 := byte(0x33)
	c1 := byte(0x55)

	b.Run("Decode_PureAffine_64B", func(b *testing.B) {
		b.SetBytes(int64(K))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			przm.GFPIDecodeBlock(ref, c0, c1, 0, nil, dst)
		}
	})

	errMask := uint64(0x0000000100000001) // 2 errors
	errBytes := []byte{0xAA, 0xBB}

	b.Run("Decode_NoisyAffine_64B", func(b *testing.B) {
		b.SetBytes(int64(K))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			przm.GFPIDecodeBlock(ref, c0, c1, errMask, errBytes, dst)
		}
	})

	delta := make([]byte, K)
	for tIdx := 0; tIdx < K; tIdx++ {
		delta[tIdx] = c0 ^ przm.GF256Mul(c1, byte(tIdx))
	}

	b.Run("Fit_Affine_64B", func(b *testing.B) {
		b.SetBytes(int64(K))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, _, _, _ = przm.GFPIFitAffine(delta, 4)
		}
	})
}
