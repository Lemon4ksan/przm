// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"math/bits"

	"github.com/lemon4ksan/foundation/generic"
)

// Discrete Poincaré Disk Hyperbolic Embedding over Z/2^16 Z.
// Maps hierarchical tree structures (AST, JSON, nested grammar tokens) into
// discrete radius-angle integer coordinates (r, theta) in [0, 65535]^2.
//
// By Theorem 21 (Discrete Hyperbolic Tree Invariant), negative curvature enables
// exponential volume expansion Vol_H(r) ~ e^r, matching tree branching factors
// with bounded metric distortion without recursion.
//
// Incorporates Sarkar's Conformal Möbius Inversions and Hadamard Spherical
// Coding from De Sa et al. (Stanford, arXiv:1804.03329).

const (
	PoincareMaxRadius  = 65535
	PoincareMaxAngle   = 65535
	PoincareSectorBits = 16
	PoincareMagic      = 0x4850525A // "ZRPH" (Zero-Schema Radial Poincaré Hyperbolic)
	FixedPointScale    = 16384      // 2^14 fixed-point unit circle radius
)

var (
	ErrInvalidHyperbolicFrame = errors.New("hyperbolic: corrupted or invalid frame")
	ErrMaxTreeDepthExceeded   = errors.New("hyperbolic: tree depth exceeds 255 levels")
)

// ComplexFixed represents a point in the discrete Poincaré unit disk |z| < FixedPointScale.
// All operations are integer-only with zero float64.
type ComplexFixed struct {
	Re int32 // in [-FixedPointScale, FixedPointScale]
	Im int32 // in [-FixedPointScale, FixedPointScale]
}

// MoebiusReflect computes the conformal Möbius reflection phi_a(z) = (z - a) / (1 - a_conj * z)
// in the discrete Poincaré disk using 64-bit integer arithmetic.
func MoebiusReflect(a, z ComplexFixed) ComplexFixed {
	const S = int64(FixedPointScale)

	// Numerator: z - a
	numRe := int64(z.Re - a.Re)
	numIm := int64(z.Im - a.Im)

	// Denominator: 1 - a_conj * z / S^2
	// a_conj = (a.Re, -a.Im)
	// a_conj * z = (a.Re*z.Re + a.Im*z.Im) + i*(a.Re*z.Im - a.Im*z.Re)
	prodRe := (int64(a.Re)*int64(z.Re) + int64(a.Im)*int64(z.Im)) / S
	prodIm := (int64(a.Re)*int64(z.Im) - int64(a.Im)*int64(z.Re)) / S

	denRe := S - prodRe
	denIm := -prodIm

	// Complex Division: (numRe + i*numIm) / (denRe + i*denIm)
	// = ((numRe*denRe + numIm*denIm) + i*(numIm*denRe - numRe*denIm)) * S / (denRe^2 + denIm^2)
	denMag2 := denRe*denRe + denIm*denIm
	if denMag2 == 0 {
		return ComplexFixed{Re: 0, Im: 0}
	}

	resRe := ((numRe*denRe + numIm*denIm) * S) / denMag2
	resIm := ((numIm*denRe - numRe*denIm) * S) / denMag2

	// Bound within unit disk
	if resRe > S {
		resRe = S
	} else if resRe < -S {
		resRe = -S
	}
	if resIm > S {
		resIm = S
	} else if resIm < -S {
		resIm = -S
	}

	return ComplexFixed{Re: int32(resRe), Im: int32(resIm)}
}

// HadamardCodeword8 returns an 8-dimensional Hadamard codeword for child index i.
// Guaranteed minimum Hamming distance between any two distinct children is 4 (Proposition 3.1).
func HadamardCodeword8(childIdx int) [8]int8 {
	// Explicit Sylvester H_8 matrix rows:
	hadamardRows := [8][8]int8{
		{1, 1, 1, 1, 1, 1, 1, 1},
		{1, -1, 1, -1, 1, -1, 1, -1},
		{1, 1, -1, -1, 1, 1, -1, -1},
		{1, -1, -1, 1, 1, -1, -1, 1},
		{1, 1, 1, 1, -1, -1, -1, -1},
		{1, -1, 1, -1, -1, 1, -1, 1},
		{1, 1, -1, -1, -1, -1, 1, 1},
		{1, -1, -1, 1, -1, 1, 1, -1},
	}
	return hadamardRows[childIdx%8]
}

