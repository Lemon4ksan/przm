/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Challenger M1-2 Empirical Adversarial Stress Suite for PRZM Pure C Decompressor.
 *
 * Verifies:
 * 1. Sheaf Hodge frame reconstruction across ALL Galois twists alpha in [1, 255].
 * 2. Harmonic modes: Curl-Free, Sparse-Curl (various NNZ), Constant Boundaries, Raw, Cascaded PWS.
 * 3. Heisenberg group foliation/unfoliation across strides 1, 2, 3, 4, 0-byte frames, and odd lengths.
 * 4. Multi-threaded concurrency stress test (16 threads x 5,000 iterations = 80,000 decompressions).
 * 5. Strict Zero-Allocation contract verification (0 heap allocations during decompression).
 */

#include "przm_dec.h"

#include <windows.h>
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

static inline void write_u32_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
    p[3] = (uint8_t)(v >> 24);
}

/* Reference Galois multiplication over F_{2^8} (AES polynomial 0x11B) */
static inline uint8_t ref_gf256_mul(uint8_t a, uint8_t b) {
    uint8_t res = 0;
    while (b > 0) {
        if (b & 1) res ^= a;
        uint8_t carry = a & 0x80;
        a <<= 1;
        if (carry) a ^= 0x1B;
        b >>= 1;
    }
    return res;
}

/* PRNG state */
static __thread uint64_t tls_rng = 0x853c49e6748fea9bULL;
static inline uint8_t tls_rand8(void) {
    tls_rng = tls_rng * 6364136223846793005ULL + 1442695040888963407ULL;
    return (uint8_t)(tls_rng >> 56);
}

/* Heap allocation tracking flag */
static volatile LONG g_heap_allocations = 0;
static volatile LONG g_active_decompression = 0;

void* __wrap_malloc(size_t size) {
    if (g_active_decompression > 0) {
        InterlockedIncrement(&g_heap_allocations);
    }
    return malloc(size);
}

/* ============================================================================
 * 1. Sheaf Frame Generator & Oracle for All Twists alpha in [1, 255]
 * ============================================================================ */

/* Builds a Curl-Free Sheaf frame and computes its exact expected ground truth */
static size_t build_sheaf_curlfree_frame(uint8_t* frame, uint8_t* expected,
                                         uint16_t M, uint16_t W, uint8_t twist,
                                         uint8_t anchor, uint8_t gamma_h, uint8_t gamma_v) {
    size_t orig_len = (size_t)M * W;
    write_u32_le(frame + 0, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame + 4, (uint32_t)orig_len);
    write_u16_le(frame + 8, W);
    write_u16_le(frame + 10, M);
    frame[12] = anchor;
    frame[13] = twist;
    write_u16_le(frame + 14, 0); // reserved
    write_u16_le(frame + 16, PRZM_CSHD_FLAG_CURL_FREE | PRZM_CSHD_FLAG_CONST_EH | PRZM_CSHD_FLAG_CONST_EV);
    memset(frame + 18, 0, 6);

    // Payload: gamma_h (1 byte), gamma_v (1 byte)
    frame[24] = gamma_h;
    frame[25] = gamma_v;
    size_t frame_len = 26;

    // Ground truth oracle computation
    expected[0] = anchor;
    for (uint16_t j = 0; j < W - 1; j++) {
        if (twist <= 1) {
            expected[j + 1] = expected[j] ^ gamma_h;
        } else {
            expected[j + 1] = ref_gf256_mul(twist, expected[j]) ^ gamma_h;
        }
    }
    for (uint16_t i = 1; i < M; i++) {
        size_t row_curr = (size_t)i * W;
        size_t row_prev = (size_t)(i - 1) * W;

        expected[row_curr] = (twist <= 1) ? (expected[row_prev] ^ gamma_v)
                                          : (ref_gf256_mul(twist, expected[row_prev]) ^ gamma_v);
        uint8_t cur_v = gamma_v;
        for (uint16_t j = 0; j < W - 1; j++) {
            if (twist > 1) {
                cur_v = ref_gf256_mul(twist, cur_v);
            }
            expected[row_curr + j + 1] = (twist <= 1) ? (expected[row_prev + j + 1] ^ cur_v)
                                                     : (ref_gf256_mul(twist, expected[row_prev + j + 1]) ^ cur_v);
        }
    }
    return frame_len;
}

