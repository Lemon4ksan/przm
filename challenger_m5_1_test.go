// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
)

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
	binary.Write(&f, binary.LittleEndian, przm.MagicPRZM)
	f.WriteByte(przm.FlagExtOffset)
	f.WriteByte(byte(uSize))
	f.WriteByte(byte(uSize >> 8))
	f.WriteByte(byte(uSize >> 16))

	pCmd, _ := przm.PackStream(cmdBytes, przm.ModeFlat)
	pOff, _ := przm.PackStream([]byte{}, przm.ModeFlat)
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
	// Go przm.Decompress (0 panics, must return ErrCorruptedFrame)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("[%s] Decompress panicked: %v", name, r)
			}
		}()
		_, err := przm.Decompress(frame)
		if err == nil || !errors.Is(err, przm.ErrCorruptedFrame) {
			t.Fatalf("[%s] Decompress expected ErrCorruptedFrame, got %v", name, err)
		}
	}()

	// Go przm.DecompressTo (0 panics, must return ErrCorruptedFrame)
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("[%s] DecompressTo panicked: %v", name, r)
			}
		}()
		dst := make([]byte, 1024*1024)
		_, err := przm.DecompressTo(frame, dst)
		if err == nil || !errors.Is(err, przm.ErrCorruptedFrame) {
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
		{"Trunc_0B", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock)},
		{"Trunc_1B", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x00)},
		{"Trunc_2B", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x00, 0x00)},
		{"Trunc_3B", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x00, 0x00, 0x00)},

		// 2. Forward block references (srcBlockIdx >= currentBlockIndex)
		{"Fwd_Ref0_AtStart", 131072, []byte{przm.OpCopyBlock, 0x00, 0x00, 0x00, 0x00}},
		{"Fwd_Ref1_AtBlk1", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x01, 0x00, 0x00, 0x00)},
		{"Fwd_Ref2_AtBlk1", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x02, 0x00, 0x00, 0x00)},

		// 3. OOB / Giant block indices
		{"OOB_256", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x00, 0x01, 0x00, 0x00)},
		{"OOB_0xFFFFFFFF", 131072, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0xFF, 0xFF, 0xFF, 0xFF)},

		// 4. Buffer overflow attempt (valid block 0, but exceeds uSize/buffer capacity)
		{"BufOverflow_ExceedBy1B", 131072, append(append([]byte{}, rle64K...), 0xF3, przm.OpCopyBlock, 0x00, 0x00, 0x00, 0x00)},
		{"BufOverflow_ExceedBy31K", 100000, append(append([]byte{}, rle64K...), przm.OpCopyBlock, 0x00, 0x00, 0x00, 0x00)},
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
		b0, b1 := make([]byte, przm.DedupBlockSize), make([]byte, przm.DedupBlockSize)
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
		base := make([]byte, przm.DedupBlockSize)
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
			bA := make([]byte, przm.DedupBlockSize)
			bB := make([]byte, przm.DedupBlockSize)
			for i := range przm.DedupBlockSize {
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
		comp, err := przm.Compress(raw, lvl)
		if err != nil {
			t.Fatalf("Compress lvl %d failed: %v", lvl, err)
		}

		// Go Decompress
		dec, err := przm.Decompress(comp)
		if err != nil || !bytes.Equal(dec, raw) {
			t.Fatalf("Go Decompress mismatch or error: %v", err)
		}

		// Go DecompressTo
		dst := make([]byte, len(raw)+64)
		decTo, err := przm.DecompressTo(comp, dst)
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
