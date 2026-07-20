package apispecifications

import (
	"path/filepath"
	"strings"

	conv "github.com/duh-rpc/openapi-markdown.go"
	platformapi "github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/utils/atomicfile"
	"gopkg.in/yaml.v3"
)

// contractPath and markdownPath are the OpenAPI specification and companion
// documentation this package generates. They are relative to the working
// directory go test and go run set to a package's own directory, so these resolve
// correctly from both this package (conformance-tests/api-specifications) and
// conformance-tests/cmd, the two directories that invoke this generation - both
// two levels below the repository root.
var (
	contractPath = filepath.Join("..", "..", "api-specifications", "openapi.yaml")
	markdownPath = filepath.Join("..", "..", "api-specifications", "openapi.md")
)

// generateOpenAPISpec generates the OpenAPI specification from the platform's huma
// API and writes it to api-specifications/openapi.yaml, unconditionally. The
// specification is a build artifact of platform/api, not a hand-maintained file,
// so every run regenerates it from the current platform API rather than checking
// it for staleness - it is always exactly what the code that serves the API
// currently describes. platformapi.OpenAPIYAML downgrades huma's native OpenAPI
// 3.1 to 3.0.3 for the SDK toolchain. It returns the generated YAML so dependent
// artifacts like the Markdown companion can reuse it without rereading disk.
func generateOpenAPISpec() ([]byte, error) {
	yamlBytes, err := platformapi.OpenAPIYAML()
	if err != nil {
		return nil, err
	}
	if err := atomicfile.WriteFile(contractPath, yamlBytes, 0o644); err != nil {
		return nil, err
	}
	return yamlBytes, nil
}

// generateOpenAPIMarkdown renders the OpenAPI specification YAML as compact
// Markdown for review and writes it to api-specifications/openapi.md.
func generateOpenAPIMarkdown(yamlBytes []byte) error {
	preparedBytes, err := prepareYAMLForMarkdown(yamlBytes)
	if err != nil {
		return err
	}
	cfg := platformapi.Config()
	res, err := conv.Convert(preparedBytes, conv.ConvertOptions{
		Title:               cfg.Info.Title,
		Description:         cfg.Info.Description,
		EnableSharedSchemas: true,
	})
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(markdownPath, res.Markdown, 0o644)
}

// prepareYAMLForMarkdown transforms the OpenAPI specification YAML to satisfy
// openapi-markdown.go's constraint that all response schemas must be top-level
// component references ($ref) rather than inline schemas. For endpoints that
// return lists (such as []Registration), huma generates inline array schemas
// (type: array with items: $ref). This function wraps those array schemas into
// named component definitions in components/schemas so openapi-markdown.go can
// successfully resolve and document them without failing.
func prepareYAMLForMarkdown(yamlBytes []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(yamlBytes, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return yamlBytes, nil
	}

	doc := root.Content[0]
	schemasNode := findMapValue(findMapValue(doc, "components"), "schemas")
	pathsNode := findMapValue(doc, "paths")
	if schemasNode == nil || schemasNode.Kind != yaml.MappingNode || pathsNode == nil || pathsNode.Kind != yaml.MappingNode {
		return yamlBytes, nil
	}

	for i := 1; i < len(pathsNode.Content); i += 2 {
		wrapPathListSchemas(pathsNode.Content[i], schemasNode)
	}

	return yaml.Marshal(&root)
}

func wrapPathListSchemas(pathItem, schemasNode *yaml.Node) {
	if pathItem == nil || pathItem.Kind != yaml.MappingNode {
		return
	}
	for j := 1; j < len(pathItem.Content); j += 2 {
		methodNode := pathItem.Content[j]
		if methodNode.Kind != yaml.MappingNode {
			continue
		}
		responsesNode := findMapValue(methodNode, "responses")
		wrapResponseListSchemas(responsesNode, schemasNode)
	}
}

func wrapResponseListSchemas(responsesNode, schemasNode *yaml.Node) {
	if responsesNode == nil || responsesNode.Kind != yaml.MappingNode {
		return
	}
	for k := 1; k < len(responsesNode.Content); k += 2 {
		statusNode := responsesNode.Content[k]
		if statusNode.Kind != yaml.MappingNode {
			continue
		}
		schemaNode := findMapValue(findMapValue(findMapValue(statusNode, "content"), "application/json"), "schema")
		wrapArraySchemaNode(schemaNode, schemasNode)
	}
}

func wrapArraySchemaNode(schemaNode, schemasNode *yaml.Node) {
	if schemaNode == nil || schemaNode.Kind != yaml.MappingNode {
		return
	}
	typeNode := findMapValue(schemaNode, "type")
	if typeNode == nil || typeNode.Value != "array" {
		return
	}
	itemsNode := findMapValue(schemaNode, "items")
	if itemsNode == nil || itemsNode.Kind != yaml.MappingNode {
		return
	}
	refNode := findMapValue(itemsNode, "$ref")
	if refNode == nil || refNode.Value == "" {
		return
	}

	parts := strings.Split(refNode.Value, "/")
	baseName := parts[len(parts)-1]
	if baseName == "" {
		return
	}
	listSchemaName := baseName + "List"

	if findMapValue(schemasNode, listSchemaName) == nil {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: listSchemaName}
		copySchema := *schemaNode
		copyContent := make([]*yaml.Node, len(schemaNode.Content))
		copy(copyContent, schemaNode.Content)
		copySchema.Content = copyContent
		schemasNode.Content = append(schemasNode.Content, keyNode, &copySchema)
	}

	schemaNode.Content = []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "$ref"},
		{Kind: yaml.ScalarNode, Value: "#/components/schemas/" + listSchemaName},
	}
}

func findMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
