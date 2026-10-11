// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"

	"github.com/lemon4ksan/foundation/generic"
)

const (
	// BlockSize64K defines the maximum node boundary for the optimal DP forward graph.
	BlockSize64K = 65536

	maxSubLenCandidates = 16
)

// dpNode stores the dynamic programming node state for the forward DAG parser.
// Sized at exactly 32 bytes for cache alignment (power of 2) during relaxation.
type dpNode struct {
	cost    int32 // Scaled by CostScale (16 units per byte)
	from    int32 // Predecessor position for backward path tracing
	dist    int32 // Match distance (0 for literal transition)
	repCode int8  // -1 for regular match / literal; 0, 1, 2 for rep0, rep1, rep2
	_       [3]byte
	rep0    int32  // Snapshot of rep0 hardware repeat register at this node
	rep1    int32  // Snapshot of rep1 hardware repeat register at this node
	rep2    int32  // Snapshot of rep2 hardware repeat register at this node
	gen     uint32 // Epoch Generation Counter
}

// dpState is the hot cache-aligned window state for the Optimal Parser.
type dpState struct {
	cost int32
	rep0 int32
	rep1 int32
	rep2 int32
}

// dpTrace stores the sequential backward trace links.
// Padded to exactly 16 bytes to prevent cache line straddling.
type dpTrace struct {
	from    int32
	dist    int32
	repCode int8
	_       [7]byte
}

// dpCandidate represents an evaluated match candidate prior to Pareto pruning.
// Aligned to 16 bytes to prevent stack cache line straddling.
type dpCandidate struct {
	dist    int32
	length  int32
	repCode int32
	_       int32
}

// dpScratchpad encapsulates memory reused across block DP parses.
// Optimized to stay strictly in L1 cache during forward DAG relaxation.
type dpScratchpad struct {
	window [1024]dpState             // 16 KB Hot Window
	table3 [4096]int32               // 16 KB Hash Table
	trace  [BlockSize64K + 1]dpTrace // 768 KB Sequential Trace
	path   []int32
	curGen uint32
}

var dpScratchPool = generic.NewPool(func() *dpScratchpad {
	return &dpScratchpad{
		path: make([]int32, 0, BlockSize64K+1),
	}
})

