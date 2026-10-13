// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"unsafe"
)

const (
	vansMagic0 uint8 = 0x56 // 'V'
	vansMagic1 uint8 = 0x41 // 'A'
	vansVer1   uint8 = 0x01

	vansFlag4X           uint8 = 0x01 // 4-way interleaved SIMD-friendly streams
	vansFlagSheaf        uint8 = 0x02 // Étale Sheaf Vector C stream splitting applied to literals
	vansFlagCtxSheaf     uint8 = 0x04 // 2D Context Sheaf Markovian splitting (4 fibers, 0 bitmask)
	vansFlagCtxOrder1    uint8 = 0x08 // Order-1 Context-conditioned ANS (Direct Context Sheaf)
	vansFlagCtxOrder2    uint8 = 0x10 // Order-2 Context-conditioned ANS (64-state Context Sheaf)
	vansFlagOpcodeOrder2 uint8 = 0x20 // 8-context Bigram/Order-2 V-ANS for opcodes
	vansFlagMetaOrder2   uint8 = 0x40 // 8-context Bigram/Order-2 V-ANS for metadata
	vansFlagVarOrder2    uint8 = 0x80 // 8-context Bigram/Order-2 V-ANS for varStream

	CtxModeStructural uint8 = 0x00 // Mode 0: Refined 4-Class Étale Sheaf (Delim, WS, Ident, Num)
	CtxModeJSONDense  uint8 = 0x01 // Mode 1: JSON/Log Syntax-Dense Foliation
	CtxMode2Phase     uint8 = 0x02 // Mode 2: 2-Phase Causal Syntax/Content Foliation
	CtxModeOrder2     uint8 = 0x03 // Mode 3: Order-2 Bigram Foliation
	CtxModeMask       uint8 = 0xF0
	CtxModeShift      int   = 4

	vansM uint32 = 4096  // ANS table precision M = 2^12
	vansL uint32 = 65536 // State lower bound L = 2^16
)

var (
	ErrVANSCorrupted    = errors.New("przm/vans: corrupted stream")
	ErrVANSSizeMismatch = errors.New("przm/vans: decompressed size mismatch")
)

// Matrix16x16 represents the joint empirical frequency distribution of
// high nibbles (H) and low nibbles (L) for discrete byte symbols:
//
//	symbol = (H << 4) | L,   where H in 0..15, L in 0..15.
type Matrix16x16 struct {
	Counts [256]uint32 // Counts[symbol]
	Total  uint32
}

// BuildFrequencyMatrix scans a buffer and computes the honest 16x16 joint frequency counts.
func BuildFrequencyMatrix(src []byte) Matrix16x16 {
	var m Matrix16x16
	n := len(src)
	i := 0
	for ; i+7 <= n-1; i += 8 {
		m.Counts[src[i]]++
		m.Counts[src[i+1]]++
		m.Counts[src[i+2]]++
		m.Counts[src[i+3]]++
		m.Counts[src[i+4]]++
		m.Counts[src[i+5]]++
		m.Counts[src[i+6]]++
		m.Counts[src[i+7]]++
	}
	for ; i < n; i++ {
		m.Counts[src[i]]++
	}
	m.Total = uint32(n)
	return m
}

// MarginalHigh returns the marginal counts of high nibbles: f_H(h) = sum_l Counts[h][l].
func (m *Matrix16x16) MarginalHigh() [16]uint32 {
	var fH [16]uint32
	for h := range 16 {
		sum := uint32(0)
		for l := range 16 {
			sum += m.Counts[(h<<4)|l]
		}
		fH[h] = sum
	}
	return fH
}

// MarginalLow returns the marginal counts of low nibbles: f_L(l) = sum_h Counts[h][l].
func (m *Matrix16x16) MarginalLow() [16]uint32 {
	var fL [16]uint32
	for l := range 16 {
		sum := uint32(0)
		for h := range 16 {
			sum += m.Counts[(h<<4)|l]
		}
		fL[l] = sum
	}
	return fL
}

// MutualInformation computes the honest mutual information I(H; L) in bits/symbol:
//
//	I(H; L) = sum_{h,l} P(h, l) * log2( P(h, l) / (P(h) * P(l)) )
func (m *Matrix16x16) MutualInformation() float64 {
	if m.Total == 0 {
		return 0
	}
	fH := m.MarginalHigh()
	fL := m.MarginalLow()
	total := float64(m.Total)

	mi := 0.0
	for h := range 16 {
		pH := float64(fH[h]) / total
		if pH == 0 {
			continue
		}
		for l := range 16 {
			c := m.Counts[(h<<4)|l]
			if c == 0 {
				continue
			}
			pHL := float64(c) / total
			pL := float64(fL[l]) / total
			mi += pHL * math.Log2(pHL/(pH*pL))
		}
	}
	return mi
}

// JointEntropy computes H(H, L) = -sum P(h, l) * log2 P(h, l) in bits/symbol.
func (m *Matrix16x16) JointEntropy() float64 {
	if m.Total == 0 {
		return 0
	}
	total := float64(m.Total)
	ent := 0.0
	for h := range 16 {
		for l := range 16 {
			c := m.Counts[(h<<4)|l]
			if c > 0 {
				p := float64(c) / total
				ent -= p * math.Log2(p)
			}
		}
	}
	return ent
}

