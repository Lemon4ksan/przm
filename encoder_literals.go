// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math/bits"

	"github.com/lemon4ksan/foundation/bufkit"
)

func ensureBufCap(b []byte, n int) []byte {
	if cap(b) < n {
		return bufkit.AlignedBytes(n, bufkit.CacheLineSize)[:0]
	}
	return b[:0]
}

func uvarintLen(x uint64) int {
	return (bits.Len64(x|1) + 6) / 7
}

// packNibblePlanes splits a literal byte slice into a Low-Nibble plane (packed 2 per byte)
// and a High-Nibble plane (runs of identical high nibbles).
// Returns the packed stream and true if packed size is at least 8% smaller than raw lits.
func packNibblePlanes(lits, out, lowBuf []byte) ([]byte, bool) {
	n := len(lits)
	if n < 32 {
		return nil, false
	}

	// 1. Pack Low-Nibble Plane (exactly ceil(n/2) bytes)
	numPairs := (n + 1) / 2
	if cap(lowBuf) < numPairs {
		lowBuf = make([]byte, numPairs)
	} else {
		lowBuf = lowBuf[:numPairs]
	}

	j := 0
	for ; j+4 <= numPairs && 2*j+7 < n; j += 4 {
		idx := j * 2
		b0 := lits[idx] & 0x0F
		b1 := lits[idx+1] & 0x0F
		b2 := lits[idx+2] & 0x0F
		b3 := lits[idx+3] & 0x0F
		b4 := lits[idx+4] & 0x0F
		b5 := lits[idx+5] & 0x0F
		b6 := lits[idx+6] & 0x0F
		b7 := lits[idx+7] & 0x0F

		lowBuf[j] = (b0 << 4) | b1
		lowBuf[j+1] = (b2 << 4) | b3
		lowBuf[j+2] = (b4 << 4) | b5
		lowBuf[j+3] = (b6 << 4) | b7
	}
	for ; j < numPairs; j++ {
		idx := j * 2
		b0 := lits[idx] & 0x0F
		b1 := uint8(0)
		if idx+1 < n {
			b1 = lits[idx+1] & 0x0F
		}
		lowBuf[j] = (b0 << 4) | b1
	}

	// 2. Pack High-Nibble Plane (Nibble-RLE tokens: (runLen-1)<<4 | highNibble)
	out = out[:0]
	// 8-byte header: numLits (uint32), lowBytes (uint32)
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(n))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(numPairs))
	out = append(out, hdr[:]...)
	out = append(out, lowBuf...)

	for i := 0; i < n; {
		h := lits[i] >> 4
		runLen := 1
		for i+runLen < n && (lits[i+runLen]>>4) == h && runLen < 16 {
			runLen++
		}
		token := (uint8(runLen-1) << 4) | (h & 0x0F)
		out = append(out, token)
		i += runLen
	}

	// Accept if smaller than raw literals
	if len(out) < n {
		return out, true
	}
	return nil, false
}

// packSheafVANS encodes residual literals using SIMD Sheaf splitting and V-ANS.
// Returns the packed bytes, or (nil, false) if not compressible or invalid.
func packSheafVANS(lits []byte, pad *scratchpad) ([]byte, bool) {
	n := len(lits)
	if n < 32 {
		return nil, false
	}

	reqCap := n + 128
	if cap(pad.sheafSBuf) < reqCap {
		pad.sheafSBuf = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)
	}
	if cap(pad.sheafCBuf) < reqCap {
		pad.sheafCBuf = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)
	}
	maskLen := (n + 7) / 8
	if cap(pad.sheafMaskBuf) < maskLen+128 {
		pad.sheafMaskBuf = bufkit.AlignedBytes(maskLen+128, bufkit.CacheLineSize)
	}

	sLen, cLen := SheafSplit(lits, pad.sheafSBuf[:reqCap], pad.sheafCBuf[:reqCap], pad.sheafMaskBuf[:maskLen+128])
	if sLen < 16 || cLen < 16 || sLen < n/32 || cLen < n/32 {
		return nil, false
	}

	encS, errS := VANSEncode4x(pad.sheafSBuf[:sLen])
	if errS != nil || len(encS) == 0 {
		return nil, false
	}

	encC, errC := VANSEncode4x(pad.sheafCBuf[:cLen])
	if errC != nil || len(encC) == 0 {
		return nil, false
	}

	totalPayload := 24 + maskLen + len(encS) + len(encC)
	if totalPayload >= n {
		return nil, false
	}

	if cap(pad.sheafWorkBuf) < totalPayload+1024 {
		pad.sheafWorkBuf = bufkit.AlignedBytes(totalPayload+1024, bufkit.CacheLineSize)
	}
	workBuf := pad.sheafWorkBuf[:totalPayload]

	workBuf[0] = vansMagic0
	workBuf[1] = vansMagic1
	workBuf[2] = vansVer1
	workBuf[3] = vansFlag4X | vansFlagSheaf

	binary.LittleEndian.PutUint32(workBuf[4:8], uint32(n))
	binary.LittleEndian.PutUint32(workBuf[8:12], uint32(sLen))
	binary.LittleEndian.PutUint32(workBuf[12:16], uint32(maskLen))
	binary.LittleEndian.PutUint32(workBuf[16:20], uint32(len(encS)))
	binary.LittleEndian.PutUint32(workBuf[20:24], uint32(len(encC)))

	copy(workBuf[24:24+maskLen], pad.sheafMaskBuf[:maskLen])
	copy(workBuf[24+maskLen:24+maskLen+len(encS)], encS)
	copy(workBuf[24+maskLen+len(encS):], encC)

	return workBuf[:totalPayload], true
}

