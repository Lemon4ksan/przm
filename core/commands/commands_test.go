package commands

import (
	"bytes"
	"testing"
)

// constructComprehensiveCommandStream generates bytecode sequences covering all opcode families.
func constructComprehensiveCommandStream() []byte {
	var b []byte

	// 1. Fused Repeat Matches (0x00..0x7F)
	b = append(b, 0x05, 0x12, 0x43, 0x7E)

	// 2. Fused Extended Matches (0x80..0xBF)
	b = append(b, 0x82)             // short match without varint
	b = append(b, 0x8F, 0x85, 0x02) // match with LEB128 varint (261)

	// 3. Long Repeat Matches (0xC0..0xC3)
	b = append(b, 0xC0, 0x24)             // token 0x24 (no varint)
	b = append(b, 0xC2, 0x3F, 0x81, 0x01) // token 0x3F (with varint 129)

	// 4. Fused Bracket Matches (0xC4..0xCD)
	b = append(b, 0xC4, 0x15, 0xCB, 0x42, 0xCD, 0x01)

	// 5. Difference Lattice Matches (0xCE)
	b = append(b, 0xCE, 0x02, 0x05)             // desc 0x02, delta +5 (no varints)
	b = append(b, 0xCE, 0x3F, 0xFE, 0x10, 0x20) // desc with both varints (16, 32), delta -2

	// 6. RFC 7932 Transformed Matches (0xCF)
	b = append(b, 0xCF, 0x85, 0x12)       // b0 = 133 (>= 128), b1 = 18
	b = append(b, 0xCF, 0x0A, 0x82, 0x01) // b0 = 10 (< 128), wid = 130 (LEB128)

	// 7. Stride Matches (0xD0..0xD7)
	b = append(b, 0xD0, 0x14, 0xD7, 0x32)

	// 8. Delta-Mask Matches (0xE0..0xE8)
	b = append(b, 0xE0, 0x20)             // baseLen = 32
	b = append(b, 0xE8, 0xFF, 0x88, 0x04) // baseLen = 255 (with varint 520)

	// 9. Raw External Dict Matches (0xE9)
	b = append(b, 0xE9, 0x12)             // token 0x12 (no varint)
	b = append(b, 0xE9, 0x1F, 0x84, 0x02) // token 0x1F (with varint 260)

	// 10. Pure Literal Runs (0xF0) and RLE Runs (0xF1)
	b = append(b, 0xF0, 0x8A, 0x01) // literal run of 138 bytes
	b = append(b, 0xF1, 0x64)       // RLE run of 100 bytes

	// 11. Direct Short Literals (0xF3..0xFA)
	b = append(b, 0xF3, 0xF6, 0xFA)

	return b
}

func TestSplitAndJoinRoundtrip(t *testing.T) {
	orig := constructComprehensiveCommandStream()

	op := make([]byte, len(orig))
	meta := make([]byte, len(orig))
	v := make([]byte, len(orig))

	nOp, nM, nV, err := SplitCommands(orig, op, meta, v)
	if err != nil {
		t.Fatalf("SplitCommands failed: %v", err)
	}

	if nOp == 0 || nM == 0 || nV == 0 {
		t.Fatalf("expected non-zero stream sizes, got nOp=%d, nM=%d, nV=%d", nOp, nM, nV)
	}

	reconst := make([]byte, len(orig))
	if err := JoinCommands(reconst, op[:nOp], meta[:nM], v[:nV], len(orig)); err != nil {
		t.Fatalf("JoinCommands failed: %v", err)
	}

	if !bytes.Equal(orig, reconst) {
		t.Fatalf("reconstructed bytecode mismatch!\norig:    %x\nreconst: %x", orig, reconst)
	}
}

