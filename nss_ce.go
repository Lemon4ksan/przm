// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// NSS-CE Magic and Flags (Nilpotent State-Space Cellular Endomorphisms)
var (
	MagicNSSCE = [4]byte{'N', 'S', 'S', 0x01}
)

const (
	FlagNSSRaw       byte = 0x00
	FlagNSSZeroInnov byte = 0x01 // All innovations are zero (pure deterministic trajectory)
	FlagNSSSparse    byte = 0x02 // Sparse innovations stream
	FlagNSSRLE       byte = 0x03 // RLE-compressed innovations
)

const (
	NSSCEDefaultBlockSize = 4096
)

// ApplyMatrix applies an 8x8 bit-matrix H over F_2 to a byte.
// H is encoded as 8 rows of 8 bits each (row 0 in lowest byte, row 7 in highest byte).
func ApplyMatrix(H uint64, b byte) byte {
	var res byte
	for i := 0; i < 8; i++ {
		row := byte((H >> (i * 8)) & 0xFF)
		parity := byte(bits.OnesCount8(row&b) & 1)
		res |= parity << i
	}
	return res
}

// BuildMatrixTable creates a 256-byte lookup table for ApplyMatrix(H, x).
func BuildMatrixTable(H uint64) [256]byte {
	var table [256]byte
	for x := 0; x < 256; x++ {
		table[x] = ApplyMatrix(H, byte(x))
	}
	return table
}

// FindOptimalMatrix identifies the optimal 8x8 bit-matrix H over F_2 that minimizes
// the Hamming weight of the innovation stream u_t = x_t ^ H(u_{t-1}).
func FindOptimalMatrix(data []byte) uint64 {
	n := len(data)
	if n < 4 {
		return 0
	}

	var bestH uint64

	// For each output bit b (0..7), find the optimal 8-bit mask m in [0..255]
	// that predicts data[t][b] from data[t-1].
	for b := 0; b < 8; b++ {
		targetBit := byte(1 << b)
		bestMask := byte(0)
		bestErrors := n

		for m := 0; m < 256; m++ {
			maskByte := byte(m)
			errors := 0
			for t := 1; t < n; t++ {
				predBit := byte((bits.OnesCount8(maskByte&data[t-1]) & 1) << b)
				actualBit := data[t] & targetBit
				if predBit != actualBit {
					errors++
					if errors >= bestErrors {
						break
					}
				}
			}

			if errors < bestErrors {
				bestErrors = errors
				bestMask = maskByte
			}
		}

		bestH |= uint64(bestMask) << (b * 8)
	}

	return bestH
}

// NSSCEEncode compresses raw data using Nilpotent State-Space Cellular Endomorphisms.
func NSSCEEncode(src []byte) ([]byte, error) {
	n := len(src)
	out := make([]byte, 0, n+64)

	// Magic (4 bytes) + uncompressed length (4 bytes)
	out = append(out, MagicNSSCE[:]...)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	if n < 16 {
		// Too short for state-space endomorphisms
		out = append(out, FlagNSSRaw)
		var bLen [2]byte
		binary.BigEndian.PutUint16(bLen[:], uint16(n))
		out = append(out, bLen[:]...)
		out = append(out, src...)
		return out, nil
	}

	offset := 0
	for offset < n {
		remaining := n - offset
		curBlockSize := NSSCEDefaultBlockSize
		if curBlockSize > remaining {
			curBlockSize = remaining
		}

		block := src[offset : offset+curBlockSize]

		// Find optimal transition matrix H
		H := FindOptimalMatrix(block)

		// Compute innovation stream u_t
		table := BuildMatrixTable(H)
		u := make([]byte, curBlockSize)
		u[0] = block[0]
		nnz := 0
		if u[0] != 0 {
			nnz++
		}

		for t := 1; t < curBlockSize; t++ {
			pred := table[block[t-1]]
			val := block[t] ^ pred
			u[t] = val
			if val != 0 {
				nnz++
			}
		}

		// Innovation count excluding u[0]
		tailNNZ := nnz
		if u[0] != 0 {
			tailNNZ--
		}

		// Economic decision:
		// Cost for raw: 3 + curBlockSize
		// Cost for ZeroInnov: 1 (flag) + 8 (H) + 1 (u0) + 2 (blockLen) = 12 bytes!
		// Cost for Sparse: 1 + 8 + 1 + 2 + 2 (nnz) + nnz*3
		if tailNNZ == 0 {
			// Zero Innovations: 100% deterministic trajectory!
			out = append(out, FlagNSSZeroInnov)
			var hBuf [8]byte
			binary.BigEndian.PutUint64(hBuf[:], H)
			out = append(out, hBuf[:]...)
			out = append(out, u[0])
			var bLen [2]byte
			binary.BigEndian.PutUint16(bLen[:], uint16(curBlockSize))
			out = append(out, bLen[:]...)
		} else {
			sparseCost := 1 + 8 + 1 + 2 + 2 + tailNNZ*3
			if sparseCost < curBlockSize-16 && tailNNZ <= (curBlockSize-1)/4 {
				out = append(out, FlagNSSSparse)
				var hBuf [8]byte
				binary.BigEndian.PutUint64(hBuf[:], H)
				out = append(out, hBuf[:]...)
				out = append(out, u[0])
				var bLen [2]byte
				binary.BigEndian.PutUint16(bLen[:], uint16(curBlockSize))
				out = append(out, bLen[:]...)
				var nnzBuf [2]byte
				binary.BigEndian.PutUint16(nnzBuf[:], uint16(tailNNZ))
				out = append(out, nnzBuf[:]...)

				for t := 1; t < curBlockSize; t++ {
					if u[t] != 0 {
						var posBuf [2]byte
						binary.BigEndian.PutUint16(posBuf[:], uint16(t))
						out = append(out, posBuf[:]...)
						out = append(out, u[t])
					}
				}
			} else {
				// No benefit from state-space endomorphism, emit raw block
				out = append(out, FlagNSSRaw)
				var bLen [2]byte
				binary.BigEndian.PutUint16(bLen[:], uint16(curBlockSize))
				out = append(out, bLen[:]...)
				out = append(out, block...)
			}
		}

		offset += curBlockSize
	}

	return out, nil
}

