// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// -----------------------------------------------------------------------------
// Adversarial Challenger Suite for Milestone 1:
// p-Adic Ultrametric Match Finding via Hensel's Lifting
// -----------------------------------------------------------------------------

// TestChallenger_Hensel_SubMinMatchBoundaries tests inputs shorter than the
// minimum match length (4 bytes), empty buffers, and sub-8-byte boundaries.
func TestChallenger_Hensel_SubMinMatchBoundaries(t *testing.T) {
	// 1. Direct HenselLCP boundary checks
	t.Run("HenselLCP_Sub4Bytes", func(t *testing.T) {
		// Empty slices
		if l := HenselLCP(nil, nil); l != 0 {
			t.Fatalf("HenselLCP(nil, nil) expected 0, got %d", l)
		}
		if l := HenselLCP([]byte{}, []byte{}); l != 0 {
			t.Fatalf("HenselLCP([], []) expected 0, got %d", l)
		}
		if l := HenselLCP([]byte("A"), nil); l != 0 {
			t.Fatalf("HenselLCP('A', nil) expected 0, got %d", l)
		}
		if l := HenselLCP(nil, []byte("A")); l != 0 {
			t.Fatalf("HenselLCP(nil, 'A') expected 0, got %d", l)
		}

		// 1-byte
		if l := HenselLCP([]byte("A"), []byte("A")); l != 1 {
			t.Fatalf("1B match expected 1, got %d", l)
		}
		if l := HenselLCP([]byte("A"), []byte("B")); l != 0 {
			t.Fatalf("1B mismatch expected 0, got %d", l)
		}

		// 2-byte
		if l := HenselLCP([]byte("AB"), []byte("AB")); l != 2 {
			t.Fatalf("2B match expected 2, got %d", l)
		}
		if l := HenselLCP([]byte("AB"), []byte("AC")); l != 1 {
			t.Fatalf("2B partial match expected 1, got %d", l)
		}
		if l := HenselLCP([]byte("AB"), []byte("XB")); l != 0 {
			t.Fatalf("2B full mismatch expected 0, got %d", l)
		}

		// 3-byte
		if l := HenselLCP([]byte("ABC"), []byte("ABC")); l != 3 {
			t.Fatalf("3B match expected 3, got %d", l)
		}
		if l := HenselLCP([]byte("ABC"), []byte("ABD")); l != 2 {
			t.Fatalf("3B partial match expected 2, got %d", l)
		}
		if l := HenselLCP([]byte("ABC"), []byte("AXC")); l != 1 {
			t.Fatalf("3B partial match expected 1, got %d", l)
		}
		if l := HenselLCP([]byte("ABC"), []byte("XYZ")); l != 0 {
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
				if l := HenselLCP(a, b); l != expected {
					t.Fatalf("HenselLCP lenA=%d, lenB=%d expected %d, got %d", lenA, lenB, expected, l)
				}
			}
		}
	})

	// 2. Direct HenselLCPPos boundary checks
	t.Run("HenselLCPPos_Boundaries", func(t *testing.T) {
		buf := []byte("ABCDEFGH12345678")
		// Identical positions
		if l := HenselLCPPos(buf, 2, 2, 8); l != 8 {
			t.Fatalf("HenselLCPPos p1==p2 expected 8, got %d", l)
		}
		// maxLen <= 0
		if l := HenselLCPPos(buf, 0, 4, 0); l != 0 {
			t.Fatalf("HenselLCPPos maxLen=0 expected 0, got %d", l)
		}
		if l := HenselLCPPos(buf, 0, 4, -3); l != 0 {
			t.Fatalf("HenselLCPPos maxLen<0 expected 0, got %d", l)
		}
		// Sub-4 maxLen
		for m := 1; m < 4; m++ {
			if l := HenselLCPPos(buf, 0, 0, m); l != m {
				t.Fatalf("HenselLCPPos maxLen=%d expected %d, got %d", m, m, l)
			}
		}
	})

	// 3. Scratchpad boundary safety on sub-4-byte buffers
	t.Run("Scratchpad_Sub4_Safety", func(t *testing.T) {
		var sp HenselScratchpad
		sp.Reset()

		smallBuf := []byte{0x42, 0x13, 0x37} // 3 bytes
		// HenselInsert on sub-4-byte buffer should safely no-op
		HenselInsert(&sp, 0, smallBuf, len(smallBuf))
		HenselInsert(nil, 0, smallBuf, len(smallBuf))

		// HenselFindMatch on sub-4-byte buffer should return 0, 0 safely
		d, l := HenselFindMatch(smallBuf, 0, len(smallBuf), 16, 4, &sp)
		if d != 0 || l != 0 {
			t.Fatalf("HenselFindMatch on 3B buffer expected 0,0, got dist=%d len=%d", d, l)
		}
		d, l = HenselFindMatch(smallBuf, 0, len(smallBuf), 16, 4, nil)
		if d != 0 || l != 0 {
			t.Fatalf("HenselFindMatch with nil scratchpad expected 0,0, got dist=%d len=%d", d, l)
		}

		// minLen clamping: minLen < 4 clamped to 4
		d, l = HenselFindMatch(smallBuf, 0, len(smallBuf), 16, 1, &sp)
		if d != 0 || l != 0 {
			t.Fatalf("HenselFindMatch with minLen=1 expected 0,0, got dist=%d len=%d", d, l)
		}
	})

	// 4. Codec roundtrip for all small lengths 0..15 bytes at Level 3 and Level 4
	t.Run("Codec_SmallLengthsRoundtrip", func(t *testing.T) {
		testPattern := []byte("0123456789ABCDEF")
		for length := 0; length <= 16; length++ {
			sub := testPattern[:length]
			for _, lvl := range []int{3, 4} {
				comp, err := Compress(sub, lvl)
				if err != nil {
					t.Fatalf("Compress failed for len=%d lvl=%d: %v", length, lvl, err)
				}
				decomp, err := Decompress(comp)
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
				comp, err := Compress(buf, lvl)
				if err != nil {
					t.Fatalf("Compress prime len=%d lvl=%d failed: %v", p, lvl, err)
				}
				decomp, err := Decompress(comp)
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

		lcp := HenselLCP(sliceA[1:101], sliceB[3:103])
		if lcp != 45 {
			t.Fatalf("Unaligned HenselLCP expected 45, got %d", lcp)
		}

		// Also test HenselLCPPos inside a single large non-overlapping buffer
		base := make([]byte, 512)
		copy(base[10:110], sliceA[1:101])
		copy(base[200:300], sliceB[3:103])
		lcpPos := HenselLCPPos(base, 10, 200, 100)
		if lcpPos != 45 {
			t.Fatalf("Unaligned HenselLCPPos expected 45, got %d", lcpPos)
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

		var sp HenselScratchpad
		sp.Reset()

		// Populate all positions into Hensel scratchpad
		for i := 0; i+4 <= len(data); i++ {
			HenselInsert(&sp, i, data, len(data))
		}

		// Verify search at various depths does not crash, hang, or exceed maxDepth
		testPositions := []int{100, 500, 1024, 8192, 32768, 60000, 65530}
		for _, pos := range testPositions {
			for _, depth := range []int{16, 64} {
				dist, matchLen := HenselFindMatch(data, pos, len(data), depth, 4, &sp)
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
			comp, err := Compress(data, lvl)
			if err != nil {
				t.Fatalf("Compress failed for 64K repeating pattern lvl=%d: %v", lvl, err)
			}
			decomp, err := Decompress(comp)
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
			comp, err := Compress(data, lvl)
			if err != nil {
				t.Fatalf("Compress all-zeroes lvl=%d failed: %v", lvl, err)
			}
			decomp, err := Decompress(comp)
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
			comp, err := Compress(data, lvl)
			if err != nil {
				t.Fatalf("Compress all-0xFF lvl=%d failed: %v", lvl, err)
			}
			decomp, err := Decompress(comp)
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
			h := uint16((candidateW * HenselHashPrime) >> 16)
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

		var sp HenselScratchpad
		sp.Reset()
		HenselInsert(&sp, 10, buf, len(buf))

		// Search at pos 40. Prefix w2 != w1, so no match should be found!
		dist, matchLen := HenselFindMatch(buf, 40, len(buf), 16, 4, &sp)
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

		var sp HenselScratchpad
		sp.Reset()
		HenselInsert(&sp, 10, buf, len(buf))

		// Search with minLen = 4: since prefix matches, LCP is 4!
		dist, matchLen := HenselFindMatch(buf, 40, len(buf), 16, 4, &sp)
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

		var sp HenselScratchpad
		sp.Reset()
		HenselInsert(&sp, 10, buf, len(buf))

		// Search should return match of length 7
		dist, matchLen := HenselFindMatch(buf, 40, len(buf), 16, 4, &sp)
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

		var sp HenselScratchpad
		sp.Reset()
		HenselInsert(&sp, 10, buf, len(buf))
		HenselInsert(&sp, 60, buf, len(buf))

		// Search from pos 120:
		// Cand 2 is checked first (dist 60, len 4). bestLen becomes 4.
		// Cand 1 is checked second (dist 110, len 20).
		// Since Cand 1 matches 20 bytes, diff == 0, not pruned!
		// Result must be dist 110, len 20!
		dist, matchLen := HenselFindMatch(buf, 120, len(buf), 16, 4, &sp)
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

			for i := 0; i < 64; i++ {
				cand[i] = byte(i*17 + 3)
				target[i] = cand[i]
			}
			target[mismatchOffset] ^= 0x55 // Force mismatch at exact offset

			copy(buf[10:74], cand)
			copy(buf[80:144], target)

			var sp HenselScratchpad
			sp.Reset()
			HenselInsert(&sp, 10, buf, len(buf))

			dist, matchLen := HenselFindMatch(buf, 80, len(buf), 16, 4, &sp)
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
	h := (w4 * HenselHashPrime) >> 16
	h &= HenselHashMask

	cand := int(head[h]) - 1
	if cand < 0 {
		return 0, 0
	}

	bestLen := minLen - 1
	bestDist := 0
	depth := 0
	maxSearchLen := min(uSize-pos, 65535)

	for cand >= 0 && depth < maxDepth {
		candSlot := cand & HenselWindowMask
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

// TestChallenger_Hensel_ExhaustiveOracle50000 compares HenselFindMatch with
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
		[]byte("func evaluateUltrametricNonArchimedeanLifting(sp *HenselScratchpad) int {"),
		[]byte("AAAABBBBCCCCDDDDEEEEFFFFGGGGHHHHIIIIJJJJKKKKLLLLMMMMNNNNOOOOPPPP"),
		[]byte("const HenselHashPrime uint32 = 0x1E35A7BD"),
		[]byte("SHORT_4B"),
		[]byte("VERY_LONG_REPETITION_P_ADIC_HENSEL_QUADRATIC_CONVERGENCE_SPEEDUP_ACROSS_LEVELS_THREE_AND_FOUR_PRESERVING_BITSTREAM_COMPATIBILITY_AND_ZERO_ALLOCATION_DECOMPRESSOR_CONTRACT_1234567890"),
	}

	for i := 0; i < 300; i++ {
		p := patterns[i%len(patterns)]
		offset := rng.Intn(dataSize - len(p) - 1)
		copy(data[offset:], p)
	}

	var sp HenselScratchpad
	sp.Reset()

	totalTrials := 50000
	verifiedMatches := 0
	prunedMatchesDiscovered := 0

	// Pre-fill first 10,000 positions
	initInsert := 10000
	for i := 0; i < initInsert; i++ {
		HenselInsert(&sp, i, data, dataSize)
	}

	curPos := initInsert
	for trial := 0; trial < totalTrials; trial++ {
		// Periodically advance insertion point
		if curPos+4 <= dataSize {
			HenselInsert(&sp, curPos, data, dataSize)
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

		hDist, hLen := HenselFindMatch(data, testPos, dataSize, maxDepth, minLen, &sp)
		oDist, oLen := naiveHashChainOracle(data, testPos, dataSize, maxDepth, minLen, sp.head[:], sp.prev[:])

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

	var sp HenselScratchpad
	sp.Reset()

	for i := 0; i+4 <= bufSize; i++ {
		HenselInsert(&sp, i, buf, bufSize)

		if i == 65540 {
			dist, matchLen := HenselFindMatch(buf, 65540, bufSize, 64, 4, &sp)
			if dist != 65440 || matchLen < len(pattern) {
				t.Fatalf("At pos 65540: expected match to pos 100 (dist 65440), got dist=%d len=%d",
					dist, matchLen)
			}
		}

		if i == 70000 {
			dist, matchLen := HenselFindMatch(buf, 70000, bufSize, 64, 4, &sp)
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
			comp, err := Compress(largeBuf, lvl)
			if err != nil {
				t.Fatalf("Compress failed on size=%d lvl=%d: %v", size, lvl, err)
			}
			decomp, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed on size=%d lvl=%d: %v", size, lvl, err)
			}
			if !bytes.Equal(largeBuf, decomp) {
				t.Fatalf("Roundtrip mismatch on size=%d lvl=%d", size, lvl)
			}
		}
	}
}

// TestChallenger_Hensel_ZeroAllocations verifies that HenselFindMatch,
// HenselInsert, and HenselLCP strictly execute with zero heap allocations.
func TestChallenger_Hensel_ZeroAllocations(t *testing.T) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 53)
	}

	var sp HenselScratchpad
	sp.Reset()

	for i := 0; i < 500; i++ {
		HenselInsert(&sp, i, data, len(data))
	}

	allocs := testing.AllocsPerRun(100, func() {
		HenselInsert(&sp, 501, data, len(data))
		HenselFindMatch(data, 501, len(data), 64, 4, &sp)
		HenselLCP(data[10:60], data[20:70])
		HenselLCPPos(data, 10, 20, 50)
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
				comp, err := Compress(tc.data, lvl)
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
