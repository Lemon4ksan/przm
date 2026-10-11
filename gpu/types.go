// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpu

// Window size limits and matching bounds
const (
	Window64K     = 64 * 1024
	Window1M      = 1024 * 1024
	Window8M      = 8 * 1024 * 1024
	MinMatchLen   = 4
	MaxMatchLen32 = 4095
	MaxOffset32   = 1048575
	MaxMatchLen64 = 65535
)

// LZ77Match represents an unpacked dictionary match.
type LZ77Match struct {
	Position uint32
	Offset   uint32
	Length   uint16
	Literal  byte
}

// PackMatch32 packs length (12 bits) and offset (20 bits) into a 32-bit word.
func PackMatch32(length uint16, offset uint32) uint32 {
	if length < MinMatchLen || offset == 0 {
		return 0
	}
	l := min(uint32(length), MaxMatchLen32)
	return (l << 20) | (offset & MaxOffset32)
}

// UnpackMatch32 extracts length and offset from a 32-bit match word.
func UnpackMatch32(desc uint32) (length uint16, offset uint32) {
	return uint16(desc >> 20), desc & MaxOffset32
}

// IsMatch32 returns true if descriptor has valid match length.
func IsMatch32(desc uint32) bool {
	return (desc >> 20) >= MinMatchLen
}

// MatchDesc64 represents 64-bit packed descriptor for archival mode (up to 8MB/16MB).
type MatchDesc64 struct {
	Length   uint32
	Offset   uint32
	Literal  byte
	Flags    byte // 1: is_match
	Reserved uint16
}

// GPUProfile contains high-precision timing and PCIe DMA metrics.
type GPUProfile struct {
	IngressDMABytes           uint64
	IngressDMATimeMs          float64
	IngressDMABandwidthGBps   float64
	KernelTimeMs              float64
	InternalVRAMBandwidthGBps float64
	EgressDMABytes            uint64
	EgressDMATimeMs           float64
	EgressDMABandwidthGBps    float64
	TotalRoundTripMs          float64
	EndToEndBandwidthGBps     float64
	PCIeOverheadPercent       float64
	DeviceName                string
	SMCount                   int
	TotalVRAMBytes            uint64
	PipelinedChunks           int
}

// TokenizerProfile contains timing and throughput metrics for GPU fused tokenization.
type TokenizerProfile struct {
	KernelTimeMs     float64
	Pass1Ms          float64
	Pass2Ms          float64
	Pass3Ms          float64
	TotalRoundTripMs float64
	ThroughputGBps   float64
	CmdBytes         int
	OffsetBytes      int
	LiteralBytes     int
	NumChunks        int
}

// DeviceTokenStreams holds zero-copy device VRAM pointers and length metadata
// enabling downstream GPU stages (Tensor-ANS, Stream-Citadel) to consume
// tokenized outputs directly in VRAM without host round-trip synchronization.
type DeviceTokenStreams struct {
	DFinalCmds uint64  // Device pointer to compacted command stream in VRAM
	DFinalOffs uint64  // Device pointer to compacted offset stream in VRAM
	DFinalLits uint64  // Device pointer to compacted literal stream in VRAM
	DTotals    uint64  // Device pointer to [3]uint32 {cmdLen, offLen, litLen} in VRAM
	CmdLen     uint32  // Host-cached command stream byte length
	OffLen     uint32  // Host-cached offset stream byte length
	LitLen     uint32  // Host-cached literal stream byte length
	NumChunks  uint32  // Number of 32 KiB tiles
	Stream     uintptr // CUDA stream on which kernels executed
}
