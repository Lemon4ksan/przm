// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"bytes"
	"math"
	"testing"
)

// TestCascadedDeficitAggregator_TheoreticalBounds verifies that the aggregated deficit
// computed via the information-theoretic chain rule:
//
//	\Delta \mathcal{H}_{\text{agg}} = \sum_{j=1}^5 \Delta \mathcal{H}_j
//
// falls strictly within the [0.155%, 0.302%] range when evaluated on representative reduced-round payloads.
func TestCascadedDeficitAggregator_TheoreticalBounds(t *testing.T) {
	// 1. Verify static theoretical specification bounds
	sumMin := 0.0
	sumMax := 0.0
	for _, spec := range DeficitVectorSpecs {
		sumMin += spec.MinDeficit
		sumMax += spec.MaxDeficit
	}

	if math.Abs(sumMin-TheoreticalMinDeficitPercent) > 1e-6 {
		t.Fatalf("Theoretical min bounds mismatch: got %f, want %f", sumMin, TheoreticalMinDeficitPercent)
	}
	if math.Abs(sumMax-TheoreticalMaxDeficitPercent) > 1e-6 {
		t.Fatalf("Theoretical max bounds mismatch: got %f, want %f", sumMax, TheoreticalMaxDeficitPercent)
	}

	// 2. Evaluate empirical deficit on representative reduced-round payload (4096 blocks = 64 KiB)
	payload4K := GenerateRepresentativePayload(4096, 42)
	report4K := EvaluateCascadedDeficit(payload4K)

	t.Logf("[Representative Payload 4096 Blocks] Aggregated Deficit: %.4f%%", report4K.AggregatedDeficitPercent)
	for i, v := range report4K.VectorDeficits {
		t.Logf("  Vector %d (%s): %.4f%% (Theoretical: %.3f%% - %.3f%%)",
			i+1, DeficitVectorSpecs[i].Name, v, DeficitVectorSpecs[i].MinDeficit, DeficitVectorSpecs[i].MaxDeficit)
	}
	t.Logf("  Projected Savings per 1 MiB: %d bytes (Theoretical: %d - %d bytes)",
		report4K.TheoreticalSavingsBytes1MiB, TheoreticalMinBytesSaved1MiB, TheoreticalMaxBytesSaved1MiB)

	if !report4K.WithinTheoreticalBounds {
		t.Fatalf("Aggregated deficit %.4f%% is not within theoretical bounds [%.3f%%, %.3f%%]",
			report4K.AggregatedDeficitPercent, TheoreticalMinDeficitPercent, TheoreticalMaxDeficitPercent)
	}

	if report4K.TheoreticalSavingsBytes1MiB < TheoreticalMinBytesSaved1MiB ||
		report4K.TheoreticalSavingsBytes1MiB > TheoreticalMaxBytesSaved1MiB {
		t.Fatalf("Theoretical 1 MiB savings %d bytes outside expected [%d, %d] bytes",
			report4K.TheoreticalSavingsBytes1MiB, TheoreticalMinBytesSaved1MiB, TheoreticalMaxBytesSaved1MiB)
	}

	// 3. Test synthetic boundary vector profiles
	minReport := AggregateCascadedDeficit([5]float64{0.035, 0.040, 0.025, 0.030, 0.025})
	if !minReport.WithinTheoreticalBounds {
		t.Fatalf("Min boundary deficit failed bounds check: %.4f%%", minReport.AggregatedDeficitPercent)
	}
	if minReport.TheoreticalSavingsBytes1MiB < TheoreticalMinBytesSaved1MiB-10 ||
		minReport.TheoreticalSavingsBytes1MiB > TheoreticalMinBytesSaved1MiB+10 {
		t.Fatalf("Min boundary 1 MiB savings %d unexpected", minReport.TheoreticalSavingsBytes1MiB)
	}

	maxReport := AggregateCascadedDeficit([5]float64{0.075, 0.082, 0.055, 0.050, 0.040})
	if !maxReport.WithinTheoreticalBounds {
		t.Fatalf("Max boundary deficit failed bounds check: %.4f%%", maxReport.AggregatedDeficitPercent)
	}
	if maxReport.TheoreticalSavingsBytes1MiB < TheoreticalMaxBytesSaved1MiB-10 ||
		maxReport.TheoreticalSavingsBytes1MiB > TheoreticalMaxBytesSaved1MiB+10 {
		t.Fatalf("Max boundary 1 MiB savings %d unexpected", maxReport.TheoreticalSavingsBytes1MiB)
	}
}

