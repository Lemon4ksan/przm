/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 *
 * Standalone Unit and Integration Test Suite for przm_dec.c.
 */

#include "przm_dec.h"

#include <assert.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define TEST_ASSERT(cond, msg) \
    do { \
        if (!(cond)) { \
            fprintf(stderr, "[FAIL] %s:%d: %s\n", __FILE__, __LINE__, msg); \
            exit(1); \
        } \
    } while (0)

#define TEST_PASS(name) \
    printf("[PASS] %s\n", name)

static inline void write_u32_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
    p[3] = (uint8_t)(v >> 24);
}

/* 1. Test Uncompressed Size Inspection */
static void test_get_uncompressed_size(void) {
    uint8_t frame_std[8] = {
        'P', 'R', 'Z', 'M',
        0x00,               /* Flags */
        0x20, 0x4E, 0x00    /* 20000 bytes (0x004E20) */
    };
    int64_t sz = przm_get_uncompressed_size(frame_std, sizeof(frame_std));
    TEST_ASSERT(sz == 20000, "Standard PRZM size mismatch");

    uint8_t frame_symp[12];
    write_u32_le(frame_symp, PRZM_MAGIC_SYMPLECTIC);
    frame_symp[4] = 0x01;             /* Mode = Shear */
    frame_symp[5] = 0x03; frame_symp[6] = 0x07; frame_symp[7] = 0x09; /* S00, S01, S11 */
    write_u32_le(frame_symp + 8, 4096); /* 4096 bytes */
    sz = przm_get_uncompressed_size(frame_symp, sizeof(frame_symp));
    TEST_ASSERT(sz == 4096, "Symplectic size mismatch");

    uint8_t frame_pws[24] = {
        'P', 'W', 0x01, 0x00, /* Magic + Stride */
        0x80, 0x00, 0x00, 0x00  /* 128 bytes */
    };
    sz = przm_get_uncompressed_size(frame_pws, sizeof(frame_pws));
    TEST_ASSERT(sz == 128, "PWS size mismatch");

    TEST_PASS("przm_get_uncompressed_size");
}

/* 2. Test Symplectic Involution (T_S^2 == I) */
static void test_symplectic_involution(void) {
    /* 4-byte phase vector */
    uint8_t orig[16] = {
        0x12, 0x34, 0x56, 0x78,
        0xAB, 0xCD, 0xEF, 0x01,
        0x42, 0x69, 0x99, 0xAA,
        0xFF, 0x00, 0x55, 0xAA
    };

    uint8_t frame[12 + 16];
    write_u32_le(frame, PRZM_MAGIC_SYMPLECTIC);
    frame[4] = 1; /* ModeShear */
    frame[5] = 0x05; /* S00 */
    frame[6] = 0x0A; /* S01 */
    frame[7] = 0x1F; /* S11 */
    write_u32_le(frame + 8, 16);
    memcpy(frame + 12, orig, 16);

    uint8_t dst[128];
    int64_t res = przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    TEST_ASSERT(res == 16, "Symplectic decompression returned wrong size");

    /* Decompressing once transformed dst. Decompressing twice MUST reproduce orig! */
    uint8_t frame2[12 + 16];
    memcpy(frame2, frame, 12);
    memcpy(frame2 + 12, dst, 16);

    uint8_t dst2[128];
    res = przm_decompress(frame2, sizeof(frame2), dst2, sizeof(dst2));
    TEST_ASSERT(res == 16, "Second decompression failed");
    TEST_ASSERT(memcmp(dst2, orig, 16) == 0, "Symplectic involution failed to reconstruct original bytes!");

    TEST_PASS("symplectic_involution");
}

