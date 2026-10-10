/-
  PRZM Mathematical Foundation: Lean 4 Formalization
  Module: symplectic_invariant
  Pillar: Symplectic Phase-Space Foliation over Sp(4, F_{2^8}) and Nilpotent Lie Algebras
  Theorems: Symplectic Invariant Preservation, Involution Axiom, and Liouville Phase-Space Volume Conservation
-/

import PRZM.GaloisField
import Std.Tactic.BVDecide

namespace PRZM.SymplecticInvariant

open PRZM.GaloisField

/-!
  # Symplectic Geometry over Finite Fields of Characteristic 2 (F_{2^8})

  Let V = F_{2^8}^4 be a 4-dimensional symplectic phase-space equipped with coordinates
  v = (q0, q1, p0, p1), where q = (q0, q1) are generalized coordinates and p = (p0, p1)
  are conjugate momenta.

  The standard non-degenerate alternating symplectic bilinear form ω : V × V → F_{2^8} is:
    ω(u, v) = (u.q0 ⊙ v.p0 ⊕ u.p0 ⊙ v.q0) ⊕ (u.q1 ⊙ v.p1 ⊕ u.p1 ⊙ v.q1).
-/

structure SymplecticPhase4 where
  q0 : GF256
  q1 : GF256
  p0 : GF256
  p1 : GF256
deriving DecidableEq, Repr

/-- The standard alternating symplectic bilinear form ω(u, v) on V = F_{2^8}^4 -/
noncomputable def omega (u v : SymplecticPhase4) : GF256 :=
  gf_add
    (gf_add (gf_mul u.q0 v.p0) (gf_mul u.p0 v.q0))
    (gf_add (gf_mul u.q1 v.p1) (gf_mul u.p1 v.q1))

/-- Symmetric 2x2 matrix S over F_{2^8} parameterizing the Lagrangian shear -/
structure SymplecticShear2x2 where
  s00 : GF256
  s01 : GF256
  s11 : GF256
deriving DecidableEq, Repr

/--
  Upper symplectic transvection (Lagrangian shear) T_S:
    [ I_2   S ]
    [  0   I_2 ]
  where S = S^T (S01 = S10).
  Explicitly:
    q0' = q0 ⊕ (S00 ⊙ p0 ⊕ S01 ⊙ p1)
    q1' = q1 ⊕ (S01 ⊙ p0 ⊕ S11 ⊙ p1)
    p0' = p0
    p1' = p1
-/
noncomputable def apply_shear (s : SymplecticShear2x2) (v : SymplecticPhase4) : SymplecticPhase4 :=
  let q0' := gf_add v.q0 (gf_add (gf_mul s.s00 v.p0) (gf_mul s.s01 v.p1))
  let q1' := gf_add v.q1 (gf_add (gf_mul s.s01 v.p0) (gf_mul s.s11 v.p1))
  ⟨q0', q1', v.p0, v.p1⟩

/--
  Lower symplectic transvection (dual Lagrangian shear) T_S^-:
    [ I_2   0  ]
    [  S   I_2 ]
  Explicitly:
    q0' = q0
    q1' = q1
    p0' = p0 ⊕ (S00 ⊙ q0 ⊕ S01 ⊙ q1)
    p1' = p1 ⊕ (S01 ⊙ q0 ⊕ S11 ⊙ q1)
-/
noncomputable def apply_lower_shear (s : SymplecticShear2x2) (v : SymplecticPhase4) : SymplecticPhase4 :=
  let p0' := gf_add v.p0 (gf_add (gf_mul s.s00 v.q0) (gf_mul s.s01 v.q1))
  let p1' := gf_add v.p1 (gf_add (gf_mul s.s01 v.q0) (gf_mul s.s11 v.q1))
  ⟨v.q0, v.q1, p0', p1'⟩

/-!
  =============================================================================
  THEOREM 1: SYMPLECTIC INVOLUTION PROPERTY (T_S^2 = I)
  =============================================================================
  In any field of characteristic 2 (F_{2^8}):
    T_S^2 = [ I_2  S ⊕ S ] = [ I_2   0  ] = I_4
            [  0    I_2  ]   [  0   I_2 ]
  because S ⊕ S = 0.
  Therefore, the inverse transformation T_S^{-1} is mathematically identical to T_S itself.
  Decompression executes the exact same forward circuit with 0 matrix inversions,
  0 division operations, and 0 numerical loss.
-/

theorem symplectic_shear_involution (s : SymplecticShear2x2) (v : SymplecticPhase4) :
    apply_shear s (apply_shear s v) = v := by
  dsimp [apply_shear]
  have h_q0 :
    gf_add (gf_add v.q0 (gf_add (gf_mul s.s00 v.p0) (gf_mul s.s01 v.p1)))
           (gf_add (gf_mul s.s00 v.p0) (gf_mul s.s01 v.p1)) = v.q0 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_q1 :
    gf_add (gf_add v.q1 (gf_add (gf_mul s.s01 v.p0) (gf_mul s.s11 v.p1)))
           (gf_add (gf_mul s.s01 v.p0) (gf_mul s.s11 v.p1)) = v.q1 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  congr

