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

	"github.com/lemon4ksan/przm"
)

// Helper to locate compiled Pure C decompressor binary
func getCBinPath(t *testing.T) string {
	t.Helper()
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// 1. TestChallenger_IsoQuant_UnalignedLengths tests all buffer sizes N in [0..256] and key primes up to 1027,
// specifically stressing non-multiple-of-4 buffer lengths (N % 4 != 0), empty buffers, 1-byte, 2-byte, 3-byte inputs.
func TestChallenger_IsoQuant_UnalignedLengths(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	// Exhaustive 0..256 + unaligned boundaries around 512 and 1024
	var testSizes []int
	for sz := 0; sz <= 256; sz++ {
		testSizes = append(testSizes, sz)
	}
	for _, sz := range []int{257, 258, 259, 260, 511, 513, 514, 515, 1021, 1022, 1023, 1024, 1025, 1026, 1027} {
		testSizes = append(testSizes, sz)
	}

	patterns := []struct {
		name string
		gen  func(sz int) []byte
	}{
		{
			name: "Zeroes",
			gen: func(sz int) []byte {
				return make([]byte, sz)
			},
		},
		{
			name: "Repetitive_42",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range b {
					b[i] = 0x42
				}
				return b
			},
		},
		{
			name: "MultiChannelCorrelated",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := 0; i < sz; i++ {
					quadIdx := i / 4
					lane := i % 4
					switch lane {
					case 0:
						b[i] = uint8(quadIdx % 128)
					case 1:
						b[i] = uint8((quadIdx % 128) ^ 0x05)
					case 2:
						b[i] = uint8((quadIdx % 128) ^ 0x0A)
					case 3:
						b[i] = 0xFF
					}
				}
				return b
			},
		},
		{
			name: "PseudoRandom",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range b {
					b[i] = uint8((i*109 + 37) % 256)
				}
				return b
			},
		},
	}

	for _, pat := range patterns {
		for _, sz := range testSizes {
			data := pat.gen(sz)
			testName := fmt.Sprintf("%s_sz%d", pat.name, sz)

			// 1. Go Compression
			comp, err := przm.CompressIsoQuant(data)
			if err != nil {
				t.Fatalf("[%s] CompressIsoQuant failed: %v", testName, err)
			}
			if len(comp) < przm.IsoQuantHeaderSize {
				t.Fatalf("[%s] Compressed buffer too small for header: %d", testName, len(comp))
			}

			// 2. Go Decompression
			decGo, err := przm.DecompressIsoQuant(comp, nil)
			if err != nil {
				t.Fatalf("[%s] DecompressIsoQuant failed: %v", testName, err)
			}
			if !bytes.Equal(decGo, data) {
				t.Fatalf("[%s] Go decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decGo), len(data))
			}

			// 3. Global Go Decompress dispatch
			decGlobal, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("[%s] Global Decompress failed: %v", testName, err)
			}
			if !bytes.Equal(decGlobal, data) {
				t.Fatalf("[%s] Global Decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decGlobal), len(data))
			}

			// 4. Pure C Decompression (sample across sizes to maintain speed)
			if sz <= 32 || sz%7 == 0 || sz == 1023 || sz == 1025 {
				inPrzm := filepath.Join(tmpDir, testName+".przm")
				outBin := filepath.Join(tmpDir, testName+".out")

				if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
					t.Fatalf("[%s] WriteFile failed: %v", testName, err)
				}

				cmd := exec.Command(cBin, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("[%s] Pure C decompressor failed (exit status %v):\nOutput:\n%s", testName, err, string(output))
				}

				decC, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("[%s] ReadFile failed: %v", testName, err)
				}

				if !bytes.Equal(decC, data) {
					t.Fatalf("[%s] Pure C decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decC), len(data))
				}

				_ = os.Remove(inPrzm)
				_ = os.Remove(outBin)
			}
		}
	}
}

