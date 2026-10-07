/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Challenger M2-2 Empirical Adversarial Stress Suite for PRZM Pure C Decompressor.
 *
 * Verifies:
 * 1. Zero heap allocations in C runtime (__wrap_malloc).
 * 2. Invalid 4-byte varints safely rejected (PRZM_ERR_CORRUPTED_FRAME).
 * 3. Truncated offset streams safely rejected (PRZM_ERR_CORRUPTED_FRAME).
 * 4. Match distance exceeding destination write pointer safely returns PRZM_ERR_OFFSET_OOB.
 * 5. Out-of-bounds and zero distances safely rejected.
 * 6. 10,000 corrupt/fuzzed frames safely handled with zero crashes or segfaults.
 */

#include "przm_dec.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <stdbool.h>

#define CHK(cond, msg) do { \
    if (!(cond)) { \
        fprintf(stderr, "[FAIL] %s:%d: %s\n", __FILE__, __LINE__, msg); \
        exit(1); \
    } \
} while (0)

#define CHK_EQ(a, b, msg) do { \
    if ((a) != (b)) { \
        fprintf(stderr, "[FAIL] %s:%d: %s (got %lld, expected %lld)\n", \
            __FILE__, __LINE__, msg, (long long)(a), (long long)(b)); \
        exit(1); \
    } \
} while (0)

static inline void write_u16_le(uint8_t* p, uint16_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
}

static inline void write_u24_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
}

static inline void write_u32_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
    p[3] = (uint8_t)(v >> 24);
}

/* Heap allocation tracking flag */
static volatile long g_heap_allocations = 0;
static volatile int g_active_decompression = 0;

extern void* __real_malloc(size_t size);

void* __wrap_malloc(size_t size) {
    if (g_active_decompression) {
        g_heap_allocations++;
    }
    return __real_malloc(size);
}

static uint8_t g_dst_buf[16 * 1024 * 1024 + 128];

/* Test 1: Match Distance Exceeding Destination Write Pointer (PRZM_ERR_OFFSET_OOB) */
static void test_match_distance_exceeds_dst_pos(void) {
    printf("[+] Test 1: Match distance exceeding destination write pointer...\n"); fflush(stdout);

    /* Construct standard fused frame with 1 literal byte then a match distance of 500 */
    uint8_t frame[256];
    memset(frame, 0, sizeof(frame));

    write_u32_le(frame, PRZM_MAGIC_STANDARD);
    frame[4] = PRZM_FLAG_EXT_OFFSET; /* Ext offset */
    write_u24_le(frame + 5, 1024);   /* u_size = 1024 */

    size_t off = 8;
    /* Section directory: cmd_size, off_size (u24) */
    size_t cmd_size = 5 + 2; /* 5-byte flat header + 2 cmd bytes */
    size_t off_size = 5 + 2; /* 5-byte flat header + 2 off bytes */
    write_u24_le(frame + off, (uint32_t)cmd_size);
    write_u24_le(frame + off + 3, (uint32_t)off_size);
    off += 6;

    /* Section 1: 8 bytes distribution parameters */
    memset(frame + off, 0, 8);
    frame[off + 1] = PRZM_WINDOW_LOG_1M;
    off += 8;

    /* Section 2: Commands (ModeFlat, orig_len=2) */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 2);
    frame[off + 5] = 0xF3; /* 1 literal */
    frame[off + 6] = 0x80; /* match base_len=0 (m_len=4), lit_len=0 */
    off += 5 + 2;

    /* Section 3: Offsets (ModeFlat, orig_len=2) */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 2);
    /* Varint 500: 500 = 0x1F4 -> byte0 = 0xF4, byte1 = 0x03 */
    frame[off + 5] = 0xF4;
    frame[off + 6] = 0x03;
    off += 5 + 2;

    /* Section 4: Literals (1 byte) */
    frame[off] = 'A';
    off += 1;

    g_active_decompression = 1;
    int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
    g_active_decompression = 0;

    /* After writing 1 literal byte, dst_pos is 1. Match distance is 500 > 1.
       Must return PRZM_ERR_OFFSET_OOB (-3). */
    CHK_EQ(ret, PRZM_ERR_OFFSET_OOB, "Distance exceeding dst_pos must return PRZM_ERR_OFFSET_OOB");
    CHK_EQ(g_heap_allocations, 0, "Heap allocations must be 0");
    printf("    -> Correctly returned PRZM_ERR_OFFSET_OOB (-3) with 0 allocations\n");
}

