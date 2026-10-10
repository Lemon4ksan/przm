import Lake
open Lake DSL

package «przm» where
  -- PRZM mathematical specification and formal theorem proofs
  version := v!"0.3.0"
  keywords := #["compression", "galois-field", "topology", "clifford-algebra", "lean4"]

lean_lib «PRZM» where
  -- Root library containing all core modules and tournament-proven theorems
  roots := #[`PRZM, `przm_theorems, `symplectic_invariant, `padic_singularity, `hodge_cohomology]


