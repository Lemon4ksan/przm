# Project: PRZM Multithreaded Compression Engine (PRZM-MT)

## Architecture
PRZM-MT elevates the PRZM compression engine from a single-threaded bottleneck to a fully parallelized, multi-core saturated engine. It introduces chunk-level parallelization while strictly preserving PRZM's core invariants:
1. **100% Bit-Exact Lossless Roundtrips** across all datasets and edge cases.
2. **Zero Dynamic Heap Allocations (0.00 allocs/run)** during decompression (`DecompressTo`).
3. **Pure C Reference Decoder Compatibility** (`src/przm_dec.c` and `przm_decompress`).
4. **High Aggregate Compression Ratio (>3.0x, baseline ~3.6x)** on the 166-file corpus.

### Data Flow & Framing Architecture
- **Standard PRZM Single-Frame Format** (`MagicPRZM = 0x4D5A5250`): Retained for single-threaded or small payloads ($\le 16\text{ MiB}$).
- **PRZM Multi-Chunk Stream Container Format** (`MagicPRZS = 0x535A5250` - ASCII 'P','R','Z','S'):
  ```
  +--------------------------------------------------------------------------+
  | Magic (4B) : 0x535A5250 ('PRZS')                                         |
  | Stream Flags (1B) : bit 0 = Independent Chunks                           |
  | Total Uncompressed Size (8B uint64 LE)                                   |
  | Chunk Count (4B uint32 LE)                                               |
  +--------------------------------------------------------------------------+
  | For each chunk i (0 <= i < Chunk Count):                                 |
  |   Chunk Compressed Size (4B uint32 LE)                                   |
  |   Chunk Payload (valid PRZM frame of Chunk Compressed Size bytes)        |
  +--------------------------------------------------------------------------+
  ```
- **Encoding Pipeline Concurrency**:
  - `CompressWithOptions(src, opts)` inspects `opts.Threads` and `opts.ChunkSize`.
  - Input is sliced into independent chunks (adaptive size: 1 MiB to 4 MiB, default 2 MiB; files > 16 MiB partitioned into $N$ chunks).
  - Worker pool of $\min(N, \text{NumCPU})$ goroutines processes chunks concurrently using pooled scratchpads (`sync.Pool`), eliminating allocation pressure.
  - Chunks are assembled into the output buffer in deterministic stream order.
- **Decoding Pipeline Zero-Allocation Invariant**:
  - `przm_get_uncompressed_size` inspects magic: if `PRZS`, reads offset 5 (uint64) in $O(1)$ time.
  - `DecompressTo(dst, src)` validates `cap(dst) >= TotalUncompressedSize + 64`. Loops through chunks, invoking the existing zero-allocation chunk decompressor into `dst[dstPos : dstPos + chunkUSize]`. Strictly 0 heap allocations.
  - `przm_dec.c` (`przm_decompress`) implements identical chunk loop in Pure C using thread-local scratchpads.

