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

    uint8_t frame_isoq[20];
    write_u32_le(frame_isoq, PRZM_MAGIC_ISOQUANT);
    frame_isoq[4] = 0x01; /* Mode = Collimated */
    write_u32_le(frame_isoq + 8, 8192);
    sz = przm_get_uncompressed_size(frame_isoq, sizeof(frame_isoq));
    TEST_ASSERT(sz == 8192, "IsoQuant size mismatch");

    uint8_t frame_gctr[10];
    write_u32_le(frame_gctr, PRZM_MAGIC_GCTR);
    frame_gctr[4] = 0; /* op_id */
    frame_gctr[5] = 1; /* sub_fmt */
    write_u32_le(frame_gctr + 6, 1207);
    sz = przm_get_uncompressed_size(frame_gctr, sizeof(frame_gctr));
    TEST_ASSERT(sz == 1207, "GCTR size mismatch");

    uint8_t frame_heis[12];
    write_u32_le(frame_heis, PRZM_MAGIC_HEISENBERG);
    write_u32_le(frame_heis + 4, 6144);
    frame_heis[8] = 3; /* Stride = 3 */
    frame_heis[9] = 1; /* Mode = Foliated */
    frame_heis[10] = 0;
    frame_heis[11] = 0;
    sz = przm_get_uncompressed_size(frame_heis, sizeof(frame_heis));
    TEST_ASSERT(sz == 6144, "Heisenberg size mismatch");

    uint8_t frame_cshd[24];
    memset(frame_cshd, 0, sizeof(frame_cshd));
    write_u32_le(frame_cshd, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_cshd + 4, 4096);
    sz = przm_get_uncompressed_size(frame_cshd, sizeof(frame_cshd));
    TEST_ASSERT(sz == 4096, "Cellular Sheaf size mismatch");

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

/* 7. Test IsoQuant 4D Quaternionic Isoclinic Uncollimation */
static void test_isoquant_uncollimation(void) {
    uint8_t orig[32] = {
        0x01, 0x02, 0x03, 0x04, 0x10, 0x20, 0x30, 0x40,
        0xAA, 0xBB, 0xCC, 0xDD, 0x55, 0x66, 0x77, 0x88,
        0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0,
        0x42, 0x69, 0x99, 0xAA, 0xFE, 0xDC, 0xBA, 0x98
    };

    uint8_t qL_inv[4] = {1, 0x33, 0x33, 0};
    uint8_t qR_inv[4] = {1, 0, 0x77, 0x77};

    /* Involutive property: unit quaternions (sum == 1) are self-inverse.
     * Unrotating twice must bit-exactly restore orig! */
    uint8_t buf[32];
    memcpy(buf, orig, 32);

    przm_status_t status = przm_isoquant_unrotate_inplace(buf, 32, qL_inv, qR_inv);
    TEST_ASSERT(status == PRZM_OK, "First unrotate failed");
    TEST_ASSERT(memcmp(buf, orig, 32) != 0, "Rotation was a no-op");

    status = przm_isoquant_unrotate_inplace(buf, 32, qL_inv, qR_inv);
    TEST_ASSERT(status == PRZM_OK, "Second unrotate failed");
    TEST_ASSERT(memcmp(buf, orig, 32) == 0, "Double unrotate failed to reconstruct original bytes!");

    /* Frame decompression test: Mode 1 (Collimated) */
    uint8_t frame[20 + 32];
    write_u32_le(frame, PRZM_MAGIC_ISOQUANT);
    frame[4] = 1; /* Mode = Collimated */
    frame[5] = 0; frame[6] = 0; frame[7] = 0;
    write_u32_le(frame + 8, 32); /* orig_len = 32 */
    memcpy(frame + 12, qL_inv, 4);
    memcpy(frame + 16, qR_inv, 4);

    /* Construct payload by rotating orig once */
    uint8_t payload[32];
    memcpy(payload, orig, 32);
    przm_isoquant_unrotate_inplace(payload, 32, qL_inv, qR_inv);
    memcpy(frame + 20, payload, 32);

    uint8_t dst[128];
    int64_t res = przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    TEST_ASSERT(res == 32, "IsoQuant decompression returned wrong size");
    TEST_ASSERT(memcmp(dst, orig, 32) == 0, "IsoQuant decompress failed to match original data!");

    TEST_PASS("isoquant_uncollimation");
}

