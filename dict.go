// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
)

type dictEntry struct {
	pattern []byte
	tokIdx  uint16
	transOp uint8
	isRFC   bool
	length  uint8
	cost    uint8
}

type dictMatcher struct {
	id      uint32
	tokens  [][]byte
	entries []dictEntry
	head    [65536]int32
	chain   []int32
	head3   [65536]int32
	chain3  []int32
	head2   [65536]int16
}

func initMatcher(m *dictMatcher, id uint32, rawTokens []string) {
	m.id = id
	m.tokens = make([][]byte, len(rawTokens))
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.head3 {
		m.head3[i] = -1
	}
	for i := range m.head2 {
		m.head2[i] = -1
	}

	for i, s := range rawTokens {
		m.tokens[i] = []byte(s)
	}

	addEntry := func(pattern []byte, tokIdx uint16, transOp, cost uint8, isRFC bool) {
		if len(pattern) < 2 || len(pattern) > 255 {
			return
		}
		eIdx := int32(len(m.entries))
		m.entries = append(m.entries, dictEntry{
			pattern: pattern,
			tokIdx:  tokIdx,
			transOp: transOp,
			isRFC:   isRFC,
			length:  uint8(len(pattern)),
			cost:    cost,
		})
		m.chain = append(m.chain, -1)
		m.chain3 = append(m.chain3, -1)

		pLen := len(pattern)
		if pLen >= 4 {
			h := (binary.LittleEndian.Uint32(pattern[:4]) * 0x1E35A7BD) >> 16
			m.chain[eIdx] = m.head[h]
			m.head[h] = eIdx
		} else if pLen == 3 {
			u24 := uint32(pattern[0]) | (uint32(pattern[1]) << 8) | (uint32(pattern[2]) << 16)
			h3 := (u24 * 0x1E35A7BD) >> 16
			m.chain3[eIdx] = m.head3[h3]
			m.head3[h3] = eIdx
		} else if pLen == 2 {
			u16 := uint16(pattern[0]) | (uint16(pattern[1]) << 8)
			if m.head2[u16] == -1 || cost < m.entries[m.head2[u16]].cost {
				m.head2[u16] = int16(eIdx)
			}
		}
	}

	// 1. Base tokens
	for i, b := range m.tokens {
		cost := uint8(2)
		if i < 8 {
			cost = 1
		}
		addEntry(b, uint16(i), 0, cost, false)
	}

	// 2. Static Word Transforms (only for tokens where len >= 2 and <= 32)
	for i, b := range m.tokens {
		if len(b) < 2 || len(b) > 32 {
			continue
		}
		tokIdx := uint16(i)

		// 0xEA: Prefix space: " " + tok
		sp := make([]byte, len(b)+1)
		sp[0] = ' '
		copy(sp[1:], b)
		addEntry(sp, tokIdx, OpcodePrefixTransSpace, 2, false)

		// 0xEB: Suffix quote: tok + "\""
		sq := make([]byte, len(b)+1)
		copy(sq, b)
		sq[len(b)] = '"'
		addEntry(sq, tokIdx, OpcodePrefixTransQuote, 2, false)

		// 0xEC: Suffix brace: tok + "{"
		sb := make([]byte, len(b)+1)
		copy(sb, b)
		sb[len(b)] = '{'
		addEntry(sb, tokIdx, OpcodePrefixTransBrace, 2, false)

		// 0xED: Uppercase first letter: upper(b[0]) + b[1:]
		if b[0] >= 'a' && b[0] <= 'z' {
			uf := make([]byte, len(b))
			copy(uf, b)
			uf[0] -= ('a' - 'A')
			addEntry(uf, tokIdx, OpcodePrefixTransUpper, 2, false)
		}

		// 0xEE: Suffix semicolon: tok + ";"
		ss := make([]byte, len(b)+1)
		copy(ss, b)
		ss[len(b)] = ';'
		addEntry(ss, tokIdx, OpcodePrefixTransSemi, 2, false)

		// 0xEF: Space around: " " + tok + " "
		sa := make([]byte, len(b)+2)
		sa[0] = ' '
		copy(sa[1:], b)
		sa[len(b)+1] = ' '
		addEntry(sa, tokIdx, OpcodePrefixTransSpaceSpace, 2, false)

		// 0xFB: Suffix equal quote: tok + "=\""
		seq := make([]byte, len(b)+2)
		copy(seq, b)
		seq[len(b)] = '='
		seq[len(b)+1] = '"'
		addEntry(seq, tokIdx, OpcodePrefixTransEqQuote, 2, false)

		// 0xFC: Suffix tag close: tok + ">"
		stc := make([]byte, len(b)+1)
		copy(stc, b)
		stc[len(b)] = '>'
		addEntry(stc, tokIdx, OpcodePrefixTransTagClose, 2, false)
	}

	// 3. RFC 7932 121-Word Transformation Matrix
	if id == DictIDBrotliWeb {
		type rfcPatternCandidate struct {
			pattern []byte
			tokIdx  uint16
			transOp uint8
			cost    uint8
		}
		seen := make(map[string]rfcPatternCandidate)

		var allWords [][]byte
		allWords = append(allWords, m.tokens...)
		allWords = append(allWords, rfcTopWordsBytes...)

		var tmpBuf [256]byte
		for wordID, w := range allWords {
			if len(w) == 0 {
				continue
			}
			for transID := 0; transID < 121; transID++ {
				outLen, err := applyRFC7932Transform(tmpBuf[:], uint8(transID), w)
				if err != nil || outLen < 4 || outLen > 128 {
					continue
				}
				cost := uint8(4)
				if wordID < 128 {
					cost = 3
				}
				if int(outLen)-int(cost) < 1 {
					continue
				}
				patStr := string(tmpBuf[:outLen])
				cand := rfcPatternCandidate{
					pattern: append([]byte(nil), tmpBuf[:outLen]...),
					tokIdx:  uint16(wordID),
					transOp: uint8(transID),
					cost:    cost,
				}
				if existing, found := seen[patStr]; !found {
					seen[patStr] = cand
				} else {
					if cand.cost < existing.cost || (cand.cost == existing.cost && cand.tokIdx < existing.tokIdx) {
						seen[patStr] = cand
					}
				}
			}
		}

		for _, cand := range seen {
			addEntry(cand.pattern, cand.tokIdx, cand.transOp, cand.cost, true)
		}
	}
}

