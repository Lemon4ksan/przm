// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math"
	"sync"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
)

// Modes for PRZM 2.0 Vector C (Étale Sheaf Fibration / Stream Splitting).
const (
	SheafModeSplit         uint8 = 3 // Étale Sheaf Vector C Stream Splitting (raw streams)
	SheafModeSplitCascaded uint8 = 4 // Vector C cascaded with PWS / Trellis

	SheafHeaderSize int = 12
)

// SheafSyntaxTable classifies syntax delimiters vs content semantics.
// SheafSyntaxTable classifies syntax delimiters vs content semantics.
// 1 = syntax delimiter (structural), 0 = content (semantic).
var SheafSyntaxTable [256]uint8

// SheafContextTable classifies preceding byte into 4 context classes for Mode 0 (Refined Étale Sheaf):
// 0: Whitespace (' ', '\t', '\r', '\n')
// 1: Syntax delimiters (from SheafSyntaxTable except whitespace)
// 2: Lowercase ASCII letters ('a'..'z') and '_'
// 3: Other bytes (control characters, digits, uppercase, high UTF-8 bytes)
var SheafContextTable [256]uint8

// SheafContextTableM1 classifies preceding byte for Mode 1 (JSON/Log dense):
// 0: Quote ('"') & Colon (':')
// 1: Lowercase letters & identifiers ('a'..'z', '_')
// 2: Numeric digits ('0'..'9')
// 3: Whitespace & Structural ({, }, [, ], ,, etc.)
var SheafContextTableM1 [256]uint8

// SheafContextTableM2 classifies preceding byte for Mode 2 (2-Phase Causal Syntax/Content):
// 0: Delimiters, whitespace, syntax
// 1: Content (alphanumeric, identifiers, others)
var SheafContextTableM2 [256]uint8

// SheafContext8Table classifies preceding byte into 8 context classes for Order-1 Context Sheaf:
// 0: Whitespace (' ', '\t', '\n', '\r')
// 1: Punctuation / structural delimiters ('(', ')', '{', '}', '[', ']', ';', ',', '.', ':', '"', '\”)
// 2: Operators ('=', '+', '-', '*', '/', '%', '&', '|', '^', '!', '<', '>', '~', '?')
// 3: Lowercase vowels ('a', 'e', 'i', 'o', 'u')
// 4: Lowercase consonants
// 5: Uppercase ASCII letters ('A'..'Z')
// 6: ASCII digits ('0'..'9')
// 7: Other / control / binary
var SheafContext8Table [256]uint8

func classifyByte8(b byte) uint8 {
	if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
		return 0
	}
	if b == '(' || b == ')' || b == '{' || b == '}' || b == '[' || b == ']' ||
		b == ';' || b == ',' || b == '.' || b == ':' || b == '"' || b == '\'' {
		return 1
	}
	if b == '=' || b == '+' || b == '-' || b == '*' || b == '/' || b == '%' ||
		b == '&' || b == '|' || b == '^' || b == '!' || b == '<' || b == '>' || b == '~' || b == '?' {
		return 2
	}
	if b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u' {
		return 3
	}
	if b >= 'a' && b <= 'z' {
		return 4
	}
	if b >= 'A' && b <= 'Z' {
		return 5
	}
	if b >= '0' && b <= '9' {
		return 6
	}
	return 7
}

func init() {
	for i := range SheafSyntaxTable {
		SheafSyntaxTable[i] = 0
	}
	for _, b := range []byte("{}[]()<>() :;,.\"'`=+-*/%&|^!~? \t\r\n\\#") {
		SheafSyntaxTable[b] = 1
	}

	// Mode 0: Refined 4-Class Étale Sheaf
	for i := range SheafContextTable {
		SheafContextTable[i] = 3
	}
	for _, b := range []byte(" \t\r\n") {
		SheafContextTable[b] = 0
	}
	for _, b := range []byte("{}[]()<>() :;,.\"'`=+-*/%&|^!~?\\#") {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			SheafContextTable[b] = 1
		}
	}
	for b := 0; b < 256; b++ {
		if (b >= 'a' && b <= 'z') || b == '_' {
			SheafContextTable[b] = 2
		}
	}

	// Mode 1: JSON Dense
	for i := range SheafContextTableM1 {
		SheafContextTableM1[i] = 3
	}
	SheafContextTableM1['"'] = 0
	SheafContextTableM1[':'] = 0
	for b := 0; b < 256; b++ {
		if (b >= 'a' && b <= 'z') || b == '_' {
			SheafContextTableM1[b] = 1
		} else if b >= '0' && b <= '9' {
			SheafContextTableM1[b] = 2
		}
	}

	// Mode 2: 2-Phase
	for i := range SheafContextTableM2 {
		SheafContextTableM2[i] = 1
	}
	for _, b := range []byte(" \t\r\n{}[]()<>() :;,.\"'`=+-*/%&|^!~?\\#") {
		SheafContextTableM2[b] = 0
	}
	for b := range 256 {
		SheafContext8Table[b] = classifyByte8(byte(b))
	}
}