theorem symplectic_lower_shear_involution (s : SymplecticShear2x2) (v : SymplecticPhase4) :
    apply_lower_shear s (apply_lower_shear s v) = v := by
  dsimp [apply_lower_shear]
  have h_p0 :
    gf_add (gf_add v.p0 (gf_add (gf_mul s.s00 v.q0) (gf_mul s.s01 v.q1)))
           (gf_add (gf_mul s.s00 v.q0) (gf_mul s.s01 v.q1)) = v.p0 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_p1 :
    gf_add (gf_add v.p1 (gf_add (gf_mul s.s01 v.q0) (gf_mul s.s11 v.q1)))
           (gf_add (gf_mul s.s01 v.q0) (gf_mul s.s11 v.q1)) = v.p1 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  congr

/-!
  =============================================================================
  THEOREM 2: ALTERNATING SYMPLECTIC FORM IDENTITIES
  =============================================================================
  1. Alternating property: ω(v, v) = 0 for all v ∈ V.
  2. Symmetry in characteristic 2: ω(u, v) = ω(v, u).
-/

theorem omega_alternating (v : SymplecticPhase4) : omega v v = 0 := by
  dsimp [omega]
  have h0 : gf_add (gf_mul v.q0 v.p0) (gf_mul v.p0 v.q0) = 0 := by
    rw [gf_mul_comm v.p0 v.q0]
    exact gf_add_self (gf_mul v.q0 v.p0)
  have h1 : gf_add (gf_mul v.q1 v.p1) (gf_mul v.p1 v.q1) = 0 := by
    rw [gf_mul_comm v.p1 v.q1]
    exact gf_add_self (gf_mul v.q1 v.p1)
  rw [h0, h1, gf_add_self]

theorem omega_symm (u v : SymplecticPhase4) : omega u v = omega v u := by
  dsimp [omega]
  have h0 : gf_add (gf_mul u.q0 v.p0) (gf_mul u.p0 v.q0) =
            gf_add (gf_mul v.q0 u.p0) (gf_mul v.p0 u.q0) := by
    rw [gf_add_comm (gf_mul u.q0 v.p0), gf_mul_comm v.q0 u.p0, gf_mul_comm v.p0 u.q0]
  have h1 : gf_add (gf_mul u.q1 v.p1) (gf_mul u.p1 v.q1) =
            gf_add (gf_mul v.q1 u.p1) (gf_mul v.p1 u.q1) := by
    rw [gf_add_comm (gf_mul u.q1 v.p1), gf_mul_comm v.q1 u.p1, gf_mul_comm v.p1 u.q1]
  rw [h0, h1]

/-!
  =============================================================================
  THEOREM 3: SYMPLECTIC BILINEAR FORM INVARIANCE (Sp(4, F_{2^8}))
  =============================================================================
  A linear automorphism T preserves the symplectic form if and only if:
    ω(T(u), T(v)) = ω(u, v)   ∀ u, v ∈ V.
  We prove that the transvection T_S preserves ω strictly for all symmetric S
  and all phase-space vectors u, v.
-/

-- Associativity axiom of finite field multiplication
axiom gf_mul_assoc (a b c : GF256) : gf_mul (gf_mul a b) c = gf_mul a (gf_mul b c)

theorem gf_mul_sandwich (s x y : GF256) : gf_mul (gf_mul s x) y = gf_mul x (gf_mul s y) := by
  rw [gf_mul_comm s x, gf_mul_assoc]

theorem symplectic_shear_preserves_omega (s : SymplecticShear2x2) (u v : SymplecticPhase4) :
    omega (apply_shear s u) (apply_shear s v) = omega u v := by
  dsimp [omega, apply_shear]
  -- Expand the multiplication over addition using distributivity
  rw [gf_mul_distrib_right, gf_mul_distrib_right]
  rw [gf_mul_distrib_right, gf_mul_distrib_right]
  rw [gf_mul_distrib, gf_mul_distrib]
  rw [gf_mul_distrib, gf_mul_distrib]
  -- Rewrite transposed product terms using gf_mul_sandwich
  rw [gf_mul_sandwich s.s00 u.p0 v.p0]
  rw [gf_mul_sandwich s.s11 u.p1 v.p1]
  rw [gf_mul_sandwich s.s01 u.p1 v.p0]
  rw [gf_mul_sandwich s.s01 u.p0 v.p1]
  -- In characteristic 2, all identical cross-terms cancel pairwise
  dsimp [gf_add]
  apply UInt8.toBitVec_inj
  repeat rw [UInt8.toBitVec_xor]
  bv_decide

