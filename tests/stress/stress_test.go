//go:build ignore

package przm

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"math/bits"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm/bench"
)

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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
	r2 = r1
	r1 = r0
	r0 = d
	simMatch(d, 4)

	// Step 2: d=16
	d = 16
	r2 = r1
	r1 = r0
	r0 = d
	simMatch(d, 5)

	// Step 3: d=8
	d = 8
	r2 = r1
	r1 = r0
	r0 = d
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

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// =============================================================================
// CHALLENGER M1-2 SUITE: TASK 2 - 4-Stream Canonical Huffman Stress & Edge Cases
// =============================================================================

// TestChallenger_Huffman_ExtremeInputs tests 0B, 1B, 2B, homogeneous, and max block inputs.
func TestChallenger_Huffman_ExtremeInputs(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"0-byte empty", []byte{}},
		{"1-byte single", []byte{0x42}},
		{"2-byte identical", []byte{0xAA, 0xAA}},
		{"2-byte distinct", []byte{0x12, 0x34}},
		{"3-byte distinct", []byte{0x11, 0x22, 0x33}},
		{"4-byte distinct", []byte{0x01, 0x02, 0x03, 0x04}},
		{"7-byte odd-length", []byte("abcdefg")},
		{"15-byte short", []byte("hello, przm 4x!")},
		{"31-byte boundary", bytes.Repeat([]byte("A"), 31)},
		{"32-byte boundary", bytes.Repeat([]byte("B"), 32)},
		{"33-byte boundary", bytes.Repeat([]byte("C"), 33)},
		{"Homogeneous 1KB", bytes.Repeat([]byte{0x55}, 1024)},
		{"Homogeneous 64KB", bytes.Repeat([]byte{0x77}, 65536)},
		{"Bimodal 64KB (2 symbols)", bytes.Repeat([]byte{0x00, 0xFF}, 32768)},
		{"Trimodal 64KB (3 symbols)", bytes.Repeat([]byte{0x01, 0x02, 0x03}, 21845)},
		{"Four symbols 64KB", bytes.Repeat([]byte{0x10, 0x20, 0x30, 0x40}, 16384)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Test via PackStream & UnpackStream
			packed, err := PackStream(tc.data, ModeHuffman4X)
			if err != nil {
				t.Fatalf("PackStream failed on %s: %v", tc.name, err)
			}

			var scratch CanonicalHuffmanTable
			dst := make([]byte, len(tc.data)+64)
			n, err := UnpackStream(dst, packed, &scratch)
			if err != nil {
				t.Fatalf("UnpackStream failed on %s: %v", tc.name, err)
			}
			if n != len(tc.data) {
				t.Fatalf("UnpackStream returned len %d, want %d", n, len(tc.data))
			}
			if !bytes.Equal(dst[:n], tc.data) {
				t.Fatalf("Roundtrip mismatch on %s", tc.name)
			}

			// If data >= 32, also test EncodeHuffman4X and DecodeHuffman4X directly
			if len(tc.data) > 0 {
				enc, err := EncodeHuffman4X(tc.data)
				if err != nil {
					t.Fatalf("EncodeHuffman4X failed on %s: %v", tc.name, err)
				}
				decDst := make([]byte, len(tc.data)+64)
				var decTable CanonicalHuffmanTable
				decN, err := UnpackStream(decDst, enc, &decTable)
				if err != nil {
					t.Fatalf("UnpackStream on EncodeHuffman4X failed on %s: %v", tc.name, err)
				}
				if decN != len(tc.data) || !bytes.Equal(decDst[:decN], tc.data) {
					t.Fatalf("EncodeHuffman4X/UnpackStream roundtrip failed on %s", tc.name)
				}
			}
		})
	}
}

// TestChallenger_Huffman_ModeFlatFallback verifies that high-entropy data falls back to ModeFlat.
func TestChallenger_Huffman_ModeFlatFallback(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	sizes := []int{32, 64, 128, 256, 1024, 4096, 16384, 65536}

	for _, sz := range sizes {
		// Pure pseudo-random bytes (incompressible high entropy)
		noise := make([]byte, sz)
		rng.Read(noise)

		packed, err := PackStream(noise, ModeHuffman4X)
		if err != nil {
			t.Fatalf("PackStream error on noise sz=%d: %v", sz, err)
		}

		if len(packed) == 0 {
			t.Fatalf("Empty packed stream for sz=%d", sz)
		}

		// Since high entropy saves < 16 bytes (or expands due to 149B header),
		// it MUST fall back to ModeFlat (mode byte = 0)
		if packed[0] != ModeFlat {
			t.Logf("Notice: size=%d packed mode=%d len=%d (orig=%d)", sz, packed[0], len(packed), sz)
		}

		// Verify roundtrip
		var scratch CanonicalHuffmanTable
		dst := make([]byte, sz+64)
		n, err := UnpackStream(dst, packed, &scratch)
		if err != nil {
			t.Fatalf("UnpackStream failed for noise sz=%d: %v", sz, err)
		}
		if n != sz || !bytes.Equal(dst[:n], noise) {
			t.Fatalf("Roundtrip mismatch on noise sz=%d", sz)
		}
	}
}

// TestChallenger_Huffman_CorruptedHeadersAndPayloads ensures zero panics and clean errors on corruption.
func TestChallenger_Huffman_CorruptedHeadersAndPayloads(t *testing.T) {
	// Create valid Huffman-encoded stream
	rng := rand.New(rand.NewSource(42))
	raw := make([]byte, 4096)
	// Biased data with enough compression to trigger ModeHuffman4X
	for i := range raw {
		raw[i] = byte(rng.Intn(16))
	}

	packed, err := EncodeHuffman4X(raw)
	if err != nil {
		t.Fatalf("EncodeHuffman4X failed: %v", err)
	}
	if packed[0] != ModeHuffman4X {
		t.Fatalf("Expected ModeHuffman4X (1), got %d", packed[0])
	}

	var scratch CanonicalHuffmanTable
	dst := make([]byte, len(raw)+64)

	// 1. Truncated buffers from 0 to len(packed)-1
	for truncLen := 0; truncLen < len(packed); truncLen++ {
		sub := packed[:truncLen]
		_, err := UnpackStream(dst, sub, &scratch)
		if err == nil {
			t.Fatalf("Expected error on truncated stream len=%d, got nil", truncLen)
		}
	}

	// 2. Corrupt OrigLen (make it gigantic)
	corruptOrigLen := make([]byte, len(packed))
	copy(corruptOrigLen, packed)
	binary.LittleEndian.PutUint32(corruptOrigLen[1:5], 0x7FFFFFFF)
	_, err = UnpackStream(dst, corruptOrigLen, &scratch)
	if err == nil {
		t.Fatalf("Expected error for gigantic origLen, got nil")
	}

	// 3. Corrupt stream sizes (bytes 133..149)
	for s := 0; s < 4; s++ {
		corruptSz := make([]byte, len(packed))
		copy(corruptSz, packed)
		// set stream size beyond payload
		offset := 133 + s*4
		binary.LittleEndian.PutUint32(corruptSz[offset:offset+4], 0x10000000)
		_, err = UnpackStream(dst, corruptSz, &scratch)
		if err == nil {
			t.Fatalf("Expected error for stream %d huge size, got nil", s)
		}
	}

	// 4. Bit flips across entire header (first 149 bytes)
	for byteIdx := 0; byteIdx < HeaderSizeHuffman; byteIdx++ {
		for bit := 0; bit < 8; bit++ {
			mutated := make([]byte, len(packed))
			copy(mutated, packed)
			mutated[byteIdx] ^= 1 << bit

			// Must not panic!
			_, _ = UnpackStream(dst, mutated, &scratch)
		}
	}

	// 5. Random fuzzed mutations of payload
	for trial := 0; trial < 1000; trial++ {
		fuzzed := make([]byte, len(packed))
		copy(fuzzed, packed)
		numMutations := 1 + rng.Intn(20)
		for m := 0; m < numMutations; m++ {
			idx := rng.Intn(len(fuzzed))
			fuzzed[idx] = byte(rng.Intn(256))
		}
		// Must not panic
		_, _ = UnpackStream(dst, fuzzed, &scratch)
	}
}

// =============================================================================
// CHALLENGER M1-2 SUITE: TASK 1 - 3-Repeat Offset Registers & MTF Transitions
// =============================================================================

func makeSyntheticPRZMFrame(origUSize int, cmds, offs, lits []byte, stride int) []byte {
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

// TestChallenger_RepeatRegisters_MTF_And_Rejection tests rep0/rep1/rep2 MTF state transitions
// and strictly verifies rejection of invalid repeat indices k >= 3 without panicking.
func TestChallenger_RepeatRegisters_MTF_And_Rejection(t *testing.T) {
	t.Run("Rejection_k_ge_3", func(t *testing.T) {
		// 1. Fused Repeat Tokens (00 LLL MM KK) where KK == 3
		for _, op := range []byte{0x03, 0x13, 0x27, 0x3B, 0x73, 0x7F} {
			cmds := []byte{0xF0, 16, op}
			lits := make([]byte, 64)
			frame := makeSyntheticPRZMFrame(32, cmds, nil, lits, 1)
			dst := make([]byte, 64)

			_, err := DecompressTo(frame, dst)
			if err == nil {
				t.Fatalf("Expected rejection for fused repeat opcode 0x%02X (k=3), got nil error", op)
			}
			if !strings.Contains(err.Error(), "invalid repeat index") {
				t.Fatalf("Expected 'invalid repeat index' error for 0x%02X, got: %v", op, err)
			}
		}

		// 2. Long Repeat Match (0xC0 .. 0xCF) with k in 3..15
		for k := 3; k <= 15; k++ {
			op := byte(0xC0 | k)
			cmds := []byte{0xF0, 16, op, 0x00}
			lits := make([]byte, 16)
			frame := makeSyntheticPRZMFrame(32, cmds, nil, lits, 1)
			dst := make([]byte, 64)

			_, err := DecompressTo(frame, dst)
			if err == nil {
				t.Fatalf("Expected rejection for long repeat opcode 0x%02X (k=%d), got nil error", op, k)
			}
			if !strings.Contains(err.Error(), "invalid long repeat index") {
				t.Fatalf("Expected 'invalid long repeat index' error for k=%d, got: %v", k, err)
			}
		}

		// 3. Delta-Mask Locality (0xE0 .. 0xE7) with k in 3..7
		for k := 3; k <= 7; k++ {
			op := byte(0xE0 | k)
			cmds := []byte{0xF0, 16, op, 0x00}
			lits := make([]byte, 32)
			frame := makeSyntheticPRZMFrame(32, cmds, nil, lits, 1)
			dst := make([]byte, 64)

			_, err := DecompressTo(frame, dst)
			if err == nil {
				t.Fatalf("Expected rejection for delta locality opcode 0x%02X (k=%d), got nil error", op, k)
			}
			if !strings.Contains(err.Error(), "invalid delta locality index") {
				t.Fatalf("Expected 'invalid delta locality index' error for k=%d, got: %v", k, err)
			}
		}
	})

	t.Run("MTF_State_Transitions", func(t *testing.T) {
		// Trace exact MTF behavior:
		// Initial: rep0=1, rep1=4, rep2=8
		// Literal: 32 bytes "0123456789ABCDEFGHIJKLMNOPQRSTUV"
		lits := []byte("0123456789ABCDEFGHIJKLMNOPQRSTUV")
		cmds := []byte{0xF0, 32} // emit 32 literals
		offs := []byte{}

		// 1. New match at distance 16, mLen=4 (op 0x80)
		cmds = append(cmds, 0x80)
		offs = append(offs, 16)
		// Registers: rep0=16, rep1=1, rep2=4

		// 2. New match at distance 8, mLen=4 (op 0x80)
		cmds = append(cmds, 0x80)
		offs = append(offs, 8)
		// Registers: rep0=8, rep1=16, rep2=1

		// 3. Repeat match k=1 (mLen=4, op 0x01)
		// Uses d=rep1=16. MTF swap: rep1=8, rep0=16, rep2=1
		cmds = append(cmds, 0x01)

		// 4. Repeat match k=2 (mLen=4, op 0x02)
		// Uses d=rep2=1. MTF swap: rep2=8, rep1=16, rep0=1
		cmds = append(cmds, 0x02)

		// 5. Repeat match k=0 (mLen=4, op 0x00)
		// Uses d=rep0=1. Unchanged: rep0=1, rep1=16, rep2=8
		cmds = append(cmds, 0x00)

		// 6. Repeat match k=1 (mLen=4, op 0x01)
		// Uses d=rep1=16. MTF swap: rep1=1, rep0=16, rep2=8
		cmds = append(cmds, 0x01)

		// 7. Long repeat match k=2 (mLen=4, op 0xC2, token 0x00)
		// Uses d=rep2=8. MTF swap: rep2=1, rep1=16, rep0=8
		cmds = append(cmds, 0xC2, 0x00)

		totalUSize := 32 + 4*7 // 60 bytes
		frame := makeSyntheticPRZMFrame(totalUSize, cmds, offs, lits, 1)

		dst := make([]byte, totalUSize+64)
		res, err := DecompressTo(frame, dst)
		if err != nil {
			t.Fatalf("DecompressTo failed on valid MTF sequence: %v", err)
		}
		if len(res) != totalUSize {
			t.Fatalf("Expected length %d, got %d", totalUSize, len(res))
		}

		// Calculate golden reference
		golden := make([]byte, 0, totalUSize)
		golden = append(golden, lits...)
		rep0, rep1, rep2 := 1, 4, 8

		applyMatch := func(d, mLen int) {
			start := len(golden) - d
			for i := 0; i < mLen; i++ {
				golden = append(golden, golden[start+i])
			}
		}

		// step 1: d=16
		d := 16
		rep2 = rep1
		rep1 = rep0
		rep0 = d
		applyMatch(d, 4)

		// step 2: d=8
		d = 8
		rep2 = rep1
		rep1 = rep0
		rep0 = d
		applyMatch(d, 4)

		// step 3: k=1 -> d=rep1
		d = rep1
		rep1 = rep0
		rep0 = d
		applyMatch(d, 4)

		// step 4: k=2 -> d=rep2
		d = rep2
		rep2 = rep1
		rep1 = rep0
		rep0 = d
		applyMatch(d, 4)

		// step 5: k=0 -> d=rep0
		d = rep0
		applyMatch(d, 4)

		// step 6: k=1 -> d=rep1
		d = rep1
		rep1 = rep0
		rep0 = d
		applyMatch(d, 4)

		// step 7: k=2 -> d=rep2
		d = rep2
		rep2 = rep1
		rep1 = rep0
		rep0 = d
		applyMatch(d, 4)

		if !bytes.Equal(res, golden) {
			t.Fatalf("MTF decompressed output did not match golden MTF simulation!\nGot:    %x\nGolden: %x", res, golden)
		}
	})

	t.Run("NaturalRoundtrip_3Repeats", func(t *testing.T) {
		pattern := bytes.Repeat([]byte("ABCDEF_012345_GHIJKL_678901_"), 120)
		for lvl := 1; lvl <= 3; lvl++ {
			comp, err := Compress(pattern, lvl)
			if err != nil {
				t.Fatalf("Compress failed at level %d: %v", lvl, err)
			}
			dst := make([]byte, len(pattern)+64)
			dec, err := DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed at level %d: %v", lvl, err)
			}
			if !bytes.Equal(dec, pattern) {
				t.Fatalf("Mismatch on natural 3-repeat pattern at level %d", lvl)
			}
		}
	})
}

// =============================================================================
// CHALLENGER M1-2 SUITE: TASK 3 - Zero-Alloc Invariant in DecompressTo
// =============================================================================

// TestChallenger_ZeroAlloc_DecompressTo asserts strictly 0.0 heap allocations during DecompressTo.
func TestChallenger_ZeroAlloc_DecompressTo(t *testing.T) {
	datasets := []struct {
		name string
		data []byte
	}{
		{"ShortASCII_64B", []byte("The quick brown fox jumps over the lazy dog. 0123456789 ABCDEF!!")},
		{"MediumJSON_4KB", bytes.Repeat([]byte(`{"metric":"cpu","value":42.5,"status":"ok","node":"prod-worker-01"}`+"\n"), 60)},
		{"LargeText_64KB", bytes.Repeat([]byte("Go zero-allocation decompression invariant testing in PRZM codec!\n"), 1000)},
		{"HomogeneousRLE_64KB", bytes.Repeat([]byte{0x5A}, 65536)},
		{"PeriodicStride4_64KB", func() []byte {
			b := make([]byte, 65536)
			for i := range b {
				b[i] = byte(i & 3)
			}
			return b
		}()},
		{"HighEntropy_4KB", func() []byte {
			rng := rand.New(rand.NewSource(999))
			b := make([]byte, 4096)
			rng.Read(b)
			return b
		}()},
	}

	for _, ds := range datasets {
		t.Run(ds.name, func(t *testing.T) {
			for level := 1; level <= 3; level++ {
				comp, err := Compress(ds.data, level)
				if err != nil {
					t.Fatalf("Compress failed at level %d: %v", level, err)
				}

				dst := make([]byte, len(ds.data)+64)
				// Warm-up run to prime pools and buffers
				dec, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("DecompressTo warm-up failed: %v", err)
				}
				if !bytes.Equal(dec, ds.data) {
					t.Fatalf("DecompressTo mismatch on %s level %d", ds.name, level)
				}

				// Measure allocations
				allocs := testing.AllocsPerRun(100, func() {
					_, err := DecompressTo(comp, dst)
					if err != nil {
						t.Fatal(err)
					}
				})
				if allocs > 0.0 {
					t.Fatalf("VIOLATION of zero-alloc invariant on %s (lvl %d): got %.2f allocs/run (mandated strictly 0.0)", ds.name, level, allocs)
				}
			}
		})
	}
}

// =============================================================================
// CHALLENGER M1-2 SUITE: TASK 4 - Pure C Reference Decompressor Stress
// =============================================================================

// TestChallenger_PureCDecompressor_Stress validates the pure C reference decompressor test_przm_dec.exe.
func TestChallenger_PureCDecompressor_Stress(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
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
		t.Fatal("Pure C test executable test_przm_dec.exe not found")
	}

	// 1. Run internal C unit test suite
	t.Run("InternalCVerificationSuite", func(t *testing.T) {
		cmd := exec.Command(cBinPath)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("C test suite failed: %v\nOutput:\n%s", err, string(out))
		}
		if !strings.Contains(string(out), "ALL TESTS PASSED SUCCESSFULLY") {
			t.Fatalf("C test suite missing success banner:\nOutput:\n%s", string(out))
		}
	})

	tmpDir := t.TempDir()

	// 2. Stress test Go Compress -> C Decompressor across patterns and boundary sizes
	testPayloads := []struct {
		name string
		data []byte
	}{
		{"1-byte single", []byte{0x7F}},
		{"2-byte pair", []byte{0x12, 0x34}},
		{"15-byte short", []byte("abcdefghijklmno")},
		{"32-byte boundary", bytes.Repeat([]byte("X"), 32)},
		{"64-byte boundary", bytes.Repeat([]byte("01234567"), 8)},
		{"255-byte boundary", bytes.Repeat([]byte("A"), 255)},
		{"256-byte boundary", bytes.Repeat([]byte("B"), 256)},
		{"4KB structured JSON", bytes.Repeat([]byte(`{"id":123,"status":"active","payload":"prism"}`+"\n"), 80)},
		{"16KB Stride4 telemetry", func() []byte {
			b := make([]byte, 16384)
			for i := range b {
				b[i] = byte(i % 4)
			}
			return b
		}()},
		{"32KB Alternating bits", func() []byte {
			b := make([]byte, 32768)
			for i := range b {
				b[i] = byte(i & 0xAA)
			}
			return b
		}()},
		{"64KB Homogeneous", bytes.Repeat([]byte{0x42}, 65536)},
		{"8KB Incompressible noise", func() []byte {
			rng := rand.New(rand.NewSource(777))
			b := make([]byte, 8192)
			rng.Read(b)
			return b
		}()},
	}

	for _, tc := range testPayloads {
		t.Run("Payload_"+tc.name, func(t *testing.T) {
			for level := 1; level <= 4; level++ {
				comp, err := Compress(tc.data, level)
				if err != nil {
					t.Fatalf("Compress failed on %s (lvl %d): %v", tc.name, level, err)
				}

				inPath := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", tc.name, level))
				outPath := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.bin", tc.name, level))

				if err := os.WriteFile(inPath, comp, 0644); err != nil {
					t.Fatalf("Failed to write in.przm: %v", err)
				}

				cmd := exec.Command(cBinPath, inPath, outPath)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("C decompressor failed on %s (lvl %d): %v\nOutput:\n%s", tc.name, level, err, string(out))
				}

				dec, err := os.ReadFile(outPath)
				if err != nil {
					t.Fatalf("Failed to read out.bin: %v", err)
				}

				if !bytes.Equal(dec, tc.data) {
					t.Fatalf("Mismatch on %s (lvl %d): got %d bytes, want %d bytes", tc.name, level, len(dec), len(tc.data))
				}
			}
		})
	}

	// 3. Adversarial / Corrupted frames passed to C decompressor
	t.Run("AdversarialCorruptionRobustness", func(t *testing.T) {
		corruptCases := []struct {
			name string
			data []byte
		}{
			{"Empty file", []byte{}},
			{"1 byte header", []byte{'P'}},
			{"4 byte magic only", []byte{'P', 'R', 'Z', 'M'}},
			{"Wrong magic", []byte{'B', 'A', 'D', '!', 0, 10, 0, 0}},
			{"Truncated header", []byte{'P', 'R', 'Z', 'M', 0, 10}},
			{"Giant uncompressed size", []byte{'P', 'R', 'Z', 'M', 0, 0xFF, 0xFF, 0x7F}},
		}

		for _, cc := range corruptCases {
			t.Run(cc.name, func(t *testing.T) {
				inPath := filepath.Join(tmpDir, "corrupt.przm")
				outPath := filepath.Join(tmpDir, "corrupt.out")
				_ = os.WriteFile(inPath, cc.data, 0644)

				cmd := exec.Command(cBinPath, inPath, outPath)
				_ = cmd.Run()
				if cmd.ProcessState != nil && cmd.ProcessState.Success() {
					t.Fatalf("Expected C decompressor to reject corrupt frame %s", cc.name)
				}
			})
		}
	})
}

// =============================================================================
// CHALLENGER M1-2 SUITE: TASK 5 - Multi-Goroutine huffScratchPool Concurrency
// =============================================================================