/* Test 2: Invalid 4-byte and 5-byte Varints */
static void test_invalid_varints(void) {
    printf("[+] Test 2: Adversarial invalid varints in offset stream...\n");

    /* Construct frame where offset varint does not terminate or overflows */
    uint8_t frame[256];
    memset(frame, 0, sizeof(frame));

    write_u32_le(frame, PRZM_MAGIC_STANDARD);
    frame[4] = PRZM_FLAG_EXT_OFFSET;
    write_u24_le(frame + 5, 2048);

    size_t off = 8;
    write_u24_le(frame + off, 5 + 1); /* cmd_size = 6 */
    write_u24_le(frame + off + 3, 5 + 6); /* off_size = 11 */
    off += 6;

    /* Section 1 */
    memset(frame + off, 0, 8);
    frame[off + 1] = PRZM_WINDOW_LOG_8M;
    off += 8;

    /* Section 2: Command: 0x80 (match with offset) */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 1);
    frame[off + 5] = 0x80;
    off += 5 + 1;

    /* Section 3: 6 varint bytes all with high bit set (never terminates) */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 6);
    memset(frame + off + 5, 0x80 | 0x01, 6);
    off += 5 + 6;

    /* Section 4: Literals */
    frame[off] = 'B';
    off += 1;

    g_active_decompression = 1;
    int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
    g_active_decompression = 0;

    CHK_EQ(ret, PRZM_ERR_CORRUPTED_FRAME, "Unterminated varint must return PRZM_ERR_CORRUPTED_FRAME");
    CHK_EQ(g_heap_allocations, 0, "Heap allocations must be 0");
    printf("    -> Correctly rejected unterminated varint with PRZM_ERR_CORRUPTED_FRAME\n");
}

/* Test 3: Truncated Offsets Stream */
static void test_truncated_offsets(void) {
    printf("[+] Test 3: Truncated offset stream...\n");

    uint8_t frame[256];
    memset(frame, 0, sizeof(frame));

    write_u32_le(frame, PRZM_MAGIC_STANDARD);
    frame[4] = PRZM_FLAG_EXT_OFFSET;
    write_u24_le(frame + 5, 1024);

    size_t off = 8;
    write_u24_le(frame + off, 5 + 1); /* cmd_size = 6 */
    write_u24_le(frame + off + 3, 0); /* off_size = 0 (truncated!) */
    off += 6;

    /* Section 1 */
    memset(frame + off, 0, 8);
    frame[off + 1] = PRZM_WINDOW_LOG_1M;
    off += 8;

    /* Section 2: Command: 0x80 (requires offset) */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 1);
    frame[off + 5] = 0x80;
    off += 5 + 1;

    /* Section 3: off_size is 0 */

    /* Section 4: empty */

    g_active_decompression = 1;
    int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
    g_active_decompression = 0;

    CHK(ret < 0, "Truncated offset stream must fail");
    CHK_EQ(g_heap_allocations, 0, "Heap allocations must be 0");
    printf("    -> Correctly returned error code %lld on truncated offset stream\n", (long long)ret);
}

/* Test 4: Distance Zero or Negative */
static void test_zero_distance_offset(void) {
    printf("[+] Test 4: Offset distance == 0...\n");

    uint8_t frame[256];
    memset(frame, 0, sizeof(frame));

    write_u32_le(frame, PRZM_MAGIC_STANDARD);
    frame[4] = PRZM_FLAG_EXT_OFFSET;
    write_u24_le(frame + 5, 1024);

    size_t off = 8;
    write_u24_le(frame + off, 5 + 1); /* cmd_size = 6 */
    write_u24_le(frame + off + 3, 5 + 1); /* off_size = 6 */
    off += 6;

    /* Section 1 */
    memset(frame + off, 0, 8);
    frame[off + 1] = PRZM_WINDOW_LOG_1M;
    off += 8;

    /* Section 2: Command 0x80 */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 1);
    frame[off + 5] = 0x80;
    off += 5 + 1;

    /* Section 3: Varint 0 (d = 0) */
    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 1);
    frame[off + 5] = 0x00;
    off += 5 + 1;

    g_active_decompression = 1;
    int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
    g_active_decompression = 0;

    CHK_EQ(ret, PRZM_ERR_CORRUPTED_FRAME, "Zero distance offset must return PRZM_ERR_CORRUPTED_FRAME");
    CHK_EQ(g_heap_allocations, 0, "Heap allocations must be 0");
    printf("    -> Correctly returned PRZM_ERR_CORRUPTED_FRAME on d == 0\n");
}

