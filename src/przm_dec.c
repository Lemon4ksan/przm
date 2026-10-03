/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 *
 * PRZM-SPEC-0.5 Pure C Zero-Allocation Decompressor Implementation.
 */

#include "przm_dec.h"

#include <stdbool.h>
#include <string.h>

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
#define PRZM_TLS_SCRATCH_SIZE (4 * 1024 * 1024) /* 4 MiB scratchpads */

static PRZM_TLS uint8_t tls_lits_scratch[PRZM_TLS_SCRATCH_SIZE];
static PRZM_TLS uint8_t tls_split_scratch[PRZM_TLS_SCRATCH_SIZE];

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
    uint64_t val = (b0 & 0x7F) | ((uint64_t)(b1 & 0x7F) << 7) | ((uint64_t)(b2 & 0x7F) << 14);
    int shift = 21;
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
 * 4. Built-in Schema Dictionaries (DictID 1: JSON, DictID 2: Code)
 * ============================================================================ */

static const char* const g_dict_json_tokens[64] = {
    /* 0..7: Top 8 Direct 1-byte Opcode Tokens (0xD8 .. 0xDF) */
    ",\"status\":",
    ",\"level\":\"INFO\"",
    ",\"method\":\"",
    ",\"uri\":\"",
    ",\"duration_ms\":",
    ",\"bytes_sent\":",
    ",\"client_ip\":\"",
    "{\"ts\":",

    /* 8..63: Extended 2-byte Opcode Tokens (0xF2 <tokenID>) */
    ",\"status\":200",
    ",\"status\":201",
    ",\"status\":204",
    ",\"status\":400",
    ",\"status\":401",
    ",\"status\":404",
    ",\"status\":500",
    ",\"method\":\"GET\"",
    ",\"method\":\"POST\"",
    ",\"method\":\"PUT\"",
    ",\"method\":\"DELETE\"",
    ",\"client_ip\":\"192.168.1.",
    ",\"uri\":\"/api/v1/",
    "\"/api/v1/auth/login\"",
    "\"/api/v1/users/profile\"",
    "\"/api/v1/inventory/items\"",
    "\"/api/v1/billing/checkout\"",
    "\"/healthz\"",
    "\"/metrics\"",
    ",\"trace_id\":\"e4d8",
    ",\"trace_id\":\"",
    "{\"id\":",
    ",\"id\":",
    ",\"user_id\":",
    ",\"name\":\"",
    ",\"type\":\"",
    ",\"message\":\"",
    ",\"error\":\"",
    ",\"timestamp\":",
    ",\"created_at\":\"",
    ",\"updated_at\":\"",
    ",\"description\":\"",
    ",\"action\":\"",
    ",\"event\":\"",
    "{\"event\":\"",
    "{\"data\":",
    ",\"data\":",
    ",\"count\":",
    ",\"value\":",
    ",\"code\":",
    ",\"metric\":\"",
    ",\"sensor_id\":\"",
    ",\"resp\":200",
    ",\"status\":\"ok\"",
    "true,",
    "false,",
    "null,",
    ",\"ts\":",
    ",\"level\":\"WARN\"",
    ",\"level\":\"ERROR\"",
    ",\"level\":\"DEBUG\"",
    ",\"action\":\"scroll\"",
    ",\"action\":\"click\"",
    ",\"sensor_id\":\"temp-",
    ",\"metric\":\"temperature\""
};

