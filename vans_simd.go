//go:build goexperiment.simd && amd64

package przm

import (
	"math/bits"
	"simd/archsimd"
)

var splats [256]archsimd.Uint8x64

func init() {
	if archsimd.X86.AVX512() {
		for j := 0; j < 256; j++ {
			splats[j] = archsimd.BroadcastUint8x64(uint8(j))
		}
	}
}

// BuildFrequencyMatrixSIMD uses AVX-512 to build the frequency matrix processing 256 bytes per cycle
func BuildFrequencyMatrixSIMD(src []byte) Matrix16x16 {
	var m Matrix16x16
	m.Total = uint32(len(src))
	if len(src) == 0 {
		return m
	}

	if !archsimd.X86.AVX512() {
		// fallback to unrolled loop
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
		return m
	}

	n := len(src)
	i := 0

	for ; i+255 <= n-1; i += 256 {
		v0 := archsimd.LoadUint8x64(src[i : i+64])
		v1 := archsimd.LoadUint8x64(src[i+64 : i+128])
		v2 := archsimd.LoadUint8x64(src[i+128 : i+192])
		v3 := archsimd.LoadUint8x64(src[i+192 : i+256])

		for j := 0; j < 256; j++ {
			splat := splats[j]
			m0 := v0.Equal(splat).ToBits()
			m1 := v1.Equal(splat).ToBits()
			m2 := v2.Equal(splat).ToBits()
			m3 := v3.Equal(splat).ToBits()

			pop := bits.OnesCount64(m0) + bits.OnesCount64(m1) + bits.OnesCount64(m2) + bits.OnesCount64(m3)
			if pop > 0 {
				m.Counts[j] += uint32(pop)
			}
		}
	}

	// Tail
	for ; i < n; i++ {
		m.Counts[src[i]]++
	}

	return m
}
