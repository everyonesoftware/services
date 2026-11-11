package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"services/internal/monitor"
)

// These actions change monitoring expectations only, never the Docker inventory.
// The production source is the same server-owned monitor used by GET /api/monitor.
type missingServiceResolver interface {
	Resolve(context.Context, []monitor.ResolutionDecision) (string, error)
	UndoResolution(context.Context, string) error
}

func resolutionInput(w http.ResponseWriter, r *http.Request, input any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		http.Error(w, "Resolution requests require application/json", http.StatusUnsupportedMediaType)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(input)
	if err == nil {
		var extra any
		if trailing := decoder.Decode(&extra); trailing != io.EOF {
			if trailing != nil {
				err = trailing
			} else {
				err = errors.New("multiple JSON values")
			}
		}
	}
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "Invalid resolution request", status)
		return false
	}
	return true
}

func (s *Server) resolveMissing(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	var input struct {
		Decisions []monitor.ResolutionDecision `json:"decisions"`
	}
	if !resolutionInput(w, r, &input) {
		return
	}
	if len(input.Decisions) == 0 || len(input.Decisions) > 100 {
		http.Error(w, "Choose between 1 and 100 missing entries", http.StatusBadRequest)
		return
	}
	resolver, ok := s.source.(missingServiceResolver)
	if !ok {
		http.Error(w, "Monitoring resolution is unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	token, err := resolver.Resolve(ctx, input.Decisions)
	if err != nil {
		resolutionFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		UndoToken string `json:"undoToken"`
	}{UndoToken: token})
}

func (s *Server) undoMissingResolution(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if !resolutionInput(w, r, &input) {
		return
	}
	if input.Token == "" || len(input.Token) > 256 {
		http.Error(w, "An Undo token is required", http.StatusBadRequest)
		return
	}
	resolver, ok := s.source.(missingServiceResolver)
	if !ok {
		http.Error(w, "Monitoring resolution is unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := resolver.UndoResolution(ctx, input.Token); err != nil {
		resolutionFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func resolutionFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, monitor.ErrInvalidResolution):
		http.Error(w, "Invalid resolution. Check the selected decisions and snooze durations.", http.StatusBadRequest)
	case errors.Is(err, monitor.ErrResolutionConflict):
		http.Error(w, "Monitoring changed or Undo expired. Refresh and review the current missing entries before retrying.", http.StatusConflict)
	default:
		// Do not expose transport errors, Docker endpoints, or credentials.
		http.Error(w, "Monitoring resolution is unavailable. Check the Docker connection and refresh before retrying.", http.StatusServiceUnavailable)
	}
}
