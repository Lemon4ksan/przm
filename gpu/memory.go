package gpu

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

var (
	cuMemAllocHost     = modCUDA.NewProc("cuMemAllocHost")
	cuMemFreeHost      = modCUDA.NewProc("cuMemFreeHost")
	cuPrimaryCtxRetain = modCUDA.NewProc("cuDevicePrimaryCtxRetain")
	pinnedMu           sync.Mutex
	pinnedCtx          uintptr
	cuMemHostAlloc     = modCUDA.NewProc("cuMemHostAlloc")
)

func ensureContext() error {
	pinnedMu.Lock()
	defer pinnedMu.Unlock()

	if r, _, _ := cuInit.Call(0); r != 0 {
		return fmt.Errorf("cuInit failed: %d", r)
	}
	var dev int32
	if r, _, _ := cuDeviceGet.Call(uintptr(unsafe.Pointer(&dev)), 0); r != 0 {
		return fmt.Errorf("cuDeviceGet failed: %d", r)
	}

	var ctx uintptr
	if pinnedCtx != 0 {
		_, _, _ = cuCtxSetCurrent.Call(pinnedCtx)
		return nil
	}
	if r, _, _ := cuPrimaryCtxRetain.Call(uintptr(unsafe.Pointer(&ctx)), uintptr(dev)); r == 0 && ctx != 0 {
		pinnedCtx = ctx
		_, _, _ = cuCtxSetCurrent.Call(pinnedCtx)
		return nil
	}

	if r, _, _ := cuCtxCreate.Call(uintptr(unsafe.Pointer(&ctx)), 0, uintptr(dev)); r != 0 {
		return fmt.Errorf("cuCtxCreate failed: %d", r)
	}
	pinnedCtx = ctx
	_, _, _ = cuCtxSetCurrent.Call(pinnedCtx)
	return nil
}

// AllocPinnedHost allocates page-locked (pinned) host memory for fast PCIe DMA.
func AllocPinnedHost(size int) ([]byte, uintptr, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if size <= 0 {
		return nil, 0, fmt.Errorf("invalid size %d", size)
	}
	if err := ensureContext(); err != nil {
		return nil, 0, err
	}
	var ptr unsafe.Pointer
	r, _, _ := cuMemAllocHost.Call(uintptr(unsafe.Pointer(&ptr)), uintptr(size))
	if r != 0 {
		// Try cuMemHostAlloc fallback
		r, _, _ = cuMemHostAlloc.Call(uintptr(unsafe.Pointer(&ptr)), uintptr(size), 0)
	}
	if r != 0 {
		return nil, 0, fmt.Errorf("cuMemAllocHost failed with error code %d", r)
	}
	slice := unsafe.Slice((*byte)(ptr), size)
	return slice, uintptr(ptr), nil
}

// FreePinnedHost releases page-locked host memory.
func FreePinnedHost(ptr uintptr) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if ptr == 0 {
		return nil
	}
	if err := ensureContext(); err != nil {
		return err
	}
	r, _, _ := cuMemFreeHost.Call(ptr)
	if r != 0 {
		return fmt.Errorf("cuMemFreeHost failed with error code %d", r)
	}
	return nil
}

// PinnedBuffer holds a pinned slice and its raw host pointer.
type PinnedBuffer struct {
	Bytes []byte
	Ptr   uintptr
}

// PinnedBufferPool provides thread-safe reuse of pinned host buffers.
type PinnedBufferPool struct {
	mu      sync.Mutex
	bufSize int
	maxCap  int
	pool    []PinnedBuffer
}

const defaultMaxPinnedPoolCap = 8

// NewPinnedBufferPool initializes pool with preallocated pinned buffers.
func NewPinnedBufferPool(bufSize, initialCap int) (*PinnedBufferPool, error) {
	maxCap := defaultMaxPinnedPoolCap
	if initialCap > maxCap {
		maxCap = initialCap
	}
	p := &PinnedBufferPool{bufSize: bufSize, maxCap: maxCap, pool: make([]PinnedBuffer, 0, maxCap)}
	for i := 0; i < initialCap; i++ {
		b, ptr, err := AllocPinnedHost(bufSize)
		if err != nil {
			_ = p.Close()
			return nil, err
		}
		p.pool = append(p.pool, PinnedBuffer{Bytes: b, Ptr: ptr})
	}
	return p, nil
}

// Get returns a pinned buffer from the pool or allocates a new one.
func (p *PinnedBufferPool) Get() (PinnedBuffer, error) {
	p.mu.Lock()
	if len(p.pool) > 0 {
		last := len(p.pool) - 1
		buf := p.pool[last]
		p.pool = p.pool[:last]
		p.mu.Unlock()
		return buf, nil
	}
	p.mu.Unlock()
	b, ptr, err := AllocPinnedHost(p.bufSize)
	if err != nil {
		return PinnedBuffer{}, err
	}
	return PinnedBuffer{Bytes: b, Ptr: ptr}, nil
}

// Put returns a buffer back to the pool, or frees it if capacity is reached.
func (p *PinnedBufferPool) Put(buf PinnedBuffer) {
	if buf.Ptr == 0 || len(buf.Bytes) != p.bufSize {
		return
	}
	p.mu.Lock()
	if len(p.pool) < p.maxCap {
		p.pool = append(p.pool, buf)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	_ = FreePinnedHost(buf.Ptr)
}

// Close frees all pooled buffers.
func (p *PinnedBufferPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, buf := range p.pool {
		_ = FreePinnedHost(buf.Ptr)
	}
	p.pool = nil
	return nil
}
