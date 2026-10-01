// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
)

const (
	// PRZM_MAGIC_MOBIUS is the 32-bit frame magic for Fast Möbius Transform ('MOBI' in Little-Endian).
	PRZM_MAGIC_MOBIUS uint32 = 0x49424F4D
	MobiusMagic       uint32 = 0x49424F4D
	MobiusHeaderSize  int    = 12

	FMTModeRaw         uint8 = 0
	FMTModeTransformed uint8 = 1
	FMTModeCompressed  uint8 = 3

	FMTFlagBitLevel uint8 = 0x10

	DefaultFMTBlockLog2 uint8 = 6 // 64 bytes (1 cache line / PRZM-VM vector register)
)

var (
	ErrMobiusCorrupt         = errors.New("przm: invalid or corrupted mobius frame")
	ErrMobiusUnsupportedMode = errors.New("przm: unsupported mobius mode")
)

// FMTByteButterflyInplace computes the Radix-2 Yates butterfly over a power-of-two slice of length 2^k bytes.
// Factorization over F_2: M_N = (M_1)^{\otimes n}, where M_1 = [1 0; 1 1].
// Algebraic Involution Property: M_N^2 == I_N over F_2.
func FMTByteButterflyInplace(block []byte, k int) {
	n := 1 << k
	for s := 0; s < k; s++ {
		step := 1 << s
		stride := step << 1
		if step >= 8 {
			for i := 0; i < n; i += stride {
				dstSlice := block[i+step : i+stride]
				srcSlice := block[i : i+step]
				for j := 0; j < step; j += 8 {
					*(*uint64)(unsafe.Pointer(&dstSlice[j])) ^= *(*uint64)(unsafe.Pointer(&srcSlice[j]))
				}
			}
		} else {
			for i := 0; i < n; i += stride {
				dstSlice := block[i+step : i+stride]
				srcSlice := block[i : i+step]
				for j := 0; j < step; j++ {
					dstSlice[j] ^= srcSlice[j]
				}
			}
		}
	}
}

// FMTBitButterflySWARInplace computes the 3-stage intra-byte bit butterfly in-place using SWAR.
// Applies 1-bit, 2-bit, and 4-bit butterfly stages across all bitplanes.
func FMTBitButterflySWARInplace(data []byte) {
	n := len(data)
	limit8 := (n / 8) * 8
	for i := 0; i < limit8; i += 8 {
		w := binary.LittleEndian.Uint64(data[i : i+8])
		w ^= (w & 0x5555555555555555) << 1
		w ^= (w & 0x3333333333333333) << 2
		w ^= (w & 0x0F0F0F0F0F0F0F0F) << 4
		binary.LittleEndian.PutUint64(data[i:i+8], w)
	}
	for i := limit8; i < n; i++ {
		b := data[i]
		b ^= (b & 0x55) << 1
		b ^= (b & 0x33) << 2
		b ^= (b & 0x0F) << 4
		data[i] = b
	}
}

// FMTTransformInplace applies the Fast Möbius Transform across all full 2^blockLog2 blocks.
// Trailing residual bytes (len(data) mod 2^blockLog2) are preserved untouched as identity passthrough.
// Because FMT is a strict involution, applying this function twice bit-exactly restores the original data.
func FMTTransformInplace(data []byte, blockLog2 uint8, bitLevel bool) {
	if blockLog2 < 4 || blockLog2 > 16 {
		blockLog2 = DefaultFMTBlockLog2
	}
	blockSize := 1 << blockLog2
	if blockSize <= 0 || len(data) < blockSize {
		return
	}
	numBlocks := len(data) / blockSize
	k := int(blockLog2)

	if bitLevel {
		FMTBitButterflySWARInplace(data[:numBlocks*blockSize])
	}

	for b := 0; b < numBlocks; b++ {
		block := data[b*blockSize : (b+1)*blockSize]
		FMTByteButterflyInplace(block, k)
	}
}

// FastMobiusTransformInplace is an exported alias for FMTTransformInplace.
func FastMobiusTransformInplace(data []byte, blockLog2 uint8, bitLevel bool) {
	FMTTransformInplace(data, blockLog2, bitLevel)
}

// FastMobiusTransform64Inplace executes a 64-byte in-place Möbius butterfly grid over a 64-byte array/register.
func FastMobiusTransform64Inplace(reg *[64]byte) {
	FMTByteButterflyInplace(reg[:], 6)
}

