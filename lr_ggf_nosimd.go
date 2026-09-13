// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !goexperiment.simd || !amd64

package przm

const ArchSIMDEnabled = false

func reconstructBlockSIMD(b *LRGGFBlock, dst []byte) bool {
	return false
}
