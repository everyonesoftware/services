package dashboard

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"

	"services/internal/composeupdate"
	"services/internal/groups"
)

// GroupUpdater validates the entire project and updates only changed services.
type GroupUpdater interface {
	UpdateProject(context.Context, []groups.Container) error
}

var composeProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func projectUpdateGuard(project string) (string, int) {
	selfProject := os.Getenv("SERVICES_PROJECT_NAME")
	if selfProject == "" {
		selfProject = "services"
	}
	if !composeProjectName.MatchString(selfProject) {
		return "Services project identity is not configured safely", http.StatusServiceUnavailable
	}
	if project == selfProject {
		return "The Services project cannot update itself; update it externally", http.StatusConflict
	}
	return "", 0
}

func (s *Server) projectUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	project := r.PathValue("name")
	if message, status := projectUpdateGuard(project); status != 0 {
		http.Error(w, message, status)
		return
	}
	updater, ok := s.updater.(GroupUpdater)
	if !ok {
		http.Error(w, "Project updates are not configured", http.StatusServiceUnavailable)
		return
	}
	operation, accepted := composeOperations.submit(project, func(ctx context.Context, progress func(string)) error {
		// Both identity and inventory may have changed while this job was waiting.
		if message, status := projectUpdateGuard(project); status != 0 {
			return errors.New(message)
		}
		containers, err := s.actionContainers()
		if err != nil {
			return errors.New("Docker engine unavailable")
		}
		if s.updates != nil {
			s.updates.Apply(containers)
		}
		var members []groups.Container
		changed := false
		for _, container := range containers {
			if container.Project == project {
				members = append(members, container)
				changed = changed || container.UpdateAvailable
			}
		}
		if len(members) == 0 {
			return errors.New("Project not found")
		}
		if !changed {
			return errors.New("Project has no available image updates")
		}
		progress("Starting Compose project update for " + project)
		ctx = composeupdate.WithProgress(ctx, progress)
		if err := updater.UpdateProject(ctx, members); err != nil {
			return err
		}
		progress("Compose project update completed successfully")
		return nil
	})
	if !accepted {
		http.Error(w, "Compose operation queue is full; retry later", http.StatusTooManyRequests)
		return
	}
	writeOperationJSON(w, http.StatusAccepted, operation)
}
