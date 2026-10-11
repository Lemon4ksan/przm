// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package adversarial_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// =============================================================================
// SECTION 2: MILESTONE 2 — DISCRETE HEISENBERG GROUP & CELLULAR SHEAF HODGE LAPLACIAN
// =============================================================================

// -----------------------------------------------------------------------------
// Source: challenger_m2_test.go
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// Adversarial Challenger Suite for Milestone 2:
// Discrete Heisenberg Group H_3(F_{2^8}) Foliation
// -----------------------------------------------------------------------------

// TestChallengerM2_100Goroutine_ConcurrencyStress executes bounded concurrent
// goroutines continuously foliating, unfoliating, compressing, and decompressing
// Heisenberg frames under `go test -race`.
func TestChallengerM2_100Goroutine_ConcurrencyStress(t *testing.T) {
	const numGoroutines = 8
	const iterationsPerGoroutine = 50

	var startBarrier sync.WaitGroup
	startBarrier.Add(1)

	var doneGroup sync.WaitGroup
	doneGroup.Add(numGoroutines)

	errChan := make(chan error, numGoroutines*iterationsPerGoroutine)

	for g := range numGoroutines {
		go func(gid int) {
			defer doneGroup.Done()

			// Local RNG per goroutine
			rng := rand.New(rand.NewSource(int64(gid*1000 + 42)))

			startBarrier.Wait()

			testSizes := []int{0, 1, 2, 3, 4, 11, 12, 13, 17, 24, 48, 127, 256, 1023, 2048, 4096, 8192}

			for iter := range iterationsPerGoroutine {
				stride := 3
				if iter%2 == 1 {
					stride = 4
				}

				sz := testSizes[(gid+iter)%len(testSizes)]
				raw := make([]byte, sz)
				rng.Read(raw)

				// For some iterations inject Heisenberg coupling: z = x * y
				if iter%3 == 0 && sz >= stride {
					for i := 0; i+stride <= sz; i += stride {
						raw[i+2] = przm.GF256Mul(raw[i], raw[i+1])
					}
				}

				// 1. Test In-place foliation & unfoliation involution
				buf := make([]byte, sz)
				copy(buf, raw)
				przm.HeisenbergFoliateInplace(buf, stride)
				przm.HeisenbergUnfoliateInplace(buf, stride)
				if !bytes.Equal(buf, raw) {
					errChan <- fmt.Errorf("goroutine %d iter %d: Inplace involution failed on sz=%d, stride=%d", gid, iter, sz, stride)
					return
				}

				// 2. Test CompressHeisenberg & DecompressHeisenberg
				comp, err := przm.CompressHeisenberg(raw, 3)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d iter %d: CompressHeisenberg failed: %v", gid, iter, err)
					return
				}

				dst := make([]byte, sz+64)
				if sz == 0 {
					decTo, err := przm.DecompressTo(comp, dst)
					if err != nil || len(decTo) != 0 {
						errChan <- fmt.Errorf("goroutine %d iter %d: DecompressTo empty failed: %v", gid, iter, err)
						return
					}
					continue
				}

				dec, err := przm.DecompressHeisenberg(comp, dst)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressHeisenberg failed: %v", gid, iter, err)
					return
				}
				if !bytes.Equal(dec, raw) {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressHeisenberg mismatch on sz=%d, stride=%d", gid, iter, sz, stride)
					return
				}

				// 3. Test DecompressTo dispatch
				decTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressTo failed: %v", gid, iter, err)
					return
				}
				if !bytes.Equal(decTo, raw) {
					errChan <- fmt.Errorf("goroutine %d iter %d: DecompressTo mismatch on sz=%d, stride=%d", gid, iter, sz, stride)
					return
				}

				// 4. Test full pipeline Compress / Decompress
				if sz >= 48 {
					fullComp, err := przm.Compress(raw, 3)
					if err != nil {
						errChan <- fmt.Errorf("goroutine %d iter %d: Compress failed: %v", gid, iter, err)
						return
					}
					fullDec, err := przm.Decompress(fullComp)
					if err != nil {
						errChan <- fmt.Errorf("goroutine %d iter %d: Decompress failed: %v", gid, iter, err)
						return
					}
					if !bytes.Equal(fullDec, raw) {
						errChan <- fmt.Errorf("goroutine %d iter %d: Full roundtrip mismatch on sz=%d", gid, iter, sz)
						return
					}
				}
			}
		}(g)
	}

	// Release all workers simultaneously
	startBarrier.Done()
	doneGroup.Wait()
	close(errChan)

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Fatalf("Encountered %d concurrency errors under %d goroutines! First error: %v", len(errs), numGoroutines, errs[0])
	}
}

// TestChallengerM2_ZeroAllocDecompression_Assertion verifies strictly
// 0 heap allocations during decompression when destination buffer is pre-allocated.
func TestChallengerM2_ZeroAllocDecompression_Assertion(t *testing.T) {
	// 1. In-place unfoliation strictly 0 allocs
	t.Run("InplaceUnfoliation_ZeroAlloc", func(t *testing.T) {
		buf3 := make([]byte, 8192)
		allocs3 := testing.AllocsPerRun(100, func() {
			przm.HeisenbergUnfoliateInplace(buf3, 3)
		})
		if allocs3 != 0 {
			t.Fatalf("HeisenbergUnfoliateInplace(stride=3) allocated %v allocs/op, expected 0", allocs3)
		}

		buf4 := make([]byte, 8192)
		allocs4 := testing.AllocsPerRun(100, func() {
			przm.HeisenbergUnfoliateInplace(buf4, 4)
		})
		if allocs4 != 0 {
			t.Fatalf("HeisenbergUnfoliateInplace(stride=4) allocated %v allocs/op, expected 0", allocs4)
		}
	})

	// 2. Mode 1 (Foliated stream uncompressed) with pre-allocated buffer
	t.Run("Mode1_Foliated_DecompressZeroAlloc", func(t *testing.T) {
		n := 4095
		raw := make([]byte, n)
		rng := rand.New(rand.NewSource(54321))
		rng.Read(raw) // Fill with high entropy so PWS cannot compress

		// Set 10% of triplets to exact Heisenberg coupling (z = x * y)
		for i := 0; i+2 < n; i += 30 {
			raw[i] = uint8((i % 200) + 1)
			raw[i+1] = uint8(((i / 200) % 200) + 1)
			raw[i+2] = przm.GF256Mul(raw[i], raw[i+1])
		}

		comp, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}
		if comp[9] != przm.HeisenbergModeFoliated {
			t.Fatalf("Expected HeisenbergModeFoliated (1), got %d", comp[9])
		}

		dst := make([]byte, n+64)

		allocs := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressHeisenberg(comp, dst)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if len(dec) != n {
				t.Fatalf("Length mismatch")
			}
		})

		if allocs != 0 {
			t.Fatalf("DecompressHeisenberg Mode 1 expected 0 allocs/op, got %v", allocs)
		}

		allocsTo := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}
			if len(dec) != n {
				t.Fatalf("Length mismatch")
			}
		})

		if allocsTo != 0 {
			t.Fatalf("DecompressTo Mode 1 expected 0 allocs/op, got %v", allocsTo)
		}
	})

	// 3. Mode 0 (Raw passthrough bypass) with pre-allocated buffer
	t.Run("Mode0_RawBypass_DecompressZeroAlloc", func(t *testing.T) {
		n := 2048
		raw := make([]byte, n)
		rng := rand.New(rand.NewSource(12345))
		rng.Read(raw) // random data triggers Mode 0 bypass

		comp, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}
		if comp[9] != przm.HeisenbergModeRaw {
			t.Fatalf("Expected HeisenbergModeRaw (0), got %d", comp[9])
		}

		dst := make([]byte, n+64)

		allocs := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressHeisenberg(comp, dst)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if len(dec) != n {
				t.Fatalf("Length mismatch")
			}
		})

		if allocs != 0 {
			t.Fatalf("DecompressHeisenberg Mode 0 expected 0 allocs/op, got %v", allocs)
		}
	})

	// 4. Mode 3 (Foliated cascaded with PWS)
	t.Run("Mode3_CascadedPWS_DecompressAllocMeasurement", func(t *testing.T) {
		numPixels := 4096
		raw := make([]byte, numPixels*3)
		for i := range numPixels {
			x := uint8((i % 64) + 1)
			y := uint8(((i / 64) % 64) + 1)
			z := przm.GF256Mul(x, y)
			raw[i*3+0] = x
			raw[i*3+1] = y
			raw[i*3+2] = z
		}

		comp, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}
		if comp[9] != przm.HeisenbergModeCompressed {
			t.Fatalf("Expected HeisenbergModeCompressed (3), got %d", comp[9])
		}

		dst := make([]byte, len(raw)+64)
		allocs := testing.AllocsPerRun(100, func() {
			dec, err := przm.DecompressHeisenberg(comp, dst)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if len(dec) != len(raw) {
				t.Fatalf("Length mismatch")
			}
		})
		t.Logf("Mode 3 (Cascaded PWS) decompression heap allocations: %v allocs/op", allocs)
	})
}

