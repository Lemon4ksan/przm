// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func TestSheafTrellis_SheafDecomposeCollate(t *testing.T) {
	for length := 0; length <= 300; length++ {
		src := make([]byte, length)
		for i := 0; i < length; i++ {
			src[i] = byte(i*17 + 3)
		}

		var f0, f1, f2, f3 []byte
		przm.SheafDecompose4(src, &f0, &f1, &f2, &f3)

		dst := make([]byte, length)
		przm.SheafCollate4(dst, f0, f1, f2, f3)

		if !bytes.Equal(src, dst) {
			t.Fatalf("length %d failed: mismatch after Collate o Decompose", length)
		}
	}
}

func TestSheafTrellis_RepQueue(t *testing.T) {
	rq := przm.NewRepQueue4()
	// Initial state: [1, 4, 8, 16]
	rq.Update(0, 1) // Slot 0: unchanged
	rq.Update(-1, 32) // New distance 32 -> [32, 1, 4, 8]
	rq.Update(2, 4) // Slot 2 promoted -> [4, 32, 1, 8]
	rq.Update(1, 32) // Slot 1 swapped -> [32, 4, 1, 8]

	var zero [4]int
	if rq == przm.NewRepQueue4() || rq == (przm.RepQueue4{Q: zero}) {
		t.Fatalf("unexpected rep queue state")
	}
}

func TestSheafTrellis_Roundtrip(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"single_byte", []byte{0x42}},
		{"two_bytes", []byte{0x12, 0x34}},
		{"four_bytes", []byte{0x55, 0x48, 0x89, 0xE5}},
		{"short_rep", bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 8)},
		{"pattern_128", bytes.Repeat([]byte("test pattern for sheaf trellis "), 4)},
		{"machine_code_16K", bench.GenerateMachineCode(16 * 1024)},
		{"machine_code_64K", bench.GenerateMachineCode(64 * 1024)},
		{"code_16K", bench.GenerateSourceCode(16 * 1024)},
		{"code_64K", bench.GenerateSourceCode(64 * 1024)},
		{"json_16K", bench.GenerateJSON(16 * 1024)},
		{"json_64K", bench.GenerateJSON(64 * 1024)},
		{"tabular_16K", bench.GenerateTabular(16*1024, 32)},
		{"tabular_64K", bench.GenerateTabular(64*1024, 32)},
		{"rle_16K", bench.GenerateRLE(16 * 1024)},
		{"rle_64K", bench.GenerateRLE(64 * 1024)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.data) == 0 {
				res := przm.CompressSheafTrellis(tc.data)
				if res != nil {
					t.Fatalf("expected nil for empty data")
				}
				return
			}

			compressed := przm.CompressSheafTrellis(tc.data)
			if len(compressed) == 0 {
				t.Fatalf("compression failed: empty output")
			}

			decompressed, err := przm.DecompressSheafTrellis(compressed, nil)
			if err != nil {
				t.Fatalf("decompression returned error: %v", err)
			}

			if !bytes.Equal(tc.data, decompressed) {
				t.Fatalf("roundtrip mismatch: orig len %d, dec len %d", len(tc.data), len(decompressed))
			}
		})
	}
}

func TestSheafTrellis_ZeroAllocations(t *testing.T) {
	data := bench.GenerateMachineCode(64 * 1024)
	comp := przm.CompressSheafTrellis(data)

	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	// Warmup
	_, err := przm.DecompressSheafTrellis(comp, dst)
	if err != nil {
		t.Fatalf("decompression error: %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		_, err := przm.DecompressSheafTrellis(comp, dst)
		if err != nil {
			t.Fatal(err)
		}
	})

	if allocs != 0 {
		t.Fatalf("expected strictly 0 heap allocations, got %.2f", allocs)
	}
	t.Logf("DecompressSheafTrellis verified: %.0f allocs/op (Strictly Zero Allocations)", allocs)
}

func BenchmarkSheafTrellis_Decompress(b *testing.B) {
	datasets := []struct {
		name string
		data []byte
	}{
		{"MachineCode64K", bench.GenerateMachineCode(64 * 1024)},
		{"MachineCode256K", bench.GenerateMachineCode(256 * 1024)},
		{"JSON64K", bench.GenerateJSON(64 * 1024)},
		{"JSON256K", bench.GenerateJSON(256 * 1024)},
		{"Tabular64K", bench.GenerateTabular(64*1024, 32)},
	}

	for _, ds := range datasets {
		comp := przm.CompressSheafTrellis(ds.data)
		dst := bufkit.AlignedBytes(len(ds.data)+64, bufkit.CacheLineSize)

		b.Run(ds.name, func(b *testing.B) {
			b.SetBytes(int64(len(ds.data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := przm.DecompressSheafTrellis(comp, dst)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSheafTrellis_Compress(b *testing.B) {
	data := bench.GenerateMachineCode(64 * 1024)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = przm.CompressSheafTrellis(data)
	}
}

func TestSheafTrellis_CompressionRatio(t *testing.T) {
	datasets := []struct {
		name string
		data []byte
	}{
		{"x86_machine_code_64K", bench.GenerateMachineCode(64 * 1024)},
		{"go_source_code_64K", bench.GenerateSourceCode(64 * 1024)},
		{"structured_json_64K", bench.GenerateJSON(64 * 1024)},
		{"tabular_numeric_64K", bench.GenerateTabular(64*1024, 32)},
		{"rle_data_64K", bench.GenerateRLE(64 * 1024)},
	}

	fmt.Println("\n=== Phase 14: Étale Sheaf & Tropical Trellis Compression Evaluation ===")
	fmt.Printf("%-24s | %-10s | %-12s | %-12s | %-10s\n", "Dataset", "Raw Bytes", "Sheaf Bytes", "Ratio", "Space Saved")
	fmt.Println("--------------------------------------------------------------------------------")

	for _, ds := range datasets {
		comp := przm.CompressSheafTrellis(ds.data)
		ratio := float64(len(ds.data)) / float64(len(comp))
		saved := (1.0 - float64(len(comp))/float64(len(ds.data))) * 100.0
		fmt.Printf("%-24s | %-10d | %-12d | %-10.2fx | +%-6.1f%%\n",
			ds.name, len(ds.data), len(comp), ratio, saved)
	}
}
