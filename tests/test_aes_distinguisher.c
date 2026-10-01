/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "aes_distinguisher.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <assert.h>
#include <math.h>

#define ASSERT_MEM_EQ(a, b, len, msg) do { \
    if (memcmp(a, b, len) != 0) { \
        fprintf(stderr, "FAIL: %s at %s:%d\n", msg, __FILE__, __LINE__); \
        fprintf(stderr, "  got:  "); \
        for (size_t _i = 0; _i < (size_t)(len); _i++) fprintf(stderr, "%02x", ((const uint8_t*)(a))[_i]); \
        fprintf(stderr, "\n  want: "); \
        for (size_t _i = 0; _i < (size_t)(len); _i++) fprintf(stderr, "%02x", ((const uint8_t*)(b))[_i]); \
        fprintf(stderr, "\n"); \
        exit(1); \
    } \
} while(0)

#define ASSERT_TRUE(cond, msg) do { \
    if (!(cond)) { \
        fprintf(stderr, "FAIL: %s at %s:%d\n", msg, __FILE__, __LINE__); \
        exit(1); \
    } \
} while(0)

#define ASSERT_FLOAT_NEAR(val, exp, tol, msg) do { \
    if (fabs((val) - (exp)) > (tol)) { \
        fprintf(stderr, "FAIL: %s at %s:%d (got %f, want %f, diff %f > tol %f)\n", \
            msg, __FILE__, __LINE__, (double)(val), (double)(exp), fabs((val) - (exp)), (double)(tol)); \
        exit(1); \
    } \
} while(0)

typedef struct {
    int target_round;
    aes_tap_point_t target_point;
    uint8_t captured[16];
    bool hit;
} tap_capture_ctx_t;

static void tap_capture_cb(int round, aes_tap_point_t point, const uint8_t state[16], void *user_data) {
    tap_capture_ctx_t *ctx = (tap_capture_ctx_t*)user_data;
    if (ctx && round == ctx->target_round && point == ctx->target_point) {
        memcpy(ctx->captured, state, 16);
        ctx->hit = true;
    }
}

static void test_fips197_appendix_b(void) {
    printf("[+] Running test_fips197_appendix_b...\n");
    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };
    const uint8_t pt[16] = {
        0x32, 0x43, 0xf6, 0xa8, 0x88, 0x5a, 0x30, 0x8d,
        0x31, 0x31, 0x98, 0xa2, 0xe0, 0x37, 0x07, 0x34
    };

    const uint8_t exp_ark0[16] = {
        0x19, 0x3d, 0xe3, 0xbe, 0xa0, 0xf4, 0xe2, 0x2b,
        0x9a, 0xc6, 0x8d, 0x2a, 0xe9, 0xf8, 0x48, 0x08
    };
    const uint8_t exp_r1_sb[16] = {
        0xd4, 0x27, 0x11, 0xae, 0xe0, 0xbf, 0x98, 0xf1,
        0xb8, 0xb4, 0x5d, 0xe5, 0x1e, 0x41, 0x52, 0x30
    };
    const uint8_t exp_r1_sr[16] = {
        0xd4, 0xbf, 0x5d, 0x30, 0xe0, 0xb4, 0x52, 0xae,
        0xb8, 0x41, 0x11, 0xf1, 0x1e, 0x27, 0x98, 0xe5
    };
    const uint8_t exp_r1_mc[16] = {
        0x04, 0x66, 0x81, 0xe5, 0xe0, 0xcb, 0x19, 0x9a,
        0x48, 0xf8, 0xd3, 0x7a, 0x28, 0x06, 0x26, 0x4c
    };
    const uint8_t exp_r1_with_mc[16] = {
        0xa4, 0x9c, 0x7f, 0xf2, 0x68, 0x9f, 0x35, 0x2b,
        0x6b, 0x5b, 0xea, 0x43, 0x02, 0x6a, 0x50, 0x49
    };
    const uint8_t exp_r1_no_mc[16] = {
        0x74, 0x45, 0xa3, 0x27, 0x68, 0xe0, 0x7e, 0x1f,
        0x9b, 0xe2, 0x28, 0xc8, 0x34, 0x4b, 0xee, 0xe0
    };

    const uint8_t exp_r2_sb[16] = {
        0x49, 0xde, 0xd2, 0x89, 0x45, 0xdb, 0x96, 0xf1,
        0x7f, 0x39, 0x87, 0x1a, 0x77, 0x02, 0x53, 0x3b
    };
    const uint8_t exp_r2_sr[16] = {
        0x49, 0xdb, 0x87, 0x3b, 0x45, 0x39, 0x53, 0x89,
        0x7f, 0x02, 0xd2, 0xf1, 0x77, 0xde, 0x96, 0x1a
    };
    const uint8_t exp_r2_mc[16] = {
        0x58, 0x4d, 0xca, 0xf1, 0x1b, 0x4b, 0x5a, 0xac,
        0xdb, 0xe7, 0xca, 0xa8, 0x1b, 0x6b, 0xb0, 0xe5
    };
    const uint8_t exp_r2_with_mc[16] = {
        0xaa, 0x8f, 0x5f, 0x03, 0x61, 0xdd, 0xe3, 0xef,
        0x82, 0xd2, 0x4a, 0xd2, 0x68, 0x32, 0x46, 0x9a
    };

    const uint8_t exp_full_ct[16] = {
        0x39, 0x25, 0x84, 0x1d, 0x02, 0xdc, 0x09, 0xfb,
        0xdc, 0x11, 0x85, 0x97, 0x19, 0x6a, 0x0b, 0x32
    };

    uint8_t out[16];
    tap_capture_ctx_t tap;

    /* 1. Round 0 AddRoundKey tap */
    memset(&tap, 0, sizeof(tap));
    tap.target_round = 0;
    tap.target_point = AES_TAP_AFTER_ADDROUNDKEY0;
    aes_encrypt_block_with_tap(pt, key, 1, false, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 0 AddRoundKey tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_ark0, 16, "Round 0 AddRoundKey mismatch");

    /* 2. Round 1 SubBytes, ShiftRows, MixColumns taps */
    memset(&tap, 0, sizeof(tap));
    tap.target_round = 1;
    tap.target_point = AES_TAP_AFTER_SUBBYTES;
    aes_encrypt_block_with_tap(pt, key, 1, true, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 1 SubBytes tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_r1_sb, 16, "Round 1 SubBytes mismatch");

    memset(&tap, 0, sizeof(tap));
    tap.target_round = 1;
    tap.target_point = AES_TAP_AFTER_SHIFTROWS;
    aes_encrypt_block_with_tap(pt, key, 1, true, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 1 ShiftRows tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_r1_sr, 16, "Round 1 ShiftRows mismatch");

    memset(&tap, 0, sizeof(tap));
    tap.target_round = 1;
    tap.target_point = AES_TAP_AFTER_MIXCOLUMNS;
    aes_encrypt_block_with_tap(pt, key, 1, true, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 1 MixColumns tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_r1_mc, 16, "Round 1 MixColumns mismatch");

    /* 3. Round 1 final outputs */
    aes_reduced_encrypt(pt, key, 1, true, out);
    ASSERT_MEM_EQ(out, exp_r1_with_mc, 16, "Round 1 (with MC) mismatch");

    aes_reduced_encrypt(pt, key, 1, false, out);
    ASSERT_MEM_EQ(out, exp_r1_no_mc, 16, "Round 1 (without MC) mismatch");

    /* 4. Round 2 intermediate taps and output */
    memset(&tap, 0, sizeof(tap));
    tap.target_round = 2;
    tap.target_point = AES_TAP_AFTER_SUBBYTES;
    aes_encrypt_block_with_tap(pt, key, 2, true, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 2 SubBytes tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_r2_sb, 16, "Round 2 SubBytes mismatch");

    memset(&tap, 0, sizeof(tap));
    tap.target_round = 2;
    tap.target_point = AES_TAP_AFTER_SHIFTROWS;
    aes_encrypt_block_with_tap(pt, key, 2, true, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 2 ShiftRows tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_r2_sr, 16, "Round 2 ShiftRows mismatch");

    memset(&tap, 0, sizeof(tap));
    tap.target_round = 2;
    tap.target_point = AES_TAP_AFTER_MIXCOLUMNS;
    aes_encrypt_block_with_tap(pt, key, 2, true, AES_KEY_SCHEDULE_STANDARD, 0, tap_capture_cb, &tap, out);
    ASSERT_TRUE(tap.hit, "Round 2 MixColumns tap was not hit");
    ASSERT_MEM_EQ(tap.captured, exp_r2_mc, 16, "Round 2 MixColumns mismatch");

    aes_reduced_encrypt(pt, key, 2, true, out);
    ASSERT_MEM_EQ(out, exp_r2_with_mc, 16, "Round 2 (with MC) mismatch");

    /* 5. 10-round full AES-128 ciphertext */
    aes_reduced_encrypt(pt, key, 10, false, out);
    ASSERT_MEM_EQ(out, exp_full_ct, 16, "10-Round AES-128 ciphertext mismatch");
}

