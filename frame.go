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
	MagicPRZM      uint32 = 0x4D5A5250 // ASCII 'P', 'R', 'Z', 'M' (Little-Endian)
	MagicPRZS      uint32 = 0x535A5250 // ASCII 'P', 'R', 'Z', 'S' (Little-Endian)
	HeaderSize     int    = 8
	StreamHeaderSize int  = 17
	MaxBlockSize   int    = 16 * 1024 * 1024 // 16 MiB max frame size
	DedupBlockSize        = 65536           // 64 KiB block granularity for cross-block deduplication
)

// Stream configuration bitmasks.
const (
	StreamFlagIndependentChunks uint8 = 0x01 // Bit 0: Independent Chunks
)


// Frame configuration bitmasks (PRZM-SPEC-0.5 Section 3.2).
const (
	FlagStride       uint8 = 0x01 // Bit 0: Stride parameter present
	FlagDeltaMask    uint8 = 0x02 // Bit 1: Delta residual mask channel present
	FlagUncompressed uint8 = 0x04 // Bit 2: Raw uncompressed payload
	FlagExtOffset    uint8 = 0x08 // Bit 3: 24/32-bit offsets and section lengths
	FlagDictRef      uint8 = 0x10 // Bit 4: External dictionary reference present
	FlagNorm         uint8 = 0x20 // Bit 5: Base64/Hex Normalization applied
	FlagNibble       uint8 = 0x80 // Bit 7: Section 4 encoded via SIMD Nibble-Plane Splitting or V-ANS
)

// Well-known dictionary identifiers (PRZM-SPEC-0.5 FlagDictRef).
const (
	DictIDNone        uint32 = 0
	DictIDJSON        uint32 = 1      // Structured web service JSON logs & APIs
	DictIDCode        uint32 = 2      // Deprecated: legacy source code dictionary
	DictIDReservedMax uint32 = 0x0FFF // Reserved for universal open standards
	DictIDExternalMin uint32 = 0x1000 // Minimum ID for domain-specific / external dictionaries
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
	OpCopyBlock            uint8 = 0xFD // 0xFD: Aligned 64 KiB block replication (Milestone 5)
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
	ErrUnknownDict       = errors.New("przm: unknown or unregistered dictionary")
	ErrInvalidDict       = errors.New("przm: invalid dictionary format")
)

// WindowLog levels (PRZM-SPEC-0.5 Milestone 2 Window Scaling)
const (
	WindowLog64K = 16 // 64 KiB window (legacy default)
	WindowLog1M  = 20 // 1 MiB window (L3 cache resident)
	WindowLog8M  = 23 // 8 MiB window (full coverage)
)

// Compression Modes / Levels
const (
	ModeArchival = 5
)

// Specialized Container Signatures
const (
	ArchivalPNGMagic uint32 = 0x504E4741 // 'P','N','G','A' (Archival PNG container)
)


// FrameHeader holds parsed frame metadata.
type FrameHeader struct {
	Flags            uint8
	UncompressedSize int
	Stride           int    // Effective stride periodicity S (1..256)
	DictID           uint32 // Optional dictionary identifier
	CmdStreamSize    int    // Section 2 size in bytes
	OffsetStreamSize int    // Section 3 size in bytes
	DataOffset       int    // Byte offset in frame where Section 1 starts
	WindowLog        uint8  // Effective window size log2 (16 for 64K, 20 for 1M, 23 for 8M)
	NormMode         uint8  // Normalization mode from Section 1 byte 2 (0=None, 1=Hex, 2=Base64, 3=Spans, ...)
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

	// Parse WindowLog and NormMode from Section 1 if present
	if (flags&FlagUncompressed) == 0 && offset+8 <= len(src) {
		hdr.NormMode = src[offset+2]
		wlog := src[offset+1]
		if wlog >= 16 && wlog <= 24 {
			hdr.WindowLog = wlog
		} else {
			// Legacy frame fallback: infer window from uncompressedSize and FlagExtOffset
			if (flags & FlagExtOffset) != 0 {
				if uncompressedSize > 1024*1024 {
					hdr.WindowLog = WindowLog8M // 23
				} else {
					hdr.WindowLog = WindowLog1M // 20
				}
			} else {
				hdr.WindowLog = WindowLog64K // 16
			}
		}
	} else {
		hdr.WindowLog = WindowLog64K // 16
	}

	return hdr, nil
}

// StreamHeader holds parsed PRZS multi-chunk stream metadata.
type StreamHeader struct {
	Flags                 uint8
	TotalUncompressedSize uint64
	ChunkCount            uint32
}

// ParseStreamHeader parses the 17-byte PRZS stream container header.
func ParseStreamHeader(src []byte) (StreamHeader, error) {
	if len(src) < StreamHeaderSize {
		return StreamHeader{}, fmt.Errorf("%w: stream header smaller than %d bytes (%d bytes)", ErrCorruptedFrame, StreamHeaderSize, len(src))
	}

	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != MagicPRZS {
		return StreamHeader{}, fmt.Errorf("%w: expected 0x%08X (PRZS), got 0x%08X", ErrInvalidMagic, MagicPRZS, magic)
	}

	flags := src[4]
	totalUSize := binary.LittleEndian.Uint64(src[5:13])
	chunkCount := binary.LittleEndian.Uint32(src[13:17])

	return StreamHeader{
		Flags:                 flags,
		TotalUncompressedSize: totalUSize,
		ChunkCount:            chunkCount,
	}, nil
}

// BuildStreamContainer packages multiple compressed chunks into a PRZS stream container.
func BuildStreamContainer(chunks [][]byte, totalUSize uint64) []byte {
	totalSize := StreamHeaderSize
	for _, chunk := range chunks {
		totalSize += 4 + len(chunk)
	}

	buf := make([]byte, totalSize)
	binary.LittleEndian.PutUint32(buf[0:4], MagicPRZS)
	buf[4] = StreamFlagIndependentChunks
	binary.LittleEndian.PutUint64(buf[5:13], totalUSize)
	binary.LittleEndian.PutUint32(buf[13:17], uint32(len(chunks)))

	offset := StreamHeaderSize
	for _, chunk := range chunks {
		binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(chunk)))
		copy(buf[offset+4:offset+4+len(chunk)], chunk)
		offset += 4 + len(chunk)
	}

	return buf
}

