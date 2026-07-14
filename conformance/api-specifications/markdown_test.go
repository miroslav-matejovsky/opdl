package apispecifications

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRenderMarkdown is a small unit test of the Markdown renderer in isolation
// from the platform API, exercising the check helpers directly on a hand-built
// document. It documents the compact shape: one section per operation with its
// response payload's field table inlined directly beneath it.
func TestRenderMarkdown(t *testing.T) {
	doc := openAPIDoc{
		Info: openAPIInfo{Title: "Example API", Version: "1.2.3", Description: "An example."},
		Paths: map[string]map[string]openAPIOperation{
			"/widgets": {
				"get": {
					Summary: "List widgets",
					Responses: map[string]openAPIResponse{
						"200": {
							Content: map[string]openAPIMediaType{
								"application/json": {Schema: openAPISchema{Ref: "#/components/schemas/Widget"}},
							},
						},
					},
				},
			},
		},
		Components: openAPIComponents{
			Schemas: map[string]openAPISchema{
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

	require.Contains(t, got, "# Example API")
	require.Contains(t, got, "**Version:** 1.2.3")
	require.Contains(t, got, "## GET /widgets")
	require.Contains(t, got, "List widgets")
	require.Contains(t, got, "**Response `200`** — `Widget`")
	require.Contains(t, got, "| `count` | integer |  |")
	require.Contains(t, got, "| `name` | string | yes |")
	require.NotContains(t, got, "## Schemas")
}
