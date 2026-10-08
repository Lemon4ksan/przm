// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/foundation/bufkit"
)

func TestStream_HeaderParsing(t *testing.T) {
	validHdr := make([]byte, StreamHeaderSize)
	binary.LittleEndian.PutUint32(validHdr[0:4], MagicPRZS)
	validHdr[4] = StreamFlagIndependentChunks
	binary.LittleEndian.PutUint64(validHdr[5:13], 1048576)
	binary.LittleEndian.PutUint32(validHdr[13:17], 4)

	hdr, err := ParseStreamHeader(validHdr)
	if err != nil {
		t.Fatalf("ParseStreamHeader failed on valid header: %v", err)
	}
	if hdr.Flags != StreamFlagIndependentChunks {
		t.Errorf("expected flags 0x%02X, got 0x%02X", StreamFlagIndependentChunks, hdr.Flags)
	}
	if hdr.TotalUncompressedSize != 1048576 {
		t.Errorf("expected size 1048576, got %d", hdr.TotalUncompressedSize)
	}
	if hdr.ChunkCount != 4 {
		t.Errorf("expected chunk count 4, got %d", hdr.ChunkCount)
	}

	uSize, err := GetUncompressedSize(validHdr)
	if err != nil {
		t.Fatalf("GetUncompressedSize failed: %v", err)
	}
	if uSize != 1048576 {
		t.Errorf("expected GetUncompressedSize 1048576, got %d", uSize)
	}

	// Truncated header
	for l := range StreamHeaderSize {
		_, err := ParseStreamHeader(validHdr[:l])
		if !errors.Is(err, ErrCorruptedFrame) {
			t.Errorf("expected ErrCorruptedFrame for length %d, got %v", l, err)
		}
	}

	// Invalid magic
	badMagicHdr := make([]byte, StreamHeaderSize)
	copy(badMagicHdr, validHdr)
	binary.LittleEndian.PutUint32(badMagicHdr[0:4], 0x12345678)
	_, err = ParseStreamHeader(badMagicHdr)
	if !errors.Is(err, ErrInvalidMagic) {
		t.Errorf("expected ErrInvalidMagic for bad magic, got %v", err)
	}
}

func TestStream_RoundtripMultiChunk(t *testing.T) {
	testCases := []struct {
		name       string
		chunkSizes []int
		generator  func(size int, seed int) []byte
	}{
		{
			name:       "SingleChunk_Text",
			chunkSizes: []int{8192},
			generator: func(size int, seed int) []byte {
				buf := make([]byte, size)
				phrase := []byte("The quick brown fox jumps over the lazy dog. 1234567890! ")
				for i := range buf {
					buf[i] = phrase[(i+seed)%len(phrase)]
				}
				return buf
			},
		},
		{
			name:       "TwoChunks_RepetitiveJSON",
			chunkSizes: []int{16384, 16384},
			generator: func(size int, seed int) []byte {
				var b bytes.Buffer
				for i := 0; b.Len() < size; i++ {
					fmt.Fprintf(&b, `{"id":%d,"metric":"cpu_utilization","val":%d,"active":true}`, i+seed, (i*37)%100)
				}
				return b.Bytes()[:size]
			},
		},
		{
			name:       "FourChunks_BinaryPatterns",
			chunkSizes: []int{32768, 65536, 16384, 32768},
			generator: func(size int, seed int) []byte {
				buf := make([]byte, size)
				r := rand.New(rand.NewSource(int64(seed)))
				pattern := make([]byte, 128)
				r.Read(pattern)
				for i := range buf {
					buf[i] = pattern[(i)%len(pattern)]
				}
				return buf
			},
		},
		{
			name:       "EightChunks_MixedData",
			chunkSizes: []int{10000, 20000, 30000, 15000, 25000, 40000, 12000, 8000},
			generator: func(size int, seed int) []byte {
				buf := make([]byte, size)
				for i := range buf {
					if i%3 == 0 {
						buf[i] = byte(i & 0xFF)
					} else {
						buf[i] = byte((i * 13) ^ seed)
					}
				}
				return buf
			},
		},
		{
			name:       "ZeroByteStream",
			chunkSizes: []int{},
			generator: func(size int, seed int) []byte {
				return []byte{}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var fullOriginal []byte
			var chunks [][]byte

			for idx, cSize := range tc.chunkSizes {
				cData := tc.generator(cSize, idx*97)
				fullOriginal = append(fullOriginal, cData...)

				cComp, err := Compress(cData, 3)
				if err != nil {
					t.Fatalf("Compress failed for chunk %d: %v", idx, err)
				}
				chunks = append(chunks, cComp)
			}

			container := BuildStreamContainer(chunks, uint64(len(fullOriginal)))

			// 1. Verify header inspection
			hdr, err := ParseStreamHeader(container)
			if err != nil {
				t.Fatalf("ParseStreamHeader failed on built container: %v", err)
			}
			if hdr.TotalUncompressedSize != uint64(len(fullOriginal)) {
				t.Fatalf("size mismatch in header: got %d, want %d", hdr.TotalUncompressedSize, len(fullOriginal))
			}
			if int(hdr.ChunkCount) != len(tc.chunkSizes) {
				t.Fatalf("chunk count mismatch: got %d, want %d", hdr.ChunkCount, len(tc.chunkSizes))
			}

			uSize, err := GetUncompressedSize(container)
			if err != nil {
				t.Fatalf("GetUncompressedSize failed: %v", err)
			}
			if uSize != int64(len(fullOriginal)) {
				t.Fatalf("GetUncompressedSize mismatch: got %d, want %d", uSize, len(fullOriginal))
			}

			// 2. Decompress
			dec, err := Decompress(container)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(dec, fullOriginal) {
				t.Fatalf("Decompress output mismatch (got %d bytes, want %d bytes)", len(dec), len(fullOriginal))
			}

			// 3. DecompressTo with pre-allocated buffer
			dstBuf := bufkit.AlignedBytes(len(fullOriginal)+64, bufkit.CacheLineSize)
			decTo, err := DecompressTo(container, dstBuf)
			if err != nil {
				t.Fatalf("DecompressTo failed: %v", err)
			}
			if !bytes.Equal(decTo, fullOriginal) {
				t.Fatalf("DecompressTo output mismatch (got %d bytes, want %d bytes)", len(decTo), len(fullOriginal))
			}
		})
	}
}

