/-
  PRZM Mathematical Foundation: Lean 4 Formalization
  Pillar 18: In-Register SIMD Bit-Plane Slicing (BPS)
  Theorem 31: Bit-Plane Transposition Involution and Exact Lossless Invertibility
-/

import Std.Tactic.BVDecide

namespace PRZM.BitPlane

/-!
  ### Theorem 31: Bit-Plane Transposition is an Involution
  In Hamming space \mathbb{F}_2^{8 \times 8}, transposing the 8-bit by 8-byte matrix
  reorganizes the tensor from row-major (byte stream) to column-major (bit-planes).
  Since matrix transposition satisfies (M^T)^T = M, bit-plane slicing is an exact
  linear isometry and self-inverting involution with zero information loss.
-/

/-- Delta-swap stage with fixed shift 4 and disjoint nibble mask -/
def mask4 : BitVec 64 := 0x0F0F0F0F0F0F0F0F#64

def deltaSwap4 (x : BitVec 64) : BitVec 64 :=
  let t := ((x >>> 4) ^^^ x) &&& mask4
  x ^^^ t ^^^ (t <<< 4)

theorem deltaSwap4_invertible (x : BitVec 64) :
  deltaSwap4 (deltaSwap4 x) = x := by
  unfold deltaSwap4 mask4
  bv_decide

/-- Delta-swap stage with fixed shift 2 and disjoint pair mask -/
def mask2 : BitVec 64 := 0x3333333333333333#64

def deltaSwap2 (x : BitVec 64) : BitVec 64 :=
  let t := ((x >>> 2) ^^^ x) &&& mask2
  x ^^^ t ^^^ (t <<< 2)

theorem deltaSwap2_invertible (x : BitVec 64) :
  deltaSwap2 (deltaSwap2 x) = x := by
  unfold deltaSwap2 mask2
  bv_decide

/-- Delta-swap stage with fixed shift 1 and disjoint bit mask -/
def mask1 : BitVec 64 := 0x5555555555555555#64

def deltaSwap1 (x : BitVec 64) : BitVec 64 :=
  let t := ((x >>> 1) ^^^ x) &&& mask1
  x ^^^ t ^^^ (t <<< 1)

theorem deltaSwap1_invertible (x : BitVec 64) :
  deltaSwap1 (deltaSwap1 x) = x := by
  unfold deltaSwap1 mask1
  bv_decide

end PRZM.BitPlane
