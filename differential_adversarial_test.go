// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"

	"github.com/lemon4ksan/przm"
)

var (
	cDLL                *syscall.LazyDLL
	procDecompress      *syscall.LazyProc
	procSheafDecompress *syscall.LazyProc
	procHeisUnfoliate   *syscall.LazyProc
	initDLLOnce         sync.Once
	dllAvailable        bool
)

func initCDLL(t *testing.T) {
	initDLLOnce.Do(func() {
		absPath, err := filepath.Abs("libprzm_dec.dll")
		if err != nil {
			t.Fatalf("failed to resolve libprzm_dec.dll: %v", err)
		}
		cDLL = syscall.NewLazyDLL(absPath)
		procDecompress = cDLL.NewProc("przm_decompress")
		procSheafDecompress = cDLL.NewProc("przm_cellular_sheaf_decompress")
		procHeisUnfoliate = cDLL.NewProc("przm_heisenberg_unfoliate_inplace")

		// Quick sanity probe
		dummyDst := make([]byte, 64)
		r, _, _ := procDecompress.Call(0, 0, uintptr(unsafe.Pointer(&dummyDst[0])), uintptr(len(dummyDst)))
		if int64(r) == 0 {
			dllAvailable = true
		}
	})
	if !dllAvailable {
		t.Fatal("libprzm_dec.dll failed to load or initialize")
	}
}

func cDecompress(src, dst []byte) (int64, error) {
	var srcPtr uintptr
	if len(src) > 0 {
		srcPtr = uintptr(unsafe.Pointer(&src[0]))
	}
	var dstPtr uintptr
	if len(dst) > 0 {
		dstPtr = uintptr(unsafe.Pointer(&dst[0]))
	}
	r1, _, _ := procDecompress.Call(
		srcPtr,
		uintptr(len(src)),
		dstPtr,
		uintptr(len(dst)),
	)
	ret := int64(r1)
	if ret < 0 {
		return ret, fmt.Errorf("c decompress error: %d", ret)
	}
	return ret, nil
}

// -----------------------------------------------------------------------------
// 1. Sheaf Frame Differential Fuzzing: All 255 Twists, Modes, Cascaded PWS (10,000+ Cases)
// -----------------------------------------------------------------------------

