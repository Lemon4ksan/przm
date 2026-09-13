// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
)

var depStratPool = generic.NewPool(func() *[]byte {
	b := bufkit.AlignedBytes(MaxBlockSize*2+64, bufkit.CacheLineSize)
	return &b
})

// DependentSheafMagic is the 32-bit frame header identifier for Phase 17 ("DEPS").
const DependentSheafMagic uint32 = 0x44455053

var (
	// ErrCorruptedDependentSheaf indicates a malformed or truncated dependent-type sheaf frame.
	ErrCorruptedDependentSheaf = errors.New("przm: corrupted dependent sheaf frame")
)

// DetectProtobufTLV checks if a byte slice consists of serialized Tag-Length-Value (Protobuf/CBOR/MessagePack) fields.
// Returns true if at least 90% of the buffer cleanly parses as valid consecutive TLV fields with fields >= 3.
func DetectProtobufTLV(src []byte) bool {
	if len(src) < 16 {
		return false
	}

	ptr := 0
	fields := 0
	uSize := len(src)

	for ptr < uSize {
		tag, tn := binary.Uvarint(src[ptr:])
		if tn <= 0 {
			break
		}
		fieldNum := tag >> 3
		wireType := tag & 0x07

		// Protobuf field numbers must be > 0 and wire types must be in {0, 1, 2, 5}
		if fieldNum == 0 || wireType == 3 || wireType == 4 || wireType > 5 {
			break
		}
		ptr += tn

		switch wireType {
		case 0: // Varint
			_, vn := binary.Uvarint(src[ptr:])
			if vn <= 0 {
				return false
			}
			ptr += vn
		case 1: // Fixed64
			if ptr+8 > uSize {
				return false
			}
			ptr += 8
		case 2: // Length-delimited
			length, ln := binary.Uvarint(src[ptr:])
			if ln <= 0 || int(length) < 0 || ptr+ln+int(length) > uSize {
				return false
			}
			ptr += ln + int(length)
		case 5: // Fixed32
			if ptr+4 > uSize {
				return false
			}
			ptr += 4
		default:
			return false
		}
		fields++
	}

	// High confidence if >= 90% of the stream parsed into at least 3 valid fields
	return fields >= 3 && ptr >= int(float64(uSize)*0.90)
}

// StratifyDependentSheaf decomposes a serialized TLV stream into orthogonal fibers:
// [Header 36B | RemainderLen (4B) | TagStream | VarintStream | Fixed64Stream | LenDelimStream | Fixed32Stream | Remainder]
func StratifyDependentSheaf(src []byte) []byte {
	uSize := len(src)
	if uSize == 0 {
		return []byte{}
	}

	var tagBuf []byte
	var varintBuf []byte
	var fixed64Buf []byte
	var lenDelimBuf []byte
	var fixed32Buf []byte

	ptr := 0
	numFields := 0

	for ptr < uSize {
		tagStart := ptr
		tag, tn := binary.Uvarint(src[ptr:])
		if tn <= 0 {
			break
		}
		fieldNum := tag >> 3
		wireType := tag & 0x07
		if fieldNum == 0 || wireType == 3 || wireType == 4 || wireType > 5 {
			break
		}
		ptr += tn

		switch wireType {
		case 0: // Varint
			vStart := ptr
			_, vn := binary.Uvarint(src[ptr:])
			if vn <= 0 {
				ptr = tagStart
				goto parseDone
			}
			ptr += vn
			tagBuf = append(tagBuf, src[tagStart:tagStart+tn]...)
			varintBuf = append(varintBuf, src[vStart:ptr]...)

		case 1: // Fixed64
			if ptr+8 > uSize {
				ptr = tagStart
				goto parseDone
			}
			tagBuf = append(tagBuf, src[tagStart:tagStart+tn]...)
			fixed64Buf = append(fixed64Buf, src[ptr:ptr+8]...)
			ptr += 8

		case 2: // Length-delimited
			lStart := ptr
			length, ln := binary.Uvarint(src[ptr:])
			if ln <= 0 || int(length) < 0 || ptr+ln+int(length) > uSize {
				ptr = tagStart
				goto parseDone
			}
			ptr += ln
			tagBuf = append(tagBuf, src[tagStart:tagStart+tn]...)
			tagBuf = append(tagBuf, src[lStart:ptr]...) // include length varint in tag stream
			lenDelimBuf = append(lenDelimBuf, src[ptr:ptr+int(length)]...)
			ptr += int(length)

		case 5: // Fixed32
			if ptr+4 > uSize {
				ptr = tagStart
				goto parseDone
			}
			tagBuf = append(tagBuf, src[tagStart:tagStart+tn]...)
			fixed32Buf = append(fixed32Buf, src[ptr:ptr+4]...)
			ptr += 4
		}
		numFields++
	}

parseDone:
	remainder := src[ptr:]

	// Header layout:
	// 0..4: Magic (4B)
	// 4..8: OrigUSize (4B)
	// 8..12: NumFields (4B)
	// 12..16: TagSize (4B)
	// 16..20: VarintSize (4B)
	// 20..24: Fixed64Size (4B)
	// 24..28: LenDelimSize (4B)
	// 28..32: Fixed32Size (4B)
	// 32..36: RemainderSize (4B)
	totalSize := 36 + len(tagBuf) + len(varintBuf) + len(fixed64Buf) + len(lenDelimBuf) + len(fixed32Buf) + len(remainder)
	out := make([]byte, totalSize)

	binary.LittleEndian.PutUint32(out[0:4], DependentSheafMagic)
	binary.LittleEndian.PutUint32(out[4:8], uint32(uSize))
	binary.LittleEndian.PutUint32(out[8:12], uint32(numFields))
	binary.LittleEndian.PutUint32(out[12:16], uint32(len(tagBuf)))
	binary.LittleEndian.PutUint32(out[16:20], uint32(len(varintBuf)))
	binary.LittleEndian.PutUint32(out[20:24], uint32(len(fixed64Buf)))
	binary.LittleEndian.PutUint32(out[24:28], uint32(len(lenDelimBuf)))
	binary.LittleEndian.PutUint32(out[28:32], uint32(len(fixed32Buf)))
	binary.LittleEndian.PutUint32(out[32:36], uint32(len(remainder)))

	w := 36
	copy(out[w:], tagBuf)
	w += len(tagBuf)
	copy(out[w:], varintBuf)
	w += len(varintBuf)
	copy(out[w:], fixed64Buf)
	w += len(fixed64Buf)
	copy(out[w:], lenDelimBuf)
	w += len(lenDelimBuf)
	copy(out[w:], fixed32Buf)
	w += len(fixed32Buf)
	copy(out[w:], remainder)

	return out
}

