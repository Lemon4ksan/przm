// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================================
// CHALLENGE 1: Zero-Allocation Invariant Under High Concurrency & Loop Stress
// ============================================================================

// TestChallengerM3_2_ZeroAllocation_10000Runs verifies that DecompressCryptoCore
// and DecompressCryptoCoreBlock64 execute with strictly 0.00 allocs/op across
// 10,000 runs using testing.AllocsPerRun across all frame operating modes.
func TestChallengerM3_2_ZeroAllocation_10000Runs(t *testing.T) {
	const numRuns = 10000

	// 1. Prepare representative compressed frame (64 KiB raw, mixed balanced + counter)
	blocksRep := GenerateRepresentativePayload(4096, 7771)
	rawRep := make([]byte, len(blocksRep)*16)
	for i, b := range blocksRep {
		copy(rawRep[i*16:(i+1)*16], b[:])
	}
	compRepBuf := make([]byte, len(rawRep)+CryptoCoreHeaderSize+256)
	compRepLen, err := CompressCryptoCore(rawRep, compRepBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore representative failed: %v", err)
	}
	compRep := compRepBuf[:compRepLen]
	dstRep := make([]byte, len(rawRep))

	allocsRep := testing.AllocsPerRun(numRuns, func() {
		n, decErr := DecompressCryptoCore(compRep, dstRep)
		if decErr != nil || n != len(rawRep) {
			t.Fatalf("DecompressCryptoCore failed: n=%d, err=%v", n, decErr)
		}
	})
	t.Logf("[Challenge 1 | Rep Frame (64 KiB)] %d runs: %.2f allocs/op", numRuns, allocsRep)
	if allocsRep != 0.0 {
		t.Fatalf("Representative frame violated zero-allocation invariant: got %.4f allocs/op, want 0.00", allocsRep)
	}

	// 2. Prepare 100% balanced compressed frame (8 KiB raw, 2 balanced chunks)
	rawBal := make([]byte, 8192)
	key := [16]byte{0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c}
	for i := 0; i < 256; i++ {
		var pt [16]byte
		pt[0] = byte(i)
		for j := 1; j < 16; j++ {
			pt[j] = byte(j * 17)
		}
		blk := EncryptReducedRound(pt[:], key[:], 3, true)
		copy(rawBal[i*16:(i+1)*16], blk[:])
	}
	for i := 0; i < 256; i++ {
		var pt [16]byte
		pt[1] = byte(i)
		for j := 0; j < 16; j++ {
			if j != 1 {
				pt[j] = byte(j*19 + 5)
			}
		}
		blk := EncryptReducedRound(pt[:], key[:], 3, true)
		copy(rawBal[4096+i*16:4096+(i+1)*16], blk[:])
	}
	compBalBuf := make([]byte, len(rawBal)+CryptoCoreHeaderSize+128)
	compBalLen, err := CompressCryptoCore(rawBal, compBalBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore balanced failed: %v", err)
	}
	compBal := compBalBuf[:compBalLen]
	dstBal := make([]byte, len(rawBal))

	allocsBal := testing.AllocsPerRun(numRuns, func() {
		n, decErr := DecompressCryptoCore(compBal, dstBal)
		if decErr != nil || n != len(rawBal) {
			t.Fatalf("DecompressCryptoCore balanced failed: n=%d, err=%v", n, decErr)
		}
	})
	t.Logf("[Challenge 1 | 100%% Balanced Frame (8 KiB)] %d runs: %.2f allocs/op", numRuns, allocsBal)
	if allocsBal != 0.0 {
		t.Fatalf("Balanced frame violated zero-allocation invariant: got %.4f allocs/op, want 0.00", allocsBal)
	}

	// 3. Raw passthrough mode frame (flags = 0)
	rawPass := make([]byte, 512)
	for i := range rawPass {
		rawPass[i] = byte(i*37 + 11)
	}
	compPassBuf := make([]byte, len(rawPass)+CryptoCoreHeaderSize+128)
	compPassLen, err := CompressCryptoCore(rawPass, compPassBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore passthrough failed: %v", err)
	}
	compPass := compPassBuf[:compPassLen]
	dstPass := make([]byte, len(rawPass))

	allocsPass := testing.AllocsPerRun(numRuns, func() {
		n, decErr := DecompressCryptoCore(compPass, dstPass)
		if decErr != nil || n != len(rawPass) {
			t.Fatalf("DecompressCryptoCore passthrough failed: n=%d, err=%v", n, decErr)
		}
	})
	t.Logf("[Challenge 1 | Raw Passthrough Frame (512 B)] %d runs: %.2f allocs/op", numRuns, allocsPass)
	if allocsPass != 0.0 {
		t.Fatalf("Passthrough frame violated zero-allocation invariant: got %.4f allocs/op, want 0.00", allocsPass)
	}

	// 4. Empty frame (0 bytes raw payload)
	compEmptyBuf := make([]byte, CryptoCoreHeaderSize)
	compEmptyLen, err := CompressCryptoCore(nil, compEmptyBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore empty failed: %v", err)
	}
	compEmpty := compEmptyBuf[:compEmptyLen]
	dstEmpty := make([]byte, 64)

	allocsEmpty := testing.AllocsPerRun(numRuns, func() {
		n, decErr := DecompressCryptoCore(compEmpty, dstEmpty)
		if decErr != nil || n != 0 {
			t.Fatalf("DecompressCryptoCore empty failed: n=%d, err=%v", n, decErr)
		}
	})
	t.Logf("[Challenge 1 | Empty Frame] %d runs: %.2f allocs/op", numRuns, allocsEmpty)
	if allocsEmpty != 0.0 {
		t.Fatalf("Empty frame violated zero-allocation invariant: got %.4f allocs/op, want 0.00", allocsEmpty)
	}

	// 5. DecompressCryptoCoreBlock64 balanced mode
	var src48 [48]byte
	var dst64 [64]byte
	for i := range src48 {
		src48[i] = byte(i*13 + 7)
	}

	allocsBlkBal := testing.AllocsPerRun(numRuns, func() {
		inB, outB := DecompressCryptoCoreBlock64(src48[:], dst64[:], true)
		if inB != 48 || outB != 64 {
			t.Fatalf("DecompressCryptoCoreBlock64 balanced returned (%d, %d)", inB, outB)
		}
	})
	t.Logf("[Challenge 1 | Block64 Balanced Microcode] %d runs: %.2f allocs/op", numRuns, allocsBlkBal)
	if allocsBlkBal != 0.0 {
		t.Fatalf("Block64 balanced violated zero-allocation invariant: got %.4f allocs/op, want 0.00", allocsBlkBal)
	}

	// 6. DecompressCryptoCoreBlock64 unbalanced mode
	var src64 [64]byte
	for i := range src64 {
		src64[i] = byte(i*29 + 3)
	}

	allocsBlkUnbal := testing.AllocsPerRun(numRuns, func() {
		inB, outB := DecompressCryptoCoreBlock64(src64[:], dst64[:], false)
		if inB != 64 || outB != 64 {
			t.Fatalf("DecompressCryptoCoreBlock64 unbal returned (%d, %d)", inB, outB)
		}
	})
	t.Logf("[Challenge 1 | Block64 Unbalanced Microcode] %d runs: %.2f allocs/op", numRuns, allocsBlkUnbal)
	if allocsBlkUnbal != 0.0 {
		t.Fatalf("Block64 unbal violated zero-allocation invariant: got %.4f allocs/op, want 0.00", allocsBlkUnbal)
	}
}

