// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"unsafe"
)

// PWS (Pareto-Wave Synthesis) constants
const (
	pwsMagic0 uint8 = 0x50 // 'P'
	pwsMagic1 uint8 = 0x57 // 'W'
	pwsVer1   uint8 = 0x01

	maxL4Rules = 16
	maxL5Rules = 8
)

var (
	ErrPWSCorrupted    = errors.New("przm/pws: corrupted stream")
	ErrPWSSizeMismatch = errors.New("przm/pws: size mismatch")
)

// RuleL4 represents a Layer 4 production rule: expanding to 2 Layer 3 rules (16 bytes).
type RuleL4 struct {
	L3A uint8
	L3B uint8
}

// RuleL5 represents a Layer 5 production rule: expanding to 2 Layer 4 rules (32 bytes).
type RuleL5 struct {
	L4A uint8
	L4B uint8
}

// GrammarH_DAG5 extends the stratified H-DAG to depth D <= 5 (up to 32-byte non-terminals).
type GrammarH_DAG5 struct {
	L1 []RuleL1
	L2 []RuleL2
	L3 []RuleL3
	L4 []RuleL4
	L5 []RuleL5

	// Flat zero-stack vector tables
	ExpL1 [maxL1Rules]uint16
	ExpL2 [maxL2Rules]uint32
	ExpL3 [maxL3Rules]uint64
	ExpL4 [maxL4Rules][16]byte // 128-bit vector
	ExpL5 [maxL5Rules][32]byte // 256-bit vector
}

// BuildExpansionTables builds direct 16-, 32-, 64-, 128-, and 256-bit vector lookup arrays.
func (g *GrammarH_DAG5) BuildExpansionTables() {
	// L1 (2B)
	for i, r := range g.L1 {
		g.ExpL1[i] = uint16(r.U) | (uint16(r.V) << 8)
	}
	// L2 (4B)
	for j, r := range g.L2 {
		w0 := uint32(g.ExpL1[r.L1A])
		w1 := uint32(g.ExpL1[r.L1B])
		g.ExpL2[j] = w0 | (w1 << 16)
	}
	// L3 (8B)
	for k, r := range g.L3 {
		d0 := uint64(g.ExpL2[r.L2A])
		d1 := uint64(g.ExpL2[r.L2B])
		g.ExpL3[k] = d0 | (d1 << 32)
	}
	// L4 (16B = 128-bit)
	for m, r := range g.L4 {
		binary.LittleEndian.PutUint64(g.ExpL4[m][0:8], g.ExpL3[r.L3A])
		binary.LittleEndian.PutUint64(g.ExpL4[m][8:16], g.ExpL3[r.L3B])
	}
	// L5 (32B = 256-bit)
	for n, r := range g.L5 {
		copy(g.ExpL5[n][0:16], g.ExpL4[r.L4A][:])
		copy(g.ExpL5[n][16:32], g.ExpL4[r.L4B][:])
	}
}

// ExpandL5String returns the human-readable string for a 32-byte Layer 5 rule.
func (g *GrammarH_DAG5) ExpandL5String(idx int) string {
	if idx >= len(g.L5) {
		return ""
	}
	return string(g.ExpL5[idx][:])
}

// ExpandL4String returns the human-readable string for a 16-byte Layer 4 rule.
func (g *GrammarH_DAG5) ExpandL4String(idx int) string {
	if idx >= len(g.L4) {
		return ""
	}
	return string(g.ExpL4[idx][:])
}

// InduceGrammar5 induces a 5-layer H-DAG grammar (2B, 4B, 8B, 16B, 32B) over src.
func InduceGrammar5(src []byte) (*GrammarH_DAG5, []uint16) {
	sp := grammarScratchpadPool.Get()
	defer grammarScratchpadPool.Put(sp)

	g, tokens := induceGrammar5WithScratchpad(src, sp)
	resTokens := make([]uint16, len(tokens))
	copy(resTokens, tokens)
	return g, resTokens
}

