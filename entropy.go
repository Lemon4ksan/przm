// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
)

// Mode identifiers for Entropy-Pack
const (
	ModeFlat             uint8 = 0 // Raw uncompressed payload (0% CPU, O(1) slice copy)
	ModeHuffman4X        uint8 = 1 // 4-stream interleaved Table-Driven Canonical Huffman (RAM-speed focus: >10 GB/s)
	ModeHuffmanCompact   uint8 = 2 // Single-stream Table-Driven Canonical Huffman with 32B Active-Mask (<65B header)
	ModeHuffmanCompact4X uint8 = 3 // 4-stream interleaved Table-Driven Canonical Huffman with 32B Active-Mask
	ModeRecursiveLZ      uint8 = 4 // Recursive PRZM LZ77 frame (for structured, highly-repetitive command streams)
)

var (
	ErrCorruptedStream = errors.New("entropy_pack: corrupted stream")
)

// -----------------------------------------------------------------------------
// Canonical Huffman 4-Stream Interleaved Codec (RAM-Speed Architecture)
// -----------------------------------------------------------------------------

const (
	MaxHuffBits       = 11
	HuffTableSize     = 1 << MaxHuffBits // 2048 entries * 2 bytes = 4096 bytes (L1 cache resident)
	HeaderSizeHuffman = 1 + 4 + 128 + 16 // 1 (mode) + 4 (origLen) + 128 (lengths) + 16 (4*uint32 stream sizes) = 149 bytes
)

// HuffTableEntry packs (symbol, codeLen) into a 16-bit word.
// Low 8 bits: symbol (byte 0..255)
// High 8 bits: codeLen (1..11)
type HuffTableEntry uint16

func makeHuffEntry(sym byte, codeLen uint8) HuffTableEntry {
	return HuffTableEntry(uint16(sym) | (uint16(codeLen) << 8))
}

func (e HuffTableEntry) Symbol() byte {
	return byte(e & 0xFF)
}

func (e HuffTableEntry) CodeLen() uint8 {
	return uint8(e >> 8)
}

// CanonicalHuffmanTable contains the 4 KiB direct lookup table for O(1) branchless decode.
type CanonicalHuffmanTable struct {
	Table [HuffTableSize]HuffTableEntry
}

// BuildFromLengths populates the 2048-entry table from symbol bit-lengths.
// Returns false if any length exceeds MaxHuffBits or canonical construction fails.
func (h *CanonicalHuffmanTable) BuildFromLengths(lengths [256]uint8) bool {
	// 1. Count frequencies of each bit length
	var blCount [MaxHuffBits + 1]int
	for _, l := range lengths {
		if l > MaxHuffBits {
			return false
		}
		blCount[l]++
	}
	blCount[0] = 0 // Unused symbols must not offset codes

	// 2. Compute first code for each bit length (Canonical Huffman rule)
	var nextCode [MaxHuffBits + 1]uint16
	code := uint16(0)
	for b := 1; b <= MaxHuffBits; b++ {
		code = (code + uint16(blCount[b-1])) << 1
		nextCode[b] = code
	}

	// 3. Populate direct decode table
	// For each symbol with length L and canonical code C,
	// all table indices whose prefix matches C map to (sym, L).
	for sym := range 256 {
		l := lengths[sym]
		if l == 0 {
			continue
		}
		c := nextCode[l]
		nextCode[l]++

		entry := makeHuffEntry(byte(sym), l)
		shift := MaxHuffBits - l
		numEntries := 1 << shift
		baseIdx := int(c << shift)

		if baseIdx+numEntries > HuffTableSize {
			return false
		}

		for i := range numEntries {
			h.Table[baseIdx+i] = entry
		}
	}
	return true
}

// HuffBitWriter collects variable-length bit codes into a pre-allocated buffer.
type HuffBitWriter struct {
	buf   []byte // Pre-allocated destination buffer (capacity >= 32KB)
	pos   int    // Current byte offset in buf
	bits  uint64 // MSB-aligned bit accumulator
	nBits int    // Count of unwritten bits in bits (0..31)
}

// Reset re-initializes the writer to write into the provided pre-allocated buffer.
func (w *HuffBitWriter) Reset(buf []byte) {
	w.buf = buf
	w.pos = 0
	w.bits = 0
	w.nBits = 0
}

