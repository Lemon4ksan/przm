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

	var regsLo [VMNumRegs]archsimd.Uint8x32
	var regsHi [VMNumRegs]archsimd.Uint8x32

	zeroVec := archsimd.BroadcastUint8x32(0)
	for r := range VMNumRegs {
		regsLo[r] = zeroVec
		regsHi[r] = zeroVec
	}

	for *outPtr+VMRegSize <= uSize {
		for i := 0; i < numInsts; i++ {
			op, dstReg, s1, s2, imm := prog.Instructions[i].Decode()

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
					return false // fall back to scalar for ragged stream boundary
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

			case OpEMIT:
				p := *outPtr
				regsLo[dstReg].Store(dst[p : p+32])
				regsHi[dstReg].Store(dst[p+32 : p+64])
				*outPtr += VMRegSize

			default:
				return false // unsupported SIMD opcode, fallback to scalar
			}
		}
	}

	// If whole blocks were processed and exactly uSize reached, success!
	if *outPtr == uSize {
		return true
	}

	// For the remaining tail (< 64B), execute via scalar fallback
	return false
}
