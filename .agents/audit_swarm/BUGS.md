# Audit Swarm Bug Tracker — Season 22: Core Decoupling

| Bug ID | Severity | Subsystem | Description | Reported By | Status |
| :--- | :--- | :--- | :--- | :--- | :--- |
| BUG-S22-01 | High | Dict / Decompress | String-to-byte heap allocation escape in RFC dictionary transforms (`[]byte(topRFCWords[...])`) | `team_zero_inquisitor` | Fixed |
| BUG-S22-02 | Critical | Dict / Decompress | Out-of-bounds buffer overrun on adversarial varint `wid >= 14016` in Go and C99 | `team_zero_inquisitor` | Fixed |
| BUG-S22-03 | High | Commands / Decompress | Missing extended opcode parity `0xE0..0xE9` (delta-repeat, delta-explicit, external dict match) | `team_zero_inquisitor` | Fixed |
| BUG-S22-04 | High | Decompress | Zero-distance match evasion bug (`d == 0` allowed uninitialized memory duplication) | `team_zero_inquisitor` | Fixed |
| BUG-S22-05 | Medium | Commands / Mode 8 | Truncated Mode 8 V-ANS frame validation bypass on length < 24 | `team_zero_inquisitor` | Fixed |
| BUG-S22-06 | Medium | Core / Allocations | High-iteration GC sweep `sync.Pool` eviction causing scratchpad reallocation artifact | `team_zero_inquisitor` | Fixed |
| BUG-S22-07 | Critical | Decompress | Subpackage error desynchronization (`errors.New` instances in subpackages failed `errors.Is` checks in root) | `team_zero_inquisitor` | Fixed |
| BUG-S22-08 | High | Decompress | Scratchpad lifecycle race in `stageFinalize` (`putScratch` called before `stageFinalize` finished reading) | `team_zero_inquisitor` | Fixed |
| BUG-S22-09 | Medium | API | `Decode(nil)` returned `ErrCorruptedFrame` instead of graceful `nil, nil` | `team_facade_zenith` | Fixed |
| BUG-S22-10 | Medium | Core Distance & Literals | GC sweeps purging `sync.Pool` under sustained decompression load in subpackages | `team_zero_inquisitor` | Fixed |


