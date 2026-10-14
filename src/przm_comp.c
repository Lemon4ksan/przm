/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "przm_comp.h"
#include "przm_dec.h"
#include "distance_tables.h"
#include <string.h>
#include <math.h>

#define MAX_BLOCK_HORIZON   65536
#define MAX_HASH_BUCKETS    65536
#define HASH_MULT_32        0x1E35A7BDU
#define HASH_MULT_64        0x9E3779B97F4A7C15ULL
#define MAX_SEARCH_DEPTH    8
#define MAX_MATCH_LEN       1024
#define INFINITY_COST       0x3FFFFFFFU

#define FLAG_STRIDE         0x01
#define FLAG_EXT_OFFSET     0x02
#define FLAG_UNCOMPRESSED   0x04
#define FLAG_DICT_REF       0x10
#define FLAG_MIN_MATCH_3    0x20
#define FLAG_NORM           0x40
#define FLAG_NIBBLE         0x80

typedef struct {
    uint16_t from;
    uint16_t length;
    uint32_t dist;
    uint8_t kind; /* 0: lit, 1: rep, 2: bracket, 3: stride, 4: explicit */
    uint8_t arg;  /* k for rep, b for bracket, m for stride */
} comp_edge_t;

typedef struct {
    uint32_t cost;
    uint32_t r[4];
    comp_edge_t edge;
} comp_node_t;

typedef struct {
    int32_t head3[MAX_HASH_BUCKETS];
    int32_t head4[MAX_HASH_BUCKETS];
    int32_t head8[MAX_HASH_BUCKETS];
    int32_t prev_pos[MAX_BLOCK_HORIZON];
    comp_node_t nodes[MAX_BLOCK_HORIZON + 1];
    uint32_t lit_cost[256];
    comp_edge_t edges_buf[MAX_BLOCK_HORIZON];
    uint8_t all_cmds[4 * 1024 * 1024];
    uint8_t all_lits[8 * 1024 * 1024];
    uint32_t all_offs[1024 * 1024];
    uint8_t s3_buf[2 * 1024 * 1024];
    uint8_t raw_offs[4 * 1024 * 1024];
} comp_scratch_t;

static comp_scratch_t g_comp;

static inline uint32_t read_u24(const uint8_t *p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16);
}

static inline uint32_t read_u32(const uint8_t *p) {
    return (uint32_t)p[0] | ((uint32_t)p[1] << 8) | ((uint32_t)p[2] << 16) | ((uint32_t)p[3] << 24);
}

#ifndef PRZM_MAX_BLOCK_SIZE
#define PRZM_MAX_BLOCK_SIZE 16777216U
#endif

static inline uint64_t read_u64(const uint8_t *p) {
    uint64_t v = 0;
    for (int i = 0; i < 8; i++) v |= ((uint64_t)p[i]) << (i * 8);
    return v;
}

static inline uint32_t hash3(const uint8_t *p) {
    return (read_u24(p) * HASH_MULT_32) >> 16;
}

static inline uint32_t hash4(const uint8_t *p) {
    return (read_u32(p) * HASH_MULT_32) >> 16;
}

static inline uint32_t hash8(const uint8_t *p) {
    return (uint32_t)((read_u64(p) * HASH_MULT_64) >> 48);
}

static inline int get_match_len(const uint8_t *src, size_t pos, size_t ref, int max_len) {
    int len = 0;
    while (len < max_len && src[pos + len] == src[ref + len]) {
        len++;
    }
    return len;
}

static int check_high_entropy(const uint8_t *src, size_t len) {
    size_t sample_len = (len > 512) ? 512 : len;
    if (sample_len < 64) return 0;

    uint8_t seen[256];
    memset(seen, 0, sizeof(seen));
    int unique = 0;
    for (size_t i = 0; i < sample_len; i++) {
        if (!seen[src[i]]) {
            seen[src[i]] = 1;
            unique++;
        }
    }
    int threshold = (sample_len >= 256) ? (256 * 88) / 100 : (int)(sample_len * 88) / 100;
    return unique > threshold;
}

static uint32_t detect_stride(const uint8_t *src, size_t len) {
    if (len < 128) return 1;
    static const uint32_t cands[] = {4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80, 96, 128};
    size_t sample_len = (len > 2048) ? 2048 : len;
    uint32_t best_stride = 1;
    size_t max_matches = 0;

    for (size_t k = 0; k < sizeof(cands) / sizeof(cands[0]); k++) {
        uint32_t s = cands[k];
        if (s >= sample_len) continue;
        size_t matches = 0;
        for (size_t i = s; i < sample_len; i++) {
            if (src[i] == src[i - s]) matches++;
        }
        if (matches > max_matches && matches > (sample_len * 20) / 100) {
            max_matches = matches;
            best_stride = s;
        }
    }
    return best_stride;
}

