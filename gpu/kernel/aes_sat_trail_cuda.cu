/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "aes_sat_trail_cuda.h"
#include <cuda_runtime.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* Optimal MDS Column Output Pattern Table:
 * c_OptNextPattern[u][k] is the output column activity pattern v in {0..15}
 * that satisfies the MDS branch condition (wt(u) + wt(v) >= 5 for u > 0)
 * while minimizing the post-AddRoundKey cancellation weight wt(v ^ k).
 */
__constant__ uint8_t c_OptNextPattern[16][16];

static bool g_sat_cuda_initialized = false;

/* Device ShiftRows on 16-bit state activity mask */
__device__ __forceinline__ uint32_t shift_rows_mask(uint32_t s) {
    uint32_t b0  = (s >> 0) & 1,  b4  = (s >> 4) & 1,  b8  = (s >> 8) & 1,  b12 = (s >> 12) & 1;
    uint32_t b1  = (s >> 1) & 1,  b5  = (s >> 5) & 1,  b9  = (s >> 9) & 1,  b13 = (s >> 13) & 1;
    uint32_t b2  = (s >> 2) & 1,  b6  = (s >> 6) & 1,  b10 = (s >> 10) & 1, b14 = (s >> 14) & 1;
    uint32_t b3  = (s >> 3) & 1,  b7  = (s >> 7) & 1,  b11 = (s >> 11) & 1, b15 = (s >> 15) & 1;

    return (b0 << 0)   | (b5 << 1)   | (b10 << 2)  | (b15 << 3)  |
           (b4 << 4)   | (b9 << 5)   | (b14 << 6)  | (b3 << 7)   |
           (b8 << 8)   | (b13 << 9)  | (b2 << 10)  | (b7 << 11)  |
           (b12 << 12) | (b1 << 13)  | (b6 << 14)  | (b11 << 15);
}

/* Cyclic 4-bit byte rotation in 1 word (RotWord on 4-bit activity mask) */
__device__ __forceinline__ uint32_t rot4(uint32_t w) {
    return (((w >> 1) & 0x7u) | ((w << 3) & 0x8u)) & 0xFu;
}