// CollateDependentSheaf reconstructs the original serialized byte stream from stratified fibers.
// Guarantees strictly zero heap allocations when dst has sufficient capacity.
func CollateDependentSheaf(src []byte, dst []byte) ([]byte, error) {
	if len(src) < 36 {
		return nil, fmt.Errorf("%w: header truncated", ErrCorruptedDependentSheaf)
	}

	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != DependentSheafMagic {
		return nil, fmt.Errorf("%w: invalid magic 0x%08X", ErrCorruptedDependentSheaf, magic)
	}

	origUSize := int(binary.LittleEndian.Uint32(src[4:8]))
	numFields := int(binary.LittleEndian.Uint32(src[8:12]))
	tagSize := int(binary.LittleEndian.Uint32(src[12:16]))
	varintSize := int(binary.LittleEndian.Uint32(src[16:20]))
	fixed64Size := int(binary.LittleEndian.Uint32(src[20:24]))
	lenDelimSize := int(binary.LittleEndian.Uint32(src[24:28]))
	fixed32Size := int(binary.LittleEndian.Uint32(src[28:32]))
	remSize := int(binary.LittleEndian.Uint32(src[32:36]))

	totalPayload := 36 + tagSize + varintSize + fixed64Size + lenDelimSize + fixed32Size + remSize
	if len(src) < totalPayload {
		return nil, fmt.Errorf("%w: payload truncated (have %d, need %d)", ErrCorruptedDependentSheaf, len(src), totalPayload)
	}

	if cap(dst) < origUSize+64 {
		dst = bufkit.AlignedBytes(origUSize+64, bufkit.CacheLineSize)
	}
	dst = dst[:origUSize]

	pTag := 36
	pVar := pTag + tagSize
	pFix64 := pVar + varintSize
	pLen := pFix64 + fixed64Size
	pFix32 := pLen + lenDelimSize
	pRem := pFix32 + fixed32Size

	tagEnd := pTag + tagSize
	varEnd := pVar + varintSize
	fix64End := pFix64 + fixed64Size
	lenEnd := pLen + lenDelimSize
	fix32End := pFix32 + fixed32Size

	dstPos := 0
	baseDst := unsafe.Pointer(&dst[0])

	for f := 0; f < numFields && pTag < tagEnd; f++ {
		tagStart := pTag
		tag, tn := binary.Uvarint(src[pTag:tagEnd])
		if tn <= 0 {
			return nil, fmt.Errorf("%w: truncated tag in stream", ErrCorruptedDependentSheaf)
		}
		pTag += tn

		// Copy tag bytes to output
		copy(dst[dstPos:dstPos+tn], src[tagStart:pTag])
		dstPos += tn

		wireType := tag & 0x07

		switch wireType {
		case 0: // Varint
			vStart := pVar
			for pVar < varEnd && (src[pVar]&0x80) != 0 {
				pVar++
			}
			if pVar >= varEnd {
				return nil, fmt.Errorf("%w: truncated varint value", ErrCorruptedDependentSheaf)
			}
			pVar++ // include last byte (< 0x80)
			vLen := pVar - vStart
			copy(dst[dstPos:dstPos+vLen], src[vStart:pVar])
			dstPos += vLen

		case 1: // Fixed64
			if pFix64+8 > fix64End {
				return nil, fmt.Errorf("%w: truncated fixed64 value", ErrCorruptedDependentSheaf)
			}
			*(*uint64)(unsafe.Add(baseDst, dstPos)) = *(*uint64)(unsafe.Pointer(&src[pFix64]))
			pFix64 += 8
			dstPos += 8

		case 2: // Length-delimited
			lStart := pTag
			length, ln := binary.Uvarint(src[pTag:tagEnd])
			if ln <= 0 {
				return nil, fmt.Errorf("%w: truncated length varint", ErrCorruptedDependentSheaf)
			}
			pTag += ln

			// Copy length varint bytes to output
			copy(dst[dstPos:dstPos+ln], src[lStart:pTag])
			dstPos += ln

			iLen := int(length)
			if pLen+iLen > lenEnd {
				return nil, fmt.Errorf("%w: truncated length-delimited payload", ErrCorruptedDependentSheaf)
			}
			copy(dst[dstPos:dstPos+iLen], src[pLen:pLen+iLen])
			pLen += iLen
			dstPos += iLen

		case 5: // Fixed32
			if pFix32+4 > fix32End {
				return nil, fmt.Errorf("%w: truncated fixed32 value", ErrCorruptedDependentSheaf)
			}
			*(*uint32)(unsafe.Add(baseDst, dstPos)) = *(*uint32)(unsafe.Pointer(&src[pFix32]))
			pFix32 += 4
			dstPos += 4
		}
	}

	// Copy unparsed remainder
	if remSize > 0 {
		copy(dst[dstPos:], src[pRem:pRem+remSize])
		dstPos += remSize
	}

	if dstPos != origUSize {
		return nil, fmt.Errorf("%w: size mismatch: got %d, want %d", ErrCorruptedDependentSheaf, dstPos, origUSize)
	}

	return dst[:origUSize], nil
}

