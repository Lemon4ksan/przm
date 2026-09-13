// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// Galois Field F_{2^8} configuration with AES irreducible polynomial:
//
//	p(x) = x^8 + x^4 + x^3 + x + 1 (0x11B = 283)
//
// Using primitive generator g = 0x03.
const (
	gfPoly uint16 = 0x11B
	gfGen  uint8  = 0x03
)

var (
	gfExp [512]byte
	gfLog [256]byte
	gfInv [256]byte
)

func init() {
	// Initialize exponential and logarithm lookup tables
	val := 1
	for i := 0; i < 255; i++ {
		b := byte(val)
		gfExp[i] = b
		gfExp[i+255] = b
		gfLog[b] = byte(i)

		// Multiply val by generator g = 0x03: (val << 1) ^ val
		v2 := val << 1
		if v2 >= 256 {
			v2 ^= int(gfPoly)
		}
		val = v2 ^ val
	}
	gfExp[510] = gfExp[0]
	gfExp[511] = gfExp[1]
	gfLog[0] = 0 // log(0) mathematically undefined; sentinel 0

	// Initialize multiplicative inverse table:
	// For a != 0: a * a^{-1} = 1 => log(a) + log(a^{-1}) = 255
	gfInv[0] = 0
	for i := 1; i < 256; i++ {
		invExp := 255 - int(gfLog[i])
		gfInv[i] = gfExp[invExp]
	}
}

// GF256Mul performs Galois Field multiplication over F_{2^8} in O(1) time.
func GF256Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// GF256Inv returns the multiplicative inverse of a in F_{2^8} such that GF256Mul(a, GF256Inv(a)) == 1.
// If a == 0, returns 0.
func GF256Inv(a byte) byte {
	return gfInv[a]
}

// GFPIMatch represents an encoded polynomial drift match against a reference window.
type GFPIMatch struct {
	RefOffset uint32 // Offset of reference vector r in sliding buffer
	Length    uint16 // Length of vector K (e.g. 16, 32, 64)
	Degree    uint8  // Polynomial degree d: 1 (affine) or 2 (quadratic)
	C0        byte   // Constant coefficient
	C1        byte   // Linear coefficient
	C2        byte   // Quadratic coefficient (if Degree == 2)
	ErrMask   uint64 // Bitmask of non-zero residual positions (up to 64-bit)
	ErrBytes  []byte // Raw correction bytes: epsilon_j = delta_j ^ P(j)
}

// EncodedSize returns the exact byte footprint of this GFPI match header and payload.
func (m *GFPIMatch) EncodedSize() int {
	maskBytes := (int(m.Length) + 7) / 8
	coeffBytes := 2
	if m.Degree == 2 {
		coeffBytes = 3
	}
	// Format: RefOffset(4B) | Length(2B) | Degree(1B) | Coeffs(2/3B) | Mask(maskBytes) | Errs(len(ErrBytes))
	return 4 + 2 + 1 + coeffBytes + maskBytes + len(m.ErrBytes)
}

// GFPIFitAffine fits an affine polynomial P(t) = c0 ^ (c1 * t) over F_{2^8}
// to the delta vector delta = y ^ r of length K (where K <= 64).
// Returns the optimal coefficients (c0, c1), error bitmask, correction bytes, and match success.
func GFPIFitAffine(delta []byte, maxErrors int) (c0, c1 byte, errMask uint64, errBytes []byte, ok bool) {
	K := len(delta)
	if K < 4 || K > 64 {
		return 0, 0, 0, nil, false
	}

	bestC0, bestC1 := byte(0), byte(0)
	bestMask := uint64(0)
	var bestErrs []byte
	bestErrCount := K + 1

	// Candidate 1: Standard boundary control points t0 = 0, t1 = K-1
	t0, t1 := 0, K-1
	c0Cand := delta[t0]
	diff := delta[t1] ^ c0Cand
	c1Cand := GF256Mul(diff, GF256Inv(byte(t1)))

	mask, errs, errCount := evalAffineCandidate(delta, c0Cand, c1Cand, bestErrCount)
	if errCount < bestErrCount {
		bestC0, bestC1 = c0Cand, c1Cand
		bestMask = mask
		bestErrs = errs
		bestErrCount = errCount
	}

	// If boundary points had noise, evaluate candidate 2: control points t0 = 1, t1 = K-2
	if bestErrCount > 2 && K >= 6 {
		t0, t1 = 1, K-2
		diff = delta[t1] ^ delta[t0]
		tDiff := byte(t1 ^ t0) // In F_{2^8}, t1 - t0 is t1 ^ t0
		c1Alt := GF256Mul(diff, GF256Inv(tDiff))
		c0Alt := delta[t0] ^ GF256Mul(c1Alt, byte(t0))

		mask2, errs2, errCount2 := evalAffineCandidate(delta, c0Alt, c1Alt, bestErrCount)
		if errCount2 < bestErrCount {
			bestC0, bestC1 = c0Alt, c1Alt
			bestMask = mask2
			bestErrs = errs2
			bestErrCount = errCount2
		}
	}

	// Economic profit gate: encoded size must be smaller than literal cost (K bytes)
	maskBytes := (K + 7) / 8
	overhead := 2 + maskBytes + bestErrCount
	if bestErrCount > maxErrors || overhead >= K {
		return 0, 0, 0, nil, false
	}

	return bestC0, bestC1, bestMask, bestErrs, true
}

