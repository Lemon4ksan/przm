// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package swarmixer

import (
	"math/rand"
	"testing"
)

func TestMixer_InitialState(t *testing.T) {
	m := NewMixer()
	if m.PackedWeights() != 0 {
		t.Fatalf("expected packed weights 0, got %d", m.PackedWeights())
	}

	w := m.Weights()
	for i, v := range w {
		if v != 0 {
			t.Fatalf("weight %d: expected 0, got %d", i, v)
		}
	}

	// Prediction with 0 weights should be exactly 32768 (50% probability) for any features
	for f := 0; f < 256; f++ {
		p := m.Predict(uint8(f))
		if p != 32768 {
			t.Fatalf("features %d: expected prediction 32768, got %d", f, p)
		}
	}
}

func TestMixer_FeatureExtraction(t *testing.T) {
	// Bias bit must always be 1
	for b1 := 0; b1 < 256; b1++ {
		for b2 := 0; b2 < 256; b2 += 37 {
			f := ExtractFeatures(uint8(b1), uint8(b2))
			if f&1 != 1 {
				t.Fatalf("bias bit must be 1 for b1=%d, b2=%d", b1, b2)
			}
		}
	}

	// Syntax delimiter for b1='{'
	f := ExtractFeatures('{', 'a')
	if (f>>1)&1 != 1 {
		t.Fatalf("expected bit 1 set for syntax delimiter '{'")
	}
	if (f>>2)&1 != 0 {
		t.Fatalf("syntax delimiter '{' should not be lowercase")
	}

	// Lowercase ASCII for b1='a'
	f = ExtractFeatures('a', '{')
	if (f>>2)&1 != 1 {
		t.Fatalf("expected bit 2 set for lowercase 'a'")
	}

	// Uppercase ASCII for b1='Z'
	f = ExtractFeatures('Z', '{')
	if (f>>3)&1 != 1 {
		t.Fatalf("expected bit 3 set for uppercase 'Z'")
	}

	// Digit for b1='7'
	f = ExtractFeatures('7', '{')
	if (f>>3)&1 != 1 {
		t.Fatalf("expected bit 3 set for digit '7'")
	}

	// Repetition run: b1 == b2 (bit 6)
	f = ExtractFeatures('x', 'x')
	if (f>>6)&1 != 1 {
		t.Fatalf("expected bit 6 set for run 'x' == 'x'")
	}

	// Non-repetition: b1 != b2
	f = ExtractFeatures('x', 'y')
	if (f>>6)&1 != 0 {
		t.Fatalf("expected bit 6 clear for non-run 'x' != 'y'")
	}

	// Parity / LSB (bit 7)
	f = ExtractFeatures(1, 0)
	if (f>>7)&1 != 1 {
		t.Fatalf("expected bit 7 set for b1=1 LSB")
	}
	f = ExtractFeatures(0, 1)
	if (f>>7)&1 != 0 {
		t.Fatalf("expected bit 7 clear for b1=0 LSB")
	}
}

func TestMixer_DotProductBitExact(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 1000; iter++ {
		var weights [8]int8
		for i := 0; i < 8; i++ {
			weights[i] = int8(rng.Intn(256) - 128)
		}
		m := NewMixerWithWeights(weights)

		for f := 0; f < 256; f++ {
			features := uint8(f)

			// Reference scalar dot product
			var expected int32
			for i := 0; i < 8; i++ {
				if (features>>i)&1 == 1 {
					expected += int32(weights[i])
				}
			}

			actual := m.DotProduct(features)
			if actual != expected {
				t.Fatalf("features 0x%02X: expected dot product %d, got %d", features, expected, actual)
			}
		}
	}
}

func TestMixer_FastSigmoidProperties(t *testing.T) {
	// Monotonicity check across full range [-1024, +1024]
	var prev uint16
	for z := int32(-1024); z <= 1024; z++ {
		cur := FastSigmoid(z)
		if cur < prev {
			t.Fatalf("monotonicity violation at z=%d: prev=%d, cur=%d", z, prev, cur)
		}
		prev = cur
	}

	// Boundary and midpoint checks
	if FastSigmoid(0) != 32768 {
		t.Fatalf("FastSigmoid(0) expected 32768, got %d", FastSigmoid(0))
	}
	if FastSigmoid(-128) != 0 {
		t.Fatalf("FastSigmoid(-128) expected 0, got %d", FastSigmoid(-128))
	}
	if FastSigmoid(128) != 65535 {
		t.Fatalf("FastSigmoid(128) expected 65535, got %d", FastSigmoid(128))
	}
	if FastSigmoid(-1024) != 0 {
		t.Fatalf("FastSigmoid(-1024) expected 0, got %d", FastSigmoid(-1024))
	}
	if FastSigmoid(1024) != 65535 {
		t.Fatalf("FastSigmoid(1024) expected 65535, got %d", FastSigmoid(1024))
	}

	// Symmetry check: FastSigmoid(z) + FastSigmoid(-z) ≈ 65536
	for z := int32(1); z <= 128; z++ {
		pPos := int32(FastSigmoid(z))
		pNeg := int32(FastSigmoid(-z))
		sum := pPos + pNeg
		diff := sum - 65536
		if diff < -1 || diff > 1 {
			t.Fatalf("symmetry error at z=%d: pos=%d, neg=%d, sum=%d", z, pPos, pNeg, sum)
		}
	}
}

