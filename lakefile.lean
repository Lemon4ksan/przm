import Lake
open Lake DSL

package «przm» where
  -- PRZM mathematical specification and formal theorem proofs
  version := v!"0.3.0"
  keywords := #["compression", "galois-field", "topology", "clifford-algebra", "lean4"]

lean_lib «PRZM» where
  -- Root library containing all 24 modules and 38 theorems
  roots := #[`PRZM]


