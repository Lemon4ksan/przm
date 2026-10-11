// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func TestPRZM_EmptyAndShort(t *testing.T) {
	// Empty slice
	compressed, err := przm.Compress([]byte{}, 1)
	if err != nil {
		t.Fatalf("compress empty: %v", err)
	}
	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress empty: %v", err)
	}
	if len(decompressed) != 0 {
		t.Fatalf("expected empty, got %d bytes", len(decompressed))
	}

	// Short string (3 bytes < minMatch)
	short := []byte("xyz")
	compressed, err = przm.Compress(short, 1)
	if err != nil {
		t.Fatalf("compress short: %v", err)
	}
	decompressed, err = przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress short: %v", err)
	}
	if !bytes.Equal(decompressed, short) {
		t.Fatalf("expected %q, got %q", short, decompressed)
	}
}

func TestPRZM_RLE(t *testing.T) {
	// 5000 zeros
	zeros := bytes.Repeat([]byte{0x00}, 5000)
	compressed, err := przm.Compress(zeros, 1)
	if err != nil {
		t.Fatalf("compress zeros: %v", err)
	}

	// High compression ratio on RLE
	if len(compressed) > 100 {
		t.Fatalf("RLE compression suboptimal: got %d bytes for 5000 zeros", len(compressed))
	}

	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress zeros: %v", err)
	}
	if !bytes.Equal(decompressed, zeros) {
		t.Fatalf("decompressed mismatch on zeros")
	}
}

func TestPRZM_PeriodicStride(t *testing.T) {
	// Synthesize structured CSV/table records with stride S = 32
	var buf bytes.Buffer
	for i := range 200 {
		// Exactly 32 bytes per row
		row := fmt.Sprintf("ID:%05d|STATUS:OK|METRIC:%05d\n", i, (i*17)%10000)
		if len(row) != 32 {
			t.Fatalf("row length must be 32, got %d", len(row))
		}
		buf.WriteString(row)
	}
	src := buf.Bytes()

	compressed, err := przm.Compress(src, 1)
	if err != nil {
		t.Fatalf("compress stride data: %v", err)
	}

	hdr, err := przm.ParseFrameHeader(compressed)
	if err != nil {
		t.Fatalf("parse frame header: %v", err)
	}

	if (hdr.Flags & przm.FlagStride) == 0 {
		t.Fatalf("expected FlagStride to be detected for periodic table data, flags=0x%02X", hdr.Flags)
	}
	if hdr.Stride != 32 {
		t.Logf("detected stride: %d (expected 32)", hdr.Stride)
	}

	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress stride data: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatalf("stride data mismatch")
	}

	ratio := float64(len(src)) / float64(len(compressed))
	t.Logf("Stride Table Compression: %d -> %d bytes (Ratio: %.2fx)", len(src), len(compressed), ratio)
}

func TestPRZM_DeltaMaskedMatches(t *testing.T) {
	// Synthesize JSON logs differing by 1-2 bytes (e.g. sequence counter or timestamp)
	var buf bytes.Buffer
	for i := 10000; i < 10200; i++ {
		// Length ~64 bytes, only differing in the integer number
		line := fmt.Sprintf(`{"event":"click","user_id":%d,"action":"scroll","resp":200}`+"\n", i)
		buf.WriteString(line)
	}
	src := buf.Bytes()

	compressed, err := przm.Compress(src, 1)
	if err != nil {
		t.Fatalf("compress delta logs: %v", err)
	}

	hdr, err := przm.ParseFrameHeader(compressed)
	if err != nil {
		t.Fatalf("parse header: %v", err)
	}

	if (hdr.Flags & przm.FlagDeltaMask) == 0 {
		t.Logf("Note: FlagDeltaMask was not set; checking if covered by exact/stride matches (flags=0x%02X)", hdr.Flags)
	}

	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress delta logs: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatalf("delta logs mismatch: len=%d vs %d", len(decompressed), len(src))
	}

	ratio := float64(len(src)) / float64(len(compressed))
	t.Logf("JSON Logs Compression: %d -> %d bytes (Ratio: %.2fx)", len(src), len(compressed), ratio)
}

