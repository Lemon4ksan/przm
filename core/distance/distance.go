// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package distance provides match distance encoding, decoding, 49-bracket codebook mapping,
// Clustered V-ANS Mode 12 entropy coding, 4-register locality queue, and offset stream unpacking.
// Strictly conforms to the Clean Room Specification with zero dynamic memory allocation.
package distance

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"sync"
	"sync/atomic"

	"github.com/lemon4ksan/foundation/generic"
)

// Offset container mode identifiers.
const (
	ModeFlat                  byte = 0x00
	ModeDistancePrefix        byte = 0x06
	ModeFlatCompact           byte = 0x09
	ModeDistancePrefixCompact byte = 0x0A
	ModeDistancePrefixByte    byte = 0x0B
	ModeDistClusteredVANS     byte = 0x0C
)

// Standard error definitions for distance codec.
var (
	ErrBufferTooSmall  = errors.New("distance: buffer too small")
	ErrCorruptedFrame  = errors.New("distance: corrupted frame")
	ErrOffsetOOB       = errors.New("distance: match offset out of bounds")
	ErrUnsupportedMode = errors.New("distance: unsupported offset container mode")
)

// DistExtraBits contains the extra bit count for each of the 49 distance brackets.
var DistExtraBits = [49]uint8{
	0, 0, 0, 0,
	1, 1, 2, 2,
	3, 3, 4, 4,
	5, 5, 6, 6,
	7, 7, 8, 8,
	9, 9, 10, 10,
	11, 11, 12, 12,
	13, 13, 14, 14,
	15, 15, 16, 16,
	17, 17, 18, 18,
	19, 19, 20, 20,
	21, 21, 22, 22,
	24,
}

// DistBase contains the base distance for each of the 49 distance brackets.
var DistBase = [49]uint32{
	1, 2, 3, 4, 5, 7, 9, 13, 17, 25,
	33, 49, 65, 97, 129, 193, 257, 385, 513, 769,
	1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577,
	32769, 49153, 65537, 98305, 131073, 196609, 262145, 393217, 524289, 786433,
	1048577, 1572865, 2097153, 3145729, 4194305, 6291457, 8388609, 12582913, 16777217,
}

// StaticDistFreq4 contains the 4 x 64 static frequency table for Mode 12 V-ANS.
var StaticDistFreq4 = [4][64]uint16{
	{
		1, 1, 1, 1, 1, 1, 128, 43, 128, 256, 341, 687, 341, 256, 427, 43,
		256, 43, 128, 1, 43, 43, 85, 85, 85, 85, 85, 85, 171, 1, 85, 43,
		85, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	},
	{
		2, 1, 1, 1, 1, 1, 83, 61, 183, 145, 236, 214, 291, 248, 250, 186,
		218, 176, 192, 157, 189, 154, 179, 115, 164, 111, 136, 70, 98, 60, 54, 36,
		31, 18, 5, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	},
	{
		3, 1, 1, 1, 1, 1, 58, 40, 113, 100, 164, 141, 194, 174, 245, 214,
		232, 188, 226, 208, 245, 189, 245, 166, 217, 145, 144, 87, 102, 68, 52, 34,
		44, 18, 6, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	},
	{
		2, 1, 1, 1, 1, 1, 39, 38, 86, 79, 163, 124, 167, 143, 187, 151,
		179, 140, 151, 136, 168, 123, 213, 123, 179, 147, 253, 169, 254, 177, 161, 112,
		112, 60, 25, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	},
}

type distModel struct {
	Freq   [64]uint16
	CDF    [64]uint16
	Lookup [4096]uint8
}

var (
	distModels4     [4]distModel
	distModels4Once sync.Once
)

func initDistModels4() {
	distModels4Once.Do(func() {
		for c := range 4 {
			var cum uint16
			for s := range 64 {
				f := StaticDistFreq4[c][s]
				distModels4[c].Freq[s] = f
				distModels4[c].CDF[s] = cum
				for k := range f {
					distModels4[c].Lookup[cum+k] = uint8(s)
				}
				cum += f
			}
		}
	})
}

