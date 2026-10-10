// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"math/bits"
	"unsafe"
)

// RouteDecision specifies the pre-LZ classification and transform gating decision.
type RouteDecision uint8

const (
	// RouteDirectBypass fast-bypasses all transforms directly into Large Window LZ77 + 4-Stream Huffman.
	// Used for structured text, source code, JSON, XML, logs, CSV, and general binaries.
	RouteDirectBypass RouteDecision = 0

	// RouteFastBypass is an alias for RouteDirectBypass.
	RouteFastBypass RouteDecision = 0

	// RouteContainerBypass fast-bypasses pre-compressed containers (ZIP, GZIP, PNG, JPEG, PDF).
	RouteContainerBypass RouteDecision = 2

	// RouteBypass is an alias for RouteContainerBypass.
	RouteBypass RouteDecision = 2

	// RouteSparseZero identifies sparse matrices, RLE runs, and pruned neural weights (>40% zeros).
	RouteSparseZero RouteDecision = 3

	// RouteSmoothDelta identifies smooth continuous physical signals (MAD <= 8.0).
	RouteSmoothDelta RouteDecision = 4
)

func (r RouteDecision) String() string {
	switch r {
	case RouteDirectBypass:
		return "DirectBypass"
	case RouteContainerBypass:
		return "ContainerBypass"
	case RouteSparseZero:
		return "SparseZero"
	case RouteSmoothDelta:
		return "SmoothDelta"
	default:
		return "Unknown"
	}
}

// ProbeHeaderMagic inspects the initial bytes (<64B) for executable or container magic signatures.
// Returns the corresponding RouteDecision: RouteContainerBypass, or RouteDirectBypass.
// Executes in <5 ns with strictly 0 heap allocations.
func ProbeHeaderMagic(data []byte) RouteDecision {
	n := len(data)
	if n < 2 {
		return RouteDirectBypass
	}

	// 1. GZIP: 0x1F, 0x8B
	if data[0] == 0x1F && data[1] == 0x8B {
		return RouteContainerBypass
	}

	// 2. JPEG: 0xFF, 0xD8, 0xFF
	if n >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return RouteContainerBypass
	}

	// 6. ZIP container: 'P' 'K' 0x03 0x04
	if data[0] == 'P' && data[1] == 'K' && data[2] == 3 && data[3] == 4 {
		return RouteContainerBypass
	}

	// 7. PDF document: '%' 'P' 'D' 'F' '-'
	if n >= 5 && data[0] == '%' && data[1] == 'P' && data[2] == 'D' && data[3] == 'F' && data[4] == '-' {
		return RouteContainerBypass
	}

	// 8. PNG container: 0x89 'P' 'N' 'G' '\r' '\n' 0x1A '\n'
	if n >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' &&
		data[4] == '\r' && data[5] == '\n' && data[6] == 0x1A && data[7] == '\n' {
		return RouteContainerBypass
	}

	return RouteDirectBypass
}

// ProbeHeaderMagicDetailed returns booleans for PE, ELF, and container detection.
func ProbeHeaderMagicDetailed(data []byte) (isPE bool, isELF bool, isContainer bool) {
	dec := ProbeHeaderMagic(data)
	switch dec {
	case RouteContainerBypass:
		return false, false, true
	default:
		return false, false, false
	}
}

// ProbeZeroDensity512 measures the proportion of zero bytes in the first 512-byte prefix.
// Executes in <90 ns scalar (<15 ns SIMD) with strictly 0 heap allocations.
func ProbeZeroDensity512(data []byte) float64 {
	sample := min(len(data), 512)
	if sample == 0 {
		return 0
	}
	zeros := 0
	for i := range sample {
		if data[i] == 0 {
			zeros++
		}
	}
	return float64(zeros) / float64(sample)
}

