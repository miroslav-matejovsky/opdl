package apispecifications

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// renderMarkdown turns an OpenAPI document into a compact Markdown summary of the
// API surface: one section per operation, with its request and response payloads
// shown inline as field tables rather than referenced elsewhere. It exists to be skimmed
// during review — a full OpenAPI document is not — so each operation reads
// top-to-bottom without following a link to see what it returns.
func renderMarkdown(doc openAPIDoc) []byte {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", doc.Info.Title)
	if doc.Info.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", doc.Info.Description)
	}
	fmt.Fprintf(&b, "**Version:** %s\n\n", doc.Info.Version)

	for _, r := range sortedRoutes(doc) {
		writeOperation(&b, doc, r)
	}

	return []byte(b.String())
}

// route flattens the OpenAPI document's nested path/method maps into a sortable
// list, so operations render in a deterministic order independent of Go's
// randomized map iteration.
type route struct {
	path, method string
	operation    openAPIOperation
}

func sortedRoutes(doc openAPIDoc) []route {
	routes := make([]route, 0, len(doc.Paths))
	for path, methods := range doc.Paths {
		for method, op := range methods {
			routes = append(routes, route{path: path, method: method, operation: op})
		}
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path != routes[j].path {
			return routes[i].path < routes[j].path
		}
		return routes[i].method < routes[j].method
	})
	return routes
}

// writeOperation renders one "## METHOD /path" section: the summary, request
// body, then each response with its payload inlined directly beneath it.
func writeOperation(b *strings.Builder, doc openAPIDoc, r route) {
	fmt.Fprintf(b, "## %s %s\n\n", strings.ToUpper(r.method), r.path)
	if r.operation.Summary != "" {
		fmt.Fprintf(b, "%s\n\n", r.operation.Summary)
	}
	if r.operation.RequestBody != nil {
		writeRequestBody(b, doc, *r.operation.RequestBody)
	}

	statuses := make([]string, 0, len(r.operation.Responses))
	for status := range r.operation.Responses {
		statuses = append(statuses, status)
	}
	sort.Slice(statuses, func(i, j int) bool {
		left, leftErr := strconv.Atoi(statuses[i])
		right, rightErr := strconv.Atoi(statuses[j])
		if leftErr == nil && rightErr == nil {
			return left < right
		}
		return statuses[i] < statuses[j]
	})

	for _, status := range statuses {
		writeResponse(b, doc, status, r.operation.Responses[status])
	}
}

// writeRequestBody renders an operation's JSON request before its responses.
func writeRequestBody(b *strings.Builder, doc openAPIDoc, body openAPIRequestBody) {
	media, ok := body.Content["application/json"]
	if !ok {
		return
	}

	label := "**Request body**"
	if body.Required {
		label += " (required)"
	}
	if name := schemaName(media.Schema); name != "" {
		fmt.Fprintf(b, "%s — `%s`\n\n", label, name)
	} else {
		fmt.Fprintf(b, "%s\n\n", label)
	}
	writePayload(b, doc, media.Schema)
}

// writeResponse renders one response's status and, if it carries a JSON payload,
// the payload's schema name and field table, resolved from doc.Components.Schemas
// and inlined directly rather than left as a cross-reference.
func writeResponse(b *strings.Builder, doc openAPIDoc, status string, resp openAPIResponse) {
	media, ok := resp.Content["application/json"]
	if !ok {
		fmt.Fprintf(b, "**Response `%s`**\n\n", status)
		return
	}

	if name := schemaName(media.Schema); name != "" {
		fmt.Fprintf(b, "**Response `%s`** — `%s`\n\n", status, name)
	} else {
		fmt.Fprintf(b, "**Response `%s`**\n\n", status)
	}
	writePayload(b, doc, media.Schema)
}

// schemaName returns a $ref's schema name, or "" for an inline (non-reference)
// schema.
func schemaName(s openAPISchema) string {
	if s.Ref == "" {
		return ""
	}
	return s.Ref[strings.LastIndex(s.Ref, "/")+1:]
}

// resolveSchema follows a $ref into doc.Components.Schemas; a non-reference schema
// is returned unchanged.
func resolveSchema(doc openAPIDoc, s openAPISchema) openAPISchema {
	if name := schemaName(s); name != "" {
		return doc.Components.Schemas[name]
	}
	return s
}

// writePayload renders a resolved schema's body: a field table for an object
// (name, type, required), or a one-line type description otherwise.
func writePayload(b *strings.Builder, doc openAPIDoc, schema openAPISchema) {
	schema = resolveSchema(doc, schema)
	if schema.Type == schemaTypeArray && schema.Items != nil {
		fmt.Fprintf(b, "Type: %s\n\n", typeLabel(schema))
		b.WriteString("Items:\n\n")
		writePayload(b, doc, *schema.Items)
		return
	}
	if schema.Type != schemaTypeObject {
		if schema.Type != "" {
			fmt.Fprintf(b, "Type: %s\n\n", typeLabel(schema))
		}
		return
	}

	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}

	fields := make([]string, 0, len(schema.Properties))
	for field := range schema.Properties {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	b.WriteString("| Field | Type | Required |\n")
	b.WriteString("| --- | --- | --- |\n")
	for _, field := range fields {
		req := ""
		if required[field] {
			req = "yes"
		}
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", field, typeLabel(schema.Properties[field]), req)
	}
	b.WriteString("\n")
}

// typeLabel renders a schema as a short inline type label: its schema name for a
// reference, "array<item>" for an array, otherwise its OpenAPI type (or "any" if
// unconstrained).
func typeLabel(s openAPISchema) string {
	switch {
	case s.Ref != "":
		return "`" + schemaName(s) + "`"
	case s.Type == schemaTypeArray && s.Items != nil:
		return "array<" + typeLabel(*s.Items) + ">"
	case s.Type != "":
		return s.Type
	default:
		return "any"
	}
}
