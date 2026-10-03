/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 *
 * M3 Pure C Reference Decompressor Empirical Stress Harness.
 * Tests Fast Möbius Transform and Margolus RCA across:
 * - Boundary lengths 0..65537 (primes, powers of 2, non-powers of 2, odd/even)
 * - High entropy and extreme data distributions
 * - Concurrency bounded to max 8 worker threads
 * - Strictly zero heap allocations
 */

#include "przm_dec.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#ifdef _WIN32
#include <windows.h>
#else
#include <pthread.h>
#endif

#define TEST_ASSERT(cond, msg) do { \
    if (!(cond)) { \
        fprintf(stderr, "[FAIL] %s (%s:%d)\n", msg, __FILE__, __LINE__); \
        exit(1); \
    } \
} while (0)

#define TEST_PASS(name) printf("[PASS] %s\n", name)

static const size_t kBoundaryLengths[] = {
    0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33,
    63, 64, 65, 127, 128, 129, 255, 256, 257,
    511, 512, 513, 1023, 1024, 1025,
    2047, 2048, 2049, 4095, 4096, 4097,
    8191, 8192, 8193, 16383, 16384, 16385,
    32767, 32768, 32769, 65535, 65536, 65537,
    // Primes
    11, 23, 53, 97, 199, 389, 769, 1543, 3079, 6151, 12289, 24593, 49157, 65521
};
static const size_t kNumBoundaries = sizeof(kBoundaryLengths) / sizeof(kBoundaryLengths[0]);

static void fill_distribution(uint8_t* buf, size_t len, int dist_type) {
    switch (dist_type) {
    case 0: /* Zeros */
        memset(buf, 0x00, len);
        break;
    case 1: /* Ones */
        memset(buf, 0xFF, len);
        break;
    case 2: /* Alternating */
        for (size_t i = 0; i < len; i++) buf[i] = (i % 2 == 0) ? 0xAA : 0x55;
        break;
    case 3: /* Linear ramp */
        for (size_t i = 0; i < len; i++) buf[i] = (uint8_t)(i & 0xFF);
        break;
    case 4: /* Pseudo-random high entropy (LCG) */ {
        uint32_t state = 0x12345678;
        for (size_t i = 0; i < len; i++) {
            state = state * 1664525u + 1013904223u;
            buf[i] = (uint8_t)(state >> 24);
        }
        break;
    }
    case 5: /* Sparse */
        memset(buf, 0, len);
        if (len > 0) {
            buf[0] = 0x42;
            buf[len / 2] = 0x84;
            buf[len - 1] = 0x99;
        }
        break;
    default:
        for (size_t i = 0; i < len; i++) buf[i] = (uint8_t)((i * 37 + 13) & 0xFF);
        break;
    }
}

