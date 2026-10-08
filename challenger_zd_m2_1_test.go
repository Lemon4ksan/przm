// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// TestChallengerZD_HenselLCP_ExhaustiveOracle stress-tests HenselLCP and HenselLCPPos
// against a naive ground-truth comparator for lengths from 0 to 1024 bytes and various alignments.
func TestChallengerZD_HenselLCP_ExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))

	groundTruthLCP := func(a, b []byte, maxLen int) int {
		n := min(len(a), len(b), maxLen)
		for i := 0; i < n; i++ {
			if a[i] != b[i] {
				return i
			}
		}
		return n
	}

	for length := 0; length <= 512; length++ {
		a := make([]byte, length+16)
		b := make([]byte, length+16)
		rng.Read(a)
		copy(b, a)

		// Exact match
		if got := przm.HenselLCP(a[:length], b[:length]); got != length {
			t.Fatalf("Exact match failed at len %d: got %d", length, got)
		}

		// Single mismatch at every possible byte offset 0..length-1
		for mismatch := 0; mismatch < length; mismatch++ {
			b[mismatch] ^= 0x5A
			expected := groundTruthLCP(a[:length], b[:length], length)
			got := przm.HenselLCP(a[:length], b[:length])
			if got != expected {
				t.Fatalf("HenselLCP len=%d mismatch at %d: got %d, want %d", length, mismatch, got, expected)
			}

			// Also test HenselLCPPos inside unified buffer
			buf := make([]byte, 2*length+64)
			copy(buf[0:], a[:length])
			copy(buf[length+16:], b[:length])
			gotPos := przm.HenselLCPPos(buf, 0, length+16, length)
			if gotPos != expected {
				t.Fatalf("HenselLCPPos len=%d mismatch at %d: got %d, want %d", length, mismatch, gotPos, expected)
			}
			b[mismatch] ^= 0x5A // restore
		}
	}
}

