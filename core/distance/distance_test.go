// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package distance

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestDistanceToCodeAndBack(t *testing.T) {
	for d := uint32(1); d <= 4; d++ {
		c, extra, nb := DistanceToCode(d)
		if c != uint8(d-1) || extra != 0 || nb != 0 {
			t.Fatalf("small distance %d: got c=%d, extra=%d, nb=%d", d, c, extra, nb)
		}
		back, err := CodeToDistance(c, extra)
		if err != nil || back != d {
			t.Fatalf("small distance roundtrip %d: got %d, err=%v", d, back, err)
		}
	}

	for c := range uint8(49) {
		nb := DistExtraBits[c]
		base := DistBase[c]

		minDist := base
		gotC, extra, gotNb := DistanceToCode(minDist)
		if gotC != c || extra != 0 || gotNb != nb {
			t.Fatalf("bracket %d minDist %d: got c=%d, extra=%d, nb=%d", c, minDist, gotC, extra, gotNb)
		}
		back, err := CodeToDistance(gotC, extra)
		if err != nil || back != minDist {
			t.Fatalf("bracket %d minDist roundtrip: got %d, err=%v", c, back, err)
		}

		if nb > 0 && nb <= 22 {
			maxDist := base + (uint32(1) << nb) - 1
			gotC, extra, gotNb = DistanceToCode(maxDist)
			if gotC != c || gotNb != nb {
				t.Fatalf("bracket %d maxDist %d: got c=%d, nb=%d", c, maxDist, gotC, gotNb)
			}
			back, err = CodeToDistance(gotC, extra)
			if err != nil || back != maxDist {
				t.Fatalf("bracket %d maxDist roundtrip: got %d, err=%v", c, back, err)
			}
		}
	}
}

func TestBitstreamReaderWriter(t *testing.T) {
	buf := make([]byte, 256)
	w := NewBitWriter(buf)

	testValues := []struct {
		val uint32
		nb  uint8
	}{
		{val: 0b101, nb: 3},
		{val: 0b00110, nb: 5},
		{val: 0b111100001010, nb: 12},
		{val: 0b1, nb: 1},
		{val: 0b10101010101010101, nb: 17},
	}

	for _, tv := range testValues {
		if err := w.WriteBits(tv.val, tv.nb); err != nil {
			t.Fatalf("WriteBits failed: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	written := buf[:w.BytesWritten()]
	r := BitReader{Buf: written}

	for i, tv := range testValues {
		val, err := r.ReadBits(tv.nb)
		if err != nil {
			t.Fatalf("ReadBits item %d failed: %v", i, err)
		}
		if val != tv.val {
			t.Fatalf("item %d mismatch: expected 0b%b, got 0b%b", i, tv.val, val)
		}
	}
}

func TestClusteredVANSRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n := 256
	codes := make([]uint8, n)
	for i := range n {
		codes[i] = uint8(rng.Intn(49))
	}

	dst := make([]byte, 1024)
	encLen, err := EncodeDistClusteredVans(codes, dst)
	if err != nil {
		t.Fatalf("EncodeDistClusteredVans failed: %v", err)
	}
	if encLen < 5 {
		t.Fatalf("encoded stream too short: %d", encLen)
	}

	decCodes := make([]uint8, n)
	if err := DecodeDistClusteredVans(dst[:encLen], decCodes, n); err != nil {
		t.Fatalf("DecodeDistClusteredVans failed: %v", err)
	}

	if !bytes.Equal(codes, decCodes) {
		t.Fatalf("Clustered V-ANS roundtrip mismatch!")
	}
}

func TestLocalityQueue(t *testing.T) {
	var q LocalityQueue
	q.Init(1, false)
	if q.R != [4]uint32{1, 4, 8, 16} {
		t.Fatalf("default init mismatch: %v", q.R)
	}

	if d := q.UpdateRepeat(0); d != 1 || q.R != [4]uint32{1, 4, 8, 16} {
		t.Fatalf("k=0 mismatch: d=%d, queue=%v", d, q.R)
	}
	if d := q.UpdateRepeat(1); d != 4 || q.R != [4]uint32{4, 1, 8, 16} {
		t.Fatalf("k=1 mismatch: d=%d, queue=%v", d, q.R)
	}
	if d := q.UpdateRepeat(2); d != 8 || q.R != [4]uint32{8, 4, 1, 16} {
		t.Fatalf("k=2 mismatch: d=%d, queue=%v", d, q.R)
	}
	if d := q.UpdateRepeat(3); d != 16 || q.R != [4]uint32{16, 8, 4, 1} {
		t.Fatalf("k=3 mismatch: d=%d, queue=%v", d, q.R)
	}

	q.UpdateExplicit(100)
	if q.R != [4]uint32{100, 16, 8, 4} {
		t.Fatalf("explicit push mismatch: %v", q.R)
	}
}

func TestOffsetModesRoundtrip(t *testing.T) {
	distances := []uint32{1, 4, 12, 100, 500, 2048, 65536, 1000000}
	modes := []struct {
		name    string
		mode    byte
		useVANS bool
	}{
		{"Mode06_VANS", ModeDistancePrefix, true},
		{"Mode06_Raw", ModeDistancePrefix, false},
		{"Mode0A_VANS", ModeDistancePrefixCompact, true},
		{"Mode0B_VANS", ModeDistancePrefixByte, true},
	}

	packed := make([]byte, 2048)
	unpackedVarints := make([]byte, 2048)

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			n, err := PackDistancePrefix(distances, m.mode, m.useVANS, packed)
			if err != nil {
				t.Fatalf("PackDistancePrefix failed: %v", err)
			}
			vLen, err := UnpackOffsets(packed[:n], unpackedVarints)
			if err != nil {
				t.Fatalf("UnpackOffsets failed: %v", err)
			}

			var pos int
			for idx, expectedD := range distances {
				var d uint32
				var shift int
				for {
					if pos >= vLen {
						t.Fatalf("premature EOF reading varint for dist %d", idx)
					}
					b := unpackedVarints[pos]
					pos++
					d |= uint32(b&0x7F) << shift
					if (b & 0x80) == 0 {
						break
					}
					shift += 7
				}
				if d != expectedD {
					t.Fatalf("dist %d mismatch: expected %d, got %d", idx, expectedD, d)
				}
			}
			if pos != vLen {
				t.Fatalf("extra trailing varint bytes: read %d, total %d", pos, vLen)
			}
		})
	}
}