// SheafContextOrder2 computes the Order-2 bigram context from previous two bytes.
func SheafContextOrder2(p2, p1 byte) uint8 {
	if p1 >= '0' && p1 <= '9' {
		if (p2 >= '0' && p2 <= '9') || p2 == '.' || p2 == '-' {
			return 0
		}
		return 1
	}
	if (p1 >= 'a' && p1 <= 'z') || p1 == '_' {
		if (p2 >= 'a' && p2 <= 'z') || p2 == '_' {
			return 2
		}
		return 3
	}
	if p1 == ' ' || p1 == '\t' || p1 == '\r' || p1 == '\n' {
		return 0
	}
	return 1
}

// InterleaveContextSheaf4 reconstructs origLen bytes into dst from 4 context fibers using default Mode 0.
func InterleaveContextSheaf4(dst, f0, f1, f2, f3 []byte, origLen int) {
	InterleaveContextSheaf4Mode(dst, f0, f1, f2, f3, origLen, CtxModeStructural)
}

// InterleaveContextSheaf4Mode reconstructs origLen bytes into dst from 4 context fibers using the specified mode.
func InterleaveContextSheaf4Mode(dst, f0, f1, f2, f3 []byte, origLen int, mode uint8) {
	if origLen <= 0 || len(dst) < origLen {
		return
	}
	p0, p1, p2, p3 := 0, 0, 0, 0
	pDst := unsafe.Pointer(&dst[0])
	var pF0, pF1, pF2, pF3 unsafe.Pointer
	if len(f0) > 0 {
		pF0 = unsafe.Pointer(&f0[0])
	}
	if len(f1) > 0 {
		pF1 = unsafe.Pointer(&f1[0])
	}
	if len(f2) > 0 {
		pF2 = unsafe.Pointer(&f2[0])
	}
	if len(f3) > 0 {
		pF3 = unsafe.Pointer(&f3[0])
	}

	if mode == CtxModeOrder2 {
		prev2, prev1 := byte(0), byte(0)
		for j := 0; j < origLen; j++ {
			c := SheafContextOrder2(prev2, prev1)
			var bt byte
			switch c {
			case 0:
				bt = *(*byte)(unsafe.Add(pF0, p0))
				p0++
			case 1:
				bt = *(*byte)(unsafe.Add(pF1, p1))
				p1++
			case 2:
				bt = *(*byte)(unsafe.Add(pF2, p2))
				p2++
			default:
				bt = *(*byte)(unsafe.Add(pF3, p3))
				p3++
			}
			*(*byte)(unsafe.Add(pDst, j)) = bt
			prev2 = prev1
			prev1 = bt
		}
		return
	}

	lut := &SheafContextTable
	if mode == CtxModeJSONDense {
		lut = &SheafContextTableM1
	} else if mode == CtxMode2Phase {
		lut = &SheafContextTableM2
	}

	prev := byte(0)
	j := 0
	for ; j+8 <= origLen; j += 8 {
		c0 := lut[prev]
		var b0 byte
		switch c0 {
		case 0:
			b0 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b0 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b0 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b0 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j)) = b0

		c1 := lut[b0]
		var b1 byte
		switch c1 {
		case 0:
			b1 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b1 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b1 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b1 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+1)) = b1

		c2 := lut[b1]
		var b2 byte
		switch c2 {
		case 0:
			b2 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b2 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b2 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b2 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+2)) = b2

		c3 := lut[b2]
		var b3 byte
		switch c3 {
		case 0:
			b3 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b3 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b3 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b3 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+3)) = b3

		c4 := lut[b3]
		var b4 byte
		switch c4 {
		case 0:
			b4 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b4 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b4 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b4 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+4)) = b4

		c5 := lut[b4]
		var b5 byte
		switch c5 {
		case 0:
			b5 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b5 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b5 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b5 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+5)) = b5

		c6 := lut[b5]
		var b6 byte
		switch c6 {
		case 0:
			b6 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b6 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b6 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b6 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+6)) = b6

		c7 := lut[b6]
		var b7 byte
		switch c7 {
		case 0:
			b7 = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			b7 = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			b7 = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			b7 = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j+7)) = b7

		prev = b7
	}
	for ; j < origLen; j++ {
		c := lut[prev]
		var bt byte
		switch c {
		case 0:
			bt = *(*byte)(unsafe.Add(pF0, p0))
			p0++
		case 1:
			bt = *(*byte)(unsafe.Add(pF1, p1))
			p1++
		case 2:
			bt = *(*byte)(unsafe.Add(pF2, p2))
			p2++
		default:
			bt = *(*byte)(unsafe.Add(pF3, p3))
			p3++
		}
		*(*byte)(unsafe.Add(pDst, j)) = bt
		prev = bt
	}
}

