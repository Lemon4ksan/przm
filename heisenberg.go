// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"

	"github.com/lemon4ksan/foundation/bufkit"
)

// Frame constants for Discrete Heisenberg Group H_3(F_{2^8}) Foliation (PRZM-SPEC-0.5 Milestone 2).
const (
	PRZM_MAGIC_HEISENBERG uint32 = 0x53494548 // "HEIS" in Little-Endian
	HeisenbergMagic       uint32 = 0x53494548
	HeisenbergHeaderSize  int    = 12

	HeisenbergModeRaw        uint8 = 0 // Raw passthrough bypass
	HeisenbergModeFoliated   uint8 = 1 // Foliated stream uncompressed
	HeisenbergModeCompressed uint8 = 3 // Foliated stream cascaded with inner encoder (PWS / standard)

	HeisenbergStride3 uint8 = 3 // Triplet channels (e.g. RGB, 3D tensor)
	HeisenbergStride4 uint8 = 4 // Quad channels (e.g. RGBA, 4D tensor)
)

var (
	ErrHeisenbergCorrupt         = errors.New("przm: invalid or corrupted heisenberg frame")
	ErrHeisenbergUnsupportedMode = errors.New("przm: unsupported heisenberg mode")
	ErrHeisenbergNoBenefit       = errors.New("przm: heisenberg foliation does not benefit data")
)

// HeisenbergElement3 represents a 3x3 unipotent upper-triangular matrix H(x, y, z) in H_3(F_{2^8}):
//
//	[ 1  x  z ]
//	[ 0  1  y ]
//	[ 0  0  1 ]
//
// over Galois field F_{2^8} defined modulo the standard AES polynomial 0x11B.
type HeisenbergElement3 struct {
	X uint8
	Y uint8
	Z uint8
}

// MulH3 computes group multiplication a * b over H_3(F_{2^8}):
// (x, y, z) ∘ (x', y', z') = (x ⊕ x', y ⊕ y', z ⊕ z' ⊕ (x ⊙ y')).
func MulH3(a, b HeisenbergElement3) HeisenbergElement3 {
	return HeisenbergElement3{
		X: a.X ^ b.X,
		Y: a.Y ^ b.Y,
		Z: a.Z ^ b.Z ^ GF256Mul(a.X, b.Y),
	}
}

// InvH3 computes the exact algebraic inverse:
// H^{-1}(x, y, z) = H(x, y, z ⊕ (x ⊙ y)).
// By Theorem 39, in characteristic 2, this inverse operation is a strict algebraic involution:
// InvH3(InvH3(a)) == a.
func InvH3(a HeisenbergElement3) HeisenbergElement3 {
	return HeisenbergElement3{
		X: a.X,
		Y: a.Y,
		Z: a.Z ^ GF256Mul(a.X, a.Y),
	}
}

// HeisenbergFoliateInplace applies forward Heisenberg foliation in-place.
//
// For stride == 3: for each triplet (x, y, z), maps z' = z ⊕ (x ⊙ y).
// For stride == 4: for each quad (x, y, z, w), maps z' = z ⊕ (x ⊙ y), leaving w unchanged.
// Trailing unaligned bytes (< stride) are preserved unmodified as pass-through.
func HeisenbergFoliateInplace(data []byte, stride int) {
	n := len(data)
	if n < stride {
		return
	}

	if stride == 3 {
		// Process 4 triplets (12 bytes) per iteration for instruction-level parallelism
		limit12 := (n / 12) * 12
		for i := 0; i < limit12; i += 12 {
			data[i+2] ^= GF256Mul(data[i+0], data[i+1])
			data[i+5] ^= GF256Mul(data[i+3], data[i+4])
			data[i+8] ^= GF256Mul(data[i+6], data[i+7])
			data[i+11] ^= GF256Mul(data[i+9], data[i+10])
		}
		limit := (n / 3) * 3
		for i := limit12; i < limit; i += 3 {
			data[i+2] ^= GF256Mul(data[i], data[i+1])
		}
	} else if stride == 4 {
		// Process 4 quads (16 bytes) per iteration
		limit16 := (n / 16) * 16
		for i := 0; i < limit16; i += 16 {
			data[i+2] ^= GF256Mul(data[i+0], data[i+1])
			data[i+6] ^= GF256Mul(data[i+4], data[i+5])
			data[i+10] ^= GF256Mul(data[i+8], data[i+9])
			data[i+14] ^= GF256Mul(data[i+12], data[i+13])
		}
		limit := (n / 4) * 4
		for i := limit16; i < limit; i += 4 {
			data[i+2] ^= GF256Mul(data[i], data[i+1])
		}
	}
}

