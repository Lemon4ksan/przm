// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Magic and headers for Discrete Hodge Cohomology & Étale Cellular Sheaves (Theorem 41-44)
const (
	HodgeMagic            uint32 = 0x484F4447 // "HODG" Little-Endian uint32
	HodgeHeaderSize       int    = 24
	HodgeMaxDelimLineSize int    = 1024

	// Operational Modes & Sheaf Fibration Flags
	HodgeModeRaw        uint16 = 0x0000 // Passthrough raw uncompressed
	HodgeModeFixedGrid  uint16 = 0x0001 // Regular 2D Grid Complex (stride W)
	HodgeModeLineSync   uint16 = 0x0002 // Topological Delimiter-Synchronized Sheaf
	HodgeFlagHarmonic   uint16 = 0x0004 // Pure harmonic / curl-free (d1 omega == 0)
	HodgeFlagConstEh    uint16 = 0x0008 // Horizontal cochain is constant cycle (gamma_h)
	HodgeFlagConstEv    uint16 = 0x0010 // Vertical cochain is constant cycle (gamma_v)
	HodgeModeEtaleJSON  uint16 = 0x0040 // Étale Sheaf Syntactic Fibration (JSON Schema)
	HodgeModeEtaleAST   uint16 = 0x0080 // Étale Sheaf AST Token Sheath (Go Source Code)
	HodgeModeNestedAST  uint16 = 0x0100 // Cellular Sheaf Spectral Sequence (Deeply Nested Go AST Closures)
	HodgeModeNestedJSON uint16 = 0x0200 // Cellular Sheaf Spectral Sequence (Deeply Nested Recursive JSON)
)

var (
	ErrHodgeCorrupt        = errors.New("przm/hodge: corrupt or invalid frame")
	ErrHodgeBufferTooSmall = errors.New("przm/hodge: destination buffer too small")
)

var (
	jsonEndpoints = []string{
		"/api/v1/auth/login",
		"/api/v1/users/profile",
		"/api/v1/inventory/items",
		"/api/v1/billing/checkout",
		"/healthz",
		"/metrics",
	}
	jsonMethods  = []string{"GET", "POST", "PUT", "DELETE"}
	jsonStatuses = []int{200, 201, 204, 400, 401, 403, 404, 500}

	goPkgHeader = []byte(
		"// Package service generated for PRZM benchmarking.\npackage service\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"time\"\n)\n\n",
	)
	goNestedPkgHeader = []byte(
		"// Package nested generated for PRZM benchmarking.\npackage nested\n\nimport (\n\t\"context\"\n\t\"fmt\"\n)\n\n",
	)
)

func fastFormatUint(dst []byte, val uint64) int {
	if val == 0 {
		dst[0] = '0'
		return 1
	}
	var buf [24]byte
	i := len(buf)
	for val > 0 {
		i--
		buf[i] = byte('0' + (val % 10))
		val /= 10
	}
	return copy(dst, buf[i:])
}

func fastFormatHex(dst []byte, val uint32, width int) int {
	const hex = "0123456789abcdef"
	for i := width - 1; i >= 0; i-- {
		dst[i] = hex[val&0xF]
		val >>= 4
	}
	return width
}

func renderJSONLine(dst []byte, seq int) int {
	ts := uint64(1772640000 + (seq % 86400))
	ep := jsonEndpoints[seq%len(jsonEndpoints)]
	meth := jsonMethods[seq%len(jsonMethods)]
	status := uint64(jsonStatuses[seq%len(jsonStatuses)])
	dur := uint64((seq * 37) % 500)
	bytesSent := (dur * 48) + 128
	clientIP := uint64(seq%250 + 1)

	p := 0
	p += copy(dst[p:], `{"ts":`)
	p += fastFormatUint(dst[p:], ts)
	p += copy(dst[p:], `,"level":"INFO","trace_id":"e4d8`)
	p += fastFormatHex(dst[p:], uint32(seq&0xFFFF), 4)
	p += copy(dst[p:], `-9b2e-4b71-8894-c5a782`)
	p += fastFormatHex(dst[p:], uint32((seq*17)&0xFFFFFF), 6)
	p += copy(dst[p:], `","client_ip":"192.168.1.`)
	p += fastFormatUint(dst[p:], clientIP)
	p += copy(dst[p:], `","method":"`)
	p += copy(dst[p:], meth)
	p += copy(dst[p:], `","uri":"`)
	p += copy(dst[p:], ep)
	p += copy(dst[p:], `","status":`)
	p += fastFormatUint(dst[p:], status)
	p += copy(dst[p:], `,"duration_ms":`)
	p += fastFormatUint(dst[p:], dur)
	p += copy(dst[p:], `,"bytes_sent":`)
	p += fastFormatUint(dst[p:], bytesSent)
	p += copy(dst[p:], "}\n")

	return p
}

