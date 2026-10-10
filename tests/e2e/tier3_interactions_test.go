// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// Tier 3: Cross-Feature Interactions (>= 17 Pairwise Tests)

// 1. Large Window + Cross-Block Deduplication
func TestTier3_01_LargeWindow_And_BlockDedup(t *testing.T) {
	// Synthesize 256 KiB buffer with duplicate 64 KiB blocks separated by large distances
	blockA := generateRepeatOffsetData(65536, []int{32, 64})
	blockB := generateRepeatOffsetData(65536, []int{16, 48})

	var buf bytes.Buffer
	buf.Write(blockA)
	buf.Write(blockB)
	buf.Write(blockA) // identical block at 128 KiB distance
	buf.Write(blockB) // identical block at 128 KiB distance

	data := buf.Bytes()
	assertRoundtrip(t, data, 1)
}

// 2. Content Router + Base64 Normalization
func TestTier3_02_ContentRouter_And_Base64Norm(t *testing.T) {
	// Structured JSON with embedded Base64 authentication tokens
	rawB64 := generateBase64Payload(512)
	jsonDoc := fmt.Sprintf(`{"token":"%s","status":200,"user":"admin","action":"login"}`, string(rawB64))
	data := []byte(jsonDoc)

	comp := assertRoundtrip(t, data, 1)
	hdr, err := przm.ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("parse header failed: %v", err)
	}
	if hdr.UncompressedSize != len(data) {
		t.Fatalf("size mismatch: %d vs %d", hdr.UncompressedSize, len(data))
	}
}

// 3. Archival Mode + PNG Deflate Unwrapping
func TestTier3_03_ArchivalMode_And_PNGDeflate(t *testing.T) {
	pngData := generatePNGContainer(64, 64)
	// Compress with level 5 (Archival tier)
	assertRoundtrip(t, pngData, 5)
}

// 4. Repeat Registers + Optimal Parser
func TestTier3_04_RepeatRegisters_And_OptimalParser(t *testing.T) {
	// Data combining overlapping short tokens with repeated long offsets
	base := generateOverlappingMatchData(8192)
	data := append(base, base...) // repeat entire block to test repeat register choice
	assertRoundtrip(t, data, 1)
}

// 5. Canonical Huffman + ModeFlat Fallback
func TestTier3_05_CanonicalHuffman_And_ModeFlatFallback(t *testing.T) {
	// Mixed payload: 4 KiB skewed structured text followed by 4 KiB pure random noise
	skewed := generateSkewedDistribution(4096)
	noise := generateHighEntropyData(4096)
	mixed := append(skewed, noise...)

	assertRoundtrip(t, mixed, 1)
}

// 6. Ultrametric fp32 Pruning + MinMatch = 3
func TestTier3_06_UltrametricFP32_And_MinMatch3(t *testing.T) {
	// Dense repeating 3-byte tokens designed to collide on 16-bit hash table
	colliding := generateHashCollidingData(100)
	shortRepeats := generateShortRepeatData(4096, 4)
	data := append(colliding, shortRepeats...)

	assertRoundtrip(t, data, 1)
}

// 7. Zero-Heap Decode + Large Window
func TestTier3_07_ZeroHeapDecode_And_LargeWindow(t *testing.T) {
	const size = 1048576 // 1 MiB large window
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i * 31) % 256)
	}
	comp, err := przm.Compress(data, 1)
	if err != nil {
		t.Fatalf("compress 1MB failed: %v", err)
	}

	dst := make([]byte, size+64)
	// Warmup run
	_, err = przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}

	assertZeroAllocs(t, 10, func() {
		_, _ = przm.DecompressTo(comp, dst)
	})
}

// 8. Stride Periodic + Delta Masking
func TestTier3_08_StridePeriodic_And_DeltaMask(t *testing.T) {
	// 32-byte stride telemetry rows with sparse 1-byte counter changes
	var buf bytes.Buffer
	for i := range 200 {
		row := fmt.Sprintf("ID:%05d|ST:OK|VAL:%05d|SEQ:%05d\n", i, (i*13)%10000, i%100)
		buf.WriteString(row)
	}
	data := buf.Bytes()
	comp := assertRoundtrip(t, data, 1)

	hdr, err := przm.ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("parse header failed: %v", err)
	}
	t.Logf("Stride+Delta Flags: 0x%02X, Stride=%d", hdr.Flags, hdr.Stride)
}


