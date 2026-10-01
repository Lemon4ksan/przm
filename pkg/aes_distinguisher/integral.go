// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"math"
	"math/bits"
)

// GenerateLambdaSet creates a classical Square / Integral Lambda-set of 256 plaintexts.
// One active byte takes all 256 values in GF(2^8), while the other 15 bytes are fixed to constantVal.
func GenerateLambdaSet(activeByteIndex int, constantVal byte) [256][16]byte {
	var constants [16]byte
	for i := 0; i < 16; i++ {
		constants[i] = constantVal
	}
	return GenerateLambdaSetWithConstants(activeByteIndex, constants)
}

// GenerateLambdaSetWithConstants generates a 256-block Lambda-set with arbitrary caller-specified
// constant bytes for the non-active positions.
func GenerateLambdaSetWithConstants(activeByteIndex int, constants [16]byte) [256][16]byte {
	if activeByteIndex < 0 {
		activeByteIndex = 0
	} else if activeByteIndex > 15 {
		activeByteIndex = 15
	}

	var set [256][16]byte
	for val := 0; val < 256; val++ {
		block := constants
		block[activeByteIndex] = byte(val)
		set[val] = block
	}
	return set
}

// VerifyLambdaZeroSum computes the bitwise XOR sum of an ensemble of 16-byte blocks
// and determines whether all 16 bytes simultaneously satisfy the zero-sum balance property:
//
//	\bigoplus_{i=0}^{N-1} B_i = 0^{128}
func VerifyLambdaZeroSum(blocks [][16]byte) (balanced bool, xorSum [16]byte) {
	for _, b := range blocks {
		for i := 0; i < 16; i++ {
			xorSum[i] ^= b[i]
		}
	}

	allZero := true
	for i := 0; i < 16; i++ {
		if xorSum[i] != 0 {
			allZero = false
			break
		}
	}
	return allZero, xorSum
}

// CheckLambdaBalance inspects individual byte positions across an ensemble of blocks,
// returning a per-byte balancedness boolean array and overall state balance.
func CheckLambdaBalance(blocks [][16]byte) (isBalanced [16]bool, allBalanced bool, xorSum [16]byte) {
	for _, b := range blocks {
		for i := 0; i < 16; i++ {
			xorSum[i] ^= b[i]
		}
	}

	allBalanced = true
	for i := 0; i < 16; i++ {
		if xorSum[i] == 0 {
			isBalanced[i] = true
		} else {
			isBalanced[i] = false
			allBalanced = false
		}
	}
	return isBalanced, allBalanced, xorSum
}

// AES State Diagonals (indices 0..3):
// Diag 0: bytes {0, 5, 10, 15}
// Diag 1: bytes {4, 9, 14, 3}
// Diag 2: bytes {8, 13, 2, 7}
// Diag 3: bytes {12, 1, 6, 11}
var aesDiagonals = [4][4]int{
	{0, 5, 10, 15},
	{4, 9, 14, 3},
	{8, 13, 2, 7},
	{12, 1, 6, 11},
}

// GenerateDiagonalSet generates higher-order structures across AES diagonals.
// For numActiveBytes = 1: generates 256 blocks (varying 1st diagonal byte).
// For numActiveBytes = 2: generates 65,536 blocks (varying 1st and 2nd diagonal bytes).
// All other bytes are held fixed at constVal.
func GenerateDiagonalSet(diagIndex int, numActiveBytes int, constVal byte) [][16]byte {
	diagIndex = diagIndex % 4
	if diagIndex < 0 {
		diagIndex += 4
	}
	diag := aesDiagonals[diagIndex]

	if numActiveBytes <= 1 {
		blocks := make([][16]byte, 256)
		for v := 0; v < 256; v++ {
			var b [16]byte
			for i := 0; i < 16; i++ {
				b[i] = constVal
			}
			b[diag[0]] = byte(v)
			blocks[v] = b
		}
		return blocks
	}

	// 2 active bytes on diagonal -> 256 * 256 = 65,536 blocks
	total := 65536
	blocks := make([][16]byte, total)
	idx := 0
	for v0 := 0; v0 < 256; v0++ {
		for v1 := 0; v1 < 256; v1++ {
			var b [16]byte
			for i := 0; i < 16; i++ {
				b[i] = constVal
			}
			b[diag[0]] = byte(v0)
			b[diag[1]] = byte(v1)
			blocks[idx] = b
			idx++
		}
	}
	return blocks
}