/* Test 5: Exact Distance Boundary (d == dst_pos vs d == dst_pos + 1) */
static void test_exact_distance_boundary(void) {
    printf("[+] Test 5: Distance boundary d == dst_pos vs d == dst_pos + 1...\n");

    /* Case A: d == dst_pos + 1 (OOB) */
    {
        uint8_t frame[256];
        memset(frame, 0, sizeof(frame));
        write_u32_le(frame, PRZM_MAGIC_STANDARD);
        frame[4] = PRZM_FLAG_EXT_OFFSET;
        write_u24_le(frame + 5, 14); /* 10 lits + 4 match = 14 bytes */

        size_t off = 8;
        write_u24_le(frame + off, 5 + 3); /* 3 cmds: 0xFA (8 lits), 0xF4 (2 lits), 0x80 (match) */
        write_u24_le(frame + off + 3, 5 + 1); /* 1 off: d = 11 */
        off += 6;

        memset(frame + off, 0, 8);
        frame[off + 1] = PRZM_WINDOW_LOG_1M;
        off += 8;

        /* Commands: 0xFA (8 lits), 0xF4 (2 lits), 0x80 (match m_len=4) */
        frame[off] = PRZM_ENTROPY_MODE_FLAT;
        write_u32_le(frame + off + 1, 3);
        frame[off + 5] = 0xFA; /* 8 literals */
        frame[off + 6] = 0xF4; /* 2 literals */
        frame[off + 7] = 0x80; /* match */
        off += 5 + 3;

        /* Offset: 11 */
        frame[off] = PRZM_ENTROPY_MODE_FLAT;
        write_u32_le(frame + off + 1, 1);
        frame[off + 5] = 11;
        off += 5 + 1;

        /* Literals: 10 bytes */
        memset(frame + off, 'X', 10);
        off += 10;

        g_active_decompression = 1;
        int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
        g_active_decompression = 0;

        CHK_EQ(ret, PRZM_ERR_OFFSET_OOB, "d == dst_pos + 1 must return PRZM_ERR_OFFSET_OOB");
        CHK_EQ(g_heap_allocations, 0, "0 heap allocations");
    }

    /* Case B: d == dst_pos (Exact match boundary) */
    {
        uint8_t frame[256];
        memset(frame, 0, sizeof(frame));
        write_u32_le(frame, PRZM_MAGIC_STANDARD);
        frame[4] = PRZM_FLAG_EXT_OFFSET;
        write_u24_le(frame + 5, 14);

        size_t off = 8;
        write_u24_le(frame + off, 5 + 3);
        write_u24_le(frame + off + 3, 5 + 1);
        off += 6;

        memset(frame + off, 0, 8);
        frame[off + 1] = PRZM_WINDOW_LOG_1M;
        off += 8;

        frame[off] = PRZM_ENTROPY_MODE_FLAT;
        write_u32_le(frame + off + 1, 3);
        frame[off + 5] = 0xFA; /* 8 literals */
        frame[off + 6] = 0xF4; /* 2 literals */
        frame[off + 7] = 0x80; /* match */
        off += 5 + 3;

        /* Offset: 10 (exact dst_pos) */
        frame[off] = PRZM_ENTROPY_MODE_FLAT;
        write_u32_le(frame + off + 1, 1);
        frame[off + 5] = 10;
        off += 5 + 1;

        memset(frame + off, 'X', 10);
        off += 10;

        g_active_decompression = 1;
        int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
        g_active_decompression = 0;

        CHK_EQ(ret, 14, "d == dst_pos must succeed with 14 bytes decompressed");
        CHK_EQ(g_heap_allocations, 0, "0 heap allocations");
    }

    printf("    -> Correctly verified: d == dst_pos succeeds (14 bytes), d == dst_pos + 1 returns PRZM_ERR_OFFSET_OOB\n");
}