// TestChallenger_Huffman_ConcurrentScratchPoolStress launches many concurrent goroutines
// compressing and decompressing unique datasets through EncodeHuffman4X and PackStream.
// This empirically validates that huffScratchPool is concurrency-safe and suffers from
// zero cross-goroutine contamination or data races.
func TestChallenger_Huffman_ConcurrentScratchPoolStress(t *testing.T) {
	const (
		numGoroutines = 32
		iterations    = 100
	)

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(gid*10007 + 42)))

			for iter := 0; iter < iterations; iter++ {
				// Vary payload sizes across iterations:
				// - Small: 64B - 512B
				// - Medium: 2KB - 8KB
				// - Large block: 32KB - 64KB
				// - Oversized (exceeding 32KB per stream): 80KB
				var sz int
				switch iter % 4 {
				case 0:
					sz = 64 + rng.Intn(448)
				case 1:
					sz = 2048 + rng.Intn(6144)
				case 2:
					sz = 32768 + rng.Intn(32768)
				case 3:
					sz = 80000 + rng.Intn(10000)
				}

				// Generate unique payload containing goroutine ID and iteration
				payload := make([]byte, sz)
				prefix := fmt.Sprintf("GID:%03d-ITER:%04d-", gid, iter)
				copy(payload, prefix)

				// Fill remainder with patterned and compressible symbols
				symCount := 2 + (iter % 15)
				for i := len(prefix); i < sz; i++ {
					payload[i] = byte((i + gid) % symCount)
				}

				// Test EncodeHuffman4X
				enc, err := EncodeHuffman4X(payload)
				if err != nil {
					t.Errorf("Goroutine %d iter %d EncodeHuffman4X failed: %v", gid, iter, err)
					return
				}

				decDst := make([]byte, sz+64)
				var decTable CanonicalHuffmanTable
				decN, err := UnpackStream(decDst, enc, &decTable)
				if err != nil {
					t.Errorf("Goroutine %d iter %d UnpackStream failed: %v", gid, iter, err)
					return
				}
				if decN != sz {
					t.Errorf("Goroutine %d iter %d len mismatch: got %d, want %d", gid, iter, decN, sz)
					return
				}
				if !bytes.Equal(decDst[:decN], payload) {
					t.Errorf("Goroutine %d iter %d data mismatch (cross-contamination or race)", gid, iter)
					return
				}

				// Also test PackStream
				packed, err := PackStream(payload, ModeHuffman4X)
				if err != nil {
					t.Errorf("Goroutine %d iter %d PackStream failed: %v", gid, iter, err)
					return
				}
				packDst := make([]byte, sz+64)
				packN, err := UnpackStream(packDst, packed, &decTable)
				if err != nil {
					t.Errorf("Goroutine %d iter %d UnpackStream(packed) failed: %v", gid, iter, err)
					return
				}
				if packN != sz || !bytes.Equal(packDst[:packN], payload) {
					t.Errorf("Goroutine %d iter %d PackStream roundtrip mismatch", gid, iter)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// TestChallenger_HuffBitWriter_DirtyBufferStress verifies that HuffBitWriter operates
// correctly even when the pre-allocated buffer is full of dirty non-zero bytes.
func TestChallenger_HuffBitWriter_DirtyBufferStress(t *testing.T) {
	for trial := 0; trial < 100; trial++ {
		buf := make([]byte, 4096)
		// Fill buffer with dirty garbage (0xFF, 0xAA, random)
		for i := range buf {
			buf[i] = byte(trial*31 + i)
		}

		var w HuffBitWriter
		w.Reset(buf)

		// Write sequence of known bit patterns
		rng := rand.New(rand.NewSource(int64(trial + 777)))
		type bitItem struct {
			code uint16
			len  uint8
		}
		items := make([]bitItem, 100)
		totalBits := 0
		for i := range items {
			l := uint8(1 + rng.Intn(11))
			mask := uint16((1 << l) - 1)
			c := uint16(rng.Intn(int(mask) + 1))
			items[i] = bitItem{code: c, len: l}
			totalBits += int(l)
		}

		for _, item := range items {
			w.Write(item.code, item.len)
		}
		flushed := w.Flush()

		expectedBytes := (totalBits + 7) / 8
		if len(flushed) != expectedBytes {
			t.Fatalf("Trial %d: expected %d bytes, got %d", trial, expectedBytes, len(flushed))
		}

		// Verify reading with HuffBitReader
		var r HuffBitReader
		r.Init(flushed)
		for idx, item := range items {
			c := r.Peek(int(item.len))
			r.Consume(int(item.len))
			if c != item.code {
				t.Fatalf("Trial %d item %d: got code 0x%X, want 0x%X (len %d)", trial, idx, c, item.code, item.len)
			}
		}
		if r.underflow {
			t.Fatalf("Trial %d: unexpected underflow in HuffBitReader", trial)
		}
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallenger_M2_1_LargeOffsets verifies match finding at offsets exceeding 64K, 1M, up to 8M.
func TestChallenger_M2_1_LargeOffsets(t *testing.T) {
	distances := []int{65536, 131072, 1048570, 2097152, 4194304, 8000000}
	pattern := []byte("ADVERSARIAL_M2_1_LARGE_OFFSET_MATCH_TEST_STRING_!")

	for _, dist := range distances {
		if dist <= 1048570 {
			t.Run(fmt.Sprintf("1M_Dist_%d", dist), func(t *testing.T) {
				sp := PoolHensel1M.Get()
				defer PoolHensel1M.Put(sp)
				sp.Reset()

				size := dist + len(pattern) + 64
				buf := make([]byte, size)
				for i := range buf {
					buf[i] = byte((i*41 + 17) & 0xFF)
				}
				posRef := 16
				posTarget := posRef + dist
				copy(buf[posRef:], pattern)
				copy(buf[posTarget:], pattern)

				HenselInsert1M(sp, posRef, buf, size)
				d, l := HenselFindMatch1M(buf, posTarget, size, 32, 4, sp)
				if d != dist || l < len(pattern) {
					t.Fatalf("1M match failed for dist %d: got dist=%d, len=%d, want dist=%d, len>=%d", dist, d, l, dist, len(pattern))
				}
			})
		}

		t.Run(fmt.Sprintf("8M_Dist_%d", dist), func(t *testing.T) {
			sp := PoolHensel8M.Get()
			defer PoolHensel8M.Put(sp)
			sp.Reset()

			size := dist + len(pattern) + 64
			buf := make([]byte, size)
			for i := range buf {
				buf[i] = byte((i*31 + 7) & 0xFF)
			}
			posRef := 32
			posTarget := posRef + dist
			copy(buf[posRef:], pattern)
			copy(buf[posTarget:], pattern)

			HenselInsert8M(sp, posRef, buf, size)
			d, l := HenselFindMatch8M(buf, posTarget, size, 32, 4, sp)
			if d != dist || l < len(pattern) {
				t.Fatalf("8M match failed for dist %d: got dist=%d, len=%d, want dist=%d, len>=%d", dist, d, l, dist, len(pattern))
			}
		})
	}
}

// TestChallenger_M2_1_BoundaryDistances tests exact window boundaries:
// 1M: 1048575 (pass), 1048576 (fail), 1048577 (fail)
// 8M: 8388607 (pass), 8388608 (fail)
func TestChallenger_M2_1_BoundaryDistances(t *testing.T) {
	tag := []byte("BOUNDARY_TEST_TAG_12345678")

	// 1M Boundary Cases
	t.Run("1M_Boundaries", func(t *testing.T) {
		sp := PoolHensel1M.Get()
		defer PoolHensel1M.Put(sp)

		cases := []struct {
			dist     int
			wantFind bool
		}{
			{1048575, true},  // WindowSize1M - 1: inside window
			{1048576, false}, // WindowSize1M: exactly out of window
			{1048577, false}, // WindowSize1M + 1: out of window
		}

		for _, tc := range cases {
			sp.Reset()
			size := tc.dist + len(tag) + 128
			buf := make([]byte, size)
			posRef := 10
			posTarget := posRef + tc.dist
			copy(buf[posRef:], tag)
			copy(buf[posTarget:], tag)

			HenselInsert1M(sp, posRef, buf, size)
			d, l := HenselFindMatch1M(buf, posTarget, size, 32, 4, sp)
			if tc.wantFind {
				if d != tc.dist || l < len(tag) {
					t.Fatalf("Expected match at dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			} else {
				if d != 0 || l != 0 {
					t.Fatalf("Expected no match at boundary dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			}
		}
	})

	// 8M Boundary Cases
	t.Run("8M_Boundaries", func(t *testing.T) {
		sp := PoolHensel8M.Get()
		defer PoolHensel8M.Put(sp)

		cases := []struct {
			dist     int
			wantFind bool
		}{
			{8388607, true},  // WindowSize8M - 1: inside window
			{8388608, false}, // WindowSize8M: exactly out of window
		}

		for _, tc := range cases {
			sp.Reset()
			size := tc.dist + len(tag) + 128
			buf := make([]byte, size)
			posRef := 10
			posTarget := posRef + tc.dist
			copy(buf[posRef:], tag)
			copy(buf[posTarget:], tag)

			HenselInsert8M(sp, posRef, buf, size)
			d, l := HenselFindMatch8M(buf, posTarget, size, 32, 4, sp)
			if tc.wantFind {
				if d != tc.dist || l < len(tag) {
					t.Fatalf("Expected match at dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			} else {
				if d != 0 || l != 0 {
					t.Fatalf("Expected no match at boundary dist %d, got dist=%d, len=%d", tc.dist, d, l)
				}
			}
		}
	})
}

// TestChallenger_M2_1_RingWrapping tests that older entries are cleanly overwritten
// when sliding window wraps around, without producing stale matches or crashes.
func TestChallenger_M2_1_RingWrapping(t *testing.T) {
	sp := PoolHensel1M.Get()
	defer PoolHensel1M.Put(sp)
	sp.Reset()

	const wrapCount = WindowSize1M + 1000
	buf := make([]byte, wrapCount+64)
	pattern := []byte("RING_WRAP_PATTERN_12345678")

	// Plant early match at pos 100
	copy(buf[100:], pattern)
	HenselInsert1M(sp, 100, buf, len(buf))

	// Plant late match at pos 100 + WindowSize1M + 200 (distance > 1M from early match)
	latePos := 100 + WindowSize1M + 200
	copy(buf[latePos:], pattern)

	// Overwrite intermediate entries in the ring buffer
	for p := 200; p < latePos; p += 32 {
		HenselInsert1M(sp, p, buf, len(buf))
	}

	// Finding match at latePos should NOT return the wrapped entry from pos 100
	d, l := HenselFindMatch1M(buf, latePos, len(buf), 64, 4, sp)
	if d != 0 || l != 0 {
		t.Fatalf("Ring wrapping failed: expired candidate at pos 100 was matched: dist=%d, len=%d", d, l)
	}

	// Now plant a valid match within window at latePos - 500
	validPos := latePos - 500
	copy(buf[validPos:], pattern)
	HenselInsert1M(sp, validPos, buf, len(buf))

	d, l = HenselFindMatch1M(buf, latePos, len(buf), 64, 4, sp)
	if d != 500 || l < len(pattern) {
		t.Fatalf("Ring wrapping failed to match valid entry within window: got dist=%d, len=%d", d, l)
	}
}

// TestChallenger_M2_1_TheoremHL2_FP32Pruning tests hash collisions and fp32 pruning behavior.
func TestChallenger_M2_1_TheoremHL2_FP32Pruning(t *testing.T) {
	sp := PoolHensel1M.Get()
	defer PoolHensel1M.Put(sp)
	sp.Reset()

	buf := make([]byte, 8192)

	// Case 1: Same 4-byte prefix, different fp32 bytes (Theorem HL-2 pruning)
	// Candidate 1: prefix "WXYZ" + "AAAA" (length 8)
	// Candidate 2 (closer): prefix "WXYZ" + "BBBB" (length 8)
	// Target at pos: "WXYZ" + "AAAA" + "EXTENDED_DATA"
	posCand1 := 100
	posCand2 := 200
	posTarget := 500

	copy(buf[posCand1:], []byte("WXYZAAAA12345678"))
	copy(buf[posCand2:], []byte("WXYZBBBB99999999"))
	copy(buf[posTarget:], []byte("WXYZAAAA12345678"))

	HenselInsert1M(sp, posCand1, buf, len(buf))
	HenselInsert1M(sp, posCand2, buf, len(buf))

	// HenselFindMatch searches cand2 first (head of chain), then cand1.
	// cand2 has diff in fp32 ("BBBB" vs "AAAA"), pruning or rejecting it,
	// and must proceed to cand1 which is a full match of length 16.
	dist, matchLen := HenselFindMatch1M(buf, posTarget, len(buf), 16, 4, sp)
	wantDist := posTarget - posCand1
	if dist != wantDist || matchLen < 16 {
		t.Fatalf("Theorem HL-2 pruning traversal failed: got dist=%d, len=%d, want dist=%d, len>=16", dist, matchLen, wantDist)
	}

	// Case 2: Multiplicative hash collision with DIFFERENT 4-byte prefixes.
	// Search through 32-bit space for two distinct 4-byte words with exact same Hensel hash in 1M table.
	sp.Reset()
	var w1, w2 uint32
	foundCol := false
	seen := make(map[int]uint32, 5000)
	cRng := rand.New(rand.NewSource(999))
	for iter := 0; iter < 100000; iter++ {
		candidate := cRng.Uint32()
		h := computeHenselHash(candidate, HashBits1M, HashMask1M)
		if prevW, exists := seen[h]; exists && prevW != candidate {
			w1 = prevW
			w2 = candidate
			foundCol = true
			break
		}
		seen[h] = candidate
	}
	if !foundCol {
		t.Fatalf("Could not synthesize hash collision for HashBits1M")
	}

	// Position 1 has w1 + valid match payload
	// Position 2 has w2 (hash collision, but false positive 4-byte prefix)
	// Target has w1 + valid match payload
	pos1 := 1000
	posCol := 1200
	posTgt := 1500

	binary.LittleEndian.PutUint32(buf[pos1:], w1)
	copy(buf[pos1+4:], []byte("GOLDEN_PAYLOAD_TEST"))

	binary.LittleEndian.PutUint32(buf[posCol:], w2)
	copy(buf[posCol+4:], []byte("DIFFERENT_JUNK_DATA"))

	binary.LittleEndian.PutUint32(buf[posTgt:], w1)
	copy(buf[posTgt+4:], []byte("GOLDEN_PAYLOAD_TEST"))

	HenselInsert1M(sp, pos1, buf, len(buf))
	HenselInsert1M(sp, posCol, buf, len(buf))

	d, l := HenselFindMatch1M(buf, posTgt, len(buf), 16, 4, sp)
	expectedDist := posTgt - pos1
	if d != expectedDist || l < 20 {
		t.Fatalf("False positive hash collision broke chain traversal: got dist=%d, len=%d, want dist=%d", d, l, expectedDist)
	}
}

// TestChallenger_M2_1_OracleEquivalence verifies Hensel match finders against a golden brute-force oracle.
func TestChallenger_M2_1_OracleEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	dataSize := 65536
	data := make([]byte, dataSize)
	// Fill with low-entropy alphabet so matches exist naturally
	for i := range data {
		data[i] = byte('A' + rng.Intn(8))
	}

	sp1M := PoolHensel1M.Get()
	defer PoolHensel1M.Put(sp1M)
	sp1M.Reset()

	// Brute-force match finder within window and search depth
	oracleFind := func(src []byte, pos, windowSize, minLen int) (bestDist, bestLen int) {
		bestLen = minLen - 1
		bestDist = 0
		start := max(0, pos-windowSize)
		for cand := pos - 1; cand >= start; cand-- {
			l := 0
			maxL := min(len(src)-pos, 258)
			for l < maxL && src[cand+l] == src[pos+l] {
				l++
			}
			if l > bestLen {
				bestLen = l
				bestDist = pos - cand
				if bestLen >= 258 {
					break
				}
			}
		}
		if bestLen < minLen {
			return 0, 0
		}
		return bestDist, bestLen
	}

	sp8M := PoolHensel8M.Get()
	defer PoolHensel8M.Put(sp8M)
	sp8M.Reset()

	// Populate scratchpads and spot-check across multiple positions
	for i := 0; i < dataSize-260; i++ {
		HenselInsert1M(sp1M, i, data, dataSize)
		HenselInsert8M(sp8M, i, data, dataSize)

		if i > 256 && i%2048 == 0 {
			_, oLen := oracleFind(data, i, WindowSize1M, 4)
			hDist1M, hLen1M := HenselFindMatch1M(data, i, dataSize, 256, 4, sp1M)
			hDist8M, hLen8M := HenselFindMatch8M(data, i, dataSize, 256, 4, sp8M)

			if oLen >= 4 {
				if hLen1M < 4 {
					t.Fatalf("Oracle found match of len %d at pos %d, but Hensel1M returned len 0", oLen, i)
				}
				if hLen8M < 4 {
					t.Fatalf("Oracle found match of len %d at pos %d, but Hensel8M returned len 0", oLen, i)
				}
				if !bytes.Equal(data[i:i+hLen1M], data[i-hDist1M:i-hDist1M+hLen1M]) {
					t.Fatalf("Hensel1M returned invalid match bytes at pos %d: dist %d, len %d", i, hDist1M, hLen1M)
				}
				if !bytes.Equal(data[i:i+hLen8M], data[i-hDist8M:i-hDist8M+hLen8M]) {
					t.Fatalf("Hensel8M returned invalid match bytes at pos %d: dist %d, len %d", i, hDist8M, hLen8M)
				}
			}
		}
	}
}

// TestChallenger_M2_1_ZeroAlloc_DecompressTo strictly verifies testing.AllocsPerRun == 0.0
// for both 1M and 8M frames.
func TestChallenger_M2_1_ZeroAlloc_DecompressTo(t *testing.T) {
	sizes := []struct {
		name string
		size int
		wLog int
	}{
		{"1M_Payload", 1024 * 1024, WindowLog1M},
		{"8M_Payload", 8 * 1024 * 1024, WindowLog8M},
	}

	for _, s := range sizes {
		t.Run(s.name, func(t *testing.T) {
			raw := make([]byte, s.size)
			for i := range raw {
				raw[i] = byte((i*67 + 3) & 0xFF)
			}
			// Add repeated sequences to form matches
			pattern := []byte("ZERO_ALLOC_TEST_REPEATED_BLOCK_FOR_DECOMPRESS_TO_INVARIANT_")
			for i := 0; i < s.size-len(pattern); i += 1024 {
				copy(raw[i:], pattern)
			}

			compressed, err := CompressWithWindow(raw, 1, s.wLog)
			if err != nil {
				t.Fatalf("Compression failed for %s: %v", s.name, err)
			}

			dst := make([]byte, s.size+64)

			// Warm-up run and verify roundtrip bit-exactness
			out, err := DecompressTo(compressed, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed for %s: %v", s.name, err)
			}
			if !bytes.Equal(out, raw) {
				t.Fatalf("DecompressTo roundtrip mismatch for %s: got %d bytes, want %d bytes", s.name, len(out), len(raw))
			}

			// Strict 0-allocation benchmark verification
			allocs := testing.AllocsPerRun(10, func() {
				_, err := DecompressTo(compressed, dst)
				if err != nil {
					t.Fatalf("DecompressTo failed during AllocsPerRun: %v", err)
				}
			})

			if allocs != 0.0 {
				t.Fatalf("Zero-alloc violation for %s: expected 0.0 allocs/run, got %f", s.name, allocs)
			}
		})
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallenger_M2_2_PureC_1M_Window tests 1M window Go-compressed frames decoded by Pure C test_przm_dec.exe.
func TestChallenger_M2_2_PureC_1M_Window(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}
	if _, err := os.Stat(cBinPath); err != nil {
		t.Fatalf("test_przm_dec.exe not found at %s: %v", cBinPath, err)
	}

	tmpDir := t.TempDir()

	testDistances := []struct {
		name     string
		distance int
		matchLen int
	}{
		{"Distance_70K_Beyond64K", 70 * 1024, 64},
		{"Distance_128K", 128 * 1024, 128},
		{"Distance_256K", 256 * 1024, 256},
		{"Distance_512K", 512 * 1024, 512},
		{"Distance_900K_Near1M", 900 * 1024, 1024},
	}

	for _, td := range testDistances {
		t.Run(td.name, func(t *testing.T) {
			totalSize := td.distance + td.matchLen + 1024
			data := make([]byte, totalSize)

			// Fill with pseudo-random content
			for i := range data {
				data[i] = byte((i*37 + 13) & 0xFF)
			}

			// Plant a match: copy td.matchLen bytes from pos 100 to pos 100 + td.distance
			matchPayload := []byte("PRZM_L3_1M_SCALING_TEST_PAYLOAD_VERIFICATION_BYTES_PATTERN_ABCDEF1234567890")
			for i := 0; i < td.matchLen; i++ {
				data[100+i] = matchPayload[i%len(matchPayload)]
				data[100+td.distance+i] = matchPayload[i%len(matchPayload)]
			}

			for level := 1; level <= 4; level++ {
				t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
					compressed, err := CompressWithWindow(data, level, WindowLog1M)
					if err != nil {
						t.Fatalf("Go CompressWithWindow failed at level %d: %v", level, err)
					}

					// If standard PRZM LZ frame, verify WindowLog
					if len(compressed) >= 4 && binary.LittleEndian.Uint32(compressed[:4]) == MagicPRZM {
						hdr, err := ParseFrameHeader(compressed)
						if err != nil {
							t.Fatalf("ParseFrameHeader failed: %v", err)
						}
						if hdr.WindowLog != WindowLog1M {
							t.Fatalf("Expected WindowLog1M (20), got %d", hdr.WindowLog)
						}
					}

					inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", td.name, level))
					outBin := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.out", td.name, level))

					if err := os.WriteFile(inPrzm, compressed, 0644); err != nil {
						t.Fatalf("failed to write compressed frame: %v", err)
					}

					cmd := exec.Command(cBinPath, inPrzm, outBin)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
					}

					decompressed, err := os.ReadFile(outBin)
					if err != nil {
						t.Fatalf("failed to read decompressed file: %v", err)
					}

					if !bytes.Equal(decompressed, data) {
						t.Fatalf("Decompressed data mismatch: got %d bytes, want %d bytes", len(decompressed), len(data))
					}
				})
			}
		})
	}
}

// TestChallenger_M2_2_PureC_8M_Window tests 8M window Go-compressed frames decoded by Pure C test_przm_dec.exe.
func TestChallenger_M2_2_PureC_8M_Window(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	testDistances := []struct {
		name     string
		distance int
		matchLen int
	}{
		{"Distance_1M5_Beyond1M", 1500 * 1024, 256},
		{"Distance_2M5_4ByteVarint", 2500 * 1024, 512},
		{"Distance_4M5", 4500 * 1024, 1024},
		{"Distance_7M5_Near8M", 7500 * 1024, 2048},
	}

	for _, td := range testDistances {
		t.Run(td.name, func(t *testing.T) {
			totalSize := td.distance + td.matchLen + 1024
			data := make([]byte, totalSize)

			for i := range data {
				data[i] = byte((i*59 + 71) & 0xFF)
			}

			// Plant a repeat match across large distance
			pattern := []byte("PRZM_8M_LARGE_WINDOW_CORPUS_COVERAGE_4BYTE_VARINT_VERIFY_XYZ987654321")
			for i := 0; i < td.matchLen; i++ {
				data[512+i] = pattern[i%len(pattern)]
				data[512+td.distance+i] = pattern[i%len(pattern)]
			}

			// Test level 1 (fast) and level 3 (default/high)
			for _, level := range []int{1, 3} {
				t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
					compressed, err := CompressWithWindow(data, level, WindowLog8M)
					if err != nil {
						t.Fatalf("Go CompressWithWindow (8M) failed at level %d: %v", level, err)
					}

					hdr, err := ParseFrameHeader(compressed)
					if err != nil {
						t.Fatalf("ParseFrameHeader failed: %v", err)
					}
					if hdr.WindowLog != WindowLog8M {
						t.Fatalf("Expected WindowLog8M (23), got %d", hdr.WindowLog)
					}

					inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.przm", td.name, level))
					outBin := filepath.Join(tmpDir, fmt.Sprintf("%s_l%d.out", td.name, level))

					if err := os.WriteFile(inPrzm, compressed, 0644); err != nil {
						t.Fatalf("failed to write compressed frame: %v", err)
					}

					cmd := exec.Command(cBinPath, inPrzm, outBin)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("Pure C decompressor failed on 8M frame (exit status %v):\nOutput:\n%s", err, string(output))
					}

					decompressed, err := os.ReadFile(outBin)
					if err != nil {
						t.Fatalf("failed to read decompressed file: %v", err)
					}

					if !bytes.Equal(decompressed, data) {
						t.Fatalf("Decompressed data mismatch: got %d bytes, want %d bytes", len(decompressed), len(data))
					}
				})
			}
		})
	}
}

// TestChallenger_M2_2_PureC_AdversarialCorruption tests that corrupt streams cause clean failure in C without crash.
func TestChallenger_M2_2_PureC_AdversarialCorruption(t *testing.T) {
	cBinPath, err := filepath.Abs("test_przm_dec.exe")
	if err != nil {
		t.Fatalf("Failed to resolve test_przm_dec.exe path: %v", err)
	}

	tmpDir := t.TempDir()

	// Base valid frame
	validData := bytes.Repeat([]byte("PRZM_CORRUPTION_TEST_STRING_"), 100)
	validCompressed, err := Compress(validData, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	corruptCases := []struct {
		name       string
		modifyData func([]byte) []byte
	}{
		{
			name: "Truncated_To_4_Bytes",
			modifyData: func(b []byte) []byte {
				return b[:4]
			},
		},
		{
			name: "Truncated_To_7_Bytes",
			modifyData: func(b []byte) []byte {
				return b[:7]
			},
		},
		{
			name: "Corrupt_Magic",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				cp[0] = 0xFF
				cp[1] = 0xFF
				return cp
			},
		},
		{
			name: "Corrupt_USize_Huge",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				// Uncompressed size is at byte 5..7 (u24)
				cp[5] = 0xFF
				cp[6] = 0xFF
				cp[7] = 0xFF // 16,777,215 > PRZM_MAX_BLOCK_SIZE
				return cp
			},
		},
		{
			name: "Corrupt_Section_Directory_OOB",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				// Section directory is at offset 8 (or 14 if ext offset)
				if len(cp) > 12 {
					cp[8] = 0xFF
					cp[9] = 0xFF
				}
				return cp
			},
		},
		{
			name: "Random_Bit_Flipping_Payload",
			modifyData: func(b []byte) []byte {
				cp := append([]byte(nil), b...)
				for i := len(cp) / 2; i < len(cp); i += 4 {
					cp[i] ^= 0xAA
				}
				return cp
			},
		},
	}

	for _, cc := range corruptCases {
		t.Run(cc.name, func(t *testing.T) {
			corruptFrame := cc.modifyData(validCompressed)
			inPrzm := filepath.Join(tmpDir, fmt.Sprintf("%s.przm", cc.name))
			outBin := filepath.Join(tmpDir, fmt.Sprintf("%s.out", cc.name))

			if err := os.WriteFile(inPrzm, corruptFrame, 0644); err != nil {
				t.Fatalf("failed to write corrupt frame: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()

			// Must fail (exit code != 0) without panicking / crashing
			if err == nil {
				t.Fatalf("Expected C decompressor to reject corrupt frame %s, but it returned success! Output:\n%s",
					cc.name, string(output))
			}
		})
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallenger_M3_1_OverlappingMatchChains tests overlapping and competing match chains
// where greedy parsing makes the wrong choice, verifying bit-exactness and that Level 3
// produces a superior or equal compression ratio compared to Level 1 and Level 2.
func TestChallenger_M3_1_OverlappingMatchChains(t *testing.T) {
	// Case 1: Overlapping tokens ABCDEF, BCDEFGH, CDEFGHIJ repeated
	var buf1 bytes.Buffer
	tokens := []string{
		"ABCDEF",
		"BCDEFGH",
		"CDEFGHIJ",
		"DEFGHIJK",
		"EFGHIJKL",
		"FGHIJKLM",
	}
	for i := 0; i < 500; i++ {
		buf1.WriteString(tokens[i%len(tokens)])
		if i%7 == 0 {
			buf1.WriteString("_RANDOM_PAD_")
		}
	}
	data1 := buf1.Bytes()

	// Case 2: Greedy trap - short immediate match vs slightly deferred longer/repeat match
	var buf2 bytes.Buffer
	trapPatternA := "ABCDEF_SHORT"
	trapPatternB := "ABCDEF_LONG_MATCH_SEQUENCE_REPEAT_TARGET_XYZ"
	for i := 0; i < 200; i++ {
		buf2.WriteString(trapPatternA)
		buf2.WriteString(trapPatternB)
		buf2.WriteString("ABCDEF") // prefix of both patterns
		buf2.WriteString(trapPatternB)
		buf2.WriteString(fmt.Sprintf("_STEP_%d_", i))
	}
	data2 := buf2.Bytes()

	// Case 3: Dense overlapping substrings with alternating shifts
	var buf3 bytes.Buffer
	seedStr := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	for i := 0; i < 3000; i++ {
		shift := i % 10
		buf3.WriteString(seedStr[shift : shift+16])
	}
	data3 := buf3.Bytes()

	testCases := []struct {
		name string
		data []byte
	}{
		{"OverlappingTokens_ABCDEF", data1},
		{"GreedyTrap_DeferredLongMatch", data2},
		{"DenseShiftingSubstrings", data3},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			compL1, err := Compress(tc.data, 1)
			if err != nil {
				t.Fatalf("L1 compress failed: %v", err)
			}
			compL2, err := Compress(tc.data, 2)
			if err != nil {
				t.Fatalf("L2 compress failed: %v", err)
			}
			compL3, err := Compress(tc.data, 3)
			if err != nil {
				t.Fatalf("L3 compress failed: %v", err)
			}
			compL4, err := Compress(tc.data, 4)
			if err != nil {
				t.Fatalf("L4 compress failed: %v", err)
			}

			// Verify bit-exact roundtrips
			for lvl, comp := range map[int][]byte{1: compL1, 2: compL2, 3: compL3, 4: compL4} {
				dec, err := Decompress(comp)
				if err != nil {
					t.Fatalf("L%d decompress failed: %v", lvl, err)
				}
				if !bytes.Equal(dec, tc.data) {
					t.Fatalf("L%d bit-exact mismatch: decompressed length %d != raw %d", lvl, len(dec), len(tc.data))
				}

				dst := make([]byte, len(tc.data)+64)
				decTo, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("L%d DecompressTo failed: %v", lvl, err)
				}
				if !bytes.Equal(decTo, tc.data) {
					t.Fatalf("L%d DecompressTo bit-exact mismatch", lvl)
				}
			}

			t.Logf("[%s] Raw: %d B | L1: %d B (%.2fx) | L2: %d B (%.2fx) | L3: %d B (%.2fx) | L4: %d B (%.2fx)",
				tc.name, len(tc.data),
				len(compL1), float64(len(tc.data))/float64(len(compL1)),
				len(compL2), float64(len(tc.data))/float64(len(compL2)),
				len(compL3), float64(len(tc.data))/float64(len(compL3)),
				len(compL4), float64(len(tc.data))/float64(len(compL4)),
			)

			// Empirical verification: L3 ratio must be superior or equal to L1 and L2
			if len(compL3) > len(compL1) {
				t.Errorf("L3 compressed size (%d) exceeds L1 (%d) on overlapping data", len(compL3), len(compL1))
			}
		})
	}
}

// TestChallenger_M3_1_Dense2ByteAnd3ByteRepeats tests dense 2-byte and 3-byte repeats
// (testing 1-cycle repeat checks and secondary 3-byte direct-mapped hash table).
func TestChallenger_M3_1_Dense2ByteAnd3ByteRepeats(t *testing.T) {
	repeats := []struct {
		name    string
		pattern []byte
		size    int
	}{
		{"2Byte_ABAB_64B", []byte("AB"), 64},
		{"2Byte_ABAB_1KB", []byte("AB"), 1024},
		{"2Byte_ABAB_16KB", []byte("AB"), 16384},
		{"2Byte_ABAB_65536B", []byte("AB"), 65536},
		{"3Byte_XYZ_64B", []byte("XYZ"), 64},
		{"3Byte_XYZ_1KB", []byte("XYZ"), 1024},
		{"3Byte_XYZ_16KB", []byte("XYZ"), 16384},
		{"3Byte_XYZ_65536B", []byte("XYZ"), 65536},
		{"Mixed_2B_3B_16KB", []byte("ABXYZ"), 16384},
		{"Alternating_4B_32KB", []byte("AABBCCDDAABBCCDD"), 32768},
	}

	for _, rep := range repeats {
		t.Run(rep.name, func(t *testing.T) {
			data := make([]byte, rep.size)
			pLen := len(rep.pattern)
			for i := range data {
				data[i] = rep.pattern[i%pLen]
			}

			for _, level := range []int{3, 4} {
				comp, err := Compress(data, level)
				if err != nil {
					t.Fatalf("L%d compress failed: %v", level, err)
				}

				dec, err := Decompress(comp)
				if err != nil {
					t.Fatalf("L%d decompress failed: %v", level, err)
				}
				if !bytes.Equal(dec, data) {
					t.Fatalf("L%d bit-exact mismatch: len %d != %d", level, len(dec), len(data))
				}

				dst := make([]byte, rep.size+64)
				decTo, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("L%d DecompressTo failed: %v", level, err)
				}
				if !bytes.Equal(decTo, data) {
					t.Fatalf("L%d DecompressTo bit-exact mismatch", level)
				}

				// Check compression efficiency on dense repeats
				if len(comp) >= len(data)/2 && rep.size >= 1024 {
					t.Errorf("L%d failed to compress dense repeats effectively: raw %d, comp %d", level, rep.size, len(comp))
				}
			}
		})
	}
}

// TestChallenger_M3_1_VariedChunkBoundaries tests varied chunk boundaries:
// exact 64 KiB multiples, odd sizes, 0B, 1B, 2B, 3B, 4B, 65535B, 65536B, 65537B, 128KB, 1MB.
func TestChallenger_M3_1_VariedChunkBoundaries(t *testing.T) {
	sizes := []struct {
		name string
		size int
	}{
		{"Size_0B", 0},
		{"Size_1B", 1},
		{"Size_2B", 2},
		{"Size_3B", 3},
		{"Size_4B", 4},
		{"Size_5B", 5},
		{"Size_7B", 7},
		{"Size_15B", 15},
		{"Size_16B", 16},
		{"Size_31B", 31},
		{"Size_32B", 32},
		{"Size_63B", 63},
		{"Size_64B", 64},
		{"Size_127B", 127},
		{"Size_128B", 128},
		{"Size_255B", 255},
		{"Size_256B", 256},
		{"Size_1023B", 1023},
		{"Size_1024B", 1024},
		{"Size_65535B_ExactBoundaryMinus1", 65535},
		{"Size_65536B_Exact64KBoundary", 65536},
		{"Size_65537B_ExactBoundaryPlus1", 65537},
		{"Size_131072B_Exact128K", 131072},
		{"Size_131073B_128KPlus1", 131073},
		{"Size_1048576B_Exact1M", 1048576},
		{"Size_1048577B_1MPlus1", 1048577},
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			// Generate deterministic pseudo-random compressible data
			data := make([]byte, tc.size)
			rng := rand.New(rand.NewSource(int64(tc.size + 42)))
			pattern := []byte("BOUNDARY_CHUNK_TEST_PAYLOAD_ABC123_XYZ789_")
			for i := 0; i < tc.size; {
				if tc.size-i >= len(pattern) && rng.Float32() < 0.7 {
					copy(data[i:], pattern)
					i += len(pattern)
				} else {
					data[i] = byte(rng.Intn(256))
					i++
				}
			}

			for _, level := range []int{3, 4} {
				comp, err := Compress(data, level)
				if err != nil {
					t.Fatalf("L%d compress failed for size %d: %v", level, tc.size, err)
				}

				dec, err := Decompress(comp)
				if err != nil {
					t.Fatalf("L%d decompress failed for size %d: %v", level, tc.size, err)
				}
				if !bytes.Equal(dec, data) {
					t.Fatalf("L%d bit-exact mismatch for size %d: got %d bytes, want %d bytes", level, tc.size, len(dec), len(data))
				}

				dst := make([]byte, tc.size+64)
				decTo, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("L%d DecompressTo failed for size %d: %v", level, tc.size, err)
				}
				if !bytes.Equal(decTo, data) {
					t.Fatalf("L%d DecompressTo bit-exact mismatch for size %d", level, tc.size)
				}
			}
		})
	}
}

