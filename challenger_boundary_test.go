// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
)

// TestChallenger_PWS_FrameBoundary checks all truncated frame lengths [0..25] bytes for PWS.
func TestChallenger_PWS_FrameBoundary(t *testing.T) {
	// Construct a minimal valid PWS frame
	var validFrame []byte
	validFrame = append(validFrame, 'P', 'W', 0x01, 0x00) // Magic + Stride
	var sz [8]byte
	binary.LittleEndian.PutUint32(sz[0:4], 4) // origSize = 4
	binary.LittleEndian.PutUint32(sz[4:8], 1) // tokenCount = 1
	validFrame = append(validFrame, sz[:]...)
	validFrame = append(validFrame, 1, 1, 0, 0, 0, 0, 0, 0) // counts: L1=1, L2=1
	validFrame = append(validFrame, 'T', 'e')                // L1[0]
	validFrame = append(validFrame, 0, 0)                    // L2[0]
	validFrame = append(validFrame, 0xD0)                    // Axiom: token 0xD0 (L2[0])

	for length := 0; length <= len(validFrame); length++ {
		t.Run(fmt.Sprintf("Truncated_Len_%d", length), func(t *testing.T) {
			sub := validFrame[:length]
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC on truncated PWS frame length %d: %v", length, r)
				}
			}()

			dec, err := przm.PWSDecode(sub)
			if length < 20 {
				if err != przm.ErrPWSCorrupted {
					t.Fatalf("Expected ErrPWSCorrupted for length %d < 20, got err=%v", length, err)
				}
				if dec != nil {
					t.Fatalf("Expected nil decoded slice for length %d < 20, got len=%d", length, len(dec))
				}
			} else if length < len(validFrame) {
				// Between 20 and full length, should return error without panic
				if err == nil {
					t.Fatalf("Expected error for partially truncated frame length %d, got nil", length)
				}
			} else {
				// Full length should succeed
				if err != nil {
					t.Fatalf("Expected success for full frame, got %v", err)
				}
				if string(dec) != "TeTe" {
					t.Fatalf("Expected 'TeTe', got %q", string(dec))
				}
			}
		})
	}
}

// TestChallenger_ShortAndEmptyInputs tests all lengths [0..64] across all compression levels.
func TestChallenger_ShortAndEmptyInputs(t *testing.T) {
	for sz := 0; sz <= 64; sz++ {
		data := make([]byte, sz)
		for i := range data {
			data[i] = byte(i + 1)
		}

		for lvl := 1; lvl <= 4; lvl++ {
			t.Run(fmt.Sprintf("Size_%d_Level_%d", sz, lvl), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("PANIC on sz=%d lvl=%d: %v", sz, lvl, r)
					}
				}()

				comp, err := przm.Compress(data, lvl)
				if err != nil {
					t.Fatalf("Compress failed on sz=%d lvl=%d: %v", sz, lvl, err)
				}

				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("Decompress failed on sz=%d lvl=%d: %v", sz, lvl, err)
				}

				if !bytes.Equal(dec, data) {
					t.Fatalf("Mismatch on sz=%d lvl=%d: got %d bytes, want %d bytes", sz, lvl, len(dec), len(data))
				}
			})
		}
	}
}

// TestChallenger_PWSEncode_ShortInputs tests PWSEncode / PWSDecode specifically on short inputs [0..64].
func TestChallenger_PWSEncode_ShortInputs(t *testing.T) {
	for sz := 0; sz <= 64; sz++ {
		data := make([]byte, sz)
		for i := range data {
			data[i] = byte((i * 17) % 256)
		}

		t.Run(fmt.Sprintf("PWS_Size_%d", sz), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("PANIC in PWS on sz=%d: %v", sz, r)
				}
			}()

			enc, err := przm.PWSEncode(data)
			if err != nil {
				t.Fatalf("PWSEncode failed on sz=%d: %v", sz, err)
			}

			if sz == 0 {
				if len(enc) != 0 {
					t.Fatalf("Expected empty output for sz=0, got len %d", len(enc))
				}
				return
			}

			dec, err := przm.PWSDecode(enc)
			if err != nil {
				t.Fatalf("PWSDecode failed on sz=%d: %v", sz, err)
			}

			if !bytes.Equal(dec, data) {
				t.Fatalf("PWS mismatch on sz=%d: got len %d, want len %d", sz, len(dec), len(data))
			}
		})
	}
}

// TestChallenger_CDecompressor_TruncatedAndMalformedFrames checks that the C decompressor
// rejects truncated frames (< 20 bytes for PWS, < 8 bytes for PRZM) cleanly without crashing.
func TestChallenger_CDecompressor_TruncatedAndMalformedFrames(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
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

	tmpDir := t.TempDir()

	// Truncated frames
	truncatedFrames := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"1_byte", []byte{'P'}},
		{"3_bytes_pws", []byte{'P', 'W', 0x01}},
		{"4_bytes_pws", []byte{'P', 'W', 0x01, 0x00}},
		{"7_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00}},
		{"8_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00}},
		{"16_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00}},
		{"19_bytes_pws", []byte{'P', 'W', 0x01, 0x00, 0x04, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"3_bytes_przm", []byte{'P', 'R', 'Z'}},
		{"4_bytes_przm", []byte{'P', 'R', 'Z', 'M'}},
		{"7_bytes_przm", []byte{'P', 'R', 'Z', 'M', 0x00, 0x04, 0x00}},
	}

	for _, tc := range truncatedFrames {
		t.Run("C_Truncated_"+tc.name, func(t *testing.T) {
			inPrzm := filepath.Join(tmpDir, "trunc_"+tc.name+".przm")
			outBin := filepath.Join(tmpDir, "trunc_"+tc.name+".out")

			if err := os.WriteFile(inPrzm, tc.data, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			// Must fail (non-zero exit code)
			if err == nil {
				t.Fatalf("Expected C decompressor to reject truncated frame %q, but succeeded! Output:\n%s", tc.name, string(output))
			}
			// Exit code must be 2 (Invalid PRZM frame header / error) or 3 (Decompression failed), NOT a crash (e.g. status code -1073741819 / 0xC0000005)
			exitCode := cmd.ProcessState.ExitCode()
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf("C decompressor crashed or exited unexpectedly with code %d on %q. Output:\n%s", exitCode, tc.name, string(output))
			}
		})
	}
}

