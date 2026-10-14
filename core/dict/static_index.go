// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dict

import (
	"bytes"
	"encoding/binary"

	"github.com/lemon4ksan/przm/core/internal/tables"
)

// HashMultiplier32 matches the PRZM match-finder hash multiplier.
const HashMultiplier32 = uint32(0x1E35A7BD)

// StaticDictEntry represents a pre-indexed static dictionary entry.
type StaticDictEntry struct {
	Prefix uint32
	P      uint16
	Len    uint8
	Next   int32
	Word   []byte
}

var (
	staticDictHead    [65536]int32
	staticDictEntries []StaticDictEntry
)

func init() {
	initStaticDictIndex()
}

func initStaticDictIndex() {
	for i := range staticDictHead {
		staticDictHead[i] = -1
	}

	// Maximum capacity: 13504 Brotli + 512 TopRFC + 2048 CompactPairs = 16064
	staticDictEntries = make([]StaticDictEntry, 0, 16064)

	// Buffer for transformed compact pairs
	var compactWordsBuf []byte
	var scratch [256]byte

	// 1. Index BrotliDictionary words (wid 128..13503)
	for wid := uint32(128); wid < 13504; wid++ {
		w := LookupBrotliWord(wid)
		if len(w) >= 4 {
			insertStaticEntry(w, uint16(wid))
		}
	}

	// 2. Index TopRFCWords (wid 13504..14015)
	for i := range tables.TopRFCWords {
		w := []byte(tables.TopRFCWords[i])
		if len(w) >= 4 {
			insertStaticEntry(w, uint16(13504+i))
		}
	}

	// 3. Index CompactPairs (c 0..2047, p = 14016 + c)
	// CompactPairs are high-frequency RFC pairs; inserting them last puts them
	// at the front of each bucket chain (LIFO) for fastest discovery.
	for c := 0; c < len(tables.CompactPairs); c++ {
		wPtr, tau, err := resolveCompactPair(uint32(c))
		if err != nil {
			continue
		}
		var tmp [256]byte
		pos := 0
		if err := ApplyTransform(tmp[:], &pos, 256, wPtr, tau, scratch[:]); err != nil || pos < 4 {
			continue
		}
		start := len(compactWordsBuf)
		compactWordsBuf = append(compactWordsBuf, tmp[:pos]...)
		wordSlice := compactWordsBuf[start : start+pos]
		insertStaticEntry(wordSlice, uint16(14016+c))
	}
}

func insertStaticEntry(word []byte, p uint16) {
	prefix := binary.LittleEndian.Uint32(word[:4])
	h := (prefix * HashMultiplier32) >> 16

	// Deduplicate identical words within the same bucket
	for idx := staticDictHead[h]; idx >= 0; idx = staticDictEntries[idx].Next {
		e := &staticDictEntries[idx]
		if e.Prefix == prefix && int(e.Len) == len(word) && bytes.Equal(e.Word, word) {
			return
		}
	}

	entryIdx := int32(len(staticDictEntries))
	staticDictEntries = append(staticDictEntries, StaticDictEntry{
		Prefix: prefix,
		P:      p,
		Len:    uint8(len(word)),
		Next:   staticDictHead[h],
		Word:   word,
	})
	staticDictHead[h] = entryIdx
}

// FindStaticDictMatches searches for static RFC 7932 dictionary matches at the start of src.
// For each matching word of length L, it invokes fn with parameter p and length L.
// Zero dynamic allocations.
func FindStaticDictMatches(src []byte, fn func(p uint32, length int)) {
	if len(src) < 4 {
		return
	}
	prefix := binary.LittleEndian.Uint32(src[:4])
	h := (prefix * HashMultiplier32) >> 16
	idx := staticDictHead[h]
	depth := 0
	for idx >= 0 && depth < 32 {
		e := &staticDictEntries[idx]
		idx = e.Next
		depth++
		if e.Prefix != prefix {
			continue
		}
		l := int(e.Len)
		if l > len(src) {
			continue
		}
		if bytes.Equal(src[4:l], e.Word[4:l]) {
			fn(uint32(e.P), l)
		}
	}
}

// NumStaticDictEntries returns the total number of pre-indexed dictionary entries.
func NumStaticDictEntries() int {
	return len(staticDictEntries)
}
