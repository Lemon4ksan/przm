// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/rand"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// TestDECHHD_Theorem11_ExactLosslessReconstruction tests the core topological identity:
// d1(d0 X) == 0 implies that the 2D curvature C is identically zero,
// and the 2D Prefix-XOR reconstruction recovers X with 100% exact lossless fidelity.
func TestDECHHD_Theorem11_ExactLosslessReconstruction(t *testing.T) {
	M := 64
	W := 64
	grid := make([]byte, M*W)

	// Construct a 2D separable field X[i, j] = A[i] ^ B[j]
	rowBase := make([]byte, M)
	colBase := make([]byte, W)
	for i := range M {
		rowBase[i] = byte(i * 17)
	}
	for j := range W {
		colBase[j] = byte(j * 31)
	}

	for i := range M {
		for j := range W {
			grid[i*W+j] = rowBase[i] ^ colBase[j]
		}
	}

	// 1. Forward DEC Transform
	block := przm.ComputeDECHHD(grid, M, W)
	if block == nil {
		t.Fatalf("ComputeDECHHD returned nil")
	}

	// Theorem 11 assertion: curvature 2-form C must be identically zero!
	if block.NNZ != 0 {
		t.Fatalf("Theorem 11 Violated! Expected NNZ == 0 for separable field, got %d", block.NNZ)
	}

	// 2. Inverse DEC Reconstruction
	reconstructed := make([]byte, M*W)
	if err := przm.ReconstructDECHHD(block, reconstructed); err != nil {
		t.Fatalf("ReconstructDECHHD failed: %v", err)
	}

	if !bytes.Equal(grid, reconstructed) {
		t.Fatalf("Theorem 11 Violated: Reconstructed grid does not match original grid!")
	}
	t.Logf("Theorem 11 Verified: d1(d0 X) == 0 confirmed (NNZ = 0), exact bitwise recovery")

	// 3. Sparse curl test (introduce 3 localized mutations)
	gridSparse := make([]byte, M*W)
	copy(gridSparse, grid)
	gridSparse[10*W+15] ^= 0xAA
	gridSparse[25*W+30] ^= 0x55
	gridSparse[40*W+45] ^= 0x33

	blockSparse := przm.ComputeDECHHD(gridSparse, M, W)
	if blockSparse.NNZ == 0 {
		t.Fatalf("Expected non-zero curl for mutated grid")
	}

	reconstructedSparse := make([]byte, M*W)
	if err := przm.ReconstructDECHHD(blockSparse, reconstructedSparse); err != nil {
		t.Fatalf("ReconstructDECHHD failed for sparse curl: %v", err)
	}

	if !bytes.Equal(gridSparse, reconstructedSparse) {
		t.Fatalf("Reconstructed sparse grid does not match original grid!")
	}
	t.Logf("Theorem 11 Verified: Sparse curl field with NNZ=%d reconstructed losslessly", blockSparse.NNZ)
}

// TestDECHHD_SyntheticCurlFree_Vs_LZ77 compares DEC-HHD against standard LZ77 on 2D structured data.
func TestDECHHD_SyntheticCurlFree_Vs_LZ77(t *testing.T) {
	// 16 KiB 2D dataset: 256 rows x 64 columns
	M := 256
	W := 64
	totalSize := M * W // 16384 bytes
	data := make([]byte, totalSize)

	// Generate 2D separable gradient field with non-linear row variations
	// that prevent trivial 1D run-length or exact long matches in LZ77
	for i := range M {
		rowVal := byte((i*7 + 13) ^ (i >> 3))
		for j := range W {
			colVal := byte((j * 23) ^ 0x9A)
			data[i*W+j] = rowVal ^ colVal
		}
	}

	// 1. Standard LZ77 compression
	lzEncoded, err := przm.Compress(data, 1)
	if err != nil {
		t.Fatalf("Compress LZ77 failed: %v", err)
	}
	lzSize := len(lzEncoded)
	lzRatio := float64(totalSize) / float64(lzSize)

	// 2. DEC-HHD compression (Vector B)
	decCompressed, err := przm.DECHHDEncode(data)
	if err != nil {
		t.Fatalf("DECHHDEncode failed: %v", err)
	}
	decSize := len(decCompressed)
	decRatio := float64(totalSize) / float64(decSize)

	// Verify lossless roundtrip
	recovered, err := przm.DECHHDDecode(decCompressed)
	if err != nil {
		t.Fatalf("DECHHDDecode failed: %v", err)
	}
	if !bytes.Equal(data, recovered) {
		t.Fatalf("DECHHD lossless roundtrip failed on synthetic 2D data!")
	}

	t.Logf("=== 2D CURL-FREE PROJECTION COMPARISON ===")
	t.Logf("Raw Data Size:        %d bytes", totalSize)
	t.Logf("Standard LZ77:        %d bytes (Ratio: %.2fx)", lzSize, lzRatio)
	t.Logf("DEC-HHD (Vector B):   %d bytes (Ratio: %.2fx - CHAMPION)", decSize, decRatio)

	if decRatio < 15.0 {
		t.Fatalf("Expected DEC-HHD ratio >= 15.0x on curl-free field, got %.2fx", decRatio)
	}
	if decRatio <= lzRatio*2.0 {
		t.Fatalf("Expected DEC-HHD to beat LZ77 by at least 2x, got LZ=%.2fx vs DEC=%.2fx", lzRatio, decRatio)
	}
}

