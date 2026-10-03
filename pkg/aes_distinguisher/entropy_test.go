// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"crypto/rand"
	"math"
	"testing"
)

func TestComputeShannonEntropy_TheoreticalExtremes(t *testing.T) {
	// 1. Empty data
	h, deltaH, deltaHPct := ComputeShannonEntropy(nil)
	if h != 0.0 || deltaH != 8.0 || deltaHPct != 100.0 {
		t.Fatalf("empty slice: expected (0.0, 8.0, 100.0), got (%f, %f, %f)", h, deltaH, deltaHPct)
	}

	// 2. Monovalue data (all zeros)
	allZeros := make([]byte, 1024)
	h, deltaH, deltaHPct = ComputeShannonEntropy(allZeros)
	if h != 0.0 || deltaH != 8.0 || deltaHPct != 100.0 {
		t.Fatalf("all zeros: expected (0.0, 8.0, 100.0), got (%f, %f, %f)", h, deltaH, deltaHPct)
	}

	// 3. Exact uniform distribution (multiples of 256)
	uniform := make([]byte, 256*16)
	for i := range uniform {
		uniform[i] = byte(i % 256)
	}
	h, deltaH, deltaHPct = ComputeShannonEntropy(uniform)
	if math.Abs(h-8.0) > 1e-9 || math.Abs(deltaH) > 1e-9 || math.Abs(deltaHPct) > 1e-9 {
		t.Fatalf("exact uniform: expected (8.0, 0.0, 0.0), got (%f, %f, %f)", h, deltaH, deltaHPct)
	}

	// 4. Two-symbol balanced alphabet: H = 1.0 bit
	twoSymbols := make([]byte, 1000)
	for i := range twoSymbols {
		twoSymbols[i] = byte(i % 2)
	}
	h, deltaH, deltaHPct = ComputeShannonEntropy(twoSymbols)
	if math.Abs(h-1.0) > 1e-9 {
		t.Fatalf("two symbols: expected H=1.0, got %f", h)
	}
	if math.Abs(deltaH-7.0) > 1e-9 {
		t.Fatalf("two symbols: expected deltaH=7.0, got %f", deltaH)
	}
	if math.Abs(deltaHPct-(7.0/8.0)*100.0) > 1e-9 {
		t.Fatalf("two symbols: expected deltaHPct=87.5%%, got %f", deltaHPct)
	}

	// 5. Four-symbol balanced alphabet: H = 2.0 bits
	fourSymbols := make([]byte, 1000)
	for i := range fourSymbols {
		fourSymbols[i] = byte(i % 4)
	}
	h, _, _ = ComputeShannonEntropy(fourSymbols)
	if math.Abs(h-2.0) > 1e-9 {
		t.Fatalf("four symbols: expected H=2.0, got %f", h)
	}

	// 6. Sixteen-symbol balanced alphabet: H = 4.0 bits
	sixteenSymbols := make([]byte, 1600)
	for i := range sixteenSymbols {
		sixteenSymbols[i] = byte(i % 16)
	}
	h, _, _ = ComputeShannonEntropy(sixteenSymbols)
	if math.Abs(h-4.0) > 1e-9 {
		t.Fatalf("sixteen symbols: expected H=4.0, got %f", h)
	}
}