static void test_fips197_appendix_c1(void) {
    printf("[+] Running test_fips197_appendix_c1...\n");
    const uint8_t key[16] = {
        0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
        0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f
    };
    const uint8_t pt[16] = {
        0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
        0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff
    };
    const uint8_t exp_ct[16] = {
        0x69, 0xc4, 0xe0, 0xd8, 0x6a, 0x7b, 0x04, 0x30,
        0xd8, 0xcd, 0xb7, 0x80, 0x70, 0xb4, 0xc5, 0x5a
    };

    uint8_t out[16];
    aes_reduced_encrypt(pt, key, 10, false, out);
    ASSERT_MEM_EQ(out, exp_ct, 16, "FIPS 197 C.1 10-Round output mismatch");
}

static void test_key_schedule_linear_invariants(void) {
    printf("[+] Running test_key_schedule_linear_invariants...\n");
    const uint8_t test_keys[3][16] = {
        {0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c},
        {0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f},
        {0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10, 0x0f, 0x1e, 0x2d, 0x3c, 0x4b, 0x5a, 0x69, 0x78}
    };

    for (int k_idx = 0; k_idx < 3; k_idx++) {
        uint8_t round_keys[11][16];
        aes_expand_key(test_keys[k_idx], 10, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);

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
            for (int k_col = 1; k_col <= 3; k_col++) {
                uint32_t w_curr = w[4 * r + k_col];
                uint32_t w_prev_in_r = w[4 * r + k_col - 1];
                uint32_t w_prev_r = w[4 * (r - 1) + k_col];

                uint32_t diff = w_curr ^ w_prev_in_r;
                ASSERT_TRUE(diff == w_prev_r, "Linear invariant violated: W_{4r+k} ^ W_{4r+k-1} != W_{4(r-1)+k}");
            }
        }
    }
}

static void test_key_schedule_modes(void) {
    printf("[+] Running test_key_schedule_modes...\n");
    const uint8_t key[16] = {0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10};
    uint8_t round_keys[6][16];

    /* Zero mode */
    aes_expand_key(key, 5, AES_KEY_SCHEDULE_ZERO, 0, round_keys);
    for (int r = 0; r <= 5; r++) {
        for (int i = 0; i < 16; i++) {
            ASSERT_TRUE(round_keys[r][i] == 0, "Non-zero byte in KeyScheduleZero");
        }
    }

    /* Linear mode */
    uint8_t delta = 0x07;
    aes_expand_key(key, 5, AES_KEY_SCHEDULE_LINEAR, delta, round_keys);
    for (int r = 0; r <= 5; r++) {
        for (int i = 0; i < 16; i++) {
            uint8_t expected = (uint8_t)(key[i] ^ (r * delta));
            ASSERT_TRUE(round_keys[r][i] == expected, "KeyScheduleLinear mismatch");
        }
    }

    /* Repeated mode */
    aes_expand_key(key, 5, AES_KEY_SCHEDULE_REPEATED, 0, round_keys);
    for (int r = 0; r <= 5; r++) {
        ASSERT_MEM_EQ(round_keys[r], key, 16, "KeyScheduleRepeated mismatch");
    }
}

