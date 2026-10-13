// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math/bits"

	"github.com/lemon4ksan/foundation/silicon/simd"
)

type matchCandidate struct {
	mType      int
	length     int
	distance   int
	dictTok    int
	strideM    int
	cost       int
	score      int
	ringK      int
	mismatches int
	diffOffs   [2]int
	diffXORs   [2]byte
	transOp    uint8
	pairTok2   int
}

// greedyBlockEncoder encapsulates the state and algorithms for greedy LZ77 block encoding.
type greedyBlockEncoder struct {
	src           []byte
	uSize         int
	level         int
	windowSize    int
	windowLog     int
	minMatch      int
	minMatchBase  int
	compactDict   bool
	dictLen       int
	hasStride     bool
	stride        int
	activeMatcher *dictMatcher
	pad           *scratchpad
	sp1M          *HenselScratchpad1M
	sp8M          *HenselScratchpad8M
	spL6          *HenselScratchpadL6
	rep0          *int
	rep1          *int
	rep2          *int
	rep3          *int
	flags         *uint8
}

func newGreedyBlockEncoder(
	src []byte,
	uSize int,
	level int,
	windowSize int,
	windowLog int,
	minMatch int,
	minMatchBase int,
	compactDict bool,
	dictLen int,
	hasStride bool,
	stride int,
	activeMatcher *dictMatcher,
	pad *scratchpad,
	sp1M *HenselScratchpad1M,
	sp8M *HenselScratchpad8M,
	spL6 *HenselScratchpadL6,
	rep0, rep1, rep2, rep3 *int,
	flags *uint8,
) *greedyBlockEncoder {
	return &greedyBlockEncoder{
		src:           src,
		uSize:         uSize,
		level:         level,
		windowSize:    windowSize,
		windowLog:     windowLog,
		minMatch:      minMatch,
		minMatchBase:  minMatchBase,
		compactDict:   compactDict,
		dictLen:       dictLen,
		hasStride:     hasStride,
		stride:        stride,
		activeMatcher: activeMatcher,
		pad:           pad,
		sp1M:          sp1M,
		sp8M:          sp8M,
		spL6:          spL6,
		rep0:          rep0,
		rep1:          rep1,
		rep2:          rep2,
		rep3:          rep3,
		flags:         flags,
	}
}

func (e *greedyBlockEncoder) calcOffsetCost(distance int) int {
	if distance < 128 {
		return 1
	}
	if distance < 16384 {
		return 2
	}
	if distance < 2097152 {
		return 3
	}
	return 4
}

func (e *greedyBlockEncoder) writeOffset(distance int) {
	if distance < 0x80 {
		e.pad.offsetBuf = append(e.pad.offsetBuf, byte(distance))
	} else if distance < 0x4000 {
		e.pad.offsetBuf = append(e.pad.offsetBuf, byte(distance|0x80), byte(distance>>7))
	} else {
		var buf [8]byte
		n := binary.PutUvarint(buf[:], uint64(distance))
		e.pad.offsetBuf = append(e.pad.offsetBuf, buf[:n]...)
	}
}

func (e *greedyBlockEncoder) flushLiterals(literals []byte) {
	nLits := len(literals)
	if nLits == 0 {
		return
	}
	if nLits <= 8 {
		e.pad.cmdBuf = append(e.pad.cmdBuf, 0xF2+byte(nLits))
	} else if nLits < 0x80 {
		e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixLiteral, byte(nLits))
	} else if nLits < 0x4000 {
		e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixLiteral, byte(nLits|0x80), byte(nLits>>7))
	} else {
		e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixLiteral)
		var varBuf [8]byte
		n := binary.PutUvarint(varBuf[:], uint64(nLits))
		e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
	}
	e.pad.literalBuf = append(e.pad.literalBuf, literals...)
}

func (e *greedyBlockEncoder) extendMatch(pos, distance, maxLen int) int {
	mLen := 4
	if e.minMatch == 3 {
		if pos+3 >= e.uSize || pos-distance+3 >= e.uSize || e.src[pos+3] != e.src[pos-distance+3] {
			return 3
		}
	}
	for pos+mLen+8 <= e.uSize && mLen+8 <= maxLen {
		diff := binary.LittleEndian.Uint64(
			e.src[pos+mLen:pos+mLen+8],
		) ^ binary.LittleEndian.Uint64(
			e.src[pos-distance+mLen:pos-distance+mLen+8],
		)
		if diff != 0 {
			return mLen + (bits.TrailingZeros64(diff) >> 3)
		}
		mLen += 8
		if mLen >= 32 && pos+maxLen <= e.uSize {
			rem := maxLen - mLen
			if rem >= 32 {
				return mLen + simd.FindMatchLengthVector(
					e.src[pos+mLen:pos+maxLen],
					e.src[pos-distance+mLen:pos-distance+maxLen],
					rem,
				)
			}
		}
	}
	for pos+mLen < e.uSize && mLen < maxLen && e.src[pos+mLen] == e.src[pos-distance+mLen] {
		mLen++
	}
	return mLen
}

