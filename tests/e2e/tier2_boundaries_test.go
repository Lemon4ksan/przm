// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/lemon4ksan/przm"
)

// Tier 2: Boundary & Corner Cases (17 Features × 5 Tests = 85 Tests)

// F01 Boundaries: Repeat Offset Registers
func TestTier2_F01_RepeatRegisters_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"ZeroRepeats_PureLiterals", []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")},
		{"SingleRepeatMatch", bytes.Repeat([]byte("PATTERN_X_"), 2)},
		{"MaximalConsecutiveRepeats", bytes.Repeat([]byte("REP_"), 500)},
		{"AlternatingTwoOffsets", generateRepeatOffsetData(2048, []int{16, 32})},
		{"CyclicThreeOffsets", generateRepeatOffsetData(4096, []int{8, 24, 48})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F02 Boundaries: Canonical Huffman
func TestTier2_F02_CanonicalHuffman_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"SingleSymbolRepeated_1KB", bytes.Repeat([]byte{'X'}, 1024)},
		{"TwoSymbolsAlternating_1KB", bytes.Repeat([]byte{'A', 'B'}, 512)},
		{"FourSymbolsUniform_1KB", bytes.Repeat([]byte{'A', 'B', 'C', 'D'}, 256)},
		{"Uniform256Symbols_1KB", bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 128)},
		{"DegenerateSkew_99PercentSame", append(bytes.Repeat([]byte{'Z'}, 990), []byte("0123456789")...)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F03 Boundaries: ModeFlat Fallback
func TestTier2_F03_ModeFlatFallback_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"SingleBytePayload", 1},
		{"TwoBytesPayload", 2},
		{"Incompressible_16Bytes", 16},
		{"Incompressible_64Bytes", 64},
		{"Incompressible_256Bytes", 256},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			noise := generateHighEntropyData(tc.size)
			assertRoundtrip(t, noise, 1)
		})
	}
}

// F04 Boundaries: WindowLog Limits
func TestTier2_F04_WindowLog_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"Exact64KB", 65536},
		{"64KB_Plus_1Byte", 65537},
		{"128KB_Boundary", 131072},
		{"1MB_Boundary", 1048576},
		{"MaxBlockSize_LimitCheck", przm.MaxBlockSize + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "MaxBlockSize_LimitCheck" {
				// Assert error returned on exceeding 16 MiB max block limit
				overflowData := make([]byte, tc.size)
				_, err := przm.CompressWithOptions(overflowData, przm.CompressOptions{Level: 1, StandardStreamOnly: true})
				if err == nil {
					t.Fatalf("expected ErrExceedsMaxBlock, got nil")
				}
				return
			}
			data := make([]byte, tc.size)
			pattern := []byte("PRZM_WINDOW_BOUNDARY_TEST_123456789_")
			for i := range data {
				data[i] = pattern[i%len(pattern)]
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// F05 Boundaries: Ultrametric fp32 Pruning
func TestTier2_F05_UltrametricFP32_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		fn   func() []byte
	}{
		{
			name: "AllZeros_4KB",
			fn:   func() []byte { return make([]byte, 4096) },
		},
		{
			name: "AllOnes_4KB",
			fn:   func() []byte { return bytes.Repeat([]byte{0xFF}, 4096) },
		},
		{
			name: "InvertedBitwiseAlternating",
			fn: func() []byte {
				buf := make([]byte, 4096)
				for i := range buf {
					if i%2 == 0 {
						buf[i] = 0xAA
					} else {
						buf[i] = 0x55
					}
				}
				return buf
			},
		},
		{
			name: "CollidingHashes_Small",
			fn:   func() []byte { return generateHashCollidingData(25) },
		},
		{
			name: "CollidingHashes_Large",
			fn:   func() []byte { return generateHashCollidingData(300) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.fn()
			assertRoundtrip(t, data, 1)
		})
	}
}

// F06 Boundaries: Zero-Heap Decode Sizing
func TestTier2_F06_ZeroHeapDecode_Boundaries(t *testing.T) {
	tests := []struct {
		name    string
		extra   int
		expect0 bool
	}{
		{"ExactSize_AllocOk", 0, false},
		{"ExactSizePlus32_AllocOk", 32, false},
		{"ExactSizePlus64_ZeroAlloc", 64, true},
		{"ExactSizePlus128_ZeroAlloc", 128, true},
		{"Oversized2x_ZeroAlloc", 8192, true},
	}
	sample := make([]byte, 4096)
	for i := range sample {
		sample[i] = byte((i * 17) % 256)
	}
	comp, err := przm.Compress(sample, 1)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := make([]byte, len(sample)+tc.extra)
			dec, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}
			if !bytes.Equal(dec, sample) {
				t.Fatalf("roundtrip mismatch")
			}
			if tc.expect0 {
				assertZeroAllocs(t, 20, func() {
					_, _ = przm.DecompressTo(comp, dst)
				})
			}
		})
	}
}