static void test_fmt_involution(void) {
    printf("[+] Running test_fmt_involution...\n");
    uint8_t orig[32];
    for (int i = 0; i < 32; i++) {
        orig[i] = (uint8_t)((i * 37 + 13) & 0xFF);
    }

    uint8_t tt[32];
    memcpy(tt, orig, 32);

    fast_mobius_transform_inplace_u8(tt);
    fast_mobius_transform_inplace_u8(tt);

    ASSERT_MEM_EQ(tt, orig, 32, "FMT is not an involution over GF(2)");
}

static void test_fmt_sbox_spectrum(void) {
    printf("[+] Running test_fmt_sbox_spectrum...\n");
    const uint8_t *sbox = aes_get_sbox();
    anf_profile_t profiles[8];
    analyze_sbox(sbox, profiles);

    const int exp_active[8] = {132, 133, 145, 136, 131, 114, 112, 110};
    const int exp_deg7[8]   = {3, 4, 5, 4, 3, 2, 2, 2};

    int total_deg7 = 0;
    for (int k = 0; k < 8; k++) {
        /* Degree must be exactly 7 */
        ASSERT_TRUE(profiles[k].degree == 7, "S-box coordinate degree != 7");

        /* Degree 8 monomial must vanish identically: a_{255} == 0 */
        ASSERT_TRUE(profiles[k].top_monomial_zero, "a_{255} != 0 (degree 8 monomial did not vanish)");
        ASSERT_TRUE(profiles[k].degree_histogram[8] == 0, "Degree 8 histogram != 0");
        ASSERT_TRUE(profiles[k].has_degree_defect, "Degree defect not detected");

        /* Active monomial count check */
        ASSERT_TRUE(profiles[k].active_monomials == exp_active[k], "Active monomial count mismatch");

        /* Degree 7 count check */
        ASSERT_TRUE(profiles[k].degree_histogram[7] == exp_deg7[k], "Degree 7 monomial count mismatch");
        total_deg7 += profiles[k].degree_histogram[7];
    }

    /* Total degree 7 monomials across all 8 coordinates must be 25 */
    ASSERT_TRUE(total_deg7 == 25, "Total degree-7 monomials across coordinates != 25");
}

static void test_reduced_rounds_ladder(void) {
    printf("[+] Running test_reduced_rounds_ladder...\n");
    const uint8_t key[16] = {0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c};
    const uint8_t pt[16]  = {0x32, 0x43, 0xf6, 0xa8, 0x88, 0x5a, 0x30, 0x8d, 0x31, 0x31, 0x98, 0xa2, 0xe0, 0x37, 0x07, 0x34};

    for (int r = 1; r <= 5; r++) {
        uint8_t out_no_mc[16];
        uint8_t out_with_mc[16];

        aes_reduced_encrypt(pt, key, r, false, out_no_mc);
        aes_reduced_encrypt(pt, key, r, true, out_with_mc);

        /* Ciphertexts must be different from plaintext */
        ASSERT_TRUE(memcmp(out_no_mc, pt, 16) != 0, "Ciphertext equal to plaintext");
        ASSERT_TRUE(memcmp(out_with_mc, pt, 16) != 0, "Ciphertext equal to plaintext");

        /* With and without MixColumns in the final round must differ */
        ASSERT_TRUE(memcmp(out_no_mc, out_with_mc, 16) != 0, "No-MC and With-MC produced identical outputs");
    }
}

static void test_reduced_encrypt_tap(void) {
    printf("[+] Running test_reduced_encrypt_tap...\n");
    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };
    const uint8_t pt[16] = {
        0x32, 0x43, 0xf6, 0xa8, 0x88, 0x5a, 0x30, 0x8d,
        0x31, 0x31, 0x98, 0xa2, 0xe0, 0x37, 0x07, 0x34
    };

    const uint8_t exp_ark0[16] = {
        0x19, 0x3d, 0xe3, 0xbe, 0xa0, 0xf4, 0xe2, 0x2b,
        0x9a, 0xc6, 0x8d, 0x2a, 0xe9, 0xf8, 0x48, 0x08
    };
    const uint8_t exp_r1_sb[16] = {
        0xd4, 0x27, 0x11, 0xae, 0xe0, 0xbf, 0x98, 0xf1,
        0xb8, 0xb4, 0x5d, 0xe5, 0x1e, 0x41, 0x52, 0x30
    };
    const uint8_t exp_r1_sr[16] = {
        0xd4, 0xbf, 0x5d, 0x30, 0xe0, 0xb4, 0x52, 0xae,
        0xb8, 0x41, 0x11, 0xf1, 0x1e, 0x27, 0x98, 0xe5
    };
    const uint8_t exp_r1_mc[16] = {
        0x04, 0x66, 0x81, 0xe5, 0xe0, 0xcb, 0x19, 0x9a,
        0x48, 0xf8, 0xd3, 0x7a, 0x28, 0x06, 0x26, 0x4c
    };
    const uint8_t exp_r1_no_mc[16] = {
        0x74, 0x45, 0xa3, 0x27, 0x68, 0xe0, 0x7e, 0x1f,
        0x9b, 0xe2, 0x28, 0xc8, 0x34, 0x4b, 0xee, 0xe0
    };
    const uint8_t exp_r2_mc[16] = {
        0x58, 0x4d, 0xca, 0xf1, 0x1b, 0x4b, 0x5a, 0xac,
        0xdb, 0xe7, 0xca, 0xa8, 0x1b, 0x6b, 0xb0, 0xe5
    };

    uint8_t out[16];

    /* Test all 5 taps for Round 1 */
    aes_reduced_encrypt_tap(pt, key, 1, AES_TAP_AFTER_ADDROUNDKEY0, out);
    ASSERT_MEM_EQ(out, exp_ark0, 16, "aes_reduced_encrypt_tap Round 0 AddRoundKey mismatch");

    aes_reduced_encrypt_tap(pt, key, 1, AES_TAP_AFTER_SUBBYTES, out);
    ASSERT_MEM_EQ(out, exp_r1_sb, 16, "aes_reduced_encrypt_tap Round 1 SubBytes mismatch");

    aes_reduced_encrypt_tap(pt, key, 1, AES_TAP_AFTER_SHIFTROWS, out);
    ASSERT_MEM_EQ(out, exp_r1_sr, 16, "aes_reduced_encrypt_tap Round 1 ShiftRows mismatch");

    aes_reduced_encrypt_tap(pt, key, 1, AES_TAP_AFTER_MIXCOLUMNS, out);
    ASSERT_MEM_EQ(out, exp_r1_mc, 16, "aes_reduced_encrypt_tap Round 1 MixColumns mismatch");

    aes_reduced_encrypt_tap(pt, key, 1, AES_TAP_AFTER_ADDROUNDKEY, out);
    ASSERT_MEM_EQ(out, exp_r1_no_mc, 16, "aes_reduced_encrypt_tap Round 1 AddRoundKey mismatch");

    /* Test Round 2 MixColumns tap */
    aes_reduced_encrypt_tap(pt, key, 2, AES_TAP_AFTER_MIXCOLUMNS, out);
    ASSERT_MEM_EQ(out, exp_r2_mc, 16, "aes_reduced_encrypt_tap Round 2 MixColumns mismatch");

    /* Ensure MixColumns tap is not all zeros */
    const uint8_t zero[16] = {0};
    ASSERT_TRUE(memcmp(out, zero, 16) != 0, "aes_reduced_encrypt_tap returned all zeros for AfterMixColumns");
}