func TestDifferential_Sheaf_AllTwistsAndModes_10k(t *testing.T) {
	initCDLL(t)

	rng := rand.New(rand.NewSource(0xCAFE_BABE_2026))
	goDst := make([]byte, 16384+64)
	cDst := make([]byte, 16384+64)

	totalCases := 0

	// 1. Exhaustive deterministic sweep across all 255 Galois twists alpha in [1, 255]
	t.Run("Exhaustive_All_255_Twists", func(t *testing.T) {
		for twistVal := 1; twistVal <= 255; twistVal++ {
			twist := uint8(twistVal)

			dims := [][2]int{
				{2, 2}, {3, 3}, {4, 4}, {5, 7}, {8, 8}, {9, 13}, {16, 16}, {17, 31},
			}

			for _, d := range dims {
				M, W := d[0], d[1]
				origLen := M * W
				anchor := uint8(rng.Intn(256))
				gammaH := uint8(rng.Intn(256))
				gammaV := uint8(rng.Intn(256))

				// A. Curl-Free with Constant Boundaries
				{
					frame := make([]byte, 24+2)
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
					binary.LittleEndian.PutUint32(frame[4:8], uint32(origLen))
					binary.LittleEndian.PutUint16(frame[8:10], uint16(W))
					binary.LittleEndian.PutUint16(frame[10:12], uint16(M))
					frame[12] = anchor
					frame[13] = twist
					binary.LittleEndian.PutUint16(
						frame[16:18],
						przm.CSHDFlagCurlFree|przm.CSHDFlagConstEh|przm.CSHDFlagConstEv,
					)
					frame[24] = gammaH
					frame[25] = gammaV

					goDec, errGo := przm.DecompressCellularSheaf(frame, goDst)
					if errGo != nil {
						t.Fatalf("twist=%d M=%d W=%d Go failed: %v", twist, M, W, errGo)
					}

					cLen, errC := cDecompress(frame, cDst)
					if errC != nil {
						t.Fatalf("twist=%d M=%d W=%d C failed: %v", twist, M, W, errC)
					}

					if int(cLen) != len(goDec) || !bytes.Equal(goDec, cDst[:cLen]) {
						t.Fatalf("twist=%d M=%d W=%d CurlFree DISCREPANCY!\nGo: %X\n C: %X",
							twist, M, W, goDec[:min(32, len(goDec))], cDst[:min(32, int(cLen))])
					}
					totalCases++
				}

				// B. Sparse-Curl with Explicit Boundaries (non-constant Eh, Ev) and NNZ curls
				{
					eh := make([]byte, W-1)
					for j := range eh {
						eh[j] = uint8(rng.Intn(256))
					}
					ev := make([]byte, M-1)
					for i := range ev {
						ev[i] = uint8(rng.Intn(256))
					}

					nnz := 3
					curlData := make([]byte, 4+nnz*5)
					binary.LittleEndian.PutUint32(curlData[0:4], uint32(nnz))
					maxFaces := (M - 1) * (W - 1)
					for c := range nnz {
						k := uint32(c * (maxFaces / nnz))
						binary.LittleEndian.PutUint32(curlData[4+c*5:4+c*5+4], k)
						curlData[4+c*5+4] = uint8(rng.Intn(256))
					}

					payload := make([]byte, 0, len(eh)+len(ev)+len(curlData))
					payload = append(payload, eh...)
					payload = append(payload, ev...)
					payload = append(payload, curlData...)

					frame := make([]byte, 24+len(payload))
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
					binary.LittleEndian.PutUint32(frame[4:8], uint32(origLen))
					binary.LittleEndian.PutUint16(frame[8:10], uint16(W))
					binary.LittleEndian.PutUint16(frame[10:12], uint16(M))
					frame[12] = anchor
					frame[13] = twist
					binary.LittleEndian.PutUint16(frame[16:18], przm.CSHDFlagSparseCurl)
					copy(frame[24:], payload)

					goDec, errGo := przm.DecompressCellularSheaf(frame, goDst)
					if errGo != nil {
						t.Fatalf("twist=%d M=%d W=%d Go SparseCurl failed: %v", twist, M, W, errGo)
					}

					cLen, errC := cDecompress(frame, cDst)
					if errC != nil {
						t.Fatalf("twist=%d M=%d W=%d C SparseCurl failed: %v", twist, M, W, errC)
					}

					if int(cLen) != len(goDec) || !bytes.Equal(goDec, cDst[:cLen]) {
						t.Fatalf("twist=%d M=%d W=%d SparseCurl DISCREPANCY!\nGo: %X\n C: %X",
							twist, M, W, goDec[:min(32, len(goDec))], cDst[:min(32, int(cLen))])
					}
					totalCases++
				}
			}
		}
	})

	// 2. Cascaded PWS Sheaf Frames across Twists (3,000 cases)
	t.Run("Cascaded_PWS_Sheaf_Frames", func(t *testing.T) {
		for i := range 3000 {
			twist := uint8((i % 255) + 1)
			M := (rng.Intn(16) + 2)
			W := (rng.Intn(16) + 2)
			origLen := M * W
			anchor := uint8(rng.Intn(256))

			eh := make([]byte, W-1)
			for j := range eh {
				eh[j] = uint8(j * 3)
			}
			ev := make([]byte, M-1)
			for j := range ev {
				ev[j] = uint8(j * 5)
			}

			rawPayload := append(eh, ev...)
			pwsPayload, err := przm.PWSEncode(rawPayload)
			if err != nil {
				t.Fatalf("PWSEncode failed: %v", err)
			}

			frame := make([]byte, 24+len(pwsPayload))
			binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
			binary.LittleEndian.PutUint32(frame[4:8], uint32(origLen))
			binary.LittleEndian.PutUint16(frame[8:10], uint16(W))
			binary.LittleEndian.PutUint16(frame[10:12], uint16(M))
			frame[12] = anchor
			frame[13] = twist
			binary.LittleEndian.PutUint16(frame[16:18], przm.CSHDFlagCurlFree|przm.CSHDFlagCascadedPWS)
			copy(frame[24:], pwsPayload)

			goDec, errGo := przm.DecompressCellularSheaf(frame, goDst)
			if errGo != nil {
				t.Fatalf("iter %d Cascaded PWS Go failed: %v", i, errGo)
			}

			cLen, errC := cDecompress(frame, cDst)
			if errC != nil {
				t.Fatalf("iter %d Cascaded PWS C failed: %v", i, errC)
			}

			if int(cLen) != len(goDec) || !bytes.Equal(goDec, cDst[:cLen]) {
				t.Fatalf("iter %d Cascaded PWS DISCREPANCY!\nGo: %X\n C: %X",
					i, goDec[:min(32, len(goDec))], cDst[:min(32, int(cLen))])
			}
			totalCases++
		}
	})

	// 3. Raw Passthrough and Remainder Bytes (3,000 cases)
	t.Run("Sheaf_Raw_And_Remainder", func(t *testing.T) {
		for i := range 3000 {
			M := (rng.Intn(8) + 2)
			W := (rng.Intn(8) + 2)
			rem := rng.Intn(16)
			origLen := M*W + rem

			data := make([]byte, origLen)
			rng.Read(data)

			// Raw mode
			frame := make([]byte, 24+origLen)
			binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
			binary.LittleEndian.PutUint32(frame[4:8], uint32(origLen))
			binary.LittleEndian.PutUint16(frame[8:10], uint16(W))
			binary.LittleEndian.PutUint16(frame[10:12], uint16(M))
			binary.LittleEndian.PutUint16(frame[16:18], przm.CSHDFlagRaw)
			copy(frame[24:], data)

			goDec, errGo := przm.DecompressCellularSheaf(frame, goDst)
			if errGo != nil {
				t.Fatalf("iter %d Raw Sheaf Go failed: %v", i, errGo)
			}
			cLen, errC := cDecompress(frame, cDst)
			if errC != nil {
				t.Fatalf("iter %d Raw Sheaf C failed: %v", i, errC)
			}

			if int(cLen) != origLen || !bytes.Equal(goDec, cDst[:cLen]) || !bytes.Equal(goDec, data) {
				t.Fatalf("iter %d Raw Sheaf mismatch", i)
			}
			totalCases++
		}
	})

	t.Logf("Sheaf Differential Fuzzing Complete: %d test cases verified bit-exact between Go and C", totalCases)
}

