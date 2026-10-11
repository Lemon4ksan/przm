package gpu

import (
	"bytes"
	"strings"
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
	for trial := 0; trial < 10; trial++ {
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
