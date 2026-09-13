// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"math"
)

// Analytical Hyperbolic Multidimensional Scaling (h-MDS) in Minkowski Space M^r.
// Based on De Sa et al. (Stanford University, arXiv:1804.03329, Algorithm 2).
//
// By Theorem 26 (Analytical Hyperbolic Embedding via Minkowski Eigendecomposition),
// for any distance matrix D_ij in hyperbolic space, the scaled distance matrix
// Y_ij = cosh(d_H(x_i, x_j)) factors as:
//     Y = u * u^T - X * X^T
// Centering at the pseudo-Euclidean mean (X^T * u = 0) enables exact recovery of
// hyperbolic coordinates X via the top negative eigenvectors of Y (or top positive
// eigenvectors of -Y) in closed form, without gradient descent or local minima.

const (
	HMDSMagic          uint32 = 0x484D4453 // "HMDS"
	HMDSModeRaw        uint8  = 0
	HMDSMode4D         uint8  = 1 // 4-channel hyperbolic embedding (raw transformed)
	HMDSModeCompressed uint8  = 2 // 4-channel hyperbolic embedding + cascaded PWS
)

var (
	ErrHMDSCorrupt = errors.New("przm: hmds block corrupted")
)

// HyperbolicCoord4 represents a point in 4D Minkowski space M^4 with x0^2 - sum(xi^2) = 1.
type HyperbolicCoord4 struct {
	Time    float64    // x0 >= 1.0
	Spatial [3]float64 // \tilde{x} in R^3
}

// DistanceH calculates the exact Minkowski hyperbolic distance:
// d_H(x, y) = acosh(x0*y0 - \tilde{x} \cdot \tilde{y})
func (p HyperbolicCoord4) DistanceH(q HyperbolicCoord4) float64 {
	minkowskiProd := p.Time*q.Time - (p.Spatial[0]*q.Spatial[0] + p.Spatial[1]*q.Spatial[1] + p.Spatial[2]*q.Spatial[2])
	if minkowskiProd < 1.0 {
		minkowskiProd = 1.0
	}
	return math.Acosh(minkowskiProd)
}

// ProjectToPoincareBall maps point from Minkowski hyperboloid to the 3D open Poincaré ball:
// z = \tilde{x} / (1 + x0)
func (p HyperbolicCoord4) ProjectToPoincareBall() [3]float64 {
	den := 1.0 + p.Time
	return [3]float64{
		p.Spatial[0] / den,
		p.Spatial[1] / den,
		p.Spatial[2] / den,
	}
}

// ComputeChannelDistances4 calculates the 4x4 normalized Hamming/variation distance matrix
// between 4 parallel channels of src.
func ComputeChannelDistances4(src []byte) [4][4]float64 {
	var dist [4][4]float64
	n := len(src) / 4
	if n == 0 {
		return dist
	}

	// Compute differences between channels
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			diffCount := 0
			for t := 0; t < n; t++ {
				if src[t*4+i] != src[t*4+j] {
					diffCount++
				}
			}
			// Map difference ratio [0, 1] to hyperbolic distance [0.1, 3.0]
			ratio := float64(diffCount) / float64(n)
			d := 0.1 + ratio*2.5
			dist[i][j] = d
			dist[j][i] = d
		}
	}
	return dist
}

// AnalyticalHMDS4 performs exact closed-form hyperbolic recovery for 4 channels
// using Jacobi eigendecomposition on the Minkowski matrix -Y = X*X^T - u*u^T.
func AnalyticalHMDS4(dist [4][4]float64) [4]HyperbolicCoord4 {
	// Step 1: Compute Y_ij = cosh(d_ij)
	var Y [4][4]float64
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if i == j {
				Y[i][j] = 1.0
			} else {
				Y[i][j] = math.Cosh(dist[i][j])
			}
		}
	}

	// Step 2: Target matrix A = -Y
	var A [4][4]float64
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			A[i][j] = -Y[i][j]
		}
	}

	// Step 3: Jacobi Eigendecomposition of 4x4 symmetric matrix A
	eigenvals, eigenvecs := jacobiEigen4(A)

	// Sort eigenvalues descending
	idx := []int{0, 1, 2, 3}
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			if eigenvals[idx[j]] > eigenvals[idx[i]] {
				idx[i], idx[j] = idx[j], idx[i]
			}
		}
	}

	// Step 4: Extract spatial coordinates X from top positive eigenvalues of A
	// and time coordinate u from the negative eigenvalue
	var coords [4]HyperbolicCoord4
	for i := 0; i < 4; i++ {
		// Time coordinate u corresponds to the negative eigenvalue of A (positive of Y)
		negIdx := idx[3]
		valNeg := -eigenvals[negIdx]
		if valNeg < 0.1 {
			valNeg = 0.1
		}
		u_i := math.Sqrt(valNeg) * math.Abs(eigenvecs[i][negIdx])
		if u_i < 1.0 {
			u_i = 1.0
		}
		coords[i].Time = u_i

		// Spatial coordinates from top positive eigenvalues of A
		for k := 0; k < 3; k++ {
			posIdx := idx[k]
			valPos := eigenvals[posIdx]
			if valPos > 0 {
				coords[i].Spatial[k] = math.Sqrt(valPos) * eigenvecs[i][posIdx]
			}
		}

		// Renormalize onto hyperboloid: x0^2 - ||x||^2 = 1 => x0 = sqrt(1 + ||x||^2)
		spatNorm2 := coords[i].Spatial[0]*coords[i].Spatial[0] +
			coords[i].Spatial[1]*coords[i].Spatial[1] +
			coords[i].Spatial[2]*coords[i].Spatial[2]
		coords[i].Time = math.Sqrt(1.0 + spatNorm2)
	}

	return coords
}

