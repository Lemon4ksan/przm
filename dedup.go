// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"math/bits"
)

// Block deduplication table configuration (PRZM-SPEC-0.5 Milestone 5).
const (
	DedupTableSize = 512
	dedupTableMask = DedupTableSize - 1
	dedupProbes    = 4
)

// dedupEntry records a 64-bit block hash and its 0-based block index.
type dedupEntry struct {
	hash     uint64
	blockIdx uint32
	valid    bool
}

// DedupTable is a fixed-capacity hash table designed for embedding in the
// compressor scratchpad to achieve zero GC heap allocations.
type DedupTable struct {
	entries [DedupTableSize]dedupEntry
}

// Reset clears all entries in the deduplication table without heap allocations.
func (t *DedupTable) Reset() {
	clear(t.entries[:])
}

// Probe checks if an aligned 64 KiB block with matching hash was previously indexed
// and performs byte-for-byte equality verification to guarantee zero collision hazard.
// Returns the matching block index and true if an exact duplicate is found.
func (t *DedupTable) Probe(hash uint64, src []byte, currPos int) (uint32, bool) {
	idx := int(hash & dedupTableMask)
	for i := 0; i < dedupProbes; i++ {
		slot := (idx + i) & dedupTableMask
		e := &t.entries[slot]
		if !e.valid {
			return 0, false
		}
		if e.hash == hash {
			refPos := int(e.blockIdx) * DedupBlockSize
			// Strict bounds validation: historical, non-overlapping, and capacity-safe
			if refPos >= 0 && refPos+DedupBlockSize <= currPos && currPos+DedupBlockSize <= len(src) {
				if bytes.Equal(src[currPos:currPos+DedupBlockSize], src[refPos:refPos+DedupBlockSize]) {
					return e.blockIdx, true
				}
			}
		}
	}
	return 0, false
}

// Insert registers an aligned 64 KiB block with its 64-bit hash and block index.
func (t *DedupTable) Insert(hash uint64, blockIdx uint32) {
	idx := int(hash & dedupTableMask)
	for i := 0; i < dedupProbes; i++ {
		slot := (idx + i) & dedupTableMask
		e := &t.entries[slot]
		if !e.valid || e.hash == hash {
			e.hash = hash
			e.blockIdx = blockIdx
			e.valid = true
			return
		}
	}
	// Replacement policy: overwrite initial probed slot
	e := &t.entries[idx]
	e.hash = hash
	e.blockIdx = blockIdx
	e.valid = true
}

// Hash64KBlock computes a rapid 64-bit polynomial hash for a 64 KiB block
// using 32-byte 4-way unrolled uint64 loads and rotation mixing.
func Hash64KBlock(block []byte) uint64 {
	const (
		p1 uint64 = 0x9E3779B185EBCA87
		p2 uint64 = 0xC2B2AE3D27D4EB4F
		p3 uint64 = 0x165667B19E3779F9
		p4 uint64 = 0x85EBCA77C2B2AE63
	)

	n := len(block)
	h1 := uint64(n) ^ p1
	h2 := uint64(n) ^ p2
	h3 := uint64(n) ^ p3
	h4 := uint64(n) ^ p4

	i := 0
	for ; i+32 <= n; i += 32 {
		v1 := binary.LittleEndian.Uint64(block[i : i+8])
		v2 := binary.LittleEndian.Uint64(block[i+8 : i+16])
		v3 := binary.LittleEndian.Uint64(block[i+16 : i+24])
		v4 := binary.LittleEndian.Uint64(block[i+24 : i+32])

		h1 = bits.RotateLeft64(h1^(v1*p1), 31) * p2
		h2 = bits.RotateLeft64(h2^(v2*p2), 27) * p3
		h3 = bits.RotateLeft64(h3^(v3*p3), 33) * p4
		h4 = bits.RotateLeft64(h4^(v4*p4), 29) * p1
	}
	h := h1 ^ bits.RotateLeft64(h2, 17) ^ bits.RotateLeft64(h3, 37) ^ bits.RotateLeft64(h4, 51)
	for ; i+8 <= n; i += 8 {
		v := binary.LittleEndian.Uint64(block[i : i+8])
		h = bits.RotateLeft64(h^(v*p2), 31) * p1
	}
	for ; i < n; i++ {
		h = bits.RotateLeft64(h^uint64(block[i]), 11) * p2
	}
	h ^= h >> 33
	h *= p2
	h ^= h >> 29
	h *= p1
	h ^= h >> 32
	return h
}