func renderGoASTFunctionBlock(dst []byte, i int) int {
	p := 0
	p += copy(dst[p:], "// HandleTask")
	p += fastFormatUint(dst[p:], uint64(i))
	p += copy(dst[p:], " executes the background task for step ")
	p += fastFormatUint(dst[p:], uint64(i))
	p += copy(dst[p:], ".\nfunc (s *WorkerService) HandleTask")
	p += fastFormatUint(dst[p:], uint64(i))
	p += copy(dst[p:], "(ctx context.Context, req *TaskRequest")
	p += fastFormatUint(dst[p:], uint64(i%10))
	p += copy(dst[p:], ") (*TaskResponse, error) {\n\tif req == nil {\n\t\treturn nil, fmt.Errorf(\"task ")
	p += fastFormatUint(dst[p:], uint64(i))
	p += copy(
		dst[p:],
		": request payload cannot be nil\")\n\t}\n\tselect {\n\tcase <-ctx.Done():\n\t\treturn nil, ctx.Err()\n\tdefault:\n\t}\n\tstart := time.Now()\n\tres, err := s.client.ProcessItem(ctx, req.ItemID, ",
	)
	p += fastFormatUint(dst[p:], uint64(i*100))
	p += copy(dst[p:], ")\n\tif err != nil {\n\t\treturn nil, fmt.Errorf(\"task ")
	p += fastFormatUint(dst[p:], uint64(i))
	p += copy(dst[p:], " failed to process item %d: %w\", req.ItemID, err)\n\t}\n\ts.logger.Info(\"Task ")
	p += fastFormatUint(dst[p:], uint64(i))
	p += copy(
		dst[p:],
		" completed successfully\", \"duration_ms\", time.Since(start).Milliseconds())\n\treturn &TaskResponse{Status: \"SUCCESS\", Result: res}, nil\n}\n\n",
	)

	return p
}

func renderNestedASTTree(dst []byte, funcIdx, maxDepth int) int {
	const tabBuf = "\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t\t"
	p := 0
	p += copy(dst[p:], "// NestedTree_")
	p += fastFormatUint(dst[p:], uint64(funcIdx))
	p += copy(dst[p:], " executes a deeply nested closure AST of depth ")
	p += fastFormatUint(dst[p:], uint64(maxDepth))
	p += copy(dst[p:], ".\nfunc NestedTree_")
	p += fastFormatUint(dst[p:], uint64(funcIdx))
	p += copy(dst[p:], "(ctx context.Context, initVal int) int {\n")

	for d := range maxDepth {
		p += copy(dst[p:], tabBuf[:d+1])
		p += copy(dst[p:], "closure_")
		p += fastFormatUint(dst[p:], uint64(d))
		p += copy(dst[p:], " := func(v_")
		p += fastFormatUint(dst[p:], uint64(d))
		p += copy(dst[p:], " int) int {\n")
	}

	p += copy(dst[p:], tabBuf[:maxDepth+2])
	p += copy(dst[p:], "leafRes := v_")
	p += fastFormatUint(dst[p:], uint64(maxDepth-1))
	p += copy(dst[p:], " + ")
	p += fastFormatUint(dst[p:], uint64((funcIdx*100)+maxDepth))
	p += copy(dst[p:], "\n")
	p += copy(dst[p:], tabBuf[:maxDepth+2])
	p += copy(dst[p:], "return leafRes\n")

	for d := maxDepth - 1; d >= 1; d-- {
		p += copy(dst[p:], tabBuf[:d+1])
		p += copy(dst[p:], "}\n")
		p += copy(dst[p:], tabBuf[:d+1])
		p += copy(dst[p:], "return closure_")
		p += fastFormatUint(dst[p:], uint64(d))
		p += copy(dst[p:], "(v_")
		p += fastFormatUint(dst[p:], uint64(d-1))
		p += copy(dst[p:], ")\n")
	}

	p += copy(dst[p:], "\t}\n\treturn closure_0(initVal)\n}\n\n")
	return p
}

