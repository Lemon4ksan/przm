package przm

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// TestSpectral_Theorem24_UnitaryAxioms verifies U * U^T == I over F_{2^8}.
func TestSpectral_Theorem24_UnitaryAxioms(t *testing.T) {
	// 1. Check Identity
	id := IdentityUnitary4()
	if !id.IsUnitary() {
		t.Fatalf("Identity matrix is not unitary")
	}

	// 2. Check Givens rotations for all pairs and angles
	pairs := [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}
	for _, p := range pairs {
		for c := 0; c < 256; c += 17 {
			g := GivensRotation4(p[0], p[1], uint8(c))
			if !g.IsUnitary() {
				t.Fatalf("Givens rotation (%d, %d, %d) is not unitary", p[0], p[1], c)
			}
		}
	}

	// 3. Check composition of unitary rotations
	g1 := GivensRotation4(0, 1, 0x57)
	g2 := GivensRotation4(2, 3, 0x8A)
	g3 := GivensRotation4(1, 2, 0xC3)
	comp := g1.Mul(g2).Mul(g3)
	if !comp.IsUnitary() {
		t.Fatalf("Composite unitary matrix is not unitary")
	}
}

// TestSpectral_Invertibility verifies bit-exact reconstruction of ApplyUnitary4 via InvertUnitary4.
func TestSpectral_Invertibility(t *testing.T) {
	n := 1024
	orig := make([]byte, n)
	rand.Read(orig)

	// Construct non-trivial unitary matrix
	g1 := GivensRotation4(0, 1, 0x33)
	g2 := GivensRotation4(1, 2, 0x77)
	U := g1.Mul(g2)

	transformed := make([]byte, n)
	ApplyUnitary4(orig, U, transformed)

	recovered := make([]byte, n)
	InvertUnitary4(transformed, U, recovered)

	if !bytes.Equal(orig, recovered) {
		t.Fatalf("InvertUnitary4 did not recover original data bit-exact")
	}
}

// TestSpectral_Theorem24_OffDiagonalEntropyReduction verifies that optimal unitary reduction reduces off-diagonal energy.
func TestSpectral_Theorem24_OffDiagonalEntropyReduction(t *testing.T) {
	// Construct correlated 4-channel data: ch1 = ch0 ^ 0xAA, ch2 = ch0 ^ ch1, ch3 = ch2
	numVecs := 256
	data := make([]byte, numVecs*4)
	for i := 0; i < numVecs; i++ {
		ch0 := uint8(i)
		ch1 := ch0 ^ 0xAA
		ch2 := ch0 ^ ch1
		ch3 := ch2
		data[i*4] = ch0
		data[i*4+1] = ch1
		data[i*4+2] = ch2
		data[i*4+3] = ch3
	}

	rhoBefore := ComputeGramMatrix4(data)
	scoreBefore := OffDiagonalSum(rhoBefore)

	U := FindOptimalUnitary4(rhoBefore)
	transformed := make([]byte, len(data))
	ApplyUnitary4(data, U, transformed)

	rhoAfter := ComputeGramMatrix4(transformed)
	scoreAfter := OffDiagonalSum(rhoAfter)

	if scoreAfter > scoreBefore {
		t.Fatalf("Off-diagonal score increased after unitary diagonalization: before %d, after %d", scoreBefore, scoreAfter)
	}
}

// TestSpectral_Roundtrip verifies end-to-end compression and decompression.
func TestSpectral_Roundtrip(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"Small", []byte("Hello, Spectral World!")},
		{"CorrelatedChannels", bytes.Repeat([]byte{0x10, 0x20, 0x30, 0x40}, 256)},
		{"MixedData", bytes.Repeat([]byte("QUANTUM_UNITARY_SPECTRAL_DIAGONALIZATION_2026"), 64)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := CompressSpectralUnitary(tc.data)
			dec, err := DecompressSpectralUnitary(enc, nil)
			if err != nil {
				t.Fatalf("DecompressSpectralUnitary error: %v", err)
			}
			if !bytes.Equal(tc.data, dec) {
				t.Fatalf("DecompressSpectralUnitary mismatch: orig len %d, dec len %d", len(tc.data), len(dec))
			}
		})
	}
}

// BenchmarkSpectralDecompress measures decoding throughput and memory allocations.
func BenchmarkSpectralDecompress(b *testing.B) {
	data := bytes.Repeat([]byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}, 2048) // 16 KB
	enc := CompressSpectralUnitary(data)

	dst := make([]byte, len(data))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := DecompressSpectralUnitary(enc, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}
