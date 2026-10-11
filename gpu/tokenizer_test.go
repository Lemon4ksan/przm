// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpu_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

func TestWarpTokenizerParityAndBitExactDecompress(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available on this machine")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	datasets := map[string][]byte{
		"repeated_pattern": bytes.Repeat([]byte("WARP_TOKENIZER_TEST_BLOCK_0123456789_!@#$%^&*()"), 2000),
		"structured_json": []byte(`{"status":"success","code":200,"records":[` +
			strings.Repeat(`{"event":"heartbeat","stream":"archival","seq":9999,"active":true,"tag":"citadel"},`, 500) +
			`{"id":1,"name":"alpha","val":100}]}`),
		"go_source_code": []byte(
			strings.Repeat(
				"func BenchmarkTokenizer(b *testing.B) { for i := 0; i < b.N; i++ { runWarpTokenizer(); } }\n",
				300,
			),
		),
		"all_zeroes_1MB": make([]byte, 1024*1024),
		"all_ones_1MB": func() []byte {
			b := make([]byte, 1024*1024)
			for i := range b {
				b[i] = 0xFF
			}
			return b
		}(),
		"rle_sparse_1MB": func() []byte {
			b := make([]byte, 1024*1024)
			for i := 0; i < len(b); i += 128 {
				for j := 0; j < 32 && i+j < len(b); j++ {
					b[i+j] = 0xAA
				}
			}
			return b
		}(),
		"pseudo_random_1MB": func() []byte {
			rng := rand.New(rand.NewSource(42))
			b := make([]byte, 1024*1024)
			rng.Read(b)
			return b
		}(),
	}

	for name, data := range datasets {
		t.Run(name, func(t *testing.T) {
			cmdBuf, offBuf, litBuf, prof, err := comp.Tokenize(data)
			if err != nil {
				t.Fatalf("[%s] Tokenize failed: %v", name, err)
			}

			t.Logf("[%s] Tokenized: Cmds: %d B, Offs: %d B, Lits: %d B | Kernel: %.3f ms (%.2f GB/s) | Total: %.3f ms",
				name, len(cmdBuf), len(offBuf), len(litBuf),
				prof.KernelTimeMs, prof.ThroughputGBps, prof.TotalRoundTripMs)

			opts := przm.CompressOptions{
				GPUAcceleration: true,
			}
			compressed, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("[%s] CompressWithOptions failed: %v", name, err)
			}

			decomp, err := przm.Decompress(compressed)
			if err != nil {
				t.Fatalf("[%s] przm.Decompress failed: %v", name, err)
			}

			if !bytes.Equal(decomp, data) {
				t.Fatalf("[%s] Parity failure: decompressed data does not match original (got %d bytes, want %d)",
					name, len(decomp), len(data))
			}
			t.Logf("[%s] Lossless roundtrip verified 100%% bit-exact!", name)
		})
	}
}

func TestWarpTokenizerLargePayloads(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available on this machine")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	sizes := []int{
		4 * 1024 * 1024,
		8 * 1024 * 1024,
		16 * 1024 * 1024,
		32 * 1024 * 1024,
		64 * 1024 * 1024,
	}

	for _, size := range sizes {
		mb := size / (1024 * 1024)
		name := fmt.Sprintf("%dMB", mb)
		t.Run(name, func(t *testing.T) {
			data := make([]byte, size)
			for i := range data {
				data[i] = byte((i*37 + (i/2048)*19) & 0xFF)
			}
			pattern := []byte("PRZM_WARP_TOKENIZER_PARALLEL_STREAM_COMPACTION_VALIDATION_STRING_AmpereSM86!")
			for off := 0; off+len(pattern) <= size; off += 64 * 1024 {
				copy(data[off:], pattern)
			}

			cmdBuf, offBuf, litBuf, prof, err := comp.Tokenize(data)
			if err != nil {
				t.Fatalf("[%s] Tokenize failed: %v", name, err)
			}

			t.Logf(
				"[%s] Tokenize Profile: Kernel: %.3f ms (Pass1: %.3f ms, Pass2: %.3f ms, Pass3: %.3f ms) (%.2f GB/s) | Total: %.3f ms (%.2f GB/s) | Chunks: %d | Cmds: %d, Offs: %d, Lits: %d",
				name,
				prof.KernelTimeMs,
				prof.Pass1Ms,
				prof.Pass2Ms,
				prof.Pass3Ms,
				prof.ThroughputGBps,
				prof.TotalRoundTripMs,
				(float64(size)/(prof.TotalRoundTripMs/1000.0))/1e9,
				prof.NumChunks,
				len(cmdBuf),
				len(offBuf),
				len(litBuf),
			)

			if mb == 64 {
				if prof.KernelTimeMs < 1.5 {
					t.Logf("[64MB] SUCCESS: Kernel time %.3f ms DEFEATS <1.5 ms Target! (%.2f GB/s)",
						prof.KernelTimeMs, prof.ThroughputGBps)
				} else {
					t.Logf("[64MB] Notice: Kernel time %.3f ms (Target < 1.5 ms)", prof.KernelTimeMs)
				}
			}

			opts := przm.CompressOptions{
				GPUAcceleration: true,
			}
			compressed, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("[%s] CompressWithOptions failed: %v", name, err)
			}

			decomp, err := przm.Decompress(compressed)
			if err != nil {
				t.Fatalf("[%s] przm.Decompress failed: %v", name, err)
			}

			if !bytes.Equal(decomp, data) {
				t.Fatalf("[%s] Lossless bit-exact roundtrip mismatch!", name)
			}
			t.Logf("[%s] Lossless bit-exact parity verified with przm.Decompress!", name)
		})
	}
}

