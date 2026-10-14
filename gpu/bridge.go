// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpu

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

//go:embed kernel/przm_lz77.ptx
var embeddedPTX []byte

func cuCheck(call string, r uintptr) error {
	if r != 0 {
		return fmt.Errorf("%s failed with CUDA error %d", call, r)
	}
	return nil
}

const (
	cuAttrMultiprocessorCount    = 16
	cuAttrComputeCapabilityMajor = 75
	cuAttrComputeCapabilityMinor = 76
)

var (
	modCUDA              = syscall.NewLazyDLL("nvcuda.dll")
	cuInit               = modCUDA.NewProc("cuInit")
	cuDeviceGet          = modCUDA.NewProc("cuDeviceGet")
	cuDeviceGetCount     = modCUDA.NewProc("cuDeviceGetCount")
	cuDeviceGetName      = modCUDA.NewProc("cuDeviceGetName")
	cuDeviceTotalMem     = modCUDA.NewProc("cuDeviceTotalMem_v2")
	cuDeviceGetAttribute = modCUDA.NewProc("cuDeviceGetAttribute")
	cuCtxCreate          = modCUDA.NewProc("cuCtxCreate_v2")
	cuCtxSetCurrent      = modCUDA.NewProc("cuCtxSetCurrent")
	cuMemAlloc           = modCUDA.NewProc("cuMemAlloc_v2")
	cuMemFree            = modCUDA.NewProc("cuMemFree_v2")
	cuMemcpyHtoDAsync    = modCUDA.NewProc("cuMemcpyHtoDAsync_v2")
	cuMemcpyDtoHAsync    = modCUDA.NewProc("cuMemcpyDtoHAsync_v2")
	cuStreamCreate       = modCUDA.NewProc("cuStreamCreate")
	cuStreamDestroy      = modCUDA.NewProc("cuStreamDestroy_v2")
	cuStreamSynchronize  = modCUDA.NewProc("cuStreamSynchronize")
	cuModuleGetFunc      = modCUDA.NewProc("cuModuleGetFunction")
	cuLaunchKernel       = modCUDA.NewProc("cuLaunchKernel")
	cuEventCreate        = modCUDA.NewProc("cuEventCreate")
	cuEventRecord        = modCUDA.NewProc("cuEventRecord")
	cuEventSynchronize   = modCUDA.NewProc("cuEventSynchronize")
	cuEventElapsedTime   = modCUDA.NewProc("cuEventElapsedTime")
	cuEventDestroy       = modCUDA.NewProc("cuEventDestroy_v2")
	cuPrimaryCtxRelease  = modCUDA.NewProc("cuDevicePrimaryCtxRelease")
	cuModuleLoadDataEx   = modCUDA.NewProc("cuModuleLoadDataEx")
	cuModuleUnload       = modCUDA.NewProc("cuModuleUnload")
	cuMemHostRegister    = modCUDA.NewProc("cuMemHostRegister")
	cuMemHostUnregister  = modCUDA.NewProc("cuMemHostUnregister")
	cuStreamWaitEvent    = modCUDA.NewProc("cuStreamWaitEvent")
	cuMemsetD8Async      = modCUDA.NewProc("cuMemsetD8Async")
)

var (
	globalMu         sync.Mutex
	globalCompressor atomic.Pointer[GPUCompressor]
	mockUnavailable  atomic.Bool
	mockMatchErr     atomic.Pointer[error]
)

// SetMockUnavailableForTest forces IsAvailable to return false for testing fallback.
func SetMockUnavailableForTest(unavailable bool) {
	mockUnavailable.Store(unavailable)
}

// SetMockMatchErrorForTest forces MatchLZ77 to return error for testing fallback.
func SetMockMatchErrorForTest(err error) {
	if err == nil {
		mockMatchErr.Store(nil)
	} else {
		mockMatchErr.Store(&err)
	}
}

// IsAvailable returns true if the CUDA driver library (nvcuda.dll) is available,
// can be initialized via cuInit, detects at least one CUDA device, and that
// device supports compute capability major >= 8 (Ampere sm_80+).
func IsAvailable() bool {
	if mockUnavailable.Load() {
		return false
	}
	if err := modCUDA.Load(); err != nil {
		return false
	}
	if r, _, _ := cuInit.Call(0); r != 0 {
		return false
	}
	var count int32
	if r, _, _ := cuDeviceGetCount.Call(uintptr(unsafe.Pointer(&count))); r != 0 || count <= 0 {
		return false
	}
	for i := int32(0); i < count; i++ {
		var dev int32
		if r, _, _ := cuDeviceGet.Call(uintptr(unsafe.Pointer(&dev)), uintptr(i)); r != 0 {
			continue
		}
		var major int32
		if r, _, _ := cuDeviceGetAttribute.Call(
			uintptr(unsafe.Pointer(&major)),
			uintptr(cuAttrComputeCapabilityMajor),
			uintptr(dev),
		); r == 0 &&
			major >= 8 {
			return true
		}
	}
	return false
}

// GetGlobalCompressor returns a thread-safe singleton GPUCompressor instance.
// It retries initialization if a previous attempt encountered transient
// unavailability or testing mocks, preventing permanent singleton caching lock-in.
func GetGlobalCompressor() (*GPUCompressor, error) {
	if c := globalCompressor.Load(); c != nil {
		return c, nil
	}
	globalMu.Lock()
	defer globalMu.Unlock()
	if c := globalCompressor.Load(); c != nil {
		return c, nil
	}
	if !IsAvailable() {
		return nil, errors.New("cuda gpu acceleration not available or compute capability < 8.0")
	}
	comp, err := NewGPUCompressor(0)
	if err != nil {
		return nil, err
	}
	globalCompressor.Store(comp)
	return comp, nil
}

// GetGPUCompressor is an alias for GetGlobalCompressor matching PRZM architecture diagrams.
func GetGPUCompressor() (*GPUCompressor, error) {
	return GetGlobalCompressor()
}

// ResetGlobalCompressorForTest closes and resets the global singleton for testing.
func ResetGlobalCompressorForTest() {
	globalMu.Lock()
	defer globalMu.Unlock()
	if c := globalCompressor.Swap(nil); c != nil {
		_ = c.Close()
	}
}

func cstring(b []byte) string {
	for i, v := range b {
		if v == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func roundUpPow2(v int) int {
	if v <= 65536 {
		return 65536
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}

const (
	TokenizerTileSize = 32768
	MaxTileCmdBytes   = 36864
	MaxTileOffBytes   = 36864
	MaxTileLitBytes   = 36864
)

// pipelineSlot holds per-stream GPU device buffers, events, and pinned memory
// for zero-contention double-buffered pipelined streaming across multi-megabyte payloads.
type pipelineSlot struct {
	stream           uintptr
	dIn              uint64
	dMatch           uint64
	dRadixTable      uint64
	dBlockCounts     uint64
	dBlockOffsets    uint64
	dTotalCount      uint64
	dCompactPos      uint64
	dCompactDesc     uint64
	dAllocCap        int
	hTotalCountSlice []byte
	hTotalCountPtr   uintptr
	hInBuf           []byte
	hInPtr           uintptr
	hInCap           int
	hPosBuf          []byte
	hPosPtr          uintptr
	hDescBuf         []byte
	hDescPtr         uintptr
	hEgressCap       int
	dHist            uint64
	dMeta            uint64
	dFreq            uint64
	dCDF             uint64
	dMat             uint64
	dOutLen          uint64
	dTempBitstream   uint64
	hHistSlice       []byte
	hHistPtr         uintptr
	hOutLenSlice     []byte
	hOutLenPtr       uintptr
	evStart          uintptr
	evIngress        uintptr
	evKernel         uintptr
	evEgressStart    uintptr
	evEgress         uintptr

	// Tokenizer buffers and metadata
	dChunkCmds      uint64
	dChunkOffs      uint64
	dChunkLits      uint64
	dChunkCmdLen    uint64
	dChunkOffLen    uint64
	dChunkLitLen    uint64
	dChunkCmdOffset uint64
	dChunkOffOffset uint64
	dChunkLitOffset uint64
	dTotals         uint64
	dFinalCmds      uint64
	dFinalOffs      uint64
	dFinalLits      uint64
	dTokAllocCap    int
	dTokChunksAlloc int

	hTotalsSlice   []byte
	hTotalsPtr     uintptr
	hFinalCmdSlice []byte
	hFinalCmdPtr   uintptr
	hFinalOffSlice []byte
	hFinalOffPtr   uintptr
	hFinalLitSlice []byte
	hFinalLitPtr   uintptr
	hTokEgressCap  int

	evTokStart  uintptr
	evTokPass1  uintptr
	evTokPass2  uintptr
	evTokKernel uintptr
	evTokEgress uintptr

	// D2D Frame Buffer and Events
	dFrameOut      uint64
	hFrameSlice    []byte
	hFramePtr      uintptr
	dFrameAllocCap int
	evD2DStart     uintptr
	evD2DKernel    uintptr
	evD2DEgress    uintptr
}

// GPUCompressor coordinates CUDA Driver execution on NVIDIA hardware.
type GPUCompressor struct {
	mu                 sync.Mutex
	dev                int32
	ctx                uintptr
	module             uintptr
	fnInitHead         uintptr
	fnBuildChains      uintptr
	fnSearchMatches    uintptr
	fnRadixStrike      uintptr
	fnClearRadixTable  uintptr
	fnCompactCount     uintptr
	fnCompactScan      uintptr
	fnCompactScatter   uintptr
	fnVRAMBandwidth    uintptr
	fnFusedTokenizer   uintptr
	fnTokenizerScan    uintptr
	fnTokenizerGather  uintptr
	fnWriteFrameHeader uintptr
	fnDeviceCopy       uintptr
	fnClearHistogram   uintptr
	fnByteHistogram    uintptr
	fnPackStreamFlat   uintptr
	fnVANSEncode4x     uintptr
	fnMultiStreamANS   uintptr
	fnEntropyBandwidth uintptr
	slots              [2]pipelineSlot
	dRadixTable        uint64
	dIn                uint64
	dMatch             uint64
	dBlockCounts       uint64
	dBlockOffsets      uint64
	dTotalCount        uint64
	dCompactPos        uint64
	dCompactDesc       uint64
	dAllocCap          int
	hTotalCountSlice   []byte
	hTotalCountPtr     uintptr
	streams            []uintptr
	evStart            uintptr
	evIngress          uintptr
	evKernel           uintptr
	evEgressStart      uintptr
	evEgress           uintptr
	pools              map[int]*PinnedBufferPool
	ComputeMajor       int
	ComputeMinor       int
	DeviceName         string
	SMCount            int
	TotalVRAMBytes     uint64
}

// NewGPUCompressor initializes device, context, and compiles the LZ77 PTX module.
func NewGPUCompressor(deviceID int) (*GPUCompressor, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ptxData, err := loadPTX()
	if err != nil {
		return nil, err
	}

	if r, _, _ := cuInit.Call(0); r != 0 {
		return nil, fmt.Errorf("cuInit failed: %d", r)
	}
	var dev int32
	if r, _, _ := cuDeviceGet.Call(uintptr(unsafe.Pointer(&dev)), uintptr(deviceID)); r != 0 {
		return nil, fmt.Errorf("cuDeviceGet failed: %d", r)
	}

	nameBuf := make([]byte, 256)
	cuDeviceGetName.Call(uintptr(unsafe.Pointer(&nameBuf[0])), 256, uintptr(dev))
	devName := cstring(nameBuf)

	var totalMem uint64
	cuDeviceTotalMem.Call(uintptr(unsafe.Pointer(&totalMem)), uintptr(dev))
	var smCount int32
	cuDeviceGetAttribute.Call(uintptr(unsafe.Pointer(&smCount)), uintptr(cuAttrMultiprocessorCount), uintptr(dev))

	var major, minor int32
	cuDeviceGetAttribute.Call(uintptr(unsafe.Pointer(&major)), uintptr(cuAttrComputeCapabilityMajor), uintptr(dev))
	cuDeviceGetAttribute.Call(uintptr(unsafe.Pointer(&minor)), uintptr(cuAttrComputeCapabilityMinor), uintptr(dev))
	if major < 8 {
		return nil, fmt.Errorf(
			"device %d compute capability %d.%d unsupported: Ampere sm_80+ (major >= 8) required",
			deviceID,
			major,
			minor,
		)
	}

	var ctx uintptr
	if r, _, _ := cuPrimaryCtxRetain.Call(uintptr(unsafe.Pointer(&ctx)), uintptr(dev)); r != 0 || ctx == 0 {
		if r2, _, _ := cuCtxCreate.Call(uintptr(unsafe.Pointer(&ctx)), 0, uintptr(dev)); r2 != 0 {
			return nil, fmt.Errorf("cuCtxCreate failed: %d", r2)
		}
	}
	if r, _, _ := cuCtxSetCurrent.Call(ctx); r != 0 {
		cuPrimaryCtxRelease.Call(uintptr(dev))
		return nil, fmt.Errorf("cuCtxSetCurrent failed: %d", r)
	}

	var module uintptr
	logBuf := make([]byte, 8192)
	errBuf := make([]byte, 8192)
	options := []uint32{3, 4, 5, 6, 12}
	optionValues := []uintptr{
		uintptr(unsafe.Pointer(&logBuf[0])), 8192,
		uintptr(unsafe.Pointer(&errBuf[0])), 8192,
		1,
	}
	r, _, _ := cuModuleLoadDataEx.Call(
		uintptr(unsafe.Pointer(&module)),
		uintptr(unsafe.Pointer(&ptxData[0])),
		5,
		uintptr(unsafe.Pointer(&options[0])),
		uintptr(unsafe.Pointer(&optionValues[0])),
	)
	if r != 0 {
		cuCtxSetCurrent.Call(0)
		cuPrimaryCtxRelease.Call(uintptr(dev))
		return nil, fmt.Errorf("cuModuleLoadData failed: %d, err: %s", r, cstring(errBuf))
	}

	g := &GPUCompressor{
		dev: dev, ctx: ctx, module: module,
		DeviceName: devName, SMCount: int(smCount), TotalVRAMBytes: totalMem,
		ComputeMajor: int(major), ComputeMinor: int(minor),
		pools: make(map[int]*PinnedBufferPool),
	}

	g.fnInitHead = g.getFunc("przm_lz77_init_head_kernel")
	g.fnBuildChains = g.getFunc("przm_lz77_build_chains_kernel")
	g.fnSearchMatches = g.getFunc("przm_lz77_search_matches_kernel")
	g.fnRadixStrike = g.getFunc("przm_lz77_radix_strike_kernel")
	g.fnClearRadixTable = g.getFunc("przm_lz77_clear_radix_table_kernel")
	g.fnCompactCount = g.getFunc("przm_lz77_compact_count_kernel")
	g.fnCompactScan = g.getFunc("przm_lz77_compact_scan_kernel")
	g.fnCompactScatter = g.getFunc("przm_lz77_compact_scatter_kernel")
	g.fnVRAMBandwidth = g.getFunc("przm_lz77_vram_bandwidth_kernel")
	g.fnFusedTokenizer = g.getFunc("przm_fused_tokenizer_kernel")
	g.fnTokenizerScan = g.getFunc("przm_tokenizer_scan_kernel")
	g.fnTokenizerGather = g.getFunc("przm_tokenizer_gather_kernel")
	g.fnWriteFrameHeader = g.getFunc("przm_write_frame_header_kernel")
	g.fnDeviceCopy = g.getFunc("przm_device_copy_kernel")
	g.fnClearHistogram = g.getFunc("przm_clear_histogram_kernel")
	g.fnByteHistogram = g.getFunc("przm_byte_histogram_kernel")
	g.fnPackStreamFlat = g.getFunc("przm_pack_stream_flat_kernel")
	g.fnVANSEncode4x = g.getFunc("przm_vans_encode4x_kernel")
	g.fnMultiStreamANS = g.getFunc("przm_multi_stream_ans_kernel")
	g.fnEntropyBandwidth = g.getFunc("przm_entropy_bandwidth_kernel")

	// Preallocate dual-buffer pipeline slots (8 MB Radix Table per slot, compaction metadata, events)
	radixBytes := uintptr(524288 * 4)
	for s := range 2 {
		slot := &g.slots[s]
		cuStreamCreate.Call(uintptr(unsafe.Pointer(&slot.stream)), 1)
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dRadixTable)), radixBytes)
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dBlockCounts)), uintptr(224*4))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dBlockOffsets)), uintptr(224*4))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dTotalCount)), uintptr(4))
		slot.hTotalCountSlice, slot.hTotalCountPtr, _ = AllocPinnedHost(4)

		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dHist)), uintptr(256*4))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dMeta)), uintptr(4*4))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dFreq)), uintptr(256*2))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dCDF)), uintptr(257*2))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dMat)), uintptr(1024))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dOutLen)), uintptr(4))
		slot.hHistSlice, slot.hHistPtr, _ = AllocPinnedHost(256 * 4)
		slot.hOutLenSlice, slot.hOutLenPtr, _ = AllocPinnedHost(4)

		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evStart)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evIngress)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evKernel)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evEgressStart)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evEgress)), 0)

		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evTokStart)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evTokPass1)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evTokPass2)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evTokKernel)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evTokEgress)), 0)

		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evD2DStart)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evD2DKernel)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evD2DEgress)), 0)
	}

	// Mirror slot 0 to legacy fields for 100% backward compatibility
	g.dRadixTable = g.slots[0].dRadixTable
	g.dBlockCounts = g.slots[0].dBlockCounts
	g.dBlockOffsets = g.slots[0].dBlockOffsets
	g.dTotalCount = g.slots[0].dTotalCount
	g.hTotalCountSlice = g.slots[0].hTotalCountSlice
	g.hTotalCountPtr = g.slots[0].hTotalCountPtr
	g.evStart = g.slots[0].evStart
	g.evIngress = g.slots[0].evIngress
	g.evKernel = g.slots[0].evKernel
	g.evEgressStart = g.slots[0].evEgressStart
	g.evEgress = g.slots[0].evEgress

	// Pre-create reusable CUDA streams (stream 0 and 1 are slot 0 and 1)
	g.streams = make([]uintptr, 4)
	g.streams[0] = g.slots[0].stream
	g.streams[1] = g.slots[1].stream
	for i := 2; i < 4; i++ {
		cuStreamCreate.Call(uintptr(unsafe.Pointer(&g.streams[i])), 1)
	}

	return g, nil
}