// CompressDependentSheaf decomposes serialized Protobuf/TLV data and cascades the stratified fibers
// into Phase 14 Étale Sheaf & Tropical Trellis.
func CompressDependentSheaf(src []byte) []byte {
	stratified := StratifyDependentSheaf(src)
	if len(stratified) == 0 {
		return nil
	}

	// Cascade through Phase 14 Étale Sheaf Trellis
	cascaded := CompressSheafTrellis(stratified)
	if len(cascaded) == 0 || len(cascaded) >= len(src) {
		return nil
	}

	// Prefix with DependentSheafMagic
	out := make([]byte, 4+len(cascaded))
	binary.LittleEndian.PutUint32(out[0:4], DependentSheafMagic)
	copy(out[4:], cascaded)
	return out
}

// DecompressDependentSheaf restores the stratified fibers and collates them back into the exact original bytes.
// Guarantees strictly zero heap allocations on decompression.
func DecompressDependentSheaf(src []byte, dst []byte) ([]byte, error) {
	if len(src) < 4 {
		return nil, fmt.Errorf("%w: truncated frame", ErrCorruptedDependentSheaf)
	}

	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != DependentSheafMagic {
		return nil, fmt.Errorf("%w: invalid magic 0x%08X", ErrCorruptedDependentSheaf, magic)
	}

	// 1. Decompress cascaded Étale Sheaf payload into pooled buffer
	pStrat := depStratPool.Get()
	defer depStratPool.Put(pStrat)

	stratified, err := DecompressSheafTrellis(src[4:], *pStrat)
	if err != nil {
		return nil, err
	}

	// 2. Collate dependent fibers back to original stream
	return CollateDependentSheaf(stratified, dst)
}