// Normalize quantizes the 16x16 matrix counts into normalized ANS frequencies
// summing strictly to targetM (typically 4096).
// Every symbol present in the source is guaranteed F[s] >= 1.
func (m *Matrix16x16) Normalize(targetM uint32) [256]uint16 {
	var norm [256]uint16
	if m.Total == 0 {
		return norm
	}

	active := 0
	for s := range 256 {
		if m.Counts[s] > 0 {
			active++
		}
	}

	if active == 0 {
		return norm
	}

	if uint32(active) >= targetM {
		// Degenerate case: more active symbols than states (not possible with 256 symbols and M=4096)
		for s := range 256 {
			norm[s] = uint16(targetM / 256)
		}
		return norm
	}

	// 1. Minimum 1 allocation for each present symbol
	remTarget := targetM - uint32(active)
	total := uint64(m.Total)

	type remainder struct {
		sym int
		rem uint64
	}
	rems := make([]remainder, 0, active)

	allocated := uint32(0)
	for s := range 256 {
		c := m.Counts[s]
		if c > 0 {
			prod := uint64(c) * uint64(remTarget)
			add := uint32(prod / total)
			norm[s] = 1 + uint16(add)
			allocated += 1 + add
			rems = append(rems, remainder{
				sym: s,
				rem: prod % total,
			})
		}
	}

	// 2. Distribute remaining deficit to symbols with largest fractional remainders
	if allocated < targetM {
		diff := int(targetM - allocated)
		sort.Slice(rems, func(i, j int) bool {
			return rems[i].rem > rems[j].rem
		})
		for i := 0; i < diff && i < len(rems); i++ {
			norm[rems[i].sym]++
		}
	}

	return norm
}

// VANSModel holds the distribution and decoding lookup tables for V-ANS.
type VANSModel struct {
	Freq        [256]uint16
	CDF         [257]uint16
	DecodeTable [vansM]uint32
}

// NewVANSModel creates a precomputed V-ANS model from a 16x16 joint frequency matrix.
func NewVANSModel(m *Matrix16x16) *VANSModel {
	model := &VANSModel{}
	model.InitFromFreq(m.Normalize(vansM))
	return model
}

// InitFromFreq fills an existing VANSModel in-place with normalized frequencies.
func (m *VANSModel) InitFromFreq(freq [256]uint16) {
	m.Freq = freq
	cdf := uint32(0)
	for s := range 256 {
		m.CDF[s] = uint16(cdf)
		f := uint32(m.Freq[s])
		if f == 0 {
			continue
		}
		fMinus1Shift := (f - 1) << 20
		baseEntry := uint32(s) | fMinus1Shift
		tableSlice := m.DecodeTable[cdf : cdf+f]
		for j := range f {
			tableSlice[j] = baseEntry | (j << 8)
		}
		cdf += f
	}
	m.CDF[256] = uint16(cdf)
}

// NewVANSModelFromFreq constructs a model directly from normalized frequencies.
func NewVANSModelFromFreq(freq [256]uint16) *VANSModel {
	var model VANSModel
	model.InitFromFreq(freq)
	cp := model
	return &cp
}

// SerializeMatrix encodes the 16x16 matrix into a compact byte header.
func (m *VANSModel) SerializeMatrix() []byte {
	var buf []byte
	// 1. Active rows mask (16 bits: 2 bytes)
	var rowMask uint16
	for h := range 16 {
		rowActive := false
		for l := range 16 {
			if m.Freq[(h<<4)|l] > 0 {
				rowActive = true
				break
			}
		}
		if rowActive {
			rowMask |= 1 << h
		}
	}
	buf = binary.LittleEndian.AppendUint16(buf, rowMask)

	// 2. For each active row, 16-bit column mask + frequencies
	for h := range 16 {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		var colMask uint16
		for l := range 16 {
			if m.Freq[(h<<4)|l] > 0 {
				colMask |= 1 << l
			}
		}
		buf = binary.LittleEndian.AppendUint16(buf, colMask)

		// Encode weights: values 1..vansM
		for l := range 16 {
			if (colMask & (1 << l)) == 0 {
				continue
			}
			w := m.Freq[(h<<4)|l]
			if w < 128 {
				buf = append(buf, byte(w))
			} else {
				buf = append(buf, byte(0x80|(w>>8)), byte(w&0xFF))
			}
		}
	}
	return buf
}

// DeserializeMatrixTo decodes a compact matrix header into an existing VANSModel in-place without dynamic heap allocation.
func DeserializeMatrixTo(src []byte, model *VANSModel) (int, error) {
	if len(src) < 2 {
		return 0, ErrVANSCorrupted
	}
	rowMask := binary.LittleEndian.Uint16(src[0:2])
	ptr := 2

	var freq [256]uint16
	sum := uint32(0)

	for h := range 16 {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		if ptr+2 > len(src) {
			return 0, ErrVANSCorrupted
		}
		colMask := binary.LittleEndian.Uint16(src[ptr : ptr+2])
		ptr += 2

		for l := range 16 {
			if (colMask & (1 << l)) == 0 {
				continue
			}
			if ptr >= len(src) {
				return 0, ErrVANSCorrupted
			}
			b0 := src[ptr]
			ptr++
			var w uint16
			if (b0 & 0x80) == 0 {
				w = uint16(b0)
			} else {
				if ptr >= len(src) {
					return 0, ErrVANSCorrupted
				}
				b1 := src[ptr]
				ptr++
				w = (uint16(b0&0x7F) << 8) | uint16(b1)
			}
			freq[(h<<4)|l] = w
			sum += uint32(w)
		}
	}

	if sum != vansM {
		return 0, fmt.Errorf("%w: normalized weight sum is %d, want %d", ErrVANSCorrupted, sum, vansM)
	}

	model.InitFromFreq(freq)
	return ptr, nil
}

