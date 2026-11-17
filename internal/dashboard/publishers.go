package dashboard

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"services/internal/auth"
	"services/internal/githubapp"
	"services/internal/publishers"
)

type publisherProvider interface {
	ExchangeManifest(context.Context, string) (githubapp.App, error)
	CheckPermissions(context.Context, githubapp.App) (githubapp.PermissionStatus, error)
	VerifyRepositoryPermissions(context.Context, githubapp.App, githubapp.Repository) error
	Repositories(context.Context, githubapp.App) ([]githubapp.Repository, error)
	Inspect(context.Context, githubapp.App, githubapp.Repository) (githubapp.Inspection, error)
	Configure(context.Context, githubapp.App, githubapp.Repository, string, map[string]string) error
	SetEnabled(context.Context, githubapp.App, githubapp.Repository, bool) error
	DispatchTest(context.Context, githubapp.App, githubapp.Repository, string, string) error
}
type publisherIntegration struct {
	mu       sync.Mutex
	store    *publishers.Store
	provider publisherProvider
	endpoint string
}
type publisherConnectionView struct {
	Configured           bool                           `json:"configured"`
	CredentialAvailable  bool                           `json:"credentialAvailable"`
	ID                   string                         `json:"id"`
	Repository           publishers.Repository          `json:"repository"`
	ImageRepository      string                         `json:"imageRepository"`
	State                string                         `json:"state"`
	Error                string                         `json:"error,omitempty"`
	KeyID                string                         `json:"keyId,omitempty"`
	CreatedAt            string                         `json:"createdAt"`
	VerifiedAt           string                         `json:"verifiedAt,omitempty"`
	LastPublication      *publishers.PublicationReceipt `json:"lastPublication,omitempty"`
	RemoteDisablePending bool                           `json:"remoteDisablePending,omitempty"`
	Deletion             *publisherDeletionView         `json:"deletion,omitempty"`
}

type publisherDeletionView struct {
	PRURL                       string `json:"prUrl"`
	WorkflowVerified            bool   `json:"workflowVerified"`
	TailscaleConfirmed          bool   `json:"tailscaleConfirmed"`
	TailscaleRevocationAttested bool   `json:"tailscaleRevocationAttested"`
}

func connectionView(c publishers.Connection) publisherConnectionView {
	view := publisherConnectionView{Configured: c.Configured, ID: c.ID, Repository: c.Repository, ImageRepository: c.ImageRepository, State: c.State, Error: c.Error, KeyID: c.KeyID, CreatedAt: c.CreatedAt, VerifiedAt: c.VerifiedAt, LastPublication: c.LastPublication, RemoteDisablePending: c.RemoteDisablePending}
	if c.Deletion != nil {
		view.Deletion = &publisherDeletionView{PRURL: c.Deletion.PRURL, WorkflowVerified: c.Deletion.WorkflowVerified, TailscaleConfirmed: c.Deletion.TailscaleConfirmed, TailscaleRevocationAttested: publisherTailscaleRevocationAttested(c)}
	}
	return view
}
func publisherCredentialMatches(key auth.APIKey, c publishers.Connection) bool {
	return key.ID == c.KeyID && len(key.Scopes) == 1 && key.Scopes[0] == "publications:write" && len(key.Repositories) == 1 && key.Repositories[0] == c.ImageRepository
}

func (s *Server) connectionView(c publishers.Connection) publisherConnectionView {
	view := connectionView(c)
	keys, err := s.authStore.ListKeys()
	if err != nil || c.Secret == "" {
		return view
	}
	for _, key := range keys {
		expires, parseErr := time.Parse(time.RFC3339, key.ExpiresAt)
		if publisherCredentialMatches(key, c) && strings.HasPrefix(c.Secret, key.Prefix) && (key.ExpiresAt == "" || (parseErr == nil && expires.After(time.Now()))) {
			view.CredentialAvailable = true
			break
		}
	}
	return view
}

