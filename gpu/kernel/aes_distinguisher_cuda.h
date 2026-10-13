/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef AES_DISTINGUISHER_CUDA_H
#define AES_DISTINGUISHER_CUDA_H

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Configuration for 5-Round AES CUDA Scanner */
typedef struct {
    uint8_t  key[16];            /* 128-bit master key */
    bool     final_mix;          /* true: Round 5 includes MixColumns; false: standard AES (omits MC) */
    uint64_t total_blocks;       /* Total 16-byte blocks to process */
    uint32_t threads_per_block;  /* CUDA threads per block (default 256) */
    uint32_t num_blocks_grid;    /* CUDA block count (default 112 for RTX 3060) */
    uint64_t nonce_or_counter;   /* Starting counter / IV */
} aes_cuda_config_t;

/* Statistical Scanning Results */
typedef struct {
    uint64_t total_blocks_processed;
    double   elapsed_seconds;
    double   throughput_gb_s;        /* Throughput in GB/s (1e9 B/s) */
    double   throughput_gib_s;       /* Throughput in GiB/s (2^30 B/s) */
    double   throughput_mblocks_s;   /* Millions of 16-byte blocks/s */

    /* 128-bit bit-slice frequency counters */
    uint64_t bit_counts[128];        /* Frequency of 1-bits in each coordinate */
    double   bit_proportions[128];   /* Empirical probability p_i = count / N */

    /* Pearson Chi-Squared Test against Uniform Distribution */
    double   chi_squared_128;        /* Sum_{i=0..127} (2*count_i - N)^2 / N */
    double   chi_squared_dof;        /* Degrees of freedom = 128 */
    double   chi_squared_z_score;    /* (chi2 - 128) / sqrt(256) */
    double   chi_squared_p_value;    /* Right-tail p-value */

    /* Pearson Correlation Metrics */
    double   adjacent_bit_corr[128]; /* Pearson r between bit i and bit (i+1)%128 */
    double   max_adjacent_bit_corr;  /* Max absolute Pearson r among adjacent pairs */
    double   mean_adjacent_bit_corr; /* Mean absolute Pearson r among adjacent pairs */
    double   cross_word_corr[32];    /* Pearson r between bit i and bit i+32 */
    double   max_cross_word_corr;    /* Max absolute cross-word Pearson r */

    /* Byte-Level Statistics & Shannon Entropy */
    double   byte_entropy[16];       /* Shannon entropy in bits/byte for each of the 16 state positions */
    uint8_t  xor_sum[16];            /* Bitwise XOR sum over all ciphertexts */
    bool     is_zero_sum_balanced;   /* true iff all 16 bytes of xor_sum == 0x00 */
} aes_cuda_stats_t;

/*
 * aes_cuda_init:
 * Initializes CUDA device, verifies Compute Capability (sm_86 for Ampere RTX 3060),
 * and preloads constant-memory AES T-tables.
 * Returns 0 on success, negative error code on failure.
 */
int aes_cuda_init(int device_id);

/*
 * aes_cuda_scan_5r:
 * Executes ultra-high throughput 5-round AES encryption directly on RTX 3060
 * with on-the-fly parallel statistical scanning (bit frequencies, Pearson correlations,
 * Chi-squared test against uniform distribution, and XOR balance).
 * Target throughput: >50 GB/s.
 * Returns 0 on success, negative error code on failure.
 */
int aes_cuda_scan_5r(
    const aes_cuda_config_t *config,
    aes_cuda_stats_t *stats
);

/*
 * aes_cuda_scan_lambda:
 * Evaluates the Knudsen Lambda-set zero-sum property on GPU across rounds (1 to 5).
 * Generates 256-block Lambda-sets (active byte 0..255, 15 bytes constant),
 * encrypts them through reduced-round AES, and evaluates whether XOR sum vanishes.
 * Returns 0 on success, negative error code on failure.
 */
int aes_cuda_scan_lambda(
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    int active_byte,
    uint8_t const_val,
    uint8_t xor_sum[16],
    bool *is_balanced
);

/*
 * aes_cuda_verify_reference:
 * Verifies bit-exact output equivalence between GPU 5-round kernel
 * and CPU FIPS 197 reference implementation on num_samples test vectors.
 * Returns 0 on success (100% match), non-zero on mismatch.
 */
int aes_cuda_verify_reference(
    const uint8_t key[16],
    bool final_mix,
    size_t num_samples
);

/*
 * aes_cuda_cleanup:
 * Releases GPU device resources.
 */
void aes_cuda_cleanup(void);

#ifdef __cplusplus
}
#endif

#endif /* AES_DISTINGUISHER_CUDA_H */
