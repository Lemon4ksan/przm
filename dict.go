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

var jsonTokensRaw = []string{
	// Top 8 Direct 1-byte Opcode Tokens (0xD8 .. 0xDF)
	`,"status":`,
	`,"level":"INFO"`,
	`,"method":"`,
	`,"uri":"`,
	`,"duration_ms":`,
	`,"bytes_sent":`,
	`,"client_ip":"`,
	`{"ts":`,

	// Extended 2-byte Opcode Tokens (0xF2 <tokenID>)
	`,"status":200`,
	`,"status":201`,
	`,"status":204`,
	`,"status":400`,
	`,"status":401`,
	`,"status":404`,
	`,"status":500`,
	`,"method":"GET"`,
	`,"method":"POST"`,
	`,"method":"PUT"`,
	`,"method":"DELETE"`,
	`,"client_ip":"192.168.1.`,
	`,"uri":"/api/v1/`,
	`"/api/v1/auth/login"`,
	`"/api/v1/users/profile"`,
	`"/api/v1/inventory/items"`,
	`"/api/v1/billing/checkout"`,
	`"/healthz"`,
	`"/metrics"`,
	`,"trace_id":"e4d8`,
	`,"trace_id":"`,
	`{"id":`,
	`,"id":`,
	`,"user_id":`,
	`,"name":"`,
	`,"type":"`,
	`,"message":"`,
	`,"error":"`,
	`,"timestamp":`,
	`,"created_at":"`,
	`,"updated_at":"`,
	`,"description":"`,
	`,"action":"`,
	`,"event":"`,
	`{"event":"`,
	`{"data":`,
	`,"data":`,
	`,"count":`,
	`,"value":`,
	`,"code":`,
	`,"metric":"`,
	`,"sensor_id":"`,
	`,"resp":200`,
	`,"status":"ok"`,
	`true,`,
	`false,`,
	`null,`,
	`,"ts":`,
	`,"level":"WARN"`,
	`,"level":"ERROR"`,
	`,"level":"DEBUG"`,
	`,"action":"scroll"`,
	`,"action":"click"`,
	`,"sensor_id":"temp-`,
	`,"metric":"temperature"`,
}

type dictMatcher struct {
	id     uint32
	tokens [][]byte
	head   [65536]int16
	chain  [256]int16
}

var jsonMatcher dictMatcher

func init() {
	initMatcher(&jsonMatcher, DictIDJSON, jsonTokensRaw)
}

func initMatcher(m *dictMatcher, id uint32, rawTokens []string) {
	m.id = id
	m.tokens = make([][]byte, len(rawTokens))
	for i := range m.head {
		m.head[i] = -1
	}
	for i := range m.chain {
		m.chain[i] = -1
	}

	for i, s := range rawTokens {
		b := []byte(s)
		m.tokens[i] = b
		if len(b) >= 4 {
			h := (binary.LittleEndian.Uint32(b[:4]) * 0x1E35A7BD) >> 16
			m.chain[i] = m.head[h]
			m.head[h] = int16(i)
		}
	}
}

func getBuiltinDictBytes(dictID uint32) [][]byte {
	switch dictID {
	case DictIDJSON:
		return jsonMatcher.tokens
	default:
		return nil
	}
}

func (m *dictMatcher) match(src []byte, pos int) (bestIdx int, bestLen int) {
	uSize := len(src)
	if pos+4 > uSize {
		return -1, 0
	}
	h := (binary.LittleEndian.Uint32(src[pos:pos+4]) * 0x1E35A7BD) >> 16
	bestIdx = -1
	bestLen = 0

	for idx := m.head[h]; idx >= 0; idx = m.chain[idx] {
		tok := m.tokens[idx]
		tLen := len(tok)
		if tLen > bestLen && pos+tLen <= uSize {
			if bytes.Equal(src[pos:pos+tLen], tok) {
				bestIdx = int(idx)
				bestLen = tLen
			}
		}
	}
	return bestIdx, bestLen
}

// detectDictionary inspects initial block payload to determine if a well-known schema dictionary
// can compress repetitive grammar constructs with 0-overhead token opcodes.
func detectDictionary(src []byte) uint32 {
	sampleLen := min(len(src), 2048)
	if sampleLen < 64 {
		return DictIDNone
	}
	sample := src[:sampleLen]

	// JSON detection: count common delimiter pairs
	jsonHits := 0
	for i := 0; i+1 < sampleLen; i++ {
		b0 := sample[i]
		b1 := sample[i+1]
		if (b0 == '"' && b1 == ':') || (b0 == '"' && b1 == ',') || (b0 == '{' && b1 == '"') {
			jsonHits++
		}
	}
	if jsonHits >= 8 {
		return DictIDJSON
	}

	return DictIDNone
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
	for i := 0; i < numTokens; i++ {
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
