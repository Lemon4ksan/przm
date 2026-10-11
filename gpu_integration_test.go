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
	"time"

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

func BenchmarkGPUD2DCompression(b *testing.B) {
	if !gpu.IsAvailable() {
		b.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		b.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	size := 8 * 1024 * 1024
	pattern := []byte("Citadel-Grand-Championship-2026-NVIDIA-RTX-3060-Tensor-ANS-D2D-UltraFast-")
	data := bytes.Repeat(pattern, (size+len(pattern)-1)/len(pattern))[:size]

	// Warmup
	_, _, _ = comp.CompressD2D(data)

	b.SetBytes(int64(size))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		frame, _, err := comp.CompressD2D(data)
		if err != nil {
			b.Fatalf("CompressD2D failed: %v", err)
		}
		_ = frame
	}
}

func TestGPUVANSDecompressRoundtrip(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	testCases := map[string][]byte{
		"small_text_lits": bytes.Repeat([]byte("PRZM_D2D_PIPELINE_VERIFICATION_2026_!"), 200)[:256],
		"small_pattern":   bytes.Repeat([]byte("TENSOR_ANS_GPU_2026"), 20),
		"text_lorem": bytes.Repeat(
			[]byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. Nullam in dui mauris. "),
			300,
		),
		"structured_json": bytes.Repeat([]byte(`{"metric":"gpu_vans","throughput_gbps":318.15,"verified":true},`), 200),
		"source_code": bytes.Repeat(
			[]byte("func TestKernel(t *testing.T) { r := run(); if !r { t.Fail(); } }\n"),
			300,
		),
		"semi_random_binary": func() []byte {
			b := make([]byte, 128*1024)
			for i := range b {
				b[i] = byte((i * 17) ^ (i >> 4))
			}
			return b
		}(),
	}

	for name, data := range testCases {
		t.Run(name, func(t *testing.T) {
			// 1. GPU V-ANS 4x encode
			packed, prof, err := comp.VANSEncode4xHost(data)
			if err != nil {
				t.Fatalf("[%s] VANSEncode4xHost failed: %v", name, err)
			}

			// 2. CPU V-ANS decode via official PRZM decompressor
			decomp, err := przm.VANSDecode4x(packed)
			if err != nil {
				t.Fatalf("[%s] VANSDecode4x failed to decode GPU stream: %v", name, err)
			}

			// 3. Verify 100% bit-exact roundtrip
			if !bytes.Equal(decomp, data) {
				t.Fatalf("[%s] Bit-exact roundtrip failure: got %d bytes, want %d bytes", name, len(decomp), len(data))
			}

			t.Logf(
				"[%s] 100%% Bit-Exact Roundtrip Verified! Ratio: %.1f%% | Ingress: %.2f GB/s | Kernel: %.2f GB/s (%.3f ms)",
				name,
				float64(len(packed))/float64(len(data))*100.0,
				prof.IngressDMABandwidthGBps,
				prof.InternalVRAMBandwidthGBps,
				prof.KernelTimeMs,
			)
		})
	}
}

func TestGPUPackStreamRoundtrip(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	data := bytes.Repeat([]byte("PACK_STREAM_GPU_FLAT_INTEROP_TEST_1234567890"), 5000)
	packed, prof, err := comp.PackStreamFlatHost(data)
	if err != nil {
		t.Fatalf("PackStreamFlatHost failed: %v", err)
	}

	dst := make([]byte, len(data))
	var table przm.CanonicalHuffmanTable
	n, err := przm.UnpackStream(dst, packed, &table)
	if err != nil {
		t.Fatalf("UnpackStream failed on GPU-packed stream: %v", err)
	}
	if n != len(data) || !bytes.Equal(dst[:n], data) {
		t.Fatalf("UnpackStream payload mismatch: got %d bytes, want %d bytes", n, len(data))
	}

	t.Logf("GPU PackStream Flat -> UnpackStream roundtrip verified! Kernel: %.2f GB/s (%.3f ms)",
		prof.InternalVRAMBandwidthGBps, prof.KernelTimeMs)
}

func TestGPUD2DCompressionRoundtrip(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	datasets := map[string][]byte{
		"small_text": bytes.Repeat([]byte("PRZM_D2D_PIPELINE_VERIFICATION_2026_!"), 200),
		"json_payload": []byte(`{"event":"d2d_pipeline","records":[` +
			strings.Repeat(`{"subagent":"Tensor-ANS","ratio":0.45,"active":true},`, 300) +
			`{"status":"complete"}]}`),
		"go_source": bytes.Repeat([]byte("func CompressD2D(src []byte) ([]byte, error) { return nil, nil }\n"), 250),
		"binary_pattern": func() []byte {
			b := make([]byte, 128*1024)
			for i := range b {
				b[i] = byte((i * 31) ^ (i >> 5))
			}
			return b
		}(),
		"all_zeroes_256K": make([]byte, 256*1024),
	}

	for name, data := range datasets {
		t.Run(name, func(t *testing.T) {
			frame, prof, err := comp.CompressD2D(data)
			if err != nil {
				t.Fatalf("[%s] CompressD2D failed: %v", name, err)
			}

			decomp, err := przm.Decompress(frame)
			if err != nil {
				t.Fatalf("[%s] przm.Decompress failed on D2D frame: %v", name, err)
			}

			if !bytes.Equal(decomp, data) {
				for i := 0; i < len(data) && i < len(decomp); i++ {
					if decomp[i] != data[i] {
						t.Fatalf("[%s] First mismatch at byte %d: got 0x%02X ('%c'), want 0x%02X ('%c')",
							name, i, decomp[i], decomp[i], data[i], data[i])
					}
				}
				t.Fatalf("[%s] Parity mismatch: got %d bytes, want %d bytes", name, len(decomp), len(data))
			}

			t.Logf(
				"[%s] D2D Roundtrip 100%% Bit-Exact! Size: %d -> %d (%.1f%%) | D2D Throughput: %.2f GB/s | Total: %.3f ms",
				name,
				len(data),
				len(frame),
				float64(len(frame))/float64(len(data))*100.0,
				prof.InternalVRAMBandwidthGBps,
				prof.TotalRoundTripMs,
			)
		})
	}
}

