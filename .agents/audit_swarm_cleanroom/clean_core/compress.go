package clean_core

import (
	"encoding/binary"
	"math"

	"github.com/lemon4ksan/foundation/generic"
)

// Constants for Trellis RDO and Match Finder.
const (
	MaxBlockHorizon    = 65536
	MaxHashBuckets     = 65536
	HashMultiplier32   = uint32(0x1E35A7BD)
	HashMultiplier64   = uint64(0x9E3779B97F4A7C15)
	MaxSearchDepth     = 8
	MaxMatchLength     = 1024
	CostUnitScale      = 16 // 16 units = 1 byte = 8 bits
	InfinityCost       = uint32(0x3FFFFFFF)
)

// TrellisEdge defines a parsing transition edge in the Trellis DAG.
type TrellisEdge struct {
	from   uint16
	length uint16
	dist   uint32
	kind   uint8 // 0: lit, 1: rep, 2: bracket, 3: stride, 4: explicit
	arg    uint8 // k for rep, b for bracket, m for stride
}

// TrellisNode models a byte boundary state within the 64 KiB block horizon.
type TrellisNode struct {
	cost uint32
	r    [4]uint32
	edge TrellisEdge
}

// CompressorScratchpad holds cache-resident tables and working memory for compression.
type CompressorScratchpad struct {
	head3       [MaxHashBuckets]int32
	head4       [MaxHashBuckets]int32
	head8       [MaxHashBuckets]int32
	prevPos     [MaxBlockHorizon]int32
	nodes       [MaxBlockHorizon + 1]TrellisNode
	litCost     [256]uint32
	subLens     [32]int
	allCmds     [4 * 1024 * 1024]byte
	allLits     [8 * 1024 * 1024]byte
	allOffs     [1024 * 1024]uint32
	s3Buf       [2 * 1024 * 1024]byte
	edgesBuffer [MaxBlockHorizon]TrellisEdge
}

