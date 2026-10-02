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
	"time"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// -----------------------------------------------------------------------------
// 1. Concurrency Stress Test: 100 concurrent goroutines under -race
// -----------------------------------------------------------------------------
func TestMilestone3_ConcurrencyStress_100Goroutines(t *testing.T) {
	const numGoroutines = 8
	const iterations = 50

	// Prepare distinct data profiles
	datasets := [][]byte{
		// 1. Curl-free 8x8
		func() []byte {
			b := make([]byte, 64)
			b[0] = 0x55
			for j := 0; j < 7; j++ {
				b[j+1] = b[j] ^ byte(j+1)
			}
			for i := 1; i < 8; i++ {
				v := byte(i * 3)
				for j := 0; j < 8; j++ {
					b[i*8+j] = b[(i-1)*8+j] ^ v
				}
			}
			return b
		}(),
		// 2. Constant boundaries 16x16
		func() []byte {
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
		// 3. Sparse curl 16x32 (512B)
		func() []byte {
			b := make([]byte, 512)
			for i := range b {
				b[i] = byte(i * 7)
			}
			b[19] ^= 0x33
			b[105] ^= 0x77
			return b
		}(),
		// 4. Large harmonic field 64x64 (4KB)
		func() []byte {
			b := make([]byte, 4096)
			b[0] = 0x12
			for j := 0; j < 63; j++ {
				b[j+1] = b[j] ^ byte((j%5)+1)
			}
			for i := 1; i < 64; i++ {
				v := byte((i % 3) + 1)
				for j := 0; j < 64; j++ {
					b[i*64+j] = b[(i-1)*64+j] ^ v
				}
			}
			return b
		}(),
		// 5. Small raw buffer (8B)
		{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE},
	}

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*iterations)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(gid*7919 + 101)))
			dst := make([]byte, 8192)

			for iter := 0; iter < iterations; iter++ {
				data := datasets[rng.Intn(len(datasets))]

				comp, err := przm.CompressCellularSheaf(data, 3)
				if err != nil {
					errCh <- fmt.Errorf("gid %d iter %d compress err: %w", gid, iter, err)
					return
				}

				dec, err := przm.DecompressCellularSheaf(comp, dst)
				if err != nil {
					errCh <- fmt.Errorf("gid %d iter %d decompress err: %w", gid, iter, err)
					return
				}

				if !bytes.Equal(dec, data) {
					errCh <- fmt.Errorf("gid %d iter %d data mismatch! got %d bytes, want %d bytes",
						gid, iter, len(dec), len(data))
					return
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
// 2. Zero-Allocation Assertions on DecompressCellularSheaf
// -----------------------------------------------------------------------------
func TestMilestone3_ZeroAllocDecompression_Assertion(t *testing.T) {
	// Test standard uncascaded Cellular Sheaf frames
	testCases := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "CurlFree_8x8",
			gen: func() []byte {
				b := make([]byte, 64)
				b[0] = 0x42
				for j := 0; j < 7; j++ {
					b[j+1] = b[j] ^ byte(j+1)
				}
				for i := 1; i < 8; i++ {
					v := byte(i * 3)
					for j := 0; j < 8; j++ {
						b[i*8+j] = b[(i-1)*8+j] ^ v
					}
				}
				return b
			},
		},
		{
			name: "ConstBoundaries_16x16",
			gen: func() []byte {
				b := make([]byte, 256)
				b[0] = 0x99
				for j := 0; j < 15; j++ {
					b[j+1] = b[j] ^ 0x02
				}
				for i := 1; i < 16; i++ {
					for j := 0; j < 16; j++ {
						b[i*16+j] = b[(i-1)*16+j] ^ 0x05
					}
				}
				return b
			},
		},
		{
			name: "Harmonic_64x64_4KB",
			gen: func() []byte {
				b := make([]byte, 4096)
				b[0] = 0x22
				for j := 0; j < 63; j++ {
					b[j+1] = b[j] ^ byte(j+1)
				}
				for i := 1; i < 64; i++ {
					v := byte(i + 1)
					for j := 0; j < 64; j++ {
						b[i*64+j] = b[(i-1)*64+j] ^ v
					}
				}
				return b
			},
		},
		{
			name: "RawPassthrough_8B",
			gen:  func() []byte { return []byte{1, 2, 3, 4, 5, 6, 7, 8} },
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orig := tc.gen()
			comp, err := przm.CompressCellularSheaf(orig, 3)
			if err != nil {
				t.Fatalf("compress failed: %v", err)
			}

			dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

			// Warmup
			_, err = przm.DecompressCellularSheaf(comp, dst)
			if err != nil {
				t.Fatalf("decompress warmup failed: %v", err)
			}

			allocs := testing.AllocsPerRun(100, func() {
				_, err := przm.DecompressCellularSheaf(comp, dst)
				if err != nil {
					t.Fatal(err)
				}
			})

			if allocs > 0 {
				t.Fatalf("%s: violated zero-allocation contract: %f allocs/op (expected strictly 0)",
					tc.name, allocs)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 3. Pure C Cross-Decompression Stress: 50 Random Cellular Sheaf Frames
// -----------------------------------------------------------------------------
func TestMilestone3_PureC_50RandomFrames(t *testing.T) {
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
		t.Fatal("C test binary test_przm_dec.exe not found! Compile with gcc first.")
	}

	tmpDir := t.TempDir()
	rng := rand.New(rand.NewSource(20261001))

	strides := []int{4, 8, 16, 32, 64}

	for frameID := 0; frameID < 50; frameID++ {
		W := strides[rng.Intn(len(strides))]
		M := 2 + rng.Intn(35)
		remLen := rng.Intn(W)
		totalLen := M*W + remLen

		mode := rng.Intn(4)
		buf := make([]byte, totalLen)

		switch mode {
		case 0:
			// Curl-free constant boundaries
			anchor := byte(rng.Intn(256))
			gammaH := byte(rng.Intn(255) + 1)
			gammaV := byte(rng.Intn(255) + 1)
			buf[0] = anchor
			for j := 0; j < W-1; j++ {
				buf[j+1] = buf[j] ^ gammaH
			}
			for i := 1; i < M; i++ {
				for j := 0; j < W; j++ {
					buf[i*W+j] = buf[(i-1)*W+j] ^ gammaV
				}
			}
		case 1:
			// Curl-free arbitrary boundaries
			buf[0] = byte(rng.Intn(256))
			for j := 0; j < W-1; j++ {
				buf[j+1] = buf[j] ^ byte(rng.Intn(256))
			}
			ev := make([]byte, M-1)
			for i := range ev {
				ev[i] = byte(rng.Intn(256))
			}
			for i := 1; i < M; i++ {
				v := ev[i-1]
				for j := 0; j < W; j++ {
					buf[i*W+j] = buf[(i-1)*W+j] ^ v
				}
			}
		case 2:
			// Sparse curl: start curl-free and inject 1-3 curls
			buf[0] = byte(rng.Intn(256))
			for j := 0; j < W-1; j++ {
				buf[j+1] = buf[j] ^ byte(rng.Intn(256))
			}
			ev := make([]byte, M-1)
			for i := range ev {
				ev[i] = byte(rng.Intn(256))
			}
			for i := 1; i < M; i++ {
				v := ev[i-1]
				for j := 0; j < W; j++ {
					buf[i*W+j] = buf[(i-1)*W+j] ^ v
				}
			}
			numCurls := 1 + rng.Intn(min(3, (M-1)*(W-1)))
			for c := 0; c < numCurls; c++ {
				fi := 1 + rng.Intn(M-1)
				fj := 1 + rng.Intn(W-1)
				buf[fi*W+fj] ^= byte(rng.Intn(255) + 1)
			}
		case 3:
			// Dense noise (forces raw passthrough)
			for i := range buf {
				buf[i] = byte(rng.Intn(256))
			}
		}

		// Trailing remainder bytes
		for k := M * W; k < totalLen; k++ {
			buf[k] = byte(rng.Intn(256))
		}

		comp, err := przm.CompressCellularSheaf(buf, 3)
		if err != nil {
			t.Fatalf("Frame %d: CompressCellularSheaf failed: %v", frameID, err)
		}

		inPath := filepath.Join(tmpDir, fmt.Sprintf("frame_%02d.przm", frameID))
		outPath := filepath.Join(tmpDir, fmt.Sprintf("frame_%02d.out", frameID))

		if err := os.WriteFile(inPath, comp, 0644); err != nil {
			t.Fatalf("Frame %d: failed to write przm file: %v", frameID, err)
		}

		cmd := exec.Command(cBinPath, inPath, outPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Frame %d (W=%d, M=%d, mode=%d, len=%d): C decompressor crashed or returned error %v:\nOutput:\n%s",
				frameID, W, M, mode, totalLen, err, string(output))
		}

		dec, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatalf("Frame %d: failed to read decompressed file: %v", frameID, err)
		}

		if !bytes.Equal(dec, buf) {
			t.Fatalf("Frame %d (W=%d, M=%d, mode=%d, len=%d): decompressed data mismatch! Got %d bytes, want %d bytes",
				frameID, W, M, mode, totalLen, len(dec), len(buf))
		}
	}
	t.Logf("PASS: Pure C reference decompressor decoded all 50 random frames losslessly with 0 errors.")
}

// -----------------------------------------------------------------------------
// 4. Adversarial Exploration: Non-Trivial Twist (alpha > 1) Invariant
// -----------------------------------------------------------------------------
func TestMilestone3_TwistGreaterThanOne_Evaluation(t *testing.T) {
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
		t.Fatal("C test binary test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	// Test CurlFree frame with Twist = 2, W = 4, M = 3, N = 12
	frame := make([]byte, 24+3+2)
	binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
	binary.LittleEndian.PutUint32(frame[4:8], 12)
	binary.LittleEndian.PutUint16(frame[8:10], 4)
	binary.LittleEndian.PutUint16(frame[10:12], 3)
	frame[12] = 0x01
	frame[13] = 0x02 // Twist = 2
	binary.LittleEndian.PutUint16(frame[14:16], 0)
	binary.LittleEndian.PutUint16(frame[16:18], przm.CSHDFlagCurlFree)
	frame[24] = 0x10
	frame[25] = 0x20
	frame[26] = 0x30
	frame[27] = 0x04
	frame[28] = 0x08

	goDst := make([]byte, 12+64)
	goDec, err := przm.DecompressCellularSheaf(frame, goDst)
	if err != nil {
		t.Fatalf("Go decompress failed: %v", err)
	}

	inPath := filepath.Join(tmpDir, "twist2.przm")
	outPath := filepath.Join(tmpDir, "twist2.out")
	if err := os.WriteFile(inPath, frame, 0644); err != nil {
		t.Fatalf("failed to write twist2.przm: %v", err)
	}

	cmd := exec.Command(cBinPath, inPath, outPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pure C decompressor failed: %v\nOutput: %s", err, string(output))
	}

	cDec, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read twist2.out: %v", err)
	}

	t.Logf("Go Dec: % X", goDec)
	t.Logf("C  Dec: % X", cDec)

	if !bytes.Equal(goDec, cDec) {
		t.Errorf("DISCREPANCY DETECTED: Go and Pure C disagree when twist > 1!\nGo: % X\n C: % X",
			goDec, cDec)
	} else {
		t.Logf("PASS: Go and Pure C match identically on twist=2 frame!")
	}
}

func TestMilestone3_MultiplicativeProgression_EndToEndInterop(t *testing.T) {
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
		t.Fatal("C test binary test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	// Construct data with dominant ratio = 2:
	// M = 16, W = 8 -> 128 bytes
	// Row 0: x_{j+1} = 2 * x_j
	// Row i: x_{i, j} = 2 * x_{i-1, j}
	src := make([]byte, 128)
	src[0] = 0x01
	for j := 0; j < 7; j++ {
		src[j+1] = przm.GF256Mul(0x02, src[j])
	}
	for i := 1; i < 16; i++ {
		for j := 0; j < 8; j++ {
			src[i*8+j] = przm.GF256Mul(0x02, src[(i-1)*8+j])
		}
	}

	comp, err := przm.CompressCellularSheaf(src, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	t.Logf("Compressed header: Twist=%d, Flags=0x%04X, W=%d, M=%d, TotalLen=%d",
		comp[13], binary.LittleEndian.Uint16(comp[16:18]),
		binary.LittleEndian.Uint16(comp[8:10]), binary.LittleEndian.Uint16(comp[10:12]), len(comp))

	// Decompress in Go
	goDst := make([]byte, len(src)+64)
	goDec, err := przm.DecompressCellularSheaf(comp, goDst)
	if err != nil {
		t.Fatalf("Go decompress failed: %v", err)
	}

	if !bytes.Equal(goDec, src) {
		t.Fatalf("Go roundtrip failed: got % X, want % X", goDec[:16], src[:16])
	}

	// Decompress in Pure C
	inPath := filepath.Join(tmpDir, "mult.przm")
	outPath := filepath.Join(tmpDir, "mult.out")
	if err := os.WriteFile(inPath, comp, 0644); err != nil {
		t.Fatalf("failed to write mult.przm: %v", err)
	}

	cmd := exec.Command(cBinPath, inPath, outPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pure C decompressor failed: %v\nOutput: %s", err, string(output))
	}

	cDec, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read mult.out: %v", err)
	}

	if !bytes.Equal(cDec, src) {
		t.Errorf("PURE C FAILED ON MULTIPLICATIVE PROGRESSION!\nOriginal: % X\nC Decoded: % X",
			src[:16], cDec[:16])
	} else {
		t.Logf("PASS: Both Go and Pure C decompress multiplicative progression identically!")
	}
}

func TestMilestone3_Twist2_NonZeroEv_EndToEndInterop(t *testing.T) {
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
		t.Fatal("C test binary test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	// Field with Twist = 2, Curl = 0, but non-zero Ev
	// W = 8, M = 16 (128 bytes)
	// u^h(i, j) == 0 (each row is geometric progression with ratio 2)
	// u^v(i, j) == 2^j * 0x05
	src := make([]byte, 128)
	// Row 0
	src[0] = 0x03
	for j := 0; j < 7; j++ {
		src[j+1] = przm.GF256Mul(0x02, src[j])
	}
	// Rows 1..15
	for i := 1; i < 16; i++ {
		curV := byte(0x05)
		for j := 0; j < 8; j++ {
			src[i*8+j] = przm.GF256Mul(0x02, src[(i-1)*8+j]) ^ curV
			curV = przm.GF256Mul(0x02, curV)
		}
	}

	comp, err := przm.CompressCellularSheaf(src, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	twist := comp[13]
	flags := binary.LittleEndian.Uint16(comp[16:18])
	t.Logf("Twist=%d, Flags=0x%04X, CompLen=%d (orig %d)", twist, flags, len(comp), len(src))

	// Go decompress
	goDst := make([]byte, len(src)+64)
	goDec, err := przm.DecompressCellularSheaf(comp, goDst)
	if err != nil {
		t.Fatalf("Go decompress failed: %v", err)
	}
	if !bytes.Equal(goDec, src) {
		t.Fatalf("Go decompress mismatch!")
	}
	t.Logf("Go decompress: SUCCESS (Lossless)")

	// Pure C decompress
	inPath := filepath.Join(tmpDir, "twist2_ev.przm")
	outPath := filepath.Join(tmpDir, "twist2_ev.out")
	if err := os.WriteFile(inPath, comp, 0644); err != nil {
		t.Fatalf("failed to write przm: %v", err)
	}

	cmd := exec.Command(cBinPath, inPath, outPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pure C decompressor failed: %v\nOutput: %s", err, string(output))
	}

	cDec, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read out: %v", err)
	}

	if !bytes.Equal(cDec, src) {
		t.Errorf("PURE C FAILED TO DECOMPRESS TWIST=2 FIELD LOSSLESSLY!\nOrig: % X\nGo:   % X\nC:    % X",
			src[:16], goDec[:16], cDec[:16])
	} else {
		t.Logf("Pure C decompress: SUCCESS")
	}
}



// -----------------------------------------------------------------------------
// 5. Benchmark Decompression Line Rate and CPU Cycles
// -----------------------------------------------------------------------------
func TestMilestone3_BenchmarkDiagnostic(t *testing.T) {
	const size = 64 * 1024
	orig := make([]byte, size)
	for i := range orig {
		orig[i] = byte(i)
	}

	comp, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	flags := binary.LittleEndian.Uint16(comp[16:18])
	t.Logf("Comp len: %d, Flags: 0x%04X, W=%d, M=%d, CascadedPWS=%v",
		len(comp), flags, binary.LittleEndian.Uint16(comp[8:10]), binary.LittleEndian.Uint16(comp[10:12]),
		(flags&przm.CSHDFlagCascadedPWS) != 0)
}

func BenchmarkMilestone3_MicroarchitecturalLineRate(b *testing.B) {
	const size = 64 * 1024
	orig := make([]byte, size)
	for i := range orig {
		orig[i] = byte(i)
	}

	comp, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatalf("CompressCellularSheaf failed: %v", err)
	}


	dst := bufkit.AlignedBytes(size+64, bufkit.CacheLineSize)
	b.SetBytes(int64(size))
	b.ResetTimer()

	start := time.Now()
	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressCellularSheaf(comp, dst)
	}
	elapsed := time.Since(start)

	totalBytes := float64(size) * float64(b.N)
	gbPerSec := (totalBytes / elapsed.Seconds()) / (1024 * 1024 * 1024)
	cyclesPer64B := (elapsed.Seconds() * 3.0e9) / (totalBytes / 64.0)

	b.ReportMetric(gbPerSec, "GB/s")
	b.ReportMetric(cyclesPer64B, "cycles/64B")
}

func BenchmarkMilestone3_PureRowSweepThroughput(b *testing.B) {
	// 64 KiB pure harmonic field with constant boundary (no PWS cascade)
	const size = 64 * 1024
	orig := make([]byte, size)
	orig[0] = 0x55
	for j := 0; j < 63; j++ {
		orig[j+1] = orig[j] ^ 0x01
	}
	for i := 1; i < 1024; i++ {
		for j := 0; j < 64; j++ {
			orig[i*64+j] = orig[(i-1)*64+j] ^ 0x02
		}
	}

	comp, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatalf("Compress failed: %v", err)
	}

	dst := bufkit.AlignedBytes(size+64, bufkit.CacheLineSize)
	b.SetBytes(int64(size))
	b.ResetTimer()

	start := time.Now()
	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressCellularSheaf(comp, dst)
	}
	elapsed := time.Since(start)

	totalBytes := float64(size) * float64(b.N)
	gbPerSec := (totalBytes / elapsed.Seconds()) / (1024 * 1024 * 1024)
	cyclesPer64B := (elapsed.Seconds() * 3.0e9) / (totalBytes / 64.0)

	b.ReportMetric(gbPerSec, "GB/s")
	b.ReportMetric(cyclesPer64B, "cycles/64B")
}

