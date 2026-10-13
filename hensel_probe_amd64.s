// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build amd64 && !purego

#include "textflag.h"

DATA mask16<>+0x00(SB)/4, $0x0000ffff
DATA mask16<>+0x04(SB)/4, $0x0000ffff
DATA mask16<>+0x08(SB)/4, $0x0000ffff
DATA mask16<>+0x0c(SB)/4, $0x0000ffff
DATA mask16<>+0x10(SB)/4, $0x0000ffff
DATA mask16<>+0x14(SB)/4, $0x0000ffff
DATA mask16<>+0x18(SB)/4, $0x0000ffff
DATA mask16<>+0x1c(SB)/4, $0x0000ffff
GLOBL mask16<>(SB), RODATA|NOPTR, $32

// func vectorProbe8_AVX2(posBase unsafe.Pointer, fpBase unsafe.Pointer, curPos int32, targetFP uint32, windowSize int32, res *VectorProbeResult)
TEXT ·vectorProbe8_AVX2(SB), NOSPLIT, $0-40
	MOVQ posBase+0(FP), DI
	MOVQ fpBase+8(FP), SI
	MOVQ res+32(FP), R11

	// Broadcast curPos into Y0 from memory (valid AVX2 m32 operand)
	VPBROADCASTD curPos+16(FP), Y0
	// Load 8 positions from posBase into Y1
	VMOVDQU 0(DI), Y1
	// Calculate dist = curPos - pos
	VPSUBD Y1, Y0, Y2

	// Check dist > 0
	VPXOR Y3, Y3, Y3
	VPCMPGTD Y3, Y2, Y4    // Y4 = (dist > 0)

	// Check dist < windowSize
	VPBROADCASTD windowSize+24(FP), Y5
	VPCMPGTD Y2, Y5, Y6    // Y6 = (windowSize > dist)
	VPAND Y4, Y6, Y4       // Y4 = valid distance mask (0 < dist < windowSize)

	// Valid distance movemask (8 bits)
	VMOVMSKPS Y4, AX
	MOVL AX, 4(R11)        // res.ValidMask = AX

	// Broadcast targetFP into Y7
	VPBROADCASTD targetFP+20(FP), Y7
	// Load 8 fingerprints from fpBase into Y8
	VMOVDQU 0(SI), Y8
	// Compare fp == targetFP
	VPCMPEQD Y7, Y8, Y9    // Y9 = (fp == targetFP)

	// Exact match = valid distance AND fp == targetFP
	VPAND Y4, Y9, Y10
	VMOVMSKPS Y10, BX
	MOVL BX, 0(R11)        // res.ExactMask = BX

	// Check 16-bit low match: mask low 16 bits of each 32-bit dword and compare dwords
	VMOVDQU mask16<>(SB), Y14
	VPAND Y7, Y14, Y12
	VPAND Y8, Y14, Y13
	VPCMPEQD Y12, Y13, Y15
	VPAND Y4, Y15, Y15
	VMOVMSKPS Y15, CX
	MOVL CX, 8(R11)        // res.Part16Mask = CX

	VZEROUPPER
	RET

// func vectorProbe16_AVX2(posBase unsafe.Pointer, fpBase unsafe.Pointer, curPos int32, targetFP uint32, windowSize int32, res *VectorProbeResult16)
TEXT ·vectorProbe16_AVX2(SB), NOSPLIT, $0-40
	MOVQ posBase+0(FP), DI
	MOVQ fpBase+8(FP), SI
	MOVQ res+32(FP), R11

	// Broadcast curPos into Y0, windowSize into Y5, targetFP into Y7
	VPBROADCASTD curPos+16(FP), Y0
	VPBROADCASTD windowSize+24(FP), Y5
	VPBROADCASTD targetFP+20(FP), Y7
	VPXOR Y3, Y3, Y3

	// --- Block 0: Entries 0..7 ---
	VMOVDQU 0(DI), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4       // Y4 = valid dist mask low

	VMOVDQU 0(SI), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10      // Y10 = exact match low

	VMOVMSKPS Y4, AX       // low 8 bits valid
	VMOVMSKPS Y10, BX      // low 8 bits exact

	// --- Block 1: Entries 8..15 ---
	VMOVDQU 32(DI), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4       // Y4 = valid dist mask high

	VMOVDQU 32(SI), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10      // Y10 = exact match high

	VMOVMSKPS Y4, CX       // high 8 bits valid
	VMOVMSKPS Y10, DX      // high 8 bits exact

	// Combine low and high into 16-bit masks
	SHLL $8, CX
	ORL CX, AX
	MOVL AX, 4(R11)        // res.ValidMask = AX (16 bits)

	SHLL $8, DX
	ORL DX, BX
	MOVL BX, 0(R11)        // res.ExactMask = BX (16 bits)

	VZEROUPPER
	RET

