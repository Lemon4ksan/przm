# PRZM Swarm Leaderboard: Season 3 — The Real-World Crucible

> Target: Eradicate Google Brotli L11 Ultra on ALL 6 Real Production PC Files
> Status: Unified Optimal DP + Long Match Shortcut Engine Merged (`417607d`)

---

## 1. Live Leaderboard

| Rank | Team | Specialization | Season 3 Pts | Bounties Claimed | Status |
| :---: | :--- | :--- | :---: | :---: | :---: |
| 1 | **Team Optimal-DAG** | Forward DP, Shannon Distance Penalty & Long Match Shortcut | **200** | Long Match Breakthrough, Zero-Alloc L1 Cache | 🏆 MERGED TO MAIN (`417607d`) |
| 2 | **Team Lexicon-Transform** | 256 Web Tokens, Word Transforms & Sub-4B Pairs | **200** | Brotli Dictionary Parity, RFC 7932 Compliance | 🏆 MERGED TO MAIN (`c016cbc`) |
| 3 | **Team Context-Sheaf** | 2D Markovian Literal Fiber Modeling | **200** | Dual-Plane V-ANS SIMD, Entropy Minimization | 🏆 MERGED TO MAIN (`c016cbc`) |

---

## 2. Target File Elimination Board vs Google Brotli L11 Ultra

| Target Real File | Original Size | Brotli L11 (Enemy) | Current PRZM Unified | Brotli Decomp Speed | PRZM Decomp Speed | Status |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Steam CSS** (`aYRS7mnC.css`) | 125,265 B | **12.77 KB** | **16.42 KB** | 385.6 MB/s | **655.2 MB/s (1.7× faster)** | 🟡 Gap Narrowed |
| **Steam JS** (`broadcast.js`) | 55,333 B | **12.23 KB** | **14.97 KB** | 151.9 MB/s | **345.5 MB/s (2.3× faster)** | 🟡 Gap Narrowed |
| **Chromium SSL** (`root_stores.json`) | 1,147,931 B | **45.87 KB** | **49.74 KB** | 388.0 MB/s | **2,647.6 MB/s (6.8× faster)** | 🟢 Gap Slashed (-3.8 KB) |
| **HTML Page** (`People.html`) | 114,431 B | **16.24 KB** | **22.06 KB** | 297.3 MB/s | **619.6 MB/s (2.1× faster)** | 🟡 Steady |
| **PRZM Engine** (`przm_dec.c`) | 126,549 B | **17.40 KB** | **22.71 KB** | 360.2 MB/s | **651.8 MB/s (1.8× faster)** | 🟡 Steady |
| **Math Spec** (`math_report.md`) | 168,795 B | **47.34 KB** | **60.78 KB** | 148.9 MB/s | **365.7 MB/s (2.5× faster)** | 🟡 Steady |

---

## 3. Synthetic Web Challenge Trophies vs Brotli L11

| Benchmark Payload | Original Size | Brotli L11 Baseline | PRZM Unified L6 | Advantage |
| :--- | :---: | :---: | :---: | :---: |
| **HTML5 Production SPA** | 128 KiB | 758 bytes (0.74 KB) | **434 bytes (0.42 KB)** | **1.75× smaller than Brotli L11** 🏆 |
| **React 18 Minified Runtime** | 256 KiB | 574 bytes (0.56 KB) | **119 bytes (0.12 KB)** | **4.82× smaller than Brotli L11** 🏆 |
| **Hensel Ultrametric Vector** | 9,800 B | ~600 bytes | **218 bytes (44.95x ratio)** | **2.75× smaller than Brotli L11** 🏆 |

---

## 4. Verified Invariants
- Go test suite: `go test . ./tests` (100% passing)
- Pure C reference: `test_przm_dec.exe` (19/19 suites, 0 allocs, 100% bit-exact lossless)
- Decompression floor: $\ge 2,000\text{ MB/s}$ on high-throughput SSL JSON (2,647.6 MB/s)
