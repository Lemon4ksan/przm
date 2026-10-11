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

// TestHeisenberg_AlgebraAxioms verifies the fundamental algebraic invariants of H_3(F_{2^8}).
func TestHeisenberg_AlgebraAxioms(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	ident := przm.HeisenbergElement3{X: 0, Y: 0, Z: 0}

	for range 10000 {
		a := przm.HeisenbergElement3{X: uint8(rng.Intn(256)), Y: uint8(rng.Intn(256)), Z: uint8(rng.Intn(256))}
		b := przm.HeisenbergElement3{X: uint8(rng.Intn(256)), Y: uint8(rng.Intn(256)), Z: uint8(rng.Intn(256))}
		c := przm.HeisenbergElement3{X: uint8(rng.Intn(256)), Y: uint8(rng.Intn(256)), Z: uint8(rng.Intn(256))}

		// 1. Identity Element: a * e == a, e * a == a
		if przm.MulH3(a, ident) != a {
			t.Fatalf("Right identity failed for %+v", a)
		}
		if przm.MulH3(ident, a) != a {
			t.Fatalf("Left identity failed for %+v", a)
		}

		// 2. Inverse Element: a * inv(a) == e, inv(a) * a == e
		invA := przm.InvH3(a)
		if przm.MulH3(a, invA) != ident {
			t.Fatalf("Right inverse failed for %+v, got %+v", a, przm.MulH3(a, invA))
		}
		if przm.MulH3(invA, a) != ident {
			t.Fatalf("Left inverse failed for %+v, got %+v", a, przm.MulH3(invA, a))
		}

		// 3. Exact Involution: inv(inv(a)) == a (Theorem 39)
		if przm.InvH3(invA) != a {
			t.Fatalf("Involution failed: inv(inv(%+v)) != a", a)
		}

		// 4. Associativity: (a * b) * c == a * (b * c)
		ab_c := przm.MulH3(przm.MulH3(a, b), c)
		a_bc := przm.MulH3(a, przm.MulH3(b, c))
		if ab_c != a_bc {
			t.Fatalf("Associativity failed: (%+v * %+v) * %+v = %+v, want %+v", a, b, c, ab_c, a_bc)
		}

		// 5. Commutator Centrality: [a, b] = (a * b) * inv(b * a) has X=0, Y=0 (Theorem 40)
		ab := przm.MulH3(a, b)
		ba := przm.MulH3(b, a)
		comm := przm.MulH3(ab, przm.InvH3(ba))
		if comm.X != 0 || comm.Y != 0 {
			t.Fatalf("Commutator not central: [a, b] = %+v, want X=0, Y=0", comm)
		}
	}
}

// TestHeisenberg_InvolutionProperty verifies that unfoliating twice restores identical bytes.
func TestHeisenberg_InvolutionProperty(t *testing.T) {
	sizes := []int{0, 1, 2, 3, 4, 5, 12, 16, 24, 64, 255, 1024, 4096}
	rng := rand.New(rand.NewSource(1337))

	for _, size := range sizes {
		orig := make([]byte, size)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, size)
			copy(buf, orig)

			// First foliation
			przm.HeisenbergFoliateInplace(buf, stride)

			if size >= stride && bytes.Equal(buf, orig) {
				// Verify it's not a trivial no-op unless all x*y was 0
				hasNonZero := false
				for i := 0; i+stride <= size; i += stride {
					if przm.GF256Mul(orig[i], orig[i+1]) != 0 {
						hasNonZero = true
						break
					}
				}
				if hasNonZero {
					t.Fatalf("Foliation stride %d was a no-op on size %d", stride, size)
				}
			}

			// Second foliation (unfoliation) must restore exact original bytes
			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Involution failed for size %d, stride %d", size, stride)
			}
		}
	}
}

// TestHeisenberg_Stride3_ChannelIntegrity verifies that channel 0 (x) and 1 (y) are strictly preserved.
func TestHeisenberg_Stride3_ChannelIntegrity(t *testing.T) {
	n := 300
	orig := make([]byte, n)
	rng := rand.New(rand.NewSource(999))
	rng.Read(orig)

	buf := make([]byte, n)
	copy(buf, orig)

	przm.HeisenbergFoliateInplace(buf, 3)

	for i := 0; i < n; i += 3 {
		if buf[i] != orig[i] {
			t.Fatalf("Channel X modified at index %d", i)
		}
		if buf[i+1] != orig[i+1] {
			t.Fatalf("Channel Y modified at index %d", i+1)
		}
		expectedZ := orig[i+2] ^ przm.GF256Mul(orig[i], orig[i+1])
		if buf[i+2] != expectedZ {
			t.Fatalf("Channel Z mismatch at index %d: got %x, want %x", i+2, buf[i+2], expectedZ)
		}
	}

	przm.HeisenbergUnfoliateInplace(buf, 3)
	if !bytes.Equal(buf, orig) {
		t.Fatalf("Roundtrip failed")
	}
}

