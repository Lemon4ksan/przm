// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
	"unsafe"

	"github.com/lemon4ksan/przm/bench"
)

// TestChallengerZD_M3_1_DPNode_CacheAlignmentAndSize asserts that dpNode is strictly
// 32 bytes (power-of-two) and aligns to 64-byte CPU cache lines (2 nodes per line).
func TestChallengerZD_M3_1_DPNode_CacheAlignmentAndSize(t *testing.T) {
	nodeSize := unsafe.Sizeof(dpNode{})
	if nodeSize != 32 {
		t.Fatalf("dpNode size violation: got %d bytes, expected exactly 32 bytes", nodeSize)
	}

	nodeAlign := unsafe.Alignof(dpNode{})
	if nodeAlign < 4 {
		t.Fatalf("dpNode alignment too small: got %d", nodeAlign)
	}

	var arr [2]dpNode
	diff := uintptr(unsafe.Pointer(&arr[1])) - uintptr(unsafe.Pointer(&arr[0]))
	if diff != 32 {
		t.Fatalf("dpNode array stride violation: got %d, expected 32", diff)
	}

	// Verify field layout
	offsetCost := unsafe.Offsetof(arr[0].cost)
	offsetGen := unsafe.Offsetof(arr[0].gen)
	if offsetCost != 0 {
		t.Errorf("expected cost at offset 0, got %d", offsetCost)
	}
	if offsetGen != 28 {
		t.Errorf("expected gen at offset 28, got %d", offsetGen)
	}
}

// TestChallengerZD_M3_1_SubLengths_ExhaustiveOracle compares generateSubLengths against
// the mathematical ground truth S(L) = {4, 5, 6, 8, 12, 16, min(L, 18), L} for all L in [0, 65536].
func TestChallengerZD_M3_1_SubLengths_ExhaustiveOracle(t *testing.T) {
	groundTruthSubLens := func(L int) []int {
		if L < 4 {
			return nil
		}
		raw := []int{4, 5, 6, 8, 12, 16, min(L, 18), L}
		set := make(map[int]bool)
		var out []int
		for _, x := range raw {
			if x <= L && !set[x] {
				set[x] = true
				out = append(out, x)
			}
		}
		// Sort
		for i := 0; i < len(out); i++ {
			for j := i + 1; j < len(out); j++ {
				if out[i] > out[j] {
					out[i], out[j] = out[j], out[i]
				}
			}
		}
		return out
	}

	var buf [maxSubLenCandidates]int
	for L := 0; L <= 65536; L++ {
		// Fill buf with sentinel to catch out-of-bounds writes
		for k := range buf {
			buf[k] = -999
		}

		n := generateSubLengths(L, &buf)
		expected := groundTruthSubLens(L)

		if n != len(expected) {
			t.Fatalf("L=%d: got count %d, expected %d", L, n, len(expected))
		}
		for i := 0; i < n; i++ {
			if buf[i] != expected[i] {
				t.Fatalf("L=%d index %d: got %d, expected %d", L, i, buf[i], expected[i])
			}
		}
		// When L <= 18, only 0..n-1 are written, remaining elements must be untouched
		if L <= 18 {
			for k := n; k < maxSubLenCandidates; k++ {
				if buf[k] != -999 {
					t.Fatalf("L=%d corrupted buf at index %d: got %d", L, k, buf[k])
				}
			}
		} else {
			// When L > 18, fixed array literal initializes 0..7 and zeroes 8..15
			for k := 8; k < maxSubLenCandidates; k++ {
				if buf[k] != 0 {
					t.Fatalf("L=%d unexpected non-zero buf at index %d: got %d", L, k, buf[k])
				}
			}
		}
	}
}

// TestChallengerZD_M3_1_CalcEdgeCost_ExhaustiveOracle asserts that calcEdgeCostPRZM matches
// the exact specification for all match lengths, distances, and repeat codes.
func TestChallengerZD_M3_1_CalcEdgeCost_ExhaustiveOracle(t *testing.T) {
	groundTruthCost := func(mLen, dist, repCode int) int32 {
		cost := int32(28)
		if repCode >= 0 {
			if mLen <= 7 {
				return 14
			}
		} else {
			bracket := 0
			if dist >= 2097152 {
				bracket = 3
			} else if dist >= 16384 {
				bracket = 2
			} else if dist >= 128 {
				bracket = 1
			}
			cost = 14 + 13*int32(bracket+1)
		}
		if mLen >= 19 {
			cost += 14 * int32(uvarintLen(uint64(mLen-19)))
		}
		return cost
	}

	distances := []int{1, 127, 128, 16383, 16384, 2097151, 2097152, 8388608}
	repCodes := []int{-1, 0, 1, 2}
	lengths := []int{4, 5, 6, 7, 8, 18, 19, 20, 100, 1000, 65535}

	for _, d := range distances {
		for _, rep := range repCodes {
			for _, l := range lengths {
				got := calcEdgeCostPRZM(l, d, rep)
				want := groundTruthCost(l, d, rep)
				if got != want {
					t.Fatalf("calcEdgeCostPRZM(mLen=%d, dist=%d, rep=%d) = %d, want %d", l, d, rep, got, want)
				}
			}
		}
	}
}

