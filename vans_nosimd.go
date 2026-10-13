//go:build !(goexperiment.simd && amd64)

package przm

// BuildFrequencyMatrixSIMD is the portable fallback when goexperiment.simd is not enabled.
func BuildFrequencyMatrixSIMD(src []byte) Matrix16x16 {
	return BuildFrequencyMatrix(src)
}
