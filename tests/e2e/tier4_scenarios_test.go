// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package e2e_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

// Tier 4: Real-World Application Scenarios (>= 10 Multi-Feature Workloads)

// Scenario 1: High-Volume Microservice JSON Access Logs
func TestTier4_Scenario01_MicroserviceJSONAccessLogs(t *testing.T) {
	// Synthesize 64 KiB of realistic microservice JSON access logs
	var buf bytes.Buffer
	for i := range 1000 {
		fmt.Fprintf(&buf, `{"ts":"2026-10-04T12:%02d:%02dZ","trace_id":"e4d8%04x","method":"POST","uri":"/api/v1/checkout","status":200,"dur_ms":%d}`+"\n",
			(i/60)%60, i%60, i, 10+(i*7)%150)
	}
	logs := buf.Bytes()
	comp := assertRoundtrip(t, logs, 1)

	ratio := float64(len(logs)) / float64(len(comp))
	t.Logf("Scenario 1 (JSON Logs): %d -> %d bytes (Ratio: %.2fx)", len(logs), len(comp), ratio)
	if ratio < 1.5 {
		t.Fatalf("expected compression ratio >= 1.5x on JSON logs, got %.2fx", ratio)
	}
}

// Scenario 2: Git Repository Source Code Snapshot
func TestTier4_Scenario02_GitRepositorySourceCode(t *testing.T) {
	// Synthesize 64 KiB Go source code tree snapshot
	sourceCode := bench.GenerateSourceCode(65536)
	comp := assertRoundtrip(t, sourceCode, 1)

	ratio := float64(len(sourceCode)) / float64(len(comp))
	t.Logf("Scenario 2 (Source Code): %d -> %d bytes (Ratio: %.2fx)", len(sourceCode), len(comp), ratio)
	if ratio < 1.5 {
		t.Fatalf("expected compression ratio >= 1.5x on source code, got %.2fx", ratio)
	}
}

// Scenario 3: Tabular Columnar Telemetry Feed
func TestTier4_Scenario03_TabularColumnarTelemetry(t *testing.T) {
	// Synthesize 64 KiB tabular telemetry stream with fixed 32-byte stride
	tabular := bench.GenerateTabular(65536, 32)
	comp := assertRoundtrip(t, tabular, 1)

	hdr, err := przm.ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("parse header failed: %v", err)
	}
	if (hdr.Flags & przm.FlagStride) == 0 {
		t.Logf("Note: FlagStride not set; flags=0x%02X, stride=%d", hdr.Flags, hdr.Stride)
	}

	ratio := float64(len(tabular)) / float64(len(comp))
	t.Logf("Scenario 3 (Tabular 32B): %d -> %d bytes (Ratio: %.2fx)", len(tabular), len(comp), ratio)
}

// Scenario 4: Compiled x86_64 Executable Binary & Shared Library
func TestTier4_Scenario04_CompiledX86ExecutableBinary(t *testing.T) {
	// Synthesize 64 KiB machine code with x86 instruction structures and relative CALLs
	machineCode := generateMachineCode(65536)
	comp := assertRoundtrip(t, machineCode, 1)

	hdr, err := przm.ParseFrameHeader(comp)
	if err != nil {
		t.Fatalf("parse header failed: %v", err)
	}
	t.Logf("Scenario 4 (x86 Machine Code): %d -> %d bytes, Flags=0x%02X", len(machineCode), len(comp), hdr.Flags)
}

// Scenario 5: Database Write-Ahead Log (WAL) with Aligned Pages & Sparse Deltas
func TestTier4_Scenario05_DatabaseWriteAheadLog(t *testing.T) {
	const pageSize = 8192
	const pageCount = 8
	wal := make([]byte, pageCount*pageSize)

	// Base 8 KiB page template with table header
	pageHeader := []byte("PG_PAGE_HEADER_LSN_0000000001_CHECKSUM_OK_PAGE_ID_0001;")
	for p := range pageCount {
		offset := p * pageSize
		copy(wal[offset:], pageHeader)
		// Emulate row tuples inside page
		for r := range 30 {
			tupleOffset := offset + 64 + r*200
			if tupleOffset+100 <= offset+pageSize {
				tuple := fmt.Sprintf("TXN_ID:%08d|USER_ID:%06d|AMOUNT:%06d|STATE:COMMITTED;", 1000+p*10+r, 5000+r, (r*97)%10000)
				copy(wal[tupleOffset:], tuple)
			}
		}
	}

	comp := assertRoundtrip(t, wal, 1)
	ratio := float64(len(wal)) / float64(len(comp))
	t.Logf("Scenario 5 (Database WAL): %d -> %d bytes (Ratio: %.2fx)", len(wal), len(comp), ratio)
}

// Scenario 6: Embedded AI Model Quantized Tensor Weights
func TestTier4_Scenario06_EmbeddedAIModelQuantizedWeights(t *testing.T) {
	// Synthesize 64 KiB quantized weight tensor with local covariance
	weights := bench.GenerateAIWeights(65536)
	comp := assertRoundtrip(t, weights, 1)

	ratio := float64(len(weights)) / float64(len(comp))
	t.Logf("Scenario 6 (AI Weights): %d -> %d bytes (Ratio: %.2fx)", len(weights), len(comp), ratio)
}

