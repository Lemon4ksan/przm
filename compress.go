// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math"
	"math/bits"

	"github.com/lemon4ksan/foundation/generic"

	"github.com/lemon4ksan/przm/core/commands"
	cdict "github.com/lemon4ksan/przm/core/dict"
	"github.com/lemon4ksan/przm/core/distance"
	"github.com/lemon4ksan/przm/core/literals"
)

// Constants for Trellis RDO and Match Finder.
const (
	MaxBlockHorizon   = 65536
	MaxWindowHorizon  = 16777216
	WindowHorizonMask = MaxWindowHorizon - 1
	MaxHashBuckets    = 65536
	HashMultiplier32  = uint32(0x1E35A7BD)
	HashMultiplier64  = uint64(0x9E3779B97F4A7C15)
	MaxMatchLength    = 1024
	CostUnitScale     = 16 // 16 units = 1 byte = 8 bits
	InfinityCost      = uint32(0x3FFFFFFF)
)

// selectSearchDepth scales match discovery effort strictly with user-specified compression level.
func selectSearchDepth(level int) int {
	switch {
	case level <= 1:
		return 2
	case level <= 3:
		return 4
	case level <= 5:
		return 8
	case level <= 7:
		return 16
	case level <= 9:
		return 32
	case level <= 10:
		return 64
	default:
		return 128
	}
}

// TrellisNode models a byte boundary state within the 64 KiB block horizon.
type TrellisNode struct {
	cost uint32
	r    [4]uint32
	edge commands.TrellisEdge
	nLit uint16
}

// CompressorScratchpad holds cache-resident tables and working memory for compression.
type CompressorScratchpad struct {
	head4       [MaxHashBuckets]int32
	head8       [MaxHashBuckets][4]int32
	head12      [MaxHashBuckets][4]int32
	head16      [MaxHashBuckets][4]int32
	head24      [MaxHashBuckets][4]int32
	prevPos     [MaxWindowHorizon]int32
	nodes       [MaxBlockHorizon + 1]TrellisNode
	litCost     [256]uint32
	subLens     [32]int
	allCmds     [4 * 1024 * 1024]byte
	allLits     [8 * 1024 * 1024]byte
	allOffs     [1024 * 1024]uint32
	s3Buf       [2 * 1024 * 1024]byte
	edgesBuffer [MaxBlockHorizon]commands.TrellisEdge
}

var compScratchPool = generic.NewPool(func() *CompressorScratchpad {
	return new(CompressorScratchpad)
})

// AnalysisParams holds discovered frame attributes.
type AnalysisParams struct {
	stride       uint32
	wLog         uint32
	uncompressed bool
	bMin         uint32
}

// AnalyzeInput evaluates entropy pre-check, stride periodicity, and window sizing.
func AnalyzeInput(src []byte) AnalysisParams {
	u := len(src)
	p := AnalysisParams{bMin: 4}
	if u == 0 {
		p.wLog = 16
		return p
	}
	p.uncompressed = checkHighEntropy(src)
	if p.uncompressed {
		return p
	}
	p.stride = detectStride(src)
	p.wLog = selectWindowLog(u)
	return p
}

// checkHighEntropy inspects a sample for high uncompressed byte diversity.
func checkHighEntropy(src []byte) bool {
	sampleLen := min(len(src), 512)
	if sampleLen < 64 {
		return false
	}
	var seen [256]bool
	unique := 0
	for i := range sampleLen {
		b := src[i]
		if !seen[b] {
			seen[b] = true
			unique++
		}
	}
	threshold := (sampleLen * 88) / 100
	if sampleLen >= 256 {
		threshold = (256 * 88) / 100
	}
	return unique > threshold
}

// detectStride tests periodic column cadences for tabular structures.
func detectStride(src []byte) uint32 {
	if len(src) < 128 {
		return 1
	}
	candidates := []uint32{4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80, 96, 128}
	sampleLen := min(len(src), 2048)
	var bestStride uint32 = 1
	var maxMatches int
	for _, s := range candidates {
		if int(s) >= sampleLen {
			continue
		}
		matches := countStrideMatches(src, int(s), sampleLen)
		if matches > maxMatches && matches > (sampleLen*20)/100 {
			maxMatches = matches
			bestStride = s
		}
	}
	return bestStride
}

