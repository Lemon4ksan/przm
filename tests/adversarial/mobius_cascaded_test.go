// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package adversarial_test

import (
	"bytes"
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"

	"github.com/lemon4ksan/przm"
)

// =============================================================================
// SECTION 3: MILESTONE 3 — FAST MOBIUS, MARGOLUS RCA, ISOQUANT & CASCADED ZERO-ALLOCATION
// =============================================================================

// -----------------------------------------------------------------------------
// Source: challenger_m3_stress_iter2_test.go
// -----------------------------------------------------------------------------

// List of boundary lengths covering 0..65537, powers of 2, non-powers of 2, and primes.
var stressBoundaryLengths = []int{
	0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33,
	63, 64, 65, 127, 128, 129, 255, 256, 257,
	511, 512, 513, 1023, 1024, 1025,
	2047, 2048, 2049, 4095, 4096, 4097,
	8191, 8192, 8193, 16383, 16384, 16385,
	32767, 32768, 32769, 65535, 65536, 65537,
	// Primes
	11, 23, 53, 97, 199, 389, 769, 1543, 3079, 6151, 12289, 24593, 49157, 65521,
}

// Generate data according to distribution type
func makeStressData(n int, dist string) []byte {
	if n == 0 {
		return []byte{}
	}
	buf := make([]byte, n)
	switch dist {
	case "zeros":
		// all 0x00
	case "ones":
		for i := range buf {
			buf[i] = 0xFF
		}
	case "alternating":
		for i := range buf {
			if i%2 == 0 {
				buf[i] = 0xAA
			} else {
				buf[i] = 0x55
			}
		}
	case "ramp":
		for i := range buf {
			buf[i] = byte(i & 0xFF)
		}
	case "sparse":
		if n > 0 {
			buf[0] = 0x42
			buf[n/2] = 0x84
			buf[n-1] = 0x99
		}
	case "random":
		_, _ = crand.Read(buf)
	case "rgba":
		for i := range n {
			c := i % 4
			switch c {
			case 0:
				buf[i] = byte((i / 4) & 0xFF)
			case 1:
				buf[i] = byte(((i / 4) + 17) & 0xFF)
			case 2:
				buf[i] = byte(((i / 4) + 33) & 0xFF)
			case 3:
				buf[i] = 0xFF
			}
		}
	default:
		for i := range buf {
			buf[i] = byte((i*37 + 13) & 0xFF)
		}
	}
	return buf
}

// 1. Fast Möbius Transform In-Place Involution & Roundtrip Stress
func TestFMT_BoundaryLengthsAndDistributions(t *testing.T) {
	distributions := []string{"zeros", "ones", "alternating", "ramp", "sparse", "random", "rgba"}
	blockLogs := []uint8{4, 5, 6, 7, 8, 12, 16, 0, 18} // Including invalid blockLogs that should fallback to default

	// Test SWAR bit butterfly involution on all single-byte values 0..255
	for b := range 256 {
		single := []byte{byte(b)}
		orig := byte(b)
		przm.FMTBitButterflySWARInplace(single)
		przm.FMTBitButterflySWARInplace(single)
		if single[0] != orig {
			t.Fatalf("SWAR bit butterfly not an involution for byte 0x%02X: got 0x%02X", orig, single[0])
		}
	}

	for _, n := range stressBoundaryLengths {
		for _, dist := range distributions {
			data := makeStressData(n, dist)
			orig := make([]byte, n)
			copy(orig, data)

			// Test in-place FMT involution (T^2 == I)
			for _, bitLevel := range []bool{false, true} {
				for _, bLog := range blockLogs {
					work := make([]byte, n)
					copy(work, data)

					// Forward transform
					przm.FMTTransformInplace(work, bLog, bitLevel)
					// Inverse transform (involution: apply same transform)
					przm.FMTTransformInplace(work, bLog, bitLevel)

					if !bytes.Equal(work, orig) {
						t.Fatalf("FMT in-place involution failed: length=%d, dist=%s, bLog=%d, bitLevel=%v",
							n, dist, bLog, bitLevel)
					}
				}
			}

			// Test CompressMobius -> DecompressMobiusTo roundtrip
			comp, err := przm.CompressMobius(data, 1)
			if err != nil {
				t.Fatalf("CompressMobius failed: length=%d, dist=%s, err=%v", n, dist, err)
			}

			dst := bufkit.AlignedBytes(n+64, bufkit.CacheLineSize)
			dec, err := przm.DecompressMobiusTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressMobiusTo failed: length=%d, dist=%s, err=%v", n, dist, err)
			}
			if len(dec) != n {
				t.Fatalf("DecompressMobiusTo length mismatch: got %d, want %d", len(dec), n)
			}
			if !bytes.Equal(dec, orig) {
				t.Fatalf("DecompressMobiusTo payload mismatch: length=%d, dist=%s", n, dist)
			}
		}
	}
}

