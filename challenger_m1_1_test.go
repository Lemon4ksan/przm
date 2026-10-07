// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

// =============================================================================
// CHALLENGER M1-1 (GEN 3): EMPIRICAL ADVERSARIAL TEST HARNESS
// =============================================================================

func buildTestPRZMFrame(origUSize int, cmds, offs, lits []byte, stride int) []byte {
	flags := uint8(0)
	if stride > 1 {
		flags |= FlagStride
	}
	sec2 := make([]byte, 5+len(cmds))
	sec2[0] = ModeFlat
	binary.LittleEndian.PutUint32(sec2[1:5], uint32(len(cmds)))
	copy(sec2[5:], cmds)

	sec3 := make([]byte, 5+len(offs))
	sec3[0] = ModeFlat
	binary.LittleEndian.PutUint32(sec3[1:5], uint32(len(offs)))
	copy(sec3[5:], offs)

	secDirSize := 4
	sec1Size := 8
	frameParamSize := 0
	if stride > 1 {
		frameParamSize = 1
	}

	total := HeaderSize + frameParamSize + secDirSize + sec1Size + len(sec2) + len(sec3) + len(lits)
	frame := make([]byte, total)
	binary.LittleEndian.PutUint32(frame[0:4], MagicPRZM)
	frame[4] = flags
	frame[5] = byte(origUSize)
	frame[6] = byte(origUSize >> 8)
	frame[7] = byte(origUSize >> 16)

	w := HeaderSize
	if stride > 1 {
		frame[w] = uint8(stride - 1)
		w++
	}

	binary.LittleEndian.PutUint16(frame[w:w+2], uint16(len(sec2)))
	binary.LittleEndian.PutUint16(frame[w+2:w+4], uint16(len(sec3)))
	w += 4

	// 8 bytes distribution parameters
	w += 8

	copy(frame[w:], sec2)
	w += len(sec2)

	copy(frame[w:], sec3)
	w += len(sec3)

	copy(frame[w:], lits)
	return frame
}

func getCBinaryPath(t *testing.T) string {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	t.Fatal("test_przm_dec.exe not found")
	return ""
}

// -----------------------------------------------------------------------------
// 1. REPEAT REGISTERS MTF TRANSITIONS & CROSS-LANGUAGE PARITY (GO & C)
// -----------------------------------------------------------------------------