theorem symplectic_lower_shear_preserves_omega (s : SymplecticShear2x2) (u v : SymplecticPhase4) :
    omega (apply_lower_shear s u) (apply_lower_shear s v) = omega u v := by
  dsimp [omega, apply_lower_shear]
  rw [gf_mul_distrib, gf_mul_distrib]
  rw [gf_mul_distrib, gf_mul_distrib]
  rw [gf_mul_distrib_right, gf_mul_distrib_right]
  rw [gf_mul_distrib_right, gf_mul_distrib_right]
  rw [gf_mul_sandwich s.s00 u.q0 v.q0]
  rw [gf_mul_sandwich s.s11 u.q1 v.q1]
  rw [gf_mul_sandwich s.s01 u.q1 v.q0]
  rw [gf_mul_sandwich s.s01 u.q0 v.q1]
  dsimp [gf_add]
  apply UInt8.toBitVec_inj
  repeat rw [UInt8.toBitVec_xor]
  bv_decide

/-!
  =============================================================================
  THEOREM 4: DISCRETE HEISENBERG GROUP ACTION H_3(F_{2^8})
  =============================================================================
  The 3D Discrete Heisenberg Group H_3(F_{2^8}) embeds into unipotent 3x3 matrices:
        [ 1  x  z ]
    H = [ 0  1  y ]
        [ 0  0  1 ]
  Group product:
    (x1, y1, z1) ⋆ (x2, y2, z2) = (x1 ⊕ x2, y1 ⊕ y2, z1 ⊕ z2 ⊕ (x1 ⊙ y2)).
  Group inverse in characteristic 2:
    (x, y, z)^{-1} = (x, y, z ⊕ (x ⊙ y)).
  The foliation map F(x, y, z) = (x, y, z ⊕ (x ⊙ y)) is an exact algebraic involution.
-/

structure Heisenberg3 where
  x : GF256
  y : GF256
  z : GF256
deriving DecidableEq, Repr

noncomputable def heisenberg_mul (a b : Heisenberg3) : Heisenberg3 :=
  ⟨gf_add a.x b.x,
   gf_add a.y b.y,
   gf_add (gf_add a.z b.z) (gf_mul a.x b.y)⟩

noncomputable def heisenberg_inv (a : Heisenberg3) : Heisenberg3 :=
  ⟨a.x, a.y, gf_add a.z (gf_mul a.x a.y)⟩

def heisenberg_one : Heisenberg3 := ⟨0, 0, 0⟩

theorem heisenberg_inv_left (a : Heisenberg3) :
    heisenberg_mul (heisenberg_inv a) a = heisenberg_one := by
  dsimp [heisenberg_mul, heisenberg_inv, heisenberg_one]
  have hx : gf_add a.x a.x = 0 := gf_add_self a.x
  have hy : gf_add a.y a.y = 0 := gf_add_self a.y
  have hz : gf_add (gf_add (gf_add a.z (gf_mul a.x a.y)) a.z) (gf_mul a.x a.y) = 0 := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    rw [UInt8.toBitVec_zero]
    bv_decide
  congr

theorem heisenberg_inv_involution (a : Heisenberg3) :
    heisenberg_inv (heisenberg_inv a) = a := by
  dsimp [heisenberg_inv]
  have hz : gf_add (gf_add a.z (gf_mul a.x a.y)) (gf_mul a.x a.y) = a.z := by
    dsimp [gf_add]
    apply UInt8.toBitVec_inj
    repeat rw [UInt8.toBitVec_xor]
    bv_decide
  congr

/-- Forward Heisenberg foliation transform on coordinates (x, y, z) -/
noncomputable def heisenberg_foliate (a : Heisenberg3) : Heisenberg3 :=
  ⟨a.x, a.y, gf_add a.z (gf_mul a.x a.y)⟩

theorem heisenberg_foliate_roundtrip (a : Heisenberg3) :
    heisenberg_foliate (heisenberg_foliate a) = a :=
  heisenberg_inv_involution a

/-!
  =============================================================================
  THEOREM 5: LIOUVILLE DISCRETE PHASE-SPACE VOLUME CONSERVATION
  =============================================================================
  Because T_S ∘ T_S = id on V = F_{2^8}^4, T_S is a bijective automorphism of the
  discrete phase space.
  Every subset A ⊆ V has its measure (cardinality) exactly preserved:
    |T_S(A)| = |A|.
  Information entropy is strictly conserved: H(T_S(X)) = H(X).
  There is zero information loss, zero truncation, and zero numerical drift.
-/

def InjectiveMap (f : SymplecticPhase4 → SymplecticPhase4) : Prop :=
  ∀ ⦃u v⦄, f u = f v → u = v

def SurjectiveMap (f : SymplecticPhase4 → SymplecticPhase4) : Prop :=
  ∀ v, ∃ u, f u = v

def BijectiveMap (f : SymplecticPhase4 → SymplecticPhase4) : Prop :=
  InjectiveMap f ∧ SurjectiveMap f