/* Parallel GPU SAT Trail Explorer Kernel */
__global__ void aes_sat_trail_search_kernel(
    uint64_t total_candidates,
    uint32_t *d_min_state_4r,
    uint32_t *d_min_state_5r,
    uint32_t *d_min_total_4r,
    uint32_t *d_min_total_5r,
    uint32_t *d_best_seed_4r,
    uint32_t *d_best_seed_5r
) {
    uint64_t tid = (uint64_t)blockIdx.x * blockDim.x + threadIdx.x;
    uint64_t stride = (uint64_t)gridDim.x * blockDim.x;

    uint32_t l_min_state_4r = 999;
    uint32_t l_min_state_5r = 999;
    uint32_t l_min_total_4r = 999;
    uint32_t l_min_total_5r = 999;

    uint32_t l_best_4r = 0;
    uint32_t l_best_5r = 0;

    for (uint64_t idx = tid; idx < total_candidates; idx += stride) {
        uint32_t k0 = (uint32_t)(idx >> 16) & 0xFFFFu;
        uint32_t p0 = (uint32_t)idx & 0xFFFFu;
        if (p0 == 0 && k0 == 0) continue;

        // Key Schedule Linear & S-Box Expansion (Rounds 1 to 5)
        uint32_t w0 = k0 & 0xFu;
        uint32_t w1 = (k0 >> 4) & 0xFu;
        uint32_t w2 = (k0 >> 8) & 0xFu;
        uint32_t w3 = (k0 >> 12) & 0xFu;

        // Round 1 Key
        uint32_t rot_w3 = rot4(w3);
        uint32_t ks_sbox_r1 = __popc(rot_w3);
        uint32_t w4 = w0 ^ rot_w3;
        uint32_t w5 = w1 ^ w4;
        uint32_t w6 = w2 ^ w5;
        uint32_t w7 = w3 ^ w6;

        // Round 2 Key
        uint32_t rot_w7 = rot4(w7);
        uint32_t ks_sbox_r2 = __popc(rot_w7);
        uint32_t w8  = w4 ^ rot_w7;
        uint32_t w9  = w5 ^ w8;
        uint32_t w10 = w6 ^ w9;
        uint32_t w11 = w7 ^ w10;

        // Round 3 Key
        uint32_t rot_w11 = rot4(w11);
        uint32_t ks_sbox_r3 = __popc(rot_w11);
        uint32_t w12 = w8 ^ rot_w11;
        uint32_t w13 = w9 ^ w12;
        uint32_t w14 = w10 ^ w13;
        uint32_t w15 = w11 ^ w14;

        // Round 4 Key
        uint32_t rot_w15 = rot4(w15);
        uint32_t ks_sbox_r4 = __popc(rot_w15);
        uint32_t w16 = w12 ^ rot_w15;
        uint32_t w17 = w13 ^ w16;
        uint32_t w18 = w14 ^ w17;
        uint32_t w19 = w15 ^ w18;

        // Round 5 Key
        uint32_t rot_w19 = rot4(w19);
        uint32_t ks_sbox_r5 = __popc(rot_w19);
        uint32_t w20 = w16 ^ rot_w19;
        uint32_t w21 = w17 ^ w20;
        uint32_t w22 = w18 ^ w21;
        uint32_t w23 = w19 ^ w22;
        (void)w23;

        // Round 0 (Pre-round state)
        uint32_t s0 = p0 ^ k0;
        uint32_t a0 = __popc(s0);

        // Round 1
        uint32_t sr1 = shift_rows_mask(s0);
        uint32_t s1_c0 = c_OptNextPattern[sr1 & 0xFu][w4] ^ w4;
        uint32_t s1_c1 = c_OptNextPattern[(sr1 >> 4) & 0xFu][w5] ^ w5;
        uint32_t s1_c2 = c_OptNextPattern[(sr1 >> 8) & 0xFu][w6] ^ w6;
        uint32_t s1_c3 = c_OptNextPattern[(sr1 >> 12) & 0xFu][w7] ^ w7;
        uint32_t s1 = s1_c0 | (s1_c1 << 4) | (s1_c2 << 8) | (s1_c3 << 12);
        uint32_t a1 = __popc(s1);

        // Round 2
        uint32_t sr2 = shift_rows_mask(s1);
        uint32_t s2_c0 = c_OptNextPattern[sr2 & 0xFu][w8] ^ w8;
        uint32_t s2_c1 = c_OptNextPattern[(sr2 >> 4) & 0xFu][w9] ^ w9;
        uint32_t s2_c2 = c_OptNextPattern[(sr2 >> 8) & 0xFu][w10] ^ w10;
        uint32_t s2_c3 = c_OptNextPattern[(sr2 >> 12) & 0xFu][w11] ^ w11;
        uint32_t s2 = s2_c0 | (s2_c1 << 4) | (s2_c2 << 8) | (s2_c3 << 12);
        uint32_t a2 = __popc(s2);

        // Round 3
        uint32_t sr3 = shift_rows_mask(s2);
        uint32_t s3_c0 = c_OptNextPattern[sr3 & 0xFu][w12] ^ w12;
        uint32_t s3_c1 = c_OptNextPattern[(sr3 >> 4) & 0xFu][w13] ^ w13;
        uint32_t s3_c2 = c_OptNextPattern[(sr3 >> 8) & 0xFu][w14] ^ w14;
        uint32_t s3_c3 = c_OptNextPattern[(sr3 >> 12) & 0xFu][w15] ^ w15;
        uint32_t s3 = s3_c0 | (s3_c1 << 4) | (s3_c2 << 8) | (s3_c3 << 12);
        uint32_t a3 = __popc(s3);

        // Round 4
        uint32_t sr4 = shift_rows_mask(s3);
        uint32_t s4_c0 = c_OptNextPattern[sr4 & 0xFu][w16] ^ w16;
        uint32_t s4_c1 = c_OptNextPattern[(sr4 >> 4) & 0xFu][w17] ^ w17;
        uint32_t s4_c2 = c_OptNextPattern[(sr4 >> 8) & 0xFu][w18] ^ w18;
        uint32_t s4_c3 = c_OptNextPattern[(sr4 >> 12) & 0xFu][w19] ^ w19;
        uint32_t s4 = s4_c0 | (s4_c1 << 4) | (s4_c2 << 8) | (s4_c3 << 12);
        uint32_t a4 = __popc(s4);

        // Round 5
        uint32_t sr5 = shift_rows_mask(s4);
        uint32_t s5_c0 = c_OptNextPattern[sr5 & 0xFu][w20] ^ w20;
        uint32_t s5_c1 = c_OptNextPattern[(sr5 >> 4) & 0xFu][w21] ^ w21;
        uint32_t s5_c2 = c_OptNextPattern[(sr5 >> 8) & 0xFu][w22] ^ w22;
        uint32_t s5_c3 = c_OptNextPattern[(sr5 >> 12) & 0xFu][w23] ^ w23;
        uint32_t a5 = __popc(s5_c0 | (s5_c1 << 4) | (s5_c2 << 8) | (s5_c3 << 12));
        (void)a5;

        // State S-boxes
        uint32_t state_sboxes_4r = a0 + a1 + a2 + a3;
        uint32_t state_sboxes_5r = state_sboxes_4r + a4;

        // Key Schedule S-boxes
        uint32_t ks_sboxes_4r = ks_sbox_r1 + ks_sbox_r2 + ks_sbox_r3 + ks_sbox_r4;
        uint32_t ks_sboxes_5r = ks_sboxes_4r + ks_sbox_r5;

        uint32_t total_sboxes_4r = state_sboxes_4r + ks_sboxes_4r;
        uint32_t total_sboxes_5r = state_sboxes_5r + ks_sboxes_5r;

        if (state_sboxes_4r > 0 && state_sboxes_4r < l_min_state_4r) {
            l_min_state_4r = state_sboxes_4r;
            l_best_4r = (uint32_t)idx;
        }
        if (state_sboxes_5r > 0 && state_sboxes_5r < l_min_state_5r) {
            l_min_state_5r = state_sboxes_5r;
            l_best_5r = (uint32_t)idx;
        }
        if (total_sboxes_4r > 0 && total_sboxes_4r < l_min_total_4r) {
            l_min_total_4r = total_sboxes_4r;
        }
        if (total_sboxes_5r > 0 && total_sboxes_5r < l_min_total_5r) {
            l_min_total_5r = total_sboxes_5r;
        }
    }

    if (l_min_state_4r < 999) {
        uint32_t old = atomicMin(d_min_state_4r, l_min_state_4r);
        if (l_min_state_4r < old) *d_best_seed_4r = l_best_4r;
    }
    if (l_min_state_5r < 999) {
        uint32_t old = atomicMin(d_min_state_5r, l_min_state_5r);
        if (l_min_state_5r < old) *d_best_seed_5r = l_best_5r;
    }
    if (l_min_total_4r < 999) {
        atomicMin(d_min_total_4r, l_min_total_4r);
    }
    if (l_min_total_5r < 999) {
        atomicMin(d_min_total_5r, l_min_total_5r);
    }
}

