// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/przm"
)

// generateProtobufPayload generates realistic serialized Protobuf messages.
// Schema:
// 1: int64 user_id (varint)
// 2: string username (length-delimited)
// 3: string email (length-delimited)
// 4: int32 status_enum (varint)
// 5: float32 latitude (fixed32)
// 6: float32 longitude (fixed32)
// 7: int64 created_at_timestamp (varint)
func generateProtobufPayload(numRecords int) []byte {
	var buf []byte
	usernames := []string{"admin", "service_worker_01", "node_cluster_alpha", "db_syncer", "metrics_exporter"}
	domains := []string{"example.internal", "cloud.corp.net", "k8s.local", "prod.infra.io"}

	for i := range numRecords {
		// Field 1: user_id (tag = (1 << 3) | 0 = 0x08)
		buf = appendVarint(buf, (1<<3)|0)
		buf = appendVarint(buf, uint64(100000+i))

		// Field 2: username (tag = (2 << 3) | 2 = 0x12)
		u := usernames[i%len(usernames)]
		buf = appendVarint(buf, (2<<3)|2)
		buf = appendVarint(buf, uint64(len(u)))
		buf = append(buf, u...)

		// Field 3: email (tag = (3 << 3) | 2 = 0x1A)
		email := fmt.Sprintf("%s@%s", u, domains[i%len(domains)])
		buf = appendVarint(buf, (3<<3)|2)
		buf = appendVarint(buf, uint64(len(email)))
		buf = append(buf, email...)

		// Field 4: status_enum (tag = (4 << 3) | 0 = 0x20)
		buf = appendVarint(buf, (4<<3)|0)
		buf = appendVarint(buf, uint64(i%4))

		// Field 5: latitude (tag = (5 << 3) | 5 = 0x2D)
		buf = appendVarint(buf, (5<<3)|5)
		var f5 [4]byte
		binary.LittleEndian.PutUint32(f5[:], 0x42480000) // 50.0
		buf = append(buf, f5[:]...)

		// Field 6: longitude (tag = (6 << 3) | 5 = 0x35)
		buf = appendVarint(buf, (6<<3)|5)
		var f6 [4]byte
		binary.LittleEndian.PutUint32(f6[:], 0x41700000) // 15.0
		buf = append(buf, f6[:]...)

		// Field 7: timestamp (tag = (7 << 3) | 0 = 0x38)
		buf = appendVarint(buf, (7<<3)|0)
		buf = appendVarint(buf, uint64(1700000000+i*10))
	}

	return buf
}

func appendVarint(buf []byte, v uint64) []byte {
	var b [10]byte
	n := binary.PutUvarint(b[:], v)
	return append(buf, b[:n]...)
}

func TestDependentSheaf_Detect(t *testing.T) {
	data := generateProtobufPayload(50)
	if !przm.DetectProtobufTLV(data) {
		t.Fatalf("DetectProtobufTLV failed to detect genuine Protobuf payload")
	}

	// Random noise should not be detected as Protobuf
	rng := rand.New(rand.NewSource(42))
	noise := make([]byte, 1024)
	rng.Read(noise)
	if przm.DetectProtobufTLV(noise) {
		t.Fatalf("DetectProtobufTLV false positive on random noise")
	}
}

func TestDependentSheaf_StratifyCollateBitExact(t *testing.T) {
	data := generateProtobufPayload(100)
	stratified := przm.StratifyDependentSheaf(data)
	if len(stratified) == 0 {
		t.Fatalf("StratifyDependentSheaf returned empty buffer")
	}

	dst := make([]byte, len(data))
	collated, err := przm.CollateDependentSheaf(stratified, dst)
	if err != nil {
		t.Fatalf("CollateDependentSheaf error: %v", err)
	}

	if !bytes.Equal(data, collated) {
		t.Fatalf("Bit-exact mismatch! Orig=%d, Recovered=%d", len(data), len(collated))
	}
}

func TestDependentSheaf_RoundtripBitExact(t *testing.T) {
	data := generateProtobufPayload(500)
	compressed := przm.CompressDependentSheaf(data)
	if len(compressed) == 0 {
		t.Fatalf("CompressDependentSheaf returned empty buffer")
	}

	ratio := float64(len(data)) / float64(len(compressed))
	savings := (1.0 - float64(len(compressed))/float64(len(data))) * 100.0
	t.Logf("Dependent Sheaf: Raw=%d B -> Comp=%d B (Ratio: %.2fx, Saved: +%.1f%%)",
		len(data), len(compressed), ratio, savings)

	decompressed, err := przm.DecompressDependentSheaf(compressed, nil)
	if err != nil {
		t.Fatalf("DecompressDependentSheaf error: %v", err)
	}

	if !bytes.Equal(data, decompressed) {
		t.Fatalf("Bit-exact mismatch! Orig=%d, Decompressed=%d", len(data), len(decompressed))
	}
}

func TestDependentSheaf_ZeroAllocCollate(t *testing.T) {
	data := generateProtobufPayload(200)
	stratified := przm.StratifyDependentSheaf(data)
	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	allocs := testing.AllocsPerRun(100, func() {
		res, err := przm.CollateDependentSheaf(stratified, dst)
		if err != nil || len(res) != len(data) {
			t.Fatalf("collation failed: %v", err)
		}
	})

	if allocs != 0 {
		t.Fatalf("CollateDependentSheaf allocated heap: got %.2f allocs/op, want 0.0", allocs)
	}
}

func TestPRZM_Compress_Protobuf(t *testing.T) {
	data := generateProtobufPayload(300)
	comp, err := przm.Compress(data, 3)
	if err != nil {
		t.Fatalf("przm.Compress error: %v", err)
	}

	ratio := float64(len(data)) / float64(len(comp))
	savings := (1.0 - float64(len(comp))/float64(len(data))) * 100.0
	t.Logf("PRZM Protobuf Pipeline: Raw=%d B -> Comp=%d B (Ratio: %.2fx, Saved: +%.1f%%)",
		len(data), len(comp), ratio, savings)

	decomp, err := przm.Decompress(comp)
	if err != nil {
		t.Fatalf("przm.Decompress error: %v", err)
	}

	if !bytes.Equal(data, decomp) {
		t.Fatalf("PRZM end-to-end roundtrip mismatch on Protobuf stream")
	}
}

func BenchmarkDependentSheaf_Collate(b *testing.B) {
	data := generateProtobufPayload(1000)
	stratified := przm.StratifyDependentSheaf(data)
	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = przm.CollateDependentSheaf(stratified, dst)
	}
}

func BenchmarkDependentSheaf_Decompress(b *testing.B) {
	data := generateProtobufPayload(1000)
	comp := przm.CompressDependentSheaf(data)
	dst := bufkit.AlignedBytes(len(data)+64, bufkit.CacheLineSize)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = przm.DecompressDependentSheaf(comp, dst)
	}
}

func BenchmarkDependentSheaf_Compress(b *testing.B) {
	data := generateProtobufPayload(1000)

	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = przm.CompressDependentSheaf(data)
	}
}
