/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#include "przm_dec.h"
#include "przm_comp.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <assert.h>

static void test_get_uncompressed_size(void) {
    printf("[RUN] przm_get_uncompressed_size\n");
    // Standard PRZM framing
    uint8_t hdr[8] = {'P', 'R', 'Z', 'M', 0, 0x20, 0x4e, 0x00};
    int32_t sz = przm_get_uncompressed_size(hdr, sizeof(hdr));
    assert(sz == 20000);

    // Truncated header
    assert(przm_get_uncompressed_size(hdr, 7) == PRZM_ERR_CORRUPTED_FRAME);
    assert(przm_get_uncompressed_size(NULL, 8) == PRZM_ERR_CORRUPTED_FRAME);

    // Invalid magic
    uint8_t bad_hdr[8] = {'X', 'Y', 'Z', 'W', 0, 0, 0, 0};
    assert(przm_get_uncompressed_size(bad_hdr, sizeof(bad_hdr)) == PRZM_ERR_INVALID_MAGIC);

    printf("[PASS] przm_get_uncompressed_size\n");
}

static void test_standard_fused_raw(void) {
    printf("[RUN] standard_fused_raw\n");
    // Uncompressed PRZM frame with 25 'A' bytes
    uint8_t frame[33];
    frame[0] = 'P'; frame[1] = 'R'; frame[2] = 'Z'; frame[3] = 'M';
    frame[4] = 0x04; // FlagUncompressed
    frame[5] = 25; frame[6] = 0; frame[7] = 0;
    memset(frame + 8, 'A', 25);

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    assert(res == 25);
    for (int i = 0; i < 25; i++) {
        assert(dst[i] == 'A');
    }
    printf("[PASS] standard_fused_raw\n");
}

static void test_standard_fused_lz(void) {
    printf("[RUN] standard_fused_lz\n");
    // Frame that produces "HeHe"
    // Header: 'P','R','Z','M', flags=0x00, U=4
    // Stride=1, L_cmd=1, L_off=0, L1=0, L4=2 ("He")
    // Command: Fused repeat match op=0x20:
    // (op >> 4) & 7 = 2 literals ("He")
    // Match: ((op >> 2) & 3) + 4 = 4 bytes? Or repeat?
    // Let's test standard fused repeat match:
    // "HeHe" can be: 2 literals "He", then match of length 2 from distance 2!
    // But minimum match base B_min is 3 (if FlagMinMatch3) or 4.
    // Let's test FlagMinMatch3: flags = 0x40.
    // Let's construct a clean valid PRZM frame:
    // U = 8: "ABCDEFGH" (8 literals, op = 0xF3 + 7 = 0xFA (8 literals))
    uint8_t frame[64];
    size_t pos = 0;
    frame[pos++] = 'P'; frame[pos++] = 'R'; frame[pos++] = 'Z'; frame[pos++] = 'M';
    frame[pos++] = 0x00; // flags
    frame[pos++] = 8; frame[pos++] = 0; frame[pos++] = 0; // U = 8
    // Section directory: L_cmd = 1, L_off = 0
    frame[pos++] = 1; frame[pos++] = 0; // L_cmd = 1
    frame[pos++] = 0; frame[pos++] = 0; // L_off = 0
    // Section 1: 8 bytes (W_log = 16, count = 0)
    frame[pos++] = 0; frame[pos++] = 16; frame[pos++] = 0; frame[pos++] = 0;
    frame[pos++] = 0; frame[pos++] = 0;  frame[pos++] = 0; frame[pos++] = 0;
    // Section 2: Command 0xFA (8 literals)
    frame[pos++] = 0xFA; // 8 literals
    // Section 4: 8 literal bytes
    memcpy(frame + pos, "ABCDEFGH", 8); pos += 8;

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, pos, dst, sizeof(dst));
    assert(res == 8);
    assert(memcmp(dst, "ABCDEFGH", 8) == 0);
    printf("[PASS] standard_fused_lz\n");
}

static void test_dictionary_decompression(void) {
    printf("[RUN] dictionary_decompression\n");
    // RFC 7932 Brotli dictionary match (opcode 0xCF):
    // Let's emit word 0 ("time") with identity transform (tau = 0).
    // Word 0 is length 4: "time". U = 4.
    // Opcode 0xCF followed by compact 2-byte: b0 = 128 (0x80), b1 = 0 -> P = 0 -> "time", tau = 0.
    uint8_t frame[64];
    size_t pos = 0;
    frame[pos++] = 'P'; frame[pos++] = 'R'; frame[pos++] = 'Z'; frame[pos++] = 'M';
    frame[pos++] = 0x10; // FlagDictRef
    frame[pos++] = 4; frame[pos++] = 0; frame[pos++] = 0; // U = 4
    // DictID: 'B','R','T','L' = 0x4252544C
    frame[pos++] = 0x4C; frame[pos++] = 0x54; frame[pos++] = 0x52; frame[pos++] = 0x42;
    // Section directory: L_cmd = 3, L_off = 0
    frame[pos++] = 3; frame[pos++] = 0; // L_cmd = 3
    frame[pos++] = 0; frame[pos++] = 0; // L_off = 0
    // Section 2: Command: 0xCF, 0x80, 0x00
    frame[pos++] = 0xCF;
    frame[pos++] = 0x80;
    frame[pos++] = 0x00;

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, pos, dst, sizeof(dst));
    assert(res == 4);
    assert(memcmp(dst, "time", 4) == 0);

    // Also test with external dictionary descriptor:
    przm_dict_entry_t entries[2] = {
        {"{\"custom\":", 10},
        {"test", 4}
    };
    przm_dict_desc_t dict = {
        0x00001001, 0, 2, entries
    };
    uint8_t dict_frame[30] = {
        0x50, 0x52, 0x5A, 0x4D, 0x10, 0x0A, 0x00, 0x00,
        0x01, 0x10, 0x00, 0x00, 0x06, 0x00, 0x00, 0x00,
        0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
        0x00, 0x01, 0x00, 0x00, 0x00, 0xD8
    };
    memset(dst, 0, sizeof(dst));
    res = przm_decompress_dict(dict_frame, sizeof(dict_frame), dst, sizeof(dst), &dict);
    assert(res == 10);
    assert(memcmp(dst, "{\"custom\":", 10) == 0);

    printf("[PASS] dictionary_decompression\n");
}

