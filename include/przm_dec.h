/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 *
 * PRZM-SPEC-0.5 Pure C Zero-Allocation Decompressor.
 */

#ifndef PRZM_DEC_H
#define PRZM_DEC_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

/* Return error codes */
typedef int przm_status_t;
#define PRZM_OK                    0
#define PRZM_ERR_CORRUPTED_FRAME  -1
#define PRZM_ERR_INVALID_MAGIC    -2
#define PRZM_ERR_OFFSET_OOB       -3
#define PRZM_ERR_BUFFER_TOO_SMALL -4
#define PRZM_ERR_EXCEEDS_MAX_BLOCK -5
#define PRZM_ERR_UNSUPPORTED_MODE -6
#define PRZM_ERR_UNKNOWN_DICT     -7

/* Core dimensions */
#define PRZM_HEADER_SIZE          8
#define PRZM_MAX_BLOCK_SIZE       (16 * 1024 * 1024) /* 16 MiB */
#define PRZM_SAFETY_PADDING       64                 /* Minimum trailing bytes in dst */
#define PRZM_DEDUP_BLOCK_SIZE     65536              /* 64 KiB block granularity for cross-block deduplication */

/* Magic 32-bit signatures (Little-Endian) */
#define PRZM_MAGIC_STANDARD       0x4D5A5250 /* 'P','R','Z','M' */
#define PRZM_MAGIC_STREAM         0x535A5250 /* 'P','R','Z','S' (Multi-Chunk Stream) */
#define PRZM_STREAM_HEADER_SIZE   17
#define PRZM_STREAM_FLAG_INDEPENDENT 0x01
#define PRZM_MAGIC_SYMPLECTIC     0x53594D50 /* 'S','Y','M','P' */
#define PRZM_MAGIC_MAHLER         0x4D41484C /* 'M','A','H','L' */
#define PRZM_MAGIC_SHEAF          0x53484546 /* 'S','H','E','F' */
#define PRZM_MAGIC_ARM64          0x41524D36 /* 'A','R','M','6' */
#define PRZM_MAGIC_MACRO_TRELLIS  0x4D414352 /* 'M','A','C','R' */
#define PRZM_MAGIC_DEP_SHEAF      0x44455053 /* 'D','E','P','S' */
#define PRZM_MAGIC_ISOQUANT       0x514F5349 /* 'I','S','O','Q' */
#define PRZM_ISOQUANT_HEADER_SIZE 20
#define PRZM_MAGIC_GCTR           0x47435452 /* 'G','C','T','R' */
#define PRZM_GCTR_HEADER_SIZE     10
#define PRZM_MAGIC_HEISENBERG     0x53494548 /* 'H','E','I','S' */
#define PRZM_HEISENBERG_HEADER_SIZE 12
#define PRZM_MAGIC_CELLULAR_SHEAF 0x44485343 /* 'C','S','H','D' */
#define PRZM_CELLULAR_SHEAF_HEADER_SIZE 24
#define PRZM_MAGIC_MOBIUS         0x49424F4D /* 'M','O','B','I' */
#define PRZM_MOBIUS_HEADER_SIZE   12
#define PRZM_MAGIC_MARGOLUS       0x4752414D /* 'M','A','R','G' */
#define PRZM_MARGOLUS_HEADER_SIZE 16
#define PRZM_MAGIC_ARCHIVAL_PNG   0x504E4741 /* 'P','N','G','A' */

/* Compression Modes / Levels */
#define PRZM_MODE_ARCHIVAL        5

/* Opcode assignments */
#define PRZM_OP_MARGOLUS          0x0F
#define PRZM_OP_MOBIUS            0x11
#define PRZM_OP_COPY_BLOCK        0xFD               /* Aligned 64 KiB block replication (Milestone 5) */

/* Fast Möbius Transform modes and flags */
#define PRZM_FMT_MODE_RAW         0x00
#define PRZM_FMT_MODE_TRANSFORMED 0x01
#define PRZM_FMT_MODE_COMPRESSED  0x03
#define PRZM_FMT_FLAG_BIT_LEVEL   0x10

