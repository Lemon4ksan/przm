// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"crypto/rand"
	"encoding/binary"
	"math"
	"testing"
)

// ============================================================================
// CHALLENGE 1: Shannon Entropy & Chi-Squared Estimators Against Edge Distributions
// ============================================================================

// TestChallenger_ShannonEntropy_AllDiracDistributions verifies all 256 Dirac distributions
// (single non-zero probability mass, all bytes == c) across multiple lengths.
func TestChallenger_ShannonEntropy_AllDiracDistributions(t *testing.T) {
	testLengths := []int{1, 2, 3, 7, 8, 15, 16, 64, 255, 256, 1024, 65536}

	for _, c := range []byte{0x00, 0x01, 0x55, 0x7F, 0x80, 0xAA, 0xFE, 0xFF} {
		for _, n := range testLengths {
			buf := make([]byte, n)
			for i := range buf {
				buf[i] = c
			}

			h, deltaH, deltaHPct := ComputeShannonEntropy(buf)
			if h != 0.0 {
				t.Fatalf("Dirac c=0x%02X n=%d: expected H=0.0, got %f", c, n, h)
			}
			if deltaH != 8.0 {
				t.Fatalf("Dirac c=0x%02X n=%d: expected deltaH=8.0, got %f", c, n, h)
			}
			if deltaHPct != 100.0 {
				t.Fatalf("Dirac c=0x%02X n=%d: expected deltaHPct=100.0%%, got %f%%", c, n, deltaHPct)
			}

			// Chi-squared test for Dirac distribution:
			// chi2 = (256/N)*N^2 - N = 255 * N
			chi2, _, zScore := ComputeChiSquared(buf)
			expectedChi2 := 255.0 * float64(n)
			if math.Abs(chi2-expectedChi2) > 1e-6 {
				t.Fatalf("Dirac c=0x%02X n=%d: expected chi2=%f, got %f", c, n, expectedChi2, chi2)
			}

			if n >= 256 && zScore < 100.0 {
				t.Fatalf("Dirac c=0x%02X n=%d: expected huge zScore, got %f", c, n, zScore)
			}
		}
	}
}

// TestChallenger_ShannonEntropy_UniformDistributions verifies exact theoretical uniform
// distributions of varying multiples of 256 bins.
func TestChallenger_ShannonEntropy_UniformDistributions(t *testing.T) {
	multiples := []int{1, 2, 4, 8, 16, 64, 256}

	for _, k := range multiples {
		n := k * 256
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = byte(i % 256)
		}

		h, deltaH, deltaHPct := ComputeShannonEntropy(buf)
		if math.Abs(h-8.0) > 1e-12 {
			t.Fatalf("Uniform k=%d: expected H=8.0, got %f (diff: %e)", k, h, h-8.0)
		}
		if math.Abs(deltaH) > 1e-12 {
			t.Fatalf("Uniform k=%d: expected deltaH=0.0, got %f", k, deltaH)
		}
		if math.Abs(deltaHPct) > 1e-12 {
			t.Fatalf("Uniform k=%d: expected deltaHPct=0.0%%, got %f%%", k, deltaHPct)
		}

		chi2, _, zScore := ComputeChiSquared(buf)
		if math.Abs(chi2) > 1e-12 {
			t.Fatalf("Uniform k=%d: expected chi2=0.0, got %f", k, chi2)
		}
		expectedZ := -255.0 / math.Sqrt(510.0) // ~ -11.2917
		if math.Abs(zScore-expectedZ) > 1e-5 {
			t.Fatalf("Uniform k=%d: expected zScore=%f, got %f", k, expectedZ, zScore)
		}

		pVal := ComputeChiSquaredPValue(chi2)
		if pVal != 1.0 {
			t.Fatalf("Uniform k=%d: expected pVal=1.0, got %f", k, pVal)
		}
	}
}

