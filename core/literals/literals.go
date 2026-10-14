// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package literals provides 4-lane interleaved V-ANS coding, SIMD nibble planes,
// joint nibble frequency matrix encoding/decoding, and Étale Sheaf literal fiber compression.
// Strictly adheres to the Clean Room Specification with zero dynamic memory allocation.
package literals

import (
	"encoding/binary"
	"errors"
	"sync/atomic"

	"github.com/lemon4ksan/foundation/generic"
)

// Flag bit constants for Section 4 V-ANS container header.
const (
	VansFlag4X        uint8 = 0x01
	VansFlagSheaf     uint8 = 0x02
	VansFlagCtxSheaf  uint8 = 0x04
	VansFlagCtxOrder1 uint8 = 0x08
	VansCtxModeMask   uint8 = 0xF0
	FlagNibble        uint8 = 0x80
)

// Standard error definitions for literals codec.
var (
	ErrBufferTooSmall = errors.New("literals: buffer too small")
	ErrCorruptedFrame = errors.New("literals: corrupted frame")
)

// Scratchpad provides reusable intermediate memory for zero allocations.
type Scratchpad struct {
	fiberBuf   [8 * 1024 * 1024]byte
	vansLookup [4096]byte
	vansFreq   [256]uint16
	vansCDF    [256]uint16
	vansDecTab [4096]uint32
}

var scratchPool = generic.NewPool[Scratchpad](func() *Scratchpad {
	return new(Scratchpad)
})

var fastScratch [4]atomic.Pointer[Scratchpad]

func init() {
	for i := range fastScratch {
		fastScratch[i].Store(new(Scratchpad))
	}
}

func getScratch() *Scratchpad {
	for i := range fastScratch {
		if s := fastScratch[i].Swap(nil); s != nil {
			return s
		}
	}
	return scratchPool.Get()
}

func putScratch(s *Scratchpad) {
	if s == nil {
		return
	}
	for i := range fastScratch {
		if fastScratch[i].CompareAndSwap(nil, s) {
			return
		}
	}
	scratchPool.Put(s)
}

// NormalizeFrequencies produces symbol frequencies summing strictly to 4096.
func NormalizeFrequencies(counts []int, freq []uint16) error {
	for i := range freq {
		freq[i] = 0
	}
	var totalCounts, unique int
	for _, c := range counts {
		if c > 0 {
			totalCounts += c
			unique++
		}
	}
	if unique == 0 {
		return nil
	}
	if unique > 4096 {
		return ErrCorruptedFrame
	}

	remaining := 4096 - unique
	var sum uint16
	for i, c := range counts {
		if c <= 0 {
			continue
		}
		share := (c * remaining) / totalCounts
		f := uint16(1 + share)
		freq[i] = f
		sum += f
	}

	return adjustFrequencySum(counts, freq, sum)
}

// adjustFrequencySum distributes remaining points to match 4096 exactly.
func adjustFrequencySum(counts []int, freq []uint16, sum uint16) error {
	diff := int(4096) - int(sum)
	if diff > 0 {
		for range diff {
			bestSym := -1
			bestCount := -1
			for s, c := range counts {
				if c > bestCount && freq[s] > 0 {
					bestCount = c
					bestSym = s
				}
			}
			if bestSym >= 0 {
				freq[bestSym]++
				counts[bestSym] = -1
			}
		}
	} else if diff < 0 {
		for i := 0; i < -diff; i++ {
			for s := 255; s >= 0; s-- {
				if freq[s] > 1 {
					freq[s]--
					break
				}
			}
		}
	}
	return nil
}

// EncodeJointNibbleFreq serializes the 16x16 joint nibble frequency matrix.
func EncodeJointNibbleFreq(freq []uint16, buf []byte) []byte {
	var rowMask uint16
	for h := range 16 {
		for l := range 16 {
			if freq[(h<<4)|l] > 0 {
				rowMask |= (1 << h)
				break
			}
		}
	}

	buf = binary.LittleEndian.AppendUint16(buf, rowMask)
	for h := range 16 {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		var colMask uint16
		for l := range 16 {
			if freq[(h<<4)|l] > 0 {
				colMask |= (1 << l)
			}
		}
		buf = binary.LittleEndian.AppendUint16(buf, colMask)
		for l := range 16 {
			if (colMask & (1 << l)) == 0 {
				continue
			}
			w := freq[(h<<4)|l]
			buf = appendWeight(buf, w)
		}
	}
	return buf
}

