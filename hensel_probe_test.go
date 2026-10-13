// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"math/rand"
	"testing"
	"unsafe"
)

func TestVectorProbe8_ExactAndValid(t *testing.T) {
	var bucket VectorBucket8
	windowSize := int32(65536)
	curPos := int32(1000)
	targetFP := uint32(0xDEADBEEF)

	// Setup 8 candidates:
	// slot 0: dist = 10 (valid, exact FP)
	bucket.Pos[0] = curPos - 10
	bucket.FP32[0] = targetFP

	// slot 1: dist = 200 (valid, different FP)
	bucket.Pos[1] = curPos - 200
	bucket.FP32[1] = 0x12345678

	// slot 2: dist = -5 (invalid future candidate)
	bucket.Pos[2] = curPos + 5
	bucket.FP32[2] = targetFP

	// slot 3: dist = 70000 (invalid beyond window)
	bucket.Pos[3] = curPos - 70000
	bucket.FP32[3] = targetFP

	// slot 4: dist = 50 (valid, exact FP)
	bucket.Pos[4] = curPos - 50
	bucket.FP32[4] = targetFP

	// slot 5: empty (pos = 0 -> dist = 1000, but valid)
	bucket.Pos[5] = 0
	bucket.FP32[5] = 0

	// slot 6: dist = 300 (valid, low 16 bits match: 0x9999BEEF)
	bucket.Pos[6] = curPos - 300
	bucket.FP32[6] = 0x9999BEEF

	// slot 7: dist = 500 (valid, different FP)
	bucket.Pos[7] = curPos - 500
	bucket.FP32[7] = 0xAAAA5555

	var res VectorProbeResult
	vectorProbe8_AVX2(
		unsafe.Pointer(&bucket.Pos[0]),
		unsafe.Pointer(&bucket.FP32[0]),
		curPos,
		targetFP,
		windowSize,
		&res,
	)

	// Expected valid: slot 0, 1, 4, 5 (pos=0 -> d=1000 < window), 6, 7.
	// Slot 2 (future) and 3 (too far) should NOT be valid.
	if (res.ValidMask & (1 << 0)) == 0 {
		t.Errorf("expected slot 0 to be valid")
	}
	if (res.ValidMask & (1 << 1)) == 0 {
		t.Errorf("expected slot 1 to be valid")
	}
	if (res.ValidMask & (1 << 2)) != 0 {
		t.Errorf("expected slot 2 (future) to be invalid")
	}
	if (res.ValidMask & (1 << 3)) != 0 {
		t.Errorf("expected slot 3 (too far) to be invalid")
	}
	if (res.ValidMask & (1 << 4)) == 0 {
		t.Errorf("expected slot 4 to be valid")
	}

	// Expected exact: slot 0 and slot 4
	if (res.ExactMask & (1 << 0)) == 0 {
		t.Errorf("expected slot 0 to be exact match")
	}
	if (res.ExactMask & (1 << 4)) == 0 {
		t.Errorf("expected slot 4 to be exact match")
	}
	if (res.ExactMask & (1 << 1)) != 0 {
		t.Errorf("slot 1 should not be exact match")
	}
	if (res.ExactMask & (1 << 2)) != 0 {
		t.Errorf("slot 2 should not be exact match (invalid dist)")
	}

	// Expected part 16: slot 0, slot 4, and slot 6 (low 16 bits match 0xBEEF)
	if (res.Part16Mask & (1 << 6)) == 0 {
		t.Errorf("expected slot 6 to match on low 16 bits")
	}
}

func TestVectorProbe16_ExactAndValid(t *testing.T) {
	var bucket VectorBucket16
	windowSize := int32(65536)
	curPos := int32(2000)
	targetFP := uint32(0xCAFEBABE)

	for i := 0; i < 16; i++ {
		bucket.Pos[i] = curPos - int32(i*100+10)
		bucket.FP32[i] = 0x11111111 * uint32(i)
	}

	// Set exact matches on slots 3 and 11
	bucket.FP32[3] = targetFP
	bucket.FP32[11] = targetFP

	// Invalidate slot 7
	bucket.Pos[7] = curPos + 50

	var res VectorProbeResult16
	vectorProbe16_AVX2(
		unsafe.Pointer(&bucket.Pos[0]),
		unsafe.Pointer(&bucket.FP32[0]),
		curPos,
		targetFP,
		windowSize,
		&res,
	)

	if (res.ExactMask & (1 << 3)) == 0 {
		t.Errorf("expected slot 3 to be exact match")
	}
	if (res.ExactMask & (1 << 11)) == 0 {
		t.Errorf("expected slot 11 to be exact match")
	}
	if (res.ValidMask & (1 << 7)) != 0 {
		t.Errorf("slot 7 should be invalid")
	}
}

