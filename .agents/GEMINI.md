# GEMINI.md — Development Guidelines & AI Assistant Protocol

This document outlines the architecture, coding standards, build/test commands, and interaction rules for AI assistants and software engineers working in the **przm** repository.

## 1. Project Overview

**`przm`** is an ultra-high-speed, mathematically unified archival compression system that transcends classical 1D sliding-window limits (zstd, LZMA, Brotli) by mapping non-linear digital data distributions onto curved discrete manifolds.

> **Core Engineering Manifesto**:
> _«Information in the cosmos is never destroyed; it merely disperses across coordinate charts we fail to perceive. When the observer chooses the intrinsic geometry of the data manifold, the illusion of randomness collapses into deterministic harmony. Compression is not the erasure of substance, but the mathematical revelation of universal symmetry.»_

### Key Capabilities & Architectural Pillars
- **Strict Zero-Allocation Contract**: Strictly **0 dynamic heap allocations** (`0 B/op, 0 allocs/op`) during streaming decompression across both Go and Pure C99/C11 (`libprzm_dec`).
- **Microarchitectural Line-Rate Throughput**: Decompression operates at **up to 58.7 GB/s** ($O(1)$ cycles/block bound), consuming between **2.63 and 3.5 CPU clock cycles per 64-byte vector block**.
- **Formal Verification in Lean 4**: Key algebraic symmetries, involutions, and topological properties are formalized and computer-checked in **Lean 4** with strictly **0 `sorry` statements** across all 33 proof modules (`proofs/PRZM/*.lean` and `proofs/*.lean`).
- **Non-Archimedean $p$-Adic Match Finding**: Level 3/4 match finding replaces linear hash chains with ultrametric Hensel lifting, achieving a **2.03x search speedup** with zero false candidate dismissals.

---

## 2. Repository Layout

```text
przm/
├── przm.go, doc.go                   // Core API facade and architecture documentation
├── decompress.go, compress.go        // High-level compression and decompression streams
├── c/                                // Standalone Pure C99/C11 line-rate decompressor (libprzm_dec)
├── simd/                             // Hardware SIMD vectorization (AVX2, AVX-512, NEON)
├── proofs/                           // Formal mathematical specifications and Lean 4 proofs
└── .agents/                          // AI assistant guidelines and protocols
```

---

## 3. Core Architectural Principles & Technical Constraints

1. **Zero Heap Allocations on Decompression**:
   - `DecompressTo` and C `przm_decompress` must NEVER perform heap allocations (`new`, `make`, `malloc`).
   - Use fixed caller buffers or stack arenas.
2. **Lean 4 Proof Fidelity**:
   - Do not modify algebraic manifold transformations or Hensel match rules without corresponding Lean 4 proof verification. Never introduce `sorry`.
3. **Mechanical Sympathy**:
   - Align memory accesses to 64-byte CPU cache line boundaries.
   - Vectorize hot bit-unpacking loops using SWAR or SIMD intrinsics.

---

## 4. Code Style & Quality Standards

- **Go Version**: 1.27+; **C Version**: C99 / C11 conforming.
- **Documentation**: All public symbols must have clear Godoc documentation with mathematical complexity bounds noted ($O(1)$ or $O(N)$).
- **Linters**: Pass `golangci-lint run` with 0 warnings.

---

## 5. Development & Testing Commands

| Command | Purpose |
| :--- | :--- |
| `go test ./...` | Fast unit tests |
| `go test -race ./...` | Unit tests with race detector |
| `go test -bench=. -benchmem ./...` | Microbenchmarks |
| `make lint` | Run `golangci-lint` |
| `make format` | Format code and enforce license headers |

---

## 6. Commit & Pull Request Guidelines

### Conventional Commits Format:
```text
<type>(<scope>): <short summary>

<detailed description of changes>
```

Commit Types:
- `feat`: New compression feature or SIMD acceleration kernel
- `fix`: Bug fix in bitstream or framing decoder
- `impr`: Microarchitectural optimization or cycle reduction
- `proof`: Updates to Lean 4 formal specifications
- `docs`: Documentation updates
- `test`: Unit or fuzzing test suite expansion