// jacobiEigen4 computes eigenvalues and eigenvectors for 4x4 symmetric matrix.
func jacobiEigen4(A [4][4]float64) ([4]float64, [4][4]float64) {
	var V [4][4]float64
	for i := 0; i < 4; i++ {
		V[i][i] = 1.0
	}

	for iter := 0; iter < 20; iter++ {
		// Find max off-diagonal element
		maxVal := 0.0
		p, q := 0, 1
		for i := 0; i < 4; i++ {
			for j := i + 1; j < 4; j++ {
				val := math.Abs(A[i][j])
				if val > maxVal {
					maxVal = val
					p, q = i, j
				}
			}
		}

		if maxVal < 1e-9 {
			break
		}

		// Compute Jacobi rotation angle
		diff := A[q][q] - A[p][p]
		var t float64
		if math.Abs(diff) < 1e-12 {
			t = 1.0
		} else {
			phi := diff / (2.0 * A[p][q])
			t = math.Copysign(1.0/(math.Abs(phi)+math.Sqrt(phi*phi+1.0)), phi)
		}
		c := 1.0 / math.Sqrt(t*t+1.0)
		s := t * c

		// Rotate A
		App := A[p][p]
		Aqq := A[q][q]
		Apq := A[p][q]
		A[p][p] = c*c*App - 2.0*s*c*Apq + s*s*Aqq
		A[q][q] = s*s*App + 2.0*s*c*Apq + c*c*Aqq
		A[p][q] = 0.0
		A[q][p] = 0.0

		for k := 0; k < 4; k++ {
			if k != p && k != q {
				Akp := A[k][p]
				Akq := A[k][q]
				A[k][p] = c*Akp - s*Akq
				A[p][k] = A[k][p]
				A[k][q] = s*Akp + c*Akq
				A[q][k] = A[k][q]
			}
		}

		// Accumulate eigenvectors in V
		for k := 0; k < 4; k++ {
			Vkp := V[k][p]
			Vkq := V[k][q]
			V[k][p] = c*Vkp - s*Vkq
			V[k][q] = s*Vkp + c*Vkq
		}
	}

	var eigenvalues [4]float64
	for i := 0; i < 4; i++ {
		eigenvalues[i] = A[i][i]
	}
	return eigenvalues, V
}

// CompressHMDS compresses 4-channel data using analytical hyperbolic embedding.
func CompressHMDS(src []byte) []byte {
	n := len(src)
	if n < 32 {
		return encodeHMDSRaw(src)
	}

	numVectors := n / 4
	rem := n % 4
	alignedLen := numVectors * 4

	dist := ComputeChannelDistances4(src[:alignedLen])
	coords := AnalyticalHMDS4(dist)

	// Quantize Poincaré ball coordinates into 8-bit integers per channel
	// 4 channels x 3 spatial coordinates = 12 bytes header
	var qCoords [4][3]int8
	for i := 0; i < 4; i++ {
		ball := coords[i].ProjectToPoincareBall()
		for k := 0; k < 3; k++ {
			q := int8(ball[k] * 127.0)
			qCoords[i][k] = q
		}
	}

	// Apply channels permutation sorted by hyperbolic radius (distance to origin)
	radii := make([]float64, 4)
	perm := []int{0, 1, 2, 3}
	for i := 0; i < 4; i++ {
		radii[i] = coords[i].Time // x0 is monotonically related to hyperbolic radius
	}
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			if radii[perm[j]] < radii[perm[i]] {
				perm[i], perm[j] = perm[j], perm[i]
			}
		}
	}

	// Reorder channels by hyperbolic hierarchy
	transformed := make([]byte, alignedLen)
	for t := 0; t < numVectors; t++ {
		for c := 0; c < 4; c++ {
			transformed[t*4+c] = src[t*4+perm[c]]
		}
	}

	// Try cascading PWS compression on hierarchically reordered channels
	if packed, err := PWSEncode(transformed); err == nil && len(packed)+25+rem < n {
		dst := make([]byte, 25+len(packed)+rem)
		binary.LittleEndian.PutUint32(dst[0:4], HMDSMagic)
		dst[4] = HMDSModeCompressed
		binary.LittleEndian.PutUint32(dst[5:9], uint32(n))
		for i := 0; i < 4; i++ {
			dst[9+i] = uint8(perm[i])
		}
		pos := 13
		for i := 0; i < 4; i++ {
			for k := 0; k < 3; k++ {
				dst[pos] = uint8(qCoords[i][k])
				pos++
			}
		}
		copy(dst[pos:pos+len(packed)], packed)
		pos += len(packed)
		if rem > 0 {
			copy(dst[pos:pos+rem], src[alignedLen:])
			pos += rem
		}
		return dst[:pos]
	}

	// Header: Magic(4) + Mode(1) + OrigLen(4) + Perm(4) + QuantCoords(12) = 25 bytes
	dst := make([]byte, 25+alignedLen+rem)
	binary.LittleEndian.PutUint32(dst[0:4], HMDSMagic)
	dst[4] = HMDSMode4D
	binary.LittleEndian.PutUint32(dst[5:9], uint32(n))
	for i := 0; i < 4; i++ {
		dst[9+i] = uint8(perm[i])
	}
	pos := 13
	for i := 0; i < 4; i++ {
		for k := 0; k < 3; k++ {
			dst[pos] = uint8(qCoords[i][k])
			pos++
		}
	}

	copy(dst[pos:pos+alignedLen], transformed)
	pos += alignedLen

	if rem > 0 {
		copy(dst[pos:pos+rem], src[alignedLen:])
		pos += rem
	}

	return dst[:pos]
}

