// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"sync"
	"testing"
)

func TestCompressWithOptionsFunctional(t *testing.T) {
	data := []byte("Functional options pattern verification for Season 22 root facade generic pipeline architect.")
	comp, err := CompressWithOptions(data,
		WithLevel(6),
		WithThreads(2),
		WithWindowBits(18),
		WithWindowLog(18),
		WithStandardStreamOnly(true),
	)
	if err != nil {
		t.Fatalf("CompressWithOptions failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	n, err := Decompress(comp, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if n != len(data) || !bytes.Equal(dst[:n], data) {
		t.Fatalf("Roundtrip mismatch: got %d bytes, want %d", n, len(data))
	}
}

func TestCompressWithOptionsStruct(t *testing.T) {
	data := []byte("Struct-based options compatibility check for benchmark and tool consumers.")
	opts := CompressOptions{
		Level:      4,
		Threads:    1,
		WindowBits: 16,
	}
	comp, err := CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions with struct failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	dec, err := DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatalf("DecompressTo output mismatch")
	}
}

func TestDecompressToZeroAllocations(t *testing.T) {
	data := []byte("Zero allocation guarantee test for DecompressTo returning subslice.")
	comp, err := Compress(data, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	allocs := testing.AllocsPerRun(100, func() {
		dec, decErr := DecompressTo(comp, dst)
		if decErr != nil || len(dec) != len(data) {
			t.Fatalf("DecompressTo inside test failed: %v", decErr)
		}
	})

	if allocs != 0.0 {
		t.Fatalf("DecompressTo allocated %f allocs/op (strictly 0.0 required)", allocs)
	}
}

func TestDecodeHelper(t *testing.T) {
	data := []byte("Decode helper allocating slice automatically.")
	comp, err := Compress(data, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	decoded, err := Decode(comp)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if !bytes.Equal(decoded, data) {
		t.Fatalf("Decode output mismatch")
	}
}

func TestDynamicExternalDictionaryInjection(t *testing.T) {
	entries := []string{
		"PRZM_ARCHITECT_ZENITH",
		"SEASON_22_AUDIT_SWARM",
		"FLAT_CODE_LAW_CERTIFIED",
	}
	desc := NewDictionaryDesc(0x00002201, entries)
	if desc.DictID != 0x00002201 || desc.NumEntries != 3 {
		t.Fatalf("NewDictionaryDesc unexpected fields: %+v", desc)
	}

	opts := ApplyOptions(WithDictDesc(desc))
	if opts.DictID != 0x00002201 || opts.Dict != desc {
		t.Fatalf("ApplyOptions WithDictDesc mismatch: %+v", opts)
	}
}

func TestCompressWithRawDictRoundtrip(t *testing.T) {
	dict := []byte("prefix_dictionary_context_buffer_data_")
	data := []byte("prefix_dictionary_context_buffer_data_payload_stream_content_1234567890")
	comp, err := CompressWithDict(data, 6, dict)
	if err != nil {
		t.Fatalf("CompressWithDict failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	n, err := Decompress(comp, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(dst[:n], data) {
		t.Fatalf("CompressWithDict roundtrip mismatch")
	}
}

func TestDefaultOptionsAndFallback(t *testing.T) {
	opts := DefaultOptions()
	if opts.Level != 6 || opts.Threads != 1 || opts.WindowBits != 0 {
		t.Fatalf("DefaultOptions unexpected: %+v", opts)
	}

	// Empty compression returns valid minimal container
	emptyComp, err := Compress(nil, 6)
	if err != nil {
		t.Fatalf("Compress(nil) failed: %v", err)
	}
	sz, err := GetUncompressedSize(emptyComp)
	if err != nil || sz != 0 {
		t.Fatalf("GetUncompressedSize(emptyComp) = %d, err = %v; want 0, nil", sz, err)
	}
}

func TestConcurrentCompressDecompress(t *testing.T) {
	const numGoroutines = 64
	const itersPerGoroutine = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(gid int) {
			defer wg.Done()
			for it := range itersPerGoroutine {
				payloadSize := 256 + ((gid*31 + it*17) % 8192)
				buf := make([]byte, payloadSize)
				for i := range buf {
					buf[i] = byte(gid + it + i*3)
				}

				lvl := 1 + ((gid + it) % 9)
				comp, err := Compress(buf, lvl)
				if err != nil {
					t.Errorf("goroutine %d iter %d: Compress failed: %v", gid, it, err)
					return
				}

				dec, err := Decode(comp)
				if err != nil {
					t.Errorf("goroutine %d iter %d: Decode failed: %v", gid, it, err)
					return
				}
				if !bytes.Equal(buf, dec) {
					t.Errorf("goroutine %d iter %d: payload mismatch! len=%d vs %d", gid, it, len(buf), len(dec))
					return
				}

				dst := make([]byte, payloadSize, payloadSize+64)
				decTo, err := DecompressTo(comp, dst)
				if err != nil {
					t.Errorf("goroutine %d iter %d: DecompressTo failed: %v", gid, it, err)
					return
				}
				if !bytes.Equal(buf, decTo) {
					t.Errorf("goroutine %d iter %d: DecompressTo mismatch", gid, it)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestPublicAPIGracefulEdgeCases(t *testing.T) {
	t.Run("NilAndEmpty", func(t *testing.T) {
		cNil, err := Compress(nil, 6)
		if err != nil || len(cNil) < 8 {
			t.Fatalf("Compress(nil) failed: %v, len=%d", err, len(cNil))
		}
		cEmpty, err := Compress([]byte{}, 6)
		if err != nil || len(cEmpty) < 8 {
			t.Fatalf("Compress(empty) failed: %v", err)
		}

		cdNil, err := CompressWithDict(nil, 6, nil)
		if err != nil || len(cdNil) < 8 {
			t.Fatalf("CompressWithDict(nil) failed: %v", err)
		}

		coNil, err := CompressWithOptions(nil, WithLevel(4))
		if err != nil || len(coNil) < 8 {
			t.Fatalf("CompressWithOptions(nil) failed: %v", err)
		}

		n, err := Decompress(nil, nil)
		if err != nil || n != 0 {
			t.Fatalf("Decompress(nil) failed: %v, n=%d", err, n)
		}
		n, err = Decompress([]byte{}, nil)
		if err != nil || n != 0 {
			t.Fatalf("Decompress(empty) failed: %v, n=%d", err, n)
		}

		dTo, err := DecompressTo(nil, nil)
		if err != nil || len(dTo) != 0 {
			t.Fatalf("DecompressTo(nil) failed: %v", err)
		}

		n, err = DecompressDict(nil, nil, nil)
		if err != nil || n != 0 {
			t.Fatalf("DecompressDict(nil) failed: %v", err)
		}

		n, err = DecompressWithDict(nil, nil, nil, nil)
		if err != nil || n != 0 {
			t.Fatalf("DecompressWithDict(nil) failed: %v", err)
		}

		dec, err := Decode(nil)
		if err != nil || len(dec) != 0 {
			t.Fatalf("Decode(nil) failed: %v", err)
		}
		dec, err = Decode([]byte{})
		if err != nil || len(dec) != 0 {
			t.Fatalf("Decode(empty) failed: %v", err)
		}
	})

	t.Run("OversizedBlock", func(t *testing.T) {
		oversized := make([]byte, MaxBlockSize+1)
		_, err := Compress(oversized, 6)
		if err != ErrExceedsMaxBlock {
			t.Fatalf("expected ErrExceedsMaxBlock, got %v", err)
		}

		_, err = CompressWithDict(oversized, 6, nil)
		if err != ErrExceedsMaxBlock {
			t.Fatalf("expected ErrExceedsMaxBlock, got %v", err)
		}

		_, err = CompressWithOptions(oversized, WithLevel(1))
		if err != ErrExceedsMaxBlock {
			t.Fatalf("expected ErrExceedsMaxBlock, got %v", err)
		}
	})

	t.Run("CorruptedInputs", func(t *testing.T) {
		_, err := Decompress([]byte{0x01, 0x02, 0x03}, nil)
		if err != ErrCorruptedFrame {
			t.Fatalf("expected ErrCorruptedFrame on short frame, got %v", err)
		}

		badMagic := []byte{'B', 'A', 'D', 'M', 0, 10, 0, 0}
		dst := make([]byte, 10, 10+64)
		_, err = Decompress(badMagic, dst)
		if err != ErrInvalidMagic {
			t.Fatalf("expected ErrInvalidMagic, got %v", err)
		}

		_, err = Decode(badMagic)
		if err != ErrInvalidMagic {
			t.Fatalf("expected ErrInvalidMagic on Decode, got %v", err)
		}
	})
}
