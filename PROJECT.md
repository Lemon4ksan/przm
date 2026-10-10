# Project: PRZM CUDA GPU Acceleration

## Architecture
PRZM CUDA GPU Acceleration offloads the heavy LZ77 dictionary match search stage from CPU SIMD to an NVIDIA Ampere GPU (RTX 3060) via the CUDA Driver API (`nvcuda.dll`), eliminating external toolchain dependencies (pure Go dynamic loader, zero Cgo/MSVC/nvcc needed at runtime).

### High-Level Data Flow:
```
[User Data] ──> przm.CompressWithOptions(src, opts)
                     │
         opts.GPUAcceleration == true?
            ├── [YES] ──> gpu.GetGPUCompressor()
            │                 │
            │           Init Success?
            │             ├── [YES] ──> Pinned Host Buffer (24 GB/s DMA)
            │             │                 │
            │             │             Ingress PCIe DMA HtoD
            │             │                 │
            │             │             VRAM Device Buffer
            │             │                 │
            │             │             Warp-Bucketed Shared Memory LZ77 Kernel (>6 GB/s)
            │             │                 │
            │             │             Egress PCIe DMA DtoH (Matches / Descriptors)
            │             │                 │
            │             │             Token Emission & V-ANS Entropy Coding
            │             │
            │             └── [NO]  ──> Graceful Fallback (Log warning) ──┐
            │                                                             │
            └── [NO] ─────────────────────────────────────────────────────┴──> Standard CPU SIMD Hensel LZ77 (~2.5 GB/s)
```

## Feature Inventory
| # | Feature | Description | Milestone | Source |
|---|---------|-------------|-----------|--------|
| 1 | Warp-Bucketed Shared Memory LZ77 Kernel | Tiled shared-memory match search eliminating global DRAM pointer chasing, achieving >6 GB/s internal throughput | M1 | ORIGINAL_REQUEST §R1 |
| 2 | PTX 7.1 sm_86 Kernel Artifact | Compiled PTX for Ampere architecture ready for JIT loading by display driver | M1 | ORIGINAL_REQUEST §R1 |
| 3 | Pure Go CUDA Driver API Bridge | Dynamic loading of `nvcuda.dll`, primary context retain, kernel function resolution | M2 | ORIGINAL_REQUEST §R2 |
| 4 | Pinned Host Memory Allocator | `cuMemAllocHost` page-locked buffer pool for saturating PCIe Gen 4 DMA at 21-24 GB/s | M2 | ORIGINAL_REQUEST §R1/R2 |
| 5 | Public API: `WithGPUAcceleration()` | Option function and `CompressOptions.GPUAcceleration` field in `compress.go` | M3 | ORIGINAL_REQUEST §R2 |
| 6 | Engine Pipeline Routing | Directing archival and high-throughput compression requests to GPU matcher | M3 | ORIGINAL_REQUEST §R2 |
| 7 | Safe Graceful Fallback | Automatic fallback to CPU SIMD upon missing GPU, driver failure, or OOM, with zero panics and zero leaks | M3 | ORIGINAL_REQUEST §R3 |
| 8 | Bit-Exact Lossless Roundtrips | 100% roundtrip fidelity verified across all datasets | M4 | ORIGINAL_REQUEST Acceptance |
| 9 | Automated Performance & Crossover Benchmark | Benchmarking kernel throughput (>6 GB/s) and proving PCIe crossover vs 2.5 GB/s CPU SIMD | M4 | ORIGINAL_REQUEST Acceptance |
| 10 | Simulated GPU Failure Fallback Test | Programmatic test verifying CPU fallback when GPU bridge errors out | M4 | ORIGINAL_REQUEST Acceptance |

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| M1 | Warp-Bucketed CUDA Kernel | High-throughput shared-memory LZ77 kernel (`.cu` and `.ptx`) targeting >6 GB/s | none | DONE |
| M2 | GPU Driver Bridge & Memory Pool | `gpu` package in PRZM, `nvcuda.dll` dynamic loader, pinned memory pool, micro-benchmarks | M1 | DONE |
| M3 | Engine Integration & Fallback | `WithGPUAcceleration()`, pipeline routing in `compress.go`, transparent CPU fallback | M2 | DONE |
| M4 | Final E2E Suite & Crossover Proof | 100% passing E2E tests (Tiers 1-4), bit-exact roundtrips, fallback verification, and crossover benchmark | M3 | DONE |

## Interface Contracts
### `gpu` package ↔ `przm` Core
- `gpu.IsAvailable() bool`: Checks if `nvcuda.dll` is present and an Ampere+ CUDA device is accessible.
- `gpu.NewGPUCompressor(deviceID int) (*GPUCompressor, error)`: Allocates device context, compiles/loads PTX module, initializes pinned memory pool.
- `gpu.GetGlobalCompressor() (*GPUCompressor, error)`: Singleton/pooled compressor instance.
- `(g *GPUCompressor) MatchLZ77(src []byte) ([]LZ77Match, GPUProfile, error)`: Executes GPU match search over pinned buffer, returns match descriptors.
- `(g *GPUCompressor) Close() error`: Releases primary context and frees pinned host memory.
- `przm.WithGPUAcceleration() Option`: Enables GPU hardware acceleration path in `CompressOptions`.

## Code Layout
- `d:\CodingProjects\przm\gpu\`: GPU Driver API bridge, memory management, and PTX loader.
- `d:\CodingProjects\przm\gpu\kernel\`: CUDA kernel source (`przm_lz77.cu`), header (`przm_lz77.h`), and embedded PTX (`przm_lz77.ptx`).
- `d:\CodingProjects\przm\compress.go`: Core compression entry point, `CompressOptions`, and fallback routing.
- `d:\CodingProjects\przm\tests\e2e\`: E2E test suite (Tiers 1-4).
- `d:\CodingProjects\przm\tests\gpu_bench_test.go`: Automated crossover and throughput benchmark.