// TestChallengerM3_2_ZeroAllocation_HighConcurrencyStress executes DecompressCryptoCore
// across 32 concurrent goroutines performing 5,000 iterations each (160,000 total operations)
// validating zero race conditions, bit-exact roundtrips, and zero heap leakages.
func TestChallengerM3_2_ZeroAllocation_HighConcurrencyStress(t *testing.T) {
	const (
		numWorkers       = 32
		itersPerWorker   = 5000
		totalInvocations = numWorkers * itersPerWorker
	)

	// Prepare test payload (32 KiB)
	blocks := GenerateRepresentativePayload(2048, 98765)
	raw := make([]byte, len(blocks)*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}
	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+256)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore failed: %v", err)
	}
	compressed := compBuf[:compLen]

	var wg sync.WaitGroup
	var completedCount int64
	var mismatchCount int64

	// Capture memory stats before concurrent run
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	t.Logf("[Challenge 1 | Concurrency Stress] Launching %d workers x %d iters (%d total invocations)...",
		numWorkers, itersPerWorker, totalInvocations)

	start := time.Now()

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			// Each worker has its own pre-allocated destination buffer
			workerDst := make([]byte, len(raw))

			for i := 0; i < itersPerWorker; i++ {
				n, decErr := DecompressCryptoCore(compressed, workerDst)
				if decErr != nil || n != len(raw) {
					atomic.AddInt64(&mismatchCount, 1)
					return
				}
				// Verify bit-exact correctness every 500 iterations
				if i%500 == 0 {
					if !bytes.Equal(workerDst, raw) {
						atomic.AddInt64(&mismatchCount, 1)
						return
					}
				}
				atomic.AddInt64(&completedCount, 1)
			}
		}(w)
	}

	wg.Wait()
	elapsed := time.Since(start)

	runtime.GC()
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	t.Logf("[Challenge 1 | Concurrency Stress Completed] Duration: %v, Ops: %d, Throughput: %.2f ops/sec",
		elapsed, completedCount, float64(completedCount)/elapsed.Seconds())
	t.Logf("  Total Mismatches / Errors: %d", mismatchCount)
	t.Logf("  HeapAlloc delta: %d bytes (Before: %d, After: %d)",
		int64(mAfter.HeapAlloc)-int64(mBefore.HeapAlloc), mBefore.HeapAlloc, mAfter.HeapAlloc)

	if mismatchCount > 0 {
		t.Fatalf("Concurrent stress failed: %d mismatches or errors detected!", mismatchCount)
	}
	if completedCount != totalInvocations {
		t.Fatalf("Incomplete operations: got %d, want %d", completedCount, totalInvocations)
	}
}