static inline uint32_t select_window_log(size_t u) {
    if (u <= 65536) return 16;
    if (u <= 1048576) return 20;
    return 23;
}

static void compute_literal_costs(const uint8_t *block, int b_size, uint32_t *lit_cost) {
    uint32_t freq[256];
    memset(freq, 0, sizeof(freq));
    for (int i = 0; i < b_size; i++) freq[block[i]]++;

    double inv_b = 1.0 / (double)b_size;
    for (int c = 0; c < 256; c++) {
        if (freq[c] == 0) {
            lit_cost[c] = 26;
            continue;
        }
        double p = (double)freq[c] * inv_b;
        int cost = (int)round(-2.0 * (log(p) / 0.6931471805599453));
        if (cost < 2) cost = 2;
        if (cost > 26) cost = 26;
        lit_cost[c] = (uint32_t)cost;
    }
}

static inline uint32_t cost_repeat(int L, int k, int b_min) {
    int base = 40;
    if (L <= 3 + b_min) base = 10;
    else if (L <= 14 + b_min) base = 20;
    else if (L <= 14 + b_min + 127) base = 30;

    static const int bonus[4] = {6, 3, 1, 0};
    int cost = base - bonus[k & 3];
    return (cost < 3) ? 3 : (uint32_t)cost;
}

static inline uint32_t cost_explicit(int L, uint32_t dist, int b_min) {
    int base = 36;
    if (L <= 14 + b_min) base = 16;
    else if (L <= 14 + b_min + 127) base = 26;

    uint8_t code = 0, nb = 0;
    uint32_t extra = 0;
    przm_distance_to_code(dist, &code, &extra, &nb);
    return (uint32_t)(base + 2 * (int)nb);
}

static inline void update_lru_rep(uint32_t *r, int k) {
    if (k == 1) {
        uint32_t t = r[0]; r[0] = r[1]; r[1] = t;
    } else if (k == 2) {
        uint32_t t = r[2]; r[2] = r[1]; r[1] = r[0]; r[0] = t;
    } else if (k == 3) {
        uint32_t t = r[3]; r[3] = r[2]; r[2] = r[1]; r[1] = r[0]; r[0] = t;
    }
}

static inline void update_lru_exp(uint32_t *r, uint32_t d) {
    r[3] = r[2]; r[2] = r[1]; r[1] = r[0]; r[0] = d;
}

static void relax_literal(int i, const uint8_t *block) {
    uint8_t b = block[i];
    uint32_t edge_cost = g_comp.lit_cost[b];
    uint32_t target_cost = g_comp.nodes[i].cost + edge_cost;
    comp_node_t *next = &g_comp.nodes[i + 1];

    if (target_cost < next->cost) {
        next->cost = target_cost;
        memcpy(next->r, g_comp.nodes[i].r, sizeof(next->r));
        next->edge.from = (uint16_t)i;
        next->edge.length = 1;
        next->edge.dist = 0;
        next->edge.kind = 0;
        next->edge.arg = 0;
    }
}

static void relax_repeat(int i, const uint8_t *src, size_t gpos, int b_size, int b_min) {
    comp_node_t *cur = &g_comp.nodes[i];
    for (int k = 0; k < 4; k++) {
        uint32_t d = cur->r[k];
        if (d == 0 || (size_t)d > gpos) continue;

        size_t ref = gpos - d;
        int max_l = b_size - i;
        if (max_l > MAX_MATCH_LEN) max_l = MAX_MATCH_LEN;
        int m_len = get_match_len(src, gpos, ref, max_l);
        if (m_len < b_min) continue;

        int lens[2] = {m_len, 3 + b_min};
        for (int li = 0; li < 2; li++) {
            int l = lens[li];
            if (l < b_min || l > m_len) continue;
            uint32_t c = cur->cost + cost_repeat(l, k, b_min);
            comp_node_t *dst_node = &g_comp.nodes[i + l];
            if (c < dst_node->cost) {
                dst_node->cost = c;
                memcpy(dst_node->r, cur->r, sizeof(dst_node->r));
                update_lru_rep(dst_node->r, k);
                dst_node->edge.from = (uint16_t)i;
                dst_node->edge.length = (uint16_t)l;
                dst_node->edge.dist = d;
                dst_node->edge.kind = 1;
                dst_node->edge.arg = (uint8_t)k;
            }
        }
    }
}

