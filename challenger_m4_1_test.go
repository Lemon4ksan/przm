// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"
	"time"

	"github.com/lemon4ksan/przm"
)

// TestChallenger_M4_1_RouterBudgetAndAllocs stress tests the router performance
// to ensure execution strictly satisfies <290 ns budget and 0 heap allocations.
func TestChallenger_M4_1_RouterBudgetAndAllocs(t *testing.T) {
	testData := [][]byte{
		[]byte("{\n  \"status\": \"ok\",\n  \"code\": 200,\n  \"users\": [\"alice\", \"bob\"]\n}"),
		make([]byte, 1024), // all zeros
		func() []byte {
			// Smooth delta
			b := make([]byte, 1024)
			for i := range b {
				b[i] = byte(i % 16)
			}
			return b
		}(),
		func() []byte {
			// PE Header
			b := make([]byte, 1024)
			b[0] = 'M'
			b[1] = 'Z'
			b[0x3C] = 0x80
			b[0x80] = 'P'
			b[0x81] = 'E'
			return b
		}(),
	}

	for idx, d := range testData {
		// Zero allocations check
		allocs := testing.AllocsPerRun(100, func() {
			_ = przm.RouteContent(d)
		})
		if allocs != 0.0 {
			t.Fatalf("[%d] RouteContent produced %f allocs, want 0.0", idx, allocs)
		}

		// Benchmark execution time
		iters := 20000
		start := time.Now()
		for i := 0; i < iters; i++ {
			_ = przm.RouteContent(d)
		}
		duration := time.Since(start)
		nsPerOp := float64(duration.Nanoseconds()) / float64(iters)
		if nsPerOp >= 290.0 {
			t.Errorf("[%d] RouteContent took %.2f ns/op, exceeding 290 ns budget", idx, nsPerOp)
		}
		t.Logf("[%d] RouteContent: %.2f ns/op, %.1f allocs/op", idx, nsPerOp, allocs)
	}
}

// TestChallenger_M4_1_ZeroHeapNormalizationDecode verifies strictly 0 heap allocations
// for DecompressTo across whole-block Hex, Base64, and mixed spans.
func TestChallenger_M4_1_ZeroHeapNormalizationDecode(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{
			name: "WholeBlockHexLower_1KB",
			data: []byte(hex.EncodeToString(bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 128))),
		},
		{
			name: "WholeBlockHexUpper_1KB",
			data: bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 128)))),
		},
		{
			name: "WholeBlockBase64Std_1KB",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("secret_payload_1"), 64))),
		},
		{
			name: "WholeBlockBase64URL_1KB",
			data: []byte(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte("secret_payload_1"), 64))),
		},
		{
			name: "MixedSpansHexAndBase64",
			data: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("{\"hex_hash\":\"")
				buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0xCA, 0xFE, 0xBA, 0xBE}, 16)))
				buf.WriteString("\",\"jwt_token\":\"")
				buf.WriteString(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte("claim_data_token"), 8)))
				buf.WriteString("\"}")
				return buf.Bytes()
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := przm.Compress(tc.data, 1)
			if err != nil {
				t.Fatalf("Compress failed: %v", err)
			}
			dst := make([]byte, len(tc.data)+64)

			// Warmup
			_, err = przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}

			allocs := testing.AllocsPerRun(20, func() {
				_, _ = przm.DecompressTo(comp, dst)
			})
			if allocs != 0.0 {
				t.Fatalf("DecompressTo produced %f allocs, want 0.0", allocs)
			}
			t.Logf("[%s] AllocsPerRun = %.2f", tc.name, allocs)
		})
	}
}