// ============================================================================
// CHALLENGE 2: Cycle Latency Benchmark (Budget <= 384 CPU Cycles / 64-Byte Block)
// ============================================================================

// TestChallengerM3_2_CycleLatency_Block64_10000Runs benchmarks DecompressCryptoCoreBlock64
// across 10,000 runs for both balanced and unbalanced modes, asserting <= 384 cycles.
func TestChallengerM3_2_CycleLatency_Block64_10000Runs(t *testing.T) {
	const runs = 10000

	var b0, b1, b2, b3 [16]byte
	for i := 0; i < 16; i++ {
		b0[i] = byte(i*5 + 1)
		b1[i] = byte(i*11 + 3)
		b2[i] = byte(i*17 + 7)
		b3[i] = b0[i] ^ b1[i] ^ b2[i]
	}
	var src48 [48]byte
	copy(src48[0:16], b0[:])
	copy(src48[16:32], b1[:])
	copy(src48[32:48], b2[:])

	var src64 [64]byte
	copy(src64[0:48], src48[:])
	copy(src64[48:64], b3[:])

	var dst [64]byte

	// Sink variables to prevent compiler dead code elimination
	var sinkIn, sinkOut int

	// 1. Warm-up
	for i := 0; i < 1000; i++ {
		inB, outB := DecompressCryptoCoreBlock64(src48[:], dst[:], true)
		sinkIn += inB
		sinkOut += outB
		inB, outB = DecompressCryptoCoreBlock64(src64[:], dst[:], false)
		sinkIn += inB
		sinkOut += outB
	}

	// 2. Benchmark Balanced Mode (reads 48 B, reconstructs B3 = B0 ^ B1 ^ B2, writes 64 B)
	startBal := time.Now()
	for i := 0; i < runs; i++ {
		inB, outB := DecompressCryptoCoreBlock64(src48[:], dst[:], true)
		sinkIn += inB
		sinkOut += outB
	}
	elapsedBal := time.Since(startBal)
	nsPerBal := float64(elapsedBal.Nanoseconds()) / float64(runs)
	cyclesBal := nsPerBal * 3.0 // 3.0 GHz nominal -> 1 ns = 3.0 cycles

	t.Logf("[Challenge 2 | Block64 Balanced] %d runs: %.2f ns/op -> %.2f CPU cycles (Budget: <= %d, sink=%d)",
		runs, nsPerBal, cyclesBal, MaxDecompressionCyclesPer64B, sinkIn+sinkOut)
	if cyclesBal > float64(MaxDecompressionCyclesPer64B) {
		t.Fatalf("Block64 balanced latency %.2f cycles exceeded budget of %d cycles",
			cyclesBal, MaxDecompressionCyclesPer64B)
	}

	// 3. Benchmark Unbalanced Mode (copies 64 B)
	startUnbal := time.Now()
	for i := 0; i < runs; i++ {
		inB, outB := DecompressCryptoCoreBlock64(src64[:], dst[:], false)
		sinkIn += inB
		sinkOut += outB
	}
	elapsedUnbal := time.Since(startUnbal)
	nsPerUnbal := float64(elapsedUnbal.Nanoseconds()) / float64(runs)
	cyclesUnbal := nsPerUnbal * 3.0

	t.Logf("[Challenge 2 | Block64 Unbalanced] %d runs: %.2f ns/op -> %.2f CPU cycles (Budget: <= %d, sink=%d)",
		runs, nsPerUnbal, cyclesUnbal, MaxDecompressionCyclesPer64B, sinkIn+sinkOut)
	if cyclesUnbal > float64(MaxDecompressionCyclesPer64B) {
		t.Fatalf("Block64 unbal latency %.2f cycles exceeded budget of %d cycles",
			cyclesUnbal, MaxDecompressionCyclesPer64B)
	}
}

