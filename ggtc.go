// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"github.com/lemon4ksan/foundation/generic"
)

const (
	ggtcMagic0 uint8 = 0x47 // 'G'
	ggtcMagic1 uint8 = 0x47 // 'G'
	ggtcVer1   uint8 = 0x01

	maxL1Rules = 64
	maxL2Rules = 32
	maxL3Rules = 16

	minDigramOccurrences = 3
)

var (
	ErrGGTCCorrupted    = errors.New("przm/ggtc: corrupted grammar stream")
	ErrGGTCSizeMismatch = errors.New("przm/ggtc: decompressed size mismatch")
	ErrGGTCOutputTooBig = errors.New("przm/ggtc: destination buffer overflow")
)

// DigramMatrix computes and analyzes the 256x256 transition adjacency counts of symbols.
type DigramMatrix struct {
	Counts [256][256]uint32
	Total  uint32
}

// BuildDigramMatrix computes the adjacency matrix A[u][v] for adjacent symbols in src.
func BuildDigramMatrix(src []byte) DigramMatrix {
	var m DigramMatrix
	if len(src) < 2 {
		return m
	}
	for i := 0; i < len(src)-1; i++ {
		m.Counts[src[i]][src[i+1]]++
	}
	m.Total = uint32(len(src) - 1)
	return m
}

// TopDigram represents a frequent adjacent symbol pair with its occurrence count.
type TopDigram struct {
	U     uint16
	V     uint16
	Count uint32
	Score int
}

// RuleL1 represents a Layer 1 production rule: expanding to 2 terminal bytes [U, V].
type RuleL1 struct {
	U byte
	V byte
}

// RuleL2 represents a Layer 2 production rule: expanding to 2 Layer 1 rules [A, B] (4 bytes).
type RuleL2 struct {
	L1A uint8
	L1B uint8
}

// RuleL3 represents a Layer 3 production rule: expanding to 2 Layer 2 rules [C, D] (8 bytes).
type RuleL3 struct {
	L2A uint8
	L2B uint8
}

// GrammarH_DAG encapsulates the stratified hierarchical DAG grammar rules.
type GrammarH_DAG struct {
	L1 []RuleL1 // Layer 1 (2 bytes)
	L2 []RuleL2 // Layer 2 (4 bytes)
	L3 []RuleL3 // Layer 3 (8 bytes)

	// Precomputed flat expansion tables for zero-stack SIMD expansion
	ExpL1 [maxL1Rules]uint16
	ExpL2 [maxL2Rules]uint32
	ExpL3 [maxL3Rules]uint64
}

// BuildExpansionTables precalculates 16-bit, 32-bit, and 64-bit word lookups for rules.
func (g *GrammarH_DAG) BuildExpansionTables() {
	// 1. Layer 1 (2 bytes)
	for i, r := range g.L1 {
		val := uint16(r.U) | (uint16(r.V) << 8)
		g.ExpL1[i] = val
	}

	// 2. Layer 2 (4 bytes = 2 * L1)
	for j, r := range g.L2 {
		w0 := uint32(g.ExpL1[r.L1A])
		w1 := uint32(g.ExpL1[r.L1B])
		g.ExpL2[j] = w0 | (w1 << 16)
	}

	// 3. Layer 3 (8 bytes = 2 * L2)
	for k, r := range g.L3 {
		d0 := uint64(g.ExpL2[r.L2A])
		d1 := uint64(g.ExpL2[r.L2B])
		g.ExpL3[k] = d0 | (d1 << 32)
	}
}

// ExpandL3String returns the human-readable ASCII string for an 8-byte Layer 3 rule.
func (g *GrammarH_DAG) ExpandL3String(k int) string {
	if k >= len(g.L3) {
		return ""
	}
	val := g.ExpL3[k]
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, val)
	return string(b)
}

// ExpandL2String returns the human-readable ASCII string for a 4-byte Layer 2 rule.
func (g *GrammarH_DAG) ExpandL2String(j int) string {
	if j >= len(g.L2) {
		return ""
	}
	val := g.ExpL2[j]
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, val)
	return string(b)
}

// GGTCResult contains the induced grammar, compressed axiom, and compression metrics.
type GGTCResult struct {
	Grammar      GrammarH_DAG
	Axiom        []byte
	OriginalSize int
	EncodedSize  int
	Ratio        float64
}

