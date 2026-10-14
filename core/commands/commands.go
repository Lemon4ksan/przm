// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package commands provides 3-way split command stream encoding, decoding, Mode 8 framing,
// and 8-context Bigram V-ANS entropy coding with strictly zero dynamic allocations.
package commands

import (
	"encoding/binary"

	"github.com/lemon4ksan/foundation/generic"

	"github.com/lemon4ksan/przm/core/literals"
)

// SplitCommands decomposes linear bytecode instructions into opStream, metaStream, and varStream.
func SplitCommands(src, op, meta, v []byte) (nOp, nM, nV int, err error) {
	var srcPtr int
	sLen := len(src)
	for srcPtr < sLen {
		if nOp >= len(op) {
			return 0, 0, 0, ErrBufferTooSmall
		}
		opcode := src[srcPtr]
		srcPtr++
		op[nOp] = opcode
		nOp++

		if opcode < 0x80 {
			continue
		}
		if opcode < 0xC0 {
			if (opcode & 15) == 15 {
				if err := consumeVarint(src, &srcPtr, v, &nV); err != nil {
					return 0, 0, 0, err
				}
			}
			continue
		}
		if err := splitExtendedOp(opcode, src, &srcPtr, meta, &nM, v, &nV); err != nil {
			return 0, 0, 0, err
		}
	}
	return nOp, nM, nV, nil
}

func splitExtendedOp(opcode byte, src []byte, sp *int, meta []byte, mp *int, v []byte, vp *int) error {
	if opcode <= 0xC3 {
		return splitLongRepeat(src, sp, meta, mp, v, vp)
	}
	if opcode <= 0xCD {
		return splitBracketMatch(src, sp, meta, mp)
	}
	if opcode == 0xCE {
		return splitDiffLattice(src, sp, meta, mp, v, vp)
	}
	if opcode == 0xCF {
		return splitRFCTransform(src, sp, meta, mp, v, vp)
	}
	if opcode <= 0xD7 {
		return splitStrideMatch(src, sp, meta, mp)
	}
	if opcode <= 0xE8 {
		return splitDeltaMask(src, sp, meta, mp, v, vp)
	}
	if opcode == 0xE9 {
		return splitExternalDict(src, sp, meta, mp, v, vp)
	}
	return splitSpecialOp(opcode, src, sp, v, vp)
}

func splitLongRepeat(src []byte, sp *int, meta []byte, mp *int, v []byte, vp *int) error {
	if *sp >= len(src) || *mp >= len(meta) {
		return ErrCorruptedFrame
	}
	tok := src[*sp]
	*sp++
	meta[*mp] = tok
	*mp++
	if (tok & 15) == 15 {
		return consumeVarint(src, sp, v, vp)
	}
	return nil
}

func splitBracketMatch(src []byte, sp *int, meta []byte, mp *int) error {
	if *sp >= len(src) || *mp >= len(meta) {
		return ErrCorruptedFrame
	}
	meta[*mp] = src[*sp]
	*sp++
	*mp++
	return nil
}

func splitDiffLattice(src []byte, sp *int, meta []byte, mp *int, v []byte, vp *int) error {
	if *sp+2 > len(src) || *mp+2 > len(meta) {
		return ErrCorruptedFrame
	}
	desc := src[*sp]
	*sp++
	delta := src[*sp]
	*sp++
	meta[*mp] = desc
	*mp++
	meta[*mp] = delta
	*mp++
	if ((desc>>3)&7) == 7 && consumeVarint(src, sp, v, vp) != nil {
		return ErrCorruptedFrame
	}
	if (desc&7) == 7 && consumeVarint(src, sp, v, vp) != nil {
		return ErrCorruptedFrame
	}
	return nil
}

func splitRFCTransform(src []byte, sp *int, meta []byte, mp *int, v []byte, vp *int) error {
	if *sp >= len(src) || *mp >= len(meta) {
		return ErrCorruptedFrame
	}
	b0 := src[*sp]
	*sp++
	meta[*mp] = b0
	*mp++
	if b0 >= 128 {
		if *sp >= len(src) || *vp >= len(v) {
			return ErrCorruptedFrame
		}
		v[*vp] = src[*sp]
		*sp++
		*vp++
		return nil
	}
	return consumeVarint(src, sp, v, vp)
}

func splitStrideMatch(src []byte, sp *int, meta []byte, mp *int) error {
	if *sp >= len(src) || *mp >= len(meta) {
		return ErrCorruptedFrame
	}
	meta[*mp] = src[*sp]
	*sp++
	*mp++
	return nil
}

