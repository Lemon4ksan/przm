// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// Helper to decode hex string into [16]byte
func fromHex16(tb testing.TB, s string) [16]byte {
	if tb != nil {
		tb.Helper()
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		if tb != nil {
			tb.Fatalf("failed to decode hex %q: %v", s, err)
		}
		panic(err)
	}
	if len(b) != 16 {
		if tb != nil {
			tb.Fatalf("hex %q length is %d, expected 16", s, len(b))
		}
		panic("invalid length")
	}
	var res [16]byte
	copy(res[:], b)
	return res
}

// TestFIPS197_AppendixB verifies reduced-round AES and intermediate taps against NIST FIPS 197 Appendix B.
func TestFIPS197_AppendixB(t *testing.T) {
	key := fromHex16(t, "2b7e151628aed2a6abf7158809cf4f3c")
	pt := fromHex16(t, "3243f6a8885a308d313198a2e0370734")

	expectedAddRoundKey0 := fromHex16(t, "193de3bea0f4e22b9ac68d2ae9f84808")
	expectedR1SubBytes := fromHex16(t, "d42711aee0bf98f1b8b45de51e415230")
	expectedR1ShiftRows := fromHex16(t, "d4bf5d30e0b452aeb84111f11e2798e5")
	expectedR1MixColumns := fromHex16(t, "046681e5e0cb199a48f8d37a2806264c")
	expectedR1RoundKey := fromHex16(t, "a0fafe1788542cb123a339392a6c7605")
	expectedR1AddRoundKey := fromHex16(t, "a49c7ff2689f352b6b5bea43026a5049")
	expectedR1StandardNoMC := fromHex16(t, "7445a32768e07e1f9be228c8344beee0")

	expectedR2RoundKey := fromHex16(t, "f2c295f27a96b9435935807a7359f67f")

	expectedFullCiphertext := fromHex16(t, "3925841d02dc09fbdc118597196a0b32")

	// 1. Verify Round 0 tap
	tapped0 := EncryptWithTap(pt[:], key[:], 1, AfterAddRoundKey0)
	if tapped0 != expectedAddRoundKey0 {
		t.Errorf("Round 0 AddRoundKey tap mismatch:\n got: %x\nwant: %x", tapped0, expectedAddRoundKey0)
	}

	// 2. Verify Round 1 taps
	tappedR1SB := EncryptWithTap(pt[:], key[:], 1, AfterSubBytes)
	if tappedR1SB != expectedR1SubBytes {
		t.Errorf("Round 1 SubBytes tap mismatch:\n got: %x\nwant: %x", tappedR1SB, expectedR1SubBytes)
	}

	tappedR1SR := EncryptWithTap(pt[:], key[:], 1, AfterShiftRows)
	if tappedR1SR != expectedR1ShiftRows {
		t.Errorf("Round 1 ShiftRows tap mismatch:\n got: %x\nwant: %x", tappedR1SR, expectedR1ShiftRows)
	}

	// Tap MixColumns on Round 1 with finalMixCol = true
	var tappedR1MC [16]byte
	EncryptBlock(pt, key, 1, true, KeyScheduleStandard, 0, func(r int, p StateTapPoint, s *[16]byte) {
		if r == 1 && p == AfterMixColumns {
			tappedR1MC = *s
		}
	})
	if tappedR1MC != expectedR1MixColumns {
		t.Errorf("Round 1 MixColumns tap mismatch:\n got: %x\nwant: %x", tappedR1MC, expectedR1MixColumns)
	}

	// Also directly verify EncryptWithTap returns the exact AfterMixColumns intermediate state
	tappedR1MCViaTap := EncryptWithTap(pt[:], key[:], 1, AfterMixColumns)
	if tappedR1MCViaTap != expectedR1MixColumns {
		t.Errorf("Round 1 EncryptWithTap(AfterMixColumns) mismatch:\n got: %x\nwant: %x", tappedR1MCViaTap, expectedR1MixColumns)
	}

	// Verify Round 1 final output with MixColumns
	r1WithMC := EncryptReducedRound(pt[:], key[:], 1, true)
	if r1WithMC != expectedR1AddRoundKey {
		t.Errorf("Round 1 (with MC) output mismatch:\n got: %x\nwant: %x", r1WithMC, expectedR1AddRoundKey)
	}

	// Verify Round 1 final output without MixColumns (standard reduced round 1)
	r1NoMC := EncryptReducedRound(pt[:], key[:], 1, false)
	if r1NoMC != expectedR1StandardNoMC {
		t.Errorf("Round 1 (without MC) output mismatch:\n got: %x\nwant: %x", r1NoMC, expectedR1StandardNoMC)
	}

	expectedR2SB := fromHex16(t, "49ded28945db96f17f39871a7702533b")
	expectedR2SR := fromHex16(t, "49db873b453953897f02d2f177de961a")
	expectedR2MC := fromHex16(t, "584dcaf11b4b5aacdbe7caa81b6bb0e5")
	expectedR2AddRoundKey := fromHex16(t, "aa8f5f0361dde3ef82d24ad26832469a")

	// 3. Verify Round 2 intermediate states and final output
	tappedR2SB := EncryptWithTap(pt[:], key[:], 2, AfterSubBytes)
	if tappedR2SB != expectedR2SB {
		t.Errorf("Round 2 SubBytes tap mismatch:\n got: %x\nwant: %x", tappedR2SB, expectedR2SB)
	}

	tappedR2SR := EncryptWithTap(pt[:], key[:], 2, AfterShiftRows)
	if tappedR2SR != expectedR2SR {
		t.Errorf("Round 2 ShiftRows tap mismatch:\n got: %x\nwant: %x", tappedR2SR, expectedR2SR)
	}

	var tappedR2MC [16]byte
	EncryptBlock(pt, key, 2, true, KeyScheduleStandard, 0, func(r int, p StateTapPoint, s *[16]byte) {
		if r == 2 && p == AfterMixColumns {
			tappedR2MC = *s
		}
	})
	if tappedR2MC != expectedR2MC {
		t.Errorf("Round 2 MixColumns tap mismatch:\n got: %x\nwant: %x", tappedR2MC, expectedR2MC)
	}

	// Also directly verify EncryptWithTap returns the exact AfterMixColumns intermediate state
	tappedR2MCViaTap := EncryptWithTap(pt[:], key[:], 2, AfterMixColumns)
	if tappedR2MCViaTap != expectedR2MC {
		t.Errorf("Round 2 EncryptWithTap(AfterMixColumns) mismatch:\n got: %x\nwant: %x", tappedR2MCViaTap, expectedR2MC)
	}

	r2WithMC := EncryptReducedRound(pt[:], key[:], 2, true)
	if r2WithMC != expectedR2AddRoundKey {
		t.Errorf("Round 2 (with MC) output mismatch:\n got: %x\nwant: %x", r2WithMC, expectedR2AddRoundKey)
	}

	// 4. Verify 10-round full AES-128 (final round has no MixColumns)
	fullCT := EncryptReducedRound(pt[:], key[:], 10, false)
	if fullCT != expectedFullCiphertext {
		t.Errorf("10-Round AES-128 ciphertext mismatch:\n got: %x\nwant: %x", fullCT, expectedFullCiphertext)
	}

	// Verify Round Keys 1 and 2
	rks := ExpandKey(key[:], 10, KeyScheduleStandard, 0)
	if rks[1] != expectedR1RoundKey {
		t.Errorf("Round Key 1 mismatch:\n got: %x\nwant: %x", rks[1], expectedR1RoundKey)
	}
	if rks[2] != expectedR2RoundKey {
		t.Errorf("Round Key 2 mismatch:\n got: %x\nwant: %x", rks[2], expectedR2RoundKey)
	}
}

