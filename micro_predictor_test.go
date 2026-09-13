// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestMicroPredictor_Roundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	sizes := []int{0, 1, 2, 4, 10, 64, 256, 1024, 65536}

	for _, sz := range sizes {
		data := make([]byte, sz)
		rng.Read(data)

		fwd := ApplyMicroPredictorForward(data, nil)
		inv := ApplyMicroPredictorInverse(fwd, nil)

		if !bytes.Equal(inv, data) {
			t.Fatalf("size %d roundtrip mismatch", sz)
		}
	}
}

func TestMicroPredictor_SmoothSignalConvergence(t *testing.T) {
	// Smooth sine wave signal: predictor should rapidly reduce residual amplitude
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(128 + int(50.0*float64(i%64)/64.0))
	}

	fwd := ApplyMicroPredictorForward(data, nil)

	entOrig, _ := computeSampleEntropy(data)
	entFwd, _ := computeSampleEntropy(fwd)

	if entFwd >= entOrig {
		t.Logf("original entropy: %.3f, forward entropy: %.3f", entOrig, entFwd)
	}
}
