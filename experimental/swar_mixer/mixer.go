// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package swarmixer implements an 8-weight branchless logistic micro-context mixer
// operating strictly inside a single 64-bit register (0 heap, 0 scratch memory).
// It dynamically predicts optimal PRZM Context-Sheaf fibers based on the previous 2 bytes.
package swarmixer

import (
	"math/bits"
)

// Weight bounds for signed 8-bit fixed point weights.
const (
	WeightMin int8 = -128
	WeightMax int8 = 127

	// SWAR bit expansion constants.
	maskNibble uint64 = 0x0000000F0000000F
	maskPair   uint64 = 0x0003000300030003
	maskBit    uint64 = 0x0101010101010101
	signBias   uint64 = 0x8080808080808080
	lowByte16  uint64 = 0x00FF00FF00FF00FF
	midWord32  uint64 = 0x0000FFFF0000FFFF
)

// Mixer represents an 8-weight branchless micro-context mixer.
// The entire state of 8 signed 8-bit weights is packed into a single uint64 word.
// Weight i is stored at byte lane i (bits 8*i .. 8*i+7).
//
// In AVX2, this maps directly to an 8-byte lane of an AVX2 register (or 4 parallel mixers
// inside a single 256-bit YMM register using _mm256_maddubs_epi16).
// In ARM NEON, it maps to a 64-bit D-register or half of a 128-bit Q-register.
// Memory footprint: strictly 8 bytes (0 heap, 0 scratch memory).
type Mixer struct {
	weights uint64
}

// SheafSyntaxTable classifies syntax delimiters vs content semantics.
// 1 = syntax delimiter (structural), 0 = content (semantic).
var SheafSyntaxTable [256]uint8

// SheafContextTable classifies preceding byte into 4 context classes:
// 0: Syntax / structural delimiters
// 1: Lowercase ASCII letters ('a'..'z')
// 2: Uppercase ASCII letters ('A'..'Z') and ASCII digits ('0'..'9')
// 3: Other bytes (control characters, whitespace, high UTF-8 bytes)
var SheafContextTable [256]uint8

// Precomputed lookup tables for instant SWAR feature masking (2 KB L1 footprint).
var (
	featureMaskTable   [256]uint64
	featureOffsetTable [256]int32
)

func init() {
	for _, b := range []byte("{}[]()<>() :;,.\"'`=+-*/%&|^!~? \t\r\n\\") {
		SheafSyntaxTable[b] = 1
	}
	for b := range 256 {
		if SheafSyntaxTable[b] == 1 {
			SheafContextTable[b] = 0
		} else if b >= 'a' && b <= 'z' {
			SheafContextTable[b] = 1
		} else if (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') {
			SheafContextTable[b] = 2
		} else {
			SheafContextTable[b] = 3
		}

		// Precompute SWAR feature masks and baseline offsets
		f64 := ExpandBitsToBytes(uint8(b))
		featureMaskTable[b] = f64 * 0xFF
		featureOffsetTable[b] = int32(bits.OnesCount8(uint8(b))) * 128
	}
}

// NewMixer creates a new Mixer with all weights initialized to zero.
func NewMixer() Mixer {
	return Mixer{weights: 0}
}

// NewMixerWithWeights creates a Mixer with explicit initial weights.
func NewMixerWithWeights(w [8]int8) Mixer {
	var m Mixer
	m.SetWeights(w)
	return m
}

// Reset clears all weights to zero.
func (m *Mixer) Reset() {
	m.weights = 0
}

// PackedWeights returns the raw 64-bit packed weights register.
func (m *Mixer) PackedWeights() uint64 {
	return m.weights
}

// SetPackedWeights sets the raw 64-bit packed weights register.
func (m *Mixer) SetPackedWeights(pw uint64) {
	m.weights = pw
}