var compScratchPool = generic.NewPool[CompressorScratchpad](func() *CompressorScratchpad {
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

// checkHighEntropy inspects a 512-byte sample for high uncompressed byte diversity.
func checkHighEntropy(src []byte) bool {
	sampleLen := len(src)
	if sampleLen > 512 {
		sampleLen = 512
	}
	if sampleLen < 64 {
		return false
	}

	var seen [256]bool
	unique := 0
	for i := 0; i < sampleLen; i++ {
		b := src[i]
		if !seen[b] {
			seen[b] = true
			unique++
		}
	}

	var threshold int
	if sampleLen >= 256 {
		threshold = (256 * 88) / 100
	} else {
		threshold = (sampleLen * 88) / 100
	}
	return unique > threshold
}

// detectStride tests periodic column cadences for tabular structures.
func detectStride(src []byte) uint32 {
	if len(src) < 128 {
		return 1
	}
	candidates := []uint32{4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80, 96, 128}
	sampleLen := len(src)
	if sampleLen > 2048 {
		sampleLen = 2048
	}

	var bestStride uint32 = 1
	var maxMatches int

	for _, s := range candidates {
		if int(s) >= sampleLen {
			continue
		}
		matches := 0
		for i := int(s); i < sampleLen; i++ {
			if src[i] == src[i-int(s)] {
				matches++
			}
		}
		if matches > maxMatches && matches > (sampleLen*20)/100 {
			maxMatches = matches
			bestStride = s
		}
	}
	return bestStride
}

// selectWindowLog sizes the sliding window based on input capacity.
func selectWindowLog(u int) uint32 {
	if u <= 65536 {
		return 16
	}
	if u <= 1024*1024 {
		return 20
	}
	return 23
}

// Hash calculations for multi-tier indexing.
func hash3(src []byte, p int) uint16 {
	val := uint32(src[p]) | (uint32(src[p+1]) << 8) | (uint32(src[p+2]) << 16)
	return uint16((val * HashMultiplier32) >> 16)
}

func hash4(src []byte, p int) uint16 {
	val := binary.LittleEndian.Uint32(src[p : p+4])
	return uint16((val * HashMultiplier32) >> 16)
}

func hash8(src []byte, p int) uint16 {
	val := binary.LittleEndian.Uint64(src[p : p+8])
	return uint16((val * HashMultiplier64) >> 48)
}

// getParetoSublengths generates sub-length candidates to prevent greedy lock-in.
func getParetoSublengths(L, bMin int, out []int) []int {
	out = out[:0]
	if L <= 18 {
		for l := bMin; l <= L; l++ {
			out = append(out, l)
		}
		return out
	}
	candidates := []int{4, 5, 6, 7, 8, 12, 16, 18, L - 2, L - 1, L}
	for _, l := range candidates {
		if l >= bMin && l <= L {
			out = append(out, l)
		}
	}
	return out
}

// computeBlockLiteralCosts calculates empirical entropy bit-costs for the block.
func computeBlockLiteralCosts(block []byte, litCost []uint32) {
	var counts [256]int
	for _, b := range block {
		counts[b]++
	}
	bSize := float64(len(block))
	for i := 0; i < 256; i++ {
		if counts[i] == 0 {
			litCost[i] = 26
			continue
		}
		prob := float64(counts[i]) / bSize
		costBits := -math.Log2(prob)
		costUnits := int(math.Round(costBits * 2.0))
		if costUnits < 2 {
			costUnits = 2
		} else if costUnits > 26 {
			costUnits = 26
		}
		litCost[i] = uint32(costUnits)
	}
}

// costRepeat evaluates base cost and locality preservation bonus.
func costRepeat(L, k, bMin int) uint32 {
	var base uint32
	if L <= 3+bMin {
		base = 10
	} else if L <= 14+bMin {
		base = 20
	} else if L <= 14+bMin+127 {
		base = 30
	} else {
		base = 40
	}

	bonusTable := [4]uint32{6, 3, 1, 0}
	bonus := bonusTable[k&3]
	if base > bonus+3 {
		return base - bonus
	}
	return 3
}

// costExplicit evaluates base opcode cost plus distance prefix bits.
func costExplicit(L int, dist uint32, bMin int) uint32 {
	var base uint32
	if L <= 14+bMin {
		base = 16
	} else if L <= 14+bMin+127 {
		base = 30
	} else {
		base = 46
	}
	_, _, nb := DistanceToCode(dist)
	return base + uint32(2*nb)
}

// measureMatchLength computes contiguous byte match length between two positions.
func measureMatchLength(src []byte, p1, p2, maxLen int) int {
	l := 0
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
		s.nodes[i+1].edge = TrellisEdge{from: uint16(i), length: 1, kind: 0}
	}
}

// relaxRepeat tests match against the 4 repeat distance registers.
func relaxRepeat(i int, s *CompressorScratchpad, src []byte, globalPos, bSize, bMin int) {
	curCost := s.nodes[i].cost
	for k := 0; k < 4; k++ {
		d := s.nodes[i].r[k]
		if d == 0 || globalPos < int(d) {
			continue
		}
		maxL := bSize - i
		if maxL > MaxMatchLength {
			maxL = MaxMatchLength
		}
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}

		rNext := s.nodes[i].r
		updateLRURepeat(&rNext, uint8(k))
		subLens := getParetoSublengths(L, bMin, s.subLens[:])

		for _, l := range subLens {
			cost := curCost + costRepeat(l, k, bMin)
			if cost < s.nodes[i+l].cost {
				s.nodes[i+l].cost = cost
				s.nodes[i+l].r = rNext
				s.nodes[i+l].edge = TrellisEdge{from: uint16(i), length: uint16(l), dist: d, kind: 1, arg: uint8(k)}
			}
		}
	}
}

