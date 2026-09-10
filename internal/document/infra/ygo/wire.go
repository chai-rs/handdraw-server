package ygo

import (
	"math"
	"unicode/utf8"

	"github.com/chai-rs/handdraw-server/internal/document/model"
	yencoding "github.com/reearth/ygo/encoding"
)

// admission walks bounded V1 framing before ygo allocates a candidate. Only the
// supported root map and plain Y.Map/Y.Array/Y.Text content may enter schema 1.
func admission(data []byte) error {
	r := wireReader{decoder: yencoding.NewDecoder(data)}

	groups := r.count()
	for range groups {
		structs := r.count()
		r.uint()
		r.uint()

		for range structs {
			r.item()

			if r.err != nil {
				return r.err
			}
		}
	}

	groups = r.count()
	for range groups {
		r.uint()

		ranges := r.count()
		for range ranges {
			r.uint()
			r.uint()
		}
	}

	if r.err != nil || r.decoder.Remaining() != 0 {
		return model.ErrInvalidDocument
	}

	return nil
}

type wireReader struct {
	decoder *yencoding.Decoder
	err     error
	budget  uint64
}

func (r *wireReader) uint() uint64 {
	if r.err != nil {
		return 0
	}

	value, err := r.decoder.ReadVarUint()
	r.err = err

	return value
}

func (r *wireReader) count() uint64 {
	n := r.uint()

	r.budget += n
	if n > 200000 || r.budget > 200000 {
		r.err = model.ErrInvalidDocument
		return 0
	}

	return n
}

func (r *wireReader) str() string {
	if r.err != nil {
		return ""
	}

	value, err := r.decoder.ReadVarString()

	r.err = err
	if !utf8.ValidString(value) {
		r.err = model.ErrInvalidDocument
	}

	return value
}

func (r *wireReader) item() {
	info, err := r.decoder.ReadUint8()
	if err != nil {
		r.err = err
		return
	}

	tag := info & 31
	if tag == 0 || tag == 10 {
		r.uint()
		return
	}

	if info&128 != 0 {
		r.uint()
		r.uint()
	}

	if info&64 != 0 {
		r.uint()
		r.uint()
	}

	if info&192 == 0 {
		parent := r.uint()
		if parent == 1 {
			if r.str() != "handdraw" || info&32 == 0 {
				r.err = model.ErrInvalidDocument
				return
			}
		} else if parent == 0 {
			r.uint()
			r.uint()
		} else {
			r.err = model.ErrInvalidDocument
			return
		}

		if info&32 != 0 {
			r.str()
		}
	}

	if r.err != nil {
		return
	}

	switch tag {
	case 1:
		r.uint()
	case 4:
		r.str()
	case 7:
		kind := r.uint()
		if kind > 2 {
			r.err = model.ErrInvalidDocument
		}
	case 8:
		count := r.count()
		for range count {
			if r.err != nil {
				return
			}

			r.jsonValue(0)
		}
	default:
		r.err = model.ErrInvalidDocument
	}
}

// ContentAny is restricted to JSON values; undefined, binary and BigInt would
// otherwise change type during projection or cannot enter the native editor.
func (r *wireReader) jsonValue(depth int) {
	if r.err != nil {
		return
	}

	if depth > 64 {
		r.err = model.ErrInvalidDocument
		return
	}

	tag, err := r.decoder.ReadUint8()
	if err != nil {
		r.err = err
		return
	}

	switch tag {
	case 126, 120, 121:
	case 125:
		_, r.err = r.decoder.ReadVarInt()
	case 124:
		v, e := r.decoder.ReadFloat32()

		r.err = e
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			r.err = model.ErrInvalidDocument
		}
	case 123:
		v, e := r.decoder.ReadFloat64()

		r.err = e
		if math.IsNaN(v) || math.IsInf(v, 0) {
			r.err = model.ErrInvalidDocument
		}
	case 119:
		r.str()
	case 117:
		count := r.count()
		for range count {
			r.jsonValue(depth + 1)

			if r.err != nil {
				return
			}
		}
	case 118:
		count := r.count()
		seen := map[string]bool{}

		for range count {
			key := r.str()
			if seen[key] {
				r.err = model.ErrInvalidDocument
				return
			}

			seen[key] = true

			r.jsonValue(depth + 1)

			if r.err != nil {
				return
			}
		}
	default:
		r.err = model.ErrInvalidDocument
	}
}
