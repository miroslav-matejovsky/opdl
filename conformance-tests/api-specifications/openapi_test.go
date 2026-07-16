package apispecifications

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
)

type createWidgetRequest struct {
	Name string  `json:"name"`
	Role *string `json:"role,omitempty"`
}

type widgetResponse struct {
	Name     string `json:"name"`
	ServerID string `json:"server_id"`
}

type platformInstanceStatus struct {
	Machine string `json:"machine"`
	Status  string `json:"status"`
}

type registrationProcess struct {
	PlatformInstances []platformInstanceStatus `json:"platform_instances"`
}

// TestBuildOpenAPIDocCoversFutureOperationShapes verifies that the generic API
// contract can describe the request and response forms planned for registration
// without adding registration-specific generator behavior.
func TestBuildOpenAPIDocCoversFutureOperationShapes(t *testing.T) {
	doc := buildOpenAPIDoc(platformapi.Contract{
		Title: "Example API",
		Operations: []platformapi.Operation{
			{
				Method:      http.MethodPost,
				Path:        "/widgets",
				OperationID: "createWidget",
				Summary:     "Create widget",
				RequestBody: &platformapi.RequestBody{Type: createWidgetRequest{}, Required: true},
				Responses: []platformapi.Response{
					{Status: http.StatusCreated, Body: widgetResponse{}},
					{Status: http.StatusAccepted},
				},
			},
			{
				Method:      http.MethodGet,
				Path:        "/registrations/{unit_type}/{unit_id}/status",
				OperationID: "getRegistrationStatus",
				Summary:     "Get registration status",
				PathParameters: []platformapi.PathParameter{
					{Name: "unit_type", Type: uint8(0), Required: true},
					{Name: "unit_id", Type: uint16(0), Required: true},
				},
				Responses: []platformapi.Response{{Status: http.StatusOK, Body: registrationProcess{}}},
			},
			{
				Method:      http.MethodGet,
				Path:        "/widgets",
				OperationID: "listWidgets",
				Summary:     "List widgets",
				Responses:   []platformapi.Response{{Status: http.StatusOK, Body: []widgetResponse{}}},
			},
		},
	})

	create := doc.Paths["/widgets"]["post"]
	require.Equal(t, "createWidget", create.OperationID)
	require.NotNil(t, create.RequestBody)
	require.True(t, create.RequestBody.Required)
	require.Equal(t, "#/components/schemas/createWidgetRequest", create.RequestBody.Content["application/json"].Schema.Ref)
	require.Equal(t, "#/components/schemas/widgetResponse", create.Responses["201"].Content["application/json"].Schema.Ref)
	require.Empty(t, create.Responses["202"].Content)

	request := doc.Components.Schemas["createWidgetRequest"]
	require.Equal(t, "string", request.Properties["role"].Type)
	require.True(t, request.Properties["role"].Nullable)
	require.NotContains(t, request.Required, "role")
	require.Contains(t, request.Required, "name")

	status := doc.Paths["/registrations/{unit_type}/{unit_id}/status"]["get"]
	require.Equal(t, "getRegistrationStatus", status.OperationID)
	require.Equal(t, []openAPIParameter{
		{
			Name: "unit_type", In: "path", Required: true,
			Schema: openAPISchema{Type: "integer", Format: "int32", Minimum: intPointer(0), Maximum: intPointer(255)},
		},
		{
			Name: "unit_id", In: "path", Required: true,
			Schema: openAPISchema{Type: "integer", Format: "int32", Minimum: intPointer(0), Maximum: intPointer(65535)},
		},
	}, status.Parameters)

	process := doc.Components.Schemas["registrationProcess"]
	require.Equal(t, "array", process.Properties["platform_instances"].Type)
	require.Equal(t, "#/components/schemas/platformInstanceStatus", process.Properties["platform_instances"].Items.Ref)
	require.Equal(t, "object", doc.Components.Schemas["platformInstanceStatus"].Type)

	list := doc.Paths["/widgets"]["get"]
	require.Equal(t, "array", list.Responses["200"].Content["application/json"].Schema.Type)
	require.Equal(t, "#/components/schemas/widgetResponse", list.Responses["200"].Content["application/json"].Schema.Items.Ref)
	require.Equal(t, "object", doc.Components.Schemas["widgetResponse"].Type)
}

// TestPlatformOperationIDIsStable makes generated SDK method names visible to
// this package's tests, where a change to the contract is intentional and clear.
func TestPlatformOperationIDIsStable(t *testing.T) {
	doc := buildOpenAPIDoc(platformapi.Describe())

	require.NotContains(t, doc.Paths, "/")
	require.Equal(t, "registerUnit", doc.Paths["/registrations"]["post"].OperationID)
	require.Equal(t, "listRegistrations", doc.Paths["/registrations"]["get"].OperationID)
	require.Equal(t, "listRegistrationConflicts", doc.Paths["/registrations/conflicts"]["get"].OperationID)
	require.Equal(t, "getRegistrationStatus", doc.Paths["/registrations/{unit_type}/{unit_id}/status"]["get"].OperationID)
}

// TestSchemaForStruct is a small unit test of the reflection-to-schema mapping in
// isolation from the platform API or any generated artifact, exercising the check
// helpers directly. It documents that a struct becomes an object whose plain
// scalar fields are required.
func TestSchemaForStruct(t *testing.T) {
	schema := schemaFor(reflect.TypeFor[platformapi.RegistrationRequest]())

	require.Equal(t, "object", schema.Type)
	require.Equal(t, "integer", schema.Properties["unit_type"].Type)
	require.Equal(t, "integer", schema.Properties["unit_id"].Type)
	require.Equal(t, "string", schema.Properties["unit_type_name_advertised"].Type)
	require.ElementsMatch(t, []string{"unit_type", "unit_id", "unit_type_name_advertised"}, schema.Required)
}
