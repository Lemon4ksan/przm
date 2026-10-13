// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/lemon4ksan/foundation/bufkit"
	"github.com/lemon4ksan/foundation/generic"
)

const (
	hashTableSize = 65536
	hashTableMask = hashTableSize - 1
	minMatch      = 4
	maxLiteralRun = 64
	initPadSize   = 1024 * 1024
)

type scratchpad struct {
	head            []int32
	hensel          HenselScratchpad
	cmdBuf          []byte
	offsetBuf       []byte
	literalBuf      []byte
	transBuf        []byte
	nibbleBuf       []byte
	nibbleLowBuf    []byte
	splitBuf        []byte
	normBuf         []byte
	dedup           DedupTable
	sheafSBuf       []byte
	sheafCBuf       []byte
	sheafMaskBuf    []byte
	sheafWorkBuf    []byte
	ctxSheafWorkBuf []byte
	sheafFBuf       [4][]byte
}

var padPool = generic.NewPool(func() *scratchpad {
	return &scratchpad{
		head:            make([]int32, hashTableSize*4),
		cmdBuf:          bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		offsetBuf:       bufkit.AlignedBytes(initPadSize/2, bufkit.CacheLineSize)[:0],
		literalBuf:      bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		transBuf:        bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		nibbleBuf:       bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		nibbleLowBuf:    bufkit.AlignedBytes(initPadSize/2, bufkit.CacheLineSize)[:0],
		splitBuf:        bufkit.AlignedBytes(initPadSize*2, bufkit.CacheLineSize)[:0],
		normBuf:         bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		sheafSBuf:       bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		sheafCBuf:       bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		sheafMaskBuf:    bufkit.AlignedBytes(initPadSize/8+128, bufkit.CacheLineSize)[:0],
		sheafWorkBuf:    bufkit.AlignedBytes(initPadSize+1024, bufkit.CacheLineSize)[:0],
		ctxSheafWorkBuf: bufkit.AlignedBytes(initPadSize+1024, bufkit.CacheLineSize)[:0],
		sheafFBuf: [4][]byte{
			bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
			bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
			bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
			bufkit.AlignedBytes(initPadSize, bufkit.CacheLineSize)[:0],
		},
	}
})

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

