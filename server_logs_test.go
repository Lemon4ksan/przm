package przm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"
)

func generateTestServerLogStream(targetSize int) []byte {
	var b strings.Builder
	for i := 1000; b.Len() < targetSize-128; i++ {
		fmt.Fprintf(
			&b,
			`{"timestamp":"2026-10-11T01:15:%02d.%03dZ","level":"INFO","trace_id":"trc_%08x","client_ip":"192.168.1.%d","http":{"method":"POST","uri":"/api/v2/ingest/telemetry","status":200,"duration_ms":%.2f,"bytes_sent":%d},"user_agent":"CloudScale-Agent/2.4.1 (Linux x86_64) Go-http-client/2.0","service":"edge-ingest-router","region":"eu-central-1"}`+"\n",
			(i/1000)%60,
			i%1000,
			i*31,
			i%254+1,
			1.2+float64(i%50)/10.0,
			480+(i%200),
		)
	}
	return []byte(b.String()[:targetSize])
}

func TestServerLogsCurrentBaseline(t *testing.T) {
	data := generateTestServerLogStream(1024 * 1024)
	t.Logf("Dataset length: %d bytes", len(data))

	// Diagnostic: measure exact match lengths with lineStarts
	var lineStarts []int
	lineStarts = append(lineStarts, 0)
	for i, b := range data {
		if b == '\n' && i+1 < len(data) {
			lineStarts = append(lineStarts, i+1)
		}
	}
	t.Logf("Total lines detected: %d", len(lineStarts))

	totalMatch200 := 0
	count200 := 0
	for line := 200; line < len(lineStarts); line++ {
		pCur := lineStarts[line]
		pPrev200 := lineStarts[line-200]
		d200 := pCur - pPrev200
		// Check match from start of HTTP field to end of line
		// HTTP field is at pCur + ~120
		pHTTP := pCur + 77 // after client_ip
		l := 0
		for pHTTP+l < len(data) && data[pHTTP+l] == data[pHTTP-d200+l] {
			l++
		}
		if l >= 32 {
			count200++
			totalMatch200 += l
		}
	}
	t.Logf("Exact 200-line stride matches: count=%d/%d, avgLen=%.1f",
		count200, len(lineStarts)-200, float64(totalMatch200)/float64(max(count200, 1)))

	compL4, err := CompressWithOptions(data, CompressOptions{
		Level:              4,
		Threads:            1,
		StandardStreamOnly: false,
	})
	if err == nil {
		t.Logf("L4 (Zero-Dict): %d bytes (%.2f KB)", len(compL4), float64(len(compL4))/1024.0)
		hdrL4, _ := ParseFrameHeader(compL4)
		s1 := 8 + 2*int(compL4[hdrL4.DataOffset+5])
		s2 := int(hdrL4.CmdStreamSize)
		s3 := int(hdrL4.OffsetStreamSize)
		s4 := len(compL4) - (hdrL4.DataOffset + s1 + s2 + s3)
		t.Logf("L4 Breakdown: Sec1=%d, Sec2(Cmd)=%d, Sec3(Off)=%d, Sec4(Lit)=%d", s1, s2, s3, s4)

		t.Logf("L4 FlagNibble=%v (Flag=0x%02x)", (hdrL4.Flags&FlagNibble) != 0, hdrL4.Flags)

		sec2L4Off := hdrL4.DataOffset + s1
		sec3L4Off := sec2L4Off + s2
		sec2L4Type := compL4[sec2L4Off]
		sec3L4Type := compL4[sec3L4Off]
		sec2L4OrigLen := int(binary.LittleEndian.Uint32(compL4[sec2L4Off+1 : sec2L4Off+5]))
		sec3L4OrigLen := int(binary.LittleEndian.Uint32(compL4[sec3L4Off+1 : sec3L4Off+5]))
		t.Logf(
			"L4 Raw Sizes: Sec2(Cmd)=%d (type=%d), Sec3(Off)=%d (type=%d)",
			sec2L4OrigLen,
			sec2L4Type,
			sec3L4OrigLen,
			sec3L4Type,
		)

		var scratchTableL4 CanonicalHuffmanTable
		cmdBufL4 := make([]byte, sec2L4OrigLen)
		nCmdsL4, _ := UnpackStream(cmdBufL4, compL4[sec2L4Off:sec2L4Off+hdrL4.CmdStreamSize], &scratchTableL4)
		offBufL4 := make([]byte, sec3L4OrigLen)
		nOffsL4, _ := UnpackStream(offBufL4, compL4[sec3L4Off:sec3L4Off+hdrL4.OffsetStreamSize], &scratchTableL4)
		_ = nOffsL4

		opCounts := make(map[string]int)
		for _, op := range cmdBufL4[:nCmdsL4] {
			if op < 0x80 {
				opCounts["FusedRep"]++
			} else if op >= 0x80 && op <= 0xBF {
				opCounts["Explicit"]++
			} else if op >= 0xC0 && op <= 0xCF {
				opCounts["LongRep"]++
			} else if op >= 0xF2 && op <= 0xFA {
				opCounts["LitShort"]++
			} else if op == 0xF1 {
				opCounts["LitPrefix"]++
			} else if op == 0xF0 {
				opCounts["OpLit"]++
			} else {
				opCounts["Other"]++
			}
		}
		t.Logf("L4 Opcode Breakdown: %+v", opCounts)

		allDistFreqL4 := make(map[int]int)
		allDistMatchBytesL4 := make(map[int]int)
		r0L4, r1L4, r2L4 := 1, 4, 8
		offPtrL4 := 0
		cmdPtrL4 := 0
		for cmdPtrL4 < nCmdsL4 {
			op := cmdBufL4[cmdPtrL4]
			cmdPtrL4++
			if op < 0x80 {
				k := int(op & 0x03)
				mLen := int((op>>2)&0x03) + 4
				var d int
				switch k {
				case 0:
					d = r0L4
				case 1:
					d = r1L4
					r1L4, r0L4 = r0L4, r1L4
				case 2:
					d = r2L4
					r2L4, r1L4, r0L4 = r1L4, r0L4, r2L4
				}
				allDistFreqL4[d]++
				allDistMatchBytesL4[d] += mLen
			} else if op >= 0x80 && op <= 0xBF {
				baseLen := int(op & 0x0F)
				mLen := baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdBufL4[cmdPtrL4:])
					cmdPtrL4 += n
					mLen = 19 + int(extra)
				}
				d, n := binary.Uvarint(offBufL4[offPtrL4:])
				offPtrL4 += n
				r2L4 = r1L4
				r1L4 = r0L4
				r0L4 = int(d)
				allDistFreqL4[int(d)]++
				allDistMatchBytesL4[int(d)] += mLen
			} else if op >= 0xC0 && op <= 0xCF {
				k := int(op & 0x0F)
				token := cmdBufL4[cmdPtrL4]
				cmdPtrL4++
				baseLen := int(token & 0x0F)
				mLen := baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdBufL4[cmdPtrL4:])
					cmdPtrL4 += n
					mLen = 19 + int(extra)
				}
				var d int
				switch k {
				case 0:
					d = r0L4
				case 1:
					d = r1L4
					r1L4, r0L4 = r0L4, r1L4
				case 2:
					d = r2L4
					r2L4, r1L4, r0L4 = r1L4, r0L4, r2L4
				}
				allDistFreqL4[d]++
				allDistMatchBytesL4[d] += mLen
			}
		}
		type dp struct{ d, c, bytes int }
		var topDL4 []dp
		for d, c := range allDistFreqL4 {
			topDL4 = append(topDL4, dp{d, c, allDistMatchBytesL4[d]})
		}
		for i := 0; i < len(topDL4); i++ {
			for j := i + 1; j < len(topDL4); j++ {
				if topDL4[j].bytes > topDL4[i].bytes {
					topDL4[i], topDL4[j] = topDL4[j], topDL4[i]
				}
			}
		}
		lenHist := make(map[int]int)
		for _, bytes := range allDistMatchBytesL4 {
			_ = bytes
		}
		// Count lengths
		cmdPtrL4 = 0
		offPtrL4 = 0
		for cmdPtrL4 < nCmdsL4 {
			op := cmdBufL4[cmdPtrL4]
			cmdPtrL4++
			mLen := 0
			if op < 0x80 {
				mLen = int((op>>2)&0x03) + 4
			} else if op >= 0x80 && op <= 0xBF {
				baseLen := int(op & 0x0F)
				mLen = baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdBufL4[cmdPtrL4:])
					cmdPtrL4 += n
					mLen = 19 + int(extra)
				}
				_, n := binary.Uvarint(offBufL4[offPtrL4:])
				offPtrL4 += n
			} else if op >= 0xC0 && op <= 0xCF {
				token := cmdBufL4[cmdPtrL4]
				cmdPtrL4++
				baseLen := int(token & 0x0F)
				mLen = baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdBufL4[cmdPtrL4:])
					cmdPtrL4 += n
					mLen = 19 + int(extra)
				}
			}
			bracket := 0
			if mLen <= 7 {
				bracket = mLen
			} else if mLen <= 18 {
				bracket = 18
			} else if mLen <= 64 {
				bracket = 64
			} else if mLen <= 128 {
				bracket = 128
			} else {
				bracket = 256
			}
			lenHist[bracket]++
		}
		t.Logf("L4 Length Histogram: <=4:%d, 5:%d, 6:%d, 7:%d, 8..18:%d, 19..64:%d, 65..128:%d, >128:%d",
			lenHist[4], lenHist[5], lenHist[6], lenHist[7], lenHist[18], lenHist[64], lenHist[128], lenHist[256])

		// Trace matches in line 206
		p206 := lineStarts[206]
		p207 := lineStarts[207]
		t.Logf("Line 206: %q", string(data[p206:p207]))

		pSampleStart := p206
		pSampleEnd := p207
		currPos := 0
		cPtr := 0
		oPtr := 0
		lPtr := 0
		r0Sim, r1Sim, r2Sim := 1, 4, 8

		for cPtr < nCmdsL4 && currPos < len(data) {
			op := cmdBufL4[cPtr]
			cPtr++
			if op < 0x80 {
				litL := int(op >> 4)
				mLen := int((op>>2)&0x03) + 4
				k := int(op & 0x03)
				var d int
				switch k {
				case 0:
					d = r0Sim
				case 1:
					d = r1Sim
					r1Sim, r0Sim = r0Sim, r1Sim
				case 2:
					d = r2Sim
					r2Sim, r1Sim, r0Sim = r1Sim, r0Sim, r2Sim
				}
				if currPos >= pSampleStart && currPos < pSampleEnd {
					t.Logf("  [Pos %d] FusedRep k=%d d=%d litL=%d mLen=%d", currPos, k, d, litL, mLen)
				}
				currPos += litL + mLen
				lPtr += litL
			} else if op >= 0x80 && op <= 0xBF {
				litL := int((op >> 4) & 0x03)
				baseLen := int(op & 0x0F)
				mLen := baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdBufL4[cPtr:])
					cPtr += n
					mLen = 19 + int(extra)
				}
				d, n := binary.Uvarint(offBufL4[oPtr:])
				oPtr += n
				r2Sim = r1Sim
				r1Sim = r0Sim
				r0Sim = int(d)
				if currPos >= pSampleStart && currPos < pSampleEnd {
					t.Logf("  [Pos %d] Explicit d=%d litL=%d mLen=%d", currPos, d, litL, mLen)
				}
				currPos += litL + mLen
				lPtr += litL
			} else if op >= 0xC0 && op <= 0xCF {
				k := int(op & 0x0F)
				token := cmdBufL4[cPtr]
				cPtr++
				litL := int(token >> 4)
				baseLen := int(token & 0x0F)
				mLen := baseLen + 4
				if baseLen == 15 {
					extra, n := binary.Uvarint(cmdBufL4[cPtr:])
					cPtr += n
					mLen = 19 + int(extra)
				}
				var d int
				switch k {
				case 0:
					d = r0Sim
				case 1:
					d = r1Sim
					r1Sim, r0Sim = r0Sim, r1Sim
				case 2:
					d = r2Sim
					r2Sim, r1Sim, r0Sim = r1Sim, r0Sim, r2Sim
				}
				if currPos >= pSampleStart && currPos < pSampleEnd {
					t.Logf("  [Pos %d] LongRep k=%d d=%d litL=%d mLen=%d", currPos, k, d, litL, mLen)
				}
				currPos += litL + mLen
				lPtr += litL
			} else if op >= 0xF2 && op <= 0xFA {
				litL := int(op - 0xF2)
				if currPos >= pSampleStart && currPos < pSampleEnd {
					t.Logf("  [Pos %d] LiteralShort litL=%d", currPos, litL)
				}
				currPos += litL
				lPtr += litL
			} else if op == 0xF1 {
				tok := cmdBufL4[cPtr]
				cPtr++
				litL := 0
				if tok < 0x80 {
					litL = int(tok)
				} else {
					tok2 := cmdBufL4[cPtr]
					cPtr++
					litL = int(tok&0x7F) | (int(tok2) << 7)
				}
				if currPos >= pSampleStart && currPos < pSampleEnd {
					t.Logf("  [Pos %d] LiteralPrefix litL=%d", currPos, litL)
				}
				currPos += litL
				lPtr += litL
			} else {
				if currPos >= pSampleStart && currPos < pSampleEnd {
					t.Logf("  [Pos %d] Unknown op 0x%02x", currPos, op)
				}
			}
		}

		t.Logf("L4 Total Decoded Literals: %d bytes", lPtr)
		stdLit, errStd := VANSEncode4x(cmdBufL4[:lPtr]) // test dummy or real literals
		t.Logf("VANSEncode test: err=%v, len=%d", errStd, len(stdLit))
		for i := 0; i < len(topDL4) && i < 15; i++ {
			t.Logf("  dist=%d: count=%d, totalMatchBytes=%d", topDL4[i].d, topDL4[i].c, topDL4[i].bytes)
		}
	}

	compNoDict, err := CompressWithOptions(data, CompressOptions{
		Level:              6,
		MinMatch:           3,
		Threads:            1,
		StandardStreamOnly: false,
	})
	if err != nil {
		t.Fatalf("CompressWithOptions L6 failed: %v", err)
	}
	decompNoDict, err := Decompress(compNoDict)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}
	if !bytes.Equal(data, decompNoDict) {
		t.Fatalf("Bit-exact mismatch (no dict)!")
	}
	t.Logf("L6 (Zero-Dict): %d bytes (%.2f KB)", len(compNoDict), float64(len(compNoDict))/1024.0)

	hdr, err := ParseFrameHeader(compNoDict)
	if err != nil {
		t.Fatalf("ParseFrameHeader failed: %v", err)
	}
	sec1Offset := hdr.DataOffset
	sec1Size := 0
	if sec1Offset+8 <= len(compNoDict) {
		exceptionCount := int(compNoDict[sec1Offset+5])
		sec1Size = 8 + 2*exceptionCount
	}
	sec2Off := sec1Offset + sec1Size
	sec3Off := sec2Off + hdr.CmdStreamSize
	sec4Off := sec3Off + hdr.OffsetStreamSize
	sec4Size := len(compNoDict) - sec4Off

	t.Logf("Breakdown: Sec1(Dist)=%d B, Sec2(Cmd)=%d B, Sec3(Off)=%d B, Sec4(Lit)=%d B, Hdr=%d B",
		sec1Size, hdr.CmdStreamSize, hdr.OffsetStreamSize, sec4Size, hdr.DataOffset)

	sec2OrigLen := int(binary.LittleEndian.Uint32(compNoDict[sec2Off+1 : sec2Off+5]))
	sec3OrigLen := int(binary.LittleEndian.Uint32(compNoDict[sec3Off+1 : sec3Off+5]))
	t.Logf("Original sizes: Sec2=%d, Sec3=%d", sec2OrigLen, sec3OrigLen)

	var scratchTable CanonicalHuffmanTable
	cmdBuf := make([]byte, sec2OrigLen)
	nCmds, err := UnpackStream(cmdBuf, compNoDict[sec2Off:sec2Off+hdr.CmdStreamSize], &scratchTable)
	if err != nil {
		t.Fatalf("UnpackStream cmds failed: %v", err)
	}
	offBuf := make([]byte, sec3OrigLen)
	nOffs, err := UnpackStream(offBuf, compNoDict[sec3Off:sec3Off+hdr.OffsetStreamSize], &scratchTable)
	if err != nil {
		t.Fatalf("UnpackStream offs failed: %v", err)
	}

	explicitCount := 0
	repeatCount := 0
	longRepeatCount := 0
	for _, op := range cmdBuf[:nCmds] {
		if op < 0x80 {
			repeatCount++
		} else if op >= 0x80 && op <= 0xBF {
			explicitCount++
		} else if op >= 0xC0 && op <= 0xCF {
			longRepeatCount++
		}
	}
	t.Logf("Commands breakdown: Total=%d, FusedRep=%d, LongRep=%d, Explicit=%d, RawOffBytes=%d",
		nCmds, repeatCount, longRepeatCount, explicitCount, nOffs)

	allDistFreq := make(map[int]int)
	allDistMatchBytes := make(map[int]int)
	r0, r1, r2 := 1, 4, 8
	offPtr := 0
	cmdPtr := 0
	for cmdPtr < nCmds {
		op := cmdBuf[cmdPtr]
		cmdPtr++
		if op < 0x80 {
			// Fused repeat
			k := int(op & 0x03)
			mLen := int((op>>2)&0x03) + 3
			var d int
			switch k {
			case 0:
				d = r0
			case 1:
				d = r1
				r1, r0 = r0, r1
			case 2:
				d = r2
				r2, r1, r0 = r1, r0, r2
			}
			allDistFreq[d]++
			allDistMatchBytes[d] += mLen
		} else if op >= 0x80 && op <= 0xBF {
			// Explicit match
			baseLen := int(op & 0x0F)
			mLen := baseLen + 3
			if baseLen == 15 {
				extra, n := binary.Uvarint(cmdBuf[cmdPtr:])
				cmdPtr += n
				mLen = 18 + int(extra)
			}
			d, n := binary.Uvarint(offBuf[offPtr:])
			offPtr += n
			r2 = r1
			r1 = r0
			r0 = int(d)
			allDistFreq[int(d)]++
			allDistMatchBytes[int(d)] += mLen
		} else if op >= 0xC0 && op <= 0xCF {
			// Long repeat
			k := int(op & 0x0F)
			token := cmdBuf[cmdPtr]
			cmdPtr++
			baseLen := int(token & 0x0F)
			mLen := baseLen + 3
			if baseLen == 15 {
				extra, n := binary.Uvarint(cmdBuf[cmdPtr:])
				cmdPtr += n
				mLen = 18 + int(extra)
			}
			var d int
			switch k {
			case 0:
				d = r0
			case 1:
				d = r1
				r1, r0 = r0, r1
			case 2:
				d = r2
				r2, r1, r0 = r1, r0, r2
			}
			allDistFreq[d]++
			allDistMatchBytes[d] += mLen
		}
	}

	type dp struct{ d, c, bytes int }
	var topD []dp
	for d, c := range allDistFreq {
		topD = append(topD, dp{d, c, allDistMatchBytes[d]})
	}
	for i := 0; i < len(topD); i++ {
		for j := i + 1; j < len(topD); j++ {
			if topD[j].bytes > topD[i].bytes {
				topD[i], topD[j] = topD[j], topD[i]
			}
		}
	}
	t.Logf("Top 15 Distances by Total Match Bytes:")
	for i := 0; i < len(topD) && i < 15; i++ {
		t.Logf("  dist=%d: count=%d, totalMatchBytes=%d", topD[i].d, topD[i].c, topD[i].bytes)
	}

	// Measure decomp speed
	iters := 10
	start := time.Now()
	for i := 0; i < iters; i++ {
		dec, err := Decompress(compNoDict)
		if err != nil || len(dec) != len(data) {
			t.Fatalf("decomp error")
		}
	}
	elapsed := time.Since(start).Seconds()
	mbPerSec := (float64(len(data)*iters) / (1024 * 1024)) / elapsed
	t.Logf("Decomp throughput: %.1f MB/s", mbPerSec)
}
