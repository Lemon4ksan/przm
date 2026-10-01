// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"encoding/binary"
	"math/bits"
)

// ANFProfile encapsulates algebraic metrics extracted from an ANF polynomial.
type ANFProfile struct {
	Degree          int        // Maximum degree among active monomials (0..8)
	HasDegreeDefect bool       // True if degree < 8 (specifically, a_{255} == 0)
	TopMonomialZero bool       // True if coefficient for x0*x1*...*x7 is 0 (a_{255} == 0)
	ActiveMonomials int        // Total count of non-zero ANF coefficients
	DegreeHistogram [9]int     // Count of active monomials for each degree 0..8
	MonomialDensity float64    // ActiveMonomials / 256.0
}

// FastMobiusTransform8 computes the Algebraic Normal Form (ANF) in-place for an 8-variable Boolean function
// represented as a 256-bit (32-byte) truth table.
//
// By the theorem of Möbius Inversion over GF(2), the transform is an involution:
// FMT(FMT(T)) == T.
//
// Uses a branchless 64-bit SWAR butterfly network executing all 8 stages without heap allocation.
func FastMobiusTransform8(truthTable *[32]byte) {
	w0 := binary.LittleEndian.Uint64(truthTable[0:8])
	w1 := binary.LittleEndian.Uint64(truthTable[8:16])
	w2 := binary.LittleEndian.Uint64(truthTable[16:24])
	w3 := binary.LittleEndian.Uint64(truthTable[24:32])

	// Stage 0: Step = 1 bit (pairs of adjacent bits)
	const m0 = 0x5555555555555555
	w0 ^= (w0 & m0) << 1
	w1 ^= (w1 & m0) << 1
	w2 ^= (w2 & m0) << 1
	w3 ^= (w3 & m0) << 1

	// Stage 1: Step = 2 bits (pairs of 2-bit blocks)
	const m1 = 0x3333333333333333
	w0 ^= (w0 & m1) << 2
	w1 ^= (w1 & m1) << 2
	w2 ^= (w2 & m1) << 2
	w3 ^= (w3 & m1) << 2

	// Stage 2: Step = 4 bits (pairs of 4-bit nibbles)
	const m2 = 0x0F0F0F0F0F0F0F0F
	w0 ^= (w0 & m2) << 4
	w1 ^= (w1 & m2) << 4
	w2 ^= (w2 & m2) << 4
	w3 ^= (w3 & m2) << 4

	// Stage 3: Step = 8 bits (pairs of bytes)
	const m3 = 0x00FF00FF00FF00FF
	w0 ^= (w0 & m3) << 8
	w1 ^= (w1 & m3) << 8
	w2 ^= (w2 & m3) << 8
	w3 ^= (w3 & m3) << 8

	// Stage 4: Step = 16 bits (pairs of 2-byte words)
	const m4 = 0x0000FFFF0000FFFF
	w0 ^= (w0 & m4) << 16
	w1 ^= (w1 & m4) << 16
	w2 ^= (w2 & m4) << 16
	w3 ^= (w3 & m4) << 16

	// Stage 5: Step = 32 bits (pairs of 4-byte dwords)
	const m5 = 0x00000000FFFFFFFF
	w0 ^= (w0 & m5) << 32
	w1 ^= (w1 & m5) << 32
	w2 ^= (w2 & m5) << 32
	w3 ^= (w3 & m5) << 32

	// Stage 6: Step = 64 bits (pairs of 8-byte uint64 words)
	w1 ^= w0
	w3 ^= w2

	// Stage 7: Step = 128 bits (pairs of 16-byte blocks)
	w2 ^= w0
	w3 ^= w1

	binary.LittleEndian.PutUint64(truthTable[0:8], w0)
	binary.LittleEndian.PutUint64(truthTable[8:16], w1)
	binary.LittleEndian.PutUint64(truthTable[16:24], w2)
	binary.LittleEndian.PutUint64(truthTable[24:32], w3)
}

