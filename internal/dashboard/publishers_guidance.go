package dashboard

import (
	"context"
	"net/http"
	"services/internal/githubapp"
	"time"
)

// Optional so existing providers can fail closed without fabricating a subject.
type publisherFederationProvider interface {
	FederationSubject(context.Context, githubapp.App, githubapp.Repository) (string, error)
}

func (s *Server) publisherTailnetGuidance(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		Repository githubapp.Repository `json:"repository"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	repo, err := m.resolveRepository(ctx, &state, input.Repository)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	response := map[string]any{"issuer": "https://token.actions.githubusercontent.com", "subject": "", "tailnet": repositoryTailnet(state, repo.ID)}
	if provider, ok := m.provider.(publisherFederationProvider); ok {
		subject, err := provider.FederationSubject(ctx, appCredentials(state.App), repo)
		if err != nil {
			response["subjectError"] = err.Error()
		} else if subject == "" {
			response["subjectError"] = "GitHub returned no federation subject. Review the repository OIDC settings."
		} else {
			response["subject"] = subject
		}
	} else {
		response["subjectError"] = "This GitHub provider cannot read OIDC customization. Upgrade the provider before registering federation."
	}
	authJSON(w, 200, response)
}
