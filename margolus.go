// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"

	"github.com/lemon4ksan/foundation/bufkit"
)

const (
	// PRZM_MAGIC_MARGOLUS is the 32-bit frame magic for Margolus Reversible Cellular Automata ('MARG' in Little-Endian).
	PRZM_MAGIC_MARGOLUS uint32 = 0x4752414D
	MargolusMagic       uint32 = 0x4752414D
	MargolusHeaderSize  int    = 16

	MargolusModeRaw        uint8 = 0
	MargolusModeTransform  uint8 = 1
	MargolusModeCompressed uint8 = 3

	MargolusFlagToroidal uint8 = 0x01

	MargolusRuleBBM       uint8 = 0 // Billiard Ball Model (8-bitplane collision involution)
	MargolusRuleTranspose uint8 = 1 // 2x2 Matrix transpose (b <-> c)
	MargolusRuleRot180    uint8 = 2 // 180-deg rotation (a <-> d, b <-> c)
	MargolusRuleRot90     uint8 = 3 // 90-deg CW rotation bijection (inverse CCW)
	MargolusRuleXOR       uint8 = 4 // 2D differential XOR coupling involution
	MargolusRuleFlipH     uint8 = 5 // Horizontal reflection (a <-> b, c <-> d)
	MargolusRuleFlipV     uint8 = 6 // Vertical reflection (a <-> c, b <-> d)
)

var (
	ErrMargolusCorrupt         = errors.New("przm: invalid or corrupted margolus frame")
	ErrMargolusUnsupportedMode = errors.New("przm: unsupported margolus mode")
)

// ApplyMargolusBlockRule applies the local block bijection on 2x2 cells (a, b; c, d).
func ApplyMargolusBlockRule(a, b, c, d uint8, ruleID uint8, inverse bool) (uint8, uint8, uint8, uint8) {
	switch ruleID {
	case MargolusRuleBBM:
		// 11-instruction branchless SWAR across all 8 bitplanes
		diag := a & d & (^b) & (^c)
		antidiag := b & c & (^a) & (^d)
		mask := diag | antidiag
		return a ^ mask, b ^ mask, c ^ mask, d ^ mask

	case MargolusRuleTranspose:
		return a, c, b, d

	case MargolusRuleRot180:
		return d, c, b, a

	case MargolusRuleRot90:
		if !inverse {
			return c, a, d, b // 90 deg CW
		}
		return b, d, a, c // 90 deg CCW

	case MargolusRuleXOR:
		return a, b ^ a, c ^ a, d ^ a

	case MargolusRuleFlipH:
		return b, a, d, c

	case MargolusRuleFlipV:
		return c, d, a, b

	default:
		return a, b, c, d
	}
}

// MargolusStepInplace applies 1 Margolus step on an H x W lattice in-place.
// Phase 0: even partition blocks at (2i, 2j).
// Phase 1: odd partition blocks at (2i+1, 2j+1) with toroidal wrapping.
func MargolusStepInplace(data []byte, H, W, phase int, ruleID uint8, inverse bool) {
	if H < 2 || W < 2 {
		return
	}
	wMask := W - 1
	isPow2 := (W & wMask) == 0

	halfH := H / 2
	halfW := W / 2

	if phase == 0 {
		// Even step: disjoint blocks at (2i, 2j)
		for i := 0; i < halfH; i++ {
			r0 := 2 * i * W
			r1 := r0 + W
			for j := 0; j < halfW; j++ {
				c0 := 2 * j
				c1 := c0 + 1

				i00 := r0 + c0
				i01 := r0 + c1
				i10 := r1 + c0
				i11 := r1 + c1

				a, b, c, d := ApplyMargolusBlockRule(data[i00], data[i01], data[i10], data[i11], ruleID, inverse)
				data[i00] = a
				data[i01] = b
				data[i10] = c
				data[i11] = d
			}
		}
	} else {
		// Odd step: disjoint blocks at (2i+1, 2j+1) with toroidal wrapping
		for i := 0; i < halfH; i++ {
			r0Idx := 2*i + 1
			r1Idx := r0Idx + 1
			if r1Idx >= H {
				r1Idx = 0
			}
			r0 := r0Idx * W
			r1 := r1Idx * W

			for j := 0; j < halfW; j++ {
				c0 := 2*j + 1
				c1 := c0 + 1
				if isPow2 {
					c1 &= wMask
				} else if c1 >= W {
					c1 = 0
				}

				i00 := r0 + c0
				i01 := r0 + c1
				i10 := r1 + c0
				i11 := r1 + c1

				a, b, c, d := ApplyMargolusBlockRule(data[i00], data[i01], data[i10], data[i11], ruleID, inverse)
				data[i00] = a
				data[i01] = b
				data[i10] = c
				data[i11] = d
			}
		}
	}
}

// MargolusEvolveInplace simulates K steps forward in-place on data.
// Trailing residual bytes (len(data) mod 2W) are left untouched.
func MargolusEvolveInplace(data []byte, width, steps int, ruleID uint8) {
	n := len(data)
	if width < 2 || n < 2*width || steps <= 0 {
		return
	}
	H := 2 * (n / (2 * width))
	for t := 0; t < steps; t++ {
		phase := t & 1
		MargolusStepInplace(data, H, width, phase, ruleID, false)
	}
}