func TestMixer_SaturationBounds(t *testing.T) {
	m := NewMixer()

	// Verify all 8 lanes can saturate independently to WeightMax (127) and WeightMin (-128)
	for lane := 0; lane < 8; lane++ {
		features := uint8(1 << lane)

		// Train on bit 1 until saturated
		for step := 0; step < 200; step++ {
			m.Update(features, 1, 64)
			w := m.Weights()
			if w[lane] < WeightMin || w[lane] > WeightMax {
				t.Fatalf("lane %d exceeded bounds: %d", lane, w[lane])
			}
		}
		if m.Weights()[lane] != WeightMax {
			t.Fatalf("lane %d expected to saturate at %d, got %d", lane, WeightMax, m.Weights()[lane])
		}

		// Train on bit 0 until saturated
		for step := 0; step < 300; step++ {
			m.Update(features, 0, 64)
			w := m.Weights()
			if w[lane] < WeightMin || w[lane] > WeightMax {
				t.Fatalf("lane %d exceeded bounds: %d", lane, w[lane])
			}
		}
		if m.Weights()[lane] != WeightMin {
			t.Fatalf("lane %d expected to saturate at %d, got %d", lane, WeightMin, m.Weights()[lane])
		}
	}
}

func TestMixer_ConvergenceRepeatingStream(t *testing.T) {
	// Test 1: Constant 1 stream converges to P > 60000
	m1 := NewMixer()
	features := uint8(0b00000001) // Bias feature
	for i := 0; i < 30; i++ {
		m1.Update(features, 1, 32)
	}
	p1 := m1.Predict(features)
	if p1 < 60000 {
		t.Fatalf("expected convergence to high probability (>60000), got %d", p1)
	}

	// Test 2: Constant 0 stream converges to P < 5000
	m0 := NewMixer()
	for i := 0; i < 30; i++ {
		m0.Update(features, 0, 32)
	}
	p0 := m0.Predict(features)
	if p0 > 5000 {
		t.Fatalf("expected convergence to low probability (<5000), got %d", p0)
	}

	// Test 3: Alternating stream with context (0, 1, 0, 1, ...)
	// When prev byte is 0, next is 1. When prev byte is 1, next is 0.
	mAlt := NewMixer()
	var b1, b2 uint8 = 0, 0
	correctPredictions := 0
	totalSteps := 200

	for i := 0; i < totalSteps; i++ {
		nextBit := uint8(i % 2)
		f := ExtractFeatures(b1, b2)

		p := mAlt.Predict(f)
		predBit := uint8(0)
		if p > 32768 {
			predBit = 1
		}

		if i > 50 && predBit == nextBit {
			correctPredictions++
		}

		mAlt.Update(f, nextBit, 24)

		b2 = b1
		b1 = nextBit
	}

	accuracy := float64(correctPredictions) / float64(totalSteps-50)
	if accuracy < 0.90 {
		t.Fatalf("expected >90%% accuracy on alternating context, got %.2f%%", accuracy*100)
	}
}

func TestMixer_SyntheticContextSheafFiberStream(t *testing.T) {
	// Simulate JSON-like stream: delimiter '{', identifier 'a', delimiter ':', identifier 'b'
	// Delimiters map to fiber 0 (actualBit = 0), content maps to fiber 1 (actualBit = 1)
	stream := []byte("{ \"name\" : \"PRZM\" , \"version\" : 1 }")
	mixer := NewMixer()

	var b1, b2 uint8 = 0, 0
	var correctCount, totalCount int

	// Repeat pattern 10 times to evaluate online learning convergence
	for epoch := 0; epoch < 10; epoch++ {
		for _, cur := range stream {
			isSyntax := SheafSyntaxTable[cur]
			targetBit := uint8(0)
			if isSyntax == 0 {
				targetBit = 1 // content fiber
			}

			f := ExtractFeatures(b1, b2)
			p := mixer.Predict(f)

			predBit := uint8(0)
			if p > 32768 {
				predBit = 1
			}

			// In later epochs, measure accuracy
			if epoch >= 5 {
				totalCount++
				if predBit == targetBit {
					correctCount++
				}
			}

			mixer.Update(f, targetBit, 20)

			b2 = b1
			b1 = cur
		}
	}

	acc := float64(correctCount) / float64(totalCount)
	if acc < 0.78 {
		t.Fatalf("expected >78%% fiber prediction accuracy, got %.2f%% (%d/%d)", acc*100, correctCount, totalCount)
	}
}

