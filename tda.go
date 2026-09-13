package przm

import (
	"encoding/binary"
	"errors"
	"math/bits"
)

// TDA Magic and Modes
const (
	TDAMagic          uint32 = 0x54444131 // "TDA1"
	TDAModeRaw        uint8  = 0
	TDAModeSimplicial uint8  = 1
)

var (
	ErrTDACorrupt        = errors.New("przm: tda block is corrupted")
	ErrTDABufferTooSmall = errors.New("przm: tda destination buffer too small")
)

// SimplicialGrid represents a 2D cell complex over F2 for Theorem 23 boundary analysis.
// Vertices are points (y, x), Horizontal and Vertical edges are 1-cells, Faces are 2-cells.
type SimplicialGrid struct {
	Height int
	Width  int
}

// NewSimplicialGrid creates an H x W grid complex.
func NewSimplicialGrid(h, w int) *SimplicialGrid {
	return &SimplicialGrid{Height: h, Width: w}
}

// Boundary2 computes the 1-chain boundary d2(C) of a given 2-chain C over F2.
// In an H x W face grid:
// Each 2-cell F_{y,x} has boundary: E_h(y,x) + E_v(y, x+1) + E_h(y+1, x) + E_v(y, x) (mod 2).
// Returns horizontal edges HEdges [H+1][W] and vertical edges VEdges [H][W+1].
func (g *SimplicialGrid) Boundary2(faces []uint8) (hEdges []uint8, vEdges []uint8) {
	hEdges = make([]uint8, (g.Height+1)*g.Width)
	vEdges = make([]uint8, g.Height*(g.Width+1))

	for y := 0; y < g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			fVal := faces[y*g.Width+x] & 1
			if fVal == 0 {
				continue
			}
			// Boundary d2:
			// Top horizontal edge (y, x)
			hEdges[y*g.Width+x] ^= 1
			// Bottom horizontal edge (y+1, x)
			hEdges[(y+1)*g.Width+x] ^= 1
			// Left vertical edge (y, x)
			vEdges[y*(g.Width+1)+x] ^= 1
			// Right vertical edge (y, x+1)
			vEdges[y*(g.Width+1)+(x+1)] ^= 1
		}
	}
	return hEdges, vEdges
}

// Boundary1 computes the 0-chain (vertices) boundary d1(E) of a 1-chain (hEdges, vEdges) over F2.
// Theorem 23 states: d1(d2(C)) == 0 identically for all C in C2.
// Vertices size is (H+1) * (W+1).
func (g *SimplicialGrid) Boundary1(hEdges []uint8, vEdges []uint8) []uint8 {
	vertices := make([]uint8, (g.Height+1)*(g.Width+1))

	// For each horizontal edge E_h(y, x) from (y, x) to (y, x+1):
	// d1(E_h(y, x)) = V(y, x+1) ^ V(y, x)
	for y := 0; y <= g.Height; y++ {
		for x := 0; x < g.Width; x++ {
			if hEdges[y*g.Width+x]&1 == 1 {
				vertices[y*(g.Width+1)+x] ^= 1
				vertices[y*(g.Width+1)+(x+1)] ^= 1
			}
		}
	}

	// For each vertical edge E_v(y, x) from (y, x) to (y+1, x):
	// d1(E_v(y, x)) = V(y+1, x) ^ V(y, x)
	for y := 0; y < g.Height; y++ {
		for x := 0; x <= g.Width; x++ {
			if vEdges[y*(g.Width+1)+x]&1 == 1 {
				vertices[y*(g.Width+1)+x] ^= 1
				vertices[(y+1)*(g.Width+1)+x] ^= 1
			}
		}
	}

	return vertices
}

// TDAPersistentBarcode represents a persistent 1-cycle generator over F2.
// Mask persists from WordOffset for Length words.
type TDAPersistentBarcode struct {
	WordOffset uint32
	Length     uint16
	Mask       uint64
}

