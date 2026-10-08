// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
)

var (
	pngSignature = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	ErrNotPNG    = errors.New("przm: invalid PNG container signature or format")
)

// isPNG returns true if src starts with the canonical 8-byte PNG signature.
func isPNG(src []byte) bool {
	return len(src) >= 8 && bytes.Equal(src[:8], pngSignature)
}

// PNGMetadata holds parsed container headers, chunk descriptors, and CRCs for verbatim reconstruction.
type PNGMetadata struct {
	PrefixBytes  []byte
	PostfixBytes []byte
	IDATLengths  []uint32
	IDATCRCs     []uint32
	IsZlib       bool
}

// parsePNGContainer parses a PNG bitstream into its container metadata and concatenated IDAT payload.
func parsePNGContainer(src []byte) (*PNGMetadata, []byte, error) {
	if !isPNG(src) {
		return nil, nil, ErrNotPNG
	}

	pos := 8
	firstIDATPos := -1
	lastIDATPos := -1

	var idatLens []uint32
	var idatCRCs []uint32
	var idatBuf bytes.Buffer

	for pos+8 <= len(src) {
		chunkLen := binary.BigEndian.Uint32(src[pos : pos+4])
		chunkType := string(src[pos+4 : pos+8])
		chunkTotal := 12 + int(chunkLen)

		if pos+chunkTotal > len(src) {
			return nil, nil, fmt.Errorf("%w: truncated chunk %s", ErrCorruptedFrame, chunkType)
		}

		chunkData := src[pos+8 : pos+8+int(chunkLen)]
		chunkCRC := binary.BigEndian.Uint32(src[pos+8+int(chunkLen) : pos+chunkTotal])

		if chunkType == "IDAT" {
			if firstIDATPos < 0 {
				firstIDATPos = pos
			}
			lastIDATPos = pos + chunkTotal
			idatLens = append(idatLens, chunkLen)
			idatCRCs = append(idatCRCs, chunkCRC)
			idatBuf.Write(chunkData)
		} else if firstIDATPos >= 0 && lastIDATPos == pos {
			// Non-IDAT chunk encountered after IDAT; stop IDAT scanning
			break
		}

		pos += chunkTotal
	}

	if firstIDATPos < 0 || len(idatLens) == 0 {
		return nil, nil, fmt.Errorf("%w: no IDAT chunks found", ErrNotPNG)
	}

	prefixBytes := src[:firstIDATPos]
	postfixBytes := src[lastIDATPos:]

	// Verify prefix contains IHDR
	if !bytes.Contains(prefixBytes, []byte("IHDR")) {
		return nil, nil, fmt.Errorf("%w: missing IHDR chunk", ErrNotPNG)
	}

	// Verify postfix contains IEND
	if !bytes.Contains(postfixBytes, []byte("IEND")) {
		return nil, nil, fmt.Errorf("%w: missing IEND chunk", ErrNotPNG)
	}

	idatPayload := idatBuf.Bytes()
	isZlib := len(idatPayload) >= 6 &&
		(uint(idatPayload[0])*256+uint(idatPayload[1]))%31 == 0 &&
		(idatPayload[0]&0x0F) == 8

	meta := &PNGMetadata{
		PrefixBytes:  prefixBytes,
		PostfixBytes: postfixBytes,
		IDATLengths:  idatLens,
		IDATCRCs:     idatCRCs,
		IsZlib:       isZlib,
	}

	return meta, idatPayload, nil
}

