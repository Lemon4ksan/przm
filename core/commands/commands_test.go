// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
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

	mode8Buf := make([]byte, 13+len(orig)*4)
	packedLen, err := PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
	if err != nil {
		t.Fatalf("PackCommandsMode8 failed: %v", err)
	}
	if packedLen == 0 {
		t.Fatalf("expected non-zero packed bytes, got %d", packedLen)
	}

	unpackedOp := make([]byte, len(orig))
	unpackedMeta := make([]byte, len(orig))
	unpackedV := make([]byte, len(orig))

	origLen, unOp, unM, unV, err := UnpackCommandsMode8(mode8Buf[:packedLen], unpackedOp, unpackedMeta, unpackedV)
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
	packedLen, _ := PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
	UnpackCommandsMode8(mode8Buf[:packedLen], op, meta, v)

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
		_, _, _, _, _ = UnpackCommandsMode8(mode8Buf[:packedLen], op, meta, v)
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

func TestEmitRFCWordAndDiffLatticeCommands(t *testing.T) {
	block := []byte("ABCDthe transformed word hereXYZ12345678")
	neg3 := int8(-3)
	edges := []TrellisEdge{
		// 4 literals
		{From: 0, Length: 1, Kind: 0},
		{From: 1, Length: 1, Kind: 0},
		{From: 2, Length: 1, Kind: 0},
		{From: 3, Length: 1, Kind: 0},
		// Kind 5: RFC word (p = 14016, len = 5) - should flush the 4 pending literals first
		{From: 4, Length: 5, Dist: 14016, Kind: 5},
		// Kind 5: RFC word directly (no pending literals, p = 500, len = 6)
		{From: 9, Length: 6, Dist: 500, Kind: 5},
		// 2 literals
		{From: 15, Length: 1, Kind: 0},
		{From: 16, Length: 1, Kind: 0},
		// Kind 6: DiffLattice (mode = 1, delta = 2, len = 6) - short match (< 7 diff)
		{From: 17, Length: 6, Dist: 2, Kind: 6, Arg: 1},
		// Kind 6: DiffLattice (mode = 0, delta = -3, len = 15) - long match (>= 7 diff)
		{From: 23, Length: 15, Dist: uint32(byte(neg3)), Kind: 6, Arg: 0},
		// 2 ending literals (indices 38, 39)
		{From: 38, Length: 1, Kind: 0},
		{From: 39, Length: 1, Kind: 0},
	}

	var cmdsOut, litsOut []byte
	var offsOut []uint32

	EmitBlockCommands(edges, block, 4, &cmdsOut, &litsOut, &offsOut)
	if len(cmdsOut) == 0 {
		t.Fatalf("expected emitted commands, got 0")
	}

	// Verify no offsets in Section 3 for Kind 5 or Kind 6
	if len(offsOut) != 0 {
		t.Fatalf("expected 0 offsets for dict/difflattice commands, got %d", len(offsOut))
	}

	// Test packaging and unpacking roundtrip
	packed := PackageCommandsSection(cmdsOut)
	var op [2048]byte
	var meta [2048]byte
	var varBuf [2048]byte
	var cmdsUnpacked [2048]byte

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

	mode8Buf := make([]byte, 13+len(orig)*4)
	packedLen, _ := PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
	unpackedOp := make([]byte, len(orig))
	unpackedMeta := make([]byte, len(orig))
	unpackedV := make([]byte, len(orig))

	b.Run("PackMode8", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), mode8Buf)
		}
	})

	b.Run("UnpackMode8", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _, _, _ = UnpackCommandsMode8(mode8Buf[:packedLen], unpackedOp, unpackedMeta, unpackedV)
		}
	})
}

func readLEB128Test(b []byte) (uint32, int) {
	var val uint32
	var shift uint
	for i, c := range b {
		val |= uint32(c&0x7F) << shift
		if (c & 0x80) == 0 {
			return val, i + 1
		}
		shift += 7
	}
	return val, len(b)
}

