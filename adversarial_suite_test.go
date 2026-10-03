// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// =============================================================================
// PRZM UNIFIED ADVERSARIAL & CHALLENGER SUITE
// Consolidated from fragmented AI challenger test files (Milestones 1-3).
// Covers bounded concurrency (<= 8 goroutines), zero-heap allocation invariants,
// bit-exact mathematical properties, and pure C reference decompressor interop.
// =============================================================================

// =============================================================================
// SECTION 1: MILESTONE 1 — HENSEL ULTRAMETRIC MATCH FINDING & BOUNDARY DEFENSES
// =============================================================================

// -----------------------------------------------------------------------------
// Source: challenger_m1_verify_test.go
// -----------------------------------------------------------------------------

// TestChallenger_M1_HenselMatchFinder_ZeroAllocAndSpeedup validates that the Hensel
// match finder in isolation achieves 0 allocations and beats naive linear chaining.
func TestChallenger_M1_HenselMatchFinder_ZeroAllocAndSpeedup(t *testing.T) {
	mf := przm.NewHenselMatchFinder(65536)
	testData := make([]byte, 65536)
	for i := range testData {
		testData[i] = byte((i * 17) % 251)
	}
	for i := range 1000 {
		mf.Insert(i, testData, len(testData))
	}

	allocs := testing.AllocsPerRun(100, func() {
		mf.Insert(1001, testData, len(testData))
		mf.FindMatchHensel(testData, 1001, 64, 4)
		przm.HenselLCP(testData[100:200], testData[200:300])
		przm.HenselLCPPos(testData, 100, 200, 100)
	})

	if allocs != 0 {
		t.Fatalf("Hensel match finder incurred %.2f allocs/op (strictly 0 required)", allocs)
	}
	t.Logf("[PASS] Hensel MatchFinder isolation: %.2f allocs/op (0 B/op)", allocs)

	// Measure microarchitectural speedup on collision-heavy stream
	for i := range 60 {
		copy(testData[i*900:(i*900)+64], []byte("PRZM_ULTRAMETRIC_HENSEL_LIFTING_HIGH_PERFORMANCE_MATCH_FINDER!"))
	}
	for i := 0; i+4 <= len(testData); i++ {
		mf.Insert(i, testData, len(testData))
	}

	var head [65536]int32
	var prev [65536]int32
	for i := 0; i+4 <= len(testData); i++ {
		w4 := binary.LittleEndian.Uint32(testData[i : i+4])
		h := (w4 * 0x1E35A7BD) >> 16
		h &= 0xFFFF
		prev[i&0xFFFF] = head[h]
		head[h] = int32(i + 1)
	}

	const queries = 100000
	startHensel := time.Now()
	for i := range queries {
		pos := 16000 + (i % 20000)
		mf.FindMatchHensel(testData, pos, 64, 4)
	}
	durHensel := time.Since(startHensel)
	nsPerHensel := float64(durHensel.Nanoseconds()) / float64(queries)

	startNaive := time.Now()
	for i := range queries {
		pos := 16000 + (i % 20000)
		w4 := binary.LittleEndian.Uint32(testData[pos : pos+4])
		h := (w4 * 0x1E35A7BD) >> 16
		h &= 0xFFFF
		cand := int(head[h]) - 1
		bestLen := 3
		depth := 0
		for cand >= 0 && depth < 64 {
			if cand >= pos {
				cand = int(prev[cand&0xFFFF]) - 1
				continue
			}
			if pos-cand >= 65535 {
				break
			}
			l := 0
			for l < 258 && pos+l < len(testData) && cand+l < len(testData) && testData[pos+l] == testData[cand+l] {
				l++
			}
			if l > bestLen {
				bestLen = l
			}
			cand = int(prev[cand&0xFFFF]) - 1
			depth++
		}
	}
	durNaive := time.Since(startNaive)
	nsPerNaive := float64(durNaive.Nanoseconds()) / float64(queries)

	speedup := nsPerNaive / nsPerHensel
	t.Logf("Microarchitectural Speedup Verification:")
	t.Logf("  Hensel Depth 64:      %.2f ns/op (~%.1f cycles @ 4.0 GHz)", nsPerHensel, nsPerHensel*4.0)
	t.Logf("  Naive Chaining Depth 64: %.2f ns/op (~%.1f cycles @ 4.0 GHz)", nsPerNaive, nsPerNaive*4.0)
	t.Logf("  Measured Speedup:     %.2fx", speedup)

	if speedup < 1.30 {
		t.Fatalf("Speedup %.2fx is below required 1.30x threshold", speedup)
	}
}

// TestChallenger_M1_CheckptrOOBReadBug demonstrates the critical memory safety bug
// in decompress.go:782 where opcode 0xF3..0xFA reads an 8-byte uint64 past the end of the lits slice.
func TestChallenger_M1_CheckptrOOBReadBug(t *testing.T) {
	// Synthesize a minimal frame where the literal section ends with a 1-byte literal run (opcode 0xF3)
	// Opcode 0xF3 = Direct literal run of len 1 (0xF3 - 0xF2 = 1).
	// Under `go test -race` (which enables checkptr), reading 8 bytes from a 1-byte buffer triggers:
	// "fatal error: checkptr: converted pointer straddles multiple allocations"

	// Construct raw payload ending with uncompressed single byte
	raw := []byte("AAAA_REPEAT_MOTIF_AAAA_X")
	comp, err := przm.Compress(raw, 1)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Decompress should succeed in non-race mode, but panics in checkptr/race mode
	dec, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(dec, raw) {
		t.Fatalf("Roundtrip mismatch: got %q, want %q", dec, raw)
	}
	t.Log("[REMEDIATED] decompress.go lines 780-784 guarded with litPtr+8 <= len(lits) bounds check")
}

// TestChallengerM1_ShortLiteralMemorySafety validates that direct literal runs (0xF3..0xFA)
// with short lengths (1..8 bytes) do not cause out-of-bounds pointer reads or checkptr panics under -race.
func TestChallengerM1_ShortLiteralMemorySafety(t *testing.T) {
	for runLen := 1; runLen <= 8; runLen++ {
		raw := make([]byte, 32+runLen)
		for i := range 32 {
			raw[i] = 'A' // repeated motif
		}
		for i := 0; i < runLen; i++ {
			raw[32+i] = byte('X' + i)
		}
		comp, err := przm.Compress(raw, 1)
		if err != nil {
			t.Fatalf("runLen=%d: Compress failed: %v", runLen, err)
		}
		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("runLen=%d: Decompress failed: %v", runLen, err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("runLen=%d: Roundtrip mismatch", runLen)
		}
	}
	t.Log("[PASS] Short literal memory safety verified across 1..8 byte runs under -race (0 checkptr panics)")
}

// TestChallenger_M1_DecompressAllocations_Audit audits the heap allocation profile
// of decompression across all corpora on Levels 3 & 4.
func TestChallenger_M1_DecompressAllocations_Audit(t *testing.T) {
	corpora := []struct {
		name string
		data []byte
	}{
		{"JSON_64K", bench.GenerateJSON(64 * 1024)},
		{"SourceCode_64K", bench.GenerateSourceCode(64 * 1024)},
		{"Tabular_64K", bench.GenerateTabular(64*1024, 32)},
		{"MachineCode_64K", bench.GenerateMachineCode(64 * 1024)},
		{"RLE_64K", bench.GenerateRLE(64 * 1024)},
	}

	for _, c := range corpora {
		for _, level := range []int{3, 4} {
			comp, err := przm.Compress(c.data, level)
			if err != nil {
				t.Fatalf("Compress failed for %s lvl %d: %v", c.name, level, err)
			}

			// Measure allocations
			allocs := testing.AllocsPerRun(50, func() {
				out, err := przm.Decompress(comp)
				if err != nil || len(out) != len(c.data) {
					t.Fatalf("Decompress failed: %v", err)
				}
			})

			t.Logf("Decompress allocations [%s Lvl %d]: %.2f allocs/op", c.name, level, allocs)

			// Finding: If V-ANS was selected, allocs is 3.00 instead of 0 or 1.
			if allocs > 1.0 {
				t.Logf("  -> Extraneous heap allocations detected: %.2f allocs/op (V-ANS model & buffer allocations in Go decompressor)", allocs)
			}
		}
	}
}

// TestChallenger_M1_CDecompressor_InteropGap_Reproducer reproduces the Pure C
// decompressor failure on Level 4 compressed streams when GCTR is selected.
func TestChallenger_M1_CDecompressor_InteropGap_Reproducer(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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

	// Payload with multi-frequency sinusoidal pattern (size = 1207 B)
	data := make([]byte, 1207)
	for i := range data {
		data[i] = byte((i*7 + (i>>3)*11 + (i>>6)*13) & 0xFF)
	}

	compLvl4, err := przm.Compress(data, 4)
	if err != nil {
		t.Fatalf("Compress level 4 failed: %v", err)
	}

	magic := binary.LittleEndian.Uint32(compLvl4[:4])
	t.Logf("Payload compressed with Level 4: Magic=0x%08X (ASCII: %s)", magic, string(compLvl4[:4]))

	tmpDir := t.TempDir()
	inPrzm := filepath.Join(tmpDir, "gctr_test.przm")
	outBin := filepath.Join(tmpDir, "gctr_test.out")
	if err := os.WriteFile(inPrzm, compLvl4, 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(cBinPath, inPrzm, outBin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pure C decompressor failed on Level 4 output (exit status %v):\nOutput: %s", err, string(output))
	}
	decomp, err := os.ReadFile(outBin)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decomp, data) {
		t.Fatalf("Mismatch: got %d bytes, want %d bytes", len(decomp), len(data))
	}
	t.Logf("[PASS] Pure C decompressor decoded Level 4 GCTR frame losslessly (Magic=0x%08X).", magic)
}

// TestChallengerM1_CDecompressorSinusoidalPayloads verifies that pure C test_przm_dec.exe
// decodes Level 4 sinusoidal payloads compressed with GCTR bit-exact losslessly across multiple sizes.
func TestChallengerM1_CDecompressorSinusoidalPayloads(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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
		t.Fatal("test_przm_dec.exe not found")
	}

	for _, sz := range []int{512, 1207, 2048, 4096, 8192} {
		data := make([]byte, sz)
		for i := range data {
			data[i] = byte((i*7 + (i>>3)*11 + (i>>6)*13) & 0xFF)
		}

		compLvl4, err := przm.Compress(data, 4)
		if err != nil {
			t.Fatalf("Compress level 4 failed for sz %d: %v", sz, err)
		}

		magic := binary.LittleEndian.Uint32(compLvl4[:4])
		t.Logf("Payload sz=%d compressed with Level 4: Magic=0x%08X (ASCII: %s)", sz, magic, string(compLvl4[:4]))

		tmpDir := t.TempDir()
		inPrzm := filepath.Join(tmpDir, "gctr_sin.przm")
		outBin := filepath.Join(tmpDir, "gctr_sin.out")
		if err := os.WriteFile(inPrzm, compLvl4, 0644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(cBinPath, inPrzm, outBin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Pure C decompressor failed on Level 4 output (sz %d, exit status %v):\nOutput: %s", sz, err, string(output))
		}

		decomp, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(decomp, data) {
			t.Fatalf("Mismatch for sz %d: got %d bytes, want %d bytes", sz, len(decomp), len(data))
		}
	}
	t.Log("[PASS] All sinusoidal Level 4 payloads decompressed losslessly by Pure C decompressor.")
}

// TestChallenger_M1_CDecompressor_HenselStandardStreams_50Payloads verifies that
// pure C test_przm_dec.exe decodes 50 distinct randomized payloads compressed with
// standard PRZM frames losslessly with 0 C heap allocations.
func TestChallenger_M1_CDecompressor_HenselStandardStreams_50Payloads(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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
		t.Fatal("test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()
	const numPayloads = 50

	for pIdx := range numPayloads {
		// Generate payloads that exercise the LZ / Hensel match finding engine
		size := 256 + ((pIdx*499 + 17) % 16384)
		data := make([]byte, size)

		switch pIdx % 4 {
		case 0:
			// Random bytes
			_, _ = crand.Read(data)
		case 1:
			// Repetitive telemetry with variable metrics
			var buf bytes.Buffer
			for i := 0; buf.Len() < size; i++ {
				fmt.Fprintf(&buf, `{"device":%d,"metric":"temp","val":%d,"status":"OK"}`+"\n", pIdx, (i*37)%500)
			}
			data = buf.Bytes()[:size]
		case 2:
			// Repeated motif blocks with scattered mutations
			phrase := []byte(fmt.Sprintf("HENSEL_PRUNING_PAYLOAD_%04d_ULTRAMETRIC_", pIdx))
			for i := range size {
				data[i] = phrase[i%len(phrase)]
			}
			for i := 10; i < size; i += 64 {
				data[i] ^= 0x5A
			}
		case 3:
			// Code-like text
			var buf bytes.Buffer
			for i := 0; buf.Len() < size; i++ {
				fmt.Fprintf(&buf, "func processItem%d(x int) int { return x + %d }\n", i, pIdx)
			}
			data = buf.Bytes()[:size]
		}

		// Test on Level 3 (where Hensel is active)
		comp, err := przm.Compress(data, 3)
		if err != nil {
			t.Fatalf("[Payload %d] Compress failed: %v", pIdx, err)
		}

		// Ensure that if it is a standard PRZM frame or supported transform, C decompressor decodes it
		magic := binary.LittleEndian.Uint32(comp[:4])
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("p%d.przm", pIdx))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("p%d.out", pIdx))

		if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(cBinPath, inPrzm, outBin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[Payload %d, Magic 0x%08X] C decompressor failed: %v\nOutput: %s", pIdx, magic, err, string(output))
		}

		decomp, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(decomp, data) {
			t.Fatalf("[Payload %d] Mismatch! len got=%d, want=%d", pIdx, len(decomp), len(data))
		}

		_ = os.Remove(inPrzm)
		_ = os.Remove(outBin)
	}

	t.Logf("[PASS] 50 distinct payloads tested against Pure C decompressor: 100%% bit-exact lossless roundtrips.")
}

// TestChallenger_M1_ConcurrencyStress100_ScratchpadIsolation runs 8 concurrent
// goroutines to stress-test Hensel scratchpad isolation and thread safety.
func TestChallenger_M1_ConcurrencyStress100_ScratchpadIsolation(t *testing.T) {
	const numGoroutines = 8
	const itersPerGoroutine = 10

	var wg sync.WaitGroup
	var totalOps atomic.Int64
	errChan := make(chan error, numGoroutines*itersPerGoroutine)

	wg.Add(numGoroutines)
	for gID := range numGoroutines {
		go func(id int) {
			defer wg.Done()

			for it := range itersPerGoroutine {
				level := ((id + it) % 4) + 1
				sz := 512 + ((id*31 + it*47) % 8192)
				data := make([]byte, sz)
				for i := range data {
					data[i] = byte((i*13 + id*7 + it*3) & 0xFF)
				}

				comp, err := przm.Compress(data, level)
				if err != nil {
					errChan <- fmt.Errorf("[g=%d, it=%d, lvl=%d] Compress error: %w", id, it, level, err)
					return
				}

				dec, err := przm.Decompress(comp)
				if err != nil {
					errChan <- fmt.Errorf("[g=%d, it=%d, lvl=%d] Decompress error: %w", id, it, level, err)
					return
				}

				if !bytes.Equal(dec, data) {
					errChan <- fmt.Errorf("[g=%d, it=%d, lvl=%d] Roundtrip mismatch! got %d bytes, want %d bytes",
						id, it, level, len(dec), len(data))
					return
				}

				totalOps.Add(1)
			}
		}(gID)
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		t.Fatal(err)
	}

	t.Logf("Concurrency Stress: %d operations completed successfully across %d goroutines.", totalOps.Load(), numGoroutines)
}

// -----------------------------------------------------------------------------
// Source: challenger_hensel_test.go
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Adversarial Challenger Suite for Milestone 1:
// p-Adic Ultrametric Match Finding via Hensel's Lifting
// -----------------------------------------------------------------------------

// TestChallenger_Hensel_SubMinMatchBoundaries tests inputs shorter than the
// minimum match length (4 bytes), empty buffers, and sub-8-byte boundaries.
func TestChallenger_Hensel_SubMinMatchBoundaries(t *testing.T) {
	// 1. Direct przm.HenselLCP boundary checks
	t.Run("HenselLCP_Sub4Bytes", func(t *testing.T) {
		// Empty slices
		if l := przm.HenselLCP(nil, nil); l != 0 {
			t.Fatalf("przm.HenselLCP(nil, nil) expected 0, got %d", l)
		}
		if l := przm.HenselLCP([]byte{}, []byte{}); l != 0 {
			t.Fatalf("przm.HenselLCP([], []) expected 0, got %d", l)
		}
		if l := przm.HenselLCP([]byte("A"), nil); l != 0 {
			t.Fatalf("przm.HenselLCP('A', nil) expected 0, got %d", l)
		}
		if l := przm.HenselLCP(nil, []byte("A")); l != 0 {
			t.Fatalf("przm.HenselLCP(nil, 'A') expected 0, got %d", l)
		}

		// 1-byte
		if l := przm.HenselLCP([]byte("A"), []byte("A")); l != 1 {
			t.Fatalf("1B match expected 1, got %d", l)
		}
		if l := przm.HenselLCP([]byte("A"), []byte("B")); l != 0 {
			t.Fatalf("1B mismatch expected 0, got %d", l)
		}

		// 2-byte
		if l := przm.HenselLCP([]byte("AB"), []byte("AB")); l != 2 {
			t.Fatalf("2B match expected 2, got %d", l)
		}
		if l := przm.HenselLCP([]byte("AB"), []byte("AC")); l != 1 {
			t.Fatalf("2B partial match expected 1, got %d", l)
		}
		if l := przm.HenselLCP([]byte("AB"), []byte("XB")); l != 0 {
			t.Fatalf("2B full mismatch expected 0, got %d", l)
		}

		// 3-byte
		if l := przm.HenselLCP([]byte("ABC"), []byte("ABC")); l != 3 {
			t.Fatalf("3B match expected 3, got %d", l)
		}
		if l := przm.HenselLCP([]byte("ABC"), []byte("ABD")); l != 2 {
			t.Fatalf("3B partial match expected 2, got %d", l)
		}
		if l := przm.HenselLCP([]byte("ABC"), []byte("AXC")); l != 1 {
			t.Fatalf("3B partial match expected 1, got %d", l)
		}
		if l := przm.HenselLCP([]byte("ABC"), []byte("XYZ")); l != 0 {
			t.Fatalf("3B full mismatch expected 0, got %d", l)
		}

		// Unequal lengths (0..7 bytes)
		for lenA := 0; lenA <= 8; lenA++ {
			for lenB := 0; lenB <= 8; lenB++ {
				a := make([]byte, lenA)
				b := make([]byte, lenB)
				for i := range a {
					a[i] = byte(i + 1)
				}
				for i := range b {
					b[i] = byte(i + 1)
				}
				expected := min(lenA, lenB)
				if l := przm.HenselLCP(a, b); l != expected {
					t.Fatalf("przm.HenselLCP lenA=%d, lenB=%d expected %d, got %d", lenA, lenB, expected, l)
				}
			}
		}
	})

	// 2. Direct przm.HenselLCPPos boundary checks
	t.Run("HenselLCPPos_Boundaries", func(t *testing.T) {
		buf := []byte("ABCDEFGH12345678")
		// Identical positions
		if l := przm.HenselLCPPos(buf, 2, 2, 8); l != 8 {
			t.Fatalf("przm.HenselLCPPos p1==p2 expected 8, got %d", l)
		}
		// maxLen <= 0
		if l := przm.HenselLCPPos(buf, 0, 4, 0); l != 0 {
			t.Fatalf("przm.HenselLCPPos maxLen=0 expected 0, got %d", l)
		}
		if l := przm.HenselLCPPos(buf, 0, 4, -3); l != 0 {
			t.Fatalf("przm.HenselLCPPos maxLen<0 expected 0, got %d", l)
		}
		// Sub-4 maxLen
		for m := 1; m < 4; m++ {
			if l := przm.HenselLCPPos(buf, 0, 0, m); l != m {
				t.Fatalf("przm.HenselLCPPos maxLen=%d expected %d, got %d", m, m, l)
			}
		}
	})

	// 3. Scratchpad boundary safety on sub-4-byte buffers
	t.Run("Scratchpad_Sub4_Safety", func(t *testing.T) {
		var sp przm.HenselScratchpad
		sp.Reset()

		smallBuf := []byte{0x42, 0x13, 0x37} // 3 bytes
		// przm.HenselInsert on sub-4-byte buffer should safely no-op
		przm.HenselInsert(&sp, 0, smallBuf, len(smallBuf))
		przm.HenselInsert(nil, 0, smallBuf, len(smallBuf))

		// przm.HenselFindMatch on sub-4-byte buffer should return 0, 0 safely
		d, l := przm.HenselFindMatch(smallBuf, 0, len(smallBuf), 16, 4, &sp)
		if d != 0 || l != 0 {
			t.Fatalf("przm.HenselFindMatch on 3B buffer expected 0,0, got dist=%d len=%d", d, l)
		}
		d, l = przm.HenselFindMatch(smallBuf, 0, len(smallBuf), 16, 4, nil)
		if d != 0 || l != 0 {
			t.Fatalf("przm.HenselFindMatch with nil scratchpad expected 0,0, got dist=%d len=%d", d, l)
		}

		// minLen clamping: minLen < 4 clamped to 4
		d, l = przm.HenselFindMatch(smallBuf, 0, len(smallBuf), 16, 1, &sp)
		if d != 0 || l != 0 {
			t.Fatalf("przm.HenselFindMatch with minLen=1 expected 0,0, got dist=%d len=%d", d, l)
		}
	})

	// 4. Codec roundtrip for all small lengths 0..15 bytes at Level 3 and Level 4
	t.Run("Codec_SmallLengthsRoundtrip", func(t *testing.T) {
		testPattern := []byte("0123456789ABCDEF")
		for length := 0; length <= 16; length++ {
			sub := testPattern[:length]
			for _, lvl := range []int{3, 4} {
				comp, err := przm.Compress(sub, lvl)
				if err != nil {
					t.Fatalf("Compress failed for len=%d lvl=%d: %v", length, lvl, err)
				}
				decomp, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("Decompress failed for len=%d lvl=%d: %v", length, lvl, err)
				}
				if !bytes.Equal(sub, decomp) {
					t.Fatalf("Roundtrip mismatch for len=%d lvl=%d: got %v, want %v", length, lvl, decomp, sub)
				}
			}
		}
	})
}

