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

	"github.com/lemon4ksan/przm"
)

func findCDecoderChallenger(t *testing.T) string {
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

func runCDecoderTest(t *testing.T, cBin string, input []byte) (exitCode int, out []byte, err error) {
	t.Helper()
	tmpDir := t.TempDir()
	inPath := filepath.Join(tmpDir, "in.przm")
	outPath := filepath.Join(tmpDir, "out.bin")
	if err := os.WriteFile(inPath, input, 0600); err != nil {
		t.Fatalf("Failed to write temp in.przm: %v", err)
	}
	cmd := exec.Command(cBin, inPath, outPath)
	combined, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			return exitErr.ExitCode(), combined, cmdErr
		}
		return -1, combined, cmdErr
	}
	dec, readErr := os.ReadFile(outPath)
	if readErr != nil {
		return 0, nil, readErr
	}
	return 0, dec, nil
}

func buildTestPNGChunk(chunkType string, data []byte) []byte {
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	buf.Write(lenBuf[:])
	buf.WriteString(chunkType)
	buf.Write(data)

	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(data)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
	buf.Write(crcBuf[:])
	return buf.Bytes()
}

func generateSyntheticPNG(width, height, numIDATs int, withAncillary bool, pattern string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // 8-bit depth
	ihdr[9] = 2 // Truecolor RGB
	buf.Write(buildTestPNGChunk("IHDR", ihdr[:]))

	if withAncillary {
		buf.Write(buildTestPNGChunk("sRGB", []byte{0x00}))
	}

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
				pixelData[px+2] = byte((x * 7 + y * 13) % 256)
			case "checkered":
				val := byte(((x / 4) ^ (y / 4)) * 0xFF)
				pixelData[px] = val
				pixelData[px+1] = val
				pixelData[px+2] = val
			case "solid":
				pixelData[px] = 0x5A
				pixelData[px+1] = 0xA5
				pixelData[px+2] = 0x3C
			default:
				pixelData[px] = byte((x*31 ^ y*17) % 256)
				pixelData[px+1] = byte((x*19 + y*23) % 256)
				pixelData[px+2] = byte((x*11 + 42) % 256)
			}
		}
	}

	if numIDATs <= 1 {
		buf.Write(buildTestPNGChunk("IDAT", pixelData))
	} else {
		chunkSize := (len(pixelData) + numIDATs - 1) / numIDATs
		for pos := 0; pos < len(pixelData); pos += chunkSize {
			end := pos + chunkSize
			if end > len(pixelData) {
				end = len(pixelData)
			}
			buf.Write(buildTestPNGChunk("IDAT", pixelData[pos:end]))
		}
	}

	if withAncillary {
		buf.Write(buildTestPNGChunk("tEXt", []byte("Challenger\x00Milestone 6 Empirical Suite")))
	}

	buf.Write(buildTestPNGChunk("IEND", nil))
	return buf.Bytes()
}

