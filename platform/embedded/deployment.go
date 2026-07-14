package embedded

import (
	"encoding/json"
	"fmt"

	"github.com/miroslav-matejovsky/opdl/platform/deployment"
)

// Deployment decodes the embedded deployment descriptor. It owns the byte ->
// struct conversion for the data it embeds, so consumers never unmarshal
// DeploymentJSON themselves.
func Deployment() (deployment.Descriptor, error) {
	return decodeDeployment(DeploymentJSON)
}

func decodeDeployment(data []byte) (deployment.Descriptor, error) {
	var d deployment.Descriptor
	if err := json.Unmarshal(data, &d); err != nil {
		return deployment.Descriptor{}, fmt.Errorf("embedded: invalid deployment descriptor: %w", err)
	}
	return d, nil
}