// TestInstantaneousDecompression_ZeroAllocation verifies that decompression executes
// with strictly 0 heap memory allocations (0 B/op, 0 allocs/op).
func TestInstantaneousDecompression_ZeroAllocation(t *testing.T) {
	// Prepare 16 KiB representative compressed payload
	blocks := GenerateRepresentativePayload(1024, 123)
	raw := make([]byte, len(blocks)*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}

	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+128)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore failed: %v", err)
	}
	compressed := compBuf[:compLen]

	dst := make([]byte, len(raw))

	// Verify zero allocations during decompression
	allocs := testing.AllocsPerRun(500, func() {
		n, decErr := DecompressCryptoCore(compressed, dst)
		if decErr != nil || n != len(raw) {
			t.Fatalf("DecompressCryptoCore failed: n=%d, err=%v", n, decErr)
		}
	})

	t.Logf("[Zero-Allocation Verification] Decompression allocs/op: %.2f", allocs)
	if allocs != 0.0 {
		t.Fatalf("Instantaneous decompression violated zero-allocation invariant: got %.2f allocs/op, want 0.0", allocs)
	}
}

// TestInstantaneousDecompression_CycleLatency verifies that instantaneous decompression
// achieves cycle latency <= 384 CPU cycles per 64-byte block.
func TestInstantaneousDecompression_CycleLatency(t *testing.T) {
	// Prepare 64 KiB representative compressed payload (1024 64-byte blocks)
	blocks := GenerateRepresentativePayload(4096, 789)
	raw := make([]byte, len(blocks)*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}

	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+128)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore failed: %v", err)
	}
	compressed := compBuf[:compLen]

	dst := make([]byte, len(raw))

	cyclesPerBlock, err := MeasureDecompressionCycleLatency(compressed, dst, 1000)
	if err != nil {
		t.Fatalf("MeasureDecompressionCycleLatency failed: %v", err)
	}

	t.Logf("[Latency Benchmark] Measured Decompression Latency: %.2f CPU cycles per 64-byte block (Budget: <= %d)",
		cyclesPerBlock, MaxDecompressionCyclesPer64B)

	if cyclesPerBlock > float64(MaxDecompressionCyclesPer64B) {
		t.Fatalf("Decompression latency %.2f cycles exceeded microarchitectural budget of %d cycles",
			cyclesPerBlock, MaxDecompressionCyclesPer64B)
	}
}

// TestPoCCompression_LosslessRoundtrip verifies bit-exact roundtrip reconstruction
// and validates reproducible compression savings on representative crypto-core payloads.
func TestPoCCompression_LosslessRoundtrip(t *testing.T) {
	testSizes := []int{256, 512, 1024, 4096, 8192}

	for _, numBlocks := range testSizes {
		blocks := GenerateRepresentativePayload(numBlocks, int64(numBlocks*101))
		raw := make([]byte, numBlocks*16)
		for i, b := range blocks {
			copy(raw[i*16:(i+1)*16], b[:])
		}

		compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+256)
		compLen, err := CompressCryptoCore(raw, compBuf)
		if err != nil {
			t.Fatalf("[size=%d] CompressCryptoCore failed: %v", numBlocks, err)
		}
		compressed := compBuf[:compLen]

		// Decompress into pre-allocated buffer
		decomp := make([]byte, len(raw))
		decLen, err := DecompressCryptoCore(compressed, decomp)
		if err != nil {
			t.Fatalf("[size=%d] DecompressCryptoCore failed: %v", numBlocks, err)
		}

		if decLen != len(raw) {
			t.Fatalf("[size=%d] Length mismatch: got %d, want %d", numBlocks, decLen, len(raw))
		}

		if !bytes.Equal(raw, decomp) {
			t.Fatalf("[size=%d] Lossless roundtrip byte mismatch!", numBlocks)
		}

		bytesSaved := len(raw) - compLen
		savingPct := (float64(bytesSaved) / float64(len(raw))) * 100.0
		t.Logf("[size=%6d bytes (%4d blocks)] Raw: %d, Comp: %d, Saved: %d bytes (%.4f%%)",
			len(raw), numBlocks, len(raw), compLen, bytesSaved, savingPct)
	}
}