// Write emits a variable-length Huffman code (length <= 11) into the bitstream.
// CRITICAL: Bitstream word emission is strictly BigEndian (MSB-first).
// Because MaxHuffBits = 11 and w.nBits <= 31 initially, w.nBits + length <= 42.
// Therefore, w.nBits >= 32 can be true at most ONCE per call: replace 'for' with 'if'.
func (w *HuffBitWriter) Write(code uint16, length uint8) {
	w.bits |= uint64(code) << (64 - w.nBits - int(length))
	w.nBits += int(length)
	if w.nBits >= 32 {
		binary.BigEndian.PutUint32(w.buf[w.pos:w.pos+4], uint32(w.bits>>32))
		w.pos += 4
		w.bits <<= 32
		w.nBits -= 32
	}
}

// Flush flushes any remaining bits (0..31) as big-endian bytes with zero-padding on the LSBs.
func (w *HuffBitWriter) Flush() []byte {
	for w.nBits > 0 {
		w.buf[w.pos] = byte(w.bits >> 56)
		w.pos++
		w.bits <<= 8
		w.nBits -= 8
	}
	w.nBits = 0
	w.buf = w.buf[:w.pos]
	return w.buf
}

// Written returns the slice of bytes written so far.
func (w *HuffBitWriter) Written() []byte {
	return w.buf[:w.pos]
}

// HuffBitReader provides fast branchless bit extraction.
type HuffBitReader struct {
	src           []byte
	ptr           int
	bitContainer  uint64
	bitsRemaining int
	underflow     bool
}

func (r *HuffBitReader) Init(src []byte) {
	r.src = src
	r.ptr = 0
	r.bitContainer = 0
	r.bitsRemaining = 0
	r.underflow = false
	r.Refill()
}

func (r *HuffBitReader) Refill() {
	for r.bitsRemaining <= 32 && r.ptr < len(r.src) {
		r.bitContainer = (r.bitContainer << 8) | uint64(r.src[r.ptr])
		r.ptr++
		r.bitsRemaining += 8
	}
}

func (r *HuffBitReader) Peek(nbBits int) uint16 {
	if r.bitsRemaining < nbBits {
		r.Refill()
	}
	if r.bitsRemaining <= 0 {
		r.underflow = true
	}
	if r.bitsRemaining < nbBits {
		shift := nbBits - r.bitsRemaining
		return uint16((r.bitContainer << shift) & ((1 << nbBits) - 1))
	}
	shift := r.bitsRemaining - nbBits
	return uint16((r.bitContainer >> shift) & ((1 << nbBits) - 1))
}

func (r *HuffBitReader) Consume(nbBits int) {
	r.bitsRemaining -= nbBits
	if r.bitsRemaining < 0 {
		r.underflow = true
	}
}

// -----------------------------------------------------------------------------
// Huffman Tree Construction & 4-Stream Interleaving
// -----------------------------------------------------------------------------

type huffNode struct {
	left  int16
	right int16
}

type heapElem struct {
	weight uint32
	id     int16
}

func heapLess(a, b heapElem) bool {
	return a.weight < b.weight || (a.weight == b.weight && a.id < b.id)
}

func heapSiftDown(h []heapElem, i int) {
	n := len(h)
	for {
		smallest := i
		left := 2*i + 1
		right := 2*i + 2

		if left < n && heapLess(h[left], h[smallest]) {
			smallest = left
		}
		if right < n && heapLess(h[right], h[smallest]) {
			smallest = right
		}
		if smallest == i {
			break
		}
		h[i], h[smallest] = h[smallest], h[i]
		i = smallest
	}
}

