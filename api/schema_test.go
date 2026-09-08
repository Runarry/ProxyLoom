package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func document(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(OpenAPI(), &document); err != nil {
		t.Fatal(err)
	}
	return document
}
func object(t *testing.T, value any) map[string]any {
	t.Helper()
	out, ok := value.(map[string]any)
	if !ok {
		t.Fatal("expected contract object")
	}
	return out
}

func TestCompileEveryOpenAPIComponentOffline(t *testing.T) {
	contract, err := compiled()
	if err != nil {
		t.Fatal(err)
	}
	if len(contract.schemas) < 100 {
		t.Fatalf("incomplete P0 schemas: %d", len(contract.schemas))
	}
	if _, err := (denyLoader{}).Load("https://example.invalid/schema"); err == nil {
		t.Fatal("external loader accepted")
	}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if key == "$ref" {
					ref, ok := child.(string)
					if !ok || !strings.HasPrefix(ref, "#/components/") {
						t.Fatal("nonlocal schema reference")
					}
				}
				if key == "$id" || key == "$dynamicRef" {
					t.Fatal("contract must keep one local resolution base")
				}
				visit(child)
			}
		case []any:
			for _, child := range value {
				visit(child)
			}
		}
	}
	visit(document(t))
}

func TestSDDRouteInventoryAndAuthenticationSurfaces(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "fixtures", "api", "routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var expected map[string][]string
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	implementedData, err := os.ReadFile(filepath.Join("..", "fixtures", "api", "implemented-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var implemented map[string][]string
	if err := json.Unmarshal(implementedData, &implemented); err != nil {
		t.Fatal(err)
	}
	doc := document(t)
	if doc["openapi"] != "3.1.0" {
		t.Fatal("OpenAPI 3.1 contract required")
	}
	paths := object(t, doc["paths"])
	actual := map[string][]string{}
	seenIDs := map[string]bool{}
	operations := 0
	for path, raw := range paths {
		entry := object(t, raw)
		for _, method := range []string{"get", "post", "patch", "put", "delete", "head", "options"} {
			raw, ok := entry[method]
			if !ok {
				continue
			}
			operation := object(t, raw)
			actual[path] = append(actual[path], method)
			operations++
			id, ok := operation["operationId"].(string)
			if !ok || id == "" || seenIDs[id] {
				t.Fatalf("invalid operation ID on %s %s", method, path)
			}
			seenIDs[id] = true
			status := "contract-only"
			for _, mounted := range implemented[path] {
				if mounted == method {
					status = "implemented"
				}
			}
			if operation["x-implementation-status"] != status {
				t.Fatalf("route implementation inventory differs: %s %s", method, path)
			}
			if len(object(t, operation["responses"])) == 0 {
				t.Fatal("operation without typed responses")
			}
			security := operation["security"]
			if security == nil {
				security = doc["security"]
			}
			encoded, _ := json.Marshal(security)
			switch {
			case strings.HasPrefix(path, "/internal/v1/"):
				if string(encoded) != `[{"RunnerMTLS":[]}]` {
					t.Fatalf("internal mTLS isolation: %s", path)
				}
			case strings.HasPrefix(path, "/s/"):
				if string(encoded) != "[]" || operation["x-authentication-surface"] != "subscription-token" {
					t.Fatal("subscription inherited management authorization")
				}
				auth := object(t, operation["x-token-authentication"])
				if auth["required"] != true || auth["in"] != "path" || auth["parameter"] != "token" || auth["accepts_session_cookie"] != false {
					t.Fatal("path token boundary missing")
				}
				description, _ := operation["description"].(string)
				if !strings.Contains(strings.ToLower(description), "database") || !strings.Contains(description, "503") {
					t.Fatal("public database fail-closed contract missing")
				}
				found := false
				for _, p := range operation["parameters"].([]any) {
					parameter := object(t, p)
					if parameter["name"] == "token" && parameter["in"] == "path" && parameter["required"] == true {
						found = true
					}
				}
				if !found {
					t.Fatal("token path parameter missing")
				}
			case path == "/api/v1/setup" || path == "/api/v1/auth/login":
				if string(encoded) != "[]" {
					t.Fatal("bootstrap exception missing")
				}
			default:
				if string(encoded) != `[{"SessionCookie":[]}]` {
					t.Fatalf("management auth mismatch on %s", path)
				}
			}
		}
	}
	for _, methods := range expected {
		sort.Strings(methods)
	}
	for _, methods := range actual {
		sort.Strings(methods)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("route inventory differs: expected %d paths, actual %d paths", len(expected), len(actual))
	}
	for path, methods := range implemented {
		for _, method := range methods {
			found := false
			for _, candidate := range actual[path] {
				if candidate == method {
					found = true
				}
			}
			if !found {
				t.Fatalf("implemented route is not in contract: %s %s", method, path)
			}
		}
	}
	t.Logf("checked %d individual P0 operations on %d paths", operations, len(paths))
}

func TestIndependentContractFixtures(t *testing.T) {
	root := filepath.Join("..", "fixtures", "api")
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest []struct {
		File   string `json:"file"`
		Schema string `json:"schema"`
		Valid  bool   `json:"valid"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range manifest {
		t.Run(fixture.File, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, fixture.File))
			if err != nil {
				t.Fatal(err)
			}
			var value any
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			err = Validate(fixture.Schema, value)
			if (err == nil) != fixture.Valid {
				t.Fatalf("fixture schema verdict: %v", err)
			}
		})
	}
}

func TestRevisionAndCounterExactWireRange(t *testing.T) {
	for _, schema := range []string{"Revision", "Counter"} {
		for _, value := range []string{"1", "9007199254740993", "9223372036854775807"} {
			if err := Validate(schema, value); err != nil {
				t.Fatalf("%s rejected valid decimal %s: %v", schema, value, err)
			}
		}
		for _, value := range []any{"", "01", "-1", "1.0", "1e0", "9223372036854775808", json.Number("1"), nil} {
			if err := Validate(schema, value); err == nil {
				t.Fatalf("%s accepted invalid counter type/range", schema)
			}
		}
	}
	if Validate("Counter", "0") != nil || Validate("Revision", "0") == nil {
		t.Fatal("zero Counter/positive Revision distinction")
	}
}
