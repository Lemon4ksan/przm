/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "aes_distinguisher_cuda.h"
#include <cuda_runtime.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

/* NIST AES S-Box */
static const uint8_t kSBoxHost[256] = {
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

static const uint8_t kRconHost[11] = {
    0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36
};

/* Device Constant Memory Tables */
__constant__ uint32_t c_T0[256];
__constant__ uint32_t c_T1[256];
__constant__ uint32_t c_T2[256];
__constant__ uint32_t c_T3[256];
__constant__ uint8_t  c_SBox[256];
__constant__ uint32_t c_RoundKeys[15 * 4];

static bool g_cuda_initialized = false;

/* Host xtime in GF(2^8) */
static inline uint8_t host_xtime(uint8_t x) {
    return (uint8_t)((x << 1) ^ (((int8_t)x < 0) ? 0x1B : 0));
}

/* Host key expansion for rounds */
static void host_expand_key(const uint8_t key[16], int rounds, uint32_t rks[15 * 4]) {
    int total_words = 4 * (rounds + 1);
    for (int i = 0; i < 4; i++) {
        rks[i] = ((uint32_t)key[4 * i]) |
                 (((uint32_t)key[4 * i + 1]) << 8) |
                 (((uint32_t)key[4 * i + 2]) << 16) |
                 (((uint32_t)key[4 * i + 3]) << 24);
    }
    for (int i = 4; i < total_words; i++) {
        uint32_t temp = rks[i - 1];
        if (i % 4 == 0) {
            uint8_t b0 = (uint8_t)temp;
            uint8_t b1 = (uint8_t)(temp >> 8);
            uint8_t b2 = (uint8_t)(temp >> 16);
            uint8_t b3 = (uint8_t)(temp >> 24);

            uint32_t sb0 = (uint32_t)kSBoxHost[b1];
            uint32_t sb1 = ((uint32_t)kSBoxHost[b2]) << 8;
            uint32_t sb2 = ((uint32_t)kSBoxHost[b3]) << 16;
            uint32_t sb3 = ((uint32_t)kSBoxHost[b0]) << 24;
            uint32_t sub_rot = sb0 | sb1 | sb2 | sb3;

            uint32_t rcon_val = 0;
            int rcon_idx = i / 4;
            if (rcon_idx < 11) {
                rcon_val = (uint32_t)kRconHost[rcon_idx];
            }
            temp = sub_rot ^ rcon_val;
        }
        rks[i] = rks[i - 4] ^ temp;
    }
}

int aes_cuda_init(int device_id) {
    cudaError_t err = cudaSetDevice(device_id);
    if (err != cudaSuccess) {
        fprintf(stderr, "CUDA Error: cudaSetDevice failed: %s\n", cudaGetErrorString(err));
        return -1;
    }

    cudaDeviceProp prop;
    cudaGetDeviceProperties(&prop, device_id);
    if (prop.major < 8) {
        fprintf(stderr, "Warning: Compute Capability %d.%d < sm_86\n", prop.major, prop.minor);
    }

    /* Build 32-bit T-tables */
    uint32_t h_T0[256], h_T1[256], h_T2[256], h_T3[256];
    for (int x = 0; x < 256; x++) {
        uint8_t s  = kSBoxHost[x];
        uint8_t s2 = host_xtime(s);
        uint8_t s3 = s2 ^ s;

        h_T0[x] = ((uint32_t)s2) | (((uint32_t)s) << 8) | (((uint32_t)s) << 16) | (((uint32_t)s3) << 24);
        h_T1[x] = ((uint32_t)s3) | (((uint32_t)s2) << 8) | (((uint32_t)s) << 16) | (((uint32_t)s) << 24);
        h_T2[x] = ((uint32_t)s) | (((uint32_t)s3) << 8) | (((uint32_t)s2) << 16) | (((uint32_t)s) << 24);
        h_T3[x] = ((uint32_t)s) | (((uint32_t)s) << 8) | (((uint32_t)s3) << 16) | (((uint32_t)s2) << 24);
    }

    cudaMemcpyToSymbol(c_T0, h_T0, sizeof(h_T0));
    cudaMemcpyToSymbol(c_T1, h_T1, sizeof(h_T1));
    cudaMemcpyToSymbol(c_T2, h_T2, sizeof(h_T2));
    cudaMemcpyToSymbol(c_T3, h_T3, sizeof(h_T3));
    cudaMemcpyToSymbol(c_SBox, kSBoxHost, sizeof(kSBoxHost));

    g_cuda_initialized = true;
    return 0;
}

void aes_cuda_cleanup(void) {
    cudaDeviceSynchronize();
}

/*
 * Device 32x32 Bit-Matrix Transposition
 * Using warp shuffle instructions (__shfl_xor_sync) in 5 butterfly stages.
 * Transposes 32 registers across 32 threads so lane b gets bit b of all 32 threads.
 */
__device__ __forceinline__ uint32_t warp_transpose32(uint32_t x, int lane) {
    uint32_t other;

    // Stage 0: 16-bit blocks
    other = __shfl_xor_sync(0xFFFFFFFF, x, 16);
    x = ((lane & 16) == 0) ? ((x & 0x0000FFFFu) | (other << 16))
                           : ((x & 0xFFFF0000u) | (other >> 16));

    // Stage 1: 8-bit blocks
    other = __shfl_xor_sync(0xFFFFFFFF, x, 8);
    x = ((lane & 8) == 0) ? ((x & 0x00FF00FFu) | ((other & 0x00FF00FFu) << 8))
                          : ((x & 0xFF00FF00u) | ((other & 0xFF00FF00u) >> 8));

    // Stage 2: 4-bit blocks
    other = __shfl_xor_sync(0xFFFFFFFF, x, 4);
    x = ((lane & 4) == 0) ? ((x & 0x0F0F0F0Fu) | ((other & 0x0F0F0F0Fu) << 4))
                          : ((x & 0xF0F0F0F0u) | ((other & 0xF0F0F0F0u) >> 4));

    // Stage 3: 2-bit blocks
    other = __shfl_xor_sync(0xFFFFFFFF, x, 2);
    x = ((lane & 2) == 0) ? ((x & 0x33333333u) | ((other & 0x33333333u) << 2))
                          : ((x & 0xCCCCCCCCu) | ((other & 0xCCCCCCCCu) >> 2));

    // Stage 4: 1-bit blocks
    other = __shfl_xor_sync(0xFFFFFFFF, x, 1);
    x = ((lane & 1) == 0) ? ((x & 0x55555555u) | ((other & 0x55555555u) << 1))
                          : ((x & 0xAAAAAAAAu) | ((other & 0xAAAAAAAAu) >> 1));

    return x;
}

/*
 * Device 5-Round AES Encryption Kernel Core
 * Inlines full 5-round state ladder in registers using constant-memory T-tables.
 */
__device__ __forceinline__ void aes_encrypt_5r_core(
    uint32_t p0, uint32_t p1, uint32_t p2, uint32_t p3,
    bool final_mix,
    uint32_t *c0_out, uint32_t *c1_out, uint32_t *c2_out, uint32_t *c3_out
) {
    // Round 0: Initial AddRoundKey
    uint32_t s0 = p0 ^ c_RoundKeys[0];
    uint32_t s1 = p1 ^ c_RoundKeys[1];
    uint32_t s2 = p2 ^ c_RoundKeys[2];
    uint32_t s3 = p3 ^ c_RoundKeys[3];

    // Rounds 1 to 4: SubBytes + ShiftRows + MixColumns + AddRoundKey
    #pragma unroll
    for (int r = 1; r <= 4; r++) {
        uint32_t t0 = c_T0[s0 & 0xFF] ^
                      c_T1[(s1 >> 8) & 0xFF] ^
                      c_T2[(s2 >> 16) & 0xFF] ^
                      c_T3[(s3 >> 24) & 0xFF] ^
                      c_RoundKeys[4 * r + 0];

        uint32_t t1 = c_T0[s1 & 0xFF] ^
                      c_T1[(s2 >> 8) & 0xFF] ^
                      c_T2[(s3 >> 16) & 0xFF] ^
                      c_T3[(s0 >> 24) & 0xFF] ^
                      c_RoundKeys[4 * r + 1];

        uint32_t t2 = c_T0[s2 & 0xFF] ^
                      c_T1[(s3 >> 8) & 0xFF] ^
                      c_T2[(s0 >> 16) & 0xFF] ^
                      c_T3[(s1 >> 24) & 0xFF] ^
                      c_RoundKeys[4 * r + 2];

        uint32_t t3 = c_T0[s3 & 0xFF] ^
                      c_T1[(s0 >> 8) & 0xFF] ^
                      c_T2[(s1 >> 16) & 0xFF] ^
                      c_T3[(s2 >> 24) & 0xFF] ^
                      c_RoundKeys[4 * r + 3];

        s0 = t0; s1 = t1; s2 = t2; s3 = t3;
    }

    // Round 5:
    if (final_mix) {
        // Round 5 WITH MixColumns (Academic reduced-round distinguisher setting)
        uint32_t t0 = c_T0[s0 & 0xFF] ^
                      c_T1[(s1 >> 8) & 0xFF] ^
                      c_T2[(s2 >> 16) & 0xFF] ^
                      c_T3[(s3 >> 24) & 0xFF] ^
                      c_RoundKeys[20];

        uint32_t t1 = c_T0[s1 & 0xFF] ^
                      c_T1[(s2 >> 8) & 0xFF] ^
                      c_T2[(s3 >> 16) & 0xFF] ^
                      c_T3[(s0 >> 24) & 0xFF] ^
                      c_RoundKeys[21];

        uint32_t t2 = c_T0[s2 & 0xFF] ^
                      c_T1[(s3 >> 8) & 0xFF] ^
                      c_T2[(s0 >> 16) & 0xFF] ^
                      c_T3[(s1 >> 24) & 0xFF] ^
                      c_RoundKeys[22];

        uint32_t t3 = c_T0[s3 & 0xFF] ^
                      c_T1[(s0 >> 8) & 0xFF] ^
                      c_T2[(s1 >> 16) & 0xFF] ^
                      c_T3[(s2 >> 24) & 0xFF] ^
                      c_RoundKeys[23];

        *c0_out = t0; *c1_out = t1; *c2_out = t2; *c3_out = t3;
    } else {
        // Standard AES Round 5 (NO MixColumns: SubBytes + ShiftRows + AddRoundKey)
        uint32_t t0 = ((uint32_t)c_SBox[s0 & 0xFF]) |
                      (((uint32_t)c_SBox[(s1 >> 8) & 0xFF]) << 8) |
                      (((uint32_t)c_SBox[(s2 >> 16) & 0xFF]) << 16) |
                      (((uint32_t)c_SBox[(s3 >> 24) & 0xFF]) << 24);

        uint32_t t1 = ((uint32_t)c_SBox[s1 & 0xFF]) |
                      (((uint32_t)c_SBox[(s2 >> 8) & 0xFF]) << 8) |
                      (((uint32_t)c_SBox[(s3 >> 16) & 0xFF]) << 16) |
                      (((uint32_t)c_SBox[(s0 >> 24) & 0xFF]) << 24);

        uint32_t t2 = ((uint32_t)c_SBox[s2 & 0xFF]) |
                      (((uint32_t)c_SBox[(s3 >> 8) & 0xFF]) << 8) |
                      (((uint32_t)c_SBox[(s0 >> 16) & 0xFF]) << 16) |
                      (((uint32_t)c_SBox[(s1 >> 24) & 0xFF]) << 24);

        uint32_t t3 = ((uint32_t)c_SBox[s3 & 0xFF]) |
                      (((uint32_t)c_SBox[(s0 >> 8) & 0xFF]) << 8) |
                      (((uint32_t)c_SBox[(s1 >> 16) & 0xFF]) << 16) |
                      (((uint32_t)c_SBox[(s2 >> 24) & 0xFF]) << 24);

        *c0_out = t0 ^ c_RoundKeys[20];
        *c1_out = t1 ^ c_RoundKeys[21];
        *c2_out = t2 ^ c_RoundKeys[22];
        *c3_out = t3 ^ c_RoundKeys[23];
    }
}

__device__ __forceinline__ void aes_encrypt_5r_shmem(
    uint32_t p0, uint32_t p1, uint32_t p2, uint32_t p3,
    bool final_mix,
    const uint32_t * __restrict__ s_T0,
    const uint32_t * __restrict__ s_T1,
    const uint32_t * __restrict__ s_T2,
    const uint32_t * __restrict__ s_T3,
    const uint8_t  * __restrict__ s_SBox,
    const uint32_t * __restrict__ s_RK,
    uint32_t *c0_out, uint32_t *c1_out, uint32_t *c2_out, uint32_t *c3_out
) {
    uint32_t s0 = p0 ^ s_RK[0];
    uint32_t s1 = p1 ^ s_RK[1];
    uint32_t s2 = p2 ^ s_RK[2];
    uint32_t s3 = p3 ^ s_RK[3];

    #pragma unroll
    for (int r = 1; r <= 4; r++) {
        uint32_t t0 = s_T0[s0 & 0xFF] ^ s_T1[(s1 >> 8) & 0xFF] ^ s_T2[(s2 >> 16) & 0xFF] ^ s_T3[(s3 >> 24) & 0xFF] ^ s_RK[4 * r + 0];
        uint32_t t1 = s_T0[s1 & 0xFF] ^ s_T1[(s2 >> 8) & 0xFF] ^ s_T2[(s3 >> 16) & 0xFF] ^ s_T3[(s0 >> 24) & 0xFF] ^ s_RK[4 * r + 1];
        uint32_t t2 = s_T0[s2 & 0xFF] ^ s_T1[(s3 >> 8) & 0xFF] ^ s_T2[(s0 >> 16) & 0xFF] ^ s_T3[(s1 >> 24) & 0xFF] ^ s_RK[4 * r + 2];
        uint32_t t3 = s_T0[s3 & 0xFF] ^ s_T1[(s0 >> 8) & 0xFF] ^ s_T2[(s1 >> 16) & 0xFF] ^ s_T3[(s2 >> 24) & 0xFF] ^ s_RK[4 * r + 3];
        s0 = t0; s1 = t1; s2 = t2; s3 = t3;
    }

    if (final_mix) {
        uint32_t t0 = s_T0[s0 & 0xFF] ^ s_T1[(s1 >> 8) & 0xFF] ^ s_T2[(s2 >> 16) & 0xFF] ^ s_T3[(s3 >> 24) & 0xFF] ^ s_RK[20];
        uint32_t t1 = s_T0[s1 & 0xFF] ^ s_T1[(s2 >> 8) & 0xFF] ^ s_T2[(s3 >> 16) & 0xFF] ^ s_T3[(s0 >> 24) & 0xFF] ^ s_RK[21];
        uint32_t t2 = s_T0[s2 & 0xFF] ^ s_T1[(s3 >> 8) & 0xFF] ^ s_T2[(s0 >> 16) & 0xFF] ^ s_T3[(s1 >> 24) & 0xFF] ^ s_RK[22];
        uint32_t t3 = s_T0[s3 & 0xFF] ^ s_T1[(s0 >> 8) & 0xFF] ^ s_T2[(s1 >> 16) & 0xFF] ^ s_T3[(s2 >> 24) & 0xFF] ^ s_RK[23];
        *c0_out = t0; *c1_out = t1; *c2_out = t2; *c3_out = t3;
    } else {
        uint32_t t0 = ((uint32_t)((s_T0[s0 & 0xFF] >> 8) & 0xFF)) |
                      (((uint32_t)((s_T0[(s1 >> 8) & 0xFF] >> 8) & 0xFF)) << 8) |
                      (((uint32_t)((s_T0[(s2 >> 16) & 0xFF] >> 8) & 0xFF)) << 16) |
                      (((uint32_t)((s_T0[(s3 >> 24) & 0xFF] >> 8) & 0xFF)) << 24);

        uint32_t t1 = ((uint32_t)((s_T0[s1 & 0xFF] >> 8) & 0xFF)) |
                      (((uint32_t)((s_T0[(s2 >> 8) & 0xFF] >> 8) & 0xFF)) << 8) |
                      (((uint32_t)((s_T0[(s3 >> 16) & 0xFF] >> 8) & 0xFF)) << 16) |
                      (((uint32_t)((s_T0[(s0 >> 24) & 0xFF] >> 8) & 0xFF)) << 24);

        uint32_t t2 = ((uint32_t)((s_T0[s2 & 0xFF] >> 8) & 0xFF)) |
                      (((uint32_t)((s_T0[(s3 >> 8) & 0xFF] >> 8) & 0xFF)) << 8) |
                      (((uint32_t)((s_T0[(s0 >> 16) & 0xFF] >> 8) & 0xFF)) << 16) |
                      (((uint32_t)((s_T0[(s1 >> 24) & 0xFF] >> 8) & 0xFF)) << 24);

        uint32_t t3 = ((uint32_t)((s_T0[s3 & 0xFF] >> 8) & 0xFF)) |
                      (((uint32_t)((s_T0[(s0 >> 8) & 0xFF] >> 8) & 0xFF)) << 8) |
                      (((uint32_t)((s_T0[(s1 >> 16) & 0xFF] >> 8) & 0xFF)) << 16) |
                      (((uint32_t)((s_T0[(s2 >> 24) & 0xFF] >> 8) & 0xFF)) << 24);

        *c0_out = t0 ^ s_RK[20];
        *c1_out = t1 ^ s_RK[21];
        *c2_out = t2 ^ s_RK[22];
        *c3_out = t3 ^ s_RK[23];
    }
}

/*
 * High-Throughput 5-Round AES CUDA Scanner Kernel
 * Executes grid-stride warp execution with Shared-Memory T-tables.
 * Collects 128 bit frequencies, Pearson correlation coincidences, and 128-bit XOR sum.
 */
__global__ void aes5r_scanner_kernel(
    uint64_t total_blocks,
    uint64_t counter_base,
    bool final_mix,
    uint64_t *d_bit_counts,     /* [128] */
    uint64_t *d_adj_counts,     /* [128] */
    uint64_t *d_cross_counts,   /* [32] */
    uint32_t *d_xor_sum         /* [4] */
) {
    __shared__ uint32_t s_T0[256];
    __shared__ uint32_t s_T1[256];
    __shared__ uint32_t s_T2[256];
    __shared__ uint32_t s_T3[256];
    __shared__ uint8_t  s_SBox[256];
    __shared__ uint32_t s_RK[24];

    int tid = threadIdx.x;
    if (tid < 256) {
        s_T0[tid]   = c_T0[tid];
        s_T1[tid]   = c_T1[tid];
        s_T2[tid]   = c_T2[tid];
        s_T3[tid]   = c_T3[tid];
        s_SBox[tid] = c_SBox[tid];
    }
    if (tid < 24) {
        s_RK[tid] = c_RoundKeys[tid];
    }
    __syncthreads();

    int lane = threadIdx.x & 31;
    int warp_in_block = threadIdx.x >> 5;
    int warps_per_block = blockDim.x >> 5;
    uint64_t global_warp_id = ((uint64_t)blockIdx.x * warps_per_block) + warp_in_block;
    uint64_t total_warps = (uint64_t)gridDim.x * warps_per_block;

    // Registers for warp statistical accumulation (lane-specific)
    uint64_t acc_bit[4] = {0, 0, 0, 0};
    uint64_t acc_adj[4] = {0, 0, 0, 0};
    uint64_t acc_cross  = 0;
    uint32_t local_xor[4] = {0, 0, 0, 0};

    uint64_t total_warp_blocks = (total_blocks + 31) / 32;

    for (uint64_t w = global_warp_id; w < total_warp_blocks; w += total_warps) {
        uint64_t block_idx = w * 32 + lane;
        bool active = (block_idx < total_blocks);

        uint32_t p0 = 0, p1 = 0, p2 = 0, p3 = 0;
        uint32_t c0 = 0, c1 = 0, c2 = 0, c3 = 0;

        if (active) {
            uint64_t ctr = counter_base + block_idx;
            p0 = (uint32_t)ctr;
            p1 = (uint32_t)(ctr >> 32);
            p2 = (uint32_t)(ctr * 0x9E3779B9u);
            p3 = (uint32_t)(ctr * 0xC2B2AE35u);

            aes_encrypt_5r_shmem(p0, p1, p2, p3, final_mix, s_T0, s_T1, s_T2, s_T3, s_SBox, s_RK, &c0, &c1, &c2, &c3);

            local_xor[0] ^= c0;
            local_xor[1] ^= c1;
            local_xor[2] ^= c2;
            local_xor[3] ^= c3;
        }

        // Parallel 32x32 Bit-Matrix Transposition for bit frequency counts
        uint32_t t0 = warp_transpose32(c0, lane);
        uint32_t t1 = warp_transpose32(c1, lane);
        uint32_t t2 = warp_transpose32(c2, lane);
        uint32_t t3 = warp_transpose32(c3, lane);

        acc_bit[0] += __popc(t0);
        acc_bit[1] += __popc(t1);
        acc_bit[2] += __popc(t2);
        acc_bit[3] += __popc(t3);

        // Adjacent bit coincidence: c & (c >> 1)
        uint32_t adj0 = warp_transpose32(c0 & (c0 >> 1), lane);
        uint32_t adj1 = warp_transpose32(c1 & (c1 >> 1), lane);
        uint32_t adj2 = warp_transpose32(c2 & (c2 >> 1), lane);
        uint32_t adj3 = warp_transpose32(c3 & (c3 >> 1), lane);

        acc_adj[0] += __popc(adj0);
        acc_adj[1] += __popc(adj1);
        acc_adj[2] += __popc(adj2);
        acc_adj[3] += __popc(adj3);

        // Cross-word bit coincidence between word 0 and word 1
        uint32_t cross01 = warp_transpose32(c0 & c1, lane);
        acc_cross += __popc(cross01);
    }

    // Shared memory reduction within thread block
    __shared__ uint64_t smem_bits[128];
    __shared__ uint64_t smem_adjs[128];
    __shared__ uint64_t smem_cross[32];

    if (threadIdx.x < 128) {
        smem_bits[threadIdx.x] = 0;
        smem_adjs[threadIdx.x] = 0;
    }
    if (threadIdx.x < 32) {
        smem_cross[threadIdx.x] = 0;
    }
    __syncthreads();

    // Accumulate lane registers into shared memory
    atomicAdd((unsigned long long*)&smem_bits[lane],       (unsigned long long)acc_bit[0]);
    atomicAdd((unsigned long long*)&smem_bits[lane + 32],  (unsigned long long)acc_bit[1]);
    atomicAdd((unsigned long long*)&smem_bits[lane + 64],  (unsigned long long)acc_bit[2]);
    atomicAdd((unsigned long long*)&smem_bits[lane + 96],  (unsigned long long)acc_bit[3]);

    atomicAdd((unsigned long long*)&smem_adjs[lane],       (unsigned long long)acc_adj[0]);
    atomicAdd((unsigned long long*)&smem_adjs[lane + 32],  (unsigned long long)acc_adj[1]);
    atomicAdd((unsigned long long*)&smem_adjs[lane + 64],  (unsigned long long)acc_adj[2]);
    atomicAdd((unsigned long long*)&smem_adjs[lane + 96],  (unsigned long long)acc_adj[3]);

    atomicAdd((unsigned long long*)&smem_cross[lane],      (unsigned long long)acc_cross);

    // Warp XOR reduction for block
    #pragma unroll
    for (int offset = 16; offset > 0; offset >>= 1) {
        local_xor[0] ^= __shfl_down_sync(0xFFFFFFFF, local_xor[0], offset);
        local_xor[1] ^= __shfl_down_sync(0xFFFFFFFF, local_xor[1], offset);
        local_xor[2] ^= __shfl_down_sync(0xFFFFFFFF, local_xor[2], offset);
        local_xor[3] ^= __shfl_down_sync(0xFFFFFFFF, local_xor[3], offset);
    }

    __shared__ uint32_t smem_xor[4];
    if (threadIdx.x == 0) {
        smem_xor[0] = 0; smem_xor[1] = 0; smem_xor[2] = 0; smem_xor[3] = 0;
    }
    __syncthreads();

    if (lane == 0) {
        atomicXor(&smem_xor[0], local_xor[0]);
        atomicXor(&smem_xor[1], local_xor[1]);
        atomicXor(&smem_xor[2], local_xor[2]);
        atomicXor(&smem_xor[3], local_xor[3]);
    }
    __syncthreads();

    // Final block-level commit to global memory
    if (threadIdx.x < 128) {
        atomicAdd((unsigned long long*)&d_bit_counts[threadIdx.x], (unsigned long long)smem_bits[threadIdx.x]);
        atomicAdd((unsigned long long*)&d_adj_counts[threadIdx.x], (unsigned long long)smem_adjs[threadIdx.x]);
    }
    if (threadIdx.x < 32) {
        atomicAdd((unsigned long long*)&d_cross_counts[threadIdx.x], (unsigned long long)smem_cross[threadIdx.x]);
    }
    if (threadIdx.x == 0) {
        atomicXor(&d_xor_sum[0], smem_xor[0]);
        atomicXor(&d_xor_sum[1], smem_xor[1]);
        atomicXor(&d_xor_sum[2], smem_xor[2]);
        atomicXor(&d_xor_sum[3], smem_xor[3]);
    }
}

/* Lambda-Set Single Block Kernel */
__global__ void aes_lambda_kernel(
    int rounds,
    bool final_mix,
    int active_byte,
    uint8_t const_val,
    uint8_t *d_xor_sum
) {
    int tid = threadIdx.x; // 0..255 (the 256 plaintexts of Lambda set)
    if (tid >= 256) return;

    // Construct plaintext for this Lambda set member
    uint8_t pt[16];
    for (int i = 0; i < 16; i++) {
        pt[i] = (i == active_byte) ? (uint8_t)tid : const_val;
    }

    uint32_t p0 = ((uint32_t)pt[0]) | (((uint32_t)pt[1]) << 8) | (((uint32_t)pt[2]) << 16) | (((uint32_t)pt[3]) << 24);
    uint32_t p1 = ((uint32_t)pt[4]) | (((uint32_t)pt[5]) << 8) | (((uint32_t)pt[6]) << 16) | (((uint32_t)pt[7]) << 24);
    uint32_t p2 = ((uint32_t)pt[8]) | (((uint32_t)pt[9]) << 8) | (((uint32_t)pt[10]) << 16) | (((uint32_t)pt[11]) << 24);
    uint32_t p3 = ((uint32_t)pt[12]) | (((uint32_t)pt[13]) << 8) | (((uint32_t)pt[14]) << 16) | (((uint32_t)pt[15]) << 24);

    uint32_t s0 = p0 ^ c_RoundKeys[0];
    uint32_t s1 = p1 ^ c_RoundKeys[1];
    uint32_t s2 = p2 ^ c_RoundKeys[2];
    uint32_t s3 = p3 ^ c_RoundKeys[3];

    for (int r = 1; r <= rounds; r++) {
        bool is_last = (r == rounds);
        if (!is_last || final_mix) {
            uint32_t t0 = c_T0[s0 & 0xFF] ^ c_T1[(s1 >> 8) & 0xFF] ^ c_T2[(s2 >> 16) & 0xFF] ^ c_T3[(s3 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 0];
            uint32_t t1 = c_T0[s1 & 0xFF] ^ c_T1[(s2 >> 8) & 0xFF] ^ c_T2[(s3 >> 16) & 0xFF] ^ c_T3[(s0 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 1];
            uint32_t t2 = c_T0[s2 & 0xFF] ^ c_T1[(s3 >> 8) & 0xFF] ^ c_T2[(s0 >> 16) & 0xFF] ^ c_T3[(s1 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 2];
            uint32_t t3 = c_T0[s3 & 0xFF] ^ c_T1[(s0 >> 8) & 0xFF] ^ c_T2[(s1 >> 16) & 0xFF] ^ c_T3[(s2 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 3];
            s0 = t0; s1 = t1; s2 = t2; s3 = t3;
        } else {
            uint32_t t0 = ((uint32_t)c_SBox[s0 & 0xFF]) | (((uint32_t)c_SBox[(s1 >> 8) & 0xFF]) << 8) | (((uint32_t)c_SBox[(s2 >> 16) & 0xFF]) << 16) | (((uint32_t)c_SBox[(s3 >> 24) & 0xFF]) << 24) ^ c_RoundKeys[4 * r + 0];
            uint32_t t1 = ((uint32_t)c_SBox[s1 & 0xFF]) | (((uint32_t)c_SBox[(s2 >> 8) & 0xFF]) << 8) | (((uint32_t)c_SBox[(s3 >> 16) & 0xFF]) << 16) | (((uint32_t)c_SBox[(s0 >> 24) & 0xFF]) << 24) ^ c_RoundKeys[4 * r + 1];
            uint32_t t2 = ((uint32_t)c_SBox[s2 & 0xFF]) | (((uint32_t)c_SBox[(s3 >> 8) & 0xFF]) << 8) | (((uint32_t)c_SBox[(s0 >> 16) & 0xFF]) << 16) | (((uint32_t)c_SBox[(s1 >> 24) & 0xFF]) << 24) ^ c_RoundKeys[4 * r + 2];
            uint32_t t3 = ((uint32_t)c_SBox[s3 & 0xFF]) | (((uint32_t)c_SBox[(s0 >> 8) & 0xFF]) << 8) | (((uint32_t)c_SBox[(s1 >> 16) & 0xFF]) << 16) | (((uint32_t)c_SBox[(s2 >> 24) & 0xFF]) << 24) ^ c_RoundKeys[4 * r + 3];
            s0 = t0; s1 = t1; s2 = t2; s3 = t3;
        }
    }

    // Warp-level XOR reduction
    #pragma unroll
    for (int offset = 16; offset > 0; offset >>= 1) {
        s0 ^= __shfl_down_sync(0xFFFFFFFF, s0, offset);
        s1 ^= __shfl_down_sync(0xFFFFFFFF, s1, offset);
        s2 ^= __shfl_down_sync(0xFFFFFFFF, s2, offset);
        s3 ^= __shfl_down_sync(0xFFFFFFFF, s3, offset);
    }

    __shared__ uint32_t smem_xor[4];
    if (threadIdx.x == 0) {
        smem_xor[0] = 0; smem_xor[1] = 0; smem_xor[2] = 0; smem_xor[3] = 0;
    }
    __syncthreads();

    if ((threadIdx.x & 31) == 0) {
        atomicXor(&smem_xor[0], s0);
        atomicXor(&smem_xor[1], s1);
        atomicXor(&smem_xor[2], s2);
        atomicXor(&smem_xor[3], s3);
    }
    __syncthreads();

    if (threadIdx.x == 0) {
        d_xor_sum[0]  = (uint8_t)smem_xor[0];
        d_xor_sum[1]  = (uint8_t)(smem_xor[0] >> 8);
        d_xor_sum[2]  = (uint8_t)(smem_xor[0] >> 16);
        d_xor_sum[3]  = (uint8_t)(smem_xor[0] >> 24);

        d_xor_sum[4]  = (uint8_t)smem_xor[1];
        d_xor_sum[5]  = (uint8_t)(smem_xor[1] >> 8);
        d_xor_sum[6]  = (uint8_t)(smem_xor[1] >> 16);
        d_xor_sum[7]  = (uint8_t)(smem_xor[1] >> 24);

        d_xor_sum[8]  = (uint8_t)smem_xor[2];
        d_xor_sum[9]  = (uint8_t)(smem_xor[2] >> 8);
        d_xor_sum[10] = (uint8_t)(smem_xor[2] >> 16);
        d_xor_sum[11] = (uint8_t)(smem_xor[2] >> 24);

        d_xor_sum[12] = (uint8_t)smem_xor[3];
        d_xor_sum[13] = (uint8_t)(smem_xor[3] >> 8);
        d_xor_sum[14] = (uint8_t)(smem_xor[3] >> 16);
        d_xor_sum[15] = (uint8_t)(smem_xor[3] >> 24);
    }
}

int aes_cuda_scan_lambda(
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    int active_byte,
    uint8_t const_val,
    uint8_t xor_sum[16],
    bool *is_balanced
) {
    if (!g_cuda_initialized) {
        if (aes_cuda_init(0) != 0) return -1;
    }

    uint32_t rks[15 * 4];
    host_expand_key(key, rounds, rks);
    cudaMemcpyToSymbol(c_RoundKeys, rks, (rounds + 1) * 4 * sizeof(uint32_t));

    uint8_t *d_xor;
    cudaMalloc(&d_xor, 16);
    cudaMemset(d_xor, 0, 16);

    aes_lambda_kernel<<<1, 256>>>(rounds, final_mix, active_byte, const_val, d_xor);
    cudaDeviceSynchronize();

    cudaMemcpy(xor_sum, d_xor, 16, cudaMemcpyDeviceToHost);
    cudaFree(d_xor);

    bool bal = true;
    for (int i = 0; i < 16; i++) {
        if (xor_sum[i] != 0) {
            bal = false;
            break;
        }
    }
    if (is_balanced) *is_balanced = bal;
    return 0;
}

int aes_cuda_scan_5r(
    const aes_cuda_config_t *config,
    aes_cuda_stats_t *stats
) {
    if (!config || !stats) return -1;
    if (!g_cuda_initialized) {
        if (aes_cuda_init(0) != 0) return -2;
    }

    uint64_t total_blocks = config->total_blocks;
    if (total_blocks == 0) total_blocks = (1ULL << 26); // Default 67,108,864 blocks (1 GiB)

    uint32_t threads_per_block = config->threads_per_block ? config->threads_per_block : 256;
    uint32_t num_blocks_grid   = config->num_blocks_grid   ? config->num_blocks_grid   : 224; // 28 SMs * 8 blocks/SM

    // Expand key and upload to constant memory
    uint32_t rks[15 * 4];
    host_expand_key(config->key, 5, rks);
    cudaMemcpyToSymbol(c_RoundKeys, rks, 6 * 4 * sizeof(uint32_t));

    // Device memory allocation
    uint64_t *d_bit_counts;
    uint64_t *d_adj_counts;
    uint64_t *d_cross_counts;
    uint32_t *d_xor_sum;

    cudaMalloc(&d_bit_counts,   128 * sizeof(uint64_t));
    cudaMalloc(&d_adj_counts,   128 * sizeof(uint64_t));
    cudaMalloc(&d_cross_counts, 32  * sizeof(uint64_t));
    cudaMalloc(&d_xor_sum,      4   * sizeof(uint32_t));

    cudaMemset(d_bit_counts,   0, 128 * sizeof(uint64_t));
    cudaMemset(d_adj_counts,   0, 128 * sizeof(uint64_t));
    cudaMemset(d_cross_counts, 0, 32  * sizeof(uint64_t));
    cudaMemset(d_xor_sum,      0, 4   * sizeof(uint32_t));

    // CUDA Events for precise timing
    cudaEvent_t start, stop;
    cudaEventCreate(&start);
    cudaEventCreate(&stop);

    cudaEventRecord(start);

    aes5r_scanner_kernel<<<num_blocks_grid, threads_per_block>>>(
        total_blocks,
        config->nonce_or_counter,
        config->final_mix,
        d_bit_counts,
        d_adj_counts,
        d_cross_counts,
        d_xor_sum
    );

    cudaEventRecord(stop);
    cudaEventSynchronize(stop);

    float ms = 0.0f;
    cudaEventElapsedTime(&ms, start, stop);

    cudaEventDestroy(start);
    cudaEventDestroy(stop);

    // Copy results back to host
    uint64_t h_bit_counts[128];
    uint64_t h_adj_counts[128];
    uint64_t h_cross_counts[32];
    uint32_t h_xor_sum[4];

    cudaMemcpy(h_bit_counts,   d_bit_counts,   128 * sizeof(uint64_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(h_adj_counts,   d_adj_counts,   128 * sizeof(uint64_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(h_cross_counts, d_cross_counts, 32  * sizeof(uint64_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(h_xor_sum,      d_xor_sum,      4   * sizeof(uint32_t), cudaMemcpyDeviceToHost);

    cudaFree(d_bit_counts);
    cudaFree(d_adj_counts);
    cudaFree(d_cross_counts);
    cudaFree(d_xor_sum);

    // Compute metrics
    double elapsed_sec = (double)ms / 1000.0;
    double total_bytes = (double)total_blocks * 16.0;

    stats->total_blocks_processed = total_blocks;
    stats->elapsed_seconds        = elapsed_sec;
    stats->throughput_gb_s        = (total_bytes / 1e9) / elapsed_sec;
    stats->throughput_gib_s       = (total_bytes / (1024.0 * 1024.0 * 1024.0)) / elapsed_sec;
    stats->throughput_mblocks_s   = ((double)total_blocks / 1e6) / elapsed_sec;

    // Bit-slice frequencies & Chi-squared test
    double chi2 = 0.0;
    double N = (double)total_blocks;
    for (int i = 0; i < 128; i++) {
        stats->bit_counts[i] = h_bit_counts[i];
        double p = (double)h_bit_counts[i] / N;
        stats->bit_proportions[i] = p;

        double diff = 2.0 * (double)h_bit_counts[i] - N;
        chi2 += (diff * diff) / N;
    }

    stats->chi_squared_128     = chi2;
    stats->chi_squared_dof     = 128.0;
    stats->chi_squared_z_score = (chi2 - 128.0) / sqrt(256.0); // Z = (chi2 - nu) / sqrt(2*nu)
    // Complementary error function for standard normal p-value
    stats->chi_squared_p_value = 0.5 * erfc(fabs(stats->chi_squared_z_score) / sqrt(2.0));

    // Pearson correlation: Adjacent bits
    double max_adj_r = 0.0;
    double sum_adj_r = 0.0;
    for (int i = 0; i < 128; i++) {
        int bit_in_word = i % 32;
        double r = 0.0;
        if (bit_in_word < 31) {
            uint64_t n_x  = h_bit_counts[i];
            uint64_t n_y  = h_bit_counts[i + 1];
            uint64_t n_xy = h_adj_counts[i];

            double num = N * (double)n_xy - (double)n_x * (double)n_y;
            double den = sqrt((double)n_x * (N - (double)n_x) * (double)n_y * (N - (double)n_y));
            if (den > 0.0) r = num / den;
        }
        stats->adjacent_bit_corr[i] = r;
        if (fabs(r) > max_adj_r) max_adj_r = fabs(r);
        sum_adj_r += fabs(r);
    }
    stats->max_adjacent_bit_corr  = max_adj_r;
    stats->mean_adjacent_bit_corr = sum_adj_r / 128.0;

    // Cross-word correlation (word 0 and word 1)
    double max_cross_r = 0.0;
    for (int i = 0; i < 32; i++) {
        uint64_t n_x  = h_bit_counts[i];
        uint64_t n_y  = h_bit_counts[i + 32];
        uint64_t n_xy = h_cross_counts[i];

        double num = N * (double)n_xy - (double)n_x * (double)n_y;
        double den = sqrt((double)n_x * (N - (double)n_x) * (double)n_y * (N - (double)n_y));
        double r = (den > 0.0) ? (num / den) : 0.0;
        stats->cross_word_corr[i] = r;
        if (fabs(r) > max_cross_r) max_cross_r = fabs(r);
    }
    stats->max_cross_word_corr = max_cross_r;

    // XOR sum unpack & balance check
    for (int c = 0; c < 4; c++) {
        stats->xor_sum[4 * c + 0] = (uint8_t)h_xor_sum[c];
        stats->xor_sum[4 * c + 1] = (uint8_t)(h_xor_sum[c] >> 8);
        stats->xor_sum[4 * c + 2] = (uint8_t)(h_xor_sum[c] >> 16);
        stats->xor_sum[4 * c + 3] = (uint8_t)(h_xor_sum[c] >> 24);
    }

    bool bal = true;
    for (int i = 0; i < 16; i++) {
        if (stats->xor_sum[i] != 0) {
            bal = false;
            break;
        }
    }
    stats->is_zero_sum_balanced = bal;

    // Compute approximate Shannon entropy for 16 byte coordinates from bit proportions
    for (int byte_idx = 0; byte_idx < 16; byte_idx++) {
        double byte_h = 0.0;
        for (int b = 0; b < 8; b++) {
            double p = stats->bit_proportions[byte_idx * 8 + b];
            if (p > 0.0 && p < 1.0) {
                byte_h += -p * log2(p) - (1.0 - p) * log2(1.0 - p);
            } else {
                byte_h += 1.0;
            }
        }
        stats->byte_entropy[byte_idx] = byte_h;
    }

    return 0;
}

/* Reference Verification Kernel */
__global__ void aes_reference_verify_kernel(
    const uint8_t *d_inputs,
    bool final_mix,
    size_t num_samples,
    uint8_t *d_outputs
) {
    size_t idx = (size_t)blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= num_samples) return;

    const uint8_t *in = d_inputs + idx * 16;
    uint32_t p0 = ((uint32_t)in[0]) | (((uint32_t)in[1]) << 8) | (((uint32_t)in[2]) << 16) | (((uint32_t)in[3]) << 24);
    uint32_t p1 = ((uint32_t)in[4]) | (((uint32_t)in[5]) << 8) | (((uint32_t)in[6]) << 16) | (((uint32_t)in[7]) << 24);
    uint32_t p2 = ((uint32_t)in[8]) | (((uint32_t)in[9]) << 8) | (((uint32_t)in[10]) << 16) | (((uint32_t)in[11]) << 24);
    uint32_t p3 = ((uint32_t)in[12]) | (((uint32_t)in[13]) << 8) | (((uint32_t)in[14]) << 16) | (((uint32_t)in[15]) << 24);

    uint32_t c0, c1, c2, c3;
    aes_encrypt_5r_core(p0, p1, p2, p3, final_mix, &c0, &c1, &c2, &c3);

    uint8_t *out = d_outputs + idx * 16;
    out[0]  = (uint8_t)c0;
    out[1]  = (uint8_t)(c0 >> 8);
    out[2]  = (uint8_t)(c0 >> 16);
    out[3]  = (uint8_t)(c0 >> 24);

    out[4]  = (uint8_t)c1;
    out[5]  = (uint8_t)(c1 >> 8);
    out[6]  = (uint8_t)(c1 >> 16);
    out[7]  = (uint8_t)(c1 >> 24);

    out[8]  = (uint8_t)c2;
    out[9]  = (uint8_t)(c2 >> 8);
    out[10] = (uint8_t)(c2 >> 16);
    out[11] = (uint8_t)(c2 >> 24);

    out[12] = (uint8_t)c3;
    out[13] = (uint8_t)(c3 >> 8);
    out[14] = (uint8_t)(c3 >> 16);
    out[15] = (uint8_t)(c3 >> 24);
}

static void host_aes_mix_columns(uint8_t state[16]) {
    for (int c = 0; c < 4; c++) {
        int idx = c * 4;
        uint8_t s0 = state[idx];
        uint8_t s1 = state[idx + 1];
        uint8_t s2 = state[idx + 2];
        uint8_t s3 = state[idx + 3];

        state[idx]     = (uint8_t)(host_xtime(s0) ^ host_xtime(s1) ^ s1 ^ s2 ^ s3);
        state[idx + 1] = (uint8_t)(s0 ^ host_xtime(s1) ^ host_xtime(s2) ^ s2 ^ s3);
        state[idx + 2] = (uint8_t)(s0 ^ s1 ^ host_xtime(s2) ^ host_xtime(s3) ^ s3);
        state[idx + 3] = (uint8_t)(host_xtime(s0) ^ s0 ^ s1 ^ s2 ^ host_xtime(s3));
    }
}

static void host_aes_encrypt_5r(
    const uint8_t in[16],
    const uint32_t rks[15 * 4],
    bool final_mix,
    uint8_t out[16]
) {
    uint8_t state[16];
    memcpy(state, in, 16);

    // Round 0
    for (int c = 0; c < 4; c++) {
        uint32_t rk = rks[c];
        state[4 * c + 0] ^= (uint8_t)rk;
        state[4 * c + 1] ^= (uint8_t)(rk >> 8);
        state[4 * c + 2] ^= (uint8_t)(rk >> 16);
        state[4 * c + 3] ^= (uint8_t)(rk >> 24);
    }

    for (int r = 1; r <= 5; r++) {
        // SubBytes
        for (int i = 0; i < 16; i++) {
            state[i] = kSBoxHost[state[i]];
        }
        // ShiftRows
        uint8_t r1_1  = state[5],  r1_5  = state[9],  r1_9  = state[13], r1_13 = state[1];
        uint8_t r2_2  = state[10], r2_6  = state[14], r2_10 = state[2],  r2_14 = state[6];
        uint8_t r3_3  = state[15], r3_7  = state[3],  r3_11 = state[7],  r3_15 = state[11];
        state[1]  = r1_1;  state[5]  = r1_5;  state[9]  = r1_9;  state[13] = r1_13;
        state[2]  = r2_2;  state[6]  = r2_6;  state[10] = r2_10; state[14] = r2_14;
        state[3]  = r3_3;  state[7]  = r3_7;  state[11] = r3_11; state[15] = r3_15;

        // MixColumns
        if (r < 5 || final_mix) {
            host_aes_mix_columns(state);
        }

        // AddRoundKey
        for (int c = 0; c < 4; c++) {
            uint32_t rk = rks[4 * r + c];
            state[4 * c + 0] ^= (uint8_t)rk;
            state[4 * c + 1] ^= (uint8_t)(rk >> 8);
            state[4 * c + 2] ^= (uint8_t)(rk >> 16);
            state[4 * c + 3] ^= (uint8_t)(rk >> 24);
        }
    }
    memcpy(out, state, 16);
}

int aes_cuda_verify_reference(
    const uint8_t key[16],
    bool final_mix,
    size_t num_samples
) {
    if (!g_cuda_initialized) {
        if (aes_cuda_init(0) != 0) return -1;
    }

    uint32_t rks[15 * 4];
    host_expand_key(key, 5, rks);
    cudaMemcpyToSymbol(c_RoundKeys, rks, 6 * 4 * sizeof(uint32_t));

    uint8_t *h_in  = (uint8_t*)malloc(num_samples * 16);
    uint8_t *h_out = (uint8_t*)malloc(num_samples * 16);

    for (size_t i = 0; i < num_samples * 16; i++) {
        h_in[i] = (uint8_t)((i * 37 + 101) & 0xFF);
    }

    uint8_t *d_in, *d_out;
    cudaMalloc(&d_in,  num_samples * 16);
    cudaMalloc(&d_out, num_samples * 16);

    cudaMemcpy(d_in, h_in, num_samples * 16, cudaMemcpyHostToDevice);

    size_t tpb = 256;
    size_t bpg = (num_samples + tpb - 1) / tpb;
    aes_reference_verify_kernel<<<bpg, tpb>>>(d_in, final_mix, num_samples, d_out);
    cudaDeviceSynchronize();

    cudaMemcpy(h_out, d_out, num_samples * 16, cudaMemcpyDeviceToHost);

    int mismatches = 0;
    for (size_t i = 0; i < num_samples; i++) {
        uint8_t exp[16];
        host_aes_encrypt_5r(h_in + i * 16, rks, final_mix, exp);
        if (memcmp(exp, h_out + i * 16, 16) != 0) {
            mismatches++;
            if (mismatches <= 5) {
                printf("    [!] Mismatch at sample %zu: GPU != CPU\n", i);
            }
        }
    }

    cudaFree(d_in);
    cudaFree(d_out);
    free(h_in);
    free(h_out);
    return mismatches;
}
