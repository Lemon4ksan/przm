#if defined(__GNUC__) || defined(__clang__)
#pragma GCC target("ssse3,popcnt")
#endif

#include <stdint.h>
#include <immintrin.h>

typedef struct {
    const uint8_t *src;
    int64_t n;
    const uint8_t *table; // 256 bytes classification table
    uint8_t *s_buf;
    uint8_t *c_buf;
    uint8_t *mask_buf;
    const uint64_t *lut_pack_s;
    const uint64_t *lut_pack_c;
    int64_t s_len;
    int64_t c_len;
} SheafSplitArgs;

typedef struct {
    uint8_t *dst;
    const uint8_t *s;
    const uint8_t *c;
    const uint8_t *mask;
    int64_t orig_len;
    const uint64_t *lut_shuf_s;
    const uint64_t *lut_shuf_c;
} SheafInterleaveArgs;

void sheaf_simd_split(SheafSplitArgs *args) {
    const uint8_t *src = args->src;
    int64_t n = args->n;
    const uint8_t *table = args->table;

    uint8_t *s_buf = args->s_buf;
    uint8_t *c_buf = args->c_buf;
    uint8_t *mask_buf = args->mask_buf;
    const uint64_t *lut_pack_s = args->lut_pack_s;
    const uint64_t *lut_pack_c = args->lut_pack_c;

    int64_t s_pos = 0;
    int64_t c_pos = 0;
    int64_t m_pos = 0;
    int64_t i = 0;

    for (; i + 16 <= n; i += 16) {
        uint64_t src_val0 = *(const uint64_t*)(src + i);
        uint64_t src_val1 = *(const uint64_t*)(src + i + 8);

        uint32_t m0 = ((uint32_t)table[src[i]])       |
                      (((uint32_t)table[src[i+1]]) << 1) |
                      (((uint32_t)table[src[i+2]]) << 2) |
                      (((uint32_t)table[src[i+3]]) << 3) |
                      (((uint32_t)table[src[i+4]]) << 4) |
                      (((uint32_t)table[src[i+5]]) << 5) |
                      (((uint32_t)table[src[i+6]]) << 6) |
                      (((uint32_t)table[src[i+7]]) << 7);

        uint32_t m1 = ((uint32_t)table[src[i+8]])       |
                      (((uint32_t)table[src[i+9]]) << 1) |
                      (((uint32_t)table[src[i+10]]) << 2) |
                      (((uint32_t)table[src[i+11]]) << 3) |
                      (((uint32_t)table[src[i+12]]) << 4) |
                      (((uint32_t)table[src[i+13]]) << 5) |
                      (((uint32_t)table[src[i+14]]) << 6) |
                      (((uint32_t)table[src[i+15]]) << 7);

        mask_buf[m_pos] = (uint8_t)m0;
        mask_buf[m_pos + 1] = (uint8_t)m1;
        m_pos += 2;

        __m128i v_src0 = _mm_cvtsi64_si128(src_val0);
        __m128i v_shuf_s0 = _mm_cvtsi64_si128(lut_pack_s[m0]);
        __m128i v_shuf_c0 = _mm_cvtsi64_si128(lut_pack_c[m0]);
        __m128i packed_s0 = _mm_shuffle_epi8(v_src0, v_shuf_s0);
        __m128i packed_c0 = _mm_shuffle_epi8(v_src0, v_shuf_c0);

        *(uint64_t*)(s_buf + s_pos) = _mm_cvtsi128_si64(packed_s0);
        *(uint64_t*)(c_buf + c_pos) = _mm_cvtsi128_si64(packed_c0);

        int k0 = __builtin_popcount(m0);
        s_pos += k0;
        c_pos += 8 - k0;

        __m128i v_src1 = _mm_cvtsi64_si128(src_val1);
        __m128i v_shuf_s1 = _mm_cvtsi64_si128(lut_pack_s[m1]);
        __m128i v_shuf_c1 = _mm_cvtsi64_si128(lut_pack_c[m1]);
        __m128i packed_s1 = _mm_shuffle_epi8(v_src1, v_shuf_s1);
        __m128i packed_c1 = _mm_shuffle_epi8(v_src1, v_shuf_c1);

        *(uint64_t*)(s_buf + s_pos) = _mm_cvtsi128_si64(packed_s1);
        *(uint64_t*)(c_buf + c_pos) = _mm_cvtsi128_si64(packed_c1);

        int k1 = __builtin_popcount(m1);
        s_pos += k1;
        c_pos += 8 - k1;
    }

    for (; i + 8 <= n; i += 8) {
        uint64_t src_val = *(const uint64_t*)(src + i);
        __m128i v_src = _mm_cvtsi64_si128(src_val);

        uint32_t m = ((uint32_t)table[src[i]])       |
                     (((uint32_t)table[src[i+1]]) << 1) |
                     (((uint32_t)table[src[i+2]]) << 2) |
                     (((uint32_t)table[src[i+3]]) << 3) |
                     (((uint32_t)table[src[i+4]]) << 4) |
                     (((uint32_t)table[src[i+5]]) << 5) |
                     (((uint32_t)table[src[i+6]]) << 6) |
                     (((uint32_t)table[src[i+7]]) << 7);
        mask_buf[m_pos++] = (uint8_t)m;

        __m128i v_shuf_s = _mm_cvtsi64_si128(lut_pack_s[m]);
        __m128i v_shuf_c = _mm_cvtsi64_si128(lut_pack_c[m]);

        __m128i packed_s = _mm_shuffle_epi8(v_src, v_shuf_s);
        __m128i packed_c = _mm_shuffle_epi8(v_src, v_shuf_c);

        *(uint64_t*)(s_buf + s_pos) = _mm_cvtsi128_si64(packed_s);
        *(uint64_t*)(c_buf + c_pos) = _mm_cvtsi128_si64(packed_c);

        int k = __builtin_popcount(m);
        s_pos += k;
        c_pos += 8 - k;
    }

    if (i < n) {
        uint8_t m = 0;
        int bit = 0;
        for (; i < n; i++) {
            uint8_t b = src[i];
            uint8_t s = table[b];
            m |= (s << bit);
            bit++;
            s_buf[s_pos] = b;
            s_pos += s;
            c_buf[c_pos] = b;
            c_pos += (1 - s);
        }
        mask_buf[m_pos++] = m;
    }

    args->s_len = s_pos;
    args->c_len = c_pos;
}

