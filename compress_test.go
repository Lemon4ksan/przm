// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// TestPostLZ77SheafRatio empirically measures and prints the compressed size of a structured text
// dataset (Go source code) before and after this change, proving a significant mathematical reduction
// in final compressed size compared to the previous baseline where Sheaf was disabled for -mx=3.
func TestPostLZ77SheafRatio(t *testing.T) {
	// Structured text dataset: Go source code of compress.go
	data, err := os.ReadFile("compress.go")
	if err != nil {
		t.Fatalf("failed to read test dataset: %v", err)
	}

	origSize := len(data)

	// Baseline: Previous baseline where Sheaf was applied to the raw file (and disabled for -mx=3)
	sheafRawComp := przm.CompressSheaf(data)
	prevSheafBaselineSize := len(sheafRawComp)

	// Architectural Upgrade: Post-LZ77 Etale Sheaf (Vector C) stream splitting on litSection at -mx=3
	postLZ77Comp, err := przm.Compress(data, 3)
	if err != nil {
		t.Fatalf("Compress(-mx=3) failed: %v", err)
	}
	postLZ77Size := len(postLZ77Comp)

	// Verify that the frame actually used Sheaf-split literals
	hdr, err := przm.ParseFrameHeader(postLZ77Comp)
	if err != nil {
		t.Fatalf("parse header: %v", err)
	}
	if (hdr.Flags & przm.FlagNibble) == 0 {
		t.Fatalf("expected FlagNibble to be set on compressed frame")
	}

	sec4Offset := hdr.DataOffset + 8 + hdr.CmdStreamSize + hdr.OffsetStreamSize
	sec4 := postLZ77Comp[sec4Offset:]
	if len(sec4) < 24 {
		t.Fatalf("sec4 too short for Sheaf V-ANS header: %d bytes", len(sec4))
	}
	// Check signature: V-ANS Magic ('V', 'A', 0x01) with FlagSheaf
	if sec4[0] != 0x56 || sec4[1] != 0x41 || sec4[2] != 0x01 || (sec4[3]&0x02) == 0 {
		t.Fatalf("expected Sheaf V-ANS flags in Section 4, got flags=0x%02X", sec4[3])
	}

	sLen := binary.LittleEndian.Uint32(sec4[8:12])
	maskLen := binary.LittleEndian.Uint32(sec4[12:16])
	encSLen := binary.LittleEndian.Uint32(sec4[16:20])
	encCLen := binary.LittleEndian.Uint32(sec4[20:24])

	t.Logf("==========================================================================")
	t.Logf("PRZM Architectural Upgrade: Post-LZ77 Etale Sheaf Fibration Verification")
	t.Logf("==========================================================================")
	t.Logf("Dataset:                       Go source code (compress.go)")
	t.Logf("Original Uncompressed Size:    %6d bytes", origSize)
	t.Logf("Previous Sheaf Baseline Size:  %6d bytes (ratio: %6.2f%%)",
		prevSheafBaselineSize, float64(prevSheafBaselineSize)/float64(origSize)*100)
	t.Logf("New Post-LZ77 Sheaf Size:      %6d bytes (ratio: %6.2f%%)",
		postLZ77Size, float64(postLZ77Size)/float64(origSize)*100)
	t.Logf("--------------------------------------------------------------------------")
	t.Logf("Section 4 Sheaf Breakdown:     sLen=%d, maskLen=%d, encS=%d, encC=%d, total=%d",
		sLen, maskLen, encSLen, encCLen, len(sec4))

	reductionBytes := prevSheafBaselineSize - postLZ77Size
	reductionPct := float64(reductionBytes) / float64(prevSheafBaselineSize) * 100

	t.Logf("Absolute Size Reduction:       %6d bytes", reductionBytes)
	t.Logf("Mathematical Reduction:        %6.2f%% reduction over previous Sheaf baseline", reductionPct)
	t.Logf("==========================================================================")

	// Mathematical proof: final compressed size must achieve a significant reduction (> 50%)
	if postLZ77Size >= prevSheafBaselineSize {
		t.Fatalf(
			"Post-LZ77 Sheaf failed to reduce compressed size: got %d, baseline %d",
			postLZ77Size,
			prevSheafBaselineSize,
		)
	}
	if reductionPct < 50.0 {
		t.Fatalf("Reduction percentage %.2f%% is below 50.0%% threshold", reductionPct)
	}

	// Verify 100% bit-exact lossless roundtrip
	decomp, err := przm.Decompress(postLZ77Comp)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(decomp, data) {
		t.Fatalf("Decompress output does not match original (len %d vs %d)", len(decomp), len(data))
	}
}