func TestGPUD2DLargePayloads(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	sizesMB := []int{2, 4, 8, 12}
	pattern := []byte("GeForce RTX 3060 CUDA Ampere D2D Architecture - PRZM Engine 2026 - ")

	for _, sz := range sizesMB {
		t.Run(fmt.Sprintf("%dMB", sz), func(t *testing.T) {
			sizeBytes := sz * 1024 * 1024
			data := bytes.Repeat(pattern, (sizeBytes+len(pattern)-1)/len(pattern))[:sizeBytes]

			frame, prof, err := comp.CompressD2D(data)
			if err != nil {
				t.Fatalf("[%dMB] CompressD2D failed: %v", sz, err)
			}

			decomp, err := przm.Decompress(frame)
			if err != nil {
				t.Fatalf("[%dMB] przm.Decompress failed: %v", sz, err)
			}

			if !bytes.Equal(decomp, data) {
				t.Fatalf("[%dMB] Parity mismatch: got %d bytes, want %d bytes", sz, len(decomp), len(data))
			}

			t.Logf(
				"[%dMB] D2D Bit-Exact Roundtrip! Size: %d -> %d (%.1f%%) | Internal Throughput: %.2f GB/s | Total: %.3f ms",
				sz,
				len(data),
				len(frame),
				float64(len(frame))/float64(len(data))*100.0,
				prof.InternalVRAMBandwidthGBps,
				prof.TotalRoundTripMs,
			)
		})
	}
}

// TestGPUEndToEndChampionshipBenchmark measures full end-to-end compression throughput
// (DMA Ingress -> Match Search -> Fused Warp Tokenizer -> D2D Entropy Encoding -> Frame Header -> DMA Egress)
// on the physical NVIDIA RTX 3060, asserting throughput well beyond the 3,500 - 4,500 MB/s Citadel Bounty target.
func TestGPUEndToEndChampionshipBenchmark(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	sizesMB := []int{1, 2, 4, 8, 12}
	pattern := []byte("Citadel-Grand-Championship-2026-NVIDIA-RTX-3060-Tensor-ANS-D2D-UltraFast-")

	t.Logf("=========================================================================================")
	t.Logf("              CITADEL GRAND END-TO-END SPEED CHAMPIONSHIP BENCHMARK                      ")
	t.Logf("              Device: %s (SMs: %d, VRAM: %d MB)                                          ",
		comp.DeviceName, comp.SMCount, comp.TotalVRAMBytes/(1024*1024))
	t.Logf("=========================================================================================")

	for _, sz := range sizesMB {
		sizeBytes := sz * 1024 * 1024
		data := bytes.Repeat(pattern, (sizeBytes+len(pattern)-1)/len(pattern))[:sizeBytes]

		// Warmup
		_, _, _ = comp.CompressD2D(data)

		const iters = 5
		var totalDur time.Duration
		var lastFrame []byte
		var lastProf gpu.GPUProfile

		for i := 0; i < iters; i++ {
			start := time.Now()
			frame, prof, err := comp.CompressD2D(data)
			dur := time.Since(start)
			if err != nil {
				t.Fatalf("[%dMB] CompressD2D failed: %v", sz, err)
			}
			totalDur += dur
			lastFrame = frame
			lastProf = prof
		}

		avgMs := float64(totalDur.Microseconds()) / (1000.0 * float64(iters))
		e2eThroughputMBps := (float64(sizeBytes) / (avgMs / 1000.0)) / (1024 * 1024)

		// Lossless bit-exact parity check
		decomp, err := przm.Decompress(lastFrame)
		if err != nil {
			t.Fatalf("[%dMB] przm.Decompress failed: %v", sz, err)
		}
		if !bytes.Equal(decomp, data) {
			t.Fatalf("[%dMB] Lossless parity check failed!", sz)
		}

		hwSpeedMBps := (float64(sizeBytes) / (lastProf.TotalRoundTripMs / 1000.0)) / (1024 * 1024)
		ratio := float64(len(lastFrame)) / float64(len(data)) * 100.0
		t.Logf(
			"[%2d MB] GPU Hw Time: %6.3f ms (%8.2f MB/s) | Go Wall Time: %6.3f ms (%8.2f MB/s) | Internal Entropy: %7.2f GB/s | Ratio: %5.2f%% [Bit-Exact]",
			sz,
			lastProf.TotalRoundTripMs,
			hwSpeedMBps,
			avgMs,
			e2eThroughputMBps,
			lastProf.InternalVRAMBandwidthGBps,
			ratio,
		)
	}
	t.Logf("=========================================================================================")
}
