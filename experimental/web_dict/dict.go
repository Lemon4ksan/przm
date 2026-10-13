// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package web_dict

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unsafe"
)

// Wire Protocol Constants
var (
	MagicWebDict = [7]byte{'P', 'R', 'Z', 'M', '_', 'W', 'D'}
	WireVersion1 = uint8(0x01)
)

var (
	ErrMalformedHeader      = errors.New("przm/web_dict: malformed stream header")
	ErrDictionaryMismatch   = errors.New("przm/web_dict: dictionary ID or digest mismatch")
	ErrCorruptedStream      = errors.New("przm/web_dict: corrupted token stream")
	ErrBufferOverflow       = errors.New("przm/web_dict: destination buffer overflow")
	ErrSizeMismatch         = errors.New("przm/web_dict: decompressed size mismatch")
	ErrRFC9651InvalidSyntax = errors.New("przm/web_dict: invalid RFC 9651 structured field syntax")
)

// -----------------------------------------------------------------------------
// 1. RFC 9651 Structured Field Values Parser & Formatter
// -----------------------------------------------------------------------------

// ItemType defines RFC 9651 item types.
type ItemType int

const (
	TypeToken ItemType = iota
	TypeString
	TypeByteSeq
	TypeInteger
	TypeDecimal
	TypeBoolean
)

// Parameter represents a parameter in a Structured Field (key=value).
type Parameter struct {
	Key   string
	Value Item
}

// Item represents a Structured Field Item with optional parameters.
type Item struct {
	Type       ItemType
	TokenVal   string
	StringVal  string
	BytesVal   []byte
	IntVal     int64
	DecimalVal float64
	BoolVal    bool
	Parameters []Parameter
}

// DictEntry represents a key-value member of an RFC 9651 Dictionary.
type DictEntry struct {
	Key  string
	Item Item
}