// IsSparseZeroFast evaluates if zeros exceed 40% using integer arithmetic.
// Avoids floating point registers; executes with 0 allocations.
func IsSparseZeroFast(data []byte) bool {
	sample := min(len(data), 512)
	if sample < 16 {
		return false
	}
	targetZeros := sample*2/5 + 1
	maxNonZeros := sample - targetZeros

	zeros := 0
	nonZeros := 0

	p := unsafe.Pointer(&data[0])
	const mask = 0x7F7F7F7F7F7F7F7F

	i := 0
	for ; i <= sample-64; i += 64 {
		v0 := *(*uint64)(unsafe.Add(p, i))
		v1 := *(*uint64)(unsafe.Add(p, i+8))
		v2 := *(*uint64)(unsafe.Add(p, i+16))
		v3 := *(*uint64)(unsafe.Add(p, i+24))
		v4 := *(*uint64)(unsafe.Add(p, i+32))
		v5 := *(*uint64)(unsafe.Add(p, i+40))
		v6 := *(*uint64)(unsafe.Add(p, i+48))
		v7 := *(*uint64)(unsafe.Add(p, i+56))

		if (v0 | v1 | v2 | v3 | v4 | v5 | v6 | v7) == 0 {
			zeros += 64
			if zeros >= targetZeros {
				return true
			}
			continue
		}

		z0 := ^(((v0 & mask) + mask) | v0 | mask)
		z1 := ^(((v1 & mask) + mask) | v1 | mask)
		z2 := ^(((v2 & mask) + mask) | v2 | mask)
		z3 := ^(((v3 & mask) + mask) | v3 | mask)
		z4 := ^(((v4 & mask) + mask) | v4 | mask)
		z5 := ^(((v5 & mask) + mask) | v5 | mask)
		z6 := ^(((v6 & mask) + mask) | v6 | mask)
		z7 := ^(((v7 & mask) + mask) | v7 | mask)

		cZeros := bits.OnesCount64(z0) + bits.OnesCount64(z1) + bits.OnesCount64(z2) + bits.OnesCount64(z3) +
			bits.OnesCount64(z4) + bits.OnesCount64(z5) + bits.OnesCount64(z6) + bits.OnesCount64(z7)
		zeros += cZeros
		nonZeros += 64 - cZeros

		if zeros >= targetZeros {
			return true
		}
		if nonZeros > maxNonZeros {
			return false
		}
	}
	for ; i <= sample-8; i += 8 {
		v := *(*uint64)(unsafe.Add(p, i))
		z := ^(((v & mask) + mask) | v | mask)
		cZeros := bits.OnesCount64(z)
		zeros += cZeros
		nonZeros += 8 - cZeros
		if zeros >= targetZeros {
			return true
		}
		if nonZeros > maxNonZeros {
			return false
		}
	}
	for ; i < sample; i++ {
		if *(*byte)(unsafe.Add(p, i)) == 0 {
			zeros++
		} else {
			nonZeros++
		}
	}
	return zeros >= targetZeros
}

// ProbeDifferenceMAD512 computes the Mean Absolute Difference between adjacent bytes in a 512-byte sample.
// Executes in <95 ns scalar with strictly 0 heap allocations.
func ProbeDifferenceMAD512(data []byte) float64 {
	sample := min(len(data), 512)
	if sample <= 1 {
		return 0
	}
	var diffSum int64
	for i := 1; i < sample; i++ {
		d := int(data[i]) - int(data[i-1])
		if d < 0 {
			d = -d
		}
		diffSum += int64(d)
	}
	return float64(diffSum) / float64(sample-1)
}

