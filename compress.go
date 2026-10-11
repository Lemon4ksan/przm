// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
	"github.com/lemon4ksan/foundation/silicon/simd"
	"github.com/lemon4ksan/przm/gpu"
)

const (
	hashTableSize = 65536
	hashTableMask = hashTableSize - 1
	minMatch      = 4
	maxLiteralRun = 64
	initPadSize   = 1024 * 1024
)

func ensureBufCap(b []byte, n int) []byte {
	if cap(b) < n {
		return bufkit.AlignedBytes(n, bufkit.CacheLineSize)[:0]
	}
	return b[:0]
}

type scratchpad struct {
	head         []int32
	hensel       HenselScratchpad
	cmdBuf       []byte
	offsetBuf    []byte
	literalBuf   []byte
	transBuf     []byte
	nibbleBuf    []byte
	nibbleLowBuf []byte
	splitBuf     []byte
	normBuf      []byte
	dedup        DedupTable
	sheafSBuf    []byte
	sheafCBuf    []byte
	sheafMaskBuf []byte
	sheafWorkBuf []byte
}

var padPool = generic.NewPool(func() *scratchpad {
	return &scratchpad{
		head:         make([]int32, hashTableSize*4),
		cmdBuf:       bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		offsetBuf:    bufkit.AlignedBytes(initPadSize/2, bufkit.CacheLineSize)[:0],
		literalBuf:   bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		transBuf:     bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		nibbleBuf:    bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		nibbleLowBuf: bufkit.AlignedBytes(initPadSize/2, bufkit.CacheLineSize)[:0],
		splitBuf:     bufkit.AlignedBytes(initPadSize*2, bufkit.CacheLineSize)[:0],
		normBuf:      bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		sheafSBuf:    bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		sheafCBuf:    bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		sheafMaskBuf: bufkit.AlignedBytes(initPadSize/8+128, bufkit.CacheLineSize)[:0],
		sheafWorkBuf: bufkit.AlignedBytes(initPadSize+1024, bufkit.CacheLineSize)[:0],
	}
})

type matchCandidate struct {
	mType      int // 1: Repeat, 2: Stride, 3: Ring8Ext, 4: ExplicitExt, 5: DeltaMaskExpl, 6: DeltaMaskRing8, 7: ShortDict, 8: ExtDict
	length     int
	distance   int
	ringK      int
	strideM    int
	dictTok    int
	cost       int
	score      int
	mismatches int
	diffOffs   [2]int
	diffXORs   [2]byte
}

func uvarintLen(x uint64) int {
	return (bits.Len64(x|1) + 6) / 7
}

// packNibblePlanes splits a literal byte slice into a Low-Nibble plane (packed 2 per byte)
// and a High-Nibble plane (runs of identical high nibbles).
// Returns the packed stream and true if packed size is at least 8% smaller than raw lits.
func packNibblePlanes(lits []byte, out []byte, lowBuf []byte) ([]byte, bool) {
	n := len(lits)
	if n < 32 {
		return nil, false
	}

	// 1. Pack Low-Nibble Plane (exactly ceil(n/2) bytes)
	numPairs := (n + 1) / 2
	if cap(lowBuf) < numPairs {
		lowBuf = make([]byte, numPairs)
	} else {
		lowBuf = lowBuf[:numPairs]
	}

	j := 0
	for ; j+4 <= numPairs && 2*j+7 < n; j += 4 {
		idx := j * 2
		b0 := lits[idx] & 0x0F
		b1 := lits[idx+1] & 0x0F
		b2 := lits[idx+2] & 0x0F
		b3 := lits[idx+3] & 0x0F
		b4 := lits[idx+4] & 0x0F
		b5 := lits[idx+5] & 0x0F
		b6 := lits[idx+6] & 0x0F
		b7 := lits[idx+7] & 0x0F

		lowBuf[j] = (b0 << 4) | b1
		lowBuf[j+1] = (b2 << 4) | b3
		lowBuf[j+2] = (b4 << 4) | b5
		lowBuf[j+3] = (b6 << 4) | b7
	}
	for ; j < numPairs; j++ {
		idx := j * 2
		b0 := lits[idx] & 0x0F
		b1 := uint8(0)
		if idx+1 < n {
			b1 = lits[idx+1] & 0x0F
		}
		lowBuf[j] = (b0 << 4) | b1
	}

	// 2. Pack High-Nibble Plane (Nibble-RLE tokens: (runLen-1)<<4 | highNibble)
	out = out[:0]
	// 8-byte header: numLits (uint32), lowBytes (uint32)
	var hdr [8]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(n))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(numPairs))
	out = append(out, hdr[:]...)
	out = append(out, lowBuf...)

	for i := 0; i < n; {
		h := lits[i] >> 4
		runLen := 1
		for i+runLen < n && (lits[i+runLen]>>4) == h && runLen < 16 {
			runLen++
		}
		token := (uint8(runLen-1) << 4) | (h & 0x0F)
		out = append(out, token)
		i += runLen
	}

	// Only accept if at least 8% smaller than raw
	if len(out) < int(float64(n)*0.92) {
		return out, true
	}
	return nil, false
}

// packSheafVANS encodes residual literals using SIMD Sheaf splitting and V-ANS.
// Returns the packed bytes, or (nil, false) if not compressible or invalid.
func packSheafVANS(lits []byte, pad *scratchpad) ([]byte, bool) {
	n := len(lits)
	if n < 32 {
		return nil, false
	}

	reqCap := n + 128
	if cap(pad.sheafSBuf) < reqCap {
		pad.sheafSBuf = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)
	}
	if cap(pad.sheafCBuf) < reqCap {
		pad.sheafCBuf = bufkit.AlignedBytes(reqCap, bufkit.CacheLineSize)
	}
	maskLen := (n + 7) / 8
	if cap(pad.sheafMaskBuf) < maskLen+128 {
		pad.sheafMaskBuf = bufkit.AlignedBytes(maskLen+128, bufkit.CacheLineSize)
	}

	sLen, cLen := SheafSplit(lits, pad.sheafSBuf[:reqCap], pad.sheafCBuf[:reqCap], pad.sheafMaskBuf[:maskLen+128])
	if sLen < 16 || cLen < 16 || sLen < n/32 || cLen < n/32 {
		return nil, false
	}

	encS, errS := VANSEncode4x(pad.sheafSBuf[:sLen])
	if errS != nil || len(encS) == 0 {
		return nil, false
	}

	encC, errC := VANSEncode4x(pad.sheafCBuf[:cLen])
	if errC != nil || len(encC) == 0 {
		return nil, false
	}

	totalPayload := 24 + maskLen + len(encS) + len(encC)
	if totalPayload >= int(float64(n)*0.95) {
		return nil, false
	}

	if cap(pad.sheafWorkBuf) < totalPayload {
		pad.sheafWorkBuf = bufkit.AlignedBytes(totalPayload+1024, bufkit.CacheLineSize)
	}
	workBuf := pad.sheafWorkBuf[:totalPayload]

	workBuf[0] = vansMagic0
	workBuf[1] = vansMagic1
	workBuf[2] = vansVer1
	workBuf[3] = vansFlag4X | vansFlagSheaf

	binary.LittleEndian.PutUint32(workBuf[4:8], uint32(n))
	binary.LittleEndian.PutUint32(workBuf[8:12], uint32(sLen))
	binary.LittleEndian.PutUint32(workBuf[12:16], uint32(maskLen))
	binary.LittleEndian.PutUint32(workBuf[16:20], uint32(len(encS)))
	binary.LittleEndian.PutUint32(workBuf[20:24], uint32(len(encC)))

	copy(workBuf[24:24+maskLen], pad.sheafMaskBuf[:maskLen])
	copy(workBuf[24+maskLen:24+maskLen+len(encS)], encS)
	copy(workBuf[24+maskLen+len(encS):], encC)

	return workBuf[:totalPayload], true
}

// detectStride scans initial bytes to identify periodic record or column strides S in [4, 256].
func detectStride(src []byte) (int, bool) {
	sampleLen := min(len(src), 2048)
	if sampleLen < 32 {
		return 1, false
	}

	candidates := []int{4, 8, 12, 16, 20, 24, 28, 32, 40, 48, 56, 64, 72, 80, 96, 128, 160, 192, 256}
	bestStride := 1
	bestScore := 0

	for _, s := range candidates {
		if s*2 > sampleLen {
			break
		}
		score := 0
		checkCount := sampleLen - s
		for i := 0; i < checkCount; i += 4 {
			if src[i] == src[i+s] {
				score++
			}
		}
		numChecks := (checkCount + 3) / 4
		// Baseline matching ratio (at least 20% match rate and significant periodicity)
		if score > bestScore && score > (numChecks/5) {
			bestScore = score
			bestStride = s
		}
	}

	if bestScore > 0 && bestStride >= 4 {
		return bestStride, true
	}
	return 1, false
}

// isHighEntropy evaluates a 512-byte sample prefix to detect high-entropy / pre-compressed streams.
// By Theorem 20 (Adaptive Entropy Bypass), white noise cannot be compressed by grammar or tensor models.
func isHighEntropy(src []byte) bool {
	sampleLen := min(len(src), 512)
	if sampleLen < 128 {
		return false
	}
	var hist [256]uint8
	unique := 0
	for i := range sampleLen {
		b := src[i]
		if hist[b] == 0 {
			unique++
			hist[b] = 1
		}
	}
	// If unique byte count is >= 88% of sample length (e.g. >= 225 out of 256), entropy is near maximum
	threshold := (sampleLen * 88) / 100
	return unique >= threshold
}

// isCandidateAST determines if src is structured text (JSON, XML, AST, source code)
// by verifying the absence of binary control characters and presence of balanced brackets.
// Rejects binary data and unstructured text in < 25 ns.
func isCandidateAST(src []byte) bool {
	sampleLen := min(len(src), 1024)
	if sampleLen < 64 {
		return false
	}
	// Quick binary check: reject if null bytes or non-printable ASCII control bytes appear
	checkLen := min(sampleLen, 256)
	for i := range checkLen {
		b := src[i]
		if b == 0 || (b < 0x09 && b > 0x00) || (b > 0x0D && b < 0x20) {
			return false
		}
	}
	openCount, closeCount := 0, 0
	for i := range sampleLen {
		switch src[i] {
		case '{', '[', '(':
			openCount++
		case '}', ']', ')':
			closeCount++
		}
	}
	return openCount >= 4 && closeCount >= 4
}