// TestDECHHD_LosslessRoundtrip_Corpus verifies exact 100% losslessness across all 14 corpus variations.
func TestDECHHD_LosslessRoundtrip_Corpus(t *testing.T) {
	sizes := []int{16 * 1024, 64 * 1024}
	corpus := bench.GenerateCorpus(sizes)

	for _, item := range corpus {
		t.Run(item.Name, func(t *testing.T) {
			compressed, err := przm.DECHHDEncode(item.Data)
			if err != nil {
				t.Fatalf("DECHHDEncode failed: %v", err)
			}

			decompressed, err := przm.DECHHDDecode(compressed)
			if err != nil {
				t.Fatalf("DECHHDDecode failed: %v", err)
			}

			if !bytes.Equal(item.Data, decompressed) {
				t.Fatalf("Corpus %s mismatch! Orig len=%d, decomp len=%d", item.Name, len(item.Data), len(decompressed))
			}
		})
	}
}

// TestDECHHD_RandomNoiseBypass verifies that high-entropy random noise triggers the fail-safe bypass.
func TestDECHHD_RandomNoiseBypass(t *testing.T) {
	noise := make([]byte, 16384)
	if _, err := rand.Read(noise); err != nil {
		t.Fatalf("crypto/rand failed: %v", err)
	}

	compressed, err := przm.DECHHDEncode(noise)
	if err != nil {
		t.Fatalf("DECHHDEncode failed: %v", err)
	}

	// Overhead must be minimal (< 32 bytes)
	overhead := len(compressed) - len(noise)
	if overhead > 32 {
		t.Fatalf("Random noise bloated by %d bytes (limit 32)", overhead)
	}

	decompressed, err := przm.DECHHDDecode(compressed)
	if err != nil {
		t.Fatalf("DECHHDDecode failed: %v", err)
	}

	if !bytes.Equal(noise, decompressed) {
		t.Fatalf("Random noise roundtrip mismatch!")
	}
	t.Logf("Random Noise Overhead: %d bytes (len %d -> %d)", overhead, len(noise), len(compressed))
}

// BenchmarkDECHHD_Throughput measures decode throughput of DEC-HHD.
func BenchmarkDECHHD_Throughput(t *testing.B) {
	M := 1024
	W := 64
	totalSize := M * W // 64 KiB
	data := make([]byte, totalSize)

	for i := range M {
		rowVal := byte(i * 11)
		for j := range W {
			colVal := byte(j * 19)
			data[i*W+j] = rowVal ^ colVal
		}
	}

	compressed, err := przm.DECHHDEncode(data)
	if err != nil {
		t.Fatalf("DECHHDEncode failed: %v", err)
	}

	t.Run("Decode_CurlFree_64KB", func(b *testing.B) {
		b.SetBytes(int64(totalSize))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			out, err := przm.DECHHDDecode(compressed)
			if err != nil || len(out) != totalSize {
				b.Fatalf("Decode failed: %v", err)
			}
		}
	})
}
