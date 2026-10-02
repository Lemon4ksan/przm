// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"

	"github.com/lemon4ksan/foundation/bufkit"
)

// Frame constants for 4D IsoQuant Quaternionic Collimation (PRZM-SPEC-0.5).
const (
	PRZM_MAGIC_ISOQUANT uint32 = 0x514F5349 // "ISOQ" in Little-Endian
	IsoQuantMagic      uint32 = 0x514F5349
	IsoQuantHeaderSize int    = 20

	IsoQuantModeRaw        uint8 = 0 // Raw passthrough bypass
	IsoQuantModeCollimated uint8 = 1 // Collimated 4D stream
	IsoQuantModeCompressed uint8 = 3 // Collimated 4D stream + cascaded PWS AVX-wave
)

var (
	ErrIsoQuantCorrupt         = errors.New("przm: invalid or corrupted isoquant frame")
	ErrIsoQuantUnsupportedMode = errors.New("przm: unsupported isoquant mode")
)

// Quaternion4 represents a 4-dimensional discrete Hamilton quaternion in H(F_{2^8}).
// Basis elements: e0 = 1, e1 = i, e2 = j, e3 = k.
type Quaternion4 [4]uint8

// IdentityQuaternion4 returns the multiplicative identity element (1, 0, 0, 0).
func IdentityQuaternion4() Quaternion4 {
	return Quaternion4{1, 0, 0, 0}
}

// Mul computes the Hamilton product p * q over H(F_{2^8}).
// In characteristic 2, coordinates follow the Walsh-Hadamard index convolution:
// r_k = sum_{i ^ j = k} (p_i (x) q_j) over F_{2^8}.
func (p Quaternion4) Mul(q Quaternion4) Quaternion4 {
	return Quaternion4{
		GF256Mul(p[0], q[0]) ^ GF256Mul(p[1], q[1]) ^ GF256Mul(p[2], q[2]) ^ GF256Mul(p[3], q[3]),
		GF256Mul(p[0], q[1]) ^ GF256Mul(p[1], q[0]) ^ GF256Mul(p[2], q[3]) ^ GF256Mul(p[3], q[2]),
		GF256Mul(p[0], q[2]) ^ GF256Mul(p[2], q[0]) ^ GF256Mul(p[1], q[3]) ^ GF256Mul(p[3], q[1]),
		GF256Mul(p[0], q[3]) ^ GF256Mul(p[3], q[0]) ^ GF256Mul(p[1], q[2]) ^ GF256Mul(p[2], q[1]),
	}
}

// Mul package function evaluates Hamilton product a * b over H(F_{2^8}).
func Mul(a, b Quaternion4) Quaternion4 {
	return a.Mul(b)
}

// Inv computes the multiplicative inverse q^{-1} such that q * q^{-1} == (1, 0, 0, 0).
// In H(F_{2^8}), q * q = ((q0 ^ q1 ^ q2 ^ q3)^2, 0, 0, 0).
// q is invertible if and only if s = q0 ^ q1 ^ q2 ^ q3 != 0.
func (q Quaternion4) Inv() (Quaternion4, bool) {
	s := q[0] ^ q[1] ^ q[2] ^ q[3]
	if s == 0 {
		return Quaternion4{}, false
	}
	sSq := GF256Mul(s, s)
	inv := GF256Inv(sSq)
	return Quaternion4{
		GF256Mul(q[0], inv),
		GF256Mul(q[1], inv),
		GF256Mul(q[2], inv),
		GF256Mul(q[3], inv),
	}, true
}

// Inv package function evaluates multiplicative inverse q^{-1}.
func Inv(q Quaternion4) Quaternion4 {
	inv, _ := q.Inv()
	return inv
}

// IsIdentity returns true if q == (1, 0, 0, 0).
func (q Quaternion4) IsIdentity() bool {
	return q[0] == 1 && q[1] == 0 && q[2] == 0 && q[3] == 0
}

