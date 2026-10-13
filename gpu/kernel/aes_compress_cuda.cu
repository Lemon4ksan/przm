/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "aes_compress_cuda.h"
#include <cuda_runtime.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

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

static bool g_compress_cuda_initialized = false;

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

/* Device AES-128 Encryption for R rounds */
__device__ __forceinline__ void aes_encrypt_r_rounds(
    uint32_t p0, uint32_t p1, uint32_t p2, uint32_t p3,
    uint32_t rounds,
    uint32_t *c0, uint32_t *c1, uint32_t *c2, uint32_t *c3
) {
    uint32_t s0 = p0 ^ c_RoundKeys[0];
    uint32_t s1 = p1 ^ c_RoundKeys[1];
    uint32_t s2 = p2 ^ c_RoundKeys[2];
    uint32_t s3 = p3 ^ c_RoundKeys[3];

    for (uint32_t r = 1; r <= rounds; r++) {
        uint32_t t0 = c_T0[s0 & 0xFF] ^ c_T1[(s1 >> 8) & 0xFF] ^ c_T2[(s2 >> 16) & 0xFF] ^ c_T3[(s3 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 0];
        uint32_t t1 = c_T0[s1 & 0xFF] ^ c_T1[(s2 >> 8) & 0xFF] ^ c_T2[(s3 >> 16) & 0xFF] ^ c_T3[(s0 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 1];
        uint32_t t2 = c_T0[s2 & 0xFF] ^ c_T1[(s3 >> 8) & 0xFF] ^ c_T2[(s0 >> 16) & 0xFF] ^ c_T3[(s1 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 2];
        uint32_t t3 = c_T0[s3 & 0xFF] ^ c_T1[(s0 >> 8) & 0xFF] ^ c_T2[(s1 >> 16) & 0xFF] ^ c_T3[(s2 >> 24) & 0xFF] ^ c_RoundKeys[4 * r + 3];
        s0 = t0; s1 = t1; s2 = t2; s3 = t3;
    }
    *c0 = s0; *c1 = s1; *c2 = s2; *c3 = s3;
}

/* Multi-Round Synthetic Stream Generation Kernel */
__global__ void aes_generate_stream_kernel(
    uint32_t rounds,
    uint64_t total_chunks,
    uint8_t *d_out_raw
) {
    uint64_t total_blocks = total_chunks * 256;
    uint64_t gid = (uint64_t)blockIdx.x * blockDim.x + threadIdx.x;
    uint64_t stride = (uint64_t)gridDim.x * blockDim.x;

    for (uint64_t b = gid; b < total_blocks; b += stride) {
        uint64_t chunk_id = b >> 8;
        uint32_t lambda_idx = (uint32_t)(b & 0xFF);

        uint8_t const_val = (uint8_t)((chunk_id * 17ULL + 0xAA) & 0xFF);
        uint32_t p0 = ((uint32_t)lambda_idx) | (((uint32_t)const_val) << 8) | (((uint32_t)const_val) << 16) | (((uint32_t)const_val) << 24);
        uint32_t c_word = ((uint32_t)const_val) | (((uint32_t)const_val) << 8) | (((uint32_t)const_val) << 16) | (((uint32_t)const_val) << 24);
        uint32_t p1 = c_word;
        uint32_t p2 = c_word;
        uint32_t p3 = c_word;

        uint32_t c0, c1, c2, c3;
        aes_encrypt_r_rounds(p0, p1, p2, p3, rounds, &c0, &c1, &c2, &c3);

        ulonglong2 *out_ptr = (ulonglong2 *)(d_out_raw + b * 16);
        ulonglong2 val;
        val.x = ((uint64_t)c0) | (((uint64_t)c1) << 32);
        val.y = ((uint64_t)c2) | (((uint64_t)c3) << 32);
        *out_ptr = val;
    }
}

/* Chunk Balance Verification Kernel (Parallel Warp Reduction) */
__global__ void aes_verify_chunk_balance_kernel(
    const ulonglong2 * __restrict__ raw_blocks,
    uint64_t total_chunks,
    unsigned long long *d_num_violations,
    int *d_has_recorded_violation,
    uint64_t *d_first_violation_chunk,
    unsigned long long *d_first_violation_parity
) {
    uint32_t warp_id_in_grid = (blockIdx.x * (blockDim.x >> 5)) + (threadIdx.x >> 5);
    uint32_t lane_id = threadIdx.x & 31;
    uint32_t total_warps = gridDim.x * (blockDim.x >> 5);

    for (uint64_t chunk_idx = warp_id_in_grid; chunk_idx < total_chunks; chunk_idx += total_warps) {
        uint64_t chunk_base = chunk_idx * 256;
        ulonglong2 acc = make_ulonglong2(0, 0);

        #pragma unroll
        for (int j = 0; j < 8; j++) {
            int block_idx = (j << 5) | lane_id;
            ulonglong2 b = raw_blocks[chunk_base + block_idx];
            acc.x ^= b.x;
            acc.y ^= b.y;
        }

        #pragma unroll
        for (int offset = 16; offset > 0; offset >>= 1) {
            acc.x ^= __shfl_xor_sync(0xFFFFFFFF, acc.x, offset);
            acc.y ^= __shfl_xor_sync(0xFFFFFFFF, acc.y, offset);
        }

        if (lane_id == 0) {
            if (acc.x != 0 || acc.y != 0) {
                atomicAdd(d_num_violations, 1ULL);
                if (atomicCAS(d_has_recorded_violation, 0, 1) == 0) {
                    *d_first_violation_chunk = chunk_idx;
                    d_first_violation_parity[0] = acc.x;
                    d_first_violation_parity[1] = acc.y;
                }
            }
        }
    }
}

/* Stream Compression Kernel (Extract 255 blocks per 256-block chunk) */
__global__ void aes_compress_stream_kernel(
    const ulonglong2 * __restrict__ raw_blocks,
    uint64_t total_chunks,
    ulonglong2 * __restrict__ comp_blocks
) {
    uint32_t warp_id_in_grid = (blockIdx.x * (blockDim.x >> 5)) + (threadIdx.x >> 5);
    uint32_t lane_id = threadIdx.x & 31;
    uint32_t total_warps = gridDim.x * (blockDim.x >> 5);

    for (uint64_t chunk_idx = warp_id_in_grid; chunk_idx < total_chunks; chunk_idx += total_warps) {
        uint64_t raw_base  = chunk_idx * 256;
        uint64_t comp_base = chunk_idx * 255;

        #pragma unroll
        for (int j = 0; j < 8; j++) {
            int block_idx = (j << 5) | lane_id;
            if (block_idx < 255) {
                comp_blocks[comp_base + block_idx] = raw_blocks[raw_base + block_idx];
            }
        }
    }
}

/* Stream Decompression / Parallel Parity Reconstruction Kernel */
__global__ void aes_decompress_stream_kernel(
    const ulonglong2 * __restrict__ comp_blocks,
    uint64_t total_chunks,
    ulonglong2 * __restrict__ rec_blocks
) {
    uint32_t warp_id_in_grid = (blockIdx.x * (blockDim.x >> 5)) + (threadIdx.x >> 5);
    uint32_t lane_id = threadIdx.x & 31;
    uint32_t total_warps = gridDim.x * (blockDim.x >> 5);

    for (uint64_t chunk_idx = warp_id_in_grid; chunk_idx < total_chunks; chunk_idx += total_warps) {
        uint64_t comp_base = chunk_idx * 255;
        uint64_t rec_base  = chunk_idx * 256;
        ulonglong2 acc = make_ulonglong2(0, 0);

        #pragma unroll
        for (int j = 0; j < 8; j++) {
            int block_idx = (j << 5) | lane_id;
            if (block_idx < 255) {
                ulonglong2 val = comp_blocks[comp_base + block_idx];
                rec_blocks[rec_base + block_idx] = val;
                acc.x ^= val.x;
                acc.y ^= val.y;
            }
        }

        #pragma unroll
        for (int offset = 16; offset > 0; offset >>= 1) {
            acc.x ^= __shfl_xor_sync(0xFFFFFFFF, acc.x, offset);
            acc.y ^= __shfl_xor_sync(0xFFFFFFFF, acc.y, offset);
        }

        if (lane_id == 0) {
            rec_blocks[rec_base + 255] = acc;
        }
    }
}

/* Verification of Bit-Exact Reconstructed Stream */
__global__ void aes_verify_match_kernel(
    const ulonglong2 * __restrict__ raw_blocks,
    const ulonglong2 * __restrict__ rec_blocks,
    uint64_t total_128b_words,
    unsigned long long *d_mismatches
) {
    uint64_t gid = (uint64_t)blockIdx.x * blockDim.x + threadIdx.x;
    uint64_t stride = (uint64_t)gridDim.x * blockDim.x;

    for (uint64_t i = gid; i < total_128b_words; i += stride) {
        ulonglong2 a = raw_blocks[i];
        ulonglong2 b = rec_blocks[i];
        if (a.x != b.x || a.y != b.y) {
            atomicAdd(d_mismatches, 1ULL);
        }
    }
}

int aes_compress_cuda_init(int device_id) {
    cudaError_t err = cudaSetDevice(device_id);
    if (err != cudaSuccess) return -1;

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

    const uint8_t default_key[16] = {
        0x54, 0x68, 0x61, 0x74, 0x73, 0x20, 0x6D, 0x79,
        0x20, 0x4B, 0x75, 0x6E, 0x67, 0x20, 0x46, 0x75
    };
    uint32_t rks[15 * 4];
    host_expand_key(default_key, 10, rks);
    cudaMemcpyToSymbol(c_RoundKeys, rks, 11 * 4 * sizeof(uint32_t));

    g_compress_cuda_initialized = true;
    return 0;
}

int aes_compress_cuda_generate_stream(
    uint32_t rounds,
    const uint8_t key[16],
    uint64_t num_chunks,
    uint8_t *d_out_raw,
    double *elapsed_ms,
    double *gb_per_sec
) {
    if (!g_compress_cuda_initialized) aes_compress_cuda_init(0);

    if (key) {
        uint32_t rks[15 * 4];
        host_expand_key(key, (int)rounds, rks);
        cudaMemcpyToSymbol(c_RoundKeys, rks, (rounds + 1) * 4 * sizeof(uint32_t));
    }

    uint32_t threads = 256;
    uint32_t blocks = 896;

    cudaEvent_t start, stop;
    cudaEventCreate(&start);
    cudaEventCreate(&stop);

    cudaEventRecord(start);
    aes_generate_stream_kernel<<<blocks, threads>>>(rounds, num_chunks, d_out_raw);
    cudaEventRecord(stop);
    cudaEventSynchronize(stop);

    float ms = 0.0f;
    cudaEventElapsedTime(&ms, start, stop);
    cudaEventDestroy(start);
    cudaEventDestroy(stop);

    double sec = (double)ms / 1000.0;
    double raw_bytes = (double)(num_chunks * AES_CHUNK_RAW_BYTES);
    if (elapsed_ms) *elapsed_ms = ms;
    if (gb_per_sec) *gb_per_sec = (raw_bytes / sec) / 1e9;

    return 0;
}

int aes_compress_cuda_verify_balance(
    const uint8_t *d_raw,
    uint64_t num_chunks,
    bool *h_is_balanced,
    uint64_t *h_num_violations,
    uint64_t *h_first_violation_chunk,
    uint8_t h_first_violation_parity[16],
    double *elapsed_ms,
    double *gb_per_sec
) {
    unsigned long long *d_violations;
    int *d_recorded;
    uint64_t *d_chunk_idx;
    unsigned long long *d_parity;

    cudaMalloc(&d_violations, sizeof(unsigned long long));
    cudaMalloc(&d_recorded, sizeof(int));
    cudaMalloc(&d_chunk_idx, sizeof(uint64_t));
    cudaMalloc(&d_parity, 2 * sizeof(unsigned long long));

    cudaMemset(d_violations, 0, sizeof(unsigned long long));
    cudaMemset(d_recorded, 0, sizeof(int));
    cudaMemset(d_chunk_idx, 0, sizeof(uint64_t));
    cudaMemset(d_parity, 0, 2 * sizeof(unsigned long long));

    uint32_t threads = 256;
    uint32_t blocks = 896;

    cudaEvent_t start, stop;
    cudaEventCreate(&start);
    cudaEventCreate(&stop);

    cudaEventRecord(start);
    aes_verify_chunk_balance_kernel<<<blocks, threads>>>(
        (const ulonglong2 *)d_raw,
        num_chunks,
        d_violations,
        d_recorded,
        d_chunk_idx,
        d_parity
    );
    cudaEventRecord(stop);
    cudaEventSynchronize(stop);

    float ms = 0.0f;
    cudaEventElapsedTime(&ms, start, stop);
    cudaEventDestroy(start);
    cudaEventDestroy(stop);

    unsigned long long violations = 0;
    uint64_t first_chunk = 0;
    unsigned long long parity[2] = {0, 0};

    cudaMemcpy(&violations, d_violations, sizeof(unsigned long long), cudaMemcpyDeviceToHost);
    cudaMemcpy(&first_chunk, d_chunk_idx, sizeof(uint64_t), cudaMemcpyDeviceToHost);
    cudaMemcpy(parity, d_parity, 2 * sizeof(unsigned long long), cudaMemcpyDeviceToHost);

    cudaFree(d_violations);
    cudaFree(d_recorded);
    cudaFree(d_chunk_idx);
    cudaFree(d_parity);

    if (h_is_balanced) *h_is_balanced = (violations == 0);
    if (h_num_violations) *h_num_violations = violations;
    if (h_first_violation_chunk) *h_first_violation_chunk = first_chunk;
    if (h_first_violation_parity) memcpy(h_first_violation_parity, parity, 16);

    double sec = (double)ms / 1000.0;
    double raw_bytes = (double)(num_chunks * AES_CHUNK_RAW_BYTES);
    if (elapsed_ms) *elapsed_ms = ms;
    if (gb_per_sec) *gb_per_sec = (raw_bytes / sec) / 1e9;

    return 0;
}

int aes_compress_cuda_compress(
    const uint8_t *d_raw,
    uint64_t num_chunks,
    uint8_t *d_comp,
    double *elapsed_ms,
    double *gb_per_sec
) {
    uint32_t threads = 256;
    uint32_t blocks = 896;

    cudaEvent_t start, stop;
    cudaEventCreate(&start);
    cudaEventCreate(&stop);

    cudaEventRecord(start);
    aes_compress_stream_kernel<<<blocks, threads>>>(
        (const ulonglong2 *)d_raw,
        num_chunks,
        (ulonglong2 *)d_comp
    );
    cudaEventRecord(stop);
    cudaEventSynchronize(stop);

    float ms = 0.0f;
    cudaEventElapsedTime(&ms, start, stop);
    cudaEventDestroy(start);
    cudaEventDestroy(stop);

    double sec = (double)ms / 1000.0;
    double raw_bytes = (double)(num_chunks * AES_CHUNK_RAW_BYTES);
    if (elapsed_ms) *elapsed_ms = ms;
    if (gb_per_sec) *gb_per_sec = (raw_bytes / sec) / 1e9;

    return 0;
}

int aes_compress_cuda_decompress(
    const uint8_t *d_comp,
    uint64_t num_chunks,
    uint8_t *d_reconstructed,
    double *elapsed_ms,
    double *gb_per_sec
) {
    uint32_t threads = 256;
    uint32_t blocks = 896;

    cudaEvent_t start, stop;
    cudaEventCreate(&start);
    cudaEventCreate(&stop);

    cudaEventRecord(start);
    aes_decompress_stream_kernel<<<blocks, threads>>>(
        (const ulonglong2 *)d_comp,
        num_chunks,
        (ulonglong2 *)d_reconstructed
    );
    cudaEventRecord(stop);
    cudaEventSynchronize(stop);

    float ms = 0.0f;
    cudaEventElapsedTime(&ms, start, stop);
    cudaEventDestroy(start);
    cudaEventDestroy(stop);

    double sec = (double)ms / 1000.0;
    double raw_bytes = (double)(num_chunks * AES_CHUNK_RAW_BYTES);
    if (elapsed_ms) *elapsed_ms = ms;
    if (gb_per_sec) *gb_per_sec = (raw_bytes / sec) / 1e9;

    return 0;
}

int aes_compress_cuda_verify_match(
    const uint8_t *d_raw,
    const uint8_t *d_reconstructed,
    uint64_t total_bytes,
    bool *h_match,
    uint64_t *h_mismatches
) {
    uint64_t total_words = total_bytes / 16;
    unsigned long long *d_mismatches;
    cudaMalloc(&d_mismatches, sizeof(unsigned long long));
    cudaMemset(d_mismatches, 0, sizeof(unsigned long long));

    uint32_t threads = 256;
    uint32_t blocks = 896;

    aes_verify_match_kernel<<<blocks, threads>>>(
        (const ulonglong2 *)d_raw,
        (const ulonglong2 *)d_reconstructed,
        total_words,
        d_mismatches
    );
    cudaDeviceSynchronize();

    unsigned long long mismatches = 0;
    cudaMemcpy(&mismatches, d_mismatches, sizeof(unsigned long long), cudaMemcpyDeviceToHost);
    cudaFree(d_mismatches);

    if (h_match) *h_match = (mismatches == 0);
    if (h_mismatches) *h_mismatches = mismatches;

    return 0;
}

void aes_compress_cuda_cleanup(void) {
    cudaDeviceSynchronize();
}