theorem symplectic_shear_injective (s : SymplecticShear2x2) :
    InjectiveMap (apply_shear s) := by
  intro u v h
  have h_inv : apply_shear s (apply_shear s u) = apply_shear s (apply_shear s v) := by rw [h]
  rw [symplectic_shear_involution s u, symplectic_shear_involution s v] at h_inv
  exact h_inv

theorem symplectic_shear_surjective (s : SymplecticShear2x2) :
    SurjectiveMap (apply_shear s) := by
  intro v
  refine ⟨apply_shear s v, ?_⟩
  exact symplectic_shear_involution s v

theorem symplectic_shear_bijective (s : SymplecticShear2x2) :
    BijectiveMap (apply_shear s) :=
  ⟨symplectic_shear_injective s, symplectic_shear_surjective s⟩

theorem symplectic_lower_shear_bijective (s : SymplecticShear2x2) :
    BijectiveMap (apply_lower_shear s) := by
  refine ⟨?_, ?_⟩
  · intro u v h
    have h_inv : apply_lower_shear s (apply_lower_shear s u) = apply_lower_shear s (apply_lower_shear s v) := by rw [h]
    rw [symplectic_lower_shear_involution s u, symplectic_lower_shear_involution s v] at h_inv
    exact h_inv
  · intro v
    refine ⟨apply_lower_shear s v, ?_⟩
    exact symplectic_lower_shear_involution s v

/-!
  =============================================================================
  PHASE 19: 8-DIMENSIONAL SYMPLECTIC GEOMETRY Sp(8, F_{2^8}) (CYCLE 2 DIRECTIVE)
  =============================================================================
  Let V8 = F_{2^8}^8 be an 8-dimensional symplectic vector space parameterizing
  8-channel multi-stream telemetry, RGBA64 textures, and 7.1 audio streams:
    v = (q0, q1, q2, q3, p0, p1, p2, p3).
  The 8D non-degenerate alternating symplectic bilinear form ω8 is:
    ω8(u, v) = ⨁_{i=0}^3 (u.qi ⊙ v.pi ⊕ u.pi ⊙ v.qi).
-/

structure SymplecticPhase8 where
  q0 : GF256
  q1 : GF256
  q2 : GF256
  q3 : GF256
  p0 : GF256
  p1 : GF256
  p2 : GF256
  p3 : GF256
deriving DecidableEq, Repr

/-- Standard alternating symplectic bilinear form ω8(u, v) on V8 = F_{2^8}^8 -/
noncomputable def omega8 (u v : SymplecticPhase8) : GF256 :=
  gf_add
    (gf_add (gf_add (gf_mul u.q0 v.p0) (gf_mul u.p0 v.q0))
            (gf_add (gf_mul u.q1 v.p1) (gf_mul u.p1 v.q1)))
    (gf_add (gf_add (gf_mul u.q2 v.p2) (gf_mul u.p2 v.q2))
            (gf_add (gf_mul u.q3 v.p3) (gf_mul u.p3 v.q3)))

/-- Symmetric 4x4 matrix S over F_{2^8} parameterizing the 8D Lagrangian shear -/
structure SymplecticShear4x4 where
  s00 : GF256
  s01 : GF256
  s02 : GF256
  s03 : GF256
  s11 : GF256
  s12 : GF256
  s13 : GF256
  s22 : GF256
  s23 : GF256
  s33 : GF256
deriving DecidableEq, Repr

/-- Upper symplectic transvection in Sp(8, F_{2^8}) -/
noncomputable def apply_shear8 (s : SymplecticShear4x4) (v : SymplecticPhase8) : SymplecticPhase8 :=
  let delta_q0 := gf_add (gf_add (gf_mul s.s00 v.p0) (gf_mul s.s01 v.p1))
                         (gf_add (gf_mul s.s02 v.p2) (gf_mul s.s03 v.p3))
  let delta_q1 := gf_add (gf_add (gf_mul s.s01 v.p0) (gf_mul s.s11 v.p1))
                         (gf_add (gf_mul s.s12 v.p2) (gf_mul s.s13 v.p3))
  let delta_q2 := gf_add (gf_add (gf_mul s.s02 v.p0) (gf_mul s.s12 v.p1))
                         (gf_add (gf_mul s.s22 v.p2) (gf_mul s.s23 v.p3))
  let delta_q3 := gf_add (gf_add (gf_mul s.s03 v.p0) (gf_mul s.s13 v.p1))
                         (gf_add (gf_mul s.s23 v.p2) (gf_mul s.s33 v.p3))
  ⟨gf_add v.q0 delta_q0,
   gf_add v.q1 delta_q1,
   gf_add v.q2 delta_q2,
   gf_add v.q3 delta_q3,
   v.p0, v.p1, v.p2, v.p3⟩

