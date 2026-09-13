// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"fmt"
	"math"
)

// ChannelBreakdown contains metrics and statistics for each channel in a PRZM frame.
type ChannelBreakdown struct {
	UncompressedSize int
	CompressedSize   int
	Ratio            float64
	Flags            uint8
	Stride           int

	// Section byte counts
	HeaderSize    int
	DirectorySize int
	CmdSize       int
	OffsetSize    int
	LiteralSize   int

	// Section percentages
	HeaderPct  float64
	CmdPct     float64
	OffsetPct  float64
	LiteralPct float64

	// Command category counts
	Cat00RepeatCount int
	Cat01StrideCount int
	Cat10ExtCount    int
	Cat10RLECount    int
	Cat10Delta1Count int
	Cat10Delta2Count int
	Cat11LitRunCount int

	TotalMatches    int
	TotalMatchBytes int
	TotalLitBytes   int

	// Shannon Entropy of Section 4 (Literals + Deltas) in bits per byte [0..8]
	LiteralEntropy float64
	// Theoretical size of Section 4 if compressed with ideal Micro-tANS / FSE
	PotentialLiteralBytes int
	PotentialGainBytes    int
	PotentialRatio        float64
}

// AnalyzeFrame inspects a compressed PRZM frame and extracts full channel statistics and entropy.
func AnalyzeFrame(compressed []byte) (*ChannelBreakdown, error) {
	if len(compressed) < HeaderSize {
		return nil, fmt.Errorf("przm: frame buffer too small (%d bytes)", len(compressed))
	}

	hdr, err := ParseFrameHeader(compressed)
	if err != nil {
		return nil, err
	}

	b := &ChannelBreakdown{
		UncompressedSize: hdr.UncompressedSize,
		CompressedSize:   len(compressed),
		Flags:            hdr.Flags,
		Stride:           hdr.Stride,
		HeaderSize:       HeaderSize,
	}
	if b.CompressedSize > 0 && b.UncompressedSize > 0 {
		b.Ratio = float64(b.UncompressedSize) / float64(b.CompressedSize)
	}

	if (hdr.Flags&FlagUncompressed) != 0 || hdr.UncompressedSize == 0 {
		return b, nil
	}

	sec1Offset := hdr.DataOffset
	sec1Size := 0
	if sec1Offset < len(compressed) {
		if sec1Offset+8 <= len(compressed) {
			exceptionCount := int(compressed[sec1Offset+5])
			sec1Size = 8 + 2*exceptionCount
		}
	}

	sec2Offset := sec1Offset + sec1Size
	sec3Offset := sec2Offset + hdr.CmdStreamSize
	sec4Offset := sec3Offset + hdr.OffsetStreamSize

	if sec4Offset > len(compressed) {
		return nil, fmt.Errorf("%w: frame truncated before literal section", ErrCorruptedFrame)
	}

	b.DirectorySize = sec2Offset - HeaderSize
	b.CmdSize = hdr.CmdStreamSize
	b.OffsetSize = hdr.OffsetStreamSize
	b.LiteralSize = len(compressed) - sec4Offset

	totalLen := float64(len(compressed))
	b.HeaderPct = float64(b.HeaderSize+b.DirectorySize) / totalLen * 100.0
	b.CmdPct = float64(b.CmdSize) / totalLen * 100.0
	b.OffsetPct = float64(b.OffsetSize) / totalLen * 100.0
	b.LiteralPct = float64(b.LiteralSize) / totalLen * 100.0

	cmdBuf := compressed[sec2Offset : sec2Offset+hdr.CmdStreamSize]
	literalBuf := compressed[sec4Offset:]

	// Walk command stream and tally opcodes
	cmdPtr := 0
	for cmdPtr < len(cmdBuf) {
		op := cmdBuf[cmdPtr]
		cmdPtr++

		if op < 0x80 {
			// Fast path 1: Fused Repeat Token (00 LLL MM KK)
			litLen := int((op >> 4) & 0x07)
			matchLen := int((op>>2)&0x03) + 4
			b.Cat00RepeatCount++
			b.TotalMatches++
			b.TotalMatchBytes += matchLen
			b.TotalLitBytes += litLen
			continue
		}

		if op < 0xC0 {
			// Fast path 2: Fused Extended Match (10 LL MMMM)
			litLen := int((op >> 4) & 0x03)
			baseLen := int(op & 0x0F)
			matchLen := baseLen + 4
			if baseLen == 15 {
				extra, n := binary.Uvarint(cmdBuf[cmdPtr:])
				cmdPtr += n
				matchLen = 19 + int(extra)
			}
			b.Cat10ExtCount++
			b.TotalMatches++
			b.TotalMatchBytes += matchLen
			b.TotalLitBytes += litLen
			continue
		}

		switch {
		case op >= 0xC0 && op <= 0xC7:
			// Long Repeat (k 0..7) with Fused Literals
			token := cmdBuf[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			baseLen := int(token & 0x0F)
			matchLen := baseLen + 4
			if baseLen == 15 {
				extra, n := binary.Uvarint(cmdBuf[cmdPtr:])
				cmdPtr += n
				matchLen = 19 + int(extra)
			}
			b.Cat00RepeatCount++
			b.TotalMatches++
			b.TotalMatchBytes += matchLen
			b.TotalLitBytes += litLen

		case op >= 0xD0 && op <= 0xD7:
			// Stride Match
			token := cmdBuf[cmdPtr]
			cmdPtr++
			litLen := int((token >> 4) & 0x0F)
			matchLen := int(token&0x0F) + 4
			b.Cat01StrideCount++
			b.TotalMatches++
			b.TotalMatchBytes += matchLen
			b.TotalLitBytes += litLen

		case op >= 0xE0 && op <= 0xE7:
			// Delta-Mask Ring8
			baseLen := int(cmdBuf[cmdPtr])
			cmdPtr++
			matchLen := baseLen + 4
			if baseLen == 255 {
				extra, n := binary.Uvarint(cmdBuf[cmdPtr:])
				cmdPtr += n
				matchLen = 259 + int(extra)
			}
			b.Cat10Delta1Count++
			b.TotalMatches++
			b.TotalMatchBytes += matchLen

		case op == 0xE8:
			// Delta-Mask Explicit
			baseLen := int(cmdBuf[cmdPtr])
			cmdPtr++
			matchLen := baseLen + 4
			if baseLen == 255 {
				extra, n := binary.Uvarint(cmdBuf[cmdPtr:])
				cmdPtr += n
				matchLen = 259 + int(extra)
			}
			b.Cat10Delta2Count++
			b.TotalMatches++
			b.TotalMatchBytes += matchLen

		case op == 0xF0:
			// Pure Literal Run
			runLen, n := binary.Uvarint(cmdBuf[cmdPtr:])
			cmdPtr += n
			b.Cat11LitRunCount++
			b.TotalLitBytes += int(runLen)

		case op == 0xF1:
			// RLE Run
			rleLen, n := binary.Uvarint(cmdBuf[cmdPtr:])
			cmdPtr += n
			b.Cat10RLECount++
			b.TotalMatches++
			b.TotalMatchBytes += int(rleLen)
		}
	}

	// Compute Shannon Entropy of Literal Section: H(X) = -sum(P(x) * log2(P(x)))
	rawLiteralBuf := literalBuf
	if (hdr.Flags & FlagNibble) != 0 {
		bPtr := decLitPool.Get()
		defer decLitPool.Put(bPtr)
		if unpacked, err := unpackNibblePlanes(literalBuf, bPtr); err == nil {
			rawLiteralBuf = unpacked
		}
	}

	if len(rawLiteralBuf) > 0 {
		var freq [256]int
		for _, b := range rawLiteralBuf {
			freq[b]++
		}
		var entropy float64
		n := float64(len(rawLiteralBuf))
		for _, count := range freq {
			if count > 0 {
				p := float64(count) / n
				entropy -= p * math.Log2(p)
			}
		}
		b.LiteralEntropy = entropy
		// Theoretical bit length with ANS/FSE: (H(X) * N) / 8 bytes
		idealLitBytes := int(math.Ceil((entropy * n) / 8.0))
		b.PotentialLiteralBytes = idealLitBytes
		b.PotentialGainBytes = len(literalBuf) - idealLitBytes
		potentialCompSize := len(compressed) - b.PotentialGainBytes
		if potentialCompSize > 0 {
			b.PotentialRatio = float64(b.UncompressedSize) / float64(potentialCompSize)
		}
	}

	return b, nil
}

// SummaryString formats the breakdown as a human-readable table.
func (b *ChannelBreakdown) SummaryString() string {
	hasBCJ := (b.Flags & FlagBCJ) != 0
	hasNibble := (b.Flags & FlagNibble) != 0
	return fmt.Sprintf(`--- PRZM Frame Analysis ---
Raw Size:       %d B
Comp Size:      %d B (Ratio: %.2fx, Space Savings: %.1f%%)
Flags:          0x%02X (Stride=%d, BCJ=%t, Nibble=%t)

Channel Breakdown:
  Header & Dir: %5d B (%5.1f%%)
  Commands:     %5d B (%5.1f%%)
  Offsets:      %5d B (%5.1f%%)
  Literals:     %5d B (%5.1f%%)

Opcode Statistics:
  Cat 00 (Repeat Match):  %6d
  Cat 01 (Stride Match):  %6d
  Cat 10 (Extended Match):%6d
  Cat 10 (Delta Matches): %6d
  Cat 11 (Literal Runs):  %6d
  Total Match Bytes:      %6d
  Total Literal Bytes:    %6d

Literal Channel Entropy Analysis:
  Shannon Entropy:        %.3f bits/byte (max 8.0)
  Current Literals:       %d B
  Potential with tANS:    %d B (Estimated Gain: %d B / %.1f%%)
  Potential Ratio:        %.2fx (vs current %.2fx)
`,
		b.UncompressedSize, b.CompressedSize, b.Ratio, (1.0-1.0/maxFloat(1.0, b.Ratio))*100,
		b.Flags, b.Stride, hasBCJ, hasNibble,
		b.HeaderSize+b.DirectorySize, b.HeaderPct,
		b.CmdSize, b.CmdPct,
		b.OffsetSize, b.OffsetPct,
		b.LiteralSize, b.LiteralPct,
		b.Cat00RepeatCount, b.Cat01StrideCount, b.Cat10ExtCount,
		b.Cat10Delta1Count+b.Cat10Delta2Count,
		b.Cat11LitRunCount, b.TotalMatchBytes, b.TotalLitBytes,
		b.LiteralEntropy, b.LiteralSize, b.PotentialLiteralBytes,
		b.PotentialGainBytes, float64(b.PotentialGainBytes)/float64(max(1, b.LiteralSize))*100.0,
		b.PotentialRatio, b.Ratio,
	)
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
