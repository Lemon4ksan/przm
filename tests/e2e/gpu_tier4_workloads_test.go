// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

// Tier 4: Real-World Workload Tests for PRZM CUDA GPU Acceleration

// TestTier4_GPU_Workload_LargeJSONDataset verifies GPU LZ77 compression and decompression
// on a multi-megabyte structured JSON dataset (2MB - 4MB) in single-frame and multi-chunk configurations.
func TestTier4_GPU_Workload_LargeJSONDataset(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 4 large JSON test")
	}

	// Generate ~2.2 MB structured JSON corpus
	jsonCorpus := generateSyntheticJSON(20000)
	corpusSize := len(jsonCorpus)
	if corpusSize < 1024*1024 {
		t.Fatalf("expected JSON corpus >= 1MB, got %d bytes", corpusSize)
	}

	t.Run("SingleFrame_GPU", func(t *testing.T) {
		opts := przm.CompressOptions{
			GPUAcceleration:    true,
			Level:              3,
			StandardStreamOnly: true,
		}

		comp, err := przm.CompressWithOptions(jsonCorpus, opts)
		if err != nil {
			t.Fatalf("przm.CompressWithOptions failed on %d bytes JSON: %v", corpusSize, err)
		}

		ratio := float64(corpusSize) / float64(len(comp))
		t.Logf("Large JSON (Single Frame): %d -> %d bytes (Ratio: %.2fx)", corpusSize, len(comp), ratio)
		if ratio < 2.0 {
			t.Fatalf("expected compression ratio >= 2.0x on structured JSON, got %.2fx", ratio)
		}

		// Verify decompression via przm.Decompress
		decomp, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("przm.Decompress failed: %v", err)
		}
		if !bytes.Equal(decomp, jsonCorpus) {
			t.Fatalf("lossless roundtrip mismatch: got %d bytes, want %d bytes", len(decomp), corpusSize)
		}

		// Verify decompression via przm.DecompressTo
		dst := make([]byte, corpusSize+128)
		decompTo, err := przm.DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("przm.DecompressTo failed: %v", err)
		}
		if !bytes.Equal(decompTo, jsonCorpus) {
			t.Fatalf("przm.DecompressTo mismatch")
		}
	})

	t.Run("MultiChunk_Streaming_GPU", func(t *testing.T) {
		const chunkSize = 256 * 1024 // 256 KiB chunks
		opts := przm.CompressOptions{
			GPUAcceleration: true,
			Threads:         4,
			ChunkSize:       chunkSize,
			Level:           3,
		}

		comp, err := przm.CompressWithOptions(jsonCorpus, opts)
		if err != nil {
			t.Fatalf("CompressWithOptions multi-chunk failed: %v", err)
		}

		streamHdr, err := przm.ParseStreamHeader(comp)
		if err != nil {
			t.Fatalf("ParseStreamHeader failed: %v", err)
		}
		expectedChunks := (corpusSize + chunkSize - 1) / chunkSize
		if int(streamHdr.ChunkCount) != expectedChunks {
			t.Fatalf("chunk count mismatch: got %d, want %d", streamHdr.ChunkCount, expectedChunks)
		}

		decomp, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("przm.Decompress failed on multi-chunk stream: %v", err)
		}
		if !bytes.Equal(decomp, jsonCorpus) {
			t.Fatalf("multi-chunk lossless roundtrip failed")
		}
	})
}

// TestTier4_GPU_Workload_SourceCodeCorpus tests GPU LZ77 compression and bit-exact recovery
// on a multi-megabyte source code corpus (1MB - 4MB).
func TestTier4_GPU_Workload_SourceCodeCorpus(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 4 source code corpus test")
	}

	// Generate ~2.3 MB synthetic Go/C source code corpus
	srcCorpus := generateSyntheticSourceCode(15000)
	corpusSize := len(srcCorpus)
	if corpusSize < 1024*1024 {
		t.Fatalf("expected source code corpus >= 1MB, got %d bytes", corpusSize)
	}

	opts := przm.CompressOptions{
		GPUAcceleration: true,
		Level:           3,
	}

	comp, err := przm.CompressWithOptions(srcCorpus, opts)
	if err != nil {
		t.Fatalf("przm.CompressWithOptions failed on source code corpus: %v", err)
	}

	ratio := float64(corpusSize) / float64(len(comp))
	t.Logf("Source Code Corpus: %d -> %d bytes (Ratio: %.2fx)", corpusSize, len(comp), ratio)
	if ratio < 2.0 {
		t.Fatalf("expected compression ratio >= 2.0x on source code, got %.2fx", ratio)
	}

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("przm.Decompress failed: %v", err)
	}
	if !bytes.Equal(decomp, srcCorpus) {
		t.Fatalf("lossless roundtrip mismatch: got %d bytes, want %d bytes", len(decomp), corpusSize)
	}

	dst := make([]byte, corpusSize+256)
	decompTo, err := przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("przm.DecompressTo failed: %v", err)
	}
	if !bytes.Equal(decompTo, srcCorpus) {
		t.Fatalf("przm.DecompressTo mismatch on source code corpus")
	}
}

