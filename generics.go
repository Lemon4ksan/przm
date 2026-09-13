// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm

// MinNonEmpty returns the non-empty slice with the smallest length from the provided list of candidates.
// If all candidates are nil or empty, it returns nil.
func MinNonEmpty[T any](slices ...[]T) []T {
	var best []T
	for _, s := range slices {
		if len(s) > 0 && (len(best) == 0 || len(s) < len(best)) {
			best = s
		}
	}
	return best
}

// MinBy returns the element from items that minimizes the score function.
// If items is empty or score is nil, it returns the zero value of T and false.
func MinBy[T any](items []T, score func(T) int) (T, bool) {
	if len(items) == 0 || score == nil {
		var zero T
		return zero, false
	}
	best := items[0]
	bestScore := score(best)
	for i := 1; i < len(items); i++ {
		s := score(items[i])
		if s < bestScore {
			best = items[i]
			bestScore = s
		}
	}
	return best, true
}
