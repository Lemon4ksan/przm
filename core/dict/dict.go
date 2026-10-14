// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dict

import (
	"errors"

	"github.com/lemon4ksan/przm/core/internal/tables"
)

// ErrCorrupted indicates malformed dictionary data or invalid indices.
var ErrCorrupted = errors.New("dict: corrupted dictionary stream")

// Aliases for RFC 7932 and Brotli dictionary tables.
var (
	WordBase         = tables.WordBase
	DictOffsets      = tables.DictOffsets
	Transforms       = tables.Transforms
	CompactPairs     = tables.CompactPairs
	PSStrings        = tables.PSStrings
	TopRFCWords      = tables.TopRFCWords
	WebTokens        = tables.WebTokens
	BrotliDictionary = tables.BrotliDictionary
)

// LookupBrotliWord retrieves word bytes from the static RFC 7932 dictionary by word ID.
func LookupBrotliWord(wid uint32) []byte {
	return tables.LookupBrotliWord(wid)
}

// LookupWebToken retrieves a built-in web/text token by index.
func LookupWebToken(idx int) (string, bool) {
	if idx < 0 || idx >= len(tables.WebTokens) {
		return "", false
	}
	return tables.WebTokens[idx], true
}

// readLEB128Internal reads a 32-bit LEB128 integer from b at pos.
func readLEB128Internal(b []byte, pos *int) (uint32, error) {
	var res uint32
	var shift uint
	p := *pos
	for range 4 {
		if p >= len(b) {
			return 0, ErrCorrupted
		}
		byteVal := b[p]
		p++
		res |= uint32(byteVal&0x7F) << shift
		if (byteVal & 0x80) != 0 {
			shift += 7
			continue
		}
		*pos = p
		return res, nil
	}
	return 0, ErrCorrupted
}

var topRFCWordsBytes [512][]byte

func init() {
	for i := range tables.TopRFCWords {
		topRFCWordsBytes[i] = []byte(tables.TopRFCWords[i])
	}
}

// resolveWordByWid fetches word bytes from the base dictionary or top RFC words.
func resolveWordByWid(wid uint32) []byte {
	if wid < 13504 {
		return tables.LookupBrotliWord(wid)
	}
	idx := int(wid - 13504)
	if idx >= len(topRFCWordsBytes) {
		return nil
	}
	return topRFCWordsBytes[idx]
}

// resolveCompactPair extracts wid and tau from a compact RFC pair index.
func resolveCompactPair(c uint32) ([]byte, uint32, error) {
	if c >= uint32(len(tables.CompactPairs)) {
		return nil, 0, ErrCorrupted
	}
	wid := uint32(tables.CompactPairs[c].WId)
	tau := uint32(tables.CompactPairs[c].Tau)
	wPtr := resolveWordByWid(wid)
	return wPtr, tau, nil
}

// ResolveRFCWord decodes word pointer and transform index tau from command stream.
func ResolveRFCWord(b0 byte, cmd []byte, cp *int) ([]byte, uint32, error) {
	if b0 < 128 {
		tau := uint32(b0)
		wid, err := readLEB128Internal(cmd, cp)
		if err != nil {
			return nil, 0, err
		}
		wPtr := resolveWordByWid(wid)
		if len(wPtr) == 0 || tau > 120 {
			return nil, 0, ErrCorrupted
		}
		return wPtr, tau, nil
	}
	if *cp >= len(cmd) {
		return nil, 0, ErrCorrupted
	}
	b1 := cmd[*cp]
	*cp++
	p := (uint32(b0&0x7F) << 7) | uint32(b1&0x7F)
	return resolveExtendedWord(p)
}

// resolveExtendedWord decodes an RFC word when p >= 128.
func resolveExtendedWord(p uint32) ([]byte, uint32, error) {
	if p < 13504 {
		return LookupBrotliWord(p), 0, nil
	}
	if p < 14016 {
		return topRFCWordsBytes[p-13504], 0, nil
	}
	wPtr, tau, err := resolveCompactPair(p - 14016)
	if err != nil || len(wPtr) == 0 || tau > 120 {
		return nil, 0, ErrCorrupted
	}
	return wPtr, tau, nil
}

// toUppercase converts ASCII lowercase characters in-place to uppercase.
func toUppercase(b []byte) {
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
}

// capitalize capitalizes the first character of ASCII bytes in-place.
func capitalize(b []byte) {
	if len(b) > 0 && b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 32
	}
}

// transformWord applies RFC 7932 word operations.
func transformWord(op uint8, mw, scratch []byte) []byte {
	mwLen := len(mw)
	if op >= 1 && op <= 9 {
		cut := int(op)
		if cut >= mwLen {
			return nil
		}
		return mw[:mwLen-cut]
	}
	if op == 10 {
		copy(scratch[:mwLen], mw)
		capitalize(scratch[:mwLen])
		return scratch[:mwLen]
	}
	if op == 11 {
		copy(scratch[:mwLen], mw)
		toUppercase(scratch[:mwLen])
		return scratch[:mwLen]
	}
	if op >= 12 && op <= 20 {
		sk := int(op - 11)
		if sk >= mwLen {
			return nil
		}
		return mw[sk:]
	}
	return mw
}

// ApplyTransform applies string transformations and copies result into dst.
func ApplyTransform(dst []byte, dstPos *int, uSize uint32, wPtr []byte, tau uint32, scratch []byte) error {
	if tau >= uint32(len(Transforms)) {
		return ErrCorrupted
	}
	t := Transforms[tau]
	pre := PSStrings[t.PIdx]
	suf := PSStrings[t.SIdx]
	mw := transformWord(t.Op, wPtr, scratch)

	emitLen := len(pre) + len(mw) + len(suf)
	if *dstPos+emitLen > int(uSize) {
		return ErrCorrupted
	}
	copy(dst[*dstPos:], pre)
	*dstPos += len(pre)
	copy(dst[*dstPos:], mw)
	*dstPos += len(mw)
	copy(dst[*dstPos:], suf)
	*dstPos += len(suf)
	return nil
}

// ExternalDictMapper dynamically maps word IDs to byte slice offsets in an external dictionary.
type ExternalDictMapper struct {
	Dict    []byte
	Base    []uint16
	Offsets []uint32
}

// NewExternalDictMapper constructs a new dynamic ExternalDictMapper.
func NewExternalDictMapper(dict []byte, base []uint16, offsets []uint32) *ExternalDictMapper {
	return &ExternalDictMapper{
		Dict:    dict,
		Base:    base,
		Offsets: offsets,
	}
}

// Lookup finds a word byte slice by its ID in the external dictionary.
func (m *ExternalDictMapper) Lookup(wid uint32) ([]byte, bool) {
	if len(m.Base) == 0 || len(m.Offsets) == 0 || len(m.Dict) == 0 {
		return nil, false
	}
	low := 0
	high := len(m.Base) - 1
	best := 0
	for low <= high {
		mid := (low + high) / 2
		if uint32(m.Base[mid]) <= wid {
			best = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if best >= len(m.Offsets) {
		return nil, false
	}
	wLen := best + 4
	idx := int(wid - uint32(m.Base[best]))
	off := int(m.Offsets[best]) + idx*wLen
	if off < 0 || off+wLen > len(m.Dict) {
		return nil, false
	}
	return m.Dict[off : off+wLen], true
}
