// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lemon4ksan/przm"
)

func findCDecoderM6(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

func buildPNGChunk(chunkType string, data []byte) []byte {
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	buf.Write(lenBuf[:])
	buf.WriteString(chunkType)
	buf.Write(data)

	// Compute IEEE CRC32 over chunkType + data
	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(data)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
	buf.Write(crcBuf[:])

	return buf.Bytes()
}

// generateMockPNG creates valid synthetic PNG containers with custom dimensions, chunking, and metadata.
func generateMockPNG(width, height int, splitIDATChunks int, withAncillary bool, pattern string) []byte {
	var buf bytes.Buffer
	// PNG 8-byte signature
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	// IHDR chunk
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // 8-bit depth
	ihdr[9] = 2 // Truecolor RGB
	buf.Write(buildPNGChunk("IHDR", ihdr[:]))

	// Optional ancillary chunk before IDAT (prefix)
	if withAncillary {
		srgb := []byte{0x00} // Perceptual rendering intent
		buf.Write(buildPNGChunk("sRGB", srgb))
	}

	// Uncompressed scanline payload
	rawPixelCount := (width*3 + 1) * height
	pixelData := make([]byte, rawPixelCount)
	for y := 0; y < height; y++ {
		rowStart := y * (width*3 + 1)
		pixelData[rowStart] = 0 // Filter None
		for x := 0; x < width; x++ {
			px := rowStart + 1 + x*3
			switch pattern {
			case "gradient":
				pixelData[px] = byte(x % 256)
				pixelData[px+1] = byte(y % 256)
				pixelData[px+2] = byte((x + y) % 256)
			case "checkered":
				val := byte(((x / 8) ^ (y / 8)) * 0xFF)
				pixelData[px] = val
				pixelData[px+1] = val
				pixelData[px+2] = val
			case "solid":
				pixelData[px] = 0xAA
				pixelData[px+1] = 0xBB
				pixelData[px+2] = 0xCC
			default:
				pixelData[px] = byte((x*17 + y*31) % 256)
				pixelData[px+1] = byte((x*13 ^ y*7) % 256)
				pixelData[px+2] = byte((x*19 + 5) % 256)
			}
		}
	}

	// Split IDAT payload into 1 or more IDAT chunks
	if splitIDATChunks <= 1 {
		buf.Write(buildPNGChunk("IDAT", pixelData))
	} else {
		chunkSize := (len(pixelData) + splitIDATChunks - 1) / splitIDATChunks
		for pos := 0; pos < len(pixelData); pos += chunkSize {
			end := pos + chunkSize
			if end > len(pixelData) {
				end = len(pixelData)
			}
			buf.Write(buildPNGChunk("IDAT", pixelData[pos:end]))
		}
	}

	// Optional ancillary chunk after IDAT (postfix)
	if withAncillary {
		comment := []byte("Author\x00PRZM Milestone 6 Empirical Challenger")
		buf.Write(buildPNGChunk("tEXt", comment))
	}

	// IEND chunk
	buf.Write(buildPNGChunk("IEND", nil))
	return buf.Bytes()
}

// TestChallenger_M6_2_CrossLanguage_Archival_Parity verifies 100% bit-exact byte-for-byte parity
// between Go CompressOptions{ArchivalMode: true} and Pure C test_przm_dec.exe.
func TestChallenger_M6_2_CrossLanguage_Archival_Parity(t *testing.T) {
	cBinPath := findCDecoderM6(t)
	tmpDir := t.TempDir()

	testCases := []struct {
		name          string
		width         int
		height        int
		splitChunks   int
		withAncillary bool
		pattern       string
	}{
		{"Small_16x16_Gradient", 16, 16, 1, false, "gradient"},
		{"Standard_32x32_Checkered", 32, 32, 1, false, "checkered"},
		{"Medium_64x64_MultiChunk3", 64, 64, 3, false, "gradient"},
		{"Wide_128x32_Solid_Ancillary", 128, 32, 1, true, "solid"},
		{"Large_128x128_MultiChunk4_Ancillary", 128, 128, 4, true, "patterned"},
		{"Rect_256x64_MultiChunk2", 256, 64, 2, false, "gradient"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rawPNG := generateMockPNG(tc.width, tc.height, tc.splitChunks, tc.withAncillary, tc.pattern)
			if len(rawPNG) == 0 {
				t.Fatalf("Failed to generate mock PNG")
			}

			// 1. Compress using Go CompressOptions{ArchivalMode: true}
			comp, err := przm.CompressWithOptions(rawPNG, przm.CompressOptions{
				ArchivalMode: true,
			})
			if err != nil {
				t.Fatalf("przm.CompressWithOptions(ArchivalMode=true) failed: %v", err)
			}
			if len(comp) < 12 {
				t.Fatalf("Compressed frame too small: %d bytes", len(comp))
			}

			// Verify frame magic is ArchivalPNGMagic (0x504E4741 = 'PNGA')
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != przm.ArchivalPNGMagic {
				t.Fatalf("Expected ArchivalPNGMagic 0x%08X, got 0x%08X", przm.ArchivalPNGMagic, magic)
			}

			// 2. Verify Go roundtrip decode
			goDec, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Go Decompress failed: %v", err)
			}
			if !bytes.Equal(goDec, rawPNG) {
				for i := 0; i < len(rawPNG) && i < len(goDec); i++ {
					if rawPNG[i] != goDec[i] {
						t.Fatalf("Go Decompress output does not match original PNG at byte %d (len raw=%d, len dec=%d): want 0x%02X, got 0x%02X",
							i, len(rawPNG), len(goDec), rawPNG[i], goDec[i])
					}
				}
				t.Fatalf("Go Decompress output length mismatch: raw=%d, dec=%d", len(rawPNG), len(goDec))
			}

			dstBuf := make([]byte, len(rawPNG)+64)
			goDecTo, err := przm.DecompressTo(comp, dstBuf)
			if err != nil {
				t.Fatalf("Go DecompressTo failed: %v", err)
			}
			if !bytes.Equal(goDecTo, rawPNG) {
				t.Fatalf("Go DecompressTo output does not match original PNG")
			}

			// 3. Write compressed archival frame to temporary storage
			inPrzm := filepath.Join(tmpDir, tc.name+".przm")
			outBin := filepath.Join(tmpDir, tc.name+".png")
			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("Failed to write temporary przm file: %v", err)
			}

			// 4. Execute Pure C test_przm_dec.exe on compressed archival frame
			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor returned error %v:\nOutput:\n%s", err, string(output))
			}

			// 5. Verify exit code 0 and 100% bit-exact byte-for-byte parity
			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("Failed to read Pure C decompressed output: %v", err)
			}

			if len(cDec) != len(rawPNG) {
				t.Fatalf("Pure C output length mismatch: got %d bytes, want %d bytes", len(cDec), len(rawPNG))
			}

			if !bytes.Equal(cDec, rawPNG) {
				t.Fatalf("100%% Parity Violation: Pure C output does not match original PNG bit-for-bit!")
			}

			if !bytes.Equal(cDec, goDec) {
				t.Fatalf("Cross-language mismatch between Pure C and Go decompressor outputs!")
			}

			ratio := float64(len(rawPNG)) / float64(len(comp))
			t.Logf("[%s] SUCCESS: PNG %d bytes -> PRZM %d bytes (%.2fx) -> Pure C Bit-Exact Parity Confirmed",
				tc.name, len(rawPNG), len(comp), ratio)
		})
	}
}

