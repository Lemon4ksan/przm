// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// generateARM64Binary synthesizes realistic AArch64 machine code
// with function frames, common callees (malloc, printf, runtime.morestack),
// loops, and ALU instructions.
func generateARM64Binary(numFuncs int, callsPerFunc int) []byte {
	var buf bytes.Buffer
	targetFuncs := []uint32{0x1000, 0x1400, 0x2000, 0x2800}

	for range numFuncs {
		// Prologue: STP x29, x30, [sp, #-16]! (0xA9BF7BFD)
		//           MOV x29, sp               (0x910003FD)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0xA9BF7BFD))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0x910003FD))

		for c := range callsPerFunc {
			// Some ALU work: ADD x0, x0, #1 (0x91000400)
			_ = binary.Write(&buf, binary.LittleEndian, uint32(0x91000400))

			// BL instruction targeting one of the targetFuncs
			target := targetFuncs[c%len(targetFuncs)]
			pc := uint32(buf.Len())
			relOffset := (int32(target) - int32(pc)) >> 2
			blInst := uint32(0x94000000) | (uint32(relOffset) & 0x03FFFFFF)
			_ = binary.Write(&buf, binary.LittleEndian, blInst)

			// Local unconditional branch B forward 2 instructions
			bInst := uint32(0x14000002)
			_ = binary.Write(&buf, binary.LittleEndian, bInst)

			// NOP / skipped instruction
			_ = binary.Write(&buf, binary.LittleEndian, uint32(0xD503201F))
		}

		// Epilogue: LDP x29, x30, [sp], #16 (0xA8C17BFD)
		//           RET                     (0xD65F03C0)
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0xA8C17BFD))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(0xD65F03C0))
	}

	return buf.Bytes()
}

func TestARM64Filter_RoundtripBitExact(t *testing.T) {
	orig := generateARM64Binary(20, 15)
	work := make([]byte, len(orig))
	copy(work, orig)

	// Forward filter: relative branches -> collimated absolute targets
	n := przm.ARM64Filter(work, 0, true)
	if n != len(orig)&^3 {
		t.Fatalf("ARM64Filter returned %d, expected %d", n, len(orig)&^3)
	}

	// Verify that the buffer actually changed
	if bytes.Equal(work, orig) {
		t.Fatal("ARM64Filter did not modify any instructions")
	}

	// Reverse filter: collimated absolute targets -> relative branches
	przm.ARM64Filter(work, 0, false)

	// Verify 100% bit-exact roundtrip
	if !bytes.Equal(work, orig) {
		t.Fatal("ARM64Filter failed 100% bit-exact roundtrip!")
	}
}

func TestARM64Filter_TargetCollimation(t *testing.T) {
	// Synthesize code where 5 calls from different PC addresses target the exact same func (0x5000)
	var buf bytes.Buffer
	targetFunc := uint32(0x5000)

	callPCs := []int{}
	for i := range 5 {
		// Insert variable padding instructions
		for p := 0; p < i*3+2; p++ {
			_ = binary.Write(&buf, binary.LittleEndian, uint32(0x91000400))
		}
		pc := uint32(buf.Len())
		callPCs = append(callPCs, int(pc))
		rel := (int32(targetFunc) - int32(pc)) >> 2
		blInst := uint32(0x94000000) | (uint32(rel) & 0x03FFFFFF)
		_ = binary.Write(&buf, binary.LittleEndian, blInst)
	}

	raw := buf.Bytes()
	collimated := make([]byte, len(raw))
	copy(collimated, raw)

	przm.ARM64Filter(collimated, 0, true)

	// Check that all 5 calls now have the EXACT SAME 32-bit instruction word
	var firstCollimated uint32
	for idx, pc := range callPCs {
		w := binary.LittleEndian.Uint32(collimated[pc : pc+4])
		if idx == 0 {
			firstCollimated = w
		} else if w != firstCollimated {
			t.Fatalf("call site %d at pc=%d collimated to 0x%08X, expected 0x%08X", idx, pc, w, firstCollimated)
		}
	}

	// Reverse and check bit-exactness
	przm.ARM64Filter(collimated, 0, false)
	if !bytes.Equal(collimated, raw) {
		t.Fatal("Inverse filter failed to restore original branches")
	}
}

func TestDetectARM64(t *testing.T) {
	arm64Code := generateARM64Binary(10, 8)
	if !przm.DetectARM64(arm64Code) {
		t.Fatal("expected DetectARM64 to return true on synthetic ARM64 binary")
	}

	// Non-ARM64 data
	zeroes := make([]byte, 1024)
	if przm.DetectARM64(zeroes) {
		t.Fatal("DetectARM64 returned true on zeroes")
	}

	text := []byte("The quick brown fox jumps over the lazy dog. A quick test for text rejection.")
	if przm.DetectARM64(text) {
		t.Fatal("DetectARM64 returned true on text")
	}

	randomData := make([]byte, 1024)
	rng := rand.New(rand.NewSource(42))
	rng.Read(randomData)
	if przm.DetectARM64(randomData) {
		t.Fatal("DetectARM64 returned true on random noise")
	}
}

func TestARM64_ZeroAllocDecompress(t *testing.T) {
	orig := generateARM64Binary(25, 10)
	compressed := przm.CompressARM64(orig)
	if len(compressed) == 0 {
		t.Fatal("CompressARM64 returned empty slice")
	}

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	// Verify correctness first
	res, err := przm.DecompressARM64(compressed, dst)
	if err != nil {
		t.Fatalf("DecompressARM64 failed: %v", err)
	}
	if !bytes.Equal(res, orig) {
		t.Fatal("DecompressARM64 roundtrip mismatch!")
	}

	// Verify 0 heap allocations
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = przm.DecompressARM64(compressed, dst)
	})

	if allocs > 0 {
		t.Fatalf("DecompressARM64 allocated %v allocs/op, expected strictly 0", allocs)
	}
}

func BenchmarkARM64Filter(b *testing.B) {
	data := generateARM64Binary(200, 20)
	work := make([]byte, len(data))

	b.SetBytes(int64(len(data)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		copy(work, data)
		przm.ARM64Filter(work, 0, true)
	}
}

func BenchmarkARM64Decompress(b *testing.B) {
	data := generateARM64Binary(200, 20)
	comp := przm.CompressARM64(data)
	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := przm.DecompressARM64(comp, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestPRZM_Compress_ARM64(t *testing.T) {
	orig := generateARM64Binary(50, 15)

	// Compress via standard top-level Compress API
	comp, err := przm.Compress(orig, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	ratio := float64(len(orig)) / float64(len(comp))
	saved := (1.0 - float64(len(comp))/float64(len(orig))) * 100.0
	t.Logf("ARM64 Binary: Raw=%d B -> PRZM=%d B (Ratio: %.2fx, Saved: +%.1f%%)",
		len(orig), len(comp), ratio, saved)

	if len(comp) >= len(orig) {
		t.Fatalf("expected compression to reduce size, got %d >= %d", len(comp), len(orig))
	}

	// Decompress via standard top-level Decompress API
	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(decomp, orig) {
		t.Fatal("Decompressed binary does not match original!")
	}
}
