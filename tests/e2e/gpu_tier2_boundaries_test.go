// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

// Tier 2: Boundary & Corner Cases (10 Boundaries x 5 Tests = 50 Tests)

// Boundary 1: Sub-Minimum Match Length (len < 4 bytes)
func TestTier2_GPU_B01_SubMinMatchLength(t *testing.T) {
	comp := getTestGPUCompressor(t)

	tests := []struct {
		name string
		data []byte
	}{
		{"EmptySlice", []byte{}},
		{"SingleByte", []byte{0x7F}},
		{"TwoBytes", []byte{0x12, 0x34}},
		{"ThreeBytes", []byte{0xAA, 0xBB, 0xCC}},
		{"NilSlice", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matches, _, err := comp.MatchLZ77(tc.data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed on short input %s: %v", tc.name, err)
			}
			if len(matches) != 0 {
				t.Fatalf("Expected 0 matches for input len %d, got %d", len(tc.data), len(matches))
			}
			assertGPUMatchIntegrity(t, tc.data, matches)
		})
	}
}

// Boundary 2: Minimum Match Boundary (exact 4-8 bytes)
func TestTier2_GPU_B02_MinMatchBoundary(t *testing.T) {
	comp := getTestGPUCompressor(t)

	matchLens := []int{4, 5, 6, 7, 8}

	for _, mLen := range matchLens {
		t.Run(fmt.Sprintf("ExactLen_%d", mLen), func(t *testing.T) {
			pattern := []byte("ABCDEFGH"[:mLen])
			data := make([]byte, 1024)
			copy(data[0:], pattern)
			copy(data[256:], pattern)

			matches, _, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed: %v", err)
			}
			assertGPUMatchIntegrity(t, data, matches)

			// Roundtrip through GPU pipeline
			opts := przm.CompressOptions{GPUAcceleration: true}
			c, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("Compress failed: %v", err)
			}
			dec, err := przm.Decode(c)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("Roundtrip mismatch")
			}
		})
	}
}

// Boundary 3: Tile Boundary at 32 KiB
func TestTier2_GPU_B03_TileBoundary_32KB(t *testing.T) {
	comp := getTestGPUCompressor(t)

	sizes := []struct {
		name string
		size int
	}{
		{"TileMinus4", 32768 - 4},
		{"TileMinus1", 32768 - 1},
		{"ExactTile_32KB", 32768},
		{"TilePlus1", 32768 + 1},
		{"TilePlus4", 32768 + 4},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			pat := []byte("TILE_BOUNDARY_0123456789ABCDEF_")
			data := bytes.Repeat(pat, (tc.size/len(pat))+1)[:tc.size]
			matches, _, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed: %v", err)
			}
			assertGPUMatchIntegrity(t, data, matches)
		})
	}
}

// Boundary 4: Multi-Tile Boundary at 64 KiB
func TestTier2_GPU_B04_MultiTileBoundary_64KB(t *testing.T) {
	comp := getTestGPUCompressor(t)

	sizes := []struct {
		name string
		size int
	}{
		{"DualTileMinus1", 65536 - 1},
		{"ExactDualTile_64KB", 65536},
		{"DualTilePlus1", 65536 + 1},
		{"CrossTileMatch_32K", 65536 + 16},
		{"DualTilePlus128", 65536 + 128},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Repeat([]byte("DUAL_TILE_BOUNDARY_PATTERN_64K!_"), (tc.size/32)+1)[:tc.size]
			matches, _, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed: %v", err)
			}
			assertGPUMatchIntegrity(t, data, matches)
		})
	}
}

// Boundary 5: Window Boundary at 1 MiB
func TestTier2_GPU_B05_WindowBoundary_1MB(t *testing.T) {
	comp := getTestGPUCompressor(t)

	tests := []struct {
		name string
		size int
	}{
		{"WindowMinus1K", 1024*1024 - 1024},
		{"WindowMinus1B", 1024*1024 - 1},
		{"ExactWindow_1MB", 1024 * 1024},
		{"WindowPlus1B", 1024*1024 + 1},
		{"WindowPlus1K", 1024*1024 + 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := bytes.Repeat([]byte("1MB_WINDOW_BOUNDARY_TEST_PATTERN"), (tc.size/32)+1)[:tc.size]
			matches, _, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed: %v", err)
			}
			assertGPUMatchIntegrity(t, data, matches)
		})
	}
}