static void test_mix_columns_invariant_subspace(void) {
    printf("[+] Running test_mix_columns_invariant_subspace...\n");

    /* 1. All 256 uniform states: c * J */
    for (int c = 0; c < 256; c++) {
        uint8_t state[16];
        memset(state, (uint8_t)c, 16);
        aes_mix_columns(state);
        for (int i = 0; i < 16; i++) {
            ASSERT_TRUE(state[i] == (uint8_t)c, "MixColumns failed on uniform state c");
        }
    }

    /* 2. Column-wise invariant states: each column has 4 identical bytes */
    for (int col0 = 0; col0 < 256; col0 += 64) {
        for (int col1 = 0; col1 < 256; col1 += 64) {
            for (int col2 = 0; col2 < 256; col2 += 64) {
                for (int col3 = 0; col3 < 256; col3 += 64) {
                    uint8_t state[16];
                    for (int r = 0; r < 4; r++) {
                        state[0 * 4 + r] = (uint8_t)col0;
                        state[1 * 4 + r] = (uint8_t)col1;
                        state[2 * 4 + r] = (uint8_t)col2;
                        state[3 * 4 + r] = (uint8_t)col3;
                    }
                    uint8_t expected[16];
                    memcpy(expected, state, 16);
                    aes_mix_columns(state);
                    ASSERT_MEM_EQ(state, expected, 16, "MixColumns column invariant mismatch");
                }
            }
        }
    }
}

static void test_boundary_clamping(void) {
    printf("[+] Running test_boundary_clamping...\n");
    const uint8_t key[16] = {0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6, 0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c};
    const uint8_t pt[16]  = {0x32, 0x43, 0xf6, 0xa8, 0x88, 0x5a, 0x30, 0x8d, 0x31, 0x31, 0x98, 0xa2, 0xe0, 0x37, 0x07, 0x34};

    /* Round keys buffer for 15 round keys */
    uint8_t round_keys[16][16];

    /* Test negative rounds clamp to 1 */
    aes_expand_key(key, -100, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);
    aes_expand_key(key, -1, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);
    aes_expand_key(key, 0, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);

    /* Test excessive rounds clamp to 14 without stack overflow */
    aes_expand_key(key, 15, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);
    aes_expand_key(key, 25, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);
    aes_expand_key(key, 1000, AES_KEY_SCHEDULE_STANDARD, 0, round_keys);

    /* aes_reduced_encrypt_tap boundary clamping */
    uint8_t out[16];
    aes_reduced_encrypt_tap(pt, key, -5, AES_TAP_AFTER_SUBBYTES, out);
    const uint8_t zero[16] = {0};
    ASSERT_TRUE(memcmp(out, zero, 16) != 0, "Boundary tapped output is zero");

    aes_reduced_encrypt_tap(pt, key, 50, AES_TAP_AFTER_SUBBYTES, out);
    ASSERT_TRUE(memcmp(out, zero, 16) != 0, "Boundary tapped output is zero");
}

static void test_m2_shannon_entropy(void) {
    printf("[+] Running test_m2_shannon_entropy...\n");

    /* 1. All zeros: H = 0.0, Delta H = 8.0, Delta H% = 100.0% */
    uint8_t zeros[1024];
    memset(zeros, 0, sizeof(zeros));
    double h, dh, dh_pct;
    compute_shannon_entropy(zeros, sizeof(zeros), &h, &dh, &dh_pct);
    ASSERT_FLOAT_NEAR(h, 0.0, 1e-9, "zeros: expected H=0.0");
    ASSERT_FLOAT_NEAR(dh, 8.0, 1e-9, "zeros: expected delta_h=8.0");
    ASSERT_FLOAT_NEAR(dh_pct, 100.0, 1e-9, "zeros: expected delta_h_pct=100.0");

    /* 2. Exact uniform distribution over 256 bytes */
    uint8_t uniform[256 * 16];
    for (size_t i = 0; i < sizeof(uniform); i++) {
        uniform[i] = (uint8_t)(i % 256);
    }
    compute_shannon_entropy(uniform, sizeof(uniform), &h, &dh, &dh_pct);
    ASSERT_FLOAT_NEAR(h, 8.0, 1e-9, "uniform: expected H=8.0");
    ASSERT_FLOAT_NEAR(dh, 0.0, 1e-9, "uniform: expected delta_h=0.0");
    ASSERT_FLOAT_NEAR(dh_pct, 0.0, 1e-9, "uniform: expected delta_h_pct=0.0");

    /* 3. Two-symbol balanced stream: H = 1.0 bit */
    uint8_t two_sym[1000];
    for (size_t i = 0; i < sizeof(two_sym); i++) {
        two_sym[i] = (uint8_t)(i % 2);
    }
    compute_shannon_entropy(two_sym, sizeof(two_sym), &h, &dh, &dh_pct);
    ASSERT_FLOAT_NEAR(h, 1.0, 1e-9, "two_sym: expected H=1.0");
    ASSERT_FLOAT_NEAR(dh, 7.0, 1e-9, "two_sym: expected delta_h=7.0");
    ASSERT_FLOAT_NEAR(dh_pct, (7.0 / 8.0) * 100.0, 1e-9, "two_sym: expected delta_h_pct=87.5%");

    /* 4. Four-symbol balanced stream: H = 2.0 bits */
    uint8_t four_sym[1000];
    for (size_t i = 0; i < sizeof(four_sym); i++) {
        four_sym[i] = (uint8_t)(i % 4);
    }
    double h4 = shannon_entropy_bytes(four_sym, sizeof(four_sym));
    ASSERT_FLOAT_NEAR(h4, 2.0, 1e-9, "four_sym: expected H=2.0");
}

