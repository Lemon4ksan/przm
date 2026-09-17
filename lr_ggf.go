// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// LR-GGF (Low-Rank Galois-Grassmannian Factorization) constants
const (
	lrggfMagic0 uint8 = 0x4C // 'L'
	lrggfMagic1 uint8 = 0x52 // 'R'
	lrggfVer1   uint8 = 0x01

	DefaultBlockDim = 64 // 64x64 = 4096 bytes per tensor block
	MaxRankLimit    = 16 // Maximum rank to search per block
)

var (
	ErrLRGGFCorrupted    = errors.New("przm/lrggf: corrupted stream")
	ErrLRGGFSizeMismatch = errors.New("przm/lrggf: decompressed size mismatch")
)

// SparseError represents a non-zero residual correction entry E[row, col] = val.
type SparseError struct {
	Row uint8
	Col uint8
	Val byte
}

// LRGGFBlock holds the rank-R factor matrices U, V and sparse residuals E for an M x N block.
type LRGGFBlock struct {
	M      int           // Rows (<= 256, typically 64)
	N      int           // Cols (<= 256, typically 64)
	Rank   int           // Algebraic rank over F_2^8 (0..16)
	U      []byte        // M x Rank left column basis
	V      []byte        // N x Rank right row basis
	Errors []SparseError // Sparse residual entries
	Raw    []byte        // Fallback raw block if rank factorization is unprofitable
}

// FactorizeBlock decomposes an M x N block X in F_2^8 into (U * V^T) ^ E.
func FactorizeBlock(src []byte, M, N, maxRank int) LRGGFBlock {
	blockSize := M * N
	if len(src) < blockSize {
		padded := make([]byte, blockSize)
		copy(padded, src)
		src = padded
	}

	// Working copy of residual matrix R = X
	R := make([]byte, blockSize)
	copy(R, src[:blockSize])

	// Count initial non-zero elements
	curNNZ := 0
	for _, b := range R {
		if b != 0 {
			curNNZ++
		}
	}

	block := LRGGFBlock{
		M: M,
		N: N,
	}

	var U_basis []byte // Rank * M
	var V_basis []byte // Rank * N

	for r := 0; r < maxRank; r++ {
		if curNNZ == 0 {
			break
		}

		// Find optimal pivot (bestR, bestC) maximizing rowNNZ * colNNZ
		var rowNNZ [256]int
		var colNNZ [256]int
		for i := 0; i < M; i++ {
			rowOff := i * N
			for j := 0; j < N; j++ {
				if R[rowOff+j] != 0 {
					rowNNZ[i]++
					colNNZ[j]++
				}
			}
		}

		bestR, bestC := -1, -1
		bestScore := 0
		for i := 0; i < M; i++ {
			rowOff := i * N
			for j := 0; j < N; j++ {
				if R[rowOff+j] != 0 {
					score := rowNNZ[i] * colNNZ[j]
					if score > bestScore {
						bestScore = score
						bestR = i
						bestC = j
					}
				}
			}
		}

		if bestR < 0 {
			break
		}

		bestAlpha := R[bestR*N+bestC]
		invAlpha := gfInv[bestAlpha]

		u_vec := make([]byte, M)
		v_vec := make([]byte, N)
		for i := 0; i < M; i++ {
			u_vec[i] = R[i*N+bestC]
		}
		rowOff := bestR * N
		for j := 0; j < N; j++ {
			v_vec[j] = GF256Mul(R[rowOff+j], invAlpha)
		}

		// Calculate net elimination gain of this rank-1 outer product
		netGain := 0
		for i := 0; i < M; i++ {
			u_val := u_vec[i]
			if u_val == 0 {
				continue
			}
			rOff := i * N
			for j := 0; j < N; j++ {
				pred := GF256Mul(u_val, v_vec[j])
				orig := R[rOff+j]
				if orig == pred {
					netGain++
				} else if orig == 0 && pred != 0 {
					netGain--
				}
			}
		}

		// Economic profitability check:
		// Storing one rank costs M + N bytes (128 bytes).
		// Must eliminate at least (M+N)/2 errors to be worthwhile.
		minGain := (M + N) / 2
		if netGain <= minGain {
			break
		}

		// Subtract rank-1 matrix from residual R: R = R ^ (u * v^T)
		newNNZ := 0
		for i := 0; i < M; i++ {
			u_val := u_vec[i]
			rOff := i * N
			if u_val == 0 {
				for j := 0; j < N; j++ {
					if R[rOff+j] != 0 {
						newNNZ++
					}
				}
				continue
			}
			for j := 0; j < N; j++ {
				pred := GF256Mul(u_val, v_vec[j])
				newVal := R[rOff+j] ^ pred
				R[rOff+j] = newVal
				if newVal != 0 {
					newNNZ++
				}
			}
		}

		U_basis = append(U_basis, u_vec...)
		V_basis = append(V_basis, v_vec...)
		block.Rank++
		curNNZ = newNNZ
	}

	factorizedCost := 4 + block.Rank*(M+N) + curNNZ*3
	if block.Rank == 0 || factorizedCost >= blockSize {
		block.Rank = 0
		block.U = nil
		block.V = nil
		block.Errors = nil
		block.Raw = make([]byte, blockSize)
		copy(block.Raw, src[:blockSize])
		return block
	}

	block.U = U_basis
	block.V = V_basis

	if curNNZ > 0 {
		block.Errors = make([]SparseError, 0, curNNZ)
		for i := 0; i < M; i++ {
			rOff := i * N
			for j := 0; j < N; j++ {
				val := R[rOff+j]
				if val != 0 {
					block.Errors = append(block.Errors, SparseError{
						Row: uint8(i),
						Col: uint8(j),
						Val: val,
					})
				}
			}
		}
	}

	return block
}