// TestChallenger_ShannonEntropy_SingleBitBias tests single-bit bias epsilon \in [0.02, 0.30]
// and verifies the Taylor expansion bridge + Miller-Madow sample bias:
// Delta H \approx (2.88539 * eps^2) + (255 / (2 * N * ln 2)).
func TestChallenger_ShannonEntropy_SingleBitBias(t *testing.T) {
	n := 500000 // 500k bytes for high statistical precision

	biases := []float64{0.02, 0.05, 0.10, 0.20, 0.30}

	for _, eps := range biases {
		p1 := 0.5 + eps
		threshold := uint32(p1 * float64(math.MaxUint32))

		buf := make([]byte, n)
		randomWords := make([]uint32, n)
		for i := range randomWords {
			var b [4]byte
			rand.Read(b[:])
			randomWords[i] = binary.LittleEndian.Uint32(b[:])
		}

		for i := range n {
			val := byte(randomWords[i]>>8) & 0xFE
			if randomWords[i] < threshold {
				val |= 1
			}
			buf[i] = val
		}

		_, deltaH, deltaHPct := ComputeShannonEntropy(buf)
		chi2, _, zScore := ComputeChiSquared(buf)

		// Theoretical entropy deficit for 1 biased bit out of 8 bits:
		// Delta H = 1 - h_2(p1) = 1 - [ -p1*log2(p1) - (1-p1)*log2(1-p1) ]
		theoryDeficit := 1.0 - (-p1*math.Log2(p1) - (1.0-p1)*math.Log2(1.0-p1))
		// Miller-Madow finite sample bias for N bytes over K=256 bins
		millerMadow := 255.0 / (2.0 * float64(n) * math.Ln2)
		expectedTotalDeltaH := theoryDeficit + millerMadow
		expectedTotalDeltaHPct := (expectedTotalDeltaH / 8.0) * 100.0

		relDiff := math.Abs(deltaH-expectedTotalDeltaH) / expectedTotalDeltaH
		t.Logf("[Single-Bit Bias eps=%.2f] Empirical Delta H = %.6f (%.4f%%), Expected = %.6f (%.4f%%), RelDiff = %.2f%%, Chi2 = %.1f, Z = %.1f",
			eps, deltaH, deltaHPct, expectedTotalDeltaH, expectedTotalDeltaHPct, relDiff*100.0, chi2, zScore)

		if relDiff > 0.15 {
			t.Fatalf("eps=%.2f: empirical deltaH (%f) diverges from expected (%f) by > 15%%", eps, deltaH, expectedTotalDeltaH)
		}

		// For eps >= 0.05, Z-score must decisively detect non-uniformity (Z > 3.29)
		if eps >= 0.05 && zScore <= 3.29 {
			t.Fatalf("eps=%.2f: Chi2 Z-score failed to distinguish bias: Z=%f", eps, zScore)
		}
	}
}

// TestChallenger_Statistical_BoundaryCases tests empty buffers, 1-byte, 2-byte buffers
// and validates no panics, NaNs, or infinite values.
func TestChallenger_Statistical_BoundaryCases(t *testing.T) {
	// Empty slice
	h, dh, dhPct := ComputeShannonEntropy([]byte{})
	if h != 0.0 || dh != 8.0 || dhPct != 100.0 {
		t.Fatalf("empty slice failed: (%f, %f, %f)", h, dh, dhPct)
	}

	c2, _, z := ComputeChiSquared([]byte{})
	if c2 != 0.0 || z != 0.0 {
		t.Fatalf("empty slice chi2 failed: (%f, %f)", c2, z)
	}

	h2, dh2, _ := ComputeBigramEntropy([]byte{0x42})
	if h2 != 0.0 || dh2 != 16.0 {
		t.Fatalf("single byte bigram failed: (%f, %f)", h2, dh2)
	}

	hs4, dhs4, _ := ComputeStride4Entropy([]byte{0x01, 0x02, 0x03, 0x04})
	if hs4 != 0.0 || dhs4 != 16.0 {
		t.Fatalf("4-byte stride4 failed: (%f, %f)", hs4, dhs4)
	}

	cond := ComputeConditionalEntropy([]byte{0x42})
	if cond != 0.0 {
		t.Fatalf("single byte conditional entropy failed: %f", cond)
	}

	// Check P-value extremes
	if ComputeChiSquaredPValue(-1000.0) != 1.0 {
		t.Fatalf("negative extreme p-value should clamp to 1.0")
	}
	if ComputeChiSquaredPValue(100000.0) != 0.0 {
		t.Fatalf("positive extreme p-value should clamp to 0.0")
	}
}

// ============================================================================
// CHALLENGE 2: 128x128 Bit-Slice Spatial Correlation Matrix Stress-Testing
// ============================================================================

