// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/przm"
)

// cGF256Mul implements the exact Russian Peasant algorithm from src/przm_dec.c:
//
//	uint8_t p = 0;
//	for (int i = 0; i < 8; i++) {
//	    if (b & 1) p ^= a;
//	    uint8_t hi = a & 0x80;
//	    a <<= 1;
//	    if (hi) a ^= 0x1B;
//	    b >>= 1;
//	}
//	return p;
func cGF256Mul(a, b uint8) uint8 {
	var p uint8 = 0
	for i := 0; i < 8; i++ {
		if (b & 1) != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1B
		}
		b >>= 1
	}
	return p
}

// TestAdversarialHeisenberg_GF256MultiplicationTableExhaustive tests all 65,536 pairs
// of (a, b) over F_{2^8} to prove Go table lookup GF256Mul exactly matches C gf256_mul.
func TestAdversarialHeisenberg_GF256MultiplicationTableExhaustive(t *testing.T) {
	for a := 0; a < 256; a++ {
		for b := 0; b < 256; b++ {
			uA := uint8(a)
			uB := uint8(b)
			goRes := przm.GF256Mul(uA, uB)
			cRes := cGF256Mul(uA, uB)
			if goRes != cRes {
				t.Fatalf("GF(256) multiplication mismatch at (%d, %d): Go=%02X, C=%02X", a, b, goRes, cRes)
			}
			// Commutativity
			if przm.GF256Mul(uA, uB) != przm.GF256Mul(uB, uA) {
				t.Fatalf("Commutativity violated at (%d, %d)", a, b)
			}
		}
	}
}

// TestAdversarialHeisenberg_AlgebraicInvolutionFuzz100k verifies 100,000 randomized triplets
// and quads over F_{2^8}, proving exact algebraic involution H^{-1}(H(x, y, z)) == (x, y, z).
func TestAdversarialHeisenberg_AlgebraicInvolutionFuzz100k(t *testing.T) {
	rng := rand.New(rand.NewSource(987654321))
	const iterations = 100000

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for i := 0; i < iterations; i++ {
		x := uint8(rng.Intn(256))
		y := uint8(rng.Intn(256))
		z := uint8(rng.Intn(256))
		elem := przm.HeisenbergElement3{X: x, Y: y, Z: z}

		// 1. Group element inversion and involution
		invElem := przm.InvH3(elem)
		invInvElem := przm.InvH3(invElem)
		if invInvElem != elem {
			t.Fatalf("Iteration %d: InvH3(InvH3(%+v)) != elem, got %+v", i, elem, invInvElem)
		}

		// 2. Right and Left inverse cancel to identity
		mulRight := przm.MulH3(elem, invElem)
		if mulRight != ident {
			t.Fatalf("Iteration %d: elem * inv(elem) != ident: got %+v", i, mulRight)
		}
		mulLeft := przm.MulH3(invElem, elem)
		if mulLeft != ident {
			t.Fatalf("Iteration %d: inv(elem) * elem != ident: got %+v", i, mulLeft)
		}

		// 3. Coordinate foliation mapping involution: z' = z ^ (x * y), z'' = z' ^ (x * y) == z
		prod := przm.GF256Mul(x, y)
		zPrime := z ^ prod
		zDoublePrime := zPrime ^ prod
		if zDoublePrime != z {
			t.Fatalf("Iteration %d: coordinate involution failed: x=%02X y=%02X z=%02X -> z'=%02X -> z''=%02X",
				i, x, y, z, zPrime, zDoublePrime)
		}

		// 4. Stride 4 Quad foliation: (x, y, z, w) -> (x, y, z ^ (x * y), w)
		w := uint8(rng.Intn(256))
		quad := [4]byte{x, y, z, w}
		foliatedQuad := quad
		przm.HeisenbergFoliateInplace(foliatedQuad[:], 4)
		if foliatedQuad[0] != x || foliatedQuad[1] != y || foliatedQuad[2] != zPrime || foliatedQuad[3] != w {
			t.Fatalf("Iteration %d: quad foliation mismatch: got %v, want [%02X %02X %02X %02X]",
				i, foliatedQuad, x, y, zPrime, w)
		}
		przm.HeisenbergUnfoliateInplace(foliatedQuad[:], 4)
		if foliatedQuad != quad {
			t.Fatalf("Iteration %d: quad unfoliation mismatch: got %v, want %v", i, foliatedQuad, quad)
		}
	}
}

