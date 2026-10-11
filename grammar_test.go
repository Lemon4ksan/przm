// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func TestGGTC_GrammarExtraction(t *testing.T) {
	jsonData := bench.GenerateJSON(64 * 1024)
	g, tokens := przm.InduceGrammar(jsonData)

	t.Logf("=== Induced Stratified H-DAG Grammar on JSON (64 KiB) ===")
	t.Logf("Original size: %d bytes -> Axiom tokens: %d (reduction: %.2fx)",
		len(jsonData), len(tokens), float64(len(jsonData))/float64(len(tokens)))
	t.Logf("Rules induced: Layer 1 (2B): %d | Layer 2 (4B): %d | Layer 3 (8B): %d",
		len(g.L1), len(g.L2), len(g.L3))

	t.Logf("\nTop 8-byte Layer 3 Syntactic Rules (64-bit non-terminals):")
	for i := 0; i < len(g.L3); i++ {
		s := g.ExpandL3String(i)
		t.Logf("  L3[%2d] = %q (0x%016X)", i, s, g.ExpL3[i])
	}

	t.Logf("\nTop 4-byte Layer 2 Syntactic Rules (32-bit non-terminals):")
	for j := 0; j < len(g.L2) && j < 10; j++ {
		s := g.ExpandL2String(j)
		t.Logf("  L2[%2d] = %q (0x%08X)", j, s, g.ExpL2[j])
	}

	if len(g.L3) == 0 {
		t.Errorf("expected Layer 3 8-byte rules to be induced on structured JSON")
	}
}

func TestGGTC_Roundtrip(t *testing.T) {
	// 1. Edge cases
	edgeCases := [][]byte{
		{},
		{0x42},
		{0x01, 0x02},
		[]byte("abcdefgh"),
		[]byte("Structured Grammar-Graph Tensor Compression (GGTC) Lossless Test"),
		bytes.Repeat([]byte("{\"status\":\"ok\",\"code\":200}\n"), 50),
		bytes.Repeat([]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, 64),
	}

	for i, ec := range edgeCases {
		t.Run(fmt.Sprintf("edge_%d", i), func(t *testing.T) {
			enc, err := przm.GGTCEncode(ec)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			dec, err := przm.GGTCDecode(enc)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			if !bytes.Equal(dec, ec) {
				t.Fatalf("mismatch: got %d bytes, want %d bytes", len(dec), len(ec))
			}
		})
	}

	// 2. Standard corpora across all sizes
	sizes := []int{16 * 1024, 64 * 1024, 256 * 1024}
	corpus := bench.GenerateCorpus(sizes)

	for _, item := range corpus {
		t.Run(item.Name, func(t *testing.T) {
			enc, err := przm.GGTCEncode(item.Data)
			if err != nil {
				t.Fatalf("encode failed on %s: %v", item.Name, err)
			}

			dec, err := przm.GGTCDecode(enc)
			if err != nil {
				t.Fatalf("decode failed on %s: %v", item.Name, err)
			}

			if !bytes.Equal(dec, item.Data) {
				t.Fatalf("lossless violation on %s: decoded data does not match original", item.Name)
			}
		})
	}

	// 3. Random noise
	t.Run("random_noise_64K", func(t *testing.T) {
		noise := make([]byte, 64*1024)
		rand.Read(noise)
		enc, err := przm.GGTCEncode(noise)
		if err != nil {
			t.Fatalf("encode random failed: %v", err)
		}
		dec, err := przm.GGTCDecode(enc)
		if err != nil {
			t.Fatalf("decode random failed: %v", err)
		}
		if !bytes.Equal(dec, noise) {
			t.Fatalf("lossless violation on random noise")
		}
	})
}

func TestGGTC_CompressionRatio(t *testing.T) {
	sizes := []int{16 * 1024, 64 * 1024, 256 * 1024}
	corpus := bench.GenerateCorpus(sizes)

	t.Logf("\n%-18s | %-10s | %-12s | %-10s | %-14s | %-10s",
		"Dataset", "Raw Bytes", "GGTC Bytes", "GGTC Ratio", "GGTC+VANS Bytes", "Comb Ratio")
	t.Logf("---------------------------------------------------------------------------------------------")

	for _, item := range corpus {
		// Pure GGTC
		encGGTC, err := przm.GGTCEncode(item.Data)
		if err != nil {
			t.Fatalf("GGTC encode failed: %v", err)
		}
		ratioGGTC := float64(len(item.Data)) / float64(len(encGGTC))

		// Synergy: GGTC + V-ANS
		encComb, err := przm.VANSEncode4x(encGGTC)
		if err != nil {
			t.Fatalf("V-ANS on GGTC failed: %v", err)
		}
		ratioComb := float64(len(item.Data)) / float64(len(encComb))

		t.Logf("%-18s | %-10d | %-12d | %-10.2fx | %-14d | %-10.2fx",
			item.Name, len(item.Data), len(encGGTC), ratioGGTC, len(encComb), ratioComb)
	}
}

func BenchmarkGGTC_Throughput(b *testing.B) {
	data := bench.GenerateJSON(64 * 1024)

	b.Run("Encode_JSON_64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.GGTCEncode(data)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	enc, _ := przm.GGTCEncode(data)
	b.Run("ZeroStack_Decode_JSON_64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.GGTCDecode(enc)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	x86Data := bench.GenerateMachineCode(64 * 1024)
	encX86, _ := przm.GGTCEncode(x86Data)
	b.Run("ZeroStack_Decode_x86_64K", func(b *testing.B) {
		b.SetBytes(int64(len(x86Data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.GGTCDecode(encX86)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	tabData := bench.GenerateTabular(64*1024, 64)
	encTab, _ := przm.GGTCEncode(tabData)
	b.Run("ZeroStack_Decode_Tabular64_64K", func(b *testing.B) {
		b.SetBytes(int64(len(tabData)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.GGTCDecode(encTab)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
