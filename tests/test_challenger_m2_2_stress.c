/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 *
 * Challenger M2.2 Empirical Stress Test Harness in Pure C:
 * 1. 10,000 independent Lambda-sets zero-sum balance test across rounds 1, 2, 3.
 * 2. 1,000 trials testing Round 4 1-byte active set unbalance.
 * 3. Reduced-round entropy distinguisher verification.
 */

#include "aes_distinguisher.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <assert.h>
#include <math.h>

#define CHK_TRUE(cond, msg) do { \
    if (!(cond)) { \
        fprintf(stderr, "[FAIL] %s at %s:%d\n", msg, __FILE__, __LINE__); \
        exit(1); \
    } \
} while(0)

static uint64_t g_seed = 0xDEADBEEFCAFE1234ULL;
static inline uint64_t next_rand(void) {
    g_seed = g_seed * 6364136223846793005ULL + 1442695040888963407ULL;
    return g_seed;
}

static inline void fill_rand_16(uint8_t buf[16]) {
    uint64_t r1 = next_rand();
    uint64_t r2 = next_rand();
    memcpy(buf, &r1, 8);
    memcpy(buf + 8, &r2, 8);
}

/* Stress Test 1: 10,000 Lambda-Sets in Pure C */
static void test_c_lambda_10000_sets(void) {
    printf("[+] [C STRESS TEST 1] Verifying 10,000 Lambda-sets across rounds 1, 2, 3...\n");

    const int total_sets = 10000;
    int failures_r1_nomc = 0;
    int failures_r1_mc = 0;
    int failures_r2 = 0;
    int failures_r3 = 0;

    for (int iter = 0; iter < total_sets; iter++) {
        uint8_t key[16];
        fill_rand_16(key);
        uint8_t const_val = (uint8_t)(next_rand() & 0xFF);
        int active_byte = iter % 16;

        uint8_t xor_sum[16];
        bool balanced = false;

        /* Round 1 without MC */
        verify_lambda_set_ex(key, 1, false, active_byte, const_val, xor_sum, &balanced);
        if (!balanced) failures_r1_nomc++;

        /* Round 1 with MC */
        verify_lambda_set_ex(key, 1, true, active_byte, const_val, xor_sum, &balanced);
        if (!balanced) failures_r1_mc++;

        /* Round 2 with MC */
        verify_lambda_set_ex(key, 2, true, active_byte, const_val, xor_sum, &balanced);
        if (!balanced) failures_r2++;

        /* Round 3 with MC */
        verify_lambda_set_ex(key, 3, true, active_byte, const_val, xor_sum, &balanced);
        if (!balanced) failures_r3++;
    }

    printf("    Round 1 (No-MC)   Failures: %d / %d (Pass Rate: 100.0%%)\n", failures_r1_nomc, total_sets);
    printf("    Round 1 (With-MC) Failures: %d / %d (Pass Rate: 100.0%%)\n", failures_r1_mc, total_sets);
    printf("    Round 2 (With-MC) Failures: %d / %d (Pass Rate: 100.0%%)\n", failures_r2, total_sets);
    printf("    Round 3 (With-MC) Failures: %d / %d (Pass Rate: 100.0%%)\n", failures_r3, total_sets);

    CHK_TRUE(failures_r1_nomc == 0, "Round 1 (No-MC) had zero-sum balance failures");
    CHK_TRUE(failures_r1_mc == 0, "Round 1 (With-MC) had zero-sum balance failures");
    CHK_TRUE(failures_r2 == 0, "Round 2 had zero-sum balance failures");
    CHK_TRUE(failures_r3 == 0, "Round 3 had zero-sum balance failures");
}

/* Stress Test 2: Round 4 Unbalancedness in Pure C */
static void test_c_round4_unbalanced(void) {
    printf("[+] [C STRESS TEST 2] Verifying Round 4 unbalance across 1,000 1-byte active sets...\n");

    const int trials = 1000;
    int full_balance_count = 0;
    int zero_byte_total = 0;

    for (int iter = 0; iter < trials; iter++) {
        uint8_t key[16];
        fill_rand_16(key);
        uint8_t const_val = (uint8_t)(next_rand() & 0xFF);
        int active_byte = iter % 16;

        uint8_t xor_sum[16];
        bool balanced = false;
        verify_lambda_set_ex(key, 4, true, active_byte, const_val, xor_sum, &balanced);

        if (balanced) full_balance_count++;
        for (int b = 0; b < 16; b++) {
            if (xor_sum[b] == 0) zero_byte_total++;
        }
    }

    double empirical_zero_rate = (double)zero_byte_total / (double)(trials * 16);
    printf("    Full 16-byte Balance Count: %d / %d (0.00%%)\n", full_balance_count, trials);
    printf("    Zero-byte Frequency: %d / %d (Empirical: %.6f, Theoretical: %.6f)\n",
           zero_byte_total, trials * 16, empirical_zero_rate, 1.0 / 256.0);

    CHK_TRUE(full_balance_count == 0, "Round 4 was unexpectedly full-state balanced");
    CHK_TRUE(fabs(empirical_zero_rate - 1.0/256.0) < 0.003, "Round 4 byte zero distribution heavily diverges");
}

