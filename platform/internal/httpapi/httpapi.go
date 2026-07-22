package httpapi

import (
	"context"
	"encoding/json"
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
func NewHandler(commands *registration.CommandService, queries *registration.QueryService, instance func() api.Instance, exposeSpec bool) http.Handler {
	mux := http.NewServeMux()
	cfg := api.Config()
	if !exposeSpec {
		cfg.OpenAPIPath = ""
		cfg.DocsPath = ""
		cfg.SchemasPath = ""
	}
	api.Register(humago.New(mux, cfg), api.Handlers{
		Instance: instance,
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

// NewPassiveHandler returns the handler a Passive instance serves.
//
// A Passive instance binds its own address for its whole lifetime, not only
// while it is Active, so an operator can ask it about itself at any time. What it
// may answer is bounded by what it actually knows: it holds no Primary Ownership
// and its projection is not authoritative, so answering a domain query from it
// would make the ownership rule meaningless. It answers for itself, and refuses
// everything else.
//
// The refusal is a 503 naming the machine's other instance, because a caller that
// reached this instance reached the wrong one rather than a broken one. That is
// also why the domain paths are registered at all: an unregistered path would
// answer 404, which says the operation does not exist rather than that it is not
// served here.
//
// It takes no registration services because there are none to take. A Passive
// instance's projection is opened for catch-up so it can take over quickly, and
// it is never wired to a listener.
func NewPassiveHandler(instance func() api.Instance) http.Handler {
	mux := http.NewServeMux()
	cfg := api.Config()
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	api.RegisterInstance(humago.New(mux, cfg), instance)

	for _, pattern := range api.DomainPaths {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
			refuse(w, instance())
		})
	}
	return mux
}

// refuse answers one domain request on a Passive instance.
//
// The body is application/problem+json, the same content type huma produces for
// the Active instance's errors, so a client parses one error shape whichever
// instance it reached.
func refuse(w http.ResponseWriter, instance api.Instance) {
	detail := "this instance is passive and does not serve domain operations"
	if instance.PeerAddress != "" {
		detail += "; the machine's other instance holds Primary Ownership and serves at " + instance.PeerAddress
	}
	body, err := json.Marshal(problem{
		Type:   "about:blank",
		Title:  "instance_passive",
		Status: http.StatusServiceUnavailable,
		Detail: detail,
		// The instance is carried whole so a caller learns which one refused it
		// without a second request to an instance it already knows is passive.
		Instance: &instance,
	})
	if err != nil {
		http.Error(w, "instance_passive", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(body)
}

// problem is the RFC 7807 body a Passive instance refuses with. It mirrors the
// fields huma emits, plus the refusing instance.
type problem struct {
	Type     string        `json:"type"`
	Title    string        `json:"title"`
	Status   int           `json:"status"`
	Detail   string        `json:"detail"`
	Instance *api.Instance `json:"instance,omitempty"`
}