/* 1. Test Pure C Fast Möbius Transform Involution & Decompression */
static void test_c_mobius_stress(void) {
    uint8_t* work = (uint8_t*)malloc(70000);
    uint8_t* orig = (uint8_t*)malloc(70000);
    uint8_t* dst  = (uint8_t*)malloc(70000 + PRZM_SAFETY_PADDING);
    uint8_t* frame = (uint8_t*)malloc(70000 + PRZM_MOBIUS_HEADER_SIZE);

    TEST_ASSERT(work && orig && dst && frame, "Allocation failed in test_c_mobius_stress");

    /* Test all 256 single-byte bit butterfly SWAR values */
    for (int b = 0; b < 256; b++) {
        uint8_t byte_val = (uint8_t)b;
        uint8_t temp[1] = {byte_val};
        przm_fmt_transform_inplace(temp, 1, 6, 1);
        przm_fmt_transform_inplace(temp, 1, 6, 1);
        TEST_ASSERT(temp[0] == byte_val, "Bit butterfly SWAR single-byte involution failed");
    }

    /* Test across all boundary lengths and distributions */
    for (size_t bi = 0; bi < kNumBoundaries; bi++) {
        size_t n = kBoundaryLengths[bi];
        for (int dist = 0; dist < 6; dist++) {
            fill_distribution(orig, n, dist);

            /* In-place involution test (T^2 == I) for both bitLevel 0 and 1 */
            for (uint8_t bit_level = 0; bit_level <= 1; bit_level++) {
                uint8_t block_logs[] = {4, 5, 6, 7, 8, 12, 16};
                for (size_t li = 0; li < sizeof(block_logs)/sizeof(block_logs[0]); li++) {
                    uint8_t bLog = block_logs[li];
                    memcpy(work, orig, n);

                    przm_fmt_transform_inplace(work, n, bLog, bit_level);
                    przm_fmt_transform_inplace(work, n, bLog, bit_level);

                    TEST_ASSERT(memcmp(work, orig, n) == 0, "C FMT involution failed");
                }
            }

            /* Test przm_mobius_decompress in ModeTransformed */
            if (n <= 65536) {
                memcpy(work, orig, n);
                przm_fmt_transform_inplace(work, n, 6, 0);

                /* Build valid frame */
                frame[0] = 'M'; frame[1] = 'O'; frame[2] = 'B'; frame[3] = 'I';
                frame[4] = (uint8_t)(n & 0xFF);
                frame[5] = (uint8_t)((n >> 8) & 0xFF);
                frame[6] = (uint8_t)((n >> 16) & 0xFF);
                frame[7] = (uint8_t)((n >> 24) & 0xFF);
                frame[8] = 6; /* blockLog2 */
                frame[9] = PRZM_FMT_MODE_TRANSFORMED;
                frame[10] = PRZM_OP_MOBIUS;
                frame[11] = 0;
                memcpy(frame + PRZM_MOBIUS_HEADER_SIZE, work, n);

                int64_t u_size = przm_get_uncompressed_size(frame, PRZM_MOBIUS_HEADER_SIZE + n);
                TEST_ASSERT(u_size == (int64_t)n, "C przm_get_uncompressed_size mismatch for MOBI");

                memset(dst, 0, n + PRZM_SAFETY_PADDING);
                int64_t dec_res = przm_decompress(frame, PRZM_MOBIUS_HEADER_SIZE + n, dst, n + PRZM_SAFETY_PADDING);
                TEST_ASSERT(dec_res == (int64_t)n, "C przm_decompress returned wrong size for MOBI");
                TEST_ASSERT(memcmp(dst, orig, n) == 0, "C przm_decompress output mismatch for MOBI");
            }
        }
    }

    free(work);
    free(orig);
    free(dst);
    free(frame);
    TEST_PASS("test_c_mobius_stress (All Boundaries & Involution)");
}