int aes_sat_trail_init(int device_id) {
    cudaError_t err = cudaSetDevice(device_id);
    if (err != cudaSuccess) return -1;

    // Compute optimal MDS cancellation pattern table
    uint8_t h_v[16][16];
    for (int u = 0; u < 16; u++) {
        for (int k = 0; k < 16; k++) {
            if (u == 0) {
                h_v[u][k] = 0;
            } else {
                int min_w = 99;
                int best_v = 0;
                int wu = 0;
                for (int b = 0; b < 4; b++) wu += (u >> b) & 1;
                for (int v = 1; v < 16; v++) {
                    int wv = 0;
                    for (int b = 0; b < 4; b++) wv += (v >> b) & 1;
                    if (wu + wv >= 5) {
                        int w_out = 0;
                        int diff = v ^ k;
                        for (int b = 0; b < 4; b++) w_out += (diff >> b) & 1;
                        if (w_out < min_w) {
                            min_w = w_out;
                            best_v = v;
                        }
                    }
                }
                h_v[u][k] = (uint8_t)best_v;
            }
        }
    }

    cudaMemcpyToSymbol(c_OptNextPattern, h_v, sizeof(h_v));
    g_sat_cuda_initialized = true;
    return 0;
}

void aes_sat_trail_cleanup(void) {
    cudaDeviceSynchronize();
}

