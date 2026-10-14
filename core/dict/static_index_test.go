// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dict

import (
	"bytes"
	"testing"
)

func TestStaticIndexPrecomputed(t *testing.T) {
	n := NumStaticDictEntries()
	if n < 10000 {
		t.Fatalf("expected >= 10000 static dict entries, got %d", n)
	}
	t.Logf("Static dictionary entries indexed: %d", n)
}

func TestStaticIndexFindMatchesAndVerify(t *testing.T) {
	testPhrases := []string{
		"return value from function",
		"<html><body><div class=\"content\">",
		"http://schemas.xmlsoap.org/soap/envelope/",
		"constant variable declaration",
		"information technology system",
	}

	var scratch [256]byte
	for _, phrase := range testPhrases {
		b := []byte(phrase)
		matchesFound := 0
		for i := 0; i+4 <= len(b); i++ {
			FindStaticDictMatches(b[i:], func(p uint32, matchLen int) {
				matchesFound++
				sub := b[i : i+matchLen]

				// Decompress using RFC 7932 decoder logic
				b0 := byte(0x80 | ((p >> 7) & 0x7F))
				b1 := byte(p & 0x7F)
				cmd := []byte{b0, b1}
				cp := 1
				wPtr, tau, err := ResolveRFCWord(b0, cmd, &cp)
				if err != nil {
					t.Fatalf("ResolveRFCWord failed for p=%d: %v", p, err)
				}
				var dst [256]byte
				pos := 0
				if err := ApplyTransform(dst[:], &pos, 256, wPtr, tau, scratch[:]); err != nil {
					t.Fatalf("ApplyTransform failed for p=%d: %v", p, err)
				}
				if !bytes.Equal(dst[:pos], sub) {
					t.Fatalf("Dictionary word decompressed mismatch: got %q, want %q", dst[:pos], sub)
				}
			})
		}
		if matchesFound == 0 {
			t.Fatalf("expected matches for phrase %q, found 0", phrase)
		}
		t.Logf("Phrase %q: %d dictionary matches found and verified", phrase, matchesFound)
	}
}

func TestStaticIndexZeroAllocs(t *testing.T) {
	buf := []byte("return value from function and http://example.com/api")
	allocs := testing.AllocsPerRun(1000, func() {
		for i := 0; i+4 <= len(buf); i++ {
			FindStaticDictMatches(buf[i:], func(p uint32, matchLen int) {
				_ = p
				_ = matchLen
			})
		}
	})
	if allocs != 0.00 {
		t.Fatalf("FindStaticDictMatches allocated %f allocs/run (strictly 0.00 required!)", allocs)
	}
}