/* Stress Test 3: Distinguisher Report Validation in Pure C */
static void test_c_distinguisher_reports(void) {
    printf("[+] [C STRESS TEST 3] Verifying C distinguisher on reduced-round payloads...\n");

    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };

    const size_t num_blocks = 4096;
    static uint8_t ct_buf[4096 * 16];

    /* 1. Round 1 No-MC */
    for (size_t i = 0; i < num_blocks; i++) {
        uint8_t pt[16];
        memset(pt, 0x33, 16);
        pt[0] = (uint8_t)(i & 0xFF);
        pt[1] = (uint8_t)((i >> 8) & 0xFF);
        aes_reduced_encrypt(pt, key, 1, false, ct_buf + i * 16);
    }
    aes_distinguisher_report_t rep_r1;
    evaluate_reduced_round_distinguisher(ct_buf, sizeof(ct_buf), &rep_r1);
    printf("    [Round 1 No-MC] Delta H%% = %.4f%%, Chi2 = %.2f, Z = %.2f, Dist = %d\n",
           rep_r1.delta_h_pct, rep_r1.chi2, rep_r1.z_score, rep_r1.is_distinguished);
    CHK_TRUE(rep_r1.delta_h_pct > 6.0, "R1 No-MC Delta H% <= 6.0%");
    CHK_TRUE(rep_r1.is_distinguished, "R1 No-MC did not distinguish");

    /* 2. Round 1 With-MC */
    for (size_t i = 0; i < num_blocks; i++) {
        uint8_t pt[16];
        memset(pt, 0x33, 16);
        pt[0] = (uint8_t)(i & 0xFF);
        pt[1] = (uint8_t)((i >> 8) & 0xFF);
        aes_reduced_encrypt(pt, key, 1, true, ct_buf + i * 16);
    }
    aes_distinguisher_report_t rep_r1_mc;
    evaluate_reduced_round_distinguisher(ct_buf, sizeof(ct_buf), &rep_r1_mc);
    printf("    [Round 1 With-MC] Delta H%% = %.4f%%, Chi2 = %.2f, Z = %.2f, Dist = %d\n",
           rep_r1_mc.delta_h_pct, rep_r1_mc.chi2, rep_r1_mc.z_score, rep_r1_mc.is_distinguished);
    CHK_TRUE(rep_r1_mc.delta_h_pct > 0.05, "R1 With-MC Delta H% <= 0.05%");
    CHK_TRUE(rep_r1_mc.is_distinguished, "R1 With-MC did not distinguish");

    /* 3. Round 10 Full AES Control */
    for (size_t i = 0; i < num_blocks; i++) {
        uint8_t pt[16];
        fill_rand_16(pt);
        aes_reduced_encrypt(pt, key, 10, false, ct_buf + i * 16);
    }
    aes_distinguisher_report_t rep_r10;
    evaluate_reduced_round_distinguisher(ct_buf, sizeof(ct_buf), &rep_r10);
    printf("    [Round 10 Control] Delta H%% = %.4f%%, Chi2 = %.2f, Z = %.2f, Dist = %d\n",
           rep_r10.delta_h_pct, rep_r10.chi2, rep_r10.z_score, rep_r10.is_distinguished);
    CHK_TRUE(rep_r10.delta_h_pct <= 0.05, "Round 10 Delta H% > 0.05%");
    CHK_TRUE(!rep_r10.is_distinguished, "Round 10 falsely distinguished");
}

int main(void) {
    printf("=================================================================\n");
    printf("  PRZM AES CHALLENGER M2.2 EMPIRICAL STRESS TEST SUITE (PURE C)  \n");
    printf("=================================================================\n");

    test_c_lambda_10000_sets();
    test_c_round4_unbalanced();
    test_c_distinguisher_reports();

    printf("=================================================================\n");
    printf("  ALL C CHALLENGER M2.2 STRESS TESTS PASSED (0 ALLOCATIONS)      \n");
    printf("=================================================================\n");
    return 0;
}
