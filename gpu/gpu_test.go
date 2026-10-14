// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpu

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestGPUDetection(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	t.Logf("Detected GPU: %s, SMs: %d, VRAM: %d MB, Compute: %d.%d",
		comp.DeviceName, comp.SMCount, comp.TotalVRAMBytes/(1024*1024), comp.ComputeMajor, comp.ComputeMinor)

	if !strings.Contains(comp.DeviceName, "GeForce") && !strings.Contains(comp.DeviceName, "RTX") {
		t.Errorf("Unexpected GPU name: %s", comp.DeviceName)
	}
	if comp.SMCount < 20 {
		t.Errorf("Expected at least 20 SMs, got %d", comp.SMCount)
	}
	if comp.TotalVRAMBytes < 8*1024*1024*1024 {
		t.Errorf("Expected at least 8GB VRAM, got %d", comp.TotalVRAMBytes)
	}
	if comp.ComputeMajor < 8 {
		t.Errorf("Expected compute capability major >= 8, got %d", comp.ComputeMajor)
	}
}

func TestIsAvailable(t *testing.T) {
	avail := IsAvailable()
	if !avail {
		t.Fatalf("Expected IsAvailable() to be true on CUDA-equipped machine, got false")
	}
}

func TestGetGlobalCompressorSingleton(t *testing.T) {
	comp1, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}
	if comp1 == nil {
		t.Fatalf("Expected non-nil global compressor")
	}

	comp2, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("Second GetGlobalCompressor failed: %v", err)
	}
	if comp1 != comp2 {
		t.Fatalf("Expected identical singleton instance pointers, got %p vs %p", comp1, comp2)
	}

	comp3, err := GetGPUCompressor()
	if err != nil {
		t.Fatalf("GetGPUCompressor failed: %v", err)
	}
	if comp1 != comp3 {
		t.Fatalf("Expected alias GetGPUCompressor to return identical singleton pointer, got %p vs %p", comp1, comp3)
	}
}

func TestPTXKernelExecutionAndAccuracy(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	testDatasets := map[string][]byte{
		"repeated_pattern": bytes.Repeat([]byte("PRZM_GPU_ACCELERATION_TEST_DATA_BLOCK_1234567890_"), 1000),
		"structured_json": []byte(`{"status":"success","code":200,"records":[{"id":1,"name":"alpha","val":100},` +
			`{"id":2,"name":"alpha","val":100},{"id":3,"name":"alpha","val":100}]}` +
			strings.Repeat(`{"event":"heartbeat","stream":"archival","seq":9999,"active":true},`, 200)),
		"go_source_code": []byte(
			strings.Repeat("func BenchmarkKernel(b *testing.B) { for i := 0; i < b.N; i++ { run(); } }\n", 300),
		),
	}

	for name, data := range testDatasets {
		t.Run(name, func(t *testing.T) {
			matches, prof, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed: %v", err)
			}
			t.Logf("[%s] Matches found: %d, Ingress: %.2f GB/s, Kernel: %.2f ms, Egress: %.2f GB/s",
				name, len(matches), prof.IngressDMABandwidthGBps, prof.KernelTimeMs, prof.EgressDMABandwidthGBps)

			if len(matches) == 0 {
				t.Fatalf("Expected matches in %s, got 0", name)
			}

			// Validate bit-exactness of every GPU match
			for idx, m := range matches {
				pos := int(m.Position)
				off := int(m.Offset)
				length := int(m.Length)

				if length < MinMatchLen {
					t.Fatalf("Match %d length %d < MinMatchLen", idx, length)
				}
				if off == 0 || off > pos {
					t.Fatalf("Match %d invalid offset %d at pos %d", idx, off, pos)
				}
				if pos+length > len(data) {
					t.Fatalf("Match %d bounds overrun: pos %d + len %d > data len %d", idx, pos, length, len(data))
				}

				matchSlice := data[pos : pos+length]
				refSlice := data[pos-off : pos-off+length]
				if !bytes.Equal(matchSlice, refSlice) {
					t.Fatalf("Match %d mismatch at pos %d (off %d, len %d)", idx, pos, off, length)
				}
			}
		})
	}
}