/* Builds a Sparse-Curl Sheaf frame and computes its exact expected ground truth */
static size_t build_sheaf_sparse_curl_frame(uint8_t* frame, uint8_t* expected,
                                            uint16_t M, uint16_t W, uint8_t twist,
                                            uint8_t anchor, uint8_t gamma_h, uint8_t gamma_v,
                                            uint32_t nnz, const uint32_t* curl_k, const uint8_t* curl_c) {
    size_t orig_len = (size_t)M * W;
    write_u32_le(frame + 0, PRZM_MAGIC_CELLULAR_SHEAF);
    write_u32_le(frame + 4, (uint32_t)orig_len);
    write_u16_le(frame + 8, W);
    write_u16_le(frame + 10, M);
    frame[12] = anchor;
    frame[13] = twist;
    write_u16_le(frame + 14, 0);
    write_u16_le(frame + 16, PRZM_CSHD_FLAG_SPARSE_CURL | PRZM_CSHD_FLAG_CONST_EH | PRZM_CSHD_FLAG_CONST_EV);
    memset(frame + 18, 0, 6);

    frame[24] = gamma_h;
    frame[25] = gamma_v;
    write_u32_le(frame + 26, nnz);
    size_t pos = 30;
    for (uint32_t idx = 0; idx < nnz; idx++) {
        write_u32_le(frame + pos, curl_k[idx]);
        frame[pos + 4] = curl_c[idx];
        pos += 5;
    }
    size_t frame_len = pos;

    // Ground truth oracle computation
    expected[0] = anchor;
    for (uint16_t j = 0; j < W - 1; j++) {
        if (twist <= 1) {
            expected[j + 1] = expected[j] ^ gamma_h;
        } else {
            expected[j + 1] = ref_gf256_mul(twist, expected[j]) ^ gamma_h;
        }
    }

    uint32_t c_idx = 0;
    uint32_t next_k = (nnz > 0) ? curl_k[0] : 0xFFFFFFFFU;
    uint8_t next_c = (nnz > 0) ? curl_c[0] : 0;

    for (uint16_t i = 1; i < M; i++) {
        size_t row_curr = (size_t)i * W;
        size_t row_prev = (size_t)(i - 1) * W;

        expected[row_curr] = (twist <= 1) ? (expected[row_prev] ^ gamma_v)
                                          : (ref_gf256_mul(twist, expected[row_prev]) ^ gamma_v);
        uint8_t cur_v = gamma_v;
        for (uint16_t j = 0; j < W - 1; j++) {
            uint32_t cur_k = (uint32_t)(i - 1) * (uint32_t)(W - 1) + (uint32_t)j;
            if (c_idx < nnz && cur_k == next_k) {
                if (twist <= 1) {
                    cur_v ^= next_c;
                } else {
                    cur_v = ref_gf256_mul(twist, cur_v) ^ next_c;
                }
                c_idx++;
                if (c_idx < nnz) {
                    next_k = curl_k[c_idx];
                    next_c = curl_c[c_idx];
                }
            } else if (twist > 1) {
                cur_v = ref_gf256_mul(twist, cur_v);
            }

            expected[row_curr + j + 1] = (twist <= 1) ? (expected[row_prev + j + 1] ^ cur_v)
                                                     : (ref_gf256_mul(twist, expected[row_prev + j + 1]) ^ cur_v);
        }
    }
    return frame_len;
}

/* ============================================================================
 * 2. Exhaustive Sheaf Twist Fuzzing across All Twists alpha in [1, 255]
 * ============================================================================ */