// TestFIPS197_AppendixC1 verifies 10-round AES-128 against Appendix C.1 test vector.
func TestFIPS197_AppendixC1(t *testing.T) {
	key := fromHex16(t, "000102030405060708090a0b0c0d0e0f")
	pt := fromHex16(t, "00112233445566778899aabbccddeeff")
	expectedCT := fromHex16(t, "69c4e0d86a7b0430d8cdb78070b4c55a")

	ct := EncryptReducedRound(pt[:], key[:], 10, false)
	if ct != expectedCT {
		t.Errorf("FIPS 197 C.1 10-Round output mismatch:\n got: %x\nwant: %x", ct, expectedCT)
	}
}

// TestKeySchedule_LinearInvariants proves the theorem:
// W_{4r+k} ^ W_{4r+k-1} = W_{4(r-1)+k} for k in {1, 2, 3} across all rounds r in {1..10}.
func TestKeySchedule_LinearInvariants(t *testing.T) {
	// Test on multiple distinct keys (standard FIPS keys + random keys)
	testKeys := [][]byte{
		[]byte("0123456789abcdef"),
		[]byte("\x2b\x7e\x15\x16\x28\xae\xd2\xa6\xab\xf7\x15\x88\x09\xcf\x4f\x3c"),
		[]byte("\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f"),
	}

	for range 10 {
		rndKey := make([]byte, 16)
		_, _ = rand.Read(rndKey)
		testKeys = append(testKeys, rndKey)
	}

	for idx, k := range testKeys {
		rks := ExpandKey(k, 10, KeyScheduleStandard, 0)
		if len(rks) != 11 {
			t.Fatalf("expected 11 round keys, got %d", len(rks))
		}

		// Convert round keys back into words W_0 .. W_43
		w := make([]uint32, 44)
		for r := 0; r <= 10; r++ {
			for col := range 4 {
				w[4*r+col] = uint32(rks[r][4*col]) |
					(uint32(rks[r][4*col+1]) << 8) |
					(uint32(rks[r][4*col+2]) << 16) |
					(uint32(rks[r][4*col+3]) << 24)
			}
		}

		// Verify linear invariant for columns k = 1, 2, 3
		for r := 1; r <= 10; r++ {
			for kCol := 1; kCol <= 3; kCol++ {
				wCurr := w[4*r+kCol]
				wPrevInRound := w[4*r+kCol-1]
				wPrevRound := w[4*(r-1)+kCol]

				xorDiff := wCurr ^ wPrevInRound
				if xorDiff != wPrevRound {
					t.Fatalf("Key #%d, Round %d, Column %d: Linear invariant violated! W_%d ^ W_%d = %08x, expected W_%d = %08x",
						idx, r, kCol, 4*r+kCol, 4*r+kCol-1, xorDiff, 4*(r-1)+kCol, wPrevRound)
				}
			}
		}
	}
}

