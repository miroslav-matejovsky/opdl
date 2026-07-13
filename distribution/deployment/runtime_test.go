package deployment_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	deploymentcontract "github.com/miroslav-matejovsky/opdl/distribution/deployment"
)

func runtimePolicy() deploymentcontract.Runtime {
	return deploymentcontract.Runtime{Parameters: []deploymentcontract.Parameter{
		// deployment default with runtime override
		{Key: deploymentcontract.ParamHTTPAddr, Default: "127.0.0.1:8080", Owner: deploymentcontract.OwnerDeployment, Override: true},
		// deployment-only (not overridable)
		{Key: "metrics", Default: "on", Owner: deploymentcontract.OwnerDeployment},
		// runtime-only, required
		{Key: "node_tag", Owner: deploymentcontract.OwnerRuntime, Required: true},
	}}
}

func descriptorWithRuntime(r deploymentcontract.Runtime) deploymentcontract.Descriptor {
	d := validDescriptor()
	d.Runtime = r
	return d
}

func TestResolveAppliesDefaults(t *testing.T) {
	d := descriptorWithRuntime(deploymentcontract.Runtime{Parameters: []deploymentcontract.Parameter{
		{Key: deploymentcontract.ParamHTTPAddr, Default: "127.0.0.1:8080", Owner: deploymentcontract.OwnerDeployment, Override: true},
	}})
	eff, err := deploymentcontract.Resolve(d, deploymentcontract.RuntimeConfig{})
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1:8080", eff.ParamOr(deploymentcontract.ParamHTTPAddr, "x"))
}

func TestResolveRuntimeOverridesDeploymentDefault(t *testing.T) {
	d := descriptorWithRuntime(runtimePolicy())
	rc := deploymentcontract.RuntimeConfig{Values: map[string]string{
		deploymentcontract.ParamHTTPAddr: "0.0.0.0:9000",
		"node_tag":                       "edge-7",
	}}
	eff, err := deploymentcontract.Resolve(d, rc)
	require.NoError(t, err)
	require.Equal(t, "0.0.0.0:9000", eff.ParamOr(deploymentcontract.ParamHTTPAddr, "x"))
	require.Equal(t, "edge-7", eff.ParamOr("node_tag", "x"))
	require.Equal(t, "on", eff.ParamOr("metrics", "x"))
}

func TestResolveRejectsForbiddenOverride(t *testing.T) {
	d := descriptorWithRuntime(runtimePolicy())
	rc := deploymentcontract.RuntimeConfig{Values: map[string]string{"metrics": "off", "node_tag": "x"}}
	_, err := deploymentcontract.Resolve(d, rc)
	require.ErrorContains(t, err, "not overridable")
}

func TestResolveRejectsUnknownParameter(t *testing.T) {
	d := descriptorWithRuntime(runtimePolicy())
	rc := deploymentcontract.RuntimeConfig{Values: map[string]string{"ghost": "x", "node_tag": "y"}}
	_, err := deploymentcontract.Resolve(d, rc)
	require.ErrorContains(t, err, "unknown parameter")
}

func TestResolveRejectsMissingRequired(t *testing.T) {
	d := descriptorWithRuntime(runtimePolicy())
	_, err := deploymentcontract.Resolve(d, deploymentcontract.RuntimeConfig{})
	require.ErrorContains(t, err, "required but has no value")
}

func TestValidateRejectsDuplicateParameter(t *testing.T) {
	d := descriptorWithRuntime(deploymentcontract.Runtime{Parameters: []deploymentcontract.Parameter{
		{Key: "dup", Owner: deploymentcontract.OwnerDeployment},
		{Key: "dup", Owner: deploymentcontract.OwnerDeployment},
	}})
	require.ErrorContains(t, d.Validate(), "declared more than once")
}

func TestValidateRejectsInvalidOwner(t *testing.T) {
	d := descriptorWithRuntime(deploymentcontract.Runtime{Parameters: []deploymentcontract.Parameter{
		{Key: "k", Owner: "nonsense"},
	}})
	require.ErrorContains(t, d.Validate(), "invalid owner")
}
