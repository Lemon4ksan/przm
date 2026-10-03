// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"math/bits"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
)

// Frame constants for Cellular Sheaves with Discrete Hodge Laplacians (PRZM-SPEC-0.5 Milestone 3).
const (
	// PRZM_MAGIC_CELLULAR_SHEAF is "CSHD" in Little-Endian:
	// 'C'=0x43, 'S'=0x53, 'H'=0x48, 'D'=0x44 -> 0x44485343.
	PRZM_MAGIC_CELLULAR_SHEAF uint32 = 0x44485343
	CellularSheafMagic        uint32 = 0x44485343
	CellularSheafHeaderSize   int    = 24

	// Flags (uint16 Little-Endian)
	CSHDFlagRaw         uint16 = 0x0000 // Passthrough raw uncompressed
	CSHDFlagCurlFree    uint16 = 0x0001 // Harmonic / curl-free (C == 0)
	CSHDFlagSparseCurl  uint16 = 0x0002 // Sparse face curls (NNZ > 0)
	CSHDFlagConstEh     uint16 = 0x0004 // Horizontal boundary 1-form is constant cycle (gamma_h)
	CSHDFlagConstEv     uint16 = 0x0008 // Vertical boundary 1-form is constant cycle (gamma_v)
	CSHDFlagCascadedPWS uint16 = 0x0010 // Payload cascaded with PWS
)

var (
	ErrCellularSheafCorrupt         = errors.New("przm: invalid or corrupted cellular sheaf frame")
	ErrCellularSheafUnsupportedMode = errors.New("przm: unsupported cellular sheaf mode or flags")
	ErrCellularSheafBufferTooSmall  = errors.New("przm: destination buffer too small for cellular sheaf decompression")
	ErrCellularSheafNoBenefit       = errors.New("przm: cellular sheaf does not compress data")
)

// Candidate strides for autonomous structure discovery.
var CSHDCandidateStrides = []int{4, 8, 16, 32, 64}

var sheafPayloadPool = generic.NewPool(func() *[]byte {
	b := bufkit.AlignedBytes(MaxBlockSize+64, bufkit.CacheLineSize)
	return &b
})

// CellComplex2D represents an M x W regular 2D cell complex X = (X0, X1, X2) over F_{2^8}.
// - 0-cells X0: vertices v_{i, j} for i in [0, M-1], j in [0, W-1].
// - 1-cells X1: horizontal edges e^h_{i, j} and vertical edges e^v_{i, j}.
// - 2-cells X2: elementary faces f_{i, j} bounded by (e^h_{i, j}, e^v_{i, j+1}, e^h_{i+1, j}, e^v_{i, j}).
type CellComplex2D struct {
	Rows  int   // Height M (rows of 0-cells)
	Cols  int   // Width W (columns of 0-cells)
	Twist uint8 // Galois twist parameter alpha (1 for canonical constant sheaf)
}

// Cochain0 represents a 0-cochain on vertices X0 (size M * W).
type Cochain0 []uint8

// Cochain1 represents a 1-cochain on horizontal and vertical edges X1.
type Cochain1 struct {
	Eh []uint8 // Horizontal 1-forms: size M * (W - 1)
	Ev []uint8 // Vertical 1-forms: size (M - 1) * W
}

// Cochain2 represents a 2-cochain on elementary faces X2 (size (M - 1) * (W - 1)).
type Cochain2 []uint8

// SparseFaceCurl represents a non-zero discrete face curl C(f) at linear index.
type SparseFaceCurl struct {
	Index uint32 // Linear face index: (i - 1) * (W - 1) + j
	Val   uint8  // Non-zero curl value in F_{2^8}
}

// SheafBoundaryRep represents the compact boundary representation of a 2D cellular sheaf.
type SheafBoundaryRep struct {
	Anchor      uint8            // 0-cochain potential at anchor vertex (0, 0)
	Eh          []uint8          // Boundary horizontal 1-forms along row 0 (length W - 1)
	Ev          []uint8          // Boundary vertical 1-forms along col 0 (length M - 1)
	GammaH      uint8            // Horizontal homology coefficient (when ConstEh)
	GammaV      uint8            // Vertical homology coefficient (when ConstEv)
	ConstEh     bool             // True if all horizontal boundary edges equal GammaH
	ConstEv     bool             // True if all vertical boundary edges equal GammaV
	SparseCurls []SparseFaceCurl // Non-zero face curls (if any)
	NNZ         int              // Non-zero curl count
	Remainder   []uint8          // Trailing unaligned remainder bytes
}

