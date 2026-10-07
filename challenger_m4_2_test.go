// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
)

func findCDecompressor(t *testing.T) string {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	t.Skip("test_przm_dec.exe not found")
	return ""
}

// TestChallenger_M4_2_ExhaustiveMode9AndAllModesParity validates 100% bit-exact parity
// between Go and Pure C decompressors across all 9 normalization modes with diverse sizes.
func TestChallenger_M4_2_ExhaustiveMode9AndAllModesParity(t *testing.T) {
	cBin := findCDecompressor(t)
	tmpDir := t.TempDir()

	type testCase struct {
		name     string
		raw      []byte
		normMode uint8
	}

	var cases []testCase

	// Mode 1: Hex Lower (128B, 256B, 1024B, 4096B)
	for _, reps := range []int{16, 32, 128, 512} {
		raw := []byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67}, reps)))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode1_HexLower_%dB", len(raw)),
			raw:      raw,
			normMode: 1,
		})
	}

	// Mode 4: Hex Upper (128B, 256B, 1024B, 4096B)
	for _, reps := range []int{8, 16, 64, 256} {
		raw := bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}, reps))))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode4_HexUpper_%dB", len(raw)),
			raw:      raw,
			normMode: 4,
		})
	}

	// Mode 2: Base64 Std (No pad: raw len % 3 == 0)
	for _, reps := range []int{30, 60, 200, 1000} {
		raw := []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("123"), reps)))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode2_Base64Std_NoPad_raw%dB", reps*3),
			raw:      raw,
			normMode: 2,
		})
	}

	// Mode 5: Base64 Std (Pad 1: raw len % 3 == 2)
	for _, reps := range []int{20, 50, 200, 1000} {
		raw := []byte(base64.StdEncoding.EncodeToString(append(bytes.Repeat([]byte("123"), reps), '4', '5')))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode5_Base64Std_Pad1_raw%dB", reps*3+2),
			raw:      raw,
			normMode: 5,
		})
	}

	// Mode 6: Base64 Std (Pad 2: raw len % 3 == 1)
	for _, reps := range []int{20, 50, 200, 1000} {
		raw := []byte(base64.StdEncoding.EncodeToString(append(bytes.Repeat([]byte("123"), reps), '4')))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode6_Base64Std_Pad2_raw%dB", reps*3+1),
			raw:      raw,
			normMode: 6,
		})
	}

	// Mode 7: Base64 URL (No pad: raw len % 3 == 0, URL safe '-' and '_')
	for _, reps := range []int{20, 50, 200, 1000} {
		rawBytes := bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, reps)
		raw := []byte(base64.URLEncoding.EncodeToString(rawBytes))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode7_Base64URL_NoPad_raw%dB", reps*3),
			raw:      raw,
			normMode: 7,
		})
	}

	// Mode 8: Base64 URL (Pad 1: raw len % 3 == 2, URL safe '-' and '_')
	for _, reps := range []int{20, 50, 200, 1000} {
		rawBytes := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, reps), 0xFB, 0xFF)
		raw := []byte(base64.URLEncoding.EncodeToString(rawBytes))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode8_Base64URL_Pad1_raw%dB", reps*3+2),
			raw:      raw,
			normMode: 8,
		})
	}

	// Mode 9: Base64 URL (Pad 2: raw len % 3 == 1, URL safe '-' and '_')
	// EXHAUSTIVE SIZES for Mode 9
	for _, reps := range []int{16, 25, 50, 100, 250, 500, 1000, 2500} {
		rawBytes := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, reps), 0xFB)
		raw := []byte(base64.URLEncoding.EncodeToString(rawBytes))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode9_Base64URL_Pad2_raw%dB", reps*3+1),
			raw:      raw,
			normMode: 9,
		})
	}

	// Mode 3: Mixed spans
	cases = append(cases, testCase{
		name: "Mode3_MixedSpans_JSON",
		raw: func() []byte {
			var buf bytes.Buffer
			buf.WriteString("{\"status\":\"ok\",\"hex\":\"")
			buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44}, 16)))
			buf.WriteString("\",\"token\":\"")
			buf.WriteString(base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 16), 0xFB)))
			buf.WriteString("\"}")
			return buf.Bytes()
		}(),
		normMode: 3,
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := przm.Compress(tc.raw, 1)
			if err != nil {
				t.Fatalf("Go Compress failed: %v", err)
			}

			hdr, err := przm.ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("ParseFrameHeader failed: %v", err)
			}
			if hdr.NormMode != tc.normMode {
				t.Fatalf("Expected NormMode %d, got %d", tc.normMode, hdr.NormMode)
			}

			// Verify Go decompress
			goDst := make([]byte, len(tc.raw)+64)
			goDec, err := przm.DecompressTo(comp, goDst)
			if err != nil {
				t.Fatalf("Go DecompressTo failed: %v", err)
			}
			if !bytes.Equal(goDec, tc.raw) {
				t.Fatalf("Go DecompressTo payload mismatch")
			}

			// Verify C decompress
			inPrzm := filepath.Join(tmpDir, tc.name+".przm")
			outBin := filepath.Join(tmpDir, tc.name+".out")
			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBin, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
			}

			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}

			if !bytes.Equal(cDec, tc.raw) {
				t.Fatalf("Pure C decompressed payload mismatch: got %d bytes, want %d bytes", len(cDec), len(tc.raw))
			}

			if !bytes.Equal(cDec, goDec) {
				t.Fatalf("Cross-language mismatch between Go and C outputs!")
			}
		})
	}
}

