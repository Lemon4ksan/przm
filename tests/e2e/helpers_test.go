// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
)

// assertRoundtrip compresses and decompresses src using przm public APIs,
// verifying bit-exact identity and graceful handling.
func assertRoundtrip(t *testing.T, src []byte, level int) []byte {
	t.Helper()
	compressed, err := przm.Compress(src, level)
	if err != nil {
		t.Fatalf("przm.Compress(level=%d, len=%d) failed: %v", level, len(src), err)
	}

	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("przm.Decompress(compressed_len=%d) failed: %v", len(compressed), err)
	}

	if !bytes.Equal(decompressed, src) {
		for i := 0; i < len(src) && i < len(decompressed); i++ {
			if src[i] != decompressed[i] {
				t.Fatalf("roundtrip mismatch at byte %d (orig_len=%d, dec_len=%d): want 0x%02X, got 0x%02X",
					i, len(src), len(decompressed), src[i], decompressed[i])
			}
		}
		t.Fatalf("roundtrip length mismatch: want %d bytes, got %d bytes", len(src), len(decompressed))
	}
	return compressed
}

// assertRoundtripTo tests DecompressTo into a pre-allocated dst buffer,
// verifying bit-exact identity.
func assertRoundtripTo(t *testing.T, src []byte, level int, dst []byte) []byte {
	t.Helper()
	compressed, err := przm.Compress(src, level)
	if err != nil {
		t.Fatalf("przm.Compress(level=%d, len=%d) failed: %v", level, len(src), err)
	}

	dec, err := przm.DecompressTo(compressed, dst)
	if err != nil {
		t.Fatalf("przm.DecompressTo failed: %v", err)
	}

	if !bytes.Equal(dec, src) {
		t.Fatalf("roundtrip DecompressTo mismatch: want len %d, got len %d", len(src), len(dec))
	}
	return compressed
}

// assertZeroAllocs runs fn runs times and verifies strictly 0 heap allocations per op.
func assertZeroAllocs(t *testing.T, runs int, fn func()) {
	t.Helper()
	allocs := testing.AllocsPerRun(runs, fn)
	if allocs > 0.0 {
		t.Fatalf("expected 0.0 heap allocations/run, got %.2f allocs/run", allocs)
	}
}

// assertNoPanic asserts that fn executes without panicking.
func assertNoPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("CRITICAL INVARIANT VIOLATION: %s panicked: %v", name, r)
		}
	}()
	fn()
}

// Data generator: repeating pattern with specified stride/offset to exercise repeat registers.
func generateRepeatOffsetData(length int, offsets []int) []byte {
	buf := make([]byte, length)
	// Base pattern
	pattern := []byte("PRZM_COMPRESSION_OFFSET_REGISTERS_STREAM_TEST_ABCDEF0123456789")
	for i := range buf {
		buf[i] = pattern[i%len(pattern)]
	}
	// Introduce repeats at specific offsets
	for _, off := range offsets {
		if off <= 0 || off >= length {
			continue
		}
		for i := off; i < length; i += off * 2 {
			copyLen := min(off, length-i)
			copy(buf[i:i+copyLen], buf[i-off:i-off+copyLen])
		}
	}
	return buf
}

// Data generator: skewed byte distribution to exercise Canonical Huffman entropy coding.
func generateSkewedDistribution(length int) []byte {
	buf := make([]byte, length)
	// 70% symbol A, 15% symbol B, 10% symbol C, 5% others
	for i := range buf {
		mod := i % 100
		switch {
		case mod < 70:
			buf[i] = 'A'
		case mod < 85:
			buf[i] = 'B'
		case mod < 95:
			buf[i] = 'C'
		default:
			buf[i] = byte(i % 256)
		}
	}
	return buf
}

// Data generator: high-entropy incompressible pseudo-random bytes.
func generateHighEntropyData(length int) []byte {
	buf := make([]byte, length)
	_, _ = rand.Read(buf)
	return buf
}

// Data generator: hash collision adversarial sequence (identical 16-bit prefix, differing suffixes).
func generateHashCollidingData(count int) []byte {
	var buf bytes.Buffer
	for i := range count {
		// All tokens share common prefix to produce identical 2-4 byte hash prefixes
		fmt.Fprintf(&buf, "PREFIX_FIXED_%04x_COLLISION_DATA_TOKEN;", i)
	}
	return buf.Bytes()
}

// Data generator: overlapping matches where DP parsing beats greedy choice.
func generateOverlappingMatchData(length int) []byte {
	var buf bytes.Buffer
	tokenA := "ABCDEF"
	tokenB := "BCDEFGH"
	tokenC := "CDEFGHIJ"
	for buf.Len() < length {
		buf.WriteString(tokenA)
		buf.WriteString(tokenB)
		buf.WriteString(tokenC)
		buf.WriteString(tokenA)
	}
	return buf.Bytes()[:length]
}

