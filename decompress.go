// Package clean_core provides the clean-room reference decompressor for PRZM frames.
// Strictly conforms to the Clean Room Specification with zero dynamic memory allocation.
package przm

import (
	"encoding/binary"
	"errors"

	"github.com/lemon4ksan/foundation/generic"
)

// Frame Signatures and Capacity Bounds
const (
	MagicPRZM    = 0x4D5A5250 // ASCII 'P','R','Z','M'
	MaxBlockSize = 16777216   // 16 MiB maximum payload
	DictIDBrotli = 0x4252544C // ASCII 'B','R','T','L'
)

// Configuration Vector Bitmasks
const (
	FlagStride       = 0x01
	FlagDeltaMask    = 0x02
	FlagUncompressed = 0x04
	FlagExtOffset    = 0x08
	FlagDictRef      = 0x10
	FlagNorm         = 0x20
	FlagMinMatch3    = 0x40
	FlagNibble       = 0x80
)

// Error Definitions
var (
	ErrCorruptedFrame  = errors.New("przm: corrupted frame")
	ErrInvalidMagic    = errors.New("przm: invalid magic")
	ErrOffsetOOB       = errors.New("przm: offset out of bounds")
	ErrBufferTooSmall  = errors.New("przm: destination buffer too small")
	ErrExceedsMaxBlock = errors.New("przm: exceeds max block size")
	ErrUnsupportedMode = errors.New("przm: unsupported compression mode")
	ErrUnknownDict     = errors.New("przm: unknown dictionary")
)

// DictEntry represents a single external dictionary entry.
type DictEntry struct {
	Data   string
	Length int
}

// DictionaryDesc describes an external static dictionary.
type DictionaryDesc struct {
	DictID       uint32
	TransformIdx int
	NumEntries   int
	Entries      []DictEntry
}

// Scratchpad provides reusable intermediate memory for zero allocations.
type Scratchpad struct {
	lits        [8 * 1024 * 1024]byte
	fiberBuf    [8 * 1024 * 1024]byte
	cmds        [4 * 1024 * 1024]byte
	offs        [4 * 1024 * 1024]byte
	op          [2 * 1024 * 1024]byte
	meta        [2 * 1024 * 1024]byte
	varBuf      [2 * 1024 * 1024]byte
	norm        [8 * 1024 * 1024]byte
	vansLookup  [4096]byte
	vansFreq    [256]uint16
	vansCDF     [256]uint16
	wordScratch [128]byte
}

var scratchPool = generic.NewPool[Scratchpad](func() *Scratchpad {
	return new(Scratchpad)
})

// FrameContext holds structural offsets and parameters for the current frame.
type FrameContext struct {
	flags    uint8
	uSize    uint32
	stride   uint32
	dictID   uint32
	bMin     uint32
	o1       int
	o2       int
	o3       int
	o4       int
	l1       int
	lCmd     int
	lOff     int
	l4       int
	litBuf   []byte
	cmdBuf   []byte
	offBuf   []byte
	dict     []byte
	dictDesc *DictionaryDesc
}

// readLEB128 parses an unsigned LEB128 integer up to 4 bytes.
func readLEB128(b []byte, pos *int) (uint32, error) {
	var res uint32
	var shift uint
	p := *pos
	for i := 0; i < 4; i++ {
		if p >= len(b) {
			return 0, ErrCorruptedFrame
		}
		byteVal := b[p]
		p++
		res |= uint32(byteVal&0x7F) << shift
		if (byteVal & 0x80) == 0 {
			if res > MaxBlockSize {
				return 0, ErrCorruptedFrame
			}
			*pos = p
			return res, nil
		}
		shift += 7
	}
	return 0, ErrCorruptedFrame
}

// copyLEB128Raw copies raw LEB128 bytes from src to dst.
func copyLEB128Raw(src []byte, sp *int, dst []byte, dp *int) error {
	p, d := *sp, *dp
	for i := 0; i < 4; i++ {
		if p >= len(src) || d >= len(dst) {
			return ErrCorruptedFrame
		}
		b := src[p]
		p++
		dst[d] = b
		d++
		if (b & 0x80) == 0 {
			*sp, *dp = p, d
			return nil
		}
	}
	return ErrCorruptedFrame
}

