/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef PRZM_AES_DISTINGUISHER_H
#define PRZM_AES_DISTINGUISHER_H

#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* State tap point enumeration */
typedef enum {
    AES_TAP_AFTER_ADDROUNDKEY0 = 0,
    AES_TAP_AFTER_SUBBYTES     = 1,
    AES_TAP_AFTER_SHIFTROWS    = 2,
    AES_TAP_AFTER_MIXCOLUMNS   = 3,
    AES_TAP_AFTER_ADDROUNDKEY  = 4
} aes_tap_point_t;

typedef aes_tap_point_t aes_state_tap_point_t;

/* Key schedule mode enumeration */
typedef enum {
    AES_KEY_SCHEDULE_STANDARD = 0,
    AES_KEY_SCHEDULE_ZERO     = 1,
    AES_KEY_SCHEDULE_LINEAR   = 2,
    AES_KEY_SCHEDULE_REPEATED = 3
} aes_key_schedule_mode_t;

/* Tap callback invoked at intermediate micro-steps */
typedef void (*aes_tap_callback_t)(int round, aes_tap_point_t point, const uint8_t state[16], void *user_data);

/* ANF profile structure */
typedef struct {
    int degree;                 /* Maximum degree among active monomials (0..8) */
    bool has_degree_defect;     /* True if degree < 8 */
    bool top_monomial_zero;     /* True if a_{255} == 0 */
    int active_monomials;       /* Total non-zero terms in ANF */
    int degree_histogram[9];    /* Count of active monomials for each degree 0..8 */
    double monomial_density;    /* active_monomials / 256.0 */
} anf_profile_t;

/* AES S-Box table inspection */
const uint8_t* aes_get_sbox(void);
const uint8_t* aes_get_inv_sbox(void);

/* Key expansion into pre-allocated round_keys[(rounds+1)][16] */
void aes_expand_key(
    const uint8_t key[16],
    int rounds,
    aes_key_schedule_mode_t mode,
    uint8_t linear_delta,
    uint8_t round_keys[][16]
);

/* Elementary round primitives (in-place on state[16]) */
void aes_sub_bytes(uint8_t state[16]);
void aes_shift_rows(uint8_t state[16]);
void aes_mix_columns(uint8_t state[16]);
void aes_add_round_key(uint8_t state[16], const uint8_t rk[16]);

/*
 * aes_encrypt_block_with_tap:
 * Executes reduced-round AES on a single 16-byte block with intermediate state taps.
 * Zero heap allocations (0 B/op).
 */
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
);

/*
 * aes_reduced_encrypt:
 * Standard reduced-round encryption conforming to PROJECT.md interface contract.
 */
void aes_reduced_encrypt(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    uint8_t out[16]
);

/*
 * aes_reduced_encrypt_tap:
 * Runs reduced-round AES and returns the intermediate state at the specified tap point.
 * Zero heap allocations (0 B/op).
 */
void aes_reduced_encrypt_tap(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    aes_state_tap_point_t tap,
    uint8_t out[16]
);

/*
 * Fast Möbius Transform (FMT) over F_2^8 (256-bit truth tables = 32 bytes)
 * Radix-2 butterfly network, zero heap allocations.
 */
void fast_mobius_transform_inplace_u8(uint8_t tt[32]);
void fast_mobius_transform_u8(const uint8_t tt[32], uint8_t anf[32]);
int compute_anf_degree(const uint8_t anf[32]);
void analyze_boolean_function(const uint8_t tt[32], uint8_t anf[32], anf_profile_t *profile);
void extract_coordinate_truth_table(const uint8_t sbox_table[256], int bit, uint8_t tt[32]);
void analyze_sbox(const uint8_t sbox_table[256], anf_profile_t profiles[8]);

/*
 * Milestone 2: Multi-Dimensional Entropy & Integral Verifier API
 */

/* Computes Shannon entropy H in bits/byte [0.0 .. 8.0] */
double shannon_entropy_bytes(const uint8_t *data, size_t len);

/* Computes Shannon entropy H, absolute deficit delta_h (8.0 - H), and delta_h_pct ((delta_h/8.0)*100).
 * Zero heap allocations (0 B/op).
 */
void compute_shannon_entropy(
    const uint8_t *data,
    size_t len,
    double *h,
    double *delta_h,
    double *delta_h_pct
);

/* Computes Pearson Chi-Square statistic and standard normal Z-score against uniform distribution.
 * Zero heap allocations (0 B/op).
 */
void compute_chi_squared(
    const uint8_t *data,
    size_t len,
    double *chi2,
    double *z_score
);

/* Computes Stride-4 joint byte entropy across 4-byte column strides.
 * Zero heap allocations (0 B/op).
 */
double compute_stride4_entropy(const uint8_t *data, size_t len);

/* Computes bit-slice cross correlation between bit i and bit j across blocks.
 * Zero heap allocations (0 B/op).
 */
double compute_bit_correlation(
    const uint8_t blocks[][16],
    size_t num_blocks,
    int bit_i,
    int bit_j
);