PRZM_INLINE const char* get_schema_token(uint32_t dict_id, uint8_t token_idx,
                                         const przm_dictionary_t* ext_dict,
                                         size_t* out_len) {
    if (dict_id == PRZM_DICT_ID_JSON) {
        if (token_idx < (uint8_t)(sizeof(g_dict_json_tokens) / sizeof(g_dict_json_tokens[0]))) {
            const char* str = g_dict_json_tokens[token_idx];
            *out_len = strlen(str);
            return str;
        }
    } else if (ext_dict != NULL && ext_dict->dict_id == dict_id) {
        if (token_idx < ext_dict->num_tokens) {
            *out_len = ext_dict->tokens[token_idx].len;
            return ext_dict->tokens[token_idx].str;
        }
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

static int vans_decode4x(const uint8_t* src, size_t src_len, uint8_t* dst, size_t orig_len) {
    if (src_len < 8) return PRZM_ERR_CORRUPTED_FRAME;
    if (src[0] != PRZM_MAGIC_VANS_0 || src[1] != PRZM_MAGIC_VANS_1 || src[2] != PRZM_VERSION_1) {
        return PRZM_ERR_CORRUPTED_FRAME;
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

    /* 32-bit Specialized signatures */
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
 * 14. Standard Fused Frame Decoder Engine ('PRZM', 0x4D5A5250)
 * ============================================================================ */

PRZM_INLINE bool przm_check_cap(size_t alloc_size, size_t dst_pos, size_t len) {
    size_t limit = alloc_size + PRZM_SAFETY_PADDING;
    return (dst_pos <= limit) && (len <= limit - dst_pos);
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
    if ((flags & PRZM_FLAG_BCJ_SPLIT) != 0) {
        if (offset + 4 > src_len) return PRZM_ERR_CORRUPTED_FRAME;
        call_count = read_u32_le(src + offset);
        offset += 4;
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

    /* Initialize Ring-16 locality queue */
    int q[16] = {1, stride, 2 * stride, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384};

    size_t cmd_ptr = sec2_offset;
    size_t cmd_end = sec2_offset + cmd_size;
    size_t off_ptr = sec3_offset;
    size_t lit_ptr = 0;
    size_t dst_pos = 0;

    bool is_split = (flags & PRZM_FLAG_BCJ_SPLIT) != 0;
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
    } else {
        if (dst_cap < u_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;
    }

    /* Command Stream Execution Loop */
    while (dst_pos < alloc_size && cmd_ptr < cmd_end) {
        uint8_t op = src[cmd_ptr++];

        if (op < 0x80) {
            /* Fast-Path 1: Fused Repeat Token (0 LLL MM KK) */
            size_t lit_len = (op >> 4) & 0x07;
            size_t m_len = ((op >> 2) & 0x03) + 4;
            int k = (int)(op & 0x03);

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = q[k];
            for (int j = k; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

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
            size_t m_len = base_len + 4;
            if (base_len == 15) {
                int extra = 0;
                int err = fast_read_varint(src, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 19 + (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = 0;
            int err = fast_read_varint(src, sec4_offset, &off_ptr, &d);
            if (err != PRZM_OK || d <= 0) return PRZM_ERR_CORRUPTED_FRAME;

            for (int j = 15; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        /* Specialized Opcodes (0xC0 .. 0xFF) */
        if (op >= 0xC0 && op <= 0xCF) {
            /* Long Repeat Match */
            int k = (int)(op & 0x0F);
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t token = src[cmd_ptr++];
            size_t lit_len = token >> 4;
            size_t base_len = token & 0x0F;
            size_t m_len = base_len + 4;
            if (base_len == 15) {
                int extra = 0;
                int err = fast_read_varint(src, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 19 + (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = q[k];
            for (int j = k; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            if (!przm_check_cap(alloc_size, dst_pos, m_len)) return PRZM_ERR_CORRUPTED_FRAME;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op >= 0xD0 && op <= 0xD7) {
            /* Stride Match (m 1..8) */
            int m = (int)(op & 0x07) + 1;
            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            uint8_t token = src[cmd_ptr++];
            size_t lit_len = (size_t)(token >> 4);
            size_t m_len = (size_t)(token & 0x0F) + 4;

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                if (!przm_check_cap(alloc_size, dst_pos, lit_len)) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = m * stride;
            for (int j = 15; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

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
            int d = q[k];
            for (int j = k; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            size_t base_byte = src[cmd_ptr++];
            size_t m_len = base_byte + 4;
            if (base_byte == 255) {
                int extra = 0;
                int err = fast_read_varint(src, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 259 + (size_t)extra;
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
            int d = 0;
            int err = fast_read_varint(src, sec4_offset, &off_ptr, &d);
            if (err != PRZM_OK || d <= 0) return PRZM_ERR_CORRUPTED_FRAME;

            for (int j = 15; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (cmd_ptr >= cmd_end) return PRZM_ERR_CORRUPTED_FRAME;
            size_t base_byte = src[cmd_ptr++];
            size_t m_len = base_byte + 4;
            if (base_byte == 255) {
                int extra = 0;
                err = fast_read_varint(src, cmd_end, &cmd_ptr, &extra);
                if (err != PRZM_OK || extra < 0) return PRZM_ERR_CORRUPTED_FRAME;
                m_len = 259 + (size_t)extra;
            }

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
            int err = fast_read_varint(src, cmd_end, &cmd_ptr, &run_len);
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
            int err = fast_read_varint(src, cmd_end, &cmd_ptr, &rle_len);
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
            uint8_t tok_id = src[cmd_ptr++];
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, ext_dict, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            if (!przm_check_cap(alloc_size, dst_pos, t_len)) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
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
    }

    if (dst_pos < alloc_size) return PRZM_ERR_CORRUPTED_FRAME;

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
    } else if ((flags & PRZM_FLAG_BCJ) != 0) {
        x86_bcj_inverse(dst, u_size);
    }

    return (int64_t)u_size;
}

/* ============================================================================
 * 15. Public API Implementation: przm_decompress
 * ============================================================================ */

int64_t przm_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len == 0) return 0;
    if (src == NULL || dst == NULL || src_len < 4) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t magic = read_u32_le(src);

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

    /* Zero-Schema VM and GGTC are Go-native formats; return PRZM_ERR_UNSUPPORTED_MODE in pure C */
    if ((src[0] == PRZM_MAGIC_VM_0 && src[1] == PRZM_MAGIC_VM_1 && src[2] == PRZM_VERSION_1) ||
        (src[0] == PRZM_MAGIC_GGTC_0 && src[1] == PRZM_MAGIC_GGTC_1 && src[2] == PRZM_VERSION_1)) {
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
    if (magic == PRZM_MAGIC_STANDARD) {
        return decompress_standard_fused(src, src_len, dst, dst_cap, dict);
    }

    /* Non-standard frames fall through to standard przm_decompress */
    return przm_decompress(src, src_len, dst, dst_cap);
}
