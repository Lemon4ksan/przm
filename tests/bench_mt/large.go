// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// runLargeBenchmark executes the 256MB multi-threaded saturation and scaling suite.
func runLargeBenchmark(cfg Config) (*LargeReport, error) {
	data, err := os.ReadFile(cfg.LargeFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read large test file %s: %w", cfg.LargeFile, err)
	}

	rawBytes := int64(len(data))
	report := &LargeReport{
		FilePath:     cfg.LargeFile,
		RawBytes:     rawBytes,
		BitExactAll:  true,
		ZeroAllocAll: true,
	}

	threadCounts := []int{1, 2, 4, 8, 12}
	presets := []struct {
		level int
		name  string
	}{
		{level: 1, name: "PRZM L1"},
		{level: 3, name: "PRZM L3"},
	}

	// Buffer for decompression verification (aligned + 64 bytes safety padding)
	dst := bufkit.AlignedBytes(len(data)+64, 64)

	scalingAll := true
	runs := max(cfg.Runs, 1)

	for _, preset := range presets {
		var speedT1, speedT12 float64
		for _, th := range threadCounts {
			opts := przm.CompressOptions{
				Level:   preset.level,
				Threads: th,
			}

			// Warmup compression
			comp, err := przm.CompressWithOptions(data, opts)
			if err != nil {
				res := ThreadResult{
					Threads:    th,
					Level:      preset.level,
					PresetName: preset.name,
					Error:      fmt.Sprintf("compression failed: %v", err),
				}
				report.PRZMResults = append(report.PRZMResults, res)
				continue
			}

			// Timed compression run measuring process CPU times
			var totalWallElapsed time.Duration
			var totalCPUElapsed time.Duration
			var compTimed []byte
			for range runs {
				timer := startCPUTimer()
				ct, err := przm.CompressWithOptions(data, opts)
				w, c, _ := timer.Stop()
				if err == nil {
					compTimed = ct
				}
				totalWallElapsed += w
				totalCPUElapsed += c
			}
			if compTimed == nil {
				compTimed = comp
			}

			wallElapsed := totalWallElapsed / time.Duration(runs)
			cpuElapsed := totalCPUElapsed / time.Duration(runs)
			saturation := 0.0
			if wallElapsed > 0 {
				saturation = cpuElapsed.Seconds() / wallElapsed.Seconds()
			}

			compLen := int64(len(compTimed))
			ratio := 0.0
			if compLen > 0 {
				ratio = float64(rawBytes) / float64(compLen)
			}
			compSpeed := float64(rawBytes) / (1024 * 1024 * wallElapsed.Seconds())

			if th == 1 {
				speedT1 = compSpeed
			} else if th == 12 {
				speedT12 = compSpeed
			}

			if saturation > report.MaxSaturation {
				report.MaxSaturation = saturation
			}
			if saturation >= 5.0 {
				report.SaturationMet = true
			}

			// Decompression and verification
			startDec := time.Now()
			dec, err := przm.DecompressTo(compTimed, dst)
			decElapsed := time.Since(startDec)

			bitExact := (err == nil && bytes.Equal(dec, data))
			if !bitExact {
				report.BitExactAll = false
			}

			decompSpeed := float64(rawBytes) / (1024 * 1024 * decElapsed.Seconds())

			// Zero heap allocations verification
			allocs := testing.AllocsPerRun(3, func() {
				_, _ = przm.DecompressTo(compTimed, dst)
			})
			if allocs != 0.0 {
				report.ZeroAllocAll = false
			}

			tr := ThreadResult{
				Threads:         th,
				Level:           preset.level,
				PresetName:      preset.name,
				CompBytes:       compLen,
				Ratio:           ratio,
				WallSeconds:     wallElapsed.Seconds(),
				CPUSeconds:      cpuElapsed.Seconds(),
				Saturation:      saturation,
				CompSpeedMBps:   compSpeed,
				DecompSpeedMBps: decompSpeed,
				AllocsPerOp:     allocs,
				BitExact:        bitExact,
			}
			report.PRZMResults = append(report.PRZMResults, tr)
		}

		// Scaling verification: 12 threads should achieve at least 2.0x speedup over 1 thread
		if speedT12 <= speedT1*2.0 {
			scalingAll = false
		}
	}

	report.ScalingVerified = scalingAll

	// Run Zstd reference scaling across the same thread counts at Level 3
	if isZstdAvailable(cfg.ZstdPath) {
		for _, th := range threadCounts {
			zRes, err := runZstdLargeBenchmark(cfg.ZstdPath, cfg.LargeFile, 3, th, runs)
			if err == nil {
				report.ZstdResults = append(report.ZstdResults, zRes)
			}
		}
	}

	return report, nil
}