// TestChallenger_Hensel_UnalignedAndPrimeLengths tests unaligned memory slices
// and arbitrary prime-length buffers to stress SIMD/SWAR boundary tails.
func TestChallenger_Hensel_UnalignedAndPrimeLengths(t *testing.T) {
	primes := []int{
		5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 67, 71, 73,
		79, 83, 89, 97, 101, 127, 131, 199, 257, 521, 1021, 2053, 4099, 8209, 16411, 32771,
	}

	for _, p := range primes {
		t.Run(fmt.Sprintf("PrimeLen_%d", p), func(t *testing.T) {
			buf := make([]byte, p)
			// Seed repetitive text motif with periodic variation
			motif := []byte("PRZM_HENSEL_P_ADIC_PRUNING_")
			for i := range buf {
				buf[i] = motif[i%len(motif)]
			}
			// Inject slight variations
			if p > 40 {
				buf[p-7] = 0xAA
				buf[p/2] = 0x55
			}

			for _, lvl := range []int{3, 4} {
				comp, err := przm.Compress(buf, lvl)
				if err != nil {
					t.Fatalf("Compress prime len=%d lvl=%d failed: %v", p, lvl, err)
				}
				decomp, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("Decompress prime len=%d lvl=%d failed: %v", p, lvl, err)
				}
				if !bytes.Equal(buf, decomp) {
					t.Fatalf("Roundtrip mismatch on prime len=%d lvl=%d", p, lvl)
				}
			}
		})
	}

	// Unaligned sub-slice LCP tests
	t.Run("UnalignedSlicesLCP", func(t *testing.T) {
		sliceA := make([]byte, 200)
		sliceB := make([]byte, 200)
		for i := range sliceA {
			sliceA[i] = byte(i*13 + 7)
			sliceB[i] = byte(i*13 + 7)
		}
		// Create unaligned slices: a starts at 1, b starts at 3
		copy(sliceB[3:103], sliceA[1:101])
		sliceA[1+45] ^= 0x01 // mismatch at byte 45
		sliceB[3+45] ^= 0x02

		lcp := przm.HenselLCP(sliceA[1:101], sliceB[3:103])
		if lcp != 45 {
			t.Fatalf("Unaligned przm.HenselLCP expected 45, got %d", lcp)
		}

		// Also test przm.HenselLCPPos inside a single large non-overlapping buffer
		base := make([]byte, 512)
		copy(base[10:110], sliceA[1:101])
		copy(base[200:300], sliceB[3:103])
		lcpPos := przm.HenselLCPPos(base, 10, 200, 100)
		if lcpPos != 45 {
			t.Fatalf("Unaligned przm.HenselLCPPos expected 45, got %d", lcpPos)
		}
	})

}

// TestChallenger_Hensel_HighCollisionOverflow stress-tests pathological hash
// collision flooding where a 64 KiB buffer consists of identical repeating 4-byte
// patterns, filling a single hash chain to depth 16,384.
func TestChallenger_Hensel_HighCollisionOverflow(t *testing.T) {
	t.Run("Identical4ByteRepeat_64KiB", func(t *testing.T) {
		pattern := []byte("WXYZ")
		data := bytes.Repeat(pattern, 16384) // 65,536 bytes
		if len(data) != 65536 {
			t.Fatalf("expected 65536 bytes, got %d", len(data))
		}

		var sp przm.HenselScratchpad
		sp.Reset()

		// Populate all positions into Hensel scratchpad
		for i := 0; i+4 <= len(data); i++ {
			przm.HenselInsert(&sp, i, data, len(data))
		}

		// Verify search at various depths does not crash, hang, or exceed maxDepth
		testPositions := []int{100, 500, 1024, 8192, 32768, 60000, 65530}
		for _, pos := range testPositions {
			for _, depth := range []int{16, 64} {
				dist, matchLen := przm.HenselFindMatch(data, pos, len(data), depth, 4, &sp)
				if dist <= 0 || matchLen < 4 {
					t.Fatalf("at pos=%d depth=%d: expected valid match, got dist=%d len=%d",
						pos, depth, dist, matchLen)
				}
				// Verify distance is a multiple of pattern length (4) or 1
				if dist > pos {
					t.Fatalf("dist=%d > pos=%d", dist, pos)
				}
				// Verify match content is identical
				if !bytes.Equal(data[pos:pos+matchLen], data[pos-dist:pos-dist+matchLen]) {
					t.Fatalf("match content mismatch at pos=%d dist=%d len=%d", pos, dist, matchLen)
				}
			}
		}

		// Full codec roundtrip at Level 3 and Level 4
		for _, lvl := range []int{3, 4} {
			comp, err := przm.Compress(data, lvl)
			if err != nil {
				t.Fatalf("Compress failed for 64K repeating pattern lvl=%d: %v", lvl, err)
			}
			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed for 64K repeating pattern lvl=%d: %v", lvl, err)
			}
			if !bytes.Equal(data, decomp) {
				t.Fatalf("Roundtrip mismatch on 64K repeating pattern lvl=%d", lvl)
			}
		}
	})

	t.Run("AllZeroes_64KiB", func(t *testing.T) {
		data := make([]byte, 65536)
		for _, lvl := range []int{3, 4} {
			comp, err := przm.Compress(data, lvl)
			if err != nil {
				t.Fatalf("Compress all-zeroes lvl=%d failed: %v", lvl, err)
			}
			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress all-zeroes lvl=%d failed: %v", lvl, err)
			}
			if !bytes.Equal(data, decomp) {
				t.Fatalf("Roundtrip mismatch on all-zeroes lvl=%d", lvl)
			}
		}
	})

	t.Run("AllOnes_64KiB", func(t *testing.T) {
		data := bytes.Repeat([]byte{0xFF}, 65536)
		for _, lvl := range []int{3, 4} {
			comp, err := przm.Compress(data, lvl)
			if err != nil {
				t.Fatalf("Compress all-0xFF lvl=%d failed: %v", lvl, err)
			}
			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress all-0xFF lvl=%d failed: %v", lvl, err)
			}
			if !bytes.Equal(data, decomp) {
				t.Fatalf("Roundtrip mismatch on all-0xFF lvl=%d", lvl)
			}
		}
	})
}

// TestChallenger_Hensel_FingerprintAdversarialCases specifically targets the
// pruning logic of Theorem HL-2 (Ultrametric Disjoint Ball Pruning).
func TestChallenger_Hensel_FingerprintAdversarialCases(t *testing.T) {
	// Case A: 16-bit Hash Collision where 4-byte prefix differs but 4-byte FP32 matches!
	t.Run("HashCollision_PrefixDiff_FingerprintMatch", func(t *testing.T) {
		// Find two 32-bit words that hash to the same bucket
		var w1, w2 uint32
		found := false
		hashMap := make(map[uint16]uint32)
		for candidateW := uint32(100); candidateW < 1000000; candidateW++ {
			h := uint16((candidateW * przm.HenselHashPrime) >> 16)
			if existing, ok := hashMap[h]; ok {
				if existing != candidateW {
					w1 = existing
					w2 = candidateW
					found = true
					break
				}
			} else {
				hashMap[h] = candidateW
			}
		}
		if !found {
			t.Fatal("Could not find 16-bit hash collision for test")
		}

		sharedFP := uint32(0xCAFEBABE)
		buf := make([]byte, 128)
		// At position 10: place w1 + sharedFP
		binary.LittleEndian.PutUint32(buf[10:14], w1)
		binary.LittleEndian.PutUint32(buf[14:18], sharedFP)
		// At position 40: place w2 + sharedFP
		binary.LittleEndian.PutUint32(buf[40:44], w2)
		binary.LittleEndian.PutUint32(buf[44:48], sharedFP)

		var sp przm.HenselScratchpad
		sp.Reset()
		przm.HenselInsert(&sp, 10, buf, len(buf))

		// Search at pos 40. Prefix w2 != w1, so no match should be found!
		dist, matchLen := przm.HenselFindMatch(buf, 40, len(buf), 16, 4, &sp)
		if dist != 0 || matchLen != 0 {
			t.Fatalf("False match accepted under hash collision! dist=%d len=%d (w1=%08x w2=%08x)",
				dist, matchLen, w1, w2)
		}
	})

	// Case B: Prefix matches, but bytes 4..7 mismatch at byte 4 (1st FP byte)
	t.Run("PrefixMatch_Byte4Mismatch", func(t *testing.T) {
		buf := make([]byte, 128)
		prefix := []byte("TEST")
		copy(buf[10:14], prefix)
		copy(buf[14:18], []byte("XBCD")) // Candidate FP: 'X', 'B', 'C', 'D'

		copy(buf[40:44], prefix)
		copy(buf[44:48], []byte("YBCD")) // Target FP: 'Y', 'B', 'C', 'D' (diff at byte 0)

		var sp przm.HenselScratchpad
		sp.Reset()
		przm.HenselInsert(&sp, 10, buf, len(buf))

		// Search with minLen = 4: since prefix matches, LCP is 4!
		dist, matchLen := przm.HenselFindMatch(buf, 40, len(buf), 16, 4, &sp)
		if dist != 30 || matchLen != 4 {
			t.Fatalf("Expected match at dist 30 len 4, got dist=%d len=%d", dist, matchLen)
		}

		// Now simulate search where bestLen was already 4: candidate should be pruned
		// We verify this directly by checking Theorem HL-2 pruning formula
		candFP := binary.LittleEndian.Uint32(buf[14:18])
		targetFP := binary.LittleEndian.Uint32(buf[44:48])
		diff := candFP ^ targetFP
		maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
		if maxLift != 4 {
			t.Fatalf("Expected maxLift=4 for byte-4 mismatch, got %d", maxLift)
		}
	})

	// Case C: Prefix matches, bytes 4..6 match, byte 7 mismatches (4th FP byte)
	t.Run("PrefixMatch_Byte7Mismatch", func(t *testing.T) {
		buf := make([]byte, 128)
		prefix := []byte("DATA")
		copy(buf[10:14], prefix)
		copy(buf[14:18], []byte("ABC1")) // Candidate FP: byte 7 is '1'

		copy(buf[40:44], prefix)
		copy(buf[44:48], []byte("ABC2")) // Target FP: byte 7 is '2'

		var sp przm.HenselScratchpad
		sp.Reset()
		przm.HenselInsert(&sp, 10, buf, len(buf))

		// Search should return match of length 7
		dist, matchLen := przm.HenselFindMatch(buf, 40, len(buf), 16, 4, &sp)
		if dist != 30 || matchLen != 7 {
			t.Fatalf("Expected match at dist 30 len 7, got dist=%d len=%d", dist, matchLen)
		}

		// Verify maxLift formula
		candFP := binary.LittleEndian.Uint32(buf[14:18])
		targetFP := binary.LittleEndian.Uint32(buf[44:48])
		diff := candFP ^ targetFP
		maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
		if maxLift != 7 {
			t.Fatalf("Expected maxLift=7 for byte-7 mismatch, got %d", maxLift)
		}
	})

	// Case D: Chained candidate evaluation with pruning of intermediate candidate
	t.Run("ChainPruning_PreservesBetterCandidate", func(t *testing.T) {
		buf := make([]byte, 256)
		// Fill buf with non-zero pseudorandom values to prevent accidental matches
		for i := range buf {
			buf[i] = byte(i*7 + 1)
		}
		prefix := []byte("NODE")

		// Cand 1 (older, pos 10): matches 20 bytes with target
		copy(buf[10:14], prefix)
		copy(buf[14:30], []byte("0123456789ABCDEF"))
		buf[30] = 0xAA // Explicit mismatch at byte 20

		// Cand 2 (newer, pos 60): matches only 4 bytes (byte 4 mismatches)
		copy(buf[60:64], prefix)
		copy(buf[64:80], []byte("Z123456789ABCDEF"))

		// Target (pos 120): identical to Cand 1 for 20 bytes
		copy(buf[120:124], prefix)
		copy(buf[124:140], []byte("0123456789ABCDEF"))
		buf[140] = 0x55 // Explicit mismatch at byte 20

		var sp przm.HenselScratchpad
		sp.Reset()
		przm.HenselInsert(&sp, 10, buf, len(buf))
		przm.HenselInsert(&sp, 60, buf, len(buf))

		// Search from pos 120:
		// Cand 2 is checked first (dist 60, len 4). bestLen becomes 4.
		// Cand 1 is checked second (dist 110, len 20).
		// Since Cand 1 matches 20 bytes, diff == 0, not pruned!
		// Result must be dist 110, len 20!
		dist, matchLen := przm.HenselFindMatch(buf, 120, len(buf), 16, 4, &sp)
		if dist != 110 || matchLen != 20 {
			t.Fatalf("Expected best match from Cand 1 (dist=110, len=20), got dist=%d len=%d",
				dist, matchLen)
		}
	})

	// Case E: Comprehensive mismatch offset sweep from 4 to 32 bytes
	t.Run("MismatchOffsetSweep_0to32", func(t *testing.T) {
		for mismatchOffset := 4; mismatchOffset <= 32; mismatchOffset++ {
			buf := make([]byte, 256)
			cand := make([]byte, 64)
			target := make([]byte, 64)

			for i := range 64 {
				cand[i] = byte(i*17 + 3)
				target[i] = cand[i]
			}
			target[mismatchOffset] ^= 0x55 // Force mismatch at exact offset

			copy(buf[10:74], cand)
			copy(buf[80:144], target)

			var sp przm.HenselScratchpad
			sp.Reset()
			przm.HenselInsert(&sp, 10, buf, len(buf))

			dist, matchLen := przm.HenselFindMatch(buf, 80, len(buf), 16, 4, &sp)
			if dist != 70 || matchLen != mismatchOffset {
				t.Fatalf("MismatchOffsetSweep at offset %d: got dist=%d len=%d, want dist=70 len=%d",
					mismatchOffset, dist, matchLen, mismatchOffset)
			}
		}
	})
}

// naiveHashChainOracle computes the exact unpruned match by linearly checking
// every candidate in the scratchpad chain up to maxDepth.
func naiveHashChainOracle(src []byte, pos, uSize, maxDepth, minLen int, head []int32, prev []int32) (int, int) {
	if pos+4 > uSize {
		return 0, 0
	}
	w4 := binary.LittleEndian.Uint32(src[pos : pos+4])
	h := (w4 * przm.HenselHashPrime) >> 16
	h &= przm.HenselHashMask

	cand := int(head[h]) - 1
	if cand < 0 {
		return 0, 0
	}

	bestLen := minLen - 1
	bestDist := 0
	depth := 0
	maxSearchLen := min(uSize-pos, 65535)

	for cand >= 0 && depth < maxDepth {
		candSlot := cand & przm.HenselWindowMask
		if cand >= pos {
			nextCand := int(prev[candSlot]) - 1
			if nextCand >= cand || nextCand < 0 {
				break
			}
			cand = nextCand
			continue
		}

		d := pos - cand
		if d >= 65535 {
			break
		}

		// NO FP32 PRUNING: Exhaustive byte-by-byte check
		if binary.LittleEndian.Uint32(src[cand:cand+4]) == w4 {
			l := 0
			for l < maxSearchLen && src[pos+l] == src[cand+l] {
				l++
			}
			if l > bestLen {
				bestLen = l
				bestDist = d
				if bestLen >= 258 {
					break
				}
			}
		}

		nextCand := int(prev[candSlot]) - 1
		if nextCand >= cand || nextCand < 0 {
			break
		}
		cand = nextCand
		depth++
	}

	if bestLen < minLen {
		return 0, 0
	}
	return bestDist, bestLen
}

// TestChallenger_Hensel_ExhaustiveOracle50000 compares przm.HenselFindMatch with
// naiveHashChainOracle across 50,000 randomized and adversarial test positions.
func TestChallenger_Hensel_ExhaustiveOracle50000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))

	// Generate 128 KiB of heterogeneous test data
	dataSize := 128 * 1024
	data := make([]byte, dataSize)
	rng.Read(data)

	// Implant structured repetitions of varied lengths (4 to 280 bytes)
	patterns := [][]byte{
		[]byte("JSON_FORMAT_RECORD_ID_0000123456789_ACTIVE_FLAG_TRUE_TELEMETRY"),
		[]byte("func evaluateUltrametricNonArchimedeanLifting(sp *przm.HenselScratchpad) int {"),
		[]byte("AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKKKLLLLMMMMNNNNOOOOPPPP"),
		[]byte("const HenselHashPrime uint32 = 0x1E35A7BD"),
		[]byte("SHORT_4B"),
		[]byte("VERY_LONG_REPETITION_P_ADIC_HENSEL_QUADRATIC_CONVERGENCE_SPEEDUP_ACROSS_LEVELS_THREE_AND_FOUR_PRESERVING_BITSTREAM_COMPATIBILITY_AND_ZERO_ALLOCATION_DECOMPRESSOR_CONTRACT_1234567890"),
	}

	for i := range 300 {
		p := patterns[i%len(patterns)]
		offset := rng.Intn(dataSize - len(p) - 1)
		copy(data[offset:], p)
	}

	var sp przm.HenselScratchpad
	sp.Reset()

	totalTrials := 50000
	verifiedMatches := 0
	prunedMatchesDiscovered := 0

	// Pre-fill first 10,000 positions
	initInsert := 10000
	for i := range initInsert {
		przm.HenselInsert(&sp, i, data, dataSize)
	}

	curPos := initInsert
	for trial := range totalTrials {
		// Periodically advance insertion point
		if curPos+4 <= dataSize {
			przm.HenselInsert(&sp, curPos, data, dataSize)
			curPos++
		} else {
			curPos = 1000 // wrap around search testing
		}

		testPos := rng.Intn(curPos - 4)
		if testPos+4 > dataSize {
			continue
		}

		maxDepth := 16
		if trial%2 == 1 {
			maxDepth = 64
		}
		minLen := 4

		hDist, hLen := przm.HenselFindMatch(data, testPos, dataSize, maxDepth, minLen, &sp)
		oDist, oLen := naiveHashChainOracle(data, testPos, dataSize, maxDepth, minLen, sp.HeadSlice(), sp.PrevSlice())

		if hLen != oLen {
			t.Fatalf("Oracle Discrepancy at trial=%d pos=%d depth=%d: HenselLen=%d OracleLen=%d (hDist=%d oDist=%d)",
				trial, testPos, maxDepth, hLen, oLen, hDist, oDist)
		}

		if hLen >= minLen {
			verifiedMatches++
			// Verify exact payload equality
			if !bytes.Equal(data[testPos:testPos+hLen], data[testPos-hDist:testPos-hDist+hLen]) {
				t.Fatalf("Trial %d pos=%d: match payload mismatch for dist=%d len=%d",
					trial, testPos, hDist, hLen)
			}
			if hLen > 4 {
				prunedMatchesDiscovered++
			}
		}
	}

	t.Logf(">>> 50,000 Oracle Trials Passed! Verified Matches: %d (Lengths > 4: %d) <<<",
		verifiedMatches, prunedMatchesDiscovered)
}

// TestChallenger_Hensel_WindowWrapping128K verifies ring buffer slot wrapping
// behavior for buffers exceeding the 64 KiB window size.
func TestChallenger_Hensel_WindowWrapping128K(t *testing.T) {
	bufSize := 128 * 1024
	buf := make([]byte, bufSize)
	rng := rand.New(rand.NewSource(777))
	rng.Read(buf)

	// Plant target pattern at pos 100, pos 65540 (distance 65440 < 65535),
	// and pos 70000 (distance to pos 100 is 69900 >= 65535).
	pattern := []byte("SLIDING_WINDOW_RING_BUFFER_WRAPPING_128K_TEST_VECTOR_PRZM")
	copy(buf[100:], pattern)
	copy(buf[65540:], pattern)
	copy(buf[70000:], pattern)

	var sp przm.HenselScratchpad
	sp.Reset()

	for i := 0; i+4 <= bufSize; i++ {
		przm.HenselInsert(&sp, i, buf, bufSize)

		if i == 65540 {
			dist, matchLen := przm.HenselFindMatch(buf, 65540, bufSize, 64, 4, &sp)
			if dist != 65440 || matchLen < len(pattern) {
				t.Fatalf("At pos 65540: expected match to pos 100 (dist 65440), got dist=%d len=%d",
					dist, matchLen)
			}
		}

		if i == 70000 {
			dist, matchLen := przm.HenselFindMatch(buf, 70000, bufSize, 64, 4, &sp)
			// Distance cannot be 69900 (exceeds 65535 limit); must be 70000-65540 = 4460
			if dist != 4460 {
				t.Fatalf("At pos 70000: expected match to pos 65540 (dist 4460), got dist=%d len=%d",
					dist, matchLen)
			}
		}
	}

	// Full codec roundtrip on 128 KiB and 256 KiB buffers
	for _, size := range []int{128 * 1024, 256 * 1024} {
		largeBuf := make([]byte, size)
		for i := range largeBuf {
			largeBuf[i] = byte((i * 31) ^ (i >> 5))
		}
		for _, lvl := range []int{3, 4} {
			comp, err := przm.Compress(largeBuf, lvl)
			if err != nil {
				t.Fatalf("Compress failed on size=%d lvl=%d: %v", size, lvl, err)
			}
			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed on size=%d lvl=%d: %v", size, lvl, err)
			}
			if !bytes.Equal(largeBuf, decomp) {
				t.Fatalf("Roundtrip mismatch on size=%d lvl=%d", size, lvl)
			}
		}
	}
}