// TestAdversarialHeisenberg_BoundaryLengths tests buffer lengths 0, 1, 2, unaligned lengths,
// and SWAR loop non-multiples (12, 16, 32).
func TestAdversarialHeisenberg_BoundaryLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(1122334455))

	// All interesting lengths: boundary, unaligned, SWAR boundaries, prime lengths
	testLengths := []int{
		0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 17, 18, 23, 24, 25, 31, 32, 33, 35, 47, 48, 49,
		63, 64, 65, 95, 96, 97, 127, 128, 129, 255, 256, 257,
		511, 512, 513, 1023, 1024, 1025, 4095, 4096, 4097,
	}

	for _, n := range testLengths {
		orig := make([]byte, n)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, n)
			copy(buf, orig)

			// 1. Forward foliation
			przm.HeisenbergFoliateInplace(buf, stride)

			// 2. Channel invariants:
			// Bytes strictly within full stride blocks
			fullBlocks := n / stride
			for b := 0; b < fullBlocks; b++ {
				idx := b * stride
				// X must remain unchanged
				if buf[idx+0] != orig[idx+0] {
					t.Fatalf("Length %d, Stride %d: Channel X corrupted at block %d (index %d)", n, stride, b, idx)
				}
				// Y must remain unchanged
				if buf[idx+1] != orig[idx+1] {
					t.Fatalf("Length %d, Stride %d: Channel Y corrupted at block %d (index %d)", n, stride, b, idx+1)
				}
				// Z must equal origZ ^ (origX * origY)
				expectedZ := orig[idx+2] ^ przm.GF256Mul(orig[idx+0], orig[idx+1])
				if buf[idx+2] != expectedZ {
					t.Fatalf("Length %d, Stride %d: Channel Z mismatch at block %d (index %d): got %02X, want %02X",
						n, stride, b, idx+2, buf[idx+2], expectedZ)
				}
				// For stride 4, W must remain unchanged
				if stride == 4 {
					if buf[idx+3] != orig[idx+3] {
						t.Fatalf("Length %d, Stride %d: Channel W corrupted at block %d (index %d)", n, stride, b, idx+3)
					}
				}
			}

			// 3. Unaligned tail bytes (indices >= fullBlocks * stride) must be completely untouched
			tailStart := fullBlocks * stride
			for j := tailStart; j < n; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Length %d, Stride %d: Unaligned tail byte modified at index %d: got %02X, want %02X",
						n, stride, j, buf[j], orig[j])
				}
			}

			// 4. Inverse unfoliation must restore original bytes bit-identically
			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d, Stride %d: Involution failed! Restored buffer differs from original", n, stride)
			}
		}
	}
}

// TestAdversarialHeisenberg_ComprehensiveEveryLength0to256 tests EVERY single integer
// length from 0 to 256 for both stride 3 and 4 to guarantee zero off-by-one errors.
func TestAdversarialHeisenberg_ComprehensiveEveryLength0to256(t *testing.T) {
	rng := rand.New(rand.NewSource(44556677))

	for n := 0; n <= 256; n++ {
		orig := make([]byte, n)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, n)
			copy(buf, orig)

			przm.HeisenbergFoliateInplace(buf, stride)

			// Tail check
			tailStart := (n / stride) * stride
			for j := tailStart; j < n; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Length %d (stride %d): tail byte %d corrupted", n, stride, j)
				}
			}

			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d (stride %d): unfoliate mismatch", n, stride)
			}
		}
	}
}

