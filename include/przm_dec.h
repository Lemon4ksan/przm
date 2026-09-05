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
#define PRZM_OK                    0
#define PRZM_ERR_CORRUPTED_FRAME  -1
#define PRZM_ERR_INVALID_MAGIC    -2
#define PRZM_ERR_OFFSET_OOB       -3
#define PRZM_ERR_BUFFER_TOO_SMALL -4
#define PRZM_ERR_EXCEEDS_MAX_BLOCK -5
#define PRZM_ERR_UNSUPPORTED_MODE -6

/* Core dimensions */
#define PRZM_HEADER_SIZE          8
#define PRZM_MAX_BLOCK_SIZE       (16 * 1024 * 1024) /* 16 MiB */
#define PRZM_SAFETY_PADDING       64                 /* Minimum trailing bytes in dst */

/* Magic 32-bit signatures (Little-Endian) */
#define PRZM_MAGIC_STANDARD       0x4D5A5250 /* 'P','R','Z','M' */
#define PRZM_MAGIC_SYMPLECTIC     0x53594D50 /* 'S','Y','M','P' */
#define PRZM_MAGIC_MAHLER         0x4D41484C /* 'M','A','H','L' */
#define PRZM_MAGIC_SHEAF          0x53484546 /* 'S','H','E','F' */
#define PRZM_MAGIC_ARM64          0x41524D36 /* 'A','R','M','6' */
#define PRZM_MAGIC_MACRO_TRELLIS  0x4D414352 /* 'M','A','C','R' */
#define PRZM_MAGIC_DEP_SHEAF      0x44455053 /* 'D','E','P','S' */

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
#define PRZM_FLAG_BCJ_SPLIT       0x20
#define PRZM_FLAG_BCJ             0x40
#define PRZM_FLAG_NIBBLE          0x80

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

#ifdef __cplusplus
}
#endif

#endif /* PRZM_DEC_H */