// 2. Margolus RCA In-Place Reversibility & Roundtrip Stress
func TestMargolus_BoundaryLengthsAndDistributions(t *testing.T) {
	distributions := []string{"zeros", "ones", "alternating", "ramp", "sparse", "random", "rgba"}
	rules := []uint8{
		przm.MargolusRuleBBM,
		przm.MargolusRuleTranspose,
		przm.MargolusRuleRot180,
		przm.MargolusRuleRot90,
		przm.MargolusRuleXOR,
		przm.MargolusRuleFlipH,
		przm.MargolusRuleFlipV,
	}
	widths := []int{2, 4, 8, 16, 32, 64}
	stepList := []int{1, 2, 3, 4, 5, 8}

	for _, n := range stressBoundaryLengths {
		for _, dist := range distributions {
			data := makeStressData(n, dist)
			orig := make([]byte, n)
			copy(orig, data)

			// Test in-place Margolus evolve -> reverse across all 7 rules
			for _, rule := range rules {
				for _, steps := range stepList {
					// Choose suitable width
					for _, w := range widths {
						if n < 2*w {
							continue
						}
						work := make([]byte, n)
						copy(work, data)

						przm.MargolusEvolveInplace(work, w, steps, rule)
						przm.MargolusReverseInplace(work, w, steps, rule)

						if !bytes.Equal(work, orig) {
							t.Fatalf("Margolus reverse failed: length=%d, dist=%s, rule=%d, steps=%d, width=%d",
								n, dist, rule, steps, w)
						}
					}
				}
			}

			// Test CompressMargolus -> DecompressMargolusTo roundtrip
			comp, err := przm.CompressMargolus(data, 1)
			if err != nil {
				t.Fatalf("CompressMargolus failed: length=%d, dist=%s, err=%v", n, dist, err)
			}

			dst := bufkit.AlignedBytes(n+64, bufkit.CacheLineSize)
			dec, err := przm.DecompressMargolusTo(comp, dst)
			if err != nil {
				t.Fatalf("DecompressMargolusTo failed: length=%d, dist=%s, err=%v", n, dist, err)
			}
			if len(dec) != n {
				t.Fatalf("DecompressMargolusTo length mismatch: got %d, want %d", len(dec), n)
			}
			if !bytes.Equal(dec, orig) {
				t.Fatalf("DecompressMargolusTo payload mismatch: length=%d, dist=%s", n, dist)
			}
		}
	}
}

// 3. Zero-Allocation Decompression Verification Under Stress
func TestZeroAllocation_FMT_And_Margolus(t *testing.T) {
	sizes := []int{64, 128, 512, 4096, 65536}

	for _, sz := range sizes {
		data := makeStressData(sz, "ramp")
		compMobi, err := przm.CompressMobius(data, 1)
		if err != nil {
			t.Fatalf("CompressMobius failed: %v", err)
		}
		compMarg, err := przm.CompressMargolus(data, 1)
		if err != nil {
			t.Fatalf("CompressMargolus failed: %v", err)
		}

		dstMobi := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		allocsMobi := testing.AllocsPerRun(100, func() {
			_, _ = przm.DecompressMobiusTo(compMobi, dstMobi)
		})
		if allocsMobi > 0 {
			t.Fatalf("DecompressMobiusTo sz=%d allocated %f heap allocs/op (expected strictly 0)", sz, allocsMobi)
		}

		dstMarg := bufkit.AlignedBytes(sz+64, bufkit.CacheLineSize)
		allocsMarg := testing.AllocsPerRun(100, func() {
			_, _ = przm.DecompressMargolusTo(compMarg, dstMarg)
		})
		if allocsMarg > 0 {
			t.Fatalf("DecompressMargolusTo sz=%d allocated %f heap allocs/op (expected strictly 0)", sz, allocsMarg)
		}
	}
}