static void relax_brackets(int i, const uint8_t *src, size_t gpos, int b_size, int b_min) {
    static const uint32_t brackets[10] = {1, 2, 3, 4, 5, 6, 7, 8, 16, 32};
    comp_node_t *cur = &g_comp.nodes[i];

    for (int b = 0; b < 10; b++) {
        uint32_t d = brackets[b];
        if ((size_t)d > gpos) continue;

        size_t ref = gpos - d;
        int max_l = b_size - i;
        int max_bracket = 15 + b_min;
        if (max_l > max_bracket) max_l = max_bracket;

        int m_len = get_match_len(src, gpos, ref, max_l);
        if (m_len < b_min) continue;

        uint32_t c = cur->cost + 22;
        comp_node_t *dst_node = &g_comp.nodes[i + m_len];
        if (c < dst_node->cost) {
            dst_node->cost = c;
            memcpy(dst_node->r, cur->r, sizeof(dst_node->r));
            update_lru_exp(dst_node->r, d);
            dst_node->edge.from = (uint16_t)i;
            dst_node->edge.length = (uint16_t)m_len;
            dst_node->edge.dist = d;
            dst_node->edge.kind = 2;
            dst_node->edge.arg = (uint8_t)b;
        }
    }
}

static void relax_stride(int i, const uint8_t *src, size_t gpos, int b_size, uint32_t stride, int b_min) {
    if (stride <= 1) return;
    comp_node_t *cur = &g_comp.nodes[i];

    for (uint32_t m = 1; m <= 8; m++) {
        uint32_t d = m * stride;
        if ((size_t)d > gpos) continue;

        size_t ref = gpos - d;
        int max_l = b_size - i;
        int max_stride = 15 + b_min;
        if (max_l > max_stride) max_l = max_stride;

        int m_len = get_match_len(src, gpos, ref, max_l);
        if (m_len < b_min) continue;

        uint32_t c = cur->cost + 22;
        comp_node_t *dst_node = &g_comp.nodes[i + m_len];
        if (c < dst_node->cost) {
            dst_node->cost = c;
            memcpy(dst_node->r, cur->r, sizeof(dst_node->r));
            update_lru_exp(dst_node->r, d);
            dst_node->edge.from = (uint16_t)i;
            dst_node->edge.length = (uint16_t)m_len;
            dst_node->edge.dist = d;
            dst_node->edge.kind = 3;
            dst_node->edge.arg = (uint8_t)m;
        }
    }
}

static void relax_match_candidate(int i, const uint8_t *src, size_t gpos, int b_size,
                                 size_t ref, int b_min) {
    uint32_t d = (uint32_t)(gpos - ref);
    int max_l = b_size - i;
    if (max_l > MAX_MATCH_LEN) max_l = MAX_MATCH_LEN;

    int m_len = get_match_len(src, gpos, ref, max_l);
    if (m_len < b_min) return;

    int lens[3] = {m_len, 15 + b_min, 3 + b_min};
    comp_node_t *cur = &g_comp.nodes[i];

    for (int li = 0; li < 3; li++) {
        int l = lens[li];
        if (l < b_min || l > m_len) continue;
        uint32_t c = cur->cost + cost_explicit(l, d, b_min);
        comp_node_t *dst_node = &g_comp.nodes[i + l];
        if (c < dst_node->cost) {
            dst_node->cost = c;
            memcpy(dst_node->r, cur->r, sizeof(dst_node->r));
            update_lru_exp(dst_node->r, d);
            dst_node->edge.from = (uint16_t)i;
            dst_node->edge.length = (uint16_t)l;
            dst_node->edge.dist = d;
            dst_node->edge.kind = 4;
            dst_node->edge.arg = 0;
        }
    }
}

static void relax_hash_matches(int i, const uint8_t *src, size_t gpos, int b_size,
                               uint32_t max_dist, int b_min) {
    if (i + 4 > b_size) return;
    uint32_t h4 = hash4(src + gpos);
    int32_t curr = g_comp.head4[h4];
    int depth = 0;

    while (curr >= 0 && depth < MAX_SEARCH_DEPTH) {
        if ((size_t)curr >= gpos) break;
        uint32_t d = (uint32_t)(gpos - (size_t)curr);
        if (d > max_dist) break;
        relax_match_candidate(i, src, gpos, b_size, (size_t)curr, b_min);
        int local_idx = curr % MAX_BLOCK_HORIZON;
        curr = g_comp.prev_pos[local_idx];
        depth++;
    }
}