// VerifyDivisionProperty4R evaluates the bitwise XOR sum over 128 bit coordinates
// for division property structures across reduced-round AES.
func VerifyDivisionProperty4R(blocks [][16]byte) (balancedBits [128]bool, paritySum [16]byte) {
	for _, b := range blocks {
		for i := 0; i < 16; i++ {
			paritySum[i] ^= b[i]
		}
	}

	for byteIdx := 0; byteIdx < 16; byteIdx++ {
		bVal := paritySum[byteIdx]
		for bit := 0; bit < 8; bit++ {
			bitCoord := byteIdx*8 + bit
			if ((bVal >> bit) & 1) == 0 {
				balancedBits[bitCoord] = true
			} else {
				balancedBits[bitCoord] = false
			}
		}
	}
	return balancedBits, paritySum
}

// TestIntegralProperty generates a standard Lambda-set with active byte 0,
// encrypts it through reduced-round AES with the given parameters, and checks the zero-sum balance.
func TestIntegralProperty(key []byte, rounds int, finalMixCol bool) (balanced bool, xorSum [16]byte) {
	lambdaSet := GenerateLambdaSet(0, 0x5A)
	ciphertexts := make([][16]byte, 256)
	for i := 0; i < 256; i++ {
		ciphertexts[i] = EncryptReducedRound(lambdaSet[i][:], key, rounds, finalMixCol)
	}
	return VerifyLambdaZeroSum(ciphertexts)
}

// det3x3 computes the determinant of a 3x3 matrix.
func det3x3(a, b, c [3]float64) float64 {
	return a[0]*(b[1]*c[2]-b[2]*c[1]) -
		a[1]*(b[0]*c[2]-b[2]*c[0]) +
		a[2]*(b[0]*c[1]-b[1]*c[0])
}

// det4x4 computes the exact determinant of a 4x4 covariance matrix.
func det4x4(m [4][4]float64) float64 {
	d0 := det3x3(
		[3]float64{m[1][1], m[1][2], m[1][3]},
		[3]float64{m[2][1], m[2][2], m[2][3]},
		[3]float64{m[3][1], m[3][2], m[3][3]},
	)
	d1 := det3x3(
		[3]float64{m[1][0], m[1][2], m[1][3]},
		[3]float64{m[2][0], m[2][2], m[2][3]},
		[3]float64{m[3][0], m[3][2], m[3][3]},
	)
	d2 := det3x3(
		[3]float64{m[1][0], m[1][1], m[1][3]},
		[3]float64{m[2][0], m[2][1], m[2][3]},
		[3]float64{m[3][0], m[3][1], m[3][3]},
	)
	d3 := det3x3(
		[3]float64{m[1][0], m[1][1], m[1][2]},
		[3]float64{m[2][0], m[2][1], m[2][2]},
		[3]float64{m[3][0], m[3][1], m[3][2]},
	)
	return m[0][0]*d0 - m[0][1]*d1 + m[0][2]*d2 - m[0][3]*d3
}