// TestKeyScheduleModes tests KeyScheduleZero, KeyScheduleLinear, and KeyScheduleRepeated.
func TestKeyScheduleModes(t *testing.T) {
	key := fromHex16(t, "0123456789abcdef0123456789abcdef")
	pt := fromHex16(t, "feeeeeddddccccbbbaaaa99998888777")

	// 1. KeyScheduleZero
	zeroKeys := ExpandKey(key[:], 5, KeyScheduleZero, 0)
	for r, rk := range zeroKeys {
		var zeroBlock [16]byte
		if rk != zeroBlock {
			t.Errorf("Round %d key in KeyScheduleZero is not zero: %x", r, rk)
		}
	}
	outZero := EncryptBlock(pt, key, 3, false, KeyScheduleZero, 0, nil)

	// In KeyScheduleZero, round 0 AddRoundKey is identity
	tapped0Zero := EncryptBlock(pt, key, 3, false, KeyScheduleZero, 0, func(r int, p StateTapPoint, s *[16]byte) {
		if r == 0 && p == AfterAddRoundKey0 {
			if *s != pt {
				t.Errorf("Round 0 state in KeyScheduleZero should equal plaintext: got %x, want %x", *s, pt)
			}
		}
	})
	_ = tapped0Zero
	_ = outZero

	// 2. KeyScheduleLinear
	delta := byte(0x05)
	linearKeys := ExpandKey(key[:], 5, KeyScheduleLinear, delta)
	for r := 0; r <= 5; r++ {
		for i := range 16 {
			expected := key[i] ^ byte(r*int(delta))
			if linearKeys[r][i] != expected {
				t.Errorf("KeyScheduleLinear round %d byte %d mismatch: got %x, want %x", r, i, linearKeys[r][i], expected)
			}
		}
	}

	// 3. KeyScheduleRepeated
	repKeys := ExpandKey(key[:], 5, KeyScheduleRepeated, 0)
	for r := 0; r <= 5; r++ {
		if repKeys[r] != key {
			t.Errorf("KeyScheduleRepeated round %d mismatch: got %x, want %x", r, repKeys[r], key)
		}
	}
}

// TestReducedRoundLadder tests rounds 1 to 5 with and without MixColumns.
func TestReducedRoundLadder(t *testing.T) {
	key := fromHex16(t, "2b7e151628aed2a6abf7158809cf4f3c")
	pt := fromHex16(t, "3243f6a8885a308d313198a2e0370734")

	for rounds := 1; rounds <= 5; rounds++ {
		for _, finalMix := range []bool{false, true} {
			ct := EncryptReducedRound(pt[:], key[:], rounds, finalMix)
			if ct == pt {
				t.Errorf("Rounds %d finalMix=%v returned unmodified plaintext", rounds, finalMix)
			}

			// Verify Engine stream encryption matches single-block EncryptBlock
			eng, err := NewEngine(Config{
				Rounds:                rounds,
				IncludeLastMixColumns: finalMix,
				KeySchedule:           KeyScheduleStandard,
				Key:                   key[:],
			})
			if err != nil {
				t.Fatalf("NewEngine error: %v", err)
			}

			dst := make([]byte, 16)
			eng.EncryptBlock(dst, pt[:], nil)
			if !bytes.Equal(dst, ct[:]) {
				t.Errorf("Engine.EncryptBlock != EncryptReducedRound for rounds %d finalMix=%v", rounds, finalMix)
			}

			// Stream of 4 blocks
			multiPT := bytes.Repeat(pt[:], 4)
			multiCT := make([]byte, 64)
			err = eng.EncryptStream(multiCT, multiPT)
			if err != nil {
				t.Fatalf("EncryptStream failed: %v", err)
			}
			for b := range 4 {
				if !bytes.Equal(multiCT[b*16:(b+1)*16], ct[:]) {
					t.Errorf("Stream block %d mismatch for rounds %d", b, rounds)
				}
			}
		}
	}
}

