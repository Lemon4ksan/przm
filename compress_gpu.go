// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"

	"github.com/lemon4ksan/foundation/generic"

	"github.com/lemon4ksan/przm/gpu"
)

// tryCompressGPU offloads LZ77 match search to an NVIDIA Ampere+ GPU via the CUDA Driver API.
// If GPU acceleration is enabled, available, and yields valid compression smaller than raw uncompressed size,
// it returns the canonical PRZM frame and true. Otherwise, it gracefully returns nil, false for CPU fallback.
func tryCompressGPU(src []byte, opts CompressOptions, pad *scratchpad) (out []byte, ok bool) {
	if !opts.GPUAcceleration || !gpu.IsAvailable() || len(src) == 0 {
		return nil, false
	}
	if len(src) > MaxBlockSize || opts.Dict != nil {
		return nil, false
	}

	defer func() {
		if r := recover(); r != nil {
			out = nil
			ok = false
		}
	}()

	comp, err := gpu.GetGlobalCompressor()
	if err != nil || comp == nil {
		return nil, false
	}

	// 1. Autonomous Device-to-Device (D2D) GPU Compression Pipeline
	// Offloads Ingress -> Radix Match -> Fused Warp Tokenizer -> D2D Entropy Encoding -> Egress to GPU
	if !opts.ArchivalMode && opts.Level <= 3 && len(src) >= 4096 {
		frame, _, err := comp.CompressD2D(src)
		if err == nil && len(frame) > 0 && len(frame) < len(src) {
			return frame, true
		}
	}

	matches, _, err := comp.MatchLZ77(src)
	if err != nil {
		return nil, false
	}

	uSize := len(src)
	pad.cmdBuf = pad.cmdBuf[:0]
	pad.offsetBuf = pad.offsetBuf[:0]
	pad.literalBuf = pad.literalBuf[:0]

	flushLiterals := func(lits []byte) {
		nLits := len(lits)
		if nLits == 0 {
			return
		}
		if nLits <= 8 {
			pad.cmdBuf = append(pad.cmdBuf, 0xF2+byte(nLits))
		} else if nLits < 0x80 {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral, byte(nLits))
		} else if nLits < 0x4000 {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral, byte(nLits|0x80), byte(nLits>>7))
		} else {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral)
			var varBuf [8]byte
			n := binary.PutUvarint(varBuf[:], uint64(nLits))
			pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
		}
		pad.literalBuf = append(pad.literalBuf, lits...)
	}

	writeOffset := func(d int) {
		if d < 0x80 {
			pad.offsetBuf = append(pad.offsetBuf, byte(d))
		} else if d < 0x4000 {
			pad.offsetBuf = append(pad.offsetBuf, byte(d|0x80), byte(d>>7))
		} else {
			var buf [8]byte
			n := binary.PutUvarint(buf[:], uint64(d))
			pad.offsetBuf = append(pad.offsetBuf, buf[:n]...)
		}
	}

	rep0 := 1
	rep1 := 4
	rep2 := 8

	pos := 0
	litStart := 0
	matchIdx := 0

	for pos < uSize {
		// 1. RLE Check (run of identical bytes >= 12)
		if pos+12 <= uSize && src[pos] == src[pos+1] && src[pos] == src[pos+2] && src[pos] == src[pos+3] {
			rleByte := src[pos]
			rleLen := 4
			for pos+rleLen < uSize && src[pos+rleLen] == rleByte && rleLen < 65535 {
				rleLen++
			}
			if rleLen >= 12 {
				if pos > litStart {
					flushLiterals(src[litStart:pos])
				}
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRLE)
				var varBuf [8]byte
				n := binary.PutUvarint(varBuf[:], uint64(rleLen))
				pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				pad.literalBuf = append(pad.literalBuf, rleByte)

				pos += rleLen
				litStart = pos
				continue
			}
		}

		for matchIdx < len(matches) && matches[matchIdx].Position < uint32(pos) {
			matchIdx++
		}

		if matchIdx < len(matches) && matches[matchIdx].Position == uint32(pos) {
			m := matches[matchIdx]
			d := int(m.Offset)
			mLen := int(m.Length)

			// Lookahead check: does pos+1 have a significantly longer match?
			if matchIdx+1 < len(matches) && matches[matchIdx+1].Position == uint32(pos+1) {
				nextM := matches[matchIdx+1]
				if int(nextM.Length) > mLen+1 {
					pos++
					continue
				}
			}

			if d > 0 && d <= pos && pos+mLen <= uSize && mLen >= 4 &&
				binary.LittleEndian.Uint32(src[pos:pos+4]) == binary.LittleEndian.Uint32(src[pos-d:pos-d+4]) {

				for pos+mLen < uSize && src[pos+mLen] == src[pos-d+mLen] && mLen < 65530 {
					mLen++
				}

				for pos > litStart && pos-d > 0 && src[pos-1] == src[pos-d-1] && mLen < 65530 {
					pos--
					mLen++
				}

				litLen := pos - litStart

				ringK := -1
				if rep0 == d {
					ringK = 0
				} else if rep1 == d {
					ringK = 1
				} else if rep2 == d {
					ringK = 2
				}

				if ringK >= 0 {
					if mLen <= 7 {
						if litLen > 7 {
							flushLiterals(src[litStart : pos-7])
							litStart = pos - 7
							litLen = 7
						}
						op := uint8((litLen << 4) | ((mLen - 4) << 2) | ringK)
						pad.cmdBuf = append(pad.cmdBuf, op)
						if litLen > 0 {
							pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
						}
					} else {
						if litLen > 15 {
							flushLiterals(src[litStart : pos-15])
							litStart = pos - 15
							litLen = 15
						}
						baseLen := mLen - 4
						if baseLen > 14 {
							baseLen = 15
						}
						op := OpcodePrefixLongRepeat | uint8(ringK)
						token := uint8((litLen << 4) | baseLen)
						pad.cmdBuf = append(pad.cmdBuf, op, token)
						if baseLen == 15 {
							var varBuf [8]byte
							n := binary.PutUvarint(varBuf[:], uint64(mLen-19))
							pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
						}
						if litLen > 0 {
							pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
						}
					}
					switch ringK {
					case 0:
					case 1:
						rep1 = rep0
						rep0 = d
					case 2:
						rep2 = rep1
						rep1 = rep0
						rep0 = d
					}
				} else {
					if litLen > 3 {
						flushLiterals(src[litStart : pos-3])
						litStart = pos - 3
						litLen = 3
					}
					baseLen := mLen - 4
					if baseLen > 14 {
						baseLen = 15
					}
					op := OpcodeFlagFusedExt | uint8(litLen<<4) | uint8(baseLen)
					pad.cmdBuf = append(pad.cmdBuf, op)
					if baseLen == 15 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(mLen-19))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					}
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					writeOffset(d)
					rep2 = rep1
					rep1 = rep0
					rep0 = d
				}

				pos += mLen
				litStart = pos
				continue
			}
		}

		pos++
	}

	if litStart < uSize {
		flushLiterals(src[litStart:uSize])
	}

	level := opts.Level
	if opts.ArchivalMode {
		level = ModeArchival
	}
	if level <= 0 {
		level = 3
	}

	litSection := pad.literalBuf
	flags := uint8(0)
	if len(pad.literalBuf) >= 32 {
		var vansOut []byte
		var vansBeatsRaw bool
		if !opts.StandardStreamOnly && level >= 2 {
			sheafOut, sheafOk := packSheafVANS(pad.literalBuf, pad)
			ctxSheafOut, ctxSheafOk := packContextSheafVANS(pad.literalBuf, pad)
			var stdOut []byte
			var stdErr error
			if comp != nil {
				stdOut, _, stdErr = comp.VANSEncode4xHost(pad.literalBuf)
			}
			if stdErr != nil || len(stdOut) == 0 {
				stdOut, stdErr = VANSEncode4x(pad.literalBuf)
			}
			stdOk := stdErr == nil && len(stdOut) < int(float64(len(pad.literalBuf))*0.95)

			if isProbablyText(src) && level <= 3 && sheafOk {
				vansOut = sheafOut
				vansBeatsRaw = true
			} else {
				bestLen := int(float64(len(pad.literalBuf)) * 0.95)
				if stdOk && len(stdOut) < bestLen {
					vansOut = stdOut
					bestLen = len(stdOut)
					vansBeatsRaw = true
				}
				if sheafOk && len(sheafOut) < bestLen {
					vansOut = sheafOut
					bestLen = len(sheafOut)
					vansBeatsRaw = true
				}
				if ctxSheafOk && len(ctxSheafOut) < bestLen {
					vansOut = ctxSheafOut
					vansBeatsRaw = true
				}
			}
		}

		var nibbleOut []byte
		var nibbleOk bool
		if level > 3 {
			nibbleOut, nibbleOk = packNibblePlanes(pad.literalBuf, pad.nibbleBuf, pad.nibbleLowBuf)
		}
		if vansBeatsRaw && (!nibbleOk || len(vansOut) <= len(nibbleOut)) {
			litSection = vansOut
			flags |= FlagNibble
		} else if nibbleOk {
			litSection = nibbleOut
			flags |= FlagNibble
		}
	}

	cmdMode := uint8(ModeHuffman4X)
	if level <= 3 {
		cmdMode = ModeFlat
	}
	var cmdSection, offSection []byte
	if cmdMode == ModeFlat && comp != nil {
		var cErr, oErr error
		cmdSection, _, cErr = comp.PackStreamFlatHost(pad.cmdBuf)
		offSection, _, oErr = comp.PackStreamFlatHost(pad.offsetBuf)
		if cErr != nil || oErr != nil {
			cmdSection = nil
			offSection = nil
		}
	}
	if len(cmdSection) == 0 {
		var err error
		cmdSection, err = PackCommandStream(pad.cmdBuf, cmdMode)
		if err != nil {
			return nil, false
		}
		offSection, err = PackStream(pad.offsetBuf, cmdMode)
		if err != nil {
			return nil, false
		}
	}

	windowLog := opts.WindowLog
	if windowLog == 0 {
		if uSize <= 64*1024 {
			windowLog = WindowLog64K
		} else if uSize > 1024*1024 || level >= 3 {
			windowLog = WindowLog8M
		} else {
			windowLog = WindowLog1M
		}
	}
	if windowLog < WindowLog64K {
		windowLog = WindowLog64K
	} else if windowLog > WindowLog8M {
		windowLog = WindowLog8M
	}

	if uSize > 65535 || len(cmdSection) > 65535 || len(offSection) > 65535 || windowLog > WindowLog64K {
		flags |= FlagExtOffset
	}

	secDirSize := generic.Ternary((flags&FlagExtOffset) != 0, 6, 4)
	sec1Size := 8
	totalCompressedSize := HeaderSize + secDirSize + sec1Size + len(cmdSection) + len(offSection) + len(litSection)

	if totalCompressedSize >= uSize {
		return nil, false
	}

	out = make([]byte, totalCompressedSize)
	binary.LittleEndian.PutUint32(out[0:4], MagicPRZM)
	out[4] = flags
	out[5] = byte(uSize)
	out[6] = byte(uSize >> 8)
	out[7] = byte(uSize >> 16)

	wOffset := HeaderSize
	if (flags & FlagExtOffset) == 0 {
		binary.LittleEndian.PutUint16(out[wOffset:wOffset+2], uint16(len(cmdSection)))
		binary.LittleEndian.PutUint16(out[wOffset+2:wOffset+4], uint16(len(offSection)))
		wOffset += 4
	} else {
		cmdLen := len(cmdSection)
		offLen := len(offSection)
		out[wOffset] = byte(cmdLen)
		out[wOffset+1] = byte(cmdLen >> 8)
		out[wOffset+2] = byte(cmdLen >> 16)
		out[wOffset+3] = byte(offLen)
		out[wOffset+4] = byte(offLen >> 8)
		out[wOffset+5] = byte(offLen >> 16)
		wOffset += 6
	}

	for i := range sec1Size {
		out[wOffset+i] = 0
	}
	out[wOffset+1] = byte(windowLog)
	wOffset += sec1Size

	copy(out[wOffset:wOffset+len(cmdSection)], cmdSection)
	wOffset += len(cmdSection)

	copy(out[wOffset:wOffset+len(offSection)], offSection)
	wOffset += len(offSection)

	copy(out[wOffset:wOffset+len(litSection)], litSection)

	return out, true
}
