// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
)

// Normalization mode constants for Section 1 byte 2.
const (
	NormModeNone        uint8 = 0
	NormModeHex         uint8 = 1 // Whole-block lowercase hex [0-9a-f]
	NormModeBase64      uint8 = 2 // Whole-block standard Base64 (no padding)
	NormModeSpans       uint8 = 3 // Mixed span-based normalization
	NormModeHexUpper    uint8 = 4 // Whole-block uppercase hex [0-9A-F]
	NormModeBase64Pad1  uint8 = 5 // Whole-block standard Base64 with 1 '='
	NormModeBase64Pad2  uint8 = 6 // Whole-block standard Base64 with 2 '=='
	NormModeBase64URL   uint8 = 7 // Whole-block URL-safe Base64 (no padding)
	NormModeBase64URLP1 uint8 = 8 // Whole-block URL-safe Base64 with 1 '='
	NormModeBase64URLP2 uint8 = 9 // Whole-block URL-safe Base64 with 2 '=='
)

// Span type identifiers for span table records.
const (
	NormSpanHexLower      uint8 = 1 // Even-length lowercase hex [0-9a-f] (50% contraction)
	NormSpanHexUpper      uint8 = 2 // Even-length uppercase hex [0-9A-F] (50% contraction)
	NormSpanBase64Std     uint8 = 3 // Standard Base64 [A-Za-z0-9+/], 0 '=' (25% contraction)
	NormSpanBase64Pad1    uint8 = 4 // Standard Base64, ending with 1 '='
	NormSpanBase64Pad2    uint8 = 5 // Standard Base64, ending with 2 '=='
	NormSpanBase64URL     uint8 = 6 // URL-safe Base64 [A-Za-z0-9-_], 0 '='
	NormSpanBase64URLPad1 uint8 = 7 // URL-safe Base64, ending with 1 '='
	NormSpanBase64URLPad2 uint8 = 8 // URL-safe Base64, ending with 2 '=='
)

const (
	minNormSpanLen = 64
	minNetSavings  = 16
)

var (
	ErrCorruptedNormStream = errors.New("przm: corrupted normalized stream")
)

// NormSpan describes a single contracted Base64 or Hex span.
type NormSpan struct {
	Type          uint8
	OrigOffset    int
	OrigLen       int
	ContractedLen int
}

// NormPlan holds the detected normalization layout for a block.
type NormPlan struct {
	Spans          []NormSpan
	NetSavings     int
	ContractedSize int
}

const (
	hexLutLower = "0123456789abcdef"
	hexLutUpper = "0123456789ABCDEF"
	b64LutStd   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	b64LutURL   = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
)

var b64RevStd [256]int8
var b64RevURL [256]int8

func init() {
	for i := range b64RevStd {
		b64RevStd[i] = -1
		b64RevURL[i] = -1
	}
	for i := range len(b64LutStd) {
		b64RevStd[b64LutStd[i]] = int8(i)
	}
	for i := range len(b64LutURL) {
		b64RevURL[b64LutURL[i]] = int8(i)
	}
}

// IsWholeBlockHex checks if src consists entirely of even-length hex characters (>= 64B).
func IsWholeBlockHex(src []byte) (isHex bool, upper bool) {
	n := len(src)
	if n < minNormSpanLen || (n%2) != 0 {
		return false, false
	}
	hasLower := false
	hasUpper := false
	for i := range n {
		b := src[i]
		if b >= '0' && b <= '9' {
			continue
		}
		if b >= 'a' && b <= 'f' {
			hasLower = true
			continue
		}
		if b >= 'A' && b <= 'F' {
			hasUpper = true
			continue
		}
		return false, false
	}
	if hasLower && hasUpper {
		return false, false // Mixed case hex left uncontracted or handled by Base64
	}
	return true, hasUpper
}

