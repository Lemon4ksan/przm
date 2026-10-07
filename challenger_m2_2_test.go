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

// TestChallenger_M2_2_PureC_1M_Window tests 1M window Go-compressed frames decoded by Pure C test_przm_dec.exe.
func TestChallenger_M2_2_PureC_1M_Window(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}
	if _, err := os.Stat(cBinPath); err != nil {
		t.Fatalf("test_przm_dec.exe not found at %s: %v", cBinPath, err)
	}

	tmpDir := t.TempDir()

	testDistances := []struct {
		name     string
		distance int
		matchLen int
	}{
		{"Distance_70K_Beyond64K", 70 * 1024, 64},
		{"Distance_128K", 128 * 1024, 128},
		{"Distance_256K", 256 * 1024, 256},
		{"Distance_512K", 512 * 1024, 512},
		{"Distance_900K_Near1M", 900 * 1024, 1024},
	}

	for _, td := range testDistances {
		t.Run(td.name, func(t *testing.T) {
			totalSize := td.distance + td.matchLen + 1024
			data := make([]byte, totalSize)

			// Fill with pseudo-random content
			for i := range data {
				data[i] = byte((i*37 + 13) & 0xFF)
			}

			// Plant a match: copy td.matchLen bytes from pos 100 to pos 100 + td.distance
			matchPayload := []byte("PRZM_L3_1M_SCALING_TEST_PAYLOAD_VERIFICATION_BYTES_PATTERN_ABCDEF1234567890")
			for i := 0; i < td.matchLen; i++ {
				data[100+i] = matchPayload[i%len(matchPayload)]
				data[100+td.distance+i] = matchPayload[i%len(matchPayload)]
			}

			for level := 1; level <= 4; level++ {
				t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
					compressed, err := przm.CompressWithWindow(data, level, przm.WindowLog1M)
					if err != nil {
						t.Fatalf("Go CompressWithWindow failed at level %d: %v", level, err)
					}

					// If standard PRZM LZ frame, verify WindowLog
					if len(compressed) >= 4 && binary.LittleEndian.Uint32(compressed[:4]) == przm.MagicPRZM {
						hdr, err := przm.ParseFrameHeader(compressed)
						if err != nil {
							t.Fatalf("ParseFrameHeader failed: %v", err)
						}
						if hdr.WindowLog != przm.WindowLog1M {
							t.Fatalf("Expected WindowLog1M (20), got %d", hdr.WindowLog)
						}
					}

					inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", td.name, level))
					outBin := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.out", td.name, level))

					if err := os.WriteFile(inPrzm, compressed, 0644); err != nil {
						t.Fatalf("failed to write compressed frame: %v", err)
					}

					cmd := exec.Command(cBinPath, inPrzm, outBin)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
					}

					decompressed, err := os.ReadFile(outBin)
					if err != nil {
						t.Fatalf("failed to read decompressed file: %v", err)
					}

					if !bytes.Equal(decompressed, data) {
						t.Fatalf("Decompressed data mismatch: got %d bytes, want %d bytes", len(decompressed), len(data))
					}
				})
			}
		})
	}
}

