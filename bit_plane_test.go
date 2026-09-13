// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestTranspose8x8_Involution(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 10000; i++ {
		x := rng.Uint64()
		trans := Transpose8x8(x)
		inv := Transpose8x8(trans)
		if inv != x {
			t.Fatalf("involution failed: x=%x, trans=%x, inv=%x", x, trans, inv)
		}
	}
}

func TestBitPlaneSlice_Roundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))
	sizes := []int{0, 1, 7, 8, 15, 16, 63, 64, 127, 1024, 65536, 131072}

	for _, sz := range sizes {
		data := make([]byte, sz)
		rng.Read(data)

		fwd := BitPlaneSliceForward(data, nil)
		inv := BitPlaneSliceInverse(fwd, nil)

		if !bytes.Equal(inv, data) {
			t.Fatalf("size %d roundtrip mismatch", sz)
		}
	}
}
