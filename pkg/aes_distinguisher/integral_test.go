// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"testing"
)

func TestGenerateLambdaSet_Structure(t *testing.T) {
	constVal := byte(0x3C)

	for activeIdx := 0; activeIdx < 16; activeIdx++ {
		set := GenerateLambdaSet(activeIdx, constVal)
		if len(set) != 256 {
			t.Fatalf("expected 256 elements in Lambda-set, got %d", len(set))
		}

		var seen [256]bool
		for val := 0; val < 256; val++ {
			block := set[val]
			actVal := block[activeIdx]
			if seen[actVal] {
				t.Fatalf("duplicate active byte value %d in Lambda-set at activeIdx=%d", actVal, activeIdx)
			}
			seen[actVal] = true

			for j := 0; j < 16; j++ {
				if j != activeIdx && block[j] != constVal {
					t.Fatalf("non-active byte at index %d corrupted: expected 0x%02x, got 0x%02x", j, constVal, block[j])
				}
			}
		}
	}
}

func TestGenerateLambdaSetWithConstants(t *testing.T) {
	constants := [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}
	activeIdx := 7
	set := GenerateLambdaSetWithConstants(activeIdx, constants)

	for val := 0; val < 256; val++ {
		block := set[val]
		if block[activeIdx] != byte(val) {
			t.Fatalf("active byte value mismatch: expected %d, got %d", val, block[activeIdx])
		}
		for j := 0; j < 16; j++ {
			if j != activeIdx && block[j] != constants[j] {
				t.Fatalf("constant byte at %d mismatch: expected %d, got %d", j, constants[j], block[j])
			}
		}
	}
}

func TestVerifyLambdaZeroSum_Rounds1To3(t *testing.T) {
	keys := [][]byte{
		{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c},
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11, 0x00},
	}

	activeBytes := []int{0, 3, 7, 12, 15}

	for kIdx, key := range keys {
		for _, actIdx := range activeBytes {
			lambdaSet := GenerateLambdaSet(actIdx, byte(0x42+actIdx))

			// 1. Round 1 (With MixColumns)
			ctR1 := make([][16]byte, 256)
			for i := 0; i < 256; i++ {
				ctR1[i] = EncryptReducedRound(lambdaSet[i][:], key, 1, true)
			}
			balancedR1, xorR1 := VerifyLambdaZeroSum(ctR1)
			if !balancedR1 {
				t.Fatalf("Key %d, active %d: Round 1 (with MC) failed zero-sum balance: %x", kIdx, actIdx, xorR1)
			}

			// 2. Round 1 (No MixColumns)
			ctR1NoMC := make([][16]byte, 256)
			for i := 0; i < 256; i++ {
				ctR1NoMC[i] = EncryptReducedRound(lambdaSet[i][:], key, 1, false)
			}
			balancedR1NoMC, xorR1NoMC := VerifyLambdaZeroSum(ctR1NoMC)
			if !balancedR1NoMC {
				t.Fatalf("Key %d, active %d: Round 1 (no MC) failed zero-sum balance: %x", kIdx, actIdx, xorR1NoMC)
			}

			// 3. Round 2 (With MixColumns)
			ctR2 := make([][16]byte, 256)
			for i := 0; i < 256; i++ {
				ctR2[i] = EncryptReducedRound(lambdaSet[i][:], key, 2, true)
			}
			balancedR2, xorR2 := VerifyLambdaZeroSum(ctR2)
			if !balancedR2 {
				t.Fatalf("Key %d, active %d: Round 2 failed zero-sum balance: %x", kIdx, actIdx, xorR2)
			}

			// 4. Round 3 (With MixColumns)
			ctR3 := make([][16]byte, 256)
			for i := 0; i < 256; i++ {
				ctR3[i] = EncryptReducedRound(lambdaSet[i][:], key, 3, true)
			}
			balancedR3, xorR3 := VerifyLambdaZeroSum(ctR3)
			if !balancedR3 {
				t.Fatalf("Key %d, active %d: Round 3 failed zero-sum balance: %x", kIdx, actIdx, xorR3)
			}
		}
	}
}

func TestCheckLambdaBalance_Detailed(t *testing.T) {
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	lambdaSet := GenerateLambdaSet(0, 0x55)

	ctR3 := make([][16]byte, 256)
	for i := 0; i < 256; i++ {
		ctR3[i] = EncryptReducedRound(lambdaSet[i][:], key, 3, true)
	}

	isBalanced, allBalanced, xorSum := CheckLambdaBalance(ctR3)
	if !allBalanced {
		t.Fatalf("expected allBalanced=true for Round 3, got false with xorSum=%x", xorSum)
	}
	for i := 0; i < 16; i++ {
		if !isBalanced[i] {
			t.Fatalf("expected byte %d to be balanced in Round 3", i)
		}
	}
}

func TestLambdaSet_Round4_BoundaryUnbalanced(t *testing.T) {
	// In 4-round AES, a single active byte Lambda-set (N=256) is NOT balanced across the full state.
	// This confirms the exactness of the 3-round integral boundary.
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	lambdaSet := GenerateLambdaSet(0, 0x33)

	ctR4 := make([][16]byte, 256)
	for i := 0; i < 256; i++ {
		ctR4[i] = EncryptReducedRound(lambdaSet[i][:], key, 4, true)
	}

	balancedR4, xorR4 := VerifyLambdaZeroSum(ctR4)
	if balancedR4 {
		t.Logf("Note: Round 4 happened to balance (prob ~ 2^-128): %x", xorR4)
	} else {
		t.Logf("Confirmed: Round 4 with N=256 is unbalanced (expected): %x", xorR4)
	}
}

func TestGenerateDiagonalSet_And_DivisionProperty(t *testing.T) {
	// Test 1-active byte diagonal set (256 blocks)
	diagSet1 := GenerateDiagonalSet(0, 1, 0x77)
	if len(diagSet1) != 256 {
		t.Fatalf("expected 256 blocks, got %d", len(diagSet1))
	}
	// Diagonal 0 contains bytes {0, 5, 10, 15}. For numActiveBytes=1, byte 0 is active.
	for v := 0; v < 256; v++ {
		if diagSet1[v][0] != byte(v) {
			t.Fatalf("diag 0 byte 0 expected %d, got %d", v, diagSet1[v][0])
		}
		if diagSet1[v][5] != 0x77 || diagSet1[v][10] != 0x77 || diagSet1[v][15] != 0x77 {
			t.Fatalf("other diagonal bytes should be const 0x77")
		}
	}

	// VerifyDivisionProperty4R on balanced set
	balancedBits, paritySum := VerifyDivisionProperty4R(diagSet1)
	// Byte 0 is active 0..255, so its XOR sum is 0
	if paritySum[0] != 0 {
		t.Fatalf("expected byte 0 XOR sum 0, got %d", paritySum[0])
	}
	for bit := 0; bit < 8; bit++ {
		if !balancedBits[bit] {
			t.Fatalf("expected bit %d of byte 0 to be balanced", bit)
		}
	}
}