func induceGrammar5WithScratchpad(src []byte, sp *grammarScratchpad) (*GrammarH_DAG5, []uint16) {
	baseG, tokens := induceGrammarWithScratchpad(src, sp)

	g := &GrammarH_DAG5{
		L1: baseG.L1,
		L2: baseG.L2,
		L3: baseG.L3,
	}

	if len(g.L3) == 0 || len(tokens) < 4 {
		g.BuildExpansionTables()
		return g, tokens
	}

	// ==========================================
	// Phase 4: Induce Layer 4 Rules (16-byte pairs: L3 + L3)
	// ==========================================
	sp.cands = sp.cands[:0]
	var dirtyL4 [16]uint32
	dirtyL4Count := 0
	clear(sp.freq256[:])

	l3Count := uint16(len(g.L3))
	tLen := len(tokens)
	for i := 0; i < tLen-1; i++ {
		tA, tB := tokens[i], tokens[i+1]
		if tA >= 1024 && tA < 1024+l3Count && tB >= 1024 && tB < 1024+l3Count {
			pk := (uint32(tA-1024) << 4) | uint32(tB-1024)
			if sp.freq256[pk] == 0 && dirtyL4Count < len(dirtyL4) {
				dirtyL4[dirtyL4Count] = pk
				dirtyL4Count++
			}
			sp.freq256[pk]++
		}
	}

	for idx := 0; idx < dirtyL4Count; idx++ {
		pk := dirtyL4[idx]
		c := int(sp.freq256[pk])
		if c >= 2 { // Maneth threshold: occ >= 2 is profitable for 16B rules
			sp.cands = insertTopCand(sp.cands, maxL4Rules, pairCandidate{key: pk, count: c})
		}
	}

	l4Limit := len(sp.cands)
	if l4Limit > maxL4Rules {
		l4Limit = maxL4Rules
	}

	for i := 0; i < l4Limit; i++ {
		k := sp.cands[i].key
		l3A := uint8(k >> 4)
		l3B := uint8(k & 0x0F)
		l4ID := uint8(len(g.L4))
		g.L4 = append(g.L4, RuleL4{L3A: l3A, L3B: l3B})
		sp.pairTo256[k] = l4ID + 1
	}

	// Substitute Layer 4 rules (token base 2048)
	if len(g.L4) > 0 {
		outTokens := sp.otherBuffer(tokens)
		for i := 0; i < tLen; {
			if i < tLen-1 && tokens[i] >= 1024 && tokens[i] < 1024+l3Count &&
				tokens[i+1] >= 1024 && tokens[i+1] < 1024+l3Count {
				pk := (uint32(tokens[i]-1024) << 4) | uint32(tokens[i+1]-1024)
				if id := sp.pairTo256[pk]; id > 0 {
					outTokens = append(outTokens, 2048+uint16(id-1))
					i += 2
					continue
				}
			}
			outTokens = append(outTokens, tokens[i])
			i++
		}
		for i := 0; i < l4Limit; i++ {
			sp.pairTo256[sp.cands[i].key] = 0
		}
		tokens = outTokens
	}

	// ==========================================
	// Phase 5: Induce Layer 5 Rules (32-byte pairs: L4 + L4)
	// ==========================================
	if len(g.L4) > 0 && len(tokens) >= 4 {
		sp.cands = sp.cands[:0]
		var dirtyL5 [16]uint32
		dirtyL5Count := 0
		clear(sp.freq256[:])

		l4Count := uint16(len(g.L4))
		tLen = len(tokens)
		for i := 0; i < tLen-1; i++ {
			tA, tB := tokens[i], tokens[i+1]
			if tA >= 2048 && tA < 2048+l4Count && tB >= 2048 && tB < 2048+l4Count {
				pk := (uint32(tA-2048) << 4) | uint32(tB-2048)
				if sp.freq256[pk] == 0 && dirtyL5Count < len(dirtyL5) {
					dirtyL5[dirtyL5Count] = pk
					dirtyL5Count++
				}
				sp.freq256[pk]++
			}
		}

		for idx := 0; idx < dirtyL5Count; idx++ {
			pk := dirtyL5[idx]
			c := int(sp.freq256[pk])
			if c >= 2 {
				sp.cands = insertTopCand(sp.cands, maxL5Rules, pairCandidate{key: pk, count: c})
			}
		}

		l5Limit := len(sp.cands)
		if l5Limit > maxL5Rules {
			l5Limit = maxL5Rules
		}

		for i := 0; i < l5Limit; i++ {
			k := sp.cands[i].key
			l4A := uint8(k >> 4)
			l4B := uint8(k & 0x0F)
			l5ID := uint8(len(g.L5))
			g.L5 = append(g.L5, RuleL5{L4A: l4A, L4B: l4B})
			sp.pairTo256[k] = l5ID + 1
		}

		// Substitute Layer 5 rules (token base 4096)
		if len(g.L5) > 0 {
			outTokens := sp.otherBuffer(tokens)
			for i := 0; i < tLen; {
				if i < tLen-1 && tokens[i] >= 2048 && tokens[i] < 2048+l4Count &&
					tokens[i+1] >= 2048 && tokens[i+1] < 2048+l4Count {
					pk := (uint32(tokens[i]-2048) << 4) | uint32(tokens[i+1]-2048)
					if id := sp.pairTo256[pk]; id > 0 {
						outTokens = append(outTokens, 4096+uint16(id-1))
						i += 2
						continue
					}
				}
				outTokens = append(outTokens, tokens[i])
				i++
			}
			for i := 0; i < l5Limit; i++ {
				sp.pairTo256[sp.cands[i].key] = 0
			}
			tokens = outTokens
		}
	}

	g.BuildExpansionTables()
	return g, tokens
}

