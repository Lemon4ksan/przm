#include <stdio.h>
#include <time.h>
#include <stdint.h>
#include <string.h>

#include "przm_dec.h"

#define ITERS 10000000

int main() {
    uint8_t orig[16] = {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15};
    uint8_t fwd[16];
    memcpy(fwd, orig, 16);
    for (int s = 1; s < 16; s++) {
        for (int i = 15; i >= s; i--) {
            fwd[i] = (uint8_t)(fwd[i] - fwd[i - 1]);
        }
    }

    uint8_t frame[28];
    frame[0] = 0x4C; frame[1] = 0x41; frame[2] = 0x48; frame[3] = 0x4D; // MAHL
    frame[4] = 1; // Mode16
    frame[5] = 0; frame[6] = 0; frame[7] = 0;
    frame[8] = 16; frame[9] = 0; frame[10] = 0; frame[11] = 0;
    memcpy(frame + 12, fwd, 16);

    uint8_t dst[128];
    
    clock_t start = clock();
    for (int i = 0; i < ITERS; i++) {
        przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    }
    clock_t end = clock();
    
    double time_spent = (double)(end - start) / CLOCKS_PER_SEC;
    printf("C Mahler Transform (10M iters): %f seconds\n", time_spent);
    return 0;
}
