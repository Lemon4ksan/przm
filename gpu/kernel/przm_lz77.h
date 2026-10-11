/**
 * przm_lz77.h - C interface header for PRZM GPU LZ77 Compression Kernel
 * 
 * Defines data structures, packed match formats, and function prototypes
 * for GPU-accelerated parallel LZ77 dictionary search and hash-chaining.
 * 
 * Hardware Target: NVIDIA Ampere GA106 (RTX 3060, SM 8.6, 28 SMs, 12 GB VRAM)
 * Copyright (c) 2026 PRZM GPU Project. All rights reserved.
 */

#ifndef PRZM_LZ77_H
#define PRZM_LZ77_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Window size limits */
#define PRZM_WINDOW_64K           (64 * 1024)
#define PRZM_WINDOW_1M            (1024 * 1024)
#define PRZM_WINDOW_8M            (8 * 1024 * 1024)

#define PRZM_MIN_MATCH_LEN        4
#define PRZM_MAX_MATCH_LEN_32     4095      /* 12-bit length in 32-bit packed descriptor */
#define PRZM_MAX_OFFSET_32        1048575   /* 20-bit offset in 32-bit packed descriptor */
#define PRZM_MAX_MATCH_LEN_64     65535     /* 16-bit / 32-bit length */

/* GPU execution configuration defaults */
#define PRZM_CUDA_BLOCK_SIZE      256
#define PRZM_CUDA_GRID_SIZE       224       /* 224 blocks * 256 threads = 57,344 threads (8 blocks/SM on 28 SMs) */
#define PRZM_HASH_PRIME           0x1E35A7BDU

/* Shared memory tile configuration */
#define PRZM_GPU_TILE_SIZE        32768     /* 32 KiB shared memory tile */
#define PRZM_GPU_NUM_BUCKETS      2048      /* 2048 shared memory hash buckets */

/**
 * Packed 32-bit match descriptor:
 * Bits [31:20] - Match length (12 bits: 0..4095). 0 indicates literal byte.
 * Bits [19:0]  - Match offset (20 bits: 0..1,048,575). Distance backwards from current pos.
 */
typedef uint32_t przm_match_desc32_t;

#if defined(__CUDACC__) || defined(__CUDA_ARCH__)
#define PRZM_HD __host__ __device__
#else
#define PRZM_HD
#endif

static inline PRZM_HD przm_match_desc32_t przm_pack_match32(uint16_t length, uint32_t offset) {
    if (length < PRZM_MIN_MATCH_LEN || offset == 0) {
        return 0; /* Literal */
    }
    uint32_t l = (uint32_t)(length > PRZM_MAX_MATCH_LEN_32 ? PRZM_MAX_MATCH_LEN_32 : length);
    uint32_t o = offset & 0x000FFFFFU;
    return (l << 20) | o;
}

static inline PRZM_HD uint16_t przm_unpack_length32(przm_match_desc32_t desc) {
    return (uint16_t)(desc >> 20);
}

static inline PRZM_HD uint32_t przm_unpack_offset32(przm_match_desc32_t desc) {
    return desc & 0x000FFFFFU;
}

static inline PRZM_HD int przm_is_match32(przm_match_desc32_t desc) {
    return (desc >> 20) >= PRZM_MIN_MATCH_LEN;
}

/**
 * Packed 64-bit match descriptor with explicit literal byte tag:
 * Supports up to 8MB / 16MB sliding windows and longer matches.
 */
typedef struct {
    uint32_t length;     /* Match length (0 if literal) */
    uint32_t offset;     /* Distance backwards from current pos */
    uint8_t  literal;    /* Uncompressed byte when length == 0 */
    uint8_t  flags;      /* Bit 0: is_match, Bit 1: is_long_match */
    uint16_t reserved;
} przm_match_desc64_t;

/**
 * High-precision GPU execution and PCIe transfer profiling metrics.
 */
typedef struct {
    uint64_t ingress_dma_bytes;
    double   ingress_dma_time_ms;
    double   ingress_dma_bandwidth_gbps;

    double   kernel_time_ms;
    double   internal_vram_bandwidth_gbps;

    uint64_t egress_dma_bytes;
    double   egress_dma_time_ms;
    double   egress_dma_bandwidth_gbps;

    double   total_round_trip_ms;
    double   pcie_overhead_percent;

    char     device_name[128];
    int      sm_count;
    uint64_t total_vram_bytes;
} przm_gpu_profile_t;

#define PRZM_RADIX_STRIKE_NUM_BUCKETS 2097152U
#define PRZM_RADIX_STRIKE_STRIDE      8

#define PRZM_COMPACT_BLOCK_SIZE 256
#define PRZM_COMPACT_NUM_WARPS  (PRZM_COMPACT_BLOCK_SIZE / 32)

/**
 * Host C wrapper entry points.
 */
int cuda_lz77_match_batch(
    const uint8_t* d_input,
    size_t input_len,
    uint32_t* d_matches,
    size_t window_size,
    void* stream
);

int cuda_lz77_radix_strike_batch(
    const uint8_t* d_input,
    size_t input_len,
    uint32_t* d_radix_table,
    uint32_t* d_matches,
    size_t window_size,
    void* stream
);

int cuda_lz77_compact_stream(
    const uint32_t* d_matches,
    uint32_t n,
    uint32_t* d_block_counts,
    uint32_t* d_block_offsets,
    uint32_t* d_total_count,
    uint32_t* d_compact_pos,
    uint32_t* d_compact_desc,
    uint32_t num_blocks,
    void* stream
);

#ifdef __cplusplus
}
#endif

#endif /* PRZM_LZ77_H */
