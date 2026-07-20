package apispecifications

import (
	"testing"

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
		"registerUnit",
		"listRegistrations",
		"listRegistrationConflicts",
		"getRegistrationStatus",
	} {
		require.Contains(t, yaml, "operationId: "+id)
	}
}
