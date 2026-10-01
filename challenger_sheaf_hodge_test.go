// Copyright (c) 2026 Lemon4ksan All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package przm_test

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/lemon4ksan/przm"
)

// dotGF256 computes the standard bilinear inner product in F_{2^8}^N:
// <x, y> = \sum_{i} x[i] * y[i] (with addition as XOR).
func dotGF256(x, y []uint8) uint8 {
	var acc uint8
	n := len(x)
	if len(y) < n {
		n = len(y)
	}
	for i := 0; i < n; i++ {
		acc ^= przm.GF256Mul(x[i], y[i])
	}
	return acc
}

// dotCochain0 computes <phi, psi> on C^0(X; F_{2^8}).
func dotCochain0(phi, psi przm.Cochain0) uint8 {
	return dotGF256([]uint8(phi), []uint8(psi))
}

// dotCochain1 computes <u, v> on C^1(X; F_{2^8}) = <u.Eh, v.Eh> ^ <u.Ev, v.Ev>.
func dotCochain1(u, v przm.Cochain1) uint8 {
	return dotGF256(u.Eh, v.Eh) ^ dotGF256(u.Ev, v.Ev)
}

// dotCochain2 computes <c1, c2> on C^2(X; F_{2^8}).
func dotCochain2(c1, c2 przm.Cochain2) uint8 {
	return dotGF256([]uint8(c1), []uint8(c2))
}

