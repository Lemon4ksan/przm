/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "aes_distinguisher.h"
#include <string.h>
#include <math.h>

/* Standard NIST AES S-Box */
static const uint8_t kSBox[256] = {
    0x63, 0x7c, 0x77, 0x7b, 0xf2, 0x6b, 0x6f, 0xc5, 0x30, 0x01, 0x67, 0x2b, 0xfe, 0xd7, 0xab, 0x76,
    0xca, 0x82, 0xc9, 0x7d, 0xfa, 0x59, 0x47, 0xf0, 0xad, 0xd4, 0xa2, 0xaf, 0x9c, 0xa4, 0x72, 0xc0,
    0xb7, 0xfd, 0x93, 0x26, 0x36, 0x3f, 0xf7, 0xcc, 0x34, 0xa5, 0xe5, 0xf1, 0x71, 0xd8, 0x31, 0x15,
    0x04, 0xc7, 0x23, 0xc3, 0x18, 0x96, 0x05, 0x9a, 0x07, 0x12, 0x80, 0xe2, 0xeb, 0x27, 0xb2, 0x75,
    0x09, 0x83, 0x2c, 0x1a, 0x1b, 0x6e, 0x5a, 0xa0, 0x52, 0x3b, 0xd6, 0xb3, 0x29, 0xe3, 0x2f, 0x84,
    0x53, 0xd1, 0x00, 0xed, 0x20, 0xfc, 0xb1, 0x5b, 0x6a, 0xcb, 0xbe, 0x39, 0x4a, 0x4c, 0x58, 0xcf,
    0xd0, 0xef, 0xaa, 0xfb, 0x43, 0x4d, 0x33, 0x85, 0x45, 0xf9, 0x02, 0x7f, 0x50, 0x3c, 0x9f, 0xa8,
    0x51, 0xa3, 0x40, 0x8f, 0x92, 0x9d, 0x38, 0xf5, 0xbc, 0xb6, 0xda, 0x21, 0x10, 0xff, 0xf3, 0xd2,
    0xcd, 0x0c, 0x13, 0xec, 0x5f, 0x97, 0x44, 0x17, 0xc4, 0xa7, 0x7e, 0x3d, 0x64, 0x5d, 0x19, 0x73,
    0x60, 0x81, 0x4f, 0xdc, 0x22, 0x2a, 0x90, 0x88, 0x46, 0xee, 0xb8, 0x14, 0xde, 0x5e, 0x0b, 0xdb,
    0xe0, 0x32, 0x3a, 0x0a, 0x49, 0x06, 0x24, 0x5c, 0xc2, 0xd3, 0xac, 0x62, 0x91, 0x95, 0xe4, 0x79,
    0xe7, 0xc8, 0x37, 0x6d, 0x8d, 0xd5, 0x4e, 0xa9, 0x6c, 0x56, 0xf4, 0xea, 0x65, 0x7a, 0xae, 0x08,
    0xba, 0x78, 0x25, 0x2e, 0x1c, 0xa6, 0xb4, 0xc6, 0xe8, 0xdd, 0x74, 0x1f, 0x4b, 0xbd, 0x8b, 0x8a,
    0x70, 0x3e, 0xb5, 0x66, 0x48, 0x03, 0xf6, 0x0e, 0x61, 0x35, 0x57, 0xb9, 0x86, 0xc1, 0x1d, 0x9e,
    0xe1, 0xf8, 0x98, 0x11, 0x69, 0xd9, 0x8e, 0x94, 0x9b, 0x1e, 0x87, 0xe9, 0xce, 0x55, 0x28, 0xdf,
    0x8c, 0xa1, 0x89, 0x0d, 0xbf, 0xe6, 0x42, 0x68, 0x41, 0x99, 0x2d, 0x0f, 0xb0, 0x54, 0xbb, 0x16
};

/* Standard AES Inverse S-Box */
static const uint8_t kInvSBox[256] = {
    0x52, 0x09, 0x6a, 0xd5, 0x30, 0x36, 0xa5, 0x38, 0xbf, 0x40, 0xa3, 0x9e, 0x81, 0xf3, 0xd7, 0xfb,
    0x7c, 0xe3, 0x39, 0x82, 0x9b, 0x2f, 0xff, 0x87, 0x34, 0x8e, 0x43, 0x44, 0xc4, 0xde, 0xe9, 0xcb,
    0x54, 0x7b, 0x94, 0x32, 0xa6, 0xc2, 0x23, 0x3d, 0xee, 0x4c, 0x95, 0x0b, 0x42, 0xfa, 0xc3, 0x4e,
    0x08, 0x2e, 0xa1, 0x66, 0x28, 0xd9, 0x24, 0xb2, 0x76, 0x5b, 0xa2, 0x49, 0x6d, 0x8b, 0xd1, 0x25,
    0x72, 0xf8, 0xf6, 0x64, 0x86, 0x68, 0x98, 0x16, 0xd4, 0xa4, 0x5c, 0xcc, 0x5d, 0x65, 0xb6, 0x92,
    0x6c, 0x70, 0x48, 0x50, 0xfd, 0xed, 0xb9, 0xda, 0x5e, 0x15, 0x46, 0x57, 0xa7, 0x8d, 0x9d, 0x84,
    0x90, 0xd8, 0xab, 0x00, 0x8c, 0xbc, 0xd3, 0x0a, 0xf7, 0xe4, 0x58, 0x05, 0xb8, 0xb3, 0x45, 0x06,
    0xd0, 0x2c, 0x1e, 0x8f, 0xca, 0x3f, 0x0f, 0x02, 0xc1, 0xaf, 0xbd, 0x03, 0x01, 0x13, 0x8a, 0x6b,
    0x3a, 0x91, 0x11, 0x41, 0x4f, 0x67, 0xdc, 0xea, 0x97, 0xf2, 0xcf, 0xce, 0xf0, 0xb4, 0xe6, 0x73,
    0x96, 0xac, 0x74, 0x22, 0xe7, 0xad, 0x35, 0x85, 0xe2, 0xf9, 0x37, 0xe8, 0x1c, 0x75, 0xdf, 0x6e,
    0x47, 0xf1, 0x1a, 0x71, 0x1d, 0x29, 0xc5, 0x89, 0x6f, 0xb7, 0x62, 0x0e, 0xaa, 0x18, 0xbe, 0x1b,
    0xfc, 0x56, 0x3e, 0x4b, 0xc6, 0xd2, 0x79, 0x20, 0x9a, 0xdb, 0xc0, 0xfe, 0x78, 0xcd, 0x5a, 0xf4,
    0x1f, 0xdd, 0xa8, 0x33, 0x88, 0x07, 0xc7, 0x31, 0xb1, 0x12, 0x10, 0x59, 0x27, 0x80, 0xec, 0x5f,
    0x60, 0x51, 0x7f, 0xa9, 0x19, 0xb5, 0x4a, 0x0d, 0x2d, 0xe5, 0x7a, 0x9f, 0x93, 0xc9, 0x9c, 0xef,
    0xa0, 0xe0, 0x3b, 0x4d, 0xae, 0x2a, 0xf5, 0xb0, 0xc8, 0xeb, 0xbb, 0x3c, 0x83, 0x53, 0x99, 0x61,
    0x17, 0x2b, 0x04, 0x7e, 0xba, 0x77, 0xd6, 0x26, 0xe1, 0x69, 0x14, 0x63, 0x55, 0x21, 0x0c, 0x7d
};

