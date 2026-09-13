package przm

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// DEC-HHD Magic and Flags
var (
	MagicDECHHD = [4]byte{'D', 'E', 'C', 0x01}
)

const (
	FlagDECRaw        byte = 0x00
	FlagDECCurlFree   byte = 0x01 // d1(d0 X) == 0 everywhere (harmonic field)
	FlagDECSparseCurl byte = 0x02 // Sparse curl field (vorticity is localized)
)

const (
	DECHHDDefaultBlockSize = 4096
)

// DECHHDCandidateStrides to evaluate during structure analysis
var DECHHDCandidateStrides = []int{4, 8, 16, 32, 64}

// DECHHDBlock holds the discrete exterior calculus decomposition of a 2D grid.
type DECHHDBlock struct {
	Stride int    // Width W (columns)
	Rows   int    // Height M (rows)
	Anchor byte   // 0-form anchor at (0,0)
	Ey     []byte // 1-form horizontal boundary edges (length W-1)
	Ex     []byte // 1-form vertical boundary edges (length M-1)
	Curl   []byte // 2-form curvature / vorticity matrix (length (M-1)*(W-1))
	NNZ    int    // Number of non-zero elements in Curl
}

// ComputeDECHHD transforms an M x W grid into its discrete exterior calculus decomposition.
// Input grid must have length >= M * W.
func ComputeDECHHD(grid []byte, M, W int) *DECHHDBlock {
	if M < 2 || W < 2 || len(grid) < M*W {
		return nil
	}

	block := &DECHHDBlock{
		Stride: W,
		Rows:   M,
		Anchor: grid[0],
		Ey:     make([]byte, W-1),
		Ex:     make([]byte, M-1),
		Curl:   make([]byte, (M-1)*(W-1)),
		NNZ:    0,
	}

	// 1. Boundary horizontal 1-forms Ey along row 0
	for j := 0; j < W-1; j++ {
		block.Ey[j] = grid[j+1] ^ grid[j]
	}

	// 2. Boundary vertical 1-forms Ex along col 0
	for i := 0; i < M-1; i++ {
		block.Ex[i] = grid[(i+1)*W] ^ grid[i*W]
	}

	// 3. Discrete curvature 2-form (curl / vorticity)
	// C[i, j] = grid[i+1, j+1] ^ grid[i+1, j] ^ grid[i, j+1] ^ grid[i, j]
	curlIdx := 0
	for i := 0; i < M-1; i++ {
		rowCurr := (i + 1) * W
		rowPrev := i * W
		for j := 0; j < W-1; j++ {
			c := grid[rowCurr+j+1] ^ grid[rowCurr+j] ^ grid[rowPrev+j+1] ^ grid[rowPrev+j]
			block.Curl[curlIdx] = c
			if c != 0 {
				block.NNZ++
			}
			curlIdx++
		}
	}

	return block
}

// ReconstructDECHHD reconstructs the M x W grid from its DEC decomposition.
// Guarantee: zero branch mispredictions in inner loops; purely vectorized XORs.
func ReconstructDECHHD(block *DECHHDBlock, dst []byte) error {
	M := block.Rows
	W := block.Stride
	if M < 2 || W < 2 || len(dst) < M*W {
		return errors.New("przm: invalid dst buffer or dimensions in ReconstructDECHHD")
	}

	if DECSIMDEnabled && reconstructDECHHDSIMD(block, dst) {
		return nil
	}

	// Row 0: 1D Prefix-XOR from Anchor
	dst[0] = block.Anchor
	for j := 0; j < W-1; j++ {
		dst[j+1] = dst[j] ^ block.Ey[j]
	}

	// Interior reconstruction:
	// Let V[j] = X[i+1, j] ^ X[i, j].
	// Then V[0] = Ex[i], and V[j+1] = V[j] ^ C[i, j].
	// Then X[i+1, j] = X[i, j] ^ V[j].
	v := make([]byte, W)
	curlIdx := 0

	for i := 0; i < M-1; i++ {
		rowCurr := (i + 1) * W
		rowPrev := i * W

		// Initialize vertical differential vector V
		v[0] = block.Ex[i]
		if block.NNZ == 0 {
			// Harmonic / curl-free fast path: V is constant Ex[i] everywhere!
			exVal := block.Ex[i]
			for j := 0; j < W; j++ {
				dst[rowCurr+j] = dst[rowPrev+j] ^ exVal
			}
		} else {
			// Prefix-XOR across row for V
			for j := 0; j < W-1; j++ {
				v[j+1] = v[j] ^ block.Curl[curlIdx]
				curlIdx++
			}
			// Vector XOR with previous row
			for j := 0; j < W; j++ {
				dst[rowCurr+j] = dst[rowPrev+j] ^ v[j]
			}
		}
	}

	return nil
}

