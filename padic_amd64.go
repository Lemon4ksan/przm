// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 && !purego

package przm

import "unsafe"

// Feature detection for AVX2 support
var hasAVX2 = detectAVX2()

func detectAVX2() bool {
	maxLeaf, _, _, _ := cpuid(0, 0)
	if maxLeaf < 7 {
		return false
	}
	_, _, c1, _ := cpuid(1, 0)
	const (
		osxsaveBit = 1 << 27
		avxBit     = 1 << 28
	)
	if (c1 & (osxsaveBit | avxBit)) != (osxsaveBit | avxBit) {
		return false
	}
	eaxXCR0, _ := xgetbv()
	if (eaxXCR0 & 0x06) != 0x06 {
		return false
	}
	_, b7, _, _ := cpuid(7, 0)
	return (b7 & (1 << 5)) != 0
}

//go:noescape
func padicInverseAVX2_16(data unsafe.Pointer, n int)

//go:noescape
func padicInverseAVX2_32(data unsafe.Pointer, n int)

//go:noescape
func padicInverseAVX2_64(data unsafe.Pointer, n int)

//go:noescape
func padicInverseWord64_AVX2(data unsafe.Pointer, numWords int)

// PadicInverseSIMD_16 executes hardware AVX/SSE prefix integration on stride-16 records.
func PadicInverseSIMD_16(data []byte) {
	n := len(data)
	if n < 32 {
		for j := 16; j < n; j++ {
			data[j] += data[j-16]
		}
		return
	}
	if hasAVX2 {
		padicInverseAVX2_16(unsafe.Pointer(&data[0]), n)
		return
	}
	padicInverseSWAR16(data)
}

// PadicInverseSIMD_32 executes hardware AVX2 prefix integration on stride-32 records.
func PadicInverseSIMD_32(data []byte) {
	n := len(data)
	if n < 64 {
		for j := 32; j < n; j++ {
			data[j] += data[j-32]
		}
		return
	}
	if hasAVX2 {
		padicInverseAVX2_32(unsafe.Pointer(&data[0]), n)
		return
	}
	padicInverseSWAR32(data)
}

// PadicInverseSIMD_64 executes hardware AVX2 prefix integration on stride-64 records.
func PadicInverseSIMD_64(data []byte) {
	n := len(data)
	if n < 128 {
		for j := 64; j < n; j++ {
			data[j] += data[j-64]
		}
		return
	}
	if hasAVX2 {
		padicInverseAVX2_64(unsafe.Pointer(&data[0]), n)
		return
	}
	padicInverseSWAR64(data)
}

const (
	swar7F = 0x7f7f7f7f7f7f7f7f
	swar80 = 0x8080808080808080
)

func swarAddInline(x, y uint64) uint64 {
	s := (x & swar7F) + (y & swar7F)
	return s ^ ((x ^ y) & swar80)
}

func padicInverseSWAR16(dst []byte) {
	n := len(dst)
	p0 := unsafe.Pointer(&dst[0])
	w0 := *(*uint64)(p0)
	w1 := *(*uint64)(unsafe.Add(p0, 8))

	for j := 16; j <= n-16; j += 16 {
		pCur := unsafe.Pointer(&dst[j])
		w0 = swarAddInline(*(*uint64)(pCur), w0)
		w1 = swarAddInline(*(*uint64)(unsafe.Add(pCur, 8)), w1)
		*(*uint64)(pCur) = w0
		*(*uint64)(unsafe.Add(pCur, 8)) = w1
	}
}

func padicInverseSWAR32(dst []byte) {
	n := len(dst)
	p0 := unsafe.Pointer(&dst[0])
	w0 := *(*uint64)(p0)
	w1 := *(*uint64)(unsafe.Add(p0, 8))
	w2 := *(*uint64)(unsafe.Add(p0, 16))
	w3 := *(*uint64)(unsafe.Add(p0, 24))

	for j := 32; j <= n-32; j += 32 {
		pCur := unsafe.Pointer(&dst[j])
		w0 = swarAddInline(*(*uint64)(pCur), w0)
		w1 = swarAddInline(*(*uint64)(unsafe.Add(pCur, 8)), w1)
		w2 = swarAddInline(*(*uint64)(unsafe.Add(pCur, 16)), w2)
		w3 = swarAddInline(*(*uint64)(unsafe.Add(pCur, 24)), w3)

		*(*uint64)(pCur) = w0
		*(*uint64)(unsafe.Add(pCur, 8)) = w1
		*(*uint64)(unsafe.Add(pCur, 16)) = w2
		*(*uint64)(unsafe.Add(pCur, 24)) = w3
	}
}

func padicInverseSWAR64(dst []byte) {
	n := len(dst)
	p0 := unsafe.Pointer(&dst[0])
	var w [8]uint64
	for k := range 8 {
		w[k] = *(*uint64)(unsafe.Add(p0, k*8))
	}

	for j := 64; j <= n-64; j += 64 {
		pCur := unsafe.Pointer(&dst[j])
		for k := range 8 {
			w[k] = swarAddInline(*(*uint64)(unsafe.Add(pCur, k*8)), w[k])
			*(*uint64)(unsafe.Add(pCur, k*8)) = w[k]
		}
	}
}

// PadicInverseWord64 executes hardware AVX2 / SWAR prefix integration on 64-bit integer words.
func PadicInverseWord64(data []byte) {
	n := len(data) / 8
	if n <= 1 {
		return
	}
	if hasAVX2 {
		padicInverseWord64_AVX2(unsafe.Pointer(&data[0]), n)
		return
	}
	padicInverseWord64_SWAR(data)
}

func padicInverseWord64_SWAR(data []byte) {
	n := len(data) / 8
	if n <= 1 {
		return
	}
	ptr := (*[1 << 30]uint64)(unsafe.Pointer(&data[0]))[:n:n]
	acc := ptr[0]
	i := 1
	for ; i+4 <= n; i += 4 {
		acc += ptr[i]
		ptr[i] = acc
		acc += ptr[i+1]
		ptr[i+1] = acc
		acc += ptr[i+2]
		ptr[i+2] = acc
		acc += ptr[i+3]
		ptr[i+3] = acc
	}
	for ; i < n; i++ {
		acc += ptr[i]
		ptr[i] = acc
	}
}