// relaxBrackets probes direct fused bracket distances (1..8, 16, 32).
func relaxBrackets(i int, s *CompressorScratchpad, src []byte, globalPos, bSize, bMin int) {
	curCost := s.nodes[i].cost
	for b := 0; b < 10; b++ {
		var d uint32
		if b <= 7 {
			d = uint32(b + 1)
		} else if b == 8 {
			d = 16
		} else {
			d = 32
		}
		if globalPos < int(d) {
			continue
		}
		maxL := bSize - i
		if maxL > 15+bMin {
			maxL = 15 + bMin
		}
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}

		rNext := s.nodes[i].r
		updateLRUExplicit(&rNext, d)
		subLens := getParetoSublengths(L, bMin, s.subLens[:])

		for _, l := range subLens {
			cost := curCost + 22
			if cost < s.nodes[i+l].cost {
				s.nodes[i+l].cost = cost
				s.nodes[i+l].r = rNext
				s.nodes[i+l].edge = TrellisEdge{from: uint16(i), length: uint16(l), dist: d, kind: 2, arg: uint8(b)}
			}
		}
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
		maxL := bSize - i
		if maxL > 15+bMin {
			maxL = 15 + bMin
		}
		if maxL < bMin {
			continue
		}
		L := measureMatchLength(src, globalPos, globalPos-int(d), maxL)
		if L < bMin {
			continue
		}

		rNext := s.nodes[i].r
		updateLRUExplicit(&rNext, d)
		subLens := getParetoSublengths(L, bMin, s.subLens[:])

		for _, l := range subLens {
			cost := curCost + 22
			if cost < s.nodes[i+l].cost {
				s.nodes[i+l].cost = cost
				s.nodes[i+l].r = rNext
				s.nodes[i+l].edge = TrellisEdge{from: uint16(i), length: uint16(l), dist: d, kind: 3, arg: uint8(m)}
			}
		}
	}
}

// relaxHashMatches queries multi-tier hash tables for sliding window matches.
func relaxHashMatches(i int, s *CompressorScratchpad, src []byte, globalPos, bSize, maxDist, bMin int) {
	if i+4 > bSize {
		return
	}
	curCost := s.nodes[i].cost
	h4 := hash4(src, globalPos)
	cand := s.head4[h4]
	depth := 0

	for cand >= 0 && depth < MaxSearchDepth {
		candPos := int(cand)
		d := uint32(globalPos - candPos)
		if d == 0 || d > uint32(maxDist) {
			break
		}

		maxL := bSize - i
		if maxL > MaxMatchLength {
			maxL = MaxMatchLength
		}
		L := measureMatchLength(src, globalPos, candPos, maxL)
		if L >= bMin {
			rNext := s.nodes[i].r
			updateLRUExplicit(&rNext, d)
			subLens := getParetoSublengths(L, bMin, s.subLens[:])

			for _, l := range subLens {
				cost := curCost + costExplicit(l, d, bMin)
				if cost < s.nodes[i+l].cost {
					s.nodes[i+l].cost = cost
					s.nodes[i+l].r = rNext
					s.nodes[i+l].edge = TrellisEdge{from: uint16(i), length: uint16(l), dist: d, kind: 4}
				}
			}
		}
		cand = s.prevPos[candPos%MaxBlockHorizon]
		depth++
	}
}

// insertHash inserts current position into hash indexes.
func insertHash(src []byte, globalPos, i int, s *CompressorScratchpad) {
	if globalPos+4 <= len(src) {
		h4 := hash4(src, globalPos)
		s.prevPos[globalPos%MaxBlockHorizon] = s.head4[h4]
		s.head4[h4] = int32(globalPos)
	}
}

// initBlockNodes resets Trellis DP nodes for a new block horizon.
func initBlockNodes(s *CompressorScratchpad, bSize int, initialR [4]uint32) {
	s.nodes[0].cost = 0
	s.nodes[0].r = initialR
	s.nodes[0].edge = TrellisEdge{}
	for i := 1; i <= bSize; i++ {
		s.nodes[i].cost = InfinityCost
		s.nodes[i].edge = TrellisEdge{}
	}
}