// GetPinned leases a page-locked pinned host buffer from the power-of-2 pooled slab allocator.
func (g *GPUCompressor) GetPinned(size int) (PinnedBuffer, *PinnedBufferPool, error) {
	if g.pools == nil {
		g.pools = make(map[int]*PinnedBufferPool)
	}
	allocSize := roundUpPow2(size)
	pool, ok := g.pools[allocSize]
	if !ok {
		var err error
		pool, err = NewPinnedBufferPool(allocSize, 0)
		if err != nil {
			return PinnedBuffer{}, nil, err
		}
		g.pools[allocSize] = pool
	}
	buf, err := pool.Get()
	if err == nil {
		buf.Bytes = buf.Bytes[:size]
	}
	return buf, pool, err
}

func (g *GPUCompressor) getFunc(name string) uintptr {
	nBytes := append([]byte(name), 0)
	var hfunc uintptr
	cuModuleGetFunc.Call(uintptr(unsafe.Pointer(&hfunc)), g.module, uintptr(unsafe.Pointer(&nBytes[0])))
	return hfunc
}

func sanitizePTXVersion(data []byte) []byte {
	if bytes.Contains(data, []byte(".version 9.4")) {
		data = bytes.ReplaceAll(data, []byte(".version 9.4"), []byte(".version 9.3"))
	}
	return data
}

func loadPTX() ([]byte, error) {
	candidates := []string{
		"gpu/kernel/przm_lz77.ptx",
		"kernel/przm_lz77.ptx",
		"../gpu/kernel/przm_lz77.ptx",
		"../../gpu/kernel/przm_lz77.ptx",
		"../kernel/przm_lz77.ptx",
		"przm_lz77.ptx",
		`d:\CodingProjects\przm\gpu\kernel\przm_lz77.ptx`,
	}
	if envPath := os.Getenv("PRZM_PTX_PATH"); envPath != "" {
		candidates = append([]string{envPath}, candidates...)
	}
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			data = sanitizePTXVersion(data)
			return append(data, 0), nil
		}
	}
	if len(embeddedPTX) > 0 {
		data := sanitizePTXVersion(embeddedPTX)
		return append(data, 0), nil
	}
	return nil, errors.New("przm_lz77.ptx not found")
}

// Close destroys CUDA context and frees GPU resources.
func (g *GPUCompressor) Close() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.pools != nil {
		for _, pool := range g.pools {
			_ = pool.Close()
		}
		g.pools = nil
	}

	if g.ctx != 0 {
		cuCtxSetCurrent.Call(g.ctx)
	}

	for s := range 2 {
		slot := &g.slots[s]
		if slot.dRadixTable != 0 {
			cuMemFree.Call(uintptr(slot.dRadixTable))
			slot.dRadixTable = 0
		}
		if slot.dIn != 0 {
			cuMemFree.Call(uintptr(slot.dIn))
			slot.dIn = 0
		}
		if slot.dMatch != 0 {
			cuMemFree.Call(uintptr(slot.dMatch))
			slot.dMatch = 0
		}
		if slot.dCompactPos != 0 {
			cuMemFree.Call(uintptr(slot.dCompactPos))
			slot.dCompactPos = 0
		}
		if slot.dCompactDesc != 0 {
			cuMemFree.Call(uintptr(slot.dCompactDesc))
			slot.dCompactDesc = 0
		}
		if slot.dBlockCounts != 0 {
			cuMemFree.Call(uintptr(slot.dBlockCounts))
			slot.dBlockCounts = 0
		}
		if slot.dBlockOffsets != 0 {
			cuMemFree.Call(uintptr(slot.dBlockOffsets))
			slot.dBlockOffsets = 0
		}
		if slot.dTotalCount != 0 {
			cuMemFree.Call(uintptr(slot.dTotalCount))
			slot.dTotalCount = 0
		}
		if slot.hTotalCountPtr != 0 {
			_ = FreePinnedHost(slot.hTotalCountPtr)
			slot.hTotalCountPtr = 0
			slot.hTotalCountSlice = nil
		}
		if slot.hInPtr != 0 {
			_ = FreePinnedHost(slot.hInPtr)
			slot.hInPtr = 0
			slot.hInBuf = nil
			slot.hInCap = 0
		}
		if slot.hPosPtr != 0 {
			_ = FreePinnedHost(slot.hPosPtr)
			slot.hPosPtr = 0
			slot.hPosBuf = nil
		}
		if slot.hDescPtr != 0 {
			_ = FreePinnedHost(slot.hDescPtr)
			slot.hDescPtr = 0
			slot.hDescBuf = nil
		}
		if slot.dHist != 0 {
			cuMemFree.Call(uintptr(slot.dHist))
			slot.dHist = 0
		}
		if slot.dMeta != 0 {
			cuMemFree.Call(uintptr(slot.dMeta))
			slot.dMeta = 0
		}
		if slot.dFreq != 0 {
			cuMemFree.Call(uintptr(slot.dFreq))
			slot.dFreq = 0
		}
		if slot.dCDF != 0 {
			cuMemFree.Call(uintptr(slot.dCDF))
			slot.dCDF = 0
		}
		if slot.dMat != 0 {
			cuMemFree.Call(uintptr(slot.dMat))
			slot.dMat = 0
		}
		if slot.dOutLen != 0 {
			cuMemFree.Call(uintptr(slot.dOutLen))
			slot.dOutLen = 0
		}
		if slot.dTempBitstream != 0 {
			cuMemFree.Call(uintptr(slot.dTempBitstream))
			slot.dTempBitstream = 0
		}
		if slot.hHistPtr != 0 {
			_ = FreePinnedHost(slot.hHistPtr)
			slot.hHistPtr = 0
			slot.hHistSlice = nil
		}
		if slot.hOutLenPtr != 0 {
			_ = FreePinnedHost(slot.hOutLenPtr)
			slot.hOutLenPtr = 0
			slot.hOutLenSlice = nil
		}

		// Clean tokenizer device buffers
		if slot.dChunkCmds != 0 {
			cuMemFree.Call(uintptr(slot.dChunkCmds))
			slot.dChunkCmds = 0
		}
		if slot.dChunkOffs != 0 {
			cuMemFree.Call(uintptr(slot.dChunkOffs))
			slot.dChunkOffs = 0
		}
		if slot.dChunkLits != 0 {
			cuMemFree.Call(uintptr(slot.dChunkLits))
			slot.dChunkLits = 0
		}
		if slot.dChunkCmdLen != 0 {
			cuMemFree.Call(uintptr(slot.dChunkCmdLen))
			slot.dChunkCmdLen = 0
		}
		if slot.dChunkOffLen != 0 {
			cuMemFree.Call(uintptr(slot.dChunkOffLen))
			slot.dChunkOffLen = 0
		}
		if slot.dChunkLitLen != 0 {
			cuMemFree.Call(uintptr(slot.dChunkLitLen))
			slot.dChunkLitLen = 0
		}
		if slot.dChunkCmdOffset != 0 {
			cuMemFree.Call(uintptr(slot.dChunkCmdOffset))
			slot.dChunkCmdOffset = 0
		}
		if slot.dChunkOffOffset != 0 {
			cuMemFree.Call(uintptr(slot.dChunkOffOffset))
			slot.dChunkOffOffset = 0
		}
		if slot.dChunkLitOffset != 0 {
			cuMemFree.Call(uintptr(slot.dChunkLitOffset))
			slot.dChunkLitOffset = 0
		}
		if slot.dTotals != 0 {
			cuMemFree.Call(uintptr(slot.dTotals))
			slot.dTotals = 0
		}
		if slot.dFinalCmds != 0 {
			cuMemFree.Call(uintptr(slot.dFinalCmds))
			slot.dFinalCmds = 0
		}
		if slot.dFinalOffs != 0 {
			cuMemFree.Call(uintptr(slot.dFinalOffs))
			slot.dFinalOffs = 0
		}
		if slot.dFinalLits != 0 {
			cuMemFree.Call(uintptr(slot.dFinalLits))
			slot.dFinalLits = 0
		}
		if slot.dFrameOut != 0 {
			cuMemFree.Call(uintptr(slot.dFrameOut))
			slot.dFrameOut = 0
		}

		// Clean tokenizer pinned host buffers
		if slot.hTotalsPtr != 0 {
			_ = FreePinnedHost(slot.hTotalsPtr)
			slot.hTotalsPtr = 0
			slot.hTotalsSlice = nil
		}
		if slot.hFinalCmdPtr != 0 {
			_ = FreePinnedHost(slot.hFinalCmdPtr)
			slot.hFinalCmdPtr = 0
			slot.hFinalCmdSlice = nil
		}
		if slot.hFinalOffPtr != 0 {
			_ = FreePinnedHost(slot.hFinalOffPtr)
			slot.hFinalOffPtr = 0
			slot.hFinalOffSlice = nil
		}
		if slot.hFinalLitPtr != 0 {
			_ = FreePinnedHost(slot.hFinalLitPtr)
			slot.hFinalLitPtr = 0
			slot.hFinalLitSlice = nil
		}
		if slot.hFramePtr != 0 {
			_ = FreePinnedHost(slot.hFramePtr)
			slot.hFramePtr = 0
			slot.hFrameSlice = nil
		}

		slot.dTokAllocCap = 0
		slot.dTokChunksAlloc = 0
		slot.hTokEgressCap = 0
		slot.dFrameAllocCap = 0
		slot.hEgressCap = 0
		slot.dAllocCap = 0

		if slot.evStart != 0 {
			cuEventDestroy.Call(slot.evStart)
			slot.evStart = 0
		}
		if slot.evIngress != 0 {
			cuEventDestroy.Call(slot.evIngress)
			slot.evIngress = 0
		}
		if slot.evKernel != 0 {
			cuEventDestroy.Call(slot.evKernel)
			slot.evKernel = 0
		}
		if slot.evEgressStart != 0 {
			cuEventDestroy.Call(slot.evEgressStart)
			slot.evEgressStart = 0
		}
		if slot.evEgress != 0 {
			cuEventDestroy.Call(slot.evEgress)
			slot.evEgress = 0
		}
		if slot.evTokStart != 0 {
			cuEventDestroy.Call(slot.evTokStart)
			slot.evTokStart = 0
		}
		if slot.evTokPass1 != 0 {
			cuEventDestroy.Call(slot.evTokPass1)
			slot.evTokPass1 = 0
		}
		if slot.evTokPass2 != 0 {
			cuEventDestroy.Call(slot.evTokPass2)
			slot.evTokPass2 = 0
		}
		if slot.evTokKernel != 0 {
			cuEventDestroy.Call(slot.evTokKernel)
			slot.evTokKernel = 0
		}
		if slot.evTokEgress != 0 {
			cuEventDestroy.Call(slot.evTokEgress)
			slot.evTokEgress = 0
		}
		if slot.evD2DStart != 0 {
			cuEventDestroy.Call(slot.evD2DStart)
			slot.evD2DStart = 0
		}
		if slot.evD2DKernel != 0 {
			cuEventDestroy.Call(slot.evD2DKernel)
			slot.evD2DKernel = 0
		}
		if slot.evD2DEgress != 0 {
			cuEventDestroy.Call(slot.evD2DEgress)
			slot.evD2DEgress = 0
		}
	}
	g.dRadixTable = 0
	g.dIn = 0
	g.dMatch = 0
	g.dCompactPos = 0
	g.dCompactDesc = 0
	g.dBlockCounts = 0
	g.dBlockOffsets = 0
	g.dTotalCount = 0
	g.hTotalCountPtr = 0
	g.hTotalCountSlice = nil
	g.dAllocCap = 0
	g.evStart = 0
	g.evIngress = 0
	g.evKernel = 0
	g.evEgressStart = 0
	g.evEgress = 0

	for _, s := range g.streams {
		if s != 0 {
			cuStreamDestroy.Call(s)
		}
	}
	g.streams = nil

	if g.module != 0 {
		cuModuleUnload.Call(g.module)
		g.module = 0
	}

	if g.ctx != 0 {
		cuCtxSetCurrent.Call(0)
		cuPrimaryCtxRelease.Call(uintptr(g.dev))
		g.ctx = 0
	}
	return nil
}