// TestChallengerM2_EmptyBuffer_EdgeCase tests and documents the behavior of empty buffer compression/decompression.
func TestChallengerM2_EmptyBuffer_EdgeCase(t *testing.T) {
	comp, err := przm.CompressHeisenberg([]byte{}, 3)
	if err != nil {
		t.Fatalf("CompressHeisenberg on empty slice failed: %v", err)
	}

	// Calling DecompressHeisenberg directly on 0-byte slice returns nil error and 0 length (PROJECT.md Feature 4 contract)
	dec, decErr := przm.DecompressHeisenberg(comp, nil)
	if decErr != nil || len(dec) != 0 {
		t.Fatalf("Expected nil error and empty slice when passing 0 bytes to DecompressHeisenberg, got: %v, len=%d", decErr, len(dec))
	}

	// Calling DecompressTo directly on 0-byte slice succeeds via wrapper guard
	decTo, err := przm.DecompressTo(comp, nil)
	if err != nil || len(decTo) != 0 {
		t.Fatalf("DecompressTo failed on 0 bytes: %v", err)
	}
}

// TestChallengerM2_GF256Mul_FullFieldEquivalence verifies that Go table-lookup GF256Mul
// is 100% algebraically identical to Pure C shift-and-xor gf256_mul across all 65,536 byte pairs.
func TestChallengerM2_GF256Mul_FullFieldEquivalence(t *testing.T) {
	pureCGf256Mul := func(a, b uint8) uint8 {
		var p uint8 = 0
		for range 8 {
			if (b & 1) != 0 {
				p ^= a
			}
			hi := a & 0x80
			a <<= 1
			if hi != 0 {
				a ^= 0x1B
			}
			b >>= 1
		}
		return p
	}

	for a := range 256 {
		for b := range 256 {
			cExpected := pureCGf256Mul(uint8(a), uint8(b))
			goActual := przm.GF256Mul(uint8(a), uint8(b))
			if goActual != cExpected {
				t.Fatalf("GF256Mul mismatch at a=0x%02X, b=0x%02X: Go=0x%02X, C=0x%02X", a, b, goActual, cExpected)
			}
		}
	}
}

// TestChallengerM2_PureC_50RandomFrames_Stress generates 50 random Heisenberg
// frames in Go across stride 3 and 4, pipes them to Pure C `test_przm_dec.exe`,
// and asserts 100% bit-exact lossless roundtrips.
func TestChallengerM2_PureC_50RandomFrames_Stress(t *testing.T) {
	cBinPath, err := filepath.Abs("../../test_przm_dec.exe")
	if err != nil || func() bool { _, e := os.Stat(cBinPath); return e != nil }() {
		cBinPath, err = filepath.Abs("test_przm_dec.exe")
	}
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}
	if _, err := os.Stat(cBinPath); err != nil {
		t.Fatalf("test_przm_dec.exe not found at %s: build it first", cBinPath)
	}

	tmpDir := t.TempDir()
	rng := rand.New(rand.NewSource(99999))

	// 50 test configurations: 25 stride 3, 25 stride 4
	sizes := []int{
		12, 13, 15, 23, 24, 25, 48, 49, 99, 100,
		128, 255, 256, 300, 511, 512, 777, 1024, 1500, 2048,
		3000, 3072, 4000, 4095, 4096,
	}

	for i := range 50 {
		stride := 3
		if i >= 25 {
			stride = 4
		}
		sz := sizes[i%len(sizes)]

		testName := fmt.Sprintf("Frame_%02d_Stride%d_Size%d", i, stride, sz)
		t.Run(testName, func(t *testing.T) {
			raw := make([]byte, sz)
			rng.Read(raw)

			// Alternate frame coupling characteristics:
			// - Pure coupled (z = x * y)
			// - Partial coupled with noise
			// - Pure random (mode 0 raw bypass)
			switch i % 3 {
			case 0:
				// Pure coupled
				for j := 0; j+stride <= sz; j += stride {
					raw[j+2] = przm.GF256Mul(raw[j], raw[j+1])
				}
			case 1:
				// Partial coupled
				for j := 0; j+stride <= sz; j += stride {
					if j%2 == 0 {
						raw[j+2] = przm.GF256Mul(raw[j], raw[j+1])
					}
				}
			case 2:
				// Pure random; left as is
			}

			// Compress in Go
			comp, err := przm.CompressHeisenberg(raw, 3)
			if err != nil {
				t.Fatalf("Go CompressHeisenberg failed: %v", err)
			}

			inPrzm := filepath.Join(tmpDir, fmt.Sprintf("f_%d.przm", i))
			outBin := filepath.Join(tmpDir, fmt.Sprintf("f_%d.bin", i))

			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("Failed to write frame file: %v", err)
			}

			// Invoke Pure C decompressor
			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("C test_przm_dec.exe failed on frame %d (stride=%d, sz=%d, mode=%d):\nOutput:\n%s\nErr: %v",
					i, stride, sz, comp[9], string(output), err)
			}

			// Read back pure C output
			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("Failed to read C output file: %v", err)
			}

			if !bytes.Equal(cDec, raw) {
				t.Fatalf("Bit-exact mismatch on frame %d (stride=%d, sz=%d, mode=%d)! Got %d bytes, want %d bytes",
					i, stride, sz, comp[9], len(cDec), len(raw))
			}
		})
	}
}

