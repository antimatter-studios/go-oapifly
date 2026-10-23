package oapifly

import (
	"reflect"
	"strings"
)

// EmptyLists returns v with every nil slice and nil map it would encode replaced by an empty
// one, so a response encodes [] and {} where a handler left a list unset (#28).
//
// The description this package writes says a slice or map field without omitempty is a
// required array or object, never null: a client gets T[] and needs no null check. Go's
// encoding/json writes a nil slice or map as null, so the promise has to be kept when the
// response is encoded, not left to every handler. Call EmptyLists on a response just before
// encoding it.
//
// It follows encoding/json's view of the value: exported fields only, fields tagged
// json:"-" are left alone, and an omitempty field stays nil, since it is omitted rather than
// written as null. Nested structs, pointers, slices, arrays and map values are walked. A field
// where null means something different from empty belongs in its own, explicitly nullable
// field, not a nil list.
//
// v is not modified: the result is a copy wherever something had to change.
func EmptyLists[T any](v T) T {
	in := reflect.ValueOf(&v).Elem()
	out := emptied(in, map[uintptr]bool{})
	return out.Interface().(T)
}

// emptied returns v, or a copy of it with its nil lists made empty.
func emptied(v reflect.Value, seen map[uintptr]bool) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := emptied(v.Elem(), seen)
		out := reflect.New(v.Type()).Elem()
		out.Set(inner)
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		// A value that reaches itself through pointers is walked once.
		if seen[v.Pointer()] {
			return v
		}
		seen[v.Pointer()] = true
		inner := emptied(v.Elem(), seen)
		delete(seen, v.Pointer())
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(inner)
		return out
	case reflect.Slice:
		if v.IsNil() {
			if v.Type().Elem().Kind() == reflect.Uint8 {
				// A []byte is a base64 string, not a list: nil encodes as null and there is
				// no empty string to substitute that would mean the same thing.
				return v
			}
			return reflect.MakeSlice(v.Type(), 0, 0)
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(emptied(v.Index(i), seen))
		}
		return out
	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(emptied(v.Index(i), seen))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return reflect.MakeMap(v.Type())
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), emptied(iter.Value(), seen))
		}
		return out
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			f := v.Type().Field(i)
			if !f.IsExported() && !f.Anonymous {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			field := out.Field(i)
			if !field.CanSet() {
				continue
			}
			if isNilList(field) && hasOption(tagOptions(tag), "omitempty") {
				// Omitted when empty, so never written as null.
				continue
			}
			field.Set(emptied(field, seen))
		}
		return out
	}
	return v
}

func isNilList(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.IsNil()
	}
	return false
}

func tagOptions(tag string) string {
	_, opts, _ := strings.Cut(tag, ",")
	return opts
}