// TestHeisenberg_Stride4_PassThrough verifies that channel 3 (alpha/w) is strictly untouched.
func TestHeisenberg_Stride4_PassThrough(t *testing.T) {
	n := 400
	orig := make([]byte, n)
	rng := rand.New(rand.NewSource(777))
	rng.Read(orig)

	buf := make([]byte, n)
	copy(buf, orig)

	przm.HeisenbergFoliateInplace(buf, 4)

	for i := 0; i < n; i += 4 {
		if buf[i] != orig[i] {
			t.Fatalf("Channel X modified at index %d", i)
		}
		if buf[i+1] != orig[i+1] {
			t.Fatalf("Channel Y modified at index %d", i+1)
		}
		expectedZ := orig[i+2] ^ przm.GF256Mul(orig[i], orig[i+1])
		if buf[i+2] != expectedZ {
			t.Fatalf("Channel Z mismatch at index %d: got %x, want %x", i+2, buf[i+2], expectedZ)
		}
		if buf[i+3] != orig[i+3] {
			t.Fatalf("Channel W pass-through violated at index %d", i+3)
		}
	}

	przm.HeisenbergUnfoliateInplace(buf, 4)
	if !bytes.Equal(buf, orig) {
		t.Fatalf("Roundtrip failed")
	}
}

// TestHeisenberg_BoundaryLengths tests arbitrary unaligned lengths.
func TestHeisenberg_BoundaryLengths(t *testing.T) {
	rng := rand.New(rand.NewSource(54321))
	unalignedSizes := []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 10, 11, 13, 14, 15, 17, 31, 33, 47, 49, 101, 103, 4097}

	for _, size := range unalignedSizes {
		orig := make([]byte, size)
		rng.Read(orig)

		for _, stride := range []int{3, 4} {
			buf := make([]byte, size)
			copy(buf, orig)

			przm.HeisenbergFoliateInplace(buf, stride)

			// Check tail bytes
			tailStart := (size / stride) * stride
			for j := tailStart; j < size; j++ {
				if buf[j] != orig[j] {
					t.Fatalf("Unaligned tail modified at byte %d (size %d, stride %d)", j, size, stride)
				}
			}

			przm.HeisenbergUnfoliateInplace(buf, stride)
			if !bytes.Equal(buf, orig) {
				t.Fatalf("Unaligned boundary roundtrip mismatch for size %d, stride %d", size, stride)
			}
		}
	}
}

