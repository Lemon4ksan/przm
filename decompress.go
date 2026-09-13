// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/codec/filter/bcj"
	"github.com/lemon4ksan/foundation/generic"
	"github.com/lemon4ksan/foundation/silicon/simd"
)

var decLitPool = generic.NewPool(func() *[]byte {
	b := bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize)
	return &b
})

// fastReadVarint decodes an unsigned varint with inline 1-byte and 2-byte fast paths.
// In PRZM frames, offsets and match lengths <= 256K are guaranteed <= 3 bytes.
func fastReadVarint(src []byte, ptr int) (int, int) {
	b0 := src[ptr]
	ptr++
	if b0 < 0x80 {
		return int(b0), ptr
	}
	b1 := src[ptr]
	ptr++
	if b1 < 0x80 {
		return int(b0&0x7F) | (int(b1) << 7), ptr
	}
	b2 := src[ptr]
	ptr++
	if b2 < 0x80 {
		return int(b0&0x7F) | (int(b1&0x7F) << 7) | (int(b2) << 14), ptr
	}
	extra, n := binary.Uvarint(src[ptr-3:])
	return int(extra), ptr - 3 + n
}

// copyMatch copies mLen bytes from dst[srcIdx] to dst[dstPos] with zero bounds checks.
// Assumes dst has at least 64 bytes padding beyond uSize.
func copyMatch(baseDst unsafe.Pointer, dstPos, srcIdx, mLen, d int) {
	pSrc := unsafe.Add(baseDst, srcIdx)
	pDst := unsafe.Add(baseDst, dstPos)

	if d >= 32 {
		for mLen > 32 {
			*(*uint64)(pDst) = *(*uint64)(pSrc)
			*(*uint64)(unsafe.Add(pDst, 8)) = *(*uint64)(unsafe.Add(pSrc, 8))
			*(*uint64)(unsafe.Add(pDst, 16)) = *(*uint64)(unsafe.Add(pSrc, 16))
			*(*uint64)(unsafe.Add(pDst, 24)) = *(*uint64)(unsafe.Add(pSrc, 24))
			pSrc = unsafe.Add(pSrc, 32)
			pDst = unsafe.Add(pDst, 32)
			mLen -= 32
		}
		for mLen > 0 {
			*(*uint64)(pDst) = *(*uint64)(pSrc)
			pSrc = unsafe.Add(pSrc, 8)
			pDst = unsafe.Add(pDst, 8)
			mLen -= 8
		}
		return
	}
	if d >= 8 {
		for mLen > 0 {
			*(*uint64)(pDst) = *(*uint64)(pSrc)
			pSrc = unsafe.Add(pSrc, 8)
			pDst = unsafe.Add(pDst, 8)
			mLen -= 8
		}
		return
	}
	if d == 1 {
		pattern := simd.RepeatByte0x01 * uint64(*(*byte)(pSrc))
		for mLen > 32 {
			*(*uint64)(pDst) = pattern
			*(*uint64)(unsafe.Add(pDst, 8)) = pattern
			*(*uint64)(unsafe.Add(pDst, 16)) = pattern
			*(*uint64)(unsafe.Add(pDst, 24)) = pattern
			pDst = unsafe.Add(pDst, 32)
			mLen -= 32
		}
		for mLen > 0 {
			*(*uint64)(pDst) = pattern
			pDst = unsafe.Add(pDst, 8)
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
			mLen -= 8
		}
		return
	}
	for i := 0; i < mLen; i++ {
		*(*byte)(unsafe.Add(pDst, i)) = *(*byte)(unsafe.Add(pSrc, i))
	}
}

// unpackLiterals unpacks literals from Section 4, supporting both v0.2 V-ANS and v0.1 Nibble-Planes.
func unpackLiterals(src []byte, bPtr *[]byte) ([]byte, error) {
	if len(src) >= 8 && src[0] == vansMagic0 && src[1] == vansMagic1 && src[2] == vansVer1 {
		return VANSDecode4x(src)
	}
	return unpackNibblePlanes(src, bPtr)
}

