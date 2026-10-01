// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package aes_distinguisher

import (
	"errors"

	"github.com/lemon4ksan/przm"
)

// StateTapPoint identifies intermediate execution points in the AES round ladder.
type StateTapPoint int

const (
	AfterAddRoundKey0 StateTapPoint = iota
	AfterSubBytes
	AfterShiftRows
	AfterMixColumns
	AfterAddRoundKey
)

// Aliases for StateTapPoint
const (
	TapAfterAddRoundKey0 = AfterAddRoundKey0
	TapAfterSubBytes     = AfterSubBytes
	TapAfterShiftRows    = AfterShiftRows
	TapAfterMixColumns   = AfterMixColumns
	TapAfterAddRoundKey  = AfterAddRoundKey
)

// KeyScheduleMode specifies how round keys are generated.
type KeyScheduleMode int

const (
	KeyScheduleStandard KeyScheduleMode = iota // NIST FIPS 197 key expansion
	KeyScheduleZero                            // All round keys zeroed
	KeyScheduleLinear                          // Kr[i] = K0[i] ^ (r * Delta)
	KeyScheduleRepeated                        // Kr = K0 for all rounds
)

// RoundState represents a 16-byte AES state matrix (column-major).
type RoundState struct {
	Block [16]byte
}

// TapCallback is invoked at intermediate state tap points if non-nil.
type TapCallback func(round int, point StateTapPoint, state *[16]byte)

// Config encapsulates configuration for reduced-round AES.
type Config struct {
	Rounds                int             // Number of rounds (1 to 10)
	IncludeLastMixColumns bool            // True if the final round should execute MixColumns
	KeySchedule           KeyScheduleMode // Key schedule mode
	Key                   []byte          // Master key (16 bytes for AES-128)
	LinearDelta           byte            // Delta increment for KeyScheduleLinear
}

var (
	sbox    [256]byte
	invSbox [256]byte
	rcon    = [11]byte{0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36}
)

func init() {
	// Dynamically generate the AES S-box using PRZM's GF(2^8) arithmetic from gfpi.go
	for i := 0; i < 256; i++ {
		inv := przm.GF256Inv(byte(i))
		s := inv
		var res byte
		for bit := 0; bit < 8; bit++ {
			b0 := (s >> bit) & 1
			b4 := (s >> ((bit + 4) % 8)) & 1
			b5 := (s >> ((bit + 5) % 8)) & 1
			b6 := (s >> ((bit + 6) % 8)) & 1
			b7 := (s >> ((bit + 7) % 8)) & 1
			cBit := byte((0x63 >> bit) & 1)
			res |= (b0 ^ b4 ^ b5 ^ b6 ^ b7 ^ cBit) << bit
		}
		sbox[i] = res
		invSbox[res] = byte(i)
	}
}

// GetSBox returns the genuine AES S-box table.
func GetSBox() [256]byte {
	return sbox
}

// GetInvSBox returns the genuine AES inverse S-box table.
func GetInvSBox() [256]byte {
	return invSbox
}

// SubBytes applies the S-box substitution to each byte of state s.
func SubBytes(s *[16]byte) {
	for i := 0; i < 16; i++ {
		s[i] = sbox[s[i]]
	}
}

// ShiftRows performs the AES cyclic row shifts on a column-major state matrix.
// Row 0: no shift
// Row 1: left shift 1
// Row 2: left shift 2
// Row 3: left shift 3
func ShiftRows(s *[16]byte) {
	r1_1, r1_5, r1_9, r1_13 := s[5], s[9], s[13], s[1]
	r2_2, r2_6, r2_10, r2_14 := s[10], s[14], s[2], s[6]
	r3_3, r3_7, r3_11, r3_15 := s[15], s[3], s[7], s[11]

	s[1], s[5], s[9], s[13] = r1_1, r1_5, r1_9, r1_13
	s[2], s[6], s[10], s[14] = r2_2, r2_6, r2_10, r2_14
	s[3], s[7], s[11], s[15] = r3_3, r3_7, r3_11, r3_15
}