/* Margolus RCA transition rules */
#define PRZM_MARGOLUS_RULE_BBM        0
#define PRZM_MARGOLUS_RULE_TRANSPOSE  1
#define PRZM_MARGOLUS_RULE_ROT180     2
#define PRZM_MARGOLUS_RULE_ROT90      3
#define PRZM_MARGOLUS_RULE_XOR        4
#define PRZM_MARGOLUS_RULE_FLIP_H     5
#define PRZM_MARGOLUS_RULE_FLIP_V     6

/* Well-known dictionary identifiers */
#define PRZM_DICT_ID_NONE         0
#define PRZM_DICT_ID_JSON         1
#define PRZM_DICT_ID_CODE         2 /* Deprecated */
#define PRZM_DICT_ID_RESERVED_MAX 0x0FFF
#define PRZM_DICT_ID_EXTERNAL_MIN 0x1000
#define PRZM_DICT_MAX_TOKENS      256

typedef struct {
    const char* str;
    size_t      len;
} przm_dict_entry_t;

typedef struct {
    uint32_t                 dict_id;
    size_t                   num_tokens;
    const przm_dict_entry_t* tokens;
} przm_dictionary_t;

/* Flags for Cellular Sheaves with Discrete Hodge Laplacians */
#define PRZM_CSHD_FLAG_RAW          0x0000
#define PRZM_CSHD_FLAG_CURL_FREE    0x0001
#define PRZM_CSHD_FLAG_SPARSE_CURL  0x0002
#define PRZM_CSHD_FLAG_CONST_EH     0x0004
#define PRZM_CSHD_FLAG_CONST_EV     0x0008
#define PRZM_CSHD_FLAG_CASCADED_PWS 0x0010

/* 3-byte versioned ISA signatures */
#define PRZM_MAGIC_VM_0           'P'
#define PRZM_MAGIC_VM_1           'V'
#define PRZM_MAGIC_PWS_0          'P'
#define PRZM_MAGIC_PWS_1          'W'
#define PRZM_MAGIC_GGTC_0         'G'
#define PRZM_MAGIC_GGTC_1         'G'
#define PRZM_MAGIC_VANS_0         'V'
#define PRZM_MAGIC_VANS_1         'A'
#define PRZM_VERSION_1            0x01

/* Standard Frame Flags */
#define PRZM_FLAG_STRIDE          0x01
#define PRZM_FLAG_DELTA_MASK      0x02
#define PRZM_FLAG_UNCOMPRESSED    0x04
#define PRZM_FLAG_EXT_OFFSET      0x08
#define PRZM_FLAG_DICT_REF        0x10
#define PRZM_FLAG_NORM            0x20 /* Bit 5: Base64/Hex Normalization applied (when FlagBCJ == 0) */
#define PRZM_FLAG_BCJ_SPLIT       0x20 /* Bit 5: x86 BCJ displacement splitting applied (when FlagBCJ != 0) */
#define PRZM_FLAG_BCJ             0x40
#define PRZM_FLAG_NIBBLE          0x80

/* Normalization Modes (Section 1 byte 2) */
#define PRZM_NORM_NONE            0
#define PRZM_NORM_HEX             1 /* Whole-block lowercase hex */
#define PRZM_NORM_BASE64          2 /* Whole-block standard Base64 unpadded */
#define PRZM_NORM_SPANS           3 /* Span-based mixed normalization */
#define PRZM_NORM_HEX_UPPER       4 /* Whole-block uppercase hex */
#define PRZM_NORM_BASE64_PAD1     5 /* Whole-block standard Base64 with 1 '=' */
#define PRZM_NORM_BASE64_PAD2     6 /* Whole-block standard Base64 with 2 '==' */
#define PRZM_NORM_BASE64_URL      7 /* Whole-block URL-safe Base64 unpadded */
#define PRZM_NORM_BASE64_URL_P1   8 /* Whole-block URL-safe Base64 with 1 '=' */
#define PRZM_NORM_BASE64_URL_P2   9 /* Whole-block URL-safe Base64 with 2 '==' */

