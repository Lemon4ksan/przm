// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// helper to construct arbitrary balanced/unbalanced chunk ensembles
func generateConfiguredPayload(numBlocks int, seed int64, balancedTargets int) []byte {
	blocks := make([][16]byte, numBlocks)
	key := [16]byte{
		0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
		0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c,
	}
	key[0] ^= byte(seed)
	key[15] ^= byte(seed >> 8)

	numChunks := numBlocks / CryptoCoreChunkBlocks
	for c := range numChunks {
		chunkStart := c * CryptoCoreChunkBlocks
		isBalanced := (c % 256) < balancedTargets

		if isBalanced {
			// Round 3 Lambda set: active byte 0 varies 0..255, remainder constant
			for i := range CryptoCoreChunkBlocks {
				var pt [16]byte
				pt[0] = byte(i)
				for j := 1; j < 16; j++ {
					pt[j] = byte(j*17 + c*3)
				}
				blocks[chunkStart+i] = EncryptReducedRound(pt[:], key[:], 3, true)
			}
		} else {
			// Round 4 Counter: non-zero sum
			for i := range CryptoCoreChunkBlocks {
				idx := chunkStart + i
				var pt [16]byte
				pt[0] = byte(idx)
				pt[1] = byte(idx >> 8)
				pt[2] = byte(idx >> 16)
				for j := 3; j < 16; j++ {
					pt[j] = byte(j*11 + c)
				}
				blocks[idx] = EncryptReducedRound(pt[:], key[:], 4, true)
			}
		}
	}

	for idx := numChunks * CryptoCoreChunkBlocks; idx < numBlocks; idx++ {
		var pt [16]byte
		pt[0] = byte(idx)
		pt[1] = byte(idx >> 8)
		for j := 2; j < 16; j++ {
			pt[j] = byte(j * 7)
		}
		blocks[idx] = EncryptReducedRound(pt[:], key[:], 4, true)
	}

	raw := make([]byte, numBlocks*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}
	return raw
}

// TestChallenger_1MiB_DiverseSeeds stress-tests the 1 MiB pure crypto-core payload
// across 13 diverse seeds and validates that compression savings strictly fall
// in [1,630, 3,170] bytes (0.155% - 0.302%) with 100% bit-exact lossless reconstruction.
func TestChallenger_1MiB_DiverseSeeds(t *testing.T) {
	seeds := []int64{
		0, 1, 42, 123, 777, 1024, 9999, 65535,
		1234567, 987654321, 0xDEADBEEF, -1, 0x1337CAFE,
	}

	const totalBlocks = 65536 // 1 MiB = 1,048,576 bytes
	const expectedRawLen = 1048576

	totalBytesVerified := 0

	for _, seed := range seeds {
		raw := generateConfiguredPayload(totalBlocks, seed, 130) // 130 balanced chunks
		if len(raw) != expectedRawLen {
			t.Fatalf("[seed=%d] Unexpected raw length: got %d, want %d", seed, len(raw), expectedRawLen)
		}

		compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+4096)
		compLen, err := CompressCryptoCore(raw, compBuf)
		if err != nil {
			t.Fatalf("[seed=%d] CompressCryptoCore failed: %v", seed, err)
		}
		compressed := compBuf[:compLen]

		// 1. Check savings bounds
		bytesSaved := len(raw) - compLen
		savingPct := (float64(bytesSaved) / float64(len(raw))) * 100.0

		if bytesSaved < TheoreticalMinBytesSaved1MiB || bytesSaved > TheoreticalMaxBytesSaved1MiB {
			t.Fatalf("[seed=%d] Compression savings %d bytes outside theoretical [%d, %d]",
				seed, bytesSaved, TheoreticalMinBytesSaved1MiB, TheoreticalMaxBytesSaved1MiB)
		}
		if savingPct < TheoreticalMinDeficitPercent || savingPct > TheoreticalMaxDeficitPercent {
			t.Fatalf("[seed=%d] Compression pct %.4f%% outside theoretical [%.3f%%, %.3f%%]",
				seed, savingPct, TheoreticalMinDeficitPercent, TheoreticalMaxDeficitPercent)
		}

		// 2. Check lossless decompress
		decomp := make([]byte, len(raw))
		decLen, err := DecompressCryptoCore(compressed, decomp)
		if err != nil {
			t.Fatalf("[seed=%d] DecompressCryptoCore failed: %v", seed, err)
		}
		if decLen != len(raw) {
			t.Fatalf("[seed=%d] Decompressed length mismatch: got %d, want %d", seed, decLen, len(raw))
		}
		if !bytes.Equal(raw, decomp) {
			t.Fatalf("[seed=%d] LOSSLESS RECONSTRUCTION FAILURE: Byte mismatch detected!", seed)
		}

		totalBytesVerified += len(raw)
		t.Logf("[Seed %10d] Saved: %d bytes (%.4f%%) | Lossless: 100%% EXACT (%d bytes)",
			seed, bytesSaved, savingPct, len(raw))
	}

	t.Logf("--> Total bit-exact payload bytes verified across all seeds: %d bytes (0 corrupted bytes)",
		totalBytesVerified)
}

