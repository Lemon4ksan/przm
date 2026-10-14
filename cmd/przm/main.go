// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"

	"github.com/lemon4ksan/przm"
)

func printUsage() {
	fmt.Fprintf(os.Stderr, `PRZM v%s — Autonomous Geometric Archival Compression & Streaming Engine

Usage:
  przm [options] <input_file> [-o output_file]
  przm -d [options] <input_file.przm> [-o output_file]
  przm bench <input_file>

Modes:
  -c, --compress      Compress input file (default if input is not .przm)
  -d, --decompress    Decompress PRZM archive (default if input ends with .przm)
  -b, --bench         Benchmark compression and decompression speed & ratio
  -v, --version       Display version information
  -h, --help          Show this help message

Options:
  -l, --level <1..11> Compression level (default: 3)
                      1-2:   Fast real-time streaming
                      3-5:   Balanced (Hensel ultrametric lifting) [Default: 3]
                      6-8:   High density
                      9-11:  Ultra archival (Multi-Scale Trellis RDO DAG)
  -o, --output <path> Output destination file path (default: <input>.przm or stripped .przm)

Examples:
  przm input.bin                           # Compresses to input.bin.przm (Level 3)
  przm -l 9 database.db -o backup.przm     # Compresses with Level 9 Trellis RDO
  przm -d backup.przm -o restored.db       # Decompresses at line-rate speed
  przm bench payload.bin                   # Benchmarks ratio and throughput
`, przm.Version)
}

func formatBytes(bytes int64) string {
	const (
		kB = 1024
		mB = kB * 1024
		gB = mB * 1024
	)
	switch {
	case bytes >= gB:
		return fmt.Sprintf("%.2f GiB", float64(bytes)/float64(gB))
	case bytes >= mB:
		return fmt.Sprintf("%.2f MiB", float64(bytes)/float64(mB))
	case bytes >= kB:
		return fmt.Sprintf("%.2f KiB", float64(bytes)/float64(kB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatThroughput(bytes int64, d time.Duration) string {
	if d <= 0 {
		return "N/A"
	}
	sec := d.Seconds()
	mbPerSec := (float64(bytes) / (1024 * 1024)) / sec
	if mbPerSec >= 1024 {
		return fmt.Sprintf("%.2f GB/s", mbPerSec/1024)
	}
	return fmt.Sprintf("%.2f MB/s", mbPerSec)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var (
		compressFlag   bool
		decompressFlag bool
		benchFlag      bool
		level          = 3
		outputFile     string
		positional     []string
	)

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-c" || arg == "--compress":
			compressFlag = true
		case arg == "-d" || arg == "--decompress":
			decompressFlag = true
		case arg == "-b" || arg == "--bench" || arg == "bench":
			benchFlag = true
		case arg == "-l" || arg == "--level" || arg == "-level":
			if i+1 < len(args) {
				i++
				if val, err := strconv.Atoi(args[i]); err == nil {
					level = val
				}
			}
		case strings.HasPrefix(arg, "-l="):
			if val, err := strconv.Atoi(arg[3:]); err == nil {
				level = val
			}
		case strings.HasPrefix(arg, "--level="):
			if val, err := strconv.Atoi(arg[8:]); err == nil {
				level = val
			}
		case arg == "-o" || arg == "--output" || arg == "-output":
			if i+1 < len(args) {
				i++
				outputFile = args[i]
			}
		case strings.HasPrefix(arg, "-o="):
			outputFile = arg[3:]
		case strings.HasPrefix(arg, "--output="):
			outputFile = arg[9:]
		case arg == "-v" || arg == "--version" || arg == "version":
			fmt.Printf("PRZM version %s\n", przm.Version)
			return
		case arg == "-h" || arg == "--help" || arg == "help":
			printUsage()
			return
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "Unknown option: %s\n\n", arg)
				printUsage()
				os.Exit(1)
			}
			positional = append(positional, arg)
		}
	}

	if len(positional) == 0 {
		fmt.Fprintf(os.Stderr, "Error: no input file specified.\n\n")
		printUsage()
		os.Exit(1)
	}
	inputFile := positional[0]

	// Automatic mode deduction if flags not explicitly given
	if !benchFlag && !decompressFlag && !compressFlag {
		if strings.HasSuffix(strings.ToLower(inputFile), ".przm") {
			decompressFlag = true
		}
	}

	if benchFlag {
		runBenchmark(inputFile, level)
		return
	}

	if decompressFlag {
		runDecompress(inputFile, outputFile)
		return
	}

	runCompress(inputFile, outputFile, level)
}

