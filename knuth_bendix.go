// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

// KBScratchpad holds pre-allocated tables for critical pair resolution,
// canonical folding, and 5-layer Maneth utility pruning. Zero heap allocations.
type KBScratchpad struct {
	canonicalL2 [maxL2Rules]uint32
	remapL2     [maxL2Rules]int
	canonicalL3 [maxL3Rules]uint64
	remapL3     [maxL3Rules]int
	canonicalL4 [maxL4Rules][16]byte
	remapL4     [maxL4Rules]int
	canonicalL5 [maxL5Rules][32]byte
	remapL5     [maxL5Rules]int
}

// KBConfluentSubstituteL1 performs confluent string term rewriting for Layer 1 rules (2B).
func KBConfluentSubstituteL1(
	tokens []uint16,
	outTokens []uint16,
	pairToL1 []uint8,
	freqL1 []uint16,
) []uint16 {
	tLen := len(tokens)
	outTokens = outTokens[:0]

	for i := 0; i < tLen; {
		if i < tLen-1 && tokens[i] < 256 && tokens[i+1] < 256 {
			pk1 := (uint32(tokens[i]) << 8) | uint32(tokens[i+1])
			if id1 := pairToL1[pk1]; id1 > 0 {
				// Check for overlapping redex (critical pair) with (tokens[i+1], tokens[i+2])
				if i < tLen-2 && tokens[i+2] < 256 {
					pk2 := (uint32(tokens[i+1]) << 8) | uint32(tokens[i+2])
					if id2 := pairToL1[pk2]; id2 > 0 {
						// Overlapping critical pair: compare stratified termination ordering
						cost1 := uint32(0)
						if freqL1 != nil {
							cost1 = uint32(freqL1[pk1])
						}
						if len(outTokens) > 0 && outTokens[len(outTokens)-1] >= 256 {
							cost1 += 100000
						}
						if i < tLen-3 && tokens[i+3] < 256 {
							pk3 := (uint32(tokens[i+2]) << 8) | uint32(tokens[i+3])
							if pairToL1[pk3] > 0 {
								if freqL1 != nil {
									cost1 += uint32(freqL1[pk3])
								}
								cost1 += 10000
							}
						}
						cost2 := uint32(0)
						if freqL1 != nil {
							cost2 = uint32(freqL1[pk2])
						}
						if i < tLen-4 && tokens[i+3] < 256 && tokens[i+4] < 256 {
							pk4 := (uint32(tokens[i+3]) << 8) | uint32(tokens[i+4])
							if pairToL1[pk4] > 0 {
								cost2 += 10000
							}
						}
						if cost2 > cost1 {
							outTokens = append(outTokens, tokens[i], 256+uint16(id2-1))
							i += 3
							continue
						}
					}
				}
				outTokens = append(outTokens, 256+uint16(id1-1))
				i += 2
				continue
			}
		}
		outTokens = append(outTokens, tokens[i])
		i++
	}
	return outTokens
}