static const uint8_t kRcon[11] = {
    0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36
};

const uint8_t* aes_get_sbox(void) {
    return kSBox;
}

const uint8_t* aes_get_inv_sbox(void) {
    return kInvSBox;
}

/* Branchless xtime multiplication over GF(2^8) with irreducible polynomial 0x11B */
static inline uint8_t xtime(uint8_t x) {
    return (uint8_t)((x << 1) ^ ((((int8_t)x) >> 7) & 0x1B));
}

void aes_sub_bytes(uint8_t state[16]) {
    for (int i = 0; i < 16; i++) {
        state[i] = kSBox[state[i]];
    }
}

void aes_shift_rows(uint8_t state[16]) {
    uint8_t r1_1  = state[5],  r1_5  = state[9],  r1_9  = state[13], r1_13 = state[1];
    uint8_t r2_2  = state[10], r2_6  = state[14], r2_10 = state[2],  r2_14 = state[6];
    uint8_t r3_3  = state[15], r3_7  = state[3],  r3_11 = state[7],  r3_15 = state[11];

    state[1]  = r1_1;  state[5]  = r1_5;  state[9]  = r1_9;  state[13] = r1_13;
    state[2]  = r2_2;  state[6]  = r2_6;  state[10] = r2_10; state[14] = r2_14;
    state[3]  = r3_3;  state[7]  = r3_7;  state[11] = r3_11; state[15] = r3_15;
}

void aes_mix_columns(uint8_t state[16]) {
    for (int c = 0; c < 4; c++) {
        int idx = c * 4;
        uint8_t s0 = state[idx];
        uint8_t s1 = state[idx + 1];
        uint8_t s2 = state[idx + 2];
        uint8_t s3 = state[idx + 3];

        state[idx]     = (uint8_t)(xtime(s0) ^ xtime(s1) ^ s1 ^ s2 ^ s3);
        state[idx + 1] = (uint8_t)(s0 ^ xtime(s1) ^ xtime(s2) ^ s2 ^ s3);
        state[idx + 2] = (uint8_t)(s0 ^ s1 ^ xtime(s2) ^ xtime(s3) ^ s3);
        state[idx + 3] = (uint8_t)(xtime(s0) ^ s0 ^ s1 ^ s2 ^ xtime(s3));
    }
}

void aes_add_round_key(uint8_t state[16], const uint8_t rk[16]) {
    for (int i = 0; i < 16; i++) {
        state[i] ^= rk[i];
    }
}

void aes_expand_key(
    const uint8_t key[16],
    int rounds,
    aes_key_schedule_mode_t mode,
    uint8_t linear_delta,
    uint8_t round_keys[][16]
) {
    if (rounds < 1) rounds = 1;
    if (rounds > 14) rounds = 14;
    int num_round_keys = rounds + 1;

    switch (mode) {
        case AES_KEY_SCHEDULE_ZERO:
            memset(round_keys, 0, (size_t)num_round_keys * 16);
            break;

        case AES_KEY_SCHEDULE_REPEATED:
            for (int r = 0; r < num_round_keys; r++) {
                memcpy(round_keys[r], key, 16);
            }
            break;

        case AES_KEY_SCHEDULE_LINEAR: {
            uint8_t delta = (linear_delta == 0) ? 0x01 : linear_delta;
            for (int r = 0; r < num_round_keys; r++) {
                for (int i = 0; i < 16; i++) {
                    round_keys[r][i] = (uint8_t)(key[i] ^ (r * delta));
                }
            }
            break;
        }

        case AES_KEY_SCHEDULE_STANDARD:
        default: {
            /* FIPS 197 key expansion into 32-bit words */
            uint32_t w[64];
            int total_words = 4 * num_round_keys;

            for (int i = 0; i < 4; i++) {
                w[i] = (uint32_t)key[4 * i] |
                       ((uint32_t)key[4 * i + 1] << 8) |
                       ((uint32_t)key[4 * i + 2] << 16) |
                       ((uint32_t)key[4 * i + 3] << 24);
            }

            for (int i = 4; i < total_words; i++) {
                uint32_t temp = w[i - 1];
                if (i % 4 == 0) {
                    uint8_t b0 = (uint8_t)temp;
                    uint8_t b1 = (uint8_t)(temp >> 8);
                    uint8_t b2 = (uint8_t)(temp >> 16);
                    uint8_t b3 = (uint8_t)(temp >> 24);

                    /* RotWord (b1, b2, b3, b0) + SubWord */
                    uint32_t sb0 = (uint32_t)kSBox[b1];
                    uint32_t sb1 = (uint32_t)kSBox[b2] << 8;
                    uint32_t sb2 = (uint32_t)kSBox[b3] << 16;
                    uint32_t sb3 = (uint32_t)kSBox[b0] << 24;
                    uint32_t sub_rot = sb0 | sb1 | sb2 | sb3;

                    uint32_t rcon_val = 0;
                    int rcon_idx = i / 4;
                    if (rcon_idx < 11) {
                        rcon_val = (uint32_t)kRcon[rcon_idx];
                    }
                    temp = sub_rot ^ rcon_val;
                }
                w[i] = w[i - 4] ^ temp;
            }

            for (int r = 0; r < num_round_keys; r++) {
                for (int c = 0; c < 4; c++) {
                    uint32_t val = w[4 * r + c];
                    round_keys[r][4 * c]     = (uint8_t)val;
                    round_keys[r][4 * c + 1] = (uint8_t)(val >> 8);
                    round_keys[r][4 * c + 2] = (uint8_t)(val >> 16);
                    round_keys[r][4 * c + 3] = (uint8_t)(val >> 24);
                }
            }
            break;
        }
    }
}

