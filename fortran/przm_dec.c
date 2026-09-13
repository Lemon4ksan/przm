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
        val |= (uint64_t)(b & 0x7F) << shift;
        if ((b & 0x80) == 0) {
            *out_val = (int)val;
            return PRZM_OK;
        }
        shift += 7;
        if (shift > 63) return PRZM_ERR_CORRUPTED_FRAME;
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

static const char* const g_dict_code_tokens[42] = {
    /* 0..7: Top 8 Direct 1-byte Opcode Tokens (0xD8 .. 0xDF) */
    "if err != nil {\n",
    "return nil, err",
    "func (",
    "package ",
    "import (\n",
    "return nil",
    "return err",
    "for i := 0; i < ",

    /* 8..41: Extended 2-byte Opcode Tokens (0xF2 <tokenID>) */
    "context.Context",
    "binary.LittleEndian.",
    "fmt.Sprintf(",
    "fmt.Errorf(",
    "type ",
    "struct {\n",
    "interface {\n",
    "[]byte",
    "string",
    "error",
    "make([]",
    "append(",
    "switch ",
    "default:\n",
    "continue\n",
    "const (\n",
    "var (\n",
    "sync.Mutex",
    "sync.Pool",
    "func (s *",
    "len(",
    "return ",
    "case ",
    "uint64",
    "uint32",
    "int64",
    "float64",
    "bool",
    "true",
    "false",
    "nil",
    "bytes.Buffer"
};

PRZM_INLINE const char* get_schema_token(uint32_t dict_id, uint8_t token_idx, size_t* out_len) {
    if (dict_id == 1 && token_idx < (uint8_t)(sizeof(g_dict_json_tokens) / sizeof(g_dict_json_tokens[0]))) {
        const char* str = g_dict_json_tokens[token_idx];
        *out_len = strlen(str);
        return str;
    }
    if (dict_id == 2 && token_idx < (uint8_t)(sizeof(g_dict_code_tokens) / sizeof(g_dict_code_tokens[0]))) {
        const char* str = g_dict_code_tokens[token_idx];
        *out_len = strlen(str);
        return str;
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

    if (num_lits != orig_len || low_bytes != expected_low || 8 + low_bytes > src_len) {
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
        if (p + run_len > num_lits) return PRZM_ERR_CORRUPTED_FRAME;
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
            if (id >= num_l5) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst + dst_pos, g.exp_l5[id], 32);
            dst_pos += 32;
            continue;
        }
        if (op >= 0xEC && op <= 0xF3) {
            size_t id = op - 0xEC;
            if (id >= num_l4) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst + dst_pos, g.exp_l4[id], 16);
            dst_pos += 16;
            continue;
        }
        if (op >= 0xE0 && op <= 0xEB) {
            size_t id = op - 0xE0;
            if (id >= num_l3) return PRZM_ERR_CORRUPTED_FRAME;
            write_u64_le(dst + dst_pos, g.exp_l3[id]);
            dst_pos += 8;
            continue;
        }
        if (op >= 0xD0 && op <= 0xDF) {
            size_t id = op - 0xD0;
            if (id >= num_l2) return PRZM_ERR_CORRUPTED_FRAME;
            write_u32_le(dst + dst_pos, g.exp_l2[id]);
            dst_pos += 4;
            continue;
        }
        if (op >= 0xC0 && op <= 0xCF) {
            size_t id = op - 0xC0;
            if (id >= num_l1) return PRZM_ERR_CORRUPTED_FRAME;
            write_u16_le(dst + dst_pos, g.exp_l1[id]);
            dst_pos += 2;
            continue;
        }
        if (op == 0xFF) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            dst[dst_pos++] = ax_stream[ax_ptr++];
            continue;
        }
        if (op == 0xFC) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            size_t id = ax_stream[ax_ptr++];
            if (id >= num_l1) return PRZM_ERR_CORRUPTED_FRAME;
            write_u16_le(dst + dst_pos, g.exp_l1[id]);
            dst_pos += 2;
            continue;
        }
        if (op == 0xFD) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            size_t id = ax_stream[ax_ptr++];
            if (id >= num_l2) return PRZM_ERR_CORRUPTED_FRAME;
            write_u32_le(dst + dst_pos, g.exp_l2[id]);
            dst_pos += 4;
            continue;
        }
        if (op == 0xFE) {
            if (ax_ptr >= ax_len) return PRZM_ERR_CORRUPTED_FRAME;
            size_t id = ax_stream[ax_ptr++];
            if (id < 32) {
                if (id >= num_l3) return PRZM_ERR_CORRUPTED_FRAME;
                write_u64_le(dst + dst_pos, g.exp_l3[id]);
                dst_pos += 8;
            } else if (id < 48) {
                size_t r = id - 32;
                if (r >= num_l4) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(dst + dst_pos, g.exp_l4[r], 16);
                dst_pos += 16;
            } else {
                size_t r = id - 48;
                if (r >= num_l5) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(dst + dst_pos, g.exp_l5[r], 32);
                dst_pos += 32;
            }
            continue;
        }
    }

    if (dst_pos != orig_size) return PRZM_ERR_CORRUPTED_FRAME;

    if (stride >= 2) {
        if (orig_size <= PRZM_TLS_SCRATCH_SIZE) {
            memcpy(tls_split_scratch, dst, orig_size);
            untranspose_stride(tls_split_scratch, dst, orig_size, stride);
        } else {
            /* In-place untranspose fallback */
            uint8_t* tmp = (uint8_t*)dst + orig_size;
            memcpy(tmp, dst, orig_size);
            untranspose_stride(tmp, dst, orig_size, stride);
        }
    }

    return (int64_t)orig_size;
}

