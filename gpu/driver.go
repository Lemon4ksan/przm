// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gpu

// cuDriver abstracts dynamic loading of the platform CUDA driver library.
type cuDriver interface {
	Load() error
}

// cuProc abstracts a callable CUDA driver function pointer.
type cuProc interface {
	Find() error
	Call(a ...uintptr) (r1, r2 uintptr, lastErr error)
}

var (
	modCUDA cuDriver

	cuInit               cuProc
	cuDeviceGet          cuProc
	cuDeviceGetCount     cuProc
	cuDeviceGetName      cuProc
	cuDeviceTotalMem     cuProc
	cuDeviceGetAttribute cuProc
	cuCtxCreate          cuProc
	cuCtxSetCurrent      cuProc
	cuMemAlloc           cuProc
	cuMemFree            cuProc
	cuMemcpyHtoDAsync    cuProc
	cuMemcpyDtoHAsync    cuProc
	cuStreamCreate       cuProc
	cuStreamDestroy      cuProc
	cuStreamSynchronize  cuProc
	cuModuleGetFunc      cuProc
	cuLaunchKernel       cuProc
	cuEventCreate        cuProc
	cuEventRecord        cuProc
	cuEventSynchronize   cuProc
	cuEventElapsedTime   cuProc
	cuEventDestroy       cuProc
	cuPrimaryCtxRelease  cuProc
	cuPrimaryCtxRetain   cuProc
	cuModuleLoadDataEx   cuProc
	cuModuleUnload       cuProc
	cuMemHostRegister    cuProc
	cuMemHostUnregister  cuProc
	cuStreamWaitEvent    cuProc
	cuMemsetD8Async      cuProc
	cuMemAllocHost       cuProc
	cuMemFreeHost        cuProc
	cuMemHostAlloc       cuProc
)
