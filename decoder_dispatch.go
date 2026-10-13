// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/lemon4ksan/foundation/silicon/simd"
)

// fastReadVarint decodes an unsigned varint with inline 1-byte, 2-byte, 3-byte, and 4-byte fast paths.
// In PRZM frames, offsets and match lengths <= 8 MiB are guaranteed <= 4 bytes.
func fastReadVarint(src []byte, ptr int) (int, int, error) {
	if ptr < 0 || ptr >= len(src) {
		return 0, ptr, fmt.Errorf("%w: truncated varint stream", ErrCorruptedFrame)
	}
	b0 := src[ptr]
	ptr++
	if b0 < 0x80 {
		return int(b0), ptr, nil
	}
	if ptr >= len(src) {
		return 0, ptr, fmt.Errorf("%w: truncated varint stream", ErrCorruptedFrame)
	}
	b1 := src[ptr]
	ptr++
	if b1 < 0x80 {
		return int(b0&0x7F) | (int(b1) << 7), ptr, nil
	}
	if ptr >= len(src) {
		return 0, ptr, fmt.Errorf("%w: truncated varint stream", ErrCorruptedFrame)
	}
	b2 := src[ptr]
	ptr++
	if b2 < 0x80 {
		return int(b0&0x7F) | (int(b1&0x7F) << 7) | (int(b2) << 14), ptr, nil
	}
	if ptr >= len(src) {
		return 0, ptr, fmt.Errorf("%w: truncated varint stream", ErrCorruptedFrame)
	}
	b3 := src[ptr]
	ptr++
	if b3 < 0x80 {
		v := int(b0&0x7F) | (int(b1&0x7F) << 7) | (int(b2&0x7F) << 14) | (int(b3) << 21)
		if v > MaxBlockSize {
			return 0, ptr, fmt.Errorf("%w: varint %d exceeds MaxBlockSize", ErrCorruptedFrame, v)
		}
		return v, ptr, nil
	}
	if ptr-4 < 0 || ptr-4 > len(src) {
		return 0, ptr, fmt.Errorf("%w: invalid varint offset", ErrCorruptedFrame)
	}
	extra, n := binary.Uvarint(src[ptr-4:])
	if n <= 0 || extra > uint64(MaxBlockSize) {
		return 0, ptr, fmt.Errorf("%w: corrupted varint encoding", ErrCorruptedFrame)
	}
	return int(extra), ptr - 4 + n, nil
}

// copyMatch copies mLen bytes from dst[srcIdx] to dst[dstPos] with zero bounds checks.
// Assumes dst has at least 64 bytes padding beyond uSize.
func copyMatch(baseDst unsafe.Pointer, dstPos, srcIdx, mLen, d int) {
	pSrc := unsafe.Add(baseDst, srcIdx)
	pDst := unsafe.Add(baseDst, dstPos)

	if d >= mLen {
		if d >= 16 {
			for mLen >= 32 {
				*(*[4]uint64)(pDst) = *(*[4]uint64)(pSrc)
				pSrc = unsafe.Add(pSrc, 32)
				pDst = unsafe.Add(pDst, 32)
				mLen -= 32
			}
			if mLen > 16 {
				*(*[2]uint64)(pDst) = *(*[2]uint64)(pSrc)
				pSrc = unsafe.Add(pSrc, 16)
				pDst = unsafe.Add(pDst, 16)
				mLen -= 16
			}
			if mLen > 8 {
				*(*[2]uint64)(pDst) = *(*[2]uint64)(pSrc)
				return
			}
			if mLen > 0 {
				*(*uint64)(pDst) = *(*uint64)(pSrc)
				return
			}
			return
		}
		if d >= 8 {
			for mLen > 8 {
				*(*uint64)(pDst) = *(*uint64)(pSrc)
				pSrc = unsafe.Add(pSrc, 8)
				pDst = unsafe.Add(pDst, 8)
				mLen -= 8
			}
			if mLen > 0 {
				*(*uint64)(pDst) = *(*uint64)(pSrc)
			}
			return
		}
		if mLen <= 4 {
			switch mLen {
			case 1:
				*(*byte)(pDst) = *(*byte)(pSrc)
			case 2:
				*(*uint16)(pDst) = *(*uint16)(pSrc)
			case 3:
				*(*uint16)(pDst) = *(*uint16)(pSrc)
				*(*byte)(unsafe.Add(pDst, 2)) = *(*byte)(unsafe.Add(pSrc, 2))
			case 4:
				*(*uint32)(pDst) = *(*uint32)(pSrc)
			}
			return
		}
		*(*uint32)(pDst) = *(*uint32)(pSrc)
		*(*uint32)(unsafe.Add(pDst, mLen-4)) = *(*uint32)(unsafe.Add(pSrc, mLen-4))
		return
	}

	if d >= 16 {
		for mLen > 0 {
			*(*[2]uint64)(pDst) = *(*[2]uint64)(pSrc)
			pSrc = unsafe.Add(pSrc, 16)
			pDst = unsafe.Add(pDst, 16)
			if mLen <= 16 {
				return
			}
			mLen -= 16
		}
		return
	}

	if d >= 8 {
		for mLen > 0 {
			*(*uint64)(pDst) = *(*uint64)(pSrc)
			pSrc = unsafe.Add(pSrc, 8)
			pDst = unsafe.Add(pDst, 8)
			if mLen <= 8 {
				return
			}
			mLen -= 8
		}
		return
	}

	if d == 1 {
		pattern := simd.RepeatByte0x01 * uint64(*(*byte)(pSrc))
		for mLen > 32 {
			*(*[4]uint64)(pDst) = [4]uint64{pattern, pattern, pattern, pattern}
			pDst = unsafe.Add(pDst, 32)
			mLen -= 32
		}
		for mLen > 0 {
			*(*uint64)(pDst) = pattern
			pDst = unsafe.Add(pDst, 8)
			if mLen <= 8 {
				return
			}
			mLen -= 8
		}
		return
	}

	if d == 2 {
		w := uint64(*(*uint16)(pSrc))
		pattern := w | (w << 16) | (w << 32) | (w << 48)
		for mLen > 0 {
			*(*uint64)(pDst) = pattern
			pDst = unsafe.Add(pDst, 8)
			if mLen <= 8 {
				return
			}
			mLen -= 8
		}
		return
	}

	if d == 4 {
		w := uint64(*(*uint32)(pSrc))
		pattern := w | (w << 32)
		for mLen > 0 {
			*(*uint64)(pDst) = pattern
			pDst = unsafe.Add(pDst, 8)
			if mLen <= 8 {
				return
			}
			mLen -= 8
		}
		return
	}

	for i := 0; i < mLen; i++ {
		*(*byte)(unsafe.Add(pDst, i)) = *(*byte)(unsafe.Add(pSrc, i))
	}
}