// PWSEncode encodes data using the 5-layer AVX-Wave H-DAG.
func PWSEncode(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	stride := 1
	workSrc := src
	if n >= 256 {
		s, score := DetectStride(src)
		if score >= 0.22 && s >= 2 {
			stride = s
			workSrc = TransposeStride(src, s)
		}
	}

	sp := grammarScratchpadPool.Get()
	defer grammarScratchpadPool.Put(sp)

	g, tokens := induceGrammar5WithScratchpad(workSrc, sp)

	rulesHeaderSize := 16 + len(g.L1)*2 + len(g.L2)*2 + len(g.L3)*2 + len(g.L4)*2 + len(g.L5)*2
	out := make([]byte, 0, rulesHeaderSize+len(tokens))

	strideByte := uint8(0)
	if stride >= 2 {
		strideByte = uint8(stride)
	}
	out = append(out, pwsMagic0, pwsMagic1, pwsVer1, strideByte)
	var szBuf [8]byte
	binary.LittleEndian.PutUint32(szBuf[0:4], uint32(n))
	binary.LittleEndian.PutUint32(szBuf[4:8], uint32(len(tokens)))
	out = append(out, szBuf[:]...)

	// Rule counts
	out = append(out, uint8(len(g.L1)), uint8(len(g.L2)), uint8(len(g.L3)), uint8(len(g.L4)), uint8(len(g.L5)), 0x00, 0x00, 0x00)

	// Rule tables
	for _, r := range g.L1 {
		out = append(out, r.U, r.V)
	}
	for _, r := range g.L2 {
		out = append(out, r.L1A, r.L1B)
	}
	for _, r := range g.L3 {
		out = append(out, r.L2A, r.L2B)
	}
	for _, r := range g.L4 {
		out = append(out, r.L3A, r.L3B)
	}
	for _, r := range g.L5 {
		out = append(out, r.L4A, r.L4B)
	}

	// Pack tokens
	for _, tok := range tokens {
		if tok < 256 {
			b := byte(tok)
			if b < 0xC0 {
				out = append(out, b)
			} else {
				out = append(out, 0xFF, b)
			}
		} else if tok < 512 {
			id := uint8(tok - 256)
			if id < 16 {
				out = append(out, 0xC0+id)
			} else {
				out = append(out, 0xFC, id)
			}
		} else if tok < 1024 {
			id := uint8(tok - 512)
			if id < 16 {
				out = append(out, 0xD0+id)
			} else {
				out = append(out, 0xFD, id)
			}
		} else if tok < 2048 {
			// Layer 3 (8B)
			id := uint8(tok - 1024)
			if id < 12 {
				out = append(out, 0xE0+id)
			} else {
				out = append(out, 0xFE, id) // 12..31 (< 32)
			}
		} else if tok < 4096 {
			// Layer 4 (16B)
			id := uint8(tok - 2048)
			if id < 8 {
				out = append(out, 0xEC+id)
			} else {
				out = append(out, 0xFE, 32+id) // 40..47 (< 48)
			}
		} else {
			// Layer 5 (32B)
			id := uint8(tok - 4096)
			if id < 8 {
				out = append(out, 0xF4+id)
			} else {
				out = append(out, 0xFE, 48+id) // >= 48
			}
		}
	}

	return out, nil
}

