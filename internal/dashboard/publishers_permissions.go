package dashboard

import (
	"context"
	"errors"
	"net/http"
	"time"

	"services/internal/publishers"
)

// refreshAppPermissions is called only during explicit setup actions, never by
// local status polling. Callers hold m.mu. Cached metadata guides the UI; each
// setup request rechecks the App and the selected installation before writes.
func (m *publisherIntegration) refreshAppPermissions(ctx context.Context, state *publishers.Snapshot) error {
	permission, checkErr := m.provider.CheckPermissions(ctx, appCredentials(state.App))
	state.App.PermissionsChecked = checkErr == nil
	state.App.VariablesWrite = checkErr == nil && permission.VariablesWrite
	if checkErr == nil {
		state.App.SettingsURL = permission.SettingsURL
	}
	if err := m.store.Save(*state); err != nil {
		return errors.New("Cannot persist GitHub permission verification")
	}
	if checkErr != nil {
		return errors.New("Cannot verify GitHub App permissions. Check GitHub access and try Check GitHub permissions again.")
	}
	return nil
}

func (m *publisherIntegration) requireAppPermissions(ctx context.Context, state *publishers.Snapshot) error {
	if err := m.refreshAppPermissions(ctx, state); err != nil {
		return err
	}
	if !state.App.VariablesWrite {
		return errors.New("Grant Variables: Read and write in the GitHub App settings, save, then check permissions again. Existing installations must approve updated access.")
	}
	return nil
}

func (s *Server) publisherGitHubPermissions(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !authDecode(w, r, &input) {
		return
	}
	m.mu.Lock()
	state, err := m.store.Load()
	if err != nil {
		m.mu.Unlock()
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	if state.App.ID == 0 {
		m.mu.Unlock()
		http.Error(w, "Connect the GitHub App first", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	err = m.refreshAppPermissions(ctx, &state)
	m.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	s.publisherStatus(w, r)
}