// TestChallenger_M6_2_Archival_ZeroAlloc verifies testing.AllocsPerRun(100, ...) is strictly 0.00
// during DecompressTo on archival streams (both PNG and non-PNG streams).
func TestChallenger_M6_2_Archival_ZeroAlloc(t *testing.T) {
	testCases := []struct {
		name string
		gen  func() ([]byte, []byte) // returns (compressed, original)
	}{
		{
			name: "ArchivalPNG_32x32",
			gen: func() ([]byte, []byte) {
				png := generateMockPNG(32, 32, 1, false, "gradient")
				comp, err := przm.CompressWithOptions(png, przm.CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, png
			},
		},
		{
			name: "ArchivalPNG_64x64_MultiChunk",
			gen: func() ([]byte, []byte) {
				png := generateMockPNG(64, 64, 3, true, "checkered")
				comp, err := przm.CompressWithOptions(png, przm.CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, png
			},
		},
		{
			name: "ArchivalPNG_128x64",
			gen: func() ([]byte, []byte) {
				png := generateMockPNG(128, 64, 2, false, "gradient")
				comp, err := przm.CompressWithOptions(png, przm.CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, png
			},
		},
		{
			name: "ArchivalApproxMatch_64KB",
			gen: func() ([]byte, []byte) {
				raw := make([]byte, 65536)
				for i := range raw {
					raw[i] = byte(i % 128)
				}
				// Inject periodic Hamming <= 2 delta
				for i := 64; i < len(raw); i += 64 {
					raw[i] ^= 0x03
				}
				comp, err := przm.CompressWithOptions(raw, przm.CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, raw
			},
		},
		{
			name: "ArchivalApproxMatch_256KB",
			gen: func() ([]byte, []byte) {
				raw := make([]byte, 262144)
				for i := range raw {
					raw[i] = byte(i*17 + 5)
				}
				for i := 128; i < len(raw); i += 128 {
					raw[i] ^= 0x01
				}
				comp, err := przm.CompressWithOptions(raw, przm.CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, raw
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			comp, original := tc.gen()
			origLen := len(original)
			dst := make([]byte, origLen+64)

			// Warmup run
			decWarmup, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("Warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decWarmup, original) {
				for i := 0; i < len(original) && i < len(decWarmup); i++ {
					if original[i] != decWarmup[i] {
						t.Fatalf("Warmup DecompressTo mismatch at byte %d (len orig=%d, len dec=%d): want 0x%02X, got 0x%02X",
							i, len(original), len(decWarmup), original[i], decWarmup[i])
					}
				}
				t.Fatalf("Warmup DecompressTo length mismatch: orig=%d, dec=%d", len(original), len(decWarmup))
			}

			// Strictly verify testing.AllocsPerRun(100, ...) is strictly 0.00
			allocs := testing.AllocsPerRun(100, func() {
				out, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("DecompressTo in AllocsPerRun failed: %v", err)
				}
				_ = out
			})

			if allocs != 0.0 {
				t.Fatalf("ZERO-ALLOC VIOLATION: DecompressTo produced %.2f allocs/run (mandated strictly 0.00)", allocs)
			}

			t.Logf("[%s] AllocsPerRun(100) = %.2f (strictly 0.00 zero-heap invariant verified)", tc.name, allocs)
		})
	}
}

// TestChallenger_M6_2_MainMode_LineRate_Immunity benchmarks Compress and DecompressTo
// at Level 1 and Level 3 to confirm archival additions introduced ZERO regression
// to Main Mode throughput (retaining 10-50+ GB/s line rate).
func TestChallenger_M6_2_MainMode_LineRate_Immunity(t *testing.T) {
	sizes := []struct {
		name      string
		numBlocks int
	}{
		{"256KB_4Blocks", 4},
		{"1MB_16Blocks", 16},
		{"4MB_64Blocks", 64},
	}

	levels := []int{1, 3}

	for _, sz := range sizes {
		// Generate repeatable multi-block payload exercising line-rate Main Mode
		block := make([]byte, przm.DedupBlockSize)
		for i := range block {
			block[i] = byte((i * 31) ^ (i >> 5) ^ 0x69)
		}
		var raw bytes.Buffer
		for range sz.numBlocks {
			raw.Write(block)
		}
		data := raw.Bytes()
		totalBytes := len(data)

		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Lvl%d", sz.name, lvl)
			t.Run(testName, func(t *testing.T) {
				// 1. Measure Main Mode Compress throughput
				compStart := time.Now()
				compIters := 50
				if sz.numBlocks >= 64 {
					compIters = 10
				}
				var comp []byte
				var err error
				for i := 0; i < compIters; i++ {
					comp, err = przm.Compress(data, lvl)
					if err != nil {
						t.Fatalf("Compress failed: %v", err)
					}
				}
				compElapsed := time.Since(compStart)
				compMBps := (float64(totalBytes*compIters) / (1024 * 1024)) / compElapsed.Seconds()
				t.Logf("[%s] Compress: processed %d bytes in %v => %.2f MB/s",
					testName, totalBytes*compIters, compElapsed, compMBps)

				dst := make([]byte, totalBytes+64)

				// Warmup run
				decWarmup, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("Warmup DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decWarmup, data) {
					t.Fatalf("DecompressTo data mismatch")
				}

				// 2. Measure Main Mode DecompressTo throughput
				decIters := 5000
				if sz.numBlocks >= 64 {
					decIters = 1000
				} else if sz.numBlocks >= 16 {
					decIters = 3000
				}

				decStart := time.Now()
				for i := 0; i < decIters; i++ {
					_, _ = przm.DecompressTo(comp, dst)
				}
				decElapsed := time.Since(decStart)

				bytesProcessed := float64(totalBytes) * float64(decIters)
				decGBps := (bytesProcessed / (1024 * 1024 * 1024)) / decElapsed.Seconds()

				t.Logf("[%s] DecompressTo: processed %d bytes x %d iters in %v => %.2f GB/s",
					testName, totalBytes, decIters, decElapsed, decGBps)

				// Verify line rate >= 10.0 GB/s (saturating RAM-speed 10-50+ GB/s bracket)
				if decGBps < 10.0 {
					t.Errorf("Line rate regression detected: got %.2f GB/s, expected >= 10.00 GB/s", decGBps)
				} else {
					t.Logf("[%s] SUCCESS: Main Mode line-rate immunity confirmed (%.2f GB/s in 10-50+ GB/s bracket)",
						testName, decGBps)
				}
			})
		}
	}
}
