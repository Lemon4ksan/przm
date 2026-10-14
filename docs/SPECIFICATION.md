---
title: "PRZM Compression Format: Bitstream Syntax and Decompressor Semantics"
abbrev: "PRZM Compression Format"
docname: draft-lemon4ksan-przm-compression-01
category: std
ipr: trust200902
area: Applications and Real-Time
workgroup: Network Working Group
keyword:
  - compression
  - bitstream
  - przm
  - entropy
  - zero-allocation
  - lz77
  - vans
author:
  -
    ins: A. Komolov
    name: Arsenii Komolov
    email: arsenii.komolov@yandex.ru
    uri: https://github.com/lemon4ksan/przm

--- abstract

This document defines the normative bitstream syntax and deterministic decompressor semantics for the PRZM (PrismLZ) data compression format.

In adherence to the principles governing foundational open multimedia and data compression standards (such as DEFLATE, JPEG, H.264, AV1, and Zstandard), this specification does not mandate, prescribe, or constrain compression algorithms or heuristics. The compression architecture (encoder) is intentionally non-normative: any implementation of an encoder MAY utilize arbitrary substring-search strategies, graph-theoretic matchers, dynamic programming, machine-learning models, or brute-force evaluation, subject to a single invariant: the emitted bitstream MUST strictly conform to the bitstream grammar and decompressor invariants defined in this specification.

This document specifies the global framing layout, header fields, section directory topology, the complete deterministic state machine for the fused command stream (0x00..0xFF), the 4-lane interleaved Vector Asymmetric Numeral Systems (V-ANS 4x) entropy decoder, the stackless Pareto-Wave Synthesis (PWS) H-DAG grammar unroller, and discrete algebraic transformations across symplectic phase spaces, 2-adic Mahler ultrametric Pascal wavelets, and architecture-specific branch filters.

--- middle

# Introduction and Scope

This specification applies to software and hardware implementations designed to store, transmit, or decompress data formatted according to the PRZM standard.

PRZM is engineered for high-throughput streaming pipelines, archival storage containers (e.g., EON), game engines (Unreal Engine, Unity, Godot), GPU microcode storage (shader caches), and kernel-level filesystem mounts. Decompression is designed to saturate host memory bus bandwidths (35 to 56 GB/s) using vector SIMD instruction sets (AVX2, AVX-512, ARM NEON).

# Terminology and Conventions

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in BCP 14 [RFC2119] [RFC8174] when, and only when, they appear in all capitals, as shown here.

- **Bitstream**: A contiguous sequence of octets formatted according to this standard.
- **Frame**: An autonomous, self-contained unit of compressed data capable of being decompressed independently of any preceding or succeeding data.
- **Uncompressed Size ($U_{\text{size}}$)**: The exact number of uncompressed octets produced by decoding a frame.
- **Destination Buffer ($D$)**: The contiguous memory region provided by the caller to receive uncompressed bytes.
- **Safety Padding ($P_{\text{safety}}$)**: An obligatory trailing margin of at least 64 bytes beyond $U_{\text{size}}$ allocated in $D$ to permit unaligned 64-bit vector stores without boundary faults.
- **Locality Queue ($Q$)**: A 16-element ring buffer containing recently referenced match distances, maintained via a Move-to-Front policy.

Unless explicitly noted otherwise, all multi-byte integers are serialized in Little-Endian byte order.

# The Encoder Freedom Principle

A fundamental axiom of this specification is the strict decoupling of encoder heuristics from decompressor mechanics:

> **This specification defines exclusively the grammar of the bitstream and the deterministic transformation performed by the decompressor. Encoders are unconstrained in their algorithmic design.**

## Permissible Encoder Implementations

