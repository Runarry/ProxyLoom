package apicontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/api"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

const MaxJSONBytes = 10 << 20
const MaxJSONDepth = 128
const MaxNumberBytes = 128
const MaxNumberExponent = 308

// Decode checks the actual OpenAPI schema before decoding into a typed DTO.
// It never returns JSON/schema-library errors that may contain request values.
// destination must be a fresh DTO; it is assigned only after full validation.
func Decode(data []byte, schemaName string, destination any) error {
	canonical, err := CanonicalRequest(data, schemaName)
	if err != nil {
		return err
	}
	target := reflect.ValueOf(destination)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return NewError(InternalError)
	}
	next := reflect.New(target.Elem().Type())
	if err := json.Unmarshal(canonical, next.Interface()); err != nil {
		var boundary *Error
		if errors.As(err, &boundary) {
			return boundary
		}
		return NewError(MalformedRequest)
	}
	target.Elem().Set(next.Elem())
	return nil
}

// CanonicalRequest supplies the storage idempotency boundary with a validated,
// canonical JSON document. Storage computes its purpose-separated request HMAC;
// callers must not persist or log these bytes because they may contain secrets.
func CanonicalRequest(data []byte, schemaName string) ([]byte, error) {
	value, err := parseJSON(data)
	if err != nil {
		return nil, err
	}
	if err := api.Validate(schemaName, value); err != nil {
		return nil, schemaError(err)
	}
	normalizeNumbers(value)
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, NewError(MalformedRequest)
	}
	return canonical, nil
}

func ReadRequest(request *http.Request, schemaName string, destination any) error {
	mediaType, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" || (params["charset"] != "" && params["charset"] != "utf-8") {
		return NewError(MalformedRequest)
	}
	if request.ContentLength > MaxJSONBytes {
		return NewError(InputLimitExceeded)
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, MaxJSONBytes+1))
	if err != nil {
		return NewError(MalformedRequest)
	}
	return Decode(data, schemaName, destination)
}

func parseJSON(data []byte) (any, error) {
	if len(data) > MaxJSONBytes {
		return nil, NewError(InputLimitExceeded)
	}
	if !utf8.Valid(data) || !validSurrogates(data) {
		return nil, NewError(MalformedRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readJSON(decoder, "", 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, NewError(MalformedRequest)
	}
	return value, nil
}

func readJSON(decoder *json.Decoder, path string, depth int) (any, error) {
	if depth > MaxJSONDepth {
		return nil, NewError(InputLimitExceeded)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, NewError(MalformedRequest, Detail{FieldPath: path})
	}
	delim, container := token.(json.Delim)
	if !container {
		if number, ok := token.(json.Number); ok && !boundedNumber(string(number)) {
			return nil, NewError(MalformedRequest, Detail{FieldPath: path})
		}
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, NewError(MalformedRequest, Detail{FieldPath: path})
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, NewError(MalformedRequest, Detail{FieldPath: path})
			}
			childPath := path
			if api.KnownField(key) {
				childPath += "/" + key
			}
			if _, exists := object[key]; exists {
				return nil, NewError(DuplicateField, Detail{FieldPath: childPath})
			}
			value, err := readJSON(decoder, childPath, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim('}') {
			return nil, NewError(MalformedRequest)
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for decoder.More() {
			value, err := readJSON(decoder, path+"/"+strconv.Itoa(len(array)), depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim(']') {
			return nil, NewError(MalformedRequest)
		}
		return array, nil
	default:
		return nil, NewError(MalformedRequest)
	}
}

func validSurrogates(data []byte) bool {
	inString := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || data[i] != '\\' || i+1 >= len(data) {
			continue
		}
		if data[i+1] != 'u' {
			i++
			continue
		}
		if i+6 > len(data) {
			return false
		}
		n, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
		if err != nil || (n >= 0xdc00 && n <= 0xdfff) {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(data) || data[i+6] != '\\' || data[i+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(data[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
		i += 5
	}
	return true
}

func normalizeNumbers(value any) any {
	switch value := value.(type) {
	case json.Number:
		return json.Number(canonicalNumber(string(value)))
	case map[string]any:
		for key, child := range value {
			value[key] = normalizeNumbers(child)
		}
	case []any:
		for index, child := range value {
			value[index] = normalizeNumbers(child)
		}
	}
	return value
}

// These bounds apply before JSON Schema/big-number parsing. A short hostile
// exponent cannot amplify validation memory use. Normalization below operates
// only on bounded decimal strings, preserving all significant input digits.
func boundedNumber(number string) bool {
	if len(number) > MaxNumberBytes {
		return false
	}
	if index := strings.IndexAny(number, "eE"); index >= 0 {
		exponent, err := strconv.ParseInt(number[index+1:], 10, 32)
		return err == nil && exponent >= -MaxNumberExponent && exponent <= MaxNumberExponent
	}
	return true
}

func canonicalNumber(number string) string {
	negative := strings.HasPrefix(number, "-")
	if negative {
		number = number[1:]
	}
	exponent := 0
	if index := strings.IndexAny(number, "eE"); index >= 0 {
		exponent, _ = strconv.Atoi(number[index+1:])
		number = number[:index]
	}
	if index := strings.IndexByte(number, '.'); index >= 0 {
		exponent -= len(number) - index - 1
		number = number[:index] + number[index+1:]
	}
	number = strings.TrimLeft(number, "0")
	if number == "" {
		return "0"
	}
	trimmed := strings.TrimRight(number, "0")
	exponent += len(number) - len(trimmed)
	number = trimmed
	position := len(number) + exponent
	switch {
	case exponent >= 0:
		number += strings.Repeat("0", exponent)
	case position > 0:
		number = number[:position] + "." + number[position:]
	default:
		number = "0." + strings.Repeat("0", -position) + number
	}
	if negative {
		number = "-" + number
	}
	return number
}

func schemaError(err error) *Error {
	var validation *jsonschema.ValidationError
	if !errors.As(err, &validation) {
		return NewError(InternalError)
	}
	code := MalformedRequest
	details := make([]Detail, 0)
	var visit func(*jsonschema.ValidationError)
	visit = func(current *jsonschema.ValidationError) {
		if len(details) >= 16 {
			return
		}
		path := ""
		for _, part := range current.InstanceLocation {
			path += "/" + part
		}
		// Alternative union branches naturally reject fields belonging to the
		// chosen branch. Do not misreport those rejected alternatives as unknown
		// request fields; use the safe union path and the shape-error code.
		switch current.ErrorKind.(type) {
		case *kind.AnyOf, *kind.OneOf:
			details = append(details, Detail{FieldPath: path})
			return
		}
		if _, ok := current.ErrorKind.(*kind.AdditionalProperties); ok {
			code = UnknownField
			details = append(details, Detail{FieldPath: path})
			return
		}
		if len(current.Causes) != 0 {
			for _, child := range current.Causes {
				visit(child)
			}
			return
		}
		details = append(details, Detail{FieldPath: path})
	}
	visit(validation)
	return NewError(code, details...)
}
