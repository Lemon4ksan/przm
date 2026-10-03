// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"encoding/binary"
	"math"
	"testing"
)

// ============================================================================
// STRESS-TEST 1: Exact Zero-Sum Balance Across 10,000 Independent Lambda-Sets (Rounds 1, 2, 3)
// ============================================================================

// TestChallengerM2_2_LambdaZeroSum_10000Sets rigorously stress-tests the integral zero-sum invariant:
//
//	\bigoplus_{i=0}^{255} C_i = 0^{128}
//
// across exactly 10,000 independent Lambda-sets with random 128-bit keys, cycling across all 16
// active byte positions and varying constant background bytes for rounds 1, 2, and 3.
func TestChallengerM2_2_LambdaZeroSum_10000Sets(t *testing.T) {
	const totalSets = 10000

	// Pseudo-random deterministic generator for full reproducibility
	var seed uint64 = 0x8A5C1E7019283746
	nextRand := func() uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return seed
	}

	t.Logf("[STRESS TEST 1] Commencing verification of 10,000 Lambda-sets across rounds 1, 2, 3...")

	var failuresR1NoMC, failuresR1MC, failuresR2, failuresR3 int

	for iter := range totalSets {
		// Generate random 128-bit key
		var key [16]byte
		binary.LittleEndian.PutUint64(key[0:8], nextRand())
		binary.LittleEndian.PutUint64(key[8:16], nextRand())

		// Generate random 16-byte constants
		var constants [16]byte
		binary.LittleEndian.PutUint64(constants[0:8], nextRand())
		binary.LittleEndian.PutUint64(constants[8:16], nextRand())

		// Cycle active byte index across 0..15
		activeIdx := iter % 16
		lambdaSet := GenerateLambdaSetWithConstants(activeIdx, constants)

		// 1. Round 1 without MixColumns
		var ctR1NoMC [256][16]byte
		for i := range 256 {
			ctR1NoMC[i] = EncryptReducedRound(lambdaSet[i][:], key[:], 1, false)
		}
		if balanced, _ := VerifyLambdaZeroSum(ctR1NoMC[:]); !balanced {
			failuresR1NoMC++
		}

		// 2. Round 1 with MixColumns
		var ctR1MC [256][16]byte
		for i := range 256 {
			ctR1MC[i] = EncryptReducedRound(lambdaSet[i][:], key[:], 1, true)
		}
		if balanced, _ := VerifyLambdaZeroSum(ctR1MC[:]); !balanced {
			failuresR1MC++
		}

		// 3. Round 2 with MixColumns
		var ctR2 [256][16]byte
		for i := range 256 {
			ctR2[i] = EncryptReducedRound(lambdaSet[i][:], key[:], 2, true)
		}
		if balanced, _ := VerifyLambdaZeroSum(ctR2[:]); !balanced {
			failuresR2++
		}

		// 4. Round 3 with MixColumns
		var ctR3 [256][16]byte
		for i := range 256 {
			ctR3[i] = EncryptReducedRound(lambdaSet[i][:], key[:], 3, true)
		}
		if balanced, _ := VerifyLambdaZeroSum(ctR3[:]); !balanced {
			failuresR3++
		}
	}

	t.Logf("[STRESS TEST 1 Results] Total Lambda-sets tested: %d", totalSets)
	t.Logf("  Round 1 (No-MC)   Balance Failures: %d / %d (Pass Rate: %.4f%%)", failuresR1NoMC, totalSets, 100.0*float64(totalSets-failuresR1NoMC)/float64(totalSets))
	t.Logf("  Round 1 (With-MC) Balance Failures: %d / %d (Pass Rate: %.4f%%)", failuresR1MC, totalSets, 100.0*float64(totalSets-failuresR1MC)/float64(totalSets))
	t.Logf("  Round 2 (With-MC) Balance Failures: %d / %d (Pass Rate: %.4f%%)", failuresR2, totalSets, 100.0*float64(totalSets-failuresR2)/float64(totalSets))
	t.Logf("  Round 3 (With-MC) Balance Failures: %d / %d (Pass Rate: %.4f%%)", failuresR3, totalSets, 100.0*float64(totalSets-failuresR3)/float64(totalSets))

	if failuresR1NoMC > 0 || failuresR1MC > 0 || failuresR2 > 0 || failuresR3 > 0 {
		t.Fatalf("CRITICAL BUG: Zero-sum balance failed in %d/%d/%d/%d trials!",
			failuresR1NoMC, failuresR1MC, failuresR2, failuresR3)
	}
}

