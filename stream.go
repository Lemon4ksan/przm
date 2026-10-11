// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
	"encoding/binary"
	"io"
)

// DefaultStreamChunkSize defines the default chunk block size (64 KiB) for streaming.
const DefaultStreamChunkSize = 64 * 1024

// CompressStream streams uncompressed data from r to w using standard PRZM block framing.
func CompressStream(r io.Reader, w io.Writer) error {
	buf := make([]byte, DefaultStreamChunkSize)
	var lenBuf [4]byte
	for {
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			comp, cErr := Compress(buf[:n], 3)
			if cErr != nil {
				return cErr
			}
			binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(comp)))
			if _, wErr := w.Write(lenBuf[:]); wErr != nil {
				return wErr
			}
			if _, wErr := w.Write(comp); wErr != nil {
				return wErr
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// DecompressStream reads a PRZM stream from r and writes uncompressed bytes to w.
func DecompressStream(r io.Reader, w io.Writer) error {
	var lenBuf [4]byte
	for {
		_, err := io.ReadFull(r, lenBuf[:])
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		fLen := int(binary.LittleEndian.Uint32(lenBuf[:]))
		frame := make([]byte, fLen)
		if _, err := io.ReadFull(r, frame); err != nil {
			return err
		}
		dec, err := Decompress(frame)
		if err != nil {
			return err
		}
		if _, err := w.Write(dec); err != nil {
			return err
		}
	}
	return nil
}