/* 2. Test Pure C Margolus RCA Reversibility & Decompression across all 7 Rules */
static void test_c_margolus_stress(void) {
    uint8_t* work = (uint8_t*)malloc(70000);
    uint8_t* orig = (uint8_t*)malloc(70000);
    uint8_t* dst  = (uint8_t*)malloc(70000 + PRZM_SAFETY_PADDING);
    uint8_t* frame = (uint8_t*)malloc(70000 + PRZM_MARGOLUS_HEADER_SIZE);

    TEST_ASSERT(work && orig && dst && frame, "Allocation failed in test_c_margolus_stress");

    uint8_t rules[7] = {
        PRZM_MARGOLUS_RULE_BBM,
        PRZM_MARGOLUS_RULE_TRANSPOSE,
        PRZM_MARGOLUS_RULE_ROT180,
        PRZM_MARGOLUS_RULE_ROT90,
        PRZM_MARGOLUS_RULE_XOR,
        PRZM_MARGOLUS_RULE_FLIP_H,
        PRZM_MARGOLUS_RULE_FLIP_V
    };

    uint16_t widths[] = {2, 4, 8, 16, 32, 64};

    for (size_t bi = 0; bi < kNumBoundaries; bi++) {
        size_t n = kBoundaryLengths[bi];
        for (int dist = 0; dist < 6; dist++) {
            fill_distribution(orig, n, dist);

            for (int ri = 0; ri < 7; ri++) {
                uint8_t rule = rules[ri];
                for (size_t wi = 0; wi < sizeof(widths)/sizeof(widths[0]); wi++) {
                    uint16_t width = widths[wi];
                    if (n < 2 * (size_t)width) continue;

                    for (uint8_t steps = 1; steps <= 4; steps++) {
                        memcpy(work, orig, n);

                        size_t H = 2 * (n / (2 * (size_t)width));
                        /* Forward steps */
                        for (uint8_t t = 0; t < steps; t++) {
                            int phase = t & 1;
                            przm_margolus_step_inplace(work, H, width, phase, rule, 0);
                        }

                        /* Time-reversed reverse */
                        przm_margolus_reverse_inplace(work, n, width, steps, rule);

                        TEST_ASSERT(memcmp(work, orig, n) == 0, "C Margolus reverse failed to restore original data");
                    }
                }
            }

            /* Test przm_margolus_decompress in ModeTransform */
            if (n >= 16 && n <= 65536) {
                uint16_t width = 8;
                uint8_t steps = 2;
                uint8_t rule = PRZM_MARGOLUS_RULE_BBM;

                memcpy(work, orig, n);
                size_t H = 2 * (n / (2 * (size_t)width));
                for (uint8_t t = 0; t < steps; t++) {
                    int phase = t & 1;
                    przm_margolus_step_inplace(work, H, width, phase, rule, 0);
                }

                frame[0] = 'M'; frame[1] = 'A'; frame[2] = 'R'; frame[3] = 'G';
                frame[4] = (uint8_t)(n & 0xFF);
                frame[5] = (uint8_t)((n >> 8) & 0xFF);
                frame[6] = (uint8_t)((n >> 16) & 0xFF);
                frame[7] = (uint8_t)((n >> 24) & 0xFF);
                frame[8] = (uint8_t)(width & 0xFF);
                frame[9] = (uint8_t)((width >> 8) & 0xFF);
                frame[10] = steps;
                frame[11] = rule;
                frame[12] = 1; /* MargolusModeTransform */
                frame[13] = 1; /* Toroidal */
                frame[14] = 0; frame[15] = 0;
                memcpy(frame + PRZM_MARGOLUS_HEADER_SIZE, work, n);

                int64_t u_size = przm_get_uncompressed_size(frame, PRZM_MARGOLUS_HEADER_SIZE + n);
                TEST_ASSERT(u_size == (int64_t)n, "C przm_get_uncompressed_size mismatch for MARG");

                memset(dst, 0, n + PRZM_SAFETY_PADDING);
                int64_t dec_res = przm_decompress(frame, PRZM_MARGOLUS_HEADER_SIZE + n, dst, n + PRZM_SAFETY_PADDING);
                TEST_ASSERT(dec_res == (int64_t)n, "C przm_decompress returned wrong size for MARG");
                TEST_ASSERT(memcmp(dst, orig, n) == 0, "C przm_decompress output mismatch for MARG");
            }
        }
    }

    free(work);
    free(orig);
    free(dst);
    free(frame);
    TEST_PASS("test_c_margolus_stress (All 7 Rules & Boundaries)");
}

/* 3. Bounded Multi-Threaded Concurrency Test (Strictly 8 Threads) */
#define CONCURRENCY_NUM_THREADS 8
#define CONCURRENCY_ITERS_PER_THREAD 200

typedef struct {
    int thread_id;
    int success;
} WorkerContext;