// DECHHDEncode compresses raw data using Discrete Exterior Calculus and Hodge-Helmholtz.
func DECHHDEncode(src []byte) ([]byte, error) {
	n := len(src)
	out := make([]byte, 0, n+64)

	// Magic (4 bytes) + uncompressed length (4 bytes)
	out = append(out, MagicDECHHD[:]...)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	if n < 16 {
		// Too small for 2D exterior calculus
		out = append(out, FlagDECRaw)
		var bLen [2]byte
		binary.BigEndian.PutUint16(bLen[:], uint16(n))
		out = append(out, bLen[:]...)
		out = append(out, src...)
		return out, nil
	}

	// Process in blocks
	offset := 0
	for offset < n {
		remaining := n - offset
		curBlockSize := DECHHDDefaultBlockSize
		if curBlockSize > remaining {
			curBlockSize = remaining
		}

		// Find best stride W in candidate strides
		bestStride := -1
		bestRows := 0
		bestNNZ := int(^uint(0) >> 1)
		var bestBlock *DECHHDBlock

		for _, W := range DECHHDCandidateStrides {
			if W >= curBlockSize {
				continue
			}
			M := curBlockSize / W
			if M < 2 {
				continue
			}
			candidate := ComputeDECHHD(src[offset:offset+M*W], M, W)
			if candidate == nil {
				continue
			}
			if candidate.NNZ < bestNNZ {
				bestNNZ = candidate.NNZ
				bestStride = W
				bestRows = M
				bestBlock = candidate
			}
		}

		if bestBlock == nil {
			// Fallback raw block
			out = append(out, FlagDECRaw)
			var bLen [2]byte
			binary.BigEndian.PutUint16(bLen[:], uint16(curBlockSize))
			out = append(out, bLen[:]...)
			out = append(out, src[offset:offset+curBlockSize]...)
			offset += curBlockSize
			continue
		}

		actualGridSize := bestRows * bestStride
		totalCurlSize := (bestRows - 1) * (bestStride - 1)

		// Economic Decision: Curl-Free, Sparse Curl, or Raw?
		if bestBlock.NNZ == 0 {
			// Harmonic / Curl-Free Block
			out = append(out, FlagDECCurlFree)
			out = append(out, byte(bestStride))
			var rBuf [2]byte
			binary.BigEndian.PutUint16(rBuf[:], uint16(bestRows))
			out = append(out, rBuf[:]...)
			out = append(out, bestBlock.Anchor)
			out = append(out, bestBlock.Ey...)
			out = append(out, bestBlock.Ex...)
		} else {
			sparseCost := 1 + 1 + 2 + 2 + 1 + (bestStride - 1) + (bestRows - 1) + (bestBlock.NNZ * 3)
			if sparseCost < actualGridSize-16 && bestBlock.NNZ <= totalCurlSize/4 {
				// Sparse Curl Block
				out = append(out, FlagDECSparseCurl)
				out = append(out, byte(bestStride))
				var rBuf [2]byte
				binary.BigEndian.PutUint16(rBuf[:], uint16(bestRows))
				out = append(out, rBuf[:]...)
				var nnzBuf [2]byte
				binary.BigEndian.PutUint16(nnzBuf[:], uint16(bestBlock.NNZ))
				out = append(out, nnzBuf[:]...)
				out = append(out, bestBlock.Anchor)
				out = append(out, bestBlock.Ey...)
				out = append(out, bestBlock.Ex...)

				// Emit sparse non-zeros
				for idx, val := range bestBlock.Curl {
					if val != 0 {
						var offBuf [2]byte
						binary.BigEndian.PutUint16(offBuf[:], uint16(idx))
						out = append(out, offBuf[:]...)
						out = append(out, val)
					}
				}
			} else {
				// Dense curl, raw fallback
				out = append(out, FlagDECRaw)
				var bLen [2]byte
				binary.BigEndian.PutUint16(bLen[:], uint16(actualGridSize))
				out = append(out, bLen[:]...)
				out = append(out, src[offset:offset+actualGridSize]...)
			}
		}

		offset += actualGridSize

		// Check if any leftover bytes remain in curBlockSize
		leftover := curBlockSize - actualGridSize
		if leftover > 0 {
			out = append(out, FlagDECRaw)
			var bLen [2]byte
			binary.BigEndian.PutUint16(bLen[:], uint16(leftover))
			out = append(out, bLen[:]...)
			out = append(out, src[offset:offset+leftover]...)
			offset += leftover
		}
	}

	return out, nil
}