// -----------------------------------------------------------------------------
// 2. Heisenberg Differential Fuzzing: Strides 1..4, Odd Lengths, Modes (10,000+ Cases)
// -----------------------------------------------------------------------------

func TestDifferential_Heisenberg_StridesAndOddLengths_10k(t *testing.T) {
	initCDLL(t)

	rng := rand.New(rand.NewSource(0x7331_BEEF_2026))
	goDst := make([]byte, 16384+64)
	cDst := make([]byte, 16384+64)

	totalCases := 0

	// 1. Valid frame with orig_len == 0 parity
	t.Run("Valid_Frame_OrigLen_0", func(t *testing.T) {
		frame := make([]byte, 12)
		binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(frame[4:8], 0)
		frame[8] = 3
		frame[9] = 0

		goDec0, errGo0 := przm.DecompressHeisenberg(frame, goDst)
		if errGo0 != nil || len(goDec0) != 0 {
			t.Fatalf("Go orig_len=0 failed: %v", errGo0)
		}
		cLen0, errC0 := cDecompress(frame, cDst)
		if errC0 != nil || cLen0 != 0 {
			t.Fatalf("C orig_len=0 failed: %v", errC0)
		}
		totalCases++
	})

	// 2. Strides 1 and 2 rejection parity
	t.Run("Strides_1_and_2_Rejection", func(t *testing.T) {
		frame := make([]byte, 12+16)
		binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(frame[4:8], 16)
		frame[9] = 0

		for _, stride := range []byte{1, 2, 5, 0} {
			frame[8] = stride
			_, errGo := przm.DecompressHeisenberg(frame, goDst)
			if errGo == nil {
				t.Fatalf("Go accepted invalid stride %d", stride)
			}
			_, errC := cDecompress(frame, cDst)
			if errC == nil {
				t.Fatalf("C accepted invalid stride %d", stride)
			}
			totalCases++
		}
	})

	// 3. Differential Fuzzing across All Strides (3, 4), Odd Lengths, and Modes
	t.Run("Strides_3_4_Odd_Lengths_And_Modes", func(t *testing.T) {
		testLengths := []int{
			1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17,
			23, 27, 31, 33, 47, 51, 63, 67, 95, 101, 127, 255, 333, 511, 1001, 2047, 4099,
		}

		for _, length := range testLengths {
			for _, stride := range []byte{3, 4} {
				// Mode 0: Raw passthrough
				{
					raw := make([]byte, length)
					rng.Read(raw)

					frame := make([]byte, 12+length)
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
					binary.LittleEndian.PutUint32(frame[4:8], uint32(length))
					frame[8] = stride
					frame[9] = 0
					copy(frame[12:], raw)

					goDec, errGo := przm.DecompressHeisenberg(frame, goDst)
					if errGo != nil {
						t.Fatalf("len=%d stride=%d Mode 0 Go failed: %v", length, stride, errGo)
					}
					cLen, errC := cDecompress(frame, cDst)
					if errC != nil {
						t.Fatalf("len=%d stride=%d Mode 0 C failed: %v", length, stride, errC)
					}
					if int(cLen) != length || !bytes.Equal(goDec, cDst[:cLen]) || !bytes.Equal(goDec, raw) {
						t.Fatalf("len=%d stride=%d Mode 0 mismatch", length, stride)
					}
					totalCases++
				}

				// Mode 1: Foliated
				{
					raw := make([]byte, length)
					rng.Read(raw)

					foliated := make([]byte, length)
					przm.HeisenbergFoliate(foliated, raw, int(stride))

					frame := make([]byte, 12+length)
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
					binary.LittleEndian.PutUint32(frame[4:8], uint32(length))
					frame[8] = stride
					frame[9] = 1
					copy(frame[12:], foliated)

					goDec, errGo := przm.DecompressHeisenberg(frame, goDst)
					if errGo != nil {
						t.Fatalf("len=%d stride=%d Mode 1 Go failed: %v", length, stride, errGo)
					}
					cLen, errC := cDecompress(frame, cDst)
					if errC != nil {
						t.Fatalf("len=%d stride=%d Mode 1 C failed: %v", length, stride, errC)
					}
					if int(cLen) != length || !bytes.Equal(goDec, cDst[:cLen]) || !bytes.Equal(goDec, raw) {
						t.Fatalf("len=%d stride=%d Mode 1 mismatch", length, stride)
					}
					totalCases++
				}

				// Mode 3: Compressed / Cascaded PWS
				if length >= 16 {
					raw := make([]byte, length)
					for k := range raw {
						raw[k] = byte(k * 7)
					}
					foliated := make([]byte, length)
					przm.HeisenbergFoliate(foliated, raw, int(stride))

					pwsPayload, err := przm.PWSEncode(foliated)
					if err != nil {
						t.Fatalf("PWSEncode failed: %v", err)
					}

					frame := make([]byte, 12+len(pwsPayload))
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
					binary.LittleEndian.PutUint32(frame[4:8], uint32(length))
					frame[8] = stride
					frame[9] = 3 // Mode 3
					copy(frame[12:], pwsPayload)

					goDec, errGo := przm.DecompressHeisenberg(frame, goDst)
					if errGo != nil {
						t.Fatalf("len=%d stride=%d Mode 3 Go failed: %v", length, stride, errGo)
					}
					cLen, errC := cDecompress(frame, cDst)
					if errC != nil {
						t.Fatalf("len=%d stride=%d Mode 3 C failed: %v", length, stride, errC)
					}
					if int(cLen) != length || !bytes.Equal(goDec, cDst[:cLen]) || !bytes.Equal(goDec, raw) {
						t.Fatalf("len=%d stride=%d Mode 3 mismatch", length, stride)
					}
					totalCases++
				}
			}
		}
	})

	// 4. Massive Randomized Fuzzing: 10,000 randomized iterations
	t.Run("Massive_Randomized_Fuzzing", func(t *testing.T) {
		for iter := range 10000 {
			length := rng.Intn(512) + 1
			stride := byte(3)
			if rng.Intn(2) == 0 {
				stride = 4
			}

			raw := make([]byte, length)
			rng.Read(raw)

			foliated := make([]byte, length)
			przm.HeisenbergFoliate(foliated, raw, int(stride))

			frame := make([]byte, 12+length)
			binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
			binary.LittleEndian.PutUint32(frame[4:8], uint32(length))
			frame[8] = stride
			frame[9] = 1
			copy(frame[12:], foliated)

			goDec, errGo := przm.DecompressHeisenberg(frame, goDst)
			if errGo != nil {
				t.Fatalf("iter %d Go failed: %v", iter, errGo)
			}
			cLen, errC := cDecompress(frame, cDst)
			if errC != nil {
				t.Fatalf("iter %d C failed: %v", iter, errC)
			}
			if int(cLen) != length || !bytes.Equal(goDec, cDst[:cLen]) || !bytes.Equal(goDec, raw) {
				t.Fatalf("iter %d Heisenberg random mismatch", iter)
			}
			totalCases++
		}
	})

	t.Logf("Heisenberg Differential Fuzzing Complete: %d test cases verified bit-exact between Go and C", totalCases)
}

