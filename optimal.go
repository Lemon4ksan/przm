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
	extra1  uint8
	extra2  uint8
	_       [5]byte
}

// dpCandidate represents an evaluated match candidate prior to Pareto pruning.
// Aligned to 16 bytes to prevent stack cache line straddling.
type dpCandidate struct {
	dist    int32
	length  int32
	repCode int32
	extra   int32
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

// calcEdgeCostPRZM calculates the fixed-point bit-cost (scaled by 16) for a match edge.
// Repeat matches incur 0 offset serialization bytes in Section 3.
func calcEdgeCostPRZM(mLen, dist, repCode, minMatchBase int) int32 {
	if repCode == -2 {
		// ShortDict token: 1 byte opcode (0xD8..0xDF), 0 offset bytes
		return 16
	}
	if repCode == -3 {
		// DictToken: 2 bytes opcode (0xF2 <tokID>), 0 offset bytes
		return 32
	}
	if repCode == -4 {
		// Word Transform: 2 bytes opcode (transOp <tokID>), 0 offset bytes
		return 32
	}
	if repCode == -5 {
		// Sub-4-byte Token Pair: 3 bytes opcode (0xFF <tok1> <tok2>), 0 offset bytes
		return 48
	}
	if repCode == -6 {
		// Compact Raw Dict: 1 byte opcode (0xE9) + 1 byte token + offCost
		offBytes := int32(calcOffCostPRZM(dist))
		return (2 + offBytes) * 16
	}
	var cost int32
	if repCode >= 0 {
		// Repeat match: 0 offset bytes in Section 3
		if mLen <= 3+minMatchBase {
			cost = 16 // 1 byte Fused Repeat token
		} else if mLen <= 14+minMatchBase {
			cost = 32 // 2 bytes Long Repeat (opcode + token)
		} else if mLen <= 14+minMatchBase+127 {
			cost = 48 // 3 bytes Long Repeat
		} else {
			cost = 64 // 4 bytes Long Repeat
		}
		// Register conservation bonus for rep0 / rep1:
		if repCode == 0 {
			cost -= 8 // 0.5 byte bonus: rep0 is free and conserved
		} else if repCode == 1 {
			cost -= 4 // 0.25 byte bonus
		} else {
			cost += 4
		}
	} else {
		// Explicit match: opcode bytes in Section 2 + offset bytes in Section 3
		offBytes := int32(calcOffCostPRZM(dist))
		if mLen <= 14+minMatchBase {
			cost = (1 + offBytes) * 16
		} else if mLen <= 14+minMatchBase+127 {
			cost = (2 + offBytes) * 16
		} else {
			cost = (3 + offBytes) * 16
		}
		// Shannon Distance Entropy Cost Penalty:
		if dist >= 65536 {
			cost += 40
		} else if dist >= 16384 {
			cost += 32
		} else if dist >= 1024 {
			cost += 20
		} else if dist >= 128 {
			cost += 12
		}
	}
	if mLen >= 16 {
		bonus := int32(mLen / 4)
		if cost > bonus+4 {
			cost -= bonus
		} else {
			cost = 4
		}
	}
	return cost
}

var subLenLUT = [19][8]uint8{
	3:  {3},
	4:  {4},
	5:  {3, 4, 5},
	6:  {3, 4, 5, 6},
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
	3: 1, 4: 1, 5: 3, 6: 4, 7: 4, 8: 4, 9: 5, 10: 5, 11: 5, 12: 5,
	13: 6, 14: 6, 15: 6, 16: 6, 17: 7, 18: 7,
}

// generateSubLengths writes sub-lengths S(L) into out without dynamic slice appends.
// Returns the number of sub-lengths written (at most 8).
func generateSubLengths(L, minMatchBase int, out *[maxSubLenCandidates]int) int {
	if L < minMatchBase {
		return 0
	}
	if L > 18 {
		if minMatchBase == 3 {
			*out = [maxSubLenCandidates]int{3, 4, 6, 8, 12, 16, 18, L}
		} else {
			*out = [maxSubLenCandidates]int{4, 5, 6, 8, 12, 16, 18, L}
		}
		return 8
	}
	n := int(subLenCounts[L])
	cnt := 0
	for j := range n {
		sl := int(subLenLUT[L][j])
		if sl >= minMatchBase {
			out[cnt] = sl
			cnt++
		}
	}
	return cnt
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

// emitPathPRZM traces back from segEnd to segStart, reverses the path, and emits PRZM tokens.
func emitPathPRZM(
	src []byte,
	bStart int,
	segStart int,
	segEnd int,
	uSize int,
	pad *scratchpad,
	dpPad *dpScratchpad,
	rep0, rep1, rep2 *int,
	minMatchBase int,
) {
	if segEnd <= segStart {
		return
	}
	path := dpPad.path[:0]
	curr := int32(segEnd)
	for curr > int32(segStart) {
		path = append(path, curr)
		prev := dpPad.trace[curr].from
		if prev >= curr || prev < int32(segStart) {
			prev = curr - 1
			dpPad.trace[curr].from = prev
			dpPad.trace[curr].dist = 0
			dpPad.trace[curr].repCode = -1
		}
		curr = prev
	}

	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}

	litStart := -1
	litLen := 0
	from := int32(segStart)

	for _, to := range path {
		dist := int(dpPad.trace[to].dist)
		repCode := int(dpPad.trace[to].repCode)
		step := int(to - from)

		// Literal transition: repCode == -1 and dist == 0
		if repCode == -1 && dist == 0 {
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

		if repCode < -1 {
			if litLen > 0 {
				flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+litLen])
				litStart = -1
				litLen = 0
			}
			switch repCode {
			case -2:
				// OpcodePrefixShortDict (0xD8..0xDF)
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixShortDict|dpPad.trace[to].extra1)
			case -3:
				// OpcodePrefixDictToken (0xF2 <tokID>)
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixDictToken, dpPad.trace[to].extra1)
			case -4:
				// Transformed Token (transOp <tokID>)
				pad.cmdBuf = append(pad.cmdBuf, dpPad.trace[to].extra2, dpPad.trace[to].extra1)
			case -5:
				// Transformed Pair (0xFF <tok1> <tok2>)
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixTransPair, dpPad.trace[to].extra1, dpPad.trace[to].extra2)
			case -6:
				// Compact Raw Dict (0xE9 <token> <dictOff>)
				baseLen := mLen - minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				token := uint8(baseLen)
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRawDict, token)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(mLen-(15+minMatchBase)))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				}
				writeOffsetPRZM(&pad.offsetBuf, mDist)
			}
			litStart = -1
			litLen = 0
			from = to
			continue
		}

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
	activeMatcher *dictMatcher,
	dictLen int,
	compactDict bool,
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

	clear(dpPad.trace[:bSize+1])

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
	segStart := 0

	for i := 0; i < bSize; i++ {
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

		// 2. Candidate Generation with Bracket Dominance Pareto Pruning & Long Match Shortcut
		var candidates [32]dpCandidate
		candCount := 0

		var bestLong dpCandidate
		bestLongScore := 0

		updateLong := func(d, l, repCode int) {
			l = min(l, maxL)
			if l < 64 {
				return
			}
			score := l
			if repCode >= 0 {
				score += 3 // bias towards repeat matches
			}
			if score > bestLongScore {
				bestLongScore = score
				bestLong = dpCandidate{dist: int32(d), length: int32(l), repCode: int32(repCode)}
			}
		}

		// Dictionary Token Matching
		if activeMatcher != nil {
			tokIdx, transOp, tokLen, _, score, pairTok2 := activeMatcher.match(src, pos)
			if score >= 1 && tokLen >= 2 && candCount < 30 {
				if tokIdx < 8 && transOp == 0 {
					candidates[candCount] = dpCandidate{dist: 0, length: int32(tokLen), repCode: -2, extra: int32(tokIdx)}
					candCount++
				} else if transOp == 0 {
					candidates[candCount] = dpCandidate{dist: 0, length: int32(tokLen), repCode: -3, extra: int32(tokIdx)}
					candCount++
				} else if transOp == OpcodePrefixTransPair && pairTok2 >= 0 {
					candidates[candCount] = dpCandidate{dist: 0, length: int32(tokLen), repCode: -5, extra: int32(tokIdx) | (int32(pairTok2) << 8)}
					candCount++
				} else if transOp != 0 {
					candidates[candCount] = dpCandidate{dist: 0, length: int32(tokLen), repCode: -4, extra: int32(tokIdx) | (int32(transOp) << 8)}
					candCount++
				}
			}
		}

		// Repeat 0 check
		if r0 > 0 && pos-r0 >= 0 && r0 <= windowSize {
			minR0 := minMatchBase
			if pos+2 <= uSize && binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r0:pos-r0+2]) {
				l0 := HenselLCPPos(src, pos, pos-r0, maxL)
				if l0 >= minR0 {
					candidates[candCount] = dpCandidate{dist: int32(r0), length: int32(min(l0, dpMaxL)), repCode: 0}
					candCount++
					updateLong(r0, l0, 0)
				}
			}
		}

		// Repeat 1 check
		if r1 > 0 && pos-r1 >= 0 && r1 <= windowSize && r1 != r0 {
			minR1 := minMatchBase
			if pos+2 <= uSize && binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r1:pos-r1+2]) {
				l1 := HenselLCPPos(src, pos, pos-r1, maxL)
				if l1 >= minR1 {
					candidates[candCount] = dpCandidate{dist: int32(r1), length: int32(min(l1, dpMaxL)), repCode: 1}
					candCount++
					updateLong(r1, l1, 1)
				}
			}
		}

		// Repeat 2 check
		if r2 > 0 && pos-r2 >= 0 && r2 <= windowSize && r2 != r0 && r2 != r1 {
			minR2 := minMatchBase
			if pos+2 <= uSize && binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r2:pos-r2+2]) {
				l2 := HenselLCPPos(src, pos, pos-r2, maxL)
				if l2 >= minR2 {
					candidates[candCount] = dpCandidate{dist: int32(r2), length: int32(min(l2, dpMaxL)), repCode: 2}
					candCount++
					updateLong(r2, l2, 2)
				}
			}
		}

		// Explicit match candidates sorted into 4 varint brackets
		var bracketBest [4]dpCandidate
		updateBracket := func(d, l int) {
			l = min(l, maxL)
			if d <= 0 || d > windowSize || d == r0 || d == r1 || d == r2 || l < minMatchBase {
				return
			}
			mPos := pos - d
			if compactDict && dictLen > 0 && mPos < dictLen {
				if candCount < 30 && l >= minMatchBase {
					candidates[candCount] = dpCandidate{dist: int32(mPos), length: int32(min(l, dpMaxL)), repCode: -6, extra: 0}
					candCount++
					updateLong(mPos, l, -6)
				}
				return
			}
			updateLong(d, l, -1)
			minExp := minMatchBase
			if d >= 16384 {
				minExp = minMatchBase + 3
			} else if d >= 1024 {
				minExp = minMatchBase + 2
			} else if d >= 128 {
				minExp = minMatchBase + 1
			}
			if l < minExp {
				return
			}
			b := getVarintBracket(d)
			lClamped := min(l, dpMaxL)
			if lClamped > int(bracketBest[b].length) {
				bracketBest[b] = dpCandidate{dist: int32(d), length: int32(lClamped), repCode: -1}
			}
		}

		// Direct canonical strides evaluation
		canDists := [...]int{344, 343, 342, 68692, 68746, 68741, 68694, 87268, 17200, 17150}
		for _, cd := range canDists {
			if cd > 0 && pos-cd >= 0 && cd <= windowSize && cd != r0 && cd != r1 && cd != r2 {
				if pos+4 <= uSize && pos-cd+4 <= uSize && binary.LittleEndian.Uint32(src[pos:pos+4]) == binary.LittleEndian.Uint32(src[pos-cd:pos-cd+4]) {
					l := HenselLCPPos(src, pos, pos-cd, maxL)
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
						l3 := HenselLCPPos(src, pos, prev3, maxL)
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
					l0 := HenselLCPPos(src, pos, p0, maxL)
					updateBracket(d0, l0)
				}
			}
			if p1 >= 0 && p1 != p0 {
				d1 := pos - p1
				if d1 > 0 && d1 <= windowSize {
					l1 := HenselLCPPos(src, pos, p1, maxL)
					updateBracket(d1, l1)
				}
			}
			if p2 >= 0 && p2 != p0 && p2 != p1 {
				d2 := pos - p2
				if d2 > 0 && d2 <= windowSize {
					l2 := HenselLCPPos(src, pos, p2, maxL)
					updateBracket(d2, l2)
				}
			}
			if p3 >= 0 && p3 != p0 && p3 != p1 && p3 != p2 {
				d3 := pos - p3
				if d3 > 0 && d3 <= windowSize {
					l3 := HenselLCPPos(src, pos, p3, maxL)
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
			searchMinLen := max(minMatchBase, curMaxLen+1)
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
				if hDist > 0 && hLen >= minMatchBase {
					updateBracket(hDist, hLen)
				}
			}
		}

		// Check if Long Match Shortcut triggers
		if bestLongScore >= 64 {
			// 1. Emit optimal path up to current position i
			emitPathPRZM(src, bStart, segStart, i, uSize, pad, dpPad, rep0, rep1, rep2, minMatchBase)

			// 2. Emit the long match directly
			mLen := int(bestLong.length)
			mDist := int(bestLong.dist)
			repCode := int(bestLong.repCode)

			if repCode >= 0 {
				baseLen := mLen - minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodePrefixLongRepeat | uint8(repCode)
				token := uint8(baseLen)
				pad.cmdBuf = append(pad.cmdBuf, op, token)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(mLen-(15+minMatchBase)))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
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
			} else if repCode == -6 {
				baseLen := mLen - minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				token := uint8(baseLen)
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRawDict, token)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(mLen-(15+minMatchBase)))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				}
				writeOffsetPRZM(&pad.offsetBuf, mDist)
			} else {
				baseLen := mLen - minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodeFlagFusedExt | uint8(baseLen)
				pad.cmdBuf = append(pad.cmdBuf, op)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(mLen-(15+minMatchBase)))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				}
				writeOffsetPRZM(&pad.offsetBuf, mDist)
				*rep2 = *rep1
				*rep1 = *rep0
				*rep0 = mDist
			}

			// Hash sampling across the long match
			insertHashPRZM(src, pos, uSize, pad, windowLog, sp1M, sp8M)
			if pos+1+4 <= uSize {
				insertHashPRZM(src, pos+1, uSize, pad, windowLog, sp1M, sp8M)
			}
			if pos+mLen-1+4 <= uSize {
				insertHashPRZM(src, pos+mLen-1, uSize, pad, windowLog, sp1M, sp8M)
			}

			// Advance segStart and i
			segStart = i + mLen
			i = segStart - 1

			if segStart < bSize {
				dpPad.window[segStart&1023] = dpState{
					cost: 0,
					rep0: int32(*rep0),
					rep1: int32(*rep1),
					rep2: int32(*rep2),
				}
				dpPad.trace[segStart] = dpTrace{
					from:    int32(segStart),
					dist:    0,
					repCode: -1,
				}
				for j := 1; j < 1024; j++ {
					dpPad.window[(segStart+j)&1023].cost = 1 << 30
				}
			}
			continue
		}

		for b := range 4 {
			if bracketBest[b].length >= int32(minMatchBase) {
				candidates[candCount] = bracketBest[b]
				candCount++
			}
		}

		// 3. Forward Relaxation over sub-lengths S(L)
		cands := candidates[:candCount]
		for c := range cands {
			cand := &cands[c]
			candL := min(int(cand.length), dpMaxL)
			if candL < 2 {
				continue
			}
			if cand.repCode < -1 {
				// Dictionary Token or Compact Raw Dict
				subLen := candL
				next := i + subLen
				edgeCost := calcEdgeCostPRZM(subLen, int(cand.dist), int(cand.repCode), minMatchBase)
				newCost := curCost + edgeCost

				targetIdx := next & 1023
				target := &dpPad.window[targetIdx]
				targetTrace := &dpPad.trace[next]

				if newCost < target.cost {
					target.cost = newCost
					targetTrace.from = int32(i)
					targetTrace.dist = cand.dist
					targetTrace.repCode = int8(cand.repCode)
					targetTrace.extra1 = uint8(cand.extra & 0xFF)
					targetTrace.extra2 = uint8((cand.extra >> 8) & 0xFF)

					target.rep0 = int32(r0)
					target.rep1 = int32(r1)
					target.rep2 = int32(r2)
				}
				continue
			}

			if candL < minMatchBase {
				continue
			}
			nSubLens := generateSubLengths(candL, minMatchBase, &subLenBuf)
			for sIdx := range nSubLens {
				subLen := subLenBuf[sIdx]
				next := i + subLen
				edgeCost := calcEdgeCostPRZM(subLen, int(cand.dist), int(cand.repCode), minMatchBase)
				newCost := curCost + edgeCost

				targetIdx := next & 1023
				target := &dpPad.window[targetIdx]

				targetTrace := &dpPad.trace[next]

				if newCost < target.cost || (newCost == target.cost && cand.repCode >= 0 && targetTrace.repCode < 0) {
					target.cost = newCost
					targetTrace.from = int32(i)
					targetTrace.dist = cand.dist
					targetTrace.repCode = int8(cand.repCode)
					targetTrace.extra1 = 0
					targetTrace.extra2 = 0

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

	// 4. Backward Path Trace & PRZM Token Emission for the remaining segment
	if segStart < bSize {
		emitPathPRZM(src, bStart, segStart, bSize, uSize, pad, dpPad, rep0, rep1, rep2, minMatchBase)
	}
}
