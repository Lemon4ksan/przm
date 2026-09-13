package przm

import (
	"encoding/binary"
	"errors"
	"math"
)

// Spectral Unitary Constants
const (
	SpectralMagic          uint32 = 0x53504543 // "SPEC"
	SpectralModeRaw        uint8  = 0
	SpectralModeU4         uint8  = 1 // 4x4 Unitary spectral reduction over F_{2^8} (raw transformed)
	SpectralModeCompressed uint8  = 2 // 4x4 Unitary with cascaded PWS-compressed payload
)

var (
	ErrSpectralCorrupt = errors.New("przm: spectral block corrupted")
)

// UnitaryMatrix4 is a 4x4 orthogonal matrix in F_{2^8} satisfying U * U^T = I.
type UnitaryMatrix4 [4][4]uint8

// IdentityUnitary4 returns the 4x4 identity matrix over F_{2^8}.
func IdentityUnitary4() UnitaryMatrix4 {
	return UnitaryMatrix4{
		{1, 0, 0, 0},
		{0, 1, 0, 0},
		{0, 0, 1, 0},
		{0, 0, 0, 1},
	}
}

// Mul multiplies two 4x4 matrices over F_{2^8}.
func (u UnitaryMatrix4) Mul(v UnitaryMatrix4) UnitaryMatrix4 {
	var res UnitaryMatrix4
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			var sum uint8
			for k := 0; k < 4; k++ {
				sum ^= GF256Mul(u[i][k], v[k][j])
			}
			res[i][j] = sum
		}
	}
	return res
}

// Transpose returns the transpose U^T. Over F_{2^8}, U^T = U^{-1} for orthogonal matrices.
func (u UnitaryMatrix4) Transpose() UnitaryMatrix4 {
	var res UnitaryMatrix4
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			res[i][j] = u[j][i]
		}
	}
	return res
}

// IsUnitary checks if U * U^T == I in F_{2^8}.
func (u UnitaryMatrix4) IsUnitary() bool {
	prod := u.Mul(u.Transpose())
	id := IdentityUnitary4()
	return prod == id
}

// GivensRotation4 creates an elementary orthogonal Givens rotation in F_{2^8}
// acting on channels (chA, chB) with parameter c in F_{2^8}.
// Because in characteristic 2: c^2 + (c ^ 1)^2 = (c ^ c ^ 1)^2 = 1,
// setting s = c ^ 1 always yields an orthogonal 2x2 block [c, s; s, c].
func GivensRotation4(chA, chB int, c uint8) UnitaryMatrix4 {
	u := IdentityUnitary4()
	s := c ^ 1
	u[chA][chA] = c
	u[chA][chB] = s
	u[chB][chA] = s
	u[chB][chB] = c
	return u
}

// ComputeGramMatrix4 computes the 4x4 Gram/covariance matrix rho = X^T * X over F_{2^8}
// where rows of X are 4-byte vectors.
func ComputeGramMatrix4(data []byte) [4][4]uint8 {
	var rho [4][4]uint8
	n := len(data) / 4
	for t := 0; t < n; t++ {
		v := data[t*4 : (t+1)*4]
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				rho[i][j] ^= GF256Mul(v[i], v[j])
			}
		}
	}
	return rho
}

// OffDiagonalSum returns the sum of off-diagonal magnitudes in rho.
func OffDiagonalSum(rho [4][4]uint8) int {
	sum := 0
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			if i != j {
				sum += int(rho[i][j])
			}
		}
	}
	return sum
}

