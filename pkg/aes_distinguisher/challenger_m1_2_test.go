// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"encoding/hex"
	"math/bits"
	"testing"
)

var expectedTapsWithMC = map[int]map[StateTapPoint]string{
	1: {
		AfterAddRoundKey0: "193de3bea0f4e22b9ac68d2ae9f84808",
		AfterSubBytes:     "d42711aee0bf98f1b8b45de51e415230",
		AfterShiftRows:    "d4bf5d30e0b452aeb84111f11e2798e5",
		AfterMixColumns:   "046681e5e0cb199a48f8d37a2806264c",
		AfterAddRoundKey:  "a49c7ff2689f352b6b5bea43026a5049",
	},
	2: {
		AfterAddRoundKey0: "193de3bea0f4e22b9ac68d2ae9f84808",
		AfterSubBytes:     "49ded28945db96f17f39871a7702533b",
		AfterShiftRows:    "49db873b453953897f02d2f177de961a",
		AfterMixColumns:   "584dcaf11b4b5aacdbe7caa81b6bb0e5",
		AfterAddRoundKey:  "aa8f5f0361dde3ef82d24ad26832469a",
	},
	3: {
		AfterAddRoundKey0: "193de3bea0f4e22b9ac68d2ae9f84808",
		AfterSubBytes:     "ac73cf7befc111df13b5d6b545235ab8",
		AfterShiftRows:    "acc1d6b8efb55a7b1323cfdf457311b5",
		AfterMixColumns:   "75ec0993200b633353c0cf7cbb25d0dc",
		AfterAddRoundKey:  "486c4eee671d9d0d4de3b138d65f58e7",
	},
	4: {
		AfterAddRoundKey0: "193de3bea0f4e22b9ac68d2ae9f84808",
		AfterSubBytes:     "52502f2885a45ed7e311c807f6cf6a94",
		AfterShiftRows:    "52a4c89485116a28e3cf2fd7f6505e07",
		AfterMixColumns:   "0fd6daa9603138bf6fc0106b5eb31301",
		AfterAddRoundKey:  "e0927fe8c86363c0d9b1355085b8be01",
	},
	5: {
		AfterAddRoundKey0: "193de3bea0f4e22b9ac68d2ae9f84808",
		AfterSubBytes:     "e14fd29be8fbfbba35c89653976cae7c",
		AfterShiftRows:    "e1fb967ce8c8ae9b356cd2ba974ffb53",
		AfterMixColumns:   "25d1a9adbd11d168b63a338e4c4cc0b0",
		AfterAddRoundKey:  "f1006f55c1924cef7cc88b325db5d50c",
	},
}

// TestChallenger_IntermediateStateTaps verifies all 5 tap points across rounds 1..5
// comparing Go EncryptBlock(finalMixCol=true) against NIST and C reference ground truth.
func TestChallenger_IntermediateStateTaps(t *testing.T) {
	key := fromHex16(t, "2b7e151628aed2a6abf7158809cf4f3c")
	pt := fromHex16(t, "3243f6a8885a308d313198a2e0370734")

	for r := 1; r <= 5; r++ {
		for tap := AfterAddRoundKey0; tap <= AfterAddRoundKey; tap++ {
			var state [16]byte
			targetRound := r
			if tap == AfterAddRoundKey0 {
				targetRound = 0
			}

			EncryptBlock(pt, key, r, true, KeyScheduleStandard, 0, func(round int, p StateTapPoint, s *[16]byte) {
				if round == targetRound && p == tap {
					state = *s
				}
			})

			gotHex := hex.EncodeToString(state[:])
			wantHex := expectedTapsWithMC[r][tap]
			if gotHex != wantHex {
				t.Fatalf("Round %d Tap %d mismatch: got %s, want %s", r, tap, gotHex, wantHex)
			}
		}
	}
}

// TestChallenger_EncryptWithTap_DefectIsolation verifies that EncryptWithTap
// properly records the AfterMixColumns tap now that finalMixCol is enabled.
func TestChallenger_EncryptWithTap_DefectIsolation(t *testing.T) {
	key := fromHex16(t, "2b7e151628aed2a6abf7158809cf4f3c")
	pt := fromHex16(t, "3243f6a8885a308d313198a2e0370734")

	for r := 1; r <= 5; r++ {
		tappedMC := EncryptWithTap(pt[:], key[:], r, AfterMixColumns)
		var zeroBlock [16]byte
		if tappedMC == zeroBlock {
			t.Fatalf("BUG DETECTED: EncryptWithTap for round %d with AfterMixColumns returned all zeros! Tap never fired!", r)
		}
		gotHex := hex.EncodeToString(tappedMC[:])
		wantHex := expectedTapsWithMC[r][AfterMixColumns]
		if gotHex != wantHex {
			t.Fatalf("Round %d AfterMixColumns tap mismatch: got %s, want %s", r, gotHex, wantHex)
		}
		t.Logf("Verified remediated: EncryptWithTap(r=%d, AfterMixColumns) yields correct state: %x", r, tappedMC)
	}
}