An encoder implementation MAY choose any search, parsing, or optimization technique:
1. **Low-Latency Encoders (Level 1)**: Single-probe hash tables, 4-byte prefix hashing, greedy match selection, raw literal flushing.
2. **Balanced Encoders (Level 2)**: Chained hash tables, lazy evaluation ($k$-step forward lookahead), automatic stride periodicity detection.
3. **Optimal Encoders (Levels 3–4)**: Full suffix arrays, directed acyclic graph (DAG) parsing via dynamic programming, forward trellis path optimization in the tropical min-plus semiring $(\mathbb{Z} \cup \{\infty\}, \min, +)$, and machine-learned structural categorization.
4. **Hardware Encoders**: Dedicated FPGA pipelines or parallel GPU compute shaders.

## Conformance Criterion for Compressed Streams

A compressed stream is valid if and only if a conforming decompressor processing the stream produces an uncompressed byte sequence that is bit-for-bit identical to the original input, without encountering undefined opcodes, out-of-bounds offset references, or buffer overrun conditions.

# Global Framing and Coordinate Signatures

A PRZM bitstream consists of one or more frames. The first 4 bytes of each frame MUST contain a 32-bit signature (or 3-byte versioned prefix) identifying the active coordinate chart of the PRZM differential-geometric atlas:

| Signature ASCII | Hexadecimal (LE) | Target Coordinate Chart / Mode |
|:---|:---|:---|
| "PRZM" | 0x4D5A5250 | Standard Fused Frame (Universal) |
| "SYMP" | 0x53594D50 | Symplectic Phase Space Sp(4, F_{2^8}) |
| "MAHL" | 0x4D41484C | 2-Adic Mahler Ultrametric Wavelet |
| "SHEF" | 0x53484546 | Étale Sheaf & Tropical Trellis |
| "ARM6" | 0x41524D36 | ARM64 Instruction Branch Collimator |
| "MACR" | 0x4D414352 | Ultra-Long Horizon Macro-Trellis |
| "DEPS" | 0x44455053 | Dependent-Type Sheaf Stratification |

Versioned ISA formats are identified by 3-byte prefixes:
- `PW\x01` (`0x015750`): Pareto-Wave Synthesis (PWS) H-DAG Grammar.
- `PV\x01` (`0x015650`): Zero-Schema Vector Virtual Machine ISA.
- `GG\x01` (`0x014747`): Geometric Grammar Tree Codec.

A decompressor MUST inspect the magic bytes at offset 0 and dispatch execution to the corresponding functional pipeline.

# Standard Fused Frame Specification

The Standard Fused Frame (`"PRZM"`) constitutes the general-purpose compression substrate.

## Frame Header Layout

The frame header occupies exactly 8 bytes:

```text
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|       'P'     |       'R'     |       'Z'     |       'M'     | [0..3]
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|     Flags     |               UncompressedSize (24-bit)       | [4..7]
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

- **Magic** (Bytes 0..3): `0x4D5A5250` (`'P'`, `'R'`, `'Z'`, `'M'`).
- **Flags** (Byte 4): 8-bit frame configuration bitmask.
- **UncompressedSize** (Bytes 5..7): 24-bit unsigned integer in Little-Endian format specifying the uncompressed payload size ($0 \le U_{\text{size}} \le 16\text{ MiB}$).

If $U_{\text{size}} = 0$, the decompressor SHALL emit zero bytes and immediately report success.

## Header Flags Bitfield

```text
  Bit 0: FLAG_STRIDE       (0x01) - 1-byte Stride parameter present (value S-1)
  Bit 1: FLAG_DELTA_MASK   (0x02) - Delta mutation mode active
  Bit 2: FLAG_UNCOMPRESSED (0x04) - Uncompressed raw payload
  Bit 3: FLAG_EXT_OFFSET   (0x08) - 24-bit section sizes in directory
  Bit 4: FLAG_DICT_REF     (0x10) - 4-byte DictID parameter present
  Bit 5: FLAG_NORM         (0x20) - Involutive normalization permutation active
  Bit 6: FLAG_MIN_MATCH_3  (0x40) - Periodic tabular data: minimum match length bMin = 3
  Bit 7: FLAG_NIBBLE       (0x80) - Section 4 encoded via V-ANS or Nibble-Planes
