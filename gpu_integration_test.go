// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

func TestGPUCompressionRoundtrip(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available on this machine")
	}

	testDatasets := map[string][]byte{
		"repeated_pattern": bytes.Repeat([]byte("PRZM_GPU_ACCELERATION_TEST_BLOCK_0123456789_"), 500),
		"structured_json": []byte(`{"status":"success","code":200,"records":[` +
			strings.Repeat(`{"event":"heartbeat","stream":"archival","seq":9999,"active":true},`, 150) +
			`{"id":1,"name":"alpha","val":100}]}`),
		"source_code": []byte(
			strings.Repeat("func BenchmarkKernel(b *testing.B) { for i := 0; i < b.N; i++ { run(); } }\n", 100),
		),
		"mixed_binary": func() []byte {
			b := make([]byte, 64*1024)
			for i := range b {
				b[i] = byte((i * 37) ^ (i >> 3))
			}
			return b
		}(),
	}

	for name, data := range testDatasets {
		t.Run(name, func(t *testing.T) {
			opts := przm.CompressOptions{
				GPUAcceleration: true,
			}

			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("[%s] CompressWithOptions with GPU failed: %v", name, err)
			}

			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("[%s] Decompress of GPU-compressed stream failed: %v", name, err)
			}

			if !bytes.Equal(decomp, data) {
				t.Fatalf("[%s] Lossless roundtrip failure: got %d bytes, want %d bytes", name, len(decomp), len(data))
			}
		})
	}
}

func TestGPUWithGPUAccelerationOption(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available on this machine")
	}

	data := bytes.Repeat([]byte("OptionWithGPUAccelerationIntegrationVerification_"), 200)

	// Call using variadic Option function WithGPUAcceleration()
	comp, err := przm.CompressWithOptions(data, przm.CompressOptions{}, przm.WithGPUAcceleration())
	if err != nil {
		t.Fatalf("CompressWithOptions with WithGPUAcceleration() option failed: %v", err)
	}

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(decomp, data) {
		t.Fatalf("Decompressed output does not match original data")
	}
}

func TestGPUFallbackWhenUnavailable(t *testing.T) {
	data := bytes.Repeat([]byte("FallbackWhenUnavailableSimulatedTestPayload!"), 300)

	// Force GPU to report unavailable
	gpu.SetMockUnavailableForTest(true)
	defer gpu.SetMockUnavailableForTest(false)

	opts := przm.CompressOptions{
		GPUAcceleration: true,
	}

	// Should transparently fallback to CPU SIMD
	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Expected transparent fallback to CPU, but compression failed: %v", err)
	}

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed after fallback compression: %v", err)
	}

	if !bytes.Equal(decomp, data) {
		t.Fatalf("Lossless roundtrip mismatch during mock unavailable fallback")
	}
}

func TestGPUFallbackOnMatchError(t *testing.T) {
	data := bytes.Repeat([]byte("FallbackOnMatchErrorSimulatedCUDAFailureData!"), 300)

	// Force GPU MatchLZ77 to return error
	gpu.SetMockMatchErrorForTest(errors.New("simulated CUDA driver failure: out of memory"))
	defer gpu.SetMockMatchErrorForTest(nil)

	opts := przm.CompressOptions{
		GPUAcceleration: true,
	}

	// Should transparently fallback to CPU SIMD
	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Expected transparent fallback to CPU, but compression failed: %v", err)
	}

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed after fallback compression: %v", err)
	}

	if !bytes.Equal(decomp, data) {
		t.Fatalf("Lossless roundtrip mismatch during mock match error fallback")
	}
}

func TestGPUFallbackEdgeCases(t *testing.T) {
	edgeCases := map[string][]byte{
		"empty":        {},
		"single_byte":  {0x42},
		"two_bytes":    {0x12, 0x34},
		"three_bytes":  {0x01, 0x02, 0x03},
		"sixteen_null": make([]byte, 16),
	}

	for name, data := range edgeCases {
		t.Run(name, func(t *testing.T) {
			opts := przm.CompressOptions{
				GPUAcceleration: true,
			}

			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("Edge case %s compression failed: %v", name, err)
			}

			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Edge case %s decompression failed: %v", name, err)
			}

			if !bytes.Equal(decomp, data) {
				t.Fatalf("Edge case %s roundtrip mismatch: got %v, want %v", name, decomp, data)
			}
		})
	}
}

func TestGPUMultiChunkIntegration(t *testing.T) {
	// Create payload larger than DefaultChunkSize (2 MiB) to exercise multi-chunk routing with GPU options
	chunkSize := 128 * 1024 // 128 KiB
	data := make([]byte, 512*1024)
	for i := range data {
		data[i] = byte((i * 13) % 251)
	}
	// Add repeated sections to allow compression
	for i := range 4 {
		copy(data[i*100000:], bytes.Repeat(fmt.Appendf(nil, "CHUNK_PATTERN_%d_", i), 2000))
	}

	opts := przm.CompressOptions{
		GPUAcceleration: true,
		ChunkSize:       chunkSize,
		Threads:         2,
	}

	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("MultiChunk compression with GPU failed: %v", err)
	}

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("MultiChunk decompression failed: %v", err)
	}

	if !bytes.Equal(decomp, data) {
		t.Fatalf("MultiChunk lossless roundtrip mismatch: len got %d, want %d", len(decomp), len(data))
	}
}

func TestGPULargePayloadCompressionRoundtrip(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available on this machine")
	}

	sizes := []int{4 * 1024 * 1024, 8 * 1024 * 1024, 16 * 1024 * 1024, 32 * 1024 * 1024, 64 * 1024 * 1024}

	for _, size := range sizes {
		name := fmt.Sprintf("%dMB", size/(1024*1024))
		t.Run(name, func(t *testing.T) {
			data := make([]byte, size)
			for i := range data {
				data[i] = byte((i*37 + (i/2048)*19) & 0xFF)
			}
			pattern := []byte("PRZM_GPU_RADIX_STRIKE_LONG_DISTANCE_COMPRESSION_VALIDATION_STRING_1234567890!")
			for off := 0; off+len(pattern) <= size; off += 128 * 1024 {
				copy(data[off:], pattern)
			}

			opts := przm.CompressOptions{
				GPUAcceleration: true,
			}

			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("[%s] CompressWithOptions failed: %v", name, err)
			}

			ratio := float64(len(data)) / float64(len(comp))
			t.Logf("[%s] Compressed from %d bytes to %d bytes (Ratio: %.2fx)", name, len(data), len(comp), ratio)

			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("[%s] CPU Decompress failed: %v", name, err)
			}

			if !bytes.Equal(decomp, data) {
				t.Fatalf("[%s] Lossless roundtrip mismatch: got %d bytes, want %d bytes", name, len(decomp), len(data))
			}
			t.Logf("[%s] Lossless roundtrip verified bit-exact on CPU decompressor!", name)
		})
	}
}

func BenchmarkGPUCompression(b *testing.B) {
	if !gpu.IsAvailable() {
		b.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	size := 4 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*37 + (i/2048)*19) & 0xFF)
	}
	pattern := []byte("PRZM_GPU_RADIX_STRIKE_BENCHMARK_PAYLOAD_BLOCK_1234567890!")
	for off := 0; off+len(pattern) <= size; off += 128 * 1024 {
		copy(data[off:], pattern)
	}

	opts := przm.CompressOptions{
		GPUAcceleration: true,
	}

	b.SetBytes(int64(size))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		comp, err := przm.CompressWithOptions(data, opts)
		if err != nil {
			b.Fatalf("CompressWithOptions failed: %v", err)
		}
		_ = comp
	}
}