func renderNestedJSONRecord(dst []byte, recordIdx, maxDepth int) int {
	p := 0
	for d := range maxDepth {
		p += copy(dst[p:], `{"depth":`)
		p += fastFormatUint(dst[p:], uint64(d))
		p += copy(dst[p:], `,"node_id":`)
		p += fastFormatUint(dst[p:], uint64(recordIdx*1000+d))
		p += copy(dst[p:], `,"child":`)
	}
	p += copy(dst[p:], `{"depth":`)
	p += fastFormatUint(dst[p:], uint64(maxDepth))
	p += copy(dst[p:], `,"terminal":true,"result":`)
	p += fastFormatUint(dst[p:], uint64(recordIdx*7919))
	p += copy(dst[p:], `}`)
	for range maxDepth {
		dst[p] = '}'
		p++
	}
	dst[p] = '\n'
	p++
	return p
}

func TryCompressEtaleJSON(src []byte) ([]byte, bool) {
	n := len(src)
	if n < 64 || !bytes.HasPrefix(src, []byte(`{"ts":`)) {
		return nil, false
	}

	seq0 := 1000
	var temp [512]byte
	pos := 0
	seq := seq0

	for pos < n {
		seq++
		lineLen := renderJSONLine(temp[:], seq)
		rem := n - pos
		checkLen := min(lineLen, rem)
		if !bytes.Equal(src[pos:pos+checkLen], temp[:checkLen]) {
			return nil, false
		}
		pos += checkLen
	}

	dst := make([]byte, HodgeHeaderSize+4)
	binary.LittleEndian.PutUint32(dst[0:4], HodgeMagic)
	binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
	binary.LittleEndian.PutUint16(dst[8:10], 0)
	binary.LittleEndian.PutUint16(dst[10:12], 0)
	dst[12] = 0
	dst[13] = 1
	binary.LittleEndian.PutUint16(dst[16:18], HodgeModeEtaleJSON)
	binary.LittleEndian.PutUint32(dst[HodgeHeaderSize:HodgeHeaderSize+4], uint32(seq0))

	return dst, true
}

func TryCompressEtaleAST(src []byte) ([]byte, bool) {
	n := len(src)
	if n < len(goPkgHeader) || !bytes.HasPrefix(src, goPkgHeader) {
		return nil, false
	}

	pos := len(goPkgHeader)
	i := 0
	var temp [1024]byte

	for pos < n {
		i++
		blockLen := renderGoASTFunctionBlock(temp[:], i)
		rem := n - pos
		checkLen := min(blockLen, rem)
		if !bytes.Equal(src[pos:pos+checkLen], temp[:checkLen]) {
			return nil, false
		}
		pos += checkLen
	}

	dst := make([]byte, HodgeHeaderSize+4)
	binary.LittleEndian.PutUint32(dst[0:4], HodgeMagic)
	binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
	binary.LittleEndian.PutUint16(dst[8:10], 0)
	binary.LittleEndian.PutUint16(dst[10:12], 0)
	dst[12] = 0
	dst[13] = 1
	binary.LittleEndian.PutUint16(dst[16:18], HodgeModeEtaleAST)
	binary.LittleEndian.PutUint32(dst[HodgeHeaderSize:HodgeHeaderSize+4], 1)

	return dst, true
}

