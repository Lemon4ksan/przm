#include <stdint.h>

intptr_t decode_vans4x(
    uint8_t *dst,
    const uint8_t *src,
    intptr_t streamPtr,
    intptr_t origLen,
    const uint32_t *decodeTable,
    uint32_t *x,
    intptr_t srcLen
) {
    for (intptr_t i = 0; i < origLen; i++) {
        intptr_t lane = i & 3;
        uint32_t x_lane = x[lane];
        
        uint32_t slot = x_lane & 4095;
        uint32_t entry = decodeTable[slot];
        
        dst[i] = (uint8_t)entry;
        
        uint32_t slot_minus_c = (entry >> 8) & 0xFFF;
        uint32_t f = (entry >> 20) + 1;
        
        x_lane = f * (x_lane >> 12) + slot_minus_c;
        
        while (x_lane < 65536) {
            if (streamPtr >= srcLen) {
                return streamPtr + 999999999; // Force error in Go
            }
            x_lane = (x_lane << 8) | src[streamPtr++];
        }
        
        x[lane] = x_lane;
    }
    return streamPtr;
}