// execMatch safely executes a match copy, transparently handling positive offsets in outBuf
// and negative offsets into the raw prefix dictionary buffer.
func execMatch(baseDst unsafe.Pointer, outBuf, rawDict []byte, dstPos, d, mLen int) error {
	if d <= 0 || mLen <= 0 || dstPos < 0 || dstPos+mLen > len(outBuf) {
		return fmt.Errorf("%w: invalid match bounds (d=%d, dstPos=%d, mLen=%d)", ErrCorruptedFrame, d, dstPos, mLen)
	}
	srcIdx := dstPos - d
	if srcIdx >= 0 {
		copyMatch(baseDst, dstPos, srcIdx, mLen, d)
		return nil
	}
	if len(rawDict) == 0 {
		return fmt.Errorf(
			"%w: invalid match distance %d (srcIdx=%d, dstPos=%d, mLen=%d)",
			ErrCorruptedFrame,
			d,
			srcIdx,
			dstPos,
			mLen,
		)
	}
	dictOffset := len(rawDict) + srcIdx
	if dictOffset < 0 || dictOffset >= len(rawDict) {
		return fmt.Errorf(
			"%w: dictionary offset %d out of bounds (dictLen=%d)",
			ErrCorruptedFrame,
			dictOffset,
			len(rawDict),
		)
	}
	dictAvail := len(rawDict) - dictOffset
	copyFromDict := min(mLen, dictAvail)
	copy(outBuf[dstPos:dstPos+copyFromDict], rawDict[dictOffset:dictOffset+copyFromDict])
	if mLen > copyFromDict {
		remLen := mLen - copyFromDict
		copyStart := dstPos + copyFromDict
		for i := range remLen {
			outBuf[copyStart+i] = outBuf[i]
		}
	}
	return nil
}

// frameDecoder manages the decompress state machine and executes PRZM bytecodes.
type frameDecoder struct {
	baseDst   unsafe.Pointer
	outBuf    []byte
	allocSize int
	dstPos    int

	cmds   []byte
	cmdPtr int
	cmdEnd int

	lits   []byte
	litPtr int

	offs   []byte
	offPtr int

	rep0         int
	rep1         int
	rep2         int
	stride       int
	minMatchBase int

	rawDict    []byte
	activeDict [][]byte
}