// TestChallenger_VariedChunkBalanceRatios systematically tests chunk balance ratios:
// 0%, 25%, 41.02% (min threshold), 50%, 50.78% (PoC), 75%, 78.52% (max threshold), 100%.
// It validates the analytical formula: Net Savings = 16*k - 48 bytes (for k > 0)
// and confirms 100% lossless reconstruction across all balance ratios.
func TestChallenger_VariedChunkBalanceRatios(t *testing.T) {
	type ratioCase struct {
		name              string
		balancedCount     int     // k out of 256 chunks
		ratioPct          float64 // k / 256 * 100%
		expectIn1MiBBound bool
	}

	cases := []ratioCase{
		{name: "0% Balanced (All Raw)", balancedCount: 0, ratioPct: 0.0, expectIn1MiBBound: false},
		{name: "10% Balanced", balancedCount: 26, ratioPct: 26.0 / 256.0 * 100.0, expectIn1MiBBound: false},
		{name: "25% Balanced", balancedCount: 64, ratioPct: 25.0, expectIn1MiBBound: false},
		{name: "41.02% Balanced (Exact Lower Bound Threshold)", balancedCount: 105, ratioPct: 105.0 / 256.0 * 100.0, expectIn1MiBBound: true},
		{name: "50% Balanced", balancedCount: 128, ratioPct: 50.0, expectIn1MiBBound: true},
		{name: "50.78% Balanced (PRZM PoC Target)", balancedCount: 130, ratioPct: 130.0 / 256.0 * 100.0, expectIn1MiBBound: true},
		{name: "75% Balanced", balancedCount: 192, ratioPct: 75.0, expectIn1MiBBound: true},
		{name: "78.52% Balanced (Exact Upper Bound Threshold)", balancedCount: 201, ratioPct: 201.0 / 256.0 * 100.0, expectIn1MiBBound: true},
		{name: "90% Balanced", balancedCount: 230, ratioPct: 230.0 / 256.0 * 100.0, expectIn1MiBBound: false},
		{name: "100% Balanced (All Zero-Sum)", balancedCount: 256, ratioPct: 100.0, expectIn1MiBBound: false},
	}

	const totalBlocks = 65536
	const expectedRawLen = 1048576

	for _, tc := range cases {
		raw := generateConfiguredPayload(totalBlocks, 7777, tc.balancedCount)

		compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+4096)
		compLen, err := CompressCryptoCore(raw, compBuf)
		if err != nil {
			t.Fatalf("[%s] Compress failed: %v", tc.name, err)
		}
		compressed := compBuf[:compLen]

		// Verify decompress
		decomp := make([]byte, len(raw))
		decLen, err := DecompressCryptoCore(compressed, decomp)
		if err != nil {
			t.Fatalf("[%s] Decompress failed: %v", tc.name, err)
		}
		if decLen != len(raw) || !bytes.Equal(raw, decomp) {
			t.Fatalf("[%s] Lossless roundtrip byte mismatch!", tc.name)
		}

		bytesSaved := len(raw) - compLen
		savingPct := (float64(bytesSaved) / float64(len(raw))) * 100.0

		if tc.balancedCount == 0 {
			// Mode 0: Passthrough mode (overhead = 16 bytes)
			if compLen != len(raw)+CryptoCoreHeaderSize {
				t.Fatalf("[%s] Passthrough compLen mismatch: got %d, want %d",
					tc.name, compLen, len(raw)+CryptoCoreHeaderSize)
			}
			t.Logf("[%s] Ratio: %.2f%% (k=%d) | Passthrough compLen=%d, Saved=%d B | Lossless: PASS",
				tc.name, tc.ratioPct, tc.balancedCount, compLen, bytesSaved)
		} else {
			// Mode 1: Net savings formula = 16*k - 48
			expectedSavings := 16*tc.balancedCount - 48
			if bytesSaved != expectedSavings {
				t.Fatalf("[%s] Savings mismatch: got %d, want %d (from 16*%d - 48)",
					tc.name, bytesSaved, expectedSavings, tc.balancedCount)
			}

			inBounds := (bytesSaved >= TheoreticalMinBytesSaved1MiB && bytesSaved <= TheoreticalMaxBytesSaved1MiB)
			if inBounds != tc.expectIn1MiBBound {
				t.Fatalf("[%s] Bounds check failure: inBounds=%v, expected=%v (savings=%d B, pct=%.4f%%)",
					tc.name, inBounds, tc.expectIn1MiBBound, bytesSaved, savingPct)
			}

			t.Logf("[%s] Ratio: %.2f%% (k=%d) | Saved=%4d B (%.4f%%) | InBounds: %-5v | Lossless: PASS",
				tc.name, tc.ratioPct, tc.balancedCount, bytesSaved, savingPct, inBounds)
		}
	}
}

