// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.simd && amd64

package przm

import (
	"simd/archsimd"
)

const VMSIMDEnabled = true

// ExecuteProgramSIMD executes the PRZM-VM microprogram on AMD64 AVX2/GFNI hardware using Go 1.27's simd/archsimd.
// Compiles directly to inlined VGF2P8MULB + VPXORD + VMOVDQU intrinsics without Plan9 call overhead.
func ExecuteProgramSIMD(prog *VMProgram, ctx *VMContext, streamData [][]byte, streamPtrs []int, dst []byte, outPtr *int, uSize int) bool {
	numInsts := len(prog.Instructions)
	if numInsts > VMMaxInsts || numInsts == 0 {
		return false
	}

	if archsimd.X86.AVX512() {
		return executeProgramSIMD512(prog, ctx, streamData, streamPtrs, dst, outPtr, uSize, numInsts)
	}

	var regsLo [VMNumRegs]archsimd.Uint8x32
	var regsHi [VMNumRegs]archsimd.Uint8x32

	zeroVec := archsimd.BroadcastUint8x32(0)
	for r := range VMNumRegs {
		regsLo[r] = zeroVec
		regsHi[r] = zeroVec
	}

	for *outPtr+VMRegSize <= uSize {
		skip := false
		for i := range numInsts {
			op, dstReg, s1, s2, imm := prog.Instructions[i].Decode()

			if skip {
				switch op {
				case OpLOADSTREAM:
					streamIdx := imm
					if streamIdx >= 0 && streamIdx < len(streamData) {
						streamPtrs[streamIdx] += VMRegSize
						if streamPtrs[streamIdx] > len(streamData[streamIdx]) {
							streamPtrs[streamIdx] = len(streamData[streamIdx])
						}
					}
				case OpEMIT:
					*outPtr += VMRegSize
				}
				continue
			}

			switch op {
			case OpNOP:
				// no-op

			case OpLOADSTREAM:
				streamIdx := imm
				if streamIdx < 0 || streamIdx >= len(streamData) {
					return false
				}
				sBuf := streamData[streamIdx]
				sPtr := streamPtrs[streamIdx]
				if sPtr+VMRegSize <= len(sBuf) {
					regsLo[dstReg] = archsimd.LoadUint8x32(sBuf[sPtr : sPtr+32])
					regsHi[dstReg] = archsimd.LoadUint8x32(sBuf[sPtr+32 : sPtr+64])
					streamPtrs[streamIdx] += VMRegSize
				} else {
					goto Done // fall back to scalar for ragged stream boundary
				}

			case OpEXPAND:
				v := archsimd.BroadcastUint8x32(byte(imm))
				regsLo[dstReg] = v
				regsHi[dstReg] = v

			case OpMOV:
				regsLo[dstReg] = regsLo[s1]
				regsHi[dstReg] = regsHi[s1]

			case OpXOR:
				regsLo[dstReg] = regsLo[s1].Xor(regsLo[s2])
				regsHi[dstReg] = regsHi[s1].Xor(regsHi[s2])

			case OpTERNLOG:
				regsLo[dstReg] = regsLo[dstReg].Xor(regsLo[s1].Xor(regsLo[s2]))
				regsHi[dstReg] = regsHi[dstReg].Xor(regsHi[s1].Xor(regsHi[s2]))

			case OpGFMUL:
				regsLo[dstReg] = regsLo[s1].GaloisFieldMul(regsLo[s2])
				regsHi[dstReg] = regsHi[s1].GaloisFieldMul(regsHi[s2])

			case OpCMP_EQ:
				regsLo[dstReg] = regsLo[s1].Equal(regsLo[s2]).ToInt8x32().ToBits()
				regsHi[dstReg] = regsHi[s1].Equal(regsHi[s2]).ToInt8x32().ToBits()

			case OpSKIP_CHUNK:
				if regsLo[s1].IsZero() && regsHi[s1].IsZero() {
					skip = true
				}

			case OpEMIT:
				p := *outPtr
				regsLo[dstReg].Store(dst[p : p+32])
				regsHi[dstReg].Store(dst[p+32 : p+64])
				*outPtr += VMRegSize

			case OpMARGOLUS:
				regsLo[dstReg].Store(ctx.Regs[dstReg][0:32])
				regsHi[dstReg].Store(ctx.Regs[dstReg][32:64])
				ruleID := uint8(imm & 0x0F)
				if ruleID == 0 && s1 > 0 {
					ruleID = uint8(s1 & 0x0F)
				}
				phase := (imm >> 4) & 1
				if (s2 & 1) != 0 {
					phase = s2 & 1
				}
				inverse := (imm & 0x100) == 0
				MargolusStepInplace(ctx.Regs[dstReg][:], 8, 8, phase, ruleID, inverse)
				regsLo[dstReg] = archsimd.LoadUint8x32(ctx.Regs[dstReg][0:32])
				regsHi[dstReg] = archsimd.LoadUint8x32(ctx.Regs[dstReg][32:64])

			case OpMOBIUS:
				regsLo[dstReg].Store(ctx.Regs[dstReg][0:32])
				regsHi[dstReg].Store(ctx.Regs[dstReg][32:64])
				if (imm&1) != 0 || (imm&int(FMTFlagBitLevel)) != 0 {
					FMTBitButterflySWARInplace(ctx.Regs[dstReg][:])
				}
				FastMobiusTransform64Inplace(&ctx.Regs[dstReg])
				regsLo[dstReg] = archsimd.LoadUint8x32(ctx.Regs[dstReg][0:32])
				regsHi[dstReg] = archsimd.LoadUint8x32(ctx.Regs[dstReg][32:64])

			default:
				return false // unsupported SIMD opcode, fallback to scalar
			}
		}
	}

Done:
	for r := range VMNumRegs {
		regsLo[r].Store(ctx.Regs[r][0:32])
		regsHi[r].Store(ctx.Regs[r][32:64])
	}
	return true
}

