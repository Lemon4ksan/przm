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
	"strings"
	"testing"
	"time"

	"github.com/lemon4ksan/przm"
)

func runCDecompressor(cBinPath string, args ...string) ([]byte, error) {
	var output []byte
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		cmd := exec.Command(cBinPath, args...)
		output, err = cmd.CombinedOutput()
		if err == nil {
			return output, nil
		}
		if !strings.Contains(err.Error(), "Access is denied") && !strings.Contains(err.Error(), "0xc0000005") {
			return output, err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return output, err
}

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
		name   string
		data   []byte
		levels []int
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
				for i := range 500 {
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
				for range 100 {
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
				for i := range code {
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
		{
			name: "IsoQuantMultiChannelRGBA",
			data: func() []byte {
				buf := make([]byte, 1024*4)
				for i := range 1024 {
					baseVal := uint8(i % 128)
					buf[i*4+0] = baseVal
					buf[i*4+1] = baseVal ^ 0x05
					buf[i*4+2] = baseVal ^ 0x0A
					buf[i*4+3] = 0xFF
				}
				return buf
			}(),
			levels: []int{1, 2, 3, 4},
		},
		{
			name: "IsoQuantShaderSPIRV",
			data: func() []byte {
				buf := make([]byte, 2048)
				binary.LittleEndian.PutUint32(buf[0:4], 0x07230203) // SPIR-V Magic
				binary.LittleEndian.PutUint32(buf[4:8], 0x00010500) // Version 1.5
				for i := 8; i < len(buf); i += 4 {
					buf[i] = uint8(i % 64)
					buf[i+1] = uint8((i + 1) % 64)
					buf[i+2] = uint8((i + 2) % 64)
					buf[i+3] = 0x3F // IEEE float exp ~ 1.0
				}
				return buf
			}(),
			levels: []int{1, 2, 3, 4},
		},
		{
			name: "HeisenbergMultiChannelRGB",
			data: func() []byte {
				buf := make([]byte, 3072)
				for i := range 1024 {
					x := uint8((i % 64) + 1)
					y := uint8(((i / 64) % 64) + 1)
					buf[i*3+0] = x
					buf[i*3+1] = y
					buf[i*3+2] = przm.GF256Mul(x, y)
				}
				return buf
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

					if err := os.WriteFile(inPrzm, compressed, 0o644); err != nil {
						t.Fatalf("failed to write compressed file: %v", err)
					}

					output, err := runCDecompressor(cBinPath, inPrzm, outBin)
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

	// Test direct Go-compressed IsoQuant frame interop with Pure C decompressor
	t.Run("DirectIsoQuantFrame", func(t *testing.T) {
		raw := make([]byte, 4096)
		for i := range 1024 {
			b := uint8(i % 128)
			raw[i*4+0] = b
			raw[i*4+1] = b ^ 0x03
			raw[i*4+2] = b ^ 0x07
			raw[i*4+3] = 0xFF
		}

		compressed, err := przm.CompressIsoQuant(raw)
		if err != nil {
			t.Fatalf("CompressIsoQuant failed: %v", err)
		}

		inPrzm := filepath.Join(tmpDir, "isoquant_direct.przm")
		outBin := filepath.Join(tmpDir, "isoquant_direct.out")

		if err := os.WriteFile(inPrzm, compressed, 0o644); err != nil {
			t.Fatalf("failed to write compressed file: %v", err)
		}

		output, err := runCDecompressor(cBinPath, inPrzm, outBin)
		if err != nil {
			t.Fatalf("C decompressor failed on IsoQuant frame (exit status %v):\nOutput:\n%s", err, string(output))
		}

		decompressed, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatalf("failed to read C decompressed file: %v", err)
		}

		if !bytes.Equal(decompressed, raw) {
			t.Fatalf("Direct IsoQuant interop mismatch: got %d bytes, want %d bytes", len(decompressed), len(raw))
		}
	})

	// Test direct Go-compressed Heisenberg frame interop with Pure C decompressor
	t.Run("DirectHeisenbergFrame_Stride3", func(t *testing.T) {
		raw := make([]byte, 3072)
		for i := range 1024 {
			x := uint8((i % 64) + 1)
			y := uint8(((i / 64) % 64) + 1)
			raw[i*3+0] = x
			raw[i*3+1] = y
			raw[i*3+2] = przm.GF256Mul(x, y)
		}

		compressed, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}

		inPrzm := filepath.Join(tmpDir, "heis3_direct.przm")
		outBin := filepath.Join(tmpDir, "heis3_direct.out")

		if err := os.WriteFile(inPrzm, compressed, 0o644); err != nil {
			t.Fatalf("failed to write compressed file: %v", err)
		}

		output, err := runCDecompressor(cBinPath, inPrzm, outBin)
		if err != nil {
			t.Fatalf(
				"C decompressor failed on Heisenberg Stride 3 frame (exit status %v):\nOutput:\n%s",
				err,
				string(output),
			)
		}

		decompressed, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatalf("failed to read C decompressed file: %v", err)
		}

		if !bytes.Equal(decompressed, raw) {
			t.Fatalf(
				"Direct Heisenberg Stride 3 interop mismatch: got %d bytes, want %d bytes",
				len(decompressed),
				len(raw),
			)
		}
	})

	t.Run("DirectHeisenbergFrame_Stride4", func(t *testing.T) {
		raw := make([]byte, 4096)
		for i := range 1024 {
			x := uint8((i % 64) + 1)
			y := uint8(((i / 64) % 64) + 1)
			raw[i*4+0] = x
			raw[i*4+1] = y
			raw[i*4+2] = przm.GF256Mul(x, y)
			raw[i*4+3] = 0xFF
		}

		compressed, err := przm.CompressHeisenberg(raw, 3)
		if err != nil {
			t.Fatalf("CompressHeisenberg failed: %v", err)
		}

		inPrzm := filepath.Join(tmpDir, "heis4_direct.przm")
		outBin := filepath.Join(tmpDir, "heis4_direct.out")

		if err := os.WriteFile(inPrzm, compressed, 0o644); err != nil {
			t.Fatalf("failed to write compressed file: %v", err)
		}

		output, err := runCDecompressor(cBinPath, inPrzm, outBin)
		if err != nil {
			t.Fatalf(
				"C decompressor failed on Heisenberg Stride 4 frame (exit status %v):\nOutput:\n%s",
				err,
				string(output),
			)
		}

		decompressed, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatalf("failed to read C decompressed file: %v", err)
		}

		if !bytes.Equal(decompressed, raw) {
			t.Fatalf(
				"Direct Heisenberg Stride 4 interop mismatch: got %d bytes, want %d bytes",
				len(decompressed),
				len(raw),
			)
		}
	})

	// Test Go-generated multi-chunk PRZS stream containers decoded by Pure C reference decompressor
	t.Run("MultiChunkPRZSStream", func(t *testing.T) {
		streamCases := []struct {
			name       string
			chunkSizes []int
			level      int
		}{
			{
				name:       "2_Chunks_Text",
				chunkSizes: []int{8192, 16384},
				level:      1,
			},
			{
				name:       "4_Chunks_Mixed",
				chunkSizes: []int{10000, 25000, 15000, 30000},
				level:      2,
			},
			{
				name:       "8_Chunks_Binary",
				chunkSizes: []int{4096, 8192, 12288, 16384, 20480, 24576, 28672, 32768},
				level:      3,
			},
		}

		for _, sc := range streamCases {
			t.Run(sc.name, func(t *testing.T) {
				var fullOriginal []byte
				var chunks [][]byte

				for idx, sz := range sc.chunkSizes {
					chunkData := make([]byte, sz)
					for i := range chunkData {
						chunkData[i] = byte((i*31 + idx*97 + 7) & 0xFF)
					}
					fullOriginal = append(fullOriginal, chunkData...)

					compChunk, err := przm.Compress(chunkData, sc.level)
					if err != nil {
						t.Fatalf("failed to compress chunk %d: %v", idx, err)
					}
					chunks = append(chunks, compChunk)
				}

				przsContainer := przm.BuildStreamContainer(chunks, uint64(len(fullOriginal)))

				inPrzs := filepath.Join(tmpDir, fmt.Sprintf("%s.przs", sc.name))
				outBin := filepath.Join(tmpDir, fmt.Sprintf("%s.out", sc.name))

				if err := os.WriteFile(inPrzs, przsContainer, 0o644); err != nil {
					t.Fatalf("failed to write PRZS file: %v", err)
				}

				output, err := runCDecompressor(cBinPath, inPrzs, outBin)
				if err != nil {
					t.Fatalf(
						"Pure C decompressor failed on PRZS multi-chunk stream (exit status %v):\nOutput:\n%s",
						err,
						string(output),
					)
				}

				decompressed, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("failed to read C decompressed file: %v", err)
				}

				if !bytes.Equal(decompressed, fullOriginal) {
					t.Fatalf(
						"Decompressed data mismatch (got %d bytes, want %d bytes)",
						len(decompressed),
						len(fullOriginal),
					)
				}

				// Cross-check Go decompressor produces the identical bitstream
				goDec, err := przm.Decompress(przsContainer)
				if err != nil {
					t.Fatalf("Go Decompress failed: %v", err)
				}
				if !bytes.Equal(goDec, fullOriginal) {
					t.Fatalf("Go Decompress mismatch")
				}
			})
		}
	})

	// Test direct Go-compressed GGTC grammar frame interop with Pure C decompressor
	t.Run("DirectGGTCFrame", func(t *testing.T) {
		datasets := []struct {
			name string
			data []byte
		}{
			{
				name: "SmallString",
				data: []byte("Structured Grammar-Graph Tensor Compression (GGTC) Lossless Test!"),
			},
			{
				name: "JSON_16K",
				data: func() []byte {
					var buf bytes.Buffer
					for i := 0; i < 200; i++ {
						fmt.Fprintf(
							&buf,
							"{\"status\":\"ok\",\"code\":200,\"index\":%d,\"msg\":\"hello grammar\"}\n",
							i,
						)
					}
					return buf.Bytes()
				}(),
			},
			{
				name: "RepetitivePattern",
				data: bytes.Repeat([]byte("PRZM_MATHEMATICAL_SINGULARITY_RECONSTRUCTIVE_GRAMMAR_DAG"), 50),
			},
		}

		for _, ds := range datasets {
			t.Run(ds.name, func(t *testing.T) {
				compressed, err := przm.GGTCEncode(ds.data)
				if err != nil {
					t.Fatalf("GGTCEncode failed: %v", err)
				}

				inPrzm := filepath.Join(tmpDir, fmt.Sprintf("ggtc_%s.przm", ds.name))
				outBin := filepath.Join(tmpDir, fmt.Sprintf("ggtc_%s.out", ds.name))

				if err := os.WriteFile(inPrzm, compressed, 0o644); err != nil {
					t.Fatalf("failed to write compressed file: %v", err)
				}

				output, err := runCDecompressor(cBinPath, inPrzm, outBin)
				if err != nil {
					t.Fatalf("C decompressor failed on GGTC frame (exit status %v):\nOutput:\n%s", err, string(output))
				}

				decompressed, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("failed to read C decompressed file: %v", err)
				}

				if !bytes.Equal(decompressed, ds.data) {
					t.Fatalf("GGTC interop mismatch: got %d bytes, want %d bytes", len(decompressed), len(ds.data))
				}

				// Cross-check Go decompressor produces the identical bitstream
				goDec, err := przm.GGTCDecode(compressed)
				if err != nil {
					t.Fatalf("Go GGTCDecode failed: %v", err)
				}
				if !bytes.Equal(goDec, ds.data) {
					t.Fatalf("Go GGTCDecode mismatch")
				}
			})
		}
	})
}
