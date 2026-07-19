package apispecifications

import (
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
	"github.com/miroslav-matejovsky/opdl/utils/jsonfields"
)

// contractPath is the OpenAPI specification this package generates. It is relative
// to the working directory go test and go run set to a package's own directory, so
// this resolves correctly from both this package (conformance-tests/api-specifications)
// and conformance-tests/cmd, the two directories that invoke this generation — both two
// levels below the repository root.
var contractPath = filepath.Join("..", "..", "api-specifications", "openapi.yaml")

// markdownPath is the compact, human-readable companion to contractPath: the same
// specification rendered as Markdown instead of YAML, for reviewing API changes
// without reading OpenAPI. See renderMarkdown.
var markdownPath = filepath.Join(filepath.Dir(contractPath), "openapi.md")

const (
	schemaTypeArray   = "array"
	schemaTypeObject  = "object"
	schemaFormatInt32 = "int32"
)

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
	if err := atomicfile.WriteFile(contractPath, yamlBytes, 0o644); err != nil {
		return err
	}

	return atomicfile.WriteFile(markdownPath, renderMarkdown(doc), 0o644)
}

// buildOpenAPIDoc turns the platform's API contract into an OpenAPI 3.0.3
// document. JSON schemas are derived from the Go request, path-parameter, and
// response types by reflection, so the specification describes exactly the JSON
// the platform accepts and serves. Both generated artifacts render from this one
// document, so they cannot disagree.
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
	builder := schemaBuilder{doc: &doc}

	for _, op := range c.Operations {
		operation := openAPIOperation{
			OperationID: op.OperationID,
			Summary:     op.Summary,
			Responses:   map[string]openAPIResponse{},
		}
		for _, parameter := range op.PathParameters {
			operation.Parameters = append(operation.Parameters, openAPIParameter{
				Name:     parameter.Name,
				In:       "path",
				Required: parameter.Required,
				Schema:   builder.schemaFor(reflect.TypeOf(parameter.Type)),
			})
		}
		if body := op.RequestBody; body != nil {
			operation.RequestBody = &openAPIRequestBody{
				Required: body.Required,
				Content: map[string]openAPIMediaType{
					"application/json": {Schema: builder.schemaFor(reflect.TypeOf(body.Type))},
				},
			}
		}
		for _, response := range op.Responses {
			documented := openAPIResponse{Description: op.Summary}
			if response.Body != nil {
				documented.Content = map[string]openAPIMediaType{
					"application/json": {Schema: builder.schemaFor(reflect.TypeOf(response.Body))},
				}
			}
			operation.Responses[strconv.Itoa(response.Status)] = documented
		}

		methods := doc.Paths[op.Path]
		if methods == nil {
			methods = map[string]openAPIOperation{}
			doc.Paths[op.Path] = methods
		}
		methods[strings.ToLower(op.Method)] = operation
	}

	return doc
}

// schemaBuilder derives document schemas and turns named struct types into
// reusable components. Slices remain inline array wrappers so a top-level array
// references its named item schema instead of creating an unnamed component.
type schemaBuilder struct {
	doc *openAPIDoc
}

func (b schemaBuilder) schemaFor(t reflect.Type) openAPISchema {
	if t == nil {
		return openAPISchema{}
	}
	if t.Kind() == reflect.Pointer {
		schema := b.schemaFor(t.Elem())
		schema.Nullable = true
		return schema
	}
	if t.Kind() == reflect.Struct && t.Name() != "" {
		b.addComponent(t)
		return openAPISchema{Ref: "#/components/schemas/" + t.Name()}
	}
	return b.inlineSchemaFor(t)
}

func (b schemaBuilder) addComponent(t reflect.Type) {
	if _, ok := b.doc.Components.Schemas[t.Name()]; ok {
		return
	}
	// Install a placeholder before recursing so a self-referential type terminates.
	b.doc.Components.Schemas[t.Name()] = openAPISchema{}
	b.doc.Components.Schemas[t.Name()] = b.structSchema(t)
}

