// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build windows

package main

import (
	"syscall"
	"time"
)

// ProcessCPUTimer tracks wall-clock and process CPU time on Windows.
type ProcessCPUTimer struct {
	handle syscall.Handle
	k1, u1 syscall.Filetime
	start  time.Time
}

// startCPUTimer captures the current process times and wall clock.
func startCPUTimer() *ProcessCPUTimer {
	h, _ := syscall.GetCurrentProcess()
	t := &ProcessCPUTimer{handle: h}
	var c, e syscall.Filetime
	_ = syscall.GetProcessTimes(h, &c, &e, &t.k1, &t.u1)
	t.start = time.Now()
	return t
}

// Stop computes elapsed wall time, CPU time (kernel + user), and the CPU saturation factor S.
func (t *ProcessCPUTimer) Stop() (wallElapsed time.Duration, cpuElapsed time.Duration, saturation float64) {
	wallElapsed = time.Since(t.start)
	var c, e, k2, u2 syscall.Filetime
	_ = syscall.GetProcessTimes(t.handle, &c, &e, &k2, &u2)

	cpuTime1 := fileTimeToDuration(t.k1, t.u1)
	cpuTime2 := fileTimeToDuration(k2, u2)
	cpuElapsed = cpuTime2 - cpuTime1
	if wallElapsed > 0 {
		saturation = cpuElapsed.Seconds() / wallElapsed.Seconds()
	}
	return wallElapsed, cpuElapsed, saturation
}

// fileTimeToDuration converts Windows Filetime (100-nanosecond ticks) to time.Duration.
func fileTimeToDuration(k, u syscall.Filetime) time.Duration {
	kt := (int64(k.HighDateTime) << 32) | int64(k.LowDateTime)
	ut := (int64(u.HighDateTime) << 32) | int64(u.LowDateTime)
	return time.Duration((kt + ut) * 100)
}