/* Normalization Span Types (for PRZM_NORM_SPANS) */
#define PRZM_SPAN_HEX_LOWER       1
#define PRZM_SPAN_HEX_UPPER       2
#define PRZM_SPAN_BASE64_STD      3
#define PRZM_SPAN_BASE64_PAD1     4
#define PRZM_SPAN_BASE64_PAD2     5
#define PRZM_SPAN_BASE64_URL      6
#define PRZM_SPAN_BASE64_URL_P1   7
#define PRZM_SPAN_BASE64_URL_P2   8

/* Entropy Stream Modes (Section 2 & 3) */
#define PRZM_ENTROPY_MODE_FLAT        0
#define PRZM_ENTROPY_MODE_HUFFMAN_4X  1

/* WindowLog levels (PRZM-SPEC-0.5 Milestone 2 Window Scaling) */
#define PRZM_WINDOW_LOG_64K           16
#define PRZM_WINDOW_LOG_1M            20
#define PRZM_WINDOW_LOG_8M            23

/**
 * przm_get_uncompressed_size
 *
 * Inspects a PRZM-compressed frame header and extracts the exact uncompressed payload size.
 *
 * @param src Pointer to compressed frame bytes.
 * @param src_len Byte length of compressed buffer.
 * @return Uncompressed size in bytes, or negative PRZM_ERR_* on corruption.
 */
int64_t przm_get_uncompressed_size(const uint8_t* src, size_t src_len);

/**
 * przm_decompress
 *
 * Decompresses a PRZM-compressed frame into a pre-allocated destination buffer.
 *
 * Guarantees:
 * - Strictly zero heap memory allocations (0 malloc/free calls).
 * - Thread-safe and reentrant.
 * - Bit-exact lossless reconstruction matching PRZM-SPEC-0.5.
 *
 * @param src Pointer to compressed frame.
 * @param src_len Length of compressed frame.
 * @param dst Pointer to pre-allocated output buffer. MUST have capacity of at least
 *            (uncompressed_size + PRZM_SAFETY_PADDING) bytes to allow vector operations.
 * @param dst_cap Total capacity of dst buffer in bytes.
 * @return Number of uncompressed bytes written to dst, or negative PRZM_ERR_* code.
 */
int64_t przm_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap);

/**
 * przm_decompress_dict
 *
 * Decompresses a PRZM-compressed frame using an optional external dictionary.
 * Strictly 0 heap allocations, thread-safe, and reentrant.
 *
 * @param src Pointer to compressed frame bytes.
 * @param src_len Byte length of compressed buffer.
 * @param dst Pre-allocated destination buffer (capacity >= uncompressed_size + PRZM_SAFETY_PADDING).
 * @param dst_cap Total allocated capacity of dst.
 * @param dict Optional pointer to caller-managed external dictionary descriptor.
 * @return Number of uncompressed bytes written to dst, or negative PRZM_ERR_* on failure.
 */
int64_t przm_decompress_dict(const uint8_t* src, size_t src_len,
                             uint8_t* dst, size_t dst_cap,
                             const przm_dictionary_t* dict);

/**
 * przm_fmt_transform_inplace / przm_mobius_transform_inplace
 *
 * In-place Fast Möbius Transform (F_2 Yates butterfly involution).
 * Operates across full 2^block_log2 blocks; residual bytes are preserved.
 * Forward and untransform are algebraically identical: FMT(FMT(X)) == X.
 */
int przm_fmt_transform_inplace(uint8_t* dst, size_t len, uint8_t block_log2, uint8_t bit_level);
int przm_mobius_transform_inplace(uint8_t* dst, size_t len, uint8_t block_log2, uint8_t bit_level);
int przm_mobius_untransform_inplace(uint8_t* dst, size_t len, uint8_t block_log2, uint8_t bit_level);
int64_t przm_mobius_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap);