// IsWholeBlockBase64 checks if src consists entirely of Base64 characters (>= 64B, multiple of 4).
func IsWholeBlockBase64(src []byte) (isB64 bool, padCount int, urlSafe bool) {
	n := len(src)
	if n < minNormSpanLen || (n%4) != 0 {
		return false, 0, false
	}

	pads := 0
	if src[n-1] == '=' {
		pads = 1
		if src[n-2] == '=' {
			pads = 2
		}
	}

	bodyLen := n - pads
	hasPlusSlash := false
	hasDashUnderscore := false

	for i := range bodyLen {
		b := src[i]
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') {
			continue
		}
		if b == '+' || b == '/' {
			hasPlusSlash = true
			continue
		}
		if b == '-' || b == '_' {
			hasDashUnderscore = true
			continue
		}
		return false, 0, false
	}

	if hasPlusSlash && hasDashUnderscore {
		return false, 0, false // Mixed alphabet
	}

	isURL := hasDashUnderscore
	if pads > 0 {
		lut := &b64RevStd
		if isURL {
			lut = &b64RevURL
		}
		if pads == 1 {
			v2 := lut[src[n-2]]
			if v2 < 0 || (v2&0x03) != 0 {
				return false, 0, false
			}
		} else if pads == 2 {
			if n >= 3 && src[n-3] == '=' {
				return false, 0, false
			}
			v1 := lut[src[n-3]]
			if v1 < 0 || (v1&0x0F) != 0 {
				return false, 0, false
			}
		}
	}
	return true, pads, isURL
}

func hexNibble(c byte) (byte, bool) {
	if c >= '0' && c <= '9' {
		return c - '0', true
	}
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 10, true
	}
	if c >= 'A' && c <= 'F' {
		return c - 'A' + 10, true
	}
	return 0, false
}

// ContractHex contracts 2 ASCII hex characters into 1 raw byte.
func ContractHex(src []byte, dst []byte) {
	n := len(src) / 2
	for i := range n {
		hi, _ := hexNibble(src[2*i])
		lo, _ := hexNibble(src[2*i+1])
		dst[i] = (hi << 4) | lo
	}
}

// ExpandHexTo expands raw binary bytes into lowercase or uppercase ASCII hex.
func ExpandHexTo(dst []byte, src []byte, upper bool) {
	lut := hexLutLower
	if upper {
		lut = hexLutUpper
	}
	n := len(src)
	if len(dst) < n*2 {
		n = len(dst) / 2
	}
	for i := 0; i < n; i++ {
		b := src[i]
		dst[2*i] = lut[b>>4]
		dst[2*i+1] = lut[b&0x0F]
	}
}

// ContractBase64 contracts 4 ASCII Base64 chars into 3 raw bytes (handling trailing padding).
func ContractBase64(src []byte, dst []byte, padCount int, urlSafe bool) int {
	lut := &b64RevStd
	if urlSafe {
		lut = &b64RevURL
	}
	n := len(src)
	numQuads := n / 4
	dstIdx := 0

	for q := range numQuads {
		sIdx := q * 4
		isLast := (q == numQuads-1) && (padCount > 0)
		if !isLast {
			v0 := uint32(lut[src[sIdx]])
			v1 := uint32(lut[src[sIdx+1]])
			v2 := uint32(lut[src[sIdx+2]])
			v3 := uint32(lut[src[sIdx+3]])
			u := (v0 << 18) | (v1 << 12) | (v2 << 6) | v3
			dst[dstIdx] = byte(u >> 16)
			dst[dstIdx+1] = byte(u >> 8)
			dst[dstIdx+2] = byte(u)
			dstIdx += 3
		} else if padCount == 1 {
			v0 := uint32(lut[src[sIdx]])
			v1 := uint32(lut[src[sIdx+1]])
			v2 := uint32(lut[src[sIdx+2]])
			u := (v0 << 18) | (v1 << 12) | (v2 << 6)
			dst[dstIdx] = byte(u >> 16)
			dst[dstIdx+1] = byte(u >> 8)
			dstIdx += 2
		} else if padCount == 2 {
			v0 := uint32(lut[src[sIdx]])
			v1 := uint32(lut[src[sIdx+1]])
			u := (v0 << 18) | (v1 << 12)
			dst[dstIdx] = byte(u >> 16)
			dstIdx += 1
		}
	}
	return dstIdx
}