// FastMobiusTransform computes the ANF of an 8-variable Boolean function given as a byte slice.
// If len(truthTable) < 32, it pads with zeros. Returns a newly allocated 32-byte ANF slice.
func FastMobiusTransform(truthTable []byte) []byte {
	var tt [32]byte
	if len(truthTable) >= 32 {
		copy(tt[:], truthTable[:32])
	} else if len(truthTable) > 0 {
		copy(tt[:], truthTable)
	}

	FastMobiusTransform8(&tt)
	out := make([]byte, 32)
	copy(out, tt[:])
	return out
}

// ComputeANFDegree computes the maximum algebraic degree among all active monomials in an ANF representation.
func ComputeANFDegree(anf []byte) int {
	var buf [32]byte
	if len(anf) >= 32 {
		copy(buf[:], anf[:32])
	} else if len(anf) > 0 {
		copy(buf[:], anf)
	}

	maxDeg := -1
	for u := 0; u < 256; u++ {
		bit := (buf[u/8] >> (u % 8)) & 1
		if bit == 1 {
			deg := bits.OnesCount8(uint8(u))
			if deg > maxDeg {
				maxDeg = deg
			}
		}
	}
	if maxDeg < 0 {
		return 0
	}
	return maxDeg
}

// AnalyzeBooleanFunction computes the FMT and extracts algebraic profile metrics.
func AnalyzeBooleanFunction(truthTable [32]byte) (anf [32]byte, profile ANFProfile) {
	anf = truthTable
	FastMobiusTransform8(&anf)

	maxDeg := -1
	active := 0
	var hist [9]int

	for u := 0; u < 256; u++ {
		bit := (anf[u/8] >> (u % 8)) & 1
		if bit == 1 {
			deg := bits.OnesCount8(uint8(u))
			hist[deg]++
			active++
			if deg > maxDeg {
				maxDeg = deg
			}
		}
	}

	if maxDeg < 0 {
		maxDeg = 0
	}

	topBit := (anf[31] >> 7) & 1
	topZero := (topBit == 0)

	profile = ANFProfile{
		Degree:          maxDeg,
		HasDegreeDefect: (maxDeg < 8),
		TopMonomialZero: topZero,
		ActiveMonomials: active,
		DegreeHistogram: hist,
		MonomialDensity: float64(active) / 256.0,
	}

	return anf, profile
}

// ExtractCoordinateTruthTable extracts the 256-bit truth table of the k-th coordinate function (0 <= bit < 8) of an 8x8 S-box.
func ExtractCoordinateTruthTable(sbox [256]byte, bit int) [32]byte {
	var tt [32]byte
	if bit < 0 || bit >= 8 {
		return tt
	}
	for x := 0; x < 256; x++ {
		val := sbox[x]
		coordBit := (val >> bit) & 1
		if coordBit == 1 {
			tt[x/8] |= (1 << (x % 8))
		}
	}
	return tt
}

// ComponentFunctionTruthTable extracts the truth table of the linear combination component function
// f_c(x) = c . S(x) = \bigoplus_{k=0}^7 c_k S_k(x) for non-zero c in GF(2^8).
func ComponentFunctionTruthTable(sbox [256]byte, mask byte) [32]byte {
	var tt [32]byte
	for x := 0; x < 256; x++ {
		val := sbox[x] & mask
		parity := byte(bits.OnesCount8(val) & 1)
		if parity == 1 {
			tt[x/8] |= (1 << (x % 8))
		}
	}
	return tt
}

// AnalyzeSBox evaluates all 8 coordinate Boolean functions of an 8x8 S-box.
func AnalyzeSBox(sbox [256]byte) [8]ANFProfile {
	var profiles [8]ANFProfile
	for k := 0; k < 8; k++ {
		tt := ExtractCoordinateTruthTable(sbox, k)
		_, profiles[k] = AnalyzeBooleanFunction(tt)
	}
	return profiles
}