// TestChallengerZD_M3_1_DPEpochRollover asserts that when curGen wraps around uint32,
// dpPad.curGen is properly reset to 1, table is cleared, and output is bit-exact.
func TestChallengerZD_M3_1_DPEpochRollover(t *testing.T) {
	// Construct input data with repeated structures to trigger optimal parsing
	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte((i * 17) ^ (i >> 3))
	}
	// Inject repeat patterns
	for i := 100; i < 50000; i += 256 {
		copy(data[i:i+64], data[i-64:i])
	}

	// 1. Compress with fresh scratchpad
	padFresh := padPool.Get()
	defer padPool.Put(padFresh)
	clear(padFresh.head)
	padFresh.hensel.Reset()
	padFresh.cmdBuf = padFresh.cmdBuf[:0]
	padFresh.literalBuf = padFresh.literalBuf[:0]
	padFresh.offsetBuf = padFresh.offsetBuf[:0]
	dpFresh := dpScratchPool.Get()
	defer dpScratchPool.Put(dpFresh)
	clear(dpFresh.table3[:])
	r0_1, r1_1, r2_1 := 0, 0, 0
	compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, padFresh, dpFresh, &r0_1, &r1_1, &r2_1, nil, nil)
	cmdFresh := make([]byte, len(padFresh.cmdBuf))
	copy(cmdFresh, padFresh.cmdBuf)
	litFresh := make([]byte, len(padFresh.literalBuf))
	copy(litFresh, padFresh.literalBuf)
	offFresh := make([]byte, len(padFresh.offsetBuf))
	copy(offFresh, padFresh.offsetBuf)

	// 2. Compress with scratchpad at epoch wrap boundary (math.MaxUint32)
	padRollover := padPool.Get()
	defer padPool.Put(padRollover)
	clear(padRollover.head)
	padRollover.hensel.Reset()
	padRollover.cmdBuf = padRollover.cmdBuf[:0]
	padRollover.literalBuf = padRollover.literalBuf[:0]
	padRollover.offsetBuf = padRollover.offsetBuf[:0]
	dpRollover := dpScratchPool.Get()
	defer dpScratchPool.Put(dpRollover)
	clear(dpRollover.table3[:])
	dpRollover.curGen = math.MaxUint32

	r0_2, r1_2, r2_2 := 0, 0, 0
	compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, padRollover, dpRollover, &r0_2, &r1_2, &r2_2, nil, nil)

	if dpRollover.curGen != 1 {
		t.Fatalf("curGen did not reset to 1 after wrap-around: got %d", dpRollover.curGen)
	}

	if !bytes.Equal(cmdFresh, padRollover.cmdBuf) {
		t.Fatalf("cmdBuf mismatch after epoch rollover")
	}
	if !bytes.Equal(litFresh, padRollover.literalBuf) {
		t.Fatalf("literalBuf mismatch after epoch rollover")
	}
	if !bytes.Equal(offFresh, padRollover.offsetBuf) {
		t.Fatalf("offsetBuf mismatch after epoch rollover")
	}
}

// TestChallengerZD_M3_1_BoundarySizes_LosslessRoundtrip stresses all boundary sizes
// at Level 3 optimal parsing and verifies 100% bit-exact restoration.
func TestChallengerZD_M3_1_BoundarySizes_LosslessRoundtrip(t *testing.T) {
	sizes := []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 12, 16, 17, 18, 19, 20,
		63, 64, 65, 127, 128, 129, 255, 256, 257,
		4095, 4096, 4097, 16383, 16384, 16385,
		65535, 65536, 65537, 131071, 131072, 131073,
		262144, 1048576,
	}

	rng := rand.New(rand.NewSource(42))

	for _, sz := range sizes {
		// Generate realistic corpora across data profiles
		payloads := []struct {
			name string
			data []byte
		}{
			{
				name: "repeated",
				data: bytes.Repeat([]byte("PRZM_ZERO_DRAG_L3_OPTIMAL_PARSER_"), (sz/33)+1)[:sz],
			},
			{
				name: "json",
				data: bench.GenerateJSON(sz),
			},
			{
				name: "tabular32",
				data: bench.GenerateTabular(sz, 32),
			},
			{
				name: "machine_code",
				data: bench.GenerateMachineCode(sz),
			},
			{
				name: "pseudo_random",
				data: make([]byte, sz),
			},
		}
		rng.Read(payloads[4].data)

		for _, p := range payloads {
			compressed, err := Compress(p.data, 3)
			if err != nil {
				t.Fatalf("Compress failed for size %d (%s): %v", sz, p.name, err)
			}

			// Decompress standard
			decomp, err := Decompress(compressed)
			if err != nil {
				t.Fatalf("Decompress failed for size %d (%s): %v", sz, p.name, err)
			}
			if !bytes.Equal(decomp, p.data) {
				t.Fatalf("Bit-exact mismatch for size %d (%s)", sz, p.name)
			}

			// DecompressTo zero-alloc (with required +64 SIMD cacheline padding)
			outBuf := make([]byte, sz+64)
			outSlice, err := DecompressTo(compressed, outBuf)
			if err != nil {
				t.Fatalf("DecompressTo failed for size %d (%s): %v", sz, p.name, err)
			}
			if len(outSlice) != sz {
				t.Fatalf("DecompressTo returned length %d, expected %d", len(outSlice), sz)
			}
			if !bytes.Equal(outSlice, p.data) {
				t.Fatalf("DecompressTo mismatch for size %d (%s)", sz, p.name)
			}
		}
	}
}

