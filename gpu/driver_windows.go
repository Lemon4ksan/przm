// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows

package gpu

import "syscall"

func init() {
	dll := syscall.NewLazyDLL("nvcuda.dll")
	modCUDA = dll

	cuInit = dll.NewProc("cuInit")
	cuDeviceGet = dll.NewProc("cuDeviceGet")
	cuDeviceGetCount = dll.NewProc("cuDeviceGetCount")
	cuDeviceGetName = dll.NewProc("cuDeviceGetName")
	cuDeviceTotalMem = dll.NewProc("cuDeviceTotalMem_v2")
	cuDeviceGetAttribute = dll.NewProc("cuDeviceGetAttribute")
	cuCtxCreate = dll.NewProc("cuCtxCreate_v2")
	cuCtxSetCurrent = dll.NewProc("cuCtxSetCurrent")
	cuMemAlloc = dll.NewProc("cuMemAlloc_v2")
	cuMemFree = dll.NewProc("cuMemFree_v2")
	cuMemcpyHtoDAsync = dll.NewProc("cuMemcpyHtoDAsync_v2")
	cuMemcpyDtoHAsync = dll.NewProc("cuMemcpyDtoHAsync_v2")
	cuStreamCreate = dll.NewProc("cuStreamCreate")
	cuStreamDestroy = dll.NewProc("cuStreamDestroy_v2")
	cuStreamSynchronize = dll.NewProc("cuStreamSynchronize")
	cuModuleGetFunc = dll.NewProc("cuModuleGetFunction")
	cuLaunchKernel = dll.NewProc("cuLaunchKernel")
	cuEventCreate = dll.NewProc("cuEventCreate")
	cuEventRecord = dll.NewProc("cuEventRecord")
	cuEventSynchronize = dll.NewProc("cuEventSynchronize")
	cuEventElapsedTime = dll.NewProc("cuEventElapsedTime")
	cuEventDestroy = dll.NewProc("cuEventDestroy_v2")
	cuPrimaryCtxRelease = dll.NewProc("cuDevicePrimaryCtxRelease")
	cuPrimaryCtxRetain = dll.NewProc("cuDevicePrimaryCtxRetain")
	cuModuleLoadDataEx = dll.NewProc("cuModuleLoadDataEx")
	cuModuleUnload = dll.NewProc("cuModuleUnload")
	cuMemHostRegister = dll.NewProc("cuMemHostRegister")
	cuMemHostUnregister = dll.NewProc("cuMemHostUnregister")
	cuStreamWaitEvent = dll.NewProc("cuStreamWaitEvent")
	cuMemsetD8Async = dll.NewProc("cuMemsetD8Async")
	cuMemAllocHost = dll.NewProc("cuMemAllocHost")
	cuMemFreeHost = dll.NewProc("cuMemFreeHost")
	cuMemHostAlloc = dll.NewProc("cuMemHostAlloc")
}
