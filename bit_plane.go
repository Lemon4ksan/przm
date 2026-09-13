// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
)

var (
	ErrBitPlaneTruncated = errors.New("przm/bitplane: truncated or invalid stream")
)

const (
	BitPlaneMagic uint32 = 0x504C4E31 // "1NLP" (Plane 1)
)

// Transpose8x8 performs an exact SWAR 8x8 bit-matrix transposition on a 64-bit word.
// Input: 8 bytes, each containing 8 bits.
// Output: 8 bytes, where output byte k contains bit k from each of the 8 input bytes.
// By Theorem 31, Transpose8x8 is an involution: Transpose8x8(Transpose8x8(x)) == x.
func Transpose8x8(x uint64) uint64 {
	// Hacker's Delight Chapter 7: 8x8 bit matrix transposition
	// Across row/col boundaries with stride 8, delta is always a multiple of 7 (7*(j - i)).
	t := (x ^ (x >> 7)) & 0x00AA00AA00AA00AA
	x = x ^ t ^ (t << 7)
	t = (x ^ (x >> 14)) & 0x0000CCCC0000CCCC
	x = x ^ t ^ (t << 14)
	t = (x ^ (x >> 28)) & 0x00000000F0F0F0F0
	x = x ^ t ^ (t << 28)
	return x
}

// BitPlaneSliceForward transforms an N-byte buffer by slicing it into 8 contiguous bit-planes.
// All MSBs (bit 7) are grouped into Plane 7, bits 6 into Plane 6, down to Plane 0.
// Tail bytes (< 8) are preserved at the end.
func BitPlaneSliceForward(src []byte, dst []byte) []byte {
	n := len(src)
	num8 := n / 8
	planeLen := num8

	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}

	for i := 0; i < num8; i++ {
		chunk := binary.LittleEndian.Uint64(src[i*8 : i*8+8])
		trans := Transpose8x8(chunk)
		for p := 0; p < 8; p++ {
			dst[p*planeLen+i] = byte(trans >> (p * 8))
		}
	}

	// Copy remaining tail bytes
	tail := num8 * 8
	if tail < n {
		copy(dst[tail:], src[tail:])
	}

	return dst
}

// BitPlaneSliceInverse reconstructs the original N-byte buffer from 8 contiguous bit-planes.
// Exact bit-for-bit lossless inverse of BitPlaneSliceForward.
func BitPlaneSliceInverse(src []byte, dst []byte) []byte {
	n := len(src)
	num8 := n / 8
	planeLen := num8

	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}

	for i := 0; i < num8; i++ {
		var trans uint64
		for p := 0; p < 8; p++ {
			trans |= uint64(src[p*planeLen+i]) << (p * 8)
		}
		chunk := Transpose8x8(trans)
		binary.LittleEndian.PutUint64(dst[i*8:i*8+8], chunk)
	}

	tail := num8 * 8
	if tail < n {
		copy(dst[tail:], src[tail:])
	}

	return dst
}