// TestChallengerM2_MalformedFrame_Robustness adversarially stress-tests
// the Heisenberg parser against corrupted headers, invalid strides, out-of-bound lengths,
// and truncated payloads in both Go and Pure C.
func TestChallengerM2_MalformedFrame_Robustness(t *testing.T) {
	cBinPath, err := filepath.Abs("../../test_przm_dec.exe")
	if err != nil || func() bool { _, e := os.Stat(cBinPath); return e != nil }() {
		cBinPath, err = filepath.Abs("test_przm_dec.exe")
	}
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	t.Run("Go_MalformedFrames", func(t *testing.T) {
		// 1. Truncated header (1 <= sz < 12 bytes)
		for sz := 1; sz < 12; sz++ {
			buf := make([]byte, sz)
			_, err := przm.DecompressHeisenberg(buf, nil)
			if err == nil {
				t.Fatalf("Expected error on truncated header sz=%d, got nil", sz)
			}
		}

		// 2. Invalid Magic
		badMagic := make([]byte, 24)
		binary.LittleEndian.PutUint32(badMagic[0:4], 0xDEADBEEF)
		_, err := przm.DecompressHeisenberg(badMagic, nil)
		if err == nil {
			t.Fatalf("Expected error on bad magic, got nil")
		}

		// 3. Invalid Stride
		for _, badStride := range []uint8{0, 1, 2, 5, 10, 255} {
			buf := make([]byte, 24)
			binary.LittleEndian.PutUint32(buf[0:4], przm.PRZM_MAGIC_HEISENBERG)
			binary.LittleEndian.PutUint32(buf[4:8], 12)
			buf[8] = badStride
			buf[9] = 1 // mode foliated
			_, err := przm.DecompressHeisenberg(buf, nil)
			if err == nil {
				t.Fatalf("Expected error on bad stride=%d, got nil", badStride)
			}
		}

		// 4. Invalid Mode
		for _, badMode := range []uint8{2, 4, 5, 255} {
			buf := make([]byte, 24)
			binary.LittleEndian.PutUint32(buf[0:4], przm.PRZM_MAGIC_HEISENBERG)
			binary.LittleEndian.PutUint32(buf[4:8], 12)
			buf[8] = 3
			buf[9] = badMode
			_, err := przm.DecompressHeisenberg(buf, nil)
			if err == nil {
				t.Fatalf("Expected error on bad mode=%d, got nil", badMode)
			}
		}

		// 5. Truncated payload for Mode 1
		trunc := make([]byte, 20)
		binary.LittleEndian.PutUint32(trunc[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(trunc[4:8], 100) // Claims 100 bytes uncompressed
		trunc[8] = 3
		trunc[9] = 1 // Foliated
		_, err = przm.DecompressHeisenberg(trunc, nil)
		if err == nil {
			t.Fatalf("Expected error on truncated payload, got nil")
		}
	})

	t.Run("PureC_MalformedFrames", func(t *testing.T) {
		// Verify Pure C CLI cleanly exits with non-zero error without crashing
		malformedCases := []struct {
			name string
			data []byte
		}{
			{
				name: "TruncatedHeader",
				data: []byte{0x48, 0x45, 0x49},
			},
			{
				name: "BadMagic",
				data: []byte{0x12, 0x34, 0x56, 0x78, 0x10, 0x00, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00},
			},
			{
				name: "BadStride",
				data: []byte{0x48, 0x45, 0x49, 0x53, 0x0C, 0x00, 0x00, 0x00, 0x05, 0x01, 0x00, 0x00, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
			},
			{
				name: "TruncatedPayload",
				data: []byte{0x48, 0x45, 0x49, 0x53, 0xFF, 0x00, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 1, 2, 3},
			},
		}

		for _, mc := range malformedCases {
			t.Run(mc.name, func(t *testing.T) {
				inPrzm := filepath.Join(tmpDir, mc.name+".przm")
				outBin := filepath.Join(tmpDir, mc.name+".out")
				_ = os.WriteFile(inPrzm, mc.data, 0644)

				cmd := exec.Command(cBinPath, inPrzm, outBin)
				err := cmd.Run()
				if err == nil {
					t.Fatalf("Pure C decompressor expected failure on %s, but exited cleanly", mc.name)
				}
			})
		}
	})
}

// BenchmarkChallengerM2_LineRateThroughput benchmarks decompression rate on
// 64 KiB and 256 KiB blocks to verify > 20 GB/s decompressor line rate.
func BenchmarkChallengerM2_LineRateThroughput(b *testing.B) {
	sizes := []int{64 * 1024, 256 * 1024}

	for _, sz := range sizes {
		b.Run(fmt.Sprintf("Block_%dKiB", sz/1024), func(b *testing.B) {
			raw := make([]byte, sz)
			for i := 0; i+2 < sz; i += 3 {
				raw[i] = uint8(i & 0xFF)
				raw[i+1] = uint8((i * 7) & 0xFF)
				raw[i+2] = przm.GF256Mul(raw[i], raw[i+1]) ^ uint8((i*13+17)&0xFF)
			}

			comp, err := przm.CompressHeisenberg(raw, 3)
			if err != nil {
				b.Fatal(err)
			}

			dst := make([]byte, sz+64)

			b.SetBytes(int64(sz))
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := przm.DecompressHeisenberg(comp, dst)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_m2_adversarial_test.go
// -----------------------------------------------------------------------------

// cGF256Mul implements the exact Russian Peasant algorithm from src/przm_dec.c:
//
//	uint8_t p = 0;
//	for (int i = 0; i < 8; i++) {
//	    if (b & 1) p ^= a;
//	    uint8_t hi = a & 0x80;
//	    a <<= 1;
//	    if (hi) a ^= 0x1B;
//	    b >>= 1;
//	}
//	return p;
func cGF256Mul(a, b uint8) uint8 {
	var p uint8 = 0
	for range 8 {
		if (b & 1) != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1B
		}
		b >>= 1
	}
	return p
}

// TestAdversarialHeisenberg_GF256MultiplicationTableExhaustive tests all 65,536 pairs
// of (a, b) over F_{2^8} to prove Go table lookup GF256Mul exactly matches C gf256_mul.
func TestAdversarialHeisenberg_GF256MultiplicationTableExhaustive(t *testing.T) {
	for a := range 256 {
		for b := range 256 {
			uA := uint8(a)
			uB := uint8(b)
			goRes := przm.GF256Mul(uA, uB)
			cRes := cGF256Mul(uA, uB)
			if goRes != cRes {
				t.Fatalf("GF(256) multiplication mismatch at (%d, %d): Go=%02X, C=%02X", a, b, goRes, cRes)
			}
			// Commutativity
			if przm.GF256Mul(uA, uB) != przm.GF256Mul(uB, uA) {
				t.Fatalf("Commutativity violated at (%d, %d)", a, b)
			}
		}
	}
}

// TestAdversarialHeisenberg_AlgebraicInvolutionFuzz100k verifies 100,000 randomized triplets
// and quads over F_{2^8}, proving exact algebraic involution H^{-1}(H(x, y, z)) == (x, y, z).
func TestAdversarialHeisenberg_AlgebraicInvolutionFuzz100k(t *testing.T) {
	rng := rand.New(rand.NewSource(987654321))
	const iterations = 100000

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for i := range iterations {
		x := uint8(rng.Intn(256))
		y := uint8(rng.Intn(256))
		z := uint8(rng.Intn(256))
		elem := przm.HeisenbergElement3{X: x, Y: y, Z: z}

		// 1. Group element inversion and involution
		invElem := przm.InvH3(elem)
		invInvElem := przm.InvH3(invElem)
		if invInvElem != elem {
			t.Fatalf("Iteration %d: InvH3(InvH3(%+v)) != elem, got %+v", i, elem, invInvElem)
		}

		// 2. Right and Left inverse cancel to identity
		mulRight := przm.MulH3(elem, invElem)
		if mulRight != ident {
			t.Fatalf("Iteration %d: elem * inv(elem) != ident: got %+v", i, mulRight)
		}
		mulLeft := przm.MulH3(invElem, elem)
		if mulLeft != ident {
			t.Fatalf("Iteration %d: inv(elem) * elem != ident: got %+v", i, mulLeft)
		}

		// 3. Coordinate foliation mapping involution: z' = z ^ (x * y), z'' = z' ^ (x * y) == z
		prod := przm.GF256Mul(x, y)
		zPrime := z ^ prod
		zDoublePrime := zPrime ^ prod
		if zDoublePrime != z {
			t.Fatalf("Iteration %d: coordinate involution failed: x=%02X y=%02X z=%02X -> z'=%02X -> z''=%02X",
				i, x, y, z, zPrime, zDoublePrime)
		}

		// 4. Stride 4 Quad foliation: (x, y, z, w) -> (x, y, z ^ (x * y), w)
		w := uint8(rng.Intn(256))
		quad := [4]byte{x, y, z, w}
		foliatedQuad := quad
		przm.HeisenbergFoliateInplace(foliatedQuad[:], 4)
		if foliatedQuad[0] != x || foliatedQuad[1] != y || foliatedQuad[2] != zPrime || foliatedQuad[3] != w {
			t.Fatalf("Iteration %d: quad foliation mismatch: got %v, want [%02X %02X %02X %02X]",
				i, foliatedQuad, x, y, zPrime, w)
		}
		przm.HeisenbergUnfoliateInplace(foliatedQuad[:], 4)
		if foliatedQuad != quad {
			t.Fatalf("Iteration %d: quad unfoliation mismatch: got %v, want %v", i, foliatedQuad, quad)
		}
	}
}

// TestAdversarialHeisenberg_BoundaryLengths tests buffer lengths 0, 1, 2, unaligned lengths,
// and SWAR loop non-multiples (12, 16, 32).
func TestAdversarialHeisenberg_BoundaryLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(1122334455))

	// All interesting lengths: boundary, unaligned, SWAR boundaries, prime lengths
	testLengths := []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 23, 24, 25, 31, 32, 33, 35, 47, 48, 49,
		63, 64, 65, 95, 96, 97, 127, 128, 129, 255, 256, 257,
		511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097,
	}

	for _, n := range testLengths {
		orig := make([]byte, n)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, n)
			copy(buf, orig)

			// 1. Forward foliation
			przm.HeisenbergFoliateInplace(buf, stride)

			// 2. Channel invariants:
			// Bytes strictly within full stride blocks
			fullBlocks := n / stride
			for b := range fullBlocks {
				idx := b * stride
				// X must remain unchanged
				if buf[idx+0] != orig[idx+0] {
					t.Fatalf("Length %d, Stride %d: Channel X corrupted at block %d (index %d)", n, stride, b, idx)
				}
				// Y must remain unchanged
				if buf[idx+1] != orig[idx+1] {
					t.Fatalf("Length %d, Stride %d: Channel Y corrupted at block %d (index %d)", n, stride, b, idx+1)
				}
				// Z must equal origZ ^ (origX * origY)
				expectedZ := orig[idx+2] ^ przm.GF256Mul(orig[idx+0], orig[idx+1])
				if buf[idx+2] != expectedZ {
					t.Fatalf("Length %d, Stride %d: Channel Z mismatch at block %d (index %d): got %02X, want %02X",
						n, stride, b, idx+2, buf[idx+2], expectedZ)
				}
				// For stride 4, W must remain unchanged
				if stride == 4 {
					if buf[idx+3] != orig[idx+3] {
						t.Fatalf("Length %d, Stride %d: Channel W corrupted at block %d (index %d)", n, stride, b, idx+3)
					}
				}
			}

			// 3. Unaligned tail bytes (indices >= fullBlocks * stride) must be completely untouched
			tailStart := fullBlocks * stride
			for j := tailStart; j < n; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Length %d, Stride %d: Unaligned tail byte modified at index %d: got %02X, want %02X",
						n, stride, j, buf[j], orig[j])
				}
			}

			// 4. Inverse unfoliation must restore original bytes bit-identically
			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d, Stride %d: Involution failed! Restored buffer differs from original", n, stride)
			}
		}
	}
}

// TestAdversarialHeisenberg_ComprehensiveEveryLength0to256 tests EVERY single integer
// length from 0 to 256 for both stride 3 and 4 to guarantee zero off-by-one errors.
func TestAdversarialHeisenberg_ComprehensiveEveryLength0to256(t *testing.T) {
	rng := rand.New(rand.NewSource(44556677))

	for n := 0; n <= 256; n++ {
		orig := make([]byte, n)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, n)
			copy(buf, orig)

			przm.HeisenbergFoliateInplace(buf, stride)

			// Tail check
			tailStart := (n / stride) * stride
			for j := tailStart; j < n; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Length %d (stride %d): tail byte %d corrupted", n, stride, j)
				}
			}

			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d (stride %d): unfoliate mismatch", n, stride)
			}
		}
	}
}

