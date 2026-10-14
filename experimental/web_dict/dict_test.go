// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_dict

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
)

// Realistic Web Payloads for Testing & Benchmarking
const sampleHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <title>PRZM Web Dictionary Benchmark</title>
    <link rel="stylesheet" href="/assets/style.css">
</head>
<body>
    <div id="app" class="container">
        <header class="navbar">
            <span class="logo">PRZM v0.8.0</span>
            <a href="/login" class="nav-item">Login</a>
        </header>
        <div class="main-content">
            <p class="description">High-performance RFC 9651 Shared Dictionary Ingestion</p>
            <button id="submit-btn" class="btn btn-primary" type="submit">Execute</button>
        </div>
    </div>
    <script type="text/javascript" src="/assets/main.js"></script>
</body>
</html>`

const sampleCSS = `
/* PRZM Modern Web Stylesheet */
* {
    box-sizing: border-box;
    margin: 0;
    padding: 0;
}
body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    font-size: 14px;
    background-color: #f8f9fa;
    color: #212529;
}
.container {
    width: 100%;
    position: relative;
    display: flex;
    flex-direction: column;
}
.navbar {
    display: inline-block;
    background-color: #ffffff;
    border-bottom: 1px solid #dee2e6;
    padding: 10px 20px;
}
.btn-primary {
    background-color: #007bff;
    color: #ffffff;
    cursor: pointer;
    border-radius: 4px;
    transition: all 0.2s ease-in-out;
}
.btn-primary:hover {
    background-color: #0056b3;
}
`

const sampleJS = `
// PRZM Web Dictionary Async Client
import { setupRouter } from './router.js';

async function fetchData(endpoint) {
    try {
        const response = await fetch(endpoint, {
            headers: {
                "Accept": "application/json",
                "Content-Type": "application/json"
            }
        });
        const data = await response.json();
        console.log("data:", JSON.stringify(data));
        return data;
    } catch (err) {
        console.error("fetch failed:", err);
        return null;
    }
}