// IsoQuantCollimation4D applies forward 4D isoclinic rotation:
// v' = qL * v * qR
// across all 4-byte quads in src, writing the rotated stream into dst.
// Supports dst == src (in-place) or distinct buffers.
func IsoQuantCollimation4D(dst, src []byte, qL, qR Quaternion4) {
	qEff := qL.Mul(qR)
	limit := (len(src) / 4) * 4
	for i := 0; i < limit; i += 4 {
		v0, v1, v2, v3 := src[i], src[i+1], src[i+2], src[i+3]
		dst[i]   = GF256Mul(qEff[0], v0) ^ GF256Mul(qEff[1], v1) ^ GF256Mul(qEff[2], v2) ^ GF256Mul(qEff[3], v3)
		dst[i+1] = GF256Mul(qEff[0], v1) ^ GF256Mul(qEff[1], v0) ^ GF256Mul(qEff[2], v3) ^ GF256Mul(qEff[3], v2)
		dst[i+2] = GF256Mul(qEff[0], v2) ^ GF256Mul(qEff[2], v0) ^ GF256Mul(qEff[1], v3) ^ GF256Mul(qEff[3], v1)
		dst[i+3] = GF256Mul(qEff[0], v3) ^ GF256Mul(qEff[3], v0) ^ GF256Mul(qEff[1], v2) ^ GF256Mul(qEff[2], v1)
	}
	for i := limit; i < len(src); i++ {
		dst[i] = src[i]
	}
}

// IsoQuantUncollimation4D applies inverse 4D isoclinic rotation:
// v = qL_inv * v' * qR_inv
// across all 4-byte quads in src, writing reconstructed bytes into dst.
func IsoQuantUncollimation4D(dst, src []byte, qLInv, qRInv Quaternion4) {
	copy(dst, src)
	IsoQuantUncollimation4DInplace(dst, qLInv, qRInv)
}

// IsoQuantUncollimation4DInplace applies branchless in-place inverse 4D isoclinic rotation.
// Guarantees strictly zero dynamic memory allocations and zero scratchpad allocations.
func IsoQuantUncollimation4DInplace(dst []byte, qLInv, qRInv Quaternion4) {
	qEff := qLInv.Mul(qRInv)
	if qEff.IsIdentity() {
		return
	}
	limit := (len(dst) / 4) * 4
	for i := 0; i < limit; i += 4 {
		v0, v1, v2, v3 := dst[i], dst[i+1], dst[i+2], dst[i+3]
		dst[i]   = GF256Mul(qEff[0], v0) ^ GF256Mul(qEff[1], v1) ^ GF256Mul(qEff[2], v2) ^ GF256Mul(qEff[3], v3)
		dst[i+1] = GF256Mul(qEff[0], v1) ^ GF256Mul(qEff[1], v0) ^ GF256Mul(qEff[2], v3) ^ GF256Mul(qEff[3], v2)
		dst[i+2] = GF256Mul(qEff[0], v2) ^ GF256Mul(qEff[2], v0) ^ GF256Mul(qEff[1], v3) ^ GF256Mul(qEff[3], v1)
		dst[i+3] = GF256Mul(qEff[0], v3) ^ GF256Mul(qEff[3], v0) ^ GF256Mul(qEff[1], v2) ^ GF256Mul(qEff[2], v1)
	}
}

