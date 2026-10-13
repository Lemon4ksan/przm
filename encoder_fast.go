// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math/bits"
	"unsafe"
)

// fastBlockEncoder implements the Level 1-3 high-throughput 2-way hash chain LZ77 block compressor.
type fastBlockEncoder struct {
	src        []byte
	uSize      int
	windowSize int
	minMatch   int
	hasStride  bool
	stride     int
	pad        *scratchpad
	rep0       *int
	rep1       *int
	rep2       *int
	rep3       *int
}

func newFastBlockEncoder(
	src []byte,
	uSize int,
	windowSize int,
	minMatch int,
	hasStride bool,
	stride int,
	pad *scratchpad,
	rep0, rep1, rep2, rep3 *int,
) *fastBlockEncoder {
	return &fastBlockEncoder{
		src:        src,
		uSize:      uSize,
		windowSize: windowSize,
		minMatch:   minMatch,
		hasStride:  hasStride,
		stride:     stride,
		pad:        pad,
		rep0:       rep0,
		rep1:       rep1,
		rep2:       rep2,
		rep3:       rep3,
	}
}

func (e *fastBlockEncoder) flushLiterals(literals []byte) {
	nLits := len(literals)
	if nLits == 0 {
		return
	}
	if nLits <= 8 {
		e.pad.cmdBuf = append(e.pad.cmdBuf, 0xF2+byte(nLits))
	} else if nLits < 0x80 {
		e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixLiteral, byte(nLits))
	} else if nLits < 0x4000 {
		e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixLiteral, byte(nLits|0x80), byte(nLits>>7))
	} else {
		e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixLiteral)
		var varBuf [8]byte
		n := binary.PutUvarint(varBuf[:], uint64(nLits))
		e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
	}
	e.pad.literalBuf = append(e.pad.literalBuf, literals...)
}

func (e *fastBlockEncoder) writeOffset(distance int) {
	if distance < 0x80 {
		e.pad.offsetBuf = append(e.pad.offsetBuf, byte(distance))
	} else if distance < 0x4000 {
		e.pad.offsetBuf = append(e.pad.offsetBuf, byte(distance|0x80), byte(distance>>7))
	} else {
		var buf [8]byte
		n := binary.PutUvarint(buf[:], uint64(distance))
		e.pad.offsetBuf = append(e.pad.offsetBuf, buf[:n]...)
	}
}

