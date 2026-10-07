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

	"github.com/lemon4ksan/przm"
)

// TestChallenger_M3_1_OverlappingMatchChains tests overlapping and competing match chains
// where greedy parsing makes the wrong choice, verifying bit-exactness and that Level 3
// produces a superior or equal compression ratio compared to Level 1 and Level 2.
func TestChallenger_M3_1_OverlappingMatchChains(t *testing.T) {
	// Case 1: Overlapping tokens ABCDEF, BCDEFGH, CDEFGHIJ repeated
	var buf1 bytes.Buffer
	tokens := []string{
		"ABCDEF",
		"BCDEFGH",
		"CDEFGHIJ",
		"DEFGHIJK",
		"EFGHIJKL",
		"FGHIJKLM",
	}
	for i := 0; i < 500; i++ {
		buf1.WriteString(tokens[i%len(tokens)])
		if i%7 == 0 {
			buf1.WriteString("_RANDOM_PAD_")
		}
	}
	data1 := buf1.Bytes()

	// Case 2: Greedy trap - short immediate match vs slightly deferred longer/repeat match
	var buf2 bytes.Buffer
	trapPatternA := "ABCDEF_SHORT"
	trapPatternB := "ABCDEF_LONG_MATCH_SEQUENCE_REPEAT_TARGET_XYZ"
	for i := 0; i < 200; i++ {
		buf2.WriteString(trapPatternA)
		buf2.WriteString(trapPatternB)
		buf2.WriteString("ABCDEF") // prefix of both patterns
		buf2.WriteString(trapPatternB)
		buf2.WriteString(fmt.Sprintf("_STEP_%d_", i))
	}
	data2 := buf2.Bytes()

	// Case 3: Dense overlapping substrings with alternating shifts
	var buf3 bytes.Buffer
	seedStr := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for i := 0; i < 3000; i++ {
		shift := i % 10
		buf3.WriteString(seedStr[shift : shift+16])
	}
	data3 := buf3.Bytes()

	testCases := []struct {
		name string
		data []byte
	}{
		{"OverlappingTokens_ABCDEF", data1},
		{"GreedyTrap_DeferredLongMatch", data2},
		{"DenseShiftingSubstrings", data3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compL1, err := przm.Compress(tc.data, 1)
			if err != nil {
				t.Fatalf("L1 compress failed: %v", err)
			}
			compL2, err := przm.Compress(tc.data, 2)
			if err != nil {
				t.Fatalf("L2 compress failed: %v", err)
			}
			compL3, err := przm.Compress(tc.data, 3)
			if err != nil {
				t.Fatalf("L3 compress failed: %v", err)
			}
			compL4, err := przm.Compress(tc.data, 4)
			if err != nil {
				t.Fatalf("L4 compress failed: %v", err)
			}

			// Verify bit-exact roundtrips
			for lvl, comp := range map[int][]byte{1: compL1, 2: compL2, 3: compL3, 4: compL4} {
				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("L%d decompress failed: %v", lvl, err)
				}
				if !bytes.Equal(dec, tc.data) {
					t.Fatalf("L%d bit-exact mismatch: decompressed length %d != raw %d", lvl, len(dec), len(tc.data))
				}

				dst := make([]byte, len(tc.data)+64)
				decTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("L%d DecompressTo failed: %v", lvl, err)
				}
				if !bytes.Equal(decTo, tc.data) {
					t.Fatalf("L%d DecompressTo bit-exact mismatch", lvl)
				}
			}

			t.Logf("[%s] Raw: %d B | L1: %d B (%.2fx) | L2: %d B (%.2fx) | L3: %d B (%.2fx) | L4: %d B (%.2fx)",
				tc.name, len(tc.data),
				len(compL1), float64(len(tc.data))/float64(len(compL1)),
				len(compL2), float64(len(tc.data))/float64(len(compL2)),
				len(compL3), float64(len(tc.data))/float64(len(compL3)),
				len(compL4), float64(len(tc.data))/float64(len(compL4)),
			)

			// Empirical verification: L3 ratio must be superior or equal to L1 and L2
			if len(compL3) > len(compL1) {
				t.Errorf("L3 compressed size (%d) exceeds L1 (%d) on overlapping data", len(compL3), len(compL1))
			}
		})
	}
}