// countStrideMatches counts equal byte pairs at given stride interval.
func countStrideMatches(src []byte, s, sampleLen int) int {
	matches := 0
	for i := s; i < sampleLen; i++ {
		if src[i] == src[i-s] {
			matches++
		}
	}
	return matches
}

// selectWindowLog computes optimal window logarithm based on buffer size.
func selectWindowLog(u int) uint32 {
	switch {
	case u <= 1024:
		return 10
	case u <= 4096:
		return 12
	case u <= 16384:
		return 14
	case u <= 65536:
		return 16
	case u <= 262144:
		return 18
	case u <= 1048576:
		return 20
	case u <= 4194304:
		return 22
	default:
		return 24
	}
}

func hash4(src []byte, p int) uint16 {
	return uint16((binary.LittleEndian.Uint32(src[p:p+4]) * HashMultiplier32) >> 16)
}

func hash8(src []byte, p int) uint16 {
	return uint16((binary.LittleEndian.Uint64(src[p:p+8]) * HashMultiplier64) >> 48)
}

func hash12(src []byte, p int) uint16 {
	v1 := binary.LittleEndian.Uint64(src[p : p+8])
	v2 := binary.LittleEndian.Uint64(src[p+4 : p+12])
	return uint16(((v1 ^ (v2 * 0xBF58476D1CE4E5B9)) * HashMultiplier64) >> 48)
}

func hash16(src []byte, p int) uint16 {
	v1 := binary.LittleEndian.Uint64(src[p : p+8])
	v2 := binary.LittleEndian.Uint64(src[p+8 : p+16])
	return uint16(((v1 ^ (v2 * 0xBF58476D1CE4E5B9)) * HashMultiplier64) >> 48)
}

func hash24(src []byte, p int) uint16 {
	v1 := binary.LittleEndian.Uint64(src[p : p+8])
	v2 := binary.LittleEndian.Uint64(src[p+8 : p+16])
	v3 := binary.LittleEndian.Uint64(src[p+16 : p+24])
	h := v1 ^ (v2 * 0xBF58476D1CE4E5B9) ^ (v3 * 0x94D049BB133111EB)
	return uint16((h * HashMultiplier64) >> 48)
}

var distCodeCost [64]uint32

func init() {
	for c := range 64 {
		sumF := 0
		for ctx := range 4 {
			sumF += int(distance.StaticDistFreq4[ctx][c])
		}
		avgF := float64(sumF) / 4.0
		if avgF <= 0 {
			avgF = 1.0
		}
		bits := math.Log2(4096.0 / avgF)
		cost := uint32(bits*2.0 + 0.5)
		if cost < 6 {
			cost = 6
		}
		distCodeCost[c] = cost
	}
}

// getParetoSublengths generates candidate match lengths for trellis search.
func getParetoSublengths(L, bMin int, out []int) []int {
	n := 0
	out[n] = L
	n++
	for delta := 1; delta <= 4; delta++ {
		cand := L - delta
		if cand >= bMin {
			out[n] = cand
			n++
		}
	}
	candBoundary := 14 + bMin
	if candBoundary >= bMin && candBoundary < L-4 {
		out[n] = candBoundary
		n++
	}
	for sub := 8; sub >= bMin; sub-- {
		if sub < L-4 && sub != candBoundary {
			out[n] = sub
			n++
		}
	}
	return out[:n]
}

func calcLitCost(f int, invN float64) uint32 {
	if f == 0 {
		return 32
	}
	bits := -math.Log2(float64(f) * invN)
	cost := uint32(bits*2.0 + 0.5)
	if cost < 2 {
		return 2
	}
	return cost
}

// computeBlockLiteralCosts calculates Shannon surrogate bit costs for literal bytes.
func computeBlockLiteralCosts(block []byte, litCost []uint32) {
	var freq [256]int
	for _, b := range block {
		freq[b]++
	}
	invN := 1.0 / float64(len(block))
	for b := range 256 {
		litCost[b] = calcLitCost(freq[b], invN)
	}
}

