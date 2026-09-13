// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.simd && amd64

package przm

import (
	"simd/archsimd"
)

const ArchSIMDEnabled = true

// reconstructBlockSIMD accelerates the tensor Galois outer product reconstruction using Go 1.27's simd/archsimd.
// Compiles directly to inlined VGF2P8MULB + VPXORD intrinsics without Plan9 call overhead.
func reconstructBlockSIMD(b *LRGGFBlock, dst []byte) bool {
	blockSize := b.M * b.N
	if len(dst) < blockSize {
		return false
	}

	if b.Rank == 0 {
		if len(b.Raw) >= blockSize {
			copy(dst[:blockSize], b.Raw[:blockSize])
		} else {
			clear(dst[:blockSize])
		}
		return true
	}

	clear(dst[:blockSize])

	M, N, R := b.M, b.N, b.Rank

	for r := 0; r < R; r++ {
		uOffset := r * M
		vOffset := r * N

		for i := 0; i < M; i++ {
			u_val := b.U[uOffset+i]
			if u_val == 0 {
				continue
			}

			uVec := archsimd.BroadcastUint8x32(u_val)
			rowOff := i * N

			j := 0
			for ; j+32 <= N; j += 32 {
				vVec := archsimd.LoadUint8x32(b.V[vOffset+j : vOffset+j+32])
				prod := uVec.GaloisFieldMul(vVec)
				cur := archsimd.LoadUint8x32(dst[rowOff+j : rowOff+j+32])
				res := cur.Xor(prod)
				res.Store(dst[rowOff+j : rowOff+j+32])
			}
			// scalar tail for non-32-multiple widths
			for ; j < N; j++ {
				dst[rowOff+j] ^= GF256Mul(u_val, b.V[vOffset+j])
			}
		}
	}

	for _, e := range b.Errors {
		idx := int(e.Row)*N + int(e.Col)
		if idx < blockSize {
			dst[idx] ^= e.Val
		}
	}

	return true
}
