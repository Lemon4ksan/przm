// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/lemon4ksan/przm/core/commands"
)

// verifyRoundtrip compresses src, decompresses into dst, and checks bit-exact equivalence.
func verifyRoundtrip(t *testing.T, src []byte, name string) {
	t.Helper()
	comp, err := Compress(src, 6)
	if err != nil {
		t.Fatalf("%s: Compress error: %v", name, err)
	}

	dst := make([]byte, len(src)+128)
	n, err := Decompress(comp, dst)
	if err != nil {
		t.Logf("comp len: %d, hdr: %x", len(comp), comp[:min(len(comp), 32)])
		t.Fatalf("%s: Decompress error: %v", name, err)
	}
	if n != len(src) {
		t.Fatalf("%s: decompressed len mismatch: got %d, want %d", name, n, len(src))
	}

	srcHash := sha256.Sum256(src)
	dstHash := sha256.Sum256(dst[:n])
	if srcHash != dstHash {
		t.Fatalf("%s: SHA-256 mismatch!\nsrc: %x\ndst: %x", name, srcHash, dstHash)
	}
}

func TestCompressEmptyAndShort(t *testing.T) {
	verifyRoundtrip(t, []byte{}, "empty")
	verifyRoundtrip(t, []byte{42}, "single-byte")
	verifyRoundtrip(t, []byte("short string"), "short")
	verifyRoundtrip(t, []byte("exactly 16 bytes"), "16-bytes")
}

func TestCompressRepetitive(t *testing.T) {
	pattern := []byte("The quick brown fox jumps over the lazy dog. ")
	src := bytes.Repeat(pattern, 100)
	verifyRoundtrip(t, src, "fox-repeat-100")

	singleByte := bytes.Repeat([]byte{0x7F}, 4096)
	verifyRoundtrip(t, singleByte, "single-byte-4096")
}

func TestCompressStrideTabular(t *testing.T) {
	// 16-byte fixed struct rows with recurring column values
	record := []byte{0x01, 0x00, 0x00, 0x00, 0xAA, 0xBB, 0xCC, 0xDD, 0x10, 0x20, 0x30, 0x40, 0xFE, 0xED, 0xBE, 0xEF}
	src := make([]byte, 16*300)
	for i := range 300 {
		copy(src[i*16:], record)
		src[i*16] = byte(i & 0xFF) // slight variation in ID
	}
	verifyRoundtrip(t, src, "stride-tabular-300-rows")
}

func TestCompressHighEntropyFallback(t *testing.T) {
	r := rand.New(rand.NewSource(12345))
	src := make([]byte, 1024)
	r.Read(src)
	verifyRoundtrip(t, src, "random-high-entropy-1024")
}

func TestCompressMultiBlock(t *testing.T) {
	// 130 KiB buffer spanning multiple 64 KiB blocks
	chunk := bytes.Repeat(
		[]byte("AlphaBetaGammaDeltaEpsilonZetaEtaThetaIotaKappaLambdaMuNuXiOmicronPiRhoSigmaTau"),
		500,
	)
	src := bytes.Repeat(chunk, 4) // ~160 KiB
	verifyRoundtrip(t, src, "multiblock-160k")
}

func TestCompressZeroAllocationsDecompress(t *testing.T) {
	pattern := []byte("Benchmark zero allocation decompression stream text with repetition.")
	src := bytes.Repeat(pattern, 50)
	comp, err := Compress(src, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(src)+128)
	// Warmup
	if _, err := Decompress(comp, dst); err != nil {
		t.Fatalf("Decompress warmup failed: %v", err)
	}

	allocs := testing.AllocsPerRun(10, func() {
		_, _ = Decompress(comp, dst)
	})

	if allocs > 0.00 {
		t.Fatalf("expected 0.00 allocs/op during decompression, got %f", allocs)
	}
}

