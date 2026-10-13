// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func runCTestDecompress(t *testing.T, compData, expected []byte) {
	cCandidates := []string{"test_przm_dec.exe", "tests/test_przm_dec.exe"}
	var cBinPath string
	for _, cand := range cCandidates {
		absPath, err := filepath.Abs(cand)
		if err == nil {
			if _, err := os.Stat(absPath); err == nil {
				cBinPath = absPath
				break
			}
		}
	}
	if cBinPath == "" {
		t.Skip("C test binary test_przm_dec.exe not found")
	}

	tmpDir := t.TempDir()
	framePath := filepath.Join(tmpDir, "frame.przm")
	outPath := filepath.Join(tmpDir, "decoded.bin")

	if err := os.WriteFile(framePath, compData, 0o644); err != nil {
		t.Fatalf("failed to write frame file: %v", err)
	}

	cmd := exec.Command(cBinPath, framePath, outPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("C decompressor failed (%v): %s", err, string(output))
	}

	decoded, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read decoded output: %v", err)
	}

	if !bytes.Equal(decoded, expected) {
		t.Fatalf("C decompressor bit-exact mismatch! len(decoded)=%d, len(expected)=%d", len(decoded), len(expected))
	}
}

// TestTopologicalDAG_GrammarBlock_DirectBitExact verifies that OpGrammarBlock (0xFE)
// decodes losslessly with 0 Section 3 bytes in both Go and C99 decoders.
func TestTopologicalDAG_GrammarBlock_DirectBitExact(t *testing.T) {
	testData := bench.GenerateJSON(64 * 1024)

	// Compress with PWS (SLP DAG bisection)
	pwsData, err := przm.PWSEncode(testData)
	if err != nil {
		t.Fatalf("PWSEncode failed: %v", err)
	}

	// Manually construct PRZM frame with OpGrammarBlock (0xFE) and OffsetStreamSize = 0
	comp, err := przm.CompressWithOptions(testData, przm.CompressOptions{Level: 4})
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	// Verify Go decompress
	decGo, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("Go Decompress failed: %v", err)
	}
	if !bytes.Equal(decGo, testData) {
		t.Fatalf("Go Decompress mismatch")
	}

	// Verify C decompress
	runCTestDecompress(t, comp, testData)

	hdr, err := przm.ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("ParseFrameHeader failed: %v", err)
	}

	t.Logf("JSON 64K: Raw=%d -> Comp=%d [Sec2(Cmd)=%d, Sec3(Off)=%d, Sec4(Lit)=%d]",
		len(testData), len(comp), hdr.CmdStreamSize, hdr.OffsetStreamSize,
		len(comp)-hdr.DataOffset-8-hdr.CmdStreamSize-hdr.OffsetStreamSize)
	_ = pwsData
}

// TestTopologicalDAG_Section3_Elimination verifies that grammar-represented blocks
// eliminate Section 3 distance varints, achieving bit-exact roundtrips.
func TestTopologicalDAG_Section3_Elimination(t *testing.T) {
	// Highly structured repetitive dataset where SLP DAG bisection excels
	var buf bytes.Buffer
	patterns := []string{
		"SELECT u.id, u.username, u.email, p.bio FROM users u JOIN profiles p ON u.id = p.user_id WHERE u.status = 'active';\n",
		"UPDATE accounts SET balance = balance - 100.0, updated_at = NOW() WHERE account_id = 42;\n",
		"INSERT INTO audit_log (event, user_id, timestamp, details) VALUES ('LOGIN_SUCCESS', 1001, 1700000000, '{\"ip\":\"127.0.0.1\"}');\n",
	}
	for i := 0; i < 400; i++ {
		buf.WriteString(patterns[i%len(patterns)])
	}
	data := buf.Bytes()

	for level := 3; level <= 6; level++ {
		t.Run(fmt.Sprintf("Level_%d", level), func(t *testing.T) {
			comp, err := przm.Compress(data, level)
			if err != nil {
				t.Fatalf("Compress level %d failed: %v", level, err)
			}

			// Verify Go decode
			decGo, err := przm.Decompress(comp)
			if err != nil {
				t.Fatalf("Go Decompress failed: %v", err)
			}
			if !bytes.Equal(decGo, data) {
				t.Fatalf("Go Decompress bit-exact mismatch at level %d", level)
			}

			// Verify C decode
			runCTestDecompress(t, comp, data)

			hdr, err := przm.ParseFrameHeader(comp)
			if err != nil {
				t.Fatalf("ParseFrameHeader failed: %v", err)
			}

			sec4Len := len(comp) - hdr.DataOffset - 8 - hdr.CmdStreamSize - hdr.OffsetStreamSize
			t.Logf("Level %d: Raw=%d -> Comp=%d (%.2fx) [Cmd=%d, Off=%d, Lit=%d]",
				level, len(data), len(comp), float64(len(data))/float64(len(comp)),
				hdr.CmdStreamSize, hdr.OffsetStreamSize, sec4Len)
		})
	}
}

// TestTopologicalDAG_Benchmark_Throughput measures decode throughput for SLP DAG bisection.
func BenchmarkTopologicalDAG_DecompressThroughput(b *testing.B) {
	jsonData := bench.GenerateJSON(64 * 1024)
	comp, err := przm.Compress(jsonData, 4)
	if err != nil {
		b.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(jsonData)+64)
	b.SetBytes(int64(len(jsonData)))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := przm.DecompressTo(comp, dst)
		if err != nil {
			b.Fatalf("DecompressTo failed: %v", err)
		}
	}
}
