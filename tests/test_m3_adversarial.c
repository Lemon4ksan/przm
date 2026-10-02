#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <assert.h>
#include "aes_distinguisher.h"

int main(void) {
    printf("[*] Starting M3 Adversarial Stress Testing...\n");

    /* 1. Stress test aes_evaluate_cascaded_deficit with boundary block counts */
    {
        aes_cascaded_deficit_report_t rep;
        uint8_t blocks[16][16];
        memset(blocks, 0x5A, sizeof(blocks));

        /* num_blocks = 0 */
        memset(&rep, 0xFF, sizeof(rep));
        aes_evaluate_cascaded_deficit(blocks, 0, &rep);
        assert(rep.block_count == 0);
        assert(rep.aggregated_deficit_pct == 0.0);

        /* num_blocks = 15 (< 16 threshold) */
        memset(&rep, 0xFF, sizeof(rep));
        aes_evaluate_cascaded_deficit(blocks, 15, &rep);
        assert(rep.block_count == 15);
        assert(rep.aggregated_deficit_pct == 0.0);

        /* num_blocks = 16 (minimum threshold) */
        memset(&rep, 0xFF, sizeof(rep));
        aes_evaluate_cascaded_deficit(blocks, 16, &rep);
        assert(rep.block_count == 16);
    }
    printf("[+] Passed boundary block counts in aes_evaluate_cascaded_deficit\n");

    /* 2. Stress test aes_cascaded_deficit_aggregate with extreme values */
    {
        aes_cascaded_deficit_report_t rep;
        double zero_vec[5] = {0.0, 0.0, 0.0, 0.0, 0.0};
        aes_cascaded_deficit_aggregate(zero_vec, &rep);
        assert(rep.aggregated_deficit_pct == 0.0);
        assert(!rep.within_theoretical_bounds);
        assert(rep.theoretical_savings_bytes_1mib == 0);

        double negative_vec[5] = {-0.01, -0.02, 0.0, 0.0, 0.0};
        aes_cascaded_deficit_aggregate(negative_vec, &rep);
        assert(rep.aggregated_deficit_pct < 0.0);
        assert(!rep.within_theoretical_bounds);

        double extreme_vec[5] = {10.0, 20.0, 30.0, 40.0, 50.0};
        aes_cascaded_deficit_aggregate(extreme_vec, &rep);
        assert(rep.aggregated_deficit_pct == 150.0);
        assert(!rep.within_theoretical_bounds);
    }
    printf("[+] Passed extreme values in aes_cascaded_deficit_aggregate\n");

    /* 3. Stress test unsupported flags and corruption in aes_crypto_core_decompress */
    {
        uint8_t raw[4096];
        uint8_t comp[4096 + 256];
        uint8_t decomp[4096 + 256];
        size_t comp_len = 0;
        size_t decomp_len = 0;
        int status;

        memset(raw, 0x42, sizeof(raw));
        status = aes_crypto_core_compress(raw, sizeof(raw), comp, sizeof(comp), &comp_len);
        assert(status == 0);

        /* Case 3a: Corrupt flags to 0x0002 */
        comp[12] = 0x02;
        comp[13] = 0x00;
        status = aes_crypto_core_decompress(comp, comp_len, decomp, sizeof(decomp), &decomp_len);
        assert(status == -6); /* -6: unsupported crypto mode */

        /* Case 3b: Corrupt flags to 0xFFFF */
        comp[12] = 0xFF;
        comp[13] = 0xFF;
        status = aes_crypto_core_decompress(comp, comp_len, decomp, sizeof(decomp), &decomp_len);
        assert(status == -6);

        /* Case 3c: Revert flags, truncate comp_len by 1 byte */
        comp[12] = 0x00; /* Raw mode */
        comp[13] = 0x00;
        status = aes_crypto_core_decompress(comp, comp_len - 1, decomp, sizeof(decomp), &decomp_len);
        assert(status == -1); /* Truncated */

        /* Case 3d: Insufficient dst_cap */
        status = aes_crypto_core_decompress(comp, comp_len, decomp, sizeof(raw) - 1, &decomp_len);
        assert(status == -4); /* Buffer too small */

        /* Case 3e: Adversarial header inconsistency (raw_sz = 100, num_chunks = 1 -> num_chunks * 4096 > raw_sz) */
        {
            uint8_t malformed[4097], dst_adv[4096];
            size_t out_len_adv = 0;
            memset(malformed, 0, sizeof(malformed));
            uint32_t magic = PRZM_MAGIC_CRYPTO_CORE;
            uint32_t raw_sz_adv = 100;
            uint32_t comp_sz_adv = 4097;
            uint16_t flags_adv = 1, chunks_adv = 1;
            memcpy(malformed, &magic, 4);
            memcpy(malformed + 4, &raw_sz_adv, 4);
            memcpy(malformed + 8, &comp_sz_adv, 4);
            memcpy(malformed + 12, &flags_adv, 2);
            memcpy(malformed + 14, &chunks_adv, 2);
            malformed[16] = 0x01; /* balanced chunk 0 */
            int ret_adv = aes_crypto_core_decompress(malformed, sizeof(malformed), dst_adv, sizeof(dst_adv), &out_len_adv);
            assert(ret_adv == -1);
        }
    }
    printf("[+] Passed corrupt flags, truncated buffers, and adversarial header underflow in decompress\n");

    /* 4. Stress test adversarial block64 microcode */
    {
        uint8_t in_buf[64];
        uint8_t out_buf[64];
        memset(in_buf, 0xAA, sizeof(in_buf));
        memset(out_buf, 0x55, sizeof(out_buf));

        /* Unbalanced */
        int r = aes_crypto_core_decompress_block64(in_buf, out_buf, false);
        assert(r == 64);
        assert(memcmp(out_buf, in_buf, 64) == 0);

        /* Balanced */
        r = aes_crypto_core_decompress_block64(in_buf, out_buf, true);
        assert(r == 48);
        /* Check reconstruction */
        uint64_t w0, w2, w4, r0;
        memcpy(&w0, in_buf, 8);
        memcpy(&w2, in_buf + 16, 8);
        memcpy(&w4, in_buf + 32, 8);
        r0 = w0 ^ w2 ^ w4;
        assert(memcmp(out_buf + 48, &r0, 8) == 0);
    }
    printf("[+] Passed adversarial block64 microcode\n");

    printf("[*] ALL ADVERSARIAL STRESS TESTS COMPLETED SUCCESSFULLY!\n");
    return 0;
}