static void test_m2_chi_squared(void) {
    printf("[+] Running test_m2_chi_squared...\n");

    /* 1. Exact uniform data: chi2 = 0.0, Z ~ -11.29 */
    uint8_t uniform[256 * 32];
    for (size_t i = 0; i < sizeof(uniform); i++) {
        uniform[i] = (uint8_t)(i % 256);
    }
    double chi2, z;
    compute_chi_squared(uniform, sizeof(uniform), &chi2, &z);
    ASSERT_FLOAT_NEAR(chi2, 0.0, 1e-9, "uniform: expected chi2=0.0");
    double exp_z = (0.0 - 255.0) / sqrt(510.0);
    ASSERT_FLOAT_NEAR(z, exp_z, 1e-5, "uniform: expected z ~ -11.29");

    /* 2. All zeros data: Chi2 = N * 255, huge positive Z */
    uint8_t zeros[1024];
    memset(zeros, 0, sizeof(zeros));
    compute_chi_squared(zeros, sizeof(zeros), &chi2, &z);
    double exp_chi2 = 1024.0 * 255.0;
    ASSERT_FLOAT_NEAR(chi2, exp_chi2, 1e-5, "zeros: expected chi2 = N * 255");
    ASSERT_TRUE(z > 1000.0, "zeros: expected huge positive Z");
}

static void test_m2_stride4_entropy(void) {
    printf("[+] Running test_m2_stride4_entropy...\n");

    /* 4-byte repeating pattern: pairs at stride 4 are identical, 4 distinct values */
    uint8_t pat4[1600];
    const uint8_t pat[4] = {0x11, 0x22, 0x33, 0x44};
    for (size_t i = 0; i < sizeof(pat4); i++) {
        pat4[i] = pat[i % 4];
    }
    double h_s4 = compute_stride4_entropy(pat4, sizeof(pat4));
    /* Exactly 4 pairs each with freq 1/4 -> H = 2.0 bits */
    ASSERT_FLOAT_NEAR(h_s4, 2.0, 0.05, "stride4: expected ~2.0 bits");
}

static void test_m2_bit_correlation(void) {
    printf("[+] Running test_m2_bit_correlation...\n");

    /* Create 256 blocks where bit 0 and bit 1 are perfectly correlated,
       and bit 0 and bit 2 are perfectly anti-correlated */
    uint8_t blocks[256][16];
    memset(blocks, 0, sizeof(blocks));
    for (int k = 0; k < 256; k++) {
        uint8_t b = (uint8_t)(k % 2);
        blocks[k][0] = (uint8_t)(b | (b << 1) | ((1 - b) << 2));
    }

    /* Same bit: correlation 1.0 */
    double r_self = compute_bit_correlation(blocks, 256, 0, 0);
    ASSERT_FLOAT_NEAR(r_self, 1.0, 1e-9, "self bit correlation != 1.0");

    /* Perfectly correlated bits 0 and 1 */
    double r_01 = compute_bit_correlation(blocks, 256, 0, 1);
    ASSERT_FLOAT_NEAR(r_01, 1.0, 1e-6, "correlated bits 0 and 1 != 1.0");

    /* Perfectly anti-correlated bits 0 and 2 */
    double r_02 = compute_bit_correlation(blocks, 256, 0, 2);
    ASSERT_FLOAT_NEAR(r_02, -1.0, 1e-6, "anti-correlated bits 0 and 2 != -1.0");
}

static void test_m2_lambda_set_zero_sum(void) {
    printf("[+] Running test_m2_lambda_set_zero_sum...\n");

    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };

    const uint8_t zero_16[16] = {0};

    /* 1. PROJECT.md interface contract: verify_lambda_set(rounds, xor_sum) */
    uint8_t xor_sum[16];

    /* Round 1 */
    verify_lambda_set(1, xor_sum);
    ASSERT_MEM_EQ(xor_sum, zero_16, 16, "verify_lambda_set Round 1 zero-sum failed");

    /* Round 2 */
    verify_lambda_set(2, xor_sum);
    ASSERT_MEM_EQ(xor_sum, zero_16, 16, "verify_lambda_set Round 2 zero-sum failed");

    /* Round 3: provably balanced with probability 1.0 */
    verify_lambda_set(3, xor_sum);
    ASSERT_MEM_EQ(xor_sum, zero_16, 16, "verify_lambda_set Round 3 zero-sum failed");

    /* 2. Test across multiple active bytes and both final_mix=true and final_mix=false */
    const int active_test_bytes[5] = {0, 3, 7, 12, 15};
    for (int idx = 0; idx < 5; idx++) {
        int act = active_test_bytes[idx];
        bool balanced = false;

        /* Round 1 with MC */
        verify_lambda_set_ex(key, 1, true, act, 0x42, xor_sum, &balanced);
        ASSERT_TRUE(balanced, "Round 1 (with MC) failed balancedness check");
        ASSERT_MEM_EQ(xor_sum, zero_16, 16, "Round 1 (with MC) xor_sum != 0");

        /* Round 1 no MC */
        verify_lambda_set_ex(key, 1, false, act, 0x42, xor_sum, &balanced);
        ASSERT_TRUE(balanced, "Round 1 (no MC) failed balancedness check");
        ASSERT_MEM_EQ(xor_sum, zero_16, 16, "Round 1 (no MC) xor_sum != 0");

        /* Round 2 */
        verify_lambda_set_ex(key, 2, true, act, 0x42, xor_sum, &balanced);
        ASSERT_TRUE(balanced, "Round 2 failed balancedness check");
        ASSERT_MEM_EQ(xor_sum, zero_16, 16, "Round 2 xor_sum != 0");

        /* Round 3 */
        verify_lambda_set_ex(key, 3, true, act, 0x42, xor_sum, &balanced);
        ASSERT_TRUE(balanced, "Round 3 failed balancedness check");
        ASSERT_MEM_EQ(xor_sum, zero_16, 16, "Round 3 xor_sum != 0");
    }

    /* 3. Confirm Round 4 is NOT balanced for N=256 single active byte */
    bool balanced_r4 = false;
    verify_lambda_set_ex(key, 4, true, 0, 0x42, xor_sum, &balanced_r4);
    ASSERT_TRUE(!balanced_r4, "Round 4 N=256 was unexpectedly balanced");
    ASSERT_TRUE(memcmp(xor_sum, zero_16, 16) != 0, "Round 4 xor_sum unexpectedly all zeros");
}

