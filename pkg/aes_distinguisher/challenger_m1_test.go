// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"bytes"
	"crypto/aes"
	"crypto/rand"
	"encoding/binary"
	"math/bits"
	"testing"
)

// TestChallenger_DifferentialOracle_10000 tests Go reduced-round AES against standard Go crypto/aes
// on full 10 rounds across 10,000 random plaintexts and keys.
func TestChallenger_DifferentialOracle_10000(t *testing.T) {
	const numTrials = 10000

	var pt [16]byte
	var key [16]byte
	var stdCT [16]byte

	for trial := range numTrials {
		if _, err := rand.Read(pt[:]); err != nil {
			t.Fatalf("rand.Read(pt) failed: %v", err)
		}
		if _, err := rand.Read(key[:]); err != nil {
			t.Fatalf("rand.Read(key) failed: %v", err)
		}

		cipher, err := aes.NewCipher(key[:])
		if err != nil {
			t.Fatalf("aes.NewCipher failed: %v", err)
		}
		cipher.Encrypt(stdCT[:], pt[:])

		// 1. Test EncryptReducedRound (10 rounds, finalMixCol=false)
		rrCT := EncryptReducedRound(pt[:], key[:], 10, false)
		if rrCT != stdCT {
			t.Fatalf("Trial %d mismatch in EncryptReducedRound:\n  pt:  %x\n  key: %x\n  got: %x\n want: %x",
				trial, pt, key, rrCT, stdCT)
		}

		// 2. Test Engine.EncryptBlock
		eng, err := NewEngine(Config{
			Rounds:                10,
			IncludeLastMixColumns: false,
			KeySchedule:           KeyScheduleStandard,
			Key:                   key[:],
		})
		if err != nil {
			t.Fatalf("NewEngine failed: %v", err)
		}

		var engCT [16]byte
		eng.EncryptBlock(engCT[:], pt[:], nil)
		if engCT != stdCT {
			t.Fatalf("Trial %d mismatch in Engine.EncryptBlock:\n  pt:  %x\n  key: %x\n  got: %x\n want: %x",
				trial, pt, key, engCT, stdCT)
		}
	}
}

// TestChallenger_BoundaryVectors tests fixed boundary vectors (zeros, ones, alternating bits) against crypto/aes.
func TestChallenger_BoundaryVectors(t *testing.T) {
	boundaryPatterns := [][]byte{
		bytes.Repeat([]byte{0x00}, 16),
		bytes.Repeat([]byte{0xFF}, 16),
		bytes.Repeat([]byte{0xAA}, 16),
		bytes.Repeat([]byte{0x55}, 16),
		bytes.Repeat([]byte{0x0F}, 16),
		bytes.Repeat([]byte{0xF0}, 16),
		{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F},
		{0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F},
	}

	for _, ptBytes := range boundaryPatterns {
		for _, keyBytes := range boundaryPatterns {
			cipher, err := aes.NewCipher(keyBytes)
			if err != nil {
				t.Fatalf("aes.NewCipher failed: %v", err)
			}
			var stdCT [16]byte
			cipher.Encrypt(stdCT[:], ptBytes)

			rrCT := EncryptReducedRound(ptBytes, keyBytes, 10, false)
			if rrCT != stdCT {
				t.Fatalf("Boundary mismatch for pt=%x key=%x: got %x, want %x", ptBytes, keyBytes, rrCT, stdCT)
			}
		}
	}
}

// TestChallenger_Engine_StreamDifferential verifies multi-block stream encryption against crypto/aes block-by-block.
func TestChallenger_Engine_StreamDifferential(t *testing.T) {
	const numBlocks = 256
	src := make([]byte, numBlocks*16)
	dst := make([]byte, numBlocks*16)
	expected := make([]byte, numBlocks*16)
	key := make([]byte, 16)

	_, _ = rand.Read(key)
	_, _ = rand.Read(src)

	cipher, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher failed: %v", err)
	}

	for b := range numBlocks {
		cipher.Encrypt(expected[b*16:(b+1)*16], src[b*16:(b+1)*16])
	}

	eng, err := NewEngine(Config{
		Rounds:                10,
		IncludeLastMixColumns: false,
		KeySchedule:           KeyScheduleStandard,
		Key:                   key,
	})
	if err != nil {
		t.Fatalf("NewEngine failed: %v", err)
	}

	if err := eng.EncryptStream(dst, src); err != nil {
		t.Fatalf("EncryptStream failed: %v", err)
	}

	if !bytes.Equal(dst, expected) {
		t.Fatalf("EncryptStream output mismatch with standard crypto/aes over %d blocks", numBlocks)
	}
}

