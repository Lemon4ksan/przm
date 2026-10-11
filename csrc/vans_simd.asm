	.file	"vans_simd.c"
	.intel_syntax noprefix
	.text
	.p2align 4
	.globl	"decode_vans4x"
	.def	"decode_vans4x";	.scl	2;	.type	32;	.endef
"decode_vans4x":
	sub	rsp, 88
	lea	rax, -3[r9]
	mov	QWORD PTR 24[rsp], rbx
	mov	rbx, QWORD PTR 136[rsp]
	mov	QWORD PTR 32[rsp], rsi
	mov	rsi, r9
	movdqu	xmm1, XMMWORD PTR [rbx]
	mov	QWORD PTR 48[rsp], rbp
	mov	rbp, rdx
	mov	QWORD PTR 56[rsp], r12
	mov	r12, rcx
	mov	QWORD PTR 64[rsp], r13
	movdqa	xmm0, xmm1
	mov	r13, QWORD PTR 128[rsp]
	mov	QWORD PTR 72[rsp], r14
	mov	r14, QWORD PTR 144[rsp]
	mov	QWORD PTR 8[rsp], rax
	test	rax, rax
	jle	.L14
	mov	QWORD PTR 80[rsp], r15
	lea	r15, -8[r14]
	cmp	r15, r8
	jle	.L34
	pshufd	xmm0, xmm1, 255
	mov	QWORD PTR 40[rsp], rdi
	movd	eax, xmm1
	xor	r10d, r10d
	movd	r9d, xmm0
	movdqa	xmm0, xmm1
	mov	QWORD PTR 120[rsp], rsi
	punpckhdq	xmm0, xmm1
	movd	edx, xmm0
	pshufd	xmm0, xmm1, 85
	movd	ecx, xmm0
	jmp	.L7
	.p2align 4,,10
	.p2align 3
.L36:
	movzx	r11d, BYTE PTR 0[rbp+r11]
	sal	eax, 8
	add	r8, 2
	or	eax, r11d
.L3:
	cmp	ecx, 65535
	ja	.L4
	movzx	ebx, BYTE PTR 0[rbp+r8]
	sal	ecx, 8
	lea	r11, 1[r8]
	or	ecx, ebx
	cmp	ecx, 65535
	ja	.L16
	movzx	r11d, BYTE PTR 0[rbp+r11]
	sal	ecx, 8
	add	r8, 2
	or	ecx, r11d
.L4:
	cmp	edx, 65535
	ja	.L5
	movzx	ebx, BYTE PTR 0[rbp+r8]
	sal	edx, 8
	lea	r11, 1[r8]
	or	edx, ebx
	cmp	edx, 65535
	ja	.L17
	movzx	r11d, BYTE PTR 0[rbp+r11]
	sal	edx, 8
	add	r8, 2
	or	edx, r11d
.L5:
	cmp	r9d, 65535
	ja	.L6
	movzx	ebx, BYTE PTR 0[rbp+r8]
	sal	r9d, 8
	lea	r11, 1[r8]
	or	r9d, ebx
	cmp	r9d, 65535
	ja	.L18
	movzx	r11d, BYTE PTR 0[rbp+r11]
	sal	r9d, 8
	add	r8, 2
	or	r9d, r11d
.L6:
	add	r10, 4
	cmp	QWORD PTR 8[rsp], r10
	jle	.L20
.L37:
	cmp	r8, r15
	jge	.L20