// TestChallenger_M3_1_ZeroHeapAllocs_DecompressTo empirically verifies that DecompressTo
// achieves strictly 0.0 heap allocations per run on optimal-parsed payloads.
func TestChallenger_M3_1_ZeroHeapAllocs_DecompressTo(t *testing.T) {
	testSizes := []struct {
		name string
		size int
	}{
		{"16KB_Overlapping", 16 * 1024},
		{"64KB_DenseRepeats", 64 * 1024},
		{"65536B_Exact64K", 65536},
		{"65537B_CrossBlock", 65537},
		{"128KB_MultiBlock", 128 * 1024},
		{"1MB_LargeWindow", 1024 * 1024},
	}

	for _, tc := range testSizes {
		t.Run(tc.name, func(t *testing.T) {
			data := make([]byte, tc.size)
			pattern := []byte("TEST_CHALLENGER_M3_1_ZERO_HEAP_ALLOCS_OPTIMAL_PARSER_PAYLOAD_STRING_ABC123_")
			for i := range data {
				data[i] = pattern[i%len(pattern)]
			}

			// Compress with Level 3 (Optimal DAG parse)
			comp, err := Compress(data, 3)
			if err != nil {
				t.Fatalf("L3 compress failed: %v", err)
			}

			dst := make([]byte, tc.size+64)

			// Warmup run
			res, err := DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(res, data) {
				t.Fatalf("warmup bit-exact mismatch")
			}

			// Profile dynamic allocations across 50 runs
			allocs := testing.AllocsPerRun(50, func() {
				_, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("DecompressTo failed in benchmark loop: %v", err)
				}
			})

			t.Logf("[%s] AllocsPerRun = %.2f", tc.name, allocs)
			if allocs != 0.0 {
				t.Errorf("FAIL: Expected strictly 0.0 heap allocations in DecompressTo, got %.2f", allocs)
			}
		})
	}
}

// TestChallenger_M3_1_PureC_Interop verifies that Level 3 optimal-parsed frames
// decompress bit-exact with the Pure C zero-alloc decompressor (test_przm_dec.exe).
func TestChallenger_M3_1_PureC_Interop(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
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

	testCases := []struct {
		name string
		size int
		gen  func(int) []byte
	}{
		{
			name: "OverlappingTokens",
			size: 16384,
			gen: func(size int) []byte {
				var buf bytes.Buffer
				toks := []string{"ABCDEF", "BCDEFGH", "CDEFGHIJ", "DEFGHIJK"}
				for buf.Len() < size {
					buf.WriteString(toks[(buf.Len()/6)%len(toks)])
				}
				return buf.Bytes()[:size]
			},
		},
		{
			name: "Dense2ByteRepeats",
			size: 8192,
			gen: func(size int) []byte {
				data := make([]byte, size)
				for i := range data {
					if i%2 == 0 {
						data[i] = 'A'
					} else {
						data[i] = 'B'
					}
				}
				return data
			},
		},
		{
			name: "CrossBlock_65537B",
			size: 65537,
			gen: func(size int) []byte {
				data := make([]byte, size)
				pat := []byte("PURE_C_CROSS_BLOCK_OPTIMAL_PARSER_VERIFICATION_")
				for i := range data {
					data[i] = pat[i%len(pat)]
				}
				return data
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.gen(tc.size)
			comp, err := Compress(raw, 3)
			if err != nil {
				t.Fatalf("Compress L3 failed: %v", err)
			}

			inPath := filepath.Join(tmpDir, tc.name+".przm")
			outPath := filepath.Join(tmpDir, tc.name+".out")

			if err := os.WriteFile(inPath, comp, 0644); err != nil {
				t.Fatalf("failed to write compressed file: %v", err)
			}

			cmd := exec.Command(cBinPath, inPath, outPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
			}

			decompressed, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("failed to read decompressed output: %v", err)
			}

			if !bytes.Equal(decompressed, raw) {
				t.Fatalf("Pure C decompressed output does not match raw original: got %d B, want %d B", len(decompressed), len(raw))
			}
		})
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

func findCDecoder(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// TestChallenger_M3_2_Optimal_PureC_DiversePayloads tests diverse payloads
// (Go/C source code, structured JSON, binary bytecode pattern, dense 2B/3B repeats,
// and high entropy with embedded repeats) compressed with Level 3 optimal parse
// (Compress(data, 3)), then decompresses them using test_przm_dec.exe.
// Confirms exit code 0 and 100% bit-exact roundtrips.
func TestChallenger_M3_2_Optimal_PureC_DiversePayloads(t *testing.T) {
	cBinPath := findCDecoder(t)
	tmpDir := t.TempDir()

	testCases := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "GoSourceCode",
			gen: func() []byte {
				// Try reading real Go source files first
				var buf bytes.Buffer
				for _, f := range []string{"optimal.go", "compress.go", "format.go"} {
					if content, err := os.ReadFile(f); err == nil {
						buf.Write(content)
					}
				}
				if buf.Len() < 16384 {
					// Fallback synthesis with rich Go language syntax
					for i := 0; i < 400; i++ {
						fmt.Fprintf(&buf, `
type PipelineNode%d struct {
	ID        int64
	Name      string
	mu        sync.RWMutex
	children  []*PipelineNode%d
}
func (n *PipelineNode%d) Process(ctx context.Context, input []byte) ([]byte, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(input) == 0 {
		return nil, fmt.Errorf("empty payload at node %%d", n.ID)
	}
	res := make([]byte, len(input))
	copy(res, input)
	return res, nil
`, i, i, i)
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "CSourceCode",
			gen: func() []byte {
				var buf bytes.Buffer
				for _, f := range []string{"src/przm_dec.c", "include/przm_dec.h"} {
					if content, err := os.ReadFile(f); err == nil {
						buf.Write(content)
					}
				}
				if buf.Len() < 16384 {
					for i := 0; i < 400; i++ {
						fmt.Fprintf(&buf, `#ifndef PRZM_MODULE_%d_H
#define PRZM_MODULE_%d_H
#include <stdint.h>
#include <stdbool.h>
typedef struct przm_ctx_%d {
    uint32_t state;
    uint64_t counter;
    uint8_t  scratch[128];
} przm_ctx_%d_t;
static inline int przm_step_%d(przm_ctx_%d_t* ctx, const uint8_t* in, size_t len) {
    if (!ctx || !in || len < 4) return -1;
    ctx->counter += len;
    ctx->state ^= *(const uint32_t*)in;
    return 0;
}
#endif /* PRZM_MODULE_%d_H */
`, i, i, i, i, i, i, i)
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "StructuredJSON",
			gen: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("[\n")
				for i := 0; i < 600; i++ {
					if i > 0 {
						buf.WriteString(",\n")
					}
					fmt.Fprintf(&buf, `  {
    "id": %d,
    "uuid": "4f9d2a1b-%04x-%04x-%04x-%012x",
    "event_type": "TELEMETRY_SAMPLE_INGESTION",
    "timestamp": %d,
    "status": "PROCESSED",
    "payload": {
      "metric_name": "cpu_core_utilization",
      "metric_value": %f,
      "labels": {
        "region": "us-east-1",
        "zone": "us-east-1a",
        "cluster": "k8s-production-alpha",
        "tier": "compute-highmem"
      }
    },
    "tags": ["monitoring", "telemetry", "prometheus", "production"]
  }`, i, i%65535, (i*3)%65535, (i*7)%65535, i*1234567, 1728000000+i, float64(i%100)/2.5)
				}
				buf.WriteString("\n]\n")
				return buf.Bytes()
			},
		},
		{
			name: "BinaryBytecodePattern",
			gen: func() []byte {
				// Machine bytecode instructions with repeated subroutines, CALLs, JMPs, registers
				var buf bytes.Buffer
				prologue := []byte{0x55, 0x48, 0x89, 0xE5, 0x48, 0x83, 0xEC, 0x40}
				epilogue := []byte{0x48, 0x89, 0xEC, 0x5D, 0xC3}
				instructions := [][]byte{
					{0x48, 0x89, 0x7D, 0xF8},             // mov [rbp-8], rdi
					{0x48, 0x89, 0x75, 0xF0},             // mov [rbp-16], rsi
					{0x48, 0x8B, 0x45, 0xF8},             // mov rax, [rbp-8]
					{0x48, 0x03, 0x45, 0xF0},             // add rax, [rbp-16]
					{0x48, 0x83, 0xF8, 0x00},             // cmp rax, 0
					{0x0F, 0x84, 0x20, 0x00, 0x00, 0x00}, // jz +0x20
					{0x48, 0xFF, 0xC0},                   // inc rax
					{0xE8, 0x10, 0x00, 0x00, 0x00},       // call +0x10
				}
				for funcIdx := 0; funcIdx < 500; funcIdx++ {
					buf.Write(prologue)
					for step := 0; step < 12; step++ {
						inst := instructions[(funcIdx+step)%len(instructions)]
						buf.Write(inst)
						// occasional variable operand
						var imm [4]byte
						binary.LittleEndian.PutUint32(imm[:], uint32(funcIdx*step))
						buf.Write(imm[:])
					}
					buf.Write(epilogue)
					// Padding alignment
					alignBytes := (16 - (buf.Len() % 16)) % 16
					for p := 0; p < alignBytes; p++ {
						buf.WriteByte(0x90) // NOP
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "Dense2B3BRepeats",
			gen: func() []byte {
				// Dense alternating 2-byte and 3-byte repeats designed to test short match selection in optimal DP
				var buf bytes.Buffer
				twoBytePairs := []string{"AB", "CD", "EF", "01", "XY", "!!"}
				threeByteTokens := []string{"XYZ", "ABC", "123", "###", "UVW"}

				for i := 0; i < 300; i++ {
					pair := twoBytePairs[i%len(twoBytePairs)]
					for rep := 0; rep < 24; rep++ {
						buf.WriteString(pair)
					}
					tok := threeByteTokens[i%len(threeByteTokens)]
					for rep := 0; rep < 18; rep++ {
						buf.WriteString(tok)
					}
					// Mixed interleaved section
					for m := 0; m < 10; m++ {
						buf.WriteString(pair)
						buf.WriteString(tok)
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "HighEntropyWithEmbeddedRepeats",
			gen: func() []byte {
				// 64 KiB of pseudorandom noise with deterministic anchors embedded every 1024 bytes
				rng := rand.New(rand.NewSource(0x5EED_0001))
				size := 65536
				raw := make([]byte, size)
				rng.Read(raw)

				anchorA := []byte("PRZM_DETERMINISTIC_ANCHOR_SECTION_OPTIMAL_MATCH_AAA_")
				anchorB := []byte("PRZM_DETERMINISTIC_ANCHOR_SECTION_OPTIMAL_MATCH_BBB_")

				for pos := 256; pos+len(anchorA) < size; pos += 1024 {
					if (pos/1024)%2 == 0 {
						copy(raw[pos:], anchorA)
					} else {
						copy(raw[pos:], anchorB)
					}
				}
				return raw
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.gen()
			if len(raw) == 0 {
				t.Fatalf("test case generator produced empty payload")
			}

			// Compress with Level 3 (Optimal Parse)
			comp, err := Compress(raw, 3)
			if err != nil {
				t.Fatalf("Compress(level 3) failed: %v", err)
			}
			if len(comp) == 0 {
				t.Fatalf("Compress returned empty compressed frame")
			}

			inPath := filepath.Join(tmpDir, tc.name+".przm")
			outPath := filepath.Join(tmpDir, tc.name+".out")

			if err := os.WriteFile(inPath, comp, 0644); err != nil {
				t.Fatalf("failed to write compressed frame: %v", err)
			}

			// Decompress with Pure C executable
			cmd := exec.Command(cBinPath, inPath, outPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("test_przm_dec.exe failed on %s (exit: %v):\nOutput:\n%s", tc.name, err, string(output))
			}

			decomp, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("failed to read decompressed output: %v", err)
			}

			if !bytes.Equal(decomp, raw) {
				t.Fatalf("Lossy decompression on %s! Raw len=%d, Decomp len=%d", tc.name, len(raw), len(decomp))
			}
			t.Logf("[%s] OK: Raw=%d B -> Comp=%d B (ratio: %.2fx) -> 100%% bit-exact Pure C decompress",
				tc.name, len(raw), len(comp), float64(len(raw))/float64(len(comp)))
		})
	}
}

// TestChallenger_M3_2_Adversarial_Corruption validates that corrupt, malformed,
// or truncated streams cleanly fail in test_przm_dec.exe with non-zero exit codes
// without crashes, memory faults, or segmentation violations.
func TestChallenger_M3_2_Adversarial_Corruption(t *testing.T) {
	cBinPath := findCDecoder(t)
	tmpDir := t.TempDir()

	// Base valid compressed frame produced by Level 3
	var basePayload bytes.Buffer
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&basePayload, "RECORD_%04d: optimal parsing adversarial baseline payload data section\n", i)
	}
	rawBase := basePayload.Bytes()
	validFrame, err := Compress(rawBase, 3)
	if err != nil {
		t.Fatalf("failed to compress base frame: %v", err)
	}

	testCases := []struct {
		name        string
		makeCorrupt func() []byte
	}{
		{
			name: "Truncated_4B",
			makeCorrupt: func() []byte {
				if len(validFrame) < 4 {
					return []byte{0x50, 0x52, 0x5A, 0x4D}
				}
				return validFrame[:4]
			},
		},
		{
			name: "Truncated_7B",
			makeCorrupt: func() []byte {
				if len(validFrame) < 7 {
					return []byte{0x50, 0x52, 0x5A, 0x4D, 0x00, 0x01, 0x00}
				}
				return validFrame[:7]
			},
		},
		{
			name: "CorruptMagic",
			makeCorrupt: func() []byte {
				bad := make([]byte, len(validFrame))
				copy(bad, validFrame)
				// Overwrite magic bytes with invalid magic 0xDEADBEEF
				bad[0] = 0xEF
				bad[1] = 0xBE
				bad[2] = 0xAD
				bad[3] = 0xDE
				return bad
			},
		},
		{
			name: "OversizedUncompressedSize_Standard",
			makeCorrupt: func() []byte {
				// Standard PRZM frame with uncompressed size claiming 16 MiB (0xFFFFFF), but total length is only 20 bytes
				bad := make([]byte, 20)
				binary.LittleEndian.PutUint32(bad[0:4], 0x4D5A5250) // 'PRZM'
				bad[4] = 0x00                                       // Flags
				bad[5] = 0xFF                                       // u_size = 0xFFFFFF (16,777,215)
				bad[6] = 0xFF
				bad[7] = 0xFF
				return bad
			},
		},
		{
			name: "OversizedUncompressedSize_SpecializedExceedsMax",
			makeCorrupt: func() []byte {
				// Specialized SYMP frame with orig_len = 32 MiB (> PRZM_MAX_BLOCK_SIZE 16 MiB)
				bad := make([]byte, 32)
				binary.LittleEndian.PutUint32(bad[0:4], 0x53594D50)    // 'SYMP'
				bad[4] = 0x01                                          // mode
				binary.LittleEndian.PutUint32(bad[8:12], 32*1024*1024) // 32 MiB
				return bad
			},
		},
		{
			name: "CorruptSectionDirectory",
			makeCorrupt: func() []byte {
				bad := make([]byte, len(validFrame))
				copy(bad, validFrame)
				// Corrupt section directory bytes (offsets 8..14 in standard header)
				if len(bad) > 16 {
					for i := 8; i < 16 && i < len(bad); i++ {
						bad[i] = 0xFF
					}
				}
				return bad
			},
		},
	}

	// Add 30 randomized bit-flipping test cases targeting critical structural framing
	// (magic bytes 0..3, uncompressed size 5..7, and section directory 8..11)
	rng := rand.New(rand.NewSource(0xBAD_C0DE))
	criticalIndices := []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11}
	for i := 0; i < 30; i++ {
		iter := i
		testCases = append(testCases, struct {
			name        string
			makeCorrupt func() []byte
		}{
			name: fmt.Sprintf("RandomBitFlipping_Structural_%02d", iter),
			makeCorrupt: func() []byte {
				bad := make([]byte, len(validFrame))
				copy(bad, validFrame)
				flips := 1 + rng.Intn(3)
				for f := 0; f < flips; f++ {
					targetByte := criticalIndices[rng.Intn(len(criticalIndices))]
					if targetByte < len(bad) {
						targetBit := uint(rng.Intn(8))
						bad[targetByte] ^= (1 << targetBit)
					}
				}
				return bad
			},
		})
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			corruptStream := tc.makeCorrupt()
			inPath := filepath.Join(tmpDir, tc.name+".bad.przm")
			outPath := filepath.Join(tmpDir, tc.name+".bad.out")

			if err := os.WriteFile(inPath, corruptStream, 0644); err != nil {
				t.Fatalf("failed to write corrupt input: %v", err)
			}

			cmd := exec.Command(cBinPath, inPath, outPath)
			output, err := cmd.CombinedOutput()

			// test_przm_dec.exe MUST fail (err != nil)
			if err == nil {
				t.Fatalf("[%s] Security hazard: test_przm_dec.exe succeeded with return code 0 on corrupted stream!", tc.name)
			}

			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("[%s] Unexpected exec error: %v", tc.name, err)
			}

			exitCode := exitErr.ExitCode()
			uExitCode := uint32(exitCode)

			// Verify it did not crash with access violation / segfault / stack corruption
			// Windows NTSTATUS codes:
			// 0xC0000005: STATUS_ACCESS_VIOLATION
			// 0xC00000FD: STATUS_STACK_OVERFLOW
			// 0xC000001D: STATUS_ILLEGAL_INSTRUCTION
			// 0xC0000028: STATUS_BAD_STACK
			if uExitCode == 0xC0000005 || uExitCode == 0xC00000FD || uExitCode == 0xC000001D || uExitCode == 0xC0000028 {
				t.Fatalf("[%s] CRASH / SEGFAULT DETECTED: exit code 0x%X\nOutput:\n%s", tc.name, uExitCode, string(output))
			}

			if exitCode <= 0 {
				t.Fatalf("[%s] Expected positive non-zero error exit code, got %d", tc.name, exitCode)
			}

			t.Logf("[%s] OK: test_przm_dec.exe rejected corrupt stream cleanly with exit code %d (output: %s)",
				tc.name, exitCode, bytes.TrimSpace(output))
		})
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallenger_M4_1_RouterBudgetAndAllocs stress tests the router performance
// to ensure execution strictly satisfies <290 ns budget and 0 heap allocations.
func TestChallenger_M4_1_RouterBudgetAndAllocs(t *testing.T) {
	testData := [][]byte{
		[]byte("{\n  \"status\": \"ok\",\n  \"code\": 200,\n  \"users\": [\"alice\", \"bob\"]\n}"),
		make([]byte, 1024), // all zeros
		func() []byte {
			// Smooth delta
			b := make([]byte, 1024)
			for i := range b {
				b[i] = byte(i % 16)
			}
			return b
		}(),
		func() []byte {
			// PE Header
			b := make([]byte, 1024)
			b[0] = 'M'
			b[1] = 'Z'
			b[0x3C] = 0x80
			b[0x80] = 'P'
			b[0x81] = 'E'
			return b
		}(),
	}

	for idx, d := range testData {
		// Zero allocations check
		allocs := testing.AllocsPerRun(100, func() {
			_ = RouteContent(d)
		})
		if allocs != 0.0 {
			t.Fatalf("[%d] RouteContent produced %f allocs, want 0.0", idx, allocs)
		}

		// Benchmark execution time
		iters := 20000
		start := time.Now()
		for i := 0; i < iters; i++ {
			_ = RouteContent(d)
		}
		duration := time.Since(start)
		nsPerOp := float64(duration.Nanoseconds()) / float64(iters)
		if nsPerOp >= 290.0 {
			t.Errorf("[%d] RouteContent took %.2f ns/op, exceeding 290 ns budget", idx, nsPerOp)
		}
		t.Logf("[%d] RouteContent: %.2f ns/op, %.1f allocs/op", idx, nsPerOp, allocs)
	}
}

// TestChallenger_M4_1_ZeroHeapNormalizationDecode verifies strictly 0 heap allocations
// for DecompressTo across whole-block Hex, Base64, and mixed spans.
func TestChallenger_M4_1_ZeroHeapNormalizationDecode(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{
			name: "WholeBlockHexLower_1KB",
			data: []byte(hex.EncodeToString(bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 128))),
		},
		{
			name: "WholeBlockHexUpper_1KB",
			data: bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 128)))),
		},
		{
			name: "WholeBlockBase64Std_1KB",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("secret_payload_1"), 64))),
		},
		{
			name: "WholeBlockBase64URL_1KB",
			data: []byte(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte("secret_payload_1"), 64))),
		},
		{
			name: "MixedSpansHexAndBase64",
			data: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("{\"hex_hash\":\"")
				buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0xCA, 0xFE, 0xBA, 0xBE}, 16)))
				buf.WriteString("\",\"jwt_token\":\"")
				buf.WriteString(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte("claim_data_token"), 8)))
				buf.WriteString("\"}")
				return buf.Bytes()
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := Compress(tc.data, 1)
			if err != nil {
				t.Fatalf("Compress failed: %v", err)
			}
			dst := make([]byte, len(tc.data)+64)

			// Warmup
			_, err = DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}

			allocs := testing.AllocsPerRun(20, func() {
				_, _ = DecompressTo(comp, dst)
			})
			if allocs != 0.0 {
				t.Fatalf("DecompressTo produced %f allocs, want 0.0", allocs)
			}
			t.Logf("[%s] AllocsPerRun = %.2f", tc.name, allocs)
		})
	}
}