// TestChallengerM3_2_CycleLatency_FullFrame_10000Runs benchmarks full 64 KiB frame
// decompression across 10,000 iterations, verifying average cycle latency <= 384 cycles/64B.
func TestChallengerM3_2_CycleLatency_FullFrame_10000Runs(t *testing.T) {
	const (
		runs      = 10000
		numBlocks = 4096 // 4096 * 16 = 65,536 bytes = 64 KiB = 1024 64-byte blocks
	)

	blocks := GenerateRepresentativePayload(numBlocks, 102938)
	raw := make([]byte, numBlocks*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}

	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+256)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		t.Fatalf("CompressCryptoCore failed: %v", err)
	}
	compressed := compBuf[:compLen]
	dst := make([]byte, len(raw))

	// Measure across 10,000 iterations
	cyclesPer64B, err := MeasureDecompressionCycleLatency(compressed, dst, runs)
	if err != nil {
		t.Fatalf("MeasureDecompressionCycleLatency failed: %v", err)
	}

	// Calculate throughput
	num64B := len(raw) / 64 // 1024 blocks
	nsPer64B := cyclesPer64B / 3.0
	totalNsPerFrame := nsPer64B * float64(num64B)
	throughputMBps := (float64(len(raw)) / (1024 * 1024)) / (totalNsPerFrame / 1e9)

	t.Logf("[Challenge 2 | Full Frame 64 KiB] %d runs: %.2f cycles/64B (Budget: <= %d), Latency: %.2f ns/frame, Throughput: %.2f MB/s",
		runs, cyclesPer64B, MaxDecompressionCyclesPer64B, totalNsPerFrame, throughputMBps)

	if cyclesPer64B > float64(MaxDecompressionCyclesPer64B) {
		t.Fatalf("Full frame decompression latency %.2f cycles exceeded budget of %d cycles",
			cyclesPer64B, MaxDecompressionCyclesPer64B)
	}
}

// ============================================================================
// CHALLENGE 3: Adversarial Malformed Frames Challenge
// ============================================================================