/* ============================================================================
 * 13. Public API Implementation: przm_get_uncompressed_size
 * ============================================================================ */

int64_t przm_get_uncompressed_size(const uint8_t* src, size_t src_len) {
    if (src == NULL || src_len < 4) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t magic = read_u32_le(src);

    /* 32-bit Specialized signatures */
    if (magic == PRZM_MAGIC_SYMPLECTIC || magic == PRZM_MAGIC_ARM64 ||
        magic == PRZM_MAGIC_SHEAF || magic == PRZM_MAGIC_MAHLER) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        return (int64_t)read_u32_le(src + 8);
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

static int64_t decompress_standard_fused(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap) {
    if (src_len < PRZM_HEADER_SIZE) return PRZM_ERR_CORRUPTED_FRAME;

    uint8_t flags = src[4];
    size_t u_size = (size_t)read_u24_le(src + 5);

    if (u_size > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
    if (dst_cap < u_size + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

    /* Raw uncompressed fast path */
    if ((flags & PRZM_FLAG_UNCOMPRESSED) != 0) {
        if (src_len < PRZM_HEADER_SIZE + u_size) return PRZM_ERR_CORRUPTED_FRAME;
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

    if (sec4_offset > src_len) return PRZM_ERR_CORRUPTED_FRAME;
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
        alloc_size = 4 + 4 * (size_t)call_count + u_size;
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
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = q[k];
            for (int j = k; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
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
                if (err != PRZM_OK) return err;
                m_len = 19 + (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
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
                if (err != PRZM_OK) return err;
                m_len = 19 + (size_t)extra;
            }

            if (lit_len > 0) {
                if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = q[k];
            for (int j = k; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
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
                memcpy(decode_dst + dst_pos, lits + lit_ptr, lit_len);
                lit_ptr += lit_len;
                dst_pos += lit_len;
            }

            int d = m * stride;
            for (int j = 15; j > 0; j--) q[j] = q[j - 1];
            q[0] = d;

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
            copy_match(decode_dst, dst_pos, dst_pos - d, m_len, d);
            dst_pos += m_len;
            continue;
        }

        if (op >= 0xD8 && op <= 0xDF) {
            /* Direct 1-byte schema token */
            uint8_t tok_id = (uint8_t)(op - 0xD8);
            size_t t_len = 0;
            const char* str = get_schema_token(dict_id, tok_id, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
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
                if (err != PRZM_OK) return err;
                m_len = 259 + (size_t)extra;
            }

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
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
                if (err != PRZM_OK) return err;
                m_len = 259 + (size_t)extra;
            }

            if (dst_pos < (size_t)d) return PRZM_ERR_OFFSET_OOB;
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
            const char* str = get_schema_token(dict_id, tok_id, &t_len);
            if (!str || t_len == 0) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(decode_dst + dst_pos, str, t_len);
            dst_pos += t_len;
            continue;
        }

        if (op >= 0xF3 && op <= 0xFA) {
            /* Direct 1-byte literal run */
            size_t lit_len = (size_t)(op - 0xF3 + 1);
            if (lit_ptr + lit_len > lits_len) return PRZM_ERR_CORRUPTED_FRAME;
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
    if (src == NULL || dst == NULL || src_len < 4) return PRZM_ERR_CORRUPTED_FRAME;

    uint32_t magic = read_u32_le(src);

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
            if (src_len < 12 + orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            /* Shear only */
            if (src_len < 12 + orig_len) return PRZM_ERR_CORRUPTED_FRAME;
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

    /* 2. 2-Adic Mahler Frame */
    if (magic == PRZM_MAGIC_MAHLER) {
        if (src_len < 12) return PRZM_ERR_CORRUPTED_FRAME;
        uint8_t mode = src[4];
        size_t orig_len = (size_t)read_u32_le(src + 8);
        if (orig_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;
        if (dst_cap < orig_len + PRZM_SAFETY_PADDING) return PRZM_ERR_BUFFER_TOO_SMALL;

        if (mode == 0) {
            if (src_len < 12 + orig_len) return PRZM_ERR_CORRUPTED_FRAME;
            memcpy(dst, src + 12, orig_len);
            return (int64_t)orig_len;
        } else if (mode == 1) {
            if (src_len < 12 + orig_len) return PRZM_ERR_CORRUPTED_FRAME;
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
            if (src_len < 12 + orig_len) return PRZM_ERR_CORRUPTED_FRAME;
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

    /* 4. Pareto-Wave Synthesis Frame ('PW\x01') */
    if (src[0] == PRZM_MAGIC_PWS_0 && src[1] == PRZM_MAGIC_PWS_1 && src[2] == PRZM_VERSION_1) {
        return pws_decode(src, src_len, dst, dst_cap);
    }

    /* 5. Standard Fused Frame ('PRZM') */
    if (magic == PRZM_MAGIC_STANDARD) {
        return decompress_standard_fused(src, src_len, dst, dst_cap);
    }

    return PRZM_ERR_INVALID_MAGIC;
}
