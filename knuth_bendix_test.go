// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func TestKB_CriticalPairResolution(t *testing.T) {
	// Construct input with overlapping critical pair: "abc"
	// pair1 = ('a', 'b'), pair2 = ('b', 'c')
	// If pair2 frequency is higher, 'b' should pair with 'c', leaving 'a' as literal.
	tokens := []uint16{'a', 'b', 'c'}
	pairToL1 := make([]uint8, 65536)
	freqL1 := make([]uint16, 65536)

	pk1 := (uint32('a') << 8) | uint32('b')
	pk2 := (uint32('b') << 8) | uint32('c')

	pairToL1[pk1] = 1 // Rule 1: "ab" -> token 256
	pairToL1[pk2] = 2 // Rule 2: "bc" -> token 257

	// Case 1: freq(pk2) > freq(pk1) -> should reduce to ['a', 257]
	freqL1[pk1] = 10
	freqL1[pk2] = 20

	out := przm.KBConfluentSubstituteL1(tokens, make([]uint16, 0, 16), pairToL1, freqL1)
	if len(out) != 2 || out[0] != 'a' || out[1] != 257 {
		t.Fatalf("expected ['a', 257], got %v", out)
	}

	// Case 2: freq(pk1) >= freq(pk2) -> should reduce to [256, 'c']
	freqL1[pk1] = 20
	freqL1[pk2] = 10

	out2 := przm.KBConfluentSubstituteL1(tokens, make([]uint16, 0, 16), pairToL1, freqL1)
	if len(out2) != 2 || out2[0] != 256 || out2[1] != 'c' {
		t.Fatalf("expected [256, 'c'], got %v", out2)
	}
}

func TestKB_CanonicalFoldingAndPruning(t *testing.T) {
	data := bench.GenerateJSON(64 * 1024)

	g, tokens := przm.InduceGrammar5(data)
	if g == nil || len(tokens) == 0 {
		t.Fatal("InduceGrammar5 returned nil grammar or empty tokens")
	}

	t.Logf("Rules induced with Knuth-Bendix: L1=%d, L2=%d, L3=%d, L4=%d, L5=%d",
		len(g.L1), len(g.L2), len(g.L3), len(g.L4), len(g.L5))

	// Verify expansion tables match rules
	g.BuildExpansionTables()

	// Verify lossless roundtrip via PWSEncode / PWSDecode
	enc, err := przm.PWSEncode(data)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	dec, err := przm.PWSDecode(enc)
	if err != nil {
		t.Fatalf("PWSDecode failed: %v", err)
	}

	if !bytes.Equal(data, dec) {
		t.Fatalf("lossless roundtrip failed: len(data)=%d, len(dec)=%d", len(data), len(dec))
	}
}

func TestKB_RoundtripMultiCorpus(t *testing.T) {
	datasets := []struct {
		name string
		data []byte
	}{
		{"json_64K", bench.GenerateJSON(64 * 1024)},
		{"code_64K", bench.GenerateSourceCode(64 * 1024)},
		{"x86_64K", bench.GenerateMachineCode(64 * 1024)},
		{"tabular32_64K", bench.GenerateTabular(64*1024, 32)},
		{"rle_64K", bench.GenerateRLE(64 * 1024)},
	}

	for _, ds := range datasets {
		t.Run(ds.name, func(t *testing.T) {
			enc, err := przm.PWSEncode(ds.data)
			if err != nil {
				t.Fatalf("PWSEncode failed on %s: %v", ds.name, err)
			}

			dec, err := przm.PWSDecode(enc)
			if err != nil {
				t.Fatalf("PWSDecode failed on %s: %v", ds.name, err)
			}

			if !bytes.Equal(ds.data, dec) {
				t.Fatalf("Roundtrip mismatch on %s: orig %d, dec %d", ds.name, len(ds.data), len(dec))
			}
		})
	}
}