func (b schemaBuilder) inlineSchemaFor(t reflect.Type) openAPISchema {
	switch t.Kind() {
	case reflect.String:
		return openAPISchema{Type: "string"}
	case reflect.Bool:
		return openAPISchema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return integerSchema(t.Kind())
	case reflect.Float32, reflect.Float64:
		return openAPISchema{Type: "number"}
	case reflect.Slice, reflect.Array:
		item := b.schemaFor(t.Elem())
		return openAPISchema{Type: schemaTypeArray, Items: &item}
	case reflect.Struct:
		return b.structSchema(t)
	default:
		return openAPISchema{}
	}
}

// schemaFor builds an inline OpenAPI schema for a Go type. The document builder
// uses reusable components for named structs; this helper keeps focused schema
// tests independent of an OpenAPI document.
func schemaFor(t reflect.Type) openAPISchema {
	doc := openAPIDoc{Components: openAPIComponents{Schemas: map[string]openAPISchema{}}}
	return (schemaBuilder{doc: &doc}).inlineSchemaFor(t)
}

func integerSchema(kind reflect.Kind) openAPISchema {
	schema := openAPISchema{Type: "integer"}
	switch kind {
	case reflect.Uint8:
		schema.Format = schemaFormatInt32
		schema.Minimum = intPointer(0)
		schema.Maximum = intPointer(255)
	case reflect.Uint16:
		schema.Format = schemaFormatInt32
		schema.Minimum = intPointer(0)
		schema.Maximum = intPointer(65535)
	case reflect.Int64, reflect.Uint64:
		schema.Format = "int64"
	case reflect.Int8, reflect.Int16, reflect.Int32:
		schema.Format = schemaFormatInt32
	default:
		return schema
	}
	return schema
}

func intPointer(value int) *int {
	return &value
}

// structSchema builds an object schema from a struct type, mapping each exported
// field to a property under its JSON name. A field is required unless it is a
// pointer or carries the json "omitempty" option.
func (b schemaBuilder) structSchema(t reflect.Type) openAPISchema {
	props := map[string]openAPISchema{}
	var required []string
	fields, err := jsonfields.Fields(t)
	if err != nil {
		return openAPISchema{Type: schemaTypeObject, Properties: props, Required: required}
	}
	for _, f := range fields {
		props[f.Name] = b.schemaFor(f.Type)
		if !f.OmitEmpty && f.Type.Kind() != reflect.Pointer {
			required = append(required, f.Name)
		}
	}
	sort.Strings(required)
	return openAPISchema{Type: schemaTypeObject, Properties: props, Required: required}
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
	OperationID string                     `yaml:"operationId,omitempty"`
	Summary     string                     `yaml:"summary,omitempty"`
	Parameters  []openAPIParameter         `yaml:"parameters,omitempty"`
	RequestBody *openAPIRequestBody        `yaml:"requestBody,omitempty"`
	Responses   map[string]openAPIResponse `yaml:"responses"`
}

type openAPIParameter struct {
	Name     string        `yaml:"name"`
	In       string        `yaml:"in"`
	Required bool          `yaml:"required"`
	Schema   openAPISchema `yaml:"schema"`
}

type openAPIRequestBody struct {
	Required bool                        `yaml:"required"`
	Content  map[string]openAPIMediaType `yaml:"content"`
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
	Format     string                   `yaml:"format,omitempty"`
	Minimum    *int                     `yaml:"minimum,omitempty"`
	Maximum    *int                     `yaml:"maximum,omitempty"`
	Nullable   bool                     `yaml:"nullable,omitempty"`
	Properties map[string]openAPISchema `yaml:"properties,omitempty"`
	Items      *openAPISchema           `yaml:"items,omitempty"`
	Required   []string                 `yaml:"required,omitempty"`
}