func TestDistanceZeroAllocations(t *testing.T) {
	distances := []uint32{1, 4, 12, 100, 500, 2048, 65536, 1000000}
	packed := make([]byte, 1024)
	unpackedVarints := make([]byte, 1024)
	codes := make([]uint8, len(distances))
	for i, d := range distances {
		c, _, _ := DistanceToCode(d)
		codes[i] = c
	}
	encCodes := make([]byte, 256)
	decCodes := make([]uint8, len(distances))

	nPacked, _ := PackDistancePrefix(distances, ModeDistancePrefix, true, packed)
	encCodesLen, _ := EncodeDistClusteredVans(codes, encCodes)

	// Warmup
	DistanceToCode(500)
	CodeToDistance(12, 4)
	EncodeDistClusteredVans(codes, encCodes)
	DecodeDistClusteredVans(encCodes[:encCodesLen], decCodes, len(distances))
	PackDistancePrefix(distances, ModeDistancePrefix, true, packed)
	UnpackOffsets(packed[:nPacked], unpackedVarints)

	allocsCode := testing.AllocsPerRun(100, func() {
		c, extra, _ := DistanceToCode(500)
		_, _ = CodeToDistance(c, extra)
	})
	if allocsCode != 0.00 {
		t.Fatalf("DistanceToCode allocated %f allocs/op (0.00 required!)", allocsCode)
	}

	allocsVansEnc := testing.AllocsPerRun(100, func() {
		_, _ = EncodeDistClusteredVans(codes, encCodes)
	})
	if allocsVansEnc != 0.00 {
		t.Fatalf("EncodeDistClusteredVans allocated %f allocs/op (0.00 required!)", allocsVansEnc)
	}

	allocsVansDec := testing.AllocsPerRun(100, func() {
		_ = DecodeDistClusteredVans(encCodes[:encCodesLen], decCodes, len(distances))
	})
	if allocsVansDec != 0.00 {
		t.Fatalf("DecodeDistClusteredVans allocated %f allocs/op (0.00 required!)", allocsVansDec)
	}

	allocsPack := testing.AllocsPerRun(100, func() {
		_, _ = PackDistancePrefix(distances, ModeDistancePrefix, true, packed)
	})
	if allocsPack != 0.00 {
		t.Fatalf("PackDistancePrefix allocated %f allocs/op (0.00 required!)", allocsPack)
	}

	allocsUnpack := testing.AllocsPerRun(100, func() {
		_, _ = UnpackOffsets(packed[:nPacked], unpackedVarints)
	})
	if allocsUnpack != 0.00 {
		t.Fatalf("UnpackOffsets allocated %f allocs/op (0.00 required!)", allocsUnpack)
	}
}