static void test_command_split_decompression(void) {
    printf("[RUN] command_split_decompression\n");
    // Test Mode 8 command split decompression
    // Input with Mode 8 tri-stream
    // Let's create a frame with 4 literal bytes: "WXYZ"
    // opStream: 1 byte: 0xF6 (4 literals: 0xF6 - 0xF2 = 4)
    // Mode 8:
    // Byte 0: 0x08
    // L_cmd_orig = 1
    // L_op = 2 (Mode 9 with 1 byte 0xF6: 0x09, 0xF6)
    // L_m = 1 (Mode 9 with 0 bytes: 0x09)
    // L_var = 1 (Mode 9 with 0 bytes: 0x09)
    // Total L_cmd = 13 + 2 + 1 + 1 = 17 bytes
    uint8_t frame[64];
    size_t pos = 0;
    frame[pos++] = 'P'; frame[pos++] = 'R'; frame[pos++] = 'Z'; frame[pos++] = 'M';
    frame[pos++] = 0x00; // flags
    frame[pos++] = 4; frame[pos++] = 0; frame[pos++] = 0; // U = 4
    // Section directory: L_cmd = 17, L_off = 0
    frame[pos++] = 17; frame[pos++] = 0;
    frame[pos++] = 0; frame[pos++] = 0;
    // Section 1: 8 bytes (W_log = 16, count = 0)
    frame[pos++] = 0; frame[pos++] = 16; frame[pos++] = 0; frame[pos++] = 0;
    frame[pos++] = 0; frame[pos++] = 0;  frame[pos++] = 0; frame[pos++] = 0;
    // Section 2: Mode 8
    frame[pos++] = 0x08; // Mode 8
    // L_cmd_orig = 1 (uint32 LE)
    frame[pos++] = 1; frame[pos++] = 0; frame[pos++] = 0; frame[pos++] = 0;
    // L_op = 2 (uint32 LE)
    frame[pos++] = 2; frame[pos++] = 0; frame[pos++] = 0; frame[pos++] = 0;
    // L_m = 1 (uint32 LE)
    frame[pos++] = 1; frame[pos++] = 0; frame[pos++] = 0; frame[pos++] = 0;
    // opStream: Mode 9 (1 byte data: 0xF6)
    frame[pos++] = 0x09; frame[pos++] = 0xF6;
    // metaStream: Mode 9 (0 bytes data)
    frame[pos++] = 0x09;
    // varStream: Mode 9 (0 bytes data)
    frame[pos++] = 0x09;
    // Section 4: 4 literal bytes
    memcpy(frame + pos, "WXYZ", 4); pos += 4;

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, pos, dst, sizeof(dst));
    assert(res == 4);
    assert(memcmp(dst, "WXYZ", 4) == 0);
    printf("[PASS] command_split_decompression\n");
}

#include "vans_meta_tables.h"

static void test_command_split_and_join_roundtrip(void) {
    printf("[RUN] command_split_and_join_roundtrip\n");
    uint8_t orig[256];
    size_t o_len = 0;

    // 0x00..0x7F Fused repeat matches
    orig[o_len++] = 0x00;
    orig[o_len++] = 0x12;
    orig[o_len++] = 0x7F;

    // 0x80..0xBF Fused extended matches
    orig[o_len++] = 0x80;
    orig[o_len++] = 0x85;
    orig[o_len++] = 0x8F; orig[o_len++] = 0x2A;
    orig[o_len++] = 0xBF; orig[o_len++] = 0x90; orig[o_len++] = 0x01;

    // 0xC0..0xC3 Long repeat matches
    orig[o_len++] = 0xC0; orig[o_len++] = 0x04;
    orig[o_len++] = 0xC3; orig[o_len++] = 0x1F; orig[o_len++] = 0x33;

    // 0xC4..0xCD Fused bracket matches
    orig[o_len++] = 0xC4; orig[o_len++] = 0x01;
    orig[o_len++] = 0xCD; orig[o_len++] = 0x55;

    // 0xCE Difference lattice matches
    orig[o_len++] = 0xCE; orig[o_len++] = 0x00; orig[o_len++] = 0x01;
    orig[o_len++] = 0xCE; orig[o_len++] = 0x38; orig[o_len++] = 0x02; orig[o_len++] = 0x05;
    orig[o_len++] = 0xCE; orig[o_len++] = 0x07; orig[o_len++] = 0x03; orig[o_len++] = 0x08;
    orig[o_len++] = 0xCE; orig[o_len++] = 0x3F; orig[o_len++] = 0x04; orig[o_len++] = 0x09; orig[o_len++] = 0x0A;

    // 0xCF RFC transformed matches
    orig[o_len++] = 0xCF; orig[o_len++] = 0x85; orig[o_len++] = 0x20;
    orig[o_len++] = 0xCF; orig[o_len++] = 0x10; orig[o_len++] = 0x45;

    // 0xD0..0xD7 Stride matches
    orig[o_len++] = 0xD0; orig[o_len++] = 0x11;
    orig[o_len++] = 0xD7; orig[o_len++] = 0x22;

    // 0xE0..0xE8 Delta-mask matches
    orig[o_len++] = 0xE0; orig[o_len++] = 0x05;
    orig[o_len++] = 0xE7; orig[o_len++] = 0xFF; orig[o_len++] = 0x50;
    orig[o_len++] = 0xE8; orig[o_len++] = 0xFF; orig[o_len++] = 0x60;

    // 0xE9 Raw external dict match
    orig[o_len++] = 0xE9; orig[o_len++] = 0x04;
    orig[o_len++] = 0xE9; orig[o_len++] = 0x1F; orig[o_len++] = 0x70;

    // 0xF0 Pure literal run
    orig[o_len++] = 0xF0; orig[o_len++] = 0x80; orig[o_len++] = 0x02;

    // 0xF1 RLE run
    orig[o_len++] = 0xF1; orig[o_len++] = 0x10;

    // 0xF3..0xFA Direct short literals
    orig[o_len++] = 0xF3;
    orig[o_len++] = 0xF8;
    orig[o_len++] = 0xFA;

    uint8_t op[256], meta[256], var[256];
    size_t op_len = 0, meta_len = 0, var_len = 0;

    int32_t s_res = przm_split_commands(orig, o_len,
                                        op, sizeof(op), &op_len,
                                        meta, sizeof(meta), &meta_len,
                                        var, sizeof(var), &var_len);
    assert(s_res == PRZM_OK);
    assert(op_len > 0);
    assert(meta_len > 0);
    assert(var_len > 0);

    uint8_t reconst[256];
    int32_t j_res = przm_join_commands(reconst, sizeof(reconst),
                                       op, op_len,
                                       meta, meta_len,
                                       var, var_len,
                                       o_len);
    assert(j_res == PRZM_OK);
    assert(memcmp(orig, reconst, o_len) == 0);

    // Test buffer safety
    uint8_t small_buf[2];
    assert(przm_split_commands(orig, o_len, small_buf, 2, &op_len, meta, sizeof(meta), &meta_len, var, sizeof(var), &var_len) == PRZM_ERR_BUFFER_TOO_SMALL);
    assert(przm_join_commands(reconst, 10, op, op_len, meta, meta_len, var, var_len, o_len) == PRZM_ERR_BUFFER_TOO_SMALL);

    printf("[PASS] command_split_and_join_roundtrip\n");
}

