// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"unsafe"
)

// Inter-chunk framing flags for Tensor-Continuous Structural Induction (T_CSI)
const (
	FlagChunkIsolated uint8 = 0x00 // Independent block: full grammar & state reset
	FlagChunkInherit  uint8 = 0x01 // Inherit grammar from previous chunk (0 rules in header)
	FlagChunkExtPoly  uint8 = 0x02 // Extend active Galois polynomial across boundary
)

var (
	ErrInterChunkCorrupted    = errors.New("przm/interchunk: corrupted stream")
	ErrInterChunkSizeMismatch = errors.New("przm/interchunk: decompressed size mismatch")
)

// InterChunkContext encapsulates the persistent cross-chunk state C = <G, S, P>.
// Total memory footprint is strictly <= 256 bytes.
type InterChunkContext struct {
	Grammar    GrammarH_DAG
	HasGrammar bool
	VANSState  [4]uint32
	ActivePoly GFPIMatch
	HasPoly    bool
}

// Reset clears the inter-chunk state on context switches or high entropy divergence.
func (c *InterChunkContext) Reset() {
	c.Grammar = GrammarH_DAG{}
	c.HasGrammar = false
	c.VANSState = [4]uint32{}
	c.HasPoly = false
}

// MemorySize returns the exact resident memory footprint of the carried state.
func (c *InterChunkContext) MemorySize() int {
	return len(c.Grammar.L1)*2 + len(c.Grammar.L2)*2 + len(c.Grammar.L3)*2 + 16 + 8
}

// EncodeChunkStream compresses a continuous stream of chunks using inter-chunk state carry (T_CSI).
func EncodeChunkStream(chunks [][]byte) ([][]byte, error) {
	if len(chunks) == 0 {
		return nil, nil
	}

	frames := make([][]byte, len(chunks))
	var ctx InterChunkContext

	for k, chunk := range chunks {
		if len(chunk) == 0 {
			frames[k] = []byte{ggtcMagic0, ggtcMagic1, ggtcVer1, FlagChunkIsolated, 0, 0, 0, 0}
			continue
		}

		// Check if we can inherit the grammar from the previous chunk
		canInherit := false
		var inheritedFrame []byte

		if ctx.HasGrammar && len(ctx.Grammar.L1) > 0 {
			candTokens := substituteGrammar(chunk, &ctx.Grammar)
			reduction := float64(len(chunk)) / float64(len(candTokens))
			if reduction >= 1.25 {
				inheritedFrame = packInheritedFrame(chunk, candTokens)
				canInherit = true
			}
		}

		// Re-induce grammar for chunk 0 or compare against fresh
		gFresh, indTokens := InduceGrammar(chunk)
		freshFrame := packFullFrame(chunk, gFresh, indTokens)

		if canInherit && len(inheritedFrame) <= len(freshFrame)+32 {
			frames[k] = inheritedFrame
		} else {
			ctx.Grammar = *gFresh
			ctx.HasGrammar = true
			frames[k] = freshFrame
		}
	}

	return frames, nil
}

// DecodeChunkStream decompresses a sequence of T_CSI frames back to original chunks.
func DecodeChunkStream(frames [][]byte) ([][]byte, error) {
	if len(frames) == 0 {
		return nil, nil
	}

	chunks := make([][]byte, len(frames))
	var ctx InterChunkContext

	for k, frame := range frames {
		if len(frame) < 8 {
			return nil, ErrInterChunkCorrupted
		}

		if frame[0] != ggtcMagic0 || frame[1] != ggtcMagic1 || frame[2] != ggtcVer1 {
			return nil, fmt.Errorf("%w: invalid signature", ErrInterChunkCorrupted)
		}

		flags := frame[3]
		if flags&FlagChunkInherit != 0 {
			// Zero-overhead inherited grammar decoding:
			if !ctx.HasGrammar {
				return nil, fmt.Errorf("%w: chunk requests grammar inheritance but context is empty", ErrInterChunkCorrupted)
			}
			chunk, err := decodeInheritedFrame(frame, &ctx.Grammar)
			if err != nil {
				return nil, err
			}
			chunks[k] = chunk
		} else {
			// Full frame with grammar definition:
			chunk, g, err := decodeFullFrame(frame)
			if err != nil {
				return nil, err
			}
			ctx.Grammar = *g
			ctx.HasGrammar = true
			chunks[k] = chunk
		}
	}

	return chunks, nil
}