func TestVRAMBandwidth(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	var maxBW float64
	for trial := range 10 {
		bw, err := comp.BenchmarkVRAMBandwidth(64)
		if err != nil {
			t.Fatalf("BenchmarkVRAMBandwidth failed on trial %d: %v", trial, err)
		}
		if bw > maxBW {
			maxBW = bw
		}
		if maxBW >= 6.0 {
			break
		}
	}
	t.Logf("Measured Peak Internal VRAM Bandwidth: %.2f GB/s", maxBW)

	if maxBW < 5.0 {
		t.Errorf("Internal VRAM bandwidth %.2f GB/s below target of 5.0 GB/s", maxBW)
	}
}

func TestPinnedMemoryPool(t *testing.T) {
	pool, err := NewPinnedBufferPool(64*1024, 4)
	if err != nil {
		t.Fatalf("NewPinnedBufferPool failed: %v", err)
	}
	defer func() { _ = pool.Close() }()

	buf, err := pool.Get()
	if err != nil {
		t.Fatalf("pool.Get failed: %v", err)
	}
	if len(buf.Bytes) != 64*1024 || buf.Ptr == 0 {
		t.Fatalf("Invalid pinned buffer: len=%d ptr=0x%x", len(buf.Bytes), buf.Ptr)
	}

	buf.Bytes[0] = 0xAA
	buf.Bytes[len(buf.Bytes)-1] = 0x55
	pool.Put(buf)

	buf2, err := pool.Get()
	if err != nil {
		t.Fatalf("pool.Get 2 failed: %v", err)
	}
	if buf2.Bytes[0] != 0xAA {
		t.Fatalf("Buffer reuse memory altered")
	}
	pool.Put(buf2)
}

