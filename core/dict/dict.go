package dict

import (
	"errors"
)

// ErrCorrupted indicates malformed dictionary data or invalid indices.
var ErrCorrupted = errors.New("dict: corrupted dictionary stream")


// LookupWebToken retrieves a built-in web/text token by index.
func LookupWebToken(idx int) (string, bool) {
	if idx < 0 || idx >= len(WebTokens) {
		return "", false
	}
	return WebTokens[idx], true
}

// readLEB128Internal reads a 32-bit LEB128 integer from b at pos.
func readLEB128Internal(b []byte, pos *int) (uint32, error) {
	var res uint32
	var shift uint
	p := *pos
	for i := 0; i < 4; i++ {
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

// resolveWordByWid fetches word bytes from the base dictionary or top RFC words.
func resolveWordByWid(wid uint32) []byte {
	if wid < 13504 {
		return LookupBrotliWord(wid)
	}
	idx := int(wid - 13504)
	if idx >= len(TopRFCWords) {
		return nil
	}
	return []byte(TopRFCWords[idx])
}

// resolveCompactPair extracts wid and tau from a compact RFC pair index.
func resolveCompactPair(c uint32) ([]byte, uint32, error) {
	if c >= uint32(len(CompactPairs)) {
		return nil, 0, ErrCorrupted
	}
	wid := uint32(CompactPairs[c].WId)
	tau := uint32(CompactPairs[c].Tau)
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
		return []byte(TopRFCWords[p-13504]), 0, nil
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
func transformWord(op uint8, mw []byte, scratch []byte) []byte {
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
