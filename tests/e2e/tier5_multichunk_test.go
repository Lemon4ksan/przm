// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
)

// Tier 5: Multi-Chunk ('PRZS') & Concurrency E2E Test Suite

// TestTier5_MultiChunk_ThreadScaling tests roundtrips through przm.CompressWithOptions
// across thread counts [1, 2, 4, 8, 16] and chunk sizes [64KB, 256KB, 1MB, 2MB] on 2 MiB / 4 MiB inputs,
// verifying bit-exact decompression with both przm.Decompress and przm.DecompressTo.
func TestTier5_MultiChunk_ThreadScaling(t *testing.T) {
	sizes := []struct {
		name string
		size int
	}{
		{"2MiB", 2 * 1024 * 1024},
		{"4MiB", 4 * 1024 * 1024},
	}

	threadCounts := []int{1, 2, 4, 8, 16}
	chunkSizes := []struct {
		name string
		size int
	}{
		{"64KB", 64 * 1024},
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
		{"2MB", 2 * 1024 * 1024},
	}

	for _, s := range sizes {
		// Generate structured compressible dataset with variable patterns
		input := make([]byte, s.size)
		pattern := []byte("PRZM_E2E_TIER5_THREAD_SCALING_TEST_PAYLOAD_PATTERN_0123456789_ABCDEF_")
		for i := range input {
			input[i] = pattern[(i*13)%len(pattern)] ^ byte((i>>10)&0xFF)
		}

		dst := make([]byte, s.size+64)

		for _, th := range threadCounts {
			for _, cs := range chunkSizes {
				testName := fmt.Sprintf("%s_Threads%d_Chunk%s", s.name, th, cs.name)
				t.Run(testName, func(t *testing.T) {
					opts := przm.CompressOptions{
						Threads:   th,
						ChunkSize: cs.size,
						Level:     1,
					}
					comp, err := przm.CompressWithOptions(input, opts)
					if err != nil {
						t.Fatalf("CompressWithOptions failed: %v", err)
					}

					// Verify expected container format
					magic := binary.LittleEndian.Uint32(comp[0:4])
					if s.size > cs.size && th > 1 {
						if magic != przm.MagicPRZS {
							t.Fatalf("expected MagicPRZS (0x%08X), got 0x%08X", przm.MagicPRZS, magic)
						}
					}

					// Verify przm.Decompress
					dec, err := przm.Decompress(comp)
					if err != nil {
						t.Fatalf("przm.Decompress failed: %v", err)
					}
					if !bytes.Equal(dec, input) {
						t.Fatalf("przm.Decompress bit-exact mismatch: got len %d, want %d", len(dec), len(input))
					}

					// Verify przm.DecompressTo
					decTo, err := przm.DecompressTo(comp, dst)
					if err != nil {
						t.Fatalf("przm.DecompressTo failed: %v", err)
					}
					if !bytes.Equal(decTo, input) {
						t.Fatalf("przm.DecompressTo bit-exact mismatch: got len %d, want %d", len(decTo), len(input))
					}
				})
			}
		}
	}
}

// TestTier5_MultiChunk_OversizedTransparentStreaming tests roundtrips of payloads > 16 MiB (e.g. 17 MiB)
// through standard public przm.Compress(src, level) and przm.Decompress(comp) without special options,
// proving transparent multi-chunk framing.
func TestTier5_MultiChunk_OversizedTransparentStreaming(t *testing.T) {
	const payloadSize = 17 * 1024 * 1024 // 17 MiB > 16 MiB MaxBlockSize
	src := make([]byte, payloadSize)
	pattern := []byte("OVERSIZED_TRANSPARENT_MULTI_CHUNK_STREAMING_TEST_1234567890_PRZM_PRZS_CONTAINER_")
	for i := range src {
		src[i] = pattern[i%len(pattern)] ^ byte((i>>14)&0x1F)
	}

	// 1. Standard public przm.Compress without special options
	comp, err := przm.Compress(src, 1)
	if err != nil {
		t.Fatalf("przm.Compress on 17 MiB payload failed: %v", err)
	}

	// 2. Validate transparent multi-chunk framing container magic
	if len(comp) < 17 {
		t.Fatalf("compressed stream too short: %d bytes", len(comp))
	}
	magic := binary.LittleEndian.Uint32(comp[0:4])
	if magic != przm.MagicPRZS {
		t.Fatalf("expected transparent MagicPRZS (0x%08X) for >16 MiB payload, got 0x%08X", przm.MagicPRZS, magic)
	}
	totalUSize := binary.LittleEndian.Uint64(comp[5:13])
	if totalUSize != uint64(payloadSize) {
		t.Fatalf("expected header TotalUncompressedSize %d, got %d", payloadSize, totalUSize)
	}
	chunkCount := binary.LittleEndian.Uint32(comp[13:17])
	if chunkCount <= 1 {
		t.Fatalf("expected chunk count > 1 for >16 MiB payload, got %d", chunkCount)
	}

	// 3. Standard public przm.Decompress without special options
	dec, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("przm.Decompress on transparent 17 MiB stream failed: %v", err)
	}
	if !bytes.Equal(dec, src) {
		t.Fatalf("przm.Decompress roundtrip mismatch on 17 MiB stream")
	}

	// 4. Verify przm.DecompressTo with pre-allocated buffer
	dst := make([]byte, payloadSize+64)
	decTo, err := przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("przm.DecompressTo on transparent 17 MiB stream failed: %v", err)
	}
	if !bytes.Equal(decTo, src) {
		t.Fatalf("przm.DecompressTo roundtrip mismatch on 17 MiB stream")
	}
}

