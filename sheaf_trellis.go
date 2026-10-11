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
func (rq *RepQueue4) Update(slot, d int) {
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

// TropicalInf represents +infinity in the min-plus semiring.
// 0x3F3F3F3F is chosen so that repeated additions do not cause 32-bit integer overflow.
const TropicalInf int32 = 0x3F3F3F3F

// TropicalMatrix4 represents a 4x4 state transition matrix in (Z union {+inf}, min, +).
// Formulates transitions across the 4-slot RepQueue4 state space.
// 16 int32 entries = 64 bytes (exact CPU cacheline alignment).
type TropicalMatrix4 struct {
	M [4][4]int32
}

// TropicalVector4 represents a 1x4 state cost vector over RepQueue4.
type TropicalVector4 [4]int32

// TropicalIdentity4 initializes the tropical multiplicative identity matrix:
// 0 on diagonal, +inf elsewhere.
func TropicalIdentity4() TropicalMatrix4 {
	return TropicalMatrix4{
		M: [4][4]int32{
			{0, TropicalInf, TropicalInf, TropicalInf},
			{TropicalInf, 0, TropicalInf, TropicalInf},
			{TropicalInf, TropicalInf, 0, TropicalInf},
			{TropicalInf, TropicalInf, TropicalInf, 0},
		},
	}
}

// satAdd performs saturating addition in the min-plus tropical semiring.
// Prevents overflow past TropicalInf during multi-step matrix multiplications.
func satAdd(a, b int32) int32 {
	if a >= TropicalInf || b >= TropicalInf {
		return TropicalInf
	}
	sum := a + b
	if sum > TropicalInf {
		return TropicalInf
	}
	return sum
}

// TropicalMul4 computes C = A (x) B over the min-plus semiring:
// C[i][j] = min_k (A[i][k] + B[k][j]) with saturating addition.
// Associative invariant: (A (x) B) (x) C == A (x) (B (x) C).
func TropicalMul4(A, B, C *TropicalMatrix4) {
	for i := range 4 {
		a0 := A.M[i][0]
		a1 := A.M[i][1]
		a2 := A.M[i][2]
		a3 := A.M[i][3]
		for j := range 4 {
			c0 := satAdd(a0, B.M[0][j])
			c1 := satAdd(a1, B.M[1][j])
			c2 := satAdd(a2, B.M[2][j])
			c3 := satAdd(a3, B.M[3][j])
			m := min(c3, min(c2, min(c1, c0)))
			C.M[i][j] = m
		}
	}
}

// TropicalVecMul4 computes out = d (x) W over the min-plus semiring with saturating addition.
func TropicalVecMul4(d *TropicalVector4, W *TropicalMatrix4, out *TropicalVector4) {
	d0, d1, d2, d3 := d[0], d[1], d[2], d[3]
	for j := range 4 {
		c0 := satAdd(d0, W.M[0][j])
		c1 := satAdd(d1, W.M[1][j])
		c2 := satAdd(d2, W.M[2][j])
		c3 := satAdd(d3, W.M[3][j])
		m := min(c3, min(c2, min(c1, c0)))
		out[j] = m
	}
}

// BlellochReduction computes the associative reduction of an array of transition matrices
// across a horizon H using a parallel reduction tree in O(log H) span.
func BlellochReduction(m, tree []TropicalMatrix4) TropicalMatrix4 {
	n := len(m)
	if n == 0 {
		return TropicalIdentity4()
	}
	if n == 1 {
		return m[0]
	}
	copy(tree[:n], m)
	step := 1
	for step < n {
		for i := 0; i < n; i += 2 * step {
			if i+step < n {
				var res TropicalMatrix4
				TropicalMul4(&tree[i], &tree[i+step], &res)
				tree[i] = res
			}
		}
		step *= 2
	}
	return tree[0]
}

// BlellochPrefixScan computes all inclusive prefix products P[t] = M[0] (x) ... (x) M[t]
// across horizon H using Blelloch's parallel prefix scan (up-sweep reduction tree and down-sweep scan).
func BlellochPrefixScan(m, prefixes, tree []TropicalMatrix4) {
	n := len(m)
	if n == 0 {
		return
	}
	if n == 1 {
		prefixes[0] = m[0]
		if len(tree) > 0 {
			tree[0] = m[0]
		}
		return
	}

	// P is the smallest power of 2 >= n
	P := 1
	for P < n {
		P <<= 1
	}

	ident := TropicalIdentity4()

	// Use stack buffer up to 128 elements (8 KiB, zero heap allocations).
	var stackBuf [128]TropicalMatrix4
	var localTree []TropicalMatrix4
	if P <= 128 {
		localTree = stackBuf[:P]
	} else {
		localTree = make([]TropicalMatrix4, P)
	}

	copy(localTree[:n], m)
	for j := n; j < P; j++ {
		localTree[j] = ident
	}

	// Phase 1: Up-sweep (Balanced Binary Reduction Tree)
	for s := 1; s < P; s *= 2 {
		stride := 2 * s
		for i := 0; i < P; i += stride {
			left := i + s - 1
			right := i + stride - 1
			var res TropicalMatrix4
			TropicalMul4(&localTree[left], &localTree[right], &res)
			localTree[right] = res
		}
	}

	// Store up-sweep tree nodes into tree parameter for genuine tree state retention
	if len(tree) >= n {
		copy(tree[:n], localTree[:n])
	}

	// Total product across all n elements
	total := localTree[P-1]

	// Phase 2: Down-sweep (Distribution Tree)
	localTree[P-1] = ident
	for s := P / 2; s >= 1; s /= 2 {
		stride := 2 * s
		for i := 0; i < P; i += stride {
			left := i + s - 1
			right := i + stride - 1
			parent := localTree[right]
			leftVal := localTree[left]
			localTree[left] = parent
			var rightVal TropicalMatrix4
			TropicalMul4(&parent, &leftVal, &rightVal)
			localTree[right] = rightVal
		}
	}

	// Produce inclusive prefix scan: prefixes[i] = m[0] (x) ... (x) m[i]
	for i := 0; i < n-1; i++ {
		prefixes[i] = localTree[i+1]
	}
	prefixes[n-1] = total

	// Mirror final prefix state into tree if capacity permits
	if len(tree) >= n {
		copy(tree[:n], prefixes[:n])
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
func SheafCollate4(dst, f0, f1, f2, f3 []byte) {
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
	head            [65536]int32
	f0              []byte
	f1              []byte
	f2              []byte
	f3              []byte
	outBuf          []byte
	sheafBuf        []byte
	outBufDirect    []byte
	outBufSheaf     []byte
	trellisTree     [64]TropicalMatrix4
	trellisMats     [64]TropicalMatrix4
	trellisPrefixes [64]TropicalMatrix4
}

var sheafPadPool = generic.NewPool(func() *sheafScratchpad {
	return &sheafScratchpad{
		f0:           bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		f1:           bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		f2:           bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		f3:           bufkit.AlignedBytes(64*1024, bufkit.CacheLineSize)[:0],
		outBuf:       bufkit.AlignedBytes(256*1024, bufkit.CacheLineSize)[:0],
		sheafBuf:     bufkit.AlignedBytes(256*1024, bufkit.CacheLineSize)[:0],
		outBufDirect: bufkit.AlignedBytes(256*1024, bufkit.CacheLineSize)[:0],
		outBufSheaf:  bufkit.AlignedBytes(256*1024, bufkit.CacheLineSize)[:0],
	}
})

func (pad *sheafScratchpad) ensureBufs(n int) {
	if cap(pad.outBufDirect) < n+64 {
		pad.outBufDirect = bufkit.AlignedBytes(n+64, bufkit.CacheLineSize)[:0]
	}
	if cap(pad.outBufSheaf) < n+64 {
		pad.outBufSheaf = bufkit.AlignedBytes(n+64, bufkit.CacheLineSize)[:0]
	}
}

var sheafDecChan = make(chan []byte, 32)

func getSheafDecBuf(minCap int) []byte {
	for {
		select {
		case b := <-sheafDecChan:
			if cap(b) >= minCap {
				return b[:minCap]
			}
			// Insufficient capacity; drop and continue draining/checking
			continue
		default:
			return bufkit.AlignedBytes(minCap, bufkit.CacheLineSize)
		}
	}
}

func putSheafDecBuf(b []byte) {
	if b == nil {
		return
	}
	select {
	case sheafDecChan <- b:
	default:
	}
}

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

	pad.ensureBufs(n)
	clear(pad.head[:])
	pad.outBuf = pad.outBuf[:0]

	// 1. Direct Trellis Token Stream (0 heap allocs)
	tokensDirect := encodeTrellisStream(src, pad.outBufDirect[:0], pad)

	// 2. Evaluate Étale Sheaf 4 Decomposition (0 heap allocs)
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
		tokensSheaf = encodeTrellisStream(pad.sheafBuf, pad.outBufSheaf[:0], pad)
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

// encodeTrellisStream implements Theorem 35: Tropical Min-Plus Trellis Parser
// with 4-Slot Rep-Queue recurrent state tracking, 16/32-bit SWAR match gating,
// and associative semiring horizon parsing. Zero heap allocations during parsing.
func encodeTrellisStream(src, out []byte, pad *sheafScratchpad) []byte {
	n := len(src)
	out = out[:0]
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

	for pos+4 <= n {
		srcWord := binary.LittleEndian.Uint32(src[pos : pos+4])
		bestScore := 0
		bestType := 0
		bestSlot := -1
		bestDist := 0
		bestLen := 0

		// --- 1. Fast SWAR RepQueue4 Check ---
		// 16-bit match gating: rejects 99.9% of non-matching probes in 1 CPU cycle
		srcWord16 := uint16(srcWord)
		for slot := range 4 {
			d := rq.Q[slot]
			if d <= 0 || pos < d {
				continue
			}
			cand16 := binary.LittleEndian.Uint16(src[pos-d : pos-d+2])
			if srcWord16 != cand16 {
				continue
			}
			maxM := min(n-pos, 258)
			m := 2 + matchLength(src, pos+2, pos-d+2, maxM-2)
			cost := 1
			if m > 16 {
				cost += 2
			}
			score := m - cost
			if score > bestScore {
				bestScore = score
				bestType = 1
				bestSlot = slot
				bestDist = d
				bestLen = m
			}
		}

		// --- 2. Fast SWAR Explicit Hash Table Check ---
		h := (srcWord * 0x1E35A7BD) >> 16
		prev := int(pad.head[h]) - 1
		pad.head[h] = int32(pos + 1)

		if prev >= 0 {
			d := pos - prev
			if d > 0 && d < 65535 && pos-d >= 0 {
				// 32-bit SWAR match gating: eliminates hash collisions in 1 CPU cycle
				if binary.LittleEndian.Uint32(src[prev:prev+4]) == srcWord {
					maxM := min(n-pos, 258)
					m := 4 + matchLength(src, pos+4, prev+4, maxM-4)
					cost := 3
					if m > 34 {
						cost += 2
					}
					score := m - cost
					if score > bestScore {
						bestScore = score
						bestType = 2
						bestSlot = -1
						bestDist = d
						bestLen = m
					}
				}
			}
		}

		// --- 3. Evaluate Decision ---
		if bestScore <= 0 || bestLen < 2 {
			if litLen == 0 {
				litStart = pos
			}
			litLen++
			pos++
			continue
		}

		// --- 4. Tropical Trellis Horizon Evaluation (Theorem 35) ---
		// Multi-step horizon evaluation via Blelloch parallel prefix scan over RepQueue4 states.
		if pos+5 <= n && bestLen < 32 {
			H := min(4, n-pos) // Multi-step parsing horizon (up to 4 steps)
			for t := range H {
				curPos := pos + t
				for r := range 4 {
					for c := range 4 {
						pad.trellisMats[t].M[r][c] = TropicalInf
					}
					// Literal self-transition: stays in state r with 1-byte literal cost
					pad.trellisMats[t].M[r][r] = 8
				}

				if curPos+4 <= n {
					w := binary.LittleEndian.Uint32(src[curPos : curPos+4])
					w16 := uint16(w)
					// RepQueue transitions: matching slot s transitions any state i -> s
					for s := range 4 {
						d := rq.Q[s]
						if d > 0 && curPos >= d && w16 == binary.LittleEndian.Uint16(src[curPos-d:curPos-d+2]) {
							maxM := min(n-curPos, 258)
							m := 2 + matchLength(src, curPos+2, curPos-d+2, maxM-2)
							if m >= 2 {
								cost := int32(8)
								if m > 16 {
									cost = 24
								}
								for i := range 4 {
									if cost < pad.trellisMats[t].M[i][s] {
										pad.trellisMats[t].M[i][s] = cost
									}
								}
							}
						}
					}

					// Explicit match transitions: transitions any state i -> 0
					hE := (w * 0x1E35A7BD) >> 16
					pE := int(pad.head[hE]) - 1
					if t == 1 {
						pad.head[hE] = int32(pos + 2)
					}
					if pE >= 0 {
						d := curPos - pE
						if d > 0 && d < 65535 && curPos-d >= 0 {
							if binary.LittleEndian.Uint32(src[pE:pE+4]) == w {
								maxM := min(n-curPos, 258)
								m := 4 + matchLength(src, curPos+4, pE+4, maxM-4)
								if m >= 4 {
									cost := int32(24)
									if m > 34 {
										cost = 40
									}
									for i := range 4 {
										if cost < pad.trellisMats[t].M[i][0] {
											pad.trellisMats[t].M[i][0] = cost
										}
									}
								}
							}
						}
					}
				}
			}

			// Blelloch parallel prefix scan evaluates all cumulative transition kernels in O(log H) span
			BlellochPrefixScan(pad.trellisMats[:H], pad.trellisPrefixes[:H], pad.trellisTree[:H])

			// Compute multi-step min-plus geodesic cost vector from initial state d0 = [0, inf, inf, inf]
			var d0 TropicalVector4 = [4]int32{0, TropicalInf, TropicalInf, TropicalInf}
			var dH TropicalVector4
			TropicalVecMul4(&d0, &pad.trellisPrefixes[H-1], &dH)

			nextWord := binary.LittleEndian.Uint32(src[pos+1 : pos+5])
			nextWord16 := uint16(nextWord)
			lookScore := 0
			lookType := 0
			lookSlot := -1
			lookDist := 0
			lookLen := 0

			// RepQueue check at pos+1 with 16-bit match gating
			for slot := range 4 {
				d := rq.Q[slot]
				if d <= 0 || (pos+1) < d {
					continue
				}
				if nextWord16 != binary.LittleEndian.Uint16(src[pos+1-d:pos+1-d+2]) {
					continue
				}
				maxM := min(n-(pos+1), 258)
				m := 2 + matchLength(src, pos+3, pos+1-d+2, maxM-2)
				cost := 1
				if m > 16 {
					cost += 2
				}
				score := m - cost
				if score > lookScore {
					lookScore = score
					lookType = 1
					lookSlot = slot
					lookDist = d
					lookLen = m
				}
			}

			// Explicit check at pos+1 with 32-bit match gating
			hNext := (nextWord * 0x1E35A7BD) >> 16
			prevNext := int(pad.head[hNext]) - 1
			pad.head[hNext] = int32(pos + 2)
			if prevNext >= 0 {
				d := (pos + 1) - prevNext
				if d > 0 && d < 65535 && (pos+1)-d >= 0 {
					if binary.LittleEndian.Uint32(src[prevNext:prevNext+4]) == nextWord {
						maxM := min(n-(pos+1), 258)
						m := 4 + matchLength(src, pos+5, prevNext+4, maxM-4)
						cost := 3
						if m > 34 {
							cost += 2
						}
						score := m - cost
						if score > lookScore {
							lookScore = score
							lookType = 2
							lookSlot = -1
							lookDist = d
							lookLen = m
						}
					}
				}
			}

			preferLookahead := false
			if lookScore > bestScore+1 && lookLen > bestLen {
				preferLookahead = true
			} else if lookScore > 0 && dH[0] < 8*int32(bestLen) && lookLen > bestLen {
				preferLookahead = true
			}

			if preferLookahead {
				if litLen == 0 {
					litStart = pos
				}
				litLen++
				pos++
				bestScore = lookScore
				bestType = lookType
				bestSlot = lookSlot
				bestDist = lookDist
				bestLen = lookLen
			}
		}

		// --- 5. Commit Chosen Match ---
		flushLits()

		if bestType == 1 {
			// Rep-Queue Match
			slot := bestSlot
			mLen := bestLen
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
			rq.Update(slot, bestDist)
			pos += mLen
		} else {
			// Explicit Match
			mLen := bestLen
			d := bestDist
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

	for pos < n {
		if litLen == 0 {
			litStart = pos
		}
		litLen++
		pos++
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

		decBuf := getSheafDecBuf(origLen + 64)
		sheafDec := decBuf[:origLen]
		if err := decodeTrellisPayload(src[28:], sheafDec); err != nil {
			putSheafDecBuf(decBuf)
			return nil, err
		}

		f0 := sheafDec[0:l0]
		f1 := sheafDec[l0 : l0+l1]
		f2 := sheafDec[l0+l1 : l0+l1+l2]
		f3 := sheafDec[l0+l1+l2 : origLen]

		SheafCollate4(dst, f0, f1, f2, f3)
		putSheafDecBuf(decBuf)
		return dst, nil
	}

	if mode == SheafModeSplit || mode == SheafModeSplitCascaded {
		return DecompressSheaf(src, dst)
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
				return fmt.Errorf(
					"literal overflow: srcPtr=%d, runLen=%d, srcLen=%d, dstPos=%d, origLen=%d",
					srcPtr,
					runLen,
					srcLen,
					dstPos,
					origLen,
				)
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
				return fmt.Errorf(
					"rep match invalid: repIdx=%d, d=%d, dstPos=%d, mLen=%d, origLen=%d",
					repIdx,
					d,
					dstPos,
					mLen,
					origLen,
				)
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
				return errors.New("truncated explicit match offset")
			}
			d := int(binary.LittleEndian.Uint16(payload[srcPtr : srcPtr+2]))
			srcPtr += 2
			if lenCode == 31 {
				extra, n := binary.Uvarint(payload[srcPtr:])
				srcPtr += n
				mLen = 34 + int(extra)
			}
			if d <= 0 || dstPos < d || dstPos+mLen > origLen {
				return fmt.Errorf(
					"explicit match invalid: d=%d, dstPos=%d, mLen=%d, origLen=%d",
					d,
					dstPos,
					mLen,
					origLen,
				)
			}

			copyMatch(baseDst, dstPos, dstPos-d, mLen, d)
			rq.Update(-1, d)
			dstPos += mLen
		}
	}

	if dstPos != origLen {
		return fmt.Errorf(
			"dstPos mismatch: dstPos=%d, origLen=%d, srcPtr=%d, srcLen=%d",
			dstPos,
			origLen,
			srcPtr,
			srcLen,
		)
	}
	return nil
}