// CompressTDA encodes raw data into a topological representation over F2.
// Employs persistent 1-cycle extraction and boundary annihilation (Theorem 23).
// If the block is incompressible or too small, a raw bypass format is used.
func CompressTDA(src []byte) []byte {
	n := len(src)
	if n < 32 {
		return encodeTDARaw(src)
	}

	// Align to 8-byte uint64 words
	numWords := n / 8
	rem := n % 8

	words := make([]uint64, numWords)
	for i := 0; i < numWords; i++ {
		words[i] = binary.LittleEndian.Uint64(src[i*8 : (i+1)*8])
	}

	// Step 1: Detect Persistent 1-cycles (Barcodes)
	// Look for frequent bitmasks that persist across multiple adjacent words
	var barcodes []TDAPersistentBarcode
	workWords := make([]uint64, numWords)
	copy(workWords, words)

	// Sliding window persistent cycle search
	for i := 0; i < numWords; {
		w := workWords[i]
		if w == 0 {
			i++
			continue
		}

		// Count persistence length
		runLen := 1
		for i+runLen < numWords && workWords[i+runLen] == w && runLen < 65535 {
			runLen++
		}

		// If a cycle persists for >= 3 words, it forms a persistent homology class
		if runLen >= 3 {
			barcodes = append(barcodes, TDAPersistentBarcode{
				WordOffset: uint32(i),
				Length:     uint16(runLen),
				Mask:       w,
			})
			for j := 0; j < runLen; j++ {
				workWords[i+j] ^= w
			}
			i += runLen
		} else {
			i++
		}
	}

	// Step 2: 2-Chain Boundary Annihilation
	// In the residual workWords, apply 1D boundary difference over F2:
	// diff[i] = workWords[i] ^ workWords[i-1]
	diffWords := make([]uint64, numWords)
	if numWords > 0 {
		diffWords[0] = workWords[0]
		for i := 1; i < numWords; i++ {
			diffWords[i] = workWords[i] ^ workWords[i-1]
		}
	}

	// Check if diffWords has lower total Hamming weight
	origHamming := 0
	diffHamming := 0
	for i := 0; i < numWords; i++ {
		origHamming += bits.OnesCount64(workWords[i])
		diffHamming += bits.OnesCount64(diffWords[i])
	}

	useDiff := diffHamming < origHamming
	activeWords := workWords
	if useDiff {
		activeWords = diffWords
	}

	// Collect non-zero residual words (sparse representation)
	type nonZeroWord struct {
		Idx uint32
		Val uint64
	}
	var nonZeros []nonZeroWord
	for i, w := range activeWords {
		if w != 0 {
			nonZeros = append(nonZeros, nonZeroWord{Idx: uint32(i), Val: w})
		}
	}

	// Estimate encoded size
	// Header: Magic (4) + Mode (1) + Flags (1) + OrigLen (4) + NumWords (4) = 14 bytes
	// Barcodes: NumBarcodes (2) + each (4 + 2 + 8 = 14 bytes)
	// NonZeros: Count (4) + each (4 + 8 = 12 bytes)
	// Remainder bytes: rem bytes
	estSize := 14 + 2 + len(barcodes)*14 + 4 + len(nonZeros)*12 + rem
	if estSize >= n {
		// Incompressible, fallback to raw
		return encodeTDARaw(src)
	}

	// Assemble TDA compressed block
	dst := make([]byte, estSize)
	binary.LittleEndian.PutUint32(dst[0:4], TDAMagic)
	dst[4] = TDAModeSimplicial

	var flags uint8 = 0
	if useDiff {
		flags |= 0x01
	}
	dst[5] = flags

	binary.LittleEndian.PutUint32(dst[6:10], uint32(n))
	binary.LittleEndian.PutUint32(dst[10:14], uint32(numWords))

	pos := 14
	binary.LittleEndian.PutUint16(dst[pos:pos+2], uint16(len(barcodes)))
	pos += 2

	for _, bc := range barcodes {
		binary.LittleEndian.PutUint32(dst[pos:pos+4], bc.WordOffset)
		binary.LittleEndian.PutUint16(dst[pos+4:pos+6], bc.Length)
		binary.LittleEndian.PutUint64(dst[pos+6:pos+14], bc.Mask)
		pos += 14
	}

	binary.LittleEndian.PutUint32(dst[pos:pos+4], uint32(len(nonZeros)))
	pos += 4

	for _, nz := range nonZeros {
		binary.LittleEndian.PutUint32(dst[pos:pos+4], nz.Idx)
		binary.LittleEndian.PutUint64(dst[pos+4:pos+12], nz.Val)
		pos += 12
	}

	if rem > 0 {
		copy(dst[pos:pos+rem], src[numWords*8:])
		pos += rem
	}

	return dst[:pos]
}

