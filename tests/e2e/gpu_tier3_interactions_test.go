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
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

// Tier 3: Cross-Feature Interactions for PRZM CUDA GPU Acceleration

// TestTier3_GPU_Multithreading_Pairwise tests pairwise interaction of GPU acceleration
// with multi-threaded chunk processing (Threads: 2, 4) against single-threaded baseline.
func TestTier3_GPU_Multithreading_Pairwise(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 3 multithreading test")
	}

	datasets := []struct {
		name string
		data []byte
	}{
		{
			name: "StructuredJSON_1MB",
			data: generateSyntheticJSON(10000), // ~1.1 MB
		},
		{
			name: "TiledPattern_1.5MB",
			data: generateTiledData(48, "GPU_TIER3_MULTITHREADING_TILED_PATTERN_"), // 1.5 MB
		},
	}

	threadCounts := []int{2, 4}
	const chunkSize = 128 * 1024 // 128 KiB chunks

	for _, ds := range datasets {
		for _, th := range threadCounts {
			testName := fmt.Sprintf("%s_Threads%d", ds.name, th)
			t.Run(testName, func(t *testing.T) {
				opts := przm.CompressOptions{
					GPUAcceleration: true,
					Threads:         th,
					ChunkSize:       chunkSize,
					Level:           3,
				}

				comp, err := przm.CompressWithOptions(ds.data, opts)
				if err != nil {
					t.Fatalf("CompressWithOptions(threads=%d) failed: %v", th, err)
				}

				// For multi-chunk streams, verify container magic
				if len(comp) >= 4 && binary.LittleEndian.Uint32(comp[0:4]) == przm.MagicPRZS {
					streamHdr, err := przm.ParseStreamHeader(comp)
					if err != nil {
						t.Fatalf("ParseStreamHeader failed on multi-chunk stream: %v", err)
					}
					expectedChunks := (len(ds.data) + chunkSize - 1) / chunkSize
					if int(streamHdr.ChunkCount) != expectedChunks {
						t.Fatalf("chunk count mismatch: got %d, want %d", streamHdr.ChunkCount, expectedChunks)
					}
					if streamHdr.TotalUncompressedSize != uint64(len(ds.data)) {
						t.Fatalf(
							"uncompressed size mismatch: got %d, want %d",
							streamHdr.TotalUncompressedSize,
							len(ds.data),
						)
					}
				}

				// Verify bit-exact roundtrip via przm.Decode
				decomp, err := przm.Decode(comp)
				if err != nil {
					t.Fatalf("przm.Decode failed: %v", err)
				}
				if !bytes.Equal(decomp, ds.data) {
					t.Fatalf("lossless roundtrip failed: got %d bytes, want %d bytes", len(decomp), len(ds.data))
				}

				// Verify bit-exact roundtrip via przm.DecompressTo
				dst := make([]byte, len(ds.data)+128)
				decompTo, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("przm.DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decompTo, ds.data) {
					t.Fatalf(
						"przm.DecompressTo roundtrip mismatch: got %d bytes, want %d bytes",
						len(decompTo),
						len(ds.data),
					)
				}
			})
		}
	}
}

// TestTier3_GPU_ChunkSizes_Pairwise tests pairwise interaction of GPU acceleration
// across different chunk sizes (64KB, 128KB, 1MB, 2MB) with multi-chunk stream framing.
func TestTier3_GPU_ChunkSizes_Pairwise(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 3 chunk sizes test")
	}

	// 2.5 MiB dataset so that all tested chunk sizes result in multi-chunk framing
	const totalSize = 2560 * 1024
	data := generateTiledData(80, "GPU_TIER3_CHUNK_SIZES_TEST_PATTERN_TILE_PAYLOAD_")
	if len(data) > totalSize {
		data = data[:totalSize]
	}

	chunkCases := []struct {
		name      string
		chunkSize int
	}{
		{"Chunk64KB", 64 * 1024},
		{"Chunk128KB", 128 * 1024},
		{"Chunk1MB", 1024 * 1024},
		{"Chunk2MB", 2048 * 1024},
	}

	for _, cc := range chunkCases {
		t.Run(cc.name, func(t *testing.T) {
			opts := przm.CompressOptions{
				GPUAcceleration: true,
				ChunkSize:       cc.chunkSize,
				Threads:         2,
				Level:           3,
			}

			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("CompressWithOptions(chunkSize=%d) failed: %v", cc.chunkSize, err)
			}

			// Validate stream container header if multi-chunk stream
			if len(comp) >= 4 && binary.LittleEndian.Uint32(comp[0:4]) == przm.MagicPRZS {
				streamHdr, err := przm.ParseStreamHeader(comp)
				if err != nil {
					t.Fatalf("failed to parse PRZS stream header: %v", err)
				}

				expectedChunks := (len(data) + cc.chunkSize - 1) / cc.chunkSize
				if int(streamHdr.ChunkCount) != expectedChunks {
					t.Fatalf("unexpected chunk count: got %d, want %d", streamHdr.ChunkCount, expectedChunks)
				}
				if streamHdr.TotalUncompressedSize != uint64(len(data)) {
					t.Fatalf(
						"total uncompressed size mismatch: got %d, want %d",
						streamHdr.TotalUncompressedSize,
						len(data),
					)
				}
			}

			// Validate bit-exact lossless roundtrip
			decomp, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("przm.Decode failed: %v", err)
			}
			if !bytes.Equal(decomp, data) {
				t.Fatalf("lossless roundtrip failed: got %d bytes, want %d bytes", len(decomp), len(data))
			}

			// Validate przm.DecompressTo
			dst := make([]byte, len(data)+64)
			decompTo, err := przm.DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("przm.DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decompTo, data) {
				t.Fatalf("DecompressTo bit-exact mismatch")
			}
		})
	}
}

