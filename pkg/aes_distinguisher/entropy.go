// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"encoding/binary"
	"math"
	"math/bits"

	"github.com/lemon4ksan/przm"
)

// EntropyReport aggregates multi-dimensional statistical and cryptanalytic metrics for a stream.
type EntropyReport struct {
	SampleBytes         int                // Total bytes evaluated
	ShannonEntropy      float64            // Byte Shannon entropy H_0 in bits/byte [0.0 .. 8.0]
	DeltaH              float64            // Shannon entropy deficit (8.0 - H_0)
	DeltaHPct           float64            // Percentage entropy deficit ((DeltaH / 8.0) * 100%)
	ChiSquare           float64            // Pearson Chi-Square statistic against uniform distribution
	ChiSquarePValue     float64            // Asymptotic p-value for Chi-Square test (df = 255)
	ChiSquareZScore     float64            // Standardized normal score: (ChiSquare - 255) / sqrt(510)
	BigramEntropy       float64            // Joint bigram entropy H_2 in bits/bigram [0.0 .. 16.0]
	BigramDeltaHPct     float64            // Bigram entropy deficit ((16.0 - BigramEntropy) / 16.0 * 100%)
	Stride4Entropy      float64            // Stride-4 joint byte entropy in bits/pair [0.0 .. 16.0]
	Stride4DeltaHPct    float64            // Stride-4 entropy deficit ((16.0 - Stride4Entropy) / 16.0 * 100%)
	ConditionalEntropy  float64            // Conditional entropy H(X_k | X_{k-1}) = H_2 - H_0
	TotalBitCorrelation float64            // Sum of squared off-diagonal bit cross-correlations
	DistinguisherFired  bool               // True if DeltaHPct > 0.05% or ChiSquareZScore > 3.29 (p < 0.001)
}

// ComputeShannonEntropy calculates the Shannon entropy H, the absolute deficit Delta H,
// and the relative percentage deficit Delta H % for byte-oriented data.
//
// Formula:
//
//	H = -\sum_{i=0}^{255} p_i \log_2(p_i)
//	\Delta H = 8.0 - H
//	\Delta H\% = (\Delta H / 8.0) \times 100\%
//
// If data is empty, returns (0.0, 8.0, 100.0).
func ComputeShannonEntropy(data []byte) (h float64, deltaH float64, deltaHPercent float64) {
	n := len(data)
	if n == 0 {
		return 0.0, 8.0, 100.0
	}

	var counts [256]uint64
	for _, b := range data {
		counts[b]++
	}

	fn := float64(n)
	sum := 0.0
	for _, c := range counts {
		if c > 0 {
			p := float64(c) / fn
			sum -= p * math.Log2(p)
		}
	}

	// Clamp to theoretical maximum of 8.0 bits/byte
	if sum > 8.0 {
		sum = 8.0
	} else if sum < 0.0 {
		sum = 0.0
	}

	h = sum
	deltaH = 8.0 - h
	if deltaH < 0.0 {
		deltaH = 0.0
	}
	deltaHPercent = (deltaH / 8.0) * 100.0
	return h, deltaH, deltaHPercent
}

// ComputeMillerMadowCorrection returns the finite-sample Miller-Madow bias correction in bits:
//
//	\Delta H_{\text{MM}} = \frac{K_{\text{obs}} - 1}{2 N \ln 2}
//
// where K_obs is the number of observed bins with non-zero counts (out of 256).
// For N = 0 or K_obs <= 1, returns 0.0.
func ComputeMillerMadowCorrection(data []byte) float64 {
	n := len(data)
	if n == 0 {
		return 0.0
	}
	var counts [256]uint64
	for _, b := range data {
		counts[b]++
	}
	kObs := 0
	for _, c := range counts {
		if c > 0 {
			kObs++
		}
	}
	if kObs <= 1 {
		return 0.0
	}
	return float64(kObs-1) / (2.0 * float64(n) * math.Ln2)
}