func TestEmitExplicitMatchAllLitAndLengths(t *testing.T) {
	for _, bMin := range []int{3, 4} {
		for nLit := 0; nLit <= 16; nLit++ {
			for lMatch := bMin; lMatch <= bMin+100; lMatch++ {
				pendingLits := make([]byte, nLit)
				for i := range pendingLits {
					pendingLits[i] = byte('A' + (i % 26))
				}
				var cmdsOut, litsOut []byte
				var offsOut []uint32

				e := TrellisEdge{
					Length: uint16(lMatch),
					Dist:   42,
					Kind:   4,
				}

				emitExplicitMatch(e, &pendingLits, bMin, &cmdsOut, &litsOut, &offsOut)

				if len(offsOut) != 1 || offsOut[0] != 42 {
					t.Fatalf("bMin=%d, nLit=%d, lMatch=%d: expected offsOut=[42], got %v", bMin, nLit, lMatch, offsOut)
				}
				if nLit > 3 && len(pendingLits) != 0 {
					t.Fatalf("bMin=%d, nLit=%d, lMatch=%d: pendingLits not cleared after flush", bMin, nLit, lMatch)
				}

				matchOpIdx := 0
				if nLit > 15 {
					if len(pendingLits) != 0 {
						t.Fatalf("bMin=%d, nLit=%d, lMatch=%d: pendingLits not cleared after flush", bMin, nLit, lMatch)
					}
					if len(cmdsOut) < 2 || cmdsOut[0] != 0xF0 {
						t.Fatalf("bMin=%d, nLit=%d: expected 0xF0 literal run op, got %x", bMin, nLit, cmdsOut)
					}
					val, nRead := readLEB128Test(cmdsOut[1:])
					if int(val) != nLit {
						t.Fatalf("bMin=%d, nLit=%d: LEB128 decoded %d, want %d", bMin, nLit, val, nLit)
					}
					matchOpIdx = 1 + nRead
				} else if nLit > 3 {
					if len(cmdsOut) < 2 || cmdsOut[0] != 0xE9 {
						t.Fatalf("bMin=%d, nLit=%d: expected 0xE9 op, got %x", bMin, nLit, cmdsOut)
					}
					tok := cmdsOut[1]
					if int(tok>>4) != nLit {
						t.Fatalf("bMin=%d, nLit=%d: expected tok lit %d, got %d", bMin, nLit, nLit, tok>>4)
					}
					continue
				}

				if matchOpIdx >= len(cmdsOut) {
					t.Fatalf(
						"bMin=%d, nLit=%d, lMatch=%d: missing explicit match opcode in %x",
						bMin,
						nLit,
						lMatch,
						cmdsOut,
					)
				}
				op := cmdsOut[matchOpIdx]
				if op < 0x80 || op >= 0xC0 {
					t.Fatalf("bMin=%d, nLit=%d, lMatch=%d: op 0x%02x not in [0x80, 0xC0)", bMin, nLit, lMatch, op)
				}
				expectedLitBits := 0
				if nLit <= 3 {
					expectedLitBits = nLit
				}
				actualLitBits := int((op >> 4) & 3)
				if actualLitBits != expectedLitBits {
					t.Fatalf(
						"bMin=%d, nLit=%d, lMatch=%d: literal bits got %d, want %d",
						bMin,
						nLit,
						lMatch,
						actualLitBits,
						expectedLitBits,
					)
				}

				matchOffset := lMatch - bMin
				if matchOffset < 15 {
					if int(op&15) != matchOffset {
						t.Fatalf(
							"bMin=%d, nLit=%d, lMatch=%d: match code got %d, want %d",
							bMin,
							nLit,
							lMatch,
							op&15,
							matchOffset,
						)
					}
					if len(cmdsOut) != matchOpIdx+1 {
						t.Fatalf(
							"bMin=%d, nLit=%d, lMatch=%d: unexpected extra bytes in %x",
							bMin,
							nLit,
							lMatch,
							cmdsOut,
						)
					}
				} else {
					if int(op&15) != 15 {
						t.Fatalf(
							"bMin=%d, nLit=%d, lMatch=%d: expected match code 15, got %d",
							bMin,
							nLit,
							lMatch,
							op&15,
						)
					}
					extraVal, nRead := readLEB128Test(cmdsOut[matchOpIdx+1:])
					if int(extraVal) != matchOffset-15 {
						t.Fatalf(
							"bMin=%d, nLit=%d, lMatch=%d: LEB128 extra got %d, want %d",
							bMin,
							nLit,
							lMatch,
							extraVal,
							matchOffset-15,
						)
					}
					if len(cmdsOut) != matchOpIdx+1+nRead {
						t.Fatalf("bMin=%d, nLit=%d, lMatch=%d: trailing bytes in %x", bMin, nLit, lMatch, cmdsOut)
					}
				}

				opBuf := make([]byte, len(cmdsOut))
				metaBuf := make([]byte, len(cmdsOut))
				varBuf := make([]byte, len(cmdsOut))
				nOp, nM, nV, err := SplitCommands(cmdsOut, opBuf, metaBuf, varBuf)
				if err != nil {
					t.Fatalf("SplitCommands failed on %x: %v", cmdsOut, err)
				}
				reconst := make([]byte, len(cmdsOut))
				if err := JoinCommands(reconst, opBuf[:nOp], metaBuf[:nM], varBuf[:nV], len(cmdsOut)); err != nil {
					t.Fatalf("JoinCommands failed: %v", err)
				}
				if !bytes.Equal(reconst, cmdsOut) {
					t.Fatalf("roundtrip mismatch!\nwant: %x\ngot:  %x", cmdsOut, reconst)
				}
			}
		}
	}
}

