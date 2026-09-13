// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.simd && amd64

package przm

import (
	"simd/archsimd"
)

const DECSIMDEnabled = true

func reconstructDECHHDSIMD(block *DECHHDBlock, dst []byte) bool {
	M := block.Rows
	W := block.Stride
	if M < 2 || W < 2 || len(dst) < M*W {
		return false
	}

	// Row 0: Prefix-XOR from anchor
	dst[0] = block.Anchor
	for j := 0; j < W-1; j++ {
		dst[j+1] = dst[j] ^ block.Ey[j]
	}

	v := make([]byte, W)
	curlIdx := 0

	for i := 0; i < M-1; i++ {
		rowCurr := (i + 1) * W
		rowPrev := i * W

		v[0] = block.Ex[i]
		if block.NNZ == 0 {
			exVal := block.Ex[i]
			exVec := archsimd.BroadcastUint8x32(exVal)
			j := 0
			for ; j+32 <= W; j += 32 {
				prevVec := archsimd.LoadUint8x32(dst[rowPrev+j : rowPrev+j+32])
				res := prevVec.Xor(exVec)
				res.Store(dst[rowCurr+j : rowCurr+j+32])
			}
			for ; j < W; j++ {
				dst[rowCurr+j] = dst[rowPrev+j] ^ exVal
			}
		} else {
			for j := 0; j < W-1; j++ {
				v[j+1] = v[j] ^ block.Curl[curlIdx]
				curlIdx++
			}
			j := 0
			for ; j+32 <= W; j += 32 {
				vVec := archsimd.LoadUint8x32(v[j : j+32])
				prevVec := archsimd.LoadUint8x32(dst[rowPrev+j : rowPrev+j+32])
				res := prevVec.Xor(vVec)
				res.Store(dst[rowCurr+j : rowCurr+j+32])
			}
			for ; j < W; j++ {
				dst[rowCurr+j] = dst[rowPrev+j] ^ v[j]
			}
		}
	}

	return true
}