void sheaf_simd_interleave(SheafInterleaveArgs *args) {
    uint8_t *dst = args->dst;
    const uint8_t *s = args->s;
    const uint8_t *c = args->c;
    const uint8_t *mask = args->mask;
    int64_t orig_len = args->orig_len;
    const uint64_t *lut_shuf_s = args->lut_shuf_s;
    const uint64_t *lut_shuf_c = args->lut_shuf_c;

    int64_t s_pos = 0;
    int64_t c_pos = 0;
    int64_t dst_pos = 0;
    int64_t m_pos = 0;

    for (; dst_pos + 16 <= orig_len; dst_pos += 16) {
        uint32_t m0 = mask[m_pos];
        uint32_t m1 = mask[m_pos + 1];
        m_pos += 2;

        uint64_t s_val0 = *(const uint64_t*)(s + s_pos);
        uint64_t c_val0 = *(const uint64_t*)(c + c_pos);
        __m128i v_s0 = _mm_cvtsi64_si128(s_val0);
        __m128i v_c0 = _mm_cvtsi64_si128(c_val0);
        __m128i v_shuf_s0 = _mm_cvtsi64_si128(lut_shuf_s[m0]);
        __m128i v_shuf_c0 = _mm_cvtsi64_si128(lut_shuf_c[m0]);
        __m128i res_s0 = _mm_shuffle_epi8(v_s0, v_shuf_s0);
        __m128i res_c0 = _mm_shuffle_epi8(v_c0, v_shuf_c0);
        __m128i res0 = _mm_or_si128(res_s0, res_c0);
        *(uint64_t*)(dst + dst_pos) = _mm_cvtsi128_si64(res0);

        int k0 = __builtin_popcount(m0);
        s_pos += k0;
        c_pos += 8 - k0;

        uint64_t s_val1 = *(const uint64_t*)(s + s_pos);
        uint64_t c_val1 = *(const uint64_t*)(c + c_pos);
        __m128i v_s1 = _mm_cvtsi64_si128(s_val1);
        __m128i v_c1 = _mm_cvtsi64_si128(c_val1);
        __m128i v_shuf_s1 = _mm_cvtsi64_si128(lut_shuf_s[m1]);
        __m128i v_shuf_c1 = _mm_cvtsi64_si128(lut_shuf_c[m1]);
        __m128i res_s1 = _mm_shuffle_epi8(v_s1, v_shuf_s1);
        __m128i res_c1 = _mm_shuffle_epi8(v_c1, v_shuf_c1);
        __m128i res1 = _mm_or_si128(res_s1, res_c1);
        *(uint64_t*)(dst + dst_pos + 8) = _mm_cvtsi128_si64(res1);

        int k1 = __builtin_popcount(m1);
        s_pos += k1;
        c_pos += 8 - k1;
    }

    for (; dst_pos + 8 <= orig_len; dst_pos += 8) {
        uint32_t m = mask[m_pos++];

        uint64_t s_val = *(const uint64_t*)(s + s_pos);
        uint64_t c_val = *(const uint64_t*)(c + c_pos);

        __m128i v_s = _mm_cvtsi64_si128(s_val);
        __m128i v_c = _mm_cvtsi64_si128(c_val);

        __m128i v_shuf_s = _mm_cvtsi64_si128(lut_shuf_s[m]);
        __m128i v_shuf_c = _mm_cvtsi64_si128(lut_shuf_c[m]);

        __m128i res_s = _mm_shuffle_epi8(v_s, v_shuf_s);
        __m128i res_c = _mm_shuffle_epi8(v_c, v_shuf_c);
        __m128i res = _mm_or_si128(res_s, res_c);

        *(uint64_t*)(dst + dst_pos) = _mm_cvtsi128_si64(res);

        int k = __builtin_popcount(m);
        s_pos += k;
        c_pos += 8 - k;
    }

    if (dst_pos < orig_len) {
        uint8_t m = mask[m_pos];
        int bit = 0;
        for (; dst_pos < orig_len; dst_pos++) {
            int bs = (m >> bit) & 1;
            bit++;
            if (bs) {
                dst[dst_pos] = s[s_pos++];
            } else {
                dst[dst_pos] = c[c_pos++];
            }
        }
    }
}
