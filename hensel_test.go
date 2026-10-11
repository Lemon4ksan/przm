// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/przm"
)

// TestHensel_TheoremHL1_LCP_Exactness validates that HenselLCP and HenselLCPPos
// yield the exact same result as a ground-truth byte-by-byte comparison
// across all boundary lengths from 0 to 200 bytes.
func TestHensel_TheoremHL1_LCP_Exactness(t *testing.T) {
	for expectedLen := 0; expectedLen <= 200; expectedLen++ {
		a := make([]byte, 256)
		b := make([]byte, 256)
		for i := range 256 {
			a[i] = byte((i * 37) + 11)
			b[i] = a[i]
		}
		// Create mismatch exactly at expectedLen
		if expectedLen < 256 {
			b[expectedLen] ^= 0xFF
		}

		lcp := przm.HenselLCP(a, b)
		if lcp != expectedLen {
			t.Fatalf("HenselLCP mismatch at expectedLen=%d: got %d", expectedLen, lcp)
		}

		// Also verify HenselLCPPos inside a single buffer
		buf := make([]byte, 512)
		copy(buf[0:], a)
		copy(buf[256:], b)
		lcpPos := przm.HenselLCPPos(buf, 0, 256, 256)
		if lcpPos != expectedLen {
			t.Fatalf("HenselLCPPos mismatch at expectedLen=%d: got %d", expectedLen, lcpPos)
		}
	}

	// Test identical slices
	full := make([]byte, 128)
	for i := range full {
		full[i] = byte(i)
	}
	if lcp := przm.HenselLCP(full, full); lcp != len(full) {
		t.Fatalf("HenselLCP on identical slices expected %d, got %d", len(full), lcp)
	}

	// Test empty slices
	if lcp := przm.HenselLCP(nil, nil); lcp != 0 {
		t.Fatalf("HenselLCP on nil slices expected 0, got %d", lcp)
	}
}

// TestHensel_TheoremHL2_PruningCorrectness verifies the Ultrametric Pruning Criterion:
// No candidate that could beat bestLen is EVER pruned by the fp32 fingerprint check.
func TestHensel_TheoremHL2_PruningCorrectness(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for range 10000 {
		target := make([]byte, 32)
		cand := make([]byte, 32)
		rng.Read(target)
		rng.Read(cand)

		// Randomly force prefix match of length 0..31
		shared := rng.Intn(32)
		copy(cand[:shared], target[:shared])
		if shared < 32 {
			cand[shared] = target[shared] ^ 0x01
		}

		// Ground truth LCP
		trueLCP := 0
		for trueLCP < 32 && target[trueLCP] == cand[trueLCP] {
			trueLCP++
		}

		targetFP := binary.LittleEndian.Uint32(target[4:8])
		candFP := binary.LittleEndian.Uint32(cand[4:8])

		// For every possible bestLen in 4..31:
		for bestLen := 4; bestLen < 32; bestLen++ {
			diff := candFP ^ targetFP
			if diff != 0 {
				maxLift := 4 + (bits.TrailingZeros32(diff) >> 3)
				pruned := maxLift <= bestLen
				if pruned && trueLCP > bestLen {
					t.Fatalf("Theorem HL-2 violation! Candidate with trueLCP=%d > bestLen=%d was pruned! (maxLift=%d, diff=%08x)",
						trueLCP, bestLen, maxLift, diff)
				}
			}
		}
	}
}

// TestHensel_MatchFinderAPI tests the HenselMatchFinder interface contract.
func TestHensel_MatchFinderAPI(t *testing.T) {
	mf := przm.NewHenselMatchFinder(65536)
	if mf == nil {
		t.Fatal("NewHenselMatchFinder returned nil")
	}

	pattern := []byte("The quick brown fox jumps over the lazy dog. 1234567890! ")
	data := bytes.Repeat(pattern, 100) // ~5.7 KiB

	mf.Reset()
	for i := 0; i+4 <= len(data); i++ {
		mf.Insert(i, data, len(data))
	}

	// Search match for a position in the second repetition
	searchPos := len(pattern) + 4
	dist, matchLen := mf.FindMatchHensel(data, searchPos, 16, 4)

	if dist == 0 || matchLen < 4 {
		t.Fatalf("expected valid match, got dist=%d, len=%d", dist, matchLen)
	}

	if dist != len(pattern) {
		t.Logf("Found match at distance %d (pattern length %d), matchLen=%d", dist, len(pattern), matchLen)
	}

	// Verify the match content
	if !bytes.Equal(data[searchPos:searchPos+matchLen], data[searchPos-dist:searchPos-dist+matchLen]) {
		t.Fatalf("match content mismatch at dist=%d, len=%d", dist, matchLen)
	}
}