// CompressArchivalPNG compresses a PNG image by isolating IDAT scanlines and wrapping with ArchivalPNGMagic.
func CompressArchivalPNG(src []byte, level int) ([]byte, error) {
	meta, idatPayload, err := parsePNGContainer(src)
	if err != nil {
		return nil, err
	}

	innerLevel := level
	if innerLevel >= ModeArchival || innerLevel <= 0 {
		innerLevel = 3
	}

	// Compress IDAT scanline stream using standard PRZM stream format without experimental formats
	innerPRZM, err := CompressWithOptions(idatPayload, CompressOptions{
		Level:              innerLevel,
		WindowLog:          WindowLog8M,
		StandardStreamOnly: true,
	})
	if err != nil {
		return nil, err
	}

	idatCount := len(meta.IDATLengths)
	prefixLen := len(meta.PrefixBytes)
	postfixLen := len(meta.PostfixBytes)

	if prefixLen > 65535 || postfixLen > 65535 || idatCount > 65535 {
		return nil, fmt.Errorf("%w: metadata exceeds 16-bit limits", ErrExceedsMaxBlock)
	}

	metaSize := 8 + idatCount*8 + prefixLen + postfixLen
	totalSize := 12 + metaSize + len(innerPRZM)

	out := make([]byte, totalSize)

	// Archival PNG Container Header
	binary.LittleEndian.PutUint32(out[0:4], ArchivalPNGMagic)
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(src)))
	binary.LittleEndian.PutUint32(out[8:12], uint32(metaSize))

	// Metadata Block
	metaBuf := out[12 : 12+metaSize]
	metaBuf[0] = 0x50 // Magic 'P'
	if meta.IsZlib {
		metaBuf[1] = 0x01
	} else {
		metaBuf[1] = 0x00
	}
	binary.LittleEndian.PutUint16(metaBuf[2:4], uint16(prefixLen))
	binary.LittleEndian.PutUint16(metaBuf[4:6], uint16(postfixLen))
	binary.LittleEndian.PutUint16(metaBuf[6:8], uint16(idatCount))

	descPos := 8
	for i := range idatCount {
		binary.LittleEndian.PutUint32(metaBuf[descPos:descPos+4], meta.IDATLengths[i])
		binary.BigEndian.PutUint32(metaBuf[descPos+4:descPos+8], meta.IDATCRCs[i])
		descPos += 8
	}

	copy(metaBuf[descPos:descPos+prefixLen], meta.PrefixBytes)
	copy(metaBuf[descPos+prefixLen:descPos+prefixLen+postfixLen], meta.PostfixBytes)

	// Append inner compressed PRZM frame
	copy(out[12+metaSize:], innerPRZM)

	return out, nil
}