func (m *dictMatcher) match(
	src []byte,
	pos int,
) (bestIdx int, bestTransOp uint8, bestLen, bestCost, bestScore, pairTok2 int, isRFC bool) {
	uSize := len(src)
	bestIdx = -1
	pairTok2 = -1
	bestScore = 0

	// 1. Length >= 4 matches via head/chain
	if pos+4 <= uSize {
		u32 := binary.LittleEndian.Uint32(src[pos : pos+4])
		h := (u32 * 0x1E35A7BD) >> 16
		for idx := m.head[h]; idx >= 0; idx = m.chain[idx] {
			e := &m.entries[idx]
			pLen := int(e.length)
			score := pLen - int(e.cost)
			if score > bestScore && pos+pLen <= uSize {
				if bytes.Equal(src[pos:pos+pLen], e.pattern) {
					bestIdx = int(e.tokIdx)
					bestTransOp = e.transOp
					bestLen = pLen
					bestCost = int(e.cost)
					bestScore = score
					isRFC = e.isRFC
				}
			}
		}
	}

	// 2. Length 3 matches via head3/chain3
	if pos+3 <= uSize && bestScore < 3 {
		u24 := uint32(src[pos]) | (uint32(src[pos+1]) << 8) | (uint32(src[pos+2]) << 16)
		h3 := (u24 * 0x1E35A7BD) >> 16
		for idx := m.head3[h3]; idx >= 0; idx = m.chain3[idx] {
			e := &m.entries[idx]
			pLen := int(e.length)
			score := pLen - int(e.cost)
			if score > bestScore && pos+pLen <= uSize {
				if bytes.Equal(src[pos:pos+pLen], e.pattern) {
					bestIdx = int(e.tokIdx)
					bestTransOp = e.transOp
					bestLen = pLen
					bestCost = int(e.cost)
					bestScore = score
					isRFC = e.isRFC
				}
			}
		}
	}

	// 3. Length 2 matches via head2
	if pos+2 <= uSize && bestScore < 1 {
		u16 := uint16(src[pos]) | (uint16(src[pos+1]) << 8)
		if idx := m.head2[u16]; idx >= 0 {
			e := &m.entries[idx]
			pLen := int(e.length)
			score := pLen - int(e.cost)
			if score > bestScore && pos+pLen <= uSize {
				if bytes.Equal(src[pos:pos+pLen], e.pattern) {
					bestIdx = int(e.tokIdx)
					bestTransOp = e.transOp
					bestLen = pLen
					bestCost = int(e.cost)
					bestScore = score
					isRFC = e.isRFC
				}
			}
		}
	}

	// 4. Sub-4-byte Token Pair probe (0xFF)
	if !isRFC && bestLen >= 2 && bestTransOp == 0 && pos+bestLen+2 <= uSize {
		pos2 := pos + bestLen
		tok2Idx := -1
		tok2Len := 0

		if pos2+4 <= uSize {
			u32 := binary.LittleEndian.Uint32(src[pos2 : pos2+4])
			h := (u32 * 0x1E35A7BD) >> 16
			for idx := m.head[h]; idx >= 0; idx = m.chain[idx] {
				e := &m.entries[idx]
				if !e.isRFC && e.transOp == 0 {
					pLen := int(e.length)
					if pLen > tok2Len && pos2+pLen <= uSize {
						if bytes.Equal(src[pos2:pos2+pLen], e.pattern) {
							tok2Idx = int(e.tokIdx)
							tok2Len = pLen
						}
					}
				}
			}
		}
		if tok2Len == 0 && pos2+3 <= uSize {
			u24 := uint32(src[pos2]) | (uint32(src[pos2+1]) << 8) | (uint32(src[pos2+2]) << 16)
			h3 := (u24 * 0x1E35A7BD) >> 16
			for idx := m.head3[h3]; idx >= 0; idx = m.chain3[idx] {
				e := &m.entries[idx]
				if !e.isRFC && e.transOp == 0 {
					pLen := int(e.length)
					if pLen > tok2Len && pos2+pLen <= uSize {
						if bytes.Equal(src[pos2:pos2+pLen], e.pattern) {
							tok2Idx = int(e.tokIdx)
							tok2Len = pLen
						}
					}
				}
			}
		}
		if tok2Len == 0 && pos2+2 <= uSize {
			u16 := uint16(src[pos2]) | (uint16(src[pos2+1]) << 8)
			if idx := m.head2[u16]; idx >= 0 {
				e := &m.entries[idx]
				if !e.isRFC && e.transOp == 0 {
					pLen := int(e.length)
					if pLen > tok2Len && pos2+pLen <= uSize {
						if bytes.Equal(src[pos2:pos2+pLen], e.pattern) {
							tok2Idx = int(e.tokIdx)
							tok2Len = pLen
						}
					}
				}
			}
		}

		if tok2Len >= 2 {
			pairLen := bestLen + tok2Len
			pairCost := 3 // 0xFF, byte(bestIdx), byte(tok2Idx)
			pairScore := pairLen - pairCost
			if pairScore > bestScore {
				pairTok2 = tok2Idx
				bestTransOp = OpcodePrefixTransPair
				bestLen = pairLen
				bestCost = pairCost
				bestScore = pairScore
				isRFC = false
			}
		}
	}

	return bestIdx, bestTransOp, bestLen, bestCost, bestScore, pairTok2, isRFC
}

