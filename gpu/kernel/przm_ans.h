/**
 * przm_ans.h - C/CUDA interface header for PRZM GPU Entropy Encoding Kernels
 * 
 * Hardware Target: NVIDIA Ampere GA106 (GeForce RTX 3060, SM 8.6, 28 SMs, 12 GB VRAM)
 * Pillar: Massively Parallel Vector-State Asymmetric Numeral Systems (V-ANS) & Fast PackStream
 * 
 * Target: >20 GB/s internal entropy encoding throughput.
 * 
 * Copyright (c) 2026 PRZM GPU Project. All rights reserved.
 */

#ifndef PRZM_ANS_H
#define PRZM_ANS_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

#define PRZM_ANS_M                4096U     /* ANS table precision M = 2^12 */
#define PRZM_ANS_L                65536U    /* ANS lower state bound L = 2^16 */
#define PRZM_ANS_NUM_SYMBOLS      256U      /* Byte alphabet size */

#define PRZM_VANS_MAGIC0          0x56U     /* 'V' */
#define PRZM_VANS_MAGIC1          0x41U     /* 'A' */
#define PRZM_VANS_VER1            0x01U
#define PRZM_VANS_FLAG_4X         0x01U     /* 4-way interleaved SIMD/warp streams */

#define PRZM_STREAM_MODE_FLAT     0U        /* ModeFlat: 0x00 | origLen(4B) | payload */
#define PRZM_STREAM_MODE_HUFF4X   1U        /* ModeHuffman4X */

#define PRZM_HIST_BLOCK_SIZE      256
#define PRZM_HIST_WARPS_PER_BLOCK 8         /* 256 / 32 = 8 warps */
#define PRZM_ANS_MAX_STREAMS      128

#if defined(__CUDACC__) || defined(__CUDA_ARCH__)
#define PRZM_ANS_HD __host__ __device__
#else
#define PRZM_ANS_HD
#endif

/**
 * GPU Histogram Statistics
 */
typedef struct {
    uint32_t total_bytes;
    uint32_t active_symbols;
    uint32_t max_frequency;
    uint32_t min_frequency;
} przm_gpu_hist_stats_t;

/**
 * Stream descriptor for multi-stream parallel ANS encoding
 */
typedef struct {
    uint32_t in_offset;
    uint32_t in_length;
    uint32_t out_offset;
    uint32_t out_length;
    uint32_t final_states[4];
} przm_ans_stream_desc_t;

#ifdef __cplusplus
}
#endif

#endif /* PRZM_ANS_H */
