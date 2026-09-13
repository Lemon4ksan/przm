// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestZeroSchema_GGAT_PeriodDetection(t *testing.T) {
	testStrides := []int{4, 8, 16, 32, 64}
	numRows := 128

	for _, s := range testStrides {
		data := make([]byte, s*numRows)
		for r := 0; r < numRows; r++ {
			row := data[r*s : (r+1)*s]
			numConst := s / 4
			if numConst < 1 {
				numConst = 1
			}
			for j := 0; j < numConst; j++ {
				row[j] = byte(0xA0 + j)
			}
			for j := numConst; j < s; j++ {
				row[j] = byte(r*7 + j*19)
			}
		}

		detected, conf := ComputeGGAT(data, 64)
		if detected != s {
			t.Errorf("expected stride %d, got %d (confidence: %.3f)", s, detected, conf)
		}
		if conf < 0.15 {
			t.Errorf("stride %d: confidence too low: %.3f", s, conf)
		}
	}
}

func TestZeroSchema_Theorem13_SpectralSegregation(t *testing.T) {
	// Construct a synthetic 16-byte record schema with:
	// - Cols 0..3: Quasi-constants ('{', '"', 'i', 'd')
	// - Cols 4..7: Monotonic counter (linear progression with constant deltas)
	// - Cols 8..11: Correlated tensor (Galois rank-1 projection u * v^T)
	// - Cols 12..15: High-entropy payload (pseudo-random bytes)
	stride := 16
	numRows := 256
	totalBytes := stride * numRows
	data := make([]byte, totalBytes)

	uRank := make([]byte, numRows)
	rand.Read(uRank)
	vRank := []byte{0x03, 0x07, 0x0F, 0x1F}

	for r := 0; r < numRows; r++ {
		row := data[r*stride : (r+1)*stride]

		// 1. Const cols
		row[0] = '{'
		row[1] = '"'
		row[2] = 'i'
		row[3] = 'd'

		// 2. Affine counter cols (high entropy, but constant delta -> diff-entropy = 0)
		row[4] = byte(r)
		row[5] = byte(r*3 + 1)
		row[6] = byte(r*7 + 5)
		row[7] = byte(r*11 + 9)

		// 3. Tensor rank-1 cols
		for k := 0; k < 4; k++ {
			row[8+k] = GF256Mul(uRank[r], vRank[k])
		}

		// 4. Entropy cols
		for k := 0; k < 4; k++ {
			row[12+k] = byte((r*1103515245 + 12345 + k*73) >> 16)
		}
	}

	schema, err := SynthesizeStructure(data)
	if err != nil {
		t.Fatalf("SynthesizeStructure failed: %v", err)
	}

	if schema.Stride != stride {
		t.Fatalf("expected stride %d, got %d", stride, schema.Stride)
	}

	// Verify column classifications
	hasConst := false
	hasAffine := false
	hasTensor := false

	for _, c := range schema.Clusters {
		switch c.Type {
		case ChannelConst:
			hasConst = true
			for _, idx := range c.ColumnIndices {
				if idx >= 4 {
					t.Errorf("const cluster captured non-const col %d", idx)
				}
			}
		case ChannelAffine:
			hasAffine = true
		case ChannelTensor, ChannelEntropy:
			hasTensor = true
		}
	}

	if !hasConst {
		t.Errorf("expected ChannelConst cluster")
	}
	if !hasAffine {
		t.Errorf("expected ChannelAffine cluster")
	}
	if !hasTensor {
		t.Errorf("expected ChannelTensor cluster")
	}

	// Theorem 13 Verification: Channel segregation reduces total conditional entropy
	rawEntropy := computeEntropy8(data)
	t.Logf("Raw 1D stream entropy: %.3f bits/symbol", rawEntropy)

	totalChannelEntropy := 0.0
	totalCols := 0
	for _, c := range schema.Clusters {
		t.Logf("Cluster Type %d: cols %v, mean entropy: %.3f bits/symbol", c.Type, c.ColumnIndices, c.Entropy)
		totalChannelEntropy += c.Entropy * float64(len(c.ColumnIndices))
		totalCols += len(c.ColumnIndices)
	}
	meanChannelEntropy := totalChannelEntropy / float64(totalCols)
	t.Logf("Segregated mean channel entropy: %.3f bits/symbol", meanChannelEntropy)

	if meanChannelEntropy >= rawEntropy {
		t.Errorf("Theorem 13 violated: channel entropy (%.3f) >= raw entropy (%.3f)", meanChannelEntropy, rawEntropy)
	}
}

func TestZeroSchema_CorpusExtraction(t *testing.T) {
	// Test on real JSON, Tabular, and Random corpus
	jsonSample := bytes.Repeat([]byte(`{"id":1001,"status":"ok","sensor":12.34,"flag":true}`+"\n"), 64)
	schemaJSON, err := SynthesizeStructure(jsonSample)
	if err != nil {
		t.Fatalf("JSON synthesis failed: %v", err)
	}
	t.Logf("JSON sample: detected stride %d, clusters %d, confidence: %.3f", schemaJSON.Stride, len(schemaJSON.Clusters), schemaJSON.Confidence)

	// Random data bypass
	noise := make([]byte, 4096)
	rand.Read(noise)
	schemaNoise, err := SynthesizeStructure(noise)
	if err != nil {
		t.Fatalf("Noise synthesis failed: %v", err)
	}
	if schemaNoise.Stride != 1 || len(schemaNoise.Clusters) != 1 {
		t.Errorf("Noise should not be partitioned: stride %d, clusters %d", schemaNoise.Stride, len(schemaNoise.Clusters))
	}
	t.Logf("Noise sample: correctly bypassed as single entropy channel (confidence: %.3f)", schemaNoise.Confidence)
}