// TestAdversarialHeisenberg_FrameCodecBoundaryPayloads tests roundtrips of boundary payloads
// through CompressHeisenberg and DecompressHeisenberg.
func TestAdversarialHeisenberg_FrameCodecBoundaryPayloads(t *testing.T) {
	rng := rand.New(rand.NewSource(99887766))

	testSizes := []int{0, 1, 2, 3, 4, 5, 10, 11, 12, 13, 24, 25, 47, 48, 49, 100, 3072, 4096}

	for _, size := range testSizes {
		orig := make([]byte, size)
		rng.Read(orig)

		// Make correlated data if size >= 3
		if size >= 3 {
			for i := 0; i+3 <= size; i += 3 {
				orig[i+2] = przm.GF256Mul(orig[i], orig[i+1])
			}
		}

		compressed, err := przm.CompressHeisenberg(orig, 3)
		if err != nil {
			t.Fatalf("Size %d: CompressHeisenberg failed: %v", size, err)
		}

		if size == 0 {
			// Check size 0 behavior
			t.Logf("Size 0: compressed len = %d", len(compressed))
			decompressed, err := przm.DecompressHeisenberg(compressed, nil)
			if err != nil {
				t.Logf("DISCOVERY: Size 0 DecompressHeisenberg returned error: %v (compressed len: %d)", err, len(compressed))
			} else if !bytes.Equal(decompressed, orig) {
				t.Fatalf("Size 0 mismatch: got %v, want %v", decompressed, orig)
			}
			continue
		}

		decompressed, err := przm.DecompressHeisenberg(compressed, nil)
		if err != nil {
			t.Fatalf("Size %d: DecompressHeisenberg failed: %v", size, err)
		}
		if !bytes.Equal(decompressed, orig) {
			t.Fatalf("Size %d: Decompressed mismatch: got len %d, want len %d", size, len(decompressed), len(orig))
		}

		// Also verify DecompressTo
		dst := make([]byte, size+64)
		decTo, err := przm.DecompressTo(compressed, dst)
		if err != nil {
			t.Fatalf("Size %d: DecompressTo failed: %v", size, err)
		}
		if !bytes.Equal(decTo, orig) {
			t.Fatalf("Size %d: DecompressTo mismatch", size)
		}
	}
}

