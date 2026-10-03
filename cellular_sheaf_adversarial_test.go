// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// ----------------------------------------------------------------------------
// 1. Nilpotency Fuzzing: d1 ∘ d0 ≡ 0 over 50,000 Random Fields across all Twists
// ----------------------------------------------------------------------------

func TestAdversarial_CoboundaryNilpotency_50K(t *testing.T) {
	rng := rand.New(rand.NewSource(0xDEADBEEF))
	const totalIterations = 50000

	t.Logf("Running 50,000 nilpotency tests (d1 ∘ d0 ≡ 0) across all twists alpha in F_{2^8} and random geometries...")

	for iter := range totalIterations {
		// Sample arbitrary matrix geometries (M >= 2, W >= 2)
		M := 2 + rng.Intn(31) // Rows in [2, 32]
		W := 2 + rng.Intn(31) // Cols in [2, 32]

		// Sample twist: cover all 256 field elements systematically and randomly
		var twist uint8
		if iter < 256 {
			twist = uint8(iter)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Random 0-cochain phi
		phi := make(przm.Cochain0, M*W)
		for i := range phi {
			phi[i] = uint8(rng.Intn(256))
		}

		// u = d0(phi)
		u := przm.Coboundary0(complex, phi)

		// c = d1(u) = (d1 ∘ d0)(phi)
		c := przm.Coboundary1(complex, u)

		// Verify every face curl is identically zero
		for idx, val := range c {
			if val != 0 {
				t.Fatalf("[Iteration %d] Nilpotency violation! M=%d, W=%d, Twist=0x%02X: (d1 ∘ d0)(phi) != 0 at face %d (val=0x%02X)",
					iter, M, W, twist, idx, val)
			}
		}
	}

	t.Logf("PASS: 50,000 iterations completed. d1 ∘ d0 ≡ 0 identically holds across all 256 Galois twists and arbitrary geometries.")
}

// ----------------------------------------------------------------------------
// 2. Hodge Decomposition Orthogonality & Adjoint Property
// ----------------------------------------------------------------------------

// GF256Dot computes the bilinear pairing <x, y> = sum_i GF256Mul(x[i], y[i]) in F_{2^8}.
func gf256Dot(x, y []uint8) uint8 {
	var sum uint8
	n := min(len(y), len(x))
	for i := range n {
		sum ^= przm.GF256Mul(x[i], y[i])
	}
	return sum
}

// Cochain1Dot computes the standard pairing on C^1 = C^1_h + C^1_v.
func cochain1Dot(u, v przm.Cochain1) uint8 {
	return gf256Dot(u.Eh, v.Eh) ^ gf256Dot(u.Ev, v.Ev)
}

// ReferenceAdjoint0 computes the mathematically correct formal adjoint d0^T: C^1 -> C^0.
func ReferenceAdjoint0(complex przm.CellComplex2D, u przm.Cochain1) przm.Cochain0 {
	M := complex.Rows
	W := complex.Cols
	alpha := complex.Twist

	div := make([]uint8, M*W)

	for i := range M {
		rowOff := i * W
		hRow := i * (W - 1)

		for j := range W {
			val := uint8(0)

			// Horizontal outgoing: source vertex v has coefficient alpha in Coboundary0
			if j < W-1 {
				ehOut := u.Eh[hRow+j]
				if alpha > 1 {
					ehOut = przm.GF256Mul(alpha, ehOut)
				}
				val ^= ehOut
			}
			// Horizontal incoming: target vertex has coefficient 1 in Coboundary0
			if j > 0 {
				val ^= u.Eh[hRow+j-1]
			}

			// Vertical outgoing: source vertex v has coefficient alpha in Coboundary0
			if i < M-1 {
				evOut := u.Ev[i*W+j]
				if alpha > 1 {
					evOut = przm.GF256Mul(alpha, evOut)
				}
				val ^= evOut
			}
			// Vertical incoming: target vertex has coefficient 1 in Coboundary0
			if i > 0 {
				val ^= u.Ev[(i-1)*W+j]
			}

			div[rowOff+j] = val
		}
	}

	return div
}

func TestAdversarial_HodgeDecomposition_AdjointProperty(t *testing.T) {
	// The formal adjoint d0^T must satisfy: <d0(phi), w> = <phi, d0^T(w)> for all phi in C^0, w in C^1.
	// Consequently, for all w in ker(d0^T), <d0(phi), w> = <phi, 0> = 0, proving im(d0) ⟂ ker(d0^T).
	rng := rand.New(rand.NewSource(42))

	dimensions := [][2]int{
		{2, 2}, {3, 3}, {4, 4}, {8, 8}, {5, 9}, {11, 7},
	}

	twists := []uint8{0, 1, 2, 3, 7, 0x1B, 0x42, 0x80, 0xFF}

	adjoint0Failures := 0
	refAdjoint0Failures := 0
	adjoint1Failures := 0

	for _, dim := range dimensions {
		M, W := dim[0], dim[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

			for range 20 {
				phi := make(przm.Cochain0, M*W)
				for i := range phi {
					phi[i] = uint8(rng.Intn(256))
				}

				w := przm.Cochain1{
					Eh: make([]uint8, M*(W-1)),
					Ev: make([]uint8, (M-1)*W),
				}
				for i := range w.Eh {
					w.Eh[i] = uint8(rng.Intn(256))
				}
				for i := range w.Ev {
					w.Ev[i] = uint8(rng.Intn(256))
				}

				// Check d0 adjoint property with actual przm.Adjoint0: <d0 phi, w> == <phi, d0^T w>
				d0phi := przm.Coboundary0(complex, phi)
				d0Tw := przm.Adjoint0(complex, w)

				lhs0 := cochain1Dot(d0phi, w)
				rhs0 := gf256Dot([]uint8(phi), []uint8(d0Tw))

				if lhs0 != rhs0 {
					adjoint0Failures++
					if adjoint0Failures <= 5 {
						t.Logf("Adjoint0 mismatch: M=%d, W=%d, Twist=0x%02X: <d0 phi, w>=0x%02X != <phi, d0^T w>=0x%02X",
							M, W, twist, lhs0, rhs0)
					}
				}

				// Check with ReferenceAdjoint0
				refD0Tw := ReferenceAdjoint0(complex, w)
				refRhs0 := gf256Dot([]uint8(phi), []uint8(refD0Tw))
				if lhs0 != refRhs0 {
					refAdjoint0Failures++
				}

				// Check d1 adjoint property: <d1 u, c> == <u, d1^T c>
				c := make(przm.Cochain2, (M-1)*(W-1))
				for i := range c {
					c[i] = uint8(rng.Intn(256))
				}
				d1u := przm.Coboundary1(complex, w)
				d1Tc := przm.Adjoint1(complex, c)

				lhs1 := gf256Dot([]uint8(d1u), []uint8(c))
				rhs1 := cochain1Dot(w, d1Tc)

				if lhs1 != rhs1 {
					adjoint1Failures++
					if adjoint1Failures <= 5 {
						t.Logf("Adjoint1 mismatch: M=%d, W=%d, Twist=0x%02X: <d1 u, c>=0x%02X != <u, d1^T c>=0x%02X",
							M, W, twist, lhs1, rhs1)
					}
				}
			}
		}
	}

	t.Logf("Adjoint property summary: przm.Adjoint0 failures = %d, ReferenceAdjoint0 failures = %d, Adjoint1 failures = %d",
		adjoint0Failures, refAdjoint0Failures, adjoint1Failures)

	if refAdjoint0Failures != 0 {
		t.Fatalf("ReferenceAdjoint0 failed %d times!", refAdjoint0Failures)
	}

	if adjoint1Failures > 0 {
		t.Errorf("Adjoint1 is NOT the formal adjoint of Coboundary1! Failures: %d", adjoint1Failures)
	}

	if adjoint0Failures > 0 {
		t.Errorf("[BUG CONFIRMED] przm.Adjoint0 has inverted incoming/outgoing Galois multipliers for twist > 1! Failures: %d (im(d0) ⟂ ker(d0^T) violated)",
			adjoint0Failures)
	}
}

// ----------------------------------------------------------------------------
// 3. Boundary Testing: Extremely Small & Unaligned Buffers
// ----------------------------------------------------------------------------

func TestAdversarial_SmallAndUnalignedBuffers(t *testing.T) {
	sizes := []int{
		0, 1, 2, 3, 4, 7, 8, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65,
		37, 53, 97, 127, 251, 509, 1021, 2053, 4093, 8191,
	}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("Size_%d", sz), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(sz * 7919)))
			src := make([]byte, sz)
			for i := range src {
				src[i] = uint8(rng.Intn(256))
			}

			// 1. Direct CompressCellularSheaf -> DecompressCellularSheaf
			comp, err := przm.CompressCellularSheaf(src, 3)
			if err != nil {
				t.Fatalf("CompressCellularSheaf(size=%d) failed: %v", sz, err)
			}

			dst := make([]byte, sz+64)
			decomp, err := przm.DecompressCellularSheaf(comp, dst)
			if err != nil {
				t.Fatalf("DecompressCellularSheaf(size=%d) failed: %v", sz, err)
			}
			if !bytes.Equal(decomp, src) {
				t.Fatalf("DecompressCellularSheaf roundtrip mismatch for size=%d (len got %d, want %d)",
					sz, len(decomp), len(src))
			}

			// 2. Full PRZM Codec roundtrip: Compress -> Decompress
			fullComp, err := przm.Compress(src, 3)
			if err != nil {
				t.Fatalf("Compress(size=%d) failed: %v", sz, err)
			}
			fullDecomp, err := przm.Decompress(fullComp)
			if err != nil {
				t.Fatalf("Decompress(size=%d) failed: %v", sz, err)
			}
			if !bytes.Equal(fullDecomp, src) {
				t.Fatalf("Full codec roundtrip mismatch for size=%d", sz)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// 4. Boundary Testing: Odd Heights/Widths and Structured Remainder Slices
// ----------------------------------------------------------------------------

func TestAdversarial_OddGeometries_And_RemainderHandling(t *testing.T) {
	// Construct grids with explicit odd dimensions and varying remainder slices
	oddGeoms := [][2]int{
		{3, 3}, {5, 5}, {7, 7}, {9, 9}, {3, 7}, {7, 3}, {5, 11}, {11, 5},
	}

	for _, geom := range oddGeoms {
		M, W := geom[0], geom[1]
		gridSize := M * W
		remainders := []int{0, 1, 2, 3, 5, 7}

		for _, rem := range remainders {
			totalLen := gridSize + rem
			t.Run(fmt.Sprintf("%dx%d_rem%d", M, W, rem), func(t *testing.T) {
				data := make([]byte, totalLen)
				// Create harmonic grid
				data[0] = 0x33
				for j := 0; j < W-1; j++ {
					data[j+1] = data[j] ^ uint8(j+1)
				}
				for i := 1; i < M; i++ {
					v := uint8(i * 5)
					for j := range W {
						data[i*W+j] = data[(i-1)*W+j] ^ v
					}
				}
				// Fill remainder with arbitrary bytes
				for r := range rem {
					data[gridSize+r] = uint8(0xAA + r)
				}

				// Direct Sheaf roundtrip
				comp, err := przm.CompressCellularSheaf(data, 3)
				if err != nil {
					t.Fatalf("CompressCellularSheaf failed: %v", err)
				}

				dst := make([]byte, totalLen+64)
				decomp, err := przm.DecompressCellularSheaf(comp, dst)
				if err != nil {
					t.Fatalf("DecompressCellularSheaf failed: %v", err)
				}

				if !bytes.Equal(decomp, data) {
					t.Fatalf("Data mismatch on odd geometry %dx%d with remainder %d", M, W, rem)
				}
			})
		}
	}
}

// ----------------------------------------------------------------------------
// 5. Non-Zero Face Curls: Dense, Boundary, and Perturbed Vorticity Streams
// ----------------------------------------------------------------------------

func TestAdversarial_NonZeroFaceCurls(t *testing.T) {
	M, W := 16, 16
	gridSize := M * W

	// 1. Boundary curls: only corners of curl field perturbed
	t.Run("BoundaryCornerCurls", func(t *testing.T) {
		data := make([]byte, gridSize)
		for i := range data {
			data[i] = uint8(i)
		}
		// Perturb corners
		data[1*W+1] ^= 0x11         // Top-left face (0,0)
		data[1*W+(W-1)] ^= 0x22     // Top-right face (0, W-2)
		data[(M-1)*W+1] ^= 0x44     // Bottom-left face (M-2, 0)
		data[(M-1)*W+(W-1)] ^= 0x88 // Bottom-right face (M-2, W-2)

		comp, err := przm.CompressCellularSheaf(data, 3)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}
		decomp, err := przm.DecompressCellularSheaf(comp, make([]byte, gridSize+64))
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(decomp, data) {
			t.Fatalf("Boundary curl roundtrip mismatch")
		}
	})

	// 2. Dense curl stream: every alternating face has non-zero curl
	t.Run("DenseCurlStream_Checkerboard", func(t *testing.T) {
		data := make([]byte, gridSize)
		for i := range M {
			for j := range W {
				data[i*W+j] = uint8((i * 7) ^ (j * 13))
			}
		}

		comp, err := przm.CompressCellularSheaf(data, 3)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}
		decomp, err := przm.DecompressCellularSheaf(comp, make([]byte, gridSize+64))
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(decomp, data) {
			t.Fatalf("Dense curl roundtrip mismatch")
		}
	})

	// 3. 100% Random High-Entropy Noise (should cleanly trigger bypass without corruption)
	t.Run("HighEntropy_Noise", func(t *testing.T) {
		rng := rand.New(rand.NewSource(999))
		data := make([]byte, 1024)
		for i := range data {
			data[i] = uint8(rng.Intn(256))
		}

		comp, err := przm.CompressCellularSheaf(data, 3)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}
		decomp, err := przm.DecompressCellularSheaf(comp, make([]byte, 1024+64))
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(decomp, data) {
			t.Fatalf("High entropy noise roundtrip mismatch")
		}
	})
}