// MixColumns transforms each column of state s by multiplying by the MDS matrix over GF(2^8).
// Uses PRZM's GF256Mul for field multiplications.
func MixColumns(s *[16]byte) {
	for c := 0; c < 4; c++ {
		idx := c * 4
		s0, s1, s2, s3 := s[idx], s[idx+1], s[idx+2], s[idx+3]

		s[idx] = przm.GF256Mul(0x02, s0) ^ przm.GF256Mul(0x03, s1) ^ s2 ^ s3
		s[idx+1] = s0 ^ przm.GF256Mul(0x02, s1) ^ przm.GF256Mul(0x03, s2) ^ s3
		s[idx+2] = s0 ^ s1 ^ przm.GF256Mul(0x02, s2) ^ przm.GF256Mul(0x03, s3)
		s[idx+3] = przm.GF256Mul(0x03, s0) ^ s1 ^ s2 ^ przm.GF256Mul(0x02, s3)
	}
}

// AddRoundKey XORs the round key rk into state s.
func AddRoundKey(s *[16]byte, rk *[16]byte) {
	for i := 0; i < 16; i++ {
		s[i] ^= rk[i]
	}
}

// ExpandKey derives (rounds + 1) round keys of 16 bytes each based on the selected mode.
func ExpandKey(key []byte, rounds int, mode KeyScheduleMode, linearDelta byte) [][16]byte {
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 14 {
		rounds = 14
	}
	numRoundKeys := rounds + 1
	roundKeys := make([][16]byte, numRoundKeys)

	var master [16]byte
	if len(key) >= 16 {
		copy(master[:], key[:16])
	} else if len(key) > 0 {
		copy(master[:], key)
	}

	switch mode {
	case KeyScheduleZero:
		// All round keys are initialized to zero
		return roundKeys

	case KeyScheduleRepeated:
		for r := 0; r < numRoundKeys; r++ {
			roundKeys[r] = master
		}
		return roundKeys

	case KeyScheduleLinear:
		delta := linearDelta
		if delta == 0 {
			delta = 0x01
		}
		for r := 0; r < numRoundKeys; r++ {
			for i := 0; i < 16; i++ {
				roundKeys[r][i] = master[i] ^ byte(r*int(delta))
			}
		}
		return roundKeys

	case KeyScheduleStandard:
		fallthrough
	default:
		// NIST FIPS 197 standard AES-128 key expansion
		roundKeys[0] = master

		totalWords := 4 * numRoundKeys
		w := make([]uint32, totalWords)
		for i := 0; i < 4; i++ {
			w[i] = uint32(master[4*i]) |
				(uint32(master[4*i+1]) << 8) |
				(uint32(master[4*i+2]) << 16) |
				(uint32(master[4*i+3]) << 24)
		}

		for i := 4; i < totalWords; i++ {
			temp := w[i-1]
			if i%4 == 0 {
				// RotWord: cyclic left shift of bytes
				b0 := byte(temp)
				b1 := byte(temp >> 8)
				b2 := byte(temp >> 16)
				b3 := byte(temp >> 24)
				// Rotated: (b1, b2, b3, b0)
				// SubWord: apply S-box
				sb0 := uint32(sbox[b1])
				sb1 := uint32(sbox[b2]) << 8
				sb2 := uint32(sbox[b3]) << 16
				sb3 := uint32(sbox[b0]) << 24
				subRot := sb0 | sb1 | sb2 | sb3

				rconVal := uint32(0)
				rconIdx := i / 4
				if rconIdx < len(rcon) {
					rconVal = uint32(rcon[rconIdx])
				}
				temp = subRot ^ rconVal
			}
			w[i] = w[i-4] ^ temp
		}

		for r := 0; r < numRoundKeys; r++ {
			for c := 0; c < 4; c++ {
				val := w[4*r+c]
				roundKeys[r][4*c] = byte(val)
				roundKeys[r][4*c+1] = byte(val >> 8)
				roundKeys[r][4*c+2] = byte(val >> 16)
				roundKeys[r][4*c+3] = byte(val >> 24)
			}
		}
		return roundKeys
	}
}