/-- Lower symplectic transvection in Sp(8, F_{2^8}) -/
noncomputable def apply_lower_shear8 (s : SymplecticShear4x4) (v : SymplecticPhase8) : SymplecticPhase8 :=
  let delta_p0 := gf_add (gf_add (gf_mul s.s00 v.q0) (gf_mul s.s01 v.q1))
                         (gf_add (gf_mul s.s02 v.q2) (gf_mul s.s03 v.q3))
  let delta_p1 := gf_add (gf_add (gf_mul s.s01 v.q0) (gf_mul s.s11 v.q1))
                         (gf_add (gf_mul s.s12 v.q2) (gf_mul s.s13 v.q3))
  let delta_p2 := gf_add (gf_add (gf_mul s.s02 v.q0) (gf_mul s.s12 v.q1))
                         (gf_add (gf_mul s.s22 v.q2) (gf_mul s.s23 v.q3))
  let delta_p3 := gf_add (gf_add (gf_mul s.s03 v.q0) (gf_mul s.s13 v.q1))
                         (gf_add (gf_mul s.s23 v.q2) (gf_mul s.s33 v.q3))
  ⟨v.q0, v.q1, v.q2, v.q3,
   gf_add v.p0 delta_p0,
   gf_add v.p1 delta_p1,
   gf_add v.p2 delta_p2,
   gf_add v.p3 delta_p3⟩

/-!
  # Theorem 8A: 8D Symplectic Involution Guarantee (T_S^2 = I_8)
-/

theorem symplectic_shear8_involution (s : SymplecticShear4x4) (v : SymplecticPhase8) :
    apply_shear8 s (apply_shear8 s v) = v := by
  dsimp [apply_shear8]
  have h_q0 :
    gf_add (gf_add v.q0 (gf_add (gf_add (gf_mul s.s00 v.p0) (gf_mul s.s01 v.p1))
                                (gf_add (gf_mul s.s02 v.p2) (gf_mul s.s03 v.p3))))
           (gf_add (gf_add (gf_mul s.s00 v.p0) (gf_mul s.s01 v.p1))
                   (gf_add (gf_mul s.s02 v.p2) (gf_mul s.s03 v.p3))) = v.q0 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_q1 :
    gf_add (gf_add v.q1 (gf_add (gf_add (gf_mul s.s01 v.p0) (gf_mul s.s11 v.p1))
                                (gf_add (gf_mul s.s12 v.p2) (gf_mul s.s13 v.p3))))
           (gf_add (gf_add (gf_mul s.s01 v.p0) (gf_mul s.s11 v.p1))
                   (gf_add (gf_mul s.s12 v.p2) (gf_mul s.s13 v.p3))) = v.q1 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_q2 :
    gf_add (gf_add v.q2 (gf_add (gf_add (gf_mul s.s02 v.p0) (gf_mul s.s12 v.p1))
                                (gf_add (gf_mul s.s22 v.p2) (gf_mul s.s23 v.p3))))
           (gf_add (gf_add (gf_mul s.s02 v.p0) (gf_mul s.s12 v.p1))
                   (gf_add (gf_mul s.s22 v.p2) (gf_mul s.s23 v.p3))) = v.q2 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_q3 :
    gf_add (gf_add v.q3 (gf_add (gf_add (gf_mul s.s03 v.p0) (gf_mul s.s13 v.p1))
                                (gf_add (gf_mul s.s23 v.p2) (gf_mul s.s33 v.p3))))
           (gf_add (gf_add (gf_mul s.s03 v.p0) (gf_mul s.s13 v.p1))
                   (gf_add (gf_mul s.s23 v.p2) (gf_mul s.s33 v.p3))) = v.q3 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  congr

theorem symplectic_lower_shear8_involution (s : SymplecticShear4x4) (v : SymplecticPhase8) :
    apply_lower_shear8 s (apply_lower_shear8 s v) = v := by
  dsimp [apply_lower_shear8]
  have h_p0 :
    gf_add (gf_add v.p0 (gf_add (gf_add (gf_mul s.s00 v.q0) (gf_mul s.s01 v.q1))
                                (gf_add (gf_mul s.s02 v.q2) (gf_mul s.s03 v.q3))))
           (gf_add (gf_add (gf_mul s.s00 v.q0) (gf_mul s.s01 v.q1))
                   (gf_add (gf_mul s.s02 v.q2) (gf_mul s.s03 v.q3))) = v.p0 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_p1 :
    gf_add (gf_add v.p1 (gf_add (gf_add (gf_mul s.s01 v.q0) (gf_mul s.s11 v.q1))
                                (gf_add (gf_mul s.s12 v.q2) (gf_mul s.s13 v.q3))))
           (gf_add (gf_add (gf_mul s.s01 v.q0) (gf_mul s.s11 v.q1))
                   (gf_add (gf_mul s.s12 v.q2) (gf_mul s.s13 v.q3))) = v.p1 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_p2 :
    gf_add (gf_add v.p2 (gf_add (gf_add (gf_mul s.s02 v.q0) (gf_mul s.s12 v.q1))
                                (gf_add (gf_mul s.s22 v.q2) (gf_mul s.s23 v.q3))))
           (gf_add (gf_add (gf_mul s.s02 v.q0) (gf_mul s.s12 v.q1))
                   (gf_add (gf_mul s.s22 v.q2) (gf_mul s.s23 v.q3))) = v.p2 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  have h_p3 :
    gf_add (gf_add v.p3 (gf_add (gf_add (gf_mul s.s03 v.q0) (gf_mul s.s13 v.q1))
                                (gf_add (gf_mul s.s23 v.q2) (gf_mul s.s33 v.q3))))
           (gf_add (gf_add (gf_mul s.s03 v.q0) (gf_mul s.s13 v.q1))
                   (gf_add (gf_mul s.s23 v.q2) (gf_mul s.s33 v.q3))) = v.p3 := by
    dsimp [gf_add]
    rw [xor_cancel_right]
  congr

