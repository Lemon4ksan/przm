# E2E Test Infra: PRZM CUDA GPU Acceleration

## Test Philosophy
- Opaque-box, requirement-driven. No dependency on implementation internals.
- Verification methodology: Category-Partition + Boundary Value Analysis (BVA) + Pairwise Combinatorial + Real-World Workload Testing.

## Feature Inventory & Test Coverage
| # | Feature | Source (Requirement) | Tier 1 | Tier 2 | Tier 3 | Tier 4 |
|---|---------|----------------------|:------:|:------:|:------:|:------:|
| 1 | Warp-Bucketed Shared Memory LZ77 Kernel | ORIGINAL_REQUEST §R1 | 5 | 5 | ✓ | ✓ |
| 2 | PTX 7.1 sm_86 Kernel Artifact | ORIGINAL_REQUEST §R1 | 5 | 5 | ✓ | ✓ |
| 3 | Pure Go CUDA Driver API Bridge | ORIGINAL_REQUEST §R2 | 5 | 5 | ✓ | ✓ |
| 4 | Pinned Host Memory Allocator | ORIGINAL_REQUEST §R1/R2 | 5 | 5 | ✓ | ✓ |
| 5 | Public API: WithGPUAcceleration() | ORIGINAL_REQUEST §R2 | 5 | 5 | ✓ | ✓ |
| 6 | Engine Pipeline Routing | ORIGINAL_REQUEST §R2 | 5 | 5 | ✓ | ✓ |
| 7 | Safe Graceful Fallback | ORIGINAL_REQUEST §R3 | 5 | 5 | ✓ | ✓ |
| 8 | Bit-Exact Lossless Roundtrips | Acceptance Criteria | 5 | 5 | ✓ | ✓ |
| 9 | Automated Performance & Crossover Benchmark | Acceptance Criteria | 5 | 5 | ✓ | ✓ |
| 10| Simulated GPU Failure Fallback Test | Acceptance Criteria | 5 | 5 | ✓ | ✓ |

## Test Architecture
- Test runner: `go test -v ./tests/...` and `go test -v -run=TestGPU ./...`
- Pass/Fail semantics: All tests pass with exit code 0; 0 panics; 100% bit-exact roundtrips (`bytes.Equal(decompressed, original) == true`).
- Fallback verification: Programmatically simulate `nvcuda.dll` or CUDA device init failure and assert PRZM compresses and decompresses with CPU SIMD without returning an error or panicking.
- Directory layout:
  - `d:\CodingProjects\przm\tests\e2e\gpu_tier1_features_test.go`
  - `d:\CodingProjects\przm\tests\e2e\gpu_tier2_boundaries_test.go`
  - `d:\CodingProjects\przm\tests\e2e\gpu_tier3_interactions_test.go`
  - `d:\CodingProjects\przm\tests\e2e\gpu_tier4_workloads_test.go`
  - `d:\CodingProjects\przm\tests\gpu_crossover_bench_test.go`

## Real-World Application Scenarios (Tier 4)
| # | Scenario | Features Exercised | Complexity |
|---|----------|--------------------|------------|
| 1 | Large JSON Dataset (16MB - 64MB) | GPU LZ77 + V-ANS + Pinned DMA | High |
| 2 | Go / C Source Code Repository Tarball | GPU LZ77 + Sheaf Fibration + V-ANS | High |
| 3 | Mixed Binary Executables & Shader Microcode | GPU LZ77 + Margolus / PWS | High |
| 4 | Simulated Hardware Failure Under Heavy Load | Fallback to CPU SIMD on-the-fly | High |
| 5 | Massive Archival Stream (64MB - 128MB) | PCIe Crossover Point Demonstration | High |

## Coverage Thresholds
- Tier 1: ≥5 per feature (Total ≥ 50)
- Tier 2: ≥5 per feature (Total ≥ 50)
- Tier 3: Pairwise coverage of major feature interactions
- Tier 4: ≥5 realistic application scenarios