// TestTier5_MultiChunk_ZeroAllocationDecompression tests formal testing.AllocsPerRun(100)
// verification proving strictly 0.00 dynamic heap allocations during przm.DecompressTo
// on multi-chunk PRZS streams across uneven chunk layouts.
func TestTier5_MultiChunk_ZeroAllocationDecompression(t *testing.T) {
	testCases := []struct {
		name       string
		totalSize  int
		chunkSizes []int // If specified, individual chunks packaged with BuildStreamContainer
		optChunk   int   // If >0, compressed via CompressWithOptions
	}{
		{
			name:      "Uneven_350KB_Chunk100KB",
			totalSize: 350 * 1024,
			optChunk:  100 * 1024,
		},
		{
			name:      "Uneven_200KB_Chunk64KB",
			totalSize: 200 * 1024,
			optChunk:  64 * 1024,
		},
		{
			name:      "Uneven_1234567B_Chunk250KB",
			totalSize: 1234567,
			optChunk:  250 * 1024,
		},
		{
			name:       "Heterogeneous_8_Uneven_Chunks",
			totalSize:  144 * 1024,
			chunkSizes: []int{8192, 16384, 32768, 4096, 12288, 20480, 24576, 28672},
		},
		{
			name:       "Prime_Lengths_5_Chunks",
			totalSize:  7123 + 15432 + 65536 + 12345 + 33333,
			chunkSizes: []int{7123, 15432, 65536, 12345, 33333},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var original []byte
			var compStream []byte

			if len(tc.chunkSizes) > 0 {
				var chunks [][]byte
				for idx, cSize := range tc.chunkSizes {
					cData := make([]byte, cSize)
					pattern := []byte("PRZM_UNEVEN_CHUNK_ZERO_ALLOC_TEST_PATTERN_")
					for i := range cData {
						cData[i] = pattern[(i+idx*7)%len(pattern)] ^ byte(i%251)
					}
					original = append(original, cData...)

					cComp, err := przm.Compress(cData, 1)
					if err != nil {
						t.Fatalf("chunk compress failed: %v", err)
					}
					chunks = append(chunks, cComp)
				}
				compStream = przm.BuildStreamContainer(chunks, uint64(len(original)))
			} else {
				original = make([]byte, tc.totalSize)
				pattern := []byte("PRZM_MULTI_CHUNK_ZERO_ALLOC_STREAM_DATA_")
				for i := range original {
					original[i] = pattern[(i*17)%len(pattern)] ^ byte((i>>8)&0x7F)
				}
				var err error
				compStream, err = przm.CompressWithOptions(original, przm.CompressOptions{
					Threads:   4,
					ChunkSize: tc.optChunk,
					Level:     1,
				})
				if err != nil {
					t.Fatalf("CompressWithOptions failed: %v", err)
				}
			}

			// Validate container is PRZS
			if binary.LittleEndian.Uint32(compStream[0:4]) != przm.MagicPRZS {
				t.Fatalf("expected MagicPRZS container format")
			}

			// Pre-allocate buffer according to DecompressTo invariant contract
			dst := make([]byte, len(original)+64)

			// Warmup run and correctness check
			warmup, err := przm.DecompressTo(compStream, dst)
			if err != nil {
				t.Fatalf("warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(warmup, original) {
				t.Fatalf("warmup DecompressTo bit-exact mismatch")
			}

			// Formal testing.AllocsPerRun(100) verification
			allocs := testing.AllocsPerRun(100, func() {
				out, err := przm.DecompressTo(compStream, dst)
				if err != nil {
					t.Fatalf("DecompressTo inside AllocsPerRun failed: %v", err)
				}
				_ = out
			})

			if !przm.RaceEnabled && allocs != 0.0 {
				t.Fatalf("ZERO ALLOCATION VIOLATION on %s: expected 0.00 allocs/run, got %.2f", tc.name, allocs)
			}
			t.Logf("[%s] AllocsPerRun(100) = %.2f (0.00 verified)", tc.name, allocs)
		})
	}
}

