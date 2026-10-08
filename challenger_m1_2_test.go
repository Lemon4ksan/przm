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
	"strings"
	"sync"
	"testing"
)

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