// Scratchpad provides reusable buffers for zero-allocation distance processing.
type Scratchpad struct {
	meta   [2 * 1024 * 1024]byte
	varBuf [2 * 1024 * 1024]byte
	cmds   [4 * 1024 * 1024]byte
}

var scratchPool = generic.NewPool[Scratchpad](func() *Scratchpad {
	return new(Scratchpad)
})

var fastScratch [4]atomic.Pointer[Scratchpad]

func init() {
	for i := range fastScratch {
		fastScratch[i].Store(new(Scratchpad))
	}
}

func getScratch() *Scratchpad {
	for i := range fastScratch {
		if s := fastScratch[i].Swap(nil); s != nil {
			return s
		}
	}
	return scratchPool.Get()
}

func putScratch(s *Scratchpad) {
	if s == nil {
		return
	}
	for i := range fastScratch {
		if fastScratch[i].CompareAndSwap(nil, s) {
			return
		}
	}
	scratchPool.Put(s)
}

// DistanceToCode maps a match distance D to its distance code, extra bits, and extra bit count.
func DistanceToCode(d uint32) (code uint8, extra uint32, nb uint8) {
	if d <= 4 {
		return uint8(d - 1), 0, 0
	}
	if d <= 16777216 {
		u := d - 1
		nb = uint8(bits.Len32(u) - 2)
		half := uint32((u >> nb) & 1)
		code = 2*nb + 2 + uint8(half)
		base := (2+half)<<nb + 1
		extra = d - base
		return code, extra, nb
	}
	return 48, d - 16777217, 24
}

// CodeToDistance maps a distance code and extra bits to the uncompressed match distance D.
func CodeToDistance(code uint8, extra uint32) (uint32, error) {
	if code > 48 {
		return 0, ErrCorruptedFrame
	}
	return DistBase[code] + extra, nil
}

// ContextDist returns the Markov context class for distance symbol prediction.
func ContextDist(prev uint8) int {
	if prev < 6 {
		return 0
	}
	if prev < 14 {
		return 1
	}
	if prev < 26 {
		return 2
	}
	return 3
}

// BitWriter packs bits MSB-first into a destination byte buffer.
type BitWriter struct {
	Buf []byte
	Pos int
	Acc uint64
	Cnt uint8
}

// NewBitWriter initializes a bit writer over buf.
func NewBitWriter(buf []byte) BitWriter {
	return BitWriter{Buf: buf}
}

// WriteBits writes nb bits from val into the writer (MSB-first).
func (w *BitWriter) WriteBits(val uint32, nb uint8) error {
	if nb == 0 {
		return nil
	}
	w.Acc = (w.Acc << nb) | (uint64(val) & ((1 << nb) - 1))
	w.Cnt += nb
	for w.Cnt >= 8 {
		if w.Pos >= len(w.Buf) {
			return ErrBufferTooSmall
		}
		w.Cnt -= 8
		w.Buf[w.Pos] = byte(w.Acc >> w.Cnt)
		w.Pos++
	}
	return nil
}

// Flush flushes any remaining bits in the accumulator, padding with zeros.
func (w *BitWriter) Flush() error {
	if w.Cnt > 0 {
		if w.Pos >= len(w.Buf) {
			return ErrBufferTooSmall
		}
		w.Buf[w.Pos] = byte(w.Acc << (8 - w.Cnt))
		w.Pos++
		w.Cnt = 0
		w.Acc = 0
	}
	return nil
}

// BytesWritten returns the total number of bytes written.
func (w *BitWriter) BytesWritten() int {
	return w.Pos
}

// BitReader extracts bits MSB-first from a source byte buffer.
type BitReader struct {
	Buf []byte
	Pos int
	Acc uint64
	Cnt uint8
}