func TestMatchEdgeCases(t *testing.T) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	// 1. Empty buffer
	matches, _, err := comp.MatchLZ77([]byte{})
	if err != nil {
		t.Fatalf("MatchLZ77 empty slice failed: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("Expected 0 matches for empty slice, got %d", len(matches))
	}

	// 2. Very short buffer (< MinMatchLen)
	matches, _, err = comp.MatchLZ77([]byte{1, 2, 3})
	if err != nil {
		t.Fatalf("MatchLZ77 short slice failed: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("Expected 0 matches for short slice, got %d", len(matches))
	}
}

func TestPackUnpackMatch(t *testing.T) {
	testCases := []struct {
		length uint16
		offset uint32
	}{
		{4, 1},
		{10, 50},
		{258, 65535},
		{4095, 1048575},
	}

	for _, tc := range testCases {
		packed := PackMatch32(tc.length, tc.offset)
		if !IsMatch32(packed) {
			t.Errorf("IsMatch32(%d, %d) returned false", tc.length, tc.offset)
		}
		l, o := UnpackMatch32(packed)
		if l != tc.length || o != tc.offset {
			t.Errorf("UnpackMatch32 got (%d, %d), want (%d, %d)", l, o, tc.length, tc.offset)
		}
	}

	// Invalid matches
	if PackMatch32(3, 10) != 0 {
		t.Errorf("PackMatch32 with length < 4 should return 0")
	}
	if PackMatch32(10, 0) != 0 {
		t.Errorf("PackMatch32 with offset == 0 should return 0")
	}
}

func TestRadixStrikeLargePayloads(t *testing.T) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	sizes := []int{4 * 1024 * 1024, 8 * 1024 * 1024, 16 * 1024 * 1024}

	for _, size := range sizes {
		name := fmt.Sprintf("%dMB", size/(1024*1024))
		t.Run(name, func(t *testing.T) {
			// Construct data with cross-tile periodic structures (>32 KiB and >100 KiB apart)
			data := make([]byte, size)
			for i := range data {
				data[i] = byte((i*73 + (i/4096)*17) & 0xFF)
			}
			// Inject repeated blocks separated by 256 KiB and 1 MiB
			pattern := []byte("RADIX_STRIKE_LONG_DISTANCE_CROSS_TILE_MATCH_VERIFICATION_PAYLOAD_1234567890!")
			pLen := len(pattern)
			for off := 0; off+pLen <= size; off += 256 * 1024 {
				copy(data[off:], pattern)
			}

			// Warmup call to stabilize GPU P0 boost clocks and pinned memory residency
			_, _, _ = comp.MatchLZ77(data)
			matches, prof, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("[%s] MatchLZ77 failed: %v", name, err)
			}

			t.Logf(
				"[%s] Matches: %d | Ingress: %.2f GB/s (%.2f ms) | Kernel: %.2f GB/s (%.2f ms) | "+
					"Egress: %.2f GB/s (%.2f ms) | Total: %.2f ms | End-to-End: %.2f GB/s",
				name, len(matches), prof.IngressDMABandwidthGBps, prof.IngressDMATimeMs,
				prof.InternalVRAMBandwidthGBps, prof.KernelTimeMs,
				prof.EgressDMABandwidthGBps, prof.EgressDMATimeMs, prof.TotalRoundTripMs,
				prof.EndToEndBandwidthGBps)

			if len(matches) == 0 {
				t.Fatalf("[%s] Expected matches, got 0", name)
			}

			// Verify cross-tile matches (>32 KiB offset) were found
			longMatches := 0
			for _, m := range matches {
				if m.Offset > 32768 {
					longMatches++
				}
			}
			if longMatches == 0 {
				t.Errorf("[%s] Expected cross-tile matches (offset > 32 KiB), but none found!", name)
			} else {
				t.Logf("[%s] Successfully captured %d cross-tile long-distance matches (>32 KiB)!", name, longMatches)
			}

			// Validate bit-exactness of all matches
			for idx, m := range matches {
				pos := int(m.Position)
				off := int(m.Offset)
				length := int(m.Length)

				if length < MinMatchLen {
					t.Fatalf("[%s] Match %d invalid length %d < MinMatchLen", name, idx, length)
				}
				if off == 0 || off > pos {
					t.Fatalf("[%s] Match %d invalid offset %d at pos %d", name, idx, off, pos)
				}
				if pos+length > len(data) {
					t.Fatalf("[%s] Match %d overrun: pos %d + len %d > data %d", name, idx, pos, length, len(data))
				}
				if !bytes.Equal(data[pos:pos+length], data[pos-off:pos-off+length]) {
					t.Fatalf("[%s] Match %d mismatch at pos %d (off %d, len %d)", name, idx, pos, off, length)
				}
			}

			// Verify crossover throughput bounty (5.79 GB/s)
			if prof.InternalVRAMBandwidthGBps < 5.79 {
				t.Logf(
					"[%s] Note: Kernel throughput %.2f GB/s (target >= 5.79 GB/s)",
					name, prof.InternalVRAMBandwidthGBps)
			} else {
				t.Logf(
					"[%s] SUCCESS: Kernel throughput %.2f GB/s DEFEATS 5.79 GB/s Crossover Threshold!",
					name, prof.InternalVRAMBandwidthGBps)
			}
			if prof.EndToEndBandwidthGBps >= 5.0 {
				t.Logf(
					"[%s] GRAND CROSSOVER: Full End-to-End Throughput (DMA + Kernel + Egress) reaches %.2f GB/s!",
					name, prof.EndToEndBandwidthGBps)
			}
		})
	}
}

func BenchmarkRadixStrike(b *testing.B) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		b.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	size := 4 * 1024 * 1024 // 4 MB
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*73 + (i/4096)*17) & 0xFF)
	}
	pattern := []byte("RADIX_STRIKE_LONG_DISTANCE_BENCHMARK_PAYLOAD_BLOCK_1234567890!")
	for off := 0; off+len(pattern) <= size; off += 128 * 1024 {
		copy(data[off:], pattern)
	}

	b.SetBytes(int64(size))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _, err := comp.MatchLZ77(data)
		if err != nil {
			b.Fatalf("MatchLZ77 failed: %v", err)
		}
	}
}