func TestComputeChiSquared_Properties(t *testing.T) {
	// 1. Exact uniform distribution
	uniform := make([]byte, 256*32)
	for i := range uniform {
		uniform[i] = byte(i % 256)
	}
	chi2, pVal, zScore := ComputeChiSquared(uniform)
	if math.Abs(chi2) > 1e-9 {
		t.Fatalf("exact uniform: expected chi2=0.0, got %f", chi2)
	}
	if math.Abs(pVal-1.0) > 1e-6 {
		t.Fatalf("exact uniform: expected pVal=1.0, got %f", pVal)
	}
	// Z = (0 - 255) / sqrt(510) ~ -11.29
	expectedZ := (0.0 - 255.0) / math.Sqrt(510.0)
	if math.Abs(zScore-expectedZ) > 1e-5 {
		t.Fatalf("exact uniform: expected zScore=%f, got %f", expectedZ, zScore)
	}

	// 2. Monovalue data: Chi2 = N * (256 - 1)
	allZeros := make([]byte, 1024)
	chi2Zero, pValZero, zScoreZero := ComputeChiSquared(allZeros)
	expectedChi2Zero := 1024.0 * 255.0
	if math.Abs(chi2Zero-expectedChi2Zero) > 1e-5 {
		t.Fatalf("monovalue: expected chi2=%f, got %f", expectedChi2Zero, chi2Zero)
	}
	if pValZero > 1e-6 {
		t.Fatalf("monovalue: expected pValZero < 1e-6, got %e", pValZero)
	}
	if zScoreZero < 1000.0 {
		t.Fatalf("monovalue: expected huge positive zScore, got %f", zScoreZero)
	}

	// 3. Check p-value helper directly
	pValUniform := ComputeChiSquaredPValue(0.0)
	if pValUniform != 1.0 {
		t.Fatalf("uniform p-value should be 1.0, got %f", pValUniform)
	}
	pValBiased := ComputeChiSquaredPValue(1000.0)
	if pValBiased > 1e-6 {
		t.Fatalf("biased p-value should be < 1e-6, got %e", pValBiased)
	}
}

func TestMillerMadowCorrection_Debiasing(t *testing.T) {
	// Test on N = 16384 bytes (1024 blocks), uniform random simulation
	// Uncorrected naive entropy has Miller-Madow bias: ~0.1403%
	// Corrected entropy must restore H close to 8.0 and Delta H% <= 0.05%
	prngData := make([]byte, 16384)
	rand.Read(prngData)

	hNaive, _, deltaHPctNaive := ComputeShannonEntropy(prngData)
	hCorr, _, deltaHPctCorr := ComputeShannonEntropyCorrected(prngData)
	bias := ComputeMillerMadowCorrection(prngData)

	t.Logf("[Miller-Madow Test] Naive H=%.6f (Delta H%%=%.4f%%), Bias=%.6f bits, Corrected H=%.6f (Delta H%%=%.4f%%)",
		hNaive, deltaHPctNaive, bias, hCorr, deltaHPctCorr)

	// Evaluate 1024 blocks of Round 10 AES
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	blocks1024 := make([][16]byte, 1024)
	for i := range 1024 {
		var pt [16]byte
		pt[0] = byte(i)
		pt[1] = byte(i >> 8)
		for j := 2; j < 16; j++ {
			pt[j] = byte(j * 17)
		}
		blocks1024[i] = EncryptReducedRound(pt[:], key, 10, false)
	}
	agg1024, vec1024 := ComputeMultiVectorDeficit(blocks1024)
	t.Logf("[Round 10 | 1024 blocks] Agg=%.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
		agg1024, vec1024[0], vec1024[1], vec1024[2], vec1024[3], vec1024[4])

	if bias <= 0.008 || bias >= 0.015 {
		t.Fatalf("expected Miller-Madow bias ~0.0112 bits, got %f", bias)
	}
	if deltaHPctCorr > 0.05 {
		t.Fatalf("Miller-Madow corrected Delta H%% (%.4f%%) exceeded 0.05%% threshold", deltaHPctCorr)
	}
	if math.Abs(hCorr-8.0) > 0.005 {
		t.Fatalf("corrected H (%f) diverges from 8.0 by > 0.005 bits", hCorr)
	}
}