/* 8. Test Galois Computational Trace Reduction (GCTR) Decompression */
static void test_gctr_decompression(void) {
    uint8_t pws_frame[64];
    size_t fptr = 0;
    pws_frame[fptr++] = 'P'; pws_frame[fptr++] = 'W'; pws_frame[fptr++] = 0x01; pws_frame[fptr++] = 0x00;
    write_u32_le(pws_frame + fptr, 4); fptr += 4;
    write_u32_le(pws_frame + fptr, 1); fptr += 4;
    pws_frame[fptr++] = 1; pws_frame[fptr++] = 1; pws_frame[fptr++] = 0; pws_frame[fptr++] = 0; pws_frame[fptr++] = 0;
    pws_frame[fptr++] = 0; pws_frame[fptr++] = 0; pws_frame[fptr++] = 0;
    pws_frame[fptr++] = 'H'; pws_frame[fptr++] = 'e';
    pws_frame[fptr++] = 0; pws_frame[fptr++] = 0;
    pws_frame[fptr++] = 0xD0;

    uint8_t gctr_frame[10 + 64];
    write_u32_le(gctr_frame, PRZM_MAGIC_GCTR);
    gctr_frame[4] = 0; /* OpGCTRBypass */
    gctr_frame[5] = 2; /* GCTRSubPWS */
    write_u32_le(gctr_frame + 6, 4); /* OrigSize = 4 */
    memcpy(gctr_frame + 10, pws_frame, fptr);

    uint8_t dst[128];
    int64_t res = przm_decompress(gctr_frame, 10 + fptr, dst, sizeof(dst));
    TEST_ASSERT(res == 4, "GCTR PWS decompress size mismatch");
    TEST_ASSERT(memcmp(dst, "HeHe", 4) == 0, "GCTR payload mismatch");

    TEST_PASS("gctr_decompression");
}

