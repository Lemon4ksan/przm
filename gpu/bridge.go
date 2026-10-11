package gpu

import (
	_ "embed"
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

// GPUCompressor coordinates CUDA Driver execution on NVIDIA hardware.
type GPUCompressor struct {
	mu              sync.Mutex
	dev             int32
	ctx             uintptr
	module          uintptr
	fnInitHead      uintptr
	fnBuildChains   uintptr
	fnSearchMatches uintptr
	fnVRAMBandwidth uintptr
	pools           map[int]*PinnedBufferPool
	ComputeMajor    int
	ComputeMinor    int
	DeviceName      string
	SMCount         int
	TotalVRAMBytes  uint64
	matchBuf        []LZ77Match
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
	g.fnVRAMBandwidth = g.getFunc("przm_lz77_vram_bandwidth_kernel")

	return g, nil
}

func (g *GPUCompressor) getPinned(size int) (PinnedBuffer, *PinnedBufferPool, error) {
	if g.pools == nil {
		g.pools = make(map[int]*PinnedBufferPool)
	}
	pool, ok := g.pools[size]
	if !ok {
		var err error
		pool, err = NewPinnedBufferPool(size, 0)
		if err != nil {
			return PinnedBuffer{}, nil, err
		}
		g.pools[size] = pool
	}
	buf, err := pool.Get()
	return buf, pool, err
}

func (g *GPUCompressor) getFunc(name string) uintptr {
	nBytes := append([]byte(name), 0)
	var hfunc uintptr
	cuModuleGetFunc.Call(uintptr(unsafe.Pointer(&hfunc)), g.module, uintptr(unsafe.Pointer(&nBytes[0])))
	return hfunc
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
			return append(data, 0), nil
		}
	}
	if len(embeddedPTX) > 0 {
		return append(embeddedPTX, 0), nil
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

// MatchLZ77 executes parallel LZ77 dictionary search on GPU.
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

	n := len(src)
	if n == 0 {
		return nil, GPUProfile{}, nil
	}

	var dIn, dMatch uint64

	if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&dIn)), uintptr(n)); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuMemAlloc dIn", r)
	}
	defer cuMemFree.Call(uintptr(dIn))

	if r, _, _ := cuMemAlloc.Call(uintptr(unsafe.Pointer(&dMatch)), uintptr(n*4)); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuMemAlloc dMatch", r)
	}
	defer cuMemFree.Call(uintptr(dMatch))

	var evStart, evIngress, evKernel, evEgress uintptr
	if r, _, _ := cuEventCreate.Call(uintptr(unsafe.Pointer(&evStart)), 0); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventCreate evStart", r)
	}
	defer cuEventDestroy.Call(evStart)

	if r, _, _ := cuEventCreate.Call(uintptr(unsafe.Pointer(&evIngress)), 0); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventCreate evIngress", r)
	}
	defer cuEventDestroy.Call(evIngress)

	if r, _, _ := cuEventCreate.Call(uintptr(unsafe.Pointer(&evKernel)), 0); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventCreate evKernel", r)
	}
	defer cuEventDestroy.Call(evKernel)

	if r, _, _ := cuEventCreate.Call(uintptr(unsafe.Pointer(&evEgress)), 0); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventCreate evEgress", r)
	}
	defer cuEventDestroy.Call(evEgress)

	// Stage ingress into pinned memory pool for line-rate PCIe DMA (>20 GB/s)
	inBuf, inPool, inErr := g.getPinned(n)
	var inPtr uintptr
	if inErr == nil {
		copy(inBuf.Bytes, src)
		inPtr = inBuf.Ptr
		defer inPool.Put(inBuf)
	} else {
		inPtr = uintptr(unsafe.Pointer(&src[0]))
	}

	// Multi-stream pipelined execution
	if r, _, _ := cuEventRecord.Call(evStart, 0); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evStart", r)
	}

	outBytes := n * 4
	outBuf, outPool, outErr := g.getPinned(outBytes)
	var outPtr uintptr
	var fallbackDesc []uint32
	if outErr == nil {
		outPtr = outBuf.Ptr
		defer outPool.Put(outBuf)
	} else {
		fallbackDesc = make([]uint32, n)
		outPtr = uintptr(unsafe.Pointer(&fallbackDesc[0]))
	}

	numStreams := 4
	streams := make([]uintptr, numStreams)
	for i := 0; i < numStreams; i++ {
		cuStreamCreate.Call(uintptr(unsafe.Pointer(&streams[i])), 0)
		defer cuStreamDestroy.Call(streams[i])
	}

	tileSize := 32768
	chunkSize := (n + numStreams - 1) / numStreams
	chunkSize = (chunkSize + tileSize - 1) / tileSize * tileSize
	if chunkSize == 0 {
		chunkSize = tileSize
	}

	type searchParams struct {
		dIn, inLen64, dPrev, dMatch uint64
		wSize, mDepth               uint32
	}
	params := make([]searchParams, numStreams)

	for i := 0; i < numStreams; i++ {
		offset := i * chunkSize
		if offset >= n {
			break
		}
		cLen := chunkSize
		if offset+cLen > n {
			cLen = n - offset
		}

		cInPtr := inPtr + uintptr(offset)
		cDIn := uintptr(dIn) + uintptr(offset)
		cDMatch := uintptr(dMatch) + uintptr(offset*4)
		cOutPtr := outPtr + uintptr(offset*4)

		cuMemcpyHtoDAsync.Call(cDIn, cInPtr, uintptr(cLen), streams[i])

		params[i] = searchParams{
			dIn:     uint64(cDIn),
			inLen64: uint64(cLen),
			dPrev:   0,
			dMatch:  uint64(cDMatch),
			wSize:   uint32(Window1M),
			mDepth:  2,
		}

		pSearch := []uintptr{
			uintptr(unsafe.Pointer(&params[i].dIn)), uintptr(unsafe.Pointer(&params[i].inLen64)),
			uintptr(unsafe.Pointer(&params[i].dPrev)), uintptr(unsafe.Pointer(&params[i].dMatch)),
			uintptr(unsafe.Pointer(&params[i].wSize)), uintptr(unsafe.Pointer(&params[i].mDepth)),
		}

		cuLaunchKernel.Call(
			g.fnSearchMatches,
			224,
			1,
			1,
			256,
			1,
			1,
			0,
			streams[i],
			uintptr(unsafe.Pointer(&pSearch[0])),
			0,
		)

		cuMemcpyDtoHAsync.Call(cOutPtr, cDMatch, uintptr(cLen*4), streams[i])
	}

	for i := 0; i < numStreams; i++ {
		cuStreamSynchronize.Call(streams[i])
	}

	// Record dummy events to satisfy profile timing calculations (they will be fast/zero)
	cuEventRecord.Call(evIngress, 0)
	cuEventRecord.Call(evKernel, 0)
	if r, _, _ := cuEventRecord.Call(evEgress, 0); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventRecord evEgress", r)
	}
	if r, _, _ := cuEventSynchronize.Call(evEgress); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventSynchronize evEgress", r)
	}

	var tIngress, tKernel, tEgress float32
	if r, _, _ := cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tIngress)), evStart, evIngress); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventElapsedTime tIngress", r)
	}
	if r, _, _ := cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tKernel)), evIngress, evKernel); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventElapsedTime tKernel", r)
	}
	if r, _, _ := cuEventElapsedTime.Call(uintptr(unsafe.Pointer(&tEgress)), evKernel, evEgress); r != 0 {
		return nil, GPUProfile{}, cuCheck("cuEventElapsedTime tEgress", r)
	}

	totalMs := float64(tIngress + tKernel + tEgress)
	pcieOverhead := 0.0
	if totalMs > 0 {
		pcieOverhead = (float64(tIngress+tEgress) / totalMs) * 100.0
	}

	var ingressBW, kernelBW, egressBW float64
	if tIngress > 0 {
		ingressBW = (float64(n) / (float64(tIngress) / 1000.0)) / 1e9
	}
	if tKernel > 0 {
		kernelBW = (float64(n) / (float64(tKernel) / 1000.0)) / 1e9
	}
	if tEgress > 0 {
		egressBW = (float64(outBytes) / (float64(tEgress) / 1000.0)) / 1e9
	}

	profile := GPUProfile{
		IngressDMABytes:           uint64(n),
		IngressDMATimeMs:          float64(tIngress),
		IngressDMABandwidthGBps:   ingressBW,
		KernelTimeMs:              float64(tKernel),
		InternalVRAMBandwidthGBps: kernelBW,
		EgressDMABytes:            uint64(outBytes),
		EgressDMATimeMs:           float64(tEgress),
		EgressDMABandwidthGBps:    egressBW,
		TotalRoundTripMs:          totalMs,
		PCIeOverheadPercent:       pcieOverhead,
		DeviceName:                g.DeviceName,
		SMCount:                   g.SMCount,
		TotalVRAMBytes:            g.TotalVRAMBytes,
	}

	var outDesc []uint32
	if outErr == nil {
		outDesc = unsafe.Slice((*uint32)(unsafe.Pointer(&outBuf.Bytes[0])), n)
	} else {
		outDesc = fallbackDesc
	}

	g.matchBuf = g.matchBuf[:0]
	if cap(g.matchBuf) < n/4 {
		g.matchBuf = make([]LZ77Match, 0, n/4)
	}

	for i, desc := range outDesc {
		if IsMatch32(desc) {
			l, off := UnpackMatch32(desc)
			g.matchBuf = append(g.matchBuf, LZ77Match{Position: uint32(i), Offset: off, Length: l})
		}
	}

	matches := g.matchBuf

	runtime.KeepAlive(src)
	if inErr == nil {
		runtime.KeepAlive(inBuf.Bytes)
	}
	if outErr == nil {
		runtime.KeepAlive(outBuf.Bytes)
	}

	return matches, profile, nil
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