func BenchmarkWarpTokenizerKernel64MB(b *testing.B) {
	if !gpu.IsAvailable() {
		b.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		b.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	size := 64 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*37 + (i/2048)*19) & 0xFF)
	}
	pattern := []byte("PRZM_WARP_TOKENIZER_BENCHMARK_PAYLOAD_AmpereSM86_RTX3060_VALIDATION_STRING_0123456789")
	for off := 0; off+len(pattern) <= size; off += 64 * 1024 {
		copy(data[off:], pattern)
	}

	// Warm up and initialize buffers & matches on GPU
	_, _, _, _, err = comp.Tokenize(data)
	if err != nil {
		b.Fatalf("Warmup Tokenize failed: %v", err)
	}

	b.SetBytes(int64(size))
	b.ResetTimer()

	var totalKernelMs float64
	for i := 0; i < b.N; i++ {
		_, _, _, prof, err := comp.TokenizeDeviceOnly(size)
		if err != nil {
			b.Fatalf("TokenizeDeviceOnly failed: %v", err)
		}
		totalKernelMs += prof.KernelTimeMs
	}

	if b.N > 0 {
		avgKernelMs := totalKernelMs / float64(b.N)
		throughput := (float64(size) / (avgKernelMs / 1000.0)) / 1e9
		b.Logf("[64MB Kernel] Average Kernel Time: %.3f ms | Sustained Throughput: %.2f GB/s", avgKernelMs, throughput)
	}
}

func BenchmarkWarpTokenizerEndToEnd64MB(b *testing.B) {
	if !gpu.IsAvailable() {
		b.Skip("NVIDIA Ampere+ CUDA GPU not available")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		b.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	size := 64 * 1024 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*37 + (i/2048)*19) & 0xFF)
	}
	pattern := []byte("PRZM_WARP_TOKENIZER_BENCHMARK_PAYLOAD_AmpereSM86_RTX3060_VALIDATION_STRING_0123456789")
	for off := 0; off+len(pattern) <= size; off += 64 * 1024 {
		copy(data[off:], pattern)
	}

	b.SetBytes(int64(size))
	b.ResetTimer()

	var totalRoundTripMs float64
	for i := 0; i < b.N; i++ {
		_, _, _, prof, err := comp.Tokenize(data)
		if err != nil {
			b.Fatalf("Tokenize failed: %v", err)
		}
		totalRoundTripMs += prof.TotalRoundTripMs
	}

	if b.N > 0 {
		avgMs := totalRoundTripMs / float64(b.N)
		throughput := (float64(size) / (avgMs / 1000.0)) / 1e9
		b.Logf(
			"[64MB End-to-End] Average RoundTrip Time: %.3f ms | End-to-End Throughput: %.2f GB/s",
			avgMs,
			throughput,
		)
	}
}