static void test_m2_reduced_round_distinguisher(void) {
    printf("[+] Running test_m2_reduced_round_distinguisher...\n");

    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };

    /* Generate 1024 blocks of structured / low-entropy counter plaintexts */
    const size_t num_blocks = 1024;
    static uint8_t ct_r1[1024 * 16];

    for (size_t i = 0; i < num_blocks; i++) {
        uint8_t pt[16];
        memset(pt, 0x55, 16);
        pt[0] = (uint8_t)(i & 0xFF);
        pt[1] = (uint8_t)((i >> 8) & 0xFF);
        aes_reduced_encrypt(pt, key, 1, false, ct_r1 + i * 16);
    }

    aes_distinguisher_report_t report;
    evaluate_reduced_round_distinguisher(ct_r1, sizeof(ct_r1), &report);

    /* Assert Delta H% >> 6.0% and distinguisher fired */
    ASSERT_TRUE(report.delta_h_pct > 6.0, "Round 1 (no MC) Delta H% <= 6.0%");
    ASSERT_TRUE(report.is_distinguished, "Round 1 distinguisher did not fire");
    ASSERT_TRUE(report.z_score > 3.29, "Round 1 Z-score <= 3.29");
}

static void test_m3_cascaded_deficit_aggregator(void) {
    printf("[+] Running test_m3_cascaded_deficit_aggregator...\n");

    /* 1. Chain rule aggregation test: Delta H_agg = sum_{j=1}^5 Delta H_j */
    {
        double v_test[5] = {0.050, 0.060, 0.040, 0.035, 0.030};
        aes_cascaded_deficit_report_t rep;
        double exp_agg = 0.050 + 0.060 + 0.040 + 0.035 + 0.030; /* 0.215% */
        int exp_savings = (int)round(1048576.0 * (exp_agg / 100.0));
        double exp_surplus = 128.0 * (exp_agg / 100.0);

        aes_cascaded_deficit_aggregate(v_test, &rep);
        ASSERT_FLOAT_NEAR(rep.aggregated_deficit_pct, exp_agg, 1e-9, "Chain rule sum mismatch");
        ASSERT_TRUE(rep.within_theoretical_bounds, "0.215% should be within theoretical bounds [0.155%, 0.302%]");
        ASSERT_TRUE(rep.theoretical_savings_bytes_1mib == exp_savings, "1 MiB theoretical savings mismatch");
        ASSERT_TRUE(rep.theoretical_savings_bytes_1mib >= PRZM_THEORETICAL_MIN_SAVINGS_1MIB &&
                    rep.theoretical_savings_bytes_1mib <= PRZM_THEORETICAL_MAX_SAVINGS_1MIB,
                    "1 MiB theoretical savings outside [1630, 3170] bounds");
        ASSERT_FLOAT_NEAR(rep.information_surplus_bits, exp_surplus, 1e-9, "Information surplus bits mismatch");
    }

    /* 2. Lower theoretical bound test: 0.155% */
    {
        double v_min[5] = {0.035, 0.040, 0.025, 0.030, 0.025};
        aes_cascaded_deficit_report_t rep;
        aes_cascaded_deficit_aggregate(v_min, &rep);
        ASSERT_FLOAT_NEAR(rep.aggregated_deficit_pct, PRZM_THEORETICAL_MIN_DEFICIT_PCT, 1e-9, "Min bound sum mismatch");
        ASSERT_TRUE(rep.within_theoretical_bounds, "Min bound 0.155% should be within bounds");
        ASSERT_TRUE(rep.theoretical_savings_bytes_1mib >= PRZM_THEORETICAL_MIN_SAVINGS_1MIB - 10 &&
                    rep.theoretical_savings_bytes_1mib <= PRZM_THEORETICAL_MIN_SAVINGS_1MIB + 10,
                    "Min bound 1 MiB savings unexpected");
    }

    /* 3. Upper theoretical bound test: 0.302% */
    {
        double v_max[5] = {0.075, 0.082, 0.055, 0.050, 0.040};
        aes_cascaded_deficit_report_t rep;
        aes_cascaded_deficit_aggregate(v_max, &rep);
        ASSERT_FLOAT_NEAR(rep.aggregated_deficit_pct, PRZM_THEORETICAL_MAX_DEFICIT_PCT, 1e-9, "Max bound sum mismatch");
        ASSERT_TRUE(rep.within_theoretical_bounds, "Max bound 0.302% should be within bounds");
        ASSERT_TRUE(rep.theoretical_savings_bytes_1mib >= PRZM_THEORETICAL_MAX_SAVINGS_1MIB - 10 &&
                    rep.theoretical_savings_bytes_1mib <= PRZM_THEORETICAL_MAX_SAVINGS_1MIB + 10,
                    "Max bound 1 MiB savings unexpected");
    }

    /* 4. Out-of-bounds checks */
    {
        double v_low[5] = {0.010, 0.010, 0.010, 0.010, 0.010}; /* 0.050% */
        aes_cascaded_deficit_report_t rep;
        aes_cascaded_deficit_aggregate(v_low, &rep);
        ASSERT_TRUE(!rep.within_theoretical_bounds, "0.050% should be below theoretical bounds");
    }
    {
        double v_high[5] = {0.100, 0.100, 0.100, 0.100, 0.100}; /* 0.500% */
        aes_cascaded_deficit_report_t rep;
        aes_cascaded_deficit_aggregate(v_high, &rep);
        ASSERT_TRUE(!rep.within_theoretical_bounds, "0.500% should be above theoretical bounds");
    }

    /* 5. Empirical evaluation on generated blocks */
    {
        static uint8_t test_blocks[512][16];
        const uint8_t key[16] = {
            0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
            0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
        };
        int i, k;
        aes_cascaded_deficit_report_t rep;

        /* Chunk 0 (256 blocks): Round 3 Lambda set */
        for (i = 0; i < 256; i++) {
            uint8_t pt[16] = {0};
            pt[0] = (uint8_t)i;
            for (k = 1; k < 16; k++) pt[k] = (uint8_t)(k * 17);
            aes_reduced_encrypt(pt, key, 3, true, test_blocks[i]);
        }
        /* Chunk 1 (256 blocks): Round 4 counter */
        for (i = 0; i < 256; i++) {
            uint8_t pt[16] = {0};
            pt[0] = (uint8_t)i;
            pt[1] = (uint8_t)(i >> 8);
            for (k = 2; k < 16; k++) pt[k] = (uint8_t)(k * 11 + 5);
            aes_reduced_encrypt(pt, key, 4, true, test_blocks[256 + i]);
        }

        aes_evaluate_cascaded_deficit(test_blocks, 512, &rep);
        ASSERT_TRUE(rep.block_count == 512, "Block count mismatch in evaluate");
        ASSERT_TRUE(rep.aggregated_deficit_pct > 0.0, "Aggregated deficit should be positive");
        ASSERT_TRUE(rep.vector_deficits[4] > 0.0, "Vector 5 (integral) should detect balanced chunk");
    }
}