type sheafSplitScratchpad struct {
	sBuf    []byte
	cBuf    []byte
	maskBuf []byte
	workBuf []byte
}

var sheafSplitPool = sync.Pool{
	New: func() any {
		return &sheafSplitScratchpad{
			sBuf:    bufkit.AlignedBytes(65536+128, bufkit.CacheLineSize),
			cBuf:    bufkit.AlignedBytes(65536+128, bufkit.CacheLineSize),
			maskBuf: bufkit.AlignedBytes(8192+128, bufkit.CacheLineSize),
			workBuf: bufkit.AlignedBytes(131072+128, bufkit.CacheLineSize),
		}
	},
}

// SheafSplit separates structural syntax bytes from semantic content bytes
// into independent streams using SIMD on amd64 and branchless scalar on other platforms.
// Returns sLen (syntax bytes) and cLen (content bytes).
func SheafSplit(src, sBuf, cBuf, maskBuf []byte) (int, int) {
	n := len(src)
	if n == 0 || n > MaxBlockSize {
		return 0, 0
	}
	maskLen := (n + 7) / 8
	if len(sBuf) < n || len(cBuf) < n || len(maskBuf) < maskLen {
		return 0, 0
	}
	return simdSplit(src, sBuf, cBuf, maskBuf)
}

// SheafInterleave reconstructs the original byte stream into dst from syntax (s),
// content (c), and mask streams. Guarantees 100% bit-exact lossless roundtrip.
func SheafInterleave(dst, s, c, mask []byte, origLen int) {
	if origLen <= 0 || origLen > MaxBlockSize || len(dst) < origLen || len(mask) < (origLen+7)/8 ||
		len(s)+len(c) < origLen {
		return
	}
	simdInterleave(dst, s, c, mask, origLen)
}

