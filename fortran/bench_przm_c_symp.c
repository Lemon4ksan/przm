#include <stdio.h>
#include <time.h>
#include <stdint.h>
#include <string.h>

#include "przm_dec.h"

#define ITERS 10000000

int main() {
    uint8_t orig[16] = {0x12, 0x34, 0x56, 0x78, 0xAB, 0xCD, 0xEF, 0x01, 0x42, 0x69, 0x99, 0xAA, 0xFF, 0x00, 0x55, 0xAA};

    uint8_t frame[28];
    frame[0] = 0x50; frame[1] = 0x4D; frame[2] = 0x59; frame[3] = 0x53; // SYMP
    frame[4] = 1; // Shear mode
    frame[5] = 0x05; frame[6] = 0x0A; frame[7] = 0x1F;
    frame[8] = 16; frame[9] = 0; frame[10] = 0; frame[11] = 0;
    memcpy(frame + 12, orig, 16);

    uint8_t dst[128];
    
    clock_t start = clock();
    for (int i = 0; i < ITERS; i++) {
        przm_decompress(frame, sizeof(frame), dst, sizeof(dst));
    }
    clock_t end = clock();
    
    double time_spent = (double)(end - start) / CLOCKS_PER_SEC;
    printf("C Symplectic Transform (10M iters): %f seconds\n", time_spent);
    return 0;
}
