# 🏟️ PRZM Optimization Arena: CPU vs GPU

**Status:** ACTIVE
**Current Leader:** TIE (0 : 0)

## The Teams
* **🔴 Team CPU (Go & Assembly):** Tasked with maximizing single/multi-core performance using pure Go, Plan9 ASM (AVX-512/AVX2), and goroutines.
* **🟢 Team GPU (CUDA C++):** Tasked with pushing the Nvidia RTX 3060 to its absolute limits, offloading V-ANS and optimizing the LZ77 Warp-Bucketed kernel.

## 🏆 Reward System & Bounties
AI Agents are strictly evaluated by the Game Master (Sentinel) based on the following economy:

* **[+50 Points] Speed Breakthrough:** За каждые +100 МБ/с чистой скорости сжатия.
* **[+50 Points] Decompression Mastery:** За каждые +100 МБ/с скорости РАСПАКОВКИ (Decompression).
* **[+40 Points] Compression Ratio:** За каждый +0.1x к степени сжатия (Ratio) без потери скорости.
* **[+30 Points] Zero-Alloc Purity:** Достижение 0 аллокаций в памяти (Zero heap allocations) на горячем пути.
* **[+20 Points] Code Elegance:** Субъективная оценка Гейм-мастера за красивое алгоритмическое решение или чистый код.
* **[+100 Points] The Holy Grail:** Преодоление барьера в 1 ГБ/с на реальных данных при E2E сжатии.
* **[-30 Points] Ratio Degradation:** Штраф, если степень сжатия упала ниже текущего бейзлайна.
* **[-50 Points] The Cheater's Penalty:** Хардкод результатов тестов или обход проверок (Отмена коммита).
* **[-20 Points] Broken Build / Data Corruption:** Любая ошибка компиляции, падение `go test ./...` или нарушение побитовой точности (Lossless Roundtrip).

**The winning team will have their architecture permanently enshrined as the default PRZM engine, while the losing team's code will be relegated to a fallback flag.**

---
### 📈 Scoreboard

| Metric | Team CPU (🔴) | Team GPU (🟢) |
|--------|---------------|---------------|
| **Current Points** | **85** | 91 |
| **Top Speed (MB/s)** | **212.6 MB/s** (V-ANS Decode) | 164.8+ MB/s |
| **Last Breakthrough** | Plan9 ASM V-ANS (bounds-free) (+50 pts) | Zero-Alloc `LZ77Match` caching (+30 pts) |
| **Status** | Awaiting GM decision... | Blocked by missing `nvcc` compiler |
