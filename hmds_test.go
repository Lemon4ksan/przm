// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"crypto/rand"
	"math"
	"testing"
)

// TestHMDS_Theorem26_MinkowskiProperties verifies that recovered points satisfy x0^2 - ||x||^2 = 1.
func TestHMDS_Theorem26_MinkowskiProperties(t *testing.T) {
	dist := [4][4]float64{
		{0.0, 0.5, 1.2, 0.8},
		{0.5, 0.0, 1.5, 0.7},
		{1.2, 1.5, 0.0, 1.1},
		{0.8, 0.7, 1.1, 0.0},
	}

	coords := AnalyticalHMDS4(dist)

	for i, pt := range coords {
		spatNorm2 := pt.Spatial[0]*pt.Spatial[0] + pt.Spatial[1]*pt.Spatial[1] + pt.Spatial[2]*pt.Spatial[2]
		minkowskiNorm := pt.Time*pt.Time - spatNorm2
		if math.Abs(minkowskiNorm-1.0) > 1e-4 {
			t.Fatalf("Point %d violates Minkowski hyperboloid condition: x0^2 - ||x||^2 = %f (want 1.0)",
				i, minkowskiNorm)
		}
	}
}

// TestHMDS_PoincareProjection verifies that projection to Poincaré ball satisfies ||z|| < 1.
func TestHMDS_PoincareProjection(t *testing.T) {
	pt := HyperbolicCoord4{
		Time:    2.0,
		Spatial: [3]float64{1.0, 1.0, 1.0}, // x0^2 - ||x||^2 = 4 - 3 = 1
	}

	z := pt.ProjectToPoincareBall()
	zNorm2 := z[0]*z[0] + z[1]*z[1] + z[2]*z[2]

	if zNorm2 >= 1.0 {
		t.Fatalf("Poincaré ball projection norm squared %f >= 1.0", zNorm2)
	}
}

// TestHMDS_Roundtrip verifies end-to-end compression and bit-exact decompression.
func TestHMDS_Roundtrip(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"Small", []byte("Hello, HMDS World!")},
		{"FourChannelCode", bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 256)},
		{"MixedStructured", bytes.Repeat([]byte("ANALYTICAL_HYPERBOLIC_MULTIDIMENSIONAL_SCALING_2026"), 64)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := CompressHMDS(tc.data)
			dec, err := DecompressHMDS(enc, nil)
			if err != nil {
				t.Fatalf("DecompressHMDS failed: %v", err)
			}
			if !bytes.Equal(tc.data, dec) {
				t.Fatalf("DecompressHMDS mismatch: orig %d bytes, dec %d bytes", len(tc.data), len(dec))
			}
		})
	}
}

// TestHMDS_WorstCaseNoise verifies random noise bypass without excessive expansion.
func TestHMDS_WorstCaseNoise(t *testing.T) {
	noise := make([]byte, 65536)
	rand.Read(noise)

	enc := CompressHMDS(noise)
	if len(enc) > len(noise)+25 {
		t.Fatalf("HMDS noise expansion too large: %d vs %d", len(enc), len(noise))
	}

	dec, err := DecompressHMDS(enc, nil)
	if err != nil {
		t.Fatalf("DecompressHMDS failed: %v", err)
	}
	if !bytes.Equal(noise, dec) {
		t.Fatalf("DecompressHMDS noise mismatch")
	}
}

// BenchmarkHMDS_DecodeThroughput measures decoding throughput and memory allocations.
func BenchmarkHMDS_DecodeThroughput(b *testing.B) {
	data := bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}, 2048) // 16 KB
	enc := CompressHMDS(data)

	dst := make([]byte, len(data))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := DecompressHMDS(enc, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}