func TestMode8FramingRoundtrip(t *testing.T) {
	orig := constructComprehensiveCommandStream()

	op := make([]byte, len(orig))
	meta := make([]byte, len(orig))
	v := make([]byte, len(orig))

	nOp, nM, nV, err := SplitCommands(orig, op, meta, v)
	if err != nil {
		t.Fatalf("SplitCommands failed: %v", err)
	}

	mode8Buf := make([]byte, 13+nOp+nM+nV)
	packedLen, err := PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
	if err != nil {
		t.Fatalf("PackCommandsMode8 failed: %v", err)
	}
	if packedLen != len(mode8Buf) {
		t.Fatalf("expected %d packed bytes, got %d", len(mode8Buf), packedLen)
	}

	unpackedOp := make([]byte, len(orig))
	unpackedMeta := make([]byte, len(orig))
	unpackedV := make([]byte, len(orig))

	origLen, unOp, unM, unV, err := UnpackCommandsMode8(mode8Buf, unpackedOp, unpackedMeta, unpackedV)
	if err != nil {
		t.Fatalf("UnpackCommandsMode8 failed: %v", err)
	}
	if origLen != len(orig) || unOp != nOp || unM != nM || unV != nV {
		t.Fatalf("unpacked sizes mismatch: origLen=%d/%d, op=%d/%d, meta=%d/%d, v=%d/%d",
			origLen, len(orig), unOp, nOp, unM, nM, unV, nV)
	}

	reconst := make([]byte, len(orig))
	if err := JoinCommands(reconst, unpackedOp[:unOp], unpackedMeta[:unM], unpackedV[:unV], origLen); err != nil {
		t.Fatalf("JoinCommands after unpack failed: %v", err)
	}
	if !bytes.Equal(orig, reconst) {
		t.Fatalf("roundtrip mismatch after Mode 8 packing!")
	}
}

func TestVansMetaRoundtrip(t *testing.T) {
	orig := constructComprehensiveCommandStream()
	op := make([]byte, len(orig))
	meta := make([]byte, len(orig))
	v := make([]byte, len(orig))

	_, nM, _, err := SplitCommands(orig, op, meta, v)
	if err != nil {
		t.Fatalf("SplitCommands failed: %v", err)
	}

	metaSlice := meta[:nM]
	encBuf := make([]byte, 24+nM*4+1024)

	encLen, err := EncodeVansMeta8(metaSlice, encBuf)
	if err != nil {
		t.Fatalf("EncodeVansMeta8 failed: %v", err)
	}
	if encLen < 24 {
		t.Fatalf("encoded stream too short: %d", encLen)
	}

	decBuf := make([]byte, nM)
	if err := DecodeVansMeta8(encBuf[:encLen], decBuf, nM); err != nil {
		t.Fatalf("DecodeVansMeta8 failed: %v", err)
	}

	if !bytes.Equal(metaSlice, decBuf) {
		t.Fatalf("V-ANS Meta roundtrip mismatch!\nexpected: %x\ngot:      %x", metaSlice, decBuf)
	}
}

func TestCommandsZeroAllocations(t *testing.T) {
	orig := constructComprehensiveCommandStream()
	op := make([]byte, len(orig))
	meta := make([]byte, len(orig))
	v := make([]byte, len(orig))
	reconst := make([]byte, len(orig))
	mode8Buf := make([]byte, 13+len(orig)*2)
	encMetaBuf := make([]byte, 1024)
	decMetaBuf := make([]byte, len(orig))

	nOp, nM, nV, _ := SplitCommands(orig, op, meta, v)

	// Warm up
	SplitCommands(orig, op, meta, v)
	JoinCommands(reconst, op[:nOp], meta[:nM], v[:nV], len(orig))
	PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
	UnpackCommandsMode8(mode8Buf[:13+nOp+nM+nV], op, meta, v)

	encLen, _ := EncodeVansMeta8(meta[:nM], encMetaBuf)
	DecodeVansMeta8(encMetaBuf[:encLen], decMetaBuf, nM)

	allocsSplit := testing.AllocsPerRun(100, func() {
		_, _, _, _ = SplitCommands(orig, op, meta, v)
	})
	if allocsSplit != 0.00 {
		t.Fatalf("SplitCommands allocated %f allocs/op (0.00 required!)", allocsSplit)
	}

	allocsJoin := testing.AllocsPerRun(100, func() {
		_ = JoinCommands(reconst, op[:nOp], meta[:nM], v[:nV], len(orig))
	})
	if allocsJoin != 0.00 {
		t.Fatalf("JoinCommands allocated %f allocs/op (0.00 required!)", allocsJoin)
	}

	allocsPack := testing.AllocsPerRun(100, func() {
		_, _ = PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
	})
	if allocsPack != 0.00 {
		t.Fatalf("PackCommandsMode8 allocated %f allocs/op (0.00 required!)", allocsPack)
	}

	allocsUnpack := testing.AllocsPerRun(100, func() {
		_, _, _, _, _ = UnpackCommandsMode8(mode8Buf[:13+nOp+nM+nV], op, meta, v)
	})
	if allocsUnpack != 0.00 {
		t.Fatalf("UnpackCommandsMode8 allocated %f allocs/op (0.00 required!)", allocsUnpack)
	}

	allocsMetaEnc := testing.AllocsPerRun(100, func() {
		_, _ = EncodeVansMeta8(meta[:nM], encMetaBuf)
	})
	if allocsMetaEnc != 0.00 {
		t.Fatalf("EncodeVansMeta8 allocated %f allocs/op (0.00 required!)", allocsMetaEnc)
	}

	allocsMetaDec := testing.AllocsPerRun(100, func() {
		_ = DecodeVansMeta8(encMetaBuf[:encLen], decMetaBuf, nM)
	})
	if allocsMetaDec != 0.00 {
		t.Fatalf("DecodeVansMeta8 allocated %f allocs/op (0.00 required!)", allocsMetaDec)
	}
}

