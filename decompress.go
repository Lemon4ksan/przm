// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package przm provides the reference decompressor for PRZM frames.
// Strictly conforms to the Clean Room Specification with zero dynamic memory allocation.
package przm

import (
	"encoding/binary"
	"errors"
	"sync/atomic"

	"github.com/lemon4ksan/foundation/generic"

	"github.com/lemon4ksan/przm/core/commands"
	"github.com/lemon4ksan/przm/core/dict"
	"github.com/lemon4ksan/przm/core/distance"
	"github.com/lemon4ksan/przm/core/literals"
)

// Frame Signatures and Capacity Bounds
const (
	MagicPRZM        = 0x4D5A5250 // ASCII 'P','R','Z','M'
	MagicPRZS        = 0x535A5250 // ASCII 'P','R','Z','S'
	HeaderSize       = 8
	StreamHeaderSize = 17
	MaxBlockSize     = 16777216   // 16 MiB maximum payload
	DictIDBrotli     = 0x4252544C // ASCII 'B','R','T','L'
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
	wordScratch [128]byte
}

var scratchPool = generic.NewPool(func() *Scratchpad {
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
	for range 4 {
		if p >= len(b) {
			return 0, ErrCorruptedFrame
		}
		byteVal := b[p]
		p++
		res |= uint32(byteVal&0x7F) << shift
		if (byteVal & 0x80) != 0 {
			shift += 7
			continue
		}
		if res > MaxBlockSize {
			return 0, ErrCorruptedFrame
		}
		*pos = p
		return res, nil
	}
	return 0, ErrCorruptedFrame
}

// stageParseHeader validates magic and unpacks the 8-byte frame header and directory.
func stageParseHeader(src []byte, dstCap int, f *FrameContext) error {
	if err := parseBasicHeader(src, dstCap, f); err != nil {
		return err
	}
	if f.uSize == 0 || (f.flags&FlagUncompressed) != 0 {
		return nil
	}
	off, err := parseOptionalHeaders(src, 8, f)
	if err != nil {
		return err
	}
	return parseDirectory(src, off, f)
}

func parseBasicHeader(src []byte, dstCap int, f *FrameContext) error {
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
	if f.uSize > 0 && dstCap < int(f.uSize)+64 {
		return ErrBufferTooSmall
	}
	f.bMin = 4
	if (f.flags & FlagMinMatch3) != 0 {
		f.bMin = 3
	}
	return nil
}

func parseOptionalHeaders(src []byte, startOff int, f *FrameContext) (int, error) {
	off := startOff
	f.stride = 1
	if (f.flags & FlagStride) != 0 {
		if off >= len(src) {
			return 0, ErrCorruptedFrame
		}
		f.stride = uint32(src[off]) + 1
		off++
	}
	f.dictID = 0
	if (f.flags & FlagDictRef) != 0 {
		if off+4 > len(src) {
			return 0, ErrCorruptedFrame
		}
		f.dictID = binary.LittleEndian.Uint32(src[off : off+4])
		off += 4
		if f.dictID != DictIDBrotli && (f.dictDesc == nil || f.dictDesc.DictID != f.dictID) {
			if d := GetRawDictionary(f.dictID); len(d) > 0 {
				f.dict = d
			} else {
				return 0, ErrUnknownDict
			}
		}
		if len(f.dict) == 0 {
			if d := GetRawDictionary(f.dictID); len(d) > 0 {
				f.dict = d
			}
		}
	}
	return off, nil
}

func parseDirectory(src []byte, off int, f *FrameContext) error {
	if (f.flags & FlagExtOffset) == 0 {
		if off+4 > len(src) {
			return ErrCorruptedFrame
		}
		f.lCmd = int(binary.LittleEndian.Uint16(src[off : off+2]))
		f.lOff = int(binary.LittleEndian.Uint16(src[off+2 : off+4]))
		f.o1 = off + 4
		return nil
	}
	if off+6 > len(src) {
		return ErrCorruptedFrame
	}
	f.lCmd = int(uint32(src[off]) | (uint32(src[off+1]) << 8) | (uint32(src[off+2]) << 16))
	f.lOff = int(uint32(src[off+3]) | (uint32(src[off+4]) << 8) | (uint32(src[off+5]) << 16))
	f.o1 = off + 6
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
	buf, err := literals.UnpackSection4Literals(src[f.o4:], int(f.l4), s.lits[:], s.fiberBuf[:])
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
	buf, err := commands.UnpackSection2Commands(src[f.o2:f.o2+f.lCmd], s.op[:], s.meta[:], s.varBuf[:], s.cmds[:])
	if err != nil {
		return err
	}
	f.cmdBuf = buf
	return nil
}

// stageUnpackOffsets unpacks Section 3 distance stream.
func stageUnpackOffsets(src []byte, f *FrameContext, s *Scratchpad) error {
	if f.lOff == 0 {
		f.offBuf = nil
		return nil
	}
	n, err := distance.UnpackOffsets(src[f.o3:f.o3+f.lOff], s.offs[:])
	if err != nil {
		return err
	}
	f.offBuf = s.offs[:n]
	return nil
}

// copyPendingLiterals transfers uncompressed literals to destination.
func copyPendingLiterals(dst []byte, dstP, litP *int, lLit int, f *FrameContext) (bool, error) {
	if *litP+lLit > len(f.litBuf) || *dstP+lLit > int(f.uSize) {
		return false, ErrCorruptedFrame
	}
	copy(dst[*dstP:], f.litBuf[*litP:*litP+lLit])
	*litP += lLit
	*dstP += lLit
	return *dstP == int(f.uSize), nil
}

// finishMatch executes match byte copy and updates destination pointer.
func finishMatch(dst []byte, dstP *int, d, lMatch uint32, f *FrameContext) error {
	if err := copyMatch(dst, *dstP, d, lMatch, f.uSize, f.dict); err != nil {
		return err
	}
	*dstP += int(lMatch)
	return nil
}

// copyMatch reproduces match bytes from window history or external dictionary.
func copyMatch(dst []byte, dstPos int, d, matchLen, uSize uint32, dict []byte) error {
	if d == 0 {
		return ErrOffsetOOB
	}
	if uint32(dstPos)+matchLen > uSize {
		return ErrCorruptedFrame
	}
	mLen, dist := int(matchLen), int(d)
	if dist <= dstPos {
		srcPos := dstPos - dist
		for i := range mLen {
			dst[dstPos+i] = dst[srcPos+i]
		}
		return nil
	}
	return copyDictMatch(dst, dstPos, dstPos-dist, mLen, dict)
}

func copyDictMatch(dst []byte, dstPos, sIdx, mLen int, dict []byte) error {
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
		for i := range rem {
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
	wPtr, tau, err := dict.ResolveRFCWord(b0, cmd, cp)
	if err != nil {
		return ErrCorruptedFrame
	}
	if err := dict.ApplyTransform(dst, dstPos, uSize, wPtr, tau, s.wordScratch[:]); err != nil {
		return ErrCorruptedFrame
	}
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
		if err := executeCommandStep(op, dst, &dstP, &litP, &offP, &cmdP, f, &r, s); err != nil {
			println("executeCommandStep failed on op:", op, "err:", err.Error())
			return err
		}
	}
	if dstP != uSize {
		println("dstP mismatch: dstP=", dstP, "uSize=", uSize, "cmdP=", cmdP, "len(cmdBuf)=", len(f.cmdBuf))
		return ErrCorruptedFrame
	}
	return nil
}

func executeCommandStep(
	op byte,
	dst []byte,
	dstP, litP, offP, cmdP *int,
	f *FrameContext,
	r *[4]uint32,
	s *Scratchpad,
) error {
	if op < 0x80 {
		return execFusedRepeat(dst, dstP, litP, op, f, r)
	}
	if op < 0xC0 {
		return execFusedExt(dst, dstP, litP, offP, cmdP, op, f, r)
	}
	return execExtendedOp(dst, dstP, litP, cmdP, op, f, r, s)
}

func execFusedRepeat(dst []byte, dstP, litP *int, op byte, f *FrameContext, r *[4]uint32) error {
	done, err := copyPendingLiterals(dst, dstP, litP, int((op>>4)&7), f)
	if err != nil || done {
		return err
	}
	k := op & 3
	d := r[k]
	distance.UpdateLRURepeat(r, k)
	lMatch := uint32(int((op>>2)&3) + int(f.bMin))
	return finishMatch(dst, dstP, d, lMatch, f)
}

func execFusedExt(dst []byte, dstP, litP, offP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	lMatch := int(op&15) + int(f.bMin)
	if (op & 15) == 15 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lMatch += int(extra)
	}
	done, err := copyPendingLiterals(dst, dstP, litP, int((op>>4)&3), f)
	if err != nil || done {
		return err
	}
	d, err := readLEB128(f.offBuf, offP)
	if err != nil || d == 0 {
		return ErrCorruptedFrame
	}
	distance.UpdateLRUExplicit(r, d)
	return finishMatch(dst, dstP, d, uint32(lMatch), f)
}