// substituteGrammar applies an existing pre-warmed grammar to raw bytes.
// substituteGrammar applies an existing pre-warmed grammar to raw bytes using
// greedy longest-match-first matching (8B -> 4B -> 2B -> 1B).
func substituteGrammar(src []byte, g *GrammarH_DAG) []uint16 {
	n := len(src)
	outTokens := make([]uint16, 0, n)

	numL3 := len(g.L3)
	numL2 := len(g.L2)
	numL1 := len(g.L1)

	var l2Map map[uint32]uint16
	if numL2 > 0 {
		l2Map = make(map[uint32]uint16, numL2)
		for id := 0; id < numL2; id++ {
			l2Map[g.ExpL2[id]] = uint16(id)
		}
	}

	var l1Map map[uint16]uint16
	if numL1 > 0 {
		l1Map = make(map[uint16]uint16, numL1)
		for id := 0; id < numL1; id++ {
			l1Map[g.ExpL1[id]] = uint16(id)
		}
	}

	i := 0
	for i < n {
		// 1. Try Layer 3 (8 bytes = uint64)
		if numL3 > 0 && i+8 <= n {
			w8 := binary.LittleEndian.Uint64(src[i : i+8])
			matched := false
			for id := 0; id < numL3; id++ {
				if g.ExpL3[id] == w8 {
					outTokens = append(outTokens, 1024+uint16(id))
					i += 8
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}

		// 2. Try Layer 2 (4 bytes = uint32)
		if numL2 > 0 && i+4 <= n {
			w4 := binary.LittleEndian.Uint32(src[i : i+4])
			if id, ok := l2Map[w4]; ok {
				outTokens = append(outTokens, 512+id)
				i += 4
				continue
			}
		}

		// 3. Try Layer 1 (2 bytes = uint16)
		if numL1 > 0 && i+2 <= n {
			w2 := binary.LittleEndian.Uint16(src[i : i+2])
			if id, ok := l1Map[w2]; ok {
				outTokens = append(outTokens, 256+id)
				i += 2
				continue
			}
		}

		// 4. Raw terminal
		outTokens = append(outTokens, uint16(src[i]))
		i++
	}

	return outTokens
}

func packInheritedFrame(src []byte, tokens []uint16) []byte {
	// Inherited frame header:
	// Magic(2B) | Ver(1B) | Flags=0x01(1B) | OrigSize(4B) | AxiomLen(4B) [NO RULES TABLES!]
	out := make([]byte, 0, 12+len(tokens))
	out = append(out, ggtcMagic0, ggtcMagic1, ggtcVer1, FlagChunkInherit)

	var szBuf [8]byte
	binary.LittleEndian.PutUint32(szBuf[0:4], uint32(len(src)))
	binary.LittleEndian.PutUint32(szBuf[4:8], uint32(len(tokens)))
	out = append(out, szBuf[:]...)

	// Axiom token stream
	out = emitAxiomTokens(out, tokens)
	return out
}

func packFullFrame(src []byte, g *GrammarH_DAG, tokens []uint16) []byte {
	rulesHeaderSize := 16 + len(g.L1)*2 + len(g.L2)*2 + len(g.L3)*2
	out := make([]byte, 0, rulesHeaderSize+len(tokens))

	out = append(out, ggtcMagic0, ggtcMagic1, ggtcVer1, FlagChunkIsolated)
	var szBuf [8]byte
	binary.LittleEndian.PutUint32(szBuf[0:4], uint32(len(src)))
	binary.LittleEndian.PutUint32(szBuf[4:8], uint32(len(tokens)))
	out = append(out, szBuf[:]...)

	out = append(out, uint8(len(g.L1)), uint8(len(g.L2)), uint8(len(g.L3)), 0x00)

	for _, r := range g.L1 {
		out = append(out, r.U, r.V)
	}
	for _, r := range g.L2 {
		out = append(out, r.L1A, r.L1B)
	}
	for _, r := range g.L3 {
		out = append(out, r.L2A, r.L2B)
	}

	out = emitAxiomTokens(out, tokens)
	return out
}

func emitAxiomTokens(out []byte, tokens []uint16) []byte {
	for _, tok := range tokens {
		if tok < 256 {
			b := byte(tok)
			if b < 0xD0 {
				out = append(out, b)
			} else {
				out = append(out, 0xFF, b)
			}
		} else if tok < 512 {
			id := uint8(tok - 256)
			if id < 16 {
				out = append(out, 0xD0+id)
			} else {
				out = append(out, 0xFC, id)
			}
		} else if tok < 1024 {
			id := uint8(tok - 512)
			if id < 16 {
				out = append(out, 0xE0+id)
			} else {
				out = append(out, 0xFD, id)
			}
		} else {
			id := uint8(tok - 1024)
			if id < 12 {
				out = append(out, 0xF0+id)
			} else {
				out = append(out, 0xFE, id)
			}
		}
	}
	return out
}

func decodeInheritedFrame(src []byte, g *GrammarH_DAG) ([]byte, error) {
	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	axiomStream := src[12:]

	dst := make([]byte, origSize+16)
	dstPtr := unsafe.Pointer(&dst[0])
	pDst := dstPtr
	axPtr := 0
	axEnd := len(axiomStream)

	for axPtr < axEnd {
		op := axiomStream[axPtr]
		axPtr++

		if op < 0xD0 {
			*(*byte)(pDst) = op
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op >= 0xF0 && op <= 0xFB {
			id := int(op - 0xF0)
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		if op >= 0xE0 && op <= 0xEF {
			id := int(op - 0xE0)
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op >= 0xD0 && op <= 0xDF {
			id := int(op - 0xD0)
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		if op == 0xFF {
			raw := axiomStream[axPtr]
			axPtr++
			*(*byte)(pDst) = raw
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op == 0xFE {
			id := int(axiomStream[axPtr])
			axPtr++
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		if op == 0xFD {
			id := int(axiomStream[axPtr])
			axPtr++
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op == 0xFC {
			id := int(axiomStream[axPtr])
			axPtr++
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}
	}

	decodedSize := int(uintptr(pDst) - uintptr(dstPtr))
	if decodedSize != origSize {
		return nil, ErrInterChunkSizeMismatch
	}

	return dst[:origSize], nil
}

func decodeFullFrame(src []byte) ([]byte, *GrammarH_DAG, error) {
	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	numL1 := int(src[12])
	numL2 := int(src[13])
	numL3 := int(src[14])

	ptr := 16
	rulesSize := numL1*2 + numL2*2 + numL3*2
	if ptr+rulesSize > len(src) {
		return nil, nil, ErrInterChunkCorrupted
	}

	g := &GrammarH_DAG{}
	g.L1 = make([]RuleL1, numL1)
	for i := 0; i < numL1; i++ {
		g.L1[i] = RuleL1{U: src[ptr], V: src[ptr+1]}
		ptr += 2
	}
	g.L2 = make([]RuleL2, numL2)
	for i := 0; i < numL2; i++ {
		g.L2[i] = RuleL2{L1A: src[ptr], L1B: src[ptr+1]}
		ptr += 2
	}
	g.L3 = make([]RuleL3, numL3)
	for i := 0; i < numL3; i++ {
		g.L3[i] = RuleL3{L2A: src[ptr], L2B: src[ptr+1]}
		ptr += 2
	}

	g.BuildExpansionTables()

	axiomStream := src[ptr:]
	dst := make([]byte, origSize+16)
	dstPtr := unsafe.Pointer(&dst[0])
	pDst := dstPtr
	axPtr := 0
	axEnd := len(axiomStream)

	for axPtr < axEnd {
		op := axiomStream[axPtr]
		axPtr++

		if op < 0xD0 {
			*(*byte)(pDst) = op
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op >= 0xF0 && op <= 0xFB {
			id := int(op - 0xF0)
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		if op >= 0xE0 && op <= 0xEF {
			id := int(op - 0xE0)
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op >= 0xD0 && op <= 0xDF {
			id := int(op - 0xD0)
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		if op == 0xFF {
			raw := axiomStream[axPtr]
			axPtr++
			*(*byte)(pDst) = raw
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		if op == 0xFE {
			id := int(axiomStream[axPtr])
			axPtr++
			*(*uint64)(pDst) = g.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		if op == 0xFD {
			id := int(axiomStream[axPtr])
			axPtr++
			*(*uint32)(pDst) = g.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		if op == 0xFC {
			id := int(axiomStream[axPtr])
			axPtr++
			*(*uint16)(pDst) = g.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}
	}

	decodedSize := int(uintptr(pDst) - uintptr(dstPtr))
	if decodedSize != origSize {
		return nil, nil, ErrInterChunkSizeMismatch
	}

	return dst[:origSize], g, nil
}

// DecodeFullFrameForBench exports decodeFullFrame for benchmark testing.
func DecodeFullFrameForBench(src []byte) ([]byte, *GrammarH_DAG, error) {
	return decodeFullFrame(src)
}

// DecodeInheritedFrameForBench exports decodeInheritedFrame for benchmark testing.
func DecodeInheritedFrameForBench(src []byte, g *GrammarH_DAG) ([]byte, error) {
	return decodeInheritedFrame(src, g)
}

// DefaultStreamChunkSize defines the default chunk block size (64 KiB) for streaming.
const DefaultStreamChunkSize = 64 * 1024

// CompressStream streams uncompressed data from r to w using continuous structural induction (T_CSI).
// Each 64 KiB chunk inherits the grammar of the previous chunk with 0 rules in the header.
func CompressStream(r io.Reader, w io.Writer) error {
	buf := make([]byte, DefaultStreamChunkSize)
	var chunks [][]byte
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			chunk := make([]byte, n)
			copy(chunk, buf[:n])
			chunks = append(chunks, chunk)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
	}

	frames, err := EncodeChunkStream(chunks)
	if err != nil {
		return err
	}

	var countBuf [4]byte
	binary.LittleEndian.PutUint32(countBuf[:], uint32(len(frames)))
	if _, err := w.Write(countBuf[:]); err != nil {
		return err
	}

	var lenBuf [4]byte
	for _, frame := range frames {
		binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(frame)))
		if _, err := w.Write(lenBuf[:]); err != nil {
			return err
		}
		if _, err := w.Write(frame); err != nil {
			return err
		}
	}
	return nil
}

// DecompressStream reads an inter-chunk stream from r and writes uncompressed bytes to w.
func DecompressStream(r io.Reader, w io.Writer) error {
	var countBuf [4]byte
	if _, err := io.ReadFull(r, countBuf[:]); err != nil {
		return err
	}
	numFrames := int(binary.LittleEndian.Uint32(countBuf[:]))

	frames := make([][]byte, numFrames)
	var lenBuf [4]byte
	for i := 0; i < numFrames; i++ {
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			return err
		}
		fLen := int(binary.LittleEndian.Uint32(lenBuf[:]))
		frame := make([]byte, fLen)
		if _, err := io.ReadFull(r, frame); err != nil {
			return err
		}
		frames[i] = frame
	}

	chunks, err := DecodeChunkStream(frames)
	if err != nil {
		return err
	}

	for _, chunk := range chunks {
		if _, err := w.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}
