// Package clean_core provides Distance Offsets, 49-bracket codebook mapping,
// Clustered V-ANS Mode 12 entropy coding, 4-register locality queue, and offset stream unpacking.
// Strictly adheres to the Clean Room Specification with zero dynamic memory allocations.
package przm

import (
	"encoding/binary"
	"math/bits"
	"sync"
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

// distExtraBits contains the extra bit count for each of the 49 distance brackets.
var distExtraBits = [49]uint8{
	0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16, 17, 17, 18, 18, 19, 19, 20, 20, 21, 21, 22, 22, 24,
}

// distBase contains the base distance for each of the 49 distance brackets.
var distBase = [49]uint32{
	1, 2, 3, 4, 5, 7, 9, 13, 17, 25,
	33, 49, 65, 97, 129, 193, 257, 385, 513, 769,
	1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577,
	32769, 49153, 65537, 98305, 131073, 196609, 262145, 393217, 524289, 786433,
	1048577, 1572865, 2097153, 3145729, 4194305, 6291457, 8388609, 12582913, 16777217,
}

// staticDistFreq4 contains the 4 x 64 static frequency table for Mode 12 V-ANS.
var staticDistFreq4 = [4][64]uint16{
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
		for c := 0; c < 4; c++ {
			var cum uint16
			for s := 0; s < 64; s++ {
				f := staticDistFreq4[c][s]
				distModels4[c].Freq[s] = f
				distModels4[c].CDF[s] = cum
				for k := uint16(0); k < f; k++ {
					distModels4[c].Lookup[cum+k] = uint8(s)
				}
				cum += f
			}
		}
	})
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
		base := (2 + half) << nb + 1
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
	return distBase[code] + extra, nil
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
	buf []byte
	pos int
	acc uint64
	cnt uint8
}

// NewBitWriter initializes a bit writer over buf.
func NewBitWriter(buf []byte) BitWriter {
	return BitWriter{buf: buf}
}

// WriteBits writes nb bits from val into the writer (MSB-first).
func (w *BitWriter) WriteBits(val uint32, nb uint8) error {
	if nb == 0 {
		return nil
	}
	w.acc = (w.acc << nb) | (uint64(val) & ((1 << nb) - 1))
	w.cnt += nb
	for w.cnt >= 8 {
		if w.pos >= len(w.buf) {
			return ErrBufferTooSmall
		}
		w.cnt -= 8
		w.buf[w.pos] = byte(w.acc >> w.cnt)
		w.pos++
	}
	return nil
}

// Flush flushes any remaining bits in the accumulator, padding with zeros.
func (w *BitWriter) Flush() error {
	if w.cnt > 0 {
		if w.pos >= len(w.buf) {
			return ErrBufferTooSmall
		}
		w.buf[w.pos] = byte(w.acc << (8 - w.cnt))
		w.pos++
		w.cnt = 0
		w.acc = 0
	}
	return nil
}

// BytesWritten returns the total number of bytes written.
func (w *BitWriter) BytesWritten() int {
	return w.pos
}

// BitReader extracts bits MSB-first from a source byte buffer.
type BitReader struct {
	buf []byte
	pos int
	acc uint64
	cnt uint8
}

// ReadBits reads nb bits from the stream MSB-first.
func (r *BitReader) ReadBits(nb uint8) (uint32, error) {
	if nb == 0 {
		return 0, nil
	}
	for r.cnt < nb {
		if r.pos >= len(r.buf) {
			return 0, ErrCorruptedFrame
		}
		r.acc = (r.acc << 8) | uint64(r.buf[r.pos])
		r.pos++
		r.cnt += 8
	}
	shift := r.cnt - nb
	mask := (uint64(1) << nb) - 1
	val := uint32((r.acc >> shift) & mask)
	r.cnt -= nb
	return val, nil
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

	for i := 0; i < n; i++ {
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

	s := scratchPool.Get()
	defer scratchPool.Put(s)

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
	if mode == ModeDistancePrefix || mode == ModeDistancePrefixCompact || mode == ModeDistancePrefixByte {
		return UnpackDistancePrefix(src, dst)
	}
	return 0, ErrUnsupportedMode
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
	if hSize+int(codeLen) > len(src) {
		return 0, 0, 0, 0, ErrCorruptedFrame
	}
	if origLen > 16777216 || matchCount > origLen {
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
func decodeDistanceVarints(codes []byte, matchCount int, extraStream []byte, dst []byte, origLen int) (int, error) {
	reader := BitReader{buf: extraStream}
	dstPos := 0
	for i := 0; i < matchCount; i++ {
		code := codes[i]
		if code > 48 {
			return 0, ErrCorruptedFrame
		}
		nb := distExtraBits[code]
		var extra uint32
		if nb > 0 {
			val, err := reader.ReadBits(nb)
			if err != nil {
				return 0, err
			}
			extra = val
		}
		d := distBase[code] + extra
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
	s := scratchPool.Get()
	defer scratchPool.Put(s)

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
	s := scratchPool.Get()
	defer scratchPool.Put(s)

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
		if d <= 0x7F {
			origVarintLen++
		} else if d <= 0x3FFF {
			origVarintLen += 2
		} else if d <= 0x1FFFFF {
			origVarintLen += 3
		} else {
			origVarintLen += 4
		}
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

// assemblePrefixContainer serializes the container header and appends CodeStream and ExtraBitsStream.
func assemblePrefixContainer(mode byte, origVarintLen, matchCount, codeLen uint32,
	codeStream, extraStream, dst []byte) (int, error) {
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
