// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"testing"
	"time"
)

func computeShannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var freq [256]int
	for _, b := range data {
		freq[b]++
	}
	n := float64(len(data))
	var ent float64
	for _, count := range freq {
		if count > 0 {
			p := float64(count) / n
			ent -= p * math.Log2(p)
		}
	}
	return ent
}

func generateFrontierCorpus() map[string][]byte {
	corpus := make(map[string][]byte)
	const sz = 65536 // 64 KiB L2 cache block

	// 1. x86_machine_code: Read from seal.exe or synthetic machine code if not present
	if bin, err := os.ReadFile("C:\\msys64\\usr\\bin\\bash.exe"); err == nil && len(bin) >= sz {
		corpus["x86_machine_code"] = bin[1024 : 1024+sz]
	} else if bin, err := os.ReadFile("../seal/seal.exe"); err == nil && len(bin) >= sz {
		corpus["x86_machine_code"] = bin[1024 : 1024+sz]
	} else {
		mCode := make([]byte, sz)
		rng := rand.New(rand.NewSource(101))
		for i := 0; i < sz; i += 4 {
			op := byte(rng.Intn(256))
			if rng.Float64() < 0.25 {
				op = 0xE8 // CALL
			} else if rng.Float64() < 0.15 {
				op = 0x89 // MOV
			}
			mCode[i] = op
			binary.LittleEndian.PutUint32(mCode[i:], uint32(rng.Int31()))
		}
		corpus["x86_machine_code"] = mCode
	}

	// 2. go_source_code: Read from compress.go
	if src, err := os.ReadFile("compress.go"); err == nil && len(src) >= sz/2 {
		repeated := make([]byte, sz)
		for i := 0; i < sz; i += len(src) {
			copy(repeated[i:], src)
		}
		corpus["go_source_code"] = repeated[:sz]
	} else {
		txt := make([]byte, sz)
		rng := rand.New(rand.NewSource(202))
		words := []string{"func ", "return ", "err ", "nil", "int", "string", "package ", "type ", "struct ", "{\n", "}\n"}
		pos := 0
		for pos < sz {
			w := words[rng.Intn(len(words))]
			copy(txt[pos:], []byte(w))
			pos += len(w)
		}
		corpus["go_source_code"] = txt
	}

	// 3. tabular_numeric: Sensor/telemetry/timestamps/offsets
	tab := make([]byte, sz)
	for i := 0; i < sz/8; i++ {
		ts := uint32(1700000000 + i*5)
		val := uint32(2000 + (i%100)*3)
		binary.LittleEndian.PutUint32(tab[i*8:], ts)
		binary.LittleEndian.PutUint32(tab[i*8+4:], val)
	}
	corpus["tabular_numeric"] = tab

	// 4. structured_json
	jsonBytes := make([]byte, sz)
	pos := 0
	rng := rand.New(rand.NewSource(303))
	for pos+64 < sz {
		entry := fmt.Sprintf(`{"id":%d,"tag":"item_%04d","score":%04d},`, pos/64, rng.Intn(100), rng.Intn(1000))
		copy(jsonBytes[pos:], []byte(entry))
		pos += len(entry)
	}
	corpus["structured_json"] = jsonBytes

	// 5. high_entropy_pack (pre-compressed random)
	noise := make([]byte, sz)
	rngNoise := rand.New(rand.NewSource(404))
	rngNoise.Read(noise)
	corpus["high_entropy_pack"] = noise

	return corpus
}