// TestChallenger_BitSlice_ExactCoordinateParity exhaustively tests every bit coordinate
// k in [0, 127] to verify that SWAR Transpose8x8 maps byte (k/8) and bit (k%8) to globalBit k.
func TestChallenger_BitSlice_ExactCoordinateParity(t *testing.T) {
	numBlocks := 128
	for targetBit := range 128 {
		targetByte := targetBit / 8
		targetBitInByte := targetBit % 8

		blocks := make([][16]byte, numBlocks)
		for i := range numBlocks {
			if i%2 == 0 {
				blocks[i][targetByte] = 1 << targetBitInByte
			}
		}

		corr := ComputeBitSliceCorrelations(blocks)

		// 1. Diagonal must be strictly 1.0 for all bits
		for b := range 128 {
			if math.Abs(corr[b][b]-1.0) > 1e-9 {
				t.Fatalf("targetBit=%d: corr[%d][%d] expected 1.0, got %f", targetBit, b, b, corr[b][b])
			}
		}

		// 2. Off-diagonal for targetBit against all other bits must be 0.0
		for b := range 128 {
			if b != targetBit {
				if math.Abs(corr[targetBit][b]) > 1e-9 {
					t.Fatalf("bit coordinate mismatch! targetBit=%d (byte %d, bit %d) correlated with bit %d: corr=%f",
						targetBit, targetByte, targetBitInByte, b, corr[targetBit][b])
				}
				if math.Abs(corr[b][targetBit]) > 1e-9 {
					t.Fatalf("asymmetry! corr[%d][%d]=%f != corr[%d][%d]=%f",
						b, targetBit, corr[b][targetBit], targetBit, b, corr[targetBit][b])
				}
			}
		}
	}
}

// TestChallenger_BitSlice_StressInvariants tests mathematical invariants across arbitrary
// pseudo-random block ensembles and varying block counts (including non-powers of 2).
func TestChallenger_BitSlice_StressInvariants(t *testing.T) {
	blockCounts := []int{2, 3, 7, 15, 16, 31, 63, 64, 65, 127, 128, 129, 255, 256, 500}

	for _, m := range blockCounts {
		blocks := make([][16]byte, m)
		for i := range m {
			rand.Read(blocks[i][:])
		}

		corr := ComputeBitSliceCorrelations(blocks)

		// Check symmetry, diagonal, and bounds [-1.0, 1.0]
		for i := range 128 {
			if math.Abs(corr[i][i]-1.0) > 1e-9 {
				t.Fatalf("M=%d: diagonal corr[%d][%d] != 1.0: %f", m, i, i, corr[i][i])
			}

			for j := range 128 {
				// Symmetry
				if math.Abs(corr[i][j]-corr[j][i]) > 1e-9 {
					t.Fatalf("M=%d: asymmetry corr[%d][%d]=%f != corr[%d][%d]=%f",
						m, i, j, corr[i][j], j, i, corr[j][i])
				}

				// Range check
				if corr[i][j] < -1.0-1e-9 || corr[i][j] > 1.0+1e-9 {
					t.Fatalf("M=%d: out of bounds corr[%d][%d] = %f", m, i, j, corr[i][j])
				}

				// NaN check
				if math.IsNaN(corr[i][j]) || math.IsInf(corr[i][j], 0) {
					t.Fatalf("M=%d: NaN/Inf detected at corr[%d][%d]", m, i, j)
				}
			}
		}

		totalCorr := ComputeTotalBitCorrelation(&corr)
		if totalCorr < 0.0 || math.IsNaN(totalCorr) {
			t.Fatalf("M=%d: invalid total bit correlation: %f", m, totalCorr)
		}
	}
}

