// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package bench provides benchmarking corpora and analysis tools for PRZM.
package bench

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math"
)

// DataType identifies the class of data generated for benchmarking.
type DataType string

const (
	DataJSON        DataType = "json_logs"
	DataTabular32   DataType = "tabular_stride32"
	DataTabular64   DataType = "tabular_stride64"
	DataMachineCode DataType = "machine_code_x86"
	DataSourceCode  DataType = "source_code_go"
	DataRLE         DataType = "rle_sparse"
	DataRandom      DataType = "random_noise"
)

// CorpusItem encapsulates a named dataset with its raw bytes and metadata.
type CorpusItem struct {
	Type     DataType
	Size     int
	Name     string
	Data     []byte
	Metadata string
}

// GenerateCorpus generates the standard testing matrix across data types and sizes.
func GenerateCorpus(sizes []int) []CorpusItem {
	if len(sizes) == 0 {
		sizes = []int{4 * 1024, 16 * 1024, 64 * 1024, 256 * 1024, 1024 * 1024}
	}

	var items []CorpusItem
	for _, size := range sizes {
		items = append(items,
			CorpusItem{
				Type:     DataJSON,
				Size:     size,
				Name:     fmt.Sprintf("json_%dK", size/1024),
				Data:     GenerateJSON(size),
				Metadata: "Structured microservice JSON logs (timestamps, trace IDs, endpoints)",
			},
			CorpusItem{
				Type:     DataTabular32,
				Size:     size,
				Name:     fmt.Sprintf("tabular32_%dK", size/1024),
				Data:     GenerateTabular(size, 32),
				Metadata: "Tabular telemetry with 32-byte fixed stride",
			},
			CorpusItem{
				Type:     DataTabular64,
				Size:     size,
				Name:     fmt.Sprintf("tabular64_%dK", size/1024),
				Data:     GenerateTabular(size, 64),
				Metadata: "Tabular telemetry with 64-byte fixed stride",
			},
			CorpusItem{
				Type:     DataMachineCode,
				Size:     size,
				Name:     fmt.Sprintf("x86_%dK", size/1024),
				Data:     GenerateMachineCode(size),
				Metadata: "Simulated x86_64 machine code with relative CALL/JMP opcodes",
			},
			CorpusItem{
				Type:     DataSourceCode,
				Size:     size,
				Name:     fmt.Sprintf("code_%dK", size/1024),
				Data:     GenerateSourceCode(size),
				Metadata: "Go source code with keywords, functions, error handling",
			},
			CorpusItem{
				Type:     DataRLE,
				Size:     size,
				Name:     fmt.Sprintf("rle_%dK", size/1024),
				Data:     GenerateRLE(size),
				Metadata: "Sparse data with long zero runs and repeated header tokens",
			},
			CorpusItem{
				Type:     DataRandom,
				Size:     size,
				Name:     fmt.Sprintf("random_%dK", size/1024),
				Data:     GenerateRandom(size),
				Metadata: "High-entropy incompressible cryptographic noise",
			},
		)
	}
	return items
}

// GenerateJSON produces structured web service JSON logs with repeating schemas and variable fields.
func GenerateJSON(targetSize int) []byte {
	endpoints := []string{
		"/api/v1/auth/login",
		"/api/v1/users/profile",
		"/api/v1/inventory/items",
		"/api/v1/billing/checkout",
		"/healthz",
		"/metrics",
	}
	methods := []string{"GET", "POST", "PUT", "DELETE"}
	statuses := []int{200, 201, 204, 400, 401, 403, 404, 500}

	var buf bytes.Buffer
	seq := 1000
	for buf.Len() < targetSize {
		seq++
		ts := 1772640000 + (seq % 86400)
		ep := endpoints[seq%len(endpoints)]
		meth := methods[seq%len(methods)]
		status := statuses[seq%len(statuses)]
		dur := (seq * 37) % 500

		line := fmt.Sprintf(`{"ts":%d,"level":"INFO","trace_id":"e4d8%04x-9b2e-4b71-8894-c5a782%06x","client_ip":"192.168.1.%d","method":"%s","uri":"%s","status":%d,"duration_ms":%d,"bytes_sent":%d}`+"\n",
			ts, seq&0xFFFF, (seq*17)&0xFFFFFF, seq%250+1, meth, ep, status, dur, (dur*48)+128)
		buf.WriteString(line)
	}
	res := buf.Bytes()
	if len(res) > targetSize {
		res = res[:targetSize]
	}
	return res
}