// TestChallenger_M6_1_Archival_PNG_Adversarial stress-tests truncated and corrupted Archival PNG streams.
func TestChallenger_M6_1_Archival_PNG_Adversarial(t *testing.T) {
	cBin := findCDecoderChallenger(t)

	validPNG := generateSyntheticPNG(32, 32, 2, true, "gradient")
	validComp, err := przm.CompressArchivalPNG(validPNG, 3)
	if err != nil {
		t.Fatalf("Baseline CompressArchivalPNG failed: %v", err)
	}
	if len(validComp) < 12 {
		t.Fatalf("Baseline compressed length too small: %d", len(validComp))
	}

	metaSize := int(binary.LittleEndian.Uint32(validComp[8:12]))

	type attackCase struct {
		name string
		data []byte
	}

	var attacks []attackCase

	// 1. Truncated container headers (< 12 bytes)
	for _, l := range []int{0, 1, 3, 4, 7, 8, 11} {
		if l <= len(validComp) {
			attacks = append(attacks, attackCase{
				name: fmt.Sprintf("TruncatedHeader_Len%d", l),
				data: append([]byte(nil), validComp[:l]...),
			})
		}
	}

	// 2. Partial metadata block
	if 12+metaSize <= len(validComp) {
		attacks = append(attacks, attackCase{
			name: "PartialMetadata_HeaderOnly",
			data: append([]byte(nil), validComp[:12]...),
		})
		attacks = append(attacks, attackCase{
			name: "PartialMetadata_Midway",
			data: append([]byte(nil), validComp[:12+metaSize/2]...),
		})
	}

	// 3. Invalid metadata sizes (< 8 or exceeding length)
	badMetaSmall := append([]byte(nil), validComp...)
	binary.LittleEndian.PutUint32(badMetaSmall[8:12], 4)
	attacks = append(attacks, attackCase{name: "MetaSize_TooSmall_4B", data: badMetaSmall})

	badMetaHuge := append([]byte(nil), validComp...)
	binary.LittleEndian.PutUint32(badMetaHuge[8:12], uint32(len(validComp)+1000))
	attacks = append(attacks, attackCase{name: "MetaSize_ExceedsStream", data: badMetaHuge})

	// 4. Invalid metadata magic (byte 12 != 0x50 'P')
	badMetaMagic := append([]byte(nil), validComp...)
	badMetaMagic[12] = 0xAA
	attacks = append(attacks, attackCase{name: "Invalid_MetaMagic_0xAA", data: badMetaMagic})

	// 5. Corrupted chunk descriptors & lengths exceeding bounds
	badChunkLen := append([]byte(nil), validComp...)
	// IDAT descriptor starts at offset 20 (12 + 8)
	binary.LittleEndian.PutUint32(badChunkLen[20:24], 0xFFFFFFF0)
	attacks = append(attacks, attackCase{name: "IDAT_ChunkLen_HugeOverflow", data: badChunkLen})

	badIDATCount := append([]byte(nil), validComp...)
	binary.LittleEndian.PutUint16(badIDATCount[18:20], 9999) // idatCount = 9999
	attacks = append(attacks, attackCase{name: "IDAT_Count_Desync", data: badIDATCount})

	// 6. Truncated inner compressed stream
	attacks = append(attacks, attackCase{
		name: "TruncatedInner_OnlyMeta",
		data: append([]byte(nil), validComp[:12+metaSize]...),
	})
	if 12+metaSize+4 < len(validComp) {
		attacks = append(attacks, attackCase{
			name: "TruncatedInner_PartialInnerPRZM",
			data: append([]byte(nil), validComp[:12+metaSize+4]...),
		})
	}

	dstBuf := make([]byte, len(validPNG)+64)

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			// A. Go DecompressTo & Decompress verification
			_, errTo := przm.DecompressTo(tc.data, dstBuf)
			if errTo == nil {
				t.Fatalf("Go DecompressTo unexpectedly accepted %s", tc.name)
			}
			_, errDec := przm.Decompress(tc.data)
			if errDec == nil {
				t.Fatalf("Go Decompress unexpectedly accepted %s", tc.name)
			}

			// B. Pure C verification
			exitCode, out, cErr := runCDecoderTest(t, cBin, tc.data)
			if cErr == nil || exitCode == 0 {
				t.Fatalf("Pure C decompressor unexpectedly accepted %s (exit %d)", tc.name, exitCode)
			}
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf("Pure C decompressor crashed or exited unexpectedly (code %d): %s", exitCode, string(out))
			}
		})
	}
}

func buildSyntheticDeltaFrame(cmds, offs, lits []byte, uSize int) []byte {
	var buf bytes.Buffer
	buf.WriteString("PRZM")
	buf.WriteByte(przm.FlagDeltaMask)
	var szBuf [3]byte
	szBuf[0] = byte(uSize)
	szBuf[1] = byte(uSize >> 8)
	szBuf[2] = byte(uSize >> 16)
	buf.Write(szBuf[:])

	cmdSectionLen := 1 + 4 + len(cmds) // mode (1B) + len (4B) + cmds
	var dir [4]byte
	binary.LittleEndian.PutUint16(dir[0:2], uint16(cmdSectionLen))
	binary.LittleEndian.PutUint16(dir[2:4], uint16(len(offs)))
	buf.Write(dir[:])

	buf.Write(make([]byte, 8)) // Section 1 padding

	buf.WriteByte(0) // Section 2: ModeFlat
	var cLenBuf [4]byte
	binary.LittleEndian.PutUint32(cLenBuf[:], uint32(len(cmds)))
	buf.Write(cLenBuf[:])
	buf.Write(cmds)

	buf.Write(offs)
	buf.Write(lits)
	return buf.Bytes()
}

