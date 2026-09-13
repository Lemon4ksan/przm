// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
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

var codeTokensRaw = []string{
	// Top 8 Direct 1-byte Opcode Tokens (0xD8 .. 0xDF)
	"if err != nil {\n",
	"return nil, err",
	"func (",
	"package ",
	"import (\n",
	"return nil",
	"return err",
	"for i := 0; i < ",

	// Extended 2-byte Opcode Tokens (0xF2 <tokenID>)
	"context.Context",
	"binary.LittleEndian.",
	"fmt.Sprintf(",
	"fmt.Errorf(",
	"type ",
	"struct {\n",
	"interface {\n",
	"[]byte",
	"string",
	"error",
	"make([]",
	"append(",
	"switch ",
	"default:\n",
	"continue\n",
	"const (\n",
	"var (\n",
	"sync.Mutex",
	"sync.Pool",
	"func (s *",
	"len(",
	"return ",
	"case ",
	"uint64",
	"uint32",
	"int64",
	"float64",
	"bool",
	"true",
	"false",
	"nil",
	"bytes.Buffer",
}

type dictMatcher struct {
	id     uint32
	tokens [][]byte
	head   [65536]int16
	chain  [256]int16
}

var (
	jsonMatcher dictMatcher
	codeMatcher dictMatcher
)

func init() {
	initMatcher(&jsonMatcher, DictIDJSON, jsonTokensRaw)
	initMatcher(&codeMatcher, DictIDCode, codeTokensRaw)
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
	case DictIDCode:
		return codeMatcher.tokens
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

	// Code detection: count programming keywords
	codeHits := 0
	if bytes.Contains(sample, []byte("func ")) {
		codeHits += 2
	}
	if bytes.Contains(sample, []byte("package ")) {
		codeHits += 2
	}
	if bytes.Contains(sample, []byte("import ")) {
		codeHits += 2
	}
	if bytes.Contains(sample, []byte("return ")) {
		codeHits++
	}
	if bytes.Contains(sample, []byte("err != nil")) {
		codeHits += 2
	}
	if codeHits >= 3 {
		return DictIDCode
	}

	return DictIDNone
}