// lookupBrotliWord performs binary search over the static RFC 7932 dictionary.
func lookupBrotliWord(wid uint32) []byte {
	low, high, best := 0, 20, 0
	for low <= high {
		mid := (low + high) / 2
		if uint32(wordBase[mid]) <= wid {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	wLen := best + 4
	idx := int(wid - uint32(wordBase[best]))
	off := int(dictOffsets[best]) + idx*wLen
	return brotliDictData[off : off+wLen]
}


// unpackSubstream decompresses an entropy sub-stream into dst.
func unpackSubstream(src []byte, dst []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	mode := src[0]
	if mode == 0x09 {
		n := len(src) - 1
		if n > len(dst) {
			return 0, ErrBufferTooSmall
		}
		copy(dst, src[1:])
		return n, nil
	}
	if mode == 0x00 {
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
	return 0, ErrUnsupportedMode
}

// demuxStep demultiplexes a single command token across meta and var streams.
func demuxStep(op byte, meta []byte, mp *int, vStream []byte, vp *int, dst []byte, dp *int) error {
	if op < 0x80 {
		return nil
	}
	if op < 0xC0 {
		if (op & 0x0F) == 15 {
			return copyLEB128Raw(vStream, vp, dst, dp)
		}
		return nil
	}
	if op <= 0xC3 {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		tok := meta[*mp]
		*mp++
		dst[*dp] = tok
		*dp++
		if (tok & 0x0F) == 15 {
			return copyLEB128Raw(vStream, vp, dst, dp)
		}
		return nil
	}
	if op <= 0xCD {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		dst[*dp] = meta[*mp]
		*mp++
		*dp++
		return nil
	}
	return demuxStepExt(op, meta, mp, vStream, vp, dst, dp)
}

// demuxStepExt handles extended and dictionary opcodes during tri-stream demux.
func demuxStepExt(op byte, meta []byte, mp *int, vStream []byte, vp *int, dst []byte, dp *int) error {
	if op == 0xCE {
		if *mp+2 > len(meta) || *dp+2 > len(dst) {
			return ErrCorruptedFrame
		}
		desc := meta[*mp]
		*mp++
		delta := meta[*mp]
		*mp++
		dst[*dp] = desc
		*dp++
		dst[*dp] = delta
		*dp++
		if ((desc>>3)&7) == 7 && copyLEB128Raw(vStream, vp, dst, dp) != nil {
			return ErrCorruptedFrame
		}
		if (desc&7) == 7 && copyLEB128Raw(vStream, vp, dst, dp) != nil {
			return ErrCorruptedFrame
		}
		return nil
	}
	if op == 0xCF {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		dst[*dp] = meta[*mp]
		*mp++
		*dp++
		return copyLEB128Raw(vStream, vp, dst, dp)
	}
	if op <= 0xC3 {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		tok := meta[*mp]
		*mp++
		dst[*dp] = tok
		*dp++
		if (tok & 15) == 15 {
			return copyLEB128Raw(vStream, vp, dst, dp)
		}
		return nil
	}
	if op <= 0xD7 {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		dst[*dp] = meta[*mp]
		*mp++
		*dp++
		return nil
	}
	if op <= 0xE8 {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		blen := meta[*mp]
		*mp++
		dst[*dp] = blen
		*dp++
		if blen == 255 {
			return copyLEB128Raw(vStream, vp, dst, dp)
		}
		return nil
	}
	if op == 0xE9 {
		if *mp >= len(meta) || *dp >= len(dst) {
			return ErrCorruptedFrame
		}
		tok := meta[*mp]
		*mp++
		dst[*dp] = tok
		*dp++
		if (tok & 0x0F) == 15 {
			return copyLEB128Raw(vStream, vp, dst, dp)
		}
		return nil
	}
	if op == 0xF0 || op == 0xF1 {
		return copyLEB128Raw(vStream, vp, dst, dp)
	}
	if op >= 0xF3 && op <= 0xFA {
		return nil
	}
	return ErrCorruptedFrame
}

// demultiplexCommands reconstructs the canonical command stream from tri-stream inputs.
func demultiplexCommands(opStream, meta, vStream []byte, dst []byte, expected int) error {
	var opP, metaP, varP, dstP int
	for opP < len(opStream) && dstP < expected {
		op := opStream[opP]
		opP++
		if dstP >= len(dst) {
			return ErrBufferTooSmall
		}
		dst[dstP] = op
		dstP++
		if err := demuxStep(op, meta, &metaP, vStream, &varP, dst, &dstP); err != nil {
			return err
		}
	}
	if dstP != expected {
		return ErrCorruptedFrame
	}
	return nil
}

// stageParseHeader validates magic and unpacks the 8-byte frame header and directory.
func stageParseHeader(src []byte, dstCap int, f *FrameContext) error {
	if len(src) < 8 {
		return ErrCorruptedFrame
	}
	if binary.LittleEndian.Uint32(src[0:4]) != MagicPRZM {
		return ErrInvalidMagic
	}
	f.flags = src[4]
	f.uSize = uint32(src[5]) | (uint32(src[6]) << 8) | (uint32(src[7]) << 16)
	if f.uSize > MaxBlockSize {
		return ErrExceedsMaxBlock
	}
	if f.uSize == 0 {
		return nil
	}
	if dstCap < int(f.uSize)+64 {
		return ErrBufferTooSmall
	}
	if (f.flags & FlagUncompressed) != 0 {
		return nil
	}
	f.bMin = 4
	if (f.flags & FlagMinMatch3) != 0 {
		f.bMin = 3
	}
	off := 8
	if (f.flags & FlagStride) != 0 {
		if off >= len(src) {
			return ErrCorruptedFrame
		}
		f.stride = uint32(src[off]) + 1
		off++
	} else {
		f.stride = 1
	}
	if (f.flags & FlagDictRef) != 0 {
		if off+4 > len(src) {
			return ErrCorruptedFrame
		}
		f.dictID = binary.LittleEndian.Uint32(src[off : off+4])
		off += 4
		if f.dictID != DictIDBrotli {
			if f.dictDesc == nil || f.dictDesc.DictID != f.dictID {
				return ErrUnknownDict
			}
		}
	} else {
		f.dictID = 0
	}
	if (f.flags & FlagExtOffset) == 0 {
		if off+4 > len(src) {
			return ErrCorruptedFrame
		}
		f.lCmd = int(binary.LittleEndian.Uint16(src[off : off+2]))
		f.lOff = int(binary.LittleEndian.Uint16(src[off+2 : off+4]))
		off += 4
	} else {
		if off+6 > len(src) {
			return ErrCorruptedFrame
		}
		f.lCmd = int(uint32(src[off]) | (uint32(src[off+1]) << 8) | (uint32(src[off+2]) << 16))
		f.lOff = int(uint32(src[off+3]) | (uint32(src[off+4]) << 8) | (uint32(src[off+5]) << 16))
		off += 6
	}
	f.o1 = off
	return nil
}

// stageResolveSections computes section boundaries and verifies framing integrity.
func stageResolveSections(src []byte, f *FrameContext) error {
	slen := len(src)
	if f.o1+8 <= slen {
		f.l1 = 8 + 2*int(src[f.o1+5])
	} else {
		f.l1 = 0
	}
	f.o2 = f.o1 + f.l1
	f.o3 = f.o2 + f.lCmd
	f.o4 = f.o3 + f.lOff
	if f.o1 > f.o2 || f.o2 > f.o3 || f.o3 > f.o4 || f.o4 > slen {
		return ErrCorruptedFrame
	}
	f.l4 = slen - f.o4
	return nil
}

// stageUnpackLiterals decompresses or references Section 4 literals.
func stageUnpackLiterals(src []byte, f *FrameContext, s *Scratchpad) error {
	buf, err := UnpackSection4Literals(src[f.o4:], int(f.l4), s)
	if err != nil {
		return err
	}
	f.litBuf = buf
	return nil
}

// stageUnpackCommands handles Mode 8 demux or flat substream unpacking for Section 2.
func stageUnpackCommands(src []byte, f *FrameContext, s *Scratchpad) error {
	if f.lCmd == 0 {
		f.cmdBuf = nil
		return nil
	}
	s2 := src[f.o2 : f.o2+f.lCmd]
	if s2[0] == 0x08 {
		if f.lCmd < 13 {
			return ErrCorruptedFrame
		}
		origLen := int(binary.LittleEndian.Uint32(s2[1:5]))
		lOp := int(binary.LittleEndian.Uint32(s2[5:9]))
		lM := int(binary.LittleEndian.Uint32(s2[9:13]))
		if 13+lOp+lM > f.lCmd {
			return ErrCorruptedFrame
		}
		lVar := f.lCmd - 13 - lOp - lM
		nOp, err := unpackSubstream(s2[13:13+lOp], s.op[:])
		if err != nil {
			return err
		}
		nM, err := unpackSubstream(s2[13+lOp:13+lOp+lM], s.meta[:])
		if err != nil {
			return err
		}
		nVar, err := unpackSubstream(s2[13+lOp+lM:13+lOp+lM+lVar], s.varBuf[:])
		if err != nil {
			return err
		}
		if origLen > len(s.cmds) {
			return ErrBufferTooSmall
		}
		if err := demultiplexCommands(s.op[:nOp], s.meta[:nM], s.varBuf[:nVar], s.cmds[:], origLen); err != nil {
			return err
		}
		f.cmdBuf = s.cmds[:origLen]
		return nil
	}
	if s2[0] == 0x09 {
		f.cmdBuf = s2[1:]
		return nil
	}
	if s2[0] == 0x00 {
		if f.lCmd < 5 {
			return ErrCorruptedFrame
		}
		origLen := int(binary.LittleEndian.Uint32(s2[1:5]))
		if 5+origLen > f.lCmd {
			return ErrCorruptedFrame
		}
		f.cmdBuf = s2[5 : 5+origLen]
		return nil
	}
	f.cmdBuf = s2
	return nil
}

// stageUnpackOffsets unpacks Section 3 distance stream.
func stageUnpackOffsets(src []byte, f *FrameContext, s *Scratchpad) error {
	_ = s
	if f.lOff == 0 {
		f.offBuf = nil
		return nil
	}
	s3 := src[f.o3 : f.o3+f.lOff]
	if s3[0] == 0x09 {
		f.offBuf = s3[1:]
		return nil
	}
	if s3[0] == 0x00 {
		if f.lOff < 5 {
			return ErrCorruptedFrame
		}
		origLen := int(binary.LittleEndian.Uint32(s3[1:5]))
		if 5+origLen > f.lOff {
			return ErrCorruptedFrame
		}
		f.offBuf = s3[5 : 5+origLen]
		return nil
	}
	if s3[0] == ModeDistancePrefix || s3[0] == ModeDistancePrefixCompact || s3[0] == ModeDistancePrefixByte {
		n, err := UnpackDistancePrefix(s3, s.offs[:])
		if err != nil {
			return err
		}
		f.offBuf = s.offs[:n]
		return nil
	}
	f.offBuf = s3
	return nil
}

// updateLRURepeat updates LRU history registers on repeat match reference.
func updateLRURepeat(r *[4]uint32, k uint8) {
	switch k {
	case 1:
		r[0], r[1] = r[1], r[0]
	case 2:
		r[0], r[1], r[2] = r[2], r[0], r[1]
	case 3:
		r[0], r[1], r[2], r[3] = r[3], r[0], r[1], r[2]
	}
}

// updateLRUExplicit inserts a new explicit distance at R0, shifting the queue.
func updateLRUExplicit(r *[4]uint32, d uint32) {
	r[0], r[1], r[2], r[3] = d, r[0], r[1], r[2]
}

// copyMatch reproduces match bytes from window history or external dictionary.
func copyMatch(dst []byte, dstPos int, d uint32, matchLen uint32, uSize uint32, dict []byte) error {
	if uint32(dstPos)+matchLen > uSize {
		return ErrCorruptedFrame
	}
	mLen := int(matchLen)
	dist := int(d)
	if dist <= dstPos {
		srcPos := dstPos - dist
		for i := 0; i < mLen; i++ {
			dst[dstPos+i] = dst[srcPos+i]
		}
		return nil
	}
	sIdx := dstPos - dist
	if len(dict) == 0 {
		return ErrOffsetOOB
	}
	oDict := len(dict) + sIdx
	if oDict < 0 || oDict >= len(dict) {
		return ErrOffsetOOB
	}
	aDict := mLen
	if remDict := len(dict) - oDict; aDict > remDict {
		aDict = remDict
	}
	copy(dst[dstPos:dstPos+aDict], dict[oDict:oDict+aDict])
	if mLen > aDict {
		rem := mLen - aDict
		for i := 0; i < rem; i++ {
			dst[dstPos+aDict+i] = dst[i]
		}
	}
	return nil
}

// execRFCTransform executes RFC 7932 transformed dictionary match (opcode 0xCF).
func execRFCTransform(dst []byte, dstPos *int, uSize uint32, cmd []byte, cp *int, s *Scratchpad) error {
	if *cp >= len(cmd) {
		return ErrCorruptedFrame
	}
	b0 := cmd[*cp]
	*cp++
	var wid, tau uint32
	var wPtr []byte
	if b0 >= 128 {
		if *cp >= len(cmd) {
			return ErrCorruptedFrame
		}
		b1 := cmd[*cp]
		*cp++
		p := (uint32(b0&0x7F) << 7) | uint32(b1&0x7F)
		if p < 13504 {
			wPtr = lookupBrotliWord(p)
		} else if p < 14016 {
			wPtr = []byte(topRFCWords[p-13504])
		} else {
			c := p - 14016
			if c >= 2048 {
				return ErrCorruptedFrame
			}
			wid = uint32(compactPairs[c].WId)
			tau = uint32(compactPairs[c].Tau)
			if wid < 13504 {
				wPtr = lookupBrotliWord(wid)
			} else {
				wPtr = []byte(topRFCWords[wid-13504])
			}
		}
	} else {
		tau = uint32(b0)
		val, err := readLEB128(cmd, cp)
		if err != nil {
			return ErrCorruptedFrame
		}
		wid = val
		if wid < 13504 {
			wPtr = lookupBrotliWord(wid)
		} else {
			wPtr = []byte(topRFCWords[wid-13504])
		}
	}
	if len(wPtr) == 0 || tau > 120 {
		return ErrCorruptedFrame
	}
	return applyAndEmitTransform(dst, dstPos, uSize, wPtr, tau, s)
}

// applyAndEmitTransform applies string transformations and copies to destination.
func applyAndEmitTransform(dst []byte, dstPos *int, uSize uint32, wPtr []byte, tau uint32, s *Scratchpad) error {
	t := transforms[tau]
	pre := psStrings[t.PIdx]
	suf := psStrings[t.SIdx]
	mw := wPtr
	mwLen := len(mw)
	op := t.Op
	if op >= 1 && op <= 9 {
		if int(op) < mwLen {
			mw = mw[:mwLen-int(op)]
		} else {
			mw = nil
		}
	} else if op == 10 {
		copy(s.wordScratch[:mwLen], mw)
		if mwLen > 0 && s.wordScratch[0] >= 'a' && s.wordScratch[0] <= 'z' {
			s.wordScratch[0] -= 32
		}
		mw = s.wordScratch[:mwLen]
	} else if op == 11 {
		copy(s.wordScratch[:mwLen], mw)
		for i := 0; i < mwLen; i++ {
			if s.wordScratch[i] >= 'a' && s.wordScratch[i] <= 'z' {
				s.wordScratch[i] -= 32
			}
		}
		mw = s.wordScratch[:mwLen]
	} else if op >= 12 && op <= 20 {
		sk := int(op - 11)
		if sk < mwLen {
			mw = mw[sk:]
		} else {
			mw = nil
		}
	}
	emitLen := len(pre) + len(mw) + len(suf)
	if *dstPos+emitLen > int(uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstPos:], pre)
	*dstPos += len(pre)
	copy(dst[*dstPos:], mw)
	*dstPos += len(mw)
	copy(dst[*dstPos:], suf)
	*dstPos += len(suf)
	return nil
}

// stageExecuteCommands runs the command interpreter loop over unpacked sections.
func stageExecuteCommands(dst []byte, f *FrameContext, s *Scratchpad) error {
	var r [4]uint32
	if (f.flags&FlagStride) != 0 && f.stride > 1 {
		r = [4]uint32{1, f.stride, 2 * f.stride, 3 * f.stride}
	} else {
		r = [4]uint32{1, 4, 8, 16}
	}
	var dstP, cmdP, litP, offP int
	uSize := int(f.uSize)
	for dstP < uSize && cmdP < len(f.cmdBuf) {
		op := f.cmdBuf[cmdP]
		cmdP++
		if op < 0x80 {
			if err := execFusedRepeat(dst, &dstP, &litP, op, f, &r); err != nil {
				return err
			}
			continue
		}
		if op < 0xC0 {
			if err := execFusedExt(dst, &dstP, &litP, &offP, &cmdP, op, f, &r); err != nil {
				return err
			}
			continue
		}
		if err := execExtendedOp(dst, &dstP, &litP, &offP, &cmdP, op, f, &r, s); err != nil {
			return err
		}
	}
	if dstP != uSize {
		return ErrCorruptedFrame
	}
	return nil
}

// execFusedRepeat handles 1-byte fused repeat opcodes 0x00..0x7F.
func execFusedRepeat(dst []byte, dstP, litP *int, op byte, f *FrameContext, r *[4]uint32) error {
	lLit := int((op >> 4) & 7)
	lMatch := int((op>>2)&3) + int(f.bMin)
	k := op & 3
	if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
	*litP += lLit
	*dstP += lLit
	if *dstP == int(f.uSize) {
		return nil
	}
	d := r[k]
	updateLRURepeat(r, k)
	if err := copyMatch(dst, *dstP, d, uint32(lMatch), f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += lMatch
	return nil
}

// execFusedExt handles fused extended matches 0x80..0xBF.
func execFusedExt(dst []byte, dstP, litP, offP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	lLit := int((op >> 4) & 3)
	lMatch := int(op&15) + int(f.bMin)
	if (op & 15) == 15 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lMatch += int(extra)
	}
	if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
	*litP += lLit
	*dstP += lLit
	if *dstP == int(f.uSize) {
		return nil
	}
	d, err := readLEB128(f.offBuf, offP)
	if err != nil || d == 0 {
		return ErrCorruptedFrame
	}
	updateLRUExplicit(r, d)
	if err := copyMatch(dst, *dstP, d, uint32(lMatch), f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += lMatch
	return nil
}

// execExtendedOp dispatches opcodes 0xC0..0xFF.
func execExtendedOp(dst []byte, dstP, litP, offP, cmdP *int, op byte, f *FrameContext, r *[4]uint32, s *Scratchpad) error {
	if op <= 0xC3 {
		return execLongRepeat(dst, dstP, litP, cmdP, op, f, r)
	}
	if op <= 0xCD {
		return execBracketMatch(dst, dstP, litP, cmdP, op, f, r)
	}
	if op == 0xCE {
		return execDiffLattice(dst, dstP, cmdP, f, r)
	}
	if op == 0xCF {
		return execRFCTransform(dst, dstP, f.uSize, f.cmdBuf, cmdP, s)
	}
	if op <= 0xD7 {
		return execStrideMatch(dst, dstP, litP, cmdP, op, f, r)
	}
	if op >= 0xD8 && op <= 0xDF {
		return execTokenMatch(dst, dstP, op, f)
	}
	return execSpecialOp(dst, dstP, litP, cmdP, op, f)
}

// execLongRepeat executes long repeat match opcodes 0xC0..0xC3.
func execLongRepeat(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	if *cmdP >= len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	tok := f.cmdBuf[*cmdP]
	*cmdP++
	lLit := int(tok >> 4)
	lMatch := int(tok&15) + int(f.bMin)
	if (tok & 15) == 15 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lMatch += int(extra)
	}
	if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
	*litP += lLit
	*dstP += lLit
	if *dstP == int(f.uSize) {
		return nil
	}
	k := op & 3
	d := r[k]
	updateLRURepeat(r, k)
	if err := copyMatch(dst, *dstP, d, uint32(lMatch), f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += lMatch
	return nil
}

// execBracketMatch handles fused bracket match opcodes 0xC4..0xCD.
func execBracketMatch(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	if *cmdP >= len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	tok := f.cmdBuf[*cmdP]
	*cmdP++
	lLit := int(tok >> 4)
	lMatch := int(tok&15) + int(f.bMin)
	if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
	*litP += lLit
	*dstP += lLit
	if *dstP == int(f.uSize) {
		return nil
	}
	b := int(op - 0xC4)
	var d uint32
	if b <= 7 {
		d = uint32(b + 1)
	} else if b == 8 {
		d = 16
	} else {
		d = 32
	}
	updateLRUExplicit(r, d)
	if err := copyMatch(dst, *dstP, d, uint32(lMatch), f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += lMatch
	return nil
}

// execDiffLattice processes difference lattice opcode 0xCE.
func execDiffLattice(dst []byte, dstP, cmdP *int, f *FrameContext, r *[4]uint32) error {
	if *cmdP+2 > len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	desc := f.cmdBuf[*cmdP]
	*cmdP++
	delta := int8(f.cmdBuf[*cmdP])
	*cmdP++
	mode := (desc >> 6) & 3
	var d int64
	switch mode {
	case 0:
		d = int64(r[0]) + int64(delta)
	case 1:
		d = int64(r[1]) + int64(delta)
	case 2:
		d = int64(r[2]) + int64(delta)
	case 3:
		if f.stride > 1 {
			d = int64(r[0]) + int64(delta)*int64(f.stride)
		} else {
			d = int64(uint8(delta)) + 1
		}
	}
	if d <= 0 {
		return ErrCorruptedFrame
	}
	updateLRUExplicit(r, uint32(d))
	lMatch := int(desc&7) + int(f.bMin)
	if (desc & 7) == 7 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lMatch += int(extra)
	}
	if err := copyMatch(dst, *dstP, uint32(d), uint32(lMatch), f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += lMatch
	return nil
}

// execStrideMatch processes stride match opcodes 0xD0..0xD7.
func execStrideMatch(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	if *cmdP >= len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	tok := f.cmdBuf[*cmdP]
	*cmdP++
	lLit := int(tok >> 4)
	lMatch := int(tok&15) + int(f.bMin)
	if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
	*litP += lLit
	*dstP += lLit
	if *dstP == int(f.uSize) {
		return nil
	}
	m := uint32(op&7) + 1
	d := m * f.stride
	updateLRUExplicit(r, d)
	if err := copyMatch(dst, *dstP, d, uint32(lMatch), f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += lMatch
	return nil
}

// execTokenMatch handles web/text token match opcodes 0xD8..0xDF.
func execTokenMatch(dst []byte, dstP *int, op byte, f *FrameContext) error {
	idx := int(op - 0xD8)
	if f.dictDesc != nil && idx < f.dictDesc.NumEntries {
		entry := f.dictDesc.Entries[idx]
		if *dstP+entry.Length > int(f.uSize) {
			return ErrCorruptedFrame
		}
		copy(dst[*dstP:], entry.Data[:entry.Length])
		*dstP += entry.Length
		return nil
	}
	if idx < len(webTokens) {
		tok := webTokens[idx]
		if *dstP+len(tok) > int(f.uSize) {
			return ErrCorruptedFrame
		}
		copy(dst[*dstP:], tok)
		*dstP += len(tok)
		return nil
	}
	return ErrCorruptedFrame
}

// execSpecialOp handles literal runs and run-length encoding (0xF0..0xFA).
func execSpecialOp(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext) error {
	if op == 0xF0 {
		lLit, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil || *litP+int(lLit) > len(f.litBuf) || *dstP+int(lLit) > int(f.uSize) {
			return ErrCorruptedFrame
		}
		copy(dst[*dstP:], f.litBuf[*litP:*litP+int(lLit)])
		*litP += int(lLit)
		*dstP += int(lLit)
		return nil
	}
	if op == 0xF1 {
		rLen, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil || *litP >= len(f.litBuf) || *dstP+int(rLen) > int(f.uSize) {
			return ErrCorruptedFrame
		}
		b := f.litBuf[*litP]
		*litP++
		for i := 0; i < int(rLen); i++ {
			dst[*dstP+i] = b
		}
		*dstP += int(rLen)
		return nil
	}
	if op >= 0xF3 && op <= 0xFA {
		lLit := int(op - 0xF2)
		if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
			return ErrCorruptedFrame
		}
		copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
		*litP += lLit
		*dstP += lLit
		return nil
	}
	return ErrCorruptedFrame
}

const b64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// stageFinalize executes post-decompression transforms (normalization, etc.).
func stageFinalize(dst []byte, dstCap int, f *FrameContext, s *Scratchpad) (int, error) {
	if (f.flags & FlagNorm) == 0 {
		return int(f.uSize), nil
	}
	normLen := (int(f.uSize) + 2) / 3 * 4
	if normLen+64 > dstCap || normLen > len(s.norm) {
		return 0, ErrBufferTooSmall
	}
	var i, j int
	uSize := int(f.uSize)
	for i < uSize {
		octA := dst[i]
		i++
		var octB, octC byte
		if i < uSize {
			octB = dst[i]
			i++
		}
		if i < uSize {
			octC = dst[i]
			i++
		}
		triple := (uint32(octA) << 16) | (uint32(octB) << 8) | uint32(octC)
		s.norm[j] = b64Chars[(triple>>18)&0x3F]
		j++
		s.norm[j] = b64Chars[(triple>>12)&0x3F]
		j++
		if i > uSize+1 {
			s.norm[j] = '='
		} else {
			s.norm[j] = b64Chars[(triple>>6)&0x3F]
		}
		j++
		if i > uSize {
			s.norm[j] = '='
		} else {
			s.norm[j] = b64Chars[triple&0x3F]
		}
		j++
	}
	copy(dst, s.norm[:j])
	return j, nil
}

// GetUncompressedSize returns expected uncompressed payload size from frame header.
func GetUncompressedSize(src []byte) (int, error) {
	if len(src) < 4 {
		return 0, ErrCorruptedFrame
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic == MagicPRZM {
		if len(src) < 8 {
			return 0, ErrCorruptedFrame
		}
		u := int(uint32(src[5]) | (uint32(src[6]) << 8) | (uint32(src[7]) << 16))
		return u, nil
	}
	if magic == 0x535A5250 { // PRZS
		if len(src) <= 16 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[5:9])), nil
	}
	if magic == 0x504E4741 || magic == 0x49424F4D || magic == 0x53494548 { // PNGA, MOBI, HEIS
		if len(src) <= 11 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	}
	if magic == 0x4752414D { // MARG
		if len(src) <= 15 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	}
	if magic == 0x53594D50 || magic == 0x41524D36 || magic == 0x53484546 || magic == 0x4D41484C {
		if len(src) <= 11 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[8:12])), nil
	}
	if magic == 0x514F5349 { // ISOQ
		if len(src) <= 19 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[8:12])), nil
	}
	if magic == 0x47435452 { // GCTR
		if len(src) <= 9 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[6:10])), nil
	}
	if magic == 0x44485343 { // CSHD
		if len(src) <= 23 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	}
	if len(src) > 7 && ((src[0] == 'P' && src[1] == 'W' && src[2] == 0x01) ||
		(src[0] == 'G' && src[1] == 'G' && src[2] == 0x01)) {
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	}
	return 0, ErrInvalidMagic
}