func BenchmarkDistanceToCode(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = DistanceToCode(uint32(i%100000 + 1))
	}
}

func BenchmarkCodeToDistance(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = CodeToDistance(uint8(i%49), uint32(i%16))
	}
}

func BenchmarkLocalityQueue(b *testing.B) {
	q := NewLocalityQueue()
	b.Run("Push", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			q.Push(uint32(i + 1))
		}
	})
	b.Run("Find", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = q.Find(uint32(i % 10))
		}
	})
}

func BenchmarkVansDist4(b *testing.B) {
	codes := make([]uint8, 256)
	for i := range codes {
		codes[i] = uint8(i % 49)
	}
	enc := make([]byte, 512)
	dec := make([]uint8, 256)
	nEnc, _ := EncodeDistClusteredVans(codes, enc)

	b.Run("Encode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = EncodeDistClusteredVans(codes, enc)
		}
	})

	b.Run("Decode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = DecodeDistClusteredVans(enc[:nEnc], dec, len(codes))
		}
	})
}

func BenchmarkDistancePrefix(b *testing.B) {
	distances := make([]uint32, 256)
	for i := range distances {
		distances[i] = uint32(i*17 + 1)
	}
	packed := make([]byte, 1024)
	unpacked := make([]byte, 2048)
	nPacked, _ := PackDistancePrefix(distances, ModeDistancePrefix, true, packed)

	b.Run("Pack", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = PackDistancePrefix(distances, ModeDistancePrefix, true, packed)
		}
	})

	b.Run("Unpack", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = UnpackDistancePrefix(packed[:nPacked], unpacked)
		}
	})
}

func TestSelectPrefixMode(t *testing.T) {
	// All <= 255 -> ModeDistancePrefixByte (0x0B)
	if m := SelectPrefixMode(255, 255, 255); m != ModeDistancePrefixByte {
		t.Fatalf("expected ModeDistancePrefixByte, got 0x%02X", m)
	}
	// origVarintLen > 255 -> ModeDistancePrefixCompact (0x0A)
	if m := SelectPrefixMode(256, 50, 50); m != ModeDistancePrefixCompact {
		t.Fatalf("expected ModeDistancePrefixCompact, got 0x%02X", m)
	}
	// matchCount > 255 -> ModeDistancePrefixCompact (0x0A)
	if m := SelectPrefixMode(50, 256, 50); m != ModeDistancePrefixCompact {
		t.Fatalf("expected ModeDistancePrefixCompact, got 0x%02X", m)
	}
	// codeLen > 255 -> ModeDistancePrefixCompact (0x0A)
	if m := SelectPrefixMode(50, 50, 256); m != ModeDistancePrefixCompact {
		t.Fatalf("expected ModeDistancePrefixCompact, got 0x%02X", m)
	}
	// All <= 65535 -> ModeDistancePrefixCompact (0x0A)
	if m := SelectPrefixMode(65535, 65535, 65535); m != ModeDistancePrefixCompact {
		t.Fatalf("expected ModeDistancePrefixCompact, got 0x%02X", m)
	}
	// origVarintLen > 65535 -> ModeDistancePrefix (0x06)
	if m := SelectPrefixMode(65536, 100, 100); m != ModeDistancePrefix {
		t.Fatalf("expected ModeDistancePrefix, got 0x%02X", m)
	}
	// matchCount > 65535 -> ModeDistancePrefix (0x06)
	if m := SelectPrefixMode(100, 65536, 100); m != ModeDistancePrefix {
		t.Fatalf("expected ModeDistancePrefix, got 0x%02X", m)
	}
	// codeLen > 65535 -> ModeDistancePrefix (0x06)
	if m := SelectPrefixMode(100, 100, 65536); m != ModeDistancePrefix {
		t.Fatalf("expected ModeDistancePrefix, got 0x%02X", m)
	}
}