func appendWeight(buf []byte, w uint16) []byte {
	if w < 128 {
		return append(buf, byte(w))
	}
	b0 := byte((w >> 8) | 0x80)
	b1 := byte(w & 0xFF)
	return append(buf, b0, b1)
}

// DecodeJointNibbleFreq parses the 16x16 joint nibble frequency distribution.
func DecodeJointNibbleFreq(src []byte, pos *int, freq []uint16) error {
	p := *pos
	if p+2 > len(src) {
		return ErrCorruptedFrame
	}
	rowMask := binary.LittleEndian.Uint16(src[p : p+2])
	p += 2

	for i := range freq {
		freq[i] = 0
	}
	var sum uint32

	for h := range 16 {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		if p+2 > len(src) {
			return ErrCorruptedFrame
		}
		colMask := binary.LittleEndian.Uint16(src[p : p+2])
		p += 2

		rowSum, err := decodeRowWeights(src, &p, colMask, h, freq)
		if err != nil {
			return err
		}
		sum += rowSum
	}

	if sum != 4096 {
		return ErrCorruptedFrame
	}
	*pos = p
	return nil
}

func decodeRowWeights(src []byte, p *int, colMask uint16, h int, freq []uint16) (uint32, error) {
	var rowSum uint32
	pos := *p
	for l := range 16 {
		if (colMask & (1 << l)) == 0 {
			continue
		}
		if pos >= len(src) {
			return 0, ErrCorruptedFrame
		}
		b0 := src[pos]
		pos++
		var w uint16
		if (b0 & 0x80) == 0 {
			w = uint16(b0)
		} else {
			if pos >= len(src) {
				return 0, ErrCorruptedFrame
			}
			b1 := src[pos]
			pos++
			w = (uint16(b0&0x7F) << 8) | uint16(b1)
		}
		if w == 0 || w > 4096 {
			return 0, ErrCorruptedFrame
		}
		freq[(h<<4)|l] = w
		rowSum += uint32(w)
	}
	*p = pos
	return rowSum, nil
}

// BuildVansTable creates CDF and symbol lookup tables from frequencies.
func BuildVansTable(freq, cdf []uint16, lookup []byte) {
	var cum uint16
	for s := range 256 {
		cdf[s] = cum
		f := freq[s]
		for k := range f {
			lookup[cum+k] = byte(s)
		}
		cum += f
	}
}

// BuildVansDecTab creates packed 32-bit decoding table from symbol frequencies.
// Each slot entry packs: (freq-1)<<20 | diff<<8 | sym.
func BuildVansDecTab(freq []uint16, decTab []uint32) {
	cum := uint32(0)
	for s := range 256 {
		f := uint32(freq[s])
		if f == 0 {
			continue
		}
		fMinus1 := (f - 1) & 0xFFF
		symVal := uint32(byte(s))
		for j := uint32(0); j < f; j++ {
			decTab[cum+j] = (fMinus1 << 20) | (j << 8) | symVal
		}
		cum += f
	}
}

// EncodeVANS4xDirect encodes src into dst using scratchpad without heap allocations.
func EncodeVANS4xDirect(src, dst []byte, freq []uint16, s *Scratchpad) (int, error) {
	uLen := len(src)
	if len(dst) < 8+64 {
		return 0, ErrBufferTooSmall
	}
	dst[0] = 'V'
	dst[1] = 'A'
	dst[2] = 0x01
	dst[3] = 0x00
	binary.LittleEndian.PutUint32(dst[4:8], uint32(uLen))

	BuildVansTable(freq, s.vansCDF[:], s.vansLookup[:])

	x := [4]uint32{65536, 65536, 65536, 65536}
	streamBuf := s.fiberBuf[:]
	streamLen := 0

	for i := len(src) - 1; i >= 0; i-- {
		sByte := src[i]
		lane := i & 3

		f := uint32(freq[sByte])
		c := uint32(s.vansCDF[sByte])
		if f == 0 {
			return 0, ErrCorruptedFrame
		}

		for x[lane] > 4096*f-1 {
			if streamLen >= len(streamBuf) {
				return 0, ErrBufferTooSmall
			}
			streamBuf[streamLen] = byte(x[lane] & 0xFF)
			streamLen++
			x[lane] >>= 8
		}
		x[lane] = (x[lane]/f)*4096 + c + (x[lane] % f)
	}

	freqBuf := EncodeJointNibbleFreq(freq, dst[:8])
	pos := len(freqBuf)
	if pos+16+streamLen > len(dst) {
		return 0, ErrBufferTooSmall
	}
	for j := range 4 {
		binary.LittleEndian.PutUint32(dst[pos:pos+4], x[j])
		pos += 4
	}
	for k := streamLen - 1; k >= 0; k-- {
		dst[pos] = streamBuf[k]
		pos++
	}
	return pos, nil
}