// ComputeHuffmanLengths computes canonical bit-lengths bounded to MaxHuffBits (11).
func ComputeHuffmanLengths(src []byte) ([256]uint8, int) {
	var freqs [256]int32
	mat := BuildFrequencyMatrixSIMD(src)
	for i, c := range mat.Counts {
		freqs[i] = int32(c)
	}

	var lengths [256]uint8

	var nodes [512]huffNode
	var heap [256]heapElem
	var leafSym [256]uint8

	numSymbols := 0
	for s := range 256 {
		if freqs[s] > 0 {
			id := int16(numSymbols)
			leafSym[numSymbols] = uint8(s)
			heap[numSymbols] = heapElem{weight: uint32(freqs[s]), id: id}
			nodes[id] = huffNode{left: -1, right: -1}
			numSymbols++
		}
	}

	if numSymbols == 0 {
		return lengths, 0
	}
	if numSymbols == 1 {
		lengths[leafSym[0]] = 1
		return lengths, 1
	}

	// Floyd's O(N) in-place min-heap construction
	for i := (numSymbols / 2) - 1; i >= 0; i-- {
		heapSiftDown(heap[:numSymbols], i)
	}

	heapSize := numSymbols
	nextID := int16(numSymbols)

	for heapSize > 1 {
		// Extract first minimum
		e1 := heap[0]
		heapSize--
		heap[0] = heap[heapSize]
		heapSiftDown(heap[:heapSize], 0)

		// Extract second minimum
		e2 := heap[0]

		// Record merged node
		nodes[nextID] = huffNode{left: e1.id, right: e2.id}

		// Replace root with merged node and restore min-heap invariant
		heap[0] = heapElem{weight: e1.weight + e2.weight, id: nextID}
		heapSiftDown(heap[:heapSize], 0)

		nextID++
	}

	rootID := heap[0].id
	var depths [512]uint16

	// Iterative linear depth propagation from rootID down to 0
	for i := rootID; i >= 0; i-- {
		d := depths[i]
		l, r := nodes[i].left, nodes[i].right
		if l >= 0 {
			depths[l] = d + 1
		}
		if r >= 0 {
			depths[r] = d + 1
		}
	}

	// Compute leaf lengths and initial Kraft-McMillan sum
	kraftSum := 0
	for i := 0; i < numSymbols; i++ {
		s := leafSym[i]
		l := min(uint8(depths[i]), MaxHuffBits)
		if l == 0 {
			l = 1
		}
		lengths[s] = l
		kraftSum += 1 << (MaxHuffBits - l)
	}

	// Incremental Kraft-McMillan inequality enforcement
	for kraftSum > HuffTableSize {
		bestSym := -1
		minW := uint32(0xFFFFFFFF)
		for i := 0; i < numSymbols; i++ {
			s := int(leafSym[i])
			if lengths[s] < MaxHuffBits {
				if uint32(freqs[s]) < minW {
					minW = uint32(freqs[s])
					bestSym = s
				}
			}
		}
		if bestSym < 0 {
			break
		}
		kraftSum -= 1 << (MaxHuffBits - lengths[bestSym] - 1)
		lengths[bestSym]++
	}

	return lengths, numSymbols
}

// Pre-allocated scratchpad pool for 4-stream interleaved Huffman encoding.
type huffStreamScratchpad struct {
	streams [4][32768 + 64]byte
}

var huffScratchPool = sync.Pool{
	New: func() any { return new(huffStreamScratchpad) },
}

// EncodeHuffman4X packs a byte stream into 4 independent interleaved bit-streams.
func EncodeHuffman4X(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{ModeHuffman4X, 0, 0, 0, 0}, nil
	}

	lengths, active := ComputeHuffmanLengths(src)
	if active <= 1 {
		// Single symbol or empty: handle via flat fallback
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	// Compute canonical codes
	var blCount [MaxHuffBits + 1]int
	for _, l := range lengths {
		blCount[l]++
	}
	blCount[0] = 0 // Unused symbols must not offset codes
	var nextCode [MaxHuffBits + 1]uint16
	code := uint16(0)
	for b := 1; b <= MaxHuffBits; b++ {
		code = (code + uint16(blCount[b-1])) << 1
		nextCode[b] = code
	}

	// Table fusion: pack (code << 8) | length for branchless single-lookup symbol encoding
	var fused [256]uint32
	for s := range 256 {
		l := lengths[s]
		if l > 0 {
			c := nextCode[l]
			nextCode[l]++
			fused[s] = (uint32(c) << 8) | uint32(l)
		}
	}

	// Split input into 4 chunks
	chunkSize := (n + 3) / 4
	c0 := 0
	c1 := min(chunkSize, n)
	c2 := min(2*chunkSize, n)
	c3 := min(3*chunkSize, n)
	c4 := n

	chunks := [4][]byte{
		src[c0:c1],
		src[c1:c2],
		src[c2:c3],
		src[c3:c4],
	}

	pad := huffScratchPool.Get().(*huffStreamScratchpad)
	maxChunkBytes := ((chunkSize*MaxHuffBits + 31) / 8) + 8

	var writers [4]HuffBitWriter
	for i := range 4 {
		if maxChunkBytes <= len(pad.streams[i]) {
			writers[i].Reset(pad.streams[i][:])
		} else {
			writers[i].Reset(make([]byte, maxChunkBytes))
		}
		for _, sym := range chunks[i] {
			entry := fused[sym]
			writers[i].Write(uint16(entry>>8), uint8(entry))
		}
		writers[i].Flush()
	}

	s0 := writers[0].Written()
	s1 := writers[1].Written()
	s2 := writers[2].Written()
	s3 := writers[3].Written()

	totalSize := HeaderSizeHuffman + len(s0) + len(s1) + len(s2) + len(s3)
	out := make([]byte, totalSize)
	out[0] = ModeHuffman4X
	binary.LittleEndian.PutUint32(out[1:5], uint32(n))

	// Pack 256 lengths into 128 bytes (4 bits each, values 0..11)
	for i := 0; i < 256; i += 2 {
		out[5+i/2] = (lengths[i] & 0x0F) | ((lengths[i+1] & 0x0F) << 4)
	}

	// Write 4 stream lengths
	binary.LittleEndian.PutUint32(out[133:137], uint32(len(s0)))
	binary.LittleEndian.PutUint32(out[137:141], uint32(len(s1)))
	binary.LittleEndian.PutUint32(out[141:145], uint32(len(s2)))
	binary.LittleEndian.PutUint32(out[145:149], uint32(len(s3)))

	// Direct copy payloads
	pos := HeaderSizeHuffman
	copy(out[pos:], s0)
	pos += len(s0)
	copy(out[pos:], s1)
	pos += len(s1)
	copy(out[pos:], s2)
	pos += len(s2)
	copy(out[pos:], s3)

	huffScratchPool.Put(pad)
	return out, nil
}