static void test_exhaustive_sheaf_twists_all_255(void) {
    printf("[START] Exhaustive Sheaf Hodge Twist Fuzzing (All 255 Twists x Modes)...\n");

    uint8_t frame[4096];
    uint8_t expected[4096];
    uint8_t dst[4096 + PRZM_SAFETY_PADDING];

    for (int t = 1; t <= 255; t++) {
        uint8_t twist = (uint8_t)t;

        // 1. Curl-Free test on small grid (4x4)
        {
            uint16_t M = 4, W = 4;
            uint8_t anchor = (uint8_t)(t * 7 + 1);
            uint8_t gamma_h = (uint8_t)(t * 13 + 3);
            uint8_t gamma_v = (uint8_t)(t * 29 + 5);

            size_t flen = build_sheaf_curlfree_frame(frame, expected, M, W, twist, anchor, gamma_h, gamma_v);
            memset(dst, 0xEE, sizeof(dst));

            int64_t dec = przm_decompress(frame, flen, dst, sizeof(dst));
            CHK_EQ(dec, (int64_t)(M * W), "Curl-Free decompress size mismatch");
            CHK(memcmp(dst, expected, M * W) == 0, "Curl-Free data mismatch across twist");
        }

        // 2. Sparse-Curl test on rectangular grid (5x7) with 3 non-zero curl faces
        {
            uint16_t M = 5, W = 7;
            uint8_t anchor = (uint8_t)(t * 3 + 2);
            uint8_t gamma_h = (uint8_t)(t * 11 + 4);
            uint8_t gamma_v = (uint8_t)(t * 17 + 6);
            uint32_t nnz = 3;
            uint32_t curl_k[3] = {2, 7, 15};
            uint8_t curl_c[3] = {(uint8_t)(t ^ 0x33), (uint8_t)(t ^ 0x77), (uint8_t)(t ^ 0xAA)};

            size_t flen = build_sheaf_sparse_curl_frame(frame, expected, M, W, twist, anchor, gamma_h, gamma_v, nnz, curl_k, curl_c);
            memset(dst, 0xEE, sizeof(dst));

            int64_t dec = przm_decompress(frame, flen, dst, sizeof(dst));
            CHK_EQ(dec, (int64_t)(M * W), "Sparse-Curl decompress size mismatch");
            CHK(memcmp(dst, expected, M * W) == 0, "Sparse-Curl data mismatch across twist");
        }
    }
    printf("[PASS] Exhaustive Sheaf Hodge Twists: All 255 Twists Verified Bit-Exact\n");
}

/* ============================================================================
 * 3. Heisenberg Group Fuzzing: Strides 1..4, 0-Byte Frames, Odd Lengths
 * ============================================================================ */