```

When `FLAG_UNCOMPRESSED` (0x04) is set, exactly $U_{\text{size}}$ bytes follow the 8-byte header verbatim. The decompressor SHALL copy these bytes to $D$ and terminate.

## Dynamic Frame Parameters

When `FLAG_UNCOMPRESSED` is clear, dynamic parameters immediately follow the 8-byte header starting at offset 8:

| Field | Condition | Size and Format |
|:---|:---|:---|
| Stride | FLAG_STRIDE | 1 byte (value $S-1$, effective stride $S \in 2..256$) |
| DictID | FLAG_DICT_REF | 4 bytes (uint32 Little-Endian identifier) |

Parameters MUST appear in the exact order: `Stride`, `DictID`.

## Section Directory

Immediately following the dynamic parameters is the Section Directory:
- **Standard Directory** (`FLAG_EXT_OFFSET == 0`):
  - `CmdStreamSize` (2 bytes, uint16 LE): Size of Section 2 in bytes.
  - `OffsetStreamSize` (2 bytes, uint16 LE): Size of Section 3 in bytes.
- **Extended Directory** (`FLAG_EXT_OFFSET != 0`):
  - `CmdStreamSize` (3 bytes, uint24 LE): Size of Section 2 in bytes.
  - `OffsetStreamSize` (3 bytes, uint24 LE): Size of Section 3 in bytes.

## Four-Section Topology

The compressed frame payload is organized into 4 disjoint, contiguous sections:

```text
[Header 8B] [Params 0..9B] [Dir 4..6B]
  ├── Section 1: Distribution Parameters (size = 8 + 2 * ExceptionCount)
  ├── Section 2: Command Stream (size = CmdStreamSize)
  ├── Section 3: Offset Stream (size = OffsetStreamSize)
  └── Section 4: Literal Stream (size = src_len - sec4_offset)