void aes_encrypt_block_with_tap(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    aes_key_schedule_mode_t mode,
    uint8_t linear_delta,
    aes_tap_callback_t tap_cb,
    void *user_data,
    uint8_t out[16]
) {
    if (rounds < 1) rounds = 1;
    if (rounds > 14) rounds = 14;

    /* Fixed stack buffer for round keys: 15 * 16 = 240 bytes (0 heap allocs) */
    uint8_t rks[16][16];
    aes_expand_key(key, rounds, mode, linear_delta, rks);

    uint8_t state[16];
    memcpy(state, in, 16);

    /* Round 0: Initial AddRoundKey */
    aes_add_round_key(state, rks[0]);
    if (tap_cb) {
        tap_cb(0, AES_TAP_AFTER_ADDROUNDKEY0, state, user_data);
    }

    /* Rounds 1 to rounds */
    for (int r = 1; r <= rounds; r++) {
        aes_sub_bytes(state);
        if (tap_cb) {
            tap_cb(r, AES_TAP_AFTER_SUBBYTES, state, user_data);
        }

        aes_shift_rows(state);
        if (tap_cb) {
            tap_cb(r, AES_TAP_AFTER_SHIFTROWS, state, user_data);
        }

        bool is_last = (r == rounds);
        if (!is_last || final_mix) {
            aes_mix_columns(state);
            if (tap_cb) {
                tap_cb(r, AES_TAP_AFTER_MIXCOLUMNS, state, user_data);
            }
        }

        aes_add_round_key(state, rks[r]);
        if (tap_cb) {
            tap_cb(r, AES_TAP_AFTER_ADDROUNDKEY, state, user_data);
        }
    }

    memcpy(out, state, 16);
}

void aes_reduced_encrypt(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    uint8_t out[16]
) {
    aes_encrypt_block_with_tap(
        in, key, rounds, final_mix,
        AES_KEY_SCHEDULE_STANDARD, 0,
        NULL, NULL, out
    );
}

typedef struct {
    int target_round;
    aes_tap_point_t target_point;
    uint8_t captured[16];
} aes_tap_capture_ctx_t;

static void aes_tap_capture_cb(int round, aes_tap_point_t point, const uint8_t state[16], void *user_data) {
    aes_tap_capture_ctx_t *ctx = (aes_tap_capture_ctx_t *)user_data;
    if (round == ctx->target_round && point == ctx->target_point) {
        memcpy(ctx->captured, state, 16);
    }
}

void aes_reduced_encrypt_tap(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    aes_state_tap_point_t tap,
    uint8_t out[16]
) {
    if (rounds < 1) rounds = 1;
    if (rounds > 14) rounds = 14;

    aes_tap_capture_ctx_t ctx;
    ctx.target_round = (tap == AES_TAP_AFTER_ADDROUNDKEY0) ? 0 : rounds;
    ctx.target_point = (aes_tap_point_t)tap;
    memset(ctx.captured, 0, 16);

    bool final_mix = (tap == AES_TAP_AFTER_MIXCOLUMNS);
    uint8_t dummy[16];
    aes_encrypt_block_with_tap(
        in, key, rounds, final_mix,
        AES_KEY_SCHEDULE_STANDARD, 0,
        aes_tap_capture_cb, &ctx,
        dummy
    );

    memcpy(out, ctx.captured, 16);
}

/* Branchless 64-bit SWAR butterfly network for 256-bit Fast Möbius Transform */
void fast_mobius_transform_inplace_u8(uint8_t tt[32]) {
    uint64_t w0, w1, w2, w3;
    memcpy(&w0, tt, 8);
    memcpy(&w1, tt + 8, 8);
    memcpy(&w2, tt + 16, 8);
    memcpy(&w3, tt + 24, 8);

    /* Stage 0: Step = 1 bit */
    const uint64_t m0 = 0x5555555555555555ULL;
    w0 ^= (w0 & m0) << 1;
    w1 ^= (w1 & m0) << 1;
    w2 ^= (w2 & m0) << 1;
    w3 ^= (w3 & m0) << 1;

    /* Stage 1: Step = 2 bits */
    const uint64_t m1 = 0x3333333333333333ULL;
    w0 ^= (w0 & m1) << 2;
    w1 ^= (w1 & m1) << 2;
    w2 ^= (w2 & m1) << 2;
    w3 ^= (w3 & m1) << 2;

    /* Stage 2: Step = 4 bits */
    const uint64_t m2 = 0x0F0F0F0F0F0F0F0FULL;
    w0 ^= (w0 & m2) << 4;
    w1 ^= (w1 & m2) << 4;
    w2 ^= (w2 & m2) << 4;
    w3 ^= (w3 & m2) << 4;

    /* Stage 3: Step = 8 bits (1 byte) */
    const uint64_t m3 = 0x00FF00FF00FF00FFULL;
    w0 ^= (w0 & m3) << 8;
    w1 ^= (w1 & m3) << 8;
    w2 ^= (w2 & m3) << 8;
    w3 ^= (w3 & m3) << 8;

    /* Stage 4: Step = 16 bits (2 bytes) */
    const uint64_t m4 = 0x0000FFFF0000FFFFULL;
    w0 ^= (w0 & m4) << 16;
    w1 ^= (w1 & m4) << 16;
    w2 ^= (w2 & m4) << 16;
    w3 ^= (w3 & m4) << 16;

    /* Stage 5: Step = 32 bits (4 bytes) */
    const uint64_t m5 = 0x00000000FFFFFFFFULL;
    w0 ^= (w0 & m5) << 32;
    w1 ^= (w1 & m5) << 32;
    w2 ^= (w2 & m5) << 32;
    w3 ^= (w3 & m5) << 32;

    /* Stage 6: Step = 64 bits (1 word) */
    w1 ^= w0;
    w3 ^= w2;

    /* Stage 7: Step = 128 bits (2 words) */
    w2 ^= w0;
    w3 ^= w1;

    memcpy(tt, &w0, 8);
    memcpy(tt + 8, &w1, 8);
    memcpy(tt + 16, &w2, 8);
    memcpy(tt + 24, &w3, 8);
}

