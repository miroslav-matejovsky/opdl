package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

// NewHandler wires the registration services into the platform's huma API and
// returns the http.Handler the runtime serves. The command service publishes
// proposals; the query service answers from this node's projection. The caller
// owns the server lifecycle.
//
// The split is the asynchronous contract made structural. A POST reaches only
// the journal and learns nothing about the outcome; a GET reaches only the local
// projection and never waits on the journal. Nothing here can decide a
// registration, which is why nothing here can answer a conflict immediately.
//
// When exposeSpec is true, huma's generated /openapi, /docs, and /schemas
// endpoints are served alongside the operations; otherwise only the operations
// are, and the authoritative specification is the checked-in
// api-specifications/openapi.yaml.
func NewHandler(commands *registration.CommandService, queries *registration.QueryService, exposeSpec bool) http.Handler {
	mux := http.NewServeMux()
	cfg := api.Config()
	if !exposeSpec {
		cfg.OpenAPIPath = ""
		cfg.DocsPath = ""
		cfg.SchemasPath = ""
	}
	api.Register(humago.New(mux, cfg), api.Handlers{
		Create: func(ctx context.Context, req api.RegistrationRequest) (api.ProposalAccepted, error) {
			receipt, err := commands.Create(ctx, req)
			if err != nil {
				return api.ProposalAccepted{}, err
			}
			return api.ProposalAccepted{ProposalID: receipt.ProposalID, Sequence: receipt.Sequence}, nil
		},
		List:      queries.List,
		Get:       queries.Get,
		Conflicts: queries.Conflicts,
	})
	return mux
}