type pairCandidate struct {
	key   uint32
	count int
}

func insertTopCand(cands []pairCandidate, maxLen int, cand pairCandidate) []pairCandidate {
	n := len(cands)
	pos := -1
	for i := 0; i < n; i++ {
		if cand.count > cands[i].count {
			pos = i
			break
		}
	}
	if pos == -1 {
		if n < maxLen {
			return append(cands, cand)
		}
		return cands
	}
	if n < maxLen {
		cands = append(cands, pairCandidate{})
		copy(cands[pos+1:], cands[pos:n])
		cands[pos] = cand
		return cands
	}
	copy(cands[pos+1:], cands[pos:maxLen-1])
	cands[pos] = cand
	return cands
}

type grammarScratchpad struct {
	tokensA   []uint16
	tokensB   []uint16
	dirtyL1   []uint32
	cands     []pairCandidate
	freqL1    [65536]uint16
	pairToL1  [65536]uint8
	freqL2    [4096]uint16
	pairToL2  [4096]uint8
	freqL3    [1024]uint16
	pairToL3  [1024]uint8
	freq256   [256]uint16
	pairTo256 [256]uint8
}

var grammarScratchpadPool = generic.NewPool(func() *grammarScratchpad {
	return &grammarScratchpad{
		tokensA: make([]uint16, 0, 65536),
		tokensB: make([]uint16, 0, 65536),
		dirtyL1: make([]uint32, 0, 4096),
		cands:   make([]pairCandidate, 0, 64),
	}
})

func (sp *grammarScratchpad) ensureTokens(n int) {
	if cap(sp.tokensA) < n {
		sp.tokensA = make([]uint16, n)
	} else {
		sp.tokensA = sp.tokensA[:n]
	}
	if cap(sp.tokensB) < n {
		sp.tokensB = make([]uint16, n)
	} else {
		sp.tokensB = sp.tokensB[:n]
	}
}

func (sp *grammarScratchpad) otherBuffer(curr []uint16) []uint16 {
	if len(curr) > 0 && len(sp.tokensA) > 0 && &curr[0] == &sp.tokensA[0] {
		return sp.tokensB[:0]
	}
	return sp.tokensA[:0]
}

// InduceGrammar performs stratified H-DAG grammar induction over input bytes.
// Strictly limits depth to D <= 3 with fixed powers-of-two rule lengths (2B, 4B, 8B).
func InduceGrammar(src []byte) (*GrammarH_DAG, []uint16) {
	sp := grammarScratchpadPool.Get()
	defer grammarScratchpadPool.Put(sp)

	g, tokens := induceGrammarWithScratchpad(src, sp)
	resTokens := make([]uint16, len(tokens))
	copy(resTokens, tokens)
	return g, resTokens
}