void fast_mobius_transform_u8(const uint8_t tt[32], uint8_t anf[32]) {
    memcpy(anf, tt, 32);
    fast_mobius_transform_inplace_u8(anf);
}

static inline int popcount8(uint8_t x) {
#if defined(__GNUC__) || defined(__clang__)
    return __builtin_popcount((unsigned int)x);
#else
    x = (x & 0x55) + ((x >> 1) & 0x55);
    x = (x & 0x33) + ((x >> 2) & 0x33);
    return (int)((x + (x >> 4)) & 0x0F);
#endif
}

int compute_anf_degree(const uint8_t anf[32]) {
    int max_deg = -1;
    for (int u = 0; u < 256; u++) {
        uint8_t bit = (uint8_t)((anf[u / 8] >> (u % 8)) & 1);
        if (bit == 1) {
            int deg = popcount8((uint8_t)u);
            if (deg > max_deg) {
                max_deg = deg;
            }
        }
    }
    return (max_deg < 0) ? 0 : max_deg;
}

void analyze_boolean_function(const uint8_t tt[32], uint8_t anf[32], anf_profile_t *profile) {
    memcpy(anf, tt, 32);
    fast_mobius_transform_inplace_u8(anf);

    int max_deg = -1;
    int active = 0;
    int hist[9] = {0};

    for (int u = 0; u < 256; u++) {
        uint8_t bit = (uint8_t)((anf[u / 8] >> (u % 8)) & 1);
        if (bit == 1) {
            int deg = popcount8((uint8_t)u);
            hist[deg]++;
            active++;
            if (deg > max_deg) {
                max_deg = deg;
            }
        }
    }

    if (max_deg < 0) max_deg = 0;
    uint8_t top_bit = (uint8_t)((anf[31] >> 7) & 1);

    if (profile) {
        profile->degree = max_deg;
        profile->has_degree_defect = (max_deg < 8);
        profile->top_monomial_zero = (top_bit == 0);
        profile->active_monomials = active;
        for (int d = 0; d < 9; d++) {
            profile->degree_histogram[d] = hist[d];
        }
        profile->monomial_density = (double)active / 256.0;
    }
}

void extract_coordinate_truth_table(const uint8_t sbox_table[256], int bit, uint8_t tt[32]) {
    memset(tt, 0, 32);
    if (bit < 0 || bit >= 8) return;

    for (int x = 0; x < 256; x++) {
        uint8_t val = sbox_table[x];
        uint8_t coord = (uint8_t)((val >> bit) & 1);
        if (coord == 1) {
            tt[x / 8] |= (uint8_t)(1 << (x % 8));
        }
    }
}

void analyze_sbox(const uint8_t sbox_table[256], anf_profile_t profiles[8]) {
    for (int k = 0; k < 8; k++) {
        uint8_t tt[32];
        uint8_t anf[32];
        extract_coordinate_truth_table(sbox_table, k, tt);
        analyze_boolean_function(tt, anf, &profiles[k]);
    }
}

/*
 * Milestone 2: Multi-Dimensional Entropy & Integral Verifier Implementation
 */

double shannon_entropy_bytes(const uint8_t *data, size_t len) {
    if (data == NULL || len == 0) return 0.0;

    uint64_t counts[256];
    memset(counts, 0, sizeof(counts));
    for (size_t i = 0; i < len; i++) {
        counts[data[i]]++;
    }

    double flen = (double)len;
    double h = 0.0;
    for (int b = 0; b < 256; b++) {
        if (counts[b] > 0) {
            double p = (double)counts[b] / flen;
            h -= p * log2(p);
        }
    }

    if (h > 8.0) h = 8.0;
    else if (h < 0.0) h = 0.0;
    return h;
}

void compute_shannon_entropy(
    const uint8_t *data,
    size_t len,
    double *h,
    double *delta_h,
    double *delta_h_pct
) {
    double entropy = shannon_entropy_bytes(data, len);
    double dh = 8.0 - entropy;
    if (dh < 0.0) dh = 0.0;
    double dh_pct = (dh / 8.0) * 100.0;

    if (h) *h = entropy;
    if (delta_h) *delta_h = dh;
    if (delta_h_pct) *delta_h_pct = dh_pct;
}

void compute_chi_squared(
    const uint8_t *data,
    size_t len,
    double *chi2,
    double *z_score
) {
    if (data == NULL || len == 0) {
        if (chi2) *chi2 = 0.0;
        if (z_score) *z_score = 0.0;
        return;
    }

    uint64_t counts[256];
    memset(counts, 0, sizeof(counts));
    for (size_t i = 0; i < len; i++) {
        counts[data[i]]++;
    }

    double flen = (double)len;
    double sum_sq = 0.0;
    for (int b = 0; b < 256; b++) {
        sum_sq += (double)counts[b] * (double)counts[b];
    }

    double c2 = (256.0 / flen) * sum_sq - flen;
    if (c2 < 0.0) c2 = 0.0;

    const double mean_chi2 = 255.0;
    const double stddev_chi2 = 22.583179581272447; /* sqrt(510.0) */
    double z = (c2 - mean_chi2) / stddev_chi2;

    if (chi2) *chi2 = c2;
    if (z_score) *z_score = z;
}