// DecodeHuffman4X executes 4-way interleaved branchless decoding with 0 heap allocations.
func DecodeHuffman4X(dst []byte, src []byte, decTable *CanonicalHuffmanTable) (int, error) {
	if len(src) < 5 {
		return 0, ErrCorruptedStream
	}
	origLen := int(binary.LittleEndian.Uint32(src[1:5]))
	if origLen == 0 {
		return 0, nil
	}
	if len(dst) < origLen {
		return 0, ErrBufferTooSmall
	}

	if len(src) < HeaderSizeHuffman {
		return 0, ErrCorruptedStream
	}

	// 1. Unpack lengths
	var lengths [256]uint8
	for i := range 128 {
		b := src[5+i]
		lengths[i*2] = b & 0x0F
		lengths[i*2+1] = (b >> 4) & 0x0F
	}

	// 2. Build 4 KiB direct decode table
	if decTable == nil {
		var local CanonicalHuffmanTable
		decTable = &local
	}
	if !decTable.BuildFromLengths(lengths) {
		return 0, ErrCorruptedStream
	}

	// 3. Resolve 4 stream offsets
	sz0 := int(binary.LittleEndian.Uint32(src[133:137]))
	sz1 := int(binary.LittleEndian.Uint32(src[137:141]))
	sz2 := int(binary.LittleEndian.Uint32(src[141:145]))
	sz3 := int(binary.LittleEndian.Uint32(src[145:149]))

	totalPayload := sz0 + sz1 + sz2 + sz3
	if len(src) < HeaderSizeHuffman+totalPayload {
		return 0, ErrCorruptedStream
	}

	p0 := HeaderSizeHuffman
	p1 := p0 + sz0
	p2 := p1 + sz1
	p3 := p2 + sz2
	pEnd := p3 + sz3

	if pEnd > len(src) {
		return 0, ErrCorruptedStream
	}

	chunkSize := (origLen + 3) / 4
	c0Count := min(chunkSize, origLen)
	c1Count := min(chunkSize, max(0, origLen-chunkSize))
	c2Count := min(chunkSize, max(0, origLen-2*chunkSize))
	c3Count := max(0, origLen-3*chunkSize)

	dst0 := dst[0:c0Count]
	dst1 := dst[c0Count : c0Count+c1Count]
	dst2 := dst[c0Count+c1Count : c0Count+c1Count+c2Count]
	dst3 := dst[c0Count+c1Count+c2Count : origLen]

	var r0, r1, r2, r3 HuffBitReader
	r0.Init(src[p0:p1])
	r1.Init(src[p1:p2])
	r2.Init(src[p2:p3])
	r3.Init(src[p3:pEnd])

	// 4. Superscalar 4-way unrolled decode loop
	i0, i1, i2, i3 := 0, 0, 0, 0
	for i0 < c0Count && i1 < c1Count && i2 < c2Count && i3 < c3Count {
		// Lane 0
		e0 := decTable.Table[r0.Peek(MaxHuffBits)]
		dst0[i0] = e0.Symbol()
		r0.Consume(int(e0.CodeLen()))
		i0++

		// Lane 1
		e1 := decTable.Table[r1.Peek(MaxHuffBits)]
		dst1[i1] = e1.Symbol()
		r1.Consume(int(e1.CodeLen()))
		i1++

		// Lane 2
		e2 := decTable.Table[r2.Peek(MaxHuffBits)]
		dst2[i2] = e2.Symbol()
		r2.Consume(int(e2.CodeLen()))
		i2++

		// Lane 3
		e3 := decTable.Table[r3.Peek(MaxHuffBits)]
		dst3[i3] = e3.Symbol()
		r3.Consume(int(e3.CodeLen()))
		i3++
	}

	// Drain remaining elements in individual lanes
	for i0 < c0Count {
		e0 := decTable.Table[r0.Peek(MaxHuffBits)]
		dst0[i0] = e0.Symbol()
		r0.Consume(int(e0.CodeLen()))
		i0++
	}
	for i1 < c1Count {
		e1 := decTable.Table[r1.Peek(MaxHuffBits)]
		dst1[i1] = e1.Symbol()
		r1.Consume(int(e1.CodeLen()))
		i1++
	}
	for i2 < c2Count {
		e2 := decTable.Table[r2.Peek(MaxHuffBits)]
		dst2[i2] = e2.Symbol()
		r2.Consume(int(e2.CodeLen()))
		i2++
	}
	for i3 < c3Count {
		e3 := decTable.Table[r3.Peek(MaxHuffBits)]
		dst3[i3] = e3.Symbol()
		r3.Consume(int(e3.CodeLen()))
		i3++
	}

	if r0.underflow || r1.underflow || r2.underflow || r3.underflow {
		return 0, ErrCorruptedStream
	}

	return origLen, nil
}