// TestTier3_GPU_VaryingLevels_Pairwise tests pairwise interaction of GPU acceleration
// across varying compression levels (1, 3, 7, 10).
func TestTier3_GPU_VaryingLevels_Pairwise(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 3 varying levels test")
	}

	levels := []int{1, 3, 7, 10}

	datasets := map[string][]byte{
		"SyntheticJSON": generateSyntheticJSON(3000), // ~320 KiB
		"MixedBinary":   generateMixedBinary(256 * 1024),
		"TiledPattern":  generateTiledData(8, "GPU_TIER3_LEVELS_PATTERN_"), // 256 KiB
	}

	for dsName, src := range datasets {
		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Level%d", dsName, lvl)
			t.Run(testName, func(t *testing.T) {
				// 1. Direct assertGPUPipelineRoundtrip (tests comp.MatchLZ77 + assertGPUMatchIntegrity)
				compPipe, prof := assertGPUPipelineRoundtrip(t, src, lvl)
				if len(compPipe) == 0 {
					t.Fatalf("empty compressed output from assertGPUPipelineRoundtrip")
				}
				if prof.DeviceName == "" {
					t.Fatalf("expected populated GPUProfile device name")
				}

				// 2. przm.CompressWithOptions with GPUAcceleration
				opts := przm.CompressOptions{
					GPUAcceleration: true,
					Level:           lvl,
				}
				comp, err := przm.CompressWithOptions(src, opts)
				if err != nil {
					t.Fatalf("CompressWithOptions(level=%d) failed: %v", lvl, err)
				}

				decomp, err := przm.Decode(comp)
				if err != nil {
					t.Fatalf("przm.Decode failed: %v", err)
				}

				if !bytes.Equal(decomp, src) {
					t.Fatalf(
						"lossless roundtrip mismatch at level %d: got %d bytes, want %d",
						lvl,
						len(decomp),
						len(src),
					)
				}
			})
		}
	}
}