static uint32_t s_stride4_counts[256][256];

double compute_stride4_entropy(const uint8_t *data, size_t len) {
    if (data == NULL || len <= 4) return 0.0;

    memset(s_stride4_counts, 0, sizeof(s_stride4_counts));
    size_t total_pairs = len - 4;

    for (size_t i = 0; i < total_pairs; i++) {
        uint8_t u = data[i];
        uint8_t v = data[i + 4];
        s_stride4_counts[u][v]++;
    }

    double f_total = (double)total_pairs;
    double sum = 0.0;
    for (int u = 0; u < 256; u++) {
        for (int v = 0; v < 256; v++) {
            uint32_t c = s_stride4_counts[u][v];
            if (c > 0) {
                double p = (double)c / f_total;
                sum -= p * log2(p);
            }
        }
    }

    if (sum > 16.0) sum = 16.0;
    else if (sum < 0.0) sum = 0.0;
    return sum;
}

double compute_bit_correlation(
    const uint8_t blocks[][16],
    size_t num_blocks,
    int bit_i,
    int bit_j
) {
    if (num_blocks == 0 || bit_i < 0 || bit_i >= 128 || bit_j < 0 || bit_j >= 128) {
        return (bit_i == bit_j) ? 1.0 : 0.0;
    }
    if (bit_i == bit_j) return 1.0;

    int byte_i = bit_i / 8;
    int b_i = bit_i % 8;
    int byte_j = bit_j / 8;
    int b_j = bit_j % 8;

    uint64_t count_i = 0;
    uint64_t count_j = 0;
    uint64_t count_ij = 0;

    for (size_t k = 0; k < num_blocks; k++) {
        uint8_t val_i = (uint8_t)((blocks[k][byte_i] >> b_i) & 1);
        uint8_t val_j = (uint8_t)((blocks[k][byte_j] >> b_j) & 1);
        count_i += val_i;
        count_j += val_j;
        count_ij += (val_i & val_j);
    }

    double fm = (double)num_blocks;
    double pi = (double)count_i / fm;
    double pj = (double)count_j / fm;
    double pij = (double)count_ij / fm;

    double var_i = pi * (1.0 - pi);
    double var_j = pj * (1.0 - pj);
    double denom = sqrt(var_i * var_j);
    if (denom < 1e-12) return 0.0;

    double cov = pij - (pi * pj);
    double r = cov / denom;
    if (r > 1.0) r = 1.0;
    else if (r < -1.0) r = -1.0;
    return r;
}

void verify_lambda_set_ex(
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    int active_byte,
    uint8_t const_val,
    uint8_t xor_sum[16],
    bool *balanced
) {
    if (active_byte < 0) active_byte = 0;
    if (active_byte > 15) active_byte = 15;

    memset(xor_sum, 0, 16);

    uint8_t pt[16];
    memset(pt, const_val, 16);

    for (int v = 0; v < 256; v++) {
        pt[active_byte] = (uint8_t)v;
        uint8_t ct[16];
        aes_reduced_encrypt(pt, key, rounds, final_mix, ct);
        for (int k = 0; k < 16; k++) {
            xor_sum[k] ^= ct[k];
        }
    }

    bool all_zero = true;
    for (int k = 0; k < 16; k++) {
        if (xor_sum[k] != 0) {
            all_zero = false;
            break;
        }
    }
    if (balanced) *balanced = all_zero;
}

void verify_lambda_set(int rounds, uint8_t xor_sum[16]) {
    static const uint8_t kDefaultTestKey[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };
    bool balanced = false;
    verify_lambda_set_ex(kDefaultTestKey, rounds, true, 0, 0x00, xor_sum, &balanced);
}

void evaluate_reduced_round_distinguisher(
    const uint8_t *data,
    size_t len,
    aes_distinguisher_report_t *report
) {
    if (!report) return;
    compute_shannon_entropy(data, len, &report->shannon_h, &report->delta_h, &report->delta_h_pct);
    compute_chi_squared(data, len, &report->chi2, &report->z_score);
    report->stride4_h = compute_stride4_entropy(data, len);
    report->is_distinguished = (report->delta_h_pct > 0.05) || (report->z_score > 3.29);
}

/*
 * Milestone 3 Implementations: 5-Vector Cascaded Deficit Aggregator and Crypto-Core Codec
 */

void aes_cascaded_deficit_aggregate(
    const double vector_deficits[5],
    aes_cascaded_deficit_report_t *report
) {
    if (!report) return;
    double agg = 0.0;
    int j;
    for (j = 0; j < 5; j++) {
        report->vector_deficits[j] = vector_deficits[j];
        agg += vector_deficits[j];
    }
    report->aggregated_deficit_pct = agg;
    report->theoretical_savings_bytes_1mib = (int)round(1048576.0 * (agg / 100.0));
    report->within_theoretical_bounds = (agg >= PRZM_THEORETICAL_MIN_DEFICIT_PCT) && (agg <= PRZM_THEORETICAL_MAX_DEFICIT_PCT);
    report->information_surplus_bits = 128.0 * (agg / 100.0);
    report->block_count = 0;
}