// unpackNibblePlanes reconstructs raw literals from the Low-Nibble and High-Nibble planes.
func unpackNibblePlanes(src []byte, bPtr *[]byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, fmt.Errorf("%w: truncated nibble plane header", ErrCorruptedFrame)
	}

	numLits := int(binary.LittleEndian.Uint32(src[0:4]))
	lowBytes := int(binary.LittleEndian.Uint32(src[4:8]))

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

		d0 := (uint64(p0 >> 4)) | (uint64(p0&0x0F) << 8) | (uint64(p1>>4) << 16) | (uint64(p1&0x0F) << 24) |
			(uint64(p2>>4) << 32) | (uint64(p2&0x0F) << 40) | (uint64(p3>>4) << 48) | (uint64(p3&0x0F) << 56)
		d1 := (uint64(p4 >> 4)) | (uint64(p4&0x0F) << 8) | (uint64(p5>>4) << 16) | (uint64(p5&0x0F) << 24) |
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

// Decompress decodes a PRZM-compressed byte slice back to original uncompressed bytes.
func Decompress(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}

	if len(src) >= 4 {
		// 32-bit magic formats (h-MDS, TDA, Spectral, Poincare, Clifford, GCTR)
		switch binary.LittleEndian.Uint32(src[0:4]) {
		case HMDSMagic:
			return DecompressHMDS(src, nil)
		case TDAMagic:
			return DecompressTDA(src, nil)
		case SpectralMagic:
			return DecompressSpectralUnitary(src, nil)
		case SymplecticMagic:
			return DecompressSymplectic(src, nil)
		case MahlerMagic:
			return DecompressMahler(src, nil)
		case SheafMagic:
			return DecompressSheafTrellis(src, nil)
		case ARM64Magic:
			return DecompressARM64(src, nil)
		case MacroTrellisMagic:
			return DecompressMacroTrellis(src, nil)
		case DependentSheafMagic:
			return DecompressDependentSheaf(src, nil)
		case PoincareMagic:
			return DecodeHyperbolicAST(src)
		case CliffordMagic:
			return DecodeCliffordRotors(src)
		case GCTRMagic:
			return DecompressGCTR(src, nil)
		}

		// 3-byte versioned ISA formats (Zero-Schema VM, PWS, GGTC)
		switch {
		case src[0] == vmMagic0 && src[1] == vmMagic1 && src[2] == vmVer1:
			return VMDecode(src)
		case src[0] == pwsMagic0 && src[1] == pwsMagic1 && src[2] == pwsVer1:
			return PWSDecode(src)
		case src[0] == ggtcMagic0 && src[1] == ggtcMagic1 && src[2] == ggtcVer1:
			res, _, err := decodeFullFrame(src)
			return res, err
		}
	}

	hdr, err := ParseFrameHeader(src)
	if err != nil {
		return nil, err
	}

	uSize := hdr.UncompressedSize
	if uSize == 0 {
		return []byte{}, nil
	}

	allocSize := uSize
	if (hdr.Flags & FlagBCJSplit) != 0 {
		allocSize = 4 + 4*int(hdr.CallCount) + uSize
	}

	// Safety padding (64 bytes) prevents vector load/store boundary faults
	// Aligned to 64-byte L1 cacheline boundary to eliminate split-cacheline penalties
	dst := bufkit.AlignedBytes(allocSize+64, bufkit.CacheLineSize)
	baseDst := unsafe.Pointer(&dst[0])

	// Fast-path: Raw uncompressed payload
	if (hdr.Flags & FlagUncompressed) != 0 {
		if len(src)-HeaderSize < uSize {
			return nil, fmt.Errorf("%w: truncated uncompressed payload (%d < %d)", ErrCorruptedFrame, len(src)-HeaderSize, uSize)
		}
		copy(dst[:uSize], src[HeaderSize:HeaderSize+uSize])
		return dst[:uSize], nil
	}

	// Section layout resolution
	sec1Offset := hdr.DataOffset
	sec1Size := 0
	if sec1Offset < len(src) {
		// Distribution parameters (Section 1)
		// Minimum 8 bytes if present
		if sec1Offset+8 <= len(src) {
			exceptionCount := int(src[sec1Offset+5])
			sec1Size = 8 + 2*exceptionCount
		}
	}

	sec2Offset := sec1Offset + sec1Size
	sec3Offset := sec2Offset + hdr.CmdStreamSize
	sec4Offset := sec3Offset + hdr.OffsetStreamSize

	if sec4Offset > len(src) {
		return nil, fmt.Errorf("%w: frame truncated before literal section", ErrCorruptedFrame)
	}

	var lits []byte
	if (hdr.Flags & FlagNibble) != 0 {
		bPtr := decLitPool.Get()
		defer decLitPool.Put(bPtr)
		unpacked, err := unpackLiterals(src[sec4Offset:], bPtr)
		if err != nil {
			return nil, err
		}
		lits = unpacked
	} else {
		lits = src[sec4Offset:]
	}

	cmdPtr := sec2Offset
	cmdEnd := sec2Offset + hdr.CmdStreamSize
	offPtr := sec3Offset
	litPtr := 0

	// Initialize Ring-16 locality queue
	stride := int(hdr.Stride)
	if stride < 1 {
		stride = 1
	}
	q := [16]int{1, stride, 2 * stride, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384}

	dstPos := 0

	var activeDict [][]byte
	if (hdr.Flags & FlagDictRef) != 0 {
		activeDict = getBuiltinDictBytes(hdr.DictID)
	}

	for dstPos < allocSize && cmdPtr < cmdEnd {
		op := src[cmdPtr]
		cmdPtr++

		if op < 0x80 {
			// Fast path 1: Fused Repeat Token (00 LLL MM KK)
			litLen := int((op >> 4) & 0x07)
			mLen := int((op>>2)&0x03) + 4
			k := int(op & 0x03)

			if litLen > 0 {
				if litPtr+8 <= len(lits) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else if litPtr+litLen <= len(lits) {
					copy(dst[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				} else {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				litPtr += litLen
				dstPos += litLen
			}

			d := q[k]
			switch k {
			case 0:
			case 1:
				q[1] = q[0]
				q[0] = d
			case 2:
				q[2] = q[1]
				q[1] = q[0]
				q[0] = d
			case 3:
				q[3] = q[2]
				q[2] = q[1]
				q[1] = q[0]
				q[0] = d
			default:
				for j := k; j > 0; j-- {
					q[j] = q[j-1]
				}
				q[0] = d
			}

			srcIdx := dstPos - d
			if d >= 8 || d >= mLen {
				*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Add(baseDst, srcIdx))
			} else if d == 1 {
				pattern := simd.RepeatByte0x01 * uint64(*(*byte)(unsafe.Add(baseDst, srcIdx)))
				*(*uint64)(unsafe.Add(baseDst, dstPos)) = pattern
			} else if d == 2 {
				w := uint64(*(*uint16)(unsafe.Add(baseDst, srcIdx)))
				pattern := w | (w << 16) | (w << 32) | (w << 48)
				*(*uint64)(unsafe.Add(baseDst, dstPos)) = pattern
			} else if d == 4 {
				w := uint64(*(*uint32)(unsafe.Add(baseDst, srcIdx)))
				pattern := w | (w << 32)
				*(*uint64)(unsafe.Add(baseDst, dstPos)) = pattern
			} else if d == 3 {
				pS := unsafe.Add(baseDst, srcIdx)
				b0 := uint64(*(*byte)(pS))
				b1 := uint64(*(*byte)(unsafe.Add(pS, 1)))
				b2 := uint64(*(*byte)(unsafe.Add(pS, 2)))
				pattern := b0 | (b1 << 8) | (b2 << 16) | (b0 << 24) | (b1 << 32) | (b2 << 40) | (b0 << 48) | (b1 << 56)
				*(*uint64)(unsafe.Add(baseDst, dstPos)) = pattern
			} else {
				for i := 0; i < mLen; i++ {
					*(*byte)(unsafe.Add(baseDst, dstPos+i)) = *(*byte)(unsafe.Add(baseDst, srcIdx+i))
				}
			}
			dstPos += mLen
			continue
		}

		if op < 0xC0 {
			// Fast path 2: Fused Extended Match (10 LL MMMM)
			litLen := int((op >> 4) & 0x03)
			baseLen := int(op & 0x0F)
			mLen := baseLen + 4
			if baseLen == 15 {
				var extra int
				extra, cmdPtr = fastReadVarint(src, cmdPtr)
				mLen = 19 + extra
			}

			if litLen > 0 {
				if litPtr+8 <= len(lits) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else if litPtr+litLen <= len(lits) {
					copy(dst[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				} else {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				litPtr += litLen
				dstPos += litLen
			}

			var d int
			d, offPtr = fastReadVarint(src, offPtr)

			for j := 15; j > 0; j-- {
				q[j] = q[j-1]
			}
			q[0] = d

			srcIdx := dstPos - d
			copyMatch(baseDst, dstPos, srcIdx, mLen, d)
			dstPos += mLen
			continue
		}

		// Specialized commands (0xC0 .. 0xFF)
		switch {
		case op >= 0xC0 && op <= 0xCF:
			// Long Repeat Match (k 0..15) with Fused Literals
			k := int(op & 0x0F)
			token := src[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			baseLen := int(token & 0x0F)
			mLen := baseLen + 4
			if baseLen == 15 {
				var extra int
				extra, cmdPtr = fastReadVarint(src, cmdPtr)
				mLen = 19 + extra
			}

			if litLen > 0 {
				if litPtr+litLen > len(lits) {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				if litLen <= 8 && litPtr+8 <= len(lits) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else if litLen <= 16 && litPtr+16 <= len(lits) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
					*(*uint64)(unsafe.Add(baseDst, dstPos+8)) = *(*uint64)(unsafe.Pointer(&lits[litPtr+8]))
				} else {
					copy(dst[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				}
				litPtr += litLen
				dstPos += litLen
			}

			d := q[k]
			switch k {
			case 0:
			case 1:
				q[1] = q[0]
				q[0] = d
			case 2:
				q[2] = q[1]
				q[1] = q[0]
				q[0] = d
			case 3:
				q[3] = q[2]
				q[2] = q[1]
				q[1] = q[0]
				q[0] = d
			default:
				for j := k; j > 0; j-- {
					q[j] = q[j-1]
				}
				q[0] = d
			}

			srcIdx := dstPos - d
			copyMatch(baseDst, dstPos, srcIdx, mLen, d)
			dstPos += mLen

		case op >= 0xD0 && op <= 0xD7:
			// Stride Match (m 1..8)
			m := int(op&0x07) + 1
			token := src[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			mLen := int(token&0x0F) + 4

			if litLen > 0 {
				if litPtr+litLen > len(lits) {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				copy(dst[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				litPtr += litLen
				dstPos += litLen
			}

			d := m * stride
			for j := 15; j > 0; j-- {
				q[j] = q[j-1]
			}
			q[0] = d

			srcIdx := dstPos - d
			copyMatch(baseDst, dstPos, srcIdx, mLen, d)
			dstPos += mLen

		case op >= 0xE0 && op <= 0xE7:
			// Delta-Mask with Locality Index k
			k := int(op & 0x07)
			baseLen := int(src[cmdPtr])
			cmdPtr++
			mLen := baseLen + 4
			if baseLen == 255 {
				var extra int
				extra, cmdPtr = fastReadVarint(src, cmdPtr)
				mLen = 259 + extra
			}

			d := q[k]
			switch k {
			case 0:
			case 1:
				q[1] = q[0]
				q[0] = d
			case 2:
				q[2] = q[1]
				q[1] = q[0]
				q[0] = d
			case 3:
				q[3] = q[2]
				q[2] = q[1]
				q[1] = q[0]
				q[0] = d
			default:
				for j := k; j > 0; j-- {
					q[j] = q[j-1]
				}
				q[0] = d
			}

			srcIdx := dstPos - d
			copyMatch(baseDst, dstPos, srcIdx, mLen, d)

			if litPtr >= len(lits) {
				return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
			}
			cMods := int(lits[litPtr])
			litPtr++
			if cMods == 1 && mLen <= 256 {
				if litPtr+2 > len(lits) {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				relOffset := int(lits[litPtr])
				xorVal := lits[litPtr+1]
				litPtr += 2
				if relOffset < mLen {
					*(*byte)(unsafe.Add(baseDst, dstPos+relOffset)) ^= xorVal
				}
			} else if cMods == 2 && mLen <= 256 {
				if litPtr+4 > len(lits) {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				r0 := int(lits[litPtr])
				x0 := lits[litPtr+1]
				r1 := int(lits[litPtr+2])
				x1 := lits[litPtr+3]
				litPtr += 4
				if r0 < mLen {
					*(*byte)(unsafe.Add(baseDst, dstPos+r0)) ^= x0
				}
				if r1 < mLen {
					*(*byte)(unsafe.Add(baseDst, dstPos+r1)) ^= x1
				}
			} else {
				for mMod := 0; mMod < cMods; mMod++ {
					var relOffset int
					if mLen <= 256 {
						if litPtr >= len(lits) {
							return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
						}
						relOffset = int(lits[litPtr])
						litPtr++
					} else {
						if litPtr+2 > len(lits) {
							return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
						}
						relOffset = int(binary.LittleEndian.Uint16(lits[litPtr : litPtr+2]))
						litPtr += 2
					}
					if litPtr >= len(lits) {
						return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
					}
					xorVal := lits[litPtr]
					litPtr++
					if relOffset < mLen {
						*(*byte)(unsafe.Add(baseDst, dstPos+relOffset)) ^= xorVal
					}
				}
			}
			dstPos += mLen

		case op == 0xE8:
			// Delta-Mask with Explicit Offset
			baseLen := int(src[cmdPtr])
			cmdPtr++
			mLen := baseLen + 4
			if baseLen == 255 {
				var extra int
				extra, cmdPtr = fastReadVarint(src, cmdPtr)
				mLen = 259 + extra
			}

			var d int
			d, offPtr = fastReadVarint(src, offPtr)

			for j := 15; j > 0; j-- {
				q[j] = q[j-1]
			}
			q[0] = d

			srcIdx := dstPos - d
			copyMatch(baseDst, dstPos, srcIdx, mLen, d)

			if litPtr >= len(lits) {
				return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
			}
			cMods := int(lits[litPtr])
			litPtr++
			if cMods == 1 && mLen <= 256 {
				if litPtr+2 > len(lits) {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				relOffset := int(lits[litPtr])
				xorVal := lits[litPtr+1]
				litPtr += 2
				if relOffset < mLen {
					*(*byte)(unsafe.Add(baseDst, dstPos+relOffset)) ^= xorVal
				}
			} else if cMods == 2 && mLen <= 256 {
				if litPtr+4 > len(lits) {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				r0 := int(lits[litPtr])
				x0 := lits[litPtr+1]
				r1 := int(lits[litPtr+2])
				x1 := lits[litPtr+3]
				litPtr += 4
				if r0 < mLen {
					*(*byte)(unsafe.Add(baseDst, dstPos+r0)) ^= x0
				}
				if r1 < mLen {
					*(*byte)(unsafe.Add(baseDst, dstPos+r1)) ^= x1
				}
			} else {
				for mMod := 0; mMod < cMods; mMod++ {
					var relOffset int
					if mLen <= 256 {
						if litPtr >= len(lits) {
							return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
						}
						relOffset = int(lits[litPtr])
						litPtr++
					} else {
						if litPtr+2 > len(lits) {
							return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
						}
						relOffset = int(binary.LittleEndian.Uint16(lits[litPtr : litPtr+2]))
						litPtr += 2
					}
					if litPtr >= len(lits) {
						return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
					}
					xorVal := lits[litPtr]
					litPtr++
					if relOffset < mLen {
						*(*byte)(unsafe.Add(baseDst, dstPos+relOffset)) ^= xorVal
					}
				}
			}
			dstPos += mLen

		case op == 0xF0:
			// Pure Literal Run
			var runLen int
			runLen, cmdPtr = fastReadVarint(src, cmdPtr)
			if litPtr+runLen > len(lits) {
				return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
			}
			copy(dst[dstPos:dstPos+runLen], lits[litPtr:litPtr+runLen])
			litPtr += runLen
			dstPos += runLen

		case op == 0xF1:
			// RLE Run
			var rleLen int
			rleLen, cmdPtr = fastReadVarint(src, cmdPtr)
			if litPtr >= len(lits) {
				return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
			}
			rleByte := lits[litPtr]
			litPtr++
			pattern := simd.RepeatByte0x01 * uint64(rleByte)
			pDst := unsafe.Add(baseDst, dstPos)
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
			dstPos += rleLen

		case op >= 0xD8 && op <= 0xDF:
			// Direct 1-byte schema token from active dictionary
			if len(activeDict) == 0 {
				return nil, fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
			}
			tokIdx := int(op - 0xD8)
			if tokIdx >= len(activeDict) {
				return nil, fmt.Errorf("%w: invalid dictionary token ID %d", ErrCorruptedFrame, tokIdx)
			}
			tok := activeDict[tokIdx]
			copy(dst[dstPos:], tok)
			dstPos += len(tok)

		case op == 0xF2:
			// Extended 2-byte schema token from active dictionary
			if len(activeDict) == 0 {
				return nil, fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
			}
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated dictionary token opcode", ErrCorruptedFrame)
			}
			tokIdx := int(src[cmdPtr])
			cmdPtr++
			if tokIdx >= len(activeDict) {
				return nil, fmt.Errorf("%w: invalid dictionary token ID %d", ErrCorruptedFrame, tokIdx)
			}
			tok := activeDict[tokIdx]
			copy(dst[dstPos:], tok)
			dstPos += len(tok)

		case op >= 0xF3 && op <= 0xFA:
			// Direct 1-byte Literal Run (1..8 literals)
			runLen := int(op - 0xF2)
			if litPtr+runLen > len(lits) {
				return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
			}
			pDst := unsafe.Add(baseDst, dstPos)
			pSrc := unsafe.Pointer(&lits[litPtr])
			*(*uint64)(pDst) = *(*uint64)(pSrc)
			litPtr += runLen
			dstPos += runLen
		}
	}

	if dstPos < allocSize {
		return nil, fmt.Errorf("%w: incomplete decoding: got %d of %d bytes", ErrCorruptedFrame, dstPos, allocSize)
	}

	if (hdr.Flags & FlagBCJSplit) != 0 {
		bPtr := decLitPool.Get()
		defer decLitPool.Put(bPtr)
		unsplitX86(dst, uSize, int(hdr.CallCount), *bPtr)
		return dst[:uSize], nil
	}

	if (hdr.Flags & FlagBCJ) != 0 {
		bcj.Filter(bcj.X86, dst[:uSize], 0, false)
	}

	return dst[:uSize], nil
}
