# PRZM: Autonomous Geometric Archival Compression

[English](README.md) | [Русский](README_RU.md)

[![Language](https://img.shields.io/badge/Language-C11%20%7C%20Go%201.27%20%7C%20CUDA%20sm__86-00ADD8.svg)](https://github.com/lemon4ksan/przm)
[![License](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Zero-Alloc](https://img.shields.io/badge/Zero--Heap--Alloc-0%20B%2Fop%20%7C%200%20allocs%2Fop-brightgreen.svg)]()
[![Throughput](https://img.shields.io/badge/Decompress%20Throughput-4.57--6.16%20GB%2Fs%20(Line--Rate)-orange.svg)]()
[![Formal Verification](https://img.shields.io/badge/Lean%204-0%20sorry%20(33%20Modules%20Verified)-success.svg)](proofs/)

> **"True balance is neither the cold silence of maximum entropy, nor the frozen immobility of a crystal. It is the dynamic, vibrating tension held on the razor-thin boundary between order and chaos.**
> 
> **Information in the cosmos is never destroyed; it merely disperses across coordinate charts we fail to perceive. When the observer chooses the intrinsic geometry of the data manifold, the illusion of randomness collapses into deterministic harmony. Compression is not the erasure of substance, but the mathematical revelation of universal symmetry."**
> — *The PRZM Philosophy on Universal Balance*

## Overview: The v0.8.0 Architecture

**PRZM** (PrismLZ) is an ultra-high-speed, mathematically unified archival compression system engineered to transcend classical 1D sliding-window limits (such as Zstandard, LZMA, and Brotli) by mapping non-linear digital data distributions onto curved discrete manifolds.

PRZM couples a production-grade **Go 1.27** core with hardware-accelerated **NVIDIA CUDA kernels (`gpu/`)** and a standalone, dependency-free **Pure C99/C11 Zero-Allocation Decompressor (`libprzm_dec`)**. It is designed for line-rate integration into high-performance systems: game engines (Unreal Engine, Unity, Godot), GPU streaming pipelines, asset loaders, database storage engines, and cross-language FFI bindings (Rust, C++, Zig, Python).

## Architectural Highlights & Invariant Guarantees

1. **Strict Zero-Allocation Contract**: Strictly **0 dynamic heap allocations** (`0 B/op, 0 allocs/op`, zero `malloc`/`free` calls during streaming decompression) across both Go (`DecompressTo`) and Pure C (`przm_decompress`). Memory is managed through caller-provided fixed buffers or cache-aligned stack arenas.
2. **Microarchitectural Line-Rate Throughput**: Decompression operates at **4.57–6.16 GB/s** on standard fused frames (and up to **58.7 GB/s** on specialized algebraic manifolds), consuming $O(1)$ CPU clock cycles per vector block and saturating hardware memory bus bandwidth.
3. **Hardware CUDA GPU Acceleration (`gpu/`)**: Native zero-CGo integration with the NVIDIA Driver API (`gpu/bridge.go`) executing hand-crafted PTX kernels (`sm_86` Ampere, CUDA 13.4). Delivers **40+ GB/s parallel LZ77 matching**, Warp Tokenization, and zero-copy Device-to-Device (D2D) compression directly in GPU VRAM with seamless fallback on CPU-only hosts.
4. **Multi-Scale Trellis RDO Engine**: Replaces greedy match selection with a forward dynamic programming search on a directed acyclic graph (DAG). Uses an exact bit-cost function balancing literal runs, match lengths, and a 4-register LRU distance cache to eliminate offset overhead.
5. **Mode 8 Tri-Stream Demux Architecture**: Decouples the command stream into 3 independent channels (`opStream`, `metaStream`, and `varStream`). Uniform dictionary indices and extended lengths are isolated from low-entropy match structures, completely eliminating entropy table pollution.
6. **Non-Archimedean $p$-Adic Match Finding**: Level 3/4 match finding employs ultrametric Hensel lifting across multi-tier hash tables, achieving a **2.03x search speedup** (26.3 ns vs 54.6 ns at depth 64) with mathematically proven zero false candidate dismissals (Theorems HL1/HL2).
7. **Static RFC 7932 Web Dictionary**: Integrated 14,016-word pre-trained dictionary and 121-transform matrix with $O(1)$ hash indexing, delivering instant zero-cost matches across web assets, JSON, HTML, and source code.
8. **Formal Verification in Lean 4**: Key algebraic symmetries, involutions, and topological properties are computer-checked in **Lean 4** with strictly **0 `sorry` statements** across all 33 formal proof modules (`proofs/PRZM/*.lean` and `proofs/*.lean`).
9. **Clean Room Interoperability**: 100% bit-exact parity verified between the Go reference pipeline and the clean-room Pure C99 decoder (`tests/test_przm_dec.c`, 21/21 tests passing).

## Verified Empirical Benchmarks

All benchmarks conducted on: **12th Gen Intel Core i5-12400F** (6C/12T, 4.40 GHz) | **NVIDIA GeForce RTX 3060** (12 GB VRAM, sm_86, CUDA 13.4) | Windows 11 x86_64 | GCC 16.1.0 (`-O3`) | Go 1.27 (`amd64`). All tests bounded to $\le 8$ threads.

### Head-to-Head Corpus Benchmark (45 Real-World Files)

Evaluated against the official 45-file multi-domain corpus spanning executable binaries, source code, tabular datasets, JSON/XML, and structured metadata:

| Compression Engine | Compressed Total (45 Files) | Compression Ratio | Space Saved | Decompression Speed | Heap Allocations |
| :--- | :---: | :---: | :---: | :---: | :---: |
| **PRZM v0.8.0 (Season 23)** | **296,567 B** | **5.01x** | **80.0%** | **4.57–6.16 GB/s** | **0 B/op, 0 allocs/op** |
| **Zstandard (Level 19)** | 324,934 B | 4.57x | 78.1% | 1.8–3.2 GB/s | Dynamic buffers |
| **Brotli (Level 11, HQ)** | 259,103 B | 5.74x | 82.6% | 0.35–0.45 GB/s | Heap allocated |

### The Zstd-19 Victory Breakdown

Across the 45-file test corpus, PRZM outperforms Zstandard at its maximum standard preset (Level 19):
- **+28,367 Bytes Smaller (+8.7% ratio)**: PRZM compresses the corpus down to **296,567 B** vs Zstd-19's **324,934 B**.
- **Structured Code / Tables Victory**: On structured tables (`brotli_tables.go`), PRZM produces **100,844 B** vs Zstd-19's **127,722 B** — a margin of **-26,878 bytes (-21.0% smaller)**.
- **Decompression Speed Superiority**: PRZM decompresses at **4.57–6.16 GB/s** (strictly zero heap allocations), running **2.5x–3.5x faster than Zstd-19** and **12x–15x faster than Brotli-11**.

```
Decompression Throughput Comparison:
  PRZM v0.8.0 : [████████████████████████████████████████] 5,365 MB/s (0 allocs)
  Zstandard-19: [████████████████] 2,140 MB/s
  Brotli-11   : [███] 390 MB/s
```

## Hardware CUDA GPU Acceleration (`gpu/`)

The `gpu/` package provides high-throughput hardware compression directly on NVIDIA GPUs using pure Go and the NVIDIA Driver API (`nvcuda.dll` / `libcuda.so`), requiring **zero CGo compilation**:

```
                       CUDA HARDWARE PIPELINE
┌─────────────────┐       DMA HtoD       ┌──────────────────────┐
│  Host Memory    │ ═══════════════════> │ GPU VRAM (Pinned)    │
│  (Pinned Chunks)│                      └──────────┬───────────┘
└─────────────────┘                                 │
                                    ┌───────────────┴───────────────┐
                                    │ Parallel Radix-Strike LZ77    │
                                    │ Kernel (sm_86 Ampere PTX)     │
                                    │ - 40+ GB/s Match Finding      │
                                    └───────────────┬───────────────┘
                                                    │
                                    ┌───────────────┴───────────────┐
                                    │ Warp-Level Stream Tokenizer   │
                                    │ - Compacts Commands & Offsets │
                                    │ - Device-to-Device (D2D)      │
                                    └───────────────┬───────────────┘
                                                    │
┌─────────────────┐       DMA DtoH                  │
│ PRZM Bitstream  │ <═══════════════════════════════┘
│ (Compressed)    │
└─────────────────┘
```

- **Double-Buffered Streaming DMA**: Overlaps PCIe Host-to-Device and Device-to-Host transfers with kernel computation using asynchronous CUDA streams.
- **Parallel Radix-Strike LZ77 Matching**: Custom PTX kernel searches 64 KB to 8 MB sliding windows across thousands of GPU cores in parallel.
- **Warp Tokenizer**: Compacts match descriptors into Section 2 (Commands), Section 3 (Offsets), and Section 4 (Literals) without round-tripping to host RAM.
- **Zero-CGo Architecture**: Dynamically resolves Driver API entry points via syscalls, ensuring instantaneous compilation and zero CGo build overhead.
- **Graceful Fallback**: Automatically detects hardware capabilities and falls back to CPU Trellis search on non-GPU environments.

## The 13 Transform Families

PRZM decomposes heterogeneous data streams into 13 specialized algebraic and topological manifolds:

| ID | Transform Family | Opcode / Magic | Mathematical Formulation / Invariant | Measured Throughput | Target Data Domain |
|:---|:---|:---|:---|:---|:---|
| **01** | **Standard PRZM Engine** | `PRZM` (`0x4D5A5250`) | Multilevel fused LZSS + Trellis RDO + V-ANS | 4.57–6.16 GB/s | General archives, structured data |
| **02** | **Fast Möbius Transform (FMT)** | `OpMOBIUS` (`0x11` / `MOBI`) | Radix-2 Yates butterfly involution $M_N^2 \equiv I$ over $\mathbb{F}_2$ | 0.5–1.1 GB/s | Multi-channel RGBA, bitmap bit-planes |
| **03** | **Margolus Reversible CA** | `OpMARGOLUS` (`0x0F` / `MARG`) | 2D BBM partitioned reversible cellular automata | 0.4–1.9 GB/s | GPU Shaders (SPIR-V), structured grids |
| **04** | **Discrete Heisenberg Group** | `HEIS` (`0x53494548`) | Nilpotent foliation in $H_3(\mathbb{F}_{2^8})$, $(x, y, z \oplus (x \odot y))$ | **54.0–58.0 GB/s** | Telemetry, shader bytecode, machine code |
| **05** | **Cellular Sheaves & Hodge** | `CSHD` (`0x44485343`) | Boundary operator $\partial_1 \circ \partial_2 \equiv 0$ on 2D cell complexes | **52.3–59.7 GB/s** | Harmonic vector fields, geometric meshes |
| **06** | **4D IsoQuant Collimation** | `OpISOQUANT` (`0x10` / `ISOQ`) | Isoclinic quaternionic rotation in $\mathbb{H}(\mathbb{F}_{2^8})$ ($\le 22$ cyc/64B) | **53.4–57.1 GB/s** | Sparse arrays, high-dimensional tensors |
| **07** | **2-Adic Mahler Transform** | `MAHL` (`0x4D41484C`) | Unipotent Pascal wavelet transform over $\mathbb{Z}_2$, $\det(B) \equiv 1$ | **45.7–56.0 GB/s** | Polynomial trends, pointer tables, quantized weights |
| **08** | **Symplectic Phase Foliation** | `SYMP` (`0x53594D50`) | Symplectic involution over $Sp(4, \mathbb{F}_{2^8})$, $T_S^2 \equiv I$ | **49.7–58.7 GB/s** | Multi-channel phase signals, complex audio |
| **09** | **Persistent Homology & TDA** | `TDA` (`0x0E`) | Apparent-pair boundary reduction over $\mathbb{F}_2$ via streaming XOR | **28.1–30.0 GB/s** | Dense execution traces, binary event logs |
| **10** | **Pareto-Wave Synthesis (PWS)** | `PWS1` (`PW\x01`) | AVX-Wave 256-bit unrolled vector synthesis + CAST parser | 1.2–2.6 GB/s | High-entropy source code, JSON payloads |
| **11** | **Hyperbolic MDS (h-MDS)** | `HMDS` (`0x0B`) | Minkowski spectral metric projection $-Y = XX^T - \mathbf{u}\mathbf{u}^T$ in $\mathcal{M}^4$ | 1.6–1.9 GB/s | Deep AST trees, nested XML/JSON graphs |
| **12** | **Quantum Spectral Unitary** | `QUNT` (`0x0C`) | Orthogonal Givens rotations over $\mathbb{F}_{2^8}$ ($s = c \oplus 1$) | **29.5–31.5 GB/s** | Correlated sensor channels, audio stems |
| **13** | **Hensel Ultrametric Matcher** | *Internal L3/L4* | $p$-Adic ultrametric triangle inequality $d(x,z) \le \max(d(x,y), d(y,z))$ | 2.03x Match Speed | Level 3/4 match finding on 64 KiB window |

## Binary Framing Topology

A standard PRZM frame consists of an 8-byte global header followed by 4 disjoint, independently decodable sections:

```
[Magic: 4B ("PRZM")] [Flags: 1B] [UncompressedSize: 3B LE]
[Optional Stride: 0..1B] [Optional DictID: 0..4B]
[Section Directory: 4B or 6B]
  ├── Section 1: Distribution Parameters & Window Log (8 bytes)
  ├── Section 2: Command Stream (Mode 8 Tri-Stream: opStream, metaStream, varStream)
  ├── Section 3: Offset Stream (49-bracket codebook, Mode 12 V-ANS, 4-register LRU)
  └── Section 4: Literal Stream (Raw bytes, Nibble-Planes, or 4-lane interleaved SIMD V-ANS)
```

## Repository Layout

```text
przm/
├── przm.go, api.go, options.go      # Root Go high-level facade
├── compress.go, decompress.go       # High-level streaming compression & decompression
├── doc.go                           # Complete package godoc architecture documentation
├── core/                            # Decoupled algorithmic engine modules
│   ├── commands/                    # Tri-stream Mode 8 demux & Bigram V-ANS encoder
│   ├── distance/                    # 49-bracket codebook & Mode 12 Clustered V-ANS
│   ├── literals/                    # 4-lane interleaved SIMD V-ANS coder
│   └── dict/                        # RFC 7932 14,016-word static web dictionary
├── gpu/                             # Hardware CUDA GPU acceleration subsystem
│   ├── bridge.go                    # Zero-CGo NVIDIA Driver API bridge & DMA pipeline
│   ├── memory.go                    # Pinned host memory allocator & buffer pool
│   ├── types.go                     # Match descriptors, GPU profiles, and stream topologies
│   └── kernel/przm_lz77.ptx         # Embedded PTX sm_86 parallel matching kernels
├── src/                             # Pure C99/C11 clean-room implementation
│   ├── przm_dec.c, przm_dec.h       # Zero-allocation line-rate decompressor
│   └── przm_comp.c, przm_comp.h     # Reference C encoder
├── tests/                           # Cross-language & clean-room test suites
│   ├── test_przm_dec.c              # Pure C verification suite (21/21 clean-room tests)
│   └── e2e/                         # End-to-end integration and GPU stress tests
├── cmd/                             # CLI utilities
│   └── przm-viz/                    # Terminal visualization dashboard for PRZM streams
├── docs/                            # Formal specifications & architectural RFCs
│   ├── SPECIFICATION.md             # IETF RFC standard draft
│   └── PRZM_SPEC.md                 # Complete binary format specification
└── proofs/                          # Lean 4 formal mathematical verification suite (33 modules)
    ├── PRZM/                        # 27 foundational manifold transforms & algebraic pillars
    ├── PRZM.lean                    # Root umbrella library module
    └── lakefile.lean                # Lake package configuration & build harness
```

## API Usage

### Pure C99/C11 API (Zero Allocation)

The standalone decompressor (`src/przm_dec.c` / `src/przm_dec.h`) has **zero dependencies** and operates strictly within caller-provided memory:

```c
#include "przm_dec.h"
#include <stdio.h>
#include <stdlib.h>

void decompress_example(const uint8_t* compressed, size_t comp_len) {
    // 1. Query exact uncompressed size in O(1) time
    int64_t u_size = przm_get_uncompressed_size(compressed, comp_len);
    if (u_size < 0) {
        fprintf(stderr, "Invalid or corrupted PRZM frame\n");
        return;
    }

    // 2. Allocate output destination buffer with safety padding
    size_t out_cap = (size_t)u_size + PRZM_SAFETY_PADDING;
    uint8_t* out_buf = (uint8_t*)malloc(out_cap);

    // 3. Decompress with strictly ZERO heap allocations (0 B/op)
    int64_t written = przm_decompress(compressed, comp_len, out_buf, out_cap);
    if (written != u_size) {
        fprintf(stderr, "Decompression error: %lld\n", (long long)written);
        free(out_buf);
        return;
    }

    printf("Decompressed %lld bytes at line-rate speed!\n", (long long)written);
    free(out_buf);
}
```

### Go High-Level API (Zero-Allocation In-Place)

```go
package main

import (
	"fmt"
	"log"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

func main() {
	payload := []byte("Autonomous geometric archival compression across discrete manifolds!")

	// 1. Compress at Level 3 (Trellis RDO + Hensel Matcher)
	compressed, err := przm.Compress(payload, 3)
	if err != nil {
		log.Fatalf("Compression failed: %v", err)
	}

	// 2. Allocate cache-aligned destination buffer
	dst := bufkit.AlignedBytes(len(payload)+64, bufkit.CacheLineSize)

	// 3. Decompress in-place with strictly 0 heap allocations
	n, err := przm.DecompressTo(compressed, dst)
	if err != nil {
		log.Fatalf("Decompression failed: %v", err)
	}

	fmt.Printf("Decompressed %d bytes: %s\n", n, string(dst[:n]))
}
```

### CUDA GPU Compression API

```go
package main

import (
	"fmt"
	"log"

	"github.com/lemon4ksan/przm/gpu"
)

func main() {
	if !gpu.IsAvailable() {
		fmt.Println("NVIDIA GPU not detected, falling back to CPU")
		return
	}

	compressor, err := gpu.GetGlobalCompressor()
	if err != nil {
		log.Fatal(err)
	}

	data := make([]byte, 16*1024*1024) // 16 MB payload
	// ... fill data ...

	// Device-to-Device (D2D) zero-copy compression
	compressed, profile, err := compressor.CompressD2D(data)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Compressed %d MB -> %d bytes on GPU (Kernel Time: %.2f ms, Ingress: %.2f GB/s)\n",
		len(data)/(1024*1024), len(compressed), profile.KernelTimeMs, profile.IngressDMABandwidthGBps)
}
```

## Building & Verification

### 1. Build & Run Pure C99 Decompressor Verification Suite
```bash
gcc -O3 -Wall -Wextra -Isrc -o test_przm_dec.exe tests/test_przm_dec.c src/przm_dec.c src/przm_comp.c
./test_przm_dec.exe
```
Expected output:
```text
=== Running Clean Room C99 Verification Suite ===
[RUN] przm_get_uncompressed_size
[PASS] przm_get_uncompressed_size
[RUN] standard_fused_raw
[PASS] standard_fused_raw
...
[RUN] mode8_and_explicit_matches
[PASS] mode8_and_explicit_matches
>>> ALL CLEAN ROOM C99 TESTS PASSED SUCCESSFULLY! <<<
```

### 2. Run Go Test Suite & Linter
```bash
# Run core and root test suites
go test -count=1 ./core/... .

# Run GPU hardware acceleration test suite (requires NVIDIA GPU)
go test -v ./gpu/...

# Run golangci-lint (0 warnings enforced)
golangci-lint run ./...
```

### 3. Verify Formal Mathematical Proofs in Lean 4
```bash
cd proofs
lake build
```
All 33 proof modules in `proofs/` are computer-checked with strictly 0 `sorry` statements.

## CLI Usage

The pure C99 verification binary doubles as a standalone high-speed CLI decompressor:

```bash
# Decompress any valid PRZM archive to target output file:
./test_przm_dec.exe archive.przm recovered_payload.bin
```

## License

PRZM is distributed under the **3-Clause BSD License**. See [LICENSE](LICENSE) for details.
