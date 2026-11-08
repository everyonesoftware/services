package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"services/internal/composeupdate"
	"services/internal/groups"
)

// GroupUpdater validates the entire project and updates only changed services.
type GroupUpdater interface {
	UpdateProject(context.Context, []groups.Container) error
}

// composeActionGate serializes all daemon mutations, across projects and Server
// instances. Container actions must use the same gate. A failed acquisition is
// rejected rather than queued against an inventory that can become stale.
var composeActionGate = make(chan struct{}, 1)

func (s *Server) tryComposeAction(_ string) (func(), bool) {
	select {
	case composeActionGate <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-composeActionGate }) }, true
	default:
		return nil, false
	}
}

var composeProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (s *Server) projectUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	selfProject := os.Getenv("SERVICES_PROJECT_NAME")
	if selfProject == "" {
		selfProject = "services"
	}
	if !composeProjectName.MatchString(selfProject) {
		http.Error(w, "Services project identity is not configured safely", http.StatusServiceUnavailable)
		return
	}
	if r.PathValue("name") == selfProject {
		http.Error(w, "The Services project cannot update itself; update it externally", http.StatusConflict)
		return
	}
	updater, ok := s.updater.(GroupUpdater)
	if !ok {
		http.Error(w, "Project updates are not configured", http.StatusServiceUnavailable)
		return
	}
	release, acquired := s.tryComposeAction(r.PathValue("name"))
	if !acquired {
		http.Error(w, "Another Compose action is already running", http.StatusConflict)
		return
	}
	defer release()
	containers, err := s.actionContainers()
	if err != nil {
		http.Error(w, "Docker engine unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.updates != nil {
		s.updates.Apply(containers)
	}
	var members []groups.Container
	for _, container := range containers {
		if container.Project == r.PathValue("name") {
			members = append(members, container)
		}
	}
	if len(members) == 0 {
		http.Error(w, "Project not found", http.StatusNotFound)
		return
	}
	changed := false
	for _, member := range members {
		changed = changed || member.UpdateAvailable
	}
	if !changed {
		http.Error(w, "Project has no available image updates", http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	var streamMu sync.Mutex
	event := func(kind, message string) {
		streamMu.Lock()
		defer streamMu.Unlock()
		_ = encoder.Encode(map[string]string{"type": kind, "message": message})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ctx = composeupdate.WithProgress(ctx, func(message string) { event("progress", message) })
	event("progress", "Starting Compose project update for "+r.PathValue("name"))
	if err := updater.UpdateProject(ctx, members); err != nil {
		event("error", err.Error())
		return
	}
	event("complete", "Compose project update completed successfully")
}