func TestMixer_ZeroAllocations(t *testing.T) {
	var m Mixer
	f := ExtractFeatures('{', 'a')

	allocsPredict := testing.AllocsPerRun(1000, func() {
		_ = m.Predict(f)
	})
	if allocsPredict != 0 {
		t.Fatalf("Predict produced %f allocations/op, strictly 0 required", allocsPredict)
	}

	allocsUpdate := testing.AllocsPerRun(1000, func() {
		m.Update(f, 1, 16)
	})
	if allocsUpdate != 0 {
		t.Fatalf("Update produced %f allocations/op, strictly 0 required", allocsUpdate)
	}

	allocsExtract := testing.AllocsPerRun(1000, func() {
		_ = ExtractFeatures('x', 'y')
	})
	if allocsExtract != 0 {
		t.Fatalf("ExtractFeatures produced %f allocations/op, strictly 0 required", allocsExtract)
	}
}

func TestMixer_DeterministicReplay(t *testing.T) {
	m1 := NewMixer()
	m2 := NewMixer()

	rng1 := rand.New(rand.NewSource(12345))
	rng2 := rand.New(rand.NewSource(12345))

	for i := 0; i < 500; i++ {
		f1 := uint8(rng1.Intn(256))
		bit1 := uint8(rng1.Intn(2))
		lr1 := uint8(rng1.Intn(64) + 1)

		f2 := uint8(rng2.Intn(256))
		bit2 := uint8(rng2.Intn(2))
		lr2 := uint8(rng2.Intn(64) + 1)

		p1 := m1.Predict(f1)
		p2 := m2.Predict(f2)
		if p1 != p2 {
			t.Fatalf("divergence at step %d: p1=%d, p2=%d", i, p1, p2)
		}

		m1.Update(f1, bit1, lr1)
		m2.Update(f2, bit2, lr2)

		if m1.PackedWeights() != m2.PackedWeights() {
			t.Fatalf("weight divergence at step %d: w1=0x%016X, w2=0x%016X", i, m1.PackedWeights(), m2.PackedWeights())
		}
	}
}

func TestMixer_BoundaryCases(t *testing.T) {
	m := NewMixer()

	// 1. Inactive features (0) should never change weights
	m.Update(0, 1, 32)
	if m.PackedWeights() != 0 {
		t.Fatalf("update with 0 features changed weights to %d", m.PackedWeights())
	}

	// 2. Learning rate 0 should never change weights
	m.Update(0xFF, 1, 0)
	if m.PackedWeights() != 0 {
		t.Fatalf("update with learning rate 0 changed weights to %d", m.PackedWeights())
	}

	// 3. Perfect prediction produces no weight change
	m.SetWeights([8]int8{127, 127, 127, 127, 127, 127, 127, 127})
	p := m.Predict(0xFF)
	if p != 65535 {
		t.Fatalf("expected saturated prediction 65535, got %d", p)
	}
	pwBefore := m.PackedWeights()
	m.Update(0xFF, 1, 32)
	if m.PackedWeights() != pwBefore {
		t.Fatalf("update on perfect prediction changed weights: %d vs %d", m.PackedWeights(), pwBefore)
	}
}

var (
	sinkP uint16
	sinkF uint8
)

func BenchmarkMixer_Predict(b *testing.B) {
	m := NewMixerWithWeights([8]int8{12, -34, 56, -78, 90, -12, 34, -56})
	features := uint8(0b10101011)

	var p uint16
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p += m.Predict(features)
	}
	sinkP = p
}

func BenchmarkMixer_Predict_Unrolled4(b *testing.B) {
	m := NewMixerWithWeights([8]int8{12, -34, 56, -78, 90, -12, 34, -56})
	features := uint8(0b10101011)

	var p0, p1, p2, p3 uint16
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i += 4 {
		p0 += m.Predict(features)
		p1 += m.Predict(features)
		p2 += m.Predict(features)
		p3 += m.Predict(features)
	}
	sinkP = p0 + p1 + p2 + p3
}

func BenchmarkMixer_Update(b *testing.B) {
	m := NewMixer()
	features := uint8(0b10101011)
	bit := uint8(1)
	lr := uint8(16)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(features, bit, lr)
		bit ^= 1
	}
}

func BenchmarkMixer_ExtractAndPredict(b *testing.B) {
	m := NewMixerWithWeights([8]int8{15, -20, 35, -40, 55, -60, 75, -80})
	b1, b2 := uint8('{'), uint8('a')

	var p uint16
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := ExtractFeatures(b1, b2)
		p += m.Predict(f)
	}
	sinkP = p
}

func BenchmarkMixer_FullCycle(b *testing.B) {
	m := NewMixer()
	b1, b2 := uint8('{'), uint8('a')
	bit := uint8(1)
	lr := uint8(16)

	var p uint16
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := ExtractFeatures(b1, b2)
		p += m.Predict(f)
		m.Update(f, bit, lr)
		bit ^= 1
	}
	sinkP = p
}
