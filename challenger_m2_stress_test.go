// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
)

func TestChallenger_MT_ConcurrencyAndThreadScaling(t *testing.T) {
	const payloadSize = 1024 * 1024 // 1 MiB
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte(i*37 + (i>>4)*19)
	}

	threadCounts := []int{1, 2, 4, 7, 16, 32, 64, 128}
	const chunkSize = 32 * 1024 // 32 chunks

	for _, th := range threadCounts {
		th := th
		t.Run(fmt.Sprintf("Threads_%d", th), func(t *testing.T) {
			opts := CompressOptions{
				Threads:   th,
				ChunkSize: chunkSize,
				Level:     3,
			}
			comp, err := CompressWithOptions(payload, opts)
			if err != nil {
				t.Fatalf("CompressWithOptions(threads=%d) failed: %v", th, err)
			}

			// When th > 1 and len(payload) > chunkSize, it must produce PRZS
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if th > 1 {
				if magic != MagicPRZS {
					t.Fatalf("expected MagicPRZS for threads=%d, got 0x%08X", th, magic)
				}
			}

			// Verify Decompress bit-exact
			dec, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(dec, payload) {
				t.Fatalf("Decompress payload mismatch for threads=%d", th)
			}

			// Verify DecompressTo bit-exact and 0.00 allocs
			dst := bufkit.AlignedBytes(len(payload)+64, bufkit.CacheLineSize)
			decTo, err := DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decTo, payload) {
				t.Fatalf("DecompressTo payload mismatch for threads=%d", th)
			}

			if !RaceEnabled {
				allocs := testing.AllocsPerRun(100, func() {
					out, err := DecompressTo(comp, dst)
					if err != nil {
						t.Fatalf("DecompressTo failed during alloc test: %v", err)
					}
					_ = out
				})
				if allocs != 0.0 {
					t.Fatalf("ZERO-ALLOC VIOLATION on threads=%d: %.2f allocs/run (expected 0.00)", th, allocs)
				}
			}
		})
	}
}

func TestChallenger_MT_IrregularAndBoundaryChunkSizes(t *testing.T) {
	const payloadSize = 1000007 // prime size
	payload := make([]byte, payloadSize)
	rng := rand.New(rand.NewSource(999999))
	rng.Read(payload)

	const chunkSize = 31337 // prime chunk size -> 32 chunks
	expectedChunks := uint32((payloadSize + chunkSize - 1) / chunkSize)

	levels := []int{1, 2, 3, 4, 5}
	for _, lvl := range levels {
		lvl := lvl
		t.Run(fmt.Sprintf("Level_%d", lvl), func(t *testing.T) {
			opts := CompressOptions{
				Threads:   8,
				ChunkSize: chunkSize,
				Level:     lvl,
			}
			comp, err := CompressWithOptions(payload, opts)
			if err != nil {
				t.Fatalf("CompressWithOptions(level=%d) failed: %v", lvl, err)
			}

			hdr, err := ParseStreamHeader(comp)
			if err != nil {
				t.Fatalf("ParseStreamHeader failed: %v", err)
			}
			if hdr.ChunkCount != expectedChunks {
				t.Fatalf("expected %d chunks, got %d", expectedChunks, hdr.ChunkCount)
			}
			if hdr.TotalUncompressedSize != uint64(payloadSize) {
				t.Fatalf("expected uncompressed size %d, got %d", payloadSize, hdr.TotalUncompressedSize)
			}

			dec, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(dec, payload) {
				t.Fatalf("bit-exact mismatch for level %d", lvl)
			}

			dst := bufkit.AlignedBytes(payloadSize+64, bufkit.CacheLineSize)
			if !RaceEnabled {
				allocs := testing.AllocsPerRun(100, func() {
					_, err := DecompressTo(comp, dst)
					if err != nil {
						t.Fatalf("DecompressTo failed: %v", err)
					}
				})
				if allocs != 0.0 {
					t.Fatalf("ZERO-ALLOC VIOLATION on level=%d: %.2f allocs/run", lvl, allocs)
				}
			}
		})
	}
}

