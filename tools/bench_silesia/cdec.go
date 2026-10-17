// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

/*
#cgo CFLAGS: -O3 -I../../include
#cgo LDFLAGS: -L${SRCDIR}/../.. -l:libprzm_dec.a
#include "przm_dec.h"
*/
import "C"
import (
	"fmt"
	"unsafe"
)

func decompressC(src, dst []byte) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	res := C.przm_decompress(
		(*C.uint8_t)(unsafe.Pointer(&src[0])),
		C.size_t(len(src)),
		(*C.uint8_t)(unsafe.Pointer(&dst[0])),
		C.size_t(len(dst)),
	)
	if res < 0 {
		return int(res), fmt.Errorf("c decompress error: %d", int(res))
	}
	return int(res), nil
}
