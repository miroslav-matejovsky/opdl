package conformance_test

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
)

// updateContract, when set, rewrites the checked-in OpenAPI contract from the
// current platform API description instead of asserting against it. Regenerate
// with: go test ./conformance -run TestOpenAPIContract -update
var updateContract = flag.Bool("update", false, "rewrite the generated contract files from the current source")

// contractPath is the OpenAPI contract this test generates and guards, relative
// to the conformance module directory the test runs in.
var contractPath = filepath.Join("..", "contracts", "openapi.yaml")

// TestOpenAPIContract generates the OpenAPI specification from the platform's API
// description and checks it against contracts/openapi.yaml. The contract is a
// build artifact of platform/api, not a hand-maintained file: change the platform
// API and this test fails until the contract is regenerated with -update. That
// keeps the published specification and the code that serves it in lockstep.
func TestOpenAPIContract(t *testing.T) {
	got, err := renderOpenAPI(platformapi.Describe())
	require.NoError(t, err)

	if *updateContract {
		require.NoError(t, os.WriteFile(contractPath, got, 0o644))
		return
	}

	want, err := os.ReadFile(contractPath)
	require.NoError(t, err, "contracts/openapi.yaml missing; generate it with: go test ./conformance -run TestOpenAPIContract -update")
	require.Equal(t, string(want), string(got), "contracts/openapi.yaml is stale; regenerate it with: go test ./conformance -run TestOpenAPIContract -update")
}

// renderOpenAPI turns the platform's API contract into an OpenAPI 3.0.3 document.
// Response schemas are derived from the Go response types by reflection, so the
// specification describes exactly the JSON the platform serves.
func renderOpenAPI(c platformapi.Contract) ([]byte, error) {
	doc := openAPIDoc{
		OpenAPI: "3.0.3",
		Info: openAPIInfo{
			Title:       c.Title,
			Version:     c.Version,
			Description: c.Description,
		},
		Paths:      map[string]map[string]openAPIOperation{},
		Components: openAPIComponents{Schemas: map[string]openAPISchema{}},
	}

	for _, op := range c.Operations {
		operation := openAPIOperation{
			Summary:   op.Summary,
			Responses: map[string]openAPIResponse{},
		}

		response := openAPIResponse{Description: op.Summary}
		if t := reflect.TypeOf(op.SuccessBody); t != nil {
			name := t.Name()
			doc.Components.Schemas[name] = schemaFor(t)
			response.Content = map[string]openAPIMediaType{
				"application/json": {Schema: openAPISchema{Ref: "#/components/schemas/" + name}},
			}
		}
		operation.Responses[strconv.Itoa(op.SuccessStatus)] = response

		methods := doc.Paths[op.Path]
		if methods == nil {
			methods = map[string]openAPIOperation{}
			doc.Paths[op.Path] = methods
		}
		methods[strings.ToLower(op.Method)] = operation
	}

	return yaml.Marshal(doc)
}

// schemaFor builds an OpenAPI schema for a Go type by reflection. It handles the
// JSON-encodable kinds the API uses: strings, booleans, integers, floats, slices,
// pointers, and structs. Unsupported kinds yield an empty (unconstrained) schema.
func schemaFor(t reflect.Type) openAPISchema {
	switch t.Kind() {
	case reflect.String:
		return openAPISchema{Type: "string"}
	case reflect.Bool:
		return openAPISchema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return openAPISchema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return openAPISchema{Type: "number"}
	case reflect.Pointer:
		return schemaFor(t.Elem())
	case reflect.Slice, reflect.Array:
		item := schemaFor(t.Elem())
		return openAPISchema{Type: "array", Items: &item}
	case reflect.Struct:
		return structSchema(t)
	default:
		return openAPISchema{}
	}
}

// structSchema builds an object schema from a struct type, mapping each exported
// field to a property under its JSON name. A field is required unless it is a
// pointer or carries the json "omitempty" option.
func structSchema(t reflect.Type) openAPISchema {
	props := map[string]openAPISchema{}
	var required []string
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		name, omitempty, skip := jsonField(f)
		if skip {
			continue
		}
		props[name] = schemaFor(f.Type)
		if !omitempty && f.Type.Kind() != reflect.Pointer {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	return openAPISchema{Type: "object", Properties: props, Required: required}
}

// jsonField reports a struct field's JSON name, whether it carries omitempty, and
// whether it is excluded from JSON (tag "-"). It falls back to the Go field name
// when no json tag names the field.
func jsonField(f reflect.StructField) (name string, omitempty, skip bool) {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name, false, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, true
	}
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// The types below model the subset of OpenAPI 3.0 the generator emits. Field
// order in the structs fixes the key order in the rendered YAML; maps are emitted
// in sorted key order by the YAML encoder, so the output is deterministic.

type openAPIDoc struct {
	OpenAPI    string                                 `yaml:"openapi"`
	Info       openAPIInfo                            `yaml:"info"`
	Paths      map[string]map[string]openAPIOperation `yaml:"paths"`
	Components openAPIComponents                      `yaml:"components"`
}

type openAPIInfo struct {
	Title       string `yaml:"title"`
	Version     string `yaml:"version"`
	Description string `yaml:"description,omitempty"`
}

type openAPIOperation struct {
	Summary   string                     `yaml:"summary,omitempty"`
	Responses map[string]openAPIResponse `yaml:"responses"`
}

type openAPIResponse struct {
	Description string                      `yaml:"description"`
	Content     map[string]openAPIMediaType `yaml:"content,omitempty"`
}

type openAPIMediaType struct {
	Schema openAPISchema `yaml:"schema"`
}

type openAPIComponents struct {
	Schemas map[string]openAPISchema `yaml:"schemas"`
}

type openAPISchema struct {
	Ref        string                   `yaml:"$ref,omitempty"`
	Type       string                   `yaml:"type,omitempty"`
	Properties map[string]openAPISchema `yaml:"properties,omitempty"`
	Items      *openAPISchema           `yaml:"items,omitempty"`
	Required   []string                 `yaml:"required,omitempty"`
}
