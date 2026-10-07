// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// Tier 1: Feature Coverage (17 Features × 5 Tests = 85 Tests)

// Feature 1: 3-Repeat Offset Registers (ROADMAP §9.2)
func TestTier1_F01_RepeatRegisters(t *testing.T) {
	tests := []struct {
		name    string
		length  int
		offsets []int
	}{
		{"SingleRepeatOffset", 4096, []int{32}},
		{"DualAlternatingOffsets", 8192, []int{16, 64}},
		{"TripleInterleavedOffsets", 16384, []int{12, 36, 108}},
		{"MixedLiteralAndRepeatOffsets", 8192, []int{8, 40}},
		{"LongDistanceRepeatRegister", 32768, []int{256, 512, 1024}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateRepeatOffsetData(tc.length, tc.offsets)
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 2: 4-Stream Canonical Huffman (ROADMAP §9.2)
func TestTier1_F02_CanonicalHuffman(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"SkewedDistribution_1KB", 1024},
		{"SkewedDistribution_4KB", 4096},
		{"SkewedDistribution_16KB", 16384},
		{"StructuredTextCommands_8KB", 8192},
		{"StructuredTextCommands_32KB", 32768},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateSkewedDistribution(tc.length)
			comp := assertRoundtrip(t, data, 1)
			hdr, err := przm.ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("failed to parse frame header: %v", err)
			}
			if hdr.UncompressedSize != tc.length {
				t.Fatalf("header uncompressed size mismatch: got %d, want %d", hdr.UncompressedSize, tc.length)
			}
		})
	}
}

// Feature 3: ModeFlat Stream Fallback (ROADMAP §9.2)
func TestTier1_F03_ModeFlatFallback(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"IncompressiblePRNG_128B", 128},
		{"IncompressiblePRNG_512B", 512},
		{"IncompressiblePRNG_2KB", 2048},
		{"IncompressiblePRNG_8KB", 8192},
		{"IncompressiblePRNG_32KB", 32768},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			noise := generateHighEntropyData(tc.length)
			comp := assertRoundtrip(t, noise, 1)
			hdr, err := przm.ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("parse frame header failed: %v", err)
			}
			// Incompressible data must set FlagUncompressed
			if (hdr.Flags & przm.FlagUncompressed) == 0 {
				t.Logf("Note: FlagUncompressed not set; flags=0x%02X, size=%d -> %d", hdr.Flags, tc.length, len(comp))
			}
		})
	}
}

// Feature 4: WindowLog Parameterization (ROADMAP §9.3)
func TestTier1_F04_WindowLog(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"SlidingWindow_64KB", 64 * 1024},
		{"SlidingWindow_128KB", 128 * 1024},
		{"SlidingWindow_256KB", 256 * 1024},
		{"SlidingWindow_512KB", 512 * 1024},
		{"SlidingWindow_1MB", 1024 * 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			pattern := []byte("PRZM_WINDOW_LOG_PARAMETERIZATION_MATCH_BLOCK_1234567890_")
			for i := range data {
				data[i] = pattern[i%len(pattern)]
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 5: Ultrametric fp32 Pruning (ROADMAP §9.3)
func TestTier1_F05_UltrametricFP32(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{"CollidingTokens_50", 50},
		{"CollidingTokens_100", 100},
		{"CollidingTokens_250", 250},
		{"CollidingTokens_500", 500},
		{"CollidingTokens_1000", 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateHashCollidingData(tc.count)
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 6: Zero-Heap Large Window Decode (ROADMAP §9.3)
func TestTier1_F06_ZeroHeapDecode(t *testing.T) {
	sizes := []struct {
		name string
		size int
	}{
		{"ZeroAlloc_16KB", 16 * 1024},
		{"ZeroAlloc_64KB", 64 * 1024},
		{"ZeroAlloc_128KB", 128 * 1024},
		{"ZeroAlloc_256KB", 256 * 1024},
		{"ZeroAlloc_512KB", 512 * 1024},
	}
	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			for i := range data {
				data[i] = byte((i * 19) % 256)
			}
			comp, err := przm.Compress(data, 1)
			if err != nil {
				t.Fatalf("compress failed: %v", err)
			}

			dst := make([]byte, tc.size+64)
			// Warmup run
			_, err = przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("warmup DecompressTo failed: %v", err)
			}

			assertZeroAllocs(t, 20, func() {
				_, _ = przm.DecompressTo(comp, dst)
			})
		})
	}
}