// TestChallenger_BitSlice_AdversarialAntiCorrelation tests pairs of perfectly anti-correlated bits
// across state words and confirms corr[i][j] == -1.0.
func TestChallenger_BitSlice_AdversarialAntiCorrelation(t *testing.T) {
	numBlocks := 512
	blocks := make([][16]byte, numBlocks)

	// Bit 15 (byte 1, bit 7) and Bit 92 (byte 11, bit 4) are strictly complementary
	for i := range numBlocks {
		rand.Read(blocks[i][:])
		bitVal := byte(i % 2)
		if bitVal == 1 {
			blocks[i][1] |= (1 << 7)
		} else {
			blocks[i][1] &= ^byte(1 << 7)
		}
		if (1 - bitVal) == 1 {
			blocks[i][11] |= (1 << 4)
		} else {
			blocks[i][11] &= ^byte(1 << 4)
		}
	}

	corr := ComputeBitSliceCorrelations(blocks)
	if math.Abs(corr[15][92]-(-1.0)) > 1e-5 {
		t.Fatalf("expected anti-correlation corr[15][92] == -1.0, got %f", corr[15][92])
	}
	if math.Abs(corr[92][15]-(-1.0)) > 1e-5 {
		t.Fatalf("expected anti-correlation corr[92][15] == -1.0, got %f", corr[92][15])
	}
}

// ============================================================================
// CHALLENGE 3: 10-Round AES Pseudo-Random Permutation Verification (Delta H <= 0.05%)
// ============================================================================

// TestChallenger_AES10_PRP_StructuredPlaintexts verifies that 10-round AES behaves as an ideal
// pseudo-random permutation with Delta H <= 0.05% across distinct structured plaintexts.
func TestChallenger_AES10_PRP_StructuredPlaintexts(t *testing.T) {
	numBlocks := 8192 // 131,072 bytes (0.125 MiB)
	keys := [][]byte{
		{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}, // Standard NIST key
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // All-zero key
		{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}, // Linear key
	}

	scenarios := []struct {
		name string
		gen  func(i int) [16]byte
	}{
		{
			name: "64-bit Sequential Counter (0, 1, 2, ...)",
			gen: func(i int) [16]byte {
				var pt [16]byte
				binary.LittleEndian.PutUint64(pt[0:8], uint64(i))
				return pt
			},
		},
		{
			name: "16-bit Counter with 14 Constant Bytes (Bytes 2..15 = 0x5A)",
			gen: func(i int) [16]byte {
				var pt [16]byte
				binary.LittleEndian.PutUint16(pt[0:2], uint16(i))
				for j := 2; j < 16; j++ {
					pt[j] = 0x5A
				}
				return pt
			},
		},
		{
			name: "Counter + Sparse Invariant Flip",
			gen: func(i int) [16]byte {
				var pt [16]byte
				binary.LittleEndian.PutUint16(pt[0:2], uint16(i))
				bit := (i >> 8) % 112
				pt[2+bit/8] ^= (1 << (bit % 8))
				return pt
			},
		},
		{
			name: "Counter + Alternating Pattern",
			gen: func(i int) [16]byte {
				var pt [16]byte
				binary.LittleEndian.PutUint16(pt[0:2], uint16(i))
				for j := 2; j < 16; j++ {
					if j%2 == 0 {
						pt[j] = 0xAA
					} else {
						pt[j] = 0x55
					}
				}
				return pt
			},
		},
	}

	for _, key := range keys {
		for _, sc := range scenarios {
			ciphertexts := make([]byte, numBlocks*16)
			blocks := make([][16]byte, numBlocks)

			for i := range numBlocks {
				pt := sc.gen(i)
				ct := EncryptReducedRound(pt[:], key, 10, false)
				blocks[i] = ct
				copy(ciphertexts[i*16:(i+1)*16], ct[:])
			}

			shannonH, deltaH, deltaHPct := ComputeShannonEntropy(ciphertexts)
			chi2, _, zScore := ComputeChiSquared(ciphertexts)
			report := AnalyzeStreamEntropy(ciphertexts)

			t.Logf("[AES-10 PRP Test | Key=%x... | %s]\n"+
				"    Shannon H: %.6f, Delta H: %.6f, Delta H%%: %.4f%%\n"+
				"    Chi-Squared: %.2f (df=255), Z-Score: %.2f\n"+
				"    Bigram Delta H%%: %.4f%%, Stride-4 Delta H%%: %.4f%%\n"+
				"    Total Bit Correlation: %.2f, Distinguisher Fired: %v",
				key[:4], sc.name, shannonH, deltaH, deltaHPct, chi2, zScore,
				report.BigramDeltaHPct, report.Stride4DeltaHPct, report.TotalBitCorrelation, report.DistinguisherFired)

			// Strict PRP Acceptance Criteria:
			// Delta H% must strictly be <= 0.05%
			if deltaHPct > 0.05 {
				t.Fatalf("FAIL: 10-round AES leaked Delta H%% = %.4f%% > 0.05%% under %s (Key=%x...)",
					deltaHPct, sc.name, key[:4])
			}

			// Z-score must be consistent with uniform random noise (|Z| <= 4.0, corresponding to p > 6e-5)
			if math.Abs(zScore) > 4.0 {
				t.Fatalf("FAIL: 10-round AES Chi2 Z-score |Z| = %.2f > 4.0 under %s", zScore, sc.name)
			}

			// Multi-dimensional distinguisher must NOT fire
			if report.DistinguisherFired {
				t.Fatalf("FAIL: Distinguisher falsely fired on 10-round AES under %s", sc.name)
			}
		}
	}
}

