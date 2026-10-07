// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestDedup_TableOperations(t *testing.T) {
	var table DedupTable
	table.Reset()

	blockA := make([]byte, DedupBlockSize)
	for i := range blockA {
		blockA[i] = byte(i*7 + 1)
	}
	blockB := make([]byte, DedupBlockSize)
	copy(blockB, blockA)

	// Combine into contiguous buffer of 2 blocks
	src := append(blockA, blockB...)

	hA := Hash64KBlock(src[:DedupBlockSize])
	table.Insert(hA, 0)

	// Probe block 1 against block 0
	hB := Hash64KBlock(src[DedupBlockSize:])
	if hA != hB {
		t.Fatalf("hashes of identical blocks must match: %016x != %016x", hA, hB)
	}

	matchedIdx, found := table.Probe(hB, src, DedupBlockSize)
	if !found {
		t.Fatalf("expected duplicate block to be found")
	}
	if matchedIdx != 0 {
		t.Fatalf("expected matched block index 0, got %d", matchedIdx)
	}

	// Corrupt single byte in block 1: must NOT be accepted as duplicate
	src[DedupBlockSize+42] ^= 0xFF
	hCorrupt := Hash64KBlock(src[DedupBlockSize:])
	_, foundCorrupt := table.Probe(hCorrupt, src, DedupBlockSize)
	if foundCorrupt {
		t.Fatalf("corrupted block must not be accepted as duplicate")
	}

	// Reset table
	table.Reset()
	_, foundAfterReset := table.Probe(hB, src, DedupBlockSize)
	if foundAfterReset {
		t.Fatalf("table probe must return false after reset")
	}
}

func TestDedup_EmitOpCopyBlock(t *testing.T) {
	// Construct 3 blocks: block 0, block 1 == block 0, block 2 == block 0
	pattern := make([]byte, DedupBlockSize)
	for i := range pattern {
		pattern[i] = byte((i * 13) ^ (i >> 8))
	}

	var data []byte
	data = append(data, pattern...)
	data = append(data, pattern...)
	data = append(data, pattern...)

	for _, level := range []int{1, 3, 5} {
		comp, err := Compress(data, level)
		if err != nil {
			t.Fatalf("level %d compress failed: %v", level, err)
		}

		// Verify that OpCopyBlock (0xFD) was emitted in Section 2
		hdr, err := ParseFrameHeader(comp)
		if err != nil {
			t.Fatalf("parse header failed: %v", err)
		}

		// Decompress Section 2 commands
		sec2Offset := hdr.DataOffset + 8 // Section 1 is 8 bytes
		cmdSectionRaw := comp[sec2Offset : sec2Offset+hdr.CmdStreamSize]
		var scratchTable CanonicalHuffmanTable
		cmds := make([]byte, 1024*1024)
		nCmds, err := UnpackStream(cmds, cmdSectionRaw, &scratchTable)
		if err != nil {
			t.Fatalf("unpack commands failed: %v", err)
		}
		cmds = cmds[:nCmds]

		// Check the last 10 bytes of cmds for OpCopyBlock
		if len(cmds) < 10 {
			t.Fatalf("cmds too short: %d bytes, hex: %x", len(cmds), cmds)
		}
		t.Logf("level %d nCmds=%d, last 10 bytes: %x", level, len(cmds), cmds[len(cmds)-10:])
		foundOpCopyBlock := false
		for i := 0; i <= len(cmds)-5; i++ {
			if cmds[i] == OpCopyBlock {
				targetBlock := binary.LittleEndian.Uint32(cmds[i+1 : i+5])
				if targetBlock == 0 {
					foundOpCopyBlock = true
					break
				}
			}
		}
		if !foundOpCopyBlock {
			t.Errorf("level %d: expected OpCopyBlock (0xFD) with targetBlock 0 in command stream, cmds: %x", level, cmds)
		}

		// Verify lossless roundtrip
		decomp, err := Decompress(comp)
		if err != nil {
			t.Fatalf("level %d decompress failed: %v", level, err)
		}
		if !bytes.Equal(decomp, data) {
			t.Fatalf("level %d roundtrip mismatch", level)
		}
	}
}

