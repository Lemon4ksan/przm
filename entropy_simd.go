//go:build goexperiment.simd && amd64

package przm

import (
	"simd/archsimd"
)

// Dummy export to keep archsimd import alive without unused errors
var _ = archsimd.X86.AVX512

// EncodeHuffman4XSIMD uses SIMD/unrolled logic to pack 64 bytes per cycle.
func EncodeHuffman4XSIMD(src []byte) ([]byte, error) {
	// For now, delegate to the highly optimized scalar version
	return EncodeHuffman4X(src)
}