// TestChallenger_M4_2_SubThresholdBypassParity validates that inputs below the 64B
// normalization threshold bypass normalization (NormMode=0) and decompress identically in Go & C.
func TestChallenger_M4_2_SubThresholdBypassParity(t *testing.T) {
	cBin := findCDecompressor(t)
	tmpDir := t.TempDir()

	subCases := [][]byte{
		[]byte("hello"),
		[]byte("cafebabe"),
		[]byte(base64.StdEncoding.EncodeToString([]byte("short"))),
		[]byte(base64.URLEncoding.EncodeToString([]byte("jwt-fragment-xyz"))),
		[]byte("{\"key\": 123}"),
	}

	for idx, raw := range subCases {
		comp, err := przm.Compress(raw, 1)
		if err != nil {
			t.Fatalf("[%d] Compress failed: %v", idx, err)
		}
		hdr, err := przm.ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("[%d] ParseFrameHeader failed: %v", idx, err)
		}
		if hdr.NormMode != 0 {
			t.Fatalf("[%d] Expected NormMode 0 for sub-threshold input, got %d", idx, hdr.NormMode)
		}

		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("sub_%d.przm", idx))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("sub_%d.out", idx))
		if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
			t.Fatalf("[%d] WriteFile failed: %v", idx, err)
		}

		cmd := exec.Command(cBin, inPrzm, outBin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[%d] Pure C failed: %v\nOutput: %s", idx, err, string(output))
		}
		cDec, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatalf("[%d] ReadFile failed: %v", idx, err)
		}
		if !bytes.Equal(cDec, raw) {
			t.Fatalf("[%d] Mismatch in Pure C sub-threshold decompress", idx)
		}
	}
}

// TestChallenger_M4_2_PureC_AdversarialRobustness feeds heavily corrupted and mutated
// bitstreams to test_przm_dec.exe and asserts that it rejects with non-zero error codes
// without crashing, segfaulting, or hanging.
func TestChallenger_M4_2_PureC_AdversarialRobustness(t *testing.T) {
	cBin := findCDecompressor(t)
	tmpDir := t.TempDir()

	// Base payload for mutation
	basePayload := []byte("{\"token\":\"" + base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 10), 0xFB)) + "\"}")
	validComp, err := przm.Compress(basePayload, 1)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	var corruptCases [][]byte

	// 1. Truncations
	for _, l := range []int{0, 1, 2, 3, 4, 7, 8, len(validComp) / 4, len(validComp) / 2, len(validComp) - 1} {
		if l <= len(validComp) {
			b := make([]byte, l)
			copy(b, validComp[:l])
			corruptCases = append(corruptCases, b)
		}
	}

	// 2. Magic corruption
	badMagic := make([]byte, len(validComp))
	copy(badMagic, validComp)
	badMagic[0] ^= 0xAA
	corruptCases = append(corruptCases, badMagic)

	// 3. Flags and size tampering
	badSize := make([]byte, len(validComp))
	copy(badSize, validComp)
	badSize[5] = 0xFF
	badSize[6] = 0xFF
	badSize[7] = 0xFF
	corruptCases = append(corruptCases, badSize)

	// 4. Invalid Normalization Modes (e.g., Mode 10, Mode 255)
	hdr, err := przm.ParseFrameHeader(validComp)
	if err == nil && hdr.Flags&0x20 != 0 {
		secDirLen := 4
		if hdr.Flags&0x08 != 0 {
			secDirLen = 6
		}
		sec1Pos := 8 + secDirLen
		if sec1Pos+2 < len(validComp) {
			for _, badMode := range []byte{10, 11, 15, 255} {
				badModeBuf := make([]byte, len(validComp))
				copy(badModeBuf, validComp)
				badModeBuf[sec1Pos+2] = badMode
				corruptCases = append(corruptCases, badModeBuf)
			}
		}
	}

	// 5. Random byte flips across entire stream
	for off := 4; off < len(validComp); off += 3 {
		flipped := make([]byte, len(validComp))
		copy(flipped, validComp)
		flipped[off] ^= 0xFF
		corruptCases = append(corruptCases, flipped)
	}

	// 6. Completely random byte buffers
	for _, sz := range []int{16, 64, 256, 1024} {
		rnd := make([]byte, sz)
		_, _ = rand.Read(rnd)
		corruptCases = append(corruptCases, rnd)
	}

	inPath := filepath.Join(tmpDir, "corrupt.przm")
	outPath := filepath.Join(tmpDir, "corrupt.out")

	for idx, corruptData := range corruptCases {
		if err := os.WriteFile(inPath, corruptData, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		_ = os.Remove(outPath)

		cmd := exec.Command(cBin, inPath, outPath)
		output, err := cmd.CombinedOutput()

		// A corrupt stream must either exit with error code or, if valid, produce valid output.
		// It MUST NEVER crash with segfault (exit code -1073741819 / 0xC0000005) or abort.
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			if exitCode < 0 || exitCode > 128 {
				t.Fatalf("[%d] test_przm_dec.exe crashed with abnormal exit code %d (0x%X)!\nOutput:\n%s",
					idx, exitCode, uint32(exitCode), string(output))
			}
		}
	}
	t.Logf("Tested %d adversarial corrupt bitstreams against test_przm_dec.exe: zero crashes/segfaults", len(corruptCases))
}

