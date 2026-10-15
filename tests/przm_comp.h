/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef PRZM_COMP_H
#define PRZM_COMP_H

#include <stdint.h>
#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

int32_t przm_compress(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap, int level);
int32_t przm_compress_with_dict(const uint8_t *src, size_t src_len, uint8_t *dst, size_t dst_cap,
                                int level, const uint8_t *dict, size_t dict_len);

#ifdef __cplusplus
}
#endif

#endif /* PRZM_COMP_H */
