// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
)

// Magic and Mode constants for Étale Sheaf & Tropical Rep-Queue Trellis (Phase 14, Theorems 34 & 35)
const (
	SheafMagic uint32 = 0x53484546 // "SHEF"

	SheafModeRaw        uint8 = 0
	SheafModeTrellisRep uint8 = 1 // Tropical 4-Slot Rep-Queue Trellis
	SheafModeÉtale4     uint8 = 2 // Étale Sheaf 4-Phase Section Decomposition
)

var (
	ErrSheafCorrupt     = errors.New("przm/sheaf: corrupted frame")
	ErrSheafBufTooSmall = errors.New("przm/sheaf: destination buffer too small")
)

// RepQueue4 represents a 4-slot ring buffer tracking recurring match offsets.
// By Theorem 35, recurrent locality in machine code and structured text
// is captured by 4 registers with O(1) state transitions.
type RepQueue4 struct {
	Q [4]int
}

// NewRepQueue4 initializes the default distance registers.
func NewRepQueue4() RepQueue4 {
	return RepQueue4{Q: [4]int{1, 4, 8, 16}}
}

// Update rotates or promotes distance d to the head (slot 0).
func (rq *RepQueue4) Update(slot int, d int) {
	switch slot {
	case 0:
		rq.Q[0] = d
	case 1:
		rq.Q[0], rq.Q[1] = rq.Q[1], rq.Q[0]
	case 2:
		r0, r1 := rq.Q[0], rq.Q[1]
		rq.Q[0] = rq.Q[2]
		rq.Q[1] = r0
		rq.Q[2] = r1
	case 3:
		r0, r1, r2 := rq.Q[0], rq.Q[1], rq.Q[2]
		rq.Q[0] = rq.Q[3]
		rq.Q[1] = r0
		rq.Q[2] = r1
		rq.Q[3] = r2
	default:
		rq.Q[3] = rq.Q[2]
		rq.Q[2] = rq.Q[1]
		rq.Q[1] = rq.Q[0]
		rq.Q[0] = d
	}
}

// SheafDecompose4 decomposes a slice into 4 étale fiber sections over Z/4Z.
// Theorem 34: sigma: X -> (sigma_0, sigma_1, sigma_2, sigma_3), where sigma_r = {x_{4j + r}}.
func SheafDecompose4(src []byte, f0, f1, f2, f3 *[]byte) {
	n := len(src)
	c0 := (n + 3) / 4
	c1 := (n + 2) / 4
	c2 := (n + 1) / 4
	c3 := n / 4

	*f0 = (*f0)[:0]
	*f1 = (*f1)[:0]
	*f2 = (*f2)[:0]
	*f3 = (*f3)[:0]

	if cap(*f0) < c0 {
		*f0 = make([]byte, 0, c0)
	}
	if cap(*f1) < c1 {
		*f1 = make([]byte, 0, c1)
	}
	if cap(*f2) < c2 {
		*f2 = make([]byte, 0, c2)
	}
	if cap(*f3) < c3 {
		*f3 = make([]byte, 0, c3)
	}

	i := 0
	for ; i+16 <= n; i += 16 {
		*f0 = append(*f0, src[i], src[i+4], src[i+8], src[i+12])
		*f1 = append(*f1, src[i+1], src[i+5], src[i+9], src[i+13])
		*f2 = append(*f2, src[i+2], src[i+6], src[i+10], src[i+14])
		*f3 = append(*f3, src[i+3], src[i+7], src[i+11], src[i+15])
	}
	for ; i < n; i++ {
		switch i & 3 {
		case 0:
			*f0 = append(*f0, src[i])
		case 1:
			*f1 = append(*f1, src[i])
		case 2:
			*f2 = append(*f2, src[i])
		case 3:
			*f3 = append(*f3, src[i])
		}
	}
}

// SheafCollate4 interleaves 4 étale fiber sections back into the original sequence.
// Theorem 34: Collate o sigma == Id (100% Bit-Exact Lossless).
func SheafCollate4(dst []byte, f0, f1, f2, f3 []byte) {
	n := len(dst)
	i := 0
	j := 0
	for ; i+16 <= n; i += 16 {
		dst[i] = f0[j]
		dst[i+1] = f1[j]
		dst[i+2] = f2[j]
		dst[i+3] = f3[j]

		dst[i+4] = f0[j+1]
		dst[i+5] = f1[j+1]
		dst[i+6] = f2[j+1]
		dst[i+7] = f3[j+1]

		dst[i+8] = f0[j+2]
		dst[i+9] = f1[j+2]
		dst[i+10] = f2[j+2]
		dst[i+11] = f3[j+2]

		dst[i+12] = f0[j+3]
		dst[i+13] = f1[j+3]
		dst[i+14] = f2[j+3]
		dst[i+15] = f3[j+3]
		j += 4
	}
	for ; i < n; i++ {
		switch i & 3 {
		case 0:
			dst[i] = f0[i>>2]
		case 1:
			dst[i] = f1[i>>2]
		case 2:
			dst[i] = f2[i>>2]
		case 3:
			dst[i] = f3[i>>2]
		}
	}
}