// -----------------------------------------------------------------------------
// Canonical Huffman Compact Codecs (Active-Mask Architecture)
// -----------------------------------------------------------------------------

// EncodeHuffmanCompact packs a byte stream into a single bit-stream with a 32-byte active mask.
func EncodeHuffmanCompact(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{ModeHuffmanCompact, 0, 0, 0, 0}, nil
	}

	lengths, active := ComputeHuffmanLengths(src)
	if active <= 1 {
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	var blCount [MaxHuffBits + 1]int
	for _, l := range lengths {
		blCount[l]++
	}
	blCount[0] = 0
	var nextCode [MaxHuffBits + 1]uint16
	code := uint16(0)
	for b := 1; b <= MaxHuffBits; b++ {
		code = (code + uint16(blCount[b-1])) << 1
		nextCode[b] = code
	}

	var fused [256]uint32
	for s := range 256 {
		l := lengths[s]
		if l > 0 {
			c := nextCode[l]
			nextCode[l]++
			fused[s] = (uint32(c) << 8) | uint32(l)
		}
	}

	pad := huffScratchPool.Get().(*huffStreamScratchpad)
	defer huffScratchPool.Put(pad)

	maxBytes := ((n*MaxHuffBits + 31) / 8) + 8
	var w HuffBitWriter
	if maxBytes <= len(pad.streams[0]) {
		w.Reset(pad.streams[0][:])
	} else {
		w.Reset(make([]byte, maxBytes))
	}

	for _, s := range src {
		entry := fused[s]
		w.Write(uint16(entry>>8), uint8(entry))
	}
	w.Flush()
	stream := w.Written()

	hdrSize := 37 + (active+1)/2
	out := make([]byte, hdrSize+len(stream))
	out[0] = ModeHuffmanCompact
	binary.LittleEndian.PutUint32(out[1:5], uint32(n))

	for s := range 256 {
		if lengths[s] > 0 {
			out[5+s/8] |= 1 << (s % 8)
		}
	}
	pos := 37
	nib := 0
	for s := range 256 {
		if lengths[s] > 0 {
			if (nib & 1) == 0 {
				out[pos+nib/2] = lengths[s] & 0x0F
			} else {
				out[pos+nib/2] |= (lengths[s] & 0x0F) << 4
			}
			nib++
		}
	}
	copy(out[hdrSize:], stream)
	return out, nil
}

