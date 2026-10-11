package przm

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"testing"
	"unsafe"
)

func calcEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var counts [256]int
	for _, b := range data {
		counts[b]++
	}
	total := float64(len(data))
	ent := 0.0
	for _, c := range counts {
		if c > 0 {
			p := float64(c) / total
			ent -= p * math.Log2(p)
		}
	}
	return ent
}

func calcOrder1Entropy(data []byte) float64 {
	if len(data) <= 1 {
		return 0
	}
	var counts [256][256]int
	var prevCounts [256]int
	for i := 1; i < len(data); i++ {
		p := data[i-1]
		c := data[i]
		counts[p][c]++
		prevCounts[p]++
	}
	total := float64(len(data) - 1)
	ent := 0.0
	for p := 0; p < 256; p++ {
		if prevCounts[p] == 0 {
			continue
		}
		probP := float64(prevCounts[p]) / total
		condEnt := 0.0
		for c := 0; c < 256; c++ {
			if counts[p][c] > 0 {
				probCgivenP := float64(counts[p][c]) / float64(prevCounts[p])
				condEnt -= probCgivenP * math.Log2(probCgivenP)
			}
		}
		ent += probP * condEnt
	}
	return ent
}

func calcDualPlaneEntropy(data []byte) (entH, entL, total float64) {
	if len(data) <= 1 {
		return 0, 0, 0
	}
	n := len(data)
	// H_i conditioned on H_{i-1}
	var countH [16][16]int
	var prevH [16]int
	for i := 1; i < n; i++ {
		pH := data[i-1] >> 4
		cH := data[i] >> 4
		countH[pH][cH]++
		prevH[pH]++
	}
	tot := float64(n - 1)
	for p := 0; p < 16; p++ {
		if prevH[p] == 0 {
			continue
		}
		probP := float64(prevH[p]) / tot
		for c := 0; c < 16; c++ {
			if countH[p][c] > 0 {
				pC := float64(countH[p][c]) / float64(prevH[p])
				entH -= probP * pC * math.Log2(pC)
			}
		}
	}

	// L_i conditioned on H_i only (16 contexts for low nibble)
	var countL_H [16][16]int
	var totH [16]int
	for i := 0; i < n; i++ {
		h := data[i] >> 4
		l := data[i] & 0x0F
		countL_H[h][l]++
		totH[h]++
	}
	for h := 0; h < 16; h++ {
		if totH[h] == 0 {
			continue
		}
		probH := float64(totH[h]) / float64(n)
		for l := 0; l < 16; l++ {
			if countL_H[h][l] > 0 {
				pL := float64(countL_H[h][l]) / float64(totH[h])
				entL -= probH * pL * math.Log2(pL)
			}
		}
	}

	return entH, entL, entH + entL
}

func calcContextSheafEntropy(data []byte, numContexts int, ctxFunc func(prev byte, prev2 byte) int) float64 {
	if len(data) == 0 {
		return 0
	}
	fibers := make([][]byte, numContexts)
	prev := byte(0)
	prev2 := byte(0)
	for i, b := range data {
		ctx := ctxFunc(prev, prev2)
		if ctx < 0 || ctx >= numContexts {
			ctx = 0
		}
		fibers[ctx] = append(fibers[ctx], b)
		prev2 = prev
		prev = b
		_ = i
	}
	totalBits := 0.0
	for _, f := range fibers {
		if len(f) > 0 {
			totalBits += float64(len(f)) * calcEntropy(f)
		}
	}
	return totalBits / float64(len(data))
}