func (d *frameDecoder) decode() error {
	dstPos := d.dstPos
	allocSize := d.allocSize
	baseDst := d.baseDst
	outBuf := d.outBuf
	rawDict := d.rawDict

	cmds := d.cmds
	cmdPtr := d.cmdPtr
	cmdEnd := d.cmdEnd

	lits := d.lits
	litPtr := d.litPtr
	litsLen := len(lits)

	offs := d.offs
	offPtr := d.offPtr

	rep0 := d.rep0
	rep1 := d.rep1
	rep2 := d.rep2
	minMatchBase := d.minMatchBase
	outBufLen := len(outBuf)

	for dstPos < allocSize && cmdPtr < cmdEnd {
		op := cmds[cmdPtr]
		cmdPtr++

		if op < 0x80 {
			// Fast-Path 1: Fused Repeat Token (00 LLL MM KK)
			litLen := int((op >> 4) & 0x07)
			mLen := int((op>>2)&0x03) + minMatchBase
			k := int(op & 0x03)

			if litLen > 0 {
				if litPtr+litLen > litsLen || dstPos+litLen > outBufLen {
					return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				if litPtr+8 <= litsLen && dstPos+8 <= outBufLen {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else {
					copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				}
				litPtr += litLen
				dstPos += litLen
			}

			var dist int
			switch k {
			case 0:
				dist = rep0
			case 1:
				dist = rep1
				rep1 = rep0
				rep0 = dist
			case 2:
				dist = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = dist
			default:
				return fmt.Errorf("%w: invalid repeat index %d", ErrCorruptedFrame, k)
			}

			srcIdx := dstPos - dist
			if dist > 0 && srcIdx >= 0 && dstPos+mLen <= outBufLen {
				if dist >= 8 {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Add(baseDst, srcIdx))
				} else {
					copyMatch(baseDst, dstPos, srcIdx, mLen, dist)
				}
			} else {
				if err := execMatch(baseDst, outBuf, rawDict, dstPos, dist, mLen); err != nil {
					return err
				}
			}
			dstPos += mLen
			continue
		}

		if op < 0xC0 {
			// Fast-Path 2: Fused Extended Match (10 LL MMMM)
			litLen := int((op >> 4) & 0x03)
			baseLen := int(op & 0x0F)
			mLen := baseLen + minMatchBase
			if baseLen == 15 {
				var extra int
				var err error
				extra, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return err
				}
				mLen = 15 + minMatchBase + extra
			}

			if litLen > 0 {
				if litPtr+litLen > litsLen || dstPos+litLen > outBufLen {
					return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				if litPtr+8 <= litsLen && dstPos+8 <= outBufLen {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else {
					copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				}
				litPtr += litLen
				dstPos += litLen
			}

			dist, newOffPtr, err := fastReadVarint(offs, offPtr)
			if err != nil {
				return err
			}
			offPtr = newOffPtr

			rep2 = rep1
			rep1 = rep0
			rep0 = dist

			srcIdx := dstPos - dist
			if dist >= 16 && mLen <= 16 && srcIdx >= 0 && dstPos+16 <= outBufLen {
				*(*[2]uint64)(unsafe.Add(baseDst, dstPos)) = *(*[2]uint64)(unsafe.Add(baseDst, srcIdx))
			} else if dist >= 32 && mLen <= 32 && srcIdx >= 0 && dstPos+32 <= outBufLen {
				*(*[4]uint64)(unsafe.Add(baseDst, dstPos)) = *(*[4]uint64)(unsafe.Add(baseDst, srcIdx))
			} else if dist > 0 && srcIdx >= 0 && dstPos+mLen <= outBufLen {
				copyMatch(baseDst, dstPos, srcIdx, mLen, dist)
			} else {
				if err := execMatch(baseDst, outBuf, rawDict, dstPos, dist, mLen); err != nil {
					return err
				}
			}
			dstPos += mLen
			continue
		}

		if op >= 0xC4 && op <= 0xCD {
			bracket := int(op - 0xC4)
			if cmdPtr >= cmdEnd {
				return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			token := cmds[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			baseLen := int(token & 0x0F)
			mLen := baseLen + minMatchBase

			if litLen > 0 {
				if litPtr+litLen > litsLen || dstPos+litLen > outBufLen {
					return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				litPtr += litLen
				dstPos += litLen
			}

			var dist int
			if bracket < 8 {
				dist = bracket + 1
			} else if bracket == 8 {
				dist = 16
			} else {
				dist = 32
			}

			rep2 = rep1
			rep1 = rep0
			rep0 = dist

			srcIdx := dstPos - dist
			if dist >= 16 && mLen <= 16 && srcIdx >= 0 && dstPos+16 <= outBufLen {
				*(*[2]uint64)(unsafe.Add(baseDst, dstPos)) = *(*[2]uint64)(unsafe.Add(baseDst, srcIdx))
			} else if dist >= 32 && mLen <= 32 && srcIdx >= 0 && dstPos+32 <= outBufLen {
				*(*[4]uint64)(unsafe.Add(baseDst, dstPos)) = *(*[4]uint64)(unsafe.Add(baseDst, srcIdx))
			} else if dist > 0 && srcIdx >= 0 && dstPos+mLen <= outBufLen {
				copyMatch(baseDst, dstPos, srcIdx, mLen, dist)
			} else {
				if err := execMatch(baseDst, outBuf, rawDict, dstPos, dist, mLen); err != nil {
					return err
				}
			}
			dstPos += mLen
			continue
		}

		if op == 0xCE {
			if cmdPtr+1 >= cmdEnd {
				return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			desc := cmds[cmdPtr]
			deltaByte := cmds[cmdPtr+1]
			cmdPtr += 2

			mode := int((desc >> 6) & 0x03)
			litLen := int((desc >> 3) & 0x07)
			baseLen := int(desc & 0x07)
			mLen := baseLen + minMatchBase

			if litLen == 7 {
				extra, newCmdPtr, err := fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return err
				}
				cmdPtr = newCmdPtr
				litLen += extra
			}
			if baseLen == 7 {
				extra, newCmdPtr, err := fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return err
				}
				cmdPtr = newCmdPtr
				mLen += extra
			}

			if litLen > 0 {
				if litPtr+litLen > litsLen || dstPos+litLen > outBufLen {
					return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				litPtr += litLen
				dstPos += litLen
			}

			var dist int
			deltaSigned := int(int8(deltaByte))
			switch mode {
			case 0:
				dist = rep0 + deltaSigned
			case 1:
				dist = rep1 + deltaSigned
			case 2:
				dist = rep2 + deltaSigned
			case 3:
				if d.stride > 1 {
					dist = rep0 + deltaSigned*d.stride
				} else {
					dist = int(deltaByte) + 1
				}
			}

			rep2 = rep1
			rep1 = rep0
			rep0 = dist

			srcIdx := dstPos - dist
			if dist >= 16 && mLen <= 16 && srcIdx >= 0 && dstPos+16 <= outBufLen {
				*(*[2]uint64)(unsafe.Add(baseDst, dstPos)) = *(*[2]uint64)(unsafe.Add(baseDst, srcIdx))
			} else if dist >= 32 && mLen <= 32 && srcIdx >= 0 && dstPos+32 <= outBufLen {
				*(*[4]uint64)(unsafe.Add(baseDst, dstPos)) = *(*[4]uint64)(unsafe.Add(baseDst, srcIdx))
			} else if dist > 0 && srcIdx >= 0 && dstPos+mLen <= outBufLen {
				copyMatch(baseDst, dstPos, srcIdx, mLen, dist)
			} else {
				if err := execMatch(baseDst, outBuf, rawDict, dstPos, dist, mLen); err != nil {
					return err
				}
			}
			dstPos += mLen
			continue
		}

		// Specialized Opcodes (0xC0 .. 0xFF): sync back state before calling specialized opcode handlers
		d.dstPos = dstPos
		d.cmdPtr = cmdPtr
		d.litPtr = litPtr
		d.offPtr = offPtr
		d.rep0 = rep0
		d.rep1 = rep1
		d.rep2 = rep2

		var err error
		switch {
		case op >= 0xC0 && op <= 0xC3:
			err = d.decodeLongRepeat(op)
		case op >= 0xD0 && op <= 0xD7:
			err = d.decodeStrideMatch(op)
		case op >= 0xE0 && op <= 0xE7:
			err = d.decodeDeltaMaskLocality(op)
		case op == 0xE8:
			err = d.decodeDeltaMaskExplicit()
		case op == OpcodePrefixRFCTransform:
			err = d.decodeRFCTransform()
		case op == OpcodePrefixRawDict:
			err = d.decodeRawDictMatch()
		case op == 0xF0:
			err = d.decodeLiteralRun()
		case op == 0xF1:
			err = d.decodeRLE()
		case op >= 0xD8 && op <= 0xDF:
			err = d.decodeDictToken(int(op - 0xD8))
		case op == 0xF2:
			err = d.decodeDictTokenExt()
		case op >= OpcodePrefixTransSpace && op <= OpcodePrefixTransSpaceSpace || op == OpcodePrefixTransEqQuote || op == OpcodePrefixTransTagClose:
			err = d.decodeTransformedToken(op)
		case op == OpcodePrefixTransPair:
			err = d.decodeTokenPair()
		case op >= 0xF3 && op <= 0xFA:
			err = d.decodeDirectLiteralRun(int(op - 0xF2))
		case op == OpCopyBlock:
			err = d.decodeCopyBlock()
		case op == OpGrammarBlock:
			err = d.decodeGrammarBlock()
		default:
			return fmt.Errorf("%w: unknown opcode 0x%02X", ErrCorruptedFrame, op)
		}
		if err != nil {
			return err
		}

		dstPos = d.dstPos
		cmdPtr = d.cmdPtr
		litPtr = d.litPtr
		offPtr = d.offPtr
		rep0 = d.rep0
		rep1 = d.rep1
		rep2 = d.rep2
	}

	d.dstPos = dstPos
	d.cmdPtr = cmdPtr
	d.litPtr = litPtr
	d.offPtr = offPtr
	d.rep0 = rep0
	d.rep1 = rep1
	d.rep2 = rep2

	if d.dstPos < d.allocSize {
		return fmt.Errorf("%w: incomplete decoding: got %d of %d bytes", ErrCorruptedFrame, d.dstPos, d.allocSize)
	}
	return nil
}

// decodeLongRepeat handles Long Repeat Match (k 0..2) with Fused Literals (0xC0..0xCF).
func (d *frameDecoder) decodeLongRepeat(op byte) error {
	k := int(op & 0x0F)
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
	}
	token := d.cmds[d.cmdPtr]
	d.cmdPtr++
	litLen := int((token >> 4) & 0x0F)
	baseLen := int(token & 0x0F)
	mLen := baseLen + d.minMatchBase
	if baseLen == 15 {
		var extra int
		var err error
		extra, d.cmdPtr, err = fastReadVarint(d.cmds, d.cmdPtr)
		if err != nil {
			return err
		}
		mLen = 15 + d.minMatchBase + extra
	}

	if litLen > 0 {
		if d.litPtr+litLen > len(d.lits) || d.dstPos+litLen > len(d.outBuf) {
			return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
		}
		if litLen <= 8 && d.litPtr+8 <= len(d.lits) && d.dstPos+8 <= len(d.outBuf) {
			*(*uint64)(unsafe.Add(d.baseDst, d.dstPos)) = *(*uint64)(unsafe.Pointer(&d.lits[d.litPtr]))
		} else if litLen <= 16 && d.litPtr+16 <= len(d.lits) && d.dstPos+16 <= len(d.outBuf) {
			*(*uint64)(unsafe.Add(d.baseDst, d.dstPos)) = *(*uint64)(unsafe.Pointer(&d.lits[d.litPtr]))
			*(*uint64)(unsafe.Add(d.baseDst, d.dstPos+8)) = *(*uint64)(unsafe.Pointer(&d.lits[d.litPtr+8]))
		} else {
			copy(d.outBuf[d.dstPos:d.dstPos+litLen], d.lits[d.litPtr:d.litPtr+litLen])
		}
		d.litPtr += litLen
		d.dstPos += litLen
	}

	var dist int
	switch k {
	case 0:
		dist = d.rep0
	case 1:
		dist = d.rep1
		d.rep1 = d.rep0
		d.rep0 = dist
	case 2:
		dist = d.rep2
		d.rep2 = d.rep1
		d.rep1 = d.rep0
		d.rep0 = dist
	default:
		return fmt.Errorf("%w: invalid long repeat index %d", ErrCorruptedFrame, k)
	}

	srcIdx := d.dstPos - dist
	if dist >= mLen && mLen >= 32 && srcIdx >= 0 && d.dstPos+mLen <= len(d.outBuf) {
		copy(d.outBuf[d.dstPos:d.dstPos+mLen], d.outBuf[srcIdx:srcIdx+mLen])
	} else if dist >= 8 && srcIdx >= 0 && d.dstPos+mLen <= len(d.outBuf) {
		copyMatch(d.baseDst, d.dstPos, srcIdx, mLen, dist)
	} else {
		if err := execMatch(d.baseDst, d.outBuf, d.rawDict, d.dstPos, dist, mLen); err != nil {
			return err
		}
	}
	d.dstPos += mLen
	return nil
}

// decodeStrideMatch handles Stride Match (m 1..8) (0xD0..0xD7).
func (d *frameDecoder) decodeStrideMatch(op byte) error {
	m := int(op&0x07) + 1
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
	}
	token := d.cmds[d.cmdPtr]
	d.cmdPtr++
	litLen := int((token >> 4) & 0x0F)
	mLen := int(token&0x0F) + d.minMatchBase

	if litLen > 0 {
		if d.litPtr+litLen > len(d.lits) || d.dstPos+litLen > len(d.outBuf) {
			return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:d.dstPos+litLen], d.lits[d.litPtr:d.litPtr+litLen])
		d.litPtr += litLen
		d.dstPos += litLen
	}

	dist := m * d.stride
	d.rep2 = d.rep1
	d.rep1 = d.rep0
	d.rep0 = dist

	if err := execMatch(d.baseDst, d.outBuf, d.rawDict, d.dstPos, dist, mLen); err != nil {
		return err
	}
	d.dstPos += mLen
	return nil
}

// applyDeltaMask applies in-place XOR modifications to the decoded match from literal stream.
func (d *frameDecoder) applyDeltaMask(mLen int) error {
	if d.litPtr >= len(d.lits) {
		return fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
	}
	cMods := int(d.lits[d.litPtr])
	d.litPtr++
	if cMods == 1 && mLen <= 256 {
		if d.litPtr+2 > len(d.lits) {
			return fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
		}
		relOffset := int(d.lits[d.litPtr])
		xorVal := d.lits[d.litPtr+1]
		d.litPtr += 2
		if relOffset < mLen {
			*(*byte)(unsafe.Add(d.baseDst, d.dstPos+relOffset)) ^= xorVal
		}
	} else if cMods == 2 && mLen <= 256 {
		if d.litPtr+4 > len(d.lits) {
			return fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
		}
		r0 := int(d.lits[d.litPtr])
		x0 := d.lits[d.litPtr+1]
		r1 := int(d.lits[d.litPtr+2])
		x1 := d.lits[d.litPtr+3]
		d.litPtr += 4
		if r0 < mLen {
			*(*byte)(unsafe.Add(d.baseDst, d.dstPos+r0)) ^= x0
		}
		if r1 < mLen {
			*(*byte)(unsafe.Add(d.baseDst, d.dstPos+r1)) ^= x1
		}
	} else {
		for range cMods {
			var relOffset int
			if mLen <= 256 {
				if d.litPtr >= len(d.lits) {
					return fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				relOffset = int(d.lits[d.litPtr])
				d.litPtr++
			} else {
				if d.litPtr+2 > len(d.lits) {
					return fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				relOffset = int(binary.LittleEndian.Uint16(d.lits[d.litPtr : d.litPtr+2]))
				d.litPtr += 2
			}
			if d.litPtr >= len(d.lits) {
				return fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
			}
			xorVal := d.lits[d.litPtr]
			d.litPtr++
			if relOffset < mLen {
				*(*byte)(unsafe.Add(d.baseDst, d.dstPos+relOffset)) ^= xorVal
			}
		}
	}
	return nil
}

// decodeDeltaMaskLocality handles Delta-Mask with Locality Index k (0xE0..0xE7).
func (d *frameDecoder) decodeDeltaMaskLocality(op byte) error {
	k := int(op & 0x07)
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
	}
	baseLen := int(d.cmds[d.cmdPtr])
	d.cmdPtr++
	mLen := baseLen + d.minMatchBase
	if baseLen == 255 {
		var extra int
		var err error
		extra, d.cmdPtr, err = fastReadVarint(d.cmds, d.cmdPtr)
		if err != nil {
			return err
		}
		mLen = 255 + d.minMatchBase + extra
	}

	var dist int
	switch k {
	case 0:
		dist = d.rep0
	case 1:
		dist = d.rep1
		d.rep1 = d.rep0
		d.rep0 = dist
	case 2:
		dist = d.rep2
		d.rep2 = d.rep1
		d.rep1 = d.rep0
		d.rep0 = dist
	default:
		return fmt.Errorf("%w: invalid delta locality index %d", ErrCorruptedFrame, k)
	}

	if err := execMatch(d.baseDst, d.outBuf, d.rawDict, d.dstPos, dist, mLen); err != nil {
		return err
	}
	if err := d.applyDeltaMask(mLen); err != nil {
		return err
	}
	d.dstPos += mLen
	return nil
}

// decodeDeltaMaskExplicit handles Delta-Mask with Explicit Offset (0xE8).
func (d *frameDecoder) decodeDeltaMaskExplicit() error {
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
	}
	baseLen := int(d.cmds[d.cmdPtr])
	d.cmdPtr++
	mLen := baseLen + d.minMatchBase
	if baseLen == 255 {
		var extra int
		var err error
		extra, d.cmdPtr, err = fastReadVarint(d.cmds, d.cmdPtr)
		if err != nil {
			return err
		}
		mLen = 255 + d.minMatchBase + extra
	}

	dist, newOffPtr, err := fastReadVarint(d.offs, d.offPtr)
	if err != nil {
		return err
	}
	d.offPtr = newOffPtr

	d.rep2 = d.rep1
	d.rep1 = d.rep0
	d.rep0 = dist

	if err := execMatch(d.baseDst, d.outBuf, d.rawDict, d.dstPos, dist, mLen); err != nil {
		return err
	}
	if err := d.applyDeltaMask(mLen); err != nil {
		return err
	}
	d.dstPos += mLen
	return nil
}

// decodeRawDictMatch handles Direct Raw Dictionary Match (OpcodePrefixRawDict).
func (d *frameDecoder) decodeRawDictMatch() error {
	if len(d.rawDict) == 0 {
		return fmt.Errorf("%w: raw dict match opcode without raw dictionary", ErrCorruptedFrame)
	}
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
	}
	token := d.cmds[d.cmdPtr]
	d.cmdPtr++
	litLen := int((token >> 4) & 0x0F)
	baseLen := int(token & 0x0F)
	mLen := baseLen + d.minMatchBase
	if baseLen == 15 {
		var extra int
		var err error
		extra, d.cmdPtr, err = fastReadVarint(d.cmds, d.cmdPtr)
		if err != nil {
			return err
		}
		mLen = 15 + d.minMatchBase + extra
	}

	if litLen > 0 {
		if d.litPtr+litLen > len(d.lits) || d.dstPos+litLen > len(d.outBuf) {
			return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
		}
		if litLen <= 8 && d.litPtr+8 <= len(d.lits) && d.dstPos+8 <= len(d.outBuf) {
			*(*uint64)(unsafe.Add(d.baseDst, d.dstPos)) = *(*uint64)(unsafe.Pointer(&d.lits[d.litPtr]))
		} else {
			copy(d.outBuf[d.dstPos:d.dstPos+litLen], d.lits[d.litPtr:d.litPtr+litLen])
		}
		d.litPtr += litLen
		d.dstPos += litLen
	}

	dictOff, newOffPtr, err := fastReadVarint(d.offs, d.offPtr)
	if err != nil {
		return err
	}
	d.offPtr = newOffPtr

	if mLen <= 0 || dictOff < 0 || dictOff+mLen > len(d.rawDict) || d.dstPos+mLen > len(d.outBuf) {
		return fmt.Errorf(
			"%w: raw dict offset out of bounds (off=%d, mLen=%d, dictLen=%d)",
			ErrCorruptedFrame,
			dictOff,
			mLen,
			len(d.rawDict),
		)
	}
	copy(d.outBuf[d.dstPos:d.dstPos+mLen], d.rawDict[dictOff:dictOff+mLen])
	d.dstPos += mLen
	return nil
}

// decodeRFCTransform handles RFC 7932 121-Word Transformed Matches (OpcodePrefixRFCTransform = 0xCF).
func (d *frameDecoder) decodeRFCTransform() error {
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
	}
	b0 := d.cmds[d.cmdPtr]
	d.cmdPtr++

	if b0 >= 128 {
		if d.cmdPtr >= d.cmdEnd {
			return fmt.Errorf("%w: truncated compact RFC token", ErrCorruptedFrame)
		}
		b1 := d.cmds[d.cmdPtr]
		d.cmdPtr++

		packed := (int(b0&0x7F) << 7) | int(b1&0x7F)
		var word []byte
		var transID uint8
		if packed < 13504 {
			word = przmBrotliWords[packed]
			transID = 0
		} else if packed < 14016 {
			word = rfcTopWordsBytes[packed-13504]
			transID = 0
		} else {
			cIdx := packed - 14016
			if cIdx < len(kCompactRFCPairs) {
				pair := kCompactRFCPairs[cIdx]
				word = przmBrotliWords[pair.wordID]
				transID = pair.transID
			} else {
				return fmt.Errorf("%w: invalid compact RFC pair index %d", ErrCorruptedFrame, cIdx)
			}
		}
		if len(word) == 0 {
			return fmt.Errorf("%w: invalid compact RFC word", ErrCorruptedFrame)
		}
		n, err := applyRFC7932Transform(d.outBuf[d.dstPos:], transID, word)
		if err != nil {
			return err
		}
		d.dstPos += n
		return nil
	}

	transID := b0
	wordID, newCmdPtr, err := fastReadVarint(d.cmds, d.cmdPtr)
	if err != nil {
		return err
	}
	d.cmdPtr = newCmdPtr

	var word []byte
	if wordID < 256 {
		if wordID < len(d.activeDict) {
			word = d.activeDict[wordID]
		} else if wordID < len(webTokensRawBytes) {
			word = webTokensRawBytes[wordID]
		}
	} else {
		rfcIdx := wordID - 256
		if rfcIdx < len(rfcTopWordsBytes) {
			word = rfcTopWordsBytes[rfcIdx]
		} else {
			brotliIdx := rfcIdx - len(rfcTopWordsBytes)
			if brotliIdx < len(przmBrotliWords) {
				word = przmBrotliWords[brotliIdx]
			}
		}
	}
	if len(word) == 0 {
		return fmt.Errorf("%w: invalid RFC word ID %d", ErrCorruptedFrame, wordID)
	}

	n, err := applyRFC7932Transform(d.outBuf[d.dstPos:], transID, word)
	if err != nil {
		return err
	}
	d.dstPos += n
	return nil
}