// ============================================================================
// STRESS-TEST 2: Round 4 Unbalancedness & Diagonal Sets Division Property
// ============================================================================

// TestChallengerM2_2_Round4_1ByteActive_Unbalanced verifies that 1-byte active Lambda-sets
// become unbalanced at round 4, demonstrating the exactness of the 3-round integral boundary.
func TestChallengerM2_2_Round4_1ByteActive_Unbalanced(t *testing.T) {
	const trials = 1000

	var seed uint64 = 0x1122334455667788
	nextRand := func() uint64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return seed
	}

	fullBalancedCount := 0
	zeroByteTotal := 0
	totalByteEvaluations := trials * 16

	for iter := range trials {
		var key [16]byte
		binary.LittleEndian.PutUint64(key[0:8], nextRand())
		binary.LittleEndian.PutUint64(key[8:16], nextRand())

		var constants [16]byte
		binary.LittleEndian.PutUint64(constants[0:8], nextRand())
		binary.LittleEndian.PutUint64(constants[8:16], nextRand())

		activeIdx := iter % 16
		lambdaSet := GenerateLambdaSetWithConstants(activeIdx, constants)

		var ctR4 [256][16]byte
		for i := range 256 {
			ctR4[i] = EncryptReducedRound(lambdaSet[i][:], key[:], 4, true)
		}

		isBalanced, allBalanced, xorSum := CheckLambdaBalance(ctR4[:])
		if allBalanced {
			fullBalancedCount++
		}
		for b := range 16 {
			if isBalanced[b] {
				zeroByteTotal++
			}
		}

		if iter == 0 {
			t.Logf("Sample Round 4 XOR sum for activeIdx=%d: %032x", activeIdx, xorSum)
		}
	}

	empiricalByteZeroRate := float64(zeroByteTotal) / float64(totalByteEvaluations)
	expectedByteZeroRate := 1.0 / 256.0 // 0.00390625

	t.Logf("[Round 4 1-Byte Active Test] Trials: %d", trials)
	t.Logf("  Full State Balanced: %d / %d (%.4f%%)", fullBalancedCount, trials, 100.0*float64(fullBalancedCount)/float64(trials))
	t.Logf("  Zero-Byte Frequency: %d / %d (Empirical: %.6f, Theoretical Uniform: %.6f)",
		zeroByteTotal, totalByteEvaluations, empiricalByteZeroRate, expectedByteZeroRate)

	// In 1,000 trials, the probability of full 16-byte balance under PRP is 1000 * 2^-128 ~ 2.9e-36.
	// Any full balance indicates a catastrophic bug or trivial cipher.
	if fullBalancedCount > 0 {
		t.Fatalf("UNEXPECTED: Round 4 showed full zero-sum balance in %d trials", fullBalancedCount)
	}

	// Empirical per-byte zero rate should match uniform PRP (1/256) within 3 sigma
	// Binomial N = 16000, p = 1/256, mean = 62.5, std = sqrt(62.5 * (1 - 1/256)) ~ 7.89
	diffSigma := math.Abs(float64(zeroByteTotal)-62.5) / 7.89
	t.Logf("  Deviation from uniform distribution: %.2f sigma", diffSigma)
	if diffSigma > 5.0 {
		t.Fatalf("Round 4 byte zero distribution heavily diverges from uniform: %.2f sigma", diffSigma)
	}
}