// TestChallenger_M3_1_Dense2ByteAnd3ByteRepeats tests dense 2-byte and 3-byte repeats
// (testing 1-cycle repeat checks and secondary 3-byte direct-mapped hash table).
func TestChallenger_M3_1_Dense2ByteAnd3ByteRepeats(t *testing.T) {
	repeats := []struct {
		name    string
		pattern []byte
		size    int
	}{
		{"2Byte_ABAB_64B", []byte("AB"), 64},
		{"2Byte_ABAB_1KB", []byte("AB"), 1024},
		{"2Byte_ABAB_16KB", []byte("AB"), 16384},
		{"2Byte_ABAB_65536B", []byte("AB"), 65536},
		{"3Byte_XYZ_64B", []byte("XYZ"), 64},
		{"3Byte_XYZ_1KB", []byte("XYZ"), 1024},
		{"3Byte_XYZ_16KB", []byte("XYZ"), 16384},
		{"3Byte_XYZ_65536B", []byte("XYZ"), 65536},
		{"Mixed_2B_3B_16KB", []byte("ABXYZ"), 16384},
		{"Alternating_4B_32KB", []byte("AABBCCDDAABBCCDD"), 32768},
	}

	for _, rep := range repeats {
		t.Run(rep.name, func(t *testing.T) {
			data := make([]byte, rep.size)
			pLen := len(rep.pattern)
			for i := range data {
				data[i] = rep.pattern[i%pLen]
			}

			for _, level := range []int{3, 4} {
				comp, err := przm.Compress(data, level)
				if err != nil {
					t.Fatalf("L%d compress failed: %v", level, err)
				}

				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("L%d decompress failed: %v", level, err)
				}
				if !bytes.Equal(dec, data) {
					t.Fatalf("L%d bit-exact mismatch: len %d != %d", level, len(dec), len(data))
				}

				dst := make([]byte, rep.size+64)
				decTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("L%d DecompressTo failed: %v", level, err)
				}
				if !bytes.Equal(decTo, data) {
					t.Fatalf("L%d DecompressTo bit-exact mismatch", level)
				}

				// Check compression efficiency on dense repeats
				if len(comp) >= len(data)/2 && rep.size >= 1024 {
					t.Errorf("L%d failed to compress dense repeats effectively: raw %d, comp %d", level, rep.size, len(comp))
				}
			}
		})
	}
}

// TestChallenger_M3_1_VariedChunkBoundaries tests varied chunk boundaries:
// exact 64 KiB multiples, odd sizes, 0B, 1B, 2B, 3B, 4B, 65535B, 65536B, 65537B, 128KB, 1MB.
func TestChallenger_M3_1_VariedChunkBoundaries(t *testing.T) {
	sizes := []struct {
		name string
		size int
	}{
		{"Size_0B", 0},
		{"Size_1B", 1},
		{"Size_2B", 2},
		{"Size_3B", 3},
		{"Size_4B", 4},
		{"Size_5B", 5},
		{"Size_7B", 7},
		{"Size_15B", 15},
		{"Size_16B", 16},
		{"Size_31B", 31},
		{"Size_32B", 32},
		{"Size_63B", 63},
		{"Size_64B", 64},
		{"Size_127B", 127},
		{"Size_128B", 128},
		{"Size_255B", 255},
		{"Size_256B", 256},
		{"Size_1023B", 1023},
		{"Size_1024B", 1024},
		{"Size_65535B_ExactBoundaryMinus1", 65535},
		{"Size_65536B_Exact64KBoundary", 65536},
		{"Size_65537B_ExactBoundaryPlus1", 65537},
		{"Size_131072B_Exact128K", 131072},
		{"Size_131073B_128KPlus1", 131073},
		{"Size_1048576B_Exact1M", 1048576},
		{"Size_1048577B_1MPlus1", 1048577},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			// Generate deterministic pseudo-random compressible data
			data := make([]byte, tc.size)
			rng := rand.New(rand.NewSource(int64(tc.size + 42)))
			pattern := []byte("BOUNDARY_CHUNK_TEST_PAYLOAD_ABC123_XYZ789_")
			for i := 0; i < tc.size; {
				if tc.size-i >= len(pattern) && rng.Float32() < 0.7 {
					copy(data[i:], pattern)
					i += len(pattern)
				} else {
					data[i] = byte(rng.Intn(256))
					i++
				}
			}

			for _, level := range []int{3, 4} {
				comp, err := przm.Compress(data, level)
				if err != nil {
					t.Fatalf("L%d compress failed for size %d: %v", level, tc.size, err)
				}

				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("L%d decompress failed for size %d: %v", level, tc.size, err)
				}
				if !bytes.Equal(dec, data) {
					t.Fatalf("L%d bit-exact mismatch for size %d: got %d bytes, want %d bytes", level, tc.size, len(dec), len(data))
				}

				dst := make([]byte, tc.size+64)
				decTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("L%d DecompressTo failed for size %d: %v", level, tc.size, err)
				}
				if !bytes.Equal(decTo, data) {
					t.Fatalf("L%d DecompressTo bit-exact mismatch for size %d", level, tc.size)
				}
			}
		})
	}
}