func TestFrontiers_EmpiricalComparison(t *testing.T) {
	corpus := generateFrontierCorpus()

	fmt.Println("\n==========================================================================================================================")
	fmt.Printf("%-18s | %-10s | %-12s | %-12s | %-12s | %-12s | %-14s\n",
		"Dataset (64 KB)", "Metric", "Raw Base", "Bit-Plane", "Mod-Recurr", "Micro-Pred", "Best Transform")
	fmt.Println("--------------------------------------------------------------------------------------------------------------------------")

	for _, name := range []string{"x86_machine_code", "go_source_code", "tabular_numeric", "structured_json", "high_entropy_pack"} {
		raw := corpus[name]
		baseEnt := computeShannonEntropy(raw)

		// 1. Bit-Plane Slicing
		t0 := time.Now()
		bpsOut := BitPlaneSliceForward(raw, nil)
		bpsSpeed := float64(len(raw)) / (1024 * 1024 * time.Since(t0).Seconds())
		bpsEnt := computeShannonEntropy(bpsOut)
		bpsInv := BitPlaneSliceInverse(bpsOut, nil)
		_ = bpsInv

		// 2. Modular Recurrence
		c1, c2, _ := EstimateModularRecurrence(raw)
		if c1 == 0 && c2 == 0 {
			c1 = 1 // default delta
		}
		t1 := time.Now()
		mlsrOut := ApplyModularRecurrenceForward(raw, nil, c1, c2)
		mlsrSpeed := float64(len(raw)) / (1024 * 1024 * time.Since(t1).Seconds())
		mlsrEnt := computeShannonEntropy(mlsrOut)

		// 3. Micro-Predictor
		t2 := time.Now()
		lmsOut := ApplyMicroPredictorForward(raw, nil)
		lmsSpeed := float64(len(raw)) / (1024 * 1024 * time.Since(t2).Seconds())
		lmsEnt := computeShannonEntropy(lmsOut)

		// Test V-ANS compression on best
		vBase, _ := VANSEncode4x(raw)
		vBps, _ := VANSEncode4x(bpsOut)
		vMlsr, _ := VANSEncode4x(mlsrOut)
		vLms, _ := VANSEncode4x(lmsOut)

		bestEnt := math.Min(math.Min(bpsEnt, mlsrEnt), lmsEnt)
		bestWinner := "None (Bypass)"
		if bestEnt < baseEnt-0.10 {
			if bestEnt == bpsEnt {
				bestWinner = fmt.Sprintf("Bit-Plane (-%.2f)", baseEnt-bpsEnt)
			} else if bestEnt == mlsrEnt {
				bestWinner = fmt.Sprintf("Mod-Recurr (-%.2f)", baseEnt-mlsrEnt)
			} else if bestEnt == lmsEnt {
				bestWinner = fmt.Sprintf("Micro-Pred (-%.2f)", baseEnt-lmsEnt)
			}
		}

		fmt.Printf("%-18s | Entropy    | %6.3f b/B  | %6.3f b/B  | %6.3f b/B  | %6.3f b/B  | %-14s\n",
			name, baseEnt, bpsEnt, mlsrEnt, lmsEnt, bestWinner)
		fmt.Printf("%-18s | V-ANS Size | %6d B   | %6d B   | %6d B   | %6d B   | Gain: %+.1f%%\n",
			"", len(vBase), len(vBps), len(vMlsr), len(vLms),
			float64(len(vBase)-minLen(len(vBps), len(vMlsr), len(vLms)))/float64(len(vBase))*100.0)
		fmt.Printf("%-18s | Speed      |     -      | %5.0f MB/s  | %5.0f MB/s  | %5.0f MB/s  | (In-Register)\n",
			"", bpsSpeed, mlsrSpeed, lmsSpeed)
		fmt.Println("--------------------------------------------------------------------------------------------------------------------------")
	}
}

func minLen(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

func BenchmarkFrontiers_BitPlaneForward(b *testing.B) {
	data := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BitPlaneSliceForward(data, dst)
	}
}

func BenchmarkFrontiers_BitPlaneInverse(b *testing.B) {
	data := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		BitPlaneSliceInverse(data, dst)
	}
}

func BenchmarkFrontiers_ModularRecurrenceForward(b *testing.B) {
	data := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyModularRecurrenceForward(data, dst, 2, 255)
	}
}

func BenchmarkFrontiers_MicroPredictorForward(b *testing.B) {
	data := make([]byte, 65536)
	dst := make([]byte, 65536)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ApplyMicroPredictorForward(data, dst)
	}
}

func TestBitPlane_PerPlaneEntropy(t *testing.T) {
	corpus := generateFrontierCorpus()
	fmt.Println("\n=== Bit-Plane Slicing: Per-Plane Entropy Breakdown (64 KB) ===")
	for _, name := range []string{"go_source_code", "x86_machine_code", "tabular_numeric", "structured_json"} {
		raw := corpus[name]
		num8 := len(raw) / 8
		bpsOut := BitPlaneSliceForward(raw, nil)

		fmt.Printf("\nDataset: %s (Raw Base: %.3f b/B)\n", name, computeShannonEntropy(raw))
		fmt.Printf("Plane | Type      | Entropy (b/B) | Zero Ratio | Comment\n")
		fmt.Printf("------+-----------+---------------+------------+------------------------\n")
		var sumBits float64
		for p := 7; p >= 0; p-- {
			planeBytes := bpsOut[p*num8 : (p+1)*num8]
			ent := computeShannonEntropy(planeBytes)
			zeros := 0
			for _, b := range planeBytes {
				if b == 0 {
					zeros++
				}
			}
			zRatio := float64(zeros) / float64(len(planeBytes)) * 100.0
			sumBits += ent
			comment := ""
			if zRatio > 99.0 {
				comment = "==> 99%+ Zero (Collapses completely!)"
			} else if zRatio > 50.0 {
				comment = "High zero density"
			}
			fmt.Printf("  %d   | Bit %d (MSB) | %6.3f b/B   | %5.1f%%     | %s\n", p, p, ent, zRatio, comment)
		}
		fmt.Printf("Average Bit-Plane Entropy: %.3f b/B (Sum: %.2f bits / 8 = %.3f b/B)\n", sumBits/8.0, sumBits, sumBits/8.0)
	}
}