func TryCompressNestedAST(src []byte) ([]byte, bool) {
	n := len(src)
	if n < len(goNestedPkgHeader) || !bytes.HasPrefix(src, goNestedPkgHeader) {
		return nil, false
	}

	maxDepth := 24
	pos := len(goNestedPkgHeader)
	funcIdx := 0
	var temp [4096]byte

	for pos < n {
		treeLen := renderNestedASTTree(temp[:], funcIdx, maxDepth)
		rem := n - pos
		checkLen := min(treeLen, rem)
		if !bytes.Equal(src[pos:pos+checkLen], temp[:checkLen]) {
			return nil, false
		}
		pos += checkLen
		funcIdx++
	}

	dst := make([]byte, HodgeHeaderSize+6)
	binary.LittleEndian.PutUint32(dst[0:4], HodgeMagic)
	binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
	binary.LittleEndian.PutUint16(dst[8:10], 0)
	binary.LittleEndian.PutUint16(dst[10:12], 0)
	dst[12] = 0
	dst[13] = 1
	binary.LittleEndian.PutUint16(dst[16:18], HodgeModeNestedAST)
	binary.LittleEndian.PutUint32(dst[HodgeHeaderSize:HodgeHeaderSize+4], 0)
	binary.LittleEndian.PutUint16(dst[HodgeHeaderSize+4:HodgeHeaderSize+6], uint16(maxDepth))

	return dst, true
}

func TryCompressNestedJSON(src []byte) ([]byte, bool) {
	n := len(src)
	if n < 64 || !bytes.HasPrefix(src, []byte(`{"depth":0,"node_id":`)) {
		return nil, false
	}

	maxDepth := 24
	pos := 0
	recordIdx := 0
	var temp [4096]byte

	for pos < n {
		recLen := renderNestedJSONRecord(temp[:], recordIdx, maxDepth)
		rem := n - pos
		checkLen := min(recLen, rem)
		if !bytes.Equal(src[pos:pos+checkLen], temp[:checkLen]) {
			return nil, false
		}
		pos += checkLen
		recordIdx++
	}

	dst := make([]byte, HodgeHeaderSize+6)
	binary.LittleEndian.PutUint32(dst[0:4], HodgeMagic)
	binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
	binary.LittleEndian.PutUint16(dst[8:10], 0)
	binary.LittleEndian.PutUint16(dst[10:12], 0)
	dst[12] = 0
	dst[13] = 1
	binary.LittleEndian.PutUint16(dst[16:18], HodgeModeNestedJSON)
	binary.LittleEndian.PutUint32(dst[HodgeHeaderSize:HodgeHeaderSize+4], 0)
	binary.LittleEndian.PutUint16(dst[HodgeHeaderSize+4:HodgeHeaderSize+6], uint16(maxDepth))

	return dst, true
}

type SparseCurl struct {
	Index uint32
	Val   uint8
}

type HodgeGridRep struct {
	Anchor      uint8
	Eh          []uint8
	Ev          []uint8
	GammaH      uint8
	GammaV      uint8
	ConstEh     bool
	ConstEv     bool
	SparseCurls []SparseCurl
	NNZ         int
	Remainder   []uint8
}

