// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package literals

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestFoliationClassification(t *testing.T) {
	if c := ContextClassify(0x00, 0); c != 3 {
		t.Fatalf("Mode 0 initial expected 3, got %d", c)
	}
	if c := ContextClassify(' ', 0); c != 0 {
		t.Fatalf("Mode 0 space expected 0, got %d", c)
	}
	if c := ContextClassify('{', 0); c != 1 {
		t.Fatalf("Mode 0 '{' expected 1, got %d", c)
	}
	if c := ContextClassify('a', 0); c != 2 {
		t.Fatalf("Mode 0 'a' expected 2, got %d", c)
	}
	if c := ContextClassify('A', 0); c != 3 {
		t.Fatalf("Mode 0 'A' expected 3, got %d", c)
	}

	if c := ContextClassify(0x00, 1); c != 3 {
		t.Fatalf("Mode 1 initial expected 3, got %d", c)
	}
	if c := ContextClassify('"', 1); c != 0 {
		t.Fatalf("Mode 1 quote expected 0, got %d", c)
	}
	if c := ContextClassify(':', 1); c != 0 {
		t.Fatalf("Mode 1 colon expected 0, got %d", c)
	}
	if c := ContextClassify('x', 1); c != 1 {
		t.Fatalf("Mode 1 'x' expected 1, got %d", c)
	}
	if c := ContextClassify('7', 1); c != 2 {
		t.Fatalf("Mode 1 '7' expected 2, got %d", c)
	}

	if c := ContextClassify(0x00, 2); c != 1 {
		t.Fatalf("Mode 2 initial expected 1, got %d", c)
	}
	if c := ContextClassify(' ', 2); c != 0 {
		t.Fatalf("Mode 2 space expected 0, got %d", c)
	}
	if c := ContextClassify('+', 2); c != 0 {
		t.Fatalf("Mode 2 '+' expected 0, got %d", c)
	}
	if c := ContextClassify('a', 2); c != 1 {
		t.Fatalf("Mode 2 'a' expected 1, got %d", c)
	}

	if c := ContextClassifyOrder2(0, 0); c != 1 {
		t.Fatalf("Mode 3 initial expected 1, got %d", c)
	}
	if c := ContextClassifyOrder2('1', '2'); c != 0 {
		t.Fatalf("Mode 3 digit-digit expected 0, got %d", c)
	}
	if c := ContextClassifyOrder2('a', '2'); c != 1 {
		t.Fatalf("Mode 3 nondigit-digit expected 1, got %d", c)
	}
	if c := ContextClassifyOrder2('a', 'b'); c != 2 {
		t.Fatalf("Mode 3 ident-ident expected 2, got %d", c)
	}
	if c := ContextClassifyOrder2('9', 'b'); c != 3 {
		t.Fatalf("Mode 3 nonident-ident expected 3, got %d", c)
	}
	if c := ContextClassifyOrder2('z', ' '); c != 0 {
		t.Fatalf("Mode 3 ws expected 0, got %d", c)
	}
}

func TestFoliationRoundtrip(t *testing.T) {
	testData := [][]byte{
		[]byte("hello world! { \"key\": 12345, \"flag\": true }"),
		[]byte("int main() { printf(\"Value: %d\\n\", 42); return 0; }"),
		[]byte("ABCDEF1234567890abcdefghijklmnopqrstuvwxyz_"),
		bytes.Repeat([]byte("alpha beta gamma delta epsilon "), 50),
	}

	for _, data := range testData {
		for mode := uint8(0); mode <= 3; mode++ {
			fibers, err := DecomposeFibers(data, mode)
			if err != nil {
				t.Fatalf("Decompose failed for mode %d: %v", mode, err)
			}
			dst := make([]byte, len(data))
			err = ReconstituteFibers(dst, len(data),
				fibers[0], fibers[1], fibers[2], fibers[3],
				len(fibers[0]), len(fibers[1]), len(fibers[2]), len(fibers[3]),
				mode)
			if err != nil {
				t.Fatalf("Reconstitute failed for mode %d: %v", mode, err)
			}
			if !bytes.Equal(dst, data) {
				t.Fatalf("Foliation roundtrip mismatch for mode %d", mode)
			}
		}
	}
}

func TestJointNibbleFrequencyMatrix(t *testing.T) {
	var freq [256]uint16
	freq['a'] = 2048
	freq['b'] = 1024
	freq['c'] = 1024

	buf := EncodeJointNibbleFreq(freq[:], nil)
	pos := 0
	var decodedFreq [256]uint16
	err := DecodeJointNibbleFreq(buf, &pos, decodedFreq[:])
	if err != nil {
		t.Fatalf("DecodeJointNibbleFreq failed: %v", err)
	}
	if pos != len(buf) {
		t.Fatalf("Expected pos %d, got %d", len(buf), pos)
	}
	for i := range 256 {
		if decodedFreq[i] != freq[i] {
			t.Fatalf("Frequency mismatch for symbol %d: expected %d, got %d", i, freq[i], decodedFreq[i])
		}
	}

	freq['a'] = 2000
	badBuf := EncodeJointNibbleFreq(freq[:], nil)
	pos = 0
	err = DecodeJointNibbleFreq(badBuf, &pos, decodedFreq[:])
	if err == nil {
		t.Fatalf("Expected corruption error on sum != 4096")
	}
}