// ReconstructBlock reconstructs the M x N block from its factorized components.
func ReconstructBlock(b *LRGGFBlock, dst []byte) {
	if ArchSIMDEnabled && reconstructBlockSIMD(b, dst) {
		return
	}

	blockSize := b.M * b.N
	if len(dst) < blockSize {
		return
	}

	if b.Rank == 0 {
		if len(b.Raw) >= blockSize {
			copy(dst[:blockSize], b.Raw[:blockSize])
		} else {
			clear(dst[:blockSize])
		}
		return
	}

	clear(dst[:blockSize])

	M, N, R := b.M, b.N, b.Rank

	// SIMD-parallel tensor matrix product: dst = U * V^T over F_2^8
	for r := 0; r < R; r++ {
		uOffset := r * M
		vOffset := r * N

		for i := 0; i < M; i++ {
			u_val := b.U[uOffset+i]
			if u_val == 0 {
				continue
			}

			rowOff := i * N
			for j := 0; j < N; j++ {
				dst[rowOff+j] ^= GF256Mul(u_val, b.V[vOffset+j])
			}
		}
	}

	// Apply sparse residual error corrections: dst ^= E
	for _, e := range b.Errors {
		idx := int(e.Row)*N + int(e.Col)
		if idx < blockSize {
			dst[idx] ^= e.Val
		}
	}
}

// LRGGFEncode compresses a byte stream by tiling it into M x N Galois-Grassmannian blocks.
func LRGGFEncode(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	const M = DefaultBlockDim
	const N = DefaultBlockDim
	const blockSize = M * N // 4096

	numBlocks := (n + blockSize - 1) / blockSize
	out := make([]byte, 0, n)

	// Magic signature (4B): 'L', 'R', 0x01, 0x00
	out = append(out, lrggfMagic0, lrggfMagic1, lrggfVer1, 0x00)

	var szBuf [8]byte
	binary.LittleEndian.PutUint32(szBuf[0:4], uint32(n))
	binary.LittleEndian.PutUint32(szBuf[4:8], uint32(numBlocks))
	out = append(out, szBuf[:]...)

	for bIdx := 0; bIdx < numBlocks; bIdx++ {
		start := bIdx * blockSize
		end := start + blockSize
		if end > n {
			end = n
		}

		b := FactorizeBlock(src[start:end], M, N, MaxRankLimit)

		flags := uint8(0)
		if b.Rank == 0 {
			flags |= 0x80
		}
		out = append(out, uint8(b.Rank), uint8(M), uint8(N), flags)

		if b.Rank == 0 {
			out = append(out, b.Raw...)
		} else {
			out = append(out, b.U...)
			out = append(out, b.V...)

			var errLenBuf [2]byte
			binary.LittleEndian.PutUint16(errLenBuf[:], uint16(len(b.Errors)))
			out = append(out, errLenBuf[:]...)

			for _, e := range b.Errors {
				out = append(out, e.Row, e.Col, e.Val)
			}
		}
	}

	return out, nil
}