// decodeLiteralRun handles Pure Literal Run (0xF0).
func (d *frameDecoder) decodeLiteralRun() error {
	runLen, newCmdPtr, err := fastReadVarint(d.cmds, d.cmdPtr)
	if err != nil {
		return err
	}
	d.cmdPtr = newCmdPtr

	if runLen < 0 || d.litPtr+runLen > len(d.lits) || d.dstPos+runLen > len(d.outBuf) {
		return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
	}
	copy(d.outBuf[d.dstPos:d.dstPos+runLen], d.lits[d.litPtr:d.litPtr+runLen])
	d.litPtr += runLen
	d.dstPos += runLen
	return nil
}

// decodeRLE handles RLE Run (0xF1).
func (d *frameDecoder) decodeRLE() error {
	rleLen, newCmdPtr, err := fastReadVarint(d.cmds, d.cmdPtr)
	if err != nil {
		return err
	}
	d.cmdPtr = newCmdPtr

	if rleLen <= 0 || d.litPtr >= len(d.lits) || d.dstPos+rleLen > len(d.outBuf) {
		return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
	}
	rleByte := d.lits[d.litPtr]
	d.litPtr++
	pattern := simd.RepeatByte0x01 * uint64(rleByte)
	pDst := unsafe.Add(d.baseDst, d.dstPos)
	rem := rleLen
	for rem >= 32 {
		*(*uint64)(pDst) = pattern
		*(*uint64)(unsafe.Add(pDst, 8)) = pattern
		*(*uint64)(unsafe.Add(pDst, 16)) = pattern
		*(*uint64)(unsafe.Add(pDst, 24)) = pattern
		pDst = unsafe.Add(pDst, 32)
		rem -= 32
	}
	for rem > 0 {
		*(*uint64)(pDst) = pattern
		pDst = unsafe.Add(pDst, 8)
		rem -= 8
	}
	d.dstPos += rleLen
	return nil
}

