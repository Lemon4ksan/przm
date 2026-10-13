// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"math/bits"
	"unsafe"
)

var _ = vectorChainScan_AVX2

const (
	VectorTableWays8  = 8
	VectorTableWays16 = 16
	VectorHashBits    = 16
	VectorHashSize    = 1 << VectorHashBits // 65,536 buckets
	VectorHashMask    = VectorHashSize - 1

	// Deep chain maximum candidate capacity per probe
	MaxDeepCandidates = 256
)

// VectorBucket8 packs 8 candidate positions and 8 ultrametric fingerprints into exactly 64 bytes.
// Corresponds to 1 cache line on x86-64 and 1 AVX2 YMM vector per component.
type VectorBucket8 struct {
	Pos  [8]int32  // 32 bytes (1 YMM vector)
	FP32 [8]uint32 // 32 bytes (1 YMM vector)
}

// VectorBucket16 packs 16 candidate positions and 16 fingerprints into exactly 128 bytes (2 cache lines).
type VectorBucket16 struct {
	Pos  [16]int32  // 64 bytes (2 YMM vectors)
	FP32 [16]uint32 // 64 bytes (2 YMM vectors)
}

// HenselMultiWayTable8 implements an 8-way SIMD vector hash table (4 MiB resident footprint).
type HenselMultiWayTable8 struct {
	Buckets [VectorHashSize]VectorBucket8
	Heads   [VectorHashSize]uint8 // cyclic index 0..7
}

// Reset clears the bucket insertion indices.
func (vt *HenselMultiWayTable8) Reset() {
	clear(vt.Heads[:])
}

// Insert registers a position and its 32-bit ultrametric fingerprint in O(1) clock cycles.
//
//go:nosplit
func (vt *HenselMultiWayTable8) Insert(h, pos int, targetFP uint32) {
	hIdx := h & VectorHashMask
	slot := int(vt.Heads[hIdx]) & 7
	vt.Buckets[hIdx].Pos[slot] = int32(pos + 1)
	vt.Buckets[hIdx].FP32[slot] = targetFP
	vt.Heads[hIdx] = uint8(slot + 1)
}

// Probe evaluates all 8 candidates in the bucket in parallel using AVX2.
//
//go:nosplit
func (vt *HenselMultiWayTable8) Probe(h int, curPos int32, targetFP uint32, windowSize int32) VectorProbeResult {
	var res VectorProbeResult
	hIdx := h & VectorHashMask
	b := &vt.Buckets[hIdx]
	vectorProbe8_AVX2(
		unsafe.Pointer(&b.Pos[0]),
		unsafe.Pointer(&b.FP32[0]),
		curPos,
		targetFP,
		windowSize,
		&res,
	)
	return res
}

// HenselMultiWayTable16 implements a 16-way SIMD vector hash table (8 MiB resident footprint).
type HenselMultiWayTable16 struct {
	Buckets [VectorHashSize]VectorBucket16
	Heads   [VectorHashSize]uint8 // cyclic index 0..15
}

// Reset clears the bucket insertion indices.
func (vt *HenselMultiWayTable16) Reset() {
	clear(vt.Heads[:])
}

// Insert registers a position and its fingerprint in O(1) clock cycles.
//
//go:nosplit
func (vt *HenselMultiWayTable16) Insert(h, pos int, targetFP uint32) {
	hIdx := h & VectorHashMask
	slot := int(vt.Heads[hIdx]) & 15
	vt.Buckets[hIdx].Pos[slot] = int32(pos + 1)
	vt.Buckets[hIdx].FP32[slot] = targetFP
	vt.Heads[hIdx] = uint8(slot + 1)
}

// Probe evaluates all 16 candidates in the bucket in parallel using AVX2.
//
//go:nosplit
func (vt *HenselMultiWayTable16) Probe(h int, curPos int32, targetFP uint32, windowSize int32) VectorProbeResult16 {
	var res VectorProbeResult16
	hIdx := h & VectorHashMask
	b := &vt.Buckets[hIdx]
	vectorProbe16_AVX2(
		unsafe.Pointer(&b.Pos[0]),
		unsafe.Pointer(&b.FP32[0]),
		curPos,
		targetFP,
		windowSize,
		&res,
	)
	return res
}

