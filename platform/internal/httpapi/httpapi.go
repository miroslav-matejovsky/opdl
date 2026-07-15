package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/miroslav-matejovsky/opdl/platform/api"
	"github.com/miroslav-matejovsky/opdl/platform/internal/registration"
)

const (
	errorCodeConflict       = "registration_key_conflict"
	errorCodeInvalidRequest = "invalid_request"
	errorCodeNotFound       = "registration_not_found"
	errorCodeInternal       = "internal_error"
)

// NewHandler builds the platform's registration HTTP handler from its service
// dependency. The caller owns the server lifecycle and trusted location setup.
func NewHandler(service *registration.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/registrations", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrations(service, w, r)
	})
	// Register this exact route before the parameterized status route. A conflict
	// query is a collection operation, never a malformed status lookup.
	mux.HandleFunc("/registrations/conflicts", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrationConflicts(service, w, r)
	})
	mux.HandleFunc("/registrations/", func(w http.ResponseWriter, r *http.Request) {
		handleRegistrationStatus(service, w, r)
	})
	return mux
}

func handleRegistrationConflicts(service *registration.Service, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	conflicts, err := service.Conflicts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal)
		return
	}
	writeJSON(w, http.StatusOK, conflicts)
}

func handleRegistrations(service *registration.Service, w http.ResponseWriter, r *http.Request) {
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
		if _, err := service.Create(r.Context(), request); err != nil {
			if errors.Is(err, registration.ErrConflict) {
				writeError(w, http.StatusConflict, errorCodeConflict)
				return
			}
			if isValidationError(err) {
				writeError(w, http.StatusBadRequest, errorCodeInvalidRequest)
				return
			}
			writeError(w, http.StatusInternalServerError, errorCodeInternal)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	case http.MethodGet:
		registrations, err := service.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, errorCodeInternal)
			return
		}
		writeJSON(w, http.StatusOK, registrations)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func handleRegistrationStatus(service *registration.Service, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	key, ok := parseStatusPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, errorCodeNotFound)
		return
	}
	view, found, err := service.Get(r.Context(), key)
	if err != nil {
		writeError(w, http.StatusInternalServerError, errorCodeInternal)
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, errorCodeNotFound)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func parseStatusPath(path string) (registration.Key, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/registrations/"), "/")
	if len(parts) != 3 || parts[2] != "status" || parts[0] == "" || parts[1] == "" {
		return registration.Key{}, false
	}
	unitType, err := strconv.ParseUint(parts[0], 10, 8)
	if err != nil {
		return registration.Key{}, false
	}
	unitID, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil {
		return registration.Key{}, false
	}
	return registration.Key{UnitType: uint8(unitType), UnitID: uint16(unitID)}, true
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

func isValidationError(err error) bool {
	return strings.HasPrefix(err.Error(), "registration: unit type") || strings.HasPrefix(err.Error(), "registration: role")
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