// EstimateOptimalIsoQuant evaluates empirical cross-channel covariance on a cache-resident prefix sample
// and selects the optimal isoclinic pair (qL, qR) that maximizes residual sparsity.
func EstimateOptimalIsoQuant(src []byte) (qL, qR Quaternion4, benefited bool) {
	n := len(src)
	ident := IdentityQuaternion4()
	if n < 16 {
		return ident, ident, false
	}

	sampleLen := min(n, 2048) &^ 3
	if sampleLen < 16 {
		return ident, ident, false
	}

	// Calculate base sparsity (number of zero bytes across channels)
	baseZeros := 0
	for i := 0; i < sampleLen; i++ {
		if src[i] == 0 {
			baseZeros++
		}
	}

	bestQL, bestQR := ident, ident
	bestZeros := baseZeros

	// Channel moment accumulation
	var m0, m1, m2, m3 uint8
	for i := 0; i < sampleLen; i += 4 {
		m0 ^= src[i]
		m1 ^= src[i+1]
		m2 ^= src[i+2]
		m3 ^= src[i+3]
	}

	// Candidate unit quaternions (satisfying q0 ^ q1 ^ q2 ^ q3 == 1, so q_inv == q)
	candidates := []Quaternion4{
		{0, 1, 0, 0}, // Transposition (0,1), (2,3)
		{0, 0, 1, 0}, // Transposition (0,2), (1,3)
		{0, 0, 0, 1}, // Transposition (0,3), (1,2)
		{1, 1, 1, 0}, // Triple coupling
		{1, 1, 0, 1},
		{1, 0, 1, 1},
		{0, 1, 1, 1},
	}

	// Empirical channel-coupling unit quaternions
	if m0 != 0 {
		c1 := GF256Mul(m1, GF256Inv(m0))
		c2 := GF256Mul(m2, GF256Inv(m0))
		c3 := GF256Mul(m3, GF256Inv(m0))
		if c1 != 0 {
			candidates = append(candidates, Quaternion4{1, c1, c1, 0}, Quaternion4{1, 0, c1, c1}, Quaternion4{1, c1, 0, c1})
		}
		if c2 != 0 {
			candidates = append(candidates, Quaternion4{1, c2, c2, 0}, Quaternion4{1, 0, c2, c2}, Quaternion4{1, c2, 0, c2})
		}
		if c3 != 0 {
			candidates = append(candidates, Quaternion4{1, c3, c3, 0}, Quaternion4{1, 0, c3, c3}, Quaternion4{1, c3, 0, c3})
		}
	}

	// Sample quad coupling
	for offset := 0; offset+4 <= sampleLen && offset < 32; offset += 4 {
		v0, v1, v2, v3 := src[offset], src[offset+1], src[offset+2], src[offset+3]
		if v0 != 0 {
			k1 := GF256Mul(v1, GF256Inv(v0))
			k2 := GF256Mul(v2, GF256Inv(v0))
			k3 := GF256Mul(v3, GF256Inv(v0))
			if k1 != 0 {
				candidates = append(candidates, Quaternion4{1, k1, k1, 0})
			}
			if k2 != 0 {
				candidates = append(candidates, Quaternion4{1, 0, k2, k2})
			}
			if k3 != 0 {
				candidates = append(candidates, Quaternion4{1, k3, 0, k3})
			}
		}
	}

	// Search candidate pairs (qL, qR)
	for _, candL := range candidates {
		_, okL := candL.Inv()
		if !okL {
			continue
		}
		// Test single quaternion candL with qR = ident
		qEffSingle := candL
		zerosSingle := 0
		for i := 0; i < sampleLen; i += 4 {
			d0, d1, d2, d3 := src[i], src[i+1], src[i+2], src[i+3]
			t0 := GF256Mul(qEffSingle[0], d0) ^ GF256Mul(qEffSingle[1], d1) ^ GF256Mul(qEffSingle[2], d2) ^ GF256Mul(qEffSingle[3], d3)
			t1 := GF256Mul(qEffSingle[0], d1) ^ GF256Mul(qEffSingle[1], d0) ^ GF256Mul(qEffSingle[2], d3) ^ GF256Mul(qEffSingle[3], d2)
			t2 := GF256Mul(qEffSingle[0], d2) ^ GF256Mul(qEffSingle[2], d0) ^ GF256Mul(qEffSingle[1], d3) ^ GF256Mul(qEffSingle[3], d1)
			t3 := GF256Mul(qEffSingle[0], d3) ^ GF256Mul(qEffSingle[3], d0) ^ GF256Mul(qEffSingle[1], d2) ^ GF256Mul(qEffSingle[2], d1)

			if t0 == 0 {
				zerosSingle++
			}
			if t1 == 0 {
				zerosSingle++
			}
			if t2 == 0 {
				zerosSingle++
			}
			if t3 == 0 {
				zerosSingle++
			}
		}
		if zerosSingle > bestZeros {
			bestZeros = zerosSingle
			bestQL = candL
			bestQR = ident
		}

		for _, candR := range candidates {
			_, okR := candR.Inv()
			if !okR {
				continue
			}

			qEff := candL.Mul(candR)
			if qEff.IsIdentity() {
				continue
			}

			zeros := 0
			for i := 0; i < sampleLen; i += 4 {
				d0, d1, d2, d3 := src[i], src[i+1], src[i+2], src[i+3]
				t0 := GF256Mul(qEff[0], d0) ^ GF256Mul(qEff[1], d1) ^ GF256Mul(qEff[2], d2) ^ GF256Mul(qEff[3], d3)
				t1 := GF256Mul(qEff[0], d1) ^ GF256Mul(qEff[1], d0) ^ GF256Mul(qEff[2], d3) ^ GF256Mul(qEff[3], d2)
				t2 := GF256Mul(qEff[0], d2) ^ GF256Mul(qEff[2], d0) ^ GF256Mul(qEff[1], d3) ^ GF256Mul(qEff[3], d1)
				t3 := GF256Mul(qEff[0], d3) ^ GF256Mul(qEff[3], d0) ^ GF256Mul(qEff[1], d2) ^ GF256Mul(qEff[2], d1)

				if t0 == 0 {
					zeros++
				}
				if t1 == 0 {
					zeros++
				}
				if t2 == 0 {
					zeros++
				}
				if t3 == 0 {
					zeros++
				}
			}

			if zeros > bestZeros {
				bestZeros = zeros
				bestQL = candL
				bestQR = candR
			}
		}
	}

	gain := float64(bestZeros-baseZeros) / float64(sampleLen)
	if gain > 0.02 && (!bestQL.IsIdentity() || !bestQR.IsIdentity()) {
		return bestQL, bestQR, true
	}
	return ident, ident, false
}