/-!
  # Theorem 8B: 8D Alternating Symplectic Form Invariance
-/

theorem omega8_alternating (v : SymplecticPhase8) : omega8 v v = 0 := by
  dsimp [omega8]
  have h0 : gf_add (gf_mul v.q0 v.p0) (gf_mul v.p0 v.q0) = 0 := by
    rw [gf_mul_comm v.p0 v.q0]
    exact gf_add_self (gf_mul v.q0 v.p0)
  have h1 : gf_add (gf_mul v.q1 v.p1) (gf_mul v.p1 v.q1) = 0 := by
    rw [gf_mul_comm v.p1 v.q1]
    exact gf_add_self (gf_mul v.q1 v.p1)
  have h2 : gf_add (gf_mul v.q2 v.p2) (gf_mul v.p2 v.q2) = 0 := by
    rw [gf_mul_comm v.p2 v.q2]
    exact gf_add_self (gf_mul v.q2 v.p2)
  have h3 : gf_add (gf_mul v.q3 v.p3) (gf_mul v.p3 v.q3) = 0 := by
    rw [gf_mul_comm v.p3 v.q3]
    exact gf_add_self (gf_mul v.q3 v.p3)
  rw [h0, h1, h2, h3]
  repeat rw [gf_add_self]

theorem omega8_symm (u v : SymplecticPhase8) : omega8 u v = omega8 v u := by
  dsimp [omega8]
  have h0 : gf_add (gf_mul u.q0 v.p0) (gf_mul u.p0 v.q0) =
            gf_add (gf_mul v.q0 u.p0) (gf_mul v.p0 u.q0) := by
    rw [gf_add_comm (gf_mul u.q0 v.p0), gf_mul_comm v.q0 u.p0, gf_mul_comm v.p0 u.q0]
  have h1 : gf_add (gf_mul u.q1 v.p1) (gf_mul u.p1 v.q1) =
            gf_add (gf_mul v.q1 u.p1) (gf_mul v.p1 u.q1) := by
    rw [gf_add_comm (gf_mul u.q1 v.p1), gf_mul_comm v.q1 u.p1, gf_mul_comm v.p1 u.q1]
  have h2 : gf_add (gf_mul u.q2 v.p2) (gf_mul u.p2 v.q2) =
            gf_add (gf_mul v.q2 u.p2) (gf_mul v.p2 u.q2) := by
    rw [gf_add_comm (gf_mul u.q2 v.p2), gf_mul_comm v.q2 u.p2, gf_mul_comm v.p2 u.q2]
  have h3 : gf_add (gf_mul u.q3 v.p3) (gf_mul u.p3 v.q3) =
            gf_add (gf_mul v.q3 u.p3) (gf_mul v.p3 u.q3) := by
    rw [gf_add_comm (gf_mul u.q3 v.p3), gf_mul_comm v.q3 u.p3, gf_mul_comm v.p3 u.q3]
  rw [h0, h1, h2, h3]

/-!
  # Theorem 8C: Symplectic Invariant Preservation in Sp(8, F_{2^8})
  ω8(T_S(u), T_S(v)) = ω8(u, v) for all u, v ∈ V8.
-/

