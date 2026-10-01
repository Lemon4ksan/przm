/*
 * test_m3_cross_codec.c
 * Empirical Challenger M3: Cross-Codec & Boundary Stress Test Harness
 * 
 * Tests C implementation independently and provides CLI interface
 * for Go <-> C cross-codec verification.
 * 
 * Zero dynamic heap allocations (0 malloc/free).
 */

#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <math.h>
#include "aes_distinguisher.h"

#define MAX_PAYLOAD_SIZE (2 * 1024 * 1024) /* 2 MiB static buffer */

static uint8_t s_raw_buf[MAX_PAYLOAD_SIZE];
static uint8_t s_comp_buf[MAX_PAYLOAD_SIZE + 65536];
static uint8_t s_decomp_buf[MAX_PAYLOAD_SIZE];

static int run_c_boundary_tests(void);
static int run_c_1mib_tests(void);
static int run_c_balance_ratio_tests(void);

static void generate_poc_payload_c(uint8_t *out, size_t num_blocks, int64_t seed, int balanced_chunks_target) {
    uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };
    key[0] ^= (uint8_t)(seed & 0xFF);
    key[15] ^= (uint8_t)((seed >> 8) & 0xFF);

    size_t num_chunks = num_blocks / PRZM_CRYPTO_CORE_CHUNK_BLOCKS;
    for (size_t c = 0; c < num_chunks; c++) {
        size_t chunk_start = c * PRZM_CRYPTO_CORE_CHUNK_BYTES;
        bool is_balanced = (int)(c % 256) < balanced_chunks_target;

        if (is_balanced) {
            /* Round 3 Lambda set */
            for (size_t i = 0; i < PRZM_CRYPTO_CORE_CHUNK_BLOCKS; i++) {
                uint8_t pt[16];
                pt[0] = (uint8_t)i;
                for (size_t j = 1; j < 16; j++) {
                    pt[j] = (uint8_t)(j * 17 + c * 3);
                }
                aes_reduced_encrypt(pt, key, 3, true, out + chunk_start + i * 16);
            }
        } else {
            /* Round 4 Counter */
            for (size_t i = 0; i < PRZM_CRYPTO_CORE_CHUNK_BLOCKS; i++) {
                size_t idx = chunk_start / 16 + i;
                uint8_t pt[16];
                pt[0] = (uint8_t)(idx & 0xFF);
                pt[1] = (uint8_t)((idx >> 8) & 0xFF);
                pt[2] = (uint8_t)((idx >> 16) & 0xFF);
                for (size_t j = 3; j < 16; j++) {
                    pt[j] = (uint8_t)(j * 11 + c);
                }
                aes_reduced_encrypt(pt, key, 4, true, out + chunk_start + i * 16);
            }
        }
    }

    /* Trailing blocks */
    for (size_t idx = num_chunks * PRZM_CRYPTO_CORE_CHUNK_BLOCKS; idx < num_blocks; idx++) {
        uint8_t pt[16];
        pt[0] = (uint8_t)(idx & 0xFF);
        pt[1] = (uint8_t)((idx >> 8) & 0xFF);
        for (size_t j = 2; j < 16; j++) pt[j] = (uint8_t)(j * 7);
        aes_reduced_encrypt(pt, key, 4, true, out + idx * 16);
    }
}

