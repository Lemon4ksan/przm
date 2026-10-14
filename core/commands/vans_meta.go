// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package commands provides command splitting, Mode 8 framing, and 8-context Bigram V-ANS entropy encoding and decoding.
// Strictly adheres to the Clean Room Specification with zero dynamic memory allocations.
package commands

import (
	"encoding/binary"
	"errors"

	"github.com/lemon4ksan/foundation/generic"
)

var (
	ErrCorruptedFrame = errors.New("przm: corrupted frame")
	ErrBufferTooSmall = errors.New("przm: destination buffer too small")
)

// MetaModel represents the precomputed frequency, CDF, and inversion tables for one context.
type MetaModel struct {
	Freq   [256]uint16
	CDF    [256]uint16
	Lookup [4096]byte
}

var metaModels8 [8]MetaModel

func init() {
	for c := range 8 {
		var cum uint16
		for s := range 256 {
			f := staticMetaFreq8[c][s]
			metaModels8[c].Freq[s] = f
			metaModels8[c].CDF[s] = cum
			for j := range f {
				metaModels8[c].Lookup[cum+j] = byte(s)
			}
			cum += f
		}
	}
}

// ContextMeta maps a preceding byte p to an 8-context category [0..7].
func ContextMeta(p byte) int {
	if p == 0 {
		return 0
	}
	if p < 4 {
		return 1
	}
	if p < 16 {
		return 2
	}
	if p < 32 {
		return 3
	}
	if p < 64 {
		return 4
	}
	if p < 128 {
		return 5
	}
	if p < 192 {
		return 6
	}
	return 7
}

type varBufPoolType [2 * 1024 * 1024]byte

var varBufPool = generic.NewPool[varBufPoolType](func() *varBufPoolType {
	return new(varBufPoolType)
})

// EncodeVansMeta8 encodes src into dst using 8-context Bigram V-ANS with 4 interleaved lanes.
func EncodeVansMeta8(src, dst []byte) (int, error) {
	n := len(src)
	if n == 0 {
		return encodeEmptyHeader(dst)
	}
	buf := varBufPool.Get()
	outLen, err := encodeVansMeta8Internal(src, dst, buf[:])
	varBufPool.Put(buf)
	return outLen, err
}

func encodeEmptyHeader(dst []byte) (int, error) {
	if len(dst) < 24 {
		return 0, ErrBufferTooSmall
	}
	dst[0] = 'V'
	dst[1] = 'A'
	dst[2] = 0x01
	dst[3] = 0x41
	binary.LittleEndian.PutUint32(dst[4:8], 0)
	for i := 8; i < 24; i += 4 {
		binary.LittleEndian.PutUint32(dst[i:i+4], 65536)
	}
	return 24, nil
}

func encodeVansMeta8Internal(src, dst, streamBuf []byte) (int, error) {
	n := len(src)
	var streamPtr int
	x := [4]uint32{65536, 65536, 65536, 65536}

	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		sym := src[i]
		ctx := 0
		if i > 0 {
			ctx = ContextMeta(src[i-1])
		}
		freq := uint32(metaModels8[ctx].Freq[sym])
		cdf := uint32(metaModels8[ctx].CDF[sym])

		for x[lane] > 4096*freq-1 {
			if streamPtr >= len(streamBuf) {
				return 0, ErrBufferTooSmall
			}
			streamBuf[streamPtr] = byte(x[lane] & 0xFF)
			streamPtr++
			x[lane] >>= 8
		}
		x[lane] = (x[lane]/freq)*4096 + cdf + (x[lane] % freq)
	}

	totalLen := 24 + streamPtr
	if totalLen > len(dst) {
		return 0, ErrBufferTooSmall
	}

	writeVansHeader(dst, uint32(n), &x)
	writeReversedStream(dst[24:], streamBuf[:streamPtr])
	return totalLen, nil
}

func writeVansHeader(dst []byte, n uint32, x *[4]uint32) {
	dst[0] = 'V'
	dst[1] = 'A'
	dst[2] = 0x01
	dst[3] = 0x41
	binary.LittleEndian.PutUint32(dst[4:8], n)
	binary.LittleEndian.PutUint32(dst[8:12], x[0])
	binary.LittleEndian.PutUint32(dst[12:16], x[1])
	binary.LittleEndian.PutUint32(dst[16:20], x[2])
	binary.LittleEndian.PutUint32(dst[20:24], x[3])
}

func writeReversedStream(dst, stream []byte) {
	slen := len(stream)
	for i := range slen {
		dst[i] = stream[slen-1-i]
	}
}

// DecodeVansMeta8 decodes an 8-context Bigram V-ANS stream from src into dst.
func DecodeVansMeta8(src, dst []byte, expectedLen int) error {
	if len(src) < 24 {
		return ErrCorruptedFrame
	}
	if src[0] != 'V' || src[1] != 'A' || src[2] != 0x01 || (src[3]&0x40) == 0 {
		return ErrCorruptedFrame
	}
	n := int(binary.LittleEndian.Uint32(src[4:8]))
	if n != expectedLen || n > len(dst) {
		return ErrCorruptedFrame
	}
	if n == 0 {
		return nil
	}

	x := [4]uint32{
		binary.LittleEndian.Uint32(src[8:12]),
		binary.LittleEndian.Uint32(src[12:16]),
		binary.LittleEndian.Uint32(src[16:20]),
		binary.LittleEndian.Uint32(src[20:24]),
	}
	pos := 24
	var prevSym byte

	for i := range n {
		lane := i & 3
		ctx := ContextMeta(prevSym)
		slot := x[lane] & 4095
		sym := metaModels8[ctx].Lookup[slot]
		dst[i] = sym
		prevSym = sym

		freq := uint32(metaModels8[ctx].Freq[sym])
		cdf := uint32(metaModels8[ctx].CDF[sym])
		x[lane] = freq*(x[lane]>>12) + (slot - cdf)

		for x[lane] < 65536 {
			if pos >= len(src) {
				return ErrCorruptedFrame
			}
			x[lane] = (x[lane] << 8) | uint32(src[pos])
			pos++
		}
	}

	if pos != len(src) {
		return ErrCorruptedFrame
	}
	return nil
}
