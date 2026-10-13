// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/seal/codec/compress/brotli"
)

func TestAnalyzer_StandardFrame(t *testing.T) {
	data, err := os.ReadFile("compress.go")
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	comp, err := przm.Compress(data, 5)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	analysis, err := przm.AnalyzeFrame(comp)
	if err != nil {
		t.Fatalf("AnalyzeFrame failed: %v", err)
	}

	if analysis.UncompressedSize != len(data) {
		t.Errorf("got UncompressedSize=%d, want %d", analysis.UncompressedSize, len(data))
	}
	if analysis.CompressedSize != len(comp) {
		t.Errorf("got CompressedSize=%d, want %d", analysis.CompressedSize, len(comp))
	}
	if analysis.Ratio <= 1.0 {
		t.Errorf("expected compression ratio > 1.0, got %.2f", analysis.Ratio)
	}
	if analysis.TotalMatches == 0 {
		t.Errorf("expected > 0 matches, got 0")
	}
	if analysis.CmdSize == 0 {
		t.Errorf("expected > 0 CmdSize, got 0")
	}

	// Verify JSON serialization
	jsonStr := analysis.JSON()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		t.Fatalf("JSON output invalid: %v", err)
	}

	// Verify SummaryString rendering
	summary := analysis.SummaryString()
	if len(summary) == 0 {
		t.Fatal("SummaryString returned empty string")
	}
	t.Log("\n" + summary)
}

func TestAnalyzer_DictionaryFrame(t *testing.T) {
	brotliDict := brotli.StaticDictionaryData()
	przm.RegisterRawDictionary(przm.DictIDBrotliWeb, brotliDict)
	defer przm.UnregisterRawDictionary(przm.DictIDBrotliWeb)

	data, err := os.ReadFile("optimal.go")
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	opts := przm.CompressOptions{
		Level:       5,
		DictID:      przm.DictIDBrotliWeb,
		RawDict:     brotliDict,
		CompactDict: false,
		MinMatch:    3,
	}

	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions failed: %v", err)
	}

	analysis, err := przm.AnalyzeFrame(comp)
	if err != nil {
		t.Fatalf("AnalyzeFrame failed: %v", err)
	}

	if analysis.DictID != przm.DictIDBrotliWeb {
		t.Errorf("expected DictID=0x%08X, got 0x%08X", przm.DictIDBrotliWeb, analysis.DictID)
	}
	if analysis.CatRFCTransCount == 0 && analysis.CatTokenPairCount == 0 {
		t.Log("Note: No RFC dictionary hits or token pairs on optimal.go, checking matches")
	}

	t.Log("\n" + analysis.SummaryString())
}

func TestAnalyzer_CorruptedFrame(t *testing.T) {
	// Frame too small
	_, err := przm.AnalyzeFrame([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error on truncated frame, got nil")
	}

	// Invalid magic
	badMagic := make([]byte, 32)
	_, err = przm.AnalyzeFrame(badMagic)
	if err == nil {
		t.Fatal("expected error on invalid magic, got nil")
	}
}