// ComputeShannonEntropyCorrected calculates the finite-sample Miller-Madow bias-corrected
// Shannon entropy:
//
//	H_{\text{corrected}} = \hat{H} + \frac{K_{\text{obs}} - 1}{2 N \ln 2}
//	\Delta H = 8.0 - H_{\text{corrected}}
//	\Delta H\% = (\Delta H / 8.0) \times 100\%
//
// Clamped to [0.0, 8.0]. If data is empty, returns (0.0, 8.0, 100.0).
func ComputeShannonEntropyCorrected(data []byte) (hCorrected float64, deltaH float64, deltaHPercent float64) {
	n := len(data)
	if n == 0 {
		return 0.0, 8.0, 100.0
	}

	h, _, _ := ComputeShannonEntropy(data)
	bias := ComputeMillerMadowCorrection(data)
	hCorrected = h + bias
	if hCorrected > 8.0 {
		hCorrected = 8.0
	} else if hCorrected < 0.0 {
		hCorrected = 0.0
	}

	deltaH = 8.0 - hCorrected
	if deltaH < 0.0 {
		deltaH = 0.0
	}
	deltaHPercent = (deltaH / 8.0) * 100.0
	return hCorrected, deltaH, deltaHPercent
}

// ComputeChiSquared computes the Pearson Chi-Square goodness-of-fit statistic against the uniform distribution
// over 256 byte bins, and returns (chi2, pValue, zScore) reconciling PROJECT.md:69 and PROJECT.md:34.
//
// Under the uniform null hypothesis:
//
//	E = N / 256
//	\chi^2 = \sum_{b=0}^{255} (C[b] - E)^2 / E = (256/N) \sum C[b]^2 - N
//	Degrees of Freedom: df = 255
//	\mathbb{E}[\chi^2] = 255, \mathrm{Var}(\chi^2) = 2 \times 255 = 510 \implies \sigma = \sqrt{510} \approx 22.583
//	Z = (\chi^2 - 255) / \sqrt{510}
//	pValue = ComputeChiSquaredPValue(\chi^2)
func ComputeChiSquared(data []byte) (chi2 float64, pValue float64, zScore float64) {
	n := len(data)
	if n == 0 {
		return 0.0, 1.0, 0.0
	}

	var counts [256]uint64
	for _, b := range data {
		counts[b]++
	}

	fn := float64(n)
	sumSq := 0.0
	for _, c := range counts {
		sumSq += float64(c) * float64(c)
	}

	chi2 = (256.0 / fn) * sumSq - fn
	if chi2 < 0.0 {
		chi2 = 0.0
	}

	const meanChi2 = 255.0
	const stdDevChi2 = 22.583179581272447 // sqrt(510.0)
	zScore = (chi2 - meanChi2) / stdDevChi2
	pValue = ComputeChiSquaredPValue(chi2)
	return chi2, pValue, zScore
}

// ComputeChiSquaredPValue computes the p-value under the Chi-Square distribution (df = 255)
// using the normal approximation for large degrees of freedom.
func ComputeChiSquaredPValue(chi2 float64) float64 {
	const meanChi2 = 255.0
	const stdDevChi2 = 22.583179581272447
	z := (chi2 - meanChi2) / stdDevChi2
	if z <= -10.0 {
		return 1.0
	}
	if z >= 10.0 {
		return 0.0
	}
	return 0.5 * math.Erfc(z/math.Sqrt2)
}

// ComputeBigramEntropy calculates the joint 2-gram Shannon entropy over consecutive byte pairs (data[i], data[i+1]).
// Max bigram entropy is 16.0 bits.
func ComputeBigramEntropy(data []byte) (h2 float64, deltaH2 float64, deltaH2Percent float64) {
	n := len(data)
	if n < 2 {
		return 0.0, 16.0, 100.0
	}

	var counts [256][256]uint32
	totalBigrams := n - 1

	for i := 0; i < totalBigrams; i++ {
		u := data[i]
		v := data[i+1]
		counts[u][v]++
	}

	fn := float64(totalBigrams)
	sum := 0.0
	for u := 0; u < 256; u++ {
		for v := 0; v < 256; v++ {
			c := counts[u][v]
			if c > 0 {
				p := float64(c) / fn
				sum -= p * math.Log2(p)
			}
		}
	}

	if sum > 16.0 {
		sum = 16.0
	} else if sum < 0.0 {
		sum = 0.0
	}

	h2 = sum
	deltaH2 = 16.0 - h2
	if deltaH2 < 0.0 {
		deltaH2 = 0.0
	}
	deltaH2Percent = (deltaH2 / 16.0) * 100.0
	return h2, deltaH2, deltaH2Percent
}