// PRZMDMagic defines the 32-bit container magic for external dictionaries ('PRZD').
const PRZMDMagic uint32 = 0x445A5250

// Dictionary represents a compiled schema dictionary for PRZM token matching.
type Dictionary struct {
	id      uint32
	tokens  [][]byte
	matcher dictMatcher
}

// ID returns the 32-bit identifier of the dictionary.
func (d *Dictionary) ID() uint32 {
	return d.id
}

// Tokens returns the compiled byte tokens of the dictionary.
func (d *Dictionary) Tokens() [][]byte {
	return d.tokens
}

// NewDictionary creates a custom dictionary from token strings.
func NewDictionary(id uint32, rawTokens []string) (*Dictionary, error) {
	if len(rawTokens) == 0 {
		return nil, fmt.Errorf("%w: dictionary has 0 tokens", ErrInvalidDict)
	}
	if len(rawTokens) > 256 {
		return nil, fmt.Errorf("%w: dictionary exceeds maximum 256 tokens", ErrInvalidDict)
	}
	d := &Dictionary{
		id: id,
	}
	initMatcher(&d.matcher, id, rawTokens)
	d.tokens = d.matcher.tokens
	return d, nil
}

// Bytes serializes the dictionary into the binary PRZMD container format.
// Layout:
// [4B Magic: "PRZD"] [1B Version: 1] [1B Flags: 0] [2B TokenCount: N] [4B DictID] [4B Reserved: 0] (16B header)
// [Table of N uint16_le TokenLengths]
// [Contiguous Token String Payloads]
func (d *Dictionary) Bytes() []byte {
	numTokens := len(d.tokens)
	totalPayload := 0
	for _, tok := range d.tokens {
		totalPayload += len(tok)
	}
	bufSize := 16 + numTokens*2 + totalPayload
	buf := make([]byte, bufSize)

	binary.LittleEndian.PutUint32(buf[0:4], PRZMDMagic)
	buf[4] = 1 // version 1
	buf[5] = 0 // flags
	binary.LittleEndian.PutUint16(buf[6:8], uint16(numTokens))
	binary.LittleEndian.PutUint32(buf[8:12], d.id)
	binary.LittleEndian.PutUint32(buf[12:16], 0) // reserved

	lenOffset := 16
	payloadOffset := 16 + numTokens*2
	for i, tok := range d.tokens {
		binary.LittleEndian.PutUint16(buf[lenOffset+i*2:lenOffset+i*2+2], uint16(len(tok)))
		copy(buf[payloadOffset:], tok)
		payloadOffset += len(tok)
	}
	return buf
}

