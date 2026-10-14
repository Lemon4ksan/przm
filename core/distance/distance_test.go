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

	for c := uint8(0); c < 49; c++ {
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
	for i := 0; i < n; i++ {
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