// TestChallenger_M2_2_PureC_8M_Window tests 8M window Go-compressed frames decoded by Pure C test_przm_dec.exe.
func TestChallenger_M2_2_PureC_8M_Window(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	testDistances := []struct {
		name     string
		distance int
		matchLen int
	}{
		{"Distance_1M5_Beyond1M", 1500 * 1024, 256},
		{"Distance_2M5_4ByteVarint", 2500 * 1024, 512},
		{"Distance_4M5", 4500 * 1024, 1024},
		{"Distance_7M5_Near8M", 7500 * 1024, 2048},
	}

	for _, td := range testDistances {
		t.Run(td.name, func(t *testing.T) {
			totalSize := td.distance + td.matchLen + 1024
			data := make([]byte, totalSize)

			for i := range data {
				data[i] = byte((i*59 + 71) & 0xFF)
			}

			// Plant a repeat match across large distance
			pattern := []byte("PRZM_8M_LARGE_WINDOW_CORPUS_COVERAGE_4BYTE_VARINT_VERIFY_XYZ987654321")
			for i := 0; i < td.matchLen; i++ {
				data[512+i] = pattern[i%len(pattern)]
				data[512+td.distance+i] = pattern[i%len(pattern)]
			}

			// Test level 1 (fast) and level 3 (default/high)
			for _, level := range []int{1, 3} {
				t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
					compressed, err := przm.CompressWithWindow(data, level, przm.WindowLog8M)
					if err != nil {
						t.Fatalf("Go CompressWithWindow (8M) failed at level %d: %v", level, err)
					}

					hdr, err := przm.ParseFrameHeader(compressed)
					if err != nil {
						t.Fatalf("ParseFrameHeader failed: %v", err)
					}
					if hdr.WindowLog != przm.WindowLog8M {
						t.Fatalf("Expected WindowLog8M (23), got %d", hdr.WindowLog)
					}

					inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", td.name, level))
					outBin := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.out", td.name, level))

					if err := os.WriteFile(inPrzm, compressed, 0644); err != nil {
						t.Fatalf("failed to write compressed frame: %v", err)
					}

					cmd := exec.Command(cBinPath, inPrzm, outBin)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("Pure C decompressor failed on 8M frame (exit status %v):\nOutput:\n%s", err, string(output))
					}

					decompressed, err := os.ReadFile(outBin)
					if err != nil {
						t.Fatalf("failed to read decompressed file: %v", err)
					}

					if !bytes.Equal(decompressed, data) {
						t.Fatalf("Decompressed data mismatch: got %d bytes, want %d bytes", len(decompressed), len(data))
					}
				})
			}
		})
	}
}

// TestChallenger_M2_2_PureC_AdversarialCorruption tests that corrupt streams cause clean failure in C without crash.
func TestChallenger_M2_2_PureC_AdversarialCorruption(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	// Base valid frame
	validData := bytes.Repeat([]byte("PRZM_CORRUPTION_TEST_STRING_"), 100)
	validCompressed, err := przm.Compress(validData, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	corruptCases := []struct {
		name       string
		modifyData func([]byte) []byte
	}{
		{
			name: "Truncated_To_4_Bytes",
			modifyData: func(b []byte) []byte {
				return b[:4]
			},
		},
		{
			name: "Truncated_To_7_Bytes",
			modifyData: func(b []byte) []byte {
				return b[:7]
			},
		},
		{
			name: "Corrupt_Magic",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				cp[0] = 0xFF
				cp[1] = 0xFF
				return cp
			},
		},
		{
			name: "Corrupt_USize_Huge",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				// Uncompressed size is at byte 5..7 (u24)
				cp[5] = 0xFF
				cp[6] = 0xFF
				cp[7] = 0xFF // 16,777,215 > PRZM_MAX_BLOCK_SIZE
				return cp
			},
		},
		{
			name: "Corrupt_Section_Directory_OOB",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				// Section directory is at offset 8 (or 14 if ext offset)
				if len(cp) > 12 {
					cp[8] = 0xFF
					cp[9] = 0xFF
				}
				return cp
			},
		},
		{
			name: "Random_Bit_Flipping_Payload",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				for i := len(cp) / 2; i < len(cp); i += 4 {
					cp[i] ^= 0xAA
				}
				return cp
			},
		},
	}

	for _, cc := range corruptCases {
		t.Run(cc.name, func(t *testing.T) {
			corruptFrame := cc.modifyData(validCompressed)
			inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s.przm", cc.name))
			outBin := filepath.Join(tmpDir, fmt.Sprintf("%s.out", cc.name))

			if err := os.WriteFile(inPrzm, corruptFrame, 0644); err != nil {
				t.Fatalf("failed to write corrupt frame: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()

			// Must fail (exit code != 0) without panicking / crashing
			if err == nil {
				t.Fatalf("Expected C decompressor to reject corrupt frame %s, but it returned success! Output:\n%s",
					cc.name, string(output))
			}
		})
	}
}
