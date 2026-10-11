// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"math/bits"
	"unsafe"

	"github.com/lemon4ksan/foundation/generic"
	"github.com/lemon4ksan/foundation/silicon/simd"
)

const (
	// Legacy 64 KiB constants (retained for backward compatibility)
	HenselWindowSize = 1 << WindowLog64K // 65,536
	HenselWindowMask = HenselWindowSize - 1
	HenselHashBits   = 16
	HenselHashSize   = 1 << HenselHashBits // 65,536
	HenselHashMask   = HenselHashSize - 1
	HenselHashPrime  = 0x1E35A7BD

	// Mode 1M (1 MiB Window, ~9.2 MiB Scratchpad - L3 Cache Resident)
	WindowSize1M = 1 << WindowLog1M // 1,048,576
	WindowMask1M = WindowSize1M - 1
	HashBits1M   = 18
	HashSize1M   = 1 << HashBits1M // 262,144
	HashMask1M   = HashSize1M - 1

	// Mode 8M (8 MiB Window, ~68.0 MiB Scratchpad - Full Corpus Coverage)
	WindowSize8M = 1 << WindowLog8M // 8,388,608
	WindowMask8M = WindowSize8M - 1
	HashBits8M   = 20
	HashSize8M   = 1 << HashBits8M // 1,048,576
	HashMask8M   = HashSize8M - 1
)

// HenselScratchpad encapsulates the ultrametric search tables for Hensel's lifting (64 KiB).
// Total resident memory footprint: exactly 768 KiB (contiguous, cache-friendly).
type HenselScratchpad struct {
	// head stores the most recent 1-based position for each 4-byte hash (0 = empty).
	head [HenselHashSize]int32 // 256 KiB
	// prev stores the prior 1-based candidate in the hash collision chain.
	prev [HenselWindowSize]int32 // 256 KiB
	// fp32 stores the 32-bit second-half fingerprint (bytes 4..7) for 1-cycle ultrametric pruning.
	fp32 [HenselWindowSize]uint32 // 256 KiB
}

// Reset clears the hash bucket heads, re-initializing the search structure in ~1.5 µs.
func (sp *HenselScratchpad) Reset() {
	clear(sp.head[:])
}

// HeadSlice returns a slice view of the bucket heads for oracle inspection.
func (sp *HenselScratchpad) HeadSlice() []int32 {
	return sp.head[:]
}

// PrevSlice returns a slice view of the collision chains for oracle inspection.
func (sp *HenselScratchpad) PrevSlice() []int32 {
	return sp.prev[:]
}

// HenselScratchpad1M encapsulates L3-resident 1 MiB search tables (~9.2 MiB total).
type HenselScratchpad1M struct {
	head [HashSize1M]int32    // 1,024 KiB (262,144 entries)
	prev [WindowSize1M]int32  // 4,096 KiB (1,048,576 entries)
	fp32 [WindowSize1M]uint32 // 4,096 KiB (1,048,576 entries)
}

// Reset clears only the head array (~20 µs) bypassing zeroing of prev and fp32.
func (sp *HenselScratchpad1M) Reset() {
	clear(sp.head[:])
}

// HeadSlice returns a slice view of the bucket heads for oracle inspection.
func (sp *HenselScratchpad1M) HeadSlice() []int32 {
	return sp.head[:]
}

// PrevSlice returns a slice view of the collision chains for oracle inspection.
func (sp *HenselScratchpad1M) PrevSlice() []int32 {
	return sp.prev[:]
}

// HenselScratchpad8M encapsulates full-coverage 8 MiB search tables (~68.0 MiB total).
type HenselScratchpad8M struct {
	head [HashSize8M]int32    // 4,096 KiB (1,048,576 entries)
	prev [WindowSize8M]int32  // 32,768 KiB (8,388,608 entries)
	fp32 [WindowSize8M]uint32 // 32,768 KiB (8,388,608 entries)
}

// Reset clears only the head array (~80 µs) bypassing zeroing of prev and fp32.
func (sp *HenselScratchpad8M) Reset() {
	clear(sp.head[:])
}

// HeadSlice returns a slice view of the bucket heads for oracle inspection.
func (sp *HenselScratchpad8M) HeadSlice() []int32 {
	return sp.head[:]
}

// PrevSlice returns a slice view of the collision chains for oracle inspection.
func (sp *HenselScratchpad8M) PrevSlice() []int32 {
	return sp.prev[:]
}