func (e *greedyBlockEncoder) evalCandidate(pos, distance, maxLen int) matchCandidate {
	if distance <= 0 || pos < distance || pos+e.minMatch > e.uSize {
		return matchCandidate{}
	}
	if e.minMatch == 3 {
		if pos+4 <= e.uSize && pos-distance+4 <= e.uSize {
			if (binary.LittleEndian.Uint32(e.src[pos:pos+4]) & 0x00FFFFFF) != (binary.LittleEndian.Uint32(e.src[pos-distance:pos-distance+4]) & 0x00FFFFFF) {
				return matchCandidate{}
			}
		} else {
			if e.src[pos] != e.src[pos-distance] || e.src[pos+1] != e.src[pos-distance+1] ||
				e.src[pos+2] != e.src[pos-distance+2] {
				return matchCandidate{}
			}
		}
	} else {
		if pos+4 > e.uSize || pos-distance+4 > e.uSize ||
			binary.LittleEndian.Uint32(
				e.src[pos:pos+4],
			) != binary.LittleEndian.Uint32(
				e.src[pos-distance:pos-distance+4],
			) {
			return matchCandidate{}
		}
	}
	mLen := e.extendMatch(pos, distance, maxLen)
	if mLen < e.minMatch {
		return matchCandidate{}
	}

	mPos := pos - distance
	if e.compactDict && mPos < e.dictLen {
		dictOff := mPos
		offCost := e.calcOffsetCost(dictOff)
		cost := 2 + offCost
		if mLen > 14+e.minMatchBase {
			cost += uvarintLen(uint64(mLen - (15 + e.minMatchBase)))
		}
		score := mLen - cost
		if score >= 1 {
			return matchCandidate{
				mType:   9,
				length:  mLen,
				dictTok: dictOff,
				cost:    cost,
				score:   score,
			}
		}
		return matchCandidate{}
	}

	ringK := -1
	if *e.rep0 == distance {
		ringK = 0
	} else if *e.rep1 == distance {
		ringK = 1
	} else if *e.rep2 == distance {
		ringK = 2
	} else if *e.rep3 == distance {
		ringK = 3
	}

	if ringK >= 0 {
		repBonus := 0
		if e.level >= 6 {
			if ringK == 0 {
				repBonus = 6
			} else if ringK == 1 {
				repBonus = 3
			} else {
				repBonus = 1
			}
		} else if e.level >= 4 {
			if ringK == 0 {
				repBonus = 4
			} else if ringK == 1 {
				repBonus = 2
			} else {
				repBonus = 1
			}
		}
		if ringK <= 3 && mLen <= 3+e.minMatchBase {
			return matchCandidate{
				mType:    1,
				length:   mLen,
				distance: distance,
				ringK:    ringK,
				cost:     1,
				score:    mLen - 1 + repBonus,
			}
		}
		cost := 2
		if mLen > 14+e.minMatchBase {
			cost += uvarintLen(uint64(mLen - (15 + e.minMatchBase)))
		}
		return matchCandidate{
			mType:    3,
			length:   mLen,
			distance: distance,
			ringK:    ringK,
			cost:     cost,
			score:    mLen - cost + repBonus,
		}
	}

	// Explicit Extended Match
	offCost := e.calcOffsetCost(distance)
	minExpLen := e.minMatchBase
	distPenalty := 0
	if e.level >= 6 {
		if distance >= 16384 {
			minExpLen = e.minMatchBase + 3
			if distance >= 65536 {
				distPenalty = 5
			} else {
				distPenalty = 3
			}
		} else if distance >= 1024 {
			minExpLen = e.minMatchBase + 2
			distPenalty = 2
		} else if distance >= 128 {
			minExpLen = e.minMatchBase + 1
			distPenalty = 1
		}
	} else if e.level >= 4 {
		if distance >= 16384 {
			minExpLen = e.minMatchBase + 3
			if distance >= 65536 {
				distPenalty = 4
			} else {
				distPenalty = 3
			}
		} else if distance >= 1024 {
			minExpLen = e.minMatchBase + 2
			distPenalty = 2
		} else if distance >= 128 {
			minExpLen = e.minMatchBase + 1
			distPenalty = 1
		}
	} else {
		if distance >= 128 {
			minExpLen = e.minMatchBase + 1
		}
	}
	if distance > 256 && mLen < minExpLen {
		return matchCandidate{}
	}
	var cost int
	if isBracketDist(distance) && mLen <= 15+e.minMatchBase {
		cost = 2
	} else if (!e.hasStride || e.stride <= 1) && distance <= 256 {
		cost = 3
		if mLen > 6+e.minMatchBase {
			cost += uvarintLen(uint64(mLen - (7 + e.minMatchBase)))
		}
	} else {
		cost = 1 + offCost
		if mLen > 14+e.minMatchBase {
			cost += uvarintLen(uint64(mLen - (15 + e.minMatchBase)))
		}
	}
	score := mLen - (cost + distPenalty)
	if score < 0 {
		return matchCandidate{}
	}
	return matchCandidate{
		mType:    4,
		length:   mLen,
		distance: distance,
		cost:     cost,
		score:    score,
	}
}