func litFlushCost(nLit int) uint32 {
	if nLit == 0 {
		return 0
	}
	if nLit <= 8 {
		return 10
	}
	return 22
}

// costRepeat evaluates parsing cost for match reusing register k.
func costRepeat(L, k, bMin, nLit int) uint32 {
	var base uint32 = 48
	if L <= 3+bMin && nLit <= 7 {
		base = 14
	} else if L <= 18+bMin && nLit <= 15 {
		base = 28
	} else if nLit > 15 {
		base += litFlushCost(nLit)
	}
	bonus := uint32(4 - k)
	if base > bonus+3 {
		return base - bonus
	}
	return 3
}

// costExplicit evaluates base opcode cost plus distance prefix bits.
func costExplicit(L int, dist uint32, bMin, nLit int) uint32 {
	code, _, nb := distance.DistanceToCode(dist)
	var base uint32 = 9
	if nLit >= 1 && nLit <= 3 {
		base = 12
	} else if nLit > 3 {
		base = 9 + litFlushCost(nLit)
	}
	if L > 14+bMin {
		if L <= 14+bMin+127 {
			base += 14
		} else {
			base += 28
		}
	}
	distCost := distCodeCost[code] + uint32(2*nb)
	if L == bMin && dist > 1024 {
		distCost += 6
	}
	return base + distCost
}

// measureMatchLength computes contiguous byte match length between two positions using 64-bit SWAR.
func measureMatchLength(src []byte, p1, p2, maxLen int) int {
	l := 0
	for l+8 <= maxLen {
		diff := binary.LittleEndian.Uint64(src[p1+l:]) ^ binary.LittleEndian.Uint64(src[p2+l:])
		if diff != 0 {
			return l + (bits.TrailingZeros64(diff) >> 3)
		}
		l += 8
	}
	for l < maxLen && src[p1+l] == src[p2+l] {
		l++
	}
	return l
}

// relaxLiteral advances state by emitting one literal byte.
func relaxLiteral(i int, s *CompressorScratchpad, block []byte) {
	c := block[i]
	nextCost := s.nodes[i].cost + s.litCost[c]
	if nextCost < s.nodes[i+1].cost {
		s.nodes[i+1].cost = nextCost
		s.nodes[i+1].r = s.nodes[i].r
		s.nodes[i+1].nLit = s.nodes[i].nLit + 1
		s.nodes[i+1].edge = commands.TrellisEdge{From: uint16(i), Length: 1, Kind: 0}
	}
}

// relaxRepeat tests match against the 4 repeat distance registers.
func relaxRepeat(i int, s *CompressorScratchpad, src []byte, globalPos, bSize, bMin int) {
	curCost := s.nodes[i].cost
	for k := range 4 {
		d := s.nodes[i].r[k]
		if d == 0 || globalPos < int(d) {
			continue
		}
		maxL := min(bSize-i, MaxMatchLength)
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}
		relaxRepeatSublengths(i, k, d, L, bMin, curCost, s)
	}
}

func relaxRepeatSublengths(i, k int, d uint32, L, bMin int, curCost uint32, s *CompressorScratchpad) {
	rNext := s.nodes[i].r
	distance.UpdateLRURepeat(&rNext, uint8(k))
	subLens := getParetoSublengths(L, bMin, s.subLens[:])
	nLit := int(s.nodes[i].nLit)
	for _, l := range subLens {
		cost := curCost + costRepeat(l, k, bMin, nLit)
		if cost < s.nodes[i+l].cost {
			s.nodes[i+l].cost = cost
			s.nodes[i+l].r = rNext
			s.nodes[i+l].nLit = 0
			s.nodes[i+l].edge = commands.TrellisEdge{
				From:   uint16(i),
				Length: uint16(l),
				Dist:   d,
				Kind:   1,
				Arg:    uint8(k),
			}
		}
	}
}