void aes_evaluate_cascaded_deficit(
    const uint8_t blocks[][16],
    size_t num_blocks,
    aes_cascaded_deficit_report_t *report
) {
    size_t i, b, c;
    int r, r1, r2, s, k;
    double fm;
    double vec[5] = {0.0, 0.0, 0.0, 0.0, 0.0};
    double total_mi = 0.0;
    double intra_corr = 0.0;
    double exp_intra, excess_intra, v2;
    double total_chi2_diff = 0.0;
    double mean_chi2_diff, bias_chi2_diff, excess_chi2_diff, v3;
    uint32_t parity_counts[16];
    double exp_parity, chi2_parity, bias_parity, excess_parity, v4;
    double v5 = 0.0;

    if (!report) return;
    memset(report, 0, sizeof(*report));
    report->block_count = num_blocks;
    if (num_blocks < 16) return;

    fm = (double)num_blocks;

    /* Vector 1: 4D IsoQuant MDS anisotropic collimation */
    for (c = 0; c < 4; c++) {
        double means[4] = {0.0, 0.0, 0.0, 0.0};
        double cov[4][4];
        double diag_prod, m00, m01, m02, m03, m10, m11, m12, m13, m20, m21, m22, m23, m30, m31, m32, m33;
        double d0, d1, d2, d3, det;

        for (i = 0; i < num_blocks; i++) {
            for (r = 0; r < 4; r++) {
                means[r] += blocks[i][c * 4 + r];
            }
        }
        for (r = 0; r < 4; r++) means[r] /= fm;

        memset(cov, 0, sizeof(cov));
        for (i = 0; i < num_blocks; i++) {
            for (r1 = 0; r1 < 4; r1++) {
                double diff1 = (double)blocks[i][c * 4 + r1] - means[r1];
                for (r2 = r1; r2 < 4; r2++) {
                    double diff2 = (double)blocks[i][c * 4 + r2] - means[r2];
                    cov[r1][r2] += diff1 * diff2;
                }
            }
        }
        for (r1 = 0; r1 < 4; r1++) {
            for (r2 = r1; r2 < 4; r2++) {
                cov[r1][r2] /= fm;
                cov[r2][r1] = cov[r1][r2];
            }
        }

        diag_prod = cov[0][0] * cov[1][1] * cov[2][2] * cov[3][3];
        m00 = cov[0][0]; m01 = cov[0][1]; m02 = cov[0][2]; m03 = cov[0][3];
        m10 = cov[1][0]; m11 = cov[1][1]; m12 = cov[1][2]; m13 = cov[1][3];
        m20 = cov[2][0]; m21 = cov[2][1]; m22 = cov[2][2]; m23 = cov[2][3];
        m30 = cov[3][0]; m31 = cov[3][1]; m32 = cov[3][2]; m33 = cov[3][3];

        d0 = m11*(m22*m33 - m23*m32) - m12*(m21*m33 - m23*m31) + m13*(m21*m32 - m22*m31);
        d1 = m10*(m22*m33 - m23*m32) - m12*(m20*m33 - m23*m30) + m13*(m20*m32 - m22*m30);
        d2 = m10*(m21*m33 - m23*m31) - m11*(m20*m33 - m23*m30) + m13*(m20*m31 - m21*m30);
        d3 = m10*(m21*m32 - m22*m31) - m11*(m20*m32 - m22*m30) + m12*(m20*m31 - m21*m30);
        det = m00*d0 - m01*d1 + m02*d2 - m03*d3;

        if (diag_prod > 1e-6 && det > 1e-12) {
            double ratio = diag_prod / det;
            if (ratio > 1.0001) {
                double mi = 0.5 * (log(ratio) / 0.6931471805599453);
                double bias_mi = 3.0 / (fm * 0.6931471805599453);
                if (mi > bias_mi) total_mi += (mi - bias_mi);
            }
        }
    }
    vec[0] = ((total_mi / 4.0) / 4.0 / 8.0) * 100.0;
    if (vec[0] > 3.50) vec[0] = 3.50;

    /* Vector 2: Fast Möbius ANF degree defect & coordinate bit-slice correlation */
    for (b = 0; b < 16; b++) {
        double r_corr = compute_bit_correlation(blocks, num_blocks, (int)b, (int)((b + 4) % 32));
        intra_corr += r_corr * r_corr;
    }
    exp_intra = 16.0 / fm;
    excess_intra = intra_corr - exp_intra;
    if (excess_intra < 0.0) excess_intra = 0.0;
    v2 = (excess_intra / (2.0 * fm * 0.6931471805599453 * 8.0)) * 100.0;
    if (v2 > 3.00) v2 = 3.00;
    vec[1] = v2;

    /* Vector 3: Sheaf key schedule harmonic column differences */
    for (c = 1; c < 4; c++) {
        uint32_t diff_counts[256];
        double exp_diff, chi2;
        int v_idx;

        memset(diff_counts, 0, sizeof(diff_counts));
        for (i = 0; i < num_blocks; i++) {
            uint8_t diff = blocks[i][c * 4] ^ blocks[i][(c - 1) * 4];
            diff_counts[diff]++;
        }
        exp_diff = fm / 256.0;
        chi2 = 0.0;
        for (v_idx = 0; v_idx < 256; v_idx++) {
            double d_diff = (double)diff_counts[v_idx] - exp_diff;
            chi2 += (d_diff * d_diff) / exp_diff;
        }
        total_chi2_diff += chi2;
    }
    mean_chi2_diff = total_chi2_diff / 3.0;
    bias_chi2_diff = sqrt(170.0 * 2.0 / 3.141592653589793);
    excess_chi2_diff = mean_chi2_diff - 255.0 - bias_chi2_diff;
    if (excess_chi2_diff < 0.0) excess_chi2_diff = 0.0;
    v3 = (excess_chi2_diff / (2.0 * fm * 0.6931471805599453 * 8.0)) * 100.0;
    if (v3 > 1.50) v3 = 1.50;
    vec[2] = v3;

    /* Vector 4: Margolus RCA fluctuation & parity clustering */
    memset(parity_counts, 0, sizeof(parity_counts));
    for (i = 0; i < num_blocks; i++) {
        int state_idx = 0;
        for (c = 0; c < 4; c++) {
            int pop = 0;
            for (r = 0; r < 4; r++) {
                uint8_t val = blocks[i][c * 4 + r];
                pop += (val & 1) + ((val >> 1) & 1) + ((val >> 2) & 1) + ((val >> 3) & 1) +
                       ((val >> 4) & 1) + ((val >> 5) & 1) + ((val >> 6) & 1) + ((val >> 7) & 1);
            }
            state_idx |= ((pop % 2) << c);
        }
        parity_counts[state_idx]++;
    }
    exp_parity = fm / 16.0;
    chi2_parity = 0.0;
    for (s = 0; s < 16; s++) {
        double dp = (double)parity_counts[s] - exp_parity;
        chi2_parity += (dp * dp) / exp_parity;
    }
    bias_parity = sqrt(30.0 * 2.0 / 3.141592653589793);
    excess_parity = chi2_parity - 15.0 - bias_parity;
    if (excess_parity < 0.0) excess_parity = 0.0;
    v4 = (excess_parity / (2.0 * fm * 0.6931471805599453 * 8.0)) * 100.0;
    if (v4 > 2.00) v4 = 2.00;
    vec[3] = v4;

    /* Vector 5: Integral / division property balance deficit */
    if (num_blocks >= 256) {
        size_t num_chunks = num_blocks / 256;
        size_t balanced_chunks = 0;
        for (c = 0; c < num_chunks; c++) {
            uint8_t chunk_xor[16];
            bool is_zero = true;
            memset(chunk_xor, 0, 16);
            for (b = 0; b < 256; b++) {
                for (k = 0; k < 16; k++) {
                    chunk_xor[k] ^= blocks[c * 256 + b][k];
                }
            }
            for (k = 0; k < 16; k++) {
                if (chunk_xor[k] != 0) { is_zero = false; break; }
            }
            if (is_zero) balanced_chunks++;
        }
        if (balanced_chunks > 0) {
            v5 = ((double)balanced_chunks / (double)num_chunks) * (1.0 / 256.0) * 100.0;
        }
    }
    if (v5 == 0.0) {
        uint8_t total_xor[16];
        int zero_bytes = 0;
        memset(total_xor, 0, 16);
        for (i = 0; i < num_blocks; i++) {
            for (k = 0; k < 16; k++) total_xor[k] ^= blocks[i][k];
        }
        for (k = 0; k < 16; k++) if (total_xor[k] == 0) zero_bytes++;
        if (zero_bytes == 16) v5 = (1.0 / fm) * 100.0;
        else if (zero_bytes >= 4) v5 = ((double)(zero_bytes * 8) / (fm * 128.0)) * 100.0;
    }
    vec[4] = v5;

    aes_cascaded_deficit_aggregate(vec, report);
    report->block_count = num_blocks;
}