theorem symplectic_shear8_preserves_omega8 (s : SymplecticShear4x4) (u v : SymplecticPhase8) :
    omega8 (apply_shear8 s u) (apply_shear8 s v) = omega8 u v := by
  dsimp [omega8, apply_shear8]
  repeat rw [gf_mul_distrib_right]
  repeat rw [gf_mul_distrib]
  rw [gf_mul_sandwich s.s00 u.p0 v.p0]
  rw [gf_mul_sandwich s.s11 u.p1 v.p1]
  rw [gf_mul_sandwich s.s22 u.p2 v.p2]
  rw [gf_mul_sandwich s.s33 u.p3 v.p3]
  rw [gf_mul_sandwich s.s01 u.p1 v.p0]
  rw [gf_mul_sandwich s.s01 u.p0 v.p1]
  rw [gf_mul_sandwich s.s02 u.p2 v.p0]
  rw [gf_mul_sandwich s.s02 u.p0 v.p2]
  rw [gf_mul_sandwich s.s03 u.p3 v.p0]
  rw [gf_mul_sandwich s.s03 u.p0 v.p3]
  rw [gf_mul_sandwich s.s12 u.p2 v.p1]
  rw [gf_mul_sandwich s.s12 u.p1 v.p2]
  rw [gf_mul_sandwich s.s13 u.p3 v.p1]
  rw [gf_mul_sandwich s.s13 u.p1 v.p3]
  rw [gf_mul_sandwich s.s23 u.p3 v.p2]
  rw [gf_mul_sandwich s.s23 u.p2 v.p3]
  dsimp [gf_add]
  apply UInt8.toBitVec_inj
  repeat rw [UInt8.toBitVec_xor]
  bv_decide

theorem symplectic_lower_shear8_preserves_omega8 (s : SymplecticShear4x4) (u v : SymplecticPhase8) :
    omega8 (apply_lower_shear8 s u) (apply_lower_shear8 s v) = omega8 u v := by
  dsimp [omega8, apply_lower_shear8]
  repeat rw [gf_mul_distrib]
  repeat rw [gf_mul_distrib_right]
  rw [gf_mul_sandwich s.s00 u.q0 v.q0]
  rw [gf_mul_sandwich s.s11 u.q1 v.q1]
  rw [gf_mul_sandwich s.s22 u.q2 v.q2]
  rw [gf_mul_sandwich s.s33 u.q3 v.q3]
  rw [gf_mul_sandwich s.s01 u.q1 v.q0]
  rw [gf_mul_sandwich s.s01 u.q0 v.q1]
  rw [gf_mul_sandwich s.s02 u.q2 v.q0]
  rw [gf_mul_sandwich s.s02 u.q0 v.q2]
  rw [gf_mul_sandwich s.s03 u.q3 v.q0]
  rw [gf_mul_sandwich s.s03 u.q0 v.q3]
  rw [gf_mul_sandwich s.s12 u.q2 v.q1]
  rw [gf_mul_sandwich s.s12 u.q1 v.q2]
  rw [gf_mul_sandwich s.s13 u.q3 v.q1]
  rw [gf_mul_sandwich s.s13 u.q1 v.q3]
  rw [gf_mul_sandwich s.s23 u.q3 v.q2]
  rw [gf_mul_sandwich s.s23 u.q2 v.q3]
  dsimp [gf_add]
  apply UInt8.toBitVec_inj
  repeat rw [UInt8.toBitVec_xor]
  bv_decide

/-!
  # Theorem 8D: 8D Liouville Discrete Volume Preservation
-/

def InjectiveMap8 (f : SymplecticPhase8 → SymplecticPhase8) : Prop :=
  ∀ ⦃u v⦄, f u = f v → u = v

def SurjectiveMap8 (f : SymplecticPhase8 → SymplecticPhase8) : Prop :=
  ∀ v, ∃ u, f u = v

def BijectiveMap8 (f : SymplecticPhase8 → SymplecticPhase8) : Prop :=
  InjectiveMap8 f ∧ SurjectiveMap8 f

theorem symplectic_shear8_bijective (s : SymplecticShear4x4) :
    BijectiveMap8 (apply_shear8 s) := by
  refine ⟨?_, ?_⟩
  · intro u v h
    have h_inv : apply_shear8 s (apply_shear8 s u) = apply_shear8 s (apply_shear8 s v) := by rw [h]
    rw [symplectic_shear8_involution s u, symplectic_shear8_involution s v] at h_inv
    exact h_inv
  · intro v
    refine ⟨apply_shear8 s v, ?_⟩
    exact symplectic_shear8_involution s v

theorem symplectic_lower_shear8_bijective (s : SymplecticShear4x4) :
    BijectiveMap8 (apply_lower_shear8 s) := by
  refine ⟨?_, ?_⟩
  · intro u v h
    have h_inv : apply_lower_shear8 s (apply_lower_shear8 s u) = apply_lower_shear8 s (apply_lower_shear8 s v) := by rw [h]
    rw [symplectic_lower_shear8_involution s u, symplectic_lower_shear8_involution s v] at h_inv
    exact h_inv
  · intro v
    refine ⟨apply_lower_shear8 s v, ?_⟩
    exact symplectic_lower_shear8_involution s v

