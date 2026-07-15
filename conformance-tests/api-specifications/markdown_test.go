package apispecifications

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRenderMarkdown verifies that request bodies precede sorted responses and
// that array payloads render their item fields inline.
func TestRenderMarkdown(t *testing.T) {
	doc := openAPIDoc{
		Info: openAPIInfo{Title: "Example API", Version: "1.2.3", Description: "An example."},
		Paths: map[string]map[string]openAPIOperation{
			"/widgets": {
				"post": {
					Summary: "Create widgets",
					RequestBody: &openAPIRequestBody{
						Required: true,
						Content: map[string]openAPIMediaType{
							"application/json": {Schema: openAPISchema{Ref: "#/components/schemas/CreateWidget"}},
						},
					},
					Responses: map[string]openAPIResponse{
						"202": {},
						"201": {
							Content: map[string]openAPIMediaType{
								"application/json": {Schema: openAPISchema{
									Type: "array", Items: &openAPISchema{Ref: "#/components/schemas/Widget"},
								}},
							},
						},
					},
				},
			},
			"/alpha": {
				"get": {Responses: map[string]openAPIResponse{"200": {}}},
			},
		},
		Components: openAPIComponents{
			Schemas: map[string]openAPISchema{
				"CreateWidget": {
					Type: "object",
					Properties: map[string]openAPISchema{
						"name": {Type: "string"},
					},
					Required: []string{"name"},
				},
				"Widget": {
					Type: "object",
					Properties: map[string]openAPISchema{
						"name":  {Type: "string"},
						"count": {Type: "integer"},
					},
					Required: []string{"name"},
				},
			},
		},
	}

	got := string(renderMarkdown(doc))

	require.Equal(t, got, string(renderMarkdown(doc)))
	require.Contains(t, got, "# Example API")
	require.Contains(t, got, "**Version:** 1.2.3")
	require.Contains(t, got, "## POST /widgets")
	require.Contains(t, got, "**Request body** (required) — `CreateWidget`")
	require.Contains(t, got, "**Response `201`**")
	require.Contains(t, got, "**Response `202`**")
	require.Contains(t, got, "Type: array<`Widget`>")
	require.Contains(t, got, "Items:")
	require.Contains(t, got, "| `count` | integer |  |")
	require.Contains(t, got, "| `name` | string | yes |")
	require.Less(t, strings.Index(got, "## GET /alpha"), strings.Index(got, "## POST /widgets"))
	require.Less(t, strings.Index(got, "**Request body**"), strings.Index(got, "**Response `201`**"))
	require.Less(t, strings.Index(got, "**Response `201`**"), strings.Index(got, "**Response `202`**"))
}
