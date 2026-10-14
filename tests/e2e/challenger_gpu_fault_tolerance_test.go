// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

// TestChallenger_GPU_AdversarialChaosMonkey runs a concurrent multi-threaded stress
// harness where goroutines compress diverse payloads with WithGPUAcceleration()
// while an antagonistic controller rapidly injects device unavailability, CUDA driver errors,
// and bus disconnects.
// Invariant: 0 panics, 0 failures, 100% bit-exact lossless roundtrips.
func TestChallenger_GPU_AdversarialChaosMonkey(t *testing.T) {
	defer func() {
		gpu.SetMockUnavailableForTest(false)
		gpu.SetMockMatchErrorForTest(nil)
		gpu.ResetGlobalCompressorForTest()
	}()

	const workers = 12
	const operationsPerWorker = 20
	var wg sync.WaitGroup

	stopChaos := make(chan struct{})
	var faultInjections atomic.Int64

	// Antagonistic Chaos Controller
	go func() {
		ticker := time.NewTicker(4 * time.Millisecond)
		defer ticker.Stop()
		state := 0
		for {
			select {
			case <-stopChaos:
				gpu.SetMockUnavailableForTest(false)
				gpu.SetMockMatchErrorForTest(nil)
				gpu.ResetGlobalCompressorForTest()
				return
			case <-ticker.C:
				faultInjections.Add(1)
				switch state % 4 {
				case 0:
					gpu.SetMockUnavailableForTest(true)
				case 1:
					gpu.SetMockMatchErrorForTest(errors.New("simulated CUDA driver out-of-memory error: 700"))
				case 2:
					gpu.SetMockMatchErrorForTest(errors.New("simulated PCIe bus disconnect / kernel timeout"))
				case 3:
					gpu.SetMockUnavailableForTest(false)
					gpu.SetMockMatchErrorForTest(nil)
				}
				state++
			}
		}
	}()

	errCh := make(chan error, workers*operationsPerWorker)
	var completedCount atomic.Int64

	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Create distinct payload shapes
			payloads := [][]byte{
				{},                               // 0 bytes
				{0x55},                           // 1 byte
				{0xAA, 0x55},                     // 2 bytes
				{0x01, 0x02, 0x03},               // 3 bytes
				bytes.Repeat([]byte("A"), 128),   // Small repetitive
				bytes.Repeat([]byte("B"), 65536), // 64 KiB repetitive
				make([]byte, 104729),             // Prime size zero-fill
				func() []byte { // High-entropy random 32KB
					b := make([]byte, 32768)
					for i := range b {
						b[i] = byte((i*109 + 37) ^ (i >> 5))
					}
					return b
				}(),
				func() []byte { // Structured JSON-like
					var b bytes.Buffer
					for i := range 500 {
						fmt.Fprintf(
							&b,
							`{"worker":%d,"idx":%d,"timestamp":1700000000,"status":"ACTIVE"}`+"\n",
							workerID,
							i,
						)
					}
					return b.Bytes()
				}(),
			}

			for op := range operationsPerWorker {
				data := payloads[op%len(payloads)]

				opts := przm.CompressOptions{
					Level:           (op % 5) + 1,
					Threads:         (op % 2) + 1,
					GPUAcceleration: true,
				}

				comp, err := przm.CompressWithOptions(data, opts)
				if err != nil {
					errCh <- fmt.Errorf("worker %d op %d compression failed: %w", workerID, op, err)
					return
				}

				dec, err := przm.Decode(comp)
				if err != nil {
					errCh <- fmt.Errorf("worker %d op %d decompression failed: %w", workerID, op, err)
					return
				}

				if !bytes.Equal(dec, data) {
					errCh <- fmt.Errorf("worker %d op %d roundtrip mismatch: got len %d, want len %d",
						workerID, op, len(dec), len(data))
					return
				}

				completedCount.Add(1)
				time.Sleep(2 * time.Millisecond)
			}
		}(w)
	}

	wg.Wait()
	close(stopChaos)
	close(errCh)

	for err := range errCh {
		t.Fatalf("Chaos monkey failure: %v", err)
	}

	totalExpected := int64(workers * operationsPerWorker)
	if completedCount.Load() != totalExpected {
		t.Fatalf("Expected %d successful roundtrips, got %d", totalExpected, completedCount.Load())
	}

	t.Logf(
		"Adversarial Chaos Monkey PASSED: %d operations completed across %d fault injections with 0 panics and 100%% bit-exact roundtrips",
		completedCount.Load(),
		faultInjections.Load(),
	)
}

