/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 *
 * PRZM-SPEC-0.5 Pure C Zero-Allocation Decompressor Implementation.
 */

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "przm_dec.h"

#if defined(__GNUC__) || defined(__clang__)
#define PRZM_INLINE static inline __attribute__((always_inline))
#define PRZM_TLS    __thread
#elif defined(_MSC_VER)
#define PRZM_INLINE static __forceinline
#define PRZM_TLS    __declspec(thread)
#else
#define PRZM_INLINE static inline
#define PRZM_TLS    _Thread_local
#endif

/* Thread-local scratchpad size for intermediate planes and literal buffers */
#define PRZM_TLS_SCRATCH_SIZE (8 * 1024 * 1024) /* 8 MiB scratchpads */
#define PRZM_TLS_SPLIT_SIZE   (4 * 1024 * 1024) /* 4 MiB BCJ split scratchpad */

static PRZM_TLS uint8_t tls_lits_scratch[PRZM_TLS_SCRATCH_SIZE];
static PRZM_TLS uint8_t tls_split_scratch[PRZM_TLS_SPLIT_SIZE];
static PRZM_TLS uint8_t tls_cmd_scratch[PRZM_TLS_SCRATCH_SIZE];
static PRZM_TLS uint8_t tls_off_scratch[PRZM_TLS_SCRATCH_SIZE];
static PRZM_TLS uint8_t tls_norm_scratch[PRZM_TLS_SCRATCH_SIZE];
static PRZM_TLS uint8_t tls_archival_scratch[PRZM_TLS_SCRATCH_SIZE];

/* ============================================================================
 * 1. Byte-Level Helpers & Unaligned Integer Access
 * ============================================================================ */

PRZM_INLINE uint16_t read_u16_le(const uint8_t* p) {
    return (uint16_t)p[0] | ((uint16_t)p[1] << 8);
}

PRZM_INLINE uint32_t read_u24_le(const uint8_t* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16);
}

PRZM_INLINE uint32_t read_u32_le(const uint8_t* p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) |
           ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

PRZM_INLINE uint64_t read_u64_le(const uint8_t* p) {
    return (uint64_t)read_u32_le(p) | ((uint64_t)read_u32_le(p + 4) << 32);
}

PRZM_INLINE void write_u16_le(uint8_t* p, uint16_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
}

PRZM_INLINE void write_u32_le(uint8_t* p, uint32_t v) {
    p[0] = (uint8_t)v;
    p[1] = (uint8_t)(v >> 8);
    p[2] = (uint8_t)(v >> 16);
    p[3] = (uint8_t)(v >> 24);
}

PRZM_INLINE void write_u64_le(uint8_t* p, uint64_t v) {
    write_u32_le(p, (uint32_t)v);
    write_u32_le(p + 4, (uint32_t)(v >> 32));
}

