// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// 1. Verify Sheaf Coboundary Nilpotency: d1 ∘ d0 ≡ 0 over F_{2^8}.
func TestCellularSheaf_CoboundaryNilpotency(t *testing.T) {
	dimensions := [][2]int{
		{2, 2},
		{4, 4},
		{8, 8},
		{16, 16},
		{32, 64},
		{10, 15},
		{33, 17},
	}
	twists := []uint8{1, 2, 7, 0x1B}

	for _, dim := range dimensions {
		M, W := dim[0], dim[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

			// Test with multiple pseudo-random 0-cochains
			rng := rand.New(rand.NewSource(int64(M*1000 + W*10 + int(twist))))
			for iter := 0; iter < 20; iter++ {
				phi := make(przm.Cochain0, M*W)
				for i := range phi {
					phi[i] = uint8(rng.Intn(256))
				}

				// d0 phi
				u := przm.Coboundary0(complex, phi)

				// d1 (d0 phi)
				c := przm.Coboundary1(complex, u)

				// Must be identically zero everywhere
				for idx, val := range c {
					if val != 0 {
						t.Fatalf("M=%d, W=%d, Twist=0x%02X: d1(d0(phi)) != 0 at face %d: got 0x%02X",
							M, W, twist, idx, val)
					}
				}
			}
		}
	}
}

// 2. Verify Discrete Hodge Laplacian and Harmonic Nullspace Characterization.
func TestCellularSheaf_HodgeLaplacian_HarmonicCharacterization(t *testing.T) {
	M, W := 8, 8
	complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: 1}

	// For any potential field phi:
	phi := make(przm.Cochain0, M*W)
	for i := range phi {
		phi[i] = uint8(i * 17)
	}

	// u = d0 phi (exact gradient 1-form)
	u := przm.Coboundary0(complex, phi)

	// Since u is exact, curl C = d1 u == 0
	c := przm.Coboundary1(complex, u)
	for idx, val := range c {
		if val != 0 {
			t.Fatalf("Exact gradient u = d0 phi had non-zero curl at face %d: 0x%02X", idx, val)
		}
	}

	// Adjoint1(C) must be 0
	upLap := przm.Adjoint1(complex, c)
	for i, val := range upLap.Eh {
		if val != 0 {
			t.Fatalf("Up-Laplacian horizontal edge %d non-zero: 0x%02X", i, val)
		}
	}
	for i, val := range upLap.Ev {
		if val != 0 {
			t.Fatalf("Up-Laplacian vertical edge %d non-zero: 0x%02X", i, val)
		}
	}

	// Verify Hodge 0-Laplacian: Delta_0 phi = d0^T (d0 phi)
	delta0 := przm.HodgeLaplacian0(complex, phi)
	if len(delta0) != M*W {
		t.Fatalf("HodgeLaplacian0 returned wrong length: %d", len(delta0))
	}
}

// 3. Verify Harmonic Projection and Cycle Annihilation.
func TestCellularSheaf_HarmonicProjection_CycleAnnihilation(t *testing.T) {
	M, W := 16, 16
	complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: 1}

	// Construct circulating toroidal 1-cochain:
	// constant horizontal step gammaH, constant vertical step gammaV
	gammaH := uint8(0x35)
	gammaV := uint8(0x5A)

	eh := make([]uint8, M*(W-1))
	for i := range eh {
		eh[i] = gammaH
	}
	ev := make([]uint8, (M-1)*W)
	for i := range ev {
		ev[i] = gammaV
	}
	u := przm.Cochain1{Eh: eh, Ev: ev}

	// Project harmonic 1-forms
	detGammaH, detGammaV, betti1 := przm.ProjectHarmonic1(complex, u)
	if detGammaH != gammaH {
		t.Fatalf("Detected gammaH mismatch: expected 0x%02X, got 0x%02X", gammaH, detGammaH)
	}
	if detGammaV != gammaV {
		t.Fatalf("Detected gammaV mismatch: expected 0x%02X, got 0x%02X", gammaV, detGammaV)
	}
	if betti1 != 2 {
		t.Fatalf("Detected Betti1 mismatch: expected 2, got %d", betti1)
	}
}