// Boundary 6: Max Match Length (up to 4095 bytes)
func TestTier2_GPU_B06_MaxMatchLength(t *testing.T) {
	comp := getTestGPUCompressor(t)

	runLens := []int{256, 1024, 4094, 4095, 8192}

	for _, rLen := range runLens {
		t.Run(fmt.Sprintf("RunLen_%d", rLen), func(t *testing.T) {
			data := bytes.Repeat([]byte{'X'}, rLen)
			matches, _, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed on %d run: %v", rLen, err)
			}
			assertGPUMatchIntegrity(t, data, matches)
			for _, m := range matches {
				if m.Length > gpu.MaxMatchLen32 {
					t.Fatalf("Match length %d exceeds MaxMatchLen32 (%d)", m.Length, gpu.MaxMatchLen32)
				}
			}
		})
	}
}

// Boundary 7: Max Offset Limits
func TestTier2_GPU_B07_MaxOffset(t *testing.T) {
	tests := []struct {
		name   string
		offset uint32
	}{
		{"Offset1", 1},
		{"Offset256", 256},
		{"Offset64K", 65535},
		{"OffsetMax32", gpu.MaxOffset32},
		{"OffsetAboveMax", gpu.MaxOffset32 + 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packed := gpu.PackMatch32(10, tc.offset)
			l, o := gpu.UnpackMatch32(packed)
			if l != 10 {
				t.Fatalf("Length unpacked mismatch: want 10, got %d", l)
			}
			expectedOffset := tc.offset & gpu.MaxOffset32
			if o != expectedOffset {
				t.Fatalf("Offset unpacked mismatch: want %d, got %d", expectedOffset, o)
			}
		})
	}
}

// Boundary 8: Incompressible High-Entropy Payloads
func TestTier2_GPU_B08_IncompressibleHighEntropy(t *testing.T) {
	sizes := []struct {
		name string
		size int
	}{
		{"Rand1KB", 1024},
		{"Rand16KB", 16 * 1024},
		{"Rand64KB", 64 * 1024},
		{"Rand256KB", 256 * 1024},
		{"Rand1MB", 1024 * 1024},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, tc.size)
			_, _ = rand.Read(buf)

			opts := przm.CompressOptions{GPUAcceleration: true}
			comp, err := przm.CompressWithOptions(buf, opts)
			if err != nil {
				t.Fatalf("Compression of random data failed: %v", err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if !bytes.Equal(dec, buf) {
				t.Fatalf("Random payload roundtrip mismatch")
			}
		})
	}
}

// Boundary 9: Degenerate Data Distributions
func TestTier2_GPU_B09_DegenerateDistributions(t *testing.T) {
	tests := []struct {
		name string
		gen  func() []byte
	}{
		{"AllZeroes_64KB", func() []byte { return make([]byte, 65536) }},
		{"AllOnes_64KB", func() []byte { return bytes.Repeat([]byte{0xFF}, 65536) }},
		{"AlternatingBytes_64KB", func() []byte { return bytes.Repeat([]byte{0x00, 0xFF}, 32768) }},
		{"Cycle256_64KB", func() []byte {
			b := make([]byte, 65536)
			for i := range b {
				b[i] = byte(i % 256)
			}
			return b
		}},
		{"SingleByteRun_1MB", func() []byte { return bytes.Repeat([]byte{'A'}, 1024*1024) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.gen()
			opts := przm.CompressOptions{GPUAcceleration: true}
			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("Compression failed: %v", err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("Roundtrip mismatch on degenerate pattern %s", tc.name)
			}
		})
	}
}

// Boundary 10: Descriptor Packing Limits
func TestTier2_GPU_B10_DescriptorPackingLimits(t *testing.T) {
	tests := []struct {
		name       string
		len        uint16
		off        uint32
		expectZero bool
	}{
		{"SubMinLen_3", 3, 100, true},
		{"SubMinLen_0", 0, 100, true},
		{"ZeroOffset", 10, 0, true},
		{"MinMatchLen_4", 4, 1, false},
		{"MaxLenClamping", 5000, 1000, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packed := gpu.PackMatch32(tc.len, tc.off)
			if tc.expectZero {
				if packed != 0 {
					t.Fatalf("Expected 0 descriptor for invalid input, got 0x%x", packed)
				}
				if gpu.IsMatch32(packed) {
					t.Fatalf("IsMatch32 returned true for zero descriptor")
				}
			} else {
				if !gpu.IsMatch32(packed) {
					t.Fatalf("Expected IsMatch32 == true")
				}
				l, o := gpu.UnpackMatch32(packed)
				expectedLen := min(tc.len, gpu.MaxMatchLen32)
				if l != expectedLen || o != tc.off {
					t.Fatalf("Unpack mismatch: want %d/%d, got %d/%d", expectedLen, tc.off, l, o)
				}
			}
		})
	}
}