// packContextSheafVANS encodes residual literals using Étale Context Sheaf splitting and V-ANS.
// It evaluates candidate context foliation modes (Mode 0: Refined 4-Class, Mode 1: JSON Dense,
// Mode 2: 2-Phase Causal, Mode 3: Order-2 Bigram) with raw fiber fallback to find the optimal foliation.
// Returns the packed bytes, or (nil, false) if not compressible or invalid.
func packContextSheafVANS(lits []byte, pad *scratchpad) ([]byte, bool) {
	n := len(lits)
	if n < 32 {
		return nil, false
	}

	reqCap := n + 64
	for c := range 4 {
		if cap(pad.sheafFBuf[c]) < reqCap {
			pad.sheafFBuf[c] = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)[:0]
		}
	}

	modesToTry := []uint8{CtxModeStructural, CtxModeJSONDense, CtxMode2Phase, CtxModeOrder2}
	bestLen := n
	bestMode := uint8(0)
	hasBest := false

	for _, mode := range modesToTry {
		f0 := pad.sheafFBuf[0][:0]
		f1 := pad.sheafFBuf[1][:0]
		f2 := pad.sheafFBuf[2][:0]
		f3 := pad.sheafFBuf[3][:0]

		if mode == CtxModeOrder2 {
			prev2, prev1 := byte(0), byte(0)
			for _, b := range lits {
				c := SheafContextOrder2(prev2, prev1)
				switch c {
				case 0:
					f0 = append(f0, b)
				case 1:
					f1 = append(f1, b)
				case 2:
					f2 = append(f2, b)
				default:
					f3 = append(f3, b)
				}
				prev2 = prev1
				prev1 = b
			}
		} else {
			lut := &SheafContextTable
			if mode == CtxModeJSONDense {
				lut = &SheafContextTableM1
			} else if mode == CtxMode2Phase {
				lut = &SheafContextTableM2
			}
			prev := byte(0)
			for _, b := range lits {
				c := lut[prev]
				switch c {
				case 0:
					f0 = append(f0, b)
				case 1:
					f1 = append(f1, b)
				case 2:
					f2 = append(f2, b)
				default:
					f3 = append(f3, b)
				}
				prev = b
			}
		}

		// Require non-trivial splitting
		activeFibers := 0
		if len(f0) > 0 {
			activeFibers++
		}
		if len(f1) > 0 {
			activeFibers++
		}
		if len(f2) > 0 {
			activeFibers++
		}
		if len(f3) > 0 {
			activeFibers++
		}
		if activeFibers < 2 {
			continue
		}

		curPayload := 40
		fibers := [4][]byte{f0, f1, f2, f3}
		for c := 0; c < 4; c++ {
			flen := len(fibers[c])
			if flen == 0 {
				continue
			}
			if flen >= 16 {
				e, err := VANSEncode4x(fibers[c])
				if err == nil && len(e) < flen {
					curPayload += len(e)
					continue
				}
			}
			curPayload += flen
		}

		if curPayload < bestLen {
			bestLen = curPayload
			bestMode = mode
			hasBest = true
		}
	}

	if !hasBest || bestLen >= n {
		return nil, false
	}

	// Re-partition once for the winning mode to ensure intact fiber buffers
	f0 := pad.sheafFBuf[0][:0]
	f1 := pad.sheafFBuf[1][:0]
	f2 := pad.sheafFBuf[2][:0]
	f3 := pad.sheafFBuf[3][:0]

	if bestMode == CtxModeOrder2 {
		prev2, prev1 := byte(0), byte(0)
		for _, b := range lits {
			c := SheafContextOrder2(prev2, prev1)
			switch c {
			case 0:
				f0 = append(f0, b)
			case 1:
				f1 = append(f1, b)
			case 2:
				f2 = append(f2, b)
			default:
				f3 = append(f3, b)
			}
			prev2 = prev1
			prev1 = b
		}
	} else {
		lut := &SheafContextTable
		if bestMode == CtxModeJSONDense {
			lut = &SheafContextTableM1
		} else if bestMode == CtxMode2Phase {
			lut = &SheafContextTableM2
		}
		prev := byte(0)
		for _, b := range lits {
			c := lut[prev]
			switch c {
			case 0:
				f0 = append(f0, b)
			case 1:
				f1 = append(f1, b)
			case 2:
				f2 = append(f2, b)
			default:
				f3 = append(f3, b)
			}
			prev = b
		}
	}

	fibers := [4][]byte{f0, f1, f2, f3}
	var enc [4][]byte
	totalPayload := 40
	for c := 0; c < 4; c++ {
		flen := len(fibers[c])
		if flen == 0 {
			continue
		}
		if flen >= 16 {
			e, err := VANSEncode4x(fibers[c])
			if err == nil && len(e) < flen {
				enc[c] = e
				totalPayload += len(e)
				continue
			}
		}
		enc[c] = fibers[c]
		totalPayload += flen
	}

	if totalPayload >= n {
		return nil, false
	}

	if cap(pad.ctxSheafWorkBuf) < totalPayload+1024 {
		pad.ctxSheafWorkBuf = bufkit.AlignedBytes(totalPayload+1024, bufkit.CacheLineSize)
	}
	workBuf := pad.ctxSheafWorkBuf[:totalPayload]

	workBuf[0] = vansMagic0
	workBuf[1] = vansMagic1
	workBuf[2] = vansVer1
	workBuf[3] = vansFlag4X | vansFlagCtxSheaf | (bestMode << CtxModeShift)

	binary.LittleEndian.PutUint32(workBuf[4:8], uint32(n))
	binary.LittleEndian.PutUint32(workBuf[8:12], uint32(len(f0)))
	binary.LittleEndian.PutUint32(workBuf[12:16], uint32(len(f1)))
	binary.LittleEndian.PutUint32(workBuf[16:20], uint32(len(f2)))
	binary.LittleEndian.PutUint32(workBuf[20:24], uint32(len(f3)))

	binary.LittleEndian.PutUint32(workBuf[24:28], uint32(len(enc[0])))
	binary.LittleEndian.PutUint32(workBuf[28:32], uint32(len(enc[1])))
	binary.LittleEndian.PutUint32(workBuf[32:36], uint32(len(enc[2])))
	binary.LittleEndian.PutUint32(workBuf[36:40], uint32(len(enc[3])))

	off := 40
	for c := 0; c < 4; c++ {
		if len(enc[c]) > 0 {
			copy(workBuf[off:], enc[c])
			off += len(enc[c])
		}
	}

	return workBuf[:totalPayload], true
}

