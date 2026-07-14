package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/miroslav-matejovsky/opdl/platform/api"
)

// NewHandler builds the platform's HTTP handler. It serves a single status
// endpoint that reports the platform is running as a JSON document.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleStatus)
	return mux
}

// handleStatus answers GET requests with a JSON status document and rejects any
// other method with 405 Method Not Allowed.
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := json.Marshal(api.Status{Status: "ok", Message: "platform is running"})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}
