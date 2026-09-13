// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math/bits"

	"github.com/lemon4ksan/foundation/codec/filter/bcj"
)

// splitX86 extracts 32-bit displacements of CALL (0xE8) and JMP (0xE9) instructions into 4 separate
// byte planes (b0, b1, b2, b3), setting the code addresses to zero placeholders.
// Returns the combined stream [4B callCount | b0s | b1s | b2s | b3s | transformedCode] and callCount.
func splitX86(buf []byte, uSize int, splitBuf []byte) ([]byte, int) {
	callCount := 0
	lim := uSize - 4
	for i := 0; i < lim; {
		if i+8 <= lim {
			v := binary.LittleEndian.Uint64(buf[i:])
			hasE8 := ((v ^ 0xE8E8E8E8E8E8E8E8) - 0x0101010101010101) & ^(v ^ 0xE8E8E8E8E8E8E8E8) & 0x8080808080808080
			hasE9 := ((v ^ 0xE9E9E9E9E9E9E9E9) - 0x0101010101010101) & ^(v ^ 0xE9E9E9E9E9E9E9E9) & 0x8080808080808080
			matchMask := hasE8 | hasE9
			if matchMask == 0 {
				i += 8
				continue
			}
			tz := bits.TrailingZeros64(matchMask) >> 3
			if tz > 0 {
				i += tz
				if i >= lim {
					break
				}
			}
		}

		op := buf[i]
		if op == 0xE8 || op == 0xE9 {
			callCount++
			i += 5
		} else {
			i++
		}
	}

	if callCount < 8 {
		return nil, 0
	}

	needed := 4 + 4*callCount + uSize
	if cap(splitBuf) < needed {
		splitBuf = make([]byte, needed)
	} else {
		splitBuf = splitBuf[:needed]
	}

	binary.LittleEndian.PutUint32(splitBuf[0:4], uint32(callCount))
	b0s := splitBuf[4 : 4+callCount]
	b1s := splitBuf[4+callCount : 4+2*callCount]
	b2s := splitBuf[4+2*callCount : 4+3*callCount]
	b3s := splitBuf[4+3*callCount : 4+4*callCount]
	code := splitBuf[4+4*callCount:]
	copy(code, buf)

	cIdx := 0
	for i := 0; i < lim; {
		if i+8 <= lim {
			v := binary.LittleEndian.Uint64(code[i:])
			hasE8 := ((v ^ 0xE8E8E8E8E8E8E8E8) - 0x0101010101010101) & ^(v ^ 0xE8E8E8E8E8E8E8E8) & 0x8080808080808080
			hasE9 := ((v ^ 0xE9E9E9E9E9E9E9E9) - 0x0101010101010101) & ^(v ^ 0xE9E9E9E9E9E9E9E9) & 0x8080808080808080
			matchMask := hasE8 | hasE9
			if matchMask == 0 {
				i += 8
				continue
			}
			tz := bits.TrailingZeros64(matchMask) >> 3
			if tz > 0 {
				i += tz
				if i >= lim {
					break
				}
			}
		}

		op := code[i]
		if op == 0xE8 || op == 0xE9 {
			val := binary.LittleEndian.Uint32(code[i+1 : i+5])
			b0s[cIdx] = byte(val)
			b1s[cIdx] = byte(val >> 8)
			b2s[cIdx] = byte(val >> 16)
			b3s[cIdx] = byte(val >> 24)
			binary.LittleEndian.PutUint32(code[i+1:i+5], 0)
			cIdx++
			i += 5
		} else {
			i++
		}
	}

	return splitBuf, callCount
}

// unsplitX86 restores separated displacement planes back into the code buffer and applies inverse BCJ.
func unsplitX86(dst []byte, uSize int, callCount int, tempBuf []byte) {
	planesLen := 4 * callCount
	if cap(tempBuf) < planesLen {
		tempBuf = make([]byte, planesLen)
	} else {
		tempBuf = tempBuf[:planesLen]
	}

	// Copy planes into temporary buffer to allow safe in-place restoration
	copy(tempBuf, dst[4:4+planesLen])
	recB0 := tempBuf[0:callCount]
	recB1 := tempBuf[callCount : 2*callCount]
	recB2 := tempBuf[2*callCount : 3*callCount]
	recB3 := tempBuf[3*callCount : 4*callCount]

	// Move code from offset 4+planesLen to beginning of dst
	codeOffset := 4 + planesLen
	copy(dst[:uSize], dst[codeOffset:codeOffset+uSize])

	cIdx := 0
	lim := uSize - 4
	for i := 0; i < lim && cIdx < callCount; {
		if i+8 <= lim {
			v := binary.LittleEndian.Uint64(dst[i:])
			hasE8 := ((v ^ 0xE8E8E8E8E8E8E8E8) - 0x0101010101010101) & ^(v ^ 0xE8E8E8E8E8E8E8E8) & 0x8080808080808080
			hasE9 := ((v ^ 0xE9E9E9E9E9E9E9E9) - 0x0101010101010101) & ^(v ^ 0xE9E9E9E9E9E9E9E9) & 0x8080808080808080
			matchMask := hasE8 | hasE9
			if matchMask == 0 {
				i += 8
				continue
			}
			tz := bits.TrailingZeros64(matchMask) >> 3
			if tz > 0 {
				i += tz
				if i >= lim {
					break
				}
			}
		}

		op := dst[i]
		if op == 0xE8 || op == 0xE9 {
			val := uint32(recB0[cIdx]) | (uint32(recB1[cIdx]) << 8) |
				(uint32(recB2[cIdx]) << 16) | (uint32(recB3[cIdx]) << 24)
			binary.LittleEndian.PutUint32(dst[i+1:i+5], val)
			cIdx++
			i += 5
		} else {
			i++
		}
	}

	bcj.Filter(bcj.X86, dst[:uSize], 0, false)
}