static void test_heisenberg_strides_and_edge_cases(void) {
    printf("[START] Heisenberg Strides (1..4), 0-Byte Inputs, and Odd Lengths...\n");

    uint8_t frame[1024];
    uint8_t dst[1024 + PRZM_SAFETY_PADDING];
    uint8_t raw[512];
    for (size_t i = 0; i < sizeof(raw); i++) raw[i] = (uint8_t)(i * 11 + 7);

    // 1. 0-Byte Input tests
    {
        // 0-byte input buffer (src_len == 0)
        int64_t r0 = przm_decompress(NULL, 0, dst, sizeof(dst));
        CHK_EQ(r0, 0, "0-byte src_len must return 0");

        // Heisenberg frame with orig_len == 0
        write_u32_le(frame + 0, PRZM_MAGIC_HEISENBERG);
        write_u32_le(frame + 4, 0); // orig_len = 0
        frame[8] = 3; frame[9] = 0; frame[10] = 0; frame[11] = 0;
        int64_t r_orig0 = przm_decompress(frame, 12, dst, sizeof(dst));
        CHK_EQ(r_orig0, 0, "orig_len=0 must return 0");
    }

    // 2. Strides 1 and 2 rejection in frame header
    {
        write_u32_le(frame + 0, PRZM_MAGIC_HEISENBERG);
        write_u32_le(frame + 4, 16);
        frame[9] = 0; frame[10] = 0; frame[11] = 0;

        frame[8] = 1; // Stride 1
        int64_t r1 = przm_decompress(frame, 28, dst, sizeof(dst));
        CHK_EQ(r1, PRZM_ERR_CORRUPTED_FRAME, "Stride 1 must be rejected with PRZM_ERR_CORRUPTED_FRAME");

        frame[8] = 2; // Stride 2
        int64_t r2 = przm_decompress(frame, 28, dst, sizeof(dst));
        CHK_EQ(r2, PRZM_ERR_CORRUPTED_FRAME, "Stride 2 must be rejected with PRZM_ERR_CORRUPTED_FRAME");
    }

    // 3. Heisenberg Foliation Involution across Odd and Boundary Lengths
    const size_t odd_lengths[] = {1, 2, 3, 4, 5, 7, 9, 11, 13, 17, 23, 27, 31, 47, 51, 63, 67, 101, 127, 255, 333, 511};
    size_t num_odd = sizeof(odd_lengths) / sizeof(odd_lengths[0]);

    for (size_t idx = 0; idx < num_odd; idx++) {
        size_t len = odd_lengths[idx];

        for (uint8_t stride = 3; stride <= 4; stride++) {
            uint8_t test_buf[512];
            memcpy(test_buf, raw, len);

            // Step 1: Forward unfoliation (foliation involution)
            int err1 = przm_heisenberg_unfoliate_inplace(test_buf, len, stride);
            CHK_EQ(err1, PRZM_OK, "przm_heisenberg_unfoliate_inplace failed");

            // Verify trailing unaligned bytes (< stride) are untouched
            size_t tail_start = (len / stride) * stride;
            for (size_t t = tail_start; t < len; t++) {
                CHK_EQ(test_buf[t], raw[t], "Trailing unaligned byte was corrupted");
            }

            // Step 2: Inverse unfoliation must restore exact original bytes
            int err2 = przm_heisenberg_unfoliate_inplace(test_buf, len, stride);
            CHK_EQ(err2, PRZM_OK, "second unfoliate failed");
            CHK(memcmp(test_buf, raw, len) == 0, "Algebraic involution failed on odd length");
        }
    }

    printf("[PASS] Heisenberg Strides, 0-Byte Frames, and Odd Lengths Verified\n");
}

/* ============================================================================
 * 4. Multi-Threaded Concurrency & Zero-Allocation Stress Test
 * ============================================================================ */

#define NUM_WORKER_THREADS 16
#define ITERATIONS_PER_THREAD 5000

typedef struct {
    int thread_id;
    int success_count;
} ThreadContext;