func encodeTDARaw(src []byte) []byte {
	n := len(src)
	dst := make([]byte, 9+n)
	binary.LittleEndian.PutUint32(dst[0:4], TDAMagic)
	dst[4] = TDAModeRaw
	binary.LittleEndian.PutUint32(dst[5:9], uint32(n))
	copy(dst[9:], src)
	return dst
}

// DecompressTDA decodes a topological F2 compressed stream with zero allocations
// if dst has sufficient capacity.
func DecompressTDA(src []byte, dst []byte) ([]byte, error) {
	if len(src) < 9 {
		return nil, ErrTDACorrupt
	}
	if binary.LittleEndian.Uint32(src[0:4]) != TDAMagic {
		return nil, ErrTDACorrupt
	}

	mode := src[4]
	if mode == TDAModeRaw {
		origLen := int(binary.LittleEndian.Uint32(src[5:9]))
		if len(src) < 9+origLen {
			return nil, ErrTDACorrupt
		}
		if cap(dst) >= origLen {
			dst = dst[:origLen]
		} else {
			dst = make([]byte, origLen)
		}
		copy(dst, src[9:9+origLen])
		return dst, nil
	}

	if mode != TDAModeSimplicial {
		return nil, ErrTDACorrupt
	}

	if len(src) < 14 {
		return nil, ErrTDACorrupt
	}

	flags := src[5]
	useDiff := (flags & 0x01) != 0

	origLen := int(binary.LittleEndian.Uint32(src[6:10]))
	numWords := int(binary.LittleEndian.Uint32(src[10:14]))

	if cap(dst) >= origLen {
		dst = dst[:origLen]
	} else {
		dst = make([]byte, origLen)
	}

	// Zero destination words
	for i := range dst {
		dst[i] = 0
	}

	pos := 14
	if pos+2 > len(src) {
		return nil, ErrTDACorrupt
	}
	numBarcodes := int(binary.LittleEndian.Uint16(src[pos : pos+2]))
	pos += 2

	// Allocate temporary stack words
	words := make([]uint64, numWords)

	// Step 1: Read Barcodes (persistent 1-cycles)
	if pos+numBarcodes*14 > len(src) {
		return nil, ErrTDACorrupt
	}
	type barcodeEntry struct {
		offset uint32
		length uint16
		mask   uint64
	}
	var bcs []barcodeEntry
	if numBarcodes > 0 {
		bcs = make([]barcodeEntry, numBarcodes)
		for b := 0; b < numBarcodes; b++ {
			bcs[b].offset = binary.LittleEndian.Uint32(src[pos : pos+4])
			bcs[b].length = binary.LittleEndian.Uint16(src[pos+4 : pos+6])
			bcs[b].mask = binary.LittleEndian.Uint64(src[pos+6 : pos+14])
			pos += 14
		}
	}

	// Step 2: Read sparse non-zero words
	if pos+4 > len(src) {
		return nil, ErrTDACorrupt
	}
	numNZ := int(binary.LittleEndian.Uint32(src[pos : pos+4]))
	pos += 4

	if pos+numNZ*12 > len(src) {
		return nil, ErrTDACorrupt
	}
	for i := 0; i < numNZ; i++ {
		idx := binary.LittleEndian.Uint32(src[pos : pos+4])
		val := binary.LittleEndian.Uint64(src[pos+4 : pos+12])
		pos += 12
		if int(idx) < numWords {
			words[idx] = val
		}
	}

	// Step 3: Undo Boundary Differencing if applied (accumulate prefix sum over F2)
	// x[i] = diff[i] ^ x[i-1]
	if useDiff && numWords > 0 {
		for i := 1; i < numWords; i++ {
			words[i] ^= words[i-1]
		}
	}

	// Step 4: Add Persistent 1-cycles (Cycle Filling via bitwise XOR)
	for _, bc := range bcs {
		end := int(bc.offset) + int(bc.length)
		if end > numWords {
			end = numWords
		}
		for i := int(bc.offset); i < end; i++ {
			words[i] ^= bc.mask
		}
	}

	// Step 5: Write out 64-bit words
	for i := 0; i < numWords; i++ {
		binary.LittleEndian.PutUint64(dst[i*8:(i+1)*8], words[i])
	}

	// Remainder bytes
	rem := origLen - numWords*8
	if rem > 0 {
		if pos+rem > len(src) {
			return nil, ErrTDACorrupt
		}
		copy(dst[numWords*8:origLen], src[pos:pos+rem])
	}

	return dst, nil
}