func induceGrammarWithScratchpad(src []byte, sp *grammarScratchpad) (*GrammarH_DAG, []uint16) {
	n := len(src)
	sp.ensureTokens(n)
	tokens := sp.tokensA[:n]
	for i, b := range src {
		tokens[i] = uint16(b)
	}

	g := &GrammarH_DAG{}
	if n < 16 {
		return g, tokens
	}

	// ==========================================
	// Phase 1: Induce Layer 1 Rules (2-byte pairs)
	// ==========================================
	sp.cands = sp.cands[:0]
	sp.dirtyL1 = sp.dirtyL1[:0]
	tLen := len(tokens)
	for i := 0; i < tLen-1; i++ {
		u := tokens[i]
		v := tokens[i+1]
		if u < 256 && v < 256 {
			pk := (uint32(u) << 8) | uint32(v)
			if sp.freqL1[pk] == 0 {
				sp.dirtyL1 = append(sp.dirtyL1, pk)
			}
			sp.freqL1[pk]++
		}
	}

	for _, pk := range sp.dirtyL1 {
		c := int(sp.freqL1[pk])
		if c >= minDigramOccurrences {
			sp.cands = insertTopCand(sp.cands, maxL1Rules, pairCandidate{key: pk, count: c})
		}
		sp.freqL1[pk] = 0
	}
	sp.dirtyL1 = sp.dirtyL1[:0]

	l1Limit := len(sp.cands)
	if l1Limit > maxL1Rules {
		l1Limit = maxL1Rules
	}

	for i := 0; i < l1Limit; i++ {
		k := sp.cands[i].key
		u := byte(k >> 8)
		v := byte(k & 0xFF)
		l1ID := uint8(len(g.L1))
		g.L1 = append(g.L1, RuleL1{U: u, V: v})
		sp.pairToL1[k] = l1ID + 1
	}

	// Substitute Layer 1 rules non-overlapping left-to-right
	if len(g.L1) > 0 {
		outTokens := sp.tokensB[:0]
		for i := 0; i < tLen; {
			if i < tLen-1 && tokens[i] < 256 && tokens[i+1] < 256 {
				pk := (uint32(tokens[i]) << 8) | uint32(tokens[i+1])
				if id := sp.pairToL1[pk]; id > 0 {
					outTokens = append(outTokens, 256+uint16(id-1))
					i += 2
					continue
				}
			}
			outTokens = append(outTokens, tokens[i])
			i++
		}
		for i := 0; i < l1Limit; i++ {
			sp.pairToL1[sp.cands[i].key] = 0
		}
		tokens = outTokens
	}

	// ==========================================
	// Phase 2: Induce Layer 2 Rules (4-byte pairs: L1 + L1)
	// ==========================================
	if len(g.L1) > 0 && len(tokens) >= 4 {
		sp.cands = sp.cands[:0]
		tLen = len(tokens)
		var dirtyL2 [64]uint32
		dirtyL2Count := 0
		clear(sp.freqL2[:])

		l1Count := uint16(len(g.L1))
		for i := 0; i < tLen-1; i++ {
			tA := tokens[i]
			tB := tokens[i+1]
			if tA >= 256 && tA < 256+l1Count && tB >= 256 && tB < 256+l1Count {
				pk := (uint32(tA-256) << 6) | uint32(tB-256)
				if sp.freqL2[pk] == 0 && dirtyL2Count < len(dirtyL2) {
					dirtyL2[dirtyL2Count] = pk
					dirtyL2Count++
				}
				sp.freqL2[pk]++
			}
		}

		for idx := 0; idx < dirtyL2Count; idx++ {
			pk := dirtyL2[idx]
			c := int(sp.freqL2[pk])
			if c >= minDigramOccurrences {
				sp.cands = insertTopCand(sp.cands, maxL2Rules, pairCandidate{key: pk, count: c})
			}
		}

		l2Limit := len(sp.cands)
		if l2Limit > maxL2Rules {
			l2Limit = maxL2Rules
		}

		for i := 0; i < l2Limit; i++ {
			k := sp.cands[i].key
			l1A := uint8(k >> 6)
			l1B := uint8(k & 0x3F)
			l2ID := uint8(len(g.L2))
			g.L2 = append(g.L2, RuleL2{L1A: l1A, L1B: l1B})
			sp.pairToL2[k] = l2ID + 1
		}

		// Substitute Layer 2 rules
		if len(g.L2) > 0 {
			outTokens := sp.tokensA[:0]
			for i := 0; i < tLen; {
				if i < tLen-1 && tokens[i] >= 256 && tokens[i] < 256+l1Count &&
					tokens[i+1] >= 256 && tokens[i+1] < 256+l1Count {
					pk := (uint32(tokens[i]-256) << 6) | uint32(tokens[i+1]-256)
					if id := sp.pairToL2[pk]; id > 0 {
						outTokens = append(outTokens, 512+uint16(id-1))
						i += 2
						continue
					}
				}
				outTokens = append(outTokens, tokens[i])
				i++
			}
			for i := 0; i < l2Limit; i++ {
				sp.pairToL2[sp.cands[i].key] = 0
			}
			tokens = outTokens
		}
	}

	// ==========================================
	// Phase 3: Induce Layer 3 Rules (8-byte pairs: L2 + L2)
	// ==========================================
	if len(g.L2) > 0 && len(tokens) >= 4 {
		sp.cands = sp.cands[:0]
		tLen = len(tokens)
		var dirtyL3 [32]uint32
		dirtyL3Count := 0
		clear(sp.freqL3[:])

		l2Count := uint16(len(g.L2))
		for i := 0; i < tLen-1; i++ {
			tA := tokens[i]
			tB := tokens[i+1]
			if tA >= 512 && tA < 512+l2Count && tB >= 512 && tB < 512+l2Count {
				pk := (uint32(tA-512) << 5) | uint32(tB-512)
				if sp.freqL3[pk] == 0 && dirtyL3Count < len(dirtyL3) {
					dirtyL3[dirtyL3Count] = pk
					dirtyL3Count++
				}
				sp.freqL3[pk]++
			}
		}

		for idx := 0; idx < dirtyL3Count; idx++ {
			pk := dirtyL3[idx]
			c := int(sp.freqL3[pk])
			if c >= minDigramOccurrences {
				sp.cands = insertTopCand(sp.cands, maxL3Rules, pairCandidate{key: pk, count: c})
			}
		}

		l3Limit := len(sp.cands)
		if l3Limit > maxL3Rules {
			l3Limit = maxL3Rules
		}

		for i := 0; i < l3Limit; i++ {
			k := sp.cands[i].key
			l2A := uint8(k >> 5)
			l2B := uint8(k & 0x1F)
			l3ID := uint8(len(g.L3))
			g.L3 = append(g.L3, RuleL3{L2A: l2A, L2B: l2B})
			sp.pairToL3[k] = l3ID + 1
		}

		// Substitute Layer 3 rules
		if len(g.L3) > 0 {
			outTokens := sp.tokensB[:0]
			for i := 0; i < tLen; {
				if i < tLen-1 && tokens[i] >= 512 && tokens[i] < 512+l2Count &&
					tokens[i+1] >= 512 && tokens[i+1] < 512+l2Count {
					pk := (uint32(tokens[i]-512) << 5) | uint32(tokens[i+1]-512)
					if id := sp.pairToL3[pk]; id > 0 {
						outTokens = append(outTokens, 1024+uint16(id-1))
						i += 2
						continue
					}
				}
				outTokens = append(outTokens, tokens[i])
				i++
			}
			for i := 0; i < l3Limit; i++ {
				sp.pairToL3[sp.cands[i].key] = 0
			}
			tokens = outTokens
		}
	}

	// Apply Sebastian Maneth's bottom-up grammar pruning and frequency re-ordering
	g, tokens = pruneGrammarWithScratchpad(g, tokens, sp)

	g.BuildExpansionTables()
	return g, tokens
}

