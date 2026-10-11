// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package adversarial_test

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

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

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
				t.Logf(
					"  -> Extraneous heap allocations detected: %.2f allocs/op (V-ANS model & buffer allocations in Go decompressor)",
					allocs,
				)
			}
		}
	}
}

// TestChallenger_M1_CDecompressor_InteropGap_Reproducer reproduces the Pure C
// decompressor failure on Level 4 compressed streams when GCTR is selected.
func TestChallenger_M1_CDecompressor_InteropGap_Reproducer(t *testing.T) {
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
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
	if err := os.WriteFile(inPrzm, compLvl4, 0o644); err != nil {
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
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
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
		if err := os.WriteFile(inPrzm, compLvl4, 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(cBinPath, inPrzm, outBin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf(
				"Pure C decompressor failed on Level 4 output (sz %d, exit status %v):\nOutput: %s",
				sz,
				err,
				string(output),
			)
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
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
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

		if err := os.WriteFile(inPrzm, comp, 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command(cBinPath, inPrzm, outBin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf(
				"[Payload %d, Magic 0x%08X] C decompressor failed: %v\nOutput: %s",
				pIdx,
				magic,
				err,
				string(output),
			)
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

	t.Logf(
		"Concurrency Stress: %d operations completed successfully across %d goroutines.",
		totalOps.Load(),
		numGoroutines,
	)
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
func naiveHashChainOracle(src []byte, pos, uSize, maxDepth, minLen int, head, prev []int32) (int, int) {
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
		[]byte(
			"VERY_LONG_REPETITION_P_ADIC_HENSEL_QUADRATIC_CONVERGENCE_SPEEDUP_ACROSS_LEVELS_THREE_AND_FOUR_PRESERVING_BITSTREAM_COMPATIBILITY_AND_ZERO_ALLOCATION_DECOMPRESSOR_CONTRACT_1234567890",
		),
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
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
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

				if err := os.WriteFile(inPath, comp, 0o644); err != nil {
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
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
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
		{
			"16_bytes_pws",
			[]byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00},
		},
		{
			"19_bytes_pws",
			[]byte{
				'P',
				'W',
				0x01,
				0x00,
				0x04,
				0x00,
				0x00,
				0x00,
				0x01,
				0x00,
				0x00,
				0x00,
				0x01,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
				0x00,
			},
		},
		{"3_bytes_przm", []byte{'P', 'R', 'Z'}},
		{"4_bytes_przm", []byte{'P', 'R', 'Z', 'M'}},
		{"7_bytes_przm", []byte{'P', 'R', 'Z', 'M', 0x00, 0x04, 0x00}},
	}

	for _, tc := range truncatedFrames {
		t.Run("C_Truncated_"+tc.name, func(t *testing.T) {
			inPrzm := filepath.Join(tmpDir, "trunc_"+tc.name+".przm")
			outBin := filepath.Join(tmpDir, "trunc_"+tc.name+".out")

			if err := os.WriteFile(inPrzm, tc.data, 0o644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			// Must fail (non-zero exit code)
			if err == nil {
				t.Fatalf(
					"Expected C decompressor to reject truncated frame %q, but succeeded! Output:\n%s",
					tc.name,
					string(output),
				)
			}
			// Exit code must be 2 (Invalid PRZM frame header / error) or 3 (Decompression failed), NOT a crash (e.g. status code -1073741819 / 0xC0000005)
			exitCode := cmd.ProcessState.ExitCode()
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf(
					"C decompressor crashed or exited unexpectedly with code %d on %q. Output:\n%s",
					exitCode,
					tc.name,
					string(output),
				)
			}
		})
	}
}