// Weights extracts the 8 individual signed weights from the packed register.
func (m *Mixer) Weights() [8]int8 {
	var res [8]int8
	w := m.weights
	res[0] = int8(w)
	res[1] = int8(w >> 8)
	res[2] = int8(w >> 16)
	res[3] = int8(w >> 24)
	res[4] = int8(w >> 32)
	res[5] = int8(w >> 40)
	res[6] = int8(w >> 48)
	res[7] = int8(w >> 56)
	return res
}

// SetWeights sets all 8 signed weights into the packed 64-bit register.
func (m *Mixer) SetWeights(w [8]int8) {
	m.weights = uint64(uint8(w[0])) |
		(uint64(uint8(w[1])) << 8) |
		(uint64(uint8(w[2])) << 16) |
		(uint64(uint8(w[3])) << 24) |
		(uint64(uint8(w[4])) << 32) |
		(uint64(uint8(w[5])) << 40) |
		(uint64(uint8(w[6])) << 48) |
		(uint64(uint8(w[7])) << 56)
}

// ExtractFeatures dynamically extracts an 8-bit active feature mask from the previous
// two bytes (b1 = most recent byte, b2 = second most recent byte) in order to predict
// the optimal PRZM Context-Sheaf fiber.
//
// Features:
//
//	Bit 0: Constant bias / intercept (always 1)
//	Bit 1: b1 is Syntax delimiter (SheafSyntaxTable[b1] == 1)
//	Bit 2: b1 is Lowercase ASCII ('a'..'z')
//	Bit 3: b1 is Uppercase ASCII or Digit ('A'..'Z' or '0'..'9')
//	Bit 4: Context Class fiber bit 0: c1 & 1
//	Bit 5: Context Class fiber bit 1: (c1 >> 1) & 1
//	Bit 6: Repetition / run match (b1 == b2)
//	Bit 7: Preceding byte parity / least-significant bit (b1 & 1)
func ExtractFeatures(b1, b2 uint8) uint8 {
	var f uint8 = 1 // Bit 0: Constant bias

	c1 := SheafContextTable[b1]

	f |= (SheafSyntaxTable[b1] & 1) << 1

	var isLower uint8
	if b1 >= 'a' && b1 <= 'z' {
		isLower = 1
	}
	f |= isLower << 2

	var isUpperDigit uint8
	if (b1 >= 'A' && b1 <= 'Z') || (b1 >= '0' && b1 <= '9') {
		isUpperDigit = 1
	}
	f |= isUpperDigit << 3

	// Context fiber 2-bit decomposition
	f |= (c1 & 1) << 4
	f |= ((c1 >> 1) & 1) << 5

	var isRun uint8
	if b1 == b2 {
		isRun = 1
	}
	f |= isRun << 6

	f |= (b1 & 1) << 7

	return f
}

// ExpandBitsToBytes SWAR tree: unpacks 8 feature bits in features into 8 byte lanes,
// where lane i contains 0x01 if bit i is 1, and 0x00 if bit i is 0.
// Entirely branchless, 0 memory accesses.
func ExpandBitsToBytes(features uint8) uint64 {
	x := uint64(features)
	x = (x | (x << 28)) & maskNibble
	x = (x | (x << 14)) & maskPair
	x = (x | (x << 7)) & maskBit
	return x
}

// DotProduct calculates the linear combination z = sum(w_i * x_i) for all active features
// using pure 64-bit SWAR arithmetic inside CPU integer registers (range [-1024, +1016]).
func (m *Mixer) DotProduct(features uint8) int32 {
	u := (m.weights ^ signBias) & featureMaskTable[features]

	s16 := (u & lowByte16) + ((u >> 8) & lowByte16)
	s32 := (s16 & midWord32) + ((s16 >> 16) & midWord32)
	sumU := uint32(s32) + uint32(s32>>32)

	return int32(sumU) - featureOffsetTable[features]
}

