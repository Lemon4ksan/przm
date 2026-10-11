// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"fmt"
	"math/bits"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/silicon/simd"
)

// validateSheafMask counts 1-bits in mask for the first origLen bits and ensures unused bits are 0.
func validateSheafMask(mask []byte, origLen, sLen int) bool {
	if origLen < 0 || sLen < 0 || sLen > origLen {
		return false
	}
	expectedMaskLen := (origLen + 7) / 8
	if len(mask) < expectedMaskLen {
		return false
	}
	fullBytes := origLen / 8
	remBits := origLen % 8
	ones := 0
	i := 0
	for ; i+8 <= fullBytes; i += 8 {
		ones += bits.OnesCount64(binary.LittleEndian.Uint64(mask[i:]))
	}
	for ; i < fullBytes; i++ {
		ones += bits.OnesCount8(mask[i])
	}
	if remBits > 0 {
		lastByte := mask[fullBytes]
		if (lastByte >> remBits) != 0 {
			return false // Unused high bits in mask must be zero
		}
		ones += bits.OnesCount8(lastByte)
	}
	return ones == sLen
}

// unpackLiterals unpacks literals from Section 4, supporting Sheaf-split V-ANS, standard V-ANS, and Nibble-Planes.
func unpackLiterals(src []byte, pad *decompressScratchpad) ([]byte, error) {
	if len(src) >= 8 && src[0] == vansMagic0 && src[1] == vansMagic1 && src[2] == vansVer1 {
		flags := src[3]
		if (flags & vansFlagCtxSheaf) != 0 {
			if len(src) < 40 {
				return nil, fmt.Errorf("%w: truncated context sheaf vans header", ErrCorruptedFrame)
			}
			origLen := int(binary.LittleEndian.Uint32(src[4:8]))
			fLen0 := int(binary.LittleEndian.Uint32(src[8:12]))
			fLen1 := int(binary.LittleEndian.Uint32(src[12:16]))
			fLen2 := int(binary.LittleEndian.Uint32(src[16:20]))
			fLen3 := int(binary.LittleEndian.Uint32(src[20:24]))

			encLen0 := int(binary.LittleEndian.Uint32(src[24:28]))
			encLen1 := int(binary.LittleEndian.Uint32(src[28:32]))
			encLen2 := int(binary.LittleEndian.Uint32(src[32:36]))
			encLen3 := int(binary.LittleEndian.Uint32(src[36:40]))

			if origLen < 0 || origLen > MaxBlockSize ||
				fLen0 < 0 || fLen1 < 0 || fLen2 < 0 || fLen3 < 0 ||
				encLen0 < 0 || encLen1 < 0 || encLen2 < 0 || encLen3 < 0 ||
				fLen0+fLen1+fLen2+fLen3 != origLen {
				return nil, fmt.Errorf("%w: invalid context sheaf vans dimensions", ErrCorruptedFrame)
			}

			if 40+encLen0+encLen1+encLen2+encLen3 > len(src) {
				return nil, fmt.Errorf("%w: truncated context sheaf payload", ErrCorruptedFrame)
			}

			if cap(pad.litBuf) < origLen+64 {
				pad.litBuf = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
			}
			if cap(pad.sheafSBuf) < origLen+64 {
				pad.sheafSBuf = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
			}

			f0 := pad.sheafSBuf[0:fLen0]
			f1 := pad.sheafSBuf[fLen0 : fLen0+fLen1]
			f2 := pad.sheafSBuf[fLen0+fLen1 : fLen0+fLen1+fLen2]
			f3 := pad.sheafSBuf[fLen0+fLen1+fLen2 : origLen]

			off := 40
			if fLen0 > 0 {
				if encLen0 == 0 {
					return nil, ErrCorruptedFrame
				}
				if _, err := VANSDecode4xTo(f0, src[off:off+encLen0], &pad.vansModels[0]); err != nil {
					return nil, fmt.Errorf("%w: decoding fiber 0: %w", ErrCorruptedFrame, err)
				}
				off += encLen0
			}
			if fLen1 > 0 {
				if encLen1 == 0 {
					return nil, ErrCorruptedFrame
				}
				if _, err := VANSDecode4xTo(f1, src[off:off+encLen1], &pad.vansModels[1]); err != nil {
					return nil, fmt.Errorf("%w: decoding fiber 1: %w", ErrCorruptedFrame, err)
				}
				off += encLen1
			}
			if fLen2 > 0 {
				if encLen2 == 0 {
					return nil, ErrCorruptedFrame
				}
				if _, err := VANSDecode4xTo(f2, src[off:off+encLen2], &pad.vansModels[2]); err != nil {
					return nil, fmt.Errorf("%w: decoding fiber 2: %w", ErrCorruptedFrame, err)
				}
				off += encLen2
			}
			if fLen3 > 0 {
				if encLen3 == 0 {
					return nil, ErrCorruptedFrame
				}
				if _, err := VANSDecode4xTo(f3, src[off:off+encLen3], &pad.vansModels[3]); err != nil {
					return nil, fmt.Errorf("%w: decoding fiber 3: %w", ErrCorruptedFrame, err)
				}
			}

			dst := pad.litBuf[:origLen]
			InterleaveContextSheaf4(dst, f0, f1, f2, f3, origLen)
			return dst, nil
		}
		if (flags & vansFlagSheaf) != 0 {
			if len(src) < 24 {
				return nil, fmt.Errorf("%w: truncated sheaf vans literal header", ErrCorruptedFrame)
			}
			origLen := int(binary.LittleEndian.Uint32(src[4:8]))
			sLen := int(binary.LittleEndian.Uint32(src[8:12]))
			maskLen := int(binary.LittleEndian.Uint32(src[12:16]))
			encSLen := int(binary.LittleEndian.Uint32(src[16:20]))
			encCLen := int(binary.LittleEndian.Uint32(src[20:24]))

			if origLen < 0 || origLen > MaxBlockSize {
				return nil, fmt.Errorf("%w: sheaf vans origLen %d exceeds MaxBlockSize", ErrCorruptedFrame, origLen)
			}
			expectedMaskLen := (origLen + 7) / 8
			if maskLen != expectedMaskLen || sLen < 0 || sLen > origLen || encSLen < 0 || encCLen < 0 {
				return nil, fmt.Errorf("%w: invalid sheaf vans literal parameters", ErrCorruptedFrame)
			}
			if uint64(24)+uint64(maskLen)+uint64(encSLen)+uint64(encCLen) > uint64(len(src)) {
				return nil, fmt.Errorf("%w: truncated sheaf vans payload", ErrCorruptedFrame)
			}

			mask := src[24 : 24+maskLen]
			if !validateSheafMask(mask, origLen, sLen) {
				return nil, fmt.Errorf("%w: invalid sheaf mask popcount or dirty padding bits", ErrCorruptedFrame)
			}

			encS := src[24+maskLen : 24+maskLen+encSLen]
			encC := src[24+maskLen+encSLen : 24+maskLen+encSLen+encCLen]
			cLen := origLen - sLen

			if cap(pad.sheafSBuf) < sLen+64 {
				pad.sheafSBuf = bufkit.AlignedBytes(sLen+64, bufkit.CacheLineSize)
			}
			if cap(pad.sheafCBuf) < cLen+64 {
				pad.sheafCBuf = bufkit.AlignedBytes(cLen+64, bufkit.CacheLineSize)
			}
			if cap(pad.litBuf) < origLen+64 {
				pad.litBuf = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
			}

			decS, err := VANSDecode4xTo(pad.sheafSBuf[:sLen], encS, &pad.vansModelS)
			if err != nil {
				return nil, fmt.Errorf("%w: decoding sheaf syntax stream: %w", ErrCorruptedFrame, err)
			}
			if len(decS) != sLen {
				return nil, fmt.Errorf(
					"%w: decoded sheaf syntax stream length %d != sLen %d",
					ErrCorruptedFrame,
					len(decS),
					sLen,
				)
			}

			decC, err := VANSDecode4xTo(pad.sheafCBuf[:cLen], encC, &pad.vansModelC)
			if err != nil {
				return nil, fmt.Errorf("%w: decoding sheaf content stream: %w", ErrCorruptedFrame, err)
			}
			if len(decC) != cLen {
				return nil, fmt.Errorf(
					"%w: decoded sheaf content stream length %d != cLen %d",
					ErrCorruptedFrame,
					len(decC),
					cLen,
				)
			}

			SheafInterleave(pad.litBuf[:origLen], pad.sheafSBuf[:sLen], pad.sheafCBuf[:cLen], mask, origLen)
			return pad.litBuf[:origLen], nil
		}

		origLen := int(binary.LittleEndian.Uint32(src[4:8]))
		if origLen < 0 || origLen > MaxBlockSize {
			return nil, fmt.Errorf("%w: invalid V-ANS literal origLen %d", ErrCorruptedFrame, origLen)
		}
		if cap(pad.litBuf) < origLen+64 {
			pad.litBuf = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
		}
		return VANSDecode4xTo(pad.litBuf[:origLen], src, &pad.vansModel)
	}

	return unpackNibblePlanes(src, &pad.litBuf)
}