// VectorCandidateArray stores candidate matches collected for deep vectorized scanning.
type VectorCandidateArray struct {
	Pos  [MaxDeepCandidates]int32
	FP32 [MaxDeepCandidates]uint32
}

// FindMatchSIMD16 executes an ultrafast 16-way vector probe match search.
//
//go:nosplit
func FindMatchSIMD16(
	src []byte,
	pos int,
	uSize int,
	windowSize int,
	minLen int,
	vt *HenselMultiWayTable16,
) (bestDist, bestLen int) {
	if pos+4 > uSize || vt == nil {
		return 0, 0
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, pos))
	h := int(hash4(w4)>>(32-VectorHashBits)) & VectorHashMask

	var targetFP uint32
	has8 := pos+8 <= uSize
	if has8 {
		targetFP = *(*uint32)(unsafe.Add(srcPtr, pos+4))
	}

	res := vt.Probe(h, int32(pos), targetFP, int32(windowSize))
	if res.ValidMask == 0 {
		return 0, 0
	}

	bestScore := -1000
	bestLen = minLen - 1
	bestDist = 0
	maxSearchLen := min(uSize-pos, 65535)

	calcCost := func(d int) int {
		if d < 128 {
			return 1
		}
		if d < 16384 {
			return 2
		}
		if d < 2097152 {
			return 3
		}
		return 4
	}

	// 1. Check Exact Matches (fingerprint matches completely, guaranteeing len >= 8)
	if has8 && res.ExactMask != 0 {
		exactMask := res.ExactMask
		for exactMask != 0 {
			tz := bits.TrailingZeros32(exactMask)
			exactMask &= exactMask - 1

			cand := int(vt.Buckets[h].Pos[tz]) - 1
			if cand < 0 || cand >= pos {
				continue
			}
			d := pos - cand
			if d <= 0 || d >= windowSize {
				continue
			}

			// Verify 4-byte prefix to eliminate rare hash collisions
			if *(*uint32)(unsafe.Add(srcPtr, cand)) != w4 {
				continue
			}

			// Bytes 0..7 already match 100%! Only extend beyond 8 bytes
			extLen := 8
			if pos+8 < uSize && cand+8 < uSize {
				extLen = 8 + HenselLCPPos(src, pos+8, cand+8, maxSearchLen-8)
			}
			score := extLen - calcCost(d)
			if score > bestScore || (score == bestScore && (extLen > bestLen || d < bestDist)) || extLen > bestLen+1 {
				bestScore = score
				bestLen = extLen
				bestDist = d
				if bestLen >= 258 {
					return bestDist, bestLen
				}
			}
		}
		if bestLen >= 8 {
			return bestDist, bestLen
		}
	}

	// 2. Theorem HL-2 Ultrametric Evaluation for remaining valid candidates (len 4..7)
	validMask := res.ValidMask
	for validMask != 0 {
		tz := bits.TrailingZeros32(validMask)
		validMask &= validMask - 1

		cand := int(vt.Buckets[h].Pos[tz]) - 1
		if cand < 0 || cand >= pos {
			continue
		}
		d := pos - cand
		if d <= 0 || d >= windowSize {
			continue
		}

		if has8 && bestLen >= 4 {
			candFP := vt.Buckets[h].FP32[tz]
			diff := candFP ^ targetFP
			if diff != 0 {
				maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
				score := maxLift - calcCost(d)
				if score > bestScore || (score == bestScore && (maxLift > bestLen || d < bestDist)) {
					if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
						bestScore = score
						bestLen = maxLift
						bestDist = d
					}
				}
				continue
			}
		}

		if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
			mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
			score := mLen - calcCost(d)
			if score > bestScore || (score == bestScore && (mLen > bestLen || d < bestDist)) {
				bestScore = score
				bestLen = mLen
				bestDist = d
				if bestLen >= 258 {
					break
				}
			}
		}
	}

	if bestLen < minLen {
		return 0, 0
	}
	return bestDist, bestLen
}