// TestPoCCompression_1MiBSavingsBound verifies that a 1 MiB pure crypto-core payload
// achieves reproducible lossless compression savings strictly within the theoretical bounds
// of 1,630 - 3,170 bytes (0.155% - 0.302%).
func TestPoCCompression_1MiBSavingsBound(t *testing.T) {
	const oneMiBBlocks = 65536 // 65536 * 16 = 1,048,576 bytes = 1 MiB
	blocks := GenerateCryptoCorePoCPayload(oneMiBBlocks, 9999)

	raw := make([]byte, len(blocks)*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}

	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+4096)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		t.Fatalf("1 MiB compression failed: %v", err)
	}
	compressed := compBuf[:compLen]

	// Verify lossless roundtrip
	dst := make([]byte, len(raw))
	decLen, err := DecompressCryptoCore(compressed, dst)
	if err != nil {
		t.Fatalf("1 MiB decompression failed: %v", err)
	}
	if decLen != len(raw) || !bytes.Equal(raw, dst) {
		t.Fatalf("1 MiB lossless roundtrip failed!")
	}

	bytesSaved := len(raw) - compLen
	savingPct := (float64(bytesSaved) / float64(len(raw))) * 100.0

	t.Logf("[1 MiB Pure Crypto-Core Result] Raw: %d, Comp: %d, Saved: %d bytes (%.4f%%)",
		len(raw), compLen, bytesSaved, savingPct)
	t.Logf("  Theoretical Target: %d - %d bytes (0.155%% - 0.302%%)",
		TheoreticalMinBytesSaved1MiB, TheoreticalMaxBytesSaved1MiB)

	// Assert that savings strictly fall within the theoretical range [1,630, 3,170] bytes
	if bytesSaved < TheoreticalMinBytesSaved1MiB || bytesSaved > TheoreticalMaxBytesSaved1MiB {
		t.Fatalf("1 MiB compression savings %d bytes (%.4f%%) outside theoretical bounds [%d, %d] bytes",
			bytesSaved, savingPct, TheoreticalMinBytesSaved1MiB, TheoreticalMaxBytesSaved1MiB)
	}

	if savingPct < TheoreticalMinDeficitPercent || savingPct > TheoreticalMaxDeficitPercent {
		t.Fatalf("1 MiB compression percentage %.4f%% outside theoretical bounds [%.3f%%, %.3f%%]",
			savingPct, TheoreticalMinDeficitPercent, TheoreticalMaxDeficitPercent)
	}
}

// TestDecompressCryptoCoreBlock64_Microcode verifies the branchless 64-byte microcode
// executing zero-allocation register reconstruction.
func TestDecompressCryptoCoreBlock64_Microcode(t *testing.T) {
	// Construct 4 AES blocks: B0, B1, B2, B3 such that B0 ^ B1 ^ B2 ^ B3 == 0
	var b0, b1, b2, b3 [16]byte
	for i := 0; i < 16; i++ {
		b0[i] = byte(i * 3)
		b1[i] = byte(i*7 + 1)
		b2[i] = byte(i*11 + 2)
		b3[i] = b0[i] ^ b1[i] ^ b2[i] // Zero-sum invariant
	}

	var expected64 [64]byte
	copy(expected64[0:16], b0[:])
	copy(expected64[16:32], b1[:])
	copy(expected64[32:48], b2[:])
	copy(expected64[48:64], b3[:])

	// Case 1: Balanced mode (reads 48 bytes, reconstructs 64 bytes)
	var in48 [48]byte
	copy(in48[0:16], b0[:])
	copy(in48[16:32], b1[:])
	copy(in48[32:48], b2[:])

	var out64 [64]byte
	inBytes, outBytes := DecompressCryptoCoreBlock64(in48[:], out64[:], true)
	if inBytes != 48 || outBytes != 64 {
		t.Fatalf("DecompressCryptoCoreBlock64 balanced mode returned (%d, %d), want (48, 64)", inBytes, outBytes)
	}
	if !bytes.Equal(out64[:], expected64[:]) {
		t.Fatalf("DecompressCryptoCoreBlock64 balanced reconstruction mismatch!")
	}

	// Case 2: Unbalanced mode (reads 64 bytes, copies 64 bytes)
	var outUnbal [64]byte
	inBytes, outBytes = DecompressCryptoCoreBlock64(expected64[:], outUnbal[:], false)
	if inBytes != 64 || outBytes != 64 {
		t.Fatalf("DecompressCryptoCoreBlock64 unbal mode returned (%d, %d), want (64, 64)", inBytes, outBytes)
	}
	if !bytes.Equal(outUnbal[:], expected64[:]) {
		t.Fatalf("DecompressCryptoCoreBlock64 unbal copy mismatch!")
	}
}

// BenchmarkDecompress measures decompression throughput and verifies 0 allocs/op.
func BenchmarkDecompress(b *testing.B) {
	const numBlocks = 4096 // 64 KiB
	blocks := GenerateRepresentativePayload(numBlocks, 555)
	raw := make([]byte, numBlocks*16)
	for i, blk := range blocks {
		copy(raw[i*16:(i+1)*16], blk[:])
	}

	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+128)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		b.Fatalf("CompressCryptoCore failed: %v", err)
	}
	compressed := compBuf[:compLen]

	dst := make([]byte, len(raw))

	b.SetBytes(int64(len(raw)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := DecompressCryptoCore(compressed, dst)
		if err != nil {
			b.Fatalf("DecompressCryptoCore failed: %v", err)
		}
	}
}
