/-
  PRZM Mathematical Foundation: Lean 4 Formalization
  Module: PRZM.BranchCollimation
  Pillar: Universal Instruction Branch Collimation for ARM64 & RISC-V in Aff(1, Z/2^32Z)
  Theorem: 36
-/

import PRZM.GaloisField

namespace PRZM.BranchCollimation

open PRZM.GaloisField

abbrev Word32 := BitVec 32

/-- Predicate matching ARM64 B (0x14000000) and BL (0x94000000) opcodes -/
def is_arm64_branch (v : Word32) : Bool :=
  (v &&& 0x7C000000#32) == 0x14000000#32

/-- Extract 26-bit immediate displacement from instruction -/
def get_imm26 (v : Word32) : Word32 :=
  v &&& 0x03FFFFFF#32

/-- Extract 6-bit opcode prefix invariant -/
def get_opcode6 (v : Word32) : Word32 :=
  v &&& 0xFC000000#32

/-- Forward affine gauge translation in Word32:
    Target = imm + pc_word -/
def gauge_forward (imm : Word32) (pc_word : Word32) : Word32 :=
  imm + pc_word

/-- Inverse affine gauge translation in Word32:
    imm = Target - pc_word -/
def gauge_inverse (target : Word32) (pc_word : Word32) : Word32 :=
  target - pc_word

/-- Elementary ring automorphism invertibility in Word32: (imm + pc) - pc = imm -/
theorem affine_gauge_step_invertible (imm pc : Word32) :
    gauge_inverse (gauge_forward imm pc) pc = imm := by
  dsimp [gauge_forward, gauge_inverse]
  exact BitVec.add_sub_cancel imm pc

/-- Natural number word displacement exact invertibility -/
theorem affine_gauge_nat_invertible (imm pc : Nat) :
    (imm + pc) - pc = imm := by
  exact Nat.add_sub_cancel imm pc

/-- Invariance of branch target collimation:
    Two call instructions from different call sites pc1 and pc2 targeting the same
    destination function T_word are transformed into identical collimated words. -/
theorem branch_collimation_target_identity
    (pc1 pc2 imm1 imm2 T_word : Word32)
    (h1 : imm1 + pc1 = T_word)
    (h2 : imm2 + pc2 = T_word) :
    gauge_forward imm1 pc1 = gauge_forward imm2 pc2 := by
  dsimp [gauge_forward]
  rw [h1, h2]

/-- Opcode bit disjointness invariant:
    Opcode prefix mask (bits 31..26) and immediate mask (bits 25..0) have zero bitwise intersection. -/
theorem opcode_mask_disjoint :
    (0xFC000000#32 &&& 0x03FFFFFF#32) = 0#32 := by
  decide

end PRZM.BranchCollimation
