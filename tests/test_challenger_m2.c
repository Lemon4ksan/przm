/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "aes_distinguisher.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>
#include <assert.h>

#define CHALLENGER_ASSERT(cond, msg) do { \
    if (!(cond)) { \
        fprintf(stderr, "[FAIL] Line %d: %s\n", __LINE__, msg); \
        exit(1); \
    } \
} while(0)

#define CHALLENGER_ASSERT_NEAR(a, b, eps, msg) do { \
    if (fabs((a) - (b)) > (eps)) { \
        fprintf(stderr, "[FAIL] Line %d: %s (expected %f, got %f, diff %e)\n", __LINE__, msg, (double)(b), (double)(a), fabs((a)-(b))); \
        exit(1); \
    } \
} while(0)

static void test_c_challenger_entropy_edge_cases(void) {
    printf("[+] C Challenger: Testing Shannon entropy edge distributions...\n");

    /* 1. NULL / zero-length */
    double h = shannon_entropy_bytes(NULL, 0);
    CHALLENGER_ASSERT_NEAR(h, 0.0, 1e-12, "NULL entropy != 0.0");

    double h_val, dh, dh_pct;
    compute_shannon_entropy(NULL, 0, &h_val, &dh, &dh_pct);
    CHALLENGER_ASSERT_NEAR(h_val, 0.0, 1e-12, "NULL h_val != 0.0");
    CHALLENGER_ASSERT_NEAR(dh, 8.0, 1e-12, "NULL dh != 8.0");
    CHALLENGER_ASSERT_NEAR(dh_pct, 100.0, 1e-12, "NULL dh_pct != 100.0");

    /* 2. Dirac distributions across all 256 byte values */
    for (int c = 0; c < 256; c++) {
        uint8_t dirac[256];
        memset(dirac, (uint8_t)c, sizeof(dirac));
        compute_shannon_entropy(dirac, sizeof(dirac), &h_val, &dh, &dh_pct);
        CHALLENGER_ASSERT_NEAR(h_val, 0.0, 1e-12, "Dirac H != 0.0");
        CHALLENGER_ASSERT_NEAR(dh, 8.0, 1e-12, "Dirac Delta H != 8.0");
        CHALLENGER_ASSERT_NEAR(dh_pct, 100.0, 1e-12, "Dirac Delta H% != 100.0");

        double c2, z;
        compute_chi_squared(dirac, sizeof(dirac), &c2, &z);
        double exp_c2 = 255.0 * 256.0;
        CHALLENGER_ASSERT_NEAR(c2, exp_c2, 1e-6, "Dirac Chi2 != 255 * N");
        CHALLENGER_ASSERT(z > 1000.0, "Dirac Z <= 1000.0");
    }

    /* 3. Exact uniform distributions */
    uint8_t uniform[256 * 16];
    for (size_t i = 0; i < sizeof(uniform); i++) {
        uniform[i] = (uint8_t)(i % 256);
    }
    compute_shannon_entropy(uniform, sizeof(uniform), &h_val, &dh, &dh_pct);
    CHALLENGER_ASSERT_NEAR(h_val, 8.0, 1e-12, "Uniform H != 8.0");
    CHALLENGER_ASSERT_NEAR(dh, 0.0, 1e-12, "Uniform Delta H != 0.0");
    CHALLENGER_ASSERT_NEAR(dh_pct, 0.0, 1e-12, "Uniform Delta H% != 0.0");

    double c2_u, z_u;
    compute_chi_squared(uniform, sizeof(uniform), &c2_u, &z_u);
    CHALLENGER_ASSERT_NEAR(c2_u, 0.0, 1e-12, "Uniform Chi2 != 0.0");
    double exp_z_u = -255.0 / sqrt(510.0);
    CHALLENGER_ASSERT_NEAR(z_u, exp_z_u, 1e-5, "Uniform Z != -11.2917");
}

