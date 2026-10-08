// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"testing"

	"github.com/lemon4ksan/przm"
	"github.com/lemon4ksan/przm/bench"
)

func BenchmarkPRZM_StructuredJSON(b *testing.B) {
	data := bench.GenerateJSON(64 * 1024)

	b.Run("Compress64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Compress(data, 3)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	comp, _ := przm.Compress(data, 3)
	b.Run("Decompress64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Decompress(comp)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	data256 := bench.GenerateJSON(256 * 1024)
	comp256, _ := przm.Compress(data256, 3)
	b.Run("Decompress256K", func(b *testing.B) {
		b.SetBytes(int64(len(data256)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Decompress(comp256)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPRZM_TabularStride(b *testing.B) {
	data := bench.GenerateTabular(64*1024, 32)

	b.Run("Compress64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Compress(data, 3)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	comp, _ := przm.Compress(data, 3)
	b.Run("Decompress64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Decompress(comp)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkPRZM_MachineCode(b *testing.B) {
	data := bench.GenerateMachineCode(64 * 1024)

	b.Run("Compress64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Compress(data, 3)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	comp, _ := przm.Compress(data, 3)
	b.Run("Decompress64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.Decompress(comp)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkHuffman(b *testing.B) {
	data := bench.GenerateJSON(64 * 1024)

	b.Run("Encode64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.EncodeHuffman4X(data)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	packed, err := przm.EncodeHuffman4X(data)
	if err != nil {
		b.Fatal(err)
	}
	dst := make([]byte, len(data))
	var table przm.CanonicalHuffmanTable

	b.Run("Decode64K", func(b *testing.B) {
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := przm.DecodeHuffman4X(dst, packed, &table)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

