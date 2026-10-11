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
	"github.com/lemon4ksan/foundation/generic"
	"github.com/lemon4ksan/foundation/silicon/simd"
)

var decLitPool = generic.NewPool(func() *[]byte {
	b := bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize)
	return &b
})

// decompressScratchpad holds all mutable scratch buffers required for zero-alloc frame decode.
type decompressScratchpad struct {
	table       CanonicalHuffmanTable
	cmdBuf      []byte
	offBuf      []byte
	litBuf      []byte
	normBuf     []byte
	scanlineBuf []byte
	sheafSBuf   []byte
	sheafCBuf   []byte
	vansModelS  VANSModel
	vansModelC  VANSModel
	vansModel   VANSModel
}

var decScratchPool = generic.NewPool(func() *decompressScratchpad {
	return &decompressScratchpad{
		cmdBuf:      bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
		offBuf:      bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
		litBuf:      bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
		normBuf:     bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
		scanlineBuf: bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
		sheafSBuf:   bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
		sheafCBuf:   bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize),
	}
})

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
		return int(b0&0x7F) | (int(b1&0x7F) << 7) | (int(b2&0x7F) << 14) | (int(b3) << 21), ptr, nil
	}
	if ptr-4 < 0 || ptr-4 > len(src) {
		return 0, ptr, fmt.Errorf("%w: invalid varint offset", ErrCorruptedFrame)
	}
	extra, n := binary.Uvarint(src[ptr-4:])
	if n <= 0 {
		return 0, ptr, fmt.Errorf("%w: corrupted varint encoding", ErrCorruptedFrame)
	}
	return int(extra), ptr - 4 + n, nil
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

// execMatch safely executes a match copy, transparently handling positive offsets in outBuf
// and negative offsets into the raw prefix dictionary buffer.
func execMatch(baseDst unsafe.Pointer, outBuf []byte, rawDict []byte, dstPos, d, mLen int) error {
	if d <= 0 || dstPos+mLen > len(outBuf) {
		return fmt.Errorf("%w: invalid match bounds (d=%d, dstPos=%d, mLen=%d)", ErrCorruptedFrame, d, dstPos, mLen)
	}
	srcIdx := dstPos - d
	if srcIdx >= 0 {
		copyMatch(baseDst, dstPos, srcIdx, mLen, d)
		return nil
	}
	if len(rawDict) == 0 {
		return fmt.Errorf("%w: invalid match distance %d (srcIdx=%d, dstPos=%d, mLen=%d)", ErrCorruptedFrame, d, srcIdx, dstPos, mLen)
	}
	dictOffset := len(rawDict) + srcIdx
	if dictOffset < 0 || dictOffset >= len(rawDict) {
		return fmt.Errorf("%w: dictionary offset %d out of bounds (dictLen=%d)", ErrCorruptedFrame, dictOffset, len(rawDict))
	}
	dictAvail := len(rawDict) - dictOffset
	copyFromDict := min(mLen, dictAvail)
	copy(outBuf[dstPos:dstPos+copyFromDict], rawDict[dictOffset:dictOffset+copyFromDict])
	if mLen > copyFromDict {
		remLen := mLen - copyFromDict
		copyStart := dstPos + copyFromDict
		for i := 0; i < remLen; i++ {
			outBuf[copyStart+i] = outBuf[i]
		}
	}
	return nil
}

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
				return nil, fmt.Errorf("%w: decoding sheaf syntax stream: %v", ErrCorruptedFrame, err)
			}
			if len(decS) != sLen {
				return nil, fmt.Errorf("%w: decoded sheaf syntax stream length %d != sLen %d", ErrCorruptedFrame, len(decS), sLen)
			}

			decC, err := VANSDecode4xTo(pad.sheafCBuf[:cLen], encC, &pad.vansModelC)
			if err != nil {
				return nil, fmt.Errorf("%w: decoding sheaf content stream: %v", ErrCorruptedFrame, err)
			}
			if len(decC) != cLen {
				return nil, fmt.Errorf("%w: decoded sheaf content stream length %d != cLen %d", ErrCorruptedFrame, len(decC), cLen)
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

// GetUncompressedSize returns the uncompressed payload size of any PRZM frame or PRZS stream in O(1) time.
func GetUncompressedSize(src []byte) (int64, error) {
	if len(src) < 4 {
		return 0, ErrCorruptedFrame
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic == MagicPRZS {
		if len(src) < StreamHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int64(binary.LittleEndian.Uint64(src[5:13])), nil
	}
	sz, err := getFrameUncompressedSize(src)
	if err != nil {
		return 0, err
	}
	return int64(sz), nil
}

// getFrameUncompressedSize parses the uncompressed size from a chunk frame without allocations.
func getFrameUncompressedSize(src []byte) (int, error) {
	if len(src) < 4 {
		return 0, ErrCorruptedFrame
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	switch magic {
	case MagicPRZM:
		if len(src) < HeaderSize {
			return 0, ErrCorruptedFrame
		}
		uSize := int(uint32(src[5]) | (uint32(src[6]) << 8) | (uint32(src[7]) << 16))
		if uSize > MaxBlockSize {
			return 0, ErrExceedsMaxBlock
		}
		return uSize, nil
	case MagicPRZS:
		if len(src) < StreamHeaderSize {
			return 0, ErrCorruptedFrame
		}
		uSize := binary.LittleEndian.Uint64(src[5:13])
		return int(uSize), nil
	case ArchivalPNGMagic:
		if len(src) < 12 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case MobiusMagic, MargolusMagic, HeisenbergMagic, CellularSheafMagic, HodgeMagic:
		if len(src) < 8 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case SymplecticMagic, ARM64Magic, SheafMagic, MahlerMagic, IsoQuantMagic, PadicMagic:
		if len(src) < 12 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[8:12])), nil
	case GCTRMagic:
		if len(src) < 10 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[6:10])), nil
	default:
		// Versioned 3-byte ISA headers
		if len(src) >= 8 && ((src[0] == pwsMagic0 && src[1] == pwsMagic1 && src[2] == pwsVer1) ||
			(src[0] == vmMagic0 && src[1] == vmMagic1 && src[2] == vmVer1) ||
			(src[0] == ggtcMagic0 && src[1] == ggtcMagic1 && src[2] == ggtcVer1)) {
			return int(binary.LittleEndian.Uint32(src[4:8])), nil
		}
		return 0, ErrInvalidMagic
	}
}

