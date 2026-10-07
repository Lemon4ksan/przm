// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
)

func findCDecoder(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"test_przm_dec.exe",
		"tests/test_przm_dec.exe",
		filepath.Join("..", "test_przm_dec.exe"),
		filepath.Join("..", "tests", "test_przm_dec.exe"),
	}
	for _, cand := range candidates {
		abs, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	t.Fatal("Pure C test executable test_przm_dec.exe not found")
	return ""
}

// TestChallenger_M3_2_Optimal_PureC_DiversePayloads tests diverse payloads
// (Go/C source code, structured JSON, binary bytecode pattern, dense 2B/3B repeats,
// and high entropy with embedded repeats) compressed with Level 3 optimal parse
// (przm.Compress(data, 3)), then decompresses them using test_przm_dec.exe.
// Confirms exit code 0 and 100% bit-exact roundtrips.
func TestChallenger_M3_2_Optimal_PureC_DiversePayloads(t *testing.T) {
	cBinPath := findCDecoder(t)
	tmpDir := t.TempDir()

	testCases := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "GoSourceCode",
			gen: func() []byte {
				// Try reading real Go source files first
				var buf bytes.Buffer
				for _, f := range []string{"optimal.go", "compress.go", "format.go"} {
					if content, err := os.ReadFile(f); err == nil {
						buf.Write(content)
					}
				}
				if buf.Len() < 16384 {
					// Fallback synthesis with rich Go language syntax
					for i := 0; i < 400; i++ {
						fmt.Fprintf(&buf, `package przm_test_%d
import (
	"context"
	"fmt"
	"sync"
)
type PipelineNode%d struct {
	ID        int64
	Name      string
	mu        sync.RWMutex
	children  []*PipelineNode%d
}
func (n *PipelineNode%d) Process(ctx context.Context, input []byte) ([]byte, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if len(input) == 0 {
		return nil, fmt.Errorf("empty payload at node %%d", n.ID)
	}
	res := make([]byte, len(input))
	copy(res, input)
	return res, nil
}
`, i, i, i, i)
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "CSourceCode",
			gen: func() []byte {
				var buf bytes.Buffer
				for _, f := range []string{"src/przm_dec.c", "include/przm_dec.h"} {
					if content, err := os.ReadFile(f); err == nil {
						buf.Write(content)
					}
				}
				if buf.Len() < 16384 {
					for i := 0; i < 400; i++ {
						fmt.Fprintf(&buf, `#ifndef PRZM_MODULE_%d_H
#define PRZM_MODULE_%d_H
#include <stdint.h>
#include <stdbool.h>
typedef struct przm_ctx_%d {
    uint32_t state;
    uint64_t counter;
    uint8_t  scratch[128];
} przm_ctx_%d_t;
static inline int przm_step_%d(przm_ctx_%d_t* ctx, const uint8_t* in, size_t len) {
    if (!ctx || !in || len < 4) return -1;
    ctx->counter += len;
    ctx->state ^= *(const uint32_t*)in;
    return 0;
}
#endif /* PRZM_MODULE_%d_H */
`, i, i, i, i, i, i, i)
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "StructuredJSON",
			gen: func() []byte {
				var buf bytes.Buffer
				buf.WriteString("[\n")
				for i := 0; i < 600; i++ {
					if i > 0 {
						buf.WriteString(",\n")
					}
					fmt.Fprintf(&buf, `  {
    "id": %d,
    "uuid": "4f9d2a1b-%04x-%04x-%04x-%012x",
    "event_type": "TELEMETRY_SAMPLE_INGESTION",
    "timestamp": %d,
    "status": "PROCESSED",
    "payload": {
      "metric_name": "cpu_core_utilization",
      "metric_value": %f,
      "labels": {
        "region": "us-east-1",
        "zone": "us-east-1a",
        "cluster": "k8s-production-alpha",
        "tier": "compute-highmem"
      }
    },
    "tags": ["monitoring", "telemetry", "prometheus", "production"]
  }`, i, i%65535, (i*3)%65535, (i*7)%65535, i*1234567, 1728000000+i, float64(i%100)/2.5)
				}
				buf.WriteString("\n]\n")
				return buf.Bytes()
			},
		},
		{
			name: "BinaryBytecodePattern",
			gen: func() []byte {
				// Machine bytecode instructions with repeated subroutines, CALLs, JMPs, registers
				var buf bytes.Buffer
				prologue := []byte{0x55, 0x48, 0x89, 0xE5, 0x48, 0x83, 0xEC, 0x40}
				epilogue := []byte{0x48, 0x89, 0xEC, 0x5D, 0xC3}
				instructions := [][]byte{
					{0x48, 0x89, 0x7D, 0xF8},             // mov [rbp-8], rdi
					{0x48, 0x89, 0x75, 0xF0},             // mov [rbp-16], rsi
					{0x48, 0x8B, 0x45, 0xF8},             // mov rax, [rbp-8]
					{0x48, 0x03, 0x45, 0xF0},             // add rax, [rbp-16]
					{0x48, 0x83, 0xF8, 0x00},             // cmp rax, 0
					{0x0F, 0x84, 0x20, 0x00, 0x00, 0x00}, // jz +0x20
					{0x48, 0xFF, 0xC0},                   // inc rax
					{0xE8, 0x10, 0x00, 0x00, 0x00},       // call +0x10
				}
				for funcIdx := 0; funcIdx < 500; funcIdx++ {
					buf.Write(prologue)
					for step := 0; step < 12; step++ {
						inst := instructions[(funcIdx+step)%len(instructions)]
						buf.Write(inst)
						// occasional variable operand
						var imm [4]byte
						binary.LittleEndian.PutUint32(imm[:], uint32(funcIdx*step))
						buf.Write(imm[:])
					}
					buf.Write(epilogue)
					// Padding alignment
					alignBytes := (16 - (buf.Len() % 16)) % 16
					for p := 0; p < alignBytes; p++ {
						buf.WriteByte(0x90) // NOP
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "Dense2B3BRepeats",
			gen: func() []byte {
				// Dense alternating 2-byte and 3-byte repeats designed to test short match selection in optimal DP
				var buf bytes.Buffer
				twoBytePairs := []string{"AB", "CD", "EF", "01", "XY", "!!"}
				threeByteTokens := []string{"XYZ", "ABC", "123", "###", "UVW"}

				for i := 0; i < 300; i++ {
					pair := twoBytePairs[i%len(twoBytePairs)]
					for rep := 0; rep < 24; rep++ {
						buf.WriteString(pair)
					}
					tok := threeByteTokens[i%len(threeByteTokens)]
					for rep := 0; rep < 18; rep++ {
						buf.WriteString(tok)
					}
					// Mixed interleaved section
					for m := 0; m < 10; m++ {
						buf.WriteString(pair)
						buf.WriteString(tok)
					}
				}
				return buf.Bytes()
			},
		},
		{
			name: "HighEntropyWithEmbeddedRepeats",
			gen: func() []byte {
				// 64 KiB of pseudorandom noise with deterministic anchors embedded every 1024 bytes
				rng := rand.New(rand.NewSource(0x5EED_0001))
				size := 65536
				raw := make([]byte, size)
				rng.Read(raw)

				anchorA := []byte("PRZM_DETERMINISTIC_ANCHOR_SECTION_OPTIMAL_MATCH_AAA_")
				anchorB := []byte("PRZM_DETERMINISTIC_ANCHOR_SECTION_OPTIMAL_MATCH_BBB_")

				for pos := 256; pos+len(anchorA) < size; pos += 1024 {
					if (pos/1024)%2 == 0 {
						copy(raw[pos:], anchorA)
					} else {
						copy(raw[pos:], anchorB)
					}
				}
				return raw
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			raw := tc.gen()
			if len(raw) == 0 {
				t.Fatalf("test case generator produced empty payload")
			}

			// Compress with Level 3 (Optimal Parse)
			comp, err := przm.Compress(raw, 3)
			if err != nil {
				t.Fatalf("przm.Compress(level 3) failed: %v", err)
			}
			if len(comp) == 0 {
				t.Fatalf("przm.Compress returned empty compressed frame")
			}

			inPath := filepath.Join(tmpDir, tc.name+".przm")
			outPath := filepath.Join(tmpDir, tc.name+".out")

			if err := os.WriteFile(inPath, comp, 0644); err != nil {
				t.Fatalf("failed to write compressed frame: %v", err)
			}

			// Decompress with Pure C executable
			cmd := exec.Command(cBinPath, inPath, outPath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("test_przm_dec.exe failed on %s (exit: %v):\nOutput:\n%s", tc.name, err, string(output))
			}

			decomp, err := os.ReadFile(outPath)
			if err != nil {
				t.Fatalf("failed to read decompressed output: %v", err)
			}

			if !bytes.Equal(decomp, raw) {
				t.Fatalf("Lossy decompression on %s! Raw len=%d, Decomp len=%d", tc.name, len(raw), len(decomp))
			}
			t.Logf("[%s] OK: Raw=%d B -> Comp=%d B (ratio: %.2fx) -> 100%% bit-exact Pure C decompress",
				tc.name, len(raw), len(comp), float64(len(raw))/float64(len(comp)))
		})
	}
}

// TestChallenger_M3_2_Adversarial_Corruption validates that corrupt, malformed,
// or truncated streams cleanly fail in test_przm_dec.exe with non-zero exit codes
// without crashes, memory faults, or segmentation violations.
func TestChallenger_M3_2_Adversarial_Corruption(t *testing.T) {
	cBinPath := findCDecoder(t)
	tmpDir := t.TempDir()

	// Base valid compressed frame produced by Level 3
	var basePayload bytes.Buffer
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&basePayload, "RECORD_%04d: optimal parsing adversarial baseline payload data section\n", i)
	}
	rawBase := basePayload.Bytes()
	validFrame, err := przm.Compress(rawBase, 3)
	if err != nil {
		t.Fatalf("failed to compress base frame: %v", err)
	}

	testCases := []struct {
		name       string
		makeCorrupt func() []byte
	}{
		{
			name: "Truncated_4B",
			makeCorrupt: func() []byte {
				if len(validFrame) < 4 {
					return []byte{0x50, 0x52, 0x5A, 0x4D}
				}
				return validFrame[:4]
			},
		},
		{
			name: "Truncated_7B",
			makeCorrupt: func() []byte {
				if len(validFrame) < 7 {
					return []byte{0x50, 0x52, 0x5A, 0x4D, 0x00, 0x01, 0x00}
				}
				return validFrame[:7]
			},
		},
		{
			name: "CorruptMagic",
			makeCorrupt: func() []byte {
				bad := make([]byte, len(validFrame))
				copy(bad, validFrame)
				// Overwrite magic bytes with invalid magic 0xDEADBEEF
				bad[0] = 0xEF
				bad[1] = 0xBE
				bad[2] = 0xAD
				bad[3] = 0xDE
				return bad
			},
		},
		{
			name: "OversizedUncompressedSize_Standard",
			makeCorrupt: func() []byte {
				// Standard PRZM frame with uncompressed size claiming 16 MiB (0xFFFFFF), but total length is only 20 bytes
				bad := make([]byte, 20)
				binary.LittleEndian.PutUint32(bad[0:4], 0x4D5A5250) // 'PRZM'
				bad[4] = 0x00                                      // Flags
				bad[5] = 0xFF                                      // u_size = 0xFFFFFF (16,777,215)
				bad[6] = 0xFF
				bad[7] = 0xFF
				return bad
			},
		},
		{
			name: "OversizedUncompressedSize_SpecializedExceedsMax",
			makeCorrupt: func() []byte {
				// Specialized SYMP frame with orig_len = 32 MiB (> PRZM_MAX_BLOCK_SIZE 16 MiB)
				bad := make([]byte, 32)
				binary.LittleEndian.PutUint32(bad[0:4], 0x53594D50) // 'SYMP'
				bad[4] = 0x01                                      // mode
				binary.LittleEndian.PutUint32(bad[8:12], 32*1024*1024) // 32 MiB
				return bad
			},
		},
		{
			name: "CorruptSectionDirectory",
			makeCorrupt: func() []byte {
				bad := make([]byte, len(validFrame))
				copy(bad, validFrame)
				// Corrupt section directory bytes (offsets 8..14 in standard header)
				if len(bad) > 16 {
					for i := 8; i < 16 && i < len(bad); i++ {
						bad[i] = 0xFF
					}
				}
				return bad
			},
		},
	}

	// Add 30 randomized bit-flipping test cases targeting critical structural framing
	// (magic bytes 0..3, uncompressed size 5..7, and section directory 8..11)
	rng := rand.New(rand.NewSource(0xBAD_C0DE))
	criticalIndices := []int{0, 1, 2, 3, 5, 6, 7, 8, 9, 10, 11}
	for i := 0; i < 30; i++ {
		iter := i
		testCases = append(testCases, struct {
			name        string
			makeCorrupt func() []byte
		}{
			name: fmt.Sprintf("RandomBitFlipping_Structural_%02d", iter),
			makeCorrupt: func() []byte {
				bad := make([]byte, len(validFrame))
				copy(bad, validFrame)
				flips := 1 + rng.Intn(3)
				for f := 0; f < flips; f++ {
					targetByte := criticalIndices[rng.Intn(len(criticalIndices))]
					if targetByte < len(bad) {
						targetBit := uint(rng.Intn(8))
						bad[targetByte] ^= (1 << targetBit)
					}
				}
				return bad
			},
		})
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			corruptStream := tc.makeCorrupt()
			inPath := filepath.Join(tmpDir, tc.name+".bad.przm")
			outPath := filepath.Join(tmpDir, tc.name+".bad.out")

			if err := os.WriteFile(inPath, corruptStream, 0644); err != nil {
				t.Fatalf("failed to write corrupt input: %v", err)
			}

			cmd := exec.Command(cBinPath, inPath, outPath)
			output, err := cmd.CombinedOutput()

			// test_przm_dec.exe MUST fail (err != nil)
			if err == nil {
				t.Fatalf("[%s] Security hazard: test_przm_dec.exe succeeded with return code 0 on corrupted stream!", tc.name)
			}

			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("[%s] Unexpected exec error: %v", tc.name, err)
			}

			exitCode := exitErr.ExitCode()
			uExitCode := uint32(exitCode)

			// Verify it did not crash with access violation / segfault / stack corruption
			// Windows NTSTATUS codes:
			// 0xC0000005: STATUS_ACCESS_VIOLATION
			// 0xC00000FD: STATUS_STACK_OVERFLOW
			// 0xC000001D: STATUS_ILLEGAL_INSTRUCTION
			// 0xC0000028: STATUS_BAD_STACK
			if uExitCode == 0xC0000005 || uExitCode == 0xC00000FD || uExitCode == 0xC000001D || uExitCode == 0xC0000028 {
				t.Fatalf("[%s] CRASH / SEGFAULT DETECTED: exit code 0x%X\nOutput:\n%s", tc.name, uExitCode, string(output))
			}

			if exitCode <= 0 {
				t.Fatalf("[%s] Expected positive non-zero error exit code, got %d", tc.name, exitCode)
			}

			t.Logf("[%s] OK: test_przm_dec.exe rejected corrupt stream cleanly with exit code %d (output: %s)",
				tc.name, exitCode, bytes.TrimSpace(output))
		})
	}
}