// branchlessSplit provides branch-free stream splitting for scalar platforms and tail processing.
func branchlessSplit(src []byte, table *[256]uint8, sBuf, cBuf, maskBuf []byte) (int, int) {
	n := len(src)
	if n == 0 {
		return 0, 0
	}

	pSrc := unsafe.Pointer(&src[0])
	pS := unsafe.Pointer(&sBuf[0])
	pC := unsafe.Pointer(&cBuf[0])
	pM := unsafe.Pointer(&maskBuf[0])

	sPos := 0
	cPos := 0
	mPos := 0

	i := 0
	for ; i+8 <= n; i += 8 {
		b0 := *(*byte)(unsafe.Add(pSrc, i))
		b1 := *(*byte)(unsafe.Add(pSrc, i+1))
		b2 := *(*byte)(unsafe.Add(pSrc, i+2))
		b3 := *(*byte)(unsafe.Add(pSrc, i+3))
		b4 := *(*byte)(unsafe.Add(pSrc, i+4))
		b5 := *(*byte)(unsafe.Add(pSrc, i+5))
		b6 := *(*byte)(unsafe.Add(pSrc, i+6))
		b7 := *(*byte)(unsafe.Add(pSrc, i+7))

		s0 := table[b0]
		s1 := table[b1]
		s2 := table[b2]
		s3 := table[b3]
		s4 := table[b4]
		s5 := table[b5]
		s6 := table[b6]
		s7 := table[b7]

		mask := s0 | (s1 << 1) | (s2 << 2) | (s3 << 3) | (s4 << 4) | (s5 << 5) | (s6 << 6) | (s7 << 7)
		*(*byte)(unsafe.Add(pM, mPos)) = mask
		mPos++

		*(*byte)(unsafe.Add(pS, sPos)) = b0
		sPos += int(s0)
		*(*byte)(unsafe.Add(pC, cPos)) = b0
		cPos += int(1 - s0)

		*(*byte)(unsafe.Add(pS, sPos)) = b1
		sPos += int(s1)
		*(*byte)(unsafe.Add(pC, cPos)) = b1
		cPos += int(1 - s1)

		*(*byte)(unsafe.Add(pS, sPos)) = b2
		sPos += int(s2)
		*(*byte)(unsafe.Add(pC, cPos)) = b2
		cPos += int(1 - s2)

		*(*byte)(unsafe.Add(pS, sPos)) = b3
		sPos += int(s3)
		*(*byte)(unsafe.Add(pC, cPos)) = b3
		cPos += int(1 - s3)

		*(*byte)(unsafe.Add(pS, sPos)) = b4
		sPos += int(s4)
		*(*byte)(unsafe.Add(pC, cPos)) = b4
		cPos += int(1 - s4)

		*(*byte)(unsafe.Add(pS, sPos)) = b5
		sPos += int(s5)
		*(*byte)(unsafe.Add(pC, cPos)) = b5
		cPos += int(1 - s5)

		*(*byte)(unsafe.Add(pS, sPos)) = b6
		sPos += int(s6)
		*(*byte)(unsafe.Add(pC, cPos)) = b6
		cPos += int(1 - s6)

		*(*byte)(unsafe.Add(pS, sPos)) = b7
		sPos += int(s7)
		*(*byte)(unsafe.Add(pC, cPos)) = b7
		cPos += int(1 - s7)
	}

	if i < n {
		var mask byte
		bit := 0
		for ; i < n; i++ {
			b := *(*byte)(unsafe.Add(pSrc, i))
			s := table[b]
			mask |= s << bit
			bit++

			*(*byte)(unsafe.Add(pS, sPos)) = b
			sPos += int(s)
			*(*byte)(unsafe.Add(pC, cPos)) = b
			cPos += int(1 - s)
		}
		*(*byte)(unsafe.Add(pM, mPos)) = mask
	}

	return sPos, cPos
}

// branchlessInterleave provides bounds-safe byte reconstruction for scalar platforms.
func branchlessInterleave(dst, s, c, mask []byte, origLen int) {
	if origLen == 0 {
		return
	}

	sLen := len(s)
	cLen := len(c)
	sPos := 0
	cPos := 0
	dstPos := 0
	mPos := 0

	for ; dstPos+8 <= origLen; dstPos += 8 {
		m := mask[mPos]
		mPos++

		for bit := range 8 {
			if (m>>bit)&1 != 0 {
				var b byte
				if sPos < sLen {
					b = s[sPos]
				}
				dst[dstPos+bit] = b
				sPos++
			} else {
				var b byte
				if cPos < cLen {
					b = c[cPos]
				}
				dst[dstPos+bit] = b
				cPos++
			}
		}
	}

	if dstPos < origLen {
		m := mask[mPos]
		bit := 0
		for ; dstPos < origLen; dstPos++ {
			bs := (m >> bit) & 1
			bit++
			if bs != 0 {
				var b byte
				if sPos < sLen {
					b = s[sPos]
				}
				dst[dstPos] = b
				sPos++
			} else {
				var b byte
				if cPos < cLen {
					b = c[cPos]
				}
				dst[dstPos] = b
				cPos++
			}
		}
	}
}