// TestChallenger_EdgeRounds verifies 0, 1, and 10 rounds behavior.
func TestChallenger_EdgeRounds(t *testing.T) {
	key := fromHex16(t, "2b7e151628aed2a6abf7158809cf4f3c")
	pt := fromHex16(t, "3243f6a8885a308d313198a2e0370734")

	// 1. 0-round bypass test
	r0 := EncryptReducedRound(pt[:], key[:], 0, false)
	r1 := EncryptReducedRound(pt[:], key[:], 1, false)
	if r0 == r1 {
		t.Logf("Empirically confirmed: 0-round bypass is clamped to 1-round: %x", r0)
	} else if r0 == pt {
		t.Logf("0-round bypass is true identity")
	}

	// 2. 1-round test
	expR1NoMC := fromHex16(t, "7445a32768e07e1f9be228c8344beee0")
	expR1WithMC := fromHex16(t, "a49c7ff2689f352b6b5bea43026a5049")
	if r1 != expR1NoMC {
		t.Fatalf("1-round (no MC) mismatch: got %x, want %x", r1, expR1NoMC)
	}
	r1MC := EncryptReducedRound(pt[:], key[:], 1, true)
	if r1MC != expR1WithMC {
		t.Fatalf("1-round (with MC) mismatch: got %x, want %x", r1MC, expR1WithMC)
	}

	// 3. 10-round full AES-128 test
	r10 := EncryptReducedRound(pt[:], key[:], 10, false)
	expR10 := fromHex16(t, "3925841d02dc09fbdc118597196a0b32")
	if r10 != expR10 {
		t.Fatalf("10-round mismatch: got %x, want %x", r10, expR10)
	}
}

// TestChallenger_SBoxBalancedness_Proof proves that a_255 = 0 for all coordinates
// and linear combinations based on balancedness over GF(2^8).
func TestChallenger_SBoxBalancedness_Proof(t *testing.T) {
	sbox := GetSBox()

	// Prove each coordinate has weight 128 (balanced) => a_255 = wt % 2 = 0
	for k := range 8 {
		tt := ExtractCoordinateTruthTable(sbox, k)
		hw := 0
		for _, b := range tt {
			hw += bits.OnesCount8(b)
		}
		if hw != 128 {
			t.Fatalf("Coordinate %d is not balanced: weight=%d", k, hw)
		}
		a255 := hw % 2
		if a255 != 0 {
			t.Fatalf("Coordinate %d a_255 != 0", k)
		}
	}

	// Prove all 255 non-zero linear combinations are balanced
	for c := 1; c < 256; c++ {
		tt := ComponentFunctionTruthTable(sbox, byte(c))
		hw := 0
		for _, b := range tt {
			hw += bits.OnesCount8(b)
		}
		if hw != 128 {
			t.Fatalf("Component %d is not balanced: weight=%d", c, hw)
		}
	}
}

// TestChallenger_SBoxDegreeHistogram verifies exact degree 7 and monomial distribution
func TestChallenger_SBoxDegreeHistogram(t *testing.T) {
	sbox := GetSBox()
	profiles := AnalyzeSBox(sbox)

	expActive := [8]int{132, 133, 145, 136, 131, 114, 112, 110}
	expDeg7 := [8]int{3, 4, 5, 4, 3, 2, 2, 2}
	totalDeg7 := 0

	for k := range 8 {
		p := profiles[k]
		if p.Degree != 7 {
			t.Fatalf("Coord %d degree != 7: got %d", k, p.Degree)
		}
		if !p.TopMonomialZero {
			t.Fatalf("Coord %d top monomial != 0", k)
		}
		if p.ActiveMonomials != expActive[k] {
			t.Fatalf("Coord %d active monomials mismatch: got %d, want %d", k, p.ActiveMonomials, expActive[k])
		}
		if p.DegreeHistogram[7] != expDeg7[k] {
			t.Fatalf("Coord %d deg7 mismatch: got %d, want %d", k, p.DegreeHistogram[7], expDeg7[k])
		}
		totalDeg7 += p.DegreeHistogram[7]
	}

	if totalDeg7 != 25 {
		t.Fatalf("Total deg7 monomials != 25: got %d", totalDeg7)
	}
}