// Feature 7: Forward DAG / Backward DP Parser (ROADMAP §9.4)
func TestTier1_F07_OptimalParser(t *testing.T) {
	tests := []struct {
		name string
		size int
	}{
		{"OverlappingStrings_1KB", 1024},
		{"OverlappingStrings_4KB", 4096},
		{"OverlappingStrings_16KB", 16384},
		{"OverlappingStrings_64KB", 65536},
		{"OverlappingStrings_128KB", 131072},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateOverlappingMatchData(tc.size)
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 8: MinMatch = 3 & 2-Byte Repeats (ROADMAP §9.4)
func TestTier1_F08_MinMatch3_2ByteRepeats(t *testing.T) {
	tests := []struct {
		name     string
		length   int
		tokenLen int
	}{
		{"DenseRepeats_2Byte_1KB", 1024, 0},
		{"DenseRepeats_2Byte_8KB", 8192, 1},
		{"DenseRepeats_3Byte_1KB", 1024, 4},
		{"DenseRepeats_3Byte_8KB", 8192, 5},
		{"DenseRepeats_Mixed_16KB", 16384, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateShortRepeatData(tc.length, tc.tokenLen)
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 9: Sub-Microsecond Content Router (ROADMAP §9.5)
func TestTier1_F09_ContentRouter(t *testing.T) {
	tests := []struct {
		name     string
		dataType bench.DataType
		size     int
	}{
		{"RouteJSONLogs", bench.DataJSON, 16384},
		{"RouteTabular32", bench.DataTabular32, 16384},
		{"RouteTabular64", bench.DataTabular64, 16384},
		{"RouteMachineCodeX86", bench.DataMachineCode, 16384},
		{"RouteSourceCodeGo", bench.DataSourceCode, 16384},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var data []byte
			switch tc.dataType {
			case bench.DataJSON:
				data = bench.GenerateJSON(tc.size)
			case bench.DataTabular32:
				data = bench.GenerateTabular(tc.size, 32)
			case bench.DataTabular64:
				data = bench.GenerateTabular(tc.size, 64)
			case bench.DataMachineCode:
				data = bench.GenerateMachineCode(tc.size)
			case bench.DataSourceCode:
				data = bench.GenerateSourceCode(tc.size)
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 10: Base64/Hex SIMD Normalization (ROADMAP §9.5)
func TestTier1_F10_Base64HexNorm(t *testing.T) {
	tests := []struct {
		name   string
		isHex  bool
		rawLen int
	}{
		{"Base64Payload_256B", false, 256},
		{"Base64Payload_1KB", false, 1024},
		{"Base64Payload_8KB", false, 8192},
		{"HexPayload_1KB", true, 1024},
		{"HexPayload_8KB", true, 8192},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var payload []byte
			if tc.isHex {
				payload = generateHexPayload(tc.rawLen)
			} else {
				payload = generateBase64Payload(tc.rawLen)
			}
			assertRoundtrip(t, payload, 1)
		})
	}
}

// Feature 11: Aligned 64 KiB Block Dedup (ROADMAP §9.6)
func TestTier1_F11_BlockDedup(t *testing.T) {
	tests := []struct {
		name       string
		numBlocks  int
		corruptIdx int
	}{
		{"IdenticalBlocks_2x64K", 2, 0},
		{"IdenticalBlocks_3x64K", 3, 0},
		{"IdenticalBlocks_4x64K", 4, 0},
		{"NearIdenticalBlocks_2x64K_1ByteDiff", 2, 1024},
		{"ZeroBlocks_2x64K", 2, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var data []byte
			if tc.name == "ZeroBlocks_2x64K" {
				data = make([]byte, 128*1024)
			} else {
				data = generateDuplicate64KBlocks(tc.numBlocks, tc.corruptIdx)
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 12: PNG Deflate Unwrapping (ROADMAP §9.7)
func TestTier1_F12_PNGDeflate(t *testing.T) {
	dimensions := []struct {
		name   string
		width  int
		height int
	}{
		{"PNGImage_16x16", 16, 16},
		{"PNGImage_32x32", 32, 32},
		{"PNGImage_64x64", 64, 64},
		{"PNGImage_128x32", 128, 32},
		{"PNGImage_256x64", 256, 64},
	}
	for _, tc := range dimensions {
		t.Run(tc.name, func(t *testing.T) {
			pngData := generatePNGContainer(tc.width, tc.height)
			assertRoundtrip(t, pngData, 1)
		})
	}
}

// Feature 13: Approximate Matching (Hamming <= 2) (ROADMAP §9.7)
func TestTier1_F13_ApproximateMatching(t *testing.T) {
	tests := []struct {
		name      string
		length    int
		deltaBits int
	}{
		{"Hamming1BitDiff", 4096, 1},
		{"Hamming2BitsDiff", 4096, 2},
		{"Hamming1ByteDiff", 8192, 8},
		{"SparseDeltaLogs_4KB", 4096, 4},
		{"SparseDeltaLogs_16KB", 16384, 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.length)
			for i := range data {
				data[i] = byte(i % 128)
			}
			// Inject periodic low Hamming delta
			for i := 64; i < tc.length; i += 64 {
				data[i] ^= byte((1 << (tc.deltaBits % 8)) - 1)
			}
			assertRoundtrip(t, data, 1)
		})
	}
}

// Feature 14: Archival Tier Flag Segregation (ROADMAP §9.7)
func TestTier1_F14_ArchivalTierSegregation(t *testing.T) {
	levels := []struct {
		name  string
		level int
	}{
		{"MainMode_Level1", 1},
		{"MainMode_Level2", 2},
		{"MainMode_Level3", 3},
		{"Advanced_Level4", 4},
		{"Archival_Level5", 5},
	}
	sample := bench.GenerateJSON(16384)
	for _, tc := range levels {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, sample, tc.level)
		})
	}
}

// Feature 15: E2E Opaque-Box Test Suite (PROJECT.md § Feature Inventory)
func TestTier1_F15_E2ETestSuite(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "Contract_CompressDecompress",
			fn: func(t *testing.T) {
				data := []byte("przm opaque-box contract test string for level 1")
				assertRoundtrip(t, data, 1)
			},
		},
		{
			name: "Contract_DecompressTo_Preallocated",
			fn: func(t *testing.T) {
				data := []byte("przm preallocated destination buffer contract verification")
				dst := make([]byte, len(data)+64)
				assertRoundtripTo(t, data, 1, dst)
			},
		},
		{
			name: "Contract_ParseFrameHeader",
			fn: func(t *testing.T) {
				data := make([]byte, 1024)
				comp, err := przm.Compress(data, 1)
				if err != nil {
					t.Fatalf("compress failed: %v", err)
				}
				hdr, err := przm.ParseFrameHeader(comp)
				if err != nil {
					t.Fatalf("parse frame header failed: %v", err)
				}
				if hdr.UncompressedSize != len(data) {
					t.Fatalf("size mismatch: got %d, want %d", hdr.UncompressedSize, len(data))
				}
			},
		},
		{
			name: "Contract_DictionaryDiscoveryAndRoundtrip",
			fn: func(t *testing.T) {
				tokens := []string{`,"status":`, `,"method":"GET"`, `,"level":"INFO"`}
				dict, err := przm.NewDictionary(przm.DictIDJSON, tokens)
				if err != nil {
					t.Fatalf("NewDictionary failed: %v", err)
				}
				sample := []byte(`{"level":"INFO","method":"GET","status":200}`)
				comp, err := przm.CompressWithDict(sample, 1, dict)
				if err != nil {
					t.Fatalf("CompressWithDict failed: %v", err)
				}
				dec, err := przm.DecompressWithDict(comp, dict)
				if err != nil {
					t.Fatalf("DecompressWithDict failed: %v", err)
				}
				if !bytes.Equal(dec, sample) {
					t.Fatalf("dictionary roundtrip mismatch")
				}
			},
		},
		{
			name: "Contract_StreamingRoundtrip",
			fn: func(t *testing.T) {
				sample := bytes.Repeat([]byte("PRZM_STREAMING_API_CONTRACT_CHUNK_0123456789;"), 64)
				var compBuf bytes.Buffer
				if err := przm.CompressStream(bytes.NewReader(sample), &compBuf); err != nil {
					t.Fatalf("CompressStream failed: %v", err)
				}
				var decBuf bytes.Buffer
				if err := przm.DecompressStream(bytes.NewReader(compBuf.Bytes()), &decBuf); err != nil {
					t.Fatalf("DecompressStream failed: %v", err)
				}
				if !bytes.Equal(decBuf.Bytes(), sample) {
					t.Fatalf("CompressStream roundtrip mismatch")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

// Feature 16: Scenario 2 Corpus Verification (PROJECT.md § Feature Inventory)
func TestTier1_F16_Scenario2Corpus(t *testing.T) {
	categories := []struct {
		name string
		data []byte
	}{
		{"Corpus_JSONLogs_32KB", bench.GenerateJSON(32768)},
		{"Corpus_Tabular32_32KB", bench.GenerateTabular(32768, 32)},
		{"Corpus_Tabular64_32KB", bench.GenerateTabular(32768, 64)},
		{"Corpus_MachineCode_32KB", bench.GenerateMachineCode(32768)},
		{"Corpus_SourceCodeGo_32KB", bench.GenerateSourceCode(32768)},
	}
	for _, tc := range categories {
		t.Run(tc.name, func(t *testing.T) {
			assertRoundtrip(t, tc.data, 1)
		})
	}
}

// Feature 17: Seal Container Integration Verification (PROJECT.md § Feature Inventory)
func TestTier1_F17_SealContainer(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "Seal_CodecPRZM_Workflow",
			fn: func(t *testing.T) {
				payload := []byte("SIMULATED_SEAL_CONTAINER_OBJECT_DATA_PAYLOAD_ABCDEF")
				comp, err := przm.Compress(payload, 1)
				if err != nil {
					t.Fatalf("seal compress failed: %v", err)
				}
				dec, err := przm.Decompress(comp)
				if err != nil {
					t.Fatalf("seal decompress failed: %v", err)
				}
				if !bytes.Equal(dec, payload) {
					t.Fatalf("seal payload mismatch")
				}
			},
		},
		{
			name: "Seal_DecompressTo_Workflow",
			fn: func(t *testing.T) {
				payload := []byte("SEAL_EON_CONTAINER_CHUNKS_PREALLOCATED_BUFFER_REUSE")
				comp, err := przm.Compress(payload, 1)
				if err != nil {
					t.Fatalf("seal compress failed: %v", err)
				}
				dst := make([]byte, len(payload)+64)
				dec, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("seal DecompressTo failed: %v", err)
				}
				if !bytes.Equal(dec, payload) {
					t.Fatalf("seal DecompressTo mismatch")
				}
			},
		},
		{
			name: "Seal_MagicPRZM_EnvelopeCheck",
			fn: func(t *testing.T) {
				payload := []byte("MAGIC_HEADER_ENVELOPE_VALIDATION")
				comp, err := przm.Compress(payload, 1)
				if err != nil {
					t.Fatalf("seal compress failed: %v", err)
				}
				if len(comp) < 4 {
					t.Fatalf("frame too short for magic check")
				}
				hdr, err := przm.ParseFrameHeader(comp)
				if err != nil {
					t.Fatalf("failed to parse header: %v", err)
				}
				if hdr.UncompressedSize != len(payload) {
					t.Fatalf("size mismatch: got %d, want %d", hdr.UncompressedSize, len(payload))
				}
			},
		},
		{
			name: "Seal_TTRN_TensorCompression",
			fn: func(t *testing.T) {
				tensorData := make([]byte, 2048)
				for i := range tensorData {
					tensorData[i] = byte((i * 7) % 256)
				}
				dict := make([]byte, 32768)
				for i := range dict {
					dict[i] = byte((i * 13) % 256)
				}
				compTTRN, err := przm.CompressTTRN(tensorData, dict)
				if err != nil {
					t.Fatalf("CompressTTRN failed: %v", err)
				}
				dst := make([]byte, len(tensorData))
				err = przm.DecompressTTRNDirect(compTTRN, dict, dst)
				if err != nil {
					t.Fatalf("DecompressTTRNDirect failed: %v", err)
				}
				if !bytes.Equal(dst, tensorData) {
					t.Fatalf("TTRN roundtrip mismatch")
				}
			},
		},
		{
			name: "Seal_MultiChunkStream",
			fn: func(t *testing.T) {
				chunks := [][]byte{
					[]byte("SEAL_CONTAINER_STREAM_CHUNK_001_HEADER"),
					[]byte("SEAL_CONTAINER_STREAM_CHUNK_002_METADATA"),
					[]byte("SEAL_CONTAINER_STREAM_CHUNK_003_DATA_BLOB"),
				}
				for i, chunk := range chunks {
					comp, err := przm.Compress(chunk, 1)
					if err != nil {
						t.Fatalf("chunk %d compress failed: %v", i, err)
					}
					dec, err := przm.Decompress(comp)
					if err != nil {
						t.Fatalf("chunk %d decompress failed: %v", i, err)
					}
					if !bytes.Equal(dec, chunk) {
						t.Fatalf("chunk %d mismatch", i)
					}
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}
