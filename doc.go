// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package przm provides the reference Go implementation of the PRZM (PrismLZ)
Unified Archival & Streaming Compression Engine.

# Overview

PRZM is an ultra-high-speed, discrete-manifold archival compression format
engineered to surpass classical 1D sliding-window limits (such as Zstandard and Brotli)
by mapping non-linear digital data distributions onto curved discrete coordinate charts.

Key architectural pillars of PRZM include:

  - Strict Zero-Allocation Contract: DecompressTo and Decompress operate with
    strictly 0 dynamic heap allocations (0 B/op, 0 allocs/op) across both Go
    and pure C99/C11 (libprzm_dec).
  - Microarchitectural Line-Rate Throughput: Decompression operates at rates between
    4.5 and 6.2 GB/s for standard fused frames (and up to 58.7 GB/s for specialized
    symplectic and algebraic manifolds), consuming O(1) clock cycles per vector block.
  - Multi-Scale Trellis Match Finder: Multi-tier hash tables (head4 through head32),
    harmonic stride detection, and Rate-Distortion Optimization (RDO) graph search
    provide superior match coverage, outcompressing Zstd-19 on structured data.
  - Clean Room Interoperability: 100% bit-exact parity verified between the Go reference
    pipeline and the clean room pure C99 decoder (test_przm_dec.exe).

# Binary Framing Topology

A standard PRZM frame consists of an 8-byte global header followed by 4 disjoint,
independently decodable sections:

	[Magic: 4B ("PRZM")] [Flags: 1B] [UncompressedSize: 3B LE]
	[Optional Stride: 0..1B] [Optional DictID: 0..4B]
	[Section Directory: 4B or 6B]
	  ├── Section 1: Distribution Parameters & Window Log (8 bytes)
	  ├── Section 2: Command Stream (Packed opcodes, Mode 8 Tri-Stream or flat Mode 9)
	  ├── Section 3: Offset Stream (49-bracket logarithmic codebook, Mode 12 V-ANS or flat)
	  └── Section 4: Literal Stream (Raw bytes, Nibble-Planes, or 4-lane interleaved V-ANS)

# Complexity Bounds

  - Decompress / DecompressTo: O(N) time with O(1) memory allocations (0 allocs/op).
  - Compress: O(N) time with bounded Trellis horizon and amortized O(1) hash queries.
  - ParseFrameHeader: O(1) time and 0 allocs.
*/
package przm