// TestChallenger_Distinguisher_SharpCutoff verifies that the distinguishing threshold Delta H > 0.05%
// correctly separates reduced rounds (1..3 with Lambda-sets and Round 4 with diagonal sets)
// from the indistinguishable full AES (Round 10).
func TestChallenger_Distinguisher_SharpCutoff(t *testing.T) {
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	numBlocks := 4096

	// 1. Structure for Rounds 1, 2, 3: Lambda-set counter sequence
	plaintextsR1to3 := make([][16]byte, numBlocks)
	for i := range numBlocks {
		plaintextsR1to3[i][0] = byte(i)
		plaintextsR1to3[i][1] = byte(i >> 8)
		for j := 2; j < 16; j++ {
			plaintextsR1to3[i][j] = byte(j * 17)
		}
	}

	results := make(map[int]float64)
	for _, r := range []int{1, 2, 3} {
		blocks := make([][16]byte, numBlocks)
		for i := range numBlocks {
			blocks[i] = EncryptReducedRound(plaintextsR1to3[i][:], key, r, true)
		}
		agg, _ := ComputeMultiVectorDeficit(blocks)
		results[r] = agg
		t.Logf("Round %2d: Aggregated Deficit = %.4f%%", r, agg)
	}

	// 2. Structure for Round 4: Diagonal multi-stream sequence
	plaintextsR4 := make([][16]byte, numBlocks)
	for i := range numBlocks {
		plaintextsR4[i][0] = byte(i)
		plaintextsR4[i][5] = byte(i * 13)
		plaintextsR4[i][10] = byte(i * 37)
		plaintextsR4[i][15] = byte(i * 61)
		for j := 1; j < 16; j++ {
			if j != 5 && j != 10 && j != 15 {
				plaintextsR4[i][j] = byte(j * 17)
			}
		}
	}
	blocksR4 := make([][16]byte, numBlocks)
	for i := range numBlocks {
		blocksR4[i] = EncryptReducedRound(plaintextsR4[i][:], key, 4, true)
	}
	aggR4, _ := ComputeMultiVectorDeficit(blocksR4)
	results[4] = aggR4
	t.Logf("Round  4: Aggregated Deficit = %.4f%%", aggR4)

	// 3. Full AES: Round 10 Control
	blocksR10 := make([][16]byte, numBlocks)
	for i := range numBlocks {
		blocksR10[i] = EncryptReducedRound(plaintextsR1to3[i][:], key, 10, false)
	}
	aggR10, _ := ComputeMultiVectorDeficit(blocksR10)
	results[10] = aggR10
	t.Logf("Round 10: Aggregated Deficit = %.4f%%", aggR10)

	// Verify monotonic decreasing trend on rounds 1..3
	if results[1] <= results[2] || results[2] <= results[3] {
		t.Fatalf("Distinguisher ladder is not monotonically decreasing: R1=%f, R2=%f, R3=%f",
			results[1], results[2], results[3])
	}

	// Rounds 1 to 4 must strictly exceed 0.05%
	for _, r := range []int{1, 2, 3, 4} {
		if results[r] <= 0.05 {
			t.Fatalf("Round %d deficit (%f%%) failed to exceed distinguishing threshold 0.05%%", r, results[r])
		}
	}

	// Round 10 must strictly fall below 0.05%
	if results[10] > 0.05 {
		t.Fatalf("Round 10 deficit (%f%%) exceeds indistinguishability bound 0.05%%", results[10])
	}
}