// decodeDictToken copies a dictionary token by index.
func (d *frameDecoder) decodeDictToken(tokIdx int) error {
	if len(d.activeDict) == 0 {
		return fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
	}
	if tokIdx >= len(d.activeDict) {
		return fmt.Errorf("%w: invalid dictionary token ID %d", ErrCorruptedFrame, tokIdx)
	}
	tok := d.activeDict[tokIdx]
	if d.dstPos+len(tok) > len(d.outBuf) {
		return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
	}
	copy(d.outBuf[d.dstPos:], tok)
	d.dstPos += len(tok)
	return nil
}

// decodeDictTokenExt handles Extended 2-byte schema token from active dictionary (0xF2).
func (d *frameDecoder) decodeDictTokenExt() error {
	if len(d.activeDict) == 0 {
		return fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
	}
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated dictionary token opcode", ErrCorruptedFrame)
	}
	tokIdx := int(d.cmds[d.cmdPtr])
	d.cmdPtr++
	return d.decodeDictToken(tokIdx)
}

// decodeTransformedToken handles transformed dictionary tokens (0xEA..0xEF, 0xFB, 0xFC).
func (d *frameDecoder) decodeTransformedToken(op byte) error {
	if len(d.activeDict) == 0 {
		return fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
	}
	if d.cmdPtr >= d.cmdEnd {
		return fmt.Errorf("%w: truncated transformed token opcode", ErrCorruptedFrame)
	}
	tokIdx := int(d.cmds[d.cmdPtr])
	d.cmdPtr++
	if tokIdx >= len(d.activeDict) {
		return fmt.Errorf("%w: invalid dictionary token ID %d", ErrCorruptedFrame, tokIdx)
	}
	tok := d.activeDict[tokIdx]

	switch op {
	case OpcodePrefixTransSpace:
		// 0xEA: " " + tok
		tLen := len(tok) + 1
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		d.outBuf[d.dstPos] = ' '
		copy(d.outBuf[d.dstPos+1:], tok)
		d.dstPos += tLen

	case OpcodePrefixTransQuote:
		// 0xEB: tok + "\""
		tLen := len(tok) + 1
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:], tok)
		d.outBuf[d.dstPos+len(tok)] = '"'
		d.dstPos += tLen

	case OpcodePrefixTransBrace:
		// 0xEC: tok + "{"
		tLen := len(tok) + 1
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:], tok)
		d.outBuf[d.dstPos+len(tok)] = '{'
		d.dstPos += tLen

	case OpcodePrefixTransUpper:
		// 0xED: UppercaseFirst(tok)
		tLen := len(tok)
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:], tok)
		if tok[0] >= 'a' && tok[0] <= 'z' {
			d.outBuf[d.dstPos] = tok[0] - ('a' - 'A')
		}
		d.dstPos += tLen

	case OpcodePrefixTransSemi:
		// 0xEE: tok + ";"
		tLen := len(tok) + 1
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:], tok)
		d.outBuf[d.dstPos+len(tok)] = ';'
		d.dstPos += tLen

	case OpcodePrefixTransSpaceSpace:
		// 0xEF: " " + tok + " "
		tLen := len(tok) + 2
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		d.outBuf[d.dstPos] = ' '
		copy(d.outBuf[d.dstPos+1:], tok)
		d.outBuf[d.dstPos+len(tok)+1] = ' '
		d.dstPos += tLen

	case OpcodePrefixTransEqQuote:
		// 0xFB: tok + "=\""
		tLen := len(tok) + 2
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:], tok)
		d.outBuf[d.dstPos+len(tok)] = '='
		d.outBuf[d.dstPos+len(tok)+1] = '"'
		d.dstPos += tLen

	case OpcodePrefixTransTagClose:
		// 0xFC: tok + ">"
		tLen := len(tok) + 1
		if d.dstPos+tLen > len(d.outBuf) {
			return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
		}
		copy(d.outBuf[d.dstPos:], tok)
		d.outBuf[d.dstPos+len(tok)] = '>'
		d.dstPos += tLen

	default:
		return fmt.Errorf("%w: unknown transformed opcode 0x%02X", ErrCorruptedFrame, op)
	}
	return nil
}

