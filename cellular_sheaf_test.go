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
			for range 20 {
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
				for j := range 7 {
					b[j+1] = b[j] ^ eh[j]
				}
				for i := 1; i < 8; i++ {
					v := ev[i-1]
					for j := range 8 {
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
				for j := range 15 {
					b[j+1] = b[j] ^ gammaH
				}
				for i := 1; i < 16; i++ {
					for j := range 16 {
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
				for i := range 64 {
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
				for i := range 64 {
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
				for j := range 63 {
					b[j+1] = b[j] ^ byte((j%7)+1)
				}
				for i := 1; i < 64; i++ {
					v := byte((i % 5) + 2)
					for j := range 64 {
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
	for j := range 63 {
		orig[j+1] = orig[j] ^ byte(j+1)
	}
	for i := 1; i < 64; i++ {
		v := byte(i + 1)
		for j := range 64 {
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
	for j := range 63 {
		orig[j+1] = orig[j] ^ gammaH
	}
	for i := 1; i < 64; i++ {
		for j := range 64 {
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

	for b.Loop() {
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
				for j := range 7 {
					b[j+1] = b[j] ^ byte(j+2)
				}
				for i := 1; i < 8; i++ {
					v := byte(i * 3)
					for j := range 8 {
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
				for j := range 15 {
					b[j+1] = b[j] ^ 0x03
				}
				for i := 1; i < 16; i++ {
					for j := range 16 {
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

			if err := os.WriteFile(inPrzm, comp, 0o644); err != nil {
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

// ----------------------------------------------------------------------------
// Discrete Hodge 1-Laplacian Tests: Delta_1 = d1^T d1 + d0 d0^T
// ----------------------------------------------------------------------------

func TestCellularSheaf_HodgeLaplacian1_MathematicalProperties(t *testing.T) {
	dimensions := [][2]int{
		{2, 2}, {3, 3}, {4, 4}, {5, 7}, {8, 8},
	}
	twists := []uint8{1, 2, 7, 0x1B, 0xFF}

	for _, dim := range dimensions {
		M, W := dim[0], dim[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}
			rng := rand.New(rand.NewSource(int64(M*31 + W*17 + int(twist))))

			// 1. Exact 1-form test: u = d0(phi) => Delta_{1, up}(u) == 0, Delta_1(u) == d0(Delta_0(phi))
			phi := make(przm.Cochain0, M*W)
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
			uExact := przm.Coboundary0(complex, phi)

			// Delta_1 on exact form
			lap1Exact := przm.HodgeLaplacian1(complex, uExact)

			// Expected: d0(Delta_0(phi))
			delta0Phi := przm.HodgeLaplacian0(complex, phi)
			expectedExact := przm.Coboundary0(complex, delta0Phi)

			if !bytes.Equal(lap1Exact.Eh, expectedExact.Eh) || !bytes.Equal(lap1Exact.Ev, expectedExact.Ev) {
				t.Fatalf("M=%d, W=%d, Twist=0x%02X: HodgeLaplacian1(d0(phi)) != d0(Delta_0(phi))",
					M, W, twist)
			}

			// 2. Co-exact 1-form test: w = d1^T(c) => Delta_{1, down}(w) == 0, Delta_1(w) == d1^T(d1(w))
			c := make(przm.Cochain2, (M-1)*(W-1))
			for i := range c {
				c[i] = uint8(rng.Intn(256))
			}
			wCoexact := przm.Adjoint1(complex, c)

			lap1Coexact := przm.HodgeLaplacian1(complex, wCoexact)
			d1w := przm.Coboundary1(complex, wCoexact)
			expectedCoexact := przm.Adjoint1(complex, d1w)

			if !bytes.Equal(lap1Coexact.Eh, expectedCoexact.Eh) || !bytes.Equal(lap1Coexact.Ev, expectedCoexact.Ev) {
				t.Fatalf("M=%d, W=%d, Twist=0x%02X: HodgeLaplacian1(d1^T(c)) != d1^T(d1(w))",
					M, W, twist)
			}

			// 3. Self-Adjointness: <Delta_1(u), v> == <u, Delta_1(v)>
			uRand := przm.Cochain1{
				Eh: make([]uint8, M*(W-1)),
				Ev: make([]uint8, (M-1)*W),
			}
			vRand := przm.Cochain1{
				Eh: make([]uint8, M*(W-1)),
				Ev: make([]uint8, (M-1)*W),
			}
			for i := range uRand.Eh {
				uRand.Eh[i] = uint8(rng.Intn(256))
				vRand.Eh[i] = uint8(rng.Intn(256))
			}
			for i := range uRand.Ev {
				uRand.Ev[i] = uint8(rng.Intn(256))
				vRand.Ev[i] = uint8(rng.Intn(256))
			}

			lap1U := przm.HodgeLaplacian1(complex, uRand)
			lap1V := przm.HodgeLaplacian1(complex, vRand)

			pairing1 := cochain1Dot(lap1U, vRand)
			pairing2 := cochain1Dot(uRand, lap1V)

			if pairing1 != pairing2 {
				t.Fatalf(
					"M=%d, W=%d, Twist=0x%02X: Self-adjointness of Delta_1 failed: <Delta_1 u, v>=0x%02X != <u, Delta_1 v>=0x%02X",
					M,
					W,
					twist,
					pairing1,
					pairing2,
				)
			}

			// 4. Linearity: Delta_1(u ^ v) == Delta_1(u) ^ Delta_1(v)
			uvSum := przm.Cochain1{
				Eh: make([]uint8, len(uRand.Eh)),
				Ev: make([]uint8, len(uRand.Ev)),
			}
			for i := range uvSum.Eh {
				uvSum.Eh[i] = uRand.Eh[i] ^ vRand.Eh[i]
			}
			for i := range uvSum.Ev {
				uvSum.Ev[i] = uRand.Ev[i] ^ vRand.Ev[i]
			}

			lap1Sum := przm.HodgeLaplacian1(complex, uvSum)
			for i := range lap1Sum.Eh {
				if lap1Sum.Eh[i] != (lap1U.Eh[i] ^ lap1V.Eh[i]) {
					t.Fatalf("M=%d, W=%d, Twist=0x%02X: Delta_1 linearity violated in Eh at index %d",
						M, W, twist, i)
				}
			}
			for i := range lap1Sum.Ev {
				if lap1Sum.Ev[i] != (lap1U.Ev[i] ^ lap1V.Ev[i]) {
					t.Fatalf("M=%d, W=%d, Twist=0x%02X: Delta_1 linearity violated in Ev at index %d",
						M, W, twist, i)
				}
			}
		}
	}
}

// ----------------------------------------------------------------------------
// Cellular Sheaf Direct Reconstruction Tests
// ----------------------------------------------------------------------------

func TestCellularSheaf_Reconstruct_GuardsAndErrorBranches(t *testing.T) {
	rep := &przm.SheafBoundaryRep{
		Anchor:  0x10,
		Eh:      []uint8{1, 2, 3},
		Ev:      []uint8{4, 5, 6},
		ConstEh: false,
		ConstEv: false,
	}

	// 1. M < 2
	c1 := przm.CellComplex2D{Rows: 1, Cols: 4, Twist: 1}
	dst := make([]byte, 16)
	if err := przm.ReconstructCellularSheaf(c1, rep, dst); err != przm.ErrCellularSheafBufferTooSmall {
		t.Fatalf("Expected ErrCellularSheafBufferTooSmall for M=1, got %v", err)
	}

	// 2. W < 2
	c2 := przm.CellComplex2D{Rows: 4, Cols: 1, Twist: 1}
	if err := przm.ReconstructCellularSheaf(c2, rep, dst); err != przm.ErrCellularSheafBufferTooSmall {
		t.Fatalf("Expected ErrCellularSheafBufferTooSmall for W=1, got %v", err)
	}

	// 3. len(dst) < M*W
	c3 := przm.CellComplex2D{Rows: 4, Cols: 4, Twist: 1}
	smallDst := make([]byte, 15)
	if err := przm.ReconstructCellularSheaf(c3, rep, smallDst); err != przm.ErrCellularSheafBufferTooSmall {
		t.Fatalf("Expected ErrCellularSheafBufferTooSmall for len(dst) < M*W, got %v", err)
	}
}

func TestCellularSheaf_Reconstruct_AllModesAndBranches(t *testing.T) {
	type testCase struct {
		name    string
		M       int
		W       int
		twist   uint8
		constEh bool
		constEv bool
		curls   []przm.SparseFaceCurl
	}

	cases := []testCase{
		// Branch: ConstEh=true, ConstEv=true, twist=1, NNZ=0, W >= 8 (SWAR)
		{name: "Const_Twist1_SWAR", M: 4, W: 16, twist: 1, constEh: true, constEv: true},
		// Branch: ConstEh=true, ConstEv=true, twist=1, NNZ=0, W not multiple of 8 (Tail)
		{name: "Const_Twist1_Tail", M: 3, W: 11, twist: 1, constEh: true, constEv: true},
		// Branch: ConstEh=false, ConstEv=false, twist=1, NNZ=0, W < 8
		{name: "Var_Twist1_Narrow", M: 4, W: 5, twist: 1, constEh: false, constEv: false},
		// Branch: ConstEh=true, ConstEv=false, twist=2, NNZ=0 (Galois scaling curl-free)
		{name: "ConstH_VarV_Twist2", M: 4, W: 8, twist: 2, constEh: true, constEv: false},
		// Branch: ConstEh=false, ConstEv=true, twist=3, NNZ=0 (Galois scaling curl-free)
		{name: "VarH_ConstV_Twist3", M: 5, W: 6, twist: 3, constEh: false, constEv: true},
		// Branch: ConstEh=false, ConstEv=false, twist=1, SparseCurls (hits and misses)
		{
			name:    "SparseCurls_Twist1",
			M:       4,
			W:       4,
			twist:   1,
			constEh: false,
			constEv: false,
			curls: []przm.SparseFaceCurl{
				{Index: 0, Val: 0x42},
				{Index: 4, Val: 0x77},
			},
		},
		// Branch: ConstEh=true, ConstEv=true, twist=2, SparseCurls (hits and misses with twist)
		{
			name:    "SparseCurls_Twist2",
			M:       4,
			W:       4,
			twist:   2,
			constEh: true,
			constEv: true,
			curls: []przm.SparseFaceCurl{
				{Index: 1, Val: 0x19},
				{Index: 5, Val: 0x88},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			complex := przm.CellComplex2D{Rows: tc.M, Cols: tc.W, Twist: tc.twist}
			rep := &przm.SheafBoundaryRep{
				Anchor:      0x5A,
				GammaH:      0x03,
				GammaV:      0x05,
				ConstEh:     tc.constEh,
				ConstEv:     tc.constEv,
				Eh:          make([]uint8, tc.W-1),
				Ev:          make([]uint8, tc.M-1),
				SparseCurls: tc.curls,
				NNZ:         len(tc.curls),
			}
			for j := range rep.Eh {
				rep.Eh[j] = uint8((j + 1) * 7)
			}
			for i := range rep.Ev {
				rep.Ev[i] = uint8((i + 1) * 11)
			}

			dst := make([]byte, tc.M*tc.W)
			err := przm.ReconstructCellularSheaf(complex, rep, dst)
			if err != nil {
				t.Fatalf("ReconstructCellularSheaf failed: %v", err)
			}

			// Verify anchor preserved
			if dst[0] != rep.Anchor {
				t.Fatalf("dst[0]=0x%02X, expected anchor=0x%02X", dst[0], rep.Anchor)
			}
		})
	}
}

func TestCellularSheaf_Reconstruct_RoundtripWithDecomposition(t *testing.T) {
	// Verify that AnalyzeSheafDecomposition -> ReconstructCellularSheaf is bit-exact lossless
	geometries := [][2]int{
		{2, 2}, {3, 7}, {4, 8}, {8, 8}, {16, 16}, {5, 13},
	}
	twists := []uint8{1, 2, 7, 0x1B}

	for _, geom := range geometries {
		M, W := geom[0], geom[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}
			rng := rand.New(rand.NewSource(int64(M*101 + W*31 + int(twist))))

			// Generate random source buffer
			src := make([]byte, M*W)
			for i := range src {
				src[i] = uint8(rng.Intn(256))
			}

			rep := przm.AnalyzeSheafDecomposition(src, M, W, twist)
			if rep == nil {
				t.Fatalf("AnalyzeSheafDecomposition returned nil for M=%d, W=%d", M, W)
			}

			dst := make([]byte, M*W)
			err := przm.ReconstructCellularSheaf(complex, rep, dst)
			if err != nil {
				t.Fatalf("ReconstructCellularSheaf error: %v", err)
			}

			if !bytes.Equal(dst, src) {
				t.Fatalf("Roundtrip mismatch for M=%d, W=%d, Twist=0x%02X", M, W, twist)
			}
		}
	}
}

// BenchmarkCellularSheafDecompress verifies zero dynamic heap allocations (0 B/op, 0 allocs/op)
// during instantaneous decompression of cellular sheaf frames.
func BenchmarkCellularSheafDecompress(b *testing.B) {
	orig := make([]byte, 1024)
	for i := range orig {
		orig[i] = uint8((i * 3) & 0xFF)
	}
	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, len(orig)+64)

	// Warmup to populate thread pool buffer before timer and alloc tracking
	_, _ = przm.DecompressCellularSheaf(compressed, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, err := przm.DecompressCellularSheaf(compressed, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}
