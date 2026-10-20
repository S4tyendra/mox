package webauthn

import (
	"encoding/binary"
	"fmt"
)

type cborValue struct {
	kind  byte // 0 uint, 1 nint, 2 bstr, 3 tstr, 4 array, 5 map
	u     uint64
	b     []byte
	items []cborValue
	pairs []cborPair
}

type cborPair struct {
	k, v cborValue
}

func cborDecode(b []byte) (cborValue, []byte, error) {
	if len(b) == 0 {
		return cborValue{}, nil, fmt.Errorf("empty cbor")
	}
	ib := b[0]
	mt, ai := ib>>5, ib&0x1f
	rest := b[1:]
	var n uint64
	switch ai {
	case 24:
		if len(rest) < 1 {
			return cborValue{}, nil, fmt.Errorf("short cbor")
		}
		n = uint64(rest[0])
		rest = rest[1:]
	case 25:
		if len(rest) < 2 {
			return cborValue{}, nil, fmt.Errorf("short cbor")
		}
		n = uint64(binary.BigEndian.Uint16(rest))
		rest = rest[2:]
	case 26:
		if len(rest) < 4 {
			return cborValue{}, nil, fmt.Errorf("short cbor")
		}
		n = uint64(binary.BigEndian.Uint32(rest))
		rest = rest[4:]
	case 27:
		if len(rest) < 8 {
			return cborValue{}, nil, fmt.Errorf("short cbor")
		}
		n = binary.BigEndian.Uint64(rest)
		rest = rest[8:]
	default:
		if ai > 23 {
			return cborValue{}, nil, fmt.Errorf("cbor ai %d", ai)
		}
		n = uint64(ai)
	}
	switch mt {
	case 0:
		return cborValue{kind: 0, u: n}, rest, nil
	case 1:
		return cborValue{kind: 1, u: n}, rest, nil
	case 2, 3:
		if uint64(len(rest)) < n {
			return cborValue{}, nil, fmt.Errorf("short cbor bytes")
		}
		v := cborValue{kind: mt, b: rest[:n]}
		return v, rest[n:], nil
	case 4:
		items := make([]cborValue, 0, n)
		for i := uint64(0); i < n; i++ {
			item, r, err := cborDecode(rest)
			if err != nil {
				return cborValue{}, nil, err
			}
			items = append(items, item)
			rest = r
		}
		return cborValue{kind: 4, items: items}, rest, nil
	case 5:
		pairs := make([]cborPair, 0, n)
		for i := uint64(0); i < n; i++ {
			k, r, err := cborDecode(rest)
			if err != nil {
				return cborValue{}, nil, err
			}
			v, r, err := cborDecode(r)
			if err != nil {
				return cborValue{}, nil, err
			}
			pairs = append(pairs, cborPair{k, v})
			rest = r
		}
		return cborValue{kind: 5, pairs: pairs}, rest, nil
	default:
		return cborValue{}, nil, fmt.Errorf("cbor major %d", mt)
	}
}

func (v cborValue) int() (int64, bool) {
	switch v.kind {
	case 0:
		return int64(v.u), true
	case 1:
		return -1 - int64(v.u), true
	}
	return 0, false
}

func (v cborValue) bytes() ([]byte, bool) {
	if v.kind == 2 || v.kind == 3 {
		return v.b, true
	}
	return nil, false
}

func (v cborValue) text() (string, bool) {
	if v.kind == 3 {
		return string(v.b), true
	}
	return "", false
}

func (v cborValue) mapGetInt(key int64) (cborValue, bool) {
	if v.kind != 5 {
		return cborValue{}, false
	}
	for _, p := range v.pairs {
		if n, ok := p.k.int(); ok && n == key {
			return p.v, true
		}
	}
	return cborValue{}, false
}

func (v cborValue) mapGetText(key string) (cborValue, bool) {
	if v.kind != 5 {
		return cborValue{}, false
	}
	for _, p := range v.pairs {
		if s, ok := p.k.text(); ok && s == key {
			return p.v, true
		}
	}
	return cborValue{}, false
}

func cborHead(mt byte, n uint64) []byte {
	if n < 24 {
		return []byte{mt<<5 | byte(n)}
	}
	if n < 256 {
		return []byte{mt<<5 | 24, byte(n)}
	}
	if n < 65536 {
		return []byte{mt<<5 | 25, byte(n >> 8), byte(n)}
	}
	b := []byte{mt<<5 | 26, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(n))
	return b
}

func cborEncodeInt(n int64) []byte {
	if n >= 0 {
		return cborHead(0, uint64(n))
	}
	return cborHead(1, uint64(-1-n))
}

func cborEncodeBstr(b []byte) []byte {
	return append(cborHead(2, uint64(len(b))), b...)
}

func cborEncodeTstr(s string) []byte {
	return append(cborHead(3, uint64(len(s))), s...)
}

func cborEncodeMapInt(pairs [][2]cborValue) []byte {
	out := cborHead(5, uint64(len(pairs)))
	for _, p := range pairs {
		out = append(out, cborEncodeValue(p[0])...)
		out = append(out, cborEncodeValue(p[1])...)
	}
	return out
}

func cborEncodeValue(v cborValue) []byte {
	switch v.kind {
	case 0:
		return cborEncodeInt(int64(v.u))
	case 1:
		return cborEncodeInt(-1 - int64(v.u))
	case 2:
		return cborEncodeBstr(v.b)
	case 3:
		return cborEncodeTstr(string(v.b))
	case 5:
		out := cborHead(5, uint64(len(v.pairs)))
		for _, p := range v.pairs {
			out = append(out, cborEncodeValue(p.k)...)
			out = append(out, cborEncodeValue(p.v)...)
		}
		return out
	default:
		return nil
	}
}