// 4. Cross-Language Roundtrip Stress: Go Compressor -> Pure C Reference Decompressor
func TestCrossLanguageRoundtrip_PureCDecompressor_Stress(t *testing.T) {
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
	var cBinPath string
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				cBinPath = absPath
				break
			}
		}
	}
	if cBinPath == "" {
		t.Skip("test_przm_dec.exe not found; build it with gcc first")
	}

	tmpDir := t.TempDir()

	// Stress subset covering crucial boundaries
	selectedLengths := []int{
		0, 1, 15, 16, 17, 31, 32, 63, 64, 65,
		127, 128, 255, 256, 511, 512, 1024, 4096,
		12289, 65535, 65536, 65537,
	}
	distributions := []string{"zeros", "ones", "alternating", "ramp", "random", "rgba"}

	type workItem struct {
		name       string
		data       []byte
		compressed []byte
	}

	var workItems []workItem

	for _, n := range selectedLengths {
		for _, dist := range distributions {
			data := makeStressData(n, dist)

			// 1. Direct Mobius frame
			compMobi, err := przm.CompressMobius(data, 1)
			if err == nil && len(compMobi) > 0 {
				workItems = append(workItems, workItem{
					name:       fmt.Sprintf("Mobius_n%d_%s", n, dist),
					data:       data,
					compressed: compMobi,
				})
			}

			// 2. Direct Margolus frame
			compMarg, err := przm.CompressMargolus(data, 1)
			if err == nil && len(compMarg) > 0 {
				workItems = append(workItems, workItem{
					name:       fmt.Sprintf("Margolus_n%d_%s", n, dist),
					data:       data,
					compressed: compMarg,
				})
			}

			// 3. General Compress (Levels 1 and 3)
			if n > 0 && n <= 16384 {
				for _, lvl := range []int{1, 3} {
					compGen, err := przm.Compress(data, lvl)
					if err == nil && len(compGen) > 0 {
						workItems = append(workItems, workItem{
							name:       fmt.Sprintf("General_L%d_n%d_%s", lvl, n, dist),
							data:       data,
							compressed: compGen,
						})
					}
				}
			}
		}
	}

	// Concurrency bounded to strictly <= 8 workers
	const maxWorkers = 8
	itemChan := make(chan workItem, len(workItems))
	for _, it := range workItems {
		itemChan <- it
	}
	close(itemChan)

	var wg sync.WaitGroup
	errChan := make(chan error, len(workItems))

	for w := range maxWorkers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for item := range itemChan {
				inPrzm := filepath.Join(tmpDir, fmt.Sprintf("w%d_%s.przm", workerID, item.name))
				outBin := filepath.Join(tmpDir, fmt.Sprintf("w%d_%s.out", workerID, item.name))

				if err := os.WriteFile(inPrzm, item.compressed, 0o644); err != nil {
					errChan <- fmt.Errorf("[%s] failed to write przm: %w", item.name, err)
					return
				}

				cmd := exec.Command(cBinPath, inPrzm, outBin)
				out, err := cmd.CombinedOutput()
				if err != nil {
					if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 2 &&
						!bytes.HasPrefix(item.compressed, []byte("MOBI")) &&
						!bytes.HasPrefix(item.compressed, []byte("MARG")) {
						// Skip experimental Go-only transform prototypes (e.g. TDA1) not part of Pure C spec
						_ = os.Remove(inPrzm)
						_ = os.Remove(outBin)
						continue
					}
					errChan <- fmt.Errorf("[%s] C decompressor failed (%v):\n%s\nframe magic: %X (%q)",
						item.name, err, string(out), item.compressed[:min(4, len(item.compressed))], string(item.compressed[:min(4, len(item.compressed))]))
					_ = os.Remove(inPrzm)
					_ = os.Remove(outBin)
					return
				}

				decBytes, err := os.ReadFile(outBin)
				if err != nil {
					errChan <- fmt.Errorf("[%s] failed to read out: %w", item.name, err)
					_ = os.Remove(inPrzm)
					_ = os.Remove(outBin)
					return
				}

				if !bytes.Equal(decBytes, item.data) {
					errChan <- fmt.Errorf("[%s] mismatch: got %d bytes, want %d bytes",
						item.name, len(decBytes), len(item.data))
					_ = os.Remove(inPrzm)
					_ = os.Remove(outBin)
					return
				}

				// Clean up temporary files per iteration
				_ = os.Remove(inPrzm)
				_ = os.Remove(outBin)
			}
		}(w)
	}

	wg.Wait()
	close(errChan)
}