// EncodeVANS4x compresses input literals into a 4-lane interleaved V-ANS stream.
func EncodeVANS4x(src []byte, freq []uint16) ([]byte, error) {
	s := getScratch()
	defer putScratch(s)
	dst := make([]byte, 8+600+16+len(src)*2)
	n, err := EncodeVANS4xDirect(src, dst, freq, s)
	if err != nil {
		return nil, err
	}
	out := make([]byte, n-8)
	copy(out, dst[8:n])
	return out, nil
}

// DecodeVans4x decompresses a 4-lane interleaved V-ANS payload into dst using temporary scratch tables.
func DecodeVans4x(src, dst []byte, uLen int) error {
	if uLen == 0 {
		return nil
	}
	s := getScratch()
	defer putScratch(s)
	return DecodeVANS4xDirect(src, dst, uLen, s)
}

// DecodeAdaptiveVANS4x compresses src with adaptive 4-lane V-ANS into dst with 8-byte container header.
func EncodeAdaptiveVANS4x(src, dst []byte) (int, error) {
	uLen := len(src)
	if uLen == 0 {
		return 0, nil
	}
	s := getScratch()
	defer putScratch(s)

	var counts [256]int
	for _, b := range src {
		counts[b]++
	}
	if err := NormalizeFrequencies(counts[:], s.vansFreq[:]); err != nil {
		return 0, err
	}
	return EncodeVANS4xDirect(src, dst, s.vansFreq[:], s)
}

// DecodeAdaptiveVANS4x decompresses an adaptive 4-lane V-ANS container into dst.
func DecodeAdaptiveVANS4x(src, dst []byte, expLen int) error {
	if len(src) < 8 {
		return ErrCorruptedFrame
	}
	if expLen > len(dst) {
		return ErrBufferTooSmall
	}
	return DecodeVans4x(src[8:], dst, expLen)
}

// DecodeVANS4xDirect decompresses a 4-lane interleaved V-ANS payload using the provided Scratchpad.
func DecodeVANS4xDirect(src, dst []byte, uLen int, s *Scratchpad) error {
	if uLen == 0 {
		return nil
	}
	pos := 0
	if err := DecodeJointNibbleFreq(src, &pos, s.vansFreq[:]); err != nil {
		return err
	}
	BuildVansDecTab(s.vansFreq[:], s.vansDecTab[:])
	return runVans4xLoop(src, dst, uLen, pos, s.vansDecTab[:])
}

// DecodeVANS4x decompresses a 4-lane interleaved V-ANS payload into dst.
func DecodeVANS4x(src, dst []byte, uLen int, lookup []byte, freq, cdf []uint16) error {
	if uLen == 0 {
		return nil
	}
	pos := 0
	if err := DecodeJointNibbleFreq(src, &pos, freq); err != nil {
		return err
	}
	BuildVansTable(freq, cdf, lookup)

	s := getScratch()
	defer putScratch(s)
	BuildVansDecTab(freq, s.vansDecTab[:])
	return runVans4xLoop(src, dst, uLen, pos, s.vansDecTab[:])
}