// DeserializeMatrix decodes a compact matrix header and constructs the VANSModel.
func DeserializeMatrix(src []byte) (*VANSModel, int, error) {
	var model VANSModel
	ptr, err := DeserializeMatrixTo(src, &model)
	if err != nil {
		return nil, 0, err
	}
	cp := model
	return &cp, ptr, nil
}

// VANSEncode4x encodes raw bytes using a 4-way interleaved Vector-State ANS coder.
// It constructs an honest 16x16 frequency matrix and packs the stream.
func VANSEncode4x(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	// 1. Build honest frequency matrix
	mat := BuildFrequencyMatrixSIMD(src)
	model := NewVANSModel(&mat)

	// 2. Initialize 4 independent vector states
	var x [4]uint32
	for i := range 4 {
		x[i] = vansL
	}

	var bitstream []byte

	// 3. Encode backwards
	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		f := uint32(model.Freq[s])
		c := uint32(model.CDF[s])

		// Renormalization: ensure x[lane] <= xMax
		// xMax = (256 * vansL / vansM) * f - 1 = 4096 * f - 1
		xMax := (uint32(4096) * f) - 1
		for x[lane] > xMax {
			bitstream = append(bitstream, byte(x[lane]))
			x[lane] >>= 8
		}

		// State transition
		x[lane] = (x[lane]/f)*vansM + c + (x[lane] % f)
	}

	// 4. Serialize framing
	// Header: Magic(2B) | Version(1B) | Flags(1B) | OrigLen(4B)
	matBytes := model.SerializeMatrix()
	headerSize := 8 + len(matBytes) + 16 // +16 for 4 final states (4 * uint32)
	totalSize := headerSize + len(bitstream)

	out := make([]byte, 0, totalSize)
	out = append(out, vansMagic0, vansMagic1, vansVer1, vansFlag4X)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)
	out = append(out, matBytes...)

	// Put 4 final states
	for i := range 4 {
		var stBuf [4]byte
		binary.LittleEndian.PutUint32(stBuf[:], x[i])
		out = append(out, stBuf[:]...)
	}

	// Append reversed bitstream so decompressor reads forward
	for _, b := range slices.Backward(bitstream) {
		out = append(out, b)
	}

	return out, nil
}

// VANSDecode4xTo decodes a 4-way interleaved Vector-State ANS stream into dst using preallocated model.
// Guarantees strictly 0 dynamic heap allocations if cap(dst) >= origLen.
func VANSDecode4xTo(dst, src []byte, model *VANSModel) ([]byte, error) {
	if len(src) == 0 {
		return dst[:0], nil
	}
	if len(src) < 8 {
		return nil, ErrVANSCorrupted
	}

	if src[0] != vansMagic0 || src[1] != vansMagic1 || src[2] != vansVer1 {
		return nil, fmt.Errorf("%w: invalid V-ANS signature", ErrVANSCorrupted)
	}

	flags := src[3]
	if (flags & vansFlag4X) == 0 {
		return nil, fmt.Errorf("%w: unsupported V-ANS mode", ErrVANSCorrupted)
	}
	if (flags&vansFlagCtxSheaf) == 0 && (flags&vansFlagCtxOrder2) != 0 {
		return ContextVANSDecodeOrder2To(dst, src)
	}
	if (flags & vansFlagOpcodeOrder2) != 0 {
		return ContextVANSDecodeOpcode16To(dst, src)
	}
	if (flags & vansFlagMetaOrder2) != 0 {
		return ContextVANSDecodeMeta8To(dst, src)
	}
	if (flags & vansFlagVarOrder2) != 0 {
		return ContextVANSDecodeVar8To(dst, src)
	}
	if (flags & vansFlagCtxOrder1) != 0 {
		return ContextVANSDecode4xTo(dst, src)
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	// Deserialize 16x16 Matrix into model in-place
	matBytesRead, err := DeserializeMatrixTo(src[8:], model)
	if err != nil {
		return nil, err
	}

	ptr := 8 + matBytesRead
	if ptr+16 > len(src) {
		return nil, ErrVANSCorrupted
	}

	// Read 4 initial states
	var x [4]uint32
	for i := range 4 {
		x[i] = binary.LittleEndian.Uint32(src[ptr : ptr+4])
		ptr += 4
	}

	streamPtr := ptr
	if cap(dst) < origLen {
		return nil, fmt.Errorf("%w: dst capacity %d < origLen %d", ErrBufferTooSmall, cap(dst), origLen)
	}
	dst = dst[:origLen]

	if origLen > 0 && len(src) > 0 {
		_ = decode_vans4x(
			uint64(uintptr(unsafe.Pointer(&dst[0]))),
			uint64(uintptr(unsafe.Pointer(&src[0]))),
			uint64(streamPtr),
			uint64(origLen),
			uint64(uintptr(unsafe.Pointer(&model.DecodeTable[0]))),
			uint64(uintptr(unsafe.Pointer(&x[0]))),
			uint64(len(src)),
		)
	}

	return dst, nil
}

// VANSDecode4x decodes a 4-way interleaved Vector-State ANS stream back to original bytes.
func VANSDecode4x(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < 8 {
		return nil, ErrVANSCorrupted
	}
	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return []byte{}, nil
	}
	dst := make([]byte, origLen)
	var model VANSModel
	return VANSDecode4xTo(dst, src, &model)
}