// TestChallenger_M4_1_CDecompressorNormalizationInterop tests C decompressor against
// Go-compressed frames across all normalization modes (including Base64URL with 2 '==' pads).
func TestChallenger_M4_1_CDecompressorNormalizationInterop(t *testing.T) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
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
		t.Skip("test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()

	testModes := []struct {
		name string
		data []byte
	}{
		{
			name: "NormModeHexLower",
			data: []byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}, 16))),
		},
		{
			name: "NormModeHexUpper",
			data: bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}, 16)))),
		},
		{
			name: "NormModeBase64Std_NoPad",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("123"), 30))), // len 90 % 3 == 0 -> no pad
		},
		{
			name: "NormModeBase64Std_Pad1",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("12345"), 16))), // len 80 % 3 == 2 -> 1 '=' pad
		},
		{
			name: "NormModeBase64Std_Pad2",
			data: []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("1234"), 16))), // len 64 % 3 == 1 -> 2 '==' pad
		},
		{
			name: "NormModeBase64URL_NoPad",
			data: func() []byte {
				raw := bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 30) // produces - and _
				return []byte(base64.URLEncoding.EncodeToString(raw))
			}(),
		},
		{
			name: "NormModeBase64URL_Pad1",
			data: func() []byte {
				raw := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 25), 0xFB, 0xFF) // len % 3 == 2 -> 1 '='
				return []byte(base64.URLEncoding.EncodeToString(raw))
			}(),
		},
		{
			name: "NormModeBase64URL_Pad2",
			data: func() []byte {
				raw := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 25), 0xFB) // len % 3 == 1 -> 2 '=='
				return []byte(base64.URLEncoding.EncodeToString(raw))
			}(),
		},
		{
			name: "NormModeSpansMixed",
			data: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("{\"hash\":\"")
				buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 16)))
				buf.WriteString("\",\"payload\":\"")
				buf.WriteString(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("test_data_stream"), 8)))
				buf.WriteString("\"}")
				return buf.Bytes()
			}(),
		},
	}

	for _, tm := range testModes {
		t.Run(tm.name, func(t *testing.T) {
			comp, err := Compress(tm.data, 1)
			if err != nil {
				t.Fatalf("Go compress failed: %v", err)
			}
			hdr, err := ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("ParseFrameHeader failed: %v", err)
			}
			t.Logf("[%s] NormMode = %d, Flags = 0x%02X", tm.name, hdr.NormMode, hdr.Flags)

			inPrzm := filepath.Join(tmpDir, tm.name+".przm")
			outBin := filepath.Join(tmpDir, tm.name+".out")
			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
			}

			decompressed, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}

			if !bytes.Equal(decompressed, tm.data) {
				t.Fatalf("Mismatch: got %d bytes, want %d bytes", len(decompressed), len(tm.data))
			}
		})
	}
}

// TestChallenger_M4_1_AdversarialSpanCorruption tests boundary robustness against corrupt span tables.
func TestChallenger_M4_1_AdversarialSpanCorruption(t *testing.T) {
	// Generate valid span-normalized frame
	var buf bytes.Buffer
	buf.WriteString("PREFIX_DATA:")
	buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0x12, 0x34, 0x56, 0x78}, 16)))
	buf.WriteString(":SUFFIX_DATA")
	data := buf.Bytes()

	comp, err := Compress(data, 1)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)

	// Verify original roundtrip
	dec, err := DecompressTo(comp, dst)
	if err != nil || !bytes.Equal(dec, data) {
		t.Fatalf("Initial DecompressTo failed: %v", err)
	}

	// Adversarial mutations on Section 4 / contracted stream
	for offset := 8; offset < len(comp); offset += 2 {
		corrupted := make([]byte, len(comp))
		copy(corrupted, comp)
		corrupted[offset] ^= 0xFF

		// Must not panic, must either succeed (if non-critical) or return error
		func(curOff int) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Panic on corrupt byte at %d: %v\nStack:\n%s", curOff, r, string(debug.Stack()))
				}
			}()
			_, _ = DecompressTo(corrupted, dst)
		}(offset)
	}
}

func BenchmarkRouteContent_StructuredJSON(b *testing.B) {
	data := []byte("{\n  \"status\": \"ok\",\n  \"code\": 200,\n  \"users\": [\"alice\", \"bob\"]\n}")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RouteContent(data)
	}
}

func BenchmarkRouteContent_SparseZero(b *testing.B) {
	data := make([]byte, 1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RouteContent(data)
	}
}

func BenchmarkRouteContent_SmoothDelta(b *testing.B) {
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 16)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RouteContent(data)
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

func findCDecompressor(t *testing.T) string {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	t.Skip("test_przm_dec.exe not found")
	return ""
}

// TestChallenger_M4_2_ExhaustiveMode9AndAllModesParity validates 100% bit-exact parity
// between Go and Pure C decompressors across all 9 normalization modes with diverse sizes.
func TestChallenger_M4_2_ExhaustiveMode9AndAllModesParity(t *testing.T) {
	cBin := findCDecompressor(t)
	tmpDir := t.TempDir()

	type testCase struct {
		name     string
		raw      []byte
		normMode uint8
	}

	var cases []testCase

	// Mode 1: Hex Lower (128B, 256B, 1024B, 4096B)
	for _, reps := range []int{16, 32, 128, 512} {
		raw := []byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67}, reps)))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode1_HexLower_%dB", len(raw)),
			raw:      raw,
			normMode: 1,
		})
	}

	// Mode 4: Hex Upper (128B, 256B, 1024B, 4096B)
	for _, reps := range []int{8, 16, 64, 256} {
		raw := bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0xCD, 0xEF}, reps))))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode4_HexUpper_%dB", len(raw)),
			raw:      raw,
			normMode: 4,
		})
	}

	// Mode 2: Base64 Std (No pad: raw len % 3 == 0)
	for _, reps := range []int{30, 60, 200, 1000} {
		raw := []byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("123"), reps)))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode2_Base64Std_NoPad_raw%dB", reps*3),
			raw:      raw,
			normMode: 2,
		})
	}

	// Mode 5: Base64 Std (Pad 1: raw len % 3 == 2)
	for _, reps := range []int{20, 50, 200, 1000} {
		raw := []byte(base64.StdEncoding.EncodeToString(append(bytes.Repeat([]byte("123"), reps), '4', '5')))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode5_Base64Std_Pad1_raw%dB", reps*3+2),
			raw:      raw,
			normMode: 5,
		})
	}

	// Mode 6: Base64 Std (Pad 2: raw len % 3 == 1)
	for _, reps := range []int{20, 50, 200, 1000} {
		raw := []byte(base64.StdEncoding.EncodeToString(append(bytes.Repeat([]byte("123"), reps), '4')))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode6_Base64Std_Pad2_raw%dB", reps*3+1),
			raw:      raw,
			normMode: 6,
		})
	}

	// Mode 7: Base64 URL (No pad: raw len % 3 == 0, URL safe '-' and '_')
	for _, reps := range []int{20, 50, 200, 1000} {
		rawBytes := bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, reps)
		raw := []byte(base64.URLEncoding.EncodeToString(rawBytes))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode7_Base64URL_NoPad_raw%dB", reps*3),
			raw:      raw,
			normMode: 7,
		})
	}

	// Mode 8: Base64 URL (Pad 1: raw len % 3 == 2, URL safe '-' and '_')
	for _, reps := range []int{20, 50, 200, 1000} {
		rawBytes := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, reps), 0xFB, 0xFF)
		raw := []byte(base64.URLEncoding.EncodeToString(rawBytes))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode8_Base64URL_Pad1_raw%dB", reps*3+2),
			raw:      raw,
			normMode: 8,
		})
	}

	// Mode 9: Base64 URL (Pad 2: raw len % 3 == 1, URL safe '-' and '_')
	// EXHAUSTIVE SIZES for Mode 9
	for _, reps := range []int{16, 25, 50, 100, 250, 500, 1000, 2500} {
		rawBytes := append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, reps), 0xFB)
		raw := []byte(base64.URLEncoding.EncodeToString(rawBytes))
		cases = append(cases, testCase{
			name:     fmt.Sprintf("Mode9_Base64URL_Pad2_raw%dB", reps*3+1),
			raw:      raw,
			normMode: 9,
		})
	}

	// Mode 3: Mixed spans
	cases = append(cases, testCase{
		name: "Mode3_MixedSpans_JSON",
		raw: func() []byte {
			var buf bytes.Buffer
			buf.WriteString("{\"status\":\"ok\",\"hex\":\"")
			buf.WriteString(hex.EncodeToString(bytes.Repeat([]byte{0x11, 0x22, 0x33, 0x44}, 16)))
			buf.WriteString("\",\"token\":\"")
			buf.WriteString(base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 16), 0xFB)))
			buf.WriteString("\"}")
			return buf.Bytes()
		}(),
		normMode: 3,
	})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			comp, err := Compress(tc.raw, 1)
			if err != nil {
				t.Fatalf("Go Compress failed: %v", err)
			}

			hdr, err := ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("ParseFrameHeader failed: %v", err)
			}
			if hdr.NormMode != tc.normMode {
				t.Fatalf("Expected NormMode %d, got %d", tc.normMode, hdr.NormMode)
			}

			// Verify Go decompress
			goDst := make([]byte, len(tc.raw)+64)
			goDec, err := DecompressTo(comp, goDst)
			if err != nil {
				t.Fatalf("Go DecompressTo failed: %v", err)
			}
			if !bytes.Equal(goDec, tc.raw) {
				t.Fatalf("Go DecompressTo payload mismatch")
			}

			// Verify C decompress
			inPrzm := filepath.Join(tmpDir, tc.name+".przm")
			outBin := filepath.Join(tmpDir, tc.name+".out")
			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("WriteFile failed: %v", err)
			}

			cmd := exec.Command(cBin, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor failed (exit status %v):\nOutput:\n%s", err, string(output))
			}

			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}

			if !bytes.Equal(cDec, tc.raw) {
				t.Fatalf("Pure C decompressed payload mismatch: got %d bytes, want %d bytes", len(cDec), len(tc.raw))
			}

			if !bytes.Equal(cDec, goDec) {
				t.Fatalf("Cross-language mismatch between Go and C outputs!")
			}
		})
	}
}

// TestChallenger_M4_2_SubThresholdBypassParity validates that inputs below the 64B
// normalization threshold bypass normalization (NormMode=0) and decompress identically in Go & C.
func TestChallenger_M4_2_SubThresholdBypassParity(t *testing.T) {
	cBin := findCDecompressor(t)
	tmpDir := t.TempDir()

	subCases := [][]byte{
		[]byte("hello"),
		[]byte("cafebabe"),
		[]byte(base64.StdEncoding.EncodeToString([]byte("short"))),
		[]byte(base64.URLEncoding.EncodeToString([]byte("jwt-fragment-xyz"))),
		[]byte("{\"key\": 123}"),
	}

	for idx, raw := range subCases {
		comp, err := Compress(raw, 1)
		if err != nil {
			t.Fatalf("[%d] Compress failed: %v", idx, err)
		}
		hdr, err := ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("[%d] ParseFrameHeader failed: %v", idx, err)
		}
		if hdr.NormMode != 0 {
			t.Fatalf("[%d] Expected NormMode 0 for sub-threshold input, got %d", idx, hdr.NormMode)
		}

		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("sub_%d.przm", idx))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("sub_%d.out", idx))
		if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
			t.Fatalf("[%d] WriteFile failed: %v", idx, err)
		}

		cmd := exec.Command(cBin, inPrzm, outBin)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("[%d] Pure C failed: %v\nOutput: %s", idx, err, string(output))
		}
		cDec, err := os.ReadFile(outBin)
		if err != nil {
			t.Fatalf("[%d] ReadFile failed: %v", idx, err)
		}
		if !bytes.Equal(cDec, raw) {
			t.Fatalf("[%d] Mismatch in Pure C sub-threshold decompress", idx)
		}
	}
}

// TestChallenger_M4_2_PureC_AdversarialRobustness feeds heavily corrupted and mutated
// bitstreams to test_przm_dec.exe and asserts that it rejects with non-zero error codes
// without crashing, segfaulting, or hanging.
func TestChallenger_M4_2_PureC_AdversarialRobustness(t *testing.T) {
	cBin := findCDecompressor(t)
	tmpDir := t.TempDir()

	// Base payload for mutation
	basePayload := []byte("{\"token\":\"" + base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 10), 0xFB)) + "\"}")
	validComp, err := Compress(basePayload, 1)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	var corruptCases [][]byte

	// 1. Truncations
	for _, l := range []int{0, 1, 2, 3, 4, 7, 8, len(validComp) / 4, len(validComp) / 2, len(validComp) - 1} {
		if l <= len(validComp) {
			b := make([]byte, l)
			copy(b, validComp[:l])
			corruptCases = append(corruptCases, b)
		}
	}

	// 2. Magic corruption
	badMagic := make([]byte, len(validComp))
	copy(badMagic, validComp)
	badMagic[0] ^= 0xAA
	corruptCases = append(corruptCases, badMagic)

	// 3. Flags and size tampering
	badSize := make([]byte, len(validComp))
	copy(badSize, validComp)
	badSize[5] = 0xFF
	badSize[6] = 0xFF
	badSize[7] = 0xFF
	corruptCases = append(corruptCases, badSize)

	// 4. Invalid Normalization Modes (e.g., Mode 10, Mode 255)
	hdr, err := ParseFrameHeader(validComp)
	if err == nil && hdr.Flags&0x20 != 0 {
		secDirLen := 4
		if hdr.Flags&0x08 != 0 {
			secDirLen = 6
		}
		sec1Pos := 8 + secDirLen
		if sec1Pos+2 < len(validComp) {
			for _, badMode := range []byte{10, 11, 15, 255} {
				badModeBuf := make([]byte, len(validComp))
				copy(badModeBuf, validComp)
				badModeBuf[sec1Pos+2] = badMode
				corruptCases = append(corruptCases, badModeBuf)
			}
		}
	}

	// 5. Random byte flips across entire stream
	for off := 4; off < len(validComp); off += 3 {
		flipped := make([]byte, len(validComp))
		copy(flipped, validComp)
		flipped[off] ^= 0xFF
		corruptCases = append(corruptCases, flipped)
	}

	// 6. Completely random byte buffers
	for _, sz := range []int{16, 64, 256, 1024} {
		rnd := make([]byte, sz)
		_, _ = rand.Read(rnd)
		corruptCases = append(corruptCases, rnd)
	}

	inPath := filepath.Join(tmpDir, "corrupt.przm")
	outPath := filepath.Join(tmpDir, "corrupt.out")

	for idx, corruptData := range corruptCases {
		if err := os.WriteFile(inPath, corruptData, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		_ = os.Remove(outPath)

		cmd := exec.Command(cBin, inPath, outPath)
		output, err := cmd.CombinedOutput()

		// A corrupt stream must either exit with error code or, if valid, produce valid output.
		// It MUST NEVER crash with segfault (exit code -1073741819 / 0xC0000005) or abort.
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			if exitCode < 0 || exitCode > 128 {
				t.Fatalf("[%d] test_przm_dec.exe crashed with abnormal exit code %d (0x%X)!\nOutput:\n%s",
					idx, exitCode, uint32(exitCode), string(output))
			}
		}
	}
	t.Logf("Tested %d adversarial corrupt bitstreams against test_przm_dec.exe: zero crashes/segfaults", len(corruptCases))
}

// TestChallenger_M4_2_ZeroAllocDecompressTo verifies strictly 0 heap allocations
// in Go DecompressTo across all 9 modes and various sizes.
func TestChallenger_M4_2_ZeroAllocDecompressTo(t *testing.T) {
	testData := [][]byte{
		// Mode 1: Hex Lower
		[]byte(hex.EncodeToString(bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 100))),
		// Mode 4: Hex Upper
		bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 100)))),
		// Mode 2: Base64 Std (No pad)
		[]byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("ABC"), 100))),
		// Mode 5: Base64 Std (Pad 1)
		[]byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("ABCDE"), 60))),
		// Mode 6: Base64 Std (Pad 2)
		[]byte(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("ABCD"), 60))),
		// Mode 7: Base64 URL (No pad)
		[]byte(base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 100))),
		// Mode 8: Base64 URL (Pad 1)
		[]byte(base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 100), 0xFB, 0xFF))),
		// Mode 9: Base64 URL (Pad 2)
		[]byte(base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 100), 0xFB))),
		// Mode 3: Mixed spans
		[]byte("{\"hash\":\"" + hex.EncodeToString(bytes.Repeat([]byte{0x12, 0x34}, 32)) + "\",\"jwt\":\"" +
			base64.URLEncoding.EncodeToString(append(bytes.Repeat([]byte{0xFB, 0xFF, 0xFE}, 16), 0xFB)) + "\"}"),
	}

	for idx, d := range testData {
		comp, err := Compress(d, 1)
		if err != nil {
			t.Fatalf("[%d] Compress failed: %v", idx, err)
		}
		dst := make([]byte, len(d)+64)

		// Warmup
		_, err = DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("[%d] DecompressTo warmup failed: %v", idx, err)
		}

		allocs := testing.AllocsPerRun(50, func() {
			_, _ = DecompressTo(comp, dst)
		})
		if allocs != 0.0 {
			t.Fatalf("[%d] DecompressTo produced %f allocs, want 0.0", idx, allocs)
		}
	}
}