// TestTier4_GPU_Workload_MixedBinaryExecutables tests GPU LZ77 compression on realistic
// mixed x86-64 executable machine code and structured binary data (1MB - 4MB).
func TestTier4_GPU_Workload_MixedBinaryExecutables(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 4 mixed binary test")
	}

	// Generate 2 MiB mixed executable binary
	const binSize = 2 * 1024 * 1024
	binData := generateMixedBinary(binSize)

	opts := przm.CompressOptions{
		GPUAcceleration: true,
		Level:           3,
	}

	comp, err := przm.CompressWithOptions(binData, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions failed on mixed binary: %v", err)
	}

	t.Logf("Mixed Binary Executable: %d -> %d bytes", binSize, len(comp))

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("przm.Decompress failed: %v", err)
	}
	if !bytes.Equal(decomp, binData) {
		t.Fatalf("lossless roundtrip mismatch on mixed binary")
	}

	dst := make([]byte, binSize+64)
	decompTo, err := przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("przm.DecompressTo failed: %v", err)
	}
	if !bytes.Equal(decompTo, binData) {
		t.Fatalf("DecompressTo mismatch on mixed binary")
	}
}

// TestTier4_GPU_Workload_HardwareFailureUnderLoad simulates catastrophic CUDA hardware failure
// while multiple goroutines are actively compressing data under load, confirming zero data corruption,
// zero panics, and seamless transparent CPU SIMD fallback.
func TestTier4_GPU_Workload_HardwareFailureUnderLoad(t *testing.T) {
	defer func() {
		gpu.SetMockUnavailableForTest(false)
		gpu.SetMockMatchErrorForTest(nil)
		gpu.ResetGlobalCompressorForTest()
	}()

	const workers = 8
	const itersPerWorker = 10
	var wg sync.WaitGroup

	var completedOps atomic.Int64
	errCh := make(chan error, workers*itersPerWorker)

	startGate := make(chan struct{})

	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			<-startGate

			// Worker dataset
			data := generateSyntheticJSON(1000 + workerID*500)

			for i := range itersPerWorker {
				opts := przm.CompressOptions{
					GPUAcceleration: true,
					Level:           3,
				}

				comp, err := przm.CompressWithOptions(data, opts)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d compression failed: %w", workerID, i, err)
					return
				}

				decomp, err := przm.Decompress(comp)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d decompression failed: %w", workerID, i, err)
					return
				}

				if !bytes.Equal(decomp, data) {
					errCh <- fmt.Errorf("worker %d iter %d roundtrip data corruption: got %d, want %d",
						workerID, i, len(decomp), len(data))
					return
				}

				completedOps.Add(1)
				time.Sleep(5 * time.Millisecond)
			}
		}(w)
	}

	// Release workers
	close(startGate)

	// Simulate hardware faults while workers are actively compressing
	time.Sleep(15 * time.Millisecond)
	gpu.SetMockMatchErrorForTest(errors.New("simulated PCIe bus disconnect: GPU dropped off bus"))

	time.Sleep(20 * time.Millisecond)
	gpu.SetMockUnavailableForTest(true)

	time.Sleep(20 * time.Millisecond)
	gpu.SetMockMatchErrorForTest(nil)
	gpu.SetMockUnavailableForTest(false)

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("failure during concurrent hardware fault simulation: %v", err)
	}

	totalExpected := int64(workers * itersPerWorker)
	if completedOps.Load() != totalExpected {
		t.Fatalf("expected %d completed ops, got %d", totalExpected, completedOps.Load())
	}
	t.Logf(
		"Hardware failure under load: successfully processed %d operations across fault injections",
		completedOps.Load(),
	)

	// Confirm healthy GPU execution after recovery
	recoveryData := generateSyntheticJSON(1500)
	recoveryComp, err := przm.CompressWithOptions(recoveryData, przm.CompressOptions{
		GPUAcceleration: true,
		Level:           3,
	})
	if err != nil {
		t.Fatalf("post-recovery compression failed: %v", err)
	}
	recoveryDec, err := przm.Decompress(recoveryComp)
	if err != nil || !bytes.Equal(recoveryDec, recoveryData) {
		t.Fatalf("post-recovery roundtrip failed")
	}
}