// compressBlock executes the fast dual-probe LZ77 match search over [blockStart, blockStart+blockSize).
func (e *fastBlockEncoder) compressBlock(blockStart, blockSize int) {
	pos := blockStart
	blockEnd := blockStart + blockSize
	if blockEnd > len(e.src) {
		return
	}

	srcPtr := unsafe.Pointer(&e.src[0])
	literalStart := blockStart
	consecutiveMisses := 0

	for pos+4 <= blockEnd {
		maxBlockLen := min(blockEnd-pos, 65535)
		currentWord := *(*uint32)(unsafe.Add(srcPtr, pos))
		hashPrimary := ((currentWord * 0x1E35A7BD) >> 16) & hashTableMask
		hashSecondary := ((currentWord * 0x7FEB352D) >> 15) & hashTableMask

		candPosPrimary := e.pad.head[hashPrimary*4]
		candPosSecondary := e.pad.head[hashSecondary*4+1]

		e.pad.head[hashPrimary*4] = int32(pos + 1)
		e.pad.head[hashSecondary*4+1] = int32(pos + 1)

		bestDistance := 0
		matchLength := 0

		if candPosPrimary > 0 {
			prevPos := int(candPosPrimary - 1)
			dist := pos - prevPos
			if dist > 0 && dist < e.windowSize && prevPos+4 <= len(e.src) {
				if *(*uint32)(unsafe.Add(srcPtr, prevPos)) == currentWord {
					bestDistance = dist
					matchLength = 4
					for pos+matchLength+8 <= len(e.src) && matchLength+8 <= maxBlockLen {
						diff := *(*uint64)(unsafe.Add(srcPtr, (pos + matchLength))) ^ *(*uint64)(unsafe.Add(srcPtr, (prevPos + matchLength)))
						if diff != 0 {
							matchLength += (bits.TrailingZeros64(diff) >> 3)
							break
						}
						matchLength += 8
					}
					for pos+matchLength < len(e.src) && matchLength < maxBlockLen && *(*byte)(unsafe.Add(srcPtr, (pos + matchLength))) == *(*byte)(unsafe.Add(srcPtr, (prevPos + matchLength))) {
						matchLength++
					}
				}
			}
		}

		if candPosSecondary > 0 && matchLength < 8 {
			prevPos := int(candPosSecondary - 1)
			dist := pos - prevPos
			if dist > 0 && dist < e.windowSize && prevPos+4 <= len(e.src) {
				if *(*uint32)(unsafe.Add(srcPtr, prevPos)) == currentWord {
					secondaryMatchLength := 4
					for pos+secondaryMatchLength+8 <= len(e.src) && secondaryMatchLength+8 <= maxBlockLen {
						diff := *(*uint64)(unsafe.Add(srcPtr, (pos + secondaryMatchLength))) ^ *(*uint64)(unsafe.Add(srcPtr, (prevPos + secondaryMatchLength)))
						if diff != 0 {
							secondaryMatchLength += (bits.TrailingZeros64(diff) >> 3)
							break
						}
						secondaryMatchLength += 8
					}
					for pos+secondaryMatchLength < len(e.src) && secondaryMatchLength < maxBlockLen && *(*byte)(unsafe.Add(srcPtr, (pos + secondaryMatchLength))) == *(*byte)(unsafe.Add(srcPtr, (prevPos + secondaryMatchLength))) {
						secondaryMatchLength++
					}
					if secondaryMatchLength > matchLength {
						matchLength = secondaryMatchLength
						bestDistance = dist
					}
				}
			}
		}

		if matchLength >= e.minMatch {
			literalLength := pos - literalStart
			if isBracketDist(bestDistance) && matchLength-4 <= 15 {
				bracket := bracketIndex(bestDistance)
				if literalLength > 15 {
					e.flushLiterals(e.src[literalStart : pos-15])
					literalStart = pos - 15
					literalLength = 15
				}
				op := OpcodePrefixFusedBracket + uint8(bracket)
				token := uint8((literalLength << 4) | (matchLength - 4))
				e.pad.cmdBuf = append(e.pad.cmdBuf, op, token)
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}
				*e.rep3 = *e.rep2
				*e.rep2 = *e.rep1
				*e.rep1 = *e.rep0
				*e.rep0 = bestDistance
			} else if (!e.hasStride || e.stride <= 1) && bestDistance <= 256 {
				if literalLength > 6 {
					e.flushLiterals(e.src[literalStart : pos-6])
					literalStart = pos - 6
					literalLength = 6
				}
				baseLen := matchLength - 4
				if baseLen > 6 {
					baseLen = 7
				}
				desc := uint8((3 << 6) | (literalLength << 3) | baseLen)
				deltaByte := uint8(bestDistance - 1)
				e.pad.cmdBuf = append(e.pad.cmdBuf, OpcodePrefixDiffLattice, desc, deltaByte)
				if baseLen == 7 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(matchLength-11))
					e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				}
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}
				*e.rep3 = *e.rep2
				*e.rep2 = *e.rep1
				*e.rep1 = *e.rep0
				*e.rep0 = bestDistance
			} else {
				if literalLength > 3 {
					e.flushLiterals(e.src[literalStart : pos-3])
					literalStart = pos - 3
					literalLength = 3
				}
				baseLen := matchLength - 4
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodeFlagFusedExt | uint8(literalLength<<4) | uint8(baseLen)
				e.pad.cmdBuf = append(e.pad.cmdBuf, op)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(matchLength-19))
					e.pad.cmdBuf = append(e.pad.cmdBuf, varBuf[:n]...)
				}
				if literalLength > 0 {
					e.pad.literalBuf = append(e.pad.literalBuf, e.src[literalStart:pos]...)
				}

				e.writeOffset(bestDistance)

				*e.rep3 = *e.rep2
				*e.rep2 = *e.rep1
				*e.rep1 = *e.rep0
				*e.rep0 = bestDistance
			}

			pos += matchLength
			literalStart = pos
			consecutiveMisses = 0
		} else {
			consecutiveMisses++
			step := 1 + (consecutiveMisses >> 3)
			pos += step
		}
	}

	if literalStart < blockEnd {
		e.flushLiterals(e.src[literalStart:blockEnd])
	}
}
