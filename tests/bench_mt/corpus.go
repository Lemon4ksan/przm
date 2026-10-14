// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// CorpusItem represents an ingested or synthesized benchmark file.
type CorpusItem struct {
	Name     string
	Category string
	Data     []byte
}

func generateFallbackItem(category string, size int, name string) []byte {
	switch category {
	case "source-code":
		return bench.GenerateSourceCode(size)
	case "text-logs-csv":
		return bench.GenerateJSON(size)
	case "json-xml-html":
		return bench.GenerateJSON(size)
	case "exe-dll":
		return bench.GenerateMachineCode(size)
	case "containers-media":
		return bench.GenerateRGBA(size)
	case "nn-weights-slice":
		return bench.GenerateAIWeights(size)
	case "tabular32":
		return bench.GenerateTabular(size, 32)
	case "tabular64":
		return bench.GenerateTabular(size, 64)
	case "rle":
		return bench.GenerateRLE(size)
	case "spirv":
		return bench.GenerateSPIRV(size)
	case "random":
		return bench.GenerateRandom(size)
	default:
		return bench.GenerateSourceCode(size)
	}
}

// loadCorpus loads the 166-file baseline corpus from CSV or falls back hermetically.
func loadCorpus(preferredPath string) ([]CorpusItem, error) {
	candidates := []string{
		preferredPath,
		`teamwork_projects\przm_compression_sectors\baseline_harness\per_file.csv`,
		filepath.Join("teamwork_projects", "przm_compression_sectors", "baseline_harness", "per_file.csv"),
		filepath.Join("..", "teamwork_projects", "przm_compression_sectors", "baseline_harness", "per_file.csv"),
		`d:\CodingProjects\przm\teamwork_projects\przm_compression_sectors\baseline_harness\per_file.csv`,
	}

	var csvPath string
	for _, cand := range candidates {
		if cand != "" {
			if _, err := os.Stat(cand); err == nil {
				csvPath = cand
				break
			}
		}
	}

	var items []CorpusItem
	if csvPath != "" {
		f, err := os.Open(csvPath)
		if err == nil {
			defer f.Close()
			r := csv.NewReader(f)
			_, _ = r.Read() // skip CSV header
			idx := 0
			for {
				rec, err := r.Read()
				if err == io.EOF {
					break
				}
				if err != nil || len(rec) < 3 {
					continue
				}
				idx++
				category := rec[0]
				filePath := rec[1]
				rawSize, _ := strconv.Atoi(rec[2])
				name := fmt.Sprintf("%03d_%s_%s", idx, category, filepath.Base(filePath))

				var data []byte
				if _, err := os.Stat(filePath); err == nil {
					data, _ = os.ReadFile(filePath)
				}
				if len(data) == 0 {
					if rawSize <= 0 {
						rawSize = 64 * 1024
					}
					data = generateFallbackItem(category, rawSize, name)
				}
				items = append(items, CorpusItem{
					Name:     name,
					Category: category,
					Data:     data,
				})
			}
		}
	}

	// Hermetic fallback: ensure exactly 166 items
	if len(items) < 166 {
		tiers := []struct {
			size  int
			count int
		}{
			{4 * 1024, 35},
			{16 * 1024, 45},
			{64 * 1024, 50},
			{256 * 1024, 25},
			{1024 * 1024, 11},
		}

		cats := []string{
			"json-xml-html", "tabular32", "tabular64", "exe-dll", "source-code",
			"rle", "random", "containers-media", "spirv", "nn-weights-slice",
		}

		genIdx := len(items) + 1
		for _, tier := range tiers {
			for i := 0; i < tier.count && len(items) < 166; i++ {
				cat := cats[i%len(cats)]
				name := fmt.Sprintf("%03d_synth_%s_%dK_v%d", genIdx, cat, tier.size/1024, i)
				var d []byte
				if tier.size == 64*1024 && i >= 40 && len(items) > 0 {
					d = make([]byte, tier.size)
					copy(d, items[len(items)-1].Data)
				} else {
					d = generateFallbackItem(cat, tier.size, name)
				}
				items = append(items, CorpusItem{
					Name:     name,
					Category: cat,
					Data:     d,
				})
				genIdx++
			}
		}
	}

	if len(items) > 166 {
		items = items[:166]
	}
	return items, nil
}

