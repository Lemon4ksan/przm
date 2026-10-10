// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 || purego

package przm

import "encoding/binary"

// PadicInverseSIMD_16 falls back to scalar on non-hardware platforms.
func PadicInverseSIMD_16(data []byte) {
	n := len(data)
	for j := 16; j < n; j++ {
		data[j] += data[j-16]
	}
}

// PadicInverseSIMD_32 falls back to scalar on non-hardware platforms.
func PadicInverseSIMD_32(data []byte) {
	n := len(data)
	for j := 32; j < n; j++ {
		data[j] += data[j-32]
	}
}

// PadicInverseSIMD_64 falls back to scalar on non-hardware platforms.
func PadicInverseSIMD_64(data []byte) {
	n := len(data)
	for j := 64; j < n; j++ {
		data[j] += data[j-64]
	}
}

// PadicInverseWord64 executes portable fallback prefix integration on 64-bit words.
func PadicInverseWord64(data []byte) {
	n := len(data) / 8
	if n <= 1 {
		return
	}
	acc := binary.LittleEndian.Uint64(data[0:8])
	for i := 1; i < n; i++ {
		w := binary.LittleEndian.Uint64(data[i*8 : (i+1)*8])
		acc += w
		binary.LittleEndian.PutUint64(data[i*8:(i+1)*8], acc)
	}
}