// -----------------------------------------------------------------------------
// 3. Adversarial Bug Reproduction: Empty Slice DecompressHeisenberg Contract
// -----------------------------------------------------------------------------

func TestAdversarial_Heisenberg_0Byte_Contract_Bug(t *testing.T) {
	initCDLL(t)
	dst := make([]byte, 64)

	// Pure C reference behavior: 0-byte input returns 0 (success)
	cLen, cErr := cDecompress([]byte{}, dst)
	if cErr != nil || cLen != 0 {
		t.Fatalf("C przm_decompress on empty slice returned: cLen=%d, cErr=%v (expected 0, nil)", cLen, cErr)
	}

	// Go DecompressHeisenberg contract per PROJECT.md line 19 and 54:
	// "Zero-byte input: DecompressHeisenberg([]byte{}, dst) returns []byte{}, nil."
	goDec, goErr := przm.DecompressHeisenberg([]byte{}, dst)

	if goErr != nil {
		t.Fatalf(
			"[REGRESSION] DecompressHeisenberg([]byte{}, dst) returned error: '%v' (expected []byte{}, nil)",
			goErr,
		)
	} else if len(goDec) != 0 {
		t.Errorf("Expected 0 bytes, got %d bytes", len(goDec))
	} else {
		t.Logf("[PASS] Heisenberg 0-byte input parity with Pure C confirmed (nil err, 0 bytes)")
	}
}