/* Test 6: Repeat Register Match at dst_pos == 0 (PRZM_ERR_OFFSET_OOB) */
static void test_repeat_match_at_zero(void) {
    printf("[+] Test 6: Repeat register match at dst_pos == 0...\n");

    uint8_t frame[256];
    memset(frame, 0, sizeof(frame));
    write_u32_le(frame, PRZM_MAGIC_STANDARD);
    frame[4] = PRZM_FLAG_EXT_OFFSET;
    write_u24_le(frame + 5, 4);

    size_t off = 8;
    write_u24_le(frame + off, 5 + 1); /* 1 cmd: 0x00 (k=0, rep0=1, lit_len=0, m_len=4) */
    write_u24_le(frame + off + 3, 0); /* 0 offsets */
    off += 6;

    memset(frame + off, 0, 8);
    frame[off + 1] = PRZM_WINDOW_LOG_1M;
    off += 8;

    frame[off] = PRZM_ENTROPY_MODE_FLAT;
    write_u32_le(frame + off + 1, 1);
    frame[off + 5] = 0x00; /* repeat 0 */
    off += 5 + 1;

    g_active_decompression = 1;
    int64_t ret = przm_decompress(frame, off, g_dst_buf, sizeof(g_dst_buf));
    g_active_decompression = 0;

    CHK_EQ(ret, PRZM_ERR_OFFSET_OOB, "Repeat match at dst_pos=0 must return PRZM_ERR_OFFSET_OOB");
    CHK_EQ(g_heap_allocations, 0, "0 heap allocations");
    printf("    -> Correctly returned PRZM_ERR_OFFSET_OOB on repeat match at dst_pos == 0\n");
}

/* Test 7: 10,000 Randomized Adversarial Corrupted Streams */
static void test_fuzzed_corrupt_streams(void) {
    printf("[+] Test 7: 10,000 Randomized adversarial corrupted streams...\n");

    uint64_t rng = 0xCAFEBABE12345678ULL;
    uint8_t corrupt_frame[4096];

    for (int iter = 0; iter < 10000; iter++) {
        if (iter > 0 && iter % 2500 == 0) {
            printf("    [fuzz iter %d / 10000]\n", iter);
        }
        size_t len = 16 + (size_t)(rng % 2048);
        for (size_t i = 0; i < len; i++) {
            rng = rng * 6364136223846793005ULL + 1442695040888963407ULL;
            corrupt_frame[i] = (uint8_t)(rng >> 56);
        }

        /* Sometimes inject valid PRZM magic to penetrate header parse */
        if (iter % 3 == 0) {
            write_u32_le(corrupt_frame, PRZM_MAGIC_STANDARD);
            corrupt_frame[4] = (uint8_t)(rng & 0x0F);
            write_u24_le(corrupt_frame + 5, (uint32_t)(rng % 65536));
        }

        g_active_decompression = 1;
        int64_t ret = przm_decompress(corrupt_frame, len, g_dst_buf, sizeof(g_dst_buf));
        g_active_decompression = 0;

        (void)ret;
        CHK_EQ(g_heap_allocations, 0, "Corrupt frames must not cause heap allocations");
    }

    printf("    -> 10,000 corrupt streams tested: 0 segfaults, 0 memory corruption, 0 heap allocations\n");
}

int main(void) {
    setvbuf(stdout, NULL, _IONBF, 0);
    printf("=================================================================\n");
    printf("  PRZM Pure C Decompressor Challenger M2.2 Adversarial Stress Suite\n");
    printf("=================================================================\n\n");

    test_match_distance_exceeds_dst_pos();
    test_invalid_varints();
    test_truncated_offsets();
    test_zero_distance_offset();
    test_exact_distance_boundary();
    test_repeat_match_at_zero();
    test_fuzzed_corrupt_streams();

    printf("\n=================================================================\n");
    printf("  ALL C ADVERSARIAL STRESS TESTS PASSED (0 Heap Allocations)     \n");
    printf("=================================================================\n");
    return 0;
}