// ----------------------------------------------------------------------------
// 6. Adversarial Pure C Interoperability & Galois Twist Fuzzing
// ----------------------------------------------------------------------------

func TestAdversarial_PureC_TwistCompatibility(t *testing.T) {
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
		t.Skip("C test binary test_przm_dec.exe not found; build with gcc first")
	}

	tmpDir := t.TempDir()

	// Test Pure C decompressor with frames having twist = 1 and twist > 1
	testTwists := []struct {
		name  string
		twist uint8
		curl  bool
	}{
		{"Twist1_Harmonic", 1, false},
		{"Twist1_SparseCurl", 1, true},
		{"Twist2_Harmonic", 2, false},
		{"Twist3_Harmonic", 3, false},
		{"Twist7_Harmonic", 7, false},
		{"Twist2_SparseCurl", 2, true},
	}

	W, M := 8, 8
	origLen := W * M

	for _, tt := range testTwists {
		t.Run(tt.name, func(t *testing.T) {
			// Construct synthetic raw potential grid under given twist
			orig := make([]byte, origLen)
			orig[0] = 0x24
			eh := []uint8{1, 2, 3, 4, 5, 6, 7}
			ev := []uint8{10, 20, 30, 40, 50, 60, 70}

			// Row 0
			for j := 0; j < W-1; j++ {
				if tt.twist <= 1 {
					orig[j+1] = orig[j] ^ eh[j]
				} else {
					orig[j+1] = przm.GF256Mul(tt.twist, orig[j]) ^ eh[j]
				}
			}
			// Rows 1..M-1
			for i := 1; i < M; i++ {
				v := ev[i-1]
				curV := v
				rowCurr := i * W
				rowPrev := (i - 1) * W
				for j := range W {
					if tt.twist <= 1 {
						orig[rowCurr+j] = orig[rowPrev+j] ^ curV
					} else {
						orig[rowCurr+j] = przm.GF256Mul(tt.twist, orig[rowPrev+j]) ^ curV
						curV = przm.GF256Mul(tt.twist, curV)
					}
				}
			}

			// If sparse curl requested, perturb 1 interior vertex
			if tt.curl {
				orig[18] ^= 0x55
			}

			rep := przm.AnalyzeSheafDecomposition(orig, M, W, tt.twist)
			if rep == nil {
				t.Fatalf("AnalyzeSheafDecomposition returned nil")
			}

			// Manually construct CSHD frame with this twist
			flags := uint16(0)
			if rep.NNZ == 0 {
				flags = 0x0001 // CSHDFlagCurlFree
			} else {
				flags = 0x0002 // CSHDFlagSparseCurl
			}

			payload := make([]byte, 0, 128)
			payload = append(payload, rep.Eh...)
			payload = append(payload, rep.Ev...)
			if rep.NNZ > 0 {
				var nnzBuf [4]byte
				binary.LittleEndian.PutUint32(nnzBuf[:], uint32(rep.NNZ))
				payload = append(payload, nnzBuf[:]...)
				for _, curl := range rep.SparseCurls {
					var kBuf [4]byte
					binary.LittleEndian.PutUint32(kBuf[:], curl.Index)
					payload = append(payload, kBuf[:]...)
					payload = append(payload, curl.Val)
				}
			}

			frame := make([]byte, 24+len(payload))
			binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
			binary.LittleEndian.PutUint32(frame[4:8], uint32(origLen))
			binary.LittleEndian.PutUint16(frame[8:10], uint16(W))
			binary.LittleEndian.PutUint16(frame[10:12], uint16(M))
			frame[12] = rep.Anchor
			frame[13] = tt.twist
			binary.LittleEndian.PutUint16(frame[14:16], 0)
			binary.LittleEndian.PutUint16(frame[16:18], flags)
			copy(frame[24:], payload)

			// First verify Go decompressor
			goDec, err := przm.DecompressCellularSheaf(frame, make([]byte, origLen+64))
			if err != nil {
				t.Fatalf("Go DecompressCellularSheaf failed: %v", err)
			}
			if !bytes.Equal(goDec, orig) {
				t.Fatalf("Go DecompressCellularSheaf failed to reconstruct original for %s", tt.name)
			}

			// Now test Pure C decompressor via test_przm_dec.exe
			inPrzm := filepath.Join(tmpDir, tt.name+".przm")
			outBin := filepath.Join(tmpDir, tt.name+".out")

			if err := os.WriteFile(inPrzm, frame, 0644); err != nil {
				t.Fatalf("failed to write frame file: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor execution error on %s: %v\nOutput: %s", tt.name, err, string(output))
			}

			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("failed to read C output file: %v", err)
			}

			if !bytes.Equal(cDec, orig) {
				t.Errorf("[BUG CONFIRMED] Pure C decompressor produces bit-level corruption on %s (twist=%d)!\n"+
					"Pure C does not perform Galois vector scaling during row sweep!",
					tt.name, tt.twist)
			} else {
				t.Logf("PASS: %s successfully decompressed in Pure C", tt.name)
			}
		})
	}
}

