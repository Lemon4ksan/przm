// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"testing"
)

func TestDict_NoBuiltinDictLeak(t *testing.T) {
	jsonPayload := []byte(`{"status":200,"level":"INFO","method":"GET","uri":"/api/v1/billing/checkout","user_id":"123","message":"ok"}`)

	compressed, err := Compress(jsonPayload, 2)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	hdr, err := ParseFrameHeader(compressed)
	if err != nil {
		t.Fatalf("ParseFrameHeader failed: %v", err)
	}
	if (hdr.Flags & FlagDictRef) != 0 {
		t.Fatalf("expected FlagDictRef to be 0 (no built-in dict), got 0x%02X", hdr.Flags)
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(decompressed, jsonPayload) {
		t.Fatalf("JSON roundtrip mismatch: got %s, want %s", string(decompressed), string(jsonPayload))
	}
}

func TestDict_ExternalDictionaryContainer(t *testing.T) {
	tokens := []string{
		"SELECT * FROM ",
		" WHERE id = ",
		" ORDER BY created_at DESC",
		" LIMIT 100",
		" INSERT INTO ",
		" VALUES (",
	}

	dict, err := NewDictionary(0x2001, tokens)
	if err != nil {
		t.Fatalf("NewDictionary failed: %v", err)
	}

	if dict.ID() != 0x2001 {
		t.Fatalf("dict.ID() got 0x%X, want 0x2001", dict.ID())
	}
	if len(dict.Tokens()) != len(tokens) {
		t.Fatalf("len(dict.Tokens()) got %d, want %d", len(dict.Tokens()), len(tokens))
	}

	// Test PRZMD serialization
	serialized := dict.Bytes()
	if len(serialized) < 16 {
		t.Fatalf("serialized dictionary too small: %d bytes", len(serialized))
	}

	restored, err := NewDictionaryFromBytes(serialized)
	if err != nil {
		t.Fatalf("NewDictionaryFromBytes failed: %v", err)
	}

	if restored.ID() != dict.ID() {
		t.Fatalf("restored ID mismatch: got 0x%X, want 0x%X", restored.ID(), dict.ID())
	}
	if len(restored.Tokens()) != len(dict.Tokens()) {
		t.Fatalf("restored tokens count mismatch: got %d, want %d", len(restored.Tokens()), len(dict.Tokens()))
	}
	for i := range tokens {
		if !bytes.Equal(restored.Tokens()[i], dict.Tokens()[i]) {
			t.Fatalf("token %d mismatch: got %q, want %q", i, restored.Tokens()[i], dict.Tokens()[i])
		}
	}
}

func TestDict_RegistryAndExplicitCodec(t *testing.T) {
	tokens := []string{
		"temperature_celsius",
		"humidity_relative_percent",
		"pressure_hectopascals",
		"device_uuid_prefix",
	}

	dictID := uint32(0x1050)
	dict, err := NewDictionary(dictID, tokens)
	if err != nil {
		t.Fatalf("NewDictionary error: %v", err)
	}

	err = RegisterDictionary(dict)
	if err != nil {
		t.Fatalf("RegisterDictionary error: %v", err)
	}
	defer UnregisterDictionary(dictID)

	retrieved := GetDictionary(dictID)
	if retrieved == nil || retrieved.ID() != dictID {
		t.Fatalf("GetDictionary failed to retrieve registered dictionary")
	}

	telemetryData := []byte("device_uuid_prefix-001: temperature_celsius=23.5, humidity_relative_percent=45, pressure_hectopascals=1013")
	compressed, err := CompressWithDict(telemetryData, 2, dict)
	if err != nil {
		t.Fatalf("CompressWithDict error: %v", err)
	}

	// Decompress without explicit dict (should resolve via registry)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress via registry failed: %v", err)
	}

	if !bytes.Equal(decompressed, telemetryData) {
		t.Fatalf("Decompress mismatch: got %s, want %s", string(decompressed), string(telemetryData))
	}

	// Decompress with explicit dict
	decompWithDict, err := DecompressWithDict(compressed, dict)
	if err != nil {
		t.Fatalf("DecompressWithDict failed: %v", err)
	}
	if !bytes.Equal(decompWithDict, telemetryData) {
		t.Fatalf("DecompressWithDict mismatch")
	}
}