func (g *GPUCompressor) ensureSlotCap(slot *pipelineSlot, n int) error {
	reqFrameCap := roundUpPow2(n)*2 + 65536
	if n <= slot.dAllocCap && n <= slot.hInCap && n <= slot.hEgressCap && reqFrameCap <= slot.dFrameAllocCap {
		return nil
	}
	newCap := roundUpPow2(n)

	if n > slot.dAllocCap {
		if slot.dIn != 0 {
			cuMemFree.Call(uintptr(slot.dIn))
			slot.dIn = 0
		}
		if slot.dMatch != 0 {
			cuMemFree.Call(uintptr(slot.dMatch))
			slot.dMatch = 0
		}
		if slot.dCompactPos != 0 {
			cuMemFree.Call(uintptr(slot.dCompactPos))
			slot.dCompactPos = 0
		}
		if slot.dCompactDesc != 0 {
			cuMemFree.Call(uintptr(slot.dCompactDesc))
			slot.dCompactDesc = 0
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dIn)), uintptr(newCap)); r != 0 {
			return cuCheck("cuMemAlloc dIn", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dMatch)), uintptr(newCap*4)); r != 0 {
			return cuCheck("cuMemAlloc dMatch", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dCompactPos)), uintptr(newCap*4)); r != 0 {
			return cuCheck("cuMemAlloc dCompactPos", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dCompactDesc)), uintptr(newCap*4)); r != 0 {
			return cuCheck("cuMemAlloc dCompactDesc", r)
		}
		if slot.dTempBitstream != 0 {
			cuMemFree.Call(uintptr(slot.dTempBitstream))
			slot.dTempBitstream = 0
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dTempBitstream)), uintptr(newCap+4096)); r != 0 {
			return cuCheck("cuMemAlloc dTempBitstream", r)
		}
		slot.dAllocCap = newCap
	}

	if n > slot.hInCap {
		if slot.hInPtr != 0 {
			_ = FreePinnedHost(slot.hInPtr)
			slot.hInPtr = 0
			slot.hInBuf = nil
			slot.hInCap = 0
		}
		inBuf, inPtr, err := AllocPinnedHost(newCap)
		if err != nil {
			return fmt.Errorf("AllocPinnedHost hInBuf failed: %w", err)
		}
		slot.hInBuf = inBuf
		slot.hInPtr = inPtr
		slot.hInCap = newCap
	}

	if n > slot.hEgressCap {
		if slot.hPosPtr != 0 {
			_ = FreePinnedHost(slot.hPosPtr)
			slot.hPosPtr = 0
			slot.hPosBuf = nil
		}
		if slot.hDescPtr != 0 {
			_ = FreePinnedHost(slot.hDescPtr)
			slot.hDescPtr = 0
			slot.hDescBuf = nil
		}
		slot.hEgressCap = 0

		posBuf, posPtr, err := AllocPinnedHost(newCap * 4)
		if err != nil {
			return fmt.Errorf("AllocPinnedHost hPosBuf failed: %w", err)
		}
		descBuf, descPtr, err := AllocPinnedHost(newCap * 4)
		if err != nil {
			_ = FreePinnedHost(posPtr)
			return fmt.Errorf("AllocPinnedHost hDescBuf failed: %w", err)
		}
		slot.hPosBuf = posBuf
		slot.hPosPtr = posPtr
		slot.hDescBuf = descBuf
		slot.hDescPtr = descPtr
		slot.hEgressCap = newCap
	}

	if reqFrameCap > slot.dFrameAllocCap {
		if slot.dFrameOut != 0 {
			cuMemFree.Call(uintptr(slot.dFrameOut))
			slot.dFrameOut = 0
		}
		if slot.hFramePtr != 0 {
			_ = FreePinnedHost(slot.hFramePtr)
			slot.hFramePtr = 0
			slot.hFrameSlice = nil
		}
		frameCap := reqFrameCap
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dFrameOut)), uintptr(frameCap)); r != 0 {
			return cuCheck("cuMemAlloc dFrameOut", r)
		}
		fBuf, fPtr, err := AllocPinnedHost(frameCap)
		if err != nil {
			return fmt.Errorf("AllocPinnedHost hFrameSlice failed: %w", err)
		}
		slot.hFrameSlice = fBuf
		slot.hFramePtr = fPtr
		slot.dFrameAllocCap = frameCap
	}

	return nil
}

func (g *GPUCompressor) launchMatchSearch(slot *pipelineSlot, n int) error {
	if n > 65536 && g.fnRadixStrike != 0 && slot.dRadixTable != 0 {
		return g.launchSlotKernel(slot, n)
	}
	stream := slot.stream
	inLen64 := uint64(n)
	dPrev := uint64(0)
	wSize := uint32(Window1M)
	mDepth := uint32(2)
	pSearch := []uintptr{
		uintptr(unsafe.Pointer(&slot.dIn)),
		uintptr(unsafe.Pointer(&inLen64)),
		uintptr(unsafe.Pointer(&dPrev)),
		uintptr(unsafe.Pointer(&slot.dMatch)),
		uintptr(unsafe.Pointer(&wSize)),
		uintptr(unsafe.Pointer(&mDepth)),
	}
	r, _, _ := cuLaunchKernel.Call(
		g.fnSearchMatches,
		224, 1, 1, 256, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pSearch[0])), 0,
	)
	if r != 0 {
		return cuCheck("cuLaunchKernel fnSearchMatches", r)
	}
	return nil
}

func (g *GPUCompressor) launchSlotKernel(slot *pipelineSlot, n int) error {
	stream := slot.stream
	if n > 0 && g.fnRadixStrike != 0 && slot.dRadixTable != 0 {
		tableV4 := uint32(131072) // 524288 words / 4 (uint4 vectorized)
		pClearTable := []uintptr{
			uintptr(unsafe.Pointer(&slot.dRadixTable)),
			uintptr(unsafe.Pointer(&tableV4)),
		}
		cuLaunchKernel.Call(
			g.fnClearRadixTable,
			224, 1, 1, 256, 1, 1, 0, stream,
			uintptr(unsafe.Pointer(&pClearTable[0])), 0,
		)

		matchV4 := uint32((n + 3) / 4)
		pClearMatch := []uintptr{
			uintptr(unsafe.Pointer(&slot.dMatch)),
			uintptr(unsafe.Pointer(&matchV4)),
		}
		cuLaunchKernel.Call(
			g.fnClearRadixTable,
			224, 1, 1, 256, 1, 1, 0, stream,
			uintptr(unsafe.Pointer(&pClearMatch[0])), 0,
		)

		inLen64 := uint64(n)
		wSize := uint32(Window1M)
		pStrike := []uintptr{
			uintptr(unsafe.Pointer(&slot.dIn)),
			uintptr(unsafe.Pointer(&inLen64)),
			uintptr(unsafe.Pointer(&slot.dRadixTable)),
			uintptr(unsafe.Pointer(&slot.dMatch)),
			uintptr(unsafe.Pointer(&wSize)),
		}
		cuLaunchKernel.Call(
			g.fnRadixStrike,
			224, 1, 1, 256, 1, 1, 0, stream,
			uintptr(unsafe.Pointer(&pStrike[0])), 0,
		)
	} else {
		inLen64 := uint64(n)
		dPrev := uint64(0)
		wSize := uint32(Window1M)
		mDepth := uint32(2)
		pSearch := []uintptr{
			uintptr(unsafe.Pointer(&slot.dIn)),
			uintptr(unsafe.Pointer(&inLen64)),
			uintptr(unsafe.Pointer(&dPrev)),
			uintptr(unsafe.Pointer(&slot.dMatch)),
			uintptr(unsafe.Pointer(&wSize)),
			uintptr(unsafe.Pointer(&mDepth)),
		}
		r, _, _ := cuLaunchKernel.Call(
			g.fnSearchMatches,
			224, 1, 1, 256, 1, 1, 0, stream,
			uintptr(unsafe.Pointer(&pSearch[0])), 0,
		)
		if r != 0 {
			fmt.Printf("DEBUG: fnSearchMatches launch failed with code %d\n", r)
		}
	}

	if r, _, _ := cuEventRecord.Call(slot.evKernel, stream); r != 0 {
		return cuCheck("cuEventRecord evKernel", r)
	}

	numBlocks := uint32(224)
	if n/256 < 224 && n > 0 {
		numBlocks = uint32((n + 255) / 256)
	}
	if numBlocks < 1 {
		numBlocks = 1
	}

	n32 := uint32(n)
	pCount := []uintptr{
		uintptr(unsafe.Pointer(&slot.dMatch)),
		uintptr(unsafe.Pointer(&n32)),
		uintptr(unsafe.Pointer(&slot.dBlockCounts)),
	}
	cuLaunchKernel.Call(
		g.fnCompactCount,
		uintptr(numBlocks), 1, 1, 256, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pCount[0])), 0,
	)

	pScan := []uintptr{
		uintptr(unsafe.Pointer(&slot.dBlockCounts)),
		uintptr(unsafe.Pointer(&slot.dBlockOffsets)),
		uintptr(unsafe.Pointer(&slot.dTotalCount)),
		uintptr(unsafe.Pointer(&numBlocks)),
	}
	cuLaunchKernel.Call(
		g.fnCompactScan,
		1, 1, 1, 32, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pScan[0])), 0,
	)

	pScatter := []uintptr{
		uintptr(unsafe.Pointer(&slot.dMatch)),
		uintptr(unsafe.Pointer(&n32)),
		uintptr(unsafe.Pointer(&slot.dBlockOffsets)),
		uintptr(unsafe.Pointer(&slot.dCompactPos)),
		uintptr(unsafe.Pointer(&slot.dCompactDesc)),
	}
	cuLaunchKernel.Call(
		g.fnCompactScatter,
		uintptr(numBlocks), 1, 1, 256, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pScatter[0])), 0,
	)

	if r, _, _ := cuMemcpyDtoHAsync.Call(slot.hTotalCountPtr, uintptr(slot.dTotalCount), 4, stream); r != 0 {
		return cuCheck("cuMemcpyDtoHAsync dTotalCount", r)
	}

	return nil
}

func (g *GPUCompressor) stageEgress(slot *pipelineSlot, totalMatches uint32, posPtr, descPtr uintptr) error {
	stream := slot.stream
	egressBytes := uintptr(totalMatches * 4)
	if r, _, _ := cuEventRecord.Call(slot.evEgressStart, stream); r != 0 {
		return cuCheck("cuEventRecord evEgressStart", r)
	}
	if r, _, _ := cuMemcpyDtoHAsync.Call(posPtr, uintptr(slot.dCompactPos), egressBytes, stream); r != 0 {
		return cuCheck("cuMemcpyDtoHAsync dCompactPos", r)
	}
	if r, _, _ := cuMemcpyDtoHAsync.Call(descPtr, uintptr(slot.dCompactDesc), egressBytes, stream); r != 0 {
		return cuCheck("cuMemcpyDtoHAsync dCompactDesc", r)
	}
	if r, _, _ := cuEventRecord.Call(slot.evEgress, stream); r != 0 {
		return cuCheck("cuEventRecord evEgress", r)
	}
	return nil
}

func (g *GPUCompressor) matchLZ77Single(src []byte) ([]LZ77Match, GPUProfile, error) {
	n := len(src)
	if n == 0 {
		return nil, GPUProfile{}, nil
	}

	slot := &g.slots[0]
	if err := g.ensureSlotCap(slot, n); err != nil {
		return nil, GPUProfile{}, err
	}
	g.dIn = slot.dIn
	g.dMatch = slot.dMatch
	g.dCompactPos = slot.dCompactPos
	g.dCompactDesc = slot.dCompactDesc
	g.dAllocCap = slot.dAllocCap

	stream := slot.stream

	// Stage ingress into preallocated pinned host memory
	copy(slot.hInBuf[:n], src)
	inPtr := slot.hInPtr

	// 1. Profile Start
	if r, _, _ := cuEventRecord.Call(slot.evStart, stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evStart", r)
	}

	// 2. Ingress DMA HtoD
	if r, _, _ := cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), inPtr, uintptr(n), stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuMemcpyHtoDAsync", r)
	}

	// 3. Profile Ingress Done / Kernel Start
	if r, _, _ := cuEventRecord.Call(slot.evIngress, stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evIngress", r)
	}

	// 4. Kernel Execution & Compaction
	if err := g.launchSlotKernel(slot, n); err != nil {
		return nil, GPUProfile{}, err
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuStreamSynchronize totalCount", r)
	}

	totalMatches := min(binary.LittleEndian.Uint32(slot.hTotalCountSlice), uint32(n))

	// 5. Egress DMA Phase
	if totalMatches > 0 {
		if err := g.stageEgress(slot, totalMatches, slot.hPosPtr, slot.hDescPtr); err != nil {
			return nil, GPUProfile{}, err
		}
	} else {
		cuEventRecord.Call(slot.evEgressStart, stream)
		cuEventRecord.Call(slot.evEgress, stream)
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuStreamSynchronize egress", r)
	}

	var tIngress, tKernel, tEgress float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tIngress)), slot.evStart, slot.evIngress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evIngress, slot.evKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tEgress)), slot.evEgressStart, slot.evEgress)

	totalMs := float64(tIngress + tKernel + tEgress)
	pcieOverhead := 0.0
	if totalMs > 0 {
		pcieOverhead = (float64(tIngress+tEgress) / totalMs) * 100.0
	}

	totalEgressBytes := uint64(totalMatches) * 8
	var ingressBW, kernelBW, egressBW, endToEndBW float64
	if tIngress > 0 {
		ingressBW = (float64(n) / (float64(tIngress) / 1000.0)) / 1e9
	}
	if tKernel > 0 {
		kernelBW = (float64(n) / (float64(tKernel) / 1000.0)) / 1e9
	}
	if tEgress > 0 && totalEgressBytes > 0 {
		egressBW = (float64(totalEgressBytes) / (float64(tEgress) / 1000.0)) / 1e9
	}
	if totalMs > 0 {
		endToEndBW = (float64(n) / (totalMs / 1000.0)) / 1e9
	}

	profile := GPUProfile{
		IngressDMABytes:           uint64(n),
		IngressDMATimeMs:          float64(tIngress),
		IngressDMABandwidthGBps:   ingressBW,
		KernelTimeMs:              float64(tKernel),
		InternalVRAMBandwidthGBps: kernelBW,
		EgressDMABytes:            totalEgressBytes,
		EgressDMATimeMs:           float64(tEgress),
		EgressDMABandwidthGBps:    egressBW,
		TotalRoundTripMs:          totalMs,
		EndToEndBandwidthGBps:     endToEndBW,
		PCIeOverheadPercent:       pcieOverhead,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           1,
	}

	if totalMatches == 0 {
		runtime.KeepAlive(src)
		return nil, profile, nil
	}

	posSlice := unsafe.Slice((*uint32)(unsafe.Pointer(&slot.hPosBuf[0])), totalMatches)
	descSlice := unsafe.Slice((*uint32)(unsafe.Pointer(&slot.hDescBuf[0])), totalMatches)

	// Caller-owned slice to guarantee thread safety when concurrent goroutines compress chunks
	matches := make([]LZ77Match, 0, totalMatches)
	for i := 0; i < int(totalMatches); i++ {
		desc := descSlice[i]
		if IsMatch32(desc) {
			l, off := UnpackMatch32(desc)
			matches = append(matches, LZ77Match{Position: posSlice[i], Offset: off, Length: l})
		}
	}

	runtime.KeepAlive(src)
	return matches, profile, nil
}