// decodeTokenPair handles Token Pair (0xFF): tok1 + tok2.
func (d *frameDecoder) decodeTokenPair() error {
	if len(d.activeDict) == 0 {
		return fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
	}
	if d.cmdPtr+2 > d.cmdEnd {
		return fmt.Errorf("%w: truncated token pair opcode", ErrCorruptedFrame)
	}
	tok1Idx := int(d.cmds[d.cmdPtr])
	tok2Idx := int(d.cmds[d.cmdPtr+1])
	d.cmdPtr += 2
	if tok1Idx >= len(d.activeDict) || tok2Idx >= len(d.activeDict) {
		return fmt.Errorf("%w: invalid token pair IDs %d, %d", ErrCorruptedFrame, tok1Idx, tok2Idx)
	}
	tok1 := d.activeDict[tok1Idx]
	tok2 := d.activeDict[tok2Idx]
	tLen := len(tok1) + len(tok2)
	if d.dstPos+tLen > len(d.outBuf) {
		return fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
	}
	copy(d.outBuf[d.dstPos:], tok1)
	copy(d.outBuf[d.dstPos+len(tok1):], tok2)
	d.dstPos += tLen
	return nil
}

// decodeDirectLiteralRun handles Direct 1-byte Literal Run (1..8 literals) (0xF3..0xFA).
func (d *frameDecoder) decodeDirectLiteralRun(runLen int) error {
	if runLen <= 0 || d.litPtr+runLen > len(d.lits) || d.dstPos+runLen > len(d.outBuf) {
		return fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
	}
	if d.litPtr+8 <= len(d.lits) && d.dstPos+8 <= len(d.outBuf) {
		*(*uint64)(unsafe.Add(d.baseDst, d.dstPos)) = *(*uint64)(unsafe.Pointer(&d.lits[d.litPtr]))
	} else {
		copy(d.outBuf[d.dstPos:d.dstPos+runLen], d.lits[d.litPtr:d.litPtr+runLen])
	}
	d.litPtr += runLen
	d.dstPos += runLen
	return nil
}