// HenselScratchpadL6 encapsulates multi-tier ultrametric search tables for Level 6 (~69.25 MiB total).
type HenselScratchpadL6 struct {
	head24    [HashSize1M]int32    // 1,024 KiB (262,144 entries)
	head32    [HashSize8M]int32    // 4,096 KiB (1,048,576 entries)
	macroHead [65536]int32         // 256 KiB (65,536 entries)
	prev      [WindowSize8M]int32  // 32,768 KiB (8,388,608 entries)
	fp32      [WindowSize8M]uint32 // 32,768 KiB (8,388,608 entries)
}

// Reset clears only the head arrays (~100 µs) bypassing zeroing of prev and fp32.
func (sp *HenselScratchpadL6) Reset() {
	clear(sp.head24[:])
	clear(sp.head32[:])
	clear(sp.macroHead[:])
}

// HeadSlice returns a slice view of the 32-bit bucket heads for oracle inspection.
func (sp *HenselScratchpadL6) HeadSlice() []int32 {
	return sp.head32[:]
}

// PrevSlice returns a slice view of the collision chains for oracle inspection.
func (sp *HenselScratchpadL6) PrevSlice() []int32 {
	return sp.prev[:]
}

var (
	// PoolHensel64K reuses 768 KiB scratchpads
	PoolHensel64K = generic.NewPool(func() *HenselScratchpad {
		return new(HenselScratchpad)
	})

	// PoolHensel1M reuses L3-resident ~9.2 MiB scratchpads
	PoolHensel1M = generic.NewPool(func() *HenselScratchpad1M {
		return new(HenselScratchpad1M)
	})

	// PoolHensel8M reuses full-coverage ~68.0 MiB scratchpads
	PoolHensel8M = generic.NewPool(func() *HenselScratchpad8M {
		return new(HenselScratchpad8M)
	})

	// PoolHenselL6 reuses full-coverage ~69.25 MiB multi-tier scratchpads
	PoolHenselL6 = generic.NewPool(func() *HenselScratchpadL6 {
		return new(HenselScratchpadL6)
	})
)

// HenselMatchFinder provides an ultrametric match searcher using Hensel's lifting.
type HenselMatchFinder struct {
	windowSize int
	windowLog  int
	sp         HenselScratchpad
	sp1M       *HenselScratchpad1M
	sp8M       *HenselScratchpad8M
}

// NewHenselMatchFinder allocates a new match finder with the specified window size.
func NewHenselMatchFinder(windowSize int) *HenselMatchFinder {
	if windowSize <= 0 {
		windowSize = HenselWindowSize
	}
	mf := &HenselMatchFinder{
		windowSize: windowSize,
	}
	if windowSize <= HenselWindowSize {
		mf.windowLog = WindowLog64K
	} else if windowSize <= WindowSize1M {
		mf.windowLog = WindowLog1M
		mf.sp1M = new(HenselScratchpad1M)
	} else {
		mf.windowLog = WindowLog8M
		mf.sp8M = new(HenselScratchpad8M)
	}
	return mf
}

// NewHenselMatchFinderWithWindowLog allocates a new match finder with the specified window log.
func NewHenselMatchFinderWithWindowLog(windowLog int) *HenselMatchFinder {
	switch windowLog {
	case WindowLog1M:
		return NewHenselMatchFinder(WindowSize1M)
	case WindowLog8M:
		return NewHenselMatchFinder(WindowSize8M)
	default:
		return NewHenselMatchFinder(HenselWindowSize)
	}
}

// Reset clears the finder's hash tables.
func (mf *HenselMatchFinder) Reset() {
	switch mf.windowLog {
	case WindowLog1M:
		if mf.sp1M != nil {
			mf.sp1M.Reset()
		}
	case WindowLog8M:
		if mf.sp8M != nil {
			mf.sp8M.Reset()
		}
	default:
		mf.sp.Reset()
	}
}

// Insert registers a position into the Hensel search tables.
func (mf *HenselMatchFinder) Insert(pos int, src []byte, uSize int) {
	switch mf.windowLog {
	case WindowLog1M:
		if mf.sp1M != nil {
			HenselInsert1M(mf.sp1M, pos, src, uSize)
		}
	case WindowLog8M:
		if mf.sp8M != nil {
			HenselInsert8M(mf.sp8M, pos, src, uSize)
		}
	default:
		HenselInsert(&mf.sp, pos, src, uSize)
	}
}

