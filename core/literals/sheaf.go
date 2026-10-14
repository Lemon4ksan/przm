package literals

// Character class bitmasks for fast causal classification.
var (
	sheafMode0LUT [256]uint8
	sheafMode1LUT [256]uint8
	sheafMode2LUT [256]uint8
)

func init() {
	initSheafLUTs()
}

// initSheafLUTs builds static 1-byte lookup tables for Modes 0, 1, and 2.
func initSheafLUTs() {
	wsSet := map[byte]bool{' ': true, '\t': true, '\r': true, '\n': true}
	syntaxChars := []byte{
		'{', '}', '[', ']', '(', ')', '<', '>', ':', ';', ',', '.',
		'"', '\'', '`', '=', '+', '-', '*', '/', '%', '&', '|', '^',
		'!', '~', '?', '\\', '#',
	}
	syntaxSet := make(map[byte]bool, len(syntaxChars))
	for _, c := range syntaxChars {
		syntaxSet[c] = true
	}

	for i := 0; i < 256; i++ {
		b := byte(i)
		sheafMode0LUT[b] = classifyByteMode0(b, wsSet, syntaxSet)
		sheafMode1LUT[b] = classifyByteMode1(b)
		sheafMode2LUT[b] = classifyByteMode2(b, wsSet, syntaxSet)
	}
}

// classifyByteMode0 evaluates character class partition for Mode 0.
func classifyByteMode0(b byte, wsSet, syntaxSet map[byte]bool) uint8 {
	if wsSet[b] {
		return 0
	}
	if syntaxSet[b] {
		return 1
	}
	if (b >= 'a' && b <= 'z') || b == '_' {
		return 2
	}
	return 3
}

// classifyByteMode1 evaluates JSON/Log character partition for Mode 1.
func classifyByteMode1(b byte) uint8 {
	if b == '"' || b == ':' {
		return 0
	}
	if (b >= 'a' && b <= 'z') || b == '_' {
		return 1
	}
	if b >= '0' && b <= '9' {
		return 2
	}
	return 3
}

// classifyByteMode2 evaluates binary syntax/content dichotomy for Mode 2.
func classifyByteMode2(b byte, wsSet, syntaxSet map[byte]bool) uint8 {
	if wsSet[b] || syntaxSet[b] {
		return 0
	}
	return 1
}

// ContextClassify determines the fiber class (0..3) for Modes 0, 1, 2.
func ContextClassify(p1 byte, mode uint8) uint8 {
	switch mode {
	case 0:
		return sheafMode0LUT[p1]
	case 1:
		return sheafMode1LUT[p1]
	case 2:
		return sheafMode2LUT[p1]
	default:
		return 3
	}
}

// ClassifyContext determines the fiber class (0..3) for Modes 0, 1, 2.
// It is an idiomatic alias for ContextClassify.
func ClassifyContext(p1 byte, mode uint8) uint8 {
	return ContextClassify(p1, mode)
}

// ContextClassifyOrder2 evaluates joint bigram foliation for Mode 3.
func ContextClassifyOrder2(p2, p1 byte) uint8 {
	if p1 >= '0' && p1 <= '9' {
		if (p2 >= '0' && p2 <= '9') || p2 == '.' || p2 == '-' {
			return 0
		}
		return 1
	}
	if (p1 >= 'a' && p1 <= 'z') || p1 == '_' {
		if (p2 >= 'a' && p2 <= 'z') || p2 == '_' {
			return 2
		}
		return 3
	}
	if p1 == ' ' || p1 == '\t' || p1 == '\r' || p1 == '\n' {
		return 0
	}
	return 1
}

// DecomposeFibers partitions input literals into 4 causal fiber sub-streams.
func DecomposeFibers(src []byte, mode uint8) ([4][]byte, error) {
	if mode > 3 {
		return [4][]byte{}, ErrCorruptedFrame
	}
	var fibers [4][]byte
	var p1, p2 byte
	for i, b := range src {
		var c uint8
		if mode < 3 {
			if i == 0 {
				p1 = 0x00
			}
			c = ContextClassify(p1, mode)
		} else {
			if i == 0 {
				p2, p1 = 0x00, 0x00
			}
			c = ContextClassifyOrder2(p2, p1)
		}
		fibers[c] = append(fibers[c], b)
		p2 = p1
		p1 = b
	}
	return fibers, nil
}

// ReconstituteFoliation reconstructs literals from 4 fibers via causal history.
// It is an alias for ReconstituteFibers.
func ReconstituteFoliation(dst []byte, uLit int, f0, f1, f2, f3 []byte, l0, l1, l2, l3 int, mode uint8) error {
	return ReconstituteFibers(dst, uLit, f0, f1, f2, f3, l0, l1, l2, l3, mode)
}

// ReconstituteFibers reconstructs literals from 4 fibers via causal history.
func ReconstituteFibers(dst []byte, uLit int, f0, f1, f2, f3 []byte, l0, l1, l2, l3 int, mode uint8) error {
	if l0+l1+l2+l3 != uLit || len(dst) < uLit {
		return ErrCorruptedFrame
	}
	if len(f0) < l0 || len(f1) < l1 || len(f2) < l2 || len(f3) < l3 {
		return ErrCorruptedFrame
	}
	if mode > 3 {
		return ErrCorruptedFrame
	}
	if mode == 2 && (l2 != 0 || l3 != 0) {
		return ErrCorruptedFrame
	}

	return runReconstitutionLoop(dst, uLit, f0, f1, f2, f3, l0, l1, l2, l3, mode)
}

// runReconstitutionLoop iterates through causal history, popping from fiber streams.
func runReconstitutionLoop(dst []byte, uLit int, f0, f1, f2, f3 []byte, l0, l1, l2, l3 int, mode uint8) error {
	var ptr0, ptr1, ptr2, ptr3 int
	var p1, p2 byte

	for j := 0; j < uLit; j++ {
		var c uint8
		if mode < 3 {
			c = ContextClassify(p1, mode)
		} else {
			c = ContextClassifyOrder2(p2, p1)
		}

		b, err := popFiberByte(c, f0, f1, f2, f3, &ptr0, &ptr1, &ptr2, &ptr3, l0, l1, l2, l3)
		if err != nil {
			return err
		}
		dst[j] = b
		p2 = p1
		p1 = b
	}

	if ptr0 != l0 || ptr1 != l1 || ptr2 != l2 || ptr3 != l3 {
		return ErrCorruptedFrame
	}
	return nil
}

// popFiberByte extracts a byte from the designated fiber with boundary checks.
func popFiberByte(c uint8, f0, f1, f2, f3 []byte, p0, p1, p2, p3 *int, l0, l1, l2, l3 int) (byte, error) {
	switch c {
	case 0:
		if *p0 >= l0 {
			return 0, ErrCorruptedFrame
		}
		b := f0[*p0]
		*p0++
		return b, nil
	case 1:
		if *p1 >= l1 {
			return 0, ErrCorruptedFrame
		}
		b := f1[*p1]
		*p1++
		return b, nil
	case 2:
		if *p2 >= l2 {
			return 0, ErrCorruptedFrame
		}
		b := f2[*p2]
		*p2++
		return b, nil
	case 3:
		if *p3 >= l3 {
			return 0, ErrCorruptedFrame
		}
		b := f3[*p3]
		*p3++
		return b, nil
	default:
		return 0, ErrCorruptedFrame
	}
}