// TestMixColumns_InvariantSubspace verifies Feature 4: MixColumns fixed-point invariant subspace.
// For any c in GF(2^8), MixColumns([c, c, c, c]^T) == [c, c, c, c]^T (eigenvalue lambda = 1).
func TestMixColumns_InvariantSubspace(t *testing.T) {
	// 1. Test all 256 uniform states where every byte is c in GF(2^8)
	for c := range 256 {
		var s [16]byte
		for i := range 16 {
			s[i] = byte(c)
		}
		expected := s
		MixColumns(&s)
		if s != expected {
			t.Fatalf("MixColumns failed on uniform state c=0x%02x:\n got: %x\nwant: %x", c, s, expected)
		}
	}

	// 2. Test state with 4 independent uniform columns: col_i = [c_i, c_i, c_i, c_i]^T
	for col0 := 0; col0 < 256; col0 += 64 {
		for col1 := 0; col1 < 256; col1 += 64 {
			for col2 := 0; col2 < 256; col2 += 64 {
				for col3 := 0; col3 < 256; col3 += 64 {
					var s [16]byte
					for r := range 4 {
						s[0*4+r] = byte(col0)
						s[1*4+r] = byte(col1)
						s[2*4+r] = byte(col2)
						s[3*4+r] = byte(col3)
					}
					expected := s
					MixColumns(&s)
					if s != expected {
						t.Fatalf("MixColumns failed on column invariant state:\n got: %x\nwant: %x", s, expected)
					}
				}
			}
		}
	}
}

// TestKeySchedule_BoundaryClamping verifies that ExpandKey clamps rounds safely to [1, 14].
func TestKeySchedule_BoundaryClamping(t *testing.T) {
	key := fromHex16(t, "2b7e151628aed2a6abf7158809cf4f3c")

	// Negative and zero rounds must clamp to 1 (yielding 2 round keys)
	for _, r := range []int{-100, -5, -1, 0} {
		rks := ExpandKey(key[:], r, KeyScheduleStandard, 0)
		if len(rks) != 2 {
			t.Errorf("ExpandKey with rounds=%d should clamp to 1 round (2 round keys), got %d", r, len(rks))
		}
	}

	// Rounds > 14 must clamp to 14 (yielding 15 round keys)
	for _, r := range []int{15, 20, 100} {
		rks := ExpandKey(key[:], r, KeyScheduleStandard, 0)
		if len(rks) != 15 {
			t.Errorf("ExpandKey with rounds=%d should clamp to 14 rounds (15 round keys), got %d", r, len(rks))
		}
	}

	// EncryptWithTap boundary rounds clamping
	for _, r := range []int{-5, 0} {
		tapped := EncryptWithTap(key[:], key[:], r, AfterSubBytes)
		var zeroBlock [16]byte
		if tapped == zeroBlock {
			t.Errorf("EncryptWithTap with rounds=%d returned zero block", r)
		}
	}
}

// BenchmarkEncryptBlock benchmarks single block encryption for rounds 1, 2, 5, 10.
func BenchmarkEncryptBlock(b *testing.B) {
	key := fromHex16(nil, "2b7e151628aed2a6abf7158809cf4f3c")
	pt := fromHex16(nil, "3243f6a8885a308d313198a2e0370734")

	for _, rounds := range []int{1, 2, 5, 10} {
		b.Run(string(rune('0'+rounds))+"_rounds", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(16)
			for i := 0; i < b.N; i++ {
				_ = EncryptBlock(pt, key, rounds, false, KeyScheduleStandard, 0, nil)
			}
		})
	}
}

// BenchmarkEngine_Stream benchmarks stream encryption throughput.
func BenchmarkEngine_Stream(b *testing.B) {
	key := fromHex16(nil, "2b7e151628aed2a6abf7158809cf4f3c")
	eng, err := NewEngine(Config{
		Rounds:                5,
		IncludeLastMixColumns: false,
		KeySchedule:           KeyScheduleStandard,
		Key:                   key[:],
	})
	if err != nil {
		b.Fatal(err)
	}

	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = eng.EncryptStream(dst, src)
	}
}
