// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func compressBrotli11(t testing.TB, data []byte) []byte {
	tmpIn, err := os.CreateTemp("", "br_in_*.bin")
	if err != nil {
		t.Fatalf("failed to create temp in: %v", err)
	}
	defer func() {
		_ = os.Remove(tmpIn.Name())
	}()

	if _, err := tmpIn.Write(data); err != nil {
		t.Fatalf("failed to write temp in: %v", err)
	}
	_ = tmpIn.Close()

	tmpOut := tmpIn.Name() + ".br"
	defer func() {
		_ = os.Remove(tmpOut)
	}()

	cmd := exec.Command("brotli", "-q", "11", "-f", tmpIn.Name(), "-o", tmpOut)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("brotli -q 11 failed: %v, out: %s", err, string(out))
	}

	brData, err := os.ReadFile(tmpOut)
	if err != nil {
		t.Fatalf("failed to read brotli output: %v", err)
	}
	return brData
}

type HunterDataset struct {
	Name     string
	Category string
	Data     []byte
}

func loadHunterCorpora(t testing.TB) []HunterDataset {
	var datasets []HunterDataset

	// 1. JSON access logs & micro JSON
	datasets = append(datasets, HunterDataset{
		Name:     "json_logs_64K",
		Category: "JSON",
		Data:     bench.GenerateJSON(64 * 1024),
	})
	datasets = append(datasets, HunterDataset{
		Name:     "json_logs_256K",
		Category: "JSON",
		Data:     bench.GenerateJSON(256 * 1024),
	})

	// Micro JSON (repeating compact JSON events)
	microJSON := bytes.Repeat(
		[]byte(
			`{"id":10492,"status":"ok","code":200,"service":"auth","latency_ms":1.45,"tags":["prod","web"],"active":true}`+"\n",
		),
		250,
	)
	datasets = append(datasets, HunterDataset{
		Name:     "micro_json_25K",
		Category: "JSON",
		Data:     microJSON,
	})

	// 2. C/C++ source code & Technical Markdown
	readFile := func(rel string) []byte {
		candidates := []string{rel, filepath.Join("..", rel), filepath.Join(".", rel)}
		for _, c := range candidates {
			if data, err := os.ReadFile(c); err == nil && len(data) > 0 {
				return data
			}
		}
		return nil
	}

	if cCode := readFile(filepath.Join("src", "przm_dec.c")); len(cCode) > 0 {
		datasets = append(datasets, HunterDataset{
			Name:     "przm_dec.c",
			Category: "C/C++",
			Data:     cCode,
		})
	}
	if hCode := readFile(filepath.Join("include", "przm_dec.h")); len(hCode) > 0 {
		datasets = append(datasets, HunterDataset{
			Name:     "przm_dec.h",
			Category: "C/C++",
			Data:     hCode,
		})
	}
	if testC := readFile(filepath.Join("tests", "test_przm_dec.c")); len(testC) > 0 {
		datasets = append(datasets, HunterDataset{
			Name:     "test_przm_dec.c",
			Category: "C/C++",
			Data:     testC,
		})
	}
	if readme := readFile("README.md"); len(readme) > 0 {
		datasets = append(datasets, HunterDataset{
			Name:     "README.md",
			Category: "Markdown",
			Data:     readme,
		})
	}
	if mathDoc := readFile(filepath.Join("docs", "math_report.md")); len(mathDoc) > 0 {
		datasets = append(datasets, HunterDataset{
			Name:     "math_report.md",
			Category: "Markdown",
			Data:     mathDoc,
		})
	}

	// 3. CSS / Web tokens
	cssSnippet := bytes.Repeat(
		[]byte(
			`.btn{display:inline-flex;align-items:center;justify-content:center;padding:0.5rem 1rem;font-size:0.875rem;font-weight:500;border-radius:0.375rem;border:1px solid transparent;background-color:#4f46e5;color:#ffffff;cursor:pointer;box-shadow:0 1px 2px 0 rgba(0,0,0,0.05)}.btn:hover{background-color:#4338ca}.container{width:100%;max-width:80rem;margin:0 auto;padding-left:1.5rem;padding-right:1.5rem}@media (min-width: 768px){.md\:flex{display:flex}.md\:grid{display:grid}}`+"\n",
		),
		100,
	)
	datasets = append(datasets, HunterDataset{
		Name:     "tailwind_web_30K",
		Category: "CSS/Web",
		Data:     cssSnippet,
	})

	html5Snippet := bytes.Repeat(
		[]byte(
			`<!DOCTYPE html><html lang="en" class="h-full bg-slate-50"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>PRZM Web Architecture</title><link rel="stylesheet" href="/assets/css/app.min.css"><script src="/assets/js/runtime.min.js" defer></script></head><body class="h-full font-sans antialiased text-slate-800"><div class="min-h-full flex flex-col"><header class="sticky top-0 z-40 bg-white/95 backdrop-blur border-b border-slate-200"><div class="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 h-16 flex items-center justify-between"><div class="flex items-center space-x-6"><a href="/" class="flex items-center space-x-2 text-indigo-600 font-bold text-xl">CloudScale</a></div></div></header></div></body></html>`+"\n",
		),
		100,
	)
	datasets = append(datasets, HunterDataset{
		Name:     "html5_template_50K",
		Category: "CSS/Web",
		Data:     html5Snippet,
	})

	return datasets
}

