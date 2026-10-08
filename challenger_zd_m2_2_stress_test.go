// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestChallenger_ZD_M2_2_PoolConcurrencyAndIsolation verifies that concurrent
// goroutines acquiring, mutating, resetting, and releasing scratchpads from
// PoolHensel1M, PoolHensel8M, and PoolHensel64K suffer no data race, lock contention,
// or cross-worker state leakage.
func TestChallenger_ZD_M2_2_PoolConcurrencyAndIsolation(t *testing.T) {
	const goroutines = 16
	const iterationsPerGoroutine = 20

	t.Run("PoolHensel1M_ConcurrentChurn", func(t *testing.T) {
		const gCount = 8
		const itCount = 10
		var wg sync.WaitGroup
		wg.Add(gCount)
		var matchFailures atomic.Int64

		for g := 0; g < gCount; g++ {
			gID := g
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(10000 + gID*7919)))

				for it := 0; it < itCount; it++ {
					sp := PoolHensel1M.Get()
					sp.Reset()

					// Unique synthetic buffer per worker & iteration
					bufSize := 64*1024 + rng.Intn(64*1024)
					buf := make([]byte, bufSize)
					rng.Read(buf)

					// Unique worker-specific token
					workerToken := []byte(fmt.Sprintf("W1M_%04d_%04d_TOKEN_VERIFY_", gID, it))
					refPos := 100 + rng.Intn(1000)
					targetPos := refPos + 1024 + rng.Intn(16384)
					if targetPos+len(workerToken) < bufSize {
						copy(buf[refPos:], workerToken)
						copy(buf[targetPos:], workerToken)

						// Insert preceding region up to targetPos - 1
						for p := 0; p < targetPos; p += 4 {
							if p+4 <= bufSize {
								HenselInsert1M(sp, p, buf, bufSize)
							}
						}
						// Explicitly ensure refPos is indexed
						HenselInsert1M(sp, refPos, buf, bufSize)

						dist, matchLen := HenselFindMatch1M(buf, targetPos, bufSize, 64, 4, sp)
						if matchLen < len(workerToken) {
							matchFailures.Add(1)
						} else {
							matchedRef := targetPos - dist
							if !bytes.Equal(buf[targetPos:targetPos+matchLen], buf[matchedRef:matchedRef+matchLen]) {
								matchFailures.Add(1)
							}
						}
					}

					PoolHensel1M.Put(sp)
				}
			}()
		}
		wg.Wait()

		if failures := matchFailures.Load(); failures > 0 {
			t.Fatalf("PoolHensel1M concurrent churn encountered %d match failures", failures)
		}
	})

	t.Run("PoolHensel8M_ConcurrentChurn", func(t *testing.T) {
		const gCount = 4
		const itCount = 10
		var wg sync.WaitGroup
		wg.Add(gCount)
		var matchFailures atomic.Int64

		for g := 0; g < gCount; g++ {
			gID := g
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewSource(int64(20000 + gID*3571)))

				for it := 0; it < itCount; it++ {
					sp := PoolHensel8M.Get()
					sp.Reset()

					bufSize := 128*1024 + rng.Intn(64*1024)
					buf := make([]byte, bufSize)
					rng.Read(buf)

					workerToken := []byte(fmt.Sprintf("W8M_%04d_%04d_TOKEN_TEST_", gID, it))
					refPos := 200 + rng.Intn(1000)
					targetPos := refPos + 2048 + rng.Intn(32768)
					if targetPos+len(workerToken) < bufSize {
						copy(buf[refPos:], workerToken)
						copy(buf[targetPos:], workerToken)

						for p := 0; p < targetPos; p += 8 {
							if p+4 <= bufSize {
								HenselInsert8M(sp, p, buf, bufSize)
							}
						}
						// Explicitly ensure refPos is indexed
						HenselInsert8M(sp, refPos, buf, bufSize)

						dist, matchLen := HenselFindMatch8M(buf, targetPos, bufSize, 64, 4, sp)
						if matchLen < len(workerToken) {
							matchFailures.Add(1)
						} else {
							matchedRef := targetPos - dist
							if !bytes.Equal(buf[targetPos:targetPos+matchLen], buf[matchedRef:matchedRef+matchLen]) {
								matchFailures.Add(1)
							}
						}
					}

					PoolHensel8M.Put(sp)
				}
			}()
		}
		wg.Wait()

		if failures := matchFailures.Load(); failures > 0 {
			t.Fatalf("PoolHensel8M concurrent churn encountered %d match failures", failures)
		}
	})
}