func TestDict_UnknownDictError(t *testing.T) {
	tokens := []string{"temporary_token_alpha", "temporary_token_beta"}
	tempID := uint32(0x7777)
	dict, err := NewDictionary(tempID, tokens)
	if err != nil {
		t.Fatalf("NewDictionary error: %v", err)
	}

	payload := bytes.Repeat([]byte("temporary_token_alpha data temporary_token_beta "), 10)
	compressed, err := CompressWithDict(payload, 2, dict)
	if err != nil {
		t.Fatalf("CompressWithDict error: %v", err)
	}

	// Ensure tempID is NOT in registry
	UnregisterDictionary(tempID)

	_, err = Decompress(compressed)
	if err == nil {
		t.Fatal("Decompress with unknown DictID should fail, but succeeded")
	}
}

func TestDict_TrainDictionary(t *testing.T) {
	corpus := [][]byte{
		[]byte("log_entry: service_alpha status=ok response_time=12ms action=query"),
		[]byte("log_entry: service_beta status=ok response_time=15ms action=query"),
		[]byte("log_entry: service_gamma status=ok response_time=9ms action=query"),
		[]byte("log_entry: service_delta status=ok response_time=20ms action=query"),
	}

	dictID := uint32(0x3000)
	dict, err := TrainDictionary(corpus, dictID, 16)
	if err != nil {
		t.Fatalf("TrainDictionary failed: %v", err)
	}

	if dict.ID() != dictID {
		t.Fatalf("trained dict ID got 0x%X, want 0x%X", dict.ID(), dictID)
	}
	if len(dict.Tokens()) == 0 {
		t.Fatal("TrainDictionary produced 0 tokens")
	}
}

func TestDict_RawDictionaryPrefix(t *testing.T) {
	rawDict := []byte("<!DOCTYPE html><html><head><title>Standard Web Dictionary Prefix Buffer</title></head><body><div class=\"container\"><header><h1>Welcome</h1></header>")
	dictID := uint32(0x57454231) // "WEB1"

	payload := []byte("<!DOCTYPE html><html><head><title>Standard Web Dictionary Prefix Buffer</title></head><body><div class=\"container\"><header><h1>Welcome</h1></header><p>Custom content page here</p></div></body></html>")

	for _, level := range []int{1, 3, 5} {
		opts := CompressOptions{
			Level:   level,
			RawDict: rawDict,
			DictID:  dictID,
		}

		comp, err := CompressWithOptions(payload, opts)
		if err != nil {
			t.Fatalf("level %d: CompressWithOptions error: %v", level, err)
		}

		// 1. Decompress with explicit DecompressOptions
		decomp, err := DecompressWithOptions(comp, DecompressOptions{RawDict: rawDict})
		if err != nil {
			t.Fatalf("level %d: DecompressWithOptions error: %v", level, err)
		}
		if !bytes.Equal(decomp, payload) {
			t.Fatalf("level %d: explicit raw dict mismatch: got %q, want %q", level, string(decomp), string(payload))
		}

		// 2. Decompress via global RawDict registry
		RegisterRawDictionary(dictID, rawDict)
		decompReg, err := Decompress(comp)
		if err != nil {
			t.Fatalf("level %d: Decompress via registry error: %v", level, err)
		}
		if !bytes.Equal(decompReg, payload) {
			t.Fatalf("level %d: registry raw dict mismatch: got %q, want %q", level, string(decompReg), string(payload))
		}
		UnregisterRawDictionary(dictID)

		// 3. Unregistered dictID should fail cleanly
		_, err = Decompress(comp)
		if err == nil {
			t.Fatalf("level %d: expected error for unregistered dictID, got nil", level)
		}
	}
}

