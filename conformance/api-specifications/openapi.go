package apispecifications

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
)

// contractPath is the OpenAPI specification this package generates. It is relative
// to the working directory go test and go run set to a package's own directory, so
// this resolves correctly from both this package (conformance/api-specifications)
// and conformance/cmd, the two directories that invoke this generation — both two
// levels below the repository root.
var contractPath = filepath.Join("..", "..", "api-specifications", "openapi.yaml")

// markdownPath is the compact, human-readable companion to contractPath: the same
// specification rendered as Markdown instead of YAML, for reviewing API changes
// without reading OpenAPI. See renderMarkdown.
var markdownPath = filepath.Join(filepath.Dir(contractPath), "openapi.md")

// generateOpenAPISpec generates the OpenAPI specification from the platform's API
// description and writes it to api-specifications/openapi.yaml and, as a compact
// human-readable companion, api-specifications/openapi.md — unconditionally. The
// specification is a build artifact of platform/api, not a hand-maintained file,
// so every run regenerates both from the current platform API rather than checking
// them for staleness — they are always exactly what the code that serves the API
// currently describes.
func generateOpenAPISpec() error {
	doc := buildOpenAPIDoc(platformapi.Describe())

	yamlBytes, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(contractPath, yamlBytes, 0o644); err != nil {
		return err
	}

	return os.WriteFile(markdownPath, renderMarkdown(doc), 0o644)
}

// buildOpenAPIDoc turns the platform's API contract into an OpenAPI 3.0.3
// document. Response schemas are derived from the Go response types by
// reflection, so the specification describes exactly the JSON the platform
// serves. Both generated artifacts (the YAML specification and its Markdown
// companion) render from this one document, so they cannot disagree.
func buildOpenAPIDoc(c platformapi.Contract) openAPIDoc {
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

	return doc
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