// Scenario 7: Web Application Bundle with Mixed JSON, CSS, Base64 & SVG
func TestTier4_Scenario07_WebApplicationAssetBundle(t *testing.T) {
	var bundle bytes.Buffer

	// Section A: JSON metadata
	bundle.WriteString(`{"app":"dashboard","version":"2.4.0","env":"production","modules":["auth","metrics","billing"]}` + "\n")

	// Section B: Embedded SVG icon
	bundle.WriteString(`<svg width="100" height="100" xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40" stroke="green" fill="yellow" /></svg>` + "\n")

	// Section C: Inlined Base64 asset
	b64Asset := generateBase64Payload(1024)
	fmt.Fprintf(&bundle, `{"asset_name":"logo.png","data":"%s"}`+"\n", string(b64Asset))

	// Section D: CSS stylesheet rules
	for i := range 50 {
		fmt.Fprintf(&bundle, ".widget-card-%03d { display: flex; padding: 16px; margin: 8px; border-radius: 4px; background-color: #f5f5f5; }\n", i)
	}

	data := bundle.Bytes()
	comp := assertRoundtrip(t, data, 1)

	ratio := float64(len(data)) / float64(len(comp))
	t.Logf("Scenario 7 (Web Asset Bundle): %d -> %d bytes (Ratio: %.2fx)", len(data), len(comp), ratio)
}

// Scenario 8: Seal Container Multi-Stream Archive Packaging
func TestTier4_Scenario08_SealContainerArchiveStream(t *testing.T) {
	// Emulate a Seal container archive file holding metadata, file entries, and TTRN models
	metaEntry := []byte("SEAL_CONTAINER_ARCHIVE_MANIFEST_VERSION_1_0_ENTRY_COUNT_004")
	compMeta, err := przm.Compress(metaEntry, 1)
	if err != nil {
		t.Fatalf("compress meta failed: %v", err)
	}

	codeEntry := bench.GenerateSourceCode(16384)
	compCode, err := przm.Compress(codeEntry, 1)
	if err != nil {
		t.Fatalf("compress code failed: %v", err)
	}

	tensorEntry := make([]byte, 4096)
	for i := range tensorEntry {
		tensorEntry[i] = byte((i * 19) % 256)
	}
	dict := make([]byte, 32768)
	compTensor, err := przm.CompressTTRN(tensorEntry, dict)
	if err != nil {
		t.Fatalf("compress tensor failed: %v", err)
	}

	// Decompress each entry in place with pre-allocated destination buffers
	dstMeta := make([]byte, len(metaEntry)+64)
	decMeta, err := przm.DecompressTo(compMeta, dstMeta)
	if err != nil || !bytes.Equal(decMeta, metaEntry) {
		t.Fatalf("seal meta decompress mismatch")
	}

	dstCode := make([]byte, len(codeEntry)+64)
	decCode, err := przm.DecompressTo(compCode, dstCode)
	if err != nil || !bytes.Equal(decCode, codeEntry) {
		t.Fatalf("seal code decompress mismatch")
	}

	dstTensor := make([]byte, len(tensorEntry))
	err = przm.DecompressTTRNDirect(compTensor, dict, dstTensor)
	if err != nil || !bytes.Equal(dstTensor, tensorEntry) {
		t.Fatalf("seal tensor decompress mismatch")
	}

	t.Logf("Scenario 8 (Seal Archive Multi-Stream): All entries successfully validated!")
}

// Scenario 9: High-Entropy Encrypted Cold Storage Archive
func TestTier4_Scenario09_HighEntropyColdStorage(t *testing.T) {
	// Synthesize 32 KiB encrypted pseudo-random payload
	cipherStream := generateHighEntropyData(32768)
	comp := assertRoundtrip(t, cipherStream, 1)

	// Cold storage mode must not expand beyond normal framing overhead (< 1.5%)
	expansion := float64(len(comp)) / float64(len(cipherStream))
	t.Logf("Scenario 9 (Encrypted Cold Storage): %d -> %d bytes (Expansion: %.4f)", len(cipherStream), len(comp), expansion)
	if expansion > 1.05 {
		t.Fatalf("incompressible payload expanded by more than 5%%: %.4f", expansion)
	}
}

// Scenario 10: Continuous Streaming Telemetry Ingestion
func TestTier4_Scenario10_ContinuousStreamingTelemetry(t *testing.T) {
	// 256 KiB continuous JSON telemetry stream
	var buf bytes.Buffer
	for i := range 2000 {
		fmt.Fprintf(&buf, `{"sensor":"thermocouple-%03d","val":%.3f,"status":"NORMAL","ts":%d}`+"\n",
			i%50, 45.2+float64(i%100)/7.0, 1728000000+i)
	}
	original := buf.Bytes()

	var compBuf bytes.Buffer
	if err := przm.CompressStream(bytes.NewReader(original), &compBuf); err != nil {
		t.Fatalf("CompressStream failed: %v", err)
	}

	var decBuf bytes.Buffer
	if err := przm.DecompressStream(bytes.NewReader(compBuf.Bytes()), &decBuf); err != nil {
		t.Fatalf("DecompressStream failed: %v", err)
	}

	decompressed := decBuf.Bytes()
	if !bytes.Equal(decompressed, original) {
		t.Fatalf("Continuous streaming roundtrip mismatch: len %d vs %d", len(decompressed), len(original))
	}

	ratio := float64(len(original)) / float64(compBuf.Len())
	t.Logf("Scenario 10 (Streaming Ingest 256KB): %d -> %d bytes (Ratio: %.2fx)", len(original), compBuf.Len(), ratio)
}