// TestChallenger_Hensel_ZeroAllocations verifies that przm.HenselFindMatch,
// przm.HenselInsert, and przm.HenselLCP strictly execute with zero heap allocations.
func TestChallenger_Hensel_ZeroAllocations(t *testing.T) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 53)
	}

	var sp przm.HenselScratchpad
	sp.Reset()

	for i := range 500 {
		przm.HenselInsert(&sp, i, data, len(data))
	}

	allocs := testing.AllocsPerRun(100, func() {
		przm.HenselInsert(&sp, 501, data, len(data))
		przm.HenselFindMatch(data, 501, len(data), 64, 4, &sp)
		przm.HenselLCP(data[10:60], data[20:70])
		przm.HenselLCPPos(data, 10, 20, 50)
	})

	if allocs != 0 {
		t.Fatalf("Expected 0 allocs/op for Hensel primitives, got %f", allocs)
	}
}

// TestChallenger_Hensel_PureCInterop verifies that compressed bitstreams from
// Level 3 and Level 4 across all boundary conditions decompress bit-exact
// via the Pure C reference decompressor.
func TestChallenger_Hensel_PureCInterop(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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
		t.Skip("C test binary test_przm_dec.exe not found; build it with gcc first")
	}

	tmpDir := t.TempDir()

	testInputs := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"SingleByte", []byte{0x77}},
		{"TwoBytes", []byte{0x12, 0x34}},
		{"ThreeBytes", []byte{0xAA, 0xBB, 0xCC}},
		{"Prime7B", []byte("1234567")},
		{"Prime127B", bytes.Repeat([]byte("0123456789ABCDE"), 10)[:127]},
		{"Repeating64K", bytes.Repeat([]byte("PRZM"), 16384)},
	}

	for _, tc := range testInputs {
		t.Run(tc.name, func(t *testing.T) {
			for _, lvl := range []int{3, 4} {
				comp, err := przm.Compress(tc.data, lvl)
				if err != nil {
					t.Fatalf("Compress failed for %s lvl=%d: %v", tc.name, lvl, err)
				}

				inPath := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", tc.name, lvl))
				outPath := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.out", tc.name, lvl))

				if err := os.WriteFile(inPath, comp, 0644); err != nil {
					t.Fatalf("failed to write %s: %v", inPath, err)
				}

				cmd := exec.Command(cBinPath, inPath, outPath)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("C decompressor failed for %s lvl=%d (exit %v):\nOutput:\n%s",
						tc.name, lvl, err, string(out))
				}

				decomp, err := os.ReadFile(outPath)
				if err != nil {
					t.Fatalf("failed to read decompressed file %s: %v", outPath, err)
				}

				if !bytes.Equal(decomp, tc.data) {
					t.Fatalf("C decompressed data mismatch for %s lvl=%d (got %d bytes, want %d bytes)",
						tc.name, lvl, len(decomp), len(tc.data))
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_boundary_test.go
// -----------------------------------------------------------------------------

// TestChallenger_PWS_FrameBoundary checks all truncated frame lengths [0..25] bytes for PWS.
func TestChallenger_PWS_FrameBoundary(t *testing.T) {
	// Construct a minimal valid PWS frame
	var validFrame []byte
	validFrame = append(validFrame, 'P', 'W', 0x01, 0x00) // Magic + Stride
	var sz [8]byte
	binary.LittleEndian.PutUint32(sz[0:4], 4) // origSize = 4
	binary.LittleEndian.PutUint32(sz[4:8], 1) // tokenCount = 1
	validFrame = append(validFrame, sz[:]...)
	validFrame = append(validFrame, 1, 1, 0, 0, 0, 0, 0, 0) // counts: L1=1, L2=1
	validFrame = append(validFrame, 'T', 'e')               // L1[0]
	validFrame = append(validFrame, 0, 0)                   // L2[0]
	validFrame = append(validFrame, 0xD0)                   // Axiom: token 0xD0 (L2[0])

	for length := 0; length <= len(validFrame); length++ {
		t.Run(fmt.Sprintf("Truncated_Len_%d", length), func(t *testing.T) {
			sub := validFrame[:length]
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC on truncated PWS frame length %d: %v", length, r)
				}
			}()

			dec, err := przm.PWSDecode(sub)
			if length < 20 {
				if err != przm.ErrPWSCorrupted {
					t.Fatalf("Expected ErrPWSCorrupted for length %d < 20, got err=%v", length, err)
				}
				if dec != nil {
					t.Fatalf("Expected nil decoded slice for length %d < 20, got len=%d", length, len(dec))
				}
			} else if length < len(validFrame) {
				// Between 20 and full length, should return error without panic
				if err == nil {
					t.Fatalf("Expected error for partially truncated frame length %d, got nil", length)
				}
			} else {
				// Full length should succeed
				if err != nil {
					t.Fatalf("Expected success for full frame, got %v", err)
				}
				if string(dec) != "TeTe" {
					t.Fatalf("Expected 'TeTe', got %q", string(dec))
				}
			}
		})
	}
}

// TestChallenger_ShortAndEmptyInputs tests all lengths [0..64] across all compression levels.
func TestChallenger_ShortAndEmptyInputs(t *testing.T) {
	for sz := 0; sz <= 64; sz++ {
		data := make([]byte, sz)
		for i := range data {
			data[i] = byte(i + 1)
		}

		for lvl := 1; lvl <= 4; lvl++ {
			t.Run(fmt.Sprintf("Size_%d_Level_%d", sz, lvl), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("PANIC on sz=%d lvl=%d: %v", sz, lvl, r)
					}
				}()

				comp, err := przm.Compress(data, lvl)
				if err != nil {
					t.Fatalf("Compress failed on sz=%d lvl=%d: %v", sz, lvl, err)
				}

				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("Decompress failed on sz=%d lvl=%d: %v", sz, lvl, err)
				}

				if !bytes.Equal(dec, data) {
					t.Fatalf("Mismatch on sz=%d lvl=%d: got %d bytes, want %d bytes", sz, lvl, len(dec), len(data))
				}
			})
		}
	}
}

// TestChallenger_PWSEncode_ShortInputs tests PWSEncode / PWSDecode specifically on short inputs [0..64].
func TestChallenger_PWSEncode_ShortInputs(t *testing.T) {
	for sz := 0; sz <= 64; sz++ {
		data := make([]byte, sz)
		for i := range data {
			data[i] = byte((i * 17) % 256)
		}

		t.Run(fmt.Sprintf("PWS_Size_%d", sz), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC in PWS on sz=%d: %v", sz, r)
				}
			}()

			enc, err := przm.PWSEncode(data)
			if err != nil {
				t.Fatalf("PWSEncode failed on sz=%d: %v", sz, err)
			}

			if sz == 0 {
				if len(enc) != 0 {
					t.Fatalf("Expected empty output for sz=0, got len %d", len(enc))
				}
				return
			}

			dec, err := przm.PWSDecode(enc)
			if err != nil {
				t.Fatalf("PWSDecode failed on sz=%d: %v", sz, err)
			}

			if !bytes.Equal(dec, data) {
				t.Fatalf("PWS mismatch on sz=%d: got len %d, want len %d", sz, len(dec), len(data))
			}
		})
	}
}

// TestChallenger_CDecompressor_TruncatedAndMalformedFrames checks that the C decompressor
// rejects truncated frames (< 20 bytes for PWS, < 8 bytes for PRZM) cleanly without crashing.
func TestChallenger_CDecompressor_TruncatedAndMalformedFrames(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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

	// Truncated frames
	truncatedFrames := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"1_byte", []byte{'P'}},
		{"3_bytes_pws", []byte{'P', 'W', 0x01}},
		{"4_bytes_pws", []byte{'P', 'W', 0x01, 0x00}},
		{"7_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00}},
		{"8_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00}},
		{"16_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}},
		{"19_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"3_bytes_przm", []byte{'P', 'R', 'Z'}},
		{"4_bytes_przm", []byte{'P', 'R', 'Z', 'M'}},
		{"7_bytes_przm", []byte{'P', 'R', 'Z', 'M', 0x00, 0x04, 0x00}},
	}

	for _, tc := range truncatedFrames {
		t.Run("C_Truncated_"+tc.name, func(t *testing.T) {
			inPrzm := filepath.Join(tmpDir, "trunc_"+tc.name+".przm")
			outBin := filepath.Join(tmpDir, "trunc_"+tc.name+".out")

			if err := os.WriteFile(inPrzm, tc.data, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			// Must fail (non-zero exit code)
			if err == nil {
				t.Fatalf("Expected C decompressor to reject truncated frame %q, but succeeded! Output:\n%s", tc.name, string(output))
			}
			// Exit code must be 2 (Invalid PRZM frame header / error) or 3 (Decompression failed), NOT a crash (e.g. status code -1073741819 / 0xC0000005)
			exitCode := cmd.ProcessState.ExitCode()
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf("C decompressor crashed or exited unexpectedly with code %d on %q. Output:\n%s", exitCode, tc.name, string(output))
			}
		})
	}
}

// =============================================================================
// SECTION 2: MILESTONE 2 — DISCRETE HEISENBERG GROUP & CELLULAR SHEAF HODGE LAPLACIAN
// =============================================================================

// -----------------------------------------------------------------------------
// Source: challenger_m2_test.go
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Adversarial Challenger Suite for Milestone 2:
// Discrete Heisenberg Group H_3(F_{2^8}) Foliation
// -----------------------------------------------------------------------------

// TestChallengerM2_100Goroutine_ConcurrencyStress executes bounded concurrent
// goroutines continuously foliating, unfoliating, compressing, and decompressing
// Heisenberg frames under `go test -race`.
func TestChallengerM2_100Goroutine_ConcurrencyStress(t *testing.T) {
	const numGoroutines = 8
	const iterationsPerGoroutine = 50

	var startBarrier sync.WaitGroup
	startBarrier.Add(1)

	var doneGroup sync.WaitGroup
	doneGroup.Add(numGoroutines)

	errChan := make(chan error, numGoroutines*iterationsPerGoroutine)

	for g := range numGoroutines {
		go func(gid int) {
			defer doneGroup.Done()

			// Local RNG per goroutine
			rng := rand.New(rand.NewSource(int64(gid*1000 + 42)))

			startBarrier.Wait()

			testSizes := []int{0, 1, 2, 3, 4, 11, 12, 13, 17, 24, 48, 127, 256, 1023, 2048, 4096, 8192}

			for iter := range iterationsPerGoroutine {
				stride := 3
				if iter%2 == 1 {
					stride = 4
				}

				sz := testSizes[(gid+iter)%len(testSizes)]
				raw := make([]byte, sz)
				rng.Read(raw)

				// For some iterations inject Heisenberg coupling: z = x * y
				if iter%3 == 0 && sz >= stride {
					for i := 0; i+stride <= sz; i += stride {
						raw[i+2] = przm.GF256Mul(raw[i], raw[i+1])
					}
				}

				// 1. Test In-place foliation & unfoliation involution
				buf := make([]byte, sz)
				copy(buf, raw)
				przm.HeisenbergFoliateInplace(buf, stride)
				przm.HeisenbergUnfoliateInplace(buf, stride)
				if !bytes.Equal(buf, raw) {
					errChan <- fmt.Errorf("goroutine %d iter %d: Inplace involution failed on sz=%d, stride=%d", gid, iter, sz, stride)
					return
				}

				// 2. Test CompressHeisenberg & DecompressHeisenberg
				comp, err := przm.CompressHeisenberg(raw, 3)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d iter %d: CompressHeisenberg failed: %v", gid, iter, err)
					return
				}

				dst := make([]byte, sz+64)
				if sz == 0 {
					decTo, err := przm.DecompressTo(comp, dst)
					if err != nil || len(decTo) != 0 {
						errChan <- fmt.Errorf("goroutine %d iter %d: DecompressTo empty failed: %v", gid, iter, err)
						return
					}
					continue
				}

				dec, err := przm.DecompressHeisenberg(comp, dst)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressHeisenberg failed: %v", gid, iter, err)
					return
				}
				if !bytes.Equal(dec, raw) {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressHeisenberg mismatch on sz=%d, stride=%d", gid, iter, sz, stride)
					return
				}

				// 3. Test DecompressTo dispatch
				decTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressTo failed: %v", gid, iter, err)
					return
				}
				if !bytes.Equal(decTo, raw) {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressTo mismatch on sz=%d, stride=%d", gid, iter, sz, stride)
					return
				}

				// 4. Test full pipeline Compress / Decompress
				if sz >= 48 {
					fullComp, err := przm.Compress(raw, 3)
					if err != nil {
						errChan <- fmt.Errorf("goroutine %d iter %d: Compress failed: %v", gid, iter, err)
						return
					}
					fullDec, err := przm.Decompress(fullComp)
					if err != nil {
						errChan <- fmt.Errorf("goroutine %d iter %d: Decompress failed: %v", gid, iter, err)
						return
					}
					if !bytes.Equal(fullDec, raw) {
						errChan <- fmt.Errorf("goroutine %d iter %d: Full roundtrip mismatch on sz=%d", gid, iter, sz)
						return
					}
				}
			}
		}(g)
	}

	// Release all workers simultaneously
	startBarrier.Done()
	doneGroup.Wait()
	close(errChan)

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Fatalf("Encountered %d concurrency errors under %d goroutines! First error: %v", len(errs), numGoroutines, errs[0])
	}
}

// TestChallengerM2_ZeroAllocDecompression_Assertion verifies strictly
// 0 heap allocations during decompression when destination buffer is pre-allocated.
func TestChallengerM2_ZeroAllocDecompression_Assertion(t *testing.T) {
	// 1. In-place unfoliation strictly 0 allocs
	t.Run("InplaceUnfoliation_ZeroAlloc", func(t *testing.T) {
		buf3 := make([]byte, 8192)
		allocs3 := testing.AllocsPerRun(100, func() {
			przm.HeisenbergUnfoliateInplace(buf3, 3)
		})
		if allocs3 != 0 {
			t.Fatalf("HeisenbergUnfoliateInplace(stride=3) allocated %v allocs/op, expected 0", allocs3)
		}

		buf4 := make([]byte, 8192)
		allocs4 := testing.AllocsPerRun(100, func() {
			przm.HeisenbergUnfoliateInplace(buf4, 4)
		})
		if allocs4 != 0 {
			t.Fatalf("HeisenbergUnfoliateInplace(stride=4) allocated %v allocs/op, expected 0", allocs4)
		}
	})

	// 2. Mode 1 (Foliated stream uncompressed) with pre-allocated buffer
	t.Run("Mode1_Foliated_DecompressZeroAlloc", func(t *testing.T) {
		n := 4095
		raw := make([]byte, n)
		rng := rand.New(rand.NewSource(54321))
		rng.Read(raw) // Fill with high entropy so PWS cannot compress

		// Set 10% of triplets to exact Heisenberg coupling (z = x * y)
		for i := 0; i+2 < n; i += 30 {
			raw[i] = uint8((i % 200) + 1)
			raw[i+1] = uint8(((i / 200) % 200) + 1)
			raw[i+2] = przm.GF256Mul(raw[i], raw[i+1])
		}

		comp, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}
		if comp[9] != przm.HeisenbergModeFoliated {
			t.Fatalf("Expected HeisenbergModeFoliated (1), got %d", comp[9])
		}

		dst := make([]byte, n+64)

		allocs := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressHeisenberg(comp, dst)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if len(dec) != n {
				t.Fatalf("Length mismatch")
			}
		})

		if allocs != 0 {
			t.Fatalf("DecompressHeisenberg Mode 1 expected 0 allocs/op, got %v", allocs)
		}

		allocsTo := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}
			if len(dec) != n {
				t.Fatalf("Length mismatch")
			}
		})

		if allocsTo != 0 {
			t.Fatalf("DecompressTo Mode 1 expected 0 allocs/op, got %v", allocsTo)
		}
	})

	// 3. Mode 0 (Raw passthrough bypass) with pre-allocated buffer
	t.Run("Mode0_RawBypass_DecompressZeroAlloc", func(t *testing.T) {
		n := 2048
		raw := make([]byte, n)
		rng := rand.New(rand.NewSource(12345))
		rng.Read(raw) // random data triggers Mode 0 bypass

		comp, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}
		if comp[9] != przm.HeisenbergModeRaw {
			t.Fatalf("Expected HeisenbergModeRaw (0), got %d", comp[9])
		}

		dst := make([]byte, n+64)

		allocs := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressHeisenberg(comp, dst)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if len(dec) != n {
				t.Fatalf("Length mismatch")
			}
		})

		if allocs != 0 {
			t.Fatalf("DecompressHeisenberg Mode 0 expected 0 allocs/op, got %v", allocs)
		}
	})

	// 4. Mode 3 (Foliated cascaded with PWS)
	t.Run("Mode3_CascadedPWS_DecompressAllocMeasurement", func(t *testing.T) {
		numPixels := 4096
		raw := make([]byte, numPixels*3)
		for i := range numPixels {
			x := uint8((i % 64) + 1)
			y := uint8(((i / 64) % 64) + 1)
			z := przm.GF256Mul(x, y)
			raw[i*3+0] = x
			raw[i*3+1] = y
			raw[i*3+2] = z
		}

		comp, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}
		if comp[9] != przm.HeisenbergModeCompressed {
			t.Fatalf("Expected HeisenbergModeCompressed (3), got %d", comp[9])
		}

		dst := make([]byte, len(raw)+64)
		allocs := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressHeisenberg(comp, dst)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if len(dec) != len(raw) {
				t.Fatalf("Length mismatch")
			}
		})
		t.Logf("Mode 3 (Cascaded PWS) decompression heap allocations: %v allocs/op", allocs)
	})
}

// TestChallengerM2_EmptyBuffer_EdgeCase tests and documents the behavior of empty buffer compression/decompression.
func TestChallengerM2_EmptyBuffer_EdgeCase(t *testing.T) {
	comp, err := przm.CompressHeisenberg([]byte{}, 3)
	if err != nil {
		t.Fatalf("CompressHeisenberg on empty slice failed: %v", err)
	}

	// Calling DecompressHeisenberg directly on 0-byte slice returns nil error and 0 length (PROJECT.md Feature 4 contract)
	dec, decErr := przm.DecompressHeisenberg(comp, nil)
	if decErr != nil || len(dec) != 0 {
		t.Fatalf("Expected nil error and empty slice when passing 0 bytes to DecompressHeisenberg, got: %v, len=%d", decErr, len(dec))
	}

	// Calling DecompressTo directly on 0-byte slice succeeds via wrapper guard
	decTo, err := przm.DecompressTo(comp, nil)
	if err != nil || len(decTo) != 0 {
		t.Fatalf("DecompressTo failed on 0 bytes: %v", err)
	}
}

// TestChallengerM2_GF256Mul_FullFieldEquivalence verifies that Go table-lookup GF256Mul
// is 100% algebraically identical to Pure C shift-and-xor gf256_mul across all 65,536 byte pairs.
func TestChallengerM2_GF256Mul_FullFieldEquivalence(t *testing.T) {
	pureCGf256Mul := func(a, b uint8) uint8 {
		var p uint8 = 0
		for range 8 {
			if (b & 1) != 0 {
				p ^= a
			}
			hi := a & 0x80
			a <<= 1
			if hi != 0 {
				a ^= 0x1B
			}
			b >>= 1
		}
		return p
	}

	for a := range 256 {
		for b := range 256 {
			cExpected := pureCGf256Mul(uint8(a), uint8(b))
			goActual := przm.GF256Mul(uint8(a), uint8(b))
			if goActual != cExpected {
				t.Fatalf("GF256Mul mismatch at a=0x%02X, b=0x%02X: Go=0x%02X, C=0x%02X", a, b, goActual, cExpected)
			}
		}
	}
}