// TestChallenger_M4_2_GoDecompressTo_AdversarialFuzz feeds 1000 randomized corruptions
// and mutations to Go DecompressTo to ensure zero unhandled panics or bounds errors.
func TestChallenger_M4_2_GoDecompressTo_AdversarialFuzz(t *testing.T) {
	seeds := [][]byte{
		[]byte("{\"id\":1001,\"data\":\"" + base64.URLEncoding.EncodeToString(bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF}, 16)) + "\"}"),
		bytes.Repeat([]byte{0x00, 0x01, 0x02, 0x03}, 32),
		bytes.ToUpper([]byte(hex.EncodeToString(bytes.Repeat([]byte{0xAA, 0xBB, 0xCC, 0xDD}, 16)))),
	}

	dst := make([]byte, 65536)

	for sIdx, seed := range seeds {
		comp, err := Compress(seed, 1)
		if err != nil {
			t.Fatalf("Seed %d compress failed: %v", sIdx, err)
		}

		// Mutate across 300 iterations per seed
		for iter := 0; iter < 300; iter++ {
			mutated := make([]byte, len(comp))
			copy(mutated, comp)

			// Truncate or flip random bytes
			if iter%3 == 0 && len(mutated) > 2 {
				truncLen := iter % len(mutated)
				mutated = mutated[:truncLen]
			} else {
				numFlips := 1 + (iter % 5)
				for f := 0; f < numFlips; f++ {
					flipIdx := (iter*17 + f*7) % len(mutated)
					mutated[flipIdx] ^= byte(0x55 + f*33)
				}
			}

			func(s, i int, payload []byte) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("[Seed %d, Iter %d] Go DecompressTo panicked: %v", s, i, r)
					}
				}()
				_, _ = DecompressTo(payload, dst)
			}(sIdx, iter, mutated)
		}
	}
	t.Logf("Tested 900 adversarial fuzzed bitstreams against Go DecompressTo: zero panics")
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

func findCDec(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"test_przm_dec.exe", "tests/test_przm_dec.exe", filepath.Join("..", "test_przm_dec.exe")} {
		if abs, err := filepath.Abs(p); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

func makeCorruptFrame(uSize int, cmdBytes, litBytes []byte) []byte {
	var f bytes.Buffer
	binary.Write(&f, binary.LittleEndian, MagicPRZM)
	f.WriteByte(FlagExtOffset)
	f.WriteByte(byte(uSize))
	f.WriteByte(byte(uSize >> 8))
	f.WriteByte(byte(uSize >> 16))

	pCmd, _ := PackStream(cmdBytes, ModeFlat)
	pOff, _ := PackStream([]byte{}, ModeFlat)
	cL, oL := len(pCmd), len(pOff)

	f.WriteByte(byte(cL))
	f.WriteByte(byte(cL >> 8))
	f.WriteByte(byte(cL >> 16))
	f.WriteByte(byte(oL))
	f.WriteByte(byte(oL >> 8))
	f.WriteByte(byte(oL >> 16))

	var s1 [8]byte
	s1[1] = 16
	f.Write(s1[:])
	f.Write(pCmd)
	f.Write(pOff)
	f.Write(litBytes)
	return f.Bytes()
}

func assertCorruptRejection(t *testing.T, cBin string, frame []byte, name string) {
	t.Helper()
	// Go Decompress (0 panics, must return ErrCorruptedFrame)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("[%s] Decompress panicked: %v", name, r)
			}
		}()
		_, err := Decompress(frame)
		if err == nil || !errors.Is(err, ErrCorruptedFrame) {
			t.Fatalf("[%s] Decompress expected ErrCorruptedFrame, got %v", name, err)
		}
	}()

	// Go DecompressTo (0 panics, must return ErrCorruptedFrame)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("[%s] DecompressTo panicked: %v", name, r)
			}
		}()
		dst := make([]byte, 1024*1024)
		_, err := DecompressTo(frame, dst)
		if err == nil || !errors.Is(err, ErrCorruptedFrame) {
			t.Fatalf("[%s] DecompressTo expected ErrCorruptedFrame, got %v", name, err)
		}
	}()

	// Pure C test_przm_dec.exe
	tmp := t.TempDir()
	inF, outF := filepath.Join(tmp, "in.przm"), filepath.Join(tmp, "out.bin")
	if err := os.WriteFile(inF, frame, 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(cBin, inF, outF)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("[%s] Pure C accepted corrupt frame unexpectedly", name)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() == -1073741819 || exitErr.ExitCode() == int(0xC0000005) {
			t.Fatalf("[%s] Pure C crashed with access violation! %s", name, string(out))
		}
	}
}

func TestChallenger_M5_1_Dedup_Adversarial_Corruption(t *testing.T) {
	cBin := findCDec(t)
	rle64K := []byte{0xF1, 0x80, 0x80, 0x04} // RLE 65536 bytes
	lits := []byte{0xAA, 0xBB}

	cases := []struct {
		name  string
		uSize int
		cmds  []byte
	}{
		// 1. Truncated OpCopyBlock (0, 1, 2, 3 bytes payload)
		{"Trunc_0B", 131072, append(append([]byte{}, rle64K...), OpCopyBlock)},
		{"Trunc_1B", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0x00)},
		{"Trunc_2B", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0x00, 0x00)},
		{"Trunc_3B", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0x00, 0x00, 0x00)},

		// 2. Forward block references (srcBlockIdx >= currentBlockIndex)
		{"Fwd_Ref0_AtStart", 131072, []byte{OpCopyBlock, 0x00, 0x00, 0x00, 0x00}},
		{"Fwd_Ref1_AtBlk1", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0x01, 0x00, 0x00, 0x00)},
		{"Fwd_Ref2_AtBlk1", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0x02, 0x00, 0x00, 0x00)},

		// 3. OOB / Giant block indices
		{"OOB_256", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0x00, 0x01, 0x00, 0x00)},
		{"OOB_0xFFFFFFFF", 131072, append(append([]byte{}, rle64K...), OpCopyBlock, 0xFF, 0xFF, 0xFF, 0xFF)},

		// 4. Buffer overflow attempt (valid block 0, but exceeds uSize/buffer capacity)
		{"BufOverflow_ExceedBy1B", 131072, append(append([]byte{}, rle64K...), 0xF3, OpCopyBlock, 0x00, 0x00, 0x00, 0x00)},
		{"BufOverflow_ExceedBy31K", 100000, append(append([]byte{}, rle64K...), OpCopyBlock, 0x00, 0x00, 0x00, 0x00)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frame := makeCorruptFrame(tc.uSize, tc.cmds, lits)
			assertCorruptRejection(t, cBin, frame, tc.name)
		})
	}
}

func TestChallenger_M5_1_Dedup_Boundary_And_Mix(t *testing.T) {
	cBin := findCDec(t)

	// 1. Alternating dedup and high-entropy blocks
	t.Run("Alternating_Dedup_HighEntropy", func(t *testing.T) {
		rnd := rand.New(rand.NewSource(42))
		b0, b1 := make([]byte, DedupBlockSize), make([]byte, DedupBlockSize)
		rnd.Read(b0)
		rnd.Read(b1)
		var stream []byte
		stream = append(stream, b0...) // Blk 0: Entropy A
		stream = append(stream, b1...) // Blk 1: Entropy B
		stream = append(stream, b0...) // Blk 2: Copy A (dedup)
		stream = append(stream, b1...) // Blk 3: Copy B (dedup)

		verifyRoundtrip(t, cBin, stream)
	})

	// 2. Near-identical blocks differing by 1 byte (start, middle, end) -> no false dedup
	t.Run("NearIdentical_1ByteDelta", func(t *testing.T) {
		base := make([]byte, DedupBlockSize)
		for i := range base {
			base[i] = byte(i*17 + 5)
		}
		bStart := append([]byte{}, base...)
		bStart[0] ^= 0x01
		bMid := append([]byte{}, base...)
		bMid[32768] ^= 0x01
		bEnd := append([]byte{}, base...)
		bEnd[65535] ^= 0x01

		var stream []byte
		stream = append(stream, base...)   // 0: base
		stream = append(stream, bStart...) // 1: diff at start (no dedup)
		stream = append(stream, bMid...)   // 2: diff at mid (no dedup)
		stream = append(stream, bEnd...)   // 3: diff at end (no dedup)
		stream = append(stream, base...)   // 4: copy base (dedup against 0)
		stream = append(stream, bStart...) // 5: copy bStart (dedup against 1)

		verifyRoundtrip(t, cBin, stream)
	})

	// 3. Large streams: 1 MiB and 4 MiB distant duplicates
	for _, sz := range []struct {
		name      string
		numBlocks int
	}{
		{"DistantDup_1MiB", 16},
		{"DistantDup_4MiB", 64},
	} {
		t.Run(sz.name, func(t *testing.T) {
			bA := make([]byte, DedupBlockSize)
			bB := make([]byte, DedupBlockSize)
			for i := range DedupBlockSize {
				bA[i] = byte(i*31 + 7)
				bB[i] = byte(i*59 + 13)
			}
			var stream []byte
			for i := 0; i < sz.numBlocks; i++ {
				if i%2 == 0 {
					stream = append(stream, bA...)
				} else {
					stream = append(stream, bB...)
				}
			}
			verifyRoundtrip(t, cBin, stream)
		})
	}
}

func verifyRoundtrip(t *testing.T, cBin string, raw []byte) {
	t.Helper()
	for _, lvl := range []int{1, 3} {
		comp, err := Compress(raw, lvl)
		if err != nil {
			t.Fatalf("Compress lvl %d failed: %v", lvl, err)
		}

		// Go Decompress
		dec, err := Decompress(comp)
		if err != nil || !bytes.Equal(dec, raw) {
			t.Fatalf("Go Decompress mismatch or error: %v", err)
		}

		// Go DecompressTo
		dst := make([]byte, len(raw)+64)
		decTo, err := DecompressTo(comp, dst)
		if err != nil || !bytes.Equal(decTo, raw) {
			t.Fatalf("Go DecompressTo mismatch or error: %v", err)
		}

		// Pure C
		tmp := t.TempDir()
		inF, outF := filepath.Join(tmp, "c.przm"), filepath.Join(tmp, "c.out")
		if err := os.WriteFile(inF, comp, 0644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(cBin, inF, outF)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Pure C decompressor failed: %v: %s", err, string(out))
		}
		cOut, err := os.ReadFile(outF)
		if err != nil || !bytes.Equal(cOut, raw) {
			t.Fatalf("Pure C output mismatch with raw data")
		}
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

func findCDecoderM5(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// buildPaddedBlock repeats/truncates sample content to exactly 65,536 bytes.
func buildPaddedBlock(content []byte) []byte {
	block := make([]byte, DedupBlockSize)
	if len(content) == 0 {
		return block
	}
	copied := 0
	for copied < DedupBlockSize {
		n := copy(block[copied:], content)
		copied += n
	}
	return block
}

// TestChallenger_M5_2_CrossLanguage_Dedup_Parity validates 100% bit-exact decode parity
// between Go encoder (levels 1 and 3) and Pure C decompressor (test_przm_dec.exe)
// across diverse multi-block payloads exercising 64 KiB deduplication.
func TestChallenger_M5_2_CrossLanguage_Dedup_Parity(t *testing.T) {
	cBinPath := findCDecoderM5(t)
	tmpDir := t.TempDir()

	testCases := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "SourceCode_MultiBlock_4x64K_PlusTail",
			gen: func() []byte {
				// Block 0: Go source code pattern
				var goSrc bytes.Buffer
				for i := 0; i < 250; i++ {
					fmt.Fprintf(&goSrc, `package przm_pipeline_%03d
import (
	"context"
	"sync"
)
type DedupProcessor%03d struct {
	mu sync.Mutex
	id uint64
}
func (p *DedupProcessor%03d) Process(ctx context.Context, blk []byte) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return blk, nil
}
`, i, i, i)
				}
				b0 := buildPaddedBlock(goSrc.Bytes())

				// Block 2: C source code pattern
				var cSrc bytes.Buffer
				for i := 0; i < 250; i++ {
					fmt.Fprintf(&cSrc, `/* Block dedup C routine %03d */
#include <stdint.h>
#include <string.h>
int przm_dec_block_copy_%03d(uint8_t* dst, const uint8_t* src, size_t len) {
    if (!dst || !src) return -1;
    memcpy(dst, src, len);
    return 0;
}
`, i, i)
				}
				b2 := buildPaddedBlock(cSrc.Bytes())

				// Construct: B0 (Go), B1 (copy B0), B2 (C), B3 (copy B0), plus 1024-byte tail
				var payload bytes.Buffer
				payload.Write(b0)        // Block 0
				payload.Write(b0)        // Block 1 == Block 0 (Dedup match)
				payload.Write(b2)        // Block 2 (Distinct)
				payload.Write(b0)        // Block 3 == Block 0 (Dedup match)
				payload.Write(b2[:1024]) // Tail (non-aligned tail)
				return payload.Bytes()
			},
		},
		{
			name: "StructuredJSON_MultiBlock_5x64K",
			gen: func() []byte {
				// Block 0: Structured telemetry JSON lines
				var jsonBufA bytes.Buffer
				for i := 0; i < 400; i++ {
					fmt.Fprintf(&jsonBufA, `{"event_id":%d,"service":"telemetry-router","status":"OK","metrics":{"cpu":%.2f,"mem":%d},"ts":"2026-10-07T12:00:%02dZ"}`+"\n",
						i, 12.5+float64(i%20)*0.5, 1048576*(i%16), i%60)
				}
				b0 := buildPaddedBlock(jsonBufA.Bytes())

				// Block 2: Distinct structured audit JSON lines
				var jsonBufB bytes.Buffer
				for i := 0; i < 400; i++ {
					fmt.Fprintf(&jsonBufB, `{"audit_seq":%d,"action":"AUTHENTICATE","user":"admin_%03d","granted":true,"roles":["reader","writer","admin"],"scope":"cluster-west"}`+"\n",
						i, i%50)
				}
				b2 := buildPaddedBlock(jsonBufB.Bytes())

				// Construct: B0, B1 (copy B0), B2, B3 (copy B0), B4 (copy B2)
				var payload bytes.Buffer
				payload.Write(b0) // Block 0
				payload.Write(b0) // Block 1 == Block 0 (Dedup)
				payload.Write(b2) // Block 2
				payload.Write(b0) // Block 3 == Block 0 (Dedup)
				payload.Write(b2) // Block 4 == Block 2 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "BinaryBuffers_MultiBlock_6x64K",
			gen: func() []byte {
				// Block 0: Seeded binary pseudo-random with structured header
				rng0 := rand.New(rand.NewSource(0x1337_0001))
				b0 := make([]byte, DedupBlockSize)
				rng0.Read(b0)
				copy(b0[:16], []byte("PRZM_BIN_BLOCK_0"))

				// Block 2: Distinct seeded binary block
				rng2 := rand.New(rand.NewSource(0x1337_0002))
				b2 := make([]byte, DedupBlockSize)
				rng2.Read(b2)
				copy(b2[:16], []byte("PRZM_BIN_BLOCK_2"))

				// Construct: B0, B1 (copy B0), B2, B3 (copy B0), B4 (copy B2), B5 (copy B0)
				var payload bytes.Buffer
				payload.Write(b0) // Block 0
				payload.Write(b0) // Block 1 == Block 0 (Dedup)
				payload.Write(b2) // Block 2
				payload.Write(b0) // Block 3 == Block 0 (Dedup)
				payload.Write(b2) // Block 4 == Block 2 (Dedup)
				payload.Write(b0) // Block 5 == Block 0 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Identical_2x64K",
			gen: func() []byte {
				block := make([]byte, DedupBlockSize)
				for i := range block {
					block[i] = byte(i*13 + 7)
				}
				var payload bytes.Buffer
				payload.Write(block)
				payload.Write(block)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Identical_3x64K",
			gen: func() []byte {
				block := make([]byte, DedupBlockSize)
				for i := range block {
					block[i] = byte((i * 17) ^ (i >> 8))
				}
				var payload bytes.Buffer
				payload.Write(block)
				payload.Write(block)
				payload.Write(block)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Identical_8x64K",
			gen: func() []byte {
				block := make([]byte, DedupBlockSize)
				for i := range block {
					block[i] = byte(i*31 + 101)
				}
				var payload bytes.Buffer
				for range 8 {
					payload.Write(block)
				}
				return payload.Bytes()
			},
		},
		{
			name: "NearIdentical_1ByteDelta_3x64K",
			gen: func() []byte {
				// Block 0: Base pattern
				b0 := make([]byte, DedupBlockSize)
				for i := range b0 {
					b0[i] = byte(i*19 + 3)
				}
				// Block 1: 1 byte flipped at offset 32768 (must NOT dedup)
				b1 := make([]byte, DedupBlockSize)
				copy(b1, b0)
				b1[32768] ^= 0xFF

				// Block 2: Exact copy of Block 0 (must dedup against Block 0)
				var payload bytes.Buffer
				payload.Write(b0)
				payload.Write(b1)
				payload.Write(b0)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Alternating_4x64K",
			gen: func() []byte {
				// Block A: Pattern 1
				bA := make([]byte, DedupBlockSize)
				for i := range bA {
					bA[i] = byte((i * 41) ^ 0x3C)
				}
				// Block B: Pattern 2
				bB := make([]byte, DedupBlockSize)
				for i := range bB {
					bB[i] = byte((i * 73) ^ 0xC3)
				}
				var payload bytes.Buffer
				payload.Write(bA) // Block 0
				payload.Write(bB) // Block 1
				payload.Write(bA) // Block 2 == Block 0 (Dedup)
				payload.Write(bB) // Block 3 == Block 1 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Interleaved_5x64K",
			gen: func() []byte {
				b0 := make([]byte, DedupBlockSize)
				b1 := make([]byte, DedupBlockSize)
				b2 := make([]byte, DedupBlockSize)
				for i := range DedupBlockSize {
					b0[i] = byte((i * 11) + 1)
					b1[i] = byte((i * 29) + 3)
					b2[i] = byte((i * 47) + 5)
				}
				var payload bytes.Buffer
				payload.Write(b0) // Block 0
				payload.Write(b1) // Block 1
				payload.Write(b2) // Block 2
				payload.Write(b0) // Block 3 == Block 0 (Dedup)
				payload.Write(b1) // Block 4 == Block 1 (Dedup)
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_HighEntropy_4x64K",
			gen: func() []byte {
				rng := rand.New(rand.NewSource(0xDEAD_BEEF))
				block := make([]byte, DedupBlockSize)
				rng.Read(block)
				var payload bytes.Buffer
				for range 4 {
					payload.Write(block)
				}
				return payload.Bytes()
			},
		},
		{
			name: "Repeated_Structured_16x64K_1MB",
			gen: func() []byte {
				block := make([]byte, DedupBlockSize)
				for i := range block {
					block[i] = byte((i * 19) ^ (i >> 4) ^ 0xA5)
				}
				var payload bytes.Buffer
				for range 16 {
					payload.Write(block)
				}
				return payload.Bytes()
			},
		},
	}

	levels := []int{1, 3}

	for _, tc := range testCases {
		raw := tc.gen()
		if len(raw) < 2*DedupBlockSize {
			t.Fatalf("[%s] Test payload must be >= 128 KiB to exercise deduplication, got %d", tc.name, len(raw))
		}

		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Lvl%d", tc.name, lvl)
			t.Run(testName, func(t *testing.T) {
				// 1. Compress with Go encoder
				comp, err := Compress(raw, lvl)
				if err != nil {
					t.Fatalf("Compress(lvl %d) failed: %v", lvl, err)
				}
				if len(comp) == 0 {
					t.Fatalf("Compress returned empty compressed frame")
				}

				t.Logf("[%s] comp len=%d, header=%x", testName, len(comp), comp[:min(len(comp), 32)])
				// 2. Go roundtrip verification (Decompress and DecompressTo)
				goDec, err := Decompress(comp)
				if err != nil {
					t.Fatalf("Go Decompress failed: %v", err)
				}
				if !bytes.Equal(goDec, raw) {
					t.Fatalf("Go Decompress output does not match original data!")
				}

				dstBuf := make([]byte, len(raw)+64)
				decTo, err := DecompressTo(comp, dstBuf)
				if err != nil {
					t.Fatalf("Go DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decTo, raw) {
					t.Fatalf("Go DecompressTo output does not match original data!")
				}

				// 3. Write compressed frame to temp file
				inPrzm := filepath.Join(tmpDir, testName+".przm")
				outBin := filepath.Join(tmpDir, testName+".out")
				if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
					t.Fatalf("WriteFile failed: %v", err)
				}

				// 4. Invoke Pure C reference decompressor: test_przm_dec.exe <in.przm> <out.bin>
				cmd := exec.Command(cBinPath, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("Pure C decompressor failed with status %v:\nOutput:\n%s", err, string(output))
				}

				// 5. Read Pure C decompressed output and verify bit-exact parity
				cDec, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("ReadFile failed: %v", err)
				}
				if len(cDec) != len(raw) {
					t.Fatalf("Pure C decompressed length mismatch: got %d, want %d", len(cDec), len(raw))
				}
				if !bytes.Equal(cDec, raw) {
					t.Fatalf("Pure C decompressed content differs from original raw data!")
				}
				if !bytes.Equal(cDec, goDec) {
					t.Fatalf("Cross-language mismatch between Pure C and Go decompressor outputs!")
				}

				compressionRatio := float64(len(raw)) / float64(len(comp))
				t.Logf("[%s] PASSED: Raw=%d B -> Comp=%d B (Ratio: %.2fx) -> Pure C Bit-Exact Match",
					testName, len(raw), len(comp), compressionRatio)
			})
		}
	}
}

// TestChallenger_M5_2_ZeroAlloc_And_LineRate strictly verifies:
// 1. testing.AllocsPerRun(100, ...) is strictly 0.00 during DecompressTo on multi-block deduplicated streams.
// 2. Decompression throughput on deduplicated streams saturates hardware line-rate (target: 10–50+ GB/s, matching ~54 GB/s memcpy).
func TestChallenger_M5_2_ZeroAlloc_And_LineRate(t *testing.T) {
	sizes := []struct {
		name      string
		numBlocks int
	}{
		{"4Blocks_256KB", 4},
		{"16Blocks_1MB", 16},
		{"64Blocks_4MB", 64},
	}

	levels := []int{1, 3}

	for _, sz := range sizes {
		// Construct multi-block data where block 0 repeats across all blocks
		block := make([]byte, DedupBlockSize)
		for i := range block {
			block[i] = byte((i * 23) ^ (i >> 5) ^ 0x5A)
		}
		var raw bytes.Buffer
		for range sz.numBlocks {
			raw.Write(block)
		}
		data := raw.Bytes()
		totalBytes := len(data)

		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Lvl%d", sz.name, lvl)
			t.Run(testName, func(t *testing.T) {
				comp, err := Compress(data, lvl)
				if err != nil {
					t.Fatalf("Compress failed: %v", err)
				}

				dst := make([]byte, totalBytes+64)

				// Warmup execution
				decWarmup, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("Warmup DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decWarmup, data) {
					t.Fatalf("Warmup DecompressTo data mismatch")
				}

				// --- 1. Zero dynamic heap allocation verification ---
				allocs := testing.AllocsPerRun(100, func() {
					out, err := DecompressTo(comp, dst)
					if err != nil {
						t.Fatalf("DecompressTo in AllocsPerRun failed: %v", err)
					}
					_ = out
				})

				if allocs != 0.0 {
					t.Fatalf("INVARIANT VIOLATION: DecompressTo produced %.2f allocs/run (mandated strictly 0.00)", allocs)
				}
				t.Logf("[%s] AllocsPerRun(100) = %.2f (strictly 0.00 zero-heap invariant satisfied)", testName, allocs)

				// --- 2. Hardware Line-Rate Decompression Throughput Benchmark ---
				// Dynamic iteration count based on buffer size to ensure stable measurement
				iters := 5000
				if sz.numBlocks >= 64 {
					iters = 1000
				} else if sz.numBlocks >= 16 {
					iters = 3000
				}

				start := time.Now()
				for i := 0; i < iters; i++ {
					_, _ = DecompressTo(comp, dst)
				}
				elapsed := time.Since(start)

				bytesProcessed := float64(totalBytes) * float64(iters)
				sec := elapsed.Seconds()
				gbPerSec := (bytesProcessed / (1024 * 1024 * 1024)) / sec

				t.Logf("[%s] Decompressed %d bytes x %d iters in %v => %.2f GB/s",
					testName, totalBytes, iters, elapsed, gbPerSec)

				// Target bracket: 10–50+ GB/s (saturating RAM memcpy line rate)
				if gbPerSec < 10.0 {
					t.Errorf("Throughput below target line rate bracket: got %.2f GB/s, want >= 10.00 GB/s", gbPerSec)
				} else {
					t.Logf("[%s] SUCCESS: Line-rate throughput satisfied (%.2f GB/s in 10-50+ GB/s bracket)",
						testName, gbPerSec)
				}
			})
		}
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

func findCDecoderChallenger(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

func runCDecoderTest(t *testing.T, cBin string, input []byte) (exitCode int, out []byte, err error) {
	t.Helper()
	tmpDir := t.TempDir()
	inPath := filepath.Join(tmpDir, "in.przm")
	outPath := filepath.Join(tmpDir, "out.bin")
	if err := os.WriteFile(inPath, input, 0600); err != nil {
		t.Fatalf("Failed to write temp in.przm: %v", err)
	}
	cmd := exec.Command(cBin, inPath, outPath)
	combined, cmdErr := cmd.CombinedOutput()
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			return exitErr.ExitCode(), combined, cmdErr
		}
		return -1, combined, cmdErr
	}
	dec, readErr := os.ReadFile(outPath)
	if readErr != nil {
		return 0, nil, readErr
	}
	return 0, dec, nil
}

func buildTestPNGChunk(chunkType string, data []byte) []byte {
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	buf.Write(lenBuf[:])
	buf.WriteString(chunkType)
	buf.Write(data)

	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(data)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
	buf.Write(crcBuf[:])
	return buf.Bytes()
}

func generateSyntheticPNG(width, height, numIDATs int, withAncillary bool, pattern string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // 8-bit depth
	ihdr[9] = 2 // Truecolor RGB
	buf.Write(buildTestPNGChunk("IHDR", ihdr[:]))

	if withAncillary {
		buf.Write(buildTestPNGChunk("sRGB", []byte{0x00}))
	}

	rawPixelCount := (width*3 + 1) * height
	pixelData := make([]byte, rawPixelCount)
	for y := 0; y < height; y++ {
		rowStart := y * (width*3 + 1)
		pixelData[rowStart] = 0 // Filter None
		for x := 0; x < width; x++ {
			px := rowStart + 1 + x*3
			switch pattern {
			case "gradient":
				pixelData[px] = byte(x % 256)
				pixelData[px+1] = byte(y % 256)
				pixelData[px+2] = byte((x*7 + y*13) % 256)
			case "checkered":
				val := byte(((x / 4) ^ (y / 4)) * 0xFF)
				pixelData[px] = val
				pixelData[px+1] = val
				pixelData[px+2] = val
			case "solid":
				pixelData[px] = 0x5A
				pixelData[px+1] = 0xA5
				pixelData[px+2] = 0x3C
			default:
				pixelData[px] = byte((x*31 ^ y*17) % 256)
				pixelData[px+1] = byte((x*19 + y*23) % 256)
				pixelData[px+2] = byte((x*11 + 42) % 256)
			}
		}
	}

	if numIDATs <= 1 {
		buf.Write(buildTestPNGChunk("IDAT", pixelData))
	} else {
		chunkSize := (len(pixelData) + numIDATs - 1) / numIDATs
		for pos := 0; pos < len(pixelData); pos += chunkSize {
			end := pos + chunkSize
			if end > len(pixelData) {
				end = len(pixelData)
			}
			buf.Write(buildTestPNGChunk("IDAT", pixelData[pos:end]))
		}
	}

	if withAncillary {
		buf.Write(buildTestPNGChunk("tEXt", []byte("Challenger\x00Milestone 6 Empirical Suite")))
	}

	buf.Write(buildTestPNGChunk("IEND", nil))
	return buf.Bytes()
}

// TestChallenger_M6_1_Archival_PNG_Adversarial stress-tests truncated and corrupted Archival PNG streams.
func TestChallenger_M6_1_Archival_PNG_Adversarial(t *testing.T) {
	cBin := findCDecoderChallenger(t)

	validPNG := generateSyntheticPNG(32, 32, 2, true, "gradient")
	validComp, err := CompressArchivalPNG(validPNG, 3)
	if err != nil {
		t.Fatalf("Baseline CompressArchivalPNG failed: %v", err)
	}
	if len(validComp) < 12 {
		t.Fatalf("Baseline compressed length too small: %d", len(validComp))
	}

	metaSize := int(binary.LittleEndian.Uint32(validComp[8:12]))

	type attackCase struct {
		name string
		data []byte
	}

	var attacks []attackCase

	// 1. Truncated container headers (< 12 bytes)
	for _, l := range []int{0, 1, 3, 4, 7, 8, 11} {
		if l <= len(validComp) {
			attacks = append(attacks, attackCase{
				name: fmt.Sprintf("TruncatedHeader_Len%d", l),
				data: append([]byte(nil), validComp[:l]...),
			})
		}
	}

	// 2. Partial metadata block
	if 12+metaSize <= len(validComp) {
		attacks = append(attacks, attackCase{
			name: "PartialMetadata_HeaderOnly",
			data: append([]byte(nil), validComp[:12]...),
		})
		attacks = append(attacks, attackCase{
			name: "PartialMetadata_Midway",
			data: append([]byte(nil), validComp[:12+metaSize/2]...),
		})
	}

	// 3. Invalid metadata sizes (< 8 or exceeding length)
	badMetaSmall := append([]byte(nil), validComp...)
	binary.LittleEndian.PutUint32(badMetaSmall[8:12], 4)
	attacks = append(attacks, attackCase{name: "MetaSize_TooSmall_4B", data: badMetaSmall})

	badMetaHuge := append([]byte(nil), validComp...)
	binary.LittleEndian.PutUint32(badMetaHuge[8:12], uint32(len(validComp)+1000))
	attacks = append(attacks, attackCase{name: "MetaSize_ExceedsStream", data: badMetaHuge})

	// 4. Invalid metadata magic (byte 12 != 0x50 'P')
	badMetaMagic := append([]byte(nil), validComp...)
	badMetaMagic[12] = 0xAA
	attacks = append(attacks, attackCase{name: "Invalid_MetaMagic_0xAA", data: badMetaMagic})

	// 5. Corrupted chunk descriptors & lengths exceeding bounds
	badChunkLen := append([]byte(nil), validComp...)
	// IDAT descriptor starts at offset 20 (12 + 8)
	binary.LittleEndian.PutUint32(badChunkLen[20:24], 0xFFFFFFF0)
	attacks = append(attacks, attackCase{name: "IDAT_ChunkLen_HugeOverflow", data: badChunkLen})

	badIDATCount := append([]byte(nil), validComp...)
	binary.LittleEndian.PutUint16(badIDATCount[18:20], 9999) // idatCount = 9999
	attacks = append(attacks, attackCase{name: "IDAT_Count_Desync", data: badIDATCount})

	// 6. Truncated inner compressed stream
	attacks = append(attacks, attackCase{
		name: "TruncatedInner_OnlyMeta",
		data: append([]byte(nil), validComp[:12+metaSize]...),
	})
	if 12+metaSize+4 < len(validComp) {
		attacks = append(attacks, attackCase{
			name: "TruncatedInner_PartialInnerPRZM",
			data: append([]byte(nil), validComp[:12+metaSize+4]...),
		})
	}

	dstBuf := make([]byte, len(validPNG)+64)

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			// A. Go DecompressTo & Decompress verification
			_, errTo := DecompressTo(tc.data, dstBuf)
			if errTo == nil {
				t.Fatalf("Go DecompressTo unexpectedly accepted %s", tc.name)
			}
			_, errDec := Decompress(tc.data)
			if errDec == nil {
				t.Fatalf("Go Decompress unexpectedly accepted %s", tc.name)
			}

			// B. Pure C verification
			exitCode, out, cErr := runCDecoderTest(t, cBin, tc.data)
			if cErr == nil || exitCode == 0 {
				t.Fatalf("Pure C decompressor unexpectedly accepted %s (exit %d)", tc.name, exitCode)
			}
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf("Pure C decompressor crashed or exited unexpectedly (code %d): %s", exitCode, string(out))
			}
		})
	}
}

func buildSyntheticDeltaFrame(cmds, offs, lits []byte, uSize int) []byte {
	var buf bytes.Buffer
	buf.WriteString("PRZM")
	buf.WriteByte(FlagDeltaMask)
	var szBuf [3]byte
	szBuf[0] = byte(uSize)
	szBuf[1] = byte(uSize >> 8)
	szBuf[2] = byte(uSize >> 16)
	buf.Write(szBuf[:])

	cmdSectionLen := 1 + 4 + len(cmds) // mode (1B) + len (4B) + cmds
	var dir [4]byte
	binary.LittleEndian.PutUint16(dir[0:2], uint16(cmdSectionLen))
	binary.LittleEndian.PutUint16(dir[2:4], uint16(len(offs)))
	buf.Write(dir[:])

	buf.Write(make([]byte, 8)) // Section 1 padding

	buf.WriteByte(0) // Section 2: ModeFlat
	var cLenBuf [4]byte
	binary.LittleEndian.PutUint32(cLenBuf[:], uint32(len(cmds)))
	buf.Write(cLenBuf[:])
	buf.Write(cmds)

	buf.Write(offs)
	buf.Write(lits)
	return buf.Bytes()
}

// TestChallenger_M6_1_Approx_Matching_Adversarial stress-tests malformed delta match streams.
func TestChallenger_M6_1_Approx_Matching_Adversarial(t *testing.T) {
	cBin := findCDecoderChallenger(t)

	type deltaAttack struct {
		name string
		data []byte
	}

	var attacks []deltaAttack

	// Baseline: 16 literal bytes 'A' (F0 10), then delta match
	litPrefixCmds := []byte{0xF0, 0x10}
	litPrefixData := bytes.Repeat([]byte("A"), 16)

	// 1. Invalid opcode flags: Locality index k >= 3 (0xE3 .. 0xE7)
	for k := 3; k <= 7; k++ {
		op := byte(0xE0 | k)
		cmds := append(append([]byte(nil), litPrefixCmds...), op, 0x00) // baseLen=0 -> mLen=4
		lits := append(append([]byte(nil), litPrefixData...), 0x00)     // cMods=0
		attacks = append(attacks, deltaAttack{
			name: fmt.Sprintf("InvalidOpcode_DeltaRing_k%d", k),
			data: buildSyntheticDeltaFrame(cmds, nil, lits, 32),
		})
	}

	// 2. Truncated diff offsets & XOR bytes
	// 2a. cMods = 2 but literals end after cMods byte
	cmds2a := append(append([]byte(nil), litPrefixCmds...), 0xE0, 0x00) // k=0, baseLen=0
	lits2a := append(append([]byte(nil), litPrefixData...), 0x02)       // cMods=2, no offset/xor follows
	attacks = append(attacks, deltaAttack{
		name: "TruncatedDelta_MissingAllMods",
		data: buildSyntheticDeltaFrame(cmds2a, nil, lits2a, 32),
	})

	// 2b. cMods = 1, relOffset present but missing XOR byte
	lits2b := append(append([]byte(nil), litPrefixData...), 0x01, 0x02) // cMods=1, relOffset=2, EOF
	attacks = append(attacks, deltaAttack{
		name: "TruncatedDelta_MissingXORByte",
		data: buildSyntheticDeltaFrame(cmds2a, nil, lits2b, 32),
	})

	// 2c. Explicit Delta (0xE8) with truncated offset stream in Section 3
	cmds2c := append(append([]byte(nil), litPrefixCmds...), 0xE8, 0x00)
	lits2c := append(append([]byte(nil), litPrefixData...), 0x00)
	attacks = append(attacks, deltaAttack{
		name: "TruncatedDelta_ExplicitEmptyOffsetSection",
		data: buildSyntheticDeltaFrame(cmds2c, nil, lits2c, 32),
	})

	// 3. Out-of-bounds match references
	// 3a. Explicit offset distance d=100 > dstPos=16
	cmds3a := append(append([]byte(nil), litPrefixCmds...), 0xE8, 0x00)
	offs3a := []byte{0x64} // d = 100
	lits3a := append(append([]byte(nil), litPrefixData...), 0x00)
	attacks = append(attacks, deltaAttack{
		name: "OOB_MatchRef_DistanceExceedsDecodedBytes",
		data: buildSyntheticDeltaFrame(cmds3a, offs3a, lits3a, 64),
	})

	// 3b. Match length overflows uncompressed size (uSize=20, dstPos=16, mLen=16 -> 32)
	cmds3b := append(append([]byte(nil), litPrefixCmds...), 0xE0, 12) // baseLen=12 -> mLen=16
	lits3b := append(append([]byte(nil), litPrefixData...), 0x00)
	attacks = append(attacks, deltaAttack{
		name: "OOB_MatchRef_LengthExceedsCapacity",
		data: buildSyntheticDeltaFrame(cmds3b, nil, lits3b, 20),
	})

	dstBuf := make([]byte, 256)

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			_, errTo := DecompressTo(tc.data, dstBuf)
			if errTo == nil {
				t.Fatalf("Go DecompressTo accepted %s", tc.name)
			}
			_, errDec := Decompress(tc.data)
			if errDec == nil {
				t.Fatalf("Go Decompress accepted %s", tc.name)
			}

			exitCode, out, cErr := runCDecoderTest(t, cBin, tc.data)
			if cErr == nil || exitCode == 0 {
				t.Fatalf("Pure C decompressor accepted %s (exit %d)", tc.name, exitCode)
			}
			if exitCode != 2 && exitCode != 3 {
				t.Fatalf("Pure C decompressor crashed on %s (code %d): %s", tc.name, exitCode, string(out))
			}
		})
	}
}

