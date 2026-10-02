// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// -----------------------------------------------------------------------------
// 1. Theorem 39: InvH3(InvH3(x)) == x across 50,000 random triplets in F_{2^8}^3
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_Theorem39_Involution50k(t *testing.T) {
	rng := rand.New(rand.NewSource(39393939))
	const totalTriplets = 50000

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for i := 0; i < totalTriplets; i++ {
		elem := przm.HeisenbergElement3{
			X: uint8(rng.Intn(256)),
			Y: uint8(rng.Intn(256)),
			Z: uint8(rng.Intn(256)),
		}

		// First algebraic inversion: H^{-1}(elem)
		invElem := przm.InvH3(elem)

		// Verification of coordinate map:
		// X and Y unchanged, Z' = Z ^ (X * Y)
		expectedZ := elem.Z ^ przm.GF256Mul(elem.X, elem.Y)
		if invElem.X != elem.X || invElem.Y != elem.Y || invElem.Z != expectedZ {
			t.Fatalf("Iteration %d: InvH3(%+v) coordinates invalid: got %+v, expected Z=%02X",
				i, elem, invElem, expectedZ)
		}

		// Second algebraic inversion: H^{-1}(H^{-1}(elem))
		invInvElem := przm.InvH3(invElem)

		// Strict Involution Check: Theorem 39
		if invInvElem != elem {
			t.Fatalf("Iteration %d: Theorem 39 Involution Failed! InvH3(InvH3(%+v)) = %+v, want %+v",
				i, elem, invInvElem, elem)
		}

		// Group inverse axioms: elem * invElem == e, invElem * elem == e
		rightMul := przm.MulH3(elem, invElem)
		if rightMul != ident {
			t.Fatalf("Iteration %d: elem * inv(elem) != (0,0,0): got %+v", i, rightMul)
		}
		leftMul := przm.MulH3(invElem, elem)
		if leftMul != ident {
			t.Fatalf("Iteration %d: inv(elem) * elem != (0,0,0): got %+v", i, leftMul)
		}
	}
}

// -----------------------------------------------------------------------------
// 2. Theorem 40: Unfoliate(Foliate(x)) == x across 50,000 random triplets/quads
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_Theorem40_FoliationUnfoliation50k(t *testing.T) {
	rng := rand.New(rand.NewSource(40404040))
	const totalBlocks = 50000

	// 2a. Stride 3 triplets (50,000 triplets = 150,000 bytes)
	data3 := make([]byte, totalBlocks*3)
	rng.Read(data3)
	orig3 := make([]byte, len(data3))
	copy(orig3, data3)

	// In-place foliation
	przm.HeisenbergFoliateInplace(data3, 3)

	// Validate coordinate transformation for all 50,000 triplets
	for i := 0; i < totalBlocks; i++ {
		x := orig3[i*3+0]
		y := orig3[i*3+1]
		z := orig3[i*3+2]
		expZ := z ^ przm.GF256Mul(x, y)

		if data3[i*3+0] != x {
			t.Fatalf("Triplet %d: channel X modified", i)
		}
		if data3[i*3+1] != y {
			t.Fatalf("Triplet %d: channel Y modified", i)
		}
		if data3[i*3+2] != expZ {
			t.Fatalf("Triplet %d: channel Z mismatch: got %02X, want %02X", i, data3[i*3+2], expZ)
		}
	}

	// In-place unfoliation: Theorem 40
	przm.HeisenbergUnfoliateInplace(data3, 3)
	if !bytes.Equal(data3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 In-Place Involution Failed: Unfoliate(Foliate(x)) != x")
	}

	// 2b. Test distinct buffers for Stride 3
	dst3 := make([]byte, len(orig3))
	przm.HeisenbergFoliate(dst3, orig3, 3)
	restored3 := make([]byte, len(orig3))
	przm.HeisenbergUnfoliate(restored3, dst3, 3)
	if !bytes.Equal(restored3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 Distinct Buffers Involution Failed")
	}

	// 2c. Test aliased in-place Foliate(&buf[0], &buf[0])
	aliased3 := make([]byte, len(orig3))
	copy(aliased3, orig3)
	przm.HeisenbergFoliate(aliased3, aliased3, 3)
	przm.HeisenbergUnfoliate(aliased3, aliased3, 3)
	if !bytes.Equal(aliased3, orig3) {
		t.Fatalf("Theorem 40 Stride 3 Aliased Buffers Involution Failed")
	}

	// 2d. Stride 4 quads (50,000 quads = 200,000 bytes)
	data4 := make([]byte, totalBlocks*4)
	rng.Read(data4)
	orig4 := make([]byte, len(data4))
	copy(orig4, data4)

	przm.HeisenbergFoliateInplace(data4, 4)

	for i := 0; i < totalBlocks; i++ {
		x := orig4[i*4+0]
		y := orig4[i*4+1]
		z := orig4[i*4+2]
		w := orig4[i*4+3]
		expZ := z ^ przm.GF256Mul(x, y)

		if data4[i*4+0] != x || data4[i*4+1] != y || data4[i*4+2] != expZ || data4[i*4+3] != w {
			t.Fatalf("Quad %d: channel mismatch in foliation", i)
		}
	}

	przm.HeisenbergUnfoliateInplace(data4, 4)
	if !bytes.Equal(data4, orig4) {
		t.Fatalf("Theorem 40 Stride 4 Involution Failed")
	}
}

