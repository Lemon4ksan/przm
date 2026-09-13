// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"crypto/rand"
	"os"
	"testing"
)

// TestMahler_Theorem32_UnimodularInvertibility proves Theorem 32:
// The unipotent lower-triangular Pascal matrix has det(B) = 1 (unimodular over Z/256Z).
// Forward and inverse Fast Pascal transforms are strict integer bijections.
func TestMahler_Theorem32_UnimodularInvertibility(t *testing.T) {
	// 1. Single 16-byte block random fuzz test
	for iter := 0; iter < 1000; iter++ {
		var orig, buf [16]byte
		rand.Read(orig[:])
		copy(buf[:], orig[:])

		Mahler16Forward(&buf)
		Mahler16Inverse(&buf)

		if buf != orig {
			t.Fatalf("Mahler16 bijection failed: got %v, want %v", buf, orig)
		}
	}

	// 2. Single 32-byte block random fuzz test
	for iter := 0; iter < 1000; iter++ {
		var orig, buf [32]byte
		rand.Read(orig[:])
		copy(buf[:], orig[:])

		Mahler32Forward(&buf)
		Mahler32Inverse(&buf)

		if buf != orig {
			t.Fatalf("Mahler32 bijection failed: got %v, want %v", buf, orig)
		}
	}

	// 3. Multi-block continuous buffer test
	sizes := []int{16, 64, 256, 1024, 65536}
	for _, sz := range sizes {
		orig := make([]byte, sz)
		rand.Read(orig)

		buf := make([]byte, sz)
		copy(buf, orig)

		MahlerTransform16Inplace(buf)
		MahlerInverse16Inplace(buf)

		if !bytes.Equal(buf, orig) {
			t.Fatalf("Multi-block Mahler transform mismatch on size %d", sz)
		}
	}
}

// TestMahler_Theorem33_PolynomialEntropyAnnihilation proves Theorem 33:
// Any polynomial sequence of degree d has Delta^n f == 0 for all n > d.
// The Mahler spectral coefficients vanish completely into zeros for n > d.
func TestMahler_Theorem33_PolynomialEntropyAnnihilation(t *testing.T) {
	// 1. Degree 1 polynomial: f(x) = 7*x + 13 (mod 256)
	var deg1 [16]byte
	for i := 0; i < 16; i++ {
		deg1[i] = byte((7*i + 13) & 0xFF)
	}

	trans1 := deg1
	Mahler16Forward(&trans1)

	// In degree 1, all coefficients for n > 1 must be strictly 0!
	for n := 2; n < 16; n++ {
		if trans1[n] != 0 {
			t.Fatalf("Theorem 33 violated for deg 1: c[%d] = %d != 0", n, trans1[n])
		}
	}
	// Verify exact reconstruction
	Mahler16Inverse(&trans1)
	if trans1 != deg1 {
		t.Fatalf("Reconstruction failed on deg 1 polynomial")
	}

	// 2. Degree 2 polynomial: f(x) = 3*x^2 + 5*x + 42 (mod 256)
	var deg2 [16]byte
	for i := 0; i < 16; i++ {
		deg2[i] = byte((3*i*i + 5*i + 42) & 0xFF)
	}

	trans2 := deg2
	Mahler16Forward(&trans2)

	// In degree 2, all coefficients for n > 2 must be strictly 0!
	for n := 3; n < 16; n++ {
		if trans2[n] != 0 {
			t.Fatalf("Theorem 33 violated for deg 2: c[%d] = %d != 0", n, trans2[n])
		}
	}
	// Verify exact reconstruction
	Mahler16Inverse(&trans2)
	if trans2 != deg2 {
		t.Fatalf("Reconstruction failed on deg 2 polynomial")
	}
}

// TestMahler_Roundtrip verifies CompressMahler -> DecompressMahler roundtrip.
func TestMahler_Roundtrip(t *testing.T) {
	t.Run("ShortBuffers", func(t *testing.T) {
		for sz := 0; sz < 32; sz++ {
			data := make([]byte, sz)
			for i := range data {
				data[i] = byte(i * 3)
			}
			comp := CompressMahler(data)
			dec, err := DecompressMahler(comp, nil)
			if err != nil {
				t.Fatalf("Decompress failed for size %d: %v", sz, err)
			}
			if !bytes.Equal(dec, data) {
				t.Fatalf("Roundtrip mismatch for short buffer size %d", sz)
			}
		}
	})

	t.Run("CorrelatedPolynomialStream", func(t *testing.T) {
		n := 8192
		data := make([]byte, n)
		for i := 0; i < n; i++ {
			// Piecewise polynomial trends (e.g. sensor / memory address steps)
			blkIdx := i / 16
			rem := i % 16
			data[i] = byte((blkIdx*5 + rem*3 + 17) & 0xFF)
		}

		comp := CompressMahler(data)
		dec, err := DecompressMahler(comp, nil)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, data) {
			t.Fatalf("Roundtrip mismatch on correlated polynomial stream")
		}
	})

	t.Run("RandomUniformNoise", func(t *testing.T) {
		n := 16384
		data := make([]byte, n)
		rand.Read(data)

		comp := CompressMahler(data)
		dec, err := DecompressMahler(comp, nil)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, data) {
			t.Fatalf("Roundtrip mismatch on random uniform noise")
		}
	})

	t.Run("ErrorCases", func(t *testing.T) {
		if _, err := DecompressMahler([]byte{1, 2, 3}, nil); err != ErrMahlerCorrupt {
			t.Fatalf("Expected ErrMahlerCorrupt for short header")
		}
		badMagic := make([]byte, 16)
		if _, err := DecompressMahler(badMagic, nil); err != ErrMahlerCorrupt {
			t.Fatalf("Expected ErrMahlerCorrupt for bad magic")
		}
	})
}

