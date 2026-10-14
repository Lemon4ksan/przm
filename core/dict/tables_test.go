// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dict

import (
	"bytes"
	"testing"
)

func TestBrotliDictionaryTables(t *testing.T) {
	if len(WordBase) != 22 {
		t.Fatalf("expected WordBase len 22, got %d", len(WordBase))
	}
	if len(DictOffsets) != 21 {
		t.Fatalf("expected DictOffsets len 21, got %d", len(DictOffsets))
	}
	if len(Transforms) != 121 {
		t.Fatalf("expected Transforms len 121, got %d", len(Transforms))
	}
	if len(CompactPairs) != 2048 {
		t.Fatalf("expected CompactPairs len 2048, got %d", len(CompactPairs))
	}
	if len(PSStrings) != 50 {
		t.Fatalf("expected PSStrings len 50, got %d", len(PSStrings))
	}
	if len(TopRFCWords) != 512 {
		t.Fatalf("expected TopRFCWords len 512, got %d", len(TopRFCWords))
	}
	if len(WebTokens) != 256 {
		t.Fatalf("expected WebTokens len 256, got %d", len(WebTokens))
	}
	if len(BrotliDictionary) != 122784 {
		t.Fatalf("expected BrotliDictionary len 122784, got %d", len(BrotliDictionary))
	}
}

func TestLookupBrotliWord(t *testing.T) {
	w0 := LookupBrotliWord(0)
	if string(w0) != "time" {
		t.Fatalf("expected word 0 to be 'time', got %q", string(w0))
	}
	w1 := LookupBrotliWord(1)
	if string(w1) != "down" {
		t.Fatalf("expected word 1 to be 'down', got %q", string(w1))
	}

	wLast := LookupBrotliWord(13503)
	if len(wLast) == 0 {
		t.Fatalf("expected word 13503 to be non-empty")
	}
}

func TestExternalDictMapper(t *testing.T) {
	mapper := NewExternalDictMapper(BrotliDictionary, WordBase[:], DictOffsets[:])
	w, ok := mapper.Lookup(0)
	if !ok || !bytes.Equal(w, []byte("time")) {
		t.Fatalf("expected mapper to lookup word 0 as 'time', got %q (ok=%v)", string(w), ok)
	}

	w1, ok1 := mapper.Lookup(1)
	if !ok1 || !bytes.Equal(w1, []byte("down")) {
		t.Fatalf("expected mapper to lookup word 1 as 'down', got %q (ok=%v)", string(w1), ok1)
	}

	// Non-existent word
	_, okNone := mapper.Lookup(999999)
	if okNone {
		t.Fatalf("expected lookup of out-of-range word to fail")
	}
}

func TestLookupBrotliWordZeroAllocations(t *testing.T) {
	// Warm up
	_ = LookupBrotliWord(0)
	_ = LookupBrotliWord(100)

	allocs := testing.AllocsPerRun(100, func() {
		_ = LookupBrotliWord(42)
	})
	if allocs != 0.00 {
		t.Fatalf("LookupBrotliWord allocated %f allocs/op (strictly 0.00 required!)", allocs)
	}
}