func TestVANS4xRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	testSizes := []int{4, 16, 64, 512, 4096}

	var lookup [4096]byte
	var decFreq, decCDF [256]uint16

	for _, size := range testSizes {
		raw := make([]byte, size)
		for i := range raw {
			raw[i] = byte('a' + rng.Intn(10))
		}

		counts := make([]int, 256)
		for _, b := range raw {
			counts[b]++
		}
		var freq [256]uint16
		if err := NormalizeFrequencies(counts, freq[:]); err != nil {
			t.Fatalf("NormalizeFrequencies failed: %v", err)
		}

		encoded, err := EncodeVANS4x(raw, freq[:])
		if err != nil {
			t.Fatalf("EncodeVANS4x failed (size %d): %v", size, err)
		}

		decoded := make([]byte, size)
		if err := DecodeVANS4x(encoded, decoded, size, lookup[:], decFreq[:], decCDF[:]); err != nil {
			t.Fatalf("DecodeVANS4x failed (size %d): %v", size, err)
		}

		if !bytes.Equal(raw, decoded) {
			t.Fatalf("VANS4x mismatch for size %d", size)
		}
	}
}

func TestNibblePlanesRoundtrip(t *testing.T) {
	testCases := [][]byte{
		[]byte("hello world"),
		[]byte("1234567890"),
		bytes.Repeat([]byte("test"), 30),
	}

	for _, tc := range testCases {
		packed := PackNibblePlanes(tc)
		unpacked := make([]byte, len(tc))
		err := UnpackNibblePlanes(packed, unpacked, len(tc))
		if err != nil {
			t.Fatalf("UnpackNibblePlanes failed: %v", err)
		}
		if !bytes.Equal(tc, unpacked) {
			t.Fatalf("Nibble planes roundtrip mismatch: expected %q, got %q", string(tc), string(unpacked))
		}
	}
}

func TestEtaleSheafContainerRoundtrip(t *testing.T) {
	testData := []byte("func main() { var x = 42; return x + 1; }")
	for mode := uint8(0); mode <= 3; mode++ {
		packed, err := PackEtaleSheaf(testData, mode)
		if err != nil {
			t.Fatalf("PackEtaleSheaf failed for mode %d: %v", mode, err)
		}

		var litsBuf [1024]byte
		var fiberBuf [1024]byte
		unpacked, err := UnpackSection4Literals(packed, len(packed), litsBuf[:], fiberBuf[:])
		if err != nil {
			t.Fatalf("UnpackSection4Literals failed for mode %d: %v", mode, err)
		}
		if !bytes.Equal(testData, unpacked) {
			t.Fatalf("Sheaf container roundtrip mismatch for mode %d", mode)
		}
	}
}

func BenchmarkClassifyContext(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = ClassifyContext(byte(i), uint8(i&3))
	}
}

func BenchmarkDecomposeFibers(b *testing.B) {
	src := bytes.Repeat([]byte("function computeHash(key, seed) { return (key ^ seed) * 0x9e3779b9; }\n"), 16)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecomposeFibers(src, 1)
	}
}

func BenchmarkReconstituteFoliation(b *testing.B) {
	src := bytes.Repeat([]byte("function computeHash(key, seed) { return (key ^ seed) * 0x9e3779b9; }\n"), 16)
	fibers, _ := DecomposeFibers(src, 1)
	dst := make([]byte, len(src))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ReconstituteFoliation(dst, len(src),
			fibers[0], fibers[1], fibers[2], fibers[3],
			len(fibers[0]), len(fibers[1]), len(fibers[2]), len(fibers[3]),
			1)
	}
}

func BenchmarkVans4x(b *testing.B) {
	rng := rand.New(rand.NewSource(42))
	raw := make([]byte, 1024)
	for i := range raw {
		raw[i] = byte('a' + rng.Intn(10))
	}
	counts := make([]int, 256)
	for _, c := range raw {
		counts[c]++
	}
	var freq [256]uint16
	_ = NormalizeFrequencies(counts, freq[:])
	enc, _ := EncodeVans4x(raw, freq[:])
	dst := make([]byte, len(raw))

	b.Run("Encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = EncodeVans4x(raw, freq[:])
		}
	})

	b.Run("Decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = DecodeVans4x(enc, dst, len(raw))
		}
	})
}

func BenchmarkNibblePlanes(b *testing.B) {
	raw := bytes.Repeat([]byte("alpha-numeric-payload-with-high-low-nibbles-1234567890"), 20)
	packed := PackNibblePlanes(raw)
	dst := make([]byte, len(raw))

	b.Run("Pack", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = PackNibblePlanes(raw)
		}
	})

	b.Run("Unpack", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = UnpackNibblePlanes(packed, dst, len(raw))
		}
	})
}

func BenchmarkEtaleSheafContainer(b *testing.B) {
	sample := bytes.Repeat([]byte("int compute(int x) { return x * 42 + 7; }\n"), 30)
	packed, _ := PackEtaleSheaf(sample, 0)
	var litsBuf [4096]byte
	var fiberBuf [4096]byte

	b.Run("Pack", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = PackEtaleSheaf(sample, 0)
		}
	})

	b.Run("Unpack", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = UnpackSection4Literals(packed, len(packed), litsBuf[:], fiberBuf[:])
		}
	})
}