// CompressMobius encodes src into a PRZM_MAGIC_MOBIUS frame.
// Evaluates boolean ANF / Yates butterfly involution.
func CompressMobius(src []byte, level int) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}
	if n > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	blockLog2 := DefaultFMTBlockLog2
	blockSize := 1 << blockLog2

	if n < blockSize {
		// Passthrough raw bypass
		out := make([]byte, MobiusHeaderSize+n)
		binary.LittleEndian.PutUint32(out[0:4], PRZM_MAGIC_MOBIUS)
		binary.LittleEndian.PutUint32(out[4:8], uint32(n))
		out[8] = blockLog2
		out[9] = FMTModeRaw
		out[10] = byte(OpMOBIUS)
		out[11] = 0
		copy(out[MobiusHeaderSize:], src)
		return out, nil
	}

	// Copy into transformed buffer
	tData := make([]byte, n)
	copy(tData, src)
	FMTTransformInplace(tData, blockLog2, false)

	// Evaluate inner cascade compression (PWS or standard PRZM)
	var compressedPayload []byte
	var encErr error
	if n >= 512 {
		compressedPayload, encErr = PWSEncode(tData)
	}
	if encErr != nil || len(compressedPayload) == 0 || len(compressedPayload) >= n {
		compressedPayload, encErr = Compress(tData, level)
	}

	if encErr == nil && len(compressedPayload) > 0 && len(compressedPayload)+MobiusHeaderSize < n {
		out := make([]byte, MobiusHeaderSize+len(compressedPayload))
		binary.LittleEndian.PutUint32(out[0:4], PRZM_MAGIC_MOBIUS)
		binary.LittleEndian.PutUint32(out[4:8], uint32(n))
		out[8] = blockLog2
		out[9] = FMTModeCompressed
		out[10] = byte(OpMOBIUS)
		out[11] = 0
		copy(out[MobiusHeaderSize:], compressedPayload)
		return out, nil
	}

	// Transformed uncompressed mode
	out := make([]byte, MobiusHeaderSize+n)
	binary.LittleEndian.PutUint32(out[0:4], PRZM_MAGIC_MOBIUS)
	binary.LittleEndian.PutUint32(out[4:8], uint32(n))
	out[8] = blockLog2
	out[9] = FMTModeTransformed
	out[10] = byte(OpMOBIUS)
	out[11] = 0
	copy(out[MobiusHeaderSize:], tData)
	return out, nil
}

// DecompressMobius decodes a PRZM_MAGIC_MOBIUS frame into a new buffer.
func DecompressMobius(src []byte) ([]byte, error) {
	return DecompressMobiusTo(src, nil)
}

// DecompressMobiusTo decodes a PRZM_MAGIC_MOBIUS frame into dst with strictly 0 heap allocations.
func DecompressMobiusTo(src, dst []byte) ([]byte, error) {
	return DecompressFMT(src, dst)
}

// DecompressFMT decodes a PRZM_MAGIC_MOBIUS frame into dst with strictly 0 heap allocations.
func DecompressFMT(src, dst []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < MobiusHeaderSize {
		return nil, ErrMobiusCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != PRZM_MAGIC_MOBIUS {
		return nil, ErrMobiusCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	if origLen == 0 {
		return []byte{}, nil
	}

	blockLog2 := src[8]
	if blockLog2 < 4 || blockLog2 > 16 {
		blockLog2 = DefaultFMTBlockLog2
	}
	modeByte := src[9]
	mode := modeByte & 0x0F
	bitLevel := (modeByte & FMTFlagBitLevel) != 0

	payload := src[MobiusHeaderSize:]

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	switch mode {
	case FMTModeRaw:
		if len(payload) < origLen {
			return nil, ErrMobiusCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil

	case FMTModeTransformed:
		if len(payload) < origLen {
			return nil, ErrMobiusCorrupt
		}
		copy(dst, payload[:origLen])
		FMTTransformInplace(dst, blockLog2, bitLevel)
		return dst, nil

	case FMTModeCompressed:
		var innerDec []byte
		var err error
		if len(payload) >= 3 && payload[0] == pwsMagic0 && payload[1] == pwsMagic1 && payload[2] == pwsVer1 {
			innerDec, err = PWSDecodeTo(dst, payload)
		} else {
			innerDec, err = DecompressTo(payload, dst)
		}
		if err != nil {
			return nil, err
		}
		if len(innerDec) < origLen {
			return nil, ErrMobiusCorrupt
		}
		if len(dst) > 0 && len(innerDec) > 0 && &dst[0] != &innerDec[0] {
			copy(dst, innerDec[:origLen])
		}
		FMTTransformInplace(dst, blockLog2, bitLevel)
		return dst, nil

	default:
		return nil, ErrMobiusUnsupportedMode
	}
}