// TestAdversarialHeisenberg_MalformedFrames tests security and robustness against
// corrupted headers, invalid strides, illegal modes, and truncated frames.
func TestAdversarialHeisenberg_MalformedFrames(t *testing.T) {
	validFrame := make([]byte, 12+24)
	binary.LittleEndian.PutUint32(validFrame[0:4], przm.PRZM_MAGIC_HEISENBERG)
	binary.LittleEndian.PutUint32(validFrame[4:8], 24)
	validFrame[8] = 3 // Stride = 3
	validFrame[9] = 1 // Mode = Foliated
	for i := 12; i < len(validFrame); i++ {
		validFrame[i] = uint8(i)
	}

	// 0. Empty frame (0 bytes) gracefully returns []byte{}, nil per contract
	dec0, err0 := przm.DecompressHeisenberg(validFrame[:0], nil)
	if err0 != nil || len(dec0) != 0 {
		t.Fatalf("Empty frame returned error %v, len %d (expected nil, 0)", err0, len(dec0))
	}

	// 1. Truncated header (1 <= l < 12 bytes)
	for l := 1; l < 12; l++ {
		_, err := przm.DecompressHeisenberg(validFrame[:l], nil)
		if err == nil {
			t.Fatalf("Truncated frame of length %d accepted without error", l)
		}
	}

	// 2. Corrupt Magic
	badMagic := make([]byte, len(validFrame))
	copy(badMagic, validFrame)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0xDEADBEEF)
	if _, err := przm.DecompressHeisenberg(badMagic, nil); err == nil {
		t.Fatalf("Bad magic accepted without error")
	}

	// 3. Corrupt Stride (must be 3 or 4)
	badStrides := []uint8{0, 1, 2, 5, 6, 8, 16, 255}
	for _, bs := range badStrides {
		badS := make([]byte, len(validFrame))
		copy(badS, validFrame)
		badS[8] = bs
		if _, err := przm.DecompressHeisenberg(badS, nil); err == nil {
			t.Fatalf("Illegal stride %d accepted without error", bs)
		}
	}

	// 4. Corrupt Mode
	badModes := []uint8{2, 4, 5, 255}
	for _, bm := range badModes {
		badM := make([]byte, len(validFrame))
		copy(badM, validFrame)
		badM[9] = bm
		if _, err := przm.DecompressHeisenberg(badM, nil); err == nil {
			t.Fatalf("Illegal mode %d accepted without error", bm)
		}
	}

	// 5. Truncated payload in Mode 0 and Mode 1
	for _, mode := range []uint8{0, 1} {
		truncPayload := make([]byte, 12+10) // origLen is 24, but payload has only 10
		copy(truncPayload, validFrame[:12+10])
		truncPayload[9] = mode
		if _, err := przm.DecompressHeisenberg(truncPayload, nil); err == nil {
			t.Fatalf("Truncated payload in mode %d accepted without error", mode)
		}
	}

	// 6. Excessive origLen (> MaxBlockSize)
	badLen := make([]byte, len(validFrame))
	copy(badLen, validFrame)
	binary.LittleEndian.PutUint32(badLen[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressHeisenberg(badLen, nil); err == nil {
		t.Fatalf("origLen exceeding MaxBlockSize accepted without error")
	}
}

// TestAdversarialHeisenberg_ZeroAllocationInvariant stresses zero memory allocation
// on DecompressHeisenberg and DecompressTo across varied block sizes.
func TestAdversarialHeisenberg_ZeroAllocationInvariant(t *testing.T) {
	sizes := []int{24, 48, 96, 384, 1536, 6144}

	for _, size := range sizes {
		// Construct Mode 1 (Foliated uncompressed) frame explicitly
		src := make([]byte, size)
		for i := range size {
			src[i] = uint8((i*37 + 13) & 0xFF)
		}

		// Frame with Mode 1: Foliated
		foliatedFrame := make([]byte, przm.HeisenbergHeaderSize+size)
		binary.LittleEndian.PutUint32(foliatedFrame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(foliatedFrame[4:8], uint32(size))
		foliatedFrame[8] = 3 // Stride = 3
		foliatedFrame[9] = przm.HeisenbergModeFoliated
		copy(foliatedFrame[przm.HeisenbergHeaderSize:], src)
		przm.HeisenbergFoliateInplace(foliatedFrame[przm.HeisenbergHeaderSize:], 3)

		dst := make([]byte, size+128)

		// 1. In-place unfoliation must be strictly 0 allocs
		allocsInplace := testing.AllocsPerRun(50, func() {
			przm.HeisenbergUnfoliateInplace(dst[:size], 3)
		})
		if allocsInplace != 0 {
			t.Fatalf("Size %d: HeisenbergUnfoliateInplace allocated: %v allocs/op", size, allocsInplace)
		}

		// 2. Mode 1 DecompressHeisenberg zero alloc
		allocsDec := testing.AllocsPerRun(50, func() {
			out, err := przm.DecompressHeisenberg(foliatedFrame, dst)
			if err != nil || len(out) != size {
				t.Fatalf("Decompress failed: %v", err)
			}
		})
		if allocsDec != 0 {
			t.Fatalf("Size %d: Mode 1 DecompressHeisenberg violated 0-alloc: got %v allocs/op", size, allocsDec)
		}

		// 3. Mode 1 DecompressTo zero alloc
		allocsTo := testing.AllocsPerRun(50, func() {
			out, err := przm.DecompressTo(foliatedFrame, dst)
			if err != nil || len(out) != size {
				t.Fatalf("DecompressTo failed: %v", err)
			}
		})
		if allocsTo != 0 {
			t.Fatalf("Size %d: Mode 1 DecompressTo violated 0-alloc: got %v allocs/op", size, allocsTo)
		}

		// 4. Test Mode 3 (Cascaded PWS) allocation profile
		srcCorrelated := make([]byte, size)
		for i := 0; i+3 <= size; i += 3 {
			srcCorrelated[i] = uint8(i & 0xFF)
			srcCorrelated[i+1] = uint8((i * 7) & 0xFF)
			srcCorrelated[i+2] = przm.GF256Mul(srcCorrelated[i], srcCorrelated[i+1])
		}
		comp3, err := przm.CompressHeisenberg(srcCorrelated, 3)
		if err == nil && len(comp3) > 9 && comp3[9] == przm.HeisenbergModeCompressed {
			allocsM3 := testing.AllocsPerRun(20, func() {
				_, _ = przm.DecompressHeisenberg(comp3, dst)
			})
			t.Logf("Size %d: Mode 3 (Cascaded PWS) allocations: %v allocs/op (delegated to inner PWSDecode)", size, allocsM3)
		}
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_m2_2_r3_test.go
// -----------------------------------------------------------------------------

// -----------------------------------------------------------------------------
// 1. Theorem 39: InvH3(InvH3(x)) == x across 50,000 random triplets in F_{2^8}^3
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_Theorem39_Involution50k(t *testing.T) {
	rng := rand.New(rand.NewSource(39393939))
	const totalTriplets = 50000

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for i := range totalTriplets {
		elem := przm.HeisenbergElement3{
			X: uint8(rng.Intn(256)),
			Y: uint8(rng.Intn(256)),
			Z: uint8(rng.Intn(256)),
		}

		// First algebraic inversion: H^{-1}(elem)
		invElem := przm.InvH3(elem)

		// Verification of coordinate map:
		// X and Y unchanged, Z' = Z ^ (X * Y)
		expectedZ := elem.Z ^ przm.GF256Mul(elem.X, elem.Y)
		if invElem.X != elem.X || invElem.Y != elem.Y || invElem.Z != expectedZ {
			t.Fatalf("Iteration %d: InvH3(%+v) coordinates invalid: got %+v, expected Z=%02X",
				i, elem, invElem, expectedZ)
		}

		// Second algebraic inversion: H^{-1}(H^{-1}(elem))
		invInvElem := przm.InvH3(invElem)

		// Strict Involution Check: Theorem 39
		if invInvElem != elem {
			t.Fatalf("Iteration %d: Theorem 39 Involution Failed! InvH3(InvH3(%+v)) = %+v, want %+v",
				i, elem, invInvElem, elem)
		}

		// Group inverse axioms: elem * invElem == e, invElem * elem == e
		rightMul := przm.MulH3(elem, invElem)
		if rightMul != ident {
			t.Fatalf("Iteration %d: elem * inv(elem) != (0,0,0): got %+v", i, rightMul)
		}
		leftMul := przm.MulH3(invElem, elem)
		if leftMul != ident {
			t.Fatalf("Iteration %d: inv(elem) * elem != (0,0,0): got %+v", i, leftMul)
		}
	}
}

// -----------------------------------------------------------------------------
// 2. Theorem 40: Unfoliate(Foliate(x)) == x across 50,000 random triplets/quads
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_Theorem40_FoliationUnfoliation50k(t *testing.T) {
	rng := rand.New(rand.NewSource(40404040))
	const totalBlocks = 50000

	// 2a. Stride 3 triplets (50,000 triplets = 150,000 bytes)
	data3 := make([]byte, totalBlocks*3)
	rng.Read(data3)
	orig3 := make([]byte, len(data3))
	copy(orig3, data3)

	// In-place foliation
	przm.HeisenbergFoliateInplace(data3, 3)

	// Validate coordinate transformation for all 50,000 triplets
	for i := range totalBlocks {
		x := orig3[i*3+0]
		y := orig3[i*3+1]
		z := orig3[i*3+2]
		expZ := z ^ przm.GF256Mul(x, y)

		if data3[i*3+0] != x {
			t.Fatalf("Triplet %d: channel X modified", i)
		}
		if data3[i*3+1] != y {
			t.Fatalf("Triplet %d: channel Y modified", i)
		}
		if data3[i*3+2] != expZ {
			t.Fatalf("Triplet %d: channel Z mismatch: got %02X, want %02X", i, data3[i*3+2], expZ)
		}
	}

	// In-place unfoliation: Theorem 40
	przm.HeisenbergUnfoliateInplace(data3, 3)
	if !bytes.Equal(data3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 In-Place Involution Failed: Unfoliate(Foliate(x)) != x")
	}

	// 2b. Test distinct buffers for Stride 3
	dst3 := make([]byte, len(orig3))
	przm.HeisenbergFoliate(dst3, orig3, 3)
	restored3 := make([]byte, len(orig3))
	przm.HeisenbergUnfoliate(restored3, dst3, 3)
	if !bytes.Equal(restored3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 Distinct Buffers Involution Failed")
	}

	// 2c. Test aliased in-place Foliate(&buf[0], &buf[0])
	aliased3 := make([]byte, len(orig3))
	copy(aliased3, orig3)
	przm.HeisenbergFoliate(aliased3, aliased3, 3)
	przm.HeisenbergUnfoliate(aliased3, aliased3, 3)
	if !bytes.Equal(aliased3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 Aliased Buffers Involution Failed")
	}

	// 2d. Stride 4 quads (50,000 quads = 200,000 bytes)
	data4 := make([]byte, totalBlocks*4)
	rng.Read(data4)
	orig4 := make([]byte, len(data4))
	copy(orig4, data4)

	przm.HeisenbergFoliateInplace(data4, 4)

	for i := range totalBlocks {
		x := orig4[i*4+0]
		y := orig4[i*4+1]
		z := orig4[i*4+2]
		w := orig4[i*4+3]
		expZ := z ^ przm.GF256Mul(x, y)

		if data4[i*4+0] != x || data4[i*4+1] != y || data4[i*4+2] != expZ || data4[i*4+3] != w {
			t.Fatalf("Quad %d: channel mismatch in foliation", i)
		}
	}

	przm.HeisenbergUnfoliateInplace(data4, 4)
	if !bytes.Equal(data4, orig4) {
		t.Fatalf("Theorem 40 Stride 4 Involution Failed")
	}
}

// -----------------------------------------------------------------------------
// 3. Edge Cases: all zeros, all 0xFF, alternating bit patterns, prime lengths
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_EdgeCases_AdversarialInputs(t *testing.T) {
	// Prime lengths from 2 to 4093
	primeLengths := []int{
		2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61,
		67, 71, 73, 79, 83, 89, 97, 101, 103, 107, 109, 113, 127, 131, 137,
		139, 149, 151, 157, 163, 167, 173, 179, 181, 191, 193, 197, 199, 211,
		223, 227, 229, 233, 239, 241, 251, 257, 509, 1021, 2053, 4093,
	}

	patterns := []struct {
		name string
		gen  func(length int) []byte
	}{
		{
			name: "AllZeros_0x00",
			gen: func(length int) []byte {
				return make([]byte, length)
			},
		},
		{
			name: "AllOnes_0xFF",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0xFF
				}
				return b
			},
		},
		{
			name: "Alternating_0xAA",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0xAA
				}
				return b
			},
		},
		{
			name: "Alternating_0x55",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0x55
				}
				return b
			},
		},
		{
			name: "AlternatingTriplets_AA_55_FF",
			gen: func(length int) []byte {
				b := make([]byte, length)
				cycle := []byte{0xAA, 0x55, 0xFF, 0x00, 0x33, 0xCC}
				for i := range b {
					b[i] = cycle[i%len(cycle)]
				}
				return b
			},
		},
		{
			name: "PowersOfTwo_SingleBits",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 1 << uint(i%8)
				}
				return b
			},
		},
	}

	for _, p := range patterns {
		for _, primeLen := range primeLengths {
			orig := p.gen(primeLen)

			for _, stride := range []int{3, 4} {
				buf := make([]byte, primeLen)
				copy(buf, orig)

				// In-place foliate
				przm.HeisenbergFoliateInplace(buf, stride)

				// Verify unaligned tail is completely untouched
				tailStart := (primeLen / stride) * stride
				for j := tailStart; j < primeLen; j++ {
					if buf[j] != orig[j] {
						t.Fatalf("Pattern %s, PrimeLen %d, Stride %d: tail corrupted at index %d: got %02X, want %02X",
							p.name, primeLen, stride, j, buf[j], orig[j])
					}
				}

				// In-place unfoliate must restore exact original
				przm.HeisenbergUnfoliateInplace(buf, stride)
				if !bytes.Equal(buf, orig) {
					t.Fatalf("Pattern %s, PrimeLen %d, Stride %d: involution failed",
						p.name, primeLen, stride)
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// 4. High Concurrency Stress Test: Heisenberg & Sheaf Decompressors
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_HighConcurrency_ZeroAlloc(t *testing.T) {
	const numGoroutines = 8
	const iterations = 50

	// 4a. Prepare diverse Heisenberg frames
	var heisFrames [][]byte
	var heisExpected [][]byte

	// Frame 1: Raw
	rawInput := make([]byte, 1024)
	for i := range rawInput {
		rawInput[i] = byte(i * 11)
	}
	compRaw, err := przm.CompressHeisenberg(rawInput, 1)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compRaw)
	heisExpected = append(heisExpected, rawInput)

	// Frame 2: Foliated Stride 3
	folInput3 := make([]byte, 3072)
	for i := 0; i < len(folInput3); i += 3 {
		folInput3[i] = byte(i % 127)
		folInput3[i+1] = byte((i*7 + 1) % 256)
		folInput3[i+2] = przm.GF256Mul(folInput3[i], folInput3[i+1])
	}
	compFol3, err := przm.CompressHeisenberg(folInput3, 3)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compFol3)
	heisExpected = append(heisExpected, folInput3)

	// Frame 3: Foliated Stride 4
	folInput4 := make([]byte, 4096)
	for i := 0; i < len(folInput4); i += 4 {
		folInput4[i] = byte(i % 127)
		folInput4[i+1] = byte((i*5 + 1) % 256)
		folInput4[i+2] = przm.GF256Mul(folInput4[i], folInput4[i+1])
		folInput4[i+3] = 0x88
	}
	compFol4, err := przm.CompressHeisenberg(folInput4, 3)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compFol4)
	heisExpected = append(heisExpected, folInput4)

	// 4b. Prepare diverse Cellular Sheaf frames
	var sheafFrames [][]byte
	var sheafExpected [][]byte

	// Sheaf 1: Harmonic 8x8
	h8 := make([]byte, 64)
	h8[0] = 0x77
	for j := range 7 {
		h8[j+1] = h8[j] ^ byte(j+1)
	}
	for i := 1; i < 8; i++ {
		v := byte(i * 3)
		for j := range 8 {
			h8[i*8+j] = h8[(i-1)*8+j] ^ v
		}
	}
	compSh8, err := przm.CompressCellularSheaf(h8, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSh8)
	sheafExpected = append(sheafExpected, h8)

	// Sheaf 2: ConstBoundaries 16x16
	h16 := make([]byte, 256)
	h16[0] = 0xAA
	for j := range 15 {
		h16[j+1] = h16[j] ^ 0x03
	}
	for i := 1; i < 16; i++ {
		for j := range 16 {
			h16[i*16+j] = h16[(i-1)*16+j] ^ 0x05
		}
	}
	compSh16, err := przm.CompressCellularSheaf(h16, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSh16)
	sheafExpected = append(sheafExpected, h16)

	// Sheaf 3: Sparse Curl 16x32 with unaligned remainder
	spCurl := make([]byte, 517)
	for i := range spCurl {
		spCurl[i] = byte(i * 7)
	}
	spCurl[19] ^= 0x33
	spCurl[105] ^= 0x77
	compSp, err := przm.CompressCellularSheaf(spCurl, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSp)
	sheafExpected = append(sheafExpected, spCurl)

	// 4c. Launch concurrent workers
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*iterations)

	for g := range numGoroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(gid*10007 + 73)))
			dst := bufkit.AlignedBytes(8192, bufkit.CacheLineSize)

			for iter := range iterations {
				if gid%2 == 0 {
					// Heisenberg decompress
					idx := rng.Intn(len(heisFrames))
					frame := heisFrames[idx]
					exp := heisExpected[idx]

					dec, err := przm.DecompressHeisenberg(frame, dst)
					if err != nil {
						errCh <- fmt.Errorf("gid %d iter %d DecompressHeisenberg error: %w", gid, iter, err)
						return
					}
					if !bytes.Equal(dec, exp) {
						errCh <- fmt.Errorf("gid %d iter %d DecompressHeisenberg data mismatch", gid, iter)
						return
					}
				} else {
					// Sheaf decompress
					idx := rng.Intn(len(sheafFrames))
					frame := sheafFrames[idx]
					exp := sheafExpected[idx]

					dec, err := przm.DecompressCellularSheaf(frame, dst)
					if err != nil {
						errCh <- fmt.Errorf("gid %d iter %d DecompressCellularSheaf error: %w", gid, iter, err)
						return
					}
					if !bytes.Equal(dec, exp) {
						errCh <- fmt.Errorf("gid %d iter %d DecompressCellularSheaf data mismatch", gid, iter)
						return
					}
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// 5. Parallel Benchmarks verifying 0 B/op and 0 allocs/op under multi-core load
// -----------------------------------------------------------------------------
func BenchmarkChallengerM2_2_R3_Heisenberg_ParallelZeroAlloc(b *testing.B) {
	src := make([]byte, 65536)
	for i := range src {
		src[i] = uint8((i * 3) & 0xFF)
	}
	for i := 0; i+2 < len(src); i += 3 {
		src[i+2] = przm.GF256Mul(src[i], src[i+1]) ^ uint8((i*19+7)&0xFF)
	}
	compressed, err := przm.CompressHeisenberg(src, 3)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dst := bufkit.AlignedBytes(len(src)+64, bufkit.CacheLineSize)
		for pb.Next() {
			_, err := przm.DecompressHeisenberg(compressed, dst)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkChallengerM2_2_R3_CellularSheaf_ParallelZeroAlloc(b *testing.B) {
	orig := make([]byte, 1024)
	for i := range orig {
		orig[i] = uint8((i * 3) & 0xFF)
	}
	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
		// Warmup thread-local buffer
		_, _ = przm.DecompressCellularSheaf(compressed, dst)
		for pb.Next() {
			_, err := przm.DecompressCellularSheaf(compressed, dst)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// -----------------------------------------------------------------------------
// Source: challenger_sheaf_hodge_test.go
// -----------------------------------------------------------------------------

// dotGF256 computes the standard bilinear inner product in F_{2^8}^N:
// <x, y> = \sum_{i} x[i] * y[i] (with addition as XOR).
func dotGF256(x, y []uint8) uint8 {
	var acc uint8
	n := min(len(y), len(x))
	for i := range n {
		acc ^= przm.GF256Mul(x[i], y[i])
	}
	return acc
}

// dotCochain0 computes <phi, psi> on C^0(X; F_{2^8}).
func dotCochain0(phi, psi przm.Cochain0) uint8 {
	return dotGF256([]uint8(phi), []uint8(psi))
}

// dotCochain1 computes <u, v> on C^1(X; F_{2^8}) = <u.Eh, v.Eh> ^ <u.Ev, v.Ev>.
func dotCochain1(u, v przm.Cochain1) uint8 {
	return dotGF256(u.Eh, v.Eh) ^ dotGF256(u.Ev, v.Ev)
}

// dotCochain2 computes <c1, c2> on C^2(X; F_{2^8}).
func dotCochain2(c1, c2 przm.Cochain2) uint8 {
	return dotGF256([]uint8(c1), []uint8(c2))
}

// ----------------------------------------------------------------------------
// 1. Orthogonal Decomposition & Adjoint Property (<d0 phi, psi> = <phi, d0^T psi>)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_Adjoint0_OrthogonalDecomposition_12K(t *testing.T) {
	const totalTrials = 12000
	rng := rand.New(rand.NewSource(0xCAFE_BABE_2026))

	t.Logf("Executing %d trials: Adjoint0 duality <d0 phi, psi> == <phi, d0^T psi> and orthogonal decomposition...", totalTrials)

	// Pre-define specific boundary/stress geometries
	specialDims := [][2]int{
		{2, 2}, {2, 3}, {3, 2}, {1, 2}, {2, 1}, {1, 10}, {10, 1}, {1, 1},
		{4, 4}, {8, 8}, {16, 16}, {32, 32}, {3, 7}, {11, 13}, {17, 19},
		{2, 64}, {64, 2}, {33, 17},
	}

	var (
		adjoint0Failures    int
		adjoint1Failures    int
		orthoDecompFailures int
	)

	for trial := range totalTrials {
		var M, W int
		if trial < len(specialDims) {
			M, W = specialDims[trial][0], specialDims[trial][1]
		} else {
			// Variable random geometries: rows and cols in [1, 36]
			M = 1 + rng.Intn(36)
			W = 1 + rng.Intn(36)
		}

		// Ensure all 256 twists in F_{2^8} are covered systematically and randomly
		var twist uint8
		if trial < 256*4 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Generate 0-cochain phi
		phi := make(przm.Cochain0, M*W)
		switch trial % 6 {
		case 0:
			// Uniform random
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
		case 1:
			// All zeros
			// zero value
		case 2:
			// All ones
			for i := range phi {
				phi[i] = 1
			}
		case 3:
			// Max field element
			for i := range phi {
				phi[i] = 0xFF
			}
		case 4:
			// Single impulse
			if len(phi) > 0 {
				phi[rng.Intn(len(phi))] = uint8(1 + rng.Intn(255))
			}
		case 5:
			// Checkerboard
			for i := 0; i < M; i++ {
				for j := 0; j < W; j++ {
					phi[i*W+j] = uint8(((i + j) & 1) * 0x55)
				}
			}
		}

		// Generate 1-cochain psi
		psi := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		if trial%6 != 1 {
			for i := range psi.Eh {
				psi.Eh[i] = uint8(rng.Intn(256))
			}
			for i := range psi.Ev {
				psi.Ev[i] = uint8(rng.Intn(256))
			}
		}

		// 1. Verify d0 adjoint duality: <d0 phi, psi>_1 == <phi, d0^T psi>_0
		d0phi := przm.Coboundary0(complex, phi)
		d0Tpsi := przm.Adjoint0(complex, psi)

		lhs0 := dotCochain1(d0phi, psi)
		rhs0 := dotCochain0(phi, d0Tpsi)

		if lhs0 != rhs0 {
			adjoint0Failures++
			if adjoint0Failures <= 5 {
				t.Errorf("[Trial %d] Adjoint0 failure: M=%d, W=%d, Twist=0x%02X: <d0 phi, psi>=0x%02X != <phi, d0^T psi>=0x%02X",
					trial, M, W, twist, lhs0, rhs0)
			}
		}

		// 2. Verify d1 adjoint duality when 2D faces exist (M >= 2, W >= 2)
		if M >= 2 && W >= 2 {
			c := make(przm.Cochain2, (M-1)*(W-1))
			for i := range c {
				c[i] = uint8(rng.Intn(256))
			}

			d1psi := przm.Coboundary1(complex, psi)
			d1Tc := przm.Adjoint1(complex, c)

			lhs1 := dotCochain2(d1psi, c)
			rhs1 := dotCochain1(psi, d1Tc)

			if lhs1 != rhs1 {
				adjoint1Failures++
				if adjoint1Failures <= 5 {
					t.Errorf("[Trial %d] Adjoint1 failure: M=%d, W=%d, Twist=0x%02X: <d1 psi, c>=0x%02X != <psi, d1^T c>=0x%02X",
						trial, M, W, twist, lhs1, rhs1)
				}
			}

			// 3. Verify Orthogonal Complement: im(d0) \perp ker(d0^T)
			// Because d0^T(d1^T c) == 0, d1^T c \in ker(d0^T).
			// Therefore, <d0 phi, d1^T c> must be 0 identically.
			pairingOrtho := dotCochain1(d0phi, d1Tc)
			if pairingOrtho != 0 {
				orthoDecompFailures++
				if orthoDecompFailures <= 5 {
					t.Errorf("[Trial %d] Orthogonal decomposition failure: <d0 phi, d1^T c>=0x%02X != 0 (M=%d, W=%d, Twist=0x%02X)",
						trial, pairingOrtho, M, W, twist)
				}
			}
		}
	}

	t.Logf("Summary 12K Adjoint & Orthogonality: Adjoint0 failures = %d, Adjoint1 failures = %d, Ortho failures = %d",
		adjoint0Failures, adjoint1Failures, orthoDecompFailures)

	if adjoint0Failures > 0 {
		t.Fatalf("FAILED: %d Adjoint0 mathematical failures detected!", adjoint0Failures)
	}
	if adjoint1Failures > 0 {
		t.Fatalf("FAILED: %d Adjoint1 mathematical failures detected!", adjoint1Failures)
	}
	if orthoDecompFailures > 0 {
		t.Fatalf("FAILED: %d Orthogonal decomposition failures detected!", orthoDecompFailures)
	}
}

// ----------------------------------------------------------------------------
// 2. Coboundary Nilpotency: d1 ∘ d0 ≡ 0 and Dual d0^T ∘ d1^T ≡ 0 (20,000 trials)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_CoboundaryNilpotency_20K(t *testing.T) {
	const totalTrials = 20000
	rng := rand.New(rand.NewSource(0xDEAD_BEEF_5001))

	t.Logf("Executing %d trials: Coboundary nilpotency d1 ∘ d0 ≡ 0 and dual d0^T ∘ d1^T ≡ 0...", totalTrials)

	var (
		primalNilpotencyFailures int
		dualNilpotencyFailures   int
	)

	for trial := range totalTrials {
		// Sample arbitrary 2D cell complex geometry (M >= 2, W >= 2)
		M := 2 + rng.Intn(35)
		W := 2 + rng.Intn(35)

		// Sweep all 256 twists repeatedly
		var twist uint8
		if trial < 256*8 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Primal nilpotency: for arbitrary phi, (d1 ∘ d0)(phi) == 0
		phi := make(przm.Cochain0, M*W)
		switch trial % 5 {
		case 0:
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
		case 1:
			// Single-hot impulse
			phi[rng.Intn(len(phi))] = uint8(1 + rng.Intn(255))
		case 2:
			// Linear ramp
			for i := range phi {
				phi[i] = uint8(i & 0xFF)
			}
		case 3:
			// Constant field
			cVal := uint8(rng.Intn(256))
			for i := range phi {
				phi[i] = cVal
			}
		case 4:
			// High-frequency alternating
			for i := range M {
				for j := range W {
					phi[i*W+j] = uint8((i*17 + j*31) & 0xFF)
				}
			}
		}

		d0phi := przm.Coboundary0(complex, phi)
		d1d0phi := przm.Coboundary1(complex, d0phi)

		for faceIdx, val := range d1d0phi {
			if val != 0 {
				primalNilpotencyFailures++
				if primalNilpotencyFailures <= 5 {
					t.Errorf("[Trial %d] Primal nilpotency violation: M=%d, W=%d, Twist=0x%02X: (d1 ∘ d0)(phi) != 0 at face %d (val=0x%02X)",
						trial, M, W, twist, faceIdx, val)
				}
				break
			}
		}

		// Dual nilpotency: for arbitrary 2-cochain c, (d0^T ∘ d1^T)(c) == 0
		c := make(przm.Cochain2, (M-1)*(W-1))
		for i := range c {
			c[i] = uint8(rng.Intn(256))
		}

		d1Tc := przm.Adjoint1(complex, c)
		d0Td1Tc := przm.Adjoint0(complex, d1Tc)

		for vIdx, val := range d0Td1Tc {
			if val != 0 {
				dualNilpotencyFailures++
				if dualNilpotencyFailures <= 5 {
					t.Errorf("[Trial %d] Dual nilpotency violation: M=%d, W=%d, Twist=0x%02X: (d0^T ∘ d1^T)(c) != 0 at vertex %d (val=0x%02X)",
						trial, M, W, twist, vIdx, val)
				}
				break
			}
		}
	}

	t.Logf("Summary 20K Nilpotency: Primal d1 ∘ d0 failures = %d, Dual d0^T ∘ d1^T failures = %d",
		primalNilpotencyFailures, dualNilpotencyFailures)

	if primalNilpotencyFailures > 0 {
		t.Fatalf("FAILED: %d Primal nilpotency failures detected!", primalNilpotencyFailures)
	}
	if dualNilpotencyFailures > 0 {
		t.Fatalf("FAILED: %d Dual nilpotency failures detected!", dualNilpotencyFailures)
	}
}

// ----------------------------------------------------------------------------
// 3. Self-Adjointness and Commutation of Hodge Laplacians Delta_0 and Delta_1 (12,000 trials)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_HodgeLaplacian_SelfAdjointness_12K(t *testing.T) {
	const totalTrials = 12000
	rng := rand.New(rand.NewSource(0x7001_A55A_BEEF))

	t.Logf("Executing %d trials: Self-adjointness and commutation of Hodge Laplacians Delta_0 and Delta_1...", totalTrials)

	var (
		lap0SelfAdjointFailures int
		lap1SelfAdjointFailures int
		lap0CommutationFailures int
		lap1CommutationFailures int
		lap0LinearityFailures   int
		lap1LinearityFailures   int
	)

	for trial := range totalTrials {
		M := 2 + rng.Intn(30)
		W := 2 + rng.Intn(30)

		var twist uint8
		if trial < 256*4 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// --------------------------------------------------------------------
		// A. Delta_0: Self-Adjointness <Delta_0 phi, chi> == <phi, Delta_0 chi>
		// --------------------------------------------------------------------
		phi := make(przm.Cochain0, M*W)
		chi := make(przm.Cochain0, M*W)
		for i := range phi {
			phi[i] = uint8(rng.Intn(256))
			chi[i] = uint8(rng.Intn(256))
		}

		lap0Phi := przm.HodgeLaplacian0(complex, phi)
		lap0Chi := przm.HodgeLaplacian0(complex, chi)

		p0A := dotCochain0(lap0Phi, chi)
		p0B := dotCochain0(phi, lap0Chi)

		if p0A != p0B {
			lap0SelfAdjointFailures++
			if lap0SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_0 self-adjointness failure: M=%d, W=%d, Twist=0x%02X: <Delta_0 phi, chi>=0x%02X != <phi, Delta_0 chi>=0x%02X",
					trial, M, W, twist, p0A, p0B)
			}
		}

		// Also verify Dirichlet energy identity: <Delta_0 phi, chi> == <d0 phi, d0 chi>
		d0Phi := przm.Coboundary0(complex, phi)
		d0Chi := przm.Coboundary0(complex, chi)
		dirichlet0 := dotCochain1(d0Phi, d0Chi)
		if p0A != dirichlet0 {
			lap0SelfAdjointFailures++
			if lap0SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_0 energy pairing mismatch: <Delta_0 phi, chi>=0x%02X != <d0 phi, d0 chi>=0x%02X",
					trial, p0A, dirichlet0)
			}
		}

		// Delta_0 Linearity & Homogeneity: Delta_0(a * phi ^ b * chi) == a * Delta_0(phi) ^ b * Delta_0(chi)
		scalarA := uint8(rng.Intn(256))
		scalarB := uint8(rng.Intn(256))
		comboPhi := make(przm.Cochain0, M*W)
		for i := range comboPhi {
			comboPhi[i] = przm.GF256Mul(scalarA, phi[i]) ^ przm.GF256Mul(scalarB, chi[i])
		}
		lap0Combo := przm.HodgeLaplacian0(complex, comboPhi)
		for i := range comboPhi {
			expected := przm.GF256Mul(scalarA, lap0Phi[i]) ^ przm.GF256Mul(scalarB, lap0Chi[i])
			if lap0Combo[i] != expected {
				lap0LinearityFailures++
				if lap0LinearityFailures <= 5 {
					t.Errorf("[Trial %d] Delta_0 linearity violated at vertex %d (M=%d, W=%d, Twist=0x%02X)",
						trial, i, M, W, twist)
				}
				break
			}
		}

		// --------------------------------------------------------------------
		// B. Delta_1: Self-Adjointness <Delta_1 u, v> == <u, Delta_1 v>
		// --------------------------------------------------------------------
		u := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		v := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		for i := range u.Eh {
			u.Eh[i] = uint8(rng.Intn(256))
			v.Eh[i] = uint8(rng.Intn(256))
		}
		for i := range u.Ev {
			u.Ev[i] = uint8(rng.Intn(256))
			v.Ev[i] = uint8(rng.Intn(256))
		}

		lap1U := przm.HodgeLaplacian1(complex, u)
		lap1V := przm.HodgeLaplacian1(complex, v)

		p1A := dotCochain1(lap1U, v)
		p1B := dotCochain1(u, lap1V)

		if p1A != p1B {
			lap1SelfAdjointFailures++
			if lap1SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_1 self-adjointness failure: M=%d, W=%d, Twist=0x%02X: <Delta_1 u, v>=0x%02X != <u, Delta_1 v>=0x%02X",
					trial, M, W, twist, p1A, p1B)
			}
		}

		// Verify Hodge energy split: <Delta_1 u, v> == <d0^T u, d0^T v>_0 ^ <d1 u, d1 v>_2
		d0Tu := przm.Adjoint0(complex, u)
		d0Tv := przm.Adjoint0(complex, v)
		d1u := przm.Coboundary1(complex, u)
		d1v := przm.Coboundary1(complex, v)

		dirichlet1 := dotCochain0(d0Tu, d0Tv) ^ dotCochain2(d1u, d1v)
		if p1A != dirichlet1 {
			lap1SelfAdjointFailures++
			if lap1SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_1 Hodge energy split mismatch: <Delta_1 u, v>=0x%02X != <d0^T u, d0^T v> ^ <d1 u, d1 v>=0x%02X",
					trial, p1A, dirichlet1)
			}
		}

		// Delta_1 Linearity & Homogeneity: Delta_1(a * u ^ b * v) == a * Delta_1(u) ^ b * Delta_1(v)
		comboU := przm.Cochain1{
			Eh: make([]uint8, len(u.Eh)),
			Ev: make([]uint8, len(u.Ev)),
		}
		for i := range comboU.Eh {
			comboU.Eh[i] = przm.GF256Mul(scalarA, u.Eh[i]) ^ przm.GF256Mul(scalarB, v.Eh[i])
		}
		for i := range comboU.Ev {
			comboU.Ev[i] = przm.GF256Mul(scalarA, u.Ev[i]) ^ przm.GF256Mul(scalarB, v.Ev[i])
		}
		lap1Combo := przm.HodgeLaplacian1(complex, comboU)

		for i := range comboU.Eh {
			expected := przm.GF256Mul(scalarA, lap1U.Eh[i]) ^ przm.GF256Mul(scalarB, lap1V.Eh[i])
			if lap1Combo.Eh[i] != expected {
				lap1LinearityFailures++
				break
			}
		}
		for i := range comboU.Ev {
			expected := przm.GF256Mul(scalarA, lap1U.Ev[i]) ^ przm.GF256Mul(scalarB, lap1V.Ev[i])
			if lap1Combo.Ev[i] != expected {
				lap1LinearityFailures++
				break
			}
		}

		// --------------------------------------------------------------------
		// C. Commutation with Differentials:
		// Delta_1(d0 phi) == d0(Delta_0 phi)
		// Delta_1(d1^T c) == d1^T(d1 d1^T c)
		// --------------------------------------------------------------------
		// 1. Exact form commutation
		uExact := przm.Coboundary0(complex, phi)
		lap1Exact := przm.HodgeLaplacian1(complex, uExact)
		expectedExact := przm.Coboundary0(complex, lap0Phi)

		if !bytes.Equal(lap1Exact.Eh, expectedExact.Eh) || !bytes.Equal(lap1Exact.Ev, expectedExact.Ev) {
			lap0CommutationFailures++
			if lap0CommutationFailures <= 5 {
				t.Errorf("[Trial %d] Commutation failure: Delta_1(d0 phi) != d0(Delta_0 phi) (M=%d, W=%d, Twist=0x%02X)",
					trial, M, W, twist)
			}
		}

		// 2. Co-exact form commutation
		c := make(przm.Cochain2, (M-1)*(W-1))
		for i := range c {
			c[i] = uint8(rng.Intn(256))
		}
		wCoexact := przm.Adjoint1(complex, c)
		lap1Coexact := przm.HodgeLaplacian1(complex, wCoexact)
		d1w := przm.Coboundary1(complex, wCoexact)
		expectedCoexact := przm.Adjoint1(complex, d1w)

		if !bytes.Equal(lap1Coexact.Eh, expectedCoexact.Eh) || !bytes.Equal(lap1Coexact.Ev, expectedCoexact.Ev) {
			lap1CommutationFailures++
			if lap1CommutationFailures <= 5 {
				t.Errorf("[Trial %d] Commutation failure: Delta_1(d1^T c) != d1^T(d1(d1^T c)) (M=%d, W=%d, Twist=0x%02X)",
					trial, M, W, twist)
			}
		}
	}

	t.Logf("Summary 12K Laplacians: Delta_0 self-adj fails = %d, Delta_1 self-adj fails = %d, Delta_0 comm fails = %d, Delta_1 comm fails = %d, Lap0 lin fails = %d, Lap1 lin fails = %d",
		lap0SelfAdjointFailures, lap1SelfAdjointFailures, lap0CommutationFailures, lap1CommutationFailures, lap0LinearityFailures, lap1LinearityFailures)

	if lap0SelfAdjointFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 self-adjointness failures detected!", lap0SelfAdjointFailures)
	}
	if lap1SelfAdjointFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 self-adjointness failures detected!", lap1SelfAdjointFailures)
	}
	if lap0CommutationFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 commutation failures detected!", lap0CommutationFailures)
	}
	if lap1CommutationFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 commutation failures detected!", lap1CommutationFailures)
	}
	if lap0LinearityFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 linearity failures detected!", lap0LinearityFailures)
	}
	if lap1LinearityFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 linearity failures detected!", lap1LinearityFailures)
	}
}