// TestChallenger_M4_1_CDecompressorNormalizationInterop tests C decompressor against
// Go-compressed frames across all normalization modes (including Base64URL with 2 '==' pads).
func TestChallenger_M4_1_CDecompressorNormalizationInterop(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
	var cBinPath string
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				cBinPath = absPath
				break
			}
		}
	}
	if cBinPath == "" {
		t.Skip("test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	testModes := []struct {
		name string
		data []byte
	}{
		{
			name: "NormModeHexLower",
			data: []byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}, 16))),
		},
		{
			name: "NormModeHexUpper",
			data: bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}, 16)))),
		},
		{
			name: "NormModeBase64Std_NoPad",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("123"), 30))), // len 90 % 3 == 0 -> no pad
		},
		{
			name: "NormModeBase64Std_Pad1",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("12345"), 16))), // len 80 % 3 == 2 -> 1 '=' pad
		},
		{
			name: "NormModeBase64Std_Pad2",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("1234"), 16))), // len 64 % 3 == 1 -> 2 '==' pad
		},
		{
			name: "NormModeBase64URL_NoPad",
			data: func() []byte {
				raw := bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 30) // produces - and _
				return []byte(base64.URLEncoding.EncodeToString(raw))
			}(),
		},
		{
			name: "NormModeBase64URL_Pad1",
			data: func() []byte {
				raw := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 25), 0xFB, 0xFF) // len % 3 == 2 -> 1 '='
				return []byte(base64.URLEncoding.EncodeToString(raw))
			}(),
		},
		{
			name: "NormModeBase64URL_Pad2",
			data: func() []byte {
				raw := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 25), 0xFB) // len % 3 == 1 -> 2 '=='
				return []byte(base64.URLEncoding.EncodeToString(raw))
			}(),
		},
		{
			name: "NormModeSpansMixed",
			data: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("{\"hash\":\"")
				buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 16)))
				buf.WriteString("\",\"payload\":\"")
				buf.WriteString(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("test_data_stream"), 8)))
				buf.WriteString("\"}")
				return buf.Bytes()
			}(),
		},
	}

	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			comp, err := przm.Compress(tm.data, 1)
			if err != nil {
				t.Fatalf("Go compress failed: %v", err)
			}
			hdr, err := przm.ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("ParseFrameHeader failed: %v", err)
			}
			t.Logf("[%s] NormMode = %d, Flags = 0x%02X", tm.name, hdr.NormMode, hdr.Flags)

			inPrzm := filepath.Join(tmpDir, tm.name+".przm")
			outBin := filepath.Join(tmpDir, tm.name+".out")
			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
			}

			decompressed, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}

			if !bytes.Equal(decompressed, tm.data) {
				t.Fatalf("Mismatch: got %d bytes, want %d bytes", len(decompressed), len(tm.data))
			}
		})
	}
}

// TestChallenger_M4_1_AdversarialSpanCorruption tests boundary robustness against corrupt span tables.
func TestChallenger_M4_1_AdversarialSpanCorruption(t *testing.T) {
	// Generate valid span-normalized frame
	var buf bytes.Buffer
	buf.WriteString("PREFIX_DATA:")
	buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0x12, 0x34, 0x56, 0x78}, 16)))
	buf.WriteString(":SUFFIX_DATA")
	data := buf.Bytes()

	comp, err := przm.Compress(data, 1)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)

	// Verify original roundtrip
	dec, err := przm.DecompressTo(comp, dst)
	if err != nil || !bytes.Equal(dec, data) {
		t.Fatalf("Initial DecompressTo failed: %v", err)
	}

	// Adversarial mutations on Section 4 / contracted stream
	for offset := 8; offset < len(comp); offset += 2 {
		corrupted := make([]byte, len(comp))
		copy(corrupted, comp)
		corrupted[offset] ^= 0xFF

		// Must not panic, must either succeed (if non-critical) or return error
		func(curOff int) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Panic on corrupt byte at %d: %v\nStack:\n%s", curOff, r, string(debug.Stack()))
				}
			}()
			_, _ = przm.DecompressTo(corrupted, dst)
		}(offset)
	}
}

func BenchmarkRouteContent_StructuredJSON(b *testing.B) {
	data := []byte("{\n  \"status\": \"ok\",\n  \"code\": 200,\n  \"users\": [\"alice\", \"bob\"]\n}")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = przm.RouteContent(data)
	}
}

func BenchmarkRouteContent_SparseZero(b *testing.B) {
	data := make([]byte, 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = przm.RouteContent(data)
	}
}

func BenchmarkRouteContent_SmoothDelta(b *testing.B) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 16)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = przm.RouteContent(data)
	}
}

