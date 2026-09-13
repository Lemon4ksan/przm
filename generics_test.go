// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"testing"
)

func TestMinNonEmpty(t *testing.T) {
	c1 := []byte{1, 2, 3, 4, 5}
	c2 := []byte{1, 2}
	c3 := []byte{1, 2, 3}
	var cNil []byte
	cEmpty := []byte{}

	best := MinNonEmpty(cNil, cEmpty, c1, c2, c3)
	if !bytes.Equal(best, c2) {
		t.Fatalf("expected c2, got %v", best)
	}

	if MinNonEmpty[byte](nil, nil) != nil {
		t.Fatalf("expected nil for all empty inputs")
	}
}

func TestMinBy(t *testing.T) {
	type item struct {
		name string
		cost int
	}
	items := []item{
		{"a", 50},
		{"b", 20},
		{"c", 35},
	}
	best, ok := MinBy(items, func(it item) int { return it.cost })
	if !ok || best.name != "b" {
		t.Fatalf("expected 'b', got %+v", best)
	}

	_, ok = MinBy([]item{}, func(it item) int { return it.cost })
	if ok {
		t.Fatalf("expected false for empty items")
	}
}