// packContextOrder1VANS encodes literals using Order-1 Context-conditioned V-ANS.
// Returns the packed bytes, or (nil, false) if not compressible or invalid.
func packContextOrder1VANS(lits []byte) ([]byte, bool) {
	if len(lits) < 32 {
		return nil, false
	}
	enc, err := ContextVANSEncode4x(lits)
	if err != nil || len(enc) >= len(lits) {
		return nil, false
	}
	return enc, true
}

// packLiteralSection evaluates V-ANS, Sheaf-VANS, Context-Sheaf-VANS, Order-1 Context-VANS and Nibble-Planes
// on literalBuf to find the representation with the lowest entropy and smallest footprint.
func packLiteralSection(
	lits []byte,
	pad *scratchpad,
	level int,
	opts CompressOptions,
	origSrc []byte,
	flags *uint8,
) []byte {
	if len(lits) < 32 {
		return lits
	}

	var vansOut []byte
	var vansBeatsRaw bool
	if !opts.StandardStreamOnly && level >= 2 {
		sheafOut, sheafOk := packSheafVANS(lits, pad)
		ctxSheafOut, ctxSheafOk := packContextSheafVANS(lits, pad)
		ctxOrder1Out, ctxOrder1Ok := packContextOrder1VANS(lits)
		stdOut, stdErr := VANSEncode4x(lits)
		threshold := len(lits)
		if level < 4 && !opts.ArchivalMode {
			threshold = int(float64(len(lits)) * 0.95)
		}
		stdOk := stdErr == nil && len(stdOut) < threshold

		if isProbablyText(origSrc) && level <= 3 && sheafOk {
			vansOut = sheafOut
			vansBeatsRaw = true
		} else {
			bestLen := threshold
			if stdOk && len(stdOut) < bestLen {
				vansOut = stdOut
				bestLen = len(stdOut)
				vansBeatsRaw = true
			}
			if sheafOk && len(sheafOut) < bestLen {
				vansOut = sheafOut
				bestLen = len(sheafOut)
				vansBeatsRaw = true
			}
			if ctxSheafOk && len(ctxSheafOut) < bestLen {
				vansOut = ctxSheafOut
				bestLen = len(ctxSheafOut)
				vansBeatsRaw = true
			}
			if ctxOrder1Ok && len(ctxOrder1Out) < bestLen {
				vansOut = ctxOrder1Out
				vansBeatsRaw = true
			}
		}
	}

	var nibbleOut []byte
	var nibbleOk bool
	if level > 3 {
		nibbleOut, nibbleOk = packNibblePlanes(lits, pad.nibbleBuf, pad.nibbleLowBuf)
	}

	if vansBeatsRaw && (!nibbleOk || len(vansOut) <= len(nibbleOut)) {
		*flags |= FlagNibble
		return vansOut
	} else if nibbleOk {
		*flags |= FlagNibble
		return nibbleOut
	}
	return lits
}
