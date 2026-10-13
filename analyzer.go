// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/lemon4ksan/foundation/bufkit"
)

// ChannelBreakdown contains exhaustive metrics, channel distributions,
// and entropy statistics for a modern compressed PRZM frame.
type ChannelBreakdown struct {
	// Frame Header & High-Level Metadata
	UncompressedSize int     `json:"uncompressed_size"`
	CompressedSize   int     `json:"compressed_size"`
	Ratio            float64 `json:"ratio"`
	SpaceSavedPct    float64 `json:"space_saved_pct"`
	Flags            uint8   `json:"flags"`
	ActiveFlags      string  `json:"active_flags"`
	WindowLog        uint8   `json:"window_log"`
	WindowSizeKB     int     `json:"window_size_kb"`
	DictID           uint32  `json:"dict_id,omitempty"`
	DictName         string  `json:"dict_name,omitempty"`
	Stride           int     `json:"stride,omitempty"`
	NormMode         uint8   `json:"norm_mode,omitempty"`
	NormName         string  `json:"norm_name,omitempty"`

	// Section 1: Framing & Container Overhead
	HeaderSize    int     `json:"header_size"`
	DirectorySize int     `json:"directory_size"`
	Sec1ParamSize int     `json:"sec1_param_size"`
	TotalFraming  int     `json:"total_framing"`
	FramingPct    float64 `json:"framing_pct"`

	// Section 2: Command Stream Metrics
	CmdSize          int     `json:"cmd_size"`
	CmdPct           float64 `json:"cmd_pct"`
	CmdMode          uint8   `json:"cmd_mode"`
	CmdModeName      string  `json:"cmd_mode_name"`
	CmdUnpackedBytes int     `json:"cmd_unpacked_bytes"`
	CmdOpBytes       int     `json:"cmd_op_bytes,omitempty"`
	CmdMetaBytes     int     `json:"cmd_meta_bytes,omitempty"`
	CmdVarBytes      int     `json:"cmd_var_bytes,omitempty"`

	// Opcode Execution Profile
	TotalCommands     int `json:"total_commands"`
	Cat00RepeatCount  int `json:"cat00_repeat_count"`
	Cat00Rep0Count    int `json:"cat00_rep0_count"`
	Cat00Rep1Count    int `json:"cat00_rep1_count"`
	Cat00Rep2Count    int `json:"cat00_rep2_count"`
	Cat00Rep3Count    int `json:"cat00_rep3_count"`
	Cat10ExtCount     int `json:"cat10_ext_count"`
	CatBracketCount   int `json:"cat_bracket_count"`
	CatLongRepCount   int `json:"cat_long_rep_count"`
	Cat01StrideCount  int `json:"cat01_stride_count"`
	CatDeltaLocCount  int `json:"cat_delta_loc_count"`
	CatRFCTransCount  int `json:"cat_rfc_trans_count"`
	CatRawDictCount   int `json:"cat_raw_dict_count"`
	CatDeltaRingCount int `json:"cat_delta_ring_count"`
	CatDeltaExpCount  int `json:"cat_delta_exp_count"`
	Cat11LitRunCount  int `json:"cat11_lit_run_count"`
	Cat10RLECount     int `json:"cat10_rle_count"`
	CatDedupCount     int `json:"cat_dedup_count"`
	CatGrammarCount   int `json:"cat_grammar_count"`
	CatTokenPairCount int `json:"cat_token_pair_count"`

	TotalMatches    int `json:"total_matches"`
	TotalMatchBytes int `json:"total_match_bytes"`
	TotalLitBytes   int `json:"total_lit_bytes"`

	// Section 3: Offset Stream Metrics
	OffsetSize          int     `json:"offset_size"`
	OffsetPct           float64 `json:"offset_pct"`
	OffsetMode          uint8   `json:"offset_mode"`
	OffsetModeName      string  `json:"offset_mode_name"`
	OffsetUnpackedBytes int     `json:"offset_unpacked_bytes"`
	MatchDistCount      int     `json:"match_dist_count"`
	DistinctOffsets     int     `json:"distinct_offsets"`
	AvgDistance         float64 `json:"avg_distance"`
	DistanceEntropy     float64 `json:"distance_entropy"`
	RepHitRatio         float64 `json:"rep_hit_ratio"`

	// Section 4: Literal Stream Metrics
	LiteralSize           int     `json:"literal_size"`
	LiteralPct            float64 `json:"literal_pct"`
	LiteralMode           string  `json:"literal_mode"`
	LiteralCount          int     `json:"literal_count"`
	LiteralEntropy        float64 `json:"literal_entropy"`
	BitsPerLiteral        float64 `json:"bits_per_literal"`
	VANSGainPct           float64 `json:"vans_gain_pct"`
	PotentialLiteralBytes int     `json:"potential_literal_bytes,omitempty"`
	PotentialGainBytes    int     `json:"potential_gain_bytes,omitempty"`
	PotentialRatio        float64 `json:"potential_ratio,omitempty"`
}