// unpackNibblePlanes reconstructs raw literals from the Low-Nibble and High-Nibble planes.
func unpackNibblePlanes(src []byte, bPtr *[]byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, fmt.Errorf("%w: truncated nibble plane header", ErrCorruptedFrame)
	}

	numLits := int(binary.LittleEndian.Uint32(src[0:4]))
	lowBytes := int(binary.LittleEndian.Uint32(src[4:8]))

	if numLits < 0 || numLits > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid nibble plane numLits %d", ErrCorruptedFrame, numLits)
	}
	if numLits == 0 {
		if lowBytes != 0 {
			return nil, fmt.Errorf("%w: corrupted nibble plane sizes", ErrCorruptedFrame)
		}
		return []byte{}, nil
	}

	expectedLow := (numLits + 1) / 2
	if lowBytes != expectedLow || 8+lowBytes > len(src) {
		return nil, fmt.Errorf("%w: corrupted nibble plane sizes", ErrCorruptedFrame)
	}

	if cap(*bPtr) < numLits+64 {
		*bPtr = bufkit.AlignedBytes(numLits+64, bufkit.CacheLineSize)
	}
	dstBuf := (*bPtr)[:numLits]
	dstPtr := unsafe.Pointer(&dstBuf[0])

	lowPlane := src[8 : 8+lowBytes]
	highPlane := src[8+lowBytes:]

	// 1. High Nibble Plane Unpack (Nibble-RLE tokens: (runLen-1)<<4 | highNibble)
	p := 0
	for _, token := range highPlane {
		runLen := int(token>>4) + 1
		if p+runLen > numLits {
			return nil, fmt.Errorf("%w: nibble plane literal overflow", ErrCorruptedFrame)
		}
		h := (token & 0x0F) << 4
		pattern := simd.RepeatByte0x01 * uint64(h)
		pDst := unsafe.Add(dstPtr, p)
		*(*uint64)(pDst) = pattern
		if runLen > 8 {
			*(*uint64)(unsafe.Add(pDst, 8)) = pattern
		}
		p += runLen
	}

	if p != numLits {
		return nil, fmt.Errorf("%w: high nibble count mismatch: got %d, want %d", ErrCorruptedFrame, p, numLits)
	}

	// 2. Low Nibble Plane Merge (Packed pairs: (b0 << 4) | b1)
	j := 0
	lowPlanePtr := unsafe.Pointer(&lowPlane[0])
	for ; j+8 <= lowBytes && (j+7)*2+1 < numLits; j += 8 {
		p0 := *(*byte)(unsafe.Add(lowPlanePtr, j))
		p1 := *(*byte)(unsafe.Add(lowPlanePtr, j+1))
		p2 := *(*byte)(unsafe.Add(lowPlanePtr, j+2))
		p3 := *(*byte)(unsafe.Add(lowPlanePtr, j+3))
		p4 := *(*byte)(unsafe.Add(lowPlanePtr, j+4))
		p5 := *(*byte)(unsafe.Add(lowPlanePtr, j+5))
		p6 := *(*byte)(unsafe.Add(lowPlanePtr, j+6))
		p7 := *(*byte)(unsafe.Add(lowPlanePtr, j+7))

		d0 := uint64(p0>>4) | (uint64(p0&0x0F) << 8) | (uint64(p1>>4) << 16) | (uint64(p1&0x0F) << 24) |
			(uint64(p2>>4) << 32) | (uint64(p2&0x0F) << 40) | (uint64(p3>>4) << 48) | (uint64(p3&0x0F) << 56)
		d1 := uint64(p4>>4) | (uint64(p4&0x0F) << 8) | (uint64(p5>>4) << 16) | (uint64(p5&0x0F) << 24) |
			(uint64(p6>>4) << 32) | (uint64(p6&0x0F) << 40) | (uint64(p7>>4) << 48) | (uint64(p7&0x0F) << 56)

		*(*uint64)(unsafe.Add(dstPtr, 2*j)) |= d0
		*(*uint64)(unsafe.Add(dstPtr, 2*j+8)) |= d1
	}
	for ; j < lowBytes; j++ {
		pair := *(*byte)(unsafe.Add(lowPlanePtr, j))
		idx := j * 2
		*(*byte)(unsafe.Add(dstPtr, idx)) |= pair >> 4
		if idx+1 < numLits {
			*(*byte)(unsafe.Add(dstPtr, idx+1)) |= pair & 0x0F
		}
	}

	return dstBuf, nil
}