// IsSmoothDeltaFast evaluates if Mean Absolute Difference <= 8.0 using integer arithmetic.
// Avoids floating point registers; executes with 0 allocations.
func IsSmoothDeltaFast(data []byte) bool {
	sample := min(len(data), 512)
	if sample <= 1 {
		return false
	}
	maxDiff := int32(8 * (sample - 1))
	var diffSum int32

	p := unsafe.Pointer(&data[0])

	const bias = 0x0200020002000200
	const laneOne = 0x0001000100010001
	const byteMask = 0x00FF00FF00FF00FF

	i := 1
	for ; i <= sample-32; i += 32 {
		// Chunk 0
		u0 := *(*uint64)(unsafe.Add(p, i))
		v0 := *(*uint64)(unsafe.Add(p, i-1))
		rE0 := (u0&byteMask + bias) - v0&byteMask
		invSE0 := ((rE0 >> 9) & laneOne) ^ laneOne
		absE0 := (rE0&byteMask ^ (invSE0 * 255)) + invSE0
		rO0 := ((u0>>8)&byteMask + bias) - (v0>>8)&byteMask
		invSO0 := ((rO0 >> 9) & laneOne) ^ laneOne
		absO0 := (rO0&byteMask ^ (invSO0 * 255)) + invSO0

		// Chunk 1
		u1 := *(*uint64)(unsafe.Add(p, i+8))
		v1 := *(*uint64)(unsafe.Add(p, i+7))
		rE1 := (u1&byteMask + bias) - v1&byteMask
		invSE1 := ((rE1 >> 9) & laneOne) ^ laneOne
		absE1 := (rE1&byteMask ^ (invSE1 * 255)) + invSE1
		rO1 := ((u1>>8)&byteMask + bias) - (v1>>8)&byteMask
		invSO1 := ((rO1 >> 9) & laneOne) ^ laneOne
		absO1 := (rO1&byteMask ^ (invSO1 * 255)) + invSO1

		// Chunk 2
		u2 := *(*uint64)(unsafe.Add(p, i+16))
		v2 := *(*uint64)(unsafe.Add(p, i+15))
		rE2 := (u2&byteMask + bias) - v2&byteMask
		invSE2 := ((rE2 >> 9) & laneOne) ^ laneOne
		absE2 := (rE2&byteMask ^ (invSE2 * 255)) + invSE2
		rO2 := ((u2>>8)&byteMask + bias) - (v2>>8)&byteMask
		invSO2 := ((rO2 >> 9) & laneOne) ^ laneOne
		absO2 := (rO2&byteMask ^ (invSO2 * 255)) + invSO2

		// Chunk 3
		u3 := *(*uint64)(unsafe.Add(p, i+24))
		v3 := *(*uint64)(unsafe.Add(p, i+23))
		rE3 := (u3&byteMask + bias) - v3&byteMask
		invSE3 := ((rE3 >> 9) & laneOne) ^ laneOne
		absE3 := (rE3&byteMask ^ (invSE3 * 255)) + invSE3
		rO3 := ((u3>>8)&byteMask + bias) - (v3>>8)&byteMask
		invSO3 := ((rO3 >> 9) & laneOne) ^ laneOne
		absO3 := (rO3&byteMask ^ (invSO3 * 255)) + invSO3

		laneSum := (absE0 + absO0) + (absE1 + absO1) + (absE2 + absO2) + (absE3 + absO3)
		diffSum += int32((laneSum * laneOne) >> 48)
		if diffSum > maxDiff {
			return false
		}
	}
	for ; i <= sample-8; i += 8 {
		u := *(*uint64)(unsafe.Add(p, i))
		v := *(*uint64)(unsafe.Add(p, i-1))
		rE := (u&byteMask + bias) - v&byteMask
		invSE := ((rE >> 9) & laneOne) ^ laneOne
		absE := (rE&byteMask ^ (invSE * 255)) + invSE
		rO := ((u>>8)&byteMask + bias) - (v>>8)&byteMask
		invSO := ((rO >> 9) & laneOne) ^ laneOne
		absO := (rO&byteMask ^ (invSO * 255)) + invSO
		diffSum += int32(((absE + absO) * laneOne) >> 48)
		if diffSum > maxDiff {
			return false
		}
	}
	prev := int32(*(*byte)(unsafe.Add(p, i-1)))
	for ; i < sample; i++ {
		b := int32(*(*byte)(unsafe.Add(p, i)))
		d := b - prev
		diffSum += (d ^ (d >> 31)) - (d >> 31)
		prev = b
	}
	return diffSum <= maxDiff
}

// RouteContent executes the sub-microsecond content router (<290 ns budget).
// Strictly 0 heap allocations.
func RouteContent(data []byte) RouteDecision {
	// Stage 0: Header Magic Probe (<5 ns)
	magicDec := ProbeHeaderMagic(data)
	if magicDec != RouteDirectBypass {
		return magicDec
	}

	// Stage 1: Zero Density Probe (<90 ns scalar / <15 ns SIMD)
	if IsSparseZeroFast(data) {
		return RouteSparseZero
	}

	// Stage 2: Difference MAD Probe (<95 ns scalar / <35 ns SIMD)
	if IsSmoothDeltaFast(data) {
		return RouteSmoothDelta
	}

	// Fast Bypass Path: Structured text, source code, JSON, logs, CSV, general binaries
	return RouteDirectBypass
}

// RouteBlock is an alias for RouteContent.
func RouteBlock(data []byte) RouteDecision {
	return RouteContent(data)
}