// hasChannelCorrelation4 verifies cross-channel covariance and stride-4 alignment
// to determine whether 4-channel tensor / geometric transforms (Spectral Unitary, Minkowski h-MDS)
// can reduce entropy. Rejects uncorrelated data in < 30 ns.
func hasChannelCorrelation4(src []byte) bool {
	sampleQuads := 16 // 64 bytes
	if len(src) < sampleQuads*4 {
		return false
	}
	totalCrossDiff := 0
	stride4Matches := 0
	for i := 0; i < sampleQuads*4; i += 4 {
		b0 := int(src[i])
		b1 := int(src[i+1])
		b2 := int(src[i+2])
		b3 := int(src[i+3])
		d0 := b0 - b1
		if d0 < 0 {
			d0 = -d0
		}
		d1 := b1 - b2
		if d1 < 0 {
			d1 = -d1
		}
		d2 := b2 - b3
		if d2 < 0 {
			d2 = -d2
		}
		totalCrossDiff += d0 + d1 + d2

		if i+4 < sampleQuads*4 && src[i] == src[i+4] {
			stride4Matches++
		}
	}
	// High cross-channel affinity (avg diff < 42) or clear stride-4 alignment
	return totalCrossDiff < (sampleQuads*3*42) || stride4Matches >= 6
}

// isCandidateShaderOrVec4 verifies whether src exhibits GPU SPIR-V / WGSL shader bytecode,
// float4 vector attributes, or structured 4-column tabular alignment.
// Evaluates in < 25 ns on a cacheline sample.
func isCandidateShaderOrVec4(src []byte) bool {
	n := len(src)
	if n < 64 {
		return false
	}
	// 1. Detect SPIR-V shader bytecode magic (0x07230203 or 0x03022307)
	if n >= 4 {
		magic := binary.LittleEndian.Uint32(src[0:4])
		if magic == 0x07230203 || magic == 0x03022307 {
			return true
		}
	}
	// 2. Sample quads for stride-4 channel periodicity or float4 mantissa alignment
	sampleQuads := min(n/4, 32)
	stride4Matches := 0
	float4ExpCount := 0
	for i := 0; i < sampleQuads*4; i += 4 {
		if i+4 < sampleQuads*4 && src[i] == src[i+4] {
			stride4Matches++
		}
		// IEEE-754 32-bit float exponent byte (byte index 3 in little-endian float)
		b3 := src[i+3]
		if b3 >= 0x38 && b3 <= 0x48 { // Normalized floats in [0.001, 1000.0]
			float4ExpCount++
		}
	}
	return stride4Matches >= 4 || float4ExpCount >= (sampleQuads*3)/4
}

// isCandidateTDA checks if src possesses topological sparsity (>= 15% zeros)
// or 64-bit periodic word motifs suitable for persistent homology barcode compression.
// Rejects arbitrary text and binary data in < 15 ns.
func isCandidateTDA(src []byte) bool {
	sampleLen := min(len(src), 256)
	if sampleLen < 64 {
		return false
	}
	zeroCount := 0
	words := sampleLen / 8
	repWords := 0
	for i := range words {
		w := binary.LittleEndian.Uint64(src[i*8 : (i+1)*8])
		if w == 0 {
			zeroCount += 8
		}
		if i >= 1 && (w == binary.LittleEndian.Uint64(src[(i-1)*8:i*8]) || (i >= 4 && w == binary.LittleEndian.Uint64(src[(i-4)*8:(i-3)*8]))) {
			repWords++
		}
	}
	return zeroCount >= (sampleLen*15)/100 || repWords >= 2
}

// CompressOptions configures fine-grained PRZM compression parameters.
type CompressOptions struct {
	Level              int         // 1..5
	WindowLog          int         // 16 (64K), 20 (1M), 23 (8M), or 0 (auto)
	Dict               *Dictionary // Optional external schema dictionary
	RawDict            []byte      // Optional raw prefix history dictionary buffer
	DictID             uint32      // Optional dictionary ID for RawDict
	ArchivalMode       bool        // Explicit toggle for --mode=archival
	StandardStreamOnly bool        // Ensure canonical PRZM stream format (MagicPRZM = 0x4D5A5250)
	Threads            int         // 0 = auto (runtime.GOMAXPROCS(0)), 1 = single-threaded, >1 = explicit worker count
	ChunkSize          int         // 0 = auto (default 2 MiB), >0 = explicit chunk uncompressed byte size
	GPUAcceleration    bool        // Offload LZ77 dictionary search to NVIDIA Ampere GPU via CUDA Driver API
	MinMatch           int         // Minimum match length: 3 or 4 (default 4; 3 enables FlagMinMatch3)
	CompactDict        bool        // Enable dedicated OpcodePrefixRawDict (0xE9) for constant raw dictionary offsets
}

// Option configures CompressOptions.
type Option func(*CompressOptions)

// WithGPUAcceleration enables NVIDIA GPU hardware LZ77 acceleration in CompressOptions.
func WithGPUAcceleration() Option {
	return func(opts *CompressOptions) {
		opts.GPUAcceleration = true
	}
}

const DefaultChunkSize = 2 * 1024 * 1024 // 2 MiB

func resolveConcurrencyOptions(opts CompressOptions) (threads int, chunkSize int) {
	threads = opts.Threads
	if threads <= 0 {
		threads = runtime.GOMAXPROCS(0)
	}
	if threads < 1 {
		threads = 1
	}
	chunkSize = opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	return threads, chunkSize
}

func shouldUseMultiChunk(srcLen int, opts CompressOptions) bool {
	if opts.StandardStreamOnly {
		return false
	}
	threads, chunkSize := resolveConcurrencyOptions(opts)
	if srcLen > MaxBlockSize {
		return true
	}
	if len(opts.RawDict) > 0 || opts.DictID != 0 {
		return false
	}
	if srcLen > chunkSize && threads > 1 {
		return true
	}
	return false
}

func compressMultiChunk(src []byte, opts CompressOptions) ([]byte, error) {
	threads, chunkSize := resolveConcurrencyOptions(opts)
	if chunkSize > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	uSize := len(src)
	chunkCount := (uSize + chunkSize - 1) / chunkSize
	if chunkCount <= 0 {
		chunkCount = 1
	}

	chunkOpts := opts
	chunkOpts.Threads = 1
	chunkOpts.ChunkSize = 0
	chunkOpts.StandardStreamOnly = true

	results := make([][]byte, chunkCount)

	numWorkers := max(min(chunkCount, threads), 1)

	if numWorkers <= 1 {
		for idx := 0; idx < chunkCount; idx++ {
			start := idx * chunkSize
			end := min(start+chunkSize, uSize)
			comp, err := compressSingleFrame(src[start:end], chunkOpts)
			if err != nil {
				return nil, err
			}
			results[idx] = comp
		}
		return BuildStreamContainer(results, uint64(uSize)), nil
	}

	var nextChunk atomic.Int32
	var firstErr atomic.Pointer[error]
	var wg sync.WaitGroup

	for range numWorkers {
		wg.Go(func() {
			for {
				if firstErr.Load() != nil {
					return
				}
				idx := int(nextChunk.Add(1) - 1)
				if idx >= chunkCount {
					return
				}

				start := idx * chunkSize
				end := min(start+chunkSize, uSize)

				comp, err := compressSingleFrame(src[start:end], chunkOpts)
				if err != nil {
					errCopy := err
					firstErr.CompareAndSwap(nil, &errCopy)
					return
				}
				results[idx] = comp
			}
		})
	}
	wg.Wait()

	if errPtr := firstErr.Load(); errPtr != nil {
		return nil, *errPtr
	}

	return BuildStreamContainer(results, uint64(uSize)), nil
}

// CompressWithOptions encodes src using caller-defined options.
func CompressWithOptions(src []byte, opts CompressOptions, optList ...Option) ([]byte, error) {
	for _, o := range optList {
		o(&opts)
	}
	if opts.ChunkSize > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}
	if shouldUseMultiChunk(len(src), opts) {
		return compressMultiChunk(src, opts)
	}
	return compressSingleFrame(src, opts)
}

// CompressWithWindow encodes src using explicit level and windowLog.
func CompressWithWindow(src []byte, level int, windowLog int) ([]byte, error) {
	return CompressWithOptions(src, CompressOptions{Level: level, WindowLog: windowLog, StandardStreamOnly: true})
}

// Compress encodes src into PRZM format according to PRZM-SPEC-0.5.
func Compress(src []byte, level int) ([]byte, error) {
	return CompressWithOptions(src, CompressOptions{Level: level})
}

func compressInternal(src []byte, level int, customDict *Dictionary) ([]byte, error) {
	return CompressWithOptions(src, CompressOptions{Level: level, Dict: customDict})
}