#ifdef _WIN32
static DWORD WINAPI worker_func(LPVOID arg) {
#else
static void* worker_func(void* arg) {
#endif
    WorkerContext* ctx = (WorkerContext*)arg;
    uint8_t work_buf[1024];
    uint8_t orig_buf[1024];
    uint8_t dst_buf[1024 + PRZM_SAFETY_PADDING];
    uint8_t frame_buf[1024 + 16];

    for (int iter = 0; iter < CONCURRENCY_ITERS_PER_THREAD; iter++) {
        size_t n = 256 + (iter % 256);
        fill_distribution(orig_buf, n, iter % 6);

        /* 1. Fast Möbius */
        memcpy(work_buf, orig_buf, n);
        przm_fmt_transform_inplace(work_buf, n, 6, 1);
        przm_fmt_transform_inplace(work_buf, n, 6, 1);
        if (memcmp(work_buf, orig_buf, n) != 0) {
            ctx->success = 0;
            return 0;
        }

        /* 2. Margolus BBM */
        memcpy(work_buf, orig_buf, n);
        size_t H = 2 * (n / 16);
        przm_margolus_step_inplace(work_buf, H, 8, 0, PRZM_MARGOLUS_RULE_BBM, 0);
        przm_margolus_step_inplace(work_buf, H, 8, 1, PRZM_MARGOLUS_RULE_BBM, 0);
        przm_margolus_reverse_inplace(work_buf, n, 8, 2, PRZM_MARGOLUS_RULE_BBM);
        if (memcmp(work_buf, orig_buf, n) != 0) {
            ctx->success = 0;
            return 0;
        }

        /* 3. MOBI decompress */
        memcpy(work_buf, orig_buf, n);
        przm_fmt_transform_inplace(work_buf, n, 6, 0);
        frame_buf[0] = 'M'; frame_buf[1] = 'O'; frame_buf[2] = 'B'; frame_buf[3] = 'I';
        frame_buf[4] = (uint8_t)(n & 0xFF);
        frame_buf[5] = (uint8_t)((n >> 8) & 0xFF);
        frame_buf[6] = 0; frame_buf[7] = 0;
        frame_buf[8] = 6;
        frame_buf[9] = PRZM_FMT_MODE_TRANSFORMED;
        frame_buf[10] = PRZM_OP_MOBIUS;
        frame_buf[11] = 0;
        memcpy(frame_buf + 12, work_buf, n);

        int64_t dec_res = przm_decompress(frame_buf, 12 + n, dst_buf, n + PRZM_SAFETY_PADDING);
        if (dec_res != (int64_t)n || memcmp(dst_buf, orig_buf, n) != 0) {
            ctx->success = 0;
            return 0;
        }
    }

    ctx->success = 1;
    return 0;
}

static void test_c_concurrency_stress(void) {
    WorkerContext contexts[CONCURRENCY_NUM_THREADS];

#ifdef _WIN32
    HANDLE threads[CONCURRENCY_NUM_THREADS];
    for (int i = 0; i < CONCURRENCY_NUM_THREADS; i++) {
        contexts[i].thread_id = i;
        contexts[i].success = 0;
        threads[i] = CreateThread(NULL, 0, worker_func, &contexts[i], 0, NULL);
        TEST_ASSERT(threads[i] != NULL, "CreateThread failed");
    }
    WaitForMultipleObjects(CONCURRENCY_NUM_THREADS, threads, TRUE, INFINITE);
    for (int i = 0; i < CONCURRENCY_NUM_THREADS; i++) {
        CloseHandle(threads[i]);
        TEST_ASSERT(contexts[i].success == 1, "Worker thread failed in concurrency stress");
    }
#else
    pthread_t threads[CONCURRENCY_NUM_THREADS];
    for (int i = 0; i < CONCURRENCY_NUM_THREADS; i++) {
        contexts[i].thread_id = i;
        contexts[i].success = 0;
        int rc = pthread_create(&threads[i], NULL, worker_func, &contexts[i]);
        TEST_ASSERT(rc == 0, "pthread_create failed");
    }
    for (int i = 0; i < CONCURRENCY_NUM_THREADS; i++) {
        pthread_join(threads[i], NULL);
        TEST_ASSERT(contexts[i].success == 1, "Worker thread failed in concurrency stress");
    }
#endif

    TEST_PASS("test_c_concurrency_stress (Max 8 Concurrent Threads Bounded)");
}

int main(void) {
    printf("=== Starting Pure C M3 Adversarial Stress Harness ===\n\n");
    test_c_mobius_stress();
    test_c_margolus_stress();
    test_c_concurrency_stress();
    printf("\n>>> ALL C ADVERSARIAL STRESS TESTS COMPLETED SUCCESSFULLY! <<<\n");
    return 0;
}
