module github.com/miroslav-matejovsky/opdl/conformance

go 1.26.5

require (
	github.com/miroslav-matejovsky/opdl/builder v0.0.0
	github.com/miroslav-matejovsky/opdl/platform v0.0.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/miroslav-matejovsky/opdl/builder => ../builder

replace github.com/miroslav-matejovsky/opdl/platform => ../platform
