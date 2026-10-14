/-
  PRZM Mathematical Foundation: Lean 4 Formalization
  Pillar 19: Modular Linear Shift-Register Annihilation (MLSR)
  Theorem 32: Ring Invertibility of Second-Order Shift Operators over Z/256Z
-/

import Std.Tactic.BVDecide

namespace PRZM.ModularRecurrence

/-!
  ### Theorem 32: Exact Lossless Inversion of Shift-Register Annihilators
  Over the ring Z/256Z (represented by 8-bit bitvectors), any linear recurrence filter:
    y_n = x_n - c_1 * x_{n-1} - c_2 * x_{n-2} (mod 256)
  admits an exact causal inverse filter:
    x_n = y_n + c_1 * x_{n-1} + c_2 * x_{n-2} (mod 256)
  with zero information loss and zero precision degradation.
-/

/-- Degree-1 Modular Forward Step -/
def forwardStep1 (x : BitVec 8) (x_prev : BitVec 8) (c : BitVec 8) : BitVec 8 :=
  x - c * x_prev

/-- Degree-1 Modular Inverse Step -/
def inverseStep1 (y : BitVec 8) (x_prev : BitVec 8) (c : BitVec 8) : BitVec 8 :=
  y + c * x_prev

/-- Theorem 32.1: Degree-1 Exact Inversion over Z/256Z -/
theorem inverseStep1_correct (x x_prev c : BitVec 8) :
  inverseStep1 (forwardStep1 x x_prev c) x_prev c = x := by
  unfold inverseStep1 forwardStep1
  bv_decide

/-- Degree-2 Modular Forward Step -/
def forwardStep2 (x : BitVec 8) (x_prev1 : BitVec 8) (x_prev2 : BitVec 8) (c1 : BitVec 8) (c2 : BitVec 8) : BitVec 8 :=
  x - c1 * x_prev1 - c2 * x_prev2

/-- Degree-2 Modular Inverse Step -/
def inverseStep2 (y : BitVec 8) (x_prev1 : BitVec 8) (x_prev2 : BitVec 8) (c1 : BitVec 8) (c2 : BitVec 8) : BitVec 8 :=
  y + c1 * x_prev1 + c2 * x_prev2

/-- Theorem 32.2: Degree-2 Exact Inversion over Z/256Z -/
theorem inverseStep2_correct (x x_prev1 x_prev2 c1 c2 : BitVec 8) :
  inverseStep2 (forwardStep2 x x_prev1 x_prev2 c1 c2) x_prev1 x_prev2 c1 c2 = x := by
  unfold inverseStep2 forwardStep2
  bv_decide

end PRZM.ModularRecurrence