// TestChallenger_BoundarySizes stress-tests boundary cases:
// 0 bytes, 15 bytes, 16 bytes, 255 blocks, 256 blocks (unbalanced/balanced),
// 257 blocks (unbalanced/balanced), 10,000 blocks (unbalanced/balanced),
// and unaligned irregular byte lengths.
func TestChallenger_BoundarySizes(t *testing.T) {
	type boundaryCase struct {
		name       string
		byteLength int
		isBalanced bool
	}

	cases := []boundaryCase{
		{name: "0 Bytes (Empty Payload)", byteLength: 0, isBalanced: false},
		{name: "1 Byte (Single Byte)", byteLength: 1, isBalanced: false},
		{name: "7 Bytes (Sub-Block Odd)", byteLength: 7, isBalanced: false},
		{name: "15 Bytes (1 byte shy of 1 AES block)", byteLength: 15, isBalanced: false},
		{name: "16 Bytes (Exact 1 AES block)", byteLength: 16, isBalanced: false},
		{name: "31 Bytes (Odd unaligned)", byteLength: 31, isBalanced: false},
		{name: "100 Bytes (Unaligned)", byteLength: 100, isBalanced: false},
		{name: "255 Blocks (4,080 B - 1 block shy of 1 chunk)", byteLength: 255 * 16, isBalanced: false},
		{name: "256 Blocks (4,096 B - 1 chunk, Unbalanced)", byteLength: 256 * 16, isBalanced: false},
		{name: "256 Blocks (4,096 B - 1 chunk, Balanced)", byteLength: 256 * 16, isBalanced: true},
		{name: "257 Blocks (4,112 B - 1 chunk + 1 block, Unbalanced)", byteLength: 257 * 16, isBalanced: false},
		{name: "257 Blocks (4,112 B - 1 chunk + 1 block, Balanced)", byteLength: 257 * 16, isBalanced: true},
		{name: "4,095 Bytes (1 byte shy of 1 chunk)", byteLength: 4095, isBalanced: false},
		{name: "4,097 Bytes (1 chunk + 1 byte)", byteLength: 4097, isBalanced: false},
		{name: "8,191 Bytes (2 chunks - 1 byte)", byteLength: 8191, isBalanced: false},
		{name: "8,192 Bytes (2 chunks, Balanced)", byteLength: 8192, isBalanced: true},
		{name: "8,193 Bytes (2 chunks + 1 byte)", byteLength: 8193, isBalanced: false},
		{name: "10,000 Blocks (160,000 B, Unbalanced)", byteLength: 10000 * 16, isBalanced: false},
		{name: "10,000 Blocks (160,000 B, 20 Balanced Chunks)", byteLength: 10000 * 16, isBalanced: true},
	}

	for _, bc := range cases {
		var raw []byte
		if bc.byteLength == 0 {
			raw = []byte{}
		} else if bc.byteLength%16 == 0 {
			numBlocks := bc.byteLength / 16
			balancedTarget := 0
			if bc.isBalanced {
				if numBlocks >= 256 {
					if numBlocks == 256 || numBlocks == 257 {
						balancedTarget = 1
					} else if numBlocks == 512 {
						balancedTarget = 2
					} else if numBlocks == 10000 {
						balancedTarget = 20
					}
				}
			}
			raw = generateConfiguredPayload(numBlocks, 888, balancedTarget)
		} else {
			raw = make([]byte, bc.byteLength)
			for i := range raw {
				raw[i] = byte(i*37 + 19)
			}
		}

		compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+1024)
		compLen, err := CompressCryptoCore(raw, compBuf)
		if err != nil {
			t.Fatalf("[%s] CompressCryptoCore failed: %v", bc.name, err)
		}
		compressed := compBuf[:compLen]

		decomp := make([]byte, len(raw))
		decLen, err := DecompressCryptoCore(compressed, decomp)
		if err != nil {
			t.Fatalf("[%s] DecompressCryptoCore failed: %v", bc.name, err)
		}

		if decLen != len(raw) {
			t.Fatalf("[%s] Decompress length mismatch: got %d, want %d", bc.name, decLen, len(raw))
		}
		if !bytes.Equal(raw, decomp) {
			t.Fatalf("[%s] LOSSLESS RECONSTRUCTION FAILED: Decompressed bytes differ!", bc.name)
		}

		t.Logf("[%s] Raw: %6d B -> Comp: %6d B -> Decomp: %6d B | Lossless: PASS",
			bc.name, len(raw), compLen, decLen)
	}
}

