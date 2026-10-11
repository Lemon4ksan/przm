// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

// Galois Field F_{2^8} configuration with AES irreducible polynomial:
//   p(x) = x^8 + x^4 + x^3 + x + 1 (0x11B = 283)
// Using primitive generator g = 0x03.
const (
	gfPoly uint16 = 0x11B
	gfGen  uint8  = 0x03
)

var (
	gfExp [512]byte
	gfLog [256]byte
	gfInv [256]byte
)

func init() {
	// Initialize exponential and logarithm lookup tables
	val := 1
	for i := range 255 {
		b := byte(val)
		gfExp[i] = b
		gfExp[i+255] = b
		gfLog[b] = byte(i)

		// Multiply val by generator g = 0x03: (val << 1) ^ val
		v2 := val << 1
		if v2 >= 256 {
			v2 ^= int(gfPoly)
		}
		val = v2 ^ val
	}
	gfExp[255] = gfExp[0]
	gfLog[0] = 0 // By convention

	// Initialize multiplicative inverse table:
	// For a != 0: a * a^{-1} = 1 => log(a) + log(a^{-1}) = 255
	gfInv[0] = 0
	for i := 1; i < 256; i++ {
		invExp := 255 - int(gfLog[i])
		gfInv[i] = gfExp[invExp]
	}
}

// GF256Mul performs Galois Field multiplication over F_{2^8} in O(1) time.
func GF256Mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

// GF256Inv returns the multiplicative inverse of a in F_{2^8} such that GF256Mul(a, GF256Inv(a)) == 1.
// If a == 0, returns 0.
func GF256Inv(a byte) byte {
	return gfInv[a]
}
