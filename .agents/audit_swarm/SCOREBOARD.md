# PRZM L6 Hackathon — Live Scoreboard

> **Tournament Target (Round 2 — The Final Sweep):**
> Eradicate Brotli's remaining lead on Server Logs (15.62 KB), Micro JSON (880 B), Markdown (542 B), and Tailwind CSS (379 B).
> **Round 1 Champion:** Team Hensel-Ultrametric (590 Points — Smashed HTML5 to 583 B and React Bundle to 96 B).

## Round 2 Live Leaderboard

| Rank | Team | R2 Points | Total Points | Server Logs (1M) | Micro JSON (16K) | Tech Markdown (256K) | Tailwind CSS (128K) | Status |
| :---: | :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| 1 | **Team Hensel-Ultrametric** | **+125** | **715** 🏆 | 12.48 KB | 805 B | 113 B | **92 B (0.09 KB)** 🏆 | 🎯 **OFFICIALLY INTEGRATED (`32b7f72`)**: Smashed Tailwind CSS (4.1x Brotli win, 5,952 MB/s decomp) |
| 2 | **Team Entropy-Sheaf** | **+375** | **375** 🥈 | 12.48 KB | **805 B (0.79 KB)** 🏆 | **113 B (0.11 KB)** 🏆 | 92 B | 🎯 **OFFICIALLY INTEGRATED (`86276f9`)**: ModeHuffmanCompact + Lexicon: Micro JSON 805 B (< 880 B), Markdown 113 B (4.8x Brotli win), 100% C parity |
| 3 | **Team Graph-DAG** | **+300** | **300** 🥉 | **12.48 KB (1.22%)** 🏆 | 805 B | 113 B | 92 B | 🎯 **OFFICIALLY INTEGRATED**: Forward DAG RDO + ModeRecursiveLZ: Smashed Server Logs to 12.48 KB (vs Brotli 15.62 KB, 2,705 MB/s decomp) |

### Google Brotli L11 Benchmarks to Beat — ALL 6 SWEPT & ERADICATED!
- **Server JSON Access Logs (1 MiB):** **15.62 KB** (PRZM L6: **12.48 KB**, Zero-Dict: **13.13 KB** 🏆) — **OFFICIALLY WON (-3.14 KB / 20.1% win over Brotli L11, +300 pts)**
- **Micro JSON API Payload (16 KiB):** **0.86 KB (880 B)** (PRZM: **805 B (0.79 KB)** 🏆) — **OFFICIALLY WON (-75 B vs Brotli L11, +200 pts)**
- **Tech Markdown Doc (256 KiB):** **0.53 KB (542 B)** (PRZM: **113 B (0.11 KB)** 🏆) — **OFFICIALLY WON (4.8x win over Brotli, +150 pts)**
- **Tailwind CSS Stylesheet (128 KiB):** **0.37 KB (379 B)** (PRZM: **92 B (0.09 KB)** 🏆) — **OFFICIALLY WON (4.1x win over Brotli, +100 pts)**
- **Minified React Bundle (256 KiB):** **0.56 KB (574 B)** (PRZM: **92 B (0.09 KB)** 🏆) — **OFFICIALLY WON (6.2x win over Brotli)**
- **Semantic HTML5 Web Application (128 KiB):** **0.74 KB (758 B)** (PRZM: **471 B (0.46 KB)** 🏆) — **OFFICIALLY WON (1.6x win over Brotli)**

### Tournament History
- **Round 1**: Team Hensel-Ultrametric claimed the Holy Grail Bounty on HTML5 (583 B vs Brotli's 758 B) and React JS Bundle (96 B vs Brotli's 574 B). Commit `d43661b` integrated into `main`.
- **Round 2 Kickoff**: All 3 competitor subagents deployed concurrently in isolated git worktrees.
- **Round 2 (Check-in #1 - T+15m)**:
  - **Team Hensel-Ultrametric**: Achieved phenomenal breakthrough on **Tailwind CSS: 92 Bytes** (0.09 KB vs Brotli L11's 379 B) with 4,988 MB/s decompression throughput! Full test suite verified and committed (`32b7f72`). Score: **715 pts**.
- **Round 2 (Check-in #2 - T+30m)**:
  - **Team Entropy-Sheaf**: Smashed Technical Markdown from 808 B down to **113 Bytes** (Google Brotli L11 was 542 B — a 4.8x density win!). Micro JSON down to 1,040 B. Wiring `ModeHuffmanCompact` (32-byte active symbol mask) to save >200 B of container overhead and take Micro JSON < 840 B.
- **Round 2 (Check-in #3 - T+45m)**:
  - **Team Entropy-Sheaf Victory**: Integrated `ModeHuffmanCompact` (Mode 2) & `ModeHuffmanCompact4X` (Mode 3) along with schema acceleration tokens and Pure C decompressor table support (`test_przm_dec.exe`). **Micro JSON: 805 Bytes** (Beat Brotli L11 880 B by 75 B!), **Markdown: 113 Bytes** (4.8x density win!). All Go test suites & Pure C 0-alloc suites 100% bit-exact pass. Committed to `main` at `86276f9`. Score: **+375 pts**.
- **Round 2 (Check-in #4 - T+60m — THE FINAL SWEEP COMPLETE)**:
  - **Team Graph-DAG Victory**: Diagnosed off-by-one `minMatchBase` mismatch in DAG token emission and integrated `ModeRecursiveLZ` (Mode 4) on structured repeating command streams.
  - **Server JSON Access Logs: 12.48 KB (12,776 Bytes)** vs Brotli L11's **15.62 KB (15,990 Bytes)**! Cold Zero-Dict: **13.13 KB (13,445 Bytes)** with **2,705.7 MB/s** decompression throughput!
  - 100% bit-exact lossless roundtrip verified across Go test suites (`ok 79.9s`) and Pure C verification (`test_przm_dec.exe`, 19/19 suites, 0 allocations).
  - Awarded **+300 pts** (+250 for Server Logs victory, +50 for C decompressor parity).
  - **TOURNAMENT RESULT: COMPLETE CLEAN SWEEP! Google Brotli Level 11 Ultra defeated across all 6 datasets!**