// ----------------------------------------------------------------------------
// 1. Orthogonal Decomposition & Adjoint Property (<d0 phi, psi> = <phi, d0^T psi>)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_Adjoint0_OrthogonalDecomposition_12K(t *testing.T) {
	const totalTrials = 12000
	rng := rand.New(rand.NewSource(0xCAFE_BABE_2026))

	t.Logf("Executing %d trials: Adjoint0 duality <d0 phi, psi> == <phi, d0^T psi> and orthogonal decomposition...", totalTrials)

	// Pre-define specific boundary/stress geometries
	specialDims := [][2]int{
		{2, 2}, {2, 3}, {3, 2}, {1, 2}, {2, 1}, {1, 10}, {10, 1}, {1, 1},
		{4, 4}, {8, 8}, {16, 16}, {32, 32}, {3, 7}, {11, 13}, {17, 19},
		{2, 64}, {64, 2}, {33, 17},
	}

	var (
		adjoint0Failures    int
		adjoint1Failures    int
		orthoDecompFailures int
	)

	for trial := 0; trial < totalTrials; trial++ {
		var M, W int
		if trial < len(specialDims) {
			M, W = specialDims[trial][0], specialDims[trial][1]
		} else {
			// Variable random geometries: rows and cols in [1, 36]
			M = 1 + rng.Intn(36)
			W = 1 + rng.Intn(36)
		}

		// Ensure all 256 twists in F_{2^8} are covered systematically and randomly
		var twist uint8
		if trial < 256*4 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Generate 0-cochain phi
		phi := make(przm.Cochain0, M*W)
		switch trial % 6 {
		case 0:
			// Uniform random
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
		case 1:
			// All zeros
			// zero value
		case 2:
			// All ones
			for i := range phi {
				phi[i] = 1
			}
		case 3:
			// Max field element
			for i := range phi {
				phi[i] = 0xFF
			}
		case 4:
			// Single impulse
			if len(phi) > 0 {
				phi[rng.Intn(len(phi))] = uint8(1 + rng.Intn(255))
			}
		case 5:
			// Checkerboard
			for i := 0; i < M; i++ {
				for j := 0; j < W; j++ {
					phi[i*W+j] = uint8(((i + j) & 1) * 0x55)
				}
			}
		}

		// Generate 1-cochain psi
		psi := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		if trial%6 != 1 {
			for i := range psi.Eh {
				psi.Eh[i] = uint8(rng.Intn(256))
			}
			for i := range psi.Ev {
				psi.Ev[i] = uint8(rng.Intn(256))
			}
		}

		// 1. Verify d0 adjoint duality: <d0 phi, psi>_1 == <phi, d0^T psi>_0
		d0phi := przm.Coboundary0(complex, phi)
		d0Tpsi := przm.Adjoint0(complex, psi)

		lhs0 := dotCochain1(d0phi, psi)
		rhs0 := dotCochain0(phi, d0Tpsi)

		if lhs0 != rhs0 {
			adjoint0Failures++
			if adjoint0Failures <= 5 {
				t.Errorf("[Trial %d] Adjoint0 failure: M=%d, W=%d, Twist=0x%02X: <d0 phi, psi>=0x%02X != <phi, d0^T psi>=0x%02X",
					trial, M, W, twist, lhs0, rhs0)
			}
		}

		// 2. Verify d1 adjoint duality when 2D faces exist (M >= 2, W >= 2)
		if M >= 2 && W >= 2 {
			c := make(przm.Cochain2, (M-1)*(W-1))
			for i := range c {
				c[i] = uint8(rng.Intn(256))
			}

			d1psi := przm.Coboundary1(complex, psi)
			d1Tc := przm.Adjoint1(complex, c)

			lhs1 := dotCochain2(d1psi, c)
			rhs1 := dotCochain1(psi, d1Tc)

			if lhs1 != rhs1 {
				adjoint1Failures++
				if adjoint1Failures <= 5 {
					t.Errorf("[Trial %d] Adjoint1 failure: M=%d, W=%d, Twist=0x%02X: <d1 psi, c>=0x%02X != <psi, d1^T c>=0x%02X",
						trial, M, W, twist, lhs1, rhs1)
				}
			}

			// 3. Verify Orthogonal Complement: im(d0) \perp ker(d0^T)
			// Because d0^T(d1^T c) == 0, d1^T c \in ker(d0^T).
			// Therefore, <d0 phi, d1^T c> must be 0 identically.
			pairingOrtho := dotCochain1(d0phi, d1Tc)
			if pairingOrtho != 0 {
				orthoDecompFailures++
				if orthoDecompFailures <= 5 {
					t.Errorf("[Trial %d] Orthogonal decomposition failure: <d0 phi, d1^T c>=0x%02X != 0 (M=%d, W=%d, Twist=0x%02X)",
						trial, pairingOrtho, M, W, twist)
				}
			}
		}
	}

	t.Logf("Summary 12K Adjoint & Orthogonality: Adjoint0 failures = %d, Adjoint1 failures = %d, Ortho failures = %d",
		adjoint0Failures, adjoint1Failures, orthoDecompFailures)

	if adjoint0Failures > 0 {
		t.Fatalf("FAILED: %d Adjoint0 mathematical failures detected!", adjoint0Failures)
	}
	if adjoint1Failures > 0 {
		t.Fatalf("FAILED: %d Adjoint1 mathematical failures detected!", adjoint1Failures)
	}
	if orthoDecompFailures > 0 {
		t.Fatalf("FAILED: %d Orthogonal decomposition failures detected!", orthoDecompFailures)
	}
}