// relaxBrackets probes direct fused bracket distances (1..8, 16, 32).
func relaxBrackets(i int, s *CompressorScratchpad, src []byte, globalPos, bSize, bMin int) {
	curCost := s.nodes[i].cost
	for b := range 10 {
		d := distance.BracketDistance(b)
		if globalPos < int(d) {
			continue
		}
		maxL := min(bSize-i, 15+bMin)
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}
		relaxFixedSublengths(i, 2, uint8(b), d, L, bMin, curCost, s)
	}
}

// relaxStride probes periodic stride column offsets (m * S).
func relaxStride(i int, s *CompressorScratchpad, src []byte, globalPos, bSize int, stride uint32, bMin int) {
	if stride <= 1 {
		return
	}
	curCost := s.nodes[i].cost
	for m := 1; m <= 8; m++ {
		d := uint32(m) * stride
		if globalPos < int(d) {
			continue
		}
		maxL := min(bSize-i, 15+bMin)
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}
		relaxFixedSublengths(i, 3, uint8(m), d, L, bMin, curCost, s)
	}
}

func relaxFixedSublengths(i int, kind, arg uint8, d uint32, L, bMin int, curCost uint32, s *CompressorScratchpad) {
	rNext := s.nodes[i].r
	distance.UpdateLRUExplicit(&rNext, d)
	subLens := getParetoSublengths(L, bMin, s.subLens[:])
	nLit := int(s.nodes[i].nLit)
	costBase := curCost + 22
	if nLit > 15 {
		costBase += litFlushCost(nLit)
	}
	for _, l := range subLens {
		if costBase < s.nodes[i+l].cost {
			s.nodes[i+l].cost = costBase
			s.nodes[i+l].r = rNext
			s.nodes[i+l].nLit = 0
			s.nodes[i+l].edge = commands.TrellisEdge{From: uint16(i), Length: uint16(l), Dist: d, Kind: kind, Arg: arg}
		}
	}
}

// relaxHashMatches queries multi-tier hash tables for sliding window matches.
func relaxHashMatches(
	i int,
	s *CompressorScratchpad,
	src []byte,
	globalPos, bSize, maxDist, winMask, bMin, depthLimit, level int,
) {
	if i+4 > bSize {
		return
	}
	curCost := s.nodes[i].cost
	if level >= 6 && i+24 <= bSize && globalPos+24 <= len(src) {
		relaxHash24Matches(i, s, src, globalPos, bSize, maxDist, bMin, curCost)
	}
	if level >= 5 && i+16 <= bSize && globalPos+16 <= len(src) {
		relaxHash16Matches(i, s, src, globalPos, bSize, maxDist, bMin, curCost)
	}
	if level >= 4 && i+12 <= bSize && globalPos+12 <= len(src) {
		relaxHash12Matches(i, s, src, globalPos, bSize, maxDist, bMin, curCost)
	}
	if level >= 3 && i+8 <= bSize && globalPos+8 <= len(src) {
		relaxHash8Matches(i, s, src, globalPos, bSize, maxDist, bMin, curCost)
	}
	relaxHash4Matches(i, s, src, globalPos, bSize, maxDist, winMask, bMin, depthLimit, curCost)
}

func relaxHash24Matches(
	i int,
	s *CompressorScratchpad,
	src []byte,
	globalPos, bSize, maxDist, bMin int,
	curCost uint32,
) {
	h24 := hash24(src, globalPos)
	maxL := min(bSize-i, MaxMatchLength)
	for k := 0; k < 4; k++ {
		cand := s.head24[h24][k]
		if cand < 0 {
			break
		}
		candPos := int(cand)
		d := uint32(globalPos - candPos)
		if d == 0 || d > uint32(maxDist) {
			continue
		}
		L := measureMatchLength(src, globalPos, candPos, maxL)
		if L >= 24 {
			relaxExplicitSublengths(i, d, L, bMin, curCost, s)
		}
	}
}

func relaxHash16Matches(
	i int,
	s *CompressorScratchpad,
	src []byte,
	globalPos, bSize, maxDist, bMin int,
	curCost uint32,
) {
	h16 := hash16(src, globalPos)
	maxL := min(bSize-i, MaxMatchLength)
	for k := 0; k < 4; k++ {
		cand := s.head16[h16][k]
		if cand < 0 {
			break
		}
		candPos := int(cand)
		d := uint32(globalPos - candPos)
		if d == 0 || d > uint32(maxDist) {
			continue
		}
		L := measureMatchLength(src, globalPos, candPos, maxL)
		if L >= 16 {
			relaxExplicitSublengths(i, d, L, bMin, curCost, s)
		}
	}
}