// ComputeStrideEntropy calculates the joint Shannon entropy over byte pairs separated by stride: (data[i], data[i+stride]).
// Max entropy is 16.0 bits.
func ComputeStrideEntropy(data []byte, stride int) (h float64, deltaH float64, deltaHPercent float64) {
	if stride <= 0 || len(data) <= stride {
		return 0.0, 16.0, 100.0
	}

	var counts [256][256]uint32
	totalPairs := len(data) - stride

	for i := 0; i < totalPairs; i++ {
		u := data[i]
		v := data[i+stride]
		counts[u][v]++
	}

	fn := float64(totalPairs)
	sum := 0.0
	for u := 0; u < 256; u++ {
		for v := 0; v < 256; v++ {
			c := counts[u][v]
			if c > 0 {
				p := float64(c) / fn
				sum -= p * math.Log2(p)
			}
		}
	}

	if sum > 16.0 {
		sum = 16.0
	} else if sum < 0.0 {
		sum = 0.0
	}

	h = sum
	deltaH = 16.0 - h
	if deltaH < 0.0 {
		deltaH = 0.0
	}
	deltaHPercent = (deltaH / 16.0) * 100.0
	return h, deltaH, deltaHPercent
}

// ComputeStride4Entropy evaluates the stride-4 joint byte entropy corresponding to adjacent column
// elements in the AES state matrix.
func ComputeStride4Entropy(data []byte) (h float64, deltaH float64, deltaHPercent float64) {
	return ComputeStrideEntropy(data, 4)
}

// ComputeConditionalEntropy computes H(X_k | X_{k-1}) = H_2(X) - H_0(X).
func ComputeConditionalEntropy(data []byte) float64 {
	h0, _, _ := ComputeShannonEntropy(data)
	h2, _, _ := ComputeBigramEntropy(data)
	cond := h2 - h0
	if cond < 0.0 {
		return 0.0
	}
	if cond > 8.0 {
		return 8.0
	}
	return cond
}

