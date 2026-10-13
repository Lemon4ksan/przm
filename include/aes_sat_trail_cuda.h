/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef AES_SAT_TRAIL_CUDA_H
#define AES_SAT_TRAIL_CUDA_H

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Related-Key Differential Trail Specification */
typedef struct {
    uint32_t seed_index;              /* Global seed index in search space */
    uint16_t key_diff_pattern;        /* Master key difference pattern Delta K_0 */
    uint16_t pt_diff_pattern;         /* Plaintext difference pattern Delta P */
    uint8_t  round_state_active[6];   /* Active state bytes at Rounds 0, 1, 2, 3, 4, 5 */
    uint8_t  round_key_active[6];     /* Active key bytes at Rounds 0, 1, 2, 3, 4, 5 */
    uint32_t total_state_sboxes_4r;   /* Active state S-boxes (Rounds 0..3) */
    uint32_t total_state_sboxes_5r;   /* Active state S-boxes (Rounds 0..4) */
    uint32_t total_ks_sboxes_4r;      /* Active key schedule S-boxes (Rounds 1..4) */
    uint32_t total_ks_sboxes_5r;      /* Active key schedule S-boxes (Rounds 1..5) */
    uint32_t total_sboxes_4r;         /* State + Key Schedule S-boxes (4R) */
    uint32_t total_sboxes_5r;         /* State + Key Schedule S-boxes (5R) */
    bool     is_sat_feasible;         /* Satisfies MDS code and S-box DDT constraints */
} aes_rk_trail_spec_t;

/* Exploration Benchmark Results */
typedef struct {
    uint64_t total_candidates_searched; /* Number of candidates explored (e.g. 2^32) */
    double   elapsed_seconds;           /* Kernel execution time */
    double   trails_per_second;         /* Evaluation rate in trails/s */
    double   giga_trails_per_second;    /* Evaluation rate in Giga-trails/s (10^9 / s) */

    /* 4-Round Results */
    uint32_t min_state_sboxes_4r;       /* Minimal active state S-boxes across 4 rounds */
    uint32_t min_total_sboxes_4r;       /* Minimal total S-boxes across 4 rounds */
    aes_rk_trail_spec_t best_trail_4r;  /* Optimal 4-round trail */

    /* 5-Round Results */
    uint32_t min_state_sboxes_5r;       /* Minimal active state S-boxes across 5 rounds */
    uint32_t min_total_sboxes_5r;       /* Minimal total S-boxes across 5 rounds */
    aes_rk_trail_spec_t best_trail_5r;  /* Optimal 5-round trail */

    /* Single-Key Theoretical Bounds for Comparison */
    uint32_t single_key_bound_4r;       /* 25 (Wide Trail Strategy) */
    uint32_t single_key_bound_5r;       /* 26+ */
} aes_sat_search_summary_t;

/* Initialize CUDA device for SAT trail exploration */
int aes_sat_trail_init(int device_id);

/* Run GPU Branch-and-Bound Differential Trail Explorer across candidates */
int aes_sat_trail_explore(
    uint64_t num_candidates,            /* Up to (1ULL << 32) */
    uint32_t threads_per_block,         /* default 256 */
    uint32_t num_blocks_grid,           /* default 224 for RTX 3060 */
    aes_sat_search_summary_t *summary
);

/* Clean up GPU device resources */
void aes_sat_trail_cleanup(void);

#ifdef __cplusplus
}
#endif

#endif /* AES_SAT_TRAIL_CUDA_H */