// NewBitReader initializes a bit reader over buf.
func NewBitReader(buf []byte) BitReader {
	return BitReader{Buf: buf}
}

// ReadBits reads nb bits from the stream MSB-first.
func (r *BitReader) ReadBits(nb uint8) (uint32, error) {
	if nb == 0 {
		return 0, nil
	}
	for r.Cnt < nb {
		if r.Pos >= len(r.Buf) {
			return 0, ErrCorruptedFrame
		}
		r.Acc = (r.Acc << 8) | uint64(r.Buf[r.Pos])
		r.Pos++
		r.Cnt += 8
	}
	shift := r.Cnt - nb
	mask := (uint64(1) << nb) - 1
	val := uint32((r.Acc >> shift) & mask)
	r.Cnt -= nb
	return val, nil
}

// DecodeVansDist4 decodes n distance codes using 4-context Mode 12 V-ANS.
func DecodeVansDist4(src []byte, dstCodes []uint8, n int) error {
	return DecodeDistClusteredVans(src, dstCodes, n)
}

// DecodeDistClusteredVans decodes n distance codes using 4-context Mode 12 V-ANS.
func DecodeDistClusteredVans(src []byte, dstCodes []uint8, n int) error {
	if len(src) < 5 || src[0] != ModeDistClusteredVANS {
		return ErrCorruptedFrame
	}
	if n == 0 {
		return nil
	}
	initDistModels4()

	x := binary.LittleEndian.Uint32(src[1:5])
	pos := 5
	var prev uint8

	for i := range n {
		ctx := ContextDist(prev)
		slot := x & 4095
		s := distModels4[ctx].Lookup[slot]
		dstCodes[i] = s
		prev = s

		freq := uint32(distModels4[ctx].Freq[s])
		cdf := uint32(distModels4[ctx].CDF[s])
		x = freq*(x>>12) + (slot - cdf)

		for x < 65536 {
			if pos >= len(src) {
				return ErrCorruptedFrame
			}
			x = (x << 8) | uint32(src[pos])
			pos++
		}
	}
	return nil
}

// EncodeVansDist4 encodes n distance codes into dst using Mode 12 V-ANS.
func EncodeVansDist4(codes []uint8, dst []byte) (int, error) {
	return EncodeDistClusteredVans(codes, dst)
}

// EncodeDistClusteredVans encodes n distance codes into dst using Mode 12 V-ANS.
func EncodeDistClusteredVans(codes []uint8, dst []byte) (int, error) {
	n := len(codes)
	if n == 0 {
		if len(dst) < 5 {
			return 0, ErrBufferTooSmall
		}
		dst[0] = ModeDistClusteredVANS
		binary.LittleEndian.PutUint32(dst[1:5], 65536)
		return 5, nil
	}
	initDistModels4()

	s := getScratch()
	defer putScratch(s)

	stream := s.varBuf[:]
	var streamPtr int
	x := uint32(65536)

	for i := n - 1; i >= 0; i-- {
		sym := codes[i]
		if sym >= 64 {
			return 0, ErrCorruptedFrame
		}
		prev := uint8(0)
		if i > 0 {
			prev = codes[i-1]
		}
		ctx := ContextDist(prev)
		freq := uint32(distModels4[ctx].Freq[sym])
		cdf := uint32(distModels4[ctx].CDF[sym])

		for x > 4096*freq-1 {
			if streamPtr >= len(stream) {
				return 0, ErrBufferTooSmall
			}
			stream[streamPtr] = byte(x & 0xFF)
			streamPtr++
			x >>= 8
		}
		x = (x/freq)*4096 + cdf + (x % freq)
	}

	total := 5 + streamPtr
	if total > len(dst) {
		return 0, ErrBufferTooSmall
	}
	dst[0] = ModeDistClusteredVANS
	binary.LittleEndian.PutUint32(dst[1:5], x)
	for i := 0; i < streamPtr; i++ {
		dst[5+i] = stream[streamPtr-1-i]
	}
	return total, nil
}