// TestChallenger_AdversarialRobustness tests error handling, buffer boundary checks,
// and corrupted frame detection.
func TestChallenger_AdversarialRobustness(t *testing.T) {
	// 1. Buffer too small for compress
	raw := make([]byte, 100)
	dstSmall := make([]byte, 10)
	_, err := CompressCryptoCore(raw, dstSmall)
	if err != ErrBufferTooSmall {
		t.Fatalf("Expected ErrBufferTooSmall, got %v", err)
	}

	// 2. Buffer too small for decompress
	validCompressed, err := CompressCryptoCoreAlloc(raw)
	if err != nil {
		t.Fatalf("CompressCryptoCoreAlloc failed: %v", err)
	}
	dstSmallDec := make([]byte, 50) // raw is 100
	_, err = DecompressCryptoCore(validCompressed, dstSmallDec)
	if err != ErrBufferTooSmall {
		t.Fatalf("Expected ErrBufferTooSmall for small dec buffer, got %v", err)
	}

	// 3. Truncated header (< 16 bytes)
	for trLen := range CryptoCoreHeaderSize {
		_, err = DecompressCryptoCore(validCompressed[:trLen], dstSmallDec)
		if err != ErrCorruptFrame {
			t.Fatalf("[trLen=%d] Expected ErrCorruptFrame, got %v", trLen, err)
		}
	}

	// 4. Corrupted Magic
	corruptMagic := make([]byte, len(validCompressed))
	copy(corruptMagic, validCompressed)
	corruptMagic[0] ^= 0xFF
	_, err = DecompressCryptoCore(corruptMagic, raw)
	if err != ErrInvalidMagic {
		t.Fatalf("Expected ErrInvalidMagic, got %v", err)
	}

	// 5. Unsupported mode flag
	corruptFlags := make([]byte, len(validCompressed))
	copy(corruptFlags, validCompressed)
	corruptFlags[12] = 0x05 // Flag 5
	_, err = DecompressCryptoCore(corruptFlags, raw)
	if err != ErrUnsupportedCryptoMode {
		t.Fatalf("Expected ErrUnsupportedCryptoMode, got %v", err)
	}

	// 6. Truncated compressed frame body
	rawLarge := generateConfiguredPayload(512, 123, 2) // 2 balanced chunks
	compLarge, err := CompressCryptoCoreAlloc(rawLarge)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	// Chop off 50 bytes from the end
	truncatedBody := compLarge[:len(compLarge)-50]
	dstLarge := make([]byte, len(rawLarge))
	_, err = DecompressCryptoCore(truncatedBody, dstLarge)
	if err != ErrCorruptFrame {
		t.Fatalf("Expected ErrCorruptFrame on truncated payload, got %v", err)
	}

	// 7. Adversarial header inconsistency: numChunks * 4096 > rawSize
	malformed := make([]byte, 4097)
	binary.LittleEndian.PutUint32(malformed[0:4], PRZM_MAGIC_CRYPTO_CORE)
	binary.LittleEndian.PutUint32(malformed[4:8], 100)   // rawSize = 100
	binary.LittleEndian.PutUint32(malformed[8:12], 4097) // compSize = 4097
	binary.LittleEndian.PutUint16(malformed[12:14], 1)   // flags = 1
	binary.LittleEndian.PutUint16(malformed[14:16], 1)   // numChunks = 1 (1 * 4096 > 100)
	malformed[16] = 0x01                                 // balanced chunk 0
	dstAdv := make([]byte, 4096)
	_, err = DecompressCryptoCore(malformed, dstAdv)
	if err != ErrCorruptFrame {
		t.Fatalf("Expected ErrCorruptFrame on numChunks*4096 > rawSize, got %v", err)
	}

	t.Logf("Adversarial robustness checks: ALL 7 ATTACK SCENARIOS SAFELY DEFENDED (0 panics, 0 memory corruption)")
}