func runVans4xLoop(src, dst []byte, uLen, pos int, decTab []uint32) error {
	if pos+16 > len(src) {
		return ErrCorruptedFrame
	}
	var x [4]uint32
	for j := range 4 {
		x[j] = binary.LittleEndian.Uint32(src[pos : pos+4])
		pos += 4
	}

	p := pos
	x0, x1, x2, x3 := x[0], x[1], x[2], x[3]
	i := 0
	srcLen := len(src)

	for i+4 <= uLen {
		e0 := decTab[x0&4095]
		dst[i] = byte(e0)
		x0 = ((e0>>20)+1)*(x0>>12) + ((e0 >> 8) & 0xFFF)
		for x0 < 65536 {
			if p >= srcLen {
				return ErrCorruptedFrame
			}
			x0 = (x0 << 8) | uint32(src[p])
			p++
		}

		e1 := decTab[x1&4095]
		dst[i+1] = byte(e1)
		x1 = ((e1>>20)+1)*(x1>>12) + ((e1 >> 8) & 0xFFF)
		for x1 < 65536 {
			if p >= srcLen {
				return ErrCorruptedFrame
			}
			x1 = (x1 << 8) | uint32(src[p])
			p++
		}

		e2 := decTab[x2&4095]
		dst[i+2] = byte(e2)
		x2 = ((e2>>20)+1)*(x2>>12) + ((e2 >> 8) & 0xFFF)
		for x2 < 65536 {
			if p >= srcLen {
				return ErrCorruptedFrame
			}
			x2 = (x2 << 8) | uint32(src[p])
			p++
		}

		e3 := decTab[x3&4095]
		dst[i+3] = byte(e3)
		x3 = ((e3>>20)+1)*(x3>>12) + ((e3 >> 8) & 0xFFF)
		for x3 < 65536 {
			if p >= srcLen {
				return ErrCorruptedFrame
			}
			x3 = (x3 << 8) | uint32(src[p])
			p++
		}

		i += 4
	}

	x[0], x[1], x[2], x[3] = x0, x1, x2, x3
	for i < uLen {
		lane := i & 3
		e := decTab[x[lane]&4095]
		dst[i] = byte(e)
		x[lane] = ((e>>20)+1)*(x[lane]>>12) + ((e >> 8) & 0xFFF)

		for x[lane] < 65536 {
			if p >= srcLen {
				return ErrCorruptedFrame
			}
			x[lane] = (x[lane] << 8) | uint32(src[p])
			p++
		}
		i++
	}
	return nil
}

// PackNibblePlanes formats literals into SIMD nibble planes.
func PackNibblePlanes(src []byte) []byte {
	n := len(src)
	nLow := (n + 1) / 2
	rleTokens := encodeHighNibbleRLE(src)
	out := make([]byte, 8+nLow, 8+nLow+len(rleTokens))
	binary.LittleEndian.PutUint32(out[0:4], uint32(n))
	binary.LittleEndian.PutUint32(out[4:8], uint32(nLow))

	for j := range nLow {
		p0 := src[2*j] & 0x0F
		var p1 byte
		if 2*j+1 < n {
			p1 = src[2*j+1] & 0x0F
		}
		out[8+j] = (p0 << 4) | p1
	}

	return append(out, rleTokens...)
}

func encodeHighNibbleRLE(src []byte) []byte {
	var tokens []byte
	n := len(src)
	i := 0
	for i < n {
		h := (src[i] >> 4) & 0x0F
		run := 1
		for i+run < n && run < 16 && ((src[i+run]>>4)&0x0F) == h {
			run++
		}
		tok := byte(((run - 1) << 4) | int(h))
		tokens = append(tokens, tok)
		i += run
	}
	return tokens
}

// UnpackNibblePlanes restores literals from SIMD low and high nibble planes.
func UnpackNibblePlanes(src, dst []byte, uLen int) error {
	if len(src) < 8 {
		return ErrCorruptedFrame
	}
	n := int(binary.LittleEndian.Uint32(src[0:4]))
	nLow := int(binary.LittleEndian.Uint32(src[4:8]))
	if n != uLen || nLow != (n+1)/2 || len(dst) < n {
		return ErrCorruptedFrame
	}
	if 8+nLow > len(src) {
		return ErrCorruptedFrame
	}

	if err := unpackHighNibbleRLE(src[8+nLow:], dst, n); err != nil {
		return err
	}
	mergeLowNibbles(src[8:8+nLow], dst, n, nLow)
	return nil
}

