#!/usr/bin/env python3
# Copyright (c) 2026 Lemon4ksan All rights reserved.
# Use of this source code is governed by a BSD-style
# license that can be found in the LICENSE file.

"""
Independent S-Box ANF & Degree Oracle for AES-128
Milestone 1 Empirical Challenger Verification Script
"""

def gf_mul(a, b):
    """Multiplication in GF(2^8) modulo x^8 + x^4 + x^3 + x + 1 (0x11B)"""
    p = 0
    for _ in range(8):
        if b & 1:
            p ^= a
        hi = a & 0x80
        a = (a << 1) & 0xFF
        if hi:
            a ^= 0x1B
        b >>= 1
    return p

def gf_inv(a):
    """Multiplicative inverse in GF(2^8) via Fermat's Little Theorem: a^254"""
    if a == 0:
        return 0
    res = 1
    base = a
    exp = 254
    while exp > 0:
        if exp & 1:
            res = gf_mul(res, base)
        base = gf_mul(base, base)
        exp >>= 1
    return res

def compute_sbox():
    """Compute standard AES S-box from field inversion and affine mapping"""
    sbox = [0] * 256
    for i in range(256):
        inv = gf_inv(i)
        res = 0
        for bit in range(8):
            b0 = (inv >> bit) & 1
            b4 = (inv >> ((bit + 4) % 8)) & 1
            b5 = (inv >> ((bit + 5) % 8)) & 1
            b6 = (inv >> ((bit + 6) % 8)) & 1
            b7 = (inv >> ((bit + 7) % 8)) & 1
            c_bit = (0x63 >> bit) & 1
            out_bit = b0 ^ b4 ^ b5 ^ b6 ^ b7 ^ c_bit
            res |= (out_bit << bit)
        sbox[i] = res
    return sbox

def fast_mobius_transform(tt):
    """Independent Fast Mobius Transform on 256-element list of {0, 1}"""
    anf = list(tt)
    for i in range(8):
        step = 1 << i
        for j in range(0, 256, 2 * step):
            for k in range(step):
                anf[j + step + k] ^= anf[j + k]
    return anf

def popcount(x):
    return (x).bit_count()

def analyze_anf(anf):
    max_deg = -1
    active = 0
    hist = [0] * 9
    for u in range(256):
        if anf[u] == 1:
            deg = popcount(u)
            hist[deg] += 1
            active += 1
            max_deg = max(max_deg, deg)
    top_zero = (anf[255] == 0)
    return {
        'degree': max(max_deg, 0),
        'active': active,
        'top_zero': top_zero,
        'hist': hist
    }

def main():
    print("=" * 60)
    print("INDEPENDENT AES S-BOX ANF & DEGREE VERIFICATION ORACLE")
    print("=" * 60)

    sbox = compute_sbox()
    print(f"[+] S-Box generated: S[0]=0x{sbox[0]:02x}, S[1]=0x{sbox[1]:02x}, S[255]=0x{sbox[255]:02x}")
    assert sbox[0] == 0x63
    assert sbox[1] == 0x7c
    assert sbox[255] == 0x16
    print("[+] S-Box verified against NIST FIPS 197 values")

    # 1. Algebraic proof of a_255 = 0
    print("\n[+] Testing algebraic balancedness property:")
    for k in range(8):
        tt = [(sbox[x] >> k) & 1 for x in range(256)]
        hw = sum(tt)
        a255_direct = hw % 2
        print(f"    Coord {k}: Hamming weight = {hw} (balanced), sum mod 2 = {a255_direct}")
        assert hw == 128, f"Coordinate {k} is not balanced: weight={hw}"
        assert a255_direct == 0, f"a_255 != 0 for coordinate {k}"

    print("[+] Formal Invariant Confirmed: By Mobius inversion over GF(2), a_255 = XOR_{x in GF(2^8)} f(x) = wt(f) mod 2.")
    print("    Because S is a bijection, every coordinate is balanced with wt(f) = 128 = 0 mod 2 => a_255 = 0 identically.")

    # 2. Analyze all 8 coordinates
    print("\n[+] Coordinate ANF Profiles:")
    expected_active = [132, 133, 145, 136, 131, 114, 112, 110]
    expected_deg7 = [3, 4, 5, 4, 3, 2, 2, 2]
    total_deg7 = 0

    for k in range(8):
        tt = [(sbox[x] >> k) & 1 for x in range(256)]
        anf = fast_mobius_transform(tt)
        res = analyze_anf(anf)
        total_deg7 += res['hist'][7]

        print(f"    Coord {k}: deg={res['degree']}, active={res['active']}, a_255={anf[255]}, deg7_count={res['hist'][7]}, hist={res['hist']}")
        assert res['degree'] == 7, f"Coord {k} degree != 7: {res['degree']}"
        assert res['top_zero'] == True, f"Coord {k} top monomial != 0"
        assert res['hist'][8] == 0, f"Coord {k} has active degree 8 monomials"
        assert res['active'] == expected_active[k], f"Coord {k} active mismatch: got {res['active']} want {expected_active[k]}"
        assert res['hist'][7] == expected_deg7[k], f"Coord {k} deg7 mismatch: got {res['hist'][7]} want {expected_deg7[k]}"

    print(f"[+] Total degree 7 monomials across all 8 coordinates: {total_deg7} (expected 25)")
    assert total_deg7 == 25

    # 3. Analyze all 255 non-zero component functions
    print("\n[+] Checking all 255 non-zero component functions c . S(x)...")
    max_component_deg = 0
    non_zero_a255_count = 0
    for c in range(1, 256):
        tt = [popcount(sbox[x] & c) & 1 for x in range(256)]
        # Check balancedness
        hw = sum(tt)
        assert hw == 128, f"Component {c} is not balanced: weight={hw}"
        anf = fast_mobius_transform(tt)
        res = analyze_anf(anf)
        if res['degree'] > max_component_deg:
            max_component_deg = res['degree']
        if anf[255] != 0:
            non_zero_a255_count += 1

    print(f"    Max degree over all 255 components: {max_component_deg}")
    print(f"    Components with a_255 != 0: {non_zero_a255_count}")
    assert max_component_deg <= 7
    assert non_zero_a255_count == 0

    print("\n[+] SUCCESS: All S-Box degree counts and a_255 = 0 invariants verified independently!")

if __name__ == '__main__':
    main()