func TestDict_RawDictionaryLargeBrotliLike(t *testing.T) {
	// Construct a realistic 122,784 byte external dictionary
	dictBuf := make([]byte, 122784)
	pattern := []byte("https://schema.org/WebPage application/json charset=utf-8 <meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"> ")
	for i := 0; i < len(dictBuf); i += len(pattern) {
		copy(dictBuf[i:], pattern[:min(len(pattern), len(dictBuf)-i)])
	}

	dictID := uint32(0x4252544C) // "BRTL"

	// Create payload referencing pieces from start, middle, and end of the dictionary
	var payload bytes.Buffer
	payload.WriteString("{\"header\": \"")
	payload.Write(dictBuf[100:300])
	payload.WriteString("\", \"middle\": \"")
	payload.Write(dictBuf[60000:60500])
	payload.WriteString("\", \"tail\": \"")
	payload.Write(dictBuf[120000:120400])
	payload.WriteString("\", \"boundary\": \"")
	payload.Write(dictBuf[122700:122784])
	payload.WriteString("continuation_after_dict\"}")

	rawPayload := payload.Bytes()

	for _, level := range []int{1, 3, 5} {
		opts := CompressOptions{
			Level:   level,
			RawDict: dictBuf,
			DictID:  dictID,
		}

		comp, err := CompressWithOptions(rawPayload, opts)
		if err != nil {
			t.Fatalf("level %d: Compress error: %v", level, err)
		}

		if len(comp) >= len(rawPayload) {
			t.Fatalf("level %d: expected dictionary compression savings, got %d >= %d", level, len(comp), len(rawPayload))
		}

		RegisterRawDictionary(dictID, dictBuf)
		decomp, err := Decompress(comp)
		if err != nil {
			t.Fatalf("level %d: Decompress error: %v", level, err)
		}
		if !bytes.Equal(decomp, rawPayload) {
			t.Fatalf("level %d: large dictionary roundtrip mismatch", level)
		}
		UnregisterRawDictionary(dictID)
	}
}

func TestDict_MinMatch3AndCompactDict(t *testing.T) {
	dictBuf := make([]byte, 122784)
	for i := range dictBuf {
		dictBuf[i] = byte((i*37 + 13) % 256)
	}

	dictID := uint32(0x4252544C)
	RegisterRawDictionary(dictID, dictBuf)
	defer UnregisterRawDictionary(dictID)

	var payload bytes.Buffer
	payload.WriteString(`{"type":"custom_web_event","tag":"`)
	payload.Write(dictBuf[100:103]) // 3-byte match!
	payload.WriteString(`","phrase":"`)
	payload.Write(dictBuf[1200:1225]) // 25-byte match in dict
	payload.WriteString(`","short":"`)
	payload.Write(dictBuf[5000:5003]) // 3-byte match in dict!
	payload.WriteString(`","repeat":"`)
	payload.Write(dictBuf[1200:1225]) // repeated match
	payload.WriteString(`"}`)

	data := payload.Bytes()

	for _, level := range []int{1, 3, 5} {
		opts := CompressOptions{
			Level:       level,
			RawDict:     dictBuf,
			DictID:      dictID,
			MinMatch:    3,
			CompactDict: true,
		}

		comp, err := CompressWithOptions(data, opts)
		if err != nil {
			t.Fatalf("level %d: CompressWithOptions failed: %v", level, err)
		}

		hdr, err := ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("level %d: ParseFrameHeader failed: %v", level, err)
		}
		if (hdr.Flags & FlagMinMatch3) == 0 {
			t.Fatalf("level %d: expected FlagMinMatch3 to be set in header, got 0x%02X", level, hdr.Flags)
		}

		decomp, err := Decompress(comp)
		if err != nil {
			t.Fatalf("level %d: Decompress failed: %v", level, err)
		}

		if !bytes.Equal(decomp, data) {
			t.Fatalf("level %d: decompressed mismatch: got %q, want %q", level, decomp, data)
		}
	}
}

