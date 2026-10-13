// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 && !purego

package przm

import "unsafe"

// VectorProbeResult stores the bitmasks from an 8-way vector probe.
type VectorProbeResult struct {
	ExactMask  uint32 // Bits 0..7: valid distance and targetFP == candFP (len >= 8)
	ValidMask  uint32 // Bits 0..7: 0 < pos - cand < windowSize
	Part16Mask uint32 // Bits 0..7: valid distance and low 16 bits match (len >= 6)
	_          uint32
}

// VectorProbeResult16 stores the bitmasks from a 16-way vector probe.
type VectorProbeResult16 struct {
	ExactMask uint32 // Bits 0..15: valid distance and targetFP == candFP (len >= 8)
	ValidMask uint32 // Bits 0..15: 0 < pos - cand < windowSize
}

// VectorScanResult stores summary statistics from scanning up to 256 candidates.
type VectorScanResult struct {
	BestExactSlot int32 // Index of first candidate with exact match (-1 if none)
	BestValidSlot int32 // Index of first candidate with valid distance (-1 if none)
	ExactCount    int32 // Number of exact matches
	ValidCount    int32 // Number of valid candidates
}

//go:noescape
func vectorProbe8_AVX2(
	posBase unsafe.Pointer,
	fpBase unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	res *VectorProbeResult,
)

//go:noescape
func vectorProbe16_AVX2(
	posBase unsafe.Pointer,
	fpBase unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	res *VectorProbeResult16,
)

//go:noescape
func vectorChainScan_AVX2(
	posPtr unsafe.Pointer,
	fpPtr unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	count int32,
	res *VectorScanResult,
)

// VectorProbeResult32 stores the bitmasks from a 32-way vector probe.
type VectorProbeResult32 struct {
	ExactMask uint32 // Bits 0..31: valid distance and targetFP == candFP (len >= 8)
	ValidMask uint32 // Bits 0..31: 0 < pos - cand < windowSize
}

//go:noescape
func vectorProbe32_AVX2(
	posBase unsafe.Pointer,
	fpBase unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	res *VectorProbeResult32,
)
