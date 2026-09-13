// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// generateMacroData creates a dataset with repeated macro-chunks (4 KB – 16 KB)
// interspersed with unique data to simulate realistic multi-file archives.
func generateMacroData(totalSize int, numRepeats int) []byte {
	rng := rand.New(rand.NewSource(12345))
	data := make([]byte, totalSize)
	rng.Read(data)

	// Create 3 distinct 8 KB templates
	templateSize := 8192
	templates := make([][]byte, 3)
	for t := 0; t < 3; t++ {
		templates[t] = make([]byte, templateSize)
		rng.Read(templates[t])
	}

	// Stamp templates at random positions throughout data
	step := totalSize / (numRepeats + 1)
	for i := 0; i < numRepeats; i++ {
		pos := (i + 1) * step
		if pos+templateSize <= totalSize {
			tmpl := templates[i%len(templates)]
			copy(data[pos:pos+templateSize], tmpl)
		}
	}

	return data
}

func TestMacroTrellis_RoundtripBitExact(t *testing.T) {
	orig := generateMacroData(256*1024, 12)

	comp := przm.CompressMacroTrellis(orig)
	if len(comp) == 0 {
		t.Fatal("CompressMacroTrellis returned empty slice")
	}

	ratio := float64(len(orig)) / float64(len(comp))
	saved := (1.0 - float64(len(comp))/float64(len(orig))) * 100.0
	t.Logf("Macro-Trellis: Raw=%d B -> Comp=%d B (Ratio: %.2fx, Saved: +%.1f%%)",
		len(orig), len(comp), ratio, saved)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	res, err := przm.DecompressMacroTrellis(comp, dst)
	if err != nil {
		t.Fatalf("DecompressMacroTrellis failed: %v", err)
	}

	if !bytes.Equal(res, orig) {
		t.Fatal("Decompressed data does not match original!")
	}
}

func TestMacroTrellis_ZeroAllocDecompress(t *testing.T) {
	orig := generateMacroData(128*1024, 8)
	comp := przm.CompressMacroTrellis(orig)
	if len(comp) == 0 {
		t.Fatal("CompressMacroTrellis returned empty slice")
	}

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	// Correctness check
	res, err := przm.DecompressMacroTrellis(comp, dst)
	if err != nil || !bytes.Equal(res, orig) {
		t.Fatal("DecompressMacroTrellis verification failed")
	}

	// Allocation test
	allocs := testing.AllocsPerRun(100, func() {
		_, _ = przm.DecompressMacroTrellis(comp, dst)
	})

	if allocs > 0 {
		t.Fatalf("DecompressMacroTrellis allocated %v allocs/op, expected strictly 0", allocs)
	}
}

func BenchmarkMacroTrellis_Decompress(b *testing.B) {
	data := generateMacroData(512*1024, 25)
	comp := przm.CompressMacroTrellis(data)
	if len(comp) == 0 {
		b.Fatal("Compress failed")
	}

	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := przm.DecompressMacroTrellis(comp, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMacroTrellis_Compress(b *testing.B) {
	data := generateMacroData(512*1024, 25)

	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = przm.CompressMacroTrellis(data)
	}
}
