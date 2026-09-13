// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestModularRecurrence_Roundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(999))
	sizes := []int{0, 1, 2, 3, 10, 64, 256, 4096, 65536}

	for _, sz := range sizes {
		data := make([]byte, sz)
		rng.Read(data)

		for _, pair := range CommonRecurrencePairs {
			fwd := ApplyModularRecurrenceForward(data, nil, pair.C1, pair.C2)
			inv := ApplyModularRecurrenceInverse(fwd, nil, pair.C1, pair.C2)

			if !bytes.Equal(inv, data) {
				t.Fatalf("size %d pair (%d, %d) roundtrip mismatch", sz, pair.C1, pair.C2)
			}
		}
	}
}

func TestModularRecurrence_RampAnnihilation(t *testing.T) {
	// Create linear ramp: x_i = 10 + 5*i mod 256
	// Second-order difference (c1=2, c2=255) must annihilate this completely to 0!
	data := make([]byte, 128)
	for i := range data {
		data[i] = byte((10 + 5*i) % 256)
	}

	fwd := ApplyModularRecurrenceForward(data, nil, 2, 255)
	// After position 1, all entries must be strictly 0!
	for i := 2; i < len(fwd); i++ {
		if fwd[i] != 0 {
			t.Fatalf("ramp not annihilated at index %d: got %d, expected 0", i, fwd[i])
		}
	}
}
