// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"
)

func main() {
	var cfg Config
	flag.StringVar(&cfg.Mode, "mode", "all", "Benchmark mode: 'corpus', 'large', or 'all'")
	flag.StringVar(&cfg.LargeFile, "large-file", "", "Path to large (256MB) benchmark file")
	flag.StringVar(&cfg.CorpusCSV, "corpus-csv", `teamwork_projects/przm_compression_sectors/baseline_harness/per_file.csv`, "Path to corpus per_file.csv")
	flag.StringVar(&cfg.ZstdPath, "zstd-path", "zstd", "Path to zstd executable")
	flag.BoolVar(&cfg.JSON, "json", false, "Emit JSON summary to stdout")
	flag.IntVar(&cfg.Runs, "runs", 1, "Number of timed benchmark iterations")
	flag.Parse()

	if cfg.Mode != "corpus" && cfg.Mode != "large" && cfg.Mode != "all" {
		if cfg.JSON {
			errReport := BenchmarkReport{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Hardware: HardwareInfo{
					OS:          runtime.GOOS,
					Arch:        runtime.GOARCH,
					LogicalCPUs: runtime.NumCPU(),
					GOMAXPROCS:  runtime.GOMAXPROCS(0),
				},
				Summary: ValidationSummary{
					AllPassed: false,
				},
				Validation: ValidationSummary{
					AllPassed: false,
				},
			}
			b, _ := json.MarshalIndent(errReport, "", "  ")
			fmt.Println(string(b))
		}
		fmt.Fprintf(os.Stderr, "Error: invalid --mode %q (must be 'corpus', 'large', or 'all')\n", cfg.Mode)
		flag.Usage()
		os.Exit(2)
	}

	if cfg.Runs <= 0 {
		if cfg.JSON {
			errReport := BenchmarkReport{
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Hardware: HardwareInfo{
					OS:          runtime.GOOS,
					Arch:        runtime.GOARCH,
					LogicalCPUs: runtime.NumCPU(),
					GOMAXPROCS:  runtime.GOMAXPROCS(0),
				},
				Summary: ValidationSummary{
					AllPassed: false,
				},
				Validation: ValidationSummary{
					AllPassed: false,
				},
			}
			b, _ := json.MarshalIndent(errReport, "", "  ")
			fmt.Println(string(b))
		}
		fmt.Fprintf(os.Stderr, "Error: invalid --runs %d (must be >= 1)\n", cfg.Runs)
		flag.Usage()
		os.Exit(2)
	}

	cfg.ZstdPath = resolveZstdPath(cfg.ZstdPath)

	hw := HardwareInfo{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		LogicalCPUs: runtime.NumCPU(),
		GOMAXPROCS:  runtime.GOMAXPROCS(0),
	}

	report := BenchmarkReport{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Hardware:  hw,
	}

	if !cfg.JSON {
		printBanner(hw, cfg)
	}

	allPassed := true

	// 1. Corpus Benchmark
	if cfg.Mode == "corpus" || cfg.Mode == "all" {
		if !cfg.JSON {
			fmt.Println("================================================================================")
			fmt.Println(" [1/2] RUNNING 166-FILE BASELINE CORPUS BENCHMARK")
			fmt.Println("================================================================================")
		}
		cRep, err := runCorpusBenchmark(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Corpus benchmark error: %v\n", err)
			allPassed = false
		} else {
			report.Corpus = cRep
			report.Summary.CorpusRatioOver3x = cRep.RatioOver3x
			report.Summary.CorpusBitExact = (cRep.BitExactPassing == 3)
			report.Summary.CorpusZeroAlloc = (cRep.ZeroAllocPassing == 3)

			if !cRep.RatioOver3x || cRep.BitExactPassing < 3 || cRep.ZeroAllocPassing < 3 {
				allPassed = false
			}

			if !cfg.JSON {
				printCorpusReport(cRep)
			}
		}
	}

	// 2. Large File Benchmark & CPU Saturation
	if cfg.Mode == "large" || cfg.Mode == "all" {
		if !cfg.JSON {
			fmt.Println("\n================================================================================")
			fmt.Println(" [2/2] RUNNING 256MB LARGE FILE BENCHMARK & MULTI-CORE SATURATION")
			fmt.Println("================================================================================")
		}
		lRep, err := runLargeBenchmark(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Large file benchmark error: %v\n", err)
			allPassed = false
		} else {
			report.Large = lRep
			report.Summary.LargeSaturationOver5 = lRep.SaturationMet
			report.Summary.LargeScalingVerified = lRep.ScalingVerified
			report.Summary.LargeBitExact = lRep.BitExactAll
			report.Summary.LargeZeroAlloc = lRep.ZeroAllocAll

			if !lRep.SaturationMet || !lRep.BitExactAll || !lRep.ZeroAllocAll {
				allPassed = false
			}

			if !cfg.JSON {
				printLargeReport(lRep)
			}
		}
	}

	report.Summary.AllPassed = allPassed
	report.Validation = report.Summary

	if cfg.JSON {
		b, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(b))
	} else {
		printSummary(report.Summary, cfg)
	}

	if !allPassed {
		os.Exit(1)
	}
}

