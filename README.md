# PRZM: Autonomous Geometric Archival Compression

[![Language](https://img.shields.io/badge/Language-C11%20%7C%20Go-00ADD8.svg)](https://github.com/lemon4ksan/przm)
[![License](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)
[![Zero-Alloc](https://img.shields.io/badge/Zero--Heap--Alloc-0%20B%2Fop-brightgreen.svg)]()
[![Throughput](https://img.shields.io/badge/Decompress%20Throughput-35--56%20GB%2Fs-orange.svg)]()

> **"True balance is neither the cold silence of maximum entropy, nor the frozen immobility of a crystal. It is the dynamic, vibrating tension held on the razor-thin boundary between order and chaos.**
> 
> **Information in the cosmos is never destroyed; it merely disperses across coordinate charts we fail to perceive. When the observer chooses the intrinsic geometry of the data manifold, the illusion of randomness collapses into deterministic harmony. Compression is not the erasure of substance, but the mathematical revelation of universal symmetry."**
> — *The PRZM Philosophy on Universal Balance*

PRZM is an ultra-high-speed, mathematically unified archival compression system that transcends the classical 1D sliding-window limit (zstd, LZMA, Brotli) by mapping non-linear digital data distributions onto curved discrete manifolds.

This repository provides the reference **Pure C99/C11 Zero-Allocation Decompressor (`libprzm_dec`)**, designed for instantaneous integration into high-performance systems: game engines (Unreal Engine, Unity, Godot), GPU streaming pipelines, asset loaders, emulators, and foreign function interfaces (Rust, C++, Zig, Python).

---

## Key Features

- **Strict Zero-Allocation Contract**: `0 B/op, 0 malloc/free calls`. Decompression operates strictly with zero dynamic heap allocations.
- **Microarchitecture Grounded**: Formulated over discrete finite Galois fields $\mathbb{F}_{2^8}$ and residue rings $\mathbb{Z}/2^{32}\mathbb{Z}$, with strictly integer arithmetic (zero floating-point, zero transcendental functions).
- **Extreme Decompression Throughput**: **35 to 56 GB/s** line rate in RAM across AVX2/NEON vector paths.
- **Algebraic Involutions**: Symplectic phase-space transvections ($Sp(4, \mathbb{F}_{2^8})$) execute identically during compression and decompression ($T_S^2 \equiv I$).
- **Integrated Structural Pre-Filters**:
  - 7-Zip compatible x86 BCJ branch address collimation (Bra86) and BCJ-Split 4-plane displacement unrolling.
  - ARM64 AArch64 `B` and `BL` branch target uncollimation.
  - 2-Adic Mahler Ultrametric Pascal Wavelet transforms ($\det(B) \equiv 1 \pmod{256}$).
  - 4-way interleaved Vector Asymmetric Numeral Systems (V-ANS 4x).
  - 5-Layer Pareto-Wave Synthesis (PWS) H-DAG unrolling.
  - Zero-overhead Schema Dictionaries (JSON, Source Code).

---

## Repository Structure

```
przm/
├── include/
│   ├── przm.h              # Umbrella header
│   └── przm_dec.h          # Pure C Decompressor API
├── src/
│   └── przm_dec.c          # Core zero-allocation C11 implementation
├── tests/
│   └── test_przm_dec.c     # Standalone unit & CLI decompressor test suite
├── docs/
│   └── PRZM_SPEC.md        # Complete PRZM-SPEC-0.5.0 Bitstream Specification
├── MANIFESTO.md            # Foundational mathematical philosophy of PRZM
├── Makefile                # GCC / Clang build script
└── CMakeLists.txt          # Modern CMake package definition
```

---

## C API Quickstart

Include `przm_dec.h` in your project:

```c
#include "przm_dec.h"
#include <stdio.h>
#include <stdlib.h>

void decompress_example(const uint8_t* compressed_data, size_t compressed_len) {
    // 1. Query the uncompressed payload size from the frame header (O(1))
    int64_t u_size = przm_get_uncompressed_size(compressed_data, compressed_len);
    if (u_size < 0) {
        fprintf(stderr, "Invalid PRZM frame: error %lld\n", (long long)u_size);
        return;
    }

    // 2. Allocate destination buffer with safety padding (64 bytes)
    size_t dst_capacity = (size_t)u_size + PRZM_SAFETY_PADDING;
    uint8_t* output = (uint8_t*)malloc(dst_capacity);

    // 3. Decompress with 0 dynamic heap allocations
    int64_t bytes_written = przm_decompress(compressed_data, compressed_len, output, dst_capacity);
    if (bytes_written < 0 || bytes_written != u_size) {
        fprintf(stderr, "Decompression failed: error %lld\n", (long long)bytes_written);
        free(output);
        return;
    }

    printf("Successfully decompressed %lld bytes!\n", (long long)bytes_written);

    // ... use output buffer ...
    free(output);
}
```

---

## Building

### Using Make (GCC / Clang)

```bash
# Build static library (libprzm_dec.a) and test harness
make

# Run standalone verification test suite
make test
```

### Using CMake

```bash
mkdir build && cd build
cmake ..
cmake --build .
ctest --output-on-failure
```

---

## CLI Usage

The test binary also functions as a standalone CLI decompressor:

```bash
./test_przm_dec <input.przm> <output.bin>
```

---

## Verification & Interoperability

PRZM includes a comprehensive cross-language test suite. Payloads compressed by the Go implementation at compression levels 1 through 4 across JSON, telemetry, Go source code, and x86 machine binaries decompress in pure C bit-for-bit identical to original sources.

---

## License

PRZM is licensed under the 3-Clause BSD License. See [LICENSE](LICENSE) for details.