type candL3 struct {
	oldID int
	rule  RuleL3
	count int
}

type candL2 struct {
	oldID  int
	rule   RuleL2
	count  int
	parent int
}

type candL1 struct {
	oldID  int
	rule   RuleL1
	count  int
	parent int
}

func sortCandsL3(a []candL3) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].count > a[j-1].count; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func sortCandsL2(a []candL2) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			if a[j].count > a[j-1].count || (a[j].count == a[j-1].count && a[j].parent > a[j-1].parent) {
				a[j], a[j-1] = a[j-1], a[j]
			} else {
				break
			}
		}
	}
}

func sortCandsL1(a []candL1) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			if a[j].count > a[j-1].count || (a[j].count == a[j-1].count && a[j].parent > a[j-1].parent) {
				a[j], a[j-1] = a[j-1], a[j]
			} else {
				break
			}
		}
	}
}

// PruneGrammar applies Sebastian Maneth's bottom-up grammar contribution metric:
//
//	con(A) = |ref(A)| * (|rhs(A)| - 1) - |rhs(A)| <= 0
//
// eliminating non-contributing intermediate rules (con(A) <= 0) and re-sorting
// surviving rules by descending frequency to maximize compact 1-byte direct opcodes.
func PruneGrammar(g *GrammarH_DAG, tokens []uint16) (*GrammarH_DAG, []uint16) {
	sp := grammarScratchpadPool.Get()
	defer grammarScratchpadPool.Put(sp)
	return pruneGrammarWithScratchpad(g, tokens, sp)
}