func unpackHighNibbleRLE(rleSrc, dst []byte, n int) error {
	written := 0
	pos := 0
	for written < n && pos < len(rleSrc) {
		tok := rleSrc[pos]
		pos++
		rLen := int(tok>>4) + 1
		h := (tok & 0x0F) << 4
		if written+rLen > n {
			rLen = n - written
		}
		for k := 0; k < rLen; k++ {
			dst[written+k] = h
		}
		written += rLen
	}
	if written != n {
		return ErrCorruptedFrame
	}
	return nil
}

func mergeLowNibbles(lowSrc, dst []byte, n, nLow int) {
	for j := range nLow {
		pair := lowSrc[j]
		dst[2*j] |= (pair >> 4)
		if 2*j+1 < n {
			dst[2*j+1] |= (pair & 0x0F)
		}
	}
}

// PackEtaleSheaf wraps partitioned literal fibers into the canonical 40-byte header.
func PackEtaleSheaf(src []byte, mode uint8) ([]byte, error) {
	fibers, err := DecomposeFibers(src, mode)
	if err != nil {
		return nil, err
	}
	uLit := len(src)
	var l [4]int
	for k := range 4 {
		l[k] = len(fibers[k])
	}

	var encodedFibers [4][]byte
	var e [4]int
	for k := range 4 {
		if l[k] == 0 {
			e[k] = 0
			continue
		}
		enc, encErr := tryEncodeFiber(fibers[k])
		if encErr == nil && len(enc) < l[k] {
			encodedFibers[k] = enc
			e[k] = len(enc)
		} else {
			encodedFibers[k] = fibers[k]
			e[k] = l[k]
		}
	}

	return assembleSheafContainer(uLit, mode, l, e, encodedFibers), nil
}

func tryEncodeFiber(fiber []byte) ([]byte, error) {
	counts := make([]int, 256)
	for _, b := range fiber {
		counts[b]++
	}
	var freq [256]uint16
	if err := NormalizeFrequencies(counts, freq[:]); err != nil {
		return nil, err
	}
	return EncodeVANS4x(fiber, freq[:])
}

func assembleSheafContainer(uLit int, mode uint8, l, e [4]int, encodedFibers [4][]byte) []byte {
	out := make([]byte, 40)
	out[0] = 'V'
	out[1] = 'A'
	out[2] = 0x01
	out[3] = VansFlag4X | VansFlagCtxSheaf | ((mode & 0x0F) << 4)
	binary.LittleEndian.PutUint32(out[4:8], uint32(uLit))
	for k := range 4 {
		binary.LittleEndian.PutUint32(out[8+k*4:12+k*4], uint32(l[k]))
		binary.LittleEndian.PutUint32(out[24+k*4:28+k*4], uint32(e[k]))
	}
	for k := range 4 {
		if e[k] > 0 {
			out = append(out, encodedFibers[k]...)
		}
	}
	return out
}

// PackageLiteralsSection evaluates single-stream V-ANS vs Étale Sheaf foliation vs verbatim fallback.
func PackageLiteralsSection(lits []byte) ([]byte, uint8) {
	if len(lits) == 0 {
		return nil, 0
	}
	uLit := len(lits)
	var bestPacked []byte
	minLen := uLit

	vansDst := make([]byte, 8+600+16+uLit*2)
	n, err := EncodeAdaptiveVANS4x(lits, vansDst)
	if err == nil && n < minLen {
		minLen = n
		bestPacked = vansDst[:n]
	}

	for mode := uint8(0); mode <= 3; mode++ {
		packed, err := PackEtaleSheaf(lits, mode)
		if err == nil && len(packed) < minLen {
			minLen = len(packed)
			bestPacked = packed
		}
	}

	if bestPacked != nil {
		out := make([]byte, len(bestPacked))
		copy(out, bestPacked)
		return out, FlagNibble
	}
	return lits, 0
}

