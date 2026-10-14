// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/lemon4ksan/przm"
)

// Entropy mode names (PRZM-SPEC-0.5 and extensions)
const (
	ModeFlat             uint8 = 0
	ModeHuffman4X        uint8 = 1
	ModeHuffmanCompact   uint8 = 2
	ModeHuffmanCompact4X uint8 = 3
	ModeRecursiveLZ      uint8 = 4
	ModeVANS4X           uint8 = 5
	ModeSplitVarint      uint8 = 6
	ModeMarkovHuffman4X  uint8 = 7
	ModeSubNibbleANS     uint8 = 8
)

func ModeName(mode uint8) string {
	switch mode {
	case ModeFlat:
		return "ModeFlat (Raw 0% CPU)"
	case ModeHuffman4X:
		return "ModeHuffman4X (4-Stream Canonical)"
	case ModeHuffmanCompact:
		return "ModeHuffmanCompact (Active Mask)"
	case ModeHuffmanCompact4X:
		return "ModeHuffmanCompact4X (4-Stream Active Mask)"
	case ModeRecursiveLZ:
		return "ModeRecursiveLZ (Self-Referential)"
	case ModeVANS4X:
		return "ModeVANS4X (Vector-State ANS)"
	case ModeSplitVarint:
		return "ModeSplitVarint (De-interleaved LEB128)"
	case ModeMarkovHuffman4X:
		return "ModeMarkovHuffman4X (3/4-State Markov)"
	case ModeSubNibbleANS:
		return "ModeSubNibbleANS (Sub-Nibble Interleaved)"
	default:
		return fmt.Sprintf("UnknownMode (0x%02X)", mode)
	}
}

// OpcodeFamily classifies command opcodes into functional groups.
type OpcodeFamily string

const (
	FamilyFusedRepeat   OpcodeFamily = "Fused Repeat (Fast-Path 1)"
	FamilyFusedExtended OpcodeFamily = "Fused Extended (Fast-Path 2)"
	FamilyLongRepeat    OpcodeFamily = "Long Repeat (k=0..3)"
	FamilyFusedBracket  OpcodeFamily = "Fused Offset Bracket"
	FamilyDiffLattice   OpcodeFamily = "Diff Lattice (Implicit Offset)"
	FamilyFusedMicro    OpcodeFamily = "Fused Micro-Token"
	FamilyMatchWalk     OpcodeFamily = "Match Walk (0xFE)"
	FamilyStride        OpcodeFamily = "Stride Match"
	FamilyDeltaMask     OpcodeFamily = "Delta-Mask"
	FamilyDictEntry     OpcodeFamily = "Schema / Dict Token"
	FamilyTransformed   OpcodeFamily = "Transformed Token"
	FamilyLiteralRun    OpcodeFamily = "Literal Run"
	FamilyRLE           OpcodeFamily = "Run-Length Encoding (RLE)"
	FamilyCopyBlock     OpcodeFamily = "Block Deduplication (0xFD)"
	FamilyOther         OpcodeFamily = "Other / Escapes"
)

// SectionDetail contains byte sizes, percentages and entropy modes for a section.
type SectionDetail struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Offset      int     `json:"offset"`
	PackedSize  int     `json:"packed_size"`
	RawSize     int     `json:"raw_size"`
	Percentage  float64 `json:"percentage"`
	ModeID      uint8   `json:"mode_id"`
	ModeName    string  `json:"mode_name"`
	Ratio       float64 `json:"ratio"`
	EntropyBits float64 `json:"entropy_bits"`
}

// OpcodeStat captures statistics for a single opcode byte.
type OpcodeStat struct {
	Opcode       byte         `json:"opcode"`
	Hex          string       `json:"hex"`
	Template     string       `json:"template"`
	Name         string       `json:"name"`
	Family       OpcodeFamily `json:"family"`
	Count        int          `json:"count"`
	Percentage   float64      `json:"percentage"`
	TotalLits    int          `json:"total_literals"`
	TotalMatches int          `json:"total_matches"`
}

// DisassembledCommand represents an individual decoded command token.
type DisassembledCommand struct {
	Offset      int          `json:"offset"`
	Opcode      byte         `json:"opcode"`
	Hex         string       `json:"hex"`
	Family      OpcodeFamily `json:"family"`
	Description string       `json:"description"`
	LiteralLen  int          `json:"literal_length"`
	MatchLen    int          `json:"match_length"`
	Distance    int          `json:"distance"`
	RepeatIndex int          `json:"repeat_index"`
}