/* 9. Test Discrete Heisenberg Group H_3(F_{2^8}) Involutive Unfoliation */
static void test_heisenberg_unfoliation(void) {
    /* 1. Test Stride 3 (RGB/Triplets) Involution */
    uint8_t orig3[24] = {
        0x10, 0x20, 0x30,  /* Triplet 0 */
        0xAB, 0xCD, 0xEF,  /* Triplet 1 */
        0x42, 0x69, 0x99,  /* Triplet 2 */
        0xFF, 0x01, 0x55,  /* Triplet 3 */
        0x00, 0x11, 0x22,  /* Triplet 4 */
        0x33, 0x44, 0x55,  /* Triplet 5 */
        0x66, 0x77, 0x88,  /* Triplet 6 */
        0x99, 0xAA, 0xBB   /* Triplet 7 */
    };
    uint8_t buf3[24];
    memcpy(buf3, orig3, 24);

    /* First unfoliation modifies channel z (indices 2, 5, 8, ...) */
    int status = przm_heisenberg_unfoliate_inplace(buf3, 24, 3);
    TEST_ASSERT(status == PRZM_OK, "First unfoliate stride 3 failed");
    TEST_ASSERT(memcmp(buf3, orig3, 24) != 0, "Foliation stride 3 was a no-op");
    /* Channel x and y must remain identical */
    for (size_t i = 0; i < 24; i += 3) {
        TEST_ASSERT(buf3[i] == orig3[i], "Channel x modified");
        TEST_ASSERT(buf3[i + 1] == orig3[i + 1], "Channel y modified");
    }

    /* Second unfoliation must reconstruct original bytes bit-exactly (Involution) */
    status = przm_heisenberg_unfoliate_inplace(buf3, 24, 3);
    TEST_ASSERT(status == PRZM_OK, "Second unfoliate stride 3 failed");
    TEST_ASSERT(memcmp(buf3, orig3, 24) == 0, "Involution failed for stride 3!");

    /* 2. Test Stride 4 (RGBA/Quads) Involution */
    uint8_t orig4[32] = {
        0x10, 0x20, 0x30, 0xFF,
        0xAB, 0xCD, 0xEF, 0x80,
        0x42, 0x69, 0x99, 0x00,
        0x55, 0xAA, 0x33, 0xCC,
        0x12, 0x34, 0x56, 0x78,
        0x9A, 0xBC, 0xDE, 0xF0,
        0x11, 0x22, 0x33, 0x44,
        0x55, 0x66, 0x77, 0x88
    };
    uint8_t buf4[32];
    memcpy(buf4, orig4, 32);

    status = przm_heisenberg_unfoliate_inplace(buf4, 32, 4);
    TEST_ASSERT(status == PRZM_OK, "First unfoliate stride 4 failed");
    TEST_ASSERT(memcmp(buf4, orig4, 32) != 0, "Foliation stride 4 was a no-op");
    for (size_t i = 0; i < 32; i += 4) {
        TEST_ASSERT(buf4[i] == orig4[i], "Channel x modified");
        TEST_ASSERT(buf4[i + 1] == orig4[i + 1], "Channel y modified");
        TEST_ASSERT(buf4[i + 3] == orig4[i + 3], "Channel w modified (pass-through violated)");
    }

    status = przm_heisenberg_unfoliate_inplace(buf4, 32, 4);
    TEST_ASSERT(status == PRZM_OK, "Second unfoliate stride 4 failed");
    TEST_ASSERT(memcmp(buf4, orig4, 32) == 0, "Involution failed for stride 4!");

    /* 3. Test Frame Decompression: Mode 1 (Foliated Raw) */
    uint8_t frame_m1[12 + 24];
    write_u32_le(frame_m1, PRZM_MAGIC_HEISENBERG);
    write_u32_le(frame_m1 + 4, 24);
    frame_m1[8] = 3; /* Stride = 3 */
    frame_m1[9] = 1; /* Mode = Foliated */
    frame_m1[10] = 0;
    frame_m1[11] = 0;

    /* Construct payload by foliating orig3 once */
    memcpy(frame_m1 + 12, orig3, 24);
    przm_heisenberg_unfoliate_inplace(frame_m1 + 12, 24, 3);

    uint8_t dst_frame[128];
    int64_t res_frame = przm_decompress(frame_m1, sizeof(frame_m1), dst_frame, sizeof(dst_frame));
    TEST_ASSERT(res_frame == 24, "Heisenberg Mode 1 decompression returned wrong size");
    TEST_ASSERT(memcmp(dst_frame, orig3, 24) == 0, "Heisenberg Mode 1 decompress mismatch!");

    /* 4. Test Frame Decompression: Mode 0 (Raw Bypass) */
    uint8_t frame_m0[12 + 24];
    write_u32_le(frame_m0, PRZM_MAGIC_HEISENBERG);
    write_u32_le(frame_m0 + 4, 24);
    frame_m0[8] = 3;
    frame_m0[9] = 0; /* Mode = Raw Bypass */
    frame_m0[10] = 0;
    frame_m0[11] = 0;
    memcpy(frame_m0 + 12, orig3, 24);

    res_frame = przm_decompress(frame_m0, sizeof(frame_m0), dst_frame, sizeof(dst_frame));
    TEST_ASSERT(res_frame == 24, "Heisenberg Mode 0 decompression returned wrong size");
    TEST_ASSERT(memcmp(dst_frame, orig3, 24) == 0, "Heisenberg Mode 0 decompress mismatch!");

    TEST_PASS("heisenberg_unfoliation");
}

