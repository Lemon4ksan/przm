// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package adversarial_test

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lemon4ksan/przm"
)

// Helper: locate test_przm_dec.exe
func locateCBinary(t *testing.T) string {
	candidates := []string{
		"../../test_przm_dec.exe",
		"../test_przm_dec.exe",
		"test_przm_dec.exe",
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Skip("test_przm_dec.exe not found; skipping C-dependent tests")
	return ""
}

// -----------------------------------------------------------------------------
// 1. CORRUPTED HEADERS & TRUNCATED FRAMES FUZZING (ALL MAGIC FAMILIES)
// -----------------------------------------------------------------------------

func TestSentinel_CorruptedHeadersAndTruncatedFrames(t *testing.T) {
	magics := []struct {
		name      string
		magic     uint32
		headerLen int
	}{
		{"Standard_PRZM", przm.MagicPRZM, przm.HeaderSize},
		{"Stream_PRZS", przm.MagicPRZS, przm.StreamHeaderSize},
		{"Archival_PNG", przm.ArchivalPNGMagic, 12},
		{"Mobius", przm.MobiusMagic, 12},
		{"Margolus", przm.MargolusMagic, 16},
		{"Symplectic", przm.SymplecticMagic, 12},
		{"Mahler", przm.MahlerMagic, 12},
		{"Sheaf", przm.SheafMagic, 12},
		{"IsoQuant", przm.IsoQuantMagic, 20},
		{"GCTR", przm.GCTRMagic, 10},
		{"Heisenberg", przm.HeisenbergMagic, 12},
		{"CellularSheaf", przm.CellularSheafMagic, 24},
		{"Padic", przm.PadicMagic, 12},
		{"Hodge", przm.HodgeMagic, przm.HodgeHeaderSize},
		{"HMDS", przm.HMDSMagic, 9},
		{"Spectral", przm.SpectralMagic, 9},
	}

	for _, m := range magics {
		t.Run(m.name+"_Truncation", func(t *testing.T) {
			base := make([]byte, m.headerLen+32)
			binary.LittleEndian.PutUint32(base[0:4], m.magic)
			// Truncate from 0 bytes up to headerLen-1
			for l := 0; l < m.headerLen; l++ {
				trunc := base[:l]
				_, err := przm.Decompress(trunc)
				if err == nil && l > 0 {
					t.Fatalf("%s: expected decompression error on truncated %d-byte frame", m.name, l)
				}
				_, errSz := przm.GetUncompressedSize(trunc)
				if errSz == nil {
					t.Fatalf("%s: expected size error on truncated %d-byte frame", m.name, l)
				}
			}
		})
	}

	// Versioned 3-byte ISA signatures (PWS, VM, GGTC)
	isaSignatures := []struct {
		name string
		sig  [3]byte
	}{
		{"PWS", [3]byte{'P', 'W', 0x01}},
		{"VM", [3]byte{'P', 'V', 0x01}},
		{"GGTC", [3]byte{'G', 'G', 0x01}},
	}
	for _, s := range isaSignatures {
		t.Run(s.name+"_Truncation", func(t *testing.T) {
			frame := make([]byte, 24)
			copy(frame[:3], s.sig[:])
			for l := 0; l < 8; l++ {
				_, err := przm.GetUncompressedSize(frame[:l])
				if err == nil {
					t.Fatalf("%s: expected error on truncated %d-byte ISA header", s.name, l)
				}
			}
		})
	}

	// Pseudo-random corruption fuzzing
	rnd := rand.New(rand.NewSource(1337))
	for trial := 0; trial < 1000; trial++ {
		noiseLen := rnd.Intn(256)
		noise := make([]byte, noiseLen)
		_, _ = crand.Read(noise)

		// Never panic
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decompress panicked on random %d bytes: %v", noiseLen, r)
				}
			}()
			_, _ = przm.Decompress(noise)
			_, _ = przm.GetUncompressedSize(noise)
		}()
	}
}

// -----------------------------------------------------------------------------
// 2. OUT-OF-BOUNDS MATCH OFFSETS & FUSED REPEAT REGRESSION TEST
// -----------------------------------------------------------------------------