// UnpackSection4Literals handles container dispatch for Section 4 literals.
func UnpackSection4Literals(s4 []byte, l4 int, litsBuf, fiberBuf []byte) ([]byte, error) {
	if l4 == 0 {
		return nil, nil
	}
	s := getScratch()
	defer putScratch(s)

	if l4 >= 8 && s4[0] == 'V' && s4[1] == 'A' && s4[2] == 0x01 {
		flags := s4[3]
		uLen := int(binary.LittleEndian.Uint32(s4[4:8]))
		if uLen > len(litsBuf) {
			return nil, ErrBufferTooSmall
		}
		if (flags & VansFlagCtxSheaf) != 0 {
			if err := unpackEtaleSheaf(s4[:l4], litsBuf[:uLen], uLen, fiberBuf, s); err != nil {
				return nil, err
			}
			return litsBuf[:uLen], nil
		}
		if err := DecodeVANS4xDirect(
			s4[8:l4],
			litsBuf[:uLen],
			uLen,
			s,
		); err != nil {
			return nil, err
		}
		return litsBuf[:uLen], nil
	}
	if l4 >= 8 && binary.LittleEndian.Uint32(s4[4:8]) == (binary.LittleEndian.Uint32(s4[0:4])+1)/2 {
		nLits := int(binary.LittleEndian.Uint32(s4[0:4]))
		if nLits > len(litsBuf) {
			return nil, ErrBufferTooSmall
		}
		if err := UnpackNibblePlanes(s4[:l4], litsBuf[:nLits], nLits); err != nil {
			return nil, err
		}
		return litsBuf[:nLits], nil
	}
	return s4[:l4], nil
}

// UnpackEtaleSheaf unpacks an Étale Sheaf container from s4 into dst.
func UnpackEtaleSheaf(s4, dst []byte, uLit int) error {
	s := scratchPool.Get()
	defer scratchPool.Put(s)
	_, err := UnpackSection4Literals(s4, len(s4), dst, s.fiberBuf[:])
	return err
}

func unpackEtaleSheaf(s4, dst []byte, uLit int, fiberBuf []byte, s *Scratchpad) error {
	if len(s4) < 40 {
		return ErrCorruptedFrame
	}
	flags := s4[3]
	mode := (flags >> 4) & 0x0F
	if mode > 3 {
		return ErrCorruptedFrame
	}

	var l, e [4]int
	var sumL int
	for k := range 4 {
		l[k] = int(binary.LittleEndian.Uint32(s4[8+k*4 : 12+k*4]))
		e[k] = int(binary.LittleEndian.Uint32(s4[24+k*4 : 28+k*4]))
		sumL += l[k]
	}
	if sumL != uLit || uLit > len(fiberBuf) {
		return ErrCorruptedFrame
	}
	if mode == 2 && (l[2] != 0 || l[3] != 0) {
		return ErrCorruptedFrame
	}

	return validateAndDecodeFibers(s4, dst, uLit, mode, l, e, fiberBuf, s)
}

func validateAndDecodeFibers(s4, dst []byte, uLit int, mode uint8, l, e [4]int, fiberBuf []byte, s *Scratchpad) error {
	pos := 40
	for k := range 4 {
		if l[k] == 0 && e[k] != 0 {
			return ErrCorruptedFrame
		}
		if e[k] > l[k] || pos+e[k] > len(s4) {
			return ErrCorruptedFrame
		}
		pos += e[k]
	}

	return decodeAndReconstituteFibers(s4, dst, uLit, mode, l, e, fiberBuf, s)
}

func decodeAndReconstituteFibers(
	s4, dst []byte,
	uLit int,
	mode uint8,
	l, e [4]int,
	fiberBuf []byte,
	s *Scratchpad,
) error {
	pos := 40
	fiberDstOffset := 0
	var fiberSlices [4][]byte

	for k := range 4 {
		if l[k] == 0 {
			fiberSlices[k] = nil
			continue
		}
		sub := s4[pos : pos+e[k]]
		pos += e[k]
		outFiber := fiberBuf[fiberDstOffset : fiberDstOffset+l[k]]
		fiberDstOffset += l[k]

		if e[k] == l[k] {
			copy(outFiber, sub)
		} else {
			if err := DecodeVANS4xDirect(sub, outFiber, l[k], s); err != nil {
				return err
			}
		}
		fiberSlices[k] = outFiber
	}

	return ReconstituteFibers(dst, uLit, fiberSlices[0], fiberSlices[1], fiberSlices[2], fiberSlices[3],
		l[0], l[1], l[2], l[3], mode)
}