func relaxHash12Matches(
	i int,
	s *CompressorScratchpad,
	src []byte,
	globalPos, bSize, maxDist, bMin int,
	curCost uint32,
) {
	h12 := hash12(src, globalPos)
	maxL := min(bSize-i, MaxMatchLength)
	for k := 0; k < 4; k++ {
		cand := s.head12[h12][k]
		if cand < 0 {
			break
		}
		candPos := int(cand)
		d := uint32(globalPos - candPos)
		if d == 0 || d > uint32(maxDist) {
			continue
		}
		L := measureMatchLength(src, globalPos, candPos, maxL)
		if L >= 12 {
			relaxExplicitSublengths(i, d, L, bMin, curCost, s)
		}
	}
}

func relaxHash8Matches(
	i int,
	s *CompressorScratchpad,
	src []byte,
	globalPos, bSize, maxDist, bMin int,
	curCost uint32,
) {
	h8 := hash8(src, globalPos)
	maxL := min(bSize-i, MaxMatchLength)
	for k := 0; k < 4; k++ {
		cand := s.head8[h8][k]
		if cand < 0 {
			break
		}
		candPos := int(cand)
		d := uint32(globalPos - candPos)
		if d == 0 || d > uint32(maxDist) {
			continue
		}
		L := measureMatchLength(src, globalPos, candPos, maxL)
		if L >= 8 {
			relaxExplicitSublengths(i, d, L, bMin, curCost, s)
		}
	}
}

func relaxHash4Matches(
	i int,
	s *CompressorScratchpad,
	src []byte,
	globalPos, bSize, maxDist, winMask, bMin, depthLimit int,
	curCost uint32,
) {
	h4 := hash4(src, globalPos)
	cand := s.head4[h4]
	depth := 0
	for cand >= 0 && depth < depthLimit {
		candPos := int(cand)
		d := uint32(globalPos - candPos)
		if d == 0 || d > uint32(maxDist) {
			break
		}
		maxL := min(bSize-i, MaxMatchLength)
		L := measureMatchLength(src, globalPos, candPos, maxL)
		if L >= 4 {
			relaxExplicitSublengths(i, d, L, bMin, curCost, s)
		}
		cand = s.prevPos[candPos&winMask]
		depth++
	}
}

func relaxExplicitSublengths(i int, d uint32, L, bMin int, curCost uint32, s *CompressorScratchpad) {
	if d >= 128 {
		for mode := byte(0); mode < 3; mode++ {
			ref := s.nodes[i].r[mode]
			diff := int64(d) - int64(ref)
			if diff >= -128 && diff <= 127 && diff != 0 {
				relaxDiffLatticeSublengths(i, mode, int8(diff), d, L, bMin, curCost, s)
			}
		}
	}

	rNext := s.nodes[i].r
	distance.UpdateLRUExplicit(&rNext, d)
	subLens := getParetoSublengths(L, bMin, s.subLens[:])
	nLit := int(s.nodes[i].nLit)
	for _, l := range subLens {
		cost := curCost + costExplicit(l, d, bMin, nLit)
		if cost < s.nodes[i+l].cost {
			s.nodes[i+l].cost = cost
			s.nodes[i+l].r = rNext
			s.nodes[i+l].nLit = 0
			s.nodes[i+l].edge = commands.TrellisEdge{From: uint16(i), Length: uint16(l), Dist: d, Kind: 4}
		}
	}
}