/*
 * verify_lambda_set:
 * Generates a 256-block Lambda-set (active byte 0 varies 0..255, 15 bytes constant),
 * encrypts through reduced-round AES with default test key and final MixColumns,
 * and evaluates the bitwise XOR sum into xor_sum[16].
 * Conforms to PROJECT.md interface contract.
 * Zero heap allocations (0 B/op).
 */
void verify_lambda_set(int rounds, uint8_t xor_sum[16]);

/*
 * verify_lambda_set_ex:
 * Extended Lambda-set verifier allowing custom key, active byte index, and final MixColumns flag.
 * Sets *balanced to true iff all 16 bytes of xor_sum are 0x00.
 * Zero heap allocations (0 B/op).
 */
void verify_lambda_set_ex(
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    int active_byte,
    uint8_t const_val,
    uint8_t xor_sum[16],
    bool *balanced
);

/*
 * evaluate_reduced_round_distinguisher:
 * Evaluates the full entropy and statistical profile on a stream of blocks.
 * Zero heap allocations (0 B/op).
 */
typedef struct {
    double shannon_h;
    double delta_h;
    double delta_h_pct;
    double chi2;
    double z_score;
    double stride4_h;
    bool is_distinguished;
} aes_distinguisher_report_t;

void evaluate_reduced_round_distinguisher(
    const uint8_t *data,
    size_t len,
    aes_distinguisher_report_t *report
);

/*
 * Milestone 3: Pure Crypto-Core Compression & Cascaded Deficit Aggregator
 */

#define PRZM_MAGIC_CRYPTO_CORE         0x43525950 /* 'C','R','Y','P' */
#define PRZM_CRYPTO_CORE_HEADER_SIZE   16
#define PRZM_CRYPTO_CORE_CHUNK_BLOCKS  256
#define PRZM_CRYPTO_CORE_CHUNK_BYTES   (PRZM_CRYPTO_CORE_CHUNK_BLOCKS * 16) /* 4096 */

#define PRZM_THEORETICAL_MIN_DEFICIT_PCT 0.155
#define PRZM_THEORETICAL_MAX_DEFICIT_PCT 0.302
#define PRZM_THEORETICAL_MIN_SAVINGS_1MIB 1630
#define PRZM_THEORETICAL_MAX_SAVINGS_1MIB 3170
#define PRZM_MAX_DECOMP_CYCLES_PER_64B   384

typedef struct {
    double vector_deficits[5];
    double aggregated_deficit_pct;
    int theoretical_savings_bytes_1mib;
    bool within_theoretical_bounds;
    double information_surplus_bits;
    size_t block_count;
} aes_cascaded_deficit_report_t;

/*
 * aes_cascaded_deficit_aggregate:
 * Aggregates 5 defect vectors using the information-theoretic chain rule:
 * Delta H_agg = sum_{j=1}^5 Delta H_j.
 * Zero heap allocations (0 B/op).
 */
void aes_cascaded_deficit_aggregate(
    const double vector_deficits[5],
    aes_cascaded_deficit_report_t *report
);

/*
 * aes_evaluate_cascaded_deficit:
 * Evaluates the 5 defect vectors on an array of 16-byte blocks.
 * Zero heap allocations (0 B/op).
 */
void aes_evaluate_cascaded_deficit(
    const uint8_t blocks[][16],
    size_t num_blocks,
    aes_cascaded_deficit_report_t *report
);

/*
 * aes_crypto_core_compress:
 * Compresses raw pure crypto-core payload into dst buffer.
 * Maps zero-sum division property invariants to authentic lossless compression.
 * Zero heap allocations (0 B/op, 0 malloc/free).
 * Returns 0 on success, negative error code on failure.
 */
int aes_crypto_core_compress(
    const uint8_t *src,
    size_t src_len,
    uint8_t *dst,
    size_t dst_cap,
    size_t *out_len
);

/*
 * aes_crypto_core_decompress:
 * Instantaneous O(1) decompressor for crypto-core frames.
 * Strictly ZERO heap memory allocations (0 malloc/free).
 * Returns 0 on success, negative error code on failure.
 */
int aes_crypto_core_decompress(
    const uint8_t *src,
    size_t src_len,
    uint8_t *dst,
    size_t dst_cap,
    size_t *out_len
);

/*
 * aes_crypto_core_decompress_block64:
 * Microarchitectural 64-byte block decompressor microcode.
 * Branchless register execution with cycle latency <= 384 CPU cycles.
 * If is_balanced is true: reads 48 bytes from src (B0, B1, B2) and reconstructs B3 = B0 ^ B1 ^ B2 in dst (64 bytes).
 * If is_balanced is false: copies 64 bytes from src to dst.
 * Returns bytes read from src (48 or 64).
 */
int aes_crypto_core_decompress_block64(
    const uint8_t *src,
    uint8_t *dst,
    bool is_balanced
);

#ifdef __cplusplus
}
#endif

#endif /* PRZM_AES_DISTINGUISHER_H */