// LRGGFDecode reconstructs uncompressed data from an LRGGF-encoded stream.
func LRGGFDecode(src []byte) ([]byte, error) {
	if len(src) < 12 {
		return nil, ErrLRGGFCorrupted
	}

	if src[0] != lrggfMagic0 || src[1] != lrggfMagic1 || src[2] != lrggfVer1 {
		return nil, fmt.Errorf("%w: invalid LRGGF magic signature", ErrLRGGFCorrupted)
	}

	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	numBlocks := int(binary.LittleEndian.Uint32(src[8:12]))

	dst := make([]byte, numBlocks*DefaultBlockDim*DefaultBlockDim)
	ptr := 12

	const blockSize = DefaultBlockDim * DefaultBlockDim

	for bIdx := 0; bIdx < numBlocks; bIdx++ {
		if ptr+4 > len(src) {
			return nil, ErrLRGGFCorrupted
		}

		rank := int(src[ptr])
		M := int(src[ptr+1])
		N := int(src[ptr+2])
		flags := src[ptr+3]
		ptr += 4

		b := LRGGFBlock{
			M:    M,
			N:    N,
			Rank: rank,
		}

		if flags&0x80 != 0 || rank == 0 {
			if ptr+blockSize > len(src) {
				return nil, ErrLRGGFCorrupted
			}
			b.Raw = src[ptr : ptr+blockSize]
			ptr += blockSize
		} else {
			uSize := rank * M
			vSize := rank * N
			if ptr+uSize+vSize+2 > len(src) {
				return nil, ErrLRGGFCorrupted
			}

			b.U = src[ptr : ptr+uSize]
			ptr += uSize
			b.V = src[ptr : ptr+vSize]
			ptr += vSize

			errCount := int(binary.LittleEndian.Uint16(src[ptr : ptr+2]))
			ptr += 2

			if ptr+errCount*3 > len(src) {
				return nil, ErrLRGGFCorrupted
			}

			b.Errors = make([]SparseError, errCount)
			for e := 0; e < errCount; e++ {
				b.Errors[e] = SparseError{
					Row: src[ptr],
					Col: src[ptr+1],
					Val: src[ptr+2],
				}
				ptr += 3
			}
		}

		dstOffset := bIdx * blockSize
		ReconstructBlock(&b, dst[dstOffset:dstOffset+blockSize])
	}

	if origSize > len(dst) {
		return nil, ErrLRGGFSizeMismatch
	}

	return dst[:origSize], nil
}

// Global LRGGF Macro-Deduplication
const (
	globalLRGGFMagic0 uint8 = 0x47 // 'G'
	globalLRGGFMagic1 uint8 = 0x4C // 'L'
	globalLRGGFVer1   uint8 = 0x01
)

var (
	ErrGlobalLRGGFDictSize  = errors.New("przm/global_lrggf: dictionary must be 32KiB")
)

