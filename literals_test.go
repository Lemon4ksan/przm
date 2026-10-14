package przm

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"
)

func TestFoliationClassification(t *testing.T) {
	// Mode 0: Initial p1=0 => 3
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

	// Mode 1: Initial p1=0 => 3
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

	// Mode 2: Initial p1=0 => 1
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

	// Mode 3: Initial (0, 0) => 1
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
	for i := 0; i < 256; i++ {
		if decodedFreq[i] != freq[i] {
			t.Fatalf("Frequency mismatch for symbol %d: expected %d, got %d", i, freq[i], decodedFreq[i])
		}
	}

	// Test corruption when sum != 4096
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

	s := new(Scratchpad)
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

		enc, err := EncodeVANS4x(raw, freq[:])
		if err != nil {
			t.Fatalf("EncodeVANS4x failed: %v", err)
		}

		dst := make([]byte, size)
		if err := DecodeVANS4x(enc, dst, size, s); err != nil {
			t.Fatalf("DecodeVANS4x failed for size %d: %v", size, err)
		}
		if !bytes.Equal(dst, raw) {
			t.Fatalf("VANS4x roundtrip mismatch for size %d", size)
		}
	}
}

func TestNibblePlanesRoundtrip(t *testing.T) {
	testData := [][]byte{
		[]byte("hello world 1234567890"),
		bytes.Repeat([]byte("0123456789ABCDEF"), 20),
		[]byte("a"),
		[]byte("ab"),
		[]byte("abc"),
	}

	for _, raw := range testData {
		packed := PackNibblePlanes(raw)
		dst := make([]byte, len(raw))
		err := UnpackNibblePlanes(packed, dst, len(raw))
		if err != nil {
			t.Fatalf("UnpackNibblePlanes failed: %v", err)
		}
		if !bytes.Equal(dst, raw) {
			t.Fatalf("Nibble planes roundtrip mismatch")
		}
	}
}

func TestEtaleSheafContainerRoundtrip(t *testing.T) {
	s := new(Scratchpad)
	sample := bytes.Repeat([]byte("int compute(int x) { return x * 42 + 7; }\n"), 30)

	for mode := uint8(0); mode <= 3; mode++ {
		packed, err := PackEtaleSheaf(sample, mode)
		if err != nil {
			t.Fatalf("PackEtaleSheaf failed for mode %d: %v", mode, err)
		}

		buf, err := UnpackSection4Literals(packed, len(packed), s)
		if err != nil {
			t.Fatalf("UnpackSection4Literals failed for mode %d: %v", mode, err)
		}
		if !bytes.Equal(buf, sample) {
			t.Fatalf("Sheaf container roundtrip mismatch for mode %d", mode)
		}
	}
}

func TestFullFrameWithEtaleSheaf(t *testing.T) {
	// Build an end-to-end frame where Section 4 is Étale Sheaf compressed!
	sampleText := []byte("function test() { return 123; }\n")
	lits := bytes.Repeat(sampleText, 10)
	nLits := len(lits)

	packedLits, err := PackEtaleSheaf(lits, 0)
	if err != nil {
		t.Fatalf("PackEtaleSheaf failed: %v", err)
	}

	// Commands: 1 direct literal block for all nLits
	// Opcode 0xF0: Extended Literals
	var cmds []byte
	cmds = append(cmds, 0xF0)
	cmds = appendLEB128(cmds, uint32(nLits))

	uSize := uint32(nLits)
	var frame []byte
	frame = append(frame, 'P', 'R', 'Z', 'M')
	frame = append(frame, 0x80) // FlagNibble
	frame = append(frame, byte(uSize&0xFF), byte((uSize>>8)&0xFF), byte((uSize>>16)&0xFF))

	// Directory
	frame = binary.LittleEndian.AppendUint16(frame, uint16(len(cmds)))
	frame = binary.LittleEndian.AppendUint16(frame, uint16(0)) // 0 offsets
	// Section 1: parameters (8 bytes)
	frame = append(frame, 0, 16, 0, 0, 0, 0, 0, 0)
	// Section 2: commands
	frame = append(frame, cmds...)
	// Section 3: offsets (0 bytes)
	// Section 4: packed literals
	frame = append(frame, packedLits...)

	decompressed := make([]byte, uSize+64)
	n, err := DecompressWithDict(frame, decompressed, nil, nil)
	if err != nil {
		t.Fatalf("DecompressWithDict failed on Étale Sheaf frame: %v", err)
	}
	if n != len(lits) {
		t.Fatalf("Expected length %d, got %d", len(lits), n)
	}
	if !bytes.Equal(decompressed[:n], lits) {
		t.Fatalf("Decompressed payload mismatch")
	}
}

func appendLEB128(buf []byte, v uint32) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v&0x7F)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

func BenchmarkLiterals(b *testing.B) {
	sampleText := []byte("int calculate(int val) { return val * 10 + 5; }\n")
	lits := bytes.Repeat(sampleText, 50)
	packedLits, err := PackEtaleSheaf(lits, 0)
	if err != nil {
		b.Fatalf("Pack failed: %v", err)
	}

	uSize := uint32(len(lits))
	var cmds []byte
	cmds = append(cmds, 0xF0)
	cmds = appendLEB128(cmds, uSize)

	var frame []byte
	frame = append(frame, 'P', 'R', 'Z', 'M')
	frame = append(frame, 0x80) // FlagNibble
	frame = append(frame, byte(uSize&0xFF), byte((uSize>>8)&0xFF), byte((uSize>>16)&0xFF))
	frame = binary.LittleEndian.AppendUint16(frame, uint16(len(cmds)))
	frame = binary.LittleEndian.AppendUint16(frame, uint16(0))
	frame = append(frame, 0, 16, 0, 0, 0, 0, 0, 0) // Section 1
	frame = append(frame, cmds...)
	frame = append(frame, packedLits...)

	dst := make([]byte, uSize+64)

	// Warmup
	if _, err := DecompressWithDict(frame, dst, nil, nil); err != nil {
		b.Fatalf("Decompress failed: %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		n, err := DecompressWithDict(frame, dst, nil, nil)
		if err != nil || n != len(lits) {
			b.Fatalf("Run failed: %v", err)
		}
	})

	if allocs != 0.00 {
		b.Fatalf("Zero Allocation Law VIOLATION: expected 0.00 allocs/op, got %.2f", allocs)
	}

	b.SetBytes(int64(uSize))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = DecompressWithDict(frame, dst, nil, nil)
	}
}
