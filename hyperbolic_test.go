// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"strings"
	"testing"
)

func TestHyperbolic_Roundtrip(t *testing.T) {
	testCases := []struct {
		name string
		raw  string
	}{
		{"Empty", ""},
		{"FlatJSON", `{"name":"przm","version":2,"active":true}`},
		{"NestedBrackets", `{{{hello world}}}`},
		{"DeepAST", `(program (defun foo (x) (if (> x 0) (* x 2) (- x 1))))`},
		{"DeepNesting32", strings.Repeat("[", 32) + "payload" + strings.Repeat("]", 32)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rawBytes := []byte(tc.raw)
			tree, err := EmbedPoincareTree(rawBytes)
			if err != nil {
				t.Fatalf("EmbedPoincareTree failed: %v", err)
			}

			encoded := EncodeHyperbolic(tree)
			decoded, err := DecodeHyperbolic(encoded)
			if err != nil {
				t.Fatalf("DecodeHyperbolic failed: %v", err)
			}

			if !bytes.Equal(rawBytes, decoded) {
				t.Fatalf("Mismatch: got %q, want %q", string(decoded), tc.raw)
			}
		})
	}
}

func TestHyperbolic_Theorem21_MetricProperties(t *testing.T) {
	root := PoincareNode{Radius: 0, Angle: 0, Type: '{'}
	child1 := PoincareNode{Radius: 256, Angle: 1000, Type: '{'}
	child2 := PoincareNode{Radius: 256, Angle: 5000, Type: '{'}
	leaf := PoincareNode{Radius: 512, Angle: 1100, Type: 0xFE}

	dRootChild1 := HyperbolicMetricDistance(root, child1)
	dRootChild2 := HyperbolicMetricDistance(root, child2)
	dChild1Leaf := HyperbolicMetricDistance(child1, leaf)
	dRootLeaf := HyperbolicMetricDistance(root, leaf)

	if dRootChild1 == 0 || dRootChild2 == 0 {
		t.Fatalf("Distance from root should be non-zero")
	}

	// Triangle inequality check: d(root, leaf) <= d(root, child1) + d(child1, leaf)
	if dRootLeaf > dRootChild1+dChild1Leaf+10 {
		t.Fatalf("Hyperbolic metric violates relaxed triangle inequality: %d > %d + %d",
			dRootLeaf, dRootChild1, dChild1Leaf)
	}
}

// TestHyperbolic_MoebiusReflections verifies conformal Möbius reflection in the Poincaré disk.
func TestHyperbolic_MoebiusReflections(t *testing.T) {
	// 1. Check mapping point to origin: phi_a(a) == 0
	a := ComplexFixed{Re: 4096, Im: 2048}
	origin := MoebiusReflect(a, a)
	if origin.Re != 0 || origin.Im != 0 {
		t.Fatalf("MoebiusReflect(a, a) must map to origin (0, 0), got (%d, %d)", origin.Re, origin.Im)
	}

	// 2. Check inverse reflection: phi_{-a}(phi_a(z)) approx z
	z := ComplexFixed{Re: 1024, Im: 3072}
	mapped := MoebiusReflect(a, z)
	invA := ComplexFixed{Re: -a.Re, Im: -a.Im}
	recovered := MoebiusReflect(invA, mapped)

	diffRe := recovered.Re - z.Re
	if diffRe < 0 {
		diffRe = -diffRe
	}
	diffIm := recovered.Im - z.Im
	if diffIm < 0 {
		diffIm = -diffIm
	}

	// Fixed point discretization tolerance <= 8 out of 16384
	if diffRe > 8 || diffIm > 8 {
		t.Fatalf("Moebius inverse mismatch: orig (%d, %d), recovered (%d, %d)", z.Re, z.Im, recovered.Re, recovered.Im)
	}
}

// TestHyperbolic_HadamardSphericalCodes verifies pairwise distance >= 4 in 8D.
func TestHyperbolic_HadamardSphericalCodes(t *testing.T) {
	for i := 0; i < 8; i++ {
		cI := HadamardCodeword8(i)
		for j := i + 1; j < 8; j++ {
			cJ := HadamardCodeword8(j)
			dist := HammingDistance8(cI, cJ)
			if dist < 4 {
				t.Fatalf("Hadamard distance between %d and %d is %d (< 4)", i, j, dist)
			}
		}
	}
}

func BenchmarkHyperbolic_DecodeThroughput(b *testing.B) {
	raw := `{"status":"ok","code":200,"items":[{"id":1,"name":"alpha"},{"id":2,"name":"beta"}]}`
	tree, err := EmbedPoincareTree([]byte(raw))
	if err != nil {
		b.Fatal(err)
	}
	encoded := EncodeHyperbolic(tree)

	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := DecodeHyperbolic(encoded)
		if err != nil {
			b.Fatal(err)
		}
	}
}
