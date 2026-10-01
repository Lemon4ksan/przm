// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"bytes"
	"testing"
)

func TestDict_JSONBuiltin(t *testing.T) {
	jsonPayload := []byte(`{"status":200,"level":"INFO","method":"GET","uri":"/api/v1/billing/checkout","user_id":"123","message":"ok"}`)
	dictID := detectDictionary(jsonPayload)
	if dictID != DictIDJSON {
		t.Fatalf("expected DictIDJSON (1), got %d", dictID)
	}

	compressed, err := Compress(jsonPayload, 2)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(decompressed, jsonPayload) {
		t.Fatalf("JSON roundtrip mismatch: got %s, want %s", string(decompressed), string(jsonPayload))
	}
}

func TestDict_ExternalDictionaryContainer(t *testing.T) {
	tokens := []string{
		"SELECT * FROM ",
		" WHERE id = ",
		" ORDER BY created_at DESC",
		" LIMIT 100",
		" INSERT INTO ",
		" VALUES (",
	}

	dict, err := NewDictionary(0x2001, tokens)
	if err != nil {
		t.Fatalf("NewDictionary failed: %v", err)
	}

	if dict.ID() != 0x2001 {
		t.Fatalf("dict.ID() got 0x%X, want 0x2001", dict.ID())
	}
	if len(dict.Tokens()) != len(tokens) {
		t.Fatalf("len(dict.Tokens()) got %d, want %d", len(dict.Tokens()), len(tokens))
	}

	// Test PRZMD serialization
	serialized := dict.Bytes()
	if len(serialized) < 16 {
		t.Fatalf("serialized dictionary too small: %d bytes", len(serialized))
	}

	restored, err := NewDictionaryFromBytes(serialized)
	if err != nil {
		t.Fatalf("NewDictionaryFromBytes failed: %v", err)
	}

	if restored.ID() != dict.ID() {
		t.Fatalf("restored ID mismatch: got 0x%X, want 0x%X", restored.ID(), dict.ID())
	}
	if len(restored.Tokens()) != len(dict.Tokens()) {
		t.Fatalf("restored tokens count mismatch: got %d, want %d", len(restored.Tokens()), len(dict.Tokens()))
	}
	for i := range tokens {
		if !bytes.Equal(restored.Tokens()[i], dict.Tokens()[i]) {
			t.Fatalf("token %d mismatch: got %q, want %q", i, restored.Tokens()[i], dict.Tokens()[i])
		}
	}
}

func TestDict_RegistryAndExplicitCodec(t *testing.T) {
	tokens := []string{
		"temperature_celsius",
		"humidity_relative_percent",
		"pressure_hectopascals",
		"device_uuid_prefix",
	}

	dictID := uint32(0x1050)
	dict, err := NewDictionary(dictID, tokens)
	if err != nil {
		t.Fatalf("NewDictionary error: %v", err)
	}

	err = RegisterDictionary(dict)
	if err != nil {
		t.Fatalf("RegisterDictionary error: %v", err)
	}
	defer UnregisterDictionary(dictID)

	retrieved := GetDictionary(dictID)
	if retrieved == nil || retrieved.ID() != dictID {
		t.Fatalf("GetDictionary failed to retrieve registered dictionary")
	}

	telemetryData := []byte("device_uuid_prefix-001: temperature_celsius=23.5, humidity_relative_percent=45, pressure_hectopascals=1013")
	compressed, err := CompressWithDict(telemetryData, 2, dict)
	if err != nil {
		t.Fatalf("CompressWithDict error: %v", err)
	}

	// Decompress without explicit dict (should resolve via registry)
	decompressed, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("Decompress via registry failed: %v", err)
	}

	if !bytes.Equal(decompressed, telemetryData) {
		t.Fatalf("Decompress mismatch: got %s, want %s", string(decompressed), string(telemetryData))
	}

	// Decompress with explicit dict
	decompWithDict, err := DecompressWithDict(compressed, dict)
	if err != nil {
		t.Fatalf("DecompressWithDict failed: %v", err)
	}
	if !bytes.Equal(decompWithDict, telemetryData) {
		t.Fatalf("DecompressWithDict mismatch")
	}
}

func TestDict_UnknownDictError(t *testing.T) {
	tokens := []string{"temporary_token_alpha", "temporary_token_beta"}
	tempID := uint32(0x7777)
	dict, err := NewDictionary(tempID, tokens)
	if err != nil {
		t.Fatalf("NewDictionary error: %v", err)
	}

	payload := []byte("temporary_token_alpha data temporary_token_beta")
	compressed, err := CompressWithDict(payload, 2, dict)
	if err != nil {
		t.Fatalf("CompressWithDict error: %v", err)
	}

	// Ensure tempID is NOT in registry
	UnregisterDictionary(tempID)

	_, err = Decompress(compressed)
	if err == nil {
		t.Fatal("Decompress with unknown DictID should fail, but succeeded")
	}
}

func TestDict_TrainDictionary(t *testing.T) {
	corpus := [][]byte{
		[]byte("log_entry: service_alpha status=ok response_time=12ms action=query"),
		[]byte("log_entry: service_beta status=ok response_time=15ms action=query"),
		[]byte("log_entry: service_gamma status=ok response_time=9ms action=query"),
		[]byte("log_entry: service_delta status=ok response_time=20ms action=query"),
	}

	dictID := uint32(0x3000)
	dict, err := TrainDictionary(corpus, dictID, 16)
	if err != nil {
		t.Fatalf("TrainDictionary failed: %v", err)
	}

	if dict.ID() != dictID {
		t.Fatalf("trained dict ID got 0x%X, want 0x%X", dict.ID(), dictID)
	}
	if len(dict.Tokens()) == 0 {
		t.Fatal("TrainDictionary produced 0 tokens")
	}
}
