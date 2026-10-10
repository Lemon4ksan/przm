// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

import (
)

// RouteDecision specifies the pre-LZ classification and transform gating decision.
type RouteDecision uint8

const (
	// RouteDirectBypass fast-bypasses all transforms directly into Large Window LZ77 + 4-Stream Huffman.
	// Used for structured text, source code, JSON, XML, logs, CSV, and general binaries.
	RouteDirectBypass RouteDecision = 0

	// RouteFastBypass is an alias for RouteDirectBypass.
	RouteFastBypass RouteDecision = 0

	// RouteContainerBypass fast-bypasses pre-compressed containers (ZIP, GZIP, PNG, JPEG, PDF).
	RouteContainerBypass RouteDecision = 2

	// RouteBypass is an alias for RouteContainerBypass.
	RouteBypass RouteDecision = 2

	// RouteSparseZero identifies sparse matrices, RLE runs, and pruned neural weights (>40% zeros).
	RouteSparseZero RouteDecision = 3

	// RouteSmoothDelta identifies smooth continuous physical signals (MAD <= 8.0).
	RouteSmoothDelta RouteDecision = 4
)

func (r RouteDecision) String() string {
	switch r {
	case RouteDirectBypass:
		return "DirectBypass"
	case RouteContainerBypass:
		return "ContainerBypass"
	case RouteSparseZero:
		return "SparseZero"
	case RouteSmoothDelta:
		return "SmoothDelta"
	default:
		return "Unknown"
	}
}

func RouteContent(data []byte) RouteDecision {
	return RouteDirectBypass
}

// RouteBlock is an alias for RouteContent.
func RouteBlock(data []byte) RouteDecision {
	return RouteContent(data)
}
