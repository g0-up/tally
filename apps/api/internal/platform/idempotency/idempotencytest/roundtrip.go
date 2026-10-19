// Package idempotencytest checks that a result stored by idempotency.Once replays unchanged.
package idempotencytest

import (
	"bytes"
	"encoding"
	"encoding/json"
	"reflect"
	"testing"
)

// samples are tried in order on types that decode themselves (IDs, times, dates, decimals, …);
// the first one the type accepts is kept.
var samples = []string{
	`"0b8d3c2e-5f4a-4c1e-9a7b-2d6e8f1a3c5b"`, `"2026-03-04T05:06:07.123456+07:00"`, `"2026-03-04"`, `"12.5"`, `"7"`,
	`"s"`, `7`, `true`, `{}`, `[]`,
}

// maxDepth stops the filler on recursive types.
const maxDepth = 8

// Filled returns a T whose exported fields, at every depth, hold non-zero values: two elements
// per slice, one entry per map, set pointers. Types that decode themselves get the first sample
// they accept.
func Filled[T any](t testing.TB) T {
	t.Helper()
	var v T
	fill(reflect.ValueOf(&v).Elem(), 0)
	return v
}

// AssertRoundTrip fails t unless view(v) encodes to the same JSON as view of v after it was
// encoded and decoded, as Once stores and replays it. view is what the client receives (the
// response DTO, or v itself).
func AssertRoundTrip[T, V any](t testing.TB, v T, view func(T) V) {
	t.Helper()
	stored, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	var replayed T
	if err := json.Unmarshal(stored, &replayed); err != nil {
		t.Fatalf("decode %T: %v", v, err)
	}
	want, err := json.Marshal(view(v))
	if err != nil {
		t.Fatalf("encode view of %T: %v", v, err)
	}
	got, err := json.Marshal(view(replayed))
	if err != nil {
		t.Fatalf("encode replayed view of %T: %v", v, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%T does not survive a JSON round trip:\n first: %s\nreplay: %s", v, want, got)
	}
}

var (
	jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

func fill(v reflect.Value, depth int) {
	if depth > maxDepth {
		return
	}
	if decodesItself(v.Type()) {
		if !v.CanSet() {
			return
		}
		for _, s := range samples {
			p := reflect.New(v.Type())
			if json.Unmarshal([]byte(s), p.Interface()) == nil && !p.Elem().IsZero() {
				v.Set(p.Elem())
				return
			}
		}
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), depth+1)
		v.Set(p)
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Field(i)
			// JSON promotes the fields of an embedded struct even when its type is unexported.
			if f.CanSet() || v.Type().Field(i).Anonymous && f.Kind() == reflect.Struct {
				fill(f, depth+1)
			}
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := range 2 {
			fill(s.Index(i), depth+1)
		}
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			fill(v.Index(i), depth+1)
		}
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, depth+1)
		fill(e, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.String:
		v.SetString("s")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(3)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(3)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Interface:
		if v.NumMethod() == 0 {
			v.Set(reflect.ValueOf("s"))
		}
	}
}

// decodesItself reports a type with its own JSON or text decoding, not one promoted from an
// embedded field (a struct embedding a decimal still has fields of its own to fill).
func decodesItself(t reflect.Type) bool {
	p := reflect.PointerTo(t)
	if !p.Implements(jsonUnmarshaler) && !p.Implements(textUnmarshaler) {
		return false
	}
	if t.Kind() != reflect.Struct {
		return true
	}
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && (reflect.PointerTo(f.Type).Implements(jsonUnmarshaler) || reflect.PointerTo(f.Type).Implements(textUnmarshaler)) {
			return false
		}
	}
	return true
}