// Decompress decompresses a PRZM frame into dst with strictly zero heap allocations.
func Decompress(src, dst []byte) (int, error) {
	return DecompressWithDict(src, dst, nil, nil)
}

// DecompressTo is an alias for Decompress for zero-allocation buffer destinations.
func DecompressTo(src, dst []byte) (int, error) {
	return Decompress(src, dst)
}

// Decode decompresses a PRZM frame, allocating a new destination slice automatically.
func Decode(src []byte) ([]byte, error) {
	sz, err := GetUncompressedSize(src)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, sz)
	n, err := Decompress(src, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// DecompressDict decompresses a PRZM frame using an external DictionaryDesc.
func DecompressDict(src, dst []byte, dict *DictionaryDesc) (int, error) {
	return DecompressWithDict(src, dst, nil, dict)
}

// DecompressWithDict executes the 7-stage linear pipeline for PRZM decompression.
func DecompressWithDict(src, dst []byte, dict []byte, dictDesc *DictionaryDesc) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(src) < 8 {
		return 0, ErrCorruptedFrame
	}
	var f FrameContext
	f.dict = dict
	f.dictDesc = dictDesc

	if err := stageParseHeader(src, cap(dst), &f); err != nil {
		return 0, err
	}
	if f.uSize == 0 {
		return 0, nil
	}
	if (f.flags & FlagUncompressed) != 0 {
		if len(src) < 8+int(f.uSize) {
			return 0, ErrCorruptedFrame
		}
		copy(dst, src[8:8+int(f.uSize)])
		return int(f.uSize), nil
	}
	if err := stageResolveSections(src, &f); err != nil {
		return 0, err
	}

	s := scratchPool.Get()
	err := executePipeline(src, dst, &f, s)
	scratchPool.Put(s)
	if err != nil {
		return 0, err
	}
	return stageFinalize(dst, cap(dst), &f, s)
}

// executePipeline executes the inner 4 stages using scratchpad resources.
func executePipeline(src, dst []byte, f *FrameContext, s *Scratchpad) error {
	if err := stageUnpackLiterals(src, f, s); err != nil {
		return err
	}
	if err := stageUnpackCommands(src, f, s); err != nil {
		return err
	}
	if err := stageUnpackOffsets(src, f, s); err != nil {
		return err
	}
	return stageExecuteCommands(dst, f, s)
}
