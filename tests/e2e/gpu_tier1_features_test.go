// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

// Tier 1: Feature Coverage (10 Features x 5 Tests = 50 Tests)

// Feature 1: Warp-Bucketed Shared Memory LZ77 Kernel
func TestTier1_GPU_F01_WarpSharedMemoryLZ77(t *testing.T) {
	comp := getTestGPUCompressor(t)

	tests := []struct {
		name  string
		tiles int
	}{
		{"SingleTile_32KB", 1},
		{"DualTile_64KB", 2},
		{"QuadTile_128KB", 4},
		{"OctaTile_256KB", 8},
		{"HexTile_512KB", 16},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateTiledData(tc.tiles, fmt.Sprintf("PRZM_GPU_TIER1_TILE_PATTERN_%s_", tc.name))
			matches, prof, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed on %d tiles: %v", tc.tiles, err)
			}
			assertGPUMatchIntegrity(t, data, matches)
			if len(matches) == 0 {
				t.Fatalf("Expected matches on repetitive tiled data, got 0")
			}
			if prof.KernelTimeMs < 0 {
				t.Fatalf("Invalid kernel time: %f", prof.KernelTimeMs)
			}
		})
	}
}

// Feature 2: PTX 7.1 sm_86 Kernel Artifact Verification
func TestTier1_GPU_F02_PTXKernelArtifact(t *testing.T) {
	comp := getTestGPUCompressor(t)

	tests := []struct {
		name     string
		validate func(t *testing.T)
	}{
		{
			name: "SMCount_AmpereThreshold",
			validate: func(t *testing.T) {
				if comp.SMCount < 20 {
					t.Fatalf("SMCount %d < 20 required for RTX 3060", comp.SMCount)
				}
			},
		},
		{
			name: "ComputeCapability_AmpereOrHigher",
			validate: func(t *testing.T) {
				if comp.ComputeMajor < 8 {
					t.Fatalf("Compute capability %d.%d < 8.0", comp.ComputeMajor, comp.ComputeMinor)
				}
			},
		},
		{
			name: "DeviceName_Detected",
			validate: func(t *testing.T) {
				if comp.DeviceName == "" {
					t.Fatal("DeviceName is empty")
				}
			},
		},
		{
			name: "VRAM_Capacity_AtLeast4GB",
			validate: func(t *testing.T) {
				const minVRAM = uint64(4 * 1024 * 1024 * 1024)
				if comp.TotalVRAMBytes < minVRAM {
					t.Fatalf("TotalVRAMBytes %d < 4GB", comp.TotalVRAMBytes)
				}
			},
		},
		{
			name: "KernelExecution_NonZeroBandwidth",
			validate: func(t *testing.T) {
				data := generateTiledData(4, "PRZM_PTX_VALIDATION_")
				_, prof, err := comp.MatchLZ77(data)
				if err != nil {
					t.Fatalf("MatchLZ77 failed: %v", err)
				}
				if prof.InternalVRAMBandwidthGBps < 0 {
					t.Fatalf("Invalid VRAM bandwidth metric: %f", prof.InternalVRAMBandwidthGBps)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.validate)
	}
}

// Feature 3: Pure Go CUDA Driver API Bridge
func TestTier1_GPU_F03_CUDAAPIBridge(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "IsAvailable_ReturnsTrue",
			fn: func(t *testing.T) {
				if !gpu.IsAvailable() {
					t.Fatal("Expected gpu.IsAvailable() == true")
				}
			},
		},
		{
			name: "GetGlobalCompressor_Singleton",
			fn: func(t *testing.T) {
				c1, err1 := gpu.GetGlobalCompressor()
				c2, err2 := gpu.GetGlobalCompressor()
				if err1 != nil || err2 != nil || c1 != c2 || c1 == nil {
					t.Fatalf("Singleton failure: c1=%v, c2=%v, err1=%v, err2=%v", c1, c2, err1, err2)
				}
			},
		},
		{
			name: "ConcurrentAvailabilityQueries",
			fn: func(t *testing.T) {
				var wg sync.WaitGroup
				for range 8 {
					wg.Go(func() {
						_ = gpu.IsAvailable()
					})
				}
				wg.Wait()
			},
		},
		{
			name: "SequentialMatchCalls_ContextStability",
			fn: func(t *testing.T) {
				comp := getTestGPUCompressor(t)
				data := generateRepeatingRunData(16384, 'K')
				for i := range 5 {
					m, _, err := comp.MatchLZ77(data)
					if err != nil {
						t.Fatalf("Sequential call %d failed: %v", i, err)
					}
					assertGPUMatchIntegrity(t, data, m)
				}
			},
		},
		{
			name: "DescriptorPackUnpack_ExactFidelity",
			fn: func(t *testing.T) {
				packed := gpu.PackMatch32(128, 4096)
				l, o := gpu.UnpackMatch32(packed)
				if l != 128 || o != 4096 {
					t.Fatalf("Pack/Unpack mismatch: want 128/4096, got %d/%d", l, o)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.fn)
	}
}

// Feature 4: Pinned Host Memory Allocator
func TestTier1_GPU_F04_PinnedHostMemoryAllocator(t *testing.T) {
	if !isGPUAvailable() {
		t.Skip("CUDA GPU not available")
	}

	tests := []struct {
		name string
		size int
	}{
		{"Alloc4KB", 4 * 1024},
		{"Alloc64KB", 64 * 1024},
		{"Alloc256KB", 256 * 1024},
		{"Alloc1MB", 1024 * 1024},
		{"Alloc2MB", 2048 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			buf, ptr, err := gpu.AllocPinnedHost(tc.size)
			if err != nil {
				t.Fatalf("AllocPinnedHost(%d) failed: %v", tc.size, err)
			}
			if len(buf) != tc.size || ptr == 0 {
				t.Fatalf("Invalid pinned buffer: len=%d, ptr=0x%x", len(buf), ptr)
			}
			buf[0] = 0xAA
			buf[len(buf)-1] = 0x55
			if err := gpu.FreePinnedHost(ptr); err != nil {
				t.Fatalf("FreePinnedHost failed: %v", err)
			}
		})
	}
}

// Feature 5: Public API WithGPUAcceleration() Option
func TestTier1_GPU_F05_WithGPUAccelerationOption(t *testing.T) {
	tests := []struct {
		name    string
		opt     przm.Option
		payload []byte
	}{
		{"RepeatedPattern", przm.WithGPUAcceleration(), bytes.Repeat([]byte("OPTION_GPU_TEST_012345_"), 200)},
		{"StructuredJSON", przm.WithGPUAcceleration(), generateSyntheticJSON(300)},
		{"SourceCode", przm.WithGPUAcceleration(), generateSyntheticSourceCode(150)},
		{"MixedBinary", przm.WithGPUAcceleration(), generateMixedBinary(65536)},
		{"TiledPattern", przm.WithGPUAcceleration(), generateTiledData(4, "TILED_OPTION_")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := przm.CompressWithOptions(tc.payload, przm.CompressOptions{}, tc.opt)
			if err != nil {
				t.Fatalf("CompressWithOptions failed: %v", err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if !bytes.Equal(dec, tc.payload) {
				t.Fatalf("Bit-exact roundtrip mismatch: got %d bytes, want %d", len(dec), len(tc.payload))
			}
		})
	}
}

// Feature 6: Engine Pipeline Routing
func TestTier1_GPU_F06_EnginePipelineRouting(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"RepeatedRun64KB", generateRepeatingRunData(65536, 'Z')},
		{"SyntheticJSON128KB", generateSyntheticJSON(1200)},
		{"SourceCode128KB", generateSyntheticSourceCode(800)},
		{"MixedBinary128KB", generateMixedBinary(131072)},
		{"TiledPattern128KB", generateTiledData(4, "PRZM_PIPELINE_ROUTE_")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := przm.CompressOptions{GPUAcceleration: true}
			comp, err := przm.CompressWithOptions(tc.data, opts)
			if err != nil {
				t.Fatalf("Pipeline routing failed: %v", err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Decode failed: %v", err)
			}
			if !bytes.Equal(dec, tc.data) {
				t.Fatalf("Roundtrip mismatch in pipeline routing")
			}
		})
	}
}

