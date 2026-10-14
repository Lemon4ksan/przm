package przm

import (
	"encoding/binary"
)

// Flag bit constants for Section 4 V-ANS container header.
const (
	VansFlag4X        uint8 = 0x01
	VansFlagSheaf     uint8 = 0x02
	VansFlagCtxSheaf  uint8 = 0x04
	VansFlagCtxOrder1 uint8 = 0x08
	VansCtxModeMask   uint8 = 0xF0
)

// NormalizeFrequencies produces symbol frequencies summing strictly to 4096.
func NormalizeFrequencies(counts []int, freq []uint16) error {
	for i := range freq {
		freq[i] = 0
	}
	var totalCounts, unique int
	for _, c := range counts {
		if c > 0 {
			totalCounts += c
			unique++
		}
	}
	if unique == 0 {
		return nil
	}
	if unique > 4096 {
		return ErrCorruptedFrame
	}

	remaining := 4096 - unique
	var sum uint16
	for i, c := range counts {
		if c <= 0 {
			continue
		}
		share := (c * remaining) / totalCounts
		f := uint16(1 + share)
		freq[i] = f
		sum += f
	}

	return adjustFrequencySum(counts, freq, sum)
}

// adjustFrequencySum distributes remaining points to match 4096 exactly.
func adjustFrequencySum(counts []int, freq []uint16, sum uint16) error {
	diff := int(4096) - int(sum)
	if diff > 0 {
		for i := 0; i < diff; i++ {
			bestSym := -1
			bestCount := -1
			for s, c := range counts {
				if c > bestCount && freq[s] > 0 {
					bestCount = c
					bestSym = s
				}
			}
			if bestSym >= 0 {
				freq[bestSym]++
				counts[bestSym] = -1 // Deprioritize next iteration
			}
		}
	} else if diff < 0 {
		for i := 0; i < -diff; i++ {
			for s := 255; s >= 0; s-- {
				if freq[s] > 1 {
					freq[s]--
					break
				}
			}
		}
	}
	return nil
}

// EncodeJointNibbleFreq serializes the 16x16 joint nibble frequency matrix.
func EncodeJointNibbleFreq(freq []uint16, buf []byte) []byte {
	var rowMask uint16
	for h := 0; h < 16; h++ {
		for l := 0; l < 16; l++ {
			if freq[(h<<4)|l] > 0 {
				rowMask |= (1 << h)
				break
			}
		}
	}

	buf = binary.LittleEndian.AppendUint16(buf, rowMask)
	for h := 0; h < 16; h++ {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		var colMask uint16
		for l := 0; l < 16; l++ {
			if freq[(h<<4)|l] > 0 {
				colMask |= (1 << l)
			}
		}
		buf = binary.LittleEndian.AppendUint16(buf, colMask)
		for l := 0; l < 16; l++ {
			if (colMask & (1 << l)) == 0 {
				continue
			}
			w := freq[(h<<4)|l]
			buf = appendWeight(buf, w)
		}
	}
	return buf
}

// appendWeight encodes a single frequency weight (1 or 2 bytes).
func appendWeight(buf []byte, w uint16) []byte {
	if w < 128 {
		return append(buf, byte(w))
	}
	b0 := byte((w >> 8) | 0x80)
	b1 := byte(w & 0xFF)
	return append(buf, b0, b1)
}

// DecodeJointNibbleFreq parses the 16x16 joint nibble frequency distribution.
func DecodeJointNibbleFreq(src []byte, pos *int, freq []uint16) error {
	p := *pos
	if p+2 > len(src) {
		return ErrCorruptedFrame
	}
	rowMask := binary.LittleEndian.Uint16(src[p : p+2])
	p += 2

	for i := range freq {
		freq[i] = 0
	}
	var sum uint32

	for h := 0; h < 16; h++ {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		if p+2 > len(src) {
			return ErrCorruptedFrame
		}
		colMask := binary.LittleEndian.Uint16(src[p : p+2])
		p += 2

		rowSum, err := decodeRowWeights(src, &p, colMask, h, freq)
		if err != nil {
			return err
		}
		sum += rowSum
	}

	if sum != 4096 {
		return ErrCorruptedFrame
	}
	*pos = p
	return nil
}