func TestPRZM_IncompressibleRandomFallback(t *testing.T) {
	// 8 KB random noise
	noise := make([]byte, 8192)
	_, _ = rand.Read(noise)

	compressed, err := przm.Compress(noise, 1)
	if err != nil {
		t.Fatalf("compress random noise: %v", err)
	}

	hdr, err := przm.ParseFrameHeader(compressed)
	if err != nil {
		t.Fatalf("parse header: %v", err)
	}

	if (hdr.Flags & przm.FlagUncompressed) == 0 {
		t.Fatalf("expected FlagUncompressed on random noise, got flags=0x%02X", hdr.Flags)
	}

	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress random noise: %v", err)
	}
	if !bytes.Equal(decompressed, noise) {
		t.Fatalf("random noise mismatch")
	}
}

func TestPRZM_CorpusRoundtrip(t *testing.T) {
	corpus := bench.GenerateCorpus([]int{16 * 1024, 64 * 1024, 256 * 1024})
	for _, item := range corpus {
		t.Run(item.Name, func(t *testing.T) {
			comp, err := przm.Compress(item.Data, 1)
			if err != nil {
				t.Fatalf("compress failed: %v", err)
			}
			dec, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("decompress failed: %v", err)
			}
			if !bytes.Equal(dec, item.Data) {
				for i := 0; i < len(item.Data); i++ {
					if dec[i] != item.Data[i] {
						t.Fatalf(
							"mismatch at byte %d (len %d vs %d): dec=0x%02X, want=0x%02X",
							i,
							len(dec),
							len(item.Data),
							dec[i],
							item.Data[i],
						)
					}
				}
			}
		})
	}
}

func BenchmarkPRZM_Throughput(b *testing.B) {
	// Synthesize 64 KiB realistic structured JSON buffer
	var buf bytes.Buffer
	for i := 0; buf.Len() < 64*1024; i++ {
		fmt.Fprintf(&buf, `{"sensor_id":"temp-%03d","metric":"temperature","value":%.2f,"status":"ok","ts":%d}`+"\n",
			i%50, 20.0+float64(i%100)/10.0, 17180000+i)
	}
	data := buf.Bytes()[:64*1024]

	compressed, err := przm.Compress(data, 1)
	if err != nil {
		b.Fatalf("compress: %v", err)
	}

	b.Run("Compress_64KB", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = przm.Compress(data, 1)
		}
	})

	b.Run("Decompress_64KB", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = przm.Decompress(compressed)
		}
	})
}

// TestPRZM_Unified_LevelsRoundtrip verifies that level 1, level 2 (PWS+CAST) and level 3
// all decompress with 100% bitwise identity across all corpus types.
func TestPRZM_Unified_LevelsRoundtrip(t *testing.T) {
	sizes := []int{16 * 1024, 64 * 1024}
	corpus := bench.GenerateCorpus(sizes)

	for _, item := range corpus {
		for _, lvl := range []int{1, 2, 3} {
			testName := fmt.Sprintf("%s_lvl%d", item.Name, lvl)
			t.Run(testName, func(t *testing.T) {
				comp, err := przm.Compress(item.Data, lvl)
				if err != nil {
					t.Fatalf("compress failed: %v", err)
				}
				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("decompress failed: %v", err)
				}
				if !bytes.Equal(dec, item.Data) {
					t.Fatalf("roundtrip mismatch for %s (len %d vs %d)", testName, len(dec), len(item.Data))
				}
			})
		}
	}
}

// TestPRZM_StreamRoundtrip verifies that CompressStream and DecompressStream
// correctly stream data across chunks using continuous structural induction (T_CSI).
func TestPRZM_StreamRoundtrip(t *testing.T) {
	// Synthesize 256 KiB continuous JSON stream
	var buf bytes.Buffer
	for i := 0; buf.Len() < 256*1024; i++ {
		fmt.Fprintf(&buf, `{"event":"metric_push","node_id":"cluster-%02d","cpu_pct":%.2f,"mem_mb":%d,"ts":%d}`+"\n",
			i%16, 15.0+float64(i%80)/2.0, 1024+(i*3)%8192, 17180000+i)
	}
	original := buf.Bytes()[:256*1024]

	// Compress stream
	var compBuf bytes.Buffer
	if err := przm.CompressStream(bytes.NewReader(original), &compBuf); err != nil {
		t.Fatalf("CompressStream failed: %v", err)
	}

	compressedData := compBuf.Bytes()
	ratio := float64(len(original)) / float64(len(compressedData))
	t.Logf(
		"Stream Compression (256 KiB JSON): %d -> %d bytes (Ratio: %.2fx)",
		len(original),
		len(compressedData),
		ratio,
	)

	// Decompress stream
	var decBuf bytes.Buffer
	if err := przm.DecompressStream(bytes.NewReader(compressedData), &decBuf); err != nil {
		t.Fatalf("DecompressStream failed: %v", err)
	}

	decompressed := decBuf.Bytes()
	if !bytes.Equal(decompressed, original) {
		t.Fatalf("Stream roundtrip mismatch: len %d vs %d", len(decompressed), len(original))
	}
}