func evalAffineCandidate(delta []byte, c0, c1 byte, errCutoff int) (uint64, []byte, int) {
	var mask uint64
	var errs []byte
	errCount := 0

	for t := 0; t < len(delta); t++ {
		expected := c0 ^ GF256Mul(c1, byte(t))
		actual := delta[t]
		if actual != expected {
			mask |= (uint64(1) << t)
			errs = append(errs, actual^expected)
			errCount++
			if errCount >= errCutoff {
				return 0, nil, errCount
			}
		}
	}

	return mask, errs, errCount
}

// GFPIFitQuadratic fits a degree-2 polynomial P(t) = c0 ^ (c1 * t) ^ (c2 * t^2)
// over F_{2^8} using 3 control points: t0 = 0, t1 = K/2, t2 = K-1.
func GFPIFitQuadratic(delta []byte, maxErrors int) (c0, c1, c2 byte, errMask uint64, errBytes []byte, ok bool) {
	K := len(delta)
	if K < 8 || K > 64 {
		return 0, 0, 0, 0, nil, false
	}

	t0 := 0
	t1 := K / 2
	t2 := K - 1

	c0Cand := delta[t0]
	D1 := delta[t1] ^ c0Cand
	D2 := delta[t2] ^ c0Cand

	m1 := byte(t1)
	m2 := byte(t2)
	m1Sq := GF256Mul(m1, m1)
	m2Sq := GF256Mul(m2, m2)

	// Determinant of [m1, m1Sq; m2, m2Sq] = m1*m2Sq ^ m2*m1Sq
	det := GF256Mul(m1, m2Sq) ^ GF256Mul(m2, m1Sq)
	if det == 0 {
		return 0, 0, 0, 0, nil, false
	}
	invDet := GF256Inv(det)

	// Cramer's rule:
	// c1 = (D1 * m2Sq ^ D2 * m1Sq) * invDet
	// c2 = (m1 * D2 ^ m2 * D1) * invDet
	c1Cand := GF256Mul(GF256Mul(D1, m2Sq)^GF256Mul(D2, m1Sq), invDet)
	c2Cand := GF256Mul(GF256Mul(m1, D2)^GF256Mul(m2, D1), invDet)

	var mask uint64
	var errs []byte
	errCount := 0

	for t := 0; t < K; t++ {
		bt := byte(t)
		expected := c0Cand ^ GF256Mul(c1Cand, bt) ^ GF256Mul(c2Cand, GF256Mul(bt, bt))
		actual := delta[t]
		if actual != expected {
			mask |= (uint64(1) << t)
			errs = append(errs, actual^expected)
			errCount++
			if errCount > maxErrors {
				return 0, 0, 0, 0, nil, false
			}
		}
	}

	maskBytes := (K + 7) / 8
	overhead := 3 + maskBytes + errCount
	if overhead >= K {
		return 0, 0, 0, 0, nil, false
	}

	return c0Cand, c1Cand, c2Cand, mask, errs, true
}