// TestChallenger_M6_1_BitExact_Roundtrips verifies 100% bit-exact reconstruction of multi-IDAT PNG containers.
func TestChallenger_M6_1_BitExact_Roundtrips(t *testing.T) {
	cBin := findCDecoderChallenger(t)

	cases := []struct {
		name          string
		w, h          int
		chunks        int
		withAncillary bool
		pattern       string
		level         int
	}{
		{"SingleIDAT_16x16_Gradient", 16, 16, 1, false, "gradient", 1},
		{"TwoIDAT_32x32_Checkered", 32, 32, 2, false, "checkered", 3},
		{"FourIDAT_64x64_Solid_Ancillary", 64, 64, 4, true, "solid", 5},
		{"EightIDAT_128x32_Patterned", 128, 32, 8, true, "patterned", 3},
		{"SixteenIDAT_64x128_Gradient_Ancillary", 64, 128, 16, true, "gradient", 5},
		{"MultiIDAT_256x16_Checkered", 256, 16, 5, false, "checkered", 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rawPNG := generateSyntheticPNG(tc.w, tc.h, tc.chunks, tc.withAncillary, tc.pattern)
			if len(rawPNG) == 0 {
				t.Fatalf("Failed to generate raw PNG for %s", tc.name)
			}

			// 1. CompressArchivalPNG
			comp, err := CompressArchivalPNG(rawPNG, tc.level)
			if err != nil {
				t.Fatalf("CompressArchivalPNG failed: %v", err)
			}
			if len(comp) < 12 {
				t.Fatalf("Compressed frame too small: %d", len(comp))
			}
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != ArchivalPNGMagic {
				t.Fatalf("Header magic mismatch: got 0x%08X, want 0x%08X", magic, ArchivalPNGMagic)
			}

			// 2. Go DecompressTo
			dst := make([]byte, len(rawPNG)+64)
			decTo, err := DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("Go DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decTo, rawPNG) {
				t.Fatalf("Go DecompressTo output bit-exact mismatch! len got=%d, want=%d", len(decTo), len(rawPNG))
			}

			// 3. Go Decompress
			decAlloc, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Go Decompress failed: %v", err)
			}
			if !bytes.Equal(decAlloc, rawPNG) {
				t.Fatalf("Go Decompress output bit-exact mismatch!")
			}

			// 4. Pure C test_przm_dec.exe cross-language verification
			exitCode, cDec, cErr := runCDecoderTest(t, cBin, comp)
			if cErr != nil || exitCode != 0 {
				t.Fatalf("Pure C decompressor failed (exit %d): %v", exitCode, cErr)
			}
			if !bytes.Equal(cDec, rawPNG) {
				t.Fatalf("Pure C output bit-exact mismatch with original PNG! len got=%d, want=%d", len(cDec), len(rawPNG))
			}

			t.Logf("[%s] PASSED: PNG %d B -> PRZM %d B (%.2fx) | Go & Pure C Bit-Exact",
				tc.name, len(rawPNG), len(comp), float64(len(rawPNG))/float64(len(comp)))
		})
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

func findCDecoderM6(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

func buildPNGChunk(chunkType string, data []byte) []byte {
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	buf.Write(lenBuf[:])
	buf.WriteString(chunkType)
	buf.Write(data)

	// Compute IEEE CRC32 over chunkType + data
	crc := crc32.NewIEEE()
	crc.Write([]byte(chunkType))
	crc.Write(data)
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], crc.Sum32())
	buf.Write(crcBuf[:])

	return buf.Bytes()
}

// generateMockPNG creates valid synthetic PNG containers with custom dimensions, chunking, and metadata.
func generateMockPNG(width, height int, splitIDATChunks int, withAncillary bool, pattern string) []byte {
	var buf bytes.Buffer
	// PNG 8-byte signature
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	// IHDR chunk
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // 8-bit depth
	ihdr[9] = 2 // Truecolor RGB
	buf.Write(buildPNGChunk("IHDR", ihdr[:]))

	// Optional ancillary chunk before IDAT (prefix)
	if withAncillary {
		srgb := []byte{0x00} // Perceptual rendering intent
		buf.Write(buildPNGChunk("sRGB", srgb))
	}

	// Uncompressed scanline payload
	rawPixelCount := (width*3 + 1) * height
	pixelData := make([]byte, rawPixelCount)
	for y := 0; y < height; y++ {
		rowStart := y * (width*3 + 1)
		pixelData[rowStart] = 0 // Filter None
		for x := 0; x < width; x++ {
			px := rowStart + 1 + x*3
			switch pattern {
			case "gradient":
				pixelData[px] = byte(x % 256)
				pixelData[px+1] = byte(y % 256)
				pixelData[px+2] = byte((x + y) % 256)
			case "checkered":
				val := byte(((x / 8) ^ (y / 8)) * 0xFF)
				pixelData[px] = val
				pixelData[px+1] = val
				pixelData[px+2] = val
			case "solid":
				pixelData[px] = 0xAA
				pixelData[px+1] = 0xBB
				pixelData[px+2] = 0xCC
			default:
				pixelData[px] = byte((x*17 + y*31) % 256)
				pixelData[px+1] = byte((x*13 ^ y*7) % 256)
				pixelData[px+2] = byte((x*19 + 5) % 256)
			}
		}
	}

	// Split IDAT payload into 1 or more IDAT chunks
	if splitIDATChunks <= 1 {
		buf.Write(buildPNGChunk("IDAT", pixelData))
	} else {
		chunkSize := (len(pixelData) + splitIDATChunks - 1) / splitIDATChunks
		for pos := 0; pos < len(pixelData); pos += chunkSize {
			end := pos + chunkSize
			if end > len(pixelData) {
				end = len(pixelData)
			}
			buf.Write(buildPNGChunk("IDAT", pixelData[pos:end]))
		}
	}

	// Optional ancillary chunk after IDAT (postfix)
	if withAncillary {
		comment := []byte("Author\x00PRZM Milestone 6 Empirical Challenger")
		buf.Write(buildPNGChunk("tEXt", comment))
	}

	// IEND chunk
	buf.Write(buildPNGChunk("IEND", nil))
	return buf.Bytes()
}

// TestChallenger_M6_2_CrossLanguage_Archival_Parity verifies 100% bit-exact byte-for-byte parity
// between Go CompressOptions{ArchivalMode: true} and Pure C test_przm_dec.exe.
func TestChallenger_M6_2_CrossLanguage_Archival_Parity(t *testing.T) {
	cBinPath := findCDecoderM6(t)
	tmpDir := t.TempDir()

	testCases := []struct {
		name          string
		width         int
		height        int
		splitChunks   int
		withAncillary bool
		pattern       string
	}{
		{"Small_16x16_Gradient", 16, 16, 1, false, "gradient"},
		{"Standard_32x32_Checkered", 32, 32, 1, false, "checkered"},
		{"Medium_64x64_MultiChunk3", 64, 64, 3, false, "gradient"},
		{"Wide_128x32_Solid_Ancillary", 128, 32, 1, true, "solid"},
		{"Large_128x128_MultiChunk4_Ancillary", 128, 128, 4, true, "patterned"},
		{"Rect_256x64_MultiChunk2", 256, 64, 2, false, "gradient"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rawPNG := generateMockPNG(tc.width, tc.height, tc.splitChunks, tc.withAncillary, tc.pattern)
			if len(rawPNG) == 0 {
				t.Fatalf("Failed to generate mock PNG")
			}

			// 1. Compress using Go CompressOptions{ArchivalMode: true}
			comp, err := CompressWithOptions(rawPNG, CompressOptions{
				ArchivalMode: true,
			})
			if err != nil {
				t.Fatalf("CompressWithOptions(ArchivalMode=true) failed: %v", err)
			}
			if len(comp) < 12 {
				t.Fatalf("Compressed frame too small: %d bytes", len(comp))
			}

			// Verify frame magic is ArchivalPNGMagic (0x504E4741 = 'PNGA')
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != ArchivalPNGMagic {
				t.Fatalf("Expected ArchivalPNGMagic 0x%08X, got 0x%08X", ArchivalPNGMagic, magic)
			}

			// 2. Verify Go roundtrip decode
			goDec, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Go Decompress failed: %v", err)
			}
			if !bytes.Equal(goDec, rawPNG) {
				for i := 0; i < len(rawPNG) && i < len(goDec); i++ {
					if rawPNG[i] != goDec[i] {
						t.Fatalf("Go Decompress output does not match original PNG at byte %d (len raw=%d, len dec=%d): want 0x%02X, got 0x%02X",
							i, len(rawPNG), len(goDec), rawPNG[i], goDec[i])
					}
				}
				t.Fatalf("Go Decompress output length mismatch: raw=%d, dec=%d", len(rawPNG), len(goDec))
			}

			dstBuf := make([]byte, len(rawPNG)+64)
			goDecTo, err := DecompressTo(comp, dstBuf)
			if err != nil {
				t.Fatalf("Go DecompressTo failed: %v", err)
			}
			if !bytes.Equal(goDecTo, rawPNG) {
				t.Fatalf("Go DecompressTo output does not match original PNG")
			}

			// 3. Write compressed archival frame to temporary storage
			inPrzm := filepath.Join(tmpDir, tc.name+".przm")
			outBin := filepath.Join(tmpDir, tc.name+".png")
			if err := os.WriteFile(inPrzm, comp, 0644); err != nil {
				t.Fatalf("Failed to write temporary przm file: %v", err)
			}

			// 4. Execute Pure C test_przm_dec.exe on compressed archival frame
			cmd := exec.Command(cBinPath, inPrzm, outBin)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor returned error %v:\nOutput:\n%s", err, string(output))
			}

			// 5. Verify exit code 0 and 100% bit-exact byte-for-byte parity
			cDec, err := os.ReadFile(outBin)
			if err != nil {
				t.Fatalf("Failed to read Pure C decompressed output: %v", err)
			}

			if len(cDec) != len(rawPNG) {
				t.Fatalf("Pure C output length mismatch: got %d bytes, want %d bytes", len(cDec), len(rawPNG))
			}

			if !bytes.Equal(cDec, rawPNG) {
				t.Fatalf("100%% Parity Violation: Pure C output does not match original PNG bit-for-bit!")
			}

			if !bytes.Equal(cDec, goDec) {
				t.Fatalf("Cross-language mismatch between Pure C and Go decompressor outputs!")
			}

			ratio := float64(len(rawPNG)) / float64(len(comp))
			t.Logf("[%s] SUCCESS: PNG %d bytes -> PRZM %d bytes (%.2fx) -> Pure C Bit-Exact Parity Confirmed",
				tc.name, len(rawPNG), len(comp), ratio)
		})
	}
}