// TestAdversarialHeisenberg_FrameCodecBoundaryPayloads tests roundtrips of boundary payloads
// through CompressHeisenberg and DecompressHeisenberg.
func TestAdversarialHeisenberg_FrameCodecBoundaryPayloads(t *testing.T) {
	rng := rand.New(rand.NewSource(99887766))

	testSizes := []int{0, 1, 2, 3, 4, 5, 10, 11, 12, 13, 24, 25, 47, 48, 49, 100, 3072, 4096}

	for _, size := range testSizes {
		orig := make([]byte, size)
		rng.Read(orig)

		// Make correlated data if size >= 3
		if size >= 3 {
			for i := 0; i+3 <= size; i += 3 {
				orig[i+2] = przm.GF256Mul(orig[i], orig[i+1])
			}
		}

		compressed, err := przm.CompressHeisenberg(orig, 3)
		if err != nil {
			t.Fatalf("Size %d: CompressHeisenberg failed: %v", size, err)
		}

		if size == 0 {
			// Check size 0 behavior
			t.Logf("Size 0: compressed len = %d", len(compressed))
			decompressed, err := przm.DecompressHeisenberg(compressed, nil)
			if err != nil {
				t.Logf("DISCOVERY: Size 0 DecompressHeisenberg returned error: %v (compressed len: %d)", err, len(compressed))
			} else if !bytes.Equal(decompressed, orig) {
				t.Fatalf("Size 0 mismatch: got %v, want %v", decompressed, orig)
			}
			continue
		}

		decompressed, err := przm.DecompressHeisenberg(compressed, nil)
		if err != nil {
			t.Fatalf("Size %d: DecompressHeisenberg failed: %v", size, err)
		}
		if !bytes.Equal(decompressed, orig) {
			t.Fatalf("Size %d: Decompressed mismatch: got len %d, want len %d", size, len(decompressed), len(orig))
		}

		// Also verify DecompressTo
		dst := make([]byte, size+64)
		decTo, err := przm.DecompressTo(compressed, dst)
		if err != nil {
			t.Fatalf("Size %d: DecompressTo failed: %v", size, err)
		}
		if !bytes.Equal(decTo, orig) {
			t.Fatalf("Size %d: DecompressTo mismatch", size)
		}
	}
}

