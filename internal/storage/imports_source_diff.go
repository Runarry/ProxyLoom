package storage

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type sourceDiffValue struct {
	value  json.RawMessage
	secret bool
}

// Only the closed typed IR supplies field names and secret annotations. No
// parser metadata or user-selected JSON path can reach this read projection.
func sourceFieldChanges(beforeName string, before *ir.Node, afterName string, after *ir.Node) []imports.SourceFieldChange {
	values := func(name string, node *ir.Node) map[string]sourceDiffValue {
		out := map[string]sourceDiffValue{}
		if node != nil {
			value, _ := json.Marshal(name)
			out["/name"] = sourceDiffValue{value: value}
			flattenSourceFields(reflect.ValueOf(node), "/node", out)
		}
		return out
	}
	a, b := values(beforeName, before), values(afterName, after)
	paths := map[string]bool{}
	for path := range a {
		paths[path] = true
	}
	for path := range b {
		paths[path] = true
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	changes := []imports.SourceFieldChange{}
	for _, path := range ordered {
		old, next := a[path], b[path]
		if len(old.value) == 0 {
			old.value = json.RawMessage("null")
		}
		if len(next.value) == 0 {
			next.value = json.RawMessage("null")
		}
		if bytes.Equal(old.value, next.value) {
			continue
		}
		change := imports.SourceFieldChange{FieldPath: path}
		if old.secret || next.secret {
			change.SecretChanged = true
		} else {
			change.Before, change.After = old.value, next.value
		}
		changes = append(changes, change)
	}
	return changes
}

func flattenSourceFields(value reflect.Value, path string, out map[string]sourceDiffValue) {
	if !value.IsValid() {
		return
	}
	for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	if value.Type() == reflect.TypeFor[ir.Secret]() {
		encoded, _ := json.Marshal(value.Interface())
		out[path] = sourceDiffValue{value: encoded, secret: true}
		return
	}
	if value.Kind() == reflect.Struct {
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" || name == "-" || name == "origin" || name == "schema_version" {
				continue
			}
			flattenSourceFields(value.Field(i), path+"/"+name, out)
		}
		return
	}
	encoded, _ := json.Marshal(value.Interface())
	out[path] = sourceDiffValue{value: encoded}
}