// KBConfluentSubstituteL2 performs confluent term rewriting for Layer 2 rules (4B).
func KBConfluentSubstituteL2(
	tokens []uint16,
	outTokens []uint16,
	pairToL2 []uint8,
	freqL2 []uint16,
	l1Count uint16,
) []uint16 {
	tLen := len(tokens)
	outTokens = outTokens[:0]

	for i := 0; i < tLen; {
		if i < tLen-1 && tokens[i] >= 256 && tokens[i] < 256+l1Count &&
			tokens[i+1] >= 256 && tokens[i+1] < 256+l1Count {
			pk1 := (uint32(tokens[i]-256) << 6) | uint32(tokens[i+1]-256)
			if id1 := pairToL2[pk1]; id1 > 0 {
				if i < tLen-2 && tokens[i+2] >= 256 && tokens[i+2] < 256+l1Count {
					pk2 := (uint32(tokens[i+1]-256) << 6) | uint32(tokens[i+2]-256)
					if id2 := pairToL2[pk2]; id2 > 0 {
						cost1 := uint32(0)
						if freqL2 != nil {
							cost1 = uint32(freqL2[pk1])
						}
						if len(outTokens) > 0 && outTokens[len(outTokens)-1] >= 512 {
							cost1 += 100000
						}
						if i < tLen-3 && tokens[i+3] >= 256 && tokens[i+3] < 256+l1Count {
							pk3 := (uint32(tokens[i+2]-256) << 6) | uint32(tokens[i+3]-256)
							if pairToL2[pk3] > 0 {
								if freqL2 != nil {
									cost1 += uint32(freqL2[pk3])
								}
								cost1 += 10000
							}
						}
						cost2 := uint32(0)
						if freqL2 != nil {
							cost2 = uint32(freqL2[pk2])
						}
						if i < tLen-4 && tokens[i+3] >= 256 && tokens[i+3] < 256+l1Count &&
							tokens[i+4] >= 256 && tokens[i+4] < 256+l1Count {
							pk4 := (uint32(tokens[i+3]-256) << 6) | uint32(tokens[i+4]-256)
							if pairToL2[pk4] > 0 {
								cost2 += 10000
							}
						}
						if cost2 > cost1 {
							outTokens = append(outTokens, tokens[i], 512+uint16(id2-1))
							i += 3
							continue
						}
					}
				}
				outTokens = append(outTokens, 512+uint16(id1-1))
				i += 2
				continue
			}
		}
		outTokens = append(outTokens, tokens[i])
		i++
	}
	return outTokens
}

// KBConfluentSubstituteL3 performs confluent term rewriting for Layer 3 rules (8B).
func KBConfluentSubstituteL3(
	tokens []uint16,
	outTokens []uint16,
	pairToL3 []uint8,
	freqL3 []uint16,
	l2Count uint16,
) []uint16 {
	tLen := len(tokens)
	outTokens = outTokens[:0]

	for i := 0; i < tLen; {
		if i < tLen-1 && tokens[i] >= 512 && tokens[i] < 512+l2Count &&
			tokens[i+1] >= 512 && tokens[i+1] < 512+l2Count {
			pk1 := (uint32(tokens[i]-512) << 5) | uint32(tokens[i+1]-512)
			if id1 := pairToL3[pk1]; id1 > 0 {
				if i < tLen-2 && tokens[i+2] >= 512 && tokens[i+2] < 512+l2Count {
					pk2 := (uint32(tokens[i+1]-512) << 5) | uint32(tokens[i+2]-512)
					if id2 := pairToL3[pk2]; id2 > 0 {
						cost1 := uint32(0)
						if freqL3 != nil {
							cost1 = uint32(freqL3[pk1])
						}
						if len(outTokens) > 0 && outTokens[len(outTokens)-1] >= 1024 {
							cost1 += 100000
						}
						if i < tLen-3 && tokens[i+3] >= 512 && tokens[i+3] < 512+l2Count {
							pk3 := (uint32(tokens[i+2]-512) << 5) | uint32(tokens[i+3]-512)
							if pairToL3[pk3] > 0 {
								if freqL3 != nil {
									cost1 += uint32(freqL3[pk3])
								}
								cost1 += 10000
							}
						}
						cost2 := uint32(0)
						if freqL3 != nil {
							cost2 = uint32(freqL3[pk2])
						}
						if i < tLen-4 && tokens[i+3] >= 512 && tokens[i+3] < 512+l2Count &&
							tokens[i+4] >= 512 && tokens[i+4] < 512+l2Count {
							pk4 := (uint32(tokens[i+3]-512) << 5) | uint32(tokens[i+4]-512)
							if pairToL3[pk4] > 0 {
								cost2 += 10000
							}
						}
						if cost2 > cost1 {
							outTokens = append(outTokens, tokens[i], 1024+uint16(id2-1))
							i += 3
							continue
						}
					}
				}
				outTokens = append(outTokens, 1024+uint16(id1-1))
				i += 2
				continue
			}
		}
		outTokens = append(outTokens, tokens[i])
		i++
	}
	return outTokens
}