// LocalityQueue implements the 4-register LRU match distance state machine.
type LocalityQueue struct {
	R [4]uint32
}

// NewLocalityQueue constructs a new LocalityQueue with optional stride configuration.
func NewLocalityQueue(stride ...int) LocalityQueue {
	s := 1
	if len(stride) > 0 {
		s = stride[0]
	}
	var q LocalityQueue
	if s > 1 {
		q.R[0] = 1
		q.R[1] = uint32(s)
		q.R[2] = uint32(2 * s)
		q.R[3] = uint32(3 * s)
		return q
	}
	q.R[0] = 1
	q.R[1] = 4
	q.R[2] = 8
	q.R[3] = 16
	return q
}

// Push pushes an explicit distance d to the front of the LRU queue.
func (q *LocalityQueue) Push(d uint32) {
	q.UpdateExplicit(d)
}

// Get returns the distance register at index idx in [0..3].
func (q *LocalityQueue) Get(idx int) uint32 {
	return q.R[idx]
}

// Find searches for distance d in the LRU queue. If found, returns index [0..3] and true.
func (q *LocalityQueue) Find(d uint32) (int, bool) {
	for k := range 4 {
		if q.R[k] == d {
			return k, true
		}
	}
	return -1, false
}

// Init sets the initial distance vector based on flags and stride.
func (q *LocalityQueue) Init(stride uint32, flagStride bool) {
	if flagStride && stride > 1 {
		q.R[0] = 1
		q.R[1] = stride
		q.R[2] = 2 * stride
		q.R[3] = 3 * stride
		return
	}
	q.R[0] = 1
	q.R[1] = 4
	q.R[2] = 8
	q.R[3] = 16
}

// UpdateRepeat performs an LRU state transition on repeat match reference k in [0..3].
func (q *LocalityQueue) UpdateRepeat(k int) uint32 {
	d := q.R[k]
	switch k {
	case 0:
	case 1:
		q.R[0], q.R[1] = q.R[1], q.R[0]
	case 2:
		q.R[2], q.R[1], q.R[0] = q.R[1], q.R[0], q.R[2]
	case 3:
		q.R[3], q.R[2], q.R[1], q.R[0] = q.R[2], q.R[1], q.R[0], q.R[3]
	}
	return d
}

// UpdateExplicit pushes a newly decoded explicit distance D to the head of the queue.
func (q *LocalityQueue) UpdateExplicit(d uint32) {
	q.R[3] = q.R[2]
	q.R[2] = q.R[1]
	q.R[1] = q.R[0]
	q.R[0] = d
}

// AdvanceLocalityQueue updates the locality queue with an uncompressed match distance.
func AdvanceLocalityQueue(q *LocalityQueue, dist uint32) {
	for k := range 4 {
		if q.R[k] == dist {
			q.UpdateRepeat(k)
			return
		}
	}
	q.UpdateExplicit(dist)
}

// DecodeLocalityQueue retrieves distance by repeat opcode [0..3].
func DecodeLocalityQueue(q *LocalityQueue, code uint8) uint32 {
	return q.UpdateRepeat(int(code & 3))
}

// UpdateLRURepeat updates a raw [4]uint32 array on repeat match k.
func UpdateLRURepeat(r *[4]uint32, k uint8) {
	d := r[k]
	switch k {
	case 0:
	case 1:
		r[1] = r[0]
		r[0] = d
	case 2:
		r[2] = r[1]
		r[1] = r[0]
		r[0] = d
	case 3:
		r[3] = r[2]
		r[2] = r[1]
		r[1] = r[0]
		r[0] = d
	}
}

// UpdateLRUExplicit updates a raw [4]uint32 array with explicit distance d.
func UpdateLRUExplicit(r *[4]uint32, d uint32) {
	r[3] = r[2]
	r[2] = r[1]
	r[1] = r[0]
	r[0] = d
}