// ExpandBase64To expands raw binary bytes into 4-char ASCII Base64 chunks with padding.
func ExpandBase64To(dst []byte, src []byte, padCount int, urlSafe bool) {
	lut := b64LutStd
	if urlSafe {
		lut = b64LutURL
	}

	srcLen := len(src)
	fullTriplets := srcLen / 3
	remBytes := srcLen % 3

	dstIdx := 0
	for t := range fullTriplets {
		if dstIdx+4 > len(dst) {
			return
		}
		sIdx := t * 3
		u := (uint32(src[sIdx]) << 16) | (uint32(src[sIdx+1]) << 8) | uint32(src[sIdx+2])
		dst[dstIdx] = lut[(u>>18)&0x3F]
		dst[dstIdx+1] = lut[(u>>12)&0x3F]
		dst[dstIdx+2] = lut[(u>>6)&0x3F]
		dst[dstIdx+3] = lut[u&0x3F]
		dstIdx += 4
	}

	if remBytes == 2 {
		if dstIdx+4 > len(dst) {
			return
		}
		sIdx := fullTriplets * 3
		u := (uint32(src[sIdx]) << 16) | (uint32(src[sIdx+1]) << 8)
		dst[dstIdx] = lut[(u>>18)&0x3F]
		dst[dstIdx+1] = lut[(u>>12)&0x3F]
		dst[dstIdx+2] = lut[(u>>6)&0x3F]
		dst[dstIdx+3] = '='
	} else if remBytes == 1 {
		if dstIdx+4 > len(dst) {
			return
		}
		sIdx := fullTriplets * 3
		u := uint32(src[sIdx]) << 16
		dst[dstIdx] = lut[(u>>18)&0x3F]
		dst[dstIdx+1] = lut[(u>>12)&0x3F]
		dst[dstIdx+2] = '='
		dst[dstIdx+3] = '='
	}
}

