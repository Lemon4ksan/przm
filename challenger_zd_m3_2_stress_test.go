// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestChallengerZD_M3_2_EpochRollover verifies that when curGen reaches math.MaxUint32,
// Compress(data, 3) on a 64KB block resets curGen to 1, table is cleared,
// roundtrip is bit-exact, and DecompressTo maintains 0.00 allocs.
func TestChallengerZD_M3_2_EpochRollover(t *testing.T) {
	data := make([]byte, 65536)
	for i := range data {
		data[i] = byte((i * 37) ^ (i >> 3))
	}
	for i := 256; i < 60000; i += 512 {
		copy(data[i:i+128], data[i-128:i])
	}

	dp := dpScratchPool.Get()
	dp.curGen = math.MaxUint32
	dpScratchPool.Put(dp)

	comp, err := Compress(data, 3)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dpAfter := dpScratchPool.Get()
	genAfter := dpAfter.curGen
	dpScratchPool.Put(dpAfter)

	if genAfter != 1 {
		t.Fatalf("curGen did not rollover to 1: got %d", genAfter)
	}

	decomp := make([]byte, len(data)+1024)
	allocs := testing.AllocsPerRun(50, func() {
		out, err := DecompressTo(comp, decomp)
		if err != nil || len(out) != len(data) {
			t.Fatalf("DecompressTo failed: %v", err)
		}
	})
	if allocs != 0.00 {
		t.Fatalf("DecompressTo incurred %.2f allocs, expected 0.00", allocs)
	}
	if !bytes.Equal(decomp[:len(data)], data) {
		t.Fatalf("Lossless roundtrip mismatch after epoch rollover")
	}
}

// TestChallengerZD_M3_2_ConcurrencyRace runs 16 concurrent goroutines compressing
// 64KB randomized blocks via Compress(data, 3) and verifies bit-exact lossless roundtrips.
func TestChallengerZD_M3_2_ConcurrencyRace(t *testing.T) {
	const numGoroutines = 16
	const iters = 4
	var wg sync.WaitGroup
	wg.Add(numGoroutines)
	var errCount atomic.Int64

	for g := 0; g < numGoroutines; g++ {
		gID := g
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(42000 + gID*7919)))
			buf := make([]byte, 65536)
			for it := 0; it < iters; it++ {
				rng.Read(buf)
				for j := 256; j < 60000; j += 1024 {
					copy(buf[j:j+256], buf[j-256:j])
				}
				comp, err := Compress(buf, 3)
				if err != nil {
					errCount.Add(1)
					t.Errorf("worker %d iter %d Compress failed: %v", gID, it, err)
					return
				}
				decomp, err := Decompress(comp)
				if err != nil {
					errCount.Add(1)
					t.Errorf("worker %d iter %d Decompress failed: %v", gID, it, err)
					return
				}
				if !bytes.Equal(decomp, buf) {
					errCount.Add(1)
					t.Errorf("worker %d iter %d bit-exact roundtrip mismatch", gID, it)
					return
				}
			}
		}()
	}

	wg.Wait()
	if errs := errCount.Load(); errs > 0 {
		t.Fatalf("ConcurrencyRace encountered %d failures", errs)
	}
}