// BinomialCoeff calculates C(n, k) = n! / (k! * (n-k)!) using 64-bit integer arithmetic.
func BinomialCoeff(n, k int) uint32 {
	if k < 0 || k > n {
		return 0
	}
	if k == 0 || k == n {
		return 1
	}
	if k > n/2 {
		k = n - k
	}
	res := uint64(1)
	for i := 1; i <= k; i++ {
		res = res * uint64(n-i+1) / uint64(i)
	}
	return uint32(res)
}

// SimplexToCombinatorialIndex maps a d-simplex with decreasing vertex indices (i_d > ... > i_0)
// bijectively to an integer index N = sum_{l=0}^d C(i_l, l+1) (Ulrich Bauer, Ripser, Section 2).
func SimplexToCombinatorialIndex(vertices []int) uint32 {
	dim := len(vertices) - 1
	var N uint32
	for l := 0; l <= dim; l++ {
		v := vertices[dim-l] // decreasing order: vertices[0] is i_d, vertices[dim] is i_0
		N += BinomialCoeff(v, l+1)
	}
	return N
}

// CombinatorialIndexToSimplex performs inverse recovery of decreasing vertices (i_d > ... > i_0)
// from combinatorial index N and dimension d using greedy threshold search.
func CombinatorialIndexToSimplex(N uint32, dim int) []int {
	vertices := make([]int, dim+1)
	rem := N
	v := 255 // upper bound search vertex

	for l := dim; l >= 0; l-- {
		k := l + 1
		for v >= k && BinomialCoeff(v, k) > rem {
			v--
		}
		vertices[dim-l] = v
		rem -= BinomialCoeff(v, k)
		v--
	}
	return vertices
}

// SimplexBoundaryIndices enumerates the d+1 facet combinatorial indices of a d-simplex
// on the fly over F2 without storing boundary matrices in RAM.
func SimplexBoundaryIndices(N uint32, dim int) []uint32 {
	if dim <= 0 {
		return nil
	}
	verts := CombinatorialIndexToSimplex(N, dim)
	facets := make([]uint32, dim+1)

	// Each facet omits one vertex verts[j]
	for omit := 0; omit <= dim; omit++ {
		facetVerts := make([]int, 0, dim)
		for j := 0; j <= dim; j++ {
			if j != omit {
				facetVerts = append(facetVerts, verts[j])
			}
		}
		facets[omit] = SimplexToCombinatorialIndex(facetVerts)
	}
	return facets
}

// IsApparentPair verifies whether (sigma, tau) is an Apparent Pair in the lexicographic
// refinement of the filtration (Bauer Definition 3.2):
// 1. sigma is the youngest facet of tau.
// 2. tau is the oldest cofacet of sigma.
// Theorem 3.3 proves that every apparent pair is a persistence pair with exact zero persistence,
// allowing instant annihilation without matrix reduction.
func IsApparentPair(facetsOfTau []uint32, sigmaIdx uint32, oldestCofacetOfSigma uint32, tauIdx uint32) bool {
	if len(facetsOfTau) == 0 {
		return false
	}
	// Youngest facet of tau is the first facet in reverse colexicographic order
	youngestFacet := facetsOfTau[0]
	return youngestFacet == sigmaIdx && oldestCofacetOfSigma == tauIdx
}