static void insert_hash(const uint8_t *src, size_t gpos, int i, int b_size) {
    if (i + 4 > b_size) return;
    uint32_t h4 = hash4(src + gpos);
    g_comp.prev_pos[i] = g_comp.head4[h4];
    g_comp.head4[h4] = (int32_t)gpos;
}

static void solve_block_trellis(const uint8_t *src, size_t block_start, int b_size,
                                uint32_t max_dist, uint32_t stride, int b_min) {
    const uint8_t *block = src + block_start;
    compute_literal_costs(block, b_size, g_comp.lit_cost);

    for (int i = 0; i < b_size; i++) {
        if (g_comp.nodes[i].cost == INFINITY_COST) continue;
        size_t gpos = block_start + (size_t)i;
        relax_literal(i, block);
        relax_repeat(i, src, gpos, b_size, b_min);
        relax_brackets(i, src, gpos, b_size, b_min);
        relax_stride(i, src, gpos, b_size, stride, b_min);
        relax_hash_matches(i, src, gpos, b_size, max_dist, b_min);
        insert_hash(src, gpos, i, b_size);
    }
}

static int backtrack_edges(int b_size) {
    int count = 0;
    int curr = b_size;
    while (curr > 0) {
        comp_edge_t e = g_comp.nodes[curr].edge;
        g_comp.edges_buf[count++] = e;
        curr = (int)e.from;
    }
    for (int i = 0, j = count - 1; i < j; i++, j--) {
        comp_edge_t tmp = g_comp.edges_buf[i];
        g_comp.edges_buf[i] = g_comp.edges_buf[j];
        g_comp.edges_buf[j] = tmp;
    }
    return count;
}

static inline size_t append_leb128(uint8_t *dst, size_t pos, uint32_t v) {
    while (v >= 0x80) {
        dst[pos++] = (uint8_t)((v & 0x7F) | 0x80);
        v >>= 7;
    }
    dst[pos++] = (uint8_t)v;
    return pos;
}

static void emit_literal_run(const uint8_t *lits, size_t n,
                             uint8_t *cmds, size_t *c_len,
                             uint8_t *all_lits, size_t *l_len) {
    if (n == 0) return;
    if (n <= 8) {
        cmds[(*c_len)++] = (uint8_t)(0xF2 + n);
    } else {
        cmds[(*c_len)++] = 0xF0;
        *c_len = append_leb128(cmds, *c_len, (uint32_t)n);
    }
    memcpy(all_lits + *l_len, lits, n);
    *l_len += n;
}

static void emit_explicit_match(comp_edge_t e, const uint8_t *block,
                                size_t *pending_start, size_t *pending_count,
                                int b_min, uint8_t *cmds, size_t *c_len,
                                uint8_t *all_lits, size_t *l_len,
                                uint32_t *all_offs, size_t *o_len) {
    size_t n_lit = *pending_count;
    int l_match = (int)e.length;

    if (n_lit > 3) {
        emit_literal_run(block + *pending_start, n_lit, cmds, c_len, all_lits, l_len);
        *pending_count = 0;
        n_lit = 0;
    }

    if (l_match - b_min < 15) {
        cmds[(*c_len)++] = (uint8_t)(0x80 | (n_lit << 4) | (l_match - b_min));
    } else {
        cmds[(*c_len)++] = (uint8_t)(0x80 | (n_lit << 4) | 15);
        *c_len = append_leb128(cmds, *c_len, (uint32_t)(l_match - b_min - 15));
    }
    all_offs[(*o_len)++] = e.dist;
}