func (e *greedyBlockEncoder) checkDelta(pos, blockEnd, distance, ringK int, best *matchCandidate) {
	if e.activeMatcher != nil {
		return
	}
	if distance < 14 || pos < distance || pos+8 > blockEnd || pos-distance+8 > e.uSize {
		return
	}
	diff8 := binary.LittleEndian.Uint64(
		e.src[pos:pos+8],
	) ^ binary.LittleEndian.Uint64(
		e.src[pos-distance:pos-distance+8],
	)
	if diff8 != 0 {
		hasZero := (diff8 - 0x0101010101010101) & ^diff8 & 0x8080808080808080
		zeroBytes := bits.OnesCount64(hasZero)
		if zeroBytes < 6 {
			return
		}
	}
	checkLimit := min(1024, distance)
	if pos+checkLimit > blockEnd {
		checkLimit = blockEnd - pos
	}
	if checkLimit < 14 {
		return
	}

	mismatches := 0
	var diffOffsets [2]int
	var diffXORs [2]byte
	curLen := 0
	i := 0
	for i < checkLimit {
		if i+8 <= checkLimit {
			diff := binary.LittleEndian.Uint64(
				e.src[pos+i:pos+i+8],
			) ^ binary.LittleEndian.Uint64(
				e.src[pos-distance+i:pos-distance+i+8],
			)
			if diff == 0 {
				i += 8
				curLen = i
				continue
			}
		}
		if e.src[pos+i] != e.src[pos-distance+i] {
			if mismatches >= 2 {
				break
			}
			diffOffsets[mismatches] = i
			diffXORs[mismatches] = e.src[pos+i] ^ e.src[pos-distance+i]
			mismatches++
		}
		curLen = i + 1
		i++
	}

	for curLen > 0 && mismatches > 0 && diffOffsets[mismatches-1] == curLen-1 {
		mismatches--
		curLen--
	}

	if ringK < 0 {
		if *e.rep0 == distance {
			ringK = 0
		} else if *e.rep1 == distance {
			ringK = 1
		} else if *e.rep2 == distance {
			ringK = 2
		} else if *e.rep3 == distance {
			ringK = 3
		}
	}

	minDeltaLen := 64
	minScore := best.score + 12
	if best.score == 0 {
		minScore = 24
	}

	if mismatches >= 1 && mismatches <= 2 && curLen >= minDeltaLen {
		var cost int
		var mType int
		if ringK >= 0 {
			mType = 6 // DeltaMaskRing8
			cost = 2 + 1 + mismatches*2
		} else {
			mType = 5 // DeltaMaskExplicit
			cost = 2 + e.calcOffsetCost(distance) + 1 + mismatches*2
		}
		if curLen > 258 {
			cost += uvarintLen(uint64(curLen - 259))
		}
		if curLen > 256 {
			cost += mismatches
		}
		score := curLen - cost
		if score >= minScore {
			*best = matchCandidate{
				mType:      mType,
				length:     curLen,
				distance:   distance,
				ringK:      ringK,
				cost:       cost,
				score:      score,
				mismatches: mismatches,
				diffOffs:   diffOffsets,
				diffXORs:   diffXORs,
			}
		}
	}
}

