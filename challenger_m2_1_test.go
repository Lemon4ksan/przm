// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"
)

// TestChallenger_M2_1_LargeOffsets verifies match finding at offsets exceeding 64K, 1M, up to 8M.
func TestChallenger_M2_1_LargeOffsets(t *testing.T) {
	distances := []int{65536, 131072, 1048570, 2097152, 4194304, 8000000}
	pattern := []byte("ADVERSARIAL_M2_1_LARGE_OFFSET_MATCH_TEST_STRING_!")

	for _, dist := range distances {
		if dist <= 1048570 {
			t.Run(fmt.Sprintf("1M_Dist_%d", dist), func(t *testing.T) {
				sp := PoolHensel1M.Get()
				defer PoolHensel1M.Put(sp)
				sp.Reset()

				size := dist + len(pattern) + 64
				buf := make([]byte, size)
				for i := range buf {
					buf[i] = byte((i*41 + 17) & 0xFF)
				}
				posRef := 16
				posTarget := posRef + dist
				copy(buf[posRef:], pattern)
				copy(buf[posTarget:], pattern)

				HenselInsert1M(sp, posRef, buf, size)
				d, l := HenselFindMatch1M(buf, posTarget, size, 32, 4, sp)
				if d != dist || l < len(pattern) {
					t.Fatalf("1M match failed for dist %d: got dist=%d, len=%d, want dist=%d, len>=%d", dist, d, l, dist, len(pattern))
				}
			})
		}

		t.Run(fmt.Sprintf("8M_Dist_%d", dist), func(t *testing.T) {
			sp := PoolHensel8M.Get()
			defer PoolHensel8M.Put(sp)
			sp.Reset()

			size := dist + len(pattern) + 64
			buf := make([]byte, size)
			for i := range buf {
				buf[i] = byte((i*31 + 7) & 0xFF)
			}
			posRef := 32
			posTarget := posRef + dist
			copy(buf[posRef:], pattern)
			copy(buf[posTarget:], pattern)

			HenselInsert8M(sp, posRef, buf, size)
			d, l := HenselFindMatch8M(buf, posTarget, size, 32, 4, sp)
			if d != dist || l < len(pattern) {
				t.Fatalf("8M match failed for dist %d: got dist=%d, len=%d, want dist=%d, len>=%d", dist, d, l, dist, len(pattern))
			}
		})
	}
}