// TestTier4_GPU_Workload_MultiChunkArchivalStream verifies massive multi-chunk archival streams
// with GPU acceleration, validating container headers, overall stream integrity, and independent
// chunk decompressibility.
func TestTier4_GPU_Workload_MultiChunkArchivalStream(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 4 multi-chunk archival stream test")
	}

	const chunkSize = 512 * 1024 // 512 KiB chunks
	const numChunks = 8
	const payloadSize = numChunks * chunkSize // 4 MiB total payload

	// Generate structured archival test payload
	data := make([]byte, payloadSize)
	for i := range numChunks {
		chunkSlice := data[i*chunkSize : (i+1)*chunkSize]
		pat := fmt.Sprintf(
			"ARCHIVAL_STREAM_CHUNK_HEADER_METRIC_SEQ_%06d_RECORD_TAG_TIMESTAMP_%d_",
			i,
			1760000000+i*3600,
		)
		for j := range chunkSlice {
			chunkSlice[j] = pat[(j*7)%len(pat)] ^ byte(i*17)
		}
	}

	opts := przm.CompressOptions{
		GPUAcceleration: true,
		ArchivalMode:    true,
		Level:           przm.ModeArchival,
		ChunkSize:       chunkSize,
		Threads:         4,
	}

	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions multi-chunk archival failed: %v", err)
	}

	// 1. Verify container magic and stream header
	streamHdr, err := przm.ParseStreamHeader(comp)
	if err != nil {
		t.Fatalf("ParseStreamHeader failed: %v", err)
	}
	if streamHdr.ChunkCount != uint32(numChunks) {
		t.Fatalf("chunk count mismatch: got %d, want %d", streamHdr.ChunkCount, numChunks)
	}
	if streamHdr.TotalUncompressedSize != uint64(payloadSize) {
		t.Fatalf("total uncompressed size mismatch: got %d, want %d", streamHdr.TotalUncompressedSize, payloadSize)
	}

	// 2. Full stream lossless roundtrip via przm.Decompress
	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("przm.Decompress failed on archival stream: %v", err)
	}
	if !bytes.Equal(decomp, data) {
		t.Fatalf("full stream decompression bit-exact mismatch")
	}

	// 3. Full stream lossless roundtrip via przm.DecompressTo
	dst := make([]byte, payloadSize+64)
	decompTo, err := przm.DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("przm.DecompressTo failed on archival stream: %v", err)
	}
	if !bytes.Equal(decompTo, data) {
		t.Fatalf("przm.DecompressTo bit-exact mismatch")
	}

	// 4. Independent chunk verification: extract and decompress each chunk individually
	offset := przm.StreamHeaderSize
	for c := range numChunks {
		if offset+4 > len(comp) {
			t.Fatalf("chunk %d header out of bounds", c)
		}
		cLen := int(binary.LittleEndian.Uint32(comp[offset : offset+4]))
		offset += 4
		if offset+cLen > len(comp) {
			t.Fatalf("chunk %d payload out of bounds: offset %d + len %d > comp len %d", c, offset, cLen, len(comp))
		}
		cData := comp[offset : offset+cLen]
		offset += cLen

		decChunk, err := przm.Decompress(cData)
		if err != nil {
			t.Fatalf("individual chunk %d decompression failed: %v", c, err)
		}

		expectedSlice := data[c*chunkSize : (c+1)*chunkSize]
		if !bytes.Equal(decChunk, expectedSlice) {
			t.Fatalf("chunk %d bit-exact mismatch with original slice", c)
		}
	}

	t.Logf("Multi-Chunk Archival Stream: successfully verified container and %d individual chunks (%d bytes)",
		numChunks, payloadSize)
}