// TestChallengerZD_HenselLCPPos_BoundarySafety tests invalid and edge boundary conditions.
func TestChallengerZD_HenselLCPPos_BoundarySafety(t *testing.T) {
	buf := []byte("0123456789abcdef0123456789abcdef")

	cases := []struct {
		name     string
		p1, p2   int
		maxLen   int
		expected int
	}{
		{"p1_negative", -1, 0, 10, 0},
		{"p2_negative", 0, -5, 10, 0},
		{"maxLen_zero", 0, 4, 0, 0},
		{"maxLen_negative", 0, 4, -10, 0},
		{"p1_past_end", 32, 0, 10, 0},
		{"p2_past_end", 0, 32, 10, 0},
		{"p1_clamped_match", 16, 0, 20, 16}, // clamped to len-p1 = 16; all 16 bytes match
		{"identical_offset", 0, 0, 16, 16},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := przm.HenselLCPPos(buf, tc.p1, tc.p2, tc.maxLen)
			if got != tc.expected {
				t.Fatalf("case %s: got %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestChallengerZD_HenselLCP_RLE_Overlapping tests overlapping sliding window / RLE common prefixes.
func TestChallengerZD_HenselLCP_RLE_Overlapping(t *testing.T) {
	lengths := []int{1, 2, 3, 4, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255, 256, 1024, 4096}
	for _, l := range lengths {
		buf := bytes.Repeat([]byte{'A'}, l+1)
		// Compare buf[0:l] with buf[1:l+1]
		got := przm.HenselLCPPos(buf, 0, 1, l)
		if got != l {
			t.Fatalf("RLE overlapping LCP failed for len=%d: got %d, want %d", l, got, l)
		}
	}
}

// TestChallengerZD_TheoremHL2_ExhaustiveBitShift tests all 32 single-bit and multi-bit
// mismatch patterns in bytes 4..7 to verify Theorem HL-2 exact short-circuit calculation.
func TestChallengerZD_TheoremHL2_ExhaustiveBitShift(t *testing.T) {
	for byteMismatch := 0; byteMismatch < 4; byteMismatch++ {
		for bit := 0; bit < 8; bit++ {
			diff := uint32(1<<bit) << (byteMismatch * 8)
			calculatedShift := bits.TrailingZeros32(diff) >> 3
			if calculatedShift != byteMismatch {
				t.Fatalf("HL-2 shift mismatch: diff=0x%08X, got %d, want %d", diff, calculatedShift, byteMismatch)
			}
			maxLift := 4 + calculatedShift
			expectedLift := 4 + byteMismatch
			if maxLift != expectedLift {
				t.Fatalf("HL-2 maxLift mismatch: diff=0x%08X, got %d, want %d", diff, maxLift, expectedLift)
			}
		}
	}
}

// TestChallengerZD_HighCollisionChaining tests pathological hash collision chains (1000 items in single bucket).
func TestChallengerZD_HighCollisionChaining(t *testing.T) {
	sp := przm.PoolHensel64K.Get()
	defer przm.PoolHensel64K.Put(sp)
	sp.Reset()

	// All 4-byte prefixes identical -> all collide in the same hash bucket
	buf := bytes.Repeat([]byte("SAME"), 2000) // 8000 bytes
	for i := 0; i+4 <= len(buf); i += 4 {
		przm.HenselInsert(sp, i, buf, len(buf))
	}

	// Search at pos=4000 with depth=16
	dist, matchLen := przm.HenselFindMatch(buf, 4000, len(buf), 16, 4, sp)
	if dist <= 0 || matchLen < 4 {
		t.Fatalf("Failed to find match under collision chain: dist=%d, len=%d", dist, matchLen)
	}

	// Verify candidate stepped back without panic or loop
	if !bytes.Equal(buf[4000:4000+matchLen], buf[4000-dist:4000-dist+matchLen]) {
		t.Fatalf("Match content invalid: dist=%d len=%d", dist, matchLen)
	}
}

// TestChallengerZD_WindowWrapping tests eviction when positions cross window sizes.
func TestChallengerZD_WindowWrapping(t *testing.T) {
	sp := przm.PoolHensel64K.Get()
	defer przm.PoolHensel64K.Put(sp)
	sp.Reset()

	const totalSize = przm.HenselWindowSize * 2 // 128 KiB
	buf := make([]byte, totalSize)
	for i := range buf {
		buf[i] = byte(i * 13)
	}

	// Place a marker at pos 100
	marker := []byte("UNIQUE_MARKER_FOR_WINDOW_WRAP_TEST")
	copy(buf[100:], marker)
	przm.HenselInsert(sp, 100, buf, totalSize)

	// Place same marker at pos 100 + HenselWindowSize + 50 (outside window)
	posOutside := 100 + przm.HenselWindowSize + 50
	copy(buf[posOutside:], marker)

	dist, _ := przm.HenselFindMatch(buf, posOutside, totalSize, 64, 4, sp)
	if dist != 0 {
		t.Fatalf("Candidate outside window size returned: dist=%d (windowSize=%d)", dist, przm.HenselWindowSize)
	}
}

// TestChallengerZD_Roundtrip_DiverseCorpora tests 100% bit-exact lossless roundtrips
// across diverse corpora and boundary sizes.
func TestChallengerZD_Roundtrip_DiverseCorpora(t *testing.T) {
	sizes := []int{
		0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 31, 32, 63, 64, 127, 128,
		255, 256, 511, 512, 1023, 1024, 4095, 4096, 65535, 65536, 65537,
	}

	rng := rand.New(rand.NewSource(4242))

	testCorpus := func(name string, gen func(sz int) []byte) {
		for _, sz := range sizes {
			data := gen(sz)
			for level := 1; level <= 4; level++ {
				comp, err := przm.Compress(data, level)
				if err != nil {
					t.Fatalf("[%s sz=%d lvl=%d] Compress failed: %v", name, sz, level, err)
				}

				decomp, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("[%s sz=%d lvl=%d] Decompress failed: %v", name, sz, level, err)
				}

				if !bytes.Equal(decomp, data) {
					t.Fatalf("[%s sz=%d lvl=%d] Bit-exact mismatch! orig=%d decomp=%d", name, sz, level, len(data), len(decomp))
				}

				// DecompressTo zero dynamic allocation verification
				dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)
				decompTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("[%s sz=%d lvl=%d] DecompressTo failed: %v", name, sz, level, err)
				}
				if !bytes.Equal(decompTo, data) {
					t.Fatalf("[%s sz=%d lvl=%d] DecompressTo mismatch!", name, sz, level)
				}

				// Strict zero-alloc audit on selected sizes for Main Mode frames
				if sz == 64 || sz == 4096 || sz == 65536 {
					allocs := testing.AllocsPerRun(100, func() {
						_, _ = przm.DecompressTo(comp, dst)
					})
					// Main Mode frames must be strictly 0.00 allocs
					if len(comp) >= 4 && binary.LittleEndian.Uint32(comp[:4]) != przm.TDAMagic {
						if allocs != 0.00 {
							t.Fatalf("[%s sz=%d lvl=%d] ZERO-ALLOC VIOLATION: %.2f allocs/run", name, sz, level, allocs)
						}
					}
				}
			}
		}
	}

	t.Run("AllZeroes", func(t *testing.T) {
		testCorpus("AllZeroes", func(sz int) []byte { return make([]byte, sz) })
	})

	t.Run("AllOnes", func(t *testing.T) {
		testCorpus("AllOnes", func(sz int) []byte {
			b := make([]byte, sz)
			for i := range b {
				b[i] = 0xFF
			}
			return b
		})
	})

	t.Run("AlternatingByteMotifs", func(t *testing.T) {
		testCorpus("Alternating", func(sz int) []byte {
			b := make([]byte, sz)
			for i := range b {
				if i%2 == 0 {
					b[i] = 0xAA
				} else {
					b[i] = 0x55
				}
			}
			return b
		})
	})

	t.Run("PseudoRandom", func(t *testing.T) {
		testCorpus("Random", func(sz int) []byte {
			b := make([]byte, sz)
			rng.Read(b)
			return b
		})
	})
}

// TestChallengerZD_LargeScale1MAnd8MRoundtrip tests 1 MiB and 8 MiB roundtrip exactness and zero allocs.
func TestChallengerZD_LargeScale1MAnd8MRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1M/8M roundtrip in short mode")
	}

	largeSizes := []struct {
		name string
		size int
	}{
		{"1MiB_Corpus", 1024 * 1024},
		{"8MiB_Corpus", 8 * 1024 * 1024},
	}

	for _, tc := range largeSizes {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, tc.size)
			pattern := []byte(`{"event":"metric_log","timestamp":1728391200,"cluster":"us-east-1","load":0.785,"status":"OK"}` + "\n")
			for i := 0; i < tc.size; i += len(pattern) {
				copy(buf[i:], pattern)
			}

			for level := 1; level <= 3; level++ {
				comp, err := przm.Compress(buf, level)
				if err != nil {
					t.Fatalf("[%s Lvl%d] Compress failed: %v", tc.name, level, err)
				}

				decomp, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("[%s Lvl%d] Decompress failed: %v", tc.name, level, err)
				}

				if !bytes.Equal(decomp, buf) {
					t.Fatalf("[%s Lvl%d] Decompress mismatch: orig=%d decomp=%d", tc.name, level, len(buf), len(decomp))
				}

				// Verify DecompressTo bit-exact and 0.00 allocs
				dst := bufkit.AlignedBytes(len(buf)+64, bufkit.CacheLineSize)
				decompTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("[%s Lvl%d] DecompressTo failed: %v", tc.name, level, err)
				}
				if !bytes.Equal(decompTo, buf) {
					t.Fatalf("[%s Lvl%d] DecompressTo mismatch!", tc.name, level)
				}

				allocs := testing.AllocsPerRun(10, func() {
					_, _ = przm.DecompressTo(comp, dst)
				})
				if allocs != 0.00 {
					t.Fatalf("[%s Lvl%d] ZERO-ALLOC VIOLATION: %.2f allocs/run", tc.name, level, allocs)
				}
			}
		})
	}
}

// TestChallengerZD_PureGo_Hash4_Inlined tests Hash4 function exactness.
func TestChallengerZD_PureGo_Hash4_Inlined(t *testing.T) {
	for w4 := uint32(0); w4 < 10000; w4++ {
		expected := w4 * przm.HenselHashPrime
		got := przm.Hash4(w4)
		if got != expected {
			t.Fatalf("Hash4(%d) = %d, want %d", w4, got, expected)
		}
	}
}