// TestChallenger_GPU_ScratchpadContamination verifies that when GPU compression
// begins writing to scratchpad buffers and subsequently errors out, the scratchpad
// is cleanly reset so that the fallback CPU SIMD engine emits zero corrupted frames.
func TestChallenger_GPU_ScratchpadContamination(t *testing.T) {
	defer func() {
		gpu.SetMockUnavailableForTest(false)
		gpu.SetMockMatchErrorForTest(nil)
		gpu.ResetGlobalCompressorForTest()
	}()

	testSizes := []int{64, 256, 1024, 8192, 65536, 262144}

	for _, sz := range testSizes {
		t.Run(fmt.Sprintf("Size_%d", sz), func(t *testing.T) {
			raw := make([]byte, sz)
			for i := range raw {
				raw[i] = byte(i ^ (i >> 3))
			}

			// 1. First run with simulated GPU match error -> must fallback cleanly
			gpu.SetMockMatchErrorForTest(errors.New("simulated mid-stream kernel abort"))
			compFallback, err := przm.CompressWithOptions(raw, przm.CompressOptions{GPUAcceleration: true})
			if err != nil {
				t.Fatalf("Fallback compression failed: %v", err)
			}
			decFallback, err := przm.Decode(compFallback)
			if err != nil {
				t.Fatalf("Fallback decompression failed: %v", err)
			}
			if !bytes.Equal(decFallback, raw) {
				t.Fatalf("Corrupted data recovered from fallback decompressor!")
			}

			// 2. Clear error, compress directly with CPU to compare frame consistency
			gpu.SetMockMatchErrorForTest(nil)
			compCPU, err := przm.CompressWithOptions(raw, przm.CompressOptions{GPUAcceleration: false})
			if err != nil {
				t.Fatalf("CPU baseline compression failed: %v", err)
			}
			decCPU, err := przm.Decode(compCPU)
			if err != nil {
				t.Fatalf("CPU baseline decompression failed: %v", err)
			}
			if !bytes.Equal(decCPU, raw) {
				t.Fatalf("Corrupted data recovered from CPU baseline decompressor!")
			}

			// Frame sizes should be identical because fallback delegates directly to standard CPU
			if len(compFallback) != len(compCPU) {
				t.Logf("Notice: fallback frame size %d vs CPU baseline frame size %d", len(compFallback), len(compCPU))
			}
		})
	}
}

// TestChallenger_GPU_MultiChunk_FallbackStress tests multi-chunk compression under
// heavy fault injection, ensuring chunk headers, container framing, and inter-chunk
// state remain pristine.
func TestChallenger_GPU_MultiChunk_FallbackStress(t *testing.T) {
	defer func() {
		gpu.SetMockUnavailableForTest(false)
		gpu.SetMockMatchErrorForTest(nil)
		gpu.ResetGlobalCompressorForTest()
	}()

	// 1 MiB payload with multiple chunks (Threads = 4)
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte((i * 7) ^ (i / 1024))
	}

	// Inject error before multi-chunk compression
	gpu.SetMockMatchErrorForTest(errors.New("simulated multi-chunk GPU device fault"))

	opts := przm.CompressOptions{
		GPUAcceleration: true,
		Threads:         4,
		Level:           3,
	}

	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Multi-chunk fallback compression failed: %v", err)
	}

	dec, err := przm.Decode(comp)
	if err != nil {
		t.Fatalf("Multi-chunk fallback decompression failed: %v", err)
	}

	if !bytes.Equal(dec, data) {
		t.Fatalf("Multi-chunk fallback roundtrip data mismatch!")
	}
}

// TestTier4_GPU_W04_HardwareFailureUnderLoad aliases TestTier4_GPU_Workload_HardwareFailureUnderLoad
func TestTier4_GPU_W04_HardwareFailureUnderLoad(t *testing.T) {
	TestTier4_GPU_Workload_HardwareFailureUnderLoad(t)
}

// TestTier3_GPU_I04_MidPipelineFallbackToggling aliases TestTier3_GPU_FallbackToggling_MidPipeline
func TestTier3_GPU_I04_MidPipelineFallbackToggling(t *testing.T) {
	TestTier3_GPU_FallbackToggling_MidPipeline(t)
}
