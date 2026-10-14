# Contributing to PRZM

Thank you for your interest in contributing to **PRZM**! PRZM is an ultra-high-speed, mathematically unified archival compression system mapping non-linear data distributions onto curved discrete manifolds.

We welcome performance optimizations, mathematical formalizations, hardware acceleration kernels, and bug fixes.

## Ground Rules

- **Strict Zero-Allocation Contract:** Decompression streaming paths in both Go and C must remain strictly **0 heap allocations** (`0 B/op, 0 allocs/op`). No dynamic `malloc` or slice growth in hot loops.
- **Formal Invariant Preservation:** Any change modifying core manifold transforms, Hensel lifting, or topological bounds must preserve the corresponding **Lean 4** formal proofs (`PRZM/*.lean`) with strictly 0 `sorry` statements.
- **Microarchitectural Line-Rate Speed:** PRZM targets line-rate throughput (saturating hardware bus speeds). Regression in instruction cycles per byte will not be accepted.
- **Clean Portability:** The C decompressor (`libprzm_dec`) must remain strictly standard C99/C11 with zero external runtime dependencies.

## Getting Started

```bash
git clone https://github.com/lemon4ksan/przm
cd przm
go test ./...        # run Go unit tests
go test -race ./...  # test with race detector
make lint           # run golangci-lint
```

## Workflow

1. **Fork** the repository and create a descriptive feature branch from `main`.
2. Implement your changes adhering to memory safety and zero-allocation contracts.
3. Add unit and regression tests, or fuzzing targets for format decoders.
4. Run `make lint` and ensure all tests pass cleanly.
5. Open a Pull Request targeting `main` using the provided PR template.

## Commit Style

Use [Conventional Commits](https://www.conventionalcommits.org/):

```text
feat(simd): add AVX-512 register unrolling in Margolus decompressor
fix(decoder): guard against truncated bitstream at sub-chunk boundary
perf(hensel): reduce branch mispredictions in p-adic match finding
docs: expand mathematical rationale for manifold coordinate charts
proof(lean): verify involution theorem for mobius transform
```

## Tests & Benchmarks

| Command | Purpose |
|---|---|
| `make test` | Fast unit tests |
| `make race` | Tests with Go data race detector |
| `make bench` | Micro-benchmarks for decompression throughput and cycles/byte |
| `make fuzz` | Bitstream and container parser fuzz testing |
| `make lint` | Multi-linter validation |
| `make format` | Formatting and BSD-3-Clause license verification |

## Questions

Join the [GitHub Discussions](https://github.com/lemon4ksan/przm/discussions) to discuss compression theory, p-adic mathematics, or GPU streaming pipelines.