static inline uint8_t test_context_meta(uint8_t p) {
    if (p == 0) return 0;
    if (p < 4)  return 1;
    if (p < 16) return 2;
    if (p < 32) return 3;
    if (p < 64) return 4;
    if (p < 128) return 5;
    if (p < 192) return 6;
    return 7;
}

static size_t test_vans_encode_meta8(const uint8_t *src, size_t slen, uint8_t *dst, size_t dcap) {
    uint16_t cdf[8][256];
    for (int c = 0; c < 8; c++) {
        uint16_t cum = 0;
        for (int s = 0; s < 256; s++) {
            cdf[c][s] = cum;
            cum += przm_static_meta_freq8[c][s];
        }
    }

    uint32_t x[4] = {65536, 65536, 65536, 65536};
    uint8_t st_buf[4096];
    size_t st_ptr = 0;

    for (int64_t i = (int64_t)slen - 1; i >= 0; i--) {
        int lane = (int)(i & 3);
        uint8_t sym = src[i];
        uint8_t prev = (i > 0) ? src[i - 1] : 0;
        uint8_t ctx = test_context_meta(prev);
        uint32_t freq = przm_static_meta_freq8[ctx][sym];
        uint32_t cdf_val = cdf[ctx][sym];

        while (x[lane] > 4096 * freq - 1) {
            assert(st_ptr < sizeof(st_buf));
            st_buf[st_ptr++] = (uint8_t)(x[lane] & 0xFF);
            x[lane] >>= 8;
        }
        x[lane] = (x[lane] / freq) * 4096 + cdf_val + (x[lane] % freq);
    }

    size_t total = 24 + st_ptr;
    assert(total <= dcap);

    dst[0] = 'V'; dst[1] = 'A'; dst[2] = 0x01; dst[3] = 0x41;
    dst[4] = (uint8_t)(slen & 0xFF);
    dst[5] = (uint8_t)((slen >> 8) & 0xFF);
    dst[6] = (uint8_t)((slen >> 16) & 0xFF);
    dst[7] = (uint8_t)((slen >> 24) & 0xFF);
    for (int j = 0; j < 4; j++) {
        dst[8 + j * 4 + 0] = (uint8_t)(x[j] & 0xFF);
        dst[8 + j * 4 + 1] = (uint8_t)((x[j] >> 8) & 0xFF);
        dst[8 + j * 4 + 2] = (uint8_t)((x[j] >> 16) & 0xFF);
        dst[8 + j * 4 + 3] = (uint8_t)((x[j] >> 24) & 0xFF);
    }
    for (size_t i = 0; i < st_ptr; i++) {
        dst[24 + i] = st_buf[st_ptr - 1 - i];
    }
    return total;
}