func printBanner(hw HardwareInfo, cfg Config) {
	fmt.Println("================================================================================")
	fmt.Println(" PRZM MULTITHREADED BENCHMARK SUITE & ZSTD PARITY VALIDATION (MILESTONE 4)")
	fmt.Println("================================================================================")
	fmt.Printf(" Host Environment : %s/%s | %d Logical Cores | GOMAXPROCS=%d\n", hw.OS, hw.Arch, hw.LogicalCPUs, hw.GOMAXPROCS)
	fmt.Printf(" Benchmark Mode   : %s | Runs: %d\n", cfg.Mode, cfg.Runs)
	fmt.Printf(" Corpus CSV       : %s\n", cfg.CorpusCSV)
	fmt.Printf(" Large File Target: %s\n", cfg.LargeFile)
	fmt.Printf(" Zstd Toolchain   : %s (Available: %v)\n", cfg.ZstdPath, isZstdAvailable(cfg.ZstdPath))
	fmt.Println()
}

func printCorpusReport(cr *CorpusReport) {
	fmt.Printf("Corpus Files Ingested: %d | Total Raw Bytes: %d (%.2f MB)\n\n",
		cr.ItemCount, cr.TotalRawBytes, float64(cr.TotalRawBytes)/(1024*1024))

	fmt.Printf("%-28s | %10s | %10s | %7s | %11s | %11s | %9s | %s\n",
		"Codec / Preset", "Raw (MB)", "Comp (MB)", "Ratio", "Enc (MB/s)", "Dec (MB/s)", "Alloc/Op", "Status")
	fmt.Println("-----------------------------+------------+------------+---------+-------------+-------------+-----------+--------")

	for _, r := range cr.Results {
		status := "PASS"
		if !r.BitExact || r.AllocsPerOp > 0.0 || r.Error != "" {
			status = "FAIL"
		}
		rawMB := float64(r.RawBytes) / (1024 * 1024)
		compMB := float64(r.CompBytes) / (1024 * 1024)
		fmt.Printf("%-28s | %10.2f | %10.2f | %6.3fx | %11.1f | %11.1f | %9.2f | %s\n",
			r.Name, rawMB, compMB, r.Ratio, r.CompSpeedMBps, r.DecompSpeedMBps, r.AllocsPerOp, status)
	}

	fmt.Println()
	fmt.Printf(" [+] Invariant Check: 100%% Bit-Exact Roundtrips : %d/3 PASS\n", cr.BitExactPassing)
	fmt.Printf(" [+] Invariant Check: 0.00 Heap Allocs Decompress: %d/3 PASS\n", cr.ZeroAllocPassing)
	fmt.Printf(" [+] Invariant Check: Aggregate Ratio > 3.0x     : %v\n", cr.RatioOver3x)
}