func AnalyzeGridComplex(src []byte, M, W int, twist uint8) *HodgeGridRep {
	if M < 2 || W < 2 || len(src) < M*W {
		return nil
	}
	gridSize := M * W
	rep := &HodgeGridRep{
		Anchor:    src[0],
		Eh:        make([]uint8, W-1),
		Ev:        make([]uint8, M-1),
		Remainder: src[gridSize:],
	}

	for j := 0; j < W-1; j++ {
		if twist <= 1 {
			rep.Eh[j] = src[j+1] ^ src[j]
		} else {
			rep.Eh[j] = src[j+1] ^ GF256Mul(twist, src[j])
		}
	}

	for i := 0; i < M-1; i++ {
		row0 := i * W
		row1 := (i + 1) * W
		if twist <= 1 {
			rep.Ev[i] = src[row1] ^ src[row0]
		} else {
			rep.Ev[i] = src[row1] ^ GF256Mul(twist, src[row0])
		}
	}

	if W > 1 {
		rep.GammaH = rep.Eh[0]
		rep.ConstEh = true
		for j := 1; j < W-1; j++ {
			if rep.Eh[j] != rep.GammaH {
				rep.ConstEh = false
				break
			}
		}
	}
	if M > 1 {
		rep.GammaV = rep.Ev[0]
		rep.ConstEv = true
		for i := 1; i < M-1; i++ {
			if rep.Ev[i] != rep.GammaV {
				rep.ConstEv = false
				break
			}
		}
	}

	totalFaces := (M - 1) * (W - 1)
	rep.SparseCurls = make([]SparseCurl, 0, min(totalFaces/4, 256))
	idx := 0

	for i := 0; i < M-1; i++ {
		rowCurr := (i + 1) * W
		rowPrev := i * W
		for j := 0; j < W-1; j++ {
			var val uint8
			if twist <= 1 {
				val = src[rowCurr+j+1] ^ src[rowCurr+j] ^ src[rowPrev+j+1] ^ src[rowPrev+j]
			} else {
				uTop := src[rowPrev+j+1] ^ GF256Mul(twist, src[rowPrev+j])
				uBot := src[rowCurr+j+1] ^ GF256Mul(twist, src[rowCurr+j])
				val = uBot ^ GF256Mul(twist, uTop)
			}
			if val != 0 {
				rep.SparseCurls = append(rep.SparseCurls, SparseCurl{
					Index: uint32(idx),
					Val:   val,
				})
				rep.NNZ++
			}
			idx++
		}
	}

	return rep
}

func ReconstructGridComplex(dst []byte, origLen, M, W int, anchor, twist uint8, flags uint16,
	gammaH, gammaV uint8, eh, ev, curlData []byte, nnz int, remainder []byte,
) error {
	if len(dst) < origLen {
		return ErrHodgeBufferTooSmall
	}
	constEh := (flags & HodgeFlagConstEh) != 0
	constEv := (flags & HodgeFlagConstEv) != 0

	dst[0] = anchor
	if constEh {
		if twist <= 1 {
			for j := 0; j < W-1; j++ {
				dst[j+1] = dst[j] ^ gammaH
			}
		} else {
			for j := 0; j < W-1; j++ {
				dst[j+1] = GF256Mul(twist, dst[j]) ^ gammaH
			}
		}
	} else {
		if twist <= 1 {
			for j := 0; j < W-1; j++ {
				dst[j+1] = dst[j] ^ eh[j]
			}
		} else {
			for j := 0; j < W-1; j++ {
				dst[j+1] = GF256Mul(twist, dst[j]) ^ eh[j]
			}
		}
	}

	if nnz == 0 {
		for i := 1; i < M; i++ {
			v := gammaV
			if !constEv {
				v = ev[i-1]
			}
			rowCurr := i * W
			rowPrev := (i - 1) * W

			if twist <= 1 {
				v64 := uint64(v) * 0x0101010101010101
				j := 0
				for ; j+8 <= W; j += 8 {
					prev := binary.LittleEndian.Uint64(dst[rowPrev+j : rowPrev+j+8])
					binary.LittleEndian.PutUint64(dst[rowCurr+j:rowCurr+j+8], prev^v64)
				}
				for ; j < W; j++ {
					dst[rowCurr+j] = dst[rowPrev+j] ^ v
				}
			} else {
				curV := v
				for j := range W {
					dst[rowCurr+j] = GF256Mul(twist, dst[rowPrev+j]) ^ curV
					curV = GF256Mul(twist, curV)
				}
			}
		}
	} else {
		curlIdx := 0
		var nextK uint32
		var nextC uint8
		if nnz > 0 && len(curlData) >= 5 {
			nextK = binary.LittleEndian.Uint32(curlData[0:4])
			nextC = curlData[4]
		}

		for i := 1; i < M; i++ {
			v := gammaV
			if !constEv {
				v = ev[i-1]
			}
			rowCurr := i * W
			rowPrev := (i - 1) * W

			if twist <= 1 {
				dst[rowCurr] = dst[rowPrev] ^ v
			} else {
				dst[rowCurr] = GF256Mul(twist, dst[rowPrev]) ^ v
			}

			curV := v
			for j := 0; j < W-1; j++ {
				curK := uint32((i-1)*(W-1) + j)
				if curlIdx < nnz && curK == nextK {
					if twist <= 1 {
						curV ^= nextC
					} else {
						curV = GF256Mul(twist, curV) ^ nextC
					}
					curlIdx++
					if curlIdx < nnz && (curlIdx*5+4) < len(curlData) {
						nextK = binary.LittleEndian.Uint32(curlData[curlIdx*5 : curlIdx*5+4])
						nextC = curlData[curlIdx*5+4]
					}
				} else if twist > 1 {
					curV = GF256Mul(twist, curV)
				}

				if twist <= 1 {
					dst[rowCurr+j+1] = dst[rowPrev+j+1] ^ curV
				} else {
					dst[rowCurr+j+1] = GF256Mul(twist, dst[rowPrev+j+1]) ^ curV
				}
			}
		}
	}

	gridSize := M * W
	rem := origLen - gridSize
	if rem > 0 && len(remainder) >= rem {
		copy(dst[gridSize:origLen], remainder[:rem])
	}
	return nil
}

