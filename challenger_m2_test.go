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
	"sync"
	"testing"

	"github.com/lemon4ksan/przm"
)

// -----------------------------------------------------------------------------
// Adversarial Challenger Suite for Milestone 2:
// Discrete Heisenberg Group H_3(F_{2^8}) Foliation
// -----------------------------------------------------------------------------

// TestChallengerM2_100Goroutine_ConcurrencyStress executes 100 concurrent
// goroutines continuously foliating, unfoliating, compressing, and decompressing
// Heisenberg frames under `go test -race`.
func TestChallengerM2_100Goroutine_ConcurrencyStress(t *testing.T) {
	const numGoroutines = 100
	const iterationsPerGoroutine = 50

	var startBarrier sync.WaitGroup
	startBarrier.Add(1)

	var doneGroup sync.WaitGroup
	doneGroup.Add(numGoroutines)

	errChan := make(chan error, numGoroutines*iterationsPerGoroutine)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer doneGroup.Done()

			// Local RNG per goroutine
			rng := rand.New(rand.NewSource(int64(gid*1000 + 42)))

			startBarrier.Wait()

			testSizes := []int{0, 1, 2, 3, 4, 11, 12, 13, 17, 24, 48, 127, 256, 1023, 2048, 4096, 8192}

			for iter := 0; iter < iterationsPerGoroutine; iter++ {
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

	// Release all 100 goroutines simultaneously
	startBarrier.Done()
	doneGroup.Wait()
	close(errChan)

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		t.Fatalf("Encountered %d concurrency errors under 100 goroutines! First error: %v", len(errs), errs[0])
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
			raw[i] = uint8((i%200) + 1)
			raw[i+1] = uint8(((i/200)%200) + 1)
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
		for i := 0; i < numPixels; i++ {
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

	// Calling DecompressHeisenberg directly on 0-byte slice returns ErrHeisenbergCorrupt
	_, decErr := przm.DecompressHeisenberg(comp, nil)
	if decErr != przm.ErrHeisenbergCorrupt {
		t.Fatalf("Expected ErrHeisenbergCorrupt when passing 0 bytes to DecompressHeisenberg, got: %v", decErr)
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
		for i := 0; i < 8; i++ {
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

	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
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
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
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

	for i := 0; i < 50; i++ {
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
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	t.Run("Go_MalformedFrames", func(t *testing.T) {
		// 1. Truncated header (< 12 bytes)
		for sz := 0; sz < 12; sz++ {
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