func execExtendedOp(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32, s *Scratchpad) error {
	switch {
	case op <= 0xC3:
		return execLongRepeat(dst, dstP, litP, cmdP, op, f, r)
	case op <= 0xCD:
		return execBracketMatch(dst, dstP, litP, cmdP, op, f, r)
	case op == 0xCE:
		return execDiffLattice(dst, dstP, litP, cmdP, f, r)
	case op == 0xCF:
		return execRFCTransform(dst, dstP, f.uSize, f.cmdBuf, cmdP, s)
	case op <= 0xD7:
		return execStrideMatch(dst, dstP, litP, cmdP, op, f, r)
	case op >= 0xD8 && op <= 0xDF:
		return execTokenMatch(dst, dstP, op, f)
	default:
		return execSpecialOp(dst, dstP, litP, cmdP, op, f)
	}
}

func execLongRepeat(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	if *cmdP >= len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	tok := f.cmdBuf[*cmdP]
	*cmdP++
	done, err := copyPendingLiterals(dst, dstP, litP, int(tok>>4), f)
	if err != nil || done {
		return err
	}
	lMatch := int(tok&15) + int(f.bMin)
	if (tok & 15) == 15 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lMatch += int(extra)
	}
	k := op & 3
	d := r[k]
	distance.UpdateLRURepeat(r, k)
	return finishMatch(dst, dstP, d, uint32(lMatch), f)
}

