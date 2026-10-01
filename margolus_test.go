// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestMargolusReversibility(t *testing.T) {
	rules := []uint8{
		MargolusRuleBBM,
		MargolusRuleTranspose,
		MargolusRuleRot180,
		MargolusRuleRot90,
		MargolusRuleXOR,
		MargolusRuleFlipH,
		MargolusRuleFlipV,
	}

	widths := []int{8, 16, 32, 64}
	stepCounts := []int{1, 2, 3, 4}
	lengths := []int{64, 128, 256, 1024, 75, 131, 513}

	for _, rule := range rules {
		for _, w := range widths {
			for _, steps := range stepCounts {
				for _, n := range lengths {
					if n < 2*w {
						continue
					}
					data := make([]byte, n)
					orig := make([]byte, n)
					for i := range data {
						data[i] = byte(i*17 + 31)
						orig[i] = data[i]
					}

					MargolusEvolveInplace(data, w, steps, rule)
					MargolusReverseInplace(data, w, steps, rule)

					if !bytes.Equal(data, orig) {
						t.Fatalf("Reversibility failed: rule=%d, w=%d, steps=%d, n=%d", rule, w, steps, n)
					}
				}
			}
		}
	}
}

func TestMargolusCodecRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"ShortTail", []byte("tiny_short_data")},
		{"Aligned128", bytes.Repeat([]byte("MARGOLUS_CELLULAR_AUTOMATA_BLOCK"), 4)},
		{"PseudoRandom1K", func() []byte {
			r := rand.New(rand.NewSource(123))
			buf := make([]byte, 1024)
			r.Read(buf)
			return buf
		}()},
		{"Texture8K", bytes.Repeat([]byte{0x00, 0xFF, 0x55, 0xAA, 0x12, 0x34, 0x56, 0x78}, 1024)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressMargolus(tt.data, 2)
			if err != nil {
				t.Fatalf("CompressMargolus error: %v", err)
			}

			dst := make([]byte, len(tt.data)+64)
			decompressed, err := DecompressMargolusTo(compressed, dst)
			if err != nil {
				t.Fatalf("DecompressMargolusTo error: %v", err)
			}

			if !bytes.Equal(decompressed, tt.data) {
				t.Fatalf("roundtrip mismatch: got len %d, want len %d", len(decompressed), len(tt.data))
			}
		})
	}
}

func TestDecompressMargolus_ZeroAlloc(t *testing.T) {
	src := bytes.Repeat([]byte("MARGOLUS_RCA_ZERO_ALLOCATION_DECOMPRESSION_TEST_"), 25)
	compressed, err := CompressMargolus(src, 2)
	if err != nil {
		t.Fatalf("CompressMargolus error: %v", err)
	}

	dst := make([]byte, len(src)+64)
	allocs := testing.AllocsPerRun(100, func() {
		_, err := DecompressMargolusTo(compressed, dst)
		if err != nil {
			t.Fatalf("Decompress error: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("DecompressMargolusTo allocated %f allocs/op; want strictly 0", allocs)
	}
}

func BenchmarkMargolusTransform(b *testing.B) {
	data := make([]byte, 64*1024)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MargolusEvolveInplace(data, 64, 2, MargolusRuleBBM)
	}
}

func BenchmarkMargolusDecompress(b *testing.B) {
	src := bytes.Repeat([]byte("BENCHMARK_MARGOLUS_DECOMPRESS_PAYLOAD_VERIFICATION_"), 100)
	compressed, err := CompressMargolus(src, 2)
	if err != nil {
		b.Fatalf("Compress error: %v", err)
	}

	dst := make([]byte, len(src)+64)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecompressMargolusTo(compressed, dst)
	}
}