// decodeRowWeights unpacks active column weights for a single row.
func decodeRowWeights(src []byte, p *int, colMask uint16, h int, freq []uint16) (uint32, error) {
	var rowSum uint32
	pos := *p
	for l := 0; l < 16; l++ {
		if (colMask & (1 << l)) == 0 {
			continue
		}
		if pos >= len(src) {
			return 0, ErrCorruptedFrame
		}
		b0 := src[pos]
		pos++
		var w uint16
		if (b0 & 0x80) == 0 {
			w = uint16(b0)
		} else {
			if pos >= len(src) {
				return 0, ErrCorruptedFrame
			}
			b1 := src[pos]
			pos++
			w = (uint16(b0&0x7F) << 8) | uint16(b1)
		}
		if w == 0 || w > 4096 {
			return 0, ErrCorruptedFrame
		}
		freq[(h<<4)|l] = w
		rowSum += uint32(w)
	}
	*p = pos
	return rowSum, nil
}

// BuildVansTable creates CDF and symbol lookup tables from frequencies.
func BuildVansTable(freq []uint16, cdf []uint16, lookup []byte) {
	var cum uint16
	for s := 0; s < 256; s++ {
		cdf[s] = cum
		f := freq[s]
		for k := uint16(0); k < f; k++ {
			lookup[cum+k] = byte(s)
		}
		cum += f
	}
}

// EncodeVANS4x compresses input literals into a 4-lane interleaved V-ANS stream.
func EncodeVANS4x(src []byte, freq []uint16) ([]byte, error) {
	var cdf [256]uint16
	var lookup [4096]byte
	BuildVansTable(freq, cdf[:], lookup[:])

	var x [4]uint32 = [4]uint32{65536, 65536, 65536, 65536}
	var streamBuf []byte

	for i := len(src) - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		f := uint32(freq[s])
		c := uint32(cdf[s])
		if f == 0 {
			return nil, ErrCorruptedFrame
		}

		for x[lane] > 4096*f-1 {
			streamBuf = append(streamBuf, byte(x[lane]&0xFF))
			x[lane] >>= 8
		}
		x[lane] = (x[lane]/f)*4096 + c + (x[lane] % f)
	}

	out := EncodeJointNibbleFreq(freq, nil)
	for j := 0; j < 4; j++ {
		out = binary.LittleEndian.AppendUint32(out, x[j])
	}
	for i := len(streamBuf) - 1; i >= 0; i-- {
		out = append(out, streamBuf[i])
	}
	return out, nil
}

// DecodeVANS4x decompresses a 4-lane interleaved V-ANS payload into dst.
func DecodeVANS4x(src []byte, dst []byte, uLen int, s *Scratchpad) error {
	if uLen == 0 {
		return nil
	}
	pos := 0
	if err := DecodeJointNibbleFreq(src, &pos, s.vansFreq[:]); err != nil {
		return err
	}
	BuildVansTable(s.vansFreq[:], s.vansCDF[:], s.vansLookup[:])

	if pos+16 > len(src) {
		return ErrCorruptedFrame
	}
	var x [4]uint32
	for j := 0; j < 4; j++ {
		x[j] = binary.LittleEndian.Uint32(src[pos : pos+4])
		pos += 4
	}

	return runVans4xLoop(src, &pos, dst, uLen, x, s)
}

// runVans4xLoop performs the forward interleaved vector decoding loop.
func runVans4xLoop(src []byte, pos *int, dst []byte, uLen int, x [4]uint32, s *Scratchpad) error {
	p := *pos
	for i := 0; i < uLen; i++ {
		lane := i & 3
		slot := x[lane] & 4095
		sym := s.vansLookup[slot]
		dst[i] = sym

		f := uint32(s.vansFreq[sym])
		c := uint32(s.vansCDF[sym])
		x[lane] = f*(x[lane]>>12) + (slot - c)

		for x[lane] < 65536 {
			if p >= len(src) {
				return ErrCorruptedFrame
			}
			x[lane] = (x[lane] << 8) | uint32(src[p])
			p++
		}
	}
	*pos = p
	return nil
}

// PackNibblePlanes formats literals into SIMD nibble planes.
func PackNibblePlanes(src []byte) []byte {
	n := len(src)
	nLow := (n + 1) / 2
	out := make([]byte, 8+nLow)
	binary.LittleEndian.PutUint32(out[0:4], uint32(n))
	binary.LittleEndian.PutUint32(out[4:8], uint32(nLow))

	for j := 0; j < nLow; j++ {
		p0 := src[2*j] & 0x0F
		var p1 byte
		if 2*j+1 < n {
			p1 = src[2*j+1] & 0x0F
		}
		out[8+j] = (p0 << 4) | p1
	}

	rleTokens := encodeHighNibbleRLE(src)
	return append(out, rleTokens...)
}