// decodeCopyBlock handles Block Deduplication Copy (OpCopyBlock).
func (d *frameDecoder) decodeCopyBlock() error {
	if d.cmdPtr+4 > d.cmdEnd {
		return fmt.Errorf("%w: truncated OpCopyBlock payload", ErrCorruptedFrame)
	}
	srcBlockIdx := binary.LittleEndian.Uint32(d.cmds[d.cmdPtr : d.cmdPtr+4])
	d.cmdPtr += 4
	if srcBlockIdx >= uint32(MaxBlockSize/DedupBlockSize) {
		return fmt.Errorf("%w: invalid dedup block index %d", ErrCorruptedFrame, srcBlockIdx)
	}
	ref := int(srcBlockIdx) * DedupBlockSize
	if ref < 0 || ref+DedupBlockSize > d.dstPos {
		return fmt.Errorf(
			"%w: forward or self-referential dedup block reference (ref=%d, dstPos=%d)",
			ErrCorruptedFrame,
			ref,
			d.dstPos,
		)
	}
	if d.dstPos+DedupBlockSize > len(d.outBuf) {
		return fmt.Errorf("%w: dedup block exceeds buffer capacity", ErrCorruptedFrame)
	}
	copy(d.outBuf[d.dstPos:d.dstPos+DedupBlockSize], d.outBuf[ref:ref+DedupBlockSize])
	d.dstPos += DedupBlockSize
	return nil
}