// PWSDecode performs instantaneous 256-bit AVX-Wave zero-stack decompression.
func PWSDecode(src []byte) ([]byte, error) {
	if len(src) < 24 {
		return nil, ErrPWSCorrupted
	}

	if src[0] != pwsMagic0 || src[1] != pwsMagic1 || src[2] != pwsVer1 {
		return nil, ErrPWSCorrupted
	}

	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	numL1 := int(src[12])
	numL2 := int(src[13])
	numL3 := int(src[14])
	numL4 := int(src[15])
	numL5 := int(src[16])

	ptr := 20
	rulesSize := numL1*2 + numL2*2 + numL3*2 + numL4*2 + numL5*2
	if ptr+rulesSize > len(src) {
		return nil, ErrPWSCorrupted
	}

	var g GrammarH_DAG5
	g.L1 = make([]RuleL1, numL1)
	for i := 0; i < numL1; i++ {
		g.L1[i] = RuleL1{U: src[ptr], V: src[ptr+1]}
		ptr += 2
	}
	g.L2 = make([]RuleL2, numL2)
	for i := 0; i < numL2; i++ {
		g.L2[i] = RuleL2{L1A: src[ptr], L1B: src[ptr+1]}
		ptr += 2
	}
	g.L3 = make([]RuleL3, numL3)
	for i := 0; i < numL3; i++ {
		g.L3[i] = RuleL3{L2A: src[ptr], L2B: src[ptr+1]}
		ptr += 2
	}
	g.L4 = make([]RuleL4, numL4)
	for i := 0; i < numL4; i++ {
		g.L4[i] = RuleL4{L3A: src[ptr], L3B: src[ptr+1]}
		ptr += 2
	}
	g.L5 = make([]RuleL5, numL5)
	for i := 0; i < numL5; i++ {
		g.L5[i] = RuleL5{L4A: src[ptr], L4B: src[ptr+1]}
		ptr += 2
	}

	g.BuildExpansionTables()

	dst := make([]byte, origSize+32) // 32-byte safety padding for 256-bit stores
	dstPtr := unsafe.Pointer(&dst[0])
	pDst := dstPtr

	axiomStream := src[ptr:]
	axPtr := 0
	axEnd := len(axiomStream)

	// Single-cycle vector store loop
	for axPtr < axEnd {
		op := axiomStream[axPtr]
		axPtr++

		if op < 0xC0 {
			*(*byte)(pDst) = op
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		// Layer 5 (32 bytes = 256-bit store in 1 instruction)
		if op >= 0xF4 && op <= 0xFB {
			id := int(op - 0xF4)
			*(*[32]byte)(pDst) = g.ExpL5[id]
			pDst = unsafe.Add(pDst, 32)
			continue
		}

		// Layer 4 (16 bytes = 128-bit store in 1 instruction)
		if op >= 0xEC && op <= 0xF3 {
			id := int(op - 0xEC)
			*(*[16]byte)(pDst) = g.ExpL4[id]
			pDst = unsafe.Add(pDst, 16)
			continue
		}

		// Layer 3 (8 bytes = 64-bit store)
		if op >= 0xE0 && op <= 0xEB {
			id := int(op - 0xE0)
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		// Layer 2 (4 bytes = 32-bit store)
		if op >= 0xD0 && op <= 0xDF {
			id := int(op - 0xD0)
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		// Layer 1 (2 bytes = 16-bit store)
		if op >= 0xC0 && op <= 0xCF {
			id := int(op - 0xC0)
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		// Escapes
		if op == 0xFF {
			if axPtr >= axEnd {
				return nil, ErrPWSCorrupted
			}
			raw := axiomStream[axPtr]
			axPtr++
			*(*byte)(pDst) = raw
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op == 0xFC {
			if axPtr >= axEnd {
				return nil, ErrPWSCorrupted
			}
			id := int(axiomStream[axPtr])
			axPtr++
			if id >= len(g.ExpL1) {
				return nil, ErrPWSCorrupted
			}
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		if op == 0xFD {
			if axPtr >= axEnd {
				return nil, ErrPWSCorrupted
			}
			id := int(axiomStream[axPtr])
			axPtr++
			if id >= len(g.ExpL2) {
				return nil, ErrPWSCorrupted
			}
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op == 0xFE {
			if axPtr >= axEnd {
				return nil, ErrPWSCorrupted
			}
			id := int(axiomStream[axPtr])
			axPtr++
			if id < 32 {
				if id >= len(g.ExpL3) {
					return nil, ErrPWSCorrupted
				}
				*(*uint64)(pDst) = g.ExpL3[id]
				pDst = unsafe.Add(pDst, 8)
			} else if id < 48 {
				rID := id - 32
				if rID >= len(g.ExpL4) {
					return nil, ErrPWSCorrupted
				}
				*(*[16]byte)(pDst) = g.ExpL4[rID]
				pDst = unsafe.Add(pDst, 16)
			} else {
				rID := id - 48
				if rID >= len(g.ExpL5) {
					return nil, ErrPWSCorrupted
				}
				*(*[32]byte)(pDst) = g.ExpL5[rID]
				pDst = unsafe.Add(pDst, 32)
			}
			continue
		}
	}

	decodedSize := int(uintptr(pDst) - uintptr(dstPtr))
	if decodedSize != origSize {
		return nil, ErrPWSSizeMismatch
	}

	res := dst[:origSize]
	stride := int(src[3])
	if stride >= 2 {
		return UntransposeStride(res, stride), nil
	}
	return res, nil
}

// ============================================================================
// Vector 4: Columnar Auto-Stride Transposition (CAST over F_256)
// ============================================================================

// DetectStride detects the optimal columnar periodicity S in [2, 64] via autocorrelation.
func DetectStride(src []byte) (int, float64) {
	n := len(src)
	if n < 256 {
		return 1, 0.0
	}

	bestS := 1
	bestScore := 0.0

	// Test candidate power-of-two and standard data strides
	candidates := []int{2, 3, 4, 8, 12, 16, 24, 32, 48, 64}

	for _, s := range candidates {
		if s*4 > n {
			continue
		}
		matches := 0
		samples := (n - s) / 2
		if samples > 2048 {
			samples = 2048
		}
		for i := 0; i < samples; i++ {
			if src[i] == src[i+s] {
				matches++
			}
		}
		score := float64(matches) / float64(samples)
		// We subtract random chance baseline (1/256 ≈ 0.0039)
		if score > 0.18 && score > bestScore {
			bestScore = score
			bestS = s
		}
	}

	return bestS, bestScore
}

// TransposeStride orthogonalizes an (N/S) x S byte stream into S contiguous byte planes.
func TransposeStride(src []byte, S int) []byte {
	n := len(src)
	if S <= 1 || n < S {
		res := make([]byte, n)
		copy(res, src)
		return res
	}

	rows := n / S
	rem := n % S
	out := make([]byte, n)

	idx := 0
	for col := 0; col < S; col++ {
		for r := 0; r < rows; r++ {
			out[idx] = src[r*S+col]
			idx++
		}
	}
	// Tail
	if rem > 0 {
		copy(out[idx:], src[rows*S:])
	}

	return out
}

// UntransposeStride reconstructs the original stream from S contiguous byte planes.
func UntransposeStride(src []byte, S int) []byte {
	n := len(src)
	if S <= 1 || n < S {
		res := make([]byte, n)
		copy(res, src)
		return res
	}

	rows := n / S
	rem := n % S
	out := make([]byte, n)

	idx := 0
	for col := 0; col < S; col++ {
		for r := 0; r < rows; r++ {
			out[r*S+col] = src[idx]
			idx++
		}
	}
	if rem > 0 {
		copy(out[rows*S:], src[idx:])
	}

	return out
}