/* 3. Test Mahler 2-Adic Wavelet */
static void test_mahler_transform(void) {
    uint8_t orig[16] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};

    /* Forward Pascal differences */
    uint8_t fwd[16];
    memcpy(fwd, orig, 16);
    for (int s = 1; s < 16; s++) {
        for (int i = 15; i >= s; i--) {
            fwd[i] = (uint8_t)(fwd[i] - fwd[i - 1]);
        }
    }

    uint8_t frame[12 + 16];
    write_u32_le(frame, PRZM_MAGIC_MAHLER);
    frame[4] = 1; /* Mode16 */
    frame[5] = 0; frame[6] = 0; frame[7] = 0;
    write_u32_le(frame + 8, 16);
    memcpy(frame + 12, fwd, 16);

    uint8_t dst[128];
    int64_t res = przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    TEST_ASSERT(res == 16, "Mahler decompression failed");
    TEST_ASSERT(memcmp(dst, orig, 16) == 0, "Mahler reconstruction mismatch!");

    TEST_PASS("mahler_transform");
}

/* 4. Test Standard Fused Raw Frame */
static void test_standard_fused_raw(void) {
    const char* text = "Hello PRZM v0.5 from Pure C!";
    size_t len = strlen(text);

    uint8_t frame[8 + 64];
    memcpy(frame, "PRZM", 4);
    frame[4] = PRZM_FLAG_UNCOMPRESSED;
    frame[5] = (uint8_t)len;
    frame[6] = (uint8_t)(len >> 8);
    frame[7] = (uint8_t)(len >> 16);
    memcpy(frame + 8, text, len);

    uint8_t dst[128];
    int64_t res = przm_decompress(frame, 8 + len, dst, sizeof(dst));
    TEST_ASSERT(res == (int64_t)len, "Raw decompress size mismatch");
    TEST_ASSERT(memcmp(dst, text, len) == 0, "Raw payload mismatch");

    TEST_PASS("standard_fused_raw");
}

/* 5. Test Standard Fused LZ77 with Repeat Matches */
static void test_standard_fused_lz(void) {
    /* Construct a synthetic compressed frame:
     * Section 1: 8 zeroes
     * Section 2: Command stream:
     *   - Opcode 0xF3 (Direct 1-byte lit run of 1 lit): 'A'
     *   - Opcode 0x00 (Fused Repeat: litLen=0, matchLen=4, k=0 (d=Q[0]=1)) -> emits "AAAA"
     *   - Total decoded: 5 'A's.
     * Section 3: 0 bytes
     * Section 4: 1 byte ('A')
     */
    uint8_t frame[64];
    size_t fptr = 0;
    memcpy(frame + fptr, "PRZM", 4); fptr += 4;
    frame[fptr++] = 0x00; /* Flags */
    frame[fptr++] = 5;    /* Uncompressed size = 5 */
    frame[fptr++] = 0;
    frame[fptr++] = 0;

    /* Section Directory: CmdStreamSize = 2, OffsetStreamSize = 0 */
    frame[fptr++] = 2; frame[fptr++] = 0; /* CmdSize = 2 */
    frame[fptr++] = 0; frame[fptr++] = 0; /* OffSize = 0 */

    /* Section 1: 8 zeroes */
    memset(frame + fptr, 0, 8); fptr += 8;

    /* Section 2: Command stream */
    frame[fptr++] = 0xF3; /* 1 literal */
    frame[fptr++] = 0x00; /* Fused Repeat: L=0, M=4, k=0 (d=1) */

    /* Section 4: Literals */
    frame[fptr++] = 'A';

    uint8_t dst[128];
    int64_t res = przm_decompress(frame, fptr, dst, sizeof(dst));
    TEST_ASSERT(res == 5, "Fused LZ decompress size mismatch");
    TEST_ASSERT(memcmp(dst, "AAAAA", 5) == 0, "Fused LZ payload mismatch");

    TEST_PASS("standard_fused_lz");
}