// TestChallenger_FMT_UnitBasisVectors_All256 rigorously challenges FMT on all 256 unit basis vectors e_i.
// It verifies:
// 1. Involution M_8(M_8(e_i)) == e_i.
// 2. Exact theoretical ANF coefficient: a_u = 1 iff (i &^ u) == 0 (subcube incidence).
func TestChallenger_FMT_UnitBasisVectors_All256(t *testing.T) {
	for i := range 256 {
		var tt [32]byte
		tt[i/8] = 1 << (i % 8)

		// Forward transform
		anf := tt
		FastMobiusTransform8(&anf)

		// Involution check
		inv := anf
		FastMobiusTransform8(&inv)

		if inv != tt {
			t.Fatalf("Unit basis vector e_%d: FMT is not an involution! Got %x, want %x", i, inv, tt)
		}

		// Mathematical incidence check: a_u == 1 iff i is a subcube of u (i &^ u == 0)
		for u := range 256 {
			actualBit := (anf[u/8] >> (u % 8)) & 1
			expectedBit := byte(0)
			if (i &^ u) == 0 {
				expectedBit = 1
			}
			if actualBit != expectedBit {
				t.Fatalf("Basis e_%d at monomial u=%d: got coefficient %d, expected %d", i, u, actualBit, expectedBit)
			}
		}

		// Degree check: for e_i, the highest degree active monomial is u=255 (since i &^ 255 == 0 always)
		// except when i == 0 where only a_0 is set? Wait! For any i, i &^ 255 == 0, so a_255 is ALWAYS 1 for any unit vector!
		topBit := (anf[31] >> 7) & 1
		if topBit != 1 {
			t.Fatalf("Basis e_%d: top monomial a_255 must be 1, got 0", i)
		}
		deg := ComputeANFDegree(anf[:])
		if deg != 8 {
			t.Fatalf("Basis e_%d: degree must be 8, got %d", i, deg)
		}
	}
}

// TestChallenger_FMT_RandomTruthTables_Involution tests FMT involution M_8(M_8(T)) == T on 10,000 random truth tables.
func TestChallenger_FMT_RandomTruthTables_Involution(t *testing.T) {
	const numTrials = 10000
	var orig [32]byte

	for trial := range numTrials {
		if _, err := rand.Read(orig[:]); err != nil {
			t.Fatalf("rand.Read failed: %v", err)
		}

		tt := orig
		FastMobiusTransform8(&tt)
		FastMobiusTransform8(&tt)

		if tt != orig {
			t.Fatalf("Trial %d: FMT involution failed! got %x, want %x", trial, tt, orig)
		}
	}
}

// TestChallenger_FMT_Linearity tests that FMT is a linear isomorphism over GF(2):
// FMT(T1 ^ T2) == FMT(T1) ^ FMT(T2) for 10,000 random pairs.
func TestChallenger_FMT_Linearity(t *testing.T) {
	const numTrials = 10000
	var t1, t2, tXor [32]byte

	for trial := range numTrials {
		_, _ = rand.Read(t1[:])
		_, _ = rand.Read(t2[:])

		for i := range 32 {
			tXor[i] = t1[i] ^ t2[i]
		}

		anf1 := t1
		FastMobiusTransform8(&anf1)

		anf2 := t2
		FastMobiusTransform8(&anf2)

		anfXor := tXor
		FastMobiusTransform8(&anfXor)

		var expectedAnfXor [32]byte
		for i := range 32 {
			expectedAnfXor[i] = anf1[i] ^ anf2[i]
		}

		if anfXor != expectedAnfXor {
			t.Fatalf("Trial %d: FMT linearity violated! Got %x, want %x", trial, anfXor, expectedAnfXor)
		}
	}
}