func splitDeltaMask(src []byte, sp *int, meta []byte, mp *int, v []byte, vp *int) error {
	if *sp >= len(src) || *mp >= len(meta) {
		return ErrCorruptedFrame
	}
	blen := src[*sp]
	*sp++
	meta[*mp] = blen
	*mp++
	if blen == 255 {
		return consumeVarint(src, sp, v, vp)
	}
	return nil
}

func splitExternalDict(src []byte, sp *int, meta []byte, mp *int, v []byte, vp *int) error {
	if *sp >= len(src) || *mp >= len(meta) {
		return ErrCorruptedFrame
	}
	tok := src[*sp]
	*sp++
	meta[*mp] = tok
	*mp++
	if (tok & 15) == 15 {
		return consumeVarint(src, sp, v, vp)
	}
	return nil
}

func splitSpecialOp(opcode byte, src []byte, sp *int, v []byte, vp *int) error {
	if opcode == 0xF0 || opcode == 0xF1 {
		return consumeVarint(src, sp, v, vp)
	}
	if opcode >= 0xF3 && opcode <= 0xFA {
		return nil
	}
	return ErrCorruptedFrame
}

func consumeVarint(src []byte, sp *int, v []byte, vp *int) error {
	p, outP := *sp, *vp
	for range 4 {
		if p >= len(src) || outP >= len(v) {
			return ErrCorruptedFrame
		}
		b := src[p]
		p++
		v[outP] = b
		outP++
		if (b & 0x80) == 0 {
			*sp, *vp = p, outP
			return nil
		}
	}
	return ErrCorruptedFrame
}

// JoinCommands reconstitutes a linear bytecode sequence from opStream, metaStream, and varStream.
func JoinCommands(dst, op, meta, v []byte, origLen int) error {
	var opP, metaP, varP, dstP int
	for opP < len(op) && dstP < origLen {
		opcode := op[opP]
		opP++
		if dstP >= len(dst) {
			return ErrBufferTooSmall
		}
		dst[dstP] = opcode
		dstP++
		if err := demuxStep(opcode, meta, &metaP, v, &varP, dst, &dstP); err != nil {
			return err
		}
	}
	if dstP != origLen {
		return ErrCorruptedFrame
	}
	return nil
}

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

func copyLEB128Raw(src []byte, sp *int, dst []byte, dp *int) error {
	p, outP := *sp, *dp
	for range 4 {
		if p >= len(src) || outP >= len(dst) {
			return ErrCorruptedFrame
		}
		b := src[p]
		p++
		dst[outP] = b
		outP++
		if (b & 0x80) == 0 {
			*sp, *dp = p, outP
			return nil
		}
	}
	return ErrCorruptedFrame
}

type mode8TmpBufType [2 * 1024 * 1024]byte

var mode8TmpPool = generic.NewPool[mode8TmpBufType](func() *mode8TmpBufType {
	return new(mode8TmpBufType)
})

func packSubstream(sub, tmp, dst []byte) (int, error) {
	flatLen := 1 + len(sub)
	bestLen := flatLen
	useMeta8 := false
	useAdaptive := false

	if len(sub) >= 24 && len(sub) <= len(tmp) {
		encLen, err := EncodeVansMeta8(sub, tmp)
		if err == nil && encLen < bestLen {
			bestLen = encLen
			useMeta8 = true
			copy(dst, tmp[:encLen])
		}
	}

	if len(sub) >= 16 && len(sub) <= len(tmp) {
		encLen, err := literals.EncodeAdaptiveVANS4x(sub, tmp)
		if err == nil && encLen < bestLen {
			bestLen = encLen
			useAdaptive = true
			useMeta8 = false
			copy(dst, tmp[:encLen])
		}
	}

	if useMeta8 || useAdaptive {
		return bestLen, nil
	}

	if flatLen > len(dst) {
		return 0, ErrBufferTooSmall
	}
	dst[0] = 0x09
	copy(dst[1:], sub)
	return flatLen, nil
}