// relaxDictMatches probes static RFC 7932 dictionary words.
func relaxDictMatches(i int, s *CompressorScratchpad, src []byte, globalPos, bSize, bMin int) {
	if i+4 > bSize || globalPos+4 > len(src) {
		return
	}
	curCost := s.nodes[i].cost
	nLit := int(s.nodes[i].nLit)
	flushCost := litFlushCost(nLit)
	cdict.FindStaticDictMatches(src[globalPos:], func(wid uint32, tau uint8, matchLen int) {
		if matchLen < 5 || i+matchLen > bSize {
			return
		}
		var base uint32 = 36
		if matchLen >= 8 {
			base = 28
		}
		if tau != 0 {
			base += 8
		}
		cost := curCost + base + flushCost
		if cost < s.nodes[i+matchLen].cost {
			s.nodes[i+matchLen].cost = cost
			s.nodes[i+matchLen].r = s.nodes[i].r
			s.nodes[i+matchLen].nLit = 0
			s.nodes[i+matchLen].edge = commands.TrellisEdge{
				From:   uint16(i),
				Length: uint16(matchLen),
				Dist:   wid,
				Kind:   5,
				Arg:    tau,
			}
		}
	})
}

// relaxDiffLattice probes difference lattice matches (opcode 0xCE) around stride columns.
func relaxDiffLattice(i int, s *CompressorScratchpad, src []byte, globalPos, bSize int, stride uint32, bMin int) {
	if stride <= 1 {
		return
	}
	curCost := s.nodes[i].cost
	strideDeltas := [...]int8{-2, -1, 1, 2}
	for _, delta := range strideDeltas {
		d := distance.ComputeDiffLatticeDist(3<<6, delta, stride, &s.nodes[i].r)
		if d <= 0 || globalPos < int(d) {
			continue
		}
		maxL := min(bSize-i, 13+bMin)
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}
		relaxDiffLatticeSublengths(i, 3, delta, uint32(d), L, bMin, curCost, s)
	}
}

func relaxDiffLatticeSublengths(
	i int,
	mode byte,
	delta int8,
	d uint32,
	L, bMin int,
	curCost uint32,
	s *CompressorScratchpad,
) {
	rNext := s.nodes[i].r
	distance.UpdateLRUExplicit(&rNext, d)
	subLens := getParetoSublengths(L, bMin, s.subLens[:])
	nLit := int(s.nodes[i].nLit)
	var flushCost uint32
	if nLit > 6 {
		flushCost = litFlushCost(nLit)
	}
	for _, l := range subLens {
		diff := l - bMin
		var cost uint32
		if diff < 7 {
			cost = curCost + 22 + flushCost
		} else {
			cost = curCost + 32 + flushCost
		}
		if cost < s.nodes[i+l].cost {
			s.nodes[i+l].cost = cost
			s.nodes[i+l].r = rNext
			s.nodes[i+l].nLit = 0
			s.nodes[i+l].edge = commands.TrellisEdge{
				From:   uint16(i),
				Length: uint16(l),
				Dist:   uint32(byte(delta)),
				Kind:   6,
				Arg:    mode,
			}
		}
	}
}

// insertHash updates multi-scale hash tables at the current position.
func insertHash(src []byte, globalPos, winMask int, s *CompressorScratchpad) {
	pos := int32(globalPos)
	maskPos := globalPos & winMask
	h4 := hash4(src, globalPos)
	s.prevPos[maskPos] = s.head4[h4]
	s.head4[h4] = pos
	if globalPos+8 <= len(src) {
		h8 := hash8(src, globalPos)
		s.head8[h8][3] = s.head8[h8][2]
		s.head8[h8][2] = s.head8[h8][1]
		s.head8[h8][1] = s.head8[h8][0]
		s.head8[h8][0] = pos
	}
	if globalPos+12 <= len(src) {
		h12 := hash12(src, globalPos)
		s.head12[h12][3] = s.head12[h12][2]
		s.head12[h12][2] = s.head12[h12][1]
		s.head12[h12][1] = s.head12[h12][0]
		s.head12[h12][0] = pos
	}
	if globalPos+16 <= len(src) {
		h16 := hash16(src, globalPos)
		s.head16[h16][3] = s.head16[h16][2]
		s.head16[h16][2] = s.head16[h16][1]
		s.head16[h16][1] = s.head16[h16][0]
		s.head16[h16][0] = pos
	}
	if globalPos+24 <= len(src) {
		h24 := hash24(src, globalPos)
		s.head24[h24][3] = s.head24[h24][2]
		s.head24[h24][2] = s.head24[h24][1]
		s.head24[h24][1] = s.head24[h24][0]
		s.head24[h24][0] = pos
	}
}