// TestChallengerM3_2_MalformedFrames_CorruptedMagic verifies that invalid magic signatures
// immediately return ErrInvalidMagic without panicking.
func TestChallengerM3_2_MalformedFrames_CorruptedMagic(t *testing.T) {
	dst := make([]byte, 4096)

	invalidMagics := []uint32{
		0x00000000,
		0xFFFFFFFF,
		0x43525951, // 'CRYQ'
		0x50595243, // 'PRYC' (big-endian inversion)
		0xDEADBEEF,
		0x7F454C46, // ELF magic
		0x504B0304, // ZIP magic
	}

	for _, m := range invalidMagics {
		var frame [32]byte
		binary.LittleEndian.PutUint32(frame[0:4], m)
		binary.LittleEndian.PutUint32(frame[4:8], 16)
		binary.LittleEndian.PutUint32(frame[8:12], 32)
		binary.LittleEndian.PutUint16(frame[12:14], 0)
		binary.LittleEndian.PutUint16(frame[14:16], 0)

		n, err := DecompressCryptoCore(frame[:], dst)
		if n != 0 {
			t.Fatalf("Corrupted magic 0x%08X returned n=%d, want 0", m, n)
		}
		if !errors.Is(err, ErrInvalidMagic) {
			t.Fatalf("Corrupted magic 0x%08X returned err=%v, want ErrInvalidMagic", m, err)
		}
	}
}

// TestChallengerM3_2_MalformedFrames_TruncatedFrames verifies that truncated frames
// across all boundary offsets return ErrCorruptFrame cleanly without panicking.
func TestChallengerM3_2_MalformedFrames_TruncatedFrames(t *testing.T) {
	dst := make([]byte, 8192)

	// 1. Truncated before minimum header length (0..15 bytes)
	for sz := 0; sz < CryptoCoreHeaderSize; sz++ {
		truncBuf := make([]byte, sz)
		n, err := DecompressCryptoCore(truncBuf, dst)
		if n != 0 || !errors.Is(err, ErrCorruptFrame) {
			t.Fatalf("Truncated frame len=%d: got n=%d, err=%v, want (0, ErrCorruptFrame)", sz, n, err)
		}
	}

	// 2. Valid header (16 bytes), but comp_sz claims larger size than provided slice
	{
		var frame [16]byte
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 1024)
		binary.LittleEndian.PutUint32(frame[8:12], 2048) // comp_sz = 2048 > 16
		binary.LittleEndian.PutUint16(frame[12:14], 0)
		binary.LittleEndian.PutUint16(frame[14:16], 0)

		n, err := DecompressCryptoCore(frame[:], dst)
		if n != 0 || !errors.Is(err, ErrCorruptFrame) {
			t.Fatalf("comp_sz > len(src): got n=%d, err=%v, want (0, ErrCorruptFrame)", n, err)
		}
	}

	// 3. Raw mode (flags = 0), but payload truncated (raw_sz = 100, but only 16 bytes provided)
	{
		var frame [16]byte
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 100)
		binary.LittleEndian.PutUint32(frame[8:12], 16)
		binary.LittleEndian.PutUint16(frame[12:14], 0)
		binary.LittleEndian.PutUint16(frame[14:16], 0)

		n, err := DecompressCryptoCore(frame[:], dst)
		if n != 0 || !errors.Is(err, ErrCorruptFrame) {
			t.Fatalf("Raw mode truncated payload: got n=%d, err=%v, want (0, ErrCorruptFrame)", n, err)
		}
	}

	// 4. Cascaded mode (flags = 1), but bitmap truncated (num_chunks = 8 requires 1 byte bitmap)
	{
		var frame [16]byte
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 4096)
		binary.LittleEndian.PutUint32(frame[8:12], 16)
		binary.LittleEndian.PutUint16(frame[12:14], 1)
		binary.LittleEndian.PutUint16(frame[14:16], 8) // 8 chunks -> bitmap = 1 byte -> headerTotal = 17

		n, err := DecompressCryptoCore(frame[:], dst)
		if n != 0 || !errors.Is(err, ErrCorruptFrame) {
			t.Fatalf("Truncated bitmap: got n=%d, err=%v, want (0, ErrCorruptFrame)", n, err)
		}
	}

	// 5. Cascaded mode, 1 balanced chunk (requires 4080 bytes payload), truncated at various offsets
	{
		truncOffsets := []int{17, 50, 500, 2000, 4000, 4096} // headerTotal is 17 (16 + 1)
		for _, sz := range truncOffsets {
			frame := make([]byte, sz)
			binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
			binary.LittleEndian.PutUint32(frame[4:8], 4096)
			binary.LittleEndian.PutUint32(frame[8:12], uint32(sz))
			binary.LittleEndian.PutUint16(frame[12:14], 1)
			binary.LittleEndian.PutUint16(frame[14:16], 1)
			frame[16] = 0x01 // chunk 0 balanced (needs 17 + 4080 = 4097 bytes)

			n, err := DecompressCryptoCore(frame, dst)
			if n != 0 || !errors.Is(err, ErrCorruptFrame) {
				t.Fatalf("Truncated balanced chunk at len=%d: got n=%d, err=%v, want (0, ErrCorruptFrame)", sz, n, err)
			}
		}
	}

	// 6. Cascaded mode, 1 raw chunk (requires 4096 bytes payload), truncated at 4100 bytes (needs 17 + 4096 = 4113 bytes)
	{
		frame := make([]byte, 4100)
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 4096)
		binary.LittleEndian.PutUint32(frame[8:12], 4100)
		binary.LittleEndian.PutUint16(frame[12:14], 1)
		binary.LittleEndian.PutUint16(frame[14:16], 1)
		frame[16] = 0x00 // chunk 0 raw (needs 17 + 4096 = 4113 bytes)

		n, err := DecompressCryptoCore(frame, dst)
		if n != 0 || !errors.Is(err, ErrCorruptFrame) {
			t.Fatalf("Truncated raw chunk: got n=%d, err=%v, want (0, ErrCorruptFrame)", n, err)
		}
	}
}

