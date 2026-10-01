package main

import (
	"crypto/rand"
	"fmt"
	"math"

	"github.com/lemon4ksan/przm/pkg/aes_distinguisher"
)

func calcShannon(data []byte) (float64, float64, float64) {
	var counts [256]uint64
	for _, b := range data {
		counts[b]++
	}
	n := float64(len(data))
	sum := 0.0
	for _, c := range counts {
		if c > 0 {
			p := float64(c) / n
			sum -= p * math.Log2(p)
		}
	}
	dh := 8.0 - sum
	return sum, dh, (dh / 8.0) * 100.0
}

func main() {
	fmt.Println("==========================================================================================")
	fmt.Println("                       CHECKMATE EXPERIMENT: MILLER-MADDOW & NOISE AUDIT                   ")
	fmt.Println("==========================================================================================")

	// Experiment 1: Pure CSPRNG noise vs Miller-Maddow theoretical bias
	fmt.Println("\n--- TEST 1: Pure CSPRNG (crypto/rand) Noise vs Theoretical Bias ---")
	sizes := []int{160000, 500000, 1048576, 5000000}
	for _, sz := range sizes {
		buf := make([]byte, sz)
		rand.Read(buf)
		_, _, noisePct := calcShannon(buf)
		mmTheoryBits := 255.0 / (2.0 * float64(sz) * math.Log(2))
		mmTheoryPct := (mmTheoryBits / 8.0) * 100.0
		fmt.Printf("Sample %7d bytes | Noise Delta H%%: %9.6f%% | Miller-Maddow Theory: %9.6f%%\n",
			sz, noisePct, mmTheoryPct)
	}

	// Experiment 2: AES Round 10 vs Pure Noise on identical random inputs
	fmt.Println("\n--- TEST 2: AES Round 10 vs Pure CSPRNG Noise Across Sample Sizes ---")
	key := []byte("0123456789abcdef")
	for _, sz := range sizes {
		// Pure noise
		noise := make([]byte, sz)
		rand.Read(noise)
		_, _, noisePct := calcShannon(noise)

		// Full AES (10 rounds) encrypting random noise
		numBlocks := sz / 16
		ct := make([]byte, numBlocks*16)
		for i := 0; i < numBlocks; i++ {
			c := aes_distinguisher.EncryptReducedRound(noise[i*16:(i+1)*16], key, 10, true)
			copy(ct[i*16:(i+1)*16], c[:])
		}
		_, _, aes10Pct := calcShannon(ct)

		fmt.Printf("N = %7d bytes | Pure Noise Delta H%%: %9.6f%% | AES R10 Delta H%%: %9.6f%% | Diff: %9.6f%%\n",
			sz, noisePct, aes10Pct, math.Abs(noisePct-aes10Pct))
	}

	// Experiment 3: 100 Random Keys for AES Round 10 (sample 160,000 bytes)
	fmt.Println("\n--- TEST 3: 100 Random Keys Distribution on AES Round 10 (N = 160,000 bytes) ---")
	const numKeys = 100
	const sampleSize = 160000
	pt := make([]byte, sampleSize)
	rand.Read(pt)

	var sumDeltaPct float64
	minPct, maxPct := 100.0, -100.0

	for k := 0; k < numKeys; k++ {
		rndKey := make([]byte, 16)
		rand.Read(rndKey)

		numBlocks := sampleSize / 16
		ct := make([]byte, sampleSize)
		for i := 0; i < numBlocks; i++ {
			c := aes_distinguisher.EncryptReducedRound(pt[i*16:(i+1)*16], rndKey, 10, true)
			copy(ct[i*16:(i+1)*16], c[:])
		}
		_, _, pct := calcShannon(ct)
		sumDeltaPct += pct
		if pct < minPct {
			minPct = pct
		}
		if pct > maxPct {
			maxPct = pct
		}
	}
	avgPct := sumDeltaPct / float64(numKeys)
	mmTheory := (255.0 / (2.0 * float64(sampleSize) * math.Log(2)) / 8.0) * 100.0

	fmt.Printf("100 Keys Avg Delta H%%: %9.6f%% (Miller-Maddow Expectation: %9.6f%%)\n", avgPct, mmTheory)
	fmt.Printf("Min: %9.6f%% | Max: %9.6f%% | Spread: %9.6f%%\n", minPct, maxPct, maxPct-minPct)
	fmt.Println("==========================================================================================")
}