PRZM_INLINE int fast_read_varint(const uint8_t* src, size_t src_len, size_t* ptr, int* out_val) {
    if (*ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
    uint8_t b0 = src[(*ptr)++];
    if (b0 < 0x80) {
        *out_val = (int)b0;
        return PRZM_OK;
    }
    if (*ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
    uint8_t b1 = src[(*ptr)++];
    if (b1 < 0x80) {
        *out_val = (int)(b0 & 0x7F) | ((int)b1 << 7);
        return PRZM_OK;
    }
    if (*ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
    uint8_t b2 = src[(*ptr)++];
    if (b2 < 0x80) {
        *out_val = (int)(b0 & 0x7F) | ((int)(b1 & 0x7F) << 7) | ((int)b2 << 14);
        return PRZM_OK;
    }
    if (*ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
    uint8_t b3 = src[(*ptr)++];
    if (b3 < 0x80) {
        *out_val = (int)(b0 & 0x7F) | ((int)(b1 & 0x7F) << 7) | ((int)(b2 & 0x7F) << 14) | ((int)b3 << 21);
        return PRZM_OK;
    }
    uint64_t val = (b0 & 0x7F) | ((uint64_t)(b1 & 0x7F) << 7) | ((uint64_t)(b2 & 0x7F) << 14) | ((uint64_t)(b3 & 0x7F) << 21);
    int shift = 28;
    while (*ptr < src_len) {
        uint8_t b = src[(*ptr)++];
        if (shift > 28) return PRZM_ERR_CORRUPTED_FRAME;
        val |= (uint64_t)(b & 0x7F) << shift;
        if ((b & 0x80) == 0) {
            if (val > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
            *out_val = (int)val;
            return PRZM_OK;
        }
        shift += 7;
    }
    return PRZM_ERR_CORRUPTED_FRAME;
}

/* ============================================================================
 * 2. Vectorized Match Replication (Overlapping & Non-Overlapping)
 * ============================================================================ */

PRZM_INLINE void copy_match(uint8_t* dst, size_t dst_pos, size_t src_idx, size_t m_len, int d) {
    uint8_t* p_dst = dst + dst_pos;
    const uint8_t* p_src = dst + src_idx;

    if (d >= 8) {
        while (m_len > 0) {
            write_u64_le(p_dst, read_u64_le(p_src));
            p_src += 8;
            p_dst += 8;
            if (m_len <= 8) return;
            m_len -= 8;
        }
        return;
    }

    if (d == 1) {
        memset(p_dst, *p_src, m_len);
        return;
    }

    if (d == 2) {
        uint16_t w = read_u16_le(p_src);
        uint64_t pattern = (uint64_t)w | ((uint64_t)w << 16) |
                           ((uint64_t)w << 32) | ((uint64_t)w << 48);
        while (m_len > 0) {
            write_u64_le(p_dst, pattern);
            p_dst += 8;
            if (m_len <= 8) return;
            m_len -= 8;
        }
        return;
    }

    if (d == 4) {
        uint32_t w = read_u32_le(p_src);
        uint64_t pattern = (uint64_t)w | ((uint64_t)w << 32);
        while (m_len > 0) {
            write_u64_le(p_dst, pattern);
            p_dst += 8;
            if (m_len <= 8) return;
            m_len -= 8;
        }
        return;
    }

    /* Fallback for other small distances d in {3, 5, 6, 7} */
    for (size_t i = 0; i < m_len; i++) {
        p_dst[i] = dst[src_idx + i];
    }
}

/* ============================================================================
 * 3. Galois Field GF(2^8) Arithmetic (AES Polynomial 0x11B)
 * ============================================================================ */

PRZM_INLINE uint8_t gf256_mul(uint8_t a, uint8_t b) {
    uint8_t p = 0;
    for (int i = 0; i < 8; i++) {
        if (b & 1) p ^= a;
        uint8_t hi = a & 0x80;
        a <<= 1;
        if (hi) a ^= 0x1B;
        b >>= 1;
    }
    return p;
}

/* ============================================================================
 * 3.1. 4D IsoQuant Quaternionic Inverse Collimation In-Place (Magic 'ISOQ')
 * ============================================================================ */

przm_status_t przm_isoquant_unrotate_inplace(uint8_t* dst, size_t len, const uint8_t qL_inv[4], const uint8_t qR_inv[4]) {
    if (dst == NULL || qL_inv == NULL || qR_inv == NULL) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }
    if (len < 4) {
        return PRZM_OK;
    }

    uint8_t a0 = qL_inv[0], a1 = qL_inv[1], a2 = qL_inv[2], a3 = qL_inv[3];
    uint8_t b0 = qR_inv[0], b1 = qR_inv[1], b2 = qR_inv[2], b3 = qR_inv[3];

    /* Effective quaternion q_eff = qL_inv * qR_inv over H(F_{2^8}) */
    uint8_t e0 = gf256_mul(a0, b0) ^ gf256_mul(a1, b1) ^ gf256_mul(a2, b2) ^ gf256_mul(a3, b3);
    uint8_t e1 = gf256_mul(a0, b1) ^ gf256_mul(a1, b0) ^ gf256_mul(a2, b3) ^ gf256_mul(a3, b2);
    uint8_t e2 = gf256_mul(a0, b2) ^ gf256_mul(a2, b0) ^ gf256_mul(a1, b3) ^ gf256_mul(a3, b1);
    uint8_t e3 = gf256_mul(a0, b3) ^ gf256_mul(a3, b0) ^ gf256_mul(a1, b2) ^ gf256_mul(a2, b1);

    /* If effective quaternion is identity (1, 0, 0, 0), rotation is trivial */
    if (e0 == 1 && e1 == 0 && e2 == 0 && e3 == 0) {
        return PRZM_OK;
    }

    /* Process 16 bytes (4 quaternions) per iteration for SWAR / SIMD auto-vectorization */
    size_t limit16 = (len >> 4) << 4;
    for (size_t i = 0; i < limit16; i += 16) {
        for (size_t k = 0; k < 16; k += 4) {
            uint8_t v0 = dst[i + k + 0];
            uint8_t v1 = dst[i + k + 1];
            uint8_t v2 = dst[i + k + 2];
            uint8_t v3 = dst[i + k + 3];

            dst[i + k + 0] = gf256_mul(e0, v0) ^ gf256_mul(e1, v1) ^ gf256_mul(e2, v2) ^ gf256_mul(e3, v3);
            dst[i + k + 1] = gf256_mul(e0, v1) ^ gf256_mul(e1, v0) ^ gf256_mul(e2, v3) ^ gf256_mul(e3, v2);
            dst[i + k + 2] = gf256_mul(e0, v2) ^ gf256_mul(e2, v0) ^ gf256_mul(e1, v3) ^ gf256_mul(e3, v1);
            dst[i + k + 3] = gf256_mul(e0, v3) ^ gf256_mul(e3, v0) ^ gf256_mul(e1, v2) ^ gf256_mul(e2, v1);
        }
    }

    /* Process remaining aligned 4-byte quads */
    for (size_t i = limit16; i + 4 <= len; i += 4) {
        uint8_t v0 = dst[i + 0];
        uint8_t v1 = dst[i + 1];
        uint8_t v2 = dst[i + 2];
        uint8_t v3 = dst[i + 3];

        dst[i + 0] = gf256_mul(e0, v0) ^ gf256_mul(e1, v1) ^ gf256_mul(e2, v2) ^ gf256_mul(e3, v3);
        dst[i + 1] = gf256_mul(e0, v1) ^ gf256_mul(e1, v0) ^ gf256_mul(e2, v3) ^ gf256_mul(e3, v2);
        dst[i + 2] = gf256_mul(e0, v2) ^ gf256_mul(e2, v0) ^ gf256_mul(e1, v3) ^ gf256_mul(e3, v1);
        dst[i + 3] = gf256_mul(e0, v3) ^ gf256_mul(e3, v0) ^ gf256_mul(e1, v2) ^ gf256_mul(e2, v1);
    }

    return PRZM_OK;
}

/* ============================================================================
 * 3.2. Discrete Heisenberg Group H_3(F_{2^8}) Inverse Foliation In-Place ('HEIS')
 * ============================================================================ */

int przm_heisenberg_unfoliate_inplace(uint8_t* dst, size_t len, uint8_t stride) {
    if (dst == NULL) return PRZM_ERR_CORRUPTED_FRAME;
    if (len < stride) return PRZM_OK;

    if (stride == 3) {
        /* Unroll 4 triplets (12 bytes) per iteration for instruction-level parallelism */
        size_t limit12 = (len / 12) * 12;
        for (size_t i = 0; i < limit12; i += 12) {
            uint8_t p0 = gf256_mul(dst[i + 0], dst[i + 1]);
            uint8_t p1 = gf256_mul(dst[i + 3], dst[i + 4]);
            uint8_t p2 = gf256_mul(dst[i + 6], dst[i + 7]);
            uint8_t p3 = gf256_mul(dst[i + 9], dst[i + 10]);

            dst[i + 2]  ^= p0;
            dst[i + 5]  ^= p1;
            dst[i + 8]  ^= p2;
            dst[i + 11] ^= p3;
        }

        /* Remainder triplets */
        size_t limit = (len / 3) * 3;
        for (size_t i = limit12; i < limit; i += 3) {
            dst[i + 2] ^= gf256_mul(dst[i + 0], dst[i + 1]);
        }
        return PRZM_OK;
    } else if (stride == 4) {
        /* Unroll 4 quads (16 bytes) per iteration */
        size_t limit16 = (len / 16) * 16;
        for (size_t i = 0; i < limit16; i += 16) {
            uint8_t p0 = gf256_mul(dst[i + 0], dst[i + 1]);
            uint8_t p1 = gf256_mul(dst[i + 4], dst[i + 5]);
            uint8_t p2 = gf256_mul(dst[i + 8], dst[i + 9]);
            uint8_t p3 = gf256_mul(dst[i + 12], dst[i + 13]);

            dst[i + 2]  ^= p0;
            dst[i + 6]  ^= p1;
            dst[i + 10] ^= p2;
            dst[i + 14] ^= p3;
        }

        /* Remainder quads */
        size_t limit = (len / 4) * 4;
        for (size_t i = limit16; i < limit; i += 4) {
            dst[i + 2] ^= gf256_mul(dst[i + 0], dst[i + 1]);
        }
        return PRZM_OK;
    }

    return PRZM_ERR_CORRUPTED_FRAME;
}

/* ============================================================================
 * 4. Schema Dictionaries (Explicit External Dictionaries)
 * ============================================================================ */

#include "przm_brotli_dict.h"

PRZM_INLINE const char* get_schema_token(uint32_t dict_id, uint8_t token_idx,
                                         const przm_dictionary_t* ext_dict,
                                         size_t* out_len) {
    if (ext_dict != NULL && ext_dict->dict_id == dict_id) {
        if (token_idx < ext_dict->num_tokens) {
            *out_len = ext_dict->tokens[token_idx].len;
            return ext_dict->tokens[token_idx].str;
        }
    }
    if (dict_id == PRZM_DICT_ID_BROTLI_WEB || dict_id == 0) {
        *out_len = przm_brotli_web_tokens[token_idx].len;
        return przm_brotli_web_tokens[token_idx].str;
    }
    *out_len = 0;
    return NULL;
}

/* ============================================================================
 * 5. Instruction & Architecture Filters (ARM64 & x86 BCJ)
 * ============================================================================ */

/* In-place x86 CALL (0xE8) / JMP (0xE9) relative address translation (7-Zip Bra86) */
static void x86_bcj_inverse(uint8_t* data, size_t size) {
    if (size < 5) return;
    size_t lim = size - 4;
    uint32_t mask = 0;
    uint32_t pc = 4;
    size_t p = 0;

    for (;;) {
    start:
        if (p >= lim) break;
        uint8_t b = data[p];
        if (b != 0xE8 && b != 0xE9) {
            p++;
            mask >>= 1;
            continue;
        }
        p++;

        if (mask == 0) goto a3;
        if (mask > 4 || mask == 3) {
            mask >>= 1;
            mask |= 4;
            continue;
        }
        mask >>= 1;
        if (((data[p + mask] + 1) & 0xFE) == 0) {
            mask |= 4;
            continue;
        }
        {
            uint32_t v = read_u32_le(data + p);
            v += 1U << 24;
            if ((v & 0xFE000000U) != 0) {
                mask |= 4;
                continue;
            }
            uint32_t c = pc + (uint32_t)p;
            v -= c;
            {
                mask <<= 3;
                if ((((uint8_t)(v >> mask) + 1) & 0xFE) == 0) {
                    v ^= (0x100U << mask) - 1;
                    v -= c;
                }
                mask = 0;
            }
            v &= (1U << 25) - 1;
            v -= 1U << 24;
            write_u32_le(data + p, v);
            p += 4;
            goto mainLoop;
        }

    mainLoop:
        if (p >= lim) break;
        for (;;) {
            b = data[p];
            if (b == 0xE8 || b == 0xE9) {
                p++;
                goto a3;
            }
            p++;
            if (p >= lim) goto fin;
        }

    a3:
        if (p > lim) break;
        {
            uint32_t v = read_u32_le(data + p);
            v += 1U << 24;
            if ((v & 0xFE000000U) != 0) {
                mask |= 4;
                goto start;
            }
            uint32_t c = pc + (uint32_t)p;
            v -= c;
            v &= (1U << 25) - 1;
            v -= 1U << 24;
            write_u32_le(data + p, v);
            p += 4;
            goto mainLoop;
        }
    }
fin:
    return;
}

/* In-place ARM64 B (0x14) / BL (0x94) affine branch target uncollimation */
static void arm64_filter_inverse(uint8_t* dst, size_t u_size) {
    size_t limit = (u_size >= 4) ? (u_size & ~3ULL) : 0;
    for (size_t i = 0; i < limit; i += 4) {
        uint32_t v = read_u32_le(dst + i);
        uint32_t op = v & 0xFC000000U;
        if (op == 0x14000000U || op == 0x94000000U) {
            uint32_t pc = (uint32_t)i;
            uint32_t dest = (v & 0x03FFFFFFU) - (pc >> 2);
            dest = (dest & 0x03FFFFFFU) | op;
            write_u32_le(dst + i, dest);
        }
    }
}

/* ============================================================================
 * 6. 2-Adic Mahler Ultrametric Inverse Pascal Wavelet
 * ============================================================================ */

PRZM_INLINE void mahler16_inverse(uint8_t* buf) {
    for (int s = 15; s >= 1; s--) {
        for (int i = s; i <= 15; i++) {
            buf[i] = (uint8_t)(buf[i] + buf[i - 1]);
        }
    }
}

/* ============================================================================
 * 7. Symplectic Phase-Space Transvection Involution
 * ============================================================================ */

typedef struct {
    uint8_t s00;
    uint8_t s01;
    uint8_t s11;
} SymplecticShear;

PRZM_INLINE void symplectic_shear_inplace(uint8_t* buf, size_t n, SymplecticShear shear) {
    if (shear.s00 == 0 && shear.s01 == 0 && shear.s11 == 0) return;
    size_t limit = (n / 4) * 4;
    for (size_t i = 0; i < limit; i += 4) {
        uint8_t q0 = buf[i];
        uint8_t q1 = buf[i + 1];
        uint8_t p0 = buf[i + 2];
        uint8_t p1 = buf[i + 3];

        buf[i]     = q0 ^ (gf256_mul(shear.s00, p0) ^ gf256_mul(shear.s01, p1));
        buf[i + 1] = q1 ^ (gf256_mul(shear.s01, p0) ^ gf256_mul(shear.s11, p1));
    }
}

/* ============================================================================
 * 8. Étale Sheaf 4-Phase Fiber Collation
 * ============================================================================ */

PRZM_INLINE void sheaf_collate4(uint8_t* dst, size_t n,
                           const uint8_t* f0, const uint8_t* f1,
                           const uint8_t* f2, const uint8_t* f3) {
    size_t i = 0, j = 0;
    for (; i + 16 <= n; i += 16, j += 4) {
        dst[i + 0]  = f0[j + 0]; dst[i + 1]  = f1[j + 0];
        dst[i + 2]  = f2[j + 0]; dst[i + 3]  = f3[j + 0];
        dst[i + 4]  = f0[j + 1]; dst[i + 5]  = f1[j + 1];
        dst[i + 6]  = f2[j + 1]; dst[i + 7]  = f3[j + 1];
        dst[i + 8]  = f0[j + 2]; dst[i + 9]  = f1[j + 2];
        dst[i + 10] = f2[j + 2]; dst[i + 11] = f3[j + 2];
        dst[i + 12] = f0[j + 3]; dst[i + 13] = f1[j + 3];
        dst[i + 14] = f2[j + 3]; dst[i + 15] = f3[j + 3];
    }
    for (; i < n; i++) {
        switch (i & 3) {
            case 0: dst[i] = f0[i >> 2]; break;
            case 1: dst[i] = f1[i >> 2]; break;
            case 2: dst[i] = f2[i >> 2]; break;
            case 3: dst[i] = f3[i >> 2]; break;
        }
    }
}

static int decode_trellis_payload(const uint8_t* payload, size_t payload_len, uint8_t* dst, size_t orig_len) {
    size_t src_ptr = 0;
    size_t dst_pos = 0;
    int rq[4] = {1, 4, 8, 16};

    while (src_ptr < payload_len && dst_pos < orig_len) {
        uint8_t token = payload[src_ptr++];
        if (token < 0x80) {
            size_t run_len = (size_t)token + 1;
            if (payload_len - src_ptr < run_len || orig_len - dst_pos < run_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst + dst_pos, payload + src_ptr, run_len);
            src_ptr += run_len;
            dst_pos += run_len;
        } else if (token < 0xC0) {
            int rep_idx = (token >> 4) & 3;
            int len_code = token & 0x0F;
            size_t m_len = (size_t)len_code + 2;
            if (len_code == 15) {
                int extra = 0;
                if (fast_read_varint(payload, payload_len, &src_ptr, &extra) != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 17 + (size_t)extra;
            }
            int d = rq[rep_idx];
            if (d <= 0 || dst_pos < (size_t)d || orig_len - dst_pos < m_len) return PRZM_ERR_CORRUPTED_FRAME;
            for (size_t k = 0; k < m_len; k++) {
                dst[dst_pos + k] = dst[dst_pos - d + k];
            }
            switch (rep_idx) {
                case 0: rq[0] = d; break;
                case 1: { int tmp = rq[0]; rq[0] = rq[1]; rq[1] = tmp; break; }
                case 2: { int r0 = rq[0], r1 = rq[1]; rq[0] = rq[2]; rq[1] = r0; rq[2] = r1; break; }
                case 3: { int r0 = rq[0], r1 = rq[1], r2 = rq[2]; rq[0] = rq[3]; rq[1] = r0; rq[2] = r1; rq[3] = r2; break; }
            }
            dst_pos += m_len;
        } else {
            int len_code = token & 0x1F;
            size_t m_len = (size_t)len_code + 3;
            if (payload_len - src_ptr < 2) return PRZM_ERR_CORRUPTED_FRAME;
            int d = (int)read_u16_le(payload + src_ptr);
            src_ptr += 2;
            if (len_code == 31) {
                int extra = 0;
                if (fast_read_varint(payload, payload_len, &src_ptr, &extra) != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 34 + (size_t)extra;
            }
            if (d <= 0 || dst_pos < (size_t)d || orig_len - dst_pos < m_len) return PRZM_ERR_CORRUPTED_FRAME;
            for (size_t k = 0; k < m_len; k++) {
                dst[dst_pos + k] = dst[dst_pos - d + k];
            }
            rq[3] = rq[2]; rq[2] = rq[1]; rq[1] = rq[0]; rq[0] = d;
            dst_pos += m_len;
        }
    }
    return (dst_pos == orig_len) ? PRZM_OK : PRZM_ERR_CORRUPTED_FRAME;
}

static int64_t decompress_sheaf(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
    uint8_t mode = src[4];
    size_t orig_len = (size_t)read_u32_le(src + 8);
    if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    if (mode == 0) {
        if (src_len < 12 || src_len - 12 < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, src + 12, orig_len);
        return (int64_t)orig_len;
    } else if (mode == 1) {
        if (decode_trellis_payload(src + 12, src_len - 12, dst, orig_len) != PRZM_OK) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        return (int64_t)orig_len;
    } else if (mode == 2) {
        if (src_len < 28) return PRZM_ERR_CORRUPTED_FRAME;
        size_t l0 = (size_t)read_u32_le(src + 12);
        size_t l1 = (size_t)read_u32_le(src + 16);
        size_t l2 = (size_t)read_u32_le(src + 20);
        size_t l3 = (size_t)read_u32_le(src + 24);
        if (l0 > orig_len || l1 > orig_len - l0 || l2 > orig_len - l0 - l1 || l3 != orig_len - l0 - l1 - l2) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        if (orig_len > sizeof(tls_split_scratch)) return PRZM_ERR_EXCEEDS_MAX_BLOCK;

        if (decode_trellis_payload(src + 28, src_len - 28, tls_split_scratch, orig_len) != PRZM_OK) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        const uint8_t* f0 = tls_split_scratch;
        const uint8_t* f1 = tls_split_scratch + l0;
        const uint8_t* f2 = tls_split_scratch + l0 + l1;
        const uint8_t* f3 = tls_split_scratch + l0 + l1 + l2;
        sheaf_collate4(dst, orig_len, f0, f1, f2, f3);
        return (int64_t)orig_len;
    }
    return PRZM_ERR_UNSUPPORTED_MODE;
}

/* ============================================================================
 * 9. Columnar Auto-Stride De-Transposition (CAST)
 * ============================================================================ */

static void untranspose_stride(const uint8_t* src, uint8_t* dst, size_t n, int s) {
    if (s <= 1 || n < (size_t)s) {
        if (src != dst) memcpy(dst, src, n);
        return;
    }
    size_t rows = n / s;
    size_t rem = n % s;
    size_t idx = 0;
    for (int col = 0; col < s; col++) {
        for (size_t r = 0; r < rows; r++) {
            dst[r * s + col] = src[idx++];
        }
    }
    if (rem > 0) {
        memcpy(dst + rows * s, src + idx, rem);
    }
}

/* ============================================================================
 * 10. Vector-State ANS (V-ANS) 4-Lane Interleaved Decoder
 * ============================================================================ */

#define VANS_M 4096
#define VANS_L 65536

typedef struct {
    uint16_t freq[256];
    uint16_t cdf[257];
    uint8_t  lookup[VANS_M];
} VANSModel;

static int deserialize_vans_matrix(const uint8_t* src, size_t src_len, VANSModel* model, size_t* out_read) {
    if (src_len < 2) return PRZM_ERR_CORRUPTED_FRAME;
    uint16_t row_mask = read_u16_le(src);
    size_t ptr = 2;

    memset(model->freq, 0, sizeof(model->freq));
    uint32_t sum = 0;

    for (int h = 0; h < 16; h++) {
        if ((row_mask & (1U << h)) == 0) continue;
        if (ptr + 2 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        uint16_t col_mask = read_u16_le(src + ptr);
        ptr += 2;

        for (int l = 0; l < 16; l++) {
            if ((col_mask & (1U << l)) == 0) continue;
            if (ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t b0 = src[ptr++];
            uint16_t w = 0;
            if ((b0 & 0x80) == 0) {
                w = (uint16_t)b0;
            } else {
                if (ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
                uint8_t b1 = src[ptr++];
                w = ((uint16_t)(b0 & 0x7F) << 8) | (uint16_t)b1;
            }
            model->freq[(h << 4) | l] = w;
            sum += w;
        }
    }

    if (sum != VANS_M) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t cdf = 0;
    for (int s = 0; s < 256; s++) {
        model->cdf[s] = (uint16_t)cdf;
        uint32_t f = model->freq[s];
        for (uint32_t j = 0; j < f; j++) {
            model->lookup[cdf + j] = (uint8_t)s;
        }
        cdf += f;
    }
    model->cdf[256] = (uint16_t)cdf;

    *out_read = ptr;
    return PRZM_OK;
}

static const uint8_t przm_sheaf_context_lut0[256] = {
    3, 3, 3, 3, 3, 3, 3, 3, 3, 0, 0, 3, 3, 0, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    0, 1, 1, 1, 3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 1, 1, 1, 1, 1, 1, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 1, 1, 1, 1, 2, 
    1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
    2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
};

static const uint8_t przm_sheaf_context_lut1[256] = {
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 0, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 0, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 1, 
    3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
};

static const uint8_t przm_sheaf_context_lut2[256] = {
    1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 1, 1, 0, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 1, 
    0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
    1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
};

PRZM_INLINE uint8_t przm_sheaf_order2_context(uint8_t p2, uint8_t p1) {
    if (p1 >= '0' && p1 <= '9') {
        return ((p2 >= '0' && p2 <= '9') || p2 == '.' || p2 == '-') ? 0 : 1;
    }
    if ((p1 >= 'a' && p1 <= 'z') || p1 == '_') {
        return ((p2 >= 'a' && p2 <= 'z') || p2 == '_') ? 2 : 3;
    }
    if (p1 == ' ' || p1 == '\t' || p1 == '\r' || p1 == '\n') return 0;
    return 1;
}

PRZM_INLINE void sheaf_context_interleave4_mode(
    uint8_t* dst, size_t n,
    const uint8_t* f0,
    const uint8_t* f1,
    const uint8_t* f2,
    const uint8_t* f3,
    uint8_t mode)
{
    const uint8_t* p0 = f0;
    const uint8_t* p1 = f1;
    const uint8_t* p2 = f2;
    const uint8_t* p3 = f3;
    uint8_t* d = dst;
    uint8_t* d_end = dst + n;
    uint8_t prev1 = 0;
    uint8_t prev2 = 0;

    const uint8_t* lut = przm_sheaf_context_lut0;
    if (mode == PRZM_CTX_MODE_JSON_DENSE) lut = przm_sheaf_context_lut1;
    else if (mode == PRZM_CTX_MODE_2PHASE) lut = przm_sheaf_context_lut2;

    if (mode == PRZM_CTX_MODE_ORDER2) {
        while (d < d_end) {
            uint8_t c = przm_sheaf_order2_context(prev2, prev1);
            uint8_t b;
            switch (c) {
            case 0: b = *p0++; break;
            case 1: b = *p1++; break;
            case 2: b = *p2++; break;
            default: b = *p3++; break;
            }
            *d++ = b;
            prev2 = prev1;
            prev1 = b;
        }
    } else {
        while (d + 8 <= d_end) {
            #define STEP(k) \
                uint8_t c##k = lut[prev1]; \
                uint8_t b##k; \
                switch (c##k) { \
                case 0: b##k = *p0++; break; \
                case 1: b##k = *p1++; break; \
                case 2: b##k = *p2++; break; \
                default: b##k = *p3++; break; \
                } \
                *d++ = b##k; \
                prev1 = b##k;

            STEP(0)
            STEP(1)
            STEP(2)
            STEP(3)
            STEP(4)
            STEP(5)
            STEP(6)
            STEP(7)
            #undef STEP
        }
        while (d < d_end) {
            uint8_t c = lut[prev1];
            uint8_t b;
            switch (c) {
            case 0: b = *p0++; break;
            case 1: b = *p1++; break;
            case 2: b = *p2++; break;
            default: b = *p3++; break;
            }
            *d++ = b;
            prev1 = b;
        }
    }
}

static const uint16_t przm_static_context_freq[8][256] = {
    { /* Context 0 */
        3, 2, 2, 2, 2, 2, 2, 2, 2, 19, 54, 2, 2, 2, 2, 2, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        168, 23, 21, 26, 9, 7, 69, 15, 163, 18, 94, 122, 14, 52, 11, 58, 
        84, 144, 68, 46, 76, 30, 23, 9, 43, 8, 14, 8, 73, 177, 50, 11, 
        3, 34, 34, 54, 25, 11, 23, 30, 27, 29, 9, 9, 40, 40, 14, 11, 
        37, 5, 25, 44, 22, 16, 9, 12, 7, 5, 7, 18, 12, 4, 22, 8, 
        12, 65, 75, 71, 51, 40, 69, 19, 27, 100, 18, 15, 37, 40, 29, 39, 
        68, 12, 54, 91, 75, 23, 39, 26, 8, 7, 7, 30, 82, 39, 4, 2, 
        3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 4, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 3, 4, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    },
    { /* Context 1 */
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 255, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        772, 14, 40, 7, 13, 3, 9, 46, 38, 90, 26, 32, 123, 30, 38, 18, 
        59, 150, 96, 69, 53, 36, 34, 24, 18, 18, 24, 133, 22, 42, 26, 5, 
        5, 16, 16, 24, 16, 13, 24, 11, 9, 13, 3, 3, 9, 26, 7, 11, 
        26, 5, 28, 13, 14, 11, 7, 13, 5, 5, 11, 13, 5, 30, 16, 22, 
        24, 28, 57, 57, 30, 18, 22, 18, 32, 79, 16, 7, 46, 32, 13, 26, 
        24, 11, 36, 57, 24, 30, 20, 14, 16, 14, 11, 11, 5, 36, 5, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        5, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 5, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    },
    { /* Context 2 */
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 64, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        1134, 10, 10, 3, 6, 4, 75, 10, 15, 59, 176, 68, 35, 44, 12, 77, 
        35, 21, 62, 26, 26, 10, 17, 8, 19, 8, 17, 73, 59, 236, 79, 10, 
        4, 33, 12, 19, 8, 15, 15, 6, 17, 10, 6, 4, 15, 26, 12, 15, 
        15, 4, 12, 26, 15, 8, 8, 15, 6, 6, 6, 24, 6, 28, 10, 19, 
        8, 21, 53, 46, 26, 21, 19, 12, 10, 35, 6, 6, 37, 17, 10, 28, 
        39, 3, 17, 48, 12, 12, 15, 8, 3, 8, 3, 3, 104, 37, 3, 3, 
        4, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 6, 3, 3, 3, 
        3, 3, 3, 3, 4, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4, 
    },
    { /* Context 3 */
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 11, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        204, 3, 4, 2, 6, 4, 4, 6, 11, 14, 4, 8, 17, 33, 7, 10, 
        21, 22, 13, 17, 16, 11, 12, 13, 15, 11, 6, 6, 6, 7, 4, 3, 
        2, 7, 10, 9, 3, 2, 6, 4, 4, 4, 2, 2, 6, 6, 2, 3, 
        10, 4, 3, 7, 4, 2, 2, 2, 3, 2, 2, 5, 3, 15, 2, 28, 
        6, 104, 49, 178, 200, 51, 131, 70, 9, 76, 4, 20, 274, 134, 485, 69, 
        110, 11, 327, 252, 279, 49, 62, 31, 37, 15, 31, 2, 2, 7, 2, 2, 
        2, 2, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
    },
    { /* Context 4 */
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 21, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        418, 8, 11, 3, 3, 5, 2, 3, 27, 39, 16, 15, 47, 31, 16, 13, 
        40, 39, 24, 27, 22, 17, 19, 12, 22, 8, 7, 19, 6, 7, 4, 2, 
        2, 5, 4, 7, 4, 4, 7, 6, 6, 3, 2, 2, 6, 7, 3, 4, 
        5, 2, 7, 4, 4, 2, 4, 4, 2, 2, 2, 19, 2, 9, 2, 90, 
        8, 263, 33, 95, 85, 491, 47, 42, 79, 273, 6, 22, 106, 55, 37, 216, 
        55, 5, 141, 141, 208, 101, 16, 11, 10, 64, 16, 4, 4, 7, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 3, 1, 1, 1, 1, 1, 1, 1, 2, 1, 1, 1, 
        1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
        1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 
        1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 1, 1, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
    },
    { /* Context 5 */
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 7, 2, 2, 2, 2, 2, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
        194, 4, 11, 3, 5, 8, 3, 34, 17, 44, 7, 15, 24, 25, 5, 11, 
        21, 58, 34, 21, 36, 29, 19, 7, 17, 15, 17, 16, 4, 5, 8, 4, 
        3, 152, 48, 101, 116, 209, 58, 54, 39, 94, 12, 20, 131, 82, 98, 112, 
        79, 11, 159, 163, 127, 65, 23, 42, 32, 17, 24, 3, 5, 4, 7, 146, 
        5, 86, 8, 11, 12, 97, 5, 7, 38, 70, 2, 2, 27, 7, 39, 98, 
        35, 2, 48, 25, 19, 51, 12, 5, 12, 17, 2, 7, 5, 12, 2, 2, 
        2, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 7, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 3, 3, 3, 3, 
        3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 
    },
    { /* Context 6 */
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 16, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        159, 6, 4, 2, 6, 6, 2, 6, 15, 73, 7, 10, 50, 25, 88, 19, 
        233, 335, 378, 310, 360, 190, 234, 155, 188, 133, 7, 41, 6, 6, 5, 3, 
        2, 13, 12, 16, 23, 11, 23, 10, 8, 6, 2, 4, 2, 5, 5, 5, 
        12, 2, 4, 14, 6, 17, 8, 4, 6, 2, 2, 15, 2, 41, 6, 34, 
        4, 31, 36, 33, 30, 28, 28, 3, 3, 19, 3, 6, 4, 7, 6, 9, 
        11, 2, 12, 16, 4, 6, 6, 6, 28, 2, 2, 2, 4, 19, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 6, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
        2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 
    },
    { /* Context 7 */
        6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 20, 6, 6, 6, 6, 6, 
        6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 
        163, 6, 10, 44, 6, 6, 10, 6, 25, 53, 15, 6, 29, 20, 20, 15, 
        39, 29, 29, 25, 20, 25, 20, 6, 10, 6, 39, 6, 6, 25, 6, 6, 
        6, 6, 53, 29, 25, 15, 34, 15, 48, 25, 6, 6, 48, 53, 15, 29, 
        58, 10, 39, 68, 25, 15, 15, 15, 6, 6, 6, 15, 25, 6, 6, 72, 
        53, 39, 72, 96, 77, 48, 48, 15, 20, 101, 6, 25, 106, 63, 34, 25, 
        48, 6, 15, 130, 234, 53, 39, 6, 15, 6, 6, 20, 6, 6, 6, 6, 
        48, 15, 6, 10, 6, 6, 6, 6, 6, 10, 6, 6, 6, 6, 6, 6, 
        6, 6, 6, 34, 25, 6, 6, 6, 6, 6, 6, 6, 10, 6, 6, 6, 
        10, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5, 
        5, 5, 5, 5, 5, 5, 20, 5, 10, 10, 10, 5, 5, 5, 5, 5, 
        6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 
        20, 20, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 
        6, 6, 15, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 
        6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 
    },
};

static uint8_t przm_sheaf_context8_lut[256];
static VANSModel przm_static_context_models[8];
static volatile int przm_static_context_models_initialized = 0;

static void przm_init_static_context_models(void) {
    if (przm_static_context_models_initialized) return;

    for (int i = 0; i < 256; i++) {
        uint8_t b = (uint8_t)i;
        uint8_t cls = 7;
        if (b == ' ' || b == '\t' || b == '\n' || b == '\r') {
            cls = 0;
        } else if (b == '(' || b == ')' || b == '{' || b == '}' || b == '[' || b == ']' ||
                   b == ';' || b == ',' || b == '.' || b == ':' || b == '"' || b == '\'') {
            cls = 1;
        } else if (b == '=' || b == '+' || b == '-' || b == '*' || b == '/' || b == '%' ||
                   b == '&' || b == '|' || b == '^' || b == '!' || b == '<' || b == '>' || b == '~' || b == '?') {
            cls = 2;
        } else if (b == 'a' || b == 'e' || b == 'i' || b == 'o' || b == 'u') {
            cls = 3;
        } else if (b >= 'a' && b <= 'z') {
            cls = 4;
        } else if (b >= 'A' && b <= 'Z') {
            cls = 5;
        } else if (b >= '0' && b <= '9') {
            cls = 6;
        }
        przm_sheaf_context8_lut[i] = cls;
    }

    for (int c = 0; c < 8; c++) {
        memcpy(przm_static_context_models[c].freq, przm_static_context_freq[c], sizeof(przm_static_context_freq[c]));
        uint32_t cdf = 0;
        for (int s = 0; s < 256; s++) {
            przm_static_context_models[c].cdf[s] = (uint16_t)cdf;
            uint32_t f = przm_static_context_models[c].freq[s];
            for (uint32_t j = 0; j < f; j++) {
                przm_static_context_models[c].lookup[cdf + j] = (uint8_t)s;
            }
            cdf += f;
        }
        przm_static_context_models[c].cdf[256] = (uint16_t)cdf;
    }

    przm_static_context_models_initialized = 1;
}

#include "przm_order2_tables.h"

static VANSModel przm_static_context_models_order2[64];
static int przm_static_context_models_order2_initialized = 0;

static void przm_init_static_context_models_order2(void) {
    if (przm_static_context_models_order2_initialized) return;

    for (int c = 0; c < 64; c++) {
        uint32_t cdf = 0;
        for (int s = 0; s < 256; s++) {
            uint16_t f = przm_static_context_freq_order2[c][s];
            przm_static_context_models_order2[c].freq[s] = f;
            przm_static_context_models_order2[c].cdf[s] = (uint16_t)cdf;
            for (uint32_t j = 0; j < f; j++) {
                przm_static_context_models_order2[c].lookup[cdf + j] = (uint8_t)s;
            }
            cdf += f;
        }
        przm_static_context_models_order2[c].cdf[256] = (uint16_t)cdf;
    }

    przm_static_context_models_order2_initialized = 1;
}

static int vans_decode4x(const uint8_t* src, size_t src_len, uint8_t* dst, size_t orig_len) {
    if (src_len < 8) return PRZM_ERR_CORRUPTED_FRAME;
    if (src[0] != PRZM_MAGIC_VANS_0 || src[1] != PRZM_MAGIC_VANS_1 || src[2] != PRZM_VERSION_1) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    uint8_t flags = src[3];
    if ((flags & PRZM_VANS_FLAG_CTX_SHEAF) == 0 && (flags & PRZM_VANS_FLAG_CTX_ORDER2) != 0) {
        if (src_len < 24) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t header_orig = read_u32_le(src + 4);
        if (header_orig != (uint32_t)orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (orig_len == 0) return PRZM_OK;

        przm_init_static_context_models_order2();

        uint32_t x[4];
        x[0] = read_u32_le(src + 8);
        x[1] = read_u32_le(src + 12);
        x[2] = read_u32_le(src + 16);
        x[3] = read_u32_le(src + 20);

        size_t stream_ptr = 24;
        uint8_t prev2 = 0;
        uint8_t prev1 = 0;

        size_t i = 0;
        size_t limit = (orig_len >= 4) ? orig_len - 3 : 0;
        for (; i < limit; i += 4) {
            #define DECODE_CTX2_LANE(lane, offset) do { \
                uint8_t ctx = (przm_sheaf_context_lut4[prev2] << 4) | przm_sheaf_context_lut16[prev1]; \
                const VANSModel* m = &przm_static_context_models_order2[ctx]; \
                uint32_t slot = x[lane] & (VANS_M - 1); \
                uint8_t s = m->lookup[slot]; \
                dst[i + (offset)] = s; \
                prev2 = prev1; \
                prev1 = s; \
                uint32_t f = m->freq[s]; \
                uint32_t c = m->cdf[s]; \
                x[lane] = f * (x[lane] >> 12) + (slot - c); \
                while (x[lane] < VANS_L) { \
                    if (stream_ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME; \
                    x[lane] = (x[lane] << 8) | (uint32_t)src[stream_ptr++]; \
                } \
            } while (0)

            DECODE_CTX2_LANE(0, 0);
            DECODE_CTX2_LANE(1, 1);
            DECODE_CTX2_LANE(2, 2);
            DECODE_CTX2_LANE(3, 3);
            #undef DECODE_CTX2_LANE
        }
        for (; i < orig_len; i++) {
            int lane = (int)(i & 3);
            uint8_t ctx = (przm_sheaf_context_lut4[prev2] << 4) | przm_sheaf_context_lut16[prev1];
            const VANSModel* m = &przm_static_context_models_order2[ctx];
            uint32_t slot = x[lane] & (VANS_M - 1);
            uint8_t s = m->lookup[slot];
            dst[i] = s;
            prev2 = prev1;
            prev1 = s;
            uint32_t f = m->freq[s];
            uint32_t c = m->cdf[s];
            x[lane] = f * (x[lane] >> 12) + (slot - c);
            while (x[lane] < VANS_L) {
                if (stream_ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
                x[lane] = (x[lane] << 8) | (uint32_t)src[stream_ptr++];
            }
        }

        return PRZM_OK;
    }
    if ((flags & PRZM_VANS_FLAG_CTX_ORDER1) != 0) {
        if (src_len < 24) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t header_orig = read_u32_le(src + 4);
        if (header_orig != (uint32_t)orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (orig_len == 0) return PRZM_OK;

        przm_init_static_context_models();

        uint32_t x[4];
        x[0] = read_u32_le(src + 8);
        x[1] = read_u32_le(src + 12);
        x[2] = read_u32_le(src + 16);
        x[3] = read_u32_le(src + 20);

        size_t stream_ptr = 24;
        uint8_t prev = 0;

        size_t i = 0;
        size_t limit = (orig_len >= 4) ? orig_len - 3 : 0;
        for (; i < limit; i += 4) {
            #define DECODE_CTX_LANE(lane, offset) do { \
                uint8_t ctx = przm_sheaf_context8_lut[prev]; \
                const VANSModel* m = &przm_static_context_models[ctx]; \
                uint32_t slot = x[lane] & (VANS_M - 1); \
                uint8_t s = m->lookup[slot]; \
                dst[i + (offset)] = s; \
                prev = s; \
                uint32_t f = m->freq[s]; \
                uint32_t c = m->cdf[s]; \
                x[lane] = f * (x[lane] >> 12) + (slot - c); \
                while (x[lane] < VANS_L) { \
                    if (stream_ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME; \
                    x[lane] = (x[lane] << 8) | (uint32_t)src[stream_ptr++]; \
                } \
            } while (0)

            DECODE_CTX_LANE(0, 0);
            DECODE_CTX_LANE(1, 1);
            DECODE_CTX_LANE(2, 2);
            DECODE_CTX_LANE(3, 3);
            #undef DECODE_CTX_LANE
        }
        for (; i < orig_len; i++) {
            int lane = (int)(i & 3);
            uint8_t ctx = przm_sheaf_context8_lut[prev];
            const VANSModel* m = &przm_static_context_models[ctx];
            uint32_t slot = x[lane] & (VANS_M - 1);
            uint8_t s = m->lookup[slot];
            dst[i] = s;
            prev = s;
            uint32_t f = m->freq[s];
            uint32_t c = m->cdf[s];
            x[lane] = f * (x[lane] >> 12) + (slot - c);
            while (x[lane] < VANS_L) {
                if (stream_ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
                x[lane] = (x[lane] << 8) | (uint32_t)src[stream_ptr++];
            }
        }

        return PRZM_OK;
    }
    if ((flags & PRZM_VANS_FLAG_CTX_SHEAF) != 0) {
        if (src_len < 40) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t header_orig = read_u32_le(src + 4);
        if (header_orig != (uint32_t)orig_len) return PRZM_ERR_CORRUPTED_FRAME;

        uint32_t flen0 = read_u32_le(src + 8);
        uint32_t flen1 = read_u32_le(src + 12);
        uint32_t flen2 = read_u32_le(src + 16);
        uint32_t flen3 = read_u32_le(src + 20);

        uint32_t enclen0 = read_u32_le(src + 24);
        uint32_t enclen1 = read_u32_le(src + 28);
        uint32_t enclen2 = read_u32_le(src + 32);
        uint32_t enclen3 = read_u32_le(src + 36);

        if ((size_t)flen0 + flen1 + flen2 + flen3 != orig_len) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        size_t total_payload = 40 + (size_t)enclen0 + enclen1 + enclen2 + enclen3;
        if (total_payload > src_len) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }

        uint8_t* fiber_scratch = (dst == tls_archival_scratch) ? tls_split_scratch : tls_archival_scratch;
        if (orig_len > sizeof(tls_archival_scratch)) {
            return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        }

        uint8_t* f0 = fiber_scratch;
        uint8_t* f1 = f0 + flen0;
        uint8_t* f2 = f1 + flen1;
        uint8_t* f3 = f2 + flen2;

        size_t off = 40;
        if (flen0 > 0) {
            if (enclen0 == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (enclen0 == flen0) {
                memcpy(f0, src + off, flen0);
            } else {
                int err = vans_decode4x(src + off, enclen0, f0, flen0);
                if (err != PRZM_OK) return err;
            }
            off += enclen0;
        }
        if (flen1 > 0) {
            if (enclen1 == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (enclen1 == flen1) {
                memcpy(f1, src + off, flen1);
            } else {
                int err = vans_decode4x(src + off, enclen1, f1, flen1);
                if (err != PRZM_OK) return err;
            }
            off += enclen1;
        }
        if (flen2 > 0) {
            if (enclen2 == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (enclen2 == flen2) {
                memcpy(f2, src + off, flen2);
            } else {
                int err = vans_decode4x(src + off, enclen2, f2, flen2);
                if (err != PRZM_OK) return err;
            }
            off += enclen2;
        }
        if (flen3 > 0) {
            if (enclen3 == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (enclen3 == flen3) {
                memcpy(f3, src + off, flen3);
            } else {
                int err = vans_decode4x(src + off, enclen3, f3, flen3);
                if (err != PRZM_OK) return err;
            }
            off += enclen3;
        }

        uint8_t mode = (flags >> PRZM_CTX_MODE_SHIFT) & 0x0F;
        sheaf_context_interleave4_mode(dst, orig_len, f0, f1, f2, f3, mode);
        return PRZM_OK;
    }

    if ((flags & PRZM_VANS_FLAG_SHEAF) != 0) {
        if (src_len < 24) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t header_orig = read_u32_le(src + 4);
        if (header_orig != (uint32_t)orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (orig_len == 0) return PRZM_OK;

        uint32_t slen = read_u32_le(src + 8);
        uint32_t mask_len = read_u32_le(src + 12);
        uint32_t enc_slen = read_u32_le(src + 16);
        uint32_t enc_clen = read_u32_le(src + 20);

        uint32_t expected_mask_len = (uint32_t)((orig_len + 7) / 8);
        if (mask_len != expected_mask_len || slen > orig_len) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        uint32_t clen = (uint32_t)orig_len - slen;
        if (24 + (size_t)mask_len + enc_slen + enc_clen > src_len) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }

        const uint8_t* mask = src + 24;
        size_t full_bytes = orig_len / 8;
        size_t rem_bits = orig_len % 8;
        size_t ones = 0;
        for (size_t i = 0; i < full_bytes; i++) {
            ones += (size_t)__builtin_popcount((unsigned int)mask[i]);
        }
        if (rem_bits > 0) {
            uint8_t last_byte = mask[full_bytes];
            if ((last_byte >> rem_bits) != 0) return PRZM_ERR_CORRUPTED_FRAME;
            ones += (size_t)__builtin_popcount((unsigned int)last_byte);
        }
        if (ones != slen) return PRZM_ERR_CORRUPTED_FRAME;

        uint8_t* sheaf_scratch = (dst == tls_archival_scratch) ? tls_split_scratch : tls_archival_scratch;
        if (orig_len > sizeof(tls_archival_scratch)) {
            return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        }

        uint8_t* s_buf = sheaf_scratch;
        uint8_t* c_buf = sheaf_scratch + slen;

        const uint8_t* enc_s = src + 24 + mask_len;
        const uint8_t* enc_c = enc_s + enc_slen;

        if (slen > 0) {
            if (enc_slen == 0) return PRZM_ERR_CORRUPTED_FRAME;
            int err = vans_decode4x(enc_s, enc_slen, s_buf, slen);
            if (err != PRZM_OK) return err;
        }
        if (clen > 0) {
            if (enc_clen == 0) return PRZM_ERR_CORRUPTED_FRAME;
            int err = vans_decode4x(enc_c, enc_clen, c_buf, clen);
            if (err != PRZM_OK) return err;
        }

        size_t s_pos = 0, c_pos = 0, m_pos = 0;
        int bit = 0;
        for (size_t i = 0; i < orig_len; i++) {
            if ((mask[m_pos] >> bit) & 1) {
                dst[i] = s_buf[s_pos++];
            } else {
                dst[i] = c_buf[c_pos++];
            }
            bit++;
            if (bit == 8) {
                bit = 0;
                m_pos++;
            }
        }
        return PRZM_OK;
    }

    VANSModel model;
    size_t mat_read = 0;
    int err = deserialize_vans_matrix(src + 8, src_len - 8, &model, &mat_read);
    if (err != PRZM_OK) return err;

    size_t ptr = 8 + mat_read;
    if (ptr + 16 > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t x[4];
    x[0] = read_u32_le(src + ptr + 0);
    x[1] = read_u32_le(src + ptr + 4);
    x[2] = read_u32_le(src + ptr + 8);
    x[3] = read_u32_le(src + ptr + 12);
    ptr += 16;

    size_t stream_ptr = ptr;

    for (size_t i = 0; i < orig_len; i++) {
        int lane = (int)(i & 3);
        uint32_t slot = x[lane] & (VANS_M - 1);
        uint8_t s = model.lookup[slot];
        dst[i] = s;

        uint32_t f = model.freq[s];
        uint32_t c = model.cdf[s];

        x[lane] = f * (x[lane] >> 12) + (slot - c);

        while (x[lane] < VANS_L) {
            if (stream_ptr >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
            x[lane] = (x[lane] << 8) | (uint32_t)src[stream_ptr++];
        }
    }

    return PRZM_OK;
}

/* ============================================================================
 * 11. Nibble-Plane Splitting Decoder
 * ============================================================================ */

static int unpack_nibble_planes(const uint8_t* src, size_t src_len, uint8_t* dst, size_t orig_len) {
    if (src_len < 8) return PRZM_ERR_CORRUPTED_FRAME;
    size_t num_lits = (size_t)read_u32_le(src);
    size_t low_bytes = (size_t)read_u32_le(src + 4);
    size_t expected_low = (num_lits + 1) / 2;

    if (num_lits != orig_len || low_bytes != expected_low || src_len - 8 < low_bytes) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    const uint8_t* low_plane = src + 8;
    const uint8_t* high_plane = src + 8 + low_bytes;
    size_t high_len = src_len - (8 + low_bytes);

    /* 1. High-nibble run unrolling */
    size_t p = 0;
    for (size_t i = 0; i < high_len && p < num_lits; i++) {
        uint8_t token = high_plane[i];
        size_t run_len = (size_t)(token >> 4) + 1;
        if (num_lits - p < run_len) return PRZM_ERR_CORRUPTED_FRAME;
        uint8_t h = (token & 0x0F) << 4;
        memset(dst + p, h, run_len);
        p += run_len;
    }
    if (p != num_lits) return PRZM_ERR_CORRUPTED_FRAME;

    /* 2. Low-nibble plane merge */
    for (size_t j = 0; j < low_bytes; j++) {
        uint8_t pair = low_plane[j];
        size_t idx = j * 2;
        dst[idx] |= (pair >> 4);
        if (idx + 1 < num_lits) {
            dst[idx + 1] |= (pair & 0x0F);
        }
    }

    return PRZM_OK;
}

/* ============================================================================
 * 12. Pareto-Wave Synthesis (PWS) 5-Layer H-DAG Decoder
 * ============================================================================ */

#define MAX_L1 64
#define MAX_L2 32
#define MAX_L3 16
#define MAX_L4 16
#define MAX_L5 8

typedef struct {
    uint16_t exp_l1[MAX_L1];
    uint32_t exp_l2[MAX_L2];
    uint64_t exp_l3[MAX_L3];
    uint8_t  exp_l4[MAX_L4][16];
    uint8_t  exp_l5[MAX_L5][32];
} PWSGrammar;

static int pws_decode(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len < 20) return PRZM_ERR_CORRUPTED_FRAME;
    if (src[0] != PRZM_MAGIC_PWS_0 || src[1] != PRZM_MAGIC_PWS_1 || src[2] != PRZM_VERSION_1) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    int stride = (int)src[3];
    size_t orig_size = (size_t)read_u32_le(src + 4);
    if (orig_size > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < orig_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    size_t num_l1 = src[12];
    size_t num_l2 = src[13];
    size_t num_l3 = src[14];
    size_t num_l4 = src[15];
    size_t num_l5 = src[16];

    if (num_l1 > MAX_L1 || num_l2 > MAX_L2 || num_l3 > MAX_L3 ||
        num_l4 > MAX_L4 || num_l5 > MAX_L5) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    size_t ptr = 20;
    size_t rules_size = (num_l1 + num_l2 + num_l3 + num_l4 + num_l5) * 2;
    if (ptr + rules_size > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    PWSGrammar g;
    memset(&g, 0, sizeof(g));

    /* Build L1 (2B) */
    for (size_t i = 0; i < num_l1; i++, ptr += 2) {
        g.exp_l1[i] = (uint16_t)src[ptr] | ((uint16_t)src[ptr + 1] << 8);
    }
    /* Build L2 (4B) */
    for (size_t j = 0; j < num_l2; j++, ptr += 2) {
        uint8_t a = src[ptr], b = src[ptr + 1];
        if (a >= num_l1 || b >= num_l1) return PRZM_ERR_CORRUPTED_FRAME;
        g.exp_l2[j] = (uint32_t)g.exp_l1[a] | ((uint32_t)g.exp_l1[b] << 16);
    }
    /* Build L3 (8B) */
    for (size_t k = 0; k < num_l3; k++, ptr += 2) {
        uint8_t a = src[ptr], b = src[ptr + 1];
        if (a >= num_l2 || b >= num_l2) return PRZM_ERR_CORRUPTED_FRAME;
        g.exp_l3[k] = (uint64_t)g.exp_l2[a] | ((uint64_t)g.exp_l2[b] << 32);
    }
    /* Build L4 (16B) */
    for (size_t m = 0; m < num_l4; m++, ptr += 2) {
        uint8_t a = src[ptr], b = src[ptr + 1];
        if (a >= num_l3 || b >= num_l3) return PRZM_ERR_CORRUPTED_FRAME;
        write_u64_le(g.exp_l4[m], g.exp_l3[a]);
        write_u64_le(g.exp_l4[m] + 8, g.exp_l3[b]);
    }
    /* Build L5 (32B) */
    for (size_t n = 0; n < num_l5; n++, ptr += 2) {
        uint8_t a = src[ptr], b = src[ptr + 1];
        if (a >= num_l4 || b >= num_l4) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(g.exp_l5[n], g.exp_l4[a], 16);
        memcpy(g.exp_l5[n] + 16, g.exp_l4[b], 16);
    }

    const uint8_t* ax_stream = src + ptr;
    size_t ax_len = src_len - ptr;
    size_t ax_ptr = 0;
    size_t dst_pos = 0;

    while (ax_ptr < ax_len && dst_pos < orig_size) {
        uint8_t op = ax_stream[ax_ptr++];
        if (op < 0xC0) {
            dst[dst_pos++] = op;
            continue;
        }
        if (op >= 0xF4 && op <= 0xFB) {
            size_t id = op - 0xF4;
            if (id >= num_l5 || orig_size - dst_pos < 32) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst + dst_pos, g.exp_l5[id], 32);
            dst_pos += 32;
            continue;
        }
        if (op >= 0xEC && op <= 0xF3) {
            size_t id = op - 0xEC;
            if (id >= num_l4 || orig_size - dst_pos < 16) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst + dst_pos, g.exp_l4[id], 16);
            dst_pos += 16;
            continue;
        }
        if (op >= 0xE0 && op <= 0xEB) {
            size_t id = op - 0xE0;
            if (id >= num_l3 || orig_size - dst_pos < 8) return PRZM_ERR_CORRUPTED_FRAME;
            write_u64_le(dst + dst_pos, g.exp_l3[id]);
            dst_pos += 8;
            continue;
        }
        if (op >= 0xD0 && op <= 0xDF) {
            size_t id = op - 0xD0;
            if (id >= num_l2 || orig_size - dst_pos < 4) return PRZM_ERR_CORRUPTED_FRAME;
            write_u32_le(dst + dst_pos, g.exp_l2[id]);
            dst_pos += 4;
            continue;
        }
        if (op >= 0xC0 && op <= 0xCF) {
            size_t id = op - 0xC0;
            if (id >= num_l1 || orig_size - dst_pos < 2) return PRZM_ERR_CORRUPTED_FRAME;
            write_u16_le(dst + dst_pos, g.exp_l1[id]);
            dst_pos += 2;
            continue;
        }
        if (op == 0xFF) {
            if (ax_ptr >= ax_len || orig_size - dst_pos < 1) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = ax_stream[ax_ptr++];
            continue;
        }
        if (op == 0xFC) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            size_t id = ax_stream[ax_ptr++];
            if (id >= num_l1 || orig_size - dst_pos < 2) return PRZM_ERR_CORRUPTED_FRAME;
            write_u16_le(dst + dst_pos, g.exp_l1[id]);
            dst_pos += 2;
            continue;
        }
        if (op == 0xFD) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            size_t id = ax_stream[ax_ptr++];
            if (id >= num_l2 || orig_size - dst_pos < 4) return PRZM_ERR_CORRUPTED_FRAME;
            write_u32_le(dst + dst_pos, g.exp_l2[id]);
            dst_pos += 4;
            continue;
        }
        if (op == 0xFE) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            size_t id = ax_stream[ax_ptr++];
            if (id < 32) {
                if (id >= num_l3 || orig_size - dst_pos < 8) return PRZM_ERR_CORRUPTED_FRAME;
                write_u64_le(dst + dst_pos, g.exp_l3[id]);
                dst_pos += 8;
            } else if (id < 48) {
                size_t r = id - 32;
                if (r >= num_l4 || orig_size - dst_pos < 16) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(dst + dst_pos, g.exp_l4[r], 16);
                dst_pos += 16;
            } else {
                size_t r = id - 48;
                if (r >= num_l5 || orig_size - dst_pos < 32) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(dst + dst_pos, g.exp_l5[r], 32);
                dst_pos += 32;
            }
            continue;
        }
    }

    if (dst_pos != orig_size) return PRZM_ERR_CORRUPTED_FRAME;

    if (stride >= 2) {
        if (orig_size > PRZM_TLS_SCRATCH_SIZE) {
            return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        }
        memcpy(tls_split_scratch, dst, orig_size);
        untranspose_stride(tls_split_scratch, dst, orig_size, stride);
    }

    return (int64_t)orig_size;
}

/* ============================================================================
 * 12c. Grammar-Graph Tensor Compression (GGTC) 3-Layer Stratified H-DAG Decoder
 * ============================================================================ */

#define GGTC_MAX_L1 64
#define GGTC_MAX_L2 32
#define GGTC_MAX_L3 16

int64_t przm_ggtc_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len == 0) return 0;
    if (src == NULL || dst == NULL || src_len < 16) return PRZM_ERR_CORRUPTED_FRAME;

    if (src[0] != PRZM_MAGIC_GGTC_0 || src[1] != PRZM_MAGIC_GGTC_1 || src[2] != PRZM_VERSION_1) {
        return PRZM_ERR_INVALID_MAGIC;
    }

    uint32_t orig_size = read_u32_le(src + 4);
    if (orig_size > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (orig_size == 0) return 0;
    if (dst_cap < (size_t)orig_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    /* Axiom token count from header */
    uint32_t axiom_len = read_u32_le(src + 8);
    (void)axiom_len;

    uint8_t num_l1 = src[12];
    uint8_t num_l2 = src[13];
    uint8_t num_l3 = src[14];

    if (num_l1 > GGTC_MAX_L1 || num_l2 > GGTC_MAX_L2 || num_l3 > GGTC_MAX_L3) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    size_t rules_size = ((size_t)num_l1 + (size_t)num_l2 + (size_t)num_l3) * 2;
    if (16 + rules_size > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    uint16_t exp_l1[GGTC_MAX_L1];
    uint32_t exp_l2[GGTC_MAX_L2];
    uint64_t exp_l3[GGTC_MAX_L3];

    size_t ptr = 16;

    /* 1. Build Layer 1 (2 bytes = [U, V]) */
    for (uint8_t i = 0; i < num_l1; i++) {
        uint8_t u = src[ptr];
        uint8_t v = src[ptr + 1];
        exp_l1[i] = (uint16_t)u | ((uint16_t)v << 8);
        ptr += 2;
    }

    /* 2. Build Layer 2 (4 bytes = 2 * L1) */
    for (uint8_t j = 0; j < num_l2; j++) {
        uint8_t l1a = src[ptr];
        uint8_t l1b = src[ptr + 1];
        if (l1a >= num_l1 || l1b >= num_l1) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t w0 = (uint32_t)exp_l1[l1a];
        uint32_t w1 = (uint32_t)exp_l1[l1b];
        exp_l2[j] = w0 | (w1 << 16);
        ptr += 2;
    }

    /* 3. Build Layer 3 (8 bytes = 2 * L2) */
    for (uint8_t k = 0; k < num_l3; k++) {
        uint8_t l2a = src[ptr];
        uint8_t l2b = src[ptr + 1];
        if (l2a >= num_l2 || l2b >= num_l2) return PRZM_ERR_CORRUPTED_FRAME;
        uint64_t d0 = (uint64_t)exp_l2[l2a];
        uint64_t d1 = (uint64_t)exp_l2[l2b];
        exp_l3[k] = d0 | (d1 << 32);
        ptr += 2;
    }

    /* Zero-Stack SIMD Wave Expansion of Axiom Token Stream */
    const uint8_t* ax_stream = src + ptr;
    size_t ax_len = src_len - ptr;
    size_t ax_ptr = 0;
    size_t dst_pos = 0;

    while (ax_ptr < ax_len) {
        uint8_t op = ax_stream[ax_ptr++];

        if (op < 0xD0) {
            if (dst_pos + 1 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = op;
            continue;
        }

        if (op >= 0xF0 && op <= 0xFB) {
            uint8_t id = op - 0xF0;
            if (id >= num_l3 || dst_pos + 8 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            write_u64_le(dst + dst_pos, exp_l3[id]);
            dst_pos += 8;
            continue;
        }

        if (op >= 0xE0 && op <= 0xEF) {
            uint8_t id = op - 0xE0;
            if (id >= num_l2 || dst_pos + 4 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            write_u32_le(dst + dst_pos, exp_l2[id]);
            dst_pos += 4;
            continue;
        }

        if (op >= 0xD0 && op <= 0xDF) {
            uint8_t id = op - 0xD0;
            if (id >= num_l1 || dst_pos + 2 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            write_u16_le(dst + dst_pos, exp_l1[id]);
            dst_pos += 2;
            continue;
        }

        if (op == 0xFF) {
            if (ax_ptr >= ax_len || dst_pos + 1 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = ax_stream[ax_ptr++];
            continue;
        }

        if (op == 0xFE) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t id = ax_stream[ax_ptr++];
            if (id >= num_l3 || dst_pos + 8 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            write_u64_le(dst + dst_pos, exp_l3[id]);
            dst_pos += 8;
            continue;
        }

        if (op == 0xFD) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t id = ax_stream[ax_ptr++];
            if (id >= num_l2 || dst_pos + 4 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            write_u32_le(dst + dst_pos, exp_l2[id]);
            dst_pos += 4;
            continue;
        }

        if (op == 0xFC) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t id = ax_stream[ax_ptr++];
            if (id >= num_l1 || dst_pos + 2 > orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            write_u16_le(dst + dst_pos, exp_l1[id]);
            dst_pos += 2;
            continue;
        }

        return PRZM_ERR_CORRUPTED_FRAME;
    }

    if (dst_pos != orig_size) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    return (int64_t)orig_size;
}

/* ============================================================================
 * 12b. Galois Superoptimized JIT & Trace Annihilation (GCTR) Inverse Engine
 * ============================================================================ */

static void przm_gctr_inverse_inplace(uint8_t* dst, size_t n, uint8_t op_id) {
    switch (op_id) {
        case 0: /* OpGCTRBypass */
            break;

        case 1: /* OpGCTXORDelta1 */
            for (size_t i = 1; i < n; i++) {
                dst[i] ^= dst[i - 1];
            }
            break;

        case 2: /* OpGCTXORDelta2 */
            if (n >= 2) {
                size_t i = 2;
                uint16_t prev = read_u16_le(dst);
                for (; i + 2 <= n; i += 2) {
                    uint16_t curr = read_u16_le(dst + i);
                    prev ^= curr;
                    write_u16_le(dst + i, prev);
                }
                for (; i < n; i++) {
                    dst[i] ^= dst[i - 2];
                }
            }
            break;

        case 3: /* OpGCTXORDelta4 */
            if (n >= 4) {
                size_t i = 4;
                uint32_t prev = read_u32_le(dst);
                for (; i + 4 <= n; i += 4) {
                    uint32_t curr = read_u32_le(dst + i);
                    prev ^= curr;
                    write_u32_le(dst + i, prev);
                }
                for (; i < n; i++) {
                    dst[i] ^= dst[i - 4];
                }
            }
            break;

        case 4: /* OpGCTXORDelta8 */
            if (n >= 8) {
                size_t i = 8;
                uint64_t prev = read_u64_le(dst);
                for (; i + 8 <= n; i += 8) {
                    uint64_t curr = read_u64_le(dst + i);
                    prev ^= curr;
                    write_u64_le(dst + i, prev);
                }
                for (; i < n; i++) {
                    dst[i] ^= dst[i - 8];
                }
            }
            break;

        case 5: /* OpGCTZModDelta1 */
            for (size_t i = 1; i < n; i++) {
                dst[i] = (uint8_t)(dst[i] + dst[i - 1]);
            }
            break;

        case 6: /* OpGCTZModDelta4 */
            for (size_t i = 4; i < n; i++) {
                dst[i] = (uint8_t)(dst[i] + dst[i - 4]);
            }
            break;

        case 7: /* OpGCTZModSecondOrder */
            for (size_t i = 2; i < n; i++) {
                uint8_t diff = dst[i];
                uint8_t d0 = (uint8_t)(dst[i - 1] - dst[i - 2]);
                uint8_t d1 = (uint8_t)(diff + d0);
                dst[i] = (uint8_t)(dst[i - 1] + d1);
            }
            break;

        default:
            break;
    }
}

int64_t przm_gctr_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src == NULL || dst == NULL) return PRZM_ERR_CORRUPTED_FRAME;
    if (src_len < PRZM_GCTR_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
    if (read_u32_le(src) != PRZM_MAGIC_GCTR) return PRZM_ERR_INVALID_MAGIC;

    uint8_t op_id = src[4];
    uint8_t sub_fmt = src[5];
    size_t orig_len = (size_t)read_u32_le(src + 6);

    if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;
    if (orig_len == 0) return 0;

    const uint8_t* inner_src = src + PRZM_GCTR_HEADER_SIZE;
    size_t inner_len = src_len - PRZM_GCTR_HEADER_SIZE;

    if (sub_fmt == 0x01) { /* GCTRSubVANS */
        int err = vans_decode4x(inner_src, inner_len, dst, orig_len);
        if (err != PRZM_OK) return err;
    } else if (sub_fmt == 0x02) { /* GCTRSubPWS */
        int dec = pws_decode(inner_src, inner_len, dst, dst_cap);
        if (dec < 0) return (int64_t)dec;
        if ((size_t)dec != orig_len) return PRZM_ERR_CORRUPTED_FRAME;
    } else {
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    przm_gctr_inverse_inplace(dst, orig_len, op_id);
    return (int64_t)orig_len;
}

/* ============================================================================
 * 12.1. Cellular Sheaves with Discrete Hodge Laplacians ('CSHD', 0x44485343)
 * ============================================================================ */

int64_t przm_cellular_sheaf_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src == NULL || dst == NULL) return PRZM_ERR_CORRUPTED_FRAME;
    if (src_len < PRZM_CELLULAR_SHEAF_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
    if (read_u32_le(src) != PRZM_MAGIC_CELLULAR_SHEAF) return PRZM_ERR_INVALID_MAGIC;

    size_t orig_len = (size_t)read_u32_le(src + 4);
    if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;
    if (orig_len == 0) return 0;

    uint16_t W = read_u16_le(src + 8);
    uint16_t M = read_u16_le(src + 10);
    uint8_t anchor = src[12];
    uint8_t twist = src[13];
    uint16_t flags = read_u16_le(src + 16);

    const uint8_t* payload = src + PRZM_CELLULAR_SHEAF_HEADER_SIZE;
    size_t payload_len = src_len - PRZM_CELLULAR_SHEAF_HEADER_SIZE;

    /* Handle Cascaded PWS if set */
    if ((flags & PRZM_CSHD_FLAG_CASCADED_PWS) != 0) {
        int dec = pws_decode(payload, payload_len, tls_lits_scratch, PRZM_TLS_SCRATCH_SIZE);
        if (dec < 0) return (int64_t)dec;
        payload = tls_lits_scratch;
        payload_len = (size_t)dec;
    }

    if ((flags & (PRZM_CSHD_FLAG_CURL_FREE | PRZM_CSHD_FLAG_SPARSE_CURL)) == 0) {
        /* Raw passthrough */
        if (payload_len < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, payload, orig_len);
        return (int64_t)orig_len;
    }

    if (W < 2 || M < 2 || (size_t)W * (size_t)M > orig_len) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    size_t pos = 0;
    uint8_t gamma_h = 0;
    uint8_t gamma_v = 0;

    if ((flags & PRZM_CSHD_FLAG_CONST_EH) != 0) {
        if (pos >= payload_len) return PRZM_ERR_CORRUPTED_FRAME;
        gamma_h = payload[pos++];
    }
    if ((flags & PRZM_CSHD_FLAG_CONST_EV) != 0) {
        if (pos >= payload_len) return PRZM_ERR_CORRUPTED_FRAME;
        gamma_v = payload[pos++];
    }

    const uint8_t* Eh = NULL;
    if ((flags & PRZM_CSHD_FLAG_CONST_EH) == 0) {
        if (payload_len - pos < (size_t)(W - 1)) return PRZM_ERR_CORRUPTED_FRAME;
        Eh = payload + pos;
        pos += (W - 1);
    }

    const uint8_t* Ev = NULL;
    if ((flags & PRZM_CSHD_FLAG_CONST_EV) == 0) {
        if (payload_len - pos < (size_t)(M - 1)) return PRZM_ERR_CORRUPTED_FRAME;
        Ev = payload + pos;
        pos += (M - 1);
    }

    uint32_t nnz = 0;
    const uint8_t* curl_data = NULL;
    if ((flags & PRZM_CSHD_FLAG_SPARSE_CURL) != 0) {
        if (payload_len - pos < 4) return PRZM_ERR_CORRUPTED_FRAME;
        nnz = read_u32_le(payload + pos);
        pos += 4;
        if ((payload_len - pos) / 5 < (size_t)nnz) return PRZM_ERR_CORRUPTED_FRAME;
        curl_data = payload + pos;
        pos += (size_t)nnz * 5;
    }

    /* Row 0: Prefix-XOR from anchor */
    dst[0] = anchor;
    if ((flags & PRZM_CSHD_FLAG_CONST_EH) != 0) {
        if (twist <= 1) {
            for (uint16_t j = 0; j < W - 1; j++) {
                dst[j + 1] = dst[j] ^ gamma_h;
            }
        } else {
            for (uint16_t j = 0; j < W - 1; j++) {
                dst[j + 1] = gf256_mul(twist, dst[j]) ^ gamma_h;
            }
        }
    } else {
        if (twist <= 1) {
            for (uint16_t j = 0; j < W - 1; j++) {
                dst[j + 1] = dst[j] ^ Eh[j];
            }
        } else {
            for (uint16_t j = 0; j < W - 1; j++) {
                dst[j + 1] = gf256_mul(twist, dst[j]) ^ Eh[j];
            }
        }
    }

    /* Rows 1 to M - 1: Vector row sweep */
    if (nnz == 0) {
        /* Fast path: Curl-Free */
        for (uint16_t i = 1; i < M; i++) {
            uint8_t v = ((flags & PRZM_CSHD_FLAG_CONST_EV) != 0) ? gamma_v : Ev[i - 1];
            size_t row_curr = (size_t)i * W;
            size_t row_prev = (size_t)(i - 1) * W;

            if (twist <= 1) {
                uint64_t v64 = (uint64_t)v * 0x0101010101010101ULL;
                size_t j = 0;
                for (; j + 8 <= W; j += 8) {
                    uint64_t prev = read_u64_le(dst + row_prev + j);
                    write_u64_le(dst + row_curr + j, prev ^ v64);
                }
                for (; j < W; j++) {
                    dst[row_curr + j] = dst[row_prev + j] ^ v;
                }
            } else {
                uint8_t cur_v = v;
                for (uint16_t j = 0; j < W; j++) {
                    dst[row_curr + j] = gf256_mul(twist, dst[row_prev + j]) ^ cur_v;
                    cur_v = gf256_mul(twist, cur_v);
                }
            }
        }
    } else {
        /* Sparse Curl stream */
        uint32_t curl_idx = 0;
        uint32_t next_k = 0;
        uint8_t next_c = 0;
        if (nnz > 0) {
            next_k = read_u32_le(curl_data);
            next_c = curl_data[4];
        }

        for (uint16_t i = 1; i < M; i++) {
            uint8_t v = ((flags & PRZM_CSHD_FLAG_CONST_EV) != 0) ? gamma_v : Ev[i - 1];
            size_t row_curr = (size_t)i * W;
            size_t row_prev = (size_t)(i - 1) * W;

            dst[row_curr + 0] = (twist <= 1) ? (dst[row_prev + 0] ^ v)
                                             : (gf256_mul(twist, dst[row_prev + 0]) ^ v);

            uint8_t cur_v = v;
            for (uint16_t j = 0; j < W - 1; j++) {
                uint32_t cur_k = (uint32_t)(i - 1) * (uint32_t)(W - 1) + (uint32_t)j;
                if (curl_idx < nnz && cur_k == next_k) {
                    if (twist <= 1) {
                        cur_v ^= next_c;
                    } else {
                        cur_v = gf256_mul(twist, cur_v) ^ next_c;
                    }
                    curl_idx++;
                    if (curl_idx < nnz) {
                        next_k = read_u32_le(curl_data + curl_idx * 5);
                        next_c = curl_data[curl_idx * 5 + 4];
                    }
                } else if (twist > 1) {
                    cur_v = gf256_mul(twist, cur_v);
                }
                dst[row_curr + j + 1] = (twist <= 1) ? (dst[row_prev + j + 1] ^ cur_v)
                                                     : (gf256_mul(twist, dst[row_prev + j + 1]) ^ cur_v);
            }
        }
    }

    /* Copy trailing remainder bytes if any */
    size_t grid_size = (size_t)M * (size_t)W;
    size_t rem = orig_len - grid_size;
    if (rem > 0) {
        if (pos + rem > payload_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst + grid_size, payload + pos, rem);
    }

    return (int64_t)orig_len;
}

/* ============================================================================
 * 12.2. Fast Möbius Transform Engine ('MOBI', 0x49424F4D)
 * ============================================================================ */

#define PRZM_FMT_DEFAULT_BLOCK_LOG2 6

PRZM_INLINE void fmt_byte_butterfly_inplace(uint8_t* block, int k) {
    size_t n = (size_t)1 << k;
    for (int s = 0; s < k; s++) {
        size_t step = (size_t)1 << s;
        size_t stride = step << 1;
        if (step >= 8) {
            for (size_t i = 0; i < n; i += stride) {
                uint8_t* dst_slice = block + i + step;
                const uint8_t* src_slice = block + i;
                for (size_t j = 0; j < step; j += 8) {
                    uint64_t d_val = read_u64_le(dst_slice + j);
                    uint64_t s_val = read_u64_le(src_slice + j);
                    write_u64_le(dst_slice + j, d_val ^ s_val);
                }
            }
        } else {
            for (size_t i = 0; i < n; i += stride) {
                uint8_t* dst_slice = block + i + step;
                const uint8_t* src_slice = block + i;
                for (size_t j = 0; j < step; j++) {
                    dst_slice[j] ^= src_slice[j];
                }
            }
        }
    }
}

PRZM_INLINE void fmt_bit_butterfly_swar_inplace(uint8_t* data, size_t n) {
    size_t limit8 = (n / 8) * 8;
    for (size_t i = 0; i < limit8; i += 8) {
        uint64_t w = read_u64_le(data + i);
        w ^= (w & 0x5555555555555555ULL) << 1;
        w ^= (w & 0x3333333333333333ULL) << 2;
        w ^= (w & 0x0F0F0F0F0F0F0F0FULL) << 4;
        write_u64_le(data + i, w);
    }
    for (size_t i = limit8; i < n; i++) {
        uint8_t b = data[i];
        b ^= (uint8_t)((b & 0x55) << 1);
        b ^= (uint8_t)((b & 0x33) << 2);
        b ^= (uint8_t)((b & 0x0F) << 4);
        data[i] = b;
    }
}

int przm_fmt_transform_inplace(uint8_t* dst, size_t len, uint8_t block_log2, uint8_t bit_level) {
    if (dst == NULL || len == 0) return PRZM_OK;
    if (block_log2 < 4 || block_log2 > 16) {
        block_log2 = PRZM_FMT_DEFAULT_BLOCK_LOG2;
    }
    size_t block_size = (size_t)1 << block_log2;
    if (block_size == 0 || len < block_size) {
        return PRZM_OK;
    }

    size_t num_blocks = len / block_size;
    size_t total_transformed = num_blocks * block_size;

    if (bit_level) {
        fmt_bit_butterfly_swar_inplace(dst, total_transformed);
    }

    for (size_t b = 0; b < num_blocks; b++) {
        fmt_byte_butterfly_inplace(dst + b * block_size, (int)block_log2);
    }

    return PRZM_OK;
}

int przm_mobius_transform_inplace(uint8_t* dst, size_t len, uint8_t block_log2, uint8_t bit_level) {
    return przm_fmt_transform_inplace(dst, len, block_log2, bit_level);
}

int przm_mobius_untransform_inplace(uint8_t* dst, size_t len, uint8_t block_log2, uint8_t bit_level) {
    return przm_fmt_transform_inplace(dst, len, block_log2, bit_level);
}

int64_t przm_mobius_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len == 0) return 0;
    if (src == NULL || dst == NULL || src_len < PRZM_MOBIUS_HEADER_SIZE) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    uint32_t magic = read_u32_le(src);
    if (magic != PRZM_MAGIC_MOBIUS) {
        return PRZM_ERR_INVALID_MAGIC;
    }

    size_t orig_len = (size_t)read_u32_le(src + 4);
    if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (orig_len == 0) return 0;
    if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    uint8_t block_log2 = src[8];
    if (block_log2 < 4 || block_log2 > 16) {
        block_log2 = PRZM_FMT_DEFAULT_BLOCK_LOG2;
    }
    uint8_t mode_byte = src[9];
    uint8_t mode = mode_byte & 0x0F;
    uint8_t bit_level = (mode_byte & PRZM_FMT_FLAG_BIT_LEVEL) ? 1 : 0;

    const uint8_t* payload = src + PRZM_MOBIUS_HEADER_SIZE;
    size_t payload_len = src_len - PRZM_MOBIUS_HEADER_SIZE;

    switch (mode) {
    case PRZM_FMT_MODE_RAW:
        if (payload_len < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, payload, orig_len);
        return (int64_t)orig_len;

    case PRZM_FMT_MODE_TRANSFORMED:
        if (payload_len < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, payload, orig_len);
        przm_fmt_transform_inplace(dst, orig_len, block_log2, bit_level);
        return (int64_t)orig_len;

    case PRZM_FMT_MODE_COMPRESSED: {
        int64_t dec;
        if (payload_len >= 3 && payload[0] == PRZM_MAGIC_PWS_0 &&
            payload[1] == PRZM_MAGIC_PWS_1 && payload[2] == PRZM_VERSION_1) {
            dec = pws_decode(payload, payload_len, dst, dst_cap);
        } else {
            dec = przm_decompress(payload, payload_len, dst, dst_cap);
        }
        if (dec < 0) return dec;
        if ((size_t)dec < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        przm_fmt_transform_inplace(dst, (size_t)dec, block_log2, bit_level);
        return dec;
    }

    default:
        return PRZM_ERR_UNSUPPORTED_MODE;
    }
}

/* ============================================================================
 * 12.3. Margolus Reversible Cellular Automata Engine ('MARG', 0x4752414D)
 * ============================================================================ */

PRZM_INLINE void apply_margolus_block_rule(uint8_t* a, uint8_t* b, uint8_t* c, uint8_t* d,
                                          uint8_t rule_id, int inverse) {
    uint8_t va = *a, vb = *b, vc = *c, vd = *d;
    switch (rule_id) {
    case PRZM_MARGOLUS_RULE_BBM: {
        uint8_t diag = (uint8_t)(va & vd & (~vb) & (~vc));
        uint8_t antidiag = (uint8_t)(vb & vc & (~va) & (~vd));
        uint8_t mask = (uint8_t)(diag | antidiag);
        *a = va ^ mask;
        *b = vb ^ mask;
        *c = vc ^ mask;
        *d = vd ^ mask;
        break;
    }
    case PRZM_MARGOLUS_RULE_TRANSPOSE:
        *b = vc;
        *c = vb;
        break;
    case PRZM_MARGOLUS_RULE_ROT180:
        *a = vd;
        *b = vc;
        *c = vb;
        *d = va;
        break;
    case PRZM_MARGOLUS_RULE_ROT90:
        if (!inverse) {
            *a = vc;
            *b = va;
            *c = vd;
            *d = vb;
        } else {
            *a = vb;
            *b = vd;
            *c = va;
            *d = vc;
        }
        break;
    case PRZM_MARGOLUS_RULE_XOR:
        *b = (uint8_t)(vb ^ va);
        *c = (uint8_t)(vc ^ va);
        *d = (uint8_t)(vd ^ va);
        break;
    case PRZM_MARGOLUS_RULE_FLIP_H:
        *a = vb;
        *b = va;
        *c = vd;
        *d = vc;
        break;
    case PRZM_MARGOLUS_RULE_FLIP_V:
        *a = vc;
        *b = vd;
        *c = va;
        *d = vb;
        break;
    default:
        break;
    }
}

int przm_margolus_step_inplace(uint8_t* dst, size_t H, size_t W, int phase, uint8_t rule_id, int inverse) {
    if (H < 2 || W < 2 || dst == NULL) return PRZM_OK;
    size_t w_mask = W - 1;
    int is_pow2 = (W & w_mask) == 0;
    size_t half_H = H / 2;
    size_t half_W = W / 2;

    if (phase == 0) {
        for (size_t i = 0; i < half_H; i++) {
            size_t r0 = 2 * i * W;
            size_t r1 = r0 + W;
            for (size_t j = 0; j < half_W; j++) {
                size_t c0 = 2 * j;
                size_t c1 = c0 + 1;

                size_t i00 = r0 + c0;
                size_t i01 = r0 + c1;
                size_t i10 = r1 + c0;
                size_t i11 = r1 + c1;

                apply_margolus_block_rule(&dst[i00], &dst[i01], &dst[i10], &dst[i11], rule_id, inverse);
            }
        }
    } else {
        for (size_t i = 0; i < half_H; i++) {
            size_t r0_idx = 2 * i + 1;
            size_t r1_idx = r0_idx + 1;
            if (r1_idx >= H) r1_idx = 0;

            size_t r0 = r0_idx * W;
            size_t r1 = r1_idx * W;

            for (size_t j = 0; j < half_W; j++) {
                size_t c0 = 2 * j + 1;
                size_t c1 = c0 + 1;
                if (is_pow2) {
                    c1 &= w_mask;
                } else if (c1 >= W) {
                    c1 = 0;
                }

                size_t i00 = r0 + c0;
                size_t i01 = r0 + c1;
                size_t i10 = r1 + c0;
                size_t i11 = r1 + c1;

                apply_margolus_block_rule(&dst[i00], &dst[i01], &dst[i10], &dst[i11], rule_id, inverse);
            }
        }
    }
    return PRZM_OK;
}

int przm_margolus_reverse_inplace(uint8_t* dst, size_t len, uint16_t width, uint8_t steps, uint8_t rule_id) {
    if (dst == NULL || width < 2 || len < 2 * (size_t)width || steps == 0) {
        return PRZM_OK;
    }
    size_t H = 2 * (len / (2 * (size_t)width));
    for (int t = (int)steps - 1; t >= 0; t--) {
        int phase = t & 1;
        przm_margolus_step_inplace(dst, H, (size_t)width, phase, rule_id, 1);
    }
    return PRZM_OK;
}

int przm_margolus_inverse_inplace(uint8_t* dst, size_t len, uint16_t width, uint8_t steps, uint8_t rule_id) {
    return przm_margolus_reverse_inplace(dst, len, width, steps, rule_id);
}

int64_t przm_margolus_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len == 0) return 0;
    if (src == NULL || dst == NULL || src_len < PRZM_MARGOLUS_HEADER_SIZE) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    uint32_t magic = read_u32_le(src);
    if (magic != PRZM_MAGIC_MARGOLUS) {
        return PRZM_ERR_INVALID_MAGIC;
    }

    size_t orig_len = (size_t)read_u32_le(src + 4);
    if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (orig_len == 0) return 0;
    if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    uint16_t width = read_u16_le(src + 8);
    uint8_t steps = src[10];
    uint8_t rule_id = src[11];
    uint8_t mode = src[12];

    const uint8_t* payload = src + PRZM_MARGOLUS_HEADER_SIZE;
    size_t payload_len = src_len - PRZM_MARGOLUS_HEADER_SIZE;

    if (mode == 0) {
        /* MargolusModeRaw */
        if (payload_len < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, payload, orig_len);
        return (int64_t)orig_len;
    } else if (mode == 1) {
        /* MargolusModeTransform */
        if (payload_len < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, payload, orig_len);
        przm_margolus_reverse_inplace(dst, orig_len, width, steps, rule_id);
        return (int64_t)orig_len;
    } else if (mode == 3) {
        /* MargolusModeCompressed */
        int64_t dec;
        if (payload_len >= 3 && payload[0] == PRZM_MAGIC_PWS_0 &&
            payload[1] == PRZM_MAGIC_PWS_1 && payload[2] == PRZM_VERSION_1) {
            dec = pws_decode(payload, payload_len, dst, dst_cap);
        } else {
            dec = przm_decompress(payload, payload_len, dst, dst_cap);
        }
        if (dec < 0) return dec;
        if ((size_t)dec < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        przm_margolus_reverse_inplace(dst, (size_t)dec, width, steps, rule_id);
        return dec;
    }

    return PRZM_ERR_UNSUPPORTED_MODE;
}

/* ============================================================================
 * 13. Public API Implementation: przm_get_uncompressed_size
 * ============================================================================ */


int64_t przm_get_uncompressed_size(const uint8_t* src, size_t src_len) {
    if (src == NULL || src_len < 4) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t magic = read_u32_le(src);

    /* Multi-Chunk Stream ('PRZS', 0x535A5250) */
    if (magic == PRZM_MAGIC_STREAM) {
        if (src_len < PRZM_STREAM_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u64_le(src + 5);
    }

    /* 32-bit Specialized signatures */
    if (magic == PRZM_MAGIC_ARCHIVAL_PNG) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }
    if (magic == PRZM_MAGIC_MOBIUS) {
        if (src_len < PRZM_MOBIUS_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }
    if (magic == PRZM_MAGIC_MARGOLUS) {
        if (src_len < PRZM_MARGOLUS_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }
    if (magic == PRZM_MAGIC_SYMPLECTIC || magic == PRZM_MAGIC_ARM64 ||
        magic == PRZM_MAGIC_SHEAF || magic == PRZM_MAGIC_MAHLER) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 8);
    }
    if (magic == PRZM_MAGIC_ISOQUANT) {
        if (src_len < PRZM_ISOQUANT_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 8);
    }
    if (magic == PRZM_MAGIC_GCTR) {
        if (src_len < PRZM_GCTR_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 6);
    }
    if (magic == PRZM_MAGIC_HEISENBERG) {
        if (src_len < PRZM_HEISENBERG_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }
    if (magic == PRZM_MAGIC_CELLULAR_SHEAF) {
        if (src_len < PRZM_CELLULAR_SHEAF_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }

    /* 3-byte Versioned ISA signatures */
    if (src[0] == PRZM_MAGIC_PWS_0 && src[1] == PRZM_MAGIC_PWS_1 && src[2] == PRZM_VERSION_1) {
        if (src_len < 8) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }
    if (src[0] == PRZM_MAGIC_VM_0 && src[1] == PRZM_MAGIC_VM_1 && src[2] == PRZM_VERSION_1) {
        if (src_len < 8) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }
    if (src[0] == PRZM_MAGIC_GGTC_0 && src[1] == PRZM_MAGIC_GGTC_1 && src[2] == PRZM_VERSION_1) {
        if (src_len < 8) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 4);
    }

    /* Standard Fused Frame ('PRZM') */
    if (magic == PRZM_MAGIC_STANDARD) {
        if (src_len < PRZM_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t u_size = read_u24_le(src + 5);
        return (int64_t)u_size;
    }

    return PRZM_ERR_INVALID_MAGIC;
}

/* ============================================================================
 * 13.5. Canonical Huffman 4-Stream Interleaved Decoder Engine
 * ============================================================================ */

#define PRZM_MAX_HUFF_BITS            11
#define PRZM_HUFF_TABLE_SIZE          (1 << PRZM_MAX_HUFF_BITS) /* 2048 entries * 2 bytes = 4 KiB */
#define PRZM_HEADER_SIZE_HUFFMAN      (1 + 4 + 128 + 16)        /* 149 bytes */

typedef uint16_t przm_huff_entry_t;

PRZM_INLINE przm_huff_entry_t make_huff_entry(uint8_t sym, uint8_t code_len) {
    return (przm_huff_entry_t)((uint16_t)sym | ((uint16_t)code_len << 8));
}

PRZM_INLINE uint8_t huff_entry_symbol(przm_huff_entry_t e) {
    return (uint8_t)(e & 0xFF);
}

PRZM_INLINE uint8_t huff_entry_codelen(przm_huff_entry_t e) {
    return (uint8_t)(e >> 8);
}

typedef struct {
    przm_huff_entry_t table[PRZM_HUFF_TABLE_SIZE];
} przm_canonical_huff_table_t;

static PRZM_TLS przm_canonical_huff_table_t tls_huff_table;

static bool huff_build_from_lengths(przm_canonical_huff_table_t* h, const uint8_t lengths[256]) {
    memset(h->table, 0, sizeof(h->table));

    int bl_count[PRZM_MAX_HUFF_BITS + 1] = {0};
    for (int i = 0; i < 256; i++) {
        uint8_t l = lengths[i];
        if (l > PRZM_MAX_HUFF_BITS) return false;
        bl_count[l]++;
    }
    bl_count[0] = 0;

    uint16_t next_code[PRZM_MAX_HUFF_BITS + 1] = {0};
    uint16_t code = 0;
    for (int b = 1; b <= PRZM_MAX_HUFF_BITS; b++) {
        code = (uint16_t)((code + (uint16_t)bl_count[b - 1]) << 1);
        next_code[b] = code;
    }

    for (int sym = 0; sym < 256; sym++) {
        uint8_t l = lengths[sym];
        if (l == 0) continue;
        uint16_t c = next_code[l];
        next_code[l]++;

        przm_huff_entry_t entry = make_huff_entry((uint8_t)sym, l);
        int shift = PRZM_MAX_HUFF_BITS - l;
        int num_entries = 1 << shift;
        int base_idx = (int)(c << shift);

        if (base_idx + num_entries > PRZM_HUFF_TABLE_SIZE) {
            return false;
        }

        for (int i = 0; i < num_entries; i++) {
            h->table[base_idx + i] = entry;
        }
    }
    return true;
}

typedef struct {
    const uint8_t* src;
    size_t         src_len;
    size_t         ptr;
    uint64_t       bit_container;
    int            bits_remaining;
    bool           underflow;
} przm_huff_bit_reader_t;

PRZM_INLINE void huff_reader_refill(przm_huff_bit_reader_t* r) {
    while (r->bits_remaining <= 32 && r->ptr < r->src_len) {
        r->bit_container = (r->bit_container << 8) | (uint64_t)r->src[r->ptr++];
        r->bits_remaining += 8;
    }
}

PRZM_INLINE void huff_reader_init(przm_huff_bit_reader_t* r, const uint8_t* src, size_t src_len) {
    r->src = src;
    r->src_len = src_len;
    r->ptr = 0;
    r->bit_container = 0;
    r->bits_remaining = 0;
    r->underflow = false;
    huff_reader_refill(r);
}

PRZM_INLINE uint16_t huff_reader_peek(przm_huff_bit_reader_t* r, int nb_bits) {
    if (r->bits_remaining < nb_bits) {
        huff_reader_refill(r);
    }
    if (r->bits_remaining <= 0) {
        r->underflow = true;
    }
    if (r->bits_remaining < nb_bits) {
        int shift = nb_bits - r->bits_remaining;
        if (shift >= 64 || shift < 0) return 0;
        return (uint16_t)((r->bit_container << shift) & ((1U << nb_bits) - 1U));
    }
    int shift = r->bits_remaining - nb_bits;
    return (uint16_t)((r->bit_container >> shift) & ((1U << nb_bits) - 1U));
}

PRZM_INLINE void huff_reader_consume(przm_huff_bit_reader_t* r, int nb_bits) {
    r->bits_remaining -= nb_bits;
    if (r->bits_remaining < 0) {
        r->underflow = true;
    }
}

static int64_t huffman4x_decode(const uint8_t* src, size_t src_len,
                                uint8_t* dst, size_t dst_cap,
                                przm_canonical_huff_table_t* dec_table) {
    if (src == NULL || src_len < 5) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }
    uint32_t orig_len = read_u32_le(src + 1);
    if (orig_len == 0) {
        return 0;
    }
    if (dst_cap < (size_t)orig_len) {
        return PRZM_ERR_BUFFER_TOO_SMALL;
    }
    if (src_len < PRZM_HEADER_SIZE_HUFFMAN) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    /* 1. Unpack lengths */
    uint8_t lengths[256];
    for (size_t i = 0; i < 128; i++) {
        uint8_t b = src[5 + i];
        lengths[i * 2]     = b & 0x0F;
        lengths[i * 2 + 1] = (b >> 4) & 0x0F;
    }

    /* 2. Build 4 KiB direct decode table */
    if (!huff_build_from_lengths(dec_table, lengths)) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    /* 3. Resolve 4 stream offsets */
    size_t sz0 = (size_t)read_u32_le(src + 133);
    size_t sz1 = (size_t)read_u32_le(src + 137);
    size_t sz2 = (size_t)read_u32_le(src + 141);
    size_t sz3 = (size_t)read_u32_le(src + 145);

    size_t total_payload = sz0 + sz1 + sz2 + sz3;
    if (src_len < PRZM_HEADER_SIZE_HUFFMAN + total_payload) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    size_t p0 = PRZM_HEADER_SIZE_HUFFMAN;
    size_t p1 = p0 + sz0;
    size_t p2 = p1 + sz1;
    size_t p3 = p2 + sz2;
    size_t p_end = p3 + sz3;

    if (p_end > src_len) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    size_t chunk_size = (orig_len + 3) / 4;
    size_t c0_count = chunk_size < (size_t)orig_len ? chunk_size : (size_t)orig_len;
    size_t rem1 = (size_t)orig_len > chunk_size ? (size_t)orig_len - chunk_size : 0;
    size_t c1_count = chunk_size < rem1 ? chunk_size : rem1;
    size_t rem2 = (size_t)orig_len > 2 * chunk_size ? (size_t)orig_len - 2 * chunk_size : 0;
    size_t c2_count = chunk_size < rem2 ? chunk_size : rem2;
    size_t c3_count = (size_t)orig_len > 3 * chunk_size ? (size_t)orig_len - 3 * chunk_size : 0;

    uint8_t* dst0 = dst;
    uint8_t* dst1 = dst + c0_count;
    uint8_t* dst2 = dst + c0_count + c1_count;
    uint8_t* dst3 = dst + c0_count + c1_count + c2_count;

    przm_huff_bit_reader_t r0, r1, r2, r3;
    huff_reader_init(&r0, src + p0, sz0);
    huff_reader_init(&r1, src + p1, sz1);
    huff_reader_init(&r2, src + p2, sz2);
    huff_reader_init(&r3, src + p3, sz3);

    /* 4. Superscalar 4-way unrolled decode loop */
    size_t i0 = 0, i1 = 0, i2 = 0, i3 = 0;
    while (i0 < c0_count && i1 < c1_count && i2 < c2_count && i3 < c3_count) {
        przm_huff_entry_t e0 = dec_table->table[huff_reader_peek(&r0, PRZM_MAX_HUFF_BITS)];
        uint8_t cl0 = huff_entry_codelen(e0);
        if (cl0 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst0[i0++] = huff_entry_symbol(e0);
        huff_reader_consume(&r0, (int)cl0);

        przm_huff_entry_t e1 = dec_table->table[huff_reader_peek(&r1, PRZM_MAX_HUFF_BITS)];
        uint8_t cl1 = huff_entry_codelen(e1);
        if (cl1 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst1[i1++] = huff_entry_symbol(e1);
        huff_reader_consume(&r1, (int)cl1);

        przm_huff_entry_t e2 = dec_table->table[huff_reader_peek(&r2, PRZM_MAX_HUFF_BITS)];
        uint8_t cl2 = huff_entry_codelen(e2);
        if (cl2 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst2[i2++] = huff_entry_symbol(e2);
        huff_reader_consume(&r2, (int)cl2);

        przm_huff_entry_t e3 = dec_table->table[huff_reader_peek(&r3, PRZM_MAX_HUFF_BITS)];
        uint8_t cl3 = huff_entry_codelen(e3);
        if (cl3 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst3[i3++] = huff_entry_symbol(e3);
        huff_reader_consume(&r3, (int)cl3);
    }

    while (i0 < c0_count) {
        przm_huff_entry_t e0 = dec_table->table[huff_reader_peek(&r0, PRZM_MAX_HUFF_BITS)];
        uint8_t cl0 = huff_entry_codelen(e0);
        if (cl0 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst0[i0++] = huff_entry_symbol(e0);
        huff_reader_consume(&r0, (int)cl0);
    }
    while (i1 < c1_count) {
        przm_huff_entry_t e1 = dec_table->table[huff_reader_peek(&r1, PRZM_MAX_HUFF_BITS)];
        uint8_t cl1 = huff_entry_codelen(e1);
        if (cl1 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst1[i1++] = huff_entry_symbol(e1);
        huff_reader_consume(&r1, (int)cl1);
    }
    while (i2 < c2_count) {
        przm_huff_entry_t e2 = dec_table->table[huff_reader_peek(&r2, PRZM_MAX_HUFF_BITS)];
        uint8_t cl2 = huff_entry_codelen(e2);
        if (cl2 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst2[i2++] = huff_entry_symbol(e2);
        huff_reader_consume(&r2, (int)cl2);
    }
    while (i3 < c3_count) {
        przm_huff_entry_t e3 = dec_table->table[huff_reader_peek(&r3, PRZM_MAX_HUFF_BITS)];
        uint8_t cl3 = huff_entry_codelen(e3);
        if (cl3 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst3[i3++] = huff_entry_symbol(e3);
        huff_reader_consume(&r3, (int)cl3);
    }

    if (r0.underflow || r1.underflow || r2.underflow || r3.underflow) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    return (int64_t)orig_len;
}

static int64_t huffman_compact_decode(const uint8_t* src, size_t src_len,
                                      uint8_t* dst, size_t dst_cap,
                                      przm_canonical_huff_table_t* dec_table) {
    if (src == NULL || src_len < 5) return PRZM_ERR_CORRUPTED_FRAME;
    uint32_t orig_len = read_u32_le(src + 1);
    if (orig_len == 0) return 0;
    if (dst_cap < (size_t)orig_len) return PRZM_ERR_BUFFER_TOO_SMALL;
    if (src_len < 37) return PRZM_ERR_CORRUPTED_FRAME;

    uint8_t lengths[256];
    memset(lengths, 0, sizeof(lengths));
    size_t pos = 37;
    size_t nib = 0;
    for (size_t s = 0; s < 256; s++) {
        if ((src[5 + s / 8] & (1U << (s % 8))) != 0) {
            if (pos + nib / 2 >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t b = src[pos + nib / 2];
            uint8_t l = ((nib & 1) == 0) ? (b & 0x0F) : ((b >> 4) & 0x0F);
            lengths[s] = l;
            nib++;
        }
    }
    size_t hdr_size = 37 + (nib + 1) / 2;
    if (hdr_size > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    if (!huff_build_from_lengths(dec_table, lengths)) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    przm_huff_bit_reader_t r;
    huff_reader_init(&r, src + hdr_size, src_len - hdr_size);
    for (size_t i = 0; i < (size_t)orig_len; i++) {
        przm_huff_entry_t e = dec_table->table[huff_reader_peek(&r, PRZM_MAX_HUFF_BITS)];
        uint8_t cl = huff_entry_codelen(e);
        if (cl == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst[i] = huff_entry_symbol(e);
        huff_reader_consume(&r, (int)cl);
    }
    if (r.underflow) return PRZM_ERR_CORRUPTED_FRAME;
    return (int64_t)orig_len;
}

static int64_t huffman_compact4x_decode(const uint8_t* src, size_t src_len,
                                        uint8_t* dst, size_t dst_cap,
                                        przm_canonical_huff_table_t* dec_table) {
    if (src == NULL || src_len < 5) return PRZM_ERR_CORRUPTED_FRAME;
    uint32_t orig_len = read_u32_le(src + 1);
    if (orig_len == 0) return 0;
    if (dst_cap < (size_t)orig_len) return PRZM_ERR_BUFFER_TOO_SMALL;
    if (src_len < 37) return PRZM_ERR_CORRUPTED_FRAME;

    uint8_t lengths[256];
    memset(lengths, 0, sizeof(lengths));
    size_t pos = 37;
    size_t nib = 0;
    for (size_t s = 0; s < 256; s++) {
        if ((src[5 + s / 8] & (1U << (s % 8))) != 0) {
            if (pos + nib / 2 >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t b = src[pos + nib / 2];
            uint8_t l = ((nib & 1) == 0) ? (b & 0x0F) : ((b >> 4) & 0x0F);
            lengths[s] = l;
            nib++;
        }
    }
    size_t hdr_size = 37 + (nib + 1) / 2;
    if (hdr_size + 16 > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    if (!huff_build_from_lengths(dec_table, lengths)) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    size_t sz0 = (size_t)read_u32_le(src + hdr_size);
    size_t sz1 = (size_t)read_u32_le(src + hdr_size + 4);
    size_t sz2 = (size_t)read_u32_le(src + hdr_size + 8);
    size_t sz3 = (size_t)read_u32_le(src + hdr_size + 12);

    size_t total_payload = sz0 + sz1 + sz2 + sz3;
    if (src_len < hdr_size + 16 + total_payload) return PRZM_ERR_CORRUPTED_FRAME;

    size_t p0 = hdr_size + 16;
    size_t p1 = p0 + sz0;
    size_t p2 = p1 + sz1;
    size_t p3 = p2 + sz2;
    size_t p_end = p3 + sz3;
    if (p_end > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    size_t chunk_size = (orig_len + 3) / 4;
    size_t c0_count = chunk_size < (size_t)orig_len ? chunk_size : (size_t)orig_len;
    size_t rem1 = (size_t)orig_len > chunk_size ? (size_t)orig_len - chunk_size : 0;
    size_t c1_count = chunk_size < rem1 ? chunk_size : rem1;
    size_t rem2 = (size_t)orig_len > 2 * chunk_size ? (size_t)orig_len - 2 * chunk_size : 0;
    size_t c2_count = chunk_size < rem2 ? chunk_size : rem2;
    size_t c3_count = (size_t)orig_len > 3 * chunk_size ? (size_t)orig_len - 3 * chunk_size : 0;

    uint8_t* dst0 = dst;
    uint8_t* dst1 = dst + c0_count;
    uint8_t* dst2 = dst + c0_count + c1_count;
    uint8_t* dst3 = dst + c0_count + c1_count + c2_count;

    przm_huff_bit_reader_t r0, r1, r2, r3;
    huff_reader_init(&r0, src + p0, sz0);
    huff_reader_init(&r1, src + p1, sz1);
    huff_reader_init(&r2, src + p2, sz2);
    huff_reader_init(&r3, src + p3, sz3);

    size_t i0 = 0, i1 = 0, i2 = 0, i3 = 0;
    while (i0 < c0_count && i1 < c1_count && i2 < c2_count && i3 < c3_count) {
        przm_huff_entry_t e0 = dec_table->table[huff_reader_peek(&r0, PRZM_MAX_HUFF_BITS)];
        uint8_t cl0 = huff_entry_codelen(e0);
        if (cl0 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst0[i0++] = huff_entry_symbol(e0);
        huff_reader_consume(&r0, (int)cl0);

        przm_huff_entry_t e1 = dec_table->table[huff_reader_peek(&r1, PRZM_MAX_HUFF_BITS)];
        uint8_t cl1 = huff_entry_codelen(e1);
        if (cl1 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst1[i1++] = huff_entry_symbol(e1);
        huff_reader_consume(&r1, (int)cl1);

        przm_huff_entry_t e2 = dec_table->table[huff_reader_peek(&r2, PRZM_MAX_HUFF_BITS)];
        uint8_t cl2 = huff_entry_codelen(e2);
        if (cl2 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst2[i2++] = huff_entry_symbol(e2);
        huff_reader_consume(&r2, (int)cl2);

        przm_huff_entry_t e3 = dec_table->table[huff_reader_peek(&r3, PRZM_MAX_HUFF_BITS)];
        uint8_t cl3 = huff_entry_codelen(e3);
        if (cl3 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst3[i3++] = huff_entry_symbol(e3);
        huff_reader_consume(&r3, (int)cl3);
    }

    while (i0 < c0_count) {
        przm_huff_entry_t e0 = dec_table->table[huff_reader_peek(&r0, PRZM_MAX_HUFF_BITS)];
        uint8_t cl0 = huff_entry_codelen(e0);
        if (cl0 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst0[i0++] = huff_entry_symbol(e0);
        huff_reader_consume(&r0, (int)cl0);
    }
    while (i1 < c1_count) {
        przm_huff_entry_t e1 = dec_table->table[huff_reader_peek(&r1, PRZM_MAX_HUFF_BITS)];
        uint8_t cl1 = huff_entry_codelen(e1);
        if (cl1 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst1[i1++] = huff_entry_symbol(e1);
        huff_reader_consume(&r1, (int)cl1);
    }
    while (i2 < c2_count) {
        przm_huff_entry_t e2 = dec_table->table[huff_reader_peek(&r2, PRZM_MAX_HUFF_BITS)];
        uint8_t cl2 = huff_entry_codelen(e2);
        if (cl2 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst2[i2++] = huff_entry_symbol(e2);
        huff_reader_consume(&r2, (int)cl2);
    }
    while (i3 < c3_count) {
        przm_huff_entry_t e3 = dec_table->table[huff_reader_peek(&r3, PRZM_MAX_HUFF_BITS)];
        uint8_t cl3 = huff_entry_codelen(e3);
        if (cl3 == 0) return PRZM_ERR_CORRUPTED_FRAME;
        dst3[i3++] = huff_entry_symbol(e3);
        huff_reader_consume(&r3, (int)cl3);
    }

    if (r0.underflow || r1.underflow || r2.underflow || r3.underflow) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }
    return (int64_t)orig_len;
}

/* ----------------------------------------------------------------------------
 * Log2 Distance Prefix Codebook Decoder (Entropy-Coded Band + Raw ExtraBits)
 * ---------------------------------------------------------------------------- */

static const uint32_t przm_dist_base[32] = {
    1, 2, 3, 4,
    5, 7, 9, 13,
    17, 25, 33, 49,
    65, 97, 129, 193,
    257, 385, 513, 769,
    1025, 1537, 2049, 3073,
    4097, 6145, 8193, 12289,
    16385, 24577, 32769, 65537
};

static const uint8_t przm_dist_extra_bits[32] = {
    0, 0, 0, 0,
    1, 1, 2, 2,
    3, 3, 4, 4,
    5, 5, 6, 6,
    7, 7, 8, 8,
    9, 9, 10, 10,
    11, 11, 12, 12,
    13, 13, 15, 24
};

static inline uint32_t przm_read_extra_bits(przm_huff_bit_reader_t* r, uint8_t nb) {
    if (nb == 0) return 0;
    if (nb <= 16) {
        uint32_t val = (uint32_t)huff_reader_peek(r, (int)nb);
        huff_reader_consume(r, (int)nb);
        return val;
    } else {
        int rem = (int)nb - 16;
        uint32_t high = (uint32_t)huff_reader_peek(r, 16);
        huff_reader_consume(r, 16);
        uint32_t low = (uint32_t)huff_reader_peek(r, rem);
        huff_reader_consume(r, rem);
        return (high << rem) | low;
    }
}

static int64_t distance_prefix_decode(const uint8_t* src, size_t src_len,
                                      uint8_t* dst, size_t dst_cap,
                                      przm_canonical_huff_table_t* dec_table,
                                      uint8_t* scratch_codes, size_t scratch_cap) {
    if (src == NULL || src_len < 13) return PRZM_ERR_CORRUPTED_FRAME;
    if (src[0] != PRZM_ENTROPY_MODE_DISTANCE_PREFIX) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t orig_varint_len = read_u32_le(src + 1);
    uint32_t match_count = read_u32_le(src + 5);
    uint32_t code_stream_len = read_u32_le(src + 9);

    if (orig_varint_len == 0 || match_count == 0) return 0;
    if (dst_cap < (size_t)orig_varint_len) return PRZM_ERR_BUFFER_TOO_SMALL;
    if (scratch_cap < (size_t)match_count) return PRZM_ERR_BUFFER_TOO_SMALL;
    if (13 + (size_t)code_stream_len > src_len) return PRZM_ERR_CORRUPTED_FRAME;

    /* 1. Decode distance codes via compact Huffman */
    int64_t dec_codes = huffman_compact_decode(src + 13, code_stream_len,
                                               scratch_codes, scratch_cap, dec_table);
    if (dec_codes < 0 || (uint32_t)dec_codes != match_count) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }

    /* 2. Read extra bits and reconstruct LEB128 varints */
    przm_huff_bit_reader_t r;
    size_t extra_bits_offset = 13 + (size_t)code_stream_len;
    huff_reader_init(&r, src + extra_bits_offset, src_len - extra_bits_offset);

    size_t dst_pos = 0;
    for (size_t i = 0; i < (size_t)match_count; i++) {
        uint8_t code = scratch_codes[i];
        if (code >= 32) return PRZM_ERR_CORRUPTED_FRAME;

        uint8_t nb = przm_dist_extra_bits[code];
        uint32_t extra = 0;
        if (nb > 0) {
            extra = przm_read_extra_bits(&r, nb);
        }
        uint32_t d = przm_dist_base[code] + extra;

        /* Write LEB128 varint into dst */
        if (d < 0x80) {
            if (dst_pos >= (size_t)orig_varint_len) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = (uint8_t)d;
        } else if (d < 0x4000) {
            if (dst_pos + 2 > (size_t)orig_varint_len) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = (uint8_t)(d | 0x80);
            dst[dst_pos++] = (uint8_t)(d >> 7);
        } else if (d < 0x200000) {
            if (dst_pos + 3 > (size_t)orig_varint_len) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = (uint8_t)(d | 0x80);
            dst[dst_pos++] = (uint8_t)((d >> 7) | 0x80);
            dst[dst_pos++] = (uint8_t)(d >> 14);
        } else if (d < 0x10000000) {
            if (dst_pos + 4 > (size_t)orig_varint_len) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = (uint8_t)(d | 0x80);
            dst[dst_pos++] = (uint8_t)((d >> 7) | 0x80);
            dst[dst_pos++] = (uint8_t)((d >> 14) | 0x80);
            dst[dst_pos++] = (uint8_t)(d >> 21);
        } else {
            if (dst_pos + 5 > (size_t)orig_varint_len) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = (uint8_t)(d | 0x80);
            dst[dst_pos++] = (uint8_t)((d >> 7) | 0x80);
            dst[dst_pos++] = (uint8_t)((d >> 14) | 0x80);
            dst[dst_pos++] = (uint8_t)((d >> 21) | 0x80);
            dst[dst_pos++] = (uint8_t)(d >> 28);
        }
    }

    if (r.underflow || dst_pos != (size_t)orig_varint_len) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }
    return (int64_t)dst_pos;
}

static int64_t przm_unpack_stream(const uint8_t* src, size_t src_len,
                                  uint8_t* dst, size_t dst_cap,
                                  przm_canonical_huff_table_t* dec_table) {
    if (src == NULL || src_len < 5) return PRZM_ERR_CORRUPTED_FRAME;
    uint8_t mode = src[0];
    uint32_t orig_len = read_u32_le(src + 1);
    if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < (size_t)orig_len) return PRZM_ERR_BUFFER_TOO_SMALL;

    switch (mode) {
        case PRZM_ENTROPY_MODE_FLAT:
            if (src_len - 5 < (size_t)orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 5, (size_t)orig_len);
            return (int64_t)orig_len;

        case PRZM_ENTROPY_MODE_HUFFMAN_4X:
            return huffman4x_decode(src, src_len, dst, dst_cap, dec_table);

        case PRZM_ENTROPY_MODE_HUFFMAN_COMPACT:
            return huffman_compact_decode(src, src_len, dst, dst_cap, dec_table);

        case PRZM_ENTROPY_MODE_HUFFMAN_COMPACT_4X:
            return huffman_compact4x_decode(src, src_len, dst, dst_cap, dec_table);

        case PRZM_ENTROPY_MODE_RECURSIVE_LZ: {
            if ((size_t)orig_len > sizeof(tls_archival_scratch)) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
            int64_t dec_n = przm_decompress(src + 5, src_len - 5, tls_archival_scratch, sizeof(tls_archival_scratch));
            if (dec_n < 0 || (uint32_t)dec_n != orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, tls_archival_scratch, (size_t)dec_n);
            return dec_n;
        }

        case PRZM_ENTROPY_MODE_VANS_4X: {
            int err = vans_decode4x(src + 5, src_len - 5, dst, (size_t)orig_len);
            if (err != PRZM_OK) return err;
            return (int64_t)orig_len;
        }

        case PRZM_ENTROPY_MODE_DISTANCE_PREFIX: {
            if ((size_t)orig_len > sizeof(tls_off_scratch)) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
            return distance_prefix_decode(src, src_len, dst, dst_cap, dec_table,
                                          tls_archival_scratch, sizeof(tls_archival_scratch));
        }

        case PRZM_ENTROPY_MODE_SPLIT_VARINT: {
            if (src_len < 9) return PRZM_ERR_CORRUPTED_FRAME;
            size_t c_lsb_len = (size_t)read_u32_le(src + 5);
            if (9 + c_lsb_len > src_len) return PRZM_ERR_CORRUPTED_FRAME;
            const uint8_t* c_lsb = src + 9;
            const uint8_t* c_msb = src + 9 + c_lsb_len;
            size_t c_msb_len = src_len - (9 + c_lsb_len);

            if (c_lsb_len < 5 || c_msb_len < 5) return PRZM_ERR_CORRUPTED_FRAME;
            uint32_t lsb_orig_len = read_u32_le(c_lsb + 1);
            uint32_t msb_orig_len = read_u32_le(c_msb + 1);
            if (lsb_orig_len + msb_orig_len != orig_len) return PRZM_ERR_CORRUPTED_FRAME;

            if (lsb_orig_len > sizeof(tls_split_scratch) || msb_orig_len > sizeof(tls_archival_scratch)) {
                return PRZM_ERR_EXCEEDS_MAX_BLOCK;
            }

            int64_t n_lsb = przm_unpack_stream(c_lsb, c_lsb_len, tls_split_scratch, sizeof(tls_split_scratch), dec_table);
            if (n_lsb < 0 || (uint32_t)n_lsb != lsb_orig_len) return (n_lsb < 0) ? n_lsb : PRZM_ERR_CORRUPTED_FRAME;

            int64_t n_msb = przm_unpack_stream(c_msb, c_msb_len, tls_archival_scratch, sizeof(tls_archival_scratch), dec_table);
            if (n_msb < 0 || (uint32_t)n_msb != msb_orig_len) return (n_msb < 0) ? n_msb : PRZM_ERR_CORRUPTED_FRAME;

            size_t lsb_ptr = 0, msb_ptr = 0, dst_ptr = 0;
            while (lsb_ptr < (size_t)lsb_orig_len && dst_ptr < (size_t)orig_len) {
                uint8_t b0 = tls_split_scratch[lsb_ptr++];
                dst[dst_ptr++] = b0;
                while ((b0 & 0x80) != 0) {
                    if (msb_ptr >= (size_t)msb_orig_len || dst_ptr >= (size_t)orig_len) return PRZM_ERR_CORRUPTED_FRAME;
                    uint8_t bn = tls_archival_scratch[msb_ptr++];
                    dst[dst_ptr++] = bn;
                    if ((bn & 0x80) == 0) break;
                }
            }
            if (dst_ptr != (size_t)orig_len || lsb_ptr != (size_t)lsb_orig_len || msb_ptr != (size_t)msb_orig_len) {
                return PRZM_ERR_CORRUPTED_FRAME;
            }
            return (int64_t)orig_len;
        }

        default:
            return PRZM_ERR_CORRUPTED_FRAME;
    }
}

/* ============================================================================
 * 14. Standard Fused Frame Decoder Engine ('PRZM', 0x4D5A5250)
 * ============================================================================ */

PRZM_INLINE bool przm_check_cap(size_t alloc_size, size_t dst_pos, size_t len) {
    return (dst_pos <= alloc_size) && (len <= alloc_size - dst_pos);
}

static const char* const przm_hex_lut_lower = "0123456789abcdef";
static const char* const przm_hex_lut_upper = "0123456789ABCDEF";

void przm_expand_hex(uint8_t* dst, const uint8_t* src, size_t src_len, int upper) {
    const char* lut = upper ? przm_hex_lut_upper : przm_hex_lut_lower;
    for (size_t i = 0; i < src_len; i++) {
        uint8_t b = src[i];
        dst[2 * i]     = (uint8_t)lut[b >> 4];
        dst[2 * i + 1] = (uint8_t)lut[b & 0x0F];
    }
}

static const char* const przm_b64_lut_std = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
static const char* const przm_b64_lut_url = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

void przm_expand_base64(uint8_t* dst, const uint8_t* src, size_t src_len, int pad_count, int url_safe) {
    (void)pad_count;
    const char* lut = url_safe ? przm_b64_lut_url : przm_b64_lut_std;
    size_t full_triplets = src_len / 3;
    size_t rem = src_len % 3;
    size_t dst_idx = 0;

    for (size_t t = 0; t < full_triplets; t++) {
        size_t s_idx = t * 3;
        uint32_t u = ((uint32_t)src[s_idx] << 16) | ((uint32_t)src[s_idx + 1] << 8) | (uint32_t)src[s_idx + 2];
        dst[dst_idx]     = (uint8_t)lut[(u >> 18) & 0x3F];
        dst[dst_idx + 1] = (uint8_t)lut[(u >> 12) & 0x3F];
        dst[dst_idx + 2] = (uint8_t)lut[(u >> 6) & 0x3F];
        dst[dst_idx + 3] = (uint8_t)lut[u & 0x3F];
        dst_idx += 4;
    }

    if (rem == 2) {
        size_t s_idx = full_triplets * 3;
        uint32_t u = ((uint32_t)src[s_idx] << 16) | ((uint32_t)src[s_idx + 1] << 8);
        dst[dst_idx]     = (uint8_t)lut[(u >> 18) & 0x3F];
        dst[dst_idx + 1] = (uint8_t)lut[(u >> 12) & 0x3F];
        dst[dst_idx + 2] = (uint8_t)lut[(u >> 6) & 0x3F];
        dst[dst_idx + 3] = '=';
    } else if (rem == 1) {
        size_t s_idx = full_triplets * 3;
        uint32_t u = (uint32_t)src[s_idx] << 16;
        dst[dst_idx]     = (uint8_t)lut[(u >> 18) & 0x3F];
        dst[dst_idx + 1] = (uint8_t)lut[(u >> 12) & 0x3F];
        dst[dst_idx + 2] = '=';
        dst[dst_idx + 3] = '=';
    }
}

int przm_expand_spans(uint8_t* dst, size_t dst_len, const uint8_t* src, size_t src_len) {
    if (src_len < 2) return PRZM_ERR_CORRUPTED_FRAME;
    uint16_t num_spans = read_u16_le(src);
    size_t table_size = 2 + 13 * (size_t)num_spans;
    if (src_len < table_size) return PRZM_ERR_CORRUPTED_FRAME;

    size_t dst_pos = 0;
    size_t data_read_pos = table_size;

    for (uint16_t i = 0; i < num_spans; i++) {
        size_t desc_offset = 2 + 13 * (size_t)i;
        uint8_t span_type = src[desc_offset];
        size_t contracted_offset = (size_t)read_u32_le(src + desc_offset + 1);
        size_t contracted_len    = (size_t)read_u32_le(src + desc_offset + 5);
        size_t orig_len          = (size_t)read_u32_le(src + desc_offset + 9);

        if (contracted_offset < data_read_pos || contracted_offset + contracted_len > src_len) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }

        /* Copy unmodified literals preceding this span */
        size_t lit_len = contracted_offset - data_read_pos;
        if (lit_len > 0) {
            if (dst_pos + lit_len > dst_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst + dst_pos, src + data_read_pos, lit_len);
            dst_pos += lit_len;
        }

        if (dst_pos + orig_len > dst_len) return PRZM_ERR_CORRUPTED_FRAME;
        const uint8_t* span_data = src + contracted_offset;

        size_t expected_orig_len = 0;
        if (span_type == PRZM_SPAN_HEX_LOWER || span_type == PRZM_SPAN_HEX_UPPER) {
            expected_orig_len = contracted_len * 2;
        } else if (span_type == PRZM_SPAN_BASE64_STD || span_type == PRZM_SPAN_BASE64_URL) {
            if (contracted_len % 3 != 0) return PRZM_ERR_CORRUPTED_FRAME;
            expected_orig_len = (contracted_len / 3) * 4;
        } else if (span_type == PRZM_SPAN_BASE64_PAD1 || span_type == PRZM_SPAN_BASE64_URL_P1) {
            if (contracted_len % 3 != 2) return PRZM_ERR_CORRUPTED_FRAME;
            expected_orig_len = ((contracted_len + 1) / 3) * 4;
        } else if (span_type == PRZM_SPAN_BASE64_PAD2 || span_type == PRZM_SPAN_BASE64_URL_P2) {
            if (contracted_len % 3 != 1) return PRZM_ERR_CORRUPTED_FRAME;
            expected_orig_len = ((contracted_len + 2) / 3) * 4;
        } else {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        if (orig_len != expected_orig_len) return PRZM_ERR_CORRUPTED_FRAME;

        switch (span_type) {
        case PRZM_SPAN_HEX_LOWER:
            przm_expand_hex(dst + dst_pos, span_data, contracted_len, 0);
            break;
        case PRZM_SPAN_HEX_UPPER:
            przm_expand_hex(dst + dst_pos, span_data, contracted_len, 1);
            break;
        case PRZM_SPAN_BASE64_STD:
            przm_expand_base64(dst + dst_pos, span_data, contracted_len, 0, 0);
            break;
        case PRZM_SPAN_BASE64_PAD1:
            przm_expand_base64(dst + dst_pos, span_data, contracted_len, 1, 0);
            break;
        case PRZM_SPAN_BASE64_PAD2:
            przm_expand_base64(dst + dst_pos, span_data, contracted_len, 2, 0);
            break;
        case PRZM_SPAN_BASE64_URL:
            przm_expand_base64(dst + dst_pos, span_data, contracted_len, 0, 1);
            break;
        case PRZM_SPAN_BASE64_URL_P1:
            przm_expand_base64(dst + dst_pos, span_data, contracted_len, 1, 1);
            break;
        case PRZM_SPAN_BASE64_URL_P2:
            przm_expand_base64(dst + dst_pos, span_data, contracted_len, 2, 1);
            break;
        }

        dst_pos += orig_len;
        data_read_pos = contracted_offset + contracted_len;
    }

    /* Copy trailing literals */
    if (data_read_pos < src_len) {
        size_t rem = src_len - data_read_pos;
        if (dst_pos + rem > dst_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst + dst_pos, src + data_read_pos, rem);
        dst_pos += rem;
    }

    if (dst_pos != dst_len) return PRZM_ERR_CORRUPTED_FRAME;
    return PRZM_OK;
}

static int64_t decompress_standard_fused(const uint8_t* src, size_t src_len,
                                         uint8_t* dst, size_t dst_cap,
                                         const przm_dictionary_t* ext_dict) {
    if (src_len < PRZM_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;

    uint8_t flags = src[4];
    size_t u_size = (size_t)read_u24_le(src + 5);

    if (u_size > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < u_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    /* Raw uncompressed fast path */
    if ((flags & PRZM_FLAG_UNCOMPRESSED) != 0) {
        if (src_len < PRZM_HEADER_SIZE || src_len - PRZM_HEADER_SIZE < u_size) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst, src + PRZM_HEADER_SIZE, u_size);
        return (int64_t)u_size;
    }

    size_t offset = PRZM_HEADER_SIZE;
    int stride = 1;
    uint32_t dict_id = 0;
    uint32_t call_count = 0;

    if ((flags & PRZM_FLAG_STRIDE) != 0) {
        if (offset >= src_len) return PRZM_ERR_CORRUPTED_FRAME;
        stride = (int)src[offset++] + 1;
    }
    if ((flags & PRZM_FLAG_DICT_REF) != 0) {
        if (offset + 4 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        dict_id = read_u32_le(src + offset);
        offset += 4;
    }
    bool has_norm = false;
    uint8_t norm_mode = 0;
    uint32_t contracted_len = 0;

    if ((flags & 0x20) != 0) {
        size_t sec_dir_len = ((flags & PRZM_FLAG_EXT_OFFSET) != 0) ? 6 : 4;
        size_t test_sec1 = offset + sec_dir_len;
        if (test_sec1 + 8 <= src_len && src[test_sec1 + 2] != 0 && src[test_sec1 + 2] <= PRZM_NORM_BASE64_URL_P2) {
            has_norm = true;
            norm_mode = src[test_sec1 + 2];
            contracted_len = (uint32_t)read_u16_le(src + test_sec1 + 3) |
                             ((uint32_t)src[test_sec1 + 6] << 16) |
                             ((uint32_t)src[test_sec1 + 7] << 24);
        }
        if (!has_norm) {
            if (offset + 4 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
            call_count = read_u32_le(src + offset);
            offset += 4;
        }
    }

    /* Section Directory */
    size_t cmd_size = 0, off_size = 0;
    if ((flags & PRZM_FLAG_EXT_OFFSET) == 0) {
        if (offset + 4 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        cmd_size = read_u16_le(src + offset);
        off_size = read_u16_le(src + offset + 2);
        offset += 4;
    } else {
        if (offset + 6 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        cmd_size = read_u24_le(src + offset);
        off_size = read_u24_le(src + offset + 3);
        offset += 6;
    }

    size_t sec1_offset = offset;
    size_t sec1_size = 0;
    if (sec1_offset + 8 <= src_len) {
        if (has_norm) {
            norm_mode = src[sec1_offset + 2];
            contracted_len = (uint32_t)read_u16_le(src + sec1_offset + 3) |
                             ((uint32_t)src[sec1_offset + 6] << 16) |
                             ((uint32_t)src[sec1_offset + 7] << 24);
        } else {
            norm_mode = PRZM_NORM_NONE;
        }
        uint8_t exception_count = src[sec1_offset + 5];
        sec1_size = 8 + 2 * (size_t)exception_count;
    }
    size_t sec2_offset = sec1_offset + sec1_size;
    size_t sec3_offset = sec2_offset + cmd_size;
    size_t sec4_offset = sec3_offset + off_size;

    if (sec2_offset < sec1_offset || sec3_offset < sec2_offset ||
        sec4_offset < sec3_offset || sec4_offset > src_len) {
        return PRZM_ERR_CORRUPTED_FRAME;
    }
    size_t sec4_size = src_len - sec4_offset;

    /* Decode Section 4 Literals */
    const uint8_t* lits = NULL;
    size_t lits_len = 0;

    if ((flags & PRZM_FLAG_NIBBLE) != 0) {
        const uint8_t* sec4_data = src + sec4_offset;
        if (sec4_size >= 8 && sec4_data[0] == PRZM_MAGIC_VANS_0 &&
            sec4_data[1] == PRZM_MAGIC_VANS_1 && sec4_data[2] == PRZM_VERSION_1) {
            /* V-ANS literals */
            size_t orig_lits = (size_t)read_u32_le(sec4_data + 4);
            if (orig_lits > PRZM_TLS_SCRATCH_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
            int err = vans_decode4x(sec4_data, sec4_size, tls_lits_scratch, orig_lits);
            if (err != PRZM_OK) return err;
            lits = tls_lits_scratch;
            lits_len = orig_lits;
        } else {
            /* Nibble-plane literals */
            if (sec4_size < 8) return PRZM_ERR_CORRUPTED_FRAME;
            size_t orig_lits = (size_t)read_u32_le(sec4_data);
            if (orig_lits > PRZM_TLS_SCRATCH_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
            int err = unpack_nibble_planes(sec4_data, sec4_size, tls_lits_scratch, orig_lits);
            if (err != PRZM_OK) return err;
            lits = tls_lits_scratch;
            lits_len = orig_lits;
        }
    } else {
        lits = src + sec4_offset;
        lits_len = sec4_size;
    }

    /* Decode Section 2 (Commands) */
    const uint8_t* cmds = NULL;
    size_t cmds_len = 0;
    if (cmd_size > 0) {
        if (sec2_offset + 5 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t sec2_orig_len = read_u32_le(src + sec2_offset + 1);
        if (sec2_orig_len > PRZM_TLS_SCRATCH_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        int64_t dec_n = przm_unpack_stream(src + sec2_offset, cmd_size, tls_cmd_scratch, sizeof(tls_cmd_scratch), &tls_huff_table);
        if (dec_n < 0 || (uint32_t)dec_n != sec2_orig_len) return (dec_n < 0) ? (int)dec_n : PRZM_ERR_CORRUPTED_FRAME;
        cmds = tls_cmd_scratch;
        cmds_len = (size_t)dec_n;
    }

    /* Decode Section 3 (Offsets) */
    const uint8_t* offs = NULL;
    size_t offs_len = 0;
    if (off_size > 0) {
        if (sec3_offset + 5 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        uint32_t sec3_orig_len = read_u32_le(src + sec3_offset + 1);
        if (sec3_orig_len > PRZM_TLS_SCRATCH_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        int64_t dec_n = przm_unpack_stream(src + sec3_offset, off_size, tls_off_scratch, sizeof(tls_off_scratch), &tls_huff_table);
        if (dec_n < 0 || (uint32_t)dec_n != sec3_orig_len) return (dec_n < 0) ? (int)dec_n : PRZM_ERR_CORRUPTED_FRAME;
        offs = tls_off_scratch;
        offs_len = (size_t)dec_n;
    }

    /* Initialize 3 CPU repeat offset registers */
    int rep0 = 1;
    int rep1 = 4;
    int rep2 = 8;
    if ((flags & PRZM_FLAG_STRIDE) != 0 && stride > 1) {
        rep1 = stride;
        rep2 = 2 * stride;
    }

    size_t cmd_ptr = 0;
    size_t cmd_end = cmds_len;
    size_t off_ptr = 0;
    size_t lit_ptr = 0;
    size_t dst_pos = 0;
    size_t min_match_base = (flags & PRZM_FLAG_MIN_MATCH_3) ? 3 : 4;

    bool is_split = !has_norm && ((flags & 0x20) != 0);
    size_t alloc_size = u_size;
    uint8_t* decode_dst = dst;

    if (is_split) {
        if ((size_t)call_count > sizeof(tls_split_scratch) / 4) {
            return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        }
        alloc_size = 4 + 4 * (size_t)call_count + u_size;
        if (alloc_size > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < alloc_size + PRZM_SAFETY_PADDING) {
            if (alloc_size + PRZM_SAFETY_PADDING > sizeof(tls_split_scratch)) {
                return PRZM_ERR_BUFFER_TOO_SMALL;
            }
            decode_dst = tls_split_scratch;
        }
    } else if (norm_mode != PRZM_NORM_NONE) {
        alloc_size = (size_t)contracted_len;
        if (alloc_size > sizeof(tls_norm_scratch) - PRZM_SAFETY_PADDING) {
            return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        }
        decode_dst = tls_norm_scratch;
    } else {
        if (dst_cap < u_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;
    }

    /* Command Stream Execution Loop */
    while (dst_pos < alloc_size && cmd_ptr < cmd_end) {
        uint8_t op = cmds[cmd_ptr++];

        if (op < 0x80) {
            /* Fast-Path 1: Fused Repeat Token (0 LLL MM KK) */
            size_t lit_len = (op >> 4) & 0x07;
            size_t m_len = ((op >> 2) & 0x03) + min_match_base;
            int k = (int)(op & 0x03);

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = 0;
            if (k == 0) {
                d = rep0;
            } else if (k == 1) {
                d = rep1;
                rep1 = rep0;
                rep0 = d;
            } else if (k == 2) {
                d = rep2;
                rep2 = rep1;
                rep1 = rep0;
                rep0 = d;
            } else {
                return PRZM_ERR_CORRUPTED_FRAME;
            }

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op < 0xC0) {
            /* Fast-Path 2: Fused Extended Match (10 LL MMMM) */
            size_t lit_len = (op >> 4) & 0x03;
            size_t base_len = op & 0x0F;
            size_t m_len = base_len + min_match_base;
            if (base_len == 15) {
                int extra = 0;
                int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 15 + min_match_base + (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = 0;
            int err = fast_read_varint(offs, offs_len, &off_ptr, &d);
            if (err != PRZM_OK || d <= 0) return PRZM_ERR_CORRUPTED_FRAME;

            rep2 = rep1;
            rep1 = rep0;
            rep0 = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        /* Specialized Opcodes (0xC0 .. 0xFF) */
        if (op >= 0xC0 && op <= 0xC3) {
            /* Long Repeat Match (k 0..2) */
            int k = (int)(op & 0x0F);
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t token = cmds[cmd_ptr++];
            size_t lit_len = token >> 4;
            size_t base_len = token & 0x0F;
            size_t m_len = base_len + min_match_base;
            if (base_len == 15) {
                int extra = 0;
                int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 15 + min_match_base + (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = 0;
            if (k == 0) {
                d = rep0;
            } else if (k == 1) {
                d = rep1;
                rep1 = rep0;
                rep0 = d;
            } else if (k == 2) {
                d = rep2;
                rep2 = rep1;
                rep1 = rep0;
                rep0 = d;
            } else {
                return PRZM_ERR_CORRUPTED_FRAME;
            }

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op >= 0xC4 && op <= 0xCD) {
            /* Fused Offset Bracket (0xC4..0xCD) */
            int bracket = (int)(op - 0xC4);
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t token = cmds[cmd_ptr++];
            size_t lit_len = (size_t)(token >> 4);
            size_t base_len = (size_t)(token & 0x0F);
            size_t m_len = base_len + min_match_base;

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = 0;
            if (bracket < 8) {
                d = bracket + 1;
            } else if (bracket == 8) {
                d = 16;
            } else {
                d = 32;
            }

            rep2 = rep1;
            rep1 = rep0;
            rep0 = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op == 0xCE) {
            /* Sliding Difference Lattice & Direct 1-byte Immediate Offset */
            if (cmd_ptr + 1 >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t desc = cmds[cmd_ptr++];
            uint8_t delta_byte = cmds[cmd_ptr++];

            int mode = (int)((desc >> 6) & 0x03);
            size_t lit_len = (size_t)((desc >> 3) & 0x07);
            size_t base_len = (size_t)(desc & 0x07);
            size_t m_len = base_len + min_match_base;

            if (lit_len == 7) {
                int extra = 0;
                int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                lit_len += (size_t)extra;
            }
            if (base_len == 7) {
                int extra = 0;
                int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len += (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = 0;
            int delta_signed = (int)((int8_t)delta_byte);
            switch (mode) {
            case 0:
                d = rep0 + delta_signed;
                break;
            case 1:
                d = rep1 + delta_signed;
                break;
            case 2:
                d = rep2 + delta_signed;
                break;
            case 3:
                if (stride > 1) {
                    d = rep0 + delta_signed * (int)stride;
                } else {
                    d = (int)delta_byte + 1;
                }
                break;
            }

            rep2 = rep1;
            rep1 = rep0;
            rep0 = d;

            if (dst_pos < (size_t)d || d <= 0) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op >= 0xD0 && op <= 0xD7) {
            /* Stride Match (m 1..8) */
            int m = (int)(op & 0x07) + 1;
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t token = cmds[cmd_ptr++];
            size_t lit_len = (size_t)(token >> 4);
            size_t m_len = (size_t)(token & 0x0F) + min_match_base;

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = m * stride;
            rep2 = rep1;
            rep1 = rep0;
            rep0 = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op >= 0xD8 && op <= 0xDF) {
            /* Direct 1-byte schema token */
            uint8_t tok_id = (uint8_t)(op - 0xD8);
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            continue;
        }

        if (op >= 0xE0 && op <= 0xE7) {
            /* Delta-Mask Match (Locality) */
            int k = (int)(op & 0x07);
            int d = 0;
            if (k == 0) {
                d = rep0;
            } else if (k == 1) {
                d = rep1;
                rep1 = rep0;
                rep0 = d;
            } else if (k == 2) {
                d = rep2;
                rep2 = rep1;
                rep1 = rep0;
                rep0 = d;
            } else {
                return PRZM_ERR_CORRUPTED_FRAME;
            }

            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            size_t base_byte = cmds[cmd_ptr++];
            size_t m_len = base_byte + min_match_base;
            if (base_byte == 255) {
                int extra = 0;
                int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 255 + min_match_base + (size_t)extra;
            }

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);

            /* Apply delta residual mutations */
            if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t c_mods = lits[lit_ptr++];
            for (uint8_t m = 0; m < c_mods; m++) {
                size_t rel_off = 0;
                if (m_len <= 256) {
                    if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                    rel_off = lits[lit_ptr++];
                } else {
                    if (lit_ptr + 2 > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                    rel_off = read_u16_le(lits + lit_ptr);
                    lit_ptr += 2;
                }
                if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                uint8_t xor_val = lits[lit_ptr++];
                if (rel_off < m_len) {
                    decode_dst[dst_pos + rel_off] ^= xor_val;
                }
            }
            dst_pos += m_len;
            continue;
        }

        if (op == 0xE8) {
            /* Delta-Mask Match (Explicit) */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            size_t base_byte = cmds[cmd_ptr++];
            size_t m_len = base_byte + min_match_base;
            if (base_byte == 255) {
                int extra = 0;
                int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 255 + min_match_base + (size_t)extra;
            }

            int d = 0;
            int err = fast_read_varint(offs, offs_len, &off_ptr, &d);
            if (err != PRZM_OK || d <= 0) return PRZM_ERR_CORRUPTED_FRAME;

            rep2 = rep1;
            rep1 = rep0;
            rep0 = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);

            if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t c_mods = lits[lit_ptr++];
            for (uint8_t m = 0; m < c_mods; m++) {
                size_t rel_off = 0;
                if (m_len <= 256) {
                    if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                    rel_off = lits[lit_ptr++];
                } else {
                    if (lit_ptr + 2 > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                    rel_off = read_u16_le(lits + lit_ptr);
                    lit_ptr += 2;
                }
                if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                uint8_t xor_val = lits[lit_ptr++];
                if (rel_off < m_len) {
                    decode_dst[dst_pos + rel_off] ^= xor_val;
                }
            }
            dst_pos += m_len;
            continue;
        }

        if (op == 0xF0) {
            /* Pure Literal Run */
            int run_len = 0;
            int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &run_len);
            if (err != PRZM_OK || run_len < 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (lit_ptr + (size_t)run_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, (size_t)run_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, lits + lit_ptr, (size_t)run_len);
            lit_ptr += (size_t)run_len;
            dst_pos += (size_t)run_len;
            continue;
        }

        if (op == 0xF1) {
            /* Pure RLE Run */
            int rle_len = 0;
            int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &rle_len);
            if (err != PRZM_OK || rle_len < 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (lit_ptr >= lits_len) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, (size_t)rle_len)) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t val = lits[lit_ptr++];
            memset(decode_dst + dst_pos, val, (size_t)rle_len);
            dst_pos += (size_t)rle_len;
            continue;
        }

        if (op == 0xF2) {
            /* Extended Schema Token */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            continue;
        }

        if (op == 0xEA) {
            /* Transformed Token: " " + tok */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 1)) return PRZM_ERR_CORRUPTED_FRAME;
            decode_dst[dst_pos++] = ' ';
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            continue;
        }

        if (op == 0xEB) {
            /* Transformed Token: tok + "\"" */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 1)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            decode_dst[dst_pos++] = '"';
            continue;
        }

        if (op == 0xEC) {
            /* Transformed Token: tok + "{" */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 1)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            decode_dst[dst_pos++] = '{';
            continue;
        }

        if (op == 0xED) {
            /* Transformed Token: UppercaseFirst(tok) */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            if (decode_dst[dst_pos] >= 'a' && decode_dst[dst_pos] <= 'z') {
                decode_dst[dst_pos] -= ('a' - 'A');
            }
            dst_pos += t_len;
            continue;
        }

        if (op == 0xEE) {
            /* Transformed Token: tok + ";" */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 1)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            decode_dst[dst_pos++] = ';';
            continue;
        }

        if (op == 0xEF) {
            /* Transformed Token: " " + tok + " " */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 2)) return PRZM_ERR_CORRUPTED_FRAME;
            decode_dst[dst_pos++] = ' ';
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            decode_dst[dst_pos++] = ' ';
            continue;
        }

        if (op == 0xFB) {
            /* Transformed Token: tok + "=\"" */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 2)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            decode_dst[dst_pos++] = '=';
            decode_dst[dst_pos++] = '"';
            continue;
        }

        if (op == 0xFC) {
            /* Transformed Token: tok + ">" */
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok_id = cmds[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len + 1)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            decode_dst[dst_pos++] = '>';
            continue;
        }

        if (op == 0xFF) {
            /* Transformed Token Pair: tok1 + tok2 */
            if (cmd_ptr + 2 > cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t tok1_id = cmds[cmd_ptr++];
            uint8_t tok2_id = cmds[cmd_ptr++];
            size_t t1_len = 0, t2_len = 0;
            const char* str1 = get_schema_token(dict_id, tok1_id, ext_dict, &t1_len);
            const char* str2 = get_schema_token(dict_id, tok2_id, ext_dict, &t2_len);
            if (!str1 || t1_len == 0 || !str2 || t2_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t1_len + t2_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str1, t1_len);
            dst_pos += t1_len;
            memcpy(decode_dst + dst_pos, str2, t2_len);
            dst_pos += t2_len;
            continue;
        }

        if (op >= 0xF3 && op <= 0xFA) {
            /* Direct 1-byte literal run */
            size_t lit_len = (size_t)(op - 0xF3 + 1);
            if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
            lit_ptr += lit_len;
            dst_pos += lit_len;
            continue;
        }

        if (op == PRZM_OP_COPY_BLOCK) {
            if (cmd_ptr + 4 > cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint32_t src_block_idx = read_u32_le(cmds + cmd_ptr);
            cmd_ptr += 4;
            if (src_block_idx >= (uint32_t)(dst_pos / PRZM_DEDUP_BLOCK_SIZE)) return PRZM_ERR_CORRUPTED_FRAME;
            size_t ref = (size_t)src_block_idx * PRZM_DEDUP_BLOCK_SIZE;
            if (ref + PRZM_DEDUP_BLOCK_SIZE > dst_pos) return PRZM_ERR_CORRUPTED_FRAME;
            if (dst_pos + PRZM_DEDUP_BLOCK_SIZE > alloc_size || !przm_check_cap(alloc_size, dst_pos, PRZM_DEDUP_BLOCK_SIZE)) {
                return PRZM_ERR_CORRUPTED_FRAME;
            }
            memcpy(decode_dst + dst_pos, decode_dst + ref, PRZM_DEDUP_BLOCK_SIZE);
            dst_pos += PRZM_DEDUP_BLOCK_SIZE;
            continue;
        }

        if (op == PRZM_OP_GRAMMAR_BLOCK) {
            int comp_size = 0;
            int err = fast_read_varint(cmds, cmd_end, &cmd_ptr, &comp_size);
            if (err != PRZM_OK || comp_size <= 0 || cmd_ptr + (size_t)comp_size > cmd_end) {
                return PRZM_ERR_CORRUPTED_FRAME;
            }
            const uint8_t* pws_chunk = cmds + cmd_ptr;
            cmd_ptr += (size_t)comp_size;
            if ((size_t)comp_size < 8) return PRZM_ERR_CORRUPTED_FRAME;
            size_t orig_size = (size_t)read_u32_le(pws_chunk + 4);
            if (orig_size == 0 || !przm_check_cap(alloc_size, dst_pos, orig_size)) {
                return PRZM_ERR_CORRUPTED_FRAME;
            }
            int64_t dec_ret = pws_decode(pws_chunk, (size_t)comp_size, decode_dst + dst_pos,
                                         alloc_size - dst_pos + PRZM_SAFETY_PADDING);
            if (dec_ret < 0 || (size_t)dec_ret != orig_size) return PRZM_ERR_CORRUPTED_FRAME;
            dst_pos += orig_size;
            continue;
        }

        return PRZM_ERR_CORRUPTED_FRAME;
    }

    if (dst_pos != alloc_size) return PRZM_ERR_CORRUPTED_FRAME;

    if (norm_mode != PRZM_NORM_NONE) {
        size_t expected_u_size = 0;
        if (norm_mode == PRZM_NORM_HEX || norm_mode == PRZM_NORM_HEX_UPPER) {
            expected_u_size = (size_t)contracted_len * 2;
        } else if (norm_mode == PRZM_NORM_BASE64 || norm_mode == PRZM_NORM_BASE64_URL) {
            if (contracted_len % 3 != 0) return PRZM_ERR_CORRUPTED_FRAME;
            expected_u_size = ((size_t)contracted_len / 3) * 4;
        } else if (norm_mode == PRZM_NORM_BASE64_PAD1 || norm_mode == PRZM_NORM_BASE64_URL_P1) {
            if (contracted_len % 3 != 2) return PRZM_ERR_CORRUPTED_FRAME;
            expected_u_size = (((size_t)contracted_len + 1) / 3) * 4;
        } else if (norm_mode == PRZM_NORM_BASE64_PAD2 || norm_mode == PRZM_NORM_BASE64_URL_P2) {
            if (contracted_len % 3 != 1) return PRZM_ERR_CORRUPTED_FRAME;
            expected_u_size = (((size_t)contracted_len + 2) / 3) * 4;
        }
        if (norm_mode != PRZM_NORM_SPANS && u_size != expected_u_size) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }

        if (norm_mode == PRZM_NORM_HEX) {
            przm_expand_hex(dst, tls_norm_scratch, contracted_len, 0);
        } else if (norm_mode == PRZM_NORM_HEX_UPPER) {
            przm_expand_hex(dst, tls_norm_scratch, contracted_len, 1);
        } else if (norm_mode == PRZM_NORM_BASE64) {
            przm_expand_base64(dst, tls_norm_scratch, contracted_len, 0, 0);
        } else if (norm_mode == PRZM_NORM_BASE64_PAD1) {
            przm_expand_base64(dst, tls_norm_scratch, contracted_len, 1, 0);
        } else if (norm_mode == PRZM_NORM_BASE64_PAD2) {
            przm_expand_base64(dst, tls_norm_scratch, contracted_len, 2, 0);
        } else if (norm_mode == PRZM_NORM_BASE64_URL) {
            przm_expand_base64(dst, tls_norm_scratch, contracted_len, 0, 1);
        } else if (norm_mode == PRZM_NORM_BASE64_URL_P1) {
            przm_expand_base64(dst, tls_norm_scratch, contracted_len, 1, 1);
        } else if (norm_mode == PRZM_NORM_BASE64_URL_P2) {
            przm_expand_base64(dst, tls_norm_scratch, contracted_len, 2, 1);
        } else if (norm_mode == PRZM_NORM_SPANS) {
            int err = przm_expand_spans(dst, u_size, tls_norm_scratch, contracted_len);
            if (err != PRZM_OK) return err;
        } else {
            return PRZM_ERR_CORRUPTED_FRAME;
        }
        return (int64_t)u_size;
    }

    /* Apply Post-Decompression Filters */
    if (is_split) {
        const uint8_t* b0 = decode_dst + 4;
        const uint8_t* b1 = decode_dst + 4 + call_count;
        const uint8_t* b2 = decode_dst + 4 + 2 * call_count;
        const uint8_t* b3 = decode_dst + 4 + 3 * call_count;
        const uint8_t* code = decode_dst + 4 + 4 * call_count;

        if (decode_dst != dst) {
            memcpy(dst, code, u_size);
        } else {
            memcpy(tls_split_scratch, decode_dst + 4, 4 * call_count);
            b0 = tls_split_scratch;
            b1 = tls_split_scratch + call_count;
            b2 = tls_split_scratch + 2 * call_count;
            b3 = tls_split_scratch + 3 * call_count;
            memmove(dst, code, u_size);
        }

        size_t c_idx = 0;
        size_t lim = (u_size >= 4) ? u_size - 4 : 0;
        for (size_t i = 0; i < lim && c_idx < (size_t)call_count; ) {
            uint8_t op_byte = dst[i];
            if (op_byte == 0xE8 || op_byte == 0xE9) {
                uint32_t val = (uint32_t)b0[c_idx] | ((uint32_t)b1[c_idx] << 8) |
                               ((uint32_t)b2[c_idx] << 16) | ((uint32_t)b3[c_idx] << 24);
                write_u32_le(dst + i + 1, val);
                c_idx++;
                i += 5;
            } else {
                i++;
            }
        }
        x86_bcj_inverse(dst, u_size);
    }

    return (int64_t)u_size;
}

/* ============================================================================
 * 14.5. Archival Mode PNG Deflate Container Decompression
 * ============================================================================ */

static int64_t przm_archival_png_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t orig_len_u32 = read_u32_le(src + 4);
    if (orig_len_u32 > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    size_t orig_len = (size_t)orig_len_u32;

    if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    size_t meta_size = (size_t)read_u32_le(src + 8);
    if (12 + meta_size > src_len || meta_size < 8) return PRZM_ERR_CORRUPTED_FRAME;

    const uint8_t* meta_buf = src + 12;
    if (meta_buf[0] != 0x50) return PRZM_ERR_CORRUPTED_FRAME;

    uint16_t prefix_len = read_u16_le(meta_buf + 2);
    uint16_t postfix_len = read_u16_le(meta_buf + 4);
    uint16_t idat_count = read_u16_le(meta_buf + 6);

    size_t expected_meta = 8 + (size_t)idat_count * 8 + (size_t)prefix_len + (size_t)postfix_len;
    if (meta_size != expected_meta) return PRZM_ERR_CORRUPTED_FRAME;

    /* Decompress inner scanlines with 0 heap allocations */
    const uint8_t* inner_src = src + 12 + meta_size;
    size_t inner_src_len = src_len - (12 + meta_size);

    int64_t inner_len = przm_decompress(inner_src, inner_src_len, tls_archival_scratch, sizeof(tls_archival_scratch));
    if (inner_len < 0) return inner_len;

    /* Reconstruct PNG container into dst */
    size_t dst_pos = 0;
    size_t desc_pos = 8;
    size_t prefix_pos = 8 + (size_t)idat_count * 8;
    size_t postfix_pos = prefix_pos + (size_t)prefix_len;

    /* 1. Prefix (PNG magic + IHDR + ancillary chunks) */
    if (prefix_len > 0) {
        if (dst_pos + prefix_len > orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst + dst_pos, meta_buf + prefix_pos, prefix_len);
        dst_pos += prefix_len;
    }

    /* 2. IDAT Chunks */
    size_t inner_pos = 0;
    for (size_t i = 0; i < (size_t)idat_count; i++) {
        uint32_t chunk_len = read_u32_le(meta_buf + desc_pos);
        desc_pos += 8;

        if (inner_pos + chunk_len > (size_t)inner_len || dst_pos + 12 + chunk_len > orig_len) {
            return PRZM_ERR_CORRUPTED_FRAME;
        }

        /* Chunk length (4 bytes Big-Endian) */
        dst[dst_pos]     = (uint8_t)(chunk_len >> 24);
        dst[dst_pos + 1] = (uint8_t)(chunk_len >> 16);
        dst[dst_pos + 2] = (uint8_t)(chunk_len >> 8);
        dst[dst_pos + 3] = (uint8_t)chunk_len;
        dst_pos += 4;

        /* Chunk type "IDAT" */
        dst[dst_pos]     = 'I';
        dst[dst_pos + 1] = 'D';
        dst[dst_pos + 2] = 'A';
        dst[dst_pos + 3] = 'T';
        dst_pos += 4;

        /* Payload */
        memcpy(dst + dst_pos, tls_archival_scratch + inner_pos, chunk_len);
        dst_pos += chunk_len;
        inner_pos += chunk_len;

        /* Chunk CRC (4 bytes Big-Endian, copied directly from descriptor) */
        dst[dst_pos]     = meta_buf[desc_pos - 4];
        dst[dst_pos + 1] = meta_buf[desc_pos - 3];
        dst[dst_pos + 2] = meta_buf[desc_pos - 2];
        dst[dst_pos + 3] = meta_buf[desc_pos - 1];
        dst_pos += 4;
    }

    if (inner_pos != (size_t)inner_len) return PRZM_ERR_CORRUPTED_FRAME;

    /* 3. Postfix (ancillary chunks + IEND) */
    if (postfix_len > 0) {
        if (dst_pos + postfix_len > orig_len) return PRZM_ERR_CORRUPTED_FRAME;
        memcpy(dst + dst_pos, meta_buf + postfix_pos, postfix_len);
        dst_pos += postfix_len;
    }

    if (dst_pos != orig_len) return PRZM_ERR_CORRUPTED_FRAME;

    return (int64_t)orig_len;
}

/* ============================================================================
 * 15. Public API Implementation: przm_decompress
 * ============================================================================ */

int64_t przm_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len == 0) return 0;
    if (src == NULL || dst == NULL || src_len < 4) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t magic = read_u32_le(src);

    /* Multi-Chunk Stream ('PRZS', 0x535A5250) */
    if (magic == PRZM_MAGIC_STREAM) {
        if (src_len < PRZM_STREAM_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        int64_t total_u_size = (int64_t)read_u64_le(src + 5);
        if (total_u_size < 0) return PRZM_ERR_CORRUPTED_FRAME;
        if (dst_cap < (size_t)total_u_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;
        uint32_t chunk_count = read_u32_le(src + 13);

        size_t offset = PRZM_STREAM_HEADER_SIZE;
        size_t dst_pos = 0;

        for (uint32_t i = 0; i < chunk_count; i++) {
            if (offset + 4 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint32_t chunk_comp_size = read_u32_le(src + offset);
            offset += 4;
            if (offset + chunk_comp_size > src_len) return PRZM_ERR_CORRUPTED_FRAME;

            const uint8_t* chunk_ptr = src + offset;
            offset += chunk_comp_size;

            int64_t chunk_u_size = przm_decompress(chunk_ptr, (size_t)chunk_comp_size, dst + dst_pos, dst_cap - dst_pos);
            if (chunk_u_size < 0) return chunk_u_size;

            dst_pos += (size_t)chunk_u_size;
            if (dst_pos > (size_t)total_u_size) return PRZM_ERR_CORRUPTED_FRAME;
        }

        if (dst_pos != (size_t)total_u_size) return PRZM_ERR_CORRUPTED_FRAME;
        return total_u_size;
    }

    /* 1.10. Archival Mode PNG Container ('PNGA', 0x504E4741) */
    if (magic == PRZM_MAGIC_ARCHIVAL_PNG) {
        return przm_archival_png_decompress(src, src_len, dst, dst_cap);
    }

    /* 1.8. Fast Möbius Transform Frame ('MOBI', 0x49424F4D) */
    if (magic == PRZM_MAGIC_MOBIUS) {
        return przm_mobius_decompress(src, src_len, dst, dst_cap);
    }

    /* 1.9. Margolus Reversible Cellular Automata Frame ('MARG', 0x4752414D) */
    if (magic == PRZM_MAGIC_MARGOLUS) {
        return przm_margolus_decompress(src, src_len, dst, dst_cap);
    }

    /* 1. Symplectic Sp(4, F_{2^8}) Frame */
    if (magic == PRZM_MAGIC_SYMPLECTIC) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        uint8_t mode = src[4];
        SymplecticShear shear = {src[5], src[6], src[7]};
        size_t orig_len = (size_t)read_u32_le(src + 8);
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

        if (mode == 0) {
            /* Raw */
            if (src_len < 12 || src_len - 12 < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            /* Shear only */
            if (src_len < 12 || src_len - 12 < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            symplectic_shear_inplace(dst, orig_len, shear);
            return (int64_t)orig_len;
        } else if (mode == 3) {
            /* Compressed (cascaded PWS) */
            int64_t dec = pws_decode(src + 12, src_len - 12, dst, dst_cap);
            if (dec < 0) return dec;
            symplectic_shear_inplace(dst, (size_t)dec, shear);
            return dec;
        }
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    /* 1.4. Galois Superoptimized JIT & Trace Annihilation ('GCTR', 0x47435452) */
    if (magic == PRZM_MAGIC_GCTR) {
        return przm_gctr_decompress(src, src_len, dst, dst_cap);
    }

    /* 1.5. IsoQuant 4D Quaternionic Collimation Frame ('ISOQ', 0x514F5349) */
    if (magic == PRZM_MAGIC_ISOQUANT) {
        if (src_len < PRZM_ISOQUANT_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        uint8_t mode = src[4];
        size_t orig_len = (size_t)read_u32_le(src + 8);
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

        uint8_t qL_inv[4] = {src[12], src[13], src[14], src[15]};
        uint8_t qR_inv[4] = {src[16], src[17], src[18], src[19]};

        if (mode == 0) {
            /* Raw passthrough */
            if (src_len < PRZM_ISOQUANT_HEADER_SIZE || src_len - PRZM_ISOQUANT_HEADER_SIZE < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + PRZM_ISOQUANT_HEADER_SIZE, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            /* Collimated only */
            if (src_len < PRZM_ISOQUANT_HEADER_SIZE || src_len - PRZM_ISOQUANT_HEADER_SIZE < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + PRZM_ISOQUANT_HEADER_SIZE, orig_len);
            przm_isoquant_unrotate_inplace(dst, orig_len, qL_inv, qR_inv);
            return (int64_t)orig_len;
        } else if (mode == 3) {
            /* Cascaded PWS */
            int64_t dec = pws_decode(src + PRZM_ISOQUANT_HEADER_SIZE, src_len - PRZM_ISOQUANT_HEADER_SIZE, dst, dst_cap);
            if (dec < 0) return dec;
            przm_isoquant_unrotate_inplace(dst, (size_t)dec, qL_inv, qR_inv);
            return dec;
        }
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    /* 1.6. Discrete Heisenberg Group H_3(F_{2^8}) Foliation Frame ('HEIS', 0x53494548) */
    if (magic == PRZM_MAGIC_HEISENBERG) {
        if (src_len < PRZM_HEISENBERG_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        size_t orig_len = (size_t)read_u32_le(src + 4);
        if (orig_len == 0) return 0;
        uint8_t stride = src[8];
        uint8_t mode = src[9];

        if (stride != 3 && stride != 4) return PRZM_ERR_CORRUPTED_FRAME;
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

        if (mode == 0) {
            /* Raw passthrough */
            if (src_len < PRZM_HEISENBERG_HEADER_SIZE || src_len - PRZM_HEISENBERG_HEADER_SIZE < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + PRZM_HEISENBERG_HEADER_SIZE, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            /* Foliated uncompressed */
            if (src_len < PRZM_HEISENBERG_HEADER_SIZE || src_len - PRZM_HEISENBERG_HEADER_SIZE < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + PRZM_HEISENBERG_HEADER_SIZE, orig_len);
            int err = przm_heisenberg_unfoliate_inplace(dst, orig_len, stride);
            if (err != PRZM_OK) return err;
            return (int64_t)orig_len;
        } else if (mode == 3) {
            /* Cascaded PWS or nested PRZM compression */
            const uint8_t* payload = src + PRZM_HEISENBERG_HEADER_SIZE;
            size_t payload_len = src_len - PRZM_HEISENBERG_HEADER_SIZE;
            int64_t dec = przm_decompress(payload, payload_len, dst, dst_cap);
            if (dec < 0) {
                dec = pws_decode(payload, payload_len, dst, dst_cap);
            }
            if (dec < 0) return dec;
            int err = przm_heisenberg_unfoliate_inplace(dst, (size_t)dec, stride);
            if (err != PRZM_OK) return err;
            return dec;
        }
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    /* 1.7. Cellular Sheaves with Discrete Hodge Laplacians Frame ('CSHD', 0x44485343) */
    if (magic == PRZM_MAGIC_CELLULAR_SHEAF) {
        return przm_cellular_sheaf_decompress(src, src_len, dst, dst_cap);
    }

    /* 2. 2-Adic Mahler Frame */
    if (magic == PRZM_MAGIC_MAHLER) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        uint8_t mode = src[4];
        size_t orig_len = (size_t)read_u32_le(src + 8);
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

        if (mode == 0) {
            if (src_len < 12 || src_len - 12 < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            if (src_len < 12 || src_len - 12 < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            size_t limit = (orig_len / 16) * 16;
            for (size_t i = 0; i < limit; i += 16) {
                mahler16_inverse(dst + i);
            }
            return (int64_t)orig_len;
        } else if (mode == 3) {
            int64_t dec = pws_decode(src + 12, src_len - 12, dst, dst_cap);
            if (dec < 0) return dec;
            size_t limit = ((size_t)dec / 16) * 16;
            for (size_t i = 0; i < limit; i += 16) {
                mahler16_inverse(dst + i);
            }
            return dec;
        }
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    /* 3. ARM64 Instruction Branch Collimation Frame */
    if (magic == PRZM_MAGIC_ARM64) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        uint8_t mode = src[4];
        size_t orig_len = (size_t)read_u32_le(src + 8);
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

        if (mode == 0) {
            if (src_len < 12 || src_len - 12 < orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            /* Sheaf cascaded */
            int64_t dec = przm_decompress(src + 12, src_len - 12, dst, dst_cap);
            if (dec < 0) return dec;
            arm64_filter_inverse(dst, (size_t)dec);
            return dec;
        }
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    /* 4. Étale Sheaf Trellis Frame ('SHEF', 0x53484546) */
    if (magic == PRZM_MAGIC_SHEAF) {
        return decompress_sheaf(src, src_len, dst, dst_cap);
    }

    /* 5. Pareto-Wave Synthesis Frame ('PW\x01') */
    if (src[0] == PRZM_MAGIC_PWS_0 && src[1] == PRZM_MAGIC_PWS_1 && src[2] == PRZM_VERSION_1) {
        return pws_decode(src, src_len, dst, dst_cap);
    }

    /* 6. Standard Fused Frame ('PRZM') */
    if (magic == PRZM_MAGIC_STANDARD) {
        return decompress_standard_fused(src, src_len, dst, dst_cap, NULL);
    }

    /* 7. Grammar-Graph Tensor Compression (GGTC) Frame ('GG\x01') */
    if (src[0] == PRZM_MAGIC_GGTC_0 && src[1] == PRZM_MAGIC_GGTC_1 && src[2] == PRZM_VERSION_1) {
        return przm_ggtc_decompress(src, src_len, dst, dst_cap);
    }

    /* Zero-Schema VM is a Go-native format; return PRZM_ERR_UNSUPPORTED_MODE in pure C */
    if (src[0] == PRZM_MAGIC_VM_0 && src[1] == PRZM_MAGIC_VM_1 && src[2] == PRZM_VERSION_1) {
        return PRZM_ERR_UNSUPPORTED_MODE;
    }

    return PRZM_ERR_INVALID_MAGIC;
}

/* ============================================================================
 * 16. Public API Implementation: przm_decompress_dict
 * ============================================================================ */

int64_t przm_decompress_dict(const uint8_t* src, size_t src_len,
                             uint8_t* dst, size_t dst_cap,
                             const przm_dictionary_t* dict) {
    if (src_len == 0) return 0;
    if (src == NULL || dst == NULL || src_len < 4) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t magic = read_u32_le(src);
    if (magic == PRZM_MAGIC_STREAM) {
        if (src_len < PRZM_STREAM_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;
        int64_t total_u_size = (int64_t)read_u64_le(src + 5);
        if (total_u_size < 0) return PRZM_ERR_CORRUPTED_FRAME;
        if (dst_cap < (size_t)total_u_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;
        uint32_t chunk_count = read_u32_le(src + 13);

        size_t offset = PRZM_STREAM_HEADER_SIZE;
        size_t dst_pos = 0;

        for (uint32_t i = 0; i < chunk_count; i++) {
            if (offset + 4 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
            uint32_t chunk_comp_size = read_u32_le(src + offset);
            offset += 4;
            if (offset + chunk_comp_size > src_len) return PRZM_ERR_CORRUPTED_FRAME;

            const uint8_t* chunk_ptr = src + offset;
            offset += chunk_comp_size;

            int64_t chunk_u_size = przm_decompress_dict(chunk_ptr, (size_t)chunk_comp_size, dst + dst_pos, dst_cap - dst_pos, dict);
            if (chunk_u_size < 0) return chunk_u_size;

            dst_pos += (size_t)chunk_u_size;
            if (dst_pos > (size_t)total_u_size) return PRZM_ERR_CORRUPTED_FRAME;
        }

        if (dst_pos != (size_t)total_u_size) return PRZM_ERR_CORRUPTED_FRAME;
        return total_u_size;
    }

    if (magic == PRZM_MAGIC_STANDARD) {
        return decompress_standard_fused(src, src_len, dst, dst_cap, dict);
    }

    /* Non-standard frames fall through to standard przm_decompress */
    return przm_decompress(src, src_len, dst, dst_cap);
}