func appCredentials(a publishers.App) githubapp.App {
	return githubapp.App{ID: a.ID, Slug: a.Slug, Name: a.Name, PEM: a.PEM}
}
func providerRepository(r publishers.Repository) githubapp.Repository {
	return githubapp.Repository{ID: r.ID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, InstallationID: r.InstallationID}
}
func storedRepository(r githubapp.Repository) publishers.Repository {
	return publishers.Repository{ID: r.ID, FullName: r.FullName, DefaultBranch: r.DefaultBranch, InstallationID: r.InstallationID}
}
func digestID(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func matchesHash(value, hash string) bool {
	actual := digestID(value)
	return subtle.ConstantTimeCompare([]byte(actual), []byte(hash)) == 1
}
func (s *Server) registerPublisherRoutes() {
	s.mux.HandleFunc("GET /api/publishers", s.publisherStatus)
	s.mux.HandleFunc("POST /api/publishers/github/start", s.publisherGitHubStart)
	s.mux.HandleFunc("POST /api/publishers/github/complete", s.publisherGitHubComplete)
	s.mux.HandleFunc("POST /api/publishers/github/permissions", s.publisherGitHubPermissions)
	s.mux.HandleFunc("PUT /api/publishers/tailnet", s.publisherTailnet)
	s.mux.HandleFunc("POST /api/publishers/tailnet/guidance", s.publisherTailnetGuidance)
	s.mux.HandleFunc("GET /api/publishers/repositories", s.publisherRepositories)
	s.mux.HandleFunc("POST /api/publishers/plan", s.publisherPlan)
	s.mux.HandleFunc("POST /api/publishers/workflow", s.publisherInstallWorkflow)
	s.mux.HandleFunc("POST /api/publishers", s.publisherCreate)
	s.mux.HandleFunc("POST /api/publishers/{id}/test", s.publisherTest)
	s.mux.HandleFunc("POST /api/publishers/{id}/repair/plan", s.publisherRepairReview)
	s.mux.HandleFunc("POST /api/publishers/{id}/repair", s.publisherRepair)
	s.mux.HandleFunc("POST /api/publishers/{id}/recover", s.publisherRecover)
	s.mux.HandleFunc("POST /api/publishers/{id}/rotate", s.publisherRotate)
	s.mux.HandleFunc("POST /api/publishers/{id}/reconnect", s.publisherReconnect)
	s.mux.HandleFunc("POST /api/publishers/{id}/deletion/plan", s.publisherDeletionReview)
	s.mux.HandleFunc("POST /api/publishers/{id}/deletion/workflow", s.publisherDeletionWorkflow)
	s.mux.HandleFunc("POST /api/publishers/{id}/deletion/verify", s.publisherDeletionVerify)
	s.mux.HandleFunc("POST /api/publishers/{id}/deletion/tailscale", s.publisherDeletionTailscale)
	s.mux.HandleFunc("DELETE /api/publishers/{id}", s.publisherRemove)
	s.mux.HandleFunc("POST /api/publishers/{id}/disconnect", s.publisherDisconnect)
	s.mux.HandleFunc("POST /api/publishers/callback", s.publisherCallback)
	s.mux.HandleFunc("GET /github-app-callback", s.publisherLanding)
}

// ConfigurePublishers is startup-only. A disabled integration never weakens auth.
func (s *Server) ConfigurePublishers(store *publishers.Store, provider publisherProvider) error {
	s.BeginPublisherConfiguration()
	if !s.AuthenticationConfigured() || store == nil || provider == nil || s.publicOrigin == nil || s.publicOrigin.scheme != "https" || s.publicationHandler == nil {
		return errors.New("publisher setup requires authentication, persistent storage, publication ingestion, and an HTTPS SERVICES_PUBLIC_ORIGIN")
	}
	if _, err := store.Load(); err != nil {
		return err
	}
	origin := "https://" + s.publicOrigin.host
	if s.publicOrigin.port != "443" {
		origin += ":" + s.publicOrigin.port
	}
	endpoint := origin + "/api/images/published"
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return errors.New("invalid publisher public origin")
	}
	s.publishers = &publisherIntegration{store: store, provider: provider, endpoint: endpoint}
	return nil
}