// CompressIsoQuant encodes src using 4D IsoQuant Quaternionic Collimation.
// Returns an encoded byte slice prefixed with the 20-byte PRZM_MAGIC_ISOQUANT frame header.
func CompressIsoQuant(src []byte) ([]byte, error) {
	n := len(src)
	if n > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	ident := IdentityQuaternion4()
	if n < 16 {
		dst := make([]byte, IsoQuantHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_ISOQUANT)
		dst[4] = IsoQuantModeRaw
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[12:16], ident[:])
		copy(dst[16:20], ident[:])
		copy(dst[IsoQuantHeaderSize:], src)
		return dst, nil
	}

	qL, qR, benefited := EstimateOptimalIsoQuant(src)
	if !benefited {
		dst := make([]byte, IsoQuantHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_ISOQUANT)
		dst[4] = IsoQuantModeRaw
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[12:16], ident[:])
		copy(dst[16:20], ident[:])
		copy(dst[IsoQuantHeaderSize:], src)
		return dst, nil
	}

	transformed := make([]byte, n)
	IsoQuantCollimation4D(transformed, src, qL, qR)
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	// Check if PWS AVX-Wave cascades effectively on the collimated sparse stream
	if packed, err := PWSEncode(transformed); err == nil && len(packed)+IsoQuantHeaderSize < n {
		dst := make([]byte, IsoQuantHeaderSize+len(packed))
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_ISOQUANT)
		dst[4] = IsoQuantModeCompressed
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[12:16], qLInv[:])
		copy(dst[16:20], qRInv[:])
		copy(dst[IsoQuantHeaderSize:], packed)
		return dst, nil
	}

	// Mode 1: Collimated raw stream
	dst := make([]byte, IsoQuantHeaderSize+n)
	binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_ISOQUANT)
	dst[4] = IsoQuantModeCollimated
	binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
	copy(dst[12:16], qLInv[:])
	copy(dst[16:20], qRInv[:])
	copy(dst[IsoQuantHeaderSize:], transformed)
	return dst, nil
}

// DecompressIsoQuant decodes a PRZM_MAGIC_ISOQUANT frame into dst.
// Strictly guarantees 0 heap allocations if dst has capacity >= origLen + 64.
func DecompressIsoQuant(src, dst []byte) ([]byte, error) {
	if len(src) < IsoQuantHeaderSize {
		return nil, ErrIsoQuantCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != PRZM_MAGIC_ISOQUANT {
		return nil, ErrIsoQuantCorrupt
	}

	mode := src[4]
	origLen := int(binary.LittleEndian.Uint32(src[8:12]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	var qLInv, qRInv Quaternion4
	copy(qLInv[:], src[12:16])
	copy(qRInv[:], src[16:20])

	payload := src[IsoQuantHeaderSize:]

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	switch mode {
	case IsoQuantModeRaw:
		if len(payload) < origLen {
			return nil, ErrIsoQuantCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil

	case IsoQuantModeCollimated:
		if len(payload) < origLen {
			return nil, ErrIsoQuantCorrupt
		}
		copy(dst, payload[:origLen])
		IsoQuantUncollimation4DInplace(dst, qLInv, qRInv)
		return dst, nil

	case IsoQuantModeCompressed:
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
			return nil, ErrIsoQuantCorrupt
		}
		if len(dst) > 0 && len(innerDec) > 0 && &dst[0] != &innerDec[0] {
			copy(dst, innerDec[:origLen])
		}
		IsoQuantUncollimation4DInplace(dst, qLInv, qRInv)
		return dst, nil

	default:
		return nil, ErrIsoQuantUnsupportedMode
	}
}
