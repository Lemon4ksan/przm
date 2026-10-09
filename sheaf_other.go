// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !amd64 || purego

package przm

func simdSplit(src, sBuf, cBuf, maskBuf []byte) (int, int) {
	return branchlessSplit(src, &SheafSyntaxTable, sBuf, cBuf, maskBuf)
}

func simdInterleave(dst, s, c, mask []byte, origLen int) {
	branchlessInterleave(dst, s, c, mask, origLen)
}
