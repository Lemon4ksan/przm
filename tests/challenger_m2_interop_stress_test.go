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

func TestChallenger_PureCDecompressor_GoMT_Interop(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "../test_przm_dec.exe", "tests/test_przm_dec.exe"}
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

	testCases := []struct {
		name      string
		size      int
		threads   int
		chunkSize int
		level     int
		generator func(sz int) []byte
	}{
		{
			name:      "ASCII_512K_T2_C64K",
			size:      512 * 1024,
			threads:   2,
			chunkSize: 64 * 1024,
			level:     1,
			generator: func(sz int) []byte {
				buf := make([]byte, sz)
				for i := range buf {
					buf[i] = byte('A' + ((i / 13) % 26))
				}
				return buf
			},
		},
		{
			name:      "JSON_1M_T4_C128K",
			size:      1024 * 1024,
			threads:   4,
			chunkSize: 128 * 1024,
			level:     2,
			generator: func(sz int) []byte {
				var b bytes.Buffer
				for b.Len() < sz {
					fmt.Fprintf(
						&b,
						`{"sensor_id":"s_%d","temp":%d,"alert":%t},`,
						b.Len()%100,
						20+(b.Len()%15),
						b.Len()%50 == 0,
					)
				}
				return b.Bytes()[:sz]
			},
		},
		{
			name:      "Binary_2M_T8_C256K",
			size:      2 * 1024 * 1024,
			threads:   8,
			chunkSize: 256 * 1024,
			level:     3,
			generator: func(sz int) []byte {
				buf := make([]byte, sz)
				for i := range buf {
					buf[i] = byte((i*37 + (i>>5)*19) & 0xFF)
				}
				return buf
			},
		},
		{
			name:      "Uneven_3M5_T7_C500K",
			size:      3500 * 1024,
			threads:   7,
			chunkSize: 500 * 1024,
			level:     2,
			generator: func(sz int) []byte {
				buf := make([]byte, sz)
				for i := range buf {
					buf[i] = byte((i ^ (i >> 3) ^ (i >> 7)) & 0xFF)
				}
				return buf
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.generator(tc.size)

			opts := przm.CompressOptions{
				Threads:   tc.threads,
				ChunkSize: tc.chunkSize,
				Level:     tc.level,
			}

			compressed, err := przm.CompressWithOptions(raw, opts)
			if err != nil {
				t.Fatalf("CompressWithOptions failed: %v", err)
			}

			magic := binary.LittleEndian.Uint32(compressed[:4])
			if magic != przm.MagicPRZS {
				t.Fatalf("Expected MagicPRZS, got 0x%08X", magic)
			}

			inPrzs := filepath.Join(tmpDir, fmt.Sprintf("%s.przs", tc.name))
			outBin := filepath.Join(tmpDir, fmt.Sprintf("%s.out", tc.name))

			if err := os.WriteFile(inPrzs, compressed, 0o644); err != nil {
				t.Fatalf("Failed to write compressed file: %v", err)
			}

			// Invoke Pure C CLI decompressor
			cmd := exec.Command(cBinPath, inPrzs, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor failed with error %v:\nOutput:\n%s", err, string(output))
			}

			decompressed, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("Failed to read decompressed file: %v", err)
			}

			if !bytes.Equal(decompressed, raw) {
				t.Fatalf("Decompressed data mismatch: got %d bytes, want %d bytes", len(decompressed), len(raw))
			}

			// Cross check Go decompressor
			goDec, err := przm.Decompress(compressed)
			if err != nil {
				t.Fatalf("Go Decompress failed: %v", err)
			}
			if !bytes.Equal(goDec, raw) {
				t.Fatalf("Go Decompress mismatch")
			}
		})
	}

	// Adversarial rejection test on Go MT generated stream
	t.Run("Adversarial_Corruption_Rejection", func(t *testing.T) {
		raw := make([]byte, 256*1024)
		for i := range raw {
			raw[i] = byte(i * 17)
		}
		comp, err := przm.CompressWithOptions(raw, przm.CompressOptions{Threads: 4, ChunkSize: 64 * 1024})
		if err != nil {
			t.Fatalf("CompressWithOptions failed: %v", err)
		}

		// 1. Truncated file (cut in half)
		truncPrzs := filepath.Join(tmpDir, "trunc.przs")
		truncOut := filepath.Join(tmpDir, "trunc.out")
		if err := os.WriteFile(truncPrzs, comp[:len(comp)/2], 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(cBinPath, truncPrzs, truncOut)
		if err := cmd.Run(); err == nil {
			t.Fatalf("Pure C decompressor should have rejected truncated file")
		}

		// 2. Corrupted inner chunk magic
		corruptComp := make([]byte, len(comp))
		copy(corruptComp, comp)
		// Offset 17 is chunk 0 len prefix (4 bytes). Offset 21 is chunk 0 magic ('P','R','Z','M')
		corruptComp[21] = 'X'
		corruptPrzs := filepath.Join(tmpDir, "corrupt.przs")
		corruptOut := filepath.Join(tmpDir, "corrupt.out")
		if err := os.WriteFile(corruptPrzs, corruptComp, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd = exec.Command(cBinPath, corruptPrzs, corruptOut)
		if err := cmd.Run(); err == nil {
			t.Fatalf("Pure C decompressor should have rejected corrupted inner chunk")
		}
	})
}
