/*
 * Challenger M2 Adversarial Test Suite for Discrete Heisenberg Group H_3(F_{2^8})
 * Tests boundary lengths (0, 1, 2, unaligned 10, 11, 25), SWAR tail handling,
 * 100,000 algebraic involution cycles, and frame decompression in Pure C.
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

static inline void write_u32_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
    p[3] = (uint8_t)(v >> 24);
}

/* Simple LCG PRNG for reproducibility */
static uint64_t g_seed = 0x123456789ABCDEF0ULL;
static inline uint8_t rand_byte(void) {
    g_seed = g_seed * 6364136223846793005ULL + 1442695040888963407ULL;
    return (uint8_t)(g_seed >> 56);
}

/* 1. Algebraic Involution 100k Fuzzing in C */
static void test_c_algebraic_involution_100k(void) {
    printf("[START] C Algebraic Involution 100,000 Cycles...\n");
    for (int iter = 0; iter < 100000; iter++) {
        uint8_t triplet[3];
        triplet[0] = rand_byte();
        triplet[1] = rand_byte();
        triplet[2] = rand_byte();

        uint8_t orig[3];
        memcpy(orig, triplet, 3);

        /* Foliation (first unfoliate call) */
        int err1 = przm_heisenberg_unfoliate_inplace(triplet, 3, 3);
        CHK(err1 == PRZM_OK, "triplet unfoliate 1 failed");
        CHK(triplet[0] == orig[0], "triplet channel X changed");
        CHK(triplet[1] == orig[1], "triplet channel Y changed");

        /* Involution (second unfoliate call) */
        int err2 = przm_heisenberg_unfoliate_inplace(triplet, 3, 3);
        CHK(err2 == PRZM_OK, "triplet unfoliate 2 failed");
        CHK(triplet[0] == orig[0] && triplet[1] == orig[1] && triplet[2] == orig[2],
            "triplet algebraic involution failed");

        /* Quad test (stride 4) */
        uint8_t quad[4];
        quad[0] = orig[0];
        quad[1] = orig[1];
        quad[2] = orig[2];
        quad[3] = rand_byte();
        uint8_t orig_quad[4];
        memcpy(orig_quad, quad, 4);

        int qerr1 = przm_heisenberg_unfoliate_inplace(quad, 4, 4);
        CHK(qerr1 == PRZM_OK, "quad unfoliate 1 failed");
        CHK(quad[0] == orig_quad[0], "quad channel X changed");
        CHK(quad[1] == orig_quad[1], "quad channel Y changed");
        CHK(quad[3] == orig_quad[3], "quad channel W (alpha) changed");

        int qerr2 = przm_heisenberg_unfoliate_inplace(quad, 4, 4);
        CHK(qerr2 == PRZM_OK, "quad unfoliate 2 failed");
        CHK(memcmp(quad, orig_quad, 4) == 0, "quad algebraic involution failed");
    }
    printf("[PASS] C Algebraic Involution 100,000 Cycles\n");
}

/* 2. Boundary and Unaligned Buffer Lengths in C */
static void test_c_boundary_and_unaligned_lengths(void) {
    printf("[START] C Boundary & Unaligned Lengths (0..512)...\n");

    const size_t test_sizes[] = {
        0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
        16, 17, 18, 23, 24, 25, 31, 32, 33, 47, 48, 49,
        63, 64, 65, 95, 96, 97, 127, 128, 129, 255, 256, 257, 511, 512
    };
    size_t num_sizes = sizeof(test_sizes) / sizeof(test_sizes[0]);

    uint8_t orig[1024];
    uint8_t buf[1024];

    for (size_t s = 0; s < sizeof(orig); s++) {
        orig[s] = rand_byte();
    }

    for (size_t si = 0; si < num_sizes; si++) {
        size_t len = test_sizes[si];

        for (uint8_t stride = 3; stride <= 4; stride++) {
            memcpy(buf, orig, len);

            /* Canary check: byte right after len must not be modified */
            uint8_t canary = 0xA5;
            buf[len] = canary;

            int status = przm_heisenberg_unfoliate_inplace(buf, len, stride);
            CHK(status == PRZM_OK, "unfoliate failed on valid length");
            CHK(buf[len] == canary, "canary overwritten (buffer overflow)");

            if (len < stride) {
                /* When len < stride, buffer must be 100% untouched */
                CHK(memcmp(buf, orig, len) == 0, "buffer modified when len < stride");
            } else {
                size_t full_blocks = len / stride;
                size_t tail_start = full_blocks * stride;

                /* Unaligned tail bytes must be completely untouched */
                for (size_t t = tail_start; t < len; t++) {
                    CHK(buf[t] == orig[t], "unaligned tail byte modified");
                }

                /* Channel X and Y (and W for stride 4) must be untouched */
                for (size_t b = 0; b < full_blocks; b++) {
                    size_t idx = b * stride;
                    CHK(buf[idx + 0] == orig[idx + 0], "channel X modified");
                    CHK(buf[idx + 1] == orig[idx + 1], "channel Y modified");
                    if (stride == 4) {
                        CHK(buf[idx + 3] == orig[idx + 3], "channel W modified");
                    }
                }

                /* Second unfoliation must restore exact original bytes */
                status = przm_heisenberg_unfoliate_inplace(buf, len, stride);
                CHK(status == PRZM_OK, "second unfoliate failed");
                CHK(memcmp(buf, orig, len) == 0, "involution failed for length");
            }
        }
    }

    /* Exhaustive check on every single length from 0 to 256 */
    for (size_t len = 0; len <= 256; len++) {
        for (uint8_t stride = 3; stride <= 4; stride++) {
            memcpy(buf, orig, len);
            int status = przm_heisenberg_unfoliate_inplace(buf, len, stride);
            CHK(status == PRZM_OK, "exhaustive unfoliate 1 failed");
            status = przm_heisenberg_unfoliate_inplace(buf, len, stride);
            CHK(status == PRZM_OK, "exhaustive unfoliate 2 failed");
            CHK(memcmp(buf, orig, len) == 0, "exhaustive involution failed");
        }
    }

    printf("[PASS] C Boundary & Unaligned Lengths\n");
}

