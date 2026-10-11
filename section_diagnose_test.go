package przm

import (
	"fmt"
	"testing"
	"os"
)

func TestDiagnoseSections(t *testing.T) {
	files := []struct {
		name string
		path string
	}{
		{"CSS", `d:\CodingProjects\g-man\cmd\generator\protobufs\c\partner.steamgames.com\ssr\aYRS7mnC.css`},
		{"JS", `d:\CodingProjects\g-man\cmd\generator\protobufs\c\ClientExtracted\steamui\broadcast.js`},
		{"HTML", `d:\CodingProjects\g-man\cmd\generator\protobufs\Random\People.html`},
		{"C", `d:\CodingProjects\przm\src\przm_dec.c`},
	}

	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Logf("Skip %s: %v", f.name, err)
			continue
		}
		rawDict := GetRawDictionary(0x4252544C) // Brotli dict
		opts := CompressOptions{
			Level:    6,
			RawDict:  rawDict,
			DictID:   0x4252544C,
			MinMatch: 3,
		}
		comp, err := CompressWithOptions(data, opts)
		if err != nil {
			t.Fatalf("Compress failed: %v", err)
		}
		fmt.Printf("File: %s (Raw: %d -> Comp: %d bytes)\n", f.name, len(data), len(comp))
	}
}