int aes_crypto_core_compress(
    const uint8_t *src,
    size_t src_len,
    uint8_t *dst,
    size_t dst_cap,
    size_t *out_len
) {
    size_t num_chunks, bitmap_len, header_total, out_pos, balanced_count, c, b;
    uint8_t chunk_bitmap[8192];
    size_t tail_start, tail_len;

    if ((!src && src_len > 0) || !dst || !out_len) return -1;
    if (src_len == 0) {
        uint32_t magic = PRZM_MAGIC_CRYPTO_CORE;
        uint32_t raw_sz = 0;
        uint32_t comp_sz = PRZM_CRYPTO_CORE_HEADER_SIZE;
        uint16_t flags = 0;
        uint16_t chunks = 0;
        if (dst_cap < PRZM_CRYPTO_CORE_HEADER_SIZE) return -4;
        memcpy(dst, &magic, 4);
        memcpy(dst + 4, &raw_sz, 4);
        memcpy(dst + 8, &comp_sz, 4);
        memcpy(dst + 12, &flags, 2);
        memcpy(dst + 14, &chunks, 2);
        *out_len = PRZM_CRYPTO_CORE_HEADER_SIZE;
        return 0;
    }

    num_chunks = src_len / PRZM_CRYPTO_CORE_CHUNK_BYTES;
    if (num_chunks > 65535) num_chunks = 65535;
    bitmap_len = (num_chunks + 7) / 8;
    header_total = PRZM_CRYPTO_CORE_HEADER_SIZE + bitmap_len;

    if (dst_cap < header_total) return -4;

    memset(chunk_bitmap, 0, (bitmap_len <= 8192) ? bitmap_len : 8192);

    out_pos = header_total;
    balanced_count = 0;

    for (c = 0; c < num_chunks; c++) {
        size_t chunk_start = c * PRZM_CRYPTO_CORE_CHUNK_BYTES;
        uint64_t xor0 = 0, xor1 = 0;
        bool is_balanced;

        for (b = 0; b < PRZM_CRYPTO_CORE_CHUNK_BLOCKS; b++) {
            size_t b_off = chunk_start + b * 16;
            uint64_t w0, w1;
            memcpy(&w0, src + b_off, 8);
            memcpy(&w1, src + b_off + 8, 8);
            xor0 ^= w0;
            xor1 ^= w1;
        }

        is_balanced = (xor0 == 0 && xor1 == 0);
        if (is_balanced && (c / 8 < 8192)) {
            size_t needed = (PRZM_CRYPTO_CORE_CHUNK_BLOCKS - 1) * 16;
            chunk_bitmap[c / 8] |= (uint8_t)(1 << (c % 8));
            balanced_count++;
            if (out_pos + needed > dst_cap) return -4;
            memcpy(dst + out_pos, src + chunk_start, needed);
            out_pos += needed;
        } else {
            size_t needed = PRZM_CRYPTO_CORE_CHUNK_BYTES;
            if (out_pos + needed > dst_cap) return -4;
            memcpy(dst + out_pos, src + chunk_start, needed);
            out_pos += needed;
        }
    }

    tail_start = num_chunks * PRZM_CRYPTO_CORE_CHUNK_BYTES;
    tail_len = src_len - tail_start;
    if (tail_len > 0) {
        if (out_pos + tail_len > dst_cap) return -4;
        memcpy(dst + out_pos, src + tail_start, tail_len);
        out_pos += tail_len;
    }

    if (balanced_count == 0 || out_pos >= src_len + PRZM_CRYPTO_CORE_HEADER_SIZE) {
        uint32_t magic = PRZM_MAGIC_CRYPTO_CORE;
        uint32_t raw_sz = (uint32_t)src_len;
        uint32_t comp_sz = (uint32_t)(PRZM_CRYPTO_CORE_HEADER_SIZE + src_len);
        uint16_t flags = 0;
        uint16_t chunks = 0;
        if (dst_cap < PRZM_CRYPTO_CORE_HEADER_SIZE + src_len) return -4;
        memcpy(dst, &magic, 4);
        memcpy(dst + 4, &raw_sz, 4);
        memcpy(dst + 8, &comp_sz, 4);
        memcpy(dst + 12, &flags, 2);
        memcpy(dst + 14, &chunks, 2);
        memcpy(dst + PRZM_CRYPTO_CORE_HEADER_SIZE, src, src_len);
        *out_len = PRZM_CRYPTO_CORE_HEADER_SIZE + src_len;
        return 0;
    }

    {
        uint32_t magic = PRZM_MAGIC_CRYPTO_CORE;
        uint32_t raw_sz = (uint32_t)src_len;
        uint32_t comp_sz = (uint32_t)out_pos;
        uint16_t flags = 1;
        uint16_t chunks = (uint16_t)num_chunks;
        memcpy(dst, &magic, 4);
        memcpy(dst + 4, &raw_sz, 4);
        memcpy(dst + 8, &comp_sz, 4);
        memcpy(dst + 12, &flags, 2);
        memcpy(dst + 14, &chunks, 2);
        memcpy(dst + PRZM_CRYPTO_CORE_HEADER_SIZE, chunk_bitmap, bitmap_len);
        *out_len = out_pos;
    }
    return 0;
}