func TestDict_Level6_HenselUltrametric(t *testing.T) {
	dictBuf := make([]byte, 122784)
	pattern := []byte("<div class=\"container\"><header class=\"header\"><h1>Title</h1></header></div>")
	for i := 0; i < len(dictBuf); i += len(pattern) {
		copy(dictBuf[i:], pattern[:min(len(pattern), len(dictBuf)-i)])
	}

	RegisterRawDictionary(DictIDBrotliWeb, dictBuf)
	defer UnregisterRawDictionary(DictIDBrotliWeb)

	// Realistic web payload with tokens and repetitions
	var payload bytes.Buffer
	for i := 0; i < 20; i++ {
		payload.WriteString("<!DOCTYPE html><html lang=\"en\"><head><meta charset=\"utf-8\">")
		payload.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"><title>PRZM L6 Test</title>")
		payload.WriteString("<link rel=\"stylesheet\" href=\"style.css\"></head><body>")
		payload.WriteString("<div class=\"main\"><header><nav><ul><li><a href=\"#home\">Home</a></li>")
		payload.WriteString("<li><a href=\"#about\">About</a></li></ul></nav></header>")
		payload.WriteString("<main class=\"content\"><section class=\"hero\"><h1>Welcome</h1><p>Fast decompression</p></section></main>")
		payload.WriteString("<footer><p>&copy; 2026</p></footer></div></body></html>\n")
	}

	raw := payload.Bytes()

	opts := CompressOptions{
		Level:   6,
		RawDict: dictBuf,
		DictID:  DictIDBrotliWeb,
	}

	comp, err := CompressWithOptions(raw, opts)
	if err != nil {
		t.Fatalf("Level 6 CompressWithOptions failed: %v", err)
	}

	t.Logf("Raw size: %d B, L6 Compressed size: %d B (Ratio: %.2fx)", len(raw), len(comp), float64(len(raw))/float64(len(comp)))

	decomp, err := Decompress(comp)
	if err != nil {
		t.Fatalf("Level 6 Decompress failed: %v", err)
	}

	if !bytes.Equal(decomp, raw) {
		t.Fatalf("Level 6 decompressed output does not match original (lossless check failed)")
	}
}

func TestDict_HTML5_BrotliBeat(t *testing.T) {
	snippet := `<!DOCTYPE html><html lang="en" class="h-full bg-slate-50"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>CloudScale Infrastructure Control Plane</title><link rel="stylesheet" href="/assets/css/app.min.css"><script src="/assets/js/runtime.min.js" defer></script></head><body class="h-full font-sans antialiased text-slate-800"><div class="min-h-full flex flex-col"><header class="sticky top-0 z-40 bg-white/95 backdrop-blur border-b border-slate-200"><div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between"><div class="flex items-center space-x-6"><a href="/" class="flex items-center space-x-2 text-indigo-600 font-bold text-xl"><svg class="w-8 h-8" fill="currentColor" viewBox="0 0 24 24"><path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5"/></svg><span>CloudScale</span></a><nav class="hidden md:flex space-x-4"><a href="/overview" class="text-slate-600 hover:text-indigo-600 px-3 py-2 text-sm font-medium">Overview</a><a href="/clusters" class="text-slate-600 hover:text-indigo-600 px-3 py-2 text-sm font-medium">Kubernetes</a><a href="/edge" class="text-slate-600 hover:text-indigo-600 px-3 py-2 text-sm font-medium">Edge Nodes</a><a href="/metrics" class="text-slate-600 hover:text-indigo-600 px-3 py-2 text-sm font-medium">Observability</a></nav></div><div class="flex items-center space-x-4"><button class="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-sm text-white bg-indigo-600 hover:bg-indigo-700">Deploy Service</button></div></div></header><main class="flex-1 max-w-7xl w-full mx-auto px-4 sm:px-6 lg:px-8 py-8"><div class="grid grid-cols-1 md:grid-cols-3 gap-6 mb-8"><div class="bg-white overflow-hidden shadow rounded-lg p-5"><dt class="text-sm font-medium text-slate-500 truncate">Total Ingress Throughput</dt><dd class="mt-1 text-3xl font-semibold text-slate-900">48.2 Gbps</dd></div><div class="bg-white overflow-hidden shadow rounded-lg p-5"><dt class="text-sm font-medium text-slate-500 truncate">Mean Edge Latency (p99)</dt><dd class="mt-1 text-3xl font-semibold text-slate-900">1.82 ms</dd></div><div class="bg-white overflow-hidden shadow rounded-lg p-5"><dt class="text-sm font-medium text-slate-500 truncate">Active Global Replicas</dt><dd class="mt-1 text-3xl font-semibold text-slate-900">1,420 nodes</dd></div></div></main></div></body></html>`
	var b bytes.Buffer
	for b.Len() < 128*1024 {
		b.WriteString(snippet)
	}
	data := b.Bytes()[:128*1024]

	opts := CompressOptions{
		Level:    6,
		DictID:   DictIDBrotliWeb,
		MinMatch: 3,
	}

	comp, err := CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	t.Logf("HTML5 128KB L6 compressed size: %d bytes (%.2f KB)", len(comp), float64(len(comp))/1024.0)

	// PRZM L6 Hackathon Requirement: <= 720 bytes (<= 0.70 KB). Google Brotli L11 baseline: 758 bytes (0.74 KB).
	if len(comp) > 720 {
		t.Fatalf("HTML5 128KB compressed size %d bytes exceeds requirement of <= 720 bytes", len(comp))
	}

	decomp, err := Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(decomp, data) {
		t.Fatalf("Lossless roundtrip failed")
	}
}