// ----------------------------------------------------------------------------
// 2. Coboundary Nilpotency: d1 ∘ d0 ≡ 0 and Dual d0^T ∘ d1^T ≡ 0 (20,000 trials)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_CoboundaryNilpotency_20K(t *testing.T) {
	const totalTrials = 20000
	rng := rand.New(rand.NewSource(0xDEAD_BEEF_5001))

	t.Logf("Executing %d trials: Coboundary nilpotency d1 ∘ d0 ≡ 0 and dual d0^T ∘ d1^T ≡ 0...", totalTrials)

	var (
		primalNilpotencyFailures int
		dualNilpotencyFailures   int
	)

	for trial := 0; trial < totalTrials; trial++ {
		// Sample arbitrary 2D cell complex geometry (M >= 2, W >= 2)
		M := 2 + rng.Intn(35)
		W := 2 + rng.Intn(35)

		// Sweep all 256 twists repeatedly
		var twist uint8
		if trial < 256*8 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// Primal nilpotency: for arbitrary phi, (d1 ∘ d0)(phi) == 0
		phi := make(przm.Cochain0, M*W)
		switch trial % 5 {
		case 0:
			for i := range phi {
				phi[i] = uint8(rng.Intn(256))
			}
		case 1:
			// Single-hot impulse
			phi[rng.Intn(len(phi))] = uint8(1 + rng.Intn(255))
		case 2:
			// Linear ramp
			for i := range phi {
				phi[i] = uint8(i & 0xFF)
			}
		case 3:
			// Constant field
			cVal := uint8(rng.Intn(256))
			for i := range phi {
				phi[i] = cVal
			}
		case 4:
			// High-frequency alternating
			for i := 0; i < M; i++ {
				for j := 0; j < W; j++ {
					phi[i*W+j] = uint8((i*17 + j*31) & 0xFF)
				}
			}
		}

		d0phi := przm.Coboundary0(complex, phi)
		d1d0phi := przm.Coboundary1(complex, d0phi)

		for faceIdx, val := range d1d0phi {
			if val != 0 {
				primalNilpotencyFailures++
				if primalNilpotencyFailures <= 5 {
					t.Errorf("[Trial %d] Primal nilpotency violation: M=%d, W=%d, Twist=0x%02X: (d1 ∘ d0)(phi) != 0 at face %d (val=0x%02X)",
						trial, M, W, twist, faceIdx, val)
				}
				break
			}
		}

		// Dual nilpotency: for arbitrary 2-cochain c, (d0^T ∘ d1^T)(c) == 0
		c := make(przm.Cochain2, (M-1)*(W-1))
		for i := range c {
			c[i] = uint8(rng.Intn(256))
		}

		d1Tc := przm.Adjoint1(complex, c)
		d0Td1Tc := przm.Adjoint0(complex, d1Tc)

		for vIdx, val := range d0Td1Tc {
			if val != 0 {
				dualNilpotencyFailures++
				if dualNilpotencyFailures <= 5 {
					t.Errorf("[Trial %d] Dual nilpotency violation: M=%d, W=%d, Twist=0x%02X: (d0^T ∘ d1^T)(c) != 0 at vertex %d (val=0x%02X)",
						trial, M, W, twist, vIdx, val)
				}
				break
			}
		}
	}

	t.Logf("Summary 20K Nilpotency: Primal d1 ∘ d0 failures = %d, Dual d0^T ∘ d1^T failures = %d",
		primalNilpotencyFailures, dualNilpotencyFailures)

	if primalNilpotencyFailures > 0 {
		t.Fatalf("FAILED: %d Primal nilpotency failures detected!", primalNilpotencyFailures)
	}
	if dualNilpotencyFailures > 0 {
		t.Fatalf("FAILED: %d Dual nilpotency failures detected!", dualNilpotencyFailures)
	}
}