func (e *greedyBlockEncoder) findBest(pos, blockEnd int) matchCandidate {
	if pos+e.minMatch > blockEnd {
		return matchCandidate{}
	}

	maxBlockLen := min(blockEnd-pos, 65535)
	if e.level >= 6 {
		maxBlockLen = blockEnd - pos
	}

	best := matchCandidate{}

	if e.activeMatcher != nil {
		tokIdx, transOp, tokLen, cost, _, pairTok2, isRFC := e.activeMatcher.match(e.src, pos)
		if !isRFC && tokLen >= 3 && tokLen <= maxBlockLen {
			var mType int
			dictBonus := 0
			if transOp == 0 && tokIdx < 8 {
				cost = 1
				mType = 7 // OpcodePrefixShortDict
				if e.level >= 4 {
					dictBonus = 4
				}
			} else {
				mType = 8 // OpcodePrefixDictToken or Transformed Token
				if e.level >= 4 {
					dictBonus = 2
				}
			}
			score := tokLen - cost + dictBonus
			if score > best.score && score >= 1 {
				best = matchCandidate{
					mType:    mType,
					length:   tokLen,
					dictTok:  tokIdx,
					cost:     cost,
					score:    score,
					transOp:  transOp,
					pairTok2: pairTok2,
				}
			}
		}
	}

	// 1. Hash table lookup (4-way bucket)
	var hashIndex uint32
	if e.minMatch == 3 {
		h3 := uint32(e.src[pos]) | (uint32(e.src[pos+1]) << 8) | (uint32(e.src[pos+2]) << 16)
		hashIndex = ((h3 * 0x1E35A7BD) >> 16) & hashTableMask
	} else {
		hashIndex = ((binary.LittleEndian.Uint32(e.src[pos:pos+4]) * 0x1E35A7BD) >> 16) & hashTableMask
	}
	candPos0 := e.pad.head[hashIndex*4]
	candPos1 := e.pad.head[hashIndex*4+1]
	candPos2 := e.pad.head[hashIndex*4+2]
	candPos3 := e.pad.head[hashIndex*4+3]

	candidateDistance0 := 0
	if candPos0 > 0 {
		prevPos := int(candPos0 - 1)
		distance := pos - prevPos
		if distance > 0 && distance < e.windowSize {
			candidateDistance0 = distance
			cand := e.evalCandidate(pos, distance, maxBlockLen)
			if cand.score > best.score {
				best = cand
			}
			if e.level >= 3 && cand.score < 12 {
				e.checkDelta(pos, blockEnd, distance, -1, &best)
			}
		}
	}
	if candPos1 > 0 {
		prevPos := int(candPos1 - 1)
		distance := pos - prevPos
		if distance > 0 && distance < e.windowSize && distance != candidateDistance0 {
			cand := e.evalCandidate(pos, distance, maxBlockLen)
			if cand.score > best.score {
				best = cand
			}
			if e.level >= 3 && cand.score < 12 {
				e.checkDelta(pos, blockEnd, distance, -1, &best)
			}
		}
	}
	if candPos2 > 0 {
		prevPos := int(candPos2 - 1)
		distance := pos - prevPos
		if distance > 0 && distance < e.windowSize && distance != candidateDistance0 {
			cand := e.evalCandidate(pos, distance, maxBlockLen)
			if cand.score > best.score {
				best = cand
			}
			if e.level >= 3 && cand.score < 12 {
				e.checkDelta(pos, blockEnd, distance, -1, &best)
			}
		}
	}
	if candPos3 > 0 {
		prevPos := int(candPos3 - 1)
		distance := pos - prevPos
		if distance > 0 && distance < e.windowSize && distance != candidateDistance0 {
			cand := e.evalCandidate(pos, distance, maxBlockLen)
			if cand.score > best.score {
				best = cand
			}
			if e.level >= 3 && cand.score < 12 {
				e.checkDelta(pos, blockEnd, distance, -1, &best)
			}
		}
	}

	// Hensel Ultrametric Match Finding
	if e.level >= 6 && e.spL6 != nil {
		hDist, hLen := HenselFindMatchL6(e.src, pos, e.uSize, 256, e.minMatch, e.spL6)
		if hDist > 0 && hLen >= e.minMatch {
			cand := e.evalCandidate(pos, hDist, maxBlockLen)
			if cand.score > best.score {
				best = cand
			}
			if cand.score < 12 {
				e.checkDelta(pos, blockEnd, hDist, -1, &best)
			}
		}
	} else if e.level >= 3 {
		maxDepth := 16
		if e.level >= 4 {
			maxDepth = 64
		}
		var hDist, hLen int
		switch e.windowLog {
		case WindowLog8M:
			if e.sp8M != nil {
				hDist, hLen = HenselFindMatch8M(e.src, pos, e.uSize, maxDepth, e.minMatch, e.sp8M)
			}
		case WindowLog1M:
			if e.sp1M != nil {
				hDist, hLen = HenselFindMatch1M(e.src, pos, e.uSize, maxDepth, e.minMatch, e.sp1M)
			}
		default:
			hDist, hLen = HenselFindMatch(e.src, pos, e.uSize, maxDepth, e.minMatch, &e.pad.hensel)
		}
		if hDist > 0 && hLen >= e.minMatch {
			cand := e.evalCandidate(pos, hDist, maxBlockLen)
			if cand.score > best.score {
				best = cand
			}
			if cand.score < 12 {
				e.checkDelta(pos, blockEnd, hDist, -1, &best)
			}
		}
	}

	// 2. Check 3 repeat offset registers (rep0, rep1, rep2)
	if *e.rep0 > 0 && *e.rep0 != candidateDistance0 {
		if cand := e.evalCandidate(
			pos,
			*e.rep0,
			maxBlockLen,
		); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= e.minMatch && best.mType >= 4) ||
			(cand.length >= best.length && cand.length >= e.minMatch) {
			best = cand
		}
	}
	if *e.rep1 > 0 && *e.rep1 != candidateDistance0 && *e.rep1 != *e.rep0 {
		if cand := e.evalCandidate(
			pos,
			*e.rep1,
			maxBlockLen,
		); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= e.minMatch && best.mType >= 4) ||
			(cand.length >= best.length && cand.length >= e.minMatch) {
			best = cand
		}
	}
	if *e.rep2 > 0 && *e.rep2 != candidateDistance0 && *e.rep2 != *e.rep1 && *e.rep2 != *e.rep0 {
		if cand := e.evalCandidate(
			pos,
			*e.rep2,
			maxBlockLen,
		); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= e.minMatch && best.mType >= 4) ||
			(cand.length >= best.length && cand.length >= e.minMatch) {
			best = cand
		}
	}
	if *e.rep3 > 0 && *e.rep3 != candidateDistance0 && *e.rep3 != *e.rep2 && *e.rep3 != *e.rep1 && *e.rep3 != *e.rep0 {
		if cand := e.evalCandidate(
			pos,
			*e.rep3,
			maxBlockLen,
		); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= e.minMatch && best.mType >= 4) ||
			(cand.length >= best.length && cand.length >= e.minMatch) {
			best = cand
		}
	}

	if e.level <= 2 {
		if best.length >= 8 && best.score >= 6 {
			return best
		}
	} else {
		if best.length >= 64 {
			return best
		}
	}

	if best.length >= 8 {
		return best
	}

	// 3. Check Stride
	if e.hasStride && best.length < 8 {
		for m := 3; m <= 8; m++ {
			distance := m * e.stride
			if pos >= distance && pos+4 <= blockEnd &&
				binary.LittleEndian.Uint32(
					e.src[pos:pos+4],
				) == binary.LittleEndian.Uint32(
					e.src[pos-distance:pos-distance+4],
				) {
				mLen := e.extendMatch(pos, distance, min(maxBlockLen, 19))
				if mLen >= e.minMatch {
					cost := 2
					score := mLen - cost
					if score > best.score {
						best = matchCandidate{
							mType:    2,
							length:   mLen,
							distance: distance,
							strideM:  m,
							cost:     cost,
							score:    score,
						}
					}
				}
			}
		}
	}

	// 4. Fast-filtered Delta-mask fallback check on rep0
	if e.level >= 3 && best.length < 8 {
		if *e.rep0 >= 48 && pos >= *e.rep0 {
			e.checkDelta(pos, blockEnd, *e.rep0, 0, &best)
		}
	}

	return best
}

