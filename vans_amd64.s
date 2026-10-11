// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 && !purego

#include "textflag.h"

// func decode_vans4x(dst, src, streamPtr, origLen, decodeTable, x, srcLen uint64) uint64
TEXT ·decode_vans4x(SB), NOSPLIT, $0-64
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ streamPtr+16(FP), DX
	MOVQ origLen+24(FP), CX
	MOVQ decodeTable+32(FP), R8
	MOVQ xPtr+40(FP), R9
	MOVQ srcLen+48(FP), R10

	TESTQ CX, CX
	JLE done_early

	// Load 4 initial lane states into registers (R14 is preserved for Go g)
	MOVL 0(R9), R11
	MOVL 4(R9), R12
	MOVL 8(R9), R13
	MOVL 12(R9), R15

	XORQ BX, BX // i = 0

	// Check if we can enter the fast 4-lane unrolled loop
	// AX = origLen - 4
	MOVQ CX, AX
	SUBQ $4, AX
	// BP = srcLen - 8
	MOVQ R10, BP
	SUBQ $8, BP

loop_check:
	CMPQ BX, AX
	JGT scalar_setup
	CMPQ DX, BP
	JGE scalar_setup

fast_loop:
	// --- Lane 0 (R11) ---
	MOVL R11, AX
	ANDL $0xFFF, AX
	MOVL 0(R8)(AX*4), AX
	MOVB AL, 0(DI)(BX*1)
	MOVL AX, BP
	SHRL $8, BP
	ANDL $0xFFF, BP
	SHRL $20, AX
	INCL AX
	SHRL $12, R11
	IMULL AX, R11
	ADDL BP, R11
	CMPL R11, $0xFFFF
	JA lane1
	MOVL R11, BP
	SHLL $8, BP
	MOVBLZX 0(SI)(DX*1), AX
	INCQ DX
	ORL BP, AX
	MOVL AX, R11
	CMPL R11, $0xFFFF
	JA lane1
	SHLL $8, AX
	MOVBLZX 0(SI)(DX*1), BP
	INCQ DX
	ORL BP, AX
	MOVL AX, R11

lane1:
	// --- Lane 1 (R12) ---
	MOVL R12, AX
	ANDL $0xFFF, AX
	MOVL 0(R8)(AX*4), AX
	MOVB AL, 1(DI)(BX*1)
	MOVL AX, BP
	SHRL $8, BP
	ANDL $0xFFF, BP
	SHRL $20, AX
	INCL AX
	SHRL $12, R12
	IMULL AX, R12
	ADDL BP, R12
	CMPL R12, $0xFFFF
	JA lane2
	MOVL R12, BP
	SHLL $8, BP
	MOVBLZX 0(SI)(DX*1), AX
	INCQ DX
	ORL BP, AX
	MOVL AX, R12
	CMPL R12, $0xFFFF
	JA lane2
	SHLL $8, AX
	MOVBLZX 0(SI)(DX*1), BP
	INCQ DX
	ORL BP, AX
	MOVL AX, R12

lane2:
	// --- Lane 2 (R13) ---
	MOVL R13, AX
	ANDL $0xFFF, AX
	MOVL 0(R8)(AX*4), AX
	MOVB AL, 2(DI)(BX*1)
	MOVL AX, BP
	SHRL $8, BP
	ANDL $0xFFF, BP
	SHRL $20, AX
	INCL AX
	SHRL $12, R13
	IMULL AX, R13
	ADDL BP, R13
	CMPL R13, $0xFFFF
	JA lane3
	MOVL R13, BP
	SHLL $8, BP
	MOVBLZX 0(SI)(DX*1), AX
	INCQ DX
	ORL BP, AX
	MOVL AX, R13
	CMPL R13, $0xFFFF
	JA lane3
	SHLL $8, AX
	MOVBLZX 0(SI)(DX*1), BP
	INCQ DX
	ORL BP, AX
	MOVL AX, R13

lane3:
	// --- Lane 3 (R15) ---
	MOVL R15, AX
	ANDL $0xFFF, AX
	MOVL 0(R8)(AX*4), AX
	MOVB AL, 3(DI)(BX*1)
	MOVL AX, BP
	SHRL $8, BP
	ANDL $0xFFF, BP
	SHRL $20, AX
	INCL AX
	SHRL $12, R15
	IMULL AX, R15
	ADDL BP, R15
	CMPL R15, $0xFFFF
	JA step_end
	MOVL R15, BP
	SHLL $8, BP
	MOVBLZX 0(SI)(DX*1), AX
	INCQ DX
	ORL BP, AX
	MOVL AX, R15
	CMPL R15, $0xFFFF
	JA step_end
	SHLL $8, AX
	MOVBLZX 0(SI)(DX*1), BP
	INCQ DX
	ORL BP, AX
	MOVL AX, R15

step_end:
	ADDQ $4, BX
	JMP loop_check

scalar_setup:
	// Store register states back to x array before scalar fallback
	MOVL R11, 0(R9)
	MOVL R12, 4(R9)
	MOVL R13, 8(R9)
	MOVL R15, 12(R9)

	CMPQ BX, CX
	JGE done

scalar_loop:
	MOVL BX, AX
	ANDL $3, AX
	MOVL 0(R9)(AX*4), BP
	MOVL BP, AX
	ANDL $0xFFF, AX
	MOVL 0(R8)(AX*4), AX
	MOVB AL, 0(DI)(BX*1)
	MOVL AX, R11
	SHRL $8, R11
	ANDL $0xFFF, R11
	SHRL $20, AX
	INCL AX
	SHRL $12, BP
	IMULL AX, BP
	ADDL R11, BP
	CMPL BP, $0xFFFF
	JA scalar_save

scalar_reload:
	CMPQ DX, R10
	JGE err_corrupted
	SHLL $8, BP
	MOVBLZX 0(SI)(DX*1), AX
	INCQ DX
	ORL AX, BP
	CMPL BP, $0xFFFF
	JBE scalar_reload

scalar_save:
	MOVL BX, AX
	ANDL $3, AX
	MOVL BP, 0(R9)(AX*4)
	INCQ BX
	CMPQ BX, CX
	JL scalar_loop

done:
	MOVQ DX, ret+56(FP)
	RET

done_early:
	MOVQ DX, ret+56(FP)
	RET

err_corrupted:
	ADDQ $999999999, DX
	MOVQ DX, ret+56(FP)
	RET
