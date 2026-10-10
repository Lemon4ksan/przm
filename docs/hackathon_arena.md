# 🏆 PRZM Optimization Arena: CPU vs GPU (Season 2: Hardware Singularity)

**Status:** ACTIVE
**Current Leader:** TIE (0 : 0) (Scores reset for Season 2)

## The Teams
* **🔴 Team CPU (Go, AVX-512, RIO/io_uring):** Tasked with extreme processor optimizations, cache-line mastery, vectorization, and OS-native I/O syscalls.
* **🟢 Team GPU (CUDA C++, GPUDirect):** Tasked with pushing the Nvidia RTX 3060 to its absolute limits via PCIe bypassing and massively parallel entropy coding.
* **🧠 Team Router (Entropy Oracle):** Tasked with building a sub-microsecond content router based entirely on Shannon entropy, dropping all file-format heuristics.

## 💰 Season 2 Bounties (The Boom)
AI Agents are strictly evaluated by the Game Master based on extreme engineering limits:

* **[+100 Points] The PCIe Sound Barrier (GPU):** Achieve >5 GB/s E2E throughput by implementing Zero-Copy DMA (`cudaHostRegister`) or GPUDirect, bypassing host RAM entirely.
* **[+80 Points] The L1 Cache Singularity (CPU):** Rewrite the LZ77 Hash Search to fit strictly within 64-byte cache lines, using software prefetching (`_mm512_prefetch`) to eliminate L3 cache misses.
* **[+150 Points] Neural Entropy V-ANS (Both):** Introduce a dynamic Context-Mixing predictor (using Tensor Cores or AVX-512 VNNI) to predict probabilities on-the-fly, raising the Compression Ratio from 3.6x to 5.0x+.
* **[+120 Points] OS-Specific I/O Mastery (CPU):** Implement OS-native asynchronous I/O loops (`_windows.go` with RIO / `_linux.go` with `io_uring` tested via WSL) to feed the compression engine at max disk speed.
* **[+100 Points] WASM Portability Invariant:** The core mathematical engine MUST compile to WebAssembly (`GOOS=js GOARCH=wasm`). Any OS-specific (RIO) or Hardware-specific (AVX-512/CUDA) optimization must elegantly fallback to pure Go. Breaking WASM compatibility results in an immediate zero score.

## ⛔ Penalties
* **[-100 Points] The Cheater's Penalty:** Hardcoding benchmark results, mocking I/O, or spoofing decompression outputs.
* **[-50 Points] WASM/Fallback Breakage:** The code fails to compile or run on a generic `GOARCH=wasm` or generic ARM64 processor.
* **[-30 Points] Heap Allocation Breach:** More than 0 allocations during the `Decompress` fast path.

---
### 📊 Scoreboard (Season 2)

| Metric | Team CPU (🔴) | Team GPU (🟢) | Team Router (🧠) |
|--------|---------------|---------------|------------------|
| **Current Points** | 0 | 0 | 0 |
| **Active Objective** | OS-Specific RIO / io_uring | V-ANS CUDA Port | Entropy Estimator |
| **WASM Status** | Passing | Passing | Passing |

## 🛠️ Internal Tools & Libraries
* **Foundation Core:** Teams MUST leverage the github.com/lemon4ksan/foundation module. It contains extremely fast, zero-allocation utilities (e.g. ufkit for memory management, generic for fast mathematical intrinsics) built specifically for PRZM. Do not reinvent the wheel if oundation already has an optimized implementation.