/* Host trajectory reconstructor for a given seed */
static void reconstruct_trail(
    uint32_t seed,
    aes_rk_trail_spec_t *spec
) {
    memset(spec, 0, sizeof(*spec));
    spec->seed_index = seed;
    spec->key_diff_pattern = (uint16_t)(seed >> 16);
    spec->pt_diff_pattern  = (uint16_t)seed;

    uint8_t h_v[16][16];
    cudaMemcpyFromSymbol(h_v, c_OptNextPattern, sizeof(h_v));

    uint32_t k0 = spec->key_diff_pattern;
    uint32_t p0 = spec->pt_diff_pattern;

    uint32_t w[24];
    w[0] = k0 & 0xFu; w[1] = (k0 >> 4) & 0xFu; w[2] = (k0 >> 8) & 0xFu; w[3] = (k0 >> 12) & 0xFu;
    for (int i = 4; i < 24; i++) {
        if (i % 4 == 0) {
            uint32_t rot = (((w[i - 1] >> 1) & 0x7u) | ((w[i - 1] << 3) & 0x8u)) & 0xFu;
            w[i] = w[i - 4] ^ rot;
        } else {
            w[i] = w[i - 4] ^ w[i - 1];
        }
    }

    // Key active bytes
    for (int r = 0; r <= 5; r++) {
        uint32_t kw = w[4 * r] | (w[4 * r + 1] << 4) | (w[4 * r + 2] << 8) | (w[4 * r + 3] << 12);
        int pop = 0;
        for (int b = 0; b < 16; b++) pop += (kw >> b) & 1;
        spec->round_key_active[r] = (uint8_t)pop;
    }

    // Key schedule active S-boxes
    uint32_t ks_sboxes[6] = {0};
    for (int r = 1; r <= 5; r++) {
        uint32_t rot = (((w[4 * r - 1] >> 1) & 0x7u) | ((w[4 * r - 1] << 3) & 0x8u)) & 0xFu;
        int pop = 0;
        for (int b = 0; b < 4; b++) pop += (rot >> b) & 1;
        ks_sboxes[r] = pop;
    }
    spec->total_ks_sboxes_4r = ks_sboxes[1] + ks_sboxes[2] + ks_sboxes[3] + ks_sboxes[4];
    spec->total_ks_sboxes_5r = spec->total_ks_sboxes_4r + ks_sboxes[5];

    // State trajectory
    uint32_t state = p0 ^ k0;
    int pop0 = 0;
    for (int b = 0; b < 16; b++) pop0 += (state >> b) & 1;
    spec->round_state_active[0] = (uint8_t)pop0;

    for (int r = 1; r <= 5; r++) {
        // ShiftRows
        uint32_t s = state;
        uint32_t b0  = (s >> 0) & 1,  b4  = (s >> 4) & 1,  b8  = (s >> 8) & 1,  b12 = (s >> 12) & 1;
        uint32_t b1  = (s >> 1) & 1,  b5  = (s >> 5) & 1,  b9  = (s >> 9) & 1,  b13 = (s >> 13) & 1;
        uint32_t b2  = (s >> 2) & 1,  b6  = (s >> 6) & 1,  b10 = (s >> 10) & 1, b14 = (s >> 14) & 1;
        uint32_t b3  = (s >> 3) & 1,  b7  = (s >> 7) & 1,  b11 = (s >> 11) & 1, b15 = (s >> 15) & 1;

        uint32_t sr = (b0 << 0)   | (b5 << 1)   | (b10 << 2)  | (b15 << 3)  |
                      (b4 << 4)   | (b9 << 5)   | (b14 << 6)  | (b3 << 7)   |
                      (b8 << 8)   | (b13 << 9)  | (b2 << 10)  | (b7 << 11)  |
                      (b12 << 12) | (b1 << 13)  | (b6 << 14)  | (b11 << 15);

        uint32_t sc[4];
        for (int c = 0; c < 4; c++) {
            uint32_t u = (sr >> (4 * c)) & 0xFu;
            uint32_t k = w[4 * r + c];
            uint32_t v = h_v[u][k];
            sc[c] = v ^ k;
        }
        state = sc[0] | (sc[1] << 4) | (sc[2] << 8) | (sc[3] << 12);
        int pop = 0;
        for (int b = 0; b < 16; b++) pop += (state >> b) & 1;
        spec->round_state_active[r] = (uint8_t)pop;
    }

    spec->total_state_sboxes_4r = spec->round_state_active[0] + spec->round_state_active[1] +
                                  spec->round_state_active[2] + spec->round_state_active[3];
    spec->total_state_sboxes_5r = spec->total_state_sboxes_4r + spec->round_state_active[4];

    spec->total_sboxes_4r = spec->total_state_sboxes_4r + spec->total_ks_sboxes_4r;
    spec->total_sboxes_5r = spec->total_state_sboxes_5r + spec->total_ks_sboxes_5r;
    spec->is_sat_feasible = true;
}