// OffsetBucket partitions distances into logarithmic ranges.
type OffsetBucket struct {
	Label      string  `json:"label"`
	Min        int     `json:"min"`
	Max        int     `json:"max"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
}

// TimelineSegment represents a slice of the uncompressed stream.
type TimelineSegment struct {
	Start      int     `json:"start"`
	End        int     `json:"end"`
	LiteralPct float64 `json:"literal_pct"`
	MatchPct   float64 `json:"match_pct"`
	RepeatPct  float64 `json:"repeat_pct"`
}

// FrameAnalysis holds complete diagnostics for one PRZM frame.
type FrameAnalysis struct {
	FrameIndex       int      `json:"frame_index"`
	Magic            string   `json:"magic"`
	Flags            uint8    `json:"flags"`
	FlagNames        []string `json:"flag_names"`
	UncompressedSize int      `json:"uncompressed_size"`
	CompressedSize   int      `json:"compressed_size"`
	CompressionRatio float64  `json:"compression_ratio"`
	SpaceSavedPct    float64  `json:"space_saved_pct"`
	BitsPerByte      float64  `json:"bits_per_byte"`
	Stride           int      `json:"stride"`
	WindowLog        uint8    `json:"window_log"`
	WindowSize       int      `json:"window_size"`
	DictID           uint32   `json:"dict_id"`
	NormMode         uint8    `json:"norm_mode"`
	NormModeName     string   `json:"norm_mode_name"`

	// Sections
	Sections []SectionDetail `json:"sections"`

	// Opcode & Command Analysis
	TotalCommands  int                      `json:"total_commands"`
	CommandEntropy float64                  `json:"command_entropy_bits"`
	AvgBitsPerCmd  float64                  `json:"avg_bits_per_command"`
	FamilyCounts   map[OpcodeFamily]int     `json:"family_counts"`
	FamilyPct      map[OpcodeFamily]float64 `json:"family_percentages"`
	TopOpcodes     []OpcodeStat             `json:"top_opcodes"`
	Commands       []DisassembledCommand    `json:"-"` // Omit from raw json dump to keep light unless requested

	// Offset & Locality Analysis
	TotalMatches    int            `json:"total_matches"`
	RepeatHits      int            `json:"repeat_hits"`
	RepeatHitsPct   float64        `json:"repeat_hits_pct"`
	ExplicitHits    int            `json:"explicit_hits"`
	ExplicitHitsPct float64        `json:"explicit_hits_pct"`
	BracketHits     int            `json:"bracket_hits"`
	BracketHitsPct  float64        `json:"bracket_hits_pct"`
	OffsetBuckets   []OffsetBucket `json:"offset_buckets"`

	// Literals Analysis
	TotalLiterals  int     `json:"total_literals"`
	LiteralEntropy float64 `json:"literal_entropy_bits"`
	ASCIIPct        float64 `json:"ascii_percentage"`

	// Timeline
	Timeline []TimelineSegment `json:"timeline"`
}

// AnalysisReport holds one or more frame analyses (for multi-chunk streams).
type AnalysisReport struct {
	IsStream       bool            `json:"is_stream"`
	TotalFrames    int             `json:"total_frames"`
	TotalUSize     uint64          `json:"total_uncompressed_size"`
	TotalCSize     uint64          `json:"total_compressed_size"`
	OverallRatio   float64         `json:"overall_ratio"`
	OverallSavings float64         `json:"overall_savings_pct"`
	OverallBPC     float64         `json:"overall_bits_per_byte"`
	Frames         []FrameAnalysis `json:"frames"`
}

// AnalyzePRZM parses and analyzes raw PRZM or PRZS bitstreams.
func AnalyzePRZM(compressed []byte) (*AnalysisReport, error) {
	if len(compressed) < przm.HeaderSize {
		return nil, fmt.Errorf("buffer too small (%d bytes)", len(compressed))
	}

	magic := binary.LittleEndian.Uint32(compressed[0:4])
	if magic == przm.MagicPRZS {
		return analyzeStreamContainer(compressed)
	} else if magic == przm.MagicPRZM {
		frame, err := analyzeSingleFrame(compressed, 0)
		if err != nil {
			return nil, err
		}
		return &AnalysisReport{
			IsStream:       false,
			TotalFrames:    1,
			TotalUSize:     uint64(frame.UncompressedSize),
			TotalCSize:     uint64(frame.CompressedSize),
			OverallRatio:   frame.CompressionRatio,
			OverallSavings: frame.SpaceSavedPct,
			OverallBPC:     frame.BitsPerByte,
			Frames:         []FrameAnalysis{*frame},
		}, nil
	}

	return nil, fmt.Errorf("unrecognized PRZM signature (0x%08X), expected PRZM (0x%08X) or PRZS (0x%08X)",
		magic, przm.MagicPRZM, przm.MagicPRZS)
}

func analyzeStreamContainer(src []byte) (*AnalysisReport, error) {
	shdr, err := przm.ParseStreamHeader(src)
	if err != nil {
		return nil, err
	}

	report := &AnalysisReport{
		IsStream:    true,
		TotalFrames: int(shdr.ChunkCount),
		TotalUSize:  shdr.TotalUncompressedSize,
		TotalCSize:  uint64(len(src)),
	}
	if report.TotalCSize > 0 && report.TotalUSize > 0 {
		report.OverallRatio = float64(report.TotalUSize) / float64(report.TotalCSize)
		report.OverallSavings = (1.0 - (float64(report.TotalCSize) / float64(report.TotalUSize))) * 100.0
		report.OverallBPC = (float64(report.TotalCSize) * 8.0) / float64(report.TotalUSize)
	}

	offset := przm.StreamHeaderSize
	for idx := 0; idx < int(shdr.ChunkCount) && offset+4 <= len(src); idx++ {
		chunkLen := int(binary.LittleEndian.Uint32(src[offset : offset+4]))
		offset += 4
		if offset+chunkLen > len(src) {
			break
		}
		chunkData := src[offset : offset+chunkLen]
		offset += chunkLen

		fa, err := analyzeSingleFrame(chunkData, idx)
		if err == nil {
			report.Frames = append(report.Frames, *fa)
		}
	}

	return report, nil
}

func analyzeSingleFrame(src []byte, frameIdx int) (*FrameAnalysis, error) {
	hdr, err := przm.ParseFrameHeader(src)
	if err != nil {
		return nil, err
	}

	fa := &FrameAnalysis{
		FrameIndex:       frameIdx,
		Magic:            "PRZM",
		Flags:            hdr.Flags,
		UncompressedSize: hdr.UncompressedSize,
		CompressedSize:   len(src),
		Stride:           hdr.Stride,
		WindowLog:        hdr.WindowLog,
		WindowSize:       1 << hdr.WindowLog,
		DictID:           hdr.DictID,
		NormMode:         hdr.NormMode,
		FamilyCounts:     make(map[OpcodeFamily]int),
		FamilyPct:        make(map[OpcodeFamily]float64),
	}

	if fa.CompressedSize > 0 && fa.UncompressedSize > 0 {
		fa.CompressionRatio = float64(fa.UncompressedSize) / float64(fa.CompressedSize)
		fa.SpaceSavedPct = (1.0 - (float64(fa.CompressedSize) / float64(fa.UncompressedSize))) * 100.0
		fa.BitsPerByte = (float64(fa.CompressedSize) * 8.0) / float64(fa.UncompressedSize)
	}

	// Flags names
	fa.FlagNames = parseFlagNames(hdr.Flags)
	fa.NormModeName = parseNormModeName(hdr.NormMode)

	// Compute Section offsets
	sec1Offset := hdr.DataOffset
	sec1Size := 8
	sec2Offset := sec1Offset + sec1Size
	sec3Offset := sec2Offset + hdr.CmdStreamSize
	sec4Offset := sec3Offset + hdr.OffsetStreamSize
	sec4Size := max(len(src)-sec4Offset, 0)

	// Section 1: Header / Directory
	headerBytes := sec2Offset
	fa.Sections = append(fa.Sections, SectionDetail{
		Index:      1,
		Name:       "Section 1: Frame Header & Directory",
		Offset:     0,
		PackedSize: headerBytes,
		RawSize:    headerBytes,
		Percentage: float64(headerBytes) / float64(len(src)) * 100.0,
		ModeID:     0,
		ModeName:   "Fixed Header (Little-Endian)",
		Ratio:      1.0,
	})

	// Section 2: Commands
	var cmdRaw []byte
	var cmdMode uint8
	if hdr.CmdStreamSize > 0 && sec2Offset+hdr.CmdStreamSize <= len(src) {
		packedCmds := src[sec2Offset : sec2Offset+hdr.CmdStreamSize]
		cmdMode = packedCmds[0]
		cmdRawBuf := make([]byte, 16*1024*1024)
		nCmd, err := przm.UnpackStream(cmdRawBuf, packedCmds, nil)
		if err == nil {
			cmdRaw = cmdRawBuf[:nCmd]
		} else {
			cmdRaw = packedCmds // Fallback
		}

		cRatio := 1.0
		if hdr.CmdStreamSize > 0 && len(cmdRaw) > 0 {
			cRatio = float64(len(cmdRaw)) / float64(hdr.CmdStreamSize)
		}

		fa.Sections = append(fa.Sections, SectionDetail{
			Index:       2,
			Name:        "Section 2: Command / Opcode Stream",
			Offset:      sec2Offset,
			PackedSize:  hdr.CmdStreamSize,
			RawSize:     len(cmdRaw),
			Percentage:  float64(hdr.CmdStreamSize) / float64(len(src)) * 100.0,
			ModeID:      cmdMode,
			ModeName:    ModeName(cmdMode),
			Ratio:       cRatio,
			EntropyBits: calcShannonEntropy(cmdRaw),
		})
	}

	// Section 3: Offsets
	var offRaw []byte
	var offMode uint8
	if hdr.OffsetStreamSize > 0 && sec3Offset+hdr.OffsetStreamSize <= len(src) {
		packedOffs := src[sec3Offset : sec3Offset+hdr.OffsetStreamSize]
		offMode = packedOffs[0]
		offRawBuf := make([]byte, 16*1024*1024)
		nOff, err := przm.UnpackStream(offRawBuf, packedOffs, nil)
		if err == nil {
			offRaw = offRawBuf[:nOff]
		} else {
			offRaw = packedOffs // Fallback
		}

		oRatio := 1.0
		if hdr.OffsetStreamSize > 0 && len(offRaw) > 0 {
			oRatio = float64(len(offRaw)) / float64(hdr.OffsetStreamSize)
		}

		fa.Sections = append(fa.Sections, SectionDetail{
			Index:       3,
			Name:        "Section 3: Match Offset Stream",
			Offset:      sec3Offset,
			PackedSize:  hdr.OffsetStreamSize,
			RawSize:     len(offRaw),
			Percentage:  float64(hdr.OffsetStreamSize) / float64(len(src)) * 100.0,
			ModeID:      offMode,
			ModeName:    ModeName(offMode),
			Ratio:       oRatio,
			EntropyBits: calcShannonEntropy(offRaw),
		})
	} else {
		// Empty Section 3 Omission
		fa.Sections = append(fa.Sections, SectionDetail{
			Index:      3,
			Name:       "Section 3: Match Offset Stream (OMITTED)",
			Offset:     sec3Offset,
			PackedSize: 0,
			RawSize:    0,
			Percentage: 0.0,
			ModeID:     0,
			ModeName:   "Empty Section 3 Omission (0 bytes)",
			Ratio:      1.0,
		})
	}

	// Section 4: Literals
	var litRaw []byte
	if sec4Size > 0 && sec4Offset+sec4Size <= len(src) {
		litRaw = src[sec4Offset : sec4Offset+sec4Size]
		fa.Sections = append(fa.Sections, SectionDetail{
			Index:       4,
			Name:        "Section 4: Raw Literal / Payload Stream",
			Offset:      sec4Offset,
			PackedSize:  sec4Size,
			RawSize:     sec4Size,
			Percentage:  float64(sec4Size) / float64(len(src)) * 100.0,
			ModeID:      0,
			ModeName:    "Direct Raw / V-ANS Nibble Stream",
			Ratio:       1.0,
			EntropyBits: calcShannonEntropy(litRaw),
		})
		fa.TotalLiterals = len(litRaw)
		fa.LiteralEntropy = calcShannonEntropy(litRaw)
		fa.ASCIIPct = calcASCIIPct(litRaw)
	}

	// Deep Disassembly of Command Stream
	disassembleCommands(fa, cmdRaw, offRaw, hdr)

	// Timeline generation
	generateTimeline(fa)

	return fa, nil
}

func parseFlagNames(flags uint8) []string {
	var names []string
	if flags == 0 {
		names = append(names, "Standard Defaults")
	}
	if (flags & przm.FlagStride) != 0 {
		names = append(names, "FlagStride")
	}
	if (flags & przm.FlagDeltaMask) != 0 {
		names = append(names, "FlagDeltaMask")
	}
	if (flags & przm.FlagUncompressed) != 0 {
		names = append(names, "FlagUncompressed")
	}
	if (flags & przm.FlagExtOffset) != 0 {
		names = append(names, "FlagExtOffset (24/32-bit)")
	}
	if (flags & przm.FlagDictRef) != 0 {
		names = append(names, "FlagDictRef")
	}
	if (flags & przm.FlagNorm) != 0 {
		names = append(names, "FlagNorm (Base64/Hex)")
	}
	if (flags & przm.FlagMinMatch3) != 0 {
		names = append(names, "FlagMinMatch3")
	}
	if (flags & przm.FlagNibble) != 0 {
		names = append(names, "FlagNibble (SIMD Nibble/V-ANS)")
	}
	return names
}

func parseNormModeName(mode uint8) string {
	switch mode {
	case 0:
		return "None (Raw Text/Binary)"
	case 1:
		return "Hex Lowercase"
	case 2:
		return "Base64 Standard"
	case 3:
		return "Spans (Mixed Norm)"
	case 4:
		return "Hex Uppercase"
	case 5:
		return "Base64 Standard (Pad 1)"
	case 6:
		return "Base64 Standard (Pad 2)"
	case 7:
		return "Base64 URL"
	default:
		return fmt.Sprintf("Mode %d", mode)
	}
}

func calcShannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0.0
	}
	var freqs [256]int
	for _, b := range data {
		freqs[b]++
	}
	total := float64(len(data))
	entropy := 0.0
	for _, count := range freqs {
		if count > 0 {
			p := float64(count) / total
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

func calcASCIIPct(data []byte) float64 {
	if len(data) == 0 {
		return 0.0
	}
	asciiCount := 0
	for _, b := range data {
		if (b >= 32 && b <= 126) || b == '\n' || b == '\r' || b == '\t' {
			asciiCount++
		}
	}
	return float64(asciiCount) / float64(len(data)) * 100.0
}

func disassembleCommands(fa *FrameAnalysis, cmds, offs []byte, hdr przm.FrameHeader) {
	if len(cmds) == 0 {
		return
	}

	fa.CommandEntropy = calcShannonEntropy(cmds)

	opcodeHist := make(map[byte]*OpcodeStat)
	distHist := make([]int, 0, 1024)

	rep0, rep1, rep2, rep3 := 1, 4, 8, 16
	if hdr.Stride > 1 {
		rep0, rep1, rep2, rep3 = hdr.Stride, 2*hdr.Stride, 3*hdr.Stride, 4*hdr.Stride
	}
	minMatchBase := 4
	if (hdr.Flags & przm.FlagMinMatch3) != 0 {
		minMatchBase = 3
	}

	cmdPtr := 0
	offPtr := 0

	readVarint := func() (int, bool) {
		if offPtr >= len(offs) {
			return 1, false
		}
		val, n := binary.Uvarint(offs[offPtr:])
		if n <= 0 {
			offPtr++
			return 1, false
		}
		offPtr += n
		return int(val), true
	}

	for cmdPtr < len(cmds) {
		curOffset := cmdPtr
		op := cmds[cmdPtr]
		cmdPtr++

		fa.TotalCommands++

		// Get or init opcode stat
		stat, ok := opcodeHist[op]
		if !ok {
			stat = &OpcodeStat{
				Opcode: op,
				Hex:    fmt.Sprintf("0x%02X", op),
			}
			opcodeHist[op] = stat
		}
		stat.Count++

		cmd := DisassembledCommand{
			Offset: curOffset,
			Opcode: op,
			Hex:    stat.Hex,
		}

		if op < 0x80 {
			// Fast-Path 1: Fused Repeat (00 LLL MM KK)
			litLen := int((op >> 4) & 0x07)
			mLen := int((op>>2)&0x03) + minMatchBase
			k := int(op & 0x03)

			var dist int
			switch k {
			case 0:
				dist = rep0
			case 1:
				dist = rep1
				rep1 = rep0
				rep0 = dist
			case 2:
				dist = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = dist
			case 3:
				dist = rep3
				rep3 = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = dist
			}

			fa.TotalMatches++
			fa.RepeatHits++
			distHist = append(distHist, dist)

			stat.Family = FamilyFusedRepeat
			stat.Name = fmt.Sprintf("FusedRep (lit=%d, len=%d, rep%d)", litLen, mLen, k)
			stat.Template = fmt.Sprintf("0%03b%02b%02b", litLen, mLen-minMatchBase, k)
			stat.TotalLits += litLen
			stat.TotalMatches++

			cmd.Family = FamilyFusedRepeat
			cmd.Description = fmt.Sprintf("Repeat Match via rep%d (d=%d, len=%d, lit=%d)", k, dist, mLen, litLen)
			cmd.LiteralLen = litLen
			cmd.MatchLen = mLen
			cmd.Distance = dist
			cmd.RepeatIndex = k

			fa.FamilyCounts[FamilyFusedRepeat]++
			fa.Commands = append(fa.Commands, cmd)
			continue
		}

		if op < 0xC0 {
			// Fast-Path 2: Fused Extended (10 LL MMMM)
			litLen := int((op >> 4) & 0x03)
			baseLen := int(op & 0x0F)
			mLen := baseLen + minMatchBase
			if baseLen == 15 && cmdPtr < len(cmds) {
				extra, n := binary.Uvarint(cmds[cmdPtr:])
				cmdPtr += n
				mLen = 15 + minMatchBase + int(extra)
			}

			dist, _ := readVarint()
			rep3 = rep2
			rep2 = rep1
			rep1 = rep0
			rep0 = dist

			fa.TotalMatches++
			fa.ExplicitHits++
			distHist = append(distHist, dist)

			stat.Family = FamilyFusedExtended
			stat.Name = fmt.Sprintf("FusedExt (lit=%d, baseLen=%d)", litLen, baseLen)
			stat.Template = fmt.Sprintf("10%02b%04b", litLen, baseLen)
			stat.TotalLits += litLen
			stat.TotalMatches++

			cmd.Family = FamilyFusedExtended
			cmd.Description = fmt.Sprintf("Explicit Match (d=%d, len=%d, lit=%d)", dist, mLen, litLen)
			cmd.LiteralLen = litLen
			cmd.MatchLen = mLen
			cmd.Distance = dist

			fa.FamilyCounts[FamilyFusedExtended]++
			fa.Commands = append(fa.Commands, cmd)
			continue
		}

		// Specialized Opcodes
		switch {
		case op >= 0xC0 && op <= 0xC3:
			// Long Repeat
			k := int(op & 0x03)
			var tok byte
			if cmdPtr < len(cmds) {
				tok = cmds[cmdPtr]
				cmdPtr++
			}
			litLen := int((tok >> 4) & 0x0F)
			baseLen := int(tok & 0x0F)
			mLen := baseLen + minMatchBase
			if baseLen == 15 && cmdPtr < len(cmds) {
				extra, n := binary.Uvarint(cmds[cmdPtr:])
				cmdPtr += n
				mLen = 15 + minMatchBase + int(extra)
			}

			var dist int
			switch k {
			case 0:
				dist = rep0
			case 1:
				dist = rep1
				rep1 = rep0
				rep0 = dist
			case 2:
				dist = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = dist
			case 3:
				dist = rep3
				rep3 = rep2
				rep2 = rep1
				rep1 = rep0
				rep0 = dist
			}

			fa.TotalMatches++
			fa.RepeatHits++
			distHist = append(distHist, dist)

			stat.Family = FamilyLongRepeat
			stat.Name = fmt.Sprintf("LongRepeat rep%d (lit=%d, len=%d)", k, litLen, mLen)
			cmd.Family = FamilyLongRepeat
			cmd.Description = fmt.Sprintf("Long Repeat rep%d (d=%d, len=%d, lit=%d)", k, dist, mLen, litLen)
			cmd.LiteralLen = litLen
			cmd.MatchLen = mLen
			cmd.Distance = dist
			cmd.RepeatIndex = k

			fa.FamilyCounts[FamilyLongRepeat]++
			fa.Commands = append(fa.Commands, cmd)

		case op >= 0xC4 && op <= 0xCD:
			// Fused Offset Bracket
			bracket := int(op - 0xC4)
			var tok byte
			if cmdPtr < len(cmds) {
				tok = cmds[cmdPtr]
				cmdPtr++
			}
			litLen := int((tok >> 4) & 0x0F)
			baseLen := int(tok & 0x0F)
			mLen := baseLen + minMatchBase
			var dist int
			if bracket < 8 {
				dist = bracket + 1
			} else if bracket == 8 {
				dist = 16
			} else {
				dist = 32
			}

			rep3 = rep2
			rep2 = rep1
			rep1 = rep0
			rep0 = dist

			fa.TotalMatches++
			fa.BracketHits++
			distHist = append(distHist, dist)

			stat.Family = FamilyFusedBracket
			stat.Name = fmt.Sprintf("Bracket %d (d=%d, lit=%d, len=%d)", bracket, dist, litLen, mLen)
			cmd.Family = FamilyFusedBracket
			cmd.Description = fmt.Sprintf("Fused Bracket d=%d (len=%d, lit=%d, Sec3=0B)", dist, mLen, litLen)
			cmd.LiteralLen = litLen
			cmd.MatchLen = mLen
			cmd.Distance = dist

			fa.FamilyCounts[FamilyFusedBracket]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xCE:
			// Sliding Difference Lattice & Delta-Stride Match
			var desc, deltaByte byte
			if cmdPtr+1 < len(cmds) {
				desc = cmds[cmdPtr]
				deltaByte = cmds[cmdPtr+1]
				cmdPtr += 2
			}
			mode := int((desc >> 6) & 0x03)
			litLen := int((desc >> 3) & 0x07)
			baseLen := int(desc & 0x07)
			mLen := baseLen + minMatchBase

			if litLen == 7 && cmdPtr < len(cmds) {
				extra, n := binary.Uvarint(cmds[cmdPtr:])
				cmdPtr += n
				litLen += int(extra)
			}
			if baseLen == 7 && cmdPtr < len(cmds) {
				extra, n := binary.Uvarint(cmds[cmdPtr:])
				cmdPtr += n
				mLen += int(extra)
			}

			var dist int
			deltaSigned := int(int8(deltaByte))
			switch mode {
			case 0:
				dist = rep0 + deltaSigned
			case 1:
				dist = rep1 + deltaSigned
			case 2:
				dist = rep2 + deltaSigned
			case 3:
				if hdr.Stride > 1 {
					dist = rep0 + deltaSigned*hdr.Stride
				} else {
					dist = int(deltaByte) + 1
				}
			}

			rep3 = rep2
			rep2 = rep1
			rep1 = rep0
			rep0 = dist

			fa.TotalMatches++
			distHist = append(distHist, dist)

			stat.Family = FamilyDiffLattice
			stat.Name = fmt.Sprintf("DiffLattice mode%d (d=%d, len=%d, lit=%d)", mode, dist, mLen, litLen)
			cmd.Family = FamilyDiffLattice
			cmd.Description = fmt.Sprintf(
				"Diff Lattice mode%d (d=%d, len=%d, lit=%d, Sec3=0B)",
				mode, dist, mLen, litLen,
			)
			cmd.LiteralLen = litLen
			cmd.MatchLen = mLen
			cmd.Distance = dist

			fa.FamilyCounts[FamilyDiffLattice]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xCF:
			// Dual Consecutive Fused Micro-Token
			var tok1, tok2 byte
			if cmdPtr+1 < len(cmds) {
				tok1 = cmds[cmdPtr]
				tok2 = cmds[cmdPtr+1]
				cmdPtr += 2
			}
			distTable := [16]int{1, 2, 3, 4, 5, 6, 7, 8, 12, 16, 24, 32, 48, 64, 96, 128}
			mLen1 := int((tok1>>4)&0x0F) + minMatchBase
			d1 := distTable[tok1&0x0F]
			mLen2 := int((tok2>>4)&0x0F) + minMatchBase
			d2 := distTable[tok2&0x0F]

			fa.TotalMatches += 2
			fa.BracketHits += 2
			distHist = append(distHist, d1, d2)

			stat.Family = FamilyFusedMicro
			stat.Name = fmt.Sprintf("DualMicroToken (d1=%d, d2=%d)", d1, d2)
			cmd.Family = FamilyFusedMicro
			cmd.Description = fmt.Sprintf(
				"Dual Micro-Tokens [Match1: d=%d, l=%d] [Match2: d=%d, l=%d]",
				d1,
				mLen1,
				d2,
				mLen2,
			)

			fa.FamilyCounts[FamilyFusedMicro]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xFE:
			// Match Walk (0xFE)
			var walkHdr byte
			if cmdPtr < len(cmds) {
				walkHdr = cmds[cmdPtr]
				cmdPtr++
			}
			repCode := int(walkHdr & 0x03)
			walkType := int((walkHdr >> 2) & 0x03)
			stepCount := int((walkHdr>>4)&0x0F) + 2

			stat.Family = FamilyMatchWalk
			stat.Name = fmt.Sprintf("MatchWalk (type=%d, steps=%d, rep=%d)", walkType, stepCount, repCode)
			cmd.Family = FamilyMatchWalk
			cmd.Description = fmt.Sprintf(
				"Self-Reconstructing Match Walk: %d steps, type=%d, rep=%d",
				stepCount,
				walkType,
				repCode,
			)

			fa.FamilyCounts[FamilyMatchWalk]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xFD:
			// Block Deduplication
			stat.Family = FamilyCopyBlock
			stat.Name = "CopyBlock (64 KiB Dedup)"
			cmd.Family = FamilyCopyBlock
			cmd.Description = "Aligned 64 KiB Block Deduplication Reconstruct"
			cmdPtr += 4 // block idx

			fa.FamilyCounts[FamilyCopyBlock]++
			fa.Commands = append(fa.Commands, cmd)

		case op >= 0xD0 && op <= 0xD7:
			// Stride Match
			m := int(op&0x07) + 1
			dist := m * hdr.Stride
			stat.Family = FamilyStride
			stat.Name = fmt.Sprintf("StrideMatch (m=%d, dist=%d)", m, dist)
			cmd.Family = FamilyStride
			cmd.Description = fmt.Sprintf("Columnar Stride Match m=%d (d=%d)", m, dist)
			cmd.Distance = dist
			cmdPtr++ // token

			fa.TotalMatches++
			distHist = append(distHist, dist)
			fa.FamilyCounts[FamilyStride]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xF0:
			// Literal Run
			stat.Family = FamilyLiteralRun
			stat.Name = "LiteralRun (Uvarint)"
			cmd.Family = FamilyLiteralRun
			cmd.Description = "Long Literal Run Header"
			if cmdPtr < len(cmds) {
				extra, n := binary.Uvarint(cmds[cmdPtr:])
				cmdPtr += n
				cmd.LiteralLen = int(extra) + 16
			}
			fa.FamilyCounts[FamilyLiteralRun]++
			fa.Commands = append(fa.Commands, cmd)

		case op >= 0xF3 && op <= 0xFA:
			// Direct Short Literal Run (1..8)
			litLen := int(op - 0xF3 + 1)
			stat.Family = FamilyLiteralRun
			stat.Name = fmt.Sprintf("LitShort (%d bytes)", litLen)
			cmd.Family = FamilyLiteralRun
			cmd.Description = fmt.Sprintf("Direct Short Literal Run: %d bytes", litLen)
			cmd.LiteralLen = litLen

			fa.FamilyCounts[FamilyLiteralRun]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xF1:
			// RLE
			stat.Family = FamilyRLE
			stat.Name = "RLE (Run-Length Encoding)"
			cmd.Family = FamilyRLE
			cmd.Description = "RLE Sequence Header"
			if cmdPtr < len(cmds) {
				extra, n := binary.Uvarint(cmds[cmdPtr:])
				cmdPtr += n
				cmd.MatchLen = int(extra)
			}
			fa.FamilyCounts[FamilyRLE]++
			fa.Commands = append(fa.Commands, cmd)

		case op >= 0xD8 && op <= 0xDF:
			// Direct 1-byte Schema Token
			tokID := int(op - 0xD8)
			stat.Family = FamilyDictEntry
			stat.Name = fmt.Sprintf("SchemaTokenDirect #%d", tokID)
			cmd.Family = FamilyDictEntry
			cmd.Description = fmt.Sprintf("Direct 1-byte Schema Dictionary Token #%d", tokID)

			fa.FamilyCounts[FamilyDictEntry]++
			fa.Commands = append(fa.Commands, cmd)

		case op == 0xF2:
			// Extended Schema Token
			stat.Family = FamilyDictEntry
			stat.Name = "SchemaTokenExt"
			cmd.Family = FamilyDictEntry
			cmd.Description = "Extended Schema Dictionary Token"
			cmdPtr++ // token id

			fa.FamilyCounts[FamilyDictEntry]++
			fa.Commands = append(fa.Commands, cmd)

		default:
			stat.Family = FamilyOther
			stat.Name = fmt.Sprintf("Opcode 0x%02X", op)
			cmd.Family = FamilyOther
			cmd.Description = fmt.Sprintf("Specialized Token 0x%02X", op)

			fa.FamilyCounts[FamilyOther]++
			fa.Commands = append(fa.Commands, cmd)
		}
	}

	// Calculate percentages for families
	for fam, count := range fa.FamilyCounts {
		if fa.TotalCommands > 0 {
			fa.FamilyPct[fam] = float64(count) / float64(fa.TotalCommands) * 100.0
		}
	}

	// Sort Top Opcodes
	allStats := make([]OpcodeStat, 0, len(opcodeHist))
	for _, s := range opcodeHist {
		if fa.TotalCommands > 0 {
			s.Percentage = float64(s.Count) / float64(fa.TotalCommands) * 100.0
		}
		allStats = append(allStats, *s)
	}
	sort.Slice(allStats, func(i, j int) bool {
		return allStats[i].Count > allStats[j].Count
	})
	if len(allStats) > 15 {
		fa.TopOpcodes = allStats[:15]
	} else {
		fa.TopOpcodes = allStats
	}

	// Compute average bits per command
	if fa.TotalCommands > 0 {
		fa.AvgBitsPerCmd = (float64(hdr.CmdStreamSize) * 8.0) / float64(fa.TotalCommands)
	}

	// Compute Locality metrics
	if fa.TotalMatches > 0 {
		fa.RepeatHitsPct = float64(fa.RepeatHits) / float64(fa.TotalMatches) * 100.0
		fa.ExplicitHitsPct = float64(fa.ExplicitHits) / float64(fa.TotalMatches) * 100.0
		fa.BracketHitsPct = float64(fa.BracketHits) / float64(fa.TotalMatches) * 100.0
	}

	// Bucketize match distances
	fa.OffsetBuckets = bucketizeOffsets(distHist)
}

func bucketizeOffsets(distances []int) []OffsetBucket {
	buckets := []OffsetBucket{
		{Label: "Ultra-Local (d <= 16, L1 Cache)", Min: 1, Max: 16},
		{Label: "Local (16 < d <= 256, L2 Cache)", Min: 17, Max: 256},
		{Label: "Page (256 < d <= 4,096, L3 / OS Page)", Min: 257, Max: 4096},
		{Label: "Medium (4,096 < d <= 65,536, 64KB Window)", Min: 4097, Max: 65536},
		{Label: "Far-Range (d > 65,536, 1MB-8MB Sliding Slab)", Min: 65537, Max: math.MaxInt32},
	}

	total := len(distances)
	for _, d := range distances {
		for i := range buckets {
			if d >= buckets[i].Min && d <= buckets[i].Max {
				buckets[i].Count++
				break
			}
		}
	}

	for i := range buckets {
		if total > 0 {
			buckets[i].Percentage = float64(buckets[i].Count) / float64(total) * 100.0
		}
	}

	return buckets
}

func generateTimeline(fa *FrameAnalysis) {
	const numSegments = 64
	segSize := fa.UncompressedSize / numSegments
	if segSize <= 0 {
		segSize = 1
	}

	fa.Timeline = make([]TimelineSegment, numSegments)
	for i := range numSegments {
		fa.Timeline[i] = TimelineSegment{
			Start: i * segSize,
			End:   min((i+1)*segSize, fa.UncompressedSize),
		}
	}

	curPos := 0
	for _, cmd := range fa.Commands {
		if cmd.LiteralLen > 0 {
			markTimeline(fa.Timeline, curPos, cmd.LiteralLen, segSize, "literal")
			curPos += cmd.LiteralLen
		}
		if cmd.MatchLen > 0 {
			mode := "match"
			if cmd.Family == FamilyFusedRepeat || cmd.Family == FamilyLongRepeat {
				mode = "repeat"
			}
			markTimeline(fa.Timeline, curPos, cmd.MatchLen, segSize, mode)
			curPos += cmd.MatchLen
		}
	}
}

func markTimeline(tl []TimelineSegment, start, length, segSize int, mode string) {
	if segSize <= 0 || len(tl) == 0 {
		return
	}
	end := start + length
	segStartIdx := start / segSize
	segEndIdx := end / segSize
	if segStartIdx >= len(tl) {
		segStartIdx = len(tl) - 1
	}
	if segEndIdx >= len(tl) {
		segEndIdx = len(tl) - 1
	}

	for i := segStartIdx; i <= segEndIdx; i++ {
		overlapStart := max(start, tl[i].Start)
		overlapEnd := min(end, tl[i].End)
		span := overlapEnd - overlapStart
		if span > 0 {
			weight := float64(span) / float64(tl[i].End-tl[i].Start+1)
			switch mode {
			case "literal":
				tl[i].LiteralPct += weight * 100.0
			case "match":
				tl[i].MatchPct += weight * 100.0
			case "repeat":
				tl[i].RepeatPct += weight * 100.0
			}
		}
	}
}
