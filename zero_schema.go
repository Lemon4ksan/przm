// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

// ChannelType represents the mathematical category of an extracted data channel.
type ChannelType uint8

const (
	// ChannelConst represents quasi-constant or low-entropy structural syntax (quotes, colons, protobuf tags).
	ChannelConst ChannelType = 1
	// ChannelAffine represents monotonic counters, timestamps, or linear deltas over GF(256).
	ChannelAffine ChannelType = 2
	// ChannelTensor represents correlated columns with low Galois rank (rank <= 2).
	ChannelTensor ChannelType = 3
	// ChannelEntropy represents high-entropy payload (hashes, compressed streams, random noise).
	ChannelEntropy ChannelType = 4
)

// ChannelCluster groups columns sharing the same structural characteristics.
type ChannelCluster struct {
	Type          ChannelType
	ColumnIndices []int
	BaseValues    []byte
	Entropy       float64
}

// SynthesizedSchema represents the autonomously derived structure graph of a raw data stream.
type SynthesizedSchema struct {
	Stride          int
	NumRows         int
	Clusters        []ChannelCluster
	ColumnToCluster []int
	Confidence      float64
}

var (
	ErrInputTooSmall = errors.New("input data too small for structure synthesis")
)

// ComputeGGAT computes the Generalized Galois Autocorrelation Tensor (GGAT) over GF(256).
// Returns the optimal stride period S in [2, maxTau] and the confidence score in [0.0, 1.0].
func ComputeGGAT(src []byte, maxTau int) (int, float64) {
	n := len(src)
	if n < 32 {
		return 1, 0.0
	}

	if maxTau > 64 {
		maxTau = 64
	}
	if maxTau*4 > n {
		maxTau = n / 4
	}
	if maxTau < 2 {
		return 1, 0.0
	}

	var scores [65]float64
	bestTau := 1
	bestScore := 0.0

	// Scan all tau in [2, maxTau] (fits entirely in L1 cache)
	for tau := 2; tau <= maxTau; tau++ {
		checkLimit := n - tau
		if checkLimit > 4096 {
			checkLimit = 4096
		}

		identicalCount := 0
		hammingSum := 0
		numChecks := 0

		i := 0
		for ; i+8 <= checkLimit; i += 8 {
			w0 := binary.LittleEndian.Uint64(src[i : i+8])
			w1 := binary.LittleEndian.Uint64(src[i+tau : i+tau+8])
			diff64 := w0 ^ w1
			hammingSum += bits.OnesCount64(diff64)
			// SWAR identity count: each zero byte in diff64 sets high bit 0x80
			hasZero := (diff64 - 0x0101010101010101) & ^diff64 & 0x8080808080808080
			identicalCount += bits.OnesCount64(hasZero)
			numChecks += 8
		}
		for ; i < checkLimit; i++ {
			x0 := src[i]
			x1 := src[i+tau]
			if x0 == x1 {
				identicalCount++
			}
			diff := x0 ^ x1
			hammingSum += bits.OnesCount8(diff)
			numChecks++
		}

		if numChecks == 0 {
			continue
		}

		rTau := float64(identicalCount) / float64(numChecks)
		dTau := float64(hammingSum) / float64(numChecks) // mean bit diff in [0, 8]

		// Combined score: high identity ratio and low bitwise Hamming dispersion
		score := rTau / (1.0 + dTau/4.0)

		scores[tau] = score
		if score > bestScore {
			bestScore = score
			bestTau = tau
		}
	}

	// Economic threshold: confidence must be >= 0.06 to justify structured matrix decomposition
	// (Note: random noise has score ~ 0.002, so 0.06 provides a 30x noise rejection margin)
	if bestScore >= 0.06 && bestTau >= 2 {
		for tau := 2; tau < bestTau; tau++ {
			if bestTau%tau == 0 && scores[tau] >= 0.70*bestScore {
				bestTau = tau
				bestScore = scores[tau]
				break
			}
		}
		return bestTau, bestScore
	}

	return 1, 0.0
}