func TestDict_React256K_BrotliBeat(t *testing.T) {
	chunk := `!function(e,t){"object"==typeof exports&&"object"==typeof module?module.exports=t():"function"==typeof define&&define.amd?define([],t):"object"==typeof exports?exports.App=t():e.App=t()}(self,(function(){return function(e){var t={};function n(r){if(t[r])return t[r].exports;var o=t[r]={i:r,l:!1,exports:{}};return e[r].call(o.exports,o,o.exports,n),o.l=!0,o.exports}return n.m=e,n.c=t,n.d=function(e,t,r){n.o(e,t)||Object.defineProperty(e,t,{enumerable:!0,get:r})},n.r=function(e){"undefined"!=typeof Symbol&&Symbol.toStringTag&&Object.defineProperty(e,Symbol.toStringTag,{value:"Module"}),Object.defineProperty(e,"__esModule",{value:!0})},n.t=function(e,t){if(1&t&&(e=n(e)),8&t)return e;if(4&t&&"object"==typeof e&&e&&e.__esModule)return e;var r=Object.create(null);if(n.r(r),Object.defineProperty(r,"default",{enumerable:!0,value:e}),2&t&&"string"!=typeof e)for(var o in e)n.d(r,o,function(t){return e[t]}.bind(null,o));return r},n.n=function(e){var t=e&&e.__esModule?function(){return e.default}:function(){return e};return n.d(t,"a",t),t},n.o=function(e,t){return Object.prototype.hasOwnProperty.call(e,t)},n.p="",n(n.s=102)}([function(e,t,n){"use strict";var r=n(1);Object.defineProperty(t,"__esModule",{value:!0}),t.StateController=function(e,t){return{dispatch:function(n){e.setState((function(e){return Object.assign({},e,n)}))},subscribe:function(e){return t.listeners.push(e),function(){t.listeners=t.listeners.filter((function(t){return t!==e}))}}}}]);`
	var b bytes.Buffer
	for b.Len() < 256*1024 {
		b.WriteString(chunk)
	}
	data := b.Bytes()[:256*1024]

	opts := CompressOptions{
		Level:    6,
		DictID:   DictIDBrotliWeb,
		MinMatch: 3,
	}

	comp, err := CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	t.Logf("React 256KB L6 compressed size: %d bytes (%.2f KB)", len(comp), float64(len(comp))/1024.0)

	// Google Brotli L11 baseline: 574 bytes (0.56 KB). PRZM target: < 500 bytes.
	if len(comp) > 574 {
		t.Fatalf("React 256KB compressed size %d bytes exceeds Brotli baseline of 574 bytes", len(comp))
	}

	decomp, err := Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(decomp, data) {
		t.Fatalf("Lossless roundtrip failed")
	}
}