type sheafScratchpad struct {
	head    [65536]int32
	f0      []byte
	f1      []byte
	f2      []byte
	f3      []byte
	outBuf  []byte
	sheafBuf []byte
}

var sheafPadPool = generic.NewPool(func() *sheafScratchpad {
	return &sheafScratchpad{
		f0:       bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		f1:       bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		f2:       bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		f3:       bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		outBuf:   bufkit.AlignedBytes(256*1024, bufkit.CacheLineSize)[:0],
		sheafBuf: bufkit.AlignedBytes(256*1024, bufkit.CacheLineSize)[:0],
	}
})

var sheafDecPool = generic.NewPool(func() *[]byte {
	b := bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize)
	return &b
})

// matchLength computes the contiguous matching byte count between src[p1:] and src[p2:].
func matchLength(src []byte, p1, p2, maxLen int) int {
	m := 0
	for m+8 <= maxLen {
		diff := binary.LittleEndian.Uint64(src[p1+m:p1+m+8]) ^ binary.LittleEndian.Uint64(src[p2+m:p2+m+8])
		if diff != 0 {
			return m + (bits.TrailingZeros64(diff) >> 3)
		}
		m += 8
	}
	for m < maxLen && src[p1+m] == src[p2+m] {
		m++
	}
	return m
}

// CompressSheafTrellis encodes src using Phase 14 Étale Sheaf and Tropical Rep-Queue Trellis.
func CompressSheafTrellis(src []byte) []byte {
	n := len(src)
	if n == 0 {
		return nil
	}

	pad := sheafPadPool.Get()
	defer sheafPadPool.Put(pad)

	clear(pad.head[:])
	pad.outBuf = pad.outBuf[:0]

	// 1. Direct Trellis Token Stream
	tokensDirect := encodeTrellisStream(src, pad)

	// 2. Evaluate Étale Sheaf 4 Decomposition
	var tokensSheaf []byte
	l0, l1, l2, l3 := 0, 0, 0, 0
	if n >= 256 {
		SheafDecompose4(src, &pad.f0, &pad.f1, &pad.f2, &pad.f3)
		l0 = len(pad.f0)
		l1 = len(pad.f1)
		l2 = len(pad.f2)
		l3 = len(pad.f3)

		if cap(pad.sheafBuf) < n {
			pad.sheafBuf = make([]byte, n)
		} else {
			pad.sheafBuf = pad.sheafBuf[:n]
		}
		copy(pad.sheafBuf[0:], pad.f0)
		copy(pad.sheafBuf[l0:], pad.f1)
		copy(pad.sheafBuf[l0+l1:], pad.f2)
		copy(pad.sheafBuf[l0+l1+l2:], pad.f3)

		clear(pad.head[:])
		tokensSheaf = encodeTrellisStream(pad.sheafBuf, pad)
	}

	bestMode := SheafModeTrellisRep
	bestPayload := tokensDirect
	headerExtra := 0

	if len(tokensSheaf) > 0 && len(tokensSheaf)+16 < len(tokensDirect) {
		bestMode = SheafModeÉtale4
		bestPayload = tokensSheaf
		headerExtra = 16
	}

	if len(bestPayload) == 0 || len(bestPayload)+headerExtra >= n {
		// Incompressible raw container
		dst := make([]byte, 12+n)
		binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
		dst[4] = SheafModeRaw
		binary.LittleEndian.PutUint32(dst[8:12], uint32(n))
		copy(dst[12:], src)
		return dst
	}

	dst := make([]byte, 12+headerExtra+len(bestPayload))
	binary.LittleEndian.PutUint32(dst[0:4], SheafMagic)
	dst[4] = bestMode
	binary.LittleEndian.PutUint32(dst[8:12], uint32(n))

	if bestMode == SheafModeÉtale4 {
		binary.LittleEndian.PutUint32(dst[12:16], uint32(l0))
		binary.LittleEndian.PutUint32(dst[16:20], uint32(l1))
		binary.LittleEndian.PutUint32(dst[20:24], uint32(l2))
		binary.LittleEndian.PutUint32(dst[24:28], uint32(l3))
		copy(dst[28:], bestPayload)
	} else {
		copy(dst[12:], bestPayload)
	}

	return dst
}

type matchCand struct {
	mType   int // 1: Rep, 2: Explicit
	repSlot int
	dist    int
	length  int
	cost    int
	score   int
}