func TestBrotliHunter_InitialAudit(t *testing.T) {
	datasets := loadHunterCorpora(t)

	t.Logf("\n%-22s | %-8s | %10s | %10s | %10s | %10s | %10s | %10s | %8s",
		"Dataset", "Category", "Raw Size", "Brotli-11", "PRZM L3", "PRZM L5", "PRZM L6+Web", "PWS", "Winner")
	t.Logf(
		"----------------------------------------------------------------------------------------------------------------------------------",
	)

	for _, ds := range datasets {
		rawLen := len(ds.Data)
		brComp := compressBrotli11(t, ds.Data)
		brLen := len(brComp)

		// PRZM L3
		przmCompL3, err := przm.Compress(ds.Data, 3)
		if err != nil {
			t.Fatalf("PRZM L3 failed for %s: %v", ds.Name, err)
		}
		decL3, err := przm.Decompress(przmCompL3)
		if err != nil || !bytes.Equal(decL3, ds.Data) {
			t.Fatalf("PRZM L3 lossless check failed for %s", ds.Name)
		}
		l3Len := len(przmCompL3)

		// PRZM L5 Archival
		przmCompL5, err := przm.CompressWithOptions(ds.Data, przm.CompressOptions{Level: 5, ArchivalMode: true})
		if err != nil {
			t.Fatalf("PRZM L5 failed for %s: %v", ds.Name, err)
		}
		decL5, err := przm.Decompress(przmCompL5)
		if err != nil || !bytes.Equal(decL5, ds.Data) {
			t.Fatalf("PRZM L5 lossless check failed for %s", ds.Name)
		}
		l5Len := len(przmCompL5)

		// PRZM L6 + BrotliWeb Dict
		przmCompL6Web, err := przm.CompressWithOptions(ds.Data, przm.CompressOptions{
			Level:    6,
			DictID:   przm.DictIDBrotliWeb,
			MinMatch: 3,
		})
		if err != nil {
			t.Fatalf("PRZM L6Web failed for %s: %v", ds.Name, err)
		}
		decL6Web, err := przm.Decompress(przmCompL6Web)
		if err != nil || !bytes.Equal(decL6Web, ds.Data) {
			t.Fatalf("PRZM L6Web lossless check failed for %s", ds.Name)
		}
		l6WebLen := len(przmCompL6Web)

		// PWS
		pwsLen := 999999999
		if pwsComp, err := przm.PWSEncode(ds.Data); err == nil && len(pwsComp) > 0 {
			decPWS, err := przm.PWSDecode(pwsComp)
			if err == nil && bytes.Equal(decPWS, ds.Data) {
				pwsLen = len(pwsComp)
			}
		}

		bestPRZM := min(l3Len, l5Len, l6WebLen, pwsLen)
		winner := "Brotli"
		if bestPRZM < brLen {
			winner = fmt.Sprintf("PRZM (%+.1f%%)", float64(brLen-bestPRZM)/float64(brLen)*100.0)
		} else {
			winner = fmt.Sprintf("Brotli (%+.1f%%)", float64(bestPRZM-brLen)/float64(brLen)*100.0)
		}

		t.Logf("%-22s | %-8s | %8d B | %8d B | %8d B | %8d B | %8d B | %8d B | %s",
			ds.Name, ds.Category, rawLen, brLen, l3Len, l5Len, l6WebLen, pwsLen, winner)
	}
}

func TestBrotliHunter_DiagnoseAll(t *testing.T) {
	datasets := loadHunterCorpora(t)
	for _, ds := range datasets {
		comp, err := przm.CompressWithOptions(ds.Data, przm.CompressOptions{Level: 5, ArchivalMode: true})
		if err != nil {
			t.Fatalf("Compress failed for %s: %v", ds.Name, err)
		}
		hdr, err := przm.ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("ParseFrameHeader failed for %s: %v", ds.Name, err)
		}
		s1 := 8
		sec2Off := hdr.DataOffset + s1
		sec3Off := sec2Off + int(hdr.CmdStreamSize)
		sec4Off := sec3Off + int(hdr.OffsetStreamSize)
		sec4Size := len(comp) - sec4Off
		s2Mode := comp[sec2Off]
		s2Raw := binary.LittleEndian.Uint32(comp[sec2Off+1 : sec2Off+5])
		s3Mode := comp[sec3Off]
		s3Raw := binary.LittleEndian.Uint32(comp[sec3Off+1 : sec3Off+5])
		t.Logf(
			"%-20s | Total: %6d | Sec2(Cmd): %5d (raw %5d, mode %d) | Sec3(Off): %5d (raw %5d, mode %d) | Sec4(Lit): %5d",
			ds.Name,
			len(comp),
			hdr.CmdStreamSize,
			s2Raw,
			s2Mode,
			hdr.OffsetStreamSize,
			s3Raw,
			s3Mode,
			sec4Size,
		)
	}
}

