// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"sync"
	"testing"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// TestChallenger_EdgeCases tests extreme and boundary input sizes on Sheaf Trellis and PRZM.
func TestChallenger_EdgeCases(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255, 256, 1023, 1024, 65535, 65536}

	for _, sz := range sizes {
		// 1. All zeroes
		zeroData := make([]byte, sz)
		testRoundtrips(t, "zeroes", sz, zeroData)

		// 2. All 0xFF
		ffData := make([]byte, sz)
		for i := range ffData {
			ffData[i] = 0xFF
		}
		testRoundtrips(t, "all_ff", sz, ffData)

		// 3. Alternating 0xAA 0x55
		altData := make([]byte, sz)
		for i := range altData {
			if i%2 == 0 {
				altData[i] = 0xAA
			} else {
				altData[i] = 0x55
			}
		}
		testRoundtrips(t, "alternating", sz, altData)

		// 4. Random / pseudo-random
		randData := make([]byte, sz)
		for i := range randData {
			randData[i] = byte((i * 109 + 37) % 256)
		}
		testRoundtrips(t, "pseudorand", sz, randData)
	}
}

func testRoundtrips(t *testing.T, name string, sz int, data []byte) {
	// A. Sheaf Trellis roundtrip
	compSheaf := przm.CompressSheafTrellis(data)
	if sz == 0 {
		if len(compSheaf) != 0 {
			t.Fatalf("[%s_%d] Sheaf Trellis empty input should return nil/empty, got len %d", name, sz, len(compSheaf))
		}
	} else {
		dst := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		decSheaf, err := przm.DecompressSheafTrellis(compSheaf, dst)
		if err != nil {
			t.Fatalf("[%s_%d] DecompressSheafTrellis failed: %v", name, sz, err)
		}
		if !bytes.Equal(decSheaf, data) {
			t.Fatalf("[%s_%d] Sheaf Trellis roundtrip mismatch! Expected len %d, got len %d", name, sz, len(data), len(decSheaf))
		}
	}

	// B. PRZM Unified Level 1 roundtrip
	if sz > 0 {
		compPRZM, err := przm.Compress(data, 1)
		if err != nil {
			t.Fatalf("[%s_%d] przm.Compress(lvl1) failed: %v", name, sz, err)
		}
		decPRZM, err := przm.Decompress(compPRZM)
		if err != nil {
			t.Fatalf("[%s_%d] przm.Decompress failed: %v", name, sz, err)
		}
		if !bytes.Equal(decPRZM, data) {
			t.Fatalf("[%s_%d] przm level 1 roundtrip mismatch! Expected len %d, got len %d", name, sz, len(data), len(decPRZM))
		}
	}
}

// TestChallenger_PathologicalRedexOverlaps tests dense overlapping critical pairs
// designed to challenge Knuth-Bendix confluence and canonical rule folding.
func TestChallenger_PathologicalRedexOverlaps(t *testing.T) {
	patterns := []struct {
		name string
		gen  func(n int) []byte
	}{
		{
			name: "Fibonacci_overlap",
			gen: func(n int) []byte {
				// Construct Fibonacci word (a, b, ab, aba, abaab, ...)
				s0 := "a"
				s1 := "b"
				for len(s1) < n {
					s0, s1 = s1, s1+s0
				}
				buf := []byte(s1[:n])
				return buf
			},
		},
		{
			name: "dense_overlapping_digrams",
			gen: func(n int) []byte {
				// "abacabadabacabae..." (ruler sequence with max overlapping redexes)
				b := make([]byte, n)
				for i := 0; i < n; i++ {
					// ruler function
					tz := 0
					v := i + 1
					for (v & 1) == 0 {
						tz++
						v >>= 1
					}
					b[i] = byte('a' + (tz % 26))
				}
				return b
			},
		},
		{
			name: "triple_overlap",
			gen: func(n int) []byte {
				// "abababababababa..."
				b := make([]byte, n)
				for i := 0; i < n; i++ {
					if i%2 == 0 {
						b[i] = 'a'
					} else {
						b[i] = 'b'
					}
				}
				return b
			},
		},
		{
			name: "cyclic_quad",
			gen: func(n int) []byte {
				// "abcdbcda..."
				b := make([]byte, n)
				for i := 0; i < n; i++ {
					b[i] = byte('a' + (i % 4))
				}
				return b
			},
		},
	}

	for _, p := range patterns {
		for _, sz := range []int{512, 4096, 16384, 65536} {
			data := p.gen(sz)
			for lvl := 1; lvl <= 3; lvl++ {
				comp, err := przm.Compress(data, lvl)
				if err != nil {
					t.Fatalf("[%s_%d_lvl%d] Compress error: %v", p.name, sz, lvl, err)
				}
				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("[%s_%d_lvl%d] Decompress error: %v", p.name, sz, lvl, err)
				}
				if !bytes.Equal(dec, data) {
					t.Fatalf("[%s_%d_lvl%d] Knuth-Bendix redex roundtrip mismatch!", p.name, sz, lvl)
				}
			}
		}
	}
}