// SheafStaticFreq stores normalized ANS frequencies for the 8 Order-1 context classes.
var SheafStaticFreq = [8][256]uint16{
	{ /* Context 0: Whitespace */
		3, 2, 2, 2, 2, 2, 2, 2, 2, 19, 54, 2, 2, 2, 2, 2,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		168, 23, 21, 26, 9, 7, 69, 15, 163, 18, 94, 122, 14, 52, 11, 58,
		84, 144, 68, 46, 76, 30, 23, 9, 43, 8, 14, 8, 73, 177, 50, 11,
		3, 34, 34, 54, 25, 11, 23, 30, 27, 29, 9, 9, 40, 40, 14, 11,
		37, 5, 25, 44, 22, 16, 9, 12, 7, 5, 7, 18, 12, 4, 22, 8,
		12, 65, 75, 71, 51, 40, 69, 19, 27, 100, 18, 15, 37, 40, 29, 39,
		68, 12, 54, 91, 75, 23, 39, 26, 8, 7, 7, 30, 82, 39, 4, 2,
		3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 4, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 3, 4, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	},
	{ /* Context 1: Delimiters / Punctuation */
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 255, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		772, 14, 40, 7, 13, 3, 9, 46, 38, 90, 26, 32, 123, 30, 38, 18,
		59, 150, 96, 69, 53, 36, 34, 24, 18, 18, 24, 133, 22, 42, 26, 5,
		5, 16, 16, 24, 16, 13, 24, 11, 9, 13, 3, 3, 9, 26, 7, 11,
		26, 5, 28, 13, 14, 11, 7, 13, 5, 5, 11, 13, 5, 30, 16, 22,
		24, 28, 57, 57, 30, 18, 22, 18, 32, 79, 16, 7, 46, 32, 13, 26,
		24, 11, 36, 57, 24, 30, 20, 14, 16, 14, 11, 11, 5, 36, 5, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		5, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 5, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	},
	{ /* Context 2: Operators */
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 64, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		1134, 10, 10, 3, 6, 4, 75, 10, 15, 59, 176, 68, 35, 44, 12, 77,
		35, 21, 62, 26, 26, 10, 17, 8, 19, 8, 17, 73, 59, 236, 79, 10,
		4, 33, 12, 19, 8, 15, 15, 6, 17, 10, 6, 4, 15, 26, 12, 15,
		15, 4, 12, 26, 15, 8, 8, 15, 6, 6, 6, 24, 6, 28, 10, 19,
		8, 21, 53, 46, 26, 21, 19, 12, 10, 35, 6, 6, 37, 17, 10, 28,
		39, 3, 17, 48, 12, 12, 15, 8, 3, 8, 3, 3, 104, 37, 3, 3,
		4, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 6, 3, 3, 3,
		3, 3, 3, 3, 4, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4,
	},
	{ /* Context 3: Vowels */
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 11, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		204, 3, 4, 2, 6, 4, 4, 6, 11, 14, 4, 8, 17, 33, 7, 10,
		21, 22, 13, 17, 16, 11, 12, 13, 15, 11, 6, 6, 6, 7, 4, 3,
		2, 7, 10, 9, 3, 2, 6, 4, 4, 4, 2, 2, 6, 6, 2, 3,
		10, 4, 3, 7, 4, 2, 2, 2, 3, 2, 2, 5, 3, 15, 2, 28,
		6, 104, 49, 178, 200, 51, 131, 70, 9, 76, 4, 20, 274, 134, 485, 69,
		110, 11, 327, 252, 279, 49, 62, 31, 37, 15, 31, 2, 2, 7, 2, 2,
		2, 2, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	},
	{ /* Context 4: Consonants */
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 21, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		418, 8, 11, 3, 3, 5, 2, 3, 27, 39, 16, 15, 47, 31, 16, 13,
		40, 39, 24, 27, 22, 17, 19, 12, 22, 8, 7, 19, 6, 7, 4, 2,
		2, 5, 4, 7, 4, 4, 7, 6, 6, 3, 2, 2, 6, 7, 3, 4,
		5, 2, 7, 4, 4, 2, 4, 4, 2, 2, 2, 19, 2, 9, 2, 90,
		8, 263, 33, 95, 85, 491, 47, 42, 79, 273, 6, 22, 106, 55, 37, 216,
		55, 5, 141, 141, 208, 101, 16, 11, 10, 64, 16, 4, 4, 7, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 3, 1, 1, 1, 1, 1, 1, 1, 2, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
		1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 1, 1, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	},
	{ /* Context 5: Uppercase Letters */
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 7, 2, 2, 2, 2, 2,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
		194, 4, 11, 3, 5, 8, 3, 34, 17, 44, 7, 15, 24, 25, 5, 11,
		21, 58, 34, 21, 36, 29, 19, 7, 17, 15, 17, 16, 4, 5, 8, 4,
		3, 152, 48, 101, 116, 209, 58, 54, 39, 94, 12, 20, 131, 82, 98, 112,
		79, 11, 159, 163, 127, 65, 23, 42, 32, 17, 24, 3, 5, 4, 7, 146,
		5, 86, 8, 11, 12, 97, 5, 7, 38, 70, 2, 2, 27, 7, 39, 98,
		35, 2, 48, 25, 19, 51, 12, 5, 12, 17, 2, 7, 5, 12, 2, 2,
		2, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 7, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 3, 3, 3, 3,
		3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3,
	},
	{ /* Context 6: Digits */
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 16, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		159, 6, 4, 2, 6, 6, 2, 6, 15, 73, 7, 10, 50, 25, 88, 19,
		233, 335, 378, 310, 360, 190, 234, 155, 188, 133, 7, 41, 6, 6, 5, 3,
		2, 13, 12, 16, 23, 11, 23, 10, 8, 6, 2, 4, 2, 5, 5, 5,
		12, 2, 4, 14, 6, 17, 8, 4, 6, 2, 2, 15, 2, 41, 6, 34,
		4, 31, 36, 33, 30, 28, 28, 3, 3, 19, 3, 6, 4, 7, 6, 9,
		11, 2, 12, 16, 4, 6, 6, 6, 28, 2, 2, 2, 4, 19, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 6, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
		2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2,
	},
	{ /* Context 7: Other / Control / Binary */
		6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 20, 6, 6, 6, 6, 6,
		6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
		163, 6, 10, 44, 6, 6, 10, 6, 25, 53, 15, 6, 29, 20, 20, 15,
		39, 29, 29, 25, 20, 25, 20, 6, 10, 6, 39, 6, 6, 25, 6, 6,
		6, 6, 53, 29, 25, 15, 34, 15, 48, 25, 6, 6, 48, 53, 15, 29,
		58, 10, 39, 68, 25, 15, 15, 15, 6, 6, 6, 15, 25, 6, 6, 72,
		53, 39, 72, 96, 77, 48, 48, 15, 20, 101, 6, 25, 106, 63, 34, 25,
		48, 6, 15, 130, 234, 53, 39, 6, 15, 6, 6, 20, 6, 6, 6, 6,
		48, 15, 6, 10, 6, 6, 6, 6, 6, 10, 6, 6, 6, 6, 6, 6,
		6, 6, 6, 34, 25, 6, 6, 6, 6, 6, 6, 6, 10, 6, 6, 6,
		10, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 5, 5, 5, 5,
		5, 5, 5, 5, 5, 5, 20, 5, 10, 10, 10, 5, 5, 5, 5, 5,
		6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
		20, 20, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
		6, 6, 15, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
		6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	},
}