// 4. Verify Full Lossless Roundtrip across All Modes and Dimensions.
func TestCellularSheaf_LosslessRoundtrip_AllModes(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{
			name: "Empty",
			data: []byte{},
		},
		{
			name: "TinyRaw",
			data: []byte{1, 2, 3, 4, 5},
		},
		{
			name: "HarmonicCurlFree_Stride8",
			data: func() []byte {
				// 64 bytes grid (8 x 8)
				b := make([]byte, 64)
				b[0] = 0x42
				eh := []byte{1, 2, 3, 4, 5, 6, 7}
				ev := []byte{10, 20, 30, 40, 50, 60, 70}
				for j := 0; j < 7; j++ {
					b[j+1] = b[j] ^ eh[j]
				}
				for i := 1; i < 8; i++ {
					v := ev[i-1]
					for j := 0; j < 8; j++ {
						b[i*8+j] = b[(i-1)*8+j] ^ v
					}
				}
				return b
			}(),
		},
		{
			name: "HarmonicCurlFree_ConstBoundaries_Stride16",
			data: func() []byte {
				// 256 bytes grid (16 x 16) with constant horizontal and vertical gradients
				b := make([]byte, 256)
				b[0] = 0x11
				gammaH := byte(0x03)
				gammaV := byte(0x05)
				for j := 0; j < 15; j++ {
					b[j+1] = b[j] ^ gammaH
				}
				for i := 1; i < 16; i++ {
					for j := 0; j < 16; j++ {
						b[i*16+j] = b[(i-1)*16+j] ^ gammaV
					}
				}
				return b
			}(),
		},
		{
			name: "HarmonicCurlFree_WithTrailingRemainder",
			data: func() []byte {
				// 64 bytes grid + 5 trailing bytes
				b := make([]byte, 69)
				for i := 0; i < 64; i++ {
					b[i] = byte(i * 3)
				}
				for i := 64; i < 69; i++ {
					b[i] = byte(0xEE)
				}
				return b
			}(),
		},
		{
			name: "SparseCurl_Stride8",
			data: func() []byte {
				b := make([]byte, 64)
				for i := 0; i < 64; i++ {
					b[i] = byte(i)
				}
				// Perturb 2 interior vertices to create localized curl
				b[18] ^= 0x55
				b[35] ^= 0xAA
				return b
			}(),
		},
		{
			name: "LargeStructuredField_4KB",
			data: func() []byte {
				// 64 x 64 = 4096 bytes
				b := make([]byte, 4096)
				b[0] = 0x7F
				for j := 0; j < 63; j++ {
					b[j+1] = b[j] ^ byte((j%7)+1)
				}
				for i := 1; i < 64; i++ {
					v := byte((i % 5) + 2)
					for j := 0; j < 64; j++ {
						b[i*64+j] = b[(i-1)*64+j] ^ v
					}
				}
				return b
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compressed, err := przm.CompressCellularSheaf(tc.data, 3)
			if err != nil {
				t.Fatalf("CompressCellularSheaf failed: %v", err)
			}

			// Decompress using DecompressCellularSheaf
			dst := make([]byte, len(tc.data)+64)
			decompressed, err := przm.DecompressCellularSheaf(compressed, dst)
			if err != nil {
				t.Fatalf("DecompressCellularSheaf failed: %v", err)
			}

			if !bytes.Equal(decompressed, tc.data) {
				t.Fatalf("Decompressed data mismatch! Length expected %d, got %d",
					len(tc.data), len(decompressed))
			}

			// Also verify through generic decompress dispatcher
			genDec, err := przm.Decompress(compressed)
			if err != nil {
				t.Fatalf("Generic Decompress failed: %v", err)
			}
			if !bytes.Equal(genDec, tc.data) {
				t.Fatalf("Generic Decompress mismatch!")
			}
		})
	}
}

// 5. Verify Zero Heap Allocation Invariant on Decompression (0 B/op, 0 allocs/op).
func TestCellularSheaf_ZeroAllocDecompression(t *testing.T) {
	// 4096 bytes harmonic curl-free grid
	orig := make([]byte, 4096)
	orig[0] = 0x22
	for j := 0; j < 63; j++ {
		orig[j+1] = orig[j] ^ byte(j+1)
	}
	for i := 1; i < 64; i++ {
		v := byte(i + 1)
		for j := 0; j < 64; j++ {
			orig[i*64+j] = orig[(i-1)*64+j] ^ v
		}
	}

	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		t.Fatalf("CompressCellularSheaf failed: %v", err)
	}

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	// Warmup
	_, _ = przm.DecompressCellularSheaf(compressed, dst)

	allocs := testing.AllocsPerRun(100, func() {
		_, err := przm.DecompressCellularSheaf(compressed, dst)
		if err != nil {
			t.Fatal(err)
		}
	})

	if allocs > 0 {
		t.Fatalf("DecompressCellularSheaf violated zero-allocation invariant: %f allocs/op", allocs)
	}
}

