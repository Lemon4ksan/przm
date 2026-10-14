package clean_core

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestGetUncompressedSize(t *testing.T) {
	// Standard PRZM framing: 20000 bytes
	hdr := []byte{'P', 'R', 'Z', 'M', 0, 0x20, 0x4E, 0x00}
	sz, err := GetUncompressedSize(hdr)
	if err != nil || sz != 20000 {
		t.Fatalf("expected 20000, got %d, err: %v", sz, err)
	}

	// Truncated header
	if _, err := GetUncompressedSize(hdr[:7]); err != ErrCorruptedFrame {
		t.Fatalf("expected ErrCorruptedFrame, got %v", err)
	}
	if _, err := GetUncompressedSize(nil); err != ErrCorruptedFrame {
		t.Fatalf("expected ErrCorruptedFrame, got %v", err)
	}

	// Invalid magic
	badHdr := []byte{'X', 'Y', 'Z', 'W', 0, 0, 0, 0}
	if _, err := GetUncompressedSize(badHdr); err != ErrInvalidMagic {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}

	// Legacy container tags
	przsHdr := make([]byte, 17)
	copy(przsHdr, []byte{'P', 'R', 'Z', 'S', 0})
	binary.LittleEndian.PutUint32(przsHdr[5:9], 12345)
	if sz, err := GetUncompressedSize(przsHdr); err != nil || sz != 12345 {
		t.Fatalf("PRZS: expected 12345, got %d, err: %v", sz, err)
	}
}