// encodeHighNibbleRLE computes RLE tokens for high nibbles.
func encodeHighNibbleRLE(src []byte) []byte {
	var tokens []byte
	n := len(src)
	i := 0
	for i < n {
		h := (src[i] >> 4) & 0x0F
		run := 1
		for i+run < n && run < 16 && ((src[i+run]>>4)&0x0F) == h {
			run++
		}
		tok := byte(((run - 1) << 4) | int(h))
		tokens = append(tokens, tok)
		i += run
	}
	return tokens
}

// UnpackNibblePlanes restores literals from SIMD low and high nibble planes.
func UnpackNibblePlanes(src []byte, dst []byte, uLen int) error {
	if len(src) < 8 {
		return ErrCorruptedFrame
	}
	n := int(binary.LittleEndian.Uint32(src[0:4]))
	nLow := int(binary.LittleEndian.Uint32(src[4:8]))
	if n != uLen || nLow != (n+1)/2 || len(dst) < n {
		return ErrCorruptedFrame
	}
	if 8+nLow > len(src) {
		return ErrCorruptedFrame
	}

	if err := unpackHighNibbleRLE(src[8+nLow:], dst, n); err != nil {
		return err
	}
	mergeLowNibbles(src[8:8+nLow], dst, n, nLow)
	return nil
}

// unpackHighNibbleRLE fills destination bytes with high nibble patterns.
func unpackHighNibbleRLE(rleSrc, dst []byte, n int) error {
	written := 0
	pos := 0
	for written < n && pos < len(rleSrc) {
		tok := rleSrc[pos]
		pos++
		rLen := int(tok>>4) + 1
		h := (tok & 0x0F) << 4
		if written+rLen > n {
			rLen = n - written
		}
		for k := 0; k < rLen; k++ {
			dst[written+k] = h
		}
		written += rLen
	}
	if written != n {
		return ErrCorruptedFrame
	}
	return nil
}

// mergeLowNibbles combines unpacked high nibbles with low nibble pairs.
func mergeLowNibbles(lowSrc, dst []byte, n, nLow int) {
	for j := 0; j < nLow; j++ {
		pair := lowSrc[j]
		dst[2*j] |= (pair >> 4)
		if 2*j+1 < n {
			dst[2*j+1] |= (pair & 0x0F)
		}
	}
}

// PackEtaleSheaf wraps partitioned literal fibers into the canonical 40-byte header.
func PackEtaleSheaf(src []byte, mode uint8) ([]byte, error) {
	fibers, err := DecomposeFibers(src, mode)
	if err != nil {
		return nil, err
	}
	uLit := len(src)
	var l [4]int
	for k := 0; k < 4; k++ {
		l[k] = len(fibers[k])
	}

	var encodedFibers [4][]byte
	var e [4]int
	for k := 0; k < 4; k++ {
		if l[k] == 0 {
			e[k] = 0
			continue
		}
		enc, encErr := tryEncodeFiber(fibers[k])
		if encErr == nil && len(enc) < l[k] {
			encodedFibers[k] = enc
			e[k] = len(enc)
		} else {
			encodedFibers[k] = fibers[k]
			e[k] = l[k]
		}
	}

	return assembleSheafContainer(uLit, mode, l, e, encodedFibers), nil
}

// tryEncodeFiber attempts 4-lane V-ANS compression on a single fiber.
func tryEncodeFiber(fiber []byte) ([]byte, error) {
	counts := make([]int, 256)
	for _, b := range fiber {
		counts[b]++
	}
	var freq [256]uint16
	if err := NormalizeFrequencies(counts, freq[:]); err != nil {
		return nil, err
	}
	return EncodeVANS4x(fiber, freq[:])
}

// assembleSheafContainer constructs the 40-byte header and concatenates fiber streams.
func assembleSheafContainer(uLit int, mode uint8, l, e [4]int, encodedFibers [4][]byte) []byte {
	out := make([]byte, 40)
	out[0] = 'V'
	out[1] = 'A'
	out[2] = 0x01
	out[3] = VansFlag4X | VansFlagCtxSheaf | ((mode & 0x0F) << 4)
	binary.LittleEndian.PutUint32(out[4:8], uint32(uLit))
	for k := 0; k < 4; k++ {
		binary.LittleEndian.PutUint32(out[8+k*4:12+k*4], uint32(l[k]))
		binary.LittleEndian.PutUint32(out[24+k*4:28+k*4], uint32(e[k]))
	}
	for k := 0; k < 4; k++ {
		if e[k] > 0 {
			out = append(out, encodedFibers[k]...)
		}
	}
	return out
}

