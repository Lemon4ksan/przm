// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 || purego

package przm

import "unsafe"

const hasGFNIHw = false

// TransformBlockInplace applies the symplectic shear in-place.
// Operates on 4D vector chunks.
// Because T_S is a strict involution, calling TransformBlockInplace twice
// restores the original data with 100% bit-exact precision.
// Strictly 0 heap allocations.
func (s SymplecticShear) TransformBlockInplace(block []byte) {
	if s.IsIdentity() {
		return
	}
	s.TransformBlockInplaceScalar(block)
}

// TransformBlockInplaceSIMD falls back to scalar on non-hardware platforms.
func (s SymplecticShear) TransformBlockInplaceSIMD(block []byte) {
	s.TransformBlockInplaceScalar(block)
}

// TransformBlock8Inplace applies 8-channel Sp(8, F_{2^8}) symplectic transvection in-place.
func TransformBlock8Inplace(block []byte) {
	n := len(block)
	for i := 0; i+8 <= n; i += 8 {
		p := (*uint64)(unsafe.Pointer(&block[i]))
		*p ^= (*p >> 32)
	}
}