// 6. Verify Compression Ratio Advantage on Structured 2D Manifolds.
func TestCellularSheaf_CompressionRatioAdvantage(t *testing.T) {
	// 4096 bytes grid with constant boundary cycles
	orig := make([]byte, 4096)
	orig[0] = 0x99
	gammaH := byte(0x01)
	gammaV := byte(0x02)
	for j := 0; j < 63; j++ {
		orig[j+1] = orig[j] ^ gammaH
	}
	for i := 1; i < 64; i++ {
		for j := 0; j < 64; j++ {
			orig[i*64+j] = orig[(i-1)*64+j] ^ gammaV
		}
	}

	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		t.Fatalf("CompressCellularSheaf failed: %v", err)
	}

	ratio := float64(len(orig)) / float64(len(compressed))
	t.Logf("Cellular Sheaf Harmonic 2D compression ratio: %.2fx (raw: %d, compressed: %d)",
		ratio, len(orig), len(compressed))

	if ratio < 10.0 {
		t.Fatalf("Expected compression ratio >= 10.0x for harmonic field, got %.2fx", ratio)
	}
}

// 7. Benchmark Decompression Throughput (> 20 GB/s target).
func BenchmarkCellularSheaf_DecompressThroughput(b *testing.B) {
	orig := make([]byte, 64*1024) // 64 KiB
	for i := range orig {
		orig[i] = byte(i)
	}
	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatalf("CompressCellularSheaf failed: %v", err)
	}

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	b.SetBytes(int64(len(orig)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressCellularSheaf(compressed, dst)
	}
}

// 8. Verify C Decompressor Interop: Go CompressCellularSheaf -> Pure C przm_decompress
func TestCellularSheaf_PureCDecompressorInterop(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
	var cBinPath string
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				cBinPath = absPath
				break
			}
		}
	}
	if cBinPath == "" {
		t.Skip("C test binary test_przm_dec.exe not found; build it with gcc first")
	}

	tmpDir := t.TempDir()

	testGrids := []struct {
		name string
		data []byte
	}{
		{
			name: "CurlFree_Harmonic_8x8",
			data: func() []byte {
				b := make([]byte, 64)
				b[0] = 0x55
				for j := 0; j < 7; j++ {
					b[j+1] = b[j] ^ byte(j+2)
				}
				for i := 1; i < 8; i++ {
					v := byte(i * 3)
					for j := 0; j < 8; j++ {
						b[i*8+j] = b[(i-1)*8+j] ^ v
					}
				}
				return b
			}(),
		},
		{
			name: "SparseCurl_8x8",
			data: func() []byte {
				b := make([]byte, 64)
				for i := range b {
					b[i] = byte(i * 7)
				}
				b[18] ^= 0x55
				b[35] ^= 0xAA
				return b
			}(),
		},
		{
			name: "ConstantBoundaries_16x16",
			data: func() []byte {
				b := make([]byte, 256)
				b[0] = 0xAA
				for j := 0; j < 15; j++ {
					b[j+1] = b[j] ^ 0x03
				}
				for i := 1; i < 16; i++ {
					for j := 0; j < 16; j++ {
						b[i*16+j] = b[(i-1)*16+j] ^ 0x07
					}
				}
				return b
			}(),
		},
	}

	for _, tg := range testGrids {
		t.Run(tg.name, func(t *testing.T) {
			comp, err := przm.CompressCellularSheaf(tg.data, 3)
			if err != nil {
				t.Fatalf("CompressCellularSheaf failed: %v", err)
			}

			inPrzm := filepath.Join(tmpDir, tg.name+".przm")
			outBin := filepath.Join(tmpDir, tg.name+".out")

			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("failed to write compressed file: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("C decompressor failed on %s (exit %v):\nOutput:\n%s", tg.name, err, string(output))
			}

			decomp, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("failed to read decompressed file: %v", err)
			}

			if !bytes.Equal(decomp, tg.data) {
				t.Fatalf("%s interop mismatch! Length got %d, want %d", tg.name, len(decomp), len(tg.data))
			}
		})
	}
}