// DecodeHuffmanCompact unpacks a single-stream compact Huffman payload with 0 heap allocations.
func DecodeHuffmanCompact(dst []byte, src []byte, decTable *CanonicalHuffmanTable) (int, error) {
	if len(src) < 5 {
		return 0, ErrCorruptedStream
	}
	origLen := int(binary.LittleEndian.Uint32(src[1:5]))
	if origLen == 0 {
		return 0, nil
	}
	if len(dst) < origLen {
		return 0, ErrBufferTooSmall
	}
	if len(src) < 37 {
		return 0, ErrCorruptedStream
	}

	var lengths [256]uint8
	pos := 37
	nib := 0
	for s := range 256 {
		if (src[5+s/8] & (1 << (s % 8))) != 0 {
			if pos+nib/2 >= len(src) {
				return 0, ErrCorruptedStream
			}
			var l uint8
			if (nib & 1) == 0 {
				l = src[pos+nib/2] & 0x0F
			} else {
				l = (src[pos+nib/2] >> 4) & 0x0F
			}
			lengths[s] = l
			nib++
		}
	}
	hdrSize := 37 + (nib+1)/2
	if hdrSize > len(src) {
		return 0, ErrCorruptedStream
	}

	if decTable == nil {
		var local CanonicalHuffmanTable
		decTable = &local
	}
	if !decTable.BuildFromLengths(lengths) {
		return 0, ErrCorruptedStream
	}

	var r HuffBitReader
	r.Init(src[hdrSize:])
	for i := 0; i < origLen; i++ {
		e := decTable.Table[r.Peek(MaxHuffBits)]
		dst[i] = e.Symbol()
		r.Consume(int(e.CodeLen()))
	}
	if r.underflow {
		return 0, ErrCorruptedStream
	}
	return origLen, nil
}

// EncodeHuffmanCompact4X packs a byte stream into 4 interleaved bit-streams with a 32-byte active mask.
func EncodeHuffmanCompact4X(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{ModeHuffmanCompact4X, 0, 0, 0, 0}, nil
	}

	lengths, active := ComputeHuffmanLengths(src)
	if active <= 1 {
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	var blCount [MaxHuffBits + 1]int
	for _, l := range lengths {
		blCount[l]++
	}
	blCount[0] = 0
	var nextCode [MaxHuffBits + 1]uint16
	code := uint16(0)
	for b := 1; b <= MaxHuffBits; b++ {
		code = (code + uint16(blCount[b-1])) << 1
		nextCode[b] = code
	}

	var fused [256]uint32
	for s := range 256 {
		l := lengths[s]
		if l > 0 {
			c := nextCode[l]
			nextCode[l]++
			fused[s] = (uint32(c) << 8) | uint32(l)
		}
	}

	chunkSize := (n + 3) / 4
	c0 := 0
	c1 := min(chunkSize, n)
	c2 := min(2*chunkSize, n)
	c3 := min(3*chunkSize, n)
	c4 := n

	chunks := [4][]byte{
		src[c0:c1],
		src[c1:c2],
		src[c2:c3],
		src[c3:c4],
	}
	pad := huffScratchPool.Get().(*huffStreamScratchpad)
	defer huffScratchPool.Put(pad)
	maxChunkBytes := ((chunkSize*MaxHuffBits + 31) / 8) + 8

	var writers [4]HuffBitWriter
	for i := range 4 {
		if maxChunkBytes <= len(pad.streams[i]) {
			writers[i].Reset(pad.streams[i][:])
		} else {
			writers[i].Reset(make([]byte, maxChunkBytes))
		}
		for _, sym := range chunks[i] {
			entry := fused[sym]
			writers[i].Write(uint16(entry>>8), uint8(entry))
		}
		writers[i].Flush()
	}

	s0 := writers[0].Written()
	s1 := writers[1].Written()
	s2 := writers[2].Written()
	s3 := writers[3].Written()

	hdrSize := 37 + (active+1)/2 + 16
	out := make([]byte, hdrSize+len(s0)+len(s1)+len(s2)+len(s3))
	out[0] = ModeHuffmanCompact4X
	binary.LittleEndian.PutUint32(out[1:5], uint32(n))

	for s := range 256 {
		if lengths[s] > 0 {
			out[5+s/8] |= 1 << (s % 8)
		}
	}
	pos := 37
	nib := 0
	for s := range 256 {
		if lengths[s] > 0 {
			if (nib & 1) == 0 {
				out[pos+nib/2] = lengths[s] & 0x0F
			} else {
				out[pos+nib/2] |= (lengths[s] & 0x0F) << 4
			}
			nib++
		}
	}
	pos = 37 + (active+1)/2
	binary.LittleEndian.PutUint32(out[pos:pos+4], uint32(len(s0)))
	binary.LittleEndian.PutUint32(out[pos+4:pos+8], uint32(len(s1)))
	binary.LittleEndian.PutUint32(out[pos+8:pos+12], uint32(len(s2)))
	binary.LittleEndian.PutUint32(out[pos+12:pos+16], uint32(len(s3)))
	pos += 16

	copy(out[pos:], s0)
	pos += len(s0)
	copy(out[pos:], s1)
	pos += len(s1)
	copy(out[pos:], s2)
	pos += len(s2)
	copy(out[pos:], s3)

	return out, nil
}