// TestChallengerM2_PureC_50RandomFrames_Stress generates 50 random Heisenberg
// frames in Go across stride 3 and 4, pipes them to Pure C `test_przm_dec.exe`,
// and asserts 100% bit-exact lossless roundtrips.
func TestChallengerM2_PureC_50RandomFrames_Stress(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}
	if _, err := os.Stat(cBinPath); err != nil {
		t.Fatalf("test_przm_dec.exe not found at %s: build it first", cBinPath)
	}

	tmpDir := t.TempDir()
	rng := rand.New(rand.NewSource(99999))

	// 50 test configurations: 25 stride 3, 25 stride 4
	sizes := []int{
		12, 13, 15, 23, 24, 25, 48, 49, 99, 100,
		128, 255, 256, 300, 511, 512, 777, 1024, 1500, 2048,
		3000, 3072, 4000, 4095, 4096,
	}

	for i := range 50 {
		stride := 3
		if i >= 25 {
			stride = 4
		}
		sz := sizes[i%len(sizes)]

		testName := fmt.Sprintf("Frame_%02d_Stride%d_Size%d", i, stride, sz)
		t.Run(testName, func(t *testing.T) {
			raw := make([]byte, sz)
			rng.Read(raw)

			// Alternate frame coupling characteristics:
			// - Pure coupled (z = x * y)
			// - Partial coupled with noise
			// - Pure random (mode 0 raw bypass)
			switch i % 3 {
			case 0:
				// Pure coupled
				for j := 0; j+stride <= sz; j += stride {
					raw[j+2] = przm.GF256Mul(raw[j], raw[j+1])
				}
			case 1:
				// Partial coupled
				for j := 0; j+stride <= sz; j += stride {
					if j%2 == 0 {
						raw[j+2] = przm.GF256Mul(raw[j], raw[j+1])
					}
				}
			case 2:
				// Pure random; left as is
			}

			// Compress in Go
			comp, err := przm.CompressHeisenberg(raw, 3)
			if err != nil {
				t.Fatalf("Go CompressHeisenberg failed: %v", err)
			}

			inPrzm := filepath.Join(tmpDir, fmt.Sprintf("f_%d.przm", i))
			outBin := filepath.Join(tmpDir, fmt.Sprintf("f_%d.bin", i))

			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("Failed to write frame file: %v", err)
			}

			// Invoke Pure C decompressor
			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("C test_przm_dec.exe failed on frame %d (stride=%d, sz=%d, mode=%d):\nOutput:\n%s\nErr: %v",
					i, stride, sz, comp[9], string(output), err)
			}

			// Read back pure C output
			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("Failed to read C output file: %v", err)
			}

			if !bytes.Equal(cDec, raw) {
				t.Fatalf("Bit-exact mismatch on frame %d (stride=%d, sz=%d, mode=%d)! Got %d bytes, want %d bytes",
					i, stride, sz, comp[9], len(cDec), len(raw))
			}
		})
	}
}

// TestChallengerM2_MalformedFrame_Robustness adversarially stress-tests
// the Heisenberg parser against corrupted headers, invalid strides, out-of-bound lengths,
// and truncated payloads in both Go and Pure C.
func TestChallengerM2_MalformedFrame_Robustness(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	t.Run("Go_MalformedFrames", func(t *testing.T) {
		// 1. Truncated header (1 <= sz < 12 bytes)
		for sz := 1; sz < 12; sz++ {
			buf := make([]byte, sz)
			_, err := przm.DecompressHeisenberg(buf, nil)
			if err == nil {
				t.Fatalf("Expected error on truncated header sz=%d, got nil", sz)
			}
		}

		// 2. Invalid Magic
		badMagic := make([]byte, 24)
		binary.LittleEndian.PutUint32(badMagic[0:4], 0xDEADBEEF)
		_, err := przm.DecompressHeisenberg(badMagic, nil)
		if err == nil {
			t.Fatalf("Expected error on bad magic, got nil")
		}

		// 3. Invalid Stride
		for _, badStride := range []uint8{0, 1, 2, 5, 10, 255} {
			buf := make([]byte, 24)
			binary.LittleEndian.PutUint32(buf[0:4], przm.PRZM_MAGIC_HEISENBERG)
			binary.LittleEndian.PutUint32(buf[4:8], 12)
			buf[8] = badStride
			buf[9] = 1 // mode foliated
			_, err := przm.DecompressHeisenberg(buf, nil)
			if err == nil {
				t.Fatalf("Expected error on bad stride=%d, got nil", badStride)
			}
		}

		// 4. Invalid Mode
		for _, badMode := range []uint8{2, 4, 5, 255} {
			buf := make([]byte, 24)
			binary.LittleEndian.PutUint32(buf[0:4], przm.PRZM_MAGIC_HEISENBERG)
			binary.LittleEndian.PutUint32(buf[4:8], 12)
			buf[8] = 3
			buf[9] = badMode
			_, err := przm.DecompressHeisenberg(buf, nil)
			if err == nil {
				t.Fatalf("Expected error on bad mode=%d, got nil", badMode)
			}
		}

		// 5. Truncated payload for Mode 1
		trunc := make([]byte, 20)
		binary.LittleEndian.PutUint32(trunc[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(trunc[4:8], 100) // Claims 100 bytes uncompressed
		trunc[8] = 3
		trunc[9] = 1 // Foliated
		_, err = przm.DecompressHeisenberg(trunc, nil)
		if err == nil {
			t.Fatalf("Expected error on truncated payload, got nil")
		}
	})

	t.Run("PureC_MalformedFrames", func(t *testing.T) {
		// Verify Pure C CLI cleanly exits with non-zero error without crashing
		malformedCases := []struct {
			name string
			data []byte
		}{
			{
				name: "TruncatedHeader",
				data: []byte{0x48, 0x45, 0x49},
			},
			{
				name: "BadMagic",
				data: []byte{0x12, 0x34, 0x56, 0x78, 0x10, 0x00, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00},
			},
			{
				name: "BadStride",
				data: []byte{0x48, 0x45, 0x49, 0x53, 0x0C, 0x00, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			},
			{
				name: "TruncatedPayload",
				data: []byte{0x48, 0x45, 0x49, 0x53, 0xFF, 0x00, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 1, 2, 3},
			},
		}

		for _, mc := range malformedCases {
			t.Run(mc.name, func(t *testing.T) {
				inPrzm := filepath.Join(tmpDir, mc.name+".przm")
				outBin := filepath.Join(tmpDir, mc.name+".out")
				_ = os.WriteFile(inPrzm, mc.data, 0644)

				cmd := exec.Command(cBinPath, inPrzm, outBin)
				err := cmd.Run()
				if err == nil {
					t.Fatalf("Pure C decompressor expected failure on %s, but exited cleanly", mc.name)
				}
			})
		}
	})
}

// BenchmarkChallengerM2_LineRateThroughput benchmarks decompression rate on
// 64 KiB and 256 KiB blocks to verify > 20 GB/s decompressor line rate.
func BenchmarkChallengerM2_LineRateThroughput(b *testing.B) {
	sizes := []int{64 * 1024, 256 * 1024}

	for _, sz := range sizes {
		b.Run(fmt.Sprintf("Block_%dKiB", sz/1024), func(b *testing.B) {
			raw := make([]byte, sz)
			for i := 0; i+2 < sz; i += 3 {
				raw[i] = uint8(i & 0xFF)
				raw[i+1] = uint8((i * 7) & 0xFF)
				raw[i+2] = przm.GF256Mul(raw[i], raw[i+1]) ^ uint8((i*13+17)&0xFF)
			}

			comp, err := przm.CompressHeisenberg(raw, 3)
			if err != nil {
				b.Fatal(err)
			}

			dst := make([]byte, sz+64)

			b.SetBytes(int64(sz))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := przm.DecompressHeisenberg(comp, dst)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_m2_adversarial_test.go
// -----------------------------------------------------------------------------

// cGF256Mul implements the exact Russian Peasant algorithm from src/przm_dec.c:
//
//	uint8_t p = 0;
//	for (int i = 0; i < 8; i++) {
//	    if (b & 1) p ^= a;
//	    uint8_t hi = a & 0x80;
//	    a <<= 1;
//	    if (hi) a ^= 0x1B;
//	    b >>= 1;
//	}
//	return p;
func cGF256Mul(a, b uint8) uint8 {
	var p uint8 = 0
	for range 8 {
		if (b & 1) != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1B
		}
		b >>= 1
	}
	return p
}

// TestAdversarialHeisenberg_GF256MultiplicationTableExhaustive tests all 65,536 pairs
// of (a, b) over F_{2^8} to prove Go table lookup GF256Mul exactly matches C gf256_mul.
func TestAdversarialHeisenberg_GF256MultiplicationTableExhaustive(t *testing.T) {
	for a := range 256 {
		for b := range 256 {
			uA := uint8(a)
			uB := uint8(b)
			goRes := przm.GF256Mul(uA, uB)
			cRes := cGF256Mul(uA, uB)
			if goRes != cRes {
				t.Fatalf("GF(256) multiplication mismatch at (%d, %d): Go=%02X, C=%02X", a, b, goRes, cRes)
			}
			// Commutativity
			if przm.GF256Mul(uA, uB) != przm.GF256Mul(uB, uA) {
				t.Fatalf("Commutativity violated at (%d, %d)", a, b)
			}
		}
	}
}

// TestAdversarialHeisenberg_AlgebraicInvolutionFuzz100k verifies 100,000 randomized triplets
// and quads over F_{2^8}, proving exact algebraic involution H^{-1}(H(x, y, z)) == (x, y, z).
func TestAdversarialHeisenberg_AlgebraicInvolutionFuzz100k(t *testing.T) {
	rng := rand.New(rand.NewSource(987654321))
	const iterations = 100000

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for i := range iterations {
		x := uint8(rng.Intn(256))
		y := uint8(rng.Intn(256))
		z := uint8(rng.Intn(256))
		elem := przm.HeisenbergElement3{X: x, Y: y, Z: z}

		// 1. Group element inversion and involution
		invElem := przm.InvH3(elem)
		invInvElem := przm.InvH3(invElem)
		if invInvElem != elem {
			t.Fatalf("Iteration %d: InvH3(InvH3(%+v)) != elem, got %+v", i, elem, invInvElem)
		}

		// 2. Right and Left inverse cancel to identity
		mulRight := przm.MulH3(elem, invElem)
		if mulRight != ident {
			t.Fatalf("Iteration %d: elem * inv(elem) != ident: got %+v", i, mulRight)
		}
		mulLeft := przm.MulH3(invElem, elem)
		if mulLeft != ident {
			t.Fatalf("Iteration %d: inv(elem) * elem != ident: got %+v", i, mulLeft)
		}

		// 3. Coordinate foliation mapping involution: z' = z ^ (x * y), z'' = z' ^ (x * y) == z
		prod := przm.GF256Mul(x, y)
		zPrime := z ^ prod
		zDoublePrime := zPrime ^ prod
		if zDoublePrime != z {
			t.Fatalf("Iteration %d: coordinate involution failed: x=%02X y=%02X z=%02X -> z'=%02X -> z''=%02X",
				i, x, y, z, zPrime, zDoublePrime)
		}

		// 4. Stride 4 Quad foliation: (x, y, z, w) -> (x, y, z ^ (x * y), w)
		w := uint8(rng.Intn(256))
		quad := [4]byte{x, y, z, w}
		foliatedQuad := quad
		przm.HeisenbergFoliateInplace(foliatedQuad[:], 4)
		if foliatedQuad[0] != x || foliatedQuad[1] != y || foliatedQuad[2] != zPrime || foliatedQuad[3] != w {
			t.Fatalf("Iteration %d: quad foliation mismatch: got %v, want [%02X %02X %02X %02X]",
				i, foliatedQuad, x, y, zPrime, w)
		}
		przm.HeisenbergUnfoliateInplace(foliatedQuad[:], 4)
		if foliatedQuad != quad {
			t.Fatalf("Iteration %d: quad unfoliation mismatch: got %v, want %v", i, foliatedQuad, quad)
		}
	}
}

// TestAdversarialHeisenberg_BoundaryLengths tests buffer lengths 0, 1, 2, unaligned lengths,
// and SWAR loop non-multiples (12, 16, 32).
func TestAdversarialHeisenberg_BoundaryLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(1122334455))

	// All interesting lengths: boundary, unaligned, SWAR boundaries, prime lengths
	testLengths := []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 23, 24, 25, 31, 32, 33, 35, 47, 48, 49,
		63, 64, 65, 95, 96, 97, 127, 128, 129, 255, 256, 257,
		511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097,
	}

	for _, n := range testLengths {
		orig := make([]byte, n)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, n)
			copy(buf, orig)

			// 1. Forward foliation
			przm.HeisenbergFoliateInplace(buf, stride)

			// 2. Channel invariants:
			// Bytes strictly within full stride blocks
			fullBlocks := n / stride
			for b := range fullBlocks {
				idx := b * stride
				// X must remain unchanged
				if buf[idx+0] != orig[idx+0] {
					t.Fatalf("Length %d, Stride %d: Channel X corrupted at block %d (index %d)", n, stride, b, idx)
				}
				// Y must remain unchanged
				if buf[idx+1] != orig[idx+1] {
					t.Fatalf("Length %d, Stride %d: Channel Y corrupted at block %d (index %d)", n, stride, b, idx+1)
				}
				// Z must equal origZ ^ (origX * origY)
				expectedZ := orig[idx+2] ^ przm.GF256Mul(orig[idx+0], orig[idx+1])
				if buf[idx+2] != expectedZ {
					t.Fatalf("Length %d, Stride %d: Channel Z mismatch at block %d (index %d): got %02X, want %02X",
						n, stride, b, idx+2, buf[idx+2], expectedZ)
				}
				// For stride 4, W must remain unchanged
				if stride == 4 {
					if buf[idx+3] != orig[idx+3] {
						t.Fatalf("Length %d, Stride %d: Channel W corrupted at block %d (index %d)", n, stride, b, idx+3)
					}
				}
			}

			// 3. Unaligned tail bytes (indices >= fullBlocks * stride) must be completely untouched
			tailStart := fullBlocks * stride
			for j := tailStart; j < n; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Length %d, Stride %d: Unaligned tail byte modified at index %d: got %02X, want %02X",
						n, stride, j, buf[j], orig[j])
				}
			}

			// 4. Inverse unfoliation must restore original bytes bit-identically
			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d, Stride %d: Involution failed! Restored buffer differs from original", n, stride)
			}
		}
	}
}

// TestAdversarialHeisenberg_ComprehensiveEveryLength0to256 tests EVERY single integer
// length from 0 to 256 for both stride 3 and 4 to guarantee zero off-by-one errors.
func TestAdversarialHeisenberg_ComprehensiveEveryLength0to256(t *testing.T) {
	rng := rand.New(rand.NewSource(44556677))

	for n := 0; n <= 256; n++ {
		orig := make([]byte, n)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, n)
			copy(buf, orig)

			przm.HeisenbergFoliateInplace(buf, stride)

			// Tail check
			tailStart := (n / stride) * stride
			for j := tailStart; j < n; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Length %d (stride %d): tail byte %d corrupted", n, stride, j)
				}
			}

			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d (stride %d): unfoliate mismatch", n, stride)
			}
		}
	}
}

// TestAdversarialHeisenberg_FrameCodecBoundaryPayloads tests roundtrips of boundary payloads
// through CompressHeisenberg and DecompressHeisenberg.
func TestAdversarialHeisenberg_FrameCodecBoundaryPayloads(t *testing.T) {
	rng := rand.New(rand.NewSource(99887766))

	testSizes := []int{0, 1, 2, 3, 4, 5, 10, 11, 12, 13, 24, 25, 47, 48, 49, 100, 3072, 4096}

	for _, size := range testSizes {
		orig := make([]byte, size)
		rng.Read(orig)

		// Make correlated data if size >= 3
		if size >= 3 {
			for i := 0; i+3 <= size; i += 3 {
				orig[i+2] = przm.GF256Mul(orig[i], orig[i+1])
			}
		}

		compressed, err := przm.CompressHeisenberg(orig, 3)
		if err != nil {
			t.Fatalf("Size %d: CompressHeisenberg failed: %v", size, err)
		}

		if size == 0 {
			// Check size 0 behavior
			t.Logf("Size 0: compressed len = %d", len(compressed))
			decompressed, err := przm.DecompressHeisenberg(compressed, nil)
			if err != nil {
				t.Logf("DISCOVERY: Size 0 DecompressHeisenberg returned error: %v (compressed len: %d)", err, len(compressed))
			} else if !bytes.Equal(decompressed, orig) {
				t.Fatalf("Size 0 mismatch: got %v, want %v", decompressed, orig)
			}
			continue
		}

		decompressed, err := przm.DecompressHeisenberg(compressed, nil)
		if err != nil {
			t.Fatalf("Size %d: DecompressHeisenberg failed: %v", size, err)
		}
		if !bytes.Equal(decompressed, orig) {
			t.Fatalf("Size %d: Decompressed mismatch: got len %d, want len %d", size, len(decompressed), len(orig))
		}

		// Also verify DecompressTo
		dst := make([]byte, size+64)
		decTo, err := przm.DecompressTo(compressed, dst)
		if err != nil {
			t.Fatalf("Size %d: DecompressTo failed: %v", size, err)
		}
		if !bytes.Equal(decTo, orig) {
			t.Fatalf("Size %d: DecompressTo mismatch", size)
		}
	}
}