// FrameAnalysis is an alias for ChannelBreakdown for backward compatibility.
type FrameAnalysis = ChannelBreakdown

func describeDictionary(dictID uint32) string {
	switch dictID {
	case DictIDNone:
		return "None"
	case DictIDBrotliWeb:
		return "RFC 7932 Brotli Web Dictionary"
	case DictIDJSON:
		return "Structured JSON / Web API Vocabulary"
	default:
		return fmt.Sprintf("External Custom Dictionary (0x%08X)", dictID)
	}
}

func describeNormalization(mode uint8) string {
	switch mode {
	case NormModeNone:
		return "None"
	case NormModeHex:
		return "Hexadecimal Pair Nibble Folding"
	case NormModeBase64:
		return "Base64 6-bit Direct Slicing"
	case NormModeSpans:
		return "Structured Whitespace Run Normalization"
	default:
		return fmt.Sprintf("Specialized Normalization (Mode %d)", mode)
	}
}

func describeStreamMode(mode uint8) string {
	switch mode {
	case ModeFlat:
		return "ModeFlat (Uncompressed Byte Stream)"
	case ModeHuffman4X:
		return "ModeHuffman4X (4-Stream Canonical Huffman)"
	case ModeHuffmanCompact:
		return "ModeHuffmanCompact (Single-Stream Huffman with Mask)"
	case ModeHuffmanCompact4X:
		return "ModeHuffmanCompact4X (4-Stream Huffman with Mask)"
	case ModeRecursiveLZ:
		return "ModeRecursiveLZ (Embedded Second-Order LZ77)"
	case ModeVANS4X:
		return "ModeVANS4X (4-Way Interleaved Vector-State ANS)"
	case ModeDistancePrefix:
		return "ModeDistancePrefix (Log2 Distance Prefix Codebook)"
	case ModeSplitVarint:
		return "ModeSplitVarint (2-Channel De-interleaved Varint)"
	case ModeCommandEntropy:
		return "ModeCommandEntropy (3-Way Structural Split V-ANS)"
	case ModeFlatCompact:
		return "ModeFlatCompact (1-Byte Minimal Header)"
	case ModeDistancePrefixByte:
		return "ModeDistancePrefixByte (Compact 2-Byte Prefix Header)"
	case ModeDistancePrefixCompact:
		return "ModeDistancePrefixCompact (Compact 3-Byte Prefix Header)"
	case 12:
		return "Mode 12 (4-Context Clustered Distance V-ANS)"
	case 13:
		return "Mode 13 (Log2 Length-Prefix Codebook)"
	default:
		return fmt.Sprintf("Mode %d (Proprietary Stream)", mode)
	}
}

func activeFlagsString(flags uint8) string {
	var parts []string
	if (flags & FlagStride) != 0 {
		parts = append(parts, "Stride")
	}
	if (flags & FlagDeltaMask) != 0 {
		parts = append(parts, "DeltaMask")
	}
	if (flags & FlagUncompressed) != 0 {
		parts = append(parts, "Uncompressed")
	}
	if (flags & FlagExtOffset) != 0 {
		parts = append(parts, "ExtOffset")
	}
	if (flags & FlagDictRef) != 0 {
		parts = append(parts, "DictRef")
	}
	if (flags & FlagNorm) != 0 {
		parts = append(parts, "Norm")
	}
	if (flags & FlagMinMatch3) != 0 {
		parts = append(parts, "MinMatch3")
	}
	if (flags & FlagNibble) != 0 {
		parts = append(parts, "NibblePlanes/V-ANS")
	}
	if len(parts) == 0 {
		return "None"
	}
	return strings.Join(parts, ", ")
}

