package przm

import (
	"bytes"
	"crypto/rand"
	"testing"
)

// TestTDA_Theorem23_BoundaryAnnihilation verifies that d1(d2(C)) == 0 identically for all C in C2 over F2.
func TestTDA_Theorem23_BoundaryAnnihilation(t *testing.T) {
	testSizes := [][2]int{
		{4, 4},
		{8, 8},
		{16, 16},
		{32, 16},
	}

	for _, sz := range testSizes {
		h, w := sz[0], sz[1]
		grid := NewSimplicialGrid(h, w)
		totalFaces := h * w

		// Test with all-ones faces
		allOnes := make([]uint8, totalFaces)
		for i := range allOnes {
			allOnes[i] = 1
		}
		hE, vE := grid.Boundary2(allOnes)
		v0 := grid.Boundary1(hE, vE)
		for idx, val := range v0 {
			if val != 0 {
				t.Fatalf("Theorem 23 violated (AllOnes %dx%d): vertex %d is non-zero (%d)", h, w, idx, val)
			}
		}

		// Test with pseudo-random 2-chains
		rndFaces := make([]uint8, totalFaces)
		for iter := 0; iter < 10; iter++ {
			rand.Read(rndFaces)
			for i := range rndFaces {
				rndFaces[i] &= 1
			}

			hE, vE = grid.Boundary2(rndFaces)
			v0 = grid.Boundary1(hE, vE)
			for idx, val := range v0 {
				if val != 0 {
					t.Fatalf("Theorem 23 violated (Iter %d, %dx%d): vertex %d is non-zero (%d)", iter, h, w, idx, val)
				}
			}
		}
	}
}

// TestTDA_Roundtrip verifies exact lossless compression and decompression.
func TestTDA_Roundtrip(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"Empty", []byte{}},
		{"Small", []byte("Hello, TDA World!")},
		{"PeriodicBarcodes", bytes.Repeat([]byte("CYCLIC_PERSISTENCE_VECTOR_01234567"), 128)},
		{"StructuredLog", bytes.Repeat([]byte("2026-09-05 15:30:00 [INFO] Worker process heartbeat ok status=200\n"), 64)},
		{"SparseZeros", append(make([]byte, 1024), []byte("essential feature")...)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			enc := CompressTDA(tc.data)
			dec, err := DecompressTDA(enc, nil)
			if err != nil {
				t.Fatalf("DecompressTDA error: %v", err)
			}
			if !bytes.Equal(tc.data, dec) {
				t.Fatalf("DecompressTDA mismatch: orig len %d, dec len %d", len(tc.data), len(dec))
			}
		})
	}
}

// TestTDA_WorstCaseNoise verifies that random noise does not excessively expand.
func TestTDA_WorstCaseNoise(t *testing.T) {
	noise := make([]byte, 65536)
	rand.Read(noise)

	enc := CompressTDA(noise)
	// Raw bypass max expansion is 9 bytes
	if len(enc) > len(noise)+9 {
		t.Fatalf("TDA noise expansion too large: %d vs %d", len(enc), len(noise))
	}

	dec, err := DecompressTDA(enc, nil)
	if err != nil {
		t.Fatalf("DecompressTDA error: %v", err)
	}
	if !bytes.Equal(noise, dec) {
		t.Fatalf("DecompressTDA noise mismatch")
	}
}

// BenchmarkTDADecompress measures decompression throughput and allocations.
func BenchmarkTDADecompress(b *testing.B) {
	data := bytes.Repeat([]byte("TOPOLOGICAL_PERSISTENT_HOMOLOGY_F2_CYCLE_FILLING_BARCODE_20260905"), 200) // ~13 KB
	enc := CompressTDA(data)

	dst := make([]byte, len(data))
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := DecompressTDA(enc, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestRipser_CombinatorialIndexing verifies bijective mapping between simplices and integer indices.
func TestRipser_CombinatorialIndexing(t *testing.T) {
	// 1-simplices (edges)
	for i := 1; i < 20; i++ {
		for j := 0; j < i; j++ {
			orig := []int{i, j}
			idx := SimplexToCombinatorialIndex(orig)
			rec := CombinatorialIndexToSimplex(idx, 1)
			if rec[0] != i || rec[1] != j {
				t.Fatalf("1-simplex roundtrip failed: orig %v, rec %v", orig, rec)
			}
		}
	}

	// 2-simplices (triangles)
	for i := 2; i < 15; i++ {
		for j := 1; j < i; j++ {
			for k := 0; k < j; k++ {
				orig := []int{i, j, k}
				idx := SimplexToCombinatorialIndex(orig)
				rec := CombinatorialIndexToSimplex(idx, 2)
				if rec[0] != i || rec[1] != j || rec[2] != k {
					t.Fatalf("2-simplex roundtrip failed: orig %v, rec %v", orig, rec)
				}
			}
		}
	}
}

// TestRipser_BoundaryConsistency verifies boundary of 2-simplex produces 3 facets.
func TestRipser_BoundaryConsistency(t *testing.T) {
	// Simplex {5, 3, 1}
	s := []int{5, 3, 1}
	n := SimplexToCombinatorialIndex(s)
	facets := SimplexBoundaryIndices(n, 2)

	if len(facets) != 3 {
		t.Fatalf("Expected 3 facets for 2-simplex, got %d", len(facets))
	}

	// Facets should be {3, 1}, {5, 1}, {5, 3}
	expectedFacets := [][]int{{3, 1}, {5, 1}, {5, 3}}
	for i, fIdx := range facets {
		fVerts := CombinatorialIndexToSimplex(fIdx, 1)
		exp := expectedFacets[i]
		if fVerts[0] != exp[0] || fVerts[1] != exp[1] {
			t.Fatalf("Facet %d mismatch: got %v, want %v", i, fVerts, exp)
		}
	}
}