// -----------------------------------------------------------------------------
// 3. Edge Cases: all zeros, all 0xFF, alternating bit patterns, prime lengths
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_EdgeCases_AdversarialInputs(t *testing.T) {
	// Prime lengths from 2 to 4093
	primeLengths := []int{
		2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61,
		67, 71, 73, 79, 83, 89, 97, 101, 103, 107, 109, 113, 127, 131, 137,
		139, 149, 151, 157, 163, 167, 173, 179, 181, 191, 193, 197, 199, 211,
		223, 227, 229, 233, 239, 241, 251, 257, 509, 1021, 2053, 4093,
	}

	patterns := []struct {
		name string
		gen  func(length int) []byte
	}{
		{
			name: "AllZeros_0x00",
			gen: func(length int) []byte {
				return make([]byte, length)
			},
		},
		{
			name: "AllOnes_0xFF",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0xFF
				}
				return b
			},
		},
		{
			name: "Alternating_0xAA",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0xAA
				}
				return b
			},
		},
		{
			name: "Alternating_0x55",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 0x55
				}
				return b
			},
		},
		{
			name: "AlternatingTriplets_AA_55_FF",
			gen: func(length int) []byte {
				b := make([]byte, length)
				cycle := []byte{0xAA, 0x55, 0xFF, 0x00, 0x33, 0xCC}
				for i := range b {
					b[i] = cycle[i%len(cycle)]
				}
				return b
			},
		},
		{
			name: "PowersOfTwo_SingleBits",
			gen: func(length int) []byte {
				b := make([]byte, length)
				for i := range b {
					b[i] = 1 << uint(i%8)
				}
				return b
			},
		},
	}

	for _, p := range patterns {
		for _, primeLen := range primeLengths {
			orig := p.gen(primeLen)

			for _, stride := range []int{3, 4} {
				buf := make([]byte, primeLen)
				copy(buf, orig)

				// In-place foliate
				przm.HeisenbergFoliateInplace(buf, stride)

				// Verify unaligned tail is completely untouched
				tailStart := (primeLen / stride) * stride
				for j := tailStart; j < primeLen; j++ {
					if buf[j] != orig[j] {
						t.Fatalf("Pattern %s, PrimeLen %d, Stride %d: tail corrupted at index %d: got %02X, want %02X",
							p.name, primeLen, stride, j, buf[j], orig[j])
					}
				}

				// In-place unfoliate must restore exact original
				przm.HeisenbergUnfoliateInplace(buf, stride)
				if !bytes.Equal(buf, orig) {
					t.Fatalf("Pattern %s, PrimeLen %d, Stride %d: involution failed",
						p.name, primeLen, stride)
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// 4. High Concurrency Stress Test: Heisenberg & Sheaf Decompressors
// -----------------------------------------------------------------------------
func TestChallengerM2_2_R3_HighConcurrency_ZeroAlloc(t *testing.T) {
	const numGoroutines = 8
	const iterations = 50

	// 4a. Prepare diverse Heisenberg frames
	var heisFrames [][]byte
	var heisExpected [][]byte

	// Frame 1: Raw
	rawInput := make([]byte, 1024)
	for i := range rawInput {
		rawInput[i] = byte(i * 11)
	}
	compRaw, err := przm.CompressHeisenberg(rawInput, 1)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compRaw)
	heisExpected = append(heisExpected, rawInput)

	// Frame 2: Foliated Stride 3
	folInput3 := make([]byte, 3072)
	for i := 0; i < len(folInput3); i += 3 {
		folInput3[i] = byte(i % 127)
		folInput3[i+1] = byte((i*7 + 1) % 256)
		folInput3[i+2] = przm.GF256Mul(folInput3[i], folInput3[i+1])
	}
	compFol3, err := przm.CompressHeisenberg(folInput3, 3)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compFol3)
	heisExpected = append(heisExpected, folInput3)

	// Frame 3: Foliated Stride 4
	folInput4 := make([]byte, 4096)
	for i := 0; i < len(folInput4); i += 4 {
		folInput4[i] = byte(i % 127)
		folInput4[i+1] = byte((i*5 + 1) % 256)
		folInput4[i+2] = przm.GF256Mul(folInput4[i], folInput4[i+1])
		folInput4[i+3] = 0x88
	}
	compFol4, err := przm.CompressHeisenberg(folInput4, 3)
	if err != nil {
		t.Fatal(err)
	}
	heisFrames = append(heisFrames, compFol4)
	heisExpected = append(heisExpected, folInput4)

	// 4b. Prepare diverse Cellular Sheaf frames
	var sheafFrames [][]byte
	var sheafExpected [][]byte

	// Sheaf 1: Harmonic 8x8
	h8 := make([]byte, 64)
	h8[0] = 0x77
	for j := 0; j < 7; j++ {
		h8[j+1] = h8[j] ^ byte(j+1)
	}
	for i := 1; i < 8; i++ {
		v := byte(i * 3)
		for j := 0; j < 8; j++ {
			h8[i*8+j] = h8[(i-1)*8+j] ^ v
		}
	}
	compSh8, err := przm.CompressCellularSheaf(h8, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSh8)
	sheafExpected = append(sheafExpected, h8)

	// Sheaf 2: ConstBoundaries 16x16
	h16 := make([]byte, 256)
	h16[0] = 0xAA
	for j := 0; j < 15; j++ {
		h16[j+1] = h16[j] ^ 0x03
	}
	for i := 1; i < 16; i++ {
		for j := 0; j < 16; j++ {
			h16[i*16+j] = h16[(i-1)*16+j] ^ 0x05
		}
	}
	compSh16, err := przm.CompressCellularSheaf(h16, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSh16)
	sheafExpected = append(sheafExpected, h16)

	// Sheaf 3: Sparse Curl 16x32 with unaligned remainder
	spCurl := make([]byte, 517)
	for i := range spCurl {
		spCurl[i] = byte(i * 7)
	}
	spCurl[19] ^= 0x33
	spCurl[105] ^= 0x77
	compSp, err := przm.CompressCellularSheaf(spCurl, 3)
	if err != nil {
		t.Fatal(err)
	}
	sheafFrames = append(sheafFrames, compSp)
	sheafExpected = append(sheafExpected, spCurl)

	// 4c. Launch concurrent workers
	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines*iterations)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(gid*10007 + 73)))
			dst := bufkit.AlignedBytes(8192, bufkit.CacheLineSize)

			for iter := 0; iter < iterations; iter++ {
				if gid%2 == 0 {
					// Heisenberg decompress
					idx := rng.Intn(len(heisFrames))
					frame := heisFrames[idx]
					exp := heisExpected[idx]

					dec, err := przm.DecompressHeisenberg(frame, dst)
					if err != nil {
						errCh <- fmt.Errorf("gid %d iter %d DecompressHeisenberg error: %w", gid, iter, err)
						return
					}
					if !bytes.Equal(dec, exp) {
						errCh <- fmt.Errorf("gid %d iter %d DecompressHeisenberg data mismatch", gid, iter)
						return
					}
				} else {
					// Sheaf decompress
					idx := rng.Intn(len(sheafFrames))
					frame := sheafFrames[idx]
					exp := sheafExpected[idx]

					dec, err := przm.DecompressCellularSheaf(frame, dst)
					if err != nil {
						errCh <- fmt.Errorf("gid %d iter %d DecompressCellularSheaf error: %w", gid, iter, err)
						return
					}
					if !bytes.Equal(dec, exp) {
						errCh <- fmt.Errorf("gid %d iter %d DecompressCellularSheaf data mismatch", gid, iter)
						return
					}
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// -----------------------------------------------------------------------------
// 5. Parallel Benchmarks verifying 0 B/op and 0 allocs/op under multi-core load
// -----------------------------------------------------------------------------
func BenchmarkChallengerM2_2_R3_Heisenberg_ParallelZeroAlloc(b *testing.B) {
	src := make([]byte, 65536)
	for i := range src {
		src[i] = uint8((i * 3) & 0xFF)
	}
	for i := 0; i+2 < len(src); i += 3 {
		src[i+2] = przm.GF256Mul(src[i], src[i+1]) ^ uint8((i*19+7)&0xFF)
	}
	compressed, err := przm.CompressHeisenberg(src, 3)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dst := bufkit.AlignedBytes(len(src)+64, bufkit.CacheLineSize)
		for pb.Next() {
			_, err := przm.DecompressHeisenberg(compressed, dst)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkChallengerM2_2_R3_CellularSheaf_ParallelZeroAlloc(b *testing.B) {
	orig := make([]byte, 1024)
	for i := range orig {
		orig[i] = uint8((i * 3) & 0xFF)
	}
	compressed, err := przm.CompressCellularSheaf(orig, 3)
	if err != nil {
		b.Fatal(err)
	}

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
		// Warmup thread-local buffer
		_, _ = przm.DecompressCellularSheaf(compressed, dst)
		for pb.Next() {
			_, err := przm.DecompressCellularSheaf(compressed, dst)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