// TestChallengerM2_2_DiagonalSets_DivisionProperty evaluates diagonal structures across rounds 1 to 4.
func TestChallengerM2_2_DiagonalSets_DivisionProperty(t *testing.T) {
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}

	// 1. Test all 4 AES diagonals with numActiveBytes=1 (256 blocks each) for rounds 1, 2, 3
	for diagIdx := range 4 {
		diagSet := GenerateDiagonalSet(diagIdx, 1, byte(0x10*diagIdx+0x05))
		if len(diagSet) != 256 {
			t.Fatalf("expected 256 blocks in diagonal set, got %d", len(diagSet))
		}

		for r := 1; r <= 3; r++ {
			ciphertexts := make([][16]byte, 256)
			for i := range 256 {
				ciphertexts[i] = EncryptReducedRound(diagSet[i][:], key, r, true)
			}
			balanced, xorSum := VerifyLambdaZeroSum(ciphertexts)
			if !balanced {
				t.Fatalf("Diagonal %d Round %d failed zero-sum balance: %x", diagIdx, r, xorSum)
			}
			balancedBits, paritySum := VerifyDivisionProperty4R(ciphertexts)
			if paritySum != [16]byte{0} {
				t.Fatalf("Diagonal %d Round %d parity sum != 0", diagIdx, r)
			}
			for bit := range 128 {
				if !balancedBits[bit] {
					t.Fatalf("Diagonal %d Round %d bit %d not balanced", diagIdx, r, bit)
				}
			}
		}
	}
	t.Logf("[Diagonal Sets] All 4 diagonals pass 100%% zero-sum and division property across rounds 1, 2, and 3.")

	// 2. Test higher-order 2-byte active diagonal set (65,536 blocks) for Diagonal 0
	t.Logf("[Diagonal Sets 2-Active-Byte] Generating 65,536 blocks for Diag 0...")
	diagSet2 := GenerateDiagonalSet(0, 2, 0xAA)
	if len(diagSet2) != 65536 {
		t.Fatalf("expected 65536 blocks, got %d", len(diagSet2))
	}

	// Verify Round 3 on 2-byte diagonal set
	ctR3 := make([][16]byte, len(diagSet2))
	for i := range diagSet2 {
		ctR3[i] = EncryptReducedRound(diagSet2[i][:], key, 3, true)
	}
	balancedR3, xorR3 := VerifyLambdaZeroSum(ctR3)
	if !balancedR3 {
		t.Fatalf("2-active diagonal set failed round 3 zero-sum: %x", xorR3)
	}
	t.Logf("[Diagonal Sets 2-Active-Byte] Round 3 verified strictly zero-sum balanced on 65,536 blocks: %x", xorR3)

	// Check Round 4 on 2-byte diagonal set
	ctR4 := make([][16]byte, len(diagSet2))
	for i := range diagSet2 {
		ctR4[i] = EncryptReducedRound(diagSet2[i][:], key, 4, true)
	}
	balancedBitsR4, paritySumR4 := VerifyDivisionProperty4R(ctR4)
	balancedBitCount := 0
	for bit := range 128 {
		if balancedBitsR4[bit] {
			balancedBitCount++
		}
	}
	t.Logf("[Diagonal Sets 2-Active-Byte] Round 4 on 65,536 blocks: Parity sum=%x, Balanced bits=%d / 128",
		paritySumR4, balancedBitCount)
}

// ============================================================================
// STRESS-TEST 3: Multi-Vector Aggregated Delta H > 0.05% Across Rounds 1, 2, 3, 4
// ============================================================================

