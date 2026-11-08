package dashboard

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"services/internal/operations"
)

// SetDurableOperations selects production mutation handling even when startup
// validation failed. A missing store/configuration never falls back to closures.
// Call before serving requests.
func (s *Server) SetDurableOperations(store *operations.Store, selfProject string, configurationError error) {
	s.durableProduction = true
	s.durable = store
	s.durableProject = selfProject
	s.mutationError = configurationError
}
func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	// Readiness is independent of queue idleness, including a running self-update.
	status := http.StatusOK
	if s.durableProduction && (s.durable == nil || s.mutationError != nil) {
		status = http.StatusServiceUnavailable
	}
	writeOperationJSON(w, status, map[string]any{"ready": status == http.StatusOK, "version": s.version, "schemaVersion": operations.SchemaVersion, "mutationsEnabled": s.durable != nil && s.mutationError == nil})
}
func (s *Server) durableAvailable(w http.ResponseWriter) bool {
	if s.durable == nil || s.mutationError != nil {
		http.Error(w, "Durable Compose workers are unavailable; monitoring remains enabled", 503)
		return false
	}
	return true
}
func (s *Server) durableOperations(w http.ResponseWriter) {
	if s.durable == nil {
		http.Error(w, "Durable operation journal is unavailable; queue state is unknown", http.StatusServiceUnavailable)
		return
	}
	ops, err := s.durable.List()
	if err != nil {
		http.Error(w, "Cannot read durable operations", 503)
		return
	}
	writeOperationJSON(w, 200, map[string]any{"operations": ops})
}
func (s *Server) durableOperation(w http.ResponseWriter, r *http.Request) {
	if s.durable == nil {
		http.Error(w, "Operation not found", 404)
		return
	}
	o, err := s.durable.Get(r.PathValue("id"))
	if err != nil {
		status := 503
		if errors.Is(err, sql.ErrNoRows) {
			status = 404
		}
		http.Error(w, "Cannot read operation", status)
		return
	}
	writeOperationJSON(w, 200, o)
}
func (s *Server) durableContainerAction(w http.ResponseWriter, r *http.Request, action string) {
	if !s.durableAvailable(w) {
		return
	}
	containers, err := s.actionContainers()
	if err != nil {
		http.Error(w, "Docker engine unavailable", 503)
		return
	}
	for _, c := range containers {
		if c.ID == r.PathValue("id") {
			if c.State != "running" {
				http.Error(w, "Container is not running", 409)
				return
			}
			s.submitDurable(w, r, c.Project, action, c.ID, true)
			return
		}
	}
	http.Error(w, "Container not found", 404)
}
func (s *Server) submitDurable(w http.ResponseWriter, r *http.Request, project, action, target string, stream bool) {
	if !s.durableAvailable(w) {
		return
	}
	if !composeProjectName.MatchString(project) {
		http.Error(w, "Invalid Compose project", 400)
		return
	}
	keys := r.Header.Values("Idempotency-Key")
	key := r.Header.Get("Idempotency-Key")
	if len(keys) > 1 || len(key) > 200 || strings.ContainsAny(key, "\r\n") {
		http.Error(w, "Invalid Idempotency-Key", 400)
		return
	}
	o, err := s.durable.Submit(project, action, target, key)
	if err != nil {
		status := 503
		if errors.Is(err, operations.ErrFull) {
			status = 429
		}
		if errors.Is(err, operations.ErrConflict) {
			status = 409
		}
		http.Error(w, err.Error(), status)
		return
	}
	if o.State == "queued" && project == s.durableProject && action != "pull" {
		if err = s.durable.Change(o.ID, func(v *operations.Operation) { v.SelfUpdate = true }); err != nil {
			http.Error(w, "Cannot persist operation identity", 503)
			return
		}
		o.SelfUpdate = true
	}
	if !stream {
		writeOperationJSON(w, 202, o)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	send := func(kind, message string) bool {
		if err := encoder.Encode(map[string]string{"type": kind, "message": message, "operationId": o.ID}); err != nil {
			return false
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return true
	}
	if !send("accepted", "Operation accepted; waiting for durable worker") {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	previous := []string{}
	for {
		current, err := s.durable.Get(o.ID)
		if err != nil {
			send("error", "Cannot read durable operation; outcome unknown")
			return
		}
		// Find overlap because the persisted log is a bounded sliding window.
		overlap := 0
		for n := min(len(previous), len(current.Messages)); n > 0; n-- {
			match := true
			for i := 0; i < n; i++ {
				if previous[len(previous)-n+i] != current.Messages[i] {
					match = false
					break
				}
			}
			if match {
				overlap = n
				break
			}
		}
		for _, line := range current.Messages[overlap:] {
			if !send("progress", line) {
				return
			}
		}
		previous = current.Messages
		if current.State == "complete" {
			send("complete", "Compose operation completed successfully")
			return
		}
		if current.State == "error" {
			send("error", current.Error)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