// ----------------------------------------------------------------------------
// 3. Self-Adjointness and Commutation of Hodge Laplacians Delta_0 and Delta_1 (12,000 trials)
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_HodgeLaplacian_SelfAdjointness_12K(t *testing.T) {
	const totalTrials = 12000
	rng := rand.New(rand.NewSource(0x7001_A55A_BEEF))

	t.Logf("Executing %d trials: Self-adjointness and commutation of Hodge Laplacians Delta_0 and Delta_1...", totalTrials)

	var (
		lap0SelfAdjointFailures int
		lap1SelfAdjointFailures int
		lap0CommutationFailures int
		lap1CommutationFailures int
		lap0LinearityFailures   int
		lap1LinearityFailures   int
	)

	for trial := 0; trial < totalTrials; trial++ {
		M := 2 + rng.Intn(30)
		W := 2 + rng.Intn(30)

		var twist uint8
		if trial < 256*4 {
			twist = uint8(trial % 256)
		} else {
			twist = uint8(rng.Intn(256))
		}

		complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

		// --------------------------------------------------------------------
		// A. Delta_0: Self-Adjointness <Delta_0 phi, chi> == <phi, Delta_0 chi>
		// --------------------------------------------------------------------
		phi := make(przm.Cochain0, M*W)
		chi := make(przm.Cochain0, M*W)
		for i := range phi {
			phi[i] = uint8(rng.Intn(256))
			chi[i] = uint8(rng.Intn(256))
		}

		lap0Phi := przm.HodgeLaplacian0(complex, phi)
		lap0Chi := przm.HodgeLaplacian0(complex, chi)

		p0A := dotCochain0(lap0Phi, chi)
		p0B := dotCochain0(phi, lap0Chi)

		if p0A != p0B {
			lap0SelfAdjointFailures++
			if lap0SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_0 self-adjointness failure: M=%d, W=%d, Twist=0x%02X: <Delta_0 phi, chi>=0x%02X != <phi, Delta_0 chi>=0x%02X",
					trial, M, W, twist, p0A, p0B)
			}
		}

		// Also verify Dirichlet energy identity: <Delta_0 phi, chi> == <d0 phi, d0 chi>
		d0Phi := przm.Coboundary0(complex, phi)
		d0Chi := przm.Coboundary0(complex, chi)
		dirichlet0 := dotCochain1(d0Phi, d0Chi)
		if p0A != dirichlet0 {
			lap0SelfAdjointFailures++
			if lap0SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_0 energy pairing mismatch: <Delta_0 phi, chi>=0x%02X != <d0 phi, d0 chi>=0x%02X",
					trial, p0A, dirichlet0)
			}
		}

		// Delta_0 Linearity & Homogeneity: Delta_0(a * phi ^ b * chi) == a * Delta_0(phi) ^ b * Delta_0(chi)
		scalarA := uint8(rng.Intn(256))
		scalarB := uint8(rng.Intn(256))
		comboPhi := make(przm.Cochain0, M*W)
		for i := range comboPhi {
			comboPhi[i] = przm.GF256Mul(scalarA, phi[i]) ^ przm.GF256Mul(scalarB, chi[i])
		}
		lap0Combo := przm.HodgeLaplacian0(complex, comboPhi)
		for i := range comboPhi {
			expected := przm.GF256Mul(scalarA, lap0Phi[i]) ^ przm.GF256Mul(scalarB, lap0Chi[i])
			if lap0Combo[i] != expected {
				lap0LinearityFailures++
				if lap0LinearityFailures <= 5 {
					t.Errorf("[Trial %d] Delta_0 linearity violated at vertex %d (M=%d, W=%d, Twist=0x%02X)",
						trial, i, M, W, twist)
				}
				break
			}
		}

		// --------------------------------------------------------------------
		// B. Delta_1: Self-Adjointness <Delta_1 u, v> == <u, Delta_1 v>
		// --------------------------------------------------------------------
		u := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		v := przm.Cochain1{
			Eh: make([]uint8, M*(W-1)),
			Ev: make([]uint8, (M-1)*W),
		}
		for i := range u.Eh {
			u.Eh[i] = uint8(rng.Intn(256))
			v.Eh[i] = uint8(rng.Intn(256))
		}
		for i := range u.Ev {
			u.Ev[i] = uint8(rng.Intn(256))
			v.Ev[i] = uint8(rng.Intn(256))
		}

		lap1U := przm.HodgeLaplacian1(complex, u)
		lap1V := przm.HodgeLaplacian1(complex, v)

		p1A := dotCochain1(lap1U, v)
		p1B := dotCochain1(u, lap1V)

		if p1A != p1B {
			lap1SelfAdjointFailures++
			if lap1SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_1 self-adjointness failure: M=%d, W=%d, Twist=0x%02X: <Delta_1 u, v>=0x%02X != <u, Delta_1 v>=0x%02X",
					trial, M, W, twist, p1A, p1B)
			}
		}

		// Verify Hodge energy split: <Delta_1 u, v> == <d0^T u, d0^T v>_0 ^ <d1 u, d1 v>_2
		d0Tu := przm.Adjoint0(complex, u)
		d0Tv := przm.Adjoint0(complex, v)
		d1u := przm.Coboundary1(complex, u)
		d1v := przm.Coboundary1(complex, v)

		dirichlet1 := dotCochain0(d0Tu, d0Tv) ^ dotCochain2(d1u, d1v)
		if p1A != dirichlet1 {
			lap1SelfAdjointFailures++
			if lap1SelfAdjointFailures <= 5 {
				t.Errorf("[Trial %d] Delta_1 Hodge energy split mismatch: <Delta_1 u, v>=0x%02X != <d0^T u, d0^T v> ^ <d1 u, d1 v>=0x%02X",
					trial, p1A, dirichlet1)
			}
		}

		// Delta_1 Linearity & Homogeneity: Delta_1(a * u ^ b * v) == a * Delta_1(u) ^ b * Delta_1(v)
		comboU := przm.Cochain1{
			Eh: make([]uint8, len(u.Eh)),
			Ev: make([]uint8, len(u.Ev)),
		}
		for i := range comboU.Eh {
			comboU.Eh[i] = przm.GF256Mul(scalarA, u.Eh[i]) ^ przm.GF256Mul(scalarB, v.Eh[i])
		}
		for i := range comboU.Ev {
			comboU.Ev[i] = przm.GF256Mul(scalarA, u.Ev[i]) ^ przm.GF256Mul(scalarB, v.Ev[i])
		}
		lap1Combo := przm.HodgeLaplacian1(complex, comboU)

		for i := range comboU.Eh {
			expected := przm.GF256Mul(scalarA, lap1U.Eh[i]) ^ przm.GF256Mul(scalarB, lap1V.Eh[i])
			if lap1Combo.Eh[i] != expected {
				lap1LinearityFailures++
				break
			}
		}
		for i := range comboU.Ev {
			expected := przm.GF256Mul(scalarA, lap1U.Ev[i]) ^ przm.GF256Mul(scalarB, lap1V.Ev[i])
			if lap1Combo.Ev[i] != expected {
				lap1LinearityFailures++
				break
			}
		}

		// --------------------------------------------------------------------
		// C. Commutation with Differentials:
		// Delta_1(d0 phi) == d0(Delta_0 phi)
		// Delta_1(d1^T c) == d1^T(d1 d1^T c)
		// --------------------------------------------------------------------
		// 1. Exact form commutation
		uExact := przm.Coboundary0(complex, phi)
		lap1Exact := przm.HodgeLaplacian1(complex, uExact)
		expectedExact := przm.Coboundary0(complex, lap0Phi)

		if !bytes.Equal(lap1Exact.Eh, expectedExact.Eh) || !bytes.Equal(lap1Exact.Ev, expectedExact.Ev) {
			lap0CommutationFailures++
			if lap0CommutationFailures <= 5 {
				t.Errorf("[Trial %d] Commutation failure: Delta_1(d0 phi) != d0(Delta_0 phi) (M=%d, W=%d, Twist=0x%02X)",
					trial, M, W, twist)
			}
		}

		// 2. Co-exact form commutation
		c := make(przm.Cochain2, (M-1)*(W-1))
		for i := range c {
			c[i] = uint8(rng.Intn(256))
		}
		wCoexact := przm.Adjoint1(complex, c)
		lap1Coexact := przm.HodgeLaplacian1(complex, wCoexact)
		d1w := przm.Coboundary1(complex, wCoexact)
		expectedCoexact := przm.Adjoint1(complex, d1w)

		if !bytes.Equal(lap1Coexact.Eh, expectedCoexact.Eh) || !bytes.Equal(lap1Coexact.Ev, expectedCoexact.Ev) {
			lap1CommutationFailures++
			if lap1CommutationFailures <= 5 {
				t.Errorf("[Trial %d] Commutation failure: Delta_1(d1^T c) != d1^T(d1(d1^T c)) (M=%d, W=%d, Twist=0x%02X)",
					trial, M, W, twist)
			}
		}
	}

	t.Logf("Summary 12K Laplacians: Delta_0 self-adj fails = %d, Delta_1 self-adj fails = %d, Delta_0 comm fails = %d, Delta_1 comm fails = %d, Lap0 lin fails = %d, Lap1 lin fails = %d",
		lap0SelfAdjointFailures, lap1SelfAdjointFailures, lap0CommutationFailures, lap1CommutationFailures, lap0LinearityFailures, lap1LinearityFailures)

	if lap0SelfAdjointFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 self-adjointness failures detected!", lap0SelfAdjointFailures)
	}
	if lap1SelfAdjointFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 self-adjointness failures detected!", lap1SelfAdjointFailures)
	}
	if lap0CommutationFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 commutation failures detected!", lap0CommutationFailures)
	}
	if lap1CommutationFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 commutation failures detected!", lap1CommutationFailures)
	}
	if lap0LinearityFailures > 0 {
		t.Fatalf("FAILED: %d Delta_0 linearity failures detected!", lap0LinearityFailures)
	}
	if lap1LinearityFailures > 0 {
		t.Fatalf("FAILED: %d Delta_1 linearity failures detected!", lap1LinearityFailures)
	}
}

