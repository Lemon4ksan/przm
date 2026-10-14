/-
  PRZM Mathematical Foundation: Lean 4 Formalization
  Module: PRZM.Heisenberg
  Pillar: Discrete Heisenberg Group H_3(F_{2^8}) Foliation & Nilpotent Lie Group Inversion
  Theorems: 39, 40
-/

import PRZM.GaloisField
import Std.Tactic.BVDecide

namespace PRZM.Heisenberg

open PRZM.GaloisField

/-!
  # Phase 18: Discrete Heisenberg Group H_3(F_{2^8})
  Let F_{2^8} be the Galois field with irreducible polynomial x^8 + x^4 + x^3 + x + 1 (0x11B).
  The 3-dimensional Discrete Heisenberg Group H_3(F_{2^8}) consists of 3x3 unipotent upper-triangular
  matrices of the form:
        [ 1  x  z ]
    H = [ 0  1  y ]
        [ 0  0  1 ]
  parameterized by the coordinate triple (x, y, z) ∈ F_{2^8}^3.
  Group multiplication corresponds to matrix multiplication:
    (x_1, y_1, z_1) ⋆ (x_2, y_2, z_2) = (x_1 ⊕ x_2, y_1 ⊕ y_2, z_1 ⊕ z_2 ⊕ (x_1 ⊙ y_2)).
-/

/-- Element of the Discrete Heisenberg Group H_3(F_{2^8}) -/
structure Heisenberg3 where
  x : GF256
  y : GF256
  z : GF256
deriving DecidableEq, Repr

/-- Group multiplication in H_3(F_{2^8}) -/
noncomputable def mul (a b : Heisenberg3) : Heisenberg3 :=
  ⟨gf_add a.x b.x,
   gf_add a.y b.y,
   gf_add (gf_add a.z b.z) (gf_mul a.x b.y)⟩

noncomputable instance : Mul Heisenberg3 where
  mul := mul

/-- Identity element e = (0, 0, 0) of H_3(F_{2^8}) -/
def one : Heisenberg3 := ⟨0, 0, 0⟩

instance : OfNat Heisenberg3 1 where
  ofNat := one

/--
  Exact algebraic group inverse in H_3(F_{2^8}):
    H^{-1}(x, y, z) = H(x, y, z ⊕ (x ⊙ y))
  In characteristic 2, the classical formula (-x, -y, -z + xy) simplifies to (x, y, z ⊕ xy).
-/
noncomputable def inv (a : Heisenberg3) : Heisenberg3 :=
  ⟨a.x, a.y, gf_add a.z (gf_mul a.x a.y)⟩

/-!
  # Group Axioms Formal Proofs
-/

/-- Left identity axiom: 1 ⋆ a = a -/
theorem one_mul (a : Heisenberg3) : mul one a = a := by
  dsimp [mul, one]
  have hx : gf_add 0 a.x = a.x := gf_zero_add a.x
  have hy : gf_add 0 a.y = a.y := gf_zero_add a.y
  have hz : gf_add (gf_add 0 a.z) (gf_mul 0 a.y) = a.z := by
    rw [gf_zero_mul, gf_zero_add, gf_add_zero]
  congr

/-- Right identity axiom: a ⋆ 1 = a -/
theorem mul_one (a : Heisenberg3) : mul a one = a := by
  dsimp [mul, one]
  have hx : gf_add a.x 0 = a.x := gf_add_zero a.x
  have hy : gf_add a.y 0 = a.y := gf_add_zero a.y
  have hz : gf_add (gf_add a.z 0) (gf_mul a.x 0) = a.z := by
    rw [gf_mul_zero, gf_add_zero, gf_add_zero]
  congr

/-- Left inverse axiom: a^{-1} ⋆ a = 1 -/
theorem mul_left_inv (a : Heisenberg3) : mul (inv a) a = one := by
  dsimp [mul, inv, one]
  have hx : gf_add a.x a.x = 0 := gf_add_self a.x
  have hy : gf_add a.y a.y = 0 := gf_add_self a.y
  have hz : gf_add (gf_add (gf_add a.z (gf_mul a.x a.y)) a.z) (gf_mul a.x a.y) = 0 := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    rw [UInt8.toBitVec_zero]
    bv_decide
  congr

/-- Right inverse axiom: a ⋆ a^{-1} = 1 -/
theorem mul_right_inv (a : Heisenberg3) : mul a (inv a) = one := by
  dsimp [mul, inv, one]
  have hx : gf_add a.x a.x = 0 := gf_add_self a.x
  have hy : gf_add a.y a.y = 0 := gf_add_self a.y
  have hz : gf_add (gf_add a.z (gf_add a.z (gf_mul a.x a.y))) (gf_mul a.x a.y) = 0 := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    rw [UInt8.toBitVec_zero]
    bv_decide
  congr

