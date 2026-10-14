// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var zstdBenchRegex = regexp.MustCompile(`(\d+)\s*->\s*(\d+)\s*(?:\(x?([0-9.]+)\))?,\s*([0-9.]+)\s*MB/s(?:,\s*([0-9.]+)\s*MB/s)?`)

// resolveZstdPath locates a working zstd executable on the system.
func resolveZstdPath(preferred string) string {
	if preferred != "" {
		if p, err := exec.LookPath(preferred); err == nil {
			return p
		}
		if _, err := os.Stat(preferred); err == nil {
			return preferred
		}
	}
	candidates := []string{
		"zstd",
		"zstd.exe",
	}
	for _, cand := range candidates {
		if p, err := exec.LookPath(cand); err == nil {
			return p
		}
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

// isZstdAvailable returns true if zstd can execute successfully.
func isZstdAvailable(path string) bool {
	if path == "" {
		return false
	}
	cmd := exec.Command(path, "--version")
	return cmd.Run() == nil
}

// parseZstdBenchmarkOutput parses the stdout/stderr from `zstd -b`.
func parseZstdBenchmarkOutput(output string) (rawBytes int64, compBytes int64, ratio float64, compSpeed float64, decompSpeed float64, err error) {
	// zstd uses \r for progress updates; split by both \r and \n to find the final line
	lines := strings.FieldsFunc(output, func(r rune) bool {
		return r == '\r' || r == '\n'
	})

	for _, line := range slices.Backward(lines) {
		matches := zstdBenchRegex.FindStringSubmatch(line)
		if len(matches) >= 5 {
			rawBytes, _ = strconv.ParseInt(matches[1], 10, 64)
			compBytes, _ = strconv.ParseInt(matches[2], 10, 64)
			if matches[3] != "" {
				ratio, _ = strconv.ParseFloat(matches[3], 64)
			} else if compBytes > 0 {
				ratio = float64(rawBytes) / float64(compBytes)
			}
			compSpeed, _ = strconv.ParseFloat(matches[4], 64)
			if len(matches) >= 6 && matches[5] != "" {
				decompSpeed, _ = strconv.ParseFloat(matches[5], 64)
			}
			return rawBytes, compBytes, ratio, compSpeed, decompSpeed, nil
		}
	}
	return 0, 0, 0, 0, 0, fmt.Errorf("unable to parse zstd benchmark output from: %s", output)
}

// runZstdLargeBenchmark runs `zstd -b<level> -T<threads>` on a single large file.
func runZstdLargeBenchmark(zstdPath, filePath string, level, threads, evalSeconds int) (ZstdLargeResult, error) {
	res := ZstdLargeResult{
		Threads: threads,
		Level:   level,
	}
	if !isZstdAvailable(zstdPath) {
		res.Error = "zstd CLI not available"
		return res, fmt.Errorf("zstd CLI not available")
	}

	args := []string{
		fmt.Sprintf("-b%d", level),
		fmt.Sprintf("-T%d", threads),
		fmt.Sprintf("-i%d", evalSeconds),
		filePath,
	}

	cmd := exec.Command(zstdPath, args...)
	outBytes, err := cmd.CombinedOutput()
	outStr := string(outBytes)
	if err != nil && len(outBytes) == 0 {
		res.Error = fmt.Sprintf("execution failed: %v", err)
		return res, err
	}

	raw, comp, ratio, compSpeed, decompSpeed, parseErr := parseZstdBenchmarkOutput(outStr)
	if parseErr != nil {
		res.Error = parseErr.Error()
		return res, parseErr
	}

	res.CompBytes = comp
	res.Ratio = ratio
	res.CompSpeedMBps = compSpeed
	res.DecompSpeedMBps = decompSpeed
	_ = raw
	return res, nil
}

// runZstdCorpusBenchmark runs `zstd -b<level>` across all corpus files via filelist.
func runZstdCorpusBenchmark(zstdPath string, corpusFiles []string, level, evalSeconds int) (CodecResult, error) {
	name := fmt.Sprintf("Zstd Level %d", level)
	res := CodecResult{Name: name, BitExact: true, AllocsPerOp: 0.0}

	if !isZstdAvailable(zstdPath) {
		res.Error = "zstd CLI not available"
		return res, fmt.Errorf("zstd CLI not available")
	}

	// Create temporary file list using forward slashes
	tmpList, err := os.CreateTemp("", "zstd_flist_*.txt")
	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	listPath := tmpList.Name()
	defer os.Remove(listPath)

	for _, f := range corpusFiles {
		clean := filepath.ToSlash(f)
		if _, err := tmpList.WriteString(clean + "\n"); err != nil {
			tmpList.Close()
			res.Error = err.Error()
			return res, err
		}
	}
	_ = tmpList.Close()

	args := []string{
		fmt.Sprintf("-b%d", level),
		fmt.Sprintf("-i%d", evalSeconds),
		"-T0",
		"--filelist=" + filepath.ToSlash(listPath),
	}

	cmd := exec.Command(zstdPath, args...)
	outBytes, err := cmd.CombinedOutput()
	outStr := string(outBytes)
	if err != nil && len(outBytes) == 0 {
		res.Error = fmt.Sprintf("execution failed: %v", err)
		return res, err
	}

	raw, comp, ratio, compSpeed, decompSpeed, parseErr := parseZstdBenchmarkOutput(outStr)
	if parseErr != nil {
		res.Error = parseErr.Error()
		return res, parseErr
	}

	res.RawBytes = raw
	res.CompBytes = comp
	res.Ratio = ratio
	res.CompSpeedMBps = compSpeed
	res.DecompSpeedMBps = decompSpeed
	return res, nil
}