func TestStandardFusedRaw(t *testing.T) {
	frame := make([]byte, 33)
	copy(frame[0:4], []byte{'P', 'R', 'Z', 'M'})
	frame[4] = FlagUncompressed
	frame[5] = 25
	frame[6] = 0
	frame[7] = 0
	for i := 8; i < 33; i++ {
		frame[i] = 'A'
	}

	dst := make([]byte, 128)
	n, err := Decompress(frame, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if n != 25 {
		t.Fatalf("expected 25 bytes, got %d", n)
	}
	for i := 0; i < 25; i++ {
		if dst[i] != 'A' {
			t.Fatalf("byte mismatch at %d: %c != 'A'", i, dst[i])
		}
	}
}

func TestStandardFusedLZ(t *testing.T) {
	frame := make([]byte, 0, 64)
	frame = append(frame, 'P', 'R', 'Z', 'M')
	frame = append(frame, 0x00) // flags
	frame = append(frame, 8, 0, 0) // U = 8
	// Section directory: L_cmd = 1, L_off = 0
	frame = append(frame, 1, 0)
	frame = append(frame, 0, 0)
	// Section 1: 8 bytes (W_log = 16, count = 0)
	frame = append(frame, 0, 16, 0, 0, 0, 0, 0, 0)
	// Section 2: Command 0xFA (8 literals)
	frame = append(frame, 0xFA)
	// Section 4: 8 literals
	frame = append(frame, []byte("ABCDEFGH")...)

	dst := make([]byte, 128)
	n, err := Decompress(frame, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if n != 8 {
		t.Fatalf("expected 8 bytes, got %d", n)
	}
	if string(dst[:8]) != "ABCDEFGH" {
		t.Fatalf("expected 'ABCDEFGH', got '%s'", string(dst[:8]))
	}
}

func TestCommandSplitDecompression(t *testing.T) {
	frame := make([]byte, 0, 64)
	frame = append(frame, 'P', 'R', 'Z', 'M')
	frame = append(frame, 0x00) // flags
	frame = append(frame, 4, 0, 0) // U = 4
	// Section directory: L_cmd = 17, L_off = 0
	frame = append(frame, 17, 0)
	frame = append(frame, 0, 0)
	// Section 1: 8 bytes
	frame = append(frame, 0, 16, 0, 0, 0, 0, 0, 0)
	// Section 2: Mode 8
	frame = append(frame, 0x08)
	// L_cmd_orig = 1
	var b4 [4]byte
	binary.LittleEndian.PutUint32(b4[:], 1)
	frame = append(frame, b4[:]...)
	// L_op = 2
	binary.LittleEndian.PutUint32(b4[:], 2)
	frame = append(frame, b4[:]...)
	// L_m = 1
	binary.LittleEndian.PutUint32(b4[:], 1)
	frame = append(frame, b4[:]...)
	// opStream: Mode 9, 0xF6 (4 literals)
	frame = append(frame, 0x09, 0xF6)
	// metaStream: Mode 9 (0 bytes)
	frame = append(frame, 0x09)
	// varStream: Mode 9 (0 bytes)
	frame = append(frame, 0x09)
	// Section 4: 4 literals
	frame = append(frame, []byte("WXYZ")...)

	dst := make([]byte, 128)
	n, err := Decompress(frame, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 bytes, got %d", n)
	}
	if string(dst[:4]) != "WXYZ" {
		t.Fatalf("expected 'WXYZ', got '%s'", string(dst[:4]))
	}
}

func TestDictionaryDecompression(t *testing.T) {
	// Brotli static dictionary: word 0 ("time")
	frame := make([]byte, 0, 64)
	frame = append(frame, 'P', 'R', 'Z', 'M')
	frame = append(frame, FlagDictRef)
	frame = append(frame, 4, 0, 0) // U = 4
	// DictID: 'B','R','T','L' = 0x4252544C
	frame = append(frame, 0x4C, 0x54, 0x52, 0x42)
	// Section directory: L_cmd = 3, L_off = 0
	frame = append(frame, 3, 0)
	frame = append(frame, 0, 0)
	// Section 2: 0xCF, 0x80, 0x00 -> word 0, tau = 0
	frame = append(frame, 0xCF, 0x80, 0x00)

	dst := make([]byte, 128)
	n, err := Decompress(frame, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if n != 4 {
		t.Fatalf("expected 4 bytes, got %d", n)
	}
	if string(dst[:4]) != "time" {
		t.Fatalf("expected 'time', got '%s'", string(dst[:4]))
	}

	// External dictionary test
	dict := DictionaryDesc{
		DictID:     0x00001001,
		NumEntries: 2,
		Entries: []DictEntry{
			{Data: "{\"custom\":", Length: 10},
			{Data: "test", Length: 4},
		},
	}
	dictFrame := []byte{
		0x50, 0x52, 0x5A, 0x4D, 0x10, 0x0A, 0x00, 0x00,
		0x01, 0x10, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		0x00, 0x01, 0x00, 0x00, 0x00, 0xD8,
	}
	n, err = DecompressDict(dictFrame, dst, &dict)
	if err != nil {
		t.Fatalf("DecompressDict failed: %v", err)
	}
	if n != 10 {
		t.Fatalf("expected 10 bytes, got %d", n)
	}
	if !bytes.Equal(dst[:10], []byte("{\"custom\":")) {
		t.Fatalf("expected '{\"custom\":', got '%s'", string(dst[:10]))
	}
}

func TestCorruptedAndTruncatedFrames(t *testing.T) {
	dst := make([]byte, 256)

	if _, err := Decompress(nil, dst); err != nil {
		// Empty / nil src returns 0, nil
	}
	if _, err := Decompress([]byte("PRZM"), dst); err != ErrCorruptedFrame {
		t.Fatalf("expected ErrCorruptedFrame, got %v", err)
	}

	badMagic := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0, 0x10, 0, 0}
	if _, err := Decompress(badMagic, dst); err != ErrInvalidMagic {
		t.Fatalf("expected ErrInvalidMagic, got %v", err)
	}

	smallDst := make([]byte, 4)
	validHdr := []byte{'P', 'R', 'Z', 'M', 0, 100, 0, 0}
	if _, err := Decompress(validHdr, smallDst); err != ErrBufferTooSmall {
		t.Fatalf("expected ErrBufferTooSmall, got %v", err)
	}
}

func TestZeroAllocations(t *testing.T) {
	// Construct test frames
	rawFrame := make([]byte, 33)
	copy(rawFrame[0:4], []byte{'P', 'R', 'Z', 'M'})
	rawFrame[4] = FlagUncompressed
	rawFrame[5] = 25
	copy(rawFrame[8:], bytes.Repeat([]byte{'A'}, 25))

	lzFrame := []byte{
		'P', 'R', 'Z', 'M', 0x00, 8, 0, 0,
		1, 0, 0, 0,
		0, 16, 0, 0, 0, 0, 0, 0,
		0xFA,
		'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H',
	}

	brotliFrame := []byte{
		'P', 'R', 'Z', 'M', FlagDictRef, 4, 0, 0,
		0x4C, 0x54, 0x52, 0x42,
		3, 0, 0, 0,
		0xCF, 0x80, 0x00,
	}

	dst := make([]byte, 128)

	// Warm up pool
	Decompress(rawFrame, dst)
	Decompress(lzFrame, dst)
	Decompress(brotliFrame, dst)

	allocsRaw := testing.AllocsPerRun(100, func() {
		_, _ = Decompress(rawFrame, dst)
	})
	if allocsRaw != 0.00 {
		t.Fatalf("Raw decompression allocated %f allocs/op (strictly 0.00 required!)", allocsRaw)
	}

	allocsLZ := testing.AllocsPerRun(100, func() {
		_, _ = Decompress(lzFrame, dst)
	})
	if allocsLZ != 0.00 {
		t.Fatalf("LZ decompression allocated %f allocs/op (strictly 0.00 required!)", allocsLZ)
	}

	allocsBrotli := testing.AllocsPerRun(100, func() {
		_, _ = Decompress(brotliFrame, dst)
	})
	if allocsBrotli != 0.00 {
		t.Fatalf("Brotli decompression allocated %f allocs/op (strictly 0.00 required!)", allocsBrotli)
	}
}

func BenchmarkDecompress(b *testing.B) {
	rawFrame := make([]byte, 33)
	copy(rawFrame[0:4], []byte{'P', 'R', 'Z', 'M'})
	rawFrame[4] = FlagUncompressed
	rawFrame[5] = 25
	copy(rawFrame[8:], bytes.Repeat([]byte{'A'}, 25))

	lzFrame := []byte{
		'P', 'R', 'Z', 'M', 0x00, 8, 0, 0,
		1, 0, 0, 0,
		0, 16, 0, 0, 0, 0, 0, 0,
		0xFA,
		'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H',
	}

	brotliFrame := []byte{
		'P', 'R', 'Z', 'M', FlagDictRef, 4, 0, 0,
		0x4C, 0x54, 0x52, 0x42,
		3, 0, 0, 0,
		0xCF, 0x80, 0x00,
	}

	dst := make([]byte, 128)

	b.Run("Raw", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = Decompress(rawFrame, dst)
		}
	})

	b.Run("LZ", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = Decompress(lzFrame, dst)
		}
	})

	b.Run("Brotli", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = Decompress(brotliFrame, dst)
		}
	})
}
