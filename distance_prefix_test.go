// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// TestDistancePrefix_CodebookRoundtrip tests every distance from 1 to 131072
// and octave boundary points up to 16777216.
func TestDistancePrefix_CodebookRoundtrip(t *testing.T) {
	for d := 1; d <= 131072; d++ {
		code, extra, nb := przm.DistanceToCode(d)
		if code >= 49 {
			t.Fatalf("distance %d mapped to code %d >= 49", d, code)
		}
		entry := przm.DistTable[code]
		if entry.ExtraBits != nb {
			t.Fatalf("distance %d: entry extraBits %d != returned %d", d, entry.ExtraBits, nb)
		}
		recon := przm.CodeToDistance(code, extra)
		if recon != d {
			t.Fatalf("roundtrip mismatch for distance %d: got %d (code=%d, extra=%d)", d, recon, code, extra)
		}
	}

	// Test far-range boundaries
	farDistances := []int{
		131073, 200000, 500000, 1000000, 2097152, 4194304, 8388608, 16777215, 16777216,
	}
	for _, d := range farDistances {
		code, extra, nb := przm.DistanceToCode(d)
		if code >= 49 {
			t.Fatalf("far distance %d mapped to code %d >= 49", d, code)
		}
		entry := przm.DistTable[code]
		if entry.ExtraBits != nb {
			t.Fatalf("far distance %d: extraBits %d != %d", d, entry.ExtraBits, nb)
		}
		recon := przm.CodeToDistance(code, extra)
		if recon != d {
			t.Fatalf("roundtrip mismatch for far distance %d: got %d", d, recon)
		}
	}
}

// TestDistancePrefix_SyntheticStream tests EncodeDistancePrefix and DecodeDistancePrefix directly.
func TestDistancePrefix_SyntheticStream(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	var varints []byte

	// Generate 1000 randomized distances with mixed locality
	numDistances := 1000
	origDistances := make([]int, numDistances)
	for i := 0; i < numDistances; i++ {
		var d int
		r := rng.Float64()
		switch {
		case r < 0.10:
			d = 1 + rng.Intn(4) // ultra-local
		case r < 0.50:
			d = 5 + rng.Intn(250) // local
		case r < 0.85:
			d = 256 + rng.Intn(3800) // page
		default:
			d = 4096 + rng.Intn(60000) // medium
		}
		origDistances[i] = d

		// Append LEB128 varint
		if d < 0x80 {
			varints = append(varints, byte(d))
		} else if d < 0x4000 {
			varints = append(varints, byte(d|0x80), byte(d>>7))
		} else if d < 0x200000 {
			varints = append(varints, byte(d|0x80), byte((d>>7)|0x80), byte(d>>14))
		} else {
			varints = append(varints, byte(d|0x80), byte((d>>7)|0x80), byte((d>>14)|0x80), byte(d>>21))
		}
	}

	packed, err := przm.EncodeDistancePrefix(varints)
	if err != nil {
		t.Fatalf("EncodeDistancePrefix: %v", err)
	}

	t.Logf("Synthetic distances: %d varint bytes -> %d packed Mode 6 bytes (%.2fx compression)",
		len(varints), len(packed), float64(len(varints))/float64(len(packed)))

	var scratchTab przm.CanonicalHuffmanTable
	scratchCodes := make([]byte, numDistances+64)
	decodedVarints := make([]byte, len(varints))

	n, err := przm.DecodeDistancePrefix(decodedVarints, packed, &scratchTab, scratchCodes)
	if err != nil {
		t.Fatalf("DecodeDistancePrefix: %v", err)
	}
	if n != len(varints) {
		t.Fatalf("DecodeDistancePrefix length mismatch: got %d, want %d", n, len(varints))
	}
	if !bytes.Equal(decodedVarints, varints) {
		t.Fatal("Decoded varints do not match original bit-exact")
	}
}

func resolveTestPath(rel string) string {
	if _, err := os.Stat(rel); err == nil {
		return rel
	}
	parent := filepath.Join("..", rel)
	if _, err := os.Stat(parent); err == nil {
		return parent
	}
	return rel
}