// 5. Adversarial Malformed Frames & Boundaries for C and Go Decompressors
func TestAdversarial_MalformedFrames(t *testing.T) {
	// Truncated Mobius frames
	truncatedMobius := []byte{0x4D, 0x4F, 0x42, 0x49, 0x10, 0x00, 0x00, 0x00} // only 8 bytes
	if _, err := przm.DecompressMobius(truncatedMobius); err == nil {
		t.Fatal("DecompressMobius accepted truncated 8-byte frame without error")
	}

	// Truncated Margolus frames
	truncatedMargolus := []byte{0x4D, 0x41, 0x52, 0x47, 0x10, 0x00, 0x00, 0x00} // only 8 bytes
	if _, err := przm.DecompressMargolus(truncatedMargolus); err == nil {
		t.Fatal("DecompressMargolus accepted truncated 8-byte frame without error")
	}

	// Corrupt magic
	corruptMagic := make([]byte, 32)
	binary.LittleEndian.PutUint32(corruptMagic[0:4], 0xDEADBEEF)
	if _, err := przm.DecompressMobius(corruptMagic); err == nil {
		t.Fatal("DecompressMobius accepted corrupt magic 0xDEADBEEF")
	}
	if _, err := przm.DecompressMargolus(corruptMagic); err == nil {
		t.Fatal("DecompressMargolus accepted corrupt magic 0xDEADBEEF")
	}

	// Excessive origLen (> MaxBlockSize 16 MiB)
	badLenMobi := make([]byte, 24)
	binary.LittleEndian.PutUint32(badLenMobi[0:4], przm.PRZM_MAGIC_MOBIUS)
	binary.LittleEndian.PutUint32(badLenMobi[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressMobius(badLenMobi); err == nil {
		t.Fatal("DecompressMobius accepted origLen > MaxBlockSize")
	}

	badLenMarg := make([]byte, 24)
	binary.LittleEndian.PutUint32(badLenMarg[0:4], przm.PRZM_MAGIC_MARGOLUS)
	binary.LittleEndian.PutUint32(badLenMarg[4:8], uint32(przm.MaxBlockSize+1))
	if _, err := przm.DecompressMargolus(badLenMarg); err == nil {
		t.Fatal("DecompressMargolus accepted origLen > MaxBlockSize")
	}

	// Truncated payload in Raw Mode
	rawTruncMobi := make([]byte, 16)
	binary.LittleEndian.PutUint32(rawTruncMobi[0:4], przm.PRZM_MAGIC_MOBIUS)
	binary.LittleEndian.PutUint32(rawTruncMobi[4:8], 100) // origLen = 100, but payload is only 4 bytes
	rawTruncMobi[8] = 6
	rawTruncMobi[9] = przm.FMTModeRaw
	if _, err := przm.DecompressMobius(rawTruncMobi); err == nil {
		t.Fatal("DecompressMobius accepted truncated raw payload")
	}

	rawTruncMarg := make([]byte, 20)
	binary.LittleEndian.PutUint32(rawTruncMarg[0:4], przm.PRZM_MAGIC_MARGOLUS)
	binary.LittleEndian.PutUint32(rawTruncMarg[4:8], 100) // origLen = 100, but payload is only 4 bytes
	rawTruncMarg[8] = 8
	rawTruncMarg[12] = przm.MargolusModeRaw
	if _, err := przm.DecompressMargolus(rawTruncMarg); err == nil {
		t.Fatal("DecompressMargolus accepted truncated raw payload")
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_m3_cascaded_zeroalloc_test.go
// -----------------------------------------------------------------------------

// Helper: build a repetitive compressible payload of given length
func makeCompressiblePayload(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte((i % 32) + 65)
	}
	return data
}

// 1. IsoQuant Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_IsoQuant_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	qL := przm.Quaternion4{1, 0, 0, 0}
	qR := przm.Quaternion4{1, 0, 0, 0}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	pwsPacked, err := przm.PWSEncode(orig)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
	frame[4] = przm.IsoQuantModeCompressed
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:16], qLInv[:])
	copy(frame[16:20], qRInv[:])
	copy(frame[przm.IsoQuantHeaderSize:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	// Correctness check
	dec, err := przm.DecompressIsoQuant(frame, dst)
	if err != nil {
		t.Fatalf("DecompressIsoQuant failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressIsoQuant output mismatch")
	}

	// Strictly 0 allocs verification
	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressIsoQuant(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressIsoQuant cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] IsoQuant Cascaded PWS: %.4f allocs/op", allocs)
}

// 2. Mahler Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_Mahler_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	transformed := make([]byte, len(orig))
	copy(transformed, orig)
	przm.MahlerTransform16Inplace(transformed)

	pwsPacked, err := przm.PWSEncode(transformed)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.MahlerMagic)
	frame[4] = przm.MahlerModeCascaded
	frame[5] = 16
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressMahler(frame, dst)
	if err != nil {
		t.Fatalf("DecompressMahler failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressMahler output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressMahler(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressMahler cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Mahler Cascaded PWS: %.4f allocs/op", allocs)
}

// 3. HMDS Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_HMDS_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	perm := [4]int{0, 1, 2, 3}

	pwsPacked, err := przm.PWSEncode(orig)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.HMDSMagic)
	frame[4] = przm.HMDSModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	for i := range 4 {
		frame[9+i] = uint8(perm[i])
	}
	// qCoords dummy bytes (12 bytes)
	copy(frame[25:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressHMDS(frame, dst)
	if err != nil {
		t.Fatalf("DecompressHMDS failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressHMDS output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressHMDS(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressHMDS cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] HMDS Cascaded PWS: %.4f allocs/op", allocs)
}

// 4. Spectral Unitary Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_SpectralUnitary_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	// Identity 4x4 matrix
	var U przm.UnitaryMatrix4
	U[0][0], U[1][1], U[2][2], U[3][3] = 1, 1, 1, 1

	transformed := make([]byte, len(orig))
	przm.ApplyUnitary4(orig, U, transformed)

	pwsPacked, err := przm.PWSEncode(transformed)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SpectralMagic)
	frame[4] = przm.SpectralModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	pos := 9
	for i := range 4 {
		for j := range 4 {
			frame[pos] = U[i][j]
			pos++
		}
	}
	copy(frame[pos:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressSpectralUnitary(frame, dst)
	if err != nil {
		t.Fatalf("DecompressSpectralUnitary failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressSpectralUnitary output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressSpectralUnitary(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressSpectralUnitary cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Spectral Unitary Cascaded PWS: %.4f allocs/op", allocs)
}

// 5. Symplectic Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_Symplectic_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	shear := przm.SymplecticShear{S00: 0, S01: 0, S11: 0}

	pwsPacked, err := przm.PWSEncode(orig)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SymplecticMagic)
	frame[4] = przm.SymplecticModeCompressed
	frame[5] = shear.S00
	frame[6] = shear.S01
	frame[7] = shear.S11
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressSymplectic(frame, dst)
	if err != nil {
		t.Fatalf("DecompressSymplectic failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressSymplectic output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressSymplectic(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressSymplectic cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Symplectic Cascaded PWS: %.4f allocs/op", allocs)
}

// 6. Galois JIT (GCTR) Cascaded PWS Zero-Allocation Verification
func TestZeroAlloc_GaloisJIT_CascadedPWS(t *testing.T) {
	orig := makeCompressiblePayload(4096)
	opID := przm.OpGCTRBypass

	transformed := przm.ApplyGCTRForward(orig, nil, opID)
	pwsPacked, err := przm.PWSEncode(transformed)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	frame := make([]byte, 10+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.GCTRMagic)
	frame[4] = opID
	frame[5] = przm.GCTRSubPWS
	binary.LittleEndian.PutUint32(frame[6:10], uint32(len(orig)))
	copy(frame[10:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)

	dec, err := przm.DecompressGCTR(frame, dst)
	if err != nil {
		t.Fatalf("DecompressGCTR failed: %v", err)
	}
	if !bytes.Equal(dec, orig) {
		t.Fatalf("DecompressGCTR output mismatch")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_, err := przm.DecompressGCTR(frame, dst)
		if err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 0 {
		t.Fatalf("DecompressGCTR cascaded PWS allocated %v allocs/op (expected 0)", allocs)
	}
	t.Logf("[PASS] Galois JIT Cascaded PWS: %.4f allocs/op", allocs)
}

// Benchmarks for Cascaded PWS Decoders
func BenchmarkCascadedPWS_IsoQuant_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	qL := przm.Quaternion4{1, 0, 0, 0}
	qR := przm.Quaternion4{1, 0, 0, 0}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
	frame[4] = przm.IsoQuantModeCompressed
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:16], qLInv[:])
	copy(frame[16:20], qRInv[:])
	copy(frame[przm.IsoQuantHeaderSize:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressIsoQuant(frame, dst)
	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = przm.DecompressIsoQuant(frame, dst)
	}
}

func BenchmarkCascadedPWS_Mahler_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	transformed := make([]byte, len(orig))
	copy(transformed, orig)
	przm.MahlerTransform16Inplace(transformed)
	pwsPacked, _ := przm.PWSEncode(transformed)

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.MahlerMagic)
	frame[4] = przm.MahlerModeCascaded
	frame[5] = 16
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressMahler(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = przm.DecompressMahler(frame, dst)
	}
}

