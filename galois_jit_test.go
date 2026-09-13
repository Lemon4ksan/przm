// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestApplyGCTRRoundtrip(t *testing.T) {
	operators := []uint8{
		OpGCTRBypass,
		OpGCTXORDelta1,
		OpGCTXORDelta2,
		OpGCTXORDelta4,
		OpGCTXORDelta8,
		OpGCTZModDelta1,
		OpGCTZModDelta4,
		OpGCTZModSecondOrder,
	}

	lengths := []int{1, 2, 3, 4, 7, 8, 15, 16, 64, 255, 256, 1024, 65536}

	for _, op := range operators {
		for _, n := range lengths {
			src := make([]byte, n)
			for i := range src {
				src[i] = byte(i*7 + 13) // arithmetic progression
			}

			fwd := ApplyGCTRForward(src, nil, op)
			inv, err := ApplyGCTRInverse(fwd, nil, op)
			if err != nil {
				t.Fatalf("op %d len %d: inverse returned error: %v", op, n, err)
			}
			if !bytes.Equal(src, inv) {
				t.Fatalf("op %d len %d (arithmetic): roundtrip mismatch", op, n)
			}

			// Random data
			rng := rand.New(rand.NewSource(int64(op)*1000 + int64(n)))
			rng.Read(src)

			fwd = ApplyGCTRForward(src, nil, op)
			inv, err = ApplyGCTRInverse(fwd, nil, op)
			if err != nil {
				t.Fatalf("op %d len %d (random): inverse returned error: %v", op, n, err)
			}
			if !bytes.Equal(src, inv) {
				t.Fatalf("op %d len %d (random): roundtrip mismatch", op, n)
			}
		}
	}
}

func TestGCTRSuperoptimizeOperator(t *testing.T) {
	// Test 1: Arithmetic sequence with stride (high entropy raw, zero entropy delta)
	srcArith := make([]byte, 1024)
	for i := range srcArith {
		srcArith[i] = byte(i * 3)
	}
	op1 := SuperoptimizeOperator(srcArith)
	if op1.OpID != OpGCTZModDelta1 {
		t.Errorf("expected OpGCTZModDelta1 for arithmetic sequence, got %d", op1.OpID)
	}

	// Test 2: 32-bit stride pattern (repeated 4-byte instruction words)
	src32 := make([]byte, 1024)
	pattern32 := []byte{0x48, 0x89, 0xe5, 0x90}
	for i := range src32 {
		src32[i] = pattern32[i%4]
	}
	op32 := SuperoptimizeOperator(src32)
	if op32.OpID != OpGCTXORDelta4 && op32.OpID != OpGCTZModDelta4 {
		t.Errorf("expected 4-byte stride operator, got %d", op32.OpID)
	}

	// Test 3: 64-bit stride pattern (repeated 8-byte pointer sequence)
	src64 := make([]byte, 1024)
	pattern64 := []byte{0x00, 0x10, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00}
	for i := range src64 {
		src64[i] = pattern64[i%8]
	}
	op64 := SuperoptimizeOperator(src64)
	if op64.OpID != OpGCTXORDelta8 && op64.OpID != OpGCTXORDelta4 && op64.OpID != OpGCTXORDelta2 {
		t.Errorf("expected stride operator, got %d", op64.OpID)
	}

	// Test 4: Random noise (should bypass)
	srcRand := make([]byte, 1024)
	rand.New(rand.NewSource(42)).Read(srcRand)
	opRand := SuperoptimizeOperator(srcRand)
	if opRand.OpID != OpGCTRBypass {
		t.Errorf("expected OpGCTRBypass for pure white noise, got %d (gain: %f)", opRand.OpID, opRand.Gain)
	}
}

func TestCompressDecompressGCTR(t *testing.T) {
	// Structured sequence of 64K
	src := make([]byte, 65536)
	pattern := []byte{0x48, 0x83, 0xec, 0x28, 0xe8, 0x00, 0x00, 0x00}
	for i := range src {
		src[i] = pattern[i%8] ^ byte(i/8)
	}

	compressed := CompressGCTR(src)
	if compressed == nil {
		t.Fatalf("CompressGCTR failed to compress structured sequence")
	}

	if len(compressed) >= len(src) {
		t.Fatalf("compressed size %d >= original %d", len(compressed), len(src))
	}

	decompressed, err := DecompressGCTR(compressed, nil)
	if err != nil {
		t.Fatalf("DecompressGCTR returned error: %v", err)
	}

	if !bytes.Equal(src, decompressed) {
		t.Fatalf("DecompressGCTR roundtrip mismatch: len %d vs %d", len(decompressed), len(src))
	}
}

func BenchmarkSuperoptimizeOperator(b *testing.B) {
	src := make([]byte, 65536)
	for i := range src {
		src[i] = byte(i % 16)
	}
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = SuperoptimizeOperator(src)
	}
}

func BenchmarkApplyGCTRForward(b *testing.B) {
	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = ApplyGCTRForward(src, dst, OpGCTXORDelta4)
	}
}

func BenchmarkApplyGCTRInverse(b *testing.B) {
	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = ApplyGCTRInverse(src, dst, OpGCTXORDelta4)
	}
}

func BenchmarkApplyGCTRForward_Delta8(b *testing.B) {
	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = ApplyGCTRForward(src, dst, OpGCTXORDelta8)
	}
}

func BenchmarkApplyGCTRInverse_Delta8(b *testing.B) {
	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = ApplyGCTRInverse(src, dst, OpGCTXORDelta8)
	}
}
