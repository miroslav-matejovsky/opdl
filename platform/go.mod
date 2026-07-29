module github.com/miroslav-matejovsky/opdl/platform

go 1.26.5

require (
	github.com/danielgtaylor/huma/v2 v2.39.0
	github.com/miroslav-matejovsky/opdl/utils v0.0.0
	github.com/nats-io/nats-server/v2 v2.14.3
	github.com/nats-io/nats.go v1.52.0
	github.com/stretchr/testify v1.11.1
)

require (
	github.com/antithesishq/antithesis-sdk-go v0.7.0-default-no-op // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/minio/highwayhash v1.0.4 // indirect
	github.com/nats-io/jwt/v2 v2.8.2 // indirect
	github.com/nats-io/nkeys v0.4.16 // indirect
	github.com/nats-io/nuid v1.0.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/crypto v0.53.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/miroslav-matejovsky/opdl/utils => ../utils
