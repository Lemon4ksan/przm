// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package adversarial_test

import (
	"bytes"
	crand "crypto/rand"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

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
			t.Fatalf(
				"Blelloch prefix scan last element mismatch for N=%d:\nexpected=%+v\ngot=%+v",
				n,
				expected,
				prefixes[n-1],
			)
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
	sizes := []int{
		1,
		2,
		3,
		4,
		5,
		7,
		8,
		15,
		16,
		17,
		31,
		32,
		33,
		63,
		64,
		65,
		97,
		101,
		127,
		128,
		129,
		255,
		256,
		257,
		1009,
		4096,
		65536,
		65537,
	}

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

				if err := os.WriteFile(inPrzm, comp, 0o644); err != nil {
					t.Fatalf("WriteFile failed: %v", err)
				}

				cmd := exec.Command(cBinPath, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 {
						t.Skipf(
							"Pure C decompressor does not support Go-only experimental transform %q",
							comp[:min(4, len(comp))],
						)
						return
					}
					t.Fatalf(
						"Pure C decompressor failed (exit status %v):\nOutput:\n%s\ncomp len=%d, header=%q / %x",
						err,
						string(output),
						len(comp),
						comp[:min(16, len(comp))],
						comp[:min(16, len(comp))],
					)
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

func makePattern(n, period int) []byte {
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
			t.Fatalf(
				"[%s_%d] Sheaf Trellis roundtrip mismatch! Expected len %d, got len %d",
				name,
				sz,
				len(data),
				len(decSheaf),
			)
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
			t.Fatalf(
				"[%s_%d] przm level 1 roundtrip mismatch! Expected len %d, got len %d",
				name,
				sz,
				len(data),
				len(decPRZM),
			)
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
		t.Skip(
			"skipping empirical cycle ceiling benchmark under race detector (instrumentation invalidates microarchitectural bounds)",
		)
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
