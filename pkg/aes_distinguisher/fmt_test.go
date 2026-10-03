// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"crypto/rand"
	"testing"
)

// TestFMT_Involution proves that Fast Möbius Transform over GF(2) is an involution:
// FMT(FMT(T)) == T for any truth table.
func TestFMT_Involution(t *testing.T) {
	for trial := range 20 {
		var orig [32]byte
		_, _ = rand.Read(orig[:])

		tt := orig
		FastMobiusTransform8(&tt)
		FastMobiusTransform8(&tt)

		if tt != orig {
			t.Fatalf("Trial %d: FMT is not an involution! Got %x, want %x", trial, tt, orig)
		}
	}
}

// TestFMT_SBoxCoordinateDegrees rigorously verifies:
// 1. Every S-box coordinate has algebraic degree exactly 7.
// 2. The degree-8 monomial x0*x1*...*x7 is identically zero (a_{255} == 0) across all 8 coordinates.
// 3. Monomial counts and sparsity match the exact survey spectrum.
func TestFMT_SBoxCoordinateDegrees(t *testing.T) {
	sbox := GetSBox()
	profiles := AnalyzeSBox(sbox)

	expectedActive := [8]int{132, 133, 145, 136, 131, 114, 112, 110}
	expectedDeg7 := [8]int{3, 4, 5, 4, 3, 2, 2, 2}

	totalDeg7 := 0
	for k := range 8 {
		p := profiles[k]

		// 1. Degree must be exactly 7
		if p.Degree != 7 {
			t.Errorf("Coordinate S_%d degree = %d, expected 7", k, p.Degree)
		}

		// 2. Degree 8 monomial must vanish (a_{255} == 0)
		if !p.TopMonomialZero {
			t.Errorf("Coordinate S_%d has non-zero degree-8 monomial (a_255 != 0)", k)
		}
		if p.DegreeHistogram[8] != 0 {
			t.Errorf("Coordinate S_%d degree 8 count = %d, expected 0", k, p.DegreeHistogram[8])
		}
		if !p.HasDegreeDefect {
			t.Errorf("Coordinate S_%d failed HasDegreeDefect", k)
		}

		// 3. Check active monomial count
		if p.ActiveMonomials != expectedActive[k] {
			t.Errorf("Coordinate S_%d active monomials = %d, expected %d", k, p.ActiveMonomials, expectedActive[k])
		}

		// 4. Check degree 7 monomial count
		if p.DegreeHistogram[7] != expectedDeg7[k] {
			t.Errorf("Coordinate S_%d degree 7 count = %d, expected %d", k, p.DegreeHistogram[7], expectedDeg7[k])
		}
		totalDeg7 += p.DegreeHistogram[7]
	}

	// Total degree 7 monomials across all 8 coordinates must be 25
	if totalDeg7 != 25 {
		t.Errorf("Total degree 7 monomials across all coordinates = %d, expected 25", totalDeg7)
	}
}

// TestFMT_AllComponentFunctions verifies that for ALL 255 non-zero linear combinations
// f_c(x) = c . S(x), the degree is at most 7 and a_{255} == 0.
func TestFMT_AllComponentFunctions(t *testing.T) {
	sbox := GetSBox()

	for mask := 1; mask < 256; mask++ {
		tt := ComponentFunctionTruthTable(sbox, byte(mask))
		anf, profile := AnalyzeBooleanFunction(tt)

		if !profile.TopMonomialZero {
			t.Fatalf("Component function with mask %02x has non-zero a_255!", mask)
		}
		if profile.Degree > 7 {
			t.Fatalf("Component function with mask %02x has degree %d > 7!", mask, profile.Degree)
		}

		degViaHelper := ComputeANFDegree(anf[:])
		if degViaHelper != profile.Degree {
			t.Fatalf("ComputeANFDegree mismatch for mask %02x: got %d, want %d", mask, degViaHelper, profile.Degree)
		}
	}
}

// TestFMT_HelperFunctions tests FastMobiusTransform slice wrapper and ComputeANFDegree.
func TestFMT_HelperFunctions(t *testing.T) {
	// 1. Constant function f(x) = 1 for all x
	var ttConst1 [32]byte
	for i := range ttConst1 {
		ttConst1[i] = 0xFF
	}
	anfConst := FastMobiusTransform(ttConst1[:])
	if len(anfConst) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(anfConst))
	}
	// Constant 1 has a_0 = 1, all other a_u = 0
	if anfConst[0] != 0x01 {
		t.Errorf("expected anfConst[0] == 0x01, got %02x", anfConst[0])
	}
	for i := 1; i < 32; i++ {
		if anfConst[i] != 0 {
			t.Errorf("expected anfConst[%d] == 0, got %02x", i, anfConst[i])
		}
	}
	deg0 := ComputeANFDegree(anfConst)
	if deg0 != 0 {
		t.Errorf("expected degree 0 for constant-1 function, got %d", deg0)
	}

	// 2. Degree 1 function: f(x) = x_0
	// f(x) = 1 when bit 0 of x is 1 (i.e. x is odd)
	// In each byte, odd bit positions 1, 3, 5, 7 have value 1 -> 0xAA
	var ttDeg1 [32]byte
	for i := range ttDeg1 {
		ttDeg1[i] = 0xAA
	}
	anfDeg1 := FastMobiusTransform(ttDeg1[:])
	deg1 := ComputeANFDegree(anfDeg1)
	if deg1 != 1 {
		t.Errorf("expected degree 1 for f(x)=x0, got %d", deg1)
	}
	// ANF has only a_1 = 1 (bit 1 of byte 0 -> 0x02)
	if anfDeg1[0] != 0x02 {
		t.Errorf("expected anfDeg1[0] == 0x02, got %02x", anfDeg1[0])
	}

	// 3. Degree 2 function: f(x) = x_0 * x_1
	// f(x) = 1 when bits 0 and 1 are both 1 (x mod 4 == 3)
	// In each byte, positions 3 and 7 are 1 -> 0x88
	var ttDeg2 [32]byte
	for i := range ttDeg2 {
		ttDeg2[i] = 0x88
	}
	anfDeg2 := FastMobiusTransform(ttDeg2[:])
	deg2 := ComputeANFDegree(anfDeg2)
	if deg2 != 2 {
		t.Errorf("expected degree 2 for f(x)=x0*x1, got %d", deg2)
	}
	// ANF has only a_3 = 1 (bit 3 of byte 0 -> 0x08)
	if anfDeg2[0] != 0x08 {
		t.Errorf("expected anfDeg2[0] == 0x08, got %02x", anfDeg2[0])
	}

	// 4. Degree 8 function: f(x) = 1 only when x = 255
	// This corresponds to truth table with only bit 255 set (bit 7 of byte 31)
	var ttDeg8 [32]byte
	ttDeg8[31] = 0x80
	anfDeg8 := FastMobiusTransform(ttDeg8[:])
	deg8 := ComputeANFDegree(anfDeg8)
	if deg8 != 8 {
		t.Errorf("expected degree 8, got %d", deg8)
	}
}

// BenchmarkFastMobiusTransform8 benchmarks the in-place Radix-2 FMT over GF(2^8).
func BenchmarkFastMobiusTransform8(b *testing.B) {
	var tt [32]byte
	_, _ = rand.Read(tt[:])

	b.SetBytes(32)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		FastMobiusTransform8(&tt)
	}
}