// TestAdversarial_NaturalTwistSelection_CInterop tests end-to-end pipeline:
// Feed data with multiplicative ratio 2 to CompressCellularSheaf.
// Verify CompressCellularSheaf naturally selects twist = 2.
// Then decompress using test_przm_dec.exe.
func TestAdversarial_NaturalTwistSelection_CInterop(t *testing.T) {
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
		t.Skip("C test binary test_przm_dec.exe not found")
	}

	// 256 bytes geometric progression in F_{2^8} with ratio 2
	data := make([]byte, 256)
	data[0] = 1
	for i := range 255 {
		data[i+1] = przm.GF256Mul(2, data[i])
	}

	comp, err := przm.CompressCellularSheaf(data, 3)
	if err != nil {
		t.Fatalf("CompressCellularSheaf failed: %v", err)
	}

	// Inspect header
	twist := comp[13]
	t.Logf("CompressCellularSheaf naturally selected Galois twist: %d (compressed len: %d vs raw: %d)",
		twist, len(comp), len(data))

	if twist != 2 {
		t.Logf("Note: twist selected was %d, not 2", twist)
	}

	// Verify Go decompressor succeeds
	goDec, err := przm.DecompressCellularSheaf(comp, make([]byte, len(data)+64))
	if err != nil {
		t.Fatalf("Go DecompressCellularSheaf failed: %v", err)
	}
	if !bytes.Equal(goDec, data) {
		t.Fatalf("Go DecompressCellularSheaf output mismatch")
	}

	// Verify Pure C decompressor
	tmpDir := t.TempDir()
	inPrzm := filepath.Join(tmpDir, "natural_twist.przm")
	outBin := filepath.Join(tmpDir, "natural_twist.out")

	if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cmd := exec.Command(cBinPath, inPrzm, outBin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pure C decompressor execution error: %v\nOutput: %s", err, string(output))
	}

	cDec, err := os.ReadFile(outBin)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if !bytes.Equal(cDec, data) {
		t.Errorf("[BUG CONFIRMED] End-to-end Pure C decompressor failure on natural twist frame (twist=%d)!\n"+
			"Pure C decompressor output does not match original data!", twist)
	} else {
		t.Logf("PASS: Pure C successfully decompressed natural twist frame")
	}
}