// solveBlockTrellis executes Bellman DP relaxation across the block.
func solveBlockTrellis(src []byte, blockStart, bSize, maxDist int, stride uint32, bMin int, s *CompressorScratchpad) {
	block := src[blockStart : blockStart+bSize]
	computeBlockLiteralCosts(block, s.litCost[:])

	for i := 0; i < bSize; i++ {
		if s.nodes[i].cost == InfinityCost {
			continue
		}
		globalPos := blockStart + i
		relaxLiteral(i, s, block)
		relaxRepeat(i, s, src, globalPos, bSize, bMin)
		relaxBrackets(i, s, src, globalPos, bSize, bMin)
		relaxStride(i, s, src, globalPos, bSize, stride, bMin)
		relaxHashMatches(i, s, src, globalPos, bSize, maxDist, bMin)
		insertHash(src, globalPos, i, s)
	}
}

// backtrackEdges traces back the optimal shortest path through the DAG.
func backtrackEdges(s *CompressorScratchpad, bSize int) []TrellisEdge {
	count := 0
	curr := bSize
	for curr > 0 {
		e := s.nodes[curr].edge
		s.edgesBuffer[count] = e
		count++
		curr = int(e.from)
	}

	// Reverse to forward chronological order
	for i, j := 0, count-1; i < j; i, j = i+1, j-1 {
		s.edgesBuffer[i], s.edgesBuffer[j] = s.edgesBuffer[j], s.edgesBuffer[i]
	}
	return s.edgesBuffer[:count]
}

// emitBlockCommands translates DP decisions into commands, literals, and offsets.
func emitBlockCommands(edges []TrellisEdge, block []byte, bMin int,
	cmdsOut, litsOut *[]byte, offsOut *[]uint32) {
	var pendingLits []byte

	for _, e := range edges {
		if e.kind == 0 {
			pendingLits = append(pendingLits, block[e.from])
			continue
		}
		emitMatchWithLiterals(e, &pendingLits, bMin, cmdsOut, litsOut, offsOut)
	}

	if len(pendingLits) > 0 {
		emitLiteralRun(pendingLits, cmdsOut, litsOut)
	}
}

// emitMatchWithLiterals fuses literals into match opcodes when possible.
func emitMatchWithLiterals(e TrellisEdge, pendingLits *[]byte, bMin int,
	cmdsOut, litsOut *[]byte, offsOut *[]uint32) {
	nLit := len(*pendingLits)
	lMatch := int(e.length)

	switch e.kind {
	case 1: // Repeat
		if nLit <= 7 && lMatch <= 3+bMin {
			op := byte((nLit << 4) | ((lMatch - bMin) << 2) | int(e.arg))
			*cmdsOut = append(*cmdsOut, op)
		} else {
			if nLit > 15 {
				emitLiteralRun(*pendingLits, cmdsOut, litsOut)
				*pendingLits = nil
				nLit = 0
			}
			op := byte(0xC0 | int(e.arg))
			if lMatch-bMin < 15 {
				tok := byte((nLit << 4) | (lMatch - bMin))
				*cmdsOut = append(*cmdsOut, op, tok)
			} else {
				tok := byte((nLit << 4) | 15)
				*cmdsOut = append(*cmdsOut, op, tok)
				*cmdsOut = appendLEB128Enc(*cmdsOut, uint32(lMatch-bMin-15))
			}
		}
	case 2: // Bracket
		if nLit > 15 {
			emitLiteralRun(*pendingLits, cmdsOut, litsOut)
			*pendingLits = nil
			nLit = 0
		}
		op := byte(0xC4 + int(e.arg))
		tok := byte((nLit << 4) | (lMatch - bMin))
		*cmdsOut = append(*cmdsOut, op, tok)
	case 3: // Stride
		if nLit > 15 {
			emitLiteralRun(*pendingLits, cmdsOut, litsOut)
			*pendingLits = nil
			nLit = 0
		}
		op := byte(0xD0 + int(e.arg-1))
		tok := byte((nLit << 4) | (lMatch - bMin))
		*cmdsOut = append(*cmdsOut, op, tok)
	case 4: // Explicit
		emitExplicitMatch(e, pendingLits, bMin, cmdsOut, litsOut, offsOut)
	}

	if len(*pendingLits) > 0 {
		*litsOut = append(*litsOut, *pendingLits...)
		*pendingLits = nil
	}
}