static void test_vans_meta_roundtrip(void) {
    printf("[RUN] vans_meta_roundtrip\n");
    uint8_t meta_in[128];
    for (size_t i = 0; i < sizeof(meta_in); i++) {
        meta_in[i] = (uint8_t)((i * 37 + 13) % 256);
    }

    uint8_t enc[1024];
    size_t elen = test_vans_encode_meta8(meta_in, sizeof(meta_in), enc, sizeof(enc));
    assert(elen >= 24);

    uint8_t dec[128];
    int32_t d_res = przm_vans_decode_meta8(enc, elen, dec, sizeof(dec), sizeof(meta_in));
    assert(d_res == PRZM_OK);
    assert(memcmp(meta_in, dec, sizeof(meta_in)) == 0);

    // Verify error conditions
    assert(przm_vans_decode_meta8(enc, 23, dec, sizeof(dec), sizeof(meta_in)) == PRZM_ERR_CORRUPTED_FRAME);
    enc[0] = 'X';
    assert(przm_vans_decode_meta8(enc, elen, dec, sizeof(dec), sizeof(meta_in)) == PRZM_ERR_CORRUPTED_FRAME);
    enc[0] = 'V';
    assert(przm_vans_decode_meta8(enc, elen, dec, sizeof(dec), sizeof(meta_in) + 1) == PRZM_ERR_CORRUPTED_FRAME);
    assert(przm_vans_decode_meta8(enc, elen, dec, sizeof(dec) - 1, sizeof(meta_in)) == PRZM_ERR_BUFFER_TOO_SMALL);

    printf("[PASS] vans_meta_roundtrip\n");
}

static void test_mode8_vans_frame_decompression(void) {
    printf("[RUN] mode8_vans_frame_decompression\n");
    // Target uncompressed: "ABCDEFGHGHGHGH" (U = 14)
    // Literals: "ABCDEFGH" (8 bytes)
    // Commands:
    // 1. 0xFA (8 literals)
    // 2. 0xC5 (fused bracket match with tok = 0x02: llit = 0, lmatch = 2 + 4 = 6, dist = 2)
    // Commands linear: [0xFA, 0xC5, 0x02] (3 bytes)
    // opStream: [0xFA, 0xC5] (2 bytes)
    // metaStream: [0x02] (1 byte)
    // varStream: [] (0 bytes)

    uint8_t meta_raw[1] = {0x02};
    uint8_t meta_enc[256];
    size_t meta_enc_len = test_vans_encode_meta8(meta_raw, 1, meta_enc, sizeof(meta_enc));
    assert(meta_enc_len >= 24);

    uint8_t op_enc[3] = {0x09, 0xFA, 0xC5}; // Mode 9 (2 bytes data)
    uint8_t var_enc[1] = {0x09};             // Mode 9 (0 bytes data)

    size_t l_cmd = 13 + sizeof(op_enc) + meta_enc_len + sizeof(var_enc);

    uint8_t frame[512];
    size_t pos = 0;
    frame[pos++] = 'P'; frame[pos++] = 'R'; frame[pos++] = 'Z'; frame[pos++] = 'M';
    frame[pos++] = 0x00; // flags
    frame[pos++] = 14; frame[pos++] = 0; frame[pos++] = 0; // U = 14
    // Section directory: L_cmd, L_off = 0
    frame[pos++] = (uint8_t)(l_cmd & 0xFF);
    frame[pos++] = (uint8_t)((l_cmd >> 8) & 0xFF);
    frame[pos++] = 0; frame[pos++] = 0;
    // Section 1: 8 bytes
    frame[pos++] = 0; frame[pos++] = 16; frame[pos++] = 0; frame[pos++] = 0;
    frame[pos++] = 0; frame[pos++] = 0;  frame[pos++] = 0; frame[pos++] = 0;

    // Section 2: Mode 8 container
    frame[pos++] = 0x08; // Mode 8
    // L_cmd_orig = 3
    frame[pos++] = 3; frame[pos++] = 0; frame[pos++] = 0; frame[pos++] = 0;
    // L_op = 3
    frame[pos++] = (uint8_t)sizeof(op_enc); frame[pos++] = 0; frame[pos++] = 0; frame[pos++] = 0;
    // L_m = meta_enc_len
    frame[pos++] = (uint8_t)(meta_enc_len & 0xFF);
    frame[pos++] = (uint8_t)((meta_enc_len >> 8) & 0xFF);
    frame[pos++] = 0; frame[pos++] = 0;

    // Append opStream (Mode 9)
    memcpy(frame + pos, op_enc, sizeof(op_enc)); pos += sizeof(op_enc);
    // Append metaStream (8-Context V-ANS)
    memcpy(frame + pos, meta_enc, meta_enc_len); pos += meta_enc_len;
    // Append varStream (Mode 9)
    memcpy(frame + pos, var_enc, sizeof(var_enc)); pos += sizeof(var_enc);

    // Section 4: 8 literals
    memcpy(frame + pos, "ABCDEFGH", 8); pos += 8;

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, pos, dst, sizeof(dst));
    assert(res == 14);
    assert(memcmp(dst, "ABCDEFGHGHGHGH", 14) == 0);
    printf("[PASS] mode8_vans_frame_decompression\n");
}

#include "distance_tables.h"

static inline uint8_t test_context_dist(uint8_t prev) {
    if (prev < 6) return 0;
    if (prev < 14) return 1;
    if (prev < 26) return 2;
    return 3;
}

static size_t test_vans_encode_dist4(const uint8_t *codes, size_t n, uint8_t *dst, size_t dcap) {
    uint16_t cdf[4][64];
    for (int c = 0; c < 4; c++) {
        uint16_t cum = 0;
        for (int s = 0; s < 64; s++) {
            cdf[c][s] = cum;
            cum += przm_dist_static_freq4[c][s];
        }
    }

    uint32_t x = 65536;
    uint8_t st_buf[4096];
    size_t st_ptr = 0;

    for (int64_t i = (int64_t)n - 1; i >= 0; i--) {
        uint8_t sym = codes[i];
        uint8_t prev = (i > 0) ? codes[i - 1] : 0;
        uint8_t ctx = test_context_dist(prev);
        uint32_t freq = przm_dist_static_freq4[ctx][sym];
        uint32_t cdf_val = cdf[ctx][sym];

        while (x > 4096 * freq - 1) {
            assert(st_ptr < sizeof(st_buf));
            st_buf[st_ptr++] = (uint8_t)(x & 0xFF);
            x >>= 8;
        }
        x = (x / freq) * 4096 + cdf_val + (x % freq);
    }

    size_t total = 5 + st_ptr;
    assert(total <= dcap);

    dst[0] = 0x0C;
    dst[1] = (uint8_t)(x & 0xFF);
    dst[2] = (uint8_t)((x >> 8) & 0xFF);
    dst[3] = (uint8_t)((x >> 16) & 0xFF);
    dst[4] = (uint8_t)((x >> 24) & 0xFF);
    for (size_t i = 0; i < st_ptr; i++) {
        dst[5 + i] = st_buf[st_ptr - 1 - i];
    }
    return total;
}

