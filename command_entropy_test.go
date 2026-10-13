// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/seal/codec/compress/brotli"
)

func TestCommandEntropy_RoundtripAndZeroAlloc(t *testing.T) {
	initCDLL(t)

	brotliDict := brotli.StaticDictionaryData()
	przm.RegisterRawDictionary(przm.DictIDBrotliWeb, brotliDict)
	defer przm.UnregisterRawDictionary(przm.DictIDBrotliWeb)

	testFiles := []string{
		"compress.go",
		"decoder_dispatch.go",
		"optimal.go",
		"README.md",
		"src/przm_dec.c",
	}

	for _, relPath := range testFiles {
		data, err := os.ReadFile(relPath)
		if err != nil {
			t.Fatalf("failed to read test file %s: %v", relPath, err)
		}
		expectedSHA := sha256.Sum256(data)

		for _, lvl := range []int{4, 5, 6} {
			t.Run(filepath.Base(relPath)+"_L"+string(rune('0'+lvl)), func(t *testing.T) {
				opts := przm.CompressOptions{
					Level:       lvl,
					DictID:      przm.DictIDBrotliWeb,
					RawDict:     brotliDict,
					CompactDict: false,
					MinMatch:    3,
				}

				// 1. Test Standard PRZM (tested across both Go and Pure C)
				compStd, err := przm.Compress(data, lvl)
				if err != nil {
					t.Fatalf("Compress failed: %v", err)
				}

				dst := make([]byte, len(data)+64)
				decompStd, err := przm.DecompressTo(compStd, dst)
				if err != nil {
					t.Fatalf("Go DecompressTo (Std) failed: %v", err)
				}
				if !bytes.Equal(decompStd, data) {
					t.Fatalf("Go DecompressTo (Std) mismatch!")
				}

				// Pure C decompressor check via DLL
				cDst := make([]byte, len(data)+64)
				cBytes, err := cDecompress(compStd, cDst)
				if err != nil {
					t.Fatalf("Pure C decompress failed: %v", err)
				}
				if int(cBytes) != len(data) {
					t.Fatalf("Pure C decompressed len mismatch: got %d, want %d", cBytes, len(data))
				}
				if !bytes.Equal(cDst[:cBytes], data) {
					t.Fatalf("Pure C decompressed content mismatch!")
				}
				if sha256.Sum256(cDst[:cBytes]) != expectedSHA {
					t.Fatalf("Pure C SHA-256 mismatch!")
				}

				// Strict Zero Allocation Law audit
				allocs := testing.AllocsPerRun(100, func() {
					out, errRun := przm.DecompressTo(compStd, dst)
					if errRun != nil || len(out) != len(data) {
						t.Fatalf("DecompressTo inside AllocsPerRun failed: %v", errRun)
					}
				})
				if allocs > 0.00 {
					t.Fatalf("DecompressTo violated Zero-Allocation Law: got %.2f allocs/op, want 0.00", allocs)
				}

				// 2. Test PRZM with Brotli Dictionary (tested with Go DecompressTo)
				compDict, err := przm.CompressWithOptions(data, opts)
				if err != nil {
					t.Fatalf("CompressWithOptions failed: %v", err)
				}
				decompDict, err := przm.DecompressTo(compDict, dst)
				if err != nil {
					t.Fatalf("Go DecompressTo (Dict) failed: %v", err)
				}
				if !bytes.Equal(decompDict, data) {
					t.Fatalf("Go DecompressTo (Dict) mismatch!")
				}
				if sha256.Sum256(decompDict) != expectedSHA {
					t.Fatalf("Go SHA-256 hash mismatch (Dict)!")
				}

				allocsDict := testing.AllocsPerRun(100, func() {
					out, errRun := przm.DecompressTo(compDict, dst)
					if errRun != nil || len(out) != len(data) {
						t.Fatalf("DecompressTo (Dict) inside AllocsPerRun failed: %v", errRun)
					}
				})
				if allocsDict > 0.00 {
					t.Fatalf("DecompressTo (Dict) violated Zero-Allocation Law: got %.2f allocs/op, want 0.00", allocsDict)
				}
			})
		}
	}
}

func TestCommandEntropy_AdversarialCorruption(t *testing.T) {
	data, err := os.ReadFile("compress.go")
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}

	brotliDict := brotli.StaticDictionaryData()
	przm.RegisterRawDictionary(przm.DictIDBrotliWeb, brotliDict)
	defer przm.UnregisterRawDictionary(przm.DictIDBrotliWeb)

	opts := przm.CompressOptions{
		Level:       5,
		DictID:      przm.DictIDBrotliWeb,
		RawDict:     brotliDict,
		CompactDict: false,
		MinMatch:    3,
	}

	comp, err := przm.CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(data)+64)

	// Test truncated streams
	for cut := len(comp) - 1; cut > len(comp)-100 && cut > 0; cut -= 5 {
		_, errTrunc := przm.DecompressTo(comp[:cut], dst)
		if errTrunc == nil {
			t.Fatalf("expected error on truncated stream cut=%d, got nil", cut)
		}
	}
}