func isHexChar(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

func isB64Char(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') ||
		b == '+' || b == '/' || b == '-' || b == '_' || b == '='
}

// DetectNormSpans scans src for contiguous Hex and Base64 runs >= 64 bytes.
func DetectNormSpans(src []byte) NormPlan {
	n := len(src)
	if n < minNormSpanLen {
		return NormPlan{}
	}

	var spans []NormSpan
	i := 0

	for i < n {
		if !isHexChar(src[i]) && !isB64Char(src[i]) {
			i++
			continue
		}

		start := i
		for i < n && (isHexChar(src[i]) || isB64Char(src[i])) {
			i++
		}
		spanLen := i - start
		if spanLen < minNormSpanLen {
			continue
		}

		candidate := src[start:i]

		// Priority 1: Check Hex
		isHex, upper := IsWholeBlockHex(candidate)
		if !isHex && (spanLen%2) != 0 && spanLen-1 >= minNormSpanLen {
			// Try even length
			isHex, upper = IsWholeBlockHex(candidate[:spanLen-1])
			if isHex {
				spanLen--
				candidate = candidate[:spanLen]
			}
		}

		if isHex {
			contractedLen := spanLen / 2
			spanType := NormSpanHexLower
			if upper {
				spanType = NormSpanHexUpper
			}
			spans = append(spans, NormSpan{
				Type:          spanType,
				OrigOffset:    start,
				OrigLen:       spanLen,
				ContractedLen: contractedLen,
			})
			continue
		}

		// Priority 2: Check Base64
		// Ensure multiple of 4
		rem := spanLen % 4
		if rem != 0 {
			spanLen -= rem
			candidate = candidate[:spanLen]
		}
		if spanLen >= minNormSpanLen {
			isB64, padCount, urlSafe := IsWholeBlockBase64(candidate)
			if isB64 {
				contractedLen := (spanLen/4)*3 - padCount
				var spanType uint8
				if !urlSafe {
					switch padCount {
					case 0:
						spanType = NormSpanBase64Std
					case 1:
						spanType = NormSpanBase64Pad1
					case 2:
						spanType = NormSpanBase64Pad2
					}
				} else {
					switch padCount {
					case 0:
						spanType = NormSpanBase64URL
					case 1:
						spanType = NormSpanBase64URLPad1
					case 2:
						spanType = NormSpanBase64URLPad2
					}
				}
				spans = append(spans, NormSpan{
					Type:          spanType,
					OrigOffset:    start,
					OrigLen:       spanLen,
					ContractedLen: contractedLen,
				})
			}
		}
	}

	if len(spans) == 0 {
		return NormPlan{}
	}

	grossSavings := 0
	for _, s := range spans {
		grossSavings += s.OrigLen - s.ContractedLen
	}

	// Descriptor overhead in prepended span table: 2 bytes count + 13 bytes per span
	tableOverhead := 2 + 13*len(spans)
	netSavings := grossSavings - tableOverhead

	if netSavings < minNetSavings {
		return NormPlan{}
	}

	contractedSize := n - grossSavings + tableOverhead
	return NormPlan{
		Spans:          spans,
		NetSavings:     netSavings,
		ContractedSize: contractedSize,
	}
}

// EncodeNormSpans builds the contracted buffer with prepended span table.
// Format:
// [0..1] numSpans uint16_le (K)
// For each span:
//
//	[0] spanType uint8
//	[1..4] contractedOffset uint32_le
//	[5..8] contractedLen uint32_le
//	[9..12] originalLen uint32_le
//
// Followed by contracted payload data.
func EncodeNormSpans(src []byte, plan NormPlan, out []byte) []byte {
	numSpans := len(plan.Spans)
	tableSize := 2 + 13*numSpans
	requiredCap := plan.ContractedSize
	if cap(out) < requiredCap {
		out = make([]byte, requiredCap)
	} else {
		out = out[:requiredCap]
	}

	binary.LittleEndian.PutUint16(out[0:2], uint16(numSpans))

	dataWritePos := tableSize
	srcReadPos := 0

	for i, s := range plan.Spans {
		// Copy unmodified literals prior to this span
		litLen := s.OrigOffset - srcReadPos
		if litLen > 0 {
			copy(out[dataWritePos:], src[srcReadPos:s.OrigOffset])
			dataWritePos += litLen
		}

		spanContractedStart := dataWritePos

		// Write contracted span data
		switch s.Type {
		case NormSpanHexLower, NormSpanHexUpper:
			ContractHex(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen])
			dataWritePos += s.ContractedLen
		case NormSpanBase64Std:
			ContractBase64(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen], 0, false)
			dataWritePos += s.ContractedLen
		case NormSpanBase64Pad1:
			ContractBase64(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen], 1, false)
			dataWritePos += s.ContractedLen
		case NormSpanBase64Pad2:
			ContractBase64(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen], 2, false)
			dataWritePos += s.ContractedLen
		case NormSpanBase64URL:
			ContractBase64(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen], 0, true)
			dataWritePos += s.ContractedLen
		case NormSpanBase64URLPad1:
			ContractBase64(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen], 1, true)
			dataWritePos += s.ContractedLen
		case NormSpanBase64URLPad2:
			ContractBase64(src[s.OrigOffset:s.OrigOffset+s.OrigLen], out[dataWritePos:dataWritePos+s.ContractedLen], 2, true)
			dataWritePos += s.ContractedLen
		}

		// Record descriptor in span table
		descOffset := 2 + 13*i
		out[descOffset] = s.Type
		binary.LittleEndian.PutUint32(out[descOffset+1:descOffset+5], uint32(spanContractedStart))
		binary.LittleEndian.PutUint32(out[descOffset+5:descOffset+9], uint32(s.ContractedLen))
		binary.LittleEndian.PutUint32(out[descOffset+9:descOffset+13], uint32(s.OrigLen))

		srcReadPos = s.OrigOffset + s.OrigLen
	}

	// Copy trailing literals
	if srcReadPos < len(src) {
		rem := len(src) - srcReadPos
		copy(out[dataWritePos:], src[srcReadPos:])
		dataWritePos += rem
	}

	return out[:dataWritePos]
}