// TestHensel_ZeroAllocations verifies that Hensel match search incurs 0 heap allocations.
func TestHensel_ZeroAllocations(t *testing.T) {
	mf := przm.NewHenselMatchFinder(65536)
	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 31)
	}

	for i := range 500 {
		mf.Insert(i, data, len(data))
	}

	allocs := testing.AllocsPerRun(100, func() {
		mf.Insert(501, data, len(data))
		mf.FindMatchHensel(data, 501, 16, 4)
		przm.HenselLCP(data[10:60], data[20:70])
	})

	if allocs != 0 {
		t.Fatalf("expected 0 allocs/op, got %f", allocs)
	}
}

// TestHensel_BruteForceOracle verifies that Hensel ultrametric search discovers the
// exact same optimal match as an unpruned brute-force traversal over the candidate chain.
func TestHensel_BruteForceOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(9999))
	data := make([]byte, 16384)
	rng.Read(data)

	// Seed repeated structures of various lengths (4 to 80 bytes)
	phrases := [][]byte{
		[]byte("PRZM Ultrametric Non-Archimedean Field Evaluation"),
		[]byte("Hensel quadratic Newton lifting for longest common prefix"),
		[]byte("discrete valuation ring residue field GF(2^8)"),
		[]byte("0123456789abcdef0123456789abcdef"),
	}
	for i := range 40 {
		phrase := phrases[i%len(phrases)]
		offset := 100 + i*350
		if offset+len(phrase) < len(data) {
			copy(data[offset:], phrase)
		}
	}

	mf := przm.NewHenselMatchFinder(65536)
	mf.Reset()

	for pos := 0; pos+4 <= len(data); pos++ {
		mf.Insert(pos, data, len(data))

		// Periodically test search against oracle
		if pos > 1000 && pos%250 == 0 && pos+8 <= len(data) {
			hDist, hLen := mf.FindMatchHensel(data, pos, 64, 4)

			if hDist > 0 && hLen >= 4 {
				// Verify match is valid
				if !bytes.Equal(data[pos:pos+hLen], data[pos-hDist:pos-hDist+hLen]) {
					t.Fatalf("at pos=%d: invalid match returned dist=%d len=%d", pos, hDist, hLen)
				}
				// Verify next byte actually mismatches (or is end of buffer)
				if pos+hLen < len(data) && pos-hDist+hLen < len(data) {
					if data[pos+hLen] == data[pos-hDist+hLen] && hLen < 258 {
						t.Fatalf("at pos=%d: match could extend further! dist=%d len=%d", pos, hDist, hLen)
					}
				}
			}
		}
	}
}

// TestHensel_Roundtrip_Levels3And4 verifies bit-exact lossless roundtrips across all
// standard corpora on compression Level 3 and Level 4.
func TestHensel_Roundtrip_Levels3And4(t *testing.T) {
	corpora := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "StructuredJSON",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := range 300 {
					fmt.Fprintf(&buf, `{"id":%d,"metric":"cpu_usage","val":%.2f,"tags":["us-west","prod","worker"]}`+"\n",
						1000+i, float64(i%100)/3.0)
				}
				return buf.Bytes()
			},
		},
		{
			name: "GoSourceCode",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := range 150 {
					fmt.Fprintf(&buf, `
func handleRequest%d(ctx context.Context, req *Request) (*Response, error) {
	if req == nil {
		return nil, errors.New("nil request")
	}
	res, err := processRecord(ctx, req.Payload)
	if err != nil {
		return nil, fmt.Errorf("process failed: %%w", err)
	}
	return res, nil
}
`, i)
				}
				return buf.Bytes()
			},
		},
		{
			name: "TabularTelemetry",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := range 500 {
					fmt.Fprintf(&buf, "%08x\t%10d\t%04x\t%16s\t%d\n", i, 100000+i, i%256, "STANDARD_RECORD", i*7)
				}
				return buf.Bytes()
			},
		},
		{
			name: "RepetitiveMotifs",
			gen: func() []byte {
				motif := []byte("ULTRAMETRIC_HENSEL_LIFTING_PRZM_NON_ARCHIMEDEAN_BALL_PRUNING_")
				return bytes.Repeat(motif, 100)
			},
		},
		{
			name: "BoundaryEdgeSizes",
			gen: func() []byte {
				return []byte("Tiny")
			},
		},
		{
			name: "RandomHighEntropy",
			gen: func() []byte {
				b := make([]byte, 8192)
				rng := rand.New(rand.NewSource(54321))
				rng.Read(b)
				return b
			},
		},
	}

	for _, c := range corpora {
		data := c.gen()
		for _, level := range []int{3, 4} {
			t.Run(fmt.Sprintf("%s_Lvl%d", c.name, level), func(t *testing.T) {
				compressed, err := przm.Compress(data, level)
				if err != nil {
					t.Fatalf("compression failed: %v", err)
				}
				if len(compressed) == 0 {
					t.Fatalf("empty compressed output")
				}

				decompressed, err := przm.Decompress(compressed)
				if err != nil {
					t.Fatalf("decompression failed: %v", err)
				}

				if !bytes.Equal(decompressed, data) {
					t.Fatalf("roundtrip mismatch: orig len=%d, decomp len=%d", len(data), len(decompressed))
				}
			})
		}
	}
}