// HeisenbergUnfoliateInplace applies inverse Heisenberg unfoliation in-place.
//
// Because the coordinate map (x, y, z ⊕ (x ⊙ y)) is an exact algebraic involution over F_{2^8},
// unfoliation performs the identical algebraic step as foliation:
//
//	(z ⊕ (x ⊙ y)) ⊕ (x ⊙ y) = z.
//
// Guarantees strictly zero dynamic memory allocations and O(1) execution per block.
func HeisenbergUnfoliateInplace(data []byte, stride int) {
	HeisenbergFoliateInplace(data, stride)
}

// HeisenbergFoliate copies src into dst and applies forward foliation.
// Supports in-place execution when &dst[0] == &src[0].
func HeisenbergFoliate(dst, src []byte, stride int) {
	if len(src) == 0 || len(dst) == 0 {
		return
	}
	n := min(len(dst), len(src))
	if &dst[0] != &src[0] {
		copy(dst[:n], src[:n])
	}
	HeisenbergFoliateInplace(dst[:n], stride)
}

// HeisenbergUnfoliate copies src into dst and applies inverse unfoliation.
// Supports in-place execution when &dst[0] == &src[0].
func HeisenbergUnfoliate(dst, src []byte, stride int) {
	if len(src) == 0 || len(dst) == 0 {
		return
	}
	n := min(len(dst), len(src))
	if &dst[0] != &src[0] {
		copy(dst[:n], src[:n])
	}
	HeisenbergUnfoliateInplace(dst[:n], stride)
}

// EstimateOptimalHeisenberg evaluates empirical cross-channel non-linear covariance
// on a sample of src and determines if Heisenberg foliation significantly reduces entropy
// or produces new zero/sparse residuals along channel z.
//
// Returns optimal stride (3 or 4) and whether foliation should be applied.
func EstimateOptimalHeisenberg(src []byte) (stride int, shouldApply bool) {
	n := len(src)
	if n < 24 {
		return 3, false
	}

	sampleLen := min(n, 4096)

	bestStride := 3
	bestExact := 0
	bestGain := 0
	bestBenefited := false

	for _, s := range []int{3, 4} {
		totalBlocks := sampleLen / s
		if totalBlocks < 8 {
			continue
		}

		exactMatches := 0
		baseZerosZ := 0
		foliatedZerosZ := 0

		limit := totalBlocks * s
		for i := 0; i < limit; i += s {
			x, y, z := src[i], src[i+1], src[i+2]
			prod := GF256Mul(x, y)
			zTrans := z ^ prod

			if z == 0 {
				baseZerosZ++
			}
			if zTrans == 0 {
				foliatedZerosZ++
			}
			if prod == z && (x != 0 || y != 0 || z != 0) {
				exactMatches++
			}
		}

		netGain := foliatedZerosZ - baseZerosZ
		matchRate := float64(exactMatches) / float64(totalBlocks)
		gainRate := float64(netGain) / float64(totalBlocks)

		// Genuine benefit criterion: at least 6 exact non-trivial couplings with rate >= 0.05
		// or at least 4 net zeros created with rate >= 0.03.
		if (exactMatches >= 6 && matchRate >= 0.05) || (netGain >= 4 && gainRate >= 0.03) {
			if exactMatches > bestExact || (exactMatches == bestExact && netGain > bestGain) {
				bestStride = s
				bestExact = exactMatches
				bestGain = netGain
				bestBenefited = true
			}
		}
	}

	return bestStride, bestBenefited
}

