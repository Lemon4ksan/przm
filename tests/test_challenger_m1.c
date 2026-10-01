/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Challenger M1 Empirical Adversarial Challenge Harness (C Reference Engine)
 */

#include "aes_distinguisher.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>

#define CHK(cond, msg) do { \
    if (!(cond)) { \
        fprintf(stderr, "[FAIL] %s:%d: %s\n", __FILE__, __LINE__, msg); \
        exit(1); \
    } \
} while (0)

#define CHK_MEM_EQ(a, b, len, msg) do { \
    if (memcmp(a, b, len) != 0) { \
        fprintf(stderr, "[FAIL] %s:%d: %s\n", __FILE__, __LINE__, msg); \
        exit(1); \
    } \
} while (0)

/* Simple 64-bit LCG PRNG */
static uint64_t g_rng = 0x123456789ABCDEF0ULL;
static inline uint64_t rand64(void) {
    g_rng = g_rng * 6364136223846793005ULL + 1442695040888963407ULL;
    return g_rng;
}
static inline uint8_t rand8(void) {
    return (uint8_t)(rand64() >> 56);
}

/* 1. Key Schedule Linear & Non-Linear Invariant Challenge across 100,000 keys */
static void challenge_key_schedule_100000(void) {
    printf("[+] Running C challenge_key_schedule_100000 (100,000 keys x 30 invariants = 3,000,000 checks)...\n");

    const uint8_t *sbox = aes_get_sbox();
    const uint8_t rcon[11] = {0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36};

    for (int trial = 0; trial < 100000; trial++) {
        uint8_t key[16];
        for (int i = 0; i < 16; i++) {
            key[i] = rand8();
        }

        uint8_t round_keys[11][16];
        aes_expand_key(key, 10, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);

        uint32_t w[44];
        for (int r = 0; r <= 10; r++) {
            for (int col = 0; col < 4; col++) {
                w[4 * r + col] = (uint32_t)round_keys[r][4 * col] |
                                 ((uint32_t)round_keys[r][4 * col + 1] << 8) |
                                 ((uint32_t)round_keys[r][4 * col + 2] << 16) |
                                 ((uint32_t)round_keys[r][4 * col + 3] << 24);
            }
        }

        for (int r = 1; r <= 10; r++) {
            /* Linear invariant for k in {1, 2, 3} */
            for (int k = 1; k <= 3; k++) {
                uint32_t w_curr = w[4 * r + k];
                uint32_t w_prev_in_r = w[4 * r + k - 1];
                uint32_t w_prev_r = w[4 * (r - 1) + k];

                CHK((w_curr ^ w_prev_in_r) == w_prev_r, "Linear invariant violated");
            }

            /* Non-linear invariant for k = 0 */
            uint32_t w0_curr = w[4 * r];
            uint32_t w0_prev_r = w[4 * (r - 1)];
            uint32_t w_last = w[4 * r - 1];

            /* RotWord: b0,b1,b2,b3 -> b1,b2,b3,b0 */
            uint8_t b0 = (uint8_t)w_last;
            uint8_t b1 = (uint8_t)(w_last >> 8);
            uint8_t b2 = (uint8_t)(w_last >> 16);
            uint8_t b3 = (uint8_t)(w_last >> 24);

            uint32_t sb = (uint32_t)sbox[b1] |
                          ((uint32_t)sbox[b2] << 8) |
                          ((uint32_t)sbox[b3] << 16) |
                          ((uint32_t)sbox[b0] << 24);

            uint32_t expected_diff = sb ^ (uint32_t)rcon[r];
            uint32_t actual_diff = w0_curr ^ w0_prev_r;
            CHK(actual_diff == expected_diff, "Non-linear key schedule expansion mismatch at col 0");
        }
    }
    printf("[PASS] Key schedule: 100,000 keys verified bit-exact!\n");
}

