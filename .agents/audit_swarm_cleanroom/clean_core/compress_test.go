package clean_core

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"testing"
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
	for i := 0; i < 300; i++ {
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
	chunk := bytes.Repeat([]byte("AlphaBetaGammaDeltaEpsilonZetaEtaThetaIotaKappaLambdaMuNuXiOmicronPiRhoSigmaTau"), 500)
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
	pattern := []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. ")
	src := bytes.Repeat(pattern, 100) // ~12 KiB

	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Compress(src, 6)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecompressRoundtrip(b *testing.B) {
	pattern := []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. ")
	src := bytes.Repeat(pattern, 100) // ~12 KiB
	comp, err := Compress(src, 6)
	if err != nil {
		b.Fatal(err)
	}

	dst := make([]byte, len(src)+128)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := Decompress(comp, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}