// KBConfluentSubstituteL4 performs confluent term rewriting for Layer 4 rules (16B).
func KBConfluentSubstituteL4(
	tokens []uint16,
	outTokens []uint16,
	pairTo256 []uint8,
	freq256 []uint16,
	l3Count uint16,
) []uint16 {
	tLen := len(tokens)
	outTokens = outTokens[:0]

	for i := 0; i < tLen; {
		if i < tLen-1 && tokens[i] >= 1024 && tokens[i] < 1024+l3Count &&
			tokens[i+1] >= 1024 && tokens[i+1] < 1024+l3Count {
			pk1 := (uint32(tokens[i]-1024) << 4) | uint32(tokens[i+1]-1024)
			if id1 := pairTo256[pk1]; id1 > 0 {
				if i < tLen-2 && tokens[i+2] >= 1024 && tokens[i+2] < 1024+l3Count {
					pk2 := (uint32(tokens[i+1]-1024) << 4) | uint32(tokens[i+2]-1024)
					if id2 := pairTo256[pk2]; id2 > 0 {
						cost1 := uint32(0)
						if freq256 != nil {
							cost1 = uint32(freq256[pk1])
						}
						if len(outTokens) > 0 && outTokens[len(outTokens)-1] >= 2048 {
							cost1 += 10000
						}
						if i < tLen-3 && tokens[i+3] >= 1024 && tokens[i+3] < 1024+l3Count {
							pk3 := (uint32(tokens[i+2]-1024) << 4) | uint32(tokens[i+3]-1024)
							if pairTo256[pk3] > 0 {
								if freq256 != nil {
									cost1 += uint32(freq256[pk3])
								}
								cost1 += 10000
							}
						}
						cost2 := uint32(0)
						if freq256 != nil {
							cost2 = uint32(freq256[pk2])
						}
						if i < tLen-4 && tokens[i+3] >= 1024 && tokens[i+3] < 1024+l3Count &&
							tokens[i+4] >= 1024 && tokens[i+4] < 1024+l3Count {
							pk4 := (uint32(tokens[i+3]-1024) << 4) | uint32(tokens[i+4]-1024)
							if pairTo256[pk4] > 0 {
								cost2 += 10000
							}
						}
						if cost2 > cost1 {
							outTokens = append(outTokens, tokens[i], 2048+uint16(id2-1))
							i += 3
							continue
						}
					}
				}
				outTokens = append(outTokens, 2048+uint16(id1-1))
				i += 2
				continue
			}
		}
		outTokens = append(outTokens, tokens[i])
		i++
	}
	return outTokens
}

// KBConfluentSubstituteL5 performs confluent term rewriting for Layer 5 rules (32B).
func KBConfluentSubstituteL5(
	tokens []uint16,
	outTokens []uint16,
	pairTo256 []uint8,
	freq256 []uint16,
	l4Count uint16,
) []uint16 {
	tLen := len(tokens)
	outTokens = outTokens[:0]

	for i := 0; i < tLen; {
		if i < tLen-1 && tokens[i] >= 2048 && tokens[i] < 2048+l4Count &&
			tokens[i+1] >= 2048 && tokens[i+1] < 2048+l4Count {
			pk1 := (uint32(tokens[i]-2048) << 4) | uint32(tokens[i+1]-2048)
			if id1 := pairTo256[pk1]; id1 > 0 {
				if i < tLen-2 && tokens[i+2] >= 2048 && tokens[i+2] < 2048+l4Count {
					pk2 := (uint32(tokens[i+1]-2048) << 4) | uint32(tokens[i+2]-2048)
					if id2 := pairTo256[pk2]; id2 > 0 {
						cost1 := uint32(0)
						if freq256 != nil {
							cost1 = uint32(freq256[pk1])
						}
						if len(outTokens) > 0 && outTokens[len(outTokens)-1] >= 4096 {
							cost1 += 10000
						}
						if i < tLen-3 && tokens[i+3] >= 2048 && tokens[i+3] < 2048+l4Count {
							pk3 := (uint32(tokens[i+2]-2048) << 4) | uint32(tokens[i+3]-2048)
							if pairTo256[pk3] > 0 {
								if freq256 != nil {
									cost1 += uint32(freq256[pk3])
								}
								cost1 += 10000
							}
						}
						cost2 := uint32(0)
						if freq256 != nil {
							cost2 = uint32(freq256[pk2])
						}
						if i < tLen-4 && tokens[i+3] >= 2048 && tokens[i+3] < 2048+l4Count &&
							tokens[i+4] >= 2048 && tokens[i+4] < 2048+l4Count {
							pk4 := (uint32(tokens[i+3]-2048) << 4) | uint32(tokens[i+4]-2048)
							if pairTo256[pk4] > 0 {
								cost2 += 10000
							}
						}
						if cost2 > cost1 {
							outTokens = append(outTokens, tokens[i], 4096+uint16(id2-1))
							i += 3
							continue
						}
					}
				}
				outTokens = append(outTokens, 4096+uint16(id1-1))
				i += 2
				continue
			}
		}
		outTokens = append(outTokens, tokens[i])
		i++
	}
	return outTokens
}