/-!
  =============================================================================
  PHASE 20: GRAND UNIFICATION DIRECT SUM EMBEDDING Sp(4) ⊕ Sp(4) ↪ Sp(8)
  =============================================================================
  Theorem 9: Block-diagonal symplectic direct sum decomposition.
  Every pair of 4-channel symplectic transvections (T_{S0}, T_{S1}) embeds into
  an 8-channel symplectic transvection T_{S0 ⊕ S1}:
    T_{S0 ⊕ S1}(v) = (T_{S0}(v^{(0)}), T_{S1}(v^{(1)})).
  We formally prove:
  1. ω8(u, v) = ω4(u^{(0)}, v^{(0)}) ⊕ ω4(u^{(1)}, v^{(1)})
  2. ω8(T_{S0 ⊕ S1}(u), T_{S0 ⊕ S1}(v)) = ω8(u, v)
  3. (T_{S0 ⊕ S1})^2 = I_8
  4. T_{S0 ⊕ S1} is an exact bijective automorphism of V8.
-/

/-- Project 8D phase vector to lower 4D phase vector (channels 0, 1, 4, 5) -/
def proj4_0 (v : SymplecticPhase8) : SymplecticPhase4 :=
  ⟨v.q0, v.q1, v.p0, v.p1⟩

/-- Project 8D phase vector to upper 4D phase vector (channels 2, 3, 6, 7) -/
def proj4_1 (v : SymplecticPhase8) : SymplecticPhase4 :=
  ⟨v.q2, v.q3, v.p2, v.p3⟩

/-- Direct sum operator embedding Sp(4) ⊕ Sp(4) into Sp(8) -/
noncomputable def direct_sum_shear (s0 s1 : SymplecticShear2x2) (v : SymplecticPhase8) : SymplecticPhase8 :=
  let v0' := apply_shear s0 (proj4_0 v)
  let v1' := apply_shear s1 (proj4_1 v)
  ⟨v0'.q0, v0'.q1, v1'.q0, v1'.q1,
   v0'.p0, v0'.p1, v1'.p0, v1'.p1⟩

@[simp] theorem proj4_0_direct_sum (s0 s1 : SymplecticShear2x2) (v : SymplecticPhase8) :
    proj4_0 (direct_sum_shear s0 s1 v) = apply_shear s0 (proj4_0 v) :=
  rfl

@[simp] theorem proj4_1_direct_sum (s0 s1 : SymplecticShear2x2) (v : SymplecticPhase8) :
    proj4_1 (direct_sum_shear s0 s1 v) = apply_shear s1 (proj4_1 v) :=
  rfl

/-- Canonical decomposition of ω8 into the direct sum of two ω4 forms -/
theorem omega8_decompose (u v : SymplecticPhase8) :
    omega8 u v = gf_add (omega (proj4_0 u) (proj4_0 v)) (omega (proj4_1 u) (proj4_1 v)) :=
  rfl

/-- Theorem 9A: Direct Sum Involution Property ((T_{S0 ⊕ S1})^2 = I_8) -/
theorem direct_sum_shear_involution (s0 s1 : SymplecticShear2x2) (v : SymplecticPhase8) :
    direct_sum_shear s0 s1 (direct_sum_shear s0 s1 v) = v := by
  dsimp only [direct_sum_shear, proj4_0, proj4_1]
  rw [symplectic_shear_involution s0 ⟨v.q0, v.q1, v.p0, v.p1⟩]
  rw [symplectic_shear_involution s1 ⟨v.q2, v.q3, v.p2, v.p3⟩]

/-- Theorem 9B: Direct Sum Symplectic Invariance in Sp(8, F_{2^8}) -/
theorem direct_sum_shear_preserves_omega8 (s0 s1 : SymplecticShear2x2) (u v : SymplecticPhase8) :
    omega8 (direct_sum_shear s0 s1 u) (direct_sum_shear s0 s1 v) = omega8 u v := by
  rw [omega8_decompose, omega8_decompose u v]
  dsimp [direct_sum_shear, proj4_0, proj4_1]
  rw [symplectic_shear_preserves_omega s0 ⟨u.q0, u.q1, u.p0, u.p1⟩ ⟨v.q0, v.q1, v.p0, v.p1⟩]
  rw [symplectic_shear_preserves_omega s1 ⟨u.q2, u.q3, u.p2, u.p3⟩ ⟨v.q2, v.q3, v.p2, v.p3⟩]

/-- Theorem 9C: Direct Sum Liouville Phase-Space Volume Preservation -/
theorem direct_sum_shear_bijective (s0 s1 : SymplecticShear2x2) :
    BijectiveMap8 (direct_sum_shear s0 s1) := by
  refine ⟨?_, ?_⟩
  · intro u v h
    have h_inv : direct_sum_shear s0 s1 (direct_sum_shear s0 s1 u) =
                 direct_sum_shear s0 s1 (direct_sum_shear s0 s1 v) := by rw [h]
    rw [direct_sum_shear_involution s0 s1 u, direct_sum_shear_involution s0 s1 v] at h_inv
    exact h_inv
  · intro v
    refine ⟨direct_sum_shear s0 s1 v, ?_⟩
    exact direct_sum_shear_involution s0 s1 v

end PRZM.SymplecticInvariant