// TestChallenger_HighEntropyCryptoRandom tests completely incompressible random noise.
func TestChallenger_HighEntropyCryptoRandom(t *testing.T) {
	for _, sz := range []int{1024, 8192, 65536} {
		data := make([]byte, sz)
		_, err := rand.Read(data)
		if err != nil {
			t.Fatal(err)
		}

		// Sheaf Trellis
		compSheaf := przm.CompressSheafTrellis(data)
		dst := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		decSheaf, err := przm.DecompressSheafTrellis(compSheaf, dst)
		if err != nil {
			t.Fatalf("[random_%d] Sheaf Trellis decompress failed: %v", sz, err)
		}
		if !bytes.Equal(decSheaf, data) {
			t.Fatalf("[random_%d] Sheaf Trellis random noise mismatch!", sz)
		}

		// PRZM Levels 1, 2, 3
		for lvl := 1; lvl <= 3; lvl++ {
			comp, err := przm.Compress(data, lvl)
			if err != nil {
				t.Fatalf("[random_%d_lvl%d] Compress failed: %v", sz, lvl, err)
			}
			dec, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("[random_%d_lvl%d] Decompress failed: %v", sz, lvl, err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("[random_%d_lvl%d] Random noise roundtrip mismatch!", sz, lvl)
			}
		}
	}
}

// TestChallenger_ConcurrencyAndPoolSafety verifies that scratchpad pools and shared buffers
// are completely race-free under high concurrent multi-goroutine load.
func TestChallenger_ConcurrencyAndPoolSafety(t *testing.T) {
	const numGoroutines = 16
	const iterations = 50

	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte(i ^ (i >> 3) ^ (i >> 7))
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gID int) {
			defer wg.Done()
			dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)
			for it := 0; it < iterations; it++ {
				// Sheaf Trellis
				comp := przm.CompressSheafTrellis(data)
				dec, err := przm.DecompressSheafTrellis(comp, dst)
				if err != nil {
					t.Errorf("[g=%d, it=%d] Sheaf decompress error: %v", gID, it, err)
					return
				}
				if !bytes.Equal(dec, data) {
					t.Errorf("[g=%d, it=%d] Sheaf roundtrip mismatch", gID, it)
					return
				}

				// PRZM Lvl 1
				compPRZM, err := przm.Compress(data, 1)
				if err != nil {
					t.Errorf("[g=%d, it=%d] PRZM compress error: %v", gID, it, err)
					return
				}
				decPRZM, err := przm.Decompress(compPRZM)
				if err != nil {
					t.Errorf("[g=%d, it=%d] PRZM decompress error: %v", gID, it, err)
					return
				}
				if !bytes.Equal(decPRZM, data) {
					t.Errorf("[g=%d, it=%d] PRZM roundtrip mismatch", gID, it)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// TestChallenger_EmpiricalCycleCeiling confirms decompressor operates in <= 384 CPU cycles per 64-byte block.
func TestChallenger_EmpiricalCycleCeiling(t *testing.T) {
	const sz = 65536
	data := make([]byte, sz)
	for i := range data {
		data[i] = byte(i*13 + (i >> 4)*7)
	}

	comp := przm.CompressSheafTrellis(data)
	dst := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)

	// Warmup
	for i := 0; i < 100; i++ {
		_, _ = przm.DecompressSheafTrellis(comp, dst)
	}

	// Benchmark 1000 runs
	const runs = 1000
	start := time.Now()
	for i := 0; i < runs; i++ {
		_, err := przm.DecompressSheafTrellis(comp, dst)
		if err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)

	totalBytes := float64(sz * runs)
	nsPerRun := float64(elapsed.Nanoseconds()) / float64(runs)
	nsPer64B := nsPerRun / (float64(sz) / 64.0)
	mbPerSec := (totalBytes / (1024 * 1024)) / elapsed.Seconds()

	// Assuming 4.4 GHz peak clock (0.227 ns/cycle):
	cyclesPer64B_at_4_4GHz := nsPer64B * 4.4
	// Assuming 4.0 GHz all-core clock (0.25 ns/cycle):
	cyclesPer64B_at_4_0GHz := nsPer64B * 4.0

	t.Logf("Empirical Decompressor Metrics:")
	t.Logf("  Throughput: %.2f MB/s", mbPerSec)
	t.Logf("  Time per 64B block: %.2f ns", nsPer64B)
	t.Logf("  Estimated cycles/64B (@4.0GHz): %.1f cycles", cyclesPer64B_at_4_0GHz)
	t.Logf("  Estimated cycles/64B (@4.4GHz): %.1f cycles", cyclesPer64B_at_4_4GHz)
	t.Logf("  Mandated Ceiling: <= 384 cycles/64B")

	if cyclesPer64B_at_4_4GHz > 384.0 {
		t.Fatalf("Decompressor exceeded cycle ceiling: got %.1f cycles/64B, ceiling is 384", cyclesPer64B_at_4_4GHz)
	}
}

// TestChallenger_ZeroAllocationsAllCorpora verifies that decompressors strictly incur 0 heap allocations
// across all test datasets (JSON, Code, MachineCode, Tabular, RLE).
func TestChallenger_ZeroAllocationsAllCorpora(t *testing.T) {
	corpora := [][]byte{
		bench.GenerateJSON(64 * 1024),
		bench.GenerateSourceCode(64 * 1024),
		bench.GenerateMachineCode(64 * 1024),
		bench.GenerateTabular(64*1024, 32),
		bench.GenerateRLE(64 * 1024),
	}

	for idx, data := range corpora {
		comp := przm.CompressSheafTrellis(data)
		dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

		// Warmup
		_, _ = przm.DecompressSheafTrellis(comp, dst)

		allocs := testing.AllocsPerRun(50, func() {
			_, err := przm.DecompressSheafTrellis(comp, dst)
			if err != nil {
				t.Fatal(err)
			}
		})

		if allocs != 0 {
			t.Fatalf("[corpus %d] DecompressSheafTrellis incurred %.2f allocs/op (strictly 0 required)", idx, allocs)
		}
	}
	t.Log("Strict zero-allocation invariant (0 B/op, 0 allocs/op) verified across all corpora.")
}