// MargolusReverseInplace reverses K steps in reverse time-order in-place on data.
// Inversion executes steps from t = steps-1 down to 0 with the inverse rule.
// Trailing residual bytes (len(data) mod 2W) are left untouched.
func MargolusReverseInplace(data []byte, width, steps int, ruleID uint8) {
	n := len(data)
	if width < 2 || n < 2*width || steps <= 0 {
		return
	}
	H := 2 * (n / (2 * width))
	for t := steps - 1; t >= 0; t-- {
		phase := t & 1
		MargolusStepInplace(data, H, width, phase, ruleID, true)
	}
}

// MargolusForwardInplace is an alias for MargolusEvolveInplace.
func MargolusForwardInplace(data []byte, width, steps int, ruleID uint8) {
	MargolusEvolveInplace(data, width, steps, ruleID)
}

// MargolusInverseInplace is an alias for MargolusReverseInplace.
func MargolusInverseInplace(data []byte, width, steps int, ruleID uint8) {
	MargolusReverseInplace(data, width, steps, ruleID)
}

// selectMargolusWidth picks an appropriate power-of-2 width for buffer length n.
func selectMargolusWidth(n int) int {
	if n < 64 {
		return 8
	}
	if n < 256 {
		return 16
	}
	if n < 1024 {
		return 32
	}
	return 64
}

// CompressMargolus encodes src into a PRZM_MAGIC_MARGOLUS frame.
func CompressMargolus(src []byte, level int) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}
	if n > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	width := selectMargolusWidth(n)
	if n < 2*width {
		// Passthrough raw bypass
		out := make([]byte, MargolusHeaderSize+n)
		binary.LittleEndian.PutUint32(out[0:4], PRZM_MAGIC_MARGOLUS)
		binary.LittleEndian.PutUint32(out[4:8], uint32(n))
		binary.LittleEndian.PutUint16(out[8:10], uint16(width))
		out[10] = 0 // 0 steps
		out[11] = MargolusRuleBBM
		out[12] = MargolusModeRaw
		out[13] = MargolusFlagToroidal
		binary.LittleEndian.PutUint16(out[14:16], 0)
		copy(out[MargolusHeaderSize:], src)
		return out, nil
	}

	steps := 2
	ruleID := MargolusRuleBBM

	tData := make([]byte, n)
	copy(tData, src)
	MargolusEvolveInplace(tData, width, steps, ruleID)

	var compressedPayload []byte
	var encErr error
	if n >= 512 {
		compressedPayload, encErr = PWSEncode(tData)
	}
	if encErr != nil || len(compressedPayload) == 0 || len(compressedPayload) >= n {
		compressedPayload, encErr = Compress(tData, level)
	}

	if encErr == nil && len(compressedPayload) > 0 && len(compressedPayload)+MargolusHeaderSize < n {
		out := make([]byte, MargolusHeaderSize+len(compressedPayload))
		binary.LittleEndian.PutUint32(out[0:4], PRZM_MAGIC_MARGOLUS)
		binary.LittleEndian.PutUint32(out[4:8], uint32(n))
		binary.LittleEndian.PutUint16(out[8:10], uint16(width))
		out[10] = uint8(steps)
		out[11] = ruleID
		out[12] = MargolusModeCompressed
		out[13] = MargolusFlagToroidal
		binary.LittleEndian.PutUint16(out[14:16], 0)
		copy(out[MargolusHeaderSize:], compressedPayload)
		return out, nil
	}

	// Transformed mode
	out := make([]byte, MargolusHeaderSize+n)
	binary.LittleEndian.PutUint32(out[0:4], PRZM_MAGIC_MARGOLUS)
	binary.LittleEndian.PutUint32(out[4:8], uint32(n))
	binary.LittleEndian.PutUint16(out[8:10], uint16(width))
	out[10] = uint8(steps)
	out[11] = ruleID
	out[12] = MargolusModeTransform
	out[13] = MargolusFlagToroidal
	binary.LittleEndian.PutUint16(out[14:16], 0)
	copy(out[MargolusHeaderSize:], tData)
	return out, nil
}

// DecompressMargolus decodes a PRZM_MAGIC_MARGOLUS frame into a new buffer.
func DecompressMargolus(src []byte) ([]byte, error) {
	return DecompressMargolusTo(src, nil)
}

// DecompressMargolusTo decodes a PRZM_MAGIC_MARGOLUS frame into dst with strictly 0 heap allocations.
func DecompressMargolusTo(src, dst []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < MargolusHeaderSize {
		return nil, ErrMargolusCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != PRZM_MAGIC_MARGOLUS {
		return nil, ErrMargolusCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	if origLen == 0 {
		return []byte{}, nil
	}

	width := int(binary.LittleEndian.Uint16(src[8:10]))
	steps := int(src[10])
	ruleID := src[11]
	mode := src[12]

	payload := src[MargolusHeaderSize:]

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	if mode == MargolusModeRaw {
		if len(payload) < origLen {
			return nil, ErrMargolusCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil
	} else if mode == MargolusModeTransform {
		if len(payload) < origLen {
			return nil, ErrMargolusCorrupt
		}
		copy(dst, payload[:origLen])
		MargolusReverseInplace(dst, width, steps, ruleID)
		return dst, nil
	} else if mode == MargolusModeCompressed {
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
			return nil, ErrMargolusCorrupt
		}
		if len(dst) > 0 && len(innerDec) > 0 && &dst[0] != &innerDec[0] {
			copy(dst, innerDec[:origLen])
		}
		MargolusReverseInplace(dst, width, steps, ruleID)
		return dst, nil
	}

	return nil, ErrMargolusUnsupportedMode
}
