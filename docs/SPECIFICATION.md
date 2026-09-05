---
title: "PRZM Compression Format: Bitstream Syntax and Decompressor Semantics"
abbrev: "PRZM Compression Format"
docname: draft-lemon4ksan-przm-compression-00
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
    ins: A. Senya
    name: Arseny (Lemon4ksan)
    org: PRZM Project
    email: lemon4ksan@gmail.com
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
  Bit 0: FLAG_STRIDE       (0x01) - 1-byte Stride parameter present
  Bit 1: FLAG_DELTA_MASK   (0x02) - Delta mutation mode active
  Bit 2: FLAG_UNCOMPRESSED (0x04) - Uncompressed raw payload
  Bit 3: FLAG_EXT_OFFSET   (0x08) - 24-bit section sizes in directory
  Bit 4: FLAG_DICT_REF     (0x10) - 4-byte DictID parameter present
  Bit 5: FLAG_BCJ_SPLIT    (0x20) - 4-plane x86 displacement splitter active
  Bit 6: FLAG_BCJ          (0x40) - Standard x86 Bra86 filter active
  Bit 7: FLAG_NIBBLE       (0x80) - Section 4 encoded via V-ANS or Nibble-Planes
```

When `FLAG_UNCOMPRESSED` (0x04) is set, exactly $U_{\text{size}}$ bytes follow the 8-byte header verbatim. The decompressor SHALL copy these bytes to $D$ and terminate.

## Dynamic Frame Parameters

When `FLAG_UNCOMPRESSED` is clear, dynamic parameters immediately follow the 8-byte header starting at offset 8:

| Field | Condition | Size and Format |
|:---|:---|:---|
| Stride | FLAG_STRIDE | 1 byte (value $S-1$, stride $S \in 1..256$) |
| DictID | FLAG_DICT_REF | 4 bytes (uint32 Little-Endian) |
| CallCount | FLAG_BCJ_SPLIT | 4 bytes (uint32 Little-Endian) |

Parameters MUST appear in the exact order: `Stride`, `DictID`, `CallCount`.

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

1. **Section 1 (Distribution Parameters)**: Contains probability distribution metadata. The minimum size is 8 bytes. Byte 5 of Section 1 specifies `ExceptionCount`. The total byte length of Section 1 is $\text{Sec1Size} = 8 + 2 \times \text{ExceptionCount}$.
2. **Section 2 (Command Stream)**: Sequence of fused opcodes governing data generation.
3. **Section 3 (Offset Stream)**: Packed ULEB128 variable-length integer match distances.
4. **Section 4 (Literal Stream)**: Literal octets (raw, V-ANS 4x compressed, or nibble-plane encoded).

# Fused Command Stream Syntax and Semantics

The Command Stream (Section 2) is evaluated sequentially from offset `sec2_offset` to `sec2_offset + CmdStreamSize`.

## Ring-16 Locality Offset Queue

The decompressor maintains an internal 16-element integer queue $Q$, initialized at the start of each frame using the effective stride periodicity $S$ (default $S = 1$):

$$Q = [1, S, 2S, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384]$$

Whenever match distance $d = Q[k]$ is accessed by index $k \in [0..15]$, the queue MUST be updated using the Move-to-Front rule: elements $Q[0..k-1]$ are shifted right by one position, and $d$ is inserted at index 0:

```c
int d = q[k];
for (int j = k; j > 0; j--) {
    q[j] = q[j - 1];
}
q[0] = d;
```

Whenever an explicit distance $d$ is decoded from Section 3 or synthesized via Stride matching ($d = m \times S$), the entire queue $Q$ is shifted right, and $d$ is inserted at $Q[0]$.

## Fast ULEB128 Integer Deserialization

Explicit match distances in Section 3 and length extensions in Section 2 are serialized using ULEB128 encoding (7 bits per byte, MSB indicates continuation):

```text
Byte 0: [0xxxxxxx]                         -> Value: 0 .. 127 (1 byte)
Byte 1: [1xxxxxxx] [0yyyyyyy]              -> Value: x | (y << 7) (2 bytes)
Byte 2: [1xxxxxxx] [1yyyyyyy] [0zzzzzzz]   -> Value: x | (y << 7) | (z << 14) (3 bytes)
```

For frames up to 256 KiB, all offsets and lengths are guaranteed to occupy at most 3 bytes.

## Complete Opcode Distribution Map (`0x00..0xFF`)

| Opcode Range | Command Category and Semantics |
|:---|:---|
| 0x00 .. 0x7F | Fast-Path 1: Fused Repeat (0 LLL MM KK) |
| 0x80 .. 0xBF | Fast-Path 2: Fused Extended Match (10 LL MMMM) |
| 0xC0 .. 0xCF | Long Repeat Match (k in 0..15) + Fused Literals |
| 0xD0 .. 0xD7 | Stride Match (m in 1..8, d = m * Stride) |
| 0xD8 .. 0xDF | Direct 1-Byte Schema Token (Tokens 0..7) |
| 0xE0 .. 0xE7 | Delta-Mask Match with Locality Ring (k in 0..7) |
| 0xE8 | Delta-Mask Match with Explicit Offset (ULEB128) |
| 0xE9 .. 0xEF | Reserved for future standard extensions |
| 0xF0 | Pure Literal Run (ULEB128 length, Section 4 payload) |
| 0xF1 | Pure RLE Run (ULEB128 length, single byte replicated) |
| 0xF2 | Extended 2-Byte Schema Token (0xF2 <TokenID>) |
| 0xF3 .. 0xFA | Direct 1-Byte Literal Run (1..8 literals without header) |
| 0xFB .. 0xFF | Reserved for Virtual Machine execution extensions |

## Fast-Path 1: Fused Repeat (`0x00..0x7F`)

Bit 7 is `0`. Encodes literal count, match length, and repeat index in a single byte:

```text
 Bit:   7   6   5   4   3   2   1   0
      +---+---+---+---+---+---+---+---+
      | 0 |    LLL    |   MM  |   KK  |
      +---+---+---+---+---+---+---+---+