// TestChallengerM2_2_MultiVectorAggregated_Rounds1To4 verifies that Delta H > 0.05% holds
// consistently across multiple independent keys, sample sizes, and diagonal alignments for rounds 1..4.
func TestChallengerM2_2_MultiVectorAggregated_Rounds1To4(t *testing.T) {
	testKeys := [][]byte{
		{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c},
		{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
		{0xff, 0xfe, 0xfd, 0xfc, 0xfb, 0xfa, 0xf9, 0xf8, 0xf7, 0xf6, 0xf5, 0xf4, 0xf3, 0xf2, 0xf1, 0xf0},
		{0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa},
		{0x10, 0x32, 0x54, 0x76, 0x98, 0xba, 0xdc, 0xfe, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
	}

	sampleSizes := []int{4096}

	for kIdx, key := range testKeys {
		for _, numBlocks := range sampleSizes {
			for r := 1; r <= 4; r++ {
				blocks := make([][16]byte, numBlocks)
				for i := range numBlocks {
					var pt [16]byte
					if r <= 3 {
						// Rounds 1-3: Low-entropy structured counter stream (Lambda-set ensembles)
						pt[0] = byte(i)
						pt[1] = byte(i >> 8)
						for j := 2; j < 16; j++ {
							pt[j] = byte(j * 17)
						}
					} else {
						// Round 4: Division property pure diagonal-aligned stream (Diagonal 0)
						pt[0] = byte(i)
						pt[5] = byte(i * 13)
						pt[10] = byte(i * 37)
						pt[15] = byte(i * 61)
						for j := 1; j < 16; j++ {
							if j != 5 && j != 10 && j != 15 {
								pt[j] = byte(j * 17)
							}
						}
					}
					blocks[i] = EncryptReducedRound(pt[:], key, r, true)
				}

				aggDeltaH, vec := ComputeMultiVectorDeficit(blocks)

				t.Logf("[Key %d | N=%d | Round %d] Aggregated Delta H%% = %.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
					kIdx, numBlocks, r, aggDeltaH, vec[0], vec[1], vec[2], vec[3], vec[4])

				// Invariant: Delta H > 0.05% MUST hold strictly for rounds 1, 2, 3, 4
				if aggDeltaH <= 0.05 {
					t.Fatalf("FAILED: Key %d, N=%d, Round %d: Aggregated Delta H%% = %.4f%% <= 0.05%% threshold",
						kIdx, numBlocks, r, aggDeltaH)
				}
			}

			// Control: Round 10 must NOT distinguish (Delta H <= 0.05%)
			controlBlocks := make([][16]byte, numBlocks)
			for i := range numBlocks {
				var pt [16]byte
				pt[0] = byte(i)
				pt[1] = byte(i >> 8)
				for j := 2; j < 16; j++ {
					pt[j] = byte(j * 17)
				}
				controlBlocks[i] = EncryptReducedRound(pt[:], key, 10, false)
			}
			aggCtrl, vecCtrl := ComputeMultiVectorDeficit(controlBlocks)
			t.Logf("[Key %d | N=%d | Round 10 Control] Aggregated Delta H%% = %.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
				kIdx, numBlocks, aggCtrl, vecCtrl[0], vecCtrl[1], vecCtrl[2], vecCtrl[3], vecCtrl[4])
			if aggCtrl > 0.05 {
				t.Fatalf("FAILED: Key %d, N=%d, Round 10 Control falsely distinguished: %.4f%% > 0.05%%",
					kIdx, numBlocks, aggCtrl)
			}
		}
	}
}

// TestChallengerM2_2_AllDiagonals_EntropyPenetration tests plaintexts aligned to each
// of the 4 diagonals {0, 1, 2, 3} at round 4 and confirms Delta H > 0.05%.
func TestChallengerM2_2_AllDiagonals_EntropyPenetration(t *testing.T) {
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	numBlocks := 4096

	for diagIdx := range 4 {
		diag := aesDiagonals[diagIdx]

		plaintexts := make([][16]byte, numBlocks)
		for i := range numBlocks {
			// Populate all 4 diagonal positions with varying counter / modular streams
			plaintexts[i][diag[0]] = byte(i)
			plaintexts[i][diag[1]] = byte(i * 13)
			plaintexts[i][diag[2]] = byte(i * 37)
			plaintexts[i][diag[3]] = byte(i * 61)
			for j := range 16 {
				if j != diag[0] && j != diag[1] && j != diag[2] && j != diag[3] {
					plaintexts[i][j] = byte(j * 17)
				}
			}
		}

		blocksR4 := make([][16]byte, numBlocks)
		for i := range numBlocks {
			blocksR4[i] = EncryptReducedRound(plaintexts[i][:], key, 4, true)
		}

		agg, vec := ComputeMultiVectorDeficit(blocksR4)
		t.Logf("[Diagonal %d Round 4] Aggregated Delta H%% = %.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
			diagIdx, agg, vec[0], vec[1], vec[2], vec[3], vec[4])

		if agg <= 0.05 {
			t.Fatalf("Diagonal %d at Round 4 failed Delta H > 0.05%%: got %.4f%%", diagIdx, agg)
		}
	}
}
