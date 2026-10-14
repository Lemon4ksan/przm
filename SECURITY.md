# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | ✅        |

Only the latest release receives security updates and vulnerability patches. We recommend running the most recent version of `przm`.

## Security Model & Decompression Safety Invariants

PRZM is engineered for mission-critical line-rate decompression with zero dynamic allocations (`0 B/op, 0 allocs/op`). Security researchers and contributors should note our core safety invariants:

1. **Strict Buffer Bounds Checking:** Decompressors (`DecompressTo`, `przm_decompress`) verify output buffer limits prior to every byte write. No decompression operation can exceed caller-provided buffer bounds.
2. **Deterministic Memory Ceiling:** Streaming decompression runs within fixed, stack-bounded workspace memory with zero dynamic `malloc` or heap growth.
3. **Zip Bomb & Expansion Ratio Defenses:** Framing headers declare strictly verified uncompressed block sizes to prevent decompression bombs and denial-of-service memory pressure.
4. **Formal Verification:** Key algebraic symmetries, manifold involutions, and invariant boundaries are computer-checked in **Lean 4** (33 proof modules, 0 `sorry`).

## Reporting a Vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Report security issues privately via GitHub's built-in
[Security Advisories](https://github.com/lemon4ksan/przm/security/advisories/new)
feature (Security → Advisories → New draft advisory).

Include as much of the following as you can:

- A description of the vulnerability and its potential impact (e.g. out-of-bounds read/write, integer overflow in bitstream decoder, infinite loop, memory exhaustion)
- Steps to reproduce (compressed sample file, minimal harness, or fuzz seed)
- The version of `przm` (Go package or C library version) and compiler/toolchain used
- Operating system and target architecture (x86_64, ARM64)

We will acknowledge receipt within **72 hours** and aim to release a fix within
**14 days** for confirmed critical issues.