// Data generator: dense short 2-byte and 3-byte repetitions.
func generateShortRepeatData(length int, tokenLen int) []byte {
	buf := make([]byte, length)
	tokens := [][]byte{
		[]byte("AB"), []byte("CD"), []byte("EF"), []byte("XY"), // 2-byte
		[]byte("ABC"), []byte("XYZ"), []byte("123"), []byte("789"), // 3-byte
	}
	tok := tokens[tokenLen%len(tokens)]
	for i := 0; i < length; i += len(tok) {
		copy(buf[i:min(i+len(tok), length)], tok)
	}
	return buf
}

// Data generator: Base64 payload.
func generateBase64Payload(rawSize int) []byte {
	raw := make([]byte, rawSize)
	for i := range raw {
		raw[i] = byte((i * 17) % 256)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	return []byte(encoded)
}

// Data generator: Hex payload.
func generateHexPayload(rawSize int) []byte {
	raw := make([]byte, rawSize)
	for i := range raw {
		raw[i] = byte((i * 31) % 256)
	}
	encoded := hex.EncodeToString(raw)
	return []byte(encoded)
}

// Data generator: duplicate 64 KiB blocks to exercise cross-block deduplication.
func generateDuplicate64KBlocks(numBlocks int, corruptDelta int) []byte {
	const blockSize = 65536
	block := make([]byte, blockSize)
	for i := range block {
		block[i] = byte(i*13 + 7)
	}
	buf := make([]byte, numBlocks*blockSize)
	for b := range numBlocks {
		copy(buf[b*blockSize:(b+1)*blockSize], block)
		if corruptDelta > 0 && b == 1 {
			// Intentionally flip byte in second block if requested
			buf[b*blockSize+corruptDelta] ^= 0xFF
		}
	}
	return buf
}

// Data generator: synthetic x86_64 machine code with relative CALL instructions.
func generateMachineCode(length int) []byte {
	code := make([]byte, length)
	i := 0
	funcID := 0
	for i < len(code)-32 {
		funcID++
		// Function prologue: push rbp; mov rbp, rsp; sub rsp, 32
		prologue := []byte{0x55, 0x48, 0x89, 0xE5, 0x48, 0x83, 0xEC, 0x20}
		copy(code[i:], prologue)
		i += len(prologue)

		for j := 0; j < 4 && i < len(code)-16; j++ {
			code[i] = 0x8B
			code[i+1] = 0x45
			code[i+2] = 0xFC
			code[i+3] = 0x83
			code[i+4] = 0xC0
			code[i+5] = byte(j + 1)
			i += 6

			// Relative CALL (0xE8 <rel32>)
			code[i] = 0xE8
			rel := int32((funcID*128 + j*16) - i)
			binary.LittleEndian.PutUint32(code[i+1:i+5], uint32(rel))
			i += 5
		}

		if i < len(code)-2 {
			code[i] = 0xC9 // leave
			code[i+1] = 0xC3 // ret
			i += 2
		}
	}
	return code
}

// Data generator: synthetic PNG-like container with IDAT Deflate-like chunks.
func generatePNGContainer(width, height int) []byte {
	var buf bytes.Buffer
	// PNG signature: \x89PNG\r\n\x1a\n
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	// IHDR chunk
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // color type: truecolor RGB
	buf.Write(encodePNGChunk("IHDR", ihdr[:]))

	// Synthetic IDAT payload
	rawPixelCount := (width*3 + 1) * height
	pixelData := make([]byte, rawPixelCount)
	for y := 0; y < height; y++ {
		rowStart := y * (width*3 + 1)
		pixelData[rowStart] = 0 // Filter type: None
		for x := 0; x < width; x++ {
			px := rowStart + 1 + x*3
			pixelData[px] = byte(x % 256)
			pixelData[px+1] = byte(y % 256)
			pixelData[px+2] = byte((x + y) % 256)
		}
	}
	buf.Write(encodePNGChunk("IDAT", pixelData))

	// IEND chunk
	buf.Write(encodePNGChunk("IEND", nil))
	return buf.Bytes()
}

func encodePNGChunk(chunkType string, data []byte) []byte {
	chunkLen := uint32(len(data))
	var buf bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], chunkLen)
	buf.Write(lenBuf[:])
	buf.WriteString(chunkType)
	buf.Write(data)
	var crcBuf [4]byte // mock CRC
	binary.BigEndian.PutUint32(crcBuf[:], 0xCAFEBABE)
	buf.Write(crcBuf[:])
	return buf.Bytes()
}