func (g *GPUCompressor) matchLZ77StreamingLocked(src []byte, chunkSize int) ([]LZ77Match, GPUProfile, error) {
	totalLen := len(src)
	if totalLen == 0 {
		return nil, GPUProfile{}, nil
	}

	if chunkSize <= 0 {
		chunkSize = max(totalLen/2, 16*1024*1024)
	}
	if chunkSize > 64*1024*1024 {
		chunkSize = 64 * 1024 * 1024
	}

	numChunks := (totalLen + chunkSize - 1) / chunkSize
	if numChunks <= 1 {
		return g.matchLZ77Single(src)
	}

	// Double-buffering setup: ensure both slots have preallocated device and pinned host buffers
	for s := range 2 {
		if err := g.ensureSlotCap(&g.slots[s], chunkSize); err != nil {
			return nil, GPUProfile{}, err
		}
	}

	// In-place pin registration for zero CPU copy overhead and maximum PCIe DMA rate (23.5 GB/s)
	var isHostRegistered bool
	if cuMemHostRegister.Find() == nil && cuMemHostUnregister.Find() == nil {
		r, _, _ := cuMemHostRegister.Call(uintptr(unsafe.Pointer(&src[0])), uintptr(totalLen), 0)
		if r == 0 {
			isHostRegistered = true
			defer cuMemHostUnregister.Call(uintptr(unsafe.Pointer(&src[0])))
		}
	}

	var evGlobalStart, evGlobalStop uintptr
	cuEventCreate.Call(uintptr(unsafe.Pointer(&evGlobalStart)), 0)
	defer cuEventDestroy.Call(evGlobalStart)
	cuEventCreate.Call(uintptr(unsafe.Pointer(&evGlobalStop)), 0)
	defer cuEventDestroy.Call(evGlobalStop)

	var totalIngressBytes, totalEgressBytes uint64
	var sumIngressMs, sumKernelMs, sumEgressMs float64
	chunkMatches := make([][]LZ77Match, numChunks)

	launchChunk := func(k int, slot *pipelineSlot, waitEvent uintptr) error {
		start := k * chunkSize
		end := min(start+chunkSize, totalLen)
		chunkLen := end - start

		var inPtr uintptr
		if isHostRegistered {
			inPtr = uintptr(unsafe.Pointer(&src[start]))
		} else {
			copy(slot.hInBuf[:chunkLen], src[start:end])
			inPtr = slot.hInPtr
		}

		if waitEvent != 0 {
			cuStreamWaitEvent.Call(slot.stream, waitEvent, 0)
		}

		if r, _, _ := cuEventRecord.Call(slot.evStart, slot.stream); r != 0 {
			return cuCheck("cuEventRecord evStart", r)
		}
		if r, _, _ := cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), inPtr, uintptr(chunkLen), slot.stream); r != 0 {
			return cuCheck("cuMemcpyHtoDAsync", r)
		}
		if r, _, _ := cuEventRecord.Call(slot.evIngress, slot.stream); r != 0 {
			return cuCheck("cuEventRecord evIngress", r)
		}
		totalIngressBytes += uint64(chunkLen)

		return g.launchSlotKernel(slot, chunkLen)
	}

	cuEventRecord.Call(evGlobalStart, g.slots[0].stream)

	// Step 1: Prime the pipeline - Launch Chunk 0 on slot 0
	if err := launchChunk(0, &g.slots[0], 0); err != nil {
		return nil, GPUProfile{}, err
	}

	// Step 2: If there is a chunk 1, launch Chunk 1 on slot 1 immediately (waits for slot 0 ingress to finish)
	if numChunks > 1 {
		if err := launchChunk(1, &g.slots[1], g.slots[0].evIngress); err != nil {
			return nil, GPUProfile{}, err
		}
	}

	type pendingChunk struct {
		chunkIdx     int
		baseOffset   uint32
		totalMatches uint32
	}
	var slotPending [2]*pendingChunk

	unpackPending := func(s int) {
		p := slotPending[s]
		if p == nil || p.totalMatches == 0 {
			slotPending[s] = nil
			return
		}
		slot := &g.slots[s]
		matches := make([]LZ77Match, p.totalMatches)
		pw := unsafe.Slice((*uint32)(unsafe.Pointer(&slot.hPosBuf[0])), p.totalMatches)
		dw := unsafe.Slice((*uint32)(unsafe.Pointer(&slot.hDescBuf[0])), p.totalMatches)
		bOff := p.baseOffset

		for i := 0; i < int(p.totalMatches); i++ {
			desc := dw[i]
			matches[i] = LZ77Match{
				Position: bOff + pw[i],
				Offset:   desc & MaxOffset32,
				Length:   uint16(desc >> 20),
			}
		}
		chunkMatches[p.chunkIdx] = matches
		slotPending[s] = nil
	}

	// Step 3: Pipelined loop across all chunks k = 0 .. numChunks-1
	for k := range numChunks {
		sIdx := k % 2
		slot := &g.slots[sIdx]
		start := k * chunkSize
		end := min(start+chunkSize, totalLen)
		chunkLen := end - start
		isLast := (k == numChunks-1)

		// Wait for Chunk k kernel and compaction to complete
		if r, _, _ := cuStreamSynchronize.Call(slot.stream); r != 0 {
			return nil, GPUProfile{}, cuCheck("cuStreamSynchronize kernel", r)
		}

		// At this point, any previous egress on this slot (chunk k-2) is 100% completed
		unpackPending(sIdx)

		totalMatches := min(binary.LittleEndian.Uint32(slot.hTotalCountSlice), uint32(chunkLen))

		var tIngress, tKernel float32
		cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tIngress)), slot.evStart, slot.evIngress)
		cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evIngress, slot.evKernel)
		sumIngressMs += float64(tIngress)
		sumKernelMs += float64(tKernel)

		if totalMatches > 0 {
			egressBytes := totalMatches * 4
			totalEgressBytes += uint64(egressBytes * 2)

			if err := g.stageEgress(slot, totalMatches, slot.hPosPtr, slot.hDescPtr); err != nil {
				return nil, GPUProfile{}, err
			}
			if isLast {
				cuEventRecord.Call(evGlobalStop, slot.stream)
			}
			slotPending[sIdx] = &pendingChunk{
				chunkIdx:     k,
				baseOffset:   uint32(start),
				totalMatches: totalMatches,
			}
		} else {
			cuEventRecord.Call(slot.evEgressStart, slot.stream)
			cuEventRecord.Call(slot.evEgress, slot.stream)
			if isLast {
				cuEventRecord.Call(evGlobalStop, slot.stream)
			}
		}

		// Immediately launch Chunk k+2 on slot (waits for chunk k+1 ingress to complete)
		if k+2 < numChunks {
			nextSlot := &g.slots[(k+2)%2] // == slot
			otherSlot := &g.slots[(k+1)%2]
			if err := launchChunk(k+2, nextSlot, otherSlot.evIngress); err != nil {
				return nil, GPUProfile{}, err
			}
		}
	}

	// Synchronize both slots to finish any pending egress DMAs
	for s := range 2 {
		if r, _, _ := cuStreamSynchronize.Call(g.slots[s].stream); r != 0 {
			return nil, GPUProfile{}, cuCheck("cuStreamSynchronize drain", r)
		}
		unpackPending(s)
	}

	var tGlobalMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tGlobalMs)), evGlobalStart, evGlobalStop)

	totalMs := float64(tGlobalMs)
	var ingressBW, kernelBW, egressBW, endToEndBW float64
	if sumIngressMs > 0 {
		ingressBW = (float64(totalIngressBytes) / (sumIngressMs / 1000.0)) / 1e9
	}
	if sumKernelMs > 0 {
		kernelBW = (float64(totalIngressBytes) / (sumKernelMs / 1000.0)) / 1e9
	}
	if sumEgressMs > 0 && totalEgressBytes > 0 {
		egressBW = (float64(totalEgressBytes) / (sumEgressMs / 1000.0)) / 1e9
	}
	if totalMs > 0 {
		endToEndBW = (float64(totalLen) / (totalMs / 1000.0)) / 1e9
	}

	pcieOverhead := 0.0
	if totalMs > 0 {
		pcieOverhead = (float64(sumIngressMs+sumEgressMs) / totalMs) * 100.0
	}

	profile := GPUProfile{
		IngressDMABytes:           totalIngressBytes,
		IngressDMATimeMs:          sumIngressMs,
		IngressDMABandwidthGBps:   ingressBW,
		KernelTimeMs:              sumKernelMs,
		InternalVRAMBandwidthGBps: kernelBW,
		EgressDMABytes:            totalEgressBytes,
		EgressDMATimeMs:           sumEgressMs,
		EgressDMABandwidthGBps:    egressBW,
		TotalRoundTripMs:          totalMs,
		EndToEndBandwidthGBps:     endToEndBW,
		PCIeOverheadPercent:       pcieOverhead,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           numChunks,
	}

	var totalMatchesAcrossAll int
	for _, cm := range chunkMatches {
		totalMatchesAcrossAll += len(cm)
	}

	matches := make([]LZ77Match, totalMatchesAcrossAll)
	dest := 0
	for _, cm := range chunkMatches {
		copy(matches[dest:], cm)
		dest += len(cm)
	}

	runtime.KeepAlive(src)
	return matches, profile, nil
}

// MatchLZ77Streaming executes double-buffered multi-stream pipelining across multi-megabyte payloads.
func (g *GPUCompressor) MatchLZ77Streaming(src []byte, chunkSize int) ([]LZ77Match, GPUProfile, error) {
	if pErr := mockMatchErr.Load(); pErr != nil {
		return nil, GPUProfile{}, *pErr
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	return g.matchLZ77StreamingLocked(src, chunkSize)
}

// MatchLZ77 executes parallel LZ77 dictionary search on GPU.
// Payloads > 16 MiB are automatically processed via double-buffered stream pipelining.
func (g *GPUCompressor) MatchLZ77(src []byte) ([]LZ77Match, GPUProfile, error) {
	if pErr := mockMatchErr.Load(); pErr != nil {
		return nil, GPUProfile{}, *pErr
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	if len(src) > 16*1024*1024 {
		return g.matchLZ77StreamingLocked(src, 8*1024*1024)
	}

	return g.matchLZ77Single(src)
}

func (g *GPUCompressor) ensureTokenizerCap(slot *pipelineSlot, n int) error {
	numChunks := max((n+TokenizerTileSize-1)/TokenizerTileSize, 1)

	if numChunks > slot.dTokChunksAlloc {
		if slot.dChunkCmds != 0 {
			cuMemFree.Call(uintptr(slot.dChunkCmds))
			slot.dChunkCmds = 0
		}
		if slot.dChunkOffs != 0 {
			cuMemFree.Call(uintptr(slot.dChunkOffs))
			slot.dChunkOffs = 0
		}
		if slot.dChunkLits != 0 {
			cuMemFree.Call(uintptr(slot.dChunkLits))
			slot.dChunkLits = 0
		}
		if slot.dChunkCmdLen != 0 {
			cuMemFree.Call(uintptr(slot.dChunkCmdLen))
			slot.dChunkCmdLen = 0
		}
		if slot.dChunkOffLen != 0 {
			cuMemFree.Call(uintptr(slot.dChunkOffLen))
			slot.dChunkOffLen = 0
		}
		if slot.dChunkLitLen != 0 {
			cuMemFree.Call(uintptr(slot.dChunkLitLen))
			slot.dChunkLitLen = 0
		}
		if slot.dChunkCmdOffset != 0 {
			cuMemFree.Call(uintptr(slot.dChunkCmdOffset))
			slot.dChunkCmdOffset = 0
		}
		if slot.dChunkOffOffset != 0 {
			cuMemFree.Call(uintptr(slot.dChunkOffOffset))
			slot.dChunkOffOffset = 0
		}
		if slot.dChunkLitOffset != 0 {
			cuMemFree.Call(uintptr(slot.dChunkLitOffset))
			slot.dChunkLitOffset = 0
		}

		newChunks := roundUpPow2(numChunks)
		cmdBytes := uintptr(newChunks * MaxTileCmdBytes)
		offBytes := uintptr(newChunks * MaxTileOffBytes)
		litBytes := uintptr(newChunks * MaxTileLitBytes)
		metaBytes := uintptr(newChunks * 4)

		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkCmds)), cmdBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkCmds", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkOffs)), offBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkOffs", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkLits)), litBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkLits", r)
		}

		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkCmdLen)), metaBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkCmdLen", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkOffLen)), metaBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkOffLen", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkLitLen)), metaBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkLitLen", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkCmdOffset)), metaBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkCmdOffset", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkOffOffset)), metaBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkOffOffset", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dChunkLitOffset)), metaBytes); r != 0 {
			return cuCheck("cuMemAlloc dChunkLitOffset", r)
		}

		slot.dTokChunksAlloc = newChunks
	}

	if slot.dTotals == 0 {
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dTotals)), 16); r != 0 {
			return cuCheck("cuMemAlloc dTotals", r)
		}
		totSlice, totPtr, err := AllocPinnedHost(16)
		if err != nil {
			return fmt.Errorf("AllocPinnedHost hTotalsSlice failed: %w", err)
		}
		slot.hTotalsSlice = totSlice
		slot.hTotalsPtr = totPtr
	}

	if n > slot.dTokAllocCap {
		if slot.dFinalCmds != 0 {
			cuMemFree.Call(uintptr(slot.dFinalCmds))
			slot.dFinalCmds = 0
		}
		if slot.dFinalOffs != 0 {
			cuMemFree.Call(uintptr(slot.dFinalOffs))
			slot.dFinalOffs = 0
		}
		if slot.dFinalLits != 0 {
			cuMemFree.Call(uintptr(slot.dFinalLits))
			slot.dFinalLits = 0
		}

		newCap := roundUpPow2(n)
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dFinalCmds)), uintptr(newCap)); r != 0 {
			return cuCheck("cuMemAlloc dFinalCmds", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dFinalOffs)), uintptr(newCap)); r != 0 {
			return cuCheck("cuMemAlloc dFinalOffs", r)
		}
		if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dFinalLits)), uintptr(newCap)); r != 0 {
			return cuCheck("cuMemAlloc dFinalLits", r)
		}
		slot.dTokAllocCap = newCap
	}

	if n > slot.hTokEgressCap {
		if slot.hFinalCmdPtr != 0 {
			_ = FreePinnedHost(slot.hFinalCmdPtr)
			slot.hFinalCmdPtr = 0
			slot.hFinalCmdSlice = nil
		}
		if slot.hFinalOffPtr != 0 {
			_ = FreePinnedHost(slot.hFinalOffPtr)
			slot.hFinalOffPtr = 0
			slot.hFinalOffSlice = nil
		}
		if slot.hFinalLitPtr != 0 {
			_ = FreePinnedHost(slot.hFinalLitPtr)
			slot.hFinalLitPtr = 0
			slot.hFinalLitSlice = nil
		}

		newCap := roundUpPow2(n)
		cBuf, cPtr, err := AllocPinnedHost(newCap)
		if err != nil {
			return fmt.Errorf("AllocPinnedHost hFinalCmdSlice failed: %w", err)
		}
		oBuf, oPtr, err := AllocPinnedHost(newCap)
		if err != nil {
			_ = FreePinnedHost(cPtr)
			return fmt.Errorf("AllocPinnedHost hFinalOffSlice failed: %w", err)
		}
		lBuf, lPtr, err := AllocPinnedHost(newCap)
		if err != nil {
			_ = FreePinnedHost(cPtr)
			_ = FreePinnedHost(oPtr)
			return fmt.Errorf("AllocPinnedHost hFinalLitSlice failed: %w", err)
		}

		slot.hFinalCmdSlice = cBuf
		slot.hFinalCmdPtr = cPtr
		slot.hFinalOffSlice = oBuf
		slot.hFinalOffPtr = oPtr
		slot.hFinalLitSlice = lBuf
		slot.hFinalLitPtr = lPtr
		slot.hTokEgressCap = newCap
	}

	return nil
}