// TestChallenger_M2_1_BoundaryDistances tests exact window boundaries:
// 1M: 1048575 (pass), 1048576 (fail), 1048577 (fail)
// 8M: 8388607 (pass), 8388608 (fail)
func TestChallenger_M2_1_BoundaryDistances(t *testing.T) {
	tag := []byte("BOUNDARY_TEST_TAG_12345678")

	// 1M Boundary Cases
	t.Run("1M_Boundaries", func(t *testing.T) {
		sp := PoolHensel1M.Get()
		defer PoolHensel1M.Put(sp)

		cases := []struct {
			dist     int
			wantFind bool
		}{
			{1048575, true},  // WindowSize1M - 1: inside window
			{1048576, false}, // WindowSize1M: exactly out of window
			{1048577, false}, // WindowSize1M + 1: out of window
		}

		for _, tc := range cases {
			sp.Reset()
			size := tc.dist + len(tag) + 128
			buf := make([]byte, size)
			posRef := 10
			posTarget := posRef + tc.dist
			copy(buf[posRef:], tag)
			copy(buf[posTarget:], tag)

			HenselInsert1M(sp, posRef, buf, size)
			d, l := HenselFindMatch1M(buf, posTarget, size, 32, 4, sp)
			if tc.wantFind {
				if d != tc.dist || l < len(tag) {
					t.Fatalf("Expected match at dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			} else {
				if d != 0 || l != 0 {
					t.Fatalf("Expected no match at boundary dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			}
		}
	})

	// 8M Boundary Cases
	t.Run("8M_Boundaries", func(t *testing.T) {
		sp := PoolHensel8M.Get()
		defer PoolHensel8M.Put(sp)

		cases := []struct {
			dist     int
			wantFind bool
		}{
			{8388607, true},  // WindowSize8M - 1: inside window
			{8388608, false}, // WindowSize8M: exactly out of window
		}

		for _, tc := range cases {
			sp.Reset()
			size := tc.dist + len(tag) + 128
			buf := make([]byte, size)
			posRef := 10
			posTarget := posRef + tc.dist
			copy(buf[posRef:], tag)
			copy(buf[posTarget:], tag)

			HenselInsert8M(sp, posRef, buf, size)
			d, l := HenselFindMatch8M(buf, posTarget, size, 32, 4, sp)
			if tc.wantFind {
				if d != tc.dist || l < len(tag) {
					t.Fatalf("Expected match at dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			} else {
				if d != 0 || l != 0 {
					t.Fatalf("Expected no match at boundary dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			}
		}
	})
}

// TestChallenger_M2_1_RingWrapping tests that older entries are cleanly overwritten
// when sliding window wraps around, without producing stale matches or crashes.
func TestChallenger_M2_1_RingWrapping(t *testing.T) {
	sp := PoolHensel1M.Get()
	defer PoolHensel1M.Put(sp)
	sp.Reset()

	const wrapCount = WindowSize1M + 1000
	buf := make([]byte, wrapCount+64)
	pattern := []byte("RING_WRAP_PATTERN_12345678")

	// Plant early match at pos 100
	copy(buf[100:], pattern)
	HenselInsert1M(sp, 100, buf, len(buf))

	// Plant late match at pos 100 + WindowSize1M + 200 (distance > 1M from early match)
	latePos := 100 + WindowSize1M + 200
	copy(buf[latePos:], pattern)

	// Overwrite intermediate entries in the ring buffer
	for p := 200; p < latePos; p += 32 {
		HenselInsert1M(sp, p, buf, len(buf))
	}

	// Finding match at latePos should NOT return the wrapped entry from pos 100
	d, l := HenselFindMatch1M(buf, latePos, len(buf), 64, 4, sp)
	if d != 0 || l != 0 {
		t.Fatalf("Ring wrapping failed: expired candidate at pos 100 was matched: dist=%d, len=%d", d, l)
	}

	// Now plant a valid match within window at latePos - 500
	validPos := latePos - 500
	copy(buf[validPos:], pattern)
	HenselInsert1M(sp, validPos, buf, len(buf))

	d, l = HenselFindMatch1M(buf, latePos, len(buf), 64, 4, sp)
	if d != 500 || l < len(pattern) {
		t.Fatalf("Ring wrapping failed to match valid entry within window: got dist=%d, len=%d", d, l)
	}
}

// TestChallenger_M2_1_TheoremHL2_FP32Pruning tests hash collisions and fp32 pruning behavior.
func TestChallenger_M2_1_TheoremHL2_FP32Pruning(t *testing.T) {
	sp := PoolHensel1M.Get()
	defer PoolHensel1M.Put(sp)
	sp.Reset()

	buf := make([]byte, 8192)

	// Case 1: Same 4-byte prefix, different fp32 bytes (Theorem HL-2 pruning)
	// Candidate 1: prefix "WXYZ" + "AAAA" (length 8)
	// Candidate 2 (closer): prefix "WXYZ" + "BBBB" (length 8)
	// Target at pos: "WXYZ" + "AAAA" + "EXTENDED_DATA"
	posCand1 := 100
	posCand2 := 200
	posTarget := 500

	copy(buf[posCand1:], []byte("WXYZAAAA12345678"))
	copy(buf[posCand2:], []byte("WXYZBBBB99999999"))
	copy(buf[posTarget:], []byte("WXYZAAAA12345678"))

	HenselInsert1M(sp, posCand1, buf, len(buf))
	HenselInsert1M(sp, posCand2, buf, len(buf))

	// HenselFindMatch searches cand2 first (head of chain), then cand1.
	// cand2 has diff in fp32 ("BBBB" vs "AAAA"), pruning or rejecting it,
	// and must proceed to cand1 which is a full match of length 16.
	dist, matchLen := HenselFindMatch1M(buf, posTarget, len(buf), 16, 4, sp)
	wantDist := posTarget - posCand1
	if dist != wantDist || matchLen < 16 {
		t.Fatalf("Theorem HL-2 pruning traversal failed: got dist=%d, len=%d, want dist=%d, len>=16", dist, matchLen, wantDist)
	}

	// Case 2: Multiplicative hash collision with DIFFERENT 4-byte prefixes.
	// Search through 32-bit space for two distinct 4-byte words with exact same Hensel hash in 1M table.
	sp.Reset()
	var w1, w2 uint32
	foundCol := false
	seen := make(map[int]uint32, 5000)
	cRng := rand.New(rand.NewSource(999))
	for iter := 0; iter < 100000; iter++ {
		candidate := cRng.Uint32()
		h := computeHenselHash(candidate, HashBits1M, HashMask1M)
		if prevW, exists := seen[h]; exists && prevW != candidate {
			w1 = prevW
			w2 = candidate
			foundCol = true
			break
		}
		seen[h] = candidate
	}
	if !foundCol {
		t.Fatalf("Could not synthesize hash collision for HashBits1M")
	}

	// Position 1 has w1 + valid match payload
	// Position 2 has w2 (hash collision, but false positive 4-byte prefix)
	// Target has w1 + valid match payload
	pos1 := 1000
	posCol := 1200
	posTgt := 1500

	binary.LittleEndian.PutUint32(buf[pos1:], w1)
	copy(buf[pos1+4:], []byte("GOLDEN_PAYLOAD_TEST"))

	binary.LittleEndian.PutUint32(buf[posCol:], w2)
	copy(buf[posCol+4:], []byte("DIFFERENT_JUNK_DATA"))

	binary.LittleEndian.PutUint32(buf[posTgt:], w1)
	copy(buf[posTgt+4:], []byte("GOLDEN_PAYLOAD_TEST"))

	HenselInsert1M(sp, pos1, buf, len(buf))
	HenselInsert1M(sp, posCol, buf, len(buf))

	d, l := HenselFindMatch1M(buf, posTgt, len(buf), 16, 4, sp)
	expectedDist := posTgt - pos1
	if d != expectedDist || l < 20 {
		t.Fatalf("False positive hash collision broke chain traversal: got dist=%d, len=%d, want dist=%d", d, l, expectedDist)
	}
}

// TestChallenger_M2_1_OracleEquivalence verifies Hensel match finders against a golden brute-force oracle.
func TestChallenger_M2_1_OracleEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	dataSize := 65536
	data := make([]byte, dataSize)
	// Fill with low-entropy alphabet so matches exist naturally
	for i := range data {
		data[i] = byte('A' + rng.Intn(8))
	}

	sp1M := PoolHensel1M.Get()
	defer PoolHensel1M.Put(sp1M)
	sp1M.Reset()

	// Brute-force match finder within window and search depth
	oracleFind := func(src []byte, pos, windowSize, minLen int) (bestDist, bestLen int) {
		bestLen = minLen - 1
		bestDist = 0
		start := max(0, pos-windowSize)
		for cand := pos - 1; cand >= start; cand-- {
			l := 0
			maxL := min(len(src)-pos, 258)
			for l < maxL && src[cand+l] == src[pos+l] {
				l++
			}
			if l > bestLen {
				bestLen = l
				bestDist = pos - cand
				if bestLen >= 258 {
					break
				}
			}
		}
		if bestLen < minLen {
			return 0, 0
		}
		return bestDist, bestLen
	}

	sp8M := PoolHensel8M.Get()
	defer PoolHensel8M.Put(sp8M)
	sp8M.Reset()

	// Populate scratchpads and spot-check across multiple positions
	for i := 0; i < dataSize-260; i++ {
		HenselInsert1M(sp1M, i, data, dataSize)
		HenselInsert8M(sp8M, i, data, dataSize)

		if i > 256 && i%2048 == 0 {
			_, oLen := oracleFind(data, i, WindowSize1M, 4)
			hDist1M, hLen1M := HenselFindMatch1M(data, i, dataSize, 256, 4, sp1M)
			hDist8M, hLen8M := HenselFindMatch8M(data, i, dataSize, 256, 4, sp8M)

			if oLen >= 4 {
				if hLen1M < 4 {
					t.Fatalf("Oracle found match of len %d at pos %d, but Hensel1M returned len 0", oLen, i)
				}
				if hLen8M < 4 {
					t.Fatalf("Oracle found match of len %d at pos %d, but Hensel8M returned len 0", oLen, i)
				}
				if !bytes.Equal(data[i:i+hLen1M], data[i-hDist1M:i-hDist1M+hLen1M]) {
					t.Fatalf("Hensel1M returned invalid match bytes at pos %d: dist %d, len %d", i, hDist1M, hLen1M)
				}
				if !bytes.Equal(data[i:i+hLen8M], data[i-hDist8M:i-hDist8M+hLen8M]) {
					t.Fatalf("Hensel8M returned invalid match bytes at pos %d: dist %d, len %d", i, hDist8M, hLen8M)
				}
			}
		}
	}
}

// TestChallenger_M2_1_ZeroAlloc_DecompressTo strictly verifies testing.AllocsPerRun == 0.0
// for both 1M and 8M frames.
func TestChallenger_M2_1_ZeroAlloc_DecompressTo(t *testing.T) {
	sizes := []struct {
		name string
		size int
		wLog int
	}{
		{"1M_Payload", 1024 * 1024, WindowLog1M},
		{"8M_Payload", 8 * 1024 * 1024, WindowLog8M},
	}

	for _, s := range sizes {
		t.Run(s.name, func(t *testing.T) {
			raw := make([]byte, s.size)
			for i := range raw {
				raw[i] = byte((i*67 + 3) & 0xFF)
			}
			// Add repeated sequences to form matches
			pattern := []byte("ZERO_ALLOC_TEST_REPEATED_BLOCK_FOR_DECOMPRESS_TO_INVARIANT_")
			for i := 0; i < s.size-len(pattern); i += 1024 {
				copy(raw[i:], pattern)
			}

			compressed, err := CompressWithWindow(raw, 1, s.wLog)
			if err != nil {
				t.Fatalf("Compression failed for %s: %v", s.name, err)
			}

			dst := make([]byte, s.size+64)

			// Warm-up run and verify roundtrip bit-exactness
			out, err := DecompressTo(compressed, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed for %s: %v", s.name, err)
			}
			if !bytes.Equal(out, raw) {
				t.Fatalf("DecompressTo roundtrip mismatch for %s: got %d bytes, want %d bytes", s.name, len(out), len(raw))
			}

			// Strict 0-allocation benchmark verification
			allocs := testing.AllocsPerRun(10, func() {
				_, err := DecompressTo(compressed, dst)
				if err != nil {
					t.Fatalf("DecompressTo failed during AllocsPerRun: %v", err)
				}
			})

			if allocs != 0.0 {
				t.Fatalf("Zero-alloc violation for %s: expected 0.0 allocs/run, got %f", s.name, allocs)
			}
		})
	}
}