func pruneGrammarWithScratchpad(g *GrammarH_DAG, tokens []uint16, sp *grammarScratchpad) (*GrammarH_DAG, []uint16) {
	// ==========================================
	// Phase A: Prune & Sort Layer 3 (8-byte rules)
	// ==========================================
	if len(g.L3) > 0 {
		var occL3 [maxL3Rules]int
		for _, tok := range tokens {
			if tok >= 1024 && int(tok-1024) < len(g.L3) {
				occL3[tok-1024]++
			}
		}

		var keptL3 [maxL3Rules]candL3
		keptL3Count := 0
		for id, r := range g.L3 {
			if occL3[id] >= 2 {
				keptL3[keptL3Count] = candL3{oldID: id, rule: r, count: occL3[id]}
				keptL3Count++
			}
		}

		sortCandsL3(keptL3[:keptL3Count])

		var oldToNewL3 [maxL3Rules]int
		for i := range oldToNewL3 {
			oldToNewL3[i] = -1
		}
		newL3 := make([]RuleL3, keptL3Count)
		for newID := 0; newID < keptL3Count; newID++ {
			c := keptL3[newID]
			oldToNewL3[c.oldID] = newID
			newL3[newID] = c.rule
		}

		outTokens := sp.otherBuffer(tokens)
		for _, tok := range tokens {
			if tok >= 1024 && int(tok-1024) < len(g.L3) {
				oldID := int(tok - 1024)
				newID := oldToNewL3[oldID]
				if newID >= 0 {
					outTokens = append(outTokens, 1024+uint16(newID))
				} else {
					r := g.L3[oldID]
					outTokens = append(outTokens, 512+uint16(r.L2A), 512+uint16(r.L2B))
				}
			} else {
				outTokens = append(outTokens, tok)
			}
		}
		tokens = outTokens
		g.L3 = newL3
	}

	// ==========================================
	// Phase B: Prune & Sort Layer 2 (4-byte rules)
	// ==========================================
	if len(g.L2) > 0 {
		var occL2 [maxL2Rules]int
		for _, tok := range tokens {
			if tok >= 512 && tok < 1024 && int(tok-512) < len(g.L2) {
				occL2[tok-512]++
			}
		}

		var parentL2 [maxL2Rules]int
		for _, r3 := range g.L3 {
			if int(r3.L2A) < len(g.L2) {
				parentL2[r3.L2A]++
			}
			if int(r3.L2B) < len(g.L2) {
				parentL2[r3.L2B]++
			}
		}

		var keptL2 [maxL2Rules]candL2
		keptL2Count := 0
		for id, r := range g.L2 {
			if parentL2[id] > 0 || occL2[id] >= 2 {
				keptL2[keptL2Count] = candL2{oldID: id, rule: r, count: occL2[id], parent: parentL2[id]}
				keptL2Count++
			}
		}

		sortCandsL2(keptL2[:keptL2Count])

		var oldToNewL2 [maxL2Rules]int
		for i := range oldToNewL2 {
			oldToNewL2[i] = -1
		}
		newL2 := make([]RuleL2, keptL2Count)
		for newID := 0; newID < keptL2Count; newID++ {
			c := keptL2[newID]
			oldToNewL2[c.oldID] = newID
			newL2[newID] = c.rule
		}

		for k := range g.L3 {
			oldA := int(g.L3[k].L2A)
			oldB := int(g.L3[k].L2B)
			g.L3[k].L2A = uint8(oldToNewL2[oldA])
			g.L3[k].L2B = uint8(oldToNewL2[oldB])
		}

		outTokens := sp.otherBuffer(tokens)
		for _, tok := range tokens {
			if tok >= 512 && tok < 1024 && int(tok-512) < len(g.L2) {
				oldID := int(tok - 512)
				newID := oldToNewL2[oldID]
				if newID >= 0 {
					outTokens = append(outTokens, 512+uint16(newID))
				} else {
					r := g.L2[oldID]
					outTokens = append(outTokens, 256+uint16(r.L1A), 256+uint16(r.L1B))
				}
			} else {
				outTokens = append(outTokens, tok)
			}
		}
		tokens = outTokens
		g.L2 = newL2
	}

	// ==========================================
	// Phase C: Prune & Sort Layer 1 (2-byte rules)
	// ==========================================
	if len(g.L1) > 0 {
		var occL1 [maxL1Rules]int
		for _, tok := range tokens {
			if tok >= 256 && tok < 512 && int(tok-256) < len(g.L1) {
				occL1[tok-256]++
			}
		}

		var parentL1 [maxL1Rules]int
		for _, r2 := range g.L2 {
			if int(r2.L1A) < len(g.L1) {
				parentL1[r2.L1A]++
			}
			if int(r2.L1B) < len(g.L1) {
				parentL1[r2.L1B]++
			}
		}

		var keptL1 [maxL1Rules]candL1
		keptL1Count := 0
		for id, r := range g.L1 {
			if parentL1[id] > 0 || occL1[id] >= 2 {
				keptL1[keptL1Count] = candL1{oldID: id, rule: r, count: occL1[id], parent: parentL1[id]}
				keptL1Count++
			}
		}

		sortCandsL1(keptL1[:keptL1Count])

		var oldToNewL1 [maxL1Rules]int
		for i := range oldToNewL1 {
			oldToNewL1[i] = -1
		}
		newL1 := make([]RuleL1, keptL1Count)
		for newID := 0; newID < keptL1Count; newID++ {
			c := keptL1[newID]
			oldToNewL1[c.oldID] = newID
			newL1[newID] = c.rule
		}

		for j := range g.L2 {
			oldA := int(g.L2[j].L1A)
			oldB := int(g.L2[j].L1B)
			g.L2[j].L1A = uint8(oldToNewL1[oldA])
			g.L2[j].L1B = uint8(oldToNewL1[oldB])
		}

		outTokens := sp.otherBuffer(tokens)
		for _, tok := range tokens {
			if tok >= 256 && tok < 512 && int(tok-256) < len(g.L1) {
				oldID := int(tok - 256)
				newID := oldToNewL1[oldID]
				if newID >= 0 {
					outTokens = append(outTokens, 256+uint16(newID))
				} else {
					r := g.L1[oldID]
					outTokens = append(outTokens, uint16(r.U), uint16(r.V))
				}
			} else {
				outTokens = append(outTokens, tok)
			}
		}
		tokens = outTokens
		g.L1 = newL1
	}

	return g, tokens
}