func (g *GPUCompressor) launchTokenizerKernels(slot *pipelineSlot, n int) error {
	stream := slot.stream
	numChunks := max((n+TokenizerTileSize-1)/TokenizerTileSize, 1)

	inLen64 := uint64(n)
	tileSize32 := uint32(TokenizerTileSize)
	maxCmdSlot32 := uint32(MaxTileCmdBytes)
	maxOffSlot32 := uint32(MaxTileOffBytes)
	maxLitSlot32 := uint32(MaxTileLitBytes)
	numChunks32 := uint32(numChunks)

	// Pass 1: przm_fused_tokenizer_kernel
	pTok := []uintptr{
		uintptr(unsafe.Pointer(&slot.dIn)),
		uintptr(unsafe.Pointer(&inLen64)),
		uintptr(unsafe.Pointer(&slot.dMatch)),
		uintptr(unsafe.Pointer(&slot.dChunkCmds)),
		uintptr(unsafe.Pointer(&slot.dChunkOffs)),
		uintptr(unsafe.Pointer(&slot.dChunkLits)),
		uintptr(unsafe.Pointer(&slot.dChunkCmdLen)),
		uintptr(unsafe.Pointer(&slot.dChunkOffLen)),
		uintptr(unsafe.Pointer(&slot.dChunkLitLen)),
		uintptr(unsafe.Pointer(&tileSize32)),
		uintptr(unsafe.Pointer(&maxCmdSlot32)),
		uintptr(unsafe.Pointer(&maxOffSlot32)),
		uintptr(unsafe.Pointer(&maxLitSlot32)),
		uintptr(unsafe.Pointer(&numChunks32)),
	}
	tokBlockSize := 32
	tokGridSize := numChunks

	if r, _, _ := cuLaunchKernel.Call(
		g.fnFusedTokenizer,
		uintptr(tokGridSize), 1, 1, uintptr(tokBlockSize), 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pTok[0])), 0,
	); r != 0 {
		return cuCheck("cuLaunchKernel przm_fused_tokenizer_kernel", r)
	}

	cuEventRecord.Call(slot.evTokPass1, stream)

	// Pass 2: przm_tokenizer_scan_kernel (3 warps = 96 threads)
	pScan := []uintptr{
		uintptr(unsafe.Pointer(&slot.dChunkCmdLen)),
		uintptr(unsafe.Pointer(&slot.dChunkOffLen)),
		uintptr(unsafe.Pointer(&slot.dChunkLitLen)),
		uintptr(unsafe.Pointer(&slot.dChunkCmdOffset)),
		uintptr(unsafe.Pointer(&slot.dChunkOffOffset)),
		uintptr(unsafe.Pointer(&slot.dChunkLitOffset)),
		uintptr(unsafe.Pointer(&slot.dTotals)),
		uintptr(unsafe.Pointer(&numChunks32)),
	}
	if r, _, _ := cuLaunchKernel.Call(
		g.fnTokenizerScan,
		1, 1, 1, 96, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pScan[0])), 0,
	); r != 0 {
		return cuCheck("cuLaunchKernel przm_tokenizer_scan_kernel", r)
	}
	cuEventRecord.Call(slot.evTokPass2, stream)

	// Pass 3: przm_tokenizer_gather_kernel
	gatherGridSize := min(numChunks, 256)
	pGather := []uintptr{
		uintptr(unsafe.Pointer(&slot.dChunkCmds)),
		uintptr(unsafe.Pointer(&slot.dChunkOffs)),
		uintptr(unsafe.Pointer(&slot.dChunkLits)),
		uintptr(unsafe.Pointer(&slot.dChunkCmdLen)),
		uintptr(unsafe.Pointer(&slot.dChunkOffLen)),
		uintptr(unsafe.Pointer(&slot.dChunkLitLen)),
		uintptr(unsafe.Pointer(&slot.dChunkCmdOffset)),
		uintptr(unsafe.Pointer(&slot.dChunkOffOffset)),
		uintptr(unsafe.Pointer(&slot.dChunkLitOffset)),
		uintptr(unsafe.Pointer(&slot.dFinalCmds)),
		uintptr(unsafe.Pointer(&slot.dFinalOffs)),
		uintptr(unsafe.Pointer(&slot.dFinalLits)),
		uintptr(unsafe.Pointer(&maxCmdSlot32)),
		uintptr(unsafe.Pointer(&maxOffSlot32)),
		uintptr(unsafe.Pointer(&maxLitSlot32)),
		uintptr(unsafe.Pointer(&numChunks32)),
	}
	if r, _, _ := cuLaunchKernel.Call(
		g.fnTokenizerGather,
		uintptr(gatherGridSize), 1, 1, 256, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pGather[0])), 0,
	); r != 0 {
		return cuCheck("cuLaunchKernel przm_tokenizer_gather_kernel", r)
	}

	return nil
}

func (g *GPUCompressor) tokenizeSingle(src []byte) ([]byte, []byte, []byte, TokenizerProfile, error) {
	n := len(src)
	if n == 0 {
		return nil, nil, nil, TokenizerProfile{}, nil
	}

	slot := &g.slots[0]
	if err := g.ensureSlotCap(slot, n); err != nil {
		return nil, nil, nil, TokenizerProfile{}, err
	}
	if err := g.ensureTokenizerCap(slot, n); err != nil {
		return nil, nil, nil, TokenizerProfile{}, err
	}

	stream := slot.stream

	copy(slot.hInBuf[:n], src)
	inPtr := slot.hInPtr

	if r, _, _ := cuEventRecord.Call(slot.evStart, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evStart", r)
	}
	if r, _, _ := cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), inPtr, uintptr(n), stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyHtoDAsync", r)
	}

	if err := g.launchMatchSearch(slot, n); err != nil {
		return nil, nil, nil, TokenizerProfile{}, err
	}

	if r, _, _ := cuEventRecord.Call(slot.evTokStart, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evTokStart", r)
	}

	if err := g.launchTokenizerKernels(slot, n); err != nil {
		return nil, nil, nil, TokenizerProfile{}, err
	}

	if r, _, _ := cuEventRecord.Call(slot.evTokKernel, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evTokKernel", r)
	}

	if r, _, _ := cuMemcpyDtoHAsync.Call(slot.hTotalsPtr, uintptr(slot.dTotals), 12, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dTotals", r)
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuStreamSynchronize totals", r)
	}

	totalCmd := binary.LittleEndian.Uint32(slot.hTotalsSlice[0:4])
	totalOff := binary.LittleEndian.Uint32(slot.hTotalsSlice[4:8])
	totalLit := binary.LittleEndian.Uint32(slot.hTotalsSlice[8:12])

	if totalCmd > 0 {
		if r, _, _ := cuMemcpyDtoHAsync.Call(
			slot.hFinalCmdPtr,
			uintptr(slot.dFinalCmds),
			uintptr(totalCmd),
			stream,
		); r != 0 {
			return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dFinalCmds", r)
		}
	}
	if totalOff > 0 {
		if r, _, _ := cuMemcpyDtoHAsync.Call(
			slot.hFinalOffPtr,
			uintptr(slot.dFinalOffs),
			uintptr(totalOff),
			stream,
		); r != 0 {
			return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dFinalOffs", r)
		}
	}
	if totalLit > 0 {
		if r, _, _ := cuMemcpyDtoHAsync.Call(
			slot.hFinalLitPtr,
			uintptr(slot.dFinalLits),
			uintptr(totalLit),
			stream,
		); r != 0 {
			return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dFinalLits", r)
		}
	}

	if r, _, _ := cuEventRecord.Call(slot.evTokEgress, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evTokEgress", r)
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuStreamSynchronize egress", r)
	}

	var tKernel, tTotal, tPass1, tPass2, tPass3 float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evTokStart, slot.evTokKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass1)), slot.evTokStart, slot.evTokPass1)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass2)), slot.evTokPass1, slot.evTokPass2)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass3)), slot.evTokPass2, slot.evTokKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tTotal)), slot.evStart, slot.evTokEgress)

	var throughput float64
	if tKernel > 0 {
		throughput = (float64(n) / (float64(tKernel) / 1000.0)) / 1e9
	}

	numChunks := (n + TokenizerTileSize - 1) / TokenizerTileSize
	profile := TokenizerProfile{
		KernelTimeMs:     float64(tKernel),
		Pass1Ms:          float64(tPass1),
		Pass2Ms:          float64(tPass2),
		Pass3Ms:          float64(tPass3),
		TotalRoundTripMs: float64(tTotal),
		ThroughputGBps:   throughput,
		CmdBytes:         int(totalCmd),
		OffsetBytes:      int(totalOff),
		LiteralBytes:     int(totalLit),
		NumChunks:        numChunks,
	}

	cmdBuf := make([]byte, totalCmd)
	if totalCmd > 0 {
		copy(cmdBuf, slot.hFinalCmdSlice[:totalCmd])
	}
	offsetBuf := make([]byte, totalOff)
	if totalOff > 0 {
		copy(offsetBuf, slot.hFinalOffSlice[:totalOff])
	}
	literalBuf := make([]byte, totalLit)
	if totalLit > 0 {
		copy(literalBuf, slot.hFinalLitSlice[:totalLit])
	}

	runtime.KeepAlive(src)
	return cmdBuf, offsetBuf, literalBuf, profile, nil
}

// Tokenize executes GPU LZ77 match search followed by parallel fused tokenization on GPU.
// Returns valid PRZM cmdBuf, offsetBuf, literalBuf, and profiling metrics.
func (g *GPUCompressor) Tokenize(src []byte) ([]byte, []byte, []byte, TokenizerProfile, error) {
	if pErr := mockMatchErr.Load(); pErr != nil {
		return nil, nil, nil, TokenizerProfile{}, *pErr
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	return g.tokenizeSingle(src)
}

// TokenizeDeviceOnly runs only the parallel fused tokenizer kernel pipeline on existing
// GPU memory buffers (assuming dIn and dMatch are already populated).
// Returns valid PRZM cmdBuf, offsetBuf, literalBuf, and profiling metrics.
func (g *GPUCompressor) TokenizeDeviceOnly(n int) ([]byte, []byte, []byte, TokenizerProfile, error) {
	if n <= 0 {
		return nil, nil, nil, TokenizerProfile{}, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	slot := &g.slots[0]
	if err := g.ensureTokenizerCap(slot, n); err != nil {
		return nil, nil, nil, TokenizerProfile{}, err
	}

	stream := slot.stream

	if r, _, _ := cuEventRecord.Call(slot.evTokStart, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evTokStart", r)
	}

	if err := g.launchTokenizerKernels(slot, n); err != nil {
		return nil, nil, nil, TokenizerProfile{}, err
	}

	if r, _, _ := cuEventRecord.Call(slot.evTokKernel, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evTokKernel", r)
	}

	if r, _, _ := cuMemcpyDtoHAsync.Call(slot.hTotalsPtr, uintptr(slot.dTotals), 12, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dTotals", r)
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuStreamSynchronize totals", r)
	}

	totalCmd := binary.LittleEndian.Uint32(slot.hTotalsSlice[0:4])
	totalOff := binary.LittleEndian.Uint32(slot.hTotalsSlice[4:8])
	totalLit := binary.LittleEndian.Uint32(slot.hTotalsSlice[8:12])

	if totalCmd > 0 {
		if r, _, _ := cuMemcpyDtoHAsync.Call(
			slot.hFinalCmdPtr,
			uintptr(slot.dFinalCmds),
			uintptr(totalCmd),
			stream,
		); r != 0 {
			return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dFinalCmds", r)
		}
	}
	if totalOff > 0 {
		if r, _, _ := cuMemcpyDtoHAsync.Call(
			slot.hFinalOffPtr,
			uintptr(slot.dFinalOffs),
			uintptr(totalOff),
			stream,
		); r != 0 {
			return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dFinalOffs", r)
		}
	}
	if totalLit > 0 {
		if r, _, _ := cuMemcpyDtoHAsync.Call(
			slot.hFinalLitPtr,
			uintptr(slot.dFinalLits),
			uintptr(totalLit),
			stream,
		); r != 0 {
			return nil, nil, nil, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dFinalLits", r)
		}
	}

	if r, _, _ := cuEventRecord.Call(slot.evTokEgress, stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuEventRecord evTokEgress", r)
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, nil, nil, TokenizerProfile{}, cuCheck("cuStreamSynchronize egress", r)
	}

	var tKernel, tTotal, tPass1, tPass2, tPass3 float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evTokStart, slot.evTokKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass1)), slot.evTokStart, slot.evTokPass1)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass2)), slot.evTokPass1, slot.evTokPass2)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass3)), slot.evTokPass2, slot.evTokKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tTotal)), slot.evStart, slot.evTokEgress)

	var throughput float64
	if tKernel > 0 {
		throughput = (float64(n) / (float64(tKernel) / 1000.0)) / 1e9
	}

	numChunks := (n + TokenizerTileSize - 1) / TokenizerTileSize
	profile := TokenizerProfile{
		KernelTimeMs:     float64(tKernel),
		Pass1Ms:          float64(tPass1),
		Pass2Ms:          float64(tPass2),
		Pass3Ms:          float64(tPass3),
		TotalRoundTripMs: float64(tTotal),
		ThroughputGBps:   throughput,
		CmdBytes:         int(totalCmd),
		OffsetBytes:      int(totalOff),
		LiteralBytes:     int(totalLit),
		NumChunks:        numChunks,
	}

	cmdBuf := make([]byte, totalCmd)
	if totalCmd > 0 {
		copy(cmdBuf, slot.hFinalCmdSlice[:totalCmd])
	}
	offsetBuf := make([]byte, totalOff)
	if totalOff > 0 {
		copy(offsetBuf, slot.hFinalOffSlice[:totalOff])
	}
	literalBuf := make([]byte, totalLit)
	if totalLit > 0 {
		copy(literalBuf, slot.hFinalLitSlice[:totalLit])
	}

	return cmdBuf, offsetBuf, literalBuf, profile, nil
}