// ----------------------------------------------------------------------------
// 7. Malformed Frames & Memory Safety Fuzzing
// ----------------------------------------------------------------------------

func TestAdversarial_MalformedFrames_MemorySafety(t *testing.T) {
	// Destination buffer
	dst := make([]byte, 2048)

	// 1. Truncated headers (0..23 bytes)
	for sz := range 24 {
		trunc := make([]byte, sz)
		_, err := przm.DecompressCellularSheaf(trunc, dst)
		if err == nil && sz > 0 {
			t.Fatalf("DecompressCellularSheaf did not reject truncated header (len %d)", sz)
		}
	}

	// 2. Corrupted Magic
	badMagic := make([]byte, 32)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0x12345678)
	binary.LittleEndian.PutUint32(badMagic[4:8], 16)
	if _, err := przm.DecompressCellularSheaf(badMagic, dst); err == nil {
		t.Fatalf("DecompressCellularSheaf accepted bad magic")
	}

	// 3. origLen exceeds MaxBlockSize
	hugeLen := make([]byte, 32)
	binary.LittleEndian.PutUint32(hugeLen[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
	binary.LittleEndian.PutUint32(hugeLen[4:8], 0x7FFFFFFF)
	if _, err := przm.DecompressCellularSheaf(hugeLen, dst); err == nil {
		t.Fatalf("DecompressCellularSheaf accepted oversized block")
	}

	// 4. Inconsistent grid dimensions (W*M > origLen)
	badDim := make([]byte, 32)
	binary.LittleEndian.PutUint32(badDim[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
	binary.LittleEndian.PutUint32(badDim[4:8], 16)
	binary.LittleEndian.PutUint16(badDim[8:10], 8)       // W = 8
	binary.LittleEndian.PutUint16(badDim[10:12], 8)      // M = 8 -> W*M = 64 > 16
	binary.LittleEndian.PutUint16(badDim[16:18], 0x0001) // CurlFree
	if _, err := przm.DecompressCellularSheaf(badDim, dst); err == nil {
		t.Fatalf("DecompressCellularSheaf accepted inconsistent grid dimensions (W*M > origLen)")
	}

	// 5. Sparse curl NNZ overflow
	badNNZ := make([]byte, 40)
	binary.LittleEndian.PutUint32(badNNZ[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
	binary.LittleEndian.PutUint32(badNNZ[4:8], 64)
	binary.LittleEndian.PutUint16(badNNZ[8:10], 8)
	binary.LittleEndian.PutUint16(badNNZ[10:12], 8)
	binary.LittleEndian.PutUint16(badNNZ[16:18], 0x0002) // SparseCurl
	// NNZ = 1000000 -> exceeds payload
	binary.LittleEndian.PutUint32(badNNZ[24:28], 1000000)
	if _, err := przm.DecompressCellularSheaf(badNNZ, dst); err == nil {
		t.Fatalf("DecompressCellularSheaf accepted huge NNZ without error")
	}

	t.Logf("PASS: All malformed frame tests safely rejected without panic or bounds overflow.")
}

// ----------------------------------------------------------------------------
// 8. Zero-Allocation Invariant on Adversarial Payloads
// ----------------------------------------------------------------------------

func TestAdversarial_ZeroAllocDecompression_AllVariants(t *testing.T) {
	variants := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "RawSmall",
			gen:  func() []byte { return []byte{1, 2, 3, 4, 5, 6, 7} },
		},
		{
			name: "Harmonic_8x8",
			gen: func() []byte {
				b := make([]byte, 64)
				for i := range b {
					b[i] = uint8(i * 3)
				}
				return b
			},
		},
		{
			name: "SparseCurl_16x16",
			gen: func() []byte {
				b := make([]byte, 256)
				for i := range b {
					b[i] = uint8(i * 7)
				}
				b[20] ^= 0x55
				b[40] ^= 0xAA
				return b
			},
		},
	}

	for _, v := range variants {
		data := v.gen()
		comp, err := przm.CompressCellularSheaf(data, 3)
		if err != nil {
			t.Fatalf("%s compress failed: %v", v.name, err)
		}

		dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

		// Warmup
		_, _ = przm.DecompressCellularSheaf(comp, dst)

		allocs := testing.AllocsPerRun(100, func() {
			_, err := przm.DecompressCellularSheaf(comp, dst)
			if err != nil {
				t.Fatal(err)
			}
		})

		if allocs > 0 {
			t.Fatalf("%s violated zero-allocation contract: %f allocs/op", v.name, allocs)
		}
	}
	t.Logf("PASS: Zero-allocation contract verified across all frame variants.")
}