// PackCommandsMode8 serializes opStream, metaStream, and varStream into a 13-byte Mode 8 frame.
func PackCommandsMode8(op, meta, v []byte, origLen int, dst []byte) (int, error) {
	if len(dst) < 13 {
		return 0, ErrBufferTooSmall
	}

	tmpBuf := mode8TmpPool.Get()
	pos := 13
	lOp, err := packSubstream(op, tmpBuf[:], dst[pos:])
	if err != nil {
		mode8TmpPool.Put(tmpBuf)
		return 0, err
	}
	pos += lOp

	lM, err := packSubstream(meta, tmpBuf[:], dst[pos:])
	if err != nil {
		mode8TmpPool.Put(tmpBuf)
		return 0, err
	}
	pos += lM

	lVar, err := packSubstream(v, tmpBuf[:], dst[pos:])
	if err != nil {
		mode8TmpPool.Put(tmpBuf)
		return 0, err
	}
	pos += lVar
	mode8TmpPool.Put(tmpBuf)

	dst[0] = 0x08
	binary.LittleEndian.PutUint32(dst[1:5], uint32(origLen))
	binary.LittleEndian.PutUint32(dst[5:9], uint32(lOp))
	binary.LittleEndian.PutUint32(dst[9:13], uint32(lM))

	return pos, nil
}

// UnpackCommandsMode8 parses a Mode 8 frame, extracting origLen and substreams into op, meta, v.
func UnpackCommandsMode8(src, op, meta, v []byte) (origLen, nOp, nM, nV int, err error) {
	if len(src) < 13 || src[0] != 0x08 {
		return 0, 0, 0, 0, ErrCorruptedFrame
	}
	origLen = int(binary.LittleEndian.Uint32(src[1:5]))
	lOp := int(binary.LittleEndian.Uint32(src[5:9]))
	lM := int(binary.LittleEndian.Uint32(src[9:13]))
	if 13+lOp+lM > len(src) {
		return 0, 0, 0, 0, ErrCorruptedFrame
	}
	lVar := len(src) - 13 - lOp - lM

	nOp, err = unpackSubstreamOrVans(src[13:13+lOp], op, lOp)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	nM, err = unpackSubstreamOrVans(src[13+lOp:13+lOp+lM], meta, lM)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	nV, err = unpackSubstreamOrVans(src[13+lOp+lM:13+lOp+lM+lVar], v, lVar)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	return origLen, nOp, nM, nV, nil
}

func unpackSubstreamOrVans(src, dst []byte, rawLen int) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(src) >= 8 && src[0] == 'V' && src[1] == 'A' && src[2] == 0x01 {
		expLen := int(binary.LittleEndian.Uint32(src[4:8]))
		if expLen > len(dst) {
			return 0, ErrBufferTooSmall
		}
		if (src[3] & 0x40) != 0 {
			if err := DecodeVansMeta8(src, dst, expLen); err != nil {
				return 0, err
			}
			return expLen, nil
		}
		if src[3] == 0 {
			if err := literals.DecodeAdaptiveVANS4x(src, dst, expLen); err != nil {
				return 0, err
			}
			return expLen, nil
		}
	}
	if src[0] == 0x09 {
		n := len(src) - 1
		if n > len(dst) {
			return 0, ErrBufferTooSmall
		}
		copy(dst, src[1:])
		return n, nil
	}
	if src[0] == 0x00 {
		if len(src) < 5 {
			return 0, ErrCorruptedFrame
		}
		expLen := int(binary.LittleEndian.Uint32(src[1:5]))
		if expLen > len(dst) || 5+expLen > len(src) {
			return 0, ErrCorruptedFrame
		}
		copy(dst, src[5:5+expLen])
		return expLen, nil
	}
	if rawLen > len(dst) {
		return 0, ErrBufferTooSmall
	}
	copy(dst, src[:rawLen])
	return rawLen, nil
}

// DemultiplexCommands reconstructs the canonical command stream from tri-stream inputs.
func DemultiplexCommands(opStream, meta, vStream, dst []byte, expected int) error {
	return JoinCommands(dst, opStream, meta, vStream, expected)
}

var byteSlicePool = generic.NewSlicePool[byte](65536)

func ensureCap(b []byte, req int) []byte {
	if cap(b) < req {
		return make([]byte, req)
	}
	return b[:req]
}

