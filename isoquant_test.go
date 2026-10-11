// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/przm"
)

func TestIsoQuant_AlgebraIdentity(t *testing.T) {
	ident := przm.IdentityQuaternion4()
	q := przm.Quaternion4{0x42, 0x13, 0x99, 0xAA}

	res1 := q.Mul(ident)
	res2 := ident.Mul(q)
	res3 := przm.Mul(q, ident)

	if res1 != q || res2 != q || res3 != q {
		t.Fatalf("Identity multiplication failed: got %v, %v, %v, want %v", res1, res2, res3, q)
	}
}

func TestIsoQuant_AlgebraInversion(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ident := przm.IdentityQuaternion4()

	for range 1000 {
		q := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		inv, ok := q.Inv()
		if !ok {
			if q[0]^q[1]^q[2]^q[3] != 0 {
				t.Fatalf("Expected singular quaternion for sum 0")
			}
			continue
		}

		prod := q.Mul(inv)
		if prod != ident {
			t.Fatalf("Inverse failed for %v: q * q_inv = %v, want %v", q, prod, ident)
		}

		// Also verify package-level Inv
		invPkg := przm.Inv(q)
		if invPkg != inv {
			t.Fatalf("Package Inv mismatch for %v: got %v, want %v", q, invPkg, inv)
		}
	}
}

func TestIsoQuant_UnitInvolution(t *testing.T) {
	// Any unit quaternion where sum == 1 is its own inverse
	q := przm.Quaternion4{1, 0x55, 0x55, 0} // 1 ^ 0x55 ^ 0x55 ^ 0 == 1
	inv, ok := q.Inv()
	if !ok || inv != q {
		t.Fatalf("Unit quaternion involution failed: got inv %v, want %v", inv, q)
	}
}

func TestIsoQuant_CollimationRoundtripExact(t *testing.T) {
	src := make([]byte, 1024)
	for i := range src {
		src[i] = uint8(i & 0xFF)
	}

	qL := przm.Quaternion4{1, 0x33, 0x33, 0}
	qR := przm.Quaternion4{1, 0, 0x77, 0x77}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	rotated := make([]byte, len(src))
	przm.IsoQuantCollimation4D(rotated, src, qL, qR)

	recovered := make([]byte, len(src))
	przm.IsoQuantUncollimation4D(recovered, rotated, qLInv, qRInv)

	if !bytes.Equal(src, recovered) {
		t.Fatalf("Collimation roundtrip mismatch!")
	}

	// In-place uncollimation test
	inPlace := make([]byte, len(src))
	copy(inPlace, rotated)
	przm.IsoQuantUncollimation4DInplace(inPlace, qLInv, qRInv)
	if !bytes.Equal(src, inPlace) {
		t.Fatalf("In-place uncollimation mismatch!")
	}
}

func TestIsoQuant_Roundtrip_SyntheticMultiChannel(t *testing.T) {
	// Synthesize 4-channel RGBA image gradient with strong cross-channel correlation
	numPixels := 1024
	src := make([]byte, numPixels*4)
	for i := range numPixels {
		r := uint8(i % 256)
		g := uint8((i + 2) % 256)
		b := uint8((i + 4) % 256)
		a := uint8(255)
		src[i*4+0] = r
		src[i*4+1] = g
		src[i*4+2] = b
		src[i*4+3] = a
	}

	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		t.Fatalf("CompressIsoQuant failed: %v", err)
	}
	if len(compressed) == 0 {
		t.Fatalf("CompressIsoQuant returned empty buffer")
	}

	decompressed, err := przm.DecompressIsoQuant(compressed, nil)
	if err != nil {
		t.Fatalf("DecompressIsoQuant failed: %v", err)
	}

	if !bytes.Equal(src, decompressed) {
		t.Fatalf("Multi-channel roundtrip mismatch: got %d bytes, want %d bytes", len(decompressed), len(src))
	}

	// Also test global Decompress dispatch
	globalDec, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("Global Decompress failed: %v", err)
	}
	if !bytes.Equal(src, globalDec) {
		t.Fatalf("Global Decompress mismatch: got %d bytes, want %d bytes", len(globalDec), len(src))
	}
}

func TestIsoQuant_Roundtrip_Tabular4(t *testing.T) {
	// Tabular 4-column structured metrics
	numRows := 500
	src := make([]byte, numRows*4)
	for i := range numRows {
		src[i*4+0] = uint8(i % 100)
		src[i*4+1] = uint8((i * 2) % 100)
		src[i*4+2] = uint8((i * 3) % 100)
		src[i*4+3] = uint8((i * 4) % 100)
	}

	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		t.Fatalf("CompressIsoQuant failed: %v", err)
	}
	decompressed, err := przm.DecompressIsoQuant(compressed, nil)
	if err != nil {
		t.Fatalf("Tabular-4 roundtrip failed: %v", err)
	}
	if !bytes.Equal(src, decompressed) {
		t.Fatalf("Tabular-4 data mismatch")
	}
}

