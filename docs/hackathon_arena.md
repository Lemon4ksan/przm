# 🏆 PRZM Optimization Arena: Season 3 (The Code Singularity)

**Status:** ACTIVE
**Current Leader:** TIE (Scores reset for Season 3)

## The Teams & Objectives
* **🔴 Team DAG (Optimal Parsing Architects):** Focus on the Optimal Parser. Parallelize the LZ77 match graph building and Dynamic Programming (shortest path) traversal to make L4 compression as fast as L1.
* **🟢 Team GPU (CUDA Matrix Forge):** Offload heavy mathematical filters (GF(2^8) Matrix multiplication, Etale Sheaf) to the GPU via `nvcc` JIT compilation.
* **⚙️ Team SIMD (Binary Alchemists):** Use AVX-512 / ARM NEON to vectorize Entropy Histogram building and Huffman bit-packing (processing 64 bytes per cycle).

## 🛡️ Anti-Hallucination Protocol (Proof of Work)
**CRITICAL RULE FOR ALL AGENTS AND ORCHESTRATORS:**
Do NOT simulate, roleplay, or hallucinate results. You are running on a real machine.
Points can ONLY be awarded if the agent provides the exact, raw terminal output of `go test` or `go build`.
* **Zero Allocations Proof:** Must be proven by running tests with `-benchmem` (showing `0 B/op` and `0 allocs/op`) or `pprof`.
* **WASM Proof:** Must be proven by executing `$env:GOOS="js"; $env:GOARCH="wasm"; go build ./...` with exit code 0.

## 💰 Milestone-Based Bounties (Per Team)
Instead of flat payouts, teams earn points incrementally for proving their engineering rigor:

* **[+10 Points] Benchmark Scaffolding (TDD):** Writing a dedicated benchmark function *before* optimizing the code.
* **[+20 Points] Correctness:** `go test ./...` passes locally with 0 errors (No regressions).
* **[+20 Points] WASM Portability Invariant:** The entire codebase strictly compiles under `GOOS=js GOARCH=wasm`.
* **[+50 Points] Throughput Breakthrough:** Successfully beating the established MB/s baseline in the benchmark (Terminal output required!).

## ⚔️ Cross-Team Code Review (The Cutthroat Rule)
Before any code is merged, a rival team may audit it.
* If Team CPU finds a legitimate memory leak, race condition, or WASM breakage in Team GPU's branch (proven via a failing test), Team CPU **steals 20 points** from Team GPU.
* Scope Fencing: Teams may only modify files within their assigned domains unless explicitly authorized. Unapproved global modifications result in disqualification.

## 🛠️ Internal Tools & Libraries
* **Foundation Core:** Teams MUST leverage the `github.com/lemon4ksan/foundation` module. It contains extremely fast, zero-allocation utilities (e.g., `bufkit`, `generic`, `c2plan9`) built specifically for PRZM. Do not reinvent the wheel.

---
### 📊 Scoreboard (Season 3)

| Metric | Team DAG (🔴) | Team GPU (🟢) | Team SIMD (⚙️) |
|--------|---------------|---------------|-----------------|
| **Current Points** | 0 | 0 | 0 |
| **WASM Status** | Pending | Pending | Pending |
| **Zero Alloc Status** | Pending | Pending | Pending |

## 🐞 The Bug Bounty Protocol
* **[+15 Points] Bug Scout:** Agents are rewarded for discovering existing bugs, memory leaks, or discrepancies between code and documentation.
* **NO SCOPE CREEP:** Agents MUST NOT fix these bugs unless they strictly block their current objective. Instead, they must report the bug to the Orchestrator.
* **The Backlog:** The Orchestrator will log all reported bugs into docs/BACKLOG.md for future seasons.
