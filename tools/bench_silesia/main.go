// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

type FileResult struct {
	Name             string
	Category         string
	OriginalSize     int64
	CompressedSize   int64
	Ratio            float64
	Savings          float64
	CompressTime     time.Duration
	CompressSpeed    float64 // MB/s
	DecompGoTime     time.Duration
	DecompGoSpeed    float64 // MB/s
	DecompCTime      time.Duration
	DecompCSpeed     float64 // MB/s
	VerifiedGo       bool
	VerifiedC        bool
	Chunks           int
}

var fileCategories = map[string]string{
	"dickens": "English Literature (Text)",
	"mozilla": "x86 Tarred Executables (Binary)",
	"mr":      "Magnetic Resonance Image (Medical)",
	"nci":     "Chemical DB / SMILES (Text)",
	"ooffice": "OpenOffice Executable (Binary)",
	"osdb":    "PostgreSQL DB Dump (Binary)",
	"reymont": "Polish Literature (Text)",
	"samba":   "Samba Source Tarball (C/Code)",
	"sao":     "Star Catalogue / Astronomical (Binary)",
	"webster": "English Dictionary (Text)",
	"xml":     "XML Document Tree (Structured Text)",
	"x-ray":   "X-Ray Radiograph (Medical Image)",
}

const ChunkSize = 15 * 1024 * 1024 // 15 MiB chunk size for multi-frame support

func main() {
	dirFlag := flag.String("dir", "bench_data/silesia", "Directory containing Silesia files")
	levelsFlag := flag.String("levels", "1,3", "Comma-separated PRZM compression levels to test")
	compare7zFlag := flag.Bool("compare-7z", false, "Compare with 7-Zip LZMA2")
	flag.Parse()

	var levels []int
	for _, s := range strings.Split(*levelsFlag, ",") {
		s = strings.TrimSpace(s)
		if l, err := strconv.Atoi(s); err == nil {
			levels = append(levels, l)
		}
	}
	if len(levels) == 0 {
		levels = []int{1, 3}
	}

	files, err := discoverSilesiaFiles(*dirFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning Silesia directory %s: %v\n", *dirFlag, err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "No Silesia files found in %s\n", *dirFlag)
		os.Exit(1)
	}

	fmt.Printf("Discovered %d Silesia files in %s:\n", len(files), *dirFlag)
	var totalRawBytes int64
	for _, f := range files {
		info, _ := os.Stat(f)
		sz := info.Size()
		totalRawBytes += sz
		base := strings.ToLower(filepath.Base(f))
		desc := fileCategories[base]
		if desc == "" {
			desc = "General Data"
		}
		fmt.Printf("  %-10s : %10d bytes (%.2f MB) — %s\n", filepath.Base(f), sz, float64(sz)/(1024*1024), desc)
	}
	fmt.Printf("Total Corpus Size: %d bytes (%.2f MB)\n\n", totalRawBytes, float64(totalRawBytes)/(1024*1024))

	for _, lvl := range levels {
		runLevelBenchmark(files, lvl)
	}

	if *compare7zFlag {
		run7zComparison(files)
	}
}

