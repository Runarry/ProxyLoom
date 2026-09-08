// Package api embeds the HTTP contract. All schema resolution is local and
// independent from handler registration; a documented route need not be mounted.
package api

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

const schemaID = "https://proxyloom.invalid/api/openapi.yaml"

//go:embed openapi.yaml
var files embed.FS

type contract struct {
	schemas map[string]*jsonschema.Schema
	fields  map[string]bool
}

var compiled = sync.OnceValues(func() (*contract, error) {
	data, err := files.ReadFile("openapi.yaml")
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	// Convert YAML integer types into JSON numbers without passing through floats.
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(denyLoader{})
	if err := compiler.AddResource(schemaID, value); err != nil {
		return nil, err
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		return nil, errors.New("contract components missing")
	}
	definitions, ok := components["schemas"].(map[string]any)
	if !ok || len(definitions) == 0 {
		return nil, errors.New("contract schemas missing")
	}
	out := &contract{schemas: make(map[string]*jsonschema.Schema), fields: make(map[string]bool)}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			if properties, ok := value["properties"].(map[string]any); ok {
				for name := range properties {
					out.fields[name] = true
				}
			}
			for _, child := range value {
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(definitions)
	for name := range definitions {
		schema, err := compiler.Compile(schemaID + "#/components/schemas/" + name)
		if err != nil {
			return nil, err
		}
		out.schemas[name] = schema
	}
	return out, nil
})

type denyLoader struct{}

func (denyLoader) Load(string) (any, error) {
	return nil, errors.New("external schema loading is disabled")
}

// Validate validates a parsed value against the actual OpenAPI component.
// Schema errors may contain input values: only the boundary's sanitized errors
// may be returned to clients or logged.
func Validate(name string, value any) error {
	contract, err := compiled()
	if err != nil {
		return errors.New("embedded API contract is invalid")
	}
	schema, ok := contract.schemas[name]
	if !ok {
		return errors.New("unknown API contract schema")
	}
	return schema.Validate(value)
}

// KnownField recognizes fixed contract field names for safe diagnostics.
func KnownField(name string) bool {
	contract, err := compiled()
	return err == nil && contract.fields[name]
}

func OpenAPI() []byte {
	data, _ := files.ReadFile("openapi.yaml")
	return data
}