// SheafStaticModels holds the initialized VANS models for Order-1 context classes.
var SheafStaticModels [8]VANSModel

func init() {
	for c := range 8 {
		SheafStaticModels[c].InitFromFreq(SheafStaticFreq[c])
	}
}

// ContextVANSEncode4x encodes raw bytes using a 4-way interleaved Vector-State ANS coder
// conditioned on preceding byte Order-1 context classes using the 8-context Sheaf model.
func ContextVANSEncode4x(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	var x [4]uint32
	for i := range 4 {
		x[i] = vansL
	}

	var bitstream []byte

	// Encode backwards
	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		p := byte(0)
		if i > 0 {
			p = src[i-1]
		}
		c := SheafContext8Table[p]
		m := &SheafStaticModels[c]
		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		// Renormalization
		xMax := (uint32(4096) * f) - 1
		for x[lane] > xMax {
			bitstream = append(bitstream, byte(x[lane]))
			x[lane] >>= 8
		}

		// State transition
		x[lane] = (x[lane]/f)*vansM + cdf + (x[lane] % f)
	}

	// Header: Magic(2B) | Version(1B) | Flags(1B) | OrigLen(4B) | States(16B)
	headerSize := 24
	totalSize := headerSize + len(bitstream)

	out := make([]byte, 0, totalSize)
	out = append(out, vansMagic0, vansMagic1, vansVer1, vansFlag4X|vansFlagCtxOrder1)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	// Put 4 final states
	for i := range 4 {
		var stBuf [4]byte
		binary.LittleEndian.PutUint32(stBuf[:], x[i])
		out = append(out, stBuf[:]...)
	}

	// Append reversed bitstream so decompressor reads forward
	for _, b := range slices.Backward(bitstream) {
		out = append(out, b)
	}

	return out, nil
}

// ContextVANSDecode4xTo decodes a 4-way interleaved Order-1 Context-conditioned VANS stream into dst.
// Guarantees strictly 0 dynamic heap allocations if cap(dst) >= origLen.
func ContextVANSDecode4xTo(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst[:0], nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}

	if src[0] != vansMagic0 || src[1] != vansMagic1 || src[2] != vansVer1 {
		return nil, fmt.Errorf("%w: invalid V-ANS signature", ErrVANSCorrupted)
	}

	flags := src[3]
	if (flags & vansFlagCtxOrder1) == 0 {
		return nil, fmt.Errorf("%w: unsupported V-ANS mode", ErrVANSCorrupted)
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	var x [4]uint32
	ptr := 8
	for i := range 4 {
		x[i] = binary.LittleEndian.Uint32(src[ptr : ptr+4])
		ptr += 4
	}

	if cap(dst) < origLen {
		return nil, fmt.Errorf("%w: dst capacity %d < origLen %d", ErrBufferTooSmall, cap(dst), origLen)
	}
	dst = dst[:origLen]

	streamPtr := ptr
	srcLen := len(src)
	prev := byte(0)

	for i := 0; i < origLen; i++ {
		lane := i & 3
		c := SheafContext8Table[prev]
		m := &SheafStaticModels[c]

		slot := x[lane] & (vansM - 1)
		s := byte(m.DecodeTable[slot] & 0xFF)
		dst[i] = s
		prev = s

		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		x[lane] = f*(x[lane]>>12) + (slot - cdf)

		for x[lane] < vansL {
			if streamPtr >= srcLen {
				return nil, ErrVANSCorrupted
			}
			x[lane] = (x[lane] << 8) | uint32(src[streamPtr])
			streamPtr++
		}
	}

	return dst, nil
}

// ContextVANSDecode4x decodes a 4-way interleaved Order-1 Context-conditioned VANS stream back to original bytes.
func ContextVANSDecode4x(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}
	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return []byte{}, nil
	}
	dst := make([]byte, origLen)
	return ContextVANSDecode4xTo(dst, src)
}