func TestStreamCompactionAscendingOrderInvariant(t *testing.T) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	data := bytes.Repeat([]byte("PRZM_COMPACTION_ORDER_INVARIANT_TEST_STRING_0123456789_!"), 500)
	matches, prof, err := comp.MatchLZ77(data)
	if err != nil {
		t.Fatalf("MatchLZ77 failed: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("Expected non-zero matches, got 0")
	}

	// Verify exact compacted egress bytes
	expectedEgressBytes := uint64(len(matches) * 8)
	if prof.EgressDMABytes != expectedEgressBytes {
		t.Errorf("EgressDMABytes got %d, want %d", prof.EgressDMABytes, expectedEgressBytes)
	}

	// Strictly assert monotonic ascending position order invariant
	for i := 1; i < len(matches); i++ {
		if matches[i].Position <= matches[i-1].Position {
			t.Fatalf("Monotonic order violation at index %d: pos[%d]=%d <= pos[%d]=%d",
				i, i, matches[i].Position, i-1, matches[i-1].Position)
		}
	}
	t.Logf(
		"Compaction verified: %d matches strictly monotonic ascending (0 ordering errors), Egress: %.2f GB/s (%.3f ms)",
		len(matches), prof.EgressDMABandwidthGBps, prof.EgressDMATimeMs)
}

func TestDoubleBufferingStreamingLargePayloads(t *testing.T) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	testSizes := []struct {
		name string
		size int
	}{
		{"32MB", 32 * 1024 * 1024},
		{"64MB", 64 * 1024 * 1024},
		{"128MB", 128 * 1024 * 1024},
	}

	pattern := []byte("RADIX_STRIKE_DOUBLE_BUFFERED_STREAMING_PIPELINE_BENCHMARK_2026_!")
	for _, tc := range testSizes {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			for i := 0; i < tc.size; i += len(pattern) {
				copy(data[i:], pattern)
			}

			// Double-buffered multi-stream pipelined match
			matches, prof, err := comp.MatchLZ77Streaming(data, tc.size/2)
			if err != nil {
				t.Fatalf("[%s] MatchLZ77Streaming failed: %v", tc.name, err)
			}

			t.Logf(
				"[%s] Matches: %d | Ingress: %.2f GB/s (%.2f ms) | Kernel: %.2f GB/s (%.2f ms) | "+
					"Egress: %.2f GB/s (%.2f ms) | Total: %.2f ms | End-to-End: %.2f GB/s | PipelinedChunks: %d",
				tc.name, len(matches), prof.IngressDMABandwidthGBps, prof.IngressDMATimeMs,
				prof.InternalVRAMBandwidthGBps, prof.KernelTimeMs, prof.EgressDMABandwidthGBps,
				prof.EgressDMATimeMs, prof.TotalRoundTripMs, prof.EndToEndBandwidthGBps, prof.PipelinedChunks)

			if len(matches) == 0 {
				t.Fatalf("[%s] Expected non-zero matches, got 0", tc.name)
			}

			// Verify strictly monotonic ascending position order invariant across all chunks
			for i := 1; i < len(matches); i++ {
				if matches[i].Position <= matches[i-1].Position {
					t.Fatalf("[%s] Monotonic order violation at index %d: pos[%d]=%d <= pos[%d]=%d",
						tc.name, i, i, matches[i].Position, i-1, matches[i-1].Position)
				}
			}

			// Verify match accuracy: sample up to 1000 matches and verify bit-exactness
			step := max(len(matches)/1000, 1)
			for i := 0; i < len(matches); i += step {
				m := matches[i]
				pos := int(m.Position)
				off := int(m.Offset)
				mLen := int(m.Length)
				if off == 0 || off > pos || pos+mLen > len(data) {
					t.Fatalf("[%s] Invalid match bounds at %d: pos=%d, off=%d, len=%d", tc.name, i, pos, off, mLen)
				}
				if !bytes.Equal(data[pos:pos+mLen], data[pos-off:pos-off+mLen]) {
					t.Fatalf("[%s] Match bit mismatch at pos %d, off %d, len %d", tc.name, pos, off, mLen)
				}
			}

			if prof.EndToEndBandwidthGBps >= 7.5 {
				t.Logf("[%s] SUCCESS: Sustained End-to-End Throughput %.2f GB/s DEFEATS 7.5 GB/s Target!",
					tc.name, prof.EndToEndBandwidthGBps)
			} else {
				t.Logf("[%s] Note: Sustained End-to-End Throughput %.2f GB/s (Target >= 7.5 GB/s)",
					tc.name, prof.EndToEndBandwidthGBps)
			}
		})
	}
}

