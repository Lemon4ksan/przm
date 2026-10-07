// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lemon4ksan/przm"
)

func findCDecoderM5(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// buildPaddedBlock repeats/truncates sample content to exactly 65,536 bytes.
func buildPaddedBlock(content []byte) []byte {
	block := make([]byte, przm.DedupBlockSize)
	if len(content) == 0 {
		return block
	}
	copied := 0
	for copied < przm.DedupBlockSize {
		n := copy(block[copied:], content)
		copied += n
	}
	return block
}

// TestChallenger_M5_2_CrossLanguage_Dedup_Parity validates 100% bit-exact decode parity
// between Go encoder (levels 1 and 3) and Pure C decompressor (test_przm_dec.exe)
// across diverse multi-block payloads exercising 64 KiB deduplication.
func TestChallenger_M5_2_CrossLanguage_Dedup_Parity(t *testing.T) {
	cBinPath := findCDecoderM5(t)
	tmpDir := t.TempDir()

	testCases := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "SourceCode_MultiBlock_4x64K_PlusTail",
			gen: func() []byte {
				// Block 0: Go source code pattern
				var goSrc bytes.Buffer
				for i := 0; i < 250; i++ {
					fmt.Fprintf(&goSrc, `package przm_pipeline_%03d
import (
	"context"
	"sync"
)
type DedupProcessor%03d struct {
	mu sync.Mutex
	id uint64
}
func (p *DedupProcessor%03d) Process(ctx context.Context, blk []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return blk, nil
}
`, i, i, i)
				}
				b0 := buildPaddedBlock(goSrc.Bytes())

				// Block 2: C source code pattern
				var cSrc bytes.Buffer
				for i := 0; i < 250; i++ {
					fmt.Fprintf(&cSrc, `/* Block dedup C routine %03d */
#include <stdint.h>
#include <string.h>
int przm_dec_block_copy_%03d(uint8_t* dst, const uint8_t* src, size_t len) {
    if (!dst || !src) return -1;
    memcpy(dst, src, len);
    return 0;
}
`, i, i)
				}
				b2 := buildPaddedBlock(cSrc.Bytes())

				// Construct: B0 (Go), B1 (copy B0), B2 (C), B3 (copy B0), plus 1024-byte tail
				var payload bytes.Buffer
				payload.Write(b0)            // Block 0
				payload.Write(b0)            // Block 1 == Block 0 (Dedup match)
				payload.Write(b2)            // Block 2 (Distinct)
				payload.Write(b0)            // Block 3 == Block 0 (Dedup match)
				payload.Write(b2[:1024])     // Tail (non-aligned tail)
				return payload.Bytes()
			},
		},
		{
			name: "StructuredJSON_MultiBlock_5x64K",
			gen: func() []byte {
				// Block 0: Structured telemetry JSON lines
				var jsonBufA bytes.Buffer
				for i := 0; i < 400; i++ {
					fmt.Fprintf(&jsonBufA, `{"event_id":%d,"service":"telemetry-router","status":"OK","metrics":{"cpu":%.2f,"mem":%d},"ts":"2026-10-07T12:00:%02dZ"}`+"\n",
						i, 12.5+float64(i%20)*0.5, 1048576*(i%16), i%60)
				}
				b0 := buildPaddedBlock(jsonBufA.Bytes())

				// Block 2: Distinct structured audit JSON lines
				var jsonBufB bytes.Buffer
				for i := 0; i < 400; i++ {
					fmt.Fprintf(&jsonBufB, `{"audit_seq":%d,"action":"AUTHENTICATE","user":"admin_%03d","granted":true,"roles":["reader","writer","admin"],"scope":"cluster-west"}`+"\n",
						i, i%50)
				}
				b2 := buildPaddedBlock(jsonBufB.Bytes())

				// Construct: B0, B1 (copy B0), B2, B3 (copy B0), B4 (copy B2)
				var payload bytes.Buffer
				payload.Write(b0) // Block 0
				payload.Write(b0) // Block 1 == Block 0 (Dedup)
				payload.Write(b2) // Block 2
				payload.Write(b0) // Block 3 == Block 0 (Dedup)
				payload.Write(b2) // Block 4 == Block 2 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "BinaryBuffers_MultiBlock_6x64K",
			gen: func() []byte {
				// Block 0: Seeded binary pseudo-random with structured header
				rng0 := rand.New(rand.NewSource(0x1337_0001))
				b0 := make([]byte, przm.DedupBlockSize)
				rng0.Read(b0)
				copy(b0[:16], []byte("PRZM_BIN_BLOCK_0"))

				// Block 2: Distinct seeded binary block
				rng2 := rand.New(rand.NewSource(0x1337_0002))
				b2 := make([]byte, przm.DedupBlockSize)
				rng2.Read(b2)
				copy(b2[:16], []byte("PRZM_BIN_BLOCK_2"))

				// Construct: B0, B1 (copy B0), B2, B3 (copy B0), B4 (copy B2), B5 (copy B0)
				var payload bytes.Buffer
				payload.Write(b0) // Block 0
				payload.Write(b0) // Block 1 == Block 0 (Dedup)
				payload.Write(b2) // Block 2
				payload.Write(b0) // Block 3 == Block 0 (Dedup)
				payload.Write(b2) // Block 4 == Block 2 (Dedup)
				payload.Write(b0) // Block 5 == Block 0 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Identical_2x64K",
			gen: func() []byte {
				block := make([]byte, przm.DedupBlockSize)
				for i := range block {
					block[i] = byte(i*13 + 7)
				}
				var payload bytes.Buffer
				payload.Write(block)
				payload.Write(block)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Identical_3x64K",
			gen: func() []byte {
				block := make([]byte, przm.DedupBlockSize)
				for i := range block {
					block[i] = byte((i * 17) ^ (i >> 8))
				}
				var payload bytes.Buffer
				payload.Write(block)
				payload.Write(block)
				payload.Write(block)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Identical_8x64K",
			gen: func() []byte {
				block := make([]byte, przm.DedupBlockSize)
				for i := range block {
					block[i] = byte(i*31 + 101)
				}
				var payload bytes.Buffer
				for range 8 {
					payload.Write(block)
				}
				return payload.Bytes()
			},
		},
		{
			name: "NearIdentical_1ByteDelta_3x64K",
			gen: func() []byte {
				// Block 0: Base pattern
				b0 := make([]byte, przm.DedupBlockSize)
				for i := range b0 {
					b0[i] = byte(i*19 + 3)
				}
				// Block 1: 1 byte flipped at offset 32768 (must NOT dedup)
				b1 := make([]byte, przm.DedupBlockSize)
				copy(b1, b0)
				b1[32768] ^= 0xFF

				// Block 2: Exact copy of Block 0 (must dedup against Block 0)
				var payload bytes.Buffer
				payload.Write(b0)
				payload.Write(b1)
				payload.Write(b0)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Alternating_4x64K",
			gen: func() []byte {
				// Block A: Pattern 1
				bA := make([]byte, przm.DedupBlockSize)
				for i := range bA {
					bA[i] = byte((i * 41) ^ 0x3C)
				}
				// Block B: Pattern 2
				bB := make([]byte, przm.DedupBlockSize)
				for i := range bB {
					bB[i] = byte((i * 73) ^ 0xC3)
				}
				var payload bytes.Buffer
				payload.Write(bA) // Block 0
				payload.Write(bB) // Block 1
				payload.Write(bA) // Block 2 == Block 0 (Dedup)
				payload.Write(bB) // Block 3 == Block 1 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Interleaved_5x64K",
			gen: func() []byte {
				b0 := make([]byte, przm.DedupBlockSize)
				b1 := make([]byte, przm.DedupBlockSize)
				b2 := make([]byte, przm.DedupBlockSize)
				for i := range przm.DedupBlockSize {
					b0[i] = byte((i * 11) + 1)
					b1[i] = byte((i * 29) + 3)
					b2[i] = byte((i * 47) + 5)
				}
				var payload bytes.Buffer
				payload.Write(b0) // Block 0
				payload.Write(b1) // Block 1
				payload.Write(b2) // Block 2
				payload.Write(b0) // Block 3 == Block 0 (Dedup)
				payload.Write(b1) // Block 4 == Block 1 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_HighEntropy_4x64K",
			gen: func() []byte {
				rng := rand.New(rand.NewSource(0xDEAD_BEEF))
				block := make([]byte, przm.DedupBlockSize)
				rng.Read(block)
				var payload bytes.Buffer
				for range 4 {
					payload.Write(block)
				}
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Structured_16x64K_1MB",
			gen: func() []byte {
				block := make([]byte, przm.DedupBlockSize)
				for i := range block {
					block[i] = byte((i * 19) ^ (i >> 4) ^ 0xA5)
				}
				var payload bytes.Buffer
				for range 16 {
					payload.Write(block)
				}
				return payload.Bytes()
			},
		},
	}

	levels := []int{1, 3}

	for _, tc := range testCases {
		raw := tc.gen()
		if len(raw) < 2*przm.DedupBlockSize {
			t.Fatalf("[%s] Test payload must be >= 128 KiB to exercise deduplication, got %d", tc.name, len(raw))
		}

		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Lvl%d", tc.name, lvl)
			t.Run(testName, func(t *testing.T) {
				// 1. Compress with Go encoder
				comp, err := przm.Compress(raw, lvl)
				if err != nil {
					t.Fatalf("przm.Compress(lvl %d) failed: %v", lvl, err)
				}
				if len(comp) == 0 {
					t.Fatalf("przm.Compress returned empty compressed frame")
				}

				t.Logf("[%s] comp len=%d, header=%x", testName, len(comp), comp[:min(len(comp), 32)])
				// 2. Go roundtrip verification (Decompress and DecompressTo)
				goDec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("Go Decompress failed: %v", err)
				}
				if !bytes.Equal(goDec, raw) {
					t.Fatalf("Go Decompress output does not match original data!")
				}

				dstBuf := make([]byte, len(raw)+64)
				decTo, err := przm.DecompressTo(comp, dstBuf)
				if err != nil {
					t.Fatalf("Go DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decTo, raw) {
					t.Fatalf("Go DecompressTo output does not match original data!")
				}

				// 3. Write compressed frame to temp file
				inPrzm := filepath.Join(tmpDir, testName+".przm")
				outBin := filepath.Join(tmpDir, testName+".out")
				if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
					t.Fatalf("WriteFile failed: %v", err)
				}

				// 4. Invoke Pure C reference decompressor: test_przm_dec.exe <in.przm> <out.bin>
				cmd := exec.Command(cBinPath, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("Pure C decompressor failed with status %v:\nOutput:\n%s", err, string(output))
				}

				// 5. Read Pure C decompressed output and verify bit-exact parity
				cDec, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("ReadFile failed: %v", err)
				}
				if len(cDec) != len(raw) {
					t.Fatalf("Pure C decompressed length mismatch: got %d, want %d", len(cDec), len(raw))
				}
				if !bytes.Equal(cDec, raw) {
					t.Fatalf("Pure C decompressed content differs from original raw data!")
				}
				if !bytes.Equal(cDec, goDec) {
					t.Fatalf("Cross-language mismatch between Pure C and Go decompressor outputs!")
				}

				compressionRatio := float64(len(raw)) / float64(len(comp))
				t.Logf("[%s] PASSED: Raw=%d B -> Comp=%d B (Ratio: %.2fx) -> Pure C Bit-Exact Match",
					testName, len(raw), len(comp), compressionRatio)
			})
		}
	}
}

// TestChallenger_M5_2_ZeroAlloc_And_LineRate strictly verifies:
// 1. testing.AllocsPerRun(100, ...) is strictly 0.00 during przm.DecompressTo on multi-block deduplicated streams.
// 2. Decompression throughput on deduplicated streams saturates hardware line-rate (target: 10–50+ GB/s, matching ~54 GB/s memcpy).
func TestChallenger_M5_2_ZeroAlloc_And_LineRate(t *testing.T) {
	sizes := []struct {
		name      string
		numBlocks int
	}{
		{"4Blocks_256KB", 4},
		{"16Blocks_1MB", 16},
		{"64Blocks_4MB", 64},
	}

	levels := []int{1, 3}

	for _, sz := range sizes {
		// Construct multi-block data where block 0 repeats across all blocks
		block := make([]byte, przm.DedupBlockSize)
		for i := range block {
			block[i] = byte((i * 23) ^ (i >> 5) ^ 0x5A)
		}
		var raw bytes.Buffer
		for range sz.numBlocks {
			raw.Write(block)
		}
		data := raw.Bytes()
		totalBytes := len(data)

		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Lvl%d", sz.name, lvl)
			t.Run(testName, func(t *testing.T) {
				comp, err := przm.Compress(data, lvl)
				if err != nil {
					t.Fatalf("Compress failed: %v", err)
				}

				dst := make([]byte, totalBytes+64)

				// Warmup execution
				decWarmup, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("Warmup DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decWarmup, data) {
					t.Fatalf("Warmup DecompressTo data mismatch")
				}

				// --- 1. Zero dynamic heap allocation verification ---
				allocs := testing.AllocsPerRun(100, func() {
					out, err := przm.DecompressTo(comp, dst)
					if err != nil {
						t.Fatalf("DecompressTo in AllocsPerRun failed: %v", err)
					}
					_ = out
				})

				if allocs != 0.0 {
					t.Fatalf("INVARIANT VIOLATION: DecompressTo produced %.2f allocs/run (mandated strictly 0.00)", allocs)
				}
				t.Logf("[%s] AllocsPerRun(100) = %.2f (strictly 0.00 zero-heap invariant satisfied)", testName, allocs)

				// --- 2. Hardware Line-Rate Decompression Throughput Benchmark ---
				// Dynamic iteration count based on buffer size to ensure stable measurement
				iters := 5000
				if sz.numBlocks >= 64 {
					iters = 1000
				} else if sz.numBlocks >= 16 {
					iters = 3000
				}

				start := time.Now()
				for i := 0; i < iters; i++ {
					_, _ = przm.DecompressTo(comp, dst)
				}
				elapsed := time.Since(start)

				bytesProcessed := float64(totalBytes) * float64(iters)
				sec := elapsed.Seconds()
				gbPerSec := (bytesProcessed / (1024 * 1024 * 1024)) / sec

				t.Logf("[%s] Decompressed %d bytes x %d iters in %v => %.2f GB/s",
					testName, totalBytes, iters, elapsed, gbPerSec)

				// Target bracket: 10–50+ GB/s (saturating RAM memcpy line rate)
				if gbPerSec < 10.0 {
					t.Errorf("Throughput below target line rate bracket: got %.2f GB/s, want >= 10.00 GB/s", gbPerSec)
				} else {
					t.Logf("[%s] SUCCESS: Line-rate throughput satisfied (%.2f GB/s in 10-50+ GB/s bracket)",
						testName, gbPerSec)
				}
			})
		}
	}
}