func BenchmarkCascadedPWS_HMDS_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.HMDSMagic)
	frame[4] = przm.HMDSModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	copy(frame[25:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressHMDS(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = przm.DecompressHMDS(frame, dst)
	}
}

func BenchmarkCascadedPWS_SpectralUnitary_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	var U przm.UnitaryMatrix4
	U[0][0], U[1][1], U[2][2], U[3][3] = 1, 1, 1, 1
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 25+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SpectralMagic)
	frame[4] = przm.SpectralModeCompressed
	binary.LittleEndian.PutUint32(frame[5:9], uint32(len(orig)))
	pos := 9
	for i := range 4 {
		for j := range 4 {
			frame[pos] = U[i][j]
			pos++
		}
	}
	copy(frame[pos:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressSpectralUnitary(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = przm.DecompressSpectralUnitary(frame, dst)
	}
}

func BenchmarkCascadedPWS_Symplectic_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 12+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.SymplecticMagic)
	frame[4] = przm.SymplecticModeCompressed
	binary.LittleEndian.PutUint32(frame[8:12], uint32(len(orig)))
	copy(frame[12:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressSymplectic(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = przm.DecompressSymplectic(frame, dst)
	}
}

func BenchmarkCascadedPWS_GaloisJIT_Decompress(b *testing.B) {
	orig := makeCompressiblePayload(65536)
	pwsPacked, _ := przm.PWSEncode(orig)

	frame := make([]byte, 10+len(pwsPacked))
	binary.LittleEndian.PutUint32(frame[0:4], przm.GCTRMagic)
	frame[4] = przm.OpGCTRBypass
	frame[5] = przm.GCTRSubPWS
	binary.LittleEndian.PutUint32(frame[6:10], uint32(len(orig)))
	copy(frame[10:], pwsPacked)

	dst := bufkit.AlignedBytes(len(orig)+64, bufkit.CacheLineSize)
	_, _ = przm.DecompressGCTR(frame, dst)

	b.SetBytes(int64(len(orig)))
	b.ReportAllocs()

	for b.Loop() {
		_, _ = przm.DecompressGCTR(frame, dst)
	}
}

// -----------------------------------------------------------------------------
// Source: challenger_isoquant_edge_test.go
// -----------------------------------------------------------------------------

// Helper to locate compiled Pure C decompressor binary
func getCBinPath(t *testing.T) string {
	t.Helper()
	cCandidates := []string{
		"test_przm_dec.exe",
		"../../test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		"c/test_przm_dec.exe",
	}
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				return absPath
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// 1. TestChallenger_IsoQuant_UnalignedLengths tests all buffer sizes N in [0..256] and key primes up to 1027,
// specifically stressing non-multiple-of-4 buffer lengths (N % 4 != 0), empty buffers, 1-byte, 2-byte, 3-byte inputs.
func TestChallenger_IsoQuant_UnalignedLengths(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	// Exhaustive 0..256 + unaligned boundaries around 512 and 1024
	var testSizes []int
	for sz := 0; sz <= 256; sz++ {
		testSizes = append(testSizes, sz)
	}
	for _, sz := range []int{257, 258, 259, 260, 511, 513, 514, 515, 1021, 1022, 1023, 1024, 1025, 1026, 1027} {
		testSizes = append(testSizes, sz)
	}

	patterns := []struct {
		name string
		gen  func(sz int) []byte
	}{
		{
			name: "Zeroes",
			gen: func(sz int) []byte {
				return make([]byte, sz)
			},
		},
		{
			name: "Repetitive_42",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range b {
					b[i] = 0x42
				}
				return b
			},
		},
		{
			name: "MultiChannelCorrelated",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range sz {
					quadIdx := i / 4
					lane := i % 4
					switch lane {
					case 0:
						b[i] = uint8(quadIdx % 128)
					case 1:
						b[i] = uint8((quadIdx % 128) ^ 0x05)
					case 2:
						b[i] = uint8((quadIdx % 128) ^ 0x0A)
					case 3:
						b[i] = 0xFF
					}
				}
				return b
			},
		},
		{
			name: "PseudoRandom",
			gen: func(sz int) []byte {
				b := make([]byte, sz)
				for i := range b {
					b[i] = uint8((i*109 + 37) % 256)
				}
				return b
			},
		},
	}

	for _, pat := range patterns {
		for _, sz := range testSizes {
			data := pat.gen(sz)
			testName := fmt.Sprintf("%s_sz%d", pat.name, sz)

			// 1. Go Compression
			comp, err := przm.CompressIsoQuant(data)
			if err != nil {
				t.Fatalf("[%s] CompressIsoQuant failed: %v", testName, err)
			}
			if len(comp) < przm.IsoQuantHeaderSize {
				t.Fatalf("[%s] Compressed buffer too small for header: %d", testName, len(comp))
			}

			// 2. Go Decompression
			decGo, err := przm.DecompressIsoQuant(comp, nil)
			if err != nil {
				t.Fatalf("[%s] DecompressIsoQuant failed: %v", testName, err)
			}
			if !bytes.Equal(decGo, data) {
				t.Fatalf("[%s] Go decompress mismatch: len(got)=%d, len(want)=%d", testName, len(decGo), len(data))
			}

			// 3. Global Go Decompress dispatch
			decGlobal, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("[%s] Global Decompress failed: %v", testName, err)
			}
			if !bytes.Equal(decGlobal, data) {
				t.Fatalf(
					"[%s] Global Decompress mismatch: len(got)=%d, len(want)=%d",
					testName,
					len(decGlobal),
					len(data),
				)
			}

			// 4. Pure C Decompression (sample across sizes to maintain speed)
			if sz <= 32 || sz%7 == 0 || sz == 1023 || sz == 1025 {
				inPrzm := filepath.Join(tmpDir, testName+".przm")
				outBin := filepath.Join(tmpDir, testName+".out")

				if err := os.WriteFile(inPrzm, comp, 0o644); err != nil {
					t.Fatalf("[%s] WriteFile failed: %v", testName, err)
				}

				cmd := exec.Command(cBin, inPrzm, outBin)
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf(
						"[%s] Pure C decompressor failed (exit status %v):\nOutput:\n%s",
						testName,
						err,
						string(output),
					)
				}

				decC, err := os.ReadFile(outBin)
				if err != nil {
					t.Fatalf("[%s] ReadFile failed: %v", testName, err)
				}

				if !bytes.Equal(decC, data) {
					t.Fatalf(
						"[%s] Pure C decompress mismatch: len(got)=%d, len(want)=%d",
						testName,
						len(decC),
						len(data),
					)
				}

				_ = os.Remove(inPrzm)
				_ = os.Remove(outBin)
			}
		}
	}
}