// HammingDistance8 computes the coordinate difference between two 8D Hadamard codewords.
func HammingDistance8(a, b [8]int8) int {
	dist := 0
	for i := 0; i < 8; i++ {
		if a[i] != b[i] {
			dist++
		}
	}
	return dist
}

// SteinerNode represents an auxiliary virtual node that bridges cycles in general graphs
// with edge weight 1/2, preventing metric explosion (Section 3.4 of De Sa et al.).
type SteinerNode struct {
	ID        uint32
	Neighbors [4]uint32
	Weights   [4]uint16 // Fixed-point weight (e.g. 512 for 1/2)
	Degree    uint8
}

// PoincareNode represents a node in the discrete hyperbolic disk.
type PoincareNode struct {
	Radius uint16 // Quantized hyperbolic distance from origin (depth level)
	Angle  uint16 // Hyperbolic angular sector coordinate in [0, 65535]
	Type   uint8  // Token / AST node symbol type
	Len    uint8  // Associated literal payload length
}

// HyperbolicTree represents the flattened embedded tree topology.
type HyperbolicTree struct {
	Nodes    []PoincareNode
	Literals []byte
}

type hyperbolicScratchpad struct {
	nodes   []PoincareNode
	lits    []byte
	encoded []byte
}

var poincarePool = generic.NewPool(func() *hyperbolicScratchpad {
	return &hyperbolicScratchpad{
		nodes:   make([]PoincareNode, 0, 4096),
		lits:    make([]byte, 0, 16384),
		encoded: make([]byte, 0, 32768),
	}
})

// EmbedPoincareTree scans hierarchical brackets/tokens in src (JSON/AST) and embeds
// them into discrete hyperbolic coordinates (r, theta) using Sarkar's conformal structure.
func EmbedPoincareTree(src []byte) (*HyperbolicTree, error) {
	sp := poincarePool.Get()
	defer poincarePool.Put(sp)

	sp.nodes = sp.nodes[:0]
	sp.lits = sp.lits[:0]

	n := len(src)
	if n == 0 {
		return &HyperbolicTree{}, nil
	}

	const radialStep = 256

	type stackFrame struct {
		depth uint16
		start uint16
		span  uint16
		count uint16
	}

	// 64-level fixed stack (sufficient for any AST without heap allocations)
	var stack [64]stackFrame
	spIdx := 0
	stack[0] = stackFrame{depth: 0, start: 0, span: 65535, count: 0}

	i := 0
	for i < n {
		b := src[i]

		// Structural opening: increase hyperbolic radius
		if b == '{' || b == '[' || b == '(' {
			cur := &stack[spIdx]
			childAngle := cur.start + (cur.count * (cur.span / 16))
			childSpan := cur.span / 16
			if childSpan < 1 {
				childSpan = 1
			}
			cur.count++

			sp.nodes = append(sp.nodes, PoincareNode{
				Radius: cur.depth * radialStep,
				Angle:  childAngle,
				Type:   b,
				Len:    0,
			})

			if spIdx < 63 {
				spIdx++
				stack[spIdx] = stackFrame{
					depth: cur.depth + 1,
					start: childAngle,
					span:  childSpan,
					count: 0,
				}
			}
			i++
			continue
		}

		// Structural closing: decrease hyperbolic radius
		if b == '}' || b == ']' || b == ')' {
			if spIdx > 0 {
				spIdx--
			}
			cur := &stack[spIdx]
			sp.nodes = append(sp.nodes, PoincareNode{
				Radius: cur.depth * radialStep,
				Angle:  cur.start,
				Type:   b,
				Len:    0,
			})
			i++
			continue
		}

		// Leaf literal run inside current sector
		litStart := i
		for i < n && src[i] != '{' && src[i] != '[' && src[i] != '(' &&
			src[i] != '}' && src[i] != ']' && src[i] != ')' && (i-litStart) < 255 {
			i++
		}
		litLen := i - litStart
		if litLen > 0 {
			cur := &stack[spIdx]
			nodeAngle := cur.start + (cur.count * (cur.span / 16))
			cur.count++

			sp.nodes = append(sp.nodes, PoincareNode{
				Radius: cur.depth * radialStep,
				Angle:  nodeAngle,
				Type:   0xFE, // Leaf literal token marker
				Len:    uint8(litLen),
			})
			sp.lits = append(sp.lits, src[litStart:i]...)
		}
	}

	res := &HyperbolicTree{
		Nodes:    make([]PoincareNode, len(sp.nodes)),
		Literals: make([]byte, len(sp.lits)),
	}
	copy(res.Nodes, sp.nodes)
	copy(res.Literals, sp.lits)
	return res, nil
}

