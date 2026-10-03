package przm

import (
	"encoding/binary"
	"errors"
)

const MagicTTRN uint32 = 0x4E525454 // 'TTRN'

var (
	ErrTTRNCorrupted = errors.New("przm/ttrn: corrupted stream")
)

// ShuffleStride2 separates bytes at even and odd positions.
func ShuffleStride2(src []byte, dst []byte) {
	n := len(src)
	half := (n + 1) / 2
	for i := range n {
		if i%2 == 0 {
			dst[i/2] = src[i]
		} else {
			dst[half+i/2] = src[i]
		}
	}
}

// UnshuffleStride2 reverses ShuffleStride2.
func UnshuffleStride2(src []byte, dst []byte) {
	n := len(src)
	half := (n + 1) / 2
	for i := range n {
		if i%2 == 0 {
			dst[i] = src[i/2]
		} else {
			dst[i] = src[half+i/2]
		}
	}
}

// CompressTTRN compresses a tensor using the Hybrid Tensor Train mechanism.
func CompressTTRN(src []byte, dict []byte) ([]byte, error) {
	n := len(src)

	out := make([]byte, 8)
	binary.LittleEndian.PutUint32(out[0:4], MagicTTRN)
	binary.LittleEndian.PutUint32(out[4:8], uint32(n))

	if n == 0 {
		return out, nil
	}

	half := n / 2
	streamA := src[:half]
	streamB := src[half:]

	// Stream A (TT-Cores) -> SYMP
	compA := CompressSymplectic(streamA)
	if compA == nil {
		compA = streamA
	}

	var szBuf [4]byte
	binary.LittleEndian.PutUint32(szBuf[:], uint32(len(compA)))
	out = append(out, szBuf[:]...)
	out = append(out, compA...)

	// Stream B (Residuals) -> Shuffle(stride=2) -> GlobalLRGGF
	shuffledB := make([]byte, len(streamB))
	ShuffleStride2(streamB, shuffledB)

	compB, err := GlobalLRGGFEncode(shuffledB, dict)
	if err != nil {
		return nil, err
	}

	binary.LittleEndian.PutUint32(szBuf[:], uint32(len(compB)))
	out = append(out, szBuf[:]...)
	out = append(out, compB...)

	return out, nil
}

// DecompressTTRNDirect decompresses TTRN into dst with 0 allocations.
// dst must have capacity for the full uncompressed tensor.
func DecompressTTRNDirect(src, dict, dst []byte) error {
	if len(src) < 8 {
		return ErrTTRNCorrupted
	}
	magic := binary.LittleEndian.Uint32(src[0:4])
	if magic != MagicTTRN {
		return errors.New("przm/ttrn: invalid magic signature")
	}

	origSize := int(binary.LittleEndian.Uint32(src[4:8]))
	if len(dst) < origSize {
		return errors.New("przm/ttrn: dst buffer too small")
	}

	if origSize == 0 {
		return nil
	}

	ptr := 8
	half := origSize / 2

	if ptr+4 > len(src) {
		return ErrTTRNCorrupted
	}
	lenA := int(binary.LittleEndian.Uint32(src[ptr : ptr+4]))
	ptr += 4
	if ptr+lenA > len(src) {
		return ErrTTRNCorrupted
	}

	_, err := DecompressSymplectic(src[ptr:ptr+lenA], dst[:half])
	if err != nil {
		return err
	}
	ptr += lenA

	if ptr+4 > len(src) {
		return ErrTTRNCorrupted
	}
	lenB := int(binary.LittleEndian.Uint32(src[ptr : ptr+4]))
	ptr += 4
	if ptr+lenB > len(src) {
		return ErrTTRNCorrupted
	}

	// 0 allocs L3-cache-bounded block stream B extraction
	streamB := src[ptr : ptr+lenB]
	if len(streamB) < 8 || streamB[0] != 'G' || streamB[1] != 'L' || streamB[2] != 0x01 {
		return ErrTTRNCorrupted
	}
	origSizeB := int(binary.LittleEndian.Uint32(streamB[4:8]))
	if origSizeB != origSize-half {
		return errors.New("przm/ttrn: decompressed stream B size mismatch")
	}
	if len(dict) != 32768 {
		return errors.New("przm/ttrn: dictionary must be 32KiB")
	}

	ptrB := 8
	dstB := dst[half:origSize]
	N := origSizeB
	halfN := (N + 1) / 2
	U := dict[:16384]
	V := dict[16384:]

	dstOff := 0
	for dstOff < N {
		if ptrB >= len(streamB) {
			return ErrTTRNCorrupted
		}
		flag := streamB[ptrB]
		ptrB++

		switch flag {
		case 0x00:
			if ptrB >= len(streamB) {
				return ErrTTRNCorrupted
			}
			blockLen := int(streamB[ptrB])
			ptrB++
			if ptrB+blockLen > len(streamB) || dstOff+blockLen > N {
				return ErrTTRNCorrupted
			}
			for k := range blockLen {
				val := streamB[ptrB+k]
				idx := dstOff + k
				if idx < halfN {
					dstB[idx*2] = val
				} else {
					dstB[(idx-halfN)*2+1] = val
				}
			}
			ptrB += blockLen
			dstOff += blockLen
		case 0x01:
			if ptrB+3 > len(streamB) {
				return ErrTTRNCorrupted
			}
			uIdx := int(streamB[ptrB])
			vIdx := int(streamB[ptrB+1])
			errCount := int(streamB[ptrB+2])
			ptrB += 3

			if ptrB+2*errCount > len(streamB) {
				return ErrTTRNCorrupted
			}
			blockLen := 64
			if dstOff+blockLen > N {
				blockLen = N - dstOff
			}

			uVec := U[uIdx*64 : uIdx*64+64]
			vVec := V[vIdx*64 : vIdx*64+64]

			var block [64]byte
			for k := 0; k < blockLen; k++ {
				block[k] = uVec[k] ^ vVec[k]
			}

			for range errCount {
				idx := int(streamB[ptrB])
				val := streamB[ptrB+1]
				ptrB += 2
				if idx < blockLen {
					block[idx] ^= val
				}
			}

			for k := 0; k < blockLen; k++ {
				idx := dstOff + k
				if idx < halfN {
					dstB[idx*2] = block[k]
				} else {
					dstB[(idx-halfN)*2+1] = block[k]
				}
			}
			dstOff += blockLen
		default:
			return ErrTTRNCorrupted
		}
	}

	return nil
}
