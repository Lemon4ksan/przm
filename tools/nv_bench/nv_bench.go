package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lemon4ksan/przm"
)

func zlibCompress(data []byte) int {
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	w.Write(data)
	w.Close()
	return b.Len()
}

func benchmarkShader(name string, data []byte) {
	rawSize := len(data)
	if rawSize == 0 {
		return
	}

	zlibSize := zlibCompress(data)
	zlibRatio := float64(rawSize) / float64(zlibSize)

	fmt.Println("-----------------------------------------------------------------------------------------------------------------------")
	fmt.Printf("TARGET: %s\n", name)
	fmt.Printf("Raw Size: %d bytes (%.2f KiB) | Zlib Size: %d bytes (Ratio: %.2fx)\n",
		rawSize, float64(rawSize)/1024.0, zlibSize, zlibRatio)
	fmt.Println("-----------------------------------------------------------------------------------------------------------------------")
	fmt.Printf("%-20s | %-12s | %-10s | %-16s | %-16s | %-10s\n",
		"PRZM Level / Mode", "Comp (Bytes)", "Ratio", "Comp Speed", "Decomp Speed", "Lossless")
	fmt.Println("-----------------------------------------------------------------------------------------------------------------------")

	for _, lvl := range []int{1, 2, 3} {
		// Warmup & compress
		comp, err := przm.Compress(data, lvl)
		if err != nil {
			fmt.Printf("Level %d compress err: %v\n", lvl, err)
			continue
		}
		compSize := len(comp)
		ratio := float64(rawSize) / float64(compSize)

		// Benchmark Compression Speed
		const iters = 100
		startComp := time.Now()
		for i := 0; i < iters; i++ {
			_, _ = przm.Compress(data, lvl)
		}
		compDuration := time.Since(startComp)
		compSpeedMB := (float64(rawSize*iters) / (1024 * 1024)) / compDuration.Seconds()

		// Benchmark Decompression Speed
		dst := make([]byte, rawSize+64)
		startDecomp := time.Now()
		for i := 0; i < iters; i++ {
			_, _ = przm.DecompressTo(comp, dst)
		}
		decompDuration := time.Since(startDecomp)
		decompSpeedMB := (float64(rawSize*iters) / (1024 * 1024)) / decompDuration.Seconds()

		// Verify lossless
		dec, err := przm.Decompress(comp)
		lossless := "OK (Bit-Exact)"
		if err != nil || !bytes.Equal(dec, data) {
			lossless = "FAILED"
		}

		lvlName := fmt.Sprintf("PRZM Level %d", lvl)
		if lvl == 1 {
			lvlName = "PRZM L1 (Fast)"
		} else if lvl == 2 {
			lvlName = "PRZM L2 (Balanced)"
		} else if lvl == 3 {
			lvlName = "PRZM L3 (Ultra)"
		}

		fmt.Printf("%-20s | %-12d | %-9.2fx | %10.1f MB/s | %10.1f MB/s | %-10s\n",
			lvlName, compSize, ratio, compSpeedMB, decompSpeedMB, lossless)
	}
}

func main() {
	fmt.Println("=======================================================================================================================")
	fmt.Println("                       PRZM REAL NVIDIA SHADER COMPRESSION BENCHMARK (WINDOWS)                         ")
	fmt.Println("=======================================================================================================================")

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		localAppData = `C:\Users\senya\AppData\Local`
	}

	targets := []struct {
		desc string
		path string
	}{
		{
			desc: "NVIDIA GLCache GLSL/Vulkan Compiled Shader (NVDC)",
			path: filepath.Join(localAppData, `NVIDIA\GLCache\c30d67effd5a80bf51a0817799acf3c8\c26305090aa51f1c\c92cbc022c69dbb3.bin`),
		},
		{
			desc: "NVIDIA DXCache DirectX Pipeline State Object (nvph 64KB)",
			path: filepath.Join(localAppData, `NVIDIA\DXCache\0002a9db13f58350.nvph`),
		},
		{
			desc: "NVIDIA DXCache DirectX Shader Pipeline (nvph 1MB)",
			path: filepath.Join(localAppData, `NVIDIA\DXCache\fc52a9dbefb54ade.nvph`),
		},
	}

	for _, t := range targets {
		data, err := os.ReadFile(t.path)
		if err != nil {
			fmt.Printf("Could not read %s: %v\n", t.path, err)
			continue
		}
		benchmarkShader(t.desc, data)
	}
	fmt.Println("=======================================================================================================================")
}