func TestContextSheafExperiment(t *testing.T) {
	files := []struct {
		name string
		path string
	}{
		{"Steam CSS", `d:\CodingProjects\g-man\cmd\generator\protobufs\c\partner.steamgames.com\ssr\aYRS7mnC.css`},
		{"Steam JS", `d:\CodingProjects\g-man\cmd\generator\protobufs\c\ClientExtracted\steamui\broadcast.js`},
		{"Root JSON", `d:\CodingProjects\chromium-net\net\data\ssl\root_stores\root_stores.json`},
		{"People HTML", `d:\CodingProjects\g-man\cmd\generator\protobufs\Random\People.html`},
		{"przm_dec.c", `src\przm_dec.c`},
		{"math_report.md", `d:\CodingProjects\przm\docs\math_report.md`},
	}

	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Logf("Skip %s: %v", f.name, err)
			continue
		}

		var lits []byte
		opts := CompressOptions{
			Level: 5,
			LiteralProbe: func(b []byte) {
				lits = make([]byte, len(b))
				copy(lits, b)
			},
		}
		_, err = CompressWithOptions(data, opts)
		if err != nil {
			t.Fatalf("compress failed: %v", err)
		}

		rawLitEnt := calcEntropy(lits)
		order1Ent := calcOrder1Entropy(lits)

		// Context 1: Preceding High Nibble (16 contexts)
		entHighNib := calcContextSheafEntropy(lits, 16, func(p, p2 byte) int {
			return int(p >> 4)
		})

		// Context 2: Preceding Character Class (4 contexts: Syntax, Lower, Upper/Digits, Other)
		ent4Class := calcContextSheafEntropy(lits, 4, func(p, p2 byte) int {
			if SheafSyntaxTable[p] == 1 {
				return 0
			}
			if p >= 'a' && p <= 'z' {
				return 1
			}
			if (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') {
				return 2
			}
			return 3
		})

		// Context 3: Preceding 2-byte 2nd order (e.g. 64 contexts like Brotli)
		// Brotli context map: class(p) in 4 classes, class(p2) in 4 classes -> 16 contexts,
		// or class(p) * 8 + (p & 7) -> 32 contexts
		ent16x4 := calcContextSheafEntropy(lits, 64, func(p, p2 byte) int {
			c1 := 0
			if SheafSyntaxTable[p] == 1 {
				c1 = 0
			} else if p >= 'a' && p <= 'z' {
				c1 = 1
			} else if (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') {
				c1 = 2
			} else {
				c1 = 3
			}
			c2 := 0
			if SheafSyntaxTable[p2] == 1 {
				c2 = 0
			} else if p2 >= 'a' && p2 <= 'z' {
				c2 = 1
			} else if (p2 >= 'A' && p2 <= 'Z') || (p2 >= '0' && p2 <= '9') {
				c2 = 2
			} else {
				c2 = 3
			}
			return (c1 << 4) | (c2 << 2) | int((p>>2)&3)
		})

		// Context 4: 8 contexts (Syntax, Space/Newline, Lower-vowel, Lower-cons, Upper, Digit, Quote/Brace, Other)
		ent8Class := calcContextSheafEntropy(lits, 8, func(p, p2 byte) int {
			switch {
			case p == ' ' || p == '\n' || p == '\r' || p == '\t':
				return 0
			case p == '"' || p == '\'' || p == '`' || p == '{' || p == '}' || p == '(' || p == ')' || p == '[' || p == ']':
				return 1
			case SheafSyntaxTable[p] == 1:
				return 2
			case p >= '0' && p <= '9':
				return 3
			case p >= 'a' && p <= 'z':
				// vowels vs consonants
				if p == 'a' || p == 'e' || p == 'i' || p == 'o' || p == 'u' {
					return 4
				}
				return 5
			case p >= 'A' && p <= 'Z':
				return 6
			default:
				return 7
			}
		})

		// Context 5: 2nd-order 8x8 = 64 contexts combining 8 classes of p and 8 classes of p2!
		ent2ndOrder64 := calcContextSheafEntropy(lits, 64, func(p, p2 byte) int {
			cls := func(b byte) int {
				switch {
				case b == ' ' || b == '\n' || b == '\r' || b == '\t':
					return 0
				case b == '"' || b == '\'' || b == '{' || b == '}' || b == ':' || b == ';':
					return 1
				case SheafSyntaxTable[b] == 1:
					return 2
				case b >= '0' && b <= '9':
					return 3
				case b >= 'a' && b <= 'h':
					return 4
				case b >= 'i' && b <= 'z':
					return 5
				case b >= 'A' && b <= 'Z':
					return 6
				default:
					return 7
				}
			}
			return (cls(p) << 3) | cls(p2)
		})

		entH, entL, entDual := calcDualPlaneEntropy(lits)
		fmt.Printf("[%s] Total Lits: %d | Order-0: %.3f b/B | Order-1: %.3f b/B | DualPlane(H=%.2f,L=%.2f): %.3f b/B\n", f.name, len(lits), rawLitEnt, order1Ent, entH, entL, entDual)
		fmt.Printf("   -> 4-Class: %.3f b/B | 8-Class: %.3f b/B | 16-HighNib: %.3f b/B | 64-Ctx(16x4): %.3f b/B | 2nd-Order-64: %.3f b/B\n",
			ent4Class, ent8Class, entHighNib, ent16x4, ent2ndOrder64)
	}
}

