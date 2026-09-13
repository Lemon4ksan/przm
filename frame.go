// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package przm implements the PRZM (PrismLZ) structured dictionary compression format (PRZM-SPEC-0.5).
package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Frame constants and magic signatures.
const (
	MagicPRZM    uint32 = 0x4D5A5250 // ASCII 'P', 'R', 'Z', 'M' (Little-Endian)
	HeaderSize   int    = 8
	MaxBlockSize int    = 16 * 1024 * 1024 // 16 MiB max frame size
)

// Frame configuration bitmasks (PRZM-SPEC-0.5 Section 3.2).
const (
	FlagStride       uint8 = 0x01 // Bit 0: Stride parameter present
	FlagDeltaMask    uint8 = 0x02 // Bit 1: Delta residual mask channel present
	FlagUncompressed uint8 = 0x04 // Bit 2: Raw uncompressed payload
	FlagExtOffset    uint8 = 0x08 // Bit 3: 24/32-bit offsets and section lengths
	FlagDictRef      uint8 = 0x10 // Bit 4: External dictionary reference present
	FlagBCJSplit     uint8 = 0x20 // Bit 5: x86 BCJ displacement splitting applied
	FlagBCJ          uint8 = 0x40 // Bit 6: x86_64 BCJ relative address filter applied
	FlagNibble       uint8 = 0x80 // Bit 7: Section 4 encoded via SIMD Nibble-Plane Splitting or V-ANS
)

// Well-known dictionary identifiers (PRZM-SPEC-0.5 FlagDictRef).
const (
	DictIDNone uint32 = 0
	DictIDJSON uint32 = 1 // Structured web service JSON logs & APIs
	DictIDCode uint32 = 2 // Source code (Go, C, JS, Rust)
)

// Opcode categories (PRZM-SPEC-0.5 Section 3).
const (
	OpcodeCatRepeat   uint8 = 0x00 // 00: Repeat Match (Locality Queue)
	OpcodeCatStride   uint8 = 0x40 // 01: Stride Match (m * S)
	OpcodeCatExtended uint8 = 0x80 // 10: Extended Match (Long / Delta-Masked / RLE)
	OpcodeCatLiteral  uint8 = 0xC0 // 11: Literal Run
	OpcodeCatMask     uint8 = 0xC0
)

// Fused Opcode Constants (PRZM-SPEC-0.5 Fused Engine)
const (
	OpcodeMaskFusedRepeat  uint8 = 0x80
	OpcodeFlagFusedRepeat  uint8 = 0x00
	OpcodeMaskFusedExt     uint8 = 0xC0
	OpcodeFlagFusedExt     uint8 = 0x80
	OpcodePrefixLongRepeat uint8 = 0xC0 // 0xC0 .. 0xCF: Locality queue indices 0..15
	OpcodePrefixStride     uint8 = 0xD0 // 0xD0 .. 0xD7
	OpcodePrefixShortDict  uint8 = 0xD8 // 0xD8 .. 0xDF: Top-8 direct 1-byte schema tokens
	OpcodePrefixDeltaRing  uint8 = 0xE0 // 0xE0 .. 0xE7
	OpcodePrefixDeltaExpl  uint8 = 0xE8
	OpcodePrefixLiteral    uint8 = 0xF0
	OpcodePrefixRLE        uint8 = 0xF1
	OpcodePrefixDictToken  uint8 = 0xF2 // 0xF2 <tokenID>: 2-byte schema token from active DictID
	OpcodePrefixLitShort   uint8 = 0xF3 // 0xF3 .. 0xFA: 1-byte direct literal run for 1..8 literals
)

// Extended match opcode flags (Category 10).
const (
	ExtFlagDeltaMask uint8 = 0x20 // Bit 5: Has delta mask
	ExtFlagExplicit  uint8 = 0x10 // Bit 4: 0 = Locality index, 1 = Explicit offset from Section 3
	ExtLengthMask    uint8 = 0x0F // Bits 0..3: Base length
	ExtLengthEscape  uint8 = 0x0F // Value 15 indicates variable-length extension
)