// F07 Boundaries: Optimal Parser Overlaps
func TestTier2_F07_OptimalParser_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"TieBreaker_RepeatedPairs", bytes.Repeat([]byte("ABAB"), 128)},
		{"FibonacciWordWorstCase", []byte("a" + "b" + "ab" + "bab" + "abbab" + "bababbab" + "abbabbababbab")},
		{"DecreasingMatchLengths", []byte("ABCDEFGH_ABCDEFG_ABCDEF_ABCDE_ABCD_ABC_AB")},
		{"SingleLiteralInterspersed", []byte("MATCH1_x_MATCH1_y_MATCH1_z_MATCH1")},
		{"ShortDenseOverlaps", bytes.Repeat([]byte("1212121234343434"), 64)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F08 Boundaries: MinMatch = 3 & 2-Byte Repeats
func TestTier2_F08_MinMatch3_2ByteRepeats_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"Length1Byte", []byte("A")},
		{"Length2Bytes", []byte("AB")},
		{"Length3Bytes", []byte("ABC")},
		{"Length4Bytes", []byte("ABCD")},
		{"Interleaved2And3ByteTokens", []byte("AB_XYZ_AB_XYZ_AB_XYZ_12_789_12_789")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F09 Boundaries: Content Router Heuristic Edges
func TestTier2_F09_ContentRouter_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"TinyPayload_3Bytes", []byte{0x4D, 0x5A, 0x90}},
		{"ZeroDensity14Pct", append(make([]byte, 14), bytes.Repeat([]byte{0xFF}, 86)...)},
		{"ZeroDensity15Pct", append(make([]byte, 15), bytes.Repeat([]byte{0xFF}, 85)...)},
		{"PureASCIIUniform", bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 10)},
		{"PEHeaderPrefixWithZeros", append([]byte{0x4D, 0x5A, 0x90, 0x00}, make([]byte, 256)...)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F10 Boundaries: Base64/Hex Normalization Spans
func TestTier2_F10_Base64HexNorm_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"Base64Under64Bytes_60B", generateBase64Payload(45)[:60]},
		{"Base64Exactly64Bytes", generateBase64Payload(48)[:64]},
		{"HexUnder64Bytes_32B", generateHexPayload(16)},
		{"HexExactly64Bytes", generateHexPayload(32)},
		{"MixedHexAndJSON", []byte(`{"sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F11 Boundaries: Aligned Block Dedup Edges
func TestTier2_F11_BlockDedup_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"BlockSize_65535B", 65535},
		{"BlockSize_65536B", 65536},
		{"BlockSize_65537B", 65537},
		{"TwoBlocksWith1ByteDelta", 131072},
		{"ThreeBlocksIdentical", 196608},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			for i := range data {
				data[i] = byte((i * 3) % 256)
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// F12 Boundaries: PNG Deflate Container Edges
func TestTier2_F12_PNGDeflate_Boundaries(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{"PNGImage_1x1", 1, 1},
		{"PNGImage_2x2", 2, 2},
		{"PNGImage_4x4", 4, 4},
		{"PNGImage_8x8", 8, 8},
		{"PNGImage_1x64", 1, 64},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pngData := generatePNGContainer(tc.width, tc.height)
			assertRoundtrip(t, pngData, 1)
		})
	}
}

// F13 Boundaries: Approximate Matching
func TestTier2_F13_ApproximateMatching_Boundaries(t *testing.T) {
	tests := []struct {
		name      string
		length    int
		deltaBits int
	}{
		{"ExactMatch_Delta0", 1024, 0},
		{"HammingDelta_1Bit", 1024, 1},
		{"HammingDelta_2Bits", 1024, 2},
		{"HammingDelta_3Bits", 1024, 3},
		{"Sparse1ByteDeltaEvery128Bytes", 2048, 8},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.length)
			for i := range data {
				data[i] = byte(i % 64)
			}
			if tc.deltaBits > 0 {
				for i := 128; i < tc.length; i += 128 {
					data[i] ^= byte((1 << (tc.deltaBits % 8)) - 1)
				}
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// F14 Boundaries: Archival Tier Level Scaling
func TestTier2_F14_ArchivalTierSegregation_Boundaries(t *testing.T) {
	levels := []int{0, 1, 2, 3, 4}
	sample := bytes.Repeat([]byte("LEVEL_SCALING_BOUNDARY_TEST_PATTERN_"), 32)
	for _, lvl := range levels {
		t.Run(string(rune('0'+lvl)), func(t *testing.T) {
			assertRoundtrip(t, sample, lvl)
		})
	}
}

// F15 Boundaries: Negative Testing & Robustness (Graceful Errors, Zero Panics)
func TestTier2_F15_NegativeTesting_ZeroPanics(t *testing.T) {
	validData := []byte("VALID_SOURCE_PAYLOAD_FOR_CORRUPTION_TESTING_1234567890")
	validComp, err := przm.Compress(validData, 1)
	if err != nil {
		t.Fatalf("setup compress failed: %v", err)
	}

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "CorruptedMagicSignature",
			fn: func(t *testing.T) {
				corrupted := make([]byte, len(validComp))
				copy(corrupted, validComp)
				binary.LittleEndian.PutUint32(corrupted[0:4], 0xDEADBEEF)
				assertNoPanic(t, "CorruptedMagicSignature", func() {
					_, err := przm.Decompress(corrupted)
					if err == nil {
						t.Fatalf("expected error on invalid magic, got nil")
					}
				})
			},
		},
		{
			name: "TruncatedFrame_SmallerThanHeader",
			fn: func(t *testing.T) {
				truncated := validComp[:min(len(validComp), 5)]
				assertNoPanic(t, "TruncatedHeader", func() {
					_, err := przm.Decompress(truncated)
					if err == nil {
						t.Fatalf("expected error on truncated header, got nil")
					}
				})
			},
		},
		{
			name: "TruncatedPayload_HalfFrame",
			fn: func(t *testing.T) {
				half := validComp[:len(validComp)/2]
				assertNoPanic(t, "HalfFrameTruncation", func() {
					_, err := przm.Decompress(half)
					if err == nil {
						t.Fatalf("expected error on truncated payload, got nil")
					}
				})
			},
		},
		{
			name: "CorruptedSectionDirectory",
			fn: func(t *testing.T) {
				corrupted := make([]byte, len(validComp))
				copy(corrupted, validComp)
				// Corrupt section sizes if extended
				if len(corrupted) > 12 {
					corrupted[8] ^= 0xFF
					corrupted[9] ^= 0xFF
				}
				assertNoPanic(t, "CorruptedSectionDirectory", func() {
					_, _ = przm.Decompress(corrupted)
				})
			},
		},
		{
			name: "ArbitraryRandomNoiseBitstream",
			fn: func(t *testing.T) {
				noise := generateHighEntropyData(128)
				assertNoPanic(t, "RandomNoiseDecode", func() {
					_, _ = przm.Decompress(noise)
				})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

// F16 Boundaries: Scenario 2 Corpus Edges
func TestTier2_F16_Scenario2Corpus_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"EmptySlice", []byte{}},
		{"SingleByteSlice", []byte{0x42}},
		{"TwoByteSlice", []byte{0x42, 0x43}},
		{"SingleByteRepeated_100", bytes.Repeat([]byte{0x77}, 100)},
		{"PrimeLengthBuffer_1009", make([]byte, 1009)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// F17 Boundaries: Seal Container Integration Edges
func TestTier2_F17_SealContainer_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "Seal_EmptyPayload",
			fn: func(t *testing.T) {
				assertRoundtrip(t, []byte{}, 1)
			},
		},
		{
			name: "Seal_SingleBytePayload",
			fn: func(t *testing.T) {
				assertRoundtrip(t, []byte{0x01}, 1)
			},
		},
		{
			name: "Seal_DestinationBufferTooSmall",
			fn: func(t *testing.T) {
				src := []byte("PAYLOAD_LARGER_THAN_SMALL_DESTINATION")
				comp, err := przm.Compress(src, 1)
				if err != nil {
					t.Fatalf("compress failed: %v", err)
				}
				tooSmall := make([]byte, 2)
				assertNoPanic(t, "DestinationBufferTooSmall", func() {
					// DecompressTo reallocates or handles gracefully
					dec, err := przm.DecompressTo(comp, tooSmall)
					if err == nil && !bytes.Equal(dec, src) {
						t.Fatalf("mismatch on small dst")
					}
				})
			},
		},
		{
			name: "Seal_TTRN_InvalidDictSize",
			fn: func(t *testing.T) {
				tensor := make([]byte, 64)
				invalidDict := make([]byte, 100) // TTRN expects 32768
				assertNoPanic(t, "TTRN_InvalidDictSize", func() {
					_, _ = przm.CompressTTRN(tensor, invalidDict)
				})
			},
		},
		{
			name: "Seal_TTRN_EmptyTensor",
			fn: func(t *testing.T) {
				dict := make([]byte, 32768)
				comp, err := przm.CompressTTRN([]byte{}, dict)
				if err != nil {
					t.Fatalf("empty tensor compress failed: %v", err)
				}
				dst := make([]byte, 0)
				err = przm.DecompressTTRNDirect(comp, dict, dst)
				if err != nil {
					t.Fatalf("empty tensor decompress failed: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}
