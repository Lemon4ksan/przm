#include "aes_distinguisher.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdbool.h>

typedef struct {
    int target_round;
    aes_tap_point_t target_point;
    uint8_t captured[16];
    bool hit;
} tap_ctx_t;

static void tap_callback(int round, aes_tap_point_t point, const uint8_t state[16], void *user_data) {
    tap_ctx_t *ctx = (tap_ctx_t*)user_data;
    if (ctx && round == ctx->target_round && point == ctx->target_point) {
        memcpy(ctx->captured, state, 16);
        ctx->hit = true;
    }
}

/* Simulated C wrapper for aes_reduced_encrypt_tap with final_mix parameter */
void aes_reduced_encrypt_tap_configurable(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    bool final_mix,
    aes_tap_point_t tap,
    uint8_t out[16]
) {
    tap_ctx_t ctx;
    memset(&ctx, 0, sizeof(ctx));
    ctx.target_round = (tap == AES_TAP_AFTER_ADDROUNDKEY0) ? 0 : rounds;
    ctx.target_point = tap;

    uint8_t cipher[16];
    aes_encrypt_block_with_tap(
        in, key, rounds, final_mix,
        AES_KEY_SCHEDULE_STANDARD, 0,
        tap_callback, &ctx,
        cipher
    );

    if (ctx.hit) {
        memcpy(out, ctx.captured, 16);
    } else {
        memset(out, 0, 16);
    }
}

/* Simulated C wrapper matching Go's EncryptWithTap (final_mix = false) */
void aes_reduced_encrypt_tap_nofinalmix(
    const uint8_t in[16],
    const uint8_t key[16],
    int rounds,
    aes_tap_point_t tap,
    uint8_t out[16]
) {
    aes_reduced_encrypt_tap_configurable(in, key, rounds, false, tap, out);
}

void print_hex(const char *label, const uint8_t *b, size_t len) {
    printf("%s: ", label);
    for (size_t i = 0; i < len; i++) printf("%02x", b[i]);
    printf("\n");
}

int main(void) {
    const uint8_t key[16] = {
        0x2b, 0x7e, 0x15, 0x16, 0x28, 0xae, 0xd2, 0xa6,
        0xab, 0xf7, 0x15, 0x88, 0x09, 0xcf, 0x4f, 0x3c
    };
    const uint8_t pt[16] = {
        0x32, 0x43, 0xf6, 0xa8, 0x88, 0x5a, 0x30, 0x8d,
        0x31, 0x31, 0x98, 0xa2, 0xe0, 0x37, 0x07, 0x34
    };

    printf("===================================================\n");
    printf("   C TAP & EDGE CASES EMPIRICAL VERIFICATION       \n");
    printf("===================================================\n");

    const char *tap_names[] = {
        "AfterAddRoundKey0",
        "AfterSubBytes",
        "AfterShiftRows",
        "AfterMixColumns",
        "AfterAddRoundKey"
    };

    printf("\n--- 1. Intermediate State Taps (Rounds 1 to 5, final_mix = false) ---\n");
    for (int r = 1; r <= 5; r++) {
        for (int t = 0; t < 5; t++) {
            uint8_t out[16];
            aes_reduced_encrypt_tap_nofinalmix(pt, key, r, (aes_tap_point_t)t, out);
            char lbl[64];
            snprintf(lbl, sizeof(lbl), "C [final_mix=false] R=%d Tap=%s", r, tap_names[t]);
            print_hex(lbl, out, 16);
        }
    }

    printf("\n--- 2. Intermediate State Taps (Rounds 1 to 5, final_mix = true) ---\n");
    for (int r = 1; r <= 5; r++) {
        for (int t = 0; t < 5; t++) {
            uint8_t out[16];
            aes_reduced_encrypt_tap_configurable(pt, key, r, true, (aes_tap_point_t)t, out);
            char lbl[64];
            snprintf(lbl, sizeof(lbl), "C [final_mix=true ] R=%d Tap=%s", r, tap_names[t]);
            print_hex(lbl, out, 16);
        }
    }

    printf("\n--- 3. Edge Round Counts in C ---\n");
    uint8_t out_r0_no_mc[16], out_r0_with_mc[16];
    uint8_t out_r1_no_mc[16], out_r1_with_mc[16];
    uint8_t out_r10_no_mc[16], out_r10_with_mc[16];

    aes_reduced_encrypt(pt, key, 0, false, out_r0_no_mc);
    aes_reduced_encrypt(pt, key, 0, true, out_r0_with_mc);
    aes_reduced_encrypt(pt, key, 1, false, out_r1_no_mc);
    aes_reduced_encrypt(pt, key, 1, true, out_r1_with_mc);
    aes_reduced_encrypt(pt, key, 10, false, out_r10_no_mc);
    aes_reduced_encrypt(pt, key, 10, true, out_r10_with_mc);

    print_hex("Round 0 (final_mix=false)", out_r0_no_mc, 16);
    print_hex("Round 0 (final_mix=true )", out_r0_with_mc, 16);
    print_hex("Round 1 (final_mix=false)", out_r1_no_mc, 16);
    print_hex("Round 1 (final_mix=true )", out_r1_with_mc, 16);
    print_hex("Round 10 (final_mix=false)", out_r10_no_mc, 16);
    print_hex("Round 10 (final_mix=true )", out_r10_with_mc, 16);

    printf("Check R=0 vs R=1: %s\n", (memcmp(out_r0_no_mc, out_r1_no_mc, 16) == 0) ? "IDENTICAL (R=0 clamped to R=1)" : "DIFFERENT");

    return 0;
}