// initBlockNodes resets Trellis forward costs and register snapshots.
func initBlockNodes(s *CompressorScratchpad, bSize int, initialR [4]uint32) {
	for i := 0; i <= bSize; i++ {
		s.nodes[i].cost = InfinityCost
		s.nodes[i].nLit = 0
	}
	s.nodes[0].cost = 0
	s.nodes[0].r = initialR
	s.nodes[0].nLit = 0
}

// solveBlockTrellis runs DAG shortest-path relaxation over the 64 KiB block.
func solveBlockTrellis(
	src []byte,
	blockStart, bSize, maxDist, winMask int,
	stride uint32,
	bMin, depthLimit, level int,
	s *CompressorScratchpad,
) {
	block := src[blockStart : blockStart+bSize]
	computeBlockLiteralCosts(block, s.litCost[:])
	for i := range bSize {
		globalPos := blockStart + i
		if s.nodes[i].cost != InfinityCost {
			relaxLiteral(i, s, block)
			relaxRepeat(i, s, src, globalPos, bSize, bMin)
			relaxBrackets(i, s, src, globalPos, bSize, bMin)
			relaxStride(i, s, src, globalPos, bSize, stride, bMin)
			relaxHashMatches(i, s, src, globalPos, bSize, maxDist, winMask, bMin, depthLimit, level)
			relaxDictMatches(i, s, src, globalPos, bSize, bMin)
			relaxDiffLattice(i, s, src, globalPos, bSize, stride, bMin)
		}
		if i+4 <= bSize {
			insertHash(src, globalPos, winMask, s)
		}
	}
}

// backtrackEdges traces optimal edges backwards from block end to start.
func backtrackEdges(s *CompressorScratchpad, bSize int) []commands.TrellisEdge {
	curr := bSize
	count := 0
	for curr > 0 {
		edge := s.nodes[curr].edge
		s.edgesBuffer[count] = edge
		count++
		curr = int(edge.From)
	}
	for i := 0; i < count/2; i++ {
		s.edgesBuffer[i], s.edgesBuffer[count-1-i] = s.edgesBuffer[count-1-i], s.edgesBuffer[i]
	}
	return s.edgesBuffer[:count]
}