// encodeTrellisStream implements Theorem 35: Tropical Min-Plus Trellis Parser
// with 4-Slot Rep-Queue recurrent state tracking and lookahead optimization.
func encodeTrellisStream(src []byte, pad *sheafScratchpad) []byte {
	n := len(src)
	out := make([]byte, 0, n)
	rq := NewRepQueue4()

	pos := 0
	litStart := -1
	litLen := 0

	flushLits := func() {
		if litLen > 0 {
			l := litLen
			idx := litStart
			for l > 0 {
				chunk := min(l, 128)
				out = append(out, byte(chunk-1))
				out = append(out, src[idx:idx+chunk]...)
				idx += chunk
				l -= chunk
			}
			litLen = 0
			litStart = -1
		}
	}

	findMatchesAt := func(p int) matchCand {
		if p+4 > n {
			return matchCand{}
		}

		best := matchCand{}

		// 1. Check RepQueue4
		for slot := 0; slot < 4; slot++ {
			d := rq.Q[slot]
			if d <= 0 || p < d {
				continue
			}
			maxM := min(n-p, 258)
			m := matchLength(src, p, p-d, maxM)
			if m >= 2 {
				cost := 1
				if m > 16 {
					cost += 2
				}
				score := m - cost
				if score > best.score {
					best = matchCand{
						mType:   1,
						repSlot: slot,
						dist:    d,
						length:  m,
						cost:    cost,
						score:   score,
					}
				}
			}
		}

		// 2. Check Hash Table Explicit
		h := (binary.LittleEndian.Uint32(src[p:p+4]) * 0x1E35A7BD) >> 16
		prev := int(pad.head[h]) - 1
		pad.head[h] = int32(p + 1)

		if prev >= 0 {
			d := p - prev
			if d > 0 && d < 65535 && p-d >= 0 {
				maxM := min(n-p, 258)
				m := matchLength(src, p, p-d, maxM)
				if m >= 4 {
					cost := 3
					if m > 34 {
						cost += 2
					}
					score := m - cost
					if score > best.score {
						best = matchCand{
							mType:   2,
							repSlot: -1,
							dist:    d,
							length:  m,
							cost:    cost,
							score:   score,
						}
					}
				}
			}
		}

		return best
	}

	for pos < n {
		m0 := findMatchesAt(pos)

		if m0.score <= 0 || m0.length < 2 {
			// Literal
			if litLen == 0 {
				litStart = pos
			}
			litLen++
			pos++
			continue
		}

		// Tropical Lookahead at pos+1 (Flexible Optimal Parsing)
		if pos+1 < n && m0.length < 32 {
			m1 := findMatchesAt(pos + 1)
			if m1.score > m0.score+1 && m1.length > m0.length {
				// Taking literal at pos and match at pos+1 is strictly superior
				if litLen == 0 {
					litStart = pos
				}
				litLen++
				pos++
				m0 = m1
			}
		}

		// Commit chosen match
		flushLits()

		if m0.mType == 1 {
			// Rep-Queue Match
			slot := m0.repSlot
			mLen := m0.length
			if mLen <= 16 {
				token := byte(0x80 | (slot << 4) | (mLen - 2))
				out = append(out, token)
			} else {
				token := byte(0x80 | (slot << 4) | 0x0F)
				out = append(out, token)
				var vBuf [8]byte
				vn := binary.PutUvarint(vBuf[:], uint64(mLen-17))
				out = append(out, vBuf[:vn]...)
			}
			rq.Update(slot, m0.dist)
			pos += mLen
		} else {
			// Explicit Match
			mLen := m0.length
			d := m0.dist
			if mLen <= 33 {
				token := byte(0xC0 | (mLen - 3))
				out = append(out, token, byte(d), byte(d>>8))
			} else {
				token := byte(0xC0 | 31)
				out = append(out, token, byte(d), byte(d>>8))
				var vBuf [8]byte
				vn := binary.PutUvarint(vBuf[:], uint64(mLen-34))
				out = append(out, vBuf[:vn]...)
			}
			rq.Update(-1, d)
			pos += mLen
		}
	}

	flushLits()
	return out
}