func TestPackAndUnpackMode8Comprehensive(t *testing.T) {
	testCases := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "SmallFlatCommands",
			gen: func() []byte {
				return []byte{0x05, 0x12, 0x24, 0x31, 0x40}
			},
		},
		{
			name: "RepetitiveOpStreamVansCompression",
			gen: func() []byte {
				b := make([]byte, 300)
				for i := range b {
					b[i] = 0x05
				}
				return b
			},
		},
		{
			name: "RepetitiveMetaStreamVansCompression",
			gen: func() []byte {
				b := make([]byte, 400)
				for i := 0; i < 400; i += 2 {
					b[i] = 0xC0
					b[i+1] = 0x24
				}
				return b
			},
		},
		{
			name: "RepetitiveVarStreamVansCompression",
			gen: func() []byte {
				b := make([]byte, 600)
				for i := 0; i < 600; i += 2 {
					b[i] = 0x8F
					b[i+1] = 0x10
				}
				return b
			},
		},
		{
			name: "ComprehensiveTrace",
			gen:  constructComprehensiveCommandStream,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orig := tc.gen()
			op := make([]byte, len(orig))
			meta := make([]byte, len(orig))
			v := make([]byte, len(orig))

			nOp, nM, nV, err := SplitCommands(orig, op, meta, v)
			if err != nil {
				t.Fatalf("SplitCommands failed: %v", err)
			}

			dst := make([]byte, 13+len(orig)*4+1024)
			packedLen, err := PackCommandsMode8(op[:nOp], meta[:nM], v[:nV], len(orig), dst)
			if err != nil {
				t.Fatalf("PackCommandsMode8 failed: %v", err)
			}
			if packedLen < 13 {
				t.Fatalf("packedLen %d < 13", packedLen)
			}
			if dst[0] != 0x08 {
				t.Fatalf("expected Mode 8 byte 0x08, got 0x%02x", dst[0])
			}

			decOp := make([]byte, len(orig)+64)
			decMeta := make([]byte, len(orig)+64)
			decV := make([]byte, len(orig)+64)

			uOrigLen, unOp, unM, unV, err := UnpackCommandsMode8(dst[:packedLen], decOp, decMeta, decV)
			if err != nil {
				t.Fatalf("UnpackCommandsMode8 failed: %v", err)
			}
			if uOrigLen != len(orig) {
				t.Fatalf("origLen got %d, want %d", uOrigLen, len(orig))
			}
			if unOp != nOp || unM != nM || unV != nV {
				t.Fatalf("substream lengths mismatch: got (%d, %d, %d), want (%d, %d, %d)",
					unOp, unM, unV, nOp, nM, nV)
			}
			if !bytes.Equal(decOp[:unOp], op[:nOp]) {
				t.Fatalf("op stream mismatch")
			}
			if !bytes.Equal(decMeta[:unM], meta[:nM]) {
				t.Fatalf("meta stream mismatch")
			}
			if !bytes.Equal(decV[:unV], v[:nV]) {
				t.Fatalf("var stream mismatch")
			}

			reconst := make([]byte, len(orig))
			if err := JoinCommands(reconst, decOp[:unOp], decMeta[:unM], decV[:unV], len(orig)); err != nil {
				t.Fatalf("JoinCommands failed: %v", err)
			}
			if !bytes.Equal(reconst, orig) {
				t.Fatalf("reconstructed stream mismatch!")
			}
		})
	}
}