static void test_distance_to_code_and_back(void) {
    printf("[RUN] distance_to_code_and_back\n");
    for (uint32_t d = 1; d <= 4; d++) {
        uint8_t c = 0, nb = 0;
        uint32_t extra = 0;
        assert(przm_distance_to_code(d, &c, &extra, &nb) == PRZM_OK);
        assert(c == d - 1 && extra == 0 && nb == 0);
        uint32_t back = 0;
        assert(przm_code_to_distance(c, extra, &back) == PRZM_OK);
        assert(back == d);
    }

    for (uint8_t c = 0; c < 49; c++) {
        uint32_t base = przm_dist_base[c];
        uint8_t nb = przm_dist_extra_bits[c];

        uint8_t got_c = 0, got_nb = 0;
        uint32_t extra = 0;
        assert(przm_distance_to_code(base, &got_c, &extra, &got_nb) == PRZM_OK);
        assert(got_c == c && extra == 0 && got_nb == nb);

        uint32_t back = 0;
        assert(przm_code_to_distance(got_c, extra, &back) == PRZM_OK);
        assert(back == base);

        if (nb > 0 && nb <= 22) {
            uint32_t max_dist = base + (1U << nb) - 1U;
            assert(przm_distance_to_code(max_dist, &got_c, &extra, &got_nb) == PRZM_OK);
            assert(got_c == c && got_nb == nb);
            assert(przm_code_to_distance(got_c, extra, &back) == PRZM_OK);
            assert(back == max_dist);
        }
    }
    printf("[PASS] distance_to_code_and_back\n");
}

static void test_vans_dist4_roundtrip(void) {
    printf("[RUN] vans_dist4_roundtrip\n");
    uint8_t codes[64];
    for (size_t i = 0; i < sizeof(codes); i++) {
        codes[i] = (uint8_t)((i * 17 + 5) % 49);
    }

    uint8_t enc[1024];
    size_t elen = test_vans_encode_dist4(codes, sizeof(codes), enc, sizeof(enc));
    assert(elen >= 5);

    uint8_t dec[64];
    int32_t res = przm_vans_decode_dist4(enc, elen, dec, sizeof(dec), sizeof(codes));
    assert(res == PRZM_OK);
    assert(memcmp(codes, dec, sizeof(codes)) == 0);
    printf("[PASS] vans_dist4_roundtrip\n");
}

static void test_distance_prefix_frame_decompression(void) {
    printf("[RUN] distance_prefix_frame_decompression\n");
    // U = 16: "ABCDEFGHABCDEFGH"
    // Literals: "ABCDEFGH" (8 bytes)
    // Commands:
    // 1. 0xFA (8 literals)
    // 2. 0x84 (llit=0, base=4 -> lmatch=4+4=8, explicit offset from Section 3)
    // Distance = 8
    // przm_distance_to_code(8) -> c = 5, extra = 1, nb = 1
    uint8_t code = 5;
    uint8_t code_enc[64];
    size_t code_enc_len = test_vans_encode_dist4(&code, 1, code_enc, sizeof(code_enc));
    assert(code_enc_len >= 5);

    // Extra bits stream: 1 bit with value 1 -> 0x80
    uint8_t extra_bits[1] = {0x80};

    // Mode 0x06 Section 3 container:
    // 13-byte header:
    // Byte 0: 0x06
    // L_varint = 1 (uint32 LE: distance 8 is 1-byte LEB128 0x08)
    // N_matches = 1 (uint32 LE)
    // L_code = code_enc_len (uint32 LE)
    uint8_t s3[256];
    size_t s3_pos = 0;
    s3[s3_pos++] = 0x06;
    // L_varint = 1
    s3[s3_pos++] = 1; s3[s3_pos++] = 0; s3[s3_pos++] = 0; s3[s3_pos++] = 0;
    // N_matches = 1
    s3[s3_pos++] = 1; s3[s3_pos++] = 0; s3[s3_pos++] = 0; s3[s3_pos++] = 0;
    // L_code = code_enc_len
    s3[s3_pos++] = (uint8_t)code_enc_len; s3[s3_pos++] = 0; s3[s3_pos++] = 0; s3[s3_pos++] = 0;
    // Append code stream
    memcpy(s3 + s3_pos, code_enc, code_enc_len); s3_pos += code_enc_len;
    // Append extra bits stream
    memcpy(s3 + s3_pos, extra_bits, sizeof(extra_bits)); s3_pos += sizeof(extra_bits);

    uint8_t frame[512];
    size_t pos = 0;
    frame[pos++] = 'P'; frame[pos++] = 'R'; frame[pos++] = 'Z'; frame[pos++] = 'M';
    frame[pos++] = 0x00; // flags
    frame[pos++] = 16; frame[pos++] = 0; frame[pos++] = 0; // U = 16
    // Section directory: L_cmd = 2, L_off = s3_pos
    frame[pos++] = 2; frame[pos++] = 0;
    frame[pos++] = (uint8_t)(s3_pos & 0xFF);
    frame[pos++] = (uint8_t)((s3_pos >> 8) & 0xFF);
    // Section 1: 8 bytes
    frame[pos++] = 0; frame[pos++] = 16; frame[pos++] = 0; frame[pos++] = 0;
    frame[pos++] = 0; frame[pos++] = 0;  frame[pos++] = 0; frame[pos++] = 0;
    // Section 2: Commands [0xFA, 0x84]
    frame[pos++] = 0xFA;
    frame[pos++] = 0x84;
    // Section 3: Mode 0x06 container
    memcpy(frame + pos, s3, s3_pos); pos += s3_pos;
    // Section 4: 8 literals
    memcpy(frame + pos, "ABCDEFGH", 8); pos += 8;

    uint8_t dst[128];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, pos, dst, sizeof(dst));
    assert(res == 16);
    assert(memcmp(dst, "ABCDEFGHABCDEFGH", 16) == 0);
    printf("[PASS] distance_prefix_frame_decompression\n");
}