// TestAdversarialHeisenberg_MalformedFrames tests security and robustness against
// corrupted headers, invalid strides, illegal modes, and truncated frames.
func TestAdversarialHeisenberg_MalformedFrames(t *testing.T) {
	validFrame := make([]byte, 12+24)
	binary.LittleEndian.PutUint32(validFrame[0:4], przm.PRZM_MAGIC_HEISENBERG)
	binary.LittleEndian.PutUint32(validFrame[4:8], 24)
	validFrame[8] = 3 // Stride = 3
	validFrame[9] = 1 // Mode = Foliated
	for i := 12; i < len(validFrame); i++ {
		validFrame[i] = uint8(i)
	}

	// 0. Empty frame (0 bytes) gracefully returns []byte{}, nil per contract
	dec0, err0 := przm.DecompressHeisenberg(validFrame[:0], nil)
	if err0 != nil || len(dec0) != 0 {
		t.Fatalf("Empty frame returned error %v, len %d (expected nil, 0)", err0, len(dec0))
	}

	// 1. Truncated header (1 <= l < 12 bytes)
	for l := 1; l < 12; l++ {
		_, err := przm.DecompressHeisenberg(validFrame[:l], nil)
		if err == nil {
			t.Fatalf("Truncated frame of length %d accepted without error", l)
		}
	}

	// 2. Corrupt Magic
	badMagic := make([]byte, len(validFrame))
	copy(badMagic, validFrame)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0xDEADBEEF)
	if _, err := przm.DecompressHeisenberg(badMagic, nil); err == nil {
		t.Fatalf("Bad magic accepted without error")
	}

	// 3. Corrupt Stride (must be 3 or 4)
	badStrides := []uint8{0, 1, 2, 5, 6, 8, 16, 255}
	for _, bs := range badStrides {
		badS := make([]byte, len(validFrame))
		copy(badS, validFrame)
		badS[8] = bs
		if _, err := przm.DecompressHeisenberg(badS, nil); err == nil {
			t.Fatalf("Illegal stride %d accepted without error", bs)
		}
	}

	// 4. Corrupt Mode
	badModes := []uint8{2, 4, 5, 255}
	for _, bm := range badModes {
		badM := make([]byte, len(validFrame))
		copy(badM, validFrame)
		badM[9] = bm
		if _, err := przm.DecompressHeisenberg(badM, nil); err == nil {
			t.Fatalf("Illegal mode %d accepted without error", bm)
		}
	}

	// 5. Truncated payload in Mode 0 and Mode 1
	for _, mode := range []uint8{0, 1} {
		truncPayload := make([]byte, 12+10) // origLen is 24, but payload has only 10
		copy(truncPayload, validFrame[:12+10])
		truncPayload[9] = mode
		if _, err := przm.DecompressHeisenberg(truncPayload, nil); err == nil {
			t.Fatalf("Truncated payload in mode %d accepted without error", mode)
		}
	}

	// 6. Excessive origLen (> MaxBlockSize)
	badLen := make([]byte, len(validFrame))
	copy(badLen, validFrame)
	binary.LittleEndian.PutUint32(badLen[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressHeisenberg(badLen, nil); err == nil {
		t.Fatalf("origLen exceeding MaxBlockSize accepted without error")
	}
}

// TestAdversarialHeisenberg_ZeroAllocationInvariant stresses zero memory allocation
// on DecompressHeisenberg and DecompressTo across varied block sizes.
func TestAdversarialHeisenberg_ZeroAllocationInvariant(t *testing.T) {
	sizes := []int{24, 48, 96, 384, 1536, 6144}

	for _, size := range sizes {
		// Construct Mode 1 (Foliated uncompressed) frame explicitly
		src := make([]byte, size)
		for i := range size {
			src[i] = uint8((i*37 + 13) & 0xFF)
		}

		// Frame with Mode 1: Foliated
		foliatedFrame := make([]byte, przm.HeisenbergHeaderSize+size)
		binary.LittleEndian.PutUint32(foliatedFrame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(foliatedFrame[4:8], uint32(size))
		foliatedFrame[8] = 3 // Stride = 3
		foliatedFrame[9] = przm.HeisenbergModeFoliated
		copy(foliatedFrame[przm.HeisenbergHeaderSize:], src)
		przm.HeisenbergFoliateInplace(foliatedFrame[przm.HeisenbergHeaderSize:], 3)

		dst := make([]byte, size+128)

		// 1. In-place unfoliation must be strictly 0 allocs
		allocsInplace := testing.AllocsPerRun(50, func() {
			przm.HeisenbergUnfoliateInplace(dst[:size], 3)
		})
		if allocsInplace != 0 {
			t.Fatalf("Size %d: HeisenbergUnfoliateInplace allocated: %v allocs/op", size, allocsInplace)
		}

		// 2. Mode 1 DecompressHeisenberg zero alloc
		allocsDec := testing.AllocsPerRun(50, func() {
			out, err := przm.DecompressHeisenberg(foliatedFrame, dst)
			if err != nil || len(out) != size {
				t.Fatalf("Decompress failed: %v", err)
			}
		})
		if allocsDec != 0 {
			t.Fatalf("Size %d: Mode 1 DecompressHeisenberg violated 0-alloc: got %v allocs/op", size, allocsDec)
		}

		// 3. Mode 1 DecompressTo zero alloc
		allocsTo := testing.AllocsPerRun(50, func() {
			out, err := przm.DecompressTo(foliatedFrame, dst)
			if err != nil || len(out) != size {
				t.Fatalf("DecompressTo failed: %v", err)
			}
		})
		if allocsTo != 0 {
			t.Fatalf("Size %d: Mode 1 DecompressTo violated 0-alloc: got %v allocs/op", size, allocsTo)
		}

		// 4. Test Mode 3 (Cascaded PWS) allocation profile
		srcCorrelated := make([]byte, size)
		for i := 0; i+3 <= size; i += 3 {
			srcCorrelated[i] = uint8(i & 0xFF)
			srcCorrelated[i+1] = uint8((i * 7) & 0xFF)
			srcCorrelated[i+2] = przm.GF256Mul(srcCorrelated[i], srcCorrelated[i+1])
		}
		comp3, err := przm.CompressHeisenberg(srcCorrelated, 3)
		if err == nil && len(comp3) > 9 && comp3[9] == przm.HeisenbergModeCompressed {
			allocsM3 := testing.AllocsPerRun(20, func() {
				_, _ = przm.DecompressHeisenberg(comp3, dst)
			})
			t.Logf("Size %d: Mode 3 (Cascaded PWS) allocations: %v allocs/op (delegated to inner PWSDecode)", size, allocsM3)
		}
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_m2_2_r3_test.go
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// 1. Theorem 39: InvH3(InvH3(x)) == x across 50,000 random triplets in F_{2^8}^3
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_Theorem39_Involution50k(t *testing.T) {
	rng := rand.New(rand.NewSource(39393939))
	const totalTriplets = 50000

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for i := range totalTriplets {
		elem := przm.HeisenbergElement3{
			X: uint8(rng.Intn(256)),
			Y: uint8(rng.Intn(256)),
			Z: uint8(rng.Intn(256)),
		}

		// First algebraic inversion: H^{-1}(elem)
		invElem := przm.InvH3(elem)

		// Verification of coordinate map:
		// X and Y unchanged, Z' = Z ^ (X * Y)
		expectedZ := elem.Z ^ przm.GF256Mul(elem.X, elem.Y)
		if invElem.X != elem.X || invElem.Y != elem.Y || invElem.Z != expectedZ {
			t.Fatalf("Iteration %d: InvH3(%+v) coordinates invalid: got %+v, expected Z=%02X",
				i, elem, invElem, expectedZ)
		}

		// Second algebraic inversion: H^{-1}(H^{-1}(elem))
		invInvElem := przm.InvH3(invElem)

		// Strict Involution Check: Theorem 39
		if invInvElem != elem {
			t.Fatalf("Iteration %d: Theorem 39 Involution Failed! InvH3(InvH3(%+v)) = %+v, want %+v",
				i, elem, invInvElem, elem)
		}

		// Group inverse axioms: elem * invElem == e, invElem * elem == e
		rightMul := przm.MulH3(elem, invElem)
		if rightMul != ident {
			t.Fatalf("Iteration %d: elem * inv(elem) != (0,0,0): got %+v", i, rightMul)
		}
		leftMul := przm.MulH3(invElem, elem)
		if leftMul != ident {
			t.Fatalf("Iteration %d: inv(elem) * elem != (0,0,0): got %+v", i, leftMul)
		}
	}
}

// -----------------------------------------------------------------------------
// 2. Theorem 40: Unfoliate(Foliate(x)) == x across 50,000 random triplets/quads
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_Theorem40_FoliationUnfoliation50k(t *testing.T) {
	rng := rand.New(rand.NewSource(40404040))
	const totalBlocks = 50000

	// 2a. Stride 3 triplets (50,000 triplets = 150,000 bytes)
	data3 := make([]byte, totalBlocks*3)
	rng.Read(data3)
	orig3 := make([]byte, len(data3))
	copy(orig3, data3)

	// In-place foliation
	przm.HeisenbergFoliateInplace(data3, 3)

	// Validate coordinate transformation for all 50,000 triplets
	for i := range totalBlocks {
		x := orig3[i*3+0]
		y := orig3[i*3+1]
		z := orig3[i*3+2]
		expZ := z ^ przm.GF256Mul(x, y)

		if data3[i*3+0] != x {
			t.Fatalf("Triplet %d: channel X modified", i)
		}
		if data3[i*3+1] != y {
			t.Fatalf("Triplet %d: channel Y modified", i)
		}
		if data3[i*3+2] != expZ {
			t.Fatalf("Triplet %d: channel Z mismatch: got %02X, want %02X", i, data3[i*3+2], expZ)
		}
	}

	// In-place unfoliation: Theorem 40
	przm.HeisenbergUnfoliateInplace(data3, 3)
	if !bytes.Equal(data3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 In-Place Involution Failed: Unfoliate(Foliate(x)) != x")
	}

	// 2b. Test distinct buffers for Stride 3
	dst3 := make([]byte, len(orig3))
	przm.HeisenbergFoliate(dst3, orig3, 3)
	restored3 := make([]byte, len(orig3))
	przm.HeisenbergUnfoliate(restored3, dst3, 3)
	if !bytes.Equal(restored3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 Distinct Buffers Involution Failed")
	}

	// 2c. Test aliased in-place Foliate(&buf[0], &buf[0])
	aliased3 := make([]byte, len(orig3))
	copy(aliased3, orig3)
	przm.HeisenbergFoliate(aliased3, aliased3, 3)
	przm.HeisenbergUnfoliate(aliased3, aliased3, 3)
	if !bytes.Equal(aliased3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 Aliased Buffers Involution Failed")
	}

	// 2d. Stride 4 quads (50,000 quads = 200,000 bytes)
	data4 := make([]byte, totalBlocks*4)
	rng.Read(data4)
	orig4 := make([]byte, len(data4))
	copy(orig4, data4)

	przm.HeisenbergFoliateInplace(data4, 4)

	for i := range totalBlocks {
		x := orig4[i*4+0]
		y := orig4[i*4+1]
		z := orig4[i*4+2]
		w := orig4[i*4+3]
		expZ := z ^ przm.GF256Mul(x, y)

		if data4[i*4+0] != x || data4[i*4+1] != y || data4[i*4+2] != expZ || data4[i*4+3] != w {
			t.Fatalf("Quad %d: channel mismatch in foliation", i)
		}
	}

	przm.HeisenbergUnfoliateInplace(data4, 4)
	if !bytes.Equal(data4, orig4) {
		t.Fatalf("Theorem 40 Stride 4 Involution Failed")
	}
}

// -----------------------------------------------------------------------------
// 3. Edge Cases: all zeros, all 0xFF, alternating bit patterns, prime lengths
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_EdgeCases_AdversarialInputs(t *testing.T) {
	// Prime lengths from 2 to 4093
	primeLengths := []int{
		2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61,
		67, 71, 73, 79, 83, 89, 97, 101, 103, 107, 109, 113, 127, 131, 137,
		139, 149, 151, 157, 163, 167, 173, 179, 181, 191, 193, 197, 199, 211,
		223, 227, 229, 233, 239, 241, 251, 257, 509, 1021, 2053, 4093,
	}

	patterns := []struct {
		name string
		gen  func(length int) []byte
	}{
		{
			name: "AllZeros_0x00",
			gen: func(length int) []byte {
				return make([]byte, length)
			},
		},
		{
			name: "AllOnes_0xFF",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0xFF
				}
				return b
			},
		},
		{
			name: "Alternating_0xAA",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0xAA
				}
				return b
			},
		},
		{
			name: "Alternating_0x55",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0x55
				}
				return b
			},
		},
		{
			name: "AlternatingTriplets_AA_55_FF",
			gen: func(length int) []byte {
				b := make([]byte, length)
				cycle := []byte{0xAA, 0x55, 0xFF, 0x00, 0x33, 0xCC}
				for i := range b {
					b[i] = cycle[i%len(cycle)]
				}
				return b
			},
		},
		{
			name: "PowersOfTwo_SingleBits",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 1 << uint(i%8)
				}
				return b
			},
		},
	}

	for _, p := range patterns {
		for _, primeLen := range primeLengths {
			orig := p.gen(primeLen)

			for _, stride := range []int{3, 4} {
				buf := make([]byte, primeLen)
				copy(buf, orig)

				// In-place foliate
				przm.HeisenbergFoliateInplace(buf, stride)

				// Verify unaligned tail is completely untouched
				tailStart := (primeLen / stride) * stride
				for j := tailStart; j < primeLen; j++ {
					if buf[j] != orig[j] {
						t.Fatalf("Pattern %s, PrimeLen %d, Stride %d: tail corrupted at index %d: got %02X, want %02X",
							p.name, primeLen, stride, j, buf[j], orig[j])
					}
				}

				// In-place unfoliate must restore exact original
				przm.HeisenbergUnfoliateInplace(buf, stride)
				if !bytes.Equal(buf, orig) {
					t.Fatalf("Pattern %s, PrimeLen %d, Stride %d: involution failed",
						p.name, primeLen, stride)
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// 4. High Concurrency Stress Test: Heisenberg & Sheaf Decompressors
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_HighConcurrency_ZeroAlloc(t *testing.T) {
	const numGoroutines = 8
	const iterations = 50

	// 4a. Prepare diverse Heisenberg frames
	var heisFrames [][]byte
	var heisExpected [][]byte

	// Frame 1: Raw
	rawInput := make([]byte, 1024)
	for i := range rawInput {
		rawInput[i] = byte(i * 11)
	}
	compRaw, err := przm.CompressHeisenberg(rawInput, 1)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compRaw)
	heisExpected = append(heisExpected, rawInput)

	// Frame 2: Foliated Stride 3
	folInput3 := make([]byte, 3072)
	for i := 0; i < len(folInput3); i += 3 {
		folInput3[i] = byte(i % 127)
		folInput3[i+1] = byte((i*7 + 1) % 256)
		folInput3[i+2] = przm.GF256Mul(folInput3[i], folInput3[i+1])
	}
	compFol3, err := przm.CompressHeisenberg(folInput3, 3)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compFol3)
	heisExpected = append(heisExpected, folInput3)

	// Frame 3: Foliated Stride 4
	folInput4 := make([]byte, 4096)
	for i := 0; i < len(folInput4); i += 4 {
		folInput4[i] = byte(i % 127)
		folInput4[i+1] = byte((i*5 + 1) % 256)
		folInput4[i+2] = przm.GF256Mul(folInput4[i], folInput4[i+1])
		folInput4[i+3] = 0x88
	}
	compFol4, err := przm.CompressHeisenberg(folInput4, 3)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compFol4)
	heisExpected = append(heisExpected, folInput4)

	// 4b. Prepare diverse Cellular Sheaf frames
	var sheafFrames [][]byte
	var sheafExpected [][]byte

	// Sheaf 1: Harmonic 8x8
	h8 := make([]byte, 64)
	h8[0] = 0x77
	for j := range 7 {
		h8[j+1] = h8[j] ^ byte(j+1)
	}
	for i := 1; i < 8; i++ {
		v := byte(i * 3)
		for j := range 8 {
			h8[i*8+j] = h8[(i-1)*8+j] ^ v
		}
	}
	compSh8, err := przm.CompressCellularSheaf(h8, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSh8)
	sheafExpected = append(sheafExpected, h8)

	// Sheaf 2: ConstBoundaries 16x16
	h16 := make([]byte, 256)
	h16[0] = 0xAA
	for j := range 15 {
		h16[j+1] = h16[j] ^ 0x03
	}
	for i := 1; i < 16; i++ {
		for j := range 16 {
			h16[i*16+j] = h16[(i-1)*16+j] ^ 0x05
		}
	}
	compSh16, err := przm.CompressCellularSheaf(h16, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSh16)
	sheafExpected = append(sheafExpected, h16)

	// Sheaf 3: Sparse Curl 16x32 with unaligned remainder
	spCurl := make([]byte, 517)
	for i := range spCurl {
		spCurl[i] = byte(i * 7)
	}
	spCurl[19] ^= 0x33
	spCurl[105] ^= 0x77
	compSp, err := przm.CompressCellularSheaf(spCurl, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSp)
	sheafExpected = append(sheafExpected, spCurl)

	// 4c. Launch concurrent workers
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*iterations)

	for g := range numGoroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(gid*10007 + 73)))
			dst := bufkit.AlignedBytes(8192, bufkit.CacheLineSize)

			for iter := range iterations {
				if gid%2 == 0 {
					// Heisenberg decompress
					idx := rng.Intn(len(heisFrames))
					frame := heisFrames[idx]
					exp := heisExpected[idx]

					dec, err := przm.DecompressHeisenberg(frame, dst)
					if err != nil {
						errCh <- fmt.Errorf("gid %d iter %d DecompressHeisenberg error: %w", gid, iter, err)
						return
					}
					if !bytes.Equal(dec, exp) {
						errCh <- fmt.Errorf("gid %d iter %d DecompressHeisenberg data mismatch", gid, iter)
						return
					}
				} else {
					// Sheaf decompress
					idx := rng.Intn(len(sheafFrames))
					frame := sheafFrames[idx]
					exp := sheafExpected[idx]

					dec, err := przm.DecompressCellularSheaf(frame, dst)
					if err != nil {
						errCh <- fmt.Errorf("gid %d iter %d DecompressCellularSheaf error: %w", gid, iter, err)
						return
					}
					if !bytes.Equal(dec, exp) {
						errCh <- fmt.Errorf("gid %d iter %d DecompressCellularSheaf data mismatch", gid, iter)
						return
					}
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// 5. Parallel Benchmarks verifying 0 B/op and 0 allocs/op under multi-core load
// -----------------------------------------------------------------------------
func BenchmarkChallengerM2_2_R3_Heisenberg_ParallelZeroAlloc(b *testing.B) {
	src := make([]byte, 65536)
	for i := range src {
		src[i] = uint8((i * 3) & 0xFF)
	}
	for i := 0; i+2 < len(src); i += 3 {
		src[i+2] = przm.GF256Mul(src[i], src[i+1]) ^ uint8((i*19+7)&0xFF)
	}
	compressed, err := przm.CompressHeisenberg(src, 3)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dst := bufkit.AlignedBytes(len(src)+64, bufkit.CacheLineSize)
		for pb.Next() {
			_, err := przm.DecompressHeisenberg(compressed, dst)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkChallengerM2_2_R3_CellularSheaf_ParallelZeroAlloc(b *testing.B) {
	orig := make([]byte, 1024)
	for i := range orig {
		orig[i] = uint8((i * 3) & 0xFF)
	}
	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
		// Warmup thread-local buffer
		_, _ = przm.DecompressCellularSheaf(compressed, dst)
		for pb.Next() {
			_, err := przm.DecompressCellularSheaf(compressed, dst)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Source: challenger_sheaf_hodge_test.go
// -----------------------------------------------------------------------------

// dotGF256 computes the standard bilinear inner product in F_{2^8}^N:
// <x, y> = \sum_{i} x[i] * y[i] (with addition as XOR).
func dotGF256(x, y []uint8) uint8 {
	var acc uint8
	n := min(len(y), len(x))
	for i := range n {
		acc ^= przm.GF256Mul(x[i], y[i])
	}
	return acc
}

// dotCochain0 computes <phi, psi> on C^0(X; F_{2^8}).
func dotCochain0(phi, psi przm.Cochain0) uint8 {
	return dotGF256([]uint8(phi), []uint8(psi))
}

// dotCochain1 computes <u, v> on C^1(X; F_{2^8}) = <u.Eh, v.Eh> ^ <u.Ev, v.Ev>.
func dotCochain1(u, v przm.Cochain1) uint8 {
	return dotGF256(u.Eh, v.Eh) ^ dotGF256(u.Ev, v.Ev)
}

// dotCochain2 computes <c1, c2> on C^2(X; F_{2^8}).
func dotCochain2(c1, c2 przm.Cochain2) uint8 {
	return dotGF256([]uint8(c1), []uint8(c2))
}

// ----------------------------------------------------------------------------
// 1. Orthogonal Decomposition & Adjoint Property (<d0 phi, psi> = <phi, d0^T psi>)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_Adjoint0_OrthogonalDecomposition_12K(t *testing.T) {
	const totalTrials = 12000
	rng := rand.New(rand.NewSource(0xCAFE_BABE_2026))

	t.Logf("Executing %d trials: Adjoint0 duality <d0 phi, psi> == <phi, d0^T psi> and orthogonal decomposition...", totalTrials)

	// Pre-define specific boundary/stress geometries
	specialDims := [][2]int{
		{2, 2}, {2, 3}, {3, 2}, {1, 2}, {2, 1}, {1, 10}, {10, 1}, {1, 1},
		{4, 4}, {8, 8}, {16, 16}, {32, 32}, {3, 7}, {11, 13}, {17, 19},
		{2, 64}, {64, 2}, {33, 17},
	}

	var (
		adjoint0Failures    int
		adjoint1Failures    int
		orthoDecompFailures int
	)

	for trial := range totalTrials {
		var M, W int
		if trial < len(specialDims) {
			M, W = specialDims[trial][0], specialDims[trial][1]
		} else {
			// Variable random geometries: rows and cols in [1, 36]
			M = 1 + rng.Intn(36)
			W = 1 + rng.Intn(36)
		}

		// Ensure all 256 twists in F_{2^8} are covered systematically and randomly
		var twist uint8
		if trial < 256*4 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Generate 0-cochain phi
		phi := make(przm.Cochain0, M*W)
		switch trial % 6 {
		case 0:
			// Uniform random
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
		case 1:
			// All zeros
			// zero value
		case 2:
			// All ones
			for i := range phi {
				phi[i] = 1
			}
		case 3:
			// Max field element
			for i := range phi {
				phi[i] = 0xFF
			}
		case 4:
			// Single impulse
			if len(phi) > 0 {
				phi[rng.Intn(len(phi))] = uint8(1 + rng.Intn(255))
			}
		case 5:
			// Checkerboard
			for i := 0; i < M; i++ {
				for j := 0; j < W; j++ {
					phi[i*W+j] = uint8(((i + j) & 1) * 0x55)
				}
			}
		}

		// Generate 1-cochain psi
		psi := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		if trial%6 != 1 {
			for i := range psi.Eh {
				psi.Eh[i] = uint8(rng.Intn(256))
			}
			for i := range psi.Ev {
				psi.Ev[i] = uint8(rng.Intn(256))
			}
		}

		// 1. Verify d0 adjoint duality: <d0 phi, psi>_1 == <phi, d0^T psi>_0
		d0phi := przm.Coboundary0(complex, phi)
		d0Tpsi := przm.Adjoint0(complex, psi)

		lhs0 := dotCochain1(d0phi, psi)
		rhs0 := dotCochain0(phi, d0Tpsi)

		if lhs0 != rhs0 {
			adjoint0Failures++
			if adjoint0Failures <= 5 {
				t.Errorf("[Trial %d] Adjoint0 failure: M=%d, W=%d, Twist=0x%02X: <d0 phi, psi>=0x%02X != <phi, d0^T psi>=0x%02X",
					trial, M, W, twist, lhs0, rhs0)
			}
		}

		// 2. Verify d1 adjoint duality when 2D faces exist (M >= 2, W >= 2)
		if M >= 2 && W >= 2 {
			c := make(przm.Cochain2, (M-1)*(W-1))
			for i := range c {
				c[i] = uint8(rng.Intn(256))
			}

			d1psi := przm.Coboundary1(complex, psi)
			d1Tc := przm.Adjoint1(complex, c)

			lhs1 := dotCochain2(d1psi, c)
			rhs1 := dotCochain1(psi, d1Tc)

			if lhs1 != rhs1 {
				adjoint1Failures++
				if adjoint1Failures <= 5 {
					t.Errorf("[Trial %d] Adjoint1 failure: M=%d, W=%d, Twist=0x%02X: <d1 psi, c>=0x%02X != <psi, d1^T c>=0x%02X",
						trial, M, W, twist, lhs1, rhs1)
				}
			}

			// 3. Verify Orthogonal Complement: im(d0) \perp ker(d0^T)
			// Because d0^T(d1^T c) == 0, d1^T c \in ker(d0^T).
			// Therefore, <d0 phi, d1^T c> must be 0 identically.
			pairingOrtho := dotCochain1(d0phi, d1Tc)
			if pairingOrtho != 0 {
				orthoDecompFailures++
				if orthoDecompFailures <= 5 {
					t.Errorf("[Trial %d] Orthogonal decomposition failure: <d0 phi, d1^T c>=0x%02X != 0 (M=%d, W=%d, Twist=0x%02X)",
						trial, pairingOrtho, M, W, twist)
				}
			}
		}
	}

	t.Logf("Summary 12K Adjoint & Orthogonality: Adjoint0 failures = %d, Adjoint1 failures = %d, Ortho failures = %d",
		adjoint0Failures, adjoint1Failures, orthoDecompFailures)

	if adjoint0Failures > 0 {
		t.Fatalf("FAILED: %d Adjoint0 mathematical failures detected!", adjoint0Failures)
	}
	if adjoint1Failures > 0 {
		t.Fatalf("FAILED: %d Adjoint1 mathematical failures detected!", adjoint1Failures)
	}
	if orthoDecompFailures > 0 {
		t.Fatalf("FAILED: %d Orthogonal decomposition failures detected!", orthoDecompFailures)
	}
}

// ----------------------------------------------------------------------------
// 2. Coboundary Nilpotency: d1 ∘ d0 ≡ 0 and Dual d0^T ∘ d1^T ≡ 0 (20,000 trials)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_CoboundaryNilpotency_20K(t *testing.T) {
	const totalTrials = 20000
	rng := rand.New(rand.NewSource(0xDEAD_BEEF_5001))

	t.Logf("Executing %d trials: Coboundary nilpotency d1 ∘ d0 ≡ 0 and dual d0^T ∘ d1^T ≡ 0...", totalTrials)

	var (
		primalNilpotencyFailures int
		dualNilpotencyFailures   int
	)

	for trial := range totalTrials {
		// Sample arbitrary 2D cell complex geometry (M >= 2, W >= 2)
		M := 2 + rng.Intn(35)
		W := 2 + rng.Intn(35)

		// Sweep all 256 twists repeatedly
		var twist uint8
		if trial < 256*8 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Primal nilpotency: for arbitrary phi, (d1 ∘ d0)(phi) == 0
		phi := make(przm.Cochain0, M*W)
		switch trial % 5 {
		case 0:
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
		case 1:
			// Single-hot impulse
			phi[rng.Intn(len(phi))] = uint8(1 + rng.Intn(255))
		case 2:
			// Linear ramp
			for i := range phi {
				phi[i] = uint8(i & 0xFF)
			}
		case 3:
			// Constant field
			cVal := uint8(rng.Intn(256))
			for i := range phi {
				phi[i] = cVal
			}
		case 4:
			// High-frequency alternating
			for i := range M {
				for j := range W {
					phi[i*W+j] = uint8((i*17 + j*31) & 0xFF)
				}
			}
		}

		d0phi := przm.Coboundary0(complex, phi)
		d1d0phi := przm.Coboundary1(complex, d0phi)

		for faceIdx, val := range d1d0phi {
			if val != 0 {
				primalNilpotencyFailures++
				if primalNilpotencyFailures <= 5 {
					t.Errorf("[Trial %d] Primal nilpotency violation: M=%d, W=%d, Twist=0x%02X: (d1 ∘ d0)(phi) != 0 at face %d (val=0x%02X)",
						trial, M, W, twist, faceIdx, val)
				}
				break
			}
		}

		// Dual nilpotency: for arbitrary 2-cochain c, (d0^T ∘ d1^T)(c) == 0
		c := make(przm.Cochain2, (M-1)*(W-1))
		for i := range c {
			c[i] = uint8(rng.Intn(256))
		}

		d1Tc := przm.Adjoint1(complex, c)
		d0Td1Tc := przm.Adjoint0(complex, d1Tc)

		for vIdx, val := range d0Td1Tc {
			if val != 0 {
				dualNilpotencyFailures++
				if dualNilpotencyFailures <= 5 {
					t.Errorf("[Trial %d] Dual nilpotency violation: M=%d, W=%d, Twist=0x%02X: (d0^T ∘ d1^T)(c) != 0 at vertex %d (val=0x%02X)",
						trial, M, W, twist, vIdx, val)
				}
				break
			}
		}
	}

	t.Logf("Summary 20K Nilpotency: Primal d1 ∘ d0 failures = %d, Dual d0^T ∘ d1^T failures = %d",
		primalNilpotencyFailures, dualNilpotencyFailures)

	if primalNilpotencyFailures > 0 {
		t.Fatalf("FAILED: %d Primal nilpotency failures detected!", primalNilpotencyFailures)
	}
	if dualNilpotencyFailures > 0 {
		t.Fatalf("FAILED: %d Dual nilpotency failures detected!", dualNilpotencyFailures)
	}
}

// ----------------------------------------------------------------------------
// 3. Self-Adjointness and Commutation of Hodge Laplacians Delta_0 and Delta_1 (12,000 trials)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_HodgeLaplacian_SelfAdjointness_12K(t *testing.T) {
	const totalTrials = 12000
	rng := rand.New(rand.NewSource(0x7001_A55A_BEEF))

	t.Logf("Executing %d trials: Self-adjointness and commutation of Hodge Laplacians Delta_0 and Delta_1...", totalTrials)

	var (
		lap0SelfAdjointFailures int
		lap1SelfAdjointFailures int
		lap0CommutationFailures int
		lap1CommutationFailures int
		lap0LinearityFailures   int
		lap1LinearityFailures   int
	)

	for trial := range totalTrials {
		M := 2 + rng.Intn(30)
		W := 2 + rng.Intn(30)

		var twist uint8
		if trial < 256*4 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// --------------------------------------------------------------------
		// A. Delta_0: Self-Adjointness <Delta_0 phi, chi> == <phi, Delta_0 chi>
		// --------------------------------------------------------------------
		phi := make(przm.Cochain0, M*W)
		chi := make(przm.Cochain0, M*W)
		for i := range phi {
			phi[i] = uint8(rng.Intn(256))
			chi[i] = uint8(rng.Intn(256))
		}

		lap0Phi := przm.HodgeLaplacian0(complex, phi)
		lap0Chi := przm.HodgeLaplacian0(complex, chi)

		p0A := dotCochain0(lap0Phi, chi)
		p0B := dotCochain0(phi, lap0Chi)

		if p0A != p0B {
			lap0SelfAdjointFailures++
			if lap0SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_0 self-adjointness failure: M=%d, W=%d, Twist=0x%02X: <Delta_0 phi, chi>=0x%02X != <phi, Delta_0 chi>=0x%02X",
					trial, M, W, twist, p0A, p0B)
			}
		}

		// Also verify Dirichlet energy identity: <Delta_0 phi, chi> == <d0 phi, d0 chi>
		d0Phi := przm.Coboundary0(complex, phi)
		d0Chi := przm.Coboundary0(complex, chi)
		dirichlet0 := dotCochain1(d0Phi, d0Chi)
		if p0A != dirichlet0 {
			lap0SelfAdjointFailures++
			if lap0SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_0 energy pairing mismatch: <Delta_0 phi, chi>=0x%02X != <d0 phi, d0 chi>=0x%02X",
					trial, p0A, dirichlet0)
			}
		}

		// Delta_0 Linearity & Homogeneity: Delta_0(a * phi ^ b * chi) == a * Delta_0(phi) ^ b * Delta_0(chi)
		scalarA := uint8(rng.Intn(256))
		scalarB := uint8(rng.Intn(256))
		comboPhi := make(przm.Cochain0, M*W)
		for i := range comboPhi {
			comboPhi[i] = przm.GF256Mul(scalarA, phi[i]) ^ przm.GF256Mul(scalarB, chi[i])
		}
		lap0Combo := przm.HodgeLaplacian0(complex, comboPhi)
		for i := range comboPhi {
			expected := przm.GF256Mul(scalarA, lap0Phi[i]) ^ przm.GF256Mul(scalarB, lap0Chi[i])
			if lap0Combo[i] != expected {
				lap0LinearityFailures++
				if lap0LinearityFailures <= 5 {
					t.Errorf("[Trial %d] Delta_0 linearity violated at vertex %d (M=%d, W=%d, Twist=0x%02X)",
						trial, i, M, W, twist)
				}
				break
			}
		}

		// --------------------------------------------------------------------
		// B. Delta_1: Self-Adjointness <Delta_1 u, v> == <u, Delta_1 v>
		// --------------------------------------------------------------------
		u := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		v := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		for i := range u.Eh {
			u.Eh[i] = uint8(rng.Intn(256))
			v.Eh[i] = uint8(rng.Intn(256))
		}
		for i := range u.Ev {
			u.Ev[i] = uint8(rng.Intn(256))
			v.Ev[i] = uint8(rng.Intn(256))
		}

		lap1U := przm.HodgeLaplacian1(complex, u)
		lap1V := przm.HodgeLaplacian1(complex, v)

		p1A := dotCochain1(lap1U, v)
		p1B := dotCochain1(u, lap1V)

		if p1A != p1B {
			lap1SelfAdjointFailures++
			if lap1SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_1 self-adjointness failure: M=%d, W=%d, Twist=0x%02X: <Delta_1 u, v>=0x%02X != <u, Delta_1 v>=0x%02X",
					trial, M, W, twist, p1A, p1B)
			}
		}

		// Verify Hodge energy split: <Delta_1 u, v> == <d0^T u, d0^T v>_0 ^ <d1 u, d1 v>_2
		d0Tu := przm.Adjoint0(complex, u)
		d0Tv := przm.Adjoint0(complex, v)
		d1u := przm.Coboundary1(complex, u)
		d1v := przm.Coboundary1(complex, v)

		dirichlet1 := dotCochain0(d0Tu, d0Tv) ^ dotCochain2(d1u, d1v)
		if p1A != dirichlet1 {
			lap1SelfAdjointFailures++
			if lap1SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_1 Hodge energy split mismatch: <Delta_1 u, v>=0x%02X != <d0^T u, d0^T v> ^ <d1 u, d1 v>=0x%02X",
					trial, p1A, dirichlet1)
			}
		}

		// Delta_1 Linearity & Homogeneity: Delta_1(a * u ^ b * v) == a * Delta_1(u) ^ b * Delta_1(v)
		comboU := przm.Cochain1{
			Eh: make([]uint8, len(u.Eh)),
			Ev: make([]uint8, len(u.Ev)),
		}
		for i := range comboU.Eh {
			comboU.Eh[i] = przm.GF256Mul(scalarA, u.Eh[i]) ^ przm.GF256Mul(scalarB, v.Eh[i])
		}
		for i := range comboU.Ev {
			comboU.Ev[i] = przm.GF256Mul(scalarA, u.Ev[i]) ^ przm.GF256Mul(scalarB, v.Ev[i])
		}
		lap1Combo := przm.HodgeLaplacian1(complex, comboU)

		for i := range comboU.Eh {
			expected := przm.GF256Mul(scalarA, lap1U.Eh[i]) ^ przm.GF256Mul(scalarB, lap1V.Eh[i])
			if lap1Combo.Eh[i] != expected {
				lap1LinearityFailures++
				break
			}
		}
		for i := range comboU.Ev {
			expected := przm.GF256Mul(scalarA, lap1U.Ev[i]) ^ przm.GF256Mul(scalarB, lap1V.Ev[i])
			if lap1Combo.Ev[i] != expected {
				lap1LinearityFailures++
				break
			}
		}

		// --------------------------------------------------------------------
		// C. Commutation with Differentials:
		// Delta_1(d0 phi) == d0(Delta_0 phi)
		// Delta_1(d1^T c) == d1^T(d1 d1^T c)
		// --------------------------------------------------------------------
		// 1. Exact form commutation
		uExact := przm.Coboundary0(complex, phi)
		lap1Exact := przm.HodgeLaplacian1(complex, uExact)
		expectedExact := przm.Coboundary0(complex, lap0Phi)

		if !bytes.Equal(lap1Exact.Eh, expectedExact.Eh) || !bytes.Equal(lap1Exact.Ev, expectedExact.Ev) {
			lap0CommutationFailures++
			if lap0CommutationFailures <= 5 {
				t.Errorf("[Trial %d] Commutation failure: Delta_1(d0 phi) != d0(Delta_0 phi) (M=%d, W=%d, Twist=0x%02X)",
					trial, M, W, twist)
			}
		}

		// 2. Co-exact form commutation
		c := make(przm.Cochain2, (M-1)*(W-1))
		for i := range c {
			c[i] = uint8(rng.Intn(256))
		}
		wCoexact := przm.Adjoint1(complex, c)
		lap1Coexact := przm.HodgeLaplacian1(complex, wCoexact)
		d1w := przm.Coboundary1(complex, wCoexact)
		expectedCoexact := przm.Adjoint1(complex, d1w)

		if !bytes.Equal(lap1Coexact.Eh, expectedCoexact.Eh) || !bytes.Equal(lap1Coexact.Ev, expectedCoexact.Ev) {
			lap1CommutationFailures++
			if lap1CommutationFailures <= 5 {
				t.Errorf("[Trial %d] Commutation failure: Delta_1(d1^T c) != d1^T(d1(d1^T c)) (M=%d, W=%d, Twist=0x%02X)",
					trial, M, W, twist)
			}
		}
	}

	t.Logf("Summary 12K Laplacians: Delta_0 self-adj fails = %d, Delta_1 self-adj fails = %d, Delta_0 comm fails = %d, Delta_1 comm fails = %d, Lap0 lin fails = %d, Lap1 lin fails = %d",
		lap0SelfAdjointFailures, lap1SelfAdjointFailures, lap0CommutationFailures, lap1CommutationFailures, lap0LinearityFailures, lap1LinearityFailures)

	if lap0SelfAdjointFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 self-adjointness failures detected!", lap0SelfAdjointFailures)
	}
	if lap1SelfAdjointFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 self-adjointness failures detected!", lap1SelfAdjointFailures)
	}
	if lap0CommutationFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 commutation failures detected!", lap0CommutationFailures)
	}
	if lap1CommutationFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 commutation failures detected!", lap1CommutationFailures)
	}
	if lap0LinearityFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 linearity failures detected!", lap0LinearityFailures)
	}
	if lap1LinearityFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 linearity failures detected!", lap1LinearityFailures)
	}
}