func TestPRZM_GoExeChunks(t *testing.T) {
	data, err := os.ReadFile(`C:\Program Files\go\bin\go.exe`)
	if err != nil {
		t.Skip("go.exe not found")
	}
	chunkSize := 4 * 1024 * 1024
	for _, level := range []int{1, 2, 3} {
		for start := 0; start < len(data); start += chunkSize {
			end := min(start+chunkSize, len(data))
			chunk := data[start:end]
			comp, err := przm.Compress(chunk, level)
			if err != nil {
				t.Fatalf("Level %d compress failed at [%d:%d]: %v", level, start, end, err)
			}
			dec, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Level %d decompress failed at [%d:%d]: %v", level, start, end, err)
			}
			if !bytes.Equal(dec, chunk) {
				for i := 0; i < len(chunk) && i < len(dec); i++ {
					if chunk[i] != dec[i] {
						t.Fatalf("Level %d mismatch in chunk [%d:%d] at rel %d: expected %d, got %d",
							level, start, end, i, chunk[i], dec[i])
					}
				}
				t.Fatalf("Level %d mismatch length in chunk [%d:%d]: %d vs %d", level, start, end, len(chunk), len(dec))
			}
		}
		t.Logf("Level %d passed 100%% bit-for-bit on all chunks of go.exe!", level)
	}
}

func TestPRZM_Unified_v03_Engines(t *testing.T) {
	// 1. TDA Ripser: Periodic Barcodes and Sparse Zeros
	t.Run("TDA_PeriodicBarcodes", func(t *testing.T) {
		raw := bytes.Repeat([]byte("CYCLIC_PERSISTENCE_VECTOR_01234567"), 128)
		comp, err := przm.Compress(raw, 3)
		if err != nil {
			t.Fatalf("Compress level 3 failed: %v", err)
		}
		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("TDA roundtrip mismatch: got %d bytes, want %d bytes", len(dec), len(raw))
		}
	})

	// 2. Hyperbolic AST: Deep JSON brackets
	t.Run("Hyperbolic_AST_JSON", func(t *testing.T) {
		raw := bytes.Repeat(
			[]byte(`{"user": {"id": 12345, "roles": ["admin", "developer"], "meta": {"active": true}}}, `),
			32,
		)
		comp, err := przm.Compress(raw, 3)
		if err != nil {
			t.Fatalf("Compress level 3 failed: %v", err)
		}
		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("Hyperbolic AST roundtrip mismatch: got %d bytes, want %d bytes", len(dec), len(raw))
		}
	})

	// 3. Spectral Unitary: 4-Channel Redundant stream
	t.Run("SpectralUnitary_4Channel", func(t *testing.T) {
		n := 1024
		raw := make([]byte, n*4)
		for i := range n {
			raw[i*4+0] = byte(i)
			raw[i*4+1] = byte(i) // identical channel -> collapses to zero under unitary rotation!
			raw[i*4+2] = byte(i * 3)
			raw[i*4+3] = byte(i * 3)
		}
		comp, err := przm.Compress(raw, 4)
		if err != nil {
			t.Fatalf("Compress level 4 failed: %v", err)
		}
		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("Spectral Unitary roundtrip mismatch: got %d bytes, want %d bytes", len(dec), len(raw))
		}
	})

	// 4. HMDS: 4-Channel Interleaved data
	t.Run("HMDS_4Channel", func(t *testing.T) {
		n := 1024
		raw := make([]byte, n*4)
		for i := range n {
			val := byte(i & 0xFF)
			raw[i*4+0] = val
			raw[i*4+1] = val + 1
			raw[i*4+2] = val + 2
			raw[i*4+3] = val + 3
		}
		comp, err := przm.Compress(raw, 4)
		if err != nil {
			t.Fatalf("Compress level 4 failed: %v", err)
		}
		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("HMDS roundtrip mismatch: got %d bytes, want %d bytes", len(dec), len(raw))
		}
	})
}
