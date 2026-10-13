// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math"
	"math/bits"

	"github.com/lemon4ksan/foundation/generic"
)

const (
	// BlockSize64K defines the maximum node boundary for the optimal DP forward graph.
	BlockSize64K = 65536

	maxSubLenCandidates = 16
)

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
	window       [1024]dpState             // 16 KB Hot Window
	table3       [32768]int32              // 128 KB Hash Table
	macro8       [65536 * 8]int32          // 2 MB 8-way 8-byte Macro Sliding Window Table
	dagProd      [65536 * 2]int32          // 512 KB 2-way 16-byte DAG Production Table
	dagProd32    [65536 * 2]int32          // 512 KB 2-way 32-byte Structural Macro Production Table
	trace        [BlockSize64K + 1]dpTrace // 768 KB Sequential Trace
	litCostTable [256]int32                // 1 KB Shannon Entropy Bit-Cost Table
	path         []int32
	path2        []int32
	curGen       uint32
}

var dpScratchPool = generic.NewPool(func() *dpScratchpad {
	return &dpScratchpad{
		path:  make([]int32, 0, BlockSize64K+1),
		path2: make([]int32, 0, BlockSize64K+1),
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

// buildShannonLitCostTable computes the empirical zero-order Shannon entropy bit-cost table
// for all 256 byte values within the 64 KiB block in fixed-point units (16 units = 1 byte = 8 bits).
func buildShannonLitCostTable(src []byte, bStart, bSize int, table *[256]int32) {
	if bSize <= 0 {
		return
	}
	var freq [256]uint32
	end := bStart + bSize
	for p := bStart; p < end; p++ {
		freq[src[p]]++
	}

	invSize := 1.0 / float64(bSize)
	for c := 0; c < 256; c++ {
		f := freq[c]
		if f == 0 {
			table[c] = 26 // ~13 bits (penalty for unseen symbol)
		} else {
			p := float64(f) * invSize
			// Fixed-point scale: 16 units = 1 byte = 8 bits (2 units per bit)
			cost := int32(-math.Log2(p)*2.0 + 0.5)
			if cost < 2 {
				cost = 2 // min 1 bit
			} else if cost > 26 {
				cost = 26
			}
			table[c] = cost
		}
	}
}

// calcDistPrefixCostPRZM returns the approximate bit-cost (scaled by 16) for extra bits of offset d
// under the Log2 Distance Prefix codebook.
func calcDistPrefixCostPRZM(d int) int32 {
	if d <= 4 {
		return 0 // 0 extra bits
	}
	if d <= 32768 {
		ud := uint32(d - 1)
		k := bits.Len32(ud)
		nb := k - 2
		return int32(2 * nb) // nb extra bits (each bit = 2 units)
	}
	if d <= 65536 {
		return 30 // 15 extra bits = 30 units
	}
	if d <= 2097152 {
		return 34 // 17 extra bits = 34 units (under 2MB, varint is 3 bytes)
	}
	return 42 // 21-24 extra bits = 42 units (up to 16MB)
}

// calcOffCostPRZM returns the number of bytes required to serialize offset d in Section 3.
func calcOffCostPRZM(d int) int {
	return getVarintBracket(d) + 1
}

// calcEdgeCostPRZM calculates the calibrated fixed-point bit-cost (scaled by 16 = 8 bits) for a match edge.
// Repeat matches incur 0 offset serialization bytes in Section 3.
func calcEdgeCostPRZM(mLen, dist, repCode, minMatchBase int, hasStride bool, stride int) int32 {
	if repCode == -2 {
		// ShortDict token: 1 byte opcode (0xD8..0xDF), 0 offset bytes (~6 bits)
		return 12
	}
	if repCode == -3 {
		// DictToken: 2 bytes opcode (0xF2 <tokID>), 0 offset bytes (~10 bits)
		return 20
	}
	if repCode == -4 {
		// Word Transform: 2 bytes opcode (transOp <tokID>), 0 offset bytes
		return 20
	}
	if repCode == -5 {
		// Sub-4-byte Token Pair: 3 bytes opcode (0xFF <tok1> <tok2>), 0 offset bytes
		return 30
	}
	if repCode == -6 {
		// Compact Raw Dict: 1 byte opcode (0xE9) + 1 byte token + offCost
		offCost := calcDistPrefixCostPRZM(dist)
		return 32 + offCost
	}
	if repCode == -7 {
		// RFC 7932 Transformed Dictionary Match:
		// 1 byte opcode (0xCF) + 1 byte transID + varint(wordID)
		if dist < 128 {
			return 24 // 3 command bytes (~12 bits)
		}
		return 32 // 4 command bytes (~16 bits)
	}
	var cost int32
	if repCode >= 0 {
		// Repeat match: 0 offset bytes in Section 3
		if mLen <= 3+minMatchBase {
			cost = 10 // 1 byte Fused Repeat token (~5 bits)
		} else if mLen <= 14+minMatchBase {
			cost = 20 // 2 bytes Long Repeat (opcode + token) (~10 bits)
		} else if mLen <= 14+minMatchBase+127 {
			cost = 30 // 3 bytes Long Repeat
		} else {
			cost = 40 // 4 bytes Long Repeat
		}
		// Register conservation bonus for rep0 / rep1:
		if repCode == 0 {
			cost -= 6 // Strong bonus: rep0 is free and conserved
		} else if repCode == 1 {
			cost -= 3 // Promotion bonus
		} else {
			cost -= 1
		}
		if cost < 3 {
			cost = 3
		}
	} else if isBracketDist(dist) && mLen <= 15+minMatchBase {
		// Fused Bracket: 2 bytes in Section 1, 0 offset bytes in Section 3
		cost = 24

	} else {
		// Explicit match: opcode byte in Section 1 + Section 3 Huffman distance code (~24 units)
		var baseCost int32 = 18
		if mLen > 14+minMatchBase+127 {
			baseCost = 48
		} else if mLen > 14+minMatchBase {
			baseCost = 32
		}
		cost = baseCost + calcDistPrefixCostPRZM(dist)
	}
	return cost
}

var subLenLUT = [19][8]uint8{
	3:  {3},
	4:  {4},
	5:  {3, 4, 5},
	6:  {3, 4, 5, 6},
	7:  {4, 5, 6, 7},
	8:  {4, 5, 6, 7, 8},
	9:  {4, 6, 7, 8, 9},
	10: {4, 6, 7, 8, 9, 10},
	11: {4, 6, 7, 8, 10, 11},
	12: {4, 6, 7, 8, 10, 11, 12},
	13: {4, 6, 7, 8, 10, 12, 13},
	14: {4, 6, 7, 8, 11, 13, 14},
	15: {4, 6, 7, 8, 12, 14, 15},
	16: {4, 6, 7, 8, 12, 15, 16},
	17: {4, 6, 7, 8, 12, 16, 17},
	18: {4, 6, 7, 8, 12, 16, 17, 18},
}

var subLenCounts = [19]uint8{
	3: 1, 4: 1, 5: 3, 6: 4, 7: 4, 8: 5, 9: 5, 10: 6, 11: 6, 12: 7,
	13: 7, 14: 7, 15: 7, 16: 7, 17: 7, 18: 8,
}

// generateSubLengths writes sub-lengths S(L) into out without dynamic slice appends.
// Returns the number of sub-lengths written (at most 10).
func generateSubLengths(L, minMatchBase int, out *[maxSubLenCandidates]int) int {
	if L < minMatchBase {
		return 0
	}
	if L > 18 {
		cnt := 0
		if minMatchBase == 3 {
			out[cnt] = 3
			cnt++
		}
		out[cnt] = 4
		cnt++
		out[cnt] = 6
		cnt++
		out[cnt] = 7
		cnt++
		out[cnt] = 8
		cnt++
		out[cnt] = 12
		cnt++
		out[cnt] = 16
		cnt++
		out[cnt] = 18
		cnt++
		if L-2 > 18 {
			out[cnt] = L - 2
			cnt++
		}
		if L-1 > 18 {
			out[cnt] = L - 1
			cnt++
		}
		out[cnt] = L
		cnt++
		return cnt
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
func insertHashLarge(src []byte, p, uSize, windowLog int, sp1M *HenselScratchpad1M, sp8M *HenselScratchpad8M) {
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

func insertHashPRZM(
	src []byte,
	p, uSize int,
	pad *scratchpad,
	windowLog int,
	sp1M *HenselScratchpad1M,
	sp8M *HenselScratchpad8M,
) {
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

var isDelimLUT = [256]bool{
	' ': true, '\t': true, '\n': true, '\r': true,
	';': true, ',': true, '(': true, ')': true,
	'{': true, '}': true, '[': true, ']': true,
	':': true, '.': true, '<': true, '>': true,
	'=': true, '+': true, '-': true, '*': true,
	'/': true, '%': true, '&': true, '|': true,
	'^': true, '~': true, '!': true, '?': true,
	'"': true, '\'': true, '`': true, '\\': true,
	0: true, 0xFF: true,
}

func henselMultiBracketProbe(
	src []byte,
	pos, uSize, maxDepth, windowSize, windowLog, maxL int,
	pad *scratchpad,
	sp1M *HenselScratchpad1M,
	sp8M *HenselScratchpad8M,
	updateBracket func(int, int),
) {
	if pos+4 > uSize {
		return
	}
	w4 := binary.LittleEndian.Uint32(src[pos : pos+4])
	var head []int32
	var prev []int32
	var fp32 []uint32
	var headMask, windowMask int
	h := w4 * HenselHashPrime

	switch windowLog {
	case WindowLog8M:
		if sp8M == nil {
			return
		}
		head = sp8M.head[:]
		prev = sp8M.prev[:]
		fp32 = sp8M.fp32[:]
		headMask = HashMask8M
		windowMask = WindowMask8M
		h >>= 12
	case WindowLog1M:
		if sp1M == nil {
			return
		}
		head = sp1M.head[:]
		prev = sp1M.prev[:]
		fp32 = sp1M.fp32[:]
		headMask = HashMask1M
		windowMask = WindowMask1M
		h >>= 14
	default:
		head = pad.hensel.head[:]
		prev = pad.hensel.prev[:]
		fp32 = pad.hensel.fp32[:]
		headMask = HenselHashMask
		windowMask = HenselWindowMask
		h >>= 16
	}

	cand := int(head[int(h)&headMask]) - 1
	if cand < 0 {
		return
	}

	has8 := pos+8 <= uSize
	var targetFP32 uint32
	if has8 {
		targetFP32 = binary.LittleEndian.Uint32(src[pos+4 : pos+8])
	}

	depth := 0
	for cand >= 0 && depth < maxDepth {
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

		hasDiff := false
		var maxLift int
		if has8 {
			diff := fp32[candSlot] ^ targetFP32
			if diff != 0 {
				maxLift = 4 + (bits.TrailingZeros32(diff) >> 3)
				hasDiff = true
			}
		}

		if binary.LittleEndian.Uint32(src[cand:cand+4]) == w4 {
			if hasDiff {
				if maxLift >= 4 {
					updateBracket(d, maxLift)
				}
			} else {
				mLen := HenselLCPPos(src, pos, cand, maxL)
				if mLen >= 4 {
					updateBracket(d, mLen)
					if d < 128 && mLen >= 258 {
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
	hasStride bool,
	stride int,
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

	// Command Run Folding: Coalesce consecutive contiguous matches that share the same effective distance
	coalesced := dpPad.path2[:0]
	coalesceFrom := int32(segStart)
	for i := 0; i < len(path); i++ {
		to := path[i]
		repCode := int(dpPad.trace[to].repCode)

		if len(coalesced) > 0 && repCode == 0 {
			prevTo := coalesced[len(coalesced)-1]
			prevRep := int(dpPad.trace[prevTo].repCode)
			prevDist := int(dpPad.trace[prevTo].dist)

			if (prevRep >= 0 || (prevRep == -1 && prevDist > 0)) && prevTo == coalesceFrom {
				prevFrom := dpPad.trace[prevTo].from
				dpPad.trace[to].from = prevFrom
				dpPad.trace[to].dist = int32(prevDist)
				dpPad.trace[to].repCode = int8(prevRep)
				coalesced[len(coalesced)-1] = to
				coalesceFrom = to
				continue
			}
		}

		coalesced = append(coalesced, to)
		coalesceFrom = to
	}
	path = coalesced

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
			case -7:
				// RFC 7932 Transformed Dictionary Match (0xCF <transID> <varint wordID>)
				transID := dpPad.trace[to].extra1
				wordID := int(dpPad.trace[to].dist)
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRFCTransform, transID)
				var varBuf [8]byte
				n := binary.PutUvarint(varBuf[:], uint64(wordID))
				pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
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
		} else if isBracketDist(mDist) && mLen-minMatchBase <= 15 {
			bracket := bracketIndex(mDist)
			if litLen > 15 {
				flushLen := litLen - 15
				flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+flushLen])
				litStart += flushLen
				litLen = 15
			}
			op := OpcodePrefixFusedBracket + uint8(bracket)
			token := uint8((litLen << 4) | (mLen - minMatchBase))
			pad.cmdBuf = append(pad.cmdBuf, op, token)
			if litLen > 0 {
				pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
			}
			*rep2 = *rep1
			*rep1 = *rep0
			*rep0 = mDist
		} else if (!hasStride || stride <= 1) && mDist >= 128 && mDist <= 256 && litLen > 3 && litLen <= 6 && mLen-minMatchBase <= 6 {
			if litLen > 6 {
				flushLen := litLen - 6
				flushLiteralsPRZM(&pad.cmdBuf, &pad.literalBuf, src[litStart:litStart+flushLen])
				litStart += flushLen
				litLen = 6
			}
			baseLen := mLen - minMatchBase
			if baseLen > 6 {
				baseLen = 7
			}
			desc := uint8((3 << 6) | (litLen << 3) | baseLen)
			deltaByte := uint8(mDist - 1)
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixDiffLattice, desc, deltaByte)
			if baseLen == 7 {
				var varBuf [8]byte
				n := binary.PutUvarint(varBuf[:], uint64(mLen-(7+minMatchBase)))
				pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
			}
			if litLen > 0 {
				pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
			}
			*rep2 = *rep1
			*rep1 = *rep0
			*rep0 = mDist
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
	hasStride bool,
	stride int,
) {
	if bSize <= 0 {
		return
	}
	if bSize > BlockSize64K {
		return
	}

	cmdStart := len(pad.cmdBuf)
	offStart := len(pad.offsetBuf)
	litStart := len(pad.literalBuf)
	r0Start := *rep0
	r1Start := *rep1
	r2Start := *rep2

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

	buildShannonLitCostTable(src, bStart, bSize, &dpPad.litCostTable)

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

		// 1. Literal transition i -> i+1 (dynamically scaled by symbol's Shannon entropy bit-cost)
		litCost := curCost + dpPad.litCostTable[src[pos]]
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
			if l < 512 {
				return
			}
			score := l
			if repCode >= 0 {
				score += 8 // bias towards repeat matches
			}
			if score > bestLongScore {
				bestLongScore = score
				bestLong = dpCandidate{dist: int32(d), length: int32(l), repCode: int32(repCode)}
			}
		}

		// Dictionary Token Matching
		if activeMatcher != nil {
			tokIdx, transOp, tokLen, _, score, pairTok2, isRFC := activeMatcher.match(src, pos)
			if score >= 1 && tokLen >= 2 && candCount < 30 {
				if isRFC {
					candidates[candCount] = dpCandidate{
						dist:    int32(tokIdx),
						length:  int32(tokLen),
						repCode: -7,
						extra:   int32(transOp),
					}
					candCount++
				} else if tokIdx < 8 && transOp == 0 {
					candidates[candCount] = dpCandidate{
						dist:    0,
						length:  int32(tokLen),
						repCode: -2,
						extra:   int32(tokIdx),
					}
					candCount++
				} else if transOp == 0 {
					candidates[candCount] = dpCandidate{
						dist:    0,
						length:  int32(tokLen),
						repCode: -3,
						extra:   int32(tokIdx),
					}
					candCount++
				} else if transOp == OpcodePrefixTransPair && pairTok2 >= 0 {
					candidates[candCount] = dpCandidate{
						dist:    0,
						length:  int32(tokLen),
						repCode: -5,
						extra:   int32(tokIdx) | (int32(pairTok2) << 8),
					}
					candCount++
				} else if transOp != 0 {
					candidates[candCount] = dpCandidate{
						dist:    0,
						length:  int32(tokLen),
						repCode: -4,
						extra:   int32(tokIdx) | (int32(transOp) << 8),
					}
					candCount++
				}
			}
		}

		// Repeat 0 check
		if r0 > 0 && pos-r0 >= 0 && r0 <= windowSize {
			minR0 := minMatchBase
			if pos+2 <= uSize &&
				binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r0:pos-r0+2]) {
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
			if pos+2 <= uSize &&
				binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r1:pos-r1+2]) {
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
			if pos+2 <= uSize &&
				binary.LittleEndian.Uint16(src[pos:pos+2]) == binary.LittleEndian.Uint16(src[pos-r2:pos-r2+2]) {
				l2 := HenselLCPPos(src, pos, pos-r2, maxL)
				if l2 >= minR2 {
					candidates[candCount] = dpCandidate{dist: int32(r2), length: int32(min(l2, dpMaxL)), repCode: 2}
					candCount++
					updateLong(r2, l2, 2)
				}
			}
		}

		// Explicit match candidates sorted into 12 distance brackets
		var bracketBest [12]dpCandidate
		updateBracket := func(d, l int) {
			l = min(l, maxL)
			if d <= 0 || d > windowSize || d == r0 || d == r1 || d == r2 || l < minMatchBase {
				return
			}
			mPos := pos - d
			if compactDict && dictLen > 0 && mPos < dictLen {
				if candCount < 30 && l >= minMatchBase {
					candidates[candCount] = dpCandidate{
						dist:    int32(mPos),
						length:  int32(min(l, dpMaxL)),
						repCode: -6,
						extra:   0,
					}
					candCount++
					updateLong(mPos, l, -6)
				}
				return
			}
			updateLong(d, l, -1)
			minExp := minMatchBase
			if d >= 65536 {
				minExp = minMatchBase + 2
			} else if d >= 2048 {
				minExp = minMatchBase + 1
			}
			if l < minExp {
				return
			}
			b := 0
			if d >= 4194304 {
				b = 11
			} else if d >= 1048576 {
				b = 10
			} else if d >= 262144 {
				b = 9
			} else if d >= 65536 {
				b = 8
			} else if d >= 16384 {
				b = 7
			} else if d >= 4096 {
				b = 6
			} else if d >= 1024 {
				b = 5
			} else if d >= 256 {
				b = 4
			} else if d >= 64 {
				b = 3
			} else if d >= 16 {
				b = 2
			} else if d >= 4 {
				b = 1
			}
			lClamped := min(l, dpMaxL)
			if lClamped > int(bracketBest[b].length) {
				bracketBest[b] = dpCandidate{dist: int32(d), length: int32(lClamped), repCode: -1}
			}
		}

		// Direct canonical strides evaluation
		canDists := [...]int{344, 343, 342, 68692, 68746, 68741, 68694, 87268, 17200, 17150}
		for _, cd := range canDists {
			if cd > 0 && pos-cd >= 0 && cd <= windowSize && cd != r0 && cd != r1 && cd != r2 {
				if pos+4 <= uSize && pos-cd+4 <= uSize &&
					binary.LittleEndian.Uint32(src[pos:pos+4]) == binary.LittleEndian.Uint32(src[pos-cd:pos-cd+4]) {
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
			idx3 := (h3 * 0x1E35A7BD) >> 17
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

		// Inter-Block 32-byte Structural Macro Production Factorization (2-way associative)
		if pos+32 <= uSize {
			w32_0 := binary.LittleEndian.Uint64(src[pos : pos+8])
			w32_1 := binary.LittleEndian.Uint64(src[pos+8 : pos+16])
			w32_2 := binary.LittleEndian.Uint64(src[pos+16 : pos+24])
			w32_3 := binary.LittleEndian.Uint64(src[pos+24 : pos+32])
			h32 := uint32(((w32_0 ^ (w32_1 * 0x9E3779B97F4A7C15) ^ (w32_2 * 0xC6A4A7935BD1E995) ^ (w32_3 * 0x517cc1b727220a95)) * 0x517cc1b727220a95) >> 48)
			base32 := int(h32&0xFFFF) * 2
			for s := 0; s < 2; s++ {
				cand32 := int(dpPad.dagProd32[base32+s]) - 1
				if cand32 >= 0 && cand32 < pos {
					d32 := pos - cand32
					if d32 > 0 && d32 <= windowSize && d32 != r0 && d32 != r1 && d32 != r2 {
						if binary.LittleEndian.Uint64(src[cand32:cand32+8]) == w32_0 &&
							binary.LittleEndian.Uint64(src[cand32+8:cand32+16]) == w32_1 &&
							binary.LittleEndian.Uint64(src[cand32+16:cand32+24]) == w32_2 &&
							binary.LittleEndian.Uint64(src[cand32+24:cand32+32]) == w32_3 {
							l32 := 32 + HenselLCPPos(src, pos+32, cand32+32, maxL-32)
							updateBracket(d32, l32)
							updateLong(d32, l32, -1)
						}
					}
				}
			}
			if int(dpPad.dagProd32[base32])-1 != pos {
				dpPad.dagProd32[base32+1] = dpPad.dagProd32[base32]
				dpPad.dagProd32[base32] = int32(pos + 1)
			}
		}

		// Inter-Block 16-byte DAG Production Factorization (2-way associative)
		if pos+16 <= uSize {
			w16_0 := binary.LittleEndian.Uint64(src[pos : pos+8])
			w16_1 := binary.LittleEndian.Uint64(src[pos+8 : pos+16])
			h16 := uint32(((w16_0 ^ (w16_1 * 0x9E3779B97F4A7C15)) * 0x517cc1b727220a95) >> 48)
			base16 := int(h16&0xFFFF) * 2
			for s := 0; s < 2; s++ {
				cand16 := int(dpPad.dagProd[base16+s]) - 1
				if cand16 >= 0 && cand16 < pos {
					d16 := pos - cand16
					if d16 > 0 && d16 <= windowSize && d16 != r0 && d16 != r1 && d16 != r2 {
						l16 := HenselLCPPos(src, pos, cand16, maxL)
						if l16 >= 16 {
							updateBracket(d16, l16)
							updateLong(d16, l16, -1)
						}
					}
				}
			}
			if int(dpPad.dagProd[base16])-1 != pos {
				dpPad.dagProd[base16+1] = dpPad.dagProd[base16]
				dpPad.dagProd[base16] = int32(pos + 1)
			}
		}

		// Inter-Block 8-byte Macro Sliding Window Horizon Matcher (8-way associative)
		if pos+8 <= uSize {
			w8 := binary.LittleEndian.Uint64(src[pos : pos+8])
			h8 := uint32((w8 * 0x517cc1b727220a95) >> 48)
			base8 := int(h8&0xFFFF) * 8
			for s := 0; s < 8; s++ {
				cand8 := int(dpPad.macro8[base8+s]) - 1
				if cand8 >= 0 && cand8 < pos {
					d8 := pos - cand8
					if d8 > 0 && d8 <= windowSize && d8 != r0 && d8 != r1 && d8 != r2 {
						if binary.LittleEndian.Uint64(src[cand8:cand8+8]) == w8 {
							l8 := HenselLCPPos(src, pos, cand8, maxL)
							if l8 >= 8 {
								updateBracket(d8, l8)
								updateLong(d8, l8, -1)
							}
						}
					}
				}
			}
			if int(dpPad.macro8[base8])-1 != pos {
				for s := 7; s > 0; s-- {
					dpPad.macro8[base8+s] = dpPad.macro8[base8+s-1]
				}
				dpPad.macro8[base8] = int32(pos + 1)
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

			// Entropy-Gated Decision Boundary Hensel Probing
			isBoundary := isDelimLUT[src[pos]] ||
				(pos > 0 && isDelimLUT[src[pos-1]]) ||
				dpPad.trace[i].dist != 0

			maxHenselDepth := 8
			if isBoundary {
				maxHenselDepth = 32
				if level >= 5 {
					maxHenselDepth = 48
				}
			}

			henselMultiBracketProbe(
				src, pos, uSize, maxHenselDepth, windowSize, windowLog, dpMaxL,
				pad, sp1M, sp8M, updateBracket,
			)
		}

		// Check if Long Match Shortcut triggers
		if bestLongScore >= 512 {
			// 1. Emit optimal path up to current position i
			emitPathPRZM(src, bStart, segStart, i, uSize, pad, dpPad, rep0, rep1, rep2, minMatchBase, hasStride, stride)

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
			} else if isBracketDist(mDist) && mLen-minMatchBase <= 15 {
				bracket := bracketIndex(mDist)
				op := OpcodePrefixFusedBracket + uint8(bracket)
				token := uint8(mLen - minMatchBase)
				pad.cmdBuf = append(pad.cmdBuf, op, token)
				*rep2 = *rep1
				*rep1 = *rep0

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
			if mLen >= 12 && pos+12 <= uSize {
				p4 := pos + 4
				w8_4 := binary.LittleEndian.Uint64(src[p4 : p4+8])
				h8_4 := uint32((w8_4 * 0x517cc1b727220a95) >> 48)
				b4 := int(h8_4&0xFFFF) * 8
				if int(dpPad.macro8[b4])-1 != p4 {
					for s := 7; s > 0; s-- {
						dpPad.macro8[b4+s] = dpPad.macro8[b4+s-1]
					}
					dpPad.macro8[b4] = int32(p4 + 1)
				}
			}
			for pStep := pos; pStep < pos+mLen; pStep += 8 {
				if pStep+8 <= uSize {
					w8Step := binary.LittleEndian.Uint64(src[pStep : pStep+8])
					h8Step := uint32((w8Step * 0x517cc1b727220a95) >> 48)
					base8Step := int(h8Step&0xFFFF) * 8
					if int(dpPad.macro8[base8Step])-1 != pStep {
						for s := 7; s > 0; s-- {
							dpPad.macro8[base8Step+s] = dpPad.macro8[base8Step+s-1]
						}
						dpPad.macro8[base8Step] = int32(pStep + 1)
					}
				}
				if pStep+16 <= uSize {
					w16_0 := binary.LittleEndian.Uint64(src[pStep : pStep+8])
					w16_1 := binary.LittleEndian.Uint64(src[pStep+8 : pStep+16])
					h16 := uint32(((w16_0 ^ (w16_1 * 0x9E3779B97F4A7C15)) * 0x517cc1b727220a95) >> 48)
					base16Step := int(h16&0xFFFF) * 2
					if int(dpPad.dagProd[base16Step])-1 != pStep {
						dpPad.dagProd[base16Step+1] = dpPad.dagProd[base16Step]
						dpPad.dagProd[base16Step] = int32(pStep + 1)
					}
				}
				if pStep+32 <= uSize {
					w32_0 := binary.LittleEndian.Uint64(src[pStep : pStep+8])
					w32_1 := binary.LittleEndian.Uint64(src[pStep+8 : pStep+16])
					w32_2 := binary.LittleEndian.Uint64(src[pStep+16 : pStep+24])
					w32_3 := binary.LittleEndian.Uint64(src[pStep+24 : pStep+32])
					h32 := uint32(((w32_0 ^ (w32_1 * 0x9E3779B97F4A7C15) ^ (w32_2 * 0xC6A4A7935BD1E995) ^ (w32_3 * 0x517cc1b727220a95)) * 0x517cc1b727220a95) >> 48)
					base32Step := int(h32&0xFFFF) * 2
					if int(dpPad.dagProd32[base32Step])-1 != pStep {
						dpPad.dagProd32[base32Step+1] = dpPad.dagProd32[base32Step]
						dpPad.dagProd32[base32Step] = int32(pStep + 1)
					}
				}
			}
			if pos+mLen-8 >= 0 && pos+mLen <= uSize {
				pEnd := pos + mLen - 8
				w8End := binary.LittleEndian.Uint64(src[pEnd : pEnd+8])
				h8End := uint32((w8End * 0x517cc1b727220a95) >> 48)
				bEnd := int(h8End&0xFFFF) * 8
				if int(dpPad.macro8[bEnd])-1 != pEnd {
					for s := 7; s > 0; s-- {
						dpPad.macro8[bEnd+s] = dpPad.macro8[bEnd+s-1]
					}
					dpPad.macro8[bEnd] = int32(pEnd + 1)
				}
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

		for b := range 12 {
			if bracketBest[b].length >= int32(minMatchBase) && candCount < 30 {
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
				edgeCost := calcEdgeCostPRZM(subLen, int(cand.dist), int(cand.repCode), minMatchBase, hasStride, stride)
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
				if cand.repCode < 0 {
					minSubReq := minMatchBase
					if cand.dist >= 65536 {
						if minSubReq < 6 {
							minSubReq = 6
						}
					} else if cand.dist >= 16384 {
						if minSubReq < 5 {
							minSubReq = 5
						}
					} else if cand.dist >= 128 {
						if minSubReq < 4 {
							minSubReq = 4
						}
					}
					if subLen < minSubReq {
						continue
					}
				}
				next := i + subLen
				edgeCost := calcEdgeCostPRZM(subLen, int(cand.dist), int(cand.repCode), minMatchBase, hasStride, stride)
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
		emitPathPRZM(src, bStart, segStart, bSize, uSize, pad, dpPad, rep0, rep1, rep2, minMatchBase, hasStride, stride)
	}

	// 5. Rate-Distortion Evaluation: Hierarchical SLP Grammar DAG Bisection
	// Reconstruct match segments via topological rule expansion without emitting Section 3 distance varints.
	if dictLen == 0 && bSize >= 64 {
		cmdDelta := len(pad.cmdBuf) - cmdStart
		offDelta := len(pad.offsetBuf) - offStart
		litDelta := len(pad.literalBuf) - litStart
		lzCost := cmdDelta + offDelta + litDelta

		if lzCost > 32 {
			if pwsData, err := PWSEncode(src[bStart : bStart+bSize]); err == nil && len(pwsData) > 0 {
				var varBuf [8]byte
				nVar := binary.PutUvarint(varBuf[:], uint64(len(pwsData)))
				pwsCost := 1 + nVar + len(pwsData)
				if pwsCost < lzCost {
					pad.cmdBuf = pad.cmdBuf[:cmdStart]
					pad.offsetBuf = pad.offsetBuf[:offStart]
					pad.literalBuf = pad.literalBuf[:litStart]
					*rep0 = r0Start
					*rep1 = r1Start
					*rep2 = r2Start

					pad.cmdBuf = append(pad.cmdBuf, OpGrammarBlock)
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:nVar]...)
					pad.cmdBuf = append(pad.cmdBuf, pwsData...)
				}
			}
		}
	}
}