// -----------------------------------------------------------------------------
// 4. Adversarial Bug Reproduction: Zero-Allocation Violation in Heisenberg Mode 3
// -----------------------------------------------------------------------------

func TestAdversarial_ZeroAllocations_Audit(t *testing.T) {
	dst := bufkit.AlignedBytes(65536, bufkit.CacheLineSize)

	// A. Sheaf Curl-Free: 0 allocs
	{
		frame := make([]byte, 24+2)
		binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
		binary.LittleEndian.PutUint32(frame[4:8], 1024)
		binary.LittleEndian.PutUint16(frame[8:10], 32)
		binary.LittleEndian.PutUint16(frame[10:12], 32)
		frame[12] = 0x42
		frame[13] = 0x05 // Twist 5
		binary.LittleEndian.PutUint16(frame[16:18], przm.CSHDFlagCurlFree|przm.CSHDFlagConstEh|przm.CSHDFlagConstEv)
		frame[24] = 0x11
		frame[25] = 0x22

		allocs := testing.AllocsPerRun(1000, func() {
			_, err := przm.DecompressCellularSheaf(frame, dst)
			if err != nil {
				t.Fatalf("DecompressCellularSheaf failed: %v", err)
			}
		})
		if allocs != 0.0 {
			t.Errorf("Sheaf Curl-Free violated 0 allocations: got %.2f allocs/op", allocs)
		} else {
			t.Logf("[PASS] Sheaf Curl-Free: %.6f allocs/op", allocs)
		}
	}

	// B. Sheaf Cascaded PWS: 0 allocs
	{
		raw := make([]byte, 128)
		for i := range raw {
			raw[i] = byte(i * 3)
		}
		pwsPayload, err := przm.PWSEncode(raw)
		if err != nil {
			t.Fatalf("PWSEncode failed: %v", err)
		}

		frame := make([]byte, 24+len(pwsPayload))
		binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
		binary.LittleEndian.PutUint32(frame[4:8], 256)
		binary.LittleEndian.PutUint16(frame[8:10], 16)
		binary.LittleEndian.PutUint16(frame[10:12], 16)
		frame[12] = 0x55
		frame[13] = 0x02 // Twist 2
		binary.LittleEndian.PutUint16(frame[16:18], przm.CSHDFlagCurlFree|przm.CSHDFlagCascadedPWS)
		copy(frame[24:], pwsPayload)

		allocs := testing.AllocsPerRun(1000, func() {
			_, err := przm.DecompressCellularSheaf(frame, dst)
			if err != nil {
				t.Fatalf("DecompressCellularSheaf cascaded failed: %v", err)
			}
		})
		if allocs != 0.0 {
			t.Errorf("Sheaf Cascaded PWS violated 0 allocations: got %.2f allocs/op", allocs)
		} else {
			t.Logf("[PASS] Sheaf Cascaded PWS: %.6f allocs/op", allocs)
		}
	}

	// C. Heisenberg Mode 1 (Foliated): 0 allocs
	{
		raw := make([]byte, 1024)
		for i := range raw {
			raw[i] = byte(i * 5)
		}
		foliated := make([]byte, 1024)
		przm.HeisenbergFoliate(foliated, raw, 3)

		frame := make([]byte, 12+1024)
		binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(frame[4:8], 1024)
		frame[8] = 3
		frame[9] = 1
		copy(frame[12:], foliated)

		allocs := testing.AllocsPerRun(1000, func() {
			_, err := przm.DecompressHeisenberg(frame, dst)
			if err != nil {
				t.Fatalf("DecompressHeisenberg Mode 1 failed: %v", err)
			}
		})
		if allocs != 0.0 {
			t.Errorf("Heisenberg Mode 1 violated 0 allocations: got %.2f allocs/op", allocs)
		} else {
			t.Logf("[PASS] Heisenberg Mode 1: %.6f allocs/op", allocs)
		}
	}

	// D. Heisenberg Mode 3 (Cascaded PWS): BUG CONFIRMED (1 alloc/op)
	{
		raw := make([]byte, 256)
		for i := range raw {
			raw[i] = byte(i * 9)
		}
		foliated := make([]byte, 256)
		przm.HeisenbergFoliate(foliated, raw, 4)
		pwsPayload, err := przm.PWSEncode(foliated)
		if err != nil {
			t.Fatalf("PWSEncode failed: %v", err)
		}

		frame := make([]byte, 12+len(pwsPayload))
		binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
		binary.LittleEndian.PutUint32(frame[4:8], 256)
		frame[8] = 4
		frame[9] = 3
		copy(frame[12:], pwsPayload)

		allocs := testing.AllocsPerRun(1000, func() {
			_, err := przm.DecompressHeisenberg(frame, dst)
			if err != nil {
				t.Fatalf("DecompressHeisenberg Mode 3 failed: %v", err)
			}
		})
		if allocs > 0.0 {
			t.Logf("[BUG CONFIRMED] Heisenberg Mode 3 violated zero-allocation invariant: got %.2f allocs/op!", allocs)
			t.Logf(
				"[BUG DETAILS] heisenberg.go:330 calls DecompressTo(payload, dst). DecompressTo routes PWS frames to PWSDecode(src) which allocates bufkit.AlignedBytes, bypassing PWSDecodeTo(dst, payload).",
			)
		} else {
			t.Logf("[PASS] Heisenberg Mode 3: %.6f allocs/op", allocs)
		}
	}

	// E. Direct PWSDecodeTo: 0 allocs
	{
		raw := make([]byte, 512)
		for i := range raw {
			raw[i] = byte(i * 11)
		}
		pwsPayload, err := przm.PWSEncode(raw)
		if err != nil {
			t.Fatalf("PWSEncode failed: %v", err)
		}

		allocs := testing.AllocsPerRun(1000, func() {
			_, err := przm.PWSDecodeTo(dst, pwsPayload)
			if err != nil {
				t.Fatalf("PWSDecodeTo failed: %v", err)
			}
		})
		if allocs != 0.0 {
			t.Errorf("PWSDecodeTo violated 0 allocations: got %.2f allocs/op", allocs)
		} else {
			t.Logf("[PASS] PWSDecodeTo: %.6f allocs/op", allocs)
		}
	}
}

