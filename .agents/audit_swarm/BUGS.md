# PRZM Defect Registry — Season 23

| Bug ID | Subsystem | Severity | Description | Status | Assigned Team |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **BUG-2301** | `core/commands` | **CRITICAL** | `emitExplicitMatch` inverts `nLit` and `lMatch - bMin` bitfields, overflows opcodes into `0xC0..0xFF` when $lMatch - bMin \ge 4$. | **CLOSED / VERIFIED (Season 23)** | `team_command_matrix` |
| **BUG-2302** | `core/commands` | **CRITICAL** | `PackCommandsMode8` copies raw slices without calling `EncodeVansMeta8`; condition `nPack < len(cmds)` is never true, causing 100% of command streams to fall back to `0x09` flat mode. | **CLOSED / VERIFIED (Season 23)** | `team_command_matrix` |
| **BUG-2303** | `compress.go` / `src/przm_comp.c` | **CRITICAL** | Flag bitmasks mismatch in `src/przm_comp.c` (`FLAG_EXT_OFFSET 0x02` vs `0x08`, `FLAG_MIN_MATCH_3 0x20` vs `0x40`), and `assembleCompressedFrame` fails to set `FlagExtOffset (0x08)` for sections > 65535 B. | **CLOSED / VERIFIED (Season 23)** | `team_distance_framing` |
| **BUG-2304** | `core/distance` | **MAJOR** | `PackageOffsetsSection` only uses `0x06` or `0x09`, ignoring compact modes `0x0A` (7B) and `0x0B` (4B) supported by decoders. | **CLOSED / VERIFIED (Season 23)** | `team_distance_framing` |
| **BUG-2305** | `compress.go` / `core/dict` | **MAJOR** | Static dictionary stub `_ = dict` leaves RFC 7932 122 KB Brotli dictionary, `CompactPairs[2048]`, and `DiffLattice (0xCE)` completely inactive during compression. | **CLOSED / VERIFIED (Season 23)** | `team_dict_trellis` |
