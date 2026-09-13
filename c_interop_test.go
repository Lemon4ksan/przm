// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
)

func TestPureCDecompressorInterop(t *testing.T) {
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
		t.Skip("C test binary test_przm_dec.exe not found; build it with gcc first")
	}

	tmpDir := t.TempDir()

	testCases := []struct {
		name     string
		data     []byte
		levels   []int
	}{
		{
			name:   "SmallASCII",
			data:   []byte("Hello World from PRZM v0.5 Go Compressor and Pure C Decompressor!"),
			levels: []int{1, 2, 3, 4},
		},
		{
			name: "RepetitiveTelemetry",
			data: func() []byte {
				var buf bytes.Buffer
				for i := 0; i < 500; i++ {
					fmt.Fprintf(&buf, "{\"timestamp\":%d,\"user_id\":%d,\"status\":200,\"response_time_ms\":%d}\n",
						1700000000+i, 1000+(i%10), i%50)
				}
				return buf.Bytes()
			}(),
			levels: []int{1, 2, 3, 4},
		},
		{
			name: "GoSourceCode",
			data: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("package main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\n")
				for i := 0; i < 100; i++ {
					buf.WriteString("func (s *Server) HandleRequest() (int, error) {\n")
					buf.WriteString("\tif err != nil {\n\t\treturn nil, err\n\t}\n")
					buf.WriteString("\treturn 0, nil\n}\n")
				}
				return buf.Bytes()
			}(),
			levels: []int{1, 2, 3, 4},
		},
		{
			name: "x86MachineCode",
			data: func() []byte {
				code := make([]byte, 8192)
				for i := 0; i < len(code); i++ {
					code[i] = byte(i & 0xFF)
				}
				for i := 16; i < len(code)-10; i += 64 {
					code[i] = 0xE8 // CALL
					code[i+1] = byte(i)
					code[i+2] = byte(i >> 8)
					code[i+3] = 0x00
					code[i+4] = 0x00
				}
				return code
			}(),
			levels: []int{1, 2, 3, 4},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for _, level := range tc.levels {
				t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
					compressed, err := przm.Compress(tc.data, level)
					if err != nil {
						t.Fatalf("Go compression failed at level %d: %v", level, err)
					}

					inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", tc.name, level))
					outBin := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.out", tc.name, level))

					if err := os.WriteFile(inPrzm, compressed, 0644); err != nil {
						t.Fatalf("failed to write compressed file: %v", err)
					}

					cmd := exec.Command(cBinPath, inPrzm, outBin)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
					}

					decompressed, err := os.ReadFile(outBin)
					if err != nil {
						t.Fatalf("failed to read C decompressed file: %v", err)
					}

					if !bytes.Equal(decompressed, tc.data) {
						t.Fatalf("Decompressed data mismatch (got %d bytes, want %d bytes)",
							len(decompressed), len(tc.data))
					}
				})
			}
		})
	}
}