func TestEmitBlockCommandsAndUnpack(t *testing.T) {
	block := []byte("ABCDEFGHABCDEFGH1234567812345678")
	edges := []TrellisEdge{
		{From: 0, Length: 1, Kind: 0},
		{From: 1, Length: 1, Kind: 0},
		{From: 2, Length: 1, Kind: 0},
		{From: 3, Length: 1, Kind: 0},
		{From: 4, Length: 4, Dist: 4, Kind: 1, Arg: 0},
		{From: 8, Length: 8, Dist: 8, Kind: 2, Arg: 7},
		{From: 16, Length: 8, Dist: 16, Kind: 3, Arg: 4},
		{From: 24, Length: 8, Dist: 8, Kind: 4},
	}

	var cmdsOut, litsOut []byte
	var offsOut []uint32

	EmitBlockCommands(edges, block, 4, &cmdsOut, &litsOut, &offsOut)
	if len(cmdsOut) == 0 {
		t.Fatalf("expected emitted commands, got 0")
	}

	packed := PackageCommandsSection(cmdsOut)
	if len(packed) == 0 {
		t.Fatalf("expected packaged commands section")
	}

	var op [1024]byte
	var meta [1024]byte
	var varBuf [1024]byte
	var cmdsUnpacked [1024]byte

	unpacked, err := UnpackSection2Commands(packed, op[:], meta[:], varBuf[:], cmdsUnpacked[:])
	if err != nil {
		t.Fatalf("UnpackSection2Commands failed: %v", err)
	}
	if !bytes.Equal(cmdsOut, unpacked) {
		t.Fatalf("unpacked commands mismatch: got %x, want %x", unpacked, cmdsOut)
	}
}

func BenchmarkCommands(b *testing.B) {
	orig := constructComprehensiveCommandStream()
	op := make([]byte, len(orig))
	meta := make([]byte, len(orig))
	v := make([]byte, len(orig))
	reconst := make([]byte, len(orig))
	encMetaBuf := make([]byte, 1024)
	decMetaBuf := make([]byte, len(orig))

	nOp, nM, nV, _ := SplitCommands(orig, op, meta, v)
	encLen, _ := EncodeVansMeta8(meta[:nM], encMetaBuf)

	b.Run("Split", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _, _ = SplitCommands(orig, op, meta, v)
		}
	})

	b.Run("Join", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = JoinCommands(reconst, op[:nOp], meta[:nM], v[:nV], len(orig))
		}
	})

	b.Run("VansMetaEncode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = EncodeVansMeta8(meta[:nM], encMetaBuf)
		}
	})

	b.Run("VansMetaDecode", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = DecodeVansMeta8(encMetaBuf[:encLen], decMetaBuf, nM)
		}
	})
}