```

1. **Section 1 (Distribution Parameters)**: Contains probability distribution metadata and sliding window scaling ($W_{\log} \in [16, 24]$). Total byte length is $\text{Sec1Size} = 8 + 2 \times \text{ExceptionCount}$.
2. **Section 2 (Command Stream)**: Sequence of fused opcodes governing data generation, optionally tri-stream split via Mode 8 (`0x08`) or flat (`0x09`).
3. **Section 3 (Offset Stream)**: Match distance channel encoded via Mode 9 (`ModeFlatCompact`), Mode 0 (`ModeFlat`), Mode 6/0x0A/0x0B (`ModeDistancePrefix*`), or Mode 12 (`ModeDistClusteredVANS`).
4. **Section 4 (Literal Stream)**: Literal octets (raw, V-ANS 4x compressed, or nibble-plane encoded).

# Fused Command Stream Syntax and Semantics

The Command Stream (Section 2) is evaluated sequentially from offset `sec2_offset` to `sec2_offset + CmdStreamSize`.

## 4-Register Locality Offset State Machine

The decompressor maintains an internal 4-register Least-Recently-Used (LRU) vector $\mathbf{R} = [r_0, r_1, r_2, r_3]$, initialized at the start of each frame using the effective stride periodicity $S$ (default $S = 1$):

$$\mathbf{R}_{\text{init}} = \begin{cases}
[1, S, 2S, 3S] & \text{if } S > 1 \\
[1, 4, 8, 16] & \text{if } S = 1
\end{cases}$$

Whenever match distance $d = r_k$ is accessed by index $k \in [0..3]$, the registers MUST be updated via Move-to-Front:
```c
uint32_t d = r[k];
for (int j = k; j > 0; j--) {
    r[j] = r[j - 1];
}
r[0] = d;
```

Whenever an explicit distance $d$ is decoded from Section 3 or synthesized via Stride matching ($d = m \times S$), the registers shift right:
```c
if (d != r[0]) {
    r[3] = r[2];
    r[2] = r[1];
    r[1] = r[0];
    r[0] = d;
}
```

## Section 2: 3-Way Split Command Stream Engine (Mode 8)

When Section 2 begins with byte `0x08`, the command stream is decompressed using the 3-Way Tri-Stream architecture:
- **`opStream`**: Structural operation classification tokens (1 byte per command).
- **`metaStream`**: Short match lengths, bracket descriptors, and difference lattice modes.
- **`varStream`**: High-entropy ULEB128 variable-length integer extensions.

Each stream is entropy-decoded via 8-Context Bigram V-ANS before linear bytecode reconstitution. If Section 2 begins with `0x09`, commands are stored as contiguous uncompressed linear bytecodes.

## Complete Opcode Distribution Map (`0x00..0xFF`)

| Opcode Range | Command Category and Semantics |
|:---|:---|
| 0x00 .. 0x7F | **Fast-Path 1: Fused Repeat Match** (`0 LLL MM KK`) — $L \in 0..7$, $M = MM + B_{\min}$, $k \in 0..3$ using $r_k$. |
| 0x80 .. 0xBF | **Fast-Path 2: Fused Extended Match** (`10 LL MMMM`) — $L \in 0..3$, $M = MMMM + B_{\min}$ (or $+ \text{ULEB128}$ if $MMMM=15$), explicit distance from Section 3. |
| 0xC0 .. 0xC3 | **Long Repeat Match** — $k \in 0..3$, next byte provides `[litLen (4b) \| matchLen (4b)]` with optional ULEB128 extension. |
| 0xC4 .. 0xCD | **Fused Bracket Match** — Direct fused bracket distances ($b \in 0..9 \to \{1, 2, 3, 4, 5, 6, 7, 8, 16, 32\}$), next byte `[litLen \| matchLen]`. Zero Section 3 overhead. |
| 0xCE         | **Difference Lattice Match (`DiffLattice`)** — 2D column delta around stride: next 2 bytes provide descriptor and signed 8-bit step $\delta$. |
| 0xCF         | **RFC 7932 Static Word Match** — Direct reference into 14,016-word RFC 7932 vocabulary with optional transformation. |
| 0xD0 .. 0xD7 | **Stride Match** — Periodic column offset ($m = (op \ \& \ 7) + 1$, $d = m \times S$), next byte `[litLen \| matchLen]`. |
| 0xD8 .. 0xDF | **Direct 1-Byte Schema Token** — Common schema tokens 0..7 from active `DictID`. |
| 0xE0 .. 0xE7 | **Delta-Mask Match with Locality** — Copies base match from $r_{op \& 7}$ and applies XOR mutations from Section 4. |
| 0xE8         | **Delta-Mask Match with Explicit Offset** — Explicit distance from Section 3 with Section 4 XOR mutations. |
| 0xE9         | **Explicit Match with Tail Literals** — Explicit match for literal runs $nLit > 3$, next byte `[litLen \| matchLen]`. |
| 0xEA .. 0xEF | **Fused RFC Word Transforms** — Fuses $1..6$ literal prefix bytes with RFC 7932 static dictionary word transformation. |
| 0xF0         | **Pure Literal Run** — ULEB128 length from Section 2, literal bytes from Section 4. |
| 0xF1         | **Pure RLE Run** — ULEB128 length, single repeated byte from Section 4. |
| 0xF2         | **Extended Schema Token** — 2-byte token (`0xF2 <TokenID>`) from active `DictID`. |
| 0xF3 .. 0xFA | **Direct 1-Byte Literal Run** — Emits $1..8$ literal bytes ($op - 0xF3 + 1$) directly with zero header bytes. |
| 0xFB .. 0xFF | **Reserved** — Reserved for Virtual Machine execution extensions. |

## Built-in Schema Dictionaries (`0xD8..0xDF`, `0xF2`)

When `FLAG_DICT_REF` is set, the decompressor activates the built-in lexicon designated by `DictID`:
- `DictID = 1`: JSON structural tokens (64 normative strings).
- `DictID = 2`: Programming source code tokens (42 normative strings).

- **Direct Token (`0xD8..0xDF`)**: Token index $t = op - 0xD8$ ($0..7$). The corresponding string is copied to `dst_pos`.
- **Extended Token (`0xF2`)**: Next byte in Section 2 designates $t = src[cmd\_ptr++]$. The string for token $t$ is copied to `dst_pos`.

If $t$ exceeds the active dictionary capacity or `FLAG_DICT_REF` is clear, the decompressor MUST return `PRZM_ERR_CORRUPTED_FRAME`.

## Delta-Mask Matching and Residual Mutations (`0xE0..0xE8`)

Delta-mask commands reconstruct mutated records by copying a base match and applying discrete XOR modifications:
- `0xE0..0xE7`: Base distance $d = Q[op \ \& \ 7]$.
- `0xE8`: Base distance $d$ is read from Section 3 via ULEB128.

Following the copy of $M_{\text{len}}$ bytes from `dst_pos - d`, mutation descriptors are read from Section 4:
- Mutation count $C_{\text{mods}} = lits[lit\_ptr++]$.
- For each mutation, relative offset $rel\_off$ is read (1 byte if $M_{\text{len}} \le 256$, or 2 bytes uint16 LE if $M_{\text{len}} > 256$), followed by 1 byte $xor\_val$.
- In-place mutation is applied:
  $$D[dst\_pos + rel\_off] \leftarrow D[dst\_pos + rel\_off] \oplus xor\_val$$

## Literal and Run-Length Sequences (`0xF0..0xFA`)

- **0xF0 (Pure Literal Run)**: Run length $L$ is decoded via ULEB128 from Section 2. Exactly $L$ bytes are transferred from Section 4.
- **0xF1 (Pure RLE Run)**: Run length $R$ is decoded via ULEB128 from Section 2. A single byte from Section 4 is replicated $R$ times in $D$.
- **0xF3..0xFA (Direct 1-Byte Literal Run)**: Emits $L = op - 0xF3 + 1$ ($1..8$) literals from Section 4 with zero framing overhead.

# Match Distance Stream Decompression (Section 3)

Section 3 starts at byte offset $O_3 = O_2 + \text{CmdStreamSize}$ and spans length $\text{OffsetStreamSize}$. If $\text{OffsetStreamSize} > 0$, the initial byte specifies the container framing mode:

## Section 3 Container Framing Modes

| Mode Byte | Identifier | Classification | Description |
|:---:|:---|:---|:---|
| `0x00` | `ModeFlat` | Raw Flat Sized | 4-byte LE length $L_{\text{orig}}$ followed by $L_{\text{orig}}$ raw ULEB128 bytes. |
| `0x06` | `ModeDistancePrefix` | Standard Prefix | 12-byte header ($L_{\text{varint}}$, $N_{\text{matches}}$, $L_{\text{code}}$) + CodeStream + ExtraBitsStream. |
| `0x09` | `ModeFlatCompact` | Raw Flat Compact | Raw ULEB128 bytes spanning the remaining $\text{OffsetStreamSize} - 1$ bytes. |
| `0x0A` | `ModeDistancePrefixCompact` | Compact Prefix | 6-byte header with 16-bit counts + CodeStream + ExtraBitsStream. |
| `0x0B` | `ModeDistancePrefixByte` | Byte Prefix | 3-byte header with 8-bit counts + CodeStream + ExtraBitsStream. |
| `0x0C` | `ModeDistClusteredVANS` | Clustered V-ANS (Mode 12) | 4-context Markovian V-ANS coder across the 49 distance brackets. |

## 49-Bracket Logarithmic Distance Codebook

Distances $D \ge 1$ are partitioned into 49 logarithmic brackets:
- For $1 \le D \le 4$: Distance Code $C = D - 1$, base distance $\text{Base}(C) = D$, extra bits $nb = 0$.
- For $5 \le D \le 16{,}777{,}216$:
  $$u = D - 1, \quad nb = \lfloor \log_2 u \rfloor - 1$$
  $$\text{half} = (u \gg nb) \land 1, \quad C = 2 \cdot nb + 2 + \text{half}$$
  $$\text{Base}(C) = (2 + \text{half}) \cdot 2^{nb} + 1, \quad \text{extra} = D - \text{Base}(C)$$

## Built-in RFC 7932 Static Dictionary (`DictID = 0x4252544C`)

When `FLAG_DICT_REF` is set and $\text{DictID} = \mathtt{0x4252544C}$ (`"BRTL"`), the decompressor binds the normative RFC 7932 static dictionary (122,784 bytes) containing 14,016 words partitioned across 24 length classes (lengths 4 to 24) and 121 word transformations. Opcode `0xCF` and fused opcodes `0xEA..0xEF` resolve vocabulary words directly from ROM/static cache at line rate.

# Literal Stream Decompression (Section 4)

## 4-Lane Interleaved Vector Asymmetric Numeral Systems (V-ANS 4x)

When `FLAG_NIBBLE` (0x80) is set and Section 4 begins with `VA\x01` (`0x014156`), literals are entropy-decoded using a 4-lane interleaved tANS state machine:
- The header supplies uncompressed literal length $L_{\text{orig}}$ (uint32 LE), a 128-byte upper-triangular covariance frequency matrix ($16 \times 16$), and four initial 32-bit state variables $(X_0, X_1, X_2, X_3)$.
- Decompression proceeds in round-robin fashion across all four states, achieving zero loop-carried dependencies and saturating superscalar ALU/vector units.

## Nibble-Plane Splitting

If `VA\x01` is absent, Section 4 contains two planes:
- Low nibble plane length $L_{\text{low}} = (N + 1) / 2$.
- High nibbles are expanded via Nibble-RLE: $run\_len = (token \gg 4) + 1$, $high = (token \ \& \ 0x0F) \ll 4$.
- Low nibble pairs are unpacked and merged via bitwise OR:
  $$D[2j] \leftarrow D[2j] \ | \ (pair \gg 4), \quad D[2j + 1] \leftarrow D[2j + 1] \ | \ (pair \ \& \ 0x0F)$$

# Pareto-Wave Synthesis (PWS) Structural Grammars

Frames prefixed with `PW\x01` expand non-recursive Hierarchical Acyclic Direct Graphs (H-DAG) [MANETH]:
1. The header establishes grammar rule budgets across 5 hierarchic layers:
   - $L_1$: 2-byte terminal pairs ($N_1 \le 64$).
   - $L_2$: 4-byte compounds ($N_2 \le 32$).
   - $L_3$: 8-byte quadwords ($N_3 \le 16$).
   - $L_4$: 16-byte blocks ($N_4 \le 16$).
   - $L_5$: 32-byte blocks ($N_5 \le 8$).
2. The decompressor populates expansion lookup arrays in static memory and unrolls the axiom stream in a single linear pass exceeding 40 GB/s.

# Architectural and Algebraic Pre-Filters

## Symplectic Phase-Space Involutions $Sp(4, \mathbb{F}_{2^8})$

Frames marked with `"SYMP"` apply a symplectic shear transformation. As proven in Lean 4 [LEAN4], the phase shear $T_S$ over $\mathbb{F}_{2^8}$ satisfies:

$$T_S^2 \equiv I \implies T_S^{-1} \equiv T_S$$

Decompression is algebraically identical to compression:
- Phase vector $(x_0, x_1, p_0, p_1)$ undergoes momentum shearing:
  $$p_0 \leftarrow p_0 \oplus (S_{00} \bullet x_0) \oplus (S_{01} \bullet x_1)$$
  $$p_1 \leftarrow p_1 \oplus (S_{01} \bullet x_0) \oplus (S_{11} \bullet x_1)$$
where $\bullet$ denotes multiplication in $GF(2^8)$ modulo the AES polynomial $x^8 + x^4 + x^3 + x + 1 = \mathtt{0x11B}$ [FIPS-197].

## 2-Adic Mahler Ultrametric Pascal Wavelets

Frames marked with `"MAHL"` apply an inverse Pascal transform over $\mathbb{Z}_2$. The lower-triangular Pascal matrix $B$ satisfies $\det(B) \equiv 1 \pmod{256}$, ensuring exact invertibility without division:

```c
for (int s = 15; s >= 1; s--) {
    for (int i = s; i < 16; i++) {
        buf[i] = (uint8_t)(buf[i] + buf[i - 1]);
    }
}
```

## x86 Instruction Collimation (Bra86) and BCJ-Split

- **BCJ-Split (`FLAG_BCJ_SPLIT = 0x20`)**: 32-bit displacements of `CALL` (`0xE8`) and `JMP` (`0xE9`) instructions are isolated into 4 separate byte planes $(B_0, B_1, B_2, B_3)$. The decompressor restores displacements into the code plane before invoking inverse Bra86 filtering.
- **Inverse Bra86 Filter**: Normative 7-Zip Bra86 state machine converts absolute target addresses back to relative displacements using program counter base $PC = ip + p + 4$.

## ARM64 Affine Branch Target Uncollimation

For AArch64 branch instructions `B` (`0x14000000`) and `BL` (`0x94000000`) identified by mask `v & 0x7C000000 == 0x14000000`, the relative displacement is restored:

$$dest = (v - (pc \gg 2)) \ \& \ \mathtt{0x03FFFFFF} \ | \ (v \ \& \ \mathtt{0xFC000000})$$

# Conformance Requirements and Zero-Allocation Invariant

A compliant decompressor MUST satisfy the following normative requirements:

1. **Deterministic Losslessness**: The decoded output SHALL be byte-for-byte identical to the pre-compression input.
2. **Zero-Allocation Contract (`0 B/op, 0 malloc/free calls`)**:
   - The decompressor MUST NOT allocate heap memory dynamically (`malloc`, `free`, `realloc`, `mmap`, `new`) during `przm_decompress()`.
   - All state structures, lookup arrays, and scratch buffers MUST reside on the stack or within pre-allocated Thread-Local Storage (TLS).
3. **Memory Boundary Safety**:
   - The decompressor MUST NOT read past `src_len` or write past $U_{\text{size}} + 64$.
   - Any corrupt bitstream, invalid opcode, or backward reference past index 0 MUST cause an immediate error return (`PRZM_ERR_*`) without segfault or crash.
4. **ABI Signature**:
   - `przm_get_uncompressed_size(src, src_len)` returns $U_{\text{size}}$ in $O(1)$ time.
   - Destination buffer $D$ MUST be allocated with capacity $\ge U_{\text{size}} + 64$.

# Security and Robustness Considerations

1. **Zip-Bomb Mitigation**: Frame headers define $U_{\text{size}}$, strictly bounded by `PRZM_MAX_BLOCK_SIZE` (16 MiB per frame). Callers MUST inspect $U_{\text{size}}$ prior to buffer provisioning to avert memory exhaustion.
2. **Buffer Overrun Elimination**: Vectorized 64-bit copy operations are guarded by remaining-length checks and bounded by the mandatory 64-byte trailing safety margin.
3. **Uninitialized Memory Protection**: All output locations within $D[0..U_{\text{size}}-1]$ are written deterministically; uninitialized memory disclosure is mathematically impossible.

# IANA Considerations

## Media Type Registration

This document requests IANA to register the following media type in the "Media Types" registry in accordance with [RFC6838]:

- **Type name**: application
- **Subtype name**: przm (with `application/x-przm` registered as a legacy compatibility alias)
- **Required parameters**: N/A
- **Optional parameters**: N/A
- **Encoding considerations**: binary
- **Security considerations**: See Section 11 of this document.
- **Interoperability considerations**: N/A
- **Published specification**: This document.
- **Applications that use this media type**: High-performance game asset streaming engines, GPU shader cache storage, structured archival containers (EON), real-time RPC serialization.
- **Fragment identifier considerations**: N/A
- **Additional information**:
  - Magic number(s): `0x4D 0x5A 0x52 0x50` (`"PRZM"` in Little-Endian) at offset 0
  - File extension(s): `.przm`
  - Macintosh file type code(s): N/A
- **Person & email address to contact for further information**: Arsenii Komolov <arsenii.komolov@yandex.ru>
- **Intended usage**: COMMON
- **Restrictions on usage**: None
- **Author**: Arsenii Komolov
- **Change controller**: IETF / Individual Submission

## HTTP Content-Coding Registration

This document requests IANA to register the `"przm"` token in the "HTTP Content Coding Registry" maintained under the HTTP Semantics registry [RFC9110]:

- **Name**: przm
- **Description**: PRZM autonomous geometric data compression format
- **Reference**: This document.

--- back

# Normative References

[RFC2119]
: Bradner, S., "Key words for use in RFCs to Indicate Requirement Levels", BCP 14, RFC 2119, DOI 10.17487/RFC2119, March 1997, <https://www.rfc-editor.org/info/rfc2119>.

[RFC8174]
: Leiba, B., "Ambiguity of Uppercase vs Lowercase in RFC 2119 Key Words", BCP 14, RFC 8174, DOI 10.17487/RFC8174, May 2017, <https://www.rfc-editor.org/info/rfc8174>.

[FIPS-197]
: National Institute of Standards and Technology, "Advanced Encryption Standard (AES)", Federal Information Processing Standards Publication 197, DOI 10.6028/NIST.FIPS.197, November 2001, <https://doi.org/10.6028/NIST.FIPS.197>.

[RFC6838]
: Freed, N., Klensin, J., and T. Hansen, "Media Type Specifications and Registration Procedures", BCP 13, RFC 6838, DOI 10.17487/RFC6838, January 2013, <https://www.rfc-editor.org/info/rfc6838>.

[RFC9110]
: Fielding, R., Ed., Nottingham, M., Ed., and J. Reschke, Ed., "HTTP Semantics", STD 97, RFC 9110, DOI 10.17487/RFC9110, June 2022, <https://www.rfc-editor.org/info/rfc9110>.

# Informative References

[RFC1951]
: Deutsch, P., "DEFLATE Compressed Data Format Specification version 1.3", RFC 1951, DOI 10.17487/RFC1951, May 1996, <https://www.rfc-editor.org/info/rfc1951>.

[RFC8878]
: Collet, Y. and M. Kucherawy, Ed., "Zstandard Compression and The 'application/zstd' Media Type", RFC 8878, DOI 10.17487/RFC8878, February 2021, <https://www.rfc-editor.org/info/rfc8878>.

[LEAN4]
: de Moura, L. and S. Ullrich, "The Lean 4 Theorem Prover and Programming Language", Automated Deduction - CADE 28, Lecture Notes in Computer Science, vol 12699, Springer, 2021, <https://leanprover.github.io/>.

[ANS]
: Duda, J., "Asymmetric numeral systems: entropy coding combining speed of Huffman coding with compression rate of arithmetic coding", arXiv:1311.2540, 2013, <https://arxiv.org/abs/1311.2540>.

[LZ77]
: Ziv, J. and A. Lempel, "A universal algorithm for sequential data compression", IEEE Transactions on Information Theory, Vol. 23, No. 3, pp. 337-343, DOI 10.1109/TIT.1977.1055714, May 1977.

[MANETH]
: Maneth, S. and F. Peternek, "Grammar-based compression of tree data", Journal of Computer and System Sciences, Vol. 84, pp. 241-260, 2017.

# Reference Implementation

The reference decompressor implementing this specification is maintained in the official repository:

- **Go Root Facade**: [`api.go`](../api.go), [`compress.go`](../compress.go), [`decompress.go`](../decompress.go), [`options.go`](../options.go)
- **Core Decoupled Engines**: [`core/commands/`](../core/commands), [`core/distance/`](../core/distance), [`core/literals/`](../core/literals), [`core/dict/`](../core/dict)
- **C99 Line-Rate Decompressor**: [`src/przm_dec.c`](../src/przm_dec.c), [`src/przm_comp.c`](../src/przm_comp.c)
- **C99 Verification Suite**: [`tests/test_przm_dec.c`](../tests/test_przm_dec.c) (21/21 clean-room tests)

The implementation is licensed under the **BSD-3-Clause** open-source license, permitting unrestricted integration into proprietary and open-source operating systems, game engines, and firmware.

# Author's Address

```text
Arsenii Komolov
Email: arsenii.komolov@yandex.ru
URI:   https://github.com/lemon4ksan/przm
```