// TestMahler_EndToEndPipeline verifies full integration into przm.Compress and przm.Decompress.
func TestMahler_EndToEndPipeline(t *testing.T) {
	n := 4096
	data := make([]byte, n)
	for i := 0; i < n; i++ {
		rem := i % 16
		data[i] = byte((rem*rem*3 + rem*7 + 11) & 0xFF)
	}

	compressed, err := Compress(data, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(decompressed, data) {
		t.Fatalf("End-to-end Compress/Decompress mismatch for Mahler data: got len %d, want %d", len(decompressed), len(data))
	}
}

// TestMahler_RealWorldCorpusEvaluation runs Mahler transform against diverse data types
// to evaluate where and how much boost it provides in real-world scenarios.
func TestMahler_RealWorldCorpusEvaluation(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		// 1. Monotonic / affine counter streams (e.g. timestamps, auto-increment IDs, telemetry)
		{"MonotonicCounters", func() []byte {
			b := make([]byte, 8192)
			for i := range b {
				b[i] = byte(i & 0xFF)
			}
			return b
		}()},
		// 2. Structured telemetry / step strides
		{"TelemetrySteps", func() []byte {
			b := make([]byte, 8192)
			for i := range b {
				b[i] = byte((i/16)*4 + (i%16)*2)
			}
			return b
		}()},
		// 3. Audio / sensor linear gradient wave
		{"LinearGradients", func() []byte {
			b := make([]byte, 8192)
			for i := range b {
				b[i] = byte((i * 13) & 0xFF)
			}
			return b
		}()},
		// 4. Quantized neural network weight distributions (smooth local gradients)
		{"QuantizedWeights", func() []byte {
			b := make([]byte, 8192)
			for i := range b {
				b[i] = byte(int8(float64(i%32)*1.5) + 64)
			}
			return b
		}()},
	}

	for _, tc := range testCases {
		orig := tc.data
		origLen := len(orig)

		// Measure zero concentration before and after Mahler transform
		zerosBefore := 0
		for _, b := range orig {
			if b == 0 {
				zerosBefore++
			}
		}

		transformed := make([]byte, origLen)
		copy(transformed, orig)
		MahlerTransform16Inplace(transformed)

		zerosAfter := 0
		for _, b := range transformed {
			if b == 0 {
				zerosAfter++
			}
		}

		comp := CompressMahler(orig)
		dec, err := DecompressMahler(comp, nil)
		if err != nil {
			t.Fatalf("%s: decompress failed: %v", tc.name, err)
		}
		if !bytes.Equal(dec, orig) {
			t.Fatalf("%s: roundtrip mismatch", tc.name)
		}

		t.Logf("[%s] Raw: %d B | Mahler Comp: %d B (Ratio: %.2fx) | Zeros Before: %.1f%% -> Zeros After: %.1f%%",
			tc.name, origLen, len(comp), float64(origLen)/float64(len(comp)),
			float64(zerosBefore)*100/float64(origLen),
			float64(zerosAfter)*100/float64(origLen))
	}

	// 5. Test on Real Go binary go.exe if present
	if data, err := os.ReadFile(`C:\Program Files\go\bin\go.exe`); err == nil && len(data) >= 65536 {
		chunk := data[:65536]
		zerosBefore := 0
		for _, b := range chunk {
			if b == 0 {
				zerosBefore++
			}
		}
		transformed := make([]byte, len(chunk))
		copy(transformed, chunk)
		MahlerTransform16Inplace(transformed)

		zerosAfter := 0
		for _, b := range transformed {
			if b == 0 {
				zerosAfter++
			}
		}
		t.Logf("[RealBinary:go.exe:64K] Zeros Before: %.1f%% -> Zeros After: %.1f%% (Delta Zeros: %+d)",
			float64(zerosBefore)*100/65536, float64(zerosAfter)*100/65536, zerosAfter-zerosBefore)
	}
}

// BenchmarkDecompressMahler_Throughput measures the decompression throughput of
// the 2-adic Mahler spectral transform.
func BenchmarkDecompressMahler_Throughput(b *testing.B) {
	n := 64 * 1024
	orig := make([]byte, n)
	rand.Read(orig)

	compressed := CompressMahler(orig)
	dst := make([]byte, n)

	b.SetBytes(int64(n))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if _, err := DecompressMahler(compressed, dst); err != nil {
			b.Fatalf("Decompress failed: %v", err)
		}
	}
}