// Coboundary0 computes the 0-coboundary operator d0: C^0(X; F) -> C^1(X; F) over F_{2^8}.
// For horizontal edge e^h_{i, j} = [(i, j), (i, j+1)]:
//
//	u^h_{i, j} = phi(i, j+1) ^ (alpha * phi(i, j)).
//
// For vertical edge e^v_{i, j} = [(i, j), (i+1, j)]:
//
//	u^v_{i, j} = phi(i+1, j) ^ (alpha * phi(i, j)).
func Coboundary0(complex CellComplex2D, phi Cochain0) Cochain1 {
	M := complex.Rows
	W := complex.Cols
	alpha := complex.Twist

	eh := make([]uint8, M*(W-1))
	ev := make([]uint8, (M-1)*W)

	// Horizontal edges
	idxH := 0
	for i := range M {
		rowOff := i * W
		for j := 0; j < W-1; j++ {
			v0 := phi[rowOff+j]
			v1 := phi[rowOff+j+1]
			if alpha <= 1 {
				eh[idxH] = v1 ^ v0
			} else {
				eh[idxH] = v1 ^ GF256Mul(alpha, v0)
			}
			idxH++
		}
	}

	// Vertical edges
	idxV := 0
	for i := 0; i < M-1; i++ {
		row0 := i * W
		row1 := (i + 1) * W
		for j := range W {
			v0 := phi[row0+j]
			v1 := phi[row1+j]
			if alpha <= 1 {
				ev[idxV] = v1 ^ v0
			} else {
				ev[idxV] = v1 ^ GF256Mul(alpha, v0)
			}
			idxV++
		}
	}

	return Cochain1{Eh: eh, Ev: ev}
}

// Coboundary1 computes the 1-coboundary operator d1: C^1(X; F) -> C^2(X; F) over F_{2^8}.
// Under the sheaf restriction condition over F_{2^8}:
//
//	C_{i, j} = u^h_{i+1, j} ^ (alpha * u^h_{i, j}) ^ u^v_{i, j+1} ^ (alpha * u^v_{i, j}).
//
// Nilpotency Theorem 39: For any 0-cochain phi, (d1 ∘ d0)(phi) ≡ 0 identically.
func Coboundary1(complex CellComplex2D, u Cochain1) Cochain2 {
	M := complex.Rows
	W := complex.Cols
	alpha := complex.Twist

	c := make([]uint8, (M-1)*(W-1))
	idx := 0

	for i := 0; i < M-1; i++ {
		hRow0 := i * (W - 1)
		hRow1 := (i + 1) * (W - 1)
		vRow := i * W

		for j := 0; j < W-1; j++ {
			ehTop := u.Eh[hRow0+j]
			ehBot := u.Eh[hRow1+j]
			evLeft := u.Ev[vRow+j]
			evRight := u.Ev[vRow+j+1]

			if alpha <= 1 {
				c[idx] = ehTop ^ evRight ^ ehBot ^ evLeft
			} else {
				c[idx] = ehBot ^ GF256Mul(alpha, ehTop) ^ evRight ^ GF256Mul(alpha, evLeft)
			}
			idx++
		}
	}

	return c
}