func TestHenselMultiWayTable16_EndToEnd(t *testing.T) {
	vt := new(HenselMultiWayTable16)
	vt.Reset()

	// Create test source data with repeated patterns
	src := make([]byte, 100000)
	rng := rand.New(rand.NewSource(42))
	rng.Read(src)

	// Stamp a repeating 32-byte pattern at pos 1000, 5000, 20000
	pattern := []byte("SIMD_AVX2_MULTI_WAY_HASH_PROBING")
	copy(src[1000:], pattern)
	copy(src[5000:], pattern)
	copy(src[20000:], pattern)

	// Insert positions into table
	for p := 0; p <= 10000; p++ {
		w4 := uint32(src[p]) | (uint32(src[p+1]) << 8) | (uint32(src[p+2]) << 16) | (uint32(src[p+3]) << 24)
		h := int(hash4(w4)>>(32-VectorHashBits)) & VectorHashMask
		var targetFP uint32
		if p+8 <= len(src) {
			targetFP = *(*uint32)(unsafe.Pointer(&src[p+4]))
		}
		vt.Insert(h, p, targetFP)
	}

	// Query at pos 20000
	bestDist, bestLen := FindMatchSIMD16(src, 20000, len(src), 65536, 4, vt)
	if bestDist == 0 || bestLen < len(pattern) {
		t.Fatalf("FindMatchSIMD16 failed: bestDist=%d, bestLen=%d, expected >= %d", bestDist, bestLen, len(pattern))
	}

	// Verify match contents match pattern
	candPos := 20000 - bestDist
	if !bytes.Equal(src[candPos:candPos+len(pattern)], pattern) {
		t.Fatalf("matched data does not match pattern: got %q", src[candPos:candPos+len(pattern)])
	}
}

func BenchmarkFindMatch_ScalarVsSIMD(b *testing.B) {
	src := make([]byte, 1024*1024)
	rng := rand.New(rand.NewSource(1337))
	rng.Read(src)

	// Inject periodic matches
	for i := 0; i < len(src)-64; i += 256 {
		copy(src[i:i+32], []byte("WARP_CHAIN_SIMD_AVX2_BENCH_TEST!"))
	}

	sp := new(HenselScratchpad)
	sp.Reset()
	vt := new(HenselMultiWayTable16)
	vt.Reset()

	for p := 0; p < len(src)/2; p++ {
		HenselInsert(sp, p, src, len(src))
		w4 := *(*uint32)(unsafe.Pointer(&src[p]))
		h := int(hash4(w4)>>(32-VectorHashBits)) & VectorHashMask
		var targetFP uint32
		if p+8 <= len(src) {
			targetFP = *(*uint32)(unsafe.Pointer(&src[p+4]))
		}
		vt.Insert(h, p, targetFP)
	}

	b.Run("Scalar_HenselFindMatch", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pos := (len(src)/2 + (i % 10000))
			HenselFindMatch(src, pos, len(src), 16, 4, sp)
		}
	})

	b.Run("SIMD_FindMatchSIMD16", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pos := (len(src)/2 + (i % 10000))
			FindMatchSIMD16(src, pos, len(src), 65536, 4, vt)
		}
	})
}

func TestVectorProbe32_ExactAndValid(t *testing.T) {
	var bucket VectorBucket32
	windowSize := int32(65536)
	curPos := int32(5000)
	targetFP := uint32(0xCAFED00D)

	for i := 0; i < 32; i++ {
		bucket.Pos[i] = curPos - int32(i*100+10)
		bucket.FP32[i] = 0x22222222 * uint32(i)
	}

	// Exact matches on slot 5 and slot 27
	bucket.FP32[5] = targetFP
	bucket.FP32[27] = targetFP

	// Invalidate slot 15
	bucket.Pos[15] = curPos + 100

	var res VectorProbeResult32
	vectorProbe32_AVX2(
		unsafe.Pointer(&bucket.Pos[0]),
		unsafe.Pointer(&bucket.FP32[0]),
		curPos,
		targetFP,
		windowSize,
		&res,
	)

	if (res.ExactMask & (1 << 5)) == 0 {
		t.Errorf("expected slot 5 to be exact match")
	}
	if (res.ExactMask & (1 << 27)) == 0 {
		t.Errorf("expected slot 27 to be exact match")
	}
	if (res.ValidMask & (1 << 15)) != 0 {
		t.Errorf("slot 15 should be invalid")
	}
}

func BenchmarkFindMatchSIMD_MultiDepth(b *testing.B) {
	src := make([]byte, 1024*1024)
	rng := rand.New(rand.NewSource(999))
	rng.Read(src)

	// Inject periodic patterns
	for i := 0; i < len(src)-64; i += 256 {
		copy(src[i:i+32], []byte("WARP_CHAIN_SIMD_AVX2_MULTI_WAY!!"))
	}

	vt16 := new(HenselMultiWayTable16)
	vt16.Reset()
	vt32 := new(HenselMultiWayTable32)
	vt32.Reset()

	for p := 0; p < len(src)/2; p++ {
		w4 := *(*uint32)(unsafe.Pointer(&src[p]))
		var targetFP uint32
		if p+8 <= len(src) {
			targetFP = *(*uint32)(unsafe.Pointer(&src[p+4]))
		}
		h16 := int(hash4(w4)>>(32-VectorHashBits)) & VectorHashMask
		vt16.Insert(h16, p, targetFP)

		h32 := int(w4*0x1E35A7BD>>17) & VectorTable32Mask
		vt32.Insert(h32, p, targetFP)
		hSec := int(w4*0x7FEB352D>>17) & VectorTable32Mask
		vt32.Insert(hSec, p, targetFP)
	}

	b.Run("SIMD16_Ways", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pos := (len(src)/2 + (i % 10000))
			FindMatchSIMD16(src, pos, len(src), 65536, 4, vt16)
		}
	})

	b.Run("SIMD64_Candidates", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pos := (len(src)/2 + (i % 10000))
			FindMatchSIMD64(src, pos, len(src), 65536, 4, vt32)
		}
	})

	b.Run("SIMD256_Candidates", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pos := (len(src)/2 + (i % 10000))
			FindMatchSIMD256(src, pos, len(src), 65536, 4, vt32)
		}
	})
}
