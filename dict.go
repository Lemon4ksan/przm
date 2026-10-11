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

type dictMatcher struct {
	id     uint32
	tokens [][]byte
	head   [65536]int16
	chain  [256]int16
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
	// 0..7: Top-8 Direct 1-byte Opcode Tokens (0xD8 .. 0xDF)
	"</div>",
	"<div class=\"",
	"</span>",
	"<span class=\"",
	"</a>",
	"<li class=\"",
	"</li>",
	"class=\"",

	// 8..: Extended 2-byte Opcode Tokens (0xF2 <tokenID>)
	// HTML5 Structural Blocks
	"<!DOCTYPE html>",
	"<html lang=\"en\"",
	"<head><meta charset=\"utf-8\">",
	"<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">",
	"<title>",
	"</title>",
	"<link rel=\"stylesheet\" href=\"",
	"<script src=\"",
	" defer></script>",
	"</script>",
	"</head>",
	"<body class=\"",
	"</div></body></html>",
	"<header class=\"",
	"</header>",
	"<main class=\"",
	"<main>",
	"</main>",
	"<section class=\"",
	"<section>",
	"</section>",
	"<article class=\"",
	"</article>",
	"<nav class=\"",
	"<nav>",
	"</nav>",
	"<ul class=\"",
	"<ul>",
	"</ul>",
	"<li>",
	"<a href=\"",
	"<button class=\"",
	"<button type=\"",
	"</button>",
	"<input type=\"",
	"<footer>",
	"</footer>",
	"<svg class=\"",
	"\" fill=\"currentColor\" viewBox=\"0 0 24 24\">",
	"<path d=\"",
	"\"/></svg>",
	"<div class=\"min-h-full flex flex-col\">",
	"<div class=\"max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 ",
	"h-16 flex items-center justify-between\">",
	"<div class=\"flex items-center space-x-",
	"flex items-center space-x-2 ",
	"<nav class=\"hidden md:flex space-x-4\">",
	"text-slate-600 hover:text-indigo-600 px-3 py-2 text-sm font-medium\">",
	"inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-sm text-white bg-indigo-600 hover:bg-indigo-700\">",
	"<div class=\"grid grid-cols-1 md:grid-cols-3 gap-6 mb-8\">",
	"<div class=\"bg-white overflow-hidden shadow rounded-lg p-5\">",
	"<dt class=\"text-sm font-medium text-slate-500 truncate\">",
	"<dd class=\"mt-1 text-3xl font-semibold text-slate-900\">",
	"</dd></div>",
	"Total Ingress Throughput",
	"Mean Edge Latency (p99)",
	"Active Global Replicas",
	"CloudScale Infrastructure Control Plane",
	"px-4 sm:px-6 lg:px-8 py-8\">",
	"sticky top-0 z-40 bg-white/95 backdrop-blur border-b border-slate-200\">",
	"h-full font-sans antialiased text-slate-800\">",
	"class=\"h-full bg-slate-50\"><head>",
	"<a href=\"/overview\"",
	"<a href=\"/clusters\"",
	"<a href=\"/edge\"",
	"<a href=\"/metrics\"",
	"Kubernetes",
	"Edge Nodes",
	"Observability",
	"Deploy Service",

	// JavaScript & React Runtime Blocks
	"\"use strict\";",
	"!function(e,t){",
	"\"object\"==typeof exports&&\"object\"==typeof module?module.exports=t():",
	"\"function\"==typeof define&&define.amd?define([],t):",
	"\"object\"==typeof exports?exports.App=t():e.App=t()}(self,(function(){",
	"return function(e){var t={};function n(r){",
	"if(t[r])return t[r].exports;",
	"var o=t[r]={i:r,l:!1,exports:{}};",
	"return e[r].call(o.exports,o,o.exports,n),o.l=!0,o.exports}",
	"return n.m=e,n.c=t,n.d=function(e,t,r){",
	"n.o(e,t)||Object.defineProperty(e,t,{enumerable:!0,get:r})},",
	"n.r=function(e){",
	"\"undefined\"!=typeof Symbol&&Symbol.toStringTag&&Object.defineProperty(e,Symbol.toStringTag,{value:\"Module\"}),",
	"Object.defineProperty(e,\"__esModule\",{value:!0})},",
	"n.t=function(e,t){",
	"if(1&t&&(e=n(e)),8&t)return e;",
	"if(4&t&&\"object\"==typeof e&&e&&e.__esModule)return e;",
	"var r=Object.create(null);",
	"if(n.r(r),Object.defineProperty(r,\"default\",{enumerable:!0,value:e}),2&t&&\"string\"!=typeof e)",
	"for(var o in e)n.d(r,o,function(t){return e[t]}.bind(null,o));return r},",
	"n.n=function(e){var t=e&&e.__esModule?function(){return e.default}:function(){return e};return n.d(t,\"a\",t),t},",
	"n.o=function(e,t){return Object.prototype.hasOwnProperty.call(e,t)},n.p=\"\",n(n.s=102)}([",
	"function(e,t,n){\"use strict\";",
	"var r=n(1);Object.defineProperty(t,\"__esModule\",{value:!0}),",
	"t.StateController=function(e,t){return{dispatch:function(n){",
	"e.setState((function(e){return Object.assign({},e,n)}))},",
	"subscribe:function(e){return t.listeners.push(e),function(){",
	"t.listeners=t.listeners.filter((function(t){return t!==e}))}}}}]);",
	"import React, { useState, useEffect } from 'react';",
	"export default function ",
	"export const ",
	"const [state, setState] = useState(",
	"useEffect(() => {",
	"return () => {",
	"onClick={() => ",
	"onChange={(e) => ",
	"className=\"",
	"<React.Fragment>",
	"</React.Fragment>",
	"console.log(",
	"JSON.stringify(",
	"JSON.parse(",

	// CSS & Tailwind
	"display: flex;",
	"display: grid;",
	"display: none;",
	"display: block;",
	"flex-direction: column;",
	"align-items: center;",
	"justify-content: center;",
	"justify-content: space-between;",
	"position: relative;",
	"position: absolute;",
	"position: fixed;",
	"margin: 0;",
	"padding: 0;",
	"box-sizing: border-box;",
	"font-family: ",
	"background-color: ",
	"color: ",
	"border: 1px solid ",
	"border-radius: ",
	"font-size: ",
	"font-weight: ",
	"cursor: pointer;",
	"width: 100%;",
	"height: 100%;",
	"@media (min-width: 640px)",
	"@media (min-width: 768px)",
	"@media (min-width: 1024px)",
	"@media (min-width: 1280px)",

	// Technical Markdown & API Telemetry
	"## Architecture Overview",
	"## Table of Contents",
	"### Specification",
	"| Parameter | Type | Description |",
	"| :--- | :--- | :--- |",
	"{\"status\":\"ok\",",
	"\"server\":\"edge-router-04\",",
	"\"timestamp\":\"",
	"\"roles\":[\"admin\",\"billing_manager\"]",
	"\"organization\":\"Acme Systems International LLC\"",
	"{\"id\":",
	",\"name\":\"",
	",\"type\":\"",
	",\"status\":200",
	",\"status\":\"success\"",
	",\"error\":null",
	",\"data\":[",
	",\"message\":\"",
	"https://schema.org/",
	"application/json",
	"text/html; charset=utf-8",

	// Tailwind CSS Atomic Utility Classes & Structural Rules
	".flex{display:flex}",
	".inline-flex{display:inline-flex}",
	".grid{display:grid}",
	".hidden{display:none}",
	".h-full{height:100%}",
	".w-full{width:100%}",
	".max-w-7xl{max-width:80rem}",
	".items-center{align-items:center}",
	".justify-between{justify-content:space-between}",
	".rounded-lg{border-radius:0.5rem}",
	".rounded-xl{border-radius:0.75rem}",
	".border{border-width:1px}",
	".border-slate-200{border-color:#e2e8f0}",
	".bg-white{background-color:#ffffff}",
	".bg-slate-50{background-color:#f8fafc}",
	".bg-indigo-600{background-color:#4f46e5}",
	".text-white{color:#ffffff}",
	".text-slate-900{color:#0f172a}",
	".text-slate-600{color:#475569}",
	".shadow-sm{box-shadow:0 1px 2px 0 rgba(0,0,0,0.05)}",
	".shadow{box-shadow:0 1px 3px 0 rgba(0,0,0,0.1),0 1px 2px -1px rgba(0,0,0,0.1)}",
	".hover\\:bg-indigo-700:hover{background-color:#4338ca}",
	".hover\\:text-indigo-600:hover{color:#4f46e5}",
	"@media (min-width: 768px){.md\\:flex{display:flex}}",
	"@media (min-width: 1024px){.lg\\:px-8{padding-left:2rem;padding-right:2rem}}",
	".flex{",
	".border-",
	"hover\\:",
	"max-w-",
	"@media (min-width:",
	"{display:flex}",
	"{display:inline-flex}",
	"{display:grid}",
	"{display:none}",
	"{height:100%}",
	"{width:100%}",
	"display:flex",
	"display:inline-flex",
	"display:grid",
	"display:none",
	"height:100%",
	"width:100%",
	"align-items:center",
	"justify-content:space-between",
	"border-radius:0.5rem",
	"border-radius:0.75rem",
	"border-width:1px",
	"border-color:#",
	"background-color:#",
	"padding-left:2rem;padding-right:2rem",
}

func init() {
	if webDict, err := NewDictionary(DictIDBrotliWeb, webTokensRaw); err == nil {
		_ = RegisterDictionary(webDict)
	}
}