## Feature Inventory
| # | Feature | Description | Milestone | Source |
|---|---------|-------------|-----------|--------|
| 1 | Multi-Chunk Stream Format ('PRZS') | Container header, chunk count, 64-bit uncompressed size, chunk length prefixes | M1 | Survey (Explorer 1 & 2) |
| 2 | Pure C Multi-Chunk Decoder Support | `przm_get_uncompressed_size` and `przm_decompress` in `src/przm_dec.c` | M1 | Survey (Explorer 2) |
| 3 | Go Multi-Chunk Decoder Zero-Alloc | `DecompressTo` and `Decompress` multi-chunk streaming with 0.00 allocs | M1 | Survey (Explorer 2) |
| 4 | Concurrency API Extensions | `Threads` and `ChunkSize` fields in `CompressOptions` | M2 | Survey (Explorer 1) |
| 5 | Multithreaded Compression Pipeline | Worker pool, chunk partitioning, concurrent encoding, deterministic reassembly | M2 | Survey (Explorer 1) |
| 6 | Integration into `Compress` / `CompressWithOptions` | Transparent multi-core acceleration with automatic core detection | M2 | User Request R1 |
| 7 | Full Lossless Roundtrip & Race Cleanliness | `go test ./...` and `go test -race ./...` bit-exact verification | M3 | User Request Acceptance |
| 8 | Zero-Allocation Verification Swarm | Formal `testing.AllocsPerRun(100)` verification across all chunk layouts | M3 | User Request Acceptance |
| 9 | Multi-Core Saturation Measurement | Automated script measuring CPU time vs Wall time ratio ($\ge 5.0x$ on 6C/12T) | M4 | Survey (Explorer 3) |
| 10 | 166-File Corpus Benchmark vs Zstd | Benchmark script comparing PRZM MT vs Zstd on 166-file baseline corpus | M4 | Survey (Explorer 3) |
| 11 | 256MB+ Large File Benchmark vs Zstd | Benchmark script evaluating throughput and scaling on 256MB test file | M4 | Survey (Explorer 3) |

## Milestones
| # | Name | Scope | Dependencies | Status |
|---|------|-------|-------------|--------|
| M1 | Multi-Chunk Framing & Decoder Invariant Preservation | Implement 'PRZS' container format and update Go `DecompressTo` and C `przm_dec.c` with zero-allocation decoding | None | DONE |
| M2 | Multithreaded Compression Pipeline Core | Implement worker pool, chunk dispatcher, and integrate into `Compress` / `CompressWithOptions` | M1 | DONE |
| M3 | Correctness, Invariants & Interop Verification | Comprehensive test suites, race detection, C interop verification, and 0.00 AllocsPerRun audit | M2 | DONE |
| M4 | Automated Benchmark Suite & Zstd Parity Validation | Automated benchmark script on 166-file corpus and 256MB file proving multi-core saturation and throughput | M3 | DONE |

## Interface Contracts
### `CompressOptions` (Go)
```go
type CompressOptions struct {
    Level              int         // 1..5
    WindowLog          int         // 16, 20, 23, or 0 (auto)
    Dict               *Dictionary // Optional external dictionary
    ArchivalMode       bool        // Explicit toggle for --mode=archival
    StandardStreamOnly bool        // Ensure single standard PRZM frame (fails if > 16 MiB)
    Threads            int         // 0 = auto (runtime.GOMAXPROCS(0)), 1 = single-threaded, >1 = explicit worker count
    ChunkSize          int         // 0 = auto (1MB..4MB), >0 = explicit chunk uncompressed byte size
}
```

### Pure C Decoder API (`include/przm_dec.h`)
```c
#define PRZM_MAGIC_STREAM 0x535A5250 /* 'P','R','Z','S' (Multi-Chunk Stream) */

int64_t przm_get_uncompressed_size(const uint8_t* src, size_t src_len);
int64_t przm_decompress(const uint8_t* src, size_t src_len, uint8_t* dst, size_t dst_cap);
```

## Code Layout
- `frame.go`: Add `MagicPRZS = 0x535A5250`, header constants, stream header parsing.
- `compress.go`: Extend `CompressOptions`, implement chunk splitting, worker pool, multi-threaded encoder.
- `decompress.go`: Multi-chunk decoding in `DecompressTo`, `Decompress`, preserving 0 dynamic allocations.
- `include/przm_dec.h`: Add `PRZM_MAGIC_STREAM` and error constants.
- `src/przm_dec.c`: Add stream header parsing and sequential chunk decoding in `przm_decompress` and `przm_get_uncompressed_size`.
- `tests/test_przm_dec.c`: Pure C multi-chunk test cases.
- `c_interop_test.go`: C binary interop tests for multi-chunk payloads.
- `tests/bench_mt/main.go` or `bench_mt.go`: Automated benchmark tool comparing PRZM MT against Zstd on 166-file corpus and 256MB test file with CPU saturation metrics.