// writeLEB128 encodes an unsigned 32-bit integer as an LEB128 varint into dst.
func writeLEB128(dst []byte, pos int, v uint32) (int, error) {
	for {
		if pos >= len(dst) {
			return 0, ErrBufferTooSmall
		}
		b := byte(v & 0x7F)
		v >>= 7
		if v == 0 {
			dst[pos] = b
			pos++
			return pos, nil
		}
		dst[pos] = b | 0x80
		pos++
	}
}

// UnpackOffsets unpacks Section 3 distance stream into a contiguous LEB128 varint stream.
func UnpackOffsets(src, dst []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	mode := src[0]
	if mode == ModeFlatCompact {
		n := len(src) - 1
		if n > len(dst) {
			return 0, ErrBufferTooSmall
		}
		copy(dst, src[1:])
		return n, nil
	}
	if mode == ModeFlat {
		return unpackFlatOffsets(src, dst)
	}
	if mode == ModeDistancePrefix || mode == ModeDistancePrefixCompact || mode == ModeDistancePrefixByte {
		return UnpackDistancePrefix(src, dst)
	}
	return 0, ErrUnsupportedMode
}

func unpackFlatOffsets(src, dst []byte) (int, error) {
	if len(src) < 5 {
		return 0, ErrCorruptedFrame
	}
	origLen := int(binary.LittleEndian.Uint32(src[1:5]))
	if origLen > len(dst) {
		return 0, ErrBufferTooSmall
	}
	if 5+origLen > len(src) {
		return 0, ErrCorruptedFrame
	}
	copy(dst, src[5:5+origLen])
	return origLen, nil
}

// parsePrefixHeader decodes the header of distance prefix modes (0x06, 0x0A, 0x0B).
func parsePrefixHeader(src []byte) (origLen, matchCount, codeLen uint32, hSize int, err error) {
	if len(src) < 4 {
		return 0, 0, 0, 0, ErrCorruptedFrame
	}
	mode := src[0]
	switch mode {
	case ModeDistancePrefix:
		if len(src) < 13 {
			return 0, 0, 0, 0, ErrCorruptedFrame
		}
		origLen = binary.LittleEndian.Uint32(src[1:5])
		matchCount = binary.LittleEndian.Uint32(src[5:9])
		codeLen = binary.LittleEndian.Uint32(src[9:13])
		hSize = 13
	case ModeDistancePrefixCompact:
		if len(src) < 7 {
			return 0, 0, 0, 0, ErrCorruptedFrame
		}
		origLen = uint32(binary.LittleEndian.Uint16(src[1:3]))
		matchCount = uint32(binary.LittleEndian.Uint16(src[3:5]))
		codeLen = uint32(binary.LittleEndian.Uint16(src[5:7]))
		hSize = 7
	case ModeDistancePrefixByte:
		origLen = uint32(src[1])
		matchCount = uint32(src[2])
		codeLen = uint32(src[3])
		hSize = 4
	default:
		return 0, 0, 0, 0, ErrUnsupportedMode
	}
	if hSize+int(codeLen) > len(src) || origLen > 16777216 || matchCount > origLen {
		return 0, 0, 0, 0, ErrCorruptedFrame
	}
	return origLen, matchCount, codeLen, hSize, nil
}