/**
 * przm_margolus_step_inplace / przm_margolus_reverse_inplace
 *
 * Margolus Reversible Cellular Automata on 2D H x W lattice.
 * Forward evolution and time-reversed inversion execute strictly in-place with 0 allocations.
 */
int przm_margolus_step_inplace(uint8_t* dst, size_t H, size_t W, int phase, uint8_t rule_id, int inverse);
int przm_margolus_reverse_inplace(uint8_t* dst, size_t len, uint16_t width, uint8_t steps, uint8_t rule_id);
int przm_margolus_inverse_inplace(uint8_t* dst, size_t len, uint16_t width, uint8_t steps, uint8_t rule_id);
int64_t przm_margolus_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap);

/**
 * przm_isoquant_unrotate_inplace
 *
 * In-place inverse 4D isoclinic rotation over H(F_{2^8}).
 * Branchless vector unrotation satisfying <= 22 CPU clock cycles per 64 bytes and 0 allocations.
 *
 * @param dst Pointer to destination buffer holding collimated bytes.
 * @param len Number of bytes to unrotate in-place (must be multiple of 4 for whole quads).
 * @param qL_inv Left inverse quaternion [4]uint8.
 * @param qR_inv Right inverse quaternion [4]uint8.
 * @return PRZM_OK on success, or negative PRZM_ERR_* on error.
 */
przm_status_t przm_isoquant_unrotate_inplace(uint8_t* dst, size_t len, const uint8_t qL_inv[4], const uint8_t qR_inv[4]);

/**
 * przm_gctr_decompress
 *
 * Decompresses a Galois Superoptimized JIT & Trace Annihilation (GCTR) frame
 * with zero heap allocations and full bit-exact Galois inverse reconstruction.
 *
 * @param src Pointer to compressed GCTR frame.
 * @param src_len Length of compressed GCTR frame.
 * @param dst Pre-allocated destination buffer (>= orig_len + PRZM_SAFETY_PADDING).
 * @param dst_cap Total capacity of dst.
 * @return Number of uncompressed bytes, or negative PRZM_ERR_* code.
 */
int64_t przm_gctr_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap);

/**
 * przm_heisenberg_unfoliate_inplace
 *
 * In-place inverse Heisenberg group H_3(F_{2^8}) unfoliation.
 * Strictly zero heap allocations, branchless vector-pipeline unrolling,
 * bounded by <= 18 CPU clock cycles per 64 bytes.
 *
 * @param dst Pointer to destination buffer holding foliated stream.
 * @param len Number of bytes to unfoliate in-place.
 * @param stride Channel stride (3 for triplets/RGB, 4 for quads/RGBA).
 * @return PRZM_OK on success, or negative PRZM_ERR_* on error.
 */
int przm_heisenberg_unfoliate_inplace(uint8_t* dst, size_t len, uint8_t stride);

/**
 * przm_cellular_sheaf_decompress
 *
 * Decompresses a Cellular Sheaf with Discrete Hodge Laplacians (CSHD) frame
 * with strictly zero heap memory allocations (0 malloc/free calls).
 * Reconstructs potential 0-cochains from compact boundary representations via
 * unrolled vectorized row sweeps.
 *
 * @param src Pointer to compressed CSHD frame.
 * @param src_len Byte length of compressed frame.
 * @param dst Pre-allocated destination buffer (>= orig_len + PRZM_SAFETY_PADDING).
 * @param dst_cap Total capacity of dst.
 * @return Number of uncompressed bytes, or negative PRZM_ERR_* code.
 */
int64_t przm_cellular_sheaf_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap);

/**
 * Normalization expansion functions.
 */
void przm_expand_hex(uint8_t* dst, const uint8_t* src, size_t src_len, int upper);
void przm_expand_base64(uint8_t* dst, const uint8_t* src, size_t src_len, int pad_count, int url_safe);
int przm_expand_spans(uint8_t* dst, size_t dst_len, const uint8_t* src, size_t src_len);

#ifdef __cplusplus
}
#endif

#endif /* PRZM_DEC_H */