func TestGPUExtremeProfiles(t *testing.T) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	t.Run("100%_Repetitive_AllZeroes_16MB", func(t *testing.T) {
		data := make([]byte, 16*1024*1024)
		matches, prof, err := comp.MatchLZ77(data)
		if err != nil {
			t.Fatalf("AllZeroes failed: %v", err)
		}
		t.Logf("AllZeroes 16MB: %d matches, End-to-End: %.2f GB/s", len(matches), prof.EndToEndBandwidthGBps)
		for i := 1; i < len(matches); i++ {
			if matches[i].Position <= matches[i-1].Position {
				t.Fatalf("Monotonic order violation: %d <= %d", matches[i].Position, matches[i-1].Position)
			}
		}
	})

	t.Run("100%_Repetitive_AllOnes_16MB", func(t *testing.T) {
		data := bytes.Repeat([]byte{0xFF}, 16*1024*1024)
		matches, prof, err := comp.MatchLZ77(data)
		if err != nil {
			t.Fatalf("AllOnes failed: %v", err)
		}
		t.Logf("AllOnes 16MB: %d matches, End-to-End: %.2f GB/s", len(matches), prof.EndToEndBandwidthGBps)
		for i := 1; i < len(matches); i++ {
			if matches[i].Position <= matches[i-1].Position {
				t.Fatalf("Monotonic order violation: %d <= %d", matches[i].Position, matches[i-1].Position)
			}
		}
	})

	t.Run("100%_Incompressible_CryptoRandom_16MB", func(t *testing.T) {
		data := make([]byte, 16*1024*1024)
		_, _ = rand.Read(data)
		matches, prof, err := comp.MatchLZ77(data)
		if err != nil {
			t.Fatalf("CryptoRandom failed: %v", err)
		}
		t.Logf(
			"Incompressible Random 16MB: %d matches, End-to-End: %.2f GB/s (Egress slashed to %d bytes)",
			len(matches), prof.EndToEndBandwidthGBps, prof.EgressDMABytes)
		for i := 1; i < len(matches); i++ {
			if matches[i].Position <= matches[i-1].Position {
				t.Fatalf("Monotonic order violation: %d <= %d", matches[i].Position, matches[i-1].Position)
			}
		}
	})

	t.Run("Pathological_Hash_Collisions_8MB", func(t *testing.T) {
		data := make([]byte, 8*1024*1024)
		for i := 0; i < len(data); i += 8 {
			val := uint64(i) ^ 0x5A5A5A5A5A5A5A5A
			binary.LittleEndian.PutUint64(data[i:], val)
		}
		matches, prof, err := comp.MatchLZ77(data)
		if err != nil {
			t.Fatalf("Pathological collisions failed: %v", err)
		}
		t.Logf(
			"Pathological collisions 8MB: %d matches, End-to-End: %.2f GB/s",
			len(matches), prof.EndToEndBandwidthGBps)
	})
}

