package przm

import (
	"bytes"
	"testing"
)

func TestCompressWithOptionsFunctional(t *testing.T) {
	data := []byte("Functional options pattern verification for Season 22 root facade generic pipeline architect.")
	comp, err := CompressWithOptions(data,
		WithLevel(6),
		WithThreads(2),
		WithWindowBits(18),
		WithWindowLog(18),
		WithStandardStreamOnly(true),
	)
	if err != nil {
		t.Fatalf("CompressWithOptions failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	n, err := Decompress(comp, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if n != len(data) || !bytes.Equal(dst[:n], data) {
		t.Fatalf("Roundtrip mismatch: got %d bytes, want %d", n, len(data))
	}
}

func TestCompressWithOptionsStruct(t *testing.T) {
	data := []byte("Struct-based options compatibility check for benchmark and tool consumers.")
	opts := CompressOptions{
		Level:      4,
		Threads:    1,
		WindowBits: 16,
	}
	comp, err := CompressWithOptions(data, opts)
	if err != nil {
		t.Fatalf("CompressWithOptions with struct failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	dec, err := DecompressTo(comp, dst)
	if err != nil {
		t.Fatalf("DecompressTo failed: %v", err)
	}
	if !bytes.Equal(dec, data) {
		t.Fatalf("DecompressTo output mismatch")
	}
}

func TestDecompressToZeroAllocations(t *testing.T) {
	data := []byte("Zero allocation guarantee test for DecompressTo returning subslice.")
	comp, err := Compress(data, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	allocs := testing.AllocsPerRun(100, func() {
		dec, decErr := DecompressTo(comp, dst)
		if decErr != nil || len(dec) != len(data) {
			t.Fatalf("DecompressTo inside test failed: %v", decErr)
		}
	})

	if allocs != 0.0 {
		t.Fatalf("DecompressTo allocated %f allocs/op (strictly 0.0 required)", allocs)
	}
}

func TestDecodeHelper(t *testing.T) {
	data := []byte("Decode helper allocating slice automatically.")
	comp, err := Compress(data, 6)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	decoded, err := Decode(comp)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}
	if !bytes.Equal(decoded, data) {
		t.Fatalf("Decode output mismatch")
	}
}

func TestDynamicExternalDictionaryInjection(t *testing.T) {
	entries := []string{
		"PRZM_ARCHITECT_ZENITH",
		"SEASON_22_AUDIT_SWARM",
		"FLAT_CODE_LAW_CERTIFIED",
	}
	desc := NewDictionaryDesc(0x00002201, entries)
	if desc.DictID != 0x00002201 || desc.NumEntries != 3 {
		t.Fatalf("NewDictionaryDesc unexpected fields: %+v", desc)
	}

	opts := ApplyOptions(WithDictDesc(desc))
	if opts.DictID != 0x00002201 || opts.Dict != desc {
		t.Fatalf("ApplyOptions WithDictDesc mismatch: %+v", opts)
	}
}

func TestCompressWithRawDictRoundtrip(t *testing.T) {
	dict := []byte("prefix_dictionary_context_buffer_data_")
	data := []byte("prefix_dictionary_context_buffer_data_payload_stream_content_1234567890")
	comp, err := CompressWithDict(data, 6, dict)
	if err != nil {
		t.Fatalf("CompressWithDict failed: %v", err)
	}

	dst := make([]byte, len(data), len(data)+64)
	n, err := Decompress(comp, dst)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(dst[:n], data) {
		t.Fatalf("CompressWithDict roundtrip mismatch")
	}
}

func TestDefaultOptionsAndFallback(t *testing.T) {
	opts := DefaultOptions()
	if opts.Level != 6 || opts.Threads != 1 || opts.WindowBits != 18 {
		t.Fatalf("DefaultOptions unexpected: %+v", opts)
	}

	// Empty compression returns valid minimal container
	emptyComp, err := Compress(nil, 6)
	if err != nil {
		t.Fatalf("Compress(nil) failed: %v", err)
	}
	sz, err := GetUncompressedSize(emptyComp)
	if err != nil || sz != 0 {
		t.Fatalf("GetUncompressedSize(emptyComp) = %d, err = %v; want 0, nil", sz, err)
	}
}