// UnpackSection4Literals handles container dispatch for Section 4 literals.
func UnpackSection4Literals(s4 []byte, l4 int, s *Scratchpad) ([]byte, error) {
	if l4 == 0 {
		return nil, nil
	}
	if l4 >= 8 && s4[0] == 'V' && s4[1] == 'A' && s4[2] == 0x01 {
		flags := s4[3]
		uLen := int(binary.LittleEndian.Uint32(s4[4:8]))
		if uLen > len(s.lits) {
			return nil, ErrBufferTooSmall
		}
		if (flags & VansFlagCtxSheaf) != 0 {
			if err := unpackEtaleSheaf(s4[:l4], s.lits[:uLen], uLen, s); err != nil {
				return nil, err
			}
			return s.lits[:uLen], nil
		}
		if err := DecodeVANS4x(s4[8:l4], s.lits[:uLen], uLen, s); err != nil {
			return nil, err
		}
		return s.lits[:uLen], nil
	}
	if l4 >= 8 && binary.LittleEndian.Uint32(s4[4:8]) == (binary.LittleEndian.Uint32(s4[0:4])+1)/2 {
		nLits := int(binary.LittleEndian.Uint32(s4[0:4]))
		if nLits > len(s.lits) {
			return nil, ErrBufferTooSmall
		}
		if err := UnpackNibblePlanes(s4[:l4], s.lits[:nLits], nLits); err != nil {
			return nil, err
		}
		return s.lits[:nLits], nil
	}
	return s4[:l4], nil
}

// unpackEtaleSheaf validates headers and partitions fibers for reconstruction.
func unpackEtaleSheaf(s4, dst []byte, uLit int, s *Scratchpad) error {
	if len(s4) < 40 {
		return ErrCorruptedFrame
	}
	flags := s4[3]
	mode := (flags >> 4) & 0x0F
	if mode > 3 {
		return ErrCorruptedFrame
	}

	var l, e [4]int
	var sumL int
	for k := 0; k < 4; k++ {
		l[k] = int(binary.LittleEndian.Uint32(s4[8+k*4 : 12+k*4]))
		e[k] = int(binary.LittleEndian.Uint32(s4[24+k*4 : 28+k*4]))
		sumL += l[k]
	}
	if sumL != uLit || uLit > len(s.fiberBuf) {
		return ErrCorruptedFrame
	}
	if mode == 2 && (l[2] != 0 || l[3] != 0) {
		return ErrCorruptedFrame
	}

	return validateAndDecodeFibers(s4, dst, uLit, mode, l, e, s)
}

// validateAndDecodeFibers checks bounds and decodes individual fiber streams.
func validateAndDecodeFibers(s4, dst []byte, uLit int, mode uint8, l, e [4]int, s *Scratchpad) error {
	pos := 40
	for k := 0; k < 4; k++ {
		if l[k] == 0 && e[k] != 0 {
			return ErrCorruptedFrame
		}
		if e[k] > l[k] || pos+e[k] > len(s4) {
			return ErrCorruptedFrame
		}
		pos += e[k]
	}

	return decodeAndReconstituteFibers(s4, dst, uLit, mode, l, e, s)
}

// decodeAndReconstituteFibers decodes each fiber and executes causal foliation.
func decodeAndReconstituteFibers(s4, dst []byte, uLit int, mode uint8, l, e [4]int, s *Scratchpad) error {
	pos := 40
	fiberDstOffset := 0
	var fiberSlices [4][]byte

	for k := 0; k < 4; k++ {
		if l[k] == 0 {
			fiberSlices[k] = nil
			continue
		}
		sub := s4[pos : pos+e[k]]
		pos += e[k]
		outFiber := s.fiberBuf[fiberDstOffset : fiberDstOffset+l[k]]
		fiberDstOffset += l[k]

		if e[k] == l[k] {
			copy(outFiber, sub)
		} else {
			if err := DecodeVANS4x(sub, outFiber, l[k], s); err != nil {
				return err
			}
		}
		fiberSlices[k] = outFiber
	}

	return ReconstituteFibers(dst, uLit, fiberSlices[0], fiberSlices[1], fiberSlices[2], fiberSlices[3],
		l[0], l[1], l[2], l[3], mode)
}