static int run_c_boundary_tests(void) {
    printf("[CHALLENGER_C] Running boundary test suite...\n");
    int failed = 0;

    /* 1. 0 bytes */
    {
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(NULL, 0, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != PRZM_CRYPTO_CORE_HEADER_SIZE) {
            printf("FAIL: 0 bytes compress status=%d, len=%zu\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != 0) {
            printf("FAIL: 0 bytes decompress status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 2. 15 bytes */
    {
        for (size_t i = 0; i < 15; i++) s_raw_buf[i] = (uint8_t)(i * 13 + 5);
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, 15, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != 15 + PRZM_CRYPTO_CORE_HEADER_SIZE) {
            printf("FAIL: 15 bytes compress status=%d, len=%zu\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != 15 || memcmp(s_raw_buf, s_decomp_buf, 15) != 0) {
            printf("FAIL: 15 bytes decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 3. 16 bytes (1 block) */
    {
        for (size_t i = 0; i < 16; i++) s_raw_buf[i] = (uint8_t)(i * 17 + 3);
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, 16, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != 16 + PRZM_CRYPTO_CORE_HEADER_SIZE) {
            printf("FAIL: 16 bytes compress status=%d, len=%zu\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != 16 || memcmp(s_raw_buf, s_decomp_buf, 16) != 0) {
            printf("FAIL: 16 bytes decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 4. 255 blocks (4080 bytes) */
    {
        size_t raw_len = 255 * 16;
        for (size_t i = 0; i < raw_len; i++) s_raw_buf[i] = (uint8_t)(i * 23 + 1);
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != raw_len + PRZM_CRYPTO_CORE_HEADER_SIZE) {
            printf("FAIL: 255 blocks compress status=%d, len=%zu\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL: 255 blocks decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 5. 256 blocks (4096 bytes) - Balanced */
    {
        generate_poc_payload_c(s_raw_buf, 256, 1234, 1); /* 1 balanced chunk */
        size_t raw_len = 256 * 16;
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != 4097) {
            printf("FAIL: 256 blocks balanced compress status=%d, len=%zu (expected 4097)\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL: 256 blocks balanced decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 6. 256 blocks (4096 bytes) - Unbalanced */
    {
        generate_poc_payload_c(s_raw_buf, 256, 1234, 0); /* 0 balanced chunks */
        size_t raw_len = 256 * 16;
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != raw_len + PRZM_CRYPTO_CORE_HEADER_SIZE) {
            printf("FAIL: 256 blocks unbalanced compress status=%d, len=%zu\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL: 256 blocks unbalanced decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 7. 257 blocks (4112 bytes) - Balanced */
    {
        generate_poc_payload_c(s_raw_buf, 257, 5678, 1);
        size_t raw_len = 257 * 16;
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0 || comp_len != 4113) {
            printf("FAIL: 257 blocks balanced compress status=%d, len=%zu (expected 4113)\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL: 257 blocks balanced decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    /* 8. 10,000 blocks (160,000 bytes) - Partially balanced (20 balanced chunks out of 39) */
    {
        generate_poc_payload_c(s_raw_buf, 10000, 9999, 20);
        size_t raw_len = 10000 * 16;
        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        /* Expected comp_len: 16 (hdr) + 5 (bitmap) + 20*4080 + 19*4096 + 256 (tail) = 21 + 81600 + 77824 + 256 = 159701 */
        if (status != 0 || comp_len != 159701) {
            printf("FAIL: 10,000 blocks compress status=%d, len=%zu (expected 159701)\n", status, comp_len);
            failed++;
        }
        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL: 10,000 blocks decompress mismatch status=%d, len=%zu\n", status, decomp_len);
            failed++;
        }
    }

    if (failed == 0) {
        printf("[CHALLENGER_C] Boundary tests PASSED (0 failures).\n");
    } else {
        printf("[CHALLENGER_C] Boundary tests FAILED with %d failures.\n", failed);
    }
    return failed;
}

static int run_c_1mib_tests(void) {
    printf("[CHALLENGER_C] Running 1 MiB savings across diverse seeds...\n");
    int failed = 0;
    const int64_t seeds[] = {0, 1, 42, 123, 777, 1024, 9999, 65535, 1234567, 987654321, 0xDEADBEEF, -1};
    size_t num_seeds = sizeof(seeds) / sizeof(seeds[0]);
    const size_t raw_len = 1048576; /* 1 MiB = 65,536 blocks */

    for (size_t s = 0; s < num_seeds; s++) {
        int64_t seed = seeds[s];
        generate_poc_payload_c(s_raw_buf, 65536, seed, 130); /* 130 balanced chunks */

        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0) {
            printf("FAIL [seed=%lld]: compress status=%d\n", (long long)seed, status);
            failed++;
            continue;
        }

        size_t bytes_saved = raw_len - comp_len;
        double pct = ((double)bytes_saved / (double)raw_len) * 100.0;

        if (bytes_saved < PRZM_THEORETICAL_MIN_SAVINGS_1MIB || bytes_saved > PRZM_THEORETICAL_MAX_SAVINGS_1MIB) {
            printf("FAIL [seed=%lld]: savings %zu outside [%d, %d]\n",
                (long long)seed, bytes_saved, PRZM_THEORETICAL_MIN_SAVINGS_1MIB, PRZM_THEORETICAL_MAX_SAVINGS_1MIB);
            failed++;
        }
        if (pct < PRZM_THEORETICAL_MIN_DEFICIT_PCT || pct > PRZM_THEORETICAL_MAX_DEFICIT_PCT) {
            printf("FAIL [seed=%lld]: pct %.4f%% outside [%.3f%%, %.3f%%]\n",
                (long long)seed, pct, PRZM_THEORETICAL_MIN_DEFICIT_PCT, PRZM_THEORETICAL_MAX_DEFICIT_PCT);
            failed++;
        }

        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL [seed=%lld]: lossless roundtrip byte mismatch!\n", (long long)seed);
            failed++;
        }
    }

    if (failed == 0) {
        printf("[CHALLENGER_C] 1 MiB diverse seed tests PASSED across %zu seeds.\n", num_seeds);
    } else {
        printf("[CHALLENGER_C] 1 MiB diverse seed tests FAILED with %d failures.\n", failed);
    }
    return failed;
}

static int run_c_balance_ratio_tests(void) {
    printf("[CHALLENGER_C] Running varied chunk balance ratio tests...\n");
    int failed = 0;
    const size_t raw_len = 1048576; /* 256 chunks */

    /* Test 0%, 25% (64), 50% (128), 75% (192), 100% (256) */
    const int targets[] = {0, 64, 105, 128, 130, 192, 201, 256};
    size_t num_targets = sizeof(targets) / sizeof(targets[0]);

    for (size_t t = 0; t < num_targets; t++) {
        int k = targets[t];
        generate_poc_payload_c(s_raw_buf, 65536, 42, k);

        size_t comp_len = 0, decomp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, raw_len, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0) {
            printf("FAIL [k=%d]: compress status=%d\n", k, status);
            failed++;
            continue;
        }

        status = aes_crypto_core_decompress(s_comp_buf, comp_len, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0 || decomp_len != raw_len || memcmp(s_raw_buf, s_decomp_buf, raw_len) != 0) {
            printf("FAIL [k=%d]: lossless roundtrip mismatch!\n", k);
            failed++;
            continue;
        }

        if (k == 0) {
            /* Passthrough mode: comp_len = raw_len + 16 */
            if (comp_len != raw_len + 16) {
                printf("FAIL [k=0]: expected raw passthrough 1048592, got %zu\n", comp_len);
                failed++;
            }
        } else {
            /* Mode 1: comp_len = 16 (hdr) + 32 (bitmap) + k*4080 + (256-k)*4096 = 1048576 + 48 - 16*k */
            size_t expected_comp = 1048576 + 48 - 16 * k;
            if (comp_len != expected_comp) {
                printf("FAIL [k=%d]: expected comp_len %zu, got %zu\n", k, expected_comp, comp_len);
                failed++;
            }
            int bytes_saved = (int)(raw_len - comp_len);
            bool in_bounds = (bytes_saved >= PRZM_THEORETICAL_MIN_SAVINGS_1MIB &&
                              bytes_saved <= PRZM_THEORETICAL_MAX_SAVINGS_1MIB);
            if (k >= 105 && k <= 201) {
                if (!in_bounds) {
                    printf("FAIL [k=%d]: savings %d should be in [%d, %d]\n",
                        k, bytes_saved, PRZM_THEORETICAL_MIN_SAVINGS_1MIB, PRZM_THEORETICAL_MAX_SAVINGS_1MIB);
                    failed++;
                }
            } else if (k < 105) {
                if (bytes_saved >= PRZM_THEORETICAL_MIN_SAVINGS_1MIB) {
                    printf("FAIL [k=%d]: savings %d should be below min\n", k, bytes_saved);
                    failed++;
                }
            } else if (k > 201) {
                if (bytes_saved <= PRZM_THEORETICAL_MAX_SAVINGS_1MIB) {
                    printf("FAIL [k=%d]: savings %d should be above max\n", k, bytes_saved);
                    failed++;
                }
            }
        }
    }

    if (failed == 0) {
        printf("[CHALLENGER_C] Varied chunk balance ratio tests PASSED (0 failures).\n");
    } else {
        printf("[CHALLENGER_C] Varied chunk balance ratio tests FAILED with %d failures.\n", failed);
    }
    return failed;
}

int main(int argc, char **argv) {
    if (argc >= 2 && strcmp(argv[1], "run_tests") == 0) {
        int failures = 0;
        failures += run_c_boundary_tests();
        failures += run_c_1mib_tests();
        failures += run_c_balance_ratio_tests();
        return failures == 0 ? 0 : 1;
    }

    if (argc >= 4 && strcmp(argv[1], "compress") == 0) {
        const char *in_path = argv[2];
        const char *out_path = argv[3];

        FILE *fin = fopen(in_path, "rb");
        if (!fin) { fprintf(stderr, "Cannot open %s\n", in_path); return 2; }
        size_t n = fread(s_raw_buf, 1, sizeof(s_raw_buf), fin);
        fclose(fin);

        size_t comp_len = 0;
        int status = aes_crypto_core_compress(s_raw_buf, n, s_comp_buf, sizeof(s_comp_buf), &comp_len);
        if (status != 0) { fprintf(stderr, "Compress failed: %d\n", status); return 3; }

        FILE *fout = fopen(out_path, "wb");
        if (!fout) { fprintf(stderr, "Cannot open %s\n", out_path); return 4; }
        if (comp_len > 0) fwrite(s_comp_buf, 1, comp_len, fout);
        fclose(fout);
        return 0;
    }

    if (argc >= 4 && strcmp(argv[1], "decompress") == 0) {
        const char *in_path = argv[2];
        const char *out_path = argv[3];

        FILE *fin = fopen(in_path, "rb");
        if (!fin) { fprintf(stderr, "Cannot open %s\n", in_path); return 2; }
        size_t n = fread(s_comp_buf, 1, sizeof(s_comp_buf), fin);
        fclose(fin);

        size_t decomp_len = 0;
        int status = aes_crypto_core_decompress(s_comp_buf, n, s_decomp_buf, sizeof(s_decomp_buf), &decomp_len);
        if (status != 0) { fprintf(stderr, "Decompress failed: %d\n", status); return 3; }

        FILE *fout = fopen(out_path, "wb");
        if (!fout) { fprintf(stderr, "Cannot open %s\n", out_path); return 4; }
        if (decomp_len > 0) fwrite(s_decomp_buf, 1, decomp_len, fout);
        fclose(fout);
        return 0;
    }

    printf("Usage:\n");
    printf("  %s run_tests\n", argv[0]);
    printf("  %s compress <infile> <outfile>\n", argv[0]);
    printf("  %s decompress <infile> <outfile>\n", argv[0]);
    return 0;
}