/* 3. Pure C przm_decompress Heisenberg Frame Handling */
static void test_c_frame_decompress(void) {
    printf("[START] C przm_decompress Heisenberg Frame Tests...\n");

    uint8_t raw[120];
    for (size_t i = 0; i < sizeof(raw); i++) raw[i] = (uint8_t)(i * 7 + 3);

    uint8_t frame[256];
    uint8_t dst[256];

    /* Test 3.1: Mode 0 (Raw Bypass) on boundary lengths: 0, 1, 2, 10, 11, 25 */
    const size_t m0_sizes[] = {0, 1, 2, 10, 11, 25, 48};
    for (size_t i = 0; i < sizeof(m0_sizes) / sizeof(m0_sizes[0]); i++) {
        size_t len = m0_sizes[i];

        /* Construct Heisenberg Mode 0 frame */
        write_u32_le(frame + 0, PRZM_MAGIC_HEISENBERG);
        write_u32_le(frame + 4, (uint32_t)len);
        frame[8] = 3; /* stride = 3 */
        frame[9] = 0; /* mode = 0 (Raw) */
        frame[10] = 0;
        frame[11] = 0;
        if (len > 0) {
            memcpy(frame + 12, raw, len);
        }

        memset(dst, 0xEE, sizeof(dst));
        int64_t dec = przm_decompress(frame, 12 + len, dst, sizeof(dst));
        CHK(dec == (int64_t)len, "Mode 0 decompress return size mismatch");
        if (len > 0) {
            CHK(memcmp(dst, raw, len) == 0, "Mode 0 decompressed data mismatch");
        }
    }

    /* Test 3.2: Mode 1 (Foliated) on boundary lengths: 3, 4, 10, 11, 12, 13, 25 */
    const size_t m1_sizes[] = {3, 4, 10, 11, 12, 13, 24, 25, 48};
    for (size_t i = 0; i < sizeof(m1_sizes) / sizeof(m1_sizes[0]); i++) {
        size_t len = m1_sizes[i];

        write_u32_le(frame + 0, PRZM_MAGIC_HEISENBERG);
        write_u32_le(frame + 4, (uint32_t)len);
        frame[8] = 3; /* stride = 3 */
        frame[9] = 1; /* mode = 1 (Foliated) */
        frame[10] = 0;
        frame[11] = 0;

        /* Foliate payload */
        memcpy(frame + 12, raw, len);
        przm_heisenberg_unfoliate_inplace(frame + 12, len, 3);

        memset(dst, 0xEE, sizeof(dst));
        int64_t dec = przm_decompress(frame, 12 + len, dst, sizeof(dst));
        CHK(dec == (int64_t)len, "Mode 1 decompress return size mismatch");
        CHK(memcmp(dst, raw, len) == 0, "Mode 1 decompressed data mismatch");
    }

    /* Test 3.3: przm_get_uncompressed_size on Heisenberg frame */
    write_u32_le(frame + 0, PRZM_MAGIC_HEISENBERG);
    write_u32_le(frame + 4, 12345);
    int64_t ulen = przm_get_uncompressed_size(frame, 12);
    CHK(ulen == 12345, "przm_get_uncompressed_size returned wrong length for Heisenberg frame");

    /* Test 3.4: Malformed and corrupted frames */
    /* Truncated header */
    CHK(przm_decompress(frame, 11, dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "truncated header not rejected");

    /* Illegal stride */
    frame[8] = 5;
    CHK(przm_decompress(frame, 16, dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "stride 5 not rejected");
    frame[8] = 0;
    CHK(przm_decompress(frame, 16, dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "stride 0 not rejected");

    /* Illegal mode */
    frame[8] = 3;
    frame[9] = 7;
    write_u32_le(frame + 4, 10);
    CHK(przm_decompress(frame, 22, dst, sizeof(dst)) == PRZM_ERR_UNSUPPORTED_MODE, "illegal mode not rejected");

    /* Truncated payload in Mode 0 */
    frame[9] = 0;
    write_u32_le(frame + 4, 50);
    CHK(przm_decompress(frame, 20, dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME, "truncated payload not rejected");

    /* dst_cap too small */
    write_u32_le(frame + 4, 100);
    CHK(przm_decompress(frame, 112, dst, 50) == PRZM_ERR_BUFFER_TOO_SMALL, "small dst_cap not rejected");

    printf("[PASS] C przm_decompress Heisenberg Frame Tests\n");
}

int main(void) {
    printf("=== Running Challenger M2 Heisenberg Adversarial Verification Suite ===\n\n");
    test_c_algebraic_involution_100k();
    test_c_boundary_and_unaligned_lengths();
    test_c_frame_decompress();
    printf("\n>>> ALL CHALLENGER M2 VERIFICATION SUITES PASSED! <<<\n");
    return 0;
}
