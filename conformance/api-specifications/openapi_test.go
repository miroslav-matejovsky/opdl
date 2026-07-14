package apispecifications

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
)

// TestSchemaForStruct is a small unit test of the reflection-to-schema mapping in
// isolation from the platform API or any generated artifact, exercising the check
// helpers directly. It documents that a struct becomes an object whose plain
// scalar fields are required.
func TestSchemaForStruct(t *testing.T) {
	schema := schemaFor(reflect.TypeFor[platformapi.Status]())

	require.Equal(t, "object", schema.Type)
	require.Equal(t, "string", schema.Properties["status"].Type)
	require.Equal(t, "string", schema.Properties["message"].Type)
	require.ElementsMatch(t, []string{"status", "message"}, schema.Required)
}