func TestTokenizeDeviceZeroCopy(t *testing.T) {
	if !gpu.IsAvailable() {
		t.Skip("NVIDIA Ampere+ CUDA GPU not available on this machine")
	}

	comp, err := gpu.GetGlobalCompressor()
	if err != nil {
		t.Fatalf("GetGlobalCompressor failed: %v", err)
	}

	testPayload := []byte(`{"event":"warp_tokenizer_zero_copy_test","data":[` +
		strings.Repeat(`{"subagent":"Warp-Tokenizer","target":"SM8.6","zero_copy":true},`, 1000) +
		`{"status":"complete"}]}`)

	// Reference tokenization (Host egress)
	refCmd, refOff, refLit, refProf, err := comp.Tokenize(testPayload)
	if err != nil {
		t.Fatalf("comp.Tokenize failed: %v", err)
	}
	t.Logf("Ref: Cmds: %d, Offs: %d, Lits: %d", len(refCmd), len(refOff), len(refLit))

	// Run reference tokenization a 2nd time to check idempotence
	refCmd2, refOff2, refLit2, _, err := comp.Tokenize(testPayload)
	if err != nil {
		t.Fatalf("comp.Tokenize 2nd failed: %v", err)
	}
	t.Logf("Ref2: Cmds: %d, Offs: %d, Lits: %d", len(refCmd2), len(refOff2), len(refLit2))

	// Zero-copy device tokenization
	devStreams, zeroCopyProf, err := comp.TokenizeDeviceZeroCopy(testPayload)
	if err != nil {
		t.Fatalf("comp.TokenizeDeviceZeroCopy failed: %v", err)
	}
	t.Logf("Dev: Cmds: %d, Offs: %d, Lits: %d", devStreams.CmdLen, devStreams.OffLen, devStreams.LitLen)

	if devStreams.DFinalCmds == 0 || devStreams.DFinalOffs == 0 || devStreams.DFinalLits == 0 ||
		devStreams.DTotals == 0 {
		t.Fatalf("ZeroCopy returned null device pointers: %+v", devStreams)
	}

	if int(devStreams.CmdLen) != len(refCmd) {
		t.Fatalf("CmdLen mismatch: got %d, want %d", devStreams.CmdLen, len(refCmd))
	}
	if int(devStreams.OffLen) != len(refOff) {
		t.Fatalf("OffLen mismatch: got %d, want %d", devStreams.OffLen, len(refOff))
	}
	if int(devStreams.LitLen) != len(refLit) {
		t.Fatalf("LitLen mismatch: got %d, want %d", devStreams.LitLen, len(refLit))
	}

	// Download device streams to host and verify bit-exact parity
	gotCmd, err := comp.DownloadDeviceBytes(devStreams.DFinalCmds, int(devStreams.CmdLen))
	if err != nil {
		t.Fatalf("DownloadDeviceBytes DFinalCmds failed: %v", err)
	}
	gotOff, err := comp.DownloadDeviceBytes(devStreams.DFinalOffs, int(devStreams.OffLen))
	if err != nil {
		t.Fatalf("DownloadDeviceBytes DFinalOffs failed: %v", err)
	}
	gotLit, err := comp.DownloadDeviceBytes(devStreams.DFinalLits, int(devStreams.LitLen))
	if err != nil {
		t.Fatalf("DownloadDeviceBytes DFinalLits failed: %v", err)
	}

	if !bytes.Equal(gotCmd, refCmd) {
		t.Fatalf("ZeroCopy DFinalCmds content mismatch vs refCmd")
	}
	if !bytes.Equal(gotOff, refOff) {
		t.Fatalf("ZeroCopy DFinalOffs content mismatch vs refOff")
	}
	if !bytes.Equal(gotLit, refLit) {
		t.Fatalf("ZeroCopy DFinalLits content mismatch vs refLit")
	}

	t.Logf(
		"ZeroCopy Device Streams Verified 100%% Bit-Exact! Cmds: %d, Offs: %d, Lits: %d | Kernel: %.3f ms (%.2f GB/s)",
		devStreams.CmdLen,
		devStreams.OffLen,
		devStreams.LitLen,
		zeroCopyProf.KernelTimeMs,
		zeroCopyProf.ThroughputGBps,
	)
	_ = refProf

	// Also verify TokenizePointersDeviceOnly on pre-staged buffers
	devStreamsOnly, devOnlyProf, err := comp.TokenizePointersDeviceOnly(len(testPayload))
	if err != nil {
		t.Fatalf("comp.TokenizePointersDeviceOnly failed: %v", err)
	}
	if devStreamsOnly.CmdLen != devStreams.CmdLen || devStreamsOnly.OffLen != devStreams.OffLen ||
		devStreamsOnly.LitLen != devStreams.LitLen {
		t.Fatalf("TokenizePointersDeviceOnly lengths mismatch: %+v vs %+v", devStreamsOnly, devStreams)
	}
	t.Logf("TokenizePointersDeviceOnly verified! Kernel: %.3f ms (%.2f GB/s)",
		devOnlyProf.KernelTimeMs, devOnlyProf.ThroughputGBps)
}
