// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Config holds command-line configuration for the benchmark suite.
type Config struct {
	Mode      string
	LargeFile string
	CorpusCSV string
	ZstdPath  string
	JSON      bool
	Runs      int
}

// HardwareInfo details the execution host environment.
type HardwareInfo struct {
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	LogicalCPUs int    `json:"logical_cpus"`
	GOMAXPROCS  int    `json:"gomaxprocs"`
}

// CodecResult represents performance of a codec/preset on the corpus.
type CodecResult struct {
	Name            string  `json:"name"`
	RawBytes        int64   `json:"raw_bytes"`
	CompBytes       int64   `json:"comp_bytes"`
	Ratio           float64 `json:"ratio"`
	CompSpeedMBps   float64 `json:"comp_speed_mb_s"`
	DecompSpeedMBps float64 `json:"decomp_speed_mb_s"`
	AllocsPerOp     float64 `json:"allocs_per_op"`
	BitExact        bool    `json:"bit_exact"`
	Error           string  `json:"error,omitempty"`
}

// CorpusReport contains aggregated metrics for the 166-file baseline corpus.
type CorpusReport struct {
	ItemCount        int           `json:"item_count"`
	TotalRawBytes    int64         `json:"total_raw_bytes"`
	Results          []CodecResult `json:"results"`
	BitExactPassing  int           `json:"bit_exact_passing"`
	ZeroAllocPassing int           `json:"zero_alloc_passing"`
	RatioOver3x      bool          `json:"ratio_over_3x"`
}

// ThreadResult captures PRZM MT performance at a specific worker thread count.
type ThreadResult struct {
	Threads         int     `json:"threads"`
	Level           int     `json:"level"`
	PresetName      string  `json:"preset_name"`
	CompBytes       int64   `json:"comp_bytes"`
	Ratio           float64 `json:"ratio"`
	WallSeconds     float64 `json:"wall_seconds"`
	CPUSeconds      float64 `json:"cpu_seconds"`
	Saturation      float64 `json:"saturation"`
	CompSpeedMBps   float64 `json:"comp_speed_mb_s"`
	DecompSpeedMBps float64 `json:"decomp_speed_mb_s"`
	AllocsPerOp     float64 `json:"allocs_per_op"`
	BitExact        bool    `json:"bit_exact"`
	Error           string  `json:"error,omitempty"`
}

// ZstdLargeResult captures Zstd reference performance on the large file.
type ZstdLargeResult struct {
	Threads         int     `json:"threads"`
	Level           int     `json:"level"`
	CompBytes       int64   `json:"comp_bytes"`
	Ratio           float64 `json:"ratio"`
	CompSpeedMBps   float64 `json:"comp_speed_mb_s"`
	DecompSpeedMBps float64 `json:"decomp_speed_mb_s"`
	Error           string  `json:"error,omitempty"`
}

// LargeReport contains multi-threaded saturation and scaling data on the 256MB file.
type LargeReport struct {
	FilePath        string            `json:"file_path"`
	RawBytes        int64             `json:"raw_bytes"`
	PRZMResults     []ThreadResult    `json:"przm_results"`
	ZstdResults     []ZstdLargeResult `json:"zstd_results"`
	MaxSaturation   float64           `json:"max_saturation"`
	SaturationMet   bool              `json:"saturation_met"`
	BitExactAll     bool              `json:"bit_exact_all"`
	ZeroAllocAll    bool              `json:"zero_alloc_all"`
	ScalingVerified bool              `json:"scaling_verified"`
}

// ValidationSummary aggregates pass/fail status against all project invariants.
type ValidationSummary struct {
	CorpusRatioOver3x    bool `json:"corpus_ratio_over_3x"`
	CorpusBitExact       bool `json:"corpus_bit_exact"`
	CorpusZeroAlloc      bool `json:"corpus_zero_alloc"`
	LargeSaturationOver5 bool `json:"large_saturation_over_5x"`
	LargeScalingVerified bool `json:"large_scaling_verified"`
	LargeBitExact        bool `json:"large_bit_exact"`
	LargeZeroAlloc       bool `json:"large_zero_alloc"`
	AllPassed            bool `json:"all_passed"`
}

// BenchmarkReport is the root report output structure.
type BenchmarkReport struct {
	Timestamp  string            `json:"timestamp"`
	Hardware   HardwareInfo      `json:"hardware"`
	Corpus     *CorpusReport     `json:"corpus,omitempty"`
	Large      *LargeReport      `json:"large,omitempty"`
	Summary    ValidationSummary `json:"summary"`
	Validation ValidationSummary `json:"validation"`
}
