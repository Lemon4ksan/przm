// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

// ApplyMicroPredictorForward transforms an input stream into a prediction residual stream
// using a 4-tap in-register Sign-LMS adaptive filter.
// By Theorem 33, the transform is strictly invertible.
func ApplyMicroPredictorForward(src []byte, dst []byte) []byte {
	n := len(src)
	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}

	if n == 0 {
		return dst
	}

	// 4-tap context registers
	var x1, x2, x3, x4 int
	// 4-tap integer weights (Q7 fixed-point, initially balanced)
	w1, w2, w3, w4 := 64, 32, 16, 8

	for i := 0; i < n; i++ {
		orig := int(src[i])

		// Compute predicted byte in Q7
		pred := (w1*x1 + w2*x2 + w3*x3 + w4*x4) >> 7
		if pred < 0 {
			pred = 0
		} else if pred > 255 {
			pred = 255
		}

		res := byte(orig - pred)
		dst[i] = res

		// Sign-LMS weight adaptation
		errVal := int(int8(res))
		if errVal != 0 {
			sgn := 1
			if errVal < 0 {
				sgn = -1
			}

			if x1 > 128 {
				w1 += sgn
			} else if x1 < 128 {
				w1 -= sgn
			}
			if x2 > 128 {
				w2 += sgn
			} else if x2 < 128 {
				w2 -= sgn
			}
			if x3 > 128 {
				w3 += sgn
			} else if x3 < 128 {
				w3 -= sgn
			}
			if x4 > 128 {
				w4 += sgn
			} else if x4 < 128 {
				w4 -= sgn
			}

			// Clamp weights to [-256, 256]
			if w1 > 256 {
				w1 = 256
			} else if w1 < -256 {
				w1 = -256
			}
			if w2 > 256 {
				w2 = 256
			} else if w2 < -256 {
				w2 = -256
			}
			if w3 > 256 {
				w3 = 256
			} else if w3 < -256 {
				w3 = -256
			}
			if w4 > 256 {
				w4 = 256
			} else if w4 < -256 {
				w4 = -256
			}
		}

		// Shift context
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = orig
	}

	return dst
}

// ApplyMicroPredictorInverse exactly reconstructs the original stream from prediction residuals.
func ApplyMicroPredictorInverse(src []byte, dst []byte) []byte {
	n := len(src)
	if cap(dst) < n {
		dst = make([]byte, n)
	} else {
		dst = dst[:n]
	}

	if n == 0 {
		return dst
	}

	// 4-tap context registers
	var x1, x2, x3, x4 int
	// 4-tap integer weights (identical initial state)
	w1, w2, w3, w4 := 64, 32, 16, 8

	for i := 0; i < n; i++ {
		res := src[i]

		// Recompute identical predicted byte
		pred := (w1*x1 + w2*x2 + w3*x3 + w4*x4) >> 7
		if pred < 0 {
			pred = 0
		} else if pred > 255 {
			pred = 255
		}

		orig := byte(int(res) + pred)
		dst[i] = orig
		origInt := int(orig)

		// Identical Sign-LMS weight adaptation
		errVal := int(int8(res))
		if errVal != 0 {
			sgn := 1
			if errVal < 0 {
				sgn = -1
			}

			if x1 > 128 {
				w1 += sgn
			} else if x1 < 128 {
				w1 -= sgn
			}
			if x2 > 128 {
				w2 += sgn
			} else if x2 < 128 {
				w2 -= sgn
			}
			if x3 > 128 {
				w3 += sgn
			} else if x3 < 128 {
				w3 -= sgn
			}
			if x4 > 128 {
				w4 += sgn
			} else if x4 < 128 {
				w4 -= sgn
			}

			if w1 > 256 {
				w1 = 256
			} else if w1 < -256 {
				w1 = -256
			}
			if w2 > 256 {
				w2 = 256
			} else if w2 < -256 {
				w2 = -256
			}
			if w3 > 256 {
				w3 = 256
			} else if w3 < -256 {
				w3 = -256
			}
			if w4 > 256 {
				w4 = 256
			} else if w4 < -256 {
				w4 = -256
			}
		}

		// Shift context
		x4 = x3
		x3 = x2
		x2 = x1
		x1 = origInt
	}

	return dst
}