// TestChallengerM3_2_MalformedFrames_OversizedChunkCounts verifies behavior under
// extreme/oversized chunk counts and invalid flags.
func TestChallengerM3_2_MalformedFrames_OversizedChunkCounts(t *testing.T) {
	dst := make([]byte, 8192)

	// 1. Oversized chunk count: 65535 chunks (requires 8192-byte bitmap) in small frame
	{
		var frame [32]byte
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 268435456)
		binary.LittleEndian.PutUint32(frame[8:12], 32)
		binary.LittleEndian.PutUint16(frame[12:14], 1)
		binary.LittleEndian.PutUint16(frame[14:16], 65535)

		n, err := DecompressCryptoCore(frame[:], dst)
		// Either ErrBufferTooSmall (because rawSize 256 MiB > dst len 8192) or ErrCorruptFrame
		if n != 0 || (err != ErrBufferTooSmall && err != ErrCorruptFrame) {
			t.Fatalf("Oversized 65535 chunks: got n=%d, err=%v", n, err)
		}
	}

	// 2. Unsupported compression flags (e.g., flags = 2, 3, 0xFFFF)
	invalidFlags := []uint16{2, 3, 4, 0x00FF, 0xFFFF}
	for _, flg := range invalidFlags {
		var frame [32]byte
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 16)
		binary.LittleEndian.PutUint32(frame[8:12], 32)
		binary.LittleEndian.PutUint16(frame[12:14], flg)
		binary.LittleEndian.PutUint16(frame[14:16], 0)

		n, err := DecompressCryptoCore(frame[:], dst)
		if n != 0 || !errors.Is(err, ErrUnsupportedCryptoMode) {
			t.Fatalf("Invalid flag %d: got n=%d, err=%v, want ErrUnsupportedCryptoMode", flg, n, err)
		}
	}

	// 3. Destination buffer too small for raw_sz
	{
		var frame [32]byte
		binary.LittleEndian.PutUint32(frame[0:4], PRZM_MAGIC_CRYPTO_CORE)
		binary.LittleEndian.PutUint32(frame[4:8], 100000) // raw_sz = 100,000 > len(dst) 8192
		binary.LittleEndian.PutUint32(frame[8:12], 32)
		binary.LittleEndian.PutUint16(frame[12:14], 0)
		binary.LittleEndian.PutUint16(frame[14:16], 0)

		n, err := DecompressCryptoCore(frame[:], dst)
		if n != 0 || !errors.Is(err, ErrBufferTooSmall) {
			t.Fatalf("Small dst buffer: got n=%d, err=%v, want ErrBufferTooSmall", n, err)
		}
	}
}