// PackageCommandsSection encodes Section 2 via Mode 8 or flat mode 0x09 using pooled buffers.
func PackageCommandsSection(cmds []byte) []byte {
	if len(cmds) == 0 {
		return nil
	}
	opBuf := byteSlicePool.Get()
	defer byteSlicePool.Put(opBuf)
	metaBuf := byteSlicePool.Get()
	defer byteSlicePool.Put(metaBuf)
	varBuf := byteSlicePool.Get()
	defer byteSlicePool.Put(varBuf)
	dstBuf := byteSlicePool.Get()
	defer byteSlicePool.Put(dstBuf)

	opBuf = ensureCap(opBuf, len(cmds))
	metaBuf = ensureCap(metaBuf, len(cmds))
	varBuf = ensureCap(varBuf, len(cmds))
	dstBuf = ensureCap(dstBuf, 13+len(cmds)+64)

	nOp, nM, nV, err := SplitCommands(cmds, opBuf[:len(cmds)], metaBuf[:len(cmds)], varBuf[:len(cmds)])
	if err == nil {
		nPack, pErr := PackCommandsMode8(opBuf[:nOp], metaBuf[:nM], varBuf[:nV], len(cmds), dstBuf[:cap(dstBuf)])
		if pErr == nil && nPack < len(cmds) {
			out := make([]byte, nPack)
			copy(out, dstBuf[:nPack])
			return out
		}
	}
	out := make([]byte, 1+len(cmds))
	out[0] = 0x09
	copy(out[1:], cmds)
	return out
}

// TrellisEdge defines a parsing transition edge in the Trellis DAG.
type TrellisEdge struct {
	From   uint16
	Length uint16
	Dist   uint32
	Kind   uint8 // 0: lit, 1: rep, 2: bracket, 3: stride, 4: explicit, 5: rfc, 6: difflattice
	Arg    uint8 // k for rep, b for bracket, m for stride, mode for difflattice
}

// AppendLEB128 appends uint32 in variable-length LEB128 format.
func AppendLEB128(buf []byte, v uint32) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v|0x80))
		v >>= 7
	}
	buf = append(buf, byte(v))
	return buf
}

// EmitBlockCommands generates linear bytecode tokens from the optimal Trellis path.
func EmitBlockCommands(edges []TrellisEdge, block []byte, bMin int,
	cmdsOut, litsOut *[]byte, offsOut *[]uint32,
) {
	var pendingLits []byte
	for _, e := range edges {
		if e.Kind == 0 {
			b := block[int(e.From)]
			pendingLits = append(pendingLits, b)
			*litsOut = append(*litsOut, b)
			continue
		}
		emitMatchWithLiterals(e, &pendingLits, bMin, cmdsOut, litsOut, offsOut)
	}
	if len(pendingLits) > 0 {
		EmitLiteralRun(pendingLits, cmdsOut, litsOut)
	}
}

func emitMatchWithLiterals(e TrellisEdge, pendingLits *[]byte, bMin int,
	cmdsOut, litsOut *[]byte, offsOut *[]uint32,
) {
	switch e.Kind {
	case 1:
		emitRepeatMatch(e, pendingLits, bMin, cmdsOut, litsOut)
	case 2:
		emitFixedMatch(byte(0xC4+int(e.Arg)), e, pendingLits, bMin, cmdsOut, litsOut)
	case 3:
		emitFixedMatch(byte(0xD0+int(e.Arg-1)), e, pendingLits, bMin, cmdsOut, litsOut)
	case 4:
		emitExplicitMatch(e, pendingLits, bMin, cmdsOut, litsOut, offsOut)
	case 5:
		emitRFCWordMatch(e, pendingLits, cmdsOut, litsOut)
	case 6:
		emitDiffLatticeMatch(e, pendingLits, bMin, cmdsOut, litsOut)
	}
	*pendingLits = nil
}

func emitRFCWordMatch(e TrellisEdge, pendingLits, cmdsOut, litsOut *[]byte) {
	if len(*pendingLits) > 0 {
		EmitLiteralRun(*pendingLits, cmdsOut, litsOut)
		*pendingLits = nil
	}
	if e.Arg != 0 {
		tau := byte(e.Arg)
		*cmdsOut = append(*cmdsOut, 0xCF, tau)
		*cmdsOut = AppendLEB128(*cmdsOut, e.Dist)
		return
	}
	p := e.Dist
	*cmdsOut = append(*cmdsOut, 0xCF, byte(0x80|((p>>7)&0x7F)), byte(p&0x7F))
}

func emitDiffLatticeMatch(e TrellisEdge, pendingLits *[]byte, bMin int, cmdsOut, litsOut *[]byte) {
	nLit := len(*pendingLits)
	if nLit > 6 {
		EmitLiteralRun(*pendingLits, cmdsOut, litsOut)
		*pendingLits = nil
		nLit = 0
	}
	mode := e.Arg
	delta := byte(e.Dist)
	lMatch := int(e.Length)
	diff := lMatch - bMin
	if diff < 7 {
		desc := (mode << 6) | (byte(nLit) << 3) | byte(diff)
		*cmdsOut = append(*cmdsOut, 0xCE, desc, delta)
	} else {
		desc := (mode << 6) | (byte(nLit) << 3) | 7
		*cmdsOut = append(*cmdsOut, 0xCE, desc, delta)
		*cmdsOut = AppendLEB128(*cmdsOut, uint32(diff-7))
	}
}