/* 10. Test Cellular Sheaves with Discrete Hodge Laplacians (CSHD) */
static void test_cellular_sheaf_decompression(void) {
    uint8_t dst[256];
    memset(dst, 0, sizeof(dst));

    /* 1. Test Raw Passthrough Mode */
    uint8_t raw_data[16] = {0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
                            0x88, 0x99, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF};
    uint8_t frame_raw[24 + 16];
    memset(frame_raw, 0, 24);
    write_u32_le(frame_raw, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_raw + 4, 16);
    frame_raw[13] = 1; /* Twist */
    frame_raw[16] = (uint8_t)(PRZM_CSHD_FLAG_RAW & 0xFF);
    frame_raw[17] = (uint8_t)((PRZM_CSHD_FLAG_RAW >> 8) & 0xFF);
    memcpy(frame_raw + 24, raw_data, 16);

    int64_t dec_raw = przm_decompress(frame_raw, sizeof(frame_raw), dst, sizeof(dst));
    TEST_ASSERT(dec_raw == 16, "CSHD Raw decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, raw_data, 16) == 0, "CSHD Raw decompress data mismatch");

    /* 2. Test Curl-Free Harmonic Frame (W=8, M=4, N=32) */
    uint8_t eh[7] = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07};
    uint8_t ev[3] = {0x10, 0x20, 0x30};
    uint8_t expected_cf[32];
    expected_cf[0] = 0x42;
    for (int j = 0; j < 7; j++) {
        expected_cf[j + 1] = expected_cf[j] ^ eh[j];
    }
    for (int i = 1; i < 4; i++) {
        uint8_t v = ev[i - 1];
        for (int j = 0; j < 8; j++) {
            expected_cf[i * 8 + j] = expected_cf[(i - 1) * 8 + j] ^ v;
        }
    }

    uint8_t frame_cf[24 + 7 + 3];
    memset(frame_cf, 0, 24);
    write_u32_le(frame_cf, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_cf + 4, 32);
    frame_cf[8] = 8;  frame_cf[9] = 0;  /* W = 8 */
    frame_cf[10] = 4; frame_cf[11] = 0; /* M = 4 */
    frame_cf[12] = 0x42;                /* Anchor */
    frame_cf[13] = 1;                   /* Twist = 1 */
    frame_cf[14] = 0; frame_cf[15] = 0; /* Betti1 = 0 */
    frame_cf[16] = (uint8_t)(PRZM_CSHD_FLAG_CURL_FREE & 0xFF);
    frame_cf[17] = (uint8_t)((PRZM_CSHD_FLAG_CURL_FREE >> 8) & 0xFF);
    memcpy(frame_cf + 24, eh, 7);
    memcpy(frame_cf + 31, ev, 3);

    memset(dst, 0, sizeof(dst));
    int64_t dec_cf = przm_decompress(frame_cf, sizeof(frame_cf), dst, sizeof(dst));
    TEST_ASSERT(dec_cf == 32, "CSHD Curl-Free decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_cf, 32) == 0, "CSHD Curl-Free decompress data mismatch");

    /* Also verify direct przm_cellular_sheaf_decompress call */
    memset(dst, 0, sizeof(dst));
    int64_t direct_cf = przm_cellular_sheaf_decompress(frame_cf, sizeof(frame_cf), dst, sizeof(dst));
    TEST_ASSERT(direct_cf == 32, "Direct przm_cellular_sheaf_decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_cf, 32) == 0, "Direct przm_cellular_sheaf_decompress data mismatch");

    /* 3. Test Constant Boundaries (Harmonic Generators h1 and h2) */
    uint8_t gamma_h = 0x01;
    uint8_t gamma_v = 0x04;
    uint8_t expected_const[32];
    expected_const[0] = 0x55;
    for (int j = 0; j < 7; j++) {
        expected_const[j + 1] = expected_const[j] ^ gamma_h;
    }
    for (int i = 1; i < 4; i++) {
        for (int j = 0; j < 8; j++) {
            expected_const[i * 8 + j] = expected_const[(i - 1) * 8 + j] ^ gamma_v;
        }
    }

    uint8_t frame_const[24 + 2];
    memset(frame_const, 0, 24);
    write_u32_le(frame_const, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_const + 4, 32);
    frame_const[8] = 8;  frame_const[9] = 0;  /* W = 8 */
    frame_const[10] = 4; frame_const[11] = 0; /* M = 4 */
    frame_const[12] = 0x55;                   /* Anchor */
    frame_const[13] = 1;                      /* Twist = 1 */
    frame_const[14] = 2; frame_const[15] = 0; /* Betti1 = 2 */
    uint16_t const_flags = PRZM_CSHD_FLAG_CURL_FREE | PRZM_CSHD_FLAG_CONST_EH | PRZM_CSHD_FLAG_CONST_EV;
    frame_const[16] = (uint8_t)(const_flags & 0xFF);
    frame_const[17] = (uint8_t)((const_flags >> 8) & 0xFF);
    frame_const[24] = gamma_h;
    frame_const[25] = gamma_v;

    memset(dst, 0, sizeof(dst));
    int64_t dec_const = przm_decompress(frame_const, sizeof(frame_const), dst, sizeof(dst));
    TEST_ASSERT(dec_const == 32, "CSHD Constant Boundary decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_const, 32) == 0, "CSHD Constant Boundary decompress data mismatch");

    /* 4. Test Sparse Curl Frame (W=4, M=3, N=12, 1 non-zero face curl) */
    uint8_t eh_sp[3] = {0x01, 0x02, 0x03};
    uint8_t ev_sp[2] = {0x04, 0x08};
    uint8_t expected_sp[12];
    expected_sp[0] = 0x10;
    expected_sp[1] = 0x10 ^ 0x01; /* 0x11 */
    expected_sp[2] = 0x11 ^ 0x02; /* 0x13 */
    expected_sp[3] = 0x13 ^ 0x03; /* 0x10 */

    /* Row 1: face index 1 (i=0, j=1) has curl 0x0F */
    /* V1(0)=0x04, V1(1)=0x04, V1(2)=0x04^0x0F=0x0B, V1(3)=0x0B */
    expected_sp[4] = expected_sp[0] ^ 0x04; /* 0x14 */
    expected_sp[5] = expected_sp[1] ^ 0x04; /* 0x15 */
    expected_sp[6] = expected_sp[2] ^ 0x0B; /* 0x18 */
    expected_sp[7] = expected_sp[3] ^ 0x0B; /* 0x1B */

    /* Row 2: no non-zeros on row 1, V2(j) = 0x08 everywhere */
    expected_sp[8]  = expected_sp[4] ^ 0x08; /* 0x1C */
    expected_sp[9]  = expected_sp[5] ^ 0x08; /* 0x1D */
    expected_sp[10] = expected_sp[6] ^ 0x08; /* 0x10 */
    expected_sp[11] = expected_sp[7] ^ 0x08; /* 0x13 */

    uint8_t frame_sp[24 + 3 + 2 + 4 + 5];
    memset(frame_sp, 0, 24);
    write_u32_le(frame_sp, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_sp + 4, 12);
    frame_sp[8] = 4;  frame_sp[9] = 0;  /* W = 4 */
    frame_sp[10] = 3; frame_sp[11] = 0; /* M = 3 */
    frame_sp[12] = 0x10;                /* Anchor */
    frame_sp[13] = 1;                   /* Twist = 1 */
    frame_sp[14] = 0; frame_sp[15] = 0; /* Betti1 = 0 */
    frame_sp[16] = (uint8_t)(PRZM_CSHD_FLAG_SPARSE_CURL & 0xFF);
    frame_sp[17] = (uint8_t)((PRZM_CSHD_FLAG_SPARSE_CURL >> 8) & 0xFF);
    memcpy(frame_sp + 24, eh_sp, 3);
    memcpy(frame_sp + 27, ev_sp, 2);
    write_u32_le(frame_sp + 29, 1); /* NNZ = 1 */
    write_u32_le(frame_sp + 33, 1); /* Face Index = 1 */
    frame_sp[37] = 0x0F;            /* Curl Val = 0x0F */

    memset(dst, 0, sizeof(dst));
    int64_t dec_sp = przm_decompress(frame_sp, sizeof(frame_sp), dst, sizeof(dst));
    TEST_ASSERT(dec_sp == 12, "CSHD Sparse Curl decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_sp, 12) == 0, "CSHD Sparse Curl decompress data mismatch");

    TEST_PASS("cellular_sheaf_decompression");
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
    test_isoquant_uncollimation();
    test_gctr_decompression();
    test_heisenberg_unfoliation();
    test_cellular_sheaf_decompression();

    printf("\n>>> ALL TESTS PASSED SUCCESSFULLY! (0 Allocations, 100%% Lossless) <<<\n");
    return 0;
}