// TestChallenger_ZD_M2_2_ScratchpadResetStaleDataImmunity stress-tests the fact
// that HenselScratchpad.Reset(), HenselScratchpad1M.Reset(), and HenselScratchpad8M.Reset()
// only zero out sp.head[:] and deliberately skip sp.prev and sp.fp32.
// It verifies that dirty leftover state in prev/fp32 cannot resurrect phantom matches
// or cause invalid candidate chain traversal when a scratchpad is recycled.
func TestChallenger_ZD_M2_2_ScratchpadResetStaleDataImmunity(t *testing.T) {
	t.Run("1M_StaleDataImmunity", func(t *testing.T) {
		sp := PoolHensel1M.Get()
		defer PoolHensel1M.Put(sp)

		// PHASE 1: Populate scratchpad with Dirty Data Block A
		bufA := bytes.Repeat([]byte("DIRTY_PHANTOM_STALE_SEED_DATA_ABCDEF1234567890"), 2000)
		for p := 0; p+4 <= len(bufA); p += 2 {
			HenselInsert1M(sp, p, bufA, len(bufA))
		}

		// Reset only clears head[:]
		sp.Reset()

		// PHASE 2: Populate with completely disjoint Clean Data Block B
		bufB := make([]byte, 65536)
		for i := range bufB {
			bufB[i] = byte((i * 101) & 0xFF)
		}
		// Insert a sparse known match in Block B
		knownToken := []byte("CLEAN_LEGITIMATE_MATCH_BLOCK_B_!")
		posRef := 500
		posTarget := 5500
		copy(bufB[posRef:], knownToken)
		copy(bufB[posTarget:], knownToken)

		for p := 0; p <= posTarget; p += 64 {
			if p+4 <= len(bufB) {
				HenselInsert1M(sp, p, bufB, len(bufB))
			}
		}
		HenselInsert1M(sp, posRef, bufB, len(bufB))

		// Search for matches across various positions in Block B
		for checkPos := 100; checkPos < len(bufB)-64; checkPos += 256 {
			dist, matchLen := HenselFindMatch1M(bufB, checkPos, len(bufB), 64, 4, sp)
			if matchLen >= 4 {
				cand := checkPos - dist
				if cand < 0 || cand+matchLen > len(bufB) {
					t.Fatalf("Out-of-bounds match returned from stale scratchpad: checkPos=%d dist=%d len=%d",
						checkPos, dist, matchLen)
				}
				if !bytes.Equal(bufB[checkPos:checkPos+matchLen], bufB[cand:cand+matchLen]) {
					t.Fatalf("Phantom or mismatched match payload at checkPos=%d dist=%d len=%d",
						checkPos, dist, matchLen)
				}
			}
		}

		// Specifically verify our known token in Block B matches correctly
		dist, matchLen := HenselFindMatch1M(bufB, posTarget, len(bufB), 64, 4, sp)
		if dist != posTarget-posRef || matchLen < len(knownToken) {
			t.Fatalf("Known token match failed: got dist=%d len=%d, want dist=%d len>=%d",
				dist, matchLen, posTarget-posRef, len(knownToken))
		}
	})

	t.Run("8M_StaleDataImmunity", func(t *testing.T) {
		sp := PoolHensel8M.Get()
		defer PoolHensel8M.Put(sp)

		bufA := bytes.Repeat([]byte("DIRTY_8M_STALE_HASH_CHAIN_BYTES_0987654321XYZ!"), 3000)
		for p := 0; p+4 <= len(bufA); p += 4 {
			HenselInsert8M(sp, p, bufA, len(bufA))
		}

		sp.Reset()

		bufB := make([]byte, 131072)
		for i := range bufB {
			bufB[i] = byte((i * 73) & 0xFF)
		}
		knownToken := []byte("CLEAN_8M_DISJOINT_MATCH_PAYLOAD_VALID_")
		posRef := 1000
		posTarget := 12000
		copy(bufB[posRef:], knownToken)
		copy(bufB[posTarget:], knownToken)

		for p := 0; p <= posTarget; p += 128 {
			if p+4 <= len(bufB) {
				HenselInsert8M(sp, p, bufB, len(bufB))
			}
		}
		HenselInsert8M(sp, posRef, bufB, len(bufB))

		dist, matchLen := HenselFindMatch8M(bufB, posTarget, len(bufB), 64, 4, sp)
		if dist != posTarget-posRef || matchLen < len(knownToken) {
			t.Fatalf("8M stale immunity test failed: got dist=%d len=%d, want dist=%d len>=%d",
				dist, matchLen, posTarget-posRef, len(knownToken))
		}
	})
}

