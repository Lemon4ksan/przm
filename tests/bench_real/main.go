//go:build ignore

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"github.com/lemon4ksan/przm"
	"time"
)

func main() {
	fmt.Println("PRZM Efficiency Benchmark")
	fmt.Println("-------------------------")

	targets := []string{
		`D:\CodingProjects\przm\compress.go`,
		`D:\CodingProjects\przm\test_przm_dec.exe`,
		`D:\shaders`,
	}

	for _, target := range targets {
		info, err := os.Stat(target)
		if err != nil {
			fmt.Printf("Error stating %s: %v\n", target, err)
			continue
		}

		if !info.IsDir() {
			benchmarkFile(target)
		} else {
			benchmarkDir(target)
		}
	}
}

func benchmarkFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	fmt.Printf("\nFile: %s\n", filepath.Base(path))
	fmt.Printf("Raw Size:  %d bytes (%.2f MB)\n", len(data), float64(len(data))/1024/1024)

	// Level 1
	start := time.Now()
	l1, _ := przm.Compress(data, 1)
	t1 := time.Since(start)
	
	// Level 3
	start = time.Now()
	l3, _ := przm.Compress(data, 3)
	t3 := time.Since(start)

	// Level 5 (Archival)
	start = time.Now()
	l5, _ := przm.CompressWithOptions(data, przm.CompressOptions{Level: 5, ArchivalMode: true})
	t5 := time.Since(start)

	fmt.Printf("L1: %8d bytes (%.2fx) | %v (%.2f MB/s)\n", len(l1), float64(len(data))/float64(len(l1)), t1, float64(len(data))/1024/1024/t1.Seconds())
	fmt.Printf("L3: %8d bytes (%.2fx) | %v (%.2f MB/s)\n", len(l3), float64(len(data))/float64(len(l3)), t3, float64(len(data))/1024/1024/t3.Seconds())
	if len(l5) > 0 {
		fmt.Printf("L5: %8d bytes (%.2fx) | %v (%.2f MB/s)\n", len(l5), float64(len(data))/float64(len(l5)), t5, float64(len(data))/1024/1024/t5.Seconds())
	}
}

func benchmarkDir(dir string) {
	fmt.Printf("\nDirectory: %s\n", dir)
	var totalRaw, totalL1, totalL3, totalL5 int64
	var tL1, tL3, tL5 time.Duration
	var count int

	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if totalRaw > 100*1024*1024 { return filepath.SkipAll }
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if totalRaw > 100*1024*1024 { return filepath.SkipAll }
			return nil
		}

		totalRaw += int64(len(data))
		count++

		start := time.Now()
		l1, _ := przm.Compress(data, 1)
		tL1 += time.Since(start)
		totalL1 += int64(len(l1))

		start = time.Now()
		l3, _ := przm.Compress(data, 3)
		tL3 += time.Since(start)
		totalL3 += int64(len(l3))

		// L5 is too slow for 4GB, skip or sample?
		// We will do L5 but if it takes too long we might wait a bit.
		// Actually, let's just do L5. It's the ultimate test.
		start = time.Now()
		l5, _ := przm.CompressWithOptions(data, przm.CompressOptions{Level: 5, ArchivalMode: true})
		tL5 += time.Since(start)
		totalL5 += int64(len(l5))

		// print progress
		if count%10 == 0 {
			fmt.Printf("... processed %d files (%.2f MB)\n", count, float64(totalRaw)/1024/1024)
		}
		if totalRaw > 100*1024*1024 { return filepath.SkipAll }
		return nil
	})

	fmt.Printf("\nTotal Files: %d\n", count)
	fmt.Printf("Raw Size:  %d bytes (%.2f MB)\n", totalRaw, float64(totalRaw)/1024/1024)
	fmt.Printf("L1: %8d bytes (%.2fx) | %v (%.2f MB/s)\n", totalL1, float64(totalRaw)/float64(totalL1), tL1, float64(totalRaw)/1024/1024/tL1.Seconds())
	fmt.Printf("L3: %8d bytes (%.2fx) | %v (%.2f MB/s)\n", totalL3, float64(totalRaw)/float64(totalL3), tL3, float64(totalRaw)/1024/1024/tL3.Seconds())
	fmt.Printf("L5: %8d bytes (%.2fx) | %v (%.2f MB/s)\n", totalL5, float64(totalRaw)/float64(totalL5), tL5, float64(totalRaw)/1024/1024/tL5.Seconds())
}