// 2. TestChallenger_IsoQuant_ManualModes_Unaligned forces Mode 1 (Collimated) and Mode 3 (Cascaded PWS)
// on deliberately unaligned lengths (N % 4 != 0) to stress test trailing byte handling in both Go and Pure C.
func TestChallenger_IsoQuant_ManualModes_Unaligned(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	unalignedSizes := []int{0, 1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15, 17, 18, 19, 33, 65, 127, 255, 1023}

	qL := przm.Quaternion4{1, 0x33, 0x33, 0}
	qR := przm.Quaternion4{1, 0, 0x77, 0x77}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	for _, sz := range unalignedSizes {
		raw := make([]byte, sz)
		for i := range raw {
			raw[i] = uint8((i*17 + 43) % 256)
		}

		// --- Mode 1: Manual Collimated Frame ---
		collimated := make([]byte, sz)
		przm.IsoQuantCollimation4D(collimated, raw, qL, qR)

		mode1Frame := make([]byte, przm.IsoQuantHeaderSize+sz)
		binary.LittleEndian.PutUint32(mode1Frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
		mode1Frame[4] = przm.IsoQuantModeCollimated
		binary.LittleEndian.PutUint32(mode1Frame[8:12], uint32(sz))
		copy(mode1Frame[12:16], qLInv[:])
		copy(mode1Frame[16:20], qRInv[:])
		copy(mode1Frame[przm.IsoQuantHeaderSize:], collimated)

		// Decompress in Go
		decGo1, err := przm.DecompressIsoQuant(mode1Frame, nil)
		if err != nil {
			t.Fatalf("[Mode1_sz%d] Go DecompressIsoQuant failed: %v", sz, err)
		}
		if !bytes.Equal(decGo1, raw) {
			t.Fatalf("[Mode1_sz%d] Go DecompressIsoQuant data mismatch!", sz)
		}

		// Decompress in Pure C
		inPrzm1 := filepath.Join(tmpDir, fmt.Sprintf("mode1_sz%d.przm", sz))
		outBin1 := filepath.Join(tmpDir, fmt.Sprintf("mode1_sz%d.out", sz))
		if err := os.WriteFile(inPrzm1, mode1Frame, 0644); err != nil {
			t.Fatal(err)
		}
		cmd1 := exec.Command(cBin, inPrzm1, outBin1)
		output1, err := cmd1.CombinedOutput()
		if err != nil {
			t.Fatalf("[Mode1_sz%d] Pure C failed:\n%s", sz, string(output1))
		}
		decC1, err := os.ReadFile(outBin1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decC1, raw) {
			t.Fatalf("[Mode1_sz%d] Pure C data mismatch!", sz)
		}

		// --- Mode 3: Manual Cascaded PWS Frame (for sz >= 1) ---
		if sz > 0 {
			pwsPacked, err := przm.PWSEncode(collimated)
			if err != nil {
				t.Fatalf("[Mode3_sz%d] PWSEncode failed: %v", sz, err)
			}

			mode3Frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
			binary.LittleEndian.PutUint32(mode3Frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
			mode3Frame[4] = przm.IsoQuantModeCompressed
			binary.LittleEndian.PutUint32(mode3Frame[8:12], uint32(sz))
			copy(mode3Frame[12:16], qLInv[:])
			copy(mode3Frame[16:20], qRInv[:])
			copy(mode3Frame[przm.IsoQuantHeaderSize:], pwsPacked)

			// Decompress in Go
			decGo3, err := przm.DecompressIsoQuant(mode3Frame, nil)
			if err != nil {
				t.Fatalf("[Mode3_sz%d] Go DecompressIsoQuant failed: %v", sz, err)
			}
			if !bytes.Equal(decGo3, raw) {
				t.Fatalf("[Mode3_sz%d] Go DecompressIsoQuant data mismatch!", sz)
			}

			// Decompress in Pure C
			inPrzm3 := filepath.Join(tmpDir, fmt.Sprintf("mode3_sz%d.przm", sz))
			outBin3 := filepath.Join(tmpDir, fmt.Sprintf("mode3_sz%d.out", sz))
			if err := os.WriteFile(inPrzm3, mode3Frame, 0644); err != nil {
				t.Fatal(err)
			}
			cmd3 := exec.Command(cBin, inPrzm3, outBin3)
			output3, err := cmd3.CombinedOutput()
			if err != nil {
				t.Fatalf("[Mode3_sz%d] Pure C failed:\n%s", sz, string(output3))
			}
			decC3, err := os.ReadFile(outBin3)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decC3, raw) {
				t.Fatalf("[Mode3_sz%d] Pure C data mismatch!", sz)
			}
		}
	}
}

// 3. TestChallenger_IsoQuant_AlgebraFuzz stresses Hamilton quaternion algebra properties
// across 2000 random quaternions: invertibility, commutativity, associativity.
func TestChallenger_IsoQuant_AlgebraFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	ident := przm.IdentityQuaternion4()

	for iter := 0; iter < 2000; iter++ {
		p := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		q := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		r := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}

		// Commutativity: p * q == q * p
		pq := p.Mul(q)
		qp := q.Mul(p)
		if pq != qp {
			t.Fatalf("Algebra commutativity failed: p*q=%v, q*p=%v", pq, qp)
		}

		// Associativity: (p * q) * r == p * (q * r)
		pqr1 := pq.Mul(r)
		pqr2 := p.Mul(q.Mul(r))
		if pqr1 != pqr2 {
			t.Fatalf("Algebra associativity failed: (p*q)*r=%v, p*(q*r)=%v", pqr1, pqr2)
		}

		// Inversion
		s := p[0] ^ p[1] ^ p[2] ^ p[3]
		invP, ok := p.Inv()
		if s == 0 {
			if ok {
				t.Fatalf("Expected singular quaternion for sum 0: p=%v", p)
			}
		} else {
			if !ok {
				t.Fatalf("Expected invertible quaternion for sum %d != 0: p=%v", s, p)
			}
			prod := p.Mul(invP)
			if prod != ident {
				t.Fatalf("Inverse identity failed: p*inv(p)=%v, want %v", prod, ident)
			}
		}
	}
}