// VonNeumannEntropy calculates S(rho) = -sum(lambda_i * log2(lambda_i)) from normalized diagonal eigenvalues.
func VonNeumannEntropy(diag [4]uint8) float64 {
	total := 0
	for _, v := range diag {
		total += int(v)
	}
	if total == 0 {
		return 0.0
	}

	entropy := 0.0
	for _, v := range diag {
		if v > 0 {
			p := float64(v) / float64(total)
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// FindOptimalUnitary4 searches for a unitary matrix U that minimizes off-diagonal covariance.
func FindOptimalUnitary4(rho [4][4]uint8) UnitaryMatrix4 {
	bestU := IdentityUnitary4()
	bestScore := OffDiagonalSum(rho)

	// Sweep through all channel pairs and candidate discrete angles
	pairs := [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}
	for _, p := range pairs {
		chA, chB := p[0], p[1]
		// Try sample Givens rotations over F_{2^8}
		for c := 0; c < 256; c += 16 {
			candG := GivensRotation4(chA, chB, uint8(c))
			candU := bestU.Mul(candG)
			// Transform rho: rho' = candU * rho * candU^T
			rhoPrime := candU.Mul(UnitaryMatrix4(rho)).Mul(candU.Transpose())
			score := OffDiagonalSum([4][4]uint8(rhoPrime))
			if score < bestScore {
				bestScore = score
				bestU = candU
			}
		}
	}
	return bestU
}

// ApplyUnitary4 transforms rows of src: Y = X * U^T over F_{2^8}.
func ApplyUnitary4(src []byte, U UnitaryMatrix4, dst []byte) {
	n := len(src) / 4
	for t := 0; t < n; t++ {
		s := src[t*4 : (t+1)*4]
		d := dst[t*4 : (t+1)*4]

		// Y[j] = sum_k (X[k] * U^T[k][j]) = sum_k (X[k] * U[j][k])
		for j := 0; j < 4; j++ {
			var sum uint8
			for k := 0; k < 4; k++ {
				sum ^= GF256Mul(s[k], U[j][k])
			}
			d[j] = sum
		}
	}
}

// InvertUnitary4 reconstructs rows of X from Y: X = Y * U over F_{2^8}.
// Zero allocations: operates directly on slice buffers.
func InvertUnitary4(src []byte, U UnitaryMatrix4, dst []byte) {
	n := len(src) / 4
	for t := 0; t < n; t++ {
		s := src[t*4 : (t+1)*4]
		d := dst[t*4 : (t+1)*4]

		// X[j] = sum_k (Y[k] * U[k][j])
		for j := 0; j < 4; j++ {
			var sum uint8
			for k := 0; k < 4; k++ {
				sum ^= GF256Mul(s[k], U[k][j])
			}
			d[j] = sum
		}
	}
}

// CompressSpectralUnitary compresses src using unitary spectral reduction over F_{2^8}.
func CompressSpectralUnitary(src []byte) []byte {
	n := len(src)
	if n < 32 {
		return encodeSpectralRaw(src)
	}

	numVectors := n / 4
	rem := n % 4

	alignedLen := numVectors * 4
	rho := ComputeGramMatrix4(src[:alignedLen])
	U := FindOptimalUnitary4(rho)

	// Transform data
	transformed := make([]byte, alignedLen)
	ApplyUnitary4(src[:alignedLen], U, transformed)

	// Channel RLE / zero check: check if unitary transform created zero runs in channels
	zeroCount := 0
	for _, b := range transformed {
		if b == 0 {
			zeroCount++
		}
	}

	// If zero concentration is significant, store transformed data
	// Header: Magic (4) + Mode (1) + OrigLen (4) + Matrix U (16) = 25 bytes
	headerSize := 25
	if zeroCount*4 < alignedLen {
		// Not enough compression gain, fallback to raw
		return encodeSpectralRaw(src)
	}

	// Try cascading PWS compression on zero-concentrated channels
	if packed, err := PWSEncode(transformed); err == nil && len(packed)+headerSize+rem < n {
		dst := make([]byte, headerSize+len(packed)+rem)
		binary.LittleEndian.PutUint32(dst[0:4], SpectralMagic)
		dst[4] = SpectralModeCompressed
		binary.LittleEndian.PutUint32(dst[5:9], uint32(n))
		pos := 9
		for i := 0; i < 4; i++ {
			for j := 0; j < 4; j++ {
				dst[pos] = U[i][j]
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

	dst := make([]byte, headerSize+alignedLen+rem)
	binary.LittleEndian.PutUint32(dst[0:4], SpectralMagic)
	dst[4] = SpectralModeU4
	binary.LittleEndian.PutUint32(dst[5:9], uint32(n))

	// Write 4x4 matrix U (16 bytes)
	pos := 9
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			dst[pos] = U[i][j]
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

func encodeSpectralRaw(src []byte) []byte {
	n := len(src)
	dst := make([]byte, 9+n)
	binary.LittleEndian.PutUint32(dst[0:4], SpectralMagic)
	dst[4] = SpectralModeRaw
	binary.LittleEndian.PutUint32(dst[5:9], uint32(n))
	copy(dst[9:], src)
	return dst
}

// DecompressSpectralUnitary decodes a unitary spectral block with zero allocations
// if dst capacity is sufficient.
func DecompressSpectralUnitary(src []byte, dst []byte) ([]byte, error) {
	if len(src) < 9 {
		return nil, ErrSpectralCorrupt
	}
	if binary.LittleEndian.Uint32(src[0:4]) != SpectralMagic {
		return nil, ErrSpectralCorrupt
	}

	mode := src[4]
	if mode == SpectralModeRaw {
		origLen := int(binary.LittleEndian.Uint32(src[5:9]))
		if len(src) < 9+origLen {
			return nil, ErrSpectralCorrupt
		}
		if cap(dst) >= origLen {
			dst = dst[:origLen]
		} else {
			dst = make([]byte, origLen)
		}
		copy(dst, src[9:9+origLen])
		return dst, nil
	}

	if mode != SpectralModeU4 && mode != SpectralModeCompressed {
		return nil, ErrSpectralCorrupt
	}

	if len(src) < 25 {
		return nil, ErrSpectralCorrupt
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

	// Read 4x4 matrix U
	var U UnitaryMatrix4
	pos := 9
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			U[i][j] = src[pos]
			pos++
		}
	}

	if mode == SpectralModeCompressed {
		if pos > len(src)-rem {
			return nil, ErrSpectralCorrupt
		}
		payloadLen := len(src) - pos - rem
		if payloadLen <= 0 {
			return nil, ErrSpectralCorrupt
		}
		decompressed, err := PWSDecode(src[pos : pos+payloadLen])
		if err != nil || len(decompressed) < alignedLen {
			return nil, ErrSpectralCorrupt
		}
		InvertUnitary4(decompressed[:alignedLen], U, dst[:alignedLen])
		if rem > 0 {
			copy(dst[alignedLen:origLen], src[pos+payloadLen:])
		}
		return dst, nil
	}

	if pos+alignedLen+rem > len(src) {
		return nil, ErrSpectralCorrupt
	}

	// Invert Unitary Transform: X = Y * U
	InvertUnitary4(src[pos:pos+alignedLen], U, dst[:alignedLen])
	pos += alignedLen

	if rem > 0 {
		copy(dst[alignedLen:origLen], src[pos:pos+rem])
	}

	return dst, nil
}