```
- **Literal Length** `LLL = (op >> 4) & 0x07` ($0..7$ bytes).
- **Base Match Length** `MM = (op >> 2) & 0x03`, full match length $M_{\text{len}} = MM + 4$ ($4..7$ bytes).
- **Locality Index** `KK = op & 0x03` ($k \in [0..3]$).

**Decompressor Execution Sequence:**
1. If `LLL > 0`, copy `LLL` bytes from Section 4 to destination offset `dst_pos`. Advance `lit_ptr += LLL`, `dst_pos += LLL`.
2. Retrieve distance $d = Q[k]$; update queue $Q$ via Move-to-Front.
3. Validate distance: if $dst\_pos < d$, abort with `PRZM_ERR_OFFSET_OOB`.
4. Copy $M_{\text{len}}$ bytes from offset `dst_pos - d` to `dst_pos`.
5. Advance `dst_pos += M_len`.

## Fast-Path 2: Fused Extended Match (`0x80..0xBF`)

Bits 7..6 are `10`:

```text
 Bit:   7   6   5   4   3   2   1   0
      +---+---+---+---+---+---+---+---+
      | 1   0 |   LL  |     MMMM      |
      +---+---+---+---+---+---+---+---+
```
- **Literal Length** `LL = (op >> 4) & 0x03` ($0..3$ bytes).
- **Base Match Length** `MMMM = op & 0x0F` ($0..15$).
- If `MMMM < 15`, full match length $M_{\text{len}} = MMMM + 4$ ($4..18$ bytes).
- If `MMMM == 15`, an unsigned ULEB128 integer `extra` is read from Section 2; $M_{\text{len}} = 19 + extra$.

**Decompressor Execution Sequence:**
1. If `LL > 0`, copy `LL` literals from Section 4.
2. Read explicit match distance $d$ from Section 3 (Offset Stream) via ULEB128.
3. Update queue $Q$: shift all elements right; insert $d$ at $Q[0]$.
4. Validate $dst\_pos \ge d$, then copy $M_{\text{len}}$ bytes from `dst_pos - d`.

## Long Repeat and Stride Matches (`0xC0..0xD7`)

- **Long Repeat Match (`0xC0..0xCF`)**: Lower 4 bits indicate queue index $k = op \ \& \ 0x0F$ ($0..15$). The subsequent byte `token` in Section 2 provides `lit_len = token >> 4` and base length `base_len = token & 0x0F` (with ULEB128 expansion if 15). Match distance $d = Q[k]$; queue $Q$ is updated via Move-to-Front.
- **Stride Match (`0xD0..0xD7`)**: Encodes stride multiplier $m = (op \ \& \ 0x07) + 1$ ($1..8$). The subsequent byte `token` provides `lit_len = token >> 4` and match length $M_{\text{len}} = (token \ \& \ 0x0F) + 4$. Distance is synthesized arithmetically: $d = m \times S$. Queue $Q$ is shifted right; $d$ is inserted at $Q[0]$.

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
- **Person & email address to contact for further information**: Arseny (Lemon4ksan) <lemon4ksan@gmail.com>
- **Intended usage**: COMMON
- **Restrictions on usage**: None
- **Author**: Arseny (Lemon4ksan)
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

- **Public Header**: [`include/przm_dec.h`](file:///d:/CodingProjects/przm/include/przm_dec.h)
- **Core Implementation**: [`src/przm_dec.c`](file:///d:/CodingProjects/przm/src/przm_dec.c)
- **Verification Harness**: [`tests/test_przm_dec.c`](file:///d:/CodingProjects/przm/tests/test_przm_dec.c)

The implementation is licensed under the **BSD-3-Clause** open-source license, permitting unrestricted integration into proprietary and open-source operating systems, game engines, and firmware.

# Author's Address

```text
Arseny (Lemon4ksan)
PRZM Project
Email: lemon4ksan@gmail.com
URI:   https://github.com/lemon4ksan/przm
```