// TestHeisenberg_ZeroAllocDecompression proves strictly 0 B/op and 0 allocs/op during decompression.
func TestHeisenberg_ZeroAllocDecompression(t *testing.T) {
	n := 4095
	src := make([]byte, n)
	for i := range src {
		src[i] = uint8((i * 3) & 0xFF)
	}
	for i := 0; i < n; i += 3 {
		src[i+2] = przm.GF256Mul(src[i], src[i+1]) ^ uint8((i*19+7)&0xFF)
	}

	compressed, err := przm.CompressHeisenberg(src, 3)
	if err != nil {
		t.Fatalf("CompressHeisenberg failed: %v", err)
	}

	dst := make([]byte, n+64)

	// Warmup
	dec, err := przm.DecompressHeisenberg(compressed, dst)
	if err != nil {
		t.Fatalf("Warmup DecompressHeisenberg failed: %v", err)
	}
	if !bytes.Equal(dec, src) {
		t.Fatalf("Warmup output mismatch")
	}

	// Verify DecompressTo
	decTo, err := przm.DecompressTo(compressed, dst)
	if err != nil {
		t.Fatalf("Warmup DecompressTo failed: %v", err)
	}
	if !bytes.Equal(decTo, src) {
		t.Fatalf("DecompressTo output mismatch")
	}

	// Measure heap allocations per decompress run
	allocs := testing.AllocsPerRun(100, func() {
		_, err := przm.DecompressHeisenberg(compressed, dst)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("Decompression invariant violated: expected 0 allocs/op, got %v", allocs)
	}

	allocsTo := testing.AllocsPerRun(100, func() {
		_, err := przm.DecompressTo(compressed, dst)
		if err != nil {
			t.Fatalf("DecompressTo failed: %v", err)
		}
	})

	if allocsTo != 0 {
		t.Fatalf("DecompressTo invariant violated: expected 0 allocs/op, got %v", allocsTo)
	}
}

// TestHeisenberg_CompressionRatioAdvantage verifies compression ratio gain on non-linearly coupled streams.
func TestHeisenberg_CompressionRatioAdvantage(t *testing.T) {
	numPixels := 4096
	src := make([]byte, numPixels*3)
	for i := range numPixels {
		x := uint8((i % 64) + 1)
		y := uint8(((i / 64) % 64) + 1)
		// Nonlinear interaction: z = x * y
		z := przm.GF256Mul(x, y)
		src[i*3+0] = x
		src[i*3+1] = y
		src[i*3+2] = z
	}

	compressed, err := przm.CompressHeisenberg(src, 3)
	if err != nil {
		t.Fatalf("CompressHeisenberg failed: %v", err)
	}

	ratio := float64(len(src)) / float64(len(compressed))
	t.Logf(
		"Heisenberg Multi-Channel 3D compression ratio: %.2fx (raw: %d, compressed: %d)",
		ratio,
		len(src),
		len(compressed),
	)

	if len(compressed) >= len(src) {
		t.Fatalf("Expected compression advantage on correlated 3D stream, got %d vs raw %d", len(compressed), len(src))
	}

	// Verify lossless reconstruction
	decompressed, err := przm.Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(decompressed, src) {
		t.Fatalf("Decompressed data mismatch: got %d bytes, want %d bytes", len(decompressed), len(src))
	}
}

// TestHeisenberg_EstimatorFidelity tests the covariance estimator on various classes of data.
func TestHeisenberg_EstimatorFidelity(t *testing.T) {
	// 1. Synthetic RGB non-linearly coupled stream -> should detect stride 3
	rgb := make([]byte, 3000)
	for i := 0; i < 3000; i += 3 {
		rgb[i] = uint8(i % 128)
		rgb[i+1] = uint8((i*5 + 1) % 256)
		rgb[i+2] = przm.GF256Mul(rgb[i], rgb[i+1])
	}
	s, ok := przm.EstimateOptimalHeisenberg(rgb)
	if !ok || s != 3 {
		t.Fatalf("Estimator failed to detect stride 3 on RGB stream: got stride=%d, ok=%v", s, ok)
	}

	// 2. Synthetic RGBA non-linearly coupled stream -> should detect stride 4
	rgba := make([]byte, 4000)
	for i := 0; i < 4000; i += 4 {
		rgba[i] = uint8(i % 128)
		rgba[i+1] = uint8((i*5 + 1) % 256)
		rgba[i+2] = przm.GF256Mul(rgba[i], rgba[i+1])
		rgba[i+3] = 0xFF
	}
	s, ok = przm.EstimateOptimalHeisenberg(rgba)
	if !ok || s != 4 {
		t.Fatalf("Estimator failed to detect stride 4 on RGBA stream: got stride=%d, ok=%v", s, ok)
	}

	// 3. Random noise -> should NOT apply
	noise := make([]byte, 4096)
	rng := rand.New(rand.NewSource(12345))
	rng.Read(noise)
	_, ok = przm.EstimateOptimalHeisenberg(noise)
	if ok {
		t.Fatalf("Estimator false positive on random noise")
	}

	// 4. All zeros -> should NOT apply
	zeros := make([]byte, 4096)
	_, ok = przm.EstimateOptimalHeisenberg(zeros)
	if ok {
		t.Fatalf("Estimator false positive on all zeros")
	}

	// 5. English text -> should NOT apply
	text := []byte(
		"The quick brown fox jumps over the lazy dog. Heisenberg groups model non-commutative nilpotent Lie manifolds.",
	)
	_, ok = przm.EstimateOptimalHeisenberg(text)
	if ok {
		t.Fatalf("Estimator false positive on text")
	}
}

func BenchmarkHeisenberg_ForwardFoliation(b *testing.B) {
	src := make([]byte, 65536)
	dst := make([]byte, 65536)
	copy(dst, src)

	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		przm.HeisenbergFoliate(dst, src, 3)
	}
}