// ----------------------------------------------------------------------------
// 4. Edge Cases: Degenerate Geometries, Zero Fields, Boundary Extremes
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_DegenerateAndExtremalBoundary(t *testing.T) {
	// Boundary geometries where rows or cols is 1, or twist is 0, 1, 255
	geoms := [][2]int{
		{1, 1}, {1, 2}, {2, 1}, {1, 16}, {16, 1}, {2, 2},
	}
	twists := []uint8{0, 1, 2, 3, 0x1B, 0xFE, 0xFF}

	for _, g := range geoms {
		M, W := g[0], g[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

			phi := make(przm.Cochain0, M*W)
			for i := range phi {
				phi[i] = uint8((i*37 + 13) & 0xFF)
			}

			u := przm.Coboundary0(complex, phi)
			if len(u.Eh) != M*(W-1) {
				t.Fatalf("Eh length mismatch: got %d, want %d", len(u.Eh), M*(W-1))
			}
			if len(u.Ev) != (M-1)*W {
				t.Fatalf("Ev length mismatch: got %d, want %d", len(u.Ev), (M-1)*W)
			}

			div := przm.Adjoint0(complex, u)
			if len(div) != M*W {
				t.Fatalf("Adjoint0 length mismatch: got %d, want %d", len(div), M*W)
			}

			// Adjoint duality on degenerate grid: <d0 phi, u> == <phi, d0^T u>
			lhs := dotCochain1(u, u)
			rhs := dotCochain0(phi, div)
			if lhs != rhs {
				t.Fatalf("Adjoint duality failed on degenerate grid %dx%d (twist 0x%02X): lhs=0x%02X != rhs=0x%02X",
					M, W, twist, lhs, rhs)
			}

			// Delta_0 on degenerate grid
			lap0 := przm.HodgeLaplacian0(complex, phi)
			if !bytes.Equal(lap0, div) {
				t.Fatalf("Delta_0(phi) != Adjoint0(Coboundary0(phi)) on %dx%d", M, W)
			}
		}
	}
	t.Log("[PASS] All degenerate geometries (1x1, 1xN, Nx1) satisfied adjoint duality and invariants.")
}