// emitExplicitMatch serializes explicit distance matches with fused or standalone literals.
func emitExplicitMatch(e TrellisEdge, pendingLits *[]byte, bMin int,
	cmdsOut, litsOut *[]byte, offsOut *[]uint32) {
	nLit := len(*pendingLits)
	lMatch := int(e.length)

	if nLit > 3 {
		emitLiteralRun(*pendingLits, cmdsOut, litsOut)
		*pendingLits = nil
		nLit = 0
	}

	if lMatch-bMin < 15 {
		op := byte(0x80 | (nLit << 4) | (lMatch - bMin))
		*cmdsOut = append(*cmdsOut, op)
	} else {
		op := byte(0x80 | (nLit << 4) | 15)
		*cmdsOut = append(*cmdsOut, op)
		*cmdsOut = appendLEB128Enc(*cmdsOut, uint32(lMatch-bMin-15))
	}
	*offsOut = append(*offsOut, e.dist)
}

// emitLiteralRun encodes pure literal sequences into standalone tokens.
func emitLiteralRun(lits []byte, cmdsOut, litsOut *[]byte) {
	n := len(lits)
	if n == 0 {
		return
	}
	if n <= 8 {
		*cmdsOut = append(*cmdsOut, byte(0xF2+n))
	} else {
		*cmdsOut = append(*cmdsOut, 0xF0)
		*cmdsOut = appendLEB128Enc(*cmdsOut, uint32(n))
	}
	*litsOut = append(*litsOut, lits...)
}

// appendLEB128Enc encodes an unsigned integer as variable-length LEB128.
func appendLEB128Enc(buf []byte, v uint32) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v&0x7F)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// packageSections serializes the 4 canonical PRZM sections.
func packageSections(wLog uint32, cmds, lits []byte, offs []uint32, s *CompressorScratchpad) ([]byte, []byte, []byte, []byte, uint8) {
	s1 := []byte{0x00, byte(wLog), 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	s2 := packageCommandsSection(cmds)
	s3 := packageOffsetsSection(offs, s)
	s4, flagNibble := packageLiteralsSection(lits)
	return s1, s2, s3, s4, flagNibble
}

// packageCommandsSection encodes Section 2 via Mode 8 or flat mode 0x09.
func packageCommandsSection(cmds []byte) []byte {
	if len(cmds) == 0 {
		return nil
	}
	opBuf := make([]byte, len(cmds))
	metaBuf := make([]byte, len(cmds))
	varBuf := make([]byte, len(cmds))
	nOp, nM, nV, err := SplitCommands(cmds, opBuf, metaBuf, varBuf)
	if err == nil {
		dstBuf := make([]byte, 13+nOp+nM+nV+64)
		nPack, pErr := PackCommandsMode8(opBuf[:nOp], metaBuf[:nM], varBuf[:nV], len(cmds), dstBuf)
		if pErr == nil && nPack < len(cmds) {
			out := make([]byte, nPack)
			copy(out, dstBuf[:nPack])
			return out
		}
	}
	out := make([]byte, 1+len(cmds))
	out[0] = 0x09
	copy(out[1:], cmds)
	return out
}

// packageOffsetsSection encodes Section 3 via Distance Prefix or flat mode 0x09.
func packageOffsetsSection(offs []uint32, s *CompressorScratchpad) []byte {
	if len(offs) == 0 {
		return nil
	}
	s3Len, err := PackDistancePrefix(offs, ModeDistancePrefix, true, s.s3Buf[:])
	var rawOffs []byte
	for _, d := range offs {
		rawOffs = appendLEB128Enc(rawOffs, d)
	}
	if err == nil && s3Len < len(rawOffs) {
		out := make([]byte, s3Len)
		copy(out, s.s3Buf[:s3Len])
		return out
	}
	out := make([]byte, 1+len(rawOffs))
	out[0] = 0x09
	copy(out[1:], rawOffs)
	return out
}

// packageLiteralsSection evaluates Étale Sheaf foliation vs verbatim fallback.
func packageLiteralsSection(lits []byte) ([]byte, uint8) {
	if len(lits) == 0 {
		return nil, 0
	}
	var bestSheaf []byte
	minLen := len(lits)

	for mode := uint8(0); mode <= 3; mode++ {
		packed, err := PackEtaleSheaf(lits, mode)
		if err == nil && len(packed) < minLen {
			minLen = len(packed)
			bestSheaf = packed
		}
	}

	if bestSheaf != nil {
		return bestSheaf, FlagNibble
	}
	return lits, 0
}

// assembleContainer creates the PRZM frame with uncompressed fallback guard.
func assembleContainer(src []byte, params AnalysisParams, s1, s2, s3, s4 []byte, flagNibble uint8) []byte {
	u := len(src)
	dirLen := 4
	compSize := 8 + dirLen + len(s1) + len(s2) + len(s3) + len(s4)
	if params.stride > 1 {
		compSize++
	}

	if compSize >= u || params.uncompressed {
		out := make([]byte, 8+u)
		out[0], out[1], out[2], out[3] = 'P', 'R', 'Z', 'M'
		out[4] = FlagUncompressed
		out[5] = byte(u & 0xFF)
		out[6] = byte((u >> 8) & 0xFF)
		out[7] = byte((u >> 16) & 0xFF)
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

	out := make([]byte, 0, 8+len(s1)+len(s2)+len(s3)+len(s4)+64)
	out = append(out, 'P', 'R', 'Z', 'M', flags)
	out = append(out, byte(u&0xFF), byte((u>>8)&0xFF), byte((u>>16)&0xFF))

	if params.stride > 1 {
		out = append(out, byte(params.stride-1))
	}

	out = binary.LittleEndian.AppendUint16(out, uint16(len(s2)))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(s3)))

	out = append(out, s1...)
	out = append(out, s2...)
	out = append(out, s3...)
	out = append(out, s4...)
	return out
}