// ContextVANSEncodeOrder2 encodes raw bytes using a 4-way interleaved Vector-State ANS coder
// conditioned on preceding 2 bytes Order-2 context classes using the 64-context Sheaf model.
func ContextVANSEncodeOrder2(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	var x [4]uint32
	for i := range 4 {
		x[i] = vansL
	}

	var bitstream []byte

	// Encode backwards
	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		p1 := byte(0)
		p2 := byte(0)
		if i > 0 {
			p1 = src[i-1]
		}
		if i > 1 {
			p2 = src[i-2]
		}
		ctx := (int(SheafContextOrder2Table16[p2]) << 4) | int(SheafContextOrder2Table16[p1])
		m := &SheafStaticModelsOrder2[ctx]
		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		// Renormalization
		xMax := (uint32(4096) * f) - 1
		for x[lane] > xMax {
			bitstream = append(bitstream, byte(x[lane]))
			x[lane] >>= 8
		}

		// State transition
		x[lane] = (x[lane]/f)*vansM + cdf + (x[lane] % f)
	}

	// Header: Magic(2B) | Version(1B) | Flags(1B) | OrigLen(4B) | States(16B)
	headerSize := 24
	totalSize := headerSize + len(bitstream)

	out := make([]byte, 0, totalSize)
	out = append(out, vansMagic0, vansMagic1, vansVer1, vansFlag4X|vansFlagCtxOrder2)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	// Put 4 final states
	for i := range 4 {
		var stBuf [4]byte
		binary.LittleEndian.PutUint32(stBuf[:], x[i])
		out = append(out, stBuf[:]...)
	}

	// Append reversed bitstream so decompressor reads forward
	for _, b := range slices.Backward(bitstream) {
		out = append(out, b)
	}

	return out, nil
}

// ContextVANSDecodeOrder2To decodes a 4-way interleaved Order-2 Context-conditioned VANS stream into dst.
// Guarantees strictly 0 dynamic heap allocations if cap(dst) >= origLen.
func ContextVANSDecodeOrder2To(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst[:0], nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}

	if src[0] != vansMagic0 || src[1] != vansMagic1 || src[2] != vansVer1 {
		return nil, fmt.Errorf("%w: invalid V-ANS signature", ErrVANSCorrupted)
	}

	flags := src[3]
	if (flags&vansFlagCtxSheaf) != 0 || (flags&vansFlagCtxOrder2) == 0 {
		return nil, fmt.Errorf("%w: unsupported V-ANS mode", ErrVANSCorrupted)
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	var x [4]uint32
	ptr := 8
	for i := range 4 {
		x[i] = binary.LittleEndian.Uint32(src[ptr : ptr+4])
		ptr += 4
	}

	if cap(dst) < origLen {
		return nil, fmt.Errorf("%w: dst capacity %d < origLen %d", ErrBufferTooSmall, cap(dst), origLen)
	}
	dst = dst[:origLen]

	streamPtr := ptr
	srcLen := len(src)
	prev2, prev1 := byte(0), byte(0)

	for i := 0; i < origLen; i++ {
		lane := i & 3
		ctx := (int(SheafContextOrder2Table16[prev2]) << 4) | int(SheafContextOrder2Table16[prev1])
		m := &SheafStaticModelsOrder2[ctx]

		slot := x[lane] & (vansM - 1)
		s := byte(m.DecodeTable[slot] & 0xFF)
		dst[i] = s
		prev2 = prev1
		prev1 = s

		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		x[lane] = f*(x[lane]>>12) + (slot - cdf)

		for x[lane] < vansL {
			if streamPtr >= srcLen {
				return nil, ErrVANSCorrupted
			}
			x[lane] = (x[lane] << 8) | uint32(src[streamPtr])
			streamPtr++
		}
	}

	return dst, nil
}

// ContextVANSDecodeOrder2 decodes a 4-way interleaved Order-2 Context-conditioned VANS stream back to original bytes.
func ContextVANSDecodeOrder2(src []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}
	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return []byte{}, nil
	}
	dst := make([]byte, origLen)
	return ContextVANSDecodeOrder2To(dst, src)
}

// ContextVANSEncodeOpcode16 encodes opStream using 16-context Bigram V-ANS.
func ContextVANSEncodeOpcode16(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	var x [4]uint32
	for i := range 4 {
		x[i] = vansL
	}

	var bitstream []byte

	// Encode backwards
	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		p := byte(0)
		if i > 0 {
			p = src[i-1]
		}
		ctx := ctxOpcode16(p)
		m := &OpcodeStaticModels16[ctx]
		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		// Renormalization
		xMax := (uint32(4096) * f) - 1
		for x[lane] > xMax {
			bitstream = append(bitstream, byte(x[lane]))
			x[lane] >>= 8
		}

		// State transition
		x[lane] = (x[lane]/f)*vansM + cdf + (x[lane] % f)
	}

	headerSize := 24
	totalSize := headerSize + len(bitstream)

	out := make([]byte, 0, totalSize)
	out = append(out, vansMagic0, vansMagic1, vansVer1, vansFlag4X|vansFlagOpcodeOrder2)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	for i := range 4 {
		var stBuf [4]byte
		binary.LittleEndian.PutUint32(stBuf[:], x[i])
		out = append(out, stBuf[:]...)
	}

	for _, b := range slices.Backward(bitstream) {
		out = append(out, b)
	}

	return out, nil
}

// ContextVANSEncodeOpcode8 is a backward-compatible alias for ContextVANSEncodeOpcode16.
func ContextVANSEncodeOpcode8(src []byte) ([]byte, error) {
	return ContextVANSEncodeOpcode16(src)
}