func TestComputeBitSliceCorrelations_SymmetryAndRange(t *testing.T) {
	numBlocks := 256
	blocks := make([][16]byte, numBlocks)

	// Create blocks where bit 0 and bit 1 are perfectly correlated,
	// and bit 2 is the exact complement of bit 0
	for i := range numBlocks {
		b := byte(i % 2)
		blocks[i][0] = b | (b << 1) | ((1 - b) << 2)
	}

	corr := ComputeBitSliceCorrelations(blocks)

	// 1. Diagonal must be 1.0
	for i := range 128 {
		if math.Abs(corr[i][i]-1.0) > 1e-9 {
			t.Fatalf("diagonal corr[%d][%d] expected 1.0, got %f", i, i, corr[i][i])
		}
	}

	// 2. Symmetry: corr[i][j] == corr[j][i]
	for i := range 128 {
		for j := range 128 {
			if math.Abs(corr[i][j]-corr[j][i]) > 1e-9 {
				t.Fatalf("asymmetric correlation at (%d, %d): %f vs %f", i, j, corr[i][j], corr[j][i])
			}
			if corr[i][j] < -1.0-1e-9 || corr[i][j] > 1.0+1e-9 {
				t.Fatalf("correlation out of bounds at (%d, %d): %f", i, j, corr[i][j])
			}
		}
	}

	// 3. Bit 0 and bit 1 are perfectly correlated: corr[0][1] == 1.0
	if math.Abs(corr[0][1]-1.0) > 1e-6 {
		t.Fatalf("expected perfect correlation between bit 0 and 1, got %f", corr[0][1])
	}

	// 4. Bit 0 and bit 2 are perfectly anti-correlated: corr[0][2] == -1.0
	if math.Abs(corr[0][2]-(-1.0)) > 1e-6 {
		t.Fatalf("expected perfect anti-correlation between bit 0 and 2, got %f", corr[0][2])
	}

	// 5. Total correlation sum
	totalCorr := ComputeTotalBitCorrelation(&corr)
	if totalCorr <= 0.0 {
		t.Fatalf("expected positive total bit correlation, got %f", totalCorr)
	}
}

func TestBigramAndStrideEntropy(t *testing.T) {
	// Alternating sequence 0xAA, 0x55
	n := 1000
	alt := make([]byte, n)
	for i := range alt {
		if i%2 == 0 {
			alt[i] = 0xAA
		} else {
			alt[i] = 0x55
		}
	}

	h2, deltaH2, deltaH2Pct := ComputeBigramEntropy(alt)
	// Only 2 bigrams exist: (0xAA, 0x55) and (0x55, 0xAA), each ~50%
	// So H2 = 1.0 bit
	if math.Abs(h2-1.0) > 0.05 {
		t.Fatalf("alternating bigram entropy expected ~1.0, got %f", h2)
	}
	if deltaH2 < 14.5 {
		t.Fatalf("alternating deltaH2 expected ~15.0, got %f", deltaH2)
	}
	if deltaH2Pct < 90.0 {
		t.Fatalf("alternating deltaH2Pct expected > 90%%, got %f%%", deltaH2Pct)
	}

	// Stride-4 entropy on 4-byte repeating block
	rep4 := make([]byte, 1600)
	pattern := []byte{0x10, 0x20, 0x30, 0x40}
	for i := range rep4 {
		rep4[i] = pattern[i%4]
	}
	hS4, deltaHS4, _ := ComputeStride4Entropy(rep4)
	// Stride-4 pairs are identical: (p, p) for each p in pattern
	// 4 pairs total -> H = 2.0 bits
	if math.Abs(hS4-2.0) > 0.05 {
		t.Fatalf("stride-4 entropy on 4-byte pattern expected ~2.0, got %f", hS4)
	}
	if deltaHS4 < 13.5 {
		t.Fatalf("stride-4 deltaH expected ~14.0, got %f", deltaHS4)
	}

	// Conditional entropy
	cond := ComputeConditionalEntropy(alt)
	// H(X_k | X_{k-1}) = H2 - H0 = 1.0 - 1.0 = 0.0 (strictly deterministic)
	if math.Abs(cond) > 0.05 {
		t.Fatalf("conditional entropy on deterministic alternating stream expected ~0.0, got %f", cond)
	}
}