func TestContextSheafEndToEnd(t *testing.T) {
	files := []struct {
		name string
		path string
	}{
		{"Steam CSS", `d:\CodingProjects\g-man\cmd\generator\protobufs\c\partner.steamgames.com\ssr\aYRS7mnC.css`},
		{"Steam JS", `d:\CodingProjects\g-man\cmd\generator\protobufs\c\ClientExtracted\steamui\broadcast.js`},
		{"Root JSON", `d:\CodingProjects\chromium-net\net\data\ssl\root_stores\root_stores.json`},
		{"People HTML", `d:\CodingProjects\g-man\cmd\generator\protobufs\Random\People.html`},
		{"przm_dec.c", `src\przm_dec.c`},
		{"math_report.md", `d:\CodingProjects\przm\docs\math_report.md`},
	}

	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}

		var lits []byte
		opts := CompressOptions{
			Level: 5,
			LiteralProbe: func(b []byte) {
				lits = make([]byte, len(b))
				copy(lits, b)
			},
		}
		_, err = CompressWithOptions(data, opts)
		if err != nil || len(lits) == 0 {
			continue
		}

		n := len(lits)

		// Compare 1D Sheaf (Order-0 syntax vs content) vs 2D Context Sheaf (4 fibers and 8 fibers)
		// 1D Sheaf:
		sBuf := make([]byte, n)
		cBuf := make([]byte, n)
		maskBuf := make([]byte, (n+7)/8)
		sLen, cLen := SheafSplit(lits, sBuf, cBuf, maskBuf)
		encS1D, _ := VANSEncode4x(sBuf[:sLen])
		encC1D, _ := VANSEncode4x(cBuf[:cLen])
		size1D := len(maskBuf) + len(encS1D) + len(encC1D) + 24

		// 2D Context Sheaf (4 contexts):
		// Context from preceding byte class: 0=delim, 1=lower, 2=upper/digit, 3=other
		ctx4 := func(p byte) int {
			if SheafSyntaxTable[p] == 1 {
				return 0
			}
			if p >= 'a' && p <= 'z' {
				return 1
			}
			if (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') {
				return 2
			}
			return 3
		}
		fibers4 := make([][]byte, 4)
		prev := byte(0)
		for _, b := range lits {
			c := ctx4(prev)
			fibers4[c] = append(fibers4[c], b)
			prev = b
		}
		size4 := 16 // header
		var encFibers4 [][]byte
		for c := 0; c < 4; c++ {
			if len(fibers4[c]) > 0 {
				enc, err := VANSEncode4x(fibers4[c])
				if err != nil {
					t.Fatalf("VANS encode fiber %d failed: %v", c, err)
				}
				encFibers4 = append(encFibers4, enc)
				size4 += 4 + len(enc) // 4B fiber header (fiber origLen) + enc
			} else {
				encFibers4 = append(encFibers4, nil)
				size4 += 4
			}
		}

		// 2D Context Sheaf (8 contexts: preceding class (4) x preceding syntax (2)):
		ctx8 := func(p, p2 byte) int {
			c1 := ctx4(p)
			c2 := int(SheafSyntaxTable[p2])
			return (c1 << 1) | c2
		}
		fibers8 := make([][]byte, 8)
		prev = byte(0)
		prev2 := byte(0)
		for _, b := range lits {
			c := ctx8(prev, prev2)
			fibers8[c] = append(fibers8[c], b)
			prev2 = prev
			prev = b
		}
		size8 := 24 // header
		var encFibers8 [][]byte
		for c := 0; c < 8; c++ {
			if len(fibers8[c]) > 0 {
				enc, err := VANSEncode4x(fibers8[c])
				if err != nil {
					t.Fatalf("VANS encode fiber %d failed: %v", c, err)
				}
				encFibers8 = append(encFibers8, enc)
				size8 += 4 + len(enc)
			} else {
				encFibers8 = append(encFibers8, nil)
				size8 += 4
			}
		}

		// Test Decompression and Roundtrip of 4-Context Sheaf
		decDst := make([]byte, n)
		decFibers4 := make([][]byte, 4)
		for c := 0; c < 4; c++ {
			if len(fibers4[c]) > 0 {
				fDst := make([]byte, len(fibers4[c]))
				var model VANSModel
				decF, err := VANSDecode4xTo(fDst, encFibers4[c], &model)
				if err != nil {
					t.Fatalf("VANSDecode4xTo fiber %d failed: %v", c, err)
				}
				decFibers4[c] = decF
			}
		}
		// Interleave 4-fiber
		pIdx := [4]int{}
		prev = byte(0)
		for i := 0; i < n; i++ {
			c := ctx4(prev)
			b := decFibers4[c][pIdx[c]]
			pIdx[c]++
			decDst[i] = b
			prev = b
		}

		// Verify 100% roundtrip
		if !bytes.Equal(decDst, lits) {
			t.Fatalf("[%s] Roundtrip mismatch!", f.name)
		}

		fmt.Printf("[%s] Raw Lits: %d B | 1D-Sheaf: %d B (%.2f b/B) | 2D-Sheaf(4): %d B (%.2f b/B) | 2D-Sheaf(8): %d B (%.2f b/B) | GAIN: -%d B (%.1f%%)\n",
			f.name, n, size1D, float64(size1D*8)/float64(n),
			size4, float64(size4*8)/float64(n),
			size8, float64(size8*8)/float64(n),
			size1D-size4, float64(size1D-size4)/float64(size1D)*100)
	}
}

