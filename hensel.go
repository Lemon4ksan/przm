// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math/bits"

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
	if maxLen == 0 {
		return 0
	}
	if maxLen < 4 {
		l := 0
		for l < maxLen && a[l] == b[l] {
			l++
		}
		return l
	}

	// Step 1: Base 4-byte check
	diff4 := binary.LittleEndian.Uint32(a[:4]) ^ binary.LittleEndian.Uint32(b[:4])
	if diff4 != 0 {
		return bits.TrailingZeros32(diff4) >> 3
	}
	if maxLen < 8 {
		l := 4
		for l < maxLen && a[l] == b[l] {
			l++
		}
		return l
	}

	// Step 2: Hensel Lift 4B -> 8B (Evaluate bytes 4..7)
	diff8 := binary.LittleEndian.Uint32(a[4:8]) ^ binary.LittleEndian.Uint32(b[4:8])
	if diff8 != 0 {
		return 4 + (bits.TrailingZeros32(diff8) >> 3)
	}
	if maxLen < 16 {
		l := 8
		for l < maxLen && a[l] == b[l] {
			l++
		}
		return l
	}

	// Step 3: Hensel Lift 8B -> 16B (Evaluate bytes 8..15)
	diff16 := binary.LittleEndian.Uint64(a[8:16]) ^ binary.LittleEndian.Uint64(b[8:16])
	if diff16 != 0 {
		return 8 + (bits.TrailingZeros64(diff16) >> 3)
	}
	if maxLen < 32 {
		l := 16
		for l < maxLen && a[l] == b[l] {
			l++
		}
		return l
	}

	// Step 4: Hensel Lift 16B -> 32B (Evaluate bytes 16..31)
	d24 := binary.LittleEndian.Uint64(a[16:24]) ^ binary.LittleEndian.Uint64(b[16:24])
	if d24 != 0 {
		return 16 + (bits.TrailingZeros64(d24) >> 3)
	}
	d32 := binary.LittleEndian.Uint64(a[24:32]) ^ binary.LittleEndian.Uint64(b[24:32])
	if d32 != 0 {
		return 24 + (bits.TrailingZeros64(d32) >> 3)
	}

	// Step 5: SIMD vector extension for matches >= 32 bytes
	rem := maxLen - 32
	if rem >= 32 {
		vLen := simd.FindMatchLengthVector(a[32:maxLen], b[32:maxLen], rem)
		mLen := 32 + vLen
		for mLen < maxLen && a[mLen] == b[mLen] {
			mLen++
		}
		return mLen
	}
	mLen := 32
	for mLen < maxLen && a[mLen] == b[mLen] {
		mLen++
	}
	return mLen
}

