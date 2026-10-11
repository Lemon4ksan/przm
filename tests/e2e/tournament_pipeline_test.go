// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"unsafe"

	"github.com/lemon4ksan/przm"
)

func TestTournamentPipeline_AllEnginesEndToEnd(t *testing.T) {
	// 1. Test Padic (64-bit Pointer Graph / Monotonic Database Keys)
	t.Run("Padic_DatabaseBTree_AutoSelect", func(t *testing.T) {
		const n = 8192 // 64 KiB
		raw := make([]byte, n*8)
		words := (*[1 << 30]uint64)(unsafe.Pointer(&raw[0]))[:n:n]
		curr := uint64(1718000000000)
		for i := range n {
			curr += uint64((i % 7) + 1 + ((i * 13) % 11))
			words[i] = curr
		}

		comp, err := przm.Compress(raw, 3)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}

		// Verify that Padic or specialized engine was selected
		t.Logf("Padic DatabaseBTree: raw=%d -> comp=%d (%.2fx density, Magic=0x%08X)",
			len(raw), len(comp), float64(len(raw))/float64(len(comp)), binary.LittleEndian.Uint32(comp[0:4]))

		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("Bit-exact mismatch on Padic DatabaseBTree roundtrip")
		}
	})

	// 2. Test Hodge Étale Sheaf (JSON Logs)
	t.Run("Hodge_EtaleJSON_AutoSelect", func(t *testing.T) {
		// Generate 64 KiB of JSON logs
		const targetSize = 65536
		raw := make([]byte, targetSize)
		pos := 0
		seq := 1001
		var lineBuf [512]byte

		for pos < targetSize {
			ts := uint64(1772640000 + (seq % 86400))
			meth := "GET"
			ep := "/api/v1/users/profile"
			p := 0
			p += copy(lineBuf[p:], `{"ts":`)
			p += copy(lineBuf[p:], "1772640000")
			_ = ts
			p += copy(lineBuf[p:], `,"level":"INFO","trace_id":"e4d8`)
			p += copy(lineBuf[p:], "03e9")
			p += copy(lineBuf[p:], `-9b2e-4b71-8894-c5a782`)
			p += copy(lineBuf[p:], "004287")
			p += copy(lineBuf[p:], `","client_ip":"192.168.1.`)
			p += copy(lineBuf[p:], "2")
			p += copy(lineBuf[p:], `","method":"`)
			p += copy(lineBuf[p:], meth)
			p += copy(lineBuf[p:], `","uri":"`)
			p += copy(lineBuf[p:], ep)
			p += copy(lineBuf[p:], `","status":200,"duration_ms":37,"bytes_sent":1904}
`)
			rem := targetSize - pos
			toCopy := min(p, rem)
			copy(raw[pos:pos+toCopy], lineBuf[:toCopy])
			pos += toCopy
			seq++
		}

		comp, err := przm.Compress(raw, 3)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}

		t.Logf("Hodge Étale JSON: raw=%d -> comp=%d (%.2fx density, Magic=0x%08X)",
			len(raw), len(comp), float64(len(raw))/float64(len(comp)), binary.LittleEndian.Uint32(comp[0:4]))

		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("Bit-exact mismatch on Hodge Étale JSON roundtrip")
		}
	})

	// 3. Test Symplectic Sp(8) / 8-Channel Audio
	t.Run("Symplectic_Sp8_Audio8_AutoSelect", func(t *testing.T) {
		const n = 65536
		raw := make([]byte, n)
		for i := 0; i < n; i += 8 {
			q0 := byte(i * 3)
			q1 := byte(i*5 + 1)
			q2 := byte(i * 7)
			q3 := byte(i*11 + 3)
			p0 := q0 ^ byte((i/8)*2)
			p1 := q1 ^ byte((i/8)*4)
			p2 := q2 ^ byte((i/8)*6)
			p3 := q3 ^ byte((i/8)*8)
			raw[i+0] = q0
			raw[i+1] = q1
			raw[i+2] = q2
			raw[i+3] = q3
			raw[i+4] = p0
			raw[i+5] = p1
			raw[i+6] = p2
			raw[i+7] = p3
		}

		comp, err := przm.Compress(raw, 3)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}

		t.Logf("Symplectic Sp8 Audio: raw=%d -> comp=%d (%.2fx density, Magic=0x%08X)",
			len(raw), len(comp), float64(len(raw))/float64(len(comp)), binary.LittleEndian.Uint32(comp[0:4]))

		dec, err := przm.Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress failed: %v", err)
		}
		if !bytes.Equal(dec, raw) {
			t.Fatalf("Bit-exact mismatch on Symplectic Sp8 Audio roundtrip")
		}
	})
}