func emitRepeatMatch(e TrellisEdge, pendingLits *[]byte, bMin int, cmdsOut, litsOut *[]byte) {
	nLit := len(*pendingLits)
	lMatch := int(e.Length)
	if nLit <= 7 && lMatch <= 3+bMin {
		op := byte((nLit << 4) | ((lMatch - bMin) << 2) | int(e.Arg))
		*cmdsOut = append(*cmdsOut, op)
		return
	}
	if nLit > 15 {
		EmitLiteralRun(*pendingLits, cmdsOut, litsOut)
		nLit = 0
	}
	op := byte(0xC0 | int(e.Arg))
	if lMatch-bMin < 15 {
		tok := byte((nLit << 4) | (lMatch - bMin))
		*cmdsOut = append(*cmdsOut, op, tok)
		return
	}
	tok := byte((nLit << 4) | 15)
	*cmdsOut = append(*cmdsOut, op, tok)
	*cmdsOut = AppendLEB128(*cmdsOut, uint32(lMatch-bMin-15))
}

func emitFixedMatch(op byte, e TrellisEdge, pendingLits *[]byte, bMin int, cmdsOut, litsOut *[]byte) {
	nLit := len(*pendingLits)
	lMatch := int(e.Length)
	if nLit > 15 {
		EmitLiteralRun(*pendingLits, cmdsOut, litsOut)
		nLit = 0
	}
	tok := byte((nLit << 4) | (lMatch - bMin))
	*cmdsOut = append(*cmdsOut, op, tok)
}

func emitExplicitMatch(e TrellisEdge, pendingLits *[]byte, bMin int,
	cmdsOut, litsOut *[]byte, offsOut *[]uint32,
) {
	nLit := len(*pendingLits)
	lMatch := int(e.Length)
	if nLit > 3 {
		EmitLiteralRun(*pendingLits, cmdsOut, litsOut)
		*pendingLits = nil
		nLit = 0
	}
	*offsOut = append(*offsOut, e.Dist)
	if lMatch-bMin < 15 {
		op := byte(0x80 | (nLit << 4) | (lMatch - bMin))
		*cmdsOut = append(*cmdsOut, op)
		return
	}
	op := byte(0x80 | (nLit << 4) | 15)
	*cmdsOut = append(*cmdsOut, op)
	*cmdsOut = AppendLEB128(*cmdsOut, uint32(lMatch-bMin-15))
}

// EmitLiteralRun encodes pure literal sequences into standalone tokens.
func EmitLiteralRun(lits []byte, cmdsOut, litsOut *[]byte) {
	n := len(lits)
	if n == 0 {
		return
	}
	if n <= 8 {
		*cmdsOut = append(*cmdsOut, byte(0xF2+n))
		return
	}
	*cmdsOut = append(*cmdsOut, 0xF0)
	*cmdsOut = AppendLEB128(*cmdsOut, uint32(n))
}

// UnpackSection2Commands decodes Section 2 command stream according to container framing mode.
func UnpackSection2Commands(s2, op, meta, varBuf, cmds []byte) ([]byte, error) {
	if len(s2) == 0 {
		return nil, nil
	}
	switch s2[0] {
	case 0x08:
		if len(s2) < 13 {
			return nil, ErrCorruptedFrame
		}
		origLen, nOp, nM, nV, err := UnpackCommandsMode8(s2, op, meta, varBuf)
		if err != nil {
			return nil, ErrCorruptedFrame
		}
		if origLen > len(cmds) {
			return nil, ErrBufferTooSmall
		}
		if err := DemultiplexCommands(op[:nOp], meta[:nM], varBuf[:nV], cmds, origLen); err != nil {
			return nil, ErrCorruptedFrame
		}
		return cmds[:origLen], nil
	case 0x09:
		return s2[1:], nil
	case 0x00:
		if len(s2) < 5 {
			return nil, ErrCorruptedFrame
		}
		origLen := int(binary.LittleEndian.Uint32(s2[1:5]))
		if 5+origLen > len(s2) {
			return nil, ErrCorruptedFrame
		}
		return s2[5 : 5+origLen], nil
	default:
		return s2, nil
	}
}