// tokenizeDeviceLocked executes match search and tokenizer pipeline on src, keeping
// the compacted streams resident in VRAM and returning a DeviceTokenStreams descriptor.
// Must be called with g.mu held and context current.
func (g *GPUCompressor) tokenizeDeviceLocked(
	slot *pipelineSlot,
	src []byte,
) (DeviceTokenStreams, TokenizerProfile, error) {
	n := len(src)
	if n == 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, nil
	}

	if err := g.ensureSlotCap(slot, n); err != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, err
	}
	if err := g.ensureTokenizerCap(slot, n); err != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, err
	}

	stream := slot.stream

	copy(slot.hInBuf[:n], src)
	inPtr := slot.hInPtr

	if r, _, _ := cuEventRecord.Call(slot.evStart, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuEventRecord evStart", r)
	}
	if r, _, _ := cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), inPtr, uintptr(n), stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuMemcpyHtoDAsync", r)
	}

	if err := g.launchMatchSearch(slot, n); err != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, err
	}

	if r, _, _ := cuEventRecord.Call(slot.evTokStart, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuEventRecord evTokStart", r)
	}
	if err := g.launchTokenizerKernels(slot, n); err != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, err
	}
	if r, _, _ := cuEventRecord.Call(slot.evTokKernel, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuEventRecord evTokKernel", r)
	}

	if r, _, _ := cuMemcpyDtoHAsync.Call(slot.hTotalsPtr, uintptr(slot.dTotals), 12, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dTotals", r)
	}
	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuStreamSynchronize totals", r)
	}

	totalCmd := binary.LittleEndian.Uint32(slot.hTotalsSlice[0:4])
	totalOff := binary.LittleEndian.Uint32(slot.hTotalsSlice[4:8])
	totalLit := binary.LittleEndian.Uint32(slot.hTotalsSlice[8:12])

	var tKernel, tPass1, tPass2, tPass3 float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evTokStart, slot.evTokKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass1)), slot.evTokStart, slot.evTokPass1)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass2)), slot.evTokPass1, slot.evTokPass2)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass3)), slot.evTokPass2, slot.evTokKernel)

	var throughput float64
	if tKernel > 0 {
		throughput = (float64(n) / (float64(tKernel) / 1000.0)) / 1e9
	}
	numChunks := (n + TokenizerTileSize - 1) / TokenizerTileSize

	prof := TokenizerProfile{
		KernelTimeMs:     float64(tKernel),
		Pass1Ms:          float64(tPass1),
		Pass2Ms:          float64(tPass2),
		Pass3Ms:          float64(tPass3),
		TotalRoundTripMs: float64(tKernel),
		ThroughputGBps:   throughput,
		CmdBytes:         int(totalCmd),
		OffsetBytes:      int(totalOff),
		LiteralBytes:     int(totalLit),
		NumChunks:        numChunks,
	}

	devStreams := DeviceTokenStreams{
		DFinalCmds: slot.dFinalCmds,
		DFinalOffs: slot.dFinalOffs,
		DFinalLits: slot.dFinalLits,
		DTotals:    slot.dTotals,
		CmdLen:     totalCmd,
		OffLen:     totalOff,
		LitLen:     totalLit,
		NumChunks:  uint32(numChunks),
		Stream:     stream,
	}

	return devStreams, prof, nil
}

// TokenizeDevice executes match search and tokenizer pipeline on src, keeping
// the compacted streams resident in VRAM and returning a DeviceTokenStreams descriptor.
func (g *GPUCompressor) TokenizeDevice(src []byte) (DeviceTokenStreams, TokenizerProfile, error) {
	if pErr := mockMatchErr.Load(); pErr != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, *pErr
	}
	n := len(src)
	if n == 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	slot := &g.slots[0]
	return g.tokenizeDeviceLocked(slot, src)
}

// TokenizePointersDeviceOnly executes the tokenizer pipeline directly on pre-staged GPU buffers
// (slot.dIn and slot.dMatch), providing zero-copy device pointers in VRAM without host copies.
func (g *GPUCompressor) TokenizePointersDeviceOnly(n int) (DeviceTokenStreams, TokenizerProfile, error) {
	if n <= 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	slot := &g.slots[0]
	if err := g.ensureTokenizerCap(slot, n); err != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, err
	}

	stream := slot.stream

	if r, _, _ := cuEventRecord.Call(slot.evTokStart, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuEventRecord evTokStart", r)
	}
	if err := g.launchTokenizerKernels(slot, n); err != nil {
		return DeviceTokenStreams{}, TokenizerProfile{}, err
	}
	if r, _, _ := cuEventRecord.Call(slot.evTokKernel, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuEventRecord evTokKernel", r)
	}

	if r, _, _ := cuMemcpyDtoHAsync.Call(slot.hTotalsPtr, uintptr(slot.dTotals), 12, stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuMemcpyDtoHAsync dTotals", r)
	}
	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return DeviceTokenStreams{}, TokenizerProfile{}, cuCheck("cuStreamSynchronize totals", r)
	}

	totalCmd := binary.LittleEndian.Uint32(slot.hTotalsSlice[0:4])
	totalOff := binary.LittleEndian.Uint32(slot.hTotalsSlice[4:8])
	totalLit := binary.LittleEndian.Uint32(slot.hTotalsSlice[8:12])

	var tKernel, tPass1, tPass2, tPass3 float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evTokStart, slot.evTokKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass1)), slot.evTokStart, slot.evTokPass1)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass2)), slot.evTokPass1, slot.evTokPass2)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tPass3)), slot.evTokPass2, slot.evTokKernel)

	var throughput float64
	if tKernel > 0 {
		throughput = (float64(n) / (float64(tKernel) / 1000.0)) / 1e9
	}
	numChunks := (n + TokenizerTileSize - 1) / TokenizerTileSize

	prof := TokenizerProfile{
		KernelTimeMs:     float64(tKernel),
		Pass1Ms:          float64(tPass1),
		Pass2Ms:          float64(tPass2),
		Pass3Ms:          float64(tPass3),
		TotalRoundTripMs: float64(tKernel),
		ThroughputGBps:   throughput,
		CmdBytes:         int(totalCmd),
		OffsetBytes:      int(totalOff),
		LiteralBytes:     int(totalLit),
		NumChunks:        numChunks,
	}

	devStreams := DeviceTokenStreams{
		DFinalCmds: slot.dFinalCmds,
		DFinalOffs: slot.dFinalOffs,
		DFinalLits: slot.dFinalLits,
		DTotals:    slot.dTotals,
		CmdLen:     totalCmd,
		OffLen:     totalOff,
		LitLen:     totalLit,
		NumChunks:  uint32(numChunks),
		Stream:     stream,
	}

	return devStreams, prof, nil
}

// TokenizeDeviceZeroCopy executes the full GPU LZ77 matching and stream compaction tokenizer pipeline,
// returning zero-copy device pointers and metadata in DeviceTokenStreams directly in VRAM.
func (g *GPUCompressor) TokenizeDeviceZeroCopy(src []byte) (DeviceTokenStreams, TokenizerProfile, error) {
	return g.TokenizeDevice(src)
}

// DownloadDeviceBytes downloads size bytes from device pointer dPtr to a host byte slice.
// Primarily used for verification and debugging of zero-copy device streams.
func (g *GPUCompressor) DownloadDeviceBytes(dPtr uint64, size int) ([]byte, error) {
	if size == 0 {
		return []byte{}, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, cuCheck("cuCtxSetCurrent", r)
	}

	slot := &g.slots[0]
	stream := slot.stream
	out := make([]byte, size)
	if r, _, _ := cuMemcpyDtoHAsync.Call(
		uintptr(unsafe.Pointer(&out[0])),
		uintptr(dPtr),
		uintptr(size),
		stream,
	); r != 0 {
		return nil, cuCheck("cuMemcpyDtoHAsync", r)
	}
	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, cuCheck("cuStreamSynchronize", r)
	}
	return out, nil
}

// encodeD2DLocked performs seamless device-to-device entropy encoding and frame assembly
// directly in VRAM without host roundtripping.
// Must be called with g.mu held and context current.
func (g *GPUCompressor) encodeD2DLocked(
	slot *pipelineSlot,
	streams DeviceTokenStreams,
	uSize, windowLog int,
) ([]byte, GPUProfile, error) {
	if uSize <= 0 {
		return nil, GPUProfile{}, nil
	}

	if err := g.ensureSlotCap(slot, uSize); err != nil {
		return nil, GPUProfile{}, err
	}

	stream := streams.Stream
	if stream == 0 {
		stream = slot.stream
	}

	cmdLen := int(streams.CmdLen)
	offLen := int(streams.OffLen)
	litLen := int(streams.LitLen)

	cmdSecSize := cmdLen + 5
	offSecSize := offLen + 5

	if windowLog == 0 {
		if uSize <= 64*1024 {
			windowLog = 16 // WindowLog64K
		} else if uSize > 1024*1024 {
			windowLog = 23 // WindowLog8M
		} else {
			windowLog = 20 // WindowLog1M
		}
	}

	flags := uint8(0)
	if uSize > 65535 || cmdSecSize > 65535 || offSecSize > 65535 || windowLog > 16 {
		flags |= 0x08 // FlagExtOffset (0x08)
	}

	secDirSize := 4
	if (flags & 0x08) != 0 {
		secDirSize = 6
	}
	sec1Size := 8
	sec2Offset := 8 + secDirSize + sec1Size // Cmd offset
	sec3Offset := sec2Offset + cmdSecSize   // Off offset
	sec4Offset := sec3Offset + offSecSize   // Lit offset

	if r, _, _ := cuEventRecord.Call(slot.evD2DStart, stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evD2DStart", r)
	}

	// 1. Pack commands into slot.dFrameOut + sec2Offset (GPU ModeFlat)
	dCmdDst := slot.dFrameOut + uint64(sec2Offset)
	pCmdN := uint32(cmdLen)
	pNull := uint64(0)
	pCmdArgs := []uintptr{
		uintptr(unsafe.Pointer(&streams.DFinalCmds)),
		uintptr(unsafe.Pointer(&pCmdN)),
		uintptr(unsafe.Pointer(&dCmdDst)),
		uintptr(unsafe.Pointer(&pNull)),
	}
	cuLaunchKernel.Call(
		g.fnPackStreamFlat,
		224, 1, 1, 256, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pCmdArgs[0])), 0,
	)

	// 2. Pack offsets into slot.dFrameOut + sec3Offset (GPU ModeFlat)
	dOffDst := slot.dFrameOut + uint64(sec3Offset)
	pOffN := uint32(offLen)
	pOffArgs := []uintptr{
		uintptr(unsafe.Pointer(&streams.DFinalOffs)),
		uintptr(unsafe.Pointer(&pOffN)),
		uintptr(unsafe.Pointer(&dOffDst)),
		uintptr(unsafe.Pointer(&pNull)),
	}
	cuLaunchKernel.Call(
		g.fnPackStreamFlat,
		224, 1, 1, 256, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pOffArgs[0])), 0,
	)

	// 3. Literals packing (Direct zero-stall vectorized GPU streaming in VRAM)
	dLitDst := slot.dFrameOut + uint64(sec4Offset)
	litSecSize := litLen

	if litLen > 0 {
		pCopyN := uint32(litLen)
		pCopyArgs := []uintptr{
			uintptr(unsafe.Pointer(&streams.DFinalLits)),
			uintptr(unsafe.Pointer(&dLitDst)),
			uintptr(unsafe.Pointer(&pCopyN)),
		}
		cuLaunchKernel.Call(
			g.fnDeviceCopy,
			224, 1, 1, 256, 1, 1, 0, stream,
			uintptr(unsafe.Pointer(&pCopyArgs[0])), 0,
		)
	}

	// 4. Assemble PRZM Frame Header directly in VRAM
	pUSize32 := uint32(uSize)
	pFlags8 := flags
	pWinLog8 := uint8(windowLog)
	pCmdSecLen32 := uint32(cmdSecSize)
	pOffSecLen32 := uint32(offSecSize)

	pHdrArgs := []uintptr{
		uintptr(unsafe.Pointer(&slot.dFrameOut)),
		uintptr(unsafe.Pointer(&pUSize32)),
		uintptr(unsafe.Pointer(&pFlags8)),
		uintptr(unsafe.Pointer(&pWinLog8)),
		uintptr(unsafe.Pointer(&pCmdSecLen32)),
		uintptr(unsafe.Pointer(&pOffSecLen32)),
	}
	cuLaunchKernel.Call(
		g.fnWriteFrameHeader,
		1, 1, 1, 1, 1, 1, 0, stream,
		uintptr(unsafe.Pointer(&pHdrArgs[0])), 0,
	)

	totalFrameSize := sec4Offset + litSecSize

	if r, _, _ := cuEventRecord.Call(slot.evD2DKernel, stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evD2DKernel", r)
	}

	// 5. Single Egress DMA: transfer completed frame buffer to host
	if r, _, _ := cuMemcpyDtoHAsync.Call(
		slot.hFramePtr,
		uintptr(slot.dFrameOut),
		uintptr(totalFrameSize),
		stream,
	); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuMemcpyDtoHAsync dFrameOut", r)
	}

	if r, _, _ := cuEventRecord.Call(slot.evD2DEgress, stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evD2DEgress", r)
	}

	if r, _, _ := cuStreamSynchronize.Call(stream); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuStreamSynchronize d2d egress", r)
	}

	var tKernel, tTotal float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), slot.evD2DStart, slot.evD2DKernel)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tTotal)), slot.evD2DStart, slot.evD2DEgress)

	var throughput float64
	if tKernel > 0 {
		throughput = (float64(uSize) / (float64(tKernel) / 1000.0)) / 1e9
	}

	prof := GPUProfile{
		KernelTimeMs:              float64(tKernel),
		InternalVRAMBandwidthGBps: throughput,
		EgressDMABytes:            uint64(totalFrameSize),
		TotalRoundTripMs:          float64(tTotal),
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           1,
	}

	out := make([]byte, totalFrameSize)
	copy(out, slot.hFrameSlice[:totalFrameSize])

	return out, prof, nil
}

// EncodeD2D performs seamless device-to-device entropy encoding and frame assembly
// directly in VRAM without host roundtripping.
// Takes DeviceTokenStreams (device pointers DFinalCmds, DFinalOffs, DFinalLits in VRAM),
// original uncompressed size uSize, and windowLog.
// Encodes commands, offsets, and literals directly into the device-resident PRZM frame buffer (slot.dFrameOut),
// writes the canonical PRZM frame header, and egresses only the final compressed frame to host.
func (g *GPUCompressor) EncodeD2D(streams DeviceTokenStreams, uSize, windowLog int) ([]byte, GPUProfile, error) {
	if uSize <= 0 {
		return nil, GPUProfile{}, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	slot := &g.slots[0]
	return g.encodeD2DLocked(slot, streams, uSize, windowLog)
}

// CompressD2D performs complete end-to-end GPU compression with zero intermediate host copies:
// DMA Ingress -> Radix-Strike Match Search -> Fused Warp Tokenizer -> D2D Entropy Encoding -> Frame Assembly -> Single DMA Egress.
func (g *GPUCompressor) CompressD2D(src []byte) ([]byte, GPUProfile, error) {
	if pErr := mockMatchErr.Load(); pErr != nil {
		return nil, GPUProfile{}, *pErr
	}
	n := len(src)
	if n == 0 {
		return nil, GPUProfile{}, nil
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	slot := &g.slots[0]

	devStreams, tokProf, err := g.tokenizeDeviceLocked(slot, src)
	if err != nil {
		return nil, GPUProfile{}, err
	}

	frame, d2dProf, err := g.encodeD2DLocked(slot, devStreams, n, 0)
	if err != nil {
		return nil, GPUProfile{}, err
	}

	var tIngressMatch float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tIngressMatch)), slot.evStart, slot.evTokStart)

	var tHwTotal float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tHwTotal)), slot.evStart, slot.evD2DEgress)

	totalKernelMs := float64(tIngressMatch) + tokProf.KernelTimeMs + d2dProf.KernelTimeMs
	totalMs := float64(tHwTotal)
	endToEndBW := 0.0
	if totalMs > 0 {
		endToEndBW = (float64(n) / (float64(totalMs) / 1000.0)) / 1e9
	}

	prof := GPUProfile{
		IngressDMABytes:           uint64(n),
		KernelTimeMs:              totalKernelMs,
		InternalVRAMBandwidthGBps: d2dProf.InternalVRAMBandwidthGBps,
		EgressDMABytes:            uint64(len(frame)),
		TotalRoundTripMs:          totalMs,
		EndToEndBandwidthGBps:     endToEndBW,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           1,
	}

	return frame, prof, nil
}