// ContextVANSDecodeOpcode16To decodes a 16-context Bigram V-ANS stream into dst with 0 allocations.
func ContextVANSDecodeOpcode16To(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst[:0], nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}

	if src[0] != vansMagic0 || src[1] != vansMagic1 || src[2] != vansVer1 {
		return nil, fmt.Errorf("%w: invalid V-ANS signature", ErrVANSCorrupted)
	}

	flags := src[3]
	if (flags & vansFlagOpcodeOrder2) == 0 {
		return nil, fmt.Errorf("%w: unsupported V-ANS opcode mode", ErrVANSCorrupted)
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	var x [4]uint32
	ptr := 8
	for i := range 4 {
		x[i] = binary.LittleEndian.Uint32(src[ptr : ptr+4])
		ptr += 4
	}

	if cap(dst) < origLen {
		return nil, fmt.Errorf("%w: dst capacity %d < origLen %d", ErrBufferTooSmall, cap(dst), origLen)
	}
	dst = dst[:origLen]

	streamPtr := ptr
	srcLen := len(src)
	prev := byte(0)

	for i := 0; i < origLen; i++ {
		lane := i & 3
		ctx := ctxOpcode16(prev)
		m := &OpcodeStaticModels16[ctx]

		slot := x[lane] & (vansM - 1)
		s := byte(m.DecodeTable[slot] & 0xFF)
		dst[i] = s
		prev = s

		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		x[lane] = f*(x[lane]>>12) + (slot - cdf)

		for x[lane] < vansL {
			if streamPtr >= srcLen {
				return nil, ErrVANSCorrupted
			}
			x[lane] = (x[lane] << 8) | uint32(src[streamPtr])
			streamPtr++
		}
	}

	return dst, nil
}

// ContextVANSDecodeOpcode8To is a backward-compatible alias for ContextVANSDecodeOpcode16To.
func ContextVANSDecodeOpcode8To(dst, src []byte) ([]byte, error) {
	return ContextVANSDecodeOpcode16To(dst, src)
}

// ContextVANSEncodeMeta8 encodes metaStream using 8-context Bigram V-ANS.
func ContextVANSEncodeMeta8(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	var x [4]uint32
	for i := range 4 {
		x[i] = vansL
	}

	var bitstream []byte

	// Encode backwards
	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		p := byte(0)
		if i > 0 {
			p = src[i-1]
		}
		ctx := ctxMeta(p)
		m := &MetaStaticModels8[ctx]
		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		// Renormalization
		xMax := (uint32(4096) * f) - 1
		for x[lane] > xMax {
			bitstream = append(bitstream, byte(x[lane]))
			x[lane] >>= 8
		}

		// State transition
		x[lane] = (x[lane]/f)*vansM + cdf + (x[lane] % f)
	}

	headerSize := 24
	totalSize := headerSize + len(bitstream)

	out := make([]byte, 0, totalSize)
	out = append(out, vansMagic0, vansMagic1, vansVer1, vansFlag4X|vansFlagMetaOrder2)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	for i := range 4 {
		var stBuf [4]byte
		binary.LittleEndian.PutUint32(stBuf[:], x[i])
		out = append(out, stBuf[:]...)
	}

	for _, b := range slices.Backward(bitstream) {
		out = append(out, b)
	}

	return out, nil
}

// ContextVANSDecodeMeta8To decodes an 8-context Bigram V-ANS stream into dst with 0 allocations.
func ContextVANSDecodeMeta8To(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst[:0], nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}

	if src[0] != vansMagic0 || src[1] != vansMagic1 || src[2] != vansVer1 {
		return nil, fmt.Errorf("%w: invalid V-ANS signature", ErrVANSCorrupted)
	}

	flags := src[3]
	if (flags & vansFlagMetaOrder2) == 0 {
		return nil, fmt.Errorf("%w: unsupported V-ANS meta mode", ErrVANSCorrupted)
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	var x [4]uint32
	ptr := 8
	for i := range 4 {
		x[i] = binary.LittleEndian.Uint32(src[ptr : ptr+4])
		ptr += 4
	}

	if cap(dst) < origLen {
		return nil, fmt.Errorf("%w: dst capacity %d < origLen %d", ErrBufferTooSmall, cap(dst), origLen)
	}
	dst = dst[:origLen]

	streamPtr := ptr
	srcLen := len(src)
	prev := byte(0)

	for i := 0; i < origLen; i++ {
		lane := i & 3
		ctx := ctxMeta(prev)
		m := &MetaStaticModels8[ctx]

		slot := x[lane] & (vansM - 1)
		s := byte(m.DecodeTable[slot] & 0xFF)
		dst[i] = s
		prev = s

		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		x[lane] = f*(x[lane]>>12) + (slot - cdf)

		for x[lane] < vansL {
			if streamPtr >= srcLen {
				return nil, ErrVANSCorrupted
			}
			x[lane] = (x[lane] << 8) | uint32(src[streamPtr])
			streamPtr++
		}
	}

	return dst, nil
}

// ContextVANSEncodeVar8 encodes varStream using 8-context V-ANS.
func ContextVANSEncodeVar8(src []byte) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}

	var x [4]uint32
	for i := range 4 {
		x[i] = vansL
	}

	var bitstream []byte

	// Encode backwards
	for i := n - 1; i >= 0; i-- {
		lane := i & 3
		s := src[i]
		p1 := byte(0)
		p2 := byte(0)
		if i > 0 {
			p1 = src[i-1]
		}
		if i > 1 {
			p2 = src[i-2]
		}
		ctx := ctxVar(p1, p2)
		m := &VarStaticModels256[ctx]
		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		// Renormalization
		xMax := (uint32(4096) * f) - 1
		for x[lane] > xMax {
			bitstream = append(bitstream, byte(x[lane]))
			x[lane] >>= 8
		}

		// State transition
		x[lane] = (x[lane]/f)*vansM + cdf + (x[lane] % f)
	}

	headerSize := 24
	totalSize := headerSize + len(bitstream)

	out := make([]byte, 0, totalSize)
	out = append(out, vansMagic0, vansMagic1, vansVer1, vansFlag4X|vansFlagVarOrder2)
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(n))
	out = append(out, lenBuf[:]...)

	for i := range 4 {
		var stBuf [4]byte
		binary.LittleEndian.PutUint32(stBuf[:], x[i])
		out = append(out, stBuf[:]...)
	}

	for _, b := range slices.Backward(bitstream) {
		out = append(out, b)
	}

	return out, nil
}