// GGTCEncode encodes a raw discrete byte buffer into a compact GGTC grammar stream.
// Opcodes:
//
//	0x00 .. 0xCF: Direct raw terminal bytes (< 208)
//	0xD0 .. 0xDF: Direct Layer 1 rules (0..15, expands to 2B)
//	0xE0 .. 0xEF: Direct Layer 2 rules (0..15, expands to 4B)
//	0xF0 .. 0xFB: Direct Layer 3 rules (0..11, expands to 8B)
//	0xFC <id>:    Extended Layer 1 rule (id >= 16)
//	0xFD <id>:    Extended Layer 2 rule (id >= 16)
//	0xFE <id>:    Extended Layer 3 rule (id >= 12)
//	0xFF <raw>:   Escaped raw terminal byte (raw >= 0xD0)
func GGTCEncode(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	g, tokens := InduceGrammar(src)

	// 1. Pack framing header:
	// Magic(2B) | Version(1B) | Flags(1B) | OrigSize(4B) | AxiomLen(4B) | NumL1(1B) | NumL2(1B) | NumL3(1B) | Pad(1B)
	rulesHeaderSize := 16 + len(g.L1)*2 + len(g.L2)*2 + len(g.L3)*2
	out := make([]byte, 0, rulesHeaderSize+len(tokens))

	out = append(out, ggtcMagic0, ggtcMagic1, ggtcVer1, 0x00)
	var szBuf [8]byte
	binary.LittleEndian.PutUint32(szBuf[0:4], uint32(n))
	binary.LittleEndian.PutUint32(szBuf[4:8], uint32(len(tokens)))
	out = append(out, szBuf[:]...)

	out = append(out, uint8(len(g.L1)), uint8(len(g.L2)), uint8(len(g.L3)), 0x00)

	// 2. Rules tables
	for _, r := range g.L1 {
		out = append(out, r.U, r.V)
	}
	for _, r := range g.L2 {
		out = append(out, r.L1A, r.L1B)
	}
	for _, r := range g.L3 {
		out = append(out, r.L2A, r.L2B)
	}

	// 3. Packed axiom token stream
	for _, tok := range tokens {
		if tok < 256 {
			// Terminal
			b := byte(tok)
			if b < 0xD0 {
				out = append(out, b)
			} else {
				out = append(out, 0xFF, b)
			}
		} else if tok < 512 {
			// Layer 1 rule
			id := uint8(tok - 256)
			if id < 16 {
				out = append(out, 0xD0+id)
			} else {
				out = append(out, 0xFC, id)
			}
		} else if tok < 1024 {
			// Layer 2 rule
			id := uint8(tok - 512)
			if id < 16 {
				out = append(out, 0xE0+id)
			} else {
				out = append(out, 0xFD, id)
			}
		} else {
			// Layer 3 rule
			id := uint8(tok - 1024)
			if id < 12 {
				out = append(out, 0xF0+id)
			} else {
				out = append(out, 0xFE, id)
			}
		}
	}

	return out, nil
}