// DecompressArchivalPNGTo decompresses an ArchivalPNGMagic container frame into dst with strictly 0 heap allocs.
func DecompressArchivalPNGTo(src []byte, dst []byte) ([]byte, error) {
	if len(src) < 12 {
		return nil, ErrCorruptedFrame
	}

	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != ArchivalPNGMagic {
		return nil, ErrInvalidMagic
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	metaSize := int(binary.LittleEndian.Uint32(src[8:12]))
	if 12+metaSize > len(src) || metaSize < 8 {
		return nil, ErrCorruptedFrame
	}

	metaBuf := src[12 : 12+metaSize]
	if metaBuf[0] != 0x50 {
		return nil, ErrCorruptedFrame
	}

	prefixLen := int(binary.LittleEndian.Uint16(metaBuf[2:4]))
	postfixLen := int(binary.LittleEndian.Uint16(metaBuf[4:6]))
	idatCount := int(binary.LittleEndian.Uint16(metaBuf[6:8]))

	expectedMeta := 8 + idatCount*8 + prefixLen + postfixLen
	if metaSize != expectedMeta {
		return nil, ErrCorruptedFrame
	}

	if dst == nil || cap(dst) < origLen+64 {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	// Decompress inner PRZM stream using pooled scratchpad (0 heap allocs)
	pad := decScratchPool.Get()

	innerSrc := src[12+metaSize:]
	if cap(pad.scanlineBuf) < origLen+64 {
		pad.scanlineBuf = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	innerBytes, err := DecompressTo(innerSrc, pad.scanlineBuf)
	if err != nil {
		decScratchPool.Put(pad)
		return nil, err
	}

	// Reconstruct PNG container into dst
	dstPos := 0
	descPos := 8
	prefixPos := 8 + idatCount*8
	postfixPos := prefixPos + prefixLen

	// 1. Prefix (PNG magic + IHDR + ancillary chunks)
	if prefixLen > 0 {
		if dstPos+prefixLen > origLen {
			decScratchPool.Put(pad)
			return nil, ErrCorruptedFrame
		}
		copy(dst[dstPos:dstPos+prefixLen], metaBuf[prefixPos:prefixPos+prefixLen])
		dstPos += prefixLen
	}

	// 2. IDAT Chunks
	innerPos := 0
	for range idatCount {
		chunkLen := int(binary.LittleEndian.Uint32(metaBuf[descPos : descPos+4]))
		chunkCRC := binary.BigEndian.Uint32(metaBuf[descPos+4 : descPos+8])
		descPos += 8

		if innerPos+chunkLen > len(innerBytes) || dstPos+12+chunkLen > origLen {
			decScratchPool.Put(pad)
			return nil, ErrCorruptedFrame
		}

		binary.BigEndian.PutUint32(dst[dstPos:dstPos+4], uint32(chunkLen))
		dstPos += 4
		dst[dstPos] = 'I'
		dst[dstPos+1] = 'D'
		dst[dstPos+2] = 'A'
		dst[dstPos+3] = 'T'
		dstPos += 4
		copy(dst[dstPos:dstPos+chunkLen], innerBytes[innerPos:innerPos+chunkLen])
		dstPos += chunkLen
		innerPos += chunkLen
		binary.BigEndian.PutUint32(dst[dstPos:dstPos+4], chunkCRC)
		dstPos += 4
	}

	if innerPos != len(innerBytes) {
		decScratchPool.Put(pad)
		return nil, ErrCorruptedFrame
	}

	// 3. Postfix (ancillary chunks + IEND)
	if postfixLen > 0 {
		if dstPos+postfixLen > origLen {
			decScratchPool.Put(pad)
			return nil, ErrCorruptedFrame
		}
		copy(dst[dstPos:dstPos+postfixLen], metaBuf[postfixPos:postfixPos+postfixLen])
		dstPos += postfixLen
	}

	if dstPos != origLen {
		decScratchPool.Put(pad)
		return nil, ErrCorruptedFrame
	}

	decScratchPool.Put(pad)
	return dst[:origLen], nil
}

// unpackArchivalPNG decompresses an Archival PNG container bitstream into dst with strictly 0 heap allocations.
func unpackArchivalPNG(src []byte, dst []byte) ([]byte, error) {
	return DecompressArchivalPNGTo(src, dst)
}

// ============================================================================
// Approximate Matching Engine (Hamming <= 2 Pigeonhole Sub-Chunk Indexer)
// ============================================================================

const (
	ApproxTableSize = 65536
	ApproxTableMask = ApproxTableSize - 1
)

type approxEntry struct {
	pos   int32  // 1-based pos in src (0 means empty)
	chunk uint32 // 4-byte sub-chunk value
}

// ApproxSubChunkIndex implements 4-way pigeonhole sub-chunk candidate indexing for 16-byte blocks.
type ApproxSubChunkIndex struct {
	tables [4][ApproxTableSize]approxEntry
}

var approxPool = generic.NewPool(func() *ApproxSubChunkIndex {
	return &ApproxSubChunkIndex{}
})

func (idx *ApproxSubChunkIndex) Reset() {
	clear(idx.tables[0][:])
	clear(idx.tables[1][:])
	clear(idx.tables[2][:])
	clear(idx.tables[3][:])
}

func approxHash(v uint32) uint32 {
	return (v * 0x1E35A7BD) >> 16
}

// Insert indexes the 4x 4-byte sub-chunks of the 16-byte block at pos.
func (idx *ApproxSubChunkIndex) Insert(src []byte, pos int) {
	if pos+16 > len(src) {
		return
	}
	p1 := int32(pos + 1)
	for k := range 4 {
		chunk := binary.LittleEndian.Uint32(src[pos+4*k : pos+4*k+4])
		h := approxHash(chunk) & ApproxTableMask
		idx.tables[k][h] = approxEntry{pos: p1, chunk: chunk}
	}
}

// ApproxMatchResult describes a candidate match with Hamming distance <= 2.
type ApproxMatchResult struct {
	Found      bool
	Distance   int
	Length     int
	Mismatches int
	DiffOffs   [2]int
	DiffXORs   [2]byte
	RingK      int // 0, 1, 2 if matches repeat register, else -1
	Cost       int
	Score      int
}

// FindMatch searches for reference blocks with Hamming distance <= 2 using the pigeonhole principle.
func (idx *ApproxSubChunkIndex) FindMatch(
	src []byte,
	pos, bEnd int,
	rep0, rep1, rep2, windowSize int,
	calcOffCost func(int) int,
) ApproxMatchResult {
	if pos+16 > bEnd {
		return ApproxMatchResult{}
	}

	c0 := binary.LittleEndian.Uint32(src[pos : pos+4])
	c1 := binary.LittleEndian.Uint32(src[pos+4 : pos+8])
	c2 := binary.LittleEndian.Uint32(src[pos+8 : pos+12])
	c3 := binary.LittleEndian.Uint32(src[pos+12 : pos+16])
	chunks := [4]uint32{c0, c1, c2, c3}

	var best ApproxMatchResult
	var testedRefs [4]int
	testedCount := 0

	for k := range 4 {
		h := approxHash(chunks[k]) & ApproxTableMask
		ent := idx.tables[k][h]
		if ent.pos == 0 || ent.chunk != chunks[k] {
			continue
		}
		ref := int(ent.pos - 1)
		d := pos - ref
		if d <= 0 || d > windowSize || ref+16 > len(src) {
			continue
		}

		// Avoid re-testing same ref
		alreadyTested := false
		for i := 0; i < testedCount; i++ {
			if testedRefs[i] == ref {
				alreadyTested = true
				break
			}
		}
		if alreadyTested {
			continue
		}
		if testedCount < 4 {
			testedRefs[testedCount] = ref
			testedCount++
		}

		// Pigeonhole Principle: Hamming distance <= 2 requires >= 2 matching 4-byte sub-chunks
		matchingSubChunks := 1 // slot k already matches
		for j := range 4 {
			if j != k {
				refChunk := binary.LittleEndian.Uint32(src[ref+4*j : ref+4*j+4])
				if refChunk == chunks[j] {
					matchingSubChunks++
				}
			}
		}
		if matchingSubChunks < 2 {
			continue
		}

		// Fast SWAR check
		xor0 := binary.LittleEndian.Uint64(src[pos:pos+8]) ^ binary.LittleEndian.Uint64(src[ref:ref+8])
		xor1 := binary.LittleEndian.Uint64(src[pos+8:pos+16]) ^ binary.LittleEndian.Uint64(src[ref+8:ref+16])
		if xor0 == 0 && xor1 == 0 {
			// Exact match: standard LZ77 handles exact matches without delta mask overhead
			continue
		}

		mismatches := 0
		var diffOffs [2]int
		var diffXORs [2]byte
		exceeded := false

		for b := range 16 {
			sb := src[pos+b]
			rb := src[ref+b]
			if sb != rb {
				if mismatches == 2 {
					exceeded = true
					break
				}
				diffOffs[mismatches] = b
				diffXORs[mismatches] = sb ^ rb
				mismatches++
			}
		}
		if exceeded || mismatches == 0 {
			continue
		}

		// Forward extension as long as subsequent bytes match exactly
		mLen := 16
		for pos+mLen < bEnd && ref+mLen < len(src) && src[pos+mLen] == src[ref+mLen] && mLen < 256 {
			mLen++
		}

		ringK := -1
		if d == rep0 {
			ringK = 0
		} else if d == rep1 {
			ringK = 1
		} else if d == rep2 {
			ringK = 2
		}

		var cost int
		if ringK >= 0 {
			cost = 2 + 1 + mismatches*2
		} else {
			cost = 2 + calcOffCost(d) + 1 + mismatches*2
		}
		score := mLen - cost

		if score > best.Score {
			best = ApproxMatchResult{
				Found:      true,
				Distance:   d,
				Length:     mLen,
				Mismatches: mismatches,
				DiffOffs:   diffOffs,
				DiffXORs:   diffXORs,
				RingK:      ringK,
				Cost:       cost,
				Score:      score,
			}
		}
	}

	return best
}
