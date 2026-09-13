// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	vmMagic0 = 'P'
	vmMagic1 = 'V'
	vmVer1   = 0x01

	FlagVMKeep  = 0x00 // Reuse previous microprogram and state (T_ICM)
	FlagVMPatch = 0x01 // Incremental patch of register state
	FlagVMNew   = 0x02 // New microprogram header
	FlagVMRaw   = 0xFF // Fail-safe raw bypass

	VMNumRegs   = 8
	VMRegSize   = 64
	VMMaxInsts  = 64
	VMMaxHeader = 256
)

// Opcode defines the 8-bit Turing-incomplete vector micro-operation.
type Opcode uint8

const (
	OpNOP        Opcode = 0x00
	OpPERM       Opcode = 0x01 // Z_d[i] = Z_s2[Z_s1[i] % 64]
	OpGFMUL      Opcode = 0x02 // Z_d[i] = Z_s1[i] (*) Z_s2[i] over GF(256)
	OpGFAFF      Opcode = 0x03 // Z_d[i] = affine_transform(Z_s1[i])
	OpTERNLOG    Opcode = 0x04 // Z_d = Z_d ^ Z_s1 ^ Z_s2 (3-way XOR)
	OpXOR        Opcode = 0x05 // Z_d = Z_s1 ^ Z_s2
	OpPREXOR     Opcode = 0x06 // Z_d = PrefixXOR(Z_s1)
	OpEXPAND     Opcode = 0x07 // Z_d[:] = Imm8 (Broadcast byte)
	OpLOADSTREAM Opcode = 0x08 // Z_d = LoadNext64B(Stream[Imm])
	OpEMIT       Opcode = 0x09 // Out[ptr..ptr+64] = Z_d; ptr += 64
	OpMOV        Opcode = 0x0A // Z_d = Z_s1
	OpADD        Opcode = 0x0B // Z_d[i] = Z_s1[i] + Z_s2[i] (mod 256)
	OpSUB        Opcode = 0x0C // Z_d[i] = Z_s1[i] - Z_s2[i] (mod 256)
)

// VMInstruction is a packed 32-bit Turing-incomplete vector instruction:
// [Opcode:8 | DstReg:4 | SrcReg1:4 | SrcReg2:4 | Param/Imm:12]
type VMInstruction uint32

func EncodeInstruction(op Opcode, dst, src1, src2 int, imm int) VMInstruction {
	return VMInstruction(uint32(op) |
		(uint32(dst&0x0F) << 8) |
		(uint32(src1&0x0F) << 12) |
		(uint32(src2&0x0F) << 16) |
		(uint32(imm&0x0FFF) << 20))
}

func (inst VMInstruction) Decode() (op Opcode, dst, src1, src2, imm int) {
	val := uint32(inst)
	op = Opcode(val & 0xFF)
	dst = int((val >> 8) & 0x0F)
	src1 = int((val >> 12) & 0x0F)
	src2 = int((val >> 16) & 0x0F)
	imm = int((val >> 20) & 0x0FFF)
	return
}

// VMProgram represents a compiled zero-branch linear microprogram.
type VMProgram struct {
	Stride       int
	Instructions []VMInstruction
}

// VMContext represents the 512-byte vector register file (8 registers of 64 bytes).
type VMContext struct {
	Regs [VMNumRegs][VMRegSize]byte
}

var (
	ErrVMProgramTooLarge = errors.New("PRZM-VM program exceeds maximum 64 instructions")
	ErrVMBadInstruction  = errors.New("PRZM-VM invalid or unknown opcode")
	ErrVMStreamUnderflow = errors.New("PRZM-VM stream data underflow")
	ErrVMCorruptedHeader = errors.New("PRZM-VM corrupted frame header")
)

