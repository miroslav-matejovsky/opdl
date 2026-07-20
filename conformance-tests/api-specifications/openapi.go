package apispecifications

import (
	"path/filepath"

	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
)

// contractPath is the OpenAPI specification this package generates. It is relative
// to the working directory go test and go run set to a package's own directory, so
// this resolves correctly from both this package (conformance-tests/api-specifications)
// and conformance-tests/cmd, the two directories that invoke this generation - both two
// levels below the repository root.
var contractPath = filepath.Join("..", "..", "api-specifications", "openapi.yaml")

// generateOpenAPISpec generates the OpenAPI specification from the platform's huma
// API and writes it to api-specifications/openapi.yaml, unconditionally. The
// specification is a build artifact of platform/api, not a hand-maintained file,
// so every run regenerates it from the current platform API rather than checking
// it for staleness - it is always exactly what the code that serves the API
// currently describes. platformapi.OpenAPIYAML downgrades huma's native OpenAPI
// 3.1 to 3.0.3 for the SDK toolchain.
func generateOpenAPISpec() error {
	yamlBytes, err := platformapi.OpenAPIYAML()
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(contractPath, yamlBytes, 0o644)
}
