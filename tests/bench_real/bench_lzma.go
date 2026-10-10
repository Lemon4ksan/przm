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
		`D:\CodingProjects\g-man-tf2-public\tests\testdata\schema.json`,
		`C:\Windows\Logs\CBS\CBS.log`,
	}

	fmt.Println("PRZM vs LZMA2 (xz -9) Benchmark on Real Files")
	fmt.Println("=============================================")

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("Error reading %s: %v\n", path, err)
			continue
		}

		// limit to 15MB for PRZM limit
		if len(data) > 15*1024*1024 {
			data = data[:15*1024*1024]
		}

		benchmarkFile(path, data)
	}
}

func benchmarkFile(path string, data []byte) {
	fmt.Printf("\n--- %s ---\n", filepath.Base(path))
	fmt.Printf("Raw Size: %d bytes (%.2f MB)\n", len(data), float64(len(data))/1024/1024)

	// PRZM L3
	start := time.Now()
	l3, err := przm.Compress(data, 3)
	t3 := time.Since(start)
	if err != nil { fmt.Printf("L3 Err: %v\n", err) }
	
	l3Size := len(l3)
	if l3Size == 0 { l3Size = len(data) } // if it rejected compression

	// PRZM L5
	start = time.Now()
	l5, err := przm.CompressWithOptions(data, przm.CompressOptions{Level: 5, ArchivalMode: true})
	t5 := time.Since(start)
	if err != nil { fmt.Printf("L5 Err: %v\n", err) }

	l5Size := len(l5)
	if l5Size == 0 { l5Size = len(data) }

	// LZMA2 (via 7z)
	lzmaSize, tLzma := run7z(data)

	fmt.Printf("PRZM L3 : %8d bytes (%.2fx) | Time: %v\n", l3Size, float64(len(data))/float64(l3Size), t3)
	fmt.Printf("PRZM L5 : %8d bytes (%.2fx) | Time: %v\n", l5Size, float64(len(data))/float64(l5Size), t5)
	fmt.Printf("LZMA2-9 : %8d bytes (%.2fx) | Time: %v\n", lzmaSize, float64(len(data))/float64(lzmaSize), tLzma)
}

func run7z(data []byte) (int, time.Duration) {
	tmpIn := filepath.Join(os.TempDir(), "lzma_in.dat")
	tmpOut := filepath.Join(os.TempDir(), "lzma_out.xz")
	
	os.WriteFile(tmpIn, data, 0644)
	defer os.Remove(tmpIn)
	defer os.Remove(tmpOut)

	start := time.Now()
	cmd := exec.Command(`C:\Program Files\7-Zip\7z.exe`, "a", "-txz", "-mx=9", tmpOut, tmpIn)
	cmd.Run()
	t := time.Since(start)

	outData, err := os.ReadFile(tmpOut)
	if err != nil {
		return len(data), t
	}
	return len(outData), t
}