// tryCompressGPU offloads LZ77 match search to an NVIDIA Ampere+ GPU via the CUDA Driver API.
// If GPU acceleration is enabled, available, and yields valid compression smaller than raw uncompressed size,
// it returns the canonical PRZM frame and true. Otherwise, it gracefully returns nil, false for CPU fallback.
func tryCompressGPU(src []byte, opts CompressOptions, pad *scratchpad) (out []byte, ok bool) {
	if !opts.GPUAcceleration || !gpu.IsAvailable() || len(src) == 0 {
		return nil, false
	}
	if len(src) > MaxBlockSize || opts.Dict != nil {
		return nil, false
	}

	defer func() {
		if r := recover(); r != nil {
			out = nil
			ok = false
		}
	}()

	comp, err := gpu.GetGlobalCompressor()
	if err != nil || comp == nil {
		return nil, false
	}

	matches, _, err := comp.MatchLZ77(src)
	if err != nil {
		return nil, false
	}

	uSize := len(src)
	pad.cmdBuf = pad.cmdBuf[:0]
	pad.offsetBuf = pad.offsetBuf[:0]
	pad.literalBuf = pad.literalBuf[:0]

	flushLiterals := func(lits []byte) {
		nLits := len(lits)
		if nLits == 0 {
			return
		}
		if nLits <= 8 {
			pad.cmdBuf = append(pad.cmdBuf, 0xF2+byte(nLits))
		} else if nLits < 0x80 {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral, byte(nLits))
		} else if nLits < 0x4000 {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral, byte(nLits|0x80), byte(nLits>>7))
		} else {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral)
			var varBuf [8]byte
			n := binary.PutUvarint(varBuf[:], uint64(nLits))
			pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
		}
		pad.literalBuf = append(pad.literalBuf, lits...)
	}

	writeOffset := func(d int) {
		if d < 0x80 {
			pad.offsetBuf = append(pad.offsetBuf, byte(d))
		} else if d < 0x4000 {
			pad.offsetBuf = append(pad.offsetBuf, byte(d|0x80), byte(d>>7))
		} else {
			var buf [8]byte
			n := binary.PutUvarint(buf[:], uint64(d))
			pad.offsetBuf = append(pad.offsetBuf, buf[:n]...)
		}
	}

	rep0 := 1
	rep1 := 4
	rep2 := 8

	pos := 0
	litStart := 0
	matchIdx := 0

	for pos < uSize {
		// 1. RLE Check (run of identical bytes >= 12)
		if pos+12 <= uSize && src[pos] == src[pos+1] && src[pos] == src[pos+2] && src[pos] == src[pos+3] {
			rleByte := src[pos]
			rleLen := 4
			for pos+rleLen < uSize && src[pos+rleLen] == rleByte && rleLen < 65535 {
				rleLen++
			}
			if rleLen >= 12 {
				if pos > litStart {
					flushLiterals(src[litStart:pos])
				}
				pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRLE)
				var varBuf [8]byte
				n := binary.PutUvarint(varBuf[:], uint64(rleLen))
				pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				pad.literalBuf = append(pad.literalBuf, rleByte)

				pos += rleLen
				litStart = pos
				continue
			}
		}

		for matchIdx < len(matches) && matches[matchIdx].Position < uint32(pos) {
			matchIdx++
		}

		if matchIdx < len(matches) && matches[matchIdx].Position == uint32(pos) {
			m := matches[matchIdx]
			d := int(m.Offset)
			mLen := int(m.Length)

			// Lookahead check: does pos+1 have a significantly longer match?
			if matchIdx+1 < len(matches) && matches[matchIdx+1].Position == uint32(pos+1) {
				nextM := matches[matchIdx+1]
				if int(nextM.Length) > mLen+1 {
					pos++
					continue
				}
			}

			if d > 0 && d <= pos && pos+mLen <= uSize && mLen >= 4 &&
				binary.LittleEndian.Uint32(src[pos:pos+4]) == binary.LittleEndian.Uint32(src[pos-d:pos-d+4]) {

				for pos+mLen < uSize && src[pos+mLen] == src[pos-d+mLen] && mLen < 65530 {
					mLen++
				}

				for pos > litStart && pos-d > 0 && src[pos-1] == src[pos-d-1] && mLen < 65530 {
					pos--
					mLen++
				}

				litLen := pos - litStart

				ringK := -1
				if rep0 == d {
					ringK = 0
				} else if rep1 == d {
					ringK = 1
				} else if rep2 == d {
					ringK = 2
				}

				if ringK >= 0 {
					if mLen <= 7 {
						if litLen > 7 {
							flushLiterals(src[litStart : pos-7])
							litStart = pos - 7
							litLen = 7
						}
						op := uint8((litLen << 4) | ((mLen - 4) << 2) | ringK)
						pad.cmdBuf = append(pad.cmdBuf, op)
						if litLen > 0 {
							pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
						}
					} else {
						if litLen > 15 {
							flushLiterals(src[litStart : pos-15])
							litStart = pos - 15
							litLen = 15
						}
						baseLen := mLen - 4
						if baseLen > 14 {
							baseLen = 15
						}
						op := OpcodePrefixLongRepeat | uint8(ringK)
						token := uint8((litLen << 4) | baseLen)
						pad.cmdBuf = append(pad.cmdBuf, op, token)
						if baseLen == 15 {
							var varBuf [8]byte
							n := binary.PutUvarint(varBuf[:], uint64(mLen-19))
							pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
						}
						if litLen > 0 {
							pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
						}
					}
					switch ringK {
					case 0:
					case 1:
						rep1 = rep0
						rep0 = d
					case 2:
						rep2 = rep1
						rep1 = rep0
						rep0 = d
					}
				} else {
					if litLen > 3 {
						flushLiterals(src[litStart : pos-3])
						litStart = pos - 3
						litLen = 3
					}
					baseLen := mLen - 4
					if baseLen > 14 {
						baseLen = 15
					}
					op := OpcodeFlagFusedExt | uint8(litLen<<4) | uint8(baseLen)
					pad.cmdBuf = append(pad.cmdBuf, op)
					if baseLen == 15 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(mLen-19))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					}
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					writeOffset(d)
					rep2 = rep1
					rep1 = rep0
					rep0 = d
				}

				pos += mLen
				litStart = pos
				continue
			}
		}

		pos++
	}

	if litStart < uSize {
		flushLiterals(src[litStart:uSize])
	}

	level := opts.Level
	if opts.ArchivalMode {
		level = ModeArchival
	}
	if level <= 0 {
		level = 3
	}

	litSection := pad.literalBuf
	flags := uint8(0)
	if len(pad.literalBuf) >= 32 {
		var vansOut []byte
		var vansBeatsRaw bool
		if !opts.StandardStreamOnly && level >= 2 {
			sheafOut, sheafOk := packSheafVANS(pad.literalBuf, pad)
			stdOut, stdErr := VANSEncode4x(pad.literalBuf)
			stdOk := stdErr == nil && len(stdOut) < int(float64(len(pad.literalBuf))*0.95)

			if isProbablyText(src) && sheafOk {
				vansOut = sheafOut
				vansBeatsRaw = true
			} else if sheafOk && stdOk {
				if len(sheafOut) <= len(stdOut) {
					vansOut = sheafOut
				} else {
					vansOut = stdOut
				}
				vansBeatsRaw = true
			} else if sheafOk {
				vansOut = sheafOut
				vansBeatsRaw = true
			} else if stdOk {
				vansOut = stdOut
				vansBeatsRaw = true
			}
		}

		var nibbleOut []byte
		var nibbleOk bool
		if level > 3 {
			nibbleOut, nibbleOk = packNibblePlanes(pad.literalBuf, pad.nibbleBuf, pad.nibbleLowBuf)
		}
		if vansBeatsRaw && (!nibbleOk || len(vansOut) <= len(nibbleOut)) {
			litSection = vansOut
			flags |= FlagNibble
		} else if nibbleOk {
			litSection = nibbleOut
			flags |= FlagNibble
		}
	}

	cmdMode := uint8(ModeHuffman4X)
	if level <= 3 {
		cmdMode = ModeFlat
	}
	cmdSection, err := PackStream(pad.cmdBuf, cmdMode)
	if err != nil {
		return nil, false
	}
	offSection, err := PackStream(pad.offsetBuf, cmdMode)
	if err != nil {
		return nil, false
	}

	windowLog := opts.WindowLog
	if windowLog == 0 {
		if uSize <= 64*1024 {
			windowLog = WindowLog64K
		} else if uSize > 1024*1024 || level >= 3 {
			windowLog = WindowLog8M
		} else {
			windowLog = WindowLog1M
		}
	}
	if windowLog < WindowLog64K {
		windowLog = WindowLog64K
	} else if windowLog > WindowLog8M {
		windowLog = WindowLog8M
	}

	if uSize > 65535 || len(cmdSection) > 65535 || len(offSection) > 65535 || windowLog > WindowLog64K {
		flags |= FlagExtOffset
	}

	secDirSize := generic.Ternary((flags&FlagExtOffset) != 0, 6, 4)
	sec1Size := 8
	totalCompressedSize := HeaderSize + secDirSize + sec1Size + len(cmdSection) + len(offSection) + len(litSection)

	if totalCompressedSize >= uSize {
		return nil, false
	}

	out = make([]byte, totalCompressedSize)
	binary.LittleEndian.PutUint32(out[0:4], MagicPRZM)
	out[4] = flags
	out[5] = byte(uSize)
	out[6] = byte(uSize >> 8)
	out[7] = byte(uSize >> 16)

	wOffset := HeaderSize
	if (flags & FlagExtOffset) == 0 {
		binary.LittleEndian.PutUint16(out[wOffset:wOffset+2], uint16(len(cmdSection)))
		binary.LittleEndian.PutUint16(out[wOffset+2:wOffset+4], uint16(len(offSection)))
		wOffset += 4
	} else {
		cmdLen := len(cmdSection)
		offLen := len(offSection)
		out[wOffset] = byte(cmdLen)
		out[wOffset+1] = byte(cmdLen >> 8)
		out[wOffset+2] = byte(cmdLen >> 16)
		out[wOffset+3] = byte(offLen)
		out[wOffset+4] = byte(offLen >> 8)
		out[wOffset+5] = byte(offLen >> 16)
		wOffset += 6
	}

	for i := range sec1Size {
		out[wOffset+i] = 0
	}
	out[wOffset+1] = byte(windowLog)
	wOffset += sec1Size

	copy(out[wOffset:wOffset+len(cmdSection)], cmdSection)
	wOffset += len(cmdSection)

	copy(out[wOffset:wOffset+len(offSection)], offSection)
	wOffset += len(offSection)

	copy(out[wOffset:wOffset+len(litSection)], litSection)

	return out, true
}

