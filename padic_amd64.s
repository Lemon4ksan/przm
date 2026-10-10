// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 && !purego

#include "textflag.h"

// func padicInverseAVX2_16(data unsafe.Pointer, n int)
TEXT ·padicInverseAVX2_16(SB), NOSPLIT, $0-16
	MOVQ data+0(FP), DI
	MOVQ n+8(FP), SI

	CMPQ SI, $32
	JL done16

	VMOVDQU 0(DI), X0
	MOVQ $16, AX

loop16_unrolled4:
	MOVQ SI, CX
	SUBQ AX, CX
	CMPQ CX, $64
	JL loop16_single

	VMOVDQU 0(DI)(AX*1), X1
	VPADDB X0, X1, X0
	VMOVDQU X0, 0(DI)(AX*1)

	VMOVDQU 16(DI)(AX*1), X1
	VPADDB X0, X1, X0
	VMOVDQU X0, 16(DI)(AX*1)

	VMOVDQU 32(DI)(AX*1), X1
	VPADDB X0, X1, X0
	VMOVDQU X0, 32(DI)(AX*1)

	VMOVDQU 48(DI)(AX*1), X1
	VPADDB X0, X1, X0
	VMOVDQU X0, 48(DI)(AX*1)

	ADDQ $64, AX
	JMP loop16_unrolled4

loop16_single:
	MOVQ SI, CX
	SUBQ AX, CX
	CMPQ CX, $16
	JL done16_simd

	VMOVDQU 0(DI)(AX*1), X1
	VPADDB X0, X1, X0
	VMOVDQU X0, 0(DI)(AX*1)

	ADDQ $16, AX
	JMP loop16_single

done16_simd:
	VZEROUPPER

done16:
	RET

// func padicInverseAVX2_32(data unsafe.Pointer, n int)
TEXT ·padicInverseAVX2_32(SB), NOSPLIT, $0-16
	MOVQ data+0(FP), DI
	MOVQ n+8(FP), SI

	CMPQ SI, $64
	JL done32

	VMOVDQU 0(DI), Y0
	MOVQ $32, AX

loop32_unrolled4:
	MOVQ SI, CX
	SUBQ AX, CX
	CMPQ CX, $128
	JL loop32_single

	VMOVDQU 0(DI)(AX*1), Y1
	VPADDB Y0, Y1, Y0
	VMOVDQU Y0, 0(DI)(AX*1)

	VMOVDQU 32(DI)(AX*1), Y1
	VPADDB Y0, Y1, Y0
	VMOVDQU Y0, 32(DI)(AX*1)

	VMOVDQU 64(DI)(AX*1), Y1
	VPADDB Y0, Y1, Y0
	VMOVDQU Y0, 64(DI)(AX*1)

	VMOVDQU 96(DI)(AX*1), Y1
	VPADDB Y0, Y1, Y0
	VMOVDQU Y0, 96(DI)(AX*1)

	ADDQ $128, AX
	JMP loop32_unrolled4

loop32_single:
	MOVQ SI, CX
	SUBQ AX, CX
	CMPQ CX, $32
	JL done32_simd

	VMOVDQU 0(DI)(AX*1), Y1
	VPADDB Y0, Y1, Y0
	VMOVDQU Y0, 0(DI)(AX*1)

	ADDQ $32, AX
	JMP loop32_single

done32_simd:
	VZEROUPPER

done32:
	RET

// func padicInverseAVX2_64(data unsafe.Pointer, n int)
TEXT ·padicInverseAVX2_64(SB), NOSPLIT, $0-16
	MOVQ data+0(FP), DI
	MOVQ n+8(FP), SI

	CMPQ SI, $128
	JL done64

	VMOVDQU 0(DI), Y0
	VMOVDQU 32(DI), Y1
	MOVQ $64, AX

loop64_unrolled2:
	MOVQ SI, CX
	SUBQ AX, CX
	CMPQ CX, $128
	JL loop64_single

	VMOVDQU 0(DI)(AX*1), Y2
	VMOVDQU 32(DI)(AX*1), Y3
	VPADDB Y0, Y2, Y0
	VPADDB Y1, Y3, Y1
	VMOVDQU Y0, 0(DI)(AX*1)
	VMOVDQU Y1, 32(DI)(AX*1)

	VMOVDQU 64(DI)(AX*1), Y2
	VMOVDQU 96(DI)(AX*1), Y3
	VPADDB Y0, Y2, Y0
	VPADDB Y1, Y3, Y1
	VMOVDQU Y0, 64(DI)(AX*1)
	VMOVDQU Y1, 96(DI)(AX*1)

	ADDQ $128, AX
	JMP loop64_unrolled2

loop64_single:
	MOVQ SI, CX
	SUBQ AX, CX
	CMPQ CX, $64
	JL done64_simd

	VMOVDQU 0(DI)(AX*1), Y2
	VMOVDQU 32(DI)(AX*1), Y3
	VPADDB Y0, Y2, Y0
	VPADDB Y1, Y3, Y1
	VMOVDQU Y0, 0(DI)(AX*1)
	VMOVDQU Y1, 32(DI)(AX*1)

	ADDQ $64, AX
	JMP loop64_single

done64_simd:
	VZEROUPPER

done64:
	RET

// func padicInverseWord64_AVX2(data unsafe.Pointer, numWords int)
TEXT ·padicInverseWord64_AVX2(SB), NOSPLIT, $0-16
	MOVQ data+0(FP), DI
	MOVQ numWords+8(FP), SI

	CMPQ SI, $2
	JL done_w64

	MOVQ 0(DI), AX
	MOVQ $1, CX

loop_w64_unrolled4:
	MOVQ SI, DX
	SUBQ CX, DX
	CMPQ DX, $4
	JL loop_w64_single

	ADDQ 0(DI)(CX*8), AX
	MOVQ AX, 0(DI)(CX*8)

	ADDQ 8(DI)(CX*8), AX
	MOVQ AX, 8(DI)(CX*8)

	ADDQ 16(DI)(CX*8), AX
	MOVQ AX, 16(DI)(CX*8)

	ADDQ 24(DI)(CX*8), AX
	MOVQ AX, 24(DI)(CX*8)

	ADDQ $4, CX
	JMP loop_w64_unrolled4

loop_w64_single:
	CMPQ CX, SI
	JGE done_w64

	ADDQ 0(DI)(CX*8), AX
	MOVQ AX, 0(DI)(CX*8)

	ADDQ $1, CX
	JMP loop_w64_single

done_w64:
	RET