// decodeCodeStream unpacks the distance codes from CodeStream.
func decodeCodeStream(codeStream []byte, matchCount int, dstCodes []byte) error {
	if len(codeStream) == matchCount {
		copy(dstCodes[:matchCount], codeStream)
		return nil
	}
	if len(codeStream) > 0 && codeStream[0] == ModeDistClusteredVANS {
		return DecodeDistClusteredVans(codeStream, dstCodes, matchCount)
	}
	if len(codeStream) > 0 && codeStream[0] == ModeFlatCompact {
		if len(codeStream)-1 < matchCount {
			return ErrCorruptedFrame
		}
		copy(dstCodes[:matchCount], codeStream[1:1+matchCount])
		return nil
	}
	if len(codeStream) > 0 && codeStream[0] == ModeFlat {
		if len(codeStream) < 5 {
			return ErrCorruptedFrame
		}
		orig := int(binary.LittleEndian.Uint32(codeStream[1:5]))
		if orig < matchCount || 5+orig > len(codeStream) {
			return ErrCorruptedFrame
		}
		copy(dstCodes[:matchCount], codeStream[5:5+matchCount])
		return nil
	}
	return ErrCorruptedFrame
}

// decodeDistanceVarints reads extra bits and formats output varints.
func decodeDistanceVarints(codes []byte, matchCount int, extraStream, dst []byte, origLen int) (int, error) {
	reader := BitReader{Buf: extraStream}
	dstPos := 0
	for i := range matchCount {
		code := codes[i]
		if code > 48 {
			return 0, ErrCorruptedFrame
		}
		nb := DistExtraBits[code]
		var extra uint32
		if nb > 0 {
			val, err := reader.ReadBits(nb)
			if err != nil {
				return 0, err
			}
			extra = val
		}
		d := DistBase[code] + extra
		if d == 0 || d > 16777216 {
			return 0, ErrOffsetOOB
		}
		var err error
		dstPos, err = writeLEB128(dst, dstPos, d)
		if err != nil {
			return 0, err
		}
	}
	if dstPos != origLen {
		return 0, ErrCorruptedFrame
	}
	return dstPos, nil
}

// UnpackDistancePrefix decodes a distance prefix container (0x06, 0x0A, 0x0B) into LEB128 varints.
func UnpackDistancePrefix(src, dstVarints []byte) (int, error) {
	origLen, matchCount, codeLen, hSize, err := parsePrefixHeader(src)
	if err != nil {
		return 0, err
	}
	if int(origLen) > len(dstVarints) {
		return 0, ErrBufferTooSmall
	}
	if matchCount == 0 {
		return 0, nil
	}
	s := getScratch()
	defer putScratch(s)

	codes := s.meta[:matchCount]
	codeStream := src[hSize : hSize+int(codeLen)]
	extraStream := src[hSize+int(codeLen):]

	if err := decodeCodeStream(codeStream, int(matchCount), codes); err != nil {
		return 0, err
	}
	return decodeDistanceVarints(codes, int(matchCount), extraStream, dstVarints, int(origLen))
}

// PackDistancePrefix packs a sequence of match distances into ModeDistancePrefix (0x06, 0x0A, or 0x0B).
func PackDistancePrefix(distances []uint32, mode byte, useVANS bool, dst []byte) (int, error) {
	n := len(distances)
	if n == 0 {
		return 0, nil
	}
	s := getScratch()
	defer putScratch(s)

	codes := s.meta[:n]
	extraWriter := NewBitWriter(s.varBuf[:])
	var origVarintLen int

	for i, d := range distances {
		if d == 0 || d > 16777216 {
			return 0, ErrOffsetOOB
		}
		c, extra, nb := DistanceToCode(d)
		codes[i] = c
		if nb > 0 {
			if err := extraWriter.WriteBits(extra, nb); err != nil {
				return 0, err
			}
		}
		origVarintLen += varintLen(d)
	}
	if err := extraWriter.Flush(); err != nil {
		return 0, err
	}

	codeBuf := s.cmds[:]
	var codeLen int
	if useVANS {
		var err error
		codeLen, err = EncodeDistClusteredVans(codes, codeBuf)
		if err != nil {
			return 0, err
		}
	} else {
		codeLen = n
		copy(codeBuf[:n], codes)
	}

	return assemblePrefixContainer(mode, uint32(origVarintLen), uint32(n), uint32(codeLen),
		codeBuf[:codeLen], s.varBuf[:extraWriter.BytesWritten()], dst)
}