// decodeGrammarBlock handles Straight-Line Program / Grammar DAG Bisected Block (OpGrammarBlock / 0xFE).
func (d *frameDecoder) decodeGrammarBlock() error {
	compSize, newCmdPtr, err := fastReadVarint(d.cmds, d.cmdPtr)
	if err != nil {
		return err
	}
	d.cmdPtr = newCmdPtr
	if compSize <= 0 || d.cmdPtr+compSize > d.cmdEnd {
		return fmt.Errorf("%w: truncated grammar block payload (compSize=%d)", ErrCorruptedFrame, compSize)
	}
	pwsChunk := d.cmds[d.cmdPtr : d.cmdPtr+compSize]
	d.cmdPtr += compSize
	if len(pwsChunk) < 8 {
		return fmt.Errorf("%w: grammar block smaller than header", ErrCorruptedFrame)
	}
	origSize := int(binary.LittleEndian.Uint32(pwsChunk[4:8]))
	if origSize <= 0 || d.dstPos+origSize > len(d.outBuf) {
		return fmt.Errorf("%w: grammar block size overflow (origSize=%d, dstPos=%d, outBufLen=%d)",
			ErrCorruptedFrame, origSize, d.dstPos, len(d.outBuf))
	}
	if _, err := PWSDecodeTo(d.outBuf[d.dstPos:d.dstPos+origSize], pwsChunk); err != nil {
		return err
	}
	d.dstPos += origSize
	return nil
}