// AnalyzeFrame inspects a compressed PRZM frame and extracts full channel statistics,
// opcode distributions, dictionary hit counts, and entropy metrics.
func AnalyzeFrame(compressed []byte) (*ChannelBreakdown, error) {
	if len(compressed) < HeaderSize {
		return nil, fmt.Errorf("%w: frame buffer too small (%d bytes)", ErrCorruptedFrame, len(compressed))
	}

	hdr, err := ParseFrameHeader(compressed)
	if err != nil {
		return nil, err
	}

	totalLen := float64(len(compressed))
	b := &ChannelBreakdown{
		UncompressedSize: hdr.UncompressedSize,
		CompressedSize:   len(compressed),
		Flags:            hdr.Flags,
		ActiveFlags:      activeFlagsString(hdr.Flags),
		Stride:           hdr.Stride,
		WindowLog:        hdr.WindowLog,
		WindowSizeKB:     1 << (hdr.WindowLog - 10),
		DictID:           hdr.DictID,
		DictName:         describeDictionary(hdr.DictID),
		NormMode:         hdr.NormMode,
		NormName:         describeNormalization(hdr.NormMode),
		HeaderSize:       HeaderSize,
	}

	if b.CompressedSize > 0 && b.UncompressedSize > 0 {
		b.Ratio = float64(b.UncompressedSize) / float64(b.CompressedSize)
		b.SpaceSavedPct = (1.0 - float64(b.CompressedSize)/float64(b.UncompressedSize)) * 100.0
	}

	// Uncompressed / empty frame early exit
	if (hdr.Flags&FlagUncompressed) != 0 || hdr.UncompressedSize == 0 {
		b.TotalFraming = len(compressed)
		b.FramingPct = 100.0
		return b, nil
	}

	// Layout resolution
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

	b.DirectorySize = sec1Offset - HeaderSize
	b.Sec1ParamSize = sec1Size
	b.TotalFraming = b.HeaderSize + b.DirectorySize + b.Sec1ParamSize
	b.CmdSize = hdr.CmdStreamSize
	b.OffsetSize = hdr.OffsetStreamSize
	b.LiteralSize = len(compressed) - sec4Offset

	if totalLen > 0 {
		b.FramingPct = float64(b.TotalFraming) / totalLen * 100.0
		b.CmdPct = float64(b.CmdSize) / totalLen * 100.0
		b.OffsetPct = float64(b.OffsetSize) / totalLen * 100.0
		b.LiteralPct = float64(b.LiteralSize) / totalLen * 100.0
	}

	pad := decScratchPool.Get()
	defer decScratchPool.Put(pad)

	minMatchBase := 4
	if (hdr.Flags & FlagMinMatch3) != 0 {
		minMatchBase = 3
	}

	// -------------------------------------------------------------------------
	// 1. Resolve & Analyze Section 2 (Commands)
	// -------------------------------------------------------------------------
	var cmds []byte
	if hdr.CmdStreamSize > 0 && sec2Offset < len(compressed) {
		sec2Mode := compressed[sec2Offset]
		b.CmdMode = sec2Mode
		b.CmdModeName = describeStreamMode(sec2Mode)

		if sec2Mode == ModeFlatCompact {
			cmds = compressed[sec2Offset+1 : sec2Offset+hdr.CmdStreamSize]
			b.CmdUnpackedBytes = len(cmds)
		} else if sec2Offset+5 <= len(compressed) {
			sec2OrigLen := int(binary.LittleEndian.Uint32(compressed[sec2Offset+1 : sec2Offset+5]))
			b.CmdUnpackedBytes = sec2OrigLen
			if sec2Mode == ModeFlat {
				if sec2Offset+5+sec2OrigLen <= len(compressed) {
					cmds = compressed[sec2Offset+5 : sec2Offset+5+sec2OrigLen]
				}
			} else {
				if cap(pad.cmdBuf) < sec2OrigLen+64 {
					pad.cmdBuf = bufkit.AlignedBytes(sec2OrigLen+64, bufkit.CacheLineSize)
				}
				n, errUnpack := UnpackStream(pad.cmdBuf[:sec2OrigLen], compressed[sec2Offset:sec2Offset+hdr.CmdStreamSize], &pad.table)
				if errUnpack == nil {
					cmds = pad.cmdBuf[:n]
				}
			}
		}

		if sec2Mode == ModeCommandEntropy && hdr.CmdStreamSize >= 13 {
			cOpLen := int(binary.LittleEndian.Uint32(compressed[sec2Offset+5 : sec2Offset+9]))
			cMetaLen := int(binary.LittleEndian.Uint32(compressed[sec2Offset+9 : sec2Offset+13]))
			cVarLen := hdr.CmdStreamSize - 13 - cOpLen - cMetaLen
			b.CmdOpBytes = cOpLen
			b.CmdMetaBytes = cMetaLen
			b.CmdVarBytes = cVarLen
		}
	}

	if len(cmds) > 0 {
		cmdPtr := 0
		cmdEnd := len(cmds)
		for cmdPtr < cmdEnd {
			op := cmds[cmdPtr]
			cmdPtr++
			b.TotalCommands++

			if op < 0x80 {
				litLen := int((op >> 4) & 0x07)
				mLen := int((op>>2)&0x03) + minMatchBase
				k := int(op & 0x03)
				b.Cat00RepeatCount++
				switch k {
				case 0:
					b.Cat00Rep0Count++
				case 1:
					b.Cat00Rep1Count++
				case 2:
					b.Cat00Rep2Count++
				case 3:
					b.Cat00Rep3Count++
				}
				b.TotalMatches++
				b.TotalMatchBytes += mLen
				b.TotalLitBytes += litLen
				continue
			}

			if op < 0xC0 {
				litLen := int((op >> 4) & 0x03)
				baseLen := int(op & 0x0F)
				mLen := baseLen + minMatchBase
				if baseLen == 15 {
					extra, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
					if errVar == nil {
						cmdPtr = nextPtr
						mLen = 15 + minMatchBase + extra
					}
				}
				b.Cat10ExtCount++
				b.TotalMatches++
				b.TotalMatchBytes += mLen
				b.TotalLitBytes += litLen
				continue
			}

			switch {
			case op >= OpcodePrefixLongRepeat && op <= OpcodePrefixLongRepeat+3:
				if cmdPtr < cmdEnd {
					token := cmds[cmdPtr]
					cmdPtr++
					litLen := int((token >> 4) & 0x0F)
					baseLen := int(token & 0x0F)
					mLen := baseLen + minMatchBase
					if baseLen == 15 {
						extra, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
							mLen = 15 + minMatchBase + extra
						}
					}
					b.CatLongRepCount++
					b.TotalMatches++
					b.TotalMatchBytes += mLen
					b.TotalLitBytes += litLen
				}
			case op >= OpcodePrefixFusedBracket && op <= OpcodePrefixFusedBracket+9:
				if cmdPtr < cmdEnd {
					token := cmds[cmdPtr]
					cmdPtr++
					litLen := int(token >> 4)
					baseLen := int(token & 0x0F)
					mLen := baseLen + minMatchBase
					if baseLen == 15 {
						extra, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
							mLen = 15 + minMatchBase + extra
						}
					}
					b.CatBracketCount++
					b.TotalMatches++
					b.TotalMatchBytes += mLen
					b.TotalLitBytes += litLen
				}
			case op == OpcodePrefixDiffLattice:
				if cmdPtr+1 < cmdEnd {
					desc := cmds[cmdPtr]
					cmdPtr += 2
					litLen := int((desc >> 3) & 7)
					baseLen := int(desc & 7)
					mLen := baseLen + minMatchBase
					if litLen == 7 {
						_, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
						}
					}
					if baseLen == 7 {
						extra, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
							mLen += extra
						}
					}
					b.CatDeltaLocCount++
					b.TotalMatches++
					b.TotalMatchBytes += mLen
					b.TotalLitBytes += litLen
				}
			case op == OpcodePrefixRFCTransform:
				if cmdPtr < cmdEnd {
					b0 := cmds[cmdPtr]
					cmdPtr++
					if b0 >= 128 {
						if cmdPtr < cmdEnd {
							cmdPtr++
						}
					} else {
						_, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
						}
					}
					b.CatRFCTransCount++
					b.TotalMatches++
				}
			case op >= OpcodePrefixStride && op <= OpcodePrefixStride+7:
				if cmdPtr < cmdEnd {
					token := cmds[cmdPtr]
					cmdPtr++
					litLen := int((token >> 4) & 0x0F)
					mLen := int(token&0x0F) + minMatchBase
					b.Cat01StrideCount++
					b.TotalMatches++
					b.TotalMatchBytes += mLen
					b.TotalLitBytes += litLen
				}
			case op >= OpcodePrefixShortDict && op <= OpcodePrefixShortDict+7:
				if cmdPtr < cmdEnd {
					cmdPtr++
					b.CatRawDictCount++
					b.TotalMatches++
				}
			case op >= OpcodePrefixDeltaRing && op <= OpcodePrefixDeltaRing+7:
				if cmdPtr < cmdEnd {
					baseLen := int(cmds[cmdPtr])
					cmdPtr++
					mLen := baseLen + minMatchBase
					if baseLen == 255 {
						extra, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
							mLen = 259 + extra
						}
					}
					b.CatDeltaRingCount++
					b.TotalMatches++
					b.TotalMatchBytes += mLen
				}
			case op == OpcodePrefixDeltaExpl:
				if cmdPtr < cmdEnd {
					baseLen := int(cmds[cmdPtr])
					cmdPtr++
					mLen := baseLen + minMatchBase
					if baseLen == 255 {
						extra, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
						if errVar == nil {
							cmdPtr = nextPtr
							mLen = 259 + extra
						}
					}
					b.CatDeltaExpCount++
					b.TotalMatches++
					b.TotalMatchBytes += mLen
				}
			case op == OpcodePrefixLiteral:
				runLen, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
				if errVar == nil {
					cmdPtr = nextPtr
					b.Cat11LitRunCount++
					b.TotalLitBytes += runLen
				}
			case op == OpcodePrefixRLE:
				rleLen, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
				if errVar == nil {
					cmdPtr = nextPtr
					b.Cat10RLECount++
					b.TotalMatches++
					b.TotalMatchBytes += rleLen
				}
			case op >= OpcodePrefixLitShort && op <= OpcodePrefixLitShort+7:
				litLen := int(op - OpcodePrefixLitShort + 1)
				b.Cat11LitRunCount++
				b.TotalLitBytes += litLen
			case op == OpCopyBlock:
				cmdPtr += 4
				b.CatDedupCount++
				b.TotalMatches++
				b.TotalMatchBytes += DedupBlockSize
			case op == OpGrammarBlock:
				pwsLen, nextPtr, errVar := fastReadVarint(cmds, cmdPtr)
				if errVar == nil {
					cmdPtr = nextPtr + pwsLen
					b.CatGrammarCount++
				}
			case op == OpcodePrefixTransPair:
				cmdPtr += 2
				b.CatTokenPairCount++
				b.TotalMatches += 2
			}
		}
	}

	// -------------------------------------------------------------------------
	// 2. Resolve & Analyze Section 3 (Offsets)
	// -------------------------------------------------------------------------
	if hdr.OffsetStreamSize > 0 && sec3Offset < len(compressed) {
		sec3Mode := compressed[sec3Offset]
		b.OffsetMode = sec3Mode
		b.OffsetModeName = describeStreamMode(sec3Mode)

		var offs []byte
		if sec3Mode == ModeFlatCompact {
			offs = compressed[sec3Offset+1 : sec3Offset+hdr.OffsetStreamSize]
			b.OffsetUnpackedBytes = len(offs)
		} else if sec3Mode == ModeDistancePrefixByte {
			sec3OrigLen := int(compressed[sec3Offset+1])
			b.OffsetUnpackedBytes = sec3OrigLen
			if cap(pad.offBuf) < sec3OrigLen+64 {
				pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
			}
			n, errUnpack := UnpackStream(pad.offBuf[:sec3OrigLen], compressed[sec3Offset:sec3Offset+hdr.OffsetStreamSize], &pad.table)
			if errUnpack == nil {
				offs = pad.offBuf[:n]
			}
		} else if sec3Mode == ModeDistancePrefixCompact {
			if sec3Offset+3 <= len(compressed) {
				sec3OrigLen := int(binary.LittleEndian.Uint16(compressed[sec3Offset+1 : sec3Offset+3]))
				b.OffsetUnpackedBytes = sec3OrigLen
				if cap(pad.offBuf) < sec3OrigLen+64 {
					pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
				}
				n, errUnpack := UnpackStream(pad.offBuf[:sec3OrigLen], compressed[sec3Offset:sec3Offset+hdr.OffsetStreamSize], &pad.table)
				if errUnpack == nil {
					offs = pad.offBuf[:n]
				}
			}
		} else if sec3Offset+5 <= len(compressed) {
			sec3OrigLen := int(binary.LittleEndian.Uint32(compressed[sec3Offset+1 : sec3Offset+5]))
			b.OffsetUnpackedBytes = sec3OrigLen
			switch sec3Mode {
			case ModeFlat:
				if sec3Offset+5+sec3OrigLen <= len(compressed) {
					offs = compressed[sec3Offset+5 : sec3Offset+5+sec3OrigLen]
				}
			case ModeDistancePrefix:
				if cap(pad.offBuf) < sec3OrigLen+64 {
					pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
				}
				n, errDec := DecodeDistancePrefix(pad.offBuf[:sec3OrigLen], compressed[sec3Offset:sec3Offset+hdr.OffsetStreamSize], &pad.table, pad.scanlineBuf)
				if errDec == nil {
					offs = pad.offBuf[:n]
				}
			default:
				if cap(pad.offBuf) < sec3OrigLen+64 {
					pad.offBuf = bufkit.AlignedBytes(sec3OrigLen+64, bufkit.CacheLineSize)
				}
				n, errUnpack := UnpackStream(pad.offBuf[:sec3OrigLen], compressed[sec3Offset:sec3Offset+hdr.OffsetStreamSize], &pad.table)
				if errUnpack == nil {
					offs = pad.offBuf[:n]
				}
			}
		}

		if len(offs) > 0 {
			offPtr := 0
			count := 0
			totalDist := int64(0)
			seenDist := make(map[int]bool)
			distHist := make(map[int]int)

			for offPtr < len(offs) {
				d, nextPtr, errVar := fastReadVarint(offs, offPtr)
				if errVar != nil {
					break
				}
				offPtr = nextPtr
				count++
				totalDist += int64(d)
				seenDist[d] = true
				distHist[d]++
			}
			b.MatchDistCount = count
			b.DistinctOffsets = len(seenDist)
			if count > 0 {
				b.AvgDistance = float64(totalDist) / float64(count)
			}

			// Distance Shannon Entropy
			distEntropy := 0.0
			n := float64(count)
			for _, freq := range distHist {
				p := float64(freq) / n
				distEntropy -= p * math.Log2(p)
			}
			b.DistanceEntropy = distEntropy
		}
	}

	if b.TotalMatches > 0 {
		b.RepHitRatio = float64(b.Cat00RepeatCount+b.CatLongRepCount) / float64(b.TotalMatches) * 100.0
	}

	// -------------------------------------------------------------------------
	// 3. Resolve & Analyze Section 4 (Literals)
	// -------------------------------------------------------------------------
	literalBuf := compressed[sec4Offset:]
	var rawLits []byte

	if (hdr.Flags & FlagNibble) != 0 {
		b.LiteralMode = "Nibble-Planes"
		if unpacked, errUnpack := unpackLiterals(literalBuf, pad); errUnpack == nil {
			rawLits = unpacked
		}
	} else if len(literalBuf) >= 4 && literalBuf[0] == vansMagic0 && literalBuf[1] == vansMagic1 && literalBuf[2] == vansVer1 {
		vansFlags := literalBuf[3]
		switch {
		case (vansFlags & vansFlagCtxOrder2) != 0:
			b.LiteralMode = "256-Context Markov Sheaf V-ANS"
		case (vansFlags & vansFlagOpcodeOrder2) != 0:
			b.LiteralMode = "8-Context Bigram V-ANS"
		default:
			b.LiteralMode = "4-Way Interleaved Vector-State ANS"
		}
		if hdr.UncompressedSize > 0 && len(literalBuf) >= 8 {
			origLitLen := int(binary.LittleEndian.Uint32(literalBuf[4:8]))
			if cap(pad.litBuf) < origLitLen+64 {
				pad.litBuf = bufkit.AlignedBytes(origLitLen+64, bufkit.CacheLineSize)
			}
			if decLits, errDec := VANSDecode4xTo(pad.litBuf[:origLitLen], literalBuf, nil); errDec == nil {
				rawLits = decLits
			}
		}
	} else {
		b.LiteralMode = "Raw / Flat Literals"
		rawLits = literalBuf
	}

	b.LiteralCount = len(rawLits)
	if len(rawLits) > 0 {
		var freq [256]int
		for _, c := range rawLits {
			freq[c]++
		}
		entropy := 0.0
		n := float64(len(rawLits))
		for _, count := range freq {
			if count > 0 {
				p := float64(count) / n
				entropy -= p * math.Log2(p)
			}
		}
		b.LiteralEntropy = entropy
		if len(literalBuf) > 0 {
			b.BitsPerLiteral = float64(len(literalBuf)*8) / n
			b.VANSGainPct = (1.0 - float64(len(literalBuf))/n) * 100.0
		}
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

// JSON returns a formatted JSON representation of the channel breakdown.
func (b *ChannelBreakdown) JSON() string {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}

// SummaryString formats the breakdown as a human-readable diagnostic report.
func (b *ChannelBreakdown) SummaryString() string {
	var sb strings.Builder

	sb.WriteString("================================================================================\n")
	sb.WriteString("                    PRZM FRAME DIAGNOSTIC & PROFILING REPORT                    \n")
	sb.WriteString("================================================================================\n")
	fmt.Fprintf(&sb, "  Uncompressed Size:  %10d B (%6.2f KB)\n", b.UncompressedSize, float64(b.UncompressedSize)/1024.0)
	fmt.Fprintf(&sb, "  Compressed Size:    %10d B (%6.2f KB)\n", b.CompressedSize, float64(b.CompressedSize)/1024.0)
	fmt.Fprintf(&sb, "  Compression Ratio:  %10.2fx (Space Saved: %.1f%%)\n", b.Ratio, b.SpaceSavedPct)
	fmt.Fprintf(&sb, "  Window Capacity:    %10d KB (Log2=%d)\n", b.WindowSizeKB, b.WindowLog)
	if b.DictID != 0 {
		fmt.Fprintf(&sb, "  Active Dictionary:  %s (0x%08X)\n", b.DictName, b.DictID)
	}
	if b.Stride > 1 {
		fmt.Fprintf(&sb, "  Periodicity Stride: %d bytes\n", b.Stride)
	}
	if b.NormMode != NormModeNone {
		fmt.Fprintf(&sb, "  Normalization Mode: %s\n", b.NormName)
	}
	fmt.Fprintf(&sb, "  Container Flags:    0x%02X [%s]\n", b.Flags, b.ActiveFlags)
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("CHANNEL BREAKDOWN & CONTAINER BUDGET:\n")

	renderBar := func(pct float64) string {
		bars := min(int(math.Round(pct/5.0)), 20)
		return "[" + strings.Repeat("■", bars) + strings.Repeat(" ", 20-bars) + "]"
	}

	fmt.Fprintf(&sb, "  [Section 1] Framing & Directory : %6d B (%5.1f%%)  %s\n", b.TotalFraming, b.FramingPct, renderBar(b.FramingPct))
	fmt.Fprintf(&sb, "  [Section 2] Commands Stream     : %6d B (%5.1f%%)  %s\n", b.CmdSize, b.CmdPct, renderBar(b.CmdPct))
	fmt.Fprintf(&sb, "  [Section 3] Offsets Channel     : %6d B (%5.1f%%)  %s\n", b.OffsetSize, b.OffsetPct, renderBar(b.OffsetPct))
	fmt.Fprintf(&sb, "  [Section 4] Literals Channel    : %6d B (%5.1f%%)  %s\n", b.LiteralSize, b.LiteralPct, renderBar(b.LiteralPct))
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("SECTION 2 (COMMANDS) EXECUTION PROFILE:\n")
	fmt.Fprintf(&sb, "  Encoding Mode:      %s\n", b.CmdModeName)
	if b.CmdMode == ModeCommandEntropy {
		fmt.Fprintf(&sb, "  Sub-Stream Sizes:   OpStream: %d B | MetaStream: %d B | VarStream: %d B\n",
			b.CmdOpBytes, b.CmdMetaBytes, b.CmdVarBytes)
	}
	fmt.Fprintf(&sb, "  Total Commands:     %d (Unpacked Commands: %d B)\n", b.TotalCommands, b.CmdUnpackedBytes)
	fmt.Fprintf(&sb, "  Total Matches:      %d (Covering %d match bytes)\n", b.TotalMatches, b.TotalMatchBytes)
	fmt.Fprintf(&sb, "  Total Literals:     %d bytes emitted from LZ engine\n", b.TotalLitBytes)
	fmt.Fprintf(&sb, "  Repeat Registers:   %d matches (%.1f%% Locality Hit Ratio)\n",
		b.Cat00RepeatCount+b.CatLongRepCount, b.RepHitRatio)
	fmt.Fprintf(&sb, "    • Rep0: %d | Rep1: %d | Rep2: %d | Rep3: %d\n",
		b.Cat00Rep0Count, b.Cat00Rep1Count, b.Cat00Rep2Count, b.Cat00Rep3Count)
	sb.WriteString("  Match Taxonomy Breakdown:\n")
	fmt.Fprintf(&sb, "    • Fast-Path 1 (Repeat Matches):   %6d\n", b.Cat00RepeatCount)
	fmt.Fprintf(&sb, "    • Fast-Path 2 (Extended Matches): %6d\n", b.Cat10ExtCount)
	fmt.Fprintf(&sb, "    • Fused Distance Brackets:        %6d\n", b.CatBracketCount)
	fmt.Fprintf(&sb, "    • RFC 7932 Dictionary Words:      %6d\n", b.CatRFCTransCount)
	fmt.Fprintf(&sb, "    • Compound Token Pairs:           %6d\n", b.CatTokenPairCount)
	fmt.Fprintf(&sb, "    • Raw Dictionary Matches:         %6d\n", b.CatRawDictCount)
	fmt.Fprintf(&sb, "    • Periodic Stride Matches:        %6d\n", b.Cat01StrideCount)
	fmt.Fprintf(&sb, "    • Delta Locality / Ring Matches:  %6d\n", b.CatDeltaLocCount+b.CatDeltaRingCount+b.CatDeltaExpCount)
	fmt.Fprintf(&sb, "    • RLE / Grammar / Dedup Blocks:   %6d\n", b.Cat10RLECount+b.CatGrammarCount+b.CatDedupCount)
	fmt.Fprintf(&sb, "    • Pure Literal Run Tokens:        %6d\n", b.Cat11LitRunCount)
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("SECTION 3 (OFFSETS) SPATIAL DISTRIBUTION:\n")
	fmt.Fprintf(&sb, "  Encoding Mode:      %s\n", b.OffsetModeName)
	fmt.Fprintf(&sb, "  Emitted Offsets:    %d offsets (Unpacked: %d B)\n", b.MatchDistCount, b.OffsetUnpackedBytes)
	fmt.Fprintf(&sb, "  Distinct Offsets:   %d distinct distances | Average Distance: %.1f B\n", b.DistinctOffsets, b.AvgDistance)
	fmt.Fprintf(&sb, "  Distance Entropy:   %.3f bits/symbol\n", b.DistanceEntropy)
	sb.WriteString("--------------------------------------------------------------------------------\n")
	sb.WriteString("SECTION 4 (LITERALS) ENTROPY & V-ANS PROFILE:\n")
	fmt.Fprintf(&sb, "  Encoding Mode:      %s\n", b.LiteralMode)
	fmt.Fprintf(&sb, "  Literal Count:      %d bytes (Compressed: %d B)\n", b.LiteralCount, b.LiteralSize)
	fmt.Fprintf(&sb, "  Shannon Entropy:    %.3f bits/byte (Ideal: %d B)\n", b.LiteralEntropy, b.PotentialLiteralBytes)
	fmt.Fprintf(&sb, "  Effective Bitrate:  %.3f bits/literal (V-ANS Efficiency Gain: %.1f%%)\n", b.BitsPerLiteral, b.VANSGainPct)
	sb.WriteString("================================================================================\n")

	return sb.String()
}