func discoverSilesiaFiles(dir string) ([]string, error) {
	var files []string
	known := map[string]bool{
		"dickens": true, "mozilla": true, "mr": true, "nci": true,
		"ooffice": true, "osdb": true, "reymont": true, "samba": true,
		"sao": true, "webster": true, "xml": true, "x-ray": true,
	}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		base := strings.ToLower(d.Name())
		// Strip possible extensions if extracted as .tar or similar
		if known[base] || known[strings.TrimSuffix(base, filepath.Ext(base))] {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Sort deterministically
	sort.Slice(files, func(i, j int) bool {
		return filepath.Base(files[i]) < filepath.Base(files[j])
	})
	return files, nil
}

func runLevelBenchmark(files []string, level int) {
	fmt.Printf("========================================================================================================================\n")
	fmt.Printf("  PRZM Level %d Benchmark on Silesia Corpus (Go + Pure C Decompressors)\n", level)
	fmt.Printf("========================================================================================================================\n")
	fmt.Printf("%-10s | %10s | %10s | %7s | %6s | %10s | %12s | %12s | %s\n",
		"File", "Raw Size", "PRZM Size", "Ratio", "Saved", "Comp Speed", "Decomp (Go)", "Decomp (C)", "Verify (Go/C)")
	fmt.Println(strings.Repeat("-", 120))

	var totalRaw, totalComp int64
	var totalCompDur, totalDecompGoDur, totalDecompCDur time.Duration
	allPassed := true

	for _, path := range files {
		res, err := benchmarkSingleFile(path, level)
		if err != nil {
			fmt.Printf("%-10s | ERROR: %v\n", filepath.Base(path), err)
			allPassed = false
			continue
		}

		totalRaw += res.OriginalSize
		totalComp += res.CompressedSize
		totalCompDur += res.CompressTime
		totalDecompGoDur += res.DecompGoTime
		totalDecompCDur += res.DecompCTime
		if !res.VerifiedGo || !res.VerifiedC {
			allPassed = false
		}

		vStr := "PASS (100%)"
		if !res.VerifiedGo || !res.VerifiedC {
			vStr = fmt.Sprintf("FAIL (Go:%t, C:%t)", res.VerifiedGo, res.VerifiedC)
		}

		decompGoSpeedStr := formatSpeed(res.OriginalSize, res.DecompGoTime)
		decompCSpeedStr := formatSpeed(res.OriginalSize, res.DecompCTime)
		compSpeedStr := fmt.Sprintf("%.1f MB/s", res.CompressSpeed)

		fmt.Printf("%-10s | %10d | %10d | %6.2fx | %5.1f%% | %10s | %12s | %12s | %s\n",
			res.Name, res.OriginalSize, res.CompressedSize, res.Ratio, res.Savings,
			compSpeedStr, decompGoSpeedStr, decompCSpeedStr, vStr)
	}

	totalRatio := float64(totalRaw) / float64(totalComp)
	totalSavings := (1.0 - float64(totalComp)/float64(totalRaw)) * 100.0
	overallCompSpeed := (float64(totalRaw) / (1024 * 1024)) / totalCompDur.Seconds()
	overallDecompGoSpeedStr := formatSpeed(totalRaw, totalDecompGoDur)
	overallDecompCSpeedStr := formatSpeed(totalRaw, totalDecompCDur)

	fmt.Println(strings.Repeat("-", 120))
	fmt.Printf("%-10s | %10d | %10d | %6.2fx | %5.1f%% | %10.1f MB/s | %12s | %12s | %s\n",
		"TOTAL", totalRaw, totalComp, totalRatio, totalSavings, overallCompSpeed,
		overallDecompGoSpeedStr, overallDecompCSpeedStr,
		map[bool]string{true: "ALL VERIFIED (100% BIT-EXACT)", false: "FAILURES DETECTED"}[allPassed])
	fmt.Println()
}

func benchmarkSingleFile(path string, level int) (FileResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileResult{}, err
	}

	origSize := int64(len(data))
	var compSize int64
	var totalCompTime, totalDecompGoTime, totalDecompCTime time.Duration
	chunksCount := 0
	verifiedGo := true
	verifiedC := true

	// Split into chunks if exceeds MaxBlockSize
	for offset := 0; offset < len(data); offset += ChunkSize {
		end := min(offset+ChunkSize, len(data))
		chunk := data[offset:end]
		chunksCount++

		// Compress
		tStartComp := time.Now()
		comp, err := przm.Compress(chunk, level)
		tComp := time.Since(tStartComp)
		if err != nil {
			return FileResult{}, fmt.Errorf("compress error: %w", err)
		}
		totalCompTime += tComp
		compSize += int64(len(comp))

		// Decompress & verify (Go)
		hdr, err := przm.ParseFrameHeader(comp)
		if err != nil {
			return FileResult{}, fmt.Errorf("parse header error: %w", err)
		}

		dstGo := bufkit.AlignedBytes(int(hdr.UncompressedSize)+64, bufkit.CacheLineSize)
		decompGo, err := przm.DecompressTo(comp, dstGo)
		if err != nil {
			return FileResult{}, fmt.Errorf("go decompress error: %w", err)
		}
		if !bytes.Equal(chunk, decompGo) {
			verifiedGo = false
		}

		// Benchmark Go decompression speed
		runsGo := 3
		tStartDecompGo := time.Now()
		for range runsGo {
			_, _ = przm.DecompressTo(comp, dstGo)
		}
		avgDecompGo := time.Since(tStartDecompGo) / time.Duration(runsGo)
		totalDecompGoTime += avgDecompGo

		// Decompress & verify (Pure C libprzm_dec)
		dstC := bufkit.AlignedBytes(int(hdr.UncompressedSize)+64, bufkit.CacheLineSize)
		nC, err := decompressC(comp, dstC)
		if err != nil {
			return FileResult{}, fmt.Errorf("c decompress error: %w", err)
		}
		if nC != len(chunk) || !bytes.Equal(chunk, dstC[:nC]) {
			verifiedC = false
		}

		// Benchmark C decompression speed
		runsC := 5
		tStartDecompC := time.Now()
		for range runsC {
			_, _ = decompressC(comp, dstC)
		}
		avgDecompC := time.Since(tStartDecompC) / time.Duration(runsC)
		totalDecompCTime += avgDecompC
	}

	ratio := float64(origSize) / float64(compSize)
	savings := (1.0 - float64(compSize)/float64(origSize)) * 100.0
	compSpeed := (float64(origSize) / (1024 * 1024)) / totalCompTime.Seconds()
	decompGoSpeed := (float64(origSize) / (1024 * 1024)) / totalDecompGoTime.Seconds()
	decompCSpeed := (float64(origSize) / (1024 * 1024)) / totalDecompCTime.Seconds()

	return FileResult{
		Name:            filepath.Base(path),
		OriginalSize:    origSize,
		CompressedSize:  compSize,
		Ratio:           ratio,
		Savings:         savings,
		CompressTime:    totalCompTime,
		CompressSpeed:   compSpeed,
		DecompGoTime:    totalDecompGoTime,
		DecompGoSpeed:   decompGoSpeed,
		DecompCTime:     totalDecompCTime,
		DecompCSpeed:    decompCSpeed,
		VerifiedGo:      verifiedGo,
		VerifiedC:       verifiedC,
		Chunks:          chunksCount,
	}, nil
}

