// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lemon4ksan/przm"
)

const (
	versionStr = "przm-viz v1.0.0 (PRZM Engine 0.9.x-season11)"
)

func main() {
	var (
		compressLevel int
		htmlPath      string
		jsonOutput    bool
		dumpLimit     int
		noColor       bool
		showVersion   bool
	)

	flag.IntVar(&compressLevel, "c", 0, "Compress raw input at level (1-6) before analysis (default: auto-detect)")
	flag.IntVar(&compressLevel, "compress", 0, "Compress raw input at level (1-6) before analysis")
	flag.StringVar(&htmlPath, "o", "", "Export standalone interactive HTML report to path")
	flag.StringVar(&htmlPath, "html", "", "Export standalone interactive HTML report to path")
	flag.BoolVar(&jsonOutput, "j", false, "Output machine-readable JSON diagnostics to stdout")
	flag.BoolVar(&jsonOutput, "json", false, "Output machine-readable JSON diagnostics to stdout")
	flag.IntVar(&dumpLimit, "d", 0, "Disassemble and dump first N commands (e.g. -d 50)")
	flag.IntVar(&dumpLimit, "dump", 0, "Disassemble and dump first N commands")
	flag.BoolVar(&noColor, "no-color", false, "Disable ANSI color output in terminal")
	flag.BoolVar(&showVersion, "v", false, "Print version information and exit")
	flag.BoolVar(&showVersion, "version", false, "Print version information and exit")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "%s\n", versionStr)
		fmt.Fprintf(os.Stderr, "Deep visual inspection & diagnostic disassembly for PRZM compressed bitstreams.\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  przm-viz [flags] <file.przm | file.przs | raw_file>\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  przm-viz archive.przm                     # Inspect compressed PRZM frame\n")
		fmt.Fprintf(os.Stderr, "  przm-viz archive.przs                     # Inspect multi-chunk PRZS stream\n")
		fmt.Fprintf(os.Stderr, "  przm-viz input.json -c 5                  # Compress with Level 5 and analyze\n")
		fmt.Fprintf(os.Stderr, "  przm-viz input.bin -o report.html         # Generate standalone HTML report\n")
		fmt.Fprintf(os.Stderr, "  przm-viz input.bin -d 40                  # Dump first 40 disassembled opcodes\n")
		fmt.Fprintf(os.Stderr, "  przm-viz input.bin --json | jq .          # Pipe JSON diagnostics to jq\n")
	}

	normalizeArgs()
	flag.Parse()

	if showVersion {
		fmt.Println(versionStr)
		os.Exit(0)
	}

	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(1)
	}

	initColors(noColor)

	filePath := flag.Arg(0)
	rawData, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %q: %v\n", filePath, err)
		os.Exit(1)
	}

	if len(rawData) == 0 {
		fmt.Fprintf(os.Stderr, "Error: file %q is empty\n", filePath)
		os.Exit(1)
	}

	var przmData []byte
	isRawCompressedOnFly := false
	startTime := time.Now()

	// Check if already PRZM or PRZS format
	isPRZM := false
	if len(rawData) >= 4 {
		magic := binary.LittleEndian.Uint32(rawData[0:4])
		if magic == przm.MagicPRZM || magic == przm.MagicPRZS {
			isPRZM = true
			przmData = rawData
		}
	}

	if !isPRZM {
		// Input is uncompressed data; compress it on the fly
		level := compressLevel
		if level < 1 || level > 6 {
			level = 5 // Default archival level
		}
		if !jsonOutput {
			fmt.Fprintf(os.Stderr, "%s[i] Detected raw data (%s). Compressing with PRZM Level %d...%s\n",
				colorCyan, formatBytes(len(rawData)), level, colorReset)
		}

		cStart := time.Now()
		cBuf, cErr := przm.Compress(rawData, level)
		if cErr != nil {
			fmt.Fprintf(os.Stderr, "Compression failed: %v\n", cErr)
			os.Exit(1)
		}
		przmData = cBuf
		isRawCompressedOnFly = true
		cDuration := time.Since(cStart)

		if !jsonOutput {
			ratio := float64(len(rawData)) / float64(len(cBuf))
			mbSec := (float64(len(rawData)) / (1024 * 1024)) / cDuration.Seconds()
			fmt.Fprintf(os.Stderr, "%s[i] Compressed in %v (%.1f MB/s) -> %s (%.2fx ratio)%s\n\n",
				colorGreen, cDuration, mbSec, formatBytes(len(cBuf)), ratio, colorReset)
		}
	}

	// Run deep architectural analysis
	report, err := AnalyzePRZM(przmData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Analysis failed: %v\n", err)
		os.Exit(1)
	}

	// Output JSON if requested
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(os.Stderr, "JSON encoding error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Render Rich Terminal UI
		displayName := filepath.Base(filePath)
		if isRawCompressedOnFly {
			displayName = fmt.Sprintf("%s (PRZM L%d)", displayName, compressLevel)
			if compressLevel < 1 || compressLevel > 6 {
				displayName = filepath.Base(filePath) + " (PRZM L5)"
			}
		}
		RenderTerminal(os.Stdout, report, displayName, dumpLimit)
	}

	// Export HTML report if specified
	if htmlPath != "" {
		f, err := os.Create(htmlPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create HTML report %q: %v\n", htmlPath, err)
			os.Exit(1)
		}

		if err := RenderHTML(f, report, filepath.Base(filePath)); err != nil {
			_ = f.Close()
			fmt.Fprintf(os.Stderr, "Failed to render HTML report: %v\n", err)
			os.Exit(1)
		}
		_ = f.Close()

		if !jsonOutput {
			fmt.Fprintf(
				os.Stdout,
				"%s[✓] Interactive HTML report successfully generated:%s %s%s%s (elapsed: %v)\n",
				colorBrightGreen,
				colorReset,
				colorBold,
				htmlPath,
				colorReset,
				time.Since(startTime).Round(time.Millisecond),
			)
		}
	}
}

func normalizeArgs() {
	if len(os.Args) <= 2 {
		return
	}
	args := os.Args[1:]
	var flags []string
	var pos []string
	i := 0
	takesVal := map[string]bool{
		"-c": true, "--c": true, "-compress": true, "--compress": true,
		"-o": true, "--o": true, "-html": true, "--html": true,
		"-d": true, "--d": true, "-dump": true, "--dump": true,
	}

	for i < len(args) {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "=") || !takesVal[arg] {
				flags = append(flags, arg)
				i++
			} else {
				flags = append(flags, arg)
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					flags = append(flags, args[i+1])
					i += 2
				} else {
					i++
				}
			}
		} else {
			pos = append(pos, arg)
			i++
		}
	}

	reordered := append([]string{os.Args[0]}, flags...)
	reordered = append(reordered, pos...)
	os.Args = reordered
}