// TestChallenger_M6_1_Approx_Matching_Adversarial stress-tests malformed delta match streams.
func TestChallenger_M6_1_Approx_Matching_Adversarial(t *testing.T) {
	cBin := findCDecoderChallenger(t)

	type deltaAttack struct {
		name string
		data []byte
	}

	var attacks []deltaAttack

	// Baseline: 16 literal bytes 'A' (F0 10), then delta match
	litPrefixCmds := []byte{0xF0, 0x10}
	litPrefixData := bytes.Repeat([]byte("A"), 16)

	// 1. Invalid opcode flags: Locality index k >= 3 (0xE3 .. 0xE7)
	for k := 3; k <= 7; k++ {
		op := byte(0xE0 | k)
		cmds := append(append([]byte(nil), litPrefixCmds...), op, 0x00) // baseLen=0 -> mLen=4
		lits := append(append([]byte(nil), litPrefixData...), 0x00)      // cMods=0
		attacks = append(attacks, deltaAttack{
			name: fmt.Sprintf("InvalidOpcode_DeltaRing_k%d", k),
			data: buildSyntheticDeltaFrame(cmds, nil, lits, 32),
		})
	}

	// 2. Truncated diff offsets & XOR bytes
	// 2a. cMods = 2 but literals end after cMods byte
	cmds2a := append(append([]byte(nil), litPrefixCmds...), 0xE0, 0x00) // k=0, baseLen=0
	lits2a := append(append([]byte(nil), litPrefixData...), 0x02)       // cMods=2, no offset/xor follows
	attacks = append(attacks, deltaAttack{
		name: "TruncatedDelta_MissingAllMods",
		data: buildSyntheticDeltaFrame(cmds2a, nil, lits2a, 32),
	})

	// 2b. cMods = 1, relOffset present but missing XOR byte
	lits2b := append(append([]byte(nil), litPrefixData...), 0x01, 0x02) // cMods=1, relOffset=2, EOF
	attacks = append(attacks, deltaAttack{
		name: "TruncatedDelta_MissingXORByte",
		data: buildSyntheticDeltaFrame(cmds2a, nil, lits2b, 32),
	})

	// 2c. Explicit Delta (0xE8) with truncated offset stream in Section 3
	cmds2c := append(append([]byte(nil), litPrefixCmds...), 0xE8, 0x00)
	lits2c := append(append([]byte(nil), litPrefixData...), 0x00)
	attacks = append(attacks, deltaAttack{
		name: "TruncatedDelta_ExplicitEmptyOffsetSection",
		data: buildSyntheticDeltaFrame(cmds2c, nil, lits2c, 32),
	})

	// 3. Out-of-bounds match references
	// 3a. Explicit offset distance d=100 > dstPos=16
	cmds3a := append(append([]byte(nil), litPrefixCmds...), 0xE8, 0x00)
	offs3a := []byte{0x64} // d = 100
	lits3a := append(append([]byte(nil), litPrefixData...), 0x00)
	attacks = append(attacks, deltaAttack{
		name: "OOB_MatchRef_DistanceExceedsDecodedBytes",
		data: buildSyntheticDeltaFrame(cmds3a, offs3a, lits3a, 64),
	})

	// 3b. Match length overflows uncompressed size (uSize=20, dstPos=16, mLen=16 -> 32)
	cmds3b := append(append([]byte(nil), litPrefixCmds...), 0xE0, 12) // baseLen=12 -> mLen=16
	lits3b := append(append([]byte(nil), litPrefixData...), 0x00)
	attacks = append(attacks, deltaAttack{
		name: "OOB_MatchRef_LengthExceedsCapacity",
		data: buildSyntheticDeltaFrame(cmds3b, nil, lits3b, 20),
	})

	dstBuf := make([]byte, 256)

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			_, errTo := przm.DecompressTo(tc.data, dstBuf)
			if errTo == nil {
				t.Fatalf("Go DecompressTo accepted %s", tc.name)
			}
			_, errDec := przm.Decompress(tc.data)
			if errDec == nil {
				t.Fatalf("Go Decompress accepted %s", tc.name)
			}

			exitCode, out, cErr := runCDecoderTest(t, cBin, tc.data)
			if cErr == nil || exitCode == 0 {
				t.Fatalf("Pure C decompressor accepted %s (exit %d)", tc.name, exitCode)
			}
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf("Pure C decompressor crashed on %s (code %d): %s", tc.name, exitCode, string(out))
			}
		})
	}
}