func TestIsoQuant_Roundtrip_ShaderSPIRV(t *testing.T) {
	// SPIR-V Header + float4 uniforms
	src := make([]byte, 2048)
	binary.LittleEndian.PutUint32(src[0:4], 0x07230203) // SPIR-V Magic
	binary.LittleEndian.PutUint32(src[4:8], 0x00010500) // Version 1.5
	for i := 8; i < len(src); i += 4 {
		src[i] = uint8(i % 64)
		src[i+1] = uint8((i + 1) % 64)
		src[i+2] = uint8((i + 2) % 64)
		src[i+3] = 0x3F // IEEE float exp ~ 1.0
	}

	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		t.Fatalf("CompressIsoQuant failed: %v", err)
	}
	decompressed, err := przm.DecompressIsoQuant(compressed, nil)
	if err != nil {
		t.Fatalf("Shader SPIR-V decompression failed: %v", err)
	}
	if !bytes.Equal(src, decompressed) {
		t.Fatalf("Shader SPIR-V data mismatch")
	}
}

func TestIsoQuant_Roundtrip_BinaryTrajectory(t *testing.T) {
	// 4D trajectory (x, y, z, t)
	numSteps := 400
	src := make([]byte, numSteps*4)
	for i := range numSteps {
		src[i*4+0] = uint8((i * 7) % 256)
		src[i*4+1] = uint8((i*7 + 10) % 256)
		src[i*4+2] = uint8((i*7 + 20) % 256)
		src[i*4+3] = uint8((i*7 + 30) % 256)
	}

	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		t.Fatalf("CompressIsoQuant failed: %v", err)
	}
	decompressed, err := przm.DecompressIsoQuant(compressed, nil)
	if err != nil {
		t.Fatalf("Binary trajectory decompression failed: %v", err)
	}
	if !bytes.Equal(src, decompressed) {
		t.Fatalf("Binary trajectory data mismatch")
	}
}

func TestIsoQuant_BoundaryLengths(t *testing.T) {
	testSizes := []int{0, 1, 2, 3, 4, 15, 16, 63, 64, 65, 127, 128, 255, 1023}
	for _, sz := range testSizes {
		src := make([]byte, sz)
		for i := range src {
			src[i] = uint8((i*7 + 13) % 256)
		}

		compressed, err := przm.CompressIsoQuant(src)
		if err != nil {
			t.Fatalf("Boundary test size %d compression failed: %v", sz, err)
		}
		decompressed, err := przm.DecompressIsoQuant(compressed, nil)
		if err != nil {
			t.Fatalf("Boundary test size %d decompression failed: %v", sz, err)
		}
		if !bytes.Equal(src, decompressed) {
			t.Fatalf("Boundary test size %d data mismatch", sz)
		}
	}
}

func TestIsoQuant_ZeroAllocDecompression(t *testing.T) {
	src := make([]byte, 4096)
	for i := range src {
		src[i] = uint8((i * 3) & 0xFF)
	}

	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}
	dst := make([]byte, len(src)+64)

	// Measure heap allocations per decompress run
	allocs := testing.AllocsPerRun(100, func() {
		_, err := przm.DecompressIsoQuant(compressed, dst)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("Decompression invariant violated: expected 0 allocs/op, got %v", allocs)
	}
}

func TestIsoQuant_CompressionRatioAdvantage(t *testing.T) {
	// Synthesize 4-channel image data with heavy channel cross-correlation
	numPixels := 4096
	src := make([]byte, numPixels*4)
	for i := range numPixels {
		baseVal := uint8(i % 128)
		src[i*4+0] = baseVal
		src[i*4+1] = baseVal ^ 0x05
		src[i*4+2] = baseVal ^ 0x0A
		src[i*4+3] = 0xFF // Constant alpha
	}

	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		t.Fatalf("CompressIsoQuant failed: %v", err)
	}

	ratio := float64(len(src)) / float64(len(compressed))
	t.Logf(
		"IsoQuant Multi-channel compression ratio: %.2fx (raw: %d, compressed: %d)",
		ratio,
		len(src),
		len(compressed),
	)

	if len(compressed) >= len(src) {
		t.Fatalf(
			"Expected compression advantage on correlated multi-channel stream, got %d vs raw %d",
			len(compressed),
			len(src),
		)
	}
}

func BenchmarkIsoQuant_ForwardCollimation(b *testing.B) {
	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	qL := przm.Quaternion4{1, 0x33, 0x33, 0}
	qR := przm.Quaternion4{1, 0, 0x77, 0x77}

	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		przm.IsoQuantCollimation4D(dst, src, qL, qR)
	}
}

func BenchmarkIsoQuant_InverseUncollimationInplace(b *testing.B) {
	buf := make([]byte, 65536)
	qLInv := przm.Quaternion4{1, 0x33, 0x33, 0}
	qRInv := przm.Quaternion4{1, 0, 0x77, 0x77}

	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		przm.IsoQuantUncollimation4DInplace(buf, qLInv, qRInv)
	}
}

func BenchmarkIsoQuant_DecompressZeroAlloc(b *testing.B) {
	src := make([]byte, 65536)
	for i := 0; i < len(src); i += 4 {
		src[i] = uint8(i % 128)
		src[i+1] = uint8((i + 1) % 128)
		src[i+2] = uint8((i + 2) % 128)
		src[i+3] = 0xFF
	}
	compressed, err := przm.CompressIsoQuant(src)
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, len(src)+64)

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := przm.DecompressIsoQuant(compressed, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}