// ----------------------------------------------------------------------------
// 4. Edge Cases: Degenerate Geometries, Zero Fields, Boundary Extremes
// ----------------------------------------------------------------------------

func TestChallenger_Sheaf_DegenerateAndExtremalBoundary(t *testing.T) {
	// Boundary geometries where rows or cols is 1, or twist is 0, 1, 255
	geoms := [][2]int{
		{1, 1}, {1, 2}, {2, 1}, {1, 16}, {16, 1}, {2, 2},
	}
	twists := []uint8{0, 1, 2, 3, 0x1B, 0xFE, 0xFF}

	for _, g := range geoms {
		M, W := g[0], g[1]
		for _, twist := range twists {
			complex := przm.CellComplex2D{Rows: M, Cols: W, Twist: twist}

			phi := make(przm.Cochain0, M*W)
			for i := range phi {
				phi[i] = uint8((i*37 + 13) & 0xFF)
			}

			u := przm.Coboundary0(complex, phi)
			if len(u.Eh) != M*(W-1) {
				t.Fatalf("Eh length mismatch: got %d, want %d", len(u.Eh), M*(W-1))
			}
			if len(u.Ev) != (M-1)*W {
				t.Fatalf("Ev length mismatch: got %d, want %d", len(u.Ev), (M-1)*W)
			}

			div := przm.Adjoint0(complex, u)
			if len(div) != M*W {
				t.Fatalf("Adjoint0 length mismatch: got %d, want %d", len(div), M*W)
			}

			// Adjoint duality on degenerate grid: <d0 phi, u> == <phi, d0^T u>
			lhs := dotCochain1(u, u)
			rhs := dotCochain0(phi, div)
			if lhs != rhs {
				t.Fatalf("Adjoint duality failed on degenerate grid %dx%d (twist 0x%02X): lhs=0x%02X != rhs=0x%02X",
					M, W, twist, lhs, rhs)
			}

			// Delta_0 on degenerate grid
			lap0 := przm.HodgeLaplacian0(complex, phi)
			if !bytes.Equal(lap0, div) {
				t.Fatalf("Delta_0(phi) != Adjoint0(Coboundary0(phi)) on %dx%d", M, W)
			}
		}
	}
	t.Log("[PASS] All degenerate geometries (1x1, 1xN, Nx1) satisfied adjoint duality and invariants.")
}