func varintLen(d uint32) int {
	if d <= 0x7F {
		return 1
	}
	if d <= 0x3FFF {
		return 2
	}
	if d <= 0x1FFFFF {
		return 3
	}
	return 4
}

// assemblePrefixContainer serializes the container header and appends CodeStream and ExtraBitsStream.
func assemblePrefixContainer(mode byte, origVarintLen, matchCount, codeLen uint32,
	codeStream, extraStream, dst []byte,
) (int, error) {
	var hSize int
	switch mode {
	case ModeDistancePrefix:
		hSize = 13
		if len(dst) < hSize+len(codeStream)+len(extraStream) {
			return 0, ErrBufferTooSmall
		}
		dst[0] = ModeDistancePrefix
		binary.LittleEndian.PutUint32(dst[1:5], origVarintLen)
		binary.LittleEndian.PutUint32(dst[5:9], matchCount)
		binary.LittleEndian.PutUint32(dst[9:13], codeLen)
	case ModeDistancePrefixCompact:
		hSize = 7
		if len(dst) < hSize+len(codeStream)+len(extraStream) {
			return 0, ErrBufferTooSmall
		}
		dst[0] = ModeDistancePrefixCompact
		binary.LittleEndian.PutUint16(dst[1:3], uint16(origVarintLen))
		binary.LittleEndian.PutUint16(dst[3:5], uint16(matchCount))
		binary.LittleEndian.PutUint16(dst[5:7], uint16(codeLen))
	case ModeDistancePrefixByte:
		hSize = 4
		if len(dst) < hSize+len(codeStream)+len(extraStream) {
			return 0, ErrBufferTooSmall
		}
		dst[0] = ModeDistancePrefixByte
		dst[1] = byte(origVarintLen)
		dst[2] = byte(matchCount)
		dst[3] = byte(codeLen)
	default:
		return 0, ErrUnsupportedMode
	}

	copy(dst[hSize:], codeStream)
	copy(dst[hSize+len(codeStream):], extraStream)
	return hSize + len(codeStream) + len(extraStream), nil
}

// PackageOffsetsSection encodes Section 3 via Distance Prefix or flat mode 0x09.
func PackageOffsetsSection(offs []uint32, s3Buf []byte) []byte {
	if len(offs) == 0 {
		return nil
	}
	s3Len, err := PackDistancePrefix(offs, ModeDistancePrefix, true, s3Buf)
	var rawOffs []byte
	for _, d := range offs {
		rawOffs = appendLEB128(rawOffs, d)
	}
	if err == nil && s3Len < len(rawOffs) {
		out := make([]byte, s3Len)
		copy(out, s3Buf[:s3Len])
		return out
	}
	out := make([]byte, 1+len(rawOffs))
	out[0] = 0x09
	copy(out[1:], rawOffs)
	return out
}

func appendLEB128(buf []byte, v uint32) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v|0x80))
		v >>= 7
	}
	buf = append(buf, byte(v))
	return buf
}

// BracketDistance maps bracket index 0..9 to explicit distance.
func BracketDistance(b int) uint32 {
	if b <= 7 {
		return uint32(b + 1)
	}
	if b == 8 {
		return 16
	}
	return 32
}

// ComputeDiffLatticeDist computes target match distance based on descriptor byte and delta.
func ComputeDiffLatticeDist(desc byte, delta int8, stride uint32, r *[4]uint32) int64 {
	mode := (desc >> 6) & 3
	switch mode {
	case 0:
		return int64(r[0]) + int64(delta)
	case 1:
		return int64(r[1]) + int64(delta)
	case 2:
		return int64(r[2]) + int64(delta)
	default:
		if stride > 1 {
			return int64(r[0]) + int64(delta)*int64(stride)
		}
		return int64(uint8(delta)) + 1
	}
}