func TestSentinel_OutOfBoundsMatchOffsets_Regression(t *testing.T) {
	// Construct a standard PRZM frame manually
	buildFrame := func(uSize int, cmds, offs, lits []byte) []byte {
		sec2 := make([]byte, 5+len(cmds))
		sec2[0] = przm.ModeFlat
		binary.LittleEndian.PutUint32(sec2[1:5], uint32(len(cmds)))
		copy(sec2[5:], cmds)

		sec3 := make([]byte, 5+len(offs))
		sec3[0] = przm.ModeFlat
		binary.LittleEndian.PutUint32(sec3[1:5], uint32(len(offs)))
		copy(sec3[5:], offs)

		secDirSize := 4
		sec1Size := 8
		total := przm.HeaderSize + secDirSize + sec1Size + len(sec2) + len(sec3) + len(lits)
		frame := make([]byte, total)
		binary.LittleEndian.PutUint32(frame[0:4], przm.MagicPRZM)
		frame[4] = 0 // Flags
		frame[5] = byte(uSize)
		frame[6] = byte(uSize >> 8)
		frame[7] = byte(uSize >> 16)

		w := przm.HeaderSize
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

	t.Run("decodeFusedRepeat_LiteralOverflow_Regression", func(t *testing.T) {
		// Regression test for the discovered vulnerability in decodeFusedRepeat:
		// When dstPos + litLen > len(outBuf), decodeFusedRepeat previously caused a slice bounds panic.
		uSize := 10
		// Command 1: 0xF9 -> direct literal run of 7 bytes (dstPos becomes 7)
		// Command 2: 0x70 -> Fused repeat token: litLen=7, mLen=4, k=0 (dstPos+litLen = 14 > 10)
		cmds := []byte{0xF9, 0x70}
		lits := bytes.Repeat([]byte{'A'}, 14)
		frame := buildFrame(uSize, cmds, nil, lits)

		_, err := przm.Decompress(frame)
		if err == nil {
			t.Fatal("expected ErrCorruptedFrame on literal overflow in decodeFusedRepeat, got nil")
		}
		if !errors.Is(err, przm.ErrCorruptedFrame) {
			t.Fatalf("expected ErrCorruptedFrame, got: %v", err)
		}
	})

	t.Run("RepeatMatch_InitialZeroPos_OutOfBounds", func(t *testing.T) {
		// Command 0x00: Fused repeat match (k=0, rep0=1) at dstPos=0
		cmds := []byte{0x00}
		frame := buildFrame(32, cmds, nil, nil)
		_, err := przm.Decompress(frame)
		if err == nil {
			t.Fatal("expected ErrCorruptedFrame or ErrOffsetOutOfBounds at pos=0 match")
		}
	})

	t.Run("ExtendedMatch_OOB_ExplicitOffset", func(t *testing.T) {
		// Command 0x80: Fused match with litLen=0, baseLen=0 -> mLen=4, read offset from offs
		// Offset stream has varint 999999 (OOB)
		cmds := []byte{0x80}
		offs := []byte{0xBF, 0x84, 0x3D} // 999999 in varint
		frame := buildFrame(64, cmds, offs, nil)
		_, err := przm.Decompress(frame)
		if err == nil {
			t.Fatal("expected error on out-of-bounds explicit match distance")
		}
	})

	t.Run("StrideMatch_OOB", func(t *testing.T) {
		// Command 0xD0: Stride match (m=1), stride=1, but at dstPos=0 -> dist=1 > dstPos=0
		cmds := []byte{0xD0, 0x00}
		frame := buildFrame(64, cmds, nil, nil)
		_, err := przm.Decompress(frame)
		if err == nil {
			t.Fatal("expected error on stride match at dstPos=0")
		}
	})
}

// -----------------------------------------------------------------------------
// 3. MALFORMED UVARINTS FUZZING
// -----------------------------------------------------------------------------

func TestSentinel_MalformedUvarints(t *testing.T) {
	buildFrameWithVarintInOffs := func(uSize int, rawVarint []byte) []byte {
		// Command 0x80 (fused match) reads offset from offs
		cmds := []byte{0x80}
		sec2 := make([]byte, 5+len(cmds))
		sec2[0] = przm.ModeFlat
		binary.LittleEndian.PutUint32(sec2[1:5], uint32(len(cmds)))
		copy(sec2[5:], cmds)

		sec3 := make([]byte, 5+len(rawVarint))
		sec3[0] = przm.ModeFlat
		binary.LittleEndian.PutUint32(sec3[1:5], uint32(len(rawVarint)))
		copy(sec3[5:], rawVarint)

		total := przm.HeaderSize + 4 + 8 + len(sec2) + len(sec3)
		frame := make([]byte, total)
		binary.LittleEndian.PutUint32(frame[0:4], przm.MagicPRZM)
		frame[5] = byte(uSize)
		frame[6] = byte(uSize >> 8)
		frame[7] = byte(uSize >> 16)

		w := przm.HeaderSize
		binary.LittleEndian.PutUint16(frame[w:w+2], uint16(len(sec2)))
		binary.LittleEndian.PutUint16(frame[w+2:w+4], uint16(len(sec3)))
		w += 4
		w += 8
		copy(frame[w:], sec2)
		w += len(sec2)
		copy(frame[w:], sec3)
		return frame
	}

	testCases := []struct {
		name   string
		varint []byte
	}{
		{"10_bytes_all_high_bits", bytes.Repeat([]byte{0x80}, 10)},
		{"MaxUint64_varint", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}},
		{"Negative_cast_varint", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F}},
		{"ExceedsMaxBlock_4B", []byte{0xFF, 0xFF, 0xFF, 0x7F}}, // 268,435,455 > 16 MiB
		{"Incomplete_varint_truncated", []byte{0x80}},
		{"Incomplete_3B_varint", []byte{0x80, 0x80, 0x80}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			frame := buildFrameWithVarintInOffs(64, tc.varint)
			_, err := przm.Decompress(frame)
			if err == nil {
				t.Fatalf("%s: expected ErrCorruptedFrame on malformed varint", tc.name)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 4. ZERO-LENGTH LITERALS & NORMALIZATION LIMITS
// -----------------------------------------------------------------------------

func TestSentinel_ZeroLengthLiteralsAndNormLimits(t *testing.T) {
	t.Run("NibblePlanes_ZeroLiterals", func(t *testing.T) {
		// Frame with FlagNibble (0x80), Section 4 having numLits=0, lowBytes=0
		frame := make([]byte, 36)
		binary.LittleEndian.PutUint32(frame[0:4], przm.MagicPRZM)
		frame[4] = przm.FlagNibble
		frame[5] = 0 // uSize = 0

		// Section directory: cmd=0, off=0
		binary.LittleEndian.PutUint16(frame[8:10], 0)
		binary.LittleEndian.PutUint16(frame[10:12], 0)
		// Section 1: 8 zeroes
		// Section 4 starts at 20
		binary.LittleEndian.PutUint32(frame[20:24], 0) // numLits = 0
		binary.LittleEndian.PutUint32(frame[24:28], 0) // lowBytes = 0

		// Must return empty or error without panic
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Nibble planes panicked on numLits=0: %v", r)
				}
			}()
			_, _ = przm.Decompress(frame)
		}()
	})

	t.Run("Normalization_ContractedLen_ExceedsMaxBlock", func(t *testing.T) {
		// Frame with FlagNorm, Section 1 claiming contractedLen > MaxBlockSize
		frame := make([]byte, 64)
		binary.LittleEndian.PutUint32(frame[0:4], przm.MagicPRZM)
		frame[4] = przm.FlagNorm
		frame[5] = 100 // uSize = 100

		// Section directory: cmd=0, off=0
		binary.LittleEndian.PutUint16(frame[8:10], 0)
		binary.LittleEndian.PutUint16(frame[10:12], 0)

		// Section 1 at offset 12:
		frame[14] = przm.NormModeHex // NormMode
		// Write contractedLen = 32 MiB (> 16 MiB MaxBlockSize)
		frame[15] = 0x00
		frame[16] = 0x00
		frame[17] = 0x00
		frame[18] = 0x02 // 2 << 24 = 33,554,432 bytes = 32 MiB

		_, err := przm.Decompress(frame)
		if err == nil {
			t.Fatal("expected error when contractedLen exceeds MaxBlockSize")
		}
		if !errors.Is(err, przm.ErrExceedsMaxBlock) && !errors.Is(err, przm.ErrCorruptedFrame) {
			t.Fatalf("expected ErrExceedsMaxBlock or ErrCorruptedFrame, got: %v", err)
		}
	})
}