// DecodeHuffmanCompact4X unpacks a 4-stream interleaved compact Huffman payload with 0 heap allocations.
func DecodeHuffmanCompact4X(dst []byte, src []byte, decTable *CanonicalHuffmanTable) (int, error) {
	if len(src) < 5 {
		return 0, ErrCorruptedStream
	}
	origLen := int(binary.LittleEndian.Uint32(src[1:5]))
	if origLen == 0 {
		return 0, nil
	}
	if len(dst) < origLen {
		return 0, ErrBufferTooSmall
	}
	if len(src) < 37 {
		return 0, ErrCorruptedStream
	}

	var lengths [256]uint8
	pos := 37
	nib := 0
	for s := range 256 {
		if (src[5+s/8] & (1 << (s % 8))) != 0 {
			if pos+nib/2 >= len(src) {
				return 0, ErrCorruptedStream
			}
			var l uint8
			if (nib & 1) == 0 {
				l = src[pos+nib/2] & 0x0F
			} else {
				l = (src[pos+nib/2] >> 4) & 0x0F
			}
			lengths[s] = l
			nib++
		}
	}
	hdrSize := 37 + (nib+1)/2
	if hdrSize+16 > len(src) {
		return 0, ErrCorruptedStream
	}

	if decTable == nil {
		var local CanonicalHuffmanTable
		decTable = &local
	}
	if !decTable.BuildFromLengths(lengths) {
		return 0, ErrCorruptedStream
	}

	sz0 := int(binary.LittleEndian.Uint32(src[hdrSize : hdrSize+4]))
	sz1 := int(binary.LittleEndian.Uint32(src[hdrSize+4 : hdrSize+8]))
	sz2 := int(binary.LittleEndian.Uint32(src[hdrSize+8 : hdrSize+12]))
	sz3 := int(binary.LittleEndian.Uint32(src[hdrSize+12 : hdrSize+16]))

	p0 := hdrSize + 16
	p1 := p0 + sz0
	p2 := p1 + sz1
	p3 := p2 + sz2
	pEnd := p3 + sz3

	if pEnd > len(src) {
		return 0, ErrCorruptedStream
	}

	chunkSize := (origLen + 3) / 4
	c0Count := min(chunkSize, origLen)
	c1Count := min(chunkSize, max(0, origLen-chunkSize))
	c2Count := min(chunkSize, max(0, origLen-2*chunkSize))
	c3Count := max(0, origLen-3*chunkSize)

	dst0 := dst[0:c0Count]
	dst1 := dst[c0Count : c0Count+c1Count]
	dst2 := dst[c0Count+c1Count : c0Count+c1Count+c2Count]
	dst3 := dst[c0Count+c1Count+c2Count : origLen]

	var r0, r1, r2, r3 HuffBitReader
	r0.Init(src[p0:p1])
	r1.Init(src[p1:p2])
	r2.Init(src[p2:p3])
	r3.Init(src[p3:pEnd])

	// Superscalar 4-way unrolled decode loop
	i0, i1, i2, i3 := 0, 0, 0, 0
	for i0 < c0Count && i1 < c1Count && i2 < c2Count && i3 < c3Count {
		e0 := decTable.Table[r0.Peek(MaxHuffBits)]
		dst0[i0] = e0.Symbol()
		r0.Consume(int(e0.CodeLen()))
		i0++

		e1 := decTable.Table[r1.Peek(MaxHuffBits)]
		dst1[i1] = e1.Symbol()
		r1.Consume(int(e1.CodeLen()))
		i1++

		e2 := decTable.Table[r2.Peek(MaxHuffBits)]
		dst2[i2] = e2.Symbol()
		r2.Consume(int(e2.CodeLen()))
		i2++

		e3 := decTable.Table[r3.Peek(MaxHuffBits)]
		dst3[i3] = e3.Symbol()
		r3.Consume(int(e3.CodeLen()))
		i3++
	}

	for i0 < c0Count {
		e0 := decTable.Table[r0.Peek(MaxHuffBits)]
		dst0[i0] = e0.Symbol()
		r0.Consume(int(e0.CodeLen()))
		i0++
	}
	for i1 < c1Count {
		e1 := decTable.Table[r1.Peek(MaxHuffBits)]
		dst1[i1] = e1.Symbol()
		r1.Consume(int(e1.CodeLen()))
		i1++
	}
	for i2 < c2Count {
		e2 := decTable.Table[r2.Peek(MaxHuffBits)]
		dst2[i2] = e2.Symbol()
		r2.Consume(int(e2.CodeLen()))
		i2++
	}
	for i3 < c3Count {
		e3 := decTable.Table[r3.Peek(MaxHuffBits)]
		dst3[i3] = e3.Symbol()
		r3.Consume(int(e3.CodeLen()))
		i3++
	}

	if r0.underflow || r1.underflow || r2.underflow || r3.underflow {
		return 0, ErrCorruptedStream
	}

	return origLen, nil
}