// ExpandNormSpansTo expands a stream with prepended span table directly into dst[:uSize].
// Executes with strictly 0 heap allocations.
func ExpandNormSpansTo(dst []byte, contracted []byte) (int, error) {
	if len(contracted) < 2 {
		return 0, ErrCorruptedNormStream
	}
	numSpans := int(binary.LittleEndian.Uint16(contracted[0:2]))
	tableSize := 2 + 13*numSpans
	if len(contracted) < tableSize {
		return 0, ErrCorruptedNormStream
	}

	dstPos := 0
	dataReadPos := tableSize

	for i := range numSpans {
		descOffset := 2 + 13*i
		spanType := contracted[descOffset]
		contractedOffset := int(binary.LittleEndian.Uint32(contracted[descOffset+1 : descOffset+5]))
		contractedLen := int(binary.LittleEndian.Uint32(contracted[descOffset+5 : descOffset+9]))
		origLen := int(binary.LittleEndian.Uint32(contracted[descOffset+9 : descOffset+13]))

		if contractedOffset < dataReadPos || contractedOffset+contractedLen > len(contracted) {
			return 0, ErrCorruptedNormStream
		}

		// Copy unmodified literals preceding this span
		litLen := contractedOffset - dataReadPos
		if litLen > 0 {
			if dstPos+litLen > len(dst) {
				return 0, ErrCorruptedNormStream
			}
			copy(dst[dstPos:], contracted[dataReadPos:contractedOffset])
			dstPos += litLen
		}

		if dstPos+origLen > len(dst) {
			return 0, ErrCorruptedNormStream
		}

		var expectedOrigLen int
		switch spanType {
		case NormSpanHexLower, NormSpanHexUpper:
			expectedOrigLen = contractedLen * 2
		case NormSpanBase64Std, NormSpanBase64URL:
			if contractedLen%3 != 0 {
				return 0, ErrCorruptedNormStream
			}
			expectedOrigLen = (contractedLen / 3) * 4
		case NormSpanBase64Pad1, NormSpanBase64URLPad1:
			if contractedLen%3 != 2 {
				return 0, ErrCorruptedNormStream
			}
			expectedOrigLen = ((contractedLen + 1) / 3) * 4
		case NormSpanBase64Pad2, NormSpanBase64URLPad2:
			if contractedLen%3 != 1 {
				return 0, ErrCorruptedNormStream
			}
			expectedOrigLen = ((contractedLen + 2) / 3) * 4
		default:
			return 0, ErrCorruptedNormStream
		}
		if origLen != expectedOrigLen {
			return 0, ErrCorruptedNormStream
		}

		spanData := contracted[contractedOffset : contractedOffset+contractedLen]

		switch spanType {
		case NormSpanHexLower:
			ExpandHexTo(dst[dstPos:dstPos+origLen], spanData, false)
		case NormSpanHexUpper:
			ExpandHexTo(dst[dstPos:dstPos+origLen], spanData, true)
		case NormSpanBase64Std:
			ExpandBase64To(dst[dstPos:dstPos+origLen], spanData, 0, false)
		case NormSpanBase64Pad1:
			ExpandBase64To(dst[dstPos:dstPos+origLen], spanData, 1, false)
		case NormSpanBase64Pad2:
			ExpandBase64To(dst[dstPos:dstPos+origLen], spanData, 2, false)
		case NormSpanBase64URL:
			ExpandBase64To(dst[dstPos:dstPos+origLen], spanData, 0, true)
		case NormSpanBase64URLPad1:
			ExpandBase64To(dst[dstPos:dstPos+origLen], spanData, 1, true)
		case NormSpanBase64URLPad2:
			ExpandBase64To(dst[dstPos:dstPos+origLen], spanData, 2, true)
		}

		dstPos += origLen
		dataReadPos = contractedOffset + contractedLen
	}

	// Copy trailing literals
	if dataReadPos < len(contracted) {
		rem := len(contracted) - dataReadPos
		if dstPos+rem > len(dst) {
			return 0, ErrCorruptedNormStream
		}
		copy(dst[dstPos:], contracted[dataReadPos:])
		dstPos += rem
	}

	return dstPos, nil
}