// FoldCanonicalGrammar5 detects and collapses duplicate or equivalent non-terminals across all 5 layers.
// Frees up rule slots in strict layer capacities (M1<=64, M2<=32, M3<=16, M4<=16, M5<=8).
func FoldCanonicalGrammar5(g *GrammarH_DAG5, tokens []uint16, kb *KBScratchpad) (*GrammarH_DAG5, []uint16) {
	g.BuildExpansionTables()

	// 1. Fold Layer 2 (4B)
	if len(g.L2) > 1 {
		newL2 := make([]RuleL2, 0, len(g.L2))
		for i, r := range g.L2 {
			val := g.ExpL2[i]
			dup := -1
			for j := 0; j < len(newL2); j++ {
				if kb.canonicalL2[j] == val {
					dup = j
					break
				}
			}
			if dup >= 0 {
				kb.remapL2[i] = dup
			} else {
				kb.remapL2[i] = len(newL2)
				kb.canonicalL2[len(newL2)] = val
				newL2 = append(newL2, r)
			}
		}
		if len(newL2) < len(g.L2) {
			g.L2 = newL2
			for k := range g.L3 {
				g.L3[k].L2A = uint8(kb.remapL2[g.L3[k].L2A])
				g.L3[k].L2B = uint8(kb.remapL2[g.L3[k].L2B])
			}
			for idx, tok := range tokens {
				if tok >= 512 && tok < 1024 {
					oldID := int(tok - 512)
					if oldID < len(kb.remapL2) {
						tokens[idx] = 512 + uint16(kb.remapL2[oldID])
					}
				}
			}
			g.BuildExpansionTables()
		}
	}

	// 2. Fold Layer 3 (8B)
	if len(g.L3) > 1 {
		newL3 := make([]RuleL3, 0, len(g.L3))
		for i, r := range g.L3 {
			val := g.ExpL3[i]
			dup := -1
			for j := 0; j < len(newL3); j++ {
				if kb.canonicalL3[j] == val {
					dup = j
					break
				}
			}
			if dup >= 0 {
				kb.remapL3[i] = dup
			} else {
				kb.remapL3[i] = len(newL3)
				kb.canonicalL3[len(newL3)] = val
				newL3 = append(newL3, r)
			}
		}
		if len(newL3) < len(g.L3) {
			g.L3 = newL3
			for m := range g.L4 {
				g.L4[m].L3A = uint8(kb.remapL3[g.L4[m].L3A])
				g.L4[m].L3B = uint8(kb.remapL3[g.L4[m].L3B])
			}
			for idx, tok := range tokens {
				if tok >= 1024 && tok < 2048 {
					oldID := int(tok - 1024)
					if oldID < len(kb.remapL3) {
						tokens[idx] = 1024 + uint16(kb.remapL3[oldID])
					}
				}
			}
			g.BuildExpansionTables()
		}
	}

	// 3. Fold Layer 4 (16B)
	if len(g.L4) > 1 {
		newL4 := make([]RuleL4, 0, len(g.L4))
		for i, r := range g.L4 {
			val := g.ExpL4[i]
			dup := -1
			for j := 0; j < len(newL4); j++ {
				if kb.canonicalL4[j] == val {
					dup = j
					break
				}
			}
			if dup >= 0 {
				kb.remapL4[i] = dup
			} else {
				kb.remapL4[i] = len(newL4)
				kb.canonicalL4[len(newL4)] = val
				newL4 = append(newL4, r)
			}
		}
		if len(newL4) < len(g.L4) {
			g.L4 = newL4
			for n := range g.L5 {
				g.L5[n].L4A = uint8(kb.remapL4[g.L5[n].L4A])
				g.L5[n].L4B = uint8(kb.remapL4[g.L5[n].L4B])
			}
			for idx, tok := range tokens {
				if tok >= 2048 && tok < 4096 {
					oldID := int(tok - 2048)
					if oldID < len(kb.remapL4) {
						tokens[idx] = 2048 + uint16(kb.remapL4[oldID])
					}
				}
			}
			g.BuildExpansionTables()
		}
	}

	// 4. Fold Layer 5 (32B)
	if len(g.L5) > 1 {
		newL5 := make([]RuleL5, 0, len(g.L5))
		for i, r := range g.L5 {
			val := g.ExpL5[i]
			dup := -1
			for j := 0; j < len(newL5); j++ {
				if kb.canonicalL5[j] == val {
					dup = j
					break
				}
			}
			if dup >= 0 {
				kb.remapL5[i] = dup
			} else {
				kb.remapL5[i] = len(newL5)
				kb.canonicalL5[len(newL5)] = val
				newL5 = append(newL5, r)
			}
		}
		if len(newL5) < len(g.L5) {
			g.L5 = newL5
			for idx, tok := range tokens {
				if tok >= 4096 {
					oldID := int(tok - 4096)
					if oldID < len(kb.remapL5) {
						tokens[idx] = 4096 + uint16(kb.remapL5[oldID])
					}
				}
			}
			g.BuildExpansionTables()
		}
	}

	return g, tokens
}