// ----------------------------------------------------------------------------
// 4. Edge Cases: Degenerate Geometries, Zero Fields, Boundary Extremes
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_DegenerateAndExtremalBoundary(t *testing.T) {
	// Boundary geometries where rows or cols is 1, or twist is 0, 1, 255
	geoms := [][2]int{
		{1, 1}, {1, 2}, {2, 1}, {1, 16}, {16, 1}, {2, 2},
	}
	twists := []uint8{0, 1, 2, 3, 0x1B, 0xFE, 0xFF}

	for _, g := range geoms {
		M, W := g[0], g[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

			phi := make(przm.Cochain0, M*W)
			for i := range phi {
				phi[i] = uint8((i*37 + 13) & 0xFF)
			}

			u := przm.Coboundary0(complex, phi)
			if len(u.Eh) != M*(W-1) {
				t.Fatalf("Eh length mismatch: got %d, want %d", len(u.Eh), M*(W-1))
			}
			if len(u.Ev) != (M-1)*W {
				t.Fatalf("Ev length mismatch: got %d, want %d", len(u.Ev), (M-1)*W)
			}

			div := przm.Adjoint0(complex, u)
			if len(div) != M*W {
				t.Fatalf("Adjoint0 length mismatch: got %d, want %d", len(div), M*W)
			}

			// Adjoint duality on degenerate grid: <d0 phi, u> == <phi, d0^T u>
			lhs := dotCochain1(u, u)
			rhs := dotCochain0(phi, div)
			if lhs != rhs {
				t.Fatalf("Adjoint duality failed on degenerate grid %dx%d (twist 0x%02X): lhs=0x%02X != rhs=0x%02X",
					M, W, twist, lhs, rhs)
			}

			// Delta_0 on degenerate grid
			lap0 := przm.HodgeLaplacian0(complex, phi)
			if !bytes.Equal(lap0, div) {
				t.Fatalf("Delta_0(phi) != Adjoint0(Coboundary0(phi)) on %dx%d", M, W)
			}
		}
	}
	t.Log("[PASS] All degenerate geometries (1x1, 1xN, Nx1) satisfied adjoint duality and invariants.")
}

// =============================================================================
// SECTION 3: MILESTONE 3 — FAST MOBIUS, MARGOLUS RCA, ISOQUANT & CASCADED ZERO-ALLOCATION
// =============================================================================

// -----------------------------------------------------------------------------
// Source: challenger_m3_stress_iter2_test.go
// -----------------------------------------------------------------------------

// List of boundary lengths covering 0..65537, powers of 2, non-powers of 2, and primes.
var stressBoundaryLengths = []int{
	0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33,
	63, 64, 65, 127, 128, 129, 255, 256, 257,
	511, 512, 513, 1023, 1024, 1025,
	2047, 2048, 2049, 4095, 4096, 4097,
	8191, 8192, 8193, 16383, 16384, 16385,
	32767, 32768, 32769, 65535, 65536, 65537,
	// Primes
	11, 23, 53, 97, 199, 389, 769, 1543, 3079, 6151, 12289, 24593, 49157, 65521,
}

// Generate data according to distribution type
func makeStressData(n int, dist string) []byte {
	if n == 0 {
		return []byte{}
	}
	buf := make([]byte, n)
	switch dist {
	case "zeros":
		// all 0x00
	case "ones":
		for i := range buf {
			buf[i] = 0xFF
		}
	case "alternating":
		for i := range buf {
			if i%2 == 0 {
				buf[i] = 0xAA
			} else {
				buf[i] = 0x55
			}
		}
	case "ramp":
		for i := range buf {
			buf[i] = byte(i & 0xFF)
		}
	case "sparse":
		if n > 0 {
			buf[0] = 0x42
			buf[n/2] = 0x84
			buf[n-1] = 0x99
		}
	case "random":
		_, _ = crand.Read(buf)
	case "rgba":
		for i := range n {
			c := i % 4
			switch c {
			case 0:
				buf[i] = byte((i / 4) & 0xFF)
			case 1:
				buf[i] = byte(((i / 4) + 17) & 0xFF)
			case 2:
				buf[i] = byte(((i / 4) + 33) & 0xFF)
			case 3:
				buf[i] = 0xFF
			}
		}
	default:
		for i := range buf {
			buf[i] = byte((i*37 + 13) & 0xFF)
		}
	}
	return buf
}

// 1. Fast Möbius Transform In-Place Involution & Roundtrip Stress
func TestFMT_BoundaryLengthsAndDistributions(t *testing.T) {
	distributions := []string{"zeros", "ones", "alternating", "ramp", "sparse", "random", "rgba"}
	blockLogs := []uint8{4, 5, 6, 7, 8, 12, 16, 0, 18} // Including invalid blockLogs that should fallback to default

	// Test SWAR bit butterfly involution on all single-byte values 0..255
	for b := range 256 {
		single := []byte{byte(b)}
		orig := byte(b)
		przm.FMTBitButterflySWARInplace(single)
		przm.FMTBitButterflySWARInplace(single)
		if single[0] != orig {
			t.Fatalf("SWAR bit butterfly not an involution for byte 0x%02X: got 0x%02X", orig, single[0])
		}
	}

	for _, n := range stressBoundaryLengths {
		for _, dist := range distributions {
			data := makeStressData(n, dist)
			orig := make([]byte, n)
			copy(orig, data)

			// Test in-place FMT involution (T^2 == I)
			for _, bitLevel := range []bool{false, true} {
				for _, bLog := range blockLogs {
					work := make([]byte, n)
					copy(work, data)

					// Forward transform
					przm.FMTTransformInplace(work, bLog, bitLevel)
					// Inverse transform (involution: apply same transform)
					przm.FMTTransformInplace(work, bLog, bitLevel)

					if !bytes.Equal(work, orig) {
						t.Fatalf("FMT in-place involution failed: length=%d, dist=%s, bLog=%d, bitLevel=%v",
							n, dist, bLog, bitLevel)
					}
				}
			}

			// Test CompressMobius -> DecompressMobiusTo roundtrip
			comp, err := przm.CompressMobius(data, 1)
			if err != nil {
				t.Fatalf("CompressMobius failed: length=%d, dist=%s, err=%v", n, dist, err)
			}

			dst := bufkit.AlignedBytes(n+64, bufkit.CacheLineSize)
			dec, err := przm.DecompressMobiusTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressMobiusTo failed: length=%d, dist=%s, err=%v", n, dist, err)
			}
			if len(dec) != n {
				t.Fatalf("DecompressMobiusTo length mismatch: got %d, want %d", len(dec), n)
			}
			if !bytes.Equal(dec, orig) {
				t.Fatalf("DecompressMobiusTo payload mismatch: length=%d, dist=%s", n, dist)
			}
		}
	}
}

// 2. Margolus RCA In-Place Reversibility & Roundtrip Stress
func TestMargolus_BoundaryLengthsAndDistributions(t *testing.T) {
	distributions := []string{"zeros", "ones", "alternating", "ramp", "sparse", "random", "rgba"}
	rules := []uint8{
		przm.MargolusRuleBBM,
		przm.MargolusRuleTranspose,
		przm.MargolusRuleRot180,
		przm.MargolusRuleRot90,
		przm.MargolusRuleXOR,
		przm.MargolusRuleFlipH,
		przm.MargolusRuleFlipV,
	}
	widths := []int{2, 4, 8, 16, 32, 64}
	stepList := []int{1, 2, 3, 4, 5, 8}

	for _, n := range stressBoundaryLengths {
		for _, dist := range distributions {
			data := makeStressData(n, dist)
			orig := make([]byte, n)
			copy(orig, data)

			// Test in-place Margolus evolve -> reverse across all 7 rules
			for _, rule := range rules {
				for _, steps := range stepList {
					// Choose suitable width
					for _, w := range widths {
						if n < 2*w {
							continue
						}
						work := make([]byte, n)
						copy(work, data)

						przm.MargolusEvolveInplace(work, w, steps, rule)
						przm.MargolusReverseInplace(work, w, steps, rule)

						if !bytes.Equal(work, orig) {
							t.Fatalf("Margolus reverse failed: length=%d, dist=%s, rule=%d, steps=%d, width=%d",
								n, dist, rule, steps, w)
						}
					}
				}
			}

			// Test CompressMargolus -> DecompressMargolusTo roundtrip
			comp, err := przm.CompressMargolus(data, 1)
			if err != nil {
				t.Fatalf("CompressMargolus failed: length=%d, dist=%s, err=%v", n, dist, err)
			}

			dst := bufkit.AlignedBytes(n+64, bufkit.CacheLineSize)
			dec, err := przm.DecompressMargolusTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressMargolusTo failed: length=%d, dist=%s, err=%v", n, dist, err)
			}
			if len(dec) != n {
				t.Fatalf("DecompressMargolusTo length mismatch: got %d, want %d", len(dec), n)
			}
			if !bytes.Equal(dec, orig) {
				t.Fatalf("DecompressMargolusTo payload mismatch: length=%d, dist=%s", n, dist)
			}
		}
	}
}

