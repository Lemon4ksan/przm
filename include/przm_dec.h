/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef PRZM_DEC_H
#define PRZM_DEC_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

/* PRZM Version Information */
#define PRZM_VERSION_MAJOR              0
#define PRZM_VERSION_MINOR              1
#define PRZM_VERSION_PATCH              0
#define PRZM_VERSION_STRING             "0.1.0"

/* PRZM Deterministic Status Codes */
#define PRZM_OK                         0
#define PRZM_ERR_CORRUPTED_FRAME       -1
#define PRZM_ERR_INVALID_MAGIC         -2
#define PRZM_ERR_OFFSET_OOB            -3
#define PRZM_ERR_BUFFER_TOO_SMALL      -4
#define PRZM_ERR_EXCEEDS_MAX_BLOCK     -5
#define PRZM_ERR_UNSUPPORTED_MODE      -6
#define PRZM_ERR_UNKNOWN_DICT          -7

/* Canonical Dictionary Identifier */
#define PRZM_DICT_ID_BROTLI            0x4252544C  /* ASCII 'B','R','T','L' */

/* Structured external dictionary descriptor */
typedef struct {
    const char *data;
    size_t len;
} przm_dict_entry_t;

typedef struct {
    uint32_t dict_id;
    uint32_t _pad;
    size_t count;
    const przm_dict_entry_t *entries;
} przm_dict_desc_t;

/* Canonical C API (Zero-Allocation Decompressor) */
int32_t przm_get_uncompressed_size(const uint8_t *src, size_t src_len);
int32_t przm_decompress(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap);
int32_t przm_decompress_with_dict(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap, const uint8_t *dict, size_t dict_len);
int32_t przm_decompress_dict(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap, const void *dict);

/* Stage 2 Command Stream & 8-Context V-ANS Meta API */
int32_t przm_split_commands(const uint8_t *src, size_t slen,
                            uint8_t *op, size_t op_cap, size_t *op_len,
                            uint8_t *meta, size_t meta_cap, size_t *meta_len,
                            uint8_t *var, size_t var_cap, size_t *var_len);

int32_t przm_join_commands(uint8_t *dst, size_t dst_cap,
                           const uint8_t *op, size_t op_len,
                           const uint8_t *meta, size_t meta_len,
                           const uint8_t *var, size_t var_len,
                           size_t orig_len);

int32_t przm_vans_decode_meta8(const uint8_t *src, size_t slen,
                              uint8_t *dst, size_t dcap, size_t expected_len);

/* Stage 3 Distance Offsets & Prefix API */
int32_t przm_distance_to_code(uint32_t d, uint8_t *code, uint32_t *extra, uint8_t *nb);
int32_t przm_code_to_distance(uint8_t code, uint32_t extra, uint32_t *d);
int32_t przm_vans_decode_dist4(const uint8_t *src, size_t slen, uint8_t *dst, size_t dcap, size_t n);
int32_t przm_distance_prefix_decode(const uint8_t *src, size_t slen,
                                    uint8_t *dst, size_t dcap, size_t *out_len);

/* Stage 4 Literals & Étale Sheaf API */
uint8_t przm_context_classify(uint8_t p1, uint8_t mode);
uint8_t przm_context_classify_order2(uint8_t p2, uint8_t p1);
int32_t przm_reconstitute_fibers(uint8_t *dst, size_t u_lit,
                                 const uint8_t *f0, const uint8_t *f1,
                                 const uint8_t *f2, const uint8_t *f3,
                                 size_t l0, size_t l1, size_t l2, size_t l3,
                                 uint8_t mode);
int32_t przm_vans_decode_literals(const uint8_t *payload, size_t payload_len,
                                  uint8_t *dst, size_t u_len);
int32_t przm_unpack_nibble_planes(const uint8_t *src, size_t src_len,
                                  uint8_t *dst, size_t u_len);

#ifdef __cplusplus
}
#endif

#endif /* PRZM_DEC_H */