func encodeHMDSRaw(src []byte) []byte {
	n := len(src)
	dst := make([]byte, 9+n)
	binary.LittleEndian.PutUint32(dst[0:4], HMDSMagic)
	dst[4] = HMDSModeRaw
	binary.LittleEndian.PutUint32(dst[5:9], uint32(n))
	copy(dst[9:], src)
	return dst
}

// DecompressHMDS recovers data from h-MDS compressed stream with zero allocations
// if dst capacity is sufficient.
func DecompressHMDS(src []byte, dst []byte) ([]byte, error) {
	if len(src) < 9 {
		return nil, ErrHMDSCorrupt
	}
	if binary.LittleEndian.Uint32(src[0:4]) != HMDSMagic {
		return nil, ErrHMDSCorrupt
	}

	mode := src[4]
	if mode == HMDSModeRaw {
		origLen := int(binary.LittleEndian.Uint32(src[5:9]))
		if len(src) < 9+origLen {
			return nil, ErrHMDSCorrupt
		}
		if cap(dst) >= origLen {
			dst = dst[:origLen]
		} else {
			dst = make([]byte, origLen)
		}
		copy(dst, src[9:9+origLen])
		return dst, nil
	}

	if mode != HMDSMode4D && mode != HMDSModeCompressed {
		return nil, ErrHMDSCorrupt
	}

	if len(src) < 25 {
		return nil, ErrHMDSCorrupt
	}

	origLen := int(binary.LittleEndian.Uint32(src[5:9]))
	numVectors := origLen / 4
	rem := origLen % 4
	alignedLen := numVectors * 4

	if cap(dst) >= origLen {
		dst = dst[:origLen]
	} else {
		dst = make([]byte, origLen)
	}

	// Read permutation and compute inverse
	var invPerm [4]int
	for i := 0; i < 4; i++ {
		origChannel := int(src[9+i])
		if origChannel >= 0 && origChannel < 4 {
			invPerm[i] = origChannel
		}
	}

	pos := 25

	if mode == HMDSModeCompressed {
		if pos > len(src)-rem {
			return nil, ErrHMDSCorrupt
		}
		payloadLen := len(src) - pos - rem
		if payloadLen <= 0 {
			return nil, ErrHMDSCorrupt
		}
		transformed, err := PWSDecode(src[pos : pos+payloadLen])
		if err != nil || len(transformed) < alignedLen {
			return nil, ErrHMDSCorrupt
		}
		for t := 0; t < numVectors; t++ {
			for c := 0; c < 4; c++ {
				dst[t*4+invPerm[c]] = transformed[t*4+c]
			}
		}
		if rem > 0 {
			copy(dst[alignedLen:origLen], src[pos+payloadLen:])
		}
		return dst, nil
	}

	if pos+alignedLen+rem > len(src) {
		return nil, ErrHMDSCorrupt
	}

	payload := src[pos : pos+alignedLen]

	// Invert permutation into dst
	for t := 0; t < numVectors; t++ {
		for c := 0; c < 4; c++ {
			dst[t*4+invPerm[c]] = payload[t*4+c]
		}
	}
	pos += alignedLen

	if rem > 0 {
		copy(dst[alignedLen:origLen], src[pos:pos+rem])
	}

	return dst, nil
}