// -----------------------------------------------------------------------------
// Unified Stream Packing & Flat Fallback Policy
// -----------------------------------------------------------------------------

// EncodeRecursiveLZ compresses a repetitive command or offset stream using standard PRZM LZ77.
func EncodeRecursiveLZ(src []byte) ([]byte, error) {
	if len(src) < 512 {
		return nil, ErrCorruptedStream
	}
	comp, err := CompressWithOptions(src, CompressOptions{
		Level:              5,
		StandardStreamOnly: true,
	})
	if err != nil {
		return nil, err
	}
	if len(comp)+5 >= len(src) {
		return nil, ErrCorruptedStream
	}
	out := make([]byte, 5+len(comp))
	out[0] = ModeRecursiveLZ
	binary.LittleEndian.PutUint32(out[1:5], uint32(len(src)))
	copy(out[5:], comp)
	return out, nil
}

// PackStream compresses a byte stream (Section 2 or Section 3).
// It evaluates Compact, Compact4X, standard 4X Huffman, and Recursive LZ formats and
// selects whichever yields the minimum packed size, falling back to ModeFlat
// if compression does not beat raw size.
func PackStream(src []byte, prefMode uint8) ([]byte, error) {
	n := len(src)
	if n < 32 {
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	if prefMode == ModeFlat {
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	best := make([]byte, 5+n)
	best[0] = ModeFlat
	binary.LittleEndian.PutUint32(best[1:5], uint32(n))
	copy(best[5:], src)

	if c, err := EncodeHuffmanCompact(src); err == nil && len(c) < len(best) {
		best = c
	}
	if c4, err := EncodeHuffmanCompact4X(src); err == nil && len(c4) < len(best) {
		best = c4
	}
	if m1, err := EncodeHuffman4X(src); err == nil && len(m1) < len(best) {
		best = m1
	}
	if n >= 512 {
		if rec, err := EncodeRecursiveLZ(src); err == nil && len(rec) < len(best) {
			best = rec
		}
	}

	return best, nil
}

// UnpackStream decompresses a packed stream into dst with ZERO heap allocations.
func UnpackStream(dst []byte, src []byte, scratchTable *CanonicalHuffmanTable) (int, error) {
	if len(src) < 5 {
		return 0, ErrCorruptedStream
	}
	mode := src[0]
	origLen := int(binary.LittleEndian.Uint32(src[1:5]))
	if len(dst) < origLen {
		return 0, ErrBufferTooSmall
	}

	switch mode {
	case ModeFlat:
		if len(src)-5 < origLen {
			return 0, ErrCorruptedStream
		}
		copy(dst[:origLen], src[5:5+origLen])
		return origLen, nil

	case ModeHuffman4X:
		return DecodeHuffman4X(dst, src, scratchTable)

	case ModeHuffmanCompact:
		return DecodeHuffmanCompact(dst, src, scratchTable)

	case ModeHuffmanCompact4X:
		return DecodeHuffmanCompact4X(dst, src, scratchTable)

	case ModeRecursiveLZ:
		decomp, err := DecompressTo(src[5:], dst[:origLen])
		if err != nil {
			return 0, fmt.Errorf("%w: recursive lz decode failed: %v", ErrCorruptedStream, err)
		}
		return len(decomp), nil

	default:
		return 0, fmt.Errorf("%w: unknown mode %d", ErrCorruptedStream, mode)
	}
}