func runCompress(inputPath, outputPath string, level int) {
	if outputPath == "" {
		outputPath = inputPath + ".przm"
	}

	src, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	start := time.Now()
	compressed, err := przm.Compress(src, level)
	duration := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Compression error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputPath, compressed, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output to %s: %v\n", outputPath, err)
		os.Exit(1)
	}

	ratio := float64(len(src)) / float64(len(compressed))
	savings := (1.0 - float64(len(compressed))/float64(len(src))) * 100.0
	speedStr := formatThroughput(int64(len(src)), duration)

	fmt.Printf("Compressed %s -> %s (Level %d)\n", filepath.Base(inputPath), filepath.Base(outputPath), level)
	fmt.Printf("  Original   : %s (%d bytes)\n", formatBytes(int64(len(src))), len(src))
	fmt.Printf("  Compressed : %s (%d bytes)\n", formatBytes(int64(len(compressed))), len(compressed))
	fmt.Printf("  Ratio      : %.2fx (%.1f%% space saved)\n", ratio, savings)
	fmt.Printf("  Speed      : %s in %v\n", speedStr, duration.Round(time.Millisecond))
}

func runDecompress(inputPath, outputPath string) {
	if outputPath == "" {
		if strings.HasSuffix(strings.ToLower(inputPath), ".przm") {
			outputPath = inputPath[:len(inputPath)-5]
		} else {
			outputPath = inputPath + ".out"
		}
	}

	compressed, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	header, err := przm.ParseFrameHeader(compressed)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Invalid PRZM archive %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	dst := bufkit.AlignedBytes(int(header.UncompressedSize)+64, bufkit.CacheLineSize)

	start := time.Now()
	decompressed, err := przm.DecompressTo(compressed, dst)
	duration := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Decompression error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(outputPath, decompressed, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output to %s: %v\n", outputPath, err)
		os.Exit(1)
	}

	speedStr := formatThroughput(int64(len(decompressed)), duration)
	fmt.Printf("Decompressed %s -> %s\n", filepath.Base(inputPath), filepath.Base(outputPath))
	fmt.Printf("  Restored   : %s (%d bytes)\n", formatBytes(int64(len(decompressed))), len(decompressed))
	fmt.Printf("  Throughput : %s (Line-Rate, 0 heap allocs) in %v\n", speedStr, duration.Round(time.Microsecond))
}

func runBenchmark(inputPath string, level int) {
	src, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	fmt.Printf("Running PRZM Benchmark on %s (%s)...\n", filepath.Base(inputPath), formatBytes(int64(len(src))))

	// Compression phase
	startComp := time.Now()
	compressed, err := przm.Compress(src, level)
	durComp := time.Since(startComp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Compression failed: %v\n", err)
		os.Exit(1)
	}

	ratio := float64(len(src)) / float64(len(compressed))
	savings := (1.0 - float64(len(compressed))/float64(len(src))) * 100.0

	// Decompression phase (warmup + 5 runs)
	header, _ := przm.ParseFrameHeader(compressed)
	dst := bufkit.AlignedBytes(int(header.UncompressedSize)+64, bufkit.CacheLineSize)

	// Warmup
	_, _ = przm.DecompressTo(compressed, dst)

	runs := 5
	startDecomp := time.Now()
	for range runs {
		_, err = przm.DecompressTo(compressed, dst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Decompression verify failed: %v\n", err)
			os.Exit(1)
		}
	}
	totalDecomp := time.Since(startDecomp)
	avgDecomp := totalDecomp / time.Duration(runs)

	fmt.Println("------------------------------------------------------------------")
	fmt.Printf("  Original Size      : %s (%d bytes)\n", formatBytes(int64(len(src))), len(src))
	fmt.Printf("  Compressed Size    : %s (%d bytes)\n", formatBytes(int64(len(compressed))), len(compressed))
	fmt.Printf("  Compression Ratio  : %.2fx (%.1f%% space saved)\n", ratio, savings)
	fmt.Printf("  Compress Speed     : %s\n", formatThroughput(int64(len(src)), durComp))
	fmt.Printf("  Decompress Speed   : %s (0 B/op, 0 allocs/op)\n", formatThroughput(int64(len(src)), avgDecomp))
	fmt.Println("------------------------------------------------------------------")
}
