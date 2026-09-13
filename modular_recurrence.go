// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

// ApplyModularRecurrenceForward applies a second-order linear recurrence filter over Z/256Z:
//   y_n = x_n - c1 * x_{n-1} - c2 * x_{n-2} (mod 256)
// By Theorem 32, this is strictly bijective.
func ApplyModularRecurrenceForward(src []byte, dst []byte, c1, c2 byte) []byte {
	n := len(src)
	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}

	if n == 0 {
		return dst
	}

	dst[0] = src[0]
	if n == 1 {
		return dst
	}

	dst[1] = src[1] - c1*src[0]

	for i := 2; i < n; i++ {
		dst[i] = src[i] - c1*src[i-1] - c2*src[i-2]
	}

	return dst
}

// ApplyModularRecurrenceInverse exactly inverts the second-order linear recurrence filter:
//   x_n = y_n + c1 * x_{n-1} + c2 * x_{n-2} (mod 256)
func ApplyModularRecurrenceInverse(src []byte, dst []byte, c1, c2 byte) []byte {
	n := len(src)
	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}

	if n == 0 {
		return dst
	}

	dst[0] = src[0]
	if n == 1 {
		return dst
	}

	dst[1] = src[1] + c1*dst[0]

	for i := 2; i < n; i++ {
		dst[i] = src[i] + c1*dst[i-1] + c2*dst[i-2]
	}

	return dst
}

// RecurrenceCandidate pairs
type RecurrencePair struct {
	C1 byte
	C2 byte
}

var CommonRecurrencePairs = []RecurrencePair{
	{C1: 1, C2: 0},     // 1st order delta: y = x_i - x_{i-1}
	{C1: 2, C2: 255},   // 2nd order ramp:  y = x_i - 2x_{i-1} + x_{i-2} (255 == -1 mod 256)
	{C1: 3, C2: 254},   // 3rd order ramp:  y = x_i - 3x_{i-1} + 2x_{i-2} (254 == -2 mod 256)
	{C1: 1, C2: 1},     // Additive recurrence (Fibonacci)
	{C1: 0, C2: 1},     // Stride-2 delta
	{C1: 2, C2: 0},     // Slope-2 delta
}

// EstimateModularRecurrence scans a sample and finds the best (c1, c2) minimizing Shannon entropy.
func EstimateModularRecurrence(src []byte) (bestC1, bestC2 byte, gain float64) {
	sampleLen := len(src)
	if sampleLen > 256 {
		sampleLen = 256
	}
	if sampleLen < 16 {
		return 0, 0, 0
	}

	sample := src[:sampleLen]
	baseEntropy, _ := computeSampleEntropy(sample)
	bestEnt := baseEntropy
	bestC1 = 0
	bestC2 = 0

	var tmp [256]byte

	for _, pair := range CommonRecurrencePairs {
		ApplyModularRecurrenceForward(sample, tmp[:sampleLen], pair.C1, pair.C2)
		ent, _ := computeSampleEntropy(tmp[:sampleLen])
		if ent < bestEnt {
			bestEnt = ent
			bestC1 = pair.C1
			bestC2 = pair.C2
		}
	}

	gain = baseEntropy - bestEnt
	return bestC1, bestC2, gain
}