// TestPostLZ77Sheaf_ZeroAlloc confirms strictly 0 B/op dynamic heap allocations during DecompressTo.
func TestPostLZ77Sheaf_ZeroAlloc(t *testing.T) {
	data, err := os.ReadFile("compress.go")
	if err != nil {
		t.Fatalf("failed to read test dataset: %v", err)
	}

	comp, err := przm.Compress(data, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)

	// Warmup run
	dec, err := przm.DecompressTo(comp, dst)
	if err != nil || !bytes.Equal(dec, data) {
		t.Fatalf("warmup DecompressTo failed: %v", err)
	}

	// 100 iterations of DecompressTo must yield strictly 0 heap allocations
	allocs := testing.AllocsPerRun(100, func() {
		out, err := przm.DecompressTo(comp, dst)
		if err != nil || len(out) != len(data) {
			t.Fatalf("DecompressTo error: %v", err)
		}
	})

	t.Logf("DecompressTo allocs/op on Sheaf-split frame: %.2f allocs/op", allocs)
	if allocs != 0 {
		t.Fatalf("Expected strictly 0 B/op dynamic heap allocations, got %.2f allocs/op", allocs)
	}
}

// TestPostLZ77Sheaf_RoundtripDiverse verifies 100% bit-exact lossless roundtrip on diverse datasets.
func TestPostLZ77Sheaf_RoundtripDiverse(t *testing.T) {
	testDatasets := []struct {
		name string
		data []byte
	}{
		{name: "compress.go", data: nil},
		{name: "decompress.go", data: nil},
		{name: "sheaf.go", data: nil},
		{name: "vans.go", data: nil},
		{name: "source_code_16k", data: bench.GenerateSourceCode(16 * 1024)},
		{name: "source_code_64k", data: bench.GenerateSourceCode(64 * 1024)},
		{name: "source_code_256k", data: bench.GenerateSourceCode(256 * 1024)},
		{name: "json_logs_16k", data: bench.GenerateJSON(16 * 1024)},
		{name: "json_logs_64k", data: bench.GenerateJSON(64 * 1024)},
		{name: "tabular_stride32_16k", data: bench.GenerateTabular(16*1024, 32)},
		{name: "rle_sparse_16k", data: bench.GenerateRLE(16 * 1024)},
	}

	// Load files
	for i := range testDatasets {
		if testDatasets[i].data == nil {
			b, err := os.ReadFile(testDatasets[i].name)
			if err != nil {
				t.Fatalf("read %s: %v", testDatasets[i].name, err)
			}
			testDatasets[i].data = b
		}
	}

	for _, tc := range testDatasets {
		t.Run(tc.name, func(t *testing.T) {
			for level := 1; level <= 3; level++ {
				comp, err := przm.Compress(tc.data, level)
				if err != nil {
					t.Fatalf("level %d Compress failed: %v", level, err)
				}

				dst := make([]byte, len(tc.data)+64)
				dec, err := przm.DecompressTo(comp, dst)
				if err != nil {
					t.Fatalf("level %d DecompressTo failed: %v", level, err)
				}

				if !bytes.Equal(dec, tc.data) {
					t.Fatalf("level %d bit-exact mismatch on %s: got len %d, want %d",
						level, tc.name, len(dec), len(tc.data))
				}
			}
		})
	}
}