// NewDictionaryFromBytes parses a binary PRZMD container into a Dictionary.
func NewDictionaryFromBytes(data []byte) (*Dictionary, error) {
	if len(data) < 16 {
		return nil, fmt.Errorf("%w: data too short for PRZMD header", ErrInvalidDict)
	}
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != PRZMDMagic {
		return nil, fmt.Errorf("%w: invalid PRZMD magic 0x%08X", ErrInvalidDict, magic)
	}
	version := data[4]
	if version != 1 {
		return nil, fmt.Errorf("%w: unsupported PRZMD version %d", ErrInvalidDict, version)
	}
	numTokens := int(binary.LittleEndian.Uint16(data[6:8]))
	if numTokens <= 0 || numTokens > 256 {
		return nil, fmt.Errorf("%w: invalid token count %d", ErrInvalidDict, numTokens)
	}
	dictID := binary.LittleEndian.Uint32(data[8:12])

	expectedMin := 16 + numTokens*2
	if len(data) < expectedMin {
		return nil, fmt.Errorf("%w: truncated PRZMD lengths table", ErrInvalidDict)
	}

	rawTokens := make([]string, numTokens)
	payloadOffset := expectedMin
	for i := range numTokens {
		tLen := int(binary.LittleEndian.Uint16(data[16+i*2 : 16+i*2+2]))
		if payloadOffset+tLen > len(data) {
			return nil, fmt.Errorf("%w: truncated token payload at index %d", ErrInvalidDict, i)
		}
		rawTokens[i] = string(data[payloadOffset : payloadOffset+tLen])
		payloadOffset += tLen
	}

	return NewDictionary(dictID, rawTokens)
}

// Global thread-safe dictionary registry.
var (
	dictMu       sync.RWMutex
	dictRegistry = make(map[uint32]*Dictionary)
)

// RegisterDictionary registers an external dictionary in the global registry.
func RegisterDictionary(d *Dictionary) error {
	if d == nil {
		return fmt.Errorf("%w: nil dictionary", ErrInvalidDict)
	}
	dictMu.Lock()
	defer dictMu.Unlock()
	dictRegistry[d.id] = d
	return nil
}

// UnregisterDictionary removes a dictionary from the global registry.
func UnregisterDictionary(id uint32) {
	dictMu.Lock()
	defer dictMu.Unlock()
	delete(dictRegistry, id)
}

// GetDictionary retrieves a registered dictionary by id.
func GetDictionary(id uint32) *Dictionary {
	dictMu.RLock()
	defer dictMu.RUnlock()
	return dictRegistry[id]
}

// TrainDictionary computes an optimal token lexicon from a training corpus.
// It scores candidate n-grams by net compression gain:
// G(T) = Count(T) * (Length(T) - Cost(T))
// where Cost(T) = 1 for the top 8 tokens (1-byte opcode) and 2 for the remainder (2-byte opcode).
func TrainDictionary(corpus [][]byte, dictID uint32, maxTokens int) (*Dictionary, error) {
	if len(corpus) == 0 {
		return nil, errors.New("przm: empty corpus for training")
	}
	if maxTokens <= 0 {
		maxTokens = 64
	}
	if maxTokens > 256 {
		maxTokens = 256
	}

	freq := make(map[string]int)
	for _, doc := range corpus {
		n := len(doc)
		if n < 4 {
			continue
		}
		// Sample common n-grams of lengths 4 to 32
		maxL := min(n, 32)
		for l := 4; l <= maxL; l++ {
			step := 1
			if l > 8 {
				step = 2
			}
			for i := 0; i+l <= n; i += step {
				freq[string(doc[i:i+l])]++
			}
		}
	}

	type candidate struct {
		token string
		gain  int
	}
	var candidates []candidate
	for tok, count := range freq {
		if count < 2 {
			continue
		}
		// Tentative gain with 2-byte opcode cost
		gain := count * (len(tok) - 2)
		if gain > 0 {
			candidates = append(candidates, candidate{token: tok, gain: gain})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].gain != candidates[j].gain {
			return candidates[i].gain > candidates[j].gain
		}
		return len(candidates[i].token) > len(candidates[j].token)
	})

	if len(candidates) > maxTokens {
		candidates = candidates[:maxTokens]
	}

	if len(candidates) == 0 {
		return nil, errors.New("przm: could not discover repetitive tokens in corpus")
	}

	rawTokens := make([]string, len(candidates))
	for i, c := range candidates {
		rawTokens[i] = c.token
	}

	return NewDictionary(dictID, rawTokens)
}

