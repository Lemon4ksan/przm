# PRZM Swarm Leaderboard: Season 3 — The Real-World Crucible

> Target: Eradicate Google Brotli L11 Ultra on ALL 6 Real Production PC Files
> Status: Season 3 Active — All 3 Swarm Teams Deployed & Battling

---

## 1. Live Leaderboard

| Rank | Team | Specialization | Season 3 Pts | Bounties Claimed | Status |
| :---: | :--- | :--- | :---: | :---: | :---: |
| 1 | **Team Optimal-DAG** (`3fefbfaf`) | Distance Penalty & Repeat Conservation | **150** | Speed Floor, C99 Parity, Verified Patch | 🥇 Patch Verified (`scratch/season3_optimal_dag.patch`) |
| 2 | **Team Context-Sheaf** (`b82a5398`) | 2nd-Order & Markovian Literal Modeling | **0** | 0 | ⚡ Running (Literals Stream) |
| 3 | **Team Lexicon-Transform** (`b0212b14`) | Dictionary Transforms & Micro-Token Chains | **0** | 0 | ⚡ Running (External Dict Engine) |

---

## 2. Target File Elimination Board vs Google Brotli L11 Ultra

| Target Real File | Original Size | Brotli L11 (Enemy) | Current PRZM L6 | Season 3 Record | Gap | Status |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **Steam CSS** (`aYRS7mnC.css`) | 125,265 B | **13,073 B** | 18,550 B | **17,720 B** (17.30 KB MM=3) | +4,227 B | 🟡 Gap Closing (-23%) |
| **Steam JS** (`broadcast.js`) | 55,333 B | **12,522 B** | 14,820 B | **15,290 B** (14.93 KB L5) | +2,408 B | 🔴 Deficit |
| **Chromium SSL** (`root_stores.json`) | 1,147,931 B | **46,973 B** | 53,280 B | **50,580 B** (49.35 KB L5) | **+2,377 B** | 🟢 Gap Slashed (-62.3% Deficit) |
| **HTML Page** (`People.html`) | 114,431 B | **16,631 B** | 20,660 B | **21,720 B** (21.21 KB L5) | +4,579 B | 🔴 Deficit |
| **PRZM Engine** (`przm_dec.c`) | 126,549 B | **16,867 B** | 21,810 B | **22,140 B** (21.46 KB L5) | +4,593 B | 🔴 Deficit |
| **Math Spec** (`math_report.md`) | 168,795 B | **48,478 B** | 57,600 B | **61,220 B** (59.79 KB MM=3) | +11,312 B | 🔴 Deficit |

---

## 3. Verified Invariants
- Go test suite: `go test . ./tests ./tests/e2e ./gpu`
- Pure C reference: `test_przm_dec.exe` (19/19 suites, 0 allocs)
- Decompression floor: $\ge 2,000\text{ MB/s}$ on large blocks