func TestReducedRoundDistinguisher_EmpiricalProof(t *testing.T) {
	key := []byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	numBlocks := 4096
	plaintexts := make([][16]byte, numBlocks)

	// Fill plaintexts with deterministic structured / low-entropy counter sequences
	for i := range numBlocks {
		plaintexts[i][0] = byte(i)
		plaintexts[i][1] = byte(i >> 8)
		for j := 2; j < 16; j++ {
			plaintexts[i][j] = byte(j * 17)
		}
	}

	// -------------------------------------------------------------
	// 1. ROUND 1 (No MixColumns): Massive structural entropy deficit
	// Requirement: Delta H >> 6.0%
	// -------------------------------------------------------------
	ctR1NoMC := make([]byte, numBlocks*16)
	for i := range numBlocks {
		ct := EncryptReducedRound(plaintexts[i][:], key, 1, false)
		copy(ctR1NoMC[i*16:(i+1)*16], ct[:])
	}
	_, _, deltaHPctR1NoMC := ComputeShannonEntropy(ctR1NoMC)
	chi2R1, _, zR1 := ComputeChiSquared(ctR1NoMC)
	t.Logf("[Round 1 No-MC] Delta H%% = %.4f%%, Chi2 = %.2f, Z = %.2f", deltaHPctR1NoMC, chi2R1, zR1)

	if deltaHPctR1NoMC < 6.0 {
		t.Fatalf("Round 1 No-MC: expected Delta H%% >> 6.0%%, got %.4f%%", deltaHPctR1NoMC)
	}
	if zR1 < 50.0 {
		t.Fatalf("Round 1 No-MC: expected huge Chi2 Z-score, got %.2f", zR1)
	}

	// -------------------------------------------------------------
	// 2. ROUND 1 (With MixColumns): Intra-column diffusion only
	// Requirement: Delta H > 0.05%
	// -------------------------------------------------------------
	ctR1MC := make([]byte, numBlocks*16)
	for i := range numBlocks {
		ct := EncryptReducedRound(plaintexts[i][:], key, 1, true)
		copy(ctR1MC[i*16:(i+1)*16], ct[:])
	}
	_, _, deltaHPctR1MC := ComputeShannonEntropy(ctR1MC)
	t.Logf("[Round 1 With-MC] Delta H%% = %.4f%%", deltaHPctR1MC)
	if deltaHPctR1MC < 0.05 {
		t.Fatalf("Round 1 With-MC: expected Delta H%% > 0.05%%, got %.4f%%", deltaHPctR1MC)
	}

	// -------------------------------------------------------------
	// 3. ROUND 2: 2 full rounds
	// Requirement: Delta H approx 0.46% - 1.85%, Delta H > 0.05%
	// -------------------------------------------------------------
	blocksR2 := make([][16]byte, numBlocks)
	for i := range numBlocks {
		blocksR2[i] = EncryptReducedRound(plaintexts[i][:], key, 2, true)
	}
	aggR2, vecR2 := ComputeMultiVectorDeficit(blocksR2)
	t.Logf("[Round 2] Aggregated Delta H%% = %.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
		aggR2, vecR2[0], vecR2[1], vecR2[2], vecR2[3], vecR2[4])

	if aggR2 < 0.05 {
		t.Fatalf("Round 2: expected Aggregated Delta H%% > 0.05%%, got %.4f%%", aggR2)
	}

	// -------------------------------------------------------------
	// 4. ROUND 3: Integral zero-sum and non-random structure
	// Requirement: Delta H approx 0.065% - 0.18%, Delta H > 0.05%
	// -------------------------------------------------------------
	blocksR3 := make([][16]byte, numBlocks)
	for i := range numBlocks {
		blocksR3[i] = EncryptReducedRound(plaintexts[i][:], key, 3, true)
	}
	aggR3, vecR3 := ComputeMultiVectorDeficit(blocksR3)
	t.Logf("[Round 3] Aggregated Delta H%% = %.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
		aggR3, vecR3[0], vecR3[1], vecR3[2], vecR3[3], vecR3[4])

	if aggR3 < 0.05 {
		t.Fatalf("Round 3: expected Aggregated Delta H%% > 0.05%%, got %.4f%%", aggR3)
	}

	// Also verify deterministic integral zero-sum balance for Round 3
	balancedR3, xorSumR3 := TestIntegralProperty(key, 3, true)
	if !balancedR3 {
		t.Fatalf("Round 3 integral zero-sum balance failed: xorSum=%x", xorSumR3)
	}

	// -------------------------------------------------------------
	// 5. ROUND 4: Multi-vector aggregated deficit across arbitrary keys
	// Evaluated on division property diagonal sets (Diagonal 0: {0, 5, 10, 15})
	// Requirement: Delta H > 0.05% across arbitrary keys (FIPS, zeros, ones, sequential)
	// -------------------------------------------------------------
	testKeysR4 := [][]byte{
		key, // FIPS 197 default key
		{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
	}

	for kIdx, kVal := range testKeysR4 {
		blocksR4 := make([][16]byte, numBlocks)
		for i := range numBlocks {
			var pt [16]byte
			// Diagonal 0 alignment: bytes {0, 5, 10, 15}
			pt[0] = byte(i)
			pt[5] = byte(i * 13)
			pt[10] = byte(i * 37)
			pt[15] = byte(i * 61)
			for j := 1; j < 16; j++ {
				if j != 5 && j != 10 && j != 15 {
					pt[j] = byte(j * 17)
				}
			}
			blocksR4[i] = EncryptReducedRound(pt[:], kVal, 4, true)
		}
		aggR4, vecR4 := ComputeMultiVectorDeficit(blocksR4)
		t.Logf("[Round 4 | Key %d] Aggregated Delta H%% = %.4f%% (V1=%.4f%%, V2=%.4f%%, V3=%.4f%%, V4=%.4f%%, V5=%.4f%%)",
			kIdx, aggR4, vecR4[0], vecR4[1], vecR4[2], vecR4[3], vecR4[4])

		if aggR4 < 0.05 {
			t.Fatalf("Round 4 (Key %d): expected aggregated Delta H%% > 0.05%%, got %.4f%%", kIdx, aggR4)
		}
	}

	// -------------------------------------------------------------
	// 6. CONTROL: Full 10-round AES
	// Requirement: Uniform noise floor, Delta H% <= 0.05%
	// -------------------------------------------------------------
	numControlBlocks := 16384
	ctR10 := make([]byte, numControlBlocks*16)
	for i := range numControlBlocks {
		var pt [16]byte
		rand.Read(pt[:])
		ct := EncryptReducedRound(pt[:], key, 10, false)
		copy(ctR10[i*16:(i+1)*16], ct[:])
	}
	_, _, deltaHPctR10 := ComputeShannonEntropy(ctR10)
	t.Logf("[Round 10 Control] Delta H%% = %.4f%%", deltaHPctR10)
	if deltaHPctR10 > 0.05 {
		t.Fatalf("Round 10 (Full AES control): expected Delta H%% <= 0.05%%, got %.4f%%", deltaHPctR10)
	}
}

func BenchmarkComputeShannonEntropy(b *testing.B) {
	data := make([]byte, 65536)
	rand.Read(data)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _, _ = ComputeShannonEntropy(data)
	}
}

func BenchmarkComputeBitSliceCorrelations(b *testing.B) {
	blocks := make([][16]byte, 1024)
	for i := range blocks {
		rand.Read(blocks[i][:])
	}
	b.SetBytes(int64(len(blocks) * 16))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = ComputeBitSliceCorrelations(blocks)
	}
}
