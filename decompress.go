// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
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
	vansModels  [4]VANSModel
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
	case MobiusMagic:
		if len(src) < MobiusHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case MargolusMagic:
		if len(src) < MargolusHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case HeisenbergMagic:
		if len(src) < HeisenbergHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case CellularSheafMagic:
		if len(src) < CellularSheafHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case HodgeMagic:
		if len(src) < HodgeHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	case IsoQuantMagic:
		if len(src) < IsoQuantHeaderSize {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[8:12])), nil
	case SymplecticMagic, SheafMagic, MahlerMagic, PadicMagic:
		if len(src) < 12 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[8:12])), nil
	case GCTRMagic:
		if len(src) < 10 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[6:10])), nil
	case HMDSMagic, SpectralMagic:
		if len(src) < 9 {
			return 0, ErrCorruptedFrame
		}
		return int(binary.LittleEndian.Uint32(src[5:9])), nil
	default:
		// Versioned 3-byte ISA headers
		if len(src) >= 8 && (src[0] == pwsMagic0 && src[1] == pwsMagic1 && src[2] == pwsVer1) {
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

		// 32-bit magic formats (Symplectic, Mahler, Sheaf, GCTR, IsoQuant, Heisenberg, CellularSheaf, Mobius, Margolus, Padic, Hodge, HMDS, Spectral)
		switch magic {
		case HMDSMagic:
			return DecompressHMDS(src, dst)
		case SpectralMagic:
			return DecompressSpectralUnitary(src, dst)
		case SymplecticMagic:
			return DecompressSymplectic(src, dst)
		case MahlerMagic:
			return DecompressMahler(src, dst)
		case SheafMagic:
			return DecompressSheaf(src, dst)
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

		// 3-byte versioned ISA formats (PWS)
		if src[0] == pwsMagic0 && src[1] == pwsMagic1 && src[2] == pwsVer1 {
			return PWSDecodeTo(dst, src)
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
			if contractedLen > MaxBlockSize {
				return nil, fmt.Errorf(
					"%w: contracted length %d exceeds MaxBlockSize",
					ErrExceedsMaxBlock,
					contractedLen,
				)
			}
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
			return nil, fmt.Errorf(
				"%w: truncated uncompressed payload (%d < %d)",
				ErrCorruptedFrame,
				len(src)-HeaderSize,
				uSize,
			)
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
	if sec1Offset < 0 || sec1Size < 0 || hdr.CmdStreamSize < 0 || hdr.OffsetStreamSize < 0 || sec2Offset > len(src) ||
		sec3Offset > len(src) ||
		sec4Offset > len(src) {
		return nil, fmt.Errorf(
			"%w: corrupted section directory offsets (sec2=%d, sec3=%d, sec4=%d, len=%d)",
			ErrCorruptedFrame,
			sec2Offset,
			sec3Offset,
			sec4Offset,
			len(src),
		)
	}

	var cmds []byte
	var offs []byte
	var lits []byte
	var pad *decompressScratchpad

	outBuf := dst
	if hdr.NormMode != NormModeNone {
		pad = decScratchPool.Get()
		defer decScratchPool.Put(pad)
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
		if sec2Offset >= len(src) {
			return nil, fmt.Errorf("%w: truncated section 2 header", ErrCorruptedFrame)
		}
		sec2Mode := src[sec2Offset]
		if sec2Mode == ModeFlatCompact {
			if sec2Offset+hdr.CmdStreamSize > len(src) {
				return nil, fmt.Errorf("%w: truncated flat command stream", ErrCorruptedFrame)
			}
			cmds = src[sec2Offset+1 : sec2Offset+hdr.CmdStreamSize]
		} else {
			if sec2Offset+5 > len(src) {
				return nil, fmt.Errorf("%w: truncated section 2 header", ErrCorruptedFrame)
			}
			sec2OrigLen := int(binary.LittleEndian.Uint32(src[sec2Offset+1 : sec2Offset+5]))
			switch sec2Mode {
			case ModeFlat:
				if sec2Offset+5+sec2OrigLen > sec2Offset+hdr.CmdStreamSize || sec2Offset+5+sec2OrigLen > len(src) {
					return nil, fmt.Errorf("%w: truncated flat command stream", ErrCorruptedFrame)
				}
				cmds = src[sec2Offset+5 : sec2Offset+5+sec2OrigLen]
			case ModeHuffman4X,
				ModeHuffmanCompact,
				ModeHuffmanCompact4X,
				ModeRecursiveLZ,
				ModeVANS4X,
				ModeSplitVarint,
				ModeCommandEntropy:
				if pad == nil {
					pad = decScratchPool.Get()
					defer decScratchPool.Put(pad)
				}
				if cap(pad.cmdBuf) < sec2OrigLen+64 {
					pad.cmdBuf = bufkit.AlignedBytes(sec2OrigLen+64, bufkit.CacheLineSize)
				}
				n, err := UnpackStream(
					pad.cmdBuf[:sec2OrigLen],
					src[sec2Offset:sec2Offset+hdr.CmdStreamSize],
					&pad.table,
				)
				if err != nil {
					return nil, err
				}
				cmds = pad.cmdBuf[:n]
			default:
				return nil, fmt.Errorf("%w: unknown command stream mode %d", ErrCorruptedFrame, sec2Mode)
			}
		}
	}

	// Resolve Section 3 (Offsets)
	if hdr.OffsetStreamSize == 0 {
		offs = nil
	} else {
		if sec3Offset >= len(src) {
			return nil, fmt.Errorf("%w: truncated section 3 header", ErrCorruptedFrame)
		}
		sec3Mode := src[sec3Offset]
		if sec3Mode == ModeFlatCompact {
			if sec3Offset+hdr.OffsetStreamSize > len(src) {
				return nil, fmt.Errorf("%w: truncated flat offset stream", ErrCorruptedFrame)
			}
			offs = src[sec3Offset+1 : sec3Offset+hdr.OffsetStreamSize]
		} else if sec3Mode == ModeDistancePrefixByte {
			if sec3Offset+4 > len(src) {
				return nil, fmt.Errorf("%w: truncated section 3 header", ErrCorruptedFrame)
			}
			sec3OrigLen := int(src[sec3Offset+1])
			if pad == nil {
				pad = decScratchPool.Get()
				defer decScratchPool.Put(pad)
			}
			if cap(pad.offBuf) < sec3OrigLen+64 {
				pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
			}
			n, err := UnpackStream(
				pad.offBuf[:sec3OrigLen],
				src[sec3Offset:sec3Offset+hdr.OffsetStreamSize],
				&pad.table,
			)
			if err != nil {
				return nil, err
			}
			offs = pad.offBuf[:n]
		} else if sec3Mode == ModeDistancePrefixCompact {
			if sec3Offset+7 > len(src) {
				return nil, fmt.Errorf("%w: truncated section 3 header", ErrCorruptedFrame)
			}
			sec3OrigLen := int(binary.LittleEndian.Uint16(src[sec3Offset+1 : sec3Offset+3]))
			if pad == nil {
				pad = decScratchPool.Get()
				defer decScratchPool.Put(pad)
			}
			if cap(pad.offBuf) < sec3OrigLen+64 {
				pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
			}
			n, err := UnpackStream(
				pad.offBuf[:sec3OrigLen],
				src[sec3Offset:sec3Offset+hdr.OffsetStreamSize],
				&pad.table,
			)
			if err != nil {
				return nil, err
			}
			offs = pad.offBuf[:n]
		} else {
			if sec3Offset+5 > len(src) {
				return nil, fmt.Errorf("%w: truncated section 3 header", ErrCorruptedFrame)
			}
			sec3OrigLen := int(binary.LittleEndian.Uint32(src[sec3Offset+1 : sec3Offset+5]))
			if sec3Mode == ModeFlat {
				if sec3Offset+5+sec3OrigLen > sec3Offset+hdr.OffsetStreamSize || sec3Offset+5+sec3OrigLen > len(src) {
					return nil, fmt.Errorf("%w: truncated flat offset stream", ErrCorruptedFrame)
				}
				offs = src[sec3Offset+5 : sec3Offset+5+sec3OrigLen]
			} else if sec3Mode == ModeHuffman4X || sec3Mode == ModeHuffmanCompact || sec3Mode == ModeHuffmanCompact4X || sec3Mode == ModeRecursiveLZ || sec3Mode == ModeVANS4X || sec3Mode == ModeSplitVarint {
				if pad == nil {
					pad = decScratchPool.Get()
					defer decScratchPool.Put(pad)
				}
				if cap(pad.offBuf) < sec3OrigLen+64 {
					pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
				}
				n, err := UnpackStream(
					pad.offBuf[:sec3OrigLen],
					src[sec3Offset:sec3Offset+hdr.OffsetStreamSize],
					&pad.table,
				)
				if err != nil {
					return nil, err
				}
				offs = pad.offBuf[:n]
			} else if sec3Mode == ModeDistancePrefix {
				if pad == nil {
					pad = decScratchPool.Get()
					defer decScratchPool.Put(pad)
				}
				if cap(pad.offBuf) < sec3OrigLen+64 {
					pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
				}
				n, err := DecodeDistancePrefix(
					pad.offBuf[:sec3OrigLen],
					src[sec3Offset:sec3Offset+hdr.OffsetStreamSize],
					&pad.table,
					pad.scanlineBuf,
				)
				if err != nil {
					return nil, err
				}
				offs = pad.offBuf[:n]
			} else {
				return nil, fmt.Errorf("%w: unknown offset stream mode %d", ErrCorruptedFrame, sec3Mode)
			}
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

	// Initialize 4 CPU repeat offset registers
	stride := max(int(hdr.Stride), 1)
	rep0 := 1
	rep1 := 4
	rep2 := 8
	rep3 := 16
	if (hdr.Flags&FlagStride) != 0 && hdr.Stride > 1 {
		rep1 = stride
		rep2 = 2 * stride
		rep3 = 3 * stride
	}

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

	dec := frameDecoder{
		baseDst:      baseDst,
		outBuf:       outBuf,
		allocSize:    allocSize,
		dstPos:       0,
		cmds:         cmds,
		cmdPtr:       0,
		cmdEnd:       len(cmds),
		lits:         lits,
		litPtr:       0,
		offs:         offs,
		offPtr:       0,
		rep0:         rep0,
		rep1:         rep1,
		rep2:         rep2,
		rep3:         rep3,
		stride:       stride,
		minMatchBase: minMatchBase,
		rawDict:      rawDict,
		activeDict:   activeDict,
	}

	if err := dec.decode(); err != nil {
		return nil, err
	}

	if hdr.NormMode != NormModeNone {
		switch hdr.NormMode {
		case NormModeHex, NormModeHexUpper:
			if uSize != allocSize*2 {
				return nil, fmt.Errorf(
					"%w: hex expanded size mismatch (%d != %d)",
					ErrCorruptedFrame,
					uSize,
					allocSize*2,
				)
			}
			ExpandHexTo(dst[:uSize], outBuf[:allocSize], hdr.NormMode == NormModeHexUpper)
		case NormModeBase64, NormModeBase64URL:
			if allocSize%3 != 0 || uSize != (allocSize/3)*4 {
				return nil, fmt.Errorf(
					"%w: base64 expanded size mismatch (%d != %d)",
					ErrCorruptedFrame,
					uSize,
					(allocSize/3)*4,
				)
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