// GlobalLRGGFEncode compresses src using a 32 KiB dictionary (16 KiB U, 16 KiB V).
func GlobalLRGGFEncode(src, dict []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}
	if len(dict) != 32768 {
		return nil, ErrGlobalLRGGFDictSize
	}

	U := dict[:16384]
	V := dict[16384:]

	out := make([]byte, 0, n)
	out = append(out, globalLRGGFMagic0, globalLRGGFMagic1, globalLRGGFVer1, 0x00)

	var szBuf [4]byte
	binary.LittleEndian.PutUint32(szBuf[:], uint32(n))
	out = append(out, szBuf[:]...)

	const blockSize = 64
	numBlocks := (n + blockSize - 1) / blockSize

	for bIdx := 0; bIdx < numBlocks; bIdx++ {
		start := bIdx * blockSize
		end := start + blockSize
		if end > n {
			end = n
		}
		
		block := src[start:end]
		blockLen := len(block)
		
		bestI, bestJ := 0, 0
		bestErrors := blockLen + 1
		
		var bestErrIndices []uint8
		var bestErrVals []byte

		// Combinatorial Greedy Search based on Hamming weight
		for i := 0; i < 256; i++ {
			uVec := U[i*blockSize : i*blockSize+blockSize]
			for j := 0; j < 256; j++ {
				vVec := V[j*blockSize : j*blockSize+blockSize]
				
				errCount := 0
				for k := 0; k < blockLen; k++ {
					if block[k] != (uVec[k] ^ vVec[k]) {
						errCount++
					}
				}
				
				if errCount < bestErrors {
					bestErrors = errCount
					bestI = i
					bestJ = j
				}
			}
		}

		// Recompute best errors
		if bestErrors <= blockLen {
			uVec := U[bestI*blockSize : bestI*blockSize+blockSize]
			vVec := V[bestJ*blockSize : bestJ*blockSize+blockSize]
			for k := 0; k < blockLen; k++ {
				pred := uVec[k] ^ vVec[k]
				if block[k] != pred {
					bestErrIndices = append(bestErrIndices, uint8(k))
					bestErrVals = append(bestErrVals, block[k] ^ pred)
				}
			}
		}

		costFactorized := 3 + 2*bestErrors
		costRaw := 1 + blockLen

		if costFactorized < costRaw {
			out = append(out, 0x01)
			out = append(out, uint8(bestI), uint8(bestJ))
			out = append(out, uint8(bestErrors))
			for k := 0; k < bestErrors; k++ {
				out = append(out, bestErrIndices[k], bestErrVals[k])
			}
		} else {
			out = append(out, 0x00)
			out = append(out, uint8(blockLen))
			out = append(out, block...)
		}
	}
	return out, nil
}

func GlobalLRGGFDecode(src, dict []byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, ErrLRGGFCorrupted
	}
	if len(dict) != 32768 {
		return nil, ErrGlobalLRGGFDictSize
	}
	if src[0] != globalLRGGFMagic0 || src[1] != globalLRGGFMagic1 || src[2] != globalLRGGFVer1 {
		return nil, fmt.Errorf("%w: invalid Global LRGGF magic signature", ErrLRGGFCorrupted)
	}

	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	dst := make([]byte, origSize)
	ptr := 8
	
	U := dict[:16384]
	V := dict[16384:]

	dstOff := 0
	const blockSize = 64

	for dstOff < origSize {
		if ptr >= len(src) {
			return nil, ErrLRGGFCorrupted
		}
		flag := src[ptr]
		ptr++

		if flag == 0x00 {
			if ptr >= len(src) {
				return nil, ErrLRGGFCorrupted
			}
			blockLen := int(src[ptr])
			ptr++
			if ptr+blockLen > len(src) || dstOff+blockLen > origSize {
				return nil, ErrLRGGFCorrupted
			}
			copy(dst[dstOff:dstOff+blockLen], src[ptr:ptr+blockLen])
			ptr += blockLen
			dstOff += blockLen
		} else if flag == 0x01 {
			if ptr+3 > len(src) {
				return nil, ErrLRGGFCorrupted
			}
			uIdx := int(src[ptr])
			vIdx := int(src[ptr+1])
			errCount := int(src[ptr+2])
			ptr += 3

			if ptr+2*errCount > len(src) {
				return nil, ErrLRGGFCorrupted
			}

			blockLen := blockSize
			if dstOff+blockLen > origSize {
				blockLen = origSize - dstOff
			}

			uVec := U[uIdx*blockSize : uIdx*blockSize+blockSize]
			vVec := V[vIdx*blockSize : vIdx*blockSize+blockSize]

			for k := 0; k < blockLen; k++ {
				dst[dstOff+k] = uVec[k] ^ vVec[k]
			}

			for e := 0; e < errCount; e++ {
				idx := int(src[ptr])
				val := src[ptr+1]
				ptr += 2
				if idx < blockLen {
					dst[dstOff+idx] ^= val
				}
			}
			dstOff += blockLen
		} else {
			return nil, ErrLRGGFCorrupted
		}
	}

	return dst, nil
}