// ParseItem parses an RFC 9651 Structured Field Item.
func ParseItem(s string) (Item, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return Item{}, fmt.Errorf("%w: empty item", ErrRFC9651InvalidSyntax)
	}

	// Split parameters if present
	parts := strings.Split(s, ";")
	itemPart := strings.TrimSpace(parts[0])

	var item Item
	if strings.HasPrefix(itemPart, ":") && strings.HasSuffix(itemPart, ":") {
		// Byte Sequence: :base64:
		b64 := itemPart[1 : len(itemPart)-1]
		dec, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return Item{}, fmt.Errorf("%w: invalid base64 byte sequence", ErrRFC9651InvalidSyntax)
		}
		item.Type = TypeByteSeq
		item.BytesVal = dec
	} else if strings.HasPrefix(itemPart, "\"") && strings.HasSuffix(itemPart, "\"") {
		// String: "..."
		strContent := itemPart[1 : len(itemPart)-1]
		strContent = strings.ReplaceAll(strContent, `\"`, `"`)
		strContent = strings.ReplaceAll(strContent, `\\`, `\`)
		item.Type = TypeString
		item.StringVal = strContent
	} else if strings.HasPrefix(itemPart, "?") {
		// Boolean: ?1 or ?0
		if itemPart == "?1" {
			item.Type = TypeBoolean
			item.BoolVal = true
		} else if itemPart == "?0" {
			item.Type = TypeBoolean
			item.BoolVal = false
		} else {
			return Item{}, fmt.Errorf("%w: invalid boolean value", ErrRFC9651InvalidSyntax)
		}
	} else if iv, err := strconv.ParseInt(itemPart, 10, 64); err == nil {
		// Integer
		item.Type = TypeInteger
		item.IntVal = iv
	} else if fv, err := strconv.ParseFloat(itemPart, 64); err == nil && strings.Contains(itemPart, ".") {
		// Decimal
		item.Type = TypeDecimal
		item.DecimalVal = fv
	} else {
		// Token (ident)
		item.Type = TypeToken
		item.TokenVal = itemPart
	}

	// Parse parameters
	for i := 1; i < len(parts); i++ {
		paramStr := strings.TrimSpace(parts[i])
		if len(paramStr) == 0 {
			continue
		}
		pEq := strings.IndexByte(paramStr, '=')
		if pEq == -1 {
			// Parameter without value is boolean true (?1)
			item.Parameters = append(item.Parameters, Parameter{
				Key:   paramStr,
				Value: Item{Type: TypeBoolean, BoolVal: true},
			})
		} else {
			pKey := strings.TrimSpace(paramStr[:pEq])
			pValStr := strings.TrimSpace(paramStr[pEq+1:])
			pVal, err := ParseItem(pValStr)
			if err != nil {
				return Item{}, err
			}
			item.Parameters = append(item.Parameters, Parameter{Key: pKey, Value: pVal})
		}
	}

	return item, nil
}

// FormatItem formats an RFC 9651 Item into structured field string.
func FormatItem(item Item) string {
	var sb strings.Builder
	switch item.Type {
	case TypeByteSeq:
		sb.WriteByte(':')
		sb.WriteString(base64.StdEncoding.EncodeToString(item.BytesVal))
		sb.WriteByte(':')
	case TypeString:
		sb.WriteByte('"')
		escaped := strings.ReplaceAll(item.StringVal, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		sb.WriteString(escaped)
		sb.WriteByte('"')
	case TypeBoolean:
		if item.BoolVal {
			sb.WriteString("?1")
		} else {
			sb.WriteString("?0")
		}
	case TypeInteger:
		sb.WriteString(strconv.FormatInt(item.IntVal, 10))
	case TypeDecimal:
		sb.WriteString(strconv.FormatFloat(item.DecimalVal, 'f', 3, 64))
	case TypeToken:
		sb.WriteString(item.TokenVal)
	}

	for _, p := range item.Parameters {
		sb.WriteByte(';')
		sb.WriteString(p.Key)
		if p.Value.Type == TypeBoolean && p.Value.BoolVal {
			continue // Omit =?1
		}
		sb.WriteByte('=')
		sb.WriteString(FormatItem(p.Value))
	}
	return sb.String()
}

// ParseDictionary parses an RFC 9651 Dictionary header (comma-separated key-value pairs).
func ParseDictionary(s string) ([]DictEntry, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	entries := make([]DictEntry, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) == 0 {
			continue
		}
		eq := strings.IndexByte(part, '=')
		if eq == -1 {
			// Boolean flag
			item, err := ParseItem("?1")
			if err != nil {
				return nil, err
			}
			entries = append(entries, DictEntry{Key: part, Item: item})
		} else {
			key := strings.TrimSpace(part[:eq])
			valStr := strings.TrimSpace(part[eq+1:])
			item, err := ParseItem(valStr)
			if err != nil {
				return nil, err
			}
			entries = append(entries, DictEntry{Key: key, Item: item})
		}
	}
	return entries, nil
}

// FormatDictionary formats a list of DictEntries into an RFC 9651 Dictionary header.
func FormatDictionary(entries []DictEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		if e.Item.Type == TypeBoolean && e.Item.BoolVal && len(e.Item.Parameters) == 0 {
			parts[i] = e.Key
		} else {
			parts[i] = fmt.Sprintf("%s=%s", e.Key, FormatItem(e.Item))
		}
	}
	return strings.Join(parts, ", ")
}

// ParseList parses an RFC 9651 List header.
func ParseList(s string) ([]Item, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	list := make([]Item, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) == 0 {
			continue
		}
		item, err := ParseItem(part)
		if err != nil {
			return nil, err
		}
		list = append(list, item)
	}
	return list, nil
}

// FormatList formats a list of Items into an RFC 9651 List header.
func FormatList(items []Item) string {
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = FormatItem(item)
	}
	return strings.Join(parts, ", ")
}

// Header Binding Helpers:
// Use-Dictionary: :<sha256>:; id="przm:rfc7932:v1"
func ParseUseDictionary(header string) ([]byte, string, error) {
	item, err := ParseItem(header)
	if err != nil {
		return nil, "", err
	}
	if item.Type != TypeByteSeq {
		return nil, "", fmt.Errorf("%w: Use-Dictionary must be a Byte Sequence", ErrRFC9651InvalidSyntax)
	}
	var id string
	for _, p := range item.Parameters {
		if p.Key == "id" {
			id = p.Value.StringVal
			if id == "" {
				id = p.Value.TokenVal
			}
		}
	}
	return item.BytesVal, id, nil
}

func FormatUseDictionary(sha256Digest []byte, id string) string {
	item := Item{
		Type:     TypeByteSeq,
		BytesVal: sha256Digest,
		Parameters: []Parameter{
			{Key: "id", Value: Item{Type: TypeString, StringVal: id}},
		},
	}
	return FormatItem(item)
}

// Sec-Available-Dictionary: :<hash1>:, :<hash2>:
func ParseSecAvailableDictionary(header string) ([][]byte, error) {
	items, err := ParseList(header)
	if err != nil {
		return nil, err
	}
	hashes := make([][]byte, 0, len(items))
	for _, item := range items {
		if item.Type == TypeByteSeq {
			hashes = append(hashes, item.BytesVal)
		}
	}
	return hashes, nil
}

func FormatSecAvailableDictionary(hashes [][]byte) string {
	items := make([]Item, len(hashes))
	for i, h := range hashes {
		items[i] = Item{Type: TypeByteSeq, BytesVal: h}
	}
	return FormatList(items)
}

// Dictionary-ID: "przm:rfc7932:v1"
func ParseDictionaryID(header string) (string, error) {
	item, err := ParseItem(header)
	if err != nil {
		return "", err
	}
	if item.Type == TypeString {
		return item.StringVal, nil
	}
	if item.Type == TypeToken {
		return item.TokenVal, nil
	}
	return "", fmt.Errorf("%w: Dictionary-ID must be a String or Token", ErrRFC9651InvalidSyntax)
}

func FormatDictionaryID(id string) string {
	return FormatItem(Item{Type: TypeString, StringVal: id})
}

// -----------------------------------------------------------------------------
// 2. Stratified H-DAG Rules & Web Dictionary Container
// -----------------------------------------------------------------------------

// RuleL1 represents a 2-byte digram rule.
type RuleL1 struct {
	U byte
	V byte
}

// RuleL2 represents a 4-byte quadgram rule (pair of L1 rules).
type RuleL2 struct {
	L1A uint8
	L1B uint8
}

// RuleL3 represents an 8-byte octagram rule (pair of L2 rules).
type RuleL3 struct {
	L2A uint8
	L2B uint8
}

// WebDictionary encapsulates pre-baked static dictionary strings stratified into an H-DAG.
type WebDictionary struct {
	ID       string
	SHA256   [32]byte
	RawBytes []byte
	L1       []RuleL1
	L2       []RuleL2
	L3       []RuleL3
	Phrases  [][]byte

	// Precomputed flat SIMD vector expansion tables (up to 256 rules per layer)
	ExpL1 [256]uint16
	ExpL2 [256]uint32
	ExpL3 [256]uint64

	// O(1) Fast Match Tables
	L1Direct  [65536]uint16     // 0 = none, (rule_id + 1)
	L2Map     map[uint32]uint8  // 32-bit quadgram -> rule_id
	L3Map     map[uint64]uint8  // 64-bit octagram -> rule_id
	PhraseMap map[string]uint16 // phrase string -> phrase_id
}

// BuildExpansionTables precalculates 16-bit, 32-bit, and 64-bit values for zero-stack SIMD expansion.
func (d *WebDictionary) BuildExpansionTables() {
	d.L2Map = make(map[uint32]uint8, len(d.L2))
	d.L3Map = make(map[uint64]uint8, len(d.L3))
	d.PhraseMap = make(map[string]uint16, len(d.Phrases))

	// Layer 1 (2 bytes)
	for i, r := range d.L1 {
		val := uint16(r.U) | (uint16(r.V) << 8)
		d.ExpL1[i] = val
		d.L1Direct[val] = uint16(i + 1)
	}

	// Layer 2 (4 bytes = 2 * L1)
	for j, r := range d.L2 {
		w0 := uint32(d.ExpL1[r.L1A])
		w1 := uint32(d.ExpL1[r.L1B])
		val := w0 | (w1 << 16)
		d.ExpL2[j] = val
		d.L2Map[val] = uint8(j)
	}

	// Layer 3 (8 bytes = 2 * L2)
	for k, r := range d.L3 {
		d0 := uint64(d.ExpL2[r.L2A])
		d1 := uint64(d.ExpL2[r.L2B])
		val := d0 | (d1 << 32)
		d.ExpL3[k] = val
		d.L3Map[val] = uint8(k)
	}

	// Phrases (variable length >= 9 bytes)
	for p, phrase := range d.Phrases {
		d.PhraseMap[string(phrase)] = uint16(p)
	}
}

// -----------------------------------------------------------------------------
// 3. RFC 7932 Brotli Web Dictionary Slice & Token Ingestion
// -----------------------------------------------------------------------------

// LoadBrotliWebDictionary ingests canonical RFC 7932 Brotli web substrings and stratifies them.
func LoadBrotliWebDictionary() *WebDictionary {
	dict := &WebDictionary{
		ID: "przm:rfc7932:v1",
	}

	// 1. Layer 1: Canonical 2-byte web digrams
	l1Strings := []string{
		`">`, `</`, `="`, `px`, `js`, `id`, `in`, `to`, `if`, `on`,
		`or`, `":`, `",`, `{}`, `[]`, `()`, `/*`, `*/`, `//`, "\r\n",
		`':`, `',`, ` {`, `: `, `="`, `";`, `';`, `/>`, `<!`, `->`,
		`th`, `er`, `on`, `an`, `re`, `he`, `in`, `ed`, `nd`, `ha`,
		`at`, `en`, `es`, `of`, `or`, `nt`, `ea`, `ti`, `to`, `it`,
		`st`, `io`, `le`, `is`, `ou`, `ar`, `as`, `de`, `rt`, `ve`,
	}
	dict.L1 = make([]RuleL1, len(l1Strings))
	for i, s := range l1Strings {
		dict.L1[i] = RuleL1{U: s[0], V: s[1]}
	}

	// 2. Layer 2: 4-byte quadgrams (built from L1 rules)
	// Helper to find or add L1 rule
	findL1 := func(u, v byte) uint8 {
		for idx, r := range dict.L1 {
			if r.U == u && r.V == v {
				return uint8(idx)
			}
		}
		newIdx := len(dict.L1)
		dict.L1 = append(dict.L1, RuleL1{U: u, V: v})
		return uint8(newIdx)
	}

	l2Strings := []string{
		"<div", "div>", "span", "href", "func", "tion", "true", "null",
		"text", "type", "post", "body", "head", "meta", "http", "data",
		"form", "html", "link", "size", "time", "date", "name", "file",
		"path", "user", "pass", "auth", "icon", "auto", "font", "left",
		"item", "open", "send", "load", "next", "prev", "last", "done",
		"fail", "code", "show", "hide", "view", "edit", "save", "read",
	}

	dict.L2 = make([]RuleL2, len(l2Strings))
	for j, s := range l2Strings {
		l1a := findL1(s[0], s[1])
		l1b := findL1(s[2], s[3])
		dict.L2[j] = RuleL2{L1A: l1a, L1B: l1b}
	}

	// 3. Layer 3: 8-byte octagrams (built from L2 rules)
	findL2 := func(w [4]byte) uint8 {
		val := binary.LittleEndian.Uint32(w[:])
		for idx, r := range dict.L2 {
			w0 := uint32(dict.L1[r.L1A].U) | (uint32(dict.L1[r.L1A].V) << 8)
			w1 := uint32(dict.L1[r.L1B].U) | (uint32(dict.L1[r.L1B].V) << 8)
			rVal := w0 | (w1 << 16)
			if rVal == val {
				return uint8(idx)
			}
		}
		// Add new L2
		l1a := findL1(w[0], w[1])
		l1b := findL1(w[2], w[3])
		newIdx := len(dict.L2)
		dict.L2 = append(dict.L2, RuleL2{L1A: l1a, L1B: l1b})
		return uint8(newIdx)
	}

	l3Strings := []string{
		"function", "<!DOCTYP", "E html> ", "styleshe", "et\" href",
		"\"status\"", "\"succes\"", "s\":true,", "applicat", "ion/json",
		"document", "window.l", "console.", "log(\"err", "or: {\"co",
		"de\":200,", "\"message", "\"code\":2", "display:", "flex; ma",
		"rgin: 0;", "padding:", " 0; box-", "sizing: ", "border-b",
		"ox; posi", "tion: ab", "solute; ", "color: #", "fff; bac",
		"kground-", "rgba(0, ", "font-siz", "e: 14px ", "text-ali",
	}

	dict.L3 = make([]RuleL3, len(l3Strings))
	for k, s := range l3Strings {
		if len(s) != 8 {
			panic(fmt.Sprintf("l3Strings[%d] (%q) length is %d, want 8", k, s, len(s)))
		}
		var w0, w1 [4]byte
		copy(w0[:], s[0:4])
		copy(w1[:], s[4:8])
		l2a := findL2(w0)
		l2b := findL2(w1)
		dict.L3[k] = RuleL3{L2A: l2a, L2B: l2b}
	}

	// 4. Layer 4: Extended Web-Dict Phrases (>= 9 bytes)
	dict.Phrases = [][]byte{
		[]byte("<!DOCTYPE html>"),
		[]byte("<meta charset=\"utf-8\">"),
		[]byte("<link rel=\"stylesheet\" href=\""),
		[]byte("<script type=\"text/javascript\">"),
		[]byte("application/json"),
		[]byte("application/javascript"),
		[]byte("addEventListener(\"click\","),
		[]byte("document.getElementById("),
		[]byte("document.querySelector("),
		[]byte("JSON.stringify("),
		[]byte("JSON.parse("),
		[]byte("display: inline-block;"),
		[]byte("box-sizing: border-box;"),
		[]byte("background-color: "),
		[]byte("\"status\": 200,"),
		[]byte("\"success\": true,"),
		[]byte("\x00asm\x01\x00\x00\x00"), // WebAssembly magic header
	}

	// Build flat expansion tables
	dict.BuildExpansionTables()

	// Compute canonical SHA-256
	h := sha256.New()
	for _, r := range dict.L1 {
		h.Write([]byte{r.U, r.V})
	}
	for _, r := range dict.L2 {
		h.Write([]byte{r.L1A, r.L1B})
	}
	for _, r := range dict.L3 {
		h.Write([]byte{r.L2A, r.L2B})
	}
	for _, p := range dict.Phrases {
		h.Write(p)
	}
	copy(dict.SHA256[:], h.Sum(nil))

	return dict
}