type candL5 struct {
	oldID int
	rule  RuleL5
	count int
}

type candL4 struct {
	oldID  int
	rule   RuleL4
	count  int
	parent int
}

func sortCandsL5(a []candL5) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j].count > a[j-1].count; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func sortCandsL4(a []candL4) {
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

// PruneGrammar5 applies Sebastian Maneth's bottom-up grammar contribution metric
// across all 5 layers simultaneously (L1..L5), pruning non-contributing intermediate rules
// and sorting surviving rules by descending frequency for maximal 1-byte opcode density.
func PruneGrammar5(g *GrammarH_DAG5, tokens []uint16, sp *grammarScratchpad) (*GrammarH_DAG5, []uint16) {
	// ==========================================
	// Phase 5: Prune & Sort Layer 5 (32-byte rules)
	// ==========================================
	if len(g.L5) > 0 {
		var occL5 [maxL5Rules]int
		for _, tok := range tokens {
			if tok >= 4096 && int(tok-4096) < len(g.L5) {
				occL5[tok-4096]++
			}
		}

		var keptL5 [maxL5Rules]candL5
		keptL5Count := 0
		for id, r := range g.L5 {
			if occL5[id] >= 1 { // Profit threshold for 32B rule
				keptL5[keptL5Count] = candL5{oldID: id, rule: r, count: occL5[id]}
				keptL5Count++
			}
		}

		sortCandsL5(keptL5[:keptL5Count])

		var oldToNewL5 [maxL5Rules]int
		for i := range oldToNewL5 {
			oldToNewL5[i] = -1
		}
		newL5 := make([]RuleL5, keptL5Count)
		for newID := 0; newID < keptL5Count; newID++ {
			c := keptL5[newID]
			oldToNewL5[c.oldID] = newID
			newL5[newID] = c.rule
		}

		if keptL5Count < len(g.L5) {
			outTokens := sp.otherBuffer(tokens)
			for _, tok := range tokens {
				if tok >= 4096 && int(tok-4096) < len(g.L5) {
					oldID := int(tok - 4096)
					newID := oldToNewL5[oldID]
					if newID >= 0 {
						outTokens = append(outTokens, 4096+uint16(newID))
					} else {
						// Unpack eliminated L5 rule into child L4 tokens
						r := g.L5[oldID]
						outTokens = append(outTokens, 2048+uint16(r.L4A), 2048+uint16(r.L4B))
					}
				} else {
					outTokens = append(outTokens, tok)
				}
			}
			tokens = outTokens
		} else {
			for i, tok := range tokens {
				if tok >= 4096 && int(tok-4096) < len(g.L5) {
					tokens[i] = 4096 + uint16(oldToNewL5[tok-4096])
				}
			}
		}
		g.L5 = newL5
	}

	// ==========================================
	// Phase 4: Prune & Sort Layer 4 (16-byte rules)
	// ==========================================
	if len(g.L4) > 0 {
		var occL4 [maxL4Rules]int
		var refL4 [maxL4Rules]int
		for _, tok := range tokens {
			if tok >= 2048 && tok < 4096 && int(tok-2048) < len(g.L4) {
				occL4[tok-2048]++
			}
		}
		for _, r := range g.L5 {
			refL4[r.L4A]++
			refL4[r.L4B]++
		}

		var keptL4 [maxL4Rules]candL4
		keptL4Count := 0
		for id, r := range g.L4 {
			totalRefs := occL4[id] + refL4[id]
			if totalRefs >= 1 {
				keptL4[keptL4Count] = candL4{oldID: id, rule: r, count: occL4[id], parent: refL4[id]}
				keptL4Count++
			}
		}

		sortCandsL4(keptL4[:keptL4Count])

		var oldToNewL4 [maxL4Rules]int
		for i := range oldToNewL4 {
			oldToNewL4[i] = -1
		}
		newL4 := make([]RuleL4, keptL4Count)
		for newID := 0; newID < keptL4Count; newID++ {
			c := keptL4[newID]
			oldToNewL4[c.oldID] = newID
			newL4[newID] = c.rule
		}

		// Update L5 references
		for k := range g.L5 {
			g.L5[k].L4A = uint8(oldToNewL4[g.L5[k].L4A])
			g.L5[k].L4B = uint8(oldToNewL4[g.L5[k].L4B])
		}

		if keptL4Count < len(g.L4) {
			outTokens := sp.otherBuffer(tokens)
			for _, tok := range tokens {
				if tok >= 2048 && tok < 4096 && int(tok-2048) < len(g.L4) {
					oldID := int(tok - 2048)
					newID := oldToNewL4[oldID]
					if newID >= 0 {
						outTokens = append(outTokens, 2048+uint16(newID))
					} else {
						// Unpack eliminated L4 rule into child L3 tokens
						r := g.L4[oldID]
						outTokens = append(outTokens, 1024+uint16(r.L3A), 1024+uint16(r.L3B))
					}
				} else {
					outTokens = append(outTokens, tok)
				}
			}
			tokens = outTokens
		} else {
			for i, tok := range tokens {
				if tok >= 2048 && tok < 4096 && int(tok-2048) < len(g.L4) {
					tokens[i] = 2048 + uint16(oldToNewL4[tok-2048])
				}
			}
		}
		g.L4 = newL4
	}

	// ==========================================
	// Phase 3: Prune & Sort Layer 3 (8-byte rules)
	// ==========================================
	if len(g.L3) > 0 {
		var occL3 [maxL3Rules]int
		var refL3 [maxL3Rules]int
		for _, tok := range tokens {
			if tok >= 1024 && tok < 2048 && int(tok-1024) < len(g.L3) {
				occL3[tok-1024]++
			}
		}
		for _, r := range g.L4 {
			refL3[r.L3A]++
			refL3[r.L3B]++
		}

		var keptL3 [maxL3Rules]candL3
		keptL3Count := 0
		for id, r := range g.L3 {
			totalRefs := occL3[id] + refL3[id]
			if totalRefs >= 2 || refL3[id] >= 1 {
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

		// Update L4 references
		for k := range g.L4 {
			g.L4[k].L3A = uint8(oldToNewL3[g.L4[k].L3A])
			g.L4[k].L3B = uint8(oldToNewL3[g.L4[k].L3B])
		}

		if keptL3Count < len(g.L3) {
			outTokens := sp.otherBuffer(tokens)
			for _, tok := range tokens {
				if tok >= 1024 && tok < 2048 && int(tok-1024) < len(g.L3) {
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
		} else {
			for i, tok := range tokens {
				if tok >= 1024 && tok < 2048 && int(tok-1024) < len(g.L3) {
					tokens[i] = 1024 + uint16(oldToNewL3[tok-1024])
				}
			}
		}
		g.L3 = newL3
	}

	// ==========================================
	// Phase 2: Prune & Sort Layer 2 (4-byte rules)
	// ==========================================
	if len(g.L2) > 0 {
		var occL2 [maxL2Rules]int
		var refL2 [maxL2Rules]int
		for _, tok := range tokens {
			if tok >= 512 && tok < 1024 && int(tok-512) < len(g.L2) {
				occL2[tok-512]++
			}
		}
		for _, r := range g.L3 {
			refL2[r.L2A]++
			refL2[r.L2B]++
		}

		var keptL2 [maxL2Rules]candL2
		keptL2Count := 0
		for id, r := range g.L2 {
			totalRefs := occL2[id] + refL2[id]
			if totalRefs >= 2 || refL2[id] >= 1 {
				keptL2[keptL2Count] = candL2{oldID: id, rule: r, count: occL2[id], parent: refL2[id]}
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

		// Update L3 references
		for k := range g.L3 {
			g.L3[k].L2A = uint8(oldToNewL2[g.L3[k].L2A])
			g.L3[k].L2B = uint8(oldToNewL2[g.L3[k].L2B])
		}

		if keptL2Count < len(g.L2) {
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
		} else {
			for i, tok := range tokens {
				if tok >= 512 && tok < 1024 && int(tok-512) < len(g.L2) {
					tokens[i] = 512 + uint16(oldToNewL2[tok-512])
				}
			}
		}
		g.L2 = newL2
	}

	// ==========================================
	// Phase 1: Prune & Sort Layer 1 (2-byte rules)
	// ==========================================
	if len(g.L1) > 0 {
		var occL1 [maxL1Rules]int
		var refL1 [maxL1Rules]int
		for _, tok := range tokens {
			if tok >= 256 && tok < 512 && int(tok-256) < len(g.L1) {
				occL1[tok-256]++
			}
		}
		for _, r := range g.L2 {
			refL1[r.L1A]++
			refL1[r.L1B]++
		}

		var keptL1 [maxL1Rules]candL1
		keptL1Count := 0
		for id, r := range g.L1 {
			totalRefs := occL1[id] + refL1[id]
			if totalRefs >= 2 || refL1[id] >= 1 {
				keptL1[keptL1Count] = candL1{oldID: id, rule: r, count: occL1[id], parent: refL1[id]}
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

		// Update L2 references
		for k := range g.L2 {
			g.L2[k].L1A = uint8(oldToNewL1[g.L2[k].L1A])
			g.L2[k].L1B = uint8(oldToNewL1[g.L2[k].L1B])
		}

		if keptL1Count < len(g.L1) {
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
		} else {
			for i, tok := range tokens {
				if tok >= 256 && tok < 512 && int(tok-256) < len(g.L1) {
					tokens[i] = 256 + uint16(oldToNewL1[tok-256])
				}
			}
		}
		g.L1 = newL1
	}

	g.BuildExpansionTables()
	return g, tokens
}
