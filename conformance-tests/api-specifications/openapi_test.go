package apispecifications

import (
	"testing"

	conv "github.com/duh-rpc/openapi-markdown.go"
	"github.com/stretchr/testify/require"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
)

// TestOpenAPIReportsStableOperations makes the generated SDK method names visible
// in this module, where a change to the platform's contract is intentional and
// clear, and confirms the document is the downgraded OpenAPI 3.0.3 the SDK
// toolchain (Kiota) consumes.
func TestOpenAPIReportsStableOperations(t *testing.T) {
	doc, err := platformapi.OpenAPIYAML()
	require.NoError(t, err)
	yaml := string(doc)

	require.Contains(t, yaml, "openapi: 3.0.3")
	for _, id := range []string{
		"getInstance",
		"getHealth",
		"getHealthLive",
		"getHealthReady",
		"getHealthHA",
	} {
		require.Contains(t, yaml, "operationId: "+id)
	}
}

// TestOpenAPIMarkdownIsGenerated confirms that the Markdown companion can be
// rendered from the generated OpenAPI specification and contains the expected
// documentation structure.
func TestOpenAPIMarkdownIsGenerated(t *testing.T) {
	yamlBytes, err := platformapi.OpenAPIYAML()
	require.NoError(t, err)

	preparedBytes, err := prepareYAMLForMarkdown(yamlBytes)
	require.NoError(t, err)

	cfg := platformapi.Config()
	res, err := conv.Convert(preparedBytes, conv.ConvertOptions{
		Title:               cfg.Info.Title,
		Description:         cfg.Info.Description,
		EnableSharedSchemas: true,
	})
	require.NoError(t, err)

	md := string(res.Markdown)
	require.Contains(t, md, "# "+cfg.Info.Title)
	require.Contains(t, md, "GET /instance")
	require.Contains(t, md, "Report this instance's identity and state")
}
