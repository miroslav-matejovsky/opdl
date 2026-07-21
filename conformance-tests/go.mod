module github.com/miroslav-matejovsky/opdl/conformance-tests

go 1.26.5

require (
	github.com/duh-rpc/openapi-markdown.go v0.7.0
	github.com/miroslav-matejovsky/opdl/builder v0.0.0
	github.com/miroslav-matejovsky/opdl/platform v0.0.0
	github.com/miroslav-matejovsky/opdl/utils v0.0.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/basgys/goxml2json v1.1.1-0.20231018121955-e66ee54ceaad // indirect
	github.com/buger/jsonparser v1.1.1 // indirect
	github.com/danielgtaylor/huma/v2 v2.39.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/duh-rpc/openapi-schema.go v0.9.0 // indirect
	github.com/pb33f/jsonpath v0.1.2 // indirect
	github.com/pb33f/libopenapi v0.28.2 // indirect
	github.com/pb33f/libopenapi-validator v0.9.2 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.37.0 // indirect
)

replace github.com/miroslav-matejovsky/opdl/builder => ../builder

replace github.com/miroslav-matejovsky/opdl/platform => ../platform

replace github.com/miroslav-matejovsky/opdl/utils => ../utils