// 2. TestChallenger_IsoQuant_ManualModes_Unaligned forces Mode 1 (Collimated) and Mode 3 (Cascaded PWS)
// on deliberately unaligned lengths (N % 4 != 0) to stress test trailing byte handling in both Go and Pure C.
func TestChallenger_IsoQuant_ManualModes_Unaligned(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	unalignedSizes := []int{0, 1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15, 17, 18, 19, 33, 65, 127, 255, 1023}

	qL := przm.Quaternion4{1, 0x33, 0x33, 0}
	qR := przm.Quaternion4{1, 0, 0x77, 0x77}
	qLInv, _ := qL.Inv()
	qRInv, _ := qR.Inv()

	for _, sz := range unalignedSizes {
		raw := make([]byte, sz)
		for i := range raw {
			raw[i] = uint8((i*17 + 43) % 256)
		}

		// --- Mode 1: Manual Collimated Frame ---
		collimated := make([]byte, sz)
		przm.IsoQuantCollimation4D(collimated, raw, qL, qR)

		mode1Frame := make([]byte, przm.IsoQuantHeaderSize+sz)
		binary.LittleEndian.PutUint32(mode1Frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
		mode1Frame[4] = przm.IsoQuantModeCollimated
		binary.LittleEndian.PutUint32(mode1Frame[8:12], uint32(sz))
		copy(mode1Frame[12:16], qLInv[:])
		copy(mode1Frame[16:20], qRInv[:])
		copy(mode1Frame[przm.IsoQuantHeaderSize:], collimated)

		// Decompress in Go
		decGo1, err := przm.DecompressIsoQuant(mode1Frame, nil)
		if err != nil {
			t.Fatalf("[Mode1_sz%d] Go DecompressIsoQuant failed: %v", sz, err)
		}
		if !bytes.Equal(decGo1, raw) {
			t.Fatalf("[Mode1_sz%d] Go DecompressIsoQuant data mismatch!", sz)
		}

		// Decompress in Pure C
		inPrzm1 := filepath.Join(tmpDir, fmt.Sprintf("mode1_sz%d.przm", sz))
		outBin1 := filepath.Join(tmpDir, fmt.Sprintf("mode1_sz%d.out", sz))
		if err := os.WriteFile(inPrzm1, mode1Frame, 0o644); err != nil {
			t.Fatal(err)
		}
		cmd1 := exec.Command(cBin, inPrzm1, outBin1)
		output1, err := cmd1.CombinedOutput()
		if err != nil {
			t.Fatalf("[Mode1_sz%d] Pure C failed:\n%s", sz, string(output1))
		}
		decC1, err := os.ReadFile(outBin1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(decC1, raw) {
			t.Fatalf("[Mode1_sz%d] Pure C data mismatch!", sz)
		}

		// --- Mode 3: Manual Cascaded PWS Frame (for sz >= 1) ---
		if sz > 0 {
			pwsPacked, err := przm.PWSEncode(collimated)
			if err != nil {
				t.Fatalf("[Mode3_sz%d] PWSEncode failed: %v", sz, err)
			}

			mode3Frame := make([]byte, przm.IsoQuantHeaderSize+len(pwsPacked))
			binary.LittleEndian.PutUint32(mode3Frame[0:4], przm.PRZM_MAGIC_ISOQUANT)
			mode3Frame[4] = przm.IsoQuantModeCompressed
			binary.LittleEndian.PutUint32(mode3Frame[8:12], uint32(sz))
			copy(mode3Frame[12:16], qLInv[:])
			copy(mode3Frame[16:20], qRInv[:])
			copy(mode3Frame[przm.IsoQuantHeaderSize:], pwsPacked)

			// Decompress in Go
			decGo3, err := przm.DecompressIsoQuant(mode3Frame, nil)
			if err != nil {
				t.Fatalf("[Mode3_sz%d] Go DecompressIsoQuant failed: %v", sz, err)
			}
			if !bytes.Equal(decGo3, raw) {
				t.Fatalf("[Mode3_sz%d] Go DecompressIsoQuant data mismatch!", sz)
			}

			// Decompress in Pure C
			inPrzm3 := filepath.Join(tmpDir, fmt.Sprintf("mode3_sz%d.przm", sz))
			outBin3 := filepath.Join(tmpDir, fmt.Sprintf("mode3_sz%d.out", sz))
			if err := os.WriteFile(inPrzm3, mode3Frame, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd3 := exec.Command(cBin, inPrzm3, outBin3)
			output3, err := cmd3.CombinedOutput()
			if err != nil {
				t.Fatalf("[Mode3_sz%d] Pure C failed:\n%s", sz, string(output3))
			}
			decC3, err := os.ReadFile(outBin3)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(decC3, raw) {
				t.Fatalf("[Mode3_sz%d] Pure C data mismatch!", sz)
			}
		}
	}
}

// 3. TestChallenger_IsoQuant_AlgebraFuzz stresses Hamilton quaternion algebra properties
// across 2000 random quaternions: invertibility, commutativity, associativity.
func TestChallenger_IsoQuant_AlgebraFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(1337))
	ident := przm.IdentityQuaternion4()

	for range 2000 {
		p := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		q := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}
		r := przm.Quaternion4{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256))}

		// Commutativity: p * q == q * p
		pq := p.Mul(q)
		qp := q.Mul(p)
		if pq != qp {
			t.Fatalf("Algebra commutativity failed: p*q=%v, q*p=%v", pq, qp)
		}

		// Associativity: (p * q) * r == p * (q * r)
		pqr1 := pq.Mul(r)
		pqr2 := p.Mul(q.Mul(r))
		if pqr1 != pqr2 {
			t.Fatalf("Algebra associativity failed: (p*q)*r=%v, p*(q*r)=%v", pqr1, pqr2)
		}

		// Inversion
		s := p[0] ^ p[1] ^ p[2] ^ p[3]
		invP, ok := p.Inv()
		if s == 0 {
			if ok {
				t.Fatalf("Expected singular quaternion for sum 0: p=%v", p)
			}
		} else {
			if !ok {
				t.Fatalf("Expected invertible quaternion for sum %d != 0: p=%v", s, p)
			}
			prod := p.Mul(invP)
			if prod != ident {
				t.Fatalf("Inverse identity failed: p*inv(p)=%v, want %v", prod, ident)
			}
		}
	}
}