func printLargeReport(lr *LargeReport) {
	fmt.Printf("Target File: %s | Raw Size: %d bytes (%.2f MB)\n\n",
		lr.FilePath, lr.RawBytes, float64(lr.RawBytes)/(1024*1024))

	fmt.Println("--- PRZM Multithreaded Scaling & CPU Saturation ---")
	fmt.Printf("%-10s | %7s | %10s | %7s | %9s | %9s | %10s | %11s | %11s | %s\n",
		"Preset", "Threads", "Comp (MB)", "Ratio", "Wall Time", "CPU Time", "Saturation", "Enc (MB/s)", "Dec (MB/s)", "Status")
	fmt.Println("-----------+---------+------------+---------+-----------+-----------+------------+-------------+-------------+--------")

	for _, r := range lr.PRZMResults {
		status := "PASS"
		if !r.BitExact || r.AllocsPerOp > 0.0 || r.Error != "" {
			status = "FAIL"
		}
		compMB := float64(r.CompBytes) / (1024 * 1024)
		fmt.Printf("%-10s | %7d | %10.2f | %6.3fx | %8.3fs | %8.3fs | %9.2fx | %11.1f | %11.1f | %s\n",
			r.PresetName, r.Threads, compMB, r.Ratio, r.WallSeconds, r.CPUSeconds, r.Saturation, r.CompSpeedMBps, r.DecompSpeedMBps, status)
	}

	if len(lr.ZstdResults) > 0 {
		fmt.Println("\n--- Zstd Level 3 Reference Scaling ---")
		fmt.Printf("%-10s | %7s | %10s | %7s | %11s | %11s\n",
			"Codec", "Threads", "Comp (MB)", "Ratio", "Enc (MB/s)", "Dec (MB/s)")
		fmt.Println("-----------+---------+------------+---------+-------------+-------------")
		for _, zr := range lr.ZstdResults {
			compMB := float64(zr.CompBytes) / (1024 * 1024)
			fmt.Printf("%-10s | %7d | %10.2f | %6.3fx | %11.1f | %11.1f\n",
				"Zstd -b3", zr.Threads, compMB, zr.Ratio, zr.CompSpeedMBps, zr.DecompSpeedMBps)
		}
	}

	fmt.Println()
	fmt.Printf(" [+] Invariant Check: Max CPU Saturation Factor  : %.2fx (Target >= 5.0x: %v)\n", lr.MaxSaturation, lr.SaturationMet)
	fmt.Printf(" [+] Invariant Check: 100%% Bit-Exact Roundtrip   : %v\n", lr.BitExactAll)
	fmt.Printf(" [+] Invariant Check: 0.00 Heap Allocs Decompress: %v\n", lr.ZeroAllocAll)
	fmt.Printf(" [+] Scaling Check  : Throughput Scaling Verified: %v\n", lr.ScalingVerified)
}

func printSummary(s ValidationSummary, cfg Config) {
	fmt.Println("\n================================================================================")
	fmt.Println(" FINAL MILESTONE 4 ACCEPTANCE CRITERIA VERIFICATION")
	fmt.Println("================================================================================")
	if cfg.Mode == "corpus" || cfg.Mode == "all" {
		fmt.Printf(" 1. Corpus Aggregate Compression Ratio > 3.0x     : %s\n", boolBadge(s.CorpusRatioOver3x))
		fmt.Printf(" 2. Corpus 100%% Bit-Exact Lossless Roundtrips     : %s\n", boolBadge(s.CorpusBitExact))
		fmt.Printf(" 3. Corpus DecompressTo 0.00 Dynamic Heap Allocs  : %s\n", boolBadge(s.CorpusZeroAlloc))
	} else {
		fmt.Println(" 1-3. Corpus Invariants                          : [SKIPPED (mode=large)]")
	}
	if cfg.Mode == "large" || cfg.Mode == "all" {
		fmt.Printf(" 4. Large File CPU Saturation Factor >= 5.0x      : %s\n", boolBadge(s.LargeSaturationOver5))
		fmt.Printf(" 5. Large File Multi-Core Throughput Scaling      : %s\n", boolBadge(s.LargeScalingVerified))
		fmt.Printf(" 6. Large File 100%% Bit-Exact Lossless Roundtrip  : %s\n", boolBadge(s.LargeBitExact))
		fmt.Printf(" 7. Large File DecompressTo 0.00 Heap Allocations : %s\n", boolBadge(s.LargeZeroAlloc))
	} else {
		fmt.Println(" 4-7. Large File Invariants                      : [SKIPPED (mode=corpus)]")
	}
	fmt.Println("--------------------------------------------------------------------------------")
	if s.AllPassed {
		fmt.Println(" >>> OVERALL VERDICT: ALL INVARIANTS SATISFIED & VERIFIED [PASS] <<<")
	} else {
		fmt.Println(" >>> OVERALL VERDICT: ONE OR MORE INVARIANTS FAILED [FAIL] <<<")
	}
	fmt.Println("================================================================================")
}

func boolBadge(b bool) string {
	if b {
		return "[PASS]"
	}
	return "[FAIL]"
}
