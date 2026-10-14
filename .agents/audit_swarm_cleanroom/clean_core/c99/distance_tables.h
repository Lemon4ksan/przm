/*
 * Copyright (c) 2026 Lemon4ksan All rights reserved.
 * Use of this source code is governed by a BSD-style
 * license that can be found in the LICENSE file.
 */

#ifndef PRZM_DISTANCE_TABLES_H
#define PRZM_DISTANCE_TABLES_H

#include <stdint.h>

static const uint8_t przm_dist_extra_bits[49] = {
	0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13, 14, 14, 15, 15, 16, 16, 17, 17, 18, 18, 19, 19, 20, 20, 21, 21, 22, 22, 24
};

static const uint32_t przm_dist_base[49] = {
	1, 2, 3, 4, 5, 7, 9, 13, 17, 25,
	33, 49, 65, 97, 129, 193, 257, 385, 513, 769,
	1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577,
	32769, 49153, 65537, 98305, 131073, 196609, 262145, 393217, 524289, 786433,
	1048577, 1572865, 2097153, 3145729, 4194305, 6291457, 8388609, 12582913, 16777217
};

static const uint16_t przm_dist_static_freq4[4][64] = {
	{
		1U, 1U, 1U, 1U, 1U, 1U, 128U, 43U, 128U, 256U, 341U, 687U, 341U, 256U, 427U, 43U,
		256U, 43U, 128U, 1U, 43U, 43U, 85U, 85U, 85U, 85U, 85U, 85U, 171U, 1U, 85U, 43U,
		85U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
		1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
	},
	{
		2U, 1U, 1U, 1U, 1U, 1U, 83U, 61U, 183U, 145U, 236U, 214U, 291U, 248U, 250U, 186U,
		218U, 176U, 192U, 157U, 189U, 154U, 179U, 115U, 164U, 111U, 136U, 70U, 98U, 60U, 54U, 36U,
		31U, 18U, 5U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
		1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
	},
	{
		3U, 1U, 1U, 1U, 1U, 1U, 58U, 40U, 113U, 100U, 164U, 141U, 194U, 174U, 245U, 214U,
		232U, 188U, 226U, 208U, 245U, 189U, 245U, 166U, 217U, 145U, 144U, 87U, 102U, 68U, 52U, 34U,
		44U, 18U, 6U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
		1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
	},
	{
		2U, 1U, 1U, 1U, 1U, 1U, 39U, 38U, 86U, 79U, 163U, 124U, 167U, 143U, 187U, 151U,
		179U, 140U, 151U, 136U, 168U, 123U, 213U, 123U, 179U, 147U, 253U, 169U, 254U, 177U, 161U, 112U,
		112U, 60U, 25U, 2U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
		1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U, 1U,
	},
};

#endif /* PRZM_DISTANCE_TABLES_H */