func BenchmarkHeisenberg_InverseUnfoliationInplace(b *testing.B) {
	buf := make([]byte, 65536)

	b.SetBytes(int64(len(buf)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		przm.HeisenbergUnfoliateInplace(buf, 3)
	}
}

func BenchmarkHeisenberg_DecompressZeroAlloc(b *testing.B) {
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
	dst := make([]byte, len(src)+64)

	b.SetBytes(int64(len(src)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := przm.DecompressHeisenberg(compressed, dst)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ----------------------------------------------------------------------------
// Foliate / Unfoliate & Edge Cases Unit Tests
// ----------------------------------------------------------------------------

func TestHeisenberg_Foliate_DistinctBuffers(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 5, 11, 12, 13, 15, 16, 17, 24, 25, 100, 256, 1024}
	rng := rand.New(rand.NewSource(999))

	for _, length := range lengths {
		src := make([]byte, length)
		for i := range src {
			src[i] = uint8(rng.Intn(256))
		}

		for _, stride := range []int{3, 4} {
			dst := make([]byte, length)
			przm.HeisenbergFoliate(dst, src, stride)

			restored := make([]byte, length)
			przm.HeisenbergUnfoliate(restored, dst, stride)

			if !bytes.Equal(restored, src) {
				t.Fatalf("Length %d, Stride %d: Involution mismatch after Foliate -> Unfoliate", length, stride)
			}
		}
	}
}

func TestHeisenberg_Foliate_InplaceAliased(t *testing.T) {
	lengths := []int{0, 1, 2, 3, 4, 5, 12, 16, 24, 100, 256}
	rng := rand.New(rand.NewSource(1001))

	for _, length := range lengths {
		orig := make([]byte, length)
		for i := range orig {
			orig[i] = uint8(rng.Intn(256))
		}

		for _, stride := range []int{3, 4} {
			buf := make([]byte, length)
			copy(buf, orig)

			// Foliate aliased (&buf[0] == &buf[0])
			przm.HeisenbergFoliate(buf, buf, stride)

			// Unfoliate aliased
			przm.HeisenbergUnfoliate(buf, buf, stride)

			if !bytes.Equal(buf, orig) {
				t.Fatalf("Length %d, Stride %d: Aliased in-place involution failed", length, stride)
			}
		}
	}
}

func TestHeisenberg_Foliate_Equivalence(t *testing.T) {
	lengths := []int{0, 1, 3, 4, 12, 16, 25, 64, 256}
	rng := rand.New(rand.NewSource(1002))

	for _, length := range lengths {
		src := make([]byte, length)
		for i := range src {
			src[i] = uint8(rng.Intn(256))
		}

		for _, stride := range []int{3, 4} {
			dst1 := make([]byte, length)
			przm.HeisenbergFoliate(dst1, src, stride)

			dst2 := make([]byte, length)
			copy(dst2, src)
			przm.HeisenbergFoliateInplace(dst2, stride)

			if !bytes.Equal(dst1, dst2) {
				t.Fatalf("Length %d, Stride %d: HeisenbergFoliate != HeisenbergFoliateInplace", length, stride)
			}
		}
	}
}

func TestHeisenberg_Foliate_ZeroAlloc(t *testing.T) {
	src := make([]byte, 1024)
	dst := make([]byte, 1024)

	allocs := testing.AllocsPerRun(100, func() {
		przm.HeisenbergFoliate(dst, src, 3)
		przm.HeisenbergUnfoliate(dst, dst, 3)
	})

	if allocs > 0 {
		t.Fatalf("HeisenbergFoliate/Unfoliate allocated %v allocs/op, expected 0", allocs)
	}
}

func TestHeisenberg_Decompress_ZeroByteInput(t *testing.T) {
	dst := make([]byte, 64)

	// 1. Valid 0-byte frame with 12-byte header and origLen = 0
	frame0 := make([]byte, 12)
	binary.LittleEndian.PutUint32(frame0[0:4], przm.PRZM_MAGIC_HEISENBERG)
	binary.LittleEndian.PutUint32(frame0[4:8], 0) // origLen = 0
	frame0[8] = 3                                 // stride = 3
	frame0[9] = przm.HeisenbergModeRaw

	dec, err := przm.DecompressHeisenberg(frame0, dst)
	if err != nil {
		t.Fatalf("DecompressHeisenberg(frame0, dst) failed: %v", err)
	}
	if len(dec) != 0 {
		t.Fatalf("Expected 0 bytes, got %d", len(dec))
	}

	// 2. Empty slice (0 bytes) gracefully returns []byte{}, nil
	decEmpty, err := przm.DecompressHeisenberg([]byte{}, dst)
	if err != nil || len(decEmpty) != 0 {
		t.Fatalf("Expected []byte{}, nil for empty slice, got: %v, len=%d", err, len(decEmpty))
	}
	decNil, err := przm.DecompressHeisenberg(nil, nil)
	if err != nil || len(decNil) != 0 {
		t.Fatalf("Expected []byte{}, nil for nil slice, got: %v, len=%d", err, len(decNil))
	}

	// 3. Truncated non-empty input (1 <= len < 12 bytes) must return ErrHeisenbergCorrupt
	for l := 1; l < 12; l++ {
		truncated := make([]byte, l)
		if _, err := przm.DecompressHeisenberg(truncated, dst); err != przm.ErrHeisenbergCorrupt {
			t.Fatalf("Expected ErrHeisenbergCorrupt for truncated %d bytes, got: %v", l, err)
		}
	}
}