func (e *greedyBlockEncoder) insertHash(pos int) {
	if pos+e.minMatch <= e.uSize {
		var hashIndex uint32
		if e.minMatch == 3 {
			h3 := uint32(e.src[pos]) | (uint32(e.src[pos+1]) << 8) | (uint32(e.src[pos+2]) << 16)
			hashIndex = ((h3 * 0x1E35A7BD) >> 16) & hashTableMask
		} else {
			hashIndex = ((binary.LittleEndian.Uint32(e.src[pos:pos+4]) * 0x1E35A7BD) >> 16) & hashTableMask
		}
		e.pad.head[hashIndex*4+3] = e.pad.head[hashIndex*4+2]
		e.pad.head[hashIndex*4+2] = e.pad.head[hashIndex*4+1]
		e.pad.head[hashIndex*4+1] = e.pad.head[hashIndex*4]
		e.pad.head[hashIndex*4] = int32(pos + 1)

		if e.level >= 6 && e.spL6 != nil {
			HenselInsertL6(e.spL6, pos, e.src, e.uSize)
		} else if e.level >= 3 {
			switch e.windowLog {
			case WindowLog8M:
				if e.sp8M != nil {
					HenselInsert8M(e.sp8M, pos, e.src, e.uSize)
				}
			case WindowLog1M:
				if e.sp1M != nil {
					HenselInsert1M(e.sp1M, pos, e.src, e.uSize)
				}
			default:
				HenselInsert(&e.pad.hensel, pos, e.src, e.uSize)
			}
		}
	}
}