// func vectorChainScan_AVX2(posPtr unsafe.Pointer, fpPtr unsafe.Pointer, curPos int32, targetFP uint32, windowSize int32, count int32, res *VectorScanResult)
TEXT ·vectorChainScan_AVX2(SB), NOSPLIT, $0-48
	MOVQ posPtr+0(FP), DI
	MOVQ fpPtr+8(FP), SI
	MOVL count+28(FP), R12
	MOVQ res+32(FP), R11

	VPBROADCASTD curPos+16(FP), Y0     // Y0 = curPos
	VPBROADCASTD windowSize+24(FP), Y5 // Y5 = windowSize
	VPBROADCASTD targetFP+20(FP), Y7   // Y7 = targetFP
	VPXOR Y3, Y3, Y3                   // Y3 = 0

	XORL AX, AX                        // bestSlot = -1
	DECL AX
	MOVL AX, 0(R11)                    // res.BestExactSlot = -1
	MOVL AX, 4(R11)                    // res.BestValidSlot = -1
	XORL R13, R13                      // exactCount = 0
	XORL R14, R14                      // validCount = 0

	XORL R15, R15                      // i = 0

scan_loop:
	CMPL R15, R12
	JGE scan_done

	// Load 8 positions and calculate dist
	VMOVDQU 0(DI)(R15*4), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4                   // Y4 = valid dist mask

	VMOVMSKPS Y4, AX
	TESTL AX, AX
	JZ next_8

	// There is at least 1 valid candidate
	CMPL 4(R11), $0
	JGE check_exact
	// Save first valid candidate slot
	BSFL AX, BX
	ADDL R15, BX
	MOVL BX, 4(R11)                    // res.BestValidSlot = first valid

check_exact:
	// Load 8 fingerprints
	VMOVDQU 0(SI)(R15*4), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10
	VMOVMSKPS Y10, BX
	TESTL BX, BX
	JZ count_valid

	// Exact match found!
	CMPL 0(R11), $0
	JGE inc_exact
	BSFL BX, CX
	ADDL R15, CX
	MOVL CX, 0(R11)                    // res.BestExactSlot = first exact

inc_exact:
	POPCNTL BX, CX
	ADDL CX, R13

count_valid:
	POPCNTL AX, CX
	ADDL CX, R14

next_8:
	ADDL $8, R15
	JMP scan_loop

scan_done:
	MOVL R13, 8(R11)                   // res.ExactCount = R13
	MOVL R14, 12(R11)                  // res.ValidCount = R14
	VZEROUPPER
	RET


// func vectorProbe32_AVX2(posBase unsafe.Pointer, fpBase unsafe.Pointer, curPos int32, targetFP uint32, windowSize int32, res *VectorProbeResult32)
TEXT ·vectorProbe32_AVX2(SB), NOSPLIT, $0-40
	MOVQ posBase+0(FP), DI
	MOVQ fpBase+8(FP), SI
	MOVQ res+32(FP), R11

	// Broadcast curPos into Y0, windowSize into Y5, targetFP into Y7
	VPBROADCASTD curPos+16(FP), Y0
	VPBROADCASTD windowSize+24(FP), Y5
	VPBROADCASTD targetFP+20(FP), Y7
	VPXOR Y3, Y3, Y3

	// --- Block 0: Entries 0..7 ---
	VMOVDQU 0(DI), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4       // valid dist mask

	VMOVDQU 0(SI), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10      // exact match

	VMOVMSKPS Y4, AX       // bits 0..7 valid
	VMOVMSKPS Y10, R8      // bits 0..7 exact

	// --- Block 1: Entries 8..15 ---
	VMOVDQU 32(DI), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4

	VMOVDQU 32(SI), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10

	VMOVMSKPS Y4, BX       // bits 8..15 valid
	VMOVMSKPS Y10, R9      // bits 8..15 exact

	// --- Block 2: Entries 16..23 ---
	VMOVDQU 64(DI), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4

	VMOVDQU 64(SI), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10

	VMOVMSKPS Y4, CX       // bits 16..23 valid
	VMOVMSKPS Y10, R10     // bits 16..23 exact

	// --- Block 3: Entries 24..31 ---
	VMOVDQU 96(DI), Y1
	VPSUBD Y1, Y0, Y2
	VPCMPGTD Y3, Y2, Y4
	VPCMPGTD Y2, Y5, Y6
	VPAND Y4, Y6, Y4

	VMOVDQU 96(SI), Y8
	VPCMPEQD Y7, Y8, Y9
	VPAND Y4, Y9, Y10

	VMOVMSKPS Y4, DX       // bits 24..31 valid
	VMOVMSKPS Y10, R12     // bits 24..31 exact

	// Combine into 32-bit masks
	SHLL $8, BX
	ORL BX, AX
	SHLL $16, CX
	ORL CX, AX
	SHLL $24, DX
	ORL DX, AX
	MOVL AX, 4(R11)        // res.ValidMask = AX (32 bits)

	SHLL $8, R9
	ORL R9, R8
	SHLL $16, R10
	ORL R10, R8
	SHLL $24, R12
	ORL R12, R8
	MOVL R8, 0(R11)        // res.ExactMask = R8 (32 bits)

	VZEROUPPER
	RET
