//go:build ignore

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/lemon4ksan/przm"
)

func main() {
	files := []string{
		`C:\Windows\explorer.exe`,
		`D:\shaders\fc52a9db4c4105d2.nvph`, // 256MB file for a balanced benchmark time
		`D:\CodingProjects\przm\test_przm_dec.exe`,
	}

	fmt.Println("Exhaustive Benchmark: PRZM vs LZMA2 vs Zstd")
	fmt.Println("===========================================")

	for _, path := range files {
		benchmarkFile(path)
	}
}

const chunkSize = 15 * 1024 * 1024

func compressPrzmChunked(data []byte, level int, archival bool) (int, time.Duration, error) {
	var totalSize int
	start := time.Now()
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		var comp []byte
		var err error
		if archival {
			comp, err = przm.CompressWithOptions(chunk, przm.CompressOptions{Level: level, ArchivalMode: true})
		} else {
			comp, err = przm.Compress(chunk, level)
		}
		if err != nil {
			return 0, 0, err
		}
		totalSize += len(comp)
	}
	return totalSize, time.Since(start), nil
}

func benchmarkFile(path string) {
	fmt.Printf("\n--- %s ---\n", filepath.Base(path))
	
	// Read full file
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("Error reading %s: %v\n", path, err)
		return
	}
	fmt.Printf("Raw Size: %d bytes (%.2f MB)\n", len(data), float64(len(data))/1024/1024)

	// PRZM L3
	l3Size, t3, err := compressPrzmChunked(data, 3, false)
	if err != nil { fmt.Printf("PRZM L3 Err: %v\n", err) }
	
	// PRZM L5
	l5Size, t5, err := compressPrzmChunked(data, 5, true)
	if err != nil { fmt.Printf("PRZM L5 Err: %v\n", err) }

	// ZSTD -3
	zstd3Size, tZstd3 := runZstd(path, 3)
	
	// ZSTD -19
	zstd19Size, tZstd19 := runZstd(path, 19)

	// LZMA2 Fast (-mx=3)
	lzma3Size, tLzma3 := run7z(path, 3)

	// LZMA2 Ultra (-mx=9)
	lzma9Size, tLzma9 := run7z(path, 9)

	printRes("PRZM L3", len(data), l3Size, t3)
	printRes("PRZM L5", len(data), l5Size, t5)
	printRes("ZSTD -3", len(data), zstd3Size, tZstd3)
	printRes("ZSTD -19", len(data), zstd19Size, tZstd19)
	printRes("LZMA2 -3", len(data), lzma3Size, tLzma3)
	printRes("LZMA2 -9", len(data), lzma9Size, tLzma9)
}

func printRes(name string, raw int, comp int, t time.Duration) {
	if comp == 0 {
		fmt.Printf("%-10s : FAILED\n", name)
		return
	}
	ratio := float64(raw)/float64(comp)
	speed := (float64(raw)/1024/1024) / t.Seconds()
	fmt.Printf("%-10s : %10d bytes (%6.2fx) | Time: %10v | Speed: %8.2f MB/s\n", name, comp, ratio, t, speed)
}

func run7z(path string, level int) (int, time.Duration) {
	tmpOut := filepath.Join(os.TempDir(), fmt.Sprintf("lzma_out_%d.7z", level))
	os.Remove(tmpOut)
	defer os.Remove(tmpOut)

	start := time.Now()
	// Using 7z format, LZMA2 method
	cmd := exec.Command(`C:\Program Files\7-Zip\7z.exe`, "a", "-t7z", "-m0=lzma2", fmt.Sprintf("-mx=%d", level), "-bso0", tmpOut, path)
	err := cmd.Run()
	t := time.Since(start)
	if err != nil {
		fmt.Printf("7z error: %v\n", err)
		return 0, t
	}

	info, err := os.Stat(tmpOut)
	if err != nil {
		return 0, t
	}
	return int(info.Size()), t
}

func runZstd(path string, level int) (int, time.Duration) {
	tmpOut := filepath.Join(os.TempDir(), fmt.Sprintf("zstd_out_%d.zst", level))
	os.Remove(tmpOut)
	defer os.Remove(tmpOut)

	start := time.Now()
	cmd := exec.Command("zstd", fmt.Sprintf("-%d", level), "-f", path, "-o", tmpOut)
	err := cmd.Run()
	t := time.Since(start)
	if err != nil {
		fmt.Printf("zstd error: %v\n", err)
		return 0, t
	}

	info, err := os.Stat(tmpOut)
	if err != nil {
		return 0, t
	}
	return int(info.Size()), t
}
