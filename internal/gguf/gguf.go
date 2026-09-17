// Package gguf reads the GGUF format: the metadata and tensors of a model.
//
// **It is read rather than imported**, for the same reason as internal/qr and
// the icon: the format is small, documented and still, and a library to read it
// would drag in a dependency tree far larger than the thing it does. Three
// tensor types out of eleven are needed here, and no writing at all.
//
// **What is gained by reading this file rather than the original weights** is
// that it carries the front-end with it: the mel filterbank and the window the
// model was trained with are inside as tensors. A front-end rebuilt by hand
// that differs a little gives a model that works *almost*, and that is the kind
// of fault that does not protest.
//
// Nor is that an abstract worry: measured, CED's window is a 512-point Hann
// that sums to exactly 256 but is **not symmetric** — the last sample is
// 0.000038 rather than zero, that is, it is the *periodic* Hann, which is
// torchaudio's default and not the one written by instinct. Generating it by
// hand would take the symmetric one and get one sample in 512 wrong, with
// nothing to say so.
package gguf

import "fmt"

// Type is a tensor's type, with ggml's values. Three are handled: the others
// exist and we do not know how to read them, and saying so beats guessing.
type Type uint32

const (
	TypeF32  Type = 0
	TypeF16  Type = 1
	TypeQ8_0 Type = 8
)

func (t Type) String() string {
	switch t {
	case TypeF32:
		return "F32"
	case TypeF16:
		return "F16"
	case TypeQ8_0:
		return "Q8_0"
	}
	return fmt.Sprintf("type %d", uint32(t))
}

// Tensor is one tensor of the file: its shape, its type, and the raw bytes. The
// data is not converted on open — a model has a hundred and sixty tensors and
// whoever reads wants one at a time.
type Tensor struct {
	Name string
	// Dims is the shape **as GGUF writes it**, that is, fastest-varying
	// dimension first. It is the opposite order to the one written when reading
	// a paper, and confusing the two gives a transposed matrix that has exactly
	// the right shape.
	Dims []uint64
	Type Type
	data []byte
}

// Count is how many values it holds.
func (t *Tensor) Count() int {
	n := 1
	for _, d := range t.Dims {
		n *= int(d)
	}
	return n
}

// File is an open GGUF.
type File struct {
	Version   uint32
	Meta      map[string]any
	tensors   map[string]*Tensor
	Alignment int
}

// Tensor returns a tensor by name. A wrong name is an error and not an empty
// tensor: a model missing a piece produces plausible numbers.
func (f *File) Tensor(name string) (*Tensor, error) {
	t, ok := f.tensors[name]
	if !ok {
		return nil, fmt.Errorf("gguf: no tensor named %q", name)
	}
	return t, nil
}

// Int reads an integer metadata value, whatever width it was written at.
// Whoever writes the file chooses between uint32 and uint64 without the meaning
// changing, and whoever reads should not have to know which.
func (f *File) Int(key string) (int, error) {
	v, ok := f.Meta[key]
	if !ok {
		return 0, fmt.Errorf("gguf: no metadata %q", key)
	}
	switch n := v.(type) {
	case uint8:
		return int(n), nil
	case int8:
		return int(n), nil
	case uint16:
		return int(n), nil
	case int16:
		return int(n), nil
	case uint32:
		return int(n), nil
	case int32:
		return int(n), nil
	case uint64:
		return int(n), nil
	case int64:
		return int(n), nil
	}
	return 0, fmt.Errorf("gguf: metadata %q is %T, not an integer", key, v)
}

// Float reads a floating-point metadata value, with the same tolerance.
func (f *File) Float(key string) (float64, error) {
	v, ok := f.Meta[key]
	if !ok {
		return 0, fmt.Errorf("gguf: no metadata %q", key)
	}
	switch n := v.(type) {
	case float32:
		return float64(n), nil
	case float64:
		return n, nil
	}
	if i, err := f.Int(key); err == nil {
		return float64(i), nil
	}
	return 0, fmt.Errorf("gguf: metadata %q is %T, not a number", key, v)
}

// Strings reads a metadata value that is a list of strings — the class labels
// arrive that way.
func (f *File) Strings(key string) ([]string, error) {
	v, ok := f.Meta[key]
	if !ok {
		return nil, fmt.Errorf("gguf: no metadata %q", key)
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("gguf: metadata %q is %T, not a list", key, v)
	}
	out := make([]string, len(a))
	for i, e := range a {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("gguf: %q[%d] is %T, not a string", key, i, e)
		}
		out[i] = s
	}
	return out, nil
}