func BenchmarkContextSheafDecompress(b *testing.B) {
	data, err := os.ReadFile(`d:\CodingProjects\przm\docs\math_report.md`)
	if err != nil {
		b.Skip(err)
	}
	var lits []byte
	opts := CompressOptions{
		Level: 5,
		LiteralProbe: func(b []byte) {
			lits = make([]byte, len(b))
			copy(lits, b)
		},
	}
	_, _ = CompressWithOptions(data, opts)
	n := len(lits)

	ctx4 := func(p byte) int {
		if SheafSyntaxTable[p] == 1 {
			return 0
		}
		if p >= 'a' && p <= 'z' {
			return 1
		}
		if (p >= 'A' && p <= 'Z') || (p >= '0' && p <= '9') {
			return 2
		}
		return 3
	}

	fibers4 := make([][]byte, 4)
	prev := byte(0)
	for _, bt := range lits {
		c := ctx4(prev)
		fibers4[c] = append(fibers4[c], bt)
		prev = bt
	}

	var encFibers4 [][]byte
	for c := 0; c < 4; c++ {
		enc, _ := VANSEncode4x(fibers4[c])
		encFibers4 = append(encFibers4, enc)
	}

	decDst := make([]byte, n)
	decFibers4 := make([][]byte, 4)
	for c := 0; c < 4; c++ {
		decFibers4[c] = make([]byte, len(fibers4[c]))
	}

	var models [4]VANSModel
	for c := 0; c < 4; c++ {
		if len(fibers4[c]) > 0 {
			_, _ = VANSDecode4xTo(decFibers4[c], encFibers4[c], &models[c])
		}
	}

	b.SetBytes(int64(n))
	b.ResetTimer()

	b.Run("VANSOnly", func(b *testing.B) {
		b.SetBytes(int64(n))
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for c := 0; c < 4; c++ {
				if len(fibers4[c]) > 0 {
					_, _ = VANSDecode4xTo(decFibers4[c], encFibers4[c], &models[c])
				}
			}
		}
	})

	var ctxTable [256]uint8
	for b := 0; b < 256; b++ {
		ctxTable[b] = uint8(ctx4(byte(b)))
	}

	b.Run("InterleaveFast", func(b *testing.B) {
		b.SetBytes(int64(n))
		f0 := decFibers4[0]
		f1 := decFibers4[1]
		f2 := decFibers4[2]
		f3 := decFibers4[3]
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p0, p1, p2, p3 := 0, 0, 0, 0
			prev := byte(0)
			for j := 0; j < n; j++ {
				c := ctxTable[prev]
				var bt byte
				switch c {
				case 0:
					bt = f0[p0]
					p0++
				case 1:
					bt = f1[p1]
					p1++
				case 2:
					bt = f2[p2]
					p2++
				case 3:
					bt = f3[p3]
					p3++
				}
				decDst[j] = bt
				prev = bt
			}
		}
	})

	b.Run("InterleaveUnsafe", func(b *testing.B) {
		b.SetBytes(int64(n))
		pDst := unsafe.Pointer(&decDst[0])
		var pF0, pF1, pF2, pF3 unsafe.Pointer
		if len(decFibers4[0]) > 0 { pF0 = unsafe.Pointer(&decFibers4[0][0]) }
		if len(decFibers4[1]) > 0 { pF1 = unsafe.Pointer(&decFibers4[1][0]) }
		if len(decFibers4[2]) > 0 { pF2 = unsafe.Pointer(&decFibers4[2][0]) }
		if len(decFibers4[3]) > 0 { pF3 = unsafe.Pointer(&decFibers4[3][0]) }
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			p0, p1, p2, p3 := 0, 0, 0, 0
			prev := byte(0)
			j := 0
			for ; j+4 <= n; j += 4 {
				c0 := ctxTable[prev]
				var b0 byte
				switch c0 {
				case 0: b0 = *(*byte)(unsafe.Add(pF0, p0)); p0++
				case 1: b0 = *(*byte)(unsafe.Add(pF1, p1)); p1++
				case 2: b0 = *(*byte)(unsafe.Add(pF2, p2)); p2++
				default: b0 = *(*byte)(unsafe.Add(pF3, p3)); p3++
				}
				*(*byte)(unsafe.Add(pDst, j)) = b0

				c1 := ctxTable[b0]
				var b1 byte
				switch c1 {
				case 0: b1 = *(*byte)(unsafe.Add(pF0, p0)); p0++
				case 1: b1 = *(*byte)(unsafe.Add(pF1, p1)); p1++
				case 2: b1 = *(*byte)(unsafe.Add(pF2, p2)); p2++
				default: b1 = *(*byte)(unsafe.Add(pF3, p3)); p3++
				}
				*(*byte)(unsafe.Add(pDst, j+1)) = b1

				c2 := ctxTable[b1]
				var b2 byte
				switch c2 {
				case 0: b2 = *(*byte)(unsafe.Add(pF0, p0)); p0++
				case 1: b2 = *(*byte)(unsafe.Add(pF1, p1)); p1++
				case 2: b2 = *(*byte)(unsafe.Add(pF2, p2)); p2++
				default: b2 = *(*byte)(unsafe.Add(pF3, p3)); p3++
				}
				*(*byte)(unsafe.Add(pDst, j+2)) = b2

				c3 := ctxTable[b2]
				var b3 byte
				switch c3 {
				case 0: b3 = *(*byte)(unsafe.Add(pF0, p0)); p0++
				case 1: b3 = *(*byte)(unsafe.Add(pF1, p1)); p1++
				case 2: b3 = *(*byte)(unsafe.Add(pF2, p2)); p2++
				default: b3 = *(*byte)(unsafe.Add(pF3, p3)); p3++
				}
				*(*byte)(unsafe.Add(pDst, j+3)) = b3
				prev = b3
			}
			for ; j < n; j++ {
				c := ctxTable[prev]
				var bt byte
				switch c {
				case 0: bt = *(*byte)(unsafe.Add(pF0, p0)); p0++
				case 1: bt = *(*byte)(unsafe.Add(pF1, p1)); p1++
				case 2: bt = *(*byte)(unsafe.Add(pF2, p2)); p2++
				default: bt = *(*byte)(unsafe.Add(pF3, p3)); p3++
				}
				*(*byte)(unsafe.Add(pDst, j)) = bt
				prev = bt
			}
		}
	})

	b.Run("InterleavePtrs8", func(b *testing.B) {
		b.SetBytes(int64(n))
		pDst := unsafe.Pointer(&decDst[0])
		var pF0, pF1, pF2, pF3 unsafe.Pointer
		if len(decFibers4[0]) > 0 { pF0 = unsafe.Pointer(&decFibers4[0][0]) }
		if len(decFibers4[1]) > 0 { pF1 = unsafe.Pointer(&decFibers4[1][0]) }
		if len(decFibers4[2]) > 0 { pF2 = unsafe.Pointer(&decFibers4[2][0]) }
		if len(decFibers4[3]) > 0 { pF3 = unsafe.Pointer(&decFibers4[3][0]) }
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ptr0 := uintptr(pF0)
			ptr1 := uintptr(pF1)
			ptr2 := uintptr(pF2)
			ptr3 := uintptr(pF3)
			pOut := uintptr(pDst)
			prev := byte(0)
			j := 0
			for ; j+8 <= n; j += 8 {
				c0 := ctxTable[prev]
				var b0 byte
				switch c0 {
				case 0: b0 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b0 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b0 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b0 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b0
				pOut++

				c1 := ctxTable[b0]
				var b1 byte
				switch c1 {
				case 0: b1 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b1 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b1 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b1 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b1
				pOut++

				c2 := ctxTable[b1]
				var b2 byte
				switch c2 {
				case 0: b2 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b2 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b2 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b2 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b2
				pOut++

				c3 := ctxTable[b2]
				var b3 byte
				switch c3 {
				case 0: b3 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b3 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b3 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b3 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b3
				pOut++

				c4 := ctxTable[b3]
				var b4 byte
				switch c4 {
				case 0: b4 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b4 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b4 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b4 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b4
				pOut++

				c5 := ctxTable[b4]
				var b5 byte
				switch c5 {
				case 0: b5 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b5 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b5 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b5 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b5
				pOut++

				c6 := ctxTable[b5]
				var b6 byte
				switch c6 {
				case 0: b6 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b6 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b6 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b6 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b6
				pOut++

				c7 := ctxTable[b6]
				var b7 byte
				switch c7 {
				case 0: b7 = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: b7 = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: b7 = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: b7 = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = b7
				pOut++

				prev = b7
			}
			for ; j < n; j++ {
				c := ctxTable[prev]
				var bt byte
				switch c {
				case 0: bt = *(*byte)(unsafe.Pointer(ptr0)); ptr0++
				case 1: bt = *(*byte)(unsafe.Pointer(ptr1)); ptr1++
				case 2: bt = *(*byte)(unsafe.Pointer(ptr2)); ptr2++
				default: bt = *(*byte)(unsafe.Pointer(ptr3)); ptr3++
				}
				*(*byte)(unsafe.Pointer(pOut)) = bt
				pOut++
				prev = bt
			}
		}
	})
}