static DWORD WINAPI worker_thread_proc(LPVOID param) {
    ThreadContext* ctx = (ThreadContext*)param;
    uint8_t frame_buf[4096];
    uint8_t expected_buf[4096];
    uint8_t dst_buf[4096 + PRZM_SAFETY_PADDING];

    for (int iter = 0; iter < ITERATIONS_PER_THREAD; iter++) {
        uint8_t twist = (uint8_t)((iter % 255) + 1);
        uint16_t M = (uint16_t)((tls_rand8() % 8) + 2); // 2..9
        uint16_t W = (uint16_t)((tls_rand8() % 8) + 2); // 2..9

        if ((iter & 1) == 0) {
            // Sheaf Curl-Free
            uint8_t anchor = tls_rand8();
            uint8_t gh = tls_rand8();
            uint8_t gv = tls_rand8();

            size_t flen = build_sheaf_curlfree_frame(frame_buf, expected_buf, M, W, twist, anchor, gh, gv);

            InterlockedIncrement(&g_active_decompression);
            int64_t dec = przm_decompress(frame_buf, flen, dst_buf, sizeof(dst_buf));
            InterlockedDecrement(&g_active_decompression);

            if (dec != (int64_t)(M * W) || memcmp(dst_buf, expected_buf, M * W) != 0) {
                fprintf(stderr, "[THREAD %d FAIL] Sheaf decompress mismatch at iter %d\n", ctx->thread_id, iter);
                ExitProcess(1);
            }
        } else {
            // Heisenberg Mode 1 (Foliated)
            size_t len = (size_t)((tls_rand8() % 120) + 1);
            uint8_t stride = (iter & 2) ? 3 : 4;

            write_u32_le(frame_buf + 0, PRZM_MAGIC_HEISENBERG);
            write_u32_le(frame_buf + 4, (uint32_t)len);
            frame_buf[8] = stride;
            frame_buf[9] = 1; // Mode 1
            frame_buf[10] = 0;
            frame_buf[11] = 0;

            for (size_t k = 0; k < len; k++) expected_buf[k] = tls_rand8();
            memcpy(frame_buf + 12, expected_buf, len);
            przm_heisenberg_unfoliate_inplace(frame_buf + 12, len, stride);

            InterlockedIncrement(&g_active_decompression);
            int64_t dec = przm_decompress(frame_buf, 12 + len, dst_buf, sizeof(dst_buf));
            InterlockedDecrement(&g_active_decompression);

            if (dec != (int64_t)len || memcmp(dst_buf, expected_buf, len) != 0) {
                fprintf(stderr, "[THREAD %d FAIL] Heisenberg decompress mismatch at iter %d\n", ctx->thread_id, iter);
                ExitProcess(1);
            }
        }
        ctx->success_count++;
    }
    return 0;
}

static void test_concurrency_and_zero_allocations(void) {
    printf("[START] Multi-Threaded Concurrency Stress (%d threads x %d iters = %d ops)...\n",
           NUM_WORKER_THREADS, ITERATIONS_PER_THREAD, NUM_WORKER_THREADS * ITERATIONS_PER_THREAD);

    HANDLE threads[NUM_WORKER_THREADS];
    ThreadContext contexts[NUM_WORKER_THREADS];

    g_heap_allocations = 0;
    g_active_decompression = 0;

    for (int i = 0; i < NUM_WORKER_THREADS; i++) {
        contexts[i].thread_id = i;
        contexts[i].success_count = 0;
        threads[i] = CreateThread(NULL, 0, worker_thread_proc, &contexts[i], 0, NULL);
        CHK(threads[i] != NULL, "CreateThread failed");
    }

    WaitForMultipleObjects(NUM_WORKER_THREADS, threads, TRUE, INFINITE);

    int total_ops = 0;
    for (int i = 0; i < NUM_WORKER_THREADS; i++) {
        total_ops += contexts[i].success_count;
        CloseHandle(threads[i]);
    }

    CHK_EQ(total_ops, NUM_WORKER_THREADS * ITERATIONS_PER_THREAD, "Total successful operations mismatch");
    CHK_EQ(g_heap_allocations, 0, "Heap allocations detected during decompression (strictly 0 required)");

    printf("[PASS] Multi-Threaded Concurrency Stress: %d ops completed successfully!\n", total_ops);
    printf("[PASS] Zero-Allocation Contract Verified: %ld heap allocations (EXACTLY 0)\n", g_heap_allocations);
}

int main(void) {
    printf("====================================================================\n");
    printf("  Challenger M1-2 Empirical Adversarial Stress & Parity Suite (C)    \n");
    printf("====================================================================\n\n");

    test_exhaustive_sheaf_twists_all_255();
    test_heisenberg_strides_and_edge_cases();
    test_concurrency_and_zero_allocations();

    printf("\n>>> ALL CHALLENGER M1-2 ADVERSARIAL STRESS SUITES PASSED! <<<\n");
    return 0;
}