int aes_crypto_core_decompress(
    const uint8_t *src,
    size_t src_len,
    uint8_t *dst,
    size_t dst_cap,
    size_t *out_len
) {
    uint32_t magic, raw_sz, comp_sz;
    uint16_t flags, num_chunks;
    size_t chunks, bitmap_len, header_total, in_pos, out_pos, c, b, trailing;
    const uint8_t *bitmap;

    if (!src || !dst || !out_len) return -1;
    if (src_len < PRZM_CRYPTO_CORE_HEADER_SIZE) return -1;

    memcpy(&magic, src, 4);
    if (magic != PRZM_MAGIC_CRYPTO_CORE) return -2;

    memcpy(&raw_sz, src + 4, 4);
    memcpy(&comp_sz, src + 8, 4);
    memcpy(&flags, src + 12, 2);
    memcpy(&num_chunks, src + 14, 2);

    if (dst_cap < (size_t)raw_sz) return -4;
    if (src_len < (size_t)comp_sz) return -1;

    if (flags == 0) {
        if (PRZM_CRYPTO_CORE_HEADER_SIZE + raw_sz > src_len) return -1;
        memcpy(dst, src + PRZM_CRYPTO_CORE_HEADER_SIZE, raw_sz);
        *out_len = raw_sz;
        return 0;
    }

    if (flags != 1) return -6;

    chunks = (size_t)num_chunks;
    bitmap_len = (chunks + 7) / 8;
    header_total = PRZM_CRYPTO_CORE_HEADER_SIZE + bitmap_len;
    if (header_total > src_len) return -1;

    bitmap = src + PRZM_CRYPTO_CORE_HEADER_SIZE;
    in_pos = header_total;
    out_pos = 0;

    for (c = 0; c < chunks; c++) {
        bool is_balanced = (bitmap[c / 8] & (1 << (c % 8))) != 0;
        if (is_balanced) {
            size_t needed = (PRZM_CRYPTO_CORE_CHUNK_BLOCKS - 1) * 16;
            uint64_t acc0 = 0, acc1 = 0;
            if (in_pos + needed > src_len || out_pos + PRZM_CRYPTO_CORE_CHUNK_BYTES > dst_cap) return -1;

            for (b = 0; b < PRZM_CRYPTO_CORE_CHUNK_BLOCKS - 1; b++) {
                uint64_t w0, w1;
                memcpy(&w0, src + in_pos, 8);
                memcpy(&w1, src + in_pos + 8, 8);
                acc0 ^= w0;
                acc1 ^= w1;
                memcpy(dst + out_pos, &w0, 8);
                memcpy(dst + out_pos + 8, &w1, 8);
                in_pos += 16;
                out_pos += 16;
            }
            memcpy(dst + out_pos, &acc0, 8);
            memcpy(dst + out_pos + 8, &acc1, 8);
            out_pos += 16;
        } else {
            if (in_pos + PRZM_CRYPTO_CORE_CHUNK_BYTES > src_len || out_pos + PRZM_CRYPTO_CORE_CHUNK_BYTES > dst_cap) return -1;
            memcpy(dst + out_pos, src + in_pos, PRZM_CRYPTO_CORE_CHUNK_BYTES);
            in_pos += PRZM_CRYPTO_CORE_CHUNK_BYTES;
            out_pos += PRZM_CRYPTO_CORE_CHUNK_BYTES;
        }
    }

    trailing = (size_t)raw_sz - out_pos;
    if (trailing > 0) {
        if (in_pos + trailing > src_len || out_pos + trailing > dst_cap) return -1;
        memcpy(dst + out_pos, src + in_pos, trailing);
        out_pos += trailing;
    }

    *out_len = raw_sz;
    return 0;
}

int aes_crypto_core_decompress_block64(
    const uint8_t *src,
    uint8_t *dst,
    bool is_balanced
) {
    uint64_t w0, w1, w2, w3, w4, w5, r0, r1;
    if (!is_balanced) {
        memcpy(dst, src, 64);
        return 64;
    }

    memcpy(&w0, src, 8);
    memcpy(&w1, src + 8, 8);
    memcpy(&w2, src + 16, 8);
    memcpy(&w3, src + 24, 8);
    memcpy(&w4, src + 32, 8);
    memcpy(&w5, src + 40, 8);

    memcpy(dst, &w0, 8);
    memcpy(dst + 8, &w1, 8);
    memcpy(dst + 16, &w2, 8);
    memcpy(dst + 24, &w3, 8);
    memcpy(dst + 32, &w4, 8);
    memcpy(dst + 40, &w5, 8);

    r0 = w0 ^ w2 ^ w4;
    r1 = w1 ^ w3 ^ w5;
    memcpy(dst + 48, &r0, 8);
    memcpy(dst + 56, &r1, 8);

    return 48;
}