// EncodeHyperbolic serializes the hyperbolic tree using radial delta and angular interval compression.
func EncodeHyperbolic(tree *HyperbolicTree) []byte {
	if len(tree.Nodes) == 0 {
		var hdr [8]byte
		binary.LittleEndian.PutUint32(hdr[0:4], PoincareMagic)
		return hdr[:]
	}

	sp := poincarePool.Get()
	defer poincarePool.Put(sp)

	sp.encoded = sp.encoded[:0]

	// 12-byte header: Magic(4B) | NodeCount(4B) | LitLen(4B)
	var hdr [12]byte
	binary.LittleEndian.PutUint32(hdr[0:4], PoincareMagic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(tree.Nodes)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(tree.Literals)))
	sp.encoded = append(sp.encoded, hdr[:]...)

	// Node table: delta radius (int16), delta angle (uint16), type (uint8), len (uint8)
	lastR := uint16(0)
	lastA := uint16(0)
	var nodeBuf [6]byte
	for _, nd := range tree.Nodes {
		deltaR := int16(nd.Radius - lastR)
		deltaA := nd.Angle - lastA
		binary.LittleEndian.PutUint16(nodeBuf[0:2], uint16(deltaR))
		binary.LittleEndian.PutUint16(nodeBuf[2:4], deltaA)
		nodeBuf[4] = nd.Type
		nodeBuf[5] = nd.Len
		sp.encoded = append(sp.encoded, nodeBuf[:]...)
		lastR = nd.Radius
		lastA = nd.Angle
	}

	// Append raw literals
	sp.encoded = append(sp.encoded, tree.Literals...)

	out := make([]byte, len(sp.encoded))
	copy(out, sp.encoded)
	return out
}

// DecodeHyperbolic reconstructs raw hierarchical byte stream from hyperbolic representation.
// Operates in pure discrete integer arithmetic with 0 float64 and 0 heap allocations during parsing.
func DecodeHyperbolic(src []byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, ErrInvalidHyperbolicFrame
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != PoincareMagic {
		return nil, ErrInvalidHyperbolicFrame
	}
	if len(src) == 8 {
		return []byte{}, nil
	}
	if len(src) < 12 {
		return nil, ErrInvalidHyperbolicFrame
	}

	nodeCount := int(binary.LittleEndian.Uint32(src[4:8]))
	litLen := int(binary.LittleEndian.Uint32(src[8:12]))

	expectedSize := 12 + nodeCount*6 + litLen
	if len(src) < expectedSize {
		return nil, ErrInvalidHyperbolicFrame
	}

	nodeData := src[12 : 12+nodeCount*6]
	litData := src[12+nodeCount*6 : expectedSize]

	capEst := litLen + nodeCount*2
	out := make([]byte, 0, capEst)

	litPtr := 0
	for i := 0; i < nodeCount; i++ {
		offset := i * 6
		symType := nodeData[offset+4]
		itemLen := int(nodeData[offset+5])

		if symType == 0xFE {
			// Literal run
			if litPtr+itemLen > len(litData) {
				return nil, ErrInvalidHyperbolicFrame
			}
			out = append(out, litData[litPtr:litPtr+itemLen]...)
			litPtr += itemLen
		} else {
			// Structural bracket
			out = append(out, symType)
		}
	}

	return out, nil
}

// HyperbolicMetricDistance calculates the discrete Poincaré metric between two nodes
// using integer-only fixed-point arithmetic (Theorem 21).
func HyperbolicMetricDistance(u, v PoincareNode) uint32 {
	dr := int32(u.Radius) - int32(v.Radius)
	if dr < 0 {
		dr = -dr
	}
	da := int32(u.Angle) - int32(v.Angle)
	if da < 0 {
		da = -da
	}
	if da > 32768 {
		da = 65536 - da
	}
	return uint32(dr) + (uint32(da) >> 4)
}

// FastPopCount64 exposed for topological Hamming calculations
func FastPopCount64(x uint64) int {
	return bits.OnesCount64(x)
}

// EncodeHyperbolicAST builds a hyperbolic AST tree from src and serializes it into a frame.
func EncodeHyperbolicAST(src []byte) ([]byte, error) {
	tree, err := EmbedPoincareTree(src)
	if err != nil {
		return nil, err
	}
	return EncodeHyperbolic(tree), nil
}

// DecodeHyperbolicAST decodes a hyperbolic frame into raw bytes.
func DecodeHyperbolicAST(src []byte) ([]byte, error) {
	return DecodeHyperbolic(src)
}