// CompressHeisenberg encodes src using Discrete Heisenberg Group H_3(F_{2^8}) Foliation.
// Returns an encoded byte slice prefixed with the 12-byte PRZM_MAGIC_HEISENBERG header:
//
//	[0:4]   Magic (0x53494548, "HEIS", Little-Endian)
//	[4:8]   UncompressedSize (uint32 Little-Endian)
//	[8]     Stride (uint8: 3 or 4)
//	[9]     Flags / Mode (uint8: 0=Raw, 1=Foliated, 3=Compressed)
//	[10:12] Reserved (uint16 = 0)
//	[12:]   Payload
func CompressHeisenberg(src []byte, level int) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}
	if n > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	// Small buffers fallback to raw bypass
	if n < 12 {
		dst := make([]byte, HeisenbergHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
		dst[8] = uint8(HeisenbergStride3)
		dst[9] = HeisenbergModeRaw
		binary.LittleEndian.PutUint16(dst[10:12], 0)
		copy(dst[HeisenbergHeaderSize:], src)
		return dst, nil
	}

	stride, shouldApply := EstimateOptimalHeisenberg(src)
	if !shouldApply {
		// Emit raw bypass frame so roundtrip decompressor succeeds identically
		dst := make([]byte, HeisenbergHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
		dst[8] = uint8(stride)
		dst[9] = HeisenbergModeRaw
		binary.LittleEndian.PutUint16(dst[10:12], 0)
		copy(dst[HeisenbergHeaderSize:], src)
		return dst, nil
	}

	foliated := make([]byte, n)
	copy(foliated, src)
	HeisenbergFoliateInplace(foliated, stride)

	// Check if PWS AVX-Wave cascades effectively on the foliated sparse stream
	if packed, err := PWSEncode(foliated); err == nil && len(packed)+HeisenbergHeaderSize < n {
		dst := make([]byte, HeisenbergHeaderSize+len(packed))
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
		dst[8] = uint8(stride)
		dst[9] = HeisenbergModeCompressed
		binary.LittleEndian.PutUint16(dst[10:12], 0)
		copy(dst[HeisenbergHeaderSize:], packed)
		return dst, nil
	}

	// Mode 1: Foliated uncompressed stream
	dst := make([]byte, HeisenbergHeaderSize+n)
	binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_HEISENBERG)
	binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
	dst[8] = uint8(stride)
	dst[9] = HeisenbergModeFoliated
	binary.LittleEndian.PutUint16(dst[10:12], 0)
	copy(dst[HeisenbergHeaderSize:], foliated)
	return dst, nil
}

// DecompressHeisenberg decodes a PRZM_MAGIC_HEISENBERG frame into dst.
// Strictly guarantees 0 heap allocations if dst has capacity >= origLen + 64.
func DecompressHeisenberg(src, dst []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < HeisenbergHeaderSize {
		return nil, ErrHeisenbergCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != PRZM_MAGIC_HEISENBERG {
		return nil, ErrHeisenbergCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	if origLen == 0 {
		return []byte{}, nil
	}

	stride := int(src[8])
	if stride != 3 && stride != 4 {
		return nil, ErrHeisenbergCorrupt
	}

	mode := src[9]
	payload := src[HeisenbergHeaderSize:]

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	switch mode {
	case HeisenbergModeRaw:
		if len(payload) < origLen {
			return nil, ErrHeisenbergCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil

	case HeisenbergModeFoliated:
		if len(payload) < origLen {
			return nil, ErrHeisenbergCorrupt
		}
		copy(dst, payload[:origLen])
		HeisenbergUnfoliateInplace(dst, stride)
		return dst, nil

	case HeisenbergModeCompressed:
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
			return nil, ErrHeisenbergCorrupt
		}
		if len(dst) > 0 && len(innerDec) > 0 && &dst[0] != &innerDec[0] {
			copy(dst, innerDec[:origLen])
		}
		HeisenbergUnfoliateInplace(dst, stride)
		return dst, nil

	default:
		return nil, ErrHeisenbergUnsupportedMode
	}
}