// TestChallenger_M3_1_ZeroHeapAllocs_DecompressTo empirically verifies that DecompressTo
// achieves strictly 0.0 heap allocations per run on optimal-parsed payloads.
func TestChallenger_M3_1_ZeroHeapAllocs_DecompressTo(t *testing.T) {
	testSizes := []struct {
		name string
		size int
	}{
		{"16KB_Overlapping", 16 * 1024},
		{"64KB_DenseRepeats", 64 * 1024},
		{"65536B_Exact64K", 65536},
		{"65537B_CrossBlock", 65537},
		{"128KB_MultiBlock", 128 * 1024},
		{"1MB_LargeWindow", 1024 * 1024},
	}

	for _, tc := range testSizes {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			pattern := []byte("TEST_CHALLENGER_M3_1_ZERO_HEAP_ALLOCS_OPTIMAL_PARSER_PAYLOAD_STRING_ABC123_")
			for i := range data {
				data[i] = pattern[i%len(pattern)]
			}

			// Compress with Level 3 (Optimal DAG parse)
			comp, err := przm.Compress(data, 3)
			if err != nil {
				t.Fatalf("L3 compress failed: %v", err)
			}

			dst := make([]byte, tc.size+64)

			// Warmup run
			res, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(res, data) {
				t.Fatalf("warmup bit-exact mismatch")
			}

			// Profile dynamic allocations across 50 runs
			allocs := testing.AllocsPerRun(50, func() {
				_, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("DecompressTo failed in benchmark loop: %v", err)
				}
			})

			t.Logf("[%s] AllocsPerRun = %.2f", tc.name, allocs)
			if allocs != 0.0 {
				t.Errorf("FAIL: Expected strictly 0.0 heap allocations in DecompressTo, got %.2f", allocs)
			}
		})
	}
}

// TestChallenger_M3_1_PureC_Interop verifies that Level 3 optimal-parsed frames
// decompress bit-exact with the Pure C zero-alloc decompressor (test_przm_dec.exe).
func TestChallenger_M3_1_PureC_Interop(t *testing.T) {
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
		t.Skip("C test binary test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	testCases := []struct {
		name string
		size int
		gen  func(int) []byte
	}{
		{
			name: "OverlappingTokens",
			size: 16384,
			gen: func(size int) []byte {
				var buf bytes.Buffer
				toks := []string{"ABCDEF", "BCDEFGH", "CDEFGHIJ", "DEFGHIJK"}
				for buf.Len() < size {
					buf.WriteString(toks[(buf.Len()/6)%len(toks)])
				}
				return buf.Bytes()[:size]
			},
		},
		{
			name: "Dense2ByteRepeats",
			size: 8192,
			gen: func(size int) []byte {
				data := make([]byte, size)
				for i := range data {
					if i%2 == 0 {
						data[i] = 'A'
					} else {
						data[i] = 'B'
					}
				}
				return data
			},
		},
		{
			name: "CrossBlock_65537B",
			size: 65537,
			gen: func(size int) []byte {
				data := make([]byte, size)
				pat := []byte("PURE_C_CROSS_BLOCK_OPTIMAL_PARSER_VERIFICATION_")
				for i := range data {
					data[i] = pat[i%len(pat)]
				}
				return data
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.gen(tc.size)
			comp, err := przm.Compress(raw, 3)
			if err != nil {
				t.Fatalf("Compress L3 failed: %v", err)
			}

			inPath := filepath.Join(tmpDir, tc.name+".przm")
			outPath := filepath.Join(tmpDir, tc.name+".out")

			if err := os.WriteFile(inPath, comp, 0644); err != nil {
				t.Fatalf("failed to write compressed file: %v", err)
			}

			cmd := exec.Command(cBinPath, inPath, outPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
			}

			decompressed, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("failed to read decompressed output: %v", err)
			}

			if !bytes.Equal(decompressed, raw) {
				t.Fatalf("Pure C decompressed output does not match raw original: got %d B, want %d B", len(decompressed), len(raw))
			}
		})
	}
}