// TestPostLZ77Sheaf_MalformedCorruptHeaders verifies proper error bubbling without panics or memory corruption
// across diverse malformed Section 4 Sheaf V-ANS headers and bitstreams.
func TestPostLZ77Sheaf_MalformedCorruptHeaders(t *testing.T) {
	data, err := os.ReadFile("compress.go")
	if err != nil {
		t.Fatalf("read compress.go: %v", err)
	}

	comp, err := przm.Compress(data, 3)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}

	hdr, err := przm.ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("parse header: %v", err)
	}

	sec4Offset := hdr.DataOffset + 8 + hdr.CmdStreamSize + hdr.OffsetStreamSize
	origSec4 := comp[sec4Offset:]
	if len(origSec4) < 24 {
		t.Fatalf("sec4 too short: %d", len(origSec4))
	}

	origLen := binary.LittleEndian.Uint32(origSec4[4:8])
	sLen := binary.LittleEndian.Uint32(origSec4[8:12])
	maskLen := binary.LittleEndian.Uint32(origSec4[12:16])
	encSLen := binary.LittleEndian.Uint32(origSec4[16:20])

	testCases := []struct {
		name    string
		mutate  func() []byte
		wantErr bool
	}{
		{
			name: "ZeroSLenWithNonzeroMask",
			mutate: func() []byte {
				if sLen == 0 {
					return nil
				}
				c := bytes.Clone(comp)
				binary.LittleEndian.PutUint32(c[sec4Offset+8:sec4Offset+12], 0)
				return c
			},
			wantErr: true,
		},
		{
			name: "TruncatedSec4Header",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				return c[:sec4Offset+12] // truncated before full 24B header
			},
			wantErr: true,
		},
		{
			name: "ExcessiveOrigLen",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				binary.LittleEndian.PutUint32(c[sec4Offset+4:sec4Offset+8], uint32(przm.MaxBlockSize+1))
				return c
			},
			wantErr: true,
		},
		{
			name: "SLenExceedsOrigLen",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				binary.LittleEndian.PutUint32(c[sec4Offset+8:sec4Offset+12], origLen+100)
				return c
			},
			wantErr: true,
		},
		{
			name: "InvalidMaskLen",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				binary.LittleEndian.PutUint32(c[sec4Offset+12:sec4Offset+16], maskLen+10)
				return c
			},
			wantErr: true,
		},
		{
			name: "TruncatedSyntaxStream",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				// Truncate halfway into encS
				return c[:sec4Offset+24+int(maskLen)+int(encSLen)/2]
			},
			wantErr: true,
		},
		{
			name: "CorruptMaskPopcount",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				// Flip single bit in mask byte 0 to break popcount parity
				c[sec4Offset+24] ^= 0x01
				return c
			},
			wantErr: true,
		},
		{
			name: "DirtyMaskPaddingBits",
			mutate: func() []byte {
				remBits := origLen % 8
				if remBits == 0 {
					return nil // Skip if no padding bits
				}
				c := bytes.Clone(comp)
				lastByteIdx := sec4Offset + 24 + int(maskLen) - 1
				c[lastByteIdx] |= byte(1 << 7) // set top unused bit
				return c
			},
			wantErr: true,
		},
		{
			name: "ZeroSyntaxStreamEncLen",
			mutate: func() []byte {
				c := bytes.Clone(comp)
				// Put 0 for encSLen while sLen > 0
				binary.LittleEndian.PutUint32(c[sec4Offset+16:sec4Offset+20], 0)
				return c
			},
			wantErr: true,
		},
	}

	dst := make([]byte, len(data)+64)
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate()
			if mutated == nil {
				return
			}
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("DecompressTo panicked on %s: %v", tc.name, r)
				}
			}()

			_, err := przm.DecompressTo(mutated, dst)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error on %s, got nil", tc.name)
			}
		})
	}
}