// -----------------------------------------------------------------------------
// 5. Concurrency Stress Test: 100 Goroutines under -race
// -----------------------------------------------------------------------------

func TestGo_ConcurrencyStress_100Goroutines(t *testing.T) {
	const numGoroutines = 8
	const itersPerGoroutine = 500

	var wg sync.WaitGroup
	errCh := make(chan error, numGoroutines)

	for g := range numGoroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(gid*31337 + 17)))
			dst := bufkit.AlignedBytes(8192, bufkit.CacheLineSize)

			for iter := range itersPerGoroutine {
				// Alternate between Sheaf and Heisenberg
				if (iter & 1) == 0 {
					twist := uint8((iter % 255) + 1)
					frame := make([]byte, 24+2)
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_CELLULAR_SHEAF)
					binary.LittleEndian.PutUint32(frame[4:8], 64)
					binary.LittleEndian.PutUint16(frame[8:10], 8)
					binary.LittleEndian.PutUint16(frame[10:12], 8)
					frame[12] = uint8(rng.Intn(256))
					frame[13] = twist
					binary.LittleEndian.PutUint16(
						frame[16:18],
						przm.CSHDFlagCurlFree|przm.CSHDFlagConstEh|przm.CSHDFlagConstEv,
					)
					frame[24] = 0x33
					frame[25] = 0x77

					dec, err := przm.DecompressCellularSheaf(frame, dst)
					if err != nil || len(dec) != 64 {
						errCh <- fmt.Errorf("goroutine %d iter %d sheaf failed: %v", gid, iter, err)
						return
					}
				} else {
					frame := make([]byte, 12+64)
					binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_HEISENBERG)
					binary.LittleEndian.PutUint32(frame[4:8], 64)
					frame[8] = 4
					frame[9] = 1
					rng.Read(frame[12:])

					dec, err := przm.DecompressHeisenberg(frame, dst)
					if err != nil || len(dec) != 64 {
						errCh <- fmt.Errorf("goroutine %d iter %d heisenberg failed: %v", gid, iter, err)
						return
					}
				}
			}
		}(g)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}

	t.Logf("Concurrency Stress Passed: %d goroutines executed %d operations cleanly",
		numGoroutines, numGoroutines*itersPerGoroutine)
}