// FindMatchHensel executes ultrametric match search for the target at pos.
func (mf *HenselMatchFinder) FindMatchHensel(src []byte, pos int, maxDepth int, minLen int) (bestOffset int, bestLen int) {
	switch mf.windowLog {
	case WindowLog1M:
		if mf.sp1M != nil {
			return HenselFindMatch1M(src, pos, len(src), maxDepth, minLen, mf.sp1M)
		}
	case WindowLog8M:
		if mf.sp8M != nil {
			return HenselFindMatch8M(src, pos, len(src), maxDepth, minLen, mf.sp8M)
		}
	default:
		return HenselFindMatch(src, pos, len(src), maxDepth, minLen, &mf.sp)
	}
	return 0, 0
}

// HenselLCP evaluates the longest common prefix between slices a and b
// using non-Archimedean p-adic valuation and quadratic Hensel lifting
// (4B -> 8B -> 16B -> 32B -> SIMD).
func HenselLCP(a, b []byte) int {
	maxLen := min(len(a), len(b))
	if maxLen <= 0 {
		return 0
	}
	ptr1 := unsafe.Pointer(unsafe.SliceData(a))
	ptr2 := unsafe.Pointer(unsafe.SliceData(b))
	if ptr1 == ptr2 {
		return maxLen
	}

	if maxLen < 8 {
		if maxLen < 4 {
			l := 0
			for l < maxLen && *(*byte)(unsafe.Add(ptr1, l)) == *(*byte)(unsafe.Add(ptr2, l)) {
				l++
			}
			return l
		}
		diff4 := *(*uint32)(ptr1) ^ *(*uint32)(ptr2)
		if diff4 != 0 {
			return bits.TrailingZeros32(diff4) >> 3
		}
		diffTail := *(*uint32)(unsafe.Add(ptr1, maxLen-4)) ^ *(*uint32)(unsafe.Add(ptr2, maxLen-4))
		if diffTail != 0 {
			return (maxLen - 4) + (bits.TrailingZeros32(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 1: 8-byte check
	diff8 := *(*uint64)(ptr1) ^ *(*uint64)(ptr2)
	if diff8 != 0 {
		return bits.TrailingZeros64(diff8) >> 3
	}
	if maxLen < 16 {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 2: 16-byte check
	diff16 := *(*uint64)(unsafe.Add(ptr1, 8)) ^ *(*uint64)(unsafe.Add(ptr2, 8))
	if diff16 != 0 {
		return 8 + (bits.TrailingZeros64(diff16) >> 3)
	}
	if maxLen < 24 {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 3: 24-byte check
	diff24 := *(*uint64)(unsafe.Add(ptr1, 16)) ^ *(*uint64)(unsafe.Add(ptr2, 16))
	if diff24 != 0 {
		return 16 + (bits.TrailingZeros64(diff24) >> 3)
	}
	if maxLen < 32 {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 4: 32-byte check
	diff32 := *(*uint64)(unsafe.Add(ptr1, 24)) ^ *(*uint64)(unsafe.Add(ptr2, 24))
	if diff32 != 0 {
		return 24 + (bits.TrailingZeros64(diff32) >> 3)
	}
	if maxLen == 32 {
		return 32
	}

	// Step 5: SIMD vector extension for bulk matches >= 32 bytes
	l := 32
	rem := maxLen - 32
	if rem >= 32 {
		s1 := unsafe.Slice((*byte)(unsafe.Add(ptr1, 32)), rem)
		s2 := unsafe.Slice((*byte)(unsafe.Add(ptr2, 32)), rem)
		vLen := simd.FindMatchLengthVector(s1, s2, rem)
		l = 32 + vLen
		if l < maxLen && *(*byte)(unsafe.Add(ptr1, l)) != *(*byte)(unsafe.Add(ptr2, l)) {
			return l
		}
	}

	// Unrolled 32-byte loop (4x uint64)
	for l <= maxLen-32 {
		d0 := *(*uint64)(unsafe.Add(ptr1, l)) ^ *(*uint64)(unsafe.Add(ptr2, l))
		if d0 != 0 {
			return l + (bits.TrailingZeros64(d0) >> 3)
		}
		d1 := *(*uint64)(unsafe.Add(ptr1, l+8)) ^ *(*uint64)(unsafe.Add(ptr2, l+8))
		if d1 != 0 {
			return l + 8 + (bits.TrailingZeros64(d1) >> 3)
		}
		d2 := *(*uint64)(unsafe.Add(ptr1, l+16)) ^ *(*uint64)(unsafe.Add(ptr2, l+16))
		if d2 != 0 {
			return l + 16 + (bits.TrailingZeros64(d2) >> 3)
		}
		d3 := *(*uint64)(unsafe.Add(ptr1, l+24)) ^ *(*uint64)(unsafe.Add(ptr2, l+24))
		if d3 != 0 {
			return l + 24 + (bits.TrailingZeros64(d3) >> 3)
		}
		l += 32
	}

	// 8-byte uint64 word loop
	for l <= maxLen-8 {
		diff64 := *(*uint64)(unsafe.Add(ptr1, l)) ^ *(*uint64)(unsafe.Add(ptr2, l))
		if diff64 != 0 {
			return l + (bits.TrailingZeros64(diff64) >> 3)
		}
		l += 8
	}

	// Branch-free overlapping 8-byte tail comparison at maxLen - 8
	if l < maxLen {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}
	return l
}

// HenselLCPPos evaluates the longest common prefix between src[p1:] and src[p2:]
// bounded by maxLen using quadratic Hensel lifting.
func HenselLCPPos(src []byte, p1, p2, maxLen int) int {
	if p1 == p2 {
		return maxLen
	}
	if maxLen <= 0 || p1 < 0 || p2 < 0 || p1 >= len(src) || p2 >= len(src) {
		return 0
	}
	if p1+maxLen > len(src) {
		maxLen = len(src) - p1
	}
	if p2+maxLen > len(src) {
		maxLen = len(src) - p2
	}
	if maxLen <= 0 {
		return 0
	}

	base := unsafe.Pointer(unsafe.SliceData(src))
	ptr1 := unsafe.Pointer(unsafe.Add(base, p1))
	ptr2 := unsafe.Pointer(unsafe.Add(base, p2))

	if maxLen < 8 {
		if maxLen < 4 {
			l := 0
			for l < maxLen && *(*byte)(unsafe.Add(ptr1, l)) == *(*byte)(unsafe.Add(ptr2, l)) {
				l++
			}
			return l
		}
		diff4 := *(*uint32)(ptr1) ^ *(*uint32)(ptr2)
		if diff4 != 0 {
			return bits.TrailingZeros32(diff4) >> 3
		}
		diffTail := *(*uint32)(unsafe.Add(ptr1, maxLen-4)) ^ *(*uint32)(unsafe.Add(ptr2, maxLen-4))
		if diffTail != 0 {
			return (maxLen - 4) + (bits.TrailingZeros32(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 1: 8-byte check
	diff8 := *(*uint64)(ptr1) ^ *(*uint64)(ptr2)
	if diff8 != 0 {
		return bits.TrailingZeros64(diff8) >> 3
	}
	if maxLen < 16 {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 2: 16-byte check
	diff16 := *(*uint64)(unsafe.Add(ptr1, 8)) ^ *(*uint64)(unsafe.Add(ptr2, 8))
	if diff16 != 0 {
		return 8 + (bits.TrailingZeros64(diff16) >> 3)
	}
	if maxLen < 24 {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 3: 24-byte check
	diff24 := *(*uint64)(unsafe.Add(ptr1, 16)) ^ *(*uint64)(unsafe.Add(ptr2, 16))
	if diff24 != 0 {
		return 16 + (bits.TrailingZeros64(diff24) >> 3)
	}
	if maxLen < 32 {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}

	// Step 4: 32-byte check
	diff32 := *(*uint64)(unsafe.Add(ptr1, 24)) ^ *(*uint64)(unsafe.Add(ptr2, 24))
	if diff32 != 0 {
		return 24 + (bits.TrailingZeros64(diff32) >> 3)
	}
	if maxLen == 32 {
		return 32
	}

	// Step 5: SIMD vector extension for bulk matches >= 32 bytes
	l := 32
	rem := maxLen - 32
	if rem >= 32 {
		s1 := unsafe.Slice((*byte)(unsafe.Add(ptr1, 32)), rem)
		s2 := unsafe.Slice((*byte)(unsafe.Add(ptr2, 32)), rem)
		vLen := simd.FindMatchLengthVector(s1, s2, rem)
		l = 32 + vLen
		if l < maxLen && *(*byte)(unsafe.Add(ptr1, l)) != *(*byte)(unsafe.Add(ptr2, l)) {
			return l
		}
	}

	// Unrolled 32-byte loop (4x uint64)
	for l <= maxLen-32 {
		d0 := *(*uint64)(unsafe.Add(ptr1, l)) ^ *(*uint64)(unsafe.Add(ptr2, l))
		if d0 != 0 {
			return l + (bits.TrailingZeros64(d0) >> 3)
		}
		d1 := *(*uint64)(unsafe.Add(ptr1, l+8)) ^ *(*uint64)(unsafe.Add(ptr2, l+8))
		if d1 != 0 {
			return l + 8 + (bits.TrailingZeros64(d1) >> 3)
		}
		d2 := *(*uint64)(unsafe.Add(ptr1, l+16)) ^ *(*uint64)(unsafe.Add(ptr2, l+16))
		if d2 != 0 {
			return l + 16 + (bits.TrailingZeros64(d2) >> 3)
		}
		d3 := *(*uint64)(unsafe.Add(ptr1, l+24)) ^ *(*uint64)(unsafe.Add(ptr2, l+24))
		if d3 != 0 {
			return l + 24 + (bits.TrailingZeros64(d3) >> 3)
		}
		l += 32
	}

	// 8-byte uint64 word loop
	for l <= maxLen-8 {
		diff64 := *(*uint64)(unsafe.Add(ptr1, l)) ^ *(*uint64)(unsafe.Add(ptr2, l))
		if diff64 != 0 {
			return l + (bits.TrailingZeros64(diff64) >> 3)
		}
		l += 8
	}

	// Branch-free overlapping 8-byte tail comparison at maxLen - 8
	if l < maxLen {
		diffTail := *(*uint64)(unsafe.Add(ptr1, maxLen-8)) ^ *(*uint64)(unsafe.Add(ptr2, maxLen-8))
		if diffTail != 0 {
			return (maxLen - 8) + (bits.TrailingZeros64(diffTail) >> 3)
		}
		return maxLen
	}
	return l
}

// hash4 calculates the multiplicative Fibonacci hash for a 4-byte prefix.
//
//go:nosplit
func hash4(w4 uint32) uint32 {
	return w4 * HenselHashPrime
}

// Hash4 calculates the multiplicative Fibonacci hash for a 4-byte prefix.
//
//go:nosplit
func Hash4(w4 uint32) uint32 {
	return hash4(w4)
}

// computeHenselHash calculates the multiplicative Fibonacci hash for a 4-byte prefix.
//
//go:nosplit
func computeHenselHash(w4 uint32, headBits int, headMask int) int {
	return int(hash4(w4)>>(32-headBits)) & headMask
}

// HenselInsertGeneric indexes position p into the provided scratchpad slices.
//
//go:nosplit
func HenselInsertGeneric(
	head []int32,
	prev []int32,
	fp32 []uint32,
	p int,
	src []byte,
	uSize int,
	windowMask int,
	headBits int,
	headMask int,
) {
	if p+4 > uSize {
		return
	}
	_ = head[headMask]
	_ = prev[windowMask]
	_ = fp32[windowMask]

	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, p))
	h := computeHenselHash(w4, headBits, headMask)

	if int(head[h]) == p+1 {
		return
	}

	slot := p & windowMask
	prev[slot] = head[h]
	head[h] = int32(p + 1)

	if p+8 <= uSize {
		fp32[slot] = *(*uint32)(unsafe.Add(srcPtr, p+4))
	} else {
		fp32[slot] = 0
	}
}

// HenselInsertGenericWithHash indexes position p using pre-computed w4 and hash h.
//
//go:nosplit
func HenselInsertGenericWithHash(
	head []int32,
	prev []int32,
	fp32 []uint32,
	p int,
	src []byte,
	uSize int,
	windowMask int,
	headBits int,
	headMask int,
	w4 uint32,
	h uint32,
) {
	if p+4 > uSize {
		return
	}
	_ = head[headMask]
	_ = prev[windowMask]
	_ = fp32[windowMask]

	hIdx := int(h) & headMask
	if int(head[hIdx]) == p+1 {
		return
	}

	slot := p & windowMask
	prev[slot] = head[hIdx]
	head[hIdx] = int32(p + 1)

	if p+8 <= uSize {
		srcPtr := unsafe.Pointer(unsafe.SliceData(src))
		fp32[slot] = *(*uint32)(unsafe.Add(srcPtr, p+4))
	} else {
		fp32[slot] = 0
	}
}

// HenselFindMatchGenericWithHash executes ultrametric match search up to maxDepth with Theorem HL-2 pruning
// using pre-computed w4 prefix and hash table index h.
//
//go:nosplit
func HenselFindMatchGenericWithHash(
	src []byte,
	pos int,
	uSize int,
	maxDepth int,
	minLen int,
	windowSize int,
	windowMask int,
	headBits int,
	headMask int,
	head []int32,
	prev []int32,
	fp32 []uint32,
	w4 uint32,
	h int,
) (bestDist, bestLen int) {
	if pos+4 > uSize {
		return 0, 0
	}
	if minLen < 4 {
		minLen = 4
	}

	_ = head[headMask]
	_ = prev[windowMask]
	_ = fp32[windowMask]

	cand := int(head[h&headMask]) - 1
	if cand < 0 {
		return 0, 0
	}

	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	has8 := pos+8 <= uSize
	var targetFP32 uint32
	if has8 {
		targetFP32 = *(*uint32)(unsafe.Add(srcPtr, pos+4))
	}

	bestLen = minLen - 1
	bestDist = 0
	depth := 0
	maxSearchLen := min(uSize-pos, 65535)

	for cand >= 0 && depth < maxDepth {
		candSlot := cand & windowMask

		// If candidate is ahead of or at pos, step back to past candidates
		if cand >= pos {
			nextCand := int(prev[candSlot]) - 1
			if nextCand >= cand || nextCand < 0 {
				break
			}
			cand = nextCand
			continue
		}

		d := pos - cand
		if d >= windowSize || d <= 0 {
			break
		}

		// Theorem HL-2 (Ultrametric Disjoint Ball Pruning & Exact Match Short-Circuit)
		hasDiff := false
		var maxLift int
		if has8 && bestLen >= 4 {
			diff := fp32[candSlot] ^ targetFP32
			if diff != 0 {
				maxLift = 4 + (bits.TrailingZeros32(diff) >> 3)
				if maxLift <= bestLen {
					nextCand := int(prev[candSlot]) - 1
					if nextCand >= cand || nextCand < 0 {
						break
					}
					cand = nextCand
					depth++
					continue
				}
				hasDiff = true
			}
		}

		if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
			if hasDiff {
				// Theorem HL-2 exact length proof: DO NOT call HenselLCPPos!
				bestLen = maxLift
				bestDist = d
			} else {
				mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
				if mLen > bestLen {
					bestLen = mLen
					bestDist = d
					if bestLen >= 258 {
						break
					}
				}
			}
		}

		nextCand := int(prev[candSlot]) - 1
		if nextCand >= cand || nextCand < 0 {
			break
		}
		cand = nextCand
		depth++
	}

	if bestLen < minLen {
		return 0, 0
	}
	return bestDist, bestLen
}

// HenselFindMatchGeneric executes ultrametric match search up to maxDepth with Theorem HL-2 pruning.
//
//go:nosplit
func HenselFindMatchGeneric(
	src []byte,
	pos int,
	uSize int,
	maxDepth int,
	minLen int,
	windowSize int,
	windowMask int,
	headBits int,
	headMask int,
	head []int32,
	prev []int32,
	fp32 []uint32,
) (bestDist, bestLen int) {
	if pos+4 > uSize {
		return 0, 0
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, pos))
	h := computeHenselHash(w4, headBits, headMask)
	return HenselFindMatchGenericWithHash(
		src, pos, uSize, maxDepth, minLen,
		windowSize, windowMask, headBits, headMask,
		head, prev, fp32,
		w4, h,
	)
}

// HenselInsert registers position p into the 64 KiB Hensel search tables.
func HenselInsert(sp *HenselScratchpad, p int, src []byte, uSize int) {
	if sp == nil {
		return
	}
	HenselInsertGeneric(sp.head[:], sp.prev[:], sp.fp32[:], p, src, uSize, HenselWindowMask, HenselHashBits, HenselHashMask)
}

// HenselFindMatch performs ultrametric match search up to maxDepth with Theorem HL-2 pruning (64 KiB window).
func HenselFindMatch(
	src []byte,
	pos int,
	uSize int,
	maxDepth int,
	minLen int,
	sp *HenselScratchpad,
) (bestDist, bestLen int) {
	if sp == nil {
		return 0, 0
	}
	return HenselFindMatchGeneric(
		src, pos, uSize, maxDepth, minLen,
		HenselWindowSize, HenselWindowMask, HenselHashBits, HenselHashMask,
		sp.head[:], sp.prev[:], sp.fp32[:],
	)
}

// HenselInsert1M registers position p into the 1 MiB Hensel search tables.
func HenselInsert1M(sp *HenselScratchpad1M, p int, src []byte, uSize int) {
	if sp == nil {
		return
	}
	HenselInsertGeneric(sp.head[:], sp.prev[:], sp.fp32[:], p, src, uSize, WindowMask1M, HashBits1M, HashMask1M)
}

// HenselFindMatch1M performs ultrametric match search up to maxDepth with Theorem HL-2 pruning (1 MiB window).
func HenselFindMatch1M(
	src []byte,
	pos int,
	uSize int,
	maxDepth int,
	minLen int,
	sp *HenselScratchpad1M,
) (bestDist, bestLen int) {
	if sp == nil {
		return 0, 0
	}
	return HenselFindMatchGeneric(
		src, pos, uSize, maxDepth, minLen,
		WindowSize1M, WindowMask1M, HashBits1M, HashMask1M,
		sp.head[:], sp.prev[:], sp.fp32[:],
	)
}

// HenselInsert8M registers position p into the 8 MiB Hensel search tables.
func HenselInsert8M(sp *HenselScratchpad8M, p int, src []byte, uSize int) {
	if sp == nil {
		return
	}
	HenselInsertGeneric(sp.head[:], sp.prev[:], sp.fp32[:], p, src, uSize, WindowMask8M, HashBits8M, HashMask8M)
}

// HenselFindMatch8M performs ultrametric match search up to maxDepth with Theorem HL-2 pruning (8 MiB window).
func HenselFindMatch8M(
	src []byte,
	pos int,
	uSize int,
	maxDepth int,
	minLen int,
	sp *HenselScratchpad8M,
) (bestDist, bestLen int) {
	if sp == nil {
		return 0, 0
	}
	return HenselFindMatchGeneric(
		src, pos, uSize, maxDepth, minLen,
		WindowSize8M, WindowMask8M, HashBits8M, HashMask8M,
		sp.head[:], sp.prev[:], sp.fp32[:],
	)
}

// HenselInsertL6 registers position p into the 3-tier Level 6 Hensel search tables.
//
//go:nosplit
func HenselInsertL6(sp *HenselScratchpadL6, p int, src []byte, uSize int) {
	if sp == nil || p+3 > uSize {
		return
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	slot := p & WindowMask8M

	// Tier 1: 3-byte hash
	b0 := uint32(*(*byte)(unsafe.Add(srcPtr, p)))
	b1 := uint32(*(*byte)(unsafe.Add(srcPtr, p+1)))
	b2 := uint32(*(*byte)(unsafe.Add(srcPtr, p+2)))
	h24 := ((b0 | (b1 << 8) | (b2 << 16)) * HenselHashPrime) >> (32 - HashBits1M)
	sp.head24[h24&HashMask1M] = int32(p + 1)

	// Tier 2: 4-byte hash
	if p+4 <= uSize {
		w4 := *(*uint32)(unsafe.Add(srcPtr, p))
		h32 := (w4 * HenselHashPrime) >> (32 - HashBits8M)
		hIdx := int(h32) & HashMask8M
		sp.prev[slot] = sp.head32[hIdx]
		sp.head32[hIdx] = int32(p + 1)

		if p+8 <= uSize {
			sp.fp32[slot] = *(*uint32)(unsafe.Add(srcPtr, p+4))
		} else {
			sp.fp32[slot] = 0
		}
	}

	// Tier 3: Macro-hash (sample every 8 bytes)
	if (p&7) == 0 && p+8 <= uSize {
		w8 := *(*uint64)(unsafe.Add(srcPtr, p))
		mHash := uint32((w8 ^ (w8 >> 32)) * 0x9E3779B9) >> 16
		sp.macroHead[mHash&0xFFFF] = int32(p + 1)
	}
}

// HenselFindMatchL6 performs ultrametric multi-tier match search up to maxDepth with Theorem HL-2 pruning.
//
//go:nosplit
func HenselFindMatchL6(
	src []byte,
	pos int,
	uSize int,
	maxDepth int,
	minLen int,
	sp *HenselScratchpadL6,
) (bestDist, bestLen int) {
	if sp == nil || pos+3 > uSize {
		return 0, 0
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	maxSearchLen := uSize - pos

	// 1. Tier 3: Macro Anchor check (for big structural blocks >= 16 bytes)
	if pos+8 <= uSize {
		w8 := *(*uint64)(unsafe.Add(srcPtr, pos))
		mHash := uint32((w8 ^ (w8 >> 32)) * 0x9E3779B9) >> 16
		candMacro := int(sp.macroHead[mHash&0xFFFF]) - 1
		if candMacro >= 0 && candMacro < pos {
			d := pos - candMacro
			if d < WindowSize8M && d > 0 {
				mLen := HenselLCPPos(src, pos, candMacro, maxSearchLen)
				if mLen >= 16 {
					bestLen = mLen
					bestDist = d
				}
			}
		}
	}

	// 2. Tier 2: 4-byte hash table with Theorem HL-2 ultrametric pruning
	if pos+4 <= uSize {
		w4 := *(*uint32)(unsafe.Add(srcPtr, pos))
		h32 := (w4 * HenselHashPrime) >> (32 - HashBits8M)
		cand := int(sp.head32[h32&HashMask8M]) - 1

		if cand >= 0 {
			has8 := pos+8 <= uSize
			var targetFP32 uint32
			if has8 {
				targetFP32 = *(*uint32)(unsafe.Add(srcPtr, pos+4))
			}

			depth := 0
			for cand >= 0 && depth < maxDepth {
				candSlot := cand & WindowMask8M

				if cand >= pos {
					nextCand := int(sp.prev[candSlot]) - 1
					if nextCand >= cand || nextCand < 0 {
						break
					}
					cand = nextCand
					continue
				}

				d := pos - cand
				if d >= WindowSize8M || d <= 0 {
					break
				}

				// Theorem HL-2 ultrametric pruning
				hasDiff := false
				var maxLift int
				if has8 && bestLen >= 4 {
					diff := sp.fp32[candSlot] ^ targetFP32
					if diff != 0 {
						maxLift = 4 + (bits.TrailingZeros32(diff) >> 3)
						if maxLift <= bestLen {
							nextCand := int(sp.prev[candSlot]) - 1
							if nextCand >= cand || nextCand < 0 {
								break
							}
							cand = nextCand
							depth++
							continue
						}
						hasDiff = true
					}
				}

				if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
					if hasDiff {
						bestLen = maxLift
						bestDist = d
					} else {
						mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
						if mLen > bestLen {
							bestLen = mLen
							bestDist = d
							if bestLen >= 1024 {
								break
							}
						}
					}
				}

				nextCand := int(sp.prev[candSlot]) - 1
				if nextCand >= cand || nextCand < 0 {
					break
				}
				cand = nextCand
				depth++
			}
		}
	}

	// 3. Tier 1: If minLen <= 3 and no match found >= 3 bytes, check head24
	if bestLen < 3 && minLen <= 3 {
		b0 := uint32(*(*byte)(unsafe.Add(srcPtr, pos)))
		b1 := uint32(*(*byte)(unsafe.Add(srcPtr, pos+1)))
		b2 := uint32(*(*byte)(unsafe.Add(srcPtr, pos+2)))
		h24 := ((b0 | (b1 << 8) | (b2 << 16)) * HenselHashPrime) >> (32 - HashBits1M)
		cand24 := int(sp.head24[h24&HashMask1M]) - 1
		if cand24 >= 0 && cand24 < pos {
			d := pos - cand24
			if d < WindowSize8M && d > 0 {
				c0 := *(*byte)(unsafe.Add(srcPtr, cand24))
				c1 := *(*byte)(unsafe.Add(srcPtr, cand24+1))
				c2 := *(*byte)(unsafe.Add(srcPtr, cand24+2))
				if c0 == byte(b0) && c1 == byte(b1) && c2 == byte(b2) {
					mLen := HenselLCPPos(src, pos, cand24, maxSearchLen)
					if mLen > bestLen {
						bestLen = mLen
						bestDist = d
					}
				}
			}
		}
	}

	if bestLen < minLen {
		return 0, 0
	}
	return bestDist, bestLen
}