func execBracketMatch(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	if *cmdP >= len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	tok := f.cmdBuf[*cmdP]
	*cmdP++
	done, err := copyPendingLiterals(dst, dstP, litP, int(tok>>4), f)
	if err != nil || done {
		return err
	}
	d := distance.BracketDistance(int(op - 0xC4))
	distance.UpdateLRUExplicit(r, d)
	return finishMatch(dst, dstP, d, uint32(int(tok&15)+int(f.bMin)), f)
}

func execDiffLattice(dst []byte, dstP, litP, cmdP *int, f *FrameContext, r *[4]uint32) error {
	if *cmdP+2 > len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	desc := f.cmdBuf[*cmdP]
	*cmdP++
	delta := int8(f.cmdBuf[*cmdP])
	*cmdP++
	d := distance.ComputeDiffLatticeDist(desc, delta, f.stride, r)
	if d <= 0 {
		return ErrCorruptedFrame
	}
	lLit := int((desc >> 3) & 7)
	lMatch := int(desc&7) + int(f.bMin)
	if lLit == 7 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lLit += int(extra)
	}
	if (desc & 7) == 7 {
		extra, err := readLEB128(f.cmdBuf, cmdP)
		if err != nil {
			return ErrCorruptedFrame
		}
		lMatch += int(extra)
	}
	done, err := copyPendingLiterals(dst, dstP, litP, lLit, f)
	if err != nil || done {
		return err
	}
	distance.UpdateLRUExplicit(r, uint32(d))
	return finishMatch(dst, dstP, uint32(d), uint32(lMatch), f)
}