func formatSpeed(bytes int64, d time.Duration) string {
	if d <= 0 {
		return "N/A"
	}
	mbPerSec := (float64(bytes) / (1024 * 1024)) / d.Seconds()
	if mbPerSec >= 1024 {
		return fmt.Sprintf("%.2f GB/s", mbPerSec/1024)
	}
	return fmt.Sprintf("%.1f MB/s", mbPerSec)
}

func run7zComparison(files []string) {
	fmt.Printf("=========================================================================================\n")
	fmt.Printf("  7-Zip (LZMA2 -mx=3) Comparison on Silesia Corpus\n")
	fmt.Printf("=========================================================================================\n")
	var totalRaw, totalComp int64
	var totalTime time.Duration

	sevenZipPath := "7z"
	if _, err := exec.LookPath("7z"); err != nil {
		sevenZipPath = `C:\Program Files\7-Zip\7z.exe`
	}

	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		rawSz := info.Size()
		totalRaw += rawSz

		tmpOut := filepath.Join(os.TempDir(), fmt.Sprintf("lzma2_%s.7z", filepath.Base(path)))
		_ = os.Remove(tmpOut)

		tStart := time.Now()
		cmd := exec.Command(sevenZipPath, "a", "-t7z", "-m0=lzma2", "-mx=3", "-bso0", tmpOut, path)
		if err := cmd.Run(); err != nil {
			fmt.Printf("%-10s | 7z error: %v\n", filepath.Base(path), err)
			continue
		}
		dur := time.Since(tStart)
		totalTime += dur

		outInfo, err := os.Stat(tmpOut)
		_ = os.Remove(tmpOut)
		if err != nil {
			continue
		}
		cSz := outInfo.Size()
		totalComp += cSz

		ratio := float64(rawSz) / float64(cSz)
		speed := (float64(rawSz) / (1024 * 1024)) / dur.Seconds()
		fmt.Printf("%-10s | %10d | %10d | %6.2fx | %10.1f MB/s\n", filepath.Base(path), rawSz, cSz, ratio, speed)
	}
	totRatio := float64(totalRaw) / float64(totalComp)
	totSpeed := (float64(totalRaw) / (1024 * 1024)) / totalTime.Seconds()
	fmt.Printf("%-10s | %10d | %10d | %6.2fx | %10.1f MB/s\n", "TOTAL", totalRaw, totalComp, totRatio, totSpeed)
}