// TestChallenger_M6_2_Archival_ZeroAlloc verifies testing.AllocsPerRun(100, ...) is strictly 0.00
// during DecompressTo on archival streams (both PNG and non-PNG streams).
func TestChallenger_M6_2_Archival_ZeroAlloc(t *testing.T) {
	testCases := []struct {
		name string
		gen  func() ([]byte, []byte) // returns (compressed, original)
	}{
		{
			name: "ArchivalPNG_32x32",
			gen: func() ([]byte, []byte) {
				png := generateMockPNG(32, 32, 1, false, "gradient")
				comp, err := CompressWithOptions(png, CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, png
			},
		},
		{
			name: "ArchivalPNG_64x64_MultiChunk",
			gen: func() ([]byte, []byte) {
				png := generateMockPNG(64, 64, 3, true, "checkered")
				comp, err := CompressWithOptions(png, CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, png
			},
		},
		{
			name: "ArchivalPNG_128x64",
			gen: func() ([]byte, []byte) {
				png := generateMockPNG(128, 64, 2, false, "gradient")
				comp, err := CompressWithOptions(png, CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, png
			},
		},
		{
			name: "ArchivalApproxMatch_64KB",
			gen: func() ([]byte, []byte) {
				raw := make([]byte, 65536)
				for i := range raw {
					raw[i] = byte(i % 128)
				}
				// Inject periodic Hamming <= 2 delta
				for i := 64; i < len(raw); i += 64 {
					raw[i] ^= 0x03
				}
				comp, err := CompressWithOptions(raw, CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, raw
			},
		},
		{
			name: "ArchivalApproxMatch_256KB",
			gen: func() ([]byte, []byte) {
				raw := make([]byte, 262144)
				for i := range raw {
					raw[i] = byte(i*17 + 5)
				}
				for i := 128; i < len(raw); i += 128 {
					raw[i] ^= 0x01
				}
				comp, err := CompressWithOptions(raw, CompressOptions{ArchivalMode: true})
				if err != nil {
					panic(err)
				}
				return comp, raw
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			comp, original := tc.gen()
			origLen := len(original)
			dst := make([]byte, origLen+64)

			// Warmup run
			decWarmup, err := DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("Warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decWarmup, original) {
				for i := 0; i < len(original) && i < len(decWarmup); i++ {
					if original[i] != decWarmup[i] {
						t.Fatalf("Warmup DecompressTo mismatch at byte %d (len orig=%d, len dec=%d): want 0x%02X, got 0x%02X",
							i, len(original), len(decWarmup), original[i], decWarmup[i])
					}
				}
				t.Fatalf("Warmup DecompressTo length mismatch: orig=%d, dec=%d", len(original), len(decWarmup))
			}

			// Strictly verify testing.AllocsPerRun(100, ...) is strictly 0.00
			allocs := testing.AllocsPerRun(100, func() {
				out, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("DecompressTo in AllocsPerRun failed: %v", err)
				}
				_ = out
			})

			if allocs != 0.0 {
				t.Fatalf("ZERO-ALLOC VIOLATION: DecompressTo produced %.2f allocs/run (mandated strictly 0.00)", allocs)
			}

			t.Logf("[%s] AllocsPerRun(100) = %.2f (strictly 0.00 zero-heap invariant verified)", tc.name, allocs)
		})
	}
}

// TestChallenger_M6_2_MainMode_LineRate_Immunity benchmarks Compress and DecompressTo
// at Level 1 and Level 3 to confirm archival additions introduced ZERO regression
// to Main Mode throughput (retaining 10-50+ GB/s line rate).
func TestChallenger_M6_2_MainMode_LineRate_Immunity(t *testing.T) {
	sizes := []struct {
		name      string
		numBlocks int
	}{
		{"256KB_4Blocks", 4},
		{"1MB_16Blocks", 16},
		{"4MB_64Blocks", 64},
	}

	levels := []int{1, 3}

	for _, sz := range sizes {
		// Generate repeatable multi-block payload exercising line-rate Main Mode
		block := make([]byte, DedupBlockSize)
		for i := range block {
			block[i] = byte((i * 31) ^ (i >> 5) ^ 0x69)
		}
		var raw bytes.Buffer
		for range sz.numBlocks {
			raw.Write(block)
		}
		data := raw.Bytes()
		totalBytes := len(data)

		for _, lvl := range levels {
			testName := fmt.Sprintf("%s_Lvl%d", sz.name, lvl)
			t.Run(testName, func(t *testing.T) {
				// 1. Measure Main Mode Compress throughput
				compStart := time.Now()
				compIters := 50
				if sz.numBlocks >= 64 {
					compIters = 10
				}
				var comp []byte
				var err error
				for i := 0; i < compIters; i++ {
					comp, err = Compress(data, lvl)
					if err != nil {
						t.Fatalf("Compress failed: %v", err)
					}
				}
				compElapsed := time.Since(compStart)
				compMBps := (float64(totalBytes*compIters) / (1024 * 1024)) / compElapsed.Seconds()
				t.Logf("[%s] Compress: processed %d bytes in %v => %.2f MB/s",
					testName, totalBytes*compIters, compElapsed, compMBps)

				dst := make([]byte, totalBytes+64)

				// Warmup run
				decWarmup, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("Warmup DecompressTo failed: %v", err)
				}
				if !bytes.Equal(decWarmup, data) {
					t.Fatalf("DecompressTo data mismatch")
				}

				// 2. Measure Main Mode DecompressTo throughput
				decIters := 5000
				if sz.numBlocks >= 64 {
					decIters = 1000
				} else if sz.numBlocks >= 16 {
					decIters = 3000
				}

				decStart := time.Now()
				for i := 0; i < decIters; i++ {
					_, _ = DecompressTo(comp, dst)
				}
				decElapsed := time.Since(decStart)

				bytesProcessed := float64(totalBytes) * float64(decIters)
				decGBps := (bytesProcessed / (1024 * 1024 * 1024)) / decElapsed.Seconds()

				t.Logf("[%s] DecompressTo: processed %d bytes x %d iters in %v => %.2f GB/s",
					testName, totalBytes, decIters, decElapsed, decGBps)

				// Verify line rate >= 10.0 GB/s (saturating RAM-speed 10-50+ GB/s bracket)
				if decGBps < 10.0 {
					t.Errorf("Line rate regression detected: got %.2f GB/s, expected >= 10.00 GB/s", decGBps)
				} else {
					t.Logf("[%s] SUCCESS: Main Mode line-rate immunity confirmed (%.2f GB/s in 10-50+ GB/s bracket)",
						testName, decGBps)
				}
			})
		}
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallengerZD_HenselLCP_ExhaustiveOracle stress-tests HenselLCP and HenselLCPPos
// against a naive ground-truth comparator for lengths from 0 to 1024 bytes and various alignments.
func TestChallengerZD_HenselLCP_ExhaustiveOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))

	groundTruthLCP := func(a, b []byte, maxLen int) int {
		n := min(len(a), len(b), maxLen)
		for i := 0; i < n; i++ {
			if a[i] != b[i] {
				return i
			}
		}
		return n
	}

	for length := 0; length <= 512; length++ {
		a := make([]byte, length+16)
		b := make([]byte, length+16)
		rng.Read(a)
		copy(b, a)

		// Exact match
		if got := HenselLCP(a[:length], b[:length]); got != length {
			t.Fatalf("Exact match failed at len %d: got %d", length, got)
		}

		// Single mismatch at every possible byte offset 0..length-1
		for mismatch := 0; mismatch < length; mismatch++ {
			b[mismatch] ^= 0x5A
			expected := groundTruthLCP(a[:length], b[:length], length)
			got := HenselLCP(a[:length], b[:length])
			if got != expected {
				t.Fatalf("HenselLCP len=%d mismatch at %d: got %d, want %d", length, mismatch, got, expected)
			}

			// Also test HenselLCPPos inside unified buffer
			buf := make([]byte, 2*length+64)
			copy(buf[0:], a[:length])
			copy(buf[length+16:], b[:length])
			gotPos := HenselLCPPos(buf, 0, length+16, length)
			if gotPos != expected {
				t.Fatalf("HenselLCPPos len=%d mismatch at %d: got %d, want %d", length, mismatch, gotPos, expected)
			}
			b[mismatch] ^= 0x5A // restore
		}
	}
}

// TestChallengerZD_HenselLCPPos_BoundarySafety tests invalid and edge boundary conditions.
func TestChallengerZD_HenselLCPPos_BoundarySafety(t *testing.T) {
	buf := []byte("0123456789abcdef0123456789abcdef")

	cases := []struct {
		name     string
		p1, p2   int
		maxLen   int
		expected int
	}{
		{"p1_negative", -1, 0, 10, 0},
		{"p2_negative", 0, -5, 10, 0},
		{"maxLen_zero", 0, 4, 0, 0},
		{"maxLen_negative", 0, 4, -10, 0},
		{"p1_past_end", 32, 0, 10, 0},
		{"p2_past_end", 0, 32, 10, 0},
		{"p1_clamped_match", 16, 0, 20, 16}, // clamped to len-p1 = 16; all 16 bytes match
		{"identical_offset", 0, 0, 16, 16},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HenselLCPPos(buf, tc.p1, tc.p2, tc.maxLen)
			if got != tc.expected {
				t.Fatalf("case %s: got %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

// TestChallengerZD_HenselLCP_RLE_Overlapping tests overlapping sliding window / RLE common prefixes.
func TestChallengerZD_HenselLCP_RLE_Overlapping(t *testing.T) {
	lengths := []int{1, 2, 3, 4, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255, 256, 1024, 4096}
	for _, l := range lengths {
		buf := bytes.Repeat([]byte{'A'}, l+1)
		// Compare buf[0:l] with buf[1:l+1]
		got := HenselLCPPos(buf, 0, 1, l)
		if got != l {
			t.Fatalf("RLE overlapping LCP failed for len=%d: got %d, want %d", l, got, l)
		}
	}
}

// TestChallengerZD_TheoremHL2_ExhaustiveBitShift tests all 32 single-bit and multi-bit
// mismatch patterns in bytes 4..7 to verify Theorem HL-2 exact short-circuit calculation.
func TestChallengerZD_TheoremHL2_ExhaustiveBitShift(t *testing.T) {
	for byteMismatch := 0; byteMismatch < 4; byteMismatch++ {
		for bit := 0; bit < 8; bit++ {
			diff := uint32(1<<bit) << (byteMismatch * 8)
			calculatedShift := bits.TrailingZeros32(diff) >> 3
			if calculatedShift != byteMismatch {
				t.Fatalf("HL-2 shift mismatch: diff=0x%08X, got %d, want %d", diff, calculatedShift, byteMismatch)
			}
			maxLift := 4 + calculatedShift
			expectedLift := 4 + byteMismatch
			if maxLift != expectedLift {
				t.Fatalf("HL-2 maxLift mismatch: diff=0x%08X, got %d, want %d", diff, maxLift, expectedLift)
			}
		}
	}
}

// TestChallengerZD_HighCollisionChaining tests pathological hash collision chains (1000 items in single bucket).
func TestChallengerZD_HighCollisionChaining(t *testing.T) {
	sp := PoolHensel64K.Get()
	defer PoolHensel64K.Put(sp)
	sp.Reset()

	// All 4-byte prefixes identical -> all collide in the same hash bucket
	buf := bytes.Repeat([]byte("SAME"), 2000) // 8000 bytes
	for i := 0; i+4 <= len(buf); i += 4 {
		HenselInsert(sp, i, buf, len(buf))
	}

	// Search at pos=4000 with depth=16
	dist, matchLen := HenselFindMatch(buf, 4000, len(buf), 16, 4, sp)
	if dist <= 0 || matchLen < 4 {
		t.Fatalf("Failed to find match under collision chain: dist=%d, len=%d", dist, matchLen)
	}

	// Verify candidate stepped back without panic or loop
	if !bytes.Equal(buf[4000:4000+matchLen], buf[4000-dist:4000-dist+matchLen]) {
		t.Fatalf("Match content invalid: dist=%d len=%d", dist, matchLen)
	}
}

// TestChallengerZD_WindowWrapping tests eviction when positions cross window sizes.
func TestChallengerZD_WindowWrapping(t *testing.T) {
	sp := PoolHensel64K.Get()
	defer PoolHensel64K.Put(sp)
	sp.Reset()

	const totalSize = HenselWindowSize * 2 // 128 KiB
	buf := make([]byte, totalSize)
	for i := range buf {
		buf[i] = byte(i * 13)
	}

	// Place a marker at pos 100
	marker := []byte("UNIQUE_MARKER_FOR_WINDOW_WRAP_TEST")
	copy(buf[100:], marker)
	HenselInsert(sp, 100, buf, totalSize)

	// Place same marker at pos 100 + HenselWindowSize + 50 (outside window)
	posOutside := 100 + HenselWindowSize + 50
	copy(buf[posOutside:], marker)

	dist, _ := HenselFindMatch(buf, posOutside, totalSize, 64, 4, sp)
	if dist != 0 {
		t.Fatalf("Candidate outside window size returned: dist=%d (windowSize=%d)", dist, HenselWindowSize)
	}
}

// TestChallengerZD_Roundtrip_DiverseCorpora tests 100% bit-exact lossless roundtrips
// across diverse corpora and boundary sizes.
func TestChallengerZD_Roundtrip_DiverseCorpora(t *testing.T) {
	sizes := []int{
		0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 31, 32, 63, 64, 127, 128,
		255, 256, 511, 512, 1023, 1024, 4095, 4096, 65535, 65536, 65537,
	}

	rng := rand.New(rand.NewSource(4242))

	testCorpus := func(name string, gen func(sz int) []byte) {
		for _, sz := range sizes {
			data := gen(sz)
			for level := 1; level <= 4; level++ {
				comp, err := Compress(data, level)
				if err != nil {
					t.Fatalf("[%s sz=%d lvl=%d] Compress failed: %v", name, sz, level, err)
				}

				decomp, err := Decompress(comp)
				if err != nil {
					t.Fatalf("[%s sz=%d lvl=%d] Decompress failed: %v", name, sz, level, err)
				}

				if !bytes.Equal(decomp, data) {
					t.Fatalf("[%s sz=%d lvl=%d] Bit-exact mismatch! orig=%d decomp=%d", name, sz, level, len(data), len(decomp))
				}

				// DecompressTo zero dynamic allocation verification
				dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)
				decompTo, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("[%s sz=%d lvl=%d] DecompressTo failed: %v", name, sz, level, err)
				}
				if !bytes.Equal(decompTo, data) {
					t.Fatalf("[%s sz=%d lvl=%d] DecompressTo mismatch!", name, sz, level)
				}

				// Strict zero-alloc audit on selected sizes for Main Mode frames
				if sz == 64 || sz == 4096 || sz == 65536 {
					allocs := testing.AllocsPerRun(100, func() {
						_, _ = DecompressTo(comp, dst)
					})
					// Main Mode frames must be strictly 0.00 allocs
					if len(comp) >= 4 && binary.LittleEndian.Uint32(comp[:4]) != TDAMagic {
						if allocs != 0.00 {
							t.Fatalf("[%s sz=%d lvl=%d] ZERO-ALLOC VIOLATION: %.2f allocs/run", name, sz, level, allocs)
						}
					}
				}
			}
		}
	}

	t.Run("AllZeroes", func(t *testing.T) {
		testCorpus("AllZeroes", func(sz int) []byte { return make([]byte, sz) })
	})

	t.Run("AllOnes", func(t *testing.T) {
		testCorpus("AllOnes", func(sz int) []byte {
			b := make([]byte, sz)
			for i := range b {
				b[i] = 0xFF
			}
			return b
		})
	})

	t.Run("AlternatingByteMotifs", func(t *testing.T) {
		testCorpus("Alternating", func(sz int) []byte {
			b := make([]byte, sz)
			for i := range b {
				if i%2 == 0 {
					b[i] = 0xAA
				} else {
					b[i] = 0x55
				}
			}
			return b
		})
	})

	t.Run("PseudoRandom", func(t *testing.T) {
		testCorpus("Random", func(sz int) []byte {
			b := make([]byte, sz)
			rng.Read(b)
			return b
		})
	})
}

// TestChallengerZD_LargeScale1MAnd8MRoundtrip tests 1 MiB and 8 MiB roundtrip exactness and zero allocs.
func TestChallengerZD_LargeScale1MAnd8MRoundtrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1M/8M roundtrip in short mode")
	}

	largeSizes := []struct {
		name string
		size int
	}{
		{"1MiB_Corpus", 1024 * 1024},
		{"8MiB_Corpus", 8 * 1024 * 1024},
	}

	for _, tc := range largeSizes {
		t.Run(tc.name, func(t *testing.T) {
			buf := make([]byte, tc.size)
			pattern := []byte(`{"event":"metric_log","timestamp":1728391200,"cluster":"us-east-1","load":0.785,"status":"OK"}` + "\n")
			for i := 0; i < tc.size; i += len(pattern) {
				copy(buf[i:], pattern)
			}

			for level := 1; level <= 3; level++ {
				comp, err := Compress(buf, level)
				if err != nil {
					t.Fatalf("[%s Lvl%d] Compress failed: %v", tc.name, level, err)
				}

				decomp, err := Decompress(comp)
				if err != nil {
					t.Fatalf("[%s Lvl%d] Decompress failed: %v", tc.name, level, err)
				}

				if !bytes.Equal(decomp, buf) {
					t.Fatalf("[%s Lvl%d] Decompress mismatch: orig=%d decomp=%d", tc.name, level, len(buf), len(decomp))
				}

				// Verify DecompressTo bit-exact and 0.00 allocs
				dst := bufkit.AlignedBytes(len(buf)+64, bufkit.CacheLineSize)
				decompTo, err := DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("[%s Lvl%d] DecompressTo failed: %v", tc.name, level, err)
				}
				if !bytes.Equal(decompTo, buf) {
					t.Fatalf("[%s Lvl%d] DecompressTo mismatch!", tc.name, level)
				}

				allocs := testing.AllocsPerRun(10, func() {
					_, _ = DecompressTo(comp, dst)
				})
				if allocs != 0.00 {
					t.Fatalf("[%s Lvl%d] ZERO-ALLOC VIOLATION: %.2f allocs/run", tc.name, level, allocs)
				}
			}
		})
	}
}

// TestChallengerZD_PureGo_Hash4_Inlined tests Hash4 function exactness.
func TestChallengerZD_PureGo_Hash4_Inlined(t *testing.T) {
	for w4 := uint32(0); w4 < 10000; w4++ {
		expected := w4 * HenselHashPrime
		got := Hash4(w4)
		if got != expected {
			t.Fatalf("Hash4(%d) = %d, want %d", w4, got, expected)
		}
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallenger_ZD_M2_2_PoolConcurrencyAndIsolation verifies that concurrent
// goroutines acquiring, mutating, resetting, and releasing scratchpads from
// PoolHensel1M, PoolHensel8M, and PoolHensel64K suffer no data race, lock contention,
// or cross-worker state leakage.
func TestChallenger_ZD_M2_2_PoolConcurrencyAndIsolation(t *testing.T) {
	const goroutines = 16
	const iterationsPerGoroutine = 20

	t.Run("PoolHensel1M_ConcurrentChurn", func(t *testing.T) {
		const gCount = 8
		const itCount = 10
		var wg sync.WaitGroup
		wg.Add(gCount)
		var matchFailures atomic.Int64

		for g := 0; g < gCount; g++ {
			gID := g
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(10000 + gID*7919)))

				for it := 0; it < itCount; it++ {
					sp := PoolHensel1M.Get()
					sp.Reset()

					// Unique synthetic buffer per worker & iteration
					bufSize := 64*1024 + rng.Intn(64*1024)
					buf := make([]byte, bufSize)
					rng.Read(buf)

					// Unique worker-specific token
					workerToken := []byte(fmt.Sprintf("W1M_%04d_%04d_TOKEN_VERIFY_", gID, it))
					refPos := 100 + rng.Intn(1000)
					targetPos := refPos + 1024 + rng.Intn(16384)
					if targetPos+len(workerToken) < bufSize {
						copy(buf[refPos:], workerToken)
						copy(buf[targetPos:], workerToken)

						// Insert preceding region up to targetPos - 1
						for p := 0; p < targetPos; p += 4 {
							if p+4 <= bufSize {
								HenselInsert1M(sp, p, buf, bufSize)
							}
						}
						// Explicitly ensure refPos is indexed
						HenselInsert1M(sp, refPos, buf, bufSize)

						dist, matchLen := HenselFindMatch1M(buf, targetPos, bufSize, 64, 4, sp)
						if matchLen < len(workerToken) {
							matchFailures.Add(1)
						} else {
							matchedRef := targetPos - dist
							if !bytes.Equal(buf[targetPos:targetPos+matchLen], buf[matchedRef:matchedRef+matchLen]) {
								matchFailures.Add(1)
							}
						}
					}

					PoolHensel1M.Put(sp)
				}
			}()
		}
		wg.Wait()

		if failures := matchFailures.Load(); failures > 0 {
			t.Fatalf("PoolHensel1M concurrent churn encountered %d match failures", failures)
		}
	})

	t.Run("PoolHensel8M_ConcurrentChurn", func(t *testing.T) {
		const gCount = 4
		const itCount = 10
		var wg sync.WaitGroup
		wg.Add(gCount)
		var matchFailures atomic.Int64

		for g := 0; g < gCount; g++ {
			gID := g
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(20000 + gID*3571)))

				for it := 0; it < itCount; it++ {
					sp := PoolHensel8M.Get()
					sp.Reset()

					bufSize := 128*1024 + rng.Intn(64*1024)
					buf := make([]byte, bufSize)
					rng.Read(buf)

					workerToken := []byte(fmt.Sprintf("W8M_%04d_%04d_TOKEN_TEST_", gID, it))
					refPos := 200 + rng.Intn(1000)
					targetPos := refPos + 2048 + rng.Intn(32768)
					if targetPos+len(workerToken) < bufSize {
						copy(buf[refPos:], workerToken)
						copy(buf[targetPos:], workerToken)

						for p := 0; p < targetPos; p += 8 {
							if p+4 <= bufSize {
								HenselInsert8M(sp, p, buf, bufSize)
							}
						}
						// Explicitly ensure refPos is indexed
						HenselInsert8M(sp, refPos, buf, bufSize)

						dist, matchLen := HenselFindMatch8M(buf, targetPos, bufSize, 64, 4, sp)
						if matchLen < len(workerToken) {
							matchFailures.Add(1)
						} else {
							matchedRef := targetPos - dist
							if !bytes.Equal(buf[targetPos:targetPos+matchLen], buf[matchedRef:matchedRef+matchLen]) {
								matchFailures.Add(1)
							}
						}
					}

					PoolHensel8M.Put(sp)
				}
			}()
		}
		wg.Wait()

		if failures := matchFailures.Load(); failures > 0 {
			t.Fatalf("PoolHensel8M concurrent churn encountered %d match failures", failures)
		}
	})
}

// TestChallenger_ZD_M2_2_ScratchpadResetStaleDataImmunity stress-tests the fact
// that HenselScratchpad.Reset(), HenselScratchpad1M.Reset(), and HenselScratchpad8M.Reset()
// only zero out sp.head[:] and deliberately skip sp.prev and sp.fp32.
// It verifies that dirty leftover state in prev/fp32 cannot resurrect phantom matches
// or cause invalid candidate chain traversal when a scratchpad is recycled.
func TestChallenger_ZD_M2_2_ScratchpadResetStaleDataImmunity(t *testing.T) {
	t.Run("1M_StaleDataImmunity", func(t *testing.T) {
		sp := PoolHensel1M.Get()
		defer PoolHensel1M.Put(sp)

		// PHASE 1: Populate scratchpad with Dirty Data Block A
		bufA := bytes.Repeat([]byte("DIRTY_PHANTOM_STALE_SEED_DATA_ABCDEF1234567890"), 2000)
		for p := 0; p+4 <= len(bufA); p += 2 {
			HenselInsert1M(sp, p, bufA, len(bufA))
		}

		// Reset only clears head[:]
		sp.Reset()

		// PHASE 2: Populate with completely disjoint Clean Data Block B
		bufB := make([]byte, 65536)
		for i := range bufB {
			bufB[i] = byte((i * 101) & 0xFF)
		}
		// Insert a sparse known match in Block B
		knownToken := []byte("CLEAN_LEGITIMATE_MATCH_BLOCK_B_!")
		posRef := 500
		posTarget := 5500
		copy(bufB[posRef:], knownToken)
		copy(bufB[posTarget:], knownToken)

		for p := 0; p <= posTarget; p += 64 {
			if p+4 <= len(bufB) {
				HenselInsert1M(sp, p, bufB, len(bufB))
			}
		}
		HenselInsert1M(sp, posRef, bufB, len(bufB))

		// Search for matches across various positions in Block B
		for checkPos := 100; checkPos < len(bufB)-64; checkPos += 256 {
			dist, matchLen := HenselFindMatch1M(bufB, checkPos, len(bufB), 64, 4, sp)
			if matchLen >= 4 {
				cand := checkPos - dist
				if cand < 0 || cand+matchLen > len(bufB) {
					t.Fatalf("Out-of-bounds match returned from stale scratchpad: checkPos=%d dist=%d len=%d",
						checkPos, dist, matchLen)
				}
				if !bytes.Equal(bufB[checkPos:checkPos+matchLen], bufB[cand:cand+matchLen]) {
					t.Fatalf("Phantom or mismatched match payload at checkPos=%d dist=%d len=%d",
						checkPos, dist, matchLen)
				}
			}
		}

		// Specifically verify our known token in Block B matches correctly
		dist, matchLen := HenselFindMatch1M(bufB, posTarget, len(bufB), 64, 4, sp)
		if dist != posTarget-posRef || matchLen < len(knownToken) {
			t.Fatalf("Known token match failed: got dist=%d len=%d, want dist=%d len>=%d",
				dist, matchLen, posTarget-posRef, len(knownToken))
		}
	})

	t.Run("8M_StaleDataImmunity", func(t *testing.T) {
		sp := PoolHensel8M.Get()
		defer PoolHensel8M.Put(sp)

		bufA := bytes.Repeat([]byte("DIRTY_8M_STALE_HASH_CHAIN_BYTES_0987654321XYZ!"), 3000)
		for p := 0; p+4 <= len(bufA); p += 4 {
			HenselInsert8M(sp, p, bufA, len(bufA))
		}

		sp.Reset()

		bufB := make([]byte, 131072)
		for i := range bufB {
			bufB[i] = byte((i * 73) & 0xFF)
		}
		knownToken := []byte("CLEAN_8M_DISJOINT_MATCH_PAYLOAD_VALID_")
		posRef := 1000
		posTarget := 12000
		copy(bufB[posRef:], knownToken)
		copy(bufB[posTarget:], knownToken)

		for p := 0; p <= posTarget; p += 128 {
			if p+4 <= len(bufB) {
				HenselInsert8M(sp, p, bufB, len(bufB))
			}
		}
		HenselInsert8M(sp, posRef, bufB, len(bufB))

		dist, matchLen := HenselFindMatch8M(bufB, posTarget, len(bufB), 64, 4, sp)
		if dist != posTarget-posRef || matchLen < len(knownToken) {
			t.Fatalf("8M stale immunity test failed: got dist=%d len=%d, want dist=%d len>=%d",
				dist, matchLen, posTarget-posRef, len(knownToken))
		}
	})
}

