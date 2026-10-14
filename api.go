package przm

import (
	"encoding/binary"
)

// Compress compresses an uncompressed byte buffer using Trellis RDO at the given level.
func Compress(src []byte, level int) ([]byte, error) { return CompressWithDict(src, level, nil) }

// CompressWithDict compresses an input buffer with dynamic external dictionary injection.
func CompressWithDict(src []byte, level int, dict []byte) ([]byte, error) {
	if len(src) == 0 {
		return []byte{'P', 'R', 'Z', 'M', 0, 0, 0, 0}, nil
	}
	if len(src) > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	opts := DefaultOptions()
	opts.Level = level
	opts.RawDict = dict
	return compressInternal(src, opts)
}

// CompressWithOptions compresses src using functional options or an Options struct.
func CompressWithOptions(src []byte, opts ...any) ([]byte, error) {
	cfg := ApplyOptionOrStruct(opts...)
	if len(src) == 0 {
		return []byte{'P', 'R', 'Z', 'M', 0, 0, 0, 0}, nil
	}
	if len(src) > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	return compressInternal(src, cfg)
}

// Decompress decompresses a PRZM frame into dst with strictly zero heap allocations.
func Decompress(src, dst []byte) (int, error) { return DecompressWithDict(src, dst, nil, nil) }

// DecompressTo decompresses a PRZM frame into dst with strictly zero heap allocations,
// returning the subslice of dst containing the decompressed data.
func DecompressTo(src, dst []byte) ([]byte, error) {
	n, err := Decompress(src, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// DecompressDict decompresses a PRZM frame using an external DictionaryDesc.
func DecompressDict(src, dst []byte, dict *DictionaryDesc) (int, error) {
	return DecompressWithDict(src, dst, nil, dict)
}

// DecompressWithDict executes the PRZM decompression pipeline with external dictionary injection.
func DecompressWithDict(src, dst []byte, dict []byte, dictDesc *DictionaryDesc) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(src) < 8 {
		return 0, ErrCorruptedFrame
	}
	return decompressInternal(src, dst, dict, dictDesc)
}

// Decode decompresses a PRZM frame, allocating a new destination slice automatically.
func Decode(src []byte) ([]byte, error) {
	sz, err := GetUncompressedSize(src)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, sz, sz+64)
	n, err := Decompress(src, dst)
	if err != nil {
		return nil, err
	}
	return dst[:n], nil
}

// GetUncompressedSize inspects the container header and returns uncompressed payload size.
func GetUncompressedSize(src []byte) (int, error) {
	if len(src) < 8 {
		return 0, ErrCorruptedFrame
	}
	magic := uint32(src[0]) | (uint32(src[1]) << 8) | (uint32(src[2]) << 16) | (uint32(src[3]) << 24)
	if magic == MagicPRZM {
		u := int(uint32(src[5]) | (uint32(src[6]) << 8) | (uint32(src[7]) << 16))
		return u, nil
	}
	return getLegacyUncompressedSize(magic, src)
}

func getLegacyUncompressedSize(magic uint32, src []byte) (int, error) {
	switch magic {
	case 0x535A5250: return readUint32At(src, 5, 16) // PRZS
	case 0x504E4741, 0x49424F4D, 0x53494548: return readUint32At(src, 4, 11) // PNGA, MOBI, HEIS
	case 0x4752414D: return readUint32At(src, 4, 15) // MARG
	case 0x53594D50, 0x41524D36, 0x53484546, 0x4D41484C: return readUint32At(src, 8, 11) // SYMP, ARM6, SHEF, MAHL
	case 0x514F5349: return readUint32At(src, 8, 19) // ISOQ
	case 0x47435452: return readUint32At(src, 6, 9)  // GCTR
	case 0x44485343: return readUint32At(src, 4, 23) // CSHD
	default: return checkPrefixLegacyMagics(src)
	}
}

func readUint32At(src []byte, off, minLen int) (int, error) {
	if len(src) <= minLen { return 0, ErrCorruptedFrame }
	return int(binary.LittleEndian.Uint32(src[off : off+4])), nil
}

func checkPrefixLegacyMagics(src []byte) (int, error) {
	if len(src) > 7 && ((src[0] == 'P' && src[1] == 'W' && src[2] == 0x01) ||
		(src[0] == 'G' && src[1] == 'G' && src[2] == 0x01)) {
		return int(binary.LittleEndian.Uint32(src[4:8])), nil
	}
	return 0, ErrInvalidMagic
}