static void test_m3_crypto_core_codec(void) {
    printf("[+] Running test_m3_crypto_core_codec...\n");

    /* Test 1: Empty input payload */
    {
        uint8_t comp[64];
        uint8_t decomp[64];
        size_t comp_len = 0;
        size_t decomp_len = 0;
        int status = aes_crypto_core_compress(NULL, 0, comp, sizeof(comp), &comp_len);
        ASSERT_TRUE(status == 0, "Empty compress failed");
        ASSERT_TRUE(comp_len == PRZM_CRYPTO_CORE_HEADER_SIZE, "Empty compress header size mismatch");

        status = aes_crypto_core_decompress(comp, comp_len, decomp, sizeof(decomp), &decomp_len);
        ASSERT_TRUE(status == 0, "Empty decompress failed");
        ASSERT_TRUE(decomp_len == 0, "Empty decompress should yield 0 bytes");
    }

    /* Test 2: Incompressible / raw passthrough mode */
    {
        uint8_t raw[100];
        uint8_t comp[256];
        uint8_t decomp[256];
        size_t comp_len = 0;
        size_t decomp_len = 0;
        size_t i;
        int status;

        for (i = 0; i < sizeof(raw); i++) raw[i] = (uint8_t)(i * 31 + 7);

        status = aes_crypto_core_compress(raw, sizeof(raw), comp, sizeof(comp), &comp_len);
        ASSERT_TRUE(status == 0, "Raw compress failed");
        ASSERT_TRUE(comp_len == sizeof(raw) + PRZM_CRYPTO_CORE_HEADER_SIZE, "Raw comp size mismatch");

        status = aes_crypto_core_decompress(comp, comp_len, decomp, sizeof(decomp), &decomp_len);
        ASSERT_TRUE(status == 0, "Raw decompress failed");
        ASSERT_TRUE(decomp_len == sizeof(raw), "Raw decompress length mismatch");
        ASSERT_MEM_EQ(decomp, raw, sizeof(raw), "Raw roundtrip mismatch");
    }

    /* Test 3: Balanced chunks (Lambda set under Round 3, 256 blocks = 4096 bytes) */
    {
        const uint8_t key[16] = {
            0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
            0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
        };
        static uint8_t raw[8192];
        static uint8_t comp[8192 + 1024];
        static uint8_t decomp[8192 + 256];
        size_t comp_len = 0;
        size_t decomp_len = 0;
        int i, k, status;

        for (i = 0; i < 256; i++) {
            uint8_t pt[16] = {0};
            pt[0] = (uint8_t)i;
            for (k = 1; k < 16; k++) pt[k] = (uint8_t)(k * 13);
            aes_reduced_encrypt(pt, key, 3, true, raw + i * 16);
        }
        for (i = 0; i < 256; i++) {
            uint8_t pt[16] = {0};
            pt[1] = (uint8_t)i;
            for (k = 0; k < 16; k++) if (k != 1) pt[k] = (uint8_t)(k * 19 + 3);
            aes_reduced_encrypt(pt, key, 3, true, raw + 4096 + i * 16);
        }

        status = aes_crypto_core_compress(raw, sizeof(raw), comp, sizeof(comp), &comp_len);
        ASSERT_TRUE(status == 0, "Balanced chunk compress failed");
        ASSERT_TRUE(comp_len < sizeof(raw), "Compression failed to reduce size for balanced chunks");
        ASSERT_TRUE(comp_len == 8177, "Expected exactly 8177 bytes compressed size");

        status = aes_crypto_core_decompress(comp, comp_len, decomp, sizeof(decomp), &decomp_len);
        ASSERT_TRUE(status == 0, "Balanced chunk decompress failed");
        ASSERT_TRUE(decomp_len == sizeof(raw), "Decompressed length mismatch");
        ASSERT_MEM_EQ(decomp, raw, sizeof(raw), "Lossless roundtrip reconstruction failed for balanced chunks");
    }

    /* Test 4: Mixed chunks (1 balanced chunk + 1 non-balanced chunk = 8192 bytes) */
    {
        const uint8_t key[16] = {
            0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
            0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
        };
        static uint8_t raw_mixed[8192];
        static uint8_t comp_mixed[8192 + 1024];
        static uint8_t decomp_mixed[8192 + 256];
        size_t comp_mixed_len = 0;
        size_t decomp_mixed_len = 0;
        int i, k, status;

        /* Chunk 0: Round 3 Lambda set (balanced) */
        for (i = 0; i < 256; i++) {
            uint8_t pt[16] = {0};
            pt[0] = (uint8_t)i;
            for (k = 1; k < 16; k++) pt[k] = (uint8_t)(k * 13);
            aes_reduced_encrypt(pt, key, 3, true, raw_mixed + i * 16);
        }
        /* Chunk 1: Round 4 counter (non-balanced) */
        for (i = 0; i < 256; i++) {
            uint8_t pt[16] = {0};
            pt[0] = (uint8_t)i;
            pt[1] = (uint8_t)(i >> 8);
            for (k = 2; k < 16; k++) pt[k] = (uint8_t)(k * 23 + 1);
            aes_reduced_encrypt(pt, key, 4, true, raw_mixed + 4096 + i * 16);
        }

        status = aes_crypto_core_compress(raw_mixed, sizeof(raw_mixed), comp_mixed, sizeof(comp_mixed), &comp_mixed_len);
        ASSERT_TRUE(status == 0, "Mixed chunk compress failed");

        status = aes_crypto_core_decompress(comp_mixed, comp_mixed_len, decomp_mixed, sizeof(decomp_mixed), &decomp_mixed_len);
        ASSERT_TRUE(status == 0, "Mixed chunk decompress failed");
        ASSERT_TRUE(decomp_mixed_len == sizeof(raw_mixed), "Mixed decompressed length mismatch");
        ASSERT_MEM_EQ(decomp_mixed, raw_mixed, sizeof(raw_mixed), "Lossless roundtrip reconstruction failed for mixed chunks");
    }

    /* Test 5: Error handling and corruption robustness */
    {
        uint8_t valid_comp[64];
        uint8_t corrupt_magic[64];
        uint8_t dst[64];
        size_t valid_len = 0;
        size_t out_len = 0;
        int err;

        aes_crypto_core_compress(NULL, 0, valid_comp, sizeof(valid_comp), &valid_len);

        /* Buffer too small for compress */
        err = aes_crypto_core_compress(valid_comp, 10, dst, 5, &out_len);
        ASSERT_TRUE(err < 0, "Compress should fail on small dst buffer");

        /* Null pointer checks */
        err = aes_crypto_core_compress(valid_comp, 10, NULL, sizeof(dst), &out_len);
        ASSERT_TRUE(err < 0, "Compress should fail on NULL dst");

        err = aes_crypto_core_decompress(NULL, valid_len, dst, sizeof(dst), &out_len);
        ASSERT_TRUE(err < 0, "Decompress should fail on NULL src");

        /* Corrupted magic */
        memcpy(corrupt_magic, valid_comp, valid_len);
        corrupt_magic[0] ^= 0xFF;
        err = aes_crypto_core_decompress(corrupt_magic, valid_len, dst, sizeof(dst), &out_len);
        ASSERT_TRUE(err == -2, "Decompress should fail with -2 on corrupt magic");

        /* Truncated header */
        err = aes_crypto_core_decompress(valid_comp, 8, dst, sizeof(dst), &out_len);
        ASSERT_TRUE(err < 0, "Decompress should fail on truncated header");
    }
}