// CompressOptions configures fine-grained PRZM compression parameters.
type CompressOptions struct {
	Level              int          // 1..5
	WindowLog          int          // 16 (64K), 20 (1M), 23 (8M), or 0 (auto)
	Dict               *Dictionary  // Optional external schema dictionary
	RawDict            []byte       // Optional raw prefix history dictionary buffer
	DictID             uint32       // Optional dictionary ID for RawDict
	ArchivalMode       bool         // Explicit toggle for --mode=archival
	StandardStreamOnly bool         // Ensure canonical PRZM stream format (MagicPRZM = 0x4D5A5250)
	Threads            int          // 0 = auto (runtime.GOMAXPROCS(0)), 1 = single-threaded, >1 = explicit worker count
	ChunkSize          int          // 0 = auto (default 2 MiB), >0 = explicit chunk uncompressed byte size
	GPUAcceleration    bool         // Offload LZ77 dictionary search to NVIDIA Ampere GPU via CUDA Driver API
	MinMatch           int          // Minimum match length: 3 or 4 (default 4; 3 enables FlagMinMatch3)
	CompactDict        bool         // Enable dedicated OpcodePrefixRawDict (0xE9) for constant raw dictionary offsets
	LiteralProbe       func([]byte) // Optional probe hook for testing/diagnostics
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

func resolveConcurrencyOptions(opts CompressOptions) (threads, chunkSize int) {
	threads = opts.Threads
	if threads <= 0 {
		if opts.GPUAcceleration {
			threads = 1
		} else {
			threads = runtime.GOMAXPROCS(0)
		}
	}
	if threads < 1 {
		threads = 1
	}
	chunkSize = opts.ChunkSize
	if chunkSize <= 0 {
		if opts.GPUAcceleration || opts.Level >= 6 || opts.ArchivalMode {
			chunkSize = 8 * 1024 * 1024
		} else {
			chunkSize = DefaultChunkSize
		}
	}
	return threads, chunkSize
}

func shouldUseMultiChunk(srcLen int, opts CompressOptions) bool {
	if opts.StandardStreamOnly {
		return false
	}
	if opts.GPUAcceleration && srcLen < MaxBlockSize && opts.ChunkSize == 0 {
		return false
	}
	threads, chunkSize := resolveConcurrencyOptions(opts)
	if srcLen >= MaxBlockSize {
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
	if !opts.GPUAcceleration && os.Getenv("PRZM_GPU") == "1" {
		opts.GPUAcceleration = true
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
func CompressWithWindow(src []byte, level, windowLog int) ([]byte, error) {
	return CompressWithOptions(src, CompressOptions{Level: level, WindowLog: windowLog, StandardStreamOnly: true})
}

// Compress encodes src into PRZM format according to PRZM-SPEC-0.5.
func Compress(src []byte, level int) ([]byte, error) {
	return CompressWithOptions(src, CompressOptions{Level: level})
}

func compressInternal(src []byte, level int, customDict *Dictionary) ([]byte, error) {
	return CompressWithOptions(src, CompressOptions{Level: level, Dict: customDict})
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
		if totalSpan <= 64*1024 {
			windowLog = WindowLog64K
		} else if totalSpan <= 1024*1024 && level < 6 {
			windowLog = WindowLog1M
		} else {
			windowLog = WindowLog8M
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
		sp8M = PoolHensel8M.Get()
		defer PoolHensel8M.Put(sp8M)
		sp8M.Reset()
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

	var stride int
	var hasStride bool
	if dictLen == 0 {
		stride, hasStride = detectStride(src)
	} else {
		stride, hasStride = detectStride(origSrc)
	}
	if hasStride {
		flags |= FlagStride
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
	if opts.MinMatch == 3 {
		minMatch = 3
		flags |= FlagMinMatch3
	}
	minMatchBase := minMatch
	compactDict := opts.CompactDict && dictLen > 0

	if uSize > 65535 || windowLog > WindowLog64K {
		flags |= FlagExtOffset
	}

	// Initialize 3 CPU repeat offset registers
	rep0 := 1
	rep1 := 4
	rep2 := 8
	if hasStride && stride > 1 {
		rep1 = stride
		rep2 = 2 * stride
	}

	fastEncoder := newFastBlockEncoder(src, uSize, windowSize, minMatch, hasStride, stride, pad, &rep0, &rep1, &rep2)
	greedyEncoder := newGreedyBlockEncoder(
		src, uSize, level, windowSize, windowLog,
		minMatch, minMatchBase, compactDict, dictLen,
		hasStride, stride, activeMatcher, pad,
		sp1M, sp8M, spL6, &rep0, &rep1, &rep2, &flags,
	)

	greedyEncoder.populateDictionaryHashes()

	useFast := level <= 3 && opts.MinMatch != 3 && activeMatcher == nil
	var compressFastOrGreedy func(int, int)
	if useFast {
		compressFastOrGreedy = fastEncoder.compressBlock
	} else {
		compressFastOrGreedy = greedyEncoder.compressBlock
	}

	compressBlockGreedyOrSLP := func(bStart, bSize int) {
		cmdStart := len(pad.cmdBuf)
		offStart := len(pad.offsetBuf)
		litStart := len(pad.literalBuf)
		r0Start := rep0
		r1Start := rep1
		r2Start := rep2

		compressFastOrGreedy(bStart, bSize)

		if dictLen == 0 && bSize >= 64 && level >= 3 {
			cmdDelta := len(pad.cmdBuf) - cmdStart
			offDelta := len(pad.offsetBuf) - offStart
			litDelta := len(pad.literalBuf) - litStart
			lzCost := cmdDelta + offDelta + litDelta

			if lzCost > 32 {
				if pwsData, err := PWSEncode(src[bStart : bStart+bSize]); err == nil && len(pwsData) > 0 {
					var varBuf [8]byte
					nVar := binary.PutUvarint(varBuf[:], uint64(len(pwsData)))
					pwsCost := 1 + nVar + len(pwsData)
					if pwsCost < lzCost {
						pad.cmdBuf = pad.cmdBuf[:cmdStart]
						pad.offsetBuf = pad.offsetBuf[:offStart]
						pad.literalBuf = pad.literalBuf[:litStart]
						rep0 = r0Start
						rep1 = r1Start
						rep2 = r2Start

						pad.cmdBuf = append(pad.cmdBuf, OpGrammarBlock)
						pad.cmdBuf = append(pad.cmdBuf, varBuf[:nVar]...)
						pad.cmdBuf = append(pad.cmdBuf, pwsData...)
					}
				}
			}
		}
	}

	if dictLen > 0 {
		if level >= 4 {
			dpPad := dpScratchPool.Get()
			defer dpScratchPool.Put(dpPad)
			clear(dpPad.table3[:])

			bStart := dictLen
			for bStart < uSize {
				bSize := min(uSize-bStart, BlockSize64K)
				compressBlockOptimal(
					src,
					bStart,
					bSize,
					uSize,
					windowSize,
					windowLog,
					level,
					pad,
					dpPad,
					&rep0,
					&rep1,
					&rep2,
					sp1M,
					sp8M,
					minMatchBase,
					activeMatcher,
					dictLen,
					compactDict,
					hasStride,
					stride,
				)
				bStart += bSize
			}
		} else {
			compressFastOrGreedy(dictLen, origUSize)
		}
	} else if uSize >= 131072 {
		pad.dedup.Reset()
		var dpPad *dpScratchpad
		if level >= 4 {
			dpPad = dpScratchPool.Get()
			defer dpScratchPool.Put(dpPad)
			clear(dpPad.table3[:])
		}

		bStart := 0
		blockIdx := uint32(0)
		for bStart < uSize {
			if level >= 5 {
				rLen := DetectRLE(src, bStart)
				alignedRLen := (rLen / DedupBlockSize) * DedupBlockSize
				if alignedRLen >= DedupBlockSize {
					pad.cmdBuf = append(pad.cmdBuf, OpcodePrefixRLE)
					var vBuf [8]byte
					n := binary.PutUvarint(vBuf[:], uint64(alignedRLen))
					pad.cmdBuf = append(pad.cmdBuf, vBuf[:n]...)
					pad.literalBuf = append(pad.literalBuf, src[bStart])
					bStart += alignedRLen
					blockIdx += uint32(alignedRLen / DedupBlockSize)
					continue
				}
			}

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
					compressBlockOptimal(
						src,
						bStart,
						bSize,
						uSize,
						windowSize,
						windowLog,
						level,
						pad,
						dpPad,
						&rep0,
						&rep1,
						&rep2,
						sp1M,
						sp8M,
						minMatchBase,
						activeMatcher,
						dictLen,
						compactDict,
						hasStride,
						stride,
					)
				} else {
					compressBlockGreedyOrSLP(bStart, bSize)
				}
				pad.dedup.Insert(h, blockIdx)
			} else if bSize == DedupBlockSize && blockIdx == 0 {
				if dpPad != nil {
					compressBlockOptimal(
						src,
						bStart,
						bSize,
						uSize,
						windowSize,
						windowLog,
						level,
						pad,
						dpPad,
						&rep0,
						&rep1,
						&rep2,
						sp1M,
						sp8M,
						minMatchBase,
						activeMatcher,
						dictLen,
						compactDict,
						hasStride,
						stride,
					)
				} else {
					compressBlockGreedyOrSLP(bStart, bSize)
				}
				h := Hash64KBlock(src[bStart : bStart+DedupBlockSize])
				pad.dedup.Insert(h, 0)
			} else {
				// Tail block (< 65536 bytes) compressed via standard LZ77
				if dpPad != nil {
					compressBlockOptimal(
						src,
						bStart,
						bSize,
						uSize,
						windowSize,
						windowLog,
						level,
						pad,
						dpPad,
						&rep0,
						&rep1,
						&rep2,
						sp1M,
						sp8M,
						minMatchBase,
						activeMatcher,
						dictLen,
						compactDict,
						hasStride,
						stride,
					)
				} else {
					compressBlockGreedyOrSLP(bStart, bSize)
				}
			}
			bStart += bSize
			blockIdx++
		}
	} else {
		// Under 128 KiB: bypass deduplication entirely
		if level >= 4 {
			dpPad := dpScratchPool.Get()
			defer dpScratchPool.Put(dpPad)
			clear(dpPad.table3[:])

			bStart := 0
			for bStart < uSize {
				bSize := min(uSize-bStart, BlockSize64K)
				compressBlockOptimal(
					src,
					bStart,
					bSize,
					uSize,
					windowSize,
					windowLog,
					level,
					pad,
					dpPad,
					&rep0,
					&rep1,
					&rep2,
					sp1M,
					sp8M,
					minMatchBase,
					activeMatcher,
					dictLen,
					compactDict,
					hasStride,
					stride,
				)
				bStart += bSize
			}
		} else {
			compressBlockGreedyOrSLP(0, uSize)
		}
	}

	if opts.LiteralProbe != nil {
		opts.LiteralProbe(pad.literalBuf)
	}

	// Pack Section 1 (Literals) via Etale Sheaf V-ANS or Nibble-Planes
	litSection := packLiteralSection(pad.literalBuf, pad, level, opts, origSrc, &flags)

	// Pack Section 2 (Commands) and Section 3 (Offsets) via 4-Stream Canonical Huffman
	cmdMode := uint8(ModeHuffman4X)
	if level <= 3 {
		cmdMode = ModeFlat
	}
	cmdSection, err := PackStream(pad.cmdBuf, cmdMode)
	if err != nil {
		return nil, err
	}
	var offSection []byte
	if len(pad.offsetBuf) > 0 {
		offSection, err = PackOffsetStream(pad.offsetBuf, cmdMode)
		if err != nil {
			return nil, err
		}
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
	totalCompressedSize := HeaderSize + frameParamsSize + secDirSize + sec1Size + len(
		cmdSection,
	) + len(
		offSection,
	) + len(
		litSection,
	)

	// Fallback to uncompressed if compressed payload exceeds raw data
	if totalCompressedSize >= origUSize {
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