func TestStream_ZeroAllocDecompression(t *testing.T) {
	chunkConfigs := []struct {
		name       string
		chunkSizes []int
	}{
		{
			name:       "1_Chunk_64K",
			chunkSizes: []int{65536},
		},
		{
			name:       "4_Chunks_32K",
			chunkSizes: []int{32768, 32768, 32768, 32768},
		},
		{
			name:       "8_Chunks_Uneven",
			chunkSizes: []int{8192, 16384, 32768, 4096, 12288, 20480, 24576, 16384},
		},
	}

	for _, cfg := range chunkConfigs {
		t.Run(cfg.name, func(t *testing.T) {
			var fullOriginal []byte
			var chunks [][]byte

			for idx, cSize := range cfg.chunkSizes {
				cData := make([]byte, cSize)
				for i := range cData {
					cData[i] = byte((i*17 + idx*31) & 0xFF)
				}
				fullOriginal = append(fullOriginal, cData...)

				cComp, err := Compress(cData, 2)
				if err != nil {
					t.Fatalf("Compress failed: %v", err)
				}
				chunks = append(chunks, cComp)
			}

			container := BuildStreamContainer(chunks, uint64(len(fullOriginal)))

			// Pre-allocate buffer according to zero-allocation contract
			dst := bufkit.AlignedBytes(len(fullOriginal)+64, bufkit.CacheLineSize)

			// Warmup run
			warmup, err := DecompressTo(container, dst)
			if err != nil {
				t.Fatalf("Warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(warmup, fullOriginal) {
				t.Fatalf("Warmup output mismatch")
			}

			// Strictly verify testing.AllocsPerRun(100, ...) is strictly 0.00
			allocs := testing.AllocsPerRun(100, func() {
				out, err := DecompressTo(container, dst)
				if err != nil {
					t.Fatalf("DecompressTo in AllocsPerRun failed: %v", err)
				}
				_ = out
			})

			if allocs != 0.0 {
				t.Fatalf("ZERO-ALLOC VIOLATION: DecompressTo produced %.2f allocs/run (mandated strictly 0.00)", allocs)
			}

			t.Logf("[%s] AllocsPerRun(100) = %.2f (strictly 0.00 zero-heap invariant verified)", cfg.name, allocs)
		})
	}
}

func TestStream_AdversarialCorruption(t *testing.T) {
	// Construct a reference valid 2-chunk stream
	c1Data := []byte("Chunk 1 test payload with some compressible repetition repetition repetition")
	c2Data := []byte("Chunk 2 test payload with different data and patterns 1234567890 1234567890")
	full := append(append([]byte{}, c1Data...), c2Data...)

	comp1, err := Compress(c1Data, 1)
	if err != nil {
		t.Fatal(err)
	}
	comp2, err := Compress(c2Data, 1)
	if err != nil {
		t.Fatal(err)
	}

	validStream := BuildStreamContainer([][]byte{comp1, comp2}, uint64(len(full)))

	dst := make([]byte, len(full), len(full)+64)

	// 1. Truncated container (smaller than stream header)
	_, err = Decompress(validStream[:10])
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected ErrCorruptedFrame for truncated header, got: %v", err)
	}

	// 2. Truncated at first chunk length prefix
	_, err = Decompress(validStream[:19])
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected ErrCorruptedFrame for truncated chunk prefix, got: %v", err)
	}

	// 3. Truncated inside first chunk payload
	_, err = Decompress(validStream[:25])
	if err == nil {
		t.Fatalf("expected error for truncated chunk payload")
	}

	// 4. Chunk count exceeds available data
	badCountStream := make([]byte, len(validStream))
	copy(badCountStream, validStream)
	binary.LittleEndian.PutUint32(badCountStream[13:17], 100) // claim 100 chunks instead of 2
	_, err = Decompress(badCountStream)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected ErrCorruptedFrame for excessive chunk count, got: %v", err)
	}

	// 5. Total uncompressed size mismatch (claims more than actual)
	badSizeStream := make([]byte, len(validStream))
	copy(badSizeStream, validStream)
	binary.LittleEndian.PutUint64(badSizeStream[5:13], uint64(len(full)+1000))
	_, err = DecompressTo(badSizeStream, dst)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected ErrCorruptedFrame for total size mismatch, got: %v", err)
	}

	// 6. Total uncompressed size exceeds chunkCount * MaxBlockSize
	oversizedStream := make([]byte, len(validStream))
	copy(oversizedStream, validStream)
	binary.LittleEndian.PutUint64(oversizedStream[5:13], uint64(100*MaxBlockSize))
	_, err = DecompressTo(oversizedStream, dst)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected ErrCorruptedFrame for oversized uncompressed size, got: %v", err)
	}

	// 7. Chunk compressed size is corrupted to exceed stream length
	badChunkLenStream := make([]byte, len(validStream))
	copy(badChunkLenStream, validStream)
	binary.LittleEndian.PutUint32(badChunkLenStream[StreamHeaderSize:StreamHeaderSize+4], 0x7FFFFFFF)
	_, err = DecompressTo(badChunkLenStream, dst)
	if !errors.Is(err, ErrCorruptedFrame) {
		t.Fatalf("expected ErrCorruptedFrame for corrupted chunk comp size, got: %v", err)
	}
}