func TestBrotliHunter_DiagnoseOffsets(t *testing.T) {
	datasets := loadHunterCorpora(t)
	for _, ds := range datasets {
		if ds.Name != "przm_dec.c" && ds.Name != "math_report.md" {
			continue
		}
		var offsets []byte
		opts := przm.CompressOptions{
			Level:        5,
			ArchivalMode: true,
			LiteralProbe: func(b []byte) {}, // unused
		}
		comp, _ := przm.CompressWithOptions(ds.Data, opts)
		hdr, _ := przm.ParseFrameHeader(comp)
		s1 := 8
		sec2Off := hdr.DataOffset + s1
		sec3Off := sec2Off + int(hdr.CmdStreamSize)
		s3Size := int(hdr.OffsetStreamSize)
		sec3Data := comp[sec3Off : sec3Off+s3Size]
		s3Mode := sec3Data[0]
		s3RawLen := binary.LittleEndian.Uint32(sec3Data[1:5])

		// Decompress Section 3 to get raw offsets
		scratch := make([]byte, s3RawLen)
		var huffTable przm.CanonicalHuffmanTable
		_, err := przm.UnpackStream(scratch, sec3Data, &huffTable)
		if err != nil {
			t.Fatalf("%s unpack stream failed: %v", ds.Name, err)
		}
		offsets = scratch

		t.Logf("=== Dataset: %s (Raw Offsets: %d B, Cur Sec3: %d B mode %d) ===", ds.Name, len(offsets), s3Size, s3Mode)

		// Decompress Section 2 to count command types
		s2Size := int(hdr.CmdStreamSize)
		sec2Data := comp[sec2Off : sec2Off+s2Size]
		s2RawLen := binary.LittleEndian.Uint32(sec2Data[1:5])
		cmdScratch := make([]byte, s2RawLen)
		_, _ = przm.UnpackStream(cmdScratch, sec2Data, &huffTable)

		fusedRep := 0
		longRep := 0
		explicitMatch := 0
		litOnly := 0
		totRepLen := 0
		totExpLen := 0
		totLitLen := 0
		for idx := 0; idx < len(cmdScratch); {
			op := cmdScratch[idx]
			idx++
			if op < 0x80 {
				fusedRep++
				litLen := int((op >> 4) & 0x07)
				mLen := int((op>>2)&0x03) + 4
				totLitLen += litLen
				totRepLen += mLen
			} else if op < 0xC0 {
				explicitMatch++
				litLen := int((op >> 4) & 0x03)
				baseLen := int(op & 0x0F)
				mLen := baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdScratch[idx:])
					idx += max(n, 1)
					mLen = 19 + int(extra)
				}
				totLitLen += litLen
				totExpLen += mLen
			} else if op >= 0xC0 && op <= 0xCF {
				longRep++
				if idx < len(cmdScratch) {
					token := cmdScratch[idx]
					idx++
					litLen := int(token >> 4)
					baseLen := int(token & 0x0F)
					mLen := baseLen + 4
					if baseLen == 15 {
						extra, n := binary.Uvarint(cmdScratch[idx:])
						idx += max(n, 1)
						mLen = 19 + int(extra)
					}
					totLitLen += litLen
					totRepLen += mLen
				}
			} else {
				litOnly++
			}
		}

		repAvg := float64(totRepLen) / float64(max(fusedRep, 1))
		expAvg := float64(totExpLen) / float64(max(explicitMatch, 1))
		t.Logf("FusedRep: %d (bytes %d, avg %.1f) | Explicit: %d (bytes %d, avg %.1f) | Lits: %d B",
			fusedRep, totRepLen, repAvg, explicitMatch, totExpLen, expAvg, totLitLen)
	}
}

func TestBrotliHunter_DiagnoseLiterals(t *testing.T) {
	datasets := loadHunterCorpora(t)
	for _, ds := range datasets {
		if ds.Name != "tailwind_web_30K" && ds.Name != "html5_template_50K" && ds.Name != "micro_json_25K" {
			continue
		}
		var litCaptured []byte
		opts := przm.CompressOptions{
			Level:        5,
			ArchivalMode: true,
			LiteralProbe: func(b []byte) {
				litCaptured = bytes.Clone(b)
			},
		}
		comp, err := przm.CompressWithOptions(ds.Data, opts)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}
		hdr, _ := przm.ParseFrameHeader(comp)
		sec4Off := hdr.DataOffset + 8 + int(hdr.CmdStreamSize) + int(hdr.OffsetStreamSize)
		sec4Len := len(comp) - sec4Off
		isNibble := (hdr.Flags & przm.FlagNibble) != 0

		t.Logf("=== %s: Captured Lits: %d B | Stored Sec4: %d B (FlagNibble=%v) ===",
			ds.Name, len(litCaptured), sec4Len, isNibble)

		v4, errV4 := przm.VANSEncode4x(litCaptured)
		t.Logf("VANSEncode4x: err=%v, len=%d", errV4, len(v4))
	}
}