// ContextVANSDecodeVar8To decodes an 8-context V-ANS varStream into dst with 0 allocations.
func ContextVANSDecodeVar8To(dst, src []byte) ([]byte, error) {
	if len(src) == 0 {
		return dst[:0], nil
	}
	if len(src) < 24 {
		return nil, ErrVANSCorrupted
	}

	if src[0] != vansMagic0 || src[1] != vansMagic1 || src[2] != vansVer1 {
		return nil, fmt.Errorf("%w: invalid V-ANS signature", ErrVANSCorrupted)
	}

	flags := src[3]
	if (flags & vansFlagVarOrder2) == 0 {
		return nil, fmt.Errorf("%w: missing var order2 flag", ErrVANSCorrupted)
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen < 0 || origLen > MaxBlockSize {
		return nil, fmt.Errorf("%w: invalid V-ANS origLen %d", ErrVANSCorrupted, origLen)
	}
	if origLen == 0 {
		return dst[:0], nil
	}

	var x [4]uint32
	ptr := 8
	for i := range 4 {
		x[i] = binary.LittleEndian.Uint32(src[ptr : ptr+4])
		ptr += 4
	}

	if cap(dst) < origLen {
		return nil, fmt.Errorf("%w: dst capacity %d < origLen %d", ErrBufferTooSmall, cap(dst), origLen)
	}
	dst = dst[:origLen]

	streamPtr := ptr
	srcLen := len(src)
	prev1 := byte(0)
	prev2 := byte(0)

	for i := 0; i < origLen; i++ {
		lane := i & 3
		ctx := ctxVar(prev1, prev2)
		m := &VarStaticModels256[ctx]

		slot := x[lane] & (vansM - 1)
		s := byte(m.DecodeTable[slot] & 0xFF)
		dst[i] = s
		prev2 = prev1
		prev1 = s

		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		x[lane] = f*(x[lane]>>12) + (slot - cdf)

		for x[lane] < vansL {
			if streamPtr >= srcLen {
				return nil, ErrVANSCorrupted
			}
			x[lane] = (x[lane] << 8) | uint32(src[streamPtr])
			streamPtr++
		}
	}

	return dst, nil
}

// getDistCtx returns the Markov context class (0..3) for a distance code given the preceding code.
func getDistCtx(prev byte) int {
	if prev < 6 {
		return 0
	} else if prev < 14 {
		return 1
	} else if prev < 26 {
		return 2
	}
	return 3
}

// EncodeDistClusteredVANS encodes distance codes using 4-context Markov Clustered V-ANS.
// Emits [ModeDistClusteredVANSCompact, final_state (4B LittleEndian), bitstream...].
func EncodeDistClusteredVANS(src []byte) []byte {
	n := len(src)
	if n == 0 {
		return []byte{ModeDistClusteredVANSCompact, 0, 0, 0, 0}
	}

	state := uint32(vansL)
	bitstream := make([]byte, 0, n+16)

	// Encode backwards
	for i := n - 1; i >= 0; i-- {
		s := src[i]
		prev := byte(0)
		if i > 0 {
			prev = src[i-1]
		}
		ctx := getDistCtx(prev)
		m := &DistStaticModels4[ctx]
		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])

		// Renormalization
		xMax := (uint32(4096) * f) - 1
		for state > xMax {
			bitstream = append(bitstream, byte(state))
			state >>= 8
		}

		// State transition
		state = (state/f)*vansM + cdf + (state % f)
	}

	out := make([]byte, 5+len(bitstream))
	out[0] = ModeDistClusteredVANSCompact
	binary.LittleEndian.PutUint32(out[1:5], state)
	pos := 5
	for _, b := range slices.Backward(bitstream) {
		out[pos] = b
		pos++
	}
	return out
}

// DecodeDistClusteredVANSTo decodes a 4-context Markov Clustered V-ANS stream into dst.
// Guarantees strictly 0 dynamic heap allocations if cap(dst) >= origLen.
func DecodeDistClusteredVANSTo(dst, src []byte, origLen int) (int, error) {
	if len(src) < 5 {
		return 0, ErrCorruptedStream
	}
	if src[0] != ModeDistClusteredVANSCompact {
		return 0, ErrCorruptedStream
	}
	if len(dst) < origLen {
		return 0, ErrBufferTooSmall
	}
	if origLen == 0 {
		return 0, nil
	}

	state := binary.LittleEndian.Uint32(src[1:5])
	streamPtr := 5
	srcLen := len(src)

	prev := byte(0)
	for i := 0; i < origLen; i++ {
		ctx := getDistCtx(prev)
		m := &DistStaticModels4[ctx]

		slot := state & (vansM - 1)
		s := byte(m.DecodeTable[slot] & 0xFF)
		dst[i] = s
		prev = s

		f := uint32(m.Freq[s])
		cdf := uint32(m.CDF[s])
		state = f*(state>>12) + (slot - cdf)

		for state < vansL {
			if streamPtr >= srcLen {
				return 0, ErrCorruptedStream
			}
			state = (state << 8) | uint32(src[streamPtr])
			streamPtr++
		}
	}
	return origLen, nil
}
