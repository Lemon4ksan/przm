package przm

import (
	"bytes"
	"testing"
)

// TestFunctorVM_Theorem25_Functoriality verifies that F preserves identity and morphism composition.
func TestFunctorVM_Theorem25_Functoriality(t *testing.T) {
	f := NewFunctor()

	// 1. Check Identity: F(id) is identity (emits nothing/NOP)
	_, emit := f.MapMorphism(Morphism{Type: MorphIdentity})
	if emit {
		t.Fatalf("Theorem 25 violated: F(id) must be identity (non-emitting)")
	}

	// 2. Check Composition: F(g o f) == F(g) o F(f)
	m1 := Morphism{Type: MorphLoad, DstID: 1, Imm: 0}
	m2 := Morphism{Type: MorphBroadcast, DstID: 2, Imm: 0x5A}
	m3 := Morphism{Type: MorphXOR, DstID: 3, Src1ID: 1, Src2ID: 2}

	pipeline := []Morphism{m1, m2, m3}
	prog := f.Compile(pipeline)

	if len(prog.Instructions) != 3 {
		t.Fatalf("Expected 3 compiled instructions, got %d", len(prog.Instructions))
	}

	// Verify instruction opcodes match morphism types
	op0, _, _, _, _ := prog.Instructions[0].Decode()
	op1, _, _, _, _ := prog.Instructions[1].Decode()
	op2, _, _, _, _ := prog.Instructions[2].Decode()

	if op0 != OpLOADSTREAM || op1 != OpEXPAND || op2 != OpXOR {
		t.Fatalf("Compiled instructions do not preserve morphism types: %v, %v, %v", op0, op1, op2)
	}
}

// TestFunctorVM_Theorem25_NaturalTransformation verifies Kolmogorov complexity reduction via eta.
func TestFunctorVM_Theorem25_NaturalTransformation(t *testing.T) {
	// Construct pipeline with opportunity for XOR fusion and NOP elimination
	f := NewFunctor()

	// Morphisms:
	// Z_1 = load
	// Z_2 = load
	// Z_3 = load
	// Z_1 = Z_1 ^ Z_2
	// Z_1 = Z_1 ^ Z_3
	// Identity
	morphisms := []Morphism{
		{Type: MorphLoad, DstID: 1, Imm: 0},
		{Type: MorphLoad, DstID: 2, Imm: 1},
		{Type: MorphLoad, DstID: 3, Imm: 2},
		{Type: MorphXOR, DstID: 1, Src1ID: 1, Src2ID: 2},
		{Type: MorphXOR, DstID: 1, Src1ID: 1, Src2ID: 3},
		{Type: MorphIdentity},
		{Type: MorphEmit, Src1ID: 1},
	}

	rawProg := f.Compile(morphisms)
	rawLen := len(rawProg.Instructions)

	optProg := NaturalTransformationEta(rawProg)
	optLen := len(optProg.Instructions)

	if optLen >= rawLen {
		t.Fatalf("Natural transformation failed to reduce instruction count: raw %d, opt %d", rawLen, optLen)
	}

	// Verify that the fused instruction is OpTERNLOG
	foundTernlog := false
	for _, inst := range optProg.Instructions {
		op, _, _, _, _ := inst.Decode()
		if op == OpTERNLOG {
			foundTernlog = true
			break
		}
	}

	if !foundTernlog {
		t.Fatalf("Expected OpTERNLOG after natural transformation fusion")
	}
}

// TestFunctorVM_ExecutionEquivalence verifies that the compiled VM program executes correctly.
func TestFunctorVM_ExecutionEquivalence(t *testing.T) {
	// Construct pipeline that loads stream 0, XORs with broadcast 0xFF, and emits 64 bytes
	morphisms := []Morphism{
		{Type: MorphLoad, DstID: 0, Imm: 0},
		{Type: MorphBroadcast, DstID: 1, Imm: 0xFF},
		{Type: MorphXOR, DstID: 0, Src1ID: 0, Src2ID: 1},
		{Type: MorphEmit, Src1ID: 0},
	}

	prog, err := FunctorialVMCompiler(morphisms)
	if err != nil {
		t.Fatalf("FunctorialVMCompiler error: %v", err)
	}

	inputBlock := bytes.Repeat([]byte{0x55}, 64)
	streamData := [][]byte{inputBlock}
	streamPtrs := []int{0}

	ctx := &VMContext{}
	dst := make([]byte, 64)
	outPtr := 0

	err = ExecuteProgramScalar(prog, ctx, streamData, streamPtrs, dst, &outPtr, 64)
	if err != nil {
		t.Fatalf("ExecuteProgramScalar error: %v", err)
	}

	// 0x55 ^ 0xFF == 0xAA
	expected := bytes.Repeat([]byte{0xAA}, 64)
	if !bytes.Equal(dst, expected) {
		t.Fatalf("VM execution mismatch: got %x, want %x", dst, expected)
	}
}

// BenchmarkFunctorVM_Compile measures compilation latency of natural transformation.
func BenchmarkFunctorVM_Compile(b *testing.B) {
	morphisms := []Morphism{
		{Type: MorphLoad, DstID: 1, Imm: 0},
		{Type: MorphLoad, DstID: 2, Imm: 1},
		{Type: MorphLoad, DstID: 3, Imm: 2},
		{Type: MorphXOR, DstID: 1, Src1ID: 1, Src2ID: 2},
		{Type: MorphXOR, DstID: 1, Src1ID: 1, Src2ID: 3},
		{Type: MorphEmit, Src1ID: 1},
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, err := FunctorialVMCompiler(morphisms)
		if err != nil {
			b.Fatal(err)
		}
	}
}