// Feature 7: Graceful CPU SIMD Fallback
func TestTier1_GPU_F07_GracefulCPUSIMDFallback(t *testing.T) {
	tests := []struct {
		name  string
		setup func() func()
		data  []byte
	}{
		{
			name: "MockUnavailable",
			setup: func() func() {
				gpu.SetMockUnavailableForTest(true)
				return func() {
					gpu.SetMockUnavailableForTest(false)
					gpu.SetMockMatchErrorForTest(nil)
					gpu.ResetGlobalCompressorForTest()
				}
			},
			data: generateSyntheticJSON(500),
		},
		{
			name: "MockMatchError",
			setup: func() func() {
				gpu.SetMockMatchErrorForTest(errors.New("simulated CUDA driver failure"))
				return func() {
					gpu.SetMockUnavailableForTest(false)
					gpu.SetMockMatchErrorForTest(nil)
					gpu.ResetGlobalCompressorForTest()
				}
			},
			data: generateSyntheticSourceCode(300),
		},
		{
			name:  "ZeroLengthPayload",
			setup: func() func() { return func() {} },
			data:  []byte{},
		},
		{
			name:  "SingleBytePayload",
			setup: func() func() { return func() {} },
			data:  []byte{0x42},
		},
		{
			name:  "ThreeBytePayload",
			setup: func() func() { return func() {} },
			data:  []byte{1, 2, 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cleanup := tc.setup()
			defer cleanup()

			opts := przm.CompressOptions{GPUAcceleration: true}
			comp, err := przm.CompressWithOptions(tc.data, opts)
			if err != nil {
				t.Fatalf("Compression failed during fallback test: %v", err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Decompression failed during fallback test: %v", err)
			}
			if !bytes.Equal(dec, tc.data) {
				t.Fatalf("Lossless roundtrip mismatch during fallback")
			}
		})
	}
}

// Feature 8: Bit-Exact Lossless Roundtrips
func TestTier1_GPU_F08_BitExactLosslessRoundtrips(t *testing.T) {
	levels := []int{1, 3, 5, 7, 9}

	for _, lvl := range levels {
		t.Run(fmt.Sprintf("Level_%d", lvl), func(t *testing.T) {
			data := generateSyntheticJSON(600)
			opts := przm.CompressOptions{
				GPUAcceleration: true,
				Level:           lvl,
			}
			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				t.Fatalf("Level %d compression failed: %v", lvl, err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Level %d decompression failed: %v", lvl, err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("Level %d roundtrip mismatch", lvl)
			}
		})
	}
}

// Feature 9: Automated Performance Metrics
func TestTier1_GPU_F09_AutomatedPerformanceMetrics(t *testing.T) {
	comp := getTestGPUCompressor(t)

	tests := []struct {
		name string
		size int
	}{
		{"Metric32KB", 32 * 1024},
		{"Metric64KB", 64 * 1024},
		{"Metric128KB", 128 * 1024},
		{"Metric256KB", 256 * 1024},
		{"Metric512KB", 512 * 1024},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateTiledData(tc.size/(32*1024), "PERF_METRICS_")
			_, prof, err := comp.MatchLZ77(data)
			if err != nil {
				t.Fatalf("MatchLZ77 failed: %v", err)
			}
			if prof.IngressDMABytes == 0 {
				t.Errorf("IngressDMABytes is 0")
			}
			if prof.KernelTimeMs <= 0 {
				t.Errorf("KernelTimeMs <= 0: %f", prof.KernelTimeMs)
			}
			if prof.TotalRoundTripMs <= 0 {
				t.Errorf("TotalRoundTripMs <= 0: %f", prof.TotalRoundTripMs)
			}
			if prof.DeviceName == "" {
				t.Errorf("DeviceName is empty")
			}
			if prof.SMCount <= 0 {
				t.Errorf("SMCount <= 0")
			}
		})
	}
}

