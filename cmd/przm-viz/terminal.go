// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// ANSI color codes
var (
	colorReset   = "\033[0m"
	colorBold    = "\033[1m"
	colorDim     = "\033[2m"
	colorCyan    = "\033[36m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorMagenta = "\033[35m"
	colorGray    = "\033[90m"

	// High intensity colors
	colorBrightCyan    = "\033[96m"
	colorBrightGreen   = "\033[92m"
	colorBrightYellow  = "\033[93m"
	colorBrightMagenta = "\033[95m"
	colorBrightWhite   = "\033[97m"
)

func initColors(noColor bool) {
	if noColor || os.Getenv("NO_COLOR") != "" {
		colorReset = ""
		colorBold = ""
		colorDim = ""
		colorCyan = ""
		colorGreen = ""
		colorYellow = ""
		colorMagenta = ""
		colorGray = ""
		colorBrightCyan = ""
		colorBrightGreen = ""
		colorBrightYellow = ""
		colorBrightMagenta = ""
		colorBrightWhite = ""
	}
}

// RenderTerminal displays a rich, structured visual analysis to stdout.
func RenderTerminal(w io.Writer, r *AnalysisReport, filename string, dumpLimit int) {
	// 1. Header Banner
	renderHeaderBanner(w, r, filename)

	for _, frame := range r.Frames {
		if r.IsStream {
			fmt.Fprintf(w, "\n%s═══ CHUNK #%d (Uncompressed: %s, Compressed: %s, Ratio: %.2fx) ═══%s\n",
				colorBrightCyan, frame.FrameIndex+1,
				formatBytes(frame.UncompressedSize), formatBytes(frame.CompressedSize),
				frame.CompressionRatio, colorReset)
		}

		// 2. Frame Architecture & Metadata Box
		renderFrameMetadata(w, &frame)

		// 3. Section Treemap Stacked Bar
		renderSectionTreemap(w, &frame)

		// 4. Section Details Table
		renderSectionTable(w, &frame)

		// 5. Opcode Family Distribution
		renderOpcodeFamilies(w, &frame)

		// 6. Top Opcode Disassembly Table
		renderTopOpcodes(w, &frame)

		// 7. Offset Locality & Distance Distribution
		renderOffsetDistribution(w, &frame)

		// 8. File Timeline Density Heatmap
		renderTimeline(w, &frame)

		// 9. Command Disassembly Dump (if requested)
		if dumpLimit > 0 && len(frame.Commands) > 0 {
			renderCommandDump(w, &frame, dumpLimit)
		}
	}

	fmt.Fprintln(w)
}

func renderHeaderBanner(w io.Writer, r *AnalysisReport, filename string) {
	fmt.Fprintf(
		w,
		"\n%s%s╔══════════════════════════════════════════════════════════════════════════════════════════════════════════╗%s\n",
		colorBold,
		colorBrightCyan,
		colorReset,
	)
	title := "PRZM DEEP BITSTREAM VISUALIZER & CODEC ANALYZER — " + filename
	if len(title) > 96 {
		title = title[:93] + "..."
	}
	fmt.Fprintf(w, "%s%s║  %-96s║%s\n", colorBold, colorBrightCyan, title, colorReset)
	fmt.Fprintf(
		w,
		"%s%s╠══════════════════════════════════════════════════════════════════════════════════════════════════════════╣%s\n",
		colorBold,
		colorBrightCyan,
		colorReset,
	)

	stats := fmt.Sprintf(
		"Raw Size: %s  │  Compressed: %s  │  Ratio: %s%.2fx%s  │  Saved: %s+%.1f%%%s  │  Bitrate: %.2f bpc",
		formatBytes(int(r.TotalUSize)),
		formatBytes(int(r.TotalCSize)),
		colorBrightGreen,
		r.OverallRatio,
		colorReset,
		colorBrightGreen,
		r.OverallSavings,
		colorReset,
		r.OverallBPC,
	)
	fmt.Fprintf(w, "║  %-96s║\n", stats)
	fmt.Fprintf(
		w,
		"%s%s╚══════════════════════════════════════════════════════════════════════════════════════════════════════════╝%s\n",
		colorBold,
		colorBrightCyan,
		colorReset,
	)
}

func renderFrameMetadata(w io.Writer, f *FrameAnalysis) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [1] FRAME ARCHITECTURE & METADATA %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 62)+colorReset,
	)
	fmt.Fprintf(w, "│ %sMagic:%s %s (%s)      %sWindow:%s %s (log2=%d)      %sStride:%s %d      %sDictID:%s 0x%04X\n",
		colorCyan, colorReset, f.Magic, "PRZM-SPEC-0.5",
		colorCyan, colorReset, formatBytes(f.WindowSize), f.WindowLog,
		colorCyan, colorReset, f.Stride,
		colorCyan, colorReset, f.DictID,
	)
	fmt.Fprintf(w, "│ %sNormMode:%s %s      %sFlags (0x%02X):%s %s\n",
		colorCyan, colorReset, f.NormModeName,
		colorCyan, f.Flags, colorReset, strings.Join(f.FlagNames, ", "),
	)
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderSectionTreemap(w io.Writer, f *FrameAnalysis) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [2] COMPRESSED STREAM PROPORTIONS (TREEMAP BAR) %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 48)+colorReset,
	)

	// Create 80-char stacked bar
	const barWidth = 84
	var secSizes [4]int
	var secPcts [4]float64
	for _, s := range f.Sections {
		if s.Index >= 1 && s.Index <= 4 {
			secSizes[s.Index-1] = s.PackedSize
			secPcts[s.Index-1] = s.Percentage
		}
	}

	totalCompressed := f.CompressedSize
	if totalCompressed <= 0 {
		totalCompressed = 1
	}

	chars := make([]string, 0, barWidth)
	colors := []string{colorMagenta, colorCyan, colorGreen, colorYellow}
	fillSymbols := []string{"■", "▓", "▒", "░"}

	allocated := 0
	for i := range 4 {
		width := int(math.Round(float64(secSizes[i]) / float64(totalCompressed) * float64(barWidth)))
		if secSizes[i] > 0 && width == 0 {
			width = 1
		}
		if allocated+width > barWidth {
			width = barWidth - allocated
		}
		for j := 0; j < width; j++ {
			chars = append(chars, fmt.Sprintf("%s%s%s", colors[i], fillSymbols[i], colorReset))
		}
		allocated += width
	}
	for len(chars) < barWidth {
		chars = append(chars, " ")
	}

	fmt.Fprintf(w, "│ [%s]\n", strings.Join(chars, ""))
	fmt.Fprintf(
		w,
		"│  %s■ Sec 1 (Headers): %.1f%%%s  │  %s▓ Sec 2 (Commands): %.1f%%%s  │  %s▒ Sec 3 (Offsets): %.1f%%%s  │  %s░ Sec 4 (Literals): %.1f%%%s\n",
		colorMagenta,
		secPcts[0],
		colorReset,
		colorCyan,
		secPcts[1],
		colorReset,
		colorGreen,
		secPcts[2],
		colorReset,
		colorYellow,
		secPcts[3],
		colorReset,
	)
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderSectionTable(w io.Writer, f *FrameAnalysis) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [3] SECTION CODEC & ENTROPY BREAKDOWN %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 57)+colorReset,
	)
	fmt.Fprintf(w, "│ %-34s │ %-10s │ %-10s │ %-8s │ %-6s │ %-24s\n",
		"Section Name", "Packed", "Unpacked", "Share", "Ratio", "Entropy Packaging Codec")
	fmt.Fprintf(
		w,
		"├────────────────────────────────────┼────────────┼────────────┼──────────┼────────┼──────────────────────────\n",
	)

	for _, s := range f.Sections {
		ratioStr := fmt.Sprintf("%.2fx", s.Ratio)
		if s.PackedSize == 0 {
			ratioStr = "N/A"
		}
		fmt.Fprintf(w, "│ %-34s │ %10s │ %10s │ %6.1f%%   │ %6s │ %s%-24s%s\n",
			s.Name,
			formatBytes(s.PackedSize),
			formatBytes(s.RawSize),
			s.Percentage,
			ratioStr,
			colorDim, s.ModeName, colorReset,
		)
	}
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderOpcodeFamilies(w io.Writer, f *FrameAnalysis) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [4] OPCODES & GRAMMAR CATEGORY COMPOSITION %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 52)+colorReset,
	)
	fmt.Fprintf(w, "│ Total Commands: %s%d%s  │  Shannon Entropy: %s%.2f bits/cmd%s  │  Bitrate: %s%.2f bpc%s\n",
		colorBrightCyan, f.TotalCommands, colorReset,
		colorBrightYellow, f.CommandEntropy, colorReset,
		colorBrightGreen, f.AvgBitsPerCmd, colorReset,
	)
	fmt.Fprintf(
		w,
		"├──────────────────────────────────────────────────────────────────────────────────────────────────\n",
	)

	families := []OpcodeFamily{
		FamilyFusedRepeat,
		FamilyFusedExtended,
		FamilyLongRepeat,
		FamilyFusedBracket,
		FamilyFusedMicro,
		FamilyMatchWalk,
		FamilyStride,
		FamilyLiteralRun,
		FamilyRLE,
		FamilyCopyBlock,
		FamilyDictEntry,
		FamilyOther,
	}

	const maxBarLen = 38
	for _, fam := range families {
		count := f.FamilyCounts[fam]
		if count == 0 {
			continue
		}
		pct := f.FamilyPct[fam]
		barLen := int(math.Round(pct / 100.0 * float64(maxBarLen)))
		if barLen == 0 && count > 0 {
			barLen = 1
		}
		bar := strings.Repeat("█", barLen) + strings.Repeat("░", maxBarLen-barLen)

		col := colorCyan
		if strings.Contains(string(fam), "Repeat") {
			col = colorBrightGreen
		} else if strings.Contains(string(fam), "Walk") {
			col = colorBrightMagenta
		} else if strings.Contains(string(fam), "Literal") {
			col = colorYellow
		} else if strings.Contains(string(fam), "Micro") || strings.Contains(string(fam), "Bracket") {
			col = colorBrightCyan
		}

		fmt.Fprintf(w, "│ %-30s │ %s%s%s │ %6d tokens │ %5.1f%%\n",
			fam, col, bar, colorReset, count, pct)
	}
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderTopOpcodes(w io.Writer, f *FrameAnalysis) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [5] TOP-10 OPCODES IN COMMAND STREAM (DEEP DISASSEMBLY) %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 39)+colorReset,
	)
	fmt.Fprintf(w, "│ %-6s │ %-10s │ %-40s │ %-8s │ %-6s\n",
		"Hex", "Bitmask", "Semantic Token Template", "Count", "Share")
	fmt.Fprintf(w, "├────────┼────────────┼──────────────────────────────────────────┼──────────┼────────\n")

	for i, s := range f.TopOpcodes {
		if i >= 10 {
			break
		}
		template := s.Template
		if template == "" {
			template = fmt.Sprintf("%08b", s.Opcode)
		}
		fmt.Fprintf(w, "│ %s%-6s%s │ %s%-10s%s │ %-40s │ %8d │ %5.1f%%\n",
			colorBrightCyan, s.Hex, colorReset,
			colorDim, template, colorReset,
			s.Name, s.Count, s.Percentage,
		)
	}
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderOffsetDistribution(w io.Writer, f *FrameAnalysis) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [6] MATCH LOCALITY & OFFSET DISTANCE DISTRIBUTION %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 45)+colorReset,
	)
	fmt.Fprintf(
		w,
		"│ Total Matches: %s%d%s  │  Repeat Queue Hits: %s%d (%.1f%%)%s  │  Fused Brackets: %s%d (%.1f%%)%s\n",
		colorBrightCyan,
		f.TotalMatches,
		colorReset,
		colorBrightGreen,
		f.RepeatHits,
		f.RepeatHitsPct,
		colorReset,
		colorBrightCyan,
		f.BracketHits,
		f.BracketHitsPct,
		colorReset,
	)
	fmt.Fprintf(w, "│ Explicit Varints (Section 3): %s%d (%.1f%%)%s\n",
		colorYellow, f.ExplicitHits, f.ExplicitHitsPct, colorReset)
	fmt.Fprintf(
		w,
		"├──────────────────────────────────────────────────────────────────────────────────────────────────\n",
	)

	const maxBarLen = 34
	for _, b := range f.OffsetBuckets {
		barLen := int(math.Round(b.Percentage / 100.0 * float64(maxBarLen)))
		if barLen == 0 && b.Count > 0 {
			barLen = 1
		}
		bar := strings.Repeat("█", barLen) + strings.Repeat("░", maxBarLen-barLen)

		fmt.Fprintf(w, "│ %-45s │ %s%s%s │ %6d matches │ %5.1f%%\n",
			b.Label, colorGreen, bar, colorReset, b.Count, b.Percentage)
	}
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderTimeline(w io.Writer, f *FrameAnalysis) {
	if len(f.Timeline) == 0 {
		return
	}
	fmt.Fprintf(
		w,
		"\n%s%s┌── [7] UNCOMPRESSED STREAM COMPOSITION TIMELINE (64 QUANTILES) %s\n",
		colorBold,
		colorBrightWhite,
		strings.Repeat("─", 36)+colorReset,
	)

	var sb strings.Builder
	for _, seg := range f.Timeline {
		if seg.RepeatPct > 50 {
			fmt.Fprintf(&sb, "%s█%s", colorBrightGreen, colorReset)
		} else if seg.MatchPct > 50 {
			fmt.Fprintf(&sb, "%s▓%s", colorCyan, colorReset)
		} else if seg.LiteralPct > 50 {
			fmt.Fprintf(&sb, "%s░%s", colorYellow, colorReset)
		} else {
			fmt.Fprintf(&sb, "%s▒%s", colorGray, colorReset)
		}
	}
	fmt.Fprintf(w, "│ 0%% [%s] 100%%\n", sb.String())
	fmt.Fprintf(w, "│ Legend: %s█ Repeat Match%s  │  %s▓ Explicit Match%s  │  %s░ Raw Literal%s  │  %s▒ Mixed%s\n",
		colorBrightGreen, colorReset, colorCyan, colorReset, colorYellow, colorReset, colorGray, colorReset)
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func renderCommandDump(w io.Writer, f *FrameAnalysis, limit int) {
	fmt.Fprintf(
		w,
		"\n%s%s┌── [8] DISASSEMBLED COMMAND STREAM (FIRST %d TOKENS) %s\n",
		colorBold,
		colorBrightWhite,
		limit,
		strings.Repeat("─", 44)+colorReset,
	)
	fmt.Fprintf(w, "│ %-6s │ %-6s │ %-24s │ %-54s\n", "Pos", "Hex", "Family", "Decoded Action & Operands")
	fmt.Fprintf(
		w,
		"├────────┼────────┼──────────────────────────┼────────────────────────────────────────────────────────\n",
	)

	count := min(limit, len(f.Commands))
	for i := range count {
		cmd := f.Commands[i]
		fmt.Fprintf(w, "│ #%-5d │ %s%-6s%s │ %-24s │ %s%-54s%s\n",
			cmd.Offset,
			colorBrightCyan, cmd.Hex, colorReset,
			cmd.Family,
			colorBrightWhite, cmd.Description, colorReset,
		)
	}
	if len(f.Commands) > count {
		fmt.Fprintf(w, "│ ... and %d more commands (use -d %d to dump all)\n",
			len(f.Commands)-count, len(f.Commands))
	}
	fmt.Fprintf(w, "%s└──%s%s\n", colorBold, strings.Repeat("─", 98), colorReset)
}

func formatBytes(b int) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.1f KiB", float64(b)/1024.0)
	}
	return fmt.Sprintf("%.2f MiB", float64(b)/(1024.0*1024.0))
}