func TestChallenger_M1_1_RepeatRegisters_CrossParity_And_MTF(t *testing.T) {
	cBin := getCBinaryPath(t)
	tmpDir := t.TempDir()

	// Build a scenario with intricate MTF permutations:
	// Start with 64 literals.
	// Step 1: New match at d=32, mLen=4 -> rep0=32, rep1=1, rep2=4
	// Step 2: New match at d=16, mLen=5 -> rep0=16, rep1=32, rep2=1
	// Step 3: New match at d=8,  mLen=6 -> rep0=8, rep1=16, rep2=32
	// Step 4: Repeat k=0 (d=rep0=8) -> rep0=8, rep1=16, rep2=32
	// Step 5: Repeat k=1 (d=rep1=16) -> rep0=16, rep1=8, rep2=32
	// Step 6: Repeat k=2 (d=rep2=32) -> rep0=32, rep1=16, rep2=8
	// Step 7: Repeat k=2 (d=rep2=8) -> rep0=8, rep1=32, rep2=16
	// Step 8: Long repeat k=1 (d=rep1=32) -> rep0=32, rep1=8, rep2=16
	// Step 9: Long repeat k=0 (d=rep0=32) -> rep0=32, rep1=8, rep2=16
	// Step 10: Long repeat k=2 (d=rep2=16) -> rep0=16, rep1=32, rep2=8

	lits := make([]byte, 64)
	for i := range lits {
		lits[i] = byte(i*7 + 3)
	}

	cmds := []byte{0xF0, 64} // 64 literals
	offs := []byte{}

	// Step 1: New match d=32, mLen=4 (op 0x80)
	cmds = append(cmds, 0x80)
	offs = append(offs, 32)

	// Step 2: New match d=16, mLen=5 (op 0x81)
	cmds = append(cmds, 0x81)
	offs = append(offs, 16)

	// Step 3: New match d=8, mLen=6 (op 0x82)
	cmds = append(cmds, 0x82)
	offs = append(offs, 8)

	// Step 4: k=0 (mLen=4, op 0x00)
	cmds = append(cmds, 0x00)

	// Step 5: k=1 (mLen=4, op 0x01)
	cmds = append(cmds, 0x01)

	// Step 6: k=2 (mLen=4, op 0x02)
	cmds = append(cmds, 0x02)

	// Step 7: k=2 (mLen=4, op 0x02)
	cmds = append(cmds, 0x02)

	// Step 8: Long repeat k=1, mLen=4 (op 0xC1, token 0x00)
	cmds = append(cmds, 0xC1, 0x00)

	// Step 9: Long repeat k=0, mLen=4 (op 0xC0, token 0x00)
	cmds = append(cmds, 0xC0, 0x00)

	// Step 10: Long repeat k=2, mLen=4 (op 0xC2, token 0x00)
	cmds = append(cmds, 0xC2, 0x00)

	totalUSize := 64 + 4 + 5 + 6 + 4 + 4 + 4 + 4 + 4 + 4 + 4 // 107 bytes
	frame := buildTestPRZMFrame(totalUSize, cmds, offs, lits, 1)

	// Test Go decompression
	dstGo := make([]byte, totalUSize+64)
	resGo, err := DecompressTo(frame, dstGo)
	if err != nil {
		t.Fatalf("Go DecompressTo failed on complex MTF sequence: %v", err)
	}
	if len(resGo) != totalUSize {
		t.Fatalf("Go DecompressTo returned length %d, want %d", len(resGo), totalUSize)
	}

	// Test C decompression
	inPath := filepath.Join(tmpDir, "mtf_frame.przm")
	outPath := filepath.Join(tmpDir, "mtf_frame.bin")
	if err := os.WriteFile(inPath, frame, 0644); err != nil {
		t.Fatalf("Failed to write frame: %v", err)
	}
	cmd := exec.Command(cBin, inPath, outPath)
	cOut, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("C decompressor failed on MTF frame: %v, output: %s", err, string(cOut))
	}
	resC, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("Failed to read C output: %v", err)
	}
	if len(resC) != totalUSize {
		t.Fatalf("C decompressor returned length %d, want %d", len(resC), totalUSize)
	}

	// Compare Go and C results bit-for-bit
	if !bytes.Equal(resGo, resC) {
		t.Fatalf("Discrepancy between Go and C on MTF repeat registers!\nGo: %x\n C: %x", resGo, resC)
	}

	// Oracle verification
	golden := make([]byte, 0, totalUSize)
	golden = append(golden, lits...)
	r0, r1, r2 := 1, 4, 8

	simMatch := func(d, mLen int) {
		start := len(golden) - d
		for i := 0; i < mLen; i++ {
			golden = append(golden, golden[start+i])
		}
	}

	// Step 1: d=32
	d := 32
	r2 = r1; r1 = r0; r0 = d
	simMatch(d, 4)

	// Step 2: d=16
	d = 16
	r2 = r1; r1 = r0; r0 = d
	simMatch(d, 5)

	// Step 3: d=8
	d = 8
	r2 = r1; r1 = r0; r0 = d
	simMatch(d, 6)

	// Step 4: k=0 -> d=r0 (8)
	d = r0
	simMatch(d, 4)

	// Step 5: k=1 -> d=r1 (16), swap r1, r0
	d = r1
	r1 = r0
	r0 = d
	simMatch(d, 4)

	// Step 6: k=2 -> d=r2 (32), shift r2->r1->r0
	d = r2
	r2 = r1
	r1 = r0
	r0 = d
	simMatch(d, 4)

	// Step 7: k=2 -> d=r2 (8)
	d = r2
	r2 = r1
	r1 = r0
	r0 = d
	simMatch(d, 4)

	// Step 8: k=1 -> d=r1 (32)
	d = r1
	r1 = r0
	r0 = d
	simMatch(d, 4)

	// Step 9: k=0 -> d=r0 (32)
	d = r0
	simMatch(d, 4)

	// Step 10: k=2 -> d=r2 (16)
	d = r2
	r2 = r1
	r1 = r0
	r0 = d
	simMatch(d, 4)

	if !bytes.Equal(resGo, golden) {
		t.Fatalf("Go output failed golden oracle!\nGot:    %x\nGolden: %x", resGo, golden)
	}
}