/-- Associativity axiom: (a ⋆ b) ⋆ c = a ⋆ (b ⋆ c) -/
theorem mul_assoc (a b c : Heisenberg3) : mul (mul a b) c = mul a (mul b c) := by
  dsimp [mul]
  have hx : gf_add (gf_add a.x b.x) c.x = gf_add a.x (gf_add b.x c.x) := gf_add_assoc a.x b.x c.x
  have hy : gf_add (gf_add a.y b.y) c.y = gf_add a.y (gf_add b.y c.y) := gf_add_assoc a.y b.y c.y
  have hz : gf_add (gf_add (gf_add (gf_add a.z b.z) (gf_mul a.x b.y)) c.z) (gf_mul (gf_add a.x b.x) c.y) =
            gf_add (gf_add a.z (gf_add (gf_add b.z c.z) (gf_mul b.x c.y))) (gf_mul a.x (gf_add b.y c.y)) := by
    rw [gf_mul_distrib, gf_mul_distrib_right]
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    bv_decide
  congr

/-!
  # Theorem 39: Heisenberg Group Axioms & Algebraic Inversion Involution
  In any field of characteristic 2 (F_{2^8}):
    H^{-1}(x, y, z) = H(x, y, z ⊕ (x ⊙ y))
  is an exact group inverse satisfying:
    1. H(x, y, z) ⋆ H^{-1}(x, y, z) = (0, 0, 0)
    2. H^{-1}(x, y, z) ⋆ H(x, y, z) = (0, 0, 0)
    3. Involution: H^{-1}(H^{-1}(x, y, z)) = H(x, y, z)
  The algebraic inversion operation is a strict mathematical involution.
-/

theorem heisenberg_inv_involution (a : Heisenberg3) : inv (inv a) = a := by
  dsimp [inv]
  have hz : gf_add (gf_add a.z (gf_mul a.x a.y)) (gf_mul a.x a.y) = a.z := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    bv_decide
  congr

/-!
  # Theorem 40: Discrete Heisenberg Group Foliation Exact Lossless Reconstruction
  The forward Heisenberg foliation transform on coordinates (x, y, z) maps:
    F(x, y, z) = (x, y, z ⊕ (x ⊙ y))
  The inverse Heisenberg unfoliation transform maps:
    U(x, y, z') = (x, y, z' ⊕ (x ⊙ y))
  Because characteristic is 2, forward foliation and inverse unfoliation evaluate
  the identical algebraic map:
    U(F(x, y, z)) = (x, y, (z ⊕ (x ⊙ y)) ⊕ (x ⊙ y)) = (x, y, z)
  achieving 100% bit-exact lossless roundtrip recovery with zero allocations and O(1) time.
-/

/-- Forward Heisenberg foliation on triplet (x, y, z) -/
noncomputable def foliate (a : Heisenberg3) : Heisenberg3 :=
  ⟨a.x, a.y, gf_add a.z (gf_mul a.x a.y)⟩

/-- Inverse Heisenberg unfoliation on triplet (x, y, z') -/
noncomputable def unfoliate (a : Heisenberg3) : Heisenberg3 :=
  ⟨a.x, a.y, gf_add a.z (gf_mul a.x a.y)⟩

theorem heisenberg_foliate_unfoliate (a : Heisenberg3) : unfoliate (foliate a) = a := by
  dsimp [unfoliate, foliate]
  have hz : gf_add (gf_add a.z (gf_mul a.x a.y)) (gf_mul a.x a.y) = a.z := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    bv_decide
  congr

theorem heisenberg_foliate_involution (a : Heisenberg3) : foliate (foliate a) = a :=
  heisenberg_foliate_unfoliate a

/-- Quad representation for stride 4 channels (e.g. RGBA) -/
structure Heisenberg4 where
  x : GF256
  y : GF256
  z : GF256
  w : GF256
deriving DecidableEq, Repr

/-- Forward foliation on quad (x, y, z, w): transforms z, leaving w invariant -/
noncomputable def foliate4 (q : Heisenberg4) : Heisenberg4 :=
  ⟨q.x, q.y, gf_add q.z (gf_mul q.x q.y), q.w⟩

/-- Inverse unfoliation on quad (x, y, z', w) -/
noncomputable def unfoliate4 (q : Heisenberg4) : Heisenberg4 :=
  ⟨q.x, q.y, gf_add q.z (gf_mul q.x q.y), q.w⟩

theorem heisenberg4_foliate_unfoliate (q : Heisenberg4) : unfoliate4 (foliate4 q) = q := by
  dsimp [unfoliate4, foliate4]
  have hz : gf_add (gf_add q.z (gf_mul q.x q.y)) (gf_mul q.x q.y) = q.z := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    bv_decide
  congr

theorem heisenberg4_foliate_involution (q : Heisenberg4) : foliate4 (foliate4 q) = q :=
  heisenberg4_foliate_unfoliate q

end PRZM.Heisenberg
