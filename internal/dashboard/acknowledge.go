package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

// SetOperationAcknowledger connects the Docker-aware queue recovery gate.
// Configure before serving requests; a store alone must never release this gate.
func (s *Server) SetOperationAcknowledger(fn func(context.Context, string) error) {
	s.acknowledgeOperation = fn
}

func (s *Server) acknowledge(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	if !s.durableAvailable(w) {
		return
	}
	if s.acknowledgeOperation == nil {
		http.Error(w, "Operation recovery is unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.acknowledgeOperation(ctx, r.PathValue("id")); err != nil {
		status := http.StatusConflict
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		http.Error(w, "Cannot acknowledge interruption: "+err.Error(), status)
		return
	}
	s.durableOperation(w, r)
}