static void test_corrupted_and_truncated_frames(void) {
    printf("[RUN] corrupted_and_truncated_frames\n");
    uint8_t dst[256];

    // NULL inputs
    assert(przm_decompress(NULL, 10, dst, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME);
    assert(przm_decompress((const uint8_t*)"PRZM", 4, NULL, sizeof(dst)) == PRZM_ERR_CORRUPTED_FRAME);
    assert(przm_decompress((const uint8_t*)"PRZM", 0, dst, sizeof(dst)) == PRZM_OK);

    // Buffer too small (< U + 64)
    uint8_t frame[8] = {'P', 'R', 'Z', 'M', 0x04, 100, 0, 0};
    assert(przm_decompress(frame, sizeof(frame), dst, 100) == PRZM_ERR_BUFFER_TOO_SMALL);

    // Unknown dictionary
    uint8_t dict_frame[16] = {'P', 'R', 'Z', 'M', 0x10, 10, 0, 0, 0x99, 0x99, 0x99, 0x99, 0, 0, 0, 0};
    assert(przm_decompress(dict_frame, sizeof(dict_frame), dst, sizeof(dst)) == PRZM_ERR_UNKNOWN_DICT);

    printf("[PASS] corrupted_and_truncated_frames\n");
}

static void test_foliation_classification(void) {
    printf("[RUN] foliation_classification\n");
    // Mode 0: initial p1=0 => 3
    assert(przm_context_classify(0x00, 0) == 3);
    assert(przm_context_classify(' ', 0) == 0);
    assert(przm_context_classify('{', 0) == 1);
    assert(przm_context_classify('a', 0) == 2);
    assert(przm_context_classify('A', 0) == 3);

    // Mode 1: initial p1=0 => 3
    assert(przm_context_classify(0x00, 1) == 3);
    assert(przm_context_classify('"', 1) == 0);
    assert(przm_context_classify(':', 1) == 0);
    assert(przm_context_classify('x', 1) == 1);
    assert(przm_context_classify('7', 1) == 2);

    // Mode 2: initial p1=0 => 1
    assert(przm_context_classify(0x00, 2) == 1);
    assert(przm_context_classify(' ', 2) == 0);
    assert(przm_context_classify('+', 2) == 0);
    assert(przm_context_classify('a', 2) == 1);

    // Mode 3: initial (0, 0) => 1
    assert(przm_context_classify_order2(0, 0) == 1);
    assert(przm_context_classify_order2('1', '2') == 0);
    assert(przm_context_classify_order2('a', '2') == 1);
    assert(przm_context_classify_order2('a', 'b') == 2);
    assert(przm_context_classify_order2('9', 'b') == 3);
    assert(przm_context_classify_order2('z', ' ') == 0);
    printf("[PASS] foliation_classification\n");
}

static void test_foliation_roundtrip(void) {
    printf("[RUN] foliation_roundtrip\n");
    const char *sample = "function calculate(x) { return x * 10 + 5; }\n";
    size_t slen = strlen(sample);

    for (uint8_t mode = 0; mode <= 3; mode++) {
        uint8_t f0[128], f1[128], f2[128], f3[128];
        size_t l0 = 0, l1 = 0, l2 = 0, l3 = 0;
        uint8_t p1 = 0, p2 = 0;

        for (size_t i = 0; i < slen; i++) {
            uint8_t b = (uint8_t)sample[i];
            uint8_t c = (mode < 3) ? przm_context_classify(p1, mode)
                                   : przm_context_classify_order2(p2, p1);
            if (c == 0) f0[l0++] = b;
            else if (c == 1) f1[l1++] = b;
            else if (c == 2) f2[l2++] = b;
            else if (c == 3) f3[l3++] = b;
            p2 = p1;
            p1 = b;
        }

        uint8_t dst[128];
        memset(dst, 0, sizeof(dst));
        int32_t err = przm_reconstitute_fibers(dst, slen, f0, f1, f2, f3, l0, l1, l2, l3, mode);
        assert(err == PRZM_OK);
        assert(memcmp(dst, sample, slen) == 0);
    }
    printf("[PASS] foliation_roundtrip\n");
}

static void test_nibble_planes(void) {
    printf("[RUN] nibble_planes\n");
    // "ABCDEFGH" (8 literals, all high nibble 0x4)
    // Low nibbles: 'A'=0x41, 'B'=0x42 => 0x12; 'C'=0x43, 'D'=0x44 => 0x34; 'E'=0x45, 'F'=0x46 => 0x56; 'G'=0x47, 'H'=0x48 => 0x78
    // High nibbles: run of 8 with high nibble 4 => tok = ((8-1)<<4) | 4 = 0x74
    uint8_t src[13];
    src[0] = 8; src[1] = 0; src[2] = 0; src[3] = 0; // N = 8
    src[4] = 4; src[5] = 0; src[6] = 0; src[7] = 0; // N_low = 4
    src[8] = 0x12; src[9] = 0x34; src[10] = 0x56; src[11] = 0x78; // low nibbles
    src[12] = 0x74; // high nibble RLE token: run of 8, high nibble 4

    uint8_t dst[16];
    memset(dst, 0, sizeof(dst));
    int32_t err = przm_unpack_nibble_planes(src, sizeof(src), dst, 8);
    assert(err == PRZM_OK);
    assert(memcmp(dst, "ABCDEFGH", 8) == 0);
    printf("[PASS] nibble_planes\n");
}

static void test_etale_sheaf_frame_decompression(void) {
    printf("[RUN] etale_sheaf_frame_decompression\n");
    const char *sample = "function test() { return 123; }\n";
    size_t slen = strlen(sample);

    // Decompose into 4 fibers (Mode 0)
    uint8_t f0[128], f1[128], f2[128], f3[128];
    size_t l0 = 0, l1 = 0, l2 = 0, l3 = 0;
    uint8_t p1 = 0;
    for (size_t i = 0; i < slen; i++) {
        uint8_t b = (uint8_t)sample[i];
        uint8_t c = przm_context_classify(p1, 0);
        if (c == 0) f0[l0++] = b;
        else if (c == 1) f1[l1++] = b;
        else if (c == 2) f2[l2++] = b;
        else if (c == 3) f3[l3++] = b;
        p1 = b;
    }

    // Build 40-byte Étale Sheaf Section 4
    uint8_t s4[256];
    size_t s4_pos = 0;
    s4[s4_pos++] = 'V'; s4[s4_pos++] = 'A'; s4[s4_pos++] = 0x01;
    s4[s4_pos++] = 0x01 | 0x04 | (0 << 4); // 4-lane | CtxSheaf | Mode 0
    s4[s4_pos++] = (uint8_t)(slen & 0xFF);
    s4[s4_pos++] = (uint8_t)((slen >> 8) & 0xFF);
    s4[s4_pos++] = (uint8_t)((slen >> 16) & 0xFF);
    s4[s4_pos++] = (uint8_t)((slen >> 24) & 0xFF);

    size_t l[4] = {l0, l1, l2, l3};
    for (int k = 0; k < 4; k++) {
        s4[s4_pos++] = (uint8_t)(l[k] & 0xFF);
        s4[s4_pos++] = (uint8_t)((l[k] >> 8) & 0xFF);
        s4[s4_pos++] = (uint8_t)((l[k] >> 16) & 0xFF);
        s4[s4_pos++] = (uint8_t)((l[k] >> 24) & 0xFF);
    }
    // E_k == L_k (raw uncompressed fiber payload transparency)
    for (int k = 0; k < 4; k++) {
        s4[s4_pos++] = (uint8_t)(l[k] & 0xFF);
        s4[s4_pos++] = (uint8_t)((l[k] >> 8) & 0xFF);
        s4[s4_pos++] = (uint8_t)((l[k] >> 16) & 0xFF);
        s4[s4_pos++] = (uint8_t)((l[k] >> 24) & 0xFF);
    }
    memcpy(s4 + s4_pos, f0, l0); s4_pos += l0;
    memcpy(s4 + s4_pos, f1, l1); s4_pos += l1;
    memcpy(s4 + s4_pos, f2, l2); s4_pos += l2;
    memcpy(s4 + s4_pos, f3, l3); s4_pos += l3;

    // Build frame
    uint8_t frame[512];
    size_t pos = 0;
    frame[pos++] = 'P'; frame[pos++] = 'R'; frame[pos++] = 'Z'; frame[pos++] = 'M';
    frame[pos++] = 0x80; // FlagNibble
    frame[pos++] = (uint8_t)(slen & 0xFF);
    frame[pos++] = (uint8_t)((slen >> 8) & 0xFF);
    frame[pos++] = (uint8_t)((slen >> 16) & 0xFF);

    // Section 2: Command 0xF0 + LEB128(slen)
    uint8_t cmds[16];
    size_t cmd_len = 0;
    cmds[cmd_len++] = 0xF0;
    size_t rem = slen;
    while (rem >= 0x80) {
        cmds[cmd_len++] = (uint8_t)((rem & 0x7F) | 0x80);
        rem >>= 7;
    }
    cmds[cmd_len++] = (uint8_t)rem;

    // Section directory: L_cmd, L_off (16-bit LE)
    frame[pos++] = (uint8_t)(cmd_len & 0xFF);
    frame[pos++] = (uint8_t)((cmd_len >> 8) & 0xFF);
    frame[pos++] = 0; frame[pos++] = 0; // 0 offsets

    // Section 1: parameters (8 bytes)
    frame[pos++] = 0; frame[pos++] = 16;
    for (int i = 0; i < 6; i++) frame[pos++] = 0;

    // Section 2: commands
    memcpy(frame + pos, cmds, cmd_len); pos += cmd_len;
    // Section 3: offsets (0 bytes)
    // Section 4: Étale Sheaf literals
    memcpy(frame + pos, s4, s4_pos); pos += s4_pos;

    uint8_t dst[256];
    memset(dst, 0, sizeof(dst));
    int32_t res = przm_decompress(frame, pos, dst, sizeof(dst));
    assert(res == (int32_t)slen);
    assert(memcmp(dst, sample, slen) == 0);
    printf("[PASS] etale_sheaf_frame_decompression\n");
}

static void test_compress_roundtrip(void) {
    printf("[RUN] compress_roundtrip\n");

    // 1. Empty buffer
    {
        uint8_t comp[64], dst[128];
        int32_t csz = przm_compress(NULL, 0, comp, sizeof(comp), 6);
        assert(csz == 8);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == 0);
    }

    // 2. Single byte
    {
        uint8_t src[1] = {42};
        uint8_t comp[64], dst[128];
        int32_t csz = przm_compress(src, 1, comp, sizeof(comp), 6);
        assert(csz > 0);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == 1 && dst[0] == 42);
    }

    // 3. Short text
    {
        const char *txt = "Hello Clean Room PRZM!";
        size_t tlen = strlen(txt);
        uint8_t comp[256], dst[256];
        int32_t csz = przm_compress((const uint8_t *)txt, tlen, comp, sizeof(comp), 6);
        assert(csz > 0);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == (int32_t)tlen);
        assert(memcmp(dst, txt, tlen) == 0);
    }

    // 4. Repetitive text (fox * 30 = 1350 bytes)
    {
        const char *pat = "The quick brown fox jumps over the lazy dog. ";
        size_t plen = strlen(pat);
        size_t slen = plen * 30;
        uint8_t src[2048], comp[2048], dst[2048 + 128];
        for (int i = 0; i < 30; i++) memcpy(src + i * plen, pat, plen);

        int32_t csz = przm_compress(src, slen, comp, sizeof(comp), 6);
        assert(csz > 0);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == (int32_t)slen);
        assert(memcmp(dst, src, slen) == 0);
    }

    // 5. Stride tabular data (16 bytes * 100 = 1600 bytes)
    {
        uint8_t row[16] = {1, 2, 3, 4, 10, 20, 30, 40, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22};
        uint8_t src[1600], comp[2048], dst[2048 + 128];
        for (int i = 0; i < 100; i++) {
            memcpy(src + i * 16, row, 16);
            src[i * 16] = (uint8_t)i;
        }
        int32_t csz = przm_compress(src, sizeof(src), comp, sizeof(comp), 6);
        assert(csz > 0);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == sizeof(src));
        assert(memcmp(dst, src, sizeof(src)) == 0);
    }

    // 6. High entropy pseudo-random (1024 bytes)
    {
        uint8_t src[1024], comp[2048], dst[2048 + 128];
        uint32_t seed = 987654321U;
        for (size_t i = 0; i < sizeof(src); i++) {
            seed = seed * 1664525U + 1013904223U;
            src[i] = (uint8_t)(seed >> 24);
        }
        int32_t csz = przm_compress(src, sizeof(src), comp, sizeof(comp), 6);
        assert(csz > 0);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == sizeof(src));
        assert(memcmp(dst, src, sizeof(src)) == 0);
    }

    // 7. Multi-block data spanning > 64 KiB (80 KiB)
    {
        static uint8_t src[81920], comp[90000], dst[90000];
        const char *chunk = "Lorem ipsum dolor sit amet, consectetur adipiscing elit. ";
        size_t clen = strlen(chunk);
        for (size_t i = 0; i < sizeof(src); i += clen) {
            size_t copy_len = (sizeof(src) - i < clen) ? (sizeof(src) - i) : clen;
            memcpy(src + i, chunk, copy_len);
        }
        int32_t csz = przm_compress(src, sizeof(src), comp, sizeof(comp), 6);
        assert(csz > 0);
        int32_t dsz = przm_decompress(comp, (size_t)csz, dst, sizeof(dst));
        assert(dsz == sizeof(src));
        assert(memcmp(dst, src, sizeof(src)) == 0);
    }

    printf("[PASS] compress_roundtrip\n");
}