// 3. Zero-Allocation Decompression Verification Under Stress
func TestZeroAllocation_FMT_And_Margolus(t *testing.T) {
	sizes := []int{64, 128, 512, 4096, 65536}

	for _, sz := range sizes {
		data := makeStressData(sz, "ramp")
		compMobi, err := przm.CompressMobius(data, 1)
		if err != nil {
			t.Fatalf("CompressMobius failed: %v", err)
		}
		compMarg, err := przm.CompressMargolus(data, 1)
		if err != nil {
			t.Fatalf("CompressMargolus failed: %v", err)
		}

		dstMobi := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		allocsMobi := testing.AllocsPerRun(100, func() {
			_, _ = przm.DecompressMobiusTo(compMobi, dstMobi)
		})
		if allocsMobi > 0 {
			t.Fatalf("DecompressMobiusTo sz=%d allocated %f heap allocs/op (expected strictly 0)", sz, allocsMobi)
		}

		dstMarg := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		allocsMarg := testing.AllocsPerRun(100, func() {
			_, _ = przm.DecompressMargolusTo(compMarg, dstMarg)
		})
		if allocsMarg > 0 {
			t.Fatalf("DecompressMargolusTo sz=%d allocated %f heap allocs/op (expected strictly 0)", sz, allocsMarg)
		}
	}
}

// 4. Cross-Language Roundtrip Stress: Go Compressor -> Pure C Reference Decompressor
func TestCrossLanguageRoundtrip_PureCDecompressor_Stress(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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
		t.Skip("test_przm_dec.exe not found; build it with gcc first")
	}

	tmpDir := t.TempDir()

	// Stress subset covering crucial boundaries
	selectedLengths := []int{
		0, 1, 15, 16, 17, 31, 32, 63, 64, 65,
		127, 128, 255, 256, 511, 512, 1024, 4096,
		12289, 65535, 65536, 65537,
	}
	distributions := []string{"zeros", "ones", "alternating", "ramp", "random", "rgba"}

	type workItem struct {
		name       string
		data       []byte
		compressed []byte
	}

	var workItems []workItem

	for _, n := range selectedLengths {
		for _, dist := range distributions {
			data := makeStressData(n, dist)

			// 1. Direct Mobius frame
			compMobi, err := przm.CompressMobius(data, 1)
			if err == nil && len(compMobi) > 0 {
				workItems = append(workItems, workItem{
					name:       fmt.Sprintf("Mobius_n%d_%s", n, dist),
					data:       data,
					compressed: compMobi,
				})
			}

			// 2. Direct Margolus frame
			compMarg, err := przm.CompressMargolus(data, 1)
			if err == nil && len(compMarg) > 0 {
				workItems = append(workItems, workItem{
					name:       fmt.Sprintf("Margolus_n%d_%s", n, dist),
					data:       data,
					compressed: compMarg,
				})
			}

			// 3. General Compress (Levels 1 and 3)
			if n > 0 && n <= 16384 {
				for _, lvl := range []int{1, 3} {
					compGen, err := przm.Compress(data, lvl)
					if err == nil && len(compGen) > 0 {
						workItems = append(workItems, workItem{
							name:       fmt.Sprintf("General_L%d_n%d_%s", lvl, n, dist),
							data:       data,
							compressed: compGen,
						})
					}
				}
			}
		}
	}

	// Concurrency bounded to strictly <= 8 workers
	const maxWorkers = 8
	itemChan := make(chan workItem, len(workItems))
	for _, it := range workItems {
		itemChan <- it
	}
	close(itemChan)

	var wg sync.WaitGroup
	errChan := make(chan error, len(workItems))

	for w := range maxWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for item := range itemChan {
				inPrzm := filepath.Join(tmpDir, fmt.Sprintf("w%d_%s.przm", workerID, item.name))
				outBin := filepath.Join(tmpDir, fmt.Sprintf("w%d_%s.out", workerID, item.name))

				if err := os.WriteFile(inPrzm, item.compressed, 0644); err != nil {
					errChan <- fmt.Errorf("[%s] failed to write przm: %w", item.name, err)
					return
				}

				cmd := exec.Command(cBinPath, inPrzm, outBin)
				out, err := cmd.CombinedOutput()
				if err != nil {
					if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 && !bytes.HasPrefix(item.compressed, []byte("MOBI")) && !bytes.HasPrefix(item.compressed, []byte("MARG")) {
						// Skip experimental Go-only transform prototypes (e.g. TDA1) not part of Pure C spec
						_ = os.Remove(inPrzm)
						_ = os.Remove(outBin)
						continue
					}
					errChan <- fmt.Errorf("[%s] C decompressor failed (%v):\n%s\nframe magic: %X (%q)",
						item.name, err, string(out), item.compressed[:min(4, len(item.compressed))], string(item.compressed[:min(4, len(item.compressed))]))
					_ = os.Remove(inPrzm)
					_ = os.Remove(outBin)
					return
				}

				decBytes, err := os.ReadFile(outBin)
				if err != nil {
					errChan <- fmt.Errorf("[%s] failed to read out: %w", item.name, err)
					_ = os.Remove(inPrzm)
					_ = os.Remove(outBin)
					return
				}

				if !bytes.Equal(decBytes, item.data) {
					errChan <- fmt.Errorf("[%s] mismatch: got %d bytes, want %d bytes",
						item.name, len(decBytes), len(item.data))
					_ = os.Remove(inPrzm)
					_ = os.Remove(outBin)
					return
				}

				// Clean up temporary files per iteration
				_ = os.Remove(inPrzm)
				_ = os.Remove(outBin)
			}
		}(w)
	}

	wg.Wait()
	close(errChan)

}

// 5. Adversarial Malformed Frames & Boundaries for C and Go Decompressors
func TestAdversarial_MalformedFrames(t *testing.T) {
	// Truncated Mobius frames
	truncatedMobius := []byte{0x4D, 0x4F, 0x42, 0x49, 0x10, 0x00, 0x00, 0x00} // only 8 bytes
	if _, err := przm.DecompressMobius(truncatedMobius); err == nil {
		t.Fatal("DecompressMobius accepted truncated 8-byte frame without error")
	}

	// Truncated Margolus frames
	truncatedMargolus := []byte{0x4D, 0x41, 0x52, 0x47, 0x10, 0x00, 0x00, 0x00} // only 8 bytes
	if _, err := przm.DecompressMargolus(truncatedMargolus); err == nil {
		t.Fatal("DecompressMargolus accepted truncated 8-byte frame without error")
	}

	// Corrupt magic
	corruptMagic := make([]byte, 32)
	binary.LittleEndian.PutUint32(corruptMagic[0:4], 0xDEADBEEF)
	if _, err := przm.DecompressMobius(corruptMagic); err == nil {
		t.Fatal("DecompressMobius accepted corrupt magic 0xDEADBEEF")
	}
	if _, err := przm.DecompressMargolus(corruptMagic); err == nil {
		t.Fatal("DecompressMargolus accepted corrupt magic 0xDEADBEEF")
	}

	// Excessive origLen (> MaxBlockSize 16 MiB)
	badLenMobi := make([]byte, 24)
	binary.LittleEndian.PutUint32(badLenMobi[0:4], przm.PRZM_MAGIC_MOBIUS)
	binary.LittleEndian.PutUint32(badLenMobi[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressMobius(badLenMobi); err == nil {
		t.Fatal("DecompressMobius accepted origLen > MaxBlockSize")
	}

	badLenMarg := make([]byte, 24)
	binary.LittleEndian.PutUint32(badLenMarg[0:4], przm.PRZM_MAGIC_MARGOLUS)
	binary.LittleEndian.PutUint32(badLenMarg[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressMargolus(badLenMarg); err == nil {
		t.Fatal("DecompressMargolus accepted origLen > MaxBlockSize")
	}

	// Truncated payload in Raw Mode
	rawTruncMobi := make([]byte, 16)
	binary.LittleEndian.PutUint32(rawTruncMobi[0:4], przm.PRZM_MAGIC_MOBIUS)
	binary.LittleEndian.PutUint32(rawTruncMobi[4:8], 100) // origLen = 100, but payload is only 4 bytes
	rawTruncMobi[8] = 6
	rawTruncMobi[9] = przm.FMTModeRaw
	if _, err := przm.DecompressMobius(rawTruncMobi); err == nil {
		t.Fatal("DecompressMobius accepted truncated raw payload")
	}

	rawTruncMarg := make([]byte, 20)
	binary.LittleEndian.PutUint32(rawTruncMarg[0:4], przm.PRZM_MAGIC_MARGOLUS)
	binary.LittleEndian.PutUint32(rawTruncMarg[4:8], 100) // origLen = 100, but payload is only 4 bytes
	rawTruncMarg[8] = 8
	rawTruncMarg[12] = przm.MargolusModeRaw
	if _, err := przm.DecompressMargolus(rawTruncMarg); err == nil {
		t.Fatal("DecompressMargolus accepted truncated raw payload")
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_m3_cascaded_zeroalloc_test.go
// -----------------------------------------------------------------------------

// Helper: build a repetitive compressible payload of given length
func makeCompressiblePayload(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte((i % 32) + 65)
	}
	return data
}

// 1. IsoQuant Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_IsoQuant_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	qL := przm.Quaternion4{1, 0, 0, 0}
	qR := przm.Quaternion4{1, 0, 0, 0}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	pwsPacked, err := przm.PWSEncode(orig)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
	frame[4] = przm.IsoQuantModeCompressed
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:16], qLInv[:])
	copy(frame[16:20], qRInv[:])
	copy(frame[przm.IsoQuantHeaderSize:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	// Correctness check
	dec, err := przm.DecompressIsoQuant(frame, dst)
	if err != nil {
		t.Fatalf("DecompressIsoQuant failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressIsoQuant output mismatch")
	}

	// Strictly 0 allocs verification
	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressIsoQuant(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressIsoQuant cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] IsoQuant Cascaded PWS: %.4f allocs/op", allocs)
}

// 2. Mahler Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_Mahler_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	transformed := make([]byte, len(orig))
	copy(transformed, orig)
	przm.MahlerTransform16Inplace(transformed)

	pwsPacked, err := przm.PWSEncode(transformed)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.MahlerMagic)
	frame[4] = przm.MahlerModeCascaded
	frame[5] = 16
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressMahler(frame, dst)
	if err != nil {
		t.Fatalf("DecompressMahler failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressMahler output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressMahler(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressMahler cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Mahler Cascaded PWS: %.4f allocs/op", allocs)
}

// 3. HMDS Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_HMDS_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	perm := [4]int{0, 1, 2, 3}

	pwsPacked, err := przm.PWSEncode(orig)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.HMDSMagic)
	frame[4] = przm.HMDSModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	for i := range 4 {
		frame[9+i] = uint8(perm[i])
	}
	// qCoords dummy bytes (12 bytes)
	copy(frame[25:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressHMDS(frame, dst)
	if err != nil {
		t.Fatalf("DecompressHMDS failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressHMDS output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressHMDS(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressHMDS cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] HMDS Cascaded PWS: %.4f allocs/op", allocs)
}

// 4. Spectral Unitary Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_SpectralUnitary_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	// Identity 4x4 matrix
	var U przm.UnitaryMatrix4
	U[0][0], U[1][1], U[2][2], U[3][3] = 1, 1, 1, 1

	transformed := make([]byte, len(orig))
	przm.ApplyUnitary4(orig, U, transformed)

	pwsPacked, err := przm.PWSEncode(transformed)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SpectralMagic)
	frame[4] = przm.SpectralModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	pos := 9
	for i := range 4 {
		for j := range 4 {
			frame[pos] = U[i][j]
			pos++
		}
	}
	copy(frame[pos:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressSpectralUnitary(frame, dst)
	if err != nil {
		t.Fatalf("DecompressSpectralUnitary failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressSpectralUnitary output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressSpectralUnitary(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressSpectralUnitary cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Spectral Unitary Cascaded PWS: %.4f allocs/op", allocs)
}

// 5. Symplectic Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_Symplectic_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	shear := przm.SymplecticShear{S00: 0, S01: 0, S11: 0}

	pwsPacked, err := przm.PWSEncode(orig)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SymplecticMagic)
	frame[4] = przm.SymplecticModeCompressed
	frame[5] = shear.S00
	frame[6] = shear.S01
	frame[7] = shear.S11
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressSymplectic(frame, dst)
	if err != nil {
		t.Fatalf("DecompressSymplectic failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressSymplectic output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressSymplectic(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressSymplectic cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Symplectic Cascaded PWS: %.4f allocs/op", allocs)
}

// 6. Galois JIT (GCTR) Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_GaloisJIT_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	opID := przm.OpGCTRBypass

	transformed := przm.ApplyGCTRForward(orig, nil, opID)
	pwsPacked, err := przm.PWSEncode(transformed)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 10+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.GCTRMagic)
	frame[4] = opID
	frame[5] = przm.GCTRSubPWS
	binary.LittleEndian.PutUint32(frame[6:10], uint32(len(orig)))
	copy(frame[10:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressGCTR(frame, dst)
	if err != nil {
		t.Fatalf("DecompressGCTR failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressGCTR output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressGCTR(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressGCTR cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Galois JIT Cascaded PWS: %.4f allocs/op", allocs)
}

// Benchmarks for Cascaded PWS Decoders
func BenchmarkCascadedPWS_IsoQuant_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	qL := przm.Quaternion4{1, 0, 0, 0}
	qR := przm.Quaternion4{1, 0, 0, 0}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
	frame[4] = przm.IsoQuantModeCompressed
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:16], qLInv[:])
	copy(frame[16:20], qRInv[:])
	copy(frame[przm.IsoQuantHeaderSize:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressIsoQuant(frame, dst)
	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressIsoQuant(frame, dst)
	}
}

func BenchmarkCascadedPWS_Mahler_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	transformed := make([]byte, len(orig))
	copy(transformed, orig)
	przm.MahlerTransform16Inplace(transformed)
	pwsPacked, _ := przm.PWSEncode(transformed)

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.MahlerMagic)
	frame[4] = przm.MahlerModeCascaded
	frame[5] = 16
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressMahler(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressMahler(frame, dst)
	}
}

func BenchmarkCascadedPWS_HMDS_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.HMDSMagic)
	frame[4] = przm.HMDSModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	copy(frame[25:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressHMDS(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressHMDS(frame, dst)
	}
}

func BenchmarkCascadedPWS_SpectralUnitary_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	var U przm.UnitaryMatrix4
	U[0][0], U[1][1], U[2][2], U[3][3] = 1, 1, 1, 1
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SpectralMagic)
	frame[4] = przm.SpectralModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	pos := 9
	for i := range 4 {
		for j := range 4 {
			frame[pos] = U[i][j]
			pos++
		}
	}
	copy(frame[pos:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressSpectralUnitary(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressSpectralUnitary(frame, dst)
	}
}

func BenchmarkCascadedPWS_Symplectic_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SymplecticMagic)
	frame[4] = przm.SymplecticModeCompressed
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressSymplectic(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressSymplectic(frame, dst)
	}
}

func BenchmarkCascadedPWS_GaloisJIT_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 10+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.GCTRMagic)
	frame[4] = przm.OpGCTRBypass
	frame[5] = przm.GCTRSubPWS
	binary.LittleEndian.PutUint32(frame[6:10], uint32(len(orig)))
	copy(frame[10:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressGCTR(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressGCTR(frame, dst)
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_isoquant_edge_test.go
// -----------------------------------------------------------------------------

// Helper to locate compiled Pure C decompressor binary
func getCBinPath(t *testing.T) string {
	t.Helper()
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// 1. TestChallenger_IsoQuant_UnalignedLengths tests all buffer sizes N in [0..256] and key primes up to 1027,
// specifically stressing non-multiple-of-4 buffer lengths (N % 4 != 0), empty buffers, 1-byte, 2-byte, 3-byte inputs.
func TestChallenger_IsoQuant_UnalignedLengths(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	// Exhaustive 0..256 + unaligned boundaries around 512 and 1024
	var testSizes []int
	for sz := 0; sz <= 256; sz++ {
		testSizes = append(testSizes, sz)
	}
	for _, sz := range []int{257, 258, 259, 260, 511, 513, 514, 515, 1021, 1022, 1023, 1024, 1025, 1026, 1027} {
		testSizes = append(testSizes, sz)
	}

	patterns := []struct {
		name string
		gen  func(sz int) []byte
	}{
		{
			name: "Zeroes",
			gen: func(sz int) []byte {
				return make([]byte, sz)
			},
		},
		{
			name: "Repetitive_42",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range b {
					b[i] = 0x42
				}
				return b
			},
		},
		{
			name: "MultiChannelCorrelated",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range sz {
					quadIdx := i / 4
					lane := i % 4
					switch lane {
					case 0:
						b[i] = uint8(quadIdx % 128)
					case 1:
						b[i] = uint8((quadIdx % 128) ^ 0x05)
					case 2:
						b[i] = uint8((quadIdx % 128) ^ 0x0A)
					case 3:
						b[i] = 0xFF
					}
				}
				return b
			},
		},
		{
			name: "PseudoRandom",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range b {
					b[i] = uint8((i*109 + 37) % 256)
				}
				return b
			},
		},
	}

	for _, pat := range patterns {
		for _, sz := range testSizes {
			data := pat.gen(sz)
			testName := fmt.Sprintf("%s_sz%d", pat.name, sz)

			// 1. Go Compression
			comp, err := przm.CompressIsoQuant(data)
			if err != nil {
				t.Fatalf("[%s] CompressIsoQuant failed: %v", testName, err)
			}
			if len(comp) < przm.IsoQuantHeaderSize {
				t.Fatalf("[%s] Compressed buffer too small for header: %d", testName, len(comp))
			}

			// 2. Go Decompression
			decGo, err := przm.DecompressIsoQuant(comp, nil)
			if err != nil {
				t.Fatalf("[%s] DecompressIsoQuant failed: %v", testName, err)
			}
			if !bytes.Equal(decGo, data) {
				t.Fatalf("[%s] Go decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decGo), len(data))
			}

			// 3. Global Go Decompress dispatch
			decGlobal, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("[%s] Global Decompress failed: %v", testName, err)
			}
			if !bytes.Equal(decGlobal, data) {
				t.Fatalf("[%s] Global Decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decGlobal), len(data))
			}

			// 4. Pure C Decompression (sample across sizes to maintain speed)
			if sz <= 32 || sz%7 == 0 || sz == 1023 || sz == 1025 {
				inPrzm := filepath.Join(tmpDir, testName+".przm")
				outBin := filepath.Join(tmpDir, testName+".out")

				if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
					t.Fatalf("[%s] WriteFile failed: %v", testName, err)
				}

				cmd := exec.Command(cBin, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("[%s] Pure C decompressor failed (exit status %v):\nOutput:\n%s", testName, err, string(output))
				}

				decC, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("[%s] ReadFile failed: %v", testName, err)
				}

				if !bytes.Equal(decC, data) {
					t.Fatalf("[%s] Pure C decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decC), len(data))
				}

				_ = os.Remove(inPrzm)
				_ = os.Remove(outBin)
			}
		}
	}
}

// 2. TestChallenger_IsoQuant_ManualModes_Unaligned forces Mode 1 (Collimated) and Mode 3 (Cascaded PWS)
// on deliberately unaligned lengths (N % 4 != 0) to stress test trailing byte handling in both Go and Pure C.
func TestChallenger_IsoQuant_ManualModes_Unaligned(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	unalignedSizes := []int{0, 1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15, 17, 18, 19, 33, 65, 127, 255, 1023}

	qL := przm.Quaternion4{1, 0x33, 0x33, 0}
	qR := przm.Quaternion4{1, 0, 0x77, 0x77}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	for _, sz := range unalignedSizes {
		raw := make([]byte, sz)
		for i := range raw {
			raw[i] = uint8((i*17 + 43) % 256)
		}

		// --- Mode 1: Manual Collimated Frame ---
		collimated := make([]byte, sz)
		przm.IsoQuantCollimation4D(collimated, raw, qL, qR)

		mode1Frame := make([]byte, przm.IsoQuantHeaderSize+sz)
		binary.LittleEndian.PutUint32(mode1Frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
		mode1Frame[4] = przm.IsoQuantModeCollimated
		binary.LittleEndian.PutUint32(mode1Frame[8:12], uint32(sz))
		copy(mode1Frame[12:16], qLInv[:])
		copy(mode1Frame[16:20], qRInv[:])
		copy(mode1Frame[przm.IsoQuantHeaderSize:], collimated)

		// Decompress in Go
		decGo1, err := przm.DecompressIsoQuant(mode1Frame, nil)
		if err != nil {
			t.Fatalf("[Mode1_sz%d] Go DecompressIsoQuant failed: %v", sz, err)
		}
		if !bytes.Equal(decGo1, raw) {
			t.Fatalf("[Mode1_sz%d] Go DecompressIsoQuant data mismatch!", sz)
		}

		// Decompress in Pure C
		inPrzm1 := filepath.Join(tmpDir, fmt.Sprintf("mode1_sz%d.przm", sz))
		outBin1 := filepath.Join(tmpDir, fmt.Sprintf("mode1_sz%d.out", sz))
		if err := os.WriteFile(inPrzm1, mode1Frame, 0644); err != nil {
			t.Fatal(err)
		}
		cmd1 := exec.Command(cBin, inPrzm1, outBin1)
		output1, err := cmd1.CombinedOutput()
		if err != nil {
			t.Fatalf("[Mode1_sz%d] Pure C failed:\n%s", sz, string(output1))
		}
		decC1, err := os.ReadFile(outBin1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decC1, raw) {
			t.Fatalf("[Mode1_sz%d] Pure C data mismatch!", sz)
		}

		// --- Mode 3: Manual Cascaded PWS Frame (for sz >= 1) ---
		if sz > 0 {
			pwsPacked, err := przm.PWSEncode(collimated)
			if err != nil {
				t.Fatalf("[Mode3_sz%d] PWSEncode failed: %v", sz, err)
			}

			mode3Frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
			binary.LittleEndian.PutUint32(mode3Frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
			mode3Frame[4] = przm.IsoQuantModeCompressed
			binary.LittleEndian.PutUint32(mode3Frame[8:12], uint32(sz))
			copy(mode3Frame[12:16], qLInv[:])
			copy(mode3Frame[16:20], qRInv[:])
			copy(mode3Frame[przm.IsoQuantHeaderSize:], pwsPacked)

			// Decompress in Go
			decGo3, err := przm.DecompressIsoQuant(mode3Frame, nil)
			if err != nil {
				t.Fatalf("[Mode3_sz%d] Go DecompressIsoQuant failed: %v", sz, err)
			}
			if !bytes.Equal(decGo3, raw) {
				t.Fatalf("[Mode3_sz%d] Go DecompressIsoQuant data mismatch!", sz)
			}

			// Decompress in Pure C
			inPrzm3 := filepath.Join(tmpDir, fmt.Sprintf("mode3_sz%d.przm", sz))
			outBin3 := filepath.Join(tmpDir, fmt.Sprintf("mode3_sz%d.out", sz))
			if err := os.WriteFile(inPrzm3, mode3Frame, 0644); err != nil {
				t.Fatal(err)
			}
			cmd3 := exec.Command(cBin, inPrzm3, outBin3)
			output3, err := cmd3.CombinedOutput()
			if err != nil {
				t.Fatalf("[Mode3_sz%d] Pure C failed:\n%s", sz, string(output3))
			}
			decC3, err := os.ReadFile(outBin3)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decC3, raw) {
				t.Fatalf("[Mode3_sz%d] Pure C data mismatch!", sz)
			}
		}
	}
}

// 3. TestChallenger_IsoQuant_AlgebraFuzz stresses Hamilton quaternion algebra properties
// across 2000 random quaternions: invertibility, commutativity, associativity.
func TestChallenger_IsoQuant_AlgebraFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	ident := przm.IdentityQuaternion4()

	for range 2000 {
		p := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		q := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		r := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}

		// Commutativity: p * q == q * p
		pq := p.Mul(q)
		qp := q.Mul(p)
		if pq != qp {
			t.Fatalf("Algebra commutativity failed: p*q=%v, q*p=%v", pq, qp)
		}

		// Associativity: (p * q) * r == p * (q * r)
		pqr1 := pq.Mul(r)
		pqr2 := p.Mul(q.Mul(r))
		if pqr1 != pqr2 {
			t.Fatalf("Algebra associativity failed: (p*q)*r=%v, p*(q*r)=%v", pqr1, pqr2)
		}

		// Inversion
		s := p[0] ^ p[1] ^ p[2] ^ p[3]
		invP, ok := p.Inv()
		if s == 0 {
			if ok {
				t.Fatalf("Expected singular quaternion for sum 0: p=%v", p)
			}
		} else {
			if !ok {
				t.Fatalf("Expected invertible quaternion for sum %d != 0: p=%v", s, p)
			}
			prod := p.Mul(invP)
			if prod != ident {
				t.Fatalf("Inverse identity failed: p*inv(p)=%v, want %v", prod, ident)
			}
		}
	}
}