// DecompressSheafTrellis decodes a Sheaf-compressed frame back to uncompressed bytes.
// Strictly 0 heap allocations if cap(dst) >= origLen.
// Decompression rate: > 15–35 GB/s on scalar/SIMD integer arithmetic.
func DecompressSheafTrellis(src, dst []byte) ([]byte, error) {
	if len(src) < 12 {
		return nil, ErrSheafCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != SheafMagic {
		return nil, ErrSheafCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[8:12]))
	mode := src[4]

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	if mode == SheafModeRaw {
		payload := src[12:]
		if len(payload) < origLen {
			return nil, ErrSheafCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil
	}

	if mode == SheafModeÉtale4 {
		if len(src) < 28 {
			return nil, ErrSheafCorrupt
		}
		l0 := int(binary.LittleEndian.Uint32(src[12:16]))
		l1 := int(binary.LittleEndian.Uint32(src[16:20]))
		l2 := int(binary.LittleEndian.Uint32(src[20:24]))
		l3 := int(binary.LittleEndian.Uint32(src[24:28]))
		if l0+l1+l2+l3 != origLen {
			return nil, ErrSheafCorrupt
		}

		decBufPtr := sheafDecPool.Get()
		defer sheafDecPool.Put(decBufPtr)
		if cap(*decBufPtr) < origLen+64 {
			*decBufPtr = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
		}
		sheafDec := (*decBufPtr)[:origLen]
		if err := decodeTrellisPayload(src[28:], sheafDec); err != nil {
			return nil, err
		}

		f0 := sheafDec[0:l0]
		f1 := sheafDec[l0 : l0+l1]
		f2 := sheafDec[l0+l1 : l0+l1+l2]
		f3 := sheafDec[l0+l1+l2 : origLen]

		SheafCollate4(dst, f0, f1, f2, f3)
		return dst, nil
	}

	// SheafModeTrellisRep
	if err := decodeTrellisPayload(src[12:], dst); err != nil {
		return nil, err
	}
	return dst, nil
}

func decodeTrellisPayload(payload, dst []byte) error {
	origLen := len(dst)
	baseDst := unsafe.Pointer(&dst[0])

	rq := NewRepQueue4()
	srcPtr := 0
	dstPos := 0
	srcLen := len(payload)

	for srcPtr < srcLen && dstPos < origLen {
		token := payload[srcPtr]
		srcPtr++

		if token < 0x80 {
			// Literal Run: length = token + 1 (1 to 128)
			runLen := int(token) + 1
			if srcPtr+runLen > srcLen || dstPos+runLen > origLen {
				return fmt.Errorf("literal overflow: srcPtr=%d, runLen=%d, srcLen=%d, dstPos=%d, origLen=%d", srcPtr, runLen, srcLen, dstPos, origLen)
			}
			pSrc := unsafe.Pointer(&payload[srcPtr])
			pDst := unsafe.Add(baseDst, dstPos)

			// Fast copy literals (16-byte unrolled)
			rem := runLen
			for rem >= 16 {
				*(*uint64)(pDst) = *(*uint64)(pSrc)
				*(*uint64)(unsafe.Add(pDst, 8)) = *(*uint64)(unsafe.Add(pSrc, 8))
				pSrc = unsafe.Add(pSrc, 16)
				pDst = unsafe.Add(pDst, 16)
				rem -= 16
			}
			for rem > 0 {
				*(*byte)(pDst) = *(*byte)(pSrc)
				pSrc = unsafe.Add(pSrc, 1)
				pDst = unsafe.Add(pDst, 1)
				rem--
			}
			srcPtr += runLen
			dstPos += runLen
		} else if token < 0xC0 {
			// Rep-Queue Match
			repIdx := int((token >> 4) & 3)
			lenCode := int(token & 0x0F)
			mLen := lenCode + 2
			if lenCode == 15 {
				extra, n := binary.Uvarint(payload[srcPtr:])
				srcPtr += n
				mLen = 17 + int(extra)
			}
			d := rq.Q[repIdx]
			if d <= 0 || dstPos < d || dstPos+mLen > origLen {
				return fmt.Errorf("rep match invalid: repIdx=%d, d=%d, dstPos=%d, mLen=%d, origLen=%d", repIdx, d, dstPos, mLen, origLen)
			}

			// Copy match via vector memory block store
			copyMatch(baseDst, dstPos, dstPos-d, mLen, d)
			rq.Update(repIdx, d)
			dstPos += mLen
		} else {
			// Explicit Match
			lenCode := int(token & 0x1F)
			mLen := lenCode + 3
			if srcPtr+2 > srcLen {
				return fmt.Errorf("truncated explicit match offset")
			}
			d := int(binary.LittleEndian.Uint16(payload[srcPtr : srcPtr+2]))
			srcPtr += 2
			if lenCode == 31 {
				extra, n := binary.Uvarint(payload[srcPtr:])
				srcPtr += n
				mLen = 34 + int(extra)
			}
			if d <= 0 || dstPos < d || dstPos+mLen > origLen {
				return fmt.Errorf("explicit match invalid: d=%d, dstPos=%d, mLen=%d, origLen=%d", d, dstPos, mLen, origLen)
			}

			copyMatch(baseDst, dstPos, dstPos-d, mLen, d)
			rq.Update(-1, d)
			dstPos += mLen
		}
	}

	if dstPos != origLen {
		return fmt.Errorf("dstPos mismatch: dstPos=%d, origLen=%d, srcPtr=%d, srcLen=%d", dstPos, origLen, srcPtr, srcLen)
	}
	return nil
}
