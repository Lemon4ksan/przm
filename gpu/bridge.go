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
	evStart          uintptr
	evIngress        uintptr
	evKernel         uintptr
	evEgressStart    uintptr
	evEgress         uintptr
}

// GPUCompressor coordinates CUDA Driver execution on NVIDIA hardware.
type GPUCompressor struct {
	mu                sync.Mutex
	dev               int32
	ctx               uintptr
	module            uintptr
	fnInitHead        uintptr
	fnBuildChains     uintptr
	fnSearchMatches   uintptr
	fnRadixStrike     uintptr
	fnClearRadixTable uintptr
	fnCompactCount    uintptr
	fnCompactScan     uintptr
	fnCompactScatter  uintptr
	fnVRAMBandwidth   uintptr
	slots             [2]pipelineSlot
	dRadixTable       uint64
	dIn               uint64
	dMatch            uint64
	dBlockCounts      uint64
	dBlockOffsets     uint64
	dTotalCount       uint64
	dCompactPos       uint64
	dCompactDesc      uint64
	dAllocCap         int
	hTotalCountSlice  []byte
	hTotalCountPtr    uintptr
	streams           []uintptr
	evStart           uintptr
	evIngress         uintptr
	evKernel          uintptr
	evEgressStart     uintptr
	evEgress          uintptr
	pools             map[int]*PinnedBufferPool
	ComputeMajor      int
	ComputeMinor      int
	DeviceName        string
	SMCount           int
	TotalVRAMBytes    uint64
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

	// Preallocate dual-buffer pipeline slots (8 MB Radix Table per slot, compaction metadata, events)
	radixBytes := uintptr(2097152 * 4)
	for s := range 2 {
		slot := &g.slots[s]
		cuStreamCreate.Call(uintptr(unsafe.Pointer(&slot.stream)), 1)
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dRadixTable)), radixBytes)
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dBlockCounts)), uintptr(224*4))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dBlockOffsets)), uintptr(224*4))
		cuMemAlloc.Call(uintptr(unsafe.Pointer(&slot.dTotalCount)), uintptr(4))
		slot.hTotalCountSlice, slot.hTotalCountPtr, _ = AllocPinnedHost(4)

		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evStart)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evIngress)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evKernel)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evEgressStart)), 0)
		cuEventCreate.Call(uintptr(unsafe.Pointer(&slot.evEgress)), 0)
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
		"kernel/przm_lz77.ptx",
		`d:\CodingProjects\przm\gpu\kernel\przm_lz77.ptx`,
		"gpu/kernel/przm_lz77.ptx",
		"../gpu/kernel/przm_lz77.ptx",
		"../../gpu/kernel/przm_lz77.ptx",
		"../kernel/przm_lz77.ptx",
		"przm_lz77.ptx",
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
	if n <= slot.dAllocCap && n <= slot.hInCap && n <= slot.hEgressCap {
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

	return nil
}

func (g *GPUCompressor) launchSlotKernel(slot *pipelineSlot, n int) error {
	stream := slot.stream
	if n > 0 && g.fnRadixStrike != 0 && slot.dRadixTable != 0 {
		tableV4 := uint32(524288) // 2097152 words / 4 (uint4 vectorized)
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

	if r, _, _ := cuEventRecord.Call(slot.evKernel, stream); r != 0 {
		return cuCheck("cuEventRecord evKernel", r)
	}

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

	totalMatches := binary.LittleEndian.Uint32(slot.hTotalCountSlice)
	if totalMatches > uint32(n) {
		totalMatches = uint32(n)
	}

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
	for k := 0; k < numChunks; k++ {
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

		totalMatches := binary.LittleEndian.Uint32(slot.hTotalCountSlice)
		if totalMatches > uint32(chunkLen) {
			totalMatches = uint32(chunkLen)
		}

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