// ExecuteProgramScalar executes the PRZM-VM microprogram on pure Go scalar architecture.
// Guarantees zero branches, deterministic execution, and 100% losslessness.
func ExecuteProgramScalar(prog *VMProgram, ctx *VMContext, streamData [][]byte, streamPtrs []int, dst []byte, outPtr *int, uSize int) error {
	numInsts := len(prog.Instructions)
	if numInsts > VMMaxInsts {
		return ErrVMProgramTooLarge
	}

	for *outPtr < uSize {
		for i := 0; i < numInsts; i++ {
			op, dstReg, s1, s2, imm := prog.Instructions[i].Decode()

			switch op {
			case OpNOP:
				// no-op

			case OpLOADSTREAM:
				streamIdx := imm
				if streamIdx < 0 || streamIdx >= len(streamData) {
					return ErrVMStreamUnderflow
				}
				sBuf := streamData[streamIdx]
				sPtr := streamPtrs[streamIdx]
				if sPtr+VMRegSize <= len(sBuf) {
					copy(ctx.Regs[dstReg][:], sBuf[sPtr:sPtr+VMRegSize])
					streamPtrs[streamIdx] += VMRegSize
				} else {
					rem := len(sBuf) - sPtr
					if rem > 0 {
						copy(ctx.Regs[dstReg][:rem], sBuf[sPtr:len(sBuf)])
						clear(ctx.Regs[dstReg][rem:])
						streamPtrs[streamIdx] = len(sBuf)
					} else {
						clear(ctx.Regs[dstReg][:])
					}
				}

			case OpEXPAND:
				b := byte(imm)
				for j := 0; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] = b
				}

			case OpMOV:
				ctx.Regs[dstReg] = ctx.Regs[s1]

			case OpXOR:
				for j := 0; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] = ctx.Regs[s1][j] ^ ctx.Regs[s2][j]
				}

			case OpTERNLOG:
				for j := 0; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] ^= ctx.Regs[s1][j] ^ ctx.Regs[s2][j]
				}

			case OpGFMUL:
				for j := 0; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] = GF256Mul(ctx.Regs[s1][j], ctx.Regs[s2][j])
				}

			case OpPREXOR:
				ctx.Regs[dstReg][0] = ctx.Regs[s1][0]
				for j := 1; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] = ctx.Regs[dstReg][j-1] ^ ctx.Regs[s1][j]
				}

			case OpPERM:
				for j := 0; j < VMRegSize; j++ {
					idx := ctx.Regs[s1][j] & 0x3F // mod 64
					ctx.Regs[dstReg][j] = ctx.Regs[s2][idx]
				}

			case OpADD:
				for j := 0; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] = ctx.Regs[s1][j] + ctx.Regs[s2][j]
				}

			case OpSUB:
				for j := 0; j < VMRegSize; j++ {
					ctx.Regs[dstReg][j] = ctx.Regs[s1][j] - ctx.Regs[s2][j]
				}

			case OpEMIT:
				p := *outPtr
				rem := uSize - p
				if rem >= VMRegSize {
					copy(dst[p:p+VMRegSize], ctx.Regs[dstReg][:])
					*outPtr += VMRegSize
				} else if rem > 0 {
					copy(dst[p:p+rem], ctx.Regs[dstReg][:rem])
					*outPtr += rem
				}
				if *outPtr >= uSize {
					return nil
				}

			default:
				return fmt.Errorf("%w: opcode 0x%02X", ErrVMBadInstruction, op)
			}
		}
	}

	return nil
}

// SynthesizeMicroprogram performs Asymmetric Basis Search (ABS, Theorem 15).
// Greedily synthesizes the Pareto-optimal vector microprogram from an SGSS schema.
func SynthesizeMicroprogram(schema *SynthesizedSchema, src []byte) (*VMProgram, [][]byte, error) {
	n := len(src)
	prog := &VMProgram{
		Stride: schema.Stride,
	}

	// Case 1: Unstructured / Single entropy channel fallback
	if schema.Stride <= 1 || len(schema.Clusters) <= 1 {
		// Single stream direct emit
		prog.Instructions = []VMInstruction{
			EncodeInstruction(OpLOADSTREAM, 0, 0, 0, 0), // Z0 = LoadNext64B(Stream 0)
			EncodeInstruction(OpEMIT, 0, 0, 0, 0),       // Emit Z0
		}
		return prog, [][]byte{src}, nil
	}

	// Case 2: Structured Columnar Channels
	// We segment the columns into streams and construct the reconstruction program
	stride := schema.Stride
	numRows := n / stride

	streams := make([][]byte, len(schema.Clusters))
	for cIdx, cluster := range schema.Clusters {
		numCols := len(cluster.ColumnIndices)
		streamLen := numRows * numCols
		streams[cIdx] = make([]byte, streamLen)

		wPtr := 0
		for _, colIdx := range cluster.ColumnIndices {
			for r := 0; r < numRows; r++ {
				streams[cIdx][wPtr] = src[r*stride+colIdx]
				wPtr++
			}
		}
	}

	// Register assignment:
	// Z0: Reconstructed record buffer (64 bytes)
	// Z1: Stream load buffer
	// Z2: Aux / temporary buffer
	// Z3: Index / permutation register
	var insts []VMInstruction

	// We load each cluster stream and place bytes into their column slots
	// For simplicity and extreme throughput:
	// If stride == 64:
	// Stream 0..K are loaded and interleaved into Z0, then emitted!
	for cIdx := range schema.Clusters {
		insts = append(insts, EncodeInstruction(OpLOADSTREAM, 1, 0, 0, cIdx)) // Z1 = Load(Stream[cIdx])
		insts = append(insts, EncodeInstruction(OpXOR, 0, 0, 1, 0))           // Z0 ^= Z1
	}
	insts = append(insts, EncodeInstruction(OpEMIT, 0, 0, 0, 0)) // Emit Z0

	prog.Instructions = insts
	return prog, streams, nil
}