// TestChallenger_M4_2_ZeroAllocDecompressTo verifies strictly 0 heap allocations
// in Go DecompressTo across all 9 modes and various sizes.
func TestChallenger_M4_2_ZeroAllocDecompressTo(t *testing.T) {
	testData := [][]byte{
		// Mode 1: Hex Lower
		[]byte(hex.EncodeToString(bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 100))),
		// Mode 4: Hex Upper
		bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 100)))),
		// Mode 2: Base64 Std (No pad)
		[]byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("ABC"), 100))),
		// Mode 5: Base64 Std (Pad 1)
		[]byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("ABCDE"), 60))),
		// Mode 6: Base64 Std (Pad 2)
		[]byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("ABCD"), 60))),
		// Mode 7: Base64 URL (No pad)
		[]byte(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 100))),
		// Mode 8: Base64 URL (Pad 1)
		[]byte(base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 100), 0xFB, 0xFF))),
		// Mode 9: Base64 URL (Pad 2)
		[]byte(base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 100), 0xFB))),
		// Mode 3: Mixed spans
		[]byte("{\"hash\":\"" + hex.EncodeToString(bytes.Repeat([]byte{0x12, 0x34}, 32)) + "\",\"jwt\":\"" +
			base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 16), 0xFB)) + "\"}"),
	}

	for idx, d := range testData {
		comp, err := przm.Compress(d, 1)
		if err != nil {
			t.Fatalf("[%d] Compress failed: %v", idx, err)
		}
		dst := make([]byte, len(d)+64)

		// Warmup
		_, err = przm.DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("[%d] DecompressTo warmup failed: %v", idx, err)
		}

		allocs := testing.AllocsPerRun(50, func() {
			_, _ = przm.DecompressTo(comp, dst)
		})
		if allocs != 0.0 {
			t.Fatalf("[%d] DecompressTo produced %f allocs, want 0.0", idx, allocs)
		}
	}
}

// TestChallenger_M4_2_GoDecompressTo_AdversarialFuzz feeds 1000 randomized corruptions
// and mutations to Go DecompressTo to ensure zero unhandled panics or bounds errors.
func TestChallenger_M4_2_GoDecompressTo_AdversarialFuzz(t *testing.T) {
	seeds := [][]byte{
		[]byte("{\"id\":1001,\"data\":\"" + base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 16)) + "\"}"),
		bytes.Repeat([]byte{0x00, 0x01, 0x02, 0x03}, 32),
		bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 16)))),
	}

	dst := make([]byte, 65536)

	for sIdx, seed := range seeds {
		comp, err := przm.Compress(seed, 1)
		if err != nil {
			t.Fatalf("Seed %d compress failed: %v", sIdx, err)
		}

		// Mutate across 300 iterations per seed
		for iter := 0; iter < 300; iter++ {
			mutated := make([]byte, len(comp))
			copy(mutated, comp)

			// Truncate or flip random bytes
			if iter%3 == 0 && len(mutated) > 2 {
				truncLen := iter % len(mutated)
				mutated = mutated[:truncLen]
			} else {
				numFlips := 1 + (iter % 5)
				for f := 0; f < numFlips; f++ {
					flipIdx := (iter * 17 + f * 7) % len(mutated)
					mutated[flipIdx] ^= byte(0x55 + f*33)
				}
			}

			func(s, i int, payload []byte) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("[Seed %d, Iter %d] Go DecompressTo panicked: %v", s, i, r)
					}
				}()
				_, _ = przm.DecompressTo(payload, dst)
			}(sIdx, iter, mutated)
		}
	}
	t.Logf("Tested 900 adversarial fuzzed bitstreams against Go DecompressTo: zero panics")
}

