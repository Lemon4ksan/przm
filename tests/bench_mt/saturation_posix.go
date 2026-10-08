//go:build !windows

package main

import (
	"syscall"
	"time"
)

// ProcessCPUTimer tracks wall-clock and process CPU time on POSIX systems.
type ProcessCPUTimer struct {
	ruStart syscall.Rusage
	start   time.Time
}

// startCPUTimer captures initial rusage and wall clock.
func startCPUTimer() *ProcessCPUTimer {
	t := &ProcessCPUTimer{start: time.Now()}
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &t.ruStart)
	return t
}

// Stop computes elapsed wall time, CPU time (user + sys), and saturation factor S.
func (t *ProcessCPUTimer) Stop() (wallElapsed time.Duration, cpuElapsed time.Duration, saturation float64) {
	wallElapsed = time.Since(t.start)
	var ruEnd syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ruEnd)

	utime := time.Duration(ruEnd.Utime.Sec-t.ruStart.Utime.Sec)*time.Second + time.Duration(ruEnd.Utime.Usec-t.ruStart.Utime.Usec)*time.Microsecond
	stime := time.Duration(ruEnd.Stime.Sec-t.ruStart.Stime.Sec)*time.Second + time.Duration(ruEnd.Stime.Usec-t.ruStart.Stime.Usec)*time.Microsecond
	cpuElapsed = utime + stime
	if wallElapsed > 0 {
		saturation = cpuElapsed.Seconds() / wallElapsed.Seconds()
	}
	return wallElapsed, cpuElapsed, saturation
}