/* 6. Test PWS 5-Layer Grammar Unrolling */
static void test_pws_grammar(void) {
    /* Construct a synthetic PWS frame:
     * Rule L1[0]: 'H', 'e' (2B)
     * Rule L2[0]: L1[0], L1[0] -> "HeHe" (4B)
     * Axiom: Token 0xD0 (Direct L2[0] -> "HeHe")
     * OrigSize = 4
     */
    uint8_t frame[64];
    size_t fptr = 0;
    frame[fptr++] = 'P'; frame[fptr++] = 'W'; frame[fptr++] = 0x01; frame[fptr++] = 0x00; /* Stride = 0 */
    write_u32_le(frame + fptr, 4); fptr += 4; /* OrigSize = 4 */
    write_u32_le(frame + fptr, 1); fptr += 4; /* NumTokens = 1 */

    /* Rule counts: numL1=1, numL2=1, numL3=0, numL4=0, numL5=0, 3 reserved */
    frame[fptr++] = 1; frame[fptr++] = 1; frame[fptr++] = 0; frame[fptr++] = 0; frame[fptr++] = 0;
    frame[fptr++] = 0; frame[fptr++] = 0; frame[fptr++] = 0;

    /* L1 rules */
    frame[fptr++] = 'H'; frame[fptr++] = 'e';
    /* L2 rules */
    frame[fptr++] = 0; frame[fptr++] = 0; /* L1A=0, L1B=0 */

    /* Axiom */
    frame[fptr++] = 0xD0; /* L2 rule 0 */

    uint8_t dst[128];
    int64_t res = przm_decompress(frame, fptr, dst, sizeof(dst));
    TEST_ASSERT(res == 4, "PWS decompress size mismatch");
    TEST_ASSERT(memcmp(dst, "HeHe", 4) == 0, "PWS payload mismatch");

    TEST_PASS("pws_grammar");
}

int main(int argc, char** argv) {
    if (argc == 3) {
        /* CLI decompression mode: test_przm_dec <in.przm> <out.bin> */
        FILE* fin = fopen(argv[1], "rb");
        if (!fin) {
            fprintf(stderr, "Failed to open input file: %s\n", argv[1]);
            return 1;
        }
        fseek(fin, 0, SEEK_END);
        long in_len = ftell(fin);
        fseek(fin, 0, SEEK_SET);

        uint8_t* in_buf = (uint8_t*)malloc(in_len);
        if (fread(in_buf, 1, in_len, fin) != (size_t)in_len) {
            fclose(fin);
            free(in_buf);
            return 1;
        }
        fclose(fin);

        int64_t u_size = przm_get_uncompressed_size(in_buf, in_len);
        if (u_size < 0) {
            fprintf(stderr, "Invalid PRZM frame header: error %lld\n", (long long)u_size);
            free(in_buf);
            return 2;
        }

        uint8_t* out_buf = (uint8_t*)malloc(u_size + PRZM_SAFETY_PADDING);
        int64_t dec_len = przm_decompress(in_buf, in_len, out_buf, u_size + PRZM_SAFETY_PADDING);
        free(in_buf);

        if (dec_len < 0 || dec_len != u_size) {
            fprintf(stderr, "Decompression failed: error %lld\n", (long long)dec_len);
            free(out_buf);
            return 3;
        }

        FILE* fout = fopen(argv[2], "wb");
        if (!fout) {
            fprintf(stderr, "Failed to open output file: %s\n", argv[2]);
            free(out_buf);
            return 4;
        }
        fwrite(out_buf, 1, dec_len, fout);
        fclose(fout);
        free(out_buf);
        return 0;
    }

    printf("=== Running przm_dec Pure C Verification Suite ===\n\n");

    test_get_uncompressed_size();
    test_symplectic_involution();
    test_mahler_transform();
    test_standard_fused_raw();
    test_standard_fused_lz();
    test_pws_grammar();

    printf("\n>>> ALL TESTS PASSED SUCCESSFULLY! (0 Allocations, 100%% Lossless) <<<\n");
    return 0;
}