// BenchmarkD2DEntropy benchmarks pure Device-to-Device entropy encoding and frame assembly speed in VRAM.
func (g *GPUCompressor) BenchmarkD2DEntropy(sizeMB, iterations int) (float64, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return 0, cuCheck("cuCtxSetCurrent", r)
	}

	if sizeMB <= 0 {
		sizeMB = 16
	}
	if iterations <= 0 {
		iterations = 10
	}

	sizeBytes := sizeMB * 1024 * 1024
	slot := &g.slots[0]
	if err := g.ensureSlotCap(slot, sizeBytes); err != nil {
		return 0, err
	}
	if err := g.ensureTokenizerCap(slot, sizeBytes); err != nil {
		return 0, err
	}

	stream := slot.stream

	cmdLen := uint32(sizeBytes / 4)
	offLen := uint32(sizeBytes / 4)
	litLen := uint32(sizeBytes / 2)

	cuMemsetD8Async.Call(uintptr(slot.dFinalCmds), 0x80, uintptr(cmdLen), stream)
	cuMemsetD8Async.Call(uintptr(slot.dFinalOffs), 0x01, uintptr(offLen), stream)
	cuMemsetD8Async.Call(uintptr(slot.dFinalLits), 0x41, uintptr(litLen), stream)
	cuStreamSynchronize.Call(stream)

	devStreams := DeviceTokenStreams{
		DFinalCmds: slot.dFinalCmds,
		DFinalOffs: slot.dFinalOffs,
		DFinalLits: slot.dFinalLits,
		DTotals:    slot.dTotals,
		CmdLen:     cmdLen,
		OffLen:     offLen,
		LitLen:     litLen,
		Stream:     stream,
	}

	sec2Offset := uint64(22)
	sec3Offset := sec2Offset + uint64(cmdLen+5)
	sec4Offset := sec3Offset + uint64(offLen+5)
	pCmdN := uint32(cmdLen)
	pOffN := uint32(offLen)
	pNull := uint64(0)
	dCmdDst := slot.dFrameOut + sec2Offset
	dOffDst := slot.dFrameOut + sec3Offset
	dLitDst := slot.dFrameOut + sec4Offset

	pCmdArgs := []uintptr{
		uintptr(unsafe.Pointer(&devStreams.DFinalCmds)),
		uintptr(unsafe.Pointer(&pCmdN)),
		uintptr(unsafe.Pointer(&dCmdDst)),
		uintptr(unsafe.Pointer(&pNull)),
	}
	pOffArgs := []uintptr{
		uintptr(unsafe.Pointer(&devStreams.DFinalOffs)),
		uintptr(unsafe.Pointer(&pOffN)),
		uintptr(unsafe.Pointer(&dOffDst)),
		uintptr(unsafe.Pointer(&pNull)),
	}
	pCopyN := uint32(litLen)
	pCopyArgs := []uintptr{
		uintptr(unsafe.Pointer(&devStreams.DFinalLits)),
		uintptr(unsafe.Pointer(&dLitDst)),
		uintptr(unsafe.Pointer(&pCopyN)),
	}

	// Warmup
	cuLaunchKernel.Call(g.fnPackStreamFlat, 224, 1, 1, 256, 1, 1, 0, stream, uintptr(unsafe.Pointer(&pCmdArgs[0])), 0)
	cuLaunchKernel.Call(g.fnPackStreamFlat, 224, 1, 1, 256, 1, 1, 0, stream, uintptr(unsafe.Pointer(&pOffArgs[0])), 0)
	cuLaunchKernel.Call(g.fnDeviceCopy, 224, 1, 1, 256, 1, 1, 0, stream, uintptr(unsafe.Pointer(&pCopyArgs[0])), 0)
	cuStreamSynchronize.Call(stream)

	cuEventRecord.Call(slot.evD2DStart, stream)
	for i := 0; i < iterations; i++ {
		cuLaunchKernel.Call(
			g.fnPackStreamFlat,
			224,
			1,
			1,
			256,
			1,
			1,
			0,
			stream,
			uintptr(unsafe.Pointer(&pCmdArgs[0])),
			0,
		)
		cuLaunchKernel.Call(
			g.fnPackStreamFlat,
			224,
			1,
			1,
			256,
			1,
			1,
			0,
			stream,
			uintptr(unsafe.Pointer(&pOffArgs[0])),
			0,
		)
		cuLaunchKernel.Call(g.fnDeviceCopy, 224, 1, 1, 256, 1, 1, 0, stream, uintptr(unsafe.Pointer(&pCopyArgs[0])), 0)
	}
	cuEventRecord.Call(slot.evD2DKernel, stream)
	cuStreamSynchronize.Call(stream)

	var kernelMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&kernelMs)), slot.evD2DStart, slot.evD2DKernel)

	totalProcessedBytes := uint64(sizeBytes) * uint64(iterations)
	bwGBps := (float64(totalProcessedBytes) / (float64(kernelMs) / 1000.0)) / 1e9

	return bwGBps, nil
}

// BenchmarkVRAMBandwidth measures pure internal VRAM read/write streaming bandwidth.
func (g *GPUCompressor) BenchmarkVRAMBandwidth(sizeMB int) (float64, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return 0, cuCheck("cuCtxSetCurrent", r)
	}

	bytesTotal := uint64(sizeMB) * 1024 * 1024
	numElem := bytesTotal / 16 // 16 bytes per uint4
	var dIn, dOut uint64
	if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&dIn)), uintptr(bytesTotal)); r != 0 {
		return 0, cuCheck("cuMemAlloc dIn", r)
	}
	defer cuMemFree.Call(uintptr(dIn))

	if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&dOut)), uintptr(bytesTotal)); r != 0 {
		return 0, cuCheck("cuMemAlloc dOut", r)
	}
	defer cuMemFree.Call(uintptr(dOut))

	var evStart, evStop uintptr
	if r, _, _ := cuEventCreate.Call(uintptr(unsafe.Pointer(&evStart)), 0); r != 0 {
		return 0, cuCheck("cuEventCreate evStart", r)
	}
	defer cuEventDestroy.Call(evStart)

	if r, _, _ := cuEventCreate.Call(uintptr(unsafe.Pointer(&evStop)), 0); r != 0 {
		return 0, cuCheck("cuEventCreate evStop", r)
	}
	defer cuEventDestroy.Call(evStop)

	iters := uint32(20)
	params := []uintptr{
		uintptr(unsafe.Pointer(&dIn)), uintptr(unsafe.Pointer(&dOut)),
		uintptr(unsafe.Pointer(&numElem)), uintptr(unsafe.Pointer(&iters)),
	}

	// Warmup
	if r, _, _ := cuLaunchKernel.Call(
		g.fnVRAMBandwidth,
		224,
		1,
		1,
		256,
		1,
		1,
		0,
		0,
		uintptr(unsafe.Pointer(&params[0])),
		0,
	); r != 0 {
		return 0, cuCheck("cuLaunchKernel warmup", r)
	}

	if r, _, _ := cuEventRecord.Call(evStart, 0); r != 0 {
		return 0, cuCheck("cuEventRecord evStart", r)
	}
	if r, _, _ := cuLaunchKernel.Call(
		g.fnVRAMBandwidth,
		224,
		1,
		1,
		256,
		1,
		1,
		0,
		0,
		uintptr(unsafe.Pointer(&params[0])),
		0,
	); r != 0 {
		return 0, cuCheck("cuLaunchKernel benchmark", r)
	}
	if r, _, _ := cuEventRecord.Call(evStop, 0); r != 0 {
		return 0, cuCheck("cuEventRecord evStop", r)
	}
	if r, _, _ := cuEventSynchronize.Call(evStop); r != 0 {
		return 0, cuCheck("cuEventSynchronize evStop", r)
	}

	runtime.KeepAlive(&dIn)
	runtime.KeepAlive(&dOut)
	runtime.KeepAlive(&numElem)
	runtime.KeepAlive(&iters)

	var tMs float32
	if r, _, _ := cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tMs)), evStart, evStop); r != 0 {
		return 0, cuCheck("cuEventElapsedTime", r)
	}
	if tMs <= 0 {
		return 0, fmt.Errorf("invalid elapsed time: %v ms", tMs)
	}

	// 1 uint4 read (16B) + 1 uint4 write (16B) = 32B per element per iteration
	rwBytes := float64(bytesTotal*2) * float64(iters)
	bwGBps := (rwBytes / (float64(tMs) / 1000.0)) / 1e9
	return bwGBps, nil
}

// NormalizeFreq quantizes byte histogram counts into normalized ANS frequencies summing strictly to targetM (typically 4096).
func NormalizeFreq(counts [256]uint32, targetM uint32) [256]uint16 {
	var norm [256]uint16
	total := uint64(0)
	active := 0
	for s := range 256 {
		total += uint64(counts[s])
		if counts[s] > 0 {
			active++
		}
	}
	if total == 0 || active == 0 {
		return norm
	}
	if uint32(active) >= targetM {
		for s := range 256 {
			norm[s] = uint16(targetM / 256)
		}
		return norm
	}

	remTarget := targetM - uint32(active)
	type remainder struct {
		sym int
		rem uint64
	}
	rems := make([]remainder, 0, active)

	allocated := uint32(0)
	for s := range 256 {
		c := counts[s]
		if c > 0 {
			prod := uint64(c) * uint64(remTarget)
			add := uint32(prod / total)
			norm[s] = 1 + uint16(add)
			allocated += 1 + add
			rems = append(rems, remainder{
				sym: s,
				rem: prod % total,
			})
		}
	}

	if allocated < targetM {
		diff := int(targetM - allocated)
		sort.Slice(rems, func(i, j int) bool {
			return rems[i].rem > rems[j].rem
		})
		for i := 0; i < diff && i < len(rems); i++ {
			norm[rems[i].sym]++
		}
	}
	return norm
}

// BuildCDF constructs cumulative frequency distribution from normalized frequencies.
func BuildCDF(freq [256]uint16) [257]uint16 {
	var cdf [257]uint16
	c := uint32(0)
	for s := range 256 {
		cdf[s] = uint16(c)
		c += uint32(freq[s])
	}
	cdf[256] = uint16(c)
	return cdf
}

// SerializeMatrix serializes the 16x16 frequency matrix into PRZM standard compact header format.
func SerializeMatrix(freq [256]uint16) []byte {
	var buf []byte
	var rowMask uint16
	for h := range 16 {
		rowActive := false
		for l := range 16 {
			if freq[(h<<4)|l] > 0 {
				rowActive = true
				break
			}
		}
		if rowActive {
			rowMask |= 1 << h
		}
	}
	buf = binary.LittleEndian.AppendUint16(buf, rowMask)

	for h := range 16 {
		if (rowMask & (1 << h)) == 0 {
			continue
		}
		var colMask uint16
		for l := range 16 {
			if freq[(h<<4)|l] > 0 {
				colMask |= 1 << l
			}
		}
		buf = binary.LittleEndian.AppendUint16(buf, colMask)

		for l := range 16 {
			if (colMask & (1 << l)) == 0 {
				continue
			}
			w := freq[(h<<4)|l]
			if w < 128 {
				buf = append(buf, byte(w))
			} else {
				buf = append(buf, byte(0x80|(w>>8)), byte(w&0xFF))
			}
		}
	}
	return buf
}

// ComputeHistogramGPU runs the parallel byte frequency histogram CUDA kernel on device buffer dIn.
// Returns 256 frequency counts, kernel execution time in ms, and internal VRAM bandwidth in GB/s.
func (g *GPUCompressor) ComputeHistogramGPU(dIn uint64, n int, stream uintptr) ([256]uint32, float64, float64, error) {
	if n <= 0 {
		return [256]uint32{}, 0, 0, nil
	}
	slot := &g.slots[0]
	if stream == 0 {
		stream = slot.stream
	}

	// 1. Clear histogram
	pClear := []uintptr{
		uintptr(unsafe.Pointer(&slot.dHist)),
		uintptr(unsafe.Pointer(&slot.dMeta)),
	}
	if r, _, _ := cuLaunchKernel.Call(
		g.fnClearHistogram,
		1, 1, 1,
		256, 1, 1,
		0, stream,
		uintptr(unsafe.Pointer(&pClear[0])), 0,
	); r != 0 {
		return [256]uint32{}, 0, 0, cuCheck("cuLaunchKernel fnClearHistogram", r)
	}

	// 2. Launch parallel byte histogram kernel
	numBlocks := 224
	pN := uint32(n)
	pHist := []uintptr{
		uintptr(unsafe.Pointer(&dIn)),
		uintptr(unsafe.Pointer(&pN)),
		uintptr(unsafe.Pointer(&slot.dHist)),
		uintptr(unsafe.Pointer(&slot.dMeta)),
	}

	cuEventRecord.Call(slot.evKernel, stream)

	if r, _, _ := cuLaunchKernel.Call(
		g.fnByteHistogram,
		uintptr(numBlocks), 1, 1,
		256, 1, 1,
		0, stream,
		uintptr(unsafe.Pointer(&pHist[0])), 0,
	); r != 0 {
		return [256]uint32{}, 0, 0, cuCheck("cuLaunchKernel fnByteHistogram", r)
	}

	cuEventRecord.Call(slot.evEgressStart, stream)

	// 3. Egress DMA 256 bins (1024 bytes) to pinned host memory
	if r, _, _ := cuMemcpyDtoHAsync.Call(slot.hHistPtr, uintptr(slot.dHist), 256*4, stream); r != 0 {
		return [256]uint32{}, 0, 0, cuCheck("cuMemcpyDtoHAsync dHist", r)
	}

	cuEventRecord.Call(slot.evEgress, stream)
	cuStreamSynchronize.Call(stream)

	var kernelMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&kernelMs)), slot.evKernel, slot.evEgressStart)

	var counts [256]uint32
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&counts[0])), 256*4), slot.hHistSlice[:256*4])

	bwGBps := 0.0
	if kernelMs > 0 {
		bwGBps = (float64(n) / (float64(kernelMs) / 1000.0)) / 1e9
	}

	return counts, float64(kernelMs), bwGBps, nil
}