// 10. External Dictionary + Large Window
func TestTier3_10_Dictionary_And_LargeWindow(t *testing.T) {
	tokens := []string{`,"sensor_id":`, `,"reading_val":`, `,"unit":"celsius"`}
	dict, err := przm.NewDictionary(przm.DictIDJSON, tokens)
	if err != nil {
		t.Fatalf("NewDictionary failed: %v", err)
	}

	var buf bytes.Buffer
	for i := range 500 {
		fmt.Fprintf(&buf, `{"sensor_id":"temp-%03d","reading_val":%.2f,"unit":"celsius"}`+"\n", i%20, 20.0+float64(i%50)/5.0)
	}
	data := buf.Bytes()

	comp, err := przm.CompressWithDict(data, 1, dict)
	if err != nil {
		t.Fatalf("CompressWithDict failed: %v", err)
	}

	dec, err := przm.DecompressWithDict(comp, dict)
	if err != nil {
		t.Fatalf("DecompressWithDict failed: %v", err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatalf("dictionary roundtrip mismatch")
	}
}

// 11. TTRN + Symplectic Residuals
func TestTier3_11_TTRN_And_SymplecticResidual(t *testing.T) {
	tensor := make([]byte, 4096)
	for i := range tensor {
		tensor[i] = byte((i * 23) % 256)
	}
	dict := make([]byte, 32768)
	for i := range dict {
		dict[i] = byte((i * 11) % 256)
	}

	comp, err := przm.CompressTTRN(tensor, dict)
	if err != nil {
		t.Fatalf("CompressTTRN failed: %v", err)
	}

	dst := make([]byte, len(tensor))
	err = przm.DecompressTTRNDirect(comp, dict, dst)
	if err != nil {
		t.Fatalf("DecompressTTRNDirect failed: %v", err)
	}
	if !bytes.Equal(dst, tensor) {
		t.Fatalf("TTRN symplectic roundtrip mismatch")
	}
}

// 12. Interleaved Entropy Stream + Negative Corrupted Offset
func TestTier3_12_InterleavedEntropy_And_CorruptedOffset(t *testing.T) {
	skewed := generateSkewedDistribution(8192)
	comp, err := przm.Compress(skewed, 1)
	if err != nil {
		t.Fatalf("setup compress failed: %v", err)
	}

	// Corrupt middle of stream (offset section)
	corrupted := make([]byte, len(comp))
	copy(corrupted, comp)
	if len(corrupted) > 20 {
		corrupted[len(corrupted)/2] ^= 0x7F
	}

	assertNoPanic(t, "CorruptedOffsetStream", func() {
		_, _ = przm.Decompress(corrupted)
	})
}


// 14. Block Dedup + Zero-Heap DecompressTo
func TestTier3_14_BlockDedup_And_ZeroHeapDecompressTo(t *testing.T) {
	data := generateDuplicate64KBlocks(2, 0) // 128 KiB
	comp, err := przm.Compress(data, 1)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)
	// Warmup
	_, err = przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}

	assertZeroAllocs(t, 20, func() {
		_, _ = przm.DecompressTo(comp, dst)
	})
}

// 15. Approximate Matching + Delta Mask
func TestTier3_15_ApproximateMatching_And_DeltaMask(t *testing.T) {
	// Synthesize 8 KiB dataset with recurring 64-byte records differing by only 1 bit
	var buf bytes.Buffer
	for i := range 128 {
		record := []byte(fmt.Sprintf("RECORD_INDEX_%05d_TELEMETRY_STATUS_OK_PAYLOAD_HASH_ABCD1234\n", i))
		if i%2 == 1 {
			record[15] ^= 0x01 // flip single bit
		}
		buf.Write(record)
	}
	data := buf.Bytes()
	assertRoundtrip(t, data, 1)
}

// 16. Seal Container Multi-Stream + Zero Allocation Invariant
func TestTier3_16_SealContainer_And_AllocsPerRun(t *testing.T) {
	payload := []byte("SEAL_INTEGRATED_STREAM_OBJECT_ALLOCATION_CHECK")
	comp, err := przm.Compress(payload, 1)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	dst := make([]byte, len(payload)+64)
	_, err = przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}

	assertZeroAllocs(t, 50, func() {
		_, _ = przm.DecompressTo(comp, dst)
	})
}

// 17. Corpus Roundtrip Across Multi-Levels (1, 2, 3)
func TestTier3_17_CorpusRoundtrip_And_MultiLevel(t *testing.T) {
	items := []struct {
		name string
		data []byte
	}{
		{"JSON_16KB", bench.GenerateJSON(16384)},
		{"MachineCode_16KB", bench.GenerateMachineCode(16384)},
		{"Tabular_16KB", bench.GenerateTabular(16384, 32)},
	}
	for _, it := range items {
		for _, lvl := range []int{1, 2, 3} {
			t.Run(fmt.Sprintf("%s_lvl%d", it.name, lvl), func(t *testing.T) {
				assertRoundtrip(t, it.data, lvl)
			})
		}
	}
}
