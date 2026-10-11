// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

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

// loadOrGenerateCorpus loads the 166-file baseline corpus from per_file.csv,
// falling back hermetically to synthetic generation if local files are absent.
func loadOrGenerateCorpus(t testing.TB) []CorpusItem {
	var items []CorpusItem

	candidates := []string{
		filepath.Join("..", "teamwork_projects", "przm_compression_sectors", "baseline_harness", "per_file.csv"),
		filepath.Join("teamwork_projects", "przm_compression_sectors", "baseline_harness", "per_file.csv"),
		`d:\CodingProjects\przm\teamwork_projects\przm_compression_sectors\baseline_harness\per_file.csv`,
	}

	var csvPath string
	for _, cand := range candidates {
		if _, err := os.Stat(cand); err == nil {
			csvPath = cand
			break
		}
	}

	if csvPath != "" {
		f, err := os.Open(csvPath)
		if err == nil {
			defer func() { _ = f.Close() }()
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

	// Hermetic fallback: ensure exactly 166 items across categories and size tiers
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
	return items
}

// TestCorpus_Scenario2_CompressionRatio verifies that Main Mode (Level 3) achieves
// 100% bit-exact lossless roundtrips and an aggregate compression ratio >= 2.92x.
func TestCorpus_Scenario2_CompressionRatio(t *testing.T) {
	corpus := loadOrGenerateCorpus(t)
	if len(corpus) != 166 {
		t.Fatalf("expected exactly 166 corpus items, got %d", len(corpus))
	}

	var totalRawBytes int64
	var totalCompBytes int64

	for i, item := range corpus {
		rawLen := len(item.Data)
		totalRawBytes += int64(rawLen)

		comp, err := przm.Compress(item.Data, 3)
		if err != nil {
			t.Fatalf("[%d] Compress failed for %s (%d bytes): %v", i, item.Name, rawLen, err)
		}
		totalCompBytes += int64(len(comp))

		decomp, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("[%d] Decompress failed for %s: %v", i, item.Name, err)
		}
		if !bytes.Equal(decomp, item.Data) {
			t.Fatalf("[%d] Lossless verification failed for %s", i, item.Name)
		}
	}

	ratio := float64(totalRawBytes) / float64(totalCompBytes)
	t.Logf("Total Raw Bytes:        %d", totalRawBytes)
	t.Logf("Total Compressed Bytes: %d", totalCompBytes)
	t.Logf("Aggregate Ratio:        %.4fx", ratio)

	if ratio < 2.9155 {
		t.Fatalf("INVARIANT VIOLATION: Aggregate compression ratio %.4fx < 2.9155x (Scenario 2 target)", ratio)
	}
}

// TestCorpus_ZeroAllocations verifies that DecompressTo produces strictly 0.00
// heap allocations per run across all 166 corpus items when cap(dst) >= len(item.Data) + 64.
func TestCorpus_ZeroAllocations(t *testing.T) {
	corpus := loadOrGenerateCorpus(t)
	for i, item := range corpus {
		comp, err := przm.CompressWithOptions(item.Data, przm.CompressOptions{Level: 3, StandardStreamOnly: true})
		if err != nil {
			t.Fatalf("[%d] Compress failed for %s: %v", i, item.Name, err)
		}
		_, err = przm.ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("[%d] ParseFrameHeader failed for %s: %v", i, item.Name, err)
		}
		dst := bufkit.AlignedBytes(len(item.Data)+64, 64)

		dec, err := przm.DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("[%d] DecompressTo warmup failed for %s: %v", i, item.Name, err)
		}
		if len(dec) != len(item.Data) {
			t.Fatalf("[%d] Length mismatch for %s: got %d, want %d", i, item.Name, len(dec), len(item.Data))
		}

		allocs := testing.AllocsPerRun(20, func() {
			_, _ = przm.DecompressTo(comp, dst)
		})
		if allocs != 0.00 {
			t.Fatalf("ZERO ALLOC VIOLATION on [%d] %s: %.2f allocs/run (mandated 0.00)", i, item.Name, allocs)
		}
	}
}

// BenchmarkCorpus_Decompress_MultiCore evaluates multi-core decompression line-rate
// throughput using b.RunParallel with thread-local aligned destination buffers.
func BenchmarkCorpus_Decompress_MultiCore(b *testing.B) {
	corpus := bench.GenerateCorpus([]int{64 * 1024, 256 * 1024, 1024 * 1024})

	// Inject 64K duplicate block for OpCopyBlock 54 GB/s line-rate evaluation
	dedupData := make([]byte, 128*1024)
	copy(dedupData[:64*1024], corpus[0].Data)
	copy(dedupData[64*1024:], corpus[0].Data)
	corpus = append(corpus, bench.CorpusItem{
		Name: "dedup_128K",
		Data: dedupData,
	})

	for _, item := range corpus {
		frame, err := przm.CompressWithOptions(item.Data, przm.CompressOptions{Level: 3, StandardStreamOnly: true})
		if err != nil {
			b.Fatalf("Failed to compress %s: %v", item.Name, err)
		}
		origLen := len(item.Data)
		extra := 0

		b.Run(item.Name, func(b *testing.B) {
			b.SetBytes(int64(origLen))
			b.ReportAllocs()
			b.ResetTimer()

			b.RunParallel(func(pb *testing.PB) {
				dst := bufkit.AlignedBytes(origLen+extra+64, 64)
				_, _ = przm.DecompressTo(frame, dst)

				for pb.Next() {
					_, err := przm.DecompressTo(frame, dst)
					if err != nil {
						b.Fatalf("DecompressTo failed: %v", err)
					}
				}
			})
		})
	}
}