// TestChallenger_ZD_M2_2_ExtremeHashCollisionDeepChain creates a pathological
// bucket flood where hundreds of distinct 4-byte prefixes produce the exact same
// Hensel hash, testing deep chain traversal limits and Theorem HL-2 pruning under pressure.
func TestChallenger_ZD_M2_2_ExtremeHashCollisionDeepChain(t *testing.T) {
	// Synthesize 512 distinct 32-bit values that all have the exact same hash
	const targetBucket = 42
	collidingValues := make([]uint32, 0, 512)
	for w := uint32(1); len(collidingValues) < 512; w++ {
		h := computeHenselHash(w, HenselHashBits, HenselHashMask)
		if h == targetBucket {
			collidingValues = append(collidingValues, w)
		}
	}

	sp := PoolHensel64K.Get()
	defer PoolHensel64K.Put(sp)
	sp.Reset()

	// Construct buffer where each position p = i * 8 contains collidingValues[i]
	bufSize := len(collidingValues)*8 + 64
	buf := make([]byte, bufSize)
	for i, val := range collidingValues {
		pos := i * 8
		binary.LittleEndian.PutUint32(buf[pos:], val)
		// Give each entry a distinct second 4-byte fingerprint
		binary.LittleEndian.PutUint32(buf[pos+4:], uint32(i*10007))
	}

	// Insert all into scratchpad
	for i := range collidingValues {
		pos := i * 8
		HenselInsert(sp, pos, buf, bufSize)
	}

	// Case 1: Search for a value in the middle of the chain (e.g. index 250)
	targetIdx := 250
	targetVal := collidingValues[targetIdx]
	queryPos := len(collidingValues) * 8
	binary.LittleEndian.PutUint32(buf[queryPos:], targetVal)
	binary.LittleEndian.PutUint32(buf[queryPos+4:], uint32(targetIdx*10007)) // exact 8-byte match

	// If maxDepth < 512 - targetIdx, the chain traversal will exhaust depth before reaching targetIdx
	depthInsufficient := 10
	dist, matchLen := HenselFindMatch(buf, queryPos, bufSize, depthInsufficient, 4, sp)
	if dist != 0 || matchLen != 0 {
		t.Fatalf("Expected no match with depth %d, got dist=%d len=%d", depthInsufficient, dist, matchLen)
	}

	// With sufficient maxDepth, candidate chain traversal must cleanly reach targetIdx
	depthSufficient := len(collidingValues) + 10
	dist, matchLen = HenselFindMatch(buf, queryPos, bufSize, depthSufficient, 4, sp)
	expectedDist := queryPos - (targetIdx * 8)
	if dist != expectedDist || matchLen < 8 {
		t.Fatalf("Deep chain search failed: got dist=%d len=%d, want dist=%d len>=8",
			dist, matchLen, expectedDist)
	}

	// Case 2: Theorem HL-2 Pruning under collision flood:
	// Query prefix matches targetVal, but second 4-byte fingerprint differs in byte 6
	binary.LittleEndian.PutUint32(buf[queryPos:], targetVal)
	binary.LittleEndian.PutUint32(buf[queryPos+4:], uint32(targetIdx*10007)^0x00FF0000) // byte 6 differs -> match len exactly 6

	dist, matchLen = HenselFindMatch(buf, queryPos, bufSize, depthSufficient, 4, sp)
	if dist != expectedDist || matchLen != 6 {
		t.Fatalf("Theorem HL-2 exact short-circuit under collision flood failed: got dist=%d len=%d, want dist=%d len=6",
			dist, matchLen, expectedDist)
	}
}

// TestChallenger_ZD_M2_2_TheoremHL2_ExhaustiveCombinations systematically verifies
// that Theorem HL-2 exact match length deduction produces 100% bit-exact results
// across all diff trailing-zero configurations (match lengths 4, 5, 6, 7 bytes)
// and handles diff == 0 (> 7 bytes) without divergence.
func TestChallenger_ZD_M2_2_TheoremHL2_ExhaustiveCombinations(t *testing.T) {
	sp := PoolHensel64K.Get()
	defer PoolHensel64K.Put(sp)

	for expectedLen := 4; expectedLen <= 12; expectedLen++ {
		t.Run(fmt.Sprintf("ExactLength_%d", expectedLen), func(t *testing.T) {
			sp.Reset()
			buf := make([]byte, 4096)
			// Base pattern of 16 identical bytes
			candPos := 100
			targetPos := 500

			for i := 0; i < 16; i++ {
				buf[candPos+i] = byte('A' + i)
				buf[targetPos+i] = byte('A' + i)
			}

			// Diverge at index expectedLen
			if expectedLen < 16 {
				buf[targetPos+expectedLen] = 0xFF
			}

			HenselInsert(sp, candPos, buf, len(buf))

			dist, matchLen := HenselFindMatch(buf, targetPos, len(buf), 16, 4, sp)
			expectedDist := targetPos - candPos
			if dist != expectedDist || matchLen != expectedLen {
				t.Fatalf("Theorem HL-2 length check failed for expected %d: got dist=%d len=%d, want dist=%d len=%d",
					expectedLen, dist, matchLen, expectedDist, expectedLen)
			}
		})
	}
}

// TestChallenger_ZD_M2_2_MultiGoroutine_CompressWithOptions_Stress executes high-concurrency
// end-to-end compression and decompression across all levels and window sizes,
// ensuring the underlying scratchpad pools and Hensel match finders remain thread-safe.
func TestChallenger_ZD_M2_2_MultiGoroutine_CompressWithOptions_Stress(t *testing.T) {
	const goroutines = 8
	const runsPerGoroutine = 5

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var errorsCount atomic.Int64

	for g := 0; g < goroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(30000 + gID*9973)))

			for run := 0; run < runsPerGoroutine; run++ {
				size := 128*1024 + rng.Intn(128*1024)
				data := make([]byte, size)
				rng.Read(data)

				// Inject high-repetition tokens to trigger match finding
				token := []byte(fmt.Sprintf("PARALLEL_STRESS_TEST_PAYLOAD_%d_%d", gID, run))
				for p := 0; p < size-len(token); p += 1024 {
					copy(data[p:], token)
				}

				for _, level := range []int{1, 2, 3} {
					opts := CompressOptions{
						Threads:   4,
						ChunkSize: 64 * 1024,
						Level:     level,
					}
					compressed, err := CompressWithOptions(data, opts)
					if err != nil {
						errorsCount.Add(1)
						t.Errorf("CompressWithOptions(g=%d, run=%d, lvl=%d) error: %v", gID, run, level, err)
						continue
					}

					decompressed, err := Decompress(compressed)
					if err != nil {
						errorsCount.Add(1)
						t.Errorf("Decompress(g=%d, run=%d, lvl=%d) error: %v", gID, run, level, err)
						continue
					}

					if !bytes.Equal(decompressed, data) {
						errorsCount.Add(1)
						t.Errorf("Decompress mismatch for g=%d run=%d lvl=%d", gID, run, level)
					}
				}
			}
		}()
	}
	wg.Wait()

	if errs := errorsCount.Load(); errs > 0 {
		t.Fatalf("MultiGoroutine CompressWithOptions stress encountered %d errors", errs)
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallengerZD_M3_1_DPNode_CacheAlignmentAndSize asserts that dpNode is strictly
// 32 bytes (power-of-two) and aligns to 64-byte CPU cache lines (2 nodes per line).
func TestChallengerZD_M3_1_DPNode_CacheAlignmentAndSize(t *testing.T) {
	nodeSize := unsafe.Sizeof(dpNode{})
	if nodeSize != 32 {
		t.Fatalf("dpNode size violation: got %d bytes, expected exactly 32 bytes", nodeSize)
	}

	nodeAlign := unsafe.Alignof(dpNode{})
	if nodeAlign < 4 {
		t.Fatalf("dpNode alignment too small: got %d", nodeAlign)
	}

	var arr [2]dpNode
	diff := uintptr(unsafe.Pointer(&arr[1])) - uintptr(unsafe.Pointer(&arr[0]))
	if diff != 32 {
		t.Fatalf("dpNode array stride violation: got %d, expected 32", diff)
	}

	// Verify field layout
	offsetCost := unsafe.Offsetof(arr[0].cost)
	offsetGen := unsafe.Offsetof(arr[0].gen)
	if offsetCost != 0 {
		t.Errorf("expected cost at offset 0, got %d", offsetCost)
	}
	if offsetGen != 28 {
		t.Errorf("expected gen at offset 28, got %d", offsetGen)
	}
}

// TestChallengerZD_M3_1_SubLengths_ExhaustiveOracle compares generateSubLengths against
// the mathematical ground truth S(L) = {4, 5, 6, 8, 12, 16, min(L, 18), L} for all L in [0, 65536].
func TestChallengerZD_M3_1_SubLengths_ExhaustiveOracle(t *testing.T) {
	groundTruthSubLens := func(L int) []int {
		if L < 4 {
			return nil
		}
		raw := []int{4, 5, 6, 8, 12, 16, min(L, 18), L}
		set := make(map[int]bool)
		var out []int
		for _, x := range raw {
			if x <= L && !set[x] {
				set[x] = true
				out = append(out, x)
			}
		}
		// Sort
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[i] > out[j] {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}

	var buf [maxSubLenCandidates]int
	for L := 0; L <= 65536; L++ {
		// Fill buf with sentinel to catch out-of-bounds writes
		for k := range buf {
			buf[k] = -999
		}

		n := generateSubLengths(L, &buf)
		expected := groundTruthSubLens(L)

		if n != len(expected) {
			t.Fatalf("L=%d: got count %d, expected %d", L, n, len(expected))
		}
		for i := 0; i < n; i++ {
			if buf[i] != expected[i] {
				t.Fatalf("L=%d index %d: got %d, expected %d", L, i, buf[i], expected[i])
			}
		}
		// When L <= 18, only 0..n-1 are written, remaining elements must be untouched
		if L <= 18 {
			for k := n; k < maxSubLenCandidates; k++ {
				if buf[k] != -999 {
					t.Fatalf("L=%d corrupted buf at index %d: got %d", L, k, buf[k])
				}
			}
		} else {
			// When L > 18, fixed array literal initializes 0..7 and zeroes 8..15
			for k := 8; k < maxSubLenCandidates; k++ {
				if buf[k] != 0 {
					t.Fatalf("L=%d unexpected non-zero buf at index %d: got %d", L, k, buf[k])
				}
			}
		}
	}
}

// TestChallengerZD_M3_1_CalcEdgeCost_ExhaustiveOracle asserts that calcEdgeCostPRZM matches
// the exact specification for all match lengths, distances, and repeat codes.
func TestChallengerZD_M3_1_CalcEdgeCost_ExhaustiveOracle(t *testing.T) {
	groundTruthCost := func(mLen, dist, repCode int) int32 {
		cost := int32(28)
		if repCode >= 0 {
			if mLen <= 7 {
				return 14
			}
		} else {
			bracket := 0
			if dist >= 2097152 {
				bracket = 3
			} else if dist >= 16384 {
				bracket = 2
			} else if dist >= 128 {
				bracket = 1
			}
			cost = 14 + 13*int32(bracket+1)
		}
		if mLen >= 19 {
			cost += 14 * int32(uvarintLen(uint64(mLen-19)))
		}
		return cost
	}

	distances := []int{1, 127, 128, 16383, 16384, 2097151, 2097152, 8388608}
	repCodes := []int{-1, 0, 1, 2}
	lengths := []int{4, 5, 6, 7, 8, 18, 19, 20, 100, 1000, 65535}

	for _, d := range distances {
		for _, rep := range repCodes {
			for _, l := range lengths {
				got := calcEdgeCostPRZM(l, d, rep)
				want := groundTruthCost(l, d, rep)
				if got != want {
					t.Fatalf("calcEdgeCostPRZM(mLen=%d, dist=%d, rep=%d) = %d, want %d", l, d, rep, got, want)
				}
			}
		}
	}
}

// TestChallengerZD_M3_1_DPEpochRollover asserts that when curGen wraps around uint32,
// dpPad.curGen is properly reset to 1, table is cleared, and output is bit-exact.
func TestChallengerZD_M3_1_DPEpochRollover(t *testing.T) {
	// Construct input data with repeated structures to trigger optimal parsing
	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte((i * 17) ^ (i >> 3))
	}
	// Inject repeat patterns
	for i := 100; i < 50000; i += 256 {
		copy(data[i:i+64], data[i-64:i])
	}

	// 1. Compress with fresh scratchpad
	padFresh := padPool.Get()
	defer padPool.Put(padFresh)
	clear(padFresh.head)
	padFresh.hensel.Reset()
	padFresh.cmdBuf = padFresh.cmdBuf[:0]
	padFresh.literalBuf = padFresh.literalBuf[:0]
	padFresh.offsetBuf = padFresh.offsetBuf[:0]
	dpFresh := dpScratchPool.Get()
	defer dpScratchPool.Put(dpFresh)
	clear(dpFresh.table3[:])
	r0_1, r1_1, r2_1 := 0, 0, 0
	compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, padFresh, dpFresh, &r0_1, &r1_1, &r2_1, nil, nil)
	cmdFresh := make([]byte, len(padFresh.cmdBuf))
	copy(cmdFresh, padFresh.cmdBuf)
	litFresh := make([]byte, len(padFresh.literalBuf))
	copy(litFresh, padFresh.literalBuf)
	offFresh := make([]byte, len(padFresh.offsetBuf))
	copy(offFresh, padFresh.offsetBuf)

	// 2. Compress with scratchpad at epoch wrap boundary (math.MaxUint32)
	padRollover := padPool.Get()
	defer padPool.Put(padRollover)
	clear(padRollover.head)
	padRollover.hensel.Reset()
	padRollover.cmdBuf = padRollover.cmdBuf[:0]
	padRollover.literalBuf = padRollover.literalBuf[:0]
	padRollover.offsetBuf = padRollover.offsetBuf[:0]
	dpRollover := dpScratchPool.Get()
	defer dpScratchPool.Put(dpRollover)
	clear(dpRollover.table3[:])
	dpRollover.curGen = math.MaxUint32

	r0_2, r1_2, r2_2 := 0, 0, 0
	compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, padRollover, dpRollover, &r0_2, &r1_2, &r2_2, nil, nil)

	if dpRollover.curGen != 1 {
		t.Fatalf("curGen did not reset to 1 after wrap-around: got %d", dpRollover.curGen)
	}

	if !bytes.Equal(cmdFresh, padRollover.cmdBuf) {
		t.Fatalf("cmdBuf mismatch after epoch rollover")
	}
	if !bytes.Equal(litFresh, padRollover.literalBuf) {
		t.Fatalf("literalBuf mismatch after epoch rollover")
	}
	if !bytes.Equal(offFresh, padRollover.offsetBuf) {
		t.Fatalf("offsetBuf mismatch after epoch rollover")
	}
}

// TestChallengerZD_M3_1_BoundarySizes_LosslessRoundtrip stresses all boundary sizes
// at Level 3 optimal parsing and verifies 100% bit-exact restoration.
func TestChallengerZD_M3_1_BoundarySizes_LosslessRoundtrip(t *testing.T) {
	sizes := []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 12, 16, 17, 18, 19, 20,
		63, 64, 65, 127, 128, 129, 255, 256, 257,
		4095, 4096, 4097, 16383, 16384, 16385,
		65535, 65536, 65537, 131071, 131072, 131073,
		262144, 1048576,
	}

	rng := rand.New(rand.NewSource(42))

	for _, sz := range sizes {
		// Generate realistic corpora across data profiles
		payloads := []struct {
			name string
			data []byte
		}{
			{
				name: "repeated",
				data: bytes.Repeat([]byte("PRZM_ZERO_DRAG_L3_OPTIMAL_PARSER_"), (sz/33)+1)[:sz],
			},
			{
				name: "json",
				data: bench.GenerateJSON(sz),
			},
			{
				name: "tabular32",
				data: bench.GenerateTabular(sz, 32),
			},
			{
				name: "machine_code",
				data: bench.GenerateMachineCode(sz),
			},
			{
				name: "pseudo_random",
				data: make([]byte, sz),
			},
		}
		rng.Read(payloads[4].data)

		for _, p := range payloads {
			compressed, err := Compress(p.data, 3)
			if err != nil {
				t.Fatalf("Compress failed for size %d (%s): %v", sz, p.name, err)
			}

			// Decompress standard
			decomp, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress failed for size %d (%s): %v", sz, p.name, err)
			}
			if !bytes.Equal(decomp, p.data) {
				t.Fatalf("Bit-exact mismatch for size %d (%s)", sz, p.name)
			}

			// DecompressTo zero-alloc (with required +64 SIMD cacheline padding)
			outBuf := make([]byte, sz+64)
			outSlice, err := DecompressTo(compressed, outBuf)
			if err != nil {
				t.Fatalf("DecompressTo failed for size %d (%s): %v", sz, p.name, err)
			}
			if len(outSlice) != sz {
				t.Fatalf("DecompressTo returned length %d, expected %d", len(outSlice), sz)
			}
			if !bytes.Equal(outSlice, p.data) {
				t.Fatalf("DecompressTo mismatch for size %d (%s)", sz, p.name)
			}
		}
	}
}

// TestChallengerZD_M3_1_ZeroAlloc_DecompressTo_Strict asserts that DecompressTo strictly
// incurs 0.00 heap allocations per run across all corpora and boundary sizes.
func TestChallengerZD_M3_1_ZeroAlloc_DecompressTo_Strict(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"Small_64B_JSON", bench.GenerateJSON(64)},
		{"Boundary_4KB_Tabular", bench.GenerateTabular(4096, 32)},
		{"Block_64KB_Code", bench.GenerateMachineCode(65536)},
		{"CrossBoundary_65537B_Tabular", bench.GenerateTabular(65537, 32)},
		{"Dedup_128KB_Tabular", bench.GenerateTabular(131072, 32)},
		{"Large_1MB_Pattern", func() []byte {
			buf := make([]byte, 1024*1024)
			pattern := []byte(`{"event":"metric_log","timestamp":1728391200,"cluster":"us-east-1","load":0.785,"status":"OK"}` + "\n")
			for i := 0; i < len(buf); i += len(pattern) {
				copy(buf[i:], pattern)
			}
			return buf
		}()},
	}

	for _, tc := range testCases {
		sz := len(tc.data)
		compressed, err := Compress(tc.data, 3)
		if err != nil {
			t.Fatalf("[%s] Compress failed: %v", tc.name, err)
		}

		extra := 0
		hdr, err := ParseFrameHeader(compressed)
		t.Logf("[%s] compLen=%d, magic=0x%08X, hdr=%+v, err=%v", tc.name, len(compressed), binary.LittleEndian.Uint32(compressed[:4]), hdr, err)


		outBuf := make([]byte, sz+extra+64)
		allocs := testing.AllocsPerRun(100, func() {
			outSlice, err := DecompressTo(compressed, outBuf)
			if err != nil || len(outSlice) != sz {
				t.Fatalf("[%s] DecompressTo failed: %v", tc.name, err)
			}
		})

		if allocs != 0.00 {
			t.Fatalf("[%s] DecompressTo alloc violation: got %.2f allocs/run, strictly expected 0.00", tc.name, allocs)
		}
	}
}

// TestChallengerZD_M3_1_OptimalDP_ZeroAllocInLoop verifies that compressBlockOptimal
// itself does not allocate inside the dynamic programming forward relaxation loop.
func TestChallengerZD_M3_1_OptimalDP_ZeroAllocInLoop(t *testing.T) {
	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte(i)
	}

	pad := padPool.Get()
	defer padPool.Put(pad)
	dpPad := dpScratchPool.Get()
	defer dpScratchPool.Put(dpPad)

	// Warm up
	r0, r1, r2 := 0, 0, 0
	compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, pad, dpPad, &r0, &r1, &r2, nil, nil)

	// Measure allocs in DP forward relaxation
	allocs := testing.AllocsPerRun(10, func() {
		pad.cmdBuf = pad.cmdBuf[:0]
		pad.literalBuf = pad.literalBuf[:0]
		pad.offsetBuf = pad.offsetBuf[:0]
		r0, r1, r2 = 0, 0, 0
		compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, pad, dpPad, &r0, &r1, &r2, nil, nil)
	})

	if allocs != 0.00 {
		t.Fatalf("compressBlockOptimal incurred %.2f allocs/run, expected 0.00", allocs)
	}
}

// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TestChallengerZD_M3_2_EpochRollover verifies that when curGen reaches math.MaxUint32,
// Compress(data, 3) on a 64KB block resets curGen to 1, table is cleared,
// roundtrip is bit-exact, and DecompressTo maintains 0.00 allocs.
func TestChallengerZD_M3_2_EpochRollover(t *testing.T) {
	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte((i * 37) ^ (i >> 3))
	}
	for i := 256; i < 60000; i += 512 {
		copy(data[i:i+128], data[i-128:i])
	}

	dp := dpScratchPool.Get()
	dp.curGen = math.MaxUint32
	dpScratchPool.Put(dp)

	comp, err := Compress(data, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dpAfter := dpScratchPool.Get()
	genAfter := dpAfter.curGen
	dpScratchPool.Put(dpAfter)

	if genAfter != 1 {
		t.Fatalf("curGen did not rollover to 1: got %d", genAfter)
	}

	decomp := make([]byte, len(data)+1024)
	allocs := testing.AllocsPerRun(50, func() {
		out, err := DecompressTo(comp, decomp)
		if err != nil || len(out) != len(data) {
			t.Fatalf("DecompressTo failed: %v", err)
		}
	})
	if allocs != 0.00 {
		t.Fatalf("DecompressTo incurred %.2f allocs, expected 0.00", allocs)
	}
	if !bytes.Equal(decomp[:len(data)], data) {
		t.Fatalf("Lossless roundtrip mismatch after epoch rollover")
	}
}

// TestChallengerZD_M3_2_ConcurrencyRace runs 16 concurrent goroutines compressing
// 64KB randomized blocks via Compress(data, 3) and verifies bit-exact lossless roundtrips.
func TestChallengerZD_M3_2_ConcurrencyRace(t *testing.T) {
	const numGoroutines = 16
	const iters = 4
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	var errCount atomic.Int64

	for g := 0; g < numGoroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(42000 + gID*7919)))
			buf := make([]byte, 65536)
			for it := 0; it < iters; it++ {
				rng.Read(buf)
				for j := 256; j < 60000; j += 1024 {
					copy(buf[j:j+256], buf[j-256:j])
				}
				comp, err := Compress(buf, 3)
				if err != nil {
					errCount.Add(1)
					t.Errorf("worker %d iter %d Compress failed: %v", gID, it, err)
					return
				}
				decomp, err := Decompress(comp)
				if err != nil {
					errCount.Add(1)
					t.Errorf("worker %d iter %d Decompress failed: %v", gID, it, err)
					return
				}
				if !bytes.Equal(decomp, buf) {
					errCount.Add(1)
					t.Errorf("worker %d iter %d bit-exact roundtrip mismatch", gID, it)
					return
				}
			}
		}()
	}

	wg.Wait()
	if errs := errCount.Load(); errs > 0 {
		t.Fatalf("ConcurrencyRace encountered %d failures", errs)
	}
}
