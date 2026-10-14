// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package clean_core provides 3-way split command stream encoding, decoding, and Mode 8 container framing.
// Strictly adheres to the Clean Room Specification with zero dynamic memory allocation.
package clean_core

import (
	"encoding/binary"
)

// SplitCommands decomposes linear bytecode instructions into opStream, metaStream, and varStream.
func SplitCommands(src []byte, op, meta, v []byte) (nOp, nM, nV int, err error) {
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
	for i := 0; i < 4; i++ {
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
	return demultiplexCommands(op, meta, v, dst, origLen)
}

// PackCommandsMode8 serializes opStream, metaStream, and varStream into a 13-byte Mode 8 frame.
func PackCommandsMode8(op, meta, v []byte, origLen int, dst []byte) (int, error) {
	totalLen := 13 + len(op) + len(meta) + len(v)
	if totalLen > len(dst) {
		return 0, ErrBufferTooSmall
	}
	dst[0] = 0x08
	binary.LittleEndian.PutUint32(dst[1:5], uint32(origLen))
	binary.LittleEndian.PutUint32(dst[5:9], uint32(len(op)))
	binary.LittleEndian.PutUint32(dst[9:13], uint32(len(meta)))

	pos := 13
	copy(dst[pos:], op)
	pos += len(op)
	copy(dst[pos:], meta)
	pos += len(meta)
	copy(dst[pos:], v)
	return totalLen, nil
}

// UnpackCommandsMode8 parses a Mode 8 frame, extracting origLen and substreams into op, meta, v.
func UnpackCommandsMode8(src []byte, op, meta, v []byte) (origLen, nOp, nM, nV int, err error) {
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
	if len(src) >= 24 && src[0] == 'V' && src[1] == 'A' && src[2] == 0x01 && (src[3]&0x40) != 0 {
		expLen := int(binary.LittleEndian.Uint32(src[4:8]))
		if expLen > len(dst) {
			return 0, ErrBufferTooSmall
		}
		if err := DecodeVansMeta8(src, dst, expLen); err != nil {
			return 0, err
		}
		return expLen, nil
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