func (e *greedyBlockEncoder) populateDictionaryHashes() {
	if e.dictLen <= 0 {
		return
	}
	for pos := 0; pos+e.minMatch <= e.dictLen; pos++ {
		var hashIndex uint32
		var wordAtPos uint32
		if e.minMatch == 3 {
			h3 := uint32(e.src[pos]) | (uint32(e.src[pos+1]) << 8) | (uint32(e.src[pos+2]) << 16)
			hashIndex = ((h3 * 0x1E35A7BD) >> 16) & hashTableMask
			wordAtPos = h3
		} else {
			wordAtPos = binary.LittleEndian.Uint32(e.src[pos : pos+4])
			hashIndex = ((wordAtPos * 0x1E35A7BD) >> 16) & hashTableMask
		}

		e.pad.head[hashIndex*4+3] = e.pad.head[hashIndex*4+2]
		e.pad.head[hashIndex*4+2] = e.pad.head[hashIndex*4+1]
		e.pad.head[hashIndex*4+1] = e.pad.head[hashIndex*4]
		e.pad.head[hashIndex*4] = int32(pos + 1)

		if e.level <= 3 {
			secondaryHash := ((wordAtPos * 0x7FEB352D) >> 15) & hashTableMask
			e.pad.head[secondaryHash*4+1] = int32(pos + 1)
		} else {
			if e.level >= 6 && e.spL6 != nil {
				HenselInsertL6(e.spL6, pos, e.src, e.uSize)
			} else {
				switch e.windowLog {
				case WindowLog8M:
					if e.sp8M != nil {
						HenselInsert8M(e.sp8M, pos, e.src, e.uSize)
					}
				case WindowLog1M:
					if e.sp1M != nil {
						HenselInsert1M(e.sp1M, pos, e.src, e.uSize)
					}
				default:
					HenselInsert(&e.pad.hensel, pos, e.src, e.uSize)
				}
			}
		}
	}
}

