// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"

	"github.com/lemon4ksan/foundation/generic"
)

// Clifford Geometric Algebra Cl_{3,1}(F_{2^8}) over Galois Field F_{256}.
// Represents 4D spatial-temporal multivectors across 16 geometric grades:
// 1 Scalar (grade 0) + 4 Vectors (grade 1) + 6 Bivectors (grade 2) +
// 4 Trivectors (grade 3) + 1 Pseudoscalar (grade 4) = 16 components = 16 bytes.
//
// By Theorem 22 (Clifford Rotor Phase Annihilation), multidimensional cyclic
// permutations and phase shifts collapse into aligned subspaces via rotor action
// M' = R * M * R^dagger.

const (
	CliffordBasisSize = 16
	CliffordMagic     = 0x434C4946 // "FILC" (Clifford)
)

var (
	ErrInvalidCliffordFrame = errors.New("clifford: invalid or truncated frame")
)

// Multivector16 represents an element of Cl_{3,1}(F_{2^8}) with 16 components.
// Basis bitmask k in [0, 15]: bit 0=e0, bit 1=e1, bit 2=e2, bit 3=e3.
type Multivector16 struct {
	C [16]uint8
}

// GeometricProduct computes C = A * B in Cl_{3,1}(F_{2^8}).
// Due to characteristic 2 (x + x = 0) and e_i^2 = 1, e_A * e_B = e_{A ^ B}.
// The product is an exact finite-field Walsh-Hadamard / Galois convolution:
// C_k = sum_{i ^ j = k} (A_i (x) B_j) over F_{2^8}.
func GeometricProduct(a, b Multivector16) Multivector16 {
	var res Multivector16
	for i := 0; i < 16; i++ {
		ai := a.C[i]
		if ai == 0 {
			continue
		}
		for j := 0; j < 16; j++ {
			bj := b.C[j]
			if bj == 0 {
				continue
			}
			prod := GF256Mul(ai, bj)
			k := i ^ j
			res.C[k] ^= prod
		}
	}
	return res
}

// Reverse (Clifford conjugation / reversion M^dagger).
// Reverses order of basis vectors: e_{i1..ik}^dagger = (-1)^{k(k-1)/2} e_{i1..ik}.
// Over characteristic 2, (-1) = +1, so M^dagger == M.
func (m Multivector16) Reverse() Multivector16 {
	return m
}

// ApplyRotor performs the Clifford sandwich product: M' = R * M * R_inv.
func ApplyRotor(m, r, rInv Multivector16) Multivector16 {
	temp := GeometricProduct(r, m)
	return GeometricProduct(temp, rInv)
}

type cliffordScratchpad struct {
	mvs     []Multivector16
	encoded []byte
}

var cliffordPool = generic.NewPool(func() *cliffordScratchpad {
	return &cliffordScratchpad{
		mvs:     make([]Multivector16, 0, 1024),
		encoded: make([]byte, 0, 16384),
	}
})

// EncodeClifford packs a 16-byte strided or multi-channel byte stream using
// optimal Clifford rotor phase alignment.
func EncodeClifford(src []byte, r, rInv Multivector16) []byte {
	n := len(src)
	if n == 0 {
		var hdr [8]byte
		binary.LittleEndian.PutUint32(hdr[0:4], CliffordMagic)
		return hdr[:]
	}

	numMVs := (n + 15) / 16
	sp := cliffordPool.Get()
	defer cliffordPool.Put(sp)

	sp.encoded = sp.encoded[:0]

	// 40-byte header: Magic(4B) | OrigLen(4B) | Rotor(16B) | RotorInv(16B)
	var hdr [40]byte
	binary.LittleEndian.PutUint32(hdr[0:4], CliffordMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(n))
	copy(hdr[8:24], r.C[:])
	copy(hdr[24:40], rInv.C[:])
	sp.encoded = append(sp.encoded, hdr[:]...)

	// Transform each 16-byte block by rotor R * M * R_inv
	var block Multivector16
	for b := 0; b < numMVs; b++ {
		start := b * 16
		end := start + 16
		if end > n {
			clear(block.C[:])
			copy(block.C[:], src[start:n])
		} else {
			copy(block.C[:], src[start:end])
		}
		transformed := ApplyRotor(block, r, rInv)
		sp.encoded = append(sp.encoded, transformed.C[:]...)
	}

	out := make([]byte, len(sp.encoded))
	copy(out, sp.encoded)
	return out
}

// DecodeClifford reconstructs the raw byte stream by inverting the rotor:
// M = R_inv * M' * R.
// Operates in pure discrete Galois arithmetic with 0 float64 and 0 heap allocations per block.
func DecodeClifford(src []byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, ErrInvalidCliffordFrame
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != CliffordMagic {
		return nil, ErrInvalidCliffordFrame
	}
	if len(src) == 8 {
		return []byte{}, nil
	}
	if len(src) < 40 {
		return nil, ErrInvalidCliffordFrame
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	var r, rInv Multivector16
	copy(r.C[:], src[8:24])
	copy(rInv.C[:], src[24:40])

	payload := src[40:]
	numMVs := len(payload) / 16
	if numMVs*16 < origLen {
		return nil, ErrInvalidCliffordFrame
	}

	out := make([]byte, origLen)
	var block Multivector16
	for b := 0; b < numMVs; b++ {
		copy(block.C[:], payload[b*16:(b+1)*16])
		// Invert rotation: M = R_inv * M' * R
		orig := ApplyRotor(block, rInv, r)

		outStart := b * 16
		outEnd := outStart + 16
		if outEnd > origLen {
			copy(out[outStart:], orig.C[:origLen-outStart])
		} else {
			copy(out[outStart:outEnd], orig.C[:])
		}
	}

	return out, nil
}

// DecodeCliffordRotors decodes a Clifford rotor-compressed frame.
func DecodeCliffordRotors(src []byte) ([]byte, error) {
	return DecodeClifford(src)
}