func TestChallenger_MT_ChunkSizeEqualAndOneOff(t *testing.T) {
	const baseSize = 65536
	payload := make([]byte, baseSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	testCases := []struct {
		name         string
		chunkSize    int
		expectStream bool // true => MagicPRZS, false => single-frame
	}{
		{
			name:         "PayloadEqualsChunkSize",
			chunkSize:    65536,
			expectStream: false, // len(src) <= chunkSize -> single frame
		},
		{
			name:         "PayloadGreaterThanChunkSizeBy1",
			chunkSize:    65535,
			expectStream: true, // len(src) > chunkSize -> 2 chunks (PRZS)
		},
		{
			name:         "PayloadSmallerThanChunkSizeBy1",
			chunkSize:    65537,
			expectStream: false, // len(src) <= chunkSize -> single frame
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := CompressOptions{
				Threads:   4,
				ChunkSize: tc.chunkSize,
				Level:     3,
			}
			comp, err := CompressWithOptions(payload, opts)
			if err != nil {
				t.Fatalf("CompressWithOptions failed: %v", err)
			}
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if tc.expectStream {
				if magic != MagicPRZS {
					t.Fatalf("expected MagicPRZS (0x%08X), got 0x%08X", MagicPRZS, magic)
				}
			} else {
				if magic == MagicPRZS {
					t.Fatalf("expected single-frame (not MagicPRZS), got MagicPRZS (0x%08X)", magic)
				}
			}

			dec, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(dec, payload) {
				t.Fatalf("roundtrip mismatch")
			}
		})
	}
}

func TestChallenger_MT_ZeroAndSingleBytePayloads(t *testing.T) {
	// Zero bytes
	zeroPayload := []byte{}
	opts := CompressOptions{
		Threads:   4,
		ChunkSize: 1024,
		Level:     3,
	}
	compZero, err := CompressWithOptions(zeroPayload, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions(zero) failed: %v", err)
	}
	decZero, err := Decompress(compZero)
	if err != nil {
		t.Fatalf("Decompress(zero) failed: %v", err)
	}
	if len(decZero) != 0 {
		t.Fatalf("expected 0 bytes, got %d", len(decZero))
	}

	// Single byte
	onePayload := []byte{0x7F}
	compOne, err := CompressWithOptions(onePayload, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions(one) failed: %v", err)
	}
	decOne, err := Decompress(compOne)
	if err != nil {
		t.Fatalf("Decompress(one) failed: %v", err)
	}
	if !bytes.Equal(decOne, onePayload) {
		t.Fatalf("single byte mismatch")
	}
}

func TestChallenger_MT_BoundaryErrors(t *testing.T) {
	dummy := make([]byte, 1024)

	// Direct compressMultiChunk with ChunkSize > MaxBlockSize returns ErrExceedsMaxBlock
	_, err := compressMultiChunk(dummy, CompressOptions{
		Threads:   4,
		ChunkSize: MaxBlockSize + 1,
	})
	if !errors.Is(err, ErrExceedsMaxBlock) {
		t.Fatalf("expected ErrExceedsMaxBlock for compressMultiChunk ChunkSize > MaxBlockSize, got: %v", err)
	}

	// StandardStreamOnly with len > MaxBlockSize returns ErrExceedsMaxBlock
	oversized := make([]byte, MaxBlockSize+1)
	_, err = CompressWithOptions(oversized, CompressOptions{
		StandardStreamOnly: true,
	})
	if !errors.Is(err, ErrExceedsMaxBlock) {
		t.Fatalf("expected ErrExceedsMaxBlock for oversized StandardStreamOnly, got: %v", err)
	}

	// Oversized payload with ChunkSize > MaxBlockSize triggers compressMultiChunk -> ErrExceedsMaxBlock
	_, err = CompressWithOptions(oversized, CompressOptions{
		Threads:   4,
		ChunkSize: MaxBlockSize + 1,
	})
	if !errors.Is(err, ErrExceedsMaxBlock) {
		t.Fatalf("expected ErrExceedsMaxBlock for oversized CompressWithOptions with ChunkSize > MaxBlockSize, got: %v", err)
	}
}

func TestChallenger_MT_PureCInteropWithMultiThreadOutput(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", "c/test_przm_dec.exe"}
	var cBinPath string
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				cBinPath = absPath
				break
			}
		}
	}
	if cBinPath == "" {
		t.Skip("C test binary test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()
	payloadSize := 512 * 1024
	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = byte((i*41 + 13) & 0xFF)
	}

	opts := CompressOptions{
		Threads:   8,
		ChunkSize: 64 * 1024,
		Level:     3,
	}

	comp, err := CompressWithOptions(payload, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions failed: %v", err)
	}

	inPrzs := filepath.Join(tmpDir, "mt_test.przs")
	outBin := filepath.Join(tmpDir, "mt_test.out")
	if err := os.WriteFile(inPrzs, comp, 0644); err != nil {
		t.Fatalf("failed to write PRZS: %v", err)
	}

	cmd := exec.Command(cBinPath, inPrzs, outBin)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Pure C decompressor failed on MT compressed PRZS (exit status %v):\nOutput:\n%s", err, string(output))
	}

	decompressed, err := os.ReadFile(outBin)
	if err != nil {
		t.Fatalf("failed to read C decompressed file: %v", err)
	}

	if !bytes.Equal(decompressed, payload) {
		t.Fatalf("Pure C decompressed output mismatch (got %d bytes, want %d bytes)", len(decompressed), len(payload))
	}
}
