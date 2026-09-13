// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// TestPWS_Theorem7_LosslessRoundtrip verifies Theorem 7:
// Stratified H-DAG up to Layer 5 (32 bytes = 256-bit AVX store) is 100% invertible and lossless.
func TestPWS_Theorem7_LosslessRoundtrip(t *testing.T) {
	sizes := []int{16 * 1024, 64 * 1024, 256 * 1024}
	corpus := bench.GenerateCorpus(sizes)

	for _, item := range corpus {
		t.Run(item.Name, func(t *testing.T) {
			enc, err := przm.PWSEncode(item.Data)
			if err != nil {
				t.Fatalf("PWSEncode failed: %v", err)
			}

			dec, err := przm.PWSDecode(enc)
			if err != nil {
				t.Fatalf("PWSDecode failed: %v", err)
			}

			if !bytes.Equal(dec, item.Data) {
				t.Fatalf("Theorem 7 violation on %s: decoded data does not match original", item.Name)
			}
		})
	}
}

// TestPWS_Grammar5Extraction verifies that real-world JSON logs induce 16-byte and 32-byte rules.
func TestPWS_Grammar5Extraction(t *testing.T) {
	jsonData := bench.GenerateJSON(64 * 1024)
	g, tokens := przm.InduceGrammar5(jsonData)

	t.Logf("=== Induced 5-Layer AVX-Wave H-DAG on JSON (64 KiB) ===")
	t.Logf("Original size: %d -> Axiom tokens: %d (reduction: %.2fx)",
		len(jsonData), len(tokens), float64(len(jsonData))/float64(len(tokens)))
	t.Logf("Rules induced: L1(2B): %d | L2(4B): %d | L3(8B): %d | L4(16B): %d | L5(32B): %d",
		len(g.L1), len(g.L2), len(g.L3), len(g.L4), len(g.L5))

	t.Logf("\nTop 32-byte Layer 5 Rules (256-bit SIMD non-terminals):")
	for i := 0; i < len(g.L5); i++ {
		s := g.ExpandL5String(i)
		t.Logf("  L5[%2d] = %q", i, s)
	}

	t.Logf("\nTop 16-byte Layer 4 Rules (128-bit SIMD non-terminals):")
	for j := 0; j < len(g.L4); j++ {
		s := g.ExpandL4String(j)
		t.Logf("  L4[%2d] = %q", j, s)
	}

	if len(g.L4) == 0 {
		t.Errorf("expected Layer 4 (16B) rules to be induced on structured JSON")
	}
}

// TestCAST_Theorem9_Autocorrelation verifies Theorem 9 (Columnar Auto-Stride Transposition):
// automatic stride detection, orthogonal transposition, and 100% lossless inversion.
func TestCAST_Theorem9_Autocorrelation(t *testing.T) {
	tab32 := bench.GenerateTabular(64*1024, 32)
	s32, score32 := przm.DetectStride(tab32)
	t.Logf("Tabular 32B Stride Detection: S = %d (score: %.3f)", s32, score32)
	if s32 != 32 {
		t.Errorf("expected detected stride 32, got %d", s32)
	}

	trans32 := przm.TransposeStride(tab32, s32)
	untrans32 := przm.UntransposeStride(trans32, s32)
	if !bytes.Equal(untrans32, tab32) {
		t.Fatalf("Theorem 9 violation: untransposed data does not match original tabular32")
	}

	tab64 := bench.GenerateTabular(64*1024, 64)
	s64, score64 := przm.DetectStride(tab64)
	t.Logf("Tabular 64B Stride Detection: S = %d (score: %.3f)", s64, score64)
	if s64 != 64 {
		t.Errorf("expected detected stride 64, got %d", s64)
	}

	trans64 := przm.TransposeStride(tab64, s64)
	untrans64 := przm.UntransposeStride(trans64, s64)
	if !bytes.Equal(untrans64, tab64) {
		t.Fatalf("Theorem 9 violation: untransposed data does not match original tabular64")
	}

	// Random noise should detect stride 1 (no spurious transposition)
	noise := make([]byte, 16*1024)
	rand.Read(noise)
	sNoise, scoreNoise := przm.DetectStride(noise)
	t.Logf("Random Noise Stride Detection: S = %d (score: %.3f)", sNoise, scoreNoise)
	if sNoise != 1 {
		t.Errorf("expected random noise to detect stride 1, got %d", sNoise)
	}
}

func BenchmarkPWS_Throughput(b *testing.B) {
	jsonData := bench.GenerateJSON(64 * 1024)
	encJSON, err := przm.PWSEncode(jsonData)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("ZeroStack_AVXWave_Decode_JSON_64K", func(b *testing.B) {
		b.SetBytes(int64(len(jsonData)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.PWSDecode(encJSON)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	x86Data := bench.GenerateMachineCode(64 * 1024)
	encX86, _ := przm.PWSEncode(x86Data)
	b.Run("ZeroStack_AVXWave_Decode_x86_64K", func(b *testing.B) {
		b.SetBytes(int64(len(x86Data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.PWSDecode(encX86)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	tab32 := bench.GenerateTabular(64*1024, 32)
	b.Run("CAST_Transpose_32B_64K", func(b *testing.B) {
		b.SetBytes(int64(len(tab32)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = przm.TransposeStride(tab32, 32)
		}
	})

	trans32 := przm.TransposeStride(tab32, 32)
	b.Run("CAST_Untranspose_32B_64K", func(b *testing.B) {
		b.SetBytes(int64(len(tab32)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = przm.UntransposeStride(trans32, 32)
		}
	})
}