func TestMode8ActivationInPackageCommandsSection(t *testing.T) {
	// 1. Structured command stream that SHOULD activate Mode 8
	// 500 repeat match commands (each 1 byte): opStream has 500 identical bytes (opcode 0x06)
	structured := make([]byte, 500)
	for i := range structured {
		structured[i] = 0x06
	}

	pkgStructured := PackageCommandsSection(structured)
	if len(pkgStructured) == 0 {
		t.Fatalf("PackageCommandsSection returned empty")
	}
	if pkgStructured[0] != 0x08 {
		t.Fatalf("expected Mode 8 (0x08) activation for structured commands, got 0x%02x (len=%d, origLen=%d)",
			pkgStructured[0], len(pkgStructured), len(structured))
	}
	if len(pkgStructured) >= len(structured) {
		t.Fatalf("expected Mode 8 to achieve compression: packed=%d, orig=%d", len(pkgStructured), len(structured))
	}

	var op [1024]byte
	var meta [1024]byte
	var varBuf [1024]byte
	var cmdsUnpacked [1024]byte

	unpacked, err := UnpackSection2Commands(pkgStructured, op[:], meta[:], varBuf[:], cmdsUnpacked[:])
	if err != nil {
		t.Fatalf("UnpackSection2Commands failed on Mode 8: %v", err)
	}
	if !bytes.Equal(unpacked, structured) {
		t.Fatalf("unpacked mismatch for structured commands")
	}

	// 2. Small unstructured commands that should fall back to flat Mode 0x09
	small := []byte{0x01, 0x02, 0x03}
	pkgSmall := PackageCommandsSection(small)
	if len(pkgSmall) != 1+len(small) || pkgSmall[0] != 0x09 {
		t.Fatalf("expected flat fallback 0x09 for small commands, got %x", pkgSmall)
	}
	unpackedSmall, err := UnpackSection2Commands(pkgSmall, op[:], meta[:], varBuf[:], cmdsUnpacked[:])
	if err != nil {
		t.Fatalf("UnpackSection2Commands failed on flat 0x09: %v", err)
	}
	if !bytes.Equal(unpackedSmall, small) {
		t.Fatalf("unpacked mismatch for small commands")
	}
}

func TestCrossDecodeWithC99(t *testing.T) {
	candidates := []string{
		"../../test_przm_dec.exe",
		"../test_przm_dec.exe",
		"test_przm_dec.exe",
	}
	var binPath string
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				binPath = abs
				break
			}
		}
	}
	if binPath == "" {
		t.Skip("test_przm_dec.exe not found, skipping cross-decode test")
	}

	cmd := exec.Command(binPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test_przm_dec.exe internal test suite failed: %v\nOutput:\n%s", err, string(out))
	}
	if !bytes.Contains(out, []byte("ALL CLEAN ROOM C99 TESTS PASSED SUCCESSFULLY")) {
		t.Fatalf("test_przm_dec.exe did not output success banner:\n%s", string(out))
	}

	litStr := []byte("PRZM_EXPLICIT_TEST_")
	expected := bytes.Repeat(litStr, 3)

	cmds := []byte{0xF0, 19, 0x8F, 0x00, 0x8F, 0x00}
	opBuf := make([]byte, len(cmds))
	metaBuf := make([]byte, len(cmds))
	varBuf := make([]byte, len(cmds))
	nOp, nM, nV, err := SplitCommands(cmds, opBuf, metaBuf, varBuf)
	if err != nil {
		t.Fatalf("SplitCommands failed: %v", err)
	}

	mode8Dst := make([]byte, 128)
	nPack, err := PackCommandsMode8(opBuf[:nOp], metaBuf[:nM], varBuf[:nV], len(cmds), mode8Dst)
	if err != nil {
		t.Fatalf("PackCommandsMode8 failed: %v", err)
	}
	s2 := mode8Dst[:nPack]

	s3 := []byte{19, 19}
	s4 := litStr

	var frame []byte
	frame = append(frame, 'P', 'R', 'Z', 'M')
	frame = append(frame, 0x00)
	frame = append(frame, byte(len(expected)&0xFF), byte((len(expected)>>8)&0xFF), byte((len(expected)>>16)&0xFF))
	frame = append(frame, byte(len(s2)&0xFF), byte((len(s2)>>8)&0xFF))
	frame = append(frame, byte(len(s3)&0xFF), byte((len(s3)>>8)&0xFF))
	frame = append(frame, 0, 16, 0, 0, 0, 0, 0, 0)
	frame = append(frame, s2...)
	frame = append(frame, s3...)
	frame = append(frame, s4...)

	tmpDir := t.TempDir()
	inPath := filepath.Join(tmpDir, "mode8_test.przm")
	outPath := filepath.Join(tmpDir, "mode8_test.out")
	if err := os.WriteFile(inPath, frame, 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	decCmd := exec.Command(binPath, inPath, outPath)
	decOut, err := decCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("C decompressor failed: %v\nOutput:\n%s", err, string(decOut))
	}

	result, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(result, expected) {
		t.Fatalf("Cross-decode mismatch!\nwant: %s\ngot:  %s", string(expected), string(result))
	}
}