// ComputeHistogramHost copies src to GPU, executes parallel histogram kernel, and returns counts and profile.
func (g *GPUCompressor) ComputeHistogramHost(src []byte) ([256]uint32, GPUProfile, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return [256]uint32{}, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	n := len(src)
	if n == 0 {
		return [256]uint32{}, GPUProfile{}, nil
	}

	slot := &g.slots[0]
	if err := g.ensureSlotCap(slot, n); err != nil {
		return [256]uint32{}, GPUProfile{}, err
	}

	stream := slot.stream
	cuEventRecord.Call(slot.evStart, stream)

	// Ingress DMA
	copy(slot.hInBuf[:n], src)
	cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), slot.hInPtr, uintptr(n), stream)
	cuEventRecord.Call(slot.evIngress, stream)

	counts, kernelMs, bwGBps, err := g.ComputeHistogramGPU(slot.dIn, n, stream)
	if err != nil {
		return [256]uint32{}, GPUProfile{}, err
	}

	var ingressMs, egressMs, totalMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&ingressMs)), slot.evStart, slot.evIngress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&egressMs)), slot.evEgressStart, slot.evEgress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&totalMs)), slot.evStart, slot.evEgress)

	var ingressBW, egressBW, endToEndBW float64
	if ingressMs > 0 {
		ingressBW = (float64(n) / (float64(ingressMs) / 1000.0)) / 1e9
	}
	if egressMs > 0 {
		egressBW = (1024.0 / (float64(egressMs) / 1000.0)) / 1e9
	}
	if totalMs > 0 {
		endToEndBW = (float64(n) / (float64(totalMs) / 1000.0)) / 1e9
	}

	prof := GPUProfile{
		IngressDMABytes:           uint64(n),
		IngressDMATimeMs:          float64(ingressMs),
		IngressDMABandwidthGBps:   ingressBW,
		KernelTimeMs:              kernelMs,
		InternalVRAMBandwidthGBps: bwGBps,
		EgressDMABytes:            1024,
		EgressDMATimeMs:           float64(egressMs),
		EgressDMABandwidthGBps:    egressBW,
		TotalRoundTripMs:          float64(totalMs),
		EndToEndBandwidthGBps:     endToEndBW,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           1,
	}

	return counts, prof, nil
}

// PackStreamFlatGPU packs dIn into dOut with ModeFlat header directly in VRAM.
// Returns output size (n + 5), kernel execution time in ms, and internal VRAM bandwidth in GB/s.
func (g *GPUCompressor) PackStreamFlatGPU(dIn, dOut uint64, n int, stream uintptr) (int, float64, float64, error) {
	if n <= 0 {
		return 0, 0, 0, nil
	}
	slot := &g.slots[0]
	if stream == 0 {
		stream = slot.stream
	}

	pN := uint32(n)
	pArgs := []uintptr{
		uintptr(unsafe.Pointer(&dIn)),
		uintptr(unsafe.Pointer(&pN)),
		uintptr(unsafe.Pointer(&dOut)),
		uintptr(unsafe.Pointer(&slot.dOutLen)),
	}

	cuEventRecord.Call(slot.evKernel, stream)

	numBlocks := 224
	if r, _, _ := cuLaunchKernel.Call(
		g.fnPackStreamFlat,
		uintptr(numBlocks), 1, 1,
		256, 1, 1,
		0, stream,
		uintptr(unsafe.Pointer(&pArgs[0])), 0,
	); r != 0 {
		return 0, 0, 0, cuCheck("cuLaunchKernel fnPackStreamFlat", r)
	}

	cuEventRecord.Call(slot.evEgressStart, stream)
	cuStreamSynchronize.Call(stream)

	var kernelMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&kernelMs)), slot.evKernel, slot.evEgressStart)

	bwGBps := 0.0
	if kernelMs > 0 {
		// Effective read + write transfer
		bwGBps = (float64(n*2) / (float64(kernelMs) / 1000.0)) / 1e9
	}

	return n + 5, float64(kernelMs), bwGBps, nil
}

// PackStreamFlatHost packs src using GPU PackStream ModeFlat and returns packed bytes and profile.
func (g *GPUCompressor) PackStreamFlatHost(src []byte) ([]byte, GPUProfile, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	n := len(src)
	if n == 0 {
		return []byte{0, 0, 0, 0, 0}, GPUProfile{}, nil
	}

	slot := &g.slots[0]
	reqCap := n + 64
	if err := g.ensureSlotCap(slot, reqCap); err != nil {
		return nil, GPUProfile{}, err
	}

	stream := slot.stream
	cuEventRecord.Call(slot.evStart, stream)

	// Ingress DMA
	copy(slot.hInBuf[:n], src)
	cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), slot.hInPtr, uintptr(n), stream)
	cuEventRecord.Call(slot.evIngress, stream)

	// Pack in VRAM: write to dMatch as output buffer
	outLen, kernelMs, bwGBps, err := g.PackStreamFlatGPU(slot.dIn, slot.dMatch, n, stream)
	if err != nil {
		return nil, GPUProfile{}, err
	}

	// Egress DMA
	cuMemcpyDtoHAsync.Call(slot.hPosPtr, uintptr(slot.dMatch), uintptr(outLen), stream)
	cuEventRecord.Call(slot.evEgress, stream)
	cuStreamSynchronize.Call(stream)

	var ingressMs, egressMs, totalMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&ingressMs)), slot.evStart, slot.evIngress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&egressMs)), slot.evEgressStart, slot.evEgress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&totalMs)), slot.evStart, slot.evEgress)

	var ingressBW, egressBW, endToEndBW float64
	if ingressMs > 0 {
		ingressBW = (float64(n) / (float64(ingressMs) / 1000.0)) / 1e9
	}
	if egressMs > 0 {
		egressBW = (float64(outLen) / (float64(egressMs) / 1000.0)) / 1e9
	}
	if totalMs > 0 {
		endToEndBW = (float64(n) / (float64(totalMs) / 1000.0)) / 1e9
	}

	out := make([]byte, outLen)
	copy(out, slot.hPosBuf[:outLen])

	prof := GPUProfile{
		IngressDMABytes:           uint64(n),
		IngressDMATimeMs:          float64(ingressMs),
		IngressDMABandwidthGBps:   ingressBW,
		KernelTimeMs:              kernelMs,
		InternalVRAMBandwidthGBps: bwGBps,
		EgressDMABytes:            uint64(outLen),
		EgressDMATimeMs:           float64(egressMs),
		EgressDMABandwidthGBps:    egressBW,
		TotalRoundTripMs:          float64(totalMs),
		EndToEndBandwidthGBps:     endToEndBW,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           1,
	}

	return out, prof, nil
}

// VANSEncode4xGPU executes GPU V-ANS 4x encoding on device buffer dIn.
// Returns output compressed size, kernel time in ms, and internal throughput in GB/s.
func (g *GPUCompressor) VANSEncode4xGPU(
	dIn, dOut uint64,
	n int,
	freq [256]uint16,
	cdf [257]uint16,
	matBytes []byte,
	stream uintptr,
) (int, float64, float64, error) {
	if n <= 0 {
		return 0, 0, 0, nil
	}
	slot := &g.slots[0]
	if stream == 0 {
		stream = slot.stream
	}

	if n > slot.dAllocCap {
		if err := g.ensureSlotCap(slot, n+4096); err != nil {
			return 0, 0, 0, err
		}
	}

	// Upload tables and serialized matrix to device
	cuMemcpyHtoDAsync.Call(uintptr(slot.dFreq), uintptr(unsafe.Pointer(&freq[0])), 256*2, stream)
	cuMemcpyHtoDAsync.Call(uintptr(slot.dCDF), uintptr(unsafe.Pointer(&cdf[0])), 257*2, stream)
	matLen := uint32(len(matBytes))
	if matLen > 0 {
		cuMemcpyHtoDAsync.Call(uintptr(slot.dMat), uintptr(unsafe.Pointer(&matBytes[0])), uintptr(matLen), stream)
	}

	pN := uint32(n)
	pArgs := []uintptr{
		uintptr(unsafe.Pointer(&dIn)),
		uintptr(unsafe.Pointer(&pN)),
		uintptr(unsafe.Pointer(&slot.dFreq)),
		uintptr(unsafe.Pointer(&slot.dCDF)),
		uintptr(unsafe.Pointer(&slot.dMat)),
		uintptr(unsafe.Pointer(&matLen)),
		uintptr(unsafe.Pointer(&dOut)),
		uintptr(unsafe.Pointer(&slot.dOutLen)),
		uintptr(unsafe.Pointer(&slot.dTempBitstream)),
	}

	cuEventRecord.Call(slot.evKernel, stream)

	if r, _, _ := cuLaunchKernel.Call(
		g.fnVANSEncode4x,
		1, 1, 1,
		256, 1, 1,
		0, stream,
		uintptr(unsafe.Pointer(&pArgs[0])), 0,
	); r != 0 {
		return 0, 0, 0, cuCheck("cuLaunchKernel fnVANSEncode4x", r)
	}

	cuEventRecord.Call(slot.evEgressStart, stream)
	cuMemcpyDtoHAsync.Call(slot.hOutLenPtr, uintptr(slot.dOutLen), 4, stream)
	cuStreamSynchronize.Call(stream)

	var kernelMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&kernelMs)), slot.evKernel, slot.evEgressStart)

	outLen := int(binary.LittleEndian.Uint32(slot.hOutLenSlice[:4]))
	bwGBps := 0.0
	if kernelMs > 0 {
		bwGBps = (float64(n) / (float64(kernelMs) / 1000.0)) / 1e9
	}

	return outLen, float64(kernelMs), bwGBps, nil
}

// VANSEncode4xHost encodes raw bytes using GPU-accelerated V-ANS 4x with 100% bit-exact parity.
func (g *GPUCompressor) VANSEncode4xHost(src []byte) ([]byte, GPUProfile, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuCtxSetCurrent", r)
	}

	n := len(src)
	if n == 0 {
		return []byte{}, GPUProfile{}, nil
	}

	slot := &g.slots[0]
	reqCap := n + 4096
	if err := g.ensureSlotCap(slot, reqCap); err != nil {
		return nil, GPUProfile{}, err
	}

	stream := slot.stream
	cuEventRecord.Call(slot.evStart, stream)

	// Ingress DMA
	copy(slot.hInBuf[:n], src)
	cuMemcpyHtoDAsync.Call(uintptr(slot.dIn), slot.hInPtr, uintptr(n), stream)
	cuEventRecord.Call(slot.evIngress, stream)

	// 1. Compute histogram on GPU
	counts, _, _, err := g.ComputeHistogramGPU(slot.dIn, n, stream)
	if err != nil {
		return nil, GPUProfile{}, err
	}

	// 2. Normalize and build CDF & matrix
	freq := NormalizeFreq(counts, 4096)
	cdf := BuildCDF(freq)
	matBytes := SerializeMatrix(freq)

	// 3. Encode in VRAM using GPU VANS kernel (output to dMatch)
	outLen, kernelMs, bwGBps, err := g.VANSEncode4xGPU(slot.dIn, slot.dMatch, n, freq, cdf, matBytes, stream)
	if err != nil {
		return nil, GPUProfile{}, err
	}

	// 4. Egress DMA
	cuMemcpyDtoHAsync.Call(slot.hPosPtr, uintptr(slot.dMatch), uintptr(outLen), stream)
	cuEventRecord.Call(slot.evEgress, stream)
	cuStreamSynchronize.Call(stream)

	var ingressMs, egressMs, totalMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&ingressMs)), slot.evStart, slot.evIngress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&egressMs)), slot.evEgressStart, slot.evEgress)
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&totalMs)), slot.evStart, slot.evEgress)

	var ingressBW, egressBW, endToEndBW float64
	if ingressMs > 0 {
		ingressBW = (float64(n) / (float64(ingressMs) / 1000.0)) / 1e9
	}
	if egressMs > 0 {
		egressBW = (float64(outLen) / (float64(egressMs) / 1000.0)) / 1e9
	}
	if totalMs > 0 {
		endToEndBW = (float64(n) / (float64(totalMs) / 1000.0)) / 1e9
	}

	out := make([]byte, outLen)
	copy(out, slot.hPosBuf[:outLen])

	prof := GPUProfile{
		IngressDMABytes:           uint64(n),
		IngressDMATimeMs:          float64(ingressMs),
		IngressDMABandwidthGBps:   ingressBW,
		KernelTimeMs:              kernelMs,
		InternalVRAMBandwidthGBps: bwGBps,
		EgressDMABytes:            uint64(outLen),
		EgressDMATimeMs:           float64(egressMs),
		EgressDMABandwidthGBps:    egressBW,
		TotalRoundTripMs:          float64(totalMs),
		EndToEndBandwidthGBps:     endToEndBW,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
		PipelinedChunks:           1,
	}

	return out, prof, nil
}

// BenchmarkEntropyVRAM evaluates peak internal VRAM bandwidth + ANS state transition throughput.
func (g *GPUCompressor) BenchmarkEntropyVRAM(sizeMB, iterations int) (float64, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	g.mu.Lock()
	defer g.mu.Unlock()

	if r, _, _ := cuCtxSetCurrent.Call(g.ctx); r != 0 {
		return 0, cuCheck("cuCtxSetCurrent", r)
	}

	if sizeMB <= 0 {
		sizeMB = 16
	}
	if iterations <= 0 {
		iterations = 10
	}

	sizeBytes := sizeMB * 1024 * 1024
	slot := &g.slots[0]
	if err := g.ensureSlotCap(slot, sizeBytes); err != nil {
		return 0, err
	}

	stream := slot.stream
	pN := uint32(sizeBytes)
	pIters := uint32(iterations)
	pArgs := []uintptr{
		uintptr(unsafe.Pointer(&slot.dIn)),
		uintptr(unsafe.Pointer(&pN)),
		uintptr(unsafe.Pointer(&slot.dMatch)),
		uintptr(unsafe.Pointer(&pIters)),
	}

	numBlocks := 224
	// Warmup
	cuLaunchKernel.Call(
		g.fnEntropyBandwidth,
		uintptr(numBlocks), 1, 1,
		256, 1, 1,
		0, stream,
		uintptr(unsafe.Pointer(&pArgs[0])), 0,
	)
	cuStreamSynchronize.Call(stream)

	cuEventRecord.Call(slot.evKernel, stream)

	if r, _, _ := cuLaunchKernel.Call(
		g.fnEntropyBandwidth,
		uintptr(numBlocks), 1, 1,
		256, 1, 1,
		0, stream,
		uintptr(unsafe.Pointer(&pArgs[0])), 0,
	); r != 0 {
		return 0, cuCheck("cuLaunchKernel fnEntropyBandwidth", r)
	}

	cuEventRecord.Call(slot.evEgressStart, stream)
	cuStreamSynchronize.Call(stream)

	var kernelMs float32
	cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&kernelMs)), slot.evKernel, slot.evEgressStart)

	totalBytes := uint64(sizeBytes) * 2 * uint64(iterations) // Read + write
	bwGBps := (float64(totalBytes) / (float64(kernelMs) / 1000.0)) / 1e9

	return bwGBps, nil
}
