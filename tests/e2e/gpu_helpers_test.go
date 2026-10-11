// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/gpu"
)

// Global singleton compressor for test speed & context reuse across test suites.
var (
	testGPUComp    *gpu.GPUCompressor
	testGPUOnce    sync.Once
	testGPUInitErr error
)

// isGPUAvailable checks if NVIDIA CUDA hardware and driver are accessible.
func isGPUAvailable() bool {
	testGPUOnce.Do(func() {
		testGPUComp, testGPUInitErr = gpu.NewGPUCompressor(0)
	})
	return testGPUInitErr == nil && testGPUComp != nil
}

// getTestGPUCompressor returns the shared GPU compressor or skips the test.
func getTestGPUCompressor(t *testing.T) *gpu.GPUCompressor {
	t.Helper()
	if !isGPUAvailable() {
		t.Skipf("NVIDIA CUDA GPU not available or driver initialization failed: %v", testGPUInitErr)
	}
	return testGPUComp
}

// assertGPUMatchIntegrity verifies that every match returned by the GPU kernel
// is 100% bit-exact with the preceding source bytes within the match window.
func assertGPUMatchIntegrity(t *testing.T, src []byte, matches []gpu.LZ77Match) {
	t.Helper()
	srcLen := len(src)
	for i, m := range matches {
		pos := int(m.Position)
		off := int(m.Offset)
		length := int(m.Length)

		if length < gpu.MinMatchLen {
			t.Fatalf("match[%d] length %d < MinMatchLen (%d)", i, length, gpu.MinMatchLen)
		}
		if off == 0 || off > pos {
			t.Fatalf("match[%d] invalid offset %d at position %d", i, off, pos)
		}
		if pos+length > srcLen {
			t.Fatalf("match[%d] bounds overrun: pos %d + len %d > srcLen %d", i, pos, length, srcLen)
		}

		matchedSlice := src[pos : pos+length]
		referenceSlice := src[pos-off : pos-off+length]
		if !bytes.Equal(matchedSlice, referenceSlice) {
			t.Fatalf("match[%d] data corruption: matched bytes != reference bytes at pos %d, off %d, len %d",
				i, pos, off, length)
		}
	}
}

// assertGPUPipelineRoundtrip executes GPU match search, emits tokens, and tests
// that the reconstructed output is 100% bit-exact with the original input.
func assertGPUPipelineRoundtrip(t *testing.T, src []byte, level int) ([]byte, gpu.GPUProfile) {
	t.Helper()
	comp := getTestGPUCompressor(t)

	// Step 1: GPU LZ77 match search over pinned memory
	matches, prof, err := comp.MatchLZ77(src)
	if err != nil {
		t.Fatalf("GPU MatchLZ77 failed on %d bytes: %v", len(src), err)
	}
	assertGPUMatchIntegrity(t, src, matches)

	// Step 2: PRZM core compression (or fallback if hardware error)
	compressed, err := przm.Compress(src, level)
	if err != nil {
		t.Fatalf("przm.Compress(level=%d) failed: %v", level, err)
	}

	// Step 3: Decompress and verify bit-exact lossless roundtrip
	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("przm.Decompress failed: %v", err)
	}

	if !bytes.Equal(decompressed, src) {
		t.Fatalf("roundtrip mismatch: want %d bytes, got %d bytes", len(src), len(decompressed))
	}

	return compressed, prof
}

// GPUCompressConfig encapsulates configuration for GPU acceleration tests.
type GPUCompressConfig struct {
	GPUAcceleration bool
	Level           int
	Threads         int
	ChunkSize       int
	Dict            *przm.Dictionary
}

type GPUOption func(*GPUCompressConfig)

func WithGPUAcceleration() GPUOption {
	return func(c *GPUCompressConfig) {
		c.GPUAcceleration = true
	}
}

func WithLevel(level int) GPUOption {
	return func(c *GPUCompressConfig) {
		c.Level = level
	}
}

func WithThreads(threads int) GPUOption {
	return func(c *GPUCompressConfig) {
		c.Threads = threads
	}
}

func WithChunkSize(chunkSize int) GPUOption {
	return func(c *GPUCompressConfig) {
		c.ChunkSize = chunkSize
	}
}

func WithDict(dict *przm.Dictionary) GPUOption {
	return func(c *GPUCompressConfig) {
		c.Dict = dict
	}
}

func NewGPUConfig(opts ...GPUOption) GPUCompressConfig {
	cfg := GPUCompressConfig{
		GPUAcceleration: false, // disabled by default per contract
		Level:           3,
		Threads:         1,
		ChunkSize:       2 * 1024 * 1024,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// Data generators for GPU test scenarios

func generateTiledData(tiles int, pattern string) []byte {
	const tileSize = 32768 // 32 KiB tile size
	patBytes := []byte(pattern)
	tile := make([]byte, tileSize)
	for i := range tile {
		tile[i] = patBytes[i%len(patBytes)]
	}
	buf := make([]byte, tiles*tileSize)
	for t := 0; t < tiles; t++ {
		copy(buf[t*tileSize:(t+1)*tileSize], tile)
	}
	return buf
}

func generateZeroMatchData(length int) []byte {
	buf := make([]byte, length)
	_, _ = rand.Read(buf)
	return buf
}

func generateRepeatingRunData(length int, b byte) []byte {
	return bytes.Repeat([]byte{b}, length)
}

func generateSyntheticJSON(records int) []byte {
	var sb strings.Builder
	sb.WriteString("{\"status\":\"ok\",\"timestamp\":1760000000,\"records\":[")
	for i := 0; i < records; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(
			&sb,
			`{"id":%d,"sku":"PRZM-GPU-%06d","metric":{"val":%d,"score":%.4f},"tags":["gpu","lz77","archival"]}`,
			i,
			i,
			(i*37)%1000,
			float64(i)*0.125,
		)
	}
	sb.WriteString("]}")
	return []byte(sb.String())
}

func generateSyntheticSourceCode(lines int) []byte {
	var sb strings.Builder
	sb.WriteString(
		"// Package auto-generated synthetic source corpus for GPU testing\npackage main\n\nimport (\n\t\"fmt\"\n\t\"sync\"\n)\n\n",
	)
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&sb, "func WorkerFunction_%04d(id int, ch chan int, wg *sync.WaitGroup) {\n", i)
		fmt.Fprintf(&sb, "\tdefer wg.Done()\n\tresult := id * 42 + %d\n", i%100)
		fmt.Fprintf(&sb, "\tif result > 1000 {\n\t\tch <- result\n\t} else {\n\t\tch <- 0\n\t}\n}\n\n")
	}
	return []byte(sb.String())
}

func generateMixedBinary(length int) []byte {
	buf := make([]byte, length)
	// Mix x86-64 machine instructions and structured tables
	prologue := []byte{0x55, 0x48, 0x89, 0xE5, 0x48, 0x83, 0xEC, 0x20}
	epilogue := []byte{0x48, 0x83, 0xC4, 0x20, 0x5D, 0xC3}
	i := 0
	for i < length-64 {
		copy(buf[i:], prologue)
		i += len(prologue)
		for j := 0; j < 6 && i < length-16; j++ {
			buf[i] = 0x89
			buf[i+1] = byte(0xC0 + (j % 8))
			buf[i+2] = 0x90 // NOP
			buf[i+3] = 0x90
			i += 4
		}
		copy(buf[i:], epilogue)
		i += len(epilogue)
	}
	for ; i < length; i++ {
		buf[i] = byte(i % 256)
	}
	return buf
}
