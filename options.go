// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"errors"
	"sync"
)

// Standard error definitions for przm package.
var (
	ErrCorruptedFrame  = errors.New("przm: corrupted frame")
	ErrInvalidMagic    = errors.New("przm: invalid magic")
	ErrOffsetOOB       = errors.New("przm: offset out of bounds")
	ErrBufferTooSmall  = errors.New("przm: destination buffer too small")
	ErrExceedsMaxBlock = errors.New("przm: exceeds max block size")
	ErrUnsupportedMode = errors.New("przm: unsupported compression mode")
	ErrUnknownDict     = errors.New("przm: unknown dictionary")
)

// Options configures compression and decompression execution parameters.
type Options struct {
	Level              int
	Threads            int
	WindowBits         int
	WindowLog          int
	ArchivalMode       bool
	StandardStreamOnly bool
	Dict               *DictionaryDesc
	RawDict            []byte
	DictID             uint32
	GPUAcceleration    bool
	ChunkSize          int
}

// CompressOptions is an alias for Options to maintain full backwards compatibility.
type CompressOptions = Options

// Option applies a configuration mutator to an Options instance.
type Option func(*Options)

// WithLevel configures the Trellis RDO search depth / compression level (1-12).
func WithLevel(level int) Option { return func(o *Options) { o.Level = level } }

// WithDict injects a raw byte slice as external dictionary.
func WithDict(dict []byte) Option { return func(o *Options) { o.RawDict = dict } }

// WithDictDesc injects a structured external DictionaryDesc descriptor.
func WithDictDesc(desc *DictionaryDesc) Option {
	return func(o *Options) {
		o.Dict = desc
		if desc != nil {
			o.DictID = desc.DictID
		}
	}
}

// WithWindowBits sets the sliding window horizon exponent (log2).
func WithWindowBits(bits int) Option {
	return func(o *Options) { o.WindowBits = bits; o.WindowLog = bits }
}

// WithWindowLog sets the window logarithm.
func WithWindowLog(wLog int) Option {
	return func(o *Options) { o.WindowLog = wLog; o.WindowBits = wLog }
}

// WithThreads configures concurrency for multi-block compression pipelines.
func WithThreads(threads int) Option { return func(o *Options) { o.Threads = threads } }

// WithArchivalMode toggles archival-level maximum ratio tuning.
func WithArchivalMode(archival bool) Option { return func(o *Options) { o.ArchivalMode = archival } }

// WithStandardStreamOnly restricts output strictly to standard framing.
func WithStandardStreamOnly(standard bool) Option {
	return func(o *Options) { o.StandardStreamOnly = standard }
}

// WithChunkSize configures the chunk size for multi-chunk streaming/GPU pipelines.
func WithChunkSize(chunkSize int) Option { return func(o *Options) { o.ChunkSize = chunkSize } }

// WithGPUAcceleration toggles GPU acceleration preference.
func WithGPUAcceleration(gpu ...bool) Option {
	return func(o *Options) {
		if len(gpu) == 0 || gpu[0] {
			o.GPUAcceleration = true
		} else {
			o.GPUAcceleration = false
		}
	}
}

// DefaultOptions returns the baseline configuration for optimal compression speed & ratio.
func DefaultOptions() Options {
	return Options{
		Level:      6,
		Threads:    1,
		WindowBits: 18,
		WindowLog:  18,
	}
}

// ApplyOptions computes the resolved Options from an optional list of functional options.
func ApplyOptions(opts ...Option) Options {
	cfg := DefaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return cfg
}

// ApplyOptionOrStruct extracts Options from either functional options or struct inputs.
func ApplyOptionOrStruct(opts ...any) Options {
	cfg := DefaultOptions()
	for _, opt := range opts {
		applySingleOption(&cfg, opt)
	}
	syncWindowParameters(&cfg)
	return cfg
}

func applySingleOption(cfg *Options, opt any) {
	switch v := opt.(type) {
	case Option:
		if v != nil {
			v(cfg)
		}
	case func(*Options):
		if v != nil {
			v(cfg)
		}
	case Options:
		*cfg = v
	case *Options:
		if v != nil {
			*cfg = *v
		}
	}
}

func syncWindowParameters(cfg *Options) {
	if cfg.WindowLog > 0 && cfg.WindowBits == 18 {
		cfg.WindowBits = cfg.WindowLog
	}
	if cfg.WindowBits > 0 && cfg.WindowLog == 0 {
		cfg.WindowLog = cfg.WindowBits
	}
}

// DictEntry represents an entry within an external dictionary.
type DictEntry struct {
	Data   string
	Length int
}

// DictionaryDesc provides an explicit descriptor for dynamic external dictionary injection.
type DictionaryDesc struct {
	DictID       uint32
	TransformIdx int
	NumEntries   int
	Entries      []DictEntry
}

// Dictionary abstracts external dictionary retrieval with zero hardcoded engine tokens.
type Dictionary interface {
	ID() uint32
	Lookup(idx int) ([]byte, bool)
	NumEntries() int
}

// NewDictionaryDesc constructs a dynamic DictionaryDesc descriptor from string entries.
func NewDictionaryDesc(dictID uint32, entries []string) *DictionaryDesc {
	d := &DictionaryDesc{
		DictID:     dictID,
		NumEntries: len(entries),
		Entries:    make([]DictEntry, len(entries)),
	}
	for i, s := range entries {
		d.Entries[i] = DictEntry{Data: s, Length: len(s)}
	}
	return d
}

var (
	rawDictMu       sync.RWMutex
	rawDictRegistry = make(map[uint32][]byte)
)

// RegisterRawDictionary registers a raw prefix dictionary buffer under a 32-bit dictID.
func RegisterRawDictionary(id uint32, data []byte) {
	rawDictMu.Lock()
	defer rawDictMu.Unlock()
	rawDictRegistry[id] = data
}

// UnregisterRawDictionary removes a raw dictionary from the registry.
func UnregisterRawDictionary(id uint32) {
	rawDictMu.Lock()
	defer rawDictMu.Unlock()
	delete(rawDictRegistry, id)
}

// GetRawDictionary retrieves a registered raw dictionary buffer by id.
func GetRawDictionary(id uint32) []byte {
	rawDictMu.RLock()
	defer rawDictMu.RUnlock()
	return rawDictRegistry[id]
}
