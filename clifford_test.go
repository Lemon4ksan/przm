// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"testing"

	"github.com/lemon4ksan/przm"
)

func TestClifford_Theorem22_AlgebraicAxioms(t *testing.T) {
	// Identity multivector 1 (scalar 1, rest 0)
	var id przm.Multivector16
	id.C[0] = 1

	var m przm.Multivector16
	for i := range m.C {
		m.C[i] = uint8(i*17 + 3)
	}

	// 1. Identity multiplication: 1 * M == M
	prod1 := przm.GeometricProduct(id, m)
	prod2 := przm.GeometricProduct(m, id)
	if prod1 != m || prod2 != m {
		t.Fatalf("Identity axiom failed: 1*M != M")
	}

	// 2. Rotor construction and Invertibility
	// For testing, let R be an element with known inverse
	// Let R = e0 (index 1). In F_256, e0 * e0 = e0^2 = 1. So e0^-1 = e0!
	var rSym, rSymInv przm.Multivector16
	rSym.C[1] = 1    // e0
	rSymInv.C[1] = 1 // e0^-1 = e0
	checkId := przm.GeometricProduct(rSym, rSymInv)
	if checkId != id {
		t.Fatalf("Rotor inverse failed: R * R_inv != 1")
	}

	// 3. Sandwich Inversion Theorem 22: M == R_inv * (R * M * R_inv) * R
	rotated := przm.ApplyRotor(m, rSym, rSymInv)
	restored := przm.ApplyRotor(rotated, rSymInv, rSym)
	if restored != m {
		t.Fatalf("Theorem 22 Sandwich Invertibility failed: restored != orig")
	}

	t.Logf("Theorem 22 Clifford Rotor Axioms verified 100%%!")
}

func TestClifford_Roundtrip(t *testing.T) {
	var r, rInv przm.Multivector16
	r.C[1] = 1
	rInv.C[1] = 1

	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte((i*73 + 19) & 0xFF)
	}

	encoded := przm.EncodeClifford(data, r, rInv)
	decoded, err := przm.DecodeClifford(encoded)
	if err != nil {
		t.Fatalf("DecodeClifford failed: %v", err)
	}
	if !bytes.Equal(decoded, data) {
		t.Fatalf("Roundtrip mismatch: len %d vs %d", len(decoded), len(data))
	}
}

func BenchmarkClifford_DecodeThroughput(b *testing.B) {
	var r, rInv przm.Multivector16
	r.C[1] = 1
	rInv.C[1] = 1

	data := make([]byte, 64*1024)
	for i := range data {
		data[i] = byte((i * 37) & 0xFF)
	}
	encoded := przm.EncodeClifford(data, r, rInv)

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dec, err := przm.DecodeClifford(encoded)
		if err != nil {
			b.Fatal(err)
		}
		if len(dec) != len(data) {
			b.Fatal("length mismatch")
		}
	}
}