// TestChallengerZD_M3_1_ZeroAlloc_DecompressTo_Strict asserts that DecompressTo strictly
// incurs 0.00 heap allocations per run across all corpora and boundary sizes.
func TestChallengerZD_M3_1_ZeroAlloc_DecompressTo_Strict(t *testing.T) {
	testCases := []struct {
		name string
		data []byte
	}{
		{"Small_64B_JSON", bench.GenerateJSON(64)},
		{"Boundary_4KB_Tabular", bench.GenerateTabular(4096, 32)},
		{"Block_64KB_Code", bench.GenerateMachineCode(65536)},
		{"CrossBoundary_65537B_Tabular", bench.GenerateTabular(65537, 32)},
		{"Dedup_128KB_Tabular", bench.GenerateTabular(131072, 32)},
		{"Large_1MB_Pattern", func() []byte {
			buf := make([]byte, 1024*1024)
			pattern := []byte(`{"event":"metric_log","timestamp":1728391200,"cluster":"us-east-1","load":0.785,"status":"OK"}` + "\n")
			for i := 0; i < len(buf); i += len(pattern) {
				copy(buf[i:], pattern)
			}
			return buf
		}()},
	}

	for _, tc := range testCases {
		sz := len(tc.data)
		compressed, err := Compress(tc.data, 3)
		if err != nil {
			t.Fatalf("[%s] Compress failed: %v", tc.name, err)
		}

		extra := 0
		hdr, err := ParseFrameHeader(compressed)
		t.Logf("[%s] compLen=%d, magic=0x%08X, hdr=%+v, err=%v", tc.name, len(compressed), binary.LittleEndian.Uint32(compressed[:4]), hdr, err)
		if err == nil {
			if (hdr.Flags&FlagBCJ) != 0 && (hdr.Flags&FlagBCJSplit) != 0 {
				extra = 4 + 4*int(hdr.CallCount)
			}
		}

		outBuf := make([]byte, sz+extra+64)
		allocs := testing.AllocsPerRun(100, func() {
			outSlice, err := DecompressTo(compressed, outBuf)
			if err != nil || len(outSlice) != sz {
				t.Fatalf("[%s] DecompressTo failed: %v", tc.name, err)
			}
		})

		if allocs != 0.00 {
			t.Fatalf("[%s] DecompressTo alloc violation: got %.2f allocs/run, strictly expected 0.00", tc.name, allocs)
		}
	}
}

// TestChallengerZD_M3_1_OptimalDP_ZeroAllocInLoop verifies that compressBlockOptimal
// itself does not allocate inside the dynamic programming forward relaxation loop.
func TestChallengerZD_M3_1_OptimalDP_ZeroAllocInLoop(t *testing.T) {
	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte(i)
	}

	pad := padPool.Get()
	defer padPool.Put(pad)
	dpPad := dpScratchPool.Get()
	defer dpScratchPool.Put(dpPad)

	// Warm up
	r0, r1, r2 := 0, 0, 0
	compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, pad, dpPad, &r0, &r1, &r2, nil, nil)

	// Measure allocs in DP forward relaxation
	allocs := testing.AllocsPerRun(10, func() {
		pad.cmdBuf = pad.cmdBuf[:0]
		pad.literalBuf = pad.literalBuf[:0]
		pad.offsetBuf = pad.offsetBuf[:0]
		r0, r1, r2 = 0, 0, 0
		compressBlockOptimal(data, 0, len(data), len(data), 65536, WindowLog64K, 3, pad, dpPad, &r0, &r1, &r2, nil, nil)
	})

	if allocs != 0.00 {
		t.Fatalf("compressBlockOptimal incurred %.2f allocs/run, expected 0.00", allocs)
	}
}