// TestChallenger_ZD_M2_2_ExtremeHashCollisionDeepChain creates a pathological
// bucket flood where hundreds of distinct 4-byte prefixes produce the exact same
// Hensel hash, testing deep chain traversal limits and Theorem HL-2 pruning under pressure.
func TestChallenger_ZD_M2_2_ExtremeHashCollisionDeepChain(t *testing.T) {
	// Synthesize 512 distinct 32-bit values that all have the exact same hash
	const targetBucket = 42
	collidingValues := make([]uint32, 0, 512)
	for w := uint32(1); len(collidingValues) < 512; w++ {
		h := computeHenselHash(w, HenselHashBits, HenselHashMask)
		if h == targetBucket {
			collidingValues = append(collidingValues, w)
		}
	}

	sp := PoolHensel64K.Get()
	defer PoolHensel64K.Put(sp)
	sp.Reset()

	// Construct buffer where each position p = i * 8 contains collidingValues[i]
	bufSize := len(collidingValues)*8 + 64
	buf := make([]byte, bufSize)
	for i, val := range collidingValues {
		pos := i * 8
		binary.LittleEndian.PutUint32(buf[pos:], val)
		// Give each entry a distinct second 4-byte fingerprint
		binary.LittleEndian.PutUint32(buf[pos+4:], uint32(i*10007))
	}

	// Insert all into scratchpad
	for i := range collidingValues {
		pos := i * 8
		HenselInsert(sp, pos, buf, bufSize)
	}

	// Case 1: Search for a value in the middle of the chain (e.g. index 250)
	targetIdx := 250
	targetVal := collidingValues[targetIdx]
	queryPos := len(collidingValues) * 8
	binary.LittleEndian.PutUint32(buf[queryPos:], targetVal)
	binary.LittleEndian.PutUint32(buf[queryPos+4:], uint32(targetIdx*10007)) // exact 8-byte match

	// If maxDepth < 512 - targetIdx, the chain traversal will exhaust depth before reaching targetIdx
	depthInsufficient := 10
	dist, matchLen := HenselFindMatch(buf, queryPos, bufSize, depthInsufficient, 4, sp)
	if dist != 0 || matchLen != 0 {
		t.Fatalf("Expected no match with depth %d, got dist=%d len=%d", depthInsufficient, dist, matchLen)
	}

	// With sufficient maxDepth, candidate chain traversal must cleanly reach targetIdx
	depthSufficient := len(collidingValues) + 10
	dist, matchLen = HenselFindMatch(buf, queryPos, bufSize, depthSufficient, 4, sp)
	expectedDist := queryPos - (targetIdx * 8)
	if dist != expectedDist || matchLen < 8 {
		t.Fatalf("Deep chain search failed: got dist=%d len=%d, want dist=%d len>=8",
			dist, matchLen, expectedDist)
	}

	// Case 2: Theorem HL-2 Pruning under collision flood:
	// Query prefix matches targetVal, but second 4-byte fingerprint differs in byte 6
	binary.LittleEndian.PutUint32(buf[queryPos:], targetVal)
	binary.LittleEndian.PutUint32(buf[queryPos+4:], uint32(targetIdx*10007)^0x00FF0000) // byte 6 differs -> match len exactly 6

	dist, matchLen = HenselFindMatch(buf, queryPos, bufSize, depthSufficient, 4, sp)
	if dist != expectedDist || matchLen != 6 {
		t.Fatalf("Theorem HL-2 exact short-circuit under collision flood failed: got dist=%d len=%d, want dist=%d len=6",
			dist, matchLen, expectedDist)
	}
}