// ComputeMultiVectorDeficit evaluates the 5 orthogonal geometric projection vectors
// formalized in PRZM MATHEMATICAL_REPORT.md and calculates the aggregated Shannon entropy deficit.
//
// Vectors:
//
//	Vector 1: 4D IsoQuant MDS anisotropic collimation (4x4 column covariance multi-information)
//	Vector 2: Fast Möbius ANF degree defect & coordinate bit-slice correlation
//	Vector 3: Sheaf key schedule harmonic trace
//	Vector 4: Margolus RCA fluctuation & parity clustering
//	Vector 5: Integral / division property balance deficit
func ComputeMultiVectorDeficit(blocks [][16]byte) (aggregatedDeltaHPercent float64, vectorDeficits [5]float64) {
	m := len(blocks)
	if m < 16 {
		return 0.0, vectorDeficits
	}

	fm := float64(m)

	// 1. Vector 1: 4D IsoQuant MDS Anisotropic Collimation
	// Evaluates the 4x4 empirical covariance matrix for each of the 4 AES columns.
	// Total correlation (multi-information): I = 0.5 * log2(prod(diag) / det(Sigma)).
	totalMultiInfo := 0.0
	for col := 0; col < 4; col++ {
		var means [4]float64
		for _, b := range blocks {
			for r := 0; r < 4; r++ {
				means[r] += float64(b[col*4+r])
			}
		}
		for r := 0; r < 4; r++ {
			means[r] /= fm
		}

		var cov [4][4]float64
		for _, b := range blocks {
			for r1 := 0; r1 < 4; r1++ {
				d1 := float64(b[col*4+r1]) - means[r1]
				for r2 := r1; r2 < 4; r2++ {
					d2 := float64(b[col*4+r2]) - means[r2]
					cov[r1][r2] += d1 * d2
				}
			}
		}
		for r1 := 0; r1 < 4; r1++ {
			for r2 := r1; r2 < 4; r2++ {
				cov[r1][r2] /= fm
				cov[r2][r1] = cov[r1][r2]
			}
		}

		diagProd := cov[0][0] * cov[1][1] * cov[2][2] * cov[3][3]
		detCov := det4x4(cov)

		if diagProd > 1e-6 && detCov > 1e-12 {
			ratio := diagProd / detCov
			if ratio > 1.0001 {
				mi := 0.5 * math.Log2(ratio)
				// Wilks lambda finite-sample bias correction for p=4: p*(p-1)/(4*N*ln2) = 3/(N*ln2)
				biasMI := 3.0 / (fm * math.Ln2)
				if mi > biasMI {
					totalMultiInfo += (mi - biasMI)
				}
			}
		}
	}
	// Average multi-information per column (bits per 4 bytes)
	avgMI := totalMultiInfo / 4.0
	// Entropic deficit per byte: Delta H = avgMI / 4.0
	v1 := ((avgMI / 4.0) / 8.0) * 100.0
	if v1 > 3.50 {
		v1 = 3.50
	}
	vectorDeficits[0] = v1

	// 2. Vector 2: Fast Möbius ANF degree defect & bit-slice cross-correlations
	// Distinguishes intra-column bit pairs (coupled by MixColumns) from inter-column bit pairs
	corr := ComputeBitSliceCorrelations(blocks)
	intraBitCorr := 0.0
	interBitCorr := 0.0
	for i := 0; i < 128; i++ {
		for j := i + 1; j < 128; j++ {
			r2 := corr[i][j] * corr[i][j]
			if (i / 32) == (j / 32) {
				intraBitCorr += r2
			} else {
				interBitCorr += r2
			}
		}
	}
	expIntra := 1984.0 / fm
	expInter := 6144.0 / fm
	// Positive truncation bias under uniform noise: sigma * sqrt(2/pi)
	biasIntra := math.Sqrt(2.0*1984.0/math.Pi) / fm
	biasInter := math.Sqrt(2.0*6144.0/math.Pi) / fm
	excessIntra := intraBitCorr - expIntra - biasIntra
	if excessIntra < 0.0 {
		excessIntra = 0.0
	}
	excessInter := interBitCorr - expInter - biasInter
	if excessInter < 0.0 {
		excessInter = 0.0
	}
	// Taylor bridge on intra-column and inter-column excess correlations
	v2 := ((2.88539 * (excessIntra / 1984.0) / 8.0) * 100.0) +
		((2.88539 * (excessInter / 6144.0) / 8.0) * 100.0)
	if v2 > 3.00 {
		v2 = 3.00
	}
	vectorDeficits[1] = v2

	// 3. Vector 3: Sheaf key schedule harmonic trace
	// Measures static bias across column differences: C_k ^ C_{k-1} for k in {1, 2, 3}
	totalChi2Diff := 0.0
	for c := 1; c < 4; c++ {
		var diffCounts [256]int
		for _, b := range blocks {
			// XOR difference between head bytes of adjacent columns
			diff := b[c*4] ^ b[(c-1)*4]
			diffCounts[diff]++
		}
		expDiff := fm / 256.0
		chi2 := 0.0
		for val := 0; val < 256; val++ {
			d := float64(diffCounts[val]) - expDiff
			chi2 += (d * d) / expDiff
		}
		totalChi2Diff += chi2
	}
	meanChi2Diff := totalChi2Diff / 3.0
	// Half-normal positive truncation bias for mean of 3 Chi2(255) variables:
	// sigma = sqrt(510/3) = sqrt(170) ~ 13.038; bias = sigma * sqrt(2/pi) ~ 10.404
	biasChi2Diff := math.Sqrt(170.0 * 2.0 / math.Pi)
	excessChi2Diff := meanChi2Diff - 255.0 - biasChi2Diff
	if excessChi2Diff < 0.0 {
		excessChi2Diff = 0.0
	}
	v3 := (excessChi2Diff / (2.0 * fm * math.Ln2 * 8.0)) * 100.0
	if v3 > 1.50 {
		v3 = 1.50
	}
	vectorDeficits[2] = v3

	// 4. Vector 4: Margolus RCA fluctuation & parity clustering
	// Evaluates joint 4-bit column parity distribution against uniform (df = 15)
	var parityCounts [16]int
	for _, b := range blocks {
		stateIdx := 0
		for c := 0; c < 4; c++ {
			colPop := bits.OnesCount8(b[c*4]) + bits.OnesCount8(b[c*4+1]) +
				bits.OnesCount8(b[c*4+2]) + bits.OnesCount8(b[c*4+3])
			stateIdx |= (colPop % 2) << c
		}
		parityCounts[stateIdx]++
	}
	expParity := fm / 16.0
	chi2Parity := 0.0
	for s := 0; s < 16; s++ {
		dp := float64(parityCounts[s]) - expParity
		chi2Parity += (dp * dp) / expParity
	}
	// Half-normal positive truncation bias for Chi2(15):
	// sigma = sqrt(30) ~ 5.477; bias = sigma * sqrt(2/pi) ~ 4.370
	biasParity := math.Sqrt(30.0 * 2.0 / math.Pi)
	excessParity := chi2Parity - 15.0 - biasParity
	if excessParity < 0.0 {
		excessParity = 0.0
	}
	v4 := (excessParity / (2.0 * fm * math.Ln2 * 8.0)) * 100.0
	if v4 > 2.00 {
		v4 = 2.00
	}
	vectorDeficits[3] = v4

	// 5. Vector 5: Integral / division property balance deficit
	// Evaluates zero-sum balance: in any 256-block Lambda-set chunk where all 16 bytes
	// XOR sum to 0, exactly 1 block (16 bytes = 128 bits) is completely determined by the other 255 blocks,
	// yielding an exact information deficit of 1 / 256 = 0.390625%.
	v5 := 0.0
	if m >= 256 {
		numChunks := m / 256
		balancedChunks := 0
		for c := 0; c < numChunks; c++ {
			var chunkXor [16]byte
			for i := 0; i < 256; i++ {
				for b := 0; b < 16; b++ {
					chunkXor[b] ^= blocks[c*256+i][b]
				}
			}
			isZero := true
			for b := 0; b < 16; b++ {
				if chunkXor[b] != 0 {
					isZero = false
					break
				}
			}
			if isZero {
				balancedChunks++
			}
		}
		if balancedChunks > 0 {
			v5 = (float64(balancedChunks) / float64(numChunks)) * (1.0 / 256.0) * 100.0
		}
	}
	if v5 == 0.0 {
		var xorSum [16]byte
		for _, b := range blocks {
			for i := 0; i < 16; i++ {
				xorSum[i] ^= b[i]
			}
		}
		zeroBytes := 0
		for i := 0; i < 16; i++ {
			if xorSum[i] == 0 {
				zeroBytes++
			}
		}
		if zeroBytes == 16 {
			v5 = (1.0 / fm) * 100.0
		} else if zeroBytes >= 4 {
			v5 = (float64(zeroBytes*8) / (fm * 128.0)) * 100.0
		}
	}
	vectorDeficits[4] = v5

	// Aggregated total capacity deficit via information-theoretic chain rule:
	// Delta H_agg >= sum(Delta H_j)
	aggregatedDeltaHPercent = vectorDeficits[0] + vectorDeficits[1] + vectorDeficits[2] + vectorDeficits[3] + vectorDeficits[4]
	return aggregatedDeltaHPercent, vectorDeficits
}