// Compress compresses an uncompressed byte buffer using Trellis RDO.
func Compress(src []byte, level int) ([]byte, error) {
	return CompressWithDict(src, level, nil)
}

// CompressWithDict compresses an input buffer with optional dictionary support.
func CompressWithDict(src []byte, level int, dict []byte) ([]byte, error) {
	_ = level
	_ = dict
	u := len(src)
	if u == 0 {
		return []byte{'P', 'R', 'Z', 'M', 0, 0, 0, 0}, nil
	}
	if u > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	params := AnalyzeInput(src)
	if params.uncompressed {
		return assembleContainer(src, params, nil, nil, nil, nil, 0), nil
	}

	s := compScratchPool.Get()
	defer compScratchPool.Put(s)

	return executeCompressionPipeline(src, params, s)
}

// executeCompressionPipeline parses blocks and serializes final compressed frame.
func executeCompressionPipeline(src []byte, params AnalysisParams, s *CompressorScratchpad) ([]byte, error) {
	u := len(src)
	for i := range s.head4 {
		s.head4[i] = -1
	}

	var allCmds []byte
	var allLits []byte
	var allOffs []uint32

	initialR := [4]uint32{1, 4, 8, 16}
	if params.stride > 1 {
		initialR = [4]uint32{1, params.stride, 2 * params.stride, 3 * params.stride}
	}

	maxDist := 1 << params.wLog
	pos := 0

	for pos < u {
		bSize := u - pos
		if bSize > MaxBlockHorizon {
			bSize = MaxBlockHorizon
		}
		initBlockNodes(s, bSize, initialR)
		solveBlockTrellis(src, pos, bSize, maxDist, params.stride, int(params.bMin), s)
		edges := backtrackEdges(s, bSize)
		emitBlockCommands(edges, src[pos:pos+bSize], int(params.bMin), &allCmds, &allLits, &allOffs)
		initialR = s.nodes[bSize].r
		pos += bSize
	}

	s1, s2, s3, s4, flagNibble := packageSections(params.wLog, allCmds, allLits, allOffs, s)
	return assembleContainer(src, params, s1, s2, s3, s4, flagNibble), nil
}