// -----------------------------------------------------------------------------
// 4. Roundtrip Compression & Decompression Engine
// -----------------------------------------------------------------------------

// CompressWebDict compresses src using the pre-baked H-DAG web dictionary.
func CompressWebDict(src []byte, dict *WebDictionary) ([]byte, error) {
	n := len(src)
	if n == 0 {
		return []byte{}, nil
	}
	if dict == nil {
		return nil, errors.New("przm/web_dict: nil dictionary")
	}

	// Axiom token emission buffer
	axiom := make([]byte, 0, n)
	pos := 0

	// O(1) Hierarchical Greedily Stratified Matching Loop
	for pos < n {
		rem := n - pos

		// 1. Probe Phrases (longest match first: 32 down to 9 bytes)
		if rem >= 9 {
			matched := false
			for pIdx, phrase := range dict.Phrases {
				pLen := len(phrase)
				if rem >= pLen && bytes.Equal(src[pos:pos+pLen], phrase) {
					if pIdx < 8 {
						axiom = append(axiom, uint8(0xF0+pIdx))
					} else {
						var idBuf [2]byte
						binary.LittleEndian.PutUint16(idBuf[:], uint16(pIdx))
						axiom = append(axiom, 0xFB, idBuf[0], idBuf[1])
					}
					pos += pLen
					matched = true
					break
				}
			}
			if matched {
				continue
			}
		}

		// 2. Probe Layer 3 (8 bytes)
		if rem >= 8 {
			octa := binary.LittleEndian.Uint64(src[pos : pos+8])
			if l3ID, ok := dict.L3Map[octa]; ok {
				if l3ID < 16 {
					axiom = append(axiom, 0xE0+l3ID)
				} else {
					axiom = append(axiom, 0xFA, l3ID)
				}
				pos += 8
				continue
			}
		}

		// 3. Probe Layer 2 (4 bytes)
		if rem >= 4 {
			quad := binary.LittleEndian.Uint32(src[pos : pos+4])
			if l2ID, ok := dict.L2Map[quad]; ok {
				if l2ID < 16 {
					axiom = append(axiom, 0xD0+l2ID)
				} else {
					axiom = append(axiom, 0xF9, l2ID)
				}
				pos += 4
				continue
			}
		}

		// 4. Probe Layer 1 (2 bytes)
		if rem >= 2 {
			di := binary.LittleEndian.Uint16(src[pos : pos+2])
			entry := dict.L1Direct[di]
			if entry > 0 {
				l1ID := uint8(entry - 1)
				if l1ID < 16 {
					axiom = append(axiom, 0xC0+l1ID)
				} else {
					axiom = append(axiom, 0xF8, l1ID)
				}
				pos += 2
				continue
			}
		}

		// 5. Raw Terminal Byte
		b := src[pos]
		pos++
		if b < 0xC0 {
			axiom = append(axiom, b)
		} else {
			axiom = append(axiom, 0xFF, b)
		}
	}

	// 26-Byte Framing Header:
	// Magic(7B) | Version(1B) | Flags(2B) | TruncatedDigest(8B) | OrigSize(4B) | AxiomLen(4B)
	out := make([]byte, 26+len(axiom))
	copy(out[0:7], MagicWebDict[:])
	out[7] = WireVersion1
	binary.LittleEndian.PutUint16(out[8:10], 0x0001) // Flag: GroundedWebDict
	copy(out[10:18], dict.SHA256[:8])                // 8-byte cryptographic digest prefix
	binary.LittleEndian.PutUint32(out[18:22], uint32(n))
	binary.LittleEndian.PutUint32(out[22:26], uint32(len(axiom)))
	copy(out[26:], axiom)

	return out, nil
}