// EncryptBlock executes reduced-round AES on a single 16-byte block with intermediate tap support.
func EncryptBlock(plaintext, key [16]byte, rounds int, finalMixCol bool, mode KeyScheduleMode, linearDelta byte, tap TapCallback) [16]byte {
	if rounds < 1 {
		rounds = 1
	}

	roundKeys := ExpandKey(key[:], rounds, mode, linearDelta)
	state := plaintext

	// Round 0: Initial AddRoundKey
	AddRoundKey(&state, &roundKeys[0])
	if tap != nil {
		tap(0, AfterAddRoundKey0, &state)
	}

	// Rounds 1 to rounds
	for r := 1; r <= rounds; r++ {
		SubBytes(&state)
		if tap != nil {
			tap(r, AfterSubBytes, &state)
		}

		ShiftRows(&state)
		if tap != nil {
			tap(r, AfterShiftRows, &state)
		}

		isLastRound := (r == rounds)
		if !isLastRound || finalMixCol {
			MixColumns(&state)
			if tap != nil {
				tap(r, AfterMixColumns, &state)
			}
		}

		AddRoundKey(&state, &roundKeys[r])
		if tap != nil {
			tap(r, AfterAddRoundKey, &state)
		}
	}

	return state
}

// EncryptReducedRound encrypts a plaintext block with standard key schedule and optional final MixColumns.
func EncryptReducedRound(plaintext, key []byte, rounds int, finalMixCol bool) [16]byte {
	var pt, k [16]byte
	if len(plaintext) > 0 {
		copy(pt[:], plaintext)
	}
	if len(key) > 0 {
		copy(k[:], key)
	}
	return EncryptBlock(pt, k, rounds, finalMixCol, KeyScheduleStandard, 0, nil)
}

// EncryptWithTap runs reduced-round AES and returns the intermediate state at the specified tap point in the final round (or round 0).
func EncryptWithTap(plaintext, key []byte, rounds int, tap StateTapPoint) [16]byte {
	var pt, k [16]byte
	if len(plaintext) > 0 {
		copy(pt[:], plaintext)
	}
	if len(key) > 0 {
		copy(k[:], key)
	}

	if rounds < 1 {
		rounds = 1
	}
	if rounds > 14 {
		rounds = 14
	}

	var tappedState [16]byte
	targetRound := rounds
	if tap == AfterAddRoundKey0 {
		targetRound = 0
	}

	finalMix := (tap == AfterMixColumns || tap == TapAfterMixColumns)
	EncryptBlock(pt, k, rounds, finalMix, KeyScheduleStandard, 0, func(r int, p StateTapPoint, s *[16]byte) {
		if r == targetRound && p == tap {
			tappedState = *s
		}
	})

	return tappedState
}

// Engine implements configurable reduced-round AES stream processing.
type Engine struct {
	cfg       Config
	roundKeys [][16]byte
}

// NewEngine creates a new reduced-round AES engine with precomputed key schedules.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Rounds < 1 || cfg.Rounds > 14 {
		return nil, errors.New("aes_distinguisher: rounds must be between 1 and 14")
	}
	rks := ExpandKey(cfg.Key, cfg.Rounds, cfg.KeySchedule, cfg.LinearDelta)
	return &Engine{
		cfg:       cfg,
		roundKeys: rks,
	}, nil
}

// EncryptBlock processes a single 16-byte block.
func (e *Engine) EncryptBlock(dst, src []byte, tap TapCallback) {
	var state [16]byte
	copy(state[:], src[:16])

	AddRoundKey(&state, &e.roundKeys[0])
	if tap != nil {
		tap(0, AfterAddRoundKey0, &state)
	}

	for r := 1; r <= e.cfg.Rounds; r++ {
		SubBytes(&state)
		if tap != nil {
			tap(r, AfterSubBytes, &state)
		}

		ShiftRows(&state)
		if tap != nil {
			tap(r, AfterShiftRows, &state)
		}

		isLastRound := (r == e.cfg.Rounds)
		if !isLastRound || e.cfg.IncludeLastMixColumns {
			MixColumns(&state)
			if tap != nil {
				tap(r, AfterMixColumns, &state)
			}
		}

		AddRoundKey(&state, &e.roundKeys[r])
		if tap != nil {
			tap(r, AfterAddRoundKey, &state)
		}
	}

	copy(dst[:16], state[:])
}

// EncryptStream encrypts src into dst (both must be multiples of 16 bytes).
func (e *Engine) EncryptStream(dst, src []byte) error {
	if len(src)%16 != 0 || len(dst) < len(src) {
		return errors.New("aes_distinguisher: buffer length must be multiple of 16")
	}
	blocks := len(src) / 16
	for i := 0; i < blocks; i++ {
		offset := i * 16
		e.EncryptBlock(dst[offset:offset+16], src[offset:offset+16], nil)
	}
	return nil
}