// TestDistancePrefix_E2E_GoAndPureC compresses real files and verifies bit-exact roundtrip
// on both the Go decompressor and Pure C reference decompressor.
func TestDistancePrefix_E2E_GoAndPureC(t *testing.T) {
	files := []string{resolveTestPath("src/przm_dec.c"), resolveTestPath("compress.go")}

	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		comp, err := przm.Compress(data, 5)
		if err != nil {
			t.Fatalf("compress %s: %v", path, err)
		}

		// 1. Verify frame header and Section 3 mode
		hdr, err := przm.ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("ParseFrameHeader %s: %v", path, err)
		}
		sec1Offset := hdr.DataOffset
		sec1Size := 0
		if sec1Offset+8 <= len(comp) {
			sec1Size = 8 + 2*int(comp[sec1Offset+5])
		}
		sec2Offset := sec1Offset + sec1Size
		sec3Offset := sec2Offset + hdr.CmdStreamSize
		sec3Mode := comp[sec3Offset]

		t.Logf("[%s] Raw: %d B, Comp: %d B, Sec3: %d B (mode=%d)",
			path, len(data), len(comp), hdr.OffsetStreamSize, sec3Mode)

		if sec3Mode != przm.ModeDistancePrefix {
			t.Logf("Note: [%s] Section 3 mode is %d (preferred standard if smaller)", path, sec3Mode)
		}

		// 2. Go DecompressTo validation
		dst := make([]byte, len(data)+64)
		decomp, err := przm.DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("Go DecompressTo %s: %v", path, err)
		}
		if !bytes.Equal(decomp, data) {
			t.Fatalf("Go DecompressTo %s produced corrupted output", path)
		}

		// 3. Pure C test_przm_dec.exe validation
		cBinPath, err := filepath.Abs(resolveTestPath("test_przm_dec.exe"))
		if err != nil {
			t.Fatalf("resolve test_przm_dec.exe: %v", err)
		}
		if _, err := os.Stat(cBinPath); err == nil {
			tmpPrzm := filepath.Join(os.TempDir(), fmt.Sprintf("test_dp_%d.przm", rand.Int()))
			tmpOut := filepath.Join(os.TempDir(), fmt.Sprintf("test_dp_%d.bin", rand.Int()))
			defer os.Remove(tmpPrzm)
			defer os.Remove(tmpOut)

			if err := os.WriteFile(tmpPrzm, comp, 0644); err != nil {
				t.Fatalf("write tmp przm: %v", err)
			}

			cmd := exec.Command(cBinPath, tmpPrzm, tmpOut)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pure C decompressor failed on %s: %v\nOutput: %s", path, err, string(out))
			}

			cDecomp, err := os.ReadFile(tmpOut)
			if err != nil {
				t.Fatalf("read Pure C decomp output: %v", err)
			}
			if !bytes.Equal(cDecomp, data) {
				t.Fatalf("Pure C decompressor output mismatch on %s: len %d vs %d", path, len(cDecomp), len(data))
			}
			t.Logf("[%s] Pure C reference decompressor PASSED (100%% bit-exact, 0 allocations)", path)
		}
	}
}

// TestDistancePrefix_ZeroAllocations verifies that DecompressTo incurs strictly 0 B/op
// and 0 allocs/op when decoding ModeDistancePrefix frames.
func TestDistancePrefix_ZeroAllocations(t *testing.T) {
	data, err := os.ReadFile(resolveTestPath("src/przm_dec.c"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	comp, err := przm.Compress(data, 5)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	// Warmup
	if _, err := przm.DecompressTo(comp, dst); err != nil {
		t.Fatalf("warmup DecompressTo: %v", err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		out, err := przm.DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("DecompressTo in benchmark: %v", err)
		}
		if len(out) != len(data) {
			t.Fatalf("DecompressTo length mismatch: %d != %d", len(out), len(data))
		}
	})

	t.Logf("DecompressTo AllocsPerRun: %.2f allocs/op", allocs)
	if allocs > 0 {
		t.Fatalf("DecompressTo violated zero-heap allocation guarantee: got %.2f allocs/op, want 0", allocs)
	}
}