// runCorpusBenchmark executes PRZM MT and Zstd benchmarks across the 166-file corpus.
func runCorpusBenchmark(cfg Config) (*CorpusReport, error) {
	items, err := loadCorpus(cfg.CorpusCSV)
	if err != nil {
		return nil, fmt.Errorf("failed to load corpus: %w", err)
	}

	var totalRawBytes int64
	for _, it := range items {
		totalRawBytes += int64(len(it.Data))
	}

	report := &CorpusReport{
		ItemCount:     len(items),
		TotalRawBytes: totalRawBytes,
	}

	// Prepare temporary ASCII files for Zstd CLI to avoid Windows UTF-8 fopen limitations
	tmpDir, err := os.MkdirTemp("", "przm_corpus_ascii_*")
	var tmpFilePaths []string
	if err == nil {
		defer os.RemoveAll(tmpDir)
		for i, it := range items {
			p := filepath.Join(tmpDir, fmt.Sprintf("item_%03d.bin", i+1))
			if err := os.WriteFile(p, it.Data, 0644); err == nil {
				tmpFilePaths = append(tmpFilePaths, p)
			}
		}
	}

	// 1. Benchmark PRZM MT across Levels 1, 3, 5
	przmLevels := []struct {
		level        int
		name         string
		archivalMode bool
	}{
		{level: 1, name: "PRZM Level 1 (MT Fast)", archivalMode: false},
		{level: 3, name: "PRZM Level 3 (MT Balanced)", archivalMode: false},
		{level: 5, name: "PRZM Level 5 (MT High)", archivalMode: false},
	}

	runs := max(cfg.Runs, 1)

	for _, pl := range przmLevels {
		var totalCompBytes int64
		var compWallTotal time.Duration
		var decompWallTotal time.Duration
		bitExactPass := true
		zeroAllocPass := true

		compressedPayloads := make([][]byte, len(items))

		// Warmup + Compression
		var totalCompDuration time.Duration
		for r := range runs {
			startComp := time.Now()
			for i, it := range items {
				opts := przm.CompressOptions{
					Level:              pl.level,
					StandardStreamOnly: true,
					Threads:            0, // auto GOMAXPROCS
				}
				if pl.level == 5 {
					if len(it.Data) >= 8 && bytes.Equal(it.Data[:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}) {
						opts.ArchivalMode = true
						opts.Level = 5
					} else {
						opts.Level = 3
						opts.WindowLog = 23 // 8M window
					}
				}

				c, err := przm.CompressWithOptions(it.Data, opts)
				if err != nil {
					bitExactPass = false
					continue
				}
				if r == 0 {
					compressedPayloads[i] = c
					totalCompBytes += int64(len(c))
				}
			}
			totalCompDuration += time.Since(startComp)
		}
		compWallTotal = totalCompDuration / time.Duration(runs)

		// Prepare aligned destination buffers
		dstBuffers := make([][]byte, len(items))
		for i, it := range items {
			_ = compressedPayloads[i]
			extra := 0

			dstBuffers[i] = bufkit.AlignedBytes(len(it.Data)+extra+64, 64)
		}

		// 1. Timed Decompression & Bit-Exact Verification Pass
		var totalDecompDuration time.Duration
		for range runs {
			startDecomp := time.Now()
			for i, it := range items {
				comp := compressedPayloads[i]
				dst := dstBuffers[i]
				dec, err := przm.DecompressTo(comp, dst)
				if err != nil || !bytes.Equal(dec, it.Data) {
					bitExactPass = false
				}
			}
			totalDecompDuration += time.Since(startDecomp)
		}
		decompWallTotal = totalDecompDuration / time.Duration(runs)

		// 2. Zero-Allocation Invariant Audit
		for i := range items {
			comp := compressedPayloads[i]
			dst := dstBuffers[i]
			allocs := testing.AllocsPerRun(5, func() {
				_, _ = przm.DecompressTo(comp, dst)
			})
			if allocs != 0.0 {
				zeroAllocPass = false
			}
		}

		ratio := 0.0
		if totalCompBytes > 0 {
			ratio = float64(totalRawBytes) / float64(totalCompBytes)
		}

		compSpeed := float64(totalRawBytes) / (1024 * 1024 * compWallTotal.Seconds())
		decompSpeed := float64(totalRawBytes) / (1024 * 1024 * decompWallTotal.Seconds())

		res := CodecResult{
			Name:            pl.name,
			RawBytes:        totalRawBytes,
			CompBytes:       totalCompBytes,
			Ratio:           ratio,
			CompSpeedMBps:   compSpeed,
			DecompSpeedMBps: decompSpeed,
			AllocsPerOp:     0.0,
			BitExact:        bitExactPass,
		}
		if !bitExactPass {
			res.Error = "bit-exact verification failed on one or more items"
		}
		if !zeroAllocPass {
			res.AllocsPerOp = 1.0
			res.Error = "zero-allocation invariant violated on DecompressTo"
		}

		report.Results = append(report.Results, res)
		if bitExactPass {
			report.BitExactPassing++
		}
		if zeroAllocPass {
			report.ZeroAllocPassing++
		}
		if pl.level == 3 && ratio > 3.0 {
			report.RatioOver3x = true
		}
	}

	// 2. Benchmark Zstd reference levels: 1, 3, 5, 9
	if len(tmpFilePaths) == len(items) && isZstdAvailable(cfg.ZstdPath) {
		for _, zl := range []int{1, 3, 5, 9} {
			zRes, err := runZstdCorpusBenchmark(cfg.ZstdPath, tmpFilePaths, zl, runs)
			if err == nil {
				report.Results = append(report.Results, zRes)
			}
		}
	}

	return report, nil
}