// TestChallenger_KeySchedule_LinearInvariant_100000 verifies that
// W_{4r+k} ^ W_{4r+k-1} = W_{4(r-1)+k} holds for 100,000 random keys across rounds 1..10 and columns 1..3.
// Total assertions: 100,000 * 10 * 3 = 3,000,000 invariant checks.
func TestChallenger_KeySchedule_LinearInvariant_100000(t *testing.T) {
	const numKeys = 100000
	key := make([]byte, 16)
	w := make([]uint32, 44)

	for trial := range numKeys {
		// Populate random key
		binary.LittleEndian.PutUint64(key[0:8], uint64(trial)*0x9E3779B97F4A7C15^0x123456789ABCDEF0)
		binary.LittleEndian.PutUint64(key[8:16], uint64(trial)*0xC6A4A7935BD1E995^0x0FEDCBA987654321)

		rks := ExpandKey(key, 10, KeyScheduleStandard, 0)

		// Convert 11 round keys to 44 32-bit words
		for r := 0; r <= 10; r++ {
			for col := range 4 {
				idx := 4*r + col
				w[idx] = uint32(rks[r][4*col]) |
					(uint32(rks[r][4*col+1]) << 8) |
					(uint32(rks[r][4*col+2]) << 16) |
					(uint32(rks[r][4*col+3]) << 24)
			}
		}

		// Verify linear invariant for columns k in {1, 2, 3} across rounds r in {1..10}
		for r := 1; r <= 10; r++ {
			for k := 1; k <= 3; k++ {
				wCurr := w[4*r+k]
				wPrevInRound := w[4*r+k-1]
				wPrevRound := w[4*(r-1)+k]

				if wCurr^wPrevInRound != wPrevRound {
					t.Fatalf("Trial %d, Round %d, Column %d: Linear invariant violated! W_%d ^ W_%d != W_%d",
						trial, r, k, 4*r+k, 4*r+k-1, 4*(r-1)+k)
				}
			}

			// In addition, verify that column 0 satisfies the exact non-linear step:
			// W_{4r} ^ W_{4(r-1)} == SubWord(RotWord(W_{4r-1})) ^ Rcon[r]
			w0Curr := w[4*r]
			w0PrevRound := w[4*(r-1)]
			wLast := w[4*r-1]

			// RotWord: cyclic rotate left by 8 bits
			rot := bits.RotateLeft32(wLast, -8) // Little-endian byte rotation: b0,b1,b2,b3 -> b1,b2,b3,b0
			b0 := byte(rot)
			b1 := byte(rot >> 8)
			b2 := byte(rot >> 16)
			b3 := byte(rot >> 24)

			sb := uint32(sbox[b0]) |
				(uint32(sbox[b1]) << 8) |
				(uint32(sbox[b2]) << 16) |
				(uint32(sbox[b3]) << 24)

			expectedDiff := sb ^ uint32(rcon[r])
			actualDiff := w0Curr ^ w0PrevRound
			if actualDiff != expectedDiff {
				t.Fatalf("Trial %d, Round %d, Column 0: Non-linear expansion violated! Got %08x, want %08x",
					trial, r, actualDiff, expectedDiff)
			}
		}
	}
}

// TestChallenger_EdgeCases tests edge cases: short slices, clamping, out-of-range rounds.
func TestChallenger_EdgeCases(t *testing.T) {
	// 1. Empty/short slices
	shortPT := []byte{0x01, 0x02}
	shortKey := []byte{0xAA}
	ct1 := EncryptReducedRound(shortPT, shortKey, 1, false)
	ct2 := EncryptReducedRound(shortPT, shortKey, 0, false) // rounds=0 clamped to 1
	if ct1 != ct2 {
		t.Errorf("rounds=0 clamping failed: ct1=%x ct2=%x", ct1, ct2)
	}

	// 2. FastMobiusTransform empty slice
	emptyANF := FastMobiusTransform(nil)
	if len(emptyANF) != 32 {
		t.Fatalf("expected 32 bytes for empty truth table, got %d", len(emptyANF))
	}
	var zero32 [32]byte
	if !bytes.Equal(emptyANF, zero32[:]) {
		t.Fatalf("expected all zeros for empty truth table ANF, got %x", emptyANF)
	}

	// 3. ComputeANFDegree empty slice
	if deg := ComputeANFDegree(nil); deg != 0 {
		t.Fatalf("expected degree 0 for empty ANF, got %d", deg)
	}

	// 4. ExtractCoordinateTruthTable out-of-range bit
	ttNeg := ExtractCoordinateTruthTable(sbox, -1)
	if ttNeg != zero32 {
		t.Fatalf("expected zero truth table for bit -1, got %x", ttNeg)
	}
	ttHigh := ExtractCoordinateTruthTable(sbox, 8)
	if ttHigh != zero32 {
		t.Fatalf("expected zero truth table for bit 8, got %x", ttHigh)
	}

	// 5. NewEngine out-of-range rounds
	if _, err := NewEngine(Config{Rounds: 0}); err == nil {
		t.Fatalf("expected error for Rounds=0")
	}
	if _, err := NewEngine(Config{Rounds: 15}); err == nil {
		t.Fatalf("expected error for Rounds=15")
	}
}