// ComputeBitSliceCorrelations computes the 128x128 Pearson cross-correlation matrix across all 128 bit positions
// of the AES state over an ensemble of 16-byte blocks.
//
// It leverages PRZM's SWAR Transpose8x8 bit-matrix transposition to unpack bit slices at hardware speed.
func ComputeBitSliceCorrelations(blocks [][16]byte) [128][128]float64 {
	var corr [128][128]float64
	m := len(blocks)
	if m < 2 {
		for i := 0; i < 128; i++ {
			corr[i][i] = 1.0
		}
		return corr
	}

	// Pack each bit coordinate into a bit vector of uint64 words: ceil(m / 64)
	numWords := (m + 63) / 64
	// bitVectors[b] stores bit b across all m blocks
	bitVectors := make([][]uint64, 128)
	for b := 0; b < 128; b++ {
		bitVectors[b] = make([]uint64, numWords)
	}

	// Extract bit planes across all blocks using PRZM Transpose8x8
	for k, block := range blocks {
		wordIdx := k / 64
		bitShift := uint(k % 64)

		// Unpack low 64 bits (bytes 0..7)
		lowWord := binary.LittleEndian.Uint64(block[0:8])
		transLow := przm.Transpose8x8(lowWord)
		for p := 0; p < 8; p++ {
			planeByte := byte(transLow >> (p * 8))
			for bIdx := 0; bIdx < 8; bIdx++ {
				bitVal := uint64((planeByte >> bIdx) & 1)
				globalBit := bIdx*8 + p // byte bIdx, bit p
				bitVectors[globalBit][wordIdx] |= (bitVal << bitShift)
			}
		}

		// Unpack high 64 bits (bytes 8..15)
		highWord := binary.LittleEndian.Uint64(block[8:16])
		transHigh := przm.Transpose8x8(highWord)
		for p := 0; p < 8; p++ {
			planeByte := byte(transHigh >> (p * 8))
			for bIdx := 0; bIdx < 8; bIdx++ {
				bitVal := uint64((planeByte >> bIdx) & 1)
				globalBit := 64 + bIdx*8 + p // byte (8 + bIdx), bit p
				bitVectors[globalBit][wordIdx] |= (bitVal << bitShift)
			}
		}
	}

	// Compute marginal popcounts
	counts := make([]int, 128)
	for b := 0; b < 128; b++ {
		c := 0
		for _, w := range bitVectors[b] {
			c += bits.OnesCount64(w)
		}
		counts[b] = c
	}

	fm := float64(m)

	// Compute pairwise correlations
	for i := 0; i < 128; i++ {
		corr[i][i] = 1.0
		pi := float64(counts[i]) / fm
		varI := pi * (1.0 - pi)

		for j := i + 1; j < 128; j++ {
			pj := float64(counts[j]) / fm
			varJ := pj * (1.0 - pj)

			denom := math.Sqrt(varI * varJ)
			if denom < 1e-12 {
				corr[i][j] = 0.0
				corr[j][i] = 0.0
				continue
			}

			// Pairwise AND popcount
			pairCount := 0
			vecI := bitVectors[i]
			vecJ := bitVectors[j]
			for w := 0; w < numWords; w++ {
				pairCount += bits.OnesCount64(vecI[w] & vecJ[w])
			}

			pij := float64(pairCount) / fm
			cov := pij - (pi * pj)
			r := cov / denom
			if r > 1.0 {
				r = 1.0
			} else if r < -1.0 {
				r = -1.0
			}

			corr[i][j] = r
			corr[j][i] = r
		}
	}

	return corr
}

// ComputeTotalBitCorrelation sums the squared off-diagonal Pearson correlation coefficients
// across the 128x128 bit matrix: \mathcal{C}_{\text{total}} = \sum_{0 \le i < j < 128} \rho_{ij}^2.
func ComputeTotalBitCorrelation(corr *[128][128]float64) float64 {
	total := 0.0
	for i := 0; i < 128; i++ {
		for j := i + 1; j < 128; j++ {
			r := corr[i][j]
			total += r * r
		}
	}
	return total
}

// AnalyzeStreamEntropy executes the multi-dimensional statistical evaluation pipeline on a byte stream.
func AnalyzeStreamEntropy(data []byte) EntropyReport {
	shannonH, deltaH, deltaHPct := ComputeShannonEntropyCorrected(data)
	chi2, pValue, zScore := ComputeChiSquared(data)
	bigramH, _, bigramDeltaHPct := ComputeBigramEntropy(data)
	stride4H, _, stride4DeltaHPct := ComputeStride4Entropy(data)
	condH := ComputeConditionalEntropy(data)

	totalBitCorr := 0.0
	numBlocks := len(data) / 16
	if numBlocks >= 64 {
		blocks := make([][16]byte, numBlocks)
		for i := 0; i < numBlocks; i++ {
			copy(blocks[i][:], data[i*16:(i+1)*16])
		}
		corr := ComputeBitSliceCorrelations(blocks)
		totalBitCorr = ComputeTotalBitCorrelation(&corr)
	}

	distinguisherFired := (deltaHPct > 0.05) || (zScore > 3.29)

	return EntropyReport{
		SampleBytes:         len(data),
		ShannonEntropy:      shannonH,
		DeltaH:              deltaH,
		DeltaHPct:           deltaHPct,
		ChiSquare:           chi2,
		ChiSquarePValue:     pValue,
		ChiSquareZScore:     zScore,
		BigramEntropy:       bigramH,
		BigramDeltaHPct:     bigramDeltaHPct,
		Stride4Entropy:      stride4H,
		Stride4DeltaHPct:    stride4DeltaHPct,
		ConditionalEntropy:  condH,
		TotalBitCorrelation: totalBitCorr,
		DistinguisherFired:  distinguisherFired,
	}
}