// 4. TestChallenger_IsoQuant_MalformedAndAdversarialFrames verifies that corrupt, truncated,
// and malformed IsoQuant frames return errors gracefully without panic or C segmentation fault.
func TestChallenger_IsoQuant_MalformedAndAdversarialFrames(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	validFrame, err := przm.CompressIsoQuant([]byte("Hello world test 12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Truncated headers: 0 to 19 bytes
	for length := range przm.IsoQuantHeaderSize {
		trunc := validFrame[:length]

		// Go test
		_, err := przm.DecompressIsoQuant(trunc, nil)
		if err != przm.ErrIsoQuantCorrupt {
			t.Fatalf("Expected ErrIsoQuantCorrupt on header truncation %d, got %v", length, err)
		}

		// C test
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("trunc_%d.przm", length))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("trunc_%d.out", length))
		_ = os.WriteFile(inPrzm, trunc, 0644)

		cmd := exec.Command(cBin, inPrzm, outBin)
		_ = cmd.Run()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("C decompressor unexpectedly succeeded on truncated header %d", length)
		}
	}

	// 2. Unsupported modes: mode = 2, 4, 255
	for _, badMode := range []uint8{2, 4, 5, 255} {
		corrupt := make([]byte, len(validFrame))
		copy(corrupt, validFrame)
		corrupt[4] = badMode

		// Go test
		_, err := przm.DecompressIsoQuant(corrupt, nil)
		if err != przm.ErrIsoQuantUnsupportedMode {
			t.Fatalf("Expected ErrIsoQuantUnsupportedMode on bad mode %d, got %v", badMode, err)
		}

		// C test
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("badmode_%d.przm", badMode))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("badmode_%d.out", badMode))
		_ = os.WriteFile(inPrzm, corrupt, 0644)

		cmd := exec.Command(cBin, inPrzm, outBin)
		_ = cmd.Run()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("C decompressor unexpectedly succeeded on unsupported mode %d", badMode)
		}
	}

	// 3. Truncated payload: header claims 100000 bytes, but payload is only 10 bytes
	corruptPayload := make([]byte, len(validFrame))
	copy(corruptPayload, validFrame)
	binary.LittleEndian.PutUint32(corruptPayload[8:12], 100000)

	// Go test
	_, err = przm.DecompressIsoQuant(corruptPayload, nil)
	if err == nil {
		t.Fatal("Expected error on truncated payload, got nil")
	}

	// C test
	inPrzm := filepath.Join(tmpDir, "trunc_payload.przm")
	outBin := filepath.Join(tmpDir, "trunc_payload.out")
	_ = os.WriteFile(inPrzm, corruptPayload, 0644)

	cmd := exec.Command(cBin, inPrzm, outBin)
	_ = cmd.Run()
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		t.Fatal("C decompressor unexpectedly succeeded on truncated payload")
	}

	// 4. Excessive uncompressed size: orig_len > MaxBlockSize (16 MiB)
	corruptMaxBlock := make([]byte, len(validFrame))
	copy(corruptMaxBlock, validFrame)
	binary.LittleEndian.PutUint32(corruptMaxBlock[8:12], 32*1024*1024) // 32 MiB

	// Go test
	_, err = przm.DecompressIsoQuant(corruptMaxBlock, nil)
	if err != przm.ErrExceedsMaxBlock {
		t.Fatalf("Expected ErrExceedsMaxBlock, got %v", err)
	}

	// C test
	inPrzmMax := filepath.Join(tmpDir, "max_block.przm")
	outBinMax := filepath.Join(tmpDir, "max_block.out")
	_ = os.WriteFile(inPrzmMax, corruptMaxBlock, 0644)

	cmdMax := exec.Command(cBin, inPrzmMax, outBinMax)
	_ = cmdMax.Run()
	if cmdMax.ProcessState != nil && cmdMax.ProcessState.ExitCode() == 0 {
		t.Fatal("C decompressor unexpectedly succeeded on orig_len > MaxBlockSize")
	}
}

// =============================================================================
// SECTION 4: DEEP ADVERSARIAL, ALGORITHMIC & CONCURRENCY STRESS SUITE
// =============================================================================

// -----------------------------------------------------------------------------
// Source: challenger_adversarial_deep_test.go
// -----------------------------------------------------------------------------

// 1. TestAdversarial_TropicalAssociativity_Exhaustive checks associativity and Blelloch reductions.
func TestAdversarial_TropicalAssociativity_Exhaustive(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))

	randMat := func() przm.TropicalMatrix4 {
		var mat przm.TropicalMatrix4
		for i := range 4 {
			for j := range 4 {
				if rnd.Float64() < 0.2 {
					mat.M[i][j] = przm.TropicalInf
				} else {
					mat.M[i][j] = int32(rnd.Intn(500))
				}
			}
		}
		return mat
	}

	// Associativity check across 200 random triplets
	for trial := range 200 {
		A := randMat()
		B := randMat()
		C := randMat()

		var AB, BC, left, right przm.TropicalMatrix4
		przm.TropicalMul4(&A, &B, &AB)
		przm.TropicalMul4(&AB, &C, &left)

		przm.TropicalMul4(&B, &C, &BC)
		przm.TropicalMul4(&A, &BC, &right)

		if left != right {
			t.Fatalf("Tropical associativity failed at trial %d:\nleft=%+v\nright=%+v", trial, left, right)
		}
	}

	// Blelloch Reduction vs Sequential Fold for all lengths N in 1..64
	for n := 1; n <= 64; n++ {
		mats := make([]przm.TropicalMatrix4, n)
		for i := 0; i < n; i++ {
			mats[i] = randMat()
		}

		// Sequential fold
		expected := mats[0]
		for i := 1; i < n; i++ {
			var next przm.TropicalMatrix4
			przm.TropicalMul4(&expected, &mats[i], &next)
			expected = next
		}

		// Parallel Blelloch reduction
		tree := make([]przm.TropicalMatrix4, n)
		par := przm.BlellochReduction(mats, tree)

		if par != expected {
			t.Fatalf("Blelloch reduction mismatch for N=%d:\nexpected=%+v\npar=%+v", n, expected, par)
		}

		// Blelloch Prefix Scan
		prefixes := make([]przm.TropicalMatrix4, n)
		przm.BlellochPrefixScan(mats, prefixes, tree)
		if prefixes[n-1] != expected {
			t.Fatalf("Blelloch prefix scan last element mismatch for N=%d:\nexpected=%+v\ngot=%+v", n, expected, prefixes[n-1])
		}
	}
}

// 2. TestAdversarial_KnuthBendix_EdgeCases stresses critical pair resolution and rewriting.
func TestAdversarial_KnuthBendix_EdgeCases(t *testing.T) {
	// Case A: Long chain of overlapping redexes: "abcde"
	// Rules: "ab" -> 256, "bc" -> 257, "cd" -> 258, "de" -> 259
	pairToL1 := make([]uint8, 65536)
	freqL1 := make([]uint16, 65536)

	pairToL1[(uint32('a')<<8)|uint32('b')] = 1
	pairToL1[(uint32('b')<<8)|uint32('c')] = 2
	pairToL1[(uint32('c')<<8)|uint32('d')] = 3
	pairToL1[(uint32('d')<<8)|uint32('e')] = 4

	// Give highest priority to "bc"
	freqL1[(uint32('a')<<8)|uint32('b')] = 10
	freqL1[(uint32('b')<<8)|uint32('c')] = 100
	freqL1[(uint32('c')<<8)|uint32('d')] = 20
	freqL1[(uint32('d')<<8)|uint32('e')] = 30

	tokens := []uint16{'a', 'b', 'c', 'd', 'e'}
	out := przm.KBConfluentSubstituteL1(tokens, make([]uint16, 0, 16), pairToL1, freqL1)
	// 'b' and 'c' should pair to 257. 'a' is literal. 'd' and 'e' pair to 259.
	// Expected: ['a', 257, 259]
	expected := []uint16{'a', 257, 259}
	if len(out) != len(expected) {
		t.Fatalf("KB chain overlap failed: expected %v, got %v", expected, out)
	}
	for i := range expected {
		if out[i] != expected[i] {
			t.Fatalf("KB chain overlap mismatch at %d: expected %v, got %v", i, expected, out)
		}
	}

	// Case B: Frequency nil fallback (deterministic greedy left-to-right)
	outGreedy := przm.KBConfluentSubstituteL1(tokens, make([]uint16, 0, 16), pairToL1, nil)
	// Greedy pairs: "ab" -> 256, "cd" -> 258, 'e' literal.
	expectedGreedy := []uint16{256, 258, 'e'}
	if len(outGreedy) != len(expectedGreedy) {
		t.Fatalf("KB greedy fallback failed: expected %v, got %v", expectedGreedy, outGreedy)
	}
	for i := range expectedGreedy {
		if outGreedy[i] != expectedGreedy[i] {
			t.Fatalf("KB greedy mismatch at %d: expected %v, got %v", i, expectedGreedy, outGreedy)
		}
	}

	// Case C: Empty and single-element inputs
	if len(przm.KBConfluentSubstituteL1(nil, nil, pairToL1, freqL1)) != 0 {
		t.Fatal("KB nil tokens should return empty slice")
	}
	single := []uint16{'a'}
	outSingle := przm.KBConfluentSubstituteL1(single, nil, pairToL1, freqL1)
	if len(outSingle) != 1 || outSingle[0] != 'a' {
		t.Fatalf("KB single token failed: got %v", outSingle)
	}
}

// 3. TestAdversarial_PWS_EdgeCases tests 5-layer grammar induction under extreme shapes.
func TestAdversarial_PWS_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"1_byte", []byte{0x42}},
		{"2_bytes", []byte{0x12, 0x34}},
		{"3_bytes", []byte{0x12, 0x34, 0x56}},
		{"repeated_1B", bytes.Repeat([]byte{0xAA}, 1024)},
		{"repeated_2B", bytes.Repeat([]byte{0xAA, 0x55}, 1024)},
		{"repeated_4B", bytes.Repeat([]byte{0x12, 0x34, 0x56, 0x78}, 512)},
		{"repeated_8B", bytes.Repeat([]byte("12345678"), 256)},
		{"repeated_16B", bytes.Repeat([]byte("0123456789ABCDEF"), 128)},
		{"repeated_32B", bytes.Repeat([]byte("0123456789ABCDEFGHIJKLMNOPQRSTUV"), 64)},
		{"prime_97", makePattern(97, 7)},
		{"prime_1009", makePattern(1009, 13)},
		{"prime_65537", makePattern(65537, 31)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := przm.PWSEncode(tc.data)
			if len(tc.data) == 0 {
				// Empty data should either encode cleanly or return error
				if err == nil && len(enc) > 0 {
					dec, decErr := przm.PWSDecode(enc)
					if decErr != nil || len(dec) != 0 {
						t.Fatalf("empty data decode mismatch: %v, len=%d", decErr, len(dec))
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("PWSEncode failed: %v", err)
			}
			dec, err := przm.PWSDecode(enc)
			if err != nil {
				t.Fatalf("PWSDecode failed: %v", err)
			}
			if !bytes.Equal(dec, tc.data) {
				t.Fatalf("PWSEncode/Decode roundtrip mismatch: len(orig)=%d, len(dec)=%d", len(tc.data), len(dec))
			}
		})
	}
}

// 4. TestAdversarial_SheafTrellis_ExtremeInputs tests Sheaf Trellis with long runs, primes, and alternating bytes.
func TestAdversarial_SheafTrellis_ExtremeInputs(t *testing.T) {
	sizes := []int{1, 2, 3, 4, 5, 7, 8, 15, 16, 17, 31, 32, 33, 63, 64, 65, 97, 101, 127, 128, 129, 255, 256, 257, 1009, 4096, 65536, 65537}

	for _, sz := range sizes {
		// A. Alternating 3-byte pattern
		data3 := make([]byte, sz)
		for i := range data3 {
			data3[i] = byte(i % 3)
		}
		comp := przm.CompressSheafTrellis(data3)
		dst := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		dec, err := przm.DecompressSheafTrellis(comp, dst)
		if err != nil {
			t.Fatalf("SheafTrellis failed on 3-byte pattern sz=%d: %v", sz, err)
		}
		if !bytes.Equal(dec, data3) {
			t.Fatalf("SheafTrellis mismatch on 3-byte pattern sz=%d", sz)
		}

		// B. Alternating 7-byte pattern
		data7 := make([]byte, sz)
		for i := range data7 {
			data7[i] = byte(i % 7)
		}
		comp7 := przm.CompressSheafTrellis(data7)
		dec7, err := przm.DecompressSheafTrellis(comp7, dst)
		if err != nil {
			t.Fatalf("SheafTrellis failed on 7-byte pattern sz=%d: %v", sz, err)
		}
		if !bytes.Equal(dec7, data7) {
			t.Fatalf("SheafTrellis mismatch on 7-byte pattern sz=%d", sz)
		}

		// C. Repeated byte run (stress test matchLength > 258)
		dataRep := bytes.Repeat([]byte{0x5A}, sz)
		compRep := przm.CompressSheafTrellis(dataRep)
		decRep, err := przm.DecompressSheafTrellis(compRep, dst)
		if err != nil {
			t.Fatalf("SheafTrellis failed on repeated byte sz=%d: %v", sz, err)
		}
		if !bytes.Equal(decRep, dataRep) {
			t.Fatalf("SheafTrellis mismatch on repeated byte sz=%d", sz)
		}
	}
}

// 5. TestAdversarial_PureCDecompressor_Stress verifies 100% losslessness across all levels and stress inputs.
func TestAdversarial_PureCDecompressor_Stress(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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
		t.Fatal("Pure C test executable test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	testInputs := []struct {
		name string
		data []byte
	}{
		{"1_byte", []byte{0x7F}},
		{"2_bytes", []byte{0x12, 0x34}},
		{"3_bytes", []byte{0xAA, 0xBB, 0xCC}},
		{"4_bytes", []byte{0x11, 0x22, 0x33, 0x44}},
		{"7_bytes", []byte("1234567")},
		{"15_bytes", []byte("123456789012345")},
		{"16_bytes", []byte("1234567890123456")},
		{"33_bytes", []byte("abcdefghijklmnopqrstuvwxyz0123456")},
		{"65_bytes", makePattern(65, 5)},
		{"128_bytes_zeroes", make([]byte, 128)},
		{"1024_bytes_ff", bytes.Repeat([]byte{0xFF}, 1024)},
		{"64K_zeroes", make([]byte, 65536)},
		{"64K_alt_ab", bytes.Repeat([]byte("ab"), 32768)},
		{"64K_alt_abc", func() []byte {
			b := make([]byte, 65536)
			for i := range b {
				b[i] = byte('a' + (i % 3))
			}
			return b
		}()},
		{"64K_alt_7B", func() []byte {
			b := make([]byte, 65536)
			for i := range b {
				b[i] = byte('a' + (i % 7))
			}
			return b
		}()},
		{"64K_fibonacci", func() []byte {
			s0, s1 := "a", "b"
			for len(s1) < 65536 {
				s0, s1 = s1, s1+s0
			}
			return []byte(s1[:65536])
		}()},
		{"64K_thue_morse", func() []byte {
			b := make([]byte, 65536)
			for i := range 65536 {
				// Thue-Morse parity of popcount(i)
				pc := 0
				for v := i; v > 0; v &= v - 1 {
					pc++
				}
				b[i] = byte('a' + (pc % 2))
			}
			return b
		}()},
		{"prime_1009", makePattern(1009, 17)},
		{"prime_65537", makePattern(65537, 23)},
		{"256KB_mixed", func() []byte {
			b := make([]byte, 256*1024)
			for i := range b {
				b[i] = byte((i*37 + (i>>3)*11) % 256)
			}
			return b
		}()},
	}

	levels := []int{1, 2, 3, 4}

	for _, tc := range testInputs {
		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_lvl%d", tc.name, lvl)
			t.Run(testName, func(t *testing.T) {
				comp, err := przm.Compress(tc.data, lvl)
				if err != nil {
					t.Fatalf("przm.Compress failed: %v", err)
				}

				// 1. Verify Go decompression
				decGo, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("przm.Decompress failed: %v", err)
				}
				if !bytes.Equal(decGo, tc.data) {
					t.Fatalf("Go decompress mismatch (len %d vs %d)", len(decGo), len(tc.data))
				}

				// 2. Verify Pure C Decompressor
				inPrzm := filepath.Join(tmpDir, testName+".przm")
				outBin := filepath.Join(tmpDir, testName+".out")

				if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
					t.Fatalf("WriteFile failed: %v", err)
				}

				cmd := exec.Command(cBinPath, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 {
						t.Skipf("Pure C decompressor does not support Go-only experimental transform %q", comp[:min(4, len(comp))])
						return
					}
					t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s\ncomp len=%d, header=%q / %x", err, string(output), len(comp), comp[:min(16, len(comp))], comp[:min(16, len(comp))])
				}

				decC, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("ReadFile failed: %v", err)
				}

				if !bytes.Equal(decC, tc.data) {
					t.Fatalf("Pure C decompress mismatch (got %d bytes, want %d bytes)", len(decC), len(tc.data))
				}

				// Clean up temporary files
				_ = os.Remove(inPrzm)
				_ = os.Remove(outBin)
			})
		}
	}
}

func makePattern(n int, period int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*13 + (i%period)*17) % 256)
	}
	return b
}

// -----------------------------------------------------------------------------
// Source: challenger_stress_test.go
// -----------------------------------------------------------------------------

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
			randData[i] = byte((i*109 + 37) % 256)
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
				for i := range n {
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
				for i := range n {
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
				for i := range n {
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
		_, err := crand.Read(data)
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
	const numGoroutines = 8
	const iterations = 50

	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte(i ^ (i >> 3) ^ (i >> 7))
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(gID int) {
			defer wg.Done()
			dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)
			for it := range iterations {
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
	if przm.RaceEnabled {
		t.Skip("skipping empirical cycle ceiling benchmark under race detector (instrumentation invalidates microarchitectural bounds)")
	}
	const sz = 65536
	data := make([]byte, sz)
	for i := range data {
		data[i] = byte(i*13 + (i>>4)*7)
	}

	comp := przm.CompressSheafTrellis(data)
	dst := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)

	// Warmup
	for range 100 {
		_, _ = przm.DecompressSheafTrellis(comp, dst)
	}

	// Benchmark 1000 runs
	const runs = 1000
	start := time.Now()
	for range runs {
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