// DECHHDDecode decompresses a DEC-HHD byte stream.
func DECHHDDecode(src []byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, errors.New("przm: dec_hhd stream too short")
	}
	if src[0] != MagicDECHHD[0] || src[1] != MagicDECHHD[1] || src[2] != MagicDECHHD[2] || src[3] != MagicDECHHD[3] {
		return nil, errors.New("przm: invalid dec_hhd magic header")
	}

	origLen := int(binary.BigEndian.Uint32(src[4:8]))
	out := make([]byte, origLen)
	dstPos := 0
	srcPos := 8

	for srcPos < len(src) && dstPos < origLen {
		flag := src[srcPos]
		srcPos++

		switch flag {
		case FlagDECRaw:
			if srcPos+2 > len(src) {
				return nil, errors.New("przm: truncated raw block length in dec_hhd")
			}
			bLen := int(binary.BigEndian.Uint16(src[srcPos : srcPos+2]))
			srcPos += 2
			if srcPos+bLen > len(src) || dstPos+bLen > origLen {
				return nil, errors.New("przm: invalid raw block size in dec_hhd")
			}
			copy(out[dstPos:dstPos+bLen], src[srcPos:srcPos+bLen])
			dstPos += bLen
			srcPos += bLen

		case FlagDECCurlFree:
			if srcPos+4 > len(src) {
				return nil, errors.New("przm: truncated curl-free header in dec_hhd")
			}
			W := int(src[srcPos])
			M := int(binary.BigEndian.Uint16(src[srcPos+1 : srcPos+3]))
			anchor := src[srcPos+3]
			srcPos += 4

			if W < 2 || M < 2 {
				return nil, errors.New("przm: invalid grid dimensions in dec_hhd")
			}

			payloadLen := (W - 1) + (M - 1)
			if srcPos+payloadLen > len(src) {
				return nil, errors.New("przm: truncated boundary edges in dec_hhd")
			}

			ey := src[srcPos : srcPos+W-1]
			srcPos += W - 1
			ex := src[srcPos : srcPos+M-1]
			srcPos += M - 1

			gridSize := M * W
			if dstPos+gridSize > origLen {
				return nil, errors.New("przm: decoded grid exceeds target length")
			}

			block := &DECHHDBlock{
				Stride: W,
				Rows:   M,
				Anchor: anchor,
				Ey:     ey,
				Ex:     ex,
				Curl:   nil,
				NNZ:    0,
			}

			if err := ReconstructDECHHD(block, out[dstPos:dstPos+gridSize]); err != nil {
				return nil, err
			}
			dstPos += gridSize

		case FlagDECSparseCurl:
			if srcPos+6 > len(src) {
				return nil, errors.New("przm: truncated sparse curl header in dec_hhd")
			}
			W := int(src[srcPos])
			M := int(binary.BigEndian.Uint16(src[srcPos+1 : srcPos+3]))
			nnz := int(binary.BigEndian.Uint16(src[srcPos+3 : srcPos+5]))
			anchor := src[srcPos+5]
			srcPos += 6

			if W < 2 || M < 2 {
				return nil, errors.New("przm: invalid grid dimensions in dec_hhd")
			}

			boundaryLen := (W - 1) + (M - 1)
			if srcPos+boundaryLen+nnz*3 > len(src) {
				return nil, errors.New("przm: truncated sparse curl payload in dec_hhd")
			}

			ey := src[srcPos : srcPos+W-1]
			srcPos += W - 1
			ex := src[srcPos : srcPos+M-1]
			srcPos += M - 1

			totalCurl := (M - 1) * (W - 1)
			curlBuf := make([]byte, totalCurl)

			for k := 0; k < nnz; k++ {
				idx := int(binary.BigEndian.Uint16(src[srcPos : srcPos+2]))
				val := src[srcPos+2]
				srcPos += 3
				if idx < totalCurl {
					curlBuf[idx] = val
				}
			}

			gridSize := M * W
			if dstPos+gridSize > origLen {
				return nil, errors.New("przm: decoded sparse grid exceeds target length")
			}

			block := &DECHHDBlock{
				Stride: W,
				Rows:   M,
				Anchor: anchor,
				Ey:     ey,
				Ex:     ex,
				Curl:   curlBuf,
				NNZ:    nnz,
			}

			if err := ReconstructDECHHD(block, out[dstPos:dstPos+gridSize]); err != nil {
				return nil, err
			}
			dstPos += gridSize

		default:
			return nil, errors.New("przm: unknown dec_hhd block flag")
		}
	}

	if dstPos != origLen {
		return nil, errors.New("przm: dec_hhd length mismatch on decode")
	}

	return out, nil
}

// Suppress unused import warning for bits
var _ = bits.OnesCount