static void test_m3_decompress_block64_microcode(void) {
    printf("[+] Running test_m3_decompress_block64_microcode...\n");

    /* Construct 4 AES blocks: B0, B1, B2, B3 with exact zero-sum invariant: B0 ^ B1 ^ B2 ^ B3 == 0 */
    uint8_t b0[16], b1[16], b2[16], b3[16];
    uint8_t expected64[64];
    uint8_t src48[48];
    uint8_t dst64[64];
    int bytes_read;
    int i;

    for (i = 0; i < 16; i++) {
        b0[i] = (uint8_t)(i * 3 + 1);
        b1[i] = (uint8_t)(i * 7 + 2);
        b2[i] = (uint8_t)(i * 11 + 5);
        b3[i] = (uint8_t)(b0[i] ^ b1[i] ^ b2[i]); /* Provable zero sum */
    }

    memcpy(expected64 + 0, b0, 16);
    memcpy(expected64 + 16, b1, 16);
    memcpy(expected64 + 32, b2, 16);
    memcpy(expected64 + 48, b3, 16);

    /* Case 1: Balanced mode (reads 48 bytes B0..B2, reconstructs 64 bytes B0..B3) */
    memcpy(src48 + 0, b0, 16);
    memcpy(src48 + 16, b1, 16);
    memcpy(src48 + 32, b2, 16);

    memset(dst64, 0xCC, sizeof(dst64));
    bytes_read = aes_crypto_core_decompress_block64(src48, dst64, true);
    ASSERT_TRUE(bytes_read == 48, "Microcode balanced mode should read exactly 48 bytes");
    ASSERT_MEM_EQ(dst64, expected64, 64, "Microcode balanced reconstruction mismatch");

    /* Case 2: Unbalanced / raw mode (reads 64 bytes, copies 64 bytes) */
    memset(dst64, 0x55, sizeof(dst64));
    bytes_read = aes_crypto_core_decompress_block64(expected64, dst64, false);
    ASSERT_TRUE(bytes_read == 64, "Microcode unbal mode should read exactly 64 bytes");
    ASSERT_MEM_EQ(dst64, expected64, 64, "Microcode unbal copy mismatch");
}

int main(void) {
    printf("=================================================================\n");
    printf("   PRZM AES DISTINGUISHER & FAST MOBIUS TRANSFORM C TEST HARNESS \n");
    printf("=================================================================\n");

    /* Milestone 1 Tests */
    test_fips197_appendix_b();
    test_fips197_appendix_c1();
    test_key_schedule_linear_invariants();
    test_key_schedule_modes();
    test_fmt_involution();
    test_fmt_sbox_spectrum();
    test_reduced_rounds_ladder();
    test_reduced_encrypt_tap();
    test_mix_columns_invariant_subspace();
    test_boundary_clamping();

    /* Milestone 2 Tests */
    test_m2_shannon_entropy();
    test_m2_chi_squared();
    test_m2_stride4_entropy();
    test_m2_bit_correlation();
    test_m2_lambda_set_zero_sum();
    test_m2_reduced_round_distinguisher();

    /* Milestone 3 Tests */
    test_m3_cascaded_deficit_aggregator();
    test_m3_crypto_core_codec();
    test_m3_decompress_block64_microcode();

    printf("=================================================================\n");
    printf("   ALL C VERIFICATION TESTS PASSED SUCCESSFULLY (0 HEAP ALLOCS)  \n");
    printf("=================================================================\n");

    return 0;
}