func compressSingleFrame(src []byte, opts CompressOptions) ([]byte, error) {
	level := opts.Level
	if opts.ArchivalMode {
		level = ModeArchival
	}
	if level <= 0 {
		level = 3
	}
	customDict := opts.Dict

	rawDict := opts.RawDict
	dictID := opts.DictID
	if len(rawDict) == 0 && dictID != 0 {
		rawDict = GetRawDictionary(dictID)
	}
	dictLen := len(rawDict)

	uSize := len(src)
	if uSize == 0 {
		hdr := [HeaderSize]byte{}
		binary.LittleEndian.PutUint32(hdr[0:4], MagicPRZM)
		hdr[4] = FlagUncompressed
		return hdr[:], nil
	}

	if uSize > MaxBlockSize {
		return nil, ErrExceedsMaxBlock
	}

	windowLog := opts.WindowLog
	if windowLog == 0 {
		totalSpan := uSize + dictLen
		if level >= 6 {
			windowLog = WindowLog8M
		} else if totalSpan <= 64*1024 {
			windowLog = WindowLog64K
		} else if totalSpan > 1024*1024 || level >= 3 {
			windowLog = WindowLog8M
		} else {
			windowLog = WindowLog1M
		}
	}
	if windowLog < WindowLog64K {
		windowLog = WindowLog64K
	} else if windowLog > WindowLog8M {
		windowLog = WindowLog8M
	}
	windowSize := 1 << windowLog

	pad := padPool.Get()
	defer padPool.Put(pad)

	clear(pad.head)
	var sp1M *HenselScratchpad1M
	var sp8M *HenselScratchpad8M
	var spL6 *HenselScratchpadL6
	if level >= 6 {
		spL6 = PoolHenselL6.Get()
		defer PoolHenselL6.Put(spL6)
		spL6.Reset()
	} else if level >= 3 {
		switch windowLog {
		case WindowLog8M:
			sp8M = PoolHensel8M.Get()
			defer PoolHensel8M.Put(sp8M)
			sp8M.Reset()
		case WindowLog1M:
			sp1M = PoolHensel1M.Get()
			defer PoolHensel1M.Put(sp1M)
			sp1M.Reset()
		default:
			pad.hensel.Reset()
		}
	}
	origSrc := src
	origUSize := uSize
	pad.cmdBuf = ensureBufCap(pad.cmdBuf, origUSize)
	pad.offsetBuf = ensureBufCap(pad.offsetBuf, origUSize/2)
	pad.literalBuf = ensureBufCap(pad.literalBuf, origUSize)

	if dictLen > 0 {
		fullSrc := make([]byte, dictLen+origUSize)
		copy(fullSrc[:dictLen], rawDict)
		copy(fullSrc[dictLen:], origSrc)
		src = fullSrc
		uSize = len(fullSrc)
	}

	if opts.GPUAcceleration && dictLen == 0 {
		if comp, ok := tryCompressGPU(src, opts, pad); ok {
			return comp, nil
		}
		// Clean reset for transparent CPU SIMD fallback
		pad.cmdBuf = pad.cmdBuf[:0]
		pad.offsetBuf = pad.offsetBuf[:0]
		pad.literalBuf = pad.literalBuf[:0]
	}

	highEntropy := isHighEntropy(origSrc)
	// Content Router: Sub-microsecond decision tree (<290 ns budget, 0 dynamic allocations)
	route := RouteContent(origSrc)

	var pwsCandidate []byte
	var vmCandidate []byte
	var tdaCandidate []byte
	var hypCandidate []byte
	var spectralCandidate []byte
	var hmdsCandidate []byte
	var gctrCandidate []byte
	var symplecticCandidate []byte
	var mahlerCandidate []byte
	var sheafCandidate []byte
	var arm64Candidate []byte
	var macroCandidate []byte
	var depCandidate []byte
	var isoquantCandidate []byte
	var heisenbergCandidate []byte
	var cellularSheafCandidate []byte
	var mobiusCandidate []byte
	var margolusCandidate []byte
	var padicCandidate []byte
	var hodgeCandidate []byte

	// Fast bypass: only evaluate heavy mathematical transforms if router classified as SparseZero or SmoothDelta
	allowTransforms := (route == RouteSparseZero || route == RouteSmoothDelta) && !highEntropy && level >= 2 && dictLen == 0
	if opts.StandardStreamOnly {
		allowTransforms = false
	}

	// Specialized filters: Protobuf, ARM64, p-Adic Hensel lifting, and Hodge Étale Sheaves
	if !opts.StandardStreamOnly && opts.Dict == nil && dictLen == 0 && !highEntropy && level >= 2 && origUSize >= 64 {
		if isProtobuf := DetectProtobufTLV(origSrc); isProtobuf {
			if depOut := CompressDependentSheaf(origSrc); len(depOut) > 0 && len(depOut) < origUSize {
				depCandidate = depOut
			}
		}
		if isARM64 := DetectARM64(origSrc); isARM64 {
			if arm64Out := CompressARM64(origSrc); len(arm64Out) > 0 && len(arm64Out) < origUSize {
				arm64Candidate = arm64Out
			}
		}
		if padicOut, err := CompressPadic(origSrc, level); err == nil && len(padicOut) > 0 && len(padicOut) < origUSize {
			padicCandidate = padicOut
		}
		if hodgeOut, err := CompressHodge(origSrc); err == nil && len(hodgeOut) > 0 && len(hodgeOut) < origUSize {
			hodgeCandidate = hodgeOut
		}
		if hasChannelCorrelation4(origSrc) || isCandidateShaderOrVec4(origSrc) || (uSize >= 64 && uSize%8 == 0 && EstimateOptimalSymplecticShear8(origSrc) > 0.05) {
			if sympOut := CompressSymplectic(origSrc); len(sympOut) > 0 && len(sympOut) < origUSize {
				symplecticCandidate = sympOut
			}
		}
	}

	if allowTransforms {
		// RouteSparseZero allows sparse/topological transforms:
		if route == RouteSparseZero {
			if level >= 3 && uSize >= 256 && isCandidateTDA(origSrc) {
				if tdaOut := CompressTDA(origSrc); len(tdaOut) > 0 && len(tdaOut) < origUSize {
					tdaCandidate = tdaOut
				}
			}
			if level >= 2 && uSize >= 32 {
				if csOut, err := CompressCellularSheaf(origSrc, level); err == nil && len(csOut) > 0 && len(csOut) < origUSize {
					cellularSheafCandidate = csOut
				}
			}
		}

		// RouteSmoothDelta allows continuous physical signal transforms:
		if route == RouteSmoothDelta {
			if level >= 3 && uSize >= 256 {
				if mahlerOut := CompressMahler(origSrc); len(mahlerOut) > 0 && len(mahlerOut) < origUSize {
					mahlerCandidate = mahlerOut
				}
			}
			if level >= 3 && uSize >= 256 && hasChannelCorrelation4(origSrc) {
				if sympOut := CompressSymplectic(origSrc); len(sympOut) > 0 && len(sympOut) < origUSize {
					symplecticCandidate = sympOut
				}
			}
			if level >= 4 && uSize >= 512 {
				if gctrOut := CompressGCTR(origSrc); len(gctrOut) > 0 && len(gctrOut) < origUSize {
					gctrCandidate = gctrOut
				}
			}
			if level >= 4 && uSize >= 512 && hasChannelCorrelation4(origSrc) {
				if specOut := CompressSpectralUnitary(origSrc); len(specOut) > 0 && len(specOut) < origUSize {
					spectralCandidate = specOut
				}
			}
			if level >= 4 && uSize >= 512 && hasChannelCorrelation4(origSrc) {
				if hmdsOut := CompressHMDS(origSrc); len(hmdsOut) > 0 && len(hmdsOut) < origUSize {
					hmdsCandidate = hmdsOut
				}
			}
			if level >= 3 && uSize >= 64 && (hasChannelCorrelation4(origSrc) || isCandidateShaderOrVec4(origSrc)) {
				if isoOut, err := CompressIsoQuant(origSrc); err == nil && len(isoOut) > 0 && len(isoOut) < origUSize {
					isoquantCandidate = isoOut
				}
			}
			if level >= 2 && uSize >= 48 {
				if hOut, err := CompressHeisenberg(origSrc, level); err == nil && len(hOut) > 0 && len(hOut) < origUSize {
					heisenbergCandidate = hOut
				}
			}
			if level >= 2 && uSize >= 32 {
				if csOut, err := CompressCellularSheaf(origSrc, level); err == nil && len(csOut) > 0 && len(csOut) < origUSize {
					cellularSheafCandidate = csOut
				}
			}
			if level >= 2 && uSize >= 64 {
				if mOut, err := CompressMobius(origSrc, level); err == nil && len(mOut) > 0 && len(mOut) < origUSize {
					mobiusCandidate = mOut
				}
			}
			if level >= 2 && uSize >= 32 {
				if margOut, err := CompressMargolus(origSrc, level); err == nil && len(margOut) > 0 && len(margOut) < origUSize {
					margolusCandidate = margOut
				}
			}
		}
	}
	flags := uint8(0)
	normMode := NormModeNone
	normContractedLen := 0

	if dictLen == 0 && route != RouteContainerBypass && !highEntropy && origUSize >= minNormSpanLen {
		if isHex, upper := IsWholeBlockHex(src); isHex {
			cLen := len(src) / 2
			if len(src)-cLen >= minNetSavings {
				if cap(pad.normBuf) < cLen {
					pad.normBuf = make([]byte, cLen)
				} else {
					pad.normBuf = pad.normBuf[:cLen]
				}
				ContractHex(src, pad.normBuf)
				src = pad.normBuf
				uSize = cLen
				normContractedLen = cLen
				if upper {
					normMode = NormModeHexUpper
				} else {
					normMode = NormModeHex
				}
				flags |= FlagNorm
			}
		} else if isB64, padCount, urlSafe := IsWholeBlockBase64(src); isB64 {
			cLen := (len(src)/4)*3 - padCount
			if len(src)-cLen >= minNetSavings {
				if cap(pad.normBuf) < cLen {
					pad.normBuf = make([]byte, cLen)
				} else {
					pad.normBuf = pad.normBuf[:cLen]
				}
				ContractBase64(src, pad.normBuf, padCount, urlSafe)
				src = pad.normBuf
				uSize = cLen
				normContractedLen = cLen
				if !urlSafe {
					switch padCount {
					case 0:
						normMode = NormModeBase64
					case 1:
						normMode = NormModeBase64Pad1
					case 2:
						normMode = NormModeBase64Pad2
					}
				} else {
					switch padCount {
					case 0:
						normMode = NormModeBase64URL
					case 1:
						normMode = NormModeBase64URLP1
					case 2:
						normMode = NormModeBase64URLP2
					}
				}
				flags |= FlagNorm
			}
		} else {
			var plan NormPlan
			if level >= 4 {
				plan = DetectNormSpans(src)
			}
			if plan.NetSavings >= minNetSavings && len(plan.Spans) > 0 {
				pad.normBuf = EncodeNormSpans(src, plan, pad.normBuf)
				src = pad.normBuf
				uSize = len(pad.normBuf)
				normContractedLen = uSize
				normMode = NormModeSpans
				flags |= FlagNorm
			}
		}
	}

	stride := 1
	hasStride := false
	if dictLen == 0 {
		stride, hasStride = detectStride(src)
		if hasStride {
			flags |= FlagStride
		}
	} else {
		stride, hasStride = detectStride(origSrc)
		if hasStride {
			flags |= FlagStride
		}
	}

	var activeMatcher *dictMatcher
	if customDict != nil {
		activeMatcher = &customDict.matcher
		dictID = customDict.id
		flags |= FlagDictRef
	} else if dictID != 0 {
		if d := GetDictionary(dictID); d != nil {
			activeMatcher = &d.matcher
		}
		flags |= FlagDictRef
	} else if dictLen > 0 {
		flags |= FlagDictRef
	}

	minMatch := 4
	if opts.MinMatch == 3 || (level >= 6 && opts.MinMatch != 4) {
		minMatch = 3
		flags |= FlagMinMatch3
	}
	minMatchBase := minMatch
	compactDict := opts.CompactDict && dictLen > 0

	if uSize > 65535 || windowLog > WindowLog64K {
		flags |= FlagExtOffset
	}

	calcOffCost := func(d int) int {
		if d < 128 {
			return 1
		}
		if d < 16384 {
			return 2
		}
		if d < 2097152 {
			return 3
		}
		return 4
	}

	writeOffset := func(d int) {
		if d < 0x80 {
			pad.offsetBuf = append(pad.offsetBuf, byte(d))
		} else if d < 0x4000 {
			pad.offsetBuf = append(pad.offsetBuf, byte(d|0x80), byte(d>>7))
		} else {
			var buf [8]byte
			n := binary.PutUvarint(buf[:], uint64(d))
			pad.offsetBuf = append(pad.offsetBuf, buf[:n]...)
		}
	}

	// Initialize 3 CPU repeat offset registers
	rep0 := 1
	rep1 := 4
	rep2 := 8
	if hasStride && stride > 1 {
		rep1 = stride
		rep2 = 2 * stride
	}

	flushLiterals := func(lits []byte) {
		nLits := len(lits)
		if nLits == 0 {
			return
		}
		if nLits <= 8 {
			pad.cmdBuf = append(pad.cmdBuf, 0xF2+byte(nLits))
		} else if nLits < 0x80 {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral, byte(nLits))
		} else if nLits < 0x4000 {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral, byte(nLits|0x80), byte(nLits>>7))
		} else {
			pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixLiteral)
			var varBuf [8]byte
			n := binary.PutUvarint(varBuf[:], uint64(nLits))
			pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
		}
		pad.literalBuf = append(pad.literalBuf, lits...)
	}

	extendMatch := func(pos, d, maxLen int) int {
		mLen := 4
		if minMatch == 3 {
			mLen = 3
			if pos+3 < uSize && pos-d+3 < uSize && src[pos+3] == src[pos-d+3] {
				mLen = 4
			} else {
				return 3
			}
		}
		for pos+mLen+8 <= uSize && mLen+8 <= maxLen {
			diff := binary.LittleEndian.Uint64(src[pos+mLen:pos+mLen+8]) ^ binary.LittleEndian.Uint64(src[pos-d+mLen:pos-d+mLen+8])
			if diff != 0 {
				return mLen + (bits.TrailingZeros64(diff) >> 3)
			}
			mLen += 8
			if mLen >= 32 && pos+maxLen <= uSize {
				rem := maxLen - mLen
				if rem >= 32 {
					return mLen + simd.FindMatchLengthVector(src[pos+mLen:pos+maxLen], src[pos-d+mLen:pos-d+maxLen], rem)
				}
			}
		}
		for pos+mLen < uSize && mLen < maxLen && src[pos+mLen] == src[pos-d+mLen] {
			mLen++
		}
		return mLen
	}

	evalCandidate := func(pos, d, maxLen int) matchCandidate {
		if d <= 0 || pos < d || pos+minMatch > uSize {
			return matchCandidate{}
		}
		if minMatch == 3 {
			if pos+4 <= uSize && pos-d+4 <= uSize {
				if (binary.LittleEndian.Uint32(src[pos:pos+4]) & 0x00FFFFFF) != (binary.LittleEndian.Uint32(src[pos-d:pos-d+4]) & 0x00FFFFFF) {
					return matchCandidate{}
				}
			} else {
				if src[pos] != src[pos-d] || src[pos+1] != src[pos-d+1] || src[pos+2] != src[pos-d+2] {
					return matchCandidate{}
				}
			}
		} else {
			if pos+4 > uSize || pos-d+4 > uSize || binary.LittleEndian.Uint32(src[pos:pos+4]) != binary.LittleEndian.Uint32(src[pos-d:pos-d+4]) {
				return matchCandidate{}
			}
		}
		mLen := extendMatch(pos, d, maxLen)
		if mLen < minMatch {
			return matchCandidate{}
		}

		mPos := pos - d
		if compactDict && mPos < dictLen {
			dictOff := mPos
			offCost := calcOffCost(dictOff)
			cost := 2 + offCost
			if mLen > 14+minMatchBase {
				cost += uvarintLen(uint64(mLen - (15 + minMatchBase)))
			}
			score := mLen - cost
			if score >= 1 {
				return matchCandidate{
					mType:   9,
					length:  mLen,
					dictTok: dictOff,
					cost:    cost,
					score:   score,
				}
			}
			return matchCandidate{}
		}

		ringK := -1
		if rep0 == d {
			ringK = 0
		} else if rep1 == d {
			ringK = 1
		} else if rep2 == d {
			ringK = 2
		}

		if ringK >= 0 {
			repBonus := 0
			if level >= 4 {
				if ringK == 0 {
					repBonus = 4
				} else if ringK == 1 {
					repBonus = 2
				} else {
					repBonus = 1
				}
			}
			if ringK <= 2 && mLen <= 3+minMatchBase {
				return matchCandidate{
					mType:    1,
					length:   mLen,
					distance: d,
					ringK:    ringK,
					cost:     1,
					score:    mLen - 1 + repBonus,
				}
			}
			cost := 2
			if mLen > 14+minMatchBase {
				cost += uvarintLen(uint64(mLen - (15 + minMatchBase)))
			}
			return matchCandidate{
				mType:    3,
				length:   mLen,
				distance: d,
				ringK:    ringK,
				cost:     cost,
				score:    mLen - cost + repBonus,
			}
		}

		// Explicit Extended Match
		offCost := calcOffCost(d)
		minExpLen := minMatchBase
		distPenalty := 0
		if level >= 4 {
			if d >= 16384 {
				minExpLen = minMatchBase + 3
				if d >= 65536 {
					distPenalty = 4
				} else {
					distPenalty = 3
				}
			} else if d >= 1024 {
				minExpLen = minMatchBase + 2
				distPenalty = 2
			} else if d >= 128 {
				minExpLen = minMatchBase + 1
				distPenalty = 1
			}
		} else {
			if d >= 128 {
				minExpLen = minMatchBase + 1
			}
		}
		if mLen < minExpLen {
			return matchCandidate{}
		}

		cost := 1 + offCost
		if mLen > 14+minMatchBase {
			cost += uvarintLen(uint64(mLen - (15 + minMatchBase)))
		}
		score := mLen - (cost + distPenalty)
		if score < 0 {
			return matchCandidate{}
		}
		return matchCandidate{
			mType:    4,
			length:   mLen,
			distance: d,
			cost:     cost,
			score:    score,
		}
	}

	findBest := func(pos, bEnd int) matchCandidate {
		if pos+minMatch > bEnd {
			return matchCandidate{}
		}

		maxBlockLen := min(bEnd-pos, 65535)
		if level >= 6 {
			maxBlockLen = bEnd - pos
		}

		best := matchCandidate{}

		if activeMatcher != nil {
			tokIdx, tokLen := activeMatcher.match(src, pos)
			if tokLen >= 3 && tokLen <= maxBlockLen {
				var cost int
				var mType int
				dictBonus := 0
				if tokIdx < 8 {
					cost = 1
					mType = 7 // OpcodePrefixShortDict
					if level >= 4 {
						dictBonus = 4
					}
				} else {
					cost = 2
					mType = 8 // OpcodePrefixDictToken
					if level >= 4 {
						dictBonus = 2
					}
				}
				score := tokLen - cost + dictBonus
				if score > best.score && score >= 1 {
					best = matchCandidate{
						mType:   mType,
						length:  tokLen,
						dictTok: tokIdx,
						cost:    cost,
						score:   score,
					}
				}
			}
		}

		checkDelta := func(d int, ringK int) {
			if activeMatcher != nil {
				return
			}
			if d < 14 || pos < d || pos+8 > bEnd || pos-d+8 > uSize {
				return
			}
			diff8 := binary.LittleEndian.Uint64(src[pos:pos+8]) ^ binary.LittleEndian.Uint64(src[pos-d:pos-d+8])
			if diff8 != 0 {
				hasZero := (diff8 - 0x0101010101010101) & ^diff8 & 0x8080808080808080
				zeroBytes := bits.OnesCount64(hasZero)
				if zeroBytes < 6 {
					return
				}
			}
			checkLimit := min(1024, d)
			if pos+checkLimit > bEnd {
				checkLimit = bEnd - pos
			}
			if checkLimit < 14 {
				return
			}

			mismatches := 0
			var diffOffsets [2]int
			var diffXORs [2]byte
			curLen := 0
			i := 0
			for i < checkLimit {
				if i+8 <= checkLimit {
					diff := binary.LittleEndian.Uint64(src[pos+i:pos+i+8]) ^ binary.LittleEndian.Uint64(src[pos-d+i:pos-d+i+8])
					if diff == 0 {
						i += 8
						curLen = i
						continue
					}
				}
				if src[pos+i] != src[pos-d+i] {
					if mismatches >= 2 {
						break
					}
					diffOffsets[mismatches] = i
					diffXORs[mismatches] = src[pos+i] ^ src[pos-d+i]
					mismatches++
				}
				curLen = i + 1
				i++
			}

			for curLen > 0 && mismatches > 0 && diffOffsets[mismatches-1] == curLen-1 {
				mismatches--
				curLen--
			}

			if ringK < 0 {
				if rep0 == d {
					ringK = 0
				} else if rep1 == d {
					ringK = 1
				} else if rep2 == d {
					ringK = 2
				}
			}

			minDeltaLen := 64
			minScore := best.score + 12
			if best.score == 0 {
				minScore = 24
			}

			if mismatches >= 1 && mismatches <= 2 && curLen >= minDeltaLen {
				var cost int
				var mType int
				if ringK >= 0 {
					mType = 6 // DeltaMaskRing8
					cost = 2 + 1 + mismatches*2
				} else {
					mType = 5 // DeltaMaskExplicit
					cost = 2 + calcOffCost(d) + 1 + mismatches*2
				}
				if curLen > 258 {
					cost += uvarintLen(uint64(curLen - 259))
				}
				if curLen > 256 {
					cost += mismatches
				}
				score := curLen - cost
				if score >= minScore {
					best = matchCandidate{
						mType:      mType,
						length:     curLen,
						distance:   d,
						ringK:      ringK,
						cost:       cost,
						score:      score,
						mismatches: mismatches,
						diffOffs:   diffOffsets,
						diffXORs:   diffXORs,
					}
				}
			}
		}

		// 1. Hash table lookup (4-way bucket) - Most likely and longest exact matches
		var h uint32
		if minMatch == 3 {
			h3 := uint32(src[pos]) | (uint32(src[pos+1]) << 8) | (uint32(src[pos+2]) << 16)
			h = ((h3 * 0x1E35A7BD) >> 16) & hashTableMask
		} else {
			h = ((binary.LittleEndian.Uint32(src[pos:pos+4]) * 0x1E35A7BD) >> 16) & hashTableMask
		}
		p0 := pad.head[h*4]
		p1 := pad.head[h*4+1]
		p2 := pad.head[h*4+2]
		p3 := pad.head[h*4+3]

		d0 := 0
		if p0 > 0 {
			prevPos := int(p0 - 1)
			d := pos - prevPos
			if d > 0 && d < windowSize {
				d0 = d
				cand := evalCandidate(pos, d, maxBlockLen)
				if cand.score > best.score {
					best = cand
				}
				if level >= 3 && cand.score < 12 {
					checkDelta(d, -1)
				}
			}
		}
		if p1 > 0 {
			prevPos := int(p1 - 1)
			d := pos - prevPos
			if d > 0 && d < windowSize && d != d0 {
				cand := evalCandidate(pos, d, maxBlockLen)
				if cand.score > best.score {
					best = cand
				}
				if level >= 3 && cand.score < 12 {
					checkDelta(d, -1)
				}
			}
		}
		if p2 > 0 {
			prevPos := int(p2 - 1)
			d := pos - prevPos
			if d > 0 && d < windowSize && d != d0 {
				cand := evalCandidate(pos, d, maxBlockLen)
				if cand.score > best.score {
					best = cand
				}
				if level >= 3 && cand.score < 12 {
					checkDelta(d, -1)
				}
			}
		}
		if p3 > 0 {
			prevPos := int(p3 - 1)
			d := pos - prevPos
			if d > 0 && d < windowSize && d != d0 {
				cand := evalCandidate(pos, d, maxBlockLen)
				if cand.score > best.score {
					best = cand
				}
				if level >= 3 && cand.score < 12 {
					checkDelta(d, -1)
				}
			}
		}

		// Hensel Ultrametric Match Finding for Level >= 3 (depth 16 on L3, depth 64 on L4/L5, depth 256 multi-tier on L6)
		if level >= 6 && spL6 != nil {
			hDist, hLen := HenselFindMatchL6(src, pos, uSize, 256, minMatch, spL6)
			if hDist > 0 && hLen >= minMatch {
				cand := evalCandidate(pos, hDist, maxBlockLen)
				if cand.score > best.score {
					best = cand
				}
				if cand.score < 12 {
					checkDelta(hDist, -1)
				}
			}
		} else if level >= 3 {
			maxDepth := 16
			if level >= 4 {
				maxDepth = 64
			}
			var hDist, hLen int
			switch windowLog {
			case WindowLog8M:
				if sp8M != nil {
					hDist, hLen = HenselFindMatch8M(src, pos, uSize, maxDepth, minMatch, sp8M)
				}
			case WindowLog1M:
				if sp1M != nil {
					hDist, hLen = HenselFindMatch1M(src, pos, uSize, maxDepth, minMatch, sp1M)
				}
			default:
				hDist, hLen = HenselFindMatch(src, pos, uSize, maxDepth, minMatch, &pad.hensel)
			}
			if hDist > 0 && hLen >= minMatch {
				cand := evalCandidate(pos, hDist, maxBlockLen)
				if cand.score > best.score {
					best = cand
				}
				if cand.score < 12 {
					checkDelta(hDist, -1)
				}
			}
		}

		// 2. Check 3 repeat offset registers (rep0, rep1, rep2) with repeat alignment
		if rep0 > 0 && rep0 != d0 {
			if cand := evalCandidate(pos, rep0, maxBlockLen); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= minMatch && best.mType >= 4) || (cand.length >= best.length && cand.length >= minMatch) {
				best = cand
			}
		}
		if rep1 > 0 && rep1 != d0 && rep1 != rep0 {
			if cand := evalCandidate(pos, rep1, maxBlockLen); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= minMatch && best.mType >= 4) || (cand.length >= best.length && cand.length >= minMatch) {
				best = cand
			}
		}
		if rep2 > 0 && rep2 != d0 && rep2 != rep1 && rep2 != rep0 {
			if cand := evalCandidate(pos, rep2, maxBlockLen); cand.score > best.score || (cand.length >= best.length-1 && cand.length >= minMatch && best.mType >= 4) || (cand.length >= best.length && cand.length >= minMatch) {
				best = cand
			}
		}

		// If strong match found, skip smaller checks
		if level <= 2 {
			if best.length >= 8 && best.score >= 6 {
				return best
			}
		} else {
			if best.length >= 64 {
				return best
			}
		}

		// If a good match >= 8 is found, skip stride and delta-mask
		if best.length >= 8 {
			return best
		}

		// 3. Check Stride (m * S for m >= 3, since m=1,2 are checked in Ring-8 queue)
		if hasStride && best.length < 8 {
			for m := 3; m <= 8; m++ {
				d := m * stride
				if pos >= d && pos+4 <= bEnd && binary.LittleEndian.Uint32(src[pos:pos+4]) == binary.LittleEndian.Uint32(src[pos-d:pos-d+4]) {
					mLen := extendMatch(pos, d, min(maxBlockLen, 19))
					if mLen >= minMatch {
						cost := 2
						score := mLen - cost
						if score > best.score {
							best = matchCandidate{
								mType:    2,
								length:   mLen,
								distance: d,
								strideM:  m,
								cost:     cost,
								score:    score,
							}
						}
					}
				}
			}
		}

		// 4. Fast-filtered Delta-mask fallback check on rep0
		if level >= 3 && best.length < 8 {
			if rep0 >= 48 && pos >= rep0 {
				checkDelta(rep0, 0)
			}
		}

		return best
	}

	insertHash := func(p int) {
		if p+minMatch <= uSize {
			var h uint32
			if minMatch == 3 {
				h3 := uint32(src[p]) | (uint32(src[p+1]) << 8) | (uint32(src[p+2]) << 16)
				h = ((h3 * 0x1E35A7BD) >> 16) & hashTableMask
			} else {
				h = ((binary.LittleEndian.Uint32(src[p:p+4]) * 0x1E35A7BD) >> 16) & hashTableMask
			}
			pad.head[h*4+3] = pad.head[h*4+2]
			pad.head[h*4+2] = pad.head[h*4+1]
			pad.head[h*4+1] = pad.head[h*4]
			pad.head[h*4] = int32(p + 1)

			if level >= 6 && spL6 != nil {
				HenselInsertL6(spL6, p, src, uSize)
			} else if level >= 3 {
				switch windowLog {
				case WindowLog8M:
					if sp8M != nil {
						HenselInsert8M(sp8M, p, src, uSize)
					}
				case WindowLog1M:
					if sp1M != nil {
						HenselInsert1M(sp1M, p, src, uSize)
					}
				default:
					HenselInsert(&pad.hensel, p, src, uSize)
				}
			}
		}
	}

	if dictLen > 0 {
		for p := 0; p+minMatch <= dictLen; p++ {
			var h uint32
			var u32 uint32
			if minMatch == 3 {
				h3 := uint32(src[p]) | (uint32(src[p+1]) << 8) | (uint32(src[p+2]) << 16)
				h = ((h3 * 0x1E35A7BD) >> 16) & hashTableMask
				u32 = h3
			} else {
				u32 = binary.LittleEndian.Uint32(src[p : p+4])
				h = ((u32 * 0x1E35A7BD) >> 16) & hashTableMask
			}

			pad.head[h*4+3] = pad.head[h*4+2]
			pad.head[h*4+2] = pad.head[h*4+1]
			pad.head[h*4+1] = pad.head[h*4]
			pad.head[h*4] = int32(p + 1)

			if level <= 3 {
				h2 := ((u32 * 0x7FEB352D) >> 15) & hashTableMask
				pad.head[h2*4+1] = int32(p + 1)
			} else {
				if level >= 6 && spL6 != nil {
					HenselInsertL6(spL6, p, src, uSize)
				} else {
					switch windowLog {
					case WindowLog8M:
						if sp8M != nil {
							HenselInsert8M(sp8M, p, src, uSize)
						}
					case WindowLog1M:
						if sp1M != nil {
							HenselInsert1M(sp1M, p, src, uSize)
						}
					default:
						HenselInsert(&pad.hensel, p, src, uSize)
					}
				}
			}
		}
	}

	compressBlockFast := func(bStart, bSize int) {
		pos := bStart
		bEnd := bStart + bSize
		if bEnd > len(src) {
			return
		}

		srcPtr := unsafe.Pointer(&src[0])

		litStart := bStart
		misses := 0

		for pos+4 <= bEnd {
			maxBlockLen := bEnd - pos
			if maxBlockLen > 65535 {
				maxBlockLen = 65535
			}
			u32 := *(*uint32)(unsafe.Pointer(uintptr(srcPtr) + uintptr(pos)))
			h1 := ((u32 * 0x1E35A7BD) >> 16) & hashTableMask
			h2 := ((u32 * 0x7FEB352D) >> 15) & hashTableMask

			p0 := pad.head[h1*4]
			p1 := pad.head[h2*4+1]

			pad.head[h1*4] = int32(pos + 1)
			pad.head[h2*4+1] = int32(pos + 1)

			d := 0
			mLen := 0
			if p0 > 0 {
				prevPos := int(p0 - 1)
				dist := pos - prevPos
				if dist > 0 && dist < windowSize && prevPos+4 <= len(src) {
					if *(*uint32)(unsafe.Pointer(uintptr(srcPtr) + uintptr(prevPos))) == u32 {
						d = dist
						mLen = 4
						for pos+mLen+8 <= len(src) && mLen+8 <= maxBlockLen {
							diff := *(*uint64)(unsafe.Pointer(uintptr(srcPtr) + uintptr(pos+mLen))) ^ *(*uint64)(unsafe.Pointer(uintptr(srcPtr) + uintptr(prevPos+mLen)))
							if diff != 0 {
								mLen += (bits.TrailingZeros64(diff) >> 3)
								break
							}
							mLen += 8
						}
						for pos+mLen < len(src) && mLen < maxBlockLen && *(*byte)(unsafe.Pointer(uintptr(srcPtr) + uintptr(pos+mLen))) == *(*byte)(unsafe.Pointer(uintptr(srcPtr) + uintptr(prevPos+mLen))) {
							mLen++
						}
					}
				}
			}
			if p1 > 0 && mLen < 8 {
				prevPos := int(p1 - 1)
				dist := pos - prevPos
				if dist > 0 && dist < windowSize && prevPos+4 <= len(src) {
					if *(*uint32)(unsafe.Pointer(uintptr(srcPtr) + uintptr(prevPos))) == u32 {
						mLen2 := 4
						for pos+mLen2+8 <= len(src) && mLen2+8 <= maxBlockLen {
							diff := *(*uint64)(unsafe.Pointer(uintptr(srcPtr) + uintptr(pos+mLen2))) ^ *(*uint64)(unsafe.Pointer(uintptr(srcPtr) + uintptr(prevPos+mLen2)))
							if diff != 0 {
								mLen2 += (bits.TrailingZeros64(diff) >> 3)
								break
							}
							mLen2 += 8
						}
						for pos+mLen2 < len(src) && mLen2 < maxBlockLen && *(*byte)(unsafe.Pointer(uintptr(srcPtr) + uintptr(pos+mLen2))) == *(*byte)(unsafe.Pointer(uintptr(srcPtr) + uintptr(prevPos+mLen2))) {
							mLen2++
						}
						if mLen2 > mLen {
							mLen = mLen2
							d = dist
						}
					}
				}
			}

			if mLen >= minMatch {
				litLen := pos - litStart
				if litLen > 3 {
					flushLiterals(src[litStart : pos-3])
					litStart = pos - 3
					litLen = 3
				}
				baseLen := mLen - 4
				if baseLen > 14 {
					baseLen = 15
				}
				op := OpcodeFlagFusedExt | uint8(litLen<<4) | uint8(baseLen)
				pad.cmdBuf = append(pad.cmdBuf, op)
				if baseLen == 15 {
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(mLen-19))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
				}
				if litLen > 0 {
					pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
				}

				if d < 0x80 {
					pad.offsetBuf = append(pad.offsetBuf, byte(d))
				} else if d < 0x4000 {
					pad.offsetBuf = append(pad.offsetBuf, byte(d|0x80), byte(d>>7))
				} else {
					var buf [8]byte
					n := binary.PutUvarint(buf[:], uint64(d))
					pad.offsetBuf = append(pad.offsetBuf, buf[:n]...)
				}

				rep2 = rep1
				rep1 = rep0
				rep0 = d

				pos += mLen
				litStart = pos
				misses = 0
			} else {
				misses++
				step := 1 + (misses >> 3)
				pos += step
			}
		}

		if litStart < bEnd {
			flushLiterals(src[litStart:bEnd])
		}
	}

	compressBlockGreedy := func(bStart, bSize int) {
		if level >= ModeArchival {
			// approxIdx removed
		}

		pos := bStart
		bEnd := bStart + bSize
		litStart := bStart
		misses := 0

		for pos+minMatch <= bEnd {
			// 1. RLE Check (run of identical bytes >= 12)
			if pos+12 <= bEnd && src[pos] == src[pos+1] && src[pos] == src[pos+2] && src[pos] == src[pos+3] {
				rleByte := src[pos]
				rleLen := 4
				for pos+rleLen < bEnd && src[pos+rleLen] == rleByte && rleLen < 65535 {
					rleLen++
				}
				if rleLen >= 12 {
					if pos > litStart {
						flushLiterals(src[litStart:pos])
					}
					pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRLE)
					var varBuf [8]byte
					n := binary.PutUvarint(varBuf[:], uint64(rleLen))
					pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					pad.literalBuf = append(pad.literalBuf, rleByte)

					insertHash(pos)
					pos += rleLen
					litStart = pos
					continue
				}
			}

			best := findBest(pos, bEnd)

			if best.score >= 1 && best.length >= minMatch {
				// Deterministic lazy lookahead evaluation at pos+1 to select higher-scoring match candidates
				lookaheadMax := 6
				if level >= 5 {
					lookaheadMax = 16
				}
				if level >= 3 && (best.length <= lookaheadMax || best.mType >= 4) && pos+1+minMatch <= bEnd {
					insertHash(pos)
					nextBest := findBest(pos+1, bEnd)
					// Lookahead comparison between match candidate at pos and pos+1
					preferNext := false
					if nextBest.length > best.length+1 {
						// 1. Yield dominance: l2 covers strictly more bytes even with 1 skipped literal
						preferNext = true
					} else if nextBest.score-1 > best.score {
						// 2. Information-theoretic yield: net score higher after paying 1-byte literal penalty
						preferNext = true
					} else if nextBest.score-1 == best.score && nextBest.length == best.length+1 {
						// 3. Deterministic tie-breaker: prefer repeat/fused matches (lower entropy, 1-byte opcode)
						if nextBest.mType <= 2 && best.mType > 2 {
							preferNext = true
						}
					} else if best.mType >= 4 && (nextBest.mType == 1 || nextBest.mType == 3 || nextBest.mType == 7 || nextBest.mType == 8) && nextBest.length >= best.length-2 {
						// 4. Aggressively protect repeat registers and dictionary tokens!
						// 1 literal + repeat/dict match costs fewer total stream bytes and protects registers
						preferNext = true
					}
					if preferNext {
						pos++
						continue
					}
				}

				// Backward match extension into uncommitted literals!
				if best.mType != 5 && best.mType != 6 && best.mType != 7 && best.mType != 8 && best.mType != 9 {
					for pos > litStart && pos-best.distance > 0 && src[pos-1] == src[pos-best.distance-1] && (level >= 6 || best.length < 65530) {
						pos--
						best.length++
						if best.mType == 1 && (best.length > 3+minMatchBase || best.ringK > 2) {
							best.mType = 3
						}
						if best.mType == 2 && best.length > 15+minMatchBase {
							pos++
							best.length--
							break
						}
					}
				}

				litLen := pos - litStart

				switch best.mType {
				case 1: // Fused Repeat (k <= 2, length minMatchBase..minMatchBase+3)
					if litLen > 7 {
						flushLiterals(src[litStart : pos-7])
						litStart = pos - 7
						litLen = 7
					}
					op := uint8((litLen << 4) | ((best.length - minMatchBase) << 2) | best.ringK)
					pad.cmdBuf = append(pad.cmdBuf, op)
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					switch best.ringK {
					case 0:
					case 1:
						rep1 = rep0
						rep0 = best.distance
					case 2:
						rep2 = rep1
						rep1 = rep0
						rep0 = best.distance
					}

				case 2: // Stride (m 1..8, length minMatchBase..minMatchBase+15)
					if litLen > 15 {
						flushLiterals(src[litStart : pos-15])
						litStart = pos - 15
						litLen = 15
					}
					op := OpcodePrefixStride | uint8(best.strideM-1)
					token := uint8((litLen << 4) | (best.length - minMatchBase))
					pad.cmdBuf = append(pad.cmdBuf, op, token)
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					rep2 = rep1
					rep1 = rep0
					rep0 = best.distance

				case 3: // Long Repeat (k 0..2, length minMatchBase..N) with Fused Literals
					if litLen > 15 {
						flushLiterals(src[litStart : pos-15])
						litStart = pos - 15
						litLen = 15
					}
					baseLen := best.length - minMatchBase
					if baseLen > 14 {
						baseLen = 15
					}
					op := OpcodePrefixLongRepeat | uint8(best.ringK)
					token := uint8((litLen << 4) | baseLen)
					pad.cmdBuf = append(pad.cmdBuf, op, token)
					if baseLen == 15 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(15+minMatchBase)))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					}
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					switch best.ringK {
					case 0:
					case 1:
						rep1 = rep0
						rep0 = best.distance
					case 2:
						rep2 = rep1
						rep1 = rep0
						rep0 = best.distance
					}

				case 4: // Fused Extended Match (length minMatchBase..N, explicit offset)
					if litLen > 3 {
						flushLiterals(src[litStart : pos-3])
						litStart = pos - 3
						litLen = 3
					}
					baseLen := best.length - minMatchBase
					if baseLen > 14 {
						baseLen = 15
					}
					op := OpcodeFlagFusedExt | uint8(litLen<<4) | uint8(baseLen)
					pad.cmdBuf = append(pad.cmdBuf, op)
					if baseLen == 15 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(15+minMatchBase)))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					}
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					writeOffset(best.distance)
					rep2 = rep1
					rep1 = rep0
					rep0 = best.distance

				case 5: // DeltaMaskExplicit
					if litLen > 0 {
						flushLiterals(src[litStart:pos])
					}
					flags |= FlagDeltaMask
					pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixDeltaExpl)
					baseLen := best.length - minMatchBase
					if baseLen >= 255 {
						pad.cmdBuf = append(pad.cmdBuf, 255)
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(255+minMatchBase)))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					} else {
						pad.cmdBuf = append(pad.cmdBuf, uint8(baseLen))
					}
					writeOffset(best.distance)
					pad.literalBuf = append(pad.literalBuf, uint8(best.mismatches))
					for m := 0; m < best.mismatches; m++ {
						if best.length <= 256 {
							pad.literalBuf = append(pad.literalBuf, uint8(best.diffOffs[m]))
						} else {
							var rel [2]byte
							binary.LittleEndian.PutUint16(rel[:], uint16(best.diffOffs[m]))
							pad.literalBuf = append(pad.literalBuf, rel[:]...)
						}
						pad.literalBuf = append(pad.literalBuf, best.diffXORs[m])
					}
					rep2 = rep1
					rep1 = rep0
					rep0 = best.distance

				case 6: // DeltaMaskRing8
					if litLen > 0 {
						flushLiterals(src[litStart:pos])
					}
					flags |= FlagDeltaMask
					op := OpcodePrefixDeltaRing | uint8(best.ringK)
					pad.cmdBuf = append(pad.cmdBuf, op)
					baseLen := best.length - minMatchBase
					if baseLen >= 255 {
						pad.cmdBuf = append(pad.cmdBuf, 255)
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(255+minMatchBase)))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					} else {
						pad.cmdBuf = append(pad.cmdBuf, uint8(baseLen))
					}
					pad.literalBuf = append(pad.literalBuf, uint8(best.mismatches))
					for m := 0; m < best.mismatches; m++ {
						if best.length <= 256 {
							pad.literalBuf = append(pad.literalBuf, uint8(best.diffOffs[m]))
						} else {
							var rel [2]byte
							binary.LittleEndian.PutUint16(rel[:], uint16(best.diffOffs[m]))
							pad.literalBuf = append(pad.literalBuf, rel[:]...)
						}
						pad.literalBuf = append(pad.literalBuf, best.diffXORs[m])
					}
					switch best.ringK {
					case 0:
					case 1:
						rep1 = rep0
						rep0 = best.distance
					case 2:
						rep2 = rep1
						rep1 = rep0
						rep0 = best.distance
					}

				case 7: // OpcodePrefixShortDict (0xD8 .. 0xDF)
					if litLen > 0 {
						flushLiterals(src[litStart:pos])
					}
					op := OpcodePrefixShortDict | uint8(best.dictTok)
					pad.cmdBuf = append(pad.cmdBuf, op)

				case 8: // OpcodePrefixDictToken (0xF2 <tokID>)
					if litLen > 0 {
						flushLiterals(src[litStart:pos])
					}
					pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixDictToken, uint8(best.dictTok))

				case 9: // OpcodePrefixRawDict (0xE9) - Direct raw dictionary match
					if litLen > 15 {
						flushLiterals(src[litStart : pos-15])
						litStart = pos - 15
						litLen = 15
					}
					baseLen := best.length - minMatchBase
					if baseLen > 14 {
						baseLen = 15
					}
					op := OpcodePrefixRawDict
					token := uint8((litLen << 4) | baseLen)
					pad.cmdBuf = append(pad.cmdBuf, op, token)
					if baseLen == 15 {
						var varBuf [8]byte
						n := binary.PutUvarint(varBuf[:], uint64(best.length-(15+minMatchBase)))
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:n]...)
					}
					if litLen > 0 {
						pad.literalBuf = append(pad.literalBuf, src[litStart:pos]...)
					}
					writeOffset(best.dictTok)
				}

				insertHash(pos)
				if level >= 5 && best.length <= 16 {
					for p := pos + 1; p < pos+best.length; p++ {
						insertHash(p)
					}
				} else if best.length > 2 && pos+best.length-1+4 <= uSize {
					insertHash(pos + best.length - 1)
				}
				pos += best.length
				litStart = pos
				misses = 0
				continue
			}

			insertHash(pos)
			misses++
			step := 1
			if level <= 2 {
				step = min(1+(misses>>4), 32)
			} else if level <= 3 {
				step = min(1+(misses>>6), 16)
			} else if level == 4 {
				step = min(1+(misses>>7), 4)
			}
			pos += step
		}

		// Flush remaining literals for this block
		if litStart < bEnd {
			flushLiterals(src[litStart:bEnd])
		}
	}

	useFast := level <= 3 && opts.MinMatch != 3
	if dictLen > 0 {
		generic.Ternary[func(int, int)](useFast, compressBlockFast, compressBlockGreedy)(dictLen, origUSize)
	} else if uSize >= 131072 {
		pad.dedup.Reset()
		var dpPad *dpScratchpad
		if level >= 4 && customDict == nil && dictID == 0 {
			dpPad = dpScratchPool.Get()
			defer dpScratchPool.Put(dpPad)
			clear(dpPad.table3[:])
		}

		bStart := 0
		blockIdx := uint32(0)
		for bStart < uSize {
			bSize := min(uSize-bStart, DedupBlockSize)
			if bSize == DedupBlockSize && blockIdx > 0 {
				h := Hash64KBlock(src[bStart : bStart+DedupBlockSize])
				if matchedIdx, found := pad.dedup.Probe(h, src, bStart); found {
					pad.cmdBuf = append(pad.cmdBuf, OpCopyBlock,
						byte(matchedIdx), byte(matchedIdx>>8),
						byte(matchedIdx>>16), byte(matchedIdx>>24))
					bStart += DedupBlockSize
					blockIdx++
					continue
				}
				if dpPad != nil {
					compressBlockOptimal(src, bStart, bSize, uSize, windowSize, windowLog, level, pad, dpPad, &rep0, &rep1, &rep2, sp1M, sp8M, minMatchBase)
				} else {
					generic.Ternary[func(int, int)](level <= 3, compressBlockFast, compressBlockGreedy)(bStart, bSize)
				}
				pad.dedup.Insert(h, blockIdx)
			} else if bSize == DedupBlockSize && blockIdx == 0 {
				if dpPad != nil {
					compressBlockOptimal(src, bStart, bSize, uSize, windowSize, windowLog, level, pad, dpPad, &rep0, &rep1, &rep2, sp1M, sp8M, minMatchBase)
				} else {
					generic.Ternary[func(int, int)](level <= 3, compressBlockFast, compressBlockGreedy)(bStart, bSize)
				}
				h := Hash64KBlock(src[bStart : bStart+DedupBlockSize])
				pad.dedup.Insert(h, 0)
			} else {
				// Tail block (< 65536 bytes) compressed via standard LZ77
				if dpPad != nil {
					compressBlockOptimal(src, bStart, bSize, uSize, windowSize, windowLog, level, pad, dpPad, &rep0, &rep1, &rep2, sp1M, sp8M, minMatchBase)
				} else {
					generic.Ternary[func(int, int)](level <= 3, compressBlockFast, compressBlockGreedy)(bStart, bSize)
				}
			}
			bStart += bSize
			blockIdx++
		}
	} else {
		// Under 128 KiB: bypass deduplication entirely
		if level >= 4 && customDict == nil && dictID == 0 {
			dpPad := dpScratchPool.Get()
			defer dpScratchPool.Put(dpPad)
			clear(dpPad.table3[:])

			bStart := 0
			for bStart < uSize {
				bSize := min(uSize-bStart, BlockSize64K)
				compressBlockOptimal(src, bStart, bSize, uSize, windowSize, windowLog, level, pad, dpPad, &rep0, &rep1, &rep2, sp1M, sp8M, minMatchBase)
				bStart += bSize
			}
		} else {
			generic.Ternary[func(int, int)](useFast, compressBlockFast, compressBlockGreedy)(0, uSize)
		}
	}

	// Try Post-LZ77 Etale Sheaf (Vector C) stream splitting + V-ANS, standard V-ANS, and SIMD Nibble-Plane packing on literalBuf
	litSection := pad.literalBuf
	if len(pad.literalBuf) >= 32 {
		var vansOut []byte
		var vansBeatsRaw bool
		if !opts.StandardStreamOnly && level >= 2 {
			sheafOut, sheafOk := packSheafVANS(pad.literalBuf, pad)
			stdOut, stdErr := VANSEncode4x(pad.literalBuf)
			stdOk := stdErr == nil && len(stdOut) < int(float64(len(pad.literalBuf))*0.95)

			if isProbablyText(origSrc) && level <= 3 && sheafOk {
				vansOut = sheafOut
				vansBeatsRaw = true
			} else if sheafOk && stdOk {
				if len(sheafOut) <= len(stdOut) {
					vansOut = sheafOut
				} else {
					vansOut = stdOut
				}
				vansBeatsRaw = true
			} else if sheafOk {
				vansOut = sheafOut
				vansBeatsRaw = true
			} else if stdOk {
				vansOut = stdOut
				vansBeatsRaw = true
			}
		}

		var nibbleOut []byte
		var nibbleOk bool
		if level > 3 {
			nibbleOut, nibbleOk = packNibblePlanes(pad.literalBuf, pad.nibbleBuf, pad.nibbleLowBuf)
		}

		if vansBeatsRaw && (!nibbleOk || len(vansOut) <= len(nibbleOut)) {
			litSection = vansOut
			flags |= FlagNibble
		} else if nibbleOk {
			litSection = nibbleOut
			flags |= FlagNibble
		}
	}

	// Pack Section 2 (Commands) and Section 3 (Offsets) via 4-Stream Canonical Huffman
	cmdMode := uint8(ModeHuffman4X)
	if level <= 3 {
		cmdMode = ModeFlat
	}
	cmdSection, err := PackStream(pad.cmdBuf, cmdMode)
	if err != nil {
		return nil, err
	}
	offSection, err := PackStream(pad.offsetBuf, cmdMode)
	if err != nil {
		return nil, err
	}

	if uSize > 65535 || len(cmdSection) > 65535 || len(offSection) > 65535 || windowLog > WindowLog64K {
		flags |= FlagExtOffset
	}

	// Calculate Frame Layout & Size
	frameParamsSize := 0
	if hasStride {
		frameParamsSize += 1
	}
	if (flags & FlagDictRef) != 0 {
		frameParamsSize += 4
	}

	secDirSize := generic.Ternary((flags&FlagExtOffset) != 0, 6, 4)

	sec1Size := 8 // 8-byte distribution params (ModelType = 0 uniform/raw)
	totalCompressedSize := HeaderSize + frameParamsSize + secDirSize + sec1Size + len(cmdSection) + len(offSection) + len(litSection)

	// Pareto selection between candidates
	bestCandidate := MinNonEmpty(pwsCandidate, vmCandidate, tdaCandidate, hypCandidate, spectralCandidate, hmdsCandidate, gctrCandidate, symplecticCandidate, mahlerCandidate, sheafCandidate, arm64Candidate, macroCandidate, depCandidate, isoquantCandidate, heisenbergCandidate, cellularSheafCandidate, mobiusCandidate, margolusCandidate, padicCandidate, hodgeCandidate)

	if !opts.StandardStreamOnly && len(bestCandidate) > 0 && len(bestCandidate) < totalCompressedSize {
		return bestCandidate, nil
	}

	// Fallback to uncompressed if compressed payload exceeds raw data
	if totalCompressedSize >= origUSize {
		if !opts.StandardStreamOnly && len(bestCandidate) > 0 && len(bestCandidate) < origUSize {
			return bestCandidate, nil
		}
		rawFrame := make([]byte, HeaderSize+origUSize)
		binary.LittleEndian.PutUint32(rawFrame[0:4], MagicPRZM)
		rawFrame[4] = FlagUncompressed
		rawFrame[5] = byte(origUSize)
		rawFrame[6] = byte(origUSize >> 8)
		rawFrame[7] = byte(origUSize >> 16)
		copy(rawFrame[HeaderSize:], origSrc)
		return rawFrame, nil
	}

	// Build Final Output Frame
	out := make([]byte, totalCompressedSize)
	binary.LittleEndian.PutUint32(out[0:4], MagicPRZM)
	out[4] = flags
	out[5] = byte(origUSize)
	out[6] = byte(origUSize >> 8)
	out[7] = byte(origUSize >> 16)

	wOffset := HeaderSize
	if hasStride {
		out[wOffset] = uint8(stride - 1)
		wOffset++
	}
	if (flags & FlagDictRef) != 0 {
		binary.LittleEndian.PutUint32(out[wOffset:wOffset+4], dictID)
		wOffset += 4
	}

	// Section Directory
	if (flags & FlagExtOffset) == 0 {
		binary.LittleEndian.PutUint16(out[wOffset:wOffset+2], uint16(len(cmdSection)))
		binary.LittleEndian.PutUint16(out[wOffset+2:wOffset+4], uint16(len(offSection)))
		wOffset += 4
	} else {
		cmdLen := len(cmdSection)
		offLen := len(offSection)
		out[wOffset] = byte(cmdLen)
		out[wOffset+1] = byte(cmdLen >> 8)
		out[wOffset+2] = byte(cmdLen >> 16)
		out[wOffset+3] = byte(offLen)
		out[wOffset+4] = byte(offLen >> 8)
		out[wOffset+5] = byte(offLen >> 16)
		wOffset += 6
	}

	// Section 1: Distribution Parameters (Uniform/Raw)
	for i := range sec1Size {
		out[wOffset+i] = 0
	}
	out[wOffset+1] = byte(windowLog)
	if normMode != NormModeNone {
		out[wOffset+2] = byte(normMode)
		binary.LittleEndian.PutUint16(out[wOffset+3:wOffset+5], uint16(normContractedLen))
		out[wOffset+6] = byte(normContractedLen >> 16)
		out[wOffset+7] = byte(normContractedLen >> 24)
	}
	wOffset += sec1Size

	// Section 2: Command Stream
	copy(out[wOffset:wOffset+len(cmdSection)], cmdSection)
	wOffset += len(cmdSection)

	// Section 3: Offset Channel
	copy(out[wOffset:wOffset+len(offSection)], offSection)
	wOffset += len(offSection)

	// Section 4: Literal, Mask, and RLE Channel
	copy(out[wOffset:wOffset+len(litSection)], litSection)

	return out, nil
}