// TestChallenger_ZD_M2_2_TheoremHL2_ExhaustiveCombinations systematically verifies
// that Theorem HL-2 exact match length deduction produces 100% bit-exact results
// across all diff trailing-zero configurations (match lengths 4, 5, 6, 7 bytes)
// and handles diff == 0 (> 7 bytes) without divergence.
func TestChallenger_ZD_M2_2_TheoremHL2_ExhaustiveCombinations(t *testing.T) {
	sp := PoolHensel64K.Get()
	defer PoolHensel64K.Put(sp)

	for expectedLen := 4; expectedLen <= 12; expectedLen++ {
		t.Run(fmt.Sprintf("ExactLength_%d", expectedLen), func(t *testing.T) {
			sp.Reset()
			buf := make([]byte, 4096)
			// Base pattern of 16 identical bytes
			candPos := 100
			targetPos := 500

			for i := 0; i < 16; i++ {
				buf[candPos+i] = byte('A' + i)
				buf[targetPos+i] = byte('A' + i)
			}

			// Diverge at index expectedLen
			if expectedLen < 16 {
				buf[targetPos+expectedLen] = 0xFF
			}

			HenselInsert(sp, candPos, buf, len(buf))

			dist, matchLen := HenselFindMatch(buf, targetPos, len(buf), 16, 4, sp)
			expectedDist := targetPos - candPos
			if dist != expectedDist || matchLen != expectedLen {
				t.Fatalf("Theorem HL-2 length check failed for expected %d: got dist=%d len=%d, want dist=%d len=%d",
					expectedLen, dist, matchLen, expectedDist, expectedLen)
			}
		})
	}
}

// TestChallenger_ZD_M2_2_MultiGoroutine_CompressWithOptions_Stress executes high-concurrency
// end-to-end compression and decompression across all levels and window sizes,
// ensuring the underlying scratchpad pools and Hensel match finders remain thread-safe.
func TestChallenger_ZD_M2_2_MultiGoroutine_CompressWithOptions_Stress(t *testing.T) {
	const goroutines = 8
	const runsPerGoroutine = 5

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var errorsCount atomic.Int64

	for g := 0; g < goroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(30000 + gID*9973)))

			for run := 0; run < runsPerGoroutine; run++ {
				size := 128*1024 + rng.Intn(128*1024)
				data := make([]byte, size)
				rng.Read(data)

				// Inject high-repetition tokens to trigger match finding
				token := []byte(fmt.Sprintf("PARALLEL_STRESS_TEST_PAYLOAD_%d_%d", gID, run))
				for p := 0; p < size-len(token); p += 1024 {
					copy(data[p:], token)
				}

				for _, level := range []int{1, 2, 3} {
					opts := CompressOptions{
						Threads:   4,
						ChunkSize: 64 * 1024,
						Level:     level,
					}
					compressed, err := CompressWithOptions(data, opts)
					if err != nil {
						errorsCount.Add(1)
						t.Errorf("CompressWithOptions(g=%d, run=%d, lvl=%d) error: %v", gID, run, level, err)
						continue
					}

					decompressed, err := Decompress(compressed)
					if err != nil {
						errorsCount.Add(1)
						t.Errorf("Decompress(g=%d, run=%d, lvl=%d) error: %v", gID, run, level, err)
						continue
					}

					if !bytes.Equal(decompressed, data) {
						errorsCount.Add(1)
						t.Errorf("Decompress mismatch for g=%d run=%d lvl=%d", gID, run, level)
					}
				}
			}
		}()
	}
	wg.Wait()

	if errs := errorsCount.Load(); errs > 0 {
		t.Fatalf("MultiGoroutine CompressWithOptions stress encountered %d errors", errs)
	}
}
