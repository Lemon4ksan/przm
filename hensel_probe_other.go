// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 || purego

package przm

import "unsafe"

type VectorProbeResult struct {
	ExactMask  uint32
	ValidMask  uint32
	Part16Mask uint32
	_          uint32
}

type VectorProbeResult16 struct {
	ExactMask uint32
	ValidMask uint32
}

type VectorScanResult struct {
	BestExactSlot int32
	BestValidSlot int32
	ExactCount    int32
	ValidCount    int32
}

func vectorProbe8_AVX2(
	posBase unsafe.Pointer,
	fpBase unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	res *VectorProbeResult,
) {
	positions := unsafe.Slice((*int32)(posBase), 8)
	fingerprints := unsafe.Slice((*uint32)(fpBase), 8)
	var validMask, exactMask, part16Mask uint32
	for i := 0; i < 8; i++ {
		p := positions[i]
		if p > 0 {
			d := curPos - p
			if d > 0 && d < windowSize {
				validMask |= 1 << i
				if fingerprints[i] == targetFP {
					exactMask |= 1 << i
				}
				if (fingerprints[i] & 0xFFFF) == (targetFP & 0xFFFF) {
					part16Mask |= 1 << i
				}
			}
		}
	}
	res.ValidMask = validMask
	res.ExactMask = exactMask
	res.Part16Mask = part16Mask
}

func vectorProbe16_AVX2(
	posBase unsafe.Pointer,
	fpBase unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	res *VectorProbeResult16,
) {
	positions := unsafe.Slice((*int32)(posBase), 16)
	fingerprints := unsafe.Slice((*uint32)(fpBase), 16)
	var validMask, exactMask uint32
	for i := 0; i < 16; i++ {
		p := positions[i]
		if p > 0 {
			d := curPos - p
			if d > 0 && d < windowSize {
				validMask |= 1 << i
				if fingerprints[i] == targetFP {
					exactMask |= 1 << i
				}
			}
		}
	}
	res.ValidMask = validMask
	res.ExactMask = exactMask
}

func vectorChainScan_AVX2(
	posPtr unsafe.Pointer,
	fpPtr unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	count int32,
	res *VectorScanResult,
) {
	positions := unsafe.Slice((*int32)(posPtr), count)
	fingerprints := unsafe.Slice((*uint32)(fpPtr), count)
	res.BestExactSlot = -1
	res.BestValidSlot = -1
	var exactCount, validCount int32
	for i := int32(0); i < count; i++ {
		p := positions[i]
		if p > 0 {
			d := curPos - p
			if d > 0 && d < windowSize {
				if res.BestValidSlot < 0 {
					res.BestValidSlot = i
				}
				validCount++
				if fingerprints[i] == targetFP {
					if res.BestExactSlot < 0 {
						res.BestExactSlot = i
					}
					exactCount++
				}
			}
		}
	}
	res.ExactCount = exactCount
	res.ValidCount = validCount
}


type VectorProbeResult32 struct {
	ExactMask uint32
	ValidMask uint32
}

func vectorProbe32_AVX2(
	posBase unsafe.Pointer,
	fpBase unsafe.Pointer,
	curPos int32,
	targetFP uint32,
	windowSize int32,
	res *VectorProbeResult32,
) {
	positions := unsafe.Slice((*int32)(posBase), 32)
	fingerprints := unsafe.Slice((*uint32)(fpBase), 32)
	var validMask, exactMask uint32
	for i := 0; i < 32; i++ {
		p := positions[i]
		if p > 0 {
			d := curPos - p
			if d > 0 && d < windowSize {
				validMask |= 1 << i
				if fingerprints[i] == targetFP {
					exactMask |= 1 << i
				}
			}
		}
	}
	res.ValidMask = validMask
	res.ExactMask = exactMask
}
