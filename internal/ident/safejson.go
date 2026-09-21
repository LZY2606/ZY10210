package ident

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
)

// rawMessageType 是 json.RawMessage 的反射类型。
var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// MarshalJSONSafe 序列化任意值，把其中的 NaN/±Inf 浮点数编码为 null。
// 标准 encoding/json 遇到 NaN 会整体返回错误，而预测/仿真序列可能含 NaN。
func MarshalJSONSafe(v any) ([]byte, error) {
	clean := sanitize(reflect.ValueOf(v))
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(clean); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func sanitize(rv reflect.Value) any {
	if !rv.IsValid() {
		return nil
	}
	if rv.Type() == rawMessageType {
		var parsed any
		raw := rv.Bytes()
		if len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return json.RawMessage(raw)
		}
		return cleanAny(parsed)
	}
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return sanitize(rv.Elem())
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return sanitize(rv.Elem())
	case reflect.Struct:
		t := rv.Type()
		m := make(map[string]any, rv.NumField())
		for i := 0; i < rv.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			name, omitempty := f.Name, false
			if tag := f.Tag.Get("json"); tag != "" {
				nameTag := tag
				for j := 0; j < len(tag); j++ {
					if tag[j] == ',' {
						nameTag = tag[:j]
						if tag[j+1:] == "omitempty" {
							omitempty = true
						}
						break
					}
				}
				if nameTag == "-" {
					continue
				}
				if nameTag != "" {
					name = nameTag
				}
			}
			val := sanitize(rv.Field(i))
			if omitempty && isEmpty(rv.Field(i)) {
				continue
			}
			m[name] = val
		}
		return m
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = sanitize(rv.Index(i))
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		iter := rv.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = sanitize(iter.Value())
		}
		return out
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rv.Uint()
	case reflect.String:
		return rv.String()
	default:
		return rv.Interface()
	}
}

func cleanAny(v any) any {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil
		}
		return x
	case map[string]any:
		for k, vv := range x {
			x[k] = cleanAny(vv)
		}
		return x
	case []any:
		for i, vv := range x {
			x[i] = cleanAny(vv)
		}
		return x
	default:
		return v
	}
}

func isEmpty(rv reflect.Value) bool {
	switch rv.Kind() {
	case reflect.String:
		return rv.Len() == 0
	case reflect.Slice, reflect.Array, reflect.Map:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}