// GFPIDecodeBlock reconstructs dst[0:K] from reference vector ref[0:K] and GFPI parameters:
//
//	dst[t] = ref[t] ^ P(t) ^ epsilon_t
//
// Guarantees zero-branch hot loop for the polynomial and decoupled O(E) sparse error patching.
func GFPIDecodeBlock(ref []byte, c0, c1 byte, errMask uint64, errBytes []byte, dst []byte) {
	K := len(dst)
	if K > len(ref) {
		K = len(ref)
	}

	// 1. Pure polynomial drift loop (zero branching)
	for t := 0; t < K; t++ {
		dst[t] = ref[t] ^ c0 ^ GF256Mul(c1, byte(t))
	}

	// 2. Decoupled sparse residual error patching (O(E) via bit manipulation)
	if errMask != 0 {
		m := errMask
		errIdx := 0
		for m != 0 && errIdx < len(errBytes) {
			tz := bits.TrailingZeros64(m)
			if tz < K {
				dst[tz] ^= errBytes[errIdx]
			}
			errIdx++
			m &= m - 1 // Clear lowest set bit
		}
	}
}

// GFPIDecodeQuadraticBlock reconstructs dst[0:K] from ref[0:K] using a degree-2 polynomial.
func GFPIDecodeQuadraticBlock(ref []byte, c0, c1, c2 byte, errMask uint64, errBytes []byte, dst []byte) {
	K := len(dst)
	if K > len(ref) {
		K = len(ref)
	}

	for t := 0; t < K; t++ {
		bt := byte(t)
		dst[t] = ref[t] ^ c0 ^ GF256Mul(c1, bt) ^ GF256Mul(c2, GF256Mul(bt, bt))
	}

	if errMask != 0 {
		m := errMask
		errIdx := 0
		for m != 0 && errIdx < len(errBytes) {
			tz := bits.TrailingZeros64(m)
			if tz < K {
				dst[tz] ^= errBytes[errIdx]
			}
			errIdx++
			m &= m - 1
		}
	}
}

var (
	ErrGFPICorrupted = errors.New("przm/gfpi: corrupted polynomial stream")
)

// PackGFPIMatch serializes a GFPIMatch into a compact byte payload.
func PackGFPIMatch(m *GFPIMatch) []byte {
	maskBytes := (int(m.Length) + 7) / 8
	totalSize := 4 + 2 + 1 + 2 + maskBytes + len(m.ErrBytes)
	if m.Degree == 2 {
		totalSize++
	}

	buf := make([]byte, totalSize)
	binary.LittleEndian.PutUint32(buf[0:4], m.RefOffset)
	binary.LittleEndian.PutUint16(buf[4:6], m.Length)
	buf[6] = m.Degree
	buf[7] = m.C0
	buf[8] = m.C1

	idx := 9
	if m.Degree == 2 {
		buf[idx] = m.C2
		idx++
	}

	// Pack mask (up to 8 bytes)
	for b := 0; b < maskBytes; b++ {
		buf[idx+b] = byte(m.ErrMask >> (b * 8))
	}
	idx += maskBytes

	copy(buf[idx:], m.ErrBytes)
	return buf
}

// UnpackGFPIMatch deserializes a compact byte payload back into a GFPIMatch.
func UnpackGFPIMatch(src []byte) (*GFPIMatch, int, error) {
	if len(src) < 9 {
		return nil, 0, ErrGFPICorrupted
	}

	m := &GFPIMatch{
		RefOffset: binary.LittleEndian.Uint32(src[0:4]),
		Length:    binary.LittleEndian.Uint16(src[4:6]),
		Degree:    src[6],
		C0:        src[7],
		C1:        src[8],
	}

	idx := 9
	if m.Degree == 2 {
		if len(src) < 10 {
			return nil, 0, ErrGFPICorrupted
		}
		m.C2 = src[idx]
		idx++
	}

	maskBytes := (int(m.Length) + 7) / 8
	if idx+maskBytes > len(src) {
		return nil, 0, ErrGFPICorrupted
	}

	var mask uint64
	for b := 0; b < maskBytes; b++ {
		mask |= uint64(src[idx+b]) << (b * 8)
	}
	m.ErrMask = mask
	idx += maskBytes

	// Count non-zero bits in mask
	errCount := 0
	for t := 0; t < int(m.Length); t++ {
		if (mask & (uint64(1) << t)) != 0 {
			errCount++
		}
	}

	if idx+errCount > len(src) {
		return nil, 0, fmt.Errorf("%w: missing error correction bytes (expected %d)", ErrGFPICorrupted, errCount)
	}

	m.ErrBytes = make([]byte, errCount)
	copy(m.ErrBytes, src[idx:idx+errCount])
	idx += errCount

	return m, idx, nil
}