// TestChallenger_CrossCodec_Go_C performs bidirectional cross-validation between Go and C.
// It verifies that:
// 1. Go compressed frames are cleanly decompressed by C.
// 2. C compressed frames are cleanly decompressed by Go.
// 3. Go and C compressors produce bit-exact identical compressed bitstreams.
func TestChallenger_CrossCodec_Go_C(t *testing.T) {
	// Look for compiled C test executable
	cExePath := filepath.Join("..", "..", "test_m3_cross_codec.exe")
	if _, err := os.Stat(cExePath); err != nil {
		cmd := exec.Command("gcc", "-O3", "-Iinclude", "src/aes_distinguisher.c", "tests/test_m3_cross_codec.c", "-o", "test_m3_cross_codec.exe")
		cmd.Dir = filepath.Join("..", "..")
		if errBuild := cmd.Run(); errBuild != nil {
			t.Skipf("C test executable %s not found and build failed: %v", cExePath, errBuild)
			return
		}
	}

	tempDir := t.TempDir()
	rawFile := filepath.Join(tempDir, "payload.raw")
	goCompFile := filepath.Join(tempDir, "payload_go.comp")
	cCompFile := filepath.Join(tempDir, "payload_c.comp")
	cDecompFromGo := filepath.Join(tempDir, "payload_c_from_go.raw")

	type testPayloadSpec struct {
		name       string
		byteLength int
		isBalanced bool
	}

	specs := []testPayloadSpec{
		{name: "0_bytes_empty", byteLength: 0, isBalanced: false},
		{name: "15_bytes_sub_block", byteLength: 15, isBalanced: false},
		{name: "16_bytes_one_block", byteLength: 16, isBalanced: false},
		{name: "255_blocks_4080B", byteLength: 255 * 16, isBalanced: false},
		{name: "256_blocks_unbalanced", byteLength: 256 * 16, isBalanced: false},
		{name: "256_blocks_balanced", byteLength: 256 * 16, isBalanced: true},
		{name: "257_blocks_balanced", byteLength: 257 * 16, isBalanced: true},
		{name: "10000_blocks_balanced", byteLength: 10000 * 16, isBalanced: true},
		{name: "1MiB_PoC_payload", byteLength: 65536 * 16, isBalanced: true},
	}

	for _, spec := range specs {
		var raw []byte
		if spec.byteLength == 0 {
			raw = []byte{}
		} else if spec.byteLength%16 == 0 {
			numBlocks := spec.byteLength / 16
			balancedCount := 0
			if spec.isBalanced {
				if numBlocks == 256 || numBlocks == 257 {
					balancedCount = 1
				} else if numBlocks == 10000 {
					balancedCount = 20
				} else if numBlocks == 65536 {
					balancedCount = 130
				}
			}
			raw = generateConfiguredPayload(numBlocks, 999, balancedCount)
		} else {
			raw = make([]byte, spec.byteLength)
			for i := range raw {
				raw[i] = byte(i*41 + 17)
			}
		}

		// Write raw payload to file
		if err := os.WriteFile(rawFile, raw, 0644); err != nil {
			t.Fatalf("[%s] Failed to write rawFile: %v", spec.name, err)
		}

		// Step 1: Go compresses payload
		compGo, err := CompressCryptoCoreAlloc(raw)
		if err != nil {
			t.Fatalf("[%s] Go CompressCryptoCoreAlloc failed: %v", spec.name, err)
		}
		if err := os.WriteFile(goCompFile, compGo, 0644); err != nil {
			t.Fatalf("[%s] Failed to write goCompFile: %v", spec.name, err)
		}

		// Step 2: C compresses payload
		cmdCComp := exec.Command(cExePath, "compress", rawFile, cCompFile)
		if out, err := cmdCComp.CombinedOutput(); err != nil {
			t.Fatalf("[%s] C compress failed: %v\nOutput: %s", spec.name, err, string(out))
		}
		compC, err := os.ReadFile(cCompFile)
		if err != nil {
			t.Fatalf("[%s] Failed to read cCompFile: %v", spec.name, err)
		}

		// Step 3: Compare compressed frames byte-for-byte
		if !bytes.Equal(compGo, compC) {
			t.Fatalf("[%s] COMPRESSED FRAME BITSTREAM DIVERGENCE! Go len=%d, C len=%d",
				spec.name, len(compGo), len(compC))
		}

		// Step 4: C decompresses Go-compressed frame
		cmdCDecomp := exec.Command(cExePath, "decompress", goCompFile, cDecompFromGo)
		if out, err := cmdCDecomp.CombinedOutput(); err != nil {
			t.Fatalf("[%s] C decompress from Go failed: %v\nOutput: %s", spec.name, err, string(out))
		}
		decompCFromGo, err := os.ReadFile(cDecompFromGo)
		if err != nil {
			t.Fatalf("[%s] Failed to read cDecompFromGo: %v", spec.name, err)
		}
		if !bytes.Equal(raw, decompCFromGo) {
			t.Fatalf("[%s] CROSS-CODEC FAILURE: C decompress of Go frame did not match raw! (len=%d vs %d)",
				spec.name, len(decompCFromGo), len(raw))
		}

		// Step 5: Go decompresses C-compressed frame
		decompGoFromC := make([]byte, len(raw))
		n, err := DecompressCryptoCore(compC, decompGoFromC)
		if err != nil {
			t.Fatalf("[%s] Go decompress from C failed: %v", spec.name, err)
		}
		if n != len(raw) || !bytes.Equal(raw, decompGoFromC) {
			t.Fatalf("[%s] CROSS-CODEC FAILURE: Go decompress of C frame did not match raw! (len=%d vs %d)",
				spec.name, n, len(raw))
		}

		t.Logf("[Cross-Codec %-24s] Raw: %7d B | Comp: %7d B (Go==C: EXACT) | Bidirectional Decomp: 100%% LOSSLESS",
			spec.name, len(raw), len(compGo))
	}

	t.Logf("--> All cross-codec Go <-> C compatibility tests PASSED with 100%% bit-exact parity.")
}