// CompressWithDict encodes src using an explicit dictionary.
func CompressWithDict(src []byte, level int, dict *Dictionary) ([]byte, error) {
	return compressInternal(src, level, dict)
}

// DecompressWithDict decodes src with an explicit dictionary fallback.
func DecompressWithDict(src []byte, dict *Dictionary) ([]byte, error) {
	return decompressWithDictTo(src, nil, dict)
}

var (
	rawDictMu       sync.RWMutex
	rawDictRegistry = make(map[uint32][]byte)
)

// RegisterRawDictionary registers a raw prefix dictionary buffer under a 32-bit dictID.
func RegisterRawDictionary(id uint32, data []byte) {
	rawDictMu.Lock()
	defer rawDictMu.Unlock()
	rawDictRegistry[id] = data
}

// UnregisterRawDictionary removes a raw dictionary from the registry.
func UnregisterRawDictionary(id uint32) {
	rawDictMu.Lock()
	defer rawDictMu.Unlock()
	delete(rawDictRegistry, id)
}

// GetRawDictionary retrieves a registered raw dictionary buffer by id.
func GetRawDictionary(id uint32) []byte {
	rawDictMu.RLock()
	defer rawDictMu.RUnlock()
	return rawDictRegistry[id]
}

// DictIDBrotliWeb defines the 32-bit PRZM dictionary ID for the RFC 7932 Brotli-derived external web dictionary.
const DictIDBrotliWeb uint32 = 0x4252544C // "BRTL"