// BeginPublisherConfiguration fences managed ingestion before attempting to open
// publisher storage. Until metadata loads, managed keys cannot be distinguished
// safely from ordinary keys. Unrelated legacy token ingestion is unaffected.
// This is startup-only, like ConfigurePublishers.
func (s *Server) BeginPublisherConfiguration() {
	s.publisherConfigurationAttempted = true
	s.publishers = nil
}
func (s *Server) publisherAdmin(w http.ResponseWriter, r *http.Request) (*publisherIntegration, bool) {
	if _, ok := r.Context().Value(authUserContext{}).(auth.User); !ok {
		unauthenticated(w)
		return nil, false
	}
	if s.publishers == nil {
		http.Error(w, "Publisher setup requires an HTTPS public origin and writable persistent integration storage with publication ingestion enabled", 503)
		return nil, false
	}
	return s.publishers, true
}
func (s *Server) publisherStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := r.Context().Value(authUserContext{}).(auth.User); !ok {
		unauthenticated(w)
		return
	}
	m := s.publishers
	if m == nil {
		authJSON(w, 200, map[string]any{"available": false, "reason": "Configure an HTTPS SERVICES_PUBLIC_ORIGIN and persistent SERVICES_DATA_DIR with publication ingestion available.", "github": map[string]bool{"connected": false}, "tailnet": publishers.Tailnet{}, "tailnets": map[string]publishers.Tailnet{}, "endpoint": "", "connections": []publisherConnectionView{}})
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	changed := false
	now := time.Now().Unix()
	keys, err := s.authStore.ListKeys()
	if err != nil {
		http.Error(w, "Publisher credential status unavailable", 503)
		return
	}
	activeKeys := make(map[string]bool, len(keys))
	for _, key := range keys {
		expires, parseErr := time.Parse(time.RFC3339, key.ExpiresAt)
		activeKeys[key.ID] = key.ExpiresAt == "" || (parseErr == nil && expires.Unix() > now)
	}
	views := make([]publisherConnectionView, 0, len(state.Connections))
	for i := range state.Connections {
		c := &state.Connections[i]
		if c.State == "configured" && !c.Configured {
			c.State = "error"
			c.Error = "Provisioning was interrupted. Use reviewed repair to reconcile the saved configuration before testing."
			changed = true
		}
		if c.State != "disconnected" && c.Secret != "" {
			if !activeKeys[c.KeyID] {
				c.State = "error"
				c.Error = "Publisher key was revoked or expired. Use reviewed repair to create a fresh credential and reconcile GitHub settings."
				c.VerifiedAt = ""
				changed = true
			}
		}
		if c.State == "awaiting_callback" && c.ChallengeExpires < now {
			c.State = "error"
			c.Error = "No callback received before the test expired. Check the workflow run and Tailscale access, then retry."
			changed = true
		}
		views = append(views, s.connectionView(*c))
	}
	if changed {
		if err = m.store.Save(state); err != nil {
			http.Error(w, "Cannot persist publisher status", 503)
			return
		}
	}
	gh := map[string]any{"connected": state.App.ID > 0}
	if state.App.ID > 0 {
		gh["name"] = state.App.Name
		gh["installUrl"] = "https://github.com/apps/" + url.PathEscape(state.App.Slug) + "/installations/new"
		gh["permissionsChecked"] = state.App.PermissionsChecked
		gh["variablesWrite"] = state.App.VariablesWrite
		gh["settingsUrl"] = state.App.SettingsURL
	}
	if state.Tailnets == nil {
		state.Tailnets = map[string]publishers.Tailnet{}
	}
	authJSON(w, 200, map[string]any{"available": true, "github": gh, "tailnet": state.Tailnet, "tailnets": state.Tailnets, "endpoint": m.endpoint, "connections": views})
}
func (s *Server) publisherGitHubStart(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Organization string `json:"organization,omitempty"`
	}
	if !authDecode(w, r, &body) {
		return
	}
	if body.Organization != "" && !regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`).MatchString(body.Organization) {
		http.Error(w, "Organization must be a GitHub account name, not a URL", 400)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	if state.App.ID != 0 {
		http.Error(w, "A GitHub App is already connected", 409)
		return
	}
	token, err := publishers.RandomID()
	if err != nil {
		http.Error(w, "Cannot create registration state", 503)
		return
	}
	user := r.Context().Value(authUserContext{}).(auth.User)
	state.Pending = publishers.Pending{Hash: digestID(token), UserID: user.ID, Expires: time.Now().Add(10 * time.Minute).Unix()}
	if err = m.store.Save(state); err != nil {
		http.Error(w, "Cannot persist registration state", 503)
		return
	}
	origin := strings.TrimSuffix(m.endpoint, "/api/images/published")
	// GitHub rejects Variables in manifests; grant it after App registration.
	// Setup verifies both the App grant and installation access before writes.
	manifest := map[string]any{"name": "Services publisher " + token[:8], "url": origin, "redirect_url": origin + "/github-app-callback", "public": false, "hook_attributes": map[string]any{"url": origin + "/github-events", "active": false}, "default_permissions": map[string]string{"actions": "write", "contents": "write", "workflows": "write", "pull_requests": "write", "secrets": "write", "metadata": "read"}, "default_events": []string{}}
	data, _ := json.Marshal(manifest)
	action := "https://github.com/settings/apps/new"
	if body.Organization != "" {
		action = "https://github.com/organizations/" + url.PathEscape(body.Organization) + "/settings/apps/new"
	}
	authJSON(w, 200, map[string]string{"action": action + "?state=" + url.QueryEscape(token), "manifest": string(data)})
}
func (s *Server) publisherGitHubComplete(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if !authDecode(w, r, &body) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	user := r.Context().Value(authUserContext{}).(auth.User)
	if state.App.ID != 0 || state.Pending.UserID != user.ID || state.Pending.Expires <= time.Now().Unix() || len(body.State) != 43 || !matchesHash(body.State, state.Pending.Hash) {
		http.Error(w, "Registration expired or not owned by this administrator. Start GitHub connection again.", 403)
		return
	}
	if len(body.Code) < 16 || len(body.Code) > 256 || strings.IndexFunc(body.Code, func(c rune) bool { return c < '0' || c > '9' && c < 'A' || c > 'Z' && c < 'a' || c > 'z' }) >= 0 {
		http.Error(w, "Invalid GitHub registration code", 400)
		return
	}
	state.Pending = publishers.Pending{}
	if err = m.store.Save(state); err != nil {
		http.Error(w, "Cannot consume registration state", 503)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	app, err := m.provider.ExchangeManifest(ctx, body.Code)
	if err != nil {
		http.Error(w, "GitHub registration exchange failed. Start the connection again.", 502)
		return
	}
	state.App = publishers.App{ID: app.ID, Name: app.Name, Slug: app.Slug, PEM: app.PEM}
	if err = m.store.Save(state); err != nil {
		http.Error(w, "Cannot persist GitHub App credentials. Registration was not completed locally.", 503)
		return
	}
	// Credentials are durable. A failed permission lookup must not require
	// exchanging the single-use registration code again.
	if err = m.refreshAppPermissions(ctx, &state); err != nil {
		authJSON(w, 200, map[string]bool{"connected": true, "permissionsChecked": false})
		return
	}
	authJSON(w, 200, map[string]bool{"connected": true, "permissionsChecked": true})
}

// The landing page has no session exception. A same-origin POST after navigation
// preserves SameSite=Strict login cookies without weakening the cookie policy.
func (s *Server) publisherLanding(w http.ResponseWriter, r *http.Request) {
	host, _, ok := normalizedAuthority(r.Host, "http")
	if !ok || !s.allowedOriginHost(host) {
		http.Error(w, "Untrusted callback host", 403)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	nonce, err := publishers.RandomID()
	if err != nil {
		http.Error(w, "Callback unavailable", 503)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Connect GitHub · Services</title><h1>Connecting GitHub</h1><p id="status" role="status">Completing registration…</p><p><a href="/#publishers">Return to Services publishers</a></p><script nonce="%s">const query=new URLSearchParams(location.search);const body={code:query.get('code')||'',state:query.get('state')||''};history.replaceState(null,'','/github-app-callback');fetch('/api/publishers/github/complete',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)}).then(async response=>{if(!response.ok)throw new Error(response.status===401?'Sign in to Services, then start the GitHub connection again.':'GitHub connection could not be completed. Return to Services and start again.');location.replace('/#publishers')}).catch(error=>{document.getElementById('status').textContent=error.message});</script></html>`, nonce)
}
func validTailnet(t publishers.Tailnet) bool {
	if len(t.ClientID) < 1 || len(t.ClientID) > 256 || len(t.Audience) < 1 || len(t.Audience) > 512 || len(t.Tags) > 512 {
		return false
	}
	for _, v := range []string{t.ClientID, t.Audience, t.Tags} {
		if strings.TrimSpace(v) != v || strings.IndexFunc(v, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
			return false
		}
	}
	tags := strings.Split(t.Tags, ",")
	if len(tags) > 10 {
		return false
	}
	for _, tag := range tags {
		if !regexp.MustCompile(`^tag:[a-zA-Z0-9][a-zA-Z0-9-]*$`).MatchString(tag) {
			return false
		}
	}
	return true
}
func (s *Server) publisherTailnet(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var input struct {
		publishers.Tailnet
		Repository *githubapp.Repository `json:"repository,omitempty"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	if !validTailnet(input.Tailnet) {
		http.Error(w, "Client ID, audience, and comma-separated Tailscale tags are required (no spaces)", 400)
		return
	}
	m.mu.Lock()
	state, err := m.store.Load()
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if input.Repository != nil {
			if _, accessErr := m.resolveRepository(ctx, &state, *input.Repository); accessErr != nil {
				m.mu.Unlock()
				http.Error(w, accessErr.Error(), 403)
				return
			}
		}
		for _, c := range state.Connections {
			// Legacy trust is frozen for all saved connections. Scoped trust is
			// frozen while a live connection (including interrupted setup) owns it.
			if (input.Repository == nil && state.Tailnet != input.Tailnet) || (input.Repository != nil && (c.State != "disconnected" || c.Configured) && c.Repository.ID == input.Repository.ID && repositoryTailnet(state, c.Repository.ID) != input.Tailnet) {
				m.mu.Unlock()
				http.Error(w, "Existing publishers use these settings. Reconfiguration is not yet supported.", 409)
				return
			}
		}
		if input.Repository == nil {
			state.Tailnet = input.Tailnet
		} else {
			if state.Tailnets == nil {
				state.Tailnets = map[string]publishers.Tailnet{}
			}
			state.Tailnets[strconv.FormatInt(input.Repository.ID, 10)] = input.Tailnet
		}
		err = m.store.Save(state)
	}
	m.mu.Unlock()
	if err != nil {
		http.Error(w, "Cannot persist Tailscale settings", 503)
		return
	}
	s.publisherStatus(w, r)
}
func (s *Server) publisherRepositories(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	if state.App.ID == 0 {
		http.Error(w, "Connect and install the GitHub App first", 409)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err = m.requireAppPermissions(ctx, &state); err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	repos, err := m.provider.Repositories(ctx, appCredentials(state.App))
	if err != nil {
		http.Error(w, "Cannot list GitHub repositories. Check App installation and permissions.", 502)
		return
	}
	if repos == nil {
		repos = []githubapp.Repository{}
	}
	authJSON(w, 200, map[string]any{"repositories": repos})
}

type publisherSetupRequest struct {
	Repository      githubapp.Repository `json:"repository"`
	ImageRepository string               `json:"imageRepository"`
	Approval        string               `json:"approval,omitempty"`
}

func (m *publisherIntegration) resolve(ctx context.Context, state *publishers.Snapshot, input publisherSetupRequest) (githubapp.Repository, error) {
	if state.App.ID == 0 || !validTailnet(repositoryTailnet(*state, input.Repository.ID)) {
		return githubapp.Repository{}, errors.New("Connect GitHub and register Tailscale federation first")
	}
	if len(input.ImageRepository) > 255 || !publicationRepository.MatchString(input.ImageRepository) {
		return githubapp.Repository{}, errors.New("An exact untagged lowercase GHCR image repository is required")
	}
	return m.resolveRepository(ctx, state, input.Repository)
}

func (m *publisherIntegration) resolveRepository(ctx context.Context, state *publishers.Snapshot, input githubapp.Repository) (githubapp.Repository, error) {
	if state.App.ID == 0 {
		return githubapp.Repository{}, errors.New("Connect and install the GitHub App first")
	}
	if err := m.requireAppPermissions(ctx, state); err != nil {
		return githubapp.Repository{}, err
	}
	repos, err := m.provider.Repositories(ctx, appCredentials(state.App))
	if err != nil {
		return githubapp.Repository{}, errors.New("Cannot verify GitHub repository access")
	}
	for _, repo := range repos {
		if repo == input {
			if err := m.provider.VerifyRepositoryPermissions(ctx, appCredentials(state.App), repo); err != nil {
				return githubapp.Repository{}, err
			}
			return repo, nil
		}
	}
	return githubapp.Repository{}, errors.New("Repository or installation is not currently authorized; refresh the repository list")
}

// The global value predates repository scoping. Only a saved connection may
// inherit it; merely discovering or selecting another repository grants nothing.
func repositoryTailnet(state publishers.Snapshot, repositoryID int64) publishers.Tailnet {
	if t, ok := state.Tailnets[strconv.FormatInt(repositoryID, 10)]; ok {
		return t
	}
	for _, c := range state.Connections {
		if c.Repository.ID == repositoryID {
			return state.Tailnet
		}
	}
	return publishers.Tailnet{}
}

func (m *publisherIntegration) variables(state publishers.Snapshot, repo githubapp.Repository) map[string]string {
	t := repositoryTailnet(state, repo.ID)
	return map[string]string{"SERVICES_PUBLISH_URL": m.endpoint, "SERVICES_PUBLISH_NOTIFICATIONS": "false", "TAILSCALE_CLIENT_ID": t.ClientID, "TAILSCALE_AUDIENCE": t.Audience, "TAILSCALE_TAGS": t.Tags}
}
func (s *Server) publisherPlan(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var input publisherSetupRequest
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
	repo, err := m.resolve(ctx, &state, input)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	inspection, err := m.provider.Inspect(ctx, appCredentials(state.App), repo)
	if err != nil {
		http.Error(w, "Cannot inspect the publishing workflow", 502)
		return
	}
	if inspection.Problems == nil {
		inspection.Problems = []string{}
	}
	workflow, err := m.workflowPlan(ctx, state, repo)
	if err != nil {
		http.Error(w, "Cannot prepare a workflow plan", 502)
		return
	}
	response := map[string]any{"supported": inspection.Supported, "problems": inspection.Problems, "repository": repo, "imageRepository": input.ImageRepository, "variables": m.variables(state, repo), "secretName": "SERVICES_PUBLISH_TOKEN", "approval": publisherApproval(repo, input.ImageRepository, m.variables(state, repo), workflow)}
	if workflow != nil {
		response["workflow"] = workflow
	}
	authJSON(w, 200, response)
}
func (s *Server) publisherCreate(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var input publisherSetupRequest
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
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	repo, err := m.resolve(ctx, &state, input)
	if err != nil {
		http.Error(w, err.Error(), 403)
		return
	}
	for _, c := range state.Connections {
		if c.Repository.ID == repo.ID && c.State != "disconnected" {
			http.Error(w, "This repository already has a saved connection. Use its test action or reviewed repair for provisioning failures.", 409)
			return
		}
	}
	if len(state.Connections) >= 100 {
		http.Error(w, "Publisher connection limit reached", 409)
		return
	}
	inspection, err := m.provider.Inspect(ctx, appCredentials(state.App), repo)
	if err != nil {
		http.Error(w, "Cannot inspect GitHub workflow", 502)
		return
	}
	if !inspection.Supported {
		http.Error(w, "Review and merge the setup pull request before applying repository configuration.", 409)
		return
	}
	workflow, err := m.workflowPlan(ctx, state, repo)
	if err != nil {
		http.Error(w, "Cannot verify the reviewed workflow", 502)
		return
	}
	if input.Approval != publisherApproval(repo, input.ImageRepository, m.variables(state, repo), workflow) {
		http.Error(w, "Configuration changed or was not reviewed. Review setup again before applying.", 409)
		return
	}
	id, err := publishers.RandomID()
	if err != nil {
		http.Error(w, "Cannot create connection", 503)
		return
	}
	key, secret, err := s.authStore.CreateKey("GitHub publisher "+id[:12], []string{"publications:write"}, []string{input.ImageRepository}, "")
	if err != nil {
		http.Error(w, "Cannot create scoped publisher key", 503)
		return
	}
	c := publishers.Connection{ID: id, Repository: storedRepository(repo), ImageRepository: input.ImageRepository, State: "configured", KeyID: key.ID, Secret: secret, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	state.Connections = append(state.Connections, c)
	index := len(state.Connections) - 1
	// Retain the key's identity before any remote write. Unknown partial setup is
	// never repaired by blindly overwriting an existing GitHub secret.
	if err = m.store.Save(state); err != nil {
		_ = s.authStore.RevokeKey(key.ID)
		http.Error(w, "Cannot persist publisher setup", 503)
		return
	}
	if err = m.provider.Configure(ctx, appCredentials(state.App), repo, secret, m.variables(state, repo)); err != nil {
		state.Connections[index].State = "error"
		state.Connections[index].Error = "GitHub provisioning was not completed. Use reviewed repair to approve and reconcile repository configuration."
		if errors.Is(err, githubapp.ErrConflict) {
			state.Connections[index].Error = "GitHub configuration conflicts with existing settings. No conflicting value was overwritten. Use reviewed repair to approve replacing publisher settings and delivering a fresh credential."
			if revokeErr := s.authStore.RevokeKey(key.ID); revokeErr != nil {
				http.Error(w, "Cannot revoke the failed setup credential. Revoke it from Accounts before retrying.", 503)
				return
			}
			state.Connections[index].Secret = ""
		}
		if saveErr := m.store.Save(state); saveErr != nil {
			http.Error(w, "Cannot persist failed provisioning status. Review saved setup before retrying.", 503)
			return
		}
		http.Error(w, "GitHub configuration was not completed. New setup never overwrites existing secrets. Use reviewed repair on the saved connection to approve reconciliation.", 409)
		return
	}
	state.Connections[index].Configured = true
	if err = m.store.Save(state); err != nil {
		http.Error(w, "GitHub configuration may have changed but local completion could not be persisted. Review configuration before retrying.", 503)
		return
	}
	if err = m.dispatch(ctx, &state, index); err != nil {
		http.Error(w, "Connection was saved, but the test could not be dispatched. Retry from the connection card.", 502)
		return
	}
	authJSON(w, 201, map[string]any{"connection": s.connectionView(state.Connections[index])})
}
func (m *publisherIntegration) dispatch(ctx context.Context, state *publishers.Snapshot, index int) error {
	c := &state.Connections[index]
	challenge, err := publishers.RandomID()
	if err != nil {
		return err
	}
	c.State = "awaiting_callback"
	c.Error = ""
	c.VerifiedAt = ""
	c.ChallengeHash = digestID(challenge)
	c.ChallengeExpires = time.Now().Add(10 * time.Minute).Unix()
	c.ChallengeUsed = false
	c.RunID = ""
	if err = m.store.Save(*state); err != nil {
		return err
	}
	if err = m.provider.DispatchTest(ctx, appCredentials(state.App), providerRepository(c.Repository), c.ID, challenge); err != nil {
		c.State = "error"
		c.Error = "GitHub test dispatch was not acknowledged. Inspect Actions before retrying; the test may still have started."
		if saveErr := m.store.Save(*state); saveErr != nil {
			return saveErr
		}
		return err
	}
	return nil
}
func (s *Server) publisherTest(w http.ResponseWriter, r *http.Request) {
	m, ok := s.publisherAdmin(w, r)
	if !ok {
		return
	}
	var body struct{}
	if !authDecode(w, r, &body) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state, err := m.store.Load()
	if err != nil {
		http.Error(w, "Publisher storage unavailable", 503)
		return
	}
	for i, c := range state.Connections {
		if c.ID != r.PathValue("id") {
			continue
		}
		if c.State == "disconnected" {
			http.Error(w, "This publisher is disconnected", 409)
			return
		}
		if publisherTailscaleRevocationAttested(c) {
			http.Error(w, "Tailscale revocation was confirmed; complete publisher deletion instead of dispatching a test", 409)
			return
		}
		if !c.Configured || c.Secret == "" {
			http.Error(w, "GitHub provisioning is incomplete. Use reviewed repair to approve and reconcile publisher settings.", 409)
			return
		}
		if _, err = s.authStore.AuthenticateKey(c.Secret); err != nil {
			http.Error(w, "Publisher key was revoked or expired. Use reviewed repair to create a fresh credential.", 409)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()
		if err = m.dispatch(ctx, &state, i); err != nil {
			http.Error(w, "Cannot dispatch connection test; inspect the saved status and GitHub Actions", 502)
			return
		}
		authJSON(w, 200, map[string]any{"connection": s.connectionView(state.Connections[i])})
		return
	}
	http.NotFound(w, r)
}

type publisherCallbackRequest struct {
	ConnectionID     string `json:"connectionId"`
	Challenge        string `json:"challenge"`
	SourceRepository string `json:"sourceRepository"`
	Ref              string `json:"ref"`
	RunID            string `json:"runId"`
}

func (s *Server) publisherCallback(w http.ResponseWriter, r *http.Request) {
	key, ok := r.Context().Value(authKeyContext{}).(auth.APIKey)
	if !ok {
		http.Error(w, "A managed publisher key is required", 403)
		return
	}
	m := s.publishers
	if m == nil {
		http.Error(w, "Publisher setup unavailable", 503)
		return
	}
	var input publisherCallbackRequest
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
	for i := range state.Connections {
		c := &state.Connections[i]
		if c.ID != input.ConnectionID {
			continue
		}
		allowed := false
		for _, repo := range key.Repositories {
			allowed = allowed || repo == c.ImageRepository
		}
		if publisherTailscaleRevocationAttested(*c) || !c.Configured || key.ID != c.KeyID || !allowed || len(input.Challenge) != 43 || !matchesHash(input.Challenge, c.ChallengeHash) || c.ChallengeExpires <= time.Now().Unix() || input.SourceRepository != c.Repository.FullName || input.Ref != "refs/heads/"+c.Repository.DefaultBranch || !regexp.MustCompile(`^[1-9][0-9]{0,19}$`).MatchString(input.RunID) || (c.ChallengeUsed && c.RunID != input.RunID) {
			http.Error(w, "Push challenge is expired or outside the connection's scope", 403)
			return
		}
		if c.ChallengeUsed && c.State == "verified" {
			authJSON(w, 200, map[string]string{"result": "duplicate"})
			return
		}
		// Persist the received proof before enabling remotely; retrying this exact
		// challenge/run can finish a failed readback without creating another key.
		c.ChallengeUsed = true
		c.RunID = input.RunID
		if err = m.store.Save(state); err != nil {
			http.Error(w, "Cannot persist push proof", 503)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err = m.provider.SetEnabled(ctx, appCredentials(state.App), providerRepository(c.Repository), true); err != nil {
			c.State = "error"
			c.Error = "Push callback received, but enabling notifications was not verified. Retry the connection test."
			_ = m.store.Save(state)
			http.Error(w, "Cannot verify notification activation", 503)
			return
		}
		c.State = "verified"
		c.Error = ""
		c.VerifiedAt = time.Now().UTC().Format(time.RFC3339)
		if c.PreviousKeyID != "" {
			if err = s.revokePublisherKey(c.PreviousKeyID); err != nil {
				http.Error(w, "Replacement verified, but previous key revocation failed. Retry this callback.", 503)
				return
			}
			c.PreviousKeyID = ""
			c.PreviousSecret = ""
		}
		if err = m.store.Save(state); err != nil {
			http.Error(w, "Cannot persist verified connection", 503)
			return
		}
		authJSON(w, 200, map[string]string{"result": "verified"})
		return
	}
	http.Error(w, "Unknown publisher connection", 403)
}