static void emit_match_with_literals(comp_edge_t e, const uint8_t *block,
                                     size_t *pending_start, size_t *pending_count,
                                     int b_min, uint8_t *cmds, size_t *c_len,
                                     uint8_t *all_lits, size_t *l_len,
                                     uint32_t *all_offs, size_t *o_len) {
    size_t n_lit = *pending_count;
    int l_match = (int)e.length;

    switch (e.kind) {
    case 1: /* Repeat */
        if (n_lit <= 7 && l_match <= 3 + b_min) {
            cmds[(*c_len)++] = (uint8_t)((n_lit << 4) | ((l_match - b_min) << 2) | (int)e.arg);
        } else {
            if (n_lit > 15) {
                emit_literal_run(block + *pending_start, n_lit, cmds, c_len, all_lits, l_len);
                *pending_count = 0;
                n_lit = 0;
            }
            cmds[(*c_len)++] = (uint8_t)(0xC0 | (int)e.arg);
            if (l_match - b_min < 15) {
                cmds[(*c_len)++] = (uint8_t)((n_lit << 4) | (l_match - b_min));
            } else {
                cmds[(*c_len)++] = (uint8_t)((n_lit << 4) | 15);
                *c_len = append_leb128(cmds, *c_len, (uint32_t)(l_match - b_min - 15));
            }
        }
        break;
    case 2: /* Bracket */
        if (n_lit > 15) {
            emit_literal_run(block + *pending_start, n_lit, cmds, c_len, all_lits, l_len);
            *pending_count = 0;
            n_lit = 0;
        }
        cmds[(*c_len)++] = (uint8_t)(0xC4 + (int)e.arg);
        cmds[(*c_len)++] = (uint8_t)((n_lit << 4) | (l_match - b_min));
        break;
    case 3: /* Stride */
        if (n_lit > 15) {
            emit_literal_run(block + *pending_start, n_lit, cmds, c_len, all_lits, l_len);
            *pending_count = 0;
            n_lit = 0;
        }
        cmds[(*c_len)++] = (uint8_t)(0xD0 + (int)(e.arg - 1));
        cmds[(*c_len)++] = (uint8_t)((n_lit << 4) | (l_match - b_min));
        break;
    case 4: /* Explicit */
        emit_explicit_match(e, block, pending_start, pending_count, b_min, cmds, c_len, all_lits, l_len, all_offs, o_len);
        break;
    }

    if (*pending_count > 0) {
        memcpy(all_lits + *l_len, block + *pending_start, *pending_count);
        *l_len += *pending_count;
        *pending_count = 0;
    }
}

static void emit_block_commands(const comp_edge_t *edges, int n_edges,
                                const uint8_t *block, int b_min,
                                uint8_t *cmds, size_t *c_len,
                                uint8_t *all_lits, size_t *l_len,
                                uint32_t *all_offs, size_t *o_len) {
    size_t pending_start = 0;
    size_t pending_count = 0;

    for (int i = 0; i < n_edges; i++) {
        comp_edge_t e = edges[i];
        if (e.kind == 0) {
            if (pending_count == 0) pending_start = (size_t)e.from;
            pending_count++;
            continue;
        }
        emit_match_with_literals(e, block, &pending_start, &pending_count, b_min,
                                cmds, c_len, all_lits, l_len, all_offs, o_len);
    }
    if (pending_count > 0) {
        emit_literal_run(block + pending_start, pending_count, cmds, c_len, all_lits, l_len);
    }
}

static size_t assemble_container(const uint8_t *src, size_t u, uint32_t stride, int uncompressed,
                                 uint32_t w_log, const uint8_t *cmds, size_t cmd_len,
                                 const uint32_t *offs, size_t off_count,
                                 const uint8_t *lits, size_t lit_len,
                                 uint8_t *dst, size_t dst_cap) {
    // Section 1: 8 bytes
    uint8_t s1[8] = {0x00, (uint8_t)w_log, 0, 0, 0, 0, 0, 0};

    // Section 2: Mode 0x09
    size_t s2_len = (cmd_len > 0) ? (1 + cmd_len) : 0;

    // Section 3: Mode 0x09 + LEB128 offsets
    size_t raw_len = 0;
    for (size_t i = 0; i < off_count; i++) {
        raw_len = append_leb128(g_comp.raw_offs, raw_len, offs[i]);
    }
    size_t s3_len = (raw_len > 0) ? (1 + raw_len) : 0;

    // Section 4: raw literals
    size_t s4_len = lit_len;

    size_t comp_size = 8 + 4 + 8 + s2_len + s3_len + s4_len + ((stride > 1) ? 1 : 0);
    if (comp_size >= u || uncompressed) {
        if (dst_cap < 8 + u) return 0;
        dst[0] = 'P'; dst[1] = 'R'; dst[2] = 'Z'; dst[3] = 'M';
        dst[4] = FLAG_UNCOMPRESSED;
        dst[5] = (uint8_t)(u & 0xFF);
        dst[6] = (uint8_t)((u >> 8) & 0xFF);
        dst[7] = (uint8_t)((u >> 16) & 0xFF);
        memcpy(dst + 8, src, u);
        return 8 + u;
    }

    if (dst_cap < comp_size) return 0;
    size_t p = 0;
    dst[p++] = 'P'; dst[p++] = 'R'; dst[p++] = 'Z'; dst[p++] = 'M';
    uint8_t flags = (stride > 1) ? FLAG_STRIDE : 0;
    dst[p++] = flags;
    dst[p++] = (uint8_t)(u & 0xFF);
    dst[p++] = (uint8_t)((u >> 8) & 0xFF);
    dst[p++] = (uint8_t)((u >> 16) & 0xFF);

    if (stride > 1) dst[p++] = (uint8_t)(stride - 1);
    dst[p++] = (uint8_t)(s2_len & 0xFF);
    dst[p++] = (uint8_t)((s2_len >> 8) & 0xFF);
    dst[p++] = (uint8_t)(s3_len & 0xFF);
    dst[p++] = (uint8_t)((s3_len >> 8) & 0xFF);

    memcpy(dst + p, s1, 8); p += 8;
    if (s2_len > 0) {
        dst[p++] = 0x09;
        memcpy(dst + p, cmds, cmd_len);
        p += cmd_len;
    }
    if (s3_len > 0) {
        dst[p++] = 0x09;
        memcpy(dst + p, g_comp.raw_offs, raw_len);
        p += raw_len;
    }
    if (s4_len > 0) {
        memcpy(dst + p, lits, lit_len);
        p += lit_len;
    }
    return p;
}