/* 2. FMT Boundary Challenge across all 256 Unit Basis Vectors */
static void challenge_fmt_unit_basis_all256(void) {
    printf("[+] Running C challenge_fmt_unit_basis_all256 (256 unit basis vectors)...\n");

    for (int i = 0; i < 256; i++) {
        uint8_t tt[32];
        memset(tt, 0, 32);
        tt[i / 8] = (uint8_t)(1 << (i % 8));

        uint8_t anf[32];
        fast_mobius_transform_u8(tt, anf);

        /* Involution */
        uint8_t inv[32];
        fast_mobius_transform_u8(anf, inv);
        CHK_MEM_EQ(inv, tt, 32, "FMT unit basis involution failed");

        /* Exact mathematical incidence: a_u = 1 iff (i & ~u) == 0 */
        for (int u = 0; u < 256; u++) {
            uint8_t actual = (uint8_t)((anf[u / 8] >> (u % 8)) & 1);
            uint8_t expected = ((i & ~u) == 0) ? 1 : 0;
            CHK(actual == expected, "FMT unit basis incidence mismatch");
        }

        /* Degree must be 8 for any unit basis vector */
        int deg = compute_anf_degree(anf);
        CHK(deg == 8, "Unit basis vector degree != 8");
    }
    printf("[PASS] FMT unit basis: All 256 basis vectors verified!\n");
}

/* 3. FMT Involution and Linearity on 10,000 Random Truth Tables */
static void challenge_fmt_random_involution_and_linearity(void) {
    printf("[+] Running C challenge_fmt_random_involution_and_linearity (10,000 trials)...\n");

    for (int trial = 0; trial < 10000; trial++) {
        uint8_t t1[32], t2[32], t_xor[32];
        for (int i = 0; i < 32; i++) {
            t1[i] = rand8();
            t2[i] = rand8();
            t_xor[i] = (uint8_t)(t1[i] ^ t2[i]);
        }

        /* Involution on t1 */
        uint8_t anf1[32], inv1[32];
        fast_mobius_transform_u8(t1, anf1);
        fast_mobius_transform_u8(anf1, inv1);
        CHK_MEM_EQ(inv1, t1, 32, "FMT random involution failed");

        /* Linearity */
        uint8_t anf2[32], anf_xor[32];
        fast_mobius_transform_u8(t2, anf2);
        fast_mobius_transform_u8(t_xor, anf_xor);

        for (int i = 0; i < 32; i++) {
            CHK(anf_xor[i] == (uint8_t)(anf1[i] ^ anf2[i]), "FMT linearity violated");
        }
    }
    printf("[PASS] FMT: 10,000 random truth tables involution & linearity verified!\n");
}

/* 4. S-box Spectrum Rigorous Re-verification */
static void challenge_sbox_spectrum(void) {
    printf("[+] Running C challenge_sbox_spectrum...\n");
    const uint8_t *sbox = aes_get_sbox();
    anf_profile_t profiles[8];
    analyze_sbox(sbox, profiles);

    const int exp_active[8] = {132, 133, 145, 136, 131, 114, 112, 110};
    const int exp_deg7[8]   = {3, 4, 5, 4, 3, 2, 2, 2};

    int total_deg7 = 0;
    for (int k = 0; k < 8; k++) {
        CHK(profiles[k].degree == 7, "S-box coordinate degree != 7");
        CHK(profiles[k].top_monomial_zero, "a_{255} != 0");
        CHK(profiles[k].degree_histogram[8] == 0, "Degree 8 histogram != 0");
        CHK(profiles[k].has_degree_defect, "Degree defect flag not set");
        CHK(profiles[k].active_monomials == exp_active[k], "Active monomial count mismatch");
        CHK(profiles[k].degree_histogram[7] == exp_deg7[k], "Degree 7 monomial count mismatch");
        total_deg7 += profiles[k].degree_histogram[7];
    }
    CHK(total_deg7 == 25, "Total degree-7 monomials across 8 coordinates != 25");
    printf("[PASS] S-box spectrum: All 8 coordinates verified!\n");
}

int main(void) {
    printf("=================================================================\n");
    printf("   CHALLENGER M1 EMPIRICAL ADVERSARIAL C HARNESS                 \n");
    printf("=================================================================\n");

    challenge_key_schedule_100000();
    challenge_fmt_unit_basis_all256();
    challenge_fmt_random_involution_and_linearity();
    challenge_sbox_spectrum();

    printf("=================================================================\n");
    printf("   ALL C EMPIRICAL CHALLENGES PASSED SUCCESSFULLY!               \n");
    printf("=================================================================\n");
    return 0;
}