func TestDedup_ZeroAllocationDecompress(t *testing.T) {
	block := make([]byte, DedupBlockSize)
	for i := range block {
		block[i] = byte(i*17 + 3)
	}
	var data []byte
	for range 4 {
		data = append(data, block...)
	}

	comp, err := Compress(data, 1)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)
	// Warmup
	if _, err := DecompressTo(comp, dst); err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}

	allocs := testing.AllocsPerRun(20, func() {
		_, err := DecompressTo(comp, dst)
		if err != nil {
			t.Fatalf("DecompressTo error: %v", err)
		}
	})

	if allocs != 0.0 {
		t.Fatalf("expected 0.0 allocs/run on deduplicated frame, got %f", allocs)
	}
}

func TestDedup_CorruptedOpCopyBlockRejection(t *testing.T) {
	// Construct a minimal valid PRZM frame with 2 blocks (128 KiB)
	block := make([]byte, DedupBlockSize)
	for i := range block {
		block[i] = 0xAA
	}
	data := append(block, block...)
	comp, err := Compress(data, 1)
	if err != nil {
		t.Fatalf("compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)

	// 1. Truncate payload after OpCopyBlock
	if _, err := ParseFrameHeader(comp); err != nil {
		t.Fatalf("parse header failed: %v", err)
	}

	// 2. Synthesize manual corrupted frames
	// Frame Header: 8 bytes
	// Flags = FlagExtOffset
	// uSize = 131072 (2 blocks)
	makeFrame := func(cmdBytes []byte) []byte {
		var f bytes.Buffer
		binary.Write(&f, binary.LittleEndian, MagicPRZM)
		f.WriteByte(FlagExtOffset) // Flags
		f.WriteByte(0x00)          // uSize low
		f.WriteByte(0x00)          // uSize mid (131072 = 0x020000)
		f.WriteByte(0x02)          // uSize high

		// Section Dir: 6 bytes (cmdLen 3 bytes, offLen 3 bytes)
		// Pack cmdBytes as ModeFlat
		packedCmds, _ := PackStream(cmdBytes, ModeFlat)
		packedOffs, _ := PackStream([]byte{}, ModeFlat)

		cmdLen := len(packedCmds)
		offLen := len(packedOffs)
		f.WriteByte(byte(cmdLen))
		f.WriteByte(byte(cmdLen >> 8))
		f.WriteByte(byte(cmdLen >> 16))
		f.WriteByte(byte(offLen))
		f.WriteByte(byte(offLen >> 8))
		f.WriteByte(byte(offLen >> 16))

		// Section 1: 8 bytes
		var sec1 [8]byte
		sec1[1] = 16 // WindowLog
		f.Write(sec1[:])

		// Section 2: cmds
		f.Write(packedCmds)
		// Section 3: offs
		f.Write(packedOffs)
		// Section 4: lits (65536 bytes of 0xAA for block 0)
		f.Write(block)

		return f.Bytes()
	}

	// Test A: Truncated OpCopyBlock (only 2 bytes payload instead of 4)
	truncCmd := []byte{
		0xF1, 0x80, 0x80, 0x04, // RLE 65536
		OpCopyBlock, 0x00, 0x00, // Truncated: only 2 bytes!
	}
	frameTrunc := makeFrame(truncCmd)
	_, err = DecompressTo(frameTrunc, dst)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Errorf("expected ErrCorruptedFrame for truncated OpCopyBlock, got %v", err)
	}

	// Test B: Forward Block Reference (referencing block 1 or 2 before written)
	fwdCmd := []byte{
		0xF1, 0x80, 0x80, 0x04, // RLE 65536
		OpCopyBlock, 0x01, 0x00, 0x00, 0x00, // Points to block 1 (self-reference / forward!)
	}
	frameFwd := makeFrame(fwdCmd)
	_, err = DecompressTo(frameFwd, dst)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Errorf("expected ErrCorruptedFrame for forward reference, got %v", err)
	}

	// Test C: Out-of-bounds Block Index (block 999)
	oobCmd := []byte{
		0xF1, 0x80, 0x80, 0x04, // RLE 65536
		OpCopyBlock, 0xE7, 0x03, 0x00, 0x00, // block 999
	}
	frameOOB := makeFrame(oobCmd)
	_, err = DecompressTo(frameOOB, dst)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Errorf("expected ErrCorruptedFrame for OOB block index, got %v", err)
	}
}
