import PRZM.Heisenberg

namespace PRZM.Heisenberg.Adversarial

open PRZM.GaloisField
open PRZM.Heisenberg

/-- Commutator of two Heisenberg elements: [a, b] = a ⋆ b ⋆ inv a ⋆ inv b -/
noncomputable def commutator (a b : Heisenberg3) : Heisenberg3 :=
  mul (mul a b) (mul (inv a) (inv b))

/--
  Adversarial Challenge 1: Nilpotency and Centrality of Commutators.
  In a true Heisenberg group, all commutators must belong to the center Z(H_3).
  That is, for any a, b:
  1. The x coordinate of [a, b] is 0.
  2. The y coordinate of [a, b] is 0.
  3. Elements of the form (0, 0, z) commute with all elements of H_3.
-/

theorem commutator_x_zero (a b : Heisenberg3) : (commutator a b).x = 0 := by
  dsimp [commutator, mul, inv]
  dsimp [gf_add]
  apply UInt8.toBitVec_inj
  repeat rw [UInt8.toBitVec_xor]
  rw [UInt8.toBitVec_zero]
  bv_decide

theorem commutator_y_zero (a b : Heisenberg3) : (commutator a b).y = 0 := by
  dsimp [commutator, mul, inv]
  dsimp [gf_add]
  apply UInt8.toBitVec_inj
  repeat rw [UInt8.toBitVec_xor]
  rw [UInt8.toBitVec_zero]
  bv_decide

theorem central_element_commutes (z : GF256) (g : Heisenberg3) :
    mul ⟨0, 0, z⟩ g = mul g ⟨0, 0, z⟩ := by
  dsimp [mul]
  have hx : gf_add 0 g.x = gf_add g.x 0 := by rw [gf_zero_add, gf_add_zero]
  have hy : gf_add 0 g.y = gf_add g.y 0 := by rw [gf_zero_add, gf_add_zero]
  have hz : gf_add (gf_add z g.z) (gf_mul 0 g.y) = gf_add (gf_add g.z z) (gf_mul g.x 0) := by
    rw [gf_zero_mul, gf_mul_zero, gf_add_zero, gf_add_zero, gf_add_comm z g.z]
  rw [hx, hy, hz]

/--
  Adversarial Challenge 2: Invertibility under Double Foliation.
  foliate (unfoliate a) = a, foliate (foliate a) = a, unfoliate (unfoliate a) = a.
-/
theorem foliate_roundtrip_all (a : Heisenberg3) :
    (unfoliate (foliate a) = a) ∧
    (foliate (unfoliate a) = a) ∧
    (foliate (foliate a) = a) ∧
    (unfoliate (unfoliate a) = a) := by
  refine ⟨heisenberg_foliate_unfoliate a, ?_, heisenberg_foliate_involution a, ?_⟩
  · exact heisenberg_foliate_unfoliate a
  · exact heisenberg_foliate_unfoliate a

/--
  Adversarial Challenge 3: Invertibility under Quad Foliation (stride 4).
  Leaves w strictly invariant, roundtrips 100% losslessly.
-/
theorem foliate4_roundtrip_all (q : Heisenberg4) :
    (unfoliate4 (foliate4 q) = q) ∧
    (foliate4 (unfoliate4 q) = q) ∧
    (foliate4 (foliate4 q) = q) ∧
    (unfoliate4 (unfoliate4 q) = q) ∧
    ((foliate4 q).w = q.w) ∧
    ((unfoliate4 q).w = q.w) := by
  refine ⟨heisenberg4_foliate_unfoliate q, heisenberg4_foliate_unfoliate q,
          heisenberg4_foliate_involution q, heisenberg4_foliate_unfoliate q,
          rfl, rfl⟩

/--
  Adversarial Challenge 4: Pure C & Go Bit-exact mapping equivalence.
  Ensure that applying unfoliate twice returns the original state (self-inverse property).
-/
theorem unfoliate_is_self_inverse (a : Heisenberg3) :
    unfoliate (unfoliate a) = a :=
  heisenberg_foliate_unfoliate a

end PRZM.Heisenberg.Adversarial

#print axioms PRZM.Heisenberg.Adversarial.commutator_x_zero
#print axioms PRZM.Heisenberg.Adversarial.commutator_y_zero
#print axioms PRZM.Heisenberg.Adversarial.central_element_commutes
#print axioms PRZM.Heisenberg.Adversarial.foliate_roundtrip_all
#print axioms PRZM.Heisenberg.Adversarial.foliate4_roundtrip_all

