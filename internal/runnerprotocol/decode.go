package runnerprotocol

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// DecodeStrict rejects duplicate fields, case-insensitive field aliases,
// unknown fields, trailing documents and excessive nesting before assigning a
// typed destination. Semantic validation of required values follows separately.
func DecodeStrict(data []byte, out any) error {
	if len(data) == 0 || len(data) > 15<<20 || !utf8.Valid(data) {
		return ErrInvalid
	}
	target := reflect.ValueOf(out)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return ErrInvalid
	}
	scanner := json.NewDecoder(bytes.NewReader(data))
	scanner.UseNumber()
	if scanShape(scanner, target.Type().Elem(), 0, false) != nil {
		return ErrInvalid
	}
	if _, err := scanner.Token(); err != io.EOF {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	next := reflect.New(target.Type().Elem())
	if decoder.Decode(next.Interface()) != nil {
		return ErrInvalid
	}
	target.Elem().Set(next.Elem())
	return nil
}

type wireField struct {
	Type     reflect.Type
	Required bool
}

func jsonFields(typ reflect.Type) map[string]wireField {
	fields := map[string]wireField{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		parts := strings.Split(field.Tag.Get("json"), ",")
		tag := parts[0]
		required := true
		for _, option := range parts[1:] {
			if option == "omitempty" {
				required = false
			}
		}
		if tag == "-" {
			continue
		}
		inner := field.Type
		for inner.Kind() == reflect.Pointer {
			inner = inner.Elem()
		}
		if field.Anonymous && tag == "" && inner.Kind() == reflect.Struct {
			for key, value := range jsonFields(inner) {
				fields[key] = value
			}
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		fields[tag] = wireField{field.Type, required}
	}
	return fields
}
func scanShape(decoder *json.Decoder, typ reflect.Type, depth int, nullable bool) error {
	if depth > 32 {
		return ErrInvalid
	}
	for typ != nil && typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalid
	}
	if token == nil && !nullable && typ != nil && typ.Kind() != reflect.Interface {
		return ErrInvalid
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		var fields map[string]wireField
		if typ != nil && typ.Kind() == reflect.Struct {
			fields = jsonFields(typ)
		}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return ErrInvalid
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
			var child reflect.Type
			childNullable := false
			if fields != nil {
				field, found := fields[key]
				if !found {
					return ErrInvalid
				}
				child = field.Type
				childNullable = field.Required && child.Kind() == reflect.Pointer
			} else if typ != nil && typ.Kind() == reflect.Map {
				child = typ.Elem()
			}
			if err = scanShape(decoder, child, depth+1, childNullable); err != nil {
				return err
			}
		}
		for key, field := range fields {
			if field.Required && !seen[key] {
				return ErrInvalid
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		var child reflect.Type
		if typ != nil && (typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array) {
			child = typ.Elem()
		}
		for decoder.More() {
			if scanShape(decoder, child, depth+1, false) != nil {
				return ErrInvalid
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func DecodeFrozenPayload(data []byte) (FrozenPayload, error) {
	var value FrozenPayload
	if DecodeStrict(data, &value) != nil || ValidateConfigPayload(value) != nil {
		return FrozenPayload{}, ErrInvalid
	}
	return value, nil
}