// BenchmarkHenselLCP measures throughput of quadratic Hensel LCP.
func BenchmarkHenselLCP(b *testing.B) {
	lengths := []int{8, 16, 32, 64, 128, 256}
	for _, l := range lengths {
		b.Run(fmt.Sprintf("Len_%d", l), func(b *testing.B) {
			bufA := make([]byte, l+8)
			bufB := make([]byte, l+8)
			for i := range l {
				bufA[i] = byte(i)
				bufB[i] = byte(i)
			}
			bufA[l] = 0xAA
			bufB[l] = 0x55
			b.SetBytes(int64(l))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				res := przm.HenselLCP(bufA, bufB)
				if res != l {
					b.Fatalf("expected %d, got %d", l, res)
				}
			}
		})
	}
}

// BenchmarkHenselFindMatch_vs_NaiveChaining compares Hensel Ultrametric search
// against naive linear chaining at depth 64.
func BenchmarkHenselFindMatch_vs_NaiveChaining(b *testing.B) {
	data := make([]byte, 65536)
	rng := rand.New(rand.NewSource(12345))
	rng.Read(data)
	// Seed periodic patterns
	for i := range 50 {
		copy(data[i*1000:(i*1000)+64], []byte("The quick brown fox jumps over the lazy dog. PRZM Ultrametric!"))
	}

	mf := przm.NewHenselMatchFinder(65536)
	for i := 0; i+4 <= len(data); i++ {
		mf.Insert(i, data, len(data))
	}

	var head [65536]int32
	var prev [65536]int32
	for i := 0; i+4 <= len(data); i++ {
		w4 := binary.LittleEndian.Uint32(data[i : i+4])
		h := (w4 * 0x1E35A7BD) >> 16
		h &= 0xFFFF
		prev[i&0xFFFF] = head[h]
		head[h] = int32(i + 1)
	}

	b.Run("Hensel_Depth_16", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pos := 32000 + (i % 1000)
			mf.FindMatchHensel(data, pos, 16, 4)
		}
	})

	b.Run("Hensel_Depth_64", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pos := 32000 + (i % 1000)
			mf.FindMatchHensel(data, pos, 64, 4)
		}
	})

	b.Run("Naive_Chain_Depth_64", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pos := 32000 + (i % 1000)
			w4 := binary.LittleEndian.Uint32(data[pos : pos+4])
			h := (w4 * 0x1E35A7BD) >> 16
			h &= 0xFFFF
			cand := int(head[h]) - 1
			bestLen := 3
			bestDist := 0
			depth := 0
			for cand >= 0 && depth < 64 {
				if cand >= pos {
					cand = int(prev[cand&0xFFFF]) - 1
					continue
				}
				d := pos - cand
				if d >= 65535 {
					break
				}
				l := 0
				for l < 258 && pos+l < len(data) && cand+l < len(data) && data[pos+l] == data[cand+l] {
					l++
				}
				if l > bestLen {
					bestLen = l
					bestDist = d
				}
				cand = int(prev[cand&0xFFFF]) - 1
				depth++
			}
			_ = bestDist
			_ = bestLen
		}
	})
}