// Adjoint0 computes the formal adjoint 0-coboundary d0^T: C^1 -> C^0 (Discrete Divergence) over F_{2^8}.
// At vertex (i, j), accumulates incident edge flows.
func Adjoint0(complex CellComplex2D, u Cochain1) Cochain0 {
	M := complex.Rows
	W := complex.Cols
	alpha := complex.Twist

	div := make([]uint8, M*W)

	for i := range M {
		rowOff := i * W
		hRow := i * (W - 1)

		for j := range W {
			val := uint8(0)

			// Horizontal outgoing: source vertex has coefficient alpha in Coboundary0
			if j < W-1 {
				ehOut := u.Eh[hRow+j]
				if alpha > 1 {
					ehOut = GF256Mul(alpha, ehOut)
				}
				val ^= ehOut
			}
			// Horizontal incoming: target vertex has coefficient 1 in Coboundary0
			if j > 0 {
				val ^= u.Eh[hRow+j-1]
			}

			// Vertical outgoing: source vertex has coefficient alpha in Coboundary0
			if i < M-1 {
				evOut := u.Ev[i*W+j]
				if alpha > 1 {
					evOut = GF256Mul(alpha, evOut)
				}
				val ^= evOut
			}
			// Vertical incoming: target vertex has coefficient 1 in Coboundary0
			if i > 0 {
				val ^= u.Ev[(i-1)*W+j]
			}

			div[rowOff+j] = val
		}
	}

	return div
}

// Adjoint1 computes the formal adjoint 1-coboundary d1^T: C^2 -> C^1 (Discrete Curl Adjoint) over F_{2^8}.
// Accumulates face curls incident to each edge.
func Adjoint1(complex CellComplex2D, c Cochain2) Cochain1 {
	M := complex.Rows
	W := complex.Cols
	alpha := complex.Twist

	eh := make([]uint8, M*(W-1))
	ev := make([]uint8, (M-1)*W)

	// Horizontal edges: incident to face (i, j) and face (i-1, j)
	idxH := 0
	for i := range M {
		for j := 0; j < W-1; j++ {
			val := uint8(0)
			if alpha <= 1 {
				if i < M-1 {
					val ^= c[i*(W-1)+j]
				}
				if i > 0 {
					val ^= c[(i-1)*(W-1)+j]
				}
			} else {
				if i < M-1 {
					val ^= GF256Mul(alpha, c[i*(W-1)+j])
				}
				if i > 0 {
					val ^= c[(i-1)*(W-1)+j]
				}
			}
			eh[idxH] = val
			idxH++
		}
	}

	// Vertical edges: incident to face (i, j) and face (i, j-1)
	idxV := 0
	for i := 0; i < M-1; i++ {
		for j := range W {
			val := uint8(0)
			if alpha <= 1 {
				if j < W-1 {
					val ^= c[i*(W-1)+j]
				}
				if j > 0 {
					val ^= c[i*(W-1)+j-1]
				}
			} else {
				if j < W-1 {
					val ^= GF256Mul(alpha, c[i*(W-1)+j])
				}
				if j > 0 {
					val ^= c[i*(W-1)+j-1]
				}
			}
			ev[idxV] = val
			idxV++
		}
	}

	return Cochain1{Eh: eh, Ev: ev}
}

// HodgeLaplacian0 computes the discrete 0-Laplacian Delta_0 = d0^T d0: C^0 -> C^0.
func HodgeLaplacian0(complex CellComplex2D, phi Cochain0) Cochain0 {
	d0phi := Coboundary0(complex, phi)
	return Adjoint0(complex, d0phi)
}

// HodgeLaplacian1 computes the discrete 1-Laplacian Delta_1 = d1^T d1 + d0 d0^T: C^1 -> C^1.
func HodgeLaplacian1(complex CellComplex2D, u Cochain1) Cochain1 {
	// Delta_{1, up} = d1^T (d1 u)
	c := Coboundary1(complex, u)
	up := Adjoint1(complex, c)

	// Delta_{1, down} = d0 (d0^T u)
	div := Adjoint0(complex, u)
	down := Coboundary0(complex, div)

	eh := make([]uint8, len(u.Eh))
	for i := range eh {
		eh[i] = up.Eh[i] ^ down.Eh[i]
	}
	ev := make([]uint8, len(u.Ev))
	for i := range ev {
		ev[i] = up.Ev[i] ^ down.Ev[i]
	}

	return Cochain1{Eh: eh, Ev: ev}
}