// TestTier3_GPU_FallbackToggling_MidPipeline tests dynamic toggling of GPU availability
// and match error injection mid-pipeline to confirm seamless on-the-fly CPU fallback.
func TestTier3_GPU_FallbackToggling_MidPipeline(t *testing.T) {
	data := generateSyntheticJSON(2000) // ~210 KiB

	// Always ensure cleanup
	defer func() {
		gpu.SetMockUnavailableForTest(false)
		gpu.SetMockMatchErrorForTest(nil)
		gpu.ResetGlobalCompressorForTest()
	}()

	opts := przm.CompressOptions{
		GPUAcceleration: true,
		Level:           3,
	}

	// Step 1: Initial compression with active GPU configuration
	comp1, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Step 1 failed: %v", err)
	}
	dec1, err := przm.Decode(comp1)
	if err != nil || !bytes.Equal(dec1, data) {
		t.Fatalf("Step 1 roundtrip failed")
	}

	// Step 2: Toggle GPU unavailable mid-pipeline -> must fallback to CPU SIMD
	gpu.SetMockUnavailableForTest(true)
	comp2, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Step 2 CPU fallback failed: %v", err)
	}
	dec2, err := przm.Decode(comp2)
	if err != nil || !bytes.Equal(dec2, data) {
		t.Fatalf("Step 2 roundtrip failed during mock unavailable")
	}

	// Step 3: Restore availability, then simulate CUDA kernel match error mid-pipeline
	gpu.SetMockUnavailableForTest(false)
	gpu.SetMockMatchErrorForTest(errors.New("simulated CUDA driver error: hardware device timeout"))
	comp3, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Step 3 CPU fallback on match error failed: %v", err)
	}
	dec3, err := przm.Decode(comp3)
	if err != nil || !bytes.Equal(dec3, data) {
		t.Fatalf("Step 3 roundtrip failed during mock match error")
	}

	// Step 4: Clear all mock errors, verify clean return to normal pipeline
	gpu.SetMockMatchErrorForTest(nil)
	comp4, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Step 4 recovery failed: %v", err)
	}
	dec4, err := przm.Decode(comp4)
	if err != nil || !bytes.Equal(dec4, data) {
		t.Fatalf("Step 4 roundtrip failed after recovery")
	}

	// Step 5: Rapid back-and-forth toggling loop to verify state cleanliness across calls
	for i := range 10 {
		simulateFailure := (i % 2) == 1
		if simulateFailure {
			gpu.SetMockUnavailableForTest(true)
		} else {
			gpu.SetMockUnavailableForTest(false)
		}

		compIter, err := przm.CompressWithOptions(data, opts)
		if err != nil {
			t.Fatalf("Iteration %d (simulateFailure=%v) failed: %v", i, simulateFailure, err)
		}
		decIter, err := przm.Decode(compIter)
		if err != nil || !bytes.Equal(decIter, data) {
			t.Fatalf("Iteration %d roundtrip mismatch (simulateFailure=%v)", i, simulateFailure)
		}
	}
}

// TestTier3_GPU_PinnedMemoryPool_ConcurrentReuse tests concurrent allocation, usage,
// and recycling of pinned host buffers through PinnedBufferPool and GPUCompressor.
func TestTier3_GPU_PinnedMemoryPool_ConcurrentReuse(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU unavailable for Tier 3 pinned memory pool test")
	}

	t.Run("DirectPinnedPool_ConcurrentRecycle", func(t *testing.T) {
		const bufSize = 64 * 1024
		const initialCap = 4
		pool, err := gpu.NewPinnedBufferPool(bufSize, initialCap)
		if err != nil {
			t.Fatalf("NewPinnedBufferPool failed: %v", err)
		}
		defer pool.Close()

		const workers = 16
		const iterations = 30
		var wg sync.WaitGroup
		errCh := make(chan error, workers*iterations)

		for w := range workers {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				fillByte := byte(workerID + 1)
				for range iterations {
					buf, err := pool.Get()
					if err != nil {
						errCh <- fmt.Errorf("worker %d Get() error: %w", workerID, err)
						return
					}
					if len(buf.Bytes) != bufSize || buf.Ptr == 0 {
						errCh <- fmt.Errorf("worker %d invalid buffer received", workerID)
						pool.Put(buf)
						return
					}

					// Stamp and verify buffer contents
					for idx := range buf.Bytes {
						buf.Bytes[idx] = fillByte
					}
					for idx := range 100 {
						if buf.Bytes[idx] != fillByte {
							errCh <- fmt.Errorf("worker %d buffer corruption detected", workerID)
							pool.Put(buf)
							return
						}
					}

					pool.Put(buf)
				}
			}(w)
		}

		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Fatalf("PinnedBufferPool concurrency failure: %v", err)
		}
	})

	t.Run("PipelinePinnedPool_ConcurrentCompress", func(t *testing.T) {
		const workers = 8
		const iterations = 5
		var wg sync.WaitGroup
		errCh := make(chan error, workers*iterations)

		for w := range workers {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				// Distinct dataset per worker
				data := generateTiledData(4, fmt.Sprintf("WORKER_%02d_PINNED_POOL_TEST_PAYLOAD_", workerID))
				for i := range iterations {
					comp, err := przm.CompressWithOptions(data, przm.CompressOptions{
						GPUAcceleration: true,
						Level:           3,
					})
					if err != nil {
						errCh <- fmt.Errorf("worker %d compress iter %d failed: %w", workerID, i, err)
						return
					}

					decomp, err := przm.Decode(comp)
					if err != nil {
						errCh <- fmt.Errorf("worker %d decompress iter %d failed: %w", workerID, i, err)
						return
					}

					if !bytes.Equal(decomp, data) {
						errCh <- fmt.Errorf("worker %d roundtrip mismatch at iter %d", workerID, i)
						return
					}
				}
			}(w)
		}

		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Fatalf("Concurrent GPU compression pinned memory failure: %v", err)
		}
	})
}