// 4. TestChallenger_IsoQuant_MalformedAndAdversarialFrames verifies that corrupt, truncated,
// and malformed IsoQuant frames return errors gracefully without panic or C segmentation fault.
func TestChallenger_IsoQuant_MalformedAndAdversarialFrames(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	validFrame, err := przm.CompressIsoQuant([]byte("Hello world test 12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Truncated headers: 0 to 19 bytes
	for length := 0; length < przm.IsoQuantHeaderSize; length++ {
		trunc := validFrame[:length]

		// Go test
		_, err := przm.DecompressIsoQuant(trunc, nil)
		if err != przm.ErrIsoQuantCorrupt {
			t.Fatalf("Expected ErrIsoQuantCorrupt on header truncation %d, got %v", length, err)
		}

		// C test
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("trunc_%d.przm", length))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("trunc_%d.out", length))
		_ = os.WriteFile(inPrzm, trunc, 0644)

		cmd := exec.Command(cBin, inPrzm, outBin)
		_ = cmd.Run()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("C decompressor unexpectedly succeeded on truncated header %d", length)
		}
	}

	// 2. Unsupported modes: mode = 2, 4, 255
	for _, badMode := range []uint8{2, 4, 5, 255} {
		corrupt := make([]byte, len(validFrame))
		copy(corrupt, validFrame)
		corrupt[4] = badMode

		// Go test
		_, err := przm.DecompressIsoQuant(corrupt, nil)
		if err != przm.ErrIsoQuantUnsupportedMode {
			t.Fatalf("Expected ErrIsoQuantUnsupportedMode on bad mode %d, got %v", badMode, err)
		}

		// C test
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("badmode_%d.przm", badMode))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("badmode_%d.out", badMode))
		_ = os.WriteFile(inPrzm, corrupt, 0644)

		cmd := exec.Command(cBin, inPrzm, outBin)
		_ = cmd.Run()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("C decompressor unexpectedly succeeded on unsupported mode %d", badMode)
		}
	}

	// 3. Truncated payload: header claims 100000 bytes, but payload is only 10 bytes
	corruptPayload := make([]byte, len(validFrame))
	copy(corruptPayload, validFrame)
	binary.LittleEndian.PutUint32(corruptPayload[8:12], 100000)

	// Go test
	_, err = przm.DecompressIsoQuant(corruptPayload, nil)
	if err == nil {
		t.Fatal("Expected error on truncated payload, got nil")
	}

	// C test
	inPrzm := filepath.Join(tmpDir, "trunc_payload.przm")
	outBin := filepath.Join(tmpDir, "trunc_payload.out")
	_ = os.WriteFile(inPrzm, corruptPayload, 0644)

	cmd := exec.Command(cBin, inPrzm, outBin)
	_ = cmd.Run()
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		t.Fatal("C decompressor unexpectedly succeeded on truncated payload")
	}

	// 4. Excessive uncompressed size: orig_len > MaxBlockSize (16 MiB)
	corruptMaxBlock := make([]byte, len(validFrame))
	copy(corruptMaxBlock, validFrame)
	binary.LittleEndian.PutUint32(corruptMaxBlock[8:12], 32*1024*1024) // 32 MiB

	// Go test
	_, err = przm.DecompressIsoQuant(corruptMaxBlock, nil)
	if err != przm.ErrExceedsMaxBlock {
		t.Fatalf("Expected ErrExceedsMaxBlock, got %v", err)
	}

	// C test
	inPrzmMax := filepath.Join(tmpDir, "max_block.przm")
	outBinMax := filepath.Join(tmpDir, "max_block.out")
	_ = os.WriteFile(inPrzmMax, corruptMaxBlock, 0644)

	cmdMax := exec.Command(cBin, inPrzmMax, outBinMax)
	_ = cmdMax.Run()
	if cmdMax.ProcessState != nil && cmdMax.ProcessState.ExitCode() == 0 {
		t.Fatal("C decompressor unexpectedly succeeded on orig_len > MaxBlockSize")
	}
}