int32_t przm_compress(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap, int level) {
    return przm_compress_with_dict(src, src_len, dst, dst_cap, level, NULL, 0);
}

int32_t przm_compress_with_dict(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap,
                                int level, const uint8_t *dict, size_t dict_len) {
    (void)level;
    (void)dict;
    (void)dict_len;
    if (src_len == 0) {
        if (dst_cap < 8) return PRZM_ERR_BUFFER_TOO_SMALL;
        dst[0] = 'P'; dst[1] = 'R'; dst[2] = 'Z'; dst[3] = 'M';
        for (int i = 4; i < 8; i++) dst[i] = 0;
        return 8;
    }
    if (src_len > PRZM_MAX_BLOCK_SIZE) return PRZM_ERR_EXCEEDS_MAX_BLOCK;

    int uncompressed = check_high_entropy(src, src_len);
    uint32_t stride = detect_stride(src, src_len);
    uint32_t w_log = select_window_log(src_len);
    uint32_t max_dist = 1U << w_log;
    int b_min = 4;

    if (uncompressed) {
        size_t written = assemble_container(src, src_len, stride, 1, w_log, NULL, 0, NULL, 0, NULL, 0, dst, dst_cap);
        return (written > 0) ? (int32_t)written : PRZM_ERR_BUFFER_TOO_SMALL;
    }

    for (int i = 0; i < MAX_HASH_BUCKETS; i++) g_comp.head4[i] = -1;
    size_t cmd_len = 0, lit_len = 0, off_count = 0;
    uint32_t initial_r[4] = {1, 4, 8, 16};
    if (stride > 1) {
        initial_r[0] = 1; initial_r[1] = stride;
        initial_r[2] = 2 * stride; initial_r[3] = 3 * stride;
    }

    size_t pos = 0;
    while (pos < src_len) {
        int b_size = (int)(src_len - pos);
        if (b_size > MAX_BLOCK_HORIZON) b_size = MAX_BLOCK_HORIZON;

        g_comp.nodes[0].cost = 0;
        memcpy(g_comp.nodes[0].r, initial_r, sizeof(initial_r));
        memset(&g_comp.nodes[0].edge, 0, sizeof(g_comp.nodes[0].edge));
        for (int i = 1; i <= b_size; i++) g_comp.nodes[i].cost = INFINITY_COST;

        solve_block_trellis(src, pos, b_size, max_dist, stride, b_min);
        int n_edges = backtrack_edges(b_size);
        emit_block_commands(g_comp.edges_buf, n_edges, src + pos, b_min,
                            g_comp.all_cmds, &cmd_len,
                            g_comp.all_lits, &lit_len,
                            g_comp.all_offs, &off_count);

        memcpy(initial_r, g_comp.nodes[b_size].r, sizeof(initial_r));
        pos += (size_t)b_size;
    }

    size_t written = assemble_container(src, src_len, stride, 0, w_log,
                                        g_comp.all_cmds, cmd_len,
                                        g_comp.all_offs, off_count,
                                        g_comp.all_lits, lit_len,
                                        dst, dst_cap);
    return (written > 0) ? (int32_t)written : PRZM_ERR_BUFFER_TOO_SMALL;
}