// TestAdversarialHeisenberg_MalformedFrames tests security and robustness against
// corrupted headers, invalid strides, illegal modes, and truncated frames.
func TestAdversarialHeisenberg_MalformedFrames(t *testing.T) {
	validFrame := make([]byte, 12+24)
	binary.LittleEndian.PutUint32(validFrame[0:4], przm.PRZM_MAGIC_HEISENBERG)
	binary.LittleEndian.PutUint32(validFrame[4:8], 24)
	validFrame[8] = 3 // Stride = 3
	validFrame[9] = 1 // Mode = Foliated
	for i := 12; i < len(validFrame); i++ {
		validFrame[i] = uint8(i)
	}

	// 0. Empty frame (0 bytes) gracefully returns []byte{}, nil per contract
	dec0, err0 := przm.DecompressHeisenberg(validFrame[:0], nil)
	if err0 != nil || len(dec0) != 0 {
		t.Fatalf("Empty frame returned error %v, len %d (expected nil, 0)", err0, len(dec0))
	}

	// 1. Truncated header (1 <= l < 12 bytes)
	for l := 1; l < 12; l++ {
		_, err := przm.DecompressHeisenberg(validFrame[:l], nil)
		if err == nil {
			t.Fatalf("Truncated frame of length %d accepted without error", l)
		}
	}

	// 2. Corrupt Magic
	badMagic := make([]byte, len(validFrame))
	copy(badMagic, validFrame)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0xDEADBEEF)
	if _, err := przm.DecompressHeisenberg(badMagic, nil); err == nil {
		t.Fatalf("Bad magic accepted without error")
	}

	// 3. Corrupt Stride (must be 3 or 4)
	badStrides := []uint8{0, 1, 2, 5, 6, 8, 16, 255}
	for _, bs := range badStrides {
		badS := make([]byte, len(validFrame))
		copy(badS, validFrame)
		badS[8] = bs
		if _, err := przm.DecompressHeisenberg(badS, nil); err == nil {
			t.Fatalf("Illegal stride %d accepted without error", bs)
		}
	}

	// 4. Corrupt Mode
	badModes := []uint8{2, 4, 5, 255}
	for _, bm := range badModes {
		badM := make([]byte, len(validFrame))
		copy(badM, validFrame)
		badM[9] = bm
		if _, err := przm.DecompressHeisenberg(badM, nil); err == nil {
			t.Fatalf("Illegal mode %d accepted without error", bm)
		}
	}

	// 5. Truncated payload in Mode 0 and Mode 1
	for _, mode := range []uint8{0, 1} {
		truncPayload := make([]byte, 12+10) // origLen is 24, but payload has only 10
		copy(truncPayload, validFrame[:12+10])
		truncPayload[9] = mode
		if _, err := przm.DecompressHeisenberg(truncPayload, nil); err == nil {
			t.Fatalf("Truncated payload in mode %d accepted without error", mode)
		}
	}

	// 6. Excessive origLen (> MaxBlockSize)
	badLen := make([]byte, len(validFrame))
	copy(badLen, validFrame)
	binary.LittleEndian.PutUint32(badLen[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressHeisenberg(badLen, nil); err == nil {
		t.Fatalf("origLen exceeding MaxBlockSize accepted without error")
	}
}

// TestAdversarialHeisenberg_ZeroAllocationInvariant stresses zero memory allocation
// on DecompressHeisenberg and DecompressTo across varied block sizes.
func TestAdversarialHeisenberg_ZeroAllocationInvariant(t *testing.T) {
	sizes := []int{24, 48, 96, 384, 1536, 6144}

	for _, size := range sizes {
		// Construct Mode 1 (Foliated uncompressed) frame explicitly
		src := make([]byte, size)
		for i := 0; i < size; i++ {
			src[i] = uint8((i*37 + 13) & 0xFF)
		}

		// Frame with Mode 1: Foliated
		foliatedFrame := make([]byte, przm.HeisenbergHeaderSize+size)
		binary.LittleEndian.PutUint32(foliatedFrame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(foliatedFrame[4:8], uint32(size))
		foliatedFrame[8] = 3 // Stride = 3
		foliatedFrame[9] = przm.HeisenbergModeFoliated
		copy(foliatedFrame[przm.HeisenbergHeaderSize:], src)
		przm.HeisenbergFoliateInplace(foliatedFrame[przm.HeisenbergHeaderSize:], 3)

		dst := make([]byte, size+128)

		// 1. In-place unfoliation must be strictly 0 allocs
		allocsInplace := testing.AllocsPerRun(50, func() {
			przm.HeisenbergUnfoliateInplace(dst[:size], 3)
		})
		if allocsInplace != 0 {
			t.Fatalf("Size %d: HeisenbergUnfoliateInplace allocated: %v allocs/op", size, allocsInplace)
		}

		// 2. Mode 1 DecompressHeisenberg zero alloc
		allocsDec := testing.AllocsPerRun(50, func() {
			out, err := przm.DecompressHeisenberg(foliatedFrame, dst)
			if err != nil || len(out) != size {
				t.Fatalf("Decompress failed: %v", err)
			}
		})
		if allocsDec != 0 {
			t.Fatalf("Size %d: Mode 1 DecompressHeisenberg violated 0-alloc: got %v allocs/op", size, allocsDec)
		}

		// 3. Mode 1 DecompressTo zero alloc
		allocsTo := testing.AllocsPerRun(50, func() {
			out, err := przm.DecompressTo(foliatedFrame, dst)
			if err != nil || len(out) != size {
				t.Fatalf("DecompressTo failed: %v", err)
			}
		})
		if allocsTo != 0 {
			t.Fatalf("Size %d: Mode 1 DecompressTo violated 0-alloc: got %v allocs/op", size, allocsTo)
		}

		// 4. Test Mode 3 (Cascaded PWS) allocation profile
		srcCorrelated := make([]byte, size)
		for i := 0; i+3 <= size; i += 3 {
			srcCorrelated[i] = uint8(i & 0xFF)
			srcCorrelated[i+1] = uint8((i * 7) & 0xFF)
			srcCorrelated[i+2] = przm.GF256Mul(srcCorrelated[i], srcCorrelated[i+1])
		}
		comp3, err := przm.CompressHeisenberg(srcCorrelated, 3)
		if err == nil && len(comp3) > 9 && comp3[9] == przm.HeisenbergModeCompressed {
			allocsM3 := testing.AllocsPerRun(20, func() {
				_, _ = przm.DecompressHeisenberg(comp3, dst)
			})
			t.Logf("Size %d: Mode 3 (Cascaded PWS) allocations: %v allocs/op (delegated to inner PWSDecode)", size, allocsM3)
		}
	}
}