// compressBlock executes the greedy LZ77 match search over [blockStart, blockStart+blockSize).
func (e *greedyBlockEncoder) compressBlock(blockStart, blockSize int) {
	pos := blockStart
	blockEnd := blockStart + blockSize
	literalStart := blockStart
	misses := 0

	for pos+e.minMatch <= blockEnd {
		// 1. RLE Check (run of identical bytes >= 12)
		if pos+12 <= blockEnd && e.src[pos] == e.src[pos+1] && e.src[pos] == e.src[pos+2] &&
			e.src[pos] == e.src[pos+3] {
			rleByte := e.src[pos]
			rleLen := 4
			for pos+rleLen < blockEnd && e.src[pos+rleLen] == rleByte && rleLen < 65535 {
				rleLen++
			}
			if rleLen >= 12 {
				if pos > literalStart {
					e.flushLiterals(e.src[literalStart:pos])
				}
				e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixRLE)
				var varBuf [8]byte
				n := binary.PutUvarint(varBuf[:], uint64(rleLen))
				e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				e.pad.literalBuf = append(e.pad.literalBuf, rleByte)

				e.insertHash(pos)
				pos += rleLen
				literalStart = pos
				continue
			}
		}

		best := e.findBest(pos, blockEnd)

		if best.score >= 1 && best.length >= e.minMatch {
			lookaheadMax := 6
			if e.level >= 5 {
				lookaheadMax = 16
			}
			if e.level >= 3 && (best.length <= lookaheadMax || best.mType >= 4) && pos+1+e.minMatch <= blockEnd {
				e.insertHash(pos)
				nextBest := e.findBest(pos+1, blockEnd)
				preferNext := false
				if nextBest.length > best.length+1 {
					preferNext = true
				} else if nextBest.score-1 > best.score {
					preferNext = true
				} else if nextBest.score-1 == best.score && nextBest.length == best.length+1 {
					if nextBest.mType <= 2 && best.mType > 2 {
						preferNext = true
					}
				} else if best.mType >= 4 && (nextBest.mType == 1 || nextBest.mType == 3 || nextBest.mType == 7 || nextBest.mType == 8) {
					if nextBest.length >= best.length-2 ||
						(e.level >= 6 && nextBest.ringK == 0 && nextBest.length >= best.length-3 && best.distance >= 16384) {
						preferNext = true
					}
				}
				if preferNext {
					pos++
					continue
				}
			}

			// Backward match extension
			if best.mType != 5 && best.mType != 6 && best.mType != 7 && best.mType != 8 && best.mType != 9 {
				for pos > literalStart && pos-best.distance > 0 && e.src[pos-1] == e.src[pos-best.distance-1] && (e.level >= 6 || best.length < 65530) {
					pos--
					best.length++
					if best.mType == 1 && (best.length > 3+e.minMatchBase || best.ringK > 2) {
						best.mType = 3
					}
					if best.mType == 2 && best.length > 15+e.minMatchBase {
						pos++
						best.length--
						break
					}
				}
			}

			literalLength := pos - literalStart

			switch best.mType {
			case 1: // Fused Repeat
				if literalLength > 7 {
					e.flushLiterals(e.src[literalStart : pos-7])
					literalStart = pos - 7
					literalLength = 7
				}
				op := uint8((literalLength << 4) | ((best.length - e.minMatchBase) << 2) | best.ringK)
				e.pad.cmdBuf = append(e.pad.cmdBuf, op)
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}
				switch best.ringK {
				case 0:
				case 1:
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				case 2:
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				case 3:
					*e.rep3 = *e.rep2
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				}

			case 2: // Stride
				if literalLength > 15 {
					e.flushLiterals(e.src[literalStart : pos-15])
					literalStart = pos - 15
					literalLength = 15
				}
				op := OpcodePrefixStride | uint8(best.strideM-1)
				token := uint8((literalLength << 4) | (best.length - e.minMatchBase))
				e.pad.cmdBuf = append(e.pad.cmdBuf, op, token)
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}
				*e.rep3 = *e.rep2
				*e.rep2 = *e.rep1
				*e.rep1 = *e.rep0
				*e.rep0 = best.distance

			case 3: // Long Repeat
				if literalLength > 15 {
					e.flushLiterals(e.src[literalStart : pos-15])
					literalStart = pos - 15
					literalLength = 15
				}
				baseLen := best.length - e.minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodePrefixLongRepeat | uint8(best.ringK)
				token := uint8((literalLength << 4) | baseLen)
				e.pad.cmdBuf = append(e.pad.cmdBuf, op, token)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(best.length-(15+e.minMatchBase)))
					e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				}
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}
				switch best.ringK {
				case 0:
				case 1:
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				case 2:
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				case 3:
					*e.rep3 = *e.rep2
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				}

			case 4: // Fused Extended Match / Fused Bracket / Diff Lattice
				if isBracketDist(best.distance) && best.length-e.minMatchBase <= 15 {
					bracket := bracketIndex(best.distance)
					if literalLength > 15 {
						e.flushLiterals(e.src[literalStart : pos-15])
						literalStart = pos - 15
						literalLength = 15
					}
					op := OpcodePrefixFusedBracket + uint8(bracket)
					token := uint8((literalLength << 4) | (best.length - e.minMatchBase))
					e.pad.cmdBuf = append(e.pad.cmdBuf, op, token)
					if literalLength > 0 {
						e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
					}
					*e.rep3 = *e.rep2
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				} else if (!e.hasStride || e.stride <= 1) && best.distance <= 256 {
					if literalLength > 6 {
						e.flushLiterals(e.src[literalStart : pos-6])
						literalStart = pos - 6
						literalLength = 6
					}
					baseLen := best.length - e.minMatchBase
					if baseLen > 6 {
						baseLen = 7
					}
					desc := uint8((3 << 6) | (literalLength << 3) | baseLen)
					deltaByte := uint8(best.distance - 1)
					e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixDiffLattice, desc, deltaByte)
					if baseLen == 7 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(7+e.minMatchBase)))
						e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
					}
					if literalLength > 0 {
						e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
					}
					*e.rep3 = *e.rep2
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				} else {
					if literalLength > 3 {
						e.flushLiterals(e.src[literalStart : pos-3])
						literalStart = pos - 3
						literalLength = 3
					}
					baseLen := best.length - e.minMatchBase
					if baseLen > 14 {
						baseLen = 15
					}
					op := OpcodeFlagFusedExt | uint8(literalLength<<4) | uint8(baseLen)
					e.pad.cmdBuf = append(e.pad.cmdBuf, op)
					if baseLen == 15 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(15+e.minMatchBase)))
						e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
					}
					if literalLength > 0 {
						e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
					}
					e.writeOffset(best.distance)
					*e.rep3 = *e.rep2
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				}

			case 5: // DeltaMaskExplicit
				if literalLength > 0 {
					e.flushLiterals(e.src[literalStart:pos])
				}
				*e.flags |= FlagDeltaMask
				e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixDeltaExpl)
				baseLen := best.length - e.minMatchBase
				if baseLen >= 255 {
					e.pad.cmdBuf = append(e.pad.cmdBuf, 255)
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(best.length-(255+e.minMatchBase)))
					e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				} else {
					e.pad.cmdBuf = append(e.pad.cmdBuf, uint8(baseLen))
				}
				e.writeOffset(best.distance)
				e.pad.literalBuf = append(e.pad.literalBuf, uint8(best.mismatches))
				for m := 0; m < best.mismatches; m++ {
					if best.length <= 256 {
						e.pad.literalBuf = append(e.pad.literalBuf, uint8(best.diffOffs[m]))
					} else {
						var rel [2]byte
						binary.LittleEndian.PutUint16(rel[:], uint16(best.diffOffs[m]))
						e.pad.literalBuf = append(e.pad.literalBuf, rel[:]...)
					}
					e.pad.literalBuf = append(e.pad.literalBuf, best.diffXORs[m])
				}
				*e.rep3 = *e.rep2
				*e.rep2 = *e.rep1
				*e.rep1 = *e.rep0
				*e.rep0 = best.distance

			case 6: // DeltaMaskRing8
				if literalLength > 0 {
					e.flushLiterals(e.src[literalStart:pos])
				}
				*e.flags |= FlagDeltaMask
				op := OpcodePrefixDeltaRing | uint8(best.ringK)
				e.pad.cmdBuf = append(e.pad.cmdBuf, op)
				baseLen := best.length - e.minMatchBase
				if baseLen >= 255 {
					e.pad.cmdBuf = append(e.pad.cmdBuf, 255)
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(best.length-(255+e.minMatchBase)))
					e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				} else {
					e.pad.cmdBuf = append(e.pad.cmdBuf, uint8(baseLen))
				}
				e.pad.literalBuf = append(e.pad.literalBuf, uint8(best.mismatches))
				for m := 0; m < best.mismatches; m++ {
					if best.length <= 256 {
						e.pad.literalBuf = append(e.pad.literalBuf, uint8(best.diffOffs[m]))
					} else {
						var rel [2]byte
						binary.LittleEndian.PutUint16(rel[:], uint16(best.diffOffs[m]))
						e.pad.literalBuf = append(e.pad.literalBuf, rel[:]...)
					}
					e.pad.literalBuf = append(e.pad.literalBuf, best.diffXORs[m])
				}
				switch best.ringK {
				case 0:
				case 1:
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				case 2:
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				case 3:
					*e.rep3 = *e.rep2
					*e.rep2 = *e.rep1
					*e.rep1 = *e.rep0
					*e.rep0 = best.distance
				}

			case 7: // OpcodePrefixShortDict
				if literalLength > 0 {
					e.flushLiterals(e.src[literalStart:pos])
				}
				op := OpcodePrefixShortDict | uint8(best.dictTok)
				e.pad.cmdBuf = append(e.pad.cmdBuf, op)

			case 8: // OpcodePrefixDictToken or Transformed Token
				if literalLength > 0 {
					e.flushLiterals(e.src[literalStart:pos])
				}
				if best.transOp == OpcodePrefixTransPair {
					e.pad.cmdBuf = append(
						e.pad.cmdBuf,
						OpcodePrefixTransPair,
						uint8(best.dictTok),
						uint8(best.pairTok2),
					)
				} else if best.transOp != 0 {
					e.pad.cmdBuf = append(e.pad.cmdBuf, best.transOp, uint8(best.dictTok))
				} else {
					e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixDictToken, uint8(best.dictTok))
				}

			case 9: // OpcodePrefixRawDict
				if literalLength > 15 {
					e.flushLiterals(e.src[literalStart : pos-15])
					literalStart = pos - 15
					literalLength = 15
				}
				baseLen := best.length - e.minMatchBase
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodePrefixRawDict
				token := uint8((literalLength << 4) | baseLen)
				e.pad.cmdBuf = append(e.pad.cmdBuf, op, token)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(best.length-(15+e.minMatchBase)))
					e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				}
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}
				e.writeOffset(best.dictTok)
			}

			e.insertHash(pos)
			if e.level >= 5 && best.length <= 16 {
				for p := pos + 1; p < pos+best.length; p++ {
					e.insertHash(p)
				}
			} else if best.length > 2 && pos+best.length-1+4 <= e.uSize {
				e.insertHash(pos + best.length - 1)
			}
			pos += best.length
			literalStart = pos
			misses = 0
			continue
		}

		e.insertHash(pos)
		misses++
		step := 1
		if e.level <= 2 {
			step = min(1+(misses>>4), 32)
		} else if e.level <= 3 {
			step = min(1+(misses>>6), 16)
		} else if e.level == 4 {
			step = min(1+(misses>>7), 4)
		}
		pos += step
	}

	if literalStart < blockEnd {
		e.flushLiterals(e.src[literalStart:blockEnd])
	}
}