int aes_sat_trail_explore(
    uint64_t num_candidates,
    uint32_t threads_per_block,
    uint32_t num_blocks_grid,
    aes_sat_search_summary_t *summary
) {
    if (!summary) return -1;
    if (!g_sat_cuda_initialized) {
        if (aes_sat_trail_init(0) != 0) return -2;
    }

    if (num_candidates == 0) num_candidates = (1ULL << 32);
    if (threads_per_block == 0) threads_per_block = 256;
    if (num_blocks_grid == 0) num_blocks_grid = 224;

    uint32_t *d_min_state_4r, *d_min_state_5r;
    uint32_t *d_min_total_4r, *d_min_total_5r;
    uint32_t *d_best_seed_4r, *d_best_seed_5r;

    cudaMalloc(&d_min_state_4r, sizeof(uint32_t));
    cudaMalloc(&d_min_state_5r, sizeof(uint32_t));
    cudaMalloc(&d_min_total_4r, sizeof(uint32_t));
    cudaMalloc(&d_min_total_5r, sizeof(uint32_t));
    cudaMalloc(&d_best_seed_4r, sizeof(uint32_t));
    cudaMalloc(&d_best_seed_5r, sizeof(uint32_t));

    uint32_t init_val = 999;
    cudaMemcpy(d_min_state_4r, &init_val, sizeof(uint32_t), cudaMemcpyHostToDevice);
    cudaMemcpy(d_min_state_5r, &init_val, sizeof(uint32_t), cudaMemcpyHostToDevice);
    cudaMemcpy(d_min_total_4r, &init_val, sizeof(uint32_t), cudaMemcpyHostToDevice);
    cudaMemcpy(d_min_total_5r, &init_val, sizeof(uint32_t), cudaMemcpyHostToDevice);

    cudaEvent_t start, stop;
    cudaEventCreate(&start);
    cudaEventCreate(&stop);

    cudaEventRecord(start);
    aes_sat_trail_search_kernel<<<num_blocks_grid, threads_per_block>>>(
        num_candidates,
        d_min_state_4r,
        d_min_state_5r,
        d_min_total_4r,
        d_min_total_5r,
        d_best_seed_4r,
        d_best_seed_5r
    );
    cudaEventRecord(stop);
    cudaEventSynchronize(stop);

    float ms = 0.0f;
    cudaEventElapsedTime(&ms, start, stop);
    cudaEventDestroy(start);
    cudaEventDestroy(stop);

    uint32_t h_min_state_4r, h_min_state_5r;
    uint32_t h_min_total_4r, h_min_total_5r;
    uint32_t h_best_seed_4r, h_best_seed_5r;

    cudaMemcpy(&h_min_state_4r, d_min_state_4r, sizeof(uint32_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(&h_min_state_5r, d_min_state_5r, sizeof(uint32_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(&h_min_total_4r, d_min_total_4r, sizeof(uint32_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(&h_min_total_5r, d_min_total_5r, sizeof(uint32_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(&h_best_seed_4r, d_best_seed_4r, sizeof(uint32_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(&h_best_seed_5r, d_best_seed_5r, sizeof(uint32_t), cudaMemcpyDeviceToHost);

    cudaFree(d_min_state_4r);
    cudaFree(d_min_state_5r);
    cudaFree(d_min_total_4r);
    cudaFree(d_min_total_5r);
    cudaFree(d_best_seed_4r);
    cudaFree(d_best_seed_5r);

    double sec = (double)ms / 1000.0;
    summary->total_candidates_searched = num_candidates;
    summary->elapsed_seconds           = sec;
    summary->trails_per_second         = (double)num_candidates / sec;
    summary->giga_trails_per_second    = summary->trails_per_second / 1e9;

    summary->min_state_sboxes_4r = h_min_state_4r;
    summary->min_total_sboxes_4r = h_min_total_4r;
    summary->min_state_sboxes_5r = h_min_state_5r;
    summary->min_total_sboxes_5r = h_min_total_5r;

    summary->single_key_bound_4r = 25;
    summary->single_key_bound_5r = 26;

    reconstruct_trail(h_best_seed_4r, &summary->best_trail_4r);
    reconstruct_trail(h_best_seed_5r, &summary->best_trail_5r);

    return 0;
}
