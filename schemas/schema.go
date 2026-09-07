// Package schemas embeds the versioned IR contract. Validation never loads files
// or network resources supplied by a caller.
package schemas

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const V1ID = "https://proxyloom.invalid/schemas/ir-v1.schema.json"

//go:embed ir-v1.schema.json
var files embed.FS

var compiled = sync.OnceValues(func() (map[string]*jsonschema.Schema, error) {
	data, err := files.ReadFile("ir-v1.schema.json")
	if err != nil {
		return nil, err
	}
	var doc any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	c.UseLoader(denyLoader{})
	if err := c.AddResource(V1ID, doc); err != nil {
		return nil, err
	}
	defs := doc.(map[string]any)["$defs"].(map[string]any)
	out := make(map[string]*jsonschema.Schema, len(defs))
	for name := range defs {
		sch, err := c.Compile(V1ID + "#/$defs/" + name)
		if err != nil {
			return nil, err
		}
		out[name] = sch
	}
	return out, nil
})

type denyLoader struct{}

func (denyLoader) Load(string) (any, error) {
	return nil, errors.New("schema loading outside the embedded contract is disabled")
}

// Validate validates an already parsed JSON value, with format assertions on.
// Use ir.Decode* at an untrusted JSON boundary to also reject duplicate keys.
// The returned library error may contain input values: do not log it. The IR
// package translates it to stable, value-free diagnostics.
func Validate(definition string, value any) error {
	all, err := compiled()
	if err != nil {
		return errors.New("embedded IR schema could not be compiled")
	}
	sch, ok := all[definition]
	if !ok {
		return errors.New("unknown embedded IR definition")
	}
	return sch.Validate(value)
}

// V1 returns a caller-owned copy of the standalone contract.
func V1() []byte {
	data, _ := files.ReadFile("ir-v1.schema.json")
	return data
}