// GGTCDecode performs Zero-Stack SIMD Wave Expansion of a GGTC stream back to original bytes.
// Guarantees O(N) execution with direct 64-bit and 32-bit vector stores and zero recursion.
func GGTCDecode(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < 16 {
		return nil, ErrGGTCCorrupted
	}

	if src[0] != ggtcMagic0 || src[1] != ggtcMagic1 || src[2] != ggtcVer1 {
		return nil, fmt.Errorf("%w: invalid GGTC signature", ErrGGTCCorrupted)
	}

	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	axiomLen := int(binary.LittleEndian.Uint32(src[8:12]))

	numL1 := int(src[12])
	numL2 := int(src[13])
	numL3 := int(src[14])

	ptr := 16
	rulesSize := numL1*2 + numL2*2 + numL3*2
	if ptr+rulesSize > len(src) {
		return nil, ErrGGTCCorrupted
	}

	var g GrammarH_DAG
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

	// Precalculate 64-bit flat wave expansion tables
	g.BuildExpansionTables()

	// Allocate output with 16 bytes safety padding for 64-bit vector stores
	dst := make([]byte, origSize+16)
	dstPtr := unsafe.Pointer(&dst[0])
	dstPos := 0
	tokensRead := 0

	axiomStream := src[ptr:]
	axPtr := 0

	// ==========================================
	// Zero-Stack Flat SIMD Wave Expansion Loop
	// ==========================================
	pDst := dstPtr
	axEnd := len(axiomStream)

	for axPtr < axEnd {
		op := axiomStream[axPtr]
		axPtr++

		if op < 0xD0 {
			*(*byte)(pDst) = op
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op >= 0xF0 && op <= 0xFB {
			id := int(op - 0xF0)
			if id >= len(g.L3) {
				return nil, ErrGGTCCorrupted
			}
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		if op >= 0xE0 && op <= 0xEF {
			id := int(op - 0xE0)
			if id >= len(g.L2) {
				return nil, ErrGGTCCorrupted
			}
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op >= 0xD0 && op <= 0xDF {
			id := int(op - 0xD0)
			if id >= len(g.L1) {
				return nil, ErrGGTCCorrupted
			}
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		if op == 0xFF {
			if axPtr >= axEnd {
				return nil, ErrGGTCCorrupted
			}
			raw := axiomStream[axPtr]
			axPtr++
			*(*byte)(pDst) = raw
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op == 0xFE {
			if axPtr >= axEnd {
				return nil, ErrGGTCCorrupted
			}
			id := int(axiomStream[axPtr])
			axPtr++
			if id >= len(g.L3) {
				return nil, ErrGGTCCorrupted
			}
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		if op == 0xFD {
			if axPtr >= axEnd {
				return nil, ErrGGTCCorrupted
			}
			id := int(axiomStream[axPtr])
			axPtr++
			if id >= len(g.L2) {
				return nil, ErrGGTCCorrupted
			}
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op == 0xFC {
			if axPtr >= axEnd {
				return nil, ErrGGTCCorrupted
			}
			id := int(axiomStream[axPtr])
			axPtr++
			if id >= len(g.L1) {
				return nil, ErrGGTCCorrupted
			}
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}
	}

	dstPos = int(uintptr(pDst) - uintptr(dstPtr))

	if dstPos != origSize {
		return nil, fmt.Errorf("%w: decoded %d bytes, want %d (tokens=%d/%d)",
			ErrGGTCSizeMismatch, dstPos, origSize, tokensRead, axiomLen)
	}

	return dst[:origSize], nil
}