func execStrideMatch(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext, r *[4]uint32) error {
	if *cmdP >= len(f.cmdBuf) {
		return ErrCorruptedFrame
	}
	tok := f.cmdBuf[*cmdP]
	*cmdP++
	done, err := copyPendingLiterals(dst, dstP, litP, int(tok>>4), f)
	if err != nil || done {
		return err
	}
	d := (uint32(op&7) + 1) * f.stride
	distance.UpdateLRUExplicit(r, d)
	return finishMatch(dst, dstP, d, uint32(int(tok&15)+int(f.bMin)), f)
}

// execTokenMatch handles web/text token match opcodes 0xD8..0xDF with zero hardcoded engine tokens.
func execTokenMatch(dst []byte, dstPos *int, op byte, f *FrameContext) error {
	idx := int(op - 0xD8)
	if f.dictDesc != nil && idx < f.dictDesc.NumEntries {
		entry := f.dictDesc.Entries[idx]
		if *dstPos+entry.Length > int(f.uSize) {
			return ErrCorruptedFrame
		}
		copy(dst[*dstPos:], entry.Data[:entry.Length])
		*dstPos += entry.Length
		return nil
	}
	tok, ok := dict.LookupWebToken(idx)
	if !ok || *dstPos+len(tok) > int(f.uSize) {
		return ErrCorruptedFrame
	}
	copy(dst[*dstPos:], tok)
	*dstPos += len(tok)
	return nil
}

func execSpecialOp(dst []byte, dstP, litP, cmdP *int, op byte, f *FrameContext) error {
	switch {
	case op == 0xF0:
		return execLiteralRunVar(dst, dstP, litP, cmdP, f)
	case op == 0xF1:
		return execRLEByte(dst, dstP, cmdP, litP, f)
	case op >= 0xF3 && op <= 0xFA:
		return execLiteralRunFixed(dst, dstP, litP, op, f)
	default:
		return ErrCorruptedFrame
	}
}

func execLiteralRunVar(dst []byte, dstP, litP, cmdP *int, f *FrameContext) error {
	lLit, err := readLEB128(f.cmdBuf, cmdP)
	if err != nil {
		return ErrCorruptedFrame
	}
	_, err = copyPendingLiterals(dst, dstP, litP, int(lLit), f)
	return err
}

func execRLEByte(dst []byte, dstP, cmdP, litP *int, f *FrameContext) error {
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

func execLiteralRunFixed(dst []byte, dstP, litP *int, op byte, f *FrameContext) error {
	_, err := copyPendingLiterals(dst, dstP, litP, int(op-0xF2), f)
	return err
}

const b64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

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
		encodeB64Triplet(octA, octB, octC, i, uSize, s.norm[:], &j)
	}
	copy(dst, s.norm[:j])
	return j, nil
}

func encodeB64Triplet(octA, octB, octC byte, i, uSize int, norm []byte, j *int) {
	triple := (uint32(octA) << 16) | (uint32(octB) << 8) | uint32(octC)
	norm[*j] = b64Chars[(triple>>18)&0x3F]
	*j++
	norm[*j] = b64Chars[(triple>>12)&0x3F]
	*j++
	if i > uSize+1 {
		norm[*j] = '='
	} else {
		norm[*j] = b64Chars[(triple>>6)&0x3F]
	}
	*j++
	if i > uSize {
		norm[*j] = '='
	} else {
		norm[*j] = b64Chars[triple&0x3F]
	}
	*j++
}

// decompressInternal executes the 7-stage linear pipeline for PRZM decompression.
func decompressInternal(src, dst, dict []byte, dictDesc *DictionaryDesc) (int, error) {
	var f FrameContext
	f.dict = dict
	f.dictDesc = dictDesc

	if err := stageParseHeader(src, cap(dst), &f); err != nil {
		return 0, normalizeError(err)
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
		return 0, normalizeError(err)
	}

	s := getScratch()
	err := executePipeline(src, dst, &f, s)
	if err != nil {
		putScratch(s)
		return 0, normalizeError(err)
	}
	n, err := stageFinalize(dst, cap(dst), &f, s)
	putScratch(s)
	return n, normalizeError(err)
}

func normalizeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, commands.ErrCorruptedFrame),
		errors.Is(err, distance.ErrCorruptedFrame),
		errors.Is(err, literals.ErrCorruptedFrame),
		errors.Is(err, ErrCorruptedFrame):
		return ErrCorruptedFrame
	case errors.Is(err, commands.ErrBufferTooSmall),
		errors.Is(err, distance.ErrBufferTooSmall),
		errors.Is(err, literals.ErrBufferTooSmall),
		errors.Is(err, ErrBufferTooSmall):
		return ErrBufferTooSmall
	case errors.Is(err, distance.ErrOffsetOOB), errors.Is(err, ErrOffsetOOB):
		return ErrOffsetOOB
	case errors.Is(err, distance.ErrUnsupportedMode), errors.Is(err, ErrUnsupportedMode):
		return ErrUnsupportedMode
	default:
		return err
	}
}

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

// FrameHeader exposes metadata of a parsed PRZM frame.
type FrameHeader struct {
	Flags             uint8
	UncompressedSize  int
	CompressedSize    int
	Stride            int
	WindowLog         uint8
	DictID            uint32
	NormMode          uint8
	DataOffset        int
	CmdStreamSize     int
	OffsetStreamSize  int
	LiteralStreamSize int
	HeaderSize        int
}

// ParseFrameHeader parses the frame header and directory of a PRZM frame.
func ParseFrameHeader(src []byte) (FrameHeader, error) {
	if len(src) < 8 {
		return FrameHeader{}, ErrCorruptedFrame
	}
	var f FrameContext
	if err := parseBasicHeader(src, int(f.uSize)+64, &f); err != nil && !errors.Is(err, ErrBufferTooSmall) {
		return FrameHeader{}, err
	}
	off, err := parseOptionalHeaders(src, 8, &f)
	if err != nil {
		return FrameHeader{}, err
	}
	if err := parseDirectory(src, off, &f); err != nil {
		return FrameHeader{}, err
	}
	if err := stageResolveSections(src, &f); err != nil {
		return FrameHeader{}, err
	}
	return FrameHeader{
		Flags:             f.flags,
		UncompressedSize:  int(f.uSize),
		CompressedSize:    len(src),
		Stride:            int(f.stride),
		WindowLog:         18,
		DictID:            f.dictID,
		DataOffset:        f.o1,
		CmdStreamSize:     f.lCmd,
		OffsetStreamSize:  f.lOff,
		LiteralStreamSize: f.l4,
		HeaderSize:        f.o1,
	}, nil
}

// StreamHeader exposes metadata of a multi-frame PRZS stream.
type StreamHeader struct {
	ChunkCount            uint32
	TotalUncompressedSize uint64
	Flags                 uint8
}

// ParseStreamHeader parses the 17-byte header of a PRZS stream.
func ParseStreamHeader(src []byte) (StreamHeader, error) {
	if len(src) < 17 {
		return StreamHeader{}, ErrCorruptedFrame
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != MagicPRZS {
		return StreamHeader{}, ErrInvalidMagic
	}
	flags := src[4]
	chunkCount := binary.LittleEndian.Uint32(src[5:9])
	totalUSize := binary.LittleEndian.Uint64(src[9:17])
	return StreamHeader{
		ChunkCount:            chunkCount,
		TotalUncompressedSize: totalUSize,
		Flags:                 flags,
	}, nil
}

// UnpackStream unpacks an intermediate Section 2 commands or Section 3 offsets stream.
func UnpackStream(dst, src, _ []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	s := scratchPool.Get()
	defer scratchPool.Put(s)
	buf, err := commands.UnpackSection2Commands(src, s.op[:], s.meta[:], s.varBuf[:], dst)
	if err == nil {
		return len(buf), nil
	}
	nDist, errDist := distance.UnpackOffsets(src, dst)
	if errDist == nil {
		return nDist, nil
	}
	return 0, err
}