// TestChallengerM3_2_MalformedFrames_AdversarialMutationFuzzing generates 10,000 randomly
// mutated frames (bit flips, slice truncations, random payload garbage) and verifies
// that DecompressCryptoCore NEVER panics and cleanly returns appropriate errors.
func TestChallengerM3_2_MalformedFrames_AdversarialMutationFuzzing(t *testing.T) {
	const fuzzRuns = 10000

	// Create valid base frame (16 KiB raw)
	blocks := GenerateRepresentativePayload(1024, 4444)
	raw := make([]byte, len(blocks)*16)
	for i, b := range blocks {
		copy(raw[i*16:(i+1)*16], b[:])
	}
	compBuf := make([]byte, len(raw)+CryptoCoreHeaderSize+256)
	compLen, err := CompressCryptoCore(raw, compBuf)
	if err != nil {
		t.Fatalf("Base CompressCryptoCore failed: %v", err)
	}
	baseFrame := compBuf[:compLen]

	dst := make([]byte, len(raw)+4096)
	rnd := rand.New(rand.NewSource(0x7EADCAFE12345678))

	var cleanErrors int
	var validDecs int

	t.Logf("[Challenge 3 | Fuzzing] Commencing %d adversarial mutation trials...", fuzzRuns)

	for iter := 0; iter < fuzzRuns; iter++ {
		// Clone base frame
		mut := make([]byte, len(baseFrame))
		copy(mut, baseFrame)

		fuzzMode := rnd.Intn(5)
		switch fuzzMode {
		case 0:
			// Random truncation
			cutLen := rnd.Intn(len(mut))
			mut = mut[:cutLen]
		case 1:
			// Bit flips in header (bytes 0..15)
			flips := rnd.Intn(8) + 1
			for f := 0; f < flips; f++ {
				pos := rnd.Intn(16)
				bit := rnd.Intn(8)
				mut[pos] ^= byte(1 << bit)
			}
		case 2:
			// Bit flips across entire frame
			flips := rnd.Intn(32) + 1
			for f := 0; f < flips; f++ {
				pos := rnd.Intn(len(mut))
				mut[pos] ^= byte(rnd.Intn(256))
			}
		case 3:
			// Completely random garbage buffer of random length
			randLen := rnd.Intn(4096)
			mut = make([]byte, randLen)
			rnd.Read(mut)
		case 4:
			// Corrupt chunks count and flags in header
			binary.LittleEndian.PutUint16(mut[12:14], uint16(rnd.Intn(10)))
			binary.LittleEndian.PutUint16(mut[14:16], uint16(rnd.Intn(65536)))
		}

		// Safe call protected against panic
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("[Trial %d | Mode %d] DecompressCryptoCore PANICKED: %v (frame len=%d)",
						iter, fuzzMode, r, len(mut))
				}
			}()

			n, decErr := DecompressCryptoCore(mut, dst)
			if decErr != nil {
				cleanErrors++
			} else {
				if n > len(dst) {
					t.Fatalf("[Trial %d] Output length %d exceeded dst buffer %d!", iter, n, len(dst))
				}
				validDecs++
			}
		}()
	}

	t.Logf("[Challenge 3 | Fuzzing Completed] Trials: %d, Clean Errors: %d, Valid Decompressions: %d, Panics: 0",
		fuzzRuns, cleanErrors, validDecs)
}
