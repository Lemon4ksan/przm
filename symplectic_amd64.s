// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 && !purego

#include "textflag.h"

// Shuffle masks for extracting p0 and p1:
// In 4-byte quad (q0, q1, p0, p1):
// p0 is at byte 2, p1 is at byte 3.
// We want:
// maskP0: [2, 2, 0x80, 0x80,  6, 6, 0x80, 0x80,  10, 10, 0x80, 0x80,  14, 14, 0x80, 0x80, ...]
// maskP1: [3, 3, 0x80, 0x80,  7, 7, 0x80, 0x80,  11, 11, 0x80, 0x80,  15, 15, 0x80, 0x80, ...]
DATA ·maskP0<>+0(SB)/8,  $0x8080060680800202
DATA ·maskP0<>+8(SB)/8,  $0x80800e0e80800a0a
DATA ·maskP0<>+16(SB)/8, $0x8080060680800202
DATA ·maskP0<>+24(SB)/8, $0x80800e0e80800a0a
GLOBL ·maskP0<>(SB), RODATA|NOPTR, $32

DATA ·maskP1<>+0(SB)/8,  $0x8080070780800303
DATA ·maskP1<>+8(SB)/8,  $0x80800f0f80800b0b
DATA ·maskP1<>+16(SB)/8, $0x8080070780800303
DATA ·maskP1<>+24(SB)/8, $0x80800f0f80800b0b
GLOBL ·maskP1<>(SB), RODATA|NOPTR, $32

// func transformBlockAVX2GFNI(data unsafe.Pointer, n int, s00, s01, s11 uint8)
TEXT ·transformBlockAVX2GFNI(SB), NOSPLIT, $0-19
	MOVQ data+0(FP), DI
	MOVQ n+8(FP), SI
	MOVBQZX s00+16(FP), DX
	MOVBQZX s01+17(FP), CX
	MOVBQZX s11+18(FP), R8

	CMPQ SI, $32
	JL done

	// Construct constant vectors Y0 (for C0) and Y1 (for C1):
	// C0: [s00, s01, 0, 0,  s00, s01, 0, 0,  ...]
	// C1: [s01, s11, 0, 0,  s01, s11, 0, 0,  ...]
	VPXOR X0, X0, X0
	VPINSRB $0, DX, X0, X0
	VPINSRB $1, CX, X0, X0
	VPINSRB $4, DX, X0, X0
	VPINSRB $5, CX, X0, X0
	VPINSRB $8, DX, X0, X0
	VPINSRB $9, CX, X0, X0
	VPINSRB $12, DX, X0, X0
	VPINSRB $13, CX, X0, X0
	VINSERTI128 $1, X0, Y0, Y0

	VPXOR X1, X1, X1
	VPINSRB $0, CX, X1, X1
	VPINSRB $1, R8, X1, X1
	VPINSRB $4, CX, X1, X1
	VPINSRB $5, R8, X1, X1
	VPINSRB $8, CX, X1, X1
	VPINSRB $9, R8, X1, X1
	VPINSRB $12, CX, X1, X1
	VPINSRB $13, R8, X1, X1
	VINSERTI128 $1, X1, Y1, Y1

	VMOVDQU ·maskP0<>(SB), Y2
	VMOVDQU ·maskP1<>(SB), Y3

	MOVQ SI, AX
	ANDQ $~3, AX // 4-byte aligned limit

	XORQ R9, R9  // byte offset

	// 128-byte unrolled loop (4x AVX2 registers)
loop128:
	MOVQ AX, R10
	SUBQ R9, R10
	CMPQ R10, $128
	JL loop32

	VMOVDQU 0(DI)(R9*1), Y4
	VMOVDQU 32(DI)(R9*1), Y5
	VMOVDQU 64(DI)(R9*1), Y6
	VMOVDQU 96(DI)(R9*1), Y7

	VPSHUFB Y2, Y4, Y8
	VPSHUFB Y3, Y4, Y9
	VPSHUFB Y2, Y5, Y10
	VPSHUFB Y3, Y5, Y11
	VPSHUFB Y2, Y6, Y12
	VPSHUFB Y3, Y6, Y13
	VPSHUFB Y2, Y7, Y14
	VPSHUFB Y3, Y7, Y15

	VGF2P8MULB Y0, Y8, Y8
	VGF2P8MULB Y1, Y9, Y9
	VPXOR Y8, Y9, Y8

	VGF2P8MULB Y0, Y10, Y9
	VGF2P8MULB Y1, Y11, Y10
	VPXOR Y9, Y10, Y9

	VGF2P8MULB Y0, Y12, Y10
	VGF2P8MULB Y1, Y13, Y11
	VPXOR Y10, Y11, Y10

	VGF2P8MULB Y0, Y14, Y11
	VGF2P8MULB Y1, Y15, Y12
	VPXOR Y11, Y12, Y11

	VPXOR Y4, Y8, Y4
	VPXOR Y5, Y9, Y5
	VPXOR Y6, Y10, Y6
	VPXOR Y7, Y11, Y7

	VMOVDQU Y4, 0(DI)(R9*1)
	VMOVDQU Y5, 32(DI)(R9*1)
	VMOVDQU Y6, 64(DI)(R9*1)
	VMOVDQU Y7, 96(DI)(R9*1)

	ADDQ $128, R9
	JMP loop128

loop32:
	MOVQ AX, R10
	SUBQ R9, R10
	CMPQ R10, $32
	JL doneSIMD

	VMOVDQU 0(DI)(R9*1), Y4
	VPSHUFB Y2, Y4, Y8
	VPSHUFB Y3, Y4, Y9
	VGF2P8MULB Y0, Y8, Y8
	VGF2P8MULB Y1, Y9, Y9
	VPXOR Y8, Y9, Y8
	VPXOR Y4, Y8, Y4
	VMOVDQU Y4, 0(DI)(R9*1)

	ADDQ $32, R9
	JMP loop32

doneSIMD:
	VZEROUPPER

done:
	RET

// func cpuid(eax, ecx uint32) (a, b, c, d uint32)
TEXT ·cpuid(SB), NOSPLIT, $0-24
	MOVL eax+0(FP), AX
	MOVL ecx+4(FP), CX
	CPUID
	MOVL AX, a+8(FP)
	MOVL BX, b+12(FP)
	MOVL CX, c+16(FP)
	MOVL DX, d+20(FP)
	RET

// func xgetbv() (eax, edx uint32)
TEXT ·xgetbv(SB), NOSPLIT, $0-8
	MOVL $0, CX
	XGETBV
	MOVL AX, eax+0(FP)
	MOVL DX, edx+4(FP)
	RET