var (
	ErrInvalidMagic      = errors.New("przm: invalid magic signature")
	ErrCorruptedFrame    = errors.New("przm: corrupted frame structure")
	ErrOffsetOutOfBounds = errors.New("przm: match offset out of decompressed bounds")
	ErrBufferTooSmall    = errors.New("przm: output buffer too small")
	ErrExceedsMaxBlock   = errors.New("przm: uncompressed size exceeds 16 MiB limit")
)

// FrameHeader holds parsed frame metadata.
type FrameHeader struct {
	Flags            uint8
	UncompressedSize int
	Stride           int    // Effective stride periodicity S (1..256)
	DictID           uint32 // Optional dictionary identifier
	CallCount        uint32 // Optional call count for FlagBCJSplit
	CmdStreamSize    int    // Section 2 size in bytes
	OffsetStreamSize int    // Section 3 size in bytes
	DataOffset       int    // Byte offset in frame where Section 1 starts
}

// ParseFrameHeader parses the 8-byte frame header, frame parameters, and section directory.
func ParseFrameHeader(src []byte) (FrameHeader, error) {
	if len(src) < HeaderSize {
		return FrameHeader{}, fmt.Errorf("%w: frame smaller than 8-byte header (%d bytes)", ErrCorruptedFrame, len(src))
	}

	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != MagicPRZM {
		return FrameHeader{}, fmt.Errorf("%w: expected 0x%08X, got 0x%08X", ErrInvalidMagic, MagicPRZM, magic)
	}

	flags := src[4]
	uncompressedSize := int(uint32(src[5]) | (uint32(src[6]) << 8) | (uint32(src[7]) << 16))
	if uncompressedSize > MaxBlockSize {
		return FrameHeader{}, fmt.Errorf("%w: %d bytes", ErrExceedsMaxBlock, uncompressedSize)
	}

	hdr := FrameHeader{
		Flags:            flags,
		UncompressedSize: uncompressedSize,
		Stride:           1, // Default stride
	}

	// Uncompressed raw data (or empty frame) follows immediately at offset 8
	if uncompressedSize == 0 || (flags&FlagUncompressed) != 0 {
		hdr.DataOffset = HeaderSize
		return hdr, nil
	}

	offset := HeaderSize

	// Parse Frame Parameters
	if (flags & FlagStride) != 0 {
		if offset >= len(src) {
			return FrameHeader{}, fmt.Errorf("%w: missing stride parameter", ErrCorruptedFrame)
		}
		hdr.Stride = int(src[offset]) + 1 // Stride value 0..255 encodes 1..256
		offset++
	}

	if (flags & FlagDictRef) != 0 {
		if offset+4 > len(src) {
			return FrameHeader{}, fmt.Errorf("%w: missing dictionary identifier", ErrCorruptedFrame)
		}
		hdr.DictID = binary.LittleEndian.Uint32(src[offset : offset+4])
		offset += 4
	}

	if (flags & FlagBCJSplit) != 0 {
		if offset+4 > len(src) {
			return FrameHeader{}, fmt.Errorf("%w: missing call count parameter", ErrCorruptedFrame)
		}
		hdr.CallCount = binary.LittleEndian.Uint32(src[offset : offset+4])
		offset += 4
	}

	// Parse Section Directory
	extOffset := (flags & FlagExtOffset) != 0
	if !extOffset {
		if offset+4 > len(src) {
			return FrameHeader{}, fmt.Errorf("%w: truncated section directory", ErrCorruptedFrame)
		}
		hdr.CmdStreamSize = int(binary.LittleEndian.Uint16(src[offset : offset+2]))
		hdr.OffsetStreamSize = int(binary.LittleEndian.Uint16(src[offset+2 : offset+4]))
		offset += 4
	} else {
		if offset+6 > len(src) {
			return FrameHeader{}, fmt.Errorf("%w: truncated extended section directory", ErrCorruptedFrame)
		}
		hdr.CmdStreamSize = int(uint32(src[offset]) | (uint32(src[offset+1]) << 8) | (uint32(src[offset+2]) << 16))
		hdr.OffsetStreamSize = int(uint32(src[offset+3]) | (uint32(src[offset+4]) << 8) | (uint32(src[offset+5]) << 16))
		offset += 6
	}

	hdr.DataOffset = offset
	return hdr, nil
}