// TestChallenger_M6_1_BitExact_Roundtrips verifies 100% bit-exact reconstruction of multi-IDAT PNG containers.
func TestChallenger_M6_1_BitExact_Roundtrips(t *testing.T) {
	cBin := findCDecoderChallenger(t)

	cases := []struct {
		name          string
		w, h          int
		chunks        int
		withAncillary bool
		pattern       string
		level         int
	}{
		{"SingleIDAT_16x16_Gradient", 16, 16, 1, false, "gradient", 1},
		{"TwoIDAT_32x32_Checkered", 32, 32, 2, false, "checkered", 3},
		{"FourIDAT_64x64_Solid_Ancillary", 64, 64, 4, true, "solid", 5},
		{"EightIDAT_128x32_Patterned", 128, 32, 8, true, "patterned", 3},
		{"SixteenIDAT_64x128_Gradient_Ancillary", 64, 128, 16, true, "gradient", 5},
		{"MultiIDAT_256x16_Checkered", 256, 16, 5, false, "checkered", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rawPNG := generateSyntheticPNG(tc.w, tc.h, tc.chunks, tc.withAncillary, tc.pattern)
			if len(rawPNG) == 0 {
				t.Fatalf("Failed to generate raw PNG for %s", tc.name)
			}

			// 1. CompressArchivalPNG
			comp, err := przm.CompressArchivalPNG(rawPNG, tc.level)
			if err != nil {
				t.Fatalf("CompressArchivalPNG failed: %v", err)
			}
			if len(comp) < 12 {
				t.Fatalf("Compressed frame too small: %d", len(comp))
			}
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != przm.ArchivalPNGMagic {
				t.Fatalf("Header magic mismatch: got 0x%08X, want 0x%08X", magic, przm.ArchivalPNGMagic)
			}

			// 2. Go DecompressTo
			dst := make([]byte, len(rawPNG)+64)
			decTo, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("Go DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decTo, rawPNG) {
				t.Fatalf("Go DecompressTo output bit-exact mismatch! len got=%d, want=%d", len(decTo), len(rawPNG))
			}

			// 3. Go Decompress
			decAlloc, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Go Decompress failed: %v", err)
			}
			if !bytes.Equal(decAlloc, rawPNG) {
				t.Fatalf("Go Decompress output bit-exact mismatch!")
			}

			// 4. Pure C test_przm_dec.exe cross-language verification
			exitCode, cDec, cErr := runCDecoderTest(t, cBin, comp)
			if cErr != nil || exitCode != 0 {
				t.Fatalf("Pure C decompressor failed (exit %d): %v", exitCode, cErr)
			}
			if !bytes.Equal(cDec, rawPNG) {
				t.Fatalf("Pure C output bit-exact mismatch with original PNG! len got=%d, want=%d", len(cDec), len(rawPNG))
			}

			t.Logf("[%s] PASSED: PNG %d B -> PRZM %d B (%.2fx) | Go & Pure C Bit-Exact",
				tc.name, len(rawPNG), len(comp), float64(len(rawPNG))/float64(len(comp)))
		})
	}
}