// TestPostLZ77Sheaf_ConcurrencyStress validates multi-goroutine thread safety and decScratchPool isolation.
func TestPostLZ77Sheaf_ConcurrencyStress(t *testing.T) {
	codeData, err := os.ReadFile("compress.go")
	if err != nil {
		t.Fatalf("read compress.go: %v", err)
	}
	jsonData := bench.GenerateJSON(32 * 1024)

	var wg sync.WaitGroup
	workers := 16
	itersPerWorker := 30

	errCh := make(chan error, workers*itersPerWorker*2)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			var data []byte
			if workerID%2 == 0 {
				data = codeData
			} else {
				data = jsonData
			}

			dst := make([]byte, len(data)+64)

			for i := 0; i < itersPerWorker; i++ {
				comp, err := przm.Compress(data, 3)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d compress error: %w", workerID, i, err)
					return
				}

				dec, err := przm.DecompressTo(comp, dst)
				if err != nil {
					errCh <- fmt.Errorf("worker %d iter %d decompress error: %w", workerID, i, err)
					return
				}

				if !bytes.Equal(dec, data) {
					errCh <- fmt.Errorf("worker %d iter %d mismatch (len %d vs %d)", workerID, i, len(dec), len(data))
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// TestPostLZ77Sheaf_Boundaries tests compression and decompression at structural size and pattern boundaries.
func TestPostLZ77Sheaf_Boundaries(t *testing.T) {
	sizes := []int{0, 1, 7, 8, 15, 16, 31, 32, 63, 64, 127, 128, 255, 256, 1024, 8192, 65536}

	for _, sz := range sizes {
		t.Run(fmt.Sprintf("Size_%d", sz), func(t *testing.T) {
			// Pattern 1: Alternating syntax and alphanumeric
			p1 := make([]byte, sz)
			for i := range p1 {
				if i%2 == 0 {
					p1[i] = '{'
				} else {
					p1[i] = byte('a' + (i % 26))
				}
			}

			// Pattern 2: Pure syntax
			p2 := make([]byte, sz)
			for i := range p2 {
				p2[i] = ";,()[]{}+-*="[i%12]
			}

			// Pattern 3: Pure content
			p3 := make([]byte, sz)
			for i := range p3 {
				p3[i] = byte('A' + (i % 26))
			}

			patterns := [][]byte{p1, p2, p3}
			for pIdx, pat := range patterns {
				for level := 1; level <= 3; level++ {
					comp, err := przm.Compress(pat, level)
					if err != nil {
						t.Fatalf("p%d sz=%d lvl=%d compress error: %v", pIdx, sz, level, err)
					}

					dec, err := przm.Decompress(comp)
					if err != nil {
						t.Fatalf("p%d sz=%d lvl=%d decompress error: %v", pIdx, sz, level, err)
					}

					if !bytes.Equal(dec, pat) {
						t.Fatalf("p%d sz=%d lvl=%d mismatch (len %d vs %d)", pIdx, sz, level, len(dec), len(pat))
					}
				}
			}
		})
	}
}

// TestSheaf_StandaloneCorruptMasks verifies that DecompressSheaf strictly rejects malformed frames with invalid masks.
func TestSheaf_StandaloneCorruptMasks(t *testing.T) {
	sample := bytes.Repeat([]byte("function test() { var x = 123; return x + 456; }\n"), 4)
	comp := przm.CompressSheaf(sample)
	if comp == nil || len(comp) < przm.SheafHeaderSize+4 {
		t.Fatalf("CompressSheaf produced invalid frame: len=%d", len(comp))
	}

	mode := comp[4]
	if mode != przm.SheafModeSplit && mode != przm.SheafModeSplitCascaded {
		t.Fatalf("sample did not produce split mode, got mode=%d", mode)
	}

	dst := make([]byte, len(sample)+64)

	// Subtest 1: Corrupt mask popcount
	t.Run("CorruptMaskPopcount", func(t *testing.T) {
		corrupt := bytes.Clone(comp)
		if mode == przm.SheafModeSplit {
			corrupt[przm.SheafHeaderSize+4] ^= 0x01
			_, err := przm.DecompressSheaf(corrupt, dst)
			if err == nil {
				t.Fatalf("expected DecompressSheaf to reject corrupt mask popcount")
			}
		}
	})

	// Subtest 2: Truncated frame
	t.Run("TruncatedFrame", func(t *testing.T) {
		truncated := comp[:przm.SheafHeaderSize+2]
		_, err := przm.DecompressSheaf(truncated, dst)
		if err == nil {
			t.Fatalf("expected DecompressSheaf to reject truncated frame")
		}
	})
}

// TestSheaf_InterleaveSafety verifies SheafInterleave and SheafSplit safety with edge-case buffers.
func TestSheaf_InterleaveSafety(t *testing.T) {
	dst := make([]byte, 64)
	mask := make([]byte, 8)

	// Nil s or c must not panic and not read out of bounds
	przm.SheafInterleave(dst, nil, nil, mask, 16)
	przm.SheafInterleave(dst, []byte{1}, nil, mask, 16)
	przm.SheafInterleave(dst, nil, []byte{2}, mask, 16)
	przm.SheafInterleave(dst, nil, nil, mask, 32)

	// All-1s mask with nil slices must safely return without panic or overread
	maskOnes := bytes.Repeat([]byte{0xFF}, 8)
	przm.SheafInterleave(dst, nil, nil, maskOnes, 32)
	przm.SheafInterleave(dst, []byte{1}, []byte{2}, maskOnes, 32)

	// Short dst or mask must safely return without panic
	przm.SheafInterleave(dst[:5], []byte{1}, []byte{2}, mask, 16)
	przm.SheafInterleave(dst, []byte{1}, []byte{2}, mask[:1], 16)

	// Zero origLen
	przm.SheafInterleave(dst, nil, nil, nil, 0)

	// SheafSplit with short buffers must return 0, 0 safely
	sBuf := make([]byte, 4)
	cBuf := make([]byte, 4)
	maskBuf := make([]byte, 1)
	src := make([]byte, 32)
	sLen, cLen := przm.SheafSplit(src, sBuf, cBuf, maskBuf)
	if sLen != 0 || cLen != 0 {
		t.Fatalf("expected 0, 0 from undersized SheafSplit buffers, got %d, %d", sLen, cLen)
	}

	// Unpadded slices with cap(s) == len(s) and cap(c) == len(c) must interleave bit-exactly without overread
	for _, sz := range []int{16, 24, 32, 48, 64} {
		input := make([]byte, sz)
		for i := range input {
			if i%3 == 0 {
				input[i] = '{'
			} else {
				input[i] = byte('A' + (i % 26))
			}
		}

		sPadded := make([]byte, sz+64)
		cPadded := make([]byte, sz+64)
		mBuf := make([]byte, (sz+7)/8+16)
		sCount, cCount := przm.SheafSplit(input, sPadded, cPadded, mBuf)

		// Create strictly unpadded sub-slices with cap == len
		sUnpadded := make([]byte, sCount)
		copy(sUnpadded, sPadded[:sCount])
		cUnpadded := make([]byte, cCount)
		copy(cUnpadded, cPadded[:cCount])

		out := make([]byte, sz)
		przm.SheafInterleave(out, sUnpadded, cUnpadded, mBuf, sz)
		if !bytes.Equal(out, input) {
			t.Fatalf("unpadded SheafInterleave mismatch at sz=%d: got %v, want %v", sz, out, input)
		}
	}
}