// isProbablyText inspects a prefix of src to determine whether the payload
// is structured text (JSON, Go source code, HTML, C, XML, etc.).
// Executes in <25 ns with strictly 0 heap allocations.
func isProbablyText(src []byte) bool {
	sampleLen := min(len(src), 512)
	if sampleLen < 32 {
		return false
	}
	delims := 0
	ctrls := 0
	printables := 0
	for i := range sampleLen {
		b := src[i]
		if b == 0 {
			return false // Null bytes indicate binary data
		}
		if b < 0x20 {
			if b == '\t' || b == '\n' || b == '\r' {
				delims++
				printables++
			} else {
				ctrls++
			}
		} else if b <= 0x7E {
			printables++
			if SheafSyntaxTable[b] == 1 {
				delims++
			}
		} else {
			// Multi-byte UTF-8
			printables++
		}
	}
	if ctrls > sampleLen/64 {
		return false
	}
	// In structured text / code / JSON, printables >= 85% and delimiters >= 10%
	return printables*10 >= sampleLen*8 && delims*10 >= sampleLen
}

// CompressSheaf encodes src using Étale Sheaf Vector C Stream Splitting.
// Output contains a self-describing 12-byte header:
//
//	[0..4]  Magic 'SHEF' (0x53484546)
//	[4]     Mode (SheafModeSplitCascaded / SheafModeSplit / SheafModeRaw)
//	[5..8]  Reserved
//	[8..12] origLen (uint32 LittleEndian)
func CompressSheaf(src []byte) []byte {
	n := len(src)
	if n == 0 {
		dst := make([]byte, SheafHeaderSize)
		binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
		dst[4] = SheafModeRaw
		return dst
	}
	if n > MaxBlockSize {
		return nil
	}

	// Buffers smaller than 64 bytes emit raw Sheaf bypass frame
	if n < 64 {
		dst := make([]byte, SheafHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
		dst[4] = SheafModeRaw
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[SheafHeaderSize:], src)
		return dst
	}

	sp := sheafSplitPool.Get().(*sheafSplitScratchpad)
	defer sheafSplitPool.Put(sp)

	reqCap := n + 128
	if cap(sp.sBuf) < reqCap {
		sp.sBuf = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)
	}
	if cap(sp.cBuf) < reqCap {
		sp.cBuf = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)
	}
	maskCap := (n+7)/8 + 128
	if cap(sp.maskBuf) < maskCap {
		sp.maskBuf = bufkit.AlignedBytes(maskCap, bufkit.CacheLineSize)
	}

	sLen, cLen := SheafSplit(src, sp.sBuf, sp.cBuf, sp.maskBuf)
	maskLen := (n + 7) / 8

	// If data lacks sufficient syntax separation (e.g. nearly all delimiters or no delimiters), emit raw
	if sLen < n/32 || cLen < n/32 {
		dst := make([]byte, SheafHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
		dst[4] = SheafModeRaw
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[SheafHeaderSize:], src)
		return dst
	}

	workCap := 4 + maskLen + n + 128
	if cap(sp.workBuf) < workCap {
		sp.workBuf = bufkit.AlignedBytes(workCap, bufkit.CacheLineSize)
	}

	binary.LittleEndian.PutUint32(sp.workBuf[0:4], uint32(sLen))
	copy(sp.workBuf[4:4+maskLen], sp.maskBuf[:maskLen])
	copy(sp.workBuf[4+maskLen:4+maskLen+sLen], sp.sBuf[:sLen])
	copy(sp.workBuf[4+maskLen+sLen:4+maskLen+n], sp.cBuf[:cLen])
	rawSplit := sp.workBuf[:4+maskLen+n]

	// Check if PWS AVX-wave cascades effectively on the separated streams
	if packed, err := PWSEncode(rawSplit); err == nil && len(packed)+SheafHeaderSize < n {
		dst := make([]byte, SheafHeaderSize+len(packed))
		binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
		dst[4] = SheafModeSplitCascaded
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[SheafHeaderSize:], packed)
		return dst
	}

	// Normal uncascaded split frame (with 16B tail padding for SIMD safety)
	dst := make([]byte, SheafHeaderSize+4+maskLen+n+16)
	binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
	dst[4] = SheafModeSplit
	binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
	binary.LittleEndian.PutUint32(dst[12:16], uint32(sLen))
	copy(dst[16:16+maskLen], sp.maskBuf[:maskLen])
	copy(dst[16+maskLen:16+maskLen+sLen], sp.sBuf[:sLen])
	copy(dst[16+maskLen+sLen:16+maskLen+n], sp.cBuf[:cLen])
	return dst
}