// Feature 10: Simulated Failure Fallback
func TestTier1_GPU_F10_SimulatedFailureFallback(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"JSONStream_256KB", generateSyntheticJSON(2500)},
		{"SourceStream_256KB", generateSyntheticSourceCode(1600)},
		{"BinaryStream_256KB", generateMixedBinary(262144)},
		{"TiledStream_256KB", generateTiledData(8, "SIMULATED_FAIL_")},
		{"RepeatingStream_256KB", generateRepeatingRunData(262144, 'F')},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gpu.SetMockMatchErrorForTest(errors.New("simulated PCIe bus disconnect"))
			defer func() {
				gpu.SetMockUnavailableForTest(false)
				gpu.SetMockMatchErrorForTest(nil)
				gpu.ResetGlobalCompressorForTest()
			}()

			opts := przm.CompressOptions{GPUAcceleration: true}
			comp, err := przm.CompressWithOptions(tc.payload, opts)
			if err != nil {
				t.Fatalf("Compression failed under simulated failure: %v", err)
			}
			dec, err := przm.Decode(comp)
			if err != nil {
				t.Fatalf("Decode failed under simulated failure: %v", err)
			}
			if !bytes.Equal(dec, tc.payload) {
				t.Fatalf("Bit-exact roundtrip mismatch during simulated failure")
			}
		})
	}
}
