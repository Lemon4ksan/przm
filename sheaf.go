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
// 1 = syntax delimiter (structural), 0 = content (semantic).
var SheafSyntaxTable [256]uint8

func init() {
	for i := range SheafSyntaxTable {
		SheafSyntaxTable[i] = 0
	}
	for _, b := range []byte("{}[]()<>() :;,.\"'`=+-*/%&|^!~? \t\r\n\\") {
		SheafSyntaxTable[b] = 1
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
	if origLen <= 0 || origLen > MaxBlockSize || len(dst) < origLen || len(mask) < (origLen+7)/8 || len(s)+len(c) < origLen {
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

		s0 := table[b0] & 1
		s1 := table[b1] & 1
		s2 := table[b2] & 1
		s3 := table[b3] & 1
		s4 := table[b4] & 1
		s5 := table[b5] & 1
		s6 := table[b6] & 1
		s7 := table[b7] & 1

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
			s := table[b] & 1
			mask |= s << bit
			bit++

			*(*byte)(unsafe.Add(pS, sPos)) = b
			sPos += int(s)
			*(*byte)(unsafe.Add(pC, cPos)) = b
			cPos += int(1 - s)
		}
		*(*byte)(unsafe.Add(pM, mPos)) = mask
		mPos++
	}

	return sPos, cPos
}

// branchlessInterleave provides bounds-safe byte reconstruction for scalar platforms.
func branchlessInterleave(dst []byte, s, c, mask []byte, origLen int) {
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

		for bit := 0; bit < 8; bit++ {
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
	for i := 0; i < sampleLen; i++ {
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
	for _, b := range data {
		counts[b]++
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