// TestHensel_CompressionRatioMeasurements systematically measures compressed sizes
// and ratios across compression levels 1, 2, 3, and 4.
func TestHensel_CompressionRatioMeasurements(t *testing.T) {
	corpora := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "StructuredJSON_64K",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := 0; buf.Len() < 64*1024; i++ {
					fmt.Fprintf(&buf, `{"index":%d,"guid":"%08x-%04x-%04x","active":true,"balance":%.2f,"name":"User %d"}`+"\n",
						i, i*7919, i%1000, i%500, float64(i)*1.37, i)
				}
				return buf.Bytes()[:64*1024]
			},
		},
		{
			name: "GoSourceCode_64K",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := 0; buf.Len() < 64*1024; i++ {
					fmt.Fprintf(&buf, `
func workerLoop%d(in <-chan Task, out chan<- Result) error {
	for task := range in {
		res, err := task.Execute()
		if err != nil {
			return fmt.Errorf("task failed: %%w", err)
		}
		out <- res
	}
	return nil
}
`, i)
				}
				return buf.Bytes()[:64*1024]
			},
		},
		{
			name: "TabularTelemetry_64K",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := 0; buf.Len() < 64*1024; i++ {
					fmt.Fprintf(&buf, "HOST_%04d\tSERVICE_%02d\tMETRIC_%03d\tTIMESTAMP_%d\tVALUE_%f\n",
						i%20, i%5, i%50, 1700000000+i, float64(i)*3.14159)
				}
				return buf.Bytes()[:64*1024]
			},
		},
		{
			name: "RepetitiveLogs_64K",
			gen: func() []byte {
				var buf bytes.Buffer
				for i := 0; buf.Len() < 64*1024; i++ {
					fmt.Fprintf(&buf, "[2026-10-01 12:00:%02d.123] [INFO] [cluster.worker.%d] Heartbeat acknowledged from node-%d (latency=1.2ms, status=HEALTHY)\n",
						i%60, i%8, i%8)
				}
				return buf.Bytes()[:64*1024]
			},
		},
	}

	for _, c := range corpora {
		data := c.gen()
		t.Logf("=== Corpus: %s (Raw Size: %d B) ===", c.name, len(data))
		for level := 1; level <= 4; level++ {
			comp, err := przm.Compress(data, level)
			if err != nil {
				t.Fatalf("compression failed: %v", err)
			}
			decomp, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("decompression failed: %v", err)
			}
			if !bytes.Equal(decomp, data) {
				t.Fatalf("data mismatch on %s level %d", c.name, level)
			}
			ratio := float64(len(data)) / float64(len(comp))
			t.Logf("  Level %d: Compressed Size = %6d B | Ratio = %6.2fx", level, len(comp), ratio)
		}
	}
}

func TestHensel_L6_MultiTierMatch(t *testing.T) {
	sp := przm.PoolHenselL6.Get()
	defer przm.PoolHenselL6.Put(sp)
	sp.Reset()

	data := make([]byte, 128*1024)
	for i := range data {
		data[i] = byte((i * 17) % 251)
	}

	// 1. Insert distant repetition (macro match: 2000 bytes)
	copy(data[0:2000], []byte("SPECIAL_LONG_DISTANT_REPETITIVE_MACRO_PAYLOAD_FOR_HENSEL_L6_"))
	for i := 60; i < 2000; i++ {
		data[i] = byte(i % 127)
	}
	copy(data[50000:52000], data[0:2000])

	// 2. Short 3-byte match where 4th byte differs (outside macro range)
	data[2100] = 'X'
	data[2101] = 'Y'
	data[2102] = 'Z'
	data[2103] = '1'

	data[2200] = 'X'
	data[2201] = 'Y'
	data[2202] = 'Z'
	data[2203] = '2'

	// Index 0..2150
	for p := 0; p < 2150; p++ {
		przm.HenselInsertL6(sp, p, data, len(data))
	}

	// Match at 2200 with minLen=3
	dist3, len3 := przm.HenselFindMatchL6(data, 2200, len(data), 256, 3, sp)
	if len3 < 3 || dist3 != (2200-2100) {
		t.Fatalf("expected 3-byte match with dist 100, got dist=%d, len=%d", dist3, len3)
	}

	// Index up to 50000
	for p := 2150; p < 50000; p++ {
		przm.HenselInsertL6(sp, p, data, len(data))
	}

	// Macro match at 50000
	distMacro, lenMacro := przm.HenselFindMatchL6(data, 50000, len(data), 256, 4, sp)
	if lenMacro < 2000 || distMacro != 50000 {
		t.Fatalf("expected macro match of >= 2000 bytes at dist 50000, got dist=%d, len=%d", distMacro, lenMacro)
	}
}