// -----------------------------------------------------------------------------
// 2. REJECTION OF k >= 3 ACROSS GO AND C ENGINES
// -----------------------------------------------------------------------------

func TestChallenger_M1_1_Rejection_k_ge_3_GoAndC(t *testing.T) {
	cBin := getCBinaryPath(t)
	tmpDir := t.TempDir()

	testRejection := func(name string, opcodes []byte, makeCmds func(op byte) []byte) {
		t.Run(name, func(t *testing.T) {
			for _, op := range opcodes {
				cmds := makeCmds(op)
				lits := make([]byte, 64)
				frame := buildTestPRZMFrame(32, cmds, nil, lits, 1)

				// 1. Verify Go rejects
				dstGo := make([]byte, 64)
				_, err := DecompressTo(frame, dstGo)
				if err == nil {
					t.Fatalf("Go failed to reject invalid opcode 0x%02X", op)
				}

				// 2. Verify C rejects
				inPath := filepath.Join(tmpDir, fmt.Sprintf("corrupt_0x%02X.przm", op))
				outPath := filepath.Join(tmpDir, fmt.Sprintf("corrupt_0x%02X.bin", op))
				_ = os.WriteFile(inPath, frame, 0644)

				cmd := exec.Command(cBin, inPath, outPath)
				_ = cmd.Run()
				if cmd.ProcessState != nil && cmd.ProcessState.Success() {
					t.Fatalf("C decompressor unexpectedly accepted invalid opcode 0x%02X", op)
				}
			}
		})
	}

	// Fused Repeat: op < 0x80 with KK = 3
	testRejection("FusedRepeat_KK_3", []byte{0x03, 0x13, 0x27, 0x3B, 0x43, 0x57, 0x6B, 0x73, 0x77, 0x7B, 0x7F}, func(op byte) []byte {
		return []byte{0xF0, 16, op}
	})

	// Long Repeat: 0xC0 | k with k in 3..15
	longRepeatOps := make([]byte, 0, 13)
	for k := 3; k <= 15; k++ {
		longRepeatOps = append(longRepeatOps, byte(0xC0|k))
	}
	testRejection("LongRepeat_k_ge_3", longRepeatOps, func(op byte) []byte {
		return []byte{0xF0, 16, op, 0x00}
	})

	// Delta Locality: 0xE0 | k with k in 3..7
	deltaOps := make([]byte, 0, 5)
	for k := 3; k <= 7; k++ {
		deltaOps = append(deltaOps, byte(0xE0|k))
	}
	testRejection("DeltaLocality_k_ge_3", deltaOps, func(op byte) []byte {
		return []byte{0xF0, 16, op, 0x00}
	})
}

// -----------------------------------------------------------------------------
// 3. ZERO ALLOCATIONS IN DecompressTo AND UnpackStream (STRICT ALLOCSPERRUN == 0.0)
// -----------------------------------------------------------------------------