int main(int argc, char **argv) {
    if (argc >= 4 && strcmp(argv[1], "--decompress") == 0) {
        const char *in_path = argv[2];
        const char *out_path = argv[3];
        FILE *fin = fopen(in_path, "rb");
        if (!fin) {
            fprintf(stderr, "Cannot open input: %s\n", in_path);
            return 1;
        }
        fseek(fin, 0, SEEK_END);
        long slen = ftell(fin);
        fseek(fin, 0, SEEK_SET);
        uint8_t *src = (uint8_t*)malloc(slen);
        if (fread(src, 1, slen, fin) != (size_t)slen) {
            fclose(fin);
            free(src);
            return 1;
        }
        fclose(fin);

        int32_t u_size = przm_get_uncompressed_size(src, slen);
        if (u_size < 0) {
            fprintf(stderr, "Failed to get uncompressed size: %d\n", u_size);
            free(src);
            return 1;
        }
        uint8_t *dst = (uint8_t*)malloc((size_t)u_size + 64);
        int32_t res = przm_decompress(src, slen, dst, (size_t)u_size + 64);
        if (res < 0) {
            fprintf(stderr, "przm_decompress failed: %d\n", res);
            free(src);
            free(dst);
            return 1;
        }

        FILE *fout = fopen(out_path, "wb");
        if (!fout) {
            fprintf(stderr, "Cannot open output: %s\n", out_path);
            free(src);
            free(dst);
            return 1;
        }
        fwrite(dst, 1, (size_t)res, fout);
        fclose(fout);
        free(src);
        free(dst);
        return 0;
    }

    printf("=== Running Clean Room C99 Verification Suite ===\n\n");
    test_get_uncompressed_size();
    test_standard_fused_raw();
    test_standard_fused_lz();
    test_command_split_decompression();
    test_command_split_and_join_roundtrip();
    test_vans_meta_roundtrip();
    test_mode8_vans_frame_decompression();
    test_distance_to_code_and_back();
    test_vans_dist4_roundtrip();
    test_distance_prefix_frame_decompression();
    test_foliation_classification();
    test_foliation_roundtrip();
    test_nibble_planes();
    test_etale_sheaf_frame_decompression();
    test_dictionary_decompression();
    test_corrupted_and_truncated_frames();
    test_compress_roundtrip();
    printf("\n>>> ALL CLEAN ROOM C99 TESTS PASSED SUCCESSFULLY! <<<\n");
    return 0;
}