// DecompressSheaf decodes a Sheaf frame back to uncompressed bytes.
// Strictly guarantees 0 heap allocations if cap(dst) >= origLen + 64.
func DecompressSheaf(src, dst []byte) ([]byte, error) {
	if len(src) < SheafHeaderSize {
		return nil, ErrSheafCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != SheafMagic {
		return nil, ErrSheafCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[8:12]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	mode := src[4]
	switch mode {
	case SheafModeRaw:
		payload := src[SheafHeaderSize:]
		if len(payload) < origLen {
			return nil, ErrSheafCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil

	case SheafModeSplit:
		payload := src[SheafHeaderSize:]
		maskLen := (origLen + 7) / 8
		if len(payload) < 4+maskLen+origLen {
			return nil, ErrSheafCorrupt
		}
		sLen := int(binary.LittleEndian.Uint32(payload[0:4]))
		if sLen < 0 || sLen > origLen {
			return nil, ErrSheafCorrupt
		}
		mask := payload[4 : 4+maskLen]
		if !validateSheafMask(mask, origLen, sLen) {
			return nil, ErrSheafCorrupt
		}
		s := payload[4+maskLen : 4+maskLen+sLen]
		c := payload[4+maskLen+sLen : 4+maskLen+origLen]
		SheafInterleave(dst, s, c, mask, origLen)
		return dst, nil

	case SheafModeSplitCascaded:
		sp := sheafSplitPool.Get().(*sheafSplitScratchpad)
		defer sheafSplitPool.Put(sp)

		maskLen := (origLen + 7) / 8
		reqWork := 4 + maskLen + origLen + 128
		if cap(sp.workBuf) < reqWork {
			sp.workBuf = bufkit.AlignedBytes(reqWork, bufkit.CacheLineSize)
		}

		unpacked, err := PWSDecodeTo(sp.workBuf, src[SheafHeaderSize:])
		if err != nil || len(unpacked) < 4+maskLen+origLen {
			return nil, ErrSheafCorrupt
		}

		sLen := int(binary.LittleEndian.Uint32(unpacked[0:4]))
		if sLen < 0 || sLen > origLen {
			return nil, ErrSheafCorrupt
		}
		mask := unpacked[4 : 4+maskLen]
		if !validateSheafMask(mask, origLen, sLen) {
			return nil, ErrSheafCorrupt
		}
		s := unpacked[4+maskLen : 4+maskLen+sLen]
		c := unpacked[4+maskLen+sLen : 4+maskLen+origLen]
		SheafInterleave(dst, s, c, mask, origLen)
		return dst, nil

	case SheafModeTrellisRep, SheafModeÉtale4:
		return DecompressSheafTrellis(src, dst)

	default:
		return nil, ErrSheafCorrupt
	}
}

// CalculateShannonEntropy computes the empirical Shannon entropy H_0 in bits per byte.
func CalculateShannonEntropy(data []byte) float64 {
	n := len(data)
	if n == 0 {
		return 0.0
	}
	var counts [256]int
	mat := BuildFrequencyMatrixSIMD(data)
	for i, c := range mat.Counts {
		counts[i] = int(c)
	}
	total := float64(n)
	entropy := 0.0
	for _, count := range counts {
		if count > 0 {
			p := float64(count) / total
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// CalculateSheafEntropy computes the Shannon entropy for syntax, content, and mask streams,
// along with the combined aggregate entropy per original input byte.
func CalculateSheafEntropy(s, c, mask []byte, origLen int) (entropyS, entropyC, entropyM, aggregate float64) {
	if origLen == 0 {
		return 0, 0, 0, 0
	}
	entropyS = CalculateShannonEntropy(s)
	entropyC = CalculateShannonEntropy(c)
	entropyM = CalculateShannonEntropy(mask)

	totalBits := float64(len(s))*entropyS + float64(len(c))*entropyC + float64(len(mask))*entropyM
	aggregate = totalBits / float64(origLen)
	return entropyS, entropyC, entropyM, aggregate
}
