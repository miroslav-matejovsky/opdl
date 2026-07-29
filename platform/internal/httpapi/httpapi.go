package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

// ServingMode states what surface this instance's handler serves.
type ServingMode string

const (
	// ModeActive serves the whole API: health, identity, and domain operations.
	ModeActive ServingMode = "active"
	// ModePassive serves health and identity only. Every domain operation is
	// refused with a 503 naming the instance that owns, and no write is accepted.
	ModePassive ServingMode = "passive"
)

// NewPassiveHandler returns the handler a Passive instance serves in ModePassive.
//
// A Passive instance binds its own address for its whole lifetime, not only
// while it is Active, so an operator can ask it about itself at any time. What it
// may answer is bounded by ModePassive: it holds no Primary Ownership and its
// projection is not authoritative, so answering a domain query from it would
// make the ownership rule meaningless. It serves health and identity only, and
// refuses everything else.
//
// The refusal is a 503 naming the machine's other instance, because a caller that
// reached this instance reached the wrong one rather than a broken one. That is
// also why the domain paths are registered at all: an unregistered path would
// answer 404, which says the operation does not exist rather than that it is not
// served here.
//
// It takes no domain services because there are none to take.
func NewPassiveHandler(instance func() api.Instance, started time.Time, leaseView func() api.LeaseView, eventFabric func(context.Context) error) http.Handler {
	return newRefusingHandler(ModePassive, instance, started, leaseView, eventFabric, func(current api.Instance) string {
		detail := "this instance is passive and does not serve domain operations"
		if current.PeerAddress != "" {
			detail += "; the machine's other instance holds Primary Ownership and serves at " + current.PeerAddress
		}
		return detail
	}, "instance_passive")
}

// NewActiveHandler returns the handler the instance holding Primary Ownership
// serves. It answers health and identity for the whole machine.
//
// It is constructed in ModeActive and refuses domain operations for a different
// reason than a Passive instance does. This instance does hold Primary
// Ownership: it is Active, it answers for itself, and there is nothing wrong
// with it. The platform simply has no domain surface — api.DomainPaths is empty
// — so there is nothing here to serve. The refusal is a property of the build
// rather than a state that will pass, which is why it names the platform rather
// than the instance: a caller that retries elsewhere, or later, gets the same
// answer.
//
// eventFabric probes this instance's embedded event fabric for /health. It may
// be nil, which is what the boundary tests serve; a running instance always has
// one, because it starts its broker before it serves anything.
func NewActiveHandler(instance func() api.Instance, started time.Time, leaseView func() api.LeaseView, eventFabric func(context.Context) error) http.Handler {
	return newRefusingHandler(ModeActive, instance, started, leaseView, eventFabric, func(api.Instance) string {
		return "this platform serves no domain operations"
	}, "no_domain_operations")
}

// newRefusingHandler builds the surface an instance serves when it answers for
// itself and refuses everything else: /instance from the live identity, and every
// domain path with a problem response carrying title and the detail describe
// renders.
//
// The domain paths are registered rather than left out on purpose. An
// unregistered path answers 404, which says the operation does not exist rather
// than that it is not served here.
//
// In ModePassive, a structural guard ensures that any non-GET/HEAD request is
// refused even if an operation is omitted from DomainPaths.
func newRefusingHandler(mode ServingMode, instance func() api.Instance, started time.Time, leaseView func() api.LeaseView, eventFabric func(context.Context) error, describe func(api.Instance) string, title string) http.Handler {
	mux := http.NewServeMux()
	cfg := api.Config()
	cfg.OpenAPIPath, cfg.DocsPath, cfg.SchemasPath = "", "", ""
	hapi := humago.New(mux, cfg)
	health := api.NewHealth(instance, started, leaseView, eventFabric)
	health.Instance = instance
	api.RegisterInstance(hapi, instance)
	api.RegisterHealth(hapi, health)

	for _, pattern := range api.DomainPaths {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
			current := instance()
			refuse(w, current, title, describe(current))
		})
	}
	if mode == ModePassive {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				current := instance()
				refuse(w, current, title, describe(current))
				return
			}
			mux.ServeHTTP(w, r)
		})
	}
	return mux
}

// refuse answers one domain request an instance does not serve.
//
// The body is application/problem+json, the same content type huma produces for
// a serving instance's errors, so a client parses one error shape whichever
// instance it reached.
func refuse(w http.ResponseWriter, instance api.Instance, title, detail string) {
	body, err := json.Marshal(problem{
		Type:   "about:blank",
		Title:  title,
		Status: http.StatusServiceUnavailable,
		Detail: detail,
		// The instance is carried whole so a caller learns which one refused it
		// without a second request to an instance it already knows will refuse.
		Instance: &instance,
	})
	if err != nil {
		http.Error(w, title, http.StatusServiceUnavailable)
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
