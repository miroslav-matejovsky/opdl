package main

import (
	"errors"
	"testing"

	apispecifications "github.com/miroslav-matejovsky/opdl/conformance-tests/api-specifications"
	deploymentdescriptors "github.com/miroslav-matejovsky/opdl/conformance-tests/deployment-descriptors"
)

// TestAPISpecification is the module's main API specification check, driving the
// same apispecifications.Run entry point main() calls. It always regenerates the
// OpenAPI specification and the .NET client generated from it, in that order:
//
//	go test ./conformance-tests/cmd
func TestAPISpecification(t *testing.T) {
	err := apispecifications.Run(t.Context())

	if skip, ok := errors.AsType[*apispecifications.SkipError](err); ok {
		t.Skip(skip.Reason)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// TestDeploymentDescriptors is the module's main deployment descriptor conformance
// check, driving the same deploymentdescriptors.Run entry point main() calls.
func TestDeploymentDescriptors(t *testing.T) {
	if err := deploymentdescriptors.Run(); err != nil {
		t.Fatal(err)
	}
}
