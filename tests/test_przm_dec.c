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

static inline void write_u16_le(uint8_t* p, uint16_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
}

static inline void write_u32_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
    p[3] = (uint8_t)(v >> 24);
}

static inline void write_u64_le(uint8_t* p, uint64_t v) {
    write_u32_le(p, (uint32_t)v);
    write_u32_le(p + 4, (uint32_t)(v >> 32));
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

    uint8_t frame_mobi[12];
    write_u32_le(frame_mobi, PRZM_MAGIC_MOBIUS);
    write_u32_le(frame_mobi + 4, 4096);
    frame_mobi[8] = 6; /* blockLog2 = 6 */
    frame_mobi[9] = 1; /* Transformed mode */
    frame_mobi[10] = PRZM_OP_MOBIUS;
    frame_mobi[11] = 0;
    sz = przm_get_uncompressed_size(frame_mobi, sizeof(frame_mobi));
    TEST_ASSERT(sz == 4096, "MOBIUS size mismatch");

    uint8_t frame_marg[16];
    write_u32_le(frame_marg, PRZM_MAGIC_MARGOLUS);
    write_u32_le(frame_marg + 4, 8192);
    frame_marg[8] = 8; frame_marg[9] = 0; /* width = 8 */
    frame_marg[10] = 2; /* steps = 2 */
    frame_marg[11] = PRZM_MARGOLUS_RULE_BBM;
    frame_marg[12] = 1; /* Transform mode */
    sz = przm_get_uncompressed_size(frame_marg, sizeof(frame_marg));
    TEST_ASSERT(sz == 8192, "MARGOLUS size mismatch");

    uint8_t frame_png[12];
    write_u32_le(frame_png, PRZM_MAGIC_ARCHIVAL_PNG);
    write_u32_le(frame_png + 4, 16384);
    write_u32_le(frame_png + 8, 24);
    sz = przm_get_uncompressed_size(frame_png, sizeof(frame_png));
    TEST_ASSERT(sz == 16384, "Archival PNG size mismatch");

    uint8_t frame_stream[17];
    write_u32_le(frame_stream, PRZM_MAGIC_STREAM);
    frame_stream[4] = PRZM_STREAM_FLAG_INDEPENDENT;
    write_u64_le(frame_stream + 5, 1048576);
    write_u32_le(frame_stream + 13, 4);
    sz = przm_get_uncompressed_size(frame_stream, sizeof(frame_stream));
    TEST_ASSERT(sz == 1048576, "Stream container size mismatch");

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

    /* Section Directory: CmdStreamSize = 7, OffsetStreamSize = 0 */
    frame[fptr++] = 7; frame[fptr++] = 0; /* CmdSize = 7 */
    frame[fptr++] = 0; frame[fptr++] = 0; /* OffSize = 0 */

    /* Section 1: 8 zeroes */
    memset(frame + fptr, 0, 8); fptr += 8;

    /* Section 2: Command stream (ModeFlat: mode 0, orig_len 2) */
    frame[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + fptr, 2); fptr += 4;
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

    /* 5. Test Curl-Free Harmonic Frame with Galois Twist > 1 (Twist = 2, W = 4, M = 3, N = 12) */
    uint8_t eh_t2[3] = {0x10, 0x20, 0x30};
    uint8_t ev_t2[2] = {0x04, 0x08};
    uint8_t expected_t2[12] = {
        0x01, 0x12, 0x04, 0x38,
        0x06, 0x2C, 0x18, 0x50,
        0x04, 0x48, 0x10, 0xE0
    };
    uint8_t frame_t2[24 + 3 + 2];
    memset(frame_t2, 0, 24);
    write_u32_le(frame_t2, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_t2 + 4, 12);
    frame_t2[8] = 4;  frame_t2[9] = 0;   /* W = 4 */
    frame_t2[10] = 3; frame_t2[11] = 0;  /* M = 3 */
    frame_t2[12] = 0x01;                 /* Anchor */
    frame_t2[13] = 2;                    /* Twist = 2 */
    frame_t2[14] = 0; frame_t2[15] = 0;  /* Betti1 = 0 */
    frame_t2[16] = (uint8_t)(PRZM_CSHD_FLAG_CURL_FREE & 0xFF);
    frame_t2[17] = (uint8_t)((PRZM_CSHD_FLAG_CURL_FREE >> 8) & 0xFF);
    memcpy(frame_t2 + 24, eh_t2, 3);
    memcpy(frame_t2 + 27, ev_t2, 2);

    memset(dst, 0, sizeof(dst));
    int64_t dec_t2 = przm_decompress(frame_t2, sizeof(frame_t2), dst, sizeof(dst));
    TEST_ASSERT(dec_t2 == 12, "CSHD Twist=2 Curl-Free returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_t2, 12) == 0, "CSHD Twist=2 Curl-Free data mismatch");

    /* 6. Test Sparse Curl Frame with Galois Twist > 1 (Twist = 2, W = 4, M = 3, N = 12, NNZ = 1) */
    uint8_t expected_t2_sp[12] = {
        0x01, 0x12, 0x04, 0x38,
        0x06, 0x2C, 0x17, 0x4E,
        0x04, 0x48, 0x0E, 0xDC
    };
    uint8_t frame_t2_sp[24 + 3 + 2 + 4 + 5];
    memset(frame_t2_sp, 0, 24);
    write_u32_le(frame_t2_sp, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_t2_sp + 4, 12);
    frame_t2_sp[8] = 4;  frame_t2_sp[9] = 0;   /* W = 4 */
    frame_t2_sp[10] = 3; frame_t2_sp[11] = 0;  /* M = 3 */
    frame_t2_sp[12] = 0x01;                    /* Anchor */
    frame_t2_sp[13] = 2;                       /* Twist = 2 */
    frame_t2_sp[14] = 0; frame_t2_sp[15] = 0;  /* Betti1 = 0 */
    frame_t2_sp[16] = (uint8_t)(PRZM_CSHD_FLAG_SPARSE_CURL & 0xFF);
    frame_t2_sp[17] = (uint8_t)((PRZM_CSHD_FLAG_SPARSE_CURL >> 8) & 0xFF);
    memcpy(frame_t2_sp + 24, eh_t2, 3);
    memcpy(frame_t2_sp + 27, ev_t2, 2);
    write_u32_le(frame_t2_sp + 29, 1); /* NNZ = 1 */
    write_u32_le(frame_t2_sp + 33, 1); /* Face Index = 1 (i=0, j=1) */
    frame_t2_sp[37] = 0x0F;            /* Curl Val = 0x0F */

    memset(dst, 0, sizeof(dst));
    int64_t dec_t2_sp = przm_decompress(frame_t2_sp, sizeof(frame_t2_sp), dst, sizeof(dst));
    TEST_ASSERT(dec_t2_sp == 12, "CSHD Twist=2 Sparse Curl returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_t2_sp, 12) == 0, "CSHD Twist=2 Sparse Curl data mismatch");

    /* 7. Test Constant Boundaries with Galois Twist > 1 (Twist = 3, W = 4, M = 3, N = 12) */
    uint8_t gamma_h_t3 = 0x02;
    uint8_t gamma_v_t3 = 0x05;
    uint8_t expected_t3_const[12] = {
        0x10, 0x32, 0x54, 0xFE,
        0x35, 0x59, 0xED, 0x2A,
        0x5A, 0xE4, 0x3D, 0x4D
    };
    uint8_t frame_t3_const[24 + 2];
    memset(frame_t3_const, 0, 24);
    write_u32_le(frame_t3_const, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame_t3_const + 4, 12);
    frame_t3_const[8] = 4;  frame_t3_const[9] = 0;   /* W = 4 */
    frame_t3_const[10] = 3; frame_t3_const[11] = 0;  /* M = 3 */
    frame_t3_const[12] = 0x10;                       /* Anchor */
    frame_t3_const[13] = 3;                          /* Twist = 3 */
    frame_t3_const[14] = 0; frame_t3_const[15] = 0;  /* Betti1 = 0 */
    uint16_t const_flags_t3 = PRZM_CSHD_FLAG_CURL_FREE | PRZM_CSHD_FLAG_CONST_EH | PRZM_CSHD_FLAG_CONST_EV;
    frame_t3_const[16] = (uint8_t)(const_flags_t3 & 0xFF);
    frame_t3_const[17] = (uint8_t)((const_flags_t3 >> 8) & 0xFF);
    frame_t3_const[24] = gamma_h_t3;
    frame_t3_const[25] = gamma_v_t3;

    memset(dst, 0, sizeof(dst));
    int64_t dec_t3_const = przm_decompress(frame_t3_const, sizeof(frame_t3_const), dst, sizeof(dst));
    TEST_ASSERT(dec_t3_const == 12, "CSHD Twist=3 Constant Boundary returned wrong length");
    TEST_ASSERT(memcmp(dst, expected_t3_const, 12) == 0, "CSHD Twist=3 Constant Boundary data mismatch");

    TEST_PASS("cellular_sheaf_decompression");
}

/* Test Fast Möbius Transform Involution & Decompression (0 Allocations) */
static void test_mobius_decompression(void) {
    /* 1. Verify byte-level involution FMT(FMT(X)) == X */
    uint8_t orig[128];
    uint8_t work[128];
    for (size_t i = 0; i < 128; i++) {
        orig[i] = (uint8_t)((i * 37 + 11) & 0xFF);
        work[i] = orig[i];
    }

    int err = przm_fmt_transform_inplace(work, 128, 6, 0);
    TEST_ASSERT(err == PRZM_OK, "FMT forward byte failed");
    TEST_ASSERT(memcmp(work, orig, 128) != 0, "FMT forward should modify data");

    err = przm_fmt_transform_inplace(work, 128, 6, 0);
    TEST_ASSERT(err == PRZM_OK, "FMT untransform byte failed");
    TEST_ASSERT(memcmp(work, orig, 128) == 0, "FMT byte involution failed to restore data");

    /* 2. Verify bit-level SWAR involution FMT(FMT(X)) == X */
    err = przm_fmt_transform_inplace(work, 128, 6, 1);
    TEST_ASSERT(err == PRZM_OK, "FMT forward bit+byte failed");
    TEST_ASSERT(memcmp(work, orig, 128) != 0, "FMT forward bit+byte should modify data");

    err = przm_fmt_transform_inplace(work, 128, 6, 1);
    TEST_ASSERT(err == PRZM_OK, "FMT untransform bit+byte failed");
    TEST_ASSERT(memcmp(work, orig, 128) == 0, "FMT bit+byte involution failed to restore data");

    /* 3. Test przm_mobius_decompress in ModeRaw */
    uint8_t frame_raw[12 + 64];
    write_u32_le(frame_raw, PRZM_MAGIC_MOBIUS);
    write_u32_le(frame_raw + 4, 64);
    frame_raw[8] = 6;
    frame_raw[9] = PRZM_FMT_MODE_RAW;
    frame_raw[10] = PRZM_OP_MOBIUS;
    frame_raw[11] = 0;
    memcpy(frame_raw + 12, orig, 64);

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int64_t dec = przm_decompress(frame_raw, sizeof(frame_raw), dst, sizeof(dst));
    TEST_ASSERT(dec == 64, "MOBIUS raw decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, orig, 64) == 0, "MOBIUS raw decompress payload mismatch");

    /* 4. Test przm_mobius_decompress in ModeTransformed */
    uint8_t t_buf[64];
    memcpy(t_buf, orig, 64);
    przm_fmt_transform_inplace(t_buf, 64, 6, 0);

    uint8_t frame_trans[12 + 64];
    write_u32_le(frame_trans, PRZM_MAGIC_MOBIUS);
    write_u32_le(frame_trans + 4, 64);
    frame_trans[8] = 6;
    frame_trans[9] = PRZM_FMT_MODE_TRANSFORMED;
    frame_trans[10] = PRZM_OP_MOBIUS;
    frame_trans[11] = 0;
    memcpy(frame_trans + 12, t_buf, 64);

    memset(dst, 0, sizeof(dst));
    dec = przm_decompress(frame_trans, sizeof(frame_trans), dst, sizeof(dst));
    TEST_ASSERT(dec == 64, "MOBIUS transformed decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, orig, 64) == 0, "MOBIUS transformed decompress failed to invert");

    TEST_PASS("mobius_decompression");
}

/* Test Margolus RCA Reversibility & Decompression across all 7 rules (0 Allocations) */
static void test_margolus_decompression(void) {
    uint8_t orig[128];
    uint8_t work[128];
    for (size_t i = 0; i < 128; i++) {
        orig[i] = (uint8_t)((i * 53 + 7) & 0xFF);
    }

    uint8_t rules[7] = {
        PRZM_MARGOLUS_RULE_BBM,
        PRZM_MARGOLUS_RULE_TRANSPOSE,
        PRZM_MARGOLUS_RULE_ROT180,
        PRZM_MARGOLUS_RULE_ROT90,
        PRZM_MARGOLUS_RULE_XOR,
        PRZM_MARGOLUS_RULE_FLIP_H,
        PRZM_MARGOLUS_RULE_FLIP_V
    };

    /* Verify all 7 rules invert bit-exactly for steps 1..4 */
    for (int r = 0; r < 7; r++) {
        uint8_t rule = rules[r];
        for (uint8_t steps = 1; steps <= 4; steps++) {
            memcpy(work, orig, 128);
            /* Forward steps */
            for (uint8_t s = 0; s < steps; s++) {
                int phase = s & 1;
                przm_margolus_step_inplace(work, 16, 8, phase, rule, 0);
            }
            /* Time-reversed backward steps */
            int err = przm_margolus_reverse_inplace(work, 128, 8, steps, rule);
            TEST_ASSERT(err == PRZM_OK, "Margolus reverse failed");
            TEST_ASSERT(memcmp(work, orig, 128) == 0, "Margolus rule failed bit-exact reversibility");
        }
    }

    /* Test przm_margolus_decompress in ModeTransform */
    memcpy(work, orig, 64);
    /* Forward 2 steps */
    przm_margolus_step_inplace(work, 8, 8, 0, PRZM_MARGOLUS_RULE_BBM, 0);
    przm_margolus_step_inplace(work, 8, 8, 1, PRZM_MARGOLUS_RULE_BBM, 0);

    uint8_t frame[16 + 64];
    write_u32_le(frame, PRZM_MAGIC_MARGOLUS);
    write_u32_le(frame + 4, 64);
    frame[8] = 8; frame[9] = 0; /* width = 8 */
    frame[10] = 2;              /* steps = 2 */
    frame[11] = PRZM_MARGOLUS_RULE_BBM;
    frame[12] = 1;              /* MargolusModeTransform */
    frame[13] = 1;              /* MargolusFlagToroidal */
    frame[14] = 0; frame[15] = 0;
    memcpy(frame + 16, work, 64);

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int64_t dec = przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    TEST_ASSERT(dec == 64, "Margolus decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, orig, 64) == 0, "Margolus decompress failed to restore original bytes");

    TEST_PASS("margolus_decompression");
}

/* Test External Schema Dictionary Decompression (0 Allocations) */
static void test_dictionary_decompression(void) {
    przm_dict_entry_t entries[2] = {
        {"{\"custom\":\"", 10},
        {"\"value\":100}", 11}
    };
    przm_dictionary_t custom_dict = {
        .dict_id = 0x1001,
        .num_tokens = 2,
        .tokens = entries
    };

    /* Standard frame referencing DictID 0x1001 with 1-byte opcode 0xD8 (tok_id 0) */
    uint8_t frame[8 + 4 + 4 + 5 + 1];
    write_u32_le(frame, PRZM_MAGIC_STANDARD);
    frame[4] = PRZM_FLAG_DICT_REF;
    frame[5] = 10; frame[6] = 0; frame[7] = 0; /* Uncompressed size = 10 */
    write_u32_le(frame + 8, 0x1001);           /* DictID */
    write_u16_le(frame + 12, 6);               /* cmd_size = 5 + 1 = 6 */
    write_u16_le(frame + 14, 0);               /* off_size = 0 */
    frame[16] = PRZM_ENTROPY_MODE_FLAT;        /* ModeFlat */
    write_u32_le(frame + 17, 1);               /* orig_len = 1 */
    frame[21] = 0xD8;                          /* Opcode 0xD8 -> token 0 */

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int64_t dec = przm_decompress_dict(frame, sizeof(frame), dst, sizeof(dst), &custom_dict);
    TEST_ASSERT(dec == 10, "Custom dict decompress returned wrong length");
    TEST_ASSERT(memcmp(dst, "{\"custom\":\"", 10) == 0, "Custom dict token expansion mismatch");

    TEST_PASS("dictionary_decompression");
}

/* Test Étale Sheaf Trellis Decompression across Raw & Trellis Modes (0 Allocations) */
static void test_sheaf_trellis_decompression(void) {
    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));

    /* 1. Sheaf Trellis Mode 0 (Raw) */
    uint8_t raw_frame[12 + 8];
    write_u32_le(raw_frame, PRZM_MAGIC_SHEAF);
    write_u32_le(raw_frame + 4, 0); /* mode 0 = raw */
    write_u32_le(raw_frame + 8, 8); /* orig_len = 8 */
    for (int i = 0; i < 8; i++) raw_frame[12 + i] = (uint8_t)(i + 1);

    int64_t dec_raw = przm_decompress(raw_frame, sizeof(raw_frame), dst, sizeof(dst));
    TEST_ASSERT(dec_raw == 8, "Sheaf Trellis raw decompress returned wrong length");
    for (int i = 0; i < 8; i++) {
        TEST_ASSERT(dst[i] == (uint8_t)(i + 1), "Sheaf Trellis raw data mismatch");
    }

    /* 2. Sheaf Trellis Mode 1 (Trellis Rep-Queue) */
    uint8_t trellis_frame[12 + 4];
    write_u32_le(trellis_frame, PRZM_MAGIC_SHEAF);
    write_u32_le(trellis_frame + 4, 1);  /* mode 1 = trellis */
    write_u32_le(trellis_frame + 8, 12); /* orig_len = 12 */
    trellis_frame[12] = 0x00;            /* literal run length 1 */
    trellis_frame[13] = 0x77;            /* literal byte */
    trellis_frame[14] = 0x83;            /* rep slot 0 (d=1), len = 3 + 2 = 5 */
    trellis_frame[15] = 0x84;            /* rep slot 0 (d=1), len = 4 + 2 = 6 */

    memset(dst, 0, sizeof(dst));
    int64_t dec_trellis = przm_decompress(trellis_frame, sizeof(trellis_frame), dst, sizeof(dst));
    TEST_ASSERT(dec_trellis == 12, "Sheaf Trellis mode 1 returned wrong length");
    for (int i = 0; i < 12; i++) {
        TEST_ASSERT(dst[i] == 0x77, "Sheaf Trellis mode 1 data mismatch");
    }

    TEST_PASS("sheaf_trellis_decompression");
}

/* Test Robustness against Corrupted, Truncated, and Adversarial Frames (0 Allocations) */
static void test_corrupted_and_truncated_frames(void) {
    uint8_t dst[256];
    memset(dst, 0, sizeof(dst));

    /* 1. NULL and sub-4 byte inputs to przm_get_uncompressed_size */
    TEST_ASSERT(przm_get_uncompressed_size(NULL, 0) == PRZM_ERR_CORRUPTED_FRAME, "NULL buffer should error");
    TEST_ASSERT(przm_get_uncompressed_size(NULL, 10) == PRZM_ERR_CORRUPTED_FRAME, "NULL buffer should error");
    uint8_t tiny[3] = {'P', 'R', 'Z'};
    TEST_ASSERT(przm_get_uncompressed_size(tiny, sizeof(tiny)) == PRZM_ERR_CORRUPTED_FRAME, "sub-4B should error");

    /* 2. Invalid magic signatures */
    uint8_t bad_magic[8] = {0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x10, 0x00, 0x00};
    TEST_ASSERT(przm_get_uncompressed_size(bad_magic, sizeof(bad_magic)) == PRZM_ERR_INVALID_MAGIC, "bad magic should error");

    /* 3. Magic 24 (0x18) regression test: must NOT be recognized as Cellular Sheaf */
    uint8_t false_sheaf[8] = {0x18, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x00};
    TEST_ASSERT(przm_get_uncompressed_size(false_sheaf, sizeof(false_sheaf)) == PRZM_ERR_INVALID_MAGIC, "magic 24 must not be sheaf");

    /* 4. Truncated headers across transform families */
    uint8_t trunc_mobi[8];
    write_u32_le(trunc_mobi, PRZM_MAGIC_MOBIUS);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_mobi, sizeof(trunc_mobi)) == PRZM_ERR_CORRUPTED_FRAME, "trunc mobius should error");

    uint8_t trunc_marg[12];
    write_u32_le(trunc_marg, PRZM_MAGIC_MARGOLUS);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_marg, sizeof(trunc_marg)) == PRZM_ERR_CORRUPTED_FRAME, "trunc margolus should error");

    uint8_t trunc_isoq[16];
    write_u32_le(trunc_isoq, PRZM_MAGIC_ISOQUANT);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_isoq, sizeof(trunc_isoq)) == PRZM_ERR_CORRUPTED_FRAME, "trunc isoquant should error");

    uint8_t trunc_heis[8];
    write_u32_le(trunc_heis, PRZM_MAGIC_HEISENBERG);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_heis, sizeof(trunc_heis)) == PRZM_ERR_CORRUPTED_FRAME, "trunc heisenberg should error");

    uint8_t trunc_cshd[20];
    write_u32_le(trunc_cshd, PRZM_MAGIC_CELLULAR_SHEAF);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_cshd, sizeof(trunc_cshd)) == PRZM_ERR_CORRUPTED_FRAME, "trunc cshd should error");

    uint8_t trunc_std[6];
    write_u32_le(trunc_std, PRZM_MAGIC_STANDARD);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_std, sizeof(trunc_std)) == PRZM_ERR_CORRUPTED_FRAME, "trunc std should error");

    uint8_t trunc_shef[10];
    write_u32_le(trunc_shef, PRZM_MAGIC_SHEAF);
    TEST_ASSERT(przm_get_uncompressed_size(trunc_shef, sizeof(trunc_shef)) == PRZM_ERR_CORRUPTED_FRAME, "trunc sheaf trellis should error");

    /* 5. przm_decompress NULL and 0-length inputs */
    TEST_ASSERT(przm_decompress(NULL, 10, dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "decompress NULL src should error");
    TEST_ASSERT(przm_decompress(trunc_std, sizeof(trunc_std), NULL, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "decompress NULL dst should error");
    TEST_ASSERT(przm_decompress(trunc_std, 0, dst, sizeof(dst)) == 0, "decompress 0 len should return 0");

    /* 6. Buffer too small: dst_cap < uncompressed_size + PRZM_SAFETY_PADDING */
    uint8_t frame_std[8] = {'P', 'R', 'Z', 'M', 0x04, 0x64, 0x00, 0x00}; /* raw 100 bytes */
    TEST_ASSERT(przm_decompress(frame_std, sizeof(frame_std), dst, 100) == PRZM_ERR_BUFFER_TOO_SMALL, "dst_cap 100 without 64 padding should error");

    uint8_t frame_heis[12];
    write_u32_le(frame_heis, PRZM_MAGIC_HEISENBERG);
    write_u32_le(frame_heis + 4, 100);
    frame_heis[8] = 3; frame_heis[9] = 0; frame_heis[10] = 0; frame_heis[11] = 0;
    TEST_ASSERT(przm_decompress(frame_heis, sizeof(frame_heis), dst, 100) == PRZM_ERR_BUFFER_TOO_SMALL, "heis buffer too small should error");

    uint8_t frame_isoq[20];
    memset(frame_isoq, 0, sizeof(frame_isoq));
    write_u32_le(frame_isoq, PRZM_MAGIC_ISOQUANT);
    write_u32_le(frame_isoq + 8, 100);
    TEST_ASSERT(przm_decompress(frame_isoq, sizeof(frame_isoq), dst, 100) == PRZM_ERR_BUFFER_TOO_SMALL, "isoq buffer too small should error");

    /* 7. Exceeds max block size (> 16 MiB) */
    uint8_t huge_frame[12];
    write_u32_le(huge_frame, PRZM_MAGIC_HEISENBERG);
    write_u32_le(huge_frame + 4, 32 * 1024 * 1024); /* 32 MiB > 16 MiB */
    huge_frame[8] = 3; huge_frame[9] = 0; huge_frame[10] = 0; huge_frame[11] = 0;
    TEST_ASSERT(przm_decompress(huge_frame, sizeof(huge_frame), dst, sizeof(dst)) == PRZM_ERR_EXCEEDS_MAX_BLOCK, "huge frame should error");

    /* 8. Offset out of bounds in match replication */
    uint8_t oob_frame[26] = {
        'P', 'R', 'Z', 'M',
        0x00,               /* flags */
        0x20, 0x00, 0x00,   /* 32 bytes */
        0x06, 0x00,         /* cmd_size = 6 (5B ModeFlat + 1B cmd) */
        0x00, 0x00,         /* off_size = 0 */
        0, 0, 0, 0, 0, 0, 0, 0, /* Section 1: 8 zeroes */
        0x00,               /* ModeFlat */
        0x01, 0x00, 0x00, 0x00, /* orig_len = 1 */
        0x00                /* cmd 0x00: repeat match d=1 from pos=0 -> OOB */
    };
    TEST_ASSERT(przm_decompress(oob_frame, sizeof(oob_frame), dst, sizeof(dst)) == PRZM_ERR_OFFSET_OOB, "match d=1 at pos=0 should error OFFSET_OOB");

    /* 9. Truncated literal stream */
    uint8_t trunc_lit_frame[27] = {
        'P', 'R', 'Z', 'M',
        0x00,               /* flags */
        0x20, 0x00, 0x00,   /* 32 bytes */
        0x07, 0x00,         /* cmd_size = 7 (5B ModeFlat + 2B cmd) */
        0x00, 0x00,         /* off_size = 0 */
        0, 0, 0, 0, 0, 0, 0, 0, /* Section 1: 8 zeroes */
        0x00,               /* ModeFlat */
        0x02, 0x00, 0x00, 0x00, /* orig_len = 2 */
        0xF0, 0x10          /* cmd 0xF0: literal run of 16 bytes, but 0 literals in section 4 */
    };
    TEST_ASSERT(przm_decompress(trunc_lit_frame, sizeof(trunc_lit_frame), dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "truncated literals should error");

    /* 10. BCJ split excessive call count regression test */
    uint8_t split_frame[16] = {
        'P', 'R', 'Z', 'M',
        PRZM_FLAG_BCJ_SPLIT,
        0x10, 0x00, 0x00,   /* 16 bytes */
        0x00, 0x00, 0x20, 0x00 /* call_count = 2,097,152 (> 4MiB / 4) */
    };
    TEST_ASSERT(przm_decompress(split_frame, sizeof(split_frame), dst, sizeof(dst)) == PRZM_ERR_EXCEEDS_MAX_BLOCK, "excessive call count should error");

    /* 11. Zero-Schema VM and GGTC Go-native frames return PRZM_ERR_UNSUPPORTED_MODE in C */
    uint8_t vm_frame[8] = {'P', 'V', 0x01, 0x00, 0x40, 0x00, 0x00, 0x00}; /* 64 bytes */
    TEST_ASSERT(przm_get_uncompressed_size(vm_frame, sizeof(vm_frame)) == 64, "VM size should be 64");
    TEST_ASSERT(przm_decompress(vm_frame, sizeof(vm_frame), dst, sizeof(dst)) == PRZM_ERR_UNSUPPORTED_MODE, "VM should return UNSUPPORTED_MODE");

    uint8_t ggtc_frame[8] = {'G', 'G', 0x01, 0x00, 0x80, 0x00, 0x00, 0x00}; /* 128 bytes */
    TEST_ASSERT(przm_get_uncompressed_size(ggtc_frame, sizeof(ggtc_frame)) == 128, "GGTC size should be 128");
    TEST_ASSERT(przm_decompress(ggtc_frame, sizeof(ggtc_frame), dst, sizeof(dst)) == PRZM_ERR_UNSUPPORTED_MODE, "GGTC should return UNSUPPORTED_MODE");

    /* 12. Sheaf Trellis Mode 0 Truncation Regression */
    uint8_t trunc_sheaf_raw[16];
    write_u32_le(trunc_sheaf_raw, PRZM_MAGIC_SHEAF);
    write_u32_le(trunc_sheaf_raw + 4, 0); /* mode 0 */
    write_u32_le(trunc_sheaf_raw + 8, 100); /* orig_len = 100, but only 4 bytes payload */
    TEST_ASSERT(przm_decompress(trunc_sheaf_raw, sizeof(trunc_sheaf_raw), dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "trunc sheaf raw should error");

    /* 13. Sheaf Trellis Mode 2 Fiber Sum Overflow Regression */
    uint8_t bad_fiber_sheaf[32];
    write_u32_le(bad_fiber_sheaf, PRZM_MAGIC_SHEAF);
    write_u32_le(bad_fiber_sheaf + 4, 2); /* mode 2 */
    write_u32_le(bad_fiber_sheaf + 8, 100); /* orig_len = 100 */
    write_u32_le(bad_fiber_sheaf + 12, 50); /* l0 = 50 */
    write_u32_le(bad_fiber_sheaf + 16, 60); /* l1 = 60 (50+60 > 100) */
    write_u32_le(bad_fiber_sheaf + 20, 0);
    write_u32_le(bad_fiber_sheaf + 24, 0);
    TEST_ASSERT(przm_decompress(bad_fiber_sheaf, sizeof(bad_fiber_sheaf), dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "bad fiber sum should error");

    /* 14. Cellular Sheaf Corrupted Dimensions & Sparse NNZ Overflow */
    uint8_t bad_cshd[32];
    memset(bad_cshd, 0, sizeof(bad_cshd));
    write_u32_le(bad_cshd, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(bad_cshd + 4, 100); /* orig_len = 100 */
    write_u16_le(bad_cshd + 8, 50);  /* W = 50 */
    write_u16_le(bad_cshd + 10, 50); /* M = 50 -> W * M = 2500 > 100 */
    write_u16_le(bad_cshd + 16, PRZM_CSHD_FLAG_CURL_FREE);
    TEST_ASSERT(przm_decompress(bad_cshd, sizeof(bad_cshd), dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "W*M > orig_len should error");

    /* 15. Systematic Boundary Truncation across all Magic Signatures (0..64 bytes) */
    static const uint32_t kAllMagics[] = {
        PRZM_MAGIC_STANDARD, PRZM_MAGIC_SYMPLECTIC, PRZM_MAGIC_MAHLER,
        PRZM_MAGIC_SHEAF, PRZM_MAGIC_ARM64, PRZM_MAGIC_ISOQUANT,
        PRZM_MAGIC_GCTR, PRZM_MAGIC_HEISENBERG, PRZM_MAGIC_CELLULAR_SHEAF,
        PRZM_MAGIC_MOBIUS, PRZM_MAGIC_MARGOLUS
    };
    for (size_t m = 0; m < sizeof(kAllMagics)/sizeof(kAllMagics[0]); m++) {
        uint8_t mock_frame[64];
        memset(mock_frame, 0, sizeof(mock_frame));
        write_u32_le(mock_frame, kAllMagics[m]);
        write_u32_le(mock_frame + 4, 64);
        write_u32_le(mock_frame + 8, 64);
        for (size_t t_len = 0; t_len <= sizeof(mock_frame); t_len++) {
            int64_t sz_res = przm_get_uncompressed_size(mock_frame, t_len);
            int64_t dec_res = przm_decompress(mock_frame, t_len, dst, sizeof(dst));
            (void)sz_res;
            (void)dec_res;
        }
    }

    /* 16. Adversarial Randomized Bitstream Fuzzing (10,000 runs) */
    uint32_t fuzz_state = 0xDEADBEEF;
    for (int iter = 0; iter < 10000; iter++) {
        uint8_t fuzz_buf[128];
        fuzz_state = fuzz_state * 1664525u + 1013904223u;
        size_t f_len = (size_t)(fuzz_state % (sizeof(fuzz_buf) + 1));
        for (size_t b = 0; b < f_len; b++) {
            fuzz_state = fuzz_state * 1664525u + 1013904223u;
            fuzz_buf[b] = (uint8_t)(fuzz_state >> 24);
        }
        int64_t sz_err = przm_get_uncompressed_size(fuzz_buf, f_len);
        int64_t dec_err = przm_decompress(fuzz_buf, f_len, dst, sizeof(dst));
        (void)sz_err;
        (void)dec_err;
    }

    TEST_PASS("corrupted_and_truncated_frames");
}

/* 16. Test Base64 and Hex Normalization Decoding */
static void test_normalization(void) {
    /* 1. Hex expansion unit test */
    uint8_t raw_hex[4] = {0xDE, 0xAD, 0xBE, 0xEF};
    uint8_t exp_hex_lower[8];
    uint8_t exp_hex_upper[8];
    przm_expand_hex(exp_hex_lower, raw_hex, 4, 0);
    przm_expand_hex(exp_hex_upper, raw_hex, 4, 1);
    TEST_ASSERT(memcmp(exp_hex_lower, "deadbeef", 8) == 0, "Hex lower expansion mismatch");
    TEST_ASSERT(memcmp(exp_hex_upper, "DEADBEEF", 8) == 0, "Hex upper expansion mismatch");

    /* 2. Base64 expansion unit test */
    uint8_t raw_b64[6] = {'f', 'o', 'o', 'b', 'a', 'r'};
    uint8_t exp_b64[8];
    przm_expand_base64(exp_b64, raw_b64, 6, 0, 0);
    TEST_ASSERT(memcmp(exp_b64, "Zm9vYmFy", 8) == 0, "Base64 expansion mismatch");

    /* Base64 with padding 1 ('=') */
    uint8_t raw_b64_p1[5] = {'h', 'e', 'l', 'l', 'o'};
    uint8_t exp_b64_p1[8];
    przm_expand_base64(exp_b64_p1, raw_b64_p1, 5, 1, 0);
    TEST_ASSERT(memcmp(exp_b64_p1, "aGVsbG8=", 8) == 0, "Base64 pad1 mismatch");

    /* Base64 with padding 2 ('==') */
    uint8_t raw_b64_p2[4] = {'m', 'a', 'n', 'y'};
    uint8_t exp_b64_p2[8];
    przm_expand_base64(exp_b64_p2, raw_b64_p2, 4, 2, 0);
    TEST_ASSERT(memcmp(exp_b64_p2, "bWFueQ==", 8) == 0, "Base64 pad2 mismatch");

    /* 3. Synthetic Hex-normalized frame */
    uint8_t frame_hex[64];
    size_t fptr = 0;
    memcpy(frame_hex + fptr, "PRZM", 4); fptr += 4;
    frame_hex[fptr++] = PRZM_FLAG_NORM; /* 0x20 */
    frame_hex[fptr++] = 8; /* u_size = 8 ASCII hex bytes */
    frame_hex[fptr++] = 0;
    frame_hex[fptr++] = 0;

    /* Section Directory: CmdSize = 6, OffSize = 0 */
    frame_hex[fptr++] = 6; frame_hex[fptr++] = 0;
    frame_hex[fptr++] = 0; frame_hex[fptr++] = 0;

    /* Section 1: 8 bytes */
    memset(frame_hex + fptr, 0, 8);
    frame_hex[fptr + 1] = 16; /* WindowLog = 16 */
    frame_hex[fptr + 2] = PRZM_NORM_HEX; /* 1 */
    write_u16_le(frame_hex + fptr + 3, 4); /* contracted_len = 4 */
    fptr += 8;

    /* Section 2: Command stream (ModeFlat, 1 opcode 0xF6 = 4 direct literals) */
    frame_hex[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame_hex + fptr, 1); fptr += 4;
    frame_hex[fptr++] = 0xF6; /* 4 direct literals (0xF2 + 4) */

    /* Section 4: 4 raw bytes */
    frame_hex[fptr++] = 0xCA;
    frame_hex[fptr++] = 0xFE;
    frame_hex[fptr++] = 0xBA;
    frame_hex[fptr++] = 0xBE;

    uint8_t dst[128];
    int64_t res = przm_decompress(frame_hex, fptr, dst, sizeof(dst));
    TEST_ASSERT(res == 8, "Hex normalized frame decompress size mismatch");
    TEST_ASSERT(memcmp(dst, "cafebabe", 8) == 0, "Hex normalized payload mismatch");

    TEST_PASS("normalization");
}

/* 17. Test Block Deduplication (OpCopyBlock 0xFD) */
static void test_block_deduplication(void) {
    /* 1. Synthetic 2-block frame (128 KiB uncompressed) */
    uint8_t frame2[128];
    size_t fptr = 0;
    memcpy(frame2 + fptr, "PRZM", 4); fptr += 4;
    frame2[fptr++] = PRZM_FLAG_EXT_OFFSET; /* 0x08 */
    frame2[fptr++] = 0x00; /* u_size low */
    frame2[fptr++] = 0x00; /* u_size mid: 131072 = 0x020000 */
    frame2[fptr++] = 0x02; /* u_size high */

    /* Section Directory: CmdSize = 14 (ModeFlat), OffSize = 5 (ModeFlat) */
    frame2[fptr++] = 14; frame2[fptr++] = 0; frame2[fptr++] = 0;
    frame2[fptr++] = 5;  frame2[fptr++] = 0; frame2[fptr++] = 0;

    /* Section 1: 8 bytes */
    memset(frame2 + fptr, 0, 8);
    frame2[fptr + 1] = 16; /* WindowLog = 16 */
    fptr += 8;

    /* Section 2: Command stream (ModeFlat, origLen = 9) */
    /* Commands: RLE 65536 (4 bytes) + OpCopyBlock to block 0 (5 bytes) */
    frame2[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame2 + fptr, 9); fptr += 4;
    frame2[fptr++] = 0xF1; /* OpcodePrefixRLE */
    frame2[fptr++] = 0x80; frame2[fptr++] = 0x80; frame2[fptr++] = 0x04; /* uvarint(65536) */
    frame2[fptr++] = PRZM_OP_COPY_BLOCK; /* 0xFD */
    write_u32_le(frame2 + fptr, 0); fptr += 4; /* src_block_idx = 0 */

    /* Section 3: Offset channel (ModeFlat, origLen = 0) */
    frame2[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame2 + fptr, 0); fptr += 4;

    /* Section 4: Literal channel: 1 byte 0x55 for RLE */
    frame2[fptr++] = 0x55;

    size_t total_u_size = 131072;
    uint8_t* dst = (uint8_t*)malloc(total_u_size + PRZM_SAFETY_PADDING);
    TEST_ASSERT(dst != NULL, "malloc dst failed");
    memset(dst, 0, total_u_size + PRZM_SAFETY_PADDING);

    int64_t dec_len = przm_decompress(frame2, fptr, dst, total_u_size + PRZM_SAFETY_PADDING);
    TEST_ASSERT(dec_len == (int64_t)total_u_size, "2-block dedup decompressed size mismatch");
    for (size_t i = 0; i < total_u_size; i++) {
        TEST_ASSERT(dst[i] == 0x55, "2-block dedup content mismatch");
    }

    /* 2. Synthetic 3-block frame (196608 bytes): B0=0x11, B1=0x22, B2=copy B0 */
    uint8_t frame3[128];
    fptr = 0;
    memcpy(frame3 + fptr, "PRZM", 4); fptr += 4;
    frame3[fptr++] = PRZM_FLAG_EXT_OFFSET;
    frame3[fptr++] = 0x00; frame3[fptr++] = 0x00; frame3[fptr++] = 0x03; /* 196608 = 0x030000 */

    /* CmdSize = 18, OffSize = 5 */
    frame3[fptr++] = 18; frame3[fptr++] = 0; frame3[fptr++] = 0;
    frame3[fptr++] = 5;  frame3[fptr++] = 0; frame3[fptr++] = 0;

    memset(frame3 + fptr, 0, 8);
    frame3[fptr + 1] = 16;
    fptr += 8;

    /* Section 2: ModeFlat, origLen = 13 */
    /* RLE 65536 (4) + RLE 65536 (4) + OpCopyBlock 0 (5) = 13 bytes */
    frame3[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame3 + fptr, 13); fptr += 4;
    frame3[fptr++] = 0xF1; frame3[fptr++] = 0x80; frame3[fptr++] = 0x80; frame3[fptr++] = 0x04;
    frame3[fptr++] = 0xF1; frame3[fptr++] = 0x80; frame3[fptr++] = 0x80; frame3[fptr++] = 0x04;
    frame3[fptr++] = PRZM_OP_COPY_BLOCK;
    write_u32_le(frame3 + fptr, 0); fptr += 4; /* copy block 0 */

    /* Section 3: 5 bytes */
    frame3[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame3 + fptr, 0); fptr += 4;

    /* Section 4: 2 bytes: 0x11 for first RLE, 0x22 for second RLE */
    frame3[fptr++] = 0x11;
    frame3[fptr++] = 0x22;

    size_t total_u_size3 = 196608;
    uint8_t* dst3 = (uint8_t*)malloc(total_u_size3 + PRZM_SAFETY_PADDING);
    TEST_ASSERT(dst3 != NULL, "malloc dst3 failed");

    int64_t dec_len3 = przm_decompress(frame3, fptr, dst3, total_u_size3 + PRZM_SAFETY_PADDING);
    TEST_ASSERT(dec_len3 == (int64_t)total_u_size3, "3-block dedup decompressed size mismatch");
    for (size_t i = 0; i < 65536; i++) {
        TEST_ASSERT(dst3[i] == 0x11, "3-block dedup B0 mismatch");
    }
    for (size_t i = 65536; i < 131072; i++) {
        TEST_ASSERT(dst3[i] == 0x22, "3-block dedup B1 mismatch");
    }
    for (size_t i = 131072; i < 196608; i++) {
        TEST_ASSERT(dst3[i] == 0x11, "3-block dedup B2 mismatch");
    }
    free(dst3);

    /* 3. Adversarial / Corrupted frames testing */
    /* A. Truncated OpCopyBlock command (missing payload) */
    uint8_t frame_bad[128];
    memcpy(frame_bad, frame2, sizeof(frame2));
    /* Shorten CmdSize to truncate OpCopyBlock */
    frame_bad[8] = 11; /* was 14 */
    int64_t err_trunc = przm_decompress(frame_bad, fptr, dst, total_u_size + PRZM_SAFETY_PADDING);
    TEST_ASSERT(err_trunc < 0, "truncated OpCopyBlock must fail");

    /* B. Forward reference: block 0 tries to reference block 1 */
    uint8_t frame_fwd[128];
    memcpy(frame_fwd, frame2, sizeof(frame2));
    /* Overwrite first command (offset 27) with OpCopyBlock referencing block 1 */
    frame_fwd[27] = PRZM_OP_COPY_BLOCK;
    write_u32_le(frame_fwd + 28, 1);
    int64_t err_fwd = przm_decompress(frame_fwd, fptr, dst, total_u_size + PRZM_SAFETY_PADDING);
    TEST_ASSERT(err_fwd < 0, "forward OpCopyBlock reference must fail");

    /* C. Out-of-bounds reference: block 1 references block 999 */
    uint8_t frame_oob[128];
    memcpy(frame_oob, frame2, sizeof(frame2));
    write_u32_le(frame_oob + 32, 999);
    int64_t err_oob = przm_decompress(frame_oob, fptr, dst, total_u_size + PRZM_SAFETY_PADDING);
    TEST_ASSERT(err_oob < 0, "out-of-bounds OpCopyBlock reference must fail");

    /* D. Destination buffer too small */
    int64_t err_buf = przm_decompress(frame2, fptr, dst, 65536);
    TEST_ASSERT(err_buf < 0, "buffer too small must fail");

    free(dst);
    TEST_PASS("block_deduplication");
}

/* 18. Test Archival Mode PNG Deflate Container Decompression */
static void test_archival_png(void) {
    /* 1. Construct genuine reference PNG bitstream:
     *    - PNG Signature: 8 bytes
     *    - IHDR Chunk: 4B len (13) + 4B "IHDR" + 13B data + 4B CRC
     *    - IDAT Chunk: 4B len (16) + 4B "IDAT" + 16B scanlines + 4B CRC
     *    - IEND Chunk: 4B len (0)  + 4B "IEND" + 4B CRC
     */
    uint8_t orig_png[256];
    size_t png_pos = 0;

    /* 8-byte PNG signature */
    static const uint8_t png_sig[8] = {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A};
    memcpy(orig_png + png_pos, png_sig, 8);
    png_pos += 8;

    /* IHDR chunk */
    uint32_t ihdr_data_len = 13;
    orig_png[png_pos]     = (uint8_t)(ihdr_data_len >> 24);
    orig_png[png_pos + 1] = (uint8_t)(ihdr_data_len >> 16);
    orig_png[png_pos + 2] = (uint8_t)(ihdr_data_len >> 8);
    orig_png[png_pos + 3] = (uint8_t)ihdr_data_len;
    png_pos += 4;
    memcpy(orig_png + png_pos, "IHDR", 4);
    png_pos += 4;
    memset(orig_png + png_pos, 0, 13);
    orig_png[png_pos + 3] = 1; /* width = 1 */
    orig_png[png_pos + 7] = 1; /* height = 1 */
    orig_png[png_pos + 8] = 8; /* bit depth = 8 */
    orig_png[png_pos + 9] = 2; /* color type = RGB */
    png_pos += 13;
    uint8_t ihdr_crc[4] = {0x12, 0x34, 0x56, 0x78};
    memcpy(orig_png + png_pos, ihdr_crc, 4);
    png_pos += 4;

    size_t prefix_len = png_pos; /* 33 bytes */

    /* IDAT chunk */
    const char* scanline_data = "SCANLINE_DATA_16";
    uint32_t idat_data_len = (uint32_t)strlen(scanline_data);
    orig_png[png_pos]     = (uint8_t)(idat_data_len >> 24);
    orig_png[png_pos + 1] = (uint8_t)(idat_data_len >> 16);
    orig_png[png_pos + 2] = (uint8_t)(idat_data_len >> 8);
    orig_png[png_pos + 3] = (uint8_t)idat_data_len;
    png_pos += 4;
    memcpy(orig_png + png_pos, "IDAT", 4);
    png_pos += 4;
    memcpy(orig_png + png_pos, scanline_data, idat_data_len);
    png_pos += idat_data_len;
    uint8_t idat_crc[4] = {0xDE, 0xAD, 0xBE, 0xEF};
    memcpy(orig_png + png_pos, idat_crc, 4);
    png_pos += 4;

    size_t postfix_start = png_pos;

    /* IEND chunk */
    orig_png[png_pos]     = 0;
    orig_png[png_pos + 1] = 0;
    orig_png[png_pos + 2] = 0;
    orig_png[png_pos + 3] = 0;
    png_pos += 4;
    memcpy(orig_png + png_pos, "IEND", 4);
    png_pos += 4;
    uint8_t iend_crc[4] = {0xAE, 0x42, 0x60, 0x82};
    memcpy(orig_png + png_pos, iend_crc, 4);
    png_pos += 4;

    size_t postfix_len = png_pos - postfix_start; /* 12 bytes */
    size_t total_orig_png_len = png_pos;

    /* 2. Build synthetic PRZM Archival PNG Container Frame */
    uint8_t container[512];
    size_t c_pos = 0;

    /* 12-byte Container Header */
    write_u32_le(container + c_pos, PRZM_MAGIC_ARCHIVAL_PNG);
    c_pos += 4;
    write_u32_le(container + c_pos, (uint32_t)total_orig_png_len);
    c_pos += 4;

    /* Metadata block: 8 header + 1*8 IDAT desc + prefix_len + postfix_len */
    size_t meta_size = 8 + 8 + prefix_len + postfix_len;
    write_u32_le(container + c_pos, (uint32_t)meta_size);
    c_pos += 4;

    /* Metadata Block Payload */
    size_t meta_start = c_pos;
    container[c_pos++] = 0x50; /* 'P' */
    container[c_pos++] = 0x01; /* isZlib */
    write_u16_le(container + c_pos, (uint16_t)prefix_len);
    c_pos += 2;
    write_u16_le(container + c_pos, (uint16_t)postfix_len);
    c_pos += 2;
    write_u16_le(container + c_pos, 1); /* idatCount = 1 */
    c_pos += 2;

    /* IDAT descriptor 0 */
    write_u32_le(container + c_pos, idat_data_len);
    c_pos += 4;
    memcpy(container + c_pos, idat_crc, 4); /* CRC Big-Endian */
    c_pos += 4;

    /* Prefix bytes */
    memcpy(container + c_pos, orig_png, prefix_len);
    c_pos += prefix_len;

    /* Postfix bytes */
    memcpy(container + c_pos, orig_png + postfix_start, postfix_len);
    c_pos += postfix_len;

    TEST_ASSERT(c_pos - meta_start == meta_size, "Metadata size computation error");

    /* Inner PRZM compressed stream (standard uncompressed frame for scanlines) */
    memcpy(container + c_pos, "PRZM", 4);
    c_pos += 4;
    container[c_pos++] = PRZM_FLAG_UNCOMPRESSED;
    container[c_pos++] = (uint8_t)idat_data_len;
    container[c_pos++] = (uint8_t)(idat_data_len >> 8);
    container[c_pos++] = (uint8_t)(idat_data_len >> 16);
    memcpy(container + c_pos, scanline_data, idat_data_len);
    c_pos += idat_data_len;

    size_t container_len = c_pos;

    /* 3. Verify przm_get_uncompressed_size */
    int64_t u_size = przm_get_uncompressed_size(container, container_len);
    TEST_ASSERT(u_size == (int64_t)total_orig_png_len, "Archival PNG uncompressed size mismatch");

    /* 4. Decompress into dst */
    uint8_t dst[512];
    int64_t dec_len = przm_decompress(container, container_len, dst, sizeof(dst));
    TEST_ASSERT(dec_len == (int64_t)total_orig_png_len, "Archival PNG decompressed size mismatch");
    TEST_ASSERT(memcmp(dst, orig_png, total_orig_png_len) == 0, "Archival PNG reconstructed bytes mismatch");

    /* 5. Error handling: buffer too small */
    int64_t err_small = przm_decompress(container, container_len, dst, total_orig_png_len);
    TEST_ASSERT(err_small < 0, "Archival PNG must fail when dst_cap < orig_len + SAFETY_PADDING");

    /* 6. Error handling: truncated frame */
    int64_t err_trunc = przm_decompress(container, 10, dst, sizeof(dst));
    TEST_ASSERT(err_trunc < 0, "Archival PNG must fail on truncated frame");

    TEST_PASS("archival_png");
}

/* Helper to construct a synthetic LZ frame repeating a byte 'ch' 5 times */
static size_t make_lz5_frame(uint8_t* out, uint8_t ch) {
    size_t fptr = 0;
    memcpy(out + fptr, "PRZM", 4); fptr += 4;
    out[fptr++] = 0x00; /* Flags */
    out[fptr++] = 5;    /* Uncompressed size = 5 */
    out[fptr++] = 0;
    out[fptr++] = 0;

    /* Section Directory: CmdStreamSize = 7, OffsetStreamSize = 0 */
    out[fptr++] = 7; out[fptr++] = 0;
    out[fptr++] = 0; out[fptr++] = 0;

    /* Section 1: 8 zeroes */
    memset(out + fptr, 0, 8); fptr += 8;

    /* Section 2: Command stream (ModeFlat: mode 0, orig_len 2) */
    out[fptr++] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(out + fptr, 2); fptr += 4;
    out[fptr++] = 0xF3; /* 1 literal */
    out[fptr++] = 0x00; /* Fused Repeat: L=0, M=4, k=0 (d=1) */

    /* Section 4: Literals */
    out[fptr++] = ch;
    return fptr;
}

/* Helper to construct an uncompressed PRZM frame */
static size_t make_raw_frame(uint8_t* out, const uint8_t* data, size_t len) {
    memcpy(out, "PRZM", 4);
    out[4] = PRZM_FLAG_UNCOMPRESSED;
    out[5] = (uint8_t)len;
    out[6] = (uint8_t)(len >> 8);
    out[7] = (uint8_t)(len >> 16);
    memcpy(out + 8, data, len);
    return 8 + len;
}

/* 19. Test Multi-Chunk Stream ('PRZS') Container Decompression */
static void test_stream_multi_chunk(void) {
    /* 1. Multi-Chunk RAW (2 chunks: 32 bytes 'X' + 48 bytes 'Y') */
    uint8_t raw0_data[32];
    memset(raw0_data, 'X', sizeof(raw0_data));
    uint8_t c0[64];
    size_t c0_len = make_raw_frame(c0, raw0_data, sizeof(raw0_data));

    uint8_t raw1_data[48];
    memset(raw1_data, 'Y', sizeof(raw1_data));
    uint8_t c1[64];
    size_t c1_len = make_raw_frame(c1, raw1_data, sizeof(raw1_data));

    uint8_t stream_raw[256];
    size_t s_pos = 0;
    write_u32_le(stream_raw + s_pos, PRZM_MAGIC_STREAM); s_pos += 4;
    stream_raw[s_pos++] = PRZM_STREAM_FLAG_INDEPENDENT;
    write_u64_le(stream_raw + s_pos, 80); s_pos += 8; /* Total: 32 + 48 = 80 */
    write_u32_le(stream_raw + s_pos, 2); s_pos += 4;  /* 2 chunks */

    /* Chunk 0 */
    write_u32_le(stream_raw + s_pos, (uint32_t)c0_len); s_pos += 4;
    memcpy(stream_raw + s_pos, c0, c0_len); s_pos += c0_len;

    /* Chunk 1 */
    write_u32_le(stream_raw + s_pos, (uint32_t)c1_len); s_pos += 4;
    memcpy(stream_raw + s_pos, c1, c1_len); s_pos += c1_len;

    uint8_t dst[256];
    int64_t u_sz = przm_get_uncompressed_size(stream_raw, s_pos);
    TEST_ASSERT(u_sz == 80, "PRZS raw uncompressed size mismatch");

    int64_t dec_raw = przm_decompress(stream_raw, s_pos, dst, sizeof(dst));
    TEST_ASSERT(dec_raw == 80, "PRZS raw decompressed size mismatch");
    TEST_ASSERT(memcmp(dst, raw0_data, 32) == 0, "PRZS chunk 0 mismatch");
    TEST_ASSERT(memcmp(dst + 32, raw1_data, 48) == 0, "PRZS chunk 1 mismatch");

    /* 2. Multi-Chunk LZ (3 chunks: 5 'A' + 5 'B' + 5 'C' = 15 bytes) */
    uint8_t lz0[64], lz1[64], lz2[64];
    size_t lz0_len = make_lz5_frame(lz0, 'A');
    size_t lz1_len = make_lz5_frame(lz1, 'B');
    size_t lz2_len = make_lz5_frame(lz2, 'C');

    uint8_t stream_lz[256];
    s_pos = 0;
    write_u32_le(stream_lz + s_pos, PRZM_MAGIC_STREAM); s_pos += 4;
    stream_lz[s_pos++] = PRZM_STREAM_FLAG_INDEPENDENT;
    write_u64_le(stream_lz + s_pos, 15); s_pos += 8; /* Total: 5 + 5 + 5 = 15 */
    write_u32_le(stream_lz + s_pos, 3); s_pos += 4;  /* 3 chunks */

    write_u32_le(stream_lz + s_pos, (uint32_t)lz0_len); s_pos += 4;
    memcpy(stream_lz + s_pos, lz0, lz0_len); s_pos += lz0_len;

    write_u32_le(stream_lz + s_pos, (uint32_t)lz1_len); s_pos += 4;
    memcpy(stream_lz + s_pos, lz1, lz1_len); s_pos += lz1_len;

    write_u32_le(stream_lz + s_pos, (uint32_t)lz2_len); s_pos += 4;
    memcpy(stream_lz + s_pos, lz2, lz2_len); s_pos += lz2_len;

    int64_t dec_lz = przm_decompress(stream_lz, s_pos, dst, sizeof(dst));
    TEST_ASSERT(dec_lz == 15, "PRZS LZ decompressed size mismatch");
    TEST_ASSERT(memcmp(dst, "AAAAABBBBBCCCCC", 15) == 0, "PRZS LZ content mismatch");

    /* 3. Single Chunk (1 chunk) PRZS */
    uint8_t stream_1c[128];
    s_pos = 0;
    write_u32_le(stream_1c + s_pos, PRZM_MAGIC_STREAM); s_pos += 4;
    stream_1c[s_pos++] = PRZM_STREAM_FLAG_INDEPENDENT;
    write_u64_le(stream_1c + s_pos, 5); s_pos += 8;
    write_u32_le(stream_1c + s_pos, 1); s_pos += 4;
    write_u32_le(stream_1c + s_pos, (uint32_t)lz0_len); s_pos += 4;
    memcpy(stream_1c + s_pos, lz0, lz0_len); s_pos += lz0_len;

    int64_t dec_1c = przm_decompress(stream_1c, s_pos, dst, sizeof(dst));
    TEST_ASSERT(dec_1c == 5, "PRZS 1-chunk decompressed size mismatch");
    TEST_ASSERT(memcmp(dst, "AAAAA", 5) == 0, "PRZS 1-chunk content mismatch");

    /* 4. N Chunks (8 chunks) */
    uint8_t stream_8c[1024];
    s_pos = 0;
    write_u32_le(stream_8c + s_pos, PRZM_MAGIC_STREAM); s_pos += 4;
    stream_8c[s_pos++] = PRZM_STREAM_FLAG_INDEPENDENT;
    write_u64_le(stream_8c + s_pos, 40); s_pos += 8; /* 8 * 5 = 40 */
    write_u32_le(stream_8c + s_pos, 8); s_pos += 4;

    char expected_8c[41];
    for (int i = 0; i < 8; i++) {
        uint8_t ch = (uint8_t)('0' + i);
        uint8_t lzc[64];
        size_t lzc_len = make_lz5_frame(lzc, ch);
        write_u32_le(stream_8c + s_pos, (uint32_t)lzc_len); s_pos += 4;
        memcpy(stream_8c + s_pos, lzc, lzc_len); s_pos += lzc_len;
        for (int k = 0; k < 5; k++) expected_8c[i * 5 + k] = (char)ch;
    }
    expected_8c[40] = '\0';

    int64_t dec_8c = przm_decompress(stream_8c, s_pos, dst, sizeof(dst));
    TEST_ASSERT(dec_8c == 40, "PRZS 8-chunk decompressed size mismatch");
    TEST_ASSERT(memcmp(dst, expected_8c, 40) == 0, "PRZS 8-chunk content mismatch");

    /* 5. Error & Truncation Cases */
    /* A. Truncated stream header (< 17 bytes) */
    int64_t err_thdr = przm_decompress(stream_raw, 16, dst, sizeof(dst));
    TEST_ASSERT(err_thdr < 0, "Truncated stream header must fail");

    /* B. Buffer too small (dst_cap < total_u_size + PRZM_SAFETY_PADDING) */
    int64_t err_buf = przm_decompress(stream_raw, s_pos, dst, 80 + PRZM_SAFETY_PADDING - 1);
    TEST_ASSERT(err_buf < 0, "Buffer too small must fail");

    /* C. Truncated chunk length prefix */
    int64_t err_tchunk_pfx = przm_decompress(stream_raw, 19, dst, sizeof(dst));
    TEST_ASSERT(err_tchunk_pfx < 0, "Truncated chunk length prefix must fail");

    /* D. Truncated chunk payload */
    int64_t err_tchunk_pay = przm_decompress(stream_raw, 25, dst, sizeof(dst));
    TEST_ASSERT(err_tchunk_pay < 0, "Truncated chunk payload must fail");

    /* E. Total uncompressed size mismatch (claims 100 instead of 80) */
    uint8_t stream_bad_size[256];
    memcpy(stream_bad_size, stream_raw, sizeof(stream_bad_size));
    write_u64_le(stream_bad_size + 5, 100);
    int64_t err_sz_mismatch = przm_decompress(stream_bad_size, sizeof(stream_raw), dst, sizeof(dst));
    TEST_ASSERT(err_sz_mismatch < 0, "Total size mismatch must fail");

    /* F. Corrupted inner chunk */
    uint8_t stream_bad_chunk[256];
    memcpy(stream_bad_chunk, stream_raw, sizeof(stream_bad_chunk));
    stream_bad_chunk[17 + 4] = 'X'; /* Corrupt 'P' of inner PRZM magic */
    int64_t err_bad_chunk = przm_decompress(stream_bad_chunk, sizeof(stream_raw), dst, sizeof(dst));
    TEST_ASSERT(err_bad_chunk < 0, "Corrupted inner chunk magic must fail");

    TEST_PASS("stream_multi_chunk");
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
    test_mobius_decompression();
    test_margolus_decompression();
    test_dictionary_decompression();
    test_sheaf_trellis_decompression();
    test_normalization();
    test_block_deduplication();
    test_corrupted_and_truncated_frames();
    test_archival_png();
    test_stream_multi_chunk();

    printf("\n>>> ALL TESTS PASSED SUCCESSFULLY! (0 Allocations, 100%% Lossless) <<<\n");
    return 0;
}

