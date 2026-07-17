package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

const (
	errorCodeInvalidRequest = "invalid_request"
	errorCodeNotFound       = "registration_not_found"
	errorCodeUnavailable    = "journal_unavailable"
	errorCodeInternal       = "internal_error"
)

// NewHandler builds the platform's registration HTTP handler from the two sides
// of the use case: the command service that publishes proposals and the query
// service that answers from this node's projection. The caller owns the server
// lifecycle and trusted location setup.
//
// The split is the asynchronous contract made structural. A POST reaches only
// the journal and learns nothing about the outcome; a GET reaches only the local
// projection and never waits on the journal. Nothing here can decide a
// registration, which is why nothing here can answer a conflict immediately.
func NewHandler(commands *registration.CommandService, queries *registration.QueryService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/registrations", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrations(commands, queries, w, r)
	})
	// Register this exact route before the parameterized status route. A conflict
	// query is a collection operation, never a malformed status lookup.
	mux.HandleFunc("/registrations/conflicts", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrationConflicts(queries, w, r)
	})
	mux.HandleFunc("/registrations/", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrationStatus(queries, w, r)
	})
	return mux
}

func handleRegistrationConflicts(queries *registration.QueryService, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	conflicts, err := queries.Conflicts()
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, conflicts)
}

func handleRegistrations(commands *registration.CommandService, queries *registration.QueryService, w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		if !isJSON(r.Header.Get("Content-Type")) {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest)
			return
		}
		var request api.RegistrationRequest
		if err := decodeJSON(r.Body, &request); err != nil {
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest)
			return
		}
		receipt, err := commands.Create(r.Context(), request)
		if err != nil {
			// A journal that will not take the proposal is the one failure the
			// client can act on: nothing was recorded, so retrying is safe and is
			// the right thing to do. Everything else the command service refuses is
			// the request's own fault and will fail again unchanged.
			if errors.Is(err, registration.ErrJournalUnavailable) {
				writeError(w, http.StatusServiceUnavailable, errorCodeUnavailable)
				return
			}
			writeError(w, http.StatusBadRequest, errorCodeInvalidRequest)
			return
		}
		writeJSON(w, http.StatusAccepted, api.ProposalAccepted{
			ProposalID: receipt.ProposalID,
			Sequence:   receipt.Sequence,
		})
	case http.MethodGet:
		writeJSON(w, http.StatusOK, queries.List())
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func handleRegistrationStatus(queries *registration.QueryService, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	proposalID, ok := parseStatusPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, errorCodeNotFound)
		return
	}
	view, found := queries.Get(proposalID)
	if !found {
		writeError(w, http.StatusNotFound, errorCodeNotFound)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// parseStatusPath returns the proposal ID a status path names. A proposal ID is
// opaque here: the domain derives it and this boundary only carries it back, so
// the only rule is that the path names exactly one non-empty segment.
func parseStatusPath(path string) (string, bool) {
	proposalID := strings.TrimPrefix(path, "/registrations/")
	if proposalID == "" || strings.Contains(proposalID, "/") {
		return "", false
	}
	return proposalID, true
}

func decodeJSON(body io.Reader, target any) error {
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON documents")
		}
		return err
	}
	return nil
}

func isJSON(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "application/json"
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, api.Error{Code: code})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