document.addEventListener("click", function(event) {
    const btn = document.getElementById("submit-btn");
    if (event.target === btn) {
        fetchData("/api/v1/status");
    }
});
`

const sampleJSON = `{
  "status": 200,
  "success": true,
  "message": "Resource retrieved successfully",
  "data": {
    "user_id": 104928,
    "user_name": "developer",
    "roles": ["admin", "developer"],
    "attributes": {
      "display_name": "Developer",
      "email_verified": true,
      "created_at": "2026-10-13T10:00:00Z",
      "updated_at": "2026-10-13T10:15:00Z"
    },
    "preferences": {
      "theme": "dark",
      "notifications": true,
      "language": "en-US"
    }
  }
}`

const sampleWasmText = `
(module
  (type $t0 (func (param i32 i32) (result i32)))
  (func $add (type $t0) (param $p0 i32) (param $p1 i32) (result i32)
    local.get $p0
    local.get $p1
    i32.add)
  (export "add" (func $add))
  (data (i32.const 0) "application/json")
)
`

// -----------------------------------------------------------------------------
// Test 1: RFC 9651 Structured Fields Parsing & Formatting
// -----------------------------------------------------------------------------

func TestRFC9651_ItemParserFormatter(t *testing.T) {
	// Byte Sequence
	rawBytes := []byte("przm-shared-dictionary-v1-hash")
	formattedBS := FormatItem(Item{Type: TypeByteSeq, BytesVal: rawBytes})
	if !strings.HasPrefix(formattedBS, ":") || !strings.HasSuffix(formattedBS, ":") {
		t.Fatalf("expected :base64: format, got %s", formattedBS)
	}
	parsedBS, err := ParseItem(formattedBS)
	if err != nil {
		t.Fatalf("failed to parse byte sequence: %v", err)
	}
	if !bytes.Equal(parsedBS.BytesVal, rawBytes) {
		t.Fatalf("byte sequence mismatch: got %v, want %v", parsedBS.BytesVal, rawBytes)
	}

	// String with quotes
	rawStr := `hello "przm" world`
	formattedStr := FormatItem(Item{Type: TypeString, StringVal: rawStr})
	parsedStr, err := ParseItem(formattedStr)
	if err != nil {
		t.Fatalf("failed to parse string: %v", err)
	}
	if parsedStr.StringVal != rawStr {
		t.Fatalf("string mismatch: got %q, want %q", parsedStr.StringVal, rawStr)
	}

	// Token with parameters
	itemTok := Item{
		Type:     TypeToken,
		TokenVal: "przm-dict",
		Parameters: []Parameter{
			{Key: "v", Value: Item{Type: TypeInteger, IntVal: 1}},
			{Key: "fast", Value: Item{Type: TypeBoolean, BoolVal: true}},
		},
	}
	formattedTok := FormatItem(itemTok)
	if formattedTok != "przm-dict;v=1;fast" {
		t.Fatalf("token format mismatch: got %s, want przm-dict;v=1;fast", formattedTok)
	}
	parsedTok, err := ParseItem(formattedTok)
	if err != nil {
		t.Fatalf("failed to parse token with params: %v", err)
	}
	if parsedTok.TokenVal != "przm-dict" || len(parsedTok.Parameters) != 2 {
		t.Fatalf("token parse mismatch: %+v", parsedTok)
	}
}

// -----------------------------------------------------------------------------
// Test 2: RFC 9651 Header Bindings (Use-Dictionary, Sec-Available-Dictionary, Dictionary-ID)
// -----------------------------------------------------------------------------

func TestRFC9651_HeadersBinding(t *testing.T) {
	dictHash := sha256.Sum256([]byte("brotli-rfc7932-shared-dict"))
	dictID := "przm:rfc7932:v1"

	// 1. Use-Dictionary
	useHeader := FormatUseDictionary(dictHash[:], dictID)
	parsedHash, parsedID, err := ParseUseDictionary(useHeader)
	if err != nil {
		t.Fatalf("ParseUseDictionary failed: %v", err)
	}
	if !bytes.Equal(parsedHash, dictHash[:]) {
		t.Fatalf("Use-Dictionary hash mismatch")
	}
	if parsedID != dictID {
		t.Fatalf("Use-Dictionary ID mismatch: got %s, want %s", parsedID, dictID)
	}

	// 2. Sec-Available-Dictionary
	hash2 := sha256.Sum256([]byte("second-dictionary"))
	availHeader := FormatSecAvailableDictionary([][]byte{dictHash[:], hash2[:]})
	parsedAvail, err := ParseSecAvailableDictionary(availHeader)
	if err != nil {
		t.Fatalf("ParseSecAvailableDictionary failed: %v", err)
	}
	if len(parsedAvail) != 2 || !bytes.Equal(parsedAvail[0], dictHash[:]) || !bytes.Equal(parsedAvail[1], hash2[:]) {
		t.Fatalf("Sec-Available-Dictionary parse mismatch")
	}

	// 3. Dictionary-ID
	idHeader := FormatDictionaryID(dictID)
	parsedID2, err := ParseDictionaryID(idHeader)
	if err != nil {
		t.Fatalf("ParseDictionaryID failed: %v", err)
	}
	if parsedID2 != dictID {
		t.Fatalf("Dictionary-ID mismatch: got %s, want %s", parsedID2, dictID)
	}
}

// -----------------------------------------------------------------------------
// Test 3: Dictionary Loader & H-DAG Grammar Stratification
// -----------------------------------------------------------------------------

func TestWebDictionary_LoadAndStratify(t *testing.T) {
	dict := LoadBrotliWebDictionary()
	if dict == nil {
		t.Fatal("LoadBrotliWebDictionary returned nil")
	}
	if dict.ID != "przm:rfc7932:v1" {
		t.Fatalf("unexpected dict ID: %s", dict.ID)
	}
	if len(dict.L1) < 30 {
		t.Fatalf("expected >= 30 L1 rules, got %d", len(dict.L1))
	}
	if len(dict.L2) < 20 {
		t.Fatalf("expected >= 20 L2 rules, got %d", len(dict.L2))
	}
	if len(dict.L3) < 15 {
		t.Fatalf("expected >= 15 L3 rules, got %d", len(dict.L3))
	}
	if len(dict.Phrases) < 10 {
		t.Fatalf("expected >= 10 Phrases, got %d", len(dict.Phrases))
	}

	// Check SHA-256 non-zero
	var zeroHash [32]byte
	if dict.SHA256 == zeroHash {
		t.Fatal("SHA256 is zero")
	}

	// Verify L2 expansion matches original L1 pairs
	for j, r := range dict.L2 {
		expVal := dict.ExpL2[j]
		b0 := byte(expVal)
		b1 := byte(expVal >> 8)
		b2 := byte(expVal >> 16)
		b3 := byte(expVal >> 24)

		r1a := dict.L1[r.L1A]
		r1b := dict.L1[r.L1B]
		if b0 != r1a.U || b1 != r1a.V || b2 != r1b.U || b3 != r1b.V {
			t.Fatalf("L2 expansion mismatch at %d", j)
		}
	}

	// Verify L3 expansion matches original L2 pairs
	for k, r := range dict.L3 {
		expVal := dict.ExpL3[k]
		w0 := uint32(expVal)
		w1 := uint32(expVal >> 32)
		if w0 != dict.ExpL2[r.L2A] || w1 != dict.ExpL2[r.L2B] {
			t.Fatalf("L3 expansion mismatch at %d", k)
		}
	}
	t.Logf("[PASS] Web Dictionary initialized: L1=%d, L2=%d, L3=%d, Phrases=%d, SHA256=%x",
		len(dict.L1), len(dict.L2), len(dict.L3), len(dict.Phrases), dict.SHA256[:8])
}

// -----------------------------------------------------------------------------
// Test 4: Lossless Roundtrip Compression across Diverse Web Payloads
// -----------------------------------------------------------------------------

func TestWebDictionary_RoundtripDiverse(t *testing.T) {
	dict := LoadBrotliWebDictionary()

	testCases := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"SingleByte", []byte{'X'}},
		{"HTML5", []byte(sampleHTML)},
		{"CSS3", []byte(sampleCSS)},
		{"JavaScript", []byte(sampleJS)},
		{"JSON_API", []byte(sampleJSON)},
		{"WebAssembly_Text", []byte(sampleWasmText)},
		{"RepetitiveHTML_10x", []byte(strings.Repeat(sampleHTML, 10))},
		{"RepetitiveJSON_10x", []byte(strings.Repeat(sampleJSON, 10))},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := CompressWebDict(tc.data, dict)
			if err != nil {
				t.Fatalf("CompressWebDict failed: %v", err)
			}

			dec, err := DecompressWebDict(comp, dict)
			if err != nil {
				t.Fatalf("DecompressWebDict failed: %v", err)
			}

			if !bytes.Equal(dec, tc.data) {
				t.Fatalf("Decompressed data mismatch! Got %d bytes, want %d bytes", len(dec), len(tc.data))
			}

			origLen := len(tc.data)
			compLen := len(comp)
			ratio := 1.0
			if compLen > 0 {
				ratio = float64(origLen) / float64(compLen)
			}
			t.Logf("[%s] Raw=%d B, Comp=%d B, Ratio=%.2fx", tc.name, origLen, compLen, ratio)
		})
	}
}

// -----------------------------------------------------------------------------
// Test 5: Zero Distance Overhead & Superior Compression on Small Web Payloads
// -----------------------------------------------------------------------------

func TestWebDictionary_ZeroDistanceOverhead(t *testing.T) {
	dict := LoadBrotliWebDictionary()

	// An 8-byte token like "function"
	input := []byte("functionfunctionfunctionfunction")
	comp, err := CompressWebDict(input, dict)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	// 26-byte header + 4 tokens (each 8-byte rule emitted as 1 byte: 0xE0 + id)
	axiom := comp[26:]
	if len(axiom) != 4 {
		t.Fatalf("expected exactly 4 axiom bytes for 4x 8-byte matches (zero distance), got %d", len(axiom))
	}
	for i, op := range axiom {
		if op < 0xE0 || op > 0xEF {
			t.Fatalf("axiom token %d (0x%02X) not in direct L3 range 0xE0..0xEF", i, op)
		}
	}
	t.Logf(
		"[PASS] Zero-distance confirmed: 32 raw bytes compressed into exactly 4 axiom bytes (8.0x token compression)",
	)
}

// -----------------------------------------------------------------------------
// Test 6: Zero Allocation Decompression Verification
// -----------------------------------------------------------------------------

func TestWebDictionary_ZeroAllocationDecompression(t *testing.T) {
	dict := LoadBrotliWebDictionary()
	input := []byte(strings.Repeat(sampleHTML, 5))

	comp, err := CompressWebDict(input, dict)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	dst := make([]byte, len(input)+64)

	// Warmup
	_, err = DecompressWebDictTo(comp, dst, dict)
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := DecompressWebDictTo(comp, dst, dict)
		if err != nil {
			t.Fatalf("DecompressWebDictTo failed: %v", err)
		}
	})

	if allocs != 0.00 {
		t.Fatalf(
			"DecompressWebDictTo violated 0-allocation guarantee: got %.2f allocs/op (expected strictly 0)",
			allocs,
		)
	}
	t.Logf("[PASS] Zero-Allocation Decompression Verified: %.4f allocs/op", allocs)
}

// -----------------------------------------------------------------------------
// Test 7: Security Bounds & Dictionary Mismatch Rejection
// -----------------------------------------------------------------------------

func TestWebDictionary_SecurityDigestMismatch(t *testing.T) {
	dict := LoadBrotliWebDictionary()
	input := []byte(sampleJSON)

	comp, err := CompressWebDict(input, dict)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	// Tamper with the 8-byte truncated DictID digest in header
	corruptComp := make([]byte, len(comp))
	copy(corruptComp, comp)
	corruptComp[12] ^= 0xFF

	_, err = DecompressWebDict(corruptComp, dict)
	if !errors.Is(err, ErrDictionaryMismatch) {
		t.Fatalf("expected ErrDictionaryMismatch, got %v", err)
	}
	t.Logf("[PASS] Tampered dictionary digest rejected immediately at header parsing")
}

// -----------------------------------------------------------------------------
// Benchmarks
// -----------------------------------------------------------------------------

func BenchmarkCompress_HTML(b *testing.B) {
	dict := LoadBrotliWebDictionary()
	payload := []byte(strings.Repeat(sampleHTML, 10))
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := CompressWebDict(payload, dict)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompress_HTML_ZeroAlloc(b *testing.B) {
	dict := LoadBrotliWebDictionary()
	payload := []byte(strings.Repeat(sampleHTML, 10))
	comp, err := CompressWebDict(payload, dict)
	if err != nil {
		b.Fatal(err)
	}

	dst := make([]byte, len(payload)+64)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := DecompressWebDictTo(comp, dst, dict)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCompress_JSON(b *testing.B) {
	dict := LoadBrotliWebDictionary()
	payload := []byte(strings.Repeat(sampleJSON, 10))
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := CompressWebDict(payload, dict)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompress_JSON_ZeroAlloc(b *testing.B) {
	dict := LoadBrotliWebDictionary()
	payload := []byte(strings.Repeat(sampleJSON, 10))
	comp, err := CompressWebDict(payload, dict)
	if err != nil {
		b.Fatal(err)
	}

	dst := make([]byte, len(payload)+64)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := DecompressWebDictTo(comp, dst, dict)
		if err != nil {
			b.Fatal(err)
		}
	}
}