// DecompressWebDict decompresses a stream encoded with CompressWebDict.
func DecompressWebDict(src []byte, dict *WebDictionary) ([]byte, error) {
	if len(src) == 0 {
		return []byte{}, nil
	}
	if len(src) < 26 {
		return nil, ErrMalformedHeader
	}
	if !bytes.Equal(src[0:7], MagicWebDict[:]) || src[7] != WireVersion1 {
		return nil, ErrMalformedHeader
	}
	if dict == nil {
		return nil, errors.New("przm/web_dict: nil dictionary")
	}

	// Verify cryptographic DictID digest prefix
	if !bytes.Equal(src[10:18], dict.SHA256[:8]) {
		return nil, ErrDictionaryMismatch
	}

	origSize := int(binary.LittleEndian.Uint32(src[18:22]))
	dst := make([]byte, origSize+16) // 16 bytes safety padding for 64-bit vector stores

	written, err := DecompressWebDictTo(src, dst, dict)
	if err != nil {
		return nil, err
	}
	return dst[:written], nil
}

// DecompressWebDictTo decompresses directly into caller-provided dst with 0 allocations.
func DecompressWebDictTo(src, dst []byte, dict *WebDictionary) (int, error) {
	if len(src) == 0 {
		return 0, nil
	}
	if len(src) < 26 {
		return 0, ErrMalformedHeader
	}
	if !bytes.Equal(src[0:7], MagicWebDict[:]) || src[7] != WireVersion1 {
		return 0, ErrMalformedHeader
	}
	if dict == nil {
		return 0, errors.New("przm/web_dict: nil dictionary")
	}

	// Check cryptographic DictID digest prefix
	if !bytes.Equal(src[10:18], dict.SHA256[:8]) {
		return 0, ErrDictionaryMismatch
	}

	origSize := int(binary.LittleEndian.Uint32(src[18:22]))
	axiomLen := int(binary.LittleEndian.Uint32(src[22:26]))

	if len(dst) < origSize {
		return 0, ErrBufferOverflow
	}

	axiom := src[26:]
	if len(axiom) < axiomLen {
		return 0, ErrCorruptedStream
	}

	dstPtr := unsafe.Pointer(&dst[0])
	pDst := dstPtr
	axPtr := 0
	axEnd := axiomLen

	// =========================================================================
	// Zero-Stack Flat SIMD Wave Expansion Loop (0 Allocs / O(1) Token Expansion)
	// =========================================================================
	for axPtr < axEnd {
		op := axiom[axPtr]
		axPtr++

		// 1. Direct Terminal Byte (< 192)
		if op < 0xC0 {
			*(*byte)(pDst) = op
			pDst = unsafe.Add(pDst, 1)
			continue
		}

		// 2. Direct Level 1 Rule (0xC0 .. 0xCF) -> 2 bytes
		if op <= 0xCF {
			id := op - 0xC0
			if int(id) >= len(dict.L1) {
				return 0, ErrCorruptedStream
			}
			*(*uint16)(pDst) = dict.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		// 3. Direct Level 2 Rule (0xD0 .. 0xDF) -> 4 bytes
		if op <= 0xDF {
			id := op - 0xD0
			if int(id) >= len(dict.L2) {
				return 0, ErrCorruptedStream
			}
			*(*uint32)(pDst) = dict.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		// 4. Direct Level 3 Rule (0xE0 .. 0xEF) -> 8 bytes
		if op <= 0xEF {
			id := op - 0xE0
			if int(id) >= len(dict.L3) {
				return 0, ErrCorruptedStream
			}
			*(*uint64)(pDst) = dict.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		// 5. Direct Phrase Rule (0xF0 .. 0xF7) -> 9..32 bytes
		if op <= 0xF7 {
			pID := int(op - 0xF0)
			if pID >= len(dict.Phrases) {
				return 0, ErrCorruptedStream
			}
			phrase := dict.Phrases[pID]
			currPos := int(uintptr(pDst) - uintptr(dstPtr))
			copy(dst[currPos:currPos+len(phrase)], phrase)
			pDst = unsafe.Add(pDst, len(phrase))
			continue
		}

		// 6. Extended Level 1 Rule (0xF8 <id8>)
		if op == 0xF8 {
			if axPtr >= axEnd {
				return 0, ErrCorruptedStream
			}
			id := axiom[axPtr]
			axPtr++
			if int(id) >= len(dict.L1) {
				return 0, ErrCorruptedStream
			}
			*(*uint16)(pDst) = dict.ExpL1[id]
			pDst = unsafe.Add(pDst, 2)
			continue
		}

		// 7. Extended Level 2 Rule (0xF9 <id8>)
		if op == 0xF9 {
			if axPtr >= axEnd {
				return 0, ErrCorruptedStream
			}
			id := axiom[axPtr]
			axPtr++
			if int(id) >= len(dict.L2) {
				return 0, ErrCorruptedStream
			}
			*(*uint32)(pDst) = dict.ExpL2[id]
			pDst = unsafe.Add(pDst, 4)
			continue
		}

		// 8. Extended Level 3 Rule (0xFA <id8>)
		if op == 0xFA {
			if axPtr >= axEnd {
				return 0, ErrCorruptedStream
			}
			id := axiom[axPtr]
			axPtr++
			if int(id) >= len(dict.L3) {
				return 0, ErrCorruptedStream
			}
			*(*uint64)(pDst) = dict.ExpL3[id]
			pDst = unsafe.Add(pDst, 8)
			continue
		}

		// 9. Extended Phrase Rule (0xFB <id16>)
		if op == 0xFB {
			if axPtr+1 >= axEnd {
				return 0, ErrCorruptedStream
			}
			pID := int(binary.LittleEndian.Uint16(axiom[axPtr : axPtr+2]))
			axPtr += 2
			if pID >= len(dict.Phrases) {
				return 0, ErrCorruptedStream
			}
			phrase := dict.Phrases[pID]
			currPos := int(uintptr(pDst) - uintptr(dstPtr))
			copy(dst[currPos:currPos+len(phrase)], phrase)
			pDst = unsafe.Add(pDst, len(phrase))
			continue
		}

		// 10. Escaped Raw Terminal Byte (0xFF <raw>)
		if op == 0xFF {
			if axPtr >= axEnd {
				return 0, ErrCorruptedStream
			}
			raw := axiom[axPtr]
			axPtr++
			*(*byte)(pDst) = raw
			pDst = unsafe.Add(pDst, 1)
			continue
		}
	}

	written := int(uintptr(pDst) - uintptr(dstPtr))
	if written != origSize {
		return written, fmt.Errorf("%w: decoded %d bytes, expected %d", ErrSizeMismatch, written, origSize)
	}
	return written, nil
}