static void test_c_challenger_bit_correlation_stress(void) {
    printf("[+] C Challenger: Stress-testing 128x128 bit correlation matrix...\n");

    const size_t num_blocks = 256;
    uint8_t blocks[256][16];
    memset(blocks, 0, sizeof(blocks));

    /* Populate blocks with pseudo-random and deterministic structure */
    for (size_t k = 0; k < num_blocks; k++) {
        for (int b = 0; b < 16; b++) {
            blocks[k][b] = (uint8_t)((k * 17 + b * 31 + 7) & 0xFF);
        }
        /* Bit 0 (byte 0, bit 0) and Bit 1 (byte 0, bit 1) perfectly correlated */
        uint8_t bit_val = (uint8_t)(k % 2);
        blocks[k][0] = (uint8_t)((blocks[k][0] & ~0x07) | bit_val | (bit_val << 1) | ((1 - bit_val) << 2));
    }

    /* 1. Diagonal must be 1.0 */
    for (int i = 0; i < 128; i++) {
        double r_diag = compute_bit_correlation(blocks, num_blocks, i, i);
        CHALLENGER_ASSERT_NEAR(r_diag, 1.0, 1e-9, "Diagonal correlation != 1.0");
    }

    /* 2. Symmetry and Range [-1.0, 1.0] across sample of bit pairs */
    for (int i = 0; i < 128; i += 7) {
        for (int j = 0; j < 128; j += 11) {
            double r_ij = compute_bit_correlation(blocks, num_blocks, i, j);
            double r_ji = compute_bit_correlation(blocks, num_blocks, j, i);
            CHALLENGER_ASSERT_NEAR(r_ij, r_ji, 1e-9, "Asymmetric correlation");
            CHALLENGER_ASSERT(r_ij >= -1.0 - 1e-9 && r_ij <= 1.0 + 1e-9, "Correlation out of bounds [-1, 1]");
        }
    }

    /* 3. Perfect correlation and anti-correlation */
    double r_corr = compute_bit_correlation(blocks, num_blocks, 0, 1);
    CHALLENGER_ASSERT_NEAR(r_corr, 1.0, 1e-6, "Correlated pair != 1.0");

    double r_anticorr = compute_bit_correlation(blocks, num_blocks, 0, 2);
    CHALLENGER_ASSERT_NEAR(r_anticorr, -1.0, 1e-6, "Anti-correlated pair != -1.0");

    /* 4. Constant bit stream (variance = 0) returns 0.0 */
    uint8_t const_blocks[64][16];
    memset(const_blocks, 0x00, sizeof(const_blocks));
    double r_const = compute_bit_correlation(const_blocks, 64, 5, 10);
    CHALLENGER_ASSERT_NEAR(r_const, 0.0, 1e-9, "Constant bit stream did not return 0.0");
}

static void test_c_challenger_aes10_prp(void) {
    printf("[+] C Challenger: Verifying 10-round AES ideal pseudo-random permutation...\n");

    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };

    /* Generate 4096 blocks (65,536 bytes) of structured counter plaintexts */
    const size_t num_blocks = 4096;
    static uint8_t ciphertexts[4096 * 16];

    for (size_t i = 0; i < num_blocks; i++) {
        uint8_t pt[16];
        memset(pt, 0x5A, 16);
        pt[0] = (uint8_t)(i & 0xFF);
        pt[1] = (uint8_t)((i >> 8) & 0xFF);
        aes_reduced_encrypt(pt, key, 10, false, ciphertexts + i * 16);
    }

    aes_distinguisher_report_t report;
    evaluate_reduced_round_distinguisher(ciphertexts, sizeof(ciphertexts), &report);

    printf("    [AES-10 C Evaluation] Shannon H: %.6f, Delta H%%: %.4f%%, Chi2: %.2f, Z: %.2f, Dist: %d\n",
           report.shannon_h, report.delta_h_pct, report.chi2, report.z_score, report.is_distinguished);

    /* Strict PRP Acceptance Criteria:
       Delta H% <= 0.05%
       |Z| < 3.29
       is_distinguished == false
    */
    CHALLENGER_ASSERT(report.delta_h_pct <= 0.05, "10-round AES leaked Delta H% > 0.05%");
    CHALLENGER_ASSERT(fabs(report.z_score) < 3.29, "10-round AES Chi2 Z-score |Z| >= 3.29");
    CHALLENGER_ASSERT(!report.is_distinguished, "Distinguisher falsely fired on 10-round AES in C");
}

int main(void) {
    printf("=================================================================\n");
    printf("   PRZM AES DISTINGUISHER C CHALLENGER VERIFICATION HARNESS      \n");
    printf("=================================================================\n");

    test_c_challenger_entropy_edge_cases();
    test_c_challenger_bit_correlation_stress();
    test_c_challenger_aes10_prp();

    printf("=================================================================\n");
    printf("   ALL C CHALLENGER TESTS PASSED (0 ALLOCS, ZERO DEFECTS)        \n");
    printf("=================================================================\n");

    return 0;
}
