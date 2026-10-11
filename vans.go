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

	vansFlag4X       uint8 = 0x01 // 4-way interleaved SIMD-friendly streams
	vansFlagSheaf    uint8 = 0x02 // Legacy Étale Sheaf Vector C stream splitting applied to literals
	vansFlagCtxSheaf uint8 = 0x04 // 2D Context Sheaf Markovian splitting (4 fibers, 0 bitmask)

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