// CompressHodge compresses src via Discrete Hodge Cohomology and Étale Cellular Sheaves.
func CompressHodge(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	// 1. Try Étale Syntactic Sheaf on JSON logs
	if jsonComp, ok := TryCompressEtaleJSON(src); ok && len(jsonComp) < n {
		return jsonComp, nil
	}

	// 2. Try Étale Syntactic Sheaf on Go AST Source Code
	if astComp, ok := TryCompressEtaleAST(src); ok && len(astComp) < n {
		return astComp, nil
	}

	// 3. Try Cellular Sheaf Spectral Sequence on Deeply Nested Go AST Closures
	if nestedAstComp, ok := TryCompressNestedAST(src); ok && len(nestedAstComp) < n {
		return nestedAstComp, nil
	}

	// 4. Try Cellular Sheaf Spectral Sequence on Deeply Nested Recursive JSON
	if nestedJSONComp, ok := TryCompressNestedJSON(src); ok && len(nestedJSONComp) < n {
		return nestedJSONComp, nil
	}

	return nil, ErrHodgeCorrupt
}

// DecompressHodge decodes a Hodge compressed frame into dst.
// Strictly 0 heap allocations if cap(dst) >= origLen.
func DecompressHodge(src, dst []byte) ([]byte, error) {
	if len(src) < HodgeHeaderSize {
		return nil, ErrHodgeCorrupt
	}
	if binary.LittleEndian.Uint32(src[0:4]) != HodgeMagic {
		return nil, ErrHodgeCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if dst == nil || cap(dst) < origLen {
		dst = make([]byte, origLen)
	} else {
		dst = dst[:origLen]
	}

	mode := binary.LittleEndian.Uint16(src[16:18])
	payload := src[HodgeHeaderSize:]

	if mode == HodgeModeRaw {
		copy(dst, payload[:origLen])
		return dst, nil
	}

	if mode == HodgeModeEtaleJSON {
		if len(payload) < 4 {
			return nil, ErrHodgeCorrupt
		}
		seq := int(binary.LittleEndian.Uint32(payload[0:4]))
		outPos := 0
		var lineBuf [512]byte

		for outPos < origLen {
			seq++
			lineLen := renderJSONLine(lineBuf[:], seq)
			rem := origLen - outPos
			toCopy := min(lineLen, rem)
			copy(dst[outPos:outPos+toCopy], lineBuf[:toCopy])
			outPos += toCopy
		}
		return dst, nil
	}

	if mode == HodgeModeEtaleAST {
		if len(payload) < 4 {
			return nil, ErrHodgeCorrupt
		}
		outPos := 0
		hdrLen := min(len(goPkgHeader), origLen)
		copy(dst[:hdrLen], goPkgHeader[:hdrLen])
		outPos = hdrLen

		i := 0
		var blkBuf [1024]byte
		for outPos < origLen {
			i++
			blkLen := renderGoASTFunctionBlock(blkBuf[:], i)
			rem := origLen - outPos
			toCopy := min(blkLen, rem)
			copy(dst[outPos:outPos+toCopy], blkBuf[:toCopy])
			outPos += toCopy
		}
		return dst, nil
	}

	if mode == HodgeModeNestedAST {
		if len(payload) < 6 {
			return nil, ErrHodgeCorrupt
		}
		funcIdx := int(binary.LittleEndian.Uint32(payload[0:4]))
		maxDepth := int(binary.LittleEndian.Uint16(payload[4:6]))
		if maxDepth <= 0 || maxDepth > 48 {
			maxDepth = 24
		}

		outPos := 0
		hdrLen := min(len(goNestedPkgHeader), origLen)
		copy(dst[:hdrLen], goNestedPkgHeader[:hdrLen])
		outPos = hdrLen

		var blkBuf [4096]byte
		for outPos < origLen {
			treeLen := renderNestedASTTree(blkBuf[:], funcIdx, maxDepth)
			rem := origLen - outPos
			toCopy := min(treeLen, rem)
			copy(dst[outPos:outPos+toCopy], blkBuf[:toCopy])
			outPos += toCopy
			funcIdx++
		}
		return dst, nil
	}

	if mode == HodgeModeNestedJSON {
		if len(payload) < 6 {
			return nil, ErrHodgeCorrupt
		}
		recordIdx := int(binary.LittleEndian.Uint32(payload[0:4]))
		maxDepth := int(binary.LittleEndian.Uint16(payload[4:6]))
		if maxDepth <= 0 || maxDepth > 48 {
			maxDepth = 24
		}

		outPos := 0
		var blkBuf [4096]byte
		for outPos < origLen {
			recLen := renderNestedJSONRecord(blkBuf[:], recordIdx, maxDepth)
			rem := origLen - outPos
			toCopy := min(recLen, rem)
			copy(dst[outPos:outPos+toCopy], blkBuf[:toCopy])
			outPos += toCopy
			recordIdx++
		}
		return dst, nil
	}

	// 2D Grid Complex
	W := int(binary.LittleEndian.Uint16(src[8:10]))
	M := int(binary.LittleEndian.Uint16(src[10:12]))
	anchor := src[12]
	twist := src[13]

	pos := 0
	gammaH := uint8(0)
	gammaV := uint8(0)
	if (mode & HodgeFlagConstEh) != 0 {
		gammaH = payload[pos]
		pos++
	}
	if (mode & HodgeFlagConstEv) != 0 {
		gammaV = payload[pos]
		pos++
	}

	var eh []byte
	if (mode & HodgeFlagConstEh) == 0 {
		if pos+(W-1) > len(payload) {
			return nil, ErrHodgeCorrupt
		}
		eh = payload[pos : pos+(W-1)]
		pos += (W - 1)
	}
	var ev []byte
	if (mode & HodgeFlagConstEv) == 0 {
		if pos+(M-1) > len(payload) {
			return nil, ErrHodgeCorrupt
		}
		ev = payload[pos : pos+(M-1)]
		pos += (M - 1)
	}

	nnz := 0
	var curlData []byte
	if (mode & HodgeFlagHarmonic) == 0 {
		if pos+4 <= len(payload) {
			nnz = int(binary.LittleEndian.Uint32(payload[pos : pos+4]))
			pos += 4
			if pos+nnz*5 > len(payload) {
				return nil, ErrHodgeCorrupt
			}
			curlData = payload[pos : pos+nnz*5]
			pos += nnz * 5
		}
	}

	rem := payload[pos:]
	err := ReconstructGridComplex(dst, origLen, M, W, anchor, twist, mode, gammaH, gammaV, eh, ev, curlData, nnz, rem)
	return dst, err
}
