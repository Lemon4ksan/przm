// Package przm provides the pure synthesized PRZM compression engine.
// Section 2 command operations are decoupled into core/commands.
package przm

import (
	"github.com/lemon4ksan/przm/core/commands"
)

// MetaModel represents the precomputed frequency, CDF, and inversion tables for one context.
type MetaModel = commands.MetaModel

// SplitCommands decomposes linear bytecode instructions into opStream, metaStream, and varStream.
func SplitCommands(src []byte, op, meta, v []byte) (nOp, nM, nV int, err error) {
	return commands.SplitCommands(src, op, meta, v)
}

// JoinCommands reconstitutes a linear bytecode sequence from opStream, metaStream, and varStream.
func JoinCommands(dst, op, meta, v []byte, origLen int) error {
	return commands.JoinCommands(dst, op, meta, v, origLen)
}

// PackCommandsMode8 serializes opStream, metaStream, and varStream into a 13-byte Mode 8 frame.
func PackCommandsMode8(op, meta, v []byte, origLen int, dst []byte) (int, error) {
	return commands.PackCommandsMode8(op, meta, v, origLen, dst)
}

// UnpackCommandsMode8 parses a Mode 8 frame, extracting origLen and substreams into op, meta, v.
func UnpackCommandsMode8(src []byte, op, meta, v []byte) (origLen, nOp, nM, nV int, err error) {
	return commands.UnpackCommandsMode8(src, op, meta, v)
}

// ContextMeta maps a preceding byte p to an 8-context category [0..7].
func ContextMeta(p byte) int {
	return commands.ContextMeta(p)
}

// EncodeVansMeta8 encodes src into dst using 8-context Bigram V-ANS with 4 interleaved lanes.
func EncodeVansMeta8(src, dst []byte) (int, error) {
	return commands.EncodeVansMeta8(src, dst)
}

// DecodeVansMeta8 decodes an 8-context Bigram V-ANS stream from src into dst.
func DecodeVansMeta8(src, dst []byte, expectedLen int) error {
	return commands.DecodeVansMeta8(src, dst, expectedLen)
}