// GenerateTabular produces structured binary records with a fixed stride.
// Each record contains a sequence ID (uint32), timestamp (uint64), float telemetry (float64), and status flags (uint16/uint32).
func GenerateTabular(targetSize int, stride int) []byte {
	data := make([]byte, targetSize)
	records := targetSize / stride

	for r := range records {
		offset := r * stride
		// Sequence ID (uint32)
		binary.LittleEndian.PutUint32(data[offset:offset+4], uint32(r))
		// Timestamp (uint64)
		binary.LittleEndian.PutUint64(data[offset+4:offset+12], uint64(1772640000+r*10))
		// Sensor value (float64, sine wave)
		val := math.Sin(float64(r)*0.05) * 100.0
		binary.LittleEndian.PutUint64(data[offset+12:offset+20], math.Float64bits(val))
		// Status flags (uint16)
		data[offset+20] = byte(r & 0x07)
		data[offset+21] = 0x80

		// Fill remainder of record with repeating identifier / category
		for j := 22; j < stride; j++ {
			data[offset+j] = byte('A' + ((j + r/10) % 26))
		}
	}
	return data
}

// GenerateMachineCode simulates x86_64 binary code with function prologues/epilogues,
// relative CALL (0xE8) and JMP (0xE9) instructions, and standard opcode sequences.
func GenerateMachineCode(targetSize int) []byte {
	data := make([]byte, targetSize)
	i := 0
	funcID := 0
	for i < targetSize-32 {
		funcID++
		// Function prologue: push rbp; mov rbp, rsp; sub rsp, 0x20
		prologue := []byte{0x55, 0x48, 0x89, 0xE5, 0x48, 0x83, 0xEC, 0x20}
		copy(data[i:], prologue)
		i += len(prologue)

		// Instructions with relative calls
		for j := 0; j < 4 && i < targetSize-16; j++ {
			// mov eax, [rbp-0x4]; add eax, imm
			data[i] = 0x8B
			data[i+1] = 0x45
			data[i+2] = 0xFC
			data[i+3] = 0x83
			data[i+4] = 0xC0
			data[i+5] = byte(j + 1)
			i += 6

			// CALL relative: 0xE8 <offset32>
			data[i] = 0xE8
			rel := int32((funcID*128 + j*16) - i)
			binary.LittleEndian.PutUint32(data[i+1:i+5], uint32(rel))
			i += 5
		}

		// Function epilogue: leave; ret
		if i < targetSize-2 {
			data[i] = 0xC9
			data[i+1] = 0xC3
			i += 2
		}
	}
	return data
}

// GenerateSourceCode produces realistic Go source code with repeating syntax patterns,
// error handling, struct definitions, and comments.
func GenerateSourceCode(targetSize int) []byte {
	var buf bytes.Buffer
	buf.WriteString("// Package service generated for PRZM benchmarking.\npackage service\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"time\"\n)\n\n")

	i := 0
	for buf.Len() < targetSize {
		i++
		block := fmt.Sprintf(`// HandleTask%d executes the background task for step %d.
func (s *WorkerService) HandleTask%d(ctx context.Context, req *TaskRequest%d) (*TaskResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("task %d: request payload cannot be nil")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	start := time.Now()
	res, err := s.client.ProcessItem(ctx, req.ItemID, %d)
	if err != nil {
		return nil, fmt.Errorf("task %d failed to process item %%d: %%w", req.ItemID, err)
	}
	s.logger.Info("Task %d completed successfully", "duration_ms", time.Since(start).Milliseconds())
	return &TaskResponse{Status: "SUCCESS", Result: res}, nil
}

`, i, i, i, i%10, i, i*100, i, i)
		buf.WriteString(block)
	}
	res := buf.Bytes()
	if len(res) > targetSize {
		res = res[:targetSize]
	}
	return res
}

// GenerateRLE produces sparse binary sequences with long zero runs and repeated tokens.
func GenerateRLE(targetSize int) []byte {
	data := make([]byte, targetSize)
	pos := 0
	pattern := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}

	for pos < targetSize {
		// Zero run: 64 to 256 bytes
		zeroRun := 64 + (pos % 192)
		if pos+zeroRun > targetSize {
			zeroRun = targetSize - pos
		}
		// zeros are default in make([]byte)
		pos += zeroRun
		if pos >= targetSize {
			break
		}

		// Token run: copy pattern multiple times
		reps := 4 + (pos % 8)
		for r := 0; r < reps && pos+len(pattern) <= targetSize; r++ {
			copy(data[pos:], pattern)
			pos += len(pattern)
		}
	}
	return data
}

// GenerateRandom produces cryptographically random, uniform byte noise.
func GenerateRandom(targetSize int) []byte {
	data := make([]byte, targetSize)
	_, _ = rand.Read(data)
	return data
}
