package gpu

// Window size limits and matching bounds
const (
	Window64K     = 64 * 1024
	Window1M      = 1024 * 1024
	Window8M      = 8 * 1024 * 1024
	MinMatchLen   = 4
	MaxMatchLen32 = 4095
	MaxOffset32   = 1048575
	MaxMatchLen64 = 65535
)

// LZ77Match represents an unpacked dictionary match.
type LZ77Match struct {
	Position uint32
	Offset   uint32
	Length   uint16
	Literal  byte
}

// PackMatch32 packs length (12 bits) and offset (20 bits) into a 32-bit word.
func PackMatch32(length uint16, offset uint32) uint32 {
	if length < MinMatchLen || offset == 0 {
		return 0
	}
	l := uint32(length)
	if l > MaxMatchLen32 {
		l = MaxMatchLen32
	}
	return (l << 20) | (offset & MaxOffset32)
}

// UnpackMatch32 extracts length and offset from a 32-bit match word.
func UnpackMatch32(desc uint32) (length uint16, offset uint32) {
	return uint16(desc >> 20), desc & MaxOffset32
}

// IsMatch32 returns true if descriptor has valid match length.
func IsMatch32(desc uint32) bool {
	return (desc >> 20) >= MinMatchLen
}

// MatchDesc64 represents 64-bit packed descriptor for archival mode (up to 8MB/16MB).
type MatchDesc64 struct {
	Length   uint32
	Offset   uint32
	Literal  byte
	Flags    byte // 1: is_match
	Reserved uint16
}

// GPUProfile contains high-precision timing and PCIe DMA metrics.
type GPUProfile struct {
	IngressDMABytes           uint64
	IngressDMATimeMs          float64
	IngressDMABandwidthGBps   float64
	KernelTimeMs              float64
	InternalVRAMBandwidthGBps float64
	EgressDMABytes            uint64
	EgressDMATimeMs           float64
	EgressDMABandwidthGBps    float64
	TotalRoundTripMs          float64
	PCIeOverheadPercent       float64
	DeviceName                string
	SMCount                   int
	TotalVRAMBytes            uint64
}
