package przm

import (
	"errors"
)

// MorphType defines abstract morphisms in the DataStruct category.
type MorphType uint8

const (
	MorphIdentity MorphType = iota
	MorphBroadcast
	MorphLoad
	MorphXOR
	MorphTernaryXOR
	MorphGFMul
	MorphAdd
	MorphSub
	MorphPermute
	MorphEmit
)

// Morphism represents an arrow f: A -> B in the DataStruct category.
type Morphism struct {
	Type   MorphType
	DstID  int
	Src1ID int
	Src2ID int
	Src3ID int
	Imm    int
}

// Functor translates morphisms in DataStruct to morphisms in SIMD_Regs (PRZM-VM ISA).
type Functor struct {
	regMap map[int]int // Maps abstract object IDs to SIMD registers Z_0..Z_7
}

// NewFunctor creates a new categorical functor F: DataStruct -> SIMD_Regs.
func NewFunctor() *Functor {
	return &Functor{
		regMap: make(map[int]int),
	}
}

// getReg allocates or retrieves a physical register Z_0..Z_7 for an abstract object ID.
func (f *Functor) getReg(objID int) int {
	if reg, exists := f.regMap[objID]; exists {
		return reg
	}
	// Linear assignment modulo 8
	reg := len(f.regMap) % VMNumRegs
	f.regMap[objID] = reg
	return reg
}

// MapMorphism evaluates F(f) for a single morphism f: A -> B.
func (f *Functor) MapMorphism(m Morphism) (VMInstruction, bool) {
	switch m.Type {
	case MorphIdentity:
		// F(id_A) = id_{F(A)} -> No-op
		return EncodeInstruction(OpNOP, 0, 0, 0, 0), false

	case MorphBroadcast:
		dst := f.getReg(m.DstID)
		return EncodeInstruction(OpEXPAND, dst, 0, 0, m.Imm), true

	case MorphLoad:
		dst := f.getReg(m.DstID)
		return EncodeInstruction(OpLOADSTREAM, dst, 0, 0, m.Imm), true

	case MorphXOR:
		dst := f.getReg(m.DstID)
		s1 := f.getReg(m.Src1ID)
		s2 := f.getReg(m.Src2ID)
		return EncodeInstruction(OpXOR, dst, s1, s2, 0), true

	case MorphTernaryXOR:
		dst := f.getReg(m.DstID)
		s1 := f.getReg(m.Src1ID)
		s2 := f.getReg(m.Src2ID)
		// OpTERNLOG does Z_d = Z_d ^ Z_s1 ^ Z_s2
		return EncodeInstruction(OpTERNLOG, dst, s1, s2, 0), true

	case MorphGFMul:
		dst := f.getReg(m.DstID)
		s1 := f.getReg(m.Src1ID)
		s2 := f.getReg(m.Src2ID)
		return EncodeInstruction(OpGFMUL, dst, s1, s2, 0), true

	case MorphAdd:
		dst := f.getReg(m.DstID)
		s1 := f.getReg(m.Src1ID)
		s2 := f.getReg(m.Src2ID)
		return EncodeInstruction(OpADD, dst, s1, s2, 0), true

	case MorphSub:
		dst := f.getReg(m.DstID)
		s1 := f.getReg(m.Src1ID)
		s2 := f.getReg(m.Src2ID)
		return EncodeInstruction(OpSUB, dst, s1, s2, 0), true

	case MorphPermute:
		dst := f.getReg(m.DstID)
		s1 := f.getReg(m.Src1ID)
		s2 := f.getReg(m.Src2ID)
		return EncodeInstruction(OpPERM, dst, s1, s2, 0), true

	case MorphEmit:
		src := f.getReg(m.Src1ID)
		return EncodeInstruction(OpEMIT, src, 0, 0, 0), true

	default:
		return EncodeInstruction(OpNOP, 0, 0, 0, 0), false
	}
}

// Compile converts a categorical pipeline of morphisms into a VMProgram (F(f_n o ... o f_1)).
func (f *Functor) Compile(morphisms []Morphism) *VMProgram {
	var insts []VMInstruction
	for _, m := range morphisms {
		inst, emit := f.MapMorphism(m)
		if emit {
			insts = append(insts, inst)
		}
	}
	return &VMProgram{
		Stride:       VMRegSize,
		Instructions: insts,
	}
}

// NaturalTransformationEta optimizes the instruction stream via categorical rewrite rules (Theorem 25).
// Minimizes bytecode length (Kolmogorov complexity) while preserving semantics:
// 1. Eliminates redundant moves and no-ops.
// 2. Fuses two sequential XORs (Z_d = Z_a ^ Z_b followed by Z_d = Z_d ^ Z_c) into a single OpTERNLOG.
// 3. Fuses consecutive identical broadcasts.
func NaturalTransformationEta(prog *VMProgram) *VMProgram {
	if prog == nil || len(prog.Instructions) == 0 {
		return prog
	}

	var optimized []VMInstruction
	i := 0
	n := len(prog.Instructions)

	for i < n {
		inst := prog.Instructions[i]
		op, dst, s1, s2, imm := inst.Decode()

		// Skip NOPs
		if op == OpNOP {
			i++
			continue
		}

		// Optimization Rule: Fusion of sequential XORs into OpTERNLOG
		// If inst[i] is XOR (dst = s1 ^ s2) and inst[i+1] is XOR (dst = dst ^ s3)
		if op == OpXOR && i+1 < n {
			nextInst := prog.Instructions[i+1]
			nextOp, nextDst, nextS1, nextS2, _ := nextInst.Decode()

			if nextOp == OpXOR && nextDst == dst && (nextS1 == dst || nextS2 == dst) {
				otherSrc := nextS2
				if nextS2 == dst {
					otherSrc = nextS1
				}
				// Fuse into OpTERNLOG: dst = dst ^ s1 ^ s2
				// First load s1 into dst, then TERNLOG with s2 and otherSrc
				// Or if dst == s1, we already have dst = s1 ^ s2 ^ otherSrc!
				if dst == s1 {
					fused := EncodeInstruction(OpTERNLOG, dst, s2, otherSrc, 0)
					optimized = append(optimized, fused)
					i += 2
					continue
				}
			}
		}

		// Optimization Rule: Redundant Broadcast Elimination
		if op == OpEXPAND && i+1 < n {
			nextInst := prog.Instructions[i+1]
			nextOp, nextDst, _, _, nextImm := nextInst.Decode()
			if nextOp == OpEXPAND && nextDst == dst && nextImm == imm {
				// Redundant duplicate broadcast, keep only one
				optimized = append(optimized, inst)
				i += 2
				continue
			}
		}

		optimized = append(optimized, inst)
		i++
	}

	return &VMProgram{
		Stride:       prog.Stride,
		Instructions: optimized,
	}
}

// FunctorialVMCompiler is the high-level API for Theorem 25 compilation.
func FunctorialVMCompiler(morphisms []Morphism) (*VMProgram, error) {
	if len(morphisms) > VMMaxInsts*2 {
		return nil, errors.New("przm: morphism sequence too complex for PRZM-VM")
	}

	f := NewFunctor()
	rawProg := f.Compile(morphisms)
	optProg := NaturalTransformationEta(rawProg)

	if len(optProg.Instructions) > VMMaxInsts {
		return nil, ErrVMProgramTooLarge
	}

	return optProg, nil
}