func verifyUnpackOffsetsLossless(t *testing.T, packed []byte, expected []uint32) {
	t.Helper()
	unpackedVarints := make([]byte, len(expected)*5+64)
	vLen, err := UnpackOffsets(packed, unpackedVarints)
	if err != nil {
		t.Fatalf("UnpackOffsets failed: %v", err)
	}

	pos := 0
	for idx, expectedD := range expected {
		var d uint32
		var shift int
		for {
			if pos >= vLen {
				t.Fatalf("premature EOF reading varint for dist %d", idx)
			}
			b := unpackedVarints[pos]
			pos++
			d |= uint32(b&0x7F) << shift
			if (b & 0x80) == 0 {
				break
			}
			shift += 7
		}
		if d != expectedD {
			t.Fatalf("dist %d mismatch: expected %d, got %d", idx, expectedD, d)
		}
	}
	if pos != vLen {
		t.Fatalf("extra trailing varint bytes: read %d, total %d", pos, vLen)
	}
}

func TestPackageOffsetsSectionModes(t *testing.T) {
	// 1. Empty slice returns nil
	if res := PackageOffsetsSection(nil, nil); res != nil {
		t.Fatalf("expected nil for empty offsets, got %v", res)
	}

	// 2. Small stream (70 matches of distance 1000, all fields <= 255) -> Expect ModeDistancePrefixByte (0x0B)
	{
		offs := make([]uint32, 70)
		for i := range offs {
			offs[i] = 1000
		}
		s3Buf := make([]byte, 1024)
		packed := PackageOffsetsSection(offs, s3Buf)
		if packed == nil {
			t.Fatal("PackageOffsetsSection returned nil")
		}
		if packed[0] != ModeDistancePrefixByte {
			t.Fatalf("expected small stream mode 0x0B, got 0x%02X", packed[0])
		}
		verifyUnpackOffsetsLossless(t, packed, offs)
	}

	// 3. Medium stream by count (1000 matches of distance 1000, <= 65535, > 255) -> Expect ModeDistancePrefixCompact (0x0A)
	{
		offs := make([]uint32, 1000)
		for i := range offs {
			offs[i] = 1000
		}
		s3Buf := make([]byte, 16384)
		packed := PackageOffsetsSection(offs, s3Buf)
		if packed == nil {
			t.Fatal("PackageOffsetsSection returned nil")
		}
		if packed[0] != ModeDistancePrefixCompact {
			t.Fatalf("expected medium stream mode 0x0A, got 0x%02X", packed[0])
		}
		verifyUnpackOffsetsLossless(t, packed, offs)
	}

	// 4. Medium stream by origVarintLen (200 matches of distance 1000 -> origVarintLen = 400 > 255, matchCount = 200 <= 255) -> Expect ModeDistancePrefixCompact (0x0A)
	{
		offs := make([]uint32, 200)
		for i := range offs {
			offs[i] = 1000
		}
		s3Buf := make([]byte, 16384)
		packed := PackageOffsetsSection(offs, s3Buf)
		if packed == nil {
			t.Fatal("PackageOffsetsSection returned nil")
		}
		if packed[0] != ModeDistancePrefixCompact {
			t.Fatalf("expected medium stream (large varint) mode 0x0A, got 0x%02X", packed[0])
		}
		verifyUnpackOffsetsLossless(t, packed, offs)
	}

	// 5. Large stream (> 65535 matches of distance 1000) -> Expect ModeDistancePrefix (0x06)
	{
		offs := make([]uint32, 70000)
		for i := range offs {
			offs[i] = 1000
		}
		s3Buf := make([]byte, 500000)
		packed := PackageOffsetsSection(offs, s3Buf)
		if packed == nil {
			t.Fatal("PackageOffsetsSection returned nil")
		}
		if packed[0] != ModeDistancePrefix {
			t.Fatalf("expected large stream mode 0x06, got 0x%02X", packed[0])
		}
		verifyUnpackOffsetsLossless(t, packed, offs)
	}
}

func TestPackageOffsetsSectionFallback(t *testing.T) {
	// Offset sequence where prefix encoding overhead exceeds raw LEB128
	// 10 small alternating distances: raw LEB128 is 10 bytes, while prefix header + VANS header >= 9 + 5 = 14 bytes
	offs := make([]uint32, 10)
	for i := range offs {
		offs[i] = uint32((i % 4) + 1)
	}
	s3Buf := make([]byte, 1024)
	packed := PackageOffsetsSection(offs, s3Buf)
	if packed == nil {
		t.Fatal("PackageOffsetsSection returned nil")
	}
	if packed[0] != ModeFlatCompact {
		t.Fatalf("expected fallback to ModeFlatCompact (0x09), got 0x%02X", packed[0])
	}
	verifyUnpackOffsetsLossless(t, packed, offs)
}