// packageSections dispatches unpacked stream buffers to their respective codecs.
func packageSections(
	wLog uint32,
	cmds, lits []byte,
	offs []uint32,
	s *CompressorScratchpad,
) ([]byte, []byte, []byte, []byte, uint8) {
	s1 := []byte{0x00, byte(wLog), 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	s2 := commands.PackageCommandsSection(cmds)
	s3 := distance.PackageOffsetsSection(offs, s.s3Buf[:])
	s4, flagNibble := literals.PackageLiteralsSection(lits)
	return s1, s2, s3, s4, flagNibble
}

// assembleContainer creates the PRZM frame with uncompressed fallback guard.
func assembleContainer(src []byte, params AnalysisParams, s1, s2, s3, s4 []byte, flagNibble uint8) []byte {
	u := len(src)
	dirLen := 4
	if len(s2) > 65535 || len(s3) > 65535 {
		dirLen = 6
	}
	compSize := 8 + dirLen + len(s1) + len(s2) + len(s3) + len(s4)
	if params.stride > 1 {
		compSize++
	}

	if compSize >= u || params.uncompressed {
		out := make([]byte, 8+u)
		copy(out[:4], "PRZM")
		out[4] = FlagUncompressed
		out[5] = byte(u)
		out[6] = byte(u >> 8)
		out[7] = byte(u >> 16)
		copy(out[8:], src)
		return out
	}

	return assembleCompressedFrame(u, params, s1, s2, s3, s4, flagNibble)
}

// assembleCompressedFrame concatenates header, directory, and the 4 sections.
func assembleCompressedFrame(u int, params AnalysisParams, s1, s2, s3, s4 []byte, flagNibble uint8) []byte {
	flags := flagNibble
	if params.stride > 1 {
		flags |= FlagStride
	}
	extOffset := len(s2) > 65535 || len(s3) > 65535
	if extOffset {
		flags |= FlagExtOffset
	}

	dirLen := 4
	if extOffset {
		dirLen = 6
	}
	headerLen := 8 + dirLen
	if params.stride > 1 {
		headerLen++
	}

	out := make([]byte, 0, headerLen+len(s1)+len(s2)+len(s3)+len(s4)+64)
	out = append(out, 'P', 'R', 'Z', 'M', flags)
	out = append(out, byte(u&0xFF), byte((u>>8)&0xFF), byte((u>>16)&0xFF))

	if params.stride > 1 {
		out = append(out, byte(params.stride-1))
	}

	if extOffset {
		out = append(out,
			byte(len(s2)&0xFF), byte((len(s2)>>8)&0xFF), byte((len(s2)>>16)&0xFF),
			byte(len(s3)&0xFF), byte((len(s3)>>8)&0xFF), byte((len(s3)>>16)&0xFF),
		)
	} else {
		out = binary.LittleEndian.AppendUint16(out, uint16(len(s2)))
		out = binary.LittleEndian.AppendUint16(out, uint16(len(s3)))
	}

	out = append(out, s1...)
	out = append(out, s2...)
	out = append(out, s3...)
	out = append(out, s4...)
	return out
}

// compressInternal executes the compression pipeline with clean scratchpad reuse.
func compressInternal(src []byte, opts Options) ([]byte, error) {
	level := opts.Level
	if level <= 0 {
		level = 6
	}
	params := AnalyzeInput(src)
	if opts.WindowLog >= 10 && opts.WindowLog <= 24 {
		params.wLog = uint32(opts.WindowLog)
	} else if opts.WindowBits >= 10 && opts.WindowBits <= 24 {
		params.wLog = uint32(opts.WindowBits)
	}
	if params.uncompressed {
		return assembleContainer(src, params, nil, nil, nil, nil, 0), nil
	}
	s := compScratchPool.Get()
	defer compScratchPool.Put(s)
	rawDict := opts.RawDict
	if len(rawDict) == 0 && opts.DictID != 0 {
		rawDict = GetRawDictionary(opts.DictID)
	}
	return executeCompressionPipeline(src, level, rawDict, params, s)
}

// executeCompressionPipeline parses blocks and serializes final compressed frame.
func executeCompressionPipeline(
	src []byte,
	level int,
	dict []byte,
	params AnalysisParams,
	s *CompressorScratchpad,
) ([]byte, error) {
	u := len(src)
	for i := range s.head4 {
		s.head4[i] = -1
		for k := range 4 {
			s.head8[i][k] = -1
			s.head12[i][k] = -1
			s.head16[i][k] = -1
			s.head24[i][k] = -1
		}
	}
	winSize := 1 << params.wLog
	winMask := winSize - 1
	clearLen := min(u, winSize)
	for i := range clearLen {
		s.prevPos[i] = -1
	}

	_ = dict // Generic dynamic dictionary injection hook

	allCmds := s.allCmds[:0]
	allLits := s.allLits[:0]
	allOffs := s.allOffs[:0]

	initialR := [4]uint32{1, 4, 8, 16}
	if params.stride > 1 {
		initialR = [4]uint32{1, params.stride, 2 * params.stride, 3 * params.stride}
	}

	maxDist := winSize
	depthLimit := selectSearchDepth(level)
	pos := 0

	for pos < u {
		bSize := min(u-pos, MaxBlockHorizon)
		initBlockNodes(s, bSize, initialR)
		solveBlockTrellis(src, pos, bSize, maxDist, winMask, params.stride, int(params.bMin), depthLimit, level, s)
		edges := backtrackEdges(s, bSize)
		commands.EmitBlockCommands(edges, src[pos:pos+bSize], int(params.bMin), &allCmds, &allLits, &allOffs)
		initialR = s.nodes[bSize].r
		pos += bSize
	}

	s1, s2, s3, s4, flagNibble := packageSections(params.wLog, allCmds, allLits, allOffs, s)
	return assembleContainer(src, params, s1, s2, s3, s4, flagNibble), nil
}