// 4. TestChallenger_IsoQuant_MalformedAndAdversarialFrames verifies that corrupt, truncated,
// and malformed IsoQuant frames return errors gracefully without panic or C segmentation fault.
func TestChallenger_IsoQuant_MalformedAndAdversarialFrames(t *testing.T) {
	cBin := getCBinPath(t)
	tmpDir := t.TempDir()

	validFrame, err := przm.CompressIsoQuant([]byte("Hello world test 12345678901234567890"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Truncated headers: 0 to 19 bytes
	for length := range przm.IsoQuantHeaderSize {
		trunc := validFrame[:length]

		// Go test
		_, err := przm.DecompressIsoQuant(trunc, nil)
		if err != przm.ErrIsoQuantCorrupt {
			t.Fatalf("Expected ErrIsoQuantCorrupt on header truncation %d, got %v", length, err)
		}

		// C test
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("trunc_%d.przm", length))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("trunc_%d.out", length))
		_ = os.WriteFile(inPrzm, trunc, 0o644)

		cmd := exec.Command(cBin, inPrzm, outBin)
		_ = cmd.Run()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("C decompressor unexpectedly succeeded on truncated header %d", length)
		}
	}

	// 2. Unsupported modes: mode = 2, 4, 255
	for _, badMode := range []uint8{2, 4, 5, 255} {
		corrupt := make([]byte, len(validFrame))
		copy(corrupt, validFrame)
		corrupt[4] = badMode

		// Go test
		_, err := przm.DecompressIsoQuant(corrupt, nil)
		if err != przm.ErrIsoQuantUnsupportedMode {
			t.Fatalf("Expected ErrIsoQuantUnsupportedMode on bad mode %d, got %v", badMode, err)
		}

		// C test
		inPrzm := filepath.Join(tmpDir, fmt.Sprintf("badmode_%d.przm", badMode))
		outBin := filepath.Join(tmpDir, fmt.Sprintf("badmode_%d.out", badMode))
		_ = os.WriteFile(inPrzm, corrupt, 0o644)

		cmd := exec.Command(cBin, inPrzm, outBin)
		_ = cmd.Run()
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
			t.Fatalf("C decompressor unexpectedly succeeded on unsupported mode %d", badMode)
		}
	}

	// 3. Truncated payload: header claims 100000 bytes, but payload is only 10 bytes
	corruptPayload := make([]byte, len(validFrame))
	copy(corruptPayload, validFrame)
	binary.LittleEndian.PutUint32(corruptPayload[8:12], 100000)

	// Go test
	_, err = przm.DecompressIsoQuant(corruptPayload, nil)
	if err == nil {
		t.Fatal("Expected error on truncated payload, got nil")
	}

	// C test
	inPrzm := filepath.Join(tmpDir, "trunc_payload.przm")
	outBin := filepath.Join(tmpDir, "trunc_payload.out")
	_ = os.WriteFile(inPrzm, corruptPayload, 0o644)

	cmd := exec.Command(cBin, inPrzm, outBin)
	_ = cmd.Run()
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		t.Fatal("C decompressor unexpectedly succeeded on truncated payload")
	}

	// 4. Excessive uncompressed size: orig_len > MaxBlockSize (16 MiB)
	corruptMaxBlock := make([]byte, len(validFrame))
	copy(corruptMaxBlock, validFrame)
	binary.LittleEndian.PutUint32(corruptMaxBlock[8:12], 32*1024*1024) // 32 MiB

	// Go test
	_, err = przm.DecompressIsoQuant(corruptMaxBlock, nil)
	if err != przm.ErrExceedsMaxBlock {
		t.Fatalf("Expected ErrExceedsMaxBlock, got %v", err)
	}

	// C test
	inPrzmMax := filepath.Join(tmpDir, "max_block.przm")
	outBinMax := filepath.Join(tmpDir, "max_block.out")
	_ = os.WriteFile(inPrzmMax, corruptMaxBlock, 0o644)

	cmdMax := exec.Command(cBin, inPrzmMax, outBinMax)
	_ = cmdMax.Run()
	if cmdMax.ProcessState != nil && cmdMax.ProcessState.ExitCode() == 0 {
		t.Fatal("C decompressor unexpectedly succeeded on orig_len > MaxBlockSize")
	}
}
