/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef AES_COMPRESS_CUDA_H
#define AES_COMPRESS_CUDA_H

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>

#ifdef __cplusplus
extern "C" {
#endif

#define AES_BLOCK_SIZE               16
#define AES_CHUNK_BLOCKS             256
#define AES_CHUNK_RAW_BYTES          (AES_CHUNK_BLOCKS * AES_BLOCK_SIZE)         /* 4096 bytes (4 KiB) */
#define AES_CHUNK_COMPRESSED_BLOCKS  255
#define AES_CHUNK_COMP_BYTES         (AES_CHUNK_COMPRESSED_BLOCKS * AES_BLOCK_SIZE) /* 4080 bytes */

/* Benchmark & Validation Results */
typedef struct {
    uint32_t rounds;
    uint64_t total_chunks;
    uint64_t total_raw_bytes;
    uint64_t total_compressed_bytes;
    double   compression_ratio;          /* 4080 / 4096 = 0.99609375 */
    uint64_t total_saved_bytes;

    /* Performance Metrics */
    double   stream_gen_time_ms;
    double   stream_gen_gb_per_sec;
    double   verify_time_ms;
    double   verify_gb_per_sec;
    double   compress_time_ms;
    double   compress_gb_per_sec;
    double   decompress_time_ms;
    double   decompress_gb_per_sec;

    /* Validation Results */
    bool     is_balanced;
    uint64_t num_balance_violations;
    uint64_t first_violation_chunk;
    uint8_t  first_violation_parity[16];
    bool     bit_exact_match;
    uint64_t num_byte_mismatches;
} aes_compress_bench_result_t;

/* Initialize GPU device */
int aes_compress_cuda_init(int device_id);

/* Generate synthetic multi-round AES Lambda-stream on device:
 * rounds: 3 (balanced), 4 (broken), or 5 (saturated)
 * d_out_raw: device buffer of size num_chunks * 4096 bytes
 */
int aes_compress_cuda_generate_stream(
    uint32_t rounds,
    const uint8_t key[16],
    uint64_t num_chunks,
    uint8_t *d_out_raw,
    double *elapsed_ms,
    double *gb_per_sec
);

/* Verify chunk balance: check if every 256-block chunk satisfies XOR sum == 0 */
int aes_compress_cuda_verify_balance(
    const uint8_t *d_raw,
    uint64_t num_chunks,
    bool *h_is_balanced,
    uint64_t *h_num_violations,
    uint64_t *h_first_violation_chunk,
    uint8_t h_first_violation_parity[16],
    double *elapsed_ms,
    double *gb_per_sec
);

/* Compress stream: extract 255 blocks per 256-block chunk */
int aes_compress_cuda_compress(
    const uint8_t *d_raw,
    uint64_t num_chunks,
    uint8_t *d_comp,
    double *elapsed_ms,
    double *gb_per_sec
);

/* Decompress / Reconstruct stream: parallel parity computation + copy */
int aes_compress_cuda_decompress(
    const uint8_t *d_comp,
    uint64_t num_chunks,
    uint8_t *d_reconstructed,
    double *elapsed_ms,
    double *gb_per_sec
);

/* Verify bit-exactness between original raw stream and reconstructed stream */
int aes_compress_cuda_verify_match(
    const uint8_t *d_raw,
    const uint8_t *d_reconstructed,
    uint64_t total_bytes,
    bool *h_match,
    uint64_t *h_mismatches
);

/* Cleanup device */
void aes_compress_cuda_cleanup(void);

#ifdef __cplusplus
}
#endif

#endif /* AES_COMPRESS_CUDA_H */