func executeProgramSIMD512(prog *VMProgram, ctx *VMContext, streamData [][]byte, streamPtrs []int, dst []byte, outPtr *int, uSize int, numInsts int) bool {
	var regs [VMNumRegs]archsimd.Uint8x64

	zeroVec := archsimd.BroadcastUint8x64(0)
	for r := range VMNumRegs {
		regs[r] = zeroVec
	}

	for *outPtr+VMRegSize <= uSize {
		skip := false
		for i := range numInsts {
			op, dstReg, s1, s2, imm := prog.Instructions[i].Decode()

			if skip {
				switch op {
				case OpLOADSTREAM:
					streamIdx := imm
					if streamIdx >= 0 && streamIdx < len(streamData) {
						streamPtrs[streamIdx] += VMRegSize
						if streamPtrs[streamIdx] > len(streamData[streamIdx]) {
							streamPtrs[streamIdx] = len(streamData[streamIdx])
						}
					}
				case OpEMIT:
					*outPtr += VMRegSize
				}
				continue
			}

			switch op {
			case OpNOP:
				// no-op

			case OpLOADSTREAM:
				streamIdx := imm
				if streamIdx < 0 || streamIdx >= len(streamData) {
					return false
				}
				sBuf := streamData[streamIdx]
				sPtr := streamPtrs[streamIdx]
				if sPtr+VMRegSize <= len(sBuf) {
					regs[dstReg] = archsimd.LoadUint8x64(sBuf[sPtr : sPtr+64])
					streamPtrs[streamIdx] += VMRegSize
				} else {
					goto Done // fall back to scalar for ragged stream boundary
				}

			case OpEXPAND:
				regs[dstReg] = archsimd.BroadcastUint8x64(byte(imm))

			case OpMOV:
				regs[dstReg] = regs[s1]

			case OpXOR:
				regs[dstReg] = regs[s1].Xor(regs[s2])

			case OpTERNLOG:
				regs[dstReg] = regs[dstReg].Xor(regs[s1].Xor(regs[s2]))

			case OpGFMUL:
				regs[dstReg] = regs[s1].GaloisFieldMul(regs[s2])

			case OpCMP_EQ:
				regs[dstReg] = regs[s1].Equal(regs[s2]).ToInt8x64().ToBits()

			case OpSKIP_CHUNK:
				if regs[s1].Equal(zeroVec).ToBits() == ^uint64(0) {
					skip = true
				}

			case OpEMIT:
				p := *outPtr
				regs[dstReg].Store(dst[p : p+64])
				*outPtr += VMRegSize

			case OpMARGOLUS:
				regs[dstReg].Store(ctx.Regs[dstReg][:])
				ruleID := uint8(imm & 0x0F)
				if ruleID == 0 && s1 > 0 {
					ruleID = uint8(s1 & 0x0F)
				}
				phase := (imm >> 4) & 1
				if (s2 & 1) != 0 {
					phase = s2 & 1
				}
				inverse := (imm & 0x100) == 0
				MargolusStepInplace(ctx.Regs[dstReg][:], 8, 8, phase, ruleID, inverse)
				regs[dstReg] = archsimd.LoadUint8x64(ctx.Regs[dstReg][:])

			case OpMOBIUS:
				regs[dstReg].Store(ctx.Regs[dstReg][:])
				if (imm&1) != 0 || (imm&int(FMTFlagBitLevel)) != 0 {
					FMTBitButterflySWARInplace(ctx.Regs[dstReg][:])
				}
				FastMobiusTransform64Inplace(&ctx.Regs[dstReg])
				regs[dstReg] = archsimd.LoadUint8x64(ctx.Regs[dstReg][:])

			default:
				return false // unsupported SIMD opcode, fallback to scalar
			}
		}
	}

Done:
	for r := range VMNumRegs {
		regs[r].Store(ctx.Regs[r][:])
	}
	return true
}
