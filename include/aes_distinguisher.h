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

/* Elementary inverse round primitives (in-place on state[16]) */
void aes_inv_sub_bytes(uint8_t state[16]);
void aes_inv_shift_rows(uint8_t state[16]);
void aes_inv_mix_columns(uint8_t state[16]);

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
 * aes_reduced_decrypt:
 * Standard reduced-round decryption inverting aes_reduced_encrypt.
 * Zero heap allocations (0 B/op).
 */
void aes_reduced_decrypt(
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

/*
 * Season 8 Cycle 1: Key Schedule Linearity, Subspace Trails & Adaptive Clustering API
 */

/* Key schedule linearity verification report */
typedef struct {
    size_t total_column_updates;
    size_t linear_updates;
    size_t nonlinear_updates;
    double linearity_ratio; /* linear_updates / (double)total_column_updates, exactly 0.75 */
    bool identity_holds;    /* true if W_{4r+c} ^ W_{4r+c-1} == W_{4(r-1)+c} for all c in {1,2,3} */
} aes_key_linearity_report_t;

void aes_verify_key_schedule_linearity(
    const uint8_t key[16],
    int rounds,
    aes_key_linearity_report_t *report
);

/* Related-key differential propagation report across 5 rounds */
typedef struct {
    uint8_t delta_k[16];                /* Injected key difference */
    uint8_t delta_p[16];                /* Injected plaintext difference */
    int active_key_cols[6];             /* Active columns in Delta K_r for r=0..5 */
    int active_data_bytes[6];           /* Active bytes in Delta X_r for r=0..5 */
    int active_data_cols[6];            /* Active columns in Delta X_r for r=0..5 */
    bool r0_column_canceled;            /* True if Delta X_0 has fewer active cols than Delta K_0 */
    bool r1_columns_inactive;           /* True if Delta X_1 has cols 1..3 inactive (zero) */
    int sbox_active_key_schedule_r1r2;  /* Total non-linear S-boxes activated in key schedule for r=1,2 */
} aes_related_key_diff_report_t;

void aes_evaluate_related_key_diff_5rounds(
    const uint8_t key[16],
    const uint8_t delta_w[4],
    aes_related_key_diff_report_t *report
);

/* Grassi Multiple-of-8 subspace trail evaluation report */
typedef struct {
    int rounds;
    bool final_mix;
    int ensemble_trials;
    int m8_satisfied_count;
    double m8_satisfied_pct;
    int non_m8_count;
    uint32_t mod8_histogram[8]; /* Counts of (N_coll mod 8) in {0, 1, ..., 7} */
    double chi2_uniform_mod8;   /* Chi2 against uniform distribution on Z_8 (df=7) */
    double p_value_mod8;        /* p-value against uniform Z_8 */
    bool property_persists;     /* True if m8_satisfied_pct > 99.0% */
} aes_multiple_of_8_report_t;

void aes_evaluate_multiple_of_8(
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    int num_trials,
    aes_multiple_of_8_report_t *report
);

/* Adaptive ciphertext clustering report */
typedef struct {
    int num_clusters;           /* Default K = 16 */
    uint32_t cluster_counts[16];
    double chi2_statistic;
    double degrees_of_freedom;  /* K - 1 = 15 */
    double z_score;             /* Wilson-Hilferty standardized score */
    double p_value;             /* Right-tail p-value */
    bool is_non_uniform;        /* True if p_value < 0.01 (rejection of uniform null hypothesis) */
} aes_clustering_report_t;

void aes_adaptive_cluster_5round(
    const uint8_t blocks[][16],
    size_t num_blocks,
    aes_clustering_report_t *report
);

/*
 * Season 8 Cycle 2: Multi-Round Related-Key Differential & Invariant Trails on 5 Rounds,
 * and Related-Key Boomerang / Quartet Differential Transitions
 */

/* Multi-Round Related-Key Differential & Invariant Trail Report */
typedef struct {
    uint8_t delta_w[4];
    uint8_t delta_k0[16];
    uint8_t delta_p[16];
    int ks_active_sboxes[6];       /* Active S-boxes in key schedule for rounds 0..5 (r=0 is 0) */
    int ks_total_active_sboxes;    /* Total active S-boxes across 5 rounds (exactly 3) */
    int active_key_cols[6];        /* Active columns in Delta K_r for r=0..5 */
    int active_key_bytes[6];       /* Active bytes in Delta K_r for r=0..5 */
    bool ks_row1_2_zero_r3;        /* Rows 1 & 2 strictly zero in Delta K_3 */
    bool ks_row1_zero_r4;          /* Row 1 strictly zero in Delta K_4 */
    int active_data_cols[6];       /* Active columns in data difference for r=0..5 */
    int active_data_bytes[6];      /* Active bytes in data difference for r=0..5 */
    bool r1_cols123_zero;          /* Columns 1..3 of Delta X_1 are strictly 0 (prob 1.0) */
    bool r5_barrier_reached;       /* True if Rounds 4 & 5 achieve full 4-column diffusion */
} aes_rk_trail_5round_report_t;

void aes_evaluate_rk_trail_5rounds_cycle2(
    const uint8_t key[16],
    const uint8_t delta_w[4],
    aes_rk_trail_5round_report_t *report
);

/* Related-Key Boomerang / Quartet evaluation report */
typedef struct {
    int rounds;
    bool final_mix;
    size_t num_quartets;
    size_t exact_returns;          /* P3 ^ P4 == delta_p */
    size_t col_returns[4];         /* (P3 ^ P4 ^ delta_p)[:, c] == 0 */
    size_t byte_matches;           /* total matching bytes across quartets */
    double exact_return_prob;
    double col_return_prob[4];
    double chi2_statistic;         /* Chi-squared test against uniform random noise floor */
    double p_value;                /* p-value for hypothesis rejection */
    bool distinguisher_valid;      /* True if statistically distinguishing with p < 0.01 */
} aes_rk_boomerang_report_t;

void aes_evaluate_rk_boomerang_cycle2(
    int rounds,
    bool final_mix,
    size_t num_quartets,
    aes_rk_boomerang_report_t *report
);

/*
 * Season 8 Cycle 3: Adaptive Subspace Invariant Compression & Key-Harmonic Distinguishers
 */

#define PRZM_MAGIC_SUBSPACE_CODEC 0x53554253 /* 'S','U','B','S' */
#define PRZM_SUBSPACE_HEADER_SIZE 16

typedef enum {
    AES_STREAM_INDEPENDENT_RANDOM = 0,
    AES_STREAM_RELATED_KEY_R1     = 1, /* Columns 1..3 zero -> 12 bytes saved per 32B pair (37.5%) */
    AES_STREAM_SUBSPACE_TRAIL     = 2, /* Columns 1, 3 zero -> 8 bytes saved per 32B pair (25.0%) */
    AES_STREAM_ROUND5_DIFFUSED    = 3  /* 5-round fully diffused -> Delta H = 0, no compression */
} aes_stream_regime_t;

typedef struct {
    aes_stream_regime_t regime;
    size_t num_pairs;          /* Number of 32-byte block pairs */
    size_t raw_bytes;
    size_t compressed_bytes;
    double compression_ratio;  /* compressed_bytes / (double)raw_bytes */
    double byte_savings_pct;   /* (1.0 - compression_ratio) * 100.0 */
    double shannon_entropy;    /* Empirical bits per byte */
    double shannon_deficit;    /* Delta H = 8.0 - (effective bits per byte) */
    bool exact_reconstruction; /* 100% bit-exact roundtrip verified */
    bool shannon_boundary_obeyed; /* true if compression occurs iff Delta H > 0 */
} aes_subspace_compression_report_t;

int aes_subspace_compress(
    const uint8_t *src,
    size_t src_len,
    aes_stream_regime_t regime,
    uint8_t *dst,
    size_t dst_cap,
    size_t *out_len
);

int aes_subspace_decompress(
    const uint8_t *src,
    size_t src_len,
    uint8_t *dst,
    size_t dst_cap,
    size_t *out_len
);

void aes_evaluate_subspace_compression_cycle3(
    aes_stream_regime_t regime,
    size_t num_pairs,
    aes_subspace_compression_report_t *report
);

/*
 * The Next-Level Transcendence Bounty (+300 pts):
 * 7-Round Related-Key Differential Trail & Structural Invariants (82.14% Linearity Preservation)
 */

typedef struct {
    uint8_t delta_w[4];
    uint8_t delta_k0[16];
    uint8_t delta_p[16];
    int ks_active_sboxes[8];          /* Active S-boxes in key schedule for rounds 0..7 (rounds 1..7: 0, 0, 1, 1, 1, 1, 1) */
    int ks_total_active_sboxes;       /* Exactly 5 active S-boxes across 7 rounds (82.14% linearity) */
    int active_key_cols[8];           /* Active columns in Delta K_r for r=0..7 */
    int active_key_bytes[8];          /* Active bytes in Delta K_r for r=0..7 */
    bool ks_col3_row123_zero_r6;      /* Delta K_6 column 3 has rows 1..3 strictly zero (only row 0 active) */
    bool ks_col3_row012_zero_r7;      /* Delta K_7 column 3 has rows 0..2 strictly zero (only row 3 active) */
    int active_data_cols[8];          /* Active data columns for r=0..7 */
    int active_data_bytes[8];         /* Active data bytes for r=0..7 */
} aes_rk_trail_7round_report_t;

void aes_evaluate_rk_trail_7rounds_transcendence(
    const uint8_t key[16],
    const uint8_t delta_w[4],
    aes_rk_trail_7round_report_t *report
);

#ifdef __cplusplus
}
#endif

#endif /* PRZM_AES_DISTINGUISHER_H */


