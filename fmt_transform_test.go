// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestMobiusInvolution(t *testing.T) {
	lengths := []int{64, 128, 256, 512, 1024, 4096, 77, 100, 333, 1025}
	for _, n := range lengths {
		data := make([]byte, n)
		orig := make([]byte, n)
		for i := range data {
			data[i] = byte(i*37 + 13)
			orig[i] = data[i]
		}

		// Test Byte Butterfly Involution
		FastMobiusTransformInplace(data, 6, false)
		if n >= 64 && bytes.Equal(data, orig) {
			t.Fatalf("len %d: transform did not alter data", n)
		}
		FastMobiusTransformInplace(data, 6, false)
		if !bytes.Equal(data, orig) {
			t.Fatalf("len %d: involution failed: data does not match original", n)
		}

		// Test with intra-byte bit butterfly
		FastMobiusTransformInplace(data, 6, true)
		FastMobiusTransformInplace(data, 6, true)
		if !bytes.Equal(data, orig) {
			t.Fatalf("len %d: bit-level involution failed", n)
		}
	}
}

func TestMobiusVectorRegister64(t *testing.T) {
	var reg, orig [64]byte
	for i := range reg {
		reg[i] = byte(i ^ 0xAA)
		orig[i] = reg[i]
	}

	FastMobiusTransform64Inplace(&reg)
	if reg == orig {
		t.Fatal("FastMobiusTransform64Inplace did not mutate register")
	}
	FastMobiusTransform64Inplace(&reg)
	if reg != orig {
		t.Fatal("FastMobiusTransform64Inplace involution failed")
	}
}

func TestMobiusCodecRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"Tiny", []byte("short")},
		{"Aligned64", bytes.Repeat([]byte("0123456789abcdef"), 4)},
		{"PseudoRandom1K", func() []byte {
			r := rand.New(rand.NewSource(42))
			buf := make([]byte, 1024)
			r.Read(buf)
			return buf
		}()},
		{"Repetitive16K", bytes.Repeat([]byte("PRZM_MOBIUS_FAST_TRANSFORM_INVOLUTION_BLOCK_"), 400)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := CompressMobius(tt.data, 2)
			if err != nil {
				t.Fatalf("CompressMobius error: %v", err)
			}

			dst := make([]byte, len(tt.data)+64)
			decompressed, err := DecompressMobiusTo(compressed, dst)
			if err != nil {
				t.Fatalf("DecompressMobiusTo error: %v", err)
			}

			if !bytes.Equal(decompressed, tt.data) {
				t.Fatalf("roundtrip mismatch: got len %d, want len %d", len(decompressed), len(tt.data))
			}
		})
	}
}

func TestDecompressMobius_ZeroAlloc(t *testing.T) {
	src := bytes.Repeat([]byte("FAST_MOBIUS_TRANSFORM_ZERO_ALLOCATION_TEST_"), 30)
	compressed, err := CompressMobius(src, 2)
	if err != nil {
		t.Fatalf("CompressMobius error: %v", err)
	}

	dst := make([]byte, len(src)+64)
	allocs := testing.AllocsPerRun(100, func() {
		_, err := DecompressMobiusTo(compressed, dst)
		if err != nil {
			t.Fatalf("Decompress error: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("DecompressMobiusTo allocated %f allocs/op; want strictly 0", allocs)
	}
}

func BenchmarkMobiusTransform(b *testing.B) {
	data := make([]byte, 64*1024)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()

	for b.Loop() {
		FastMobiusTransformInplace(data, 6, false)
	}
}

func BenchmarkMobiusDecompress(b *testing.B) {
	src := bytes.Repeat([]byte("BENCHMARK_MOBIUS_DECOMPRESS_PAYLOAD_VERIFICATION_"), 100)
	compressed, err := CompressMobius(src, 2)
	if err != nil {
		b.Fatalf("Compress error: %v", err)
	}

	dst := make([]byte, len(src)+64)
	b.SetBytes(int64(len(src)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = DecompressMobiusTo(compressed, dst)
	}
}