// FastSigmoid computes a branchless rational/cubic S-curve approximation mapping
// dot product z in [-1024, +1016] to a 16-bit fixed point probability P in [0, 65535]
// using a Q8.8 fixed-point model (saturation threshold S_sat = 128).
//
// Properties:
//   - FastSigmoid(0) = 32768 (exact 50% probability)
//   - FastSigmoid(z >= 128) = 65535 (saturation)
//   - FastSigmoid(z <= -128) = 0 (saturation)
//   - Monotonically non-decreasing everywhere.
//   - Branchless: 1 multiplication, 1 shift, branchless clamp.
func FastSigmoid(z int32) uint16 {
	// Clamp z to [-128, 128]
	clampedZ := min(int32(128), max(int32(-128), z))

	// Branchless absolute value
	sign := clampedZ >> 31
	absZ := (clampedZ ^ sign) - sign

	// Smooth cubic inflection: delta = (clampedZ * (256 - absZ)) * 2
	// For clampedZ = 128: 128 * 128 * 2 = 32,768
	delta := (clampedZ * (256 - absZ)) << 1

	pRaw := int32(32768) + delta

	// Branchless clamp to [0, 65535]
	p := min(int32(65535), max(int32(0), pRaw))
	return uint16(p)
}

// Predict computes the predicted probability P in [0, 65535] for the given feature mask.
// Operates branchlessly with 0 heap allocations and 0 scratch memory strictly inside CPU registers.
func (m Mixer) Predict(features uint8) uint16 {
	z := m.DotProduct(features)
	return FastSigmoid(z)
}

// Update adapts the 8 weights using the Least Mean Squares (LMS) / online gradient descent rule:
//
//	target Y = 65535 if actualBit == 1 else 0
//	error e = Y - P
//	step = clamp((e * learningRate + round) >> 16, -128, 127)
//	w_i <- clamp(w_i + step * x_i, -128, 127)
//
// Guarantees:
//   - Weights remain strictly bounded within [WeightMin, WeightMax] = [-128, 127].
//   - Zero deadzone: when e != 0 and learningRate > 0, step is guaranteed to be at least +/- 1.
//   - Zero heap allocations, zero scratch memory.
func (m *Mixer) Update(features, actualBit, learningRate uint8) {
	if learningRate == 0 || features == 0 {
		return
	}

	// Current prediction
	p := m.Predict(features)

	// Target in 16-bit fixed point: 65535 for bit 1, 0 for bit 0
	var target int32
	if actualBit != 0 {
		target = 65535
	}

	// Error in [-65535, +65535]
	err := target - int32(p)
	if err == 0 {
		return
	}

	// Scale error by learning rate: err * lr fits in int32 (max 65535 * 255 = 16,711,425)
	prod := err * int32(learningRate)

	// Symmetric rounding
	var round int32 = 32768
	if prod < 0 {
		round = -32768
	}
	step := (prod + round) >> 16

	// Deadzone elimination: ensure non-zero error produces at least +/- 1 weight step
	if step == 0 {
		if err > 0 {
			step = 1
		} else {
			step = -1
		}
	}

	// Clamp step to signed 8-bit range
	step = min(int32(WeightMax), max(int32(WeightMin), step))
	delta := int8(step)

	// Apply delta to active weights with signed saturation to [-128, 127]
	// Branchless SWAR lane-parallel update:
	m.weights = m.saddBranchless(features, delta)
}

// saddBranchless performs branchless signed 8-bit saturating addition of delta
// to all active feature lanes of m.weights.
func (m *Mixer) saddBranchless(features uint8, delta int8) uint64 {
	w := m.weights
	d := int32(delta)
	var res uint64

	// Unrolled 8 lanes: compiles directly to branchless register shifts and CMOV instructions.
	// Strictly operates inside registers; 0 memory accesses.
	for i := range 8 {
		cur := int32(int8(w >> (8 * i)))
		act := int32((features >> i) & 1)
		next := cur + d*act
		// Clamp to [-128, 127]
		clamped := min(int32(WeightMax), max(int32(WeightMin), next))
		res |= uint64(uint8(clamped)) << (8 * i)
	}

	return res
}
