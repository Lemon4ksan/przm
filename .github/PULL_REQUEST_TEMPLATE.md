## Description

Provide a concise summary of the change. Link any related issues below.

Fixes # (issue)

## Type of Change

- [ ] Bug fix (non-breaking change which fixes an issue)
- [ ] New feature (non-breaking change which adds functionality)
- [ ] Performance improvement
- [ ] Formal verification proof update (Lean 4)
- [ ] Refactoring (no functional changes)
- [ ] Documentation update

## Checklist

- [ ] I have run `make lint` — no new linter warnings (0 issues reported)
- [ ] I have run `make test` or `make race` — all unit tests pass cleanly
- [ ] Decompression hot paths adhere to the strict **zero-heap-allocation** contract (`0 B/op, 0 allocs/op`)
- [ ] Lean 4 proofs are intact with 0 `sorry` statements (if applicable)
- [ ] Commits follow Conventional Commits formatting (`feat:`, `fix:`, `perf:`, `proof:`, `docs:`)
- [ ] Public API changes are documented in `doc.go` and `README.md` / `README_RU.md`

## How has this been tested?

Describe how you verified the changes work:

```bash
go test -race ./...
```