func BenchmarkCompress(b *testing.B) {
	pattern := []byte(
		"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. ",
	)
	src := bytes.Repeat(pattern, 100) // ~12 KiB

	b.SetBytes(int64(len(src)))

	for b.Loop() {
		_, err := Compress(src, 6)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompressRoundtrip(b *testing.B) {
	pattern := []byte(
		"Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. ",
	)
	src := bytes.Repeat(pattern, 100) // ~12 KiB
	comp, err := Compress(src, 6)
	if err != nil {
		b.Fatal(err)
	}

	dst := make([]byte, len(src)+128)
	b.SetBytes(int64(len(src)))

	for b.Loop() {
		_, err := Decompress(comp, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestExtOffsetSectionsEndToEnd(t *testing.T) {
	s1 := []byte{0x00, 16, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	// 1. Section 2 exceeding 65535 bytes (70,001 bytes)
	t.Run("LargeSection2", func(t *testing.T) {
		const litCount = 70000
		s2 := make([]byte, 1+litCount)
		s2[0] = 0x09 // Mode 0x09 flat commands
		for i := 1; i <= litCount; i++ {
			s2[i] = 0xF3 // 1 literal from section 4
		}
		s4 := make([]byte, litCount)
		for i := range s4 {
			s4[i] = byte((i*31 + 7) & 0xFF)
		}

		params := AnalysisParams{stride: 1, wLog: 16, bMin: 4}
		frame := assembleCompressedFrame(litCount, params, s1, s2, nil, s4, 0)

		if frame[4]&FlagExtOffset == 0 {
			t.Fatalf("expected FlagExtOffset (0x08) to be set, got flags 0x%02X", frame[4])
		}

		// Verify 24-bit section directory offsets
		l2 := int(frame[8]) | (int(frame[9]) << 8) | (int(frame[10]) << 16)
		l3 := int(frame[11]) | (int(frame[12]) << 8) | (int(frame[13]) << 16)
		if l2 != len(s2) {
			t.Fatalf("expected l2 = %d, got %d", len(s2), l2)
		}
		if l3 != 0 {
			t.Fatalf("expected l3 = 0, got %d", l3)
		}

		dst := make([]byte, litCount+128)
		n, err := Decompress(frame, dst)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if n != litCount {
			t.Fatalf("decompressed len mismatch: got %d, want %d", n, litCount)
		}
		if !bytes.Equal(dst[:n], s4) {
			t.Fatal("decompressed content mismatch for large section 2")
		}

		// Verify zero allocations on decompression
		allocs := testing.AllocsPerRun(10, func() {
			_, _ = Decompress(frame, dst)
		})
		if allocs > 0.00 {
			t.Fatalf("expected 0.00 allocs/op during decompression, got %f", allocs)
		}
	})

	// 2. Section 3 exceeding 65535 bytes (70,001 bytes)
	t.Run("LargeSection3", func(t *testing.T) {
		const matchCount = 70000
		s3 := make([]byte, 1+matchCount)
		s3[0] = 0x09 // Mode 0x09 flat offsets
		for i := 1; i <= matchCount; i++ {
			s3[i] = 1 // LEB128 distance 1
		}

		// Section 2: 1 literal 'Z' followed by 70,000 matches of length 4 from Section 3 (opcode 0x80)
		s2 := make([]byte, 2+matchCount)
		s2[0] = 0x09
		s2[1] = 0xF3 // 1 literal
		for i := 2; i < len(s2); i++ {
			s2[i] = 0x80 // llit=0, base=0 -> lmatch=4, explicit offset from s3
		}
		s4 := []byte{'Z'}
		uSize := 1 + matchCount*4

		params := AnalysisParams{stride: 1, wLog: 16, bMin: 4}
		frame := assembleCompressedFrame(uSize, params, s1, s2, s3, s4, 0)

		if frame[4]&FlagExtOffset == 0 {
			t.Fatalf("expected FlagExtOffset (0x08) to be set, got flags 0x%02X", frame[4])
		}

		l2 := int(frame[8]) | (int(frame[9]) << 8) | (int(frame[10]) << 16)
		l3 := int(frame[11]) | (int(frame[12]) << 8) | (int(frame[13]) << 16)
		if l2 != len(s2) {
			t.Fatalf("expected l2 = %d, got %d", len(s2), l2)
		}
		if l3 != len(s3) {
			t.Fatalf("expected l3 = %d, got %d", len(s3), l3)
		}

		dst := make([]byte, uSize+128)
		n, err := Decompress(frame, dst)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if n != uSize {
			t.Fatalf("decompressed len mismatch: got %d, want %d", n, uSize)
		}
		for i := range n {
			if dst[i] != 'Z' {
				t.Fatalf("byte %d mismatch: got %d, want %d", i, dst[i], 'Z')
			}
		}

		allocs := testing.AllocsPerRun(10, func() {
			_, _ = Decompress(frame, dst)
		})
		if allocs > 0.00 {
			t.Fatalf("expected 0.00 allocs/op during decompression, got %f", allocs)
		}
	})

	// 3. FlagExtOffset with Stride > 1
	t.Run("LargeSectionWithStride", func(t *testing.T) {
		const litCount = 70000
		s2 := make([]byte, 1+litCount)
		s2[0] = 0x09
		for i := 1; i <= litCount; i++ {
			s2[i] = 0xF3
		}
		s4 := make([]byte, litCount)
		for i := range s4 {
			s4[i] = byte(i & 0xFF)
		}

		params := AnalysisParams{stride: 4, wLog: 16, bMin: 4}
		frame := assembleCompressedFrame(litCount, params, s1, s2, nil, s4, 0)

		if frame[4]&FlagExtOffset == 0 || frame[4]&FlagStride == 0 {
			t.Fatalf("expected FlagExtOffset | FlagStride, got 0x%02X", frame[4])
		}
		// Stride byte is at frame[8]
		if frame[8] != 3 { // stride - 1 = 3
			t.Fatalf("expected stride byte 3, got %d", frame[8])
		}
		// Directory starts at frame[9]
		l2 := int(frame[9]) | (int(frame[10]) << 8) | (int(frame[11]) << 16)
		if l2 != len(s2) {
			t.Fatalf("expected l2 = %d, got %d", len(s2), l2)
		}

		dst := make([]byte, litCount+128)
		n, err := Decompress(frame, dst)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if n != litCount || !bytes.Equal(dst[:n], s4) {
			t.Fatal("decompressed content mismatch for large section with stride")
		}
	})
}

func TestCompressRFCStaticDictionaryMatchesAndParity(t *testing.T) {
	// Sample text heavily containing RFC 7932 dictionary words
	src := []byte("constant information technology return function system declaration variable description " +
		"http://schemas.xmlsoap.org/soap/envelope/ " +
		"<html><body><div class=\"container\">hello world</div></body></html>")

	comp, err := Compress(src, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Unpack commands and verify 0xCF is present
	hdr, err := ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("ParseFrameHeader failed: %v", err)
	}
	s2 := comp[hdr.DataOffset : hdr.DataOffset+hdr.CmdStreamSize]
	var op, meta, vBuf, unpacked [4096]byte
	rawCmds, err := commands.UnpackSection2Commands(s2, op[:], meta[:], vBuf[:], unpacked[:])
	if err != nil {
		t.Fatalf("UnpackSection2Commands failed: %v", err)
	}

	has0xCF := slices.Contains(rawCmds, 0xCF)
	if !has0xCF {
		t.Fatalf("expected compressed frame to contain opcode 0xCF, got commands: %x", rawCmds)
	}
	t.Logf("Verified opcode 0xCF is present in compressed commands (%d bytes of commands)", len(rawCmds))

	// Verify Go DecompressTo produces 100% losslessly
	dst := make([]byte, len(src)+64)
	decomp, err := DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}
	if !bytes.Equal(decomp, src) {
		t.Fatalf("DecompressTo mismatch: got %q, want %q", decomp, src)
	}

	// Verify zero allocations in DecompressTo
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = DecompressTo(comp, dst)
	})
	if allocs != 0.00 {
		t.Fatalf("DecompressTo allocated %f allocs/op (strictly 0.00 required!)", allocs)
	}

	// Verify C99 decoder parity
	if _, err := os.Stat("test_przm_dec.exe"); err == nil {
		tmpIn := t.TempDir() + "/comp_dict.przm"
		tmpOut := t.TempDir() + "/out_dict.raw"
		if err := os.WriteFile(tmpIn, comp, 0o644); err != nil {
			t.Fatalf("failed to write temp comp file: %v", err)
		}
		cmd := exec.Command("./test_przm_dec.exe", "--decompress", tmpIn, tmpOut)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("C99 test_przm_dec.exe failed: %v, output: %s", err, string(out))
		}
		cDec, err := os.ReadFile(tmpOut)
		if err != nil {
			t.Fatalf("failed to read C99 output file: %v", err)
		}
		if !bytes.Equal(cDec, src) {
			t.Fatalf("C99 decompressor mismatch: got %q, want %q", cDec, src)
		}
		t.Logf("Verified C99 decoder decompressed dictionary-matched frame with 100%% bit-exact parity!")
	}
}

func TestCompressDiffLatticeMatchesAndParity(t *testing.T) {
	// Periodic stride pattern with correlated offset deltas
	row := []byte("ABCDEFGH12345678")
	var src []byte
	for i := range 50 {
		src = append(src, row...)
		if i%3 == 0 {
			src = append(src, "ABCDEF"...)
		}
	}

	comp, err := Compress(src, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(src)+64)
	decomp, err := DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}
	if !bytes.Equal(decomp, src) {
		t.Fatalf("DecompressTo mismatch: got %d bytes, want %d", len(decomp), len(src))
	}

	// Verify zero allocations in DecompressTo
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = DecompressTo(comp, dst)
	})
	if allocs != 0.00 {
		t.Fatalf("DecompressTo allocated %f allocs/op (strictly 0.00 required!)", allocs)
	}

	// Verify C99 decoder parity
	if _, err := os.Stat("test_przm_dec.exe"); err == nil {
		tmpIn := t.TempDir() + "/comp_diff.przm"
		tmpOut := t.TempDir() + "/out_diff.raw"
		if err := os.WriteFile(tmpIn, comp, 0o644); err != nil {
			t.Fatalf("failed to write temp comp file: %v", err)
		}
		cmd := exec.Command("./test_przm_dec.exe", "--decompress", tmpIn, tmpOut)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("C99 test_przm_dec.exe failed: %v, output: %s", err, string(out))
		}
		cDec, err := os.ReadFile(tmpOut)
		if err != nil {
			t.Fatalf("failed to read C99 output file: %v", err)
		}
		if !bytes.Equal(cDec, src) {
			t.Fatalf("C99 decompressor mismatch")
		}
		t.Logf("Verified C99 decoder decompressed frame with 100%% bit-exact parity!")
	}
}