// -----------------------------------------------------------------------------
// 5. HIGH-LOAD CONCURRENT DECOMPRESSION & MEMORY POOL SAFETY
// -----------------------------------------------------------------------------

func TestSentinel_ConcurrentDecompression_MemoryPoolSafety(t *testing.T) {
	const numWorkers = 16
	const roundsPerWorker = 30

	// Generate a varied corpus of valid compressed frames across all transforms
	testPayloads := [][]byte{
		bytes.Repeat([]byte("Hello, PRZM Invariant Sentinel! "), 64),
		bytes.Repeat([]byte{0x00, 0xFF, 0x55, 0xAA, 0x12, 0x34}, 128),
		[]byte("Short ASCII sentence for dictionary token testing."),
		make([]byte, 1024), // all zeroes
	}

	// Add random noise payloads
	rnd := rand.New(rand.NewSource(999))
	for i := 0; i < 4; i++ {
		p := make([]byte, 2048)
		_, _ = rnd.Read(p)
		testPayloads = append(testPayloads, p)
	}

	var validFrames [][]byte
	for _, p := range testPayloads {
		compressed, err := przm.Compress(p, 1)
		if err == nil {
			validFrames = append(validFrames, compressed)
		}
	}

	// Generate corrupt frames designed to stress error handling
	var corruptFrames [][]byte
	for _, vf := range validFrames {
		// Truncated
		corruptFrames = append(corruptFrames, vf[:len(vf)/2])
		// Mutated magic
		mut := append([]byte(nil), vf...)
		mut[0] ^= 0xFF
		corruptFrames = append(corruptFrames, mut)
		// Corrupted payload
		if len(vf) > 16 {
			mutPayload := append([]byte(nil), vf...)
			mutPayload[15] ^= 0x7F
			corruptFrames = append(corruptFrames, mutPayload)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers*roundsPerWorker)

	for workerID := 0; workerID < numWorkers; workerID++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			wRnd := rand.New(rand.NewSource(int64(wid*1000 + 7)))

			for r := 0; r < roundsPerWorker; r++ {
				// 1. Decompress valid frame and check correctness
				vIdx := wRnd.Intn(len(validFrames))
				expectedPayload := testPayloads[vIdx%len(testPayloads)]
				decompressed, err := przm.Decompress(validFrames[vIdx])
				if err != nil {
					errCh <- fmt.Errorf("worker %d round %d: valid frame decompression failed: %w", wid, r, err)
					return
				}
				if !bytes.Equal(decompressed, expectedPayload) {
					errCh <- fmt.Errorf("worker %d round %d: decompressed content mismatch", wid, r)
					return
				}

				// 2. Decompress corrupt frame and verify it fails gracefully without panic
				cIdx := wRnd.Intn(len(corruptFrames))
				_, _ = przm.Decompress(corruptFrames[cIdx])

				// 3. Compress & decompress on the fly
				dynPayload := make([]byte, wRnd.Intn(4096)+64)
				_, _ = wRnd.Read(dynPayload)
				comp, err := przm.Compress(dynPayload, 1)
				if err == nil {
					dec, err := przm.Decompress(comp)
					if err != nil || !bytes.Equal(dec, dynPayload) {
						errCh <- fmt.Errorf("worker %d round %d: dynamic roundtrip mismatch: %v", wid, r, err)
						return
					}
				}
			}
		}(workerID)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// 6. PURE C99 MEMORY SAFETY: FUZZ test_przm_dec.exe ON CORRUPT BITSTREAMS
// -----------------------------------------------------------------------------

func TestSentinel_PureC99MemorySafety_FuzzCLI(t *testing.T) {
	cBin := locateCBinary(t)
	tmpDir := t.TempDir()

	inPath := filepath.Join(tmpDir, "fuzz_in.przm")
	outPath := filepath.Join(tmpDir, "fuzz_out.bin")

	// Generate 100 adversarial test cases for C executable CLI mode
	rnd := rand.New(rand.NewSource(4242))

	testInputs := [][]byte{
		{},                          // 0 bytes
		{0x00},                      // 1 byte
		{'P', 'R', 'Z'},             // 3 bytes
		{'P', 'R', 'Z', 'M'},        // 4 bytes
		{'P', 'R', 'Z', 'S'},        // 4 bytes stream
		bytes.Repeat([]byte{0}, 32), // all zeros
	}

	// Add mutated PRZM frames with invalid sizes
	hugePRZM := make([]byte, 16)
	binary.LittleEndian.PutUint32(hugePRZM[0:4], przm.MagicPRZM)
	// uSize = 32 MiB (exceeds 16 MiB)
	hugePRZM[5] = 0x00
	hugePRZM[6] = 0x00
	hugePRZM[7] = 0x02
	testInputs = append(testInputs, hugePRZM)

	// Add random noise streams
	for i := 0; i < 50; i++ {
		b := make([]byte, rnd.Intn(128)+1)
		_, _ = rnd.Read(b)
		testInputs = append(testInputs, b)
	}

	for i, inp := range testInputs {
		if err := os.WriteFile(inPath, inp, 0o600); err != nil {
			t.Fatalf("failed to write fuzz input: %v", err)
		}
		_ = os.Remove(outPath)

		cmd := exec.Command(cBin, inPath, outPath)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf
		err := cmd.Run()

		// The command is expected to exit with non-zero code on corrupt input,
		// but MUST NEVER crash with access violation / segmentation fault (code 0xC0000005 or -11).
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode := exitErr.ExitCode()
			// On Windows, segfaults/crashes have exit code 0xC0000005 (signed -1073741819)
			if exitCode == -1073741819 || exitCode == 139 || exitCode < 0 {
				t.Fatalf("test_przm_dec.exe crashed with exit code %d (0x%X) on test case %d (%d bytes):\n%s",
					exitCode, uint32(exitCode), i, len(inp), outBuf.String())
			}
		}
	}
}