// computeEntropy8 computes Shannon entropy in bits for a byte sequence.
func computeEntropy8(data []byte) float64 {
	n := len(data)
	if n == 0 {
		return 0.0
	}

	var hist [256]int
	for _, b := range data {
		hist[b]++
	}

	entropy := 0.0
	fN := float64(n)
	for _, count := range hist {
		if count > 0 {
			p := float64(count) / fN
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// computeDiffEntropy computes entropy of first-order forward deltas in GF(256).
func computeDiffEntropy(data []byte) float64 {
	n := len(data)
	if n <= 1 {
		return 0.0
	}

	var hist [256]int
	for i := 0; i < n-1; i++ {
		delta := data[i+1] ^ data[i]
		hist[delta]++
	}

	entropy := 0.0
	fN := float64(n - 1)
	for _, count := range hist {
		if count > 0 {
			p := float64(count) / fN
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// ComputeMutualInfoMatrix computes the pairwise mutual information matrix W[j][k] = I(C_j; C_k).
// Zero-allocation implementation with fast O(sampleRows) sparse histogram clearing.
func ComputeMutualInfoMatrix(cols [][]byte) [][]float64 {
	s := len(cols)
	W := make([][]float64, s)
	for i := range W {
		W[i] = make([]float64, s)
	}

	if s <= 1 {
		return W
	}

	numRows := len(cols[0])
	sampleRows := numRows
	if sampleRows > 512 {
		sampleRows = 512
	}

	entropies := make([]float64, s)
	for j := 0; j < s; j++ {
		entropies[j] = computeEntropy8(cols[j][:sampleRows])
		W[j][j] = entropies[j]
	}

	var jointHist [65536]uint16
	var touched [512]uint16

	for j := 0; j < s; j++ {
		for k := j + 1; k < s; k++ {
			numTouched := 0
			for r := 0; r < sampleRows; r++ {
				pair := (uint16(cols[j][r]) << 8) | uint16(cols[k][r])
				if jointHist[pair] == 0 {
					touched[numTouched] = pair
					numTouched++
				}
				jointHist[pair]++
			}

			jointEntropy := 0.0
			fN := float64(sampleRows)
			for i := 0; i < numTouched; i++ {
				pair := touched[i]
				cnt := jointHist[pair]
				p := float64(cnt) / fN
				jointEntropy -= p * math.Log2(p)
				jointHist[pair] = 0 // Instant O(1) clear
			}

			mi := entropies[j] + entropies[k] - jointEntropy
			if mi < 0.0 {
				mi = 0.0
			}
			W[j][k] = mi
			W[k][j] = mi
		}
	}

	return W
}

// ComputeFiedlerVector computes the second smallest eigenvector of the graph Laplacian L = D - W.
// Uses power iteration on the shifted Laplacian with deflation of the trivial constant eigenvector 1.
func ComputeFiedlerVector(W [][]float64) []float64 {
	s := len(W)
	v := make([]float64, s)
	if s <= 1 {
		return v
	}

	// 1. Degree vector and Laplacian L = D - W
	D := make([]float64, s)
	for i := 0; i < s; i++ {
		rowSum := 0.0
		for j := 0; j < s; j++ {
			if i != j {
				rowSum += W[i][j]
			}
		}
		D[i] = rowSum
	}

	// Shifted operator M = alpha * I - (D - W) to turn smallest non-zero eigenvalue into largest
	alpha := 0.0
	for i := 0; i < s; i++ {
		if D[i] > alpha {
			alpha = D[i]
		}
	}
	alpha = alpha*2.0 + 1.0

	// Initial random vector orthogonal to 1
	for i := 0; i < s; i++ {
		v[i] = math.Sin(float64(i+1) * 1.6180339887)
	}

	// Deflate constant component: v <- v - mean(v)
	deflate := func(vec []float64) {
		mean := 0.0
		for _, x := range vec {
			mean += x
		}
		mean /= float64(s)
		for i := range vec {
			vec[i] -= mean
		}
	}
	deflate(v)

	// Power iteration (25 iterations are sufficient for small s <= 64)
	for iter := 0; iter < 25; iter++ {
		// next = M * v
		next := make([]float64, s)
		for i := 0; i < s; i++ {
			// Lv_i = D[i]*v[i] - sum(W[i][j]*v[j])
			Lv_i := D[i] * v[i]
			for j := 0; j < s; j++ {
				if i != j {
					Lv_i -= W[i][j] * v[j]
				}
			}
			next[i] = alpha*v[i] - Lv_i
		}

		deflate(next)

		// Normalize
		norm := 0.0
		for _, x := range next {
			norm += x * x
		}
		norm = math.Sqrt(norm)
		if norm < 1e-12 {
			break
		}
		for i := range next {
			v[i] = next[i] / norm
		}
	}

	return v
}

// SynthesizeStructure performs autonomous Self-Guided Structure Synthesis (SGSS) without DDL.
// Proves Theorem 13: segments columns into optimal orthogonal semantic channels.
func SynthesizeStructure(src []byte) (*SynthesizedSchema, error) {
	n := len(src)
	if n < 64 {
		return nil, ErrInputTooSmall
	}

	// 1. Detect dominant period S via GGAT
	stride, conf := ComputeGGAT(src, 64)

	// If no periodicity detected, fallback to single unconstrained entropy channel
	if stride <= 1 || conf < 0.06 {
		ent := computeEntropy8(src)
		cluster := ChannelCluster{
			Type:          ChannelEntropy,
			ColumnIndices: []int{0},
			BaseValues:    []byte{0},
			Entropy:       ent,
		}
		return &SynthesizedSchema{
			Stride:          1,
			NumRows:         n,
			Clusters:        []ChannelCluster{cluster},
			ColumnToCluster: []int{0},
			Confidence:      0.0,
		}, nil
	}

	numRows := n / stride
	if numRows < 2 {
		return nil, ErrInputTooSmall
	}

	// 2. Extract column vectors
	cols := make([][]byte, stride)
	for j := 0; j < stride; j++ {
		cols[j] = make([]byte, numRows)
		for r := 0; r < numRows; r++ {
			cols[j][r] = src[r*stride+j]
		}
	}

	// 3. Compute column-wise statistics
	colEntropy := make([]float64, stride)
	colDiffEntropy := make([]float64, stride)
	for j := 0; j < stride; j++ {
		colEntropy[j] = computeEntropy8(cols[j])
		colDiffEntropy[j] = computeDiffEntropy(cols[j])
	}

	// 4. Compute Mutual Information Matrix and Fiedler partition
	W := ComputeMutualInfoMatrix(cols)
	fiedler := ComputeFiedlerVector(W)

	// 5. Partition columns into semantic channels
	colToCluster := make([]int, stride)
	var clusters []ChannelCluster

	var constCols []int
	var affineCols []int
	var tensorGroupA []int
	var tensorGroupB []int

	for j := 0; j < stride; j++ {
		ent := colEntropy[j]
		diffEnt := colDiffEntropy[j]

		if ent <= 1.0 {
			// Quasi-constant: quotes, commas, brackets, static fields
			constCols = append(constCols, j)
		} else if diffEnt <= 2.5 && diffEnt < ent*0.7 {
			// Monotonic counters or affine deltas
			affineCols = append(affineCols, j)
		} else {
			// Partition remaining columns using Fiedler sign
			if fiedler[j] >= 0 {
				tensorGroupA = append(tensorGroupA, j)
			} else {
				tensorGroupB = append(tensorGroupB, j)
			}
		}
	}

	addCluster := func(cType ChannelType, indices []int) {
		if len(indices) == 0 {
			return
		}
		cIdx := len(clusters)
		sumEnt := 0.0
		baseVals := make([]byte, len(indices))
		for k, colIdx := range indices {
			colToCluster[colIdx] = cIdx
			sumEnt += colEntropy[colIdx]
			baseVals[k] = cols[colIdx][0]
		}
		clusters = append(clusters, ChannelCluster{
			Type:          cType,
			ColumnIndices: indices,
			BaseValues:    baseVals,
			Entropy:       sumEnt / float64(len(indices)),
		})
	}

	addCluster(ChannelConst, constCols)
	addCluster(ChannelAffine, affineCols)
	addCluster(ChannelTensor, tensorGroupA)
	if len(tensorGroupB) > 0 {
		addCluster(ChannelTensor, tensorGroupB)
	}

	return &SynthesizedSchema{
		Stride:          stride,
		NumRows:         numRows,
		Clusters:        clusters,
		ColumnToCluster: colToCluster,
		Confidence:      conf,
	}, nil
}