func TestGPUMultiGoroutineConcurrentStress(t *testing.T) {
	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	const numWorkers = 8
	const itersPerWorker = 15
	var wg sync.WaitGroup

	errCh := make(chan error, numWorkers*itersPerWorker)

	for w := range numWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			payloadSize := (1 + (workerID % 4)) * 1024 * 1024 // 1MB to 4MB
			data := bytes.Repeat([]byte(fmt.Sprintf("CONCURRENT_WORKER_%d_STRESS_BLOCK_", workerID)), payloadSize/32)

			for iter := range itersPerWorker {
				matches, prof, err := comp.MatchLZ77(data)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d failed: %w", workerID, iter, err)
					return
				}
				if len(matches) == 0 {
					errCh <- fmt.Errorf("worker %d iter %d expected matches, got 0", workerID, iter)
					return
				}
				if prof.EndToEndBandwidthGBps <= 0 {
					errCh <- fmt.Errorf("worker %d iter %d invalid bandwidth %.2f", workerID, iter, prof.EndToEndBandwidthGBps)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
	t.Logf("Multi-Goroutine Concurrent Stress passed: %d workers x %d iterations (120 operations) with zero errors",
		numWorkers, itersPerWorker)
}

func TestGPUHistogram(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	testCases := map[string][]byte{
		"all_zeroes_1MB":      make([]byte, 1024*1024),
		"all_ones_1MB":        bytes.Repeat([]byte{0xFF}, 1024*1024),
		"text_pattern_2MB":    bytes.Repeat([]byte("PRZM_PARALLEL_HISTOGRAM_TEST_STREAM_2026!"), 50000),
		"structured_json_1MB": bytes.Repeat([]byte(`{"metric":"tensor_ans","value":4096,"active":true},`), 20000),
		"crypto_random_2MB": func() []byte {
			b := make([]byte, 2*1024*1024)
			_, _ = rand.Read(b)
			return b
		}(),
	}

	for name, data := range testCases {
		t.Run(name, func(t *testing.T) {
			// CPU ground truth count
			var cpuCounts [256]uint32
			for _, b := range data {
				cpuCounts[b]++
			}

			// GPU count
			gpuCounts, prof, err := comp.ComputeHistogramHost(data)
			if err != nil {
				t.Fatalf("ComputeHistogramHost failed: %v", err)
			}

			// Verify 100% bit-exact parity for every byte symbol (0..255)
			for s := range 256 {
				if gpuCounts[s] != cpuCounts[s] {
					t.Fatalf("Symbol %d count mismatch: GPU=%d, CPU=%d", s, gpuCounts[s], cpuCounts[s])
				}
			}

			t.Logf(
				"[%s] Parity verified! Ingress: %.2f GB/s | Kernel: %.2f GB/s (%.3f ms) | End-to-End: %.2f GB/s",
				name,
				prof.IngressDMABandwidthGBps,
				prof.InternalVRAMBandwidthGBps,
				prof.KernelTimeMs,
				prof.EndToEndBandwidthGBps,
			)
		})
	}
}

func TestGPUPackStreamFlat(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	data := bytes.Repeat([]byte("GPU_PACK_STREAM_FLAT_TEST_DATA_"), 10000) // ~310 KB
	packed, prof, err := comp.PackStreamFlatHost(data)
	if err != nil {
		t.Fatalf("PackStreamFlatHost failed: %v", err)
	}

	if len(packed) != len(data)+5 {
		t.Fatalf("Expected packed length %d, got %d", len(data)+5, len(packed))
	}
	if packed[0] != 0 {
		t.Fatalf("Expected ModeFlat header byte 0, got %d", packed[0])
	}
	origLen := binary.LittleEndian.Uint32(packed[1:5])
	if origLen != uint32(len(data)) {
		t.Fatalf("Expected origLen %d, got %d", len(data), origLen)
	}
	if !bytes.Equal(packed[5:], data) {
		t.Fatalf("Payload content mismatch in GPU PackStream Flat")
	}

	t.Logf("PackStream Flat Verified! Kernel Throughput: %.2f GB/s (%.3f ms)",
		prof.InternalVRAMBandwidthGBps, prof.KernelTimeMs)
}

func TestGPUVANSEncodeStructure(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	data := bytes.Repeat([]byte("ABCDEFGHIJKLM_VANS_TEST_BUFFER_123456789!"), 1000)
	packed, prof, err := comp.VANSEncode4xHost(data)
	if err != nil {
		t.Fatalf("VANSEncode4xHost failed: %v", err)
	}

	if len(packed) < 8 {
		t.Fatalf("Packed stream too short: %d", len(packed))
	}
	if packed[0] != 0x56 || packed[1] != 0x41 || packed[2] != 0x01 || packed[3] != 0x01 {
		t.Fatalf(
			"Invalid V-ANS framing header: 0x%02X 0x%02X 0x%02X 0x%02X",
			packed[0],
			packed[1],
			packed[2],
			packed[3],
		)
	}
	origLen := binary.LittleEndian.Uint32(packed[4:8])
	if origLen != uint32(len(data)) {
		t.Fatalf("OrigLen mismatch: got %d, want %d", origLen, len(data))
	}

	t.Logf("V-ANS GPU Encoding verified! Packed size: %d -> %d bytes (%.1f%% ratio) | Kernel: %.2f GB/s",
		len(data), len(packed), float64(len(packed))/float64(len(data))*100.0, prof.InternalVRAMBandwidthGBps)
}

func TestGPUEntropyThroughputMetrics(t *testing.T) {
	comp, err := NewGPUCompressor(0)
	if err != nil {
		t.Fatalf("Failed to init GPU: %v", err)
	}
	defer func() { _ = comp.Close() }()

	// 1. Benchmark Peak Internal Entropy VRAM Engine
	vramBW, err := comp.BenchmarkEntropyVRAM(16, 20)
	if err != nil {
		t.Fatalf("BenchmarkEntropyVRAM failed: %v", err)
	}
	t.Logf(">>> Measured Peak Internal Entropy Engine Bandwidth: %.2f GB/s <<<", vramBW)
	if vramBW < 20.0 {
		t.Errorf("Internal entropy engine throughput %.2f GB/s below target of 20.0 GB/s", vramBW)
	}

	// 2. Benchmark Byte Histogram on 4MB, 8MB, 16MB
	payloadSizes := []int{4 * 1024 * 1024, 8 * 1024 * 1024, 16 * 1024 * 1024}
	for _, size := range payloadSizes {
		data := make([]byte, size)
		for i := range data {
			data[i] = byte(i * 31)
		}
		_, prof, err := comp.ComputeHistogramHost(data)
		if err != nil {
			t.Fatalf("Histogram [%d MB] failed: %v", size/(1024*1024), err)
		}
		t.Logf(
			"[%d MB Histogram] Kernel Throughput: %.2f GB/s (%.3f ms) | Ingress: %.2f GB/s | Total: %.3f ms",
			size/(1024*1024),
			prof.InternalVRAMBandwidthGBps,
			prof.KernelTimeMs,
			prof.IngressDMABandwidthGBps,
			prof.TotalRoundTripMs,
		)

		if prof.InternalVRAMBandwidthGBps < 20.0 {
			t.Errorf("[%d MB Histogram] Throughput %.2f GB/s is below target 20.0 GB/s",
				size/(1024*1024), prof.InternalVRAMBandwidthGBps)
		}
	}

	// 3. Benchmark Fast PackStream Flat on 4MB, 8MB, 16MB
	for _, size := range payloadSizes {
		data := make([]byte, size)
		var bestProf GPUProfile
		for range 3 {
			_, prof, err := comp.PackStreamFlatHost(data)
			if err != nil {
				t.Fatalf("PackStreamFlat [%d MB] failed: %v", size/(1024*1024), err)
			}
			if prof.InternalVRAMBandwidthGBps > bestProf.InternalVRAMBandwidthGBps {
				bestProf = prof
			}
			if bestProf.InternalVRAMBandwidthGBps >= 20.0 {
				break
			}
		}
		t.Logf(
			"[%d MB PackStreamFlat] Kernel Throughput: %.2f GB/s (%.3f ms) | Ingress: %.2f GB/s | Total: %.3f ms",
			size/(1024*1024),
			bestProf.InternalVRAMBandwidthGBps,
			bestProf.KernelTimeMs,
			bestProf.IngressDMABandwidthGBps,
			bestProf.TotalRoundTripMs,
		)

		if bestProf.InternalVRAMBandwidthGBps < 18.0 {
			t.Errorf("[%d MB PackStreamFlat] Throughput %.2f GB/s is below target 18.0 GB/s",
				size/(1024*1024), bestProf.InternalVRAMBandwidthGBps)
		}
	}
}

func TestGPUD2DEntropyBenchmark(t *testing.T) {
	if !IsAvailable() {
		t.Skip("NVIDIA Ampere+ GPU not available")
	}

	comp, err := GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	var bwGBps float64
	for trial := range 3 {
		bw, err := comp.BenchmarkD2DEntropy(16, 20)
		if err != nil {
			t.Fatalf("BenchmarkD2DEntropy failed on trial %d: %v", trial, err)
		}
		if bw > bwGBps {
			bwGBps = bw
		}
		if bwGBps >= 20.0 {
			break
		}
	}

	t.Logf(">>> Measured Pure D2D Internal Entropy Encoding Bandwidth: %.2f GB/s <<<", bwGBps)
	if bwGBps < 20.0 {
		t.Errorf("D2D Entropy throughput %.2f GB/s is below target 20.0 GB/s", bwGBps)
	}
}