func TestStream_MultithreadedCompression(t *testing.T) {
	const payloadSize = 512 * 1024 // 512 KiB (> 256 KiB)
	const chunkSize = 64 * 1024    // 64 KiB chunks -> 8 chunks

	generators := []struct {
		name string
		gen  func() []byte
	}{
		{
			name: "ASCIIText",
			gen: func() []byte {
				buf := make([]byte, payloadSize)
				phrase := []byte("PRZM Multithreaded Compression Pipeline Core Invariant Verification Test 2026. ")
				for i := range buf {
					buf[i] = phrase[i%len(phrase)]
				}
				return buf
			},
		},
		{
			name: "RepetitiveJSON",
			gen: func() []byte {
				var b bytes.Buffer
				for i := 0; b.Len() < payloadSize; i++ {
					fmt.Fprintf(&b, `{"id":%d,"metric":"cpu_utilization","val":%d,"active":true}`, i, (i*37)%100)
				}
				return b.Bytes()[:payloadSize]
			},
		},
		{
			name: "BinaryPatterns",
			gen: func() []byte {
				buf := make([]byte, payloadSize)
				pattern := make([]byte, 256)
				for i := range pattern {
					pattern[i] = byte(i ^ 0xAA)
				}
				for i := range buf {
					buf[i] = pattern[(i+i/256)%len(pattern)]
				}
				return buf
			},
		},
		{
			name: "HighEntropy",
			gen: func() []byte {
				buf := make([]byte, payloadSize)
				r := rand.New(rand.NewSource(424242))
				r.Read(buf)
				return buf
			},
		},
	}

	for _, tc := range generators {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.gen()

			// 1. Parallel compression with Threads = 4, ChunkSize = 64 KiB
			opts := CompressOptions{
				Threads:   4,
				ChunkSize: chunkSize,
				Level:     3,
			}
			comp, err := CompressWithOptions(payload, opts)
			if err != nil {
				t.Fatalf("CompressWithOptions failed: %v", err)
			}

			// Verify PRZS magic container
			if len(comp) < StreamHeaderSize {
				t.Fatalf("compressed stream too short: %d bytes", len(comp))
			}
			magic := binary.LittleEndian.Uint32(comp[0:4])
			if magic != MagicPRZS {
				t.Fatalf("expected MagicPRZS (0x%08X), got 0x%08X", MagicPRZS, magic)
			}

			hdr, err := ParseStreamHeader(comp)
			if err != nil {
				t.Fatalf("ParseStreamHeader failed: %v", err)
			}
			expectedChunks := uint32((len(payload) + chunkSize - 1) / chunkSize)
			if hdr.ChunkCount != expectedChunks {
				t.Fatalf("expected %d chunks, got %d", expectedChunks, hdr.ChunkCount)
			}
			if hdr.TotalUncompressedSize != uint64(len(payload)) {
				t.Fatalf("expected total uncompressed size %d, got %d", len(payload), hdr.TotalUncompressedSize)
			}

			// 2. Verify 100% bit-exact lossless roundtrip with Decompress
			dec, err := Decompress(comp)
			if err != nil {
				t.Fatalf("Decompress failed: %v", err)
			}
			if !bytes.Equal(dec, payload) {
				t.Fatalf("Decompress output mismatch (got %d bytes, want %d bytes)", len(dec), len(payload))
			}

			// 3. Verify DecompressTo achieves strictly 0.00 dynamic heap allocations
			dst := bufkit.AlignedBytes(len(payload)+64, bufkit.CacheLineSize)
			warmup, err := DecompressTo(comp, dst)
			if err != nil {
				t.Fatalf("Warmup DecompressTo failed: %v", err)
			}
			if !bytes.Equal(warmup, payload) {
				t.Fatalf("Warmup DecompressTo output mismatch")
			}

			if !RaceEnabled {
				allocs := testing.AllocsPerRun(100, func() {
					out, err := DecompressTo(comp, dst)
					if err != nil {
						t.Fatalf("DecompressTo in AllocsPerRun failed: %v", err)
					}
					_ = out
				})

				if allocs != 0.0 {
					t.Fatalf("ZERO-ALLOC VIOLATION on %s: DecompressTo produced %.2f allocs/run (mandated strictly 0.00)", tc.name, allocs)
				}
				t.Logf("[%s] MT(threads=4, chunk=64K) AllocsPerRun(100) = %.2f (0.00 verified)", tc.name, allocs)
			}
		})
	}

	// 4. Test Threads = 1 vs Threads = 8
	t.Run("Threads1_vs_Threads8", func(t *testing.T) {
		payload := generators[0].gen() // ASCIIText 512 KiB

		// Threads = 1 (single-threaded: when payload <= MaxBlockSize, produces single standard frame)
		opts1 := CompressOptions{Threads: 1, ChunkSize: chunkSize, Level: 3}
		comp1, err := CompressWithOptions(payload, opts1)
		if err != nil {
			t.Fatalf("CompressWithOptions(threads=1) failed: %v", err)
		}
		if binary.LittleEndian.Uint32(comp1[0:4]) != MagicPRZM {
			t.Fatalf("expected single frame MagicPRZM for Threads=1 within MaxBlockSize, got 0x%08X", binary.LittleEndian.Uint32(comp1[0:4]))
		}
		dec1, err := Decompress(comp1)
		if err != nil {
			t.Fatalf("Decompress(comp1) failed: %v", err)
		}
		if !bytes.Equal(dec1, payload) {
			t.Fatalf("Threads=1 roundtrip mismatch")
		}

		// Also test sequential compressMultiChunk with Threads = 1
		compSeq, err := compressMultiChunk(payload, opts1)
		if err != nil {
			t.Fatalf("compressMultiChunk(threads=1) failed: %v", err)
		}
		if binary.LittleEndian.Uint32(compSeq[0:4]) != MagicPRZS {
			t.Fatalf("expected MagicPRZS for compressMultiChunk(threads=1), got 0x%08X", binary.LittleEndian.Uint32(compSeq[0:4]))
		}
		decSeq, err := Decompress(compSeq)
		if err != nil {
			t.Fatalf("Decompress(compSeq) failed: %v", err)
		}
		if !bytes.Equal(decSeq, payload) {
			t.Fatalf("compressMultiChunk(threads=1) roundtrip mismatch")
		}

		// Threads = 8 (parallel multi-chunk: produces MagicPRZS stream)
		opts8 := CompressOptions{Threads: 8, ChunkSize: chunkSize, Level: 3}
		comp8, err := CompressWithOptions(payload, opts8)
		if err != nil {
			t.Fatalf("CompressWithOptions(threads=8) failed: %v", err)
		}
		if binary.LittleEndian.Uint32(comp8[0:4]) != MagicPRZS {
			t.Fatalf("expected MagicPRZS for Threads=8, got 0x%08X", binary.LittleEndian.Uint32(comp8[0:4]))
		}
		dec8, err := Decompress(comp8)
		if err != nil {
			t.Fatalf("Decompress(comp8) failed: %v", err)
		}
		if !bytes.Equal(dec8, payload) {
			t.Fatalf("Threads=8 roundtrip mismatch")
		}
	})

	// 5. Test boundary condition: payload > MaxBlockSize with opts.StandardStreamOnly = true returns ErrExceedsMaxBlock
	t.Run("StandardStreamOnly_ExceedsMaxBlock", func(t *testing.T) {
		oversized := make([]byte, MaxBlockSize+1)
		_, err := CompressWithOptions(oversized, CompressOptions{StandardStreamOnly: true})
		if !errors.Is(err, ErrExceedsMaxBlock) {
			t.Fatalf("expected ErrExceedsMaxBlock for StandardStreamOnly with size %d, got: %v", len(oversized), err)
		}
	})

	// 6. Test single chunk (size <= chunkSize) produces single standard frame
	t.Run("SingleChunkProducesStandardFrame", func(t *testing.T) {
		smallPayload := make([]byte, 32*1024) // 32 KiB <= 64 KiB chunkSize
		for i := range smallPayload {
			smallPayload[i] = byte(i * 13)
		}
		opts := CompressOptions{Threads: 4, ChunkSize: 64 * 1024, Level: 3}
		comp, err := CompressWithOptions(smallPayload, opts)
		if err != nil {
			t.Fatalf("CompressWithOptions single chunk failed: %v", err)
		}
		magic := binary.LittleEndian.Uint32(comp[0:4])
		if magic != MagicPRZM {
			t.Fatalf("expected single standard frame MagicPRZM (0x%08X), got 0x%08X", MagicPRZM, magic)
		}
		dec, err := Decompress(comp)
		if err != nil {
			t.Fatalf("Decompress single standard frame failed: %v", err)
		}
		if !bytes.Equal(dec, smallPayload) {
			t.Fatalf("Single chunk roundtrip mismatch")
		}
	})

	// 7. Test ChunkSize > MaxBlockSize returns ErrExceedsMaxBlock
	t.Run("ChunkSize_ExceedsMaxBlock", func(t *testing.T) {
		dummy := make([]byte, 1024)
		_, err := compressMultiChunk(dummy, CompressOptions{Threads: 4, ChunkSize: MaxBlockSize + 1})
		if !errors.Is(err, ErrExceedsMaxBlock) {
			t.Fatalf("expected ErrExceedsMaxBlock for ChunkSize > MaxBlockSize, got: %v", err)
		}

		// Also verify public CompressWithOptions rejects ChunkSize > MaxBlockSize on small inputs
		for _, size := range []int{1024, 64 * 1024} {
			smallPayload := make([]byte, size)
			for _, th := range []int{1, 4} {
				_, err := CompressWithOptions(smallPayload, CompressOptions{Threads: th, ChunkSize: MaxBlockSize + 1})
				if !errors.Is(err, ErrExceedsMaxBlock) {
					t.Fatalf("CompressWithOptions(size=%d, threads=%d) expected ErrExceedsMaxBlock, got: %v", size, th, err)
				}
			}
		}
	})
}