// DecompressOptions defines optional parameters for decompression.
type DecompressOptions struct {
	Dict    *Dictionary
	RawDict []byte
}

// Decompress decodes a PRZM-compressed byte slice back to original uncompressed bytes.
func Decompress(src []byte) ([]byte, error) {
	return DecompressTo(src, nil)
}

// DecompressWithOptions decodes a PRZM frame with optional dictionary overrides.
func DecompressWithOptions(src []byte, opts DecompressOptions) ([]byte, error) {
	return decompressWithDictToEx(src, nil, opts.Dict, opts.RawDict)
}

// DecompressTo decodes a PRZM-compressed frame into a destination buffer dst.
// Strictly guarantees 0 heap allocations if dst has capacity >= uncompressed size + 64.
func DecompressTo(src, dst []byte) ([]byte, error) {
	return decompressWithDictTo(src, dst, nil)
}

func decompressWithDictTo(src, dst []byte, customDict *Dictionary) ([]byte, error) {
	return decompressWithDictToEx(src, dst, customDict, nil)
}

func decompressWithDictToEx(src, dst []byte, customDict *Dictionary, explicitRawDict []byte) ([]byte, error) {
	if len(src) == 0 {
		if src == nil {
			return nil, ErrCorruptedFrame
		}
		return []byte{}, nil
	}
	if len(src) < 4 {
		return nil, ErrCorruptedFrame
	}

	if len(src) >= 4 {
		magic := binary.LittleEndian.Uint32(src[0:4])
		if magic == MagicPRZS {
			if len(src) < StreamHeaderSize {
				return nil, ErrCorruptedFrame
			}
			totalUSize := binary.LittleEndian.Uint64(src[5:13])
			chunkCount := binary.LittleEndian.Uint32(src[13:17])

			if int64(totalUSize) < 0 || totalUSize > uint64(chunkCount)*uint64(MaxBlockSize) {
				return nil, ErrCorruptedFrame
			}

			if dst == nil || cap(dst) < int(totalUSize)+64 {
				dst = bufkit.AlignedBytes(int(totalUSize)+64, bufkit.CacheLineSize)
			}
			dst = dst[:totalUSize]

			offset := StreamHeaderSize
			dstPos := 0

			for range chunkCount {
				if offset+4 > len(src) {
					return nil, ErrCorruptedFrame
				}
				chunkCompSize := int(binary.LittleEndian.Uint32(src[offset : offset+4]))
				offset += 4
				if chunkCompSize <= 0 || offset+chunkCompSize > len(src) {
					return nil, ErrCorruptedFrame
				}
				chunkSrc := src[offset : offset+chunkCompSize]
				offset += chunkCompSize

				chunkUSize, err := getFrameUncompressedSize(chunkSrc)
				if err != nil {
					return nil, err
				}
				if dstPos+chunkUSize > int(totalUSize) {
					return nil, ErrCorruptedFrame
				}

				_, err = decompressWithDictToEx(chunkSrc, dst[dstPos:dstPos+chunkUSize], customDict, explicitRawDict)
				if err != nil {
					return nil, err
				}
				dstPos += chunkUSize
			}

			if dstPos != int(totalUSize) {
				return nil, ErrCorruptedFrame
			}
			return dst[:totalUSize], nil
		}



		// 32-bit magic formats (h-MDS, TDA, Spectral, Poincare, Clifford, GCTR, Heisenberg)
		switch magic {
		case HMDSMagic:
			return DecompressHMDS(src, dst)
		case TDAMagic:
			return DecompressTDA(src, dst)
		case SpectralMagic:
			return DecompressSpectralUnitary(src, dst)
		case SymplecticMagic:
			return DecompressSymplectic(src, dst)
		case MahlerMagic:
			return DecompressMahler(src, dst)
		case SheafMagic:
			return DecompressSheaf(src, dst)
		case ARM64Magic:
			return DecompressARM64(src, dst)
		case MacroTrellisMagic:
			return DecompressMacroTrellis(src, dst)
		case DependentSheafMagic:
			return DecompressDependentSheaf(src, dst)
		case PoincareMagic:
			return DecodeHyperbolicAST(src)
		case CliffordMagic:
			return DecodeClifford(src)
		case GCTRMagic:
			return DecompressGCTR(src, dst)
		case IsoQuantMagic:
			return DecompressIsoQuant(src, dst)
		case HeisenbergMagic:
			return DecompressHeisenberg(src, dst)
		case CellularSheafMagic:
			return DecompressCellularSheaf(src, dst)
		case MobiusMagic:
			return DecompressMobiusTo(src, dst)
		case MargolusMagic:
			return DecompressMargolusTo(src, dst)
		case PadicMagic:
			return DecompressPadic(src, dst)
		case HodgeMagic:
			return DecompressHodge(src, dst)
		}

		// 3-byte versioned ISA formats (Zero-Schema VM, PWS, GGTC)
		switch {
		case src[0] == vmMagic0 && src[1] == vmMagic1 && src[2] == vmVer1:
			return VMDecode(src)
		case src[0] == pwsMagic0 && src[1] == pwsMagic1 && src[2] == pwsVer1:
			return PWSDecodeTo(dst, src)
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
	if hdr.NormMode != NormModeNone {
		contractedLen := 0
		if hdr.DataOffset+8 <= len(src) {
			contractedLen = int(binary.LittleEndian.Uint16(src[hdr.DataOffset+3:hdr.DataOffset+5])) |
				(int(src[hdr.DataOffset+6]) << 16) | (int(src[hdr.DataOffset+7]) << 24)
		}
		if contractedLen > 0 {
			allocSize = contractedLen
		}
	}

	// Safety padding (64 bytes) prevents vector load/store boundary faults
	// Aligned to 64-byte L1 cacheline boundary to eliminate split-cacheline penalties
	if dst == nil || cap(dst) < uSize+64 {
		dst = bufkit.AlignedBytes(uSize+64, bufkit.CacheLineSize)
	}
	dst = dst[:uSize]

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
	if sec1Offset < 0 || sec1Size < 0 || hdr.CmdStreamSize < 0 || hdr.OffsetStreamSize < 0 || sec2Offset > len(src) || sec3Offset > len(src) || sec4Offset > len(src) {
		return nil, fmt.Errorf("%w: corrupted section directory offsets (sec2=%d, sec3=%d, sec4=%d, len=%d)", ErrCorruptedFrame, sec2Offset, sec3Offset, sec4Offset, len(src))
	}

	var cmds []byte
	var offs []byte
	var lits []byte
	var pad *decompressScratchpad

	outBuf := dst
	if hdr.NormMode != NormModeNone {
		if pad == nil {
			pad = decScratchPool.Get()
			defer decScratchPool.Put(pad)
		}
		if cap(pad.normBuf) < allocSize+64 {
			pad.normBuf = bufkit.AlignedBytes(allocSize+64, bufkit.CacheLineSize)
		}
		outBuf = pad.normBuf[:allocSize]
	}
	baseDst := unsafe.Pointer(&outBuf[0])

	// Resolve Section 2 (Commands)
	if hdr.CmdStreamSize == 0 {
		cmds = nil
	} else {
		if sec2Offset+5 > len(src) {
			return nil, fmt.Errorf("%w: truncated section 2 header", ErrCorruptedFrame)
		}
		sec2Mode := src[sec2Offset]
		sec2OrigLen := int(binary.LittleEndian.Uint32(src[sec2Offset+1 : sec2Offset+5]))
		if sec2Mode == ModeFlat {
			if sec2Offset+5+sec2OrigLen > sec2Offset+hdr.CmdStreamSize || sec2Offset+5+sec2OrigLen > len(src) {
				return nil, fmt.Errorf("%w: truncated flat command stream", ErrCorruptedFrame)
			}
			cmds = src[sec2Offset+5 : sec2Offset+5+sec2OrigLen]
		} else if sec2Mode == ModeHuffman4X || sec2Mode == ModeHuffmanCompact || sec2Mode == ModeHuffmanCompact4X {
			if pad == nil {
				pad = decScratchPool.Get()
				defer decScratchPool.Put(pad)
			}
			if cap(pad.cmdBuf) < sec2OrigLen+64 {
				pad.cmdBuf = bufkit.AlignedBytes(sec2OrigLen+64, bufkit.CacheLineSize)
			}
			n, err := UnpackStream(pad.cmdBuf[:sec2OrigLen], src[sec2Offset:sec2Offset+hdr.CmdStreamSize], &pad.table)
			if err != nil {
				return nil, err
			}
			cmds = pad.cmdBuf[:n]
		} else {
			return nil, fmt.Errorf("%w: unknown command stream mode %d", ErrCorruptedFrame, sec2Mode)
		}
	}

	// Resolve Section 3 (Offsets)
	if hdr.OffsetStreamSize == 0 {
		offs = nil
	} else {
		if sec3Offset+5 > len(src) {
			return nil, fmt.Errorf("%w: truncated section 3 header", ErrCorruptedFrame)
		}
		sec3Mode := src[sec3Offset]
		sec3OrigLen := int(binary.LittleEndian.Uint32(src[sec3Offset+1 : sec3Offset+5]))
		if sec3Mode == ModeFlat {
			if sec3Offset+5+sec3OrigLen > sec3Offset+hdr.OffsetStreamSize || sec3Offset+5+sec3OrigLen > len(src) {
				return nil, fmt.Errorf("%w: truncated flat offset stream", ErrCorruptedFrame)
			}
			offs = src[sec3Offset+5 : sec3Offset+5+sec3OrigLen]
		} else if sec3Mode == ModeHuffman4X || sec3Mode == ModeHuffmanCompact || sec3Mode == ModeHuffmanCompact4X {
			if pad == nil {
				pad = decScratchPool.Get()
				defer decScratchPool.Put(pad)
			}
			if cap(pad.offBuf) < sec3OrigLen+64 {
				pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
			}
			n, err := UnpackStream(pad.offBuf[:sec3OrigLen], src[sec3Offset:sec3Offset+hdr.OffsetStreamSize], &pad.table)
			if err != nil {
				return nil, err
			}
			offs = pad.offBuf[:n]
		} else {
			return nil, fmt.Errorf("%w: unknown offset stream mode %d", ErrCorruptedFrame, sec3Mode)
		}
	}

	// Resolve Section 4 (Literals)
	if (hdr.Flags & FlagNibble) != 0 {
		if pad == nil {
			pad = decScratchPool.Get()
			defer decScratchPool.Put(pad)
		}
		if sec4Offset > len(src) {
			return nil, fmt.Errorf("%w: corrupted section 4 offset (%d > %d)", ErrCorruptedFrame, sec4Offset, len(src))
		}
		unpacked, err := unpackLiterals(src[sec4Offset:], pad)
		if err != nil {
			return nil, err
		}
		lits = unpacked
	} else {
		if sec4Offset > len(src) {
			return nil, fmt.Errorf("%w: corrupted section 4 offset (%d > %d)", ErrCorruptedFrame, sec4Offset, len(src))
		}
		lits = src[sec4Offset:]
	}

	cmdPtr := 0
	cmdEnd := len(cmds)
	offPtr := 0
	litPtr := 0

	// Initialize 3 CPU repeat offset registers
	stride := max(int(hdr.Stride), 1)
	rep0 := 1
	rep1 := 4
	rep2 := 8
	if (hdr.Flags&FlagStride) != 0 && hdr.Stride > 1 {
		rep1 = stride
		rep2 = 2 * stride
	}

	dstPos := 0

	minMatchBase := 4
	if (hdr.Flags & FlagMinMatch3) != 0 {
		minMatchBase = 3
	}

	var activeDict [][]byte
	rawDict := explicitRawDict
	if (hdr.Flags & FlagDictRef) != 0 {
		if len(rawDict) == 0 {
			rawDict = GetRawDictionary(hdr.DictID)
		}
		if customDict != nil && hdr.DictID == customDict.id {
			activeDict = customDict.tokens
		} else if d := GetDictionary(hdr.DictID); d != nil {
			activeDict = d.tokens
		} else if len(rawDict) == 0 {
			return nil, fmt.Errorf("%w: DictID 0x%08X is not registered", ErrUnknownDict, hdr.DictID)
		}
	}

	for dstPos < allocSize && cmdPtr < cmdEnd {
		op := cmds[cmdPtr]
		cmdPtr++

		if op < 0x80 {
			// Fast path 1: Fused Repeat Token (00 LLL MM KK)
			litLen := int((op >> 4) & 0x07)
			mLen := int((op>>2)&0x03) + minMatchBase
			k := int(op & 0x03)

			if litLen > 0 {
				if litPtr+8 <= len(lits) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else if litPtr+litLen <= len(lits) {
					copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				} else {
					return nil, fmt.Errorf("%w: literal stream truncated", ErrCorruptedFrame)
				}
				litPtr += litLen
				dstPos += litLen
			}

			var d int
			switch k {
			case 0:
				d = rep0
			case 1:
				d = rep1
				rep1 = rep0
				rep0 = d
			case 2:
				d = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = d
			default:
				return nil, fmt.Errorf("%w: invalid repeat index %d", ErrCorruptedFrame, k)
			}

			srcIdx := dstPos - d
			if d > 0 && srcIdx >= 0 && dstPos+mLen <= len(outBuf) {
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
					for i := range mLen {
						*(*byte)(unsafe.Add(baseDst, dstPos+i)) = *(*byte)(unsafe.Add(baseDst, srcIdx+i))
					}
				}
			} else {
				if err := execMatch(baseDst, outBuf, rawDict, dstPos, d, mLen); err != nil {
					return nil, err
				}
			}
			dstPos += mLen
			continue
		}

		if op < 0xC0 {
			// Fast path 2: Fused Extended Match (10 LL MMMM)
			litLen := int((op >> 4) & 0x03)
			baseLen := int(op & 0x0F)
			mLen := baseLen + minMatchBase
			if baseLen == 15 {
				var extra int
				var err error
				extra, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return nil, err
				}
				mLen = 15 + minMatchBase + extra
			}

			if litLen > 0 {
				if litPtr+litLen > len(lits) || dstPos+litLen > len(outBuf) {
					return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				if litPtr+8 <= len(lits) && dstPos+8 <= len(outBuf) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else {
					copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				}
				litPtr += litLen
				dstPos += litLen
			}

			var d int
			var err error
			d, offPtr, err = fastReadVarint(offs, offPtr)
			if err != nil {
				return nil, err
			}

			rep2 = rep1
			rep1 = rep0
			rep0 = d

			if err := execMatch(baseDst, outBuf, rawDict, dstPos, d, mLen); err != nil {
				return nil, err
			}
			dstPos += mLen
			continue
		}

		// Specialized commands (0xC0 .. 0xFF)
		switch {
		case op >= 0xC0 && op <= 0xCF:
			// Long Repeat Match (k 0..2) with Fused Literals
			k := int(op & 0x0F)
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			token := cmds[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			baseLen := int(token & 0x0F)
			mLen := baseLen + minMatchBase
			if baseLen == 15 {
				var extra int
				var err error
				extra, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return nil, err
				}
				mLen = 15 + minMatchBase + extra
			}

			if litLen > 0 {
				if litPtr+litLen > len(lits) || dstPos+litLen > len(outBuf) {
					return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				if litLen <= 8 && litPtr+8 <= len(lits) && dstPos+8 <= len(outBuf) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else if litLen <= 16 && litPtr+16 <= len(lits) && dstPos+16 <= len(outBuf) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
					*(*uint64)(unsafe.Add(baseDst, dstPos+8)) = *(*uint64)(unsafe.Pointer(&lits[litPtr+8]))
				} else {
					copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				}
				litPtr += litLen
				dstPos += litLen
			}

			var d int
			switch k {
			case 0:
				d = rep0
			case 1:
				d = rep1
				rep1 = rep0
				rep0 = d
			case 2:
				d = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = d
			default:
				return nil, fmt.Errorf("%w: invalid long repeat index %d", ErrCorruptedFrame, k)
			}

			if err := execMatch(baseDst, outBuf, rawDict, dstPos, d, mLen); err != nil {
				return nil, err
			}
			dstPos += mLen

		case op >= 0xD0 && op <= 0xD7:
			// Stride Match (m 1..8)
			m := int(op&0x07) + 1
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			token := cmds[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			mLen := int(token&0x0F) + minMatchBase

			if litLen > 0 {
				if litPtr+litLen > len(lits) || dstPos+litLen > len(outBuf) {
					return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				litPtr += litLen
				dstPos += litLen
			}

			d := m * stride
			rep2 = rep1
			rep1 = rep0
			rep0 = d

			if err := execMatch(baseDst, outBuf, rawDict, dstPos, d, mLen); err != nil {
				return nil, err
			}
			dstPos += mLen

		case op >= 0xE0 && op <= 0xE7:
			// Delta-Mask with Locality Index k
			k := int(op & 0x07)
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			baseLen := int(cmds[cmdPtr])
			cmdPtr++
			mLen := baseLen + minMatchBase
			if baseLen == 255 {
				var extra int
				var err error
				extra, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return nil, err
				}
				mLen = 255 + minMatchBase + extra
			}

			var d int
			switch k {
			case 0:
				d = rep0
			case 1:
				d = rep1
				rep1 = rep0
				rep0 = d
			case 2:
				d = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = d
			default:
				return nil, fmt.Errorf("%w: invalid delta locality index %d", ErrCorruptedFrame, k)
			}

			if err := execMatch(baseDst, outBuf, rawDict, dstPos, d, mLen); err != nil {
				return nil, err
			}

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
				for range cMods {
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
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			baseLen := int(cmds[cmdPtr])
			cmdPtr++
			mLen := baseLen + minMatchBase
			if baseLen == 255 {
				var extra int
				var err error
				extra, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return nil, err
				}
				mLen = 255 + minMatchBase + extra
			}

			var d int
			var err error
			d, offPtr, err = fastReadVarint(offs, offPtr)
			if err != nil {
				return nil, err
			}

			rep2 = rep1
			rep1 = rep0
			rep0 = d

			if err := execMatch(baseDst, outBuf, rawDict, dstPos, d, mLen); err != nil {
				return nil, err
			}

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
				for range cMods {
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

		case op == OpcodePrefixRawDict:
			// Direct Raw Dictionary Match (offset inside rawDict)
			if len(rawDict) == 0 {
				return nil, fmt.Errorf("%w: raw dict match opcode without raw dictionary", ErrCorruptedFrame)
			}
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated command stream", ErrCorruptedFrame)
			}
			token := cmds[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			baseLen := int(token & 0x0F)
			mLen := baseLen + minMatchBase
			if baseLen == 15 {
				var extra int
				var err error
				extra, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
				if err != nil {
					return nil, err
				}
				mLen = 15 + minMatchBase + extra
			}

			if litLen > 0 {
				if litPtr+litLen > len(lits) || dstPos+litLen > len(outBuf) {
					return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
				}
				if litLen <= 8 && litPtr+8 <= len(lits) && dstPos+8 <= len(outBuf) {
					*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
				} else {
					copy(outBuf[dstPos:dstPos+litLen], lits[litPtr:litPtr+litLen])
				}
				litPtr += litLen
				dstPos += litLen
			}

			var dictOff int
			var err error
			dictOff, offPtr, err = fastReadVarint(offs, offPtr)
			if err != nil {
				return nil, err
			}
			if dictOff < 0 || dictOff+mLen > len(rawDict) || dstPos+mLen > len(outBuf) {
				return nil, fmt.Errorf("%w: raw dict offset out of bounds (off=%d, mLen=%d, dictLen=%d)", ErrCorruptedFrame, dictOff, mLen, len(rawDict))
			}
			copy(outBuf[dstPos:dstPos+mLen], rawDict[dictOff:dictOff+mLen])
			dstPos += mLen

		case op == 0xF0:
			// Pure Literal Run
			var runLen int
			var err error
			runLen, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
			if err != nil {
				return nil, err
			}
			if litPtr+runLen > len(lits) || dstPos+runLen > len(outBuf) {
				return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
			}
			copy(outBuf[dstPos:dstPos+runLen], lits[litPtr:litPtr+runLen])
			litPtr += runLen
			dstPos += runLen

		case op == 0xF1:
			// RLE Run
			var rleLen int
			var err error
			rleLen, cmdPtr, err = fastReadVarint(cmds, cmdPtr)
			if err != nil {
				return nil, err
			}
			if litPtr >= len(lits) || dstPos+rleLen > len(outBuf) {
				return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
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
			if dstPos+len(tok) > len(outBuf) {
				return nil, fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
			}
			copy(outBuf[dstPos:], tok)
			dstPos += len(tok)

		case op == 0xF2:
			// Extended 2-byte schema token from active dictionary
			if len(activeDict) == 0 {
				return nil, fmt.Errorf("%w: dictionary token without active dictionary", ErrCorruptedFrame)
			}
			if cmdPtr >= cmdEnd {
				return nil, fmt.Errorf("%w: truncated dictionary token opcode", ErrCorruptedFrame)
			}
			tokIdx := int(cmds[cmdPtr])
			cmdPtr++
			if tokIdx >= len(activeDict) {
				return nil, fmt.Errorf("%w: invalid dictionary token ID %d", ErrCorruptedFrame, tokIdx)
			}
			tok := activeDict[tokIdx]
			if dstPos+len(tok) > len(outBuf) {
				return nil, fmt.Errorf("%w: dictionary token overflow", ErrCorruptedFrame)
			}
			copy(outBuf[dstPos:], tok)
			dstPos += len(tok)

		case op >= 0xF3 && op <= 0xFA:
			// Direct 1-byte Literal Run (1..8 literals)
			runLen := int(op - 0xF2)
			if litPtr+runLen > len(lits) || dstPos+runLen > len(outBuf) {
				return nil, fmt.Errorf("%w: literal stream truncated or overflow", ErrCorruptedFrame)
			}
			if litPtr+8 <= len(lits) && dstPos+8 <= len(outBuf) {
				*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&lits[litPtr]))
			} else {
				copy(outBuf[dstPos:dstPos+runLen], lits[litPtr:litPtr+runLen])
			}
			litPtr += runLen
			dstPos += runLen

		case op == OpCopyBlock:
			if cmdPtr+4 > cmdEnd {
				return nil, fmt.Errorf("%w: truncated OpCopyBlock payload", ErrCorruptedFrame)
			}
			srcBlockIdx := binary.LittleEndian.Uint32(cmds[cmdPtr : cmdPtr+4])
			cmdPtr += 4
			if srcBlockIdx >= uint32(MaxBlockSize/DedupBlockSize) {
				return nil, fmt.Errorf("%w: invalid dedup block index %d", ErrCorruptedFrame, srcBlockIdx)
			}
			ref := int(srcBlockIdx) * DedupBlockSize
			if ref < 0 || ref+DedupBlockSize > dstPos {
				return nil, fmt.Errorf("%w: forward or self-referential dedup block reference (ref=%d, dstPos=%d)", ErrCorruptedFrame, ref, dstPos)
			}
			if dstPos+DedupBlockSize > len(outBuf) {
				return nil, fmt.Errorf("%w: dedup block exceeds buffer capacity", ErrCorruptedFrame)
			}
			copy(outBuf[dstPos:dstPos+DedupBlockSize], outBuf[ref:ref+DedupBlockSize])
			dstPos += DedupBlockSize
		}
	}

	if dstPos < allocSize {
		return nil, fmt.Errorf("%w: incomplete decoding: got %d of %d bytes", ErrCorruptedFrame, dstPos, allocSize)
	}

	if hdr.NormMode != NormModeNone {
		switch hdr.NormMode {
		case NormModeHex, NormModeHexUpper:
			if uSize != allocSize*2 {
				return nil, fmt.Errorf("%w: hex expanded size mismatch (%d != %d)", ErrCorruptedFrame, uSize, allocSize*2)
			}
			ExpandHexTo(dst[:uSize], outBuf[:allocSize], hdr.NormMode == NormModeHexUpper)
		case NormModeBase64, NormModeBase64URL:
			if allocSize%3 != 0 || uSize != (allocSize/3)*4 {
				return nil, fmt.Errorf("%w: base64 expanded size mismatch (%d != %d)", ErrCorruptedFrame, uSize, (allocSize/3)*4)
			}
			ExpandBase64To(dst[:uSize], outBuf[:allocSize], 0, hdr.NormMode == NormModeBase64URL)
		case NormModeBase64Pad1, NormModeBase64URLP1:
			if allocSize%3 != 2 || uSize != ((allocSize+1)/3)*4 {
				return nil, fmt.Errorf("%w: base64 pad1 expanded size mismatch", ErrCorruptedFrame)
			}
			ExpandBase64To(dst[:uSize], outBuf[:allocSize], 1, hdr.NormMode == NormModeBase64URLP1)
		case NormModeBase64Pad2, NormModeBase64URLP2:
			if allocSize%3 != 1 || uSize != ((allocSize+2)/3)*4 {
				return nil, fmt.Errorf("%w: base64 pad2 expanded size mismatch", ErrCorruptedFrame)
			}
			ExpandBase64To(dst[:uSize], outBuf[:allocSize], 2, hdr.NormMode == NormModeBase64URLP2)
		case NormModeSpans:
			n, err := ExpandNormSpansTo(dst[:uSize], outBuf[:allocSize])
			if err != nil {
				return nil, err
			}
			if n != uSize {
				return nil, fmt.Errorf("%w: expanded size mismatch %d vs %d", ErrCorruptedNormStream, n, uSize)
			}
		default:
			return nil, fmt.Errorf("%w: unknown norm mode %d", ErrCorruptedFrame, hdr.NormMode)
		}
		return dst[:uSize], nil
	}

	return dst[:uSize], nil
}