// ProjectHarmonic1 identifies circulating harmonic 1-forms in ker(Delta_1) and performs cycle annihilation.
// Returns horizontal homology coefficient gamma_h, vertical homology coefficient gamma_v, and Betti number beta_1.
func ProjectHarmonic1(complex CellComplex2D, u Cochain1) (gammaH, gammaV uint8, betti1 uint16) {
	M := complex.Rows
	W := complex.Cols

	// Horizontal circulation across columns: evaluate row 0 boundary
	if W > 1 {
		firstEh := u.Eh[0]
		constEh := true
		for j := 1; j < W-1; j++ {
			if u.Eh[j] != firstEh {
				constEh = false
				break
			}
		}
		if constEh {
			gammaH = firstEh
			betti1++
		}
	}

	// Vertical circulation across rows: evaluate col 0 boundary
	if M > 1 {
		firstEv := u.Ev[0]
		constEv := true
		for i := 1; i < M-1; i++ {
			if u.Ev[i*W] != firstEv {
				constEv = false
				break
			}
		}
		if constEv {
			gammaV = firstEv
			betti1++
		}
	}

	return gammaH, gammaV, betti1
}

// AnalyzeSheafDecomposition performs complete cellular sheaf and Hodge Laplacian analysis
// on src interpreted as an M x W grid complex.
func AnalyzeSheafDecomposition(src []byte, M, W int, twist uint8) *SheafBoundaryRep {
	if M < 2 || W < 2 || len(src) < M*W {
		return nil
	}

	gridSize := M * W
	rep := &SheafBoundaryRep{
		Anchor:    src[0],
		Eh:        make([]uint8, W-1),
		Ev:        make([]uint8, M-1),
		Remainder: src[gridSize:],
	}

	// 1. Boundary horizontal 1-forms along row 0
	for j := 0; j < W-1; j++ {
		if twist <= 1 {
			rep.Eh[j] = src[j+1] ^ src[j]
		} else {
			rep.Eh[j] = src[j+1] ^ GF256Mul(twist, src[j])
		}
	}

	// 2. Boundary vertical 1-forms along col 0
	for i := 0; i < M-1; i++ {
		row0 := i * W
		row1 := (i + 1) * W
		if twist <= 1 {
			rep.Ev[i] = src[row1] ^ src[row0]
		} else {
			rep.Ev[i] = src[row1] ^ GF256Mul(twist, src[row0])
		}
	}

	// Check if horizontal boundary is constant cycle (harmonic generator h1)
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

	// Check if vertical boundary is constant cycle (harmonic generator h2)
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

	// 3. Discrete face curl / vorticity field
	totalFaces := (M - 1) * (W - 1)
	rep.SparseCurls = make([]SparseFaceCurl, 0, min(totalFaces/4, 256))
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
				rep.SparseCurls = append(rep.SparseCurls, SparseFaceCurl{
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

// ReconstructCellularSheaf reconstructs the M x W potential grid from its boundary representation
// using vectorized row sweeps.
// Guarantee: strictly zero heap memory allocations.
func ReconstructCellularSheaf(complex CellComplex2D, rep *SheafBoundaryRep, dst []byte) error {
	M := complex.Rows
	W := complex.Cols
	twist := complex.Twist

	if M < 2 || W < 2 || len(dst) < M*W {
		return ErrCellularSheafBufferTooSmall
	}

	// Row 0: Prefix-XOR from anchor
	dst[0] = rep.Anchor
	if rep.ConstEh {
		gamma := rep.GammaH
		if twist <= 1 {
			for j := 0; j < W-1; j++ {
				dst[j+1] = dst[j] ^ gamma
			}
		} else {
			for j := 0; j < W-1; j++ {
				dst[j+1] = GF256Mul(twist, dst[j]) ^ gamma
			}
		}
	} else {
		if twist <= 1 {
			for j := 0; j < W-1; j++ {
				dst[j+1] = dst[j] ^ rep.Eh[j]
			}
		} else {
			for j := 0; j < W-1; j++ {
				dst[j+1] = GF256Mul(twist, dst[j]) ^ rep.Eh[j]
			}
		}
	}

	// Rows 1 to M - 1: Vector row sweep
	if rep.NNZ == 0 {
		// Fast path: Curl-Free (harmonic field)
		for i := 1; i < M; i++ {
			v := rep.GammaV
			if !rep.ConstEv {
				v = rep.Ev[i-1]
			}
			rowCurr := i * W
			rowPrev := (i - 1) * W

			if twist <= 1 {
				// 64-bit SWAR vector unrolling
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
		// Sparse Curl stream: walk sparse curls in index order
		curlIdx := 0
		numCurls := len(rep.SparseCurls)
		var nextK uint32
		var nextC uint8
		if numCurls > 0 {
			nextK = rep.SparseCurls[0].Index
			nextC = rep.SparseCurls[0].Val
		}

		for i := 1; i < M; i++ {
			v := rep.GammaV
			if !rep.ConstEv {
				v = rep.Ev[i-1]
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
				if curlIdx < numCurls && curK == nextK {
					if twist <= 1 {
						curV ^= nextC
					} else {
						curV = GF256Mul(twist, curV) ^ nextC
					}
					curlIdx++
					if curlIdx < numCurls {
						nextK = rep.SparseCurls[curlIdx].Index
						nextC = rep.SparseCurls[curlIdx].Val
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

	return nil
}

// CompressCellularSheaf compresses an arbitrary byte stream using Cellular Sheaves with Discrete Hodge Laplacians.
// Frame layout:
//   - Header (24 bytes):
//     [0:4]   Magic (0x44485343, Little-Endian)
//     [4:8]   UncompressedSize (uint32 Little-Endian)
//     [8:10]  Width W (uint16 Little-Endian)
//     [10:12] Height M (uint16 Little-Endian)
//     [12]    Anchor (uint8)
//     [13]    GaloisTwist (uint8)
//     [14:16] Betti1 (uint16 Little-Endian)
//     [16:18] Flags (uint16 Little-Endian)
//     [18:24] Reserved (6B = 0)
//   - Payload:
//     [Homology coefficients if ConstEh / ConstEv]
//     [Boundary horizontal 1-forms Eh if not ConstEh]
//     [Boundary vertical 1-forms Ev if not ConstEv]
//     [Sparse curls if SparseCurl: NNZ uint32, then NNZ * (uint32 idx, uint8 val)]
//     [Trailing remainder bytes]
func CompressCellularSheaf(src []byte, level int) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}
	if n > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	// Buffers smaller than 16 bytes emit raw CSHD bypass frame
	if n < 16 {
		dst := make([]byte, CellularSheafHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_CELLULAR_SHEAF)
		binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
		binary.LittleEndian.PutUint16(dst[8:10], 0)
		binary.LittleEndian.PutUint16(dst[10:12], 0)
		dst[12] = 0
		dst[13] = 1
		binary.LittleEndian.PutUint16(dst[14:16], 0)
		binary.LittleEndian.PutUint16(dst[16:18], CSHDFlagRaw)
		copy(dst[CellularSheafHeaderSize:], src)
		return dst, nil
	}

	// Evaluate candidate strides
	bestW := 0
	bestM := 0
	bestTwist := uint8(1)
	bestSize := n + 1
	var bestRep *SheafBoundaryRep

	twists := []uint8{1}
	// If higher level (>= 3), also check for dominant multiplicative field ratio
	if level >= 3 && n >= 64 {
		ratioCount := make(map[uint8]int)
		sampleLen := min(n, 512)
		for i := 0; i+1 < sampleLen; i++ {
			if src[i] != 0 && src[i+1] != 0 {
				r := GF256Mul(src[i+1], GF256Inv(src[i]))
				if r > 1 {
					ratioCount[r]++
				}
			}
		}
		var topRatio uint8
		topFreq := 0
		for r, cnt := range ratioCount {
			if cnt > topFreq {
				topFreq = cnt
				topRatio = r
			}
		}
		if topFreq >= (sampleLen*6)/10 && topRatio > 1 {
			twists = append(twists, topRatio)
		}
	}

	for _, twist := range twists {
		for _, W := range CSHDCandidateStrides {
			if W >= n {
				continue
			}
			M := n / W
			if M < 2 {
				continue
			}

			rep := AnalyzeSheafDecomposition(src, M, W, twist)
			if rep == nil {
				continue
			}

			gridSize := M * W
			totalFaces := (M - 1) * (W - 1)
			remLen := len(rep.Remainder)

			// Compute representation byte size
			boundarySize := 0
			if rep.ConstEh {
				boundarySize += 1 // GammaH
			} else {
				boundarySize += (W - 1)
			}
			if rep.ConstEv {
				boundarySize += 1 // GammaV
			} else {
				boundarySize += (M - 1)
			}

			candidateSize := CellularSheafHeaderSize + boundarySize + remLen
			if rep.NNZ > 0 {
				candidateSize += 4 + rep.NNZ*5
			}

			// Accept candidate if it is either curl-free or sufficiently sparse
			isViable := false
			if rep.NNZ == 0 {
				isViable = true
			} else if rep.NNZ <= totalFaces/6 && candidateSize < int(float64(gridSize)*0.85) {
				isViable = true
			}

			if isViable && candidateSize < bestSize {
				bestSize = candidateSize
				bestW = W
				bestM = M
				bestTwist = twist
				bestRep = rep
			}
		}
	}

	if bestRep == nil || bestSize >= n {
		// Emit raw bypass frame so roundtrip decompressor succeeds identically
		dst := make([]byte, CellularSheafHeaderSize+n)
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_CELLULAR_SHEAF)
		binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
		binary.LittleEndian.PutUint16(dst[8:10], 0)
		binary.LittleEndian.PutUint16(dst[10:12], 0)
		dst[12] = 0
		dst[13] = 1
		binary.LittleEndian.PutUint16(dst[14:16], 0)
		binary.LittleEndian.PutUint16(dst[16:18], CSHDFlagRaw)
		copy(dst[CellularSheafHeaderSize:], src)
		return dst, nil
	}

	// Assemble payload
	flags := CSHDFlagCurlFree
	if bestRep.NNZ > 0 {
		flags = CSHDFlagSparseCurl
	}
	betti1 := uint16(0)
	if bestRep.ConstEh {
		flags |= CSHDFlagConstEh
		betti1++
	}
	if bestRep.ConstEv {
		flags |= CSHDFlagConstEv
		betti1++
	}

	payload := make([]byte, 0, bestSize-CellularSheafHeaderSize)

	if bestRep.ConstEh {
		payload = append(payload, bestRep.GammaH)
	}
	if bestRep.ConstEv {
		payload = append(payload, bestRep.GammaV)
	}
	if !bestRep.ConstEh {
		payload = append(payload, bestRep.Eh...)
	}
	if !bestRep.ConstEv {
		payload = append(payload, bestRep.Ev...)
	}

	if bestRep.NNZ > 0 {
		var nnzBuf [4]byte
		binary.LittleEndian.PutUint32(nnzBuf[:], uint32(bestRep.NNZ))
		payload = append(payload, nnzBuf[:]...)
		for _, curl := range bestRep.SparseCurls {
			var kBuf [4]byte
			binary.LittleEndian.PutUint32(kBuf[:], curl.Index)
			payload = append(payload, kBuf[:]...)
			payload = append(payload, curl.Val)
		}
	}

	if len(bestRep.Remainder) > 0 {
		payload = append(payload, bestRep.Remainder...)
	}

	// Test if PWS AVX-wave cascade yields even better compression on payload
	if packed, err := PWSEncode(payload); err == nil && len(packed)+CellularSheafHeaderSize < len(payload)+CellularSheafHeaderSize {
		flags |= CSHDFlagCascadedPWS
		dst := make([]byte, CellularSheafHeaderSize+len(packed))
		binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_CELLULAR_SHEAF)
		binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
		binary.LittleEndian.PutUint16(dst[8:10], uint16(bestW))
		binary.LittleEndian.PutUint16(dst[10:12], uint16(bestM))
		dst[12] = bestRep.Anchor
		dst[13] = bestTwist
		binary.LittleEndian.PutUint16(dst[14:16], betti1)
		binary.LittleEndian.PutUint16(dst[16:18], flags)
		copy(dst[CellularSheafHeaderSize:], packed)
		return dst, nil
	}

	// Normal uncascaded boundary frame
	dst := make([]byte, CellularSheafHeaderSize+len(payload))
	binary.LittleEndian.PutUint32(dst[0:4], PRZM_MAGIC_CELLULAR_SHEAF)
	binary.LittleEndian.PutUint32(dst[4:8], uint32(n))
	binary.LittleEndian.PutUint16(dst[8:10], uint16(bestW))
	binary.LittleEndian.PutUint16(dst[10:12], uint16(bestM))
	dst[12] = bestRep.Anchor
	dst[13] = bestTwist
	binary.LittleEndian.PutUint16(dst[14:16], betti1)
	binary.LittleEndian.PutUint16(dst[16:18], flags)
	copy(dst[CellularSheafHeaderSize:], payload)
	return dst, nil
}

// DecompressCellularSheaf decodes a PRZM_MAGIC_CELLULAR_SHEAF frame into dst.
// Strictly guarantees 0 heap allocations if dst has capacity >= origLen + 64.
func DecompressCellularSheaf(src, dst []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < CellularSheafHeaderSize {
		return nil, ErrCellularSheafCorrupt
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != PRZM_MAGIC_CELLULAR_SHEAF {
		return nil, ErrCellularSheafCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[4:8]))
	if origLen > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	if origLen == 0 {
		return []byte{}, nil
	}

	W := int(binary.LittleEndian.Uint16(src[8:10]))
	M := int(binary.LittleEndian.Uint16(src[10:12]))
	anchor := src[12]
	twist := src[13]
	flags := binary.LittleEndian.Uint16(src[16:18])

	payload := src[CellularSheafHeaderSize:]

	// Unpack cascaded PWS payload if present
	var pBuf *[]byte
	if (flags & CSHDFlagCascadedPWS) != 0 {
		pBuf = sheafPayloadPool.Get()
		defer sheafPayloadPool.Put(pBuf)

		innerDec, err := PWSDecodeTo(*pBuf, payload)
		if err != nil {
			return nil, err
		}
		payload = innerDec
	}

	if dst == nil || cap(dst) < origLen {
		dst = bufkit.AlignedBytes(origLen+64, bufkit.CacheLineSize)
	}
	dst = dst[:origLen]

	if (flags & (CSHDFlagCurlFree | CSHDFlagSparseCurl)) == 0 {
		// Raw passthrough
		if len(payload) < origLen {
			return nil, ErrCellularSheafCorrupt
		}
		copy(dst, payload[:origLen])
		return dst, nil
	}

	if W < 2 || M < 2 || W*M > origLen {
		return nil, ErrCellularSheafCorrupt
	}

	pos := 0
	gammaH := uint8(0)
	gammaV := uint8(0)
	constEh := (flags & CSHDFlagConstEh) != 0
	constEv := (flags & CSHDFlagConstEv) != 0

	if constEh {
		if pos >= len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		gammaH = payload[pos]
		pos++
	}
	if constEv {
		if pos >= len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		gammaV = payload[pos]
		pos++
	}

	var eh []uint8
	if !constEh {
		if pos+(W-1) > len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		eh = payload[pos : pos+(W-1)]
		pos += (W - 1)
	}

	var ev []uint8
	if !constEv {
		if pos+(M-1) > len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		ev = payload[pos : pos+(M-1)]
		pos += (M - 1)
	}

	nnz := 0
	var curlData []byte
	if (flags & CSHDFlagSparseCurl) != 0 {
		if pos+4 > len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		nnz = int(binary.LittleEndian.Uint32(payload[pos : pos+4]))
		pos += 4
		if pos+nnz*5 > len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		curlData = payload[pos : pos+nnz*5]
		pos += nnz * 5
	}

	// Row 0: Prefix-XOR from anchor
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

	// Rows 1 to M - 1: Vector row sweep
	if nnz == 0 {
		// Fast path: Curl-Free
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
		// Sparse Curl stream
		curlIdx := 0
		var nextK uint32
		var nextC uint8
		if nnz > 0 {
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
					if curlIdx < nnz {
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

	// Copy trailing remainder bytes if any
	gridSize := M * W
	rem := origLen - gridSize
	if rem > 0 {
		if pos+rem > len(payload) {
			return nil, ErrCellularSheafCorrupt
		}
		copy(dst[gridSize:], payload[pos:pos+rem])
	}

	return dst, nil
}

// Suppress unused import warning for bits
var _ = bits.OnesCount
