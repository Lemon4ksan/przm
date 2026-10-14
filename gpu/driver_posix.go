// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !windows

package gpu

import "errors"

var errCUDAUnavailable = errors.New("CUDA driver is not available on non-Windows platforms")

type stubDriver struct{}

func (s *stubDriver) Load() error {
	return errCUDAUnavailable
}

type stubProc struct {
	name string
}

func (p *stubProc) Find() error {
	return errCUDAUnavailable
}

func (p *stubProc) Call(_ ...uintptr) (uintptr, uintptr, error) {
	return 999, 0, errCUDAUnavailable
}

func init() {
	modCUDA = &stubDriver{}

	newStub := func(name string) cuProc {
		return &stubProc{name: name}
	}

	cuInit = newStub("cuInit")
	cuDeviceGet = newStub("cuDeviceGet")
	cuDeviceGetCount = newStub("cuDeviceGetCount")
	cuDeviceGetName = newStub("cuDeviceGetName")
	cuDeviceTotalMem = newStub("cuDeviceTotalMem_v2")
	cuDeviceGetAttribute = newStub("cuDeviceGetAttribute")
	cuCtxCreate = newStub("cuCtxCreate_v2")
	cuCtxSetCurrent = newStub("cuCtxSetCurrent")
	cuMemAlloc = newStub("cuMemAlloc_v2")
	cuMemFree = newStub("cuMemFree_v2")
	cuMemcpyHtoDAsync = newStub("cuMemcpyHtoDAsync_v2")
	cuMemcpyDtoHAsync = newStub("cuMemcpyDtoHAsync_v2")
	cuStreamCreate = newStub("cuStreamCreate")
	cuStreamDestroy = newStub("cuStreamDestroy_v2")
	cuStreamSynchronize = newStub("cuStreamSynchronize")
	cuModuleGetFunc = newStub("cuModuleGetFunction")
	cuLaunchKernel = newStub("cuLaunchKernel")
	cuEventCreate = newStub("cuEventCreate")
	cuEventRecord = newStub("cuEventRecord")
	cuEventSynchronize = newStub("cuEventSynchronize")
	cuEventElapsedTime = newStub("cuEventElapsedTime")
	cuEventDestroy = newStub("cuEventDestroy_v2")
	cuPrimaryCtxRelease = newStub("cuDevicePrimaryCtxRelease")
	cuPrimaryCtxRetain = newStub("cuDevicePrimaryCtxRetain")
	cuModuleLoadDataEx = newStub("cuModuleLoadDataEx")
	cuModuleUnload = newStub("cuModuleUnload")
	cuMemHostRegister = newStub("cuMemHostRegister")
	cuMemHostUnregister = newStub("cuMemHostUnregister")
	cuStreamWaitEvent = newStub("cuStreamWaitEvent")
	cuMemsetD8Async = newStub("cuMemsetD8Async")
	cuMemAllocHost = newStub("cuMemAllocHost")
	cuMemFreeHost = newStub("cuMemFreeHost")
	cuMemHostAlloc = newStub("cuMemHostAlloc")
}