// TestTier5_MultiChunk_FormatInteroperability tests roundtrip verification confirming
// that przm.Decompress seamlessly and automatically decodes interleaved single-frame
// (MagicPRZM) and multi-chunk (MagicPRZS) streams.
func TestTier5_MultiChunk_FormatInteroperability(t *testing.T) {
	type streamSample struct {
		name          string
		expectedMagic uint32
		original      []byte
		compressed    []byte
	}

	var samples []streamSample

	// Generate alternating sequence of single-frame and multi-chunk streams
	for i := 0; i < 10; i++ {
		if i%2 == 0 {
			// Single-frame stream (MagicPRZM)
			size := 32*1024 + i*4096
			data := make([]byte, size)
			for j := range data {
				data[j] = byte((j*31 + i*17) & 0xFF)
			}
			comp, err := przm.CompressWithOptions(data, przm.CompressOptions{
				Level:              1,
				StandardStreamOnly: true,
			})
			if err != nil {
				t.Fatalf("CompressWithOptions single-frame failed: %v", err)
			}
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != przm.MagicPRZM {
				t.Fatalf("expected MagicPRZM for sample %d, got 0x%08X", i, magic)
			}
			samples = append(samples, streamSample{
				name:          fmt.Sprintf("SingleFrame_%d_%dKB", i, size/1024),
				expectedMagic: przm.MagicPRZM,
				original:      data,
				compressed:    comp,
			})
		} else {
			// Multi-chunk stream (MagicPRZS)
			size := 256*1024 + i*65536
			data := make([]byte, size)
			for j := range data {
				data[j] = byte((j*13 + i*43) & 0xFF)
			}
			comp, err := przm.CompressWithOptions(data, przm.CompressOptions{
				Threads:   4,
				ChunkSize: 64 * 1024,
				Level:     1,
			})
			if err != nil {
				t.Fatalf("CompressWithOptions multi-chunk failed: %v", err)
			}
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != przm.MagicPRZS {
				t.Fatalf("expected MagicPRZS for sample %d, got 0x%08X", i, magic)
			}
			samples = append(samples, streamSample{
				name:          fmt.Sprintf("MultiChunk_%d_%dKB", i, size/1024),
				expectedMagic: przm.MagicPRZS,
				original:      data,
				compressed:    comp,
			})
		}
	}

	// Decompress all interleaved streams sequentially and verify automatic detection
	for idx, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			magic := binary.LittleEndian.Uint32(s.compressed[0:4])
			if magic != s.expectedMagic {
				t.Fatalf("[%d: %s] unexpected stream magic: got 0x%08X, want 0x%08X",
					idx, s.name, magic, s.expectedMagic)
			}

			// 1. przm.Decompress seamlessly decodes both formats
			dec, err := przm.Decompress(s.compressed)
			if err != nil {
				t.Fatalf("[%d: %s] przm.Decompress failed: %v", idx, s.name, err)
			}
			if !bytes.Equal(dec, s.original) {
				t.Fatalf("[%d: %s] przm.Decompress output mismatch", idx, s.name)
			}

			// 2. przm.DecompressTo seamlessly decodes both formats
			dst := make([]byte, len(s.original)+64)
			decTo, err := przm.DecompressTo(s.compressed, dst)
			if err != nil {
				t.Fatalf("[%d: %s] przm.DecompressTo failed: %v", idx, s.name, err)
			}
			if !bytes.Equal(decTo, s.original) {
				t.Fatalf("[%d: %s] przm.DecompressTo output mismatch", idx, s.name)
			}
		})
	}
}
