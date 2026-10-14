package dict

import (
	"bytes"
	"testing"
)


func TestLookupWebToken(t *testing.T) {
	tok, ok := LookupWebToken(0)
	if !ok || len(tok) == 0 {
		t.Fatalf("expected valid web token at 0, got %q, %v", tok, ok)
	}

	_, ok = LookupWebToken(9999)
	if ok {
		t.Fatalf("expected invalid web token for out-of-bounds index")
	}
}

func TestResolveRFCWordAndApplyTransform(t *testing.T) {
	// Test word 0 with tau = 0 (identity)
	// b0 = 128, b1 = 0 -> p = 0 -> LookupBrotliWord(0) = "time"
	cmd := []byte{0x80, 0x00}
	cp := 0
	b0 := cmd[cp]
	cp++
	wPtr, tau, err := ResolveRFCWord(b0, cmd, &cp)
	if err != nil {
		t.Fatalf("ResolveRFCWord failed: %v", err)
	}
	if string(wPtr) != "time" {
		t.Fatalf("expected 'time', got %q", string(wPtr))
	}

	dst := make([]byte, 32)
	dstPos := 0
	var scratch [128]byte
	if err := ApplyTransform(dst, &dstPos, 32, wPtr, tau, scratch[:]); err != nil {
		t.Fatalf("ApplyTransform failed: %v", err)
	}
	if string(dst[:dstPos]) != "time" {
		t.Fatalf("expected transformed 'time', got %q", string(dst[:dstPos]))
	}
}

func TestTransformWordCapitalizeAndUpper(t *testing.T) {
	var scratch [64]byte

	// Op 10: capitalize first letter
	mw := transformWord(10, []byte("hello"), scratch[:])
	if string(mw) != "Hello" {
		t.Fatalf("expected 'Hello', got %q", string(mw))
	}

	// Op 11: uppercase all letters
	mw = transformWord(11, []byte("world"), scratch[:])
	if string(mw) != "WORLD" {
		t.Fatalf("expected 'WORLD', got %q", string(mw))
	}

	// Op 1-9: cut last bytes
	mw = transformWord(2, []byte("testing"), scratch[:])
	if string(mw) != "testi" {
		t.Fatalf("expected 'testi', got %q", string(mw))
	}

	// Op 12-20: skip first bytes
	mw = transformWord(12, []byte("prefix"), scratch[:])
	if string(mw) != "refix" {
		t.Fatalf("expected 'refix', got %q", string(mw))
	}
}

func TestApplyTransformBounds(t *testing.T) {
	dst := make([]byte, 2)
	dstPos := 0
	var scratch [128]byte
	err := ApplyTransform(dst, &dstPos, 2, []byte("toolongstring"), 0, scratch[:])
	if err == nil {
		t.Fatalf("expected error when output exceeds uSize")
	}
	_ = bytes.Equal
}
