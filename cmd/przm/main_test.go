// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{500, "500 B"},
		{1024, "1.00 KiB"},
		{1024 * 1024, "1.00 MiB"},
		{1024 * 1024 * 1024, "1.00 GiB"},
	}

	for _, tc := range tests {
		got := formatBytes(tc.input)
		if got != tc.expected {
			t.Errorf("formatBytes(%d) = %s; want %s", tc.input, got, tc.expected)
		}
	}
}

func TestFormatThroughput(t *testing.T) {
	if got := formatThroughput(100, 0); got != "N/A" {
		t.Errorf("expected N/A for 0 duration, got %s", got)
	}

	// 100 MB in 1 second
	got := formatThroughput(100*1024*1024, time.Second)
	if got != "100.00 MB/s" {
		t.Errorf("expected 100.00 MB/s, got %s", got)
	}

	// 2048 MB in 1 second -> 2.00 GB/s
	gotGB := formatThroughput(2048*1024*1024, time.Second)
	if gotGB != "2.00 GB/s" {
		t.Errorf("expected 2.00 GB/s, got %s", gotGB)
	}
}