// NSSCEDecode decompresses an NSS-CE byte stream back to original data.
func NSSCEDecode(src []byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, errors.New("przm: nss_ce stream too short")
	}
	if src[0] != MagicNSSCE[0] || src[1] != MagicNSSCE[1] || src[2] != MagicNSSCE[2] || src[3] != MagicNSSCE[3] {
		return nil, errors.New("przm: invalid nss_ce magic header")
	}

	origLen := int(binary.BigEndian.Uint32(src[4:8]))
	out := make([]byte, origLen)
	dstPos := 0
	srcPos := 8

	for srcPos < len(src) && dstPos < origLen {
		flag := src[srcPos]
		srcPos++

		switch flag {
		case FlagNSSRaw:
			if srcPos+2 > len(src) {
				return nil, errors.New("przm: truncated raw block length in nss_ce")
			}
			bLen := int(binary.BigEndian.Uint16(src[srcPos : srcPos+2]))
			srcPos += 2
			if srcPos+bLen > len(src) || dstPos+bLen > origLen {
				return nil, errors.New("przm: invalid raw block size in nss_ce")
			}
			copy(out[dstPos:dstPos+bLen], src[srcPos:srcPos+bLen])
			dstPos += bLen
			srcPos += bLen

		case FlagNSSZeroInnov:
			if srcPos+11 > len(src) {
				return nil, errors.New("przm: truncated zero-innov header in nss_ce")
			}
			H := binary.BigEndian.Uint64(src[srcPos : srcPos+8])
			u0 := src[srcPos+8]
			bLen := int(binary.BigEndian.Uint16(src[srcPos+9 : srcPos+11]))
			srcPos += 11

			if dstPos+bLen > origLen {
				return nil, errors.New("przm: zero-innov block exceeds target length")
			}

			// Check SIMD acceleration
			if NSSCESIMDEnabled && reconstructZeroInnovSIMD(H, u0, out[dstPos:dstPos+bLen]) {
				dstPos += bLen
				continue
			}

			// Scalar table fallback
			table := BuildMatrixTable(H)
			target := out[dstPos : dstPos+bLen]
			target[0] = u0
			for t := 1; t < bLen; t++ {
				target[t] = table[target[t-1]]
			}
			dstPos += bLen

		case FlagNSSSparse:
			if srcPos+13 > len(src) {
				return nil, errors.New("przm: truncated sparse header in nss_ce")
			}
			H := binary.BigEndian.Uint64(src[srcPos : srcPos+8])
			u0 := src[srcPos+8]
			bLen := int(binary.BigEndian.Uint16(src[srcPos+9 : srcPos+11]))
			nnz := int(binary.BigEndian.Uint16(src[srcPos+11 : srcPos+13]))
			srcPos += 13

			if srcPos+nnz*3 > len(src) || dstPos+bLen > origLen {
				return nil, errors.New("przm: invalid sparse innovations payload in nss_ce")
			}

			// Reconstruct innovation buffer
			u := make([]byte, bLen)
			u[0] = u0
			for k := 0; k < nnz; k++ {
				pos := int(binary.BigEndian.Uint16(src[srcPos : srcPos+2]))
				val := src[srcPos+2]
				srcPos += 3
				if pos < bLen {
					u[pos] = val
				}
			}

			// Check SIMD acceleration
			if NSSCESIMDEnabled && reconstructFIRSIMD(H, u, out[dstPos:dstPos+bLen]) {
				dstPos += bLen
				continue
			}

			// Scalar table fallback
			table := BuildMatrixTable(H)
			target := out[dstPos : dstPos+bLen]
			target[0] = u[0]
			for t := 1; t < bLen; t++ {
				target[t] = u[t] ^ table[target[t-1]]
			}
			dstPos += bLen

		default:
			return nil, errors.New("przm: unknown nss_ce block flag")
		}
	}

	if dstPos != origLen {
		return nil, errors.New("przm: nss_ce length mismatch on decode")
	}

	return out, nil
}
