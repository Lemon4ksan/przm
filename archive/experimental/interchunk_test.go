// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// TestInterChunk_Theorem4_LosslessRoundtrip verifies Theorem 4 (Exact Reversibility):
// continuous stream of chunks with grammar state carry restores 100% byte-for-byte.
func TestInterChunk_Theorem4_LosslessRoundtrip(t *testing.T) {
	// Generate a stream of 8 consecutive JSON chunks (16 KiB each = 128 KiB continuous stream)
	numChunks := 8
	chunkSize := 16 * 1024
	chunks := make([][]byte, numChunks)
	for i := range numChunks {
		chunks[i] = bench.GenerateJSON(chunkSize)
	}

	frames, err := przm.EncodeChunkStream(chunks)
	if err != nil {
		t.Fatalf("EncodeChunkStream failed: %v", err)
	}

	decoded, err := przm.DecodeChunkStream(frames)
	if err != nil {
		t.Fatalf("DecodeChunkStream failed: %v", err)
	}

	if len(decoded) != len(chunks) {
		t.Fatalf("chunk count mismatch: got %d, want %d", len(decoded), len(chunks))
	}

	for i := range numChunks {
		if !bytes.Equal(decoded[i], chunks[i]) {
			t.Fatalf("Theorem 4 violation on chunk %d: mismatch with original data", i)
		}
	}
}

// TestInterChunk_Theorem5_HeaderConvergence verifies Theorem 5 (Asymptotic Header Convergence):
// after warm-up on chunk 0, subsequent chunks inherit grammar tables with 0 rule definitions transmitted.
func TestInterChunk_Theorem5_HeaderConvergence(t *testing.T) {
	numChunks := 10
	chunkSize := 16 * 1024
	chunks := make([][]byte, numChunks)
	for i := range numChunks {
		chunks[i] = bench.GenerateJSON(chunkSize)
	}

	// 1. Isolated compression (baseline without state carry)
	isolatedTotal := 0
	for _, chunk := range chunks {
		enc, err := przm.GGTCEncode(chunk)
		if err != nil {
			t.Fatal(err)
		}
		isolatedTotal += len(enc)
	}

	// 2. Inter-chunk state carry compression (T_CSI)
	frames, err := przm.EncodeChunkStream(chunks)
	if err != nil {
		t.Fatal(err)
	}

	streamTotal := 0
	inheritedCount := 0
	for i, frame := range frames {
		streamTotal += len(frame)
		isInherited := (frame[3] & przm.FlagChunkInherit) != 0
		if isInherited {
			inheritedCount++
		}
		t.Logf("Chunk %2d: Size = %6d bytes | Inherited = %t", i, len(frame), isInherited)
	}

	t.Logf("\n=== Inter-Chunk State Carry (T_CSI) Convergence Results ===")
	t.Logf("Total Raw Size:          %d bytes (%d chunks of %d KiB)", numChunks*chunkSize, numChunks, chunkSize/1024)
	t.Logf("Isolated Block Size:     %d bytes (Ratio: %.2fx)", isolatedTotal, float64(numChunks*chunkSize)/float64(isolatedTotal))
	t.Logf("Inter-Chunk T_CSI Size:  %d bytes (Ratio: %.2fx)", streamTotal, float64(numChunks*chunkSize)/float64(streamTotal))
	t.Logf("Header Savings:          %d bytes eliminated from stream", isolatedTotal-streamTotal)
	t.Logf("Inherited Chunks:        %d / %d (%.1f%% convergence)", inheritedCount, numChunks, float64(inheritedCount)/float64(numChunks)*100)

	if inheritedCount < 8 {
		t.Errorf("expected at least 8 chunks to inherit grammar, got %d", inheritedCount)
	}

	if streamTotal >= isolatedTotal {
		t.Errorf("expected inter-chunk compression to outperform isolated block compression")
	}
}

func BenchmarkInterChunk_Throughput(b *testing.B) {
	chunkSize := 64 * 1024
	chunk0 := bench.GenerateJSON(chunkSize)
	chunk1 := bench.GenerateJSON(chunkSize)

	frames, _ := przm.EncodeChunkStream([][]byte{chunk0, chunk1})
	inheritedFrame := frames[1]

	b.Run("Decode_Inherited_JSON_64K", func(b *testing.B) {
		b.SetBytes(int64(chunkSize))
		var ctx przm.InterChunkContext
		// Warm up context with chunk 0
		_, err := przm.DecodeChunkStream([][]byte{frames[0]})
		if err != nil {
			b.Fatal(err)
		}
		// Extract grammar for standalone decode benchmark
		_, g, _ := przm.DecodeFullFrameForBench(frames[0])
		ctx.Grammar = *g
		ctx.HasGrammar = true

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.DecodeInheritedFrameForBench(inheritedFrame, &ctx.Grammar)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
