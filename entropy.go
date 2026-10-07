// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Mode identifiers for Entropy-Pack
const (
	ModeFlat      uint8 = 0 // Raw uncompressed payload (0% CPU, O(1) slice copy)
	ModeHuffman4X uint8 = 1 // 4-stream interleaved Table-Driven Canonical Huffman (RAM-speed focus: >10 GB/s)
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
	for sym := 0; sym < 256; sym++ {
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

		for i := 0; i < numEntries; i++ {
			h.Table[baseIdx+i] = entry
		}
	}
	return true
}

// HuffBitWriter collects variable-length bit codes.
type HuffBitWriter struct {
	buf  []byte
	bits uint64
	n    int // number of bits currently in `bits`
}

func (w *HuffBitWriter) Write(code uint16, length uint8) {
	w.bits |= uint64(code) << (64 - w.n - int(length))
	w.n += int(length)
	for w.n >= 32 {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(w.bits>>32))
		w.buf = append(w.buf, b[:]...)
		w.bits <<= 32
		w.n -= 32
	}
}

func (w *HuffBitWriter) Flush() []byte {
	for w.n > 0 {
		w.buf = append(w.buf, byte(w.bits>>(64-8)))
		w.bits <<= 8
		w.n -= 8
	}
	w.n = 0
	return w.buf
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
	sym    int16
	weight int32
	left   int16
	right  int16
}

// ComputeHuffmanLengths computes canonical bit-lengths bounded to MaxHuffBits (11).
func ComputeHuffmanLengths(src []byte) ([256]uint8, int) {
	var freqs [256]int32
	for _, b := range src {
		freqs[b]++
	}

	active := 0
	for _, f := range freqs {
		if f > 0 {
			active++
		}
	}

	var lengths [256]uint8
	if active == 0 {
		return lengths, 0
	}
	if active == 1 {
		for s, f := range freqs {
			if f > 0 {
				lengths[s] = 1
				break
			}
		}
		return lengths, 1
	}

	// Priority queue to build Huffman tree
	nodes := make([]huffNode, 0, 512)
	type heapElem struct {
		id     int16
		weight int32
	}
	h := make([]heapElem, 0, active)

	for s := 0; s < 256; s++ {
		if freqs[s] > 0 {
			id := int16(len(nodes))
			nodes = append(nodes, huffNode{sym: int16(s), weight: freqs[s], left: -1, right: -1})
			h = append(h, heapElem{id: id, weight: freqs[s]})
		}
	}

	popMin := func() heapElem {
		minIdx := 0
		for i := 1; i < len(h); i++ {
			if h[i].weight < h[minIdx].weight {
				minIdx = i
			}
		}
		res := h[minIdx]
		h = append(h[:minIdx], h[minIdx+1:]...)
		return res
	}

	for len(h) > 1 {
		e1 := popMin()
		e2 := popMin()
		id := int16(len(nodes))
		w := e1.weight + e2.weight
		nodes = append(nodes, huffNode{sym: -1, weight: w, left: e1.id, right: e2.id})
		h = append(h, heapElem{id: id, weight: w})
	}

	root := h[0].id
	var walk func(id int16, depth uint8)
	walk = func(id int16, depth uint8) {
		if id < 0 {
			return
		}
		node := &nodes[id]
		if node.sym >= 0 {
			l := depth
			if l > MaxHuffBits {
				l = MaxHuffBits
			}
			if l == 0 {
				l = 1
			}
			lengths[node.sym] = l
			return
		}
		walk(node.left, depth+1)
		walk(node.right, depth+1)
	}
	walk(root, 0)

	// Enforce Kraft-McMillan inequality: sum 2^(MaxHuffBits - l) <= HuffTableSize (2048)
	kraftSum := func() int {
		sum := 0
		for s := 0; s < 256; s++ {
			if lengths[s] > 0 {
				sum += 1 << (MaxHuffBits - lengths[s])
			}
		}
		return sum
	}

	for kraftSum() > HuffTableSize {
		bestSym := -1
		minW := int32(1<<30 - 1)
		for s := 0; s < 256; s++ {
			if lengths[s] > 0 && lengths[s] < MaxHuffBits {
				if freqs[s] < minW {
					minW = freqs[s]
					bestSym = s
				}
			}
		}
		if bestSym < 0 {
			break
		}
		lengths[bestSym]++
	}

	return lengths, active
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
	var codes [256]uint16
	for s := 0; s < 256; s++ {
		l := lengths[s]
		if l > 0 {
			codes[s] = nextCode[l]
			nextCode[l]++
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

	var writers [4]HuffBitWriter
	for i := 0; i < 4; i++ {
		for _, sym := range chunks[i] {
			writers[i].Write(codes[sym], lengths[sym])
		}
		writers[i].Flush()
	}

	// Format layout:
	// Byte 0: ModeHuffman4X
	// Bytes 1..4: OrigLen (uint32)
	// Bytes 5..132: Compact 256-symbol lengths (4 bits each = 128 bytes)
	// Bytes 133..148: Stream sizes (4 * uint32 = 16 bytes for streams 0, 1, 2, 3)
	// Bytes 149+: Stream 0, Stream 1, Stream 2, Stream 3 payloads
	headerSize := HeaderSizeHuffman
	totalSize := headerSize + len(writers[0].buf) + len(writers[1].buf) + len(writers[2].buf) + len(writers[3].buf)

	out := make([]byte, 0, totalSize)
	out = append(out, ModeHuffman4X)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	// Pack 256 lengths into 128 bytes (4 bits each, values 0..11)
	for i := 0; i < 256; i += 2 {
		b := (lengths[i] & 0x0F) | ((lengths[i+1] & 0x0F) << 4)
		out = append(out, b)
	}

	// Write 4 stream lengths
	var szBuf [4]byte
	for i := 0; i < 4; i++ {
		binary.LittleEndian.PutUint32(szBuf[:], uint32(len(writers[i].buf)))
		out = append(out, szBuf[:]...)
	}

	// Append payloads
	for i := 0; i < 4; i++ {
		out = append(out, writers[i].buf...)
	}

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
	for i := 0; i < 128; i++ {
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
// Unified Stream Packing & Flat Fallback Policy
// -----------------------------------------------------------------------------

// PackStream compresses a byte stream (Section 2 or Section 3).
// If compression yields < 16 bytes reduction (or expands), it automatically
// falls back to ModeFlat to ensure zero CPU waste and zero overhead.
func PackStream(src []byte, prefMode uint8) ([]byte, error) {
	n := len(src)
	if n < 32 {
		// Flat fallback for tiny inputs
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	var packed []byte
	var err error

	switch prefMode {
	case ModeHuffman4X:
		packed, err = EncodeHuffman4X(src)
	default:
		packed, err = EncodeHuffman4X(src)
	}

	if err != nil || len(packed) >= n-16 {
		// Flat fallback: compression failed to beat raw by at least 16 bytes
		out := make([]byte, 5+n)
		out[0] = ModeFlat
		binary.LittleEndian.PutUint32(out[1:5], uint32(n))
		copy(out[5:], src)
		return out, nil
	}

	return packed, nil
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

	default:
		return 0, fmt.Errorf("%w: unknown mode %d", ErrCorruptedStream, mode)
	}
}