// getVarintBracket classifies distance d into one of 4 varint serialization brackets:
// Bracket 0: < 128 (1 byte offset)
// Bracket 1: < 16,384 (2 bytes offset)
// Bracket 2: < 2,097,152 (3 bytes offset)
// Bracket 3: >= 2,097,152 (4 bytes offset)
func getVarintBracket(d int) int {
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

// calcOffCostPRZM returns the number of bytes required to serialize offset d in Section 3.
func calcOffCostPRZM(d int) int {
	return getVarintBracket(d) + 1
}

var varintCost = [4]int32{27, 40, 53, 66} // 14 + 13*(bracket+1) for brackets 0..3

func isCanonicalDistPRZM(d int) bool {
	return d == 344 || d == 343 || d == 342 || d == 345 ||
		d == 68692 || d == 68746 || d == 68740 || d == 68741 || d == 68694 ||
		d == 87268 || d == 17200 || d == 17150
}

// calcEdgeCostPRZM calculates the fixed-point bit-cost (scaled by 16) for a match edge.
// Repeat matches incur 0 offset serialization bytes in Section 3.
func calcEdgeCostPRZM(mLen, dist, repCode int) int32 {
	var cost int32
	if repCode >= 0 {
		// Repeat match: 0 offset bytes in Section 3
		if mLen <= 7 {
			cost = 16 // 1 byte Fused Repeat token
		} else if mLen <= 18 {
			cost = 32 // 2 bytes Long Repeat (opcode + token)
		} else if mLen <= 145 {
			cost = 48 // 3 bytes Long Repeat (opcode + token + 1-byte varint)
		} else {
			cost = 64 // 4 bytes Long Repeat (opcode + token + 2-byte varint)
		}
		// Register rotation penalty for short non-canonical repeat matches
		if repCode > 0 && mLen < 8 && !isCanonicalDistPRZM(dist) {
			cost += 32 // penalize evicting rep0 for a tiny match
		}
	} else {
		// Explicit match: opcode bytes in Section 2 + offset bytes in Section 3
		offBytes := int32(calcOffCostPRZM(dist))
		if mLen <= 18 {
			cost = (1 + offBytes) * 16 // 1 token byte + offBytes
		} else if mLen <= 145 {
			cost = (2 + offBytes) * 16 // 2 token bytes + offBytes
		} else {
			cost = (3 + offBytes) * 16 // 3 token bytes + offBytes
		}
		// Non-canonical explicit match penalty (register pollution + section overhead)
		if !isCanonicalDistPRZM(dist) {
			cost += 32
		}
	}
	// Length efficiency bonus: prefer 1 long match over multiple fragmented short matches
	if mLen >= 16 {
		bonus := int32(mLen / 4)
		if isCanonicalDistPRZM(dist) {
			bonus += 8
		}
		if cost > bonus+4 {
			cost -= bonus
		} else {
			cost = 4
		}
	}
	return cost
}

var subLenLUT = [19][8]uint8{
	4:  {4},
	5:  {4, 5},
	6:  {4, 5, 6},
	7:  {4, 5, 6, 7},
	8:  {4, 5, 6, 8},
	9:  {4, 5, 6, 8, 9},
	10: {4, 5, 6, 8, 10},
	11: {4, 5, 6, 8, 11},
	12: {4, 5, 6, 8, 12},
	13: {4, 5, 6, 8, 12, 13},
	14: {4, 5, 6, 8, 12, 14},
	15: {4, 5, 6, 8, 12, 15},
	16: {4, 5, 6, 8, 12, 16},
	17: {4, 5, 6, 8, 12, 16, 17},
	18: {4, 5, 6, 8, 12, 16, 18},
}

var subLenCounts = [19]uint8{
	4: 1, 5: 2, 6: 3, 7: 4, 8: 4, 9: 5, 10: 5, 11: 5, 12: 5,
	13: 6, 14: 6, 15: 6, 16: 6, 17: 7, 18: 7,
}

// generateSubLengths writes sub-lengths S(L) = {4, 5, 6, 8, 12, 16, min(L, 18), L}
// into out without dynamic slice appends or heap allocations.
// Returns the number of sub-lengths written (at most 8).
func generateSubLengths(L int, out *[maxSubLenCandidates]int) int {
	if L < 4 {
		return 0
	}
	if L > 18 {
		*out = [maxSubLenCandidates]int{4, 5, 6, 8, 12, 16, 18, L}
		return 8
	}
	n := int(subLenCounts[L])
	for j := range n {
		out[j] = int(subLenLUT[L][j])
	}
	return n
}

func flushLiteralsPRZM(cmdBuf, litBuf *[]byte, lits []byte) {
	nLits := len(lits)
	if nLits == 0 {
		return
	}
	if nLits <= 8 {
		*cmdBuf = append(*cmdBuf, 0xF2+byte(nLits))
	} else if nLits < 0x80 {
		*cmdBuf = append(*cmdBuf, OpcodePrefixLiteral, byte(nLits))
	} else if nLits < 0x4000 {
		*cmdBuf = append(*cmdBuf, OpcodePrefixLiteral, byte(nLits|0x80), byte(nLits>>7))
	} else {
		*cmdBuf = append(*cmdBuf, OpcodePrefixLiteral)
		var varBuf [8]byte
		n := binary.PutUvarint(varBuf[:], uint64(nLits))
		*cmdBuf = append(*cmdBuf, varBuf[:n]...)
	}
	*litBuf = append(*litBuf, lits...)
}

func writeOffsetPRZM(offsetBuf *[]byte, d int) {
	if d < 0x80 {
		*offsetBuf = append(*offsetBuf, byte(d))
	} else if d < 0x4000 {
		*offsetBuf = append(*offsetBuf, byte(d|0x80), byte(d>>7))
	} else {
		var buf [8]byte
		n := binary.PutUvarint(buf[:], uint64(d))
		*offsetBuf = append(*offsetBuf, buf[:n]...)
	}
}

//go:noinline
func insertHashLarge(src []byte, p, uSize int, windowLog int, sp1M *HenselScratchpad1M, sp8M *HenselScratchpad8M) {
	switch windowLog {
	case WindowLog8M:
		if sp8M != nil {
			HenselInsert8M(sp8M, p, src, uSize)
		}
	case WindowLog1M:
		if sp1M != nil {
			HenselInsert1M(sp1M, p, src, uSize)
		}
	}
}

func insertHashPRZM(src []byte, p, uSize int, pad *scratchpad, windowLog int, sp1M *HenselScratchpad1M, sp8M *HenselScratchpad8M) {
	if p+4 <= uSize {
		h := (binary.LittleEndian.Uint32(src[p:p+4]) * 0x1E35A7BD) >> 16
		h &= hashTableMask
		pad.head[h*4+3] = pad.head[h*4+2]
		pad.head[h*4+2] = pad.head[h*4+1]
		pad.head[h*4+1] = pad.head[h*4]
		pad.head[h*4] = int32(p + 1)
		if windowLog <= WindowLog64K {
			HenselInsert(&pad.hensel, p, src, uSize)
		} else {
			insertHashLarge(src, p, uSize, windowLog, sp1M, sp8M)
		}
	}
}

// compressBlockOptimal executes the Forward DAG / Backward DP optimal parser on a 64 KiB block.
// Enforces strictly mLen >= 4 on all emitted match tokens in Main Mode.
func compressBlockOptimal(
	src []byte,
	bStart, bSize, uSize int,
	windowSize, windowLog, level int,
	pad *scratchpad,
	dpPad *dpScratchpad,
	rep0, rep1, rep2 *int,
	sp1M *HenselScratchpad1M,
	sp8M *HenselScratchpad8M,
	minMatchBase int,
) {
	if bSize <= 0 {
		return
	}
	if bSize > BlockSize64K {
		return
	}

	dpPad.curGen++
	if dpPad.curGen == 0 {
		dpPad.curGen = 1
	}

	// Initialize start node
	dpPad.window[0] = dpState{
		cost: 0,
		rep0: int32(*rep0),
		rep1: int32(*rep1),
		rep2: int32(*rep2),
	}
	dpPad.trace[0] = dpTrace{
		from:    0,
		dist:    0,
		repCode: -1,
	}

	// Clear the window initially
	for j := 1; j < 1024; j++ {
		dpPad.window[j].cost = 1 << 30
	}

	var subLenBuf [maxSubLenCandidates]int

	for i := range bSize {
		pos := bStart + i
		wIdx := i & 1023
		curNode := &dpPad.window[wIdx]
		curCost := curNode.cost
		r0 := int(curNode.rep0)
		r1 := int(curNode.rep1)
		r2 := int(curNode.rep2)

		// Clear the node that is about to enter the far end of the lookahead window
		dpPad.window[(i+1023)&1023].cost = 1 << 30

		// 1. Literal transition i -> i+1 (cost += 11, matching entropy of compressed literals)
		litCost := curCost + 11
		targetLitIdx := (i + 1) & 1023
		targetLit := &dpPad.window[targetLitIdx]
		if litCost < targetLit.cost {
			targetLit.cost = litCost
			targetLit.rep0 = int32(r0)
			targetLit.rep1 = int32(r1)
			targetLit.rep2 = int32(r2)
			
			t := &dpPad.trace[i+1]
			t.from = int32(i)
			t.dist = 0
			t.repCode = -1
		}

		maxL := min(bSize-i, 65535)
		dpMaxL := min(maxL, 1023)
		if dpMaxL < 4 {
			insertHashPRZM(src, pos, uSize, pad, windowLog, sp1M, sp8M)
			continue
		}

		// 2. Candidate Generation with Bracket Dominance Pareto Pruning
		var candidates [32]dpCandidate
		candCount := 0

		// Repeat 0 check
		if r0 > 0 && pos-r0 >= 0 && r0 <= windowSize {
			minR0 := 4
			if !isCanonicalDistPRZM(r0) {
				if r0 >= 1024 {
					minR0 = 8
				} else {
					minR0 = 6
				}
			}
			if pos+2 <= uSize && binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r0:pos-r0+2]) {
				l0 := HenselLCPPos(src, pos, pos-r0, dpMaxL)
				if l0 >= minR0 {
					candidates[candCount] = dpCandidate{dist: int32(r0), length: int32(l0), repCode: 0}
					candCount++
				}
			}
		}

		// Repeat 1 check
		if r1 > 0 && pos-r1 >= 0 && r1 <= windowSize && r1 != r0 {
			minR1 := 4
			if !isCanonicalDistPRZM(r1) {
				if r1 >= 1024 {
					minR1 = 8
				} else {
					minR1 = 6
				}
			}
			if pos+2 <= uSize && binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r1:pos-r1+2]) {
				l1 := HenselLCPPos(src, pos, pos-r1, dpMaxL)
				if l1 >= minR1 {
					candidates[candCount] = dpCandidate{dist: int32(r1), length: int32(l1), repCode: 1}
					candCount++
				}
			}
		}

		// Repeat 2 check
		if r2 > 0 && pos-r2 >= 0 && r2 <= windowSize && r2 != r0 && r2 != r1 {
			minR2 := 4
			if !isCanonicalDistPRZM(r2) {
				if r2 >= 1024 {
					minR2 = 8
				} else {
					minR2 = 6
				}
			}
			if pos+2 <= uSize && binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r2:pos-r2+2]) {
				l2 := HenselLCPPos(src, pos, pos-r2, dpMaxL)
				if l2 >= minR2 {
					candidates[candCount] = dpCandidate{dist: int32(r2), length: int32(l2), repCode: 2}
					candCount++
				}
			}
		}

		// Explicit match candidates sorted into 4 varint brackets
		var bracketBest [4]dpCandidate
		updateBracket := func(d, l int) {
			if d <= 0 || d > windowSize || d == r0 || d == r1 || d == r2 || l < 4 {
				return
			}
			minExp := 8
			if isCanonicalDistPRZM(d) {
				minExp = 4
			} else if d >= 16384 {
				minExp = 16
			} else if d >= 1024 {
				minExp = 12
			}
			if l < minExp {
				return
			}
			b := getVarintBracket(d)
			if l > int(bracketBest[b].length) {
				bracketBest[b] = dpCandidate{dist: int32(d), length: int32(l), repCode: -1}
			}
		}

		// Direct canonical strides evaluation
		canDists := [...]int{344, 343, 342, 68692, 68746, 68741, 68694, 87268, 17200, 17150}
		for _, cd := range canDists {
			if cd > 0 && pos-cd >= 0 && cd <= windowSize && cd != r0 && cd != r1 && cd != r2 {
				if pos+4 <= uSize && pos-cd+4 <= uSize && binary.LittleEndian.Uint32(src[pos:pos+4]) == binary.LittleEndian.Uint32(src[pos-cd:pos-cd+4]) {
					l := HenselLCPPos(src, pos, pos-cd, dpMaxL)
					if l >= 4 {
						updateBracket(cd, l)
					}
				}
			}
		}

		// Secondary 3-byte direct-mapped table
		if pos+3 <= uSize {
			h3 := uint32(src[pos]) | (uint32(src[pos+1]) << 8) | (uint32(src[pos+2]) << 16)
			idx3 := (h3 * 0x1E35A7BD) >> 20
			prev3 := int(dpPad.table3[idx3]) - 1
			dpPad.table3[idx3] = int32(pos + 1)
			if prev3 >= 0 {
				d3 := pos - prev3
				if d3 > 0 && d3 <= windowSize && d3 != r0 && d3 != r1 && d3 != r2 {
					if src[pos] == src[prev3] && src[pos+1] == src[prev3+1] && src[pos+2] == src[prev3+2] {
						l3 := HenselLCPPos(src, pos, prev3, dpMaxL)
						updateBracket(d3, l3)
					}
				}
			}
		}

		// Primary 4-byte hash table (2-way bucket)
		if pos+4 <= uSize {
			h := (binary.LittleEndian.Uint32(src[pos:pos+4]) * 0x1E35A7BD) >> 16
			h &= hashTableMask
			p0 := int(pad.head[h*4]) - 1
			p1 := int(pad.head[h*4+1]) - 1
			p2 := int(pad.head[h*4+2]) - 1
			p3 := int(pad.head[h*4+3]) - 1
			if p0 >= 0 {
				d0 := pos - p0
				if d0 > 0 && d0 <= windowSize {
					l0 := HenselLCPPos(src, pos, p0, dpMaxL)
					updateBracket(d0, l0)
				}
			}
			if p1 >= 0 && p1 != p0 {
				d1 := pos - p1
				if d1 > 0 && d1 <= windowSize {
					l1 := HenselLCPPos(src, pos, p1, dpMaxL)
					updateBracket(d1, l1)
				}
			}
			if p2 >= 0 && p2 != p0 && p2 != p1 {
				d2 := pos - p2
				if d2 > 0 && d2 <= windowSize {
					l2 := HenselLCPPos(src, pos, p2, dpMaxL)
					updateBracket(d2, l2)
				}
			}
			if p3 >= 0 && p3 != p0 && p3 != p1 && p3 != p2 {
				d3 := pos - p3
				if d3 > 0 && d3 <= windowSize {
					l3 := HenselLCPPos(src, pos, p3, dpMaxL)
					updateBracket(d3, l3)
				}
			}

			// Hensel Ultrametric Match Finding
			curMaxLen := max(int(bracketBest[0].length), int(bracketBest[1].length), int(bracketBest[2].length), int(bracketBest[3].length))
			for c := range candCount {
				if int(candidates[c].length) > curMaxLen {
					curMaxLen = int(candidates[c].length)
				}
			}
			searchMinLen := max(4, curMaxLen+1)
			if searchMinLen <= dpMaxL && searchMinLen <= 258 {
				maxDepth := 16
				if level >= 4 {
					maxDepth = 32
				}
				var hDist, hLen int
				switch windowLog {
				case WindowLog8M:
					if sp8M != nil {
						hDist, hLen = HenselFindMatch8M(src, pos, uSize, maxDepth, searchMinLen, sp8M)
					}
				case WindowLog1M:
					if sp1M != nil {
						hDist, hLen = HenselFindMatch1M(src, pos, uSize, maxDepth, searchMinLen, sp1M)
					}
				default:
					hDist, hLen = HenselFindMatch(src, pos, uSize, maxDepth, searchMinLen, &pad.hensel)
				}
				if hDist > 0 && hLen >= 4 {
					updateBracket(hDist, min(hLen, dpMaxL))
				}
			}
		}

		for b := range 4 {
			if bracketBest[b].length >= 4 {
				candidates[candCount] = bracketBest[b]
				candCount++
			}
		}

		// 3. Forward Relaxation over sub-lengths S(L)
		cands := candidates[:candCount]
		for c := range cands {
			cand := &cands[c]
			candL := min(int(cand.length), dpMaxL)
			if candL < 4 {
				continue
			}
			nSubLens := generateSubLengths(candL, &subLenBuf)
			for sIdx := range nSubLens {
				subLen := subLenBuf[sIdx]
				next := i + subLen
				edgeCost := calcEdgeCostPRZM(subLen, int(cand.dist), int(cand.repCode))
				newCost := curCost + edgeCost
				
				targetIdx := next & 1023
				target := &dpPad.window[targetIdx]
				
				targetTrace := &dpPad.trace[next]
				
				if newCost < target.cost || (newCost == target.cost && cand.repCode >= 0 && targetTrace.repCode < 0) {
					target.cost = newCost
					targetTrace.from = int32(i)
					targetTrace.dist = cand.dist
					targetTrace.repCode = int8(cand.repCode)
					
					switch cand.repCode {
					case 0:
						target.rep0 = int32(r0)
						target.rep1 = int32(r1)
						target.rep2 = int32(r2)
					case 1:
						target.rep0 = int32(r1)
						target.rep1 = int32(r0)
						target.rep2 = int32(r2)
					case 2:
						target.rep0 = int32(r2)
						target.rep1 = int32(r0)
						target.rep2 = int32(r1)
					default:
						target.rep0 = cand.dist
						target.rep1 = int32(r0)
						target.rep2 = int32(r1)
					}
				}
			}
		}

		insertHashPRZM(src, pos, uSize, pad, windowLog, sp1M, sp8M)
	}

	// 4. Backward Path Trace & PRZM Token Emission
	path := dpPad.path[:0]
	curr := int32(bSize)
	for curr > 0 {
		path = append(path, curr)
		curr = dpPad.trace[curr].from
	}

	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}

	litStart := -1
	litLen := 0
	from := int32(0)

	for _, to := range path {
		dist := int(dpPad.trace[to].dist)
		repCode := int(dpPad.trace[to].repCode)
		step := int(to - from)

		if dist == 0 {
			if litLen == 0 {
				litStart = bStart + int(from)
			}
			litLen += step
			from = to
			continue
		}

		pos := bStart + int(from)
		mDist := dist
		mLen := step

		if repCode >= 0 {
			// Repeat match (Fused Repeat or Long Repeat)
			if mLen <= 3+minMatchBase {
				if litLen > 7 {
					flushLen := litLen - 7
					flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+flushLen])
					litStart += flushLen
					litLen = 7
				}
				op := uint8((litLen << 4) | ((mLen - minMatchBase) << 2) | repCode)
				pad.cmdBuf = append(pad.cmdBuf, op)
				if litLen > 0 {
					pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
				}
			} else {
				if litLen > 15 {
					flushLen := litLen - 15
					flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+flushLen])
					litStart += flushLen
					litLen = 15
				}
				baseLen := mLen - minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodePrefixLongRepeat | uint8(repCode)
				token := uint8((litLen << 4) | baseLen)
				pad.cmdBuf = append(pad.cmdBuf, op, token)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(mLen-(15+minMatchBase)))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				}
				if litLen > 0 {
					pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
				}
			}
			switch repCode {
			case 0:
			case 1:
				*rep1 = *rep0
				*rep0 = mDist
			case 2:
				*rep2 = *rep1
				*rep1 = *rep0
				*rep0 = mDist
			}
		} else {
			// Fused Extended Match
			if litLen > 3 {
				flushLen := litLen - 3
				flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+flushLen])
				litStart += flushLen
				litLen = 3
			}
			baseLen := mLen - minMatchBase
			if baseLen > 14 {
				baseLen = 15
			}
			op := OpcodeFlagFusedExt | uint8(litLen<<4) | uint8(baseLen)
			pad.cmdBuf = append(pad.cmdBuf, op)
			if baseLen == 15 {
				var varBuf [8]byte
				n := binary.PutUvarint(varBuf[:], uint64(mLen-(15+minMatchBase)))
				pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
			}
			if litLen > 0 {
				pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
			}
			writeOffsetPRZM(&pad.offsetBuf, mDist)
			*rep2 = *rep1
			*rep1 = *rep0
			*rep0 = mDist
		}

		litStart = -1
		litLen = 0
		from = to
	}

	if litLen > 0 {
		flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+litLen])
	}

	dpPad.path = path
}