func TestChallenger_M1_1_ZeroAlloc_Invariant_Comprehensive(t *testing.T) {
	testData := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"1B", []byte{0x42}},
		{"2B", []byte{0xAA, 0xBB}},
		{"15B", []byte("15 bytes string")},
		{"32B_boundary", bytes.Repeat([]byte("X"), 32)},
		{"64B_small", []byte("The quick brown fox jumps over the lazy dog 1234567890 0987654321")},
		{"1KB_JSON", bytes.Repeat([]byte(`{"metric":"latency","val":12.3}`+"\n"), 31)},
		{"16KB_text", bytes.Repeat([]byte("PRZM high-speed RAM compression zero alloc guarantee!\n"), 300)},
		{"64KB_homo", bytes.Repeat([]byte{0x99}, 65536)},
		{"64KB_stride4", func() []byte {
			b := make([]byte, 65536)
			for i := range b {
				b[i] = byte(i & 3)
			}
			return b
		}()},
		{"4KB_cryptoNoise", func() []byte {
			rng := rand.New(rand.NewSource(12345))
			b := make([]byte, 4096)
			rng.Read(b)
			return b
		}()},
	}

	for _, td := range testData {
		t.Run(td.name, func(t *testing.T) {
			for level := 1; level <= 3; level++ {
				comp, err := Compress(td.data, level)
				if err != nil {
					t.Fatalf("Compress failed on %s lvl %d: %v", td.name, level, err)
				}

				dst := make([]byte, len(td.data)+64)
				// Warm up
				_, err = DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("Warmup DecompressTo failed on %s lvl %d: %v", td.name, level, err)
				}

				// Measure allocations
				allocs := testing.AllocsPerRun(100, func() {
					_, err := DecompressTo(comp, dst)
					if err != nil {
						t.Fatal(err)
					}
				})

				if allocs > 0.0 {
					t.Fatalf("ZERO-ALLOC VIOLATION on %s lvl %d: got %.2f allocs/run", td.name, level, allocs)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 4. CONCURRENT ZERO-ALLOC STRESS (MULTI-GOROUTINE SCRATCHPAD SAFETY)
// -----------------------------------------------------------------------------

func TestChallenger_M1_1_ZeroAlloc_ConcurrentPoolStress(t *testing.T) {
	data := bytes.Repeat([]byte("PRZM concurrent pool zero-allocation stress test payload 1234567890!\n"), 100)
	comp, err := Compress(data, 1)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	concurrency := 16
	iterations := 200
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for g := 0; g < concurrency; g++ {
		go func(id int) {
			defer wg.Done()
			dst := make([]byte, len(data)+64)
			for i := 0; i < iterations; i++ {
				dec, err := DecompressTo(comp, dst)
				if err != nil {
					t.Errorf("Goroutine %d iteration %d failed: %v", id, i, err)
					return
				}
				if !bytes.Equal(dec, data) {
					t.Errorf("Goroutine %d iteration %d data mismatch", id, i)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// -----------------------------------------------------------------------------
// 5. UNPACKSTREAM ZERO ALLOCATION VERIFICATION
// -----------------------------------------------------------------------------

func TestChallenger_M1_1_UnpackStream_ZeroAlloc(t *testing.T) {
	data := bytes.Repeat([]byte("ABCDEF0123456789_interleaved_entropy_huffman_"), 200)
	packed, err := PackStream(data, ModeHuffman4X)
	if err != nil {
		t.Fatalf("PackStream failed: %v", err)
	}

	var scratch CanonicalHuffmanTable
	dst := make([]byte, len(data)+64)

	// Warmup
	_, err = UnpackStream(dst, packed, &scratch)
	if err != nil {
		t.Fatalf("Warmup UnpackStream failed: %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		_, err := UnpackStream(dst, packed, &scratch)
		if err != nil {
			t.Fatal(err)
		}
	})

	if allocs > 0.0 {
		t.Fatalf("UnpackStream allocated %.2f allocs/run (mandated 0.0)", allocs)
	}
}