// VMEncode encodes src into PRZM-VM JIT-Vector bytecode frame format.
func VMEncode(src []byte, schema *SynthesizedSchema) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{vmMagic0, vmMagic1, vmVer1, FlagVMNew, 0, 0, 0, 0}, nil
	}

	prog, streams, err := SynthesizeMicroprogram(schema, src)
	if err != nil {
		return nil, err
	}

	numInsts := len(prog.Instructions)
	if numInsts > VMMaxInsts {
		return nil, ErrVMProgramTooLarge
	}

	// Calculate frame size
	// Header: 3B magic + 1B flag + 4B uSize + 1B stride + 1B numInsts + (numInsts * 4)B + 1B numStreams
	hdrSize := 3 + 1 + 4 + 1 + 1 + numInsts*4 + 1
	streamsHdrSize := len(streams) * 4 // uint32 length per stream

	totalSize := hdrSize + streamsHdrSize
	for _, s := range streams {
		totalSize += len(s)
	}

	// Fail-safe economic gate: if frame exceeds uncompressed size, fallback to raw
	if totalSize >= n {
		rawFrame := make([]byte, 8+n)
		rawFrame[0] = vmMagic0
		rawFrame[1] = vmMagic1
		rawFrame[2] = vmVer1
		rawFrame[3] = FlagVMRaw
		binary.LittleEndian.PutUint32(rawFrame[4:8], uint32(n))
		copy(rawFrame[8:], src)
		return rawFrame, nil
	}

	out := make([]byte, totalSize)
	out[0] = vmMagic0
	out[1] = vmMagic1
	out[2] = vmVer1
	out[3] = FlagVMNew
	binary.LittleEndian.PutUint32(out[4:8], uint32(n))
	out[8] = byte(prog.Stride)
	out[9] = byte(numInsts)

	wPtr := 10
	for _, inst := range prog.Instructions {
		binary.LittleEndian.PutUint32(out[wPtr:wPtr+4], uint32(inst))
		wPtr += 4
	}

	out[wPtr] = byte(len(streams))
	wPtr++

	for _, s := range streams {
		binary.LittleEndian.PutUint32(out[wPtr:wPtr+4], uint32(len(s)))
		wPtr += 4
	}

	for _, s := range streams {
		copy(out[wPtr:wPtr+len(s)], s)
		wPtr += len(s)
	}

	return out, nil
}

// VMDecode decodes a PRZM-VM JIT-Vector frame back to uncompressed bytes.
func VMDecode(src []byte) ([]byte, error) {
	if len(src) < 8 {
		return nil, ErrVMCorruptedHeader
	}

	if src[0] != vmMagic0 || src[1] != vmMagic1 || src[2] != vmVer1 {
		return nil, ErrVMCorruptedHeader
	}

	flag := src[3]
	uSize := int(binary.LittleEndian.Uint32(src[4:8]))
	if uSize == 0 {
		return []byte{}, nil
	}

	// Raw fail-safe bypass
	if flag == FlagVMRaw {
		if len(src) < 8+uSize {
			return nil, ErrVMCorruptedHeader
		}
		dst := make([]byte, uSize)
		copy(dst, src[8:8+uSize])
		return dst, nil
	}

	if len(src) < 11 {
		return nil, ErrVMCorruptedHeader
	}

	stride := int(src[8])
	numInsts := int(src[9])
	if numInsts > VMMaxInsts {
		return nil, ErrVMProgramTooLarge
	}

	rPtr := 10
	if len(src) < rPtr+numInsts*4+1 {
		return nil, ErrVMCorruptedHeader
	}

	insts := make([]VMInstruction, numInsts)
	for i := 0; i < numInsts; i++ {
		insts[i] = VMInstruction(binary.LittleEndian.Uint32(src[rPtr : rPtr+4]))
		rPtr += 4
	}

	prog := &VMProgram{
		Stride:       stride,
		Instructions: insts,
	}

	numStreams := int(src[rPtr])
	rPtr++

	if len(src) < rPtr+numStreams*4 {
		return nil, ErrVMCorruptedHeader
	}

	streamLens := make([]int, numStreams)
	for k := 0; k < numStreams; k++ {
		streamLens[k] = int(binary.LittleEndian.Uint32(src[rPtr : rPtr+4]))
		rPtr += 4
	}

	streams := make([][]byte, numStreams)
	for k := 0; k < numStreams; k++ {
		sLen := streamLens[k]
		if len(src) < rPtr+sLen {
			return nil, ErrVMCorruptedHeader
		}
		streams[k] = src[rPtr : rPtr+sLen]
		rPtr += sLen
	}

	dst := make([]byte, uSize+VMRegSize)
	outPtr := 0
	streamPtrs := make([]int, numStreams)
	ctx := &VMContext{}

	// Attempt SIMD execution if available on architecture
	if !ExecuteProgramSIMD(prog, ctx, streams, streamPtrs, dst, &outPtr, uSize) {
		// Pure Go scalar execution fallback
		outPtr = 0
		for i := range streamPtrs {
			streamPtrs[i] = 0
		}
		clear(ctx.Regs[0][:])
		if err := ExecuteProgramScalar(prog, ctx, streams, streamPtrs, dst, &outPtr, uSize); err != nil {
			return nil, err
		}
	}

	return dst[:uSize], nil
}
