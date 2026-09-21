package ident

import (
	"fmt"
	"math"
	"reflect"
)

// json cannot carry NaN/+Inf. We use an unlikely finite sentinel while
// marshaling and convert it back after unmarshaling, so traces keep NaNs at
// missing/undetermined samples across export/import.
var nanSentinel = math.Float64frombits(0x7fe0deadbeef0001)
var posInfSentinel = math.Float64frombits(0x7fe0deadbeef0002)
var negInfSentinel = math.Float64frombits(0xfee0deadbeef0003)

// sanitizeForJSON replaces non-finite float64 values in place.
func sanitizeForJSON(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			sanitizeForJSON(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				sanitizeForJSON(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sanitizeForJSON(v.Index(i))
		}
	case reflect.Float64, reflect.Float32:
		f := v.Float()
		switch {
		case math.IsNaN(f):
			v.SetFloat(nanSentinel)
		case f == math.Inf(1):
			v.SetFloat(posInfSentinel)
		case f == math.Inf(-1):
			v.SetFloat(negInfSentinel)
		}
	}
}

func restoreFromJSON(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			restoreFromJSON(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				restoreFromJSON(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			restoreFromJSON(v.Index(i))
		}
	case reflect.Float64, reflect.Float32:
		switch v.Float() {
		case nanSentinel:
			v.SetFloat(math.NaN())
		case posInfSentinel:
			v.SetFloat(math.Inf(1))
		case negInfSentinel:
			v.SetFloat(math.Inf(-1))
		}
	}
}

// MarshalJSONSafe marshals after swapping NaN/Inf for sentinels. It never
// mutates v: a reflect deep copy is sanitized instead.
func MarshalJSONSafe(v any) ([]byte, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Ptr {
		return nil, fmt.Errorf("MarshalJSONSafe 需要指针入参")
	}
	cp := reflect.New(rv.Elem().Type())
	cp.Elem().Set(rv.Elem())
	sanitizeForJSON(cp.Elem())
	return jsonMarshal(cp.Elem().Interface())
}

// UnmarshalJSONSafe decodes and restores NaN/Inf sentinels.
func UnmarshalJSONSafe(data []byte, v any) error {
	if err := jsonUnmarshal(data, v); err != nil {
		return err
	}
	restoreFromJSON(reflect.ValueOf(v).Elem())
	return nil
}
