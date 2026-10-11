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
    uint32_t x0 = x[0];
    uint32_t x1 = x[1];
    uint32_t x2 = x[2];
    uint32_t x3 = x[3];

    intptr_t i = 0;
    intptr_t limit4 = origLen - 3;
    intptr_t safeStreamLimit = srcLen - 8;

    while (i < limit4 && streamPtr < safeStreamLimit) {
        uint32_t e0 = decodeTable[x0 & 4095];
        uint32_t e1 = decodeTable[x1 & 4095];
        uint32_t e2 = decodeTable[x2 & 4095];
        uint32_t e3 = decodeTable[x3 & 4095];

        dst[i + 0] = (uint8_t)e0;
        dst[i + 1] = (uint8_t)e1;
        dst[i + 2] = (uint8_t)e2;
        dst[i + 3] = (uint8_t)e3;

        x0 = ((e0 >> 20) + 1) * (x0 >> 12) + ((e0 >> 8) & 0xFFF);
        x1 = ((e1 >> 20) + 1) * (x1 >> 12) + ((e1 >> 8) & 0xFFF);
        x2 = ((e2 >> 20) + 1) * (x2 >> 12) + ((e2 >> 8) & 0xFFF);
        x3 = ((e3 >> 20) + 1) * (x3 >> 12) + ((e3 >> 8) & 0xFFF);

        if (x0 < 65536) {
            x0 = (x0 << 8) | src[streamPtr++];
            if (x0 < 65536) x0 = (x0 << 8) | src[streamPtr++];
        }
        if (x1 < 65536) {
            x1 = (x1 << 8) | src[streamPtr++];
            if (x1 < 65536) x1 = (x1 << 8) | src[streamPtr++];
        }
        if (x2 < 65536) {
            x2 = (x2 << 8) | src[streamPtr++];
            if (x2 < 65536) x2 = (x2 << 8) | src[streamPtr++];
        }
        if (x3 < 65536) {
            x3 = (x3 << 8) | src[streamPtr++];
            if (x3 < 65536) x3 = (x3 << 8) | src[streamPtr++];
        }

        i += 4;
    }

    x[0] = x0;
    x[1] = x1;
    x[2] = x2;
    x[3] = x3;

    for (; i < origLen; i++) {
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
                return streamPtr + 999999999;
            }
            x_lane = (x_lane << 8) | src[streamPtr++];
        }

        x[lane] = x_lane;
    }

    return streamPtr;
}