var webTokensRaw = []string{
	"</div>",                         // [0]
	"<div class=\"",                  // [1]
	"</span>",                        // [2]
	"<span class=\"",                 // [3]
	"</a>",                           // [4]
	"<li class=\"",                   // [5]
	"</li>",                          // [6]
	"class=\"",                       // [7]
	"<!DOCTYPE html>",                // [8]
	"<html lang=\"en\"",              // [9]
	"<head><meta charset=\"utf-8\">", // [10]
	"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">", // [11]
	"<title>",                          // [12]
	"</title>",                         // [13]
	"<link rel=\"stylesheet\" href=\"", // [14]
	"<script src=\"",                   // [15]
	" defer></script>",                 // [16]
	"</script>",                        // [17]
	"</head>",                          // [18]
	"<body class=\"",                   // [19]
	"</div></body></html>",             // [20]
	"<header class=\"",                 // [21]
	"</header>",                        // [22]
	"<main class=\"",                   // [23]
	"<main>",                           // [24]
	"</main>",                          // [25]
	"<section class=\"",                // [26]
	"<section>",                        // [27]
	"</section>",                       // [28]
	"<article class=\"",                // [29]
	"</article>",                       // [30]
	"<nav class=\"",                    // [31]
	"<nav>",                            // [32]
	"</nav>",                           // [33]
	"<ul class=\"",                     // [34]
	"<ul>",                             // [35]
	"</ul>",                            // [36]
	"<li>",                             // [37]
	"<a href=\"",                       // [38]
	"<button class=\"",                 // [39]
	"<button type=\"",                  // [40]
	"</button>",                        // [41]
	"<input type=\"",                   // [42]
	"<footer>",                         // [43]
	"</footer>",                        // [44]
	"<svg class=\"",                    // [45]
	"\" fill=\"currentColor\" viewBox=\"0 0 24 24\">", // [46]
	"<path d=\"", // [47]
	"\"/></svg>", // [48]
	"<div class=\"min-h-full flex flex-col\">",                              // [49]
	"<div class=\"max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 ",                  // [50]
	"h-16 flex items-center justify-between\">",                             // [51]
	"<div class=\"flex items-center space-x-",                               // [52]
	"flex items-center space-x-2 ",                                          // [53]
	"<nav class=\"hidden md:flex space-x-4\">",                              // [54]
	"text-slate-600 hover:text-indigo-600 px-3 py-2 text-sm font-medium\">", // [55]
	"inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-sm text-white bg-indigo-600 hover:bg-indigo-700\">", // [56]
	"<div class=\"grid grid-cols-1 md:grid-cols-3 gap-6 mb-8\">",     // [57]
	"<div class=\"bg-white overflow-hidden shadow rounded-lg p-5\">", // [58]
	"<dt class=\"text-sm font-medium text-slate-500 truncate\">",     // [59]
	"<dd class=\"mt-1 text-3xl font-semibold text-slate-900\">",      // [60]
	"</dd></div>",                             // [61]
	"Total Ingress Throughput",                // [62]
	"Mean Edge Latency (p99)",                 // [63]
	"Active Global Replicas",                  // [64]
	"CloudScale Infrastructure Control Plane", // [65]
	"px-4 sm:px-6 lg:px-8 py-8\">",            // [66]
	"sticky top-0 z-40 bg-white/95 backdrop-blur border-b border-slate-200\">", // [67]
	"h-full font-sans antialiased text-slate-800\">",                           // [68]
	"class=\"h-full bg-slate-50\"><head>",                                      // [69]
	"<a href=\"/overview\"",                                                    // [70]
	"<a href=\"/clusters\"",                                                    // [71]
	"<a href=\"/edge\"",                                                        // [72]
	"<a href=\"/metrics\"",                                                     // [73]
	"Kubernetes",                                                               // [74]
	"Edge Nodes",                                                               // [75]
	"Observability",                                                            // [76]
	"Deploy Service",                                                           // [77]
	"\"use strict\";",                                                          // [78]
	"!function(e,t){",                                                          // [79]
	"\"object\"==typeof exports&&\"object\"==typeof module?module.exports=t():", // [80]
	"\"function\"==typeof define&&define.amd?define([],t):",                     // [81]
	"\"object\"==typeof exports?exports.App=t():e.App=t()}(self,(function(){",   // [82]
	"return function(e){var t={};function n(r){",                                // [83]
	"if(t[r])return t[r].exports;",                                              // [84]
	"var o=t[r]={i:r,l:!1,exports:{}};",                                         // [85]
	"return e[r].call(o.exports,o,o.exports,n),o.l=!0,o.exports}",               // [86]
	"return n.m=e,n.c=t,n.d=function(e,t,r){",                                   // [87]
	"n.o(e,t)||Object.defineProperty(e,t,{enumerable:!0,get:r})},",              // [88]
	"n.r=function(e){", // [89]
	"\"undefined\"!=typeof Symbol&&Symbol.toStringTag&&Object.defineProperty(e,Symbol.toStringTag,{value:\"Module\"}),", // [90]
	"Object.defineProperty(e,\"__esModule\",{value:!0})},",                                                              // [91]
	"n.t=function(e,t){",                                      // [92]
	"if(1&t&&(e=n(e)),8&t)return e;",                          // [93]
	"if(4&t&&\"object\"==typeof e&&e&&e.__esModule)return e;", // [94]
	"var r=Object.create(null);",                              // [95]
	"if(n.r(r),Object.defineProperty(r,\"default\",{enumerable:!0,value:e}),2&t&&\"string\"!=typeof e)",                 // [96]
	"for(var o in e)n.d(r,o,function(t){return e[t]}.bind(null,o));return r},",                                          // [97]
	"n.n=function(e){var t=e&&e.__esModule?function(){return e.default}:function(){return e};return n.d(t,\"a\",t),t},", // [98]
	"n.o=function(e,t){return Object.prototype.hasOwnProperty.call(e,t)},n.p=\"\",n(n.s=102)}([",                        // [99]
	"function(e,t,n){\"use strict\";",                                    // [100]
	"var r=n(1);Object.defineProperty(t,\"__esModule\",{value:!0}),",     // [101]
	"t.StateController=function(e,t){return{dispatch:function(n){",       // [102]
	"e.setState((function(e){return Object.assign({},e,n)}))},",          // [103]
	"subscribe:function(e){return t.listeners.push(e),function(){",       // [104]
	"t.listeners=t.listeners.filter((function(t){return t!==e}))}}}}]);", // [105]
	"import React, { useState, useEffect } from 'react';",                // [106]
	"export default function ",                                           // [107]
	"export const ",                                                      // [108]
	"const [state, setState] = useState(",                                // [109]
	"useEffect(() => {",                                                  // [110]
	"return () => {",                                                     // [111]
	"onClick={() => ",                                                    // [112]
	"onChange={(e) => ",                                                  // [113]
	"className=\"",                                                       // [114]
	"<React.Fragment>",                                                   // [115]
	"</React.Fragment>",                                                  // [116]
	"console.log(",                                                       // [117]
	"JSON.stringify(",                                                    // [118]
	"JSON.parse(",                                                        // [119]
	"display: flex;",                                                     // [120]
	"display: grid;",                                                     // [121]
	"display: none;",                                                     // [122]
	"display: block;",                                                    // [123]
	"flex-direction: column;",                                            // [124]
	"align-items: center;",                                               // [125]
	"justify-content: center;",                                           // [126]
	"justify-content: space-between;",                                    // [127]
	"position: relative;",                                                // [128]
	"position: absolute;",                                                // [129]
	"position: fixed;",                                                   // [130]
	"margin: 0;",                                                         // [131]
	"padding: 0;",                                                        // [132]
	"box-sizing: border-box;",                                            // [133]
	"font-family: ",                                                      // [134]
	"background-color: ",                                                 // [135]
	"color: ",                                                            // [136]
	"border: 1px solid ",                                                 // [137]
	"border-radius: ",                                                    // [138]
	"font-size: ",                                                        // [139]
	"font-weight: ",                                                      // [140]
	"cursor: pointer;",                                                   // [141]
	"width: 100%;",                                                       // [142]
	"height: 100%;",                                                      // [143]
	"@media (min-width: 640px)",                                          // [144]
	"@media (min-width: 768px)",                                          // [145]
	"@media (min-width: 1024px)",                                         // [146]
	"@media (min-width: 1280px)",                                         // [147]
	"## 1. Architectural Overview",                                       // [148]
	"## Architecture Overview",                                           // [149]
	"## Table of Contents",                                               // [150]
	"### Specification",                                                  // [151]
	"| Parameter | Type | Description |",                                 // [152]
	"| :--- | :--- | :--- |",                                             // [153]
	"{\"status\":\"ok\",",                                                // [154]
	"\"server\":\"edge-router-04\",",                                     // [155]
	"\"timestamp\":\"",                                                   // [156]
	"\"roles\":[\"admin\",\"billing_manager\"]",                          // [157]
	"\"organization\":\"Acme Systems International LLC\"",                // [158]
	"{\"id\":",                                                           // [159]
	",\"name\":\"",                                                       // [160]
	",\"type\":\"",                                                       // [161]
	",\"status\":200",                                                    // [162]
	",\"status\":\"success\"",                                            // [163]
	",\"error\":null",                                                    // [164]
	",\"data\":[",                                                        // [165]
	",\"message\":\"",                                                    // [166]
	"https://schema.org/",                                                // [167]
	"application/json",                                                   // [168]
	"text/html; charset=utf-8",                                           // [169]
	".flex{display:flex}",                                                // [170]
	".inline-flex{display:inline-flex}",                                  // [171]
	".grid{display:grid}",                                                // [172]
	".hidden{display:none}",                                              // [173]
	".h-full{height:100%}",                                               // [174]
	".w-full{width:100%}",                                                // [175]
	".max-w-7xl{max-width:80rem}",                                        // [176]
	".items-center{align-items:center}",                                  // [177]
	".justify-between{justify-content:space-between}",                    // [178]
	".rounded-lg{border-radius:0.5rem}",                                  // [179]
	".rounded-xl{border-radius:0.75rem}",                                 // [180]
	".border{border-width:1px}",                                          // [181]
	".border-slate-200{border-color:#e2e8f0}",                            // [182]
	".bg-white{background-color:#ffffff}",                                // [183]
	".bg-slate-50{background-color:#f8fafc}",                             // [184]
	".bg-indigo-600{background-color:#4f46e5}",                           // [185]
	".text-white{color:#ffffff}",                                         // [186]
	".text-slate-900{color:#0f172a}",                                     // [187]
	".text-slate-600{color:#475569}",                                     // [188]
	".shadow-sm{box-shadow:0 1px 2px 0 rgba(0,0,0,0.05)}",                // [189]
	".shadow{box-shadow:0 1px 3px 0 rgba(0,0,0,0.1),0 1px 2px -1px rgba(0,0,0,0.1)}", // [190]
	".hover\\:bg-indigo-700:hover{background-color:#4338ca}",                         // [191]
	".hover\\:text-indigo-600:hover{color:#4f46e5}",                                  // [192]
	"@media (min-width: 768px){.md\\:flex{display:flex}}",                            // [193]
	"@media (min-width: 1024px){.lg\\:px-8{padding-left:2rem;padding-right:2rem}}",   // [194]
	".flex{",                               // [195]
	".border-",                             // [196]
	"hover\\:",                             // [197]
	"max-w-",                               // [198]
	"@media (min-width:",                   // [199]
	"{display:flex}",                       // [200]
	"{display:inline-flex}",                // [201]
	"{display:grid}",                       // [202]
	"{display:none}",                       // [203]
	"{height:100%}",                        // [204]
	"{width:100%}",                         // [205]
	"display:flex",                         // [206]
	"display:inline-flex",                  // [207]
	"display:grid",                         // [208]
	"display:none",                         // [209]
	"height:100%",                          // [210]
	"width:100%",                           // [211]
	"align-items:center",                   // [212]
	"justify-content:space-between",        // [213]
	"border-radius:0.5rem",                 // [214]
	"border-radius:0.75rem",                // [215]
	"border-width:1px",                     // [216]
	"border-color:#",                       // [217]
	"background-color:#",                   // [218]
	"padding-left:2rem;padding-right:2rem", // [219]
	"# RFC Specification: Hyper-Scale Distributed Event Streaming Engine",                                                                                         // [220]
	"## Abstract\nThis document specifies the protocol semantics, framing layers, and consensus mechanisms",                                                       // [221]
	" for ultra-low latency distributed stream replication across geo-replicated heterogeneous storage clusters.",                                                 // [222]
	" The architecture guarantees strict linearizability under Raft quorum while achieving sub-millisecond p99 latency across cross-datacenter fiber interlinks.", // [223]
	"The protocol utilizes zero-copy vectored ring buffers with hardware timestamping at the network interface card (NIC)",                                        // [224]
	" layer. Each partition is partitioned across a deterministic topological sheaf over simplicial complexes:",                                                   // [225]
	"1. **Section 1 (Control Plane):** Epoch synchronization and leader leases.",                                                                                  // [226]
	"2. **Section 2 (Log Replication):** Multi-Paxos pipeline with pipelined commit indices.",                                                                     // [227]
	"3. **Section 3 (Storage Tiering):** Autonomous tiered compaction to append-only log segments.",                                                               // [228]
	"### Performance Bounds", // [229]
	"Under maximum saturation, each broker node sustains 12.8 million input/output operations per second (IOPS)", // [230]
	" with memory footprint bounded by O(log N) where N represents active TCP/QUIC stream sessions.",             // [231]
	".99,\"in_stock\":true,\"tags\":[\"cloud\",\"saas\",\"premium\"]},{\"item_id\":\"it_",                        // [232]
	".99,\"in_stock\":true,\"tags\":[\"cloud\",\"saas\",\"premium\"]},",                                          // [233]
	"{\"item_id\":\"it_",                                   // [234]
	"\",\"sku\":\"SKU-",                                    // [235]
	"-PRO\",\"price\":",                                    // [236]
	",\"price\":",                                          // [237]
	"{\"scope\":\"read:all\",\"expires_at\":",              // [238]
	"{\"scope\":\"write:deployments\",\"expires_at\":",     // [239]
	"{\"scope\":\"admin:billing\",\"expires_at\":",         // [240]
	",\"profile\":{\"avatar\":\"https://cdn.corp/avatars/", // [241]
	",\"organization\":\"Acme Systems International LLC\",\"tier\":\"enterprise_plus\",\"mfa_enabled\":true,\"last_login_ip\":\"192.168.1.105\"}},",                                                          // [242]
	",\"organization\":\"Acme Systems International LLC\",\"tier\":\"enterprise_plus\",\"mfa_enabled\":true,\"last_login_ip\":\"",                                                                            // [243]
	"\"username\":\"alex_developer\",\"email\":\"alex@engineering.corp\",\"roles\":[\"admin\",\"billing_manager\"]",                                                                                          // [244]
	"{\"status\":\"ok\",\"code\":200,\"server\":\"edge-router-04\",\"region\":\"us-east-1\",\"timestamp\":\"2026-10-11T01:15:30.124Z\",\"data\":{\"session_id\":\"sess_98f4a1c0d2\",\"user\":{\"id\":84192,", // [245]
	"{\"status\":\"ok\",\"code\":200,\"server\":\"edge-router-04\",\"region\":\"us-east-1\",\"timestamp\":\"",                                                                                                // [246]
	"\",\"data\":{\"session_id\":\"sess_", // [247]
	"\",\"user\":{\"id\":",                // [248]
	"\"expires_at\":1798761600}",          // [249]
	"\"expires_at\":1798761600",           // [250]
	"return PRZM_ERR_CORRUPTED_FRAME;",    // [251]
	"uint8_t* ",                           // [252]
	"read_u32_le(",                        // [253]
	"uint32_t ",                           // [254]
	"size_t ",                             // [255]
}

func init() {
	initRFCTables()
	if webDict, err := NewDictionary(DictIDBrotliWeb, webTokensRaw); err == nil {
		_ = RegisterDictionary(webDict)
	}
}