.L7:
	mov	r11d, eax
	shr	eax, 12
	and	r11d, 4095
	mov	edi, DWORD PTR 0[r13+r11*4]
	mov	r11d, ecx
	shr	ecx, 12
	and	r11d, 4095
	mov	esi, DWORD PTR 0[r13+r11*4]
	mov	r14d, edi
	mov	r11d, edx
	shr	edx, 12
	and	r11d, 4095
	shr	r14d, 20
	mov	ebx, DWORD PTR 0[r13+r11*4]
	add	r14d, 1
	mov	r11d, r9d
	shr	r9d, 12
	imul	eax, r14d
	and	r11d, 4095
	mov	r11d, DWORD PTR 0[r13+r11*4]
	mov	BYTE PTR [r12+r10], dil
	shr	edi, 8
	and	edi, 4095
	mov	BYTE PTR 1[r12+r10], sil
	add	eax, edi
	mov	edi, esi
	shr	esi, 8
	mov	BYTE PTR 2[r12+r10], bl
	shr	edi, 20
	and	esi, 4095
	mov	BYTE PTR 3[r12+r10], r11b
	add	edi, 1
	imul	ecx, edi
	add	ecx, esi
	mov	esi, ebx
	shr	ebx, 8
	shr	esi, 20
	and	ebx, 4095
	add	esi, 1
	imul	edx, esi
	add	edx, ebx
	mov	ebx, r11d
	shr	r11d, 8
	shr	ebx, 20
	and	r11d, 4095
	add	ebx, 1
	imul	r9d, ebx
	add	r9d, r11d
	cmp	eax, 65535
	ja	.L3
	movzx	ebx, BYTE PTR 0[rbp+r8]
	sal	eax, 8
	lea	r11, 1[r8]
	or	eax, ebx
	cmp	eax, 65535
	jbe	.L36
	mov	r8, r11
	jmp	.L3
	.p2align 4,,10
	.p2align 3
.L17:
	mov	r8, r11
	jmp	.L5
	.p2align 4,,10
	.p2align 3
.L18:
	mov	r8, r11
	add	r10, 4
	cmp	QWORD PTR 8[rsp], r10
	jg	.L37
	.p2align 4
	.p2align 3
.L20:
	movd	xmm1, edx
	movd	xmm2, r9d
	movd	xmm0, eax
	mov	rsi, QWORD PTR 120[rsp]
	movd	xmm3, ecx
	punpckldq	xmm1, xmm2
	mov	rbx, QWORD PTR 136[rsp]
	mov	r14, QWORD PTR 144[rsp]
	punpckldq	xmm0, xmm3
	mov	rdi, QWORD PTR 40[rsp]
	mov	r15, QWORD PTR 80[rsp]
	punpcklqdq	xmm0, xmm1
.L2:
	movups	XMMWORD PTR [rbx], xmm0
	cmp	rsi, r10
	jle	.L19
.L13:
	mov	rax, r10
	and	eax, 3
	lea	rcx, [rbx+rax*4]
	mov	r9d, DWORD PTR [rcx]
	mov	eax, r9d
	shr	r9d, 12
	and	eax, 4095
	mov	eax, DWORD PTR 0[r13+rax*4]
	mov	edx, eax
	mov	BYTE PTR [r12+r10], al
	shr	eax, 8
	shr	edx, 20
	and	eax, 4095
	add	edx, 1
	imul	edx, r9d
	add	eax, edx
	cmp	eax, 65535
	jbe	.L12
	jmp	.L10
	.p2align 5
	.p2align 4,,10
	.p2align 3
.L11:
	add	r8, 1
	sal	eax, 8
	movzx	edx, BYTE PTR -1[rbp+r8]
	or	eax, edx
	cmp	eax, 65535
	ja	.L10
.L12:
	cmp	r14, r8
	jg	.L11
	lea	rax, 999999999[r8]
.L1:
	mov	rbx, QWORD PTR 24[rsp]
	mov	rsi, QWORD PTR 32[rsp]
	mov	rbp, QWORD PTR 48[rsp]
	mov	r12, QWORD PTR 56[rsp]
	mov	r13, QWORD PTR 64[rsp]
	mov	r14, QWORD PTR 72[rsp]
	add	rsp, 88
	ret
	.p2align 4,,10
	.p2align 3
.L16:
	mov	r8, r11
	jmp	.L4
	.p2align 4,,10
	.p2align 3
.L10:
	add	r10, 1
	mov	DWORD PTR [rcx], eax
	cmp	rsi, r10
	jne	.L13
.L19:
	mov	rax, r8
	jmp	.L1
.L34:
	mov	r15, QWORD PTR 80[rsp]
.L14:
	xor	r10d, r10d
	jmp	.L2
	.ident	"GCC: (GNU) 16.1.0"