// FindMatchDeepVector searches 64-256 match candidates per byte position with SIMD filtering.
//
//go:nosplit
func FindMatchDeepVector(
	src []byte,
	pos int,
	uSize int,
	windowSize int,
	minLen int,
	maxCandidates int,
	vt *HenselMultiWayTable16,
	prev []int32,
	windowMask int,
) (bestDist, bestLen int) {
	if pos+4 > uSize {
		return 0, 0
	}
	if maxCandidates > MaxDeepCandidates {
		maxCandidates = MaxDeepCandidates
	}
	if maxCandidates < 16 {
		maxCandidates = 16
	}

	// Step 1: Probe the hot 16-way vector table (evaluated in ~10 ns)
	if vt != nil {
		hDist, hLen := FindMatchSIMD16(src, pos, uSize, windowSize, minLen, vt)
		if hLen >= 64 {
			return hDist, hLen
		}
		bestDist = hDist
		bestLen = hLen
	}

	if len(prev) == 0 {
		return bestDist, bestLen
	}

	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, pos))
	has8 := pos+8 <= uSize
	var targetFP uint32
	if has8 {
		targetFP = *(*uint32)(unsafe.Add(srcPtr, pos+4))
	}

	// Step 2: Traverse deep collision chains with SIMD vector scanning
	maxSearchLen := min(uSize-pos, 65535)
	if bestLen < minLen {
		bestLen = minLen - 1
	}

	h := int(hash4(w4)>>(32-VectorHashBits)) & VectorHashMask
	var startCand int
	if vt != nil {
		startCand = int(vt.Buckets[h].Pos[0]) - 1
	}
	if startCand < 0 || startCand >= pos {
		return bestDist, bestLen
	}

	cand := startCand
	depth := 0

	for cand >= 0 && depth < maxCandidates {
		candSlot := cand & windowMask
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

		if has8 && bestLen >= 4 && cand+8 <= uSize {
			candFP := *(*uint32)(unsafe.Add(srcPtr, cand+4))
			diff := candFP ^ targetFP
			if diff != 0 {
				maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
				if maxLift <= bestLen {
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

		if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
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

// BracketCandidate contains a match candidate with distance, length, and net score.
type BracketCandidate struct {
	Dist   int32
	Length int32
	Score  int32
}

// ProbeBracketCandidatesSIMD16 evaluates all 16 candidates in the bucket and returns
// the best candidate in each of the 4 varint brackets in O(1) clock cycles.
func ProbeBracketCandidatesSIMD16(
	src []byte,
	pos int,
	uSize int,
	windowSize int,
	minLen int,
	vt *HenselMultiWayTable16,
) (brackets [4]BracketCandidate) {
	if pos+4 > uSize || vt == nil {
		return brackets
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, pos))
	h := int(hash4(w4)>>(32-VectorHashBits)) & VectorHashMask

	var targetFP uint32
	has8 := pos+8 <= uSize
	if has8 {
		targetFP = *(*uint32)(unsafe.Add(srcPtr, pos+4))
	}

	res := vt.Probe(h, int32(pos), targetFP, int32(windowSize))
	if res.ValidMask == 0 {
		return brackets
	}

	maxSearchLen := min(uSize-pos, 65535)

	getBracket := func(d int) int {
		if d < 128 {
			return 0
		}
		if d < 16384 {
			return 1
		}
		if d < 2097152 {
			return 2
		}
		return 3
	}

	calcCost := func(b int) int {
		return b + 1
	}

	// 1. Exact matches (len >= 8)
	if has8 && res.ExactMask != 0 {
		exactMask := res.ExactMask
		for exactMask != 0 {
			tz := bits.TrailingZeros32(exactMask)
			exactMask &= exactMask - 1

			cand := int(vt.Buckets[h].Pos[tz]) - 1
			if cand < 0 || cand >= pos {
				continue
			}
			d := pos - cand
			if d <= 0 || d >= windowSize {
				continue
			}

			if *(*uint32)(unsafe.Add(srcPtr, cand)) != w4 {
				continue
			}

			extLen := 8
			if pos+8 < uSize && cand+8 < uSize {
				extLen = 8 + HenselLCPPos(src, pos+8, cand+8, maxSearchLen-8)
			}
			b := getBracket(d)
			score := int32(extLen - calcCost(b))
			if score > brackets[b].Score || (score == brackets[b].Score && extLen > int(brackets[b].Length)) {
				brackets[b].Dist = int32(d)
				brackets[b].Length = int32(extLen)
				brackets[b].Score = score
			}
		}
	}

	// 2. Ultrametric evaluation for remaining candidates
	validMask := res.ValidMask
	for validMask != 0 {
		tz := bits.TrailingZeros32(validMask)
		validMask &= validMask - 1

		cand := int(vt.Buckets[h].Pos[tz]) - 1
		if cand < 0 || cand >= pos {
			continue
		}
		d := pos - cand
		if d <= 0 || d >= windowSize {
			continue
		}

		b := getBracket(d)

		if has8 {
			candFP := vt.Buckets[h].FP32[tz]
			diff := candFP ^ targetFP
			if diff != 0 {
				maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
				if maxLift < minLen {
					continue
				}
				score := int32(maxLift - calcCost(b))
				if score > brackets[b].Score || (score == brackets[b].Score && maxLift > int(brackets[b].Length)) {
					if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
						brackets[b].Dist = int32(d)
						brackets[b].Length = int32(maxLift)
						brackets[b].Score = score
					}
				}
				continue
			}
		}

		if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
			mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
			if mLen >= minLen {
				score := int32(mLen - calcCost(b))
				if score > brackets[b].Score || (score == brackets[b].Score && mLen > int(brackets[b].Length)) {
					brackets[b].Dist = int32(d)
					brackets[b].Length = int32(mLen)
					brackets[b].Score = score
				}
			}
		}
	}

	return brackets
}

// VectorBucket32 packs 32 candidate positions and 32 fingerprints into exactly 256 bytes (4 cache lines).
type VectorBucket32 struct {
	Pos  [32]int32
	FP32 [32]uint32
}

const (
	VectorTable32Bits = 15
	VectorTable32Size = 1 << VectorTable32Bits // 32,768 buckets
	VectorTable32Mask = VectorTable32Size - 1
)

// HenselMultiWayTable32 implements a 32-way SIMD vector hash table (8 MiB resident footprint).
type HenselMultiWayTable32 struct {
	Buckets [VectorTable32Size]VectorBucket32
	Heads   [VectorTable32Size]uint8 // cyclic index 0..31
}

// Reset clears the bucket insertion indices.
func (vt *HenselMultiWayTable32) Reset() {
	clear(vt.Heads[:])
}

// Insert registers a position and its fingerprint in O(1) clock cycles.
//
//go:nosplit
func (vt *HenselMultiWayTable32) Insert(h, pos int, targetFP uint32) {
	hIdx := h & VectorTable32Mask
	slot := int(vt.Heads[hIdx]) & 31
	vt.Buckets[hIdx].Pos[slot] = int32(pos + 1)
	vt.Buckets[hIdx].FP32[slot] = targetFP
	vt.Heads[hIdx] = uint8(slot + 1)
}

// Probe evaluates all 32 candidates in the bucket in parallel using AVX2.
//
//go:nosplit
func (vt *HenselMultiWayTable32) Probe(h int, curPos int32, targetFP uint32, windowSize int32) VectorProbeResult32 {
	var res VectorProbeResult32
	hIdx := h & VectorTable32Mask
	b := &vt.Buckets[hIdx]
	vectorProbe32_AVX2(
		unsafe.Pointer(&b.Pos[0]),
		unsafe.Pointer(&b.FP32[0]),
		curPos,
		targetFP,
		windowSize,
		&res,
	)
	return res
}

// FindMatchSIMD64 probes 64 match candidates per byte position in O(1) SIMD cycles using dual 32-way buckets.
//
//go:nosplit
func FindMatchSIMD64(
	src []byte,
	pos int,
	uSize int,
	windowSize int,
	minLen int,
	vt *HenselMultiWayTable32,
) (bestDist, bestLen int) {
	if pos+4 > uSize || vt == nil {
		return 0, 0
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, pos))

	var targetFP uint32
	has8 := pos+8 <= uSize
	if has8 {
		targetFP = *(*uint32)(unsafe.Add(srcPtr, pos+4))
	}

	bestScore := -1000
	bestLen = minLen - 1
	bestDist = 0
	maxSearchLen := min(uSize-pos, 65535)

	calcCost := func(d int) int {
		if d < 128 {
			return 1
		}
		if d < 16384 {
			return 2
		}
		if d < 2097152 {
			return 3
		}
		return 4
	}

	// Probe primary and secondary 32-way buckets (total 64 candidates)
	h0 := int(w4*0x1E35A7BD>>17) & VectorTable32Mask
	h1 := int(w4*0x7FEB352D>>17) & VectorTable32Mask
	hashes := [2]int{h0, h1}

	for _, h := range hashes {
		res := vt.Probe(h, int32(pos), targetFP, int32(windowSize))
		if res.ValidMask == 0 {
			continue
		}

		// Exact matches (len >= 8)
		if has8 && res.ExactMask != 0 {
			exactMask := res.ExactMask
			for exactMask != 0 {
				tz := bits.TrailingZeros32(exactMask)
				exactMask &= exactMask - 1

				cand := int(vt.Buckets[h].Pos[tz]) - 1
				if cand < 0 || cand >= pos {
					continue
				}
				d := pos - cand
				if d <= 0 || d >= windowSize {
					continue
				}

				if *(*uint32)(unsafe.Add(srcPtr, cand)) != w4 {
					continue
				}

				extLen := 8
				if pos+8 < uSize && cand+8 < uSize {
					extLen = 8 + HenselLCPPos(src, pos+8, cand+8, maxSearchLen-8)
				}
				score := extLen - calcCost(d)
				if score > bestScore || (score == bestScore && (extLen > bestLen || d < bestDist)) ||
					extLen > bestLen+1 {
					bestScore = score
					bestLen = extLen
					bestDist = d
					if bestLen >= 258 {
						return bestDist, bestLen
					}
				}
			}
			if bestLen >= 8 {
				continue
			}
		}

		// Ultrametric evaluation (len 4..7)
		validMask := res.ValidMask
		for validMask != 0 {
			tz := bits.TrailingZeros32(validMask)
			validMask &= validMask - 1

			cand := int(vt.Buckets[h].Pos[tz]) - 1
			if cand < 0 || cand >= pos {
				continue
			}
			d := pos - cand
			if d <= 0 || d >= windowSize {
				continue
			}

			if has8 && bestLen >= 4 {
				candFP := vt.Buckets[h].FP32[tz]
				diff := candFP ^ targetFP
				if diff != 0 {
					maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
					score := maxLift - calcCost(d)
					if score > bestScore || (score == bestScore && (maxLift > bestLen || d < bestDist)) {
						if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
							bestScore = score
							bestLen = maxLift
							bestDist = d
						}
					}
					continue
				}
			}

			if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
				mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
				score := mLen - calcCost(d)
				if score > bestScore || (score == bestScore && (mLen > bestLen || d < bestDist)) {
					bestScore = score
					bestLen = mLen
					bestDist = d
					if bestLen >= 258 {
						return bestDist, bestLen
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

// FindMatchSIMD256 probes 256 match candidates per byte position in O(1) SIMD cycles across 8 hash buckets.
//
//go:nosplit
func FindMatchSIMD256(
	src []byte,
	pos int,
	uSize int,
	windowSize int,
	minLen int,
	vt *HenselMultiWayTable32,
) (bestDist, bestLen int) {
	if pos+4 > uSize || vt == nil {
		return 0, 0
	}
	srcPtr := unsafe.Pointer(unsafe.SliceData(src))
	w4 := *(*uint32)(unsafe.Add(srcPtr, pos))

	var targetFP uint32
	has8 := pos+8 <= uSize
	if has8 {
		targetFP = *(*uint32)(unsafe.Add(srcPtr, pos+4))
	}

	bestScore := -1000
	bestLen = minLen - 1
	bestDist = 0
	maxSearchLen := min(uSize-pos, 65535)

	calcCost := func(d int) int {
		if d < 128 {
			return 1
		}
		if d < 16384 {
			return 2
		}
		if d < 2097152 {
			return 3
		}
		return 4
	}

	// 8 coprime multipliers yielding 8 distinct 32-way buckets = 256 candidates
	primes := [...]uint32{
		0x1E35A7BD, 0x7FEB352D, 0x85EBCA6B, 0xC2B2AE35,
		0x27D4EB2F, 0x165667B1, 0x9E3779B9, 0x85A04137,
	}

	for _, p := range primes {
		h := int(w4*p>>17) & VectorTable32Mask
		res := vt.Probe(h, int32(pos), targetFP, int32(windowSize))
		if res.ValidMask == 0 {
			continue
		}

		if has8 && res.ExactMask != 0 {
			exactMask := res.ExactMask
			for exactMask != 0 {
				tz := bits.TrailingZeros32(exactMask)
				exactMask &= exactMask - 1

				cand := int(vt.Buckets[h].Pos[tz]) - 1
				if cand < 0 || cand >= pos {
					continue
				}
				d := pos - cand
				if d <= 0 || d >= windowSize {
					continue
				}

				if *(*uint32)(unsafe.Add(srcPtr, cand)) != w4 {
					continue
				}

				extLen := 8
				if pos+8 < uSize && cand+8 < uSize {
					extLen = 8 + HenselLCPPos(src, pos+8, cand+8, maxSearchLen-8)
				}
				score := extLen - calcCost(d)
				if score > bestScore || (score == bestScore && (extLen > bestLen || d < bestDist)) ||
					extLen > bestLen+1 {
					bestScore = score
					bestLen = extLen
					bestDist = d
					if bestLen >= 258 {
						return bestDist, bestLen
					}
				}
			}
			if bestLen >= 32 {
				return bestDist, bestLen
			}
		}

		validMask := res.ValidMask
		for validMask != 0 {
			tz := bits.TrailingZeros32(validMask)
			validMask &= validMask - 1

			cand := int(vt.Buckets[h].Pos[tz]) - 1
			if cand < 0 || cand >= pos {
				continue
			}
			d := pos - cand
			if d <= 0 || d >= windowSize {
				continue
			}

			if has8 && bestLen >= 4 {
				candFP := vt.Buckets[h].FP32[tz]
				diff := candFP ^ targetFP
				if diff != 0 {
					maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
					score := maxLift - calcCost(d)
					if score > bestScore || (score == bestScore && (maxLift > bestLen || d < bestDist)) {
						if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
							bestScore = score
							bestLen = maxLift
							bestDist = d
						}
					}
					continue
				}
			}

			if *(*uint32)(unsafe.Add(srcPtr, cand)) == w4 {
				mLen := HenselLCPPos(src, pos, cand, maxSearchLen)
				score := mLen - calcCost(d)
				if score > bestScore || (score == bestScore && (mLen > bestLen || d < bestDist)) {
					bestScore = score
					bestLen = mLen
					bestDist = d
					if bestLen >= 258 {
						return bestDist, bestLen
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
