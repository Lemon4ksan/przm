// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.simd && amd64

package przm

const NSSCESIMDEnabled = true

// reconstructFIRSIMD reconstructs the output stream from innovations using matrix table.
func reconstructFIRSIMD(H uint64, u []byte, dst []byte) bool {
	n := len(u)
	if len(dst) < n || n == 0 {
		return false
	}

	table := BuildMatrixTable(H)
	dst[0] = u[0]
	for t := 1; t < n; t++ {
		dst[t] = u[t] ^ table[dst[t-1]]
	}

	return true
}

// reconstructZeroInnovSIMD reconstructs zero-innovation trajectory.
func reconstructZeroInnovSIMD(H uint64, u0 byte, dst []byte) bool {
	n := len(dst)
	if n == 0 {
		return false
	}

	table := BuildMatrixTable(H)
	dst[0] = u0
	for t := 1; t < n; t++ {
		dst[t] = table[dst[t-1]]
	}

	return true
}