// HenselLCPPos evaluates the longest common prefix between src[p1:] and src[p2:]
// bounded by maxLen using quadratic Hensel lifting.
func HenselLCPPos(src []byte, p1, p2, maxLen int) int {
	if p1 == p2 {
		return maxLen
	}
	if maxLen <= 0 {
		return 0
	}
	if maxLen < 4 {
		l := 0
		for l < maxLen && src[p1+l] == src[p2+l] {
			l++
		}
		return l
	}

	// Step 1: Base 4-byte check
	diff4 := binary.LittleEndian.Uint32(src[p1:p1+4]) ^ binary.LittleEndian.Uint32(src[p2:p2+4])
	if diff4 != 0 {
		return bits.TrailingZeros32(diff4) >> 3
	}
	if maxLen < 8 {
		l := 4
		for l < maxLen && src[p1+l] == src[p2+l] {
			l++
		}
		return l
	}

	// Step 2: Hensel Lift 4B -> 8B
	diff8 := binary.LittleEndian.Uint32(src[p1+4:p1+8]) ^ binary.LittleEndian.Uint32(src[p2+4:p2+8])
	if diff8 != 0 {
		return 4 + (bits.TrailingZeros32(diff8) >> 3)
	}
	if maxLen < 16 {
		l := 8
		for l < maxLen && src[p1+l] == src[p2+l] {
			l++
		}
		return l
	}

	// Step 3: Hensel Lift 8B -> 16B
	diff16 := binary.LittleEndian.Uint64(src[p1+8:p1+16]) ^ binary.LittleEndian.Uint64(src[p2+8:p2+16])
	if diff16 != 0 {
		return 8 + (bits.TrailingZeros64(diff16) >> 3)
	}
	if maxLen < 32 {
		l := 16
		for l < maxLen && src[p1+l] == src[p2+l] {
			l++
		}
		return l
	}

	// Step 4: Hensel Lift 16B -> 32B
	d24 := binary.LittleEndian.Uint64(src[p1+16:p1+24]) ^ binary.LittleEndian.Uint64(src[p2+16:p2+24])
	if d24 != 0 {
		return 16 + (bits.TrailingZeros64(d24) >> 3)
	}
	d32 := binary.LittleEndian.Uint64(src[p1+24:p1+32]) ^ binary.LittleEndian.Uint64(src[p2+24:p2+32])
	if d32 != 0 {
		return 24 + (bits.TrailingZeros64(d32) >> 3)
	}

	// Step 5: SIMD vector extension
	rem := maxLen - 32
	if rem >= 32 {
		vLen := simd.FindMatchLengthVector(src[p1+32:p1+maxLen], src[p2+32:p2+maxLen], rem)
		mLen := 32 + vLen
		for mLen < maxLen && src[p1+mLen] == src[p2+mLen] {
			mLen++
		}
		return mLen
	}
	mLen := 32
	for mLen < maxLen && src[p1+mLen] == src[p2+mLen] {
		mLen++
	}
	return mLen
}

// computeHenselHash calculates the multiplicative Fibonacci hash for a 4-byte prefix.
//
//go:nosplit
func computeHenselHash(w4 uint32, headBits int, headMask int) int {
	return int((w4 * HenselHashPrime) >> (32 - headBits)) & headMask
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
	w4 := binary.LittleEndian.Uint32(src[p : p+4])
	h := computeHenselHash(w4, headBits, headMask)

	if int(head[h]) == p+1 {
		return
	}

	slot := p & windowMask
	prev[slot] = head[h]
	head[h] = int32(p + 1)

	if p+8 <= uSize {
		fp32[slot] = binary.LittleEndian.Uint32(src[p+4 : p+8])
	} else {
		fp32[slot] = 0
	}
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
	if minLen < 4 {
		minLen = 4
	}

	w4 := binary.LittleEndian.Uint32(src[pos : pos+4])
	h := computeHenselHash(w4, headBits, headMask)

	cand := int(head[h]) - 1
	if cand < 0 {
		return 0, 0
	}

	has8 := pos+8 <= uSize
	var targetFP32 uint32
	if has8 {
		targetFP32 = binary.LittleEndian.Uint32(src[pos+4 : pos+8])
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

		// Theorem HL-2 (Ultrametric Disjoint Ball Pruning):
		// Bitwise single-cycle XOR check before dereferencing payload memory.
		if has8 && bestLen >= 4 {
			diff := fp32[candSlot] ^ targetFP32
			if diff != 0 {
				maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
				if maxLift <= bestLen {
					// 1-cycle rejection without touching cache line for src[cand..]
					nextCand := int(prev[candSlot]) - 1
					if nextCand >= cand || nextCand < 0 {
						break
					}
					cand = nextCand
					depth++
					continue
				}
			}
		}

		// Candidate passed Theorem HL-2 pruning. Verify 4-byte prefix.
		if binary.LittleEndian.Uint32(src[cand:cand+4]) == w4 {
			mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
			if mLen > bestLen {
				bestLen = mLen
				bestDist = d
				if bestLen >= 258 {
					break
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
